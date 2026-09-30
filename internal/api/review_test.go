package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Regression tests for defects found in review.

func TestHandlerTrackerRefusesAfterClose(t *testing.T) {
	var h handlerTracker
	require.True(t, h.enter())
	done := make(chan struct{})
	go func() {
		h.closeAndWait()
		close(done)
	}()
	require.Eventually(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.closed
	}, 5*time.Second, time.Millisecond)
	// A handler starting after shutdown began is refused (no Add after Wait).
	require.False(t, h.enter())
	select {
	case <-done:
		t.Fatal("closeAndWait returned while a handler was running")
	default:
	}
	h.leave()
	<-done

	fired := false
	var slow handlerTracker
	require.True(t, slow.enter())
	waitHandlers(&slow, 20*time.Millisecond, func() { fired = true })
	require.True(t, fired)
	slow.leave()
}

// flakyListener fails its first Accept calls with a temporary error.
type flakyListener struct {
	net.Listener
	failures atomic.Int32
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.failures.Add(-1) >= 0 {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: os.NewSyscallError("accept4", syscall.EMFILE)}
	}
	return l.Listener.Accept()
}

// Before the fix a single EMFILE from Accept stopped the control API for
// good (Serve returned); it must back off and keep serving.
func TestServeSurvivesTemporaryAcceptErrors(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := &flakyListener{Listener: inner}
	ln.failures.Store(3)
	srv := &ControlServer{TLSConfig: p.serverCfg, Handler: hub}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	defer func() {
		cancel()
		require.NoError(t, <-done)
	}()
	cfg, _ := p.nodeConfig(t, "de-1")
	c := &ControlClient{HubAddr: inner.Addr().String(), TLSConfig: cfg}
	require.NoError(t, c.Upload(context.Background(), "u1", strings.NewReader("x")))
	require.Equal(t, 5*time.Millisecond, acceptBackoff(0))
	require.Equal(t, time.Second, acceptBackoff(800*time.Millisecond))
}

func TestStaleSocketOnlyOnConnectionRefused(t *testing.T) {
	wrap := func(e syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", e)}
	}
	require.True(t, staleSocket(wrap(syscall.ECONNREFUSED)))
	// A live daemon with a full accept backlog answers EAGAIN: never delete.
	require.False(t, staleSocket(wrap(syscall.EAGAIN)))
	require.False(t, staleSocket(wrap(syscall.EACCES)))
	require.False(t, staleSocket(errors.New("i/o timeout")))
}

func TestServeLocalLeavesNoTemporarySocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- ServeLocalReady(ctx, sock, http.NotFoundHandler(), nil, func() { close(ready) })
	}()
	<-ready
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "d.sock", entries[0].Name())
	fi, err := os.Lstat(sock)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSocket)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	conn, err := net.Dial("unix", sock)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	cancel()
	require.NoError(t, <-done)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestLocalConnErrMapping(t *testing.T) {
	c := newLocalClient("/run/deyroute/daemon.sock", DialOptions{Service: "deyroute-node"})
	opErr := func(e syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", e)}
	}
	e := requireCode(t, c.connErr(opErr(syscall.ENOENT)), deyerr.X003)
	require.Equal(t, "systemctl start deyroute-node", e.Fix())
	requireCode(t, c.connErr(opErr(syscall.ECONNREFUSED)), deyerr.X003)
	// A non-root user must be told to use root, not "Local API error".
	e = requireCode(t, c.connErr(opErr(syscall.EACCES)), deyerr.I001)
	require.Contains(t, e.Why(), "/run/deyroute/daemon.sock")
	require.Contains(t, e.Fix(), "sudo")
	requireCode(t, c.connErr(errors.New("other")), deyerr.X006)
}

// A plain node error must reach the hub as DEY-N011 with its (redacted)
// text, not as an information-free DEY-X000.
func TestNodePlainErrorBecomesN011Redacted(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	secret := "Zm9vYmFyYmF6cXV4cXV1eHF1dXhxdXV4cXV1eHE"
	startClient(t, cfg, addr, "de-1", func(context.Context, Command, func([]string)) (any, error) {
		return nil, fmt.Errorf("download failed: token=%s", secret)
	}, nil)
	s := waitSession(t, hub)
	e := requireCode(t, s.Call(context.Background(), CmdFetchProxy, FetchArgs{URL: "https://x"}, nil), deyerr.N011)
	require.Contains(t, e.Detail, "download failed")
	require.NotContains(t, e.Detail, secret)
	require.Contains(t, e.Message(), CmdFetchProxy)

	// The hub redacts too (an older node may send the raw text).
	e = requireCode(t, s.decodeResult("x", &Result{Error: &ErrorDTO{Message: "token=" + secret}}, nil), deyerr.N011)
	require.NotContains(t, e.Detail, secret)
}

// The node's hello must arrive within the handshake timeout.
func TestStreamWithoutHelloIsClosed(t *testing.T) {
	p := newPKI(t)
	addr, _ := startServer(t, p, newFakeHub(p), "")
	cfg, _ := p.nodeConfig(t, "de-1")
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	start := time.Now()
	resp, done := rawRequest(t, cfg, http.MethodPost, "https://"+addr+PathStream, pr)
	defer done()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, _ = io.ReadAll(resp.Body)
	require.Less(t, time.Since(start), 8*time.Second)
}

// An unauthenticated peer cannot hold a join stream open with a slow body.
func TestJoinSlowBodyTimesOut(t *testing.T) {
	p := newPKI(t)
	addr, _ := startServer(t, p, newFakeHub(p), "")
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	go func() { _, _ = pw.Write([]byte(`{"token":`)) }()
	start := time.Now()
	resp, done := rawRequest(t, joinCfg(p), http.MethodPost, "https://"+addr+PathJoin, pr)
	defer done()
	e := requireCode(t, readControlResponse(resp, nil), deyerr.N015)
	require.Contains(t, e.Why(), "not received in time")
	require.Less(t, time.Since(start), 8*time.Second)
}

func joinCfg(p *testPKI) *tls.Config { return tlsutil.JoinClientTLSConfig(p.ca.Fingerprint()) }

// blockingAuthHub never answers Authenticate until released.
type blockingAuthHub struct {
	*fakeHub
	release chan struct{}
}

func (h blockingAuthHub) Authenticate(string, string, string) error {
	<-h.release
	return deyerr.New(deyerr.N008, deyerr.Params{"node": "de-1"})
}

// A hub that accepts the connection but never answers the stream request
// must not hold the node forever.
func TestStreamOpenTimeout(t *testing.T) {
	p := newPKI(t)
	h := blockingAuthHub{fakeHub: newFakeHub(p), release: make(chan struct{})}
	addr, stop := startServer(t, p, h, "")
	defer func() {
		close(h.release)
		stop()
	}()
	cfg, _ := p.nodeConfig(t, "de-1")
	c := &ControlClient{HubAddr: addr, TLSConfig: cfg, OpenTimeout: 200 * time.Millisecond}
	start := time.Now()
	connected, err := c.runOnce(context.Background())
	require.False(t, connected)
	e := requireCode(t, err, deyerr.N009)
	require.Contains(t, e.Detail, "did not answer")
	require.Less(t, time.Since(start), 5*time.Second)
	require.Equal(t, dialTimeout+DefaultHandshakeTimeout, (&ControlClient{}).openTimeout())
}

// shortSessionHub ends every session right away (e.g. the hub refuses the
// node after hello) and counts the streams.
type shortSessionHub struct {
	*fakeHub
	streams *atomic.Int32
}

func (h shortSessionHub) Session(*Session) { h.streams.Add(1) }

// Before the fix every accepted stream reset the backoff, so a hub that
// drops streams immediately was hammered at BackoffMin forever.
func TestBackoffGrowsWhenStreamsDropImmediately(t *testing.T) {
	p := newPKI(t)
	var streams atomic.Int32
	addr, _ := startServer(t, p, shortSessionHub{fakeHub: newFakeHub(p), streams: &streams}, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	startClient(t, cfg, addr, "de-1", nil, func(c *ControlClient) {
		c.BackoffMin = 20 * time.Millisecond
		c.BackoffMax = 2 * time.Second
	})
	time.Sleep(900 * time.Millisecond)
	// 20+40+80+160+320 ms ≈ 620 ms: about 5-6 streams, never ~45.
	n := streams.Load()
	require.GreaterOrEqual(t, n, int32(2))
	require.LessOrEqual(t, n, int32(9), "streams opened: %d", n)
}

func TestFetchAssetRejectsBadArchLocally(t *testing.T) {
	c := &ControlClient{HubAddr: "127.0.0.1:1", TLSConfig: tlsutil.JoinClientTLSConfig(strings.Repeat("0", 64))}
	_, err := c.FetchAsset(context.Background(), "../../etc", io.Discard)
	requireCode(t, err, deyerr.N015)
}
