package front

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front/fronttest"
	"github.com/localroot4/deyroute/internal/wsconn"
)

// ---------------------------------------------------------------------------
// Target

func TestNewTarget(t *testing.T) {
	good := []struct {
		name                       string
		hp, scheme, edge, secret   string
		host                       string
		port                       int
		tls                        bool
		wantEdge, wantAddr, wantHH string
	}{
		{"https port derives wss", "front.example.com:2053", "", "", testSecret, "front.example.com", 2053, true, "", "front.example.com:2053", "front.example.com:2053"},
		{"443 omits the port in Host", "front.example.com:443", "", "", testSecret, "front.example.com", 443, true, "", "front.example.com:443", "front.example.com"},
		{"http port derives ws", "front.example.com:8080", "", "", testSecret, "front.example.com", 8080, false, "", "front.example.com:8080", "front.example.com:8080"},
		{"80 omits the port in Host", "front.example.com:80", "", "", testSecret, "front.example.com", 80, false, "", "front.example.com:80", "front.example.com"},
		{"explicit wss on a lab port", "localhost:9443", "wss", "", testSecret, "localhost", 9443, true, "", "localhost:9443", "localhost:9443"},
		{"explicit ws overrides the port", "front.example.com:2053", "ws", "", testSecret, "front.example.com", 2053, false, "", "front.example.com:2053", "front.example.com:2053"},
		{"edge ip", "front.example.com:443", "", "104.16.1.1", testSecret, "front.example.com", 443, true, "104.16.1.1", "front.example.com:443", "front.example.com"},
		{"edge ip v6", "front.example.com:443", "", "2606:4700::1", testSecret, "front.example.com", 443, true, "2606:4700::1", "front.example.com:443", "front.example.com"},
		{"ip literal host", "127.0.0.1:2096", "", "", testSecret, "127.0.0.1", 2096, true, "", "127.0.0.1:2096", "127.0.0.1:2096"},
		{"ipv6 host", "[2001:db8::1]:8443", "", "", testSecret, "2001:db8::1", 8443, true, "", "[2001:db8::1]:8443", "[2001:db8::1]:8443"},
	}
	for _, c := range good {
		t.Run(c.name, func(t *testing.T) {
			tg, err := NewTarget(c.hp, c.scheme, c.edge, c.secret)
			require.NoError(t, err)
			assert.Equal(t, c.host, tg.Host)
			assert.Equal(t, c.port, tg.Port)
			assert.Equal(t, c.tls, tg.TLS)
			assert.Equal(t, c.wantEdge, tg.EdgeIP)
			assert.Equal(t, c.secret, tg.Secret)
			assert.Equal(t, c.wantAddr, tg.Addr())
			assert.Equal(t, c.wantHH, tg.hostHeader())
		})
	}

	bad := []struct {
		name                     string
		hp, scheme, edge, secret string
	}{
		{"no port", "front.example.com", "", "", testSecret},
		{"empty host", ":443", "", "", testSecret},
		{"port zero", "front.example.com:0", "wss", "", testSecret},
		{"port too big", "front.example.com:65536", "wss", "", testSecret},
		{"port not a number", "front.example.com:https", "wss", "", testSecret},
		{"negative port", "front.example.com:-1", "wss", "", testSecret},
		{"host with a space", "front example.com:443", "", "", testSecret},
		{"host with a slash", "front.example.com/x:443", "", "", testSecret},
		{"numeric last label", "front.example.1234:443", "", "", testSecret},
		{"empty label", "front..example.com:443", "", "", testSecret},
		{"leading hyphen", "-front.example.com:443", "", "", testSecret},
		{"zoned ipv6", "[fe80::1%eth0]:443", "", "", testSecret},
		{"scheme cannot be derived", "front.example.com:9443", "", "", testSecret},
		{"bad scheme", "front.example.com:443", "https", "", testSecret},
		{"edge is a name", "front.example.com:443", "", "cloudflare.com", testSecret},
		{"edge unspecified", "front.example.com:443", "", "0.0.0.0", testSecret},
		{"edge multicast", "front.example.com:443", "", "224.0.0.1", testSecret},
		{"edge zoned", "front.example.com:443", "", "fe80::1%lo", testSecret},
		{"empty secret", "front.example.com:443", "", "", ""},
		{"secret with a slash", "front.example.com:443", "", "", "ab/cd"},
		{"secret with a question mark", "front.example.com:443", "", "", "ab?cd"},
		{"secret with a space", "front.example.com:443", "", "", "ab cd"},
		{"secret with a newline", "front.example.com:443", "", "", "ab\r\nX: y"},
		{"secret too long", "front.example.com:443", "", "", strings.Repeat("a", MaxSecretLen+1)},
	}
	for _, c := range bad {
		t.Run("bad "+c.name, func(t *testing.T) {
			_, err := NewTarget(c.hp, c.scheme, c.edge, c.secret)
			require.Error(t, err)
			if c.secret != "" {
				assert.NotContains(t, err.Error(), c.secret, "an error never carries the secret")
			}
		})
	}
	_, err := NewTarget("front.example.com:443", "", "", "bad secret value")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "bad secret value")
}

func TestValidSecret(t *testing.T) {
	assert.True(t, ValidSecret("a"))
	assert.True(t, ValidSecret("AZaz09-_"))
	assert.True(t, ValidSecret(strings.Repeat("x", MaxSecretLen)))
	for _, s := range []string{"", "a.b", "a b", "a@b", "a#b", "a:b", "é", strings.Repeat("x", MaxSecretLen+1)} {
		assert.False(t, ValidSecret(s), "%q", s)
	}
}

func TestSchemeFollowsConfigPorts(t *testing.T) {
	for _, p := range config.CloudflareHTTPSPorts() {
		tg, err := NewTarget(fmt.Sprintf("a.example.com:%d", p), "", "", testSecret)
		require.NoError(t, err)
		assert.True(t, tg.TLS, p)
	}
	for _, p := range config.CloudflareHTTPPorts() {
		tg, err := NewTarget(fmt.Sprintf("a.example.com:%d", p), "", "", testSecret)
		require.NoError(t, err)
		assert.False(t, tg.TLS, p)
	}
}

func TestUpgradeRequestBytes(t *testing.T) {
	tg, err := NewTarget("front.example.com:2053", "", "", testSecret)
	require.NoError(t, err)
	got := upgradeRequest(tg, controlPath, "dGhlIHNhbXBsZSBub25jZQ==")
	want := "GET /" + testSecret + "/c HTTP/1.1\r\n" +
		"Host: front.example.com:2053\r\n" +
		"User-Agent: " + userAgent + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Origin: https://front.example.com\r\n\r\n"
	assert.Equal(t, want, got)
	assert.NotContains(t, strings.ToLower(got), "extensions")
	assert.NotContains(t, strings.ToLower(got), "protocol")
	assert.NotContains(t, strings.ToLower(got), "cf-")
	assert.Contains(t, userAgent, "Mozilla/5.0")
	assert.NotContains(t, strings.ToLower(userAgent), "dey")

	tg, _ = NewTarget("front.example.com:443", "", "", testSecret)
	assert.Contains(t, upgradeRequest(tg, controlPath, "k"), "Host: front.example.com\r\n")
	tg, _ = NewTarget("[2001:db8::1]:8443", "", "", testSecret)
	got = upgradeRequest(tg, controlPath, "k")
	assert.Contains(t, got, "Host: [2001:db8::1]:8443\r\n")
	assert.Contains(t, got, "Origin: https://[2001:db8::1]\r\n")
}

// ---------------------------------------------------------------------------
// Dial through the fake CDN

func TestDialControlThroughCDNMatrix(t *testing.T) {
	cases := []struct {
		name string
		so   stackOpts
	}{
		{"wss, origin tls auto (Full)", stackOpts{cdn: fronttest.Options{OriginTLS: true}}},
		{"wss, origin plain on an auto server (sniffed)", stackOpts{}},
		{"wss, origin tls off (Flexible)", stackOpts{server: ServerOptions{TLSMode: config.FrontTLSOff}}},
		{"ws, plain clients, origin plain", stackOpts{cdn: fronttest.Options{PlainClients: true}, server: ServerOptions{TLSMode: config.FrontTLSOff}}},
		{"ws, plain clients, origin tls", stackOpts{cdn: fronttest.Options{PlainClients: true, OriginTLS: true}}},
		{"re-chunked to 3 bytes", stackOpts{cdn: fronttest.Options{Chunk: 3}}},
		{"latency", stackOpts{cdn: fronttest.Options{Delay: 15 * time.Millisecond}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newStack(t, c.so)
			conn := st.dial(t)
			echoOnce(t, conn, []byte("control-like hello"))
			echoOnce(t, conn, bytes.Repeat([]byte("0123456789"), 5000)) // many frames
			fc := st.acc.next(t)
			assert.Equal(t, ViaFront, fc.Via())
			assert.True(t, fc.TrustedClientIP(), "the CDN is on loopback, so its header is trusted")
			assert.Equal(t, "203.0.113.7", fc.ClientIP().String())
			assert.EqualValues(t, 1, st.cdn.Stats().Upgrades)
		})
	}
}

func TestDialControlEdgeIPAndRequestShape(t *testing.T) {
	st := newStack(t, stackOpts{cdn: fronttest.Options{OriginTLS: true}})
	conn := st.dial(t)
	echoOnce(t, conn, []byte("x"))

	reqs := st.cdn.Requests()
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.Equal(t, "front.example.com", r.SNI, "the SNI is the front domain even though an edge address was dialed")
	assert.Equal(t, []string{"http/1.1"}, r.ALPN, "http/1.1 only")
	assert.GreaterOrEqual(t, r.TLSVersion, uint16(tls.VersionTLS12))
	assert.Equal(t, fmt.Sprintf("front.example.com:%d", st.cdn.Port()), r.Host)
	assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
	assert.Equal(t, "https://front.example.com", r.Header.Get("Origin"))
	assert.Equal(t, "websocket", r.Header.Get("Upgrade"))
	assert.Equal(t, "13", r.Header.Get("Sec-WebSocket-Version"))
	assert.Empty(t, r.Header.Values("Sec-WebSocket-Extensions"))
	assert.Empty(t, r.Header.Values("Sec-WebSocket-Protocol"))
	assert.Empty(t, r.Header.Values("Cf-Connecting-Ip"), "the node sends no cf-* header")
	assert.Equal(t, "GET", r.Method)
}

func TestDialControlNoEdgeIPUsesHost(t *testing.T) {
	st := newStack(t, stackOpts{})
	st.target.Host = "localhost" // in the certificate's names; resolved by the system
	st.target.EdgeIP = ""
	conn := st.dial(t)
	echoOnce(t, conn, []byte("via the host name"))
	assert.Equal(t, "localhost", st.cdn.Requests()[0].SNI)
}

func TestDialControlLargeBothDirections(t *testing.T) {
	const size = 5 << 20
	for _, name := range []string{"plain chunks", "small chunks"} {
		t.Run(name, func(t *testing.T) {
			so := stackOpts{cdn: fronttest.Options{OriginTLS: true}}
			if name == "small chunks" {
				so.cdn.Chunk = 1000
			}
			// This test reads what the hub sends and checks what it receives, so it
			// uses the server's Accept directly instead of the echo acceptor.
			srv := startServer(t, ServerOptions{})
			cdn, err := fronttest.NewCDN(fronttest.Options{OriginAddr: srv.Addr().String(), OriginTLS: true, Chunk: so.cdn.Chunk})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cdn.Close() })

			up := make([]byte, size)   // node -> hub
			down := make([]byte, size) // hub -> node
			_, _ = rand.Read(up)
			_, _ = rand.Read(down)
			upSum, downSum := sha256.Sum256(up), sha256.Sum256(down)

			type result struct {
				sum [32]byte
				n   int64
				err error
			}
			hubGot := make(chan result, 1)
			var hubWG sync.WaitGroup
			hubWG.Add(1)
			go func() {
				defer hubWG.Done()
				c, err := srv.Accept()
				if err != nil {
					hubGot <- result{err: err}
					return
				}
				defer c.Close()
				go func() { _, _ = c.Write(down) }()
				h := sha256.New()
				n, err := io.CopyN(h, c, size)
				var r result
				copy(r.sum[:], h.Sum(nil))
				r.n, r.err = n, err
				hubGot <- r
				// Keep the connection open until the node has read everything.
				_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
				_, _ = io.Copy(io.Discard, c)
			}()

			d := &Dialer{RootCAs: cdn.CAPool(), UpgradeTimeout: 10 * time.Second}
			conn, err := d.DialControl(context.Background(), Target{Host: "front.example.com", Port: cdn.Port(), TLS: true, EdgeIP: "127.0.0.1", Secret: testSecret})
			require.NoError(t, err)
			go func() { _, _ = conn.Write(up) }()
			h := sha256.New()
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			n, err := io.CopyN(h, conn, size)
			require.NoError(t, err)
			require.EqualValues(t, size, n)
			assert.Equal(t, downSum[:], h.Sum(nil), "hub to node intact")

			select {
			case r := <-hubGot:
				require.NoError(t, r.err)
				assert.Equal(t, upSum[:], r.sum[:], "node to hub intact")
			case <-time.After(30 * time.Second):
				t.Fatal("the hub did not receive the upload")
			}
			require.NoError(t, conn.Close())
			hubWG.Wait()
		})
	}
}

func TestDialControlWrongSecretIsPermanent404(t *testing.T) {
	st := newStack(t, stackOpts{cdn: fronttest.Options{OriginTLS: true}})
	st.target.Secret = "a-completely-wrong-secret-0123456789"
	_, err := st.dialer.DialControl(context.Background(), st.target)
	require.Error(t, err)
	de := AsDialError(err)
	require.NotNil(t, de)
	assert.Equal(t, ClassStatus, de.Class)
	assert.Equal(t, 404, de.Status)
	assert.True(t, de.Permanent())
	assert.Zero(t, de.RetryAfter())
	assert.True(t, deyerr.HasCode(de.ToDEY(), deyerr.N017))
	assert.NotContains(t, err.Error(), "a-completely-wrong-secret")
	assert.NotContains(t, de.ToDEY().Error(), "a-completely-wrong-secret")
	assert.NotContains(t, de.ToDEY().Error(), testSecret)
}

func TestDialControlTLSModeMismatchIs525(t *testing.T) {
	// The CDN speaks TLS to an origin that is plain only: Cloudflare 525.
	st := newStack(t, stackOpts{server: ServerOptions{TLSMode: config.FrontTLSOff}, cdn: fronttest.Options{OriginTLS: true}})
	_, err := st.dialer.DialControl(context.Background(), st.target)
	de := AsDialError(err)
	require.NotNil(t, de, "%v", err)
	assert.Equal(t, 525, de.Status)
	assert.Contains(t, de.Reason, "tls mode")
	assert.False(t, de.Permanent())
}

func TestDialErrorTable(t *testing.T) {
	cases := []struct {
		name      string
		canned    *fronttest.Canned
		status    int
		permanent bool
		retry     time.Duration
		cfCode    int
		reason    string
		code      deyerr.Code
	}{
		{"301", fronttest.Redirect(301, "https://x.example.com/"+testSecret+"/c"), 301, true, 0, 0, "redirect", deyerr.N017},
		{"302", fronttest.Redirect(302, "/x"), 302, true, 0, 0, "redirect", deyerr.N017},
		{"307", fronttest.Redirect(307, "/x"), 307, true, 0, 0, "redirect", deyerr.N017},
		{"308", fronttest.Redirect(308, "/x"), 308, true, 0, 0, "redirect", deyerr.N017},
		{"400", fronttest.Status(400), 400, true, 0, 0, "WebSockets", deyerr.N017},
		{"426", fronttest.Status(426), 426, true, 0, 0, "WebSockets", deyerr.N017},
		{"403 challenge", fronttest.Challenge(), 403, true, 0, 0, "challenge", deyerr.N017},
		{"403 waf 1010", fronttest.Blocked(1010), 403, false, 0, 1010, "Browser Integrity", deyerr.N017},
		{"403 plain", fronttest.Status(403), 403, false, 0, 0, "forbidden", deyerr.N017},
		{"404", fronttest.NotFound(), 404, true, 0, 0, "secret path", deyerr.N017},
		{"429 retry-after", fronttest.RateLimited("17"), 429, false, 17 * time.Second, 1015, "rate limited", deyerr.N017},
		{"429 no retry-after", fronttest.RateLimited(""), 429, false, 0, 1015, "rate limited", deyerr.N017},
		{"500", fronttest.Status(500), 500, false, 0, 0, "unexpected", deyerr.N017},
		{"503 plain", fronttest.Status(503), 503, false, 0, 0, "unavailable", deyerr.N017},
		{"520", fronttest.OriginError(520), 520, false, 0, 0, "520", deyerr.N017},
		{"521", fronttest.OriginError(521), 521, false, 0, 0, "firewall", deyerr.N017},
		{"522", fronttest.OriginError(522), 522, false, 0, 0, "timed out", deyerr.N017},
		{"523", fronttest.OriginError(523), 523, false, 0, 0, "DNS record", deyerr.N017},
		{"524", fronttest.OriginError(524), 524, false, 0, 0, "524", deyerr.N017},
		{"525", fronttest.OriginError(525), 525, false, 0, 0, "tls mode", deyerr.N017},
		{"526", fronttest.OriginError(526), 526, false, 0, 0, "Full (strict)", deyerr.N017},
		{"527", fronttest.OriginError(527), 527, false, 0, 0, "527", deyerr.N017},
		{"530 1016", fronttest.CloudflareError(1016), 530, false, 0, 1016, "origin DNS error", deyerr.N017},
		{"530 1033", fronttest.CloudflareError(1033), 530, false, 0, 1033, "tunnel", deyerr.N017},
		{"530 unknown code", fronttest.CloudflareError(1999), 530, false, 0, 1999, "1999", deyerr.N017},
	}
	cdn, err := fronttest.NewCDN(fronttest.Options{OriginAddr: "127.0.0.1:1", PlainClients: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cdn.Close() })
	tg := Target{Host: "front.example.com", Port: cdn.Port(), EdgeIP: "127.0.0.1", Secret: testSecret}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			canned := c.canned
			cdn.Configure(func(o *fronttest.Options) { o.Canned = canned })
			_, err := DialControl(context.Background(), tg)
			require.Error(t, err)
			de := AsDialError(err)
			require.NotNil(t, de)
			assert.Equal(t, ClassStatus, de.Class)
			assert.Equal(t, c.status, de.Status)
			assert.Equal(t, c.permanent, de.Permanent(), "permanent")
			assert.Equal(t, c.retry, de.RetryAfter())
			assert.Equal(t, c.cfCode, de.CFCode)
			assert.Contains(t, de.Reason, c.reason)
			assert.Equal(t, tg.Addr(), de.Addr)

			dey := de.ToDEY()
			assert.True(t, deyerr.HasCode(dey, c.code), "%v", dey)
			e := deyerr.As(dey)
			require.NotNil(t, e)
			assert.Equal(t, tg.Addr(), e.Params["addr"])
			assert.Equal(t, c.status, e.Params["status"])
			assert.Equal(t, de.Reason, e.Params["reason"])
			assert.Contains(t, e.Message(), tg.Addr())
			var back *DialError
			require.True(t, errors.As(dey, &back), "the DialError stays reachable")

			for _, s := range []string{err.Error(), dey.Error(), de.Reason, e.Why(), e.Fix(), e.Format(false)} {
				assert.NotContains(t, s, testSecret, "the secret is never in a message")
				assert.NotContains(t, s, "/c ", "nor the path")
			}
		})
	}
}

func TestDialErrorRetryAfterDate(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	assert.Equal(t, 90*time.Second, parseRetryAfter("90", now))
	assert.Equal(t, 30*time.Second, parseRetryAfter(now.Add(30*time.Second).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter(now.Add(-time.Hour).Format(http.TimeFormat), now), "a date in the past")
	assert.Zero(t, parseRetryAfter("-5", now))
	assert.Zero(t, parseRetryAfter("soon", now))
	assert.Zero(t, parseRetryAfter("", now))
	assert.Equal(t, maxRetryAfter, parseRetryAfter("99999999999", now), "clamped")
	assert.Zero(t, parseRetryAfter("99999999999999999999999", now), "not a number any more: no hint")
}

func TestDialErrorBodyCap(t *testing.T) {
	cdn, err := fronttest.NewCDN(fronttest.Options{OriginAddr: "127.0.0.1:1", PlainClients: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cdn.Close() })
	tg := Target{Host: "front.example.com", Port: cdn.Port(), EdgeIP: "127.0.0.1", Secret: testSecret}

	// The code inside the first 2 KiB is found; one that comes later is not read.
	inside := &fronttest.Canned{Status: 530, Body: strings.Repeat("x", 1500) + " Error 1016 "}
	outside := &fronttest.Canned{Status: 530, Body: strings.Repeat("x", 2500) + " Error 1016 "}
	cdn.Configure(func(o *fronttest.Options) { o.Canned = inside })
	_, err = DialControl(context.Background(), tg)
	assert.Equal(t, 1016, AsDialError(err).CFCode)
	cdn.Configure(func(o *fronttest.Options) { o.Canned = outside })
	_, err = DialControl(context.Background(), tg)
	assert.Zero(t, AsDialError(err).CFCode)
}

func TestCFErrorCodeExtraction(t *testing.T) {
	for in, want := range map[string]int{
		"<title>Error 1020</title>":            1020,
		"error code: 1015":                     1015,
		"Error code 1010":                      1010,
		"ERROR 1016 Origin DNS error":          1016,
		"Error 521":                            0, // a 52x is a status, not a 1xxx page
		"no code here":                         0,
		"error code: 10155":                    0,
		"<span>Error&nbsp;1020</span>":         0,
		"<h1>Error</h1><h2>1020</h2>":          0,
		"x Error 1003 Direct IP access y":      1003,
		"Error 2020":                           0,
		"Cloudflare Ray ID: 1234 Error 1033 z": 1033,
	} {
		assert.Equal(t, want, cfErrorCode([]byte(in)), in)
	}
}

// ---------------------------------------------------------------------------
// Protocol-level failures against a hand-made origin

// rawOrigin answers each connection with whatever reply returns for the
// request head.
func rawOrigin(t *testing.T, reply func(c net.Conn, head string)) Target {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				br := bufio.NewReader(c)
				var head strings.Builder
				for {
					line, err := br.ReadString('\n')
					head.WriteString(line)
					if err != nil || line == "\r\n" {
						break
					}
				}
				reply(c, head.String())
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return Target{Host: "127.0.0.1", Port: portOf(ln.Addr()), Secret: testSecret}
}

func headerOf(head, name string) string {
	for _, l := range strings.Split(head, "\r\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func okAnswer(head string, extra string) string {
	return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		wsconn.AcceptKey(headerOf(head, "Sec-WebSocket-Key")) + "\r\n" + extra + "\r\n"
}

func TestDialControlProtocolFailures(t *testing.T) {
	cases := []struct {
		name   string
		answer func(head string) string
		class  string
		status int
		code   deyerr.Code
	}{
		{"wrong accept key", func(string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: AAAAAAAAAAAAAAAAAAAAAAAAAAA=\r\n\r\n"
		}, ClassProtocol, 101, deyerr.N017},
		{"missing accept key", func(string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
		}, ClassProtocol, 101, deyerr.N017},
		{"extensions negotiated", func(h string) string {
			return okAnswer(h, "Sec-WebSocket-Extensions: permessage-deflate\r\n")
		}, ClassProtocol, 101, deyerr.N017},
		{"subprotocol negotiated", func(h string) string {
			return okAnswer(h, "Sec-WebSocket-Protocol: chat\r\n")
		}, ClassProtocol, 101, deyerr.N017},
		{"no upgrade header", func(h string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + wsconn.AcceptKey(headerOf(h, "Sec-WebSocket-Key")) + "\r\n\r\n"
		}, ClassProtocol, 101, deyerr.N017},
		{"no connection header", func(h string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: " + wsconn.AcceptKey(headerOf(h, "Sec-WebSocket-Key")) + "\r\n\r\n"
		}, ClassProtocol, 101, deyerr.N017},
		{"not http", func(string) string { return "SSH-2.0-OpenSSH_9.6\r\n" }, ClassProtocol, 0, deyerr.N016},
		{"binary garbage", func(string) string { return "\x00\x01\x02\x03\x04\x05\r\n\r\n" }, ClassProtocol, 0, deyerr.N016},
		{"head over 8 KiB", func(string) string {
			return "HTTP/1.1 101 Switching Protocols\r\nX-Pad: " + strings.Repeat("a", 9000) + "\r\n\r\n"
		}, ClassProtocol, 0, deyerr.N016},
		{"closed without an answer", func(string) string { return "" }, ClassDial, 0, deyerr.N016},
		{"cut in the middle of the head", func(string) string { return "HTTP/1.1 101 Swit" }, ClassDial, 0, deyerr.N016},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg := rawOrigin(t, func(conn net.Conn, head string) { _, _ = io.WriteString(conn, c.answer(head)) })
			_, err := DialControl(context.Background(), tg)
			require.Error(t, err)
			de := AsDialError(err)
			require.NotNil(t, de)
			assert.Equal(t, c.class, de.Class)
			assert.Equal(t, c.status, de.Status)
			assert.False(t, de.Permanent())
			assert.True(t, deyerr.HasCode(de.ToDEY(), c.code), "%v", de.ToDEY())
			assert.NotContains(t, err.Error(), testSecret)
		})
	}
}

func TestDialControlRequestOnTheWire(t *testing.T) {
	heads := make(chan string, 1)
	tg := rawOrigin(t, func(c net.Conn, head string) {
		heads <- head
		_, _ = io.WriteString(c, okAnswer(head, ""))
		time.Sleep(50 * time.Millisecond)
	})
	conn, err := DialControl(context.Background(), tg)
	require.NoError(t, err)
	defer conn.Close()
	head := <-heads
	lines := strings.Split(strings.TrimSuffix(head, "\r\n\r\n"), "\r\n")
	assert.Equal(t, "GET /"+testSecret+"/c HTTP/1.1", lines[0])
	assert.Equal(t, fmt.Sprintf("Host: 127.0.0.1:%d", tg.Port), lines[1])
	assert.Equal(t, "User-Agent: "+userAgent, lines[2])
	assert.Equal(t, "Upgrade: websocket", lines[3])
	assert.Equal(t, "Connection: Upgrade", lines[4])
	assert.True(t, strings.HasPrefix(lines[5], "Sec-WebSocket-Key: "))
	assert.Equal(t, "Sec-WebSocket-Version: 13", lines[6])
	assert.Equal(t, "Origin: https://127.0.0.1", lines[7])
	assert.Len(t, lines, 8)
}

func TestDialControlBytesBehindTheHeadAreKept(t *testing.T) {
	// A server may send its first frame in the same segment as the 101.
	tg := rawOrigin(t, func(c net.Conn, head string) {
		frame := append([]byte{0x82, 5}, "hello"...)
		_, _ = c.Write(append([]byte(okAnswer(head, "")), frame...))
		time.Sleep(200 * time.Millisecond)
	})
	conn, err := DialControl(context.Background(), tg)
	require.NoError(t, err)
	defer conn.Close()
	got := make([]byte, 5)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
}

func TestDialControlReturnsAWebSocketConn(t *testing.T) {
	st := newStack(t, stackOpts{})
	conn := st.dial(t)
	wc, ok := conn.(*wsconn.Conn)
	require.True(t, ok)
	assert.Empty(t, wc.Via())
}

// ---------------------------------------------------------------------------
// Dial and TLS failures

func TestDialErrorsByCause(t *testing.T) {
	tg := Target{Host: "front.example.com", Port: 443, TLS: true, Secret: testSecret}
	cases := []struct {
		name   string
		err    error
		reason string
	}{
		{"dns not found", &net.DNSError{Err: "no such host", Name: "front.example.com", IsNotFound: true}, "does not resolve"},
		{"dns timeout", &net.DNSError{Err: "i/o timeout", Name: "front.example.com", IsTimeout: true}, "DNS lookup"},
		{"refused", &net.OpError{Op: "dial", Err: errno("ECONNREFUSED")}, "refused"},
		{"unreachable", &net.OpError{Op: "dial", Err: errno("ENETUNREACH")}, "no route"},
		{"reset", &net.OpError{Op: "read", Err: errno("ECONNRESET")}, "reset"},
		{"timeout", &net.OpError{Op: "dial", Err: timeoutErr{}}, "timed out"},
		{"other", errors.New("boom"), "failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &Dialer{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, c.err }}
			_, err := d.DialControl(context.Background(), tg)
			de := AsDialError(err)
			require.NotNil(t, de)
			assert.Equal(t, ClassDial, de.Class)
			assert.Contains(t, de.Reason, c.reason)
			assert.False(t, de.Permanent())
			assert.True(t, deyerr.HasCode(de.ToDEY(), deyerr.N016))
			assert.ErrorIs(t, err, c.err, "the cause stays reachable")
		})
	}
}

func TestDialControlTLSFailures(t *testing.T) {
	// Untrusted certificate: the pool does not contain the CDN's CA.
	t.Run("unknown authority", func(t *testing.T) {
		st := newStack(t, stackOpts{})
		d := &Dialer{RootCAs: x509Pool(t), UpgradeTimeout: 5 * time.Second}
		_, err := d.DialControl(context.Background(), st.target)
		de := AsDialError(err)
		require.NotNil(t, de)
		assert.Equal(t, ClassTLS, de.Class)
		assert.Contains(t, de.Reason, "ca-certificates")
		assert.True(t, deyerr.HasCode(de.ToDEY(), deyerr.N016))
	})
	t.Run("expired certificate hints at the clock", func(t *testing.T) {
		cs, err := fronttest.NewCertSet([]string{"front.example.com"}, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
		require.NoError(t, err)
		srv := startServer(t, ServerOptions{})
		cdn, err := fronttest.NewCDN(fronttest.Options{OriginAddr: srv.Addr().String(), Cert: &cs.Cert})
		require.NoError(t, err)
		t.Cleanup(func() { _ = cdn.Close() })
		d := &Dialer{RootCAs: cs.Pool, UpgradeTimeout: 5 * time.Second}
		_, err = d.DialControl(context.Background(), Target{Host: "front.example.com", Port: cdn.Port(), TLS: true, EdgeIP: "127.0.0.1", Secret: testSecret})
		de := AsDialError(err)
		require.NotNil(t, de)
		assert.Equal(t, ClassTLS, de.Class)
		assert.Contains(t, de.Reason, "clock")
		assert.Contains(t, de.Reason, "expired")
		assert.True(t, deyerr.HasCode(de.ToDEY(), deyerr.N016))
	})
	t.Run("not yet valid certificate hints at the clock", func(t *testing.T) {
		cs, err := fronttest.NewCertSet([]string{"front.example.com"}, time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
		require.NoError(t, err)
		srv := startServer(t, ServerOptions{})
		cdn, err := fronttest.NewCDN(fronttest.Options{OriginAddr: srv.Addr().String(), Cert: &cs.Cert})
		require.NoError(t, err)
		t.Cleanup(func() { _ = cdn.Close() })
		d := &Dialer{RootCAs: cs.Pool, UpgradeTimeout: 5 * time.Second}
		_, err = d.DialControl(context.Background(), Target{Host: "front.example.com", Port: cdn.Port(), TLS: true, EdgeIP: "127.0.0.1", Secret: testSecret})
		de := AsDialError(err)
		require.NotNil(t, de)
		assert.Contains(t, de.Reason, "clock")
	})
	t.Run("name mismatch", func(t *testing.T) {
		st := newStack(t, stackOpts{})
		st.target.Host = "other.example.org" // dialed at the edge address, verified against this name
		_, err := st.dialer.DialControl(context.Background(), st.target)
		de := AsDialError(err)
		require.NotNil(t, de)
		assert.Equal(t, ClassTLS, de.Class)
		assert.Contains(t, de.Reason, "does not match")
		assert.Contains(t, de.Reason, "other.example.org")
	})
	t.Run("plain port with a wss target", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		done := make(chan struct{})
		go func() { // a plain HTTP server that answers the TLS bytes it gets with an HTTP error
			defer close(done)
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
				_ = c.Close()
			}
		}()
		t.Cleanup(func() { _ = ln.Close(); <-done })
		_, err = (&Dialer{UpgradeTimeout: 3 * time.Second}).DialControl(context.Background(), Target{Host: "127.0.0.1", Port: portOf(ln.Addr()), TLS: true, Secret: testSecret})
		de := AsDialError(err)
		require.NotNil(t, de)
		assert.Equal(t, ClassTLS, de.Class)
		assert.Contains(t, de.Reason, "does not speak TLS")
	})
	t.Run("closed during the handshake", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_ = c.Close()
			}
		}()
		t.Cleanup(func() { _ = ln.Close(); <-done })
		_, err = (&Dialer{UpgradeTimeout: 3 * time.Second}).DialControl(context.Background(), Target{Host: "127.0.0.1", Port: portOf(ln.Addr()), TLS: true, Secret: testSecret})
		de := AsDialError(err)
		require.NotNil(t, de)
		assert.Equal(t, ClassTLS, de.Class)
		assert.Contains(t, de.Reason, "closed during the TLS handshake")
	})
}

func TestDialControlConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := portOf(ln.Addr())
	require.NoError(t, ln.Close())
	_, err = DialControl(context.Background(), Target{Host: "127.0.0.1", Port: port, TLS: true, Secret: testSecret})
	de := AsDialError(err)
	require.NotNil(t, de)
	assert.Equal(t, ClassDial, de.Class)
	assert.Contains(t, de.Reason, "refused")
}

// ---------------------------------------------------------------------------
// Budgets, cancellation, sockets

// silentListener accepts TCP connections and never says a word.
func silentListener(t *testing.T) (port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		wg.Wait()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})
	return portOf(ln.Addr())
}

func TestBlackHoledEdgeReturnsWithinTheBudget(t *testing.T) {
	port := silentListener(t)
	for _, tls := range []bool{true, false} {
		t.Run(fmt.Sprintf("tls=%v", tls), func(t *testing.T) {
			d := &Dialer{UpgradeTimeout: 400 * time.Millisecond}
			start := time.Now()
			_, err := d.DialControl(context.Background(), Target{Host: "front.example.com", Port: port, TLS: tls, EdgeIP: "127.0.0.1", Secret: testSecret}) // no deadline on the context
			el := time.Since(start)
			de := AsDialError(err)
			require.NotNil(t, de)
			if tls {
				assert.Equal(t, ClassTLS, de.Class)
				assert.Contains(t, de.Reason, "timed out")
			} else {
				assert.Equal(t, ClassDial, de.Class)
				assert.Contains(t, de.Reason, "timed out")
			}
			assert.GreaterOrEqual(t, el, 300*time.Millisecond)
			assert.Less(t, el, 3*time.Second)
			assert.True(t, deyerr.HasCode(de.ToDEY(), deyerr.N016))
		})
	}
}

func TestBlackHoledTCPConnectHonoursTheDialTimeout(t *testing.T) {
	// A connect that never completes: the hook blocks until its context ends.
	d := &Dialer{DialTimeout: 300 * time.Millisecond, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		<-ctx.Done()
		return nil, &net.OpError{Op: "dial", Err: timeoutErr{}}
	}}
	start := time.Now()
	_, err := d.DialControl(context.Background(), Target{Host: "front.example.com", Port: 443, TLS: true, Secret: testSecret})
	de := AsDialError(err)
	require.NotNil(t, de)
	assert.Equal(t, ClassDial, de.Class)
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestRealBlackHoleAddress(t *testing.T) {
	// 192.0.2.1 is TEST-NET-1: nothing answers. Depending on the host the
	// connect times out or fails at once; either way it is the dial class and
	// it is back within the dial budget.
	d := &Dialer{DialTimeout: 500 * time.Millisecond, UpgradeTimeout: 500 * time.Millisecond}
	start := time.Now()
	_, err := d.DialControl(context.Background(), Target{Host: "front.example.com", Port: 443, TLS: true, EdgeIP: "192.0.2.1", Secret: testSecret})
	de := AsDialError(err)
	require.NotNil(t, de)
	assert.Equal(t, ClassDial, de.Class)
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestContextCancelClosesPromptly(t *testing.T) {
	port := silentListener(t)
	for _, tls := range []bool{true, false} {
		t.Run(fmt.Sprintf("tls=%v", tls), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			go func() { time.Sleep(150 * time.Millisecond); cancel() }()
			start := time.Now()
			_, err := DialControl(ctx, Target{Host: "front.example.com", Port: port, TLS: tls, EdgeIP: "127.0.0.1", Secret: testSecret})
			require.Error(t, err)
			assert.ErrorIs(t, err, context.Canceled)
			assert.Less(t, time.Since(start), 2*time.Second)
			de := AsDialError(err)
			require.NotNil(t, de)
			assert.Equal(t, "canceled", de.Reason)
		})
	}
}

func TestContextAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DialControl(ctx, Target{Host: "front.example.com", Port: 443, TLS: true, Secret: testSecret})
	assert.ErrorIs(t, err, context.Canceled)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	time.Sleep(time.Millisecond)
	_, err = DialControl(ctx2, Target{Host: "front.example.com", Port: 443, TLS: true, Secret: testSecret})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, "timed out", AsDialError(err).Reason)
}

func TestContextDeadlineShorterThanTheBudget(t *testing.T) {
	port := silentListener(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := (&Dialer{UpgradeTimeout: time.Minute}).DialControl(ctx, Target{Host: "front.example.com", Port: port, TLS: false, EdgeIP: "127.0.0.1", Secret: testSecret})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestNoProxyEnvironment(t *testing.T) {
	// Every proxy variable points at a listener; the dial must never touch it.
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var hits int
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := pln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			hits++
			mu.Unlock()
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = pln.Close(); wg.Wait() })
	proxy := "http://" + pln.Addr().String()
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(k, proxy)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	st := newStack(t, stackOpts{})
	conn := st.dial(t)
	echoOnce(t, conn, []byte("direct"))
	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, hits, "the proxy variables are ignored")
}

func TestUpgradeBudgetDefaults(t *testing.T) {
	assert.Equal(t, 10*time.Second, defaultDialTimeout)
	assert.Equal(t, 20*time.Second, defaultUpgradeTimeout)
	assert.Equal(t, 30*time.Second, tcpKeepAlive)
	assert.Equal(t, 8<<10, maxResponseHead)
	assert.Equal(t, 2<<10, maxErrorBody)
}

func TestTLSConfigShape(t *testing.T) {
	pool := x509Pool(t)
	d := &Dialer{RootCAs: pool}
	cfg := d.tlsConfig(Target{Host: "front.example.com", Port: 443, TLS: true, EdgeIP: "104.16.1.1"})
	assert.Equal(t, "front.example.com", cfg.ServerName, "the front domain, also with an edge address")
	assert.Equal(t, []string{"http/1.1"}, cfg.NextProtos)
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
	assert.False(t, cfg.InsecureSkipVerify)
	assert.Same(t, pool, cfg.RootCAs)
	assert.Nil(t, (&Dialer{}).tlsConfig(Target{Host: "a.example.com"}).RootCAs, "nil means the system roots")
}

// ---------------------------------------------------------------------------
// helpers

func x509Pool(t *testing.T) *x509.CertPool {
	t.Helper()
	cs, err := fronttest.NewCertSet([]string{"unrelated.example.net"}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	require.NoError(t, err)
	return cs.Pool
}

func errno(name string) error {
	switch name {
	case "ECONNREFUSED":
		return syscall.ECONNREFUSED
	case "ENETUNREACH":
		return syscall.ENETUNREACH
	case "ECONNRESET":
		return syscall.ECONNRESET
	}
	panic("unknown errno " + name)
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
