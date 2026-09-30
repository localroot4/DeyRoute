package health

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// speedServer runs ServeSpeed until cleanup.
func speedServer(t *testing.T, seconds int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeSpeed(ctx, ln, seconds) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return ln.Addr().String()
}

func TestMeasureSpeed(t *testing.T) {
	addr := speedServer(t, 1)
	start := time.Now()
	res, err := MeasureSpeed(context.Background(), addr, 1)
	require.NoError(t, err)
	assert.Equal(t, 1.0, res.Seconds)
	assert.Positive(t, res.DownloadBytes)
	assert.Positive(t, res.UploadBytes)
	assert.Positive(t, res.DownloadMbps)
	assert.Positive(t, res.UploadMbps)
	assert.Positive(t, res.RTT)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestSpeedServerClampsSeconds(t *testing.T) {
	addr := speedServer(t, 1)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte{'D', 'E', 'Y', 'S', 'D', 30}) // asks for 30 s
	require.NoError(t, err)
	start := time.Now()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, err := io.Copy(io.Discard, c)
	require.NoError(t, err)
	assert.Positive(t, n)
	assert.Less(t, time.Since(start), 5*time.Second, "capped at the server's 1 s")
}

func TestSpeedServerUploadReply(t *testing.T) {
	addr := speedServer(t, 1)
	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Write([]byte{'D', 'E', 'Y', 'S', 'U', 1})
	require.NoError(t, err)
	_, err = c.Write(make([]byte, 1000))
	require.NoError(t, err)
	require.NoError(t, c.(*net.TCPConn).CloseWrite())
	var reply [16]byte
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err = io.ReadFull(c, reply[:])
	require.NoError(t, err)
	assert.Equal(t, uint64(1000), binary.BigEndian.Uint64(reply[:8]))
}

func TestSpeedServerRejectsGarbage(t *testing.T) {
	addr := speedServer(t, 1)
	for _, hdr := range [][]byte{[]byte("GET / "), {'D', 'E', 'Y', 'S', 'X', 1}} {
		c, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		_, err = c.Write(hdr)
		require.NoError(t, err)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := io.Copy(io.Discard, c)
		assert.NoError(t, err)
		assert.Zero(t, n)
		_ = c.Close()
	}
}

func TestMeasureSpeedErrors(t *testing.T) {
	_, err := MeasureSpeed(context.Background(), closedPort(t), 1)
	require.Error(t, err)
	e := deyerr.As(err)
	assert.Equal(t, deyerr.X052, e.Code)
	assert.Contains(t, e.Message(), "ping")
	assert.Equal(t, ReasonRefused, e.Why())

	_, err = MeasureSpeed(context.Background(), "nope", 0)
	assert.True(t, deyerr.HasCode(err, deyerr.X052))

	// A peer that answers pings but sends no data.
	pingOnly := testServer(t, func(c net.Conn) {
		defer c.Close()
		hdr := make([]byte, speedHeaderSize)
		if _, err := io.ReadFull(c, hdr); err != nil || hdr[4] != speedModePing {
			return
		}
		one := make([]byte, 1)
		for {
			if _, err := io.ReadFull(c, one); err != nil {
				return
			}
			if _, err := c.Write(one); err != nil {
				return
			}
		}
	})
	_, err = MeasureSpeed(context.Background(), pingOnly, 1)
	require.Error(t, err)
	e = deyerr.As(err)
	assert.Equal(t, deyerr.X052, e.Code)
	assert.Contains(t, e.Message(), "download")
	assert.Equal(t, "no data received", e.Why())

	// Upload against a peer that never answers the count: bounded by ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, _, err = speedUpload(ctx, blackholeServer(t), 1)
	require.Error(t, err)
	assert.Equal(t, ReasonTimeout, err.Error())
}

func TestMeasureSpeedCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := MeasureSpeed(ctx, speedServer(t, 1), 1)
	require.Error(t, err)
	assert.Equal(t, ReasonCanceled, deyerr.As(err).Why())
}

func TestSpeedHelpers(t *testing.T) {
	assert.Equal(t, DefaultSpeedSeconds, clampSeconds(0))
	assert.Equal(t, DefaultSpeedSeconds, clampSeconds(-3))
	assert.Equal(t, 5, clampSeconds(5))
	assert.Equal(t, MaxSpeedSeconds, clampSeconds(1000))
	assert.InDelta(t, 8.0, mbps(1_000_000, time.Second), 1e-9)
	assert.Positive(t, mbps(1000, 0))
	s, err := newRandStream()
	require.NoError(t, err)
	a := make([]byte, 64)
	b := make([]byte, 64)
	s.XORKeyStream(a, a)
	s.XORKeyStream(b, b)
	assert.NotEqual(t, a, b)
}
