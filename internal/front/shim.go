package front

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// ShimConfig configures RunShim.
type ShimConfig struct {
	Target Target  // the hub's front
	Dialer *Dialer // nil = the zero Dialer (system roots)
	// Port is the backend control port the local listener stands for.
	Port int
	// Node is this node's id and Token the tunnel token (preface).
	Node  string
	Token string
	// InnerTLS is the client config of the inner TLS: the deyroute CA, the hub
	// role check, no client certificate (tlsutil.ClientTLSConfig).
	InnerTLS *tls.Config
	// MaxDials bounds the data connections being opened at once (0 = 64).
	MaxDials int
	// Logger receives one line per failure kind and minute; nil discards.
	Logger *slog.Logger
}

const defaultShimDials = 64

// RunShim accepts local connections on ln (127.0.0.1:<Port>, bound by the
// caller) and carries each through the front until ctx ends; then it closes
// ln and every connection and returns nil. Remote failures never end it:
// the local connection is reset and the backend client retries, so the
// systemd unit does not restart in a loop while the CDN is unreachable.
func RunShim(ctx context.Context, ln net.Listener, c ShimConfig) error {
	if c.Dialer == nil {
		c.Dialer = &Dialer{}
	}
	if c.MaxDials <= 0 {
		c.MaxDials = defaultShimDials
	}
	log := c.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &shim{c: c, log: newRateLog(log, time.Minute), slots: make(chan struct{}, c.MaxDials), conns: map[net.Conn]struct{}{}}
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer func() {
		s.closeAll()
		wg.Wait()
	}()
	var delay time.Duration
	for {
		lc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			delay = backoff(delay)
			s.log.warn("accept", "front shim: accept failed", "error", err)
			select {
			case <-time.After(delay):
				continue
			case <-ctx.Done():
				return nil
			}
		}
		delay = 0
		if !s.track(lc) {
			_ = lc.Close()
			return nil
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.untrack(lc)
			s.serve(ctx, lc)
		}()
	}
}

type shim struct {
	c     ShimConfig
	log   *rateLog
	slots chan struct{}

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func (s *shim) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *shim) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

func (s *shim) closeAll() {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

// reset closes the local connection so the backend client sees a reset.
func reset(c net.Conn) {
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.Close()
}

// serve carries one local connection.
func (s *shim) serve(ctx context.Context, lc net.Conn) {
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		reset(lc)
		return
	}
	tc, err := s.open(ctx)
	<-s.slots
	if err != nil {
		reset(lc)
		return
	}
	// The status byte comes before any byte of the backend.
	status := make([]byte, 1)
	_ = tc.SetReadDeadline(time.Now().Add(dataSetupTimeout))
	if _, err := io.ReadFull(tc, status); err != nil || status[0] != DataOK {
		switch {
		case err != nil:
			s.log.warn("status", "front shim: the hub did not answer the data connection", "port", s.c.Port, "error", err)
		case status[0] == DataRefused:
			s.log.warn("refused", "front shim: the hub refused the data connection (DEY-N018: tunnel token or node changed, or the clocks differ by more than 2 minutes)", "port", s.c.Port)
		default:
			s.log.warn("unreachable", "front shim: the backend server on the hub is not running", "port", s.c.Port)
		}
		_ = tc.Close()
		reset(lc)
		return
	}
	_ = tc.SetReadDeadline(time.Time{})
	pipe(tc, lc)
}

// open dials the front, runs the inner TLS and writes the preface.
func (s *shim) open(ctx context.Context) (*tls.Conn, error) {
	dctx, cancel := context.WithTimeout(ctx, dataSetupTimeout+10*time.Second)
	defer cancel()
	raw, err := s.c.Dialer.DialData(dctx, s.c.Target, s.c.Port)
	if err != nil {
		var de *DialError
		if errors.As(err, &de) {
			s.log.warn("dial:"+de.Class, "front shim: cannot open a data connection through the front", "port", s.c.Port, "reason", de.Reason)
		} else {
			s.log.warn("dial", "front shim: cannot open a data connection through the front", "port", s.c.Port, "error", err)
		}
		return nil, err
	}
	tc := tls.Client(raw, s.c.InnerTLS)
	if err := tc.HandshakeContext(dctx); err != nil {
		s.log.warn("tls", "front shim: inner TLS with the hub failed (DEY-N002 if the hub certificate is not the one this node trusts)", "port", s.c.Port, "error", err)
		_ = raw.Close()
		return nil, err
	}
	pre, err := EncodePreface(s.c.Token, s.c.Port, s.c.Node, time.Now())
	if err == nil {
		_ = tc.SetWriteDeadline(time.Now().Add(dataSetupTimeout))
		_, err = tc.Write(pre)
		_ = tc.SetWriteDeadline(time.Time{})
	}
	if err != nil {
		_ = tc.Close()
		return nil, err
	}
	return tc, nil
}

// rateLog logs each kind of warning at most once per period.
type rateLog struct {
	log    *slog.Logger
	period time.Duration
	mu     sync.Mutex
	last   map[string]time.Time
}

func newRateLog(l *slog.Logger, period time.Duration) *rateLog {
	return &rateLog{log: l, period: period, last: map[string]time.Time{}}
}

func (r *rateLog) warn(kind, msg string, args ...any) {
	r.mu.Lock()
	now := time.Now()
	if t, ok := r.last[kind]; ok && now.Sub(t) < r.period {
		r.mu.Unlock()
		return
	}
	r.last[kind] = now
	r.mu.Unlock()
	r.log.Warn(msg, args...)
}
