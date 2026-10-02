package front

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/wsconn"
)

// ViaFront is the value of Via() of a connection that came through the front.
const ViaFront = "front"

// ServerOptions configure a Server.
type ServerOptions struct {
	// Secret is the path secret: only GET /<Secret>/c upgrades.
	Secret string
	// TLSMode is config.FrontTLSAuto (also ""), FrontTLSCustom or FrontTLSOff.
	// In auto mode the first byte of each connection decides: 0x16 is a TLS
	// handshake (Cloudflare "Full"), anything else plain HTTP ("Flexible"), so
	// one port serves both. Custom serves TLS only, with CertFile and KeyFile.
	// Off serves plain HTTP only.
	TLSMode  string
	CertFile string
	KeyFile  string
	// TrustedProxies are extra prefixes whose CF-Connecting-IP header is
	// trusted (besides loopback and the Cloudflare ranges).
	TrustedProxies []netip.Prefix
	// Logger receives debug and warning lines; nil discards them. The request
	// path is never logged.
	Logger *slog.Logger
	// MaxPreAuth bounds the connections that are not yet handed out by Accept
	// (TLS handshake, request parsing, upgraded and waiting): default 256.
	MaxPreAuth int
	// HeaderTimeout is the deadline for the TLS handshake and the whole request
	// head: default 10 s.
	HeaderTimeout time.Duration
	// WSIdleTimeout is wsconn's IdleTimeout of an upgraded connection: default
	// 25 s, negative = off.
	WSIdleTimeout time.Duration
	// PingInterval is wsconn's PingInterval (0 = 30 s, negative = no pings).
	PingInterval time.Duration
	// FailLimit and FailWindow: more than FailLimit scanner-class requests
	// (wrong secret, malformed) from one real address within FailWindow get that
	// address dropped without an answer until the window ends. Defaults 30 per
	// minute; a negative FailLimit turns the limiter off.
	FailLimit  int
	FailWindow time.Duration

	// trust overrides peerTrusted (tests: a peer that is not loopback).
	trust func(netip.Addr) bool
}

// Conn is an upgraded /c connection: a net.Conn over a WebSocket (see
// internal/wsconn) that knows which client it belongs to.
type Conn struct {
	*wsconn.Conn
	clientIP netip.Addr
	trusted  bool
}

// Via returns ViaFront.
func (c *Conn) Via() string { return ViaFront }

// TrustedClientIP reports whether ClientIP came from CF-Connecting-IP of a
// trusted peer (true) or is just the TCP peer address (false: behind a CDN
// that is an edge address and must not be recorded as the node's address).
func (c *Conn) TrustedClientIP() bool { return c.trusted }

// ClientIP returns the real client address when TrustedClientIP, else the TCP
// peer address (the zero Addr if it is not an IP address).
func (c *Conn) ClientIP() netip.Addr { return c.clientIP }

// Server is the hub's front listener. See the package documentation.
type Server struct {
	ln         net.Listener
	log        *slog.Logger
	secretSum  [sha256.Size]byte
	tlsCfg     *tls.Config // nil: plain only
	sniff      bool        // auto mode: TLS and plain on the same port
	serverName string
	proxies    []netip.Prefix
	trust      func(netip.Addr) bool
	fails      *failLimiter

	headerTimeout time.Duration
	wsIdle        time.Duration
	pingEvery     time.Duration

	slots     chan struct{}
	maxUntrst int32
	untrusted atomic.Int32
	queued    atomic.Int32
	maxQueue  int32
	out       chan *Conn

	mu     sync.Mutex
	active map[net.Conn]struct{} // connections in the pre-auth phase
	closed bool

	done      chan struct{}
	failed    chan struct{} // closed when the listener died on its own
	acceptErr error
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// NewServer starts the accept loop on ln, which the caller has bound; the
// Server owns it from here on and closes it in Close.
func NewServer(ln net.Listener, o ServerOptions) (*Server, error) {
	if ln == nil {
		return nil, errors.New("front: no listener")
	}
	if !ValidSecret(o.Secret) {
		return nil, errors.New("front: the path secret is empty, too long or has characters outside A-Z a-z 0-9 - _")
	}
	s := &Server{
		ln:            ln,
		log:           o.Logger,
		secretSum:     sha256.Sum256([]byte(o.Secret)),
		serverName:    pickServerName(),
		proxies:       append([]netip.Prefix(nil), o.TrustedProxies...),
		headerTimeout: o.HeaderTimeout,
		wsIdle:        o.WSIdleTimeout,
		pingEvery:     o.PingInterval,
		out:           make(chan *Conn),
		active:        map[net.Conn]struct{}{},
		done:          make(chan struct{}),
		failed:        make(chan struct{}),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	s.trust = o.trust
	if s.trust == nil {
		s.trust = func(a netip.Addr) bool { return peerTrusted(a, s.proxies) }
	}
	if s.headerTimeout <= 0 {
		s.headerTimeout = defaultHeaderTimeout
	}
	switch {
	case s.wsIdle == 0:
		s.wsIdle = defaultWSIdle
	case s.wsIdle < 0:
		s.wsIdle = 0
	}
	maxPre := o.MaxPreAuth
	if maxPre <= 0 {
		maxPre = defaultMaxPreAuth
	}
	s.slots = make(chan struct{}, maxPre)
	s.maxUntrst = int32(max(1, maxPre/2)) // #nosec G115 -- small
	s.maxQueue = int32(maxPre)            // #nosec G115 -- small
	limit, window := o.FailLimit, o.FailWindow
	if limit == 0 {
		limit = defaultFailLimit
	}
	if window <= 0 {
		window = defaultFailWindow
	}
	s.fails = newFailLimiter(limit, window)

	switch o.TLSMode {
	case "", config.FrontTLSAuto:
		cfg, err := SelfSignedTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("front: outer certificate: %w", err)
		}
		s.tlsCfg, s.sniff = cfg, true
	case config.FrontTLSCustom:
		cfg, err := customTLSConfig(o.CertFile, o.KeyFile)
		if err != nil {
			return nil, err
		}
		s.tlsCfg = cfg
	case config.FrontTLSOff:
	default:
		return nil, fmt.Errorf("front: unknown tls mode %q", o.TLSMode)
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Addr returns the listen address.
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// PreAuthInUse returns how many pre-auth slots are taken (connections in the
// handshake or head phase, or upgraded and not yet handed out by Accept).
func (s *Server) PreAuthInUse() int { return len(s.slots) }

// Accept returns the next upgraded /c connection as a *Conn. After Close it
// returns net.ErrClosed; if the listener died by itself it returns that
// failure.
func (s *Server) Accept() (net.Conn, error) {
	select {
	case <-s.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-s.out:
		if s.isClosed() {
			_ = c.Close()
			return nil, net.ErrClosed
		}
		return c, nil
	case <-s.done:
		return nil, net.ErrClosed
	case <-s.failed:
		return nil, fmt.Errorf("front: listener failed: %w", s.acceptErr)
	}
}

// Close stops the accept loop, closes the listener, every connection that is
// still in the pre-auth phase and every upgraded one that Accept did not
// hand out, and waits for the goroutines. Connections already handed out
// belong to the caller. It is safe to call more than once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		err := s.ln.Close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			s.closeErr = err
		}
		s.mu.Lock()
		s.closed = true
		for c := range s.active {
			_ = c.Close()
		}
		s.mu.Unlock()
	})
	s.wg.Wait()
	return s.closeErr
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.active[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn) {
	s.mu.Lock()
	delete(s.active, c)
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// Accept loop and admission

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	var delay time.Duration
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if s.isClosed() {
				return
			}
			if isTemporary(err) {
				delay = backoff(delay)
				s.log.Debug("front: accept failed, retrying", "error", err)
				select {
				case <-time.After(delay):
					continue
				case <-s.done:
					return
				}
			}
			s.log.Warn("front: the listener stopped", "error", err)
			s.acceptErr = err
			close(s.failed)
			return
		}
		delay = 0
		s.admit(c)
	}
}

// admit applies the pre-auth cap. A peer that cannot be a trusted proxy may
// hold at most half of the slots and is never queued, so a flood from
// anywhere cannot starve the CDN's connections; a trusted peer that finds no
// free slot waits in a bounded queue for at most HeaderTimeout.
func (s *Server) admit(c net.Conn) {
	peer := peerAddr(c).Addr()
	trusted := peer.IsValid() && s.trust(peer)
	if !trusted {
		if s.untrusted.Add(1) > s.maxUntrst {
			s.untrusted.Add(-1)
			_ = c.Close()
			return
		}
	}
	release := sync.OnceFunc(func() {
		<-s.slots
		if !trusted {
			s.untrusted.Add(-1)
		}
	})
	select {
	case s.slots <- struct{}{}:
		s.wg.Add(1)
		go s.handle(c, trusted, release)
		return
	default:
	}
	if !trusted || s.queued.Add(1) > s.maxQueue {
		if trusted {
			s.queued.Add(-1)
		} else {
			s.untrusted.Add(-1)
		}
		_ = c.Close()
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.queued.Add(-1)
		t := time.NewTimer(s.headerTimeout)
		defer t.Stop()
		select {
		case s.slots <- struct{}{}:
			s.wg.Add(1)
			go s.handle(c, trusted, release)
		case <-t.C:
			_ = c.Close()
		case <-s.done:
			_ = c.Close()
		}
	}()
}

// ---------------------------------------------------------------------------
// One connection

// prefixConn replays bytes already read from the connection (the sniffed
// first byte) before reading it again.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (p *prefixConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

func (p *prefixConn) CloseWrite() error {
	if cw, ok := p.Conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return nil
}

// headCounter fails once more than n bytes were read: the cap of the request
// line and headers together.
type headCounter struct {
	r io.Reader
	n int
}

var errHeadTooLargeReq = errors.New("front: request head too large")

func (h *headCounter) Read(p []byte) (int, error) {
	if h.n <= 0 {
		return 0, errHeadTooLargeReq
	}
	if len(p) > h.n {
		p = p[:h.n]
	}
	n, err := h.r.Read(p)
	h.n -= n
	return n, err
}

func (s *Server) handle(raw net.Conn, trustedPeer bool, release func()) {
	defer s.wg.Done()
	defer release()
	if !s.track(raw) {
		_ = raw.Close()
		return
	}
	handed := false
	defer func() {
		if !handed {
			s.untrack(raw)
		}
	}()
	peer := peerAddr(raw)
	failKey := netip.Addr{} // whom a scanner-class failure is charged to
	if !trustedPeer {
		failKey = peer.Addr()
	}
	// fail records a failure before the request is understood: only a peer that
	// is not a proxy can be charged (the address of an edge is shared).
	fail := func() {
		s.fails.fail(failKey)
	}

	_ = raw.SetDeadline(time.Now().Add(s.headerTimeout))
	conn, ok := s.outer(raw)
	if !ok {
		_ = raw.Close()
		if !trustedPeer {
			fail()
		}
		return
	}
	closeConn := func() { _ = conn.Close() }

	br := bufio.NewReaderSize(&headCounter{r: conn, n: maxHeaderBlock}, 4096)
	req, err := http.ReadRequest(br)
	if err != nil {
		closeConn()
		if !trustedPeer {
			fail()
		}
		s.log.Debug("front: request head refused", "reason", headFailure(err))
		return
	}

	// From here the request is understood: the real address is known. A request
	// that carries the secret is served even from an address that is blocked
	// for scanning (the check below applies to the scanner class only).
	realIP, hasReal := netip.Addr{}, false
	if trustedPeer {
		realIP, hasReal = connectingIP(req.Header)
		if hasReal {
			failKey = realIP
		}
	}
	segs := splitPath(req.RequestURI)
	secretOK := s.secretMatches(segs)
	if !secretOK && !isDecoyPage(req.RequestURI) {
		if s.fails.blocked(failKey) {
			closeConn()
			return
		}
		s.fails.fail(failKey)
	}
	if !secretOK || !isControlPath(segs) || !validUpgrade(req) || br.Buffered() != 0 {
		s.writeDecoy(conn, req.Method, req.RequestURI)
		closeConn()
		return
	}

	// An upgrade of /<secret>/c.
	var remote net.Addr
	clientIP, trusted := peer.Addr(), false
	if hasReal {
		clientIP, trusted = realIP, true
		remote = &net.TCPAddr{IP: realIP.AsSlice(), Port: int(peer.Port())}
	}
	_ = conn.SetWriteDeadline(time.Now().Add(s.headerTimeout))
	if _, err := io.WriteString(conn, upgradeResponse(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		closeConn()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	wc := wsconn.New(conn, nil, wsconn.Config{
		PingInterval: s.pingEvery,
		IdleTimeout:  s.wsIdle,
		RemoteAddr:   remote,
		Via:          ViaFront,
	})
	fc := &Conn{Conn: wc, clientIP: clientIP, trusted: trusted}
	// From here the wsconn owns the socket. Leaving the pre-auth set before the
	// hand-off means a Close that races with Accept can never close a connection
	// that Accept already returned; if Close wins, the select below closes it.
	handed = true
	s.untrack(raw)
	select {
	case s.out <- fc:
		s.log.Debug("front: control connection upgraded", "client_ip", clientIP.String(), "trusted_ip", trusted)
	case <-s.done:
		_ = fc.Close()
	}
}

// outer returns the connection the HTTP request is read from: raw itself, or
// the TLS server over it, after the handshake.
func (s *Server) outer(raw net.Conn) (net.Conn, bool) {
	if s.tlsCfg == nil {
		return raw, true
	}
	base := raw
	if s.sniff {
		var b [1]byte
		if _, err := io.ReadFull(raw, b[:]); err != nil {
			return nil, false
		}
		base = &prefixConn{Conn: raw, prefix: b[:]}
		if b[0] != 0x16 { // not a TLS handshake record: plain HTTP
			return base, true
		}
	}
	tc := tls.Server(base, s.tlsCfg)
	if err := tc.Handshake(); err != nil {
		s.log.Debug("front: outer TLS handshake failed", "reason", "tls")
		return nil, false
	}
	return tc, true
}

// headFailure names why a request head was refused, without the text of the
// error (it could echo part of the request).
func headFailure(err error) string {
	switch {
	case errors.Is(err, errHeadTooLargeReq):
		return "too large"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "closed"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return "malformed"
}

// ---------------------------------------------------------------------------
// Request checks

// splitPath returns the segments of the request target's path: "/a/b" is
// ["a", "b"]. A target that is not in origin form ("/...") has none.
func splitPath(requestURI string) []string {
	if !strings.HasPrefix(requestURI, "/") {
		return nil
	}
	return strings.Split(requestURI[1:], "/") // a query string stays in the last segment: no upgrade
}

// secretMatches compares the first segment with the secret in constant time
// (both are hashed first, so the length does not leak either).
func (s *Server) secretMatches(segs []string) bool {
	if len(segs) == 0 {
		return false
	}
	sum := sha256.Sum256([]byte(segs[0]))
	return subtle.ConstantTimeCompare(sum[:], s.secretSum[:]) == 1
}

// isControlPath reports whether the segments are exactly <secret>/c.
func isControlPath(segs []string) bool { return len(segs) == 2 && segs[1] == "c" }

// validUpgrade checks everything about a request but its path: GET, HTTP/1.1,
// Upgrade: websocket, Connection: upgrade, version 13, a well-formed key, no
// body, no Transfer-Encoding and no Expect.
func validUpgrade(r *http.Request) bool {
	if r.Method != http.MethodGet || !r.ProtoAtLeast(1, 1) {
		return false
	}
	if !headerHasToken(r.Header, "Upgrade", "websocket") || !headerHasToken(r.Header, "Connection", "upgrade") {
		return false
	}
	if v := r.Header.Values("Sec-WebSocket-Version"); len(v) != 1 || strings.TrimSpace(v[0]) != "13" {
		return false
	}
	k := r.Header.Values("Sec-WebSocket-Key")
	if len(k) != 1 {
		return false
	}
	if raw, err := base64.StdEncoding.DecodeString(k[0]); err != nil || len(raw) != 16 {
		return false
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || len(r.Header.Values("Expect")) != 0 {
		return false
	}
	return true
}

func upgradeResponse(key string) string {
	return "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsconn.AcceptKey(key) + "\r\n\r\n"
}
