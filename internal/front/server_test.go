package front

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/front/fronttest"
	"github.com/localroot4/deyroute/internal/wsconn"
)

const (
	testKey    = "dGhlIHNhbXBsZSBub25jZQ=="
	testAccept = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
)

var _ net.Conn = (*Conn)(nil)

// wsUpgrade upgrades GET path on addr and returns the client end.
func wsUpgrade(t *testing.T, addr string, cfg *tls.Config, path string, extra ...string) *wsconn.Conn {
	t.Helper()
	conn := rawConn(t, addr, cfg)
	_, err := io.WriteString(conn, upgradeReq(path, extra...))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, 101, resp.StatusCode)
	require.Equal(t, testAccept, resp.Header.Get("Sec-WebSocket-Accept"))
	_ = conn.SetReadDeadline(time.Time{})
	wc := wsconn.New(conn, br, wsconn.Config{Client: true})
	t.Cleanup(func() { _ = wc.Close() })
	return wc
}

// poll waits until f is true.
func poll(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// expectClosed asserts that the server closes conn without answering.
func expectClosed(t *testing.T, conn net.Conn, within time.Duration) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(within))
	b, err := io.ReadAll(conn)
	var ne net.Error
	require.False(t, errors.As(err, &ne) && ne.Timeout(), "the connection stayed open for %v", within)
	return b
}

// ---------------------------------------------------------------------------
// Construction

func TestNewServerValidation(t *testing.T) {
	ln := func() net.Listener {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	_, err := NewServer(nil, ServerOptions{Secret: testSecret})
	assert.Error(t, err)
	for _, bad := range []string{"", "has space", "slash/inside", strings.Repeat("a", MaxSecretLen+1)} {
		_, err = NewServer(ln(), ServerOptions{Secret: bad})
		require.Error(t, err)
		if bad != "" {
			assert.NotContains(t, err.Error(), bad, "the secret is not echoed")
		}
	}
	_, err = NewServer(ln(), ServerOptions{Secret: testSecret, TLSMode: "magic"})
	assert.Error(t, err)
	_, err = NewServer(ln(), ServerOptions{Secret: testSecret, TLSMode: config.FrontTLSCustom})
	assert.Error(t, err, "custom needs files")
	_, err = NewServer(ln(), ServerOptions{Secret: testSecret, TLSMode: config.FrontTLSCustom, CertFile: "/nonexistent/c.pem", KeyFile: "/nonexistent/k.pem"})
	assert.Error(t, err)
}

func TestServerDefaults(t *testing.T) {
	srv := startServer(t, ServerOptions{})
	assert.Equal(t, 256, cap(srv.slots))
	assert.Equal(t, 10*time.Second, srv.headerTimeout)
	assert.Equal(t, 25*time.Second, srv.wsIdle)
	assert.NotNil(t, srv.tlsCfg, "auto is the default mode")
	assert.True(t, srv.sniff)
	assert.Contains(t, decoyServers, srv.serverName)

	srv2 := startServer(t, ServerOptions{WSIdleTimeout: -1, MaxPreAuth: 8, HeaderTimeout: time.Second, TLSMode: config.FrontTLSOff})
	assert.Zero(t, srv2.wsIdle, "negative turns the idle watchdog off")
	assert.Equal(t, 8, cap(srv2.slots))
	assert.Nil(t, srv2.tlsCfg)
	assert.Equal(t, time.Second, srv2.headerTimeout)
	assert.Equal(t, srv2.ln.Addr(), srv2.Addr())
}

// ---------------------------------------------------------------------------
// Decoy

var decoyBadRequests = map[string]string{
	"unknown path":                  "GET /nothing-here HTTP/1.1\r\nHost: x\r\n\r\n",
	"unknown deep path":             "GET /a/b/c/d HTTP/1.1\r\nHost: x\r\n\r\n",
	"wrong secret, valid upgrade":   upgradeReq("/wrongsecretwrongsecret/c"),
	"prefix of the secret":          upgradeReq("/" + testSecret[:10] + "/c"),
	"secret plus a character":       upgradeReq("/" + testSecret + "x/c"),
	"secret in another case":        upgradeReq("/" + strings.ToUpper(testSecret) + "/c"),
	"right secret without upgrade":  "GET /" + testSecret + "/c HTTP/1.1\r\nHost: x\r\n\r\n",
	"right secret, POST":            "POST /" + testSecret + "/c HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n",
	"right secret, POST with body":  "POST /" + testSecret + "/c HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\n\r\nhello",
	"right secret, PUT":             "PUT /" + testSecret + "/c HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n",
	"right secret, DELETE":          "DELETE /" + testSecret + "/c HTTP/1.1\r\nHost: x\r\n\r\n",
	"right secret, OPTIONS":         "OPTIONS /" + testSecret + "/c HTTP/1.1\r\nHost: x\r\n\r\n",
	"right secret, HTTP/1.0":        "GET /" + testSecret + "/c HTTP/1.0\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + testKey + "\r\n\r\n",
	"right secret, version 12":      strings.Replace(upgradeReq("/"+testSecret+"/c"), "Version: 13", "Version: 12", 1),
	"right secret, no version":      strings.Replace(upgradeReq("/"+testSecret+"/c"), "Sec-WebSocket-Version: 13\r\n", "", 1),
	"right secret, no key":          strings.Replace(upgradeReq("/"+testSecret+"/c"), "Sec-WebSocket-Key: "+testKey+"\r\n", "", 1),
	"right secret, bad key":         strings.Replace(upgradeReq("/"+testSecret+"/c"), testKey, "not base64 !!", 1),
	"right secret, short key":       strings.Replace(upgradeReq("/"+testSecret+"/c"), testKey, "c2hvcnQ=", 1),
	"right secret, two keys":        upgradeReq("/"+testSecret+"/c", "Sec-WebSocket-Key: "+testKey),
	"right secret, no Connection":   strings.Replace(upgradeReq("/"+testSecret+"/c"), "Connection: Upgrade\r\n", "", 1),
	"right secret, Connection keep": strings.Replace(upgradeReq("/"+testSecret+"/c"), "Connection: Upgrade", "Connection: keep-alive", 1),
	"right secret, wrong Upgrade":   strings.Replace(upgradeReq("/"+testSecret+"/c"), "Upgrade: websocket", "Upgrade: h2c", 1),
	"right secret, a body":          upgradeReq("/"+testSecret+"/c", "Content-Length: 4") + "body",
	"right secret, chunked":         upgradeReq("/"+testSecret+"/c", "Transfer-Encoding: chunked") + "0\r\n\r\n",
	"right secret, Expect":          upgradeReq("/"+testSecret+"/c", "Expect: 100-continue"),
	"secret and /d":                 upgradeReq("/" + testSecret + "/d"),
	"secret and /t/9":               upgradeReq("/" + testSecret + "/t/9"),
	"secret, c and more":            upgradeReq("/" + testSecret + "/c/more"),
	"secret, trailing slash":        upgradeReq("/" + testSecret + "/c/"),
	"secret, double slash":          upgradeReq("/" + testSecret + "//c"),
	"secret, query":                 upgradeReq("/" + testSecret + "/c?x=1"),
	"secret only":                   upgradeReq("/" + testSecret),
	"secret and slash":              upgradeReq("/" + testSecret + "/"),
	"absolute-form target":          upgradeReq("http://front.example.com/" + testSecret + "/c"),
	"c without secret":              upgradeReq("/c"),
	"percent-encoded secret":        upgradeReq("/" + strings.Replace(testSecret, "Z", "%5A", 1) + "/c"),
}

func TestDecoyIsIdenticalForEveryReason(t *testing.T) {
	for _, mode := range []string{config.FrontTLSOff, config.FrontTLSAuto} {
		t.Run(mode, func(t *testing.T) {
			srv := startServer(t, ServerOptions{TLSMode: mode, FailLimit: -1})
			var cfg *tls.Config
			if mode == config.FrontTLSAuto {
				cfg = insecureTLS()
			}
			ref := stripDate(rawDo(t, srv.Addr().String(), decoyBadRequests["unknown path"], cfg))
			require.NotEmpty(t, ref)
			assert.True(t, strings.HasPrefix(string(ref), "HTTP/1.1 404 Not Found\r\n"))
			for name, req := range decoyBadRequests {
				got := stripDate(rawDo(t, srv.Addr().String(), req, cfg))
				assert.Equal(t, string(ref), string(got), name)
			}
		})
	}
}

func TestDecoyShape(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	get := func(req string) (*http.Response, string, []byte) {
		raw := rawDo(t, srv.Addr().String(), req, nil)
		resp, err := http.ReadResponse(bufio.NewReader(strings.NewReader(string(raw))), nil)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body), raw
	}
	resp, body, _ := get("GET /nope HTTP/1.1\r\nHost: x\r\n\r\n")
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "text/html", resp.Header.Get("Content-Type"))
	assert.True(t, resp.Close, "Connection: close")
	assert.Equal(t, srv.serverName, resp.Header.Get("Server"))
	assert.Contains(t, decoyServers, resp.Header.Get("Server"))
	_, err := http.ParseTime(resp.Header.Get("Date"))
	assert.NoError(t, err, "Date header")
	assert.EqualValues(t, len(body), resp.ContentLength)
	assert.Equal(t, decoy404, body)

	for _, p := range []string{"/", "/index.html", "/robots.txt", "/?a=b", "/index.html?x"} {
		resp, body, _ = get("GET " + p + " HTTP/1.1\r\nHost: x\r\n\r\n")
		assert.Equal(t, 200, resp.StatusCode, p)
		assert.Equal(t, "text/html", resp.Header.Get("Content-Type"), p)
		assert.Equal(t, decoyPage, body, p)
		assert.Equal(t, srv.serverName, resp.Header.Get("Server"))
	}
	// HEAD: the headers of the GET answer, no body.
	rawGet := stripDate(rawDo(t, srv.Addr().String(), "GET / HTTP/1.1\r\nHost: x\r\n\r\n", nil))
	rawHead := stripDate(rawDo(t, srv.Addr().String(), "HEAD / HTTP/1.1\r\nHost: x\r\n\r\n", nil))
	assert.Equal(t, strings.TrimSuffix(string(rawGet), decoyPage), string(rawHead))
	// Other methods on the pages get the 404.
	for _, m := range []string{"POST", "PUT", "DELETE", "OPTIONS", "PATCH"} {
		resp, _, _ = get(m + " / HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\n\r\n")
		assert.Equal(t, 404, resp.StatusCode, m)
	}
	// The product's name is nowhere in the answers.
	for _, s := range []string{"dey", "Dey", "DEY", "route", "front"} {
		assert.NotContains(t, decoyPage, s)
		assert.NotContains(t, decoy404, s)
		for _, srvName := range decoyServers {
			assert.NotContains(t, srvName, s)
		}
	}
	assert.Less(t, len(decoyPage), 400, "tiny")
}

func TestDecoyServerNameIsPerInstanceAndNeutral(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		seen[pickServerName()] = true
	}
	for n := range seen {
		assert.Contains(t, decoyServers, n)
	}
	assert.Greater(t, len(seen), 1, "the choice is random")
}

func TestDecoyGarbageGetsNoAnswer(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, FailLimit: -1})
	for name, in := range map[string]string{
		"not http":        "hello there\r\n\r\n",
		"binary":          "\x00\x01\x02\x03\r\n\r\n",
		"tls hello on 80": "\x16\x03\x01\x00\x05hello",
		"no version":      "GET /\r\n\r\n",
		"no slash":        "GET " + testSecret + "/c HTTP/1.1\r\nHost: x\r\n\r\n",
	} {
		b := rawDo(t, srv.Addr().String(), in, nil)
		assert.Empty(t, b, name)
	}
}

// ---------------------------------------------------------------------------
// Upgrade and TLS modes

func TestUpgradeResponseBytes(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	acceptEcho(t, srv)
	conn := rawConn(t, srv.Addr().String(), nil)
	defer conn.Close()
	_, err := io.WriteString(conn, upgradeReq("/"+testSecret+"/c"))
	require.NoError(t, err)
	want := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + testAccept + "\r\n\r\n"
	got := make([]byte, len(want))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	assert.Equal(t, want, string(got), "no extensions, no subprotocol")
}

func TestUpgradeIgnoresExtensionsAndSubprotocolOffers(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	acceptEcho(t, srv)
	wc := wsUpgrade(t, srv.Addr().String(), nil, "/"+testSecret+"/c", "Sec-WebSocket-Extensions: permessage-deflate", "Sec-WebSocket-Protocol: chat")
	echoOnce(t, wc, []byte("works"))
}

func TestTLSModes(t *testing.T) {
	path := "/" + testSecret + "/c"
	t.Run("auto serves TLS and plain on one port", func(t *testing.T) {
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSAuto})
		acc := acceptEcho(t, srv)
		addr := srv.Addr().String()
		echoOnce(t, wsUpgrade(t, addr, insecureTLS(), path), []byte("over tls"))
		echoOnce(t, wsUpgrade(t, addr, nil, path), []byte("over plain"))
		for _, v := range []uint16{tls.VersionTLS12, tls.VersionTLS13} {
			cfg := insecureTLS()
			cfg.MinVersion, cfg.MaxVersion = v, v
			echoOnce(t, wsUpgrade(t, addr, cfg, path), []byte(fmt.Sprintf("tls %x", v)))
		}
		acc.next(t)
		// The decoy works on both too.
		assert.Equal(t, rawDo(t, addr, "GET / HTTP/1.1\r\nHost: x\r\n\r\n", nil)[:12], rawDo(t, addr, "GET / HTTP/1.1\r\nHost: x\r\n\r\n", insecureTLS())[:12])
	})
	t.Run("auto offers http/1.1 only, TLS 1.2 or later", func(t *testing.T) {
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSAuto})
		cfg := insecureTLS()
		cfg.NextProtos = []string{"h2", "http/1.1"}
		conn := rawConn(t, srv.Addr().String(), cfg)
		assert.Equal(t, "http/1.1", conn.(*tls.Conn).ConnectionState().NegotiatedProtocol)
		_ = conn.Close()
		old := insecureTLS()
		old.MinVersion, old.MaxVersion = tls.VersionTLS11, tls.VersionTLS11
		c, err := net.Dial("tcp", srv.Addr().String())
		require.NoError(t, err)
		defer c.Close()
		assert.Error(t, tls.Client(c, old).Handshake(), "TLS 1.1 is refused")
	})
	t.Run("off is plain only", func(t *testing.T) {
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, HeaderTimeout: 500 * time.Millisecond})
		acceptEcho(t, srv)
		echoOnce(t, wsUpgrade(t, srv.Addr().String(), nil, path), []byte("plain"))
		c, err := net.Dial("tcp", srv.Addr().String())
		require.NoError(t, err)
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		assert.Error(t, tls.Client(c, insecureTLS()).Handshake(), "no TLS on a plain listener")
	})
	t.Run("custom serves the operator certificate and TLS only", func(t *testing.T) {
		cs, err := fronttest.NewCertSet([]string{"front.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		require.NoError(t, err)
		cert, key := writeCert(t, cs)
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSCustom, CertFile: cert, KeyFile: key, HeaderTimeout: 500 * time.Millisecond})
		acceptEcho(t, srv)
		verify := &tls.Config{RootCAs: cs.Pool, ServerName: "front.example.com", MinVersion: tls.VersionTLS12}
		echoOnce(t, wsUpgrade(t, srv.Addr().String(), verify, path), []byte("custom"))
		// A plain request is not answered in plain.
		b := rawDo(t, srv.Addr().String(), "GET / HTTP/1.1\r\nHost: x\r\n\r\n", nil)
		assert.False(t, strings.HasPrefix(string(b), "HTTP/"), "got %q", b)
		// The decoy over TLS.
		got := rawDo(t, srv.Addr().String(), "GET /x HTTP/1.1\r\nHost: x\r\n\r\n", verify)
		assert.True(t, strings.HasPrefix(string(got), "HTTP/1.1 404"))
	})
}

func writeCert(t *testing.T, cs *fronttest.CertSet) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cs.Cert.Certificate[0]}), 0o600))
	der, err := x509.MarshalPKCS8PrivateKey(cs.Cert.PrivateKey.(*ecdsa.PrivateKey))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600))
	return certFile, keyFile
}

func TestCustomCertReload(t *testing.T) {
	cs1, err := fronttest.NewCertSet([]string{"one.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	require.NoError(t, err)
	cert, key := writeCert(t, cs1)
	r := &certReloader{certFile: cert, keyFile: key, every: 0}
	require.NoError(t, r.load())
	got, err := r.get(nil)
	require.NoError(t, err)
	assert.Equal(t, cs1.Cert.Certificate[0], got.Certificate[0])

	// A renewed pair is picked up without a restart.
	cs2, err := fronttest.NewCertSet([]string{"two.example.com"}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	require.NoError(t, err)
	c2, k2 := writeCert(t, cs2)
	b1, _ := os.ReadFile(c2)
	b2, _ := os.ReadFile(k2)
	future := time.Now().Add(time.Minute)
	require.NoError(t, os.WriteFile(cert, b1, 0o600))
	require.NoError(t, os.WriteFile(key, b2, 0o600))
	require.NoError(t, os.Chtimes(cert, future, future))
	require.NoError(t, os.Chtimes(key, future, future))
	got, err = r.get(nil)
	require.NoError(t, err)
	assert.Equal(t, cs2.Cert.Certificate[0], got.Certificate[0])

	// A broken file keeps the working certificate.
	require.NoError(t, os.WriteFile(cert, []byte("junk"), 0o600))
	later := future.Add(time.Minute)
	require.NoError(t, os.Chtimes(cert, later, later))
	got, err = r.get(nil)
	require.NoError(t, err)
	assert.Equal(t, cs2.Cert.Certificate[0], got.Certificate[0])
}

// ---------------------------------------------------------------------------
// Header cap and deadlines

func TestHeaderCap(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, FailLimit: -1})
	addr := srv.Addr().String()
	t.Run("a header over 8 KiB gets no answer", func(t *testing.T) {
		b := rawDo(t, addr, "GET / HTTP/1.1\r\nHost: x\r\nX-Pad: "+strings.Repeat("a", 9<<10)+"\r\n\r\n", nil)
		assert.Empty(t, b)
	})
	t.Run("many small headers over 8 KiB", func(t *testing.T) {
		var sb strings.Builder
		sb.WriteString("GET / HTTP/1.1\r\nHost: x\r\n")
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&sb, "X-H%03d: 0123456789abcdef\r\n", i)
		}
		sb.WriteString("\r\n")
		assert.Empty(t, rawDo(t, addr, sb.String(), nil))
	})
	t.Run("the request line counts", func(t *testing.T) {
		assert.Empty(t, rawDo(t, addr, "GET /"+strings.Repeat("a", 9<<10)+" HTTP/1.1\r\nHost: x\r\n\r\n", nil))
	})
	t.Run("a large header under the cap is fine", func(t *testing.T) {
		b := rawDo(t, addr, "GET / HTTP/1.1\r\nHost: x\r\nX-Pad: "+strings.Repeat("a", 7<<10)+"\r\n\r\n", nil)
		assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 200"))
	})
	t.Run("no newline at all, endless line", func(t *testing.T) {
		conn := rawConn(t, addr, nil)
		defer conn.Close()
		go func() {
			chunk := []byte(strings.Repeat("a", 1024))
			for i := 0; i < 64; i++ {
				if _, err := conn.Write(chunk); err != nil {
					return
				}
			}
		}()
		expectClosed(t, conn, 3*time.Second)
	})
}

func TestSlowlorisDeadline(t *testing.T) {
	for _, mode := range []string{config.FrontTLSOff, config.FrontTLSAuto} {
		t.Run(mode+" partial head", func(t *testing.T) {
			srv := startServer(t, ServerOptions{TLSMode: mode, HeaderTimeout: 300 * time.Millisecond})
			conn := rawConn(t, srv.Addr().String(), nil)
			defer conn.Close()
			_, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nX-Slow: ")
			require.NoError(t, err)
			start := time.Now()
			expectClosed(t, conn, 3*time.Second)
			assert.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond)
			assert.Less(t, time.Since(start), 2*time.Second)
		})
		t.Run(mode+" silent connection", func(t *testing.T) {
			srv := startServer(t, ServerOptions{TLSMode: mode, HeaderTimeout: 300 * time.Millisecond})
			conn := rawConn(t, srv.Addr().String(), nil)
			defer conn.Close()
			start := time.Now()
			expectClosed(t, conn, 3*time.Second)
			assert.Less(t, time.Since(start), 2*time.Second)
		})
	}
	t.Run("a trickle does not extend the deadline", func(t *testing.T) {
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, HeaderTimeout: 400 * time.Millisecond})
		conn := rawConn(t, srv.Addr().String(), nil)
		defer conn.Close()
		start := time.Now()
		go func() {
			for _, b := range []byte("GET / HTTP/1.1\r\nHost: x\r\nX-A: b\r\n") {
				if _, err := conn.Write([]byte{b}); err != nil {
					return
				}
				time.Sleep(60 * time.Millisecond)
			}
		}()
		expectClosed(t, conn, 3*time.Second)
		assert.Less(t, time.Since(start), 1500*time.Millisecond)
	})
	t.Run("a stalled TLS handshake", func(t *testing.T) {
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSAuto, HeaderTimeout: 300 * time.Millisecond})
		conn := rawConn(t, srv.Addr().String(), nil)
		defer conn.Close()
		_, err := conn.Write([]byte{0x16, 0x03, 0x01}) // the start of a ClientHello, then nothing
		require.NoError(t, err)
		start := time.Now()
		expectClosed(t, conn, 3*time.Second)
		assert.Less(t, time.Since(start), 2*time.Second)
	})
	t.Run("the deadline is cleared after the upgrade", func(t *testing.T) {
		srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, HeaderTimeout: 200 * time.Millisecond})
		acceptEcho(t, srv)
		wc := wsUpgrade(t, srv.Addr().String(), nil, "/"+testSecret+"/c")
		time.Sleep(500 * time.Millisecond)
		echoOnce(t, wc, []byte("still open after the header deadline"))
	})
}

func TestBytesBehindTheHeadMeanNoUpgrade(t *testing.T) {
	// A client must wait for the 101; frame bytes sent with the request make it
	// a decoy case.
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	b := rawDo(t, srv.Addr().String(), upgradeReq("/"+testSecret+"/c")+"\x82\x00", nil)
	assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 404"), "%q", b)
}

// ---------------------------------------------------------------------------
// Client address

func TestClientIPFromTrustedPeers(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	acc := acceptEcho(t, srv)
	path := "/" + testSecret + "/c"

	valid := map[string]string{
		"203.0.113.5":          "203.0.113.5",
		"  203.0.113.5  ":      "203.0.113.5",
		"8.8.4.4":              "8.8.4.4",
		"2001:db8::1":          "2001:db8::1",
		"2606:4700:4700::1111": "2606:4700:4700::1111",
		"::ffff:203.0.113.5":   "203.0.113.5",
	}
	for in, want := range valid {
		wc := wsUpgrade(t, srv.Addr().String(), nil, path, "CF-Connecting-IP: "+in)
		fc := acc.next(t)
		assert.True(t, fc.TrustedClientIP(), in)
		assert.Equal(t, want, fc.ClientIP().String(), in)
		// RemoteAddr is ip:peer-port: the port is the one of the TCP peer.
		clientPort := portOf(wc.LocalAddr())
		wantAddr := net.JoinHostPort(want, fmt.Sprint(clientPort))
		assert.Equal(t, wantAddr, fc.RemoteAddr().String(), in)
		assert.Equal(t, ViaFront, fc.Via())
	}

	invalid := []string{
		"10.0.0.1", "192.168.1.1", "172.16.5.5", "127.0.0.1", "127.9.9.9", "169.254.1.1", "0.0.0.0", "0.1.2.3",
		"224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "::ffff:10.0.0.1", "::ffff:127.0.0.1",
		"not-an-ip", "1.2.3", "1.2.3.4, 5.6.7.8", "1.2.3.4:80", "[2001:db8::1]", "203.0.113.5%eth0", "2001:db8::1%eth0", "", "0x7f000001",
	}
	for _, in := range invalid {
		wc := wsUpgrade(t, srv.Addr().String(), nil, path, "CF-Connecting-IP: "+in)
		fc := acc.next(t)
		assert.False(t, fc.TrustedClientIP(), "%q", in)
		assert.Equal(t, "127.0.0.1", fc.ClientIP().String(), "%q falls back to the TCP peer", in)
		assert.Equal(t, wc.LocalAddr().String(), fc.RemoteAddr().String(), "%q", in)
	}

	t.Run("two headers", func(t *testing.T) {
		wsUpgrade(t, srv.Addr().String(), nil, path, "CF-Connecting-IP: 203.0.113.5", "CF-Connecting-IP: 203.0.113.6")
		fc := acc.next(t)
		assert.False(t, fc.TrustedClientIP())
		assert.Equal(t, "127.0.0.1", fc.ClientIP().String())
	})
	t.Run("no header", func(t *testing.T) {
		wsUpgrade(t, srv.Addr().String(), nil, path)
		fc := acc.next(t)
		assert.False(t, fc.TrustedClientIP())
		assert.Equal(t, "127.0.0.1", fc.ClientIP().String())
	})
	t.Run("X-Forwarded-For is never used", func(t *testing.T) {
		wsUpgrade(t, srv.Addr().String(), nil, path, "X-Forwarded-For: 203.0.113.9", "True-Client-IP: 203.0.113.9")
		fc := acc.next(t)
		assert.False(t, fc.TrustedClientIP())
	})
}

func TestForgedHeaderFromAnUntrustedPeerIsIgnored(t *testing.T) {
	// Only 127.0.0.1 is a trusted peer here; the client connects from 127.0.0.2.
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, trust: trustOnly("127.0.0.1")})
	acc := acceptEcho(t, srv)
	path := "/" + testSecret + "/c"

	conn := dialFrom(t, srv.Addr().String(), "127.0.0.2")
	_, err := io.WriteString(conn, upgradeReq(path, "CF-Connecting-IP: 203.0.113.99"))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, 101, resp.StatusCode)
	wc := wsconn.New(conn, br, wsconn.Config{Client: true})
	t.Cleanup(func() { _ = wc.Close() })

	fc := acc.next(t)
	assert.False(t, fc.TrustedClientIP(), "an edge address or a direct client is not recorded as the node's address")
	assert.Equal(t, "127.0.0.2", fc.ClientIP().String(), "the TCP peer, not the forged value")
	assert.Equal(t, wc.LocalAddr().String(), fc.RemoteAddr().String())

	// The same request from the trusted peer is believed.
	wsUpgrade(t, srv.Addr().String(), nil, path, "CF-Connecting-IP: 203.0.113.99")
	fc = acc.next(t)
	assert.True(t, fc.TrustedClientIP())
	assert.Equal(t, "203.0.113.99", fc.ClientIP().String())
}

func trustOnly(ip string) func(netip.Addr) bool {
	want := netip.MustParseAddr(ip)
	return func(a netip.Addr) bool { return a == want }
}

// dialFrom connects to addr from the loopback address src (127.0.0.2 and up
// are local on Linux).
func dialFrom(t *testing.T, addr, src string) net.Conn {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(src)}, Timeout: 3 * time.Second}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		t.Skipf("cannot bind %s as a source address here: %v", src, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPeerTrusted(t *testing.T) {
	extra := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("2001:db8:ff::/48")}
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "127.5.5.5": true, "::1": true, "::ffff:127.0.0.1": true,
		"104.16.0.1": true, "173.245.48.10": true, "2606:4700::1": true, "2400:cb00::7": true, "::ffff:104.16.0.1": true,
		"198.51.100.77": true, "2001:db8:ff::9": true,
		"8.8.8.8": false, "198.51.101.1": false, "10.0.0.1": false, "2001:db8:fe::1": false, "192.168.0.1": false, "1.1.1.1": false,
	} {
		assert.Equal(t, want, peerTrusted(netip.MustParseAddr(ip), extra), ip)
	}
	assert.False(t, peerTrusted(netip.Addr{}, extra))
	assert.False(t, peerTrusted(netip.MustParseAddr("198.51.100.77"), nil), "no extra prefixes: not trusted")
}

func TestConnectingIPValidation(t *testing.T) {
	h := func(vals ...string) http.Header { return http.Header{headerCFConnectingIP: vals} }
	_, ok := connectingIP(http.Header{})
	assert.False(t, ok)
	_, ok = connectingIP(h())
	assert.False(t, ok)
	a, ok := connectingIP(h("203.0.113.5"))
	assert.True(t, ok)
	assert.Equal(t, "203.0.113.5", a.String())
	_, ok = connectingIP(h("203.0.113.5", "203.0.113.5"))
	assert.False(t, ok, "exactly one header")
	assert.True(t, publicUnicast(netip.MustParseAddr("1.1.1.1")))
	assert.False(t, publicUnicast(netip.Addr{}))
}

// ---------------------------------------------------------------------------
// Concurrency caps

func TestPreAuthQueueServesWhenASlotFrees(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, MaxPreAuth: 2, HeaderTimeout: 5 * time.Second})
	addr := srv.Addr().String()
	c1, c2 := rawConn(t, addr, nil), rawConn(t, addr, nil)
	poll(t, "two slots taken", func() bool { return srv.PreAuthInUse() == 2 })

	c3 := rawConn(t, addr, nil)
	_, err := io.WriteString(c3, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	require.NoError(t, err)
	_ = c3.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = c3.Read(make([]byte, 1))
	var ne net.Error
	require.True(t, errors.As(err, &ne) && ne.Timeout(), "queued, not served and not dropped: %v", err)

	_ = c1.Close() // a slot frees (its handler ends on EOF)
	_ = c3.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, _ := io.ReadAll(c3)
	assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 200"), "%q", b)
	_ = c2.Close()
}

func TestPreAuthQueueOverflowIsRejected(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, MaxPreAuth: 1, HeaderTimeout: 5 * time.Second})
	addr := srv.Addr().String()
	c1 := rawConn(t, addr, nil)
	poll(t, "the slot is taken", func() bool { return srv.PreAuthInUse() == 1 })
	c2 := rawConn(t, addr, nil) // queued
	poll(t, "one queued", func() bool { return srv.queued.Load() == 1 })
	c3 := rawConn(t, addr, nil) // the queue is full: dropped at once
	start := time.Now()
	expectClosed(t, c3, 3*time.Second)
	assert.Less(t, time.Since(start), time.Second)
	_ = c1.Close()
	_ = c2.Close()
}

func TestUntrustedPeersGetHalfTheSlotsAndNoQueue(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, MaxPreAuth: 4, HeaderTimeout: 5 * time.Second, trust: trustOnly("127.0.0.1")})
	addr := srv.Addr().String()
	u1, u2 := dialFrom(t, addr, "127.0.0.2"), dialFrom(t, addr, "127.0.0.2")
	poll(t, "two slots taken", func() bool { return srv.PreAuthInUse() == 2 })
	u3 := dialFrom(t, addr, "127.0.0.2") // over the untrusted share
	start := time.Now()
	expectClosed(t, u3, 3*time.Second)
	assert.Less(t, time.Since(start), time.Second)

	// The trusted peer (the CDN) is not starved by them.
	b := rawDo(t, addr, "GET / HTTP/1.1\r\nHost: x\r\n\r\n", nil)
	assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 200"))
	_ = u1.Close()
	_ = u2.Close()
	poll(t, "slots released", func() bool { return srv.PreAuthInUse() == 0 })
	// And the share frees up again.
	u4 := dialFrom(t, addr, "127.0.0.2")
	_, err := io.WriteString(u4, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	require.NoError(t, err)
	_ = u4.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, _ := io.ReadAll(u4)
	assert.True(t, strings.HasPrefix(string(got), "HTTP/1.1 200"))
}

func TestSlotIsReleasedWhenAcceptHandsTheConnOut(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, MaxPreAuth: 2})
	addr := srv.Addr().String()
	path := "/" + testSecret + "/c"
	w1 := wsUpgrade(t, addr, nil, path)
	w2 := wsUpgrade(t, addr, nil, path)
	assert.Equal(t, 2, srv.PreAuthInUse(), "upgraded but not yet accepted: still pre-auth")

	// A third client waits in the queue until Accept frees a slot.
	got := make(chan error, 1)
	go func() {
		conn := rawConn(t, addr, nil)
		defer conn.Close()
		_, _ = io.WriteString(conn, upgradeReq(path))
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err == nil && resp.StatusCode != 101 {
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("served before a slot was free: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	c1, err := srv.Accept()
	require.NoError(t, err)
	defer c1.Close()
	select {
	case err := <-got:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the queued client was not served after Accept")
	}
	c2, err := srv.Accept()
	require.NoError(t, err)
	defer c2.Close()
	c3, err := srv.Accept()
	require.NoError(t, err)
	defer c3.Close()
	poll(t, "all slots released", func() bool { return srv.PreAuthInUse() == 0 })
	_, _ = w1, w2
}

func TestBurstOf300DoesNotBlockTheControlPath(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, FailLimit: -1})
	acc := acceptEcho(t, srv)
	addr := srv.Addr().String()
	var wg sync.WaitGroup
	var mu sync.Mutex
	answered := 0
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				return
			}
			defer c.Close()
			_, _ = io.WriteString(c, upgradeReq("/"+testSecret+"/t/65000")) // a refusal-class request
			_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
			b, _ := io.ReadAll(c)
			if strings.HasPrefix(string(b), "HTTP/1.1 404") {
				mu.Lock()
				answered++
				mu.Unlock()
			}
		}()
	}
	// While the burst is in flight a valid control connection gets through.
	wc := wsUpgrade(t, addr, nil, "/"+testSecret+"/c")
	echoOnce(t, wc, []byte("control during a burst"))
	acc.next(t)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 300, answered, "every request of the burst is answered, none is dropped")
}

// ---------------------------------------------------------------------------
// Failure limiter

func TestFailureLimiterPerRealAddress(t *testing.T) {
	// Peers other than 127.0.0.1 are untrusted: they are charged by TCP address.
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, FailLimit: 3, FailWindow: time.Hour, trust: trustOnly("127.0.0.1")})
	acc := acceptEcho(t, srv)
	addr := srv.Addr().String()
	wrong := upgradeReq("/not-the-secret/c")
	send := func(src, req string) []byte {
		c := dialFrom(t, addr, src)
		_, err := io.WriteString(c, req)
		require.NoError(t, err)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		b, _ := io.ReadAll(c)
		return b
	}
	for i := 0; i < 3; i++ {
		assert.True(t, strings.HasPrefix(string(send("127.0.0.2", wrong)), "HTTP/1.1 404"), "request %d", i)
	}
	assert.Empty(t, send("127.0.0.2", wrong), "over the limit: dropped without an answer")
	assert.Empty(t, send("127.0.0.2", "GET /x HTTP/1.1\r\nHost: x\r\n\r\n"))
	// Pages that scanners and browsers ask for are not counted and still answered.
	assert.True(t, strings.HasPrefix(string(send("127.0.0.2", "GET / HTTP/1.1\r\nHost: x\r\n\r\n")), "HTTP/1.1 200"))
	// Another address is unaffected.
	assert.True(t, strings.HasPrefix(string(send("127.0.0.3", wrong)), "HTTP/1.1 404"))
	// And a request with the right secret is served even from the blocked address.
	c := dialFrom(t, addr, "127.0.0.2")
	_, err := io.WriteString(c, upgradeReq("/"+testSecret+"/c"))
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	assert.Equal(t, 101, resp.StatusCode, "the reconnect that fixes a broken node is never what locks it out")
	acc.next(t)
}

func TestMalformedRequestsFromUntrustedPeersCount(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, FailLimit: 2, FailWindow: time.Hour, trust: trustOnly("127.0.0.1")})
	addr := srv.Addr().String()
	for i := 0; i < 2; i++ {
		c := dialFrom(t, addr, "127.0.0.2")
		_, _ = io.WriteString(c, "garbage\r\n\r\n")
		expectClosed(t, c, 3*time.Second)
	}
	c := dialFrom(t, addr, "127.0.0.2")
	_, _ = io.WriteString(c, upgradeReq("/wrong/c"))
	assert.Empty(t, expectClosed(t, c, 3*time.Second))
}

func TestTrustedProxyIsNeverPenalised(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, FailLimit: 3, FailWindow: time.Hour})
	acc := acceptEcho(t, srv)
	addr := srv.Addr().String()
	// Timeouts, garbage and wrong secrets without a usable CF-Connecting-IP all
	// come from the shared edge address: none of it is charged to it.
	for i := 0; i < 20; i++ {
		b := rawDo(t, addr, upgradeReq("/wrong/c"), nil)
		assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 404"), "request %d", i)
	}
	for i := 0; i < 10; i++ {
		assert.Empty(t, rawDo(t, addr, "garbage\r\n\r\n", nil))
	}
	wsUpgrade(t, addr, nil, "/"+testSecret+"/c")
	acc.next(t)

	// With a usable header the failures are charged to the real client.
	for i := 0; i < 3; i++ {
		b := rawDo(t, addr, upgradeReq("/wrong/c", "CF-Connecting-IP: 203.0.113.50"), nil)
		assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 404"))
	}
	assert.Empty(t, rawDo(t, addr, upgradeReq("/wrong/c", "CF-Connecting-IP: 203.0.113.50"), nil), "that client is dropped")
	b := rawDo(t, addr, upgradeReq("/wrong/c", "CF-Connecting-IP: 203.0.113.51"), nil)
	assert.True(t, strings.HasPrefix(string(b), "HTTP/1.1 404"), "another client behind the same edge is not")
	// The blocked client's own valid control connection is still accepted.
	wsUpgrade(t, addr, nil, "/"+testSecret+"/c", "CF-Connecting-IP: 203.0.113.50")
	fc := acc.next(t)
	assert.Equal(t, "203.0.113.50", fc.ClientIP().String())
}

func TestFailLimiterWindowAndTable(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newFailLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	ip := netip.MustParseAddr("203.0.113.1")
	assert.False(t, l.blocked(ip))
	for i := 0; i < 2; i++ {
		l.fail(ip)
		assert.False(t, l.blocked(ip))
	}
	l.fail(ip)
	assert.True(t, l.blocked(ip))
	assert.False(t, l.blocked(netip.MustParseAddr("203.0.113.2")))
	now = now.Add(59 * time.Second)
	assert.True(t, l.blocked(ip))
	now = now.Add(2 * time.Second)
	assert.False(t, l.blocked(ip), "the window ended")
	l.fail(ip)
	assert.False(t, l.blocked(ip), "and the count starts again")

	// Invalid addresses and a zero limit are never tracked.
	l.fail(netip.Addr{})
	assert.False(t, l.blocked(netip.Addr{}))
	off := newFailLimiter(-1, time.Minute)
	for i := 0; i < 10; i++ {
		off.fail(ip)
	}
	assert.False(t, off.blocked(ip))

	// The table is bounded: stale entries are swept, live ones are never evicted.
	l = newFailLimiter(1, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < maxTrackedIPs; i++ {
		a := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		l.fail(a)
	}
	extra := netip.MustParseAddr("198.51.100.1")
	l.fail(extra)
	assert.False(t, l.blocked(extra), "the table is full of live entries: the new one is not tracked")
	assert.Len(t, l.m, maxTrackedIPs)
	now = now.Add(2 * time.Minute)
	l.fail(extra)
	assert.True(t, l.blocked(extra), "stale entries were swept")
	assert.Less(t, len(l.m), 10)
}

// ---------------------------------------------------------------------------
// Secrecy

func TestSecretNeverInLogsOrErrors(t *testing.T) {
	logger, buf := debugLogger()
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSAuto, Logger: logger, HeaderTimeout: 300 * time.Millisecond, FailLimit: 2, FailWindow: time.Hour})
	acceptEcho(t, srv)
	addr := srv.Addr().String()
	secretish := []string{testSecret, "another-wrong-secret-AAAAAAAAAAAA"}

	for _, s := range secretish {
		rawDo(t, addr, upgradeReq("/"+s+"/c"), nil)
		rawDo(t, addr, upgradeReq("/"+s+"/c"), insecureTLS())
		rawDo(t, addr, "GET /"+s+"/c HTTP/1.1\r\nHost: x\r\n\r\n", nil)
		rawDo(t, addr, "GET /"+s+"/"+strings.Repeat("a", 9<<10)+" HTTP/1.1\r\n\r\n", nil) // too large
		rawDo(t, addr, "GET /"+s+"/c HTTP/9.9\r\n\r\n", nil)                              // malformed
	}
	wsUpgrade(t, addr, nil, "/"+testSecret+"/c")
	wsUpgrade(t, addr, insecureTLS(), "/"+testSecret+"/c")
	c := rawConn(t, addr, nil) // idle until the deadline
	expectClosed(t, c, 3*time.Second)
	time.Sleep(50 * time.Millisecond)

	out := buf.String()
	for _, s := range secretish {
		assert.NotContains(t, out, s)
		assert.NotContains(t, out, s[:12])
	}
	assert.NotContains(t, out, "/c")
	assert.NotContains(t, out, "GET")
	assert.Contains(t, out, "front:", "something was logged at debug level")

	_, err := NewServer(nil, ServerOptions{Secret: testSecret})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), testSecret)
}

// ---------------------------------------------------------------------------
// Lifecycle

func TestServerCloseSemantics(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	addr := srv.Addr().String()
	path := "/" + testSecret + "/c"

	// An upgraded connection that Accept never took, and one still in the head phase.
	pending := wsUpgrade(t, addr, nil, path)
	silent := rawConn(t, addr, nil)
	poll(t, "two slots", func() bool { return srv.PreAuthInUse() == 2 })

	// Accept blocks until Close.
	// (A third upgraded connection would be returned, so use a fresh server for it.)
	srv2 := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	errc := make(chan error, 1)
	go func() { _, err := srv2.Accept(); errc <- err }()
	select {
	case err := <-errc:
		t.Fatalf("Accept returned early: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, srv2.Close())
	select {
	case err := <-errc:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(3 * time.Second):
		t.Fatal("Accept did not return after Close")
	}

	require.NoError(t, srv.Close())
	require.NoError(t, srv.Close(), "idempotent")
	_, err := srv.Accept()
	assert.ErrorIs(t, err, net.ErrClosed)
	assert.Zero(t, srv.PreAuthInUse(), "every slot is released")

	// Both connections were closed by the server.
	_ = silent.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = silent.Read(make([]byte, 1))
	require.Error(t, err)
	var ne net.Error
	assert.False(t, errors.As(err, &ne) && ne.Timeout())
	_ = pending.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = pending.Read(make([]byte, 1))
	require.Error(t, err)
	assert.False(t, errors.As(err, &ne) && ne.Timeout())
	_, err = net.DialTimeout("tcp", addr, 200*time.Millisecond)
	assert.Error(t, err, "the listener is closed")
}

func TestConnsHandedOutSurviveClose(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	wc := wsUpgrade(t, srv.Addr().String(), nil, "/"+testSecret+"/c")
	c, err := srv.Accept()
	require.NoError(t, err)
	defer c.Close()
	require.NoError(t, srv.Close())
	_, err = c.Write([]byte("still mine"))
	require.NoError(t, err)
	got := make([]byte, 10)
	_ = wc.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = io.ReadFull(wc, got)
	require.NoError(t, err)
	assert.Equal(t, "still mine", string(got))
}

func TestConnIsHashableAndTyped(t *testing.T) {
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	wsUpgrade(t, srv.Addr().String(), nil, "/"+testSecret+"/c")
	c, err := srv.Accept()
	require.NoError(t, err)
	defer c.Close()
	m := map[net.Conn]int{c: 1}
	assert.Equal(t, 1, m[c])
	fc, ok := c.(*Conn)
	require.True(t, ok)
	assert.Equal(t, ViaFront, fc.Via())
	var via interface{ Via() string } = fc
	assert.Equal(t, "front", via.Via())
	_, _, err = net.SplitHostPort(c.RemoteAddr().String())
	assert.NoError(t, err)
	_, _, err = net.SplitHostPort(c.LocalAddr().String())
	assert.NoError(t, err)
}

// failingListener returns the scripted Accept results, then blocks until closed.
type failingListener struct {
	net.Listener
	mu      sync.Mutex
	script  []error
	closed  chan struct{}
	once    sync.Once
	accepts int
}

func newFailingListener(t *testing.T, script ...error) *failingListener {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return &failingListener{Listener: inner, script: script, closed: make(chan struct{})}
}

func (f *failingListener) Accept() (net.Conn, error) {
	f.mu.Lock()
	f.accepts++
	if len(f.script) > 0 {
		err := f.script[0]
		f.script = f.script[1:]
		f.mu.Unlock()
		return nil, err
	}
	f.mu.Unlock()
	return f.Listener.Accept()
}

func (f *failingListener) Close() error {
	f.once.Do(func() { close(f.closed) })
	return f.Listener.Close()
}

func TestServerSurvivesTemporaryAcceptErrors(t *testing.T) {
	fl := newFailingListener(t,
		&net.OpError{Op: "accept", Err: timeoutErr{}},
		&net.OpError{Op: "accept", Err: syscall.EMFILE},
	)
	srv, err := NewServer(fl, ServerOptions{Secret: testSecret, TLSMode: config.FrontTLSOff})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	acceptEcho(t, srv)
	echoOnce(t, wsUpgrade(t, fl.Addr().String(), nil, "/"+testSecret+"/c"), []byte("after the retries"))
}

func TestServerReportsAListenerThatDies(t *testing.T) {
	boom := errors.New("listener exploded")
	fl := newFailingListener(t, boom)
	srv, err := NewServer(fl, ServerOptions{Secret: testSecret, TLSMode: config.FrontTLSOff})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	_, err = srv.Accept()
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, net.ErrClosed, "a failure is not a close")
	require.NoError(t, srv.Close())
}

// ---------------------------------------------------------------------------
// wsconn settings of an upgraded connection

func TestUpgradedConnIdleWatchdog(t *testing.T) {
	// The server pings every 20 ms; a client that never reads cannot answer, so
	// after 150 ms without a frame the connection is cut: dead peers behind a
	// CDN are noticed in seconds, not minutes.
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, WSIdleTimeout: 150 * time.Millisecond, PingInterval: 20 * time.Millisecond})
	conn := rawConn(t, srv.Addr().String(), nil)
	defer conn.Close()
	_, err := io.WriteString(conn, upgradeReq("/"+testSecret+"/c"))
	require.NoError(t, err)
	c, err := srv.Accept()
	require.NoError(t, err)
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, err = c.Read(make([]byte, 1))
	require.Error(t, err)
	assert.ErrorIs(t, err, wsconn.ErrIdleTimeout)
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestUpgradedConnPingsKeepItAlive(t *testing.T) {
	// The same server against a client that reads (and so pongs): no cut.
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff, WSIdleTimeout: 150 * time.Millisecond, PingInterval: 20 * time.Millisecond})
	acceptEcho(t, srv)
	wc := wsUpgrade(t, srv.Addr().String(), nil, "/"+testSecret+"/c")
	go func() { _, _ = io.Copy(io.Discard, wc) }() // keeps reading, so it answers pings
	time.Sleep(600 * time.Millisecond)
	_, err := wc.Write([]byte("alive"))
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// End to end through the fake CDN: the matrix of error paths

func TestWrongRequestsThroughTheCDNGetTheDecoy(t *testing.T) {
	st := newStack(t, stackOpts{cdn: fronttest.Options{OriginTLS: true}})
	conn, err := net.Dial("tcp", st.cdn.Addr())
	require.NoError(t, err)
	tc := tls.Client(conn, &tls.Config{RootCAs: st.cdn.CAPool(), ServerName: "front.example.com"})
	require.NoError(t, tc.Handshake())
	defer tc.Close()
	_, err = io.WriteString(tc, "GET /robots.txt HTTP/1.1\r\nHost: front.example.com\r\n\r\n")
	require.NoError(t, err)
	_ = tc.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, decoyPage, string(body))
}
