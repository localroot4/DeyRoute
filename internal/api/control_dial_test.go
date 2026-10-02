package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// viaConn is a trivial carrier connection: it reports a transport name, a
// trusted flag and an overridden remote address, like a CDN front would.
type viaConn struct {
	net.Conn
	via     string
	trusted bool
	remote  net.Addr
}

func (c *viaConn) Via() string           { return c.via }
func (c *viaConn) TrustedClientIP() bool { return c.trusted }
func (c *viaConn) RemoteAddr() net.Addr  { return c.remote }

// wrapListener turns every accepted connection through wrap.
type wrapListener struct {
	net.Listener
	wrap func(net.Conn) net.Conn
}

func (l wrapListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return l.wrap(c), nil
}

// startServerWrapped serves h on a loopback listener whose connections go
// through wrap.
func startServerWrapped(t *testing.T, p *testPKI, h ControlHandler, wrap func(net.Conn) net.Conn) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &ControlServer{TLSConfig: p.serverCfg, Handler: h, HandshakeTimeout: 2 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, wrapListener{ln, wrap}) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return ln.Addr().String()
}

// frontWrap makes every connection look like it arrived through a carrier
// that cannot vouch for the client address.
func frontWrap(trusted bool) func(net.Conn) net.Conn {
	return func(c net.Conn) net.Conn {
		return &viaConn{Conn: c, via: "front", trusted: trusted, remote: &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 4444}}
	}
}

// countingDial dials target over TCP and counts the connections it made.
func countingDial(target string, n *atomic.Int32) func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		n.Add(1)
		var d net.Dialer
		return d.DialContext(ctx, "tcp", target)
	}
}

// The hub address handed to the client is not resolvable: only the dial hook
// can reach the hub.
const unreachableAddr = "hub.invalid:443"

func TestDialHookStreamUploadAssetJoin(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	payload := []byte("binary-bytes")
	h := sha256.Sum256(payload)
	sum := hex.EncodeToString(h[:])
	hub.asset = func(string) (AssetInfo, error) {
		return AssetInfo{Reader: io.NopCloser(bytes.NewReader(payload)), Size: int64(len(payload)), SHA256: sum, Version: "1.0.0"}, nil
	}
	target := startServerWrapped(t, p, hub, frontWrap(false))
	cfg, _ := p.nodeConfig(t, "de-1")
	var dials atomic.Int32
	dial := countingDial(target, &dials)

	// Stream: the hook is the only way to the hub.
	c := startClient(t, cfg, unreachableAddr, "de-1", func(context.Context, Command, func([]string)) (any, error) {
		return "pong", nil
	}, func(c *ControlClient) { c.Dial = dial })
	s := waitSession(t, hub)
	require.Equal(t, "front", s.Via)
	require.False(t, s.Trusted)
	require.Equal(t, "203.0.113.9", s.RemoteIP)
	var out string
	require.NoError(t, s.Call(context.Background(), CmdSysinfo, nil, &out))
	require.Equal(t, "pong", out)
	require.Equal(t, 1, int(dials.Load()))

	// Authenticate saw the same peer through the request context.
	hub.mu.Lock()
	require.Equal(t, Peer{IP: "203.0.113.9", Via: "front"}, hub.authPeer)
	hub.mu.Unlock()

	// Upload and asset requests use the hook too.
	ctx := context.Background()
	require.NoError(t, c.Upload(ctx, "up-1", strings.NewReader("payload")))
	hub.mu.Lock()
	require.Equal(t, "de-1:payload", hub.uploads["up-1"])
	hub.mu.Unlock()
	var buf bytes.Buffer
	meta, err := c.FetchAsset(ctx, "amd64", &buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf.Bytes())
	require.Equal(t, sum, meta.SHA256)
	require.GreaterOrEqual(t, int(dials.Load()), 3)

	// Join over the hook: pinned CA on top of the carrier.
	csr, _, err := tlsutil.NewKeyAndCSR("de-2")
	require.NoError(t, err)
	req := JoinRequest{Token: "good-token-0123456789", NodeID: "de-2", CSRPEM: string(csr)}
	before := dials.Load()
	resp, err := JoinVia(ctx, unreachableAddr, p.ca.Fingerprint(), req, dial)
	require.NoError(t, err)
	require.Equal(t, "de-2", resp.NodeID)
	require.Equal(t, "203.0.113.9", resp.PublicIP)
	require.Greater(t, dials.Load(), before)
	hub.mu.Lock()
	require.Equal(t, Peer{IP: "203.0.113.9", Via: "front"}, hub.joinPeer)
	hub.mu.Unlock()

	// Wrong pin through the hook is DEY-N002, a wrong token keeps its code.
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	_, err = JoinVia(ctx, unreachableAddr, other.Fingerprint(), req, dial)
	requireCode(t, err, deyerr.N002)
	req.Token = "wrong-token-0123456789"
	_, err = JoinVia(ctx, unreachableAddr, p.ca.Fingerprint(), req, dial)
	requireCode(t, err, deyerr.N001)

	// Join without a hook is the old behaviour.
	req.Token = "good-token-0123456789"
	_, err = Join(ctx, unreachableAddr, p.ca.Fingerprint(), req)
	requireCode(t, err, deyerr.N009)
}

func TestDialFuncResolvedOnEveryReconnect(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	target := startServerWrapped(t, p, hub, frontWrap(true))
	cfg, _ := p.nodeConfig(t, "de-1")
	var resolved, dials atomic.Int32
	startClient(t, cfg, unreachableAddr, "de-1", nil, func(c *ControlClient) {
		c.DialFunc = func() func(context.Context) (net.Conn, error) {
			resolved.Add(1)
			return countingDial(target, &dials)
		}
	})
	s := waitSession(t, hub)
	require.True(t, s.Trusted)
	s.Close() // the hub ends the stream: the client reconnects
	s2 := waitSession(t, hub)
	require.NotSame(t, s, s2)
	require.GreaterOrEqual(t, int(resolved.Load()), 2)
	require.GreaterOrEqual(t, int(dials.Load()), 2)
}

func TestDialFuncNilFallsBack(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")

	// DialFunc returning nil uses Dial, then plain TCP.
	var viaDial atomic.Int32
	startClient(t, cfg, unreachableAddr, "de-1", nil, func(c *ControlClient) {
		c.DialFunc = func() func(context.Context) (net.Conn, error) { return nil }
		c.Dial = countingDial(addr, &viaDial)
	})
	s := waitSession(t, hub)
	require.Equal(t, "", s.Via)
	require.True(t, s.Trusted)
	require.Equal(t, 1, int(viaDial.Load()))

	hub2 := newFakeHub(p)
	addr2, _ := startServer(t, p, hub2, "")
	startClient(t, cfg, addr2, "de-2", nil, func(c *ControlClient) {
		c.DialFunc = func() func(context.Context) (net.Conn, error) { return nil }
	})
	waitSession(t, hub2)
}

func TestOpenTimeoutDefaultWithDial(t *testing.T) {
	require.Equal(t, dialTimeout+DefaultHandshakeTimeout, (&ControlClient{}).openTimeout())
	d := func(context.Context) (net.Conn, error) { return nil, errors.New("x") }
	require.Equal(t, 30*time.Second, (&ControlClient{Dial: d}).openTimeout())
	require.Equal(t, 30*time.Second, (&ControlClient{DialFunc: func() func(context.Context) (net.Conn, error) { return nil }}).openTimeout())
	require.Equal(t, time.Second, (&ControlClient{Dial: d, OpenTimeout: time.Second}).openTimeout())
}

func TestDialErrorMapsToN009(t *testing.T) {
	p := newPKI(t)
	cfg, _ := p.nodeConfig(t, "de-1")
	boom := errors.New("carrier down")
	dial := func(context.Context) (net.Conn, error) { return nil, boom }
	c := &ControlClient{HubAddr: unreachableAddr, TLSConfig: cfg, Dial: dial}
	err := c.Upload(context.Background(), "up-1", strings.NewReader("x"))
	e := requireCode(t, err, deyerr.N009)
	require.Contains(t, e.Message(), unreachableAddr)
	require.ErrorIs(t, err, boom)

	_, err = c.FetchAsset(context.Background(), "amd64", io.Discard)
	requireCode(t, err, deyerr.N009)
	_, err = JoinVia(context.Background(), unreachableAddr, p.ca.Fingerprint(), JoinRequest{}, dial)
	requireCode(t, err, deyerr.N009)

	// A hook that returns neither a conn nor an error is an internal error.
	nilDial := func(context.Context) (net.Conn, error) { return nil, nil }
	_, err = JoinVia(context.Background(), unreachableAddr, p.ca.Fingerprint(), JoinRequest{}, nilDial)
	require.Error(t, err)
}

// closeTrackConn records Close.
type closeTrackConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeTrackConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

func TestDialHookClosesRawConnOnHandshakeFailure(t *testing.T) {
	// A "hub" that answers with garbage instead of a TLS handshake.
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
			_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); wg.Wait() })

	var raw atomic.Pointer[closeTrackConn]
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		tc := &closeTrackConn{Conn: c}
		raw.Store(tc)
		return tc, nil
	}
	_, err = JoinVia(context.Background(), unreachableAddr, strings.Repeat("0", 64), JoinRequest{}, dial)
	requireCode(t, err, deyerr.N009)
	got := raw.Load()
	require.NotNil(t, got)
	require.True(t, got.closed.Load(), "raw connection must be closed when the TLS handshake fails")
}

// outerErr is an outer-layer failure; it hints the reconnect pace.
type outerErr struct {
	after     time.Duration
	permanent bool
}

func (e *outerErr) Error() string             { return "outer layer refused" }
func (e *outerErr) RetryAfter() time.Duration { return e.after }
func (e *outerErr) Permanent() bool           { return e.permanent }

func TestPinFailureNotMaskedByOuterError(t *testing.T) {
	pin := deyerr.New(deyerr.N002, nil)
	outer := deyerr.Wrap(deyerr.N016, errors.New("edge"), deyerr.Params{"addr": "x:443"})

	// The pin failure was recorded first: later outer errors cannot replace it.
	box := &dialErrBox{}
	box.set(pin)
	box.set(outer)
	requireCode(t, box.get(), deyerr.N002)
	requireCode(t, netErr("x:443", outer, box), deyerr.N002)

	// And the other way round: the transport hands back the outer error but
	// the box holds the pin failure.
	requireCode(t, netErr("x:443", errors.New("wrapped: "+outer.Error()), box), deyerr.N002)

	// Without a pin failure the first DEY error wins, as before.
	box2 := &dialErrBox{}
	box2.set(outer)
	requireCode(t, netErr("x:443", errors.New("plain"), box2), deyerr.N016)
	requireCode(t, netErr("x:443", errors.New("plain"), &dialErrBox{}), deyerr.N009)
}

// pinThenOuterDial fails the TLS pin on the first connect and with an outer
// error afterwards.
func TestPinFailureThroughHookWinsOverLaterOuterError(t *testing.T) {
	p := newPKI(t)
	addr, _ := startServer(t, p, newFakeHub(p), "")
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	var n atomic.Int32
	dial := func(ctx context.Context) (net.Conn, error) {
		if n.Add(1) > 1 {
			return nil, deyerr.Wrap(deyerr.N016, errors.New("edge gone"), deyerr.Params{"addr": "x"})
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
	_, err = JoinVia(context.Background(), unreachableAddr, other.Fingerprint(), JoinRequest{}, dial)
	requireCode(t, err, deyerr.N002)
}

// logRecorder collects slog records.
type logRecorder struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (l *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (l *logRecorder) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	l.recs = append(l.recs, r)
	l.mu.Unlock()
	return nil
}
func (l *logRecorder) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logRecorder) WithGroup(string) slog.Handler      { return l }

func (l *logRecorder) count(level slog.Level) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.recs {
		if r.Level == level {
			n++
		}
	}
	return n
}

// runWithDial runs a client over a failing dial hook and returns the dial
// times once n dials happened.
func runWithDial(t *testing.T, cfg *tls.Config, err error, n int, mod func(*ControlClient), lg *slog.Logger) []time.Time {
	t.Helper()
	var mu sync.Mutex
	var times []time.Time
	reached := make(chan struct{})
	var once sync.Once
	c := &ControlClient{
		HubAddr: unreachableAddr, TLSConfig: cfg, Logger: lg,
		Dial: func(context.Context) (net.Conn, error) {
			mu.Lock()
			times = append(times, time.Now())
			if len(times) == n {
				once.Do(func() { close(reached) })
			}
			mu.Unlock()
			return nil, err
		},
	}
	if mod != nil {
		mod(c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		t.Fatal("client did not reconnect often enough")
	}
	cancel()
	require.NoError(t, <-done)
	mu.Lock()
	defer mu.Unlock()
	return append([]time.Time(nil), times...)
}

func TestRetryHinterRaisesBackoff(t *testing.T) {
	p := newPKI(t)
	cfg, _ := p.nodeConfig(t, "de-1")
	hint := 250 * time.Millisecond
	times := runWithDial(t, cfg, &outerErr{after: hint}, 3, func(c *ControlClient) {
		c.BackoffMin, c.BackoffMax = 5*time.Millisecond, 10*time.Millisecond
	}, nil)
	require.GreaterOrEqual(t, len(times), 3)
	for i := 1; i < 3; i++ {
		require.GreaterOrEqual(t, times[i].Sub(times[i-1]), hint-20*time.Millisecond, "gap %d", i)
	}

	// Without a hint the same client reconnects fast.
	times = runWithDial(t, cfg, errors.New("plain"), 5, func(c *ControlClient) {
		c.BackoffMin, c.BackoffMax = 5*time.Millisecond, 10*time.Millisecond
	}, nil)
	require.Less(t, times[4].Sub(times[0]), 2*time.Second)
}

func TestPermanentErrorWarnsOncePerDistinctError(t *testing.T) {
	p := newPKI(t)
	cfg, _ := p.nodeConfig(t, "de-1")
	rec := &logRecorder{}
	times := runWithDial(t, cfg, &outerErr{permanent: true}, 5, func(c *ControlClient) {
		c.BackoffMin, c.BackoffMax = time.Millisecond, 2*time.Millisecond
	}, slog.New(rec))
	require.GreaterOrEqual(t, len(times), 5)
	require.Equal(t, 1, rec.count(slog.LevelWarn))
	require.Equal(t, 0, rec.count(slog.LevelInfo))
	// The backoff of a permanent error grows beyond the usual ceiling.
	require.Greater(t, times[4].Sub(times[3]), 4*time.Millisecond)

	// A transient error logs one line per attempt, no warning.
	rec2 := &logRecorder{}
	runWithDial(t, cfg, &outerErr{}, 3, func(c *ControlClient) {
		c.BackoffMin, c.BackoffMax = time.Millisecond, 2*time.Millisecond
	}, slog.New(rec2))
	require.Equal(t, 0, rec2.count(slog.LevelWarn))
	require.GreaterOrEqual(t, rec2.count(slog.LevelInfo), 2)
}

func TestPlanReconnect(t *testing.T) {
	const minB, maxB = time.Second, 30 * time.Second

	// No hint: the jittered backoff (at most 20 % below it).
	for range 50 {
		d, ceil, b := planReconnect(4*time.Second, minB, maxB, 0, false)
		require.Equal(t, maxB, ceil)
		require.Equal(t, 4*time.Second, b)
		require.GreaterOrEqual(t, d, 3200*time.Millisecond)
		require.LessOrEqual(t, d, 4*time.Second)
	}
	// A hint above the backoff wins.
	d, _, _ := planReconnect(time.Second, minB, maxB, 7*time.Second, false)
	require.Equal(t, 7*time.Second, d)
	// A hint below the backoff does not shorten it.
	d, _, _ = planReconnect(20*time.Second, minB, maxB, time.Second, false)
	require.GreaterOrEqual(t, d, 16*time.Second)
	// Ten minutes is the absolute cap.
	d, _, _ = planReconnect(time.Second, minB, maxB, time.Hour, false)
	require.Equal(t, 10*time.Minute, d)
	d, _, _ = planReconnect(time.Second, minB, maxB, time.Hour, true)
	require.Equal(t, 10*time.Minute, d)

	// Permanent: starts at the usual ceiling, may grow to five minutes.
	d, ceil, b := planReconnect(time.Second, minB, maxB, 0, true)
	require.Equal(t, 5*time.Minute, ceil)
	require.Equal(t, maxB, b)
	require.GreaterOrEqual(t, d, 24*time.Second)
	d, ceil, b = planReconnect(10*time.Minute, minB, maxB, 0, true)
	require.Equal(t, 5*time.Minute, ceil)
	require.Equal(t, 5*time.Minute, b, "never above the permanent ceiling")
	require.LessOrEqual(t, d, 5*time.Minute)
	// A configured ceiling above five minutes is kept.
	_, ceil, _ = planReconnect(time.Second, minB, 8*time.Minute, 0, true)
	require.Equal(t, 8*time.Minute, ceil)
	// A transient error after a permanent one shrinks back to the ceiling.
	_, ceil, b = planReconnect(5*time.Minute, minB, maxB, 0, false)
	require.Equal(t, maxB, ceil)
	require.Equal(t, maxB, b)
}

func TestRetryHintExtraction(t *testing.T) {
	after, perm := retryHint(nil)
	require.Zero(t, after)
	require.False(t, perm)
	wrapped := deyerr.Wrap(deyerr.N009, &outerErr{after: time.Minute, permanent: true}, deyerr.Params{"addr": "x"})
	after, perm = retryHint(wrapped)
	require.Equal(t, time.Minute, after)
	require.True(t, perm)
	after, perm = retryHint(&outerErr{after: -time.Second})
	require.Zero(t, after)
	require.False(t, perm)
	after, perm = retryHint(errors.New("plain"))
	require.Zero(t, after)
	require.False(t, perm)
}

// plainWrap leaves the connection alone.
func plainWrap(c net.Conn) net.Conn { return c }

// valueConn is a conn type that is neither *net.TCPConn nor comparable.
type valueConn struct {
	net.Conn
	_ []int
}

func TestServerTracksAnyConnType(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr := startServerWrapped(t, p, hub, func(c net.Conn) net.Conn { return valueConn{Conn: c} })
	cfg, _ := p.nodeConfig(t, "de-1")
	startClient(t, cfg, addr, "de-1", nil, nil)
	s := waitSession(t, hub)
	require.Equal(t, "", s.Via)
	require.True(t, s.Trusted)
	require.Equal(t, "127.0.0.1", s.RemoteIP)
	// The cleanup stops the server, which closes the tracked connection.
}

func TestPeerPlainTCP(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr := startServerWrapped(t, p, hub, plainWrap)
	cfg, _ := p.nodeConfig(t, "de-1")
	startClient(t, cfg, addr, "de-1", nil, nil)
	s := waitSession(t, hub)
	require.Equal(t, "127.0.0.1", s.RemoteIP)
	require.Equal(t, "", s.Via)
	require.True(t, s.Trusted)
	hub.mu.Lock()
	require.Equal(t, Peer{IP: "127.0.0.1", Trusted: true}, hub.authPeer)
	hub.mu.Unlock()

	csr, _, err := tlsutil.NewKeyAndCSR("de-2")
	require.NoError(t, err)
	_, err = Join(context.Background(), addr, p.ca.Fingerprint(),
		JoinRequest{Token: "good-token-0123456789", NodeID: "de-2", CSRPEM: string(csr)})
	require.NoError(t, err)
	hub.mu.Lock()
	require.Equal(t, Peer{IP: "127.0.0.1", Trusted: true}, hub.joinPeer)
	hub.mu.Unlock()
}

func TestPeerOf(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })

	// net.Pipe has no host:port address: the string is kept as is.
	p := peerOf(a)
	require.Equal(t, Peer{IP: "pipe", Trusted: true}, p)

	vc := &viaConn{Conn: a, via: "front", trusted: true, remote: &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 1}}
	require.Equal(t, Peer{IP: "2001:db8::1", Via: "front", Trusted: true}, peerOf(vc))
	vc.trusted = false
	require.Equal(t, Peer{IP: "2001:db8::1", Via: "front"}, peerOf(vc))

	// A TLS listener wrapped around the carrier keeps the carrier's facts.
	tc := tls.Server(vc, &tls.Config{})
	require.Equal(t, Peer{IP: "2001:db8::1", Via: "front"}, peerOf(tc))
}

func TestPeerContext(t *testing.T) {
	require.Equal(t, Peer{}, PeerFrom(context.Background()))
	want := Peer{IP: "198.51.100.7", Via: "front", Trusted: true}
	require.Equal(t, want, PeerFrom(ContextWithPeer(context.Background(), want)))
	// Requests that bypass serveConn fall back to RemoteAddr.
	require.Equal(t, "10.0.0.1", hostOf("10.0.0.1:99"))
	require.Equal(t, "[::1]", hostOf("[::1]"))
}
