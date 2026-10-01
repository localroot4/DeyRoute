package tlsutil

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/registration"
	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
)

func staticResolver(addrs ...string) func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) { return addrs, nil }
}

func TestValidDomain(t *testing.T) {
	for _, d := range []string{"example.com", "a.b.example.co", "x-1.example.io", "xn--mgbh0fb.xn--mgba3a4f16a"} {
		require.True(t, ValidDomain(d), d)
	}
	for _, d := range []string{"", "localhost", "*.example.com", "1.2.3.4", "-a.example.com", "a..com", "Example.com", "a_b.example.com", "example.com.", "example.123"} {
		require.False(t, ValidDomain(d), d)
	}
}

func TestCheckDomainResolves(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", staticResolver("5.6.7.8")))
	require.NoError(t, CheckDomainResolves(ctx, "tun.example.com", " 5.6.7.8 ", staticResolver("5.6.7.8", "5.6.7.8")))
	require.NoError(t, CheckDomainResolves(ctx, "tun.example.com", "2001:db8::1", staticResolver("2001:0db8:0:0::1")))
	// Addresses of the other family are not judged (the hub's IPv6 is not
	// known here); a v4-mapped form counts as IPv4.
	require.NoError(t, CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", staticResolver("2001:db8::99", "::ffff:5.6.7.8")))

	// Another address of the same family next to the hub's: Let's Encrypt
	// may validate against it, so HTTP-01 would fail at random.
	e := requireCode(t, CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", staticResolver("9.9.9.9", "5.6.7.8")), deyerr.T004)
	require.Contains(t, e.Why(), "also point to 9.9.9.9")
	require.Contains(t, e.Fix(), "keep only the record for 5.6.7.8")
	require.Contains(t, e.Detail, "9.9.9.9, 5.6.7.8")
	e = requireCode(t, CheckDomainResolves(ctx, "tun.example.com", "2001:db8::1", staticResolver("2001:db8::1", "2001:db8::2", "1.1.1.1")), deyerr.T004)
	require.Contains(t, e.Why(), "also point to 2001:db8::2")
	require.NotContains(t, e.Why(), "1.1.1.1")

	err := CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", staticResolver("104.16.1.1", "104.16.2.2"))
	e = requireCode(t, err, deyerr.T004)
	require.Equal(t, "Domain tun.example.com does not resolve to the hub", e.Message())
	require.Contains(t, e.Why(), "5.6.7.8")
	require.Contains(t, e.Detail, "104.16.1.1, 104.16.2.2")

	e = requireCode(t, CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", staticResolver()), deyerr.T004)
	require.Contains(t, e.Detail, "no addresses")

	lookupErr := errors.New("no such host")
	e = requireCode(t, CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", func(context.Context, string) ([]string, error) {
		return nil, lookupErr
	}), deyerr.T004)
	require.ErrorIs(t, e, lookupErr)
	require.Contains(t, e.Why(), "no such host")

	e = requireCode(t, CheckDomainResolves(ctx, "tun.example.com", "not-an-ip", staticResolver("5.6.7.8")), deyerr.T004)
	require.Contains(t, e.Why(), "not a valid IP")

	// The resolver receives a context with a deadline.
	var hadDeadline bool
	require.NoError(t, CheckDomainResolves(ctx, "tun.example.com", "5.6.7.8", func(c context.Context, _ string) ([]string, error) {
		_, hadDeadline = c.Deadline()
		return []string{"5.6.7.8"}, nil
	}))
	require.True(t, hadDeadline)

	// The default resolver works for literal IPs without network access.
	require.NoError(t, CheckDomainResolves(ctx, "127.0.0.1", "127.0.0.1", nil))
}

func TestObtainACMEPreflight(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	_, _, err := ObtainACME(ctx, ACMEOptions{Domain: "1.2.3.4", AccountDir: dir})
	e := requireCode(t, err, deyerr.T003)
	require.Contains(t, e.Why(), "not a valid domain")

	_, _, err = ObtainACME(ctx, ACMEOptions{Domain: "tun.example.com"})
	requireCode(t, err, deyerr.X000)

	// A bad HTTP-01 port is refused, but it is ignored with DNS-01 (the run
	// goes on and stops at the DNS check here).
	_, _, err = ObtainACME(ctx, ACMEOptions{Domain: "tun.example.com", AccountDir: dir, HTTPPort: 70000})
	e = requireCode(t, err, deyerr.T003)
	require.Contains(t, e.Why(), "70000 is not a valid port")
	_, _, err = ObtainACME(ctx, ACMEOptions{
		Domain: "tun.example.com", AccountDir: dir, HTTPPort: -1, CloudflareToken: "cf-token-123456",
		ExpectedIP: "5.6.7.8", Resolver: staticResolver("1.1.1.1"),
	})
	requireCode(t, err, deyerr.T004)

	// DNS pointing elsewhere stops before anything is written or dialled.
	_, _, err = ObtainACME(ctx, ACMEOptions{
		Domain: "Tun.Example.com.", AccountDir: dir, ExpectedIP: "5.6.7.8",
		Resolver: staticResolver("1.1.1.1"),
	})
	e = requireCode(t, err, deyerr.T004)
	require.Equal(t, "tun.example.com", e.Params["domain"])
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	// Port 80 busy: T003 with a clear reason, still nothing written.
	var askedPort int
	_, _, err = ObtainACME(ctx, ACMEOptions{
		Domain: "tun.example.com", CacheDir: dir, ExpectedIP: "5.6.7.8",
		Resolver: staticResolver("5.6.7.8"),
		PortFree: func(p int) bool { askedPort = p; return false },
	})
	e = requireCode(t, err, deyerr.T003)
	require.Equal(t, DefaultACMEHTTPPort, askedPort)
	require.Contains(t, e.Why(), "port 80/tcp is in use")
	require.Contains(t, e.Fix(), "Cloudflare")
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestTCPPortFree(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.False(t, tcpPortFree(port))
	require.NoError(t, ln.Close())
	require.True(t, tcpPortFree(port))
}

// fakeACME serves a minimal ACME directory whose nonce endpoint fails, so
// lego gets as far as account registration without any real network.
func fakeACME(t *testing.T, directoryStatus int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/directory", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if directoryStatus != http.StatusOK {
			http.Error(w, "down", directoryStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"newNonce":   srv.URL + "/nonce",
			"newAccount": srv.URL + "/account",
			"newOrder":   srv.URL + "/order",
			"revokeCert": srv.URL + "/revoke",
			"keyChange":  srv.URL + "/keychange",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"type":"urn:ietf:params:acme:error:unauthorized","detail":"test server refuses"}`))
	})
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestObtainACMEDirectoryDown(t *testing.T) {
	srv, hits := fakeACME(t, http.StatusInternalServerError)
	dir := t.TempDir()
	_, _, err := ObtainACME(context.Background(), ACMEOptions{
		Domain: "tun.example.com", AccountDir: dir, DirectoryURL: srv.URL + "/directory",
		PortFree: func(int) bool { return true }, HTTPClient: srv.Client(),
	})
	e := requireCode(t, err, deyerr.T003)
	require.Equal(t, "ACME challenge failed for tun.example.com", e.Message())
	require.Positive(t, hits.Load(), "%v", err)

	// The account key was created and stored 0600 in a per-directory folder.
	sub := filepath.Join(dir, accountSubdir(srv.URL+"/directory"))
	st, err := os.Stat(filepath.Join(sub, acmeKeyFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	st, err = os.Stat(sub)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), st.Mode().Perm())
}

func TestObtainACMERegistrationRefused(t *testing.T) {
	for _, token := range []string{"", "cf-token"} {
		t.Run("cloudflare="+strconv.FormatBool(token != ""), func(t *testing.T) {
			srv, _ := fakeACME(t, http.StatusOK)
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _, err := ObtainACME(ctx, ACMEOptions{
				Domain: "tun.example.com", Email: "owner@example.com", AccountDir: dir,
				DirectoryURL: srv.URL + "/directory", CloudflareToken: token,
				HTTPPort: 18080, PortFree: func(int) bool { return true }, HTTPClient: srv.Client(),
			})
			requireCode(t, err, deyerr.T003)
			// No registration was saved because the server refused it.
			_, err = os.Stat(filepath.Join(dir, accountSubdir(srv.URL+"/directory"), acmeAccountFile))
			require.True(t, os.IsNotExist(err))
		})
	}
}

func TestObtainACMECancelled(t *testing.T) {
	srv, hits := fakeACME(t, http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := ObtainACME(ctx, ACMEOptions{
		Domain: "tun.example.com", AccountDir: t.TempDir(), DirectoryURL: srv.URL + "/directory",
		PortFree: func(int) bool { return true }, HTTPClient: srv.Client(),
	})
	requireCode(t, err, deyerr.T003)
	require.Zero(t, hits.Load(), "a cancelled context sends no request")
}

func TestACMEDirectories(t *testing.T) {
	require.Equal(t, ACMEProduction, ACMEOptions{}.directory())
	require.Equal(t, ACMEStaging, ACMEOptions{Staging: true}.directory())
	require.Equal(t, "https://ca.example/dir", ACMEOptions{Staging: true, DirectoryURL: "https://ca.example/dir"}.directory())
	require.Equal(t, "production", accountSubdir(ACMEProduction))
	require.Equal(t, "staging", accountSubdir(ACMEStaging))
	a, b := accountSubdir("https://a.example/dir"), accountSubdir("https://b.example/dir")
	require.NotEqual(t, a, b)
	require.Regexp(t, `^custom-[0-9a-f]{12}$`, a)
	require.Equal(t, "/x", ACMEOptions{AccountDir: "/x", CacheDir: "/y"}.accountDir())
	require.Equal(t, "/y", ACMEOptions{CacheDir: "/y"}.accountDir())
}

func TestAccountStore(t *testing.T) {
	s := accountStore{dir: filepath.Join(t.TempDir(), "production")}
	u, err := s.load("owner@example.com")
	require.NoError(t, err)
	require.NotNil(t, u.GetPrivateKey())
	require.Nil(t, u.GetRegistration())
	require.Equal(t, "owner@example.com", u.GetEmail())

	// Same key on reload, still unregistered.
	u2, err := s.load("owner@example.com")
	require.NoError(t, err)
	require.Equal(t, u.key, u2.key)
	require.Nil(t, u2.reg)

	u.reg = &registration.Resource{URI: "https://acme.example/acct/1"}
	require.NoError(t, s.saveRegistration(u, ACMEProduction))
	st, err := os.Stat(filepath.Join(s.dir, acmeAccountFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	u3, err := s.load("owner@example.com")
	require.NoError(t, err)
	require.NotNil(t, u3.reg)
	require.Equal(t, "https://acme.example/acct/1", u3.reg.URI)

	// Another email registers again (same key).
	u4, err := s.load("new@example.com")
	require.NoError(t, err)
	require.Nil(t, u4.reg)
	require.Equal(t, u.key, u4.key)

	// A damaged account.json is ignored.
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, acmeAccountFile), []byte("{"), 0o600))
	u5, err := s.load("owner@example.com")
	require.NoError(t, err)
	require.Nil(t, u5.reg)

	// A damaged key is T008.
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, acmeKeyFile), []byte("junk"), 0o600))
	_, err = s.load("owner@example.com")
	requireCode(t, err, deyerr.T008)

	// Unreadable paths (directories, a file used as a directory) are T008.
	bad := accountStore{dir: t.TempDir()}
	require.NoError(t, os.Mkdir(filepath.Join(bad.dir, acmeKeyFile), 0o700))
	_, err = bad.load("")
	requireCode(t, err, deyerr.T008)

	bad2 := accountStore{dir: t.TempDir()}
	_, err = bad2.load("")
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(bad2.dir, acmeAccountFile), 0o700))
	_, err = bad2.load("")
	requireCode(t, err, deyerr.T008)

	blocker := writeFile(t, t.TempDir(), "file", []byte("x"))
	_, err = accountStore{dir: filepath.Join(blocker, "sub")}.load("")
	requireCode(t, err, deyerr.T008)
}

// trackedBody records whether a request body was closed.
type trackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackedBody) Close() error { b.closed.Store(true); return nil }

// The http.RoundTripper contract: RoundTrip closes the request body even
// when it fails before reaching the base transport.
func TestCtxTransportClosesBodyWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tr := &ctxTransport{ctx: ctx, base: http.DefaultTransport, timeout: time.Second}
	body := &trackedBody{Reader: strings.NewReader("jws")}
	req, err := http.NewRequest(http.MethodPost, "https://acme.invalid/new-order", body)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
	require.True(t, body.closed.Load())
}

// The Cloudflare token must never reach logs or DEY error texts.
func TestObtainACMERegistersCloudflareToken(t *testing.T) {
	token := "cf-test-token-7d1c2b9a"
	_, _, err := ObtainACME(context.Background(), ACMEOptions{
		Domain: "1.2.3.4", AccountDir: t.TempDir(), CloudflareToken: token,
	})
	requireCode(t, err, deyerr.T003)
	require.NotContains(t, deylog.Redact("cloudflare said: bad token "+token), token)
}

func TestBoundClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c, closeIdle := ACMEOptions{}.boundClient(ctx)
	defer closeIdle()
	resp, err := c.Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Zero(t, c.Timeout, "no timer goroutine per request")
	require.Equal(t, acmeRequestTimeout, c.Transport.(*ctxTransport).timeout)

	cancel()
	_, err = c.Get(srv.URL) // fails before any response is received
	require.ErrorIs(t, err, context.Canceled)

	custom := &http.Client{Timeout: 5 * time.Second, Transport: srv.Client().Transport}
	c2, closeIdle2 := ACMEOptions{HTTPClient: custom}.boundClient(context.Background())
	defer closeIdle2()
	require.Equal(t, 5*time.Second, c2.Transport.(*ctxTransport).timeout)
	resp, err = c2.Get(srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}
