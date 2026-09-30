package direct

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Relay data-plane limits (spec section 12: io.Copy with splice, 32 KB
// buffers).
const (
	// copyBufferSize is the buffer used when a connection cannot splice
	// (dst without io.ReaderFrom).
	copyBufferSize = 32 * 1024
	// spliceChunk bounds one ReadFrom call so idle deadlines are re-armed
	// while data flows; *net.TCPConn still splices every chunk.
	spliceChunk = 256 * 1024
	// handshakeTimeout bounds reading the 32-byte preamble on the node.
	handshakeTimeout = 10 * time.Second
	// maxHandshakes bounds concurrent unauthenticated connections on the
	// node; more are closed at once.
	maxHandshakes = 1024
	// replayCacheSize bounds remembered TCP nonces.
	replayCacheSize = 1 << 16
	// warnInterval rate-limits repeated warnings (scanners, bad tokens).
	warnInterval = 10 * time.Second
	// acceptBackoffMax caps the sleep after temporary accept errors.
	acceptBackoffMax = time.Second
)

var copyBuffers = sync.Pool{New: func() any {
	b := make([]byte, copyBufferSize)
	return &b
}}

// RunRelay loads the relay.json at configPath and runs that relay half
// until ctx is cancelled (then it closes every listener and connection and
// returns nil). It is the entry point of "deyroute relay --tunnel <id>
// --config <file>". For the check role it returns after the check. Errors
// are DEY-B060 (config), DEY-B061 (listen) and DEY-B062 (check).
//
// The token is never logged; callers that install a log redactor should
// register it (LoadRelayConfig + Run) before logging anything else.
func RunRelay(ctx context.Context, configPath string, logger *slog.Logger) error {
	cfg, err := LoadRelayConfig(configPath)
	if err != nil {
		return err
	}
	return Run(ctx, cfg, logger)
}

// Run runs the relay described by cfg; see RunRelay.
func Run(ctx context.Context, cfg *RelayConfig, logger *slog.Logger) error {
	if cfg == nil {
		return deyerr.New(deyerr.B060, deyerr.Params{"path": "relay.json", "reason": "no configuration"})
	}
	if err := cfg.Validate(); err != nil {
		return deyerr.Wrap(deyerr.B060, err, deyerr.Params{"path": "relay.json", "reason": err.Error()})
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	logger = logger.With("component", "relay", "tunnel", cfg.Tunnel, "role", cfg.Role)
	if cfg.Role == RoleCheck {
		return runCheck(ctx, cfg, logger)
	}
	r := newRelay(cfg, logger)
	if err := r.start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	r.stop()
	logger.Info("relay stopped")
	return nil
}

// relay is one running relay half.
type relay struct {
	cfg    *RelayConfig
	log    *slog.Logger
	key    []byte
	idle   time.Duration
	dial   time.Duration
	warn   *limiter
	replay *replayCache
	hs     chan struct{} // node: handshake slots
	dialer net.Dialer

	epoch  time.Time // base of mono()
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	stopping  bool
	listeners []io.Closer
	conns     map[net.Conn]struct{}

	tcpLn   map[int]net.Listener // hub: index → listener
	udpLn   map[int]*net.UDPConn // hub: index → listener
	nodeTCP net.Listener         // node
	hubUDP  *hubUDP
	nodeUDP *nodeUDP
}

func newRelay(cfg *RelayConfig, logger *slog.Logger) *relay {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &relay{
		cfg:    cfg,
		log:    logger,
		key:    []byte(cfg.Token),
		idle:   cfg.IdleTimeout(),
		dial:   cfg.DialTimeout(),
		warn:   newLimiter(warnInterval),
		replay: newReplayCache(ReplayWindow, replayCacheSize),
		hs:     make(chan struct{}, maxHandshakes),
		dialer: net.Dialer{Timeout: cfg.DialTimeout(), KeepAlive: 30 * time.Second},
		epoch:  time.Now(),
		conns:  map[net.Conn]struct{}{},
		tcpLn:  map[int]net.Listener{},
		udpLn:  map[int]*net.UDPConn{},
	}
}

// start opens every socket of the role and starts serving. When a socket
// cannot be opened, everything already opened is closed and DEY-B061 is
// returned.
func (r *relay) start(parent context.Context) error {
	r.ctx, r.cancel = context.WithCancel(parent)
	var err error
	if r.cfg.Role == RoleHub {
		err = r.openHub()
	} else {
		err = r.openNode()
	}
	if err != nil {
		r.stop()
		return err
	}
	r.serve()
	return nil
}

func (r *relay) listenErr(addr, proto string, err error) error {
	return deyerr.Wrap(deyerr.B061, err, deyerr.Params{"addr": addr, "proto": proto, "reason": err.Error()})
}

func (r *relay) openHub() error {
	var lc net.ListenConfig
	hasUDP := false
	for _, p := range r.cfg.Ports {
		switch p.Proto {
		case config.ProtoUDP:
			hasUDP = true
			pc, err := lc.ListenPacket(r.ctx, "udp", p.Listen)
			if err != nil {
				return r.listenErr(p.Listen, p.Proto, err)
			}
			r.addListener(pc)
			r.udpLn[p.Index] = pc.(*net.UDPConn)
		default:
			ln, err := lc.Listen(r.ctx, "tcp", p.Listen)
			if err != nil {
				return r.listenErr(p.Listen, p.Proto, err)
			}
			r.addListener(ln)
			r.tcpLn[p.Index] = ln
		}
	}
	if hasUDP {
		c, err := r.dialer.DialContext(r.ctx, "udp", r.cfg.Node)
		if err != nil {
			return r.listenErr(r.cfg.Node, config.ProtoUDP, err)
		}
		r.addListener(c)
		r.hubUDP = newHubUDP(r, c.(*net.UDPConn))
	}
	return nil
}

func (r *relay) openNode() error {
	var lc net.ListenConfig
	hasTCP, hasUDP := false, false
	for _, p := range r.cfg.Ports {
		hasTCP = hasTCP || p.Proto == config.ProtoTCP
		hasUDP = hasUDP || p.Proto == config.ProtoUDP
	}
	if hasTCP {
		ln, err := lc.Listen(r.ctx, "tcp", r.cfg.Bind)
		if err != nil {
			return r.listenErr(r.cfg.Bind, config.ProtoTCP, err)
		}
		r.addListener(ln)
		r.nodeTCP = ln
	}
	if hasUDP {
		bind := r.cfg.Bind
		if host, port, err := net.SplitHostPort(bind); err == nil && port == "0" && r.nodeTCP != nil {
			// An ephemeral port (tests) is shared by both protocols, like
			// the fixed control port in production.
			bind = net.JoinHostPort(host, strconv.Itoa(r.nodeTCP.Addr().(*net.TCPAddr).Port))
		}
		pc, err := lc.ListenPacket(r.ctx, "udp", bind)
		if err != nil {
			return r.listenErr(bind, config.ProtoUDP, err)
		}
		r.addListener(pc)
		r.nodeUDP = newNodeUDP(r, pc.(*net.UDPConn))
	}
	return nil
}

// serve starts one goroutine per socket.
func (r *relay) serve() {
	for index, ln := range r.tcpLn {
		r.goFn(func() { r.acceptLoop(ln, func(c net.Conn) { r.hubConn(c, index) }) })
		r.log.Info("relay listening", "proto", "tcp", "addr", ln.Addr().String(), "index", index)
	}
	for index, ln := range r.udpLn {
		r.goFn(func() { r.hubUDP.serveListener(ln, index) })
		r.log.Info("relay listening", "proto", "udp", "addr", ln.LocalAddr().String(), "index", index)
	}
	if r.hubUDP != nil {
		r.goFn(r.hubUDP.readUpstream)
		r.goFn(r.hubUDP.janitor)
	}
	if r.nodeTCP != nil {
		r.goFn(func() { r.acceptLoop(r.nodeTCP, r.nodeConn) })
		r.log.Info("relay listening", "proto", "tcp", "addr", r.nodeTCP.Addr().String())
	}
	if r.nodeUDP != nil {
		r.goFn(r.nodeUDP.serve)
		r.goFn(r.nodeUDP.janitor)
		r.log.Info("relay listening", "proto", "udp", "addr", r.nodeUDP.ln.LocalAddr().String())
	}
}

// stop closes every socket and connection and waits for all goroutines.
func (r *relay) stop() {
	r.mu.Lock()
	r.stopping = true
	listeners := r.listeners
	r.listeners = nil
	conns := r.conns
	r.conns = map[net.Conn]struct{}{}
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	for _, l := range listeners {
		_ = l.Close()
	}
	for c := range conns {
		_ = c.Close()
	}
	if r.hubUDP != nil {
		r.hubUDP.closeAll()
	}
	if r.nodeUDP != nil {
		r.nodeUDP.closeAll()
	}
	r.wg.Wait()
}

// goFn runs f in a goroutine owned by the relay (stop waits for it).
func (r *relay) goFn(f func()) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		f()
	}()
}

func (r *relay) addListener(c io.Closer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = append(r.listeners, c)
}

func (r *relay) isStopping() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopping
}

// track registers c so stop can close it; it returns false (and closes c)
// when the relay is stopping.
func (r *relay) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		_ = c.Close()
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

// release closes c and forgets it.
func (r *relay) release(c net.Conn) {
	r.mu.Lock()
	delete(r.conns, c)
	r.mu.Unlock()
	_ = c.Close()
}

// mono returns monotonic nanoseconds since the relay was created (session
// bookkeeping is immune to wall-clock steps).
func (r *relay) mono() int64 { return int64(time.Since(r.epoch)) }

// activeConns returns the number of tracked connections (tests).
func (r *relay) activeConns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.conns)
}

// sleep waits d or until the relay stops; it reports whether to go on.
func (r *relay) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-r.ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// acceptLoop accepts connections until ln is closed. Temporary errors
// (e.g. EMFILE) back off instead of spinning.
func (r *relay) acceptLoop(ln net.Listener, handle func(net.Conn)) {
	backoff := 5 * time.Millisecond
	for {
		c, err := ln.Accept()
		if err != nil {
			if r.isStopping() || errors.Is(err, net.ErrClosed) {
				return
			}
			r.warn.log(r.log, "accept", "relay accept failed", "err", err.Error())
			if !r.sleep(backoff) {
				return
			}
			backoff = min(backoff*2, acceptBackoffMax)
			continue
		}
		backoff = 5 * time.Millisecond
		if !r.track(c) {
			return
		}
		r.goFn(func() {
			defer r.release(c)
			handle(c)
		})
	}
}

// hubConn relays one user connection accepted on the hub to the node.
func (r *relay) hubConn(client net.Conn, index int) {
	up, err := r.dialer.DialContext(r.ctx, "tcp", r.cfg.Node)
	if err != nil {
		if !r.isStopping() {
			r.warn.log(r.log, "dial-node", "relay cannot reach the node relay", "addr", r.cfg.Node, "err", err.Error())
		}
		return
	}
	if !r.track(up) {
		return
	}
	defer r.release(up)
	nonce, err := newNonce()
	if err != nil {
		r.log.Error("relay: no randomness for the nonce", "err", err.Error())
		return
	}
	pre := buildPreamble(newMacer(r.key), index, nonce)
	_ = up.SetWriteDeadline(time.Now().Add(r.dial))
	if _, err := up.Write(pre[:]); err != nil {
		r.warn.log(r.log, "preamble", "relay cannot send the preamble", "err", err.Error())
		return
	}
	_ = up.SetWriteDeadline(time.Time{})
	r.pipe(client, up)
}

// nodeConn authenticates one connection from the hub half and relays it to
// the configured target it names. Nothing is sent back on failure: the
// connection is closed, so the port looks like a silent service.
func (r *relay) nodeConn(c net.Conn) {
	select {
	case r.hs <- struct{}{}:
	default:
		r.warn.log(r.log, "handshakes", "relay: too many pending handshakes, dropping connections", "limit", maxHandshakes)
		return
	}
	index, ok := r.handshake(c)
	<-r.hs
	if !ok {
		return
	}
	target, ok := r.target(index, config.ProtoTCP)
	if !ok {
		r.warn.log(r.log, "index", "relay: hub asked for an unknown tcp target; hub and node configs differ (re-render the tunnel)", "index", index)
		return
	}
	up, err := r.dialer.DialContext(r.ctx, "tcp", target)
	if err != nil {
		if !r.isStopping() {
			r.warn.log(r.log, "dial-target:"+target, "relay cannot reach the target service", "target", target, "err", err.Error())
		}
		return
	}
	if !r.track(up) {
		return
	}
	defer r.release(up)
	r.pipe(c, up)
}

// handshake reads and authenticates the preamble.
func (r *relay) handshake(c net.Conn) (int, bool) {
	var buf [preambleLen]byte
	_ = c.SetReadDeadline(time.Now().Add(handshakeTimeout))
	if _, err := io.ReadFull(c, buf[:]); err != nil {
		r.log.Debug("relay: no preamble", "remote", c.RemoteAddr().String(), "err", err.Error())
		return 0, false
	}
	index, nonce, ok := parsePreamble(newMacer(r.key), buf[:])
	if !ok {
		r.warn.log(r.log, "auth", "relay: rejected a connection that failed authentication (wrong token or not a deyroute hub)", "remote", c.RemoteAddr().String())
		return 0, false
	}
	if !r.replay.add(nonce, time.Now()) {
		r.warn.log(r.log, "replay", "relay: rejected a replayed connection preamble", "remote", c.RemoteAddr().String())
		return 0, false
	}
	_ = c.SetReadDeadline(time.Time{})
	return index, true
}

// target returns the configured target of index for proto: the node's
// allow-list. Nothing else is ever dialled.
func (r *relay) target(index int, proto string) (string, bool) {
	for _, p := range r.cfg.Ports {
		if p.Index == index && p.Proto == proto {
			return p.Target, true
		}
	}
	return "", false
}

// activity is the last time any byte moved in either direction of a pair,
// on the monotonic clock (offset from base).
type activity struct {
	base time.Time
	last atomic.Int64
}

func newActivity() *activity {
	a := &activity{base: time.Now()}
	a.touch()
	return a
}

func (a *activity) touch()               { a.last.Store(int64(time.Since(a.base))) }
func (a *activity) since() time.Duration { return time.Since(a.base) - time.Duration(a.last.Load()) }

// closeWriter is implemented by *net.TCPConn (half-close).
type closeWriter interface{ CloseWrite() error }

func isTimeout(err error) bool { return errors.Is(err, os.ErrDeadlineExceeded) }
func isRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

// activityWindow is how often a direction reports progress: its read
// deadline, re-armed every window, makes ReadFrom return (and touch the
// pair's activity) even while a chunk is still incomplete.
func (r *relay) activityWindow() time.Duration {
	return max(r.idle/10, 10*time.Millisecond)
}

// pipe copies both directions until both are finished. A clean EOF is
// propagated as a half-close; an error in either direction closes both
// connections. A watchdog closes both when no byte moved in either
// direction for the idle timeout (plus one activity window, so bytes of a
// window not yet reported never count as idle).
//
// Only read deadlines are used: a write deadline could expire while splice
// holds bytes already read from src in its pipe, losing them. A direction
// blocked writing to a peer that does not read reports no progress, so the
// watchdog also reaps connections stalled that way.
func (r *relay) pipe(a, b net.Conn) {
	act := newActivity()
	window := r.activityWindow()
	grace := r.idle + window

	var done atomic.Bool
	var watchdog atomic.Pointer[time.Timer]
	t := time.AfterFunc(grace, func() {
		if done.Load() {
			return
		}
		if quiet := act.since(); quiet < grace {
			if tm := watchdog.Load(); tm != nil {
				tm.Reset(grace - quiet)
			}
			return
		}
		_ = a.Close()
		_ = b.Close()
	})
	watchdog.Store(t)
	defer func() {
		done.Store(true)
		t.Stop()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		copyDir(b, a, act, window)
	}()
	copyDir(a, b, act, window)
	wg.Wait()
}

// copyDir copies src to dst in chunks. *net.TCPConn → *net.TCPConn uses
// splice(2) through ReadFrom; other connections use a pooled 32 KB buffer.
func copyDir(dst, src net.Conn, act *activity, window time.Duration) {
	for {
		_ = src.SetReadDeadline(time.Now().Add(window))
		n, err := copyChunk(dst, src)
		if n > 0 {
			act.touch()
		}
		if err == nil {
			if n < spliceChunk { // src reached EOF
				if cw, ok := dst.(closeWriter); ok && cw.CloseWrite() == nil {
					return
				}
				break
			}
			continue
		}
		if isTimeout(err) {
			continue // quiet window; the watchdog decides about idleness
		}
		break
	}
	_ = src.Close()
	_ = dst.Close()
}

// copyChunk copies at most spliceChunk bytes. A *net.TCPConn destination
// splices from an *io.LimitedReader wrapping a *net.TCPConn source.
func copyChunk(dst io.Writer, src io.Reader) (int64, error) {
	lr := &io.LimitedReader{R: src, N: spliceChunk}
	if rf, ok := dst.(io.ReaderFrom); ok {
		return rf.ReadFrom(lr)
	}
	bp := copyBuffers.Get().(*[]byte)
	defer copyBuffers.Put(bp)
	return io.CopyBuffer(dst, lr, *bp)
}

// limiter rate-limits warnings per key.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[string]time.Time
	dropped  map[string]int
}

func newLimiter(interval time.Duration) *limiter {
	return &limiter{interval: interval, last: map[string]time.Time{}, dropped: map[string]int{}}
}

// log writes msg at warn level unless key was logged within the interval;
// suppressed repeats are counted in the next line.
func (l *limiter) log(logger *slog.Logger, key, msg string, args ...any) {
	l.mu.Lock()
	now := time.Now()
	if t, ok := l.last[key]; ok && now.Sub(t) < l.interval {
		l.dropped[key]++
		l.mu.Unlock()
		return
	}
	if len(l.last) > 1024 { // bound the key space
		l.last = map[string]time.Time{}
		l.dropped = map[string]int{}
	}
	l.last[key] = now
	n := l.dropped[key]
	delete(l.dropped, key)
	l.mu.Unlock()
	if n > 0 {
		args = append(args, "suppressed", n)
	}
	logger.Warn(msg, args...)
}
