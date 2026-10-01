package direct

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ------------------------------------------------------------ test helpers

type countHandler struct {
	mu         sync.Mutex
	n          int
	suppressed int64
}

func (h *countHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "suppressed" {
			h.suppressed = a.Value.Int64()
		}
		return true
	})
	return nil
}
func (h *countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countHandler) WithGroup(string) slog.Handler      { return h }
func (h *countHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}
func (h *countHandler) lastSuppressed() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.suppressed
}

func newTestLogger(h slog.Handler) *slog.Logger { return slog.New(h) }

func quietLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// echoServers runs a TCP and a UDP echo service on loopback.
type echoServers struct {
	tcp   net.Listener
	udp   *net.UDPConn
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns map[net.Conn]struct{}
	hits  int
}

func startEcho(t *testing.T) *echoServers {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	e := &echoServers{tcp: ln, udp: pc.(*net.UDPConn), conns: map[net.Conn]struct{}{}}
	e.wg.Add(2)
	go func() {
		defer e.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			e.mu.Lock()
			e.conns[c] = struct{}{}
			e.hits++
			e.mu.Unlock()
			e.wg.Add(1)
			go func() {
				defer e.wg.Done()
				_, _ = io.Copy(c, c)
				_ = c.(*net.TCPConn).CloseWrite()
				e.mu.Lock()
				delete(e.conns, c)
				e.mu.Unlock()
				_ = c.Close()
			}()
		}
	}()
	go func() {
		defer e.wg.Done()
		buf := make([]byte, 65536)
		for {
			n, addr, err := e.udp.ReadFromUDPAddrPort(buf)
			if err != nil {
				return
			}
			_, _ = e.udp.WriteToUDPAddrPort(buf[:n], addr)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		_ = e.udp.Close()
		e.mu.Lock()
		for c := range e.conns {
			_ = c.Close()
		}
		e.mu.Unlock()
		e.wg.Wait()
	})
	return e
}

func (e *echoServers) tcpHits() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hits
}

type pairOpts struct {
	hubToken, nodeToken string
	idleS, udpIdleS     int
	maxUDP              int
}

type pair struct {
	hub, node *relay
	echo      *echoServers
}

// startPair starts a node relay (targets: 0 = tcp echo, 1 = udp echo) and a
// hub relay pointing at it, both on loopback ephemeral ports.
func startPair(t *testing.T, o pairOpts) *pair {
	t.Helper()
	if o.hubToken == "" {
		o.hubToken = "tok-test"
	}
	if o.nodeToken == "" {
		o.nodeToken = "tok-test"
	}
	e := startEcho(t)
	nodeCfg := &RelayConfig{
		Version: 1, Tunnel: "main", Role: RoleNode, Token: o.nodeToken, Bind: "127.0.0.1:0",
		IdleTimeoutS: o.idleS, UDPIdleTimeoutS: o.udpIdleS,
		Ports: []RelayPort{
			{Index: 0, Proto: "tcp", Target: e.tcp.Addr().String()},
			{Index: 1, Proto: "udp", Target: e.udp.LocalAddr().String()},
		},
	}
	node := newRelay(nodeCfg, quietLogger())
	require.NoError(t, node.start(context.Background()))
	t.Cleanup(node.stop)

	hubCfg := &RelayConfig{
		Version: 1, Tunnel: "main", Role: RoleHub, Token: o.hubToken, Node: node.nodeTCP.Addr().String(),
		IdleTimeoutS: o.idleS, UDPIdleTimeoutS: o.udpIdleS, MaxUDPSessions: o.maxUDP,
		Ports: []RelayPort{
			{Index: 0, Proto: "tcp", Listen: "127.0.0.1:0"},
			{Index: 1, Proto: "udp", Listen: "127.0.0.1:0"},
		},
	}
	hub := newRelay(hubCfg, quietLogger())
	require.NoError(t, hub.start(context.Background()))
	t.Cleanup(hub.stop)
	return &pair{hub: hub, node: node, echo: e}
}

func (p *pair) hubTCP() string { return p.hub.tcpLn[0].Addr().String() }
func (p *pair) hubUDP() string { return p.hub.udpLn[1].LocalAddr().String() }

func dialTCP(t *testing.T, addr string) *net.TCPConn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	require.NoError(t, err)
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return c.(*net.TCPConn)
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

// roundTrip sends data, half-closes and reads everything back.
func roundTrip(t *testing.T, c *net.TCPConn, data []byte) []byte {
	t.Helper()
	errc := make(chan error, 1)
	go func() {
		_, err := c.Write(data)
		if err == nil {
			err = c.CloseWrite()
		}
		errc <- err
	}()
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	require.NoError(t, <-errc)
	return got
}

// expectClosed asserts the peer closes c without sending anything.
func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 16)
	n, err := c.Read(buf)
	require.Zero(t, n)
	require.Error(t, err)
	var ne net.Error
	if errors.As(err, &ne) {
		require.False(t, ne.Timeout(), "connection was not closed by the relay")
	}
}

// --------------------------------------------------------------- the tests

func TestRelayTCPEndToEnd(t *testing.T) {
	p := startPair(t, pairOpts{})
	c := dialTCP(t, p.hubTCP())
	defer c.Close()
	data := randomBytes(t, 1<<20+123)
	require.Equal(t, data, roundTrip(t, c, data), "bytes and half-close must survive the relay")
}

func TestRelayTCPConcurrent(t *testing.T) {
	p := startPair(t, pairOpts{})
	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := net.DialTimeout("tcp", p.hubTCP(), 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(20 * time.Second))
			data := make([]byte, 32*1024+i)
			if _, err := rand.Read(data); err != nil {
				errs <- err
				return
			}
			go func() {
				_, _ = c.Write(data)
				_ = c.(*net.TCPConn).CloseWrite()
			}()
			got, err := io.ReadAll(c)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(got, data) {
				errs <- errors.New("connection " + strconv.Itoa(i) + ": data mismatch")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return p.hub.activeConns() == 0 && p.node.activeConns() == 0 },
		5*time.Second, 20*time.Millisecond, "every relayed connection must be released")
}

func TestRelayUDPEndToEnd(t *testing.T) {
	p := startPair(t, pairOpts{})
	for i := 0; i < 2; i++ {
		c, err := net.Dial("udp", p.hubUDP())
		require.NoError(t, err)
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		for j := 0; j < 3; j++ {
			msg := []byte("ping-" + strconv.Itoa(i) + "-" + strconv.Itoa(j))
			_, err = c.Write(msg)
			require.NoError(t, err)
			buf := make([]byte, 2048)
			n, err := c.Read(buf)
			require.NoError(t, err)
			require.Equal(t, msg, buf[:n])
		}
	}
	require.Equal(t, 2, p.hub.hubUDP.count(), "one hub session per client")
	require.Equal(t, 2, p.node.nodeUDP.count(), "one node session per client")

	// A datagram of the maximum size passes; a larger one is dropped.
	c, err := net.Dial("udp", p.hubUDP())
	require.NoError(t, err)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	big := randomBytes(t, maxUDPPayload)
	_, err = c.Write(big)
	require.NoError(t, err)
	buf := make([]byte, 65536)
	n, err := c.Read(buf)
	require.NoError(t, err)
	require.Equal(t, big, buf[:n])
	_, err = c.Write(randomBytes(t, maxUDPPayload+1))
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = c.Read(buf)
	require.Error(t, err, "oversized datagrams are dropped")
}

func TestRelayUDPSessionExpiry(t *testing.T) {
	p := startPair(t, pairOpts{udpIdleS: 1})
	c, err := net.Dial("udp", p.hubUDP())
	require.NoError(t, err)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = c.Write([]byte("x"))
	require.NoError(t, err)
	buf := make([]byte, 16)
	_, err = c.Read(buf)
	require.NoError(t, err)
	require.Equal(t, 1, p.hub.hubUDP.count())
	require.Equal(t, 1, p.node.nodeUDP.count())
	require.Eventually(t, func() bool { return p.hub.hubUDP.count() == 0 && p.node.nodeUDP.count() == 0 },
		5*time.Second, 50*time.Millisecond, "idle sessions expire")
	// A new datagram opens a fresh session.
	_, err = c.Write([]byte("y"))
	require.NoError(t, err)
	n, err := c.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "y", string(buf[:n]))
}

func TestRelayUDPSessionLimit(t *testing.T) {
	p := startPair(t, pairOpts{maxUDP: 1})
	a, err := net.Dial("udp", p.hubUDP())
	require.NoError(t, err)
	defer a.Close()
	b, err := net.Dial("udp", p.hubUDP())
	require.NoError(t, err)
	defer b.Close()
	buf := make([]byte, 16)
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = a.Write([]byte("a"))
	require.NoError(t, err)
	_, err = a.Read(buf)
	require.NoError(t, err)
	_, err = b.Write([]byte("b"))
	require.NoError(t, err)
	_ = b.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = b.Read(buf)
	require.Error(t, err, "the second client exceeds the session cap")
	require.Equal(t, 1, p.hub.hubUDP.count())
}

func TestRelayWrongToken(t *testing.T) {
	p := startPair(t, pairOpts{hubToken: "wrong-token"})
	c := dialTCP(t, p.hubTCP())
	defer c.Close()
	_, _ = c.Write([]byte("hello"))
	expectClosed(t, c)
	require.Zero(t, p.echo.tcpHits(), "the node must not reach the target")

	u, err := net.Dial("udp", p.hubUDP())
	require.NoError(t, err)
	defer u.Close()
	_, err = u.Write([]byte("hello"))
	require.NoError(t, err)
	_ = u.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = u.Read(make([]byte, 16))
	require.Error(t, err, "unauthenticated datagrams are dropped")
	require.Zero(t, p.node.nodeUDP.count())
}

func TestRelayRejectsGarbageAndIndexes(t *testing.T) {
	p := startPair(t, pairOpts{})
	node := p.node.nodeTCP.Addr().String()

	// Garbage instead of a preamble.
	c := dialTCP(t, node)
	_, _ = c.Write(bytes.Repeat([]byte{'G'}, preambleLen))
	expectClosed(t, c)
	_ = c.Close()

	m := newMacer([]byte("tok-test"))
	for _, index := range []int{1, 9, 63} { // 1 is a UDP target, 9/63 unknown
		nonce, err := newNonce()
		require.NoError(t, err)
		pre := buildPreamble(m, index, nonce)
		c := dialTCP(t, node)
		_, err = c.Write(pre[:])
		require.NoError(t, err)
		expectClosed(t, c)
		_ = c.Close()
	}
	require.Zero(t, p.echo.tcpHits(), "no target may be dialled for a bad index")

	// A UDP datagram naming a TCP index (or an unknown one) opens nothing.
	u, err := net.Dial("udp", node)
	require.NoError(t, err)
	defer u.Close()
	buf := make([]byte, udpBufLen)
	for _, index := range []int{0, 5} {
		n := copy(buf[udpHeaderLen:], "x")
		_, err = u.Write(sealUDP(m, dirToNode, buf, index, 42, n))
		require.NoError(t, err)
	}
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, p.node.nodeUDP.count())
}

func TestRelayReplayedPreamble(t *testing.T) {
	p := startPair(t, pairOpts{})
	node := p.node.nodeTCP.Addr().String()
	nonce, err := newNonce()
	require.NoError(t, err)
	pre := buildPreamble(newMacer([]byte("tok-test")), 0, nonce)

	c := dialTCP(t, node)
	_, err = c.Write(pre[:])
	require.NoError(t, err)
	require.Equal(t, []byte("first"), roundTrip(t, c, []byte("first")))
	_ = c.Close()

	c = dialTCP(t, node)
	defer c.Close()
	_, err = c.Write(pre[:])
	require.NoError(t, err)
	_, _ = c.Write([]byte("second"))
	expectClosed(t, c)
	require.Equal(t, 1, p.echo.tcpHits())
}

func TestRelayIdleTimeout(t *testing.T) {
	p := startPair(t, pairOpts{idleS: 1})

	// An active connection outlives the idle timeout.
	active := dialTCP(t, p.hubTCP())
	defer active.Close()
	buf := make([]byte, 8)
	for i := 0; i < 8; i++ {
		_, err := active.Write([]byte("tick"))
		require.NoError(t, err)
		_, err = io.ReadFull(active, buf[:4])
		require.NoError(t, err)
		time.Sleep(300 * time.Millisecond)
	}

	// An idle one is closed.
	idle := dialTCP(t, p.hubTCP())
	defer idle.Close()
	_, err := idle.Write([]byte("tock"))
	require.NoError(t, err)
	_, err = io.ReadFull(idle, buf[:4])
	require.NoError(t, err)
	start := time.Now()
	expectClosed(t, idle)
	require.Less(t, time.Since(start), 4*time.Second)
}

func TestRelayNodeDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	hub := newRelay(&RelayConfig{Version: 1, Tunnel: "main", Role: RoleHub, Token: "t", Node: addr,
		Ports: []RelayPort{{Index: 0, Proto: "tcp", Listen: "127.0.0.1:0"}}}, quietLogger())
	require.NoError(t, hub.start(context.Background()))
	defer hub.stop()
	c := dialTCP(t, hub.tcpLn[0].Addr().String())
	defer c.Close()
	expectClosed(t, c)
}

func TestRelayStopClosesEverything(t *testing.T) {
	p := startPair(t, pairOpts{})
	c := dialTCP(t, p.hubTCP())
	defer c.Close()
	_, err := c.Write([]byte("hold"))
	require.NoError(t, err)
	_, err = io.ReadFull(c, make([]byte, 4))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return p.hub.activeConns() == 2 }, 2*time.Second, 10*time.Millisecond)
	p.hub.stop()
	p.node.stop()
	expectClosed(t, c)
	_, err = net.DialTimeout("tcp", p.hubTCP(), time.Second)
	require.Error(t, err, "listeners are closed")
}

func TestRelayListenConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	busy := ln.Addr().String()

	node := newRelay(&RelayConfig{Version: 1, Tunnel: "main", Role: RoleNode, Token: "t", Bind: busy,
		Ports: []RelayPort{{Index: 0, Proto: "tcp", Target: "127.0.0.1:1"}}}, quietLogger())
	err = node.start(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.B061), "%v", err)

	hub := newRelay(&RelayConfig{Version: 1, Tunnel: "main", Role: RoleHub, Token: "t", Node: "127.0.0.1:9",
		Ports: []RelayPort{{Index: 0, Proto: "udp", Listen: "127.0.0.1:0"}, {Index: 1, Proto: "tcp", Listen: busy}}}, quietLogger())
	err = hub.start(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.B061), "%v", err)
}

func TestRunRelayFromFile(t *testing.T) {
	e := startEcho(t)
	dir := t.TempDir()
	path := filepath.Join(dir, RelayConfigFile)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	bind := free.Addr().String()
	require.NoError(t, free.Close())
	cfg := RelayConfig{Version: 1, Tunnel: "main", Role: RoleNode, Token: "t", Bind: bind,
		Ports: []RelayPort{{Index: 0, Proto: "tcp", Target: e.tcp.Addr().String()}}}
	data, err := backend.JSONIndent(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunRelay(ctx, path, nil) }()
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", bind, 200*time.Millisecond)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 5*time.Second, 20*time.Millisecond, "RunRelay listens on the configured bind")
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("RunRelay did not stop")
	}

	err = RunRelay(context.Background(), filepath.Join(dir, "missing.json"), nil)
	require.True(t, deyerr.HasCode(err, deyerr.B060))
	err = Run(context.Background(), nil, nil)
	require.True(t, deyerr.HasCode(err, deyerr.B060))
	err = Run(context.Background(), &RelayConfig{Version: 9}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.B060))
}

func TestRunCheck(t *testing.T) {
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
			_ = c.Close()
		}
	}()
	defer wg.Wait()
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	ok := &RelayConfig{Version: 1, Tunnel: "main", Role: RoleCheck, NodeIP: "127.0.0.1", DialTimeoutS: 1,
		Ports: []RelayPort{{Index: 0, Proto: "tcp", Target: "0.0.0.0:" + strconv.Itoa(port)}}}
	require.NoError(t, Run(context.Background(), ok, quietLogger()))

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	dead := closed.Addr().(*net.TCPAddr).Port
	require.NoError(t, closed.Close())
	bad := &RelayConfig{Version: 1, Tunnel: "main", Role: RoleCheck, NodeIP: "127.0.0.1", DialTimeoutS: 1,
		Ports: []RelayPort{{Index: 0, Proto: "tcp", Target: "127.0.0.1:" + strconv.Itoa(dead)}}}
	err = Run(context.Background(), bad, quietLogger())
	require.True(t, deyerr.HasCode(err, deyerr.B062), "%v", err)
	require.Contains(t, deyerr.As(err).Why(), "127.0.0.1:"+strconv.Itoa(dead))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Run(ctx, bad, quietLogger())
	require.True(t, deyerr.HasCode(err, deyerr.B062))
}

func TestCheckCandidates(t *testing.T) {
	orig := interfaceAddrs
	defer func() { interfaceAddrs = orig }()
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("10.0.0.7"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("1.2.3.4"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("2001:db8::7"), Mask: net.CIDRMask(64, 128)},
			&net.IPAddr{IP: net.ParseIP("9.9.9.9")},
		}, nil
	}
	require.Equal(t, []string{"1.2.3.4:443", "10.0.0.7:443"}, checkCandidates("1.2.3.4", "0.0.0.0", 443))
	require.Equal(t, []string{"1.2.3.4:443", "10.0.0.7:443", "[2001:db8::7]:443"}, checkCandidates("1.2.3.4", "::", 443))
	require.Equal(t, []string{"1.2.3.4:443"}, checkCandidates("1.2.3.4", "1.2.3.4", 443))
	interfaceAddrs = func() ([]net.Addr, error) { return nil, errors.New("boom") }
	require.Equal(t, []string{"1.2.3.4:443"}, checkCandidates("1.2.3.4", "0.0.0.0", 443))
}

func TestCopyChunkFallback(t *testing.T) {
	// A writer without ReadFrom uses the pooled 32 KB buffer.
	var dst bytes.Buffer
	src := bytes.NewReader(bytes.Repeat([]byte("a"), spliceChunk+10))
	n, err := copyChunk(struct{ io.Writer }{&dst}, src)
	require.NoError(t, err)
	require.Equal(t, int64(spliceChunk), n)
	n, err = copyChunk(struct{ io.Writer }{&dst}, src)
	require.NoError(t, err)
	require.Equal(t, int64(10), n)
}

func TestRelayUDPReplayFromOtherAddress(t *testing.T) {
	p := startPair(t, pairOpts{})
	node := p.node.nodeTCP.Addr().String() // UDP shares the port
	m := newMacer([]byte("tok-test"))
	buf := make([]byte, udpBufLen)
	n := copy(buf[udpHeaderLen:], "hello")
	datagram := append([]byte(nil), sealUDP(m, dirToNode, buf, 1, 77, n)...)

	a, err := net.Dial("udp", node)
	require.NoError(t, err)
	defer a.Close()
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = a.Write(datagram)
	require.NoError(t, err)
	reply := make([]byte, udpBufLen)
	k, err := a.Read(reply)
	require.NoError(t, err)
	index, id, payload, ok := openUDP(m, dirToHub, reply[:k])
	require.True(t, ok, "the node's reply is authenticated")
	require.Equal(t, 1, index)
	require.Equal(t, uint64(77), id)
	require.Equal(t, "hello", string(payload))

	// The node's own reply reflected back to it is not a valid request (it
	// would otherwise reach the target again and echo back to the sender).
	_, err = a.Write(append([]byte(nil), reply[:k]...))
	require.NoError(t, err)
	_ = a.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = a.Read(reply)
	require.Error(t, err, "a reflected node→hub frame must be dropped")
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))

	// The same captured datagram from another address is not relayed.
	b, err := net.Dial("udp", node)
	require.NoError(t, err)
	defer b.Close()
	_, err = b.Write(datagram)
	require.NoError(t, err)
	_ = b.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err = b.Read(reply)
	require.Error(t, err)
	require.Equal(t, 1, p.node.nodeUDP.count())
}

// TestRelayStalledReader: a client that keeps sending but never reads its
// echo stalls every direction in a write; no progress is reported, so the
// idle watchdog must reap the connection (no goroutine or fd leak).
func TestRelayStalledReader(t *testing.T) {
	p := startPair(t, pairOpts{idleS: 1})
	c := dialTCP(t, p.hubTCP())
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	chunk := make([]byte, 1<<20)
	errc := make(chan error, 1)
	go func() {
		for i := 0; i < 512; i++ {
			if _, err := c.Write(chunk); err != nil {
				errc <- err
				return
			}
		}
		errc <- nil
	}()
	select {
	case err := <-errc:
		require.Error(t, err, "the relay must close a stalled connection")
		var ne net.Error
		if errors.As(err, &ne) {
			require.False(t, ne.Timeout(), "closed by the relay, not by the test deadline")
		}
	case <-time.After(25 * time.Second):
		t.Fatal("stalled connection was not reaped")
	}
	require.Eventually(t, func() bool { return p.hub.activeConns() == 0 && p.node.activeConns() == 0 },
		5*time.Second, 20*time.Millisecond)
}
