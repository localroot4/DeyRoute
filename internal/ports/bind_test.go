package ports

import (
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

func selfComm(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/self/comm")
	if err != nil {
		t.Skip("no /proc on this system")
	}
	return strings.TrimSpace(string(b))
}

func TestCheckBindRealTCP(t *testing.T) {
	comm := selfComm(t)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	res := CheckBind(port, "tcp")
	require.False(t, res.Free)
	require.Equal(t, ProtoTCP, res.Proto)
	require.Equal(t, os.Getpid(), res.PID)
	require.Equal(t, comm+" (pid "+strconv.Itoa(os.Getpid())+")", res.Process)
	require.Equal(t, "0.0.0.0:"+strconv.Itoa(port), res.Addr)
	require.False(t, res.Deyroute)

	e := deyerr.As(res.Err(port))
	require.Equal(t, deyerr.P012, e.Code)
	require.Equal(t, strconv.Itoa(port)+"/tcp", e.Params["port"])
	require.Contains(t, e.Why(), comm+" (pid ")

	// The same port is free for UDP.
	require.True(t, CheckBind(port, "udp").Free)
}

func TestCheckBindRealLoopbackListener(t *testing.T) {
	selfComm(t)
	// A service on 127.0.0.1 still blocks a tunnel listening on 0.0.0.0.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	res := CheckBind(port, "tcp")
	require.False(t, res.Free)
	require.Equal(t, os.Getpid(), res.PID)
	require.Equal(t, "127.0.0.1:"+strconv.Itoa(port), res.Addr)
}

func TestCheckBindRealUDP(t *testing.T) {
	comm := selfComm(t)
	pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
	require.NoError(t, err)
	defer func() { _ = pc.Close() }()
	port := pc.LocalAddr().(*net.UDPAddr).Port

	res := Checker{ProcFS: ProcFS{Root: "/"}}.CheckBind(context.Background(), port, "UDP")
	require.False(t, res.Free)
	require.Equal(t, ProtoUDP, res.Proto)
	require.Equal(t, os.Getpid(), res.PID)
	require.Equal(t, comm+" (pid "+strconv.Itoa(os.Getpid())+")", res.Process)
}

func TestCheckBindRealFree(t *testing.T) {
	// Find a free port by binding and closing.
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	res := CheckBind(port, "tcp")
	require.True(t, res.Free)
	require.Empty(t, res.Addr)
	require.Zero(t, res.PID)
	require.NoError(t, res.Err(port))
}

func TestCheckBindInvalid(t *testing.T) {
	require.False(t, CheckBind(0, "tcp").Free)
	require.False(t, CheckBind(70000, "tcp").Free)
	res := CheckBind(443, "icmp")
	require.False(t, res.Free)
	require.Equal(t, "icmp", res.Proto)

	// Invalid input is DEY-P010, not "already in use".
	e := deyerr.As(res.Err(443))
	require.Equal(t, deyerr.P010, e.Code)
	require.Equal(t, "443/icmp", e.Params["input"])
	e = deyerr.As(CheckBind(0, "tcp").Err(0))
	require.Equal(t, deyerr.P010, e.Code)
	require.Equal(t, "0/tcp", e.Params["input"])
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// scriptedListen fails the given networks with the given errors.
func scriptedListen(fail map[string]error) func(network, address string) (io.Closer, error) {
	return func(network, _ string) (io.Closer, error) {
		if err, ok := fail[network]; ok {
			return nil, &net.OpError{Op: "listen", Net: network, Err: os.NewSyscallError("bind", err)}
		}
		return nopCloser{}, nil
	}
}

func TestCheckBindScripted(t *testing.T) {
	f := newFakeProc(t)
	f.sock("tcp6", "[::]:443", "0A", 11)
	f.sock("tcp", "0.0.0.0:80", "0A", 12)
	f.sock("udp", "0.0.0.0:53", "07", 13)
	f.proc(4242, "deyroute", "0::/system.slice/system-deyroute\\x2dtun.slice/deyroute-tun@main.canary.service\n", 11)
	f.proc(4343, "apache2", "0::/system.slice/apache2.service\n", 12)
	procfs := f.write()

	t.Run("v6 only listener", func(t *testing.T) {
		c := Checker{ProcFS: procfs, listen: scriptedListen(map[string]error{"tcp6": syscall.EADDRINUSE})}
		res := c.CheckBind(context.Background(), 443, "tcp")
		require.False(t, res.Free)
		require.Equal(t, 4242, res.PID)
		require.Equal(t, "deyroute (pid 4242)", res.Process)
		require.Equal(t, "[::]:443", res.Addr)
		require.True(t, res.Deyroute)
		require.Equal(t, "deyroute-tun@main.canary.service", res.Unit)
	})
	t.Run("no ipv6 kernel", func(t *testing.T) {
		c := Checker{ProcFS: procfs, listen: scriptedListen(map[string]error{"tcp6": syscall.EAFNOSUPPORT})}
		require.True(t, c.CheckBind(context.Background(), 8443, "tcp").Free)
	})
	t.Run("permission denied and nobody listens", func(t *testing.T) {
		c := Checker{ProcFS: procfs, listen: scriptedListen(map[string]error{"tcp4": syscall.EACCES, "tcp6": syscall.EACCES})}
		res := c.CheckBind(context.Background(), 443+1, "tcp")
		require.True(t, res.Free)
		require.Empty(t, res.Addr)
	})
	t.Run("permission denied but owner found", func(t *testing.T) {
		c := Checker{ProcFS: procfs, listen: scriptedListen(map[string]error{"tcp4": syscall.EACCES, "tcp6": syscall.EACCES})}
		res := c.CheckBind(context.Background(), 80, "tcp")
		require.False(t, res.Free)
		require.Equal(t, "apache2 (pid 4343)", res.Process)
		require.Equal(t, "0.0.0.0:80", res.Addr)
		require.False(t, res.Deyroute)
		require.Equal(t, "apache2.service", res.Unit)
	})
	t.Run("in use, no owner anywhere", func(t *testing.T) {
		c := Checker{ProcFS: procfs, listen: scriptedListen(map[string]error{"udp4": syscall.EADDRINUSE})}
		res := c.CheckBind(context.Background(), 9999, "udp")
		require.False(t, res.Free)
		require.Zero(t, res.PID)
		require.Empty(t, res.Process)
		require.Equal(t, "0.0.0.0:9999", res.Addr)
		e := deyerr.As(res.Err(9999))
		require.Equal(t, "another process", e.Params["process"])
		require.Equal(t, "9999/udp", e.Params["port"])
	})
	t.Run("socket without visible pid, ss names it", func(t *testing.T) {
		fake := exec.NewFake().On("ss -Hlntup", exec.OK(
			"udp   UNCONN 0      0      127.0.0.53%lo:53        0.0.0.0:*    users:((\"systemd-resolve\",pid=500,fd=14))\n"))
		c := Checker{Runner: fake, ProcFS: procfs, listen: scriptedListen(map[string]error{"udp4": syscall.EADDRINUSE})}
		res := c.CheckBind(context.Background(), 53, "udp")
		require.False(t, res.Free)
		require.Equal(t, 500, res.PID)
		require.Equal(t, "systemd-resolve (pid 500)", res.Process)
		require.Equal(t, "0.0.0.0:53", res.Addr) // /proc knew the address
		require.True(t, fake.Called("ss -Hlntup"))
	})
	t.Run("ss fails", func(t *testing.T) {
		fake := exec.NewFake().On("ss -Hlntup", exec.Fail(1, "boom"))
		c := Checker{Runner: fake, ProcFS: procfs, listen: scriptedListen(map[string]error{"udp4": syscall.EADDRINUSE})}
		res := c.CheckBind(context.Background(), 53, "udp")
		require.False(t, res.Free)
		require.Zero(t, res.PID)
	})
	t.Run("ss only", func(t *testing.T) {
		fake := exec.NewFake().On("ss -Hlntup", exec.OK(
			"tcp   LISTEN 0      511    *:8081      *:*    users:((\"node\",pid=77,fd=20))\n"))
		c := Checker{Runner: fake, ProcFS: ProcFS{Root: t.TempDir()}, listen: scriptedListen(map[string]error{"tcp4": syscall.EADDRINUSE})}
		res := c.CheckBind(context.Background(), 8081, "tcp")
		require.False(t, res.Free)
		require.Equal(t, "node (pid 77)", res.Process)
		require.Equal(t, "0.0.0.0:8081", res.Addr)
	})
}

func TestParseSS(t *testing.T) {
	out := `tcp   LISTEN 0      4096   127.0.0.53%lo:53         0.0.0.0:*    users:(("systemd-resolve",pid=500,fd=14))
tcp   LISTEN 0      128          0.0.0.0:22         0.0.0.0:*    users:(("sshd",pid=800,fd=3))
tcp   LISTEN 0      511          0.0.0.0:443        0.0.0.0:*    users:(("nginx",pid=1234,fd=6),("nginx",pid=1235,fd=6))
tcp   LISTEN 0      4096            [::]:8443          [::]:*    users:(("xray",pid=99,fd=7))
tcp   LISTEN 0      4096   [2001:db8::1]:9443          [::]:*    users:(("caddy",pid=98,fd=7))
udp   UNCONN 0      0            0.0.0.0:27015      0.0.0.0:*    users:(("srcds",pid=1500,fd=9))
tcp   LISTEN 0      511          0.0.0.0:7000       0.0.0.0:*
garbage
`
	tests := []struct {
		port  int
		proto string
		pid   int
		name  string
		addr  string
		ok    bool
	}{
		{53, "tcp", 500, "systemd-resolve", "127.0.0.53:53", true},
		{443, "tcp", 1234, "nginx", "0.0.0.0:443", true},
		{8443, "tcp", 99, "xray", "[::]:8443", true},
		{9443, "tcp", 98, "caddy", "[2001:db8::1]:9443", true},
		{27015, "udp", 1500, "srcds", "0.0.0.0:27015", true},
		{27015, "tcp", 0, "", "", false},
		{7000, "tcp", 0, "", "0.0.0.0:7000", true},
		{80, "tcp", 0, "", "", false},
	}
	for _, tt := range tests {
		pid, name, addr, ok := parseSS(out, tt.port, tt.proto)
		require.Equal(t, tt.ok, ok, tt.port)
		require.Equal(t, tt.pid, pid, tt.port)
		require.Equal(t, tt.name, name, tt.port)
		require.Equal(t, tt.addr, addr, tt.port)
	}
	require.Equal(t, "myhost:80", normalizeSSAddr("myhost", 80))
	require.Equal(t, "0.0.0.0:80", normalizeSSAddr("", 80))
}
