package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Download and relay limits.
const (
	// MaxUnverifiedBytes caps fetch.proxy downloads without an expected
	// sha256 (SHA256SUMS, signatures, manifests: the hub verifies their
	// signature).
	MaxUnverifiedBytes = 16 << 20
	// TelegramHost is the only host http.post may reach.
	TelegramHost = "api.telegram.org"
	// MaxHTTPPostBody caps the body http.post sends.
	MaxHTTPPostBody = 1 << 20
	// maxHTTPPostAnswer caps the answer http.post returns.
	maxHTTPPostAnswer = 64 << 10
	// httpPostTimeout bounds one http.post when the command has no deadline.
	httpPostTimeout = 20 * time.Second
)

// backendInstall answers backend.install: the pinned release is
// downloaded (nodes have internet), verified against the manifest sha256
// and installed side by side under /var/lib/deyroute/bin/<b>/<ver>/.
func (a *agent) backendInstall(ctx context.Context, args api.BackendInstallArgs) (api.BackendInstallResult, error) {
	e := args.Entry
	if e.Name == "" {
		e.Name = args.Name
	}
	if args.Name != "" && e.Name != args.Name {
		return api.BackendInstallResult{}, a.refuse(api.CmdBackendInstall, fmt.Sprintf("entry %q does not match backend %q", e.Name, args.Name))
	}
	if !backendNameRe.MatchString(e.Name) {
		return api.BackendInstallResult{}, a.refuse(api.CmdBackendInstall, fmt.Sprintf("invalid backend name %q", e.Name))
	}
	f := install.HTTPFetcher{Client: a.o.HTTPClient}
	dir, err := a.o.Layout.InstallBackend(ctx, e, a.o.Arch, f, a.o.Mirror)
	if err != nil {
		return api.BackendInstallResult{}, err
	}
	if dir == "" { // builtin or system backend: nothing to install
		return api.BackendInstallResult{}, nil
	}
	sys := install.Layout{Root: "/"}
	a.log.Info("backend installed", slog.String("backend", e.Name), slog.String("version", e.Version))
	return api.BackendInstallResult{
		BinDir: sys.BinDir(e.Name, e.Version),
		Binary: sys.BinaryPath(e.Name, e.Version, e.Binary()),
	}, nil
}

// checkHTTPS accepts only absolute https URLs without credentials.
func checkHTTPS(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return nil, false
	}
	return u, true
}

// fetchProxy answers fetch.proxy (section 5, GitHub from Iran): download
// URL (https only), verify its sha256 (when given), then upload it to the
// hub's pending upload id. Nothing downloaded is ever executed here.
func (a *agent) fetchProxy(ctx context.Context, args api.FetchArgs) (api.FetchResult, error) {
	const cmd = api.CmdFetchProxy
	u, ok := checkHTTPS(args.URL)
	if !ok {
		return api.FetchResult{}, a.refuse(cmd, "only https URLs are downloaded")
	}
	shown := install.RedactURL(args.URL)
	file := path.Base(u.Path)
	if file == "." || file == "/" || file == "" {
		file = u.Host
	}
	want := strings.ToLower(strings.TrimSpace(args.SHA256))
	if want != "" && !validHex(want, sha256.Size) {
		return api.FetchResult{}, a.refuse(cmd, "invalid sha256")
	}
	client, err := a.requestClient()
	if err != nil {
		return api.FetchResult{}, err
	}
	ro := a.o.DownloadRetry
	ro.File = file
	ro.MismatchCode = deyerr.S001
	ro.FailCode = deyerr.N051
	ro.Params = deyerr.Params{"node": a.nodeID}
	var f install.HTTPFetcher
	var cleanup func()

	var body io.Reader
	var size int64
	var sum string
	if want == "" {
		limit := args.MaxBytes
		if limit <= 0 || limit > MaxUnverifiedBytes {
			limit = MaxUnverifiedBytes
		}
		ro.MaxBytes = limit
		f.MaxBytes = limit
		f.Client, cleanup = a.downloadClient()
		defer cleanup()
		data, _, err := install.FetchBytes(ctx, f, []string{args.URL}, ro)
		if err != nil {
			return api.FetchResult{}, err
		}
		h := sha256.Sum256(data)
		body, size, sum = bytes.NewReader(data), int64(len(data)), hex.EncodeToString(h[:])
	} else {
		limit := args.MaxBytes
		if limit <= 0 {
			limit = install.DefaultMaxBytes
		}
		ro.MaxBytes = limit
		f.MaxBytes = limit
		f.Client, cleanup = a.downloadClient()
		defer cleanup()
		dir := a.path(DownloadDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return api.FetchResult{}, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
		}
		tmpDir, err := os.MkdirTemp(dir, fetchStagePrefix)
		if err != nil {
			return api.FetchResult{}, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		dst := filepath.Join(tmpDir, "payload")
		if err := install.FetchVerified(ctx, f, []string{args.URL}, want, dst, ro); err != nil {
			return api.FetchResult{}, err
		}
		fh, err := os.Open(dst) // #nosec G304 -- our own temp file
		if err != nil {
			return api.FetchResult{}, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
		}
		defer func() { _ = fh.Close() }()
		fi, err := fh.Stat()
		if err != nil {
			return api.FetchResult{}, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
		}
		body, size, sum = fh, fi.Size(), want
	}
	if err := client.Upload(ctx, args.UploadID, body); err != nil {
		return api.FetchResult{}, err
	}
	a.log.Info("file fetched for the hub", slog.String("url", shown), slog.Int64("bytes", size))
	return api.FetchResult{Bytes: size, SHA256: sum}, nil
}

// Name prefixes of the staging entries in DownloadDir.
const (
	fetchStagePrefix  = "fetch-"
	updateStagePrefix = "deyroute-update-"
)

// cleanDownloads removes staging files a previous agent left behind when
// it stopped in the middle of a fetch.proxy or self.update download, so
// repeated crashes cannot fill the disk.
func (a *agent) cleanDownloads() {
	dir := a.path(DownloadDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, fetchStagePrefix) && !strings.HasPrefix(n, updateStagePrefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			a.log.Warn("cannot remove a stale download", slog.String("path", filepath.Join(dir, n)), dlog.Err(err))
		}
	}
}

// maxRedirects bounds the redirects of one fetch.proxy download (GitHub
// release assets redirect once to their storage host).
const maxRedirects = 10

// downloadClient returns the fetch.proxy HTTP client: Options.HTTPClient
// (or a proxy-aware default) that follows redirects only to https URLs, so
// a download that starts on https never continues in plain text. cleanup
// releases the default client's idle connections.
func (a *agent) downloadClient() (client *http.Client, cleanup func()) {
	base := a.o.HTTPClient
	cleanup = func() {}
	if base == nil {
		base = install.NewHTTPClient()
		cleanup = base.CloseIdleConnections
	}
	c := *base
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return install.Permanent(fmt.Errorf("stopped after %d redirects", maxRedirects))
		}
		if req.URL.Scheme != "https" {
			return install.Permanent(fmt.Errorf("redirect to a non-https URL (%s) refused", req.URL.Scheme))
		}
		return nil
	}
	return &c, cleanup
}

func validHex(s string, n int) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == n
}

// httpPost answers http.post: the hub's Telegram fallback (QUESTIONS.md
// C.18). Only https://api.telegram.org is reachable this way; the bot token
// in the URL path is registered as a secret and never logged or returned.
func (a *agent) httpPost(ctx context.Context, args api.HTTPPostArgs) (api.HTTPPostResult, error) {
	const cmd = api.CmdHTTPPost
	u, ok := checkHTTPS(args.URL)
	if !ok || u.Hostname() != TelegramHost || (u.Port() != "" && u.Port() != "443") {
		return api.HTTPPostResult{}, a.refuse(cmd, "only https://"+TelegramHost+" is allowed")
	}
	registerBotToken(u.Path)
	if len(args.Body) > MaxHTTPPostBody {
		return api.HTTPPostResult{}, a.refuse(cmd, fmt.Sprintf("body of %d bytes (at most %d)", len(args.Body), MaxHTTPPostBody))
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, httpPostTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(args.Body))
	if err != nil {
		return api.HTTPPostResult{}, a.refuse(cmd, "invalid request")
	}
	ct := args.ContentType
	if ct == "" {
		ct = "application/json"
	}
	req.Header.Set("Content-Type", ct)
	base := a.o.HTTPClient
	if base == nil {
		base = install.NewHTTPClient()
		defer base.CloseIdleConnections()
	}
	// A redirect could lead the POST (and its token) to another host.
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error carries the URL (and so the token): report only the cause.
		var ue *url.Error
		if stderrors.As(err, &ue) {
			err = ue.Err
		}
		return api.HTTPPostResult{}, deyerr.New(deyerr.X050, deyerr.Params{"reason": dlog.Redact(health.Classify(err))})
	}
	defer func() { _ = resp.Body.Close() }()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTTPPostAnswer))
	return api.HTTPPostResult{Status: resp.StatusCode, Body: dlog.Redact(string(answer))}, nil
}

// registerBotToken registers the token of a Telegram API path
// (/bot<token>/<method>) with the log redactor.
func registerBotToken(p string) {
	seg := strings.TrimPrefix(p, "/")
	seg, _, _ = strings.Cut(seg, "/")
	if tok, ok := strings.CutPrefix(seg, "bot"); ok && tok != "" {
		dlog.RegisterSecret(tok)
	}
}

// selfUpdate answers self.update (section 5: nodes always update from the
// hub): download the hub's deyroute binary for this architecture, verify it,
// install it keeping deyroute.prev, then restart the service after the
// answer was sent. Tunnel units are separate processes and keep running.
func (a *agent) selfUpdate(ctx context.Context, args api.SelfUpdateArgs) error {
	client, err := a.requestClient()
	if err != nil {
		return err
	}
	dir := a.path(DownloadDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmp, err := os.CreateTemp(dir, updateStagePrefix+"*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name)
	}()
	meta, err := client.FetchAsset(ctx, a.o.Arch, tmp)
	if err != nil {
		return err
	}
	file := "deyroute_linux_" + a.o.Arch
	if want := strings.ToLower(strings.TrimSpace(args.SHA256)); want != "" && want != meta.SHA256 {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": file}).
			WithDetail("the hub announced sha256 " + want + " but served " + meta.SHA256)
	}
	if args.Version != "" && meta.Version != "" && strings.TrimPrefix(args.Version, "v") != strings.TrimPrefix(meta.Version, "v") {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": file}).
			WithDetail("the hub announced version " + args.Version + " but served " + meta.Version)
	}
	if meta.Size == 0 {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": file}).WithDetail("the hub served an empty file")
	}
	if err := tmp.Sync(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": name})
	}
	if err := tmp.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": name})
	}
	// A binary this node cannot execute would leave it restarting into a
	// dead service, unreachable for the hub: refuse it before installing.
	verify := a.o.VerifyBinary
	if verify == nil {
		verify = VerifyELF
	}
	if err := verify(name, a.o.Arch); err != nil {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": file}).
			WithWhy("the hub served a file this node cannot run: " + err.Error()).
			WithDetail("nothing was installed; the running binary stays")
	}
	if err := (install.SelfUpdater{Root: a.o.Root}).Install(name); err != nil {
		return err
	}
	a.log.Info("deyroute binary updated; restarting the node service", slog.String("version", meta.Version))
	a.later(a.o.RestartDelay, a.restartSelf)
	return nil
}

// elfMachines maps GOARCH values to ELF machine types.
var elfMachines = map[string]elf.Machine{
	"amd64":   elf.EM_X86_64,
	"arm64":   elf.EM_AARCH64,
	"386":     elf.EM_386,
	"arm":     elf.EM_ARM,
	"riscv64": elf.EM_RISCV,
	"ppc64le": elf.EM_PPC64,
	"s390x":   elf.EM_S390,
}

// VerifyELF reports whether the file at p is a Linux ELF executable for
// arch (a GOARCH value; an unknown arch only needs a valid executable).
// It is the default Options.VerifyBinary of self.update.
func VerifyELF(p, arch string) error {
	f, err := elf.Open(p)
	if err != nil {
		return fmt.Errorf("not an ELF executable (%v)", err)
	}
	defer func() { _ = f.Close() }()
	if f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN {
		return fmt.Errorf("ELF type %s is not an executable", f.Type)
	}
	if want, ok := elfMachines[arch]; ok && f.Machine != want {
		return fmt.Errorf("built for %s, this node is %s", f.Machine, arch)
	}
	return nil
}

// restartSelf restarts deyroute-node without waiting (systemd stops this
// process, then starts the new binary).
func (a *agent) restartSelf(ctx context.Context) error {
	if a.o.RestartSelf != nil {
		return a.o.RestartSelf(ctx)
	}
	_, _, err := a.o.Runner.Run(ctx, "systemctl", []string{"restart", "--no-block", systemd.NodeUnit}, nil)
	return err
}
