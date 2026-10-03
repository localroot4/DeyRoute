package wsconn

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	idleTO   = 300 * time.Millisecond
	idlePing = 20 * time.Millisecond
)

func idleCfg() Config { return Config{PingInterval: idlePing, IdleTimeout: idleTO} }

// startReader runs Read in the background until it fails.
func startReader(c *Conn) <-chan error {
	ch := make(chan error, 1)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := c.Read(buf); err != nil {
				ch <- err
				return
			}
		}
	}()
	return ch
}

func requireNoResult(t *testing.T, ch <-chan error, d time.Duration, msg string) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("%s: unexpected result %v", msg, err)
	case <-time.After(d):
	}
}

func TestIdleTimeoutKillsSilentPeer(t *testing.T) {
	c, peer := rawPair(t, idleCfg())
	// The peer swallows our pings and never says anything.
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	rerr := startReader(c)
	start := time.Now()
	select {
	case err := <-rerr:
		require.ErrorIs(t, err, ErrIdleTimeout)
		require.NotErrorIs(t, err, io.EOF)
		require.NotErrorIs(t, err, ErrClosed)
		require.GreaterOrEqual(t, time.Since(start), idleTO-idlePing, "killed before the timeout")
		require.Less(t, time.Since(start), 5*time.Second)
	case <-time.After(10 * time.Second):
		t.Fatal("silent peer was not cut")
	}
	_, err := c.Write([]byte("x"))
	require.ErrorIs(t, err, ErrIdleTimeout)
	require.NoError(t, c.Close())
}

func TestIdleTimeoutPongsKeepConnectionAlive(t *testing.T) {
	c, peer := rawPair(t, idleCfg())
	rerr := startReader(c)
	// The peer answers every ping with a pong (no data at all).
	go func() {
		for {
			var h [2]byte
			if _, err := io.ReadFull(peer, h[:]); err != nil {
				return
			}
			pl := make([]byte, h[1]&0x7f)
			if _, err := io.ReadFull(peer, pl); err != nil {
				return
			}
			if h[0]&0x0f == opPing {
				if _, err := peer.Write(rawFrame{fin: true, op: opPong, payload: pl}.bytes()); err != nil {
					return
				}
			}
		}
	}()
	requireNoResult(t, rerr, 4*idleTO, "a peer that pongs was cut")
}

func TestIdleTimeoutAnyFrameCounts(t *testing.T) {
	// Data frames alone (no pong) keep the connection alive: "a frame of ANY
	// kind", so it does not depend on the CDN forwarding pings.
	c, peer := rawPair(t, idleCfg())
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if _, err := peer.Write(bin("beat").bytes()); err != nil {
					return
				}
			}
		}
	}()
	rerr := startReader(c)
	requireNoResult(t, rerr, 4*idleTO, "a peer that sends data was cut")
}

func TestIdleTimeoutNeedsAPingWritten(t *testing.T) {
	// Without pings nothing can be unanswered: IdleTimeout alone never kills.
	c, peer := rawPair(t, Config{PingInterval: -1, IdleTimeout: 50 * time.Millisecond})
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	rerr := startReader(c)
	requireNoResult(t, rerr, 600*time.Millisecond, "killed although no ping was ever written")
}

func TestIdleTimeoutOffByDefault(t *testing.T) {
	c, peer := rawPair(t, Config{PingInterval: idlePing})
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	rerr := startReader(c)
	requireNoResult(t, rerr, 3*idleTO, "IdleTimeout 0 must never kill")
}

func TestIdleTimeoutSlowReaderIsNotKilled(t *testing.T) {
	c, peer := rawPair(t, idleCfg())
	go func() { _, _ = io.Copy(io.Discard, peer) }() // takes our pings, answers nothing

	// Nobody reads from c for 3 timeouts. Its pings went out, the peer's answer
	// (if any) would sit in the kernel buffer; the clock must not run.
	time.Sleep(3 * idleTO)

	// The reader comes back; the peer's data arrives shortly after. Counting the
	// time the reader was away would have killed the connection by now.
	rerr := make(chan error, 1)
	got := make([]byte, 4)
	go func() {
		_, err := io.ReadFull(c, got)
		rerr <- err
	}()
	time.Sleep(idleTO / 3)
	_, err := peer.Write(bin("late").bytes())
	require.NoError(t, err)
	select {
	case err := <-rerr:
		require.NoError(t, err, "a slow reader was killed")
		require.Equal(t, "late", string(got))
	case <-time.After(5 * time.Second):
		t.Fatal("no data")
	}

	// Once it reads again and the peer stays silent, the watchdog does work.
	rr := startReader(c)
	select {
	case err := <-rr:
		require.ErrorIs(t, err, ErrIdleTimeout)
	case <-time.After(10 * time.Second):
		t.Fatal("silent peer was not cut after the reader came back")
	}
}

func TestIdleTimeoutReadTimeoutLoopStillKills(t *testing.T) {
	// A reader that polls with short deadlines (crypto/tls style) is "reading":
	// the gaps between its calls must not keep the connection alive forever.
	c, peer := rawPair(t, idleCfg())
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	deadline := time.Now().Add(10 * time.Second)
	buf := make([]byte, 8)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		_, err := c.Read(buf)
		var ne net.Error
		if err == nil {
			t.Fatal("unexpected data")
		}
		if !errors.As(err, &ne) || !ne.Timeout() {
			require.ErrorIs(t, err, ErrIdleTimeout)
			return
		}
	}
	t.Fatal("a polling reader of a silent peer was never cut")
}

func TestIdleTimeoutBlockedWriteIsNotCounted(t *testing.T) {
	// No automatic pings: the test sends exactly one, so the connection
	// goroutine (which runs the watchdog) is never stuck behind a second ping.
	c, peer := rawPair(t, Config{PingInterval: -1, IdleTimeout: idleTO})
	rerr := startReader(c)

	// The peer takes exactly that ping and then stops reading: from now on a
	// Write of ours blocks (backpressure).
	go c.sendPing()
	op, _, _, _ := readRawFrame(t, peer)
	require.Equal(t, opPing, op)
	time.Sleep(20 * time.Millisecond) // the ping write has completed

	werr := make(chan error, 1)
	go func() {
		_, err := c.Write(make([]byte, 100000))
		werr <- err
	}()

	// Well past the idle timeout: the ping was written, no frame came, but a
	// blocked write holds the clock.
	requireNoResult(t, rerr, 3*idleTO, "killed while a write was blocked")
	requireNoResult(t, werr, 10*time.Millisecond, "write should still be blocked")

	// The peer wakes up: drains and answers with a pong.
	go func() { _, _ = io.Copy(io.Discard, peer) }()
	_, err := peer.Write(rawFrame{fin: true, op: opPong}.bytes())
	require.NoError(t, err)
	select {
	case err := <-werr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("write did not complete")
	}
	requireNoResult(t, rerr, idleTO/2, "killed right after the write unblocked")
}

func TestIdleTrackerAccounting(t *testing.T) {
	tr := newIdleTracker(100 * time.Millisecond)
	require.False(t, tr.exceeded())

	// No ping: never exceeded, whatever the other state.
	tr.readEnter()
	time.Sleep(150 * time.Millisecond)
	require.False(t, tr.exceeded())

	// Ping written, reader active: the clock runs.
	tr.pingWritten()
	time.Sleep(60 * time.Millisecond)
	require.False(t, tr.exceeded())
	// A write in progress pauses it.
	tr.writeStart()
	time.Sleep(150 * time.Millisecond)
	require.False(t, tr.exceeded())
	tr.writeEnd()
	// So does a reader that is away.
	tr.readExit()
	time.Sleep(150 * time.Millisecond)
	require.False(t, tr.exceeded())
	tr.readEnter()
	time.Sleep(60 * time.Millisecond)
	require.True(t, tr.exceeded(), "60+60 ms of counted time")

	// Any arrival resets everything.
	tr.arrived()
	time.Sleep(150 * time.Millisecond)
	require.False(t, tr.exceeded())

	// A second ping does not restart the clock of an unanswered first one.
	tr.pingWritten()
	time.Sleep(70 * time.Millisecond)
	tr.pingWritten()
	time.Sleep(50 * time.Millisecond)
	require.True(t, tr.exceeded())

	// A nil tracker is inert.
	var nilTr *idleTracker
	nilTr.readEnter()
	nilTr.pingWritten()
	nilTr.arrived()
	require.False(t, nilTr.exceeded())
}
