package health

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// udpEchoServer runs ServeUDPEcho until test cleanup.
func udpEchoServer(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeUDPEcho(ctx, pc) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return pc.LocalAddr().String()
}

func TestUDPEcho(t *testing.T) {
	addr := udpEchoServer(t)
	r := UDPEcho(context.Background(), addr, 0, 0)
	require.True(t, r.OK, r.Err)
	assert.Positive(t, r.RTT)
}

func TestServeUDPEchoFiltersPackets(t *testing.T) {
	addr := udpEchoServer(t)
	c, err := net.Dial("udp", addr)
	require.NoError(t, err)
	defer c.Close()

	expectReply := func(pkt []byte, want bool) {
		t.Helper()
		_, err := c.Write(pkt)
		require.NoError(t, err)
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		buf := make([]byte, 256)
		n, err := c.Read(buf)
		if !want {
			assert.Error(t, err, "no reply expected for %q", pkt)
			return
		}
		require.NoError(t, err)
		assert.Equal(t, pkt, buf[:n], "reply must equal the request")
	}
	expectReply([]byte("DEYE"), true)
	expectReply([]byte("DEYE"+strings.Repeat("x", MaxEchoPacket-4)), true)
	expectReply([]byte("DEYE"+strings.Repeat("x", MaxEchoPacket-3)), false) // 65 bytes
	expectReply([]byte("HELLO WORLD"), false)
	expectReply([]byte("DEY"), false)
}

func TestUDPEchoSilentPeer(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close()
	start := time.Now()
	r := UDPEcho(context.Background(), pc.LocalAddr().String(), 2, 100*time.Millisecond)
	assert.False(t, r.OK)
	assert.Equal(t, "no echo after 2 tries", r.Err)
	assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
}

func TestUDPEchoClosedPort(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := pc.LocalAddr().String()
	require.NoError(t, pc.Close())
	start := time.Now()
	r := UDPEcho(context.Background(), addr, 2, 100*time.Millisecond)
	assert.False(t, r.OK)
	assert.Contains(t, []string{ReasonRefused, "no echo after 2 tries"}, r.Err)
	assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond, "tries are paced even when refused")
}

func TestUDPEchoLateReplyCounts(t *testing.T) {
	// A server that answers each packet after 150 ms: with a 100 ms timeout
	// the echo of try 1 arrives during try 2 and still proves the path.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 128)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pkt := append([]byte(nil), buf[:n]...)
			time.Sleep(150 * time.Millisecond)
			_, _ = pc.WriteTo(pkt, from)
		}
	}()
	defer func() {
		_ = pc.Close()
		wg.Wait()
	}()
	r := UDPEcho(context.Background(), pc.LocalAddr().String(), 3, 100*time.Millisecond)
	require.True(t, r.OK, r.Err)
	assert.GreaterOrEqual(t, r.RTT, 150*time.Millisecond)
}

func TestUDPEchoCanceled(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	r := UDPEcho(ctx, pc.LocalAddr().String(), 3, 2*time.Second)
	assert.Equal(t, ReasonCanceled, r.Err)
	assert.Less(t, time.Since(start), time.Second)

	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r = UDPEcho(ctx, pc.LocalAddr().String(), 3, 2*time.Second)
	assert.Equal(t, ReasonTimeout, r.Err)

	done, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	r = UDPEcho(done, pc.LocalAddr().String(), 3, time.Second)
	assert.Equal(t, ReasonCanceled, r.Err)
}

func TestServeUDPEchoStops(t *testing.T) {
	// Closed by the caller: a normal stop.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- ServeUDPEcho(context.Background(), pc) }()
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, pc.Close())
	require.NoError(t, <-done)

	// A read error that is neither a stop nor temporary is DEY-X051.
	pc2, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, pc2.SetReadDeadline(time.Now().Add(-time.Second)))
	err = ServeUDPEcho(context.Background(), pc2)
	require.Error(t, err)
	assert.True(t, deyerr.HasCode(err, deyerr.X051), err.Error())
}

func TestWaitUntil(t *testing.T) {
	start := time.Now()
	waitUntil(context.Background(), start.Add(-time.Second))
	assert.Less(t, time.Since(start), 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitUntil(ctx, time.Now().Add(time.Hour))
	assert.Less(t, time.Since(start), time.Second)
}
