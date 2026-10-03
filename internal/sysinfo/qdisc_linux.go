//go:build linux

package sysinfo

import (
	"encoding/binary"
	"errors"
	"net"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Sizes and values of the rtnetlink qdisc dump (linux/rtnetlink.h,
// linux/pkt_sched.h).
const (
	sizeofTcMsg   = 20         // struct tcmsg
	tcHRoot       = 0xFFFFFFFF // TC_H_ROOT: the parent of a root qdisc
	nlaTypeMask   = 0x3FFF     // NLA_TYPE_MASK
	qdiscTimeout  = 2 * time.Second
	qdiscRecvSize = 64 << 10
)

// errNetlink is a malformed or failed rtnetlink answer.
var errNetlink = errors.New("sysinfo: bad rtnetlink answer")

// RootQdisc returns the kind of the root queueing discipline of interface
// iface ("fq", "fq_codel", "mq", "noqueue", ...), read with an rtnetlink
// RTM_GETQDISC dump. It is "" when the interface does not exist or the
// kernel cannot be asked; it never runs tc.
func RootQdisc(iface string) string {
	if !ValidIface(iface) {
		return ""
	}
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	kind, err := rootQdisc(ifi.Index)
	if err != nil {
		return ""
	}
	return kind
}

// rootQdisc dumps the qdiscs and returns the kind of the root qdisc of the
// interface with the given index.
func rootQdisc(index int) (string, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return "", err
	}
	defer func() { _ = unix.Close(fd) }()
	tv := unix.NsecToTimeval(qdiscTimeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return "", err
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return "", err
	}
	// The socket is new and private: a fixed sequence number is enough.
	const seq = 1
	if err := unix.Sendto(fd, qdiscRequest(seq, index), 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return "", err
	}
	buf := make([]byte, qdiscRecvSize)
	deadline := time.Now().Add(qdiscTimeout)
	kind := ""
	for time.Now().Before(deadline) {
		n, from, err := unix.Recvfrom(fd, buf, 0)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return "", err
		}
		if sa, ok := from.(*unix.SockaddrNetlink); !ok || sa.Pid != 0 {
			continue // only the kernel answers
		}
		done, k, err := parseQdiscDump(buf[:n], seq, index)
		if err != nil {
			return "", err
		}
		if kind == "" {
			kind = k
		}
		if done {
			return kind, nil
		}
	}
	return "", unix.ETIMEDOUT
}

// qdiscRequest builds an RTM_GETQDISC dump request (nlmsghdr + tcmsg).
func qdiscRequest(seq uint32, index int) []byte {
	ne := binary.NativeEndian
	const size = unix.SizeofNlMsghdr + sizeofTcMsg
	req := make([]byte, size)
	ne.PutUint32(req[0:], size)
	ne.PutUint16(req[4:], unix.RTM_GETQDISC)
	ne.PutUint16(req[6:], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	ne.PutUint32(req[8:], seq)
	// tcmsg: family AF_UNSPEC, ifindex; handle, parent and info stay 0.
	ne.PutUint32(req[unix.SizeofNlMsghdr+4:], uint32(int32(index))) // #nosec G115 -- interface indexes are small positive ints
	return req
}

// parseQdiscDump parses one datagram of the dump answer to seq: kind is
// the root qdisc of the interface index if this datagram has it, done is
// true at NLMSG_DONE.
func parseQdiscDump(b []byte, seq uint32, index int) (done bool, kind string, err error) {
	ne := binary.NativeEndian
	for len(b) >= unix.SizeofNlMsghdr {
		l := int(ne.Uint32(b[0:]))
		typ, s := ne.Uint16(b[4:]), ne.Uint32(b[8:])
		if l < unix.SizeofNlMsghdr || l > len(b) {
			return false, kind, errNetlink
		}
		msg := b[unix.SizeofNlMsghdr:l]
		b = b[min(align4(l), len(b)):]
		if s != seq {
			continue
		}
		switch typ {
		case unix.NLMSG_DONE:
			return true, kind, nil
		case unix.NLMSG_ERROR:
			if len(msg) < 4 {
				return false, kind, errNetlink
			}
			code := int32(ne.Uint32(msg)) // #nosec G115 -- the kernel's negative errno
			if code != 0 {
				return false, kind, syscall.Errno(-code)
			}
		case unix.RTM_NEWQDISC:
			if len(msg) < sizeofTcMsg {
				continue
			}
			ifindex := int32(ne.Uint32(msg[4:])) // #nosec G115 -- tcm_ifindex is an int
			if int(ifindex) != index || ne.Uint32(msg[12:]) != tcHRoot || kind != "" {
				continue
			}
			kind = qdiscKind(msg[sizeofTcMsg:])
		}
	}
	return false, kind, nil
}

// qdiscKind returns the TCA_KIND attribute of a qdisc message ("" when it
// is missing or not a plain name).
func qdiscKind(attrs []byte) string {
	ne := binary.NativeEndian
	for len(attrs) >= 4 {
		l := int(ne.Uint16(attrs[0:]))
		typ := ne.Uint16(attrs[2:]) & nlaTypeMask
		if l < 4 || l > len(attrs) {
			return ""
		}
		if typ == unix.TCA_KIND {
			v := attrs[4:l]
			for len(v) > 0 && v[len(v)-1] == 0 {
				v = v[:len(v)-1]
			}
			if len(v) == 0 || len(v) > 16 {
				return ""
			}
			for _, c := range v {
				if c <= ' ' || c > '~' {
					return ""
				}
			}
			return string(v)
		}
		attrs = attrs[min(align4(l), len(attrs)):]
	}
	return ""
}

// align4 rounds n up to the netlink alignment.
func align4(n int) int { return (n + 3) &^ 3 }
