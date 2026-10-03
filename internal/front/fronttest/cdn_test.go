package fronttest

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/wsconn"
)

// ---------------------------------------------------------------------------
// helpers

// originOpts tunes the test origin.
type originOpts struct {
	tls          bool
	pingInterval time.Duration
	idle         time.Duration
	// onConn runs for every upgraded connection instead of the echo.
	onConn func(c *wsconn.Conn)
}

// testOrigin is a minimal WebSocket origin: it upgrades GET requests and
// echoes, and records the requests it saw.
type testOrigin struct {
	ln   net.Listener
	o    originOpts
	wg   sync.WaitGroup
	mu   sync.Mutex
	reqs []*http.Request
	cfg  *tls.Config
}

func newOrigin(t *testing.T, o originOpts) *testOrigin {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	to := &testOrigin{ln: ln, o: o}
	if o.tls {
		cs, err := NewCertSet([]string{"localhost"}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
		require.NoError(t, err)
		to.cfg = &tls.Config{Certificates: []tls.Certificate{cs.Cert}, MinVersion: tls.VersionTLS12}
	}
	to.wg.Add(1)
	go func() {
		defer to.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			to.wg.Add(1)
			go to.serve(c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		to.wg.Wait()
	})
	return to
}

func (o *testOrigin) addr() string { return o.ln.Addr().String() }

func (o *testOrigin) requests() []*http.Request {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*http.Request(nil), o.reqs...)
}

func (o *testOrigin) serve(raw net.Conn) {
	defer o.wg.Done()
	defer raw.Close()
	conn := raw
	if o.cfg != nil {
		tc := tls.Server(raw, o.cfg)
		if tc.Handshake() != nil {
			return
		}
		conn = tc
	}
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	o.mu.Lock()
	o.reqs = append(o.reqs, req)
	o.mu.Unlock()
	if req.Header.Get("Upgrade") == "" {
		_, _ = io.WriteString(conn, "HTTP/1.1 404 Not Found\r\nContent-Length: 9\r\nConnection: close\r\n\r\nnot found")
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+
		wsconn.AcceptKey(req.Header.Get("Sec-WebSocket-Key"))+"\r\n\r\n")
	wc := wsconn.New(conn, br, wsconn.Config{PingInterval: o.o.pingInterval, IdleTimeout: o.o.idle})
	defer wc.Close()
	if o.o.onConn != nil {
		o.o.onConn(wc)
		return
	}
	_, _ = io.Copy(wc, wc)
}

// dialCDN opens a TLS (or plain) connection to the CDN.
func dialCDN(t *testing.T, c *CDN, plain bool) net.Conn {
	t.Helper()
	raw, err := net.DialTimeout("tcp", c.Addr(), 3*time.Second)
	require.NoError(t, err)
	if plain {
		return raw
	}
	tc := tls.Client(raw, &tls.Config{ServerName: "front.example.com", RootCAs: c.CAPool(), NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	require.NoError(t, tc.Handshake())
	return tc
}

const testPath = "/s3cr3t-path-for-tests/c"

func upgradeRequest(path, key string, extra ...string) string {
	r := "GET " + path + " HTTP/1.1\r\nHost: front.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n"
	for _, e := range extra {
		r += e + "\r\n"
	}
	return r + "\r\n"
}

// wsClient upgrades through the CDN and returns the client wsconn.
func wsClient(t *testing.T, c *CDN, plain bool, cfg wsconn.Config, extra ...string) *wsconn.Conn {
	t.Helper()
	conn := dialCDN(t, c, plain)
	key := wsconn.NewKey()
	_, err := io.WriteString(conn, upgradeRequest(testPath, key, extra...))
	require.NoError(t, err)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	require.Equal(t, 101, resp.StatusCode)
	require.Equal(t, wsconn.AcceptKey(key), resp.Header.Get("Sec-WebSocket-Accept"))
	cfg.Client = true
	wc := wsconn.New(conn, br, cfg)
	t.Cleanup(func() { _ = wc.Close() })
	return wc
}

func newCDN(t *testing.T, o Options) *CDN {
	t.Helper()
	c, err := NewCDN(o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func echoOnce(t *testing.T, wc *wsconn.Conn, msg []byte) {
	t.Helper()
	_ = wc.SetDeadline(time.Now().Add(5 * time.Second))
	_, err := wc.Write(msg)
	require.NoError(t, err)
	got := make([]byte, len(msg))
	_, err = io.ReadFull(wc, got)
	require.NoError(t, err)
	require.True(t, bytes.Equal(msg, got))
	_ = wc.SetDeadline(time.Time{})
}

func rawRoundTrip(t *testing.T, c *CDN, req string) (*http.Response, []byte) {
	t.Helper()
	conn := dialCDN(t, c, false)
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err := io.WriteString(conn, req)
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// ---------------------------------------------------------------------------
// tests

func TestCDNUpgradePassThrough(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), ClientIP: "203.0.113.9"})
	wc := wsClient(t, c, false, wsconn.Config{}, "Sec-WebSocket-Extensions: permessage-deflate", "CF-Connecting-IP: 1.2.3.4", "X-Forwarded-For: 5.6.7.8")
	echoOnce(t, wc, []byte("hello through the edge"))

	reqs := o.requests()
	require.Len(t, reqs, 1)
	h := reqs[0].Header
	assert.Empty(t, h.Values("Sec-WebSocket-Extensions"), "the edge drops extensions")
	assert.Equal(t, []string{"203.0.113.9"}, h.Values("Cf-Connecting-Ip"), "a client-supplied value is replaced")
	assert.Equal(t, []string{"203.0.113.9"}, h.Values("X-Forwarded-For"))
	assert.Equal(t, "https", h.Get("X-Forwarded-Proto"))
	assert.Contains(t, h.Get("Cf-Visitor"), "https")
	assert.NotEmpty(t, h.Get("Cf-Ray"))
	assert.Equal(t, "front.example.com", reqs[0].Host)
	assert.Equal(t, testPath, reqs[0].RequestURI)

	st := c.Stats()
	assert.EqualValues(t, 1, st.Requests)
	assert.EqualValues(t, 1, st.Upgrades)
	// A pump counts its bytes after its Write returned, so the echo can
	// reach the client before both directions are counted.
	require.Eventually(t, func() bool {
		st := c.Stats()
		return st.BytesUp > 0 && st.BytesDown > 0
	}, 5*time.Second, 5*time.Millisecond, "forwarded bytes are counted in both directions")

	rs := c.Requests()
	require.Len(t, rs, 1)
	assert.Equal(t, "front.example.com", rs[0].SNI)
	assert.Equal(t, []string{"http/1.1"}, rs[0].ALPN)
	assert.True(t, rs[0].Upgrade)
	assert.GreaterOrEqual(t, rs[0].TLSVersion, uint16(tls.VersionTLS12))
	assert.NotContains(t, rs[0].PathHash, "s3cr3t", "the path itself is never recorded")
}

func TestCDNClientIPDefaultsToPeer(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr()})
	wc := wsClient(t, c, false, wsconn.Config{})
	echoOnce(t, wc, []byte("x"))
	assert.Equal(t, []string{"127.0.0.1"}, o.requests()[0].Header.Values("Cf-Connecting-Ip"))
}

func TestCDNPlainClientsAndTLSOrigin(t *testing.T) {
	o := newOrigin(t, originOpts{tls: true})
	c := newCDN(t, Options{OriginAddr: o.addr(), OriginTLS: true, PlainClients: true})
	wc := wsClient(t, c, true, wsconn.Config{})
	echoOnce(t, wc, []byte("flexible client, full origin"))
	assert.Equal(t, "http", o.requests()[0].Header.Get("X-Forwarded-Proto"))
	assert.Zero(t, c.Requests()[0].TLSVersion)
}

func TestCDNNonUpgradePassThrough(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr()})
	resp, body := rawRoundTrip(t, c, "GET /x HTTP/1.1\r\nHost: front.example.com\r\n\r\n")
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "not found", string(body))
	assert.EqualValues(t, 0, c.Stats().Upgrades)
}

func TestCDNOriginErrors(t *testing.T) {
	// A closed port: 521.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := ln.Addr().String()
	require.NoError(t, ln.Close())
	c := newCDN(t, Options{OriginAddr: closed})
	resp, body := rawRoundTrip(t, c, upgradeRequest(testPath, wsconn.NewKey()))
	assert.Equal(t, 521, resp.StatusCode)
	assert.Contains(t, string(body), "Error 521")
	assert.Equal(t, "cloudflare", resp.Header.Get("Server"))

	// TLS towards a plain origin: 525.
	o := newOrigin(t, originOpts{})
	c2 := newCDN(t, Options{OriginAddr: o.addr(), OriginTLS: true})
	resp, _ = rawRoundTrip(t, c2, upgradeRequest(testPath, wsconn.NewKey()))
	assert.Equal(t, 525, resp.StatusCode)
}

func TestCDNCanned(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr()})
	cases := []struct {
		name   string
		canned *Canned
		status int
		header string
		value  string
		body   string
	}{
		{"redirect", Redirect(301, "https://example.com/"), 301, "Location", "https://example.com/", ""},
		{"temporary redirect", Redirect(307, "/x"), 307, "Location", "/x", ""},
		{"challenge", Challenge(), 403, "Cf-Mitigated", "challenge", "Just a moment"},
		{"blocked", Blocked(1010), 403, "", "", "Error 1010"},
		{"rate limit", RateLimited("17"), 429, "Retry-After", "17", "Error 1015"},
		{"ssl invalid", OriginError(526), 526, "", "", "Error 526"},
		{"1xxx", CloudflareError(1016), 530, "", "", "Error 1016"},
		{"not found", NotFound(), 404, "", "", "Not Found"},
		{"bare", Status(418), 418, "", "", "418"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canned := tc.canned
			c.Configure(func(o *Options) { o.Canned = canned })
			resp, body := rawRoundTrip(t, c, upgradeRequest(testPath, wsconn.NewKey()))
			assert.Equal(t, tc.status, resp.StatusCode)
			if tc.header != "" {
				assert.Equal(t, tc.value, resp.Header.Get(tc.header))
			}
			assert.Contains(t, string(body), tc.body)
			assert.Equal(t, "cloudflare", resp.Header.Get("Server"))
		})
	}
	assert.Empty(t, o.requests(), "a canned answer never reaches the origin")
}

func TestCDNConfigureAtRuntime(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), Canned: NotFound()})
	resp, _ := rawRoundTrip(t, c, upgradeRequest(testPath, wsconn.NewKey()))
	assert.Equal(t, 404, resp.StatusCode)
	c.Configure(func(o *Options) { o.Canned = nil })
	echoOnce(t, wsClient(t, c, false, wsconn.Config{}), []byte("now it passes"))
}

func TestCDNChunkAndDelay(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), Chunk: 7, Delay: 30 * time.Millisecond})
	wc := wsClient(t, c, false, wsconn.Config{})
	start := time.Now()
	echoOnce(t, wc, bytes.Repeat([]byte("abcdefghij"), 50))
	assert.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond, "one delay per leg")
}

func TestCDNBytesPerSec(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), BytesPerSec: 200_000})
	wc := wsClient(t, c, false, wsconn.Config{})
	start := time.Now()
	echoOnce(t, wc, bytes.Repeat([]byte{7}, 100_000))
	// 100 KB at 200 KB/s is half a second per direction (the two overlap).
	assert.GreaterOrEqual(t, time.Since(start), 450*time.Millisecond)
}

func TestCDNIdleCutPingsDoNotReset(t *testing.T) {
	o := newOrigin(t, originOpts{pingInterval: 30 * time.Millisecond})
	c := newCDN(t, Options{OriginAddr: o.addr(), IdleCut: 400 * time.Millisecond})
	wc := wsClient(t, c, false, wsconn.Config{PingInterval: 30 * time.Millisecond})
	echoOnce(t, wc, []byte("one data frame"))
	// Only pings and pongs flow now: the idle timer is not reset and the edge cuts.
	_ = wc.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	_, err := wc.Read(make([]byte, 1))
	require.Error(t, err)
	var ne net.Error
	assert.False(t, errors.As(err, &ne) && ne.Timeout(), "the cut must come from the edge, not from our deadline: %v", err)
	assert.Less(t, time.Since(start), 2500*time.Millisecond)
	assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)
}

func TestCDNIdleCutDataResetsAndPingsResetWhenAsked(t *testing.T) {
	o := newOrigin(t, originOpts{pingInterval: 30 * time.Millisecond})
	c := newCDN(t, Options{OriginAddr: o.addr(), IdleCut: 400 * time.Millisecond})
	wc := wsClient(t, c, false, wsconn.Config{PingInterval: 30 * time.Millisecond})
	for i := 0; i < 6; i++ { // 6 x 150 ms > the idle window, but data keeps arriving
		echoOnce(t, wc, []byte("tick"))
		time.Sleep(150 * time.Millisecond)
	}
	// With PingsResetIdle the same silence is no longer cut.
	c.Configure(func(o *Options) { o.PingsResetIdle = true })
	time.Sleep(900 * time.Millisecond)
	echoOnce(t, wc, []byte("still here"))
}

func TestCDNSwallowPings(t *testing.T) {
	// The origin kills a connection on which none of its pings is answered (and
	// no frame arrives); with pings swallowed that is what happens, without it
	// the pongs keep it alive.
	run := func(swallow bool) error {
		errc := make(chan error, 1)
		o := newOrigin(t, originOpts{pingInterval: 20 * time.Millisecond, idle: 150 * time.Millisecond, onConn: func(c *wsconn.Conn) {
			_ = c.SetReadDeadline(time.Now().Add(1200 * time.Millisecond))
			_, err := c.Read(make([]byte, 1))
			errc <- err
		}})
		c := newCDN(t, Options{OriginAddr: o.addr(), SwallowPings: swallow})
		wc := wsClient(t, c, false, wsconn.Config{PingInterval: 20 * time.Millisecond})
		_ = wc
		select {
		case err := <-errc:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("origin read did not return")
			return nil
		}
	}
	err := run(true)
	require.Error(t, err)
	assert.ErrorIs(t, err, wsconn.ErrIdleTimeout)
	err = run(false)
	require.Error(t, err)
	assert.NotErrorIs(t, err, wsconn.ErrIdleTimeout, "pings and pongs keep it alive until our own read deadline")
}

func TestCDNCutAfterBytes(t *testing.T) {
	const total, cut = 200_000, 50_000
	payload := bytes.Repeat([]byte{0x5a}, total)
	o := newOrigin(t, originOpts{onConn: func(c *wsconn.Conn) {
		_, _ = c.Write(payload) // origin to client
		_, _ = io.Copy(io.Discard, c)
	}})
	c := newCDN(t, Options{OriginAddr: o.addr(), CutAfterBytes: cut, CutDir: Down})
	wc := wsClient(t, c, false, wsconn.Config{})
	_ = wc.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
	n, err := io.Copy(io.Discard, wc)
	require.Error(t, err)
	// The cut counts raw bytes (frame headers included), so slightly fewer
	// payload bytes than the limit arrive, and then silence: a stall, not a close.
	assert.Less(t, n, int64(cut))
	assert.Greater(t, n, int64(cut)/2)
	var ne net.Error
	require.True(t, errors.As(err, &ne) && ne.Timeout(), "a silent stall ends with our own timeout: %v", err)
}

func TestCDNCutClose(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), CutAfterBytes: 300, CutDir: Up, CutClose: true})
	wc := wsClient(t, c, false, wsconn.Config{})
	_ = wc.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = wc.Write(bytes.Repeat([]byte{1}, 2000))
	_, err := io.Copy(io.Discard, wc)
	require.Error(t, err)
	var ne net.Error
	assert.False(t, errors.As(err, &ne) && ne.Timeout(), "CutClose closes instead of stalling: %v", err)
}

func TestCDNDropAll(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), DropAll: true})
	conn := dialCDN(t, c, false)
	defer conn.Close()
	_, err := io.WriteString(conn, upgradeRequest(testPath, wsconn.NewKey()))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = conn.Read(make([]byte, 1))
	var ne net.Error
	require.True(t, errors.As(err, &ne) && ne.Timeout(), "a dropped request is never answered: %v", err)
	assert.Empty(t, o.requests())
}

func TestCDNPauseOrigin(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), PauseOrigin: true})
	conn := dialCDN(t, c, false)
	defer conn.Close()
	key := wsconn.NewKey()
	_, err := io.WriteString(conn, upgradeRequest(testPath, key))
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err)
	assert.Empty(t, o.requests(), "a paused origin is not contacted")
	c.Configure(func(o *Options) { o.PauseOrigin = false })
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	assert.Equal(t, 101, resp.StatusCode)
}

func TestCDNPauseMidStream(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr()})
	wc := wsClient(t, c, false, wsconn.Config{})
	echoOnce(t, wc, []byte("before"))
	c.Configure(func(o *Options) { o.PauseOrigin = true })
	_, err := wc.Write([]byte("during"))
	require.NoError(t, err)
	_ = wc.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	_, err = wc.Read(make([]byte, 6))
	require.Error(t, err, "nothing comes back while the origin is paused")
	c.Configure(func(o *Options) { o.PauseOrigin = false })
	_ = wc.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, 6)
	_, err = io.ReadFull(wc, got)
	require.NoError(t, err)
	assert.Equal(t, "during", string(got))
}

func TestCDNCloseJoinsEverything(t *testing.T) {
	o := newOrigin(t, originOpts{})
	c, err := NewCDN(Options{OriginAddr: o.addr()})
	require.NoError(t, err)
	wc := wsClient(t, c, false, wsconn.Config{})
	echoOnce(t, wc, []byte("x"))
	require.NoError(t, c.Close())
	require.NoError(t, c.Close(), "idempotent")
	_ = wc.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = wc.Read(make([]byte, 1))
	require.Error(t, err)
	_, err = net.DialTimeout("tcp", c.Addr(), 200*time.Millisecond)
	assert.Error(t, err)
}

func TestNewCDNValidation(t *testing.T) {
	_, err := NewCDN(Options{})
	assert.Error(t, err)
}

func TestCertSet(t *testing.T) {
	expired, err := NewCertSet([]string{"front.example.com", "127.0.0.1"}, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	o := newOrigin(t, originOpts{})
	c := newCDN(t, Options{OriginAddr: o.addr(), Cert: &expired.Cert})
	raw, err := net.Dial("tcp", c.Addr())
	require.NoError(t, err)
	defer raw.Close()
	tc := tls.Client(raw, &tls.Config{ServerName: "front.example.com", RootCAs: expired.Pool})
	err = tc.Handshake()
	require.Error(t, err, "an expired certificate is rejected")
	assert.True(t, strings.Contains(err.Error(), "expired"), err.Error())
}

// ---------------------------------------------------------------------------
// frame scanner

func frame(op byte, payload []byte, mask bool) []byte {
	b := []byte{0x80 | op}
	n := len(payload)
	m := byte(0)
	if mask {
		m = 0x80
	}
	switch {
	case n < 126:
		b = append(b, m|byte(n))
	case n <= 0xffff:
		b = append(b, m|126, byte(n>>8), byte(n))
	default:
		b = append(b, m|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if mask {
		b = append(b, 1, 2, 3, 4)
	}
	return append(b, payload...)
}

func TestFrameScanner(t *testing.T) {
	data := frame(2, bytes.Repeat([]byte{9}, 300), true)
	ping := frame(9, []byte("hi"), false)
	pong := frame(10, nil, true)
	stream := append(append(append(append([]byte{}, data...), ping...), pong...), data...)
	want := append(append([]byte{}, data...), data...)

	for _, chunk := range []int{1, 2, 3, 7, 50, len(stream)} {
		t.Run("", func(t *testing.T) {
			var sc frameScanner
			var got []byte
			var act activity
			for i := 0; i < len(stream); i += chunk {
				out, a := sc.feed(stream[i:min(i+chunk, len(stream))], true)
				got = append(got, out...)
				act.data = act.data || a.data
				act.ping = act.ping || a.ping
			}
			assert.Equal(t, want, got, "pings and pongs dropped, data intact (chunk %d)", chunk)
			assert.True(t, act.data)
			assert.True(t, act.ping)
		})
	}

	// Without swallowing everything is forwarded.
	var sc frameScanner
	out, act := sc.feed(stream, false)
	assert.Equal(t, stream, out)
	assert.True(t, act.ping && act.data)

	// A stream of pings alone is not data.
	sc = frameScanner{}
	_, act = sc.feed(append(append([]byte{}, ping...), pong...), false)
	assert.True(t, act.ping)
	assert.False(t, act.data)

	// Garbage with the MSB of a 64-bit length set switches the scanner off.
	sc = frameScanner{}
	bad := []byte{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3}
	out, act = sc.feed(bad, true)
	assert.Equal(t, bad, out)
	assert.True(t, act.data)
	out, _ = sc.feed([]byte("more"), true)
	assert.Equal(t, "more", string(out))
}

// ---------------------------------------------------------------------------
// CutProxy

func TestCutProxyStalls(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // a plain echo server
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })

	p, err := NewCutProxy("", ln.Addr().String(), 1000, Down)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	conn, err := net.Dial("tcp", p.Addr())
	require.NoError(t, err)
	defer conn.Close()
	msg := bytes.Repeat([]byte{3}, 600)
	for i := 0; i < 2; i++ { // 1200 bytes echoed, only 1000 pass
		_, err = conn.Write(msg)
		require.NoError(t, err)
		time.Sleep(30 * time.Millisecond)
	}
	_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	n, err := io.Copy(io.Discard, conn)
	assert.EqualValues(t, 1000, n)
	var ne net.Error
	require.True(t, errors.As(err, &ne) && ne.Timeout(), "the cut direction stalls, it does not close: %v", err)
	assert.EqualValues(t, 1200, p.Bytes(Up))
	assert.EqualValues(t, 1000, p.Bytes(Down))
}

func TestCutProxyCutNowAndClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })
	p, err := NewCutProxy("127.0.0.1:0", ln.Addr().String(), 0, Both)
	require.NoError(t, err)
	conn, err := net.Dial("tcp", p.Addr())
	require.NoError(t, err)
	defer conn.Close()
	_, _ = conn.Write([]byte("ab"))
	got := make([]byte, 2)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	p.CutNow()
	_, _ = conn.Write([]byte("cd"))
	_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = conn.Read(got)
	require.Error(t, err)
	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(got)
	assert.Error(t, err)
}
