package wsconn

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Config tunes a Conn.
type Config struct {
	Client       bool          // mask outgoing frames
	PingInterval time.Duration // 0 = 30 s, <0 = no pings
	IdleTimeout  time.Duration // 0 = off; counts only time with a ping actually written and no frame of ANY kind received
	MaxFrame     int           // outgoing payload per frame, 0 = 32 KiB (values above MaxRecvFrame are clamped)
	RemoteAddr   net.Addr      // overrides RemoteAddr() (the hub passes the real client address)
	Via          string        // returned by Via(), "" if not set
}

// DefaultPingInterval is the keepalive ping period (well below the ~100 s idle
// window of the CDN).
const DefaultPingInterval = 30 * time.Second

// closeWait bounds the best-effort close frame of Close and of the echo of a
// received close: it is sent only if the write side is free and the write has
// this long to complete.
const closeWait = 250 * time.Millisecond

// Conn is a net.Conn over an upgraded WebSocket connection (see the package
// documentation). Read and Write may be used concurrently with each other and
// with Close; concurrent Reads (or Writes) are serialised.
type Conn struct {
	raw       net.Conn
	client    bool
	maxFrame  int
	remote    net.Addr
	via       string
	pingEvery time.Duration
	idle      *idleTracker
	fpool     *sync.Pool

	// Read side, guarded by readMu.
	readMu   sync.Mutex
	pre      *bufio.Reader // bytes read after the HTTP upgrade, until drained
	rbuf     *[]byte       // pooled read buffer, owned only while it holds data or a read waits
	rr, rn   int
	deferred error // a raw read error that came with data, delivered after it
	hdr      [maxHeader]byte
	hdrN     int
	active   bool // a frame header was parsed and its payload is not fully consumed
	f        frameHeader
	remain   int  // payload bytes of the current frame not yet read
	pos      int  // payload offset of the next byte (for the mask rotation)
	inMsg    bool // inside a fragmented data message
	ctl      [maxControlPayload]byte
	ctlN     int
	rerr     error // sticky terminal read error

	// Write side. dataMu orders whole Write calls, frameMu orders frames (and
	// lets control frames slip in between the frames of a large Write).
	dataMu    sync.Mutex
	frameMu   sync.Mutex
	werr      error // sticky: a frame was half written, the stream is corrupt
	closeSent bool  // a close frame went out; nothing may follow it
	pingSeq   uint64

	// Hand-off to the connection goroutine: a pong or a close echo that must
	// not be written from the reader.
	pendMu  sync.Mutex
	pong    [maxControlPayload]byte
	pongN   int
	hasPong bool
	echo    int // close code to echo, <0 none queued, 0 echo without a code
	wake    chan struct{}

	// Shutdown.
	errMu    sync.Mutex
	closeErr error // first reason the connection ended; what Read and Write report afterwards
	closing  bool
	rawOnce  sync.Once
	done     chan struct{}
}

// New wraps raw, on which the HTTP upgrade has just completed, as a net.Conn.
// buffered is the reader that parsed the upgrade message and may still hold
// frame bytes the peer sent right behind it (nil if none). New starts one
// goroutine (pongs, pings, idle watchdog); it ends when the connection is
// closed or ends, so the caller must always Close the Conn.
func New(raw net.Conn, buffered *bufio.Reader, cfg Config) *Conn {
	c := &Conn{
		raw:       raw,
		client:    cfg.Client,
		maxFrame:  cfg.MaxFrame,
		remote:    cfg.RemoteAddr,
		via:       cfg.Via,
		pingEvery: cfg.PingInterval,
		idle:      newIdleTracker(cfg.IdleTimeout),
		pre:       buffered,
		echo:      -1,
		wake:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	if c.maxFrame <= 0 {
		c.maxFrame = DefaultMaxFrame
	}
	if c.maxFrame > MaxRecvFrame {
		c.maxFrame = MaxRecvFrame
	}
	if c.maxFrame == DefaultMaxFrame {
		c.fpool = &framePool
	} else {
		size := c.maxFrame + maxHeader
		c.fpool = &sync.Pool{New: func() any {
			b := make([]byte, size)
			return &b
		}}
	}
	if c.pingEvery == 0 {
		c.pingEvery = DefaultPingInterval
	}
	go c.loop()
	return c
}

// Via returns Config.Via.
func (c *Conn) Via() string { return c.via }

// LocalAddr returns the local address of the raw connection.
func (c *Conn) LocalAddr() net.Addr { return hostPort(c.raw.LocalAddr()) }

// RemoteAddr returns Config.RemoteAddr if set, else the raw peer address. The
// result always stringifies to host:port (HTTP/2 and the hub parse it).
func (c *Conn) RemoteAddr() net.Addr {
	if c.remote != nil {
		return hostPort(c.remote)
	}
	return hostPort(c.raw.RemoteAddr())
}

// SetDeadline passes straight through to the raw connection.
func (c *Conn) SetDeadline(t time.Time) error { return c.raw.SetDeadline(t) }

// SetReadDeadline passes straight through to the raw connection.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.raw.SetReadDeadline(t) }

// SetWriteDeadline passes straight through to the raw connection.
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }

// fixedAddr is a net.Addr with a fixed string.
type fixedAddr struct{ network, s string }

func (a fixedAddr) Network() string { return a.network }
func (a fixedAddr) String() string  { return a.s }

// hostPort returns a unless its string is not a host:port (nil, net.Pipe, a
// unix path), in which case it returns a placeholder that is.
func hostPort(a net.Addr) net.Addr {
	if a == nil {
		return fixedAddr{"tcp", "unknown:0"}
	}
	if _, _, err := net.SplitHostPort(a.String()); err == nil {
		return a
	}
	return fixedAddr{a.Network(), "unknown:0"}
}

// ---------------------------------------------------------------------------
// Shutdown

// setCloseErr records why the connection ended; the first reason wins.
func (c *Conn) setCloseErr(err error) {
	c.errMu.Lock()
	if c.closeErr == nil {
		c.closeErr = err
	}
	c.errMu.Unlock()
}

func (c *Conn) getCloseErr() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.closeErr
}

// terminate closes the raw connection and stops the goroutine; it reports
// whether this call did it, and the raw Close error then.
func (c *Conn) terminate() (first bool, err error) {
	c.rawOnce.Do(func() {
		first = true
		close(c.done)
		err = c.raw.Close()
	})
	return first, err
}

// Close ends the connection. It is idempotent and never blocks: the close frame
// (code 1000) is sent only if the write side is free, with a short deadline,
// and the raw connection is closed right after, which also unblocks any Read
// or Write in flight (they return an error wrapping net.ErrClosed).
func (c *Conn) Close() error {
	c.errMu.Lock()
	if c.closing {
		c.errMu.Unlock()
		return nil
	}
	c.closing = true
	if c.closeErr == nil {
		c.closeErr = errLocalClose
	}
	c.errMu.Unlock()
	c.sendClose(CloseNormal, false)
	first, err := c.terminate()
	if !first {
		return nil
	}
	return err
}

// sendClose writes a close frame once. With block false it gives up at once if
// another frame is being written; code < 0 sends no code.
func (c *Conn) sendClose(code int, block bool) {
	if block {
		c.frameMu.Lock()
	} else if !c.frameMu.TryLock() {
		return
	}
	defer c.frameMu.Unlock()
	if c.closeSent || c.werr != nil {
		return
	}
	c.closeSent = true
	var payload []byte
	var pb [2]byte
	if code >= 0 {
		binary.BigEndian.PutUint16(pb[:], uint16(code)) // #nosec G115 -- close codes are 4 digits
		payload = pb[:]
	}
	var buf [maxHeader + 2]byte
	n := c.buildFrame(buf[:], opClose, payload)
	_ = c.raw.SetWriteDeadline(time.Now().Add(closeWait))
	_ = c.writeRaw(buf[:n])
}

// ---------------------------------------------------------------------------
// Connection goroutine

// loop owns everything that must not run on the caller's goroutines: pongs and
// close echoes (so Read never blocks on a stalled writer), the keepalive pings
// and the idle watchdog.
func (c *Conn) loop() {
	var pingC, idleC <-chan time.Time
	if c.pingEvery > 0 {
		t := time.NewTicker(c.pingEvery)
		defer t.Stop()
		pingC = t.C
	}
	if c.idle != nil {
		p := min(max(c.idle.timeout/10, 5*time.Millisecond), time.Second)
		t := time.NewTicker(p)
		defer t.Stop()
		idleC = t.C
	}
	for {
		select {
		case <-c.done:
			return
		case <-c.wake:
			c.flush()
		case <-pingC:
			c.sendPing()
		case <-idleC:
			if c.idle.exceeded() {
				c.setCloseErr(errIdle)
				_, _ = c.terminate()
				return
			}
		}
	}
}

// flush writes the stashed pong and close echo.
func (c *Conn) flush() {
	c.pendMu.Lock()
	var pong []byte
	if c.hasPong {
		pong = append([]byte(nil), c.pong[:c.pongN]...)
		c.hasPong = false
	}
	echo := c.echo
	c.echo = -1
	c.pendMu.Unlock()
	if pong != nil {
		_ = c.writeControl(opPong, pong)
	}
	if echo >= 0 {
		if echo == 0 {
			echo = -1 // the peer's close had no code: echo without one
		}
		c.sendClose(echo, true)
		_, _ = c.terminate()
	}
}

func (c *Conn) kick() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Conn) sendPing() {
	c.pingSeq++
	var pl [8]byte
	binary.BigEndian.PutUint64(pl[:], c.pingSeq)
	if c.writeControl(opPing, pl[:]) == nil {
		c.idle.pingWritten()
	}
}

// writeControl writes one control frame (payload <= 125 bytes).
func (c *Conn) writeControl(op byte, payload []byte) error {
	var buf [maxHeader + maxControlPayload]byte
	n := c.buildFrame(buf[:], op, payload)
	c.frameMu.Lock()
	defer c.frameMu.Unlock()
	if err := c.writeBlocked(); err != nil {
		return err
	}
	return c.writeRaw(buf[:n])
}

// ---------------------------------------------------------------------------
// Write

// buildFrame encodes one FIN frame into buf (header + payload) and returns its
// length. Client frames are masked with a fresh random key.
func (c *Conn) buildFrame(buf []byte, op byte, payload []byte) int {
	n := len(payload)
	buf[0] = 0x80 | op
	hl := 2
	var b1 byte
	switch {
	case n < 126:
		b1 = byte(n)
	case n <= 0xffff:
		b1 = 126
		binary.BigEndian.PutUint16(buf[2:4], uint16(n)) // #nosec G115 -- n <= 0xffff
		hl = 4
	default:
		b1 = 127
		binary.BigEndian.PutUint64(buf[2:10], uint64(n)) // #nosec G115 -- n is a length
		hl = 10
	}
	if !c.client {
		buf[1] = b1
		copy(buf[hl:], payload)
		return hl + n
	}
	buf[1] = b1 | 0x80
	var key [4]byte
	_, _ = rand.Read(key[:]) // never fails since Go 1.24
	copy(buf[hl:hl+4], key[:])
	hl += 4
	maskCopy(buf[hl:hl+n], payload, key, 0)
	return hl + n
}

// writeBlocked reports why no further frame may be written (frameMu held).
func (c *Conn) writeBlocked() error {
	if c.werr != nil {
		return c.werr
	}
	if c.closeSent {
		return c.endErr()
	}
	select {
	case <-c.done:
		return c.endErr()
	default:
		return nil
	}
}

// endErr is the error to report once the connection has ended.
func (c *Conn) endErr() error {
	if err := c.getCloseErr(); err != nil {
		return err
	}
	return errLocalClose
}

// writeRaw writes one complete frame with a single Write on the raw
// connection (frameMu held). A frame that was only partly written leaves the
// stream corrupt, so the write side is poisoned; a failure before the first
// byte (a deadline, say) is retryable.
func (c *Conn) writeRaw(frame []byte) error {
	c.idle.writeStart()
	n, err := c.raw.Write(frame)
	c.idle.writeEnd()
	if err == nil {
		return nil
	}
	if n > 0 {
		if isTimeout(err) {
			// Not wrapped: a timeout error would invite the caller to retry.
			c.werr = fmt.Errorf("wsconn: write interrupted in the middle of a frame: %v", err)
		} else {
			c.werr = fmt.Errorf("wsconn: write interrupted in the middle of a frame: %w", err)
		}
		return c.werr
	}
	if ce := c.getCloseErr(); ce != nil {
		return ce
	}
	return err
}

// Write sends p as binary frames of at most MaxFrame bytes, one write syscall
// per frame. It returns the number of bytes of p that were fully framed and
// written.
func (c *Conn) Write(p []byte) (int, error) {
	c.dataMu.Lock()
	defer c.dataMu.Unlock()
	total := 0
	for len(p) > 0 {
		chunk := min(len(p), c.maxFrame)
		if err := c.writeData(p[:chunk]); err != nil {
			return total, err
		}
		total += chunk
		p = p[chunk:]
	}
	return total, nil
}

func (c *Conn) writeData(payload []byte) error {
	bp := c.fpool.Get().(*[]byte)
	defer c.fpool.Put(bp)
	n := c.buildFrame(*bp, opBinary, payload)
	c.frameMu.Lock()
	defer c.frameMu.Unlock()
	if err := c.writeBlocked(); err != nil {
		return err
	}
	return c.writeRaw((*bp)[:n])
}

// ---------------------------------------------------------------------------
// Read

// rawRead reads from the raw connection and tells the idle tracker that the
// peer is alive when bytes arrived.
func (c *Conn) rawRead(p []byte) (int, error) {
	n, err := c.raw.Read(p)
	if n > 0 {
		c.idle.arrived()
	}
	return n, err
}

// rawReadDefer is rawRead where an error that comes with data is held back
// until the data was consumed (a timeout is simply dropped: it comes again).
func (c *Conn) rawReadDefer(p []byte) (int, error) {
	n, err := c.rawRead(p)
	if n > 0 && err != nil {
		if !isTimeout(err) {
			c.deferred = err
		}
		err = nil
	}
	return n, err
}

// readSome reads up to len(p) stream bytes: first what is buffered (the reader
// that parsed the upgrade, then the small pooled buffer), then the raw
// connection. Large reads bypass the buffer. It never returns n > 0 together
// with an error.
func (c *Conn) readSome(p []byte) (int, error) {
	if c.rr < c.rn {
		n := copy(p, (*c.rbuf)[c.rr:c.rn])
		c.rr += n
		return n, nil
	}
	if c.deferred != nil {
		err := c.deferred
		c.deferred = nil
		return 0, err
	}
	if c.pre != nil {
		if nb := c.pre.Buffered(); nb > 0 {
			return c.pre.Read(p[:min(nb, len(p))])
		}
		c.pre = nil
	}
	if len(p) >= rbufSize {
		return c.rawReadDefer(p)
	}
	if c.rbuf == nil {
		c.rbuf = rbufPool.Get().(*[]byte)
		c.rr, c.rn = 0, 0
	}
	n, err := c.rawReadDefer(*c.rbuf)
	if n == 0 {
		return 0, err
	}
	c.rr, c.rn = 0, n
	m := copy(p, (*c.rbuf)[:n])
	c.rr = m
	return m, nil
}

// releaseRbuf gives the read buffer back once it holds nothing.
func (c *Conn) releaseRbuf() {
	if c.rbuf != nil && c.rr >= c.rn {
		rbufPool.Put(c.rbuf)
		c.rbuf, c.rr, c.rn = nil, 0, 0
	}
}

// Read returns payload bytes of the binary frames received. A deadline timeout
// leaves the read state intact. After a close frame it returns an error that
// wraps ErrClosed, after a protocol violation one that wraps ErrProtocol; it
// never returns io.EOF.
func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	c.idle.readEnter()
	defer c.idle.readExit()
	defer c.releaseRbuf()
	return c.read(p)
}

func (c *Conn) read(p []byte) (int, error) {
	for {
		if c.rerr != nil {
			return 0, c.rerr
		}
		if err := c.getCloseErr(); err != nil {
			c.rerr = err
			return 0, err
		}
		if !c.active {
			if err := c.readHeader(); err != nil {
				return 0, c.readFailed(err)
			}
			continue
		}
		if c.f.op >= opClose { // control frame: collect the whole payload
			for c.remain > 0 {
				n, err := c.readSome(c.ctl[c.ctlN : c.ctlN+c.remain])
				if n > 0 {
					if c.f.masked {
						seg := c.ctl[c.ctlN : c.ctlN+n]
						maskCopy(seg, seg, c.f.key, c.ctlN)
					}
					c.ctlN += n
					c.remain -= n
				}
				if err != nil {
					return 0, c.readFailed(err)
				}
			}
			c.active = false
			if err := c.handleControl(); err != nil {
				return 0, err
			}
			continue
		}
		if c.remain == 0 { // zero-length data frame
			c.active = false
			continue
		}
		n, err := c.readSome(p[:min(len(p), c.remain)])
		if n > 0 {
			if c.f.masked {
				maskCopy(p[:n], p[:n], c.f.key, c.pos)
			}
			c.pos += n
			c.remain -= n
			if c.remain == 0 {
				c.active = false
			}
			return n, nil
		}
		if err != nil {
			return 0, c.readFailed(err)
		}
	}
}

// readFailed turns a raw read error into the error Read reports. A deadline
// timeout is returned as it is and leaves the state untouched; anything else is
// terminal (and never io.EOF).
func (c *Conn) readFailed(err error) error {
	if ce := c.getCloseErr(); ce != nil {
		c.rerr = ce
		return ce
	}
	if isTimeout(err) {
		return err
	}
	var perr *ProtocolError
	if errors.As(err, &perr) {
		c.rerr = err
		return err
	}
	if errors.Is(err, io.EOF) {
		err = fmt.Errorf("wsconn: the connection ended without a close frame: %w", io.ErrUnexpectedEOF)
	} else {
		err = fmt.Errorf("wsconn: read: %w", err)
	}
	c.rerr = err
	return err
}

// readHeader reads and validates the next frame header. Its partial state
// (c.hdr, c.hdrN) survives a timeout.
func (c *Conn) readHeader() error {
	for {
		need := 2
		if c.hdrN >= 2 {
			need = headerNeed(c.hdr[1])
		}
		if c.hdrN >= need {
			break
		}
		n, err := c.readSome(c.hdr[c.hdrN:need])
		c.hdrN += n
		if err != nil {
			return err
		}
	}
	h, rsv, msb := parseHeader(c.hdr[:c.hdrN])
	c.hdrN = 0
	switch {
	case rsv:
		return c.protoFail(CloseProtocolError, "reserved bits set")
	case msb:
		return c.protoFail(CloseProtocolError, "64-bit length with the top bit set")
	}
	switch h.op {
	case opContinuation:
		if !c.inMsg {
			return c.protoFail(CloseProtocolError, "continuation frame without a message")
		}
		c.inMsg = !h.fin
	case opBinary:
		if c.inMsg {
			return c.protoFail(CloseProtocolError, "new data frame inside a fragmented message")
		}
		c.inMsg = !h.fin
	case opText:
		return c.protoFail(CloseUnsupported, "text frames are not supported")
	case opClose, opPing, opPong:
		if !h.fin {
			return c.protoFail(CloseProtocolError, "fragmented control frame")
		}
		if h.length > maxControlPayload {
			return c.protoFail(CloseProtocolError, "control frame payload above 125 bytes")
		}
	default:
		return c.protoFail(CloseProtocolError, "unknown opcode")
	}
	if h.length > MaxRecvFrame {
		return c.protoFail(CloseMessageTooBig, "frame too big")
	}
	c.f, c.remain, c.pos, c.ctlN, c.active = h, h.length, 0, 0, true
	return nil
}

// handleControl acts on a complete control frame.
func (c *Conn) handleControl() error {
	switch c.f.op {
	case opPing:
		c.pendMu.Lock()
		c.pongN = copy(c.pong[:], c.ctl[:c.ctlN]) // only the latest ping needs an answer
		c.hasPong = true
		c.pendMu.Unlock()
		c.kick()
	case opPong:
	case opClose:
		if c.ctlN == 1 {
			return c.protoFail(CloseProtocolError, "close frame with a one-byte payload")
		}
		ce := &CloseError{Code: CloseNoStatus}
		echo := 0
		if c.ctlN >= 2 {
			ce.Code = int(binary.BigEndian.Uint16(c.ctl[:2]))
			ce.Reason = string(c.ctl[2:c.ctlN])
			echo = ce.Code
		}
		c.setCloseErr(ce)
		c.rerr = ce
		c.pendMu.Lock()
		c.echo = echo
		c.pendMu.Unlock()
		c.kick()
		return ce
	}
	return nil
}

// protoFail answers a framing violation with a close frame, drops the
// connection and returns the error Read reports.
func (c *Conn) protoFail(code int, reason string) error {
	err := &ProtocolError{Code: code, Reason: reason}
	c.setCloseErr(err)
	c.rerr = err
	c.sendClose(code, false)
	_, _ = c.terminate()
	return err
}
