package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/version"
)

// Download limits and retry defaults (spec section 5: every source is tried 3
// times with backoff, then the next source).
const (
	// DefaultMaxBytes caps every download and every extracted file (512 MiB).
	DefaultMaxBytes int64 = 512 << 20
	// SmallMaxBytes caps SHA256SUMS, signatures and manifests (1 MiB).
	SmallMaxBytes int64 = 1 << 20
	// DefaultTries is the number of attempts per URL.
	DefaultTries = 3
	// DefaultBackoff is the wait after the first failed attempt; it doubles
	// after every further failure (1s, 2s, ...).
	DefaultBackoff = time.Second
	// DefaultAttemptTimeout bounds one download attempt.
	DefaultAttemptTimeout = 10 * time.Minute
)

// Fetcher downloads url and streams the body into w. Implementations: the
// direct HTTPFetcher, the daemon's "via node" fetcher (fetch.proxy over the
// control channel, spec section 5) and ChainFetcher combining them.
//
// A Fetcher must not write anything to w when it fails before the first byte;
// when it fails mid-stream the caller discards w (FetchVerified and
// ChainFetcher reset or recreate their target).
type Fetcher interface {
	Fetch(ctx context.Context, url string, w io.Writer) error
}

// FetcherFunc adapts a function to Fetcher.
type FetcherFunc func(ctx context.Context, url string, w io.Writer) error

// Fetch implements Fetcher.
func (f FetcherFunc) Fetch(ctx context.Context, url string, w io.Writer) error { return f(ctx, url, w) }

type ctxKey int

const expectedSHAKey ctxKey = 1

// WithExpectedSHA256 attaches the sha256 the downloaded content must have.
// FetchVerified sets it so a via-node Fetcher can pass it to fetch.proxy,
// letting the node verify before uploading.
func WithExpectedSHA256(ctx context.Context, sha256hex string) context.Context {
	return context.WithValue(ctx, expectedSHAKey, strings.ToLower(sha256hex))
}

// ExpectedSHA256 returns the checksum attached with WithExpectedSHA256, or "".
func ExpectedSHA256(ctx context.Context) string {
	s, _ := ctx.Value(expectedSHAKey).(string)
	return s
}

// PermanentError marks a failure that retrying the same URL cannot fix (HTTP
// 404, 403, a size limit). FetchVerified moves on to the next URL at once.
type PermanentError struct{ Err error }

// Error implements error.
func (e *PermanentError) Error() string { return e.Err.Error() }

// Unwrap returns the cause.
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err as a PermanentError (nil stays nil).
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err is a PermanentError or wraps one through
// single-error wrapping (fmt.Errorf %w, *deyerr.Error). Joined errors are not
// searched: a join of one permanent and one transient failure is transient.
func IsPermanent(err error) bool {
	for err != nil {
		if _, ok := err.(*PermanentError); ok {
			return true
		}
		err = stderrors.Unwrap(err)
	}
	return false
}

// ErrTooLarge is returned when a download exceeds its size limit.
var ErrTooLarge = deyerr.Plain("download exceeds the size limit")

// HTTPFetcher downloads over HTTP(S). It honours https_proxy/http_proxy/
// no_proxy (http.ProxyFromEnvironment), identifies itself as
// "deyroute/<version>", accepts only status 200 and enforces MaxBytes.
type HTTPFetcher struct {
	// Client is used for requests; nil means a client whose transport uses
	// http.ProxyFromEnvironment with sane dial/TLS/header timeouts.
	Client *http.Client
	// UserAgent defaults to "deyroute/<version.Version>".
	UserAgent string
	// MaxBytes defaults to DefaultMaxBytes.
	MaxBytes int64
}

// NewHTTPClient returns the default download client: proxy from the
// environment, 15 s connect/TLS timeouts, 60 s to the response header.
// Whole-transfer deadlines come from the caller's context.
func NewHTTPClient() *http.Client {
	var tr *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = dt.Clone()
	} else {
		tr = &http.Transport{}
	}
	tr.Proxy = http.ProxyFromEnvironment
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.ResponseHeaderTimeout = 60 * time.Second
	tr.ExpectContinueTimeout = time.Second
	return &http.Client{Transport: tr}
}

// Fetch implements Fetcher.
func (h HTTPFetcher) Fetch(ctx context.Context, url string, w io.Writer) error {
	client := h.Client
	if client == nil {
		// A private client per call: nothing (idle connections, goroutines)
		// outlives the download in the long-running daemon.
		client = NewHTTPClient()
		defer client.CloseIdleConnections()
	}
	limit := h.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	ua := h.UserAgent
	if ua == "" {
		ua = "deyroute/" + version.Version
	}
	shown := RedactURL(url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Permanent(fmt.Errorf("GET %s: invalid URL", shown))
	}
	if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
		// e.g. "{mirror}" resolved without a mirror: retrying cannot help.
		return Permanent(fmt.Errorf("GET %s: not an http(s) URL", shown))
	}
	req.Header.Set("User-Agent", ua)
	// Checksums are over the exact bytes: never let a transparent
	// Content-Encoding change them.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("GET %s: HTTP %s", shown, resp.Status)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
			resp.StatusCode != http.StatusRequestTimeout && resp.StatusCode != http.StatusTooManyRequests {
			return Permanent(err)
		}
		return err
	}
	if resp.ContentLength > limit {
		return Permanent(fmt.Errorf("GET %s: %w (%d > %d bytes)", shown, ErrTooLarge, resp.ContentLength, limit))
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("GET %s: %w", shown, err)
	}
	if n > limit {
		return Permanent(fmt.Errorf("GET %s: %w (> %d bytes)", shown, ErrTooLarge, limit))
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("GET %s: short body (%d of %d bytes)", shown, n, resp.ContentLength)
	}
	return nil
}

// resettable is implemented by *os.File: the target can be rewound between
// attempts.
type resettable interface {
	io.Writer
	Truncate(size int64) error
	Seek(offset int64, whence int) (int64, error)
}

// countingWriter counts bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// ChainFetcher tries each Fetcher in order until one succeeds. On the hub
// the daemon puts the via-node fetcher first (after the first join every hub
// download goes through a node, spec section 5) and the direct HTTPFetcher
// second; elsewhere direct comes first.
//
// When a fetcher fails after writing data, the next one can only run if w can
// be rewound (an *os.File); otherwise the error is returned.
type ChainFetcher struct {
	Fetchers []Fetcher
}

// Fetch implements Fetcher.
func (c ChainFetcher) Fetch(ctx context.Context, url string, w io.Writer) error {
	if len(c.Fetchers) == 0 {
		return Permanent(deyerr.Plain("no fetcher configured"))
	}
	var errs []error
	for i, f := range c.Fetchers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			if r, ok := w.(resettable); ok {
				if err := resetFile(r); err != nil {
					return err
				}
			}
		}
		cw := &countingWriter{w: w}
		err := f.Fetch(ctx, url, cw)
		if err == nil {
			return nil
		}
		errs = append(errs, err)
		if _, ok := w.(resettable); !ok && cw.n > 0 {
			break
		}
	}
	joined := stderrors.Join(errs...)
	// Only permanent when every fetcher said so; a transient failure of one
	// path is worth a retry.
	for _, e := range errs {
		if !IsPermanent(e) {
			return joined
		}
	}
	return Permanent(joined)
}

func resetFile(r resettable) error {
	if err := r.Truncate(0); err != nil {
		return err
	}
	_, err := r.Seek(0, io.SeekStart)
	return err
}

// RetryOptions controls FetchVerified / FetchBytes.
type RetryOptions struct {
	// Tries per URL (default 3).
	Tries int
	// Backoff after the first failed try, doubled after each further one
	// (default 1 s).
	Backoff time.Duration
	// AttemptTimeout bounds one attempt (default 10 min).
	AttemptTimeout time.Duration
	// Sleep waits d or until ctx is done; nil uses a timer. Tests inject a
	// recorder.
	Sleep func(ctx context.Context, d time.Duration) error
	// File names the file in error messages (default: base name of dst/URL).
	File string
	// MismatchCode is returned when the content does not match the expected
	// sha256: DEY-S001 (default, backends/self-update) or DEY-I005 (installer).
	MismatchCode deyerr.Code
	// FailCode is returned when every URL failed: DEY-I004 (default) or
	// DEY-B001 for backends.
	FailCode deyerr.Code
	// Params fill FailCode/MismatchCode templates ({backend}, {version}...);
	// "file" is added automatically.
	Params deyerr.Params
	// MaxBytes caps the download: FetchBytes defaults to SmallMaxBytes,
	// FetchVerified to DefaultMaxBytes (enforced whatever the Fetcher is).
	MaxBytes int64
}

func (o RetryOptions) withDefaults() RetryOptions {
	if o.Tries <= 0 {
		o.Tries = DefaultTries
	}
	if o.Backoff <= 0 {
		o.Backoff = DefaultBackoff
	}
	if o.AttemptTimeout <= 0 {
		o.AttemptTimeout = DefaultAttemptTimeout
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	if o.MismatchCode == "" {
		o.MismatchCode = deyerr.S001
	}
	if o.FailCode == "" {
		o.FailCode = deyerr.I004
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = SmallMaxBytes
	}
	return o
}

func (o RetryOptions) params(file string) deyerr.Params {
	p := deyerr.Params{"file": file}
	for k, v := range o.Params {
		p[k] = v
	}
	return p
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errMismatch signals a checksum mismatch from an attempt.
type errMismatch struct{ want, got, url string }

func (e *errMismatch) Error() string {
	return fmt.Sprintf("sha256 of %s is %s, expected %s", e.url, e.got, e.want)
}

// retryURLs runs attempt for each URL, DefaultTries times with doubling
// backoff, and moves to the next URL after a permanent error or a checksum
// mismatch. It returns the index of the URL that succeeded.
func retryURLs(ctx context.Context, urls []string, o RetryOptions, file string,
	attempt func(ctx context.Context, url string) error) (int, error) {
	if len(urls) == 0 {
		return -1, deyerr.New(o.FailCode, o.params(file)).WithDetail("no download source configured")
	}
	var (
		lastErr  error
		mismatch *errMismatch
	)
	for i, u := range urls {
		wait := o.Backoff
		for try := 1; try <= o.Tries; try++ {
			if err := ctx.Err(); err != nil {
				return -1, deyerr.Wrap(o.FailCode, err, o.params(file))
			}
			actx, cancel := context.WithTimeout(ctx, o.AttemptTimeout)
			err := attempt(actx, u)
			cancel()
			if err == nil {
				return i, nil
			}
			lastErr = fmt.Errorf("%s (try %d/%d): %w", RedactURL(u), try, o.Tries, err)
			var mm *errMismatch
			if stderrors.As(err, &mm) {
				mismatch = mm
				break
			}
			if IsPermanent(err) || try == o.Tries {
				break
			}
			if err := o.Sleep(ctx, wait); err != nil {
				return -1, deyerr.Wrap(o.FailCode, err, o.params(file))
			}
			wait *= 2
		}
	}
	if mismatch != nil {
		// A source served different bytes than the signed checksum: never
		// install it, and tell the owner exactly that.
		return -1, deyerr.Wrap(o.MismatchCode, mismatch, o.params(file))
	}
	return -1, deyerr.Wrap(o.FailCode, lastErr, o.params(file))
}

// FetchVerified downloads the first working URL of urls to dst: each URL is
// tried opt.Tries times (default 3) with doubling backoff, then the next URL.
// Content goes to a temp file next to dst, is verified against sha256hex and
// only then renamed to dst (mode 0644), so dst never holds unverified bytes.
//
// Errors: opt.MismatchCode (S001 default / I005) when a source served bytes
// with another checksum and no source served the right ones; opt.FailCode
// (I004 default / B001) wrapping the last error when every URL failed.
func FetchVerified(ctx context.Context, f Fetcher, urls []string, sha256hex string, dst string, opt RetryOptions) error {
	limit := opt.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	o := opt.withDefaults()
	file := o.File
	if file == "" {
		file = filepath.Base(dst)
	}
	want := strings.ToLower(strings.TrimSpace(sha256hex))
	if !validSHA256(want) {
		return deyerr.New(o.MismatchCode, o.params(file)).
			WithWhy("no valid expected sha256 was supplied; unverified files are never installed")
	}
	if f == nil {
		return deyerr.New(o.FailCode, o.params(file)).WithDetail("no fetcher configured")
	}
	dir := filepath.Dir(dst)
	err := os.MkdirAll(dir, 0o755) // #nosec G301 -- download dirs hold public release files
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".dl-*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmpName)
		}
	}()
	vctx := WithExpectedSHA256(ctx, want)
	target := &cappedFile{f: tmp, max: limit}
	_, err = retryURLs(vctx, urls, o, file, func(actx context.Context, u string) error {
		if err := resetFile(target); err != nil {
			return Permanent(err)
		}
		if err := f.Fetch(actx, u, target); err != nil {
			return err
		}
		got, err := hashOpenFile(tmp)
		if err != nil {
			return err
		}
		if got != want {
			return &errMismatch{want: want, got: got, url: RedactURL(u)}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Chmod(0o644); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	keep = true
	syncDir(dir)
	return nil
}

// FetchBytes downloads a small file (SHA256SUMS, a signature, a manifest)
// from the first working URL with the same retry policy as FetchVerified and
// returns its content and the URL index it came from. The caller verifies it
// (signature). opt.MaxBytes caps the size (default 1 MiB).
func FetchBytes(ctx context.Context, f Fetcher, urls []string, opt RetryOptions) ([]byte, int, error) {
	o := opt.withDefaults()
	file := o.File
	if file == "" && len(urls) > 0 {
		file = baseOfURL(urls[0])
	}
	if f == nil {
		return nil, -1, deyerr.New(o.FailCode, o.params(file)).WithDetail("no fetcher configured")
	}
	var buf limitedBuffer
	idx, err := retryURLs(ctx, urls, o, file, func(actx context.Context, u string) error {
		buf = limitedBuffer{max: o.MaxBytes}
		return f.Fetch(actx, u, &buf)
	})
	if err != nil {
		return nil, -1, err
	}
	return buf.b, idx, nil
}

// cappedFile is the FetchVerified target: an *os.File that refuses to grow
// past max and can be rewound (so ChainFetcher can retry another path).
type cappedFile struct {
	f      *os.File
	n, max int64
}

func (c *cappedFile) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.max {
		return 0, Permanent(fmt.Errorf("%w (> %d bytes)", ErrTooLarge, c.max))
	}
	n, err := c.f.Write(p)
	c.n += int64(n)
	return n, err
}

func (c *cappedFile) Truncate(size int64) error {
	if err := c.f.Truncate(size); err != nil {
		return err
	}
	c.n = size
	return nil
}

func (c *cappedFile) Seek(offset int64, whence int) (int64, error) { return c.f.Seek(offset, whence) }

// limitedBuffer is an in-memory writer that fails permanently past max bytes.
type limitedBuffer struct {
	b   []byte
	max int64
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if int64(len(l.b))+int64(len(p)) > l.max {
		return 0, Permanent(fmt.Errorf("%w (> %d bytes)", ErrTooLarge, l.max))
	}
	l.b = append(l.b, p...)
	return len(p), nil
}

// RedactURL hides the password of a URL with user info (an owner mirror such
// as https://user:secret@mirror.example) so it never reaches errors or logs.
// Anything that does not parse is reduced to its scheme and host part.
func RedactURL(u string) string {
	p, err := neturl.Parse(u)
	if err != nil {
		if i := strings.Index(u, "@"); i >= 0 {
			if j := strings.Index(u, "://"); j >= 0 && j < i {
				return u[:j+3] + "***@" + u[i+1:]
			}
			return "***@" + u[i+1:]
		}
		return u
	}
	if p.User == nil {
		return u
	}
	if _, has := p.User.Password(); has {
		p.User = neturl.UserPassword(p.User.Username(), "***")
	} else {
		p.User = neturl.User("***")
	}
	return p.String()
}

func baseOfURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

func hashOpenFile(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256File returns the lowercase hex sha256 of the file at path.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-chosen local file
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return hashOpenFile(f)
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// syncDir fsyncs a directory so a rename survives a crash (best effort).
func syncDir(dir string) {
	d, err := os.Open(dir) // #nosec G304 -- directory we just wrote into
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
