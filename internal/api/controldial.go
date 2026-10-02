package api

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Reconnect limits shaped by RetryHinter errors.
const (
	// maxRetryDelay caps the wait before a reconnect, whatever a hint asks.
	maxRetryDelay = 10 * time.Minute
	// permanentBackoffMax is the backoff ceiling after an error that
	// reports Permanent: the problem will not go away by itself, so the
	// node retries slowly instead of every 30 s.
	permanentBackoffMax = 5 * time.Minute
	// dialHookOpenTimeout is the default OpenTimeout when a dial hook is set:
	// a tunnelled connect (CDN upgrade) takes more round trips than TCP.
	dialHookOpenTimeout = 30 * time.Second
)

// dialHook opens the raw connection to the hub (below the control channel
// TLS layer): a plain TCP dial by default, a CDN front or another carrier
// when set.
type dialHook = func(ctx context.Context) (net.Conn, error)

// RetryHinter is implemented by errors that tell ControlClient.Run how to
// pace the next reconnect (found with errors.As).
type RetryHinter interface {
	// RetryAfter is the minimum wait before the next attempt (0 = none).
	RetryAfter() time.Duration
	// Permanent reports that retrying will not help until the configuration
	// or the far side changes: the client keeps trying, but slowly, and logs
	// the problem once instead of once per attempt.
	Permanent() bool
}

// retryHint extracts the RetryHinter of err.
func retryHint(err error) (after time.Duration, permanent bool) {
	var h RetryHinter
	if err != nil && errors.As(err, &h) {
		return max(h.RetryAfter(), 0), h.Permanent()
	}
	return 0, false
}

// planReconnect returns the wait before the next attempt, the backoff ceiling
// in force and the (clamped) backoff the delay was drawn from. The wait is
// the jittered backoff, raised to the error's hint and capped at
// maxRetryDelay. A permanent error starts at maxB and may grow up to
// permanentBackoffMax.
func planReconnect(backoff, minB, maxB, hint time.Duration, permanent bool) (delay, ceiling, clamped time.Duration) {
	ceiling = maxB
	if permanent {
		ceiling = max(maxB, permanentBackoffMax)
		backoff = max(backoff, maxB)
	}
	clamped = min(backoff, ceiling)
	delay = min(max(jitter(clamped, minB), hint), maxRetryDelay)
	return delay, ceiling, clamped
}

// dial returns the dial hook for the next connect: DialFunc (rebuilt from
// the current configuration on every connect) wins over Dial; nil = plain
// TCP.
func (c *ControlClient) dial() dialHook {
	if c.DialFunc != nil {
		if d := c.DialFunc(); d != nil {
			return d
		}
	}
	return c.Dial
}

// tlsClientConfig returns a copy of cfg with ServerName defaulted from addr
// the way tls.Dialer does.
func tlsClientConfig(cfg *tls.Config, addr string) *tls.Config {
	cfg = cfg.Clone()
	if cfg.ServerName == "" {
		cfg.ServerName = hostOf(addr)
	}
	return cfg
}

// dialHooked opens the connection through dial, wraps it with the control
// channel TLS and completes the handshake. The raw connection is closed when
// the handshake fails. Every failure is recorded in box: an inner failure
// (the pinned CA, DEY-N002) is never replaced by a later outer one.
func dialHooked(ctx context.Context, dial dialHook, addr string, cfg *tls.Config, box *dialErrBox) (net.Conn, error) {
	raw, err := dial(ctx)
	if err == nil && raw == nil {
		err = deyerr.Wrap(deyerr.X000, errors.New("dial hook returned no connection"), nil)
	}
	if err != nil {
		if box != nil {
			box.set(err)
		}
		return nil, err
	}
	tc := tls.Client(raw, tlsClientConfig(cfg, addr))
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		if box != nil {
			box.set(err)
		}
		return nil, err
	}
	return tc, nil
}

// dialErrBox keeps the dial error of one transport (the HTTP/2 layer may
// wrap or replace it). A DEY-N002 is sticky: it is the answer the owner
// needs, whatever the outer layers report afterwards.
type dialErrBox struct {
	mu  sync.Mutex
	err error
}

func (b *dialErrBox) set(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil && deyerr.HasCode(b.err, deyerr.N002) {
		return
	}
	b.err = err
}

func (b *dialErrBox) get() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}
