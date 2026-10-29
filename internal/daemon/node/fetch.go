package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
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
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
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
//
// The node installs only what a signed DEYROUTE release pins (DR-03): the
// entry must equal, in version, URL and sha256 of this node's
// architecture, the entry in the backends.yaml built into this node, or in
// the backends.yaml the hub sent along (args.Manifest) when its signature
// (args.ManifestSig) verifies with the release key built into this binary
// (install.CheckBackendEntry). An older hub sends no manifest: then only
// the built-in entries are installed. Anything else is refused before a
// download. Builtin and system backends install nothing and are not
// checked, and neither is a version already installed exactly as the
// entry pins it (Layout.HasBackend): nothing is installed then. A hub keeps
// the version a running tunnel uses after it was updated, until `deyroute
// update backends`, and this node's built-in manifest, updated with the
// hub, may no longer pin it.
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
	var pinnedBy string
	switch {
	case e.Builtin || e.System:
	case a.o.Layout.HasBackend(e, a.o.Arch):
		pinnedBy = "already installed"
	default:
		src, err := install.CheckBackendEntry(e, a.o.Arch, backend.EmbeddedManifest(), args.Manifest, args.ManifestSig, "")
		if err != nil {
			a.log.Warn("backend installation refused: no signed manifest pins it", slog.String("backend", e.Name),
				slog.String("version", e.Version), dlog.Err(err))
			return api.BackendInstallResult{}, a.refuse(api.CmdBackendInstall, err.Error())
		}
		pinnedBy = src
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
	a.log.Info("backend installed", slog.String("backend", e.Name), slog.String("version", e.Version),
		slog.String("pinned_by", pinnedBy))
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

// httpPost answers http.post: the hub's first Telegram path (QUESTIONS.md
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

// nothingInstalled is the detail of a refused self.update.
const nothingInstalled = "nothing was installed; the running binary stays"

// selfUpdate answers self.update (section 5: nodes always update from the
// hub): install the hub's version, keeping deyroute.prev, then restart the
// service after the answer was sent. Tunnel units are separate processes
// and keep running.
//
// The node installs only what a signed DEYROUTE release proves (DR-03): it
// fetches the hub's release of that version and checks it with the release
// key built into this binary (selfUpdateSigned). A hub that cannot serve
// one gets a refusal: a development build DEY-S012, an older hub (no
// api.FeatureSignedUpdate in its hello) DEY-S001. Only a node whose
// config.yaml sets node.allow_unsigned_updates (test labs) takes the hub's
// binary then (selfUpdateUnsigned); while that key is set a version older
// than its first release is refused (DEY-S011).
func (a *agent) selfUpdate(ctx context.Context, args api.SelfUpdateArgs) error {
	ver := strings.TrimPrefix(strings.TrimSpace(args.Version), "v")
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return err
	}
	if err := refuseOlderThanNodeKeys(cfg, ver); err != nil {
		return err
	}
	allowUnsigned := cfg.Node != nil && cfg.Node.AllowUnsignedUpdates
	hub := a.currentHubHello()
	signedHub := hub.HasFeature(api.FeatureSignedUpdate)
	switch {
	case signedHub && (ver == "" || install.IsReleaseVersion(ver)):
		return a.selfUpdateSigned(ctx, ver, args.SHA256)
	case allowUnsigned:
		a.log.Warn("installing a deyroute binary that no signed release proves (node.allow_unsigned_updates)",
			slog.String("version", ver))
		return a.selfUpdateUnsigned(ctx, args)
	case signedHub:
		return deyerr.New(deyerr.S012, deyerr.Params{"version": ver})
	}
	return deyerr.New(deyerr.S001, deyerr.Params{"file": install.ArchiveName(firstNonEmptyString(ver, "latest"), a.o.Arch)}).
		WithWhy("the hub runs an older deyroute that serves no signed release, so this node cannot check what it would install; update the hub first (deyroute update on the hub), then the nodes follow").
		WithDetail(nothingInstalled)
}

// currentHubHello is the hub's hello on the current connection (zero
// before it arrived).
func (a *agent) currentHubHello() api.Hello {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hubHello == nil {
		return api.Hello{}
	}
	return *a.hubHello
}

// refuseOlderThanNodeKeys returns DEY-S011 when this node's config.yaml
// sets a key (node.*, security.*) that the binary of target does not know
// (its first release, config.NewerKeysInUse, is newer than target): that
// binary would refuse config.yaml (DEY-C001) and the node could not start
// again, out of the hub's reach. A target that is not older than this build knows every
// key this build knows, and a target that is not a release version cannot
// be compared; neither is refused here.
func refuseOlderThanNodeKeys(cfg *config.Config, target string) error {
	if c, ok := install.CompareVersions(target, version.Version); ok && c >= 0 {
		return nil
	}
	var keys []string
	for _, k := range cfg.NewerKeysInUse() {
		// The keys a node's config.yaml can hold: node.* and security.*
		// (security.firewall_reassert_s, DR-14).
		if !strings.HasPrefix(k.Key, "node.") && !strings.HasPrefix(k.Key, "security.") {
			continue
		}
		if c, ok := install.CompareVersions(target, k.Since); ok && c < 0 {
			keys = append(keys, k.String())
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return deyerr.New(deyerr.S011, deyerr.Params{"action": "install deyroute " + target, "keys": strings.Join(keys, ", ")}).
		WithWhy("version " + target + " is older than the first release that knows them: it would refuse this node's config.yaml (DEY-C001), and this node could not start again, out of the hub's reach").
		WithFix("remove these keys from /etc/deyroute/config.yaml on this node (node.allow_unsigned_updates is for test labs only), then let the hub update it again").
		WithDetail(nothingInstalled)
}

// manifestSHA256 is the hex sha256 of a backends.yaml (Hello.ManifestSHA256).
func manifestSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// firstNonEmptyString returns the first non-empty value.
func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// selfUpdateSigned installs the deyroute binary of the hub's signed release
// of ver: the release files (GET /v1/assets/release/{arch}) are staged in
// the root-only DownloadDir, SHA256SUMS.minisig must verify SHA256SUMS
// with the release key built into this binary (install.MinisignPublicKey,
// or the lab's install.TestPublicKey), the signed SHA256SUMS must list
// deyroute_<ver>_linux_<arch>.tar.gz and the archive must have that
// sha256 (install.VerifyBundle), and only its regular file deyroute is
// extracted (install.ExtractReleaseBinary) and checked to run here. Every
// refusal is DEY-S001 naming the failed check; nothing is installed.
// announced is the archive's sha256 the hub named ("" = none): it only
// catches a broken transfer.
func (a *agent) selfUpdateSigned(ctx context.Context, ver, announced string) error {
	archive := install.ArchiveName(ver, a.o.Arch)
	if ver == "" {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": install.SumsFile}).
			WithWhy("the hub named no version to check its release against").WithDetail(nothingInstalled)
	}
	client, err := a.requestClient()
	if err != nil {
		return err
	}
	dir := a.path(DownloadDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	stage, err := os.MkdirTemp(dir, updateStagePrefix+"*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	defer func() { _ = os.RemoveAll(stage) }()
	wanted := map[string]bool{}
	for _, f := range install.BundleFiles(ver, a.o.Arch) {
		wanted[f] = true
	}
	served, err := client.FetchRelease(ctx, a.o.Arch, func(name string, r io.Reader) error {
		if !wanted[name] {
			return nil // a file of the release self.update does not need
		}
		return writeStaged(filepath.Join(stage, name), r)
	})
	if err != nil {
		return err
	}
	refuse := func(err error) error {
		de := deyerr.As(err)
		if de.Detail == "" {
			de = de.WithDetail(nothingInstalled)
		}
		return de
	}
	if served = strings.TrimPrefix(served, "v"); served != "" && served != ver {
		return refuse(deyerr.New(deyerr.S001, deyerr.Params{"file": archive}).
			WithWhy("the hub announced version " + ver + " but served the release of " + served))
	}
	if want := strings.ToLower(strings.TrimSpace(announced)); want != "" {
		if got, err := install.SHA256File(filepath.Join(stage, archive)); err == nil && got != want {
			return refuse(deyerr.New(deyerr.S001, deyerr.Params{"file": archive}).
				WithWhy("the hub announced sha256 " + want + " for " + archive + " but served " + got))
		}
	}
	archivePath, err := install.VerifyBundle(stage, a.o.Arch, ver, "")
	if err != nil {
		return refuse(err)
	}
	bin, err := install.ExtractReleaseBinary(archivePath, filepath.Join(stage, "bin"))
	if err != nil {
		return refuse(err)
	}
	if err := a.verifyRunnable(bin); err != nil {
		return refuse(deyerr.New(deyerr.S001, deyerr.Params{"file": archive}).
			WithWhy("the signed release holds a binary this node cannot run: " + err.Error()))
	}
	if err := a.checkSignedUpdates(ctx, bin, ver, archive); err != nil {
		return refuse(err)
	}
	if err := (install.SelfUpdater{Root: a.o.Root}).Install(bin); err != nil {
		return err
	}
	a.log.Info("deyroute binary of the signed release installed; restarting the node service", slog.String("version", ver))
	a.later(a.o.RestartDelay, a.restartSelf)
	return nil
}

// checkSignedUpdates asks the deyroute binary of the signed release ver
// (at bin, in the root-only staging directory) whether it checks signed
// updates itself: `deyroute --json version` must answer
// "signed_update": true. A release built before signed updates is genuine
// and validly signed, but once installed, its node takes any binary a hub
// serves with only the sha256 the hub announces compared. A hub that serves
// signed releases (api.FeatureSignedUpdate) only ever names its own
// version, which checks them, so such a self.update is refused (DEY-S001)
// and nothing is installed. Peers are told apart by what the build says it
// can do, never by its version: every 0.3.x edge build shares one.
//
// exec.ReleaseVersion runs it (only `deyroute --json version`, without the
// agent's environment, at most 15 s) as root, as the installer test-runs
// the binary it installs: it is the release's own code, verified against
// the release key, which the node would run as root once installed. As the
// backends' user deyroute, a compromised backend could trace it and answer
// in its place.
func (a *agent) checkSignedUpdates(ctx context.Context, bin, ver, archive string) error {
	run := a.o.ReleaseVersion
	if run == nil {
		run = exec.ReleaseVersion
	}
	out, err := run(ctx, bin)
	var answer struct {
		SignedUpdate *bool `json:"signed_update"`
	}
	if err == nil {
		err = json.Unmarshal(bytes.TrimSpace(out), &answer)
	}
	if err != nil {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": archive}).
			WithWhy("the deyroute binary of release " + ver + " did not answer `deyroute --json version` (" +
				dlog.Redact(err.Error()) + "), so this node cannot tell whether it checks signed updates itself")
	}
	if answer.SignedUpdate == nil || !*answer.SignedUpdate {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": archive}).
			WithWhy("release " + ver + " was built before signed updates: once installed, this node would take any deyroute binary the hub serves").
			WithFix("nothing to do on this node, it keeps its version. A hub that serves signed releases never asks for an older one by itself: " +
				"if nobody rolled the hub back to " + ver + ", check the hub; otherwise update the hub to a release with signed updates (deyroute update on the hub) and the nodes follow")
	}
	return nil
}

// writeStaged writes r to the new file p (0600; an existing p is an error).
func writeStaged(p string, r io.Reader) error {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- a plain name in our staging dir
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	return nil
}

// verifyRunnable checks that this node can run the binary at p
// (Options.VerifyBinary, default VerifyELF): a binary it cannot execute
// would leave it restarting into a dead service, unreachable for the hub.
func (a *agent) verifyRunnable(p string) error {
	verify := a.o.VerifyBinary
	if verify == nil {
		verify = VerifyELF
	}
	return verify(p, a.o.Arch)
}

// selfUpdateUnsigned is the self.update of nodes before signed updates,
// kept for node.allow_unsigned_updates: download the hub's deyroute binary
// for this architecture (GET /v1/assets/deyroute/{arch}), compare it with
// the sha256 and version the hub announced, check that it runs here and
// install it. No signature proves it.
func (a *agent) selfUpdateUnsigned(ctx context.Context, args api.SelfUpdateArgs) error {
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
	if err := a.verifyRunnable(name); err != nil {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": file}).
			WithWhy("the hub served a file this node cannot run: " + err.Error()).
			WithDetail(nothingInstalled)
	}
	if err := (install.SelfUpdater{Root: a.o.Root}).Install(name); err != nil {
		return err
	}
	a.log.Info("unsigned deyroute binary installed (node.allow_unsigned_updates); restarting the node service",
		slog.String("version", meta.Version))
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
