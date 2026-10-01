//go:build linux

package wireguard

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// genlSocket is a NETLINK_GENERIC socket.
type genlSocket struct{ fd int }

// openGenetlink opens and binds a generic netlink socket.
func openGenetlink() (nlTransport, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_GENERIC)
	if err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &genlSocket{fd: fd}, nil
}

// Close closes the socket.
func (s *genlSocket) Close() error { return unix.Close(s.fd) }

// Roundtrip sends req and collects the replies with sequence number seq
// until the kernel's ACK (NLMSG_ERROR) or NLMSG_DONE. The receive timeout
// follows ctx's deadline.
func (s *genlSocket) Roundtrip(ctx context.Context, req []byte, seq uint32) ([]nlMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := unix.Sendto(s.fd, req, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	buf := make([]byte, 1<<16)
	var out []nlMessage
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wait := 5 * time.Second
		if dl, ok := ctx.Deadline(); ok {
			wait = max(time.Until(dl), time.Millisecond)
		}
		tv := unix.NsecToTimeval(wait.Nanoseconds())
		if err := unix.SetsockoptTimeval(s.fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
			return nil, err
		}
		n, _, err := unix.Recvfrom(s.fd, buf, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			return nil, context.DeadlineExceeded
		}
		if err != nil {
			return nil, err
		}
		msgs, err := parseMessages(append([]byte(nil), buf[:n]...))
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if m.seq != seq {
				continue
			}
			switch m.typ {
			case nlmsgError:
				return out, ackError(m)
			case nlmsgDone:
				return out, nil
			}
			out = append(out, m)
		}
	}
}
