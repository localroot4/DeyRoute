package front

import (
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
)

func tcpListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// dialAndSend connects to ln and writes tag.
func dialAndSend(t *testing.T, ln net.Listener, tag string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Write([]byte(tag))
	require.NoError(t, err)
	return c
}

func readTag(t *testing.T, c net.Conn, n int) string {
	t.Helper()
	b := make([]byte, n)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := io.ReadFull(c, b)
	require.NoError(t, err)
	return string(b)
}

func TestFanInMergesAllInputs(t *testing.T) {
	a, b := tcpListener(t), tcpListener(t)
	f := FanIn(a, b)
	defer f.Close()
	assert.Equal(t, a.Addr().String(), f.Addr().String(), "the address of the first")

	dialAndSend(t, a, "A1")
	dialAndSend(t, b, "B1")
	dialAndSend(t, a, "A2")
	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		c, err := f.Accept()
		require.NoError(t, err)
		got[readTag(t, c, 2)] = true
		_ = c.Close()
	}
	assert.Equal(t, map[string]bool{"A1": true, "B1": true, "A2": true}, got)
}

func TestFanInInputFailureDoesNotStopTheOthers(t *testing.T) {
	boom := errors.New("input died")
	bad := newFailingListener(t, boom)
	good := tcpListener(t)
	f := FanIn(bad, good)
	defer f.Close()

	// The bad input fails at once; the good one keeps delivering.
	for i := 0; i < 3; i++ {
		dialAndSend(t, good, "ok")
		c, err := f.Accept()
		require.NoError(t, err, "round %d", i)
		assert.Equal(t, "ok", readTag(t, c, 2))
		_ = c.Close()
	}
}

func TestFanInAllInputsFailed(t *testing.T) {
	boom := errors.New("input died")
	f := FanIn(newFailingListener(t, boom), newFailingListener(t, boom))
	defer f.Close()
	_, err := f.Accept()
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, net.ErrClosed, "a failure is not a close")
	require.NoError(t, f.Close())
}

func TestFanInNoInputs(t *testing.T) {
	f := FanIn()
	_, err := f.Accept()
	assert.Error(t, err)
	assert.NotNil(t, f.Addr())
	assert.NoError(t, f.Close())
}

func TestFanInRetriesTemporaryErrors(t *testing.T) {
	fl := newFailingListener(t,
		&net.OpError{Op: "accept", Err: timeoutErr{}},
		&net.OpError{Op: "accept", Err: syscall.EMFILE},
		&net.OpError{Op: "accept", Err: syscall.ECONNABORTED},
	)
	f := FanIn(fl)
	defer f.Close()
	dialAndSend(t, fl, "ok")
	c, err := f.Accept()
	require.NoError(t, err)
	assert.Equal(t, "ok", readTag(t, c, 2))
	_ = c.Close()
}

func TestFanInCloseSemantics(t *testing.T) {
	a, b := tcpListener(t), tcpListener(t)
	f := FanIn(a, b)

	// Accept blocks until Close, then returns net.ErrClosed.
	errc := make(chan error, 1)
	go func() { _, err := f.Accept(); errc <- err }()
	select {
	case err := <-errc:
		t.Fatalf("Accept returned early: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.NoError(t, f.Close())
	require.NoError(t, f.Close(), "idempotent")
	select {
	case err := <-errc:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(3 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
	_, err := f.Accept()
	assert.ErrorIs(t, err, net.ErrClosed, "and it keeps doing so")

	// Every input is closed.
	for _, ln := range []net.Listener{a, b} {
		_, err := net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond)
		assert.Error(t, err)
	}
}

func TestFanInCloseClosesConnsNotHandedOut(t *testing.T) {
	a := tcpListener(t)
	f := FanIn(a)
	// Connections accepted by the input goroutine while nobody calls Accept.
	c1 := dialAndSend(t, a, "x")
	c2 := dialAndSend(t, a, "y")
	time.Sleep(100 * time.Millisecond) // the goroutine holds one of them, the kernel queue the other
	require.NoError(t, f.Close())
	for _, c := range []net.Conn{c1, c2} {
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err := io.ReadAll(c)
		var ne net.Error
		assert.False(t, errors.As(err, &ne) && ne.Timeout(), "the connection is closed, not left hanging")
	}
}

func TestFanInConcurrentAcceptors(t *testing.T) {
	a, b := tcpListener(t), tcpListener(t)
	f := FanIn(a, b)
	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, err := f.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				n++
				mu.Unlock()
				_ = c.Close()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		ln := a
		if i%2 == 1 {
			ln = b
		}
		c, err := net.Dial("tcp", ln.Addr().String())
		require.NoError(t, err)
		_ = c.Close()
	}
	poll(t, "20 accepts", func() bool { mu.Lock(); defer mu.Unlock(); return n == 20 })
	require.NoError(t, f.Close())
	wg.Wait()
}

// TestFanInWithTheFront: the shape the hub uses, one Serve loop over the direct
// control listener and the front.
func TestFanInWithTheFront(t *testing.T) {
	direct := tcpListener(t)
	srv := startServer(t, ServerOptions{TLSMode: config.FrontTLSOff})
	f := FanIn(direct, srv)
	defer f.Close()

	// A front connection and a direct one arrive on the same listener.
	wc := wsUpgrade(t, srv.Addr().String(), nil, "/"+testSecret+"/c")
	dc := dialAndSend(t, direct, "direct")
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		c, err := f.Accept()
		require.NoError(t, err)
		if fc, ok := c.(*Conn); ok {
			assert.Equal(t, ViaFront, fc.Via())
			seen["front"] = true
			_ = wc.Close()
		} else {
			assert.Equal(t, "direct", readTag(t, c, 6))
			seen["direct"] = true
		}
		_ = c.Close()
	}
	assert.Equal(t, map[string]bool{"front": true, "direct": true}, seen)
	_ = dc.Close()

	// The front dying does not stop the direct side.
	require.NoError(t, srv.Close())
	dialAndSend(t, direct, "again!")
	c, err := f.Accept()
	require.NoError(t, err)
	assert.Equal(t, "again!", readTag(t, c, 6))
	_ = c.Close()

	// Close closes both, and Accept then says so.
	require.NoError(t, f.Close())
	_, err = f.Accept()
	assert.ErrorIs(t, err, net.ErrClosed)
}
