package health

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// runEcho starts s on a loopback listener and stops it at cleanup.
func runEcho(t *testing.T, s TCPEchoServer) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return ln.Addr().String()
}

func TestTCPEchoServerEchoes(t *testing.T) {
	addr := runEcho(t, TCPEchoServer{})
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close()
	msg := []byte("hello through the canary")
	_, err = c.Write(msg)
	require.NoError(t, err)
	got := make([]byte, len(msg))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(c, got)
	require.NoError(t, err)
	assert.Equal(t, msg, got)
}

func TestTCPEchoServerByteLimit(t *testing.T) {
	addr := runEcho(t, TCPEchoServer{MaxBytes: 10})
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte("0123456789abcdefghij"))
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil {
		// The server closed with unread bytes: a reset may follow the echo.
		assert.ErrorIs(t, err, syscall.ECONNRESET)
	}
	assert.LessOrEqual(t, len(got), 10)
	if len(got) > 0 {
		assert.Equal(t, "0123456789"[:len(got)], string(got))
	}
}

func TestTCPEchoServerIdleTimeout(t *testing.T) {
	addr := runEcho(t, TCPEchoServer{IdleTimeout: 50 * time.Millisecond})
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	start := time.Now()
	_, err = c.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
	assert.Less(t, time.Since(start), time.Second)
}

func TestTCPEchoServerMaxConns(t *testing.T) {
	addr := runEcho(t, TCPEchoServer{MaxConns: 1})
	first, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer first.Close()
	// Make sure the first connection is being served.
	_, err = first.Write([]byte("x"))
	require.NoError(t, err)
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(first, make([]byte, 1))
	require.NoError(t, err)

	second, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = second.Read(make([]byte, 1))
	assert.Error(t, err, "the extra connection is closed at once")
}

func TestServeTCPEchoStopClosesConnections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeTCPEcho(ctx, ln) }()
	c, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte("x"))
	require.NoError(t, err)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(c, make([]byte, 1))
	require.NoError(t, err)

	cancel()
	require.NoError(t, <-done)
	_, err = c.Read(make([]byte, 1))
	assert.Error(t, err, "open connections are closed on stop")
	_, err = net.Dial("tcp", ln.Addr().String())
	assert.Error(t, err, "the listener is closed on stop")
}

// fakeListener returns scripted Accept errors.
type fakeListener struct {
	mu   sync.Mutex
	errs []error
}

func (f *fakeListener) Accept() (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) == 0 {
		return nil, net.ErrClosed
	}
	err := f.errs[0]
	f.errs = f.errs[1:]
	return nil, err
}
func (f *fakeListener) Close() error { return nil }
func (f *fakeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9}
}

func TestServeConnsAcceptErrors(t *testing.T) {
	// Temporary errors are retried with backoff; ErrClosed ends normally.
	fl := &fakeListener{errs: []error{
		&net.OpError{Op: "accept", Err: syscall.EMFILE},
		&net.OpError{Op: "accept", Err: syscall.ECONNABORTED},
	}}
	require.NoError(t, ServeTCPEcho(context.Background(), fl))

	// Anything else is DEY-X051.
	fl = &fakeListener{errs: []error{errors.New("boom")}}
	err := ServeSpeed(context.Background(), fl, 1)
	require.Error(t, err)
	assert.True(t, deyerr.HasCode(err, deyerr.X051))
	assert.Contains(t, deyerr.As(err).Message(), "127.0.0.1:9")
}

func TestNextBackoff(t *testing.T) {
	assert.Equal(t, 5*time.Millisecond, nextBackoff(0))
	assert.Equal(t, 10*time.Millisecond, nextBackoff(5*time.Millisecond))
	assert.Equal(t, time.Second, nextBackoff(800*time.Millisecond))
	assert.Equal(t, time.Second, nextBackoff(time.Second))
}
