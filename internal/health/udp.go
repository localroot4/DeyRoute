package health

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// MaxEchoPacket is the largest UDP packet the echo server answers. Replies
// are exactly as large as requests, so the echo cannot be abused as a
// traffic amplifier.
const MaxEchoPacket = 64

// EchoReplyMagic replaces EchoMagic in the replies of ServeUDPEcho. A server
// never answers a reply, so a spoofed packet cannot make two echo servers
// (two nodes, or a node and any other UDP echo service) bounce it back and
// forth forever.
const EchoReplyMagic = "DEYR"

// UDPEcho sends "DEYE"+nonce packets to a UDP echo server (ServeUDPEcho on
// the node's <ctl>/udp, section 10) and succeeds when one of them comes back
// with the same nonce: either unchanged (a plain echo) or with the magic
// replaced by EchoReplyMagic (ServeUDPEcho). tries (default UDPTries)
// packets are sent, each waiting timeout (default UDPTimeout) for its echo;
// a late echo of an earlier try still counts. RTT is measured from the send
// of the packet that came back.
func UDPEcho(ctx context.Context, addr string, tries int, timeout time.Duration) Result {
	if tries <= 0 {
		tries = UDPTries
	}
	if timeout <= 0 {
		timeout = UDPTimeout
	}
	if !validAddr(addr) {
		return Result{Err: ReasonBadAddress}
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return Result{Err: reasonFor(ctx, err)}
	}
	defer closeQuietly(conn)
	defer interruptOnDone(ctx, conn)()

	sent := make(map[[EchoPacketSize]byte]time.Time, tries)
	buf := make([]byte, MaxEchoPacket+1)
	reason := ""
	for i := 0; i < tries; i++ {
		if ctx.Err() != nil {
			return Result{Err: reasonFor(ctx, ctx.Err())}
		}
		pkt := newEchoPacket()
		sentAt := time.Now()
		tryEnd := sentAt.Add(timeout)
		sent[pkt] = sentAt
		if _, err := conn.Write(pkt[:]); err != nil {
			// Typically "connection refused" from an ICMP port unreachable
			// caused by the previous packet; pace the next try anyway.
			reason = Classify(err)
			waitUntil(ctx, tryEnd)
			continue
		}
		_ = conn.SetReadDeadline(tryEnd)
		if ctx.Err() != nil { // cancelled before the deadline was replaced
			return Result{Err: reasonFor(ctx, ctx.Err())}
		}
		for {
			n, err := conn.Read(buf)
			if err != nil {
				if ctx.Err() != nil {
					return Result{Err: reasonFor(ctx, ctx.Err())}
				}
				var ne net.Error
				if !errors.As(err, &ne) || !ne.Timeout() {
					reason = Classify(err)
					waitUntil(ctx, tryEnd)
				}
				break
			}
			if n != EchoPacketSize {
				continue
			}
			var k [EchoPacketSize]byte
			copy(k[:], buf[:n])
			if string(k[:len(EchoReplyMagic)]) == EchoReplyMagic {
				copy(k[:], EchoMagic)
			}
			if at, ok := sent[k]; ok {
				return Result{OK: true, RTT: time.Since(at)}
			}
		}
	}
	if reason == "" {
		reason = fmt.Sprintf("%s after %d tries", ReasonNoEcho, tries)
	}
	return Result{Err: reason}
}

// ServeUDPEcho answers UDP echo probes on conn until ctx is done: every
// packet of at most MaxEchoPacket bytes that starts with "DEYE" is sent back
// to its sender with the magic replaced by EchoReplyMagic ("DEYR") and the
// rest unchanged (same size: no amplification; never answered by another
// echo server: no loops); everything else is ignored. It takes ownership of
// conn and closes it when it returns. It returns nil when ctx is done or conn
// was closed, and DEY-X051 when reading fails otherwise.
func ServeUDPEcho(ctx context.Context, conn net.PacketConn) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer closeQuietly(conn)
	// Larger than MaxEchoPacket so oversized packets are detected instead
	// of being silently truncated to an acceptable size.
	buf := make([]byte, 2048)
	magic := []byte(EchoMagic)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if isTemporary(err) {
				continue
			}
			return deyerr.Wrap(deyerr.X051, err, deyerr.Params{"service": "udp echo", "addr": conn.LocalAddr().String()})
		}
		if n < len(magic) || n > MaxEchoPacket || !bytes.HasPrefix(buf[:n], magic) {
			continue
		}
		copy(buf, EchoReplyMagic)
		// A failed reply (unreachable sender) is the sender's problem.
		_, _ = conn.WriteTo(buf[:n], from)
	}
}

// waitUntil sleeps until t or until ctx is done.
func waitUntil(ctx context.Context, t time.Time) {
	d := time.Until(t)
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
