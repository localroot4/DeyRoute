package wsconn

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func noPings() Config { return Config{PingInterval: -1} }

func TestKeys(t *testing.T) {
	// The example of RFC 6455 section 1.3.
	require.Equal(t, "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=", AcceptKey("dGhlIHNhbXBsZSBub25jZQ=="))
	a, b := NewKey(), NewKey()
	require.NotEqual(t, a, b)
	require.Len(t, a, 24)
}

func TestRoundTripBothDirections(t *testing.T) {
	for _, size := range []int{1, 125, 126, 1000, 32 << 10, 32<<10 + 1, 100000, 300000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			cli, srv := connPair(t, noPings(), noPings())
			msg := make([]byte, size)
			pattern(msg, 7)

			for _, dir := range []struct{ from, to *Conn }{{cli, srv}, {srv, cli}} {
				werr := make(chan error, 1)
				go func() {
					n, err := dir.from.Write(msg)
					if err == nil && n != len(msg) {
						err = fmt.Errorf("short write %d", n)
					}
					werr <- err
				}()
				got := make([]byte, size)
				_, err := io.ReadFull(dir.to, got)
				require.NoError(t, err)
				require.Equal(t, msg, got)
				require.NoError(t, <-werr)
			}
		})
	}
}

func TestWriteFraming(t *testing.T) {
	for _, client := range []bool{true, false} {
		t.Run(fmt.Sprintf("client=%v", client), func(t *testing.T) {
			a, b := net.Pipe()
			cc := &countConn{Conn: a, sizes: make(chan int, 16)}
			c := New(cc, nil, Config{Client: client, PingInterval: -1})
			defer func() { _ = c.Close(); _ = b.Close() }()

			data := make([]byte, 100000)
			pattern(data, 0)
			go func() { _, _ = c.Write(data) }()

			var got []byte
			var lens []int
			for len(got) < len(data) {
				op, fin, masked, pl := readRawFrame(t, b)
				require.Equal(t, opBinary, op)
				require.True(t, fin, "frames are never fragmented")
				require.Equal(t, client, masked, "only a client masks")
				require.LessOrEqual(t, len(pl), 32<<10)
				lens = append(lens, len(pl))
				got = append(got, pl...)
			}
			require.Equal(t, data, got)
			require.Equal(t, []int{32768, 32768, 32768, 1696}, lens)
			// One write syscall per frame: the header and the payload are one buffer.
			require.EqualValues(t, 4, cc.writes.Load())
			for i, want := range lens {
				hl := 4 // 16-bit length form
				if client {
					hl += 4
				}
				require.Equal(t, want+hl, <-cc.sizes, "frame %d", i)
			}
		})
	}
}

func TestMaxFrameConfig(t *testing.T) {
	c, peer := rawPair(t, Config{MaxFrame: 10, PingInterval: -1})
	go func() { _, _ = c.Write(bytes.Repeat([]byte{'x'}, 25)) }()
	for _, want := range []int{10, 10, 5} {
		_, _, _, pl := readRawFrame(t, peer)
		require.Len(t, pl, want)
	}
	// Above the receive cap is clamped.
	require.Equal(t, MaxRecvFrame, New(&fakeConn{r: bytes.NewReader(nil)}, nil, Config{MaxFrame: 1 << 30, PingInterval: -1}).closeAfter(t).maxFrame)
}

// closeAfter closes the Conn at the end of the test.
func (c *Conn) closeAfter(t testing.TB) *Conn {
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestEmptyWrite(t *testing.T) {
	c, peer := rawPair(t, noPings())
	n, err := c.Write(nil)
	require.NoError(t, err)
	require.Zero(t, n)
	// Nothing was sent: the next frame is the one that follows.
	go func() { _, _ = c.Write([]byte("x")) }()
	_, _, _, pl := readRawFrame(t, peer)
	require.Equal(t, []byte("x"), pl)
	n, err = c.Read(nil)
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestReadLenient(t *testing.T) {
	key := [4]byte{0x37, 0xfa, 0x21, 0x3d}
	big := make([]byte, 70000)
	pattern(big, 3)
	mid := make([]byte, 300)
	pattern(mid, 9)

	cases := []struct {
		name   string
		frames []rawFrame
		want   []byte
	}{
		{"unmasked", []rawFrame{bin("hello")}, []byte("hello")},
		{"masked", []rawFrame{{fin: true, op: opBinary, payload: []byte("hello"), mask: true, key: key}}, []byte("hello")},
		{"7-bit boundary", []rawFrame{{fin: true, op: opBinary, payload: bytes.Repeat([]byte{1}, 125)}}, bytes.Repeat([]byte{1}, 125)},
		{"16-bit", []rawFrame{{fin: true, op: opBinary, payload: mid}}, mid},
		{"16-bit masked", []rawFrame{{fin: true, op: opBinary, payload: mid, mask: true, key: key}}, mid},
		{"64-bit real", []rawFrame{{fin: true, op: opBinary, payload: big}}, big},
		{"64-bit masked", []rawFrame{{fin: true, op: opBinary, payload: big, mask: true, key: key}}, big},
		{"64-bit form for a tiny payload", []rawFrame{{fin: true, op: opBinary, payload: []byte("tiny"), lenForm: 64}}, []byte("tiny")},
		{"16-bit form for a tiny payload", []rawFrame{{fin: true, op: opBinary, payload: []byte("tiny"), lenForm: 16, mask: true, key: key}}, []byte("tiny")},
		{"zero length frames", []rawFrame{
			{fin: true, op: opBinary}, bin("a"), {fin: true, op: opBinary}, {fin: true, op: opBinary, mask: true, key: key}, bin("b"),
		}, []byte("ab")},
		{"fragmented", []rawFrame{
			{op: opBinary, payload: []byte("one-")},
			{op: opContinuation, payload: []byte("two-"), mask: true, key: key},
			{op: opContinuation},
			{fin: true, op: opContinuation, payload: []byte("three")},
			bin("|next"),
		}, []byte("one-two-three|next")},
		{"control frames inside a message", []rawFrame{
			{op: opBinary, payload: []byte("ab")},
			{fin: true, op: opPing, payload: []byte("p1")},
			{op: opContinuation, payload: []byte("cd")},
			{fin: true, op: opPong, payload: []byte("unsolicited")},
			{fin: true, op: opPing},
			{fin: true, op: opContinuation, payload: []byte("ef")},
		}, []byte("abcdef")},
		{"masked ping and pong", []rawFrame{
			{fin: true, op: opPing, payload: []byte("hi"), mask: true, key: key},
			{fin: true, op: opPong, payload: []byte("yo"), mask: true, key: key},
			bin("data"),
		}, []byte("data")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, peer := rawPair(t, noPings())
			var stream []byte
			for _, f := range tc.frames {
				stream = append(stream, f.bytes()...)
			}
			// Any pong the Conn answers with has to be drained.
			go func() { _, _ = io.Copy(io.Discard, peer) }()
			werr := make(chan error, 1)
			go func() {
				_, err := peer.Write(stream)
				werr <- err
			}()
			got := make([]byte, len(tc.want))
			_, err := io.ReadFull(c, got)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.NoError(t, <-werr)
		})
	}
}

func TestReadSmallBuffersAndBufferedReader(t *testing.T) {
	// The bytes that arrived behind the HTTP upgrade sit in a bufio.Reader.
	f1 := rawFrame{fin: true, op: opBinary, payload: []byte("first-frame")}.bytes()
	f2 := rawFrame{fin: true, op: opBinary, payload: bytes.Repeat([]byte("0123456789"), 40), mask: true, key: [4]byte{1, 2, 3, 4}}.bytes()
	br := bufio.NewReader(bytes.NewReader(append(append([]byte("HTTP-leftover"), f1...), f2...)))
	_, err := br.Discard(len("HTTP-leftover"))
	require.NoError(t, err)
	// Make the bufio.Reader hold the frames (Peek fills it).
	_, err = br.Peek(len(f1) + len(f2))
	require.NoError(t, err)

	a, b := net.Pipe()
	c := New(a, br, noPings())
	defer func() { _ = c.Close(); _ = b.Close() }()

	// Tiny reads (1, 3, 7 bytes) must reassemble the payloads exactly.
	var got []byte
	sizes := []int{1, 3, 7}
	for i := 0; len(got) < len("first-frame")+400; i++ {
		buf := make([]byte, sizes[i%3])
		n, err := c.Read(buf)
		require.NoError(t, err)
		got = append(got, buf[:n]...)
	}
	require.Equal(t, "first-frame"+string(bytes.Repeat([]byte("0123456789"), 40)), string(got))

	// After the buffered bytes the raw connection takes over.
	go func() { _, _ = b.Write(bin("after").bytes()) }()
	buf := make([]byte, 5)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "after", string(buf))
}

func TestPingAnsweredWithoutBlockingRead(t *testing.T) {
	c, peer := rawPair(t, noPings())
	// The peer sends a ping and then data, and does NOT read the pong yet. On a
	// net.Pipe the pong write blocks until the peer reads, so a Read that wrote
	// the pong itself would hang.
	go func() {
		_, _ = peer.Write(rawFrame{fin: true, op: opPing, payload: []byte("are-you-there")}.bytes())
		_, _ = peer.Write(bin("payload").bytes())
	}()
	buf := make([]byte, 7)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(c, buf)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Read blocked behind the pong")
	}
	require.Equal(t, "payload", string(buf))

	op, fin, _, pl := readRawFrame(t, peer)
	require.Equal(t, opPong, op)
	require.True(t, fin)
	require.Equal(t, []byte("are-you-there"), pl)
}

func TestPingBothSidesWriting(t *testing.T) {
	// Both ends ping very often while both write bulk data and read it: no
	// deadlock on the synchronous pipe.
	cli, srv := connPair(t, Config{PingInterval: time.Millisecond}, Config{PingInterval: time.Millisecond})
	const total = 2 << 20
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, p := range []struct{ w, r *Conn }{{cli, srv}, {srv, cli}} {
		wg.Add(2)
		go func() {
			defer wg.Done()
			data := make([]byte, 7000)
			for sent := 0; sent < total; sent += len(data) {
				if _, err := p.w.Write(data); err != nil {
					errs <- err
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			_, err := io.CopyN(io.Discard, p.r, total/7000*7000+7000)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestKeepalivePingsAreSent(t *testing.T) {
	for _, client := range []bool{true, false} {
		c, peer := rawPair(t, Config{Client: client, PingInterval: 20 * time.Millisecond})
		seen := 0
		var last []byte
		for seen < 3 {
			op, fin, masked, pl := readRawFrame(t, peer)
			require.Equal(t, opPing, op)
			require.True(t, fin)
			require.Equal(t, client, masked)
			require.NotEqual(t, last, pl, "ping payloads differ")
			last = pl
			seen++
		}
		_ = c.Close()
	}
}

func TestCloseFrameEcho(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		code    int
		reason  string
		echo    []byte
	}{
		{"normal", append([]byte{0x03, 0xe8}, "bye"...), 1000, "bye", []byte{0x03, 0xe8}},
		{"going away", []byte{0x03, 0xe9}, 1001, "", []byte{0x03, 0xe9}},
		{"no code", nil, CloseNoStatus, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, peer := rawPair(t, noPings())
			go func() { _, _ = peer.Write(rawFrame{fin: true, op: opClose, payload: tc.payload}.bytes()) }()
			_, err := c.Read(make([]byte, 10))
			require.Error(t, err)
			require.ErrorIs(t, err, ErrClosed)
			require.NotErrorIs(t, err, io.EOF)
			var ce *CloseError
			require.ErrorAs(t, err, &ce)
			require.Equal(t, tc.code, ce.Code)
			require.Equal(t, tc.reason, ce.Reason)

			// The close frame is echoed, then the connection is dropped.
			op, fin, _, pl := readRawFrame(t, peer)
			require.Equal(t, opClose, op)
			require.True(t, fin)
			require.Equal(t, len(tc.echo), len(pl))
			require.Equal(t, tc.echo, append([]byte(nil), pl...)[:len(tc.echo)])
			_, err = peer.Read(make([]byte, 1))
			require.Error(t, err)

			// Sticky, and never io.EOF; writes fail with the same reason.
			_, err = c.Read(make([]byte, 10))
			require.ErrorIs(t, err, ErrClosed)
			_, err = c.Write([]byte("x"))
			require.ErrorIs(t, err, ErrClosed)
		})
	}
}

func TestDataBeforeCloseIsDelivered(t *testing.T) {
	c, peer := rawPair(t, noPings())
	stream := append(bin("last words").bytes(), rawFrame{fin: true, op: opClose, payload: []byte{0x03, 0xe8}}.bytes()...)
	go func() { _, _ = peer.Write(stream) }()
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	buf := make([]byte, 10)
	_, err := io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "last words", string(buf))
	_, err = c.Read(buf)
	require.ErrorIs(t, err, ErrClosed)
}

func TestConnectionLostWithoutCloseFrame(t *testing.T) {
	// A TCP end without a close frame is a truncation, never a clean EOF, at a
	// frame boundary and in the middle of a frame alike.
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"boundary", bin("abc").bytes()},
		{"nothing", nil},
		{"mid header", bin("abc").bytes()[:1]},
		{"mid payload", bin("abcdef").bytes()[:5]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, peer := rawPair(t, noPings())
			go func() {
				_, _ = peer.Write(tc.data)
				_ = peer.Close()
			}()
			var err error
			buf := make([]byte, 16)
			for err == nil {
				_, err = c.Read(buf)
			}
			require.NotErrorIs(t, err, io.EOF)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		})
	}
}

func TestCloseSendsCloseFrameAndIsIdempotent(t *testing.T) {
	c, peer := rawPair(t, noPings())
	done := make(chan struct{})
	go func() {
		defer close(done)
		op, _, _, pl := readRawFrame(t, peer)
		require.Equal(t, opClose, op)
		require.Equal(t, []byte{0x03, 0xe8}, pl)
	}()
	require.NoError(t, c.Close())
	<-done
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())

	_, err := c.Read(make([]byte, 1))
	require.ErrorIs(t, err, net.ErrClosed)
	require.NotErrorIs(t, err, ErrClosed)
	_, err = c.Write([]byte("x"))
	require.ErrorIs(t, err, net.ErrClosed)
}

func TestCloseConcurrentIsSafe(t *testing.T) {
	c, _ := rawPair(t, Config{PingInterval: time.Millisecond})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Close()
		}()
	}
	wg.Wait()
}

func TestCloseUnderWriteBackpressureDoesNotBlock(t *testing.T) {
	c, peer := rawPair(t, noPings())
	// The peer never reads: this Write blocks inside the raw conn, holding the
	// write lock.
	werr := make(chan error, 1)
	go func() {
		_, err := c.Write(make([]byte, 100000))
		werr <- err
	}()
	time.Sleep(50 * time.Millisecond)

	// A reader is blocked too.
	rerr := make(chan error, 1)
	go func() {
		_, err := c.Read(make([]byte, 10))
		rerr <- err
	}()
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a stalled Write")
	}
	require.Less(t, time.Since(start), time.Second)

	for _, ch := range []chan error{werr, rerr} {
		select {
		case err := <-ch:
			require.ErrorIs(t, err, net.ErrClosed)
		case <-time.After(2 * time.Second):
			t.Fatal("blocked call was not released by Close")
		}
	}
	require.NoError(t, c.Close())
	_ = peer
}

func TestCloseWithIdleWriteSideAndSilentPeerIsBounded(t *testing.T) {
	// The write lock is free but nobody reads on the pipe: the best-effort close
	// frame must not hold Close for long.
	c, _ := rawPair(t, noPings())
	start := time.Now()
	require.NoError(t, c.Close())
	require.Less(t, time.Since(start), time.Second)
}

func TestDeadlinesPassThrough(t *testing.T) {
	c, peer := rawPair(t, noPings())

	// Read: an expired deadline returns a timeout and the Conn stays usable.
	require.NoError(t, c.SetReadDeadline(time.Now().Add(-time.Second)))
	_, err := c.Read(make([]byte, 4))
	var ne net.Error
	require.ErrorAs(t, err, &ne)
	require.True(t, ne.Timeout())
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)

	require.NoError(t, c.SetDeadline(time.Time{}))
	go func() { _, _ = peer.Write(bin("ok").bytes()) }()
	buf := make([]byte, 2)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ok", string(buf))

	// Write: an expired deadline fails before any byte, so it is retryable.
	require.NoError(t, c.SetWriteDeadline(time.Now().Add(-time.Second)))
	_, err = c.Write([]byte("late"))
	require.ErrorAs(t, err, &ne)
	require.True(t, ne.Timeout())
	require.NoError(t, c.SetWriteDeadline(time.Time{}))
	go func() { _, _ = c.Write([]byte("retry")) }()
	_, _, _, pl := readRawFrame(t, peer)
	require.Equal(t, []byte("retry"), pl)
}

func TestReadStateSurvivesTimeouts(t *testing.T) {
	// The frame arrives in pieces, with a Read timeout between every piece:
	// inside the header, between header and payload and inside the payload.
	for _, masked := range []bool{false, true} {
		t.Run(fmt.Sprintf("masked=%v", masked), func(t *testing.T) {
			c, peer := rawPair(t, noPings())
			payload := make([]byte, 300)
			pattern(payload, 1)
			wire := rawFrame{fin: true, op: opBinary, payload: payload, mask: masked, key: [4]byte{9, 8, 7, 6}}.bytes()
			hdrLen := 4
			if masked {
				hdrLen = 8
			}
			cuts := []int{1, 3, hdrLen, hdrLen + 1, hdrLen + 100, hdrLen + 299, len(wire)}

			var got []byte
			prev := 0
			buf := make([]byte, 64)
			for _, cut := range cuts {
				go func(piece []byte) { _, _ = peer.Write(piece) }(wire[prev:cut])
				prev = cut
				// Drain what the piece makes available, then hit a timeout.
				for {
					require.NoError(t, c.SetReadDeadline(time.Now().Add(40*time.Millisecond)))
					n, err := c.Read(buf)
					got = append(got, buf[:n]...)
					if err != nil {
						var ne net.Error
						require.ErrorAs(t, err, &ne)
						require.True(t, ne.Timeout(), "unexpected error %v", err)
						break
					}
				}
			}
			require.Equal(t, payload, got)
			// And the stream continues in sync.
			require.NoError(t, c.SetReadDeadline(time.Time{}))
			go func() { _, _ = peer.Write(bin("next").bytes()) }()
			n, err := io.ReadFull(c, buf[:4])
			require.NoError(t, err)
			require.Equal(t, "next", string(buf[:n]))
		})
	}
}

func TestWriteInterruptedMidFrameIsPermanent(t *testing.T) {
	c, peer := rawPair(t, noPings())
	require.NoError(t, c.SetWriteDeadline(time.Now().Add(100*time.Millisecond)))
	werr := make(chan error, 1)
	go func() {
		_, err := c.Write(make([]byte, 1000))
		werr <- err
	}()
	// Let a part of the frame through, then the deadline fires.
	_, err := io.ReadFull(peer, make([]byte, 3))
	require.NoError(t, err)
	err = <-werr
	require.Error(t, err)

	// The stream is corrupt now: no further frame may follow.
	require.NoError(t, c.SetWriteDeadline(time.Time{}))
	_, err = c.Write([]byte("more"))
	require.Error(t, err)
	var ne net.Error
	require.False(t, errors.As(err, &ne), "a poisoned writer must not look retryable")
}

func TestProtocolErrors(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
		code int
	}{
		{"rsv1", rawFrame{fin: true, op: opBinary, rsv: 0x40, payload: []byte("x")}.bytes(), CloseProtocolError},
		{"rsv2", rawFrame{fin: true, op: opBinary, rsv: 0x20}.bytes(), CloseProtocolError},
		{"rsv3 on a control frame", rawFrame{fin: true, op: opPing, rsv: 0x10}.bytes(), CloseProtocolError},
		{"unknown data opcode 3", rawFrame{fin: true, op: 3}.bytes(), CloseProtocolError},
		{"unknown opcode 7", rawFrame{fin: true, op: 7}.bytes(), CloseProtocolError},
		{"unknown control opcode 0xB", rawFrame{fin: true, op: 0xb}.bytes(), CloseProtocolError},
		{"unknown control opcode 0xF", rawFrame{fin: true, op: 0xf}.bytes(), CloseProtocolError},
		{"text frame", rawFrame{fin: true, op: opText, payload: []byte("hi")}.bytes(), CloseUnsupported},
		{"text frame masked", rawFrame{fin: true, op: opText, payload: []byte("hi"), mask: true}.bytes(), CloseUnsupported},
		{"frame above 1 MiB", rawFrame{fin: true, op: opBinary, length: MaxRecvFrame + 1}.bytes(), CloseMessageTooBig},
		{"huge 64-bit length", rawFrame{fin: true, op: opBinary, length: 1 << 40}.bytes(), CloseMessageTooBig},
		{"64-bit length with the MSB set", append([]byte{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 1}, 'x'), CloseProtocolError},
		{"control frame above 125 bytes", rawFrame{fin: true, op: opPing, length: 126}.bytes(), CloseProtocolError},
		{"fragmented ping", rawFrame{op: opPing}.bytes(), CloseProtocolError},
		{"fragmented close", rawFrame{op: opClose}.bytes(), CloseProtocolError},
		{"continuation without a message", rawFrame{fin: true, op: opContinuation, payload: []byte("x")}.bytes(), CloseProtocolError},
		{"continuation after a complete message", append(bin("a").bytes(), rawFrame{fin: true, op: opContinuation}.bytes()...), CloseProtocolError},
		{"data frame inside a fragmented message", append(rawFrame{op: opBinary, payload: []byte("a")}.bytes(), bin("b").bytes()...), CloseProtocolError},
		{"text frame inside a fragmented message", append(rawFrame{op: opBinary, payload: []byte("a")}.bytes(), rawFrame{fin: true, op: opText}.bytes()...), CloseUnsupported},
		{"close with a one byte payload", rawFrame{fin: true, op: opClose, payload: []byte{3}}.bytes(), CloseProtocolError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, peer := rawPair(t, noPings())
			rerr := make(chan error, 1)
			go func() {
				buf := make([]byte, 64)
				for {
					if _, err := c.Read(buf); err != nil {
						rerr <- err
						return
					}
				}
			}()
			go func() { _, _ = peer.Write(tc.wire) }()

			// The close frame carries the code, then the connection is dropped.
			var closeFrame []byte
			for {
				op, _, _, pl := readRawFrame(t, peer)
				if op == opClose {
					closeFrame = pl
					break
				}
			}
			require.GreaterOrEqual(t, len(closeFrame), 2)
			require.Equal(t, tc.code, int(closeFrame[0])<<8|int(closeFrame[1]))

			err := <-rerr
			require.ErrorIs(t, err, ErrProtocol)
			require.NotErrorIs(t, err, io.EOF)
			require.NotErrorIs(t, err, ErrClosed)
			var pe *ProtocolError
			require.ErrorAs(t, err, &pe)
			require.Equal(t, tc.code, pe.Code)

			// Sticky.
			_, err = c.Read(make([]byte, 4))
			require.ErrorIs(t, err, ErrProtocol)
			_, err = c.Write([]byte("x"))
			require.Error(t, err)
		})
	}
}

func TestFrameAtTheReceiveCapIsAccepted(t *testing.T) {
	c, peer := rawPair(t, noPings())
	payload := make([]byte, MaxRecvFrame)
	pattern(payload, 5)
	go func() { _, _ = peer.Write(rawFrame{fin: true, op: opBinary, payload: payload}.bytes()) }()
	got := make([]byte, len(payload))
	_, err := io.ReadFull(c, got)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestAddrs(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()

	c := New(a, nil, Config{PingInterval: -1})
	defer func() { _ = c.Close() }()
	// net.Pipe addresses are not host:port: the adapter always returns one.
	for _, ad := range []net.Addr{c.RemoteAddr(), c.LocalAddr()} {
		_, _, err := net.SplitHostPort(ad.String())
		require.NoError(t, err, ad.String())
	}

	real := &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 4242}
	c2 := New(&fakeConn{r: bytes.NewReader(nil)}, nil, Config{PingInterval: -1, RemoteAddr: real, Via: "front"})
	defer func() { _ = c2.Close() }()
	require.Equal(t, real, c2.RemoteAddr())
	require.Equal(t, "front", c2.Via())
	require.Equal(t, "", c.Via())

	// A weird override is normalised too, and a nil raw address does not panic.
	c3 := New(&nilAddrConn{fakeConn{r: bytes.NewReader(nil)}}, nil, Config{PingInterval: -1, RemoteAddr: fixedAddr{"x", "no-port"}})
	defer func() { _ = c3.Close() }()
	_, _, err := net.SplitHostPort(c3.RemoteAddr().String())
	require.NoError(t, err)
	_, _, err = net.SplitHostPort(c3.LocalAddr().String())
	require.NoError(t, err)
}

type nilAddrConn struct{ fakeConn }

func (*nilAddrConn) LocalAddr() net.Addr  { return nil }
func (*nilAddrConn) RemoteAddr() net.Addr { return nil }

func TestConnIsHashable(t *testing.T) {
	c1, _ := rawPair(t, noPings())
	c2, _ := rawPair(t, noPings())
	m := map[net.Conn]int{c1: 1, c2: 2}
	require.Equal(t, 1, m[c1])
	require.Equal(t, 2, m[c2])
}

func TestMaskCopy(t *testing.T) {
	key := [4]byte{0xde, 0xad, 0xbe, 0xef}
	src := make([]byte, 53)
	pattern(src, 2)
	for pos := range 8 {
		for _, l := range []int{0, 1, 7, 8, 9, 31, 53} {
			want := make([]byte, l)
			for i := range want {
				want[i] = src[i] ^ key[(pos+i)&3]
			}
			got := make([]byte, l)
			maskCopy(got, src[:l], key, pos)
			require.Equal(t, want, got, "pos %d len %d", pos, l)
			inplace := bytes.Clone(src[:l])
			maskCopy(inplace, inplace, key, pos)
			require.Equal(t, want, inplace)
		}
	}
}

func TestParseHeaderForms(t *testing.T) {
	h, rsv, msb := parseHeader([]byte{0x82, 0x05})
	require.True(t, h.fin)
	require.False(t, rsv || msb)
	require.Equal(t, 5, h.length)
	h, _, _ = parseHeader([]byte{0x02, 0xfe, 0x01, 0x00, 1, 2, 3, 4})
	require.False(t, h.fin)
	require.True(t, h.masked)
	require.Equal(t, 256, h.length)
	require.Equal(t, [4]byte{1, 2, 3, 4}, h.key)
	_, _, msb = parseHeader([]byte{0x82, 127, 0x80, 0, 0, 0, 0, 0, 0, 0})
	require.True(t, msb)
	h, _, _ = parseHeader([]byte{0x82, 127, 0, 0, 0, 0, 0, 0x10, 0, 0})
	require.Equal(t, 1<<20, h.length)
}

func TestBufferPoolsAreReused(t *testing.T) {
	// An idle connection owns no read buffer, and a Write returns its frame
	// buffer: after steady-state traffic the allocations per round trip are
	// tiny and independent of the 32 KiB frame size.
	cli, srv := connPair(t, noPings(), noPings())
	data := make([]byte, 32<<10)
	got := make([]byte, len(data))
	round := func() {
		go func() { _, _ = cli.Write(data) }()
		_, _ = io.ReadFull(srv, got)
	}
	for range 10 {
		round()
	}
	srv.readMu.Lock()
	require.Nil(t, srv.rbuf, "an idle reader holds no buffer")
	srv.readMu.Unlock()
	allocs := testing.AllocsPerRun(50, round)
	require.Less(t, allocs, 20.0, "per-frame buffers must be pooled")
}
