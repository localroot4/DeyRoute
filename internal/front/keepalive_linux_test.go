//go:build linux

package front

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKeepAliveIsSetOnTheRawSocket: the TCP keepalive of the node-to-edge flow
// keeps NAT and conntrack entries alive between the 30 s WebSocket pings.
func TestKeepAliveIsSetOnTheRawSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(300 * time.Millisecond)
		}
	}()
	d := &Dialer{}
	conn, err := d.dialTCP(context.Background(), Target{Host: "127.0.0.1", Port: portOf(ln.Addr()), Secret: testSecret})
	require.NoError(t, err)
	defer conn.Close()
	tc, ok := conn.(*net.TCPConn)
	require.True(t, ok)
	rc, err := tc.SyscallConn()
	require.NoError(t, err)
	var keep, idle int
	var gerr error
	require.NoError(t, rc.Control(func(fd uintptr) {
		keep, gerr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE)
		if gerr == nil {
			idle, gerr = syscall.GetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE)
		}
	}))
	require.NoError(t, gerr)
	assert.Equal(t, 1, keep, "SO_KEEPALIVE")
	assert.Equal(t, 30, idle, "TCP_KEEPIDLE seconds")
}
