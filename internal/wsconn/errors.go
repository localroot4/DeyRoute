package wsconn

import (
	"errors"
	"fmt"
	"net"
)

// ErrClosed is wrapped by the error Read returns after the peer sent a close
// frame. It is never io.EOF, whatever the close code: crypto/tls maps an EOF at
// a record boundary to io.EOF, which would be indistinguishable from a clean
// close_notify and would hide a truncated stream.
var ErrClosed = errors.New("wsconn: connection closed by the peer")

// ErrProtocol is wrapped by the error Read returns after the peer violated the
// framing rules (the adapter answered with a close frame and dropped the
// connection).
var ErrProtocol = errors.New("wsconn: protocol error")

// ErrIdleTimeout is wrapped by the error Read and Write return after the idle
// watchdog (Config.IdleTimeout) cut the connection.
var ErrIdleTimeout = errors.New("wsconn: idle timeout")

// CloseError is the error behind ErrClosed: the close code and reason the peer
// sent (CloseNoStatus when the close frame had no code).
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("wsconn: connection closed by the peer (code %d: %s)", e.Code, e.Reason)
	}
	return fmt.Sprintf("wsconn: connection closed by the peer (code %d)", e.Code)
}

// Is makes errors.Is(err, ErrClosed) true.
func (e *CloseError) Is(target error) bool { return target == ErrClosed }

// ProtocolError describes a framing violation by the peer; Code is the close
// code the adapter sent (1002, 1003 or 1009).
type ProtocolError struct {
	Code   int
	Reason string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wsconn: protocol error (code %d): %s", e.Code, e.Reason)
}

// Is makes errors.Is(err, ErrProtocol) true.
func (e *ProtocolError) Is(target error) bool { return target == ErrProtocol }

// errLocalClose is what Read and Write return after a local Close.
var errLocalClose = fmt.Errorf("wsconn: %w", net.ErrClosed)

// errIdle is what Read and Write return after an idle kill.
var errIdle = fmt.Errorf("%w: no frame arrived after a ping was sent", ErrIdleTimeout)

// isTimeout reports whether err is a deadline expiry (which leaves the
// connection usable).
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
