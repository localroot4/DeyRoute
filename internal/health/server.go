package health

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Limits of the canary / diagnostics TCP echo (section 9 canary).
const (
	// EchoMaxBytes is the most one connection may echo; the connection is
	// closed after that.
	EchoMaxBytes = 1 << 20
	// EchoIdleTimeout closes a connection that sent nothing for this long.
	EchoIdleTimeout = 30 * time.Second
	// EchoMaxConns bounds concurrent echo connections.
	EchoMaxConns = 64
)

// TCPEchoServer is a TCP echo with per-connection limits. The zero value
// uses EchoMaxBytes, EchoIdleTimeout and EchoMaxConns.
type TCPEchoServer struct {
	MaxBytes    int64
	IdleTimeout time.Duration
	MaxConns    int
}

// ServeTCPEcho runs a TCPEchoServer with the default limits: it echoes
// bytes back (at most 1 MiB per connection, 30 s idle timeout). It is the
// loopback echo behind the canary unit and diagnostics. It takes ownership
// of ln, closes it and every open connection when it returns, and returns
// nil when ctx is done or ln was closed.
func ServeTCPEcho(ctx context.Context, ln net.Listener) error {
	return TCPEchoServer{}.Serve(ctx, ln)
}

// Serve accepts connections on ln until ctx is done (see ServeTCPEcho).
func (s TCPEchoServer) Serve(ctx context.Context, ln net.Listener) error {
	maxBytes := s.MaxBytes
	if maxBytes <= 0 {
		maxBytes = EchoMaxBytes
	}
	idle := s.IdleTimeout
	if idle <= 0 {
		idle = EchoIdleTimeout
	}
	maxConns := s.MaxConns
	if maxConns <= 0 {
		maxConns = EchoMaxConns
	}
	return serveConns(ctx, ln, "tcp echo", maxConns, func(_ context.Context, c net.Conn) {
		echoConn(c, maxBytes, idle)
	})
}

// echoConn copies c back to itself until EOF, an error, the byte limit or
// the idle timeout.
func echoConn(c net.Conn, maxBytes int64, idle time.Duration) {
	buf := make([]byte, 32<<10)
	var total int64
	for total < maxBytes {
		chunk := buf
		if left := maxBytes - total; left < int64(len(chunk)) {
			chunk = chunk[:left]
		}
		_ = c.SetReadDeadline(time.Now().Add(idle))
		n, err := c.Read(chunk)
		if n > 0 {
			_ = c.SetWriteDeadline(time.Now().Add(idle))
			if _, werr := c.Write(chunk[:n]); werr != nil {
				return
			}
			total += int64(n)
		}
		if err != nil {
			return
		}
	}
}

// serveConns is the accept loop shared by the TCP helpers. Each connection
// runs handle in its own goroutine (at most maxConns at once; extra
// connections are closed immediately). On return every tracked connection
// is closed and every handler has finished.
func serveConns(ctx context.Context, ln net.Listener, name string, maxConns int, handle func(context.Context, net.Conn)) error {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	defer closeQuietly(ln)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns = make(map[net.Conn]struct{})
	)
	defer func() {
		mu.Lock()
		for c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	}()
	sem := make(chan struct{}, maxConns)
	var backoff time.Duration
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if isTemporary(err) {
				backoff = nextBackoff(backoff)
				waitUntil(ctx, time.Now().Add(backoff))
				continue
			}
			return deyerr.Wrap(deyerr.X051, err, deyerr.Params{"service": name, "addr": ln.Addr().String()})
		}
		backoff = 0
		select {
		case sem <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}
		mu.Lock()
		conns[c] = struct{}{}
		mu.Unlock()
		wg.Add(1)
		go func() {
			defer func() {
				mu.Lock()
				delete(conns, c)
				mu.Unlock()
				_ = c.Close()
				<-sem
				wg.Done()
			}()
			handle(ctx, c)
		}()
	}
}

// nextBackoff doubles the accept retry delay from 5 ms up to 1 s.
func nextBackoff(d time.Duration) time.Duration {
	if d == 0 {
		return 5 * time.Millisecond
	}
	if d *= 2; d > time.Second {
		d = time.Second
	}
	return d
}

// isTemporary reports errors after which an accept/read loop should retry.
func isTemporary(err error) bool {
	for _, e := range []syscall.Errno{
		syscall.ECONNABORTED, syscall.EMFILE, syscall.ENFILE, syscall.ENOBUFS,
		syscall.ENOMEM, syscall.EINTR, syscall.EAGAIN, syscall.ECONNREFUSED,
		syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.EPROTO,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
