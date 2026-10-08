package install

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// flakyServer fails the first `fails` requests to /file with 503, then serves body.
func flakyServer(t *testing.T, body []byte, fails int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		switch r.URL.Path {
		case "/file":
			if n <= fails {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(body)
		case "/ua":
			_, _ = w.Write([]byte(r.UserAgent() + " " + r.Header.Get("Accept-Encoding")))
		case "/big":
			w.Header().Set("Content-Length", strconv.Itoa(4096))
			_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
		case "/chunked":
			fl, _ := w.(http.Flusher)
			for i := 0; i < 8; i++ {
				_, _ = w.Write(bytes.Repeat([]byte("y"), 512))
				if fl != nil {
					fl.Flush()
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestFetchVerifiedRetriesWithBackoff(t *testing.T) {
	body := []byte("release archive")
	srv, hits := flakyServer(t, body, 2)
	dst := filepath.Join(t.TempDir(), "sub", "a.tar.gz")
	ns := &noSleep{}
	f := HTTPFetcher{Client: srv.Client()}
	err := FetchVerified(context.Background(), f, []string{srv.URL + "/file"}, sha(body), dst, RetryOptions{Sleep: ns.sleep})
	require.NoError(t, err)
	require.Equal(t, int32(3), hits.Load())
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second}, ns.waited)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, body, got)
	fi, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	// No temp files left next to dst.
	ents, err := os.ReadDir(filepath.Dir(dst))
	require.NoError(t, err)
	require.Len(t, ents, 1)
}

func TestFetchVerifiedFallbackOrder(t *testing.T) {
	body := []byte("good")
	bad, badHits := flakyServer(t, nil, 1000) // always 503
	good, goodHits := flakyServer(t, body, 0)
	ns := &noSleep{}
	dst := filepath.Join(t.TempDir(), "f")
	err := FetchVerified(context.Background(), HTTPFetcher{}, []string{bad.URL + "/file", good.URL + "/file"}, sha(body), dst,
		RetryOptions{Sleep: ns.sleep})
	require.NoError(t, err)
	require.Equal(t, int32(3), badHits.Load(), "first source is tried 3 times")
	require.Equal(t, int32(1), goodHits.Load())
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second}, ns.waited)
}

func TestFetchVerifiedPermanentSkipsRetries(t *testing.T) {
	body := []byte("x")
	srv, hits := flakyServer(t, body, 0)
	ns := &noSleep{}
	dst := filepath.Join(t.TempDir(), "f")
	err := FetchVerified(context.Background(), HTTPFetcher{Client: srv.Client()}, []string{srv.URL + "/missing", srv.URL + "/file"}, sha(body), dst,
		RetryOptions{Sleep: ns.sleep})
	require.NoError(t, err)
	require.Equal(t, int32(2), hits.Load(), "404 is not retried")
	require.Empty(t, ns.waited)
}

func TestFetchVerifiedMismatch(t *testing.T) {
	body := []byte("tampered")
	srv, hits := flakyServer(t, body, 0)
	dst := filepath.Join(t.TempDir(), "f")
	f := HTTPFetcher{Client: srv.Client()}

	err := FetchVerified(context.Background(), f, []string{srv.URL + "/file"}, sha([]byte("original")), dst, RetryOptions{Sleep: (&noSleep{}).sleep})
	de := requireCode(t, err, deyerr.S001)
	require.Equal(t, "f", de.Params["file"])
	require.Equal(t, int32(1), hits.Load(), "a mismatch is not retried on the same URL")
	_, statErr := os.Stat(dst)
	require.True(t, os.IsNotExist(statErr), "dst never holds unverified bytes")

	err = FetchVerified(context.Background(), f, []string{srv.URL + "/file"}, sha([]byte("original")), dst,
		RetryOptions{Sleep: (&noSleep{}).sleep, MismatchCode: deyerr.I005, File: "deyroute.tar.gz"})
	de = requireCode(t, err, deyerr.I005)
	require.Contains(t, de.Message(), "deyroute.tar.gz")

	// An existing dst is untouched by a failed download.
	require.NoError(t, os.WriteFile(dst, []byte("old"), 0o600))
	err = FetchVerified(context.Background(), f, []string{srv.URL + "/file"}, sha([]byte("original")), dst, RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.S001)
	got, _ := os.ReadFile(dst)
	require.Equal(t, "old", string(got))
}

func TestFetchVerifiedMismatchThenGoodSource(t *testing.T) {
	good := []byte("original")
	m := newMapFetcher()
	m.files["https://a/f"] = []byte("evil")
	m.files["https://b/f"] = good
	dst := filepath.Join(t.TempDir(), "f")
	require.NoError(t, FetchVerified(context.Background(), m, []string{"https://a/f", "https://b/f"}, sha(good), dst, RetryOptions{Sleep: (&noSleep{}).sleep}))
	require.Equal(t, []string{sha(good), sha(good)}, m.shas, "expected sha256 travels in the context")
}

func TestFetchVerifiedAllFail(t *testing.T) {
	m := newMapFetcher()
	m.errs["https://a/f"] = deyerr.Plain("connection reset")
	dst := filepath.Join(t.TempDir(), "f")
	ns := &noSleep{}
	err := FetchVerified(context.Background(), m, []string{"https://a/f", "https://b/f"}, sha([]byte("x")), dst, RetryOptions{Sleep: ns.sleep})
	de := requireCode(t, err, deyerr.I004)
	require.Contains(t, de.Error(), "https://b/f")
	require.Equal(t, 3, m.count("https://a/f"))
	require.Equal(t, 1, m.count("https://b/f"))

	err = FetchVerified(context.Background(), m, []string{"https://a/f"}, sha([]byte("x")), dst,
		RetryOptions{Sleep: ns.sleep, Tries: 2, FailCode: deyerr.B001, Params: deyerr.Params{"backend": "xray", "version": "v1"}})
	de = requireCode(t, err, deyerr.B001)
	require.Equal(t, "Could not download xray v1", de.Message())

	err = FetchVerified(context.Background(), m, nil, sha([]byte("x")), dst, RetryOptions{})
	requireCode(t, err, deyerr.I004)
	err = FetchVerified(context.Background(), nil, []string{"https://a/f"}, sha([]byte("x")), dst, RetryOptions{})
	requireCode(t, err, deyerr.I004)
	err = FetchVerified(context.Background(), m, []string{"https://a/f"}, "", dst, RetryOptions{})
	requireCode(t, err, deyerr.S001)
}

func TestFetchVerifiedContextCancel(t *testing.T) {
	m := newMapFetcher()
	m.errs["https://a/f"] = deyerr.Plain("timeout")
	ctx, cancel := context.WithCancel(context.Background())
	sleep := func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}
	err := FetchVerified(ctx, m, []string{"https://a/f"}, sha([]byte("x")), filepath.Join(t.TempDir(), "f"), RetryOptions{Sleep: sleep})
	requireCode(t, err, deyerr.I004)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, m.count("https://a/f"))

	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	err = FetchVerified(ctx2, m, []string{"https://a/f"}, sha([]byte("x")), filepath.Join(t.TempDir(), "f"), RetryOptions{})
	requireCode(t, err, deyerr.I004)
}

func TestSleepCtx(t *testing.T) {
	require.NoError(t, sleepCtx(context.Background(), time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepCtx(ctx, time.Hour), context.Canceled)
}

func TestHTTPFetcherLimitsAndHeaders(t *testing.T) {
	srv, _ := flakyServer(t, []byte("ok"), 0)
	var buf bytes.Buffer
	f := HTTPFetcher{Client: srv.Client(), MaxBytes: 1024}
	require.NoError(t, f.Fetch(context.Background(), srv.URL+"/ua", &buf))
	require.Contains(t, buf.String(), "deyroute/")
	require.Contains(t, buf.String(), " identity")

	buf.Reset()
	err := f.Fetch(context.Background(), srv.URL+"/big", &buf)
	require.ErrorIs(t, err, ErrTooLarge)
	require.True(t, IsPermanent(err))
	require.Zero(t, buf.Len(), "declared oversize bodies are refused before reading")

	buf.Reset()
	err = f.Fetch(context.Background(), srv.URL+"/chunked", &buf)
	require.ErrorIs(t, err, ErrTooLarge)
	require.True(t, IsPermanent(err))

	err = f.Fetch(context.Background(), srv.URL+"/nope", io.Discard)
	require.True(t, IsPermanent(err))
	err = HTTPFetcher{UserAgent: "x"}.Fetch(context.Background(), "::bad-url", io.Discard)
	require.True(t, IsPermanent(err))

	// FetchVerified surfaces the size limit as a failed source.
	err = FetchVerified(context.Background(), f, []string{srv.URL + "/big"}, sha([]byte("x")), filepath.Join(t.TempDir(), "f"), RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.I004)
	require.ErrorIs(t, err, ErrTooLarge)
}

func TestHTTPFetcherServerErrorIsTransient(t *testing.T) {
	srv, _ := flakyServer(t, nil, 1000)
	err := HTTPFetcher{Client: srv.Client()}.Fetch(context.Background(), srv.URL+"/file", io.Discard)
	require.Error(t, err)
	require.False(t, IsPermanent(err))
	require.Contains(t, err.Error(), "503")
}

func TestNewHTTPClientUsesProxyFromEnvironment(t *testing.T) {
	c := NewHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tr.Proxy)
	require.Equal(t, 60*time.Second, tr.ResponseHeaderTimeout)
	tr.CloseIdleConnections()
}

func TestChainFetcher(t *testing.T) {
	partialFail := FetcherFunc(func(_ context.Context, _ string, w io.Writer) error {
		_, _ = w.Write([]byte("partial-garbage"))
		return deyerr.Plain("stream broken")
	})
	good := FetcherFunc(func(_ context.Context, _ string, w io.Writer) error {
		_, err := w.Write([]byte("payload"))
		return err
	})
	// With a file target the second fetcher starts from an empty file.
	dst := filepath.Join(t.TempDir(), "f")
	err := FetchVerified(context.Background(), ChainFetcher{Fetchers: []Fetcher{partialFail, good}}, []string{"u"}, sha([]byte("payload")), dst, RetryOptions{})
	require.NoError(t, err)
	got, _ := os.ReadFile(dst)
	require.Equal(t, "payload", string(got))

	// A plain writer cannot be rewound: no second attempt after partial data.
	var buf bytes.Buffer
	err = ChainFetcher{Fetchers: []Fetcher{partialFail, good}}.Fetch(context.Background(), "u", &buf)
	require.Error(t, err)
	require.Equal(t, "partial-garbage", buf.String())

	// Nothing written: the next fetcher is used even for a plain writer.
	buf.Reset()
	emptyFail := FetcherFunc(func(context.Context, string, io.Writer) error { return Permanent(deyerr.Plain("no node online")) })
	require.NoError(t, ChainFetcher{Fetchers: []Fetcher{emptyFail, good}}.Fetch(context.Background(), "u", &buf))
	require.Equal(t, "payload", buf.String())

	// All permanent → permanent; any transient → transient.
	err = ChainFetcher{Fetchers: []Fetcher{emptyFail, emptyFail}}.Fetch(context.Background(), "u", io.Discard)
	require.True(t, IsPermanent(err))
	err = ChainFetcher{Fetchers: []Fetcher{emptyFail, partialFail}}.Fetch(context.Background(), "u", io.Discard)
	require.False(t, IsPermanent(err))
	require.True(t, IsPermanent(ChainFetcher{}.Fetch(context.Background(), "u", io.Discard)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, ChainFetcher{Fetchers: []Fetcher{good}}.Fetch(ctx, "u", io.Discard), context.Canceled)
}

func TestFetchVerifiedCapsAnyFetcher(t *testing.T) {
	m := newMapFetcher()
	body := bytes.Repeat([]byte("n"), 64)
	m.files["node://f"] = body
	dst := filepath.Join(t.TempDir(), "f")
	err := FetchVerified(context.Background(), m, []string{"node://f"}, sha(body), dst, RetryOptions{MaxBytes: 16})
	requireCode(t, err, deyerr.I004)
	require.ErrorIs(t, err, ErrTooLarge)
	require.Equal(t, 1, m.count("node://f"), "a size violation is permanent")
	require.NoError(t, FetchVerified(context.Background(), m, []string{"node://f"}, sha(body), dst, RetryOptions{MaxBytes: 64}))
}

func TestFetchBytes(t *testing.T) {
	m := newMapFetcher()
	m.files["https://b/SHA256SUMS"] = []byte("sums")
	data, idx, err := FetchBytes(context.Background(), m, []string{"https://a/SHA256SUMS", "https://b/SHA256SUMS"}, RetryOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, idx)
	require.Equal(t, "sums", string(data))

	m.files["https://c/big"] = bytes.Repeat([]byte("z"), 100)
	_, _, err = FetchBytes(context.Background(), m, []string{"https://c/big"}, RetryOptions{MaxBytes: 10, Sleep: (&noSleep{}).sleep})
	de := requireCode(t, err, deyerr.I004)
	require.Equal(t, "big", de.Params["file"])
	require.ErrorIs(t, err, ErrTooLarge)
	require.Equal(t, 1, m.count("https://c/big"))

	_, _, err = FetchBytes(context.Background(), nil, []string{"x"}, RetryOptions{})
	requireCode(t, err, deyerr.I004)
}

func TestHelpers(t *testing.T) {
	require.Equal(t, "file.tar.gz", baseOfURL("https://x/y/file.tar.gz?sig=1#frag"))
	require.Equal(t, "plain", baseOfURL("plain"))
	require.Nil(t, Permanent(nil))
	p := filepath.Join(t.TempDir(), "x")
	require.NoError(t, os.WriteFile(p, []byte("abc"), 0o600))
	h, err := SHA256File(p)
	require.NoError(t, err)
	require.Equal(t, sha([]byte("abc")), h)
	_, err = SHA256File(p + ".missing")
	require.Error(t, err)
	ctx := WithExpectedSHA256(context.Background(), "ABC")
	require.Equal(t, "abc", ExpectedSHA256(ctx))
	require.Equal(t, "", ExpectedSHA256(context.Background()))
}

func TestRedactURL(t *testing.T) {
	require.Equal(t, "https://get.example/v1/x", RedactURL("https://get.example/v1/x"))
	require.Equal(t, "https://owner:%2A%2A%2A@mirror.example/latest/SHA256SUMS",
		RedactURL("https://owner:s3cret@mirror.example/latest/SHA256SUMS"))
	require.Equal(t, "https://%2A%2A%2A@mirror.example/x", RedactURL("https://tokenonly@mirror.example/x"))
	require.Equal(t, "https://***@bad host/x", RedactURL("https://u:p@bad host/x"))
	require.NotContains(t, RedactURL("::u:p@x"), "u:p")
}

// Credentials of an owner mirror never appear in errors (they reach logs and
// the three-line DEY output).
func TestFetchErrorsHideURLCredentials(t *testing.T) {
	srv, _ := flakyServer(t, nil, 1000)
	u := strings.Replace(srv.URL, "http://", "http://owner:s3cret@", 1)
	f := HTTPFetcher{Client: srv.Client()}
	err := f.Fetch(context.Background(), u+"/nope", io.Discard)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "s3cret")
	err = FetchVerified(context.Background(), f, []string{u + "/file"}, sha([]byte("x")),
		filepath.Join(t.TempDir(), "f"), RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.I004)
	require.NotContains(t, err.Error(), "s3cret")

	m := newMapFetcher()
	m.files["https://owner:s3cret@m.example/f"] = []byte("other bytes")
	err = FetchVerified(context.Background(), m, []string{"https://owner:s3cret@m.example/f"}, sha([]byte("x")),
		filepath.Join(t.TempDir(), "f"), RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.S001)
	require.NotContains(t, err.Error(), "s3cret")
}

// A URL that is not http(s) (e.g. "{mirror}" resolved to nothing) fails at
// once instead of being retried with backoff.
func TestHTTPFetcherRejectsNonHTTPURLs(t *testing.T) {
	for _, u := range []string{"/backends/x.tar.gz", "file:///etc/shadow", "ftp://x/y"} {
		err := HTTPFetcher{}.Fetch(context.Background(), u, io.Discard)
		require.Error(t, err, u)
		require.True(t, IsPermanent(err), u)
	}
	ns := &noSleep{}
	err := FetchVerified(context.Background(), HTTPFetcher{}, []string{"/x"}, sha([]byte("x")),
		filepath.Join(t.TempDir(), "f"), RetryOptions{Sleep: ns.sleep})
	requireCode(t, err, deyerr.I004)
	require.Empty(t, ns.waited)
}

// A path that stalls (no byte for StallTimeout) is given up for the next
// one, and a slow path that keeps sending is not cut.
func TestStalledFetchMovesOn(t *testing.T) {
	old := StallTimeout
	StallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { StallTimeout = old })

	stalled := 0
	stall := FetcherFunc(func(ctx context.Context, _ string, w io.Writer) error {
		stalled++
		_, _ = w.Write([]byte("par"))
		<-ctx.Done() // a connection that stops delivering
		return ctx.Err()
	})
	slow := FetcherFunc(func(ctx context.Context, _ string, w io.Writer) error {
		for _, b := range []byte("payload") {
			time.Sleep(40 * time.Millisecond) // below the stall timeout each time
			if _, err := w.Write([]byte{b}); err != nil {
				return err
			}
		}
		return nil
	})
	data, _, err := FetchBytes(context.Background(), ChainFetcher{Fetchers: []Fetcher{stall, slow}},
		[]string{"https://example.invalid/SHA256SUMS"}, RetryOptions{Tries: 1})
	require.NoError(t, err)
	require.Equal(t, "payload", string(data), "the partial bytes of the stalled path are dropped")
	require.Equal(t, 1, stalled)

	start := time.Now()
	_, _, err = FetchBytes(context.Background(), stall, []string{"https://example.invalid/x"}, RetryOptions{Tries: 1})
	require.Error(t, err)
	require.ErrorIs(t, err, errStalled)
	require.Less(t, time.Since(start), 5*time.Second)
}
