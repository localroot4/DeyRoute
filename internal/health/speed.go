package health

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sort"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Built-in traffic generator of `deyroute diag speed` (section 14, phase 7).
//
// Protocol (one test per TCP connection): the client sends a 6-byte header
// "DEYS" | mode | seconds; the server answers one byte, the seconds it
// grants (the request capped by its own limit), then
//
//   - mode 'D' (download): the server streams pseudo-random bytes for the
//     granted seconds and closes the connection;
//   - mode 'U' (upload): the client streams pseudo-random bytes for the
//     granted seconds and half-closes; the server discards them and
//     answers 16 bytes: bytes received and receive duration in nanoseconds
//     (both big-endian uint64);
//   - mode 'P' (ping): each byte the client sends is echoed (at most
//     speedMaxPings bytes).
//
// The data is an AES-CTR keystream, so compressing transports cannot inflate
// the result.
const (
	speedMagic      = "DEYS"
	speedHeaderSize = len(speedMagic) + 2
	speedModeDown   = 'D'
	speedModeUp     = 'U'
	speedModePing   = 'P'
	speedMaxPings   = 64
	speedPings      = 5
	speedChunk      = 32 << 10
	// speedGrace is added to every deadline: time to drain buffers through
	// a slow tunnel and to exchange headers.
	speedGrace = 15 * time.Second
)

// Speed test limits.
const (
	// DefaultSpeedSeconds is the default duration of each direction.
	DefaultSpeedSeconds = 10
	// MaxSpeedSeconds caps the duration of each direction.
	MaxSpeedSeconds = 60
	// SpeedMaxConns bounds concurrent generator connections.
	SpeedMaxConns = 16
)

// SpeedResult is the outcome of MeasureSpeed.
type SpeedResult struct {
	// Seconds is the duration of each direction (the requested seconds,
	// capped by the server).
	Seconds       float64
	DownloadMbps  float64
	UploadMbps    float64
	DownloadBytes int64
	UploadBytes   int64
	// RTT is the median of a few 1-byte round trips.
	RTT time.Duration
}

// SpeedServer is the generator side. The zero value caps each test at
// DefaultSpeedSeconds and SpeedMaxConns connections.
type SpeedServer struct {
	// MaxSeconds caps the duration a client may request (1..MaxSpeedSeconds).
	MaxSeconds int
	// MaxConns bounds concurrent connections.
	MaxConns int
}

// ServeSpeed runs a SpeedServer that allows tests of up to seconds per
// direction. It takes ownership of ln, closes it and every open connection
// when it returns, and returns nil when ctx is done or ln was closed; the
// caller bounds the server's lifetime with ctx.
func ServeSpeed(ctx context.Context, ln net.Listener, seconds int) error {
	return SpeedServer{MaxSeconds: seconds}.Serve(ctx, ln)
}

// Serve accepts generator connections on ln until ctx is done.
func (s SpeedServer) Serve(ctx context.Context, ln net.Listener) error {
	maxSecs := clampSeconds(s.MaxSeconds)
	maxConns := s.MaxConns
	if maxConns <= 0 {
		maxConns = SpeedMaxConns
	}
	return serveConns(ctx, ln, "speed test generator", maxConns, func(_ context.Context, c net.Conn) {
		serveSpeedConn(c, maxSecs)
	})
}

// serveSpeedConn runs one test.
func serveSpeedConn(c net.Conn, maxSecs int) {
	_ = c.SetReadDeadline(time.Now().Add(speedGrace))
	hdr := make([]byte, speedHeaderSize)
	if _, err := io.ReadFull(c, hdr); err != nil || string(hdr[:len(speedMagic)]) != speedMagic {
		return
	}
	secs := int(hdr[len(speedMagic)+1])
	if secs < 1 {
		secs = 1
	}
	if secs > maxSecs {
		secs = maxSecs
	}
	dur := time.Duration(secs) * time.Second
	mode := hdr[len(speedMagic)]
	if mode != speedModeDown && mode != speedModeUp && mode != speedModePing {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(speedGrace))
	if _, err := c.Write([]byte{byte(secs)}); err != nil { //nolint:gosec // G115: secs is 1..MaxSpeedSeconds
		return
	}
	switch mode {
	case speedModeDown:
		_ = c.SetReadDeadline(time.Time{})
		stream, err := newRandStream()
		if err != nil {
			return
		}
		end := time.Now().Add(dur)
		_ = c.SetWriteDeadline(end)
		buf := make([]byte, speedChunk)
		for time.Now().Before(end) {
			stream.XORKeyStream(buf, buf)
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	case speedModeUp:
		_ = c.SetReadDeadline(time.Now().Add(dur + speedGrace))
		var total int64
		var first, last time.Time
		buf := make([]byte, speedChunk)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				now := time.Now()
				if first.IsZero() {
					first = now
				}
				last = now
				total += int64(n)
			}
			if err != nil {
				break
			}
		}
		var reply [16]byte
		binary.BigEndian.PutUint64(reply[:8], uint64(total))           //nolint:gosec // G115: total >= 0
		binary.BigEndian.PutUint64(reply[8:], uint64(last.Sub(first))) //nolint:gosec // G115: last >= first
		_ = c.SetWriteDeadline(time.Now().Add(speedGrace))
		_, _ = c.Write(reply[:])
	case speedModePing:
		one := make([]byte, 1)
		for i := 0; i < speedMaxPings; i++ {
			_ = c.SetDeadline(time.Now().Add(speedGrace))
			if _, err := io.ReadFull(c, one); err != nil {
				return
			}
			if _, err := c.Write(one); err != nil {
				return
			}
		}
	}
}

// MeasureSpeed runs a ping, a download and an upload test against a
// SpeedServer at addr (typically 127.0.0.1:<listen> on the hub, reaching the
// generator on the node through the tunnel), each direction for seconds
// (DefaultSpeedSeconds when <= 0, at most MaxSpeedSeconds). Failures are
// DEY-X052.
func MeasureSpeed(ctx context.Context, addr string, seconds int) (SpeedResult, error) {
	if seconds <= 0 {
		seconds = DefaultSpeedSeconds
	}
	seconds = clampSeconds(seconds)
	res := SpeedResult{Seconds: float64(seconds)}
	if !validAddr(addr) {
		return res, speedErr(addr, "ping", ReasonBadAddress)
	}
	rtt, err := speedPing(ctx, addr)
	if err != nil {
		return res, speedErr(addr, "ping", err.Error())
	}
	res.RTT = rtt
	n, d, granted, err := speedDownload(ctx, addr, seconds)
	if err != nil {
		return res, speedErr(addr, "download", err.Error())
	}
	res.Seconds = float64(granted)
	res.DownloadBytes, res.DownloadMbps = n, mbps(n, d)
	n, d, err = speedUpload(ctx, addr, seconds)
	if err != nil {
		return res, speedErr(addr, "upload", err.Error())
	}
	res.UploadBytes, res.UploadMbps = n, mbps(n, d)
	return res, nil
}

// speedDial connects, sends the header and reads the seconds the server
// grants; the connection's deadline is the earlier of ctx's and now+limit,
// and ctx cancellation interrupts it.
func speedDial(ctx context.Context, addr string, mode byte, secs int, limit time.Duration) (net.Conn, int, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	c, err := dialTCP(ctx, addr)
	if err != nil {
		reason := reasonFor(ctx, err)
		cancel()
		return nil, 0, nil, errors.New(reason)
	}
	setDeadlineFrom(ctx, c)
	stopWatch := interruptOnDone(ctx, c)
	done := func() {
		stopWatch()
		closeQuietly(c)
		cancel()
	}
	hdr := make([]byte, 0, speedHeaderSize)
	hdr = append(hdr, speedMagic...)
	hdr = append(hdr, mode, byte(secs)) //nolint:gosec // G115: secs is clamped to 1..60
	if _, err := c.Write(hdr); err != nil {
		reason := reasonFor(ctx, err)
		done()
		return nil, 0, nil, errors.New(reason)
	}
	var ack [1]byte
	if _, err := io.ReadFull(c, ack[:]); err != nil {
		reason := reasonFor(ctx, err)
		done()
		return nil, 0, nil, errors.New(reason)
	}
	granted := int(ack[0])
	if granted < 1 || granted > MaxSpeedSeconds {
		done()
		return nil, 0, nil, errors.New("not a speed test generator")
	}
	return c, granted, done, nil
}

// speedPing returns the median of speedPings 1-byte round trips.
func speedPing(ctx context.Context, addr string) (time.Duration, error) {
	c, _, done, err := speedDial(ctx, addr, speedModePing, 1, speedGrace)
	if err != nil {
		return 0, err
	}
	defer done()
	rtts := make([]time.Duration, 0, speedPings)
	one := []byte{0}
	for i := 0; i < speedPings; i++ {
		start := time.Now()
		if _, err := c.Write(one); err != nil {
			return 0, errors.New(reasonFor(ctx, err))
		}
		if _, err := io.ReadFull(c, one); err != nil {
			return 0, errors.New(reasonFor(ctx, err))
		}
		rtts = append(rtts, time.Since(start))
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	return rtts[len(rtts)/2], nil
}

// speedDownload receives for secs seconds; the duration runs from the first
// to the last byte received.
func speedDownload(ctx context.Context, addr string, secs int) (int64, time.Duration, int, error) {
	c, granted, done, err := speedDial(ctx, addr, speedModeDown, secs, time.Duration(secs)*time.Second+speedGrace)
	if err != nil {
		return 0, 0, 0, err
	}
	defer done()
	buf := make([]byte, speedChunk)
	var total int64
	var first, last time.Time
	for {
		n, err := c.Read(buf)
		if n > 0 {
			now := time.Now()
			if first.IsZero() {
				first = now
			}
			last = now
			total += int64(n)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return total, last.Sub(first), granted, errors.New(reasonFor(ctx, err))
		}
	}
	if total == 0 {
		return 0, 0, granted, errors.New("no data received")
	}
	return total, last.Sub(first), granted, nil
}

// speedUpload sends for secs seconds, half-closes and reads the server's
// count and receive duration.
func speedUpload(ctx context.Context, addr string, secs int) (int64, time.Duration, error) {
	c, granted, done, err := speedDial(ctx, addr, speedModeUp, secs, 2*time.Duration(secs)*time.Second+speedGrace)
	if err != nil {
		return 0, 0, err
	}
	defer done()
	dur := time.Duration(granted) * time.Second
	stream, err := newRandStream()
	if err != nil {
		return 0, 0, err
	}
	end := time.Now().Add(dur)
	if dl, ok := ctx.Deadline(); !ok || end.Before(dl) {
		_ = c.SetWriteDeadline(end)
	}
	buf := make([]byte, speedChunk)
	for time.Now().Before(end) {
		stream.XORKeyStream(buf, buf)
		if _, err := c.Write(buf); err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() && ctx.Err() == nil {
				break // the planned end of the upload
			}
			return 0, 0, errors.New(reasonFor(ctx, err))
		}
	}
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	var reply [16]byte
	if _, err := io.ReadFull(c, reply[:]); err != nil {
		return 0, 0, errors.New(reasonFor(ctx, err))
	}
	total := int64(binary.BigEndian.Uint64(reply[:8]))           //nolint:gosec // G115: set by the server from an int64
	recvDur := time.Duration(binary.BigEndian.Uint64(reply[8:])) //nolint:gosec // G115: set by the server from a Duration
	if total <= 0 {
		return 0, 0, errors.New("no data received by the generator")
	}
	return total, recvDur, nil
}

// mbps converts bytes over d to megabits per second.
func mbps(n int64, d time.Duration) float64 {
	if d <= 0 {
		d = time.Millisecond
	}
	return float64(n) * 8 / d.Seconds() / 1e6
}

// clampSeconds bounds a duration to 1..MaxSpeedSeconds (DefaultSpeedSeconds
// when zero or negative).
func clampSeconds(s int) int {
	switch {
	case s <= 0:
		return DefaultSpeedSeconds
	case s > MaxSpeedSeconds:
		return MaxSpeedSeconds
	}
	return s
}

// speedErr builds the DEY-X052 error of a failed phase.
func speedErr(addr, phase, reason string) error {
	return deyerr.New(deyerr.X052, deyerr.Params{"addr": addr, "phase": phase, "reason": reason})
}

// newRandStream returns an AES-256-CTR keystream with a random key.
func newRandStream() (cipher.Stream, error) {
	var key [32]byte
	var iv [aes.BlockSize]byte
	_, _ = rand.Read(key[:]) // never fails since Go 1.24
	_, _ = rand.Read(iv[:])
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewCTR(block, iv[:]), nil
}
