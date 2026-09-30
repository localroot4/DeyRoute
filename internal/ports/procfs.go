package ports

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ErrNoOwner is returned by ProcFS.Owner when no socket is bound to the port.
var ErrNoOwner = deyerr.Plain("no socket bound to the port")

// tcpListen is the /proc/net/tcp* state of a listening socket.
const tcpListen = "0A"

// ProcFS looks up the process that owns a bound port through
// /proc/net/{tcp,tcp6,udp,udp6} and /proc/<pid>/fd, relative to Root
// ("" or "/" for the real system; a directory tree in tests).
type ProcFS struct {
	Root string
}

func (p ProcFS) path(elem ...string) string {
	root := p.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(append([]string{root, "proc"}, elem...)...)
}

// procSocket is one row of /proc/net/{tcp,udp}[6].
type procSocket struct {
	addr  netip.AddrPort
	inode uint64
}

// Owner returns the process holding port/proto: its pid, its name
// (/proc/<pid>/comm) and the local address it is bound to ("0.0.0.0:443",
// "[::]:443"). TCP sockets count only in LISTEN state; UDP sockets in any
// state. When the socket exists but no process could be matched (another
// user's process without privileges, a kernel socket), pid is 0, name is
// empty and addr is set. ErrNoOwner means nothing is bound to the port.
func (p ProcFS) Owner(port int, proto string) (pid int, name string, addr string, err error) {
	proto, ok := NormalizeProto(proto)
	if !ok || !ValidPort(port) {
		return 0, "", "", ErrNoOwner
	}
	var socks []procSocket
	var readErr error
	for _, f := range []string{proto, proto + "6"} {
		s, err := readProcNet(p.path("net", f), port, proto == ProtoTCP)
		if err != nil {
			if !os.IsNotExist(err) {
				readErr = err
			}
			continue
		}
		socks = append(socks, s...)
	}
	if len(socks) == 0 {
		if readErr != nil {
			return 0, "", "", readErr
		}
		return 0, "", "", ErrNoOwner
	}
	inodes := make(map[uint64]procSocket, len(socks))
	for _, s := range socks {
		if s.inode != 0 {
			inodes[s.inode] = s
		}
	}
	first := showAddr(socks[0].addr)
	if len(inodes) == 0 {
		return 0, "", first, nil
	}
	foundPID, sock, ok := p.findInode(inodes)
	if !ok {
		return 0, "", first, nil
	}
	return foundPID, p.Comm(foundPID), showAddr(sock.addr), nil
}

// showAddr formats a socket address for the owner, writing an IPv4-mapped
// IPv6 address (a dual-stack socket bound to ::ffff:127.0.0.1) as IPv4.
func showAddr(ap netip.AddrPort) string {
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String()
}

// findInode scans /proc/<pid>/fd/* for a "socket:[inode]" link to any of
// inodes and returns the lowest pid holding one (the parent of a
// pre-forked server such as nginx).
func (p ProcFS) findInode(inodes map[uint64]procSocket) (int, procSocket, bool) {
	entries, err := os.ReadDir(p.path())
	if err != nil {
		return 0, procSocket{}, false
	}
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()); err == nil && n > 0 {
			pids = append(pids, n)
		}
	}
	// os.ReadDir sorts by name; order numerically so the lowest pid wins.
	slices.Sort(pids)
	for _, pid := range pids {
		fdDir := p.path(strconv.Itoa(pid), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") || !strings.HasSuffix(link, "]") {
				continue
			}
			ino, err := strconv.ParseUint(link[len("socket:["):len(link)-1], 10, 64)
			if err != nil {
				continue
			}
			if s, ok := inodes[ino]; ok {
				return pid, s, true
			}
		}
	}
	return 0, procSocket{}, false
}

// Comm returns the process name from /proc/<pid>/comm ("" when unknown).
func (p ProcFS) Comm(pid int) string {
	if pid <= 0 {
		return ""
	}
	b, err := os.ReadFile(p.path(strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Unit returns the systemd unit the process runs in, from the last
// "*.service" component of /proc/<pid>/cgroup (cgroup v1 or v2), or "".
// Example: "deyroute-tun@main.de-1.backhaul-wssmux.service".
func (p ProcFS) Unit(pid int) string {
	unit, _ := p.unit(pid)
	return unit
}

// systemSlice is the cgroup of the units of the system manager (PID 1).
const systemSlice = "/system.slice/"

// unit returns Unit(pid) and whether that unit belongs to the system
// manager (its cgroup is under /system.slice/) rather than a user's own
// systemd instance (/user.slice/user-<uid>.slice/user@<uid>.service/…),
// where any local user can name a unit as they like.
func (p ProcFS) unit(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	b, err := os.ReadFile(p.path(strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", false
	}
	unit, system := "", false
	for _, line := range strings.Split(string(b), "\n") {
		// hierarchy-ID:controller-list:cgroup-path
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		comps := strings.Split(parts[2], "/")
		for i := len(comps) - 1; i >= 0; i-- {
			if strings.HasSuffix(comps[i], ".service") {
				unit, system = comps[i], strings.HasPrefix(parts[2], systemSlice)
				break
			}
		}
		if unit != "" && parts[0] == "0" { // prefer the unified (v2) hierarchy
			break
		}
	}
	return unit, system
}

// TunUnitPrefix starts the systemd unit name of every deyroute tunnel
// process ("deyroute-tun@<instance>.service").
const TunUnitPrefix = "deyroute-tun@"

// IsDeyroute reports whether pid is a deyroute tunnel process, the only kind
// the port check may offer to stop (section 10: "Stop that service (only if
// it is a deyroute unit)"): its systemd unit (from /proc/<pid>/cgroup) is a
// deyroute-tun@ instance of the system manager, or it runs outside any
// systemd service and its name starts with "deyroute". A process inside
// another service is never reported, whatever it calls itself (a process
// name is trivially set), and neither is a unit of a user's own systemd
// instance that merely carries a deyroute-tun@ name (stopping the system unit
// of that name would stop a real tunnel and leave the port busy), nor the
// deyroute-hub/deyroute-node daemons: stopping them from the port check would
// stop deyroute itself.
func (p ProcFS) IsDeyroute(pid int) bool {
	if pid <= 0 {
		return false
	}
	if unit, system := p.unit(pid); unit != "" {
		return system && strings.HasPrefix(unit, TunUnitPrefix)
	}
	return strings.HasPrefix(p.Comm(pid), "deyroute")
}

// IsDeyroute is ProcFS{Root: "/"}.IsDeyroute (the real system).
func IsDeyroute(pid int) bool { return ProcFS{Root: "/"}.IsDeyroute(pid) }

// readProcNet returns the sockets of one /proc/net file bound to port.
// listenOnly keeps only TCP LISTEN sockets.
func readProcNet(path string, port int, listenOnly bool) ([]procSocket, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is built from ProcFS.Root
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []procSocket
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		// sl local rem st tx:rx tr:when retrnsmt uid timeout inode ...
		if len(fields) < 10 {
			continue
		}
		if listenOnly && fields[3] != tcpListen {
			continue
		}
		ap, ok := parseHexAddrPort(fields[1])
		if !ok || int(ap.Port()) != port {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, procSocket{addr: ap, inode: inode})
	}
	return out, sc.Err()
}

// parseHexAddrPort decodes "0100007F:01BB" (IPv4) or the 32-hex-digit IPv6
// form. The address is stored as 32-bit words in host byte order; the port
// is a big-endian hex number.
func parseHexAddrPort(s string) (netip.AddrPort, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(b[i:], binary.NativeEndian.Uint32(raw[i:]))
	}
	var addr netip.Addr
	if len(b) == 4 {
		addr = netip.AddrFrom4([4]byte(b))
	} else {
		addr = netip.AddrFrom16([16]byte(b))
	}
	return netip.AddrPortFrom(addr, uint16(port)), true
}
