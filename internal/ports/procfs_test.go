package ports

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeProc builds a /proc tree under a temp dir.
type fakeProc struct {
	t    *testing.T
	root string
	rows map[string][]string // net file → rows
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	return &fakeProc{t: t, root: t.TempDir(), rows: map[string][]string{}}
}

// hexAddrPort encodes an address the way the kernel prints it.
func hexAddrPort(ap netip.AddrPort) string {
	b := ap.Addr().AsSlice()
	out := make([]byte, len(b))
	for i := 0; i < len(b); i += 4 {
		binary.NativeEndian.PutUint32(out[i:], binary.BigEndian.Uint32(b[i:]))
	}
	return strings.ToUpper(hex.EncodeToString(out)) + ":" + fmt.Sprintf("%04X", ap.Port())
}

// sock adds a row to /proc/net/<file>.
func (f *fakeProc) sock(file, addr, state string, inode uint64) {
	ap := netip.MustParseAddrPort(addr)
	row := fmt.Sprintf("   %d: %s 00000000:0000 %s 00000000:00000000 00:00000000 00000000     0        0 %d 1 0000000000000000 100 0 0 10 0",
		len(f.rows[file]), hexAddrPort(ap), state, inode)
	if strings.HasSuffix(file, "6") {
		row = strings.Replace(row, "00000000:0000", strings.Repeat("0", 32)+":0000", 1)
	}
	f.rows[file] = append(f.rows[file], row)
}

// proc adds a process with a comm, cgroup and socket fds.
func (f *fakeProc) proc(pid int, comm, cgroup string, inodes ...uint64) {
	dir := filepath.Join(f.root, "proc", strconv.Itoa(pid))
	require.NoError(f.t, os.MkdirAll(filepath.Join(dir, "fd"), 0o755))
	require.NoError(f.t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))
	if cgroup != "" {
		require.NoError(f.t, os.WriteFile(filepath.Join(dir, "cgroup"), []byte(cgroup), 0o644))
	}
	// Non-socket fds first, as in a real process.
	require.NoError(f.t, os.Symlink("/dev/null", filepath.Join(dir, "fd", "0")))
	require.NoError(f.t, os.Symlink("pipe:[999]", filepath.Join(dir, "fd", "1")))
	require.NoError(f.t, os.Symlink("socket:[bad]", filepath.Join(dir, "fd", "2")))
	for i, ino := range inodes {
		require.NoError(f.t, os.Symlink(fmt.Sprintf("socket:[%d]", ino), filepath.Join(dir, "fd", strconv.Itoa(3+i))))
	}
}

func (f *fakeProc) write() ProcFS {
	net := filepath.Join(f.root, "proc", "net")
	require.NoError(f.t, os.MkdirAll(net, 0o755))
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"
	for file, rows := range f.rows {
		content := header + "\n" + strings.Join(rows, "\n") + "\n"
		require.NoError(f.t, os.WriteFile(filepath.Join(net, file), []byte(content), 0o644))
	}
	// Entries that are not pids are ignored.
	require.NoError(f.t, os.MkdirAll(filepath.Join(f.root, "proc", "self"), 0o755))
	require.NoError(f.t, os.WriteFile(filepath.Join(f.root, "proc", "uptime"), []byte("1 1\n"), 0o644))
	return ProcFS{Root: f.root}
}

func TestParseHexAddrPort(t *testing.T) {
	for _, s := range []string{"0.0.0.0:443", "127.0.0.1:8443", "10.77.3.2:1", "[::]:443", "[2001:db8::1]:65535", "[::ffff:1.2.3.4]:80"} {
		ap := netip.MustParseAddrPort(s)
		got, ok := parseHexAddrPort(hexAddrPort(ap))
		require.True(t, ok, s)
		require.Equal(t, ap, got, s)
	}
	for _, bad := range []string{"", "0100007F", "zz:01BB", "0100007F:zz", "01007F:01BB", "0100007F:10000"} {
		_, ok := parseHexAddrPort(bad)
		require.False(t, ok, bad)
	}
}

func TestProcFSOwner(t *testing.T) {
	f := newFakeProc(t)
	f.sock("tcp", "0.0.0.0:22", "0A", 100)
	f.sock("tcp", "127.0.0.1:443", "01", 0)   // established client socket, not a listener
	f.sock("tcp", "0.0.0.0:443", "06", 0)     // TIME_WAIT
	f.sock("tcp", "0.0.0.0:443", "0A", 200)   // nginx
	f.sock("tcp6", "[::]:443", "0A", 201)     // nginx v6
	f.sock("tcp6", "[::]:8080", "0A", 300)    // held by nobody visible
	f.sock("udp", "0.0.0.0:27015", "07", 400) // game server
	f.sock("udp6", "[::]:5353", "07", 500)    // avahi on v6 only
	f.sock("tcp", "127.0.0.1:9000", "0A", 0)  // no inode (kernel)
	f.sock("tcp", "0.0.0.0:31000", "0A", 600) // deyroute backend
	f.proc(1, "systemd", "0::/init.scope\n")
	f.proc(800, "sshd", "0::/system.slice/ssh.service\n", 100)
	f.proc(1234, "nginx", "0::/system.slice/nginx.service\n", 200, 201)
	f.proc(1235, "nginx", "0::/system.slice/nginx.service\n", 200, 201) // worker shares the socket
	f.proc(2000, "srcds_linux", "", 400)
	f.proc(2100, "avahi-daemon", "0::/system.slice/avahi-daemon.service\n", 500)
	f.proc(3000, "backhaul", "0::/system.slice/system-deyroute\\x2dtun.slice/deyroute-tun@main.de-1.backhaul-wssmux.service\n", 600)
	p := f.write()

	pid, name, addr, err := p.Owner(443, "tcp")
	require.NoError(t, err)
	require.Equal(t, 1234, pid)
	require.Equal(t, "nginx", name)
	require.Equal(t, "0.0.0.0:443", addr)

	pid, name, addr, err = p.Owner(22, "TCP")
	require.NoError(t, err)
	require.Equal(t, 800, pid)
	require.Equal(t, "sshd", name)
	require.Equal(t, "0.0.0.0:22", addr)

	pid, name, addr, err = p.Owner(27015, "udp")
	require.NoError(t, err)
	require.Equal(t, 2000, pid)
	require.Equal(t, "srcds_linux", name)
	require.Equal(t, "0.0.0.0:27015", addr)

	pid, name, addr, err = p.Owner(5353, "udp")
	require.NoError(t, err)
	require.Equal(t, 2100, pid)
	require.Equal(t, "avahi-daemon", name)
	require.Equal(t, "[::]:5353", addr)

	// Socket exists but no visible owner.
	pid, name, addr, err = p.Owner(8080, "tcp")
	require.NoError(t, err)
	require.Zero(t, pid)
	require.Empty(t, name)
	require.Equal(t, "[::]:8080", addr)

	pid, _, addr, err = p.Owner(9000, "tcp")
	require.NoError(t, err)
	require.Zero(t, pid)
	require.Equal(t, "127.0.0.1:9000", addr)

	// Nothing bound; udp is separate from tcp.
	for _, c := range []struct {
		port  int
		proto string
	}{{80, "tcp"}, {443, "udp"}, {27015, "tcp"}, {0, "tcp"}, {443, "sctp"}} {
		_, _, _, err = p.Owner(c.port, c.proto)
		require.ErrorIs(t, err, ErrNoOwner, "%d/%s", c.port, c.proto)
	}

	// Unit, deyroute detection.
	require.Equal(t, "nginx.service", p.Unit(1234))
	require.Equal(t, "deyroute-tun@main.de-1.backhaul-wssmux.service", p.Unit(3000))
	require.Empty(t, p.Unit(1))
	require.Empty(t, p.Unit(2000)) // no cgroup file
	require.Empty(t, p.Unit(0))
	require.True(t, p.IsDeyroute(3000))
	require.False(t, p.IsDeyroute(1234))
	require.False(t, p.IsDeyroute(0))
	require.False(t, p.IsDeyroute(99999))
	require.Equal(t, "nginx", p.Comm(1235))
	require.Empty(t, p.Comm(-1))
	require.Empty(t, p.Comm(424242))
}

func TestProcFSIsDeyrouteByComm(t *testing.T) {
	f := newFakeProc(t)
	f.proc(10, "deyroute", "0::/system.slice/deyroute-hub.service\n")
	f.proc(11, "deyroutex", "")
	f.proc(12, "xray", "0::/user.slice/session-1.scope\n")
	f.proc(13, "deyroute-relay", "0::/system.slice/nginx.service\n")
	f.proc(14, "deyroute", "0::/user.slice/user-0.slice/session-3.scope\n")
	p := f.write()
	// The hub daemon is deyroute but not a tunnel unit: never offered for stopping.
	require.False(t, p.IsDeyroute(10))
	require.Equal(t, "deyroute-hub.service", p.Unit(10))
	// Outside any service, the name decides.
	require.True(t, p.IsDeyroute(11))
	require.True(t, p.IsDeyroute(14))
	require.False(t, p.IsDeyroute(12))
	// A process inside another service cannot pass for deyroute by its name.
	require.False(t, p.IsDeyroute(13))
	require.Equal(t, "nginx.service", p.Unit(13))
}

func TestProcFSUnitCgroupV1(t *testing.T) {
	f := newFakeProc(t)
	f.proc(5, "rathole", "12:pids:/system.slice/system-deyroute\\x2dtun.slice/deyroute-tun@t.n.rathole-tcp.service\n"+
		"1:name=systemd:/system.slice/system-deyroute\\x2dtun.slice/deyroute-tun@t.n.rathole-tcp.service\n"+
		"0::/\n")
	p := f.write()
	require.Equal(t, "deyroute-tun@t.n.rathole-tcp.service", p.Unit(5))
	require.True(t, p.IsDeyroute(5))
}

func TestProcFSMissingTree(t *testing.T) {
	p := ProcFS{Root: t.TempDir()}
	_, _, _, err := p.Owner(443, "tcp")
	require.ErrorIs(t, err, ErrNoOwner)

	// Only tcp exists (IPv6 disabled kernel): still works.
	f := newFakeProc(t)
	f.sock("tcp", "0.0.0.0:443", "0A", 7)
	f.proc(70, "caddy", "", 7)
	pid, name, _, err := f.write().Owner(443, "tcp")
	require.NoError(t, err)
	require.Equal(t, 70, pid)
	require.Equal(t, "caddy", name)
}

func TestProcFSUnreadableNetFile(t *testing.T) {
	root := t.TempDir()
	// A directory where a file is expected produces a read error.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proc", "net", "tcp"), 0o755))
	_, _, _, err := ProcFS{Root: root}.Owner(443, "tcp")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoOwner)
}

func TestProcFSDefaultRoot(t *testing.T) {
	require.Equal(t, "/proc/net/tcp", ProcFS{}.path("net", "tcp"))
	require.Equal(t, "/proc/1/comm", ProcFS{Root: "/"}.path("1", "comm"))
	// The real /proc of this machine is readable.
	if _, err := os.Stat("/proc/self/comm"); err == nil {
		require.NotEmpty(t, ProcFS{}.Comm(os.Getpid()))
		require.False(t, IsDeyroute(os.Getpid()))
	}
}
