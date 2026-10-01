package systemd

import (
	"context"
	"net"
	"os"
	"strconv"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// sd_notify states used by the hub and node services (Type=notify units).
const (
	StateReady     = "READY=1"
	StateWatchdog  = "WATCHDOG=1"
	StateStopping  = "STOPPING=1"
	StateReloading = "RELOADING=1"
)

// notifyTimeout bounds one datagram write to the notify socket.
const notifyTimeout = time.Second

// Notify sends state (e.g. "READY=1", or several newline-separated
// assignments) to the service manager over $NOTIFY_SOCKET. It is a no-op
// returning nil when the variable is unset (not started by systemd). Path
// sockets and abstract sockets ("@name") are supported.
func Notify(state string) error {
	addr := os.Getenv("NOTIFY_SOCKET")
	if addr == "" {
		return nil
	}
	if addr[0] != '/' && addr[0] != '@' {
		return deyerr.Wrap(deyerr.X000, deyerr.Plain("unsupported NOTIFY_SOCKET address "+strconv.Quote(addr)), nil)
	}
	// On Linux the net package maps a leading '@' to the abstract namespace.
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: addr, Net: "unixgram"})
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetWriteDeadline(time.Now().Add(notifyTimeout)); err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	if _, err := conn.Write([]byte(state)); err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	return nil
}

// NotifyStatus sends a free-form STATUS= line shown by systemctl status.
func NotifyStatus(status string) error { return Notify("STATUS=" + status) }

// WatchdogInterval returns how often the service should send WATCHDOG=1:
// half of $WATCHDOG_USEC, or 0 when the watchdog is disabled or addressed
// to another process ($WATCHDOG_PID).
func WatchdogInterval() time.Duration {
	usec := os.Getenv("WATCHDOG_USEC")
	if usec == "" {
		return 0
	}
	if pid := os.Getenv("WATCHDOG_PID"); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Microsecond / 2
}

// RunWatchdog sends WATCHDOG=1 every WatchdogInterval until ctx is done.
// It returns nil immediately when the watchdog is disabled, and ctx.Err()
// when stopped. Failed pings are reported through onError (may be nil) and
// do not stop the loop.
func RunWatchdog(ctx context.Context, onError func(error)) error {
	iv := WatchdogInterval()
	if iv <= 0 {
		return nil
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := Notify(StateWatchdog); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}
