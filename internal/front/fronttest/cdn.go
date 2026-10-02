package fronttest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Direction names one direction of the proxied byte stream.
type Direction int

// The directions: Up is client to origin, Down is origin to client.
const (
	Up Direction = iota + 1
	Down
	Both
)

// DefaultIdleCut is the idle window of the real edge (about 100 s).
const DefaultIdleCut = 100 * time.Second

// Options configure a CDN. The zero value is not usable: OriginAddr is
// required. Everything but OriginAddr, PlainClients, Hosts and Cert can be
// changed while the CDN runs with Configure (running connections notice it on
// their next chunk).
type Options struct {
	// OriginAddr is the host:port the CDN forwards to (the hub front listener).
	OriginAddr string
	// OriginTLS makes the origin leg TLS with the certificate not verified, like
	// Cloudflare's "Full" SSL mode. False is plain HTTP ("Flexible", HTTP ports).
	OriginTLS bool
	// PlainClients serves plain HTTP to clients (a Cloudflare HTTP port, ws://)
	// instead of TLS.
	PlainClients bool
	// Hosts are the names the generated edge certificate covers (DefaultHosts).
	Hosts []string
	// Cert overrides the generated edge certificate (an expired one, for
	// example, or one issued by another CA).
	Cert *tls.Certificate
	// ClientIP is the address put in CF-Connecting-IP and X-Forwarded-For; empty
	// means the peer address of the client connection.
	ClientIP string

	// Chunk re-chunks the forwarded bytes into writes of at most this many bytes.
	Chunk int
	// Delay holds every forwarded chunk back by this long (latency of a leg).
	Delay time.Duration
	// BytesPerSec limits the forwarded rate in each direction (0 = unlimited).
	BytesPerSec int
	// DropAll swallows everything: new connections are read and never answered,
	// running ones stop forwarding without being closed.
	DropAll bool
	// PauseOrigin stalls the origin: nothing is forwarded in either direction
	// and no new origin connection is made, but nothing is closed (a half-open
	// stall).
	PauseOrigin bool
	// IdleCut closes a proxied connection after this long without activity
	// (0 = DefaultIdleCut, negative = never).
	IdleCut time.Duration
	// PingsResetIdle makes ping and pong frames count as activity for IdleCut.
	// The default (false) is what the real edge seems to do: only data counts.
	PingsResetIdle bool
	// SwallowPings drops ping and pong frames in both directions.
	SwallowPings bool
	// CutAfterBytes cuts the origin leg of a connection after this many bytes in
	// the direction(s) CutDir: the rest is swallowed silently (a stall), or the
	// connection is closed when CutClose is set. 0 = no cut.
	CutAfterBytes int64
	CutDir        Direction
	CutClose      bool
	// Canned, when set, is answered to every request without contacting the origin.
	Canned *Canned
}

// Stats are the counters of a CDN.
type Stats struct {
	Requests  int64 // HTTP requests parsed
	Upgrades  int64 // 101 answers relayed
	BytesUp   int64 // bytes forwarded client to origin after a 101
	BytesDown int64 // bytes forwarded origin to client after a 101
}

// Request describes one request the CDN parsed. The path itself is not
// recorded (it carries the front secret); PathHash identifies it.
type Request struct {
	Method     string
	PathHash   string // first 8 hex digits of sha256(request URI)
	Header     http.Header
	Host       string
	Peer       string
	SNI        string
	ALPN       []string // protocols the client offered
	TLSVersion uint16   // 0 for plain clients
	Upgrade    bool     // the request asked for a WebSocket upgrade
}

// CDN is the fake Cloudflare edge.
type CDN struct {
	ln    net.Listener
	certs *CertSet
	cfg   *tls.Config

	mu    sync.Mutex
	opts  Options
	reqs  []Request
	conns map[net.Conn]struct{}

	hellos sync.Map // client address -> hello

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once

	nRequests, nUpgrades, bytesUp, bytesDown atomic.Int64
}

type hello struct {
	sni      string
	alpn     []string
	versions []uint16
}

// NewCDN starts a CDN listening on 127.0.0.1:0.
func NewCDN(o Options) (*CDN, error) {
	if o.OriginAddr == "" {
		return nil, errors.New("fronttest: OriginAddr is required")
	}
	c := &CDN{opts: o, conns: map[net.Conn]struct{}{}}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	if !o.PlainClients {
		if o.Cert != nil {
			c.certs = &CertSet{Cert: *o.Cert, Pool: x509.NewCertPool()}
		} else {
			hosts := o.Hosts
			if len(hosts) == 0 {
				hosts = DefaultHosts
			}
			cs, err := NewCertSet(hosts, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
			if err != nil {
				return nil, err
			}
			c.certs = cs
		}
		c.cfg = &tls.Config{
			Certificates: []tls.Certificate{c.certs.Cert},
			NextProtos:   []string{"h2", "http/1.1"},
			MinVersion:   tls.VersionTLS12,
			GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
				c.hellos.Store(h.Conn.RemoteAddr().String(), hello{
					sni: h.ServerName, alpn: slices.Clone(h.SupportedProtos), versions: slices.Clone(h.SupportedVersions),
				})
				return nil, nil
			},
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c.ln = ln
	c.wg.Add(1)
	go c.acceptLoop()
	return c, nil
}

// Addr returns the listen address (127.0.0.1:port).
func (c *CDN) Addr() string { return c.ln.Addr().String() }

// Port returns the listen port.
func (c *CDN) Port() int { return c.ln.Addr().(*net.TCPAddr).Port }

// CAPool returns a pool that trusts the edge certificate (empty when the CDN
// was given its own Cert or serves plain clients).
func (c *CDN) CAPool() *x509.CertPool {
	if c.certs == nil {
		return x509.NewCertPool()
	}
	return c.certs.Pool
}

// Configure changes the options of a running CDN.
func (c *CDN) Configure(f func(*Options)) {
	c.mu.Lock()
	f(&c.opts)
	c.mu.Unlock()
}

func (c *CDN) snapshot() Options {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opts
}

// Stats returns the counters.
func (c *CDN) Stats() Stats {
	return Stats{c.nRequests.Load(), c.nUpgrades.Load(), c.bytesUp.Load(), c.bytesDown.Load()}
}

// Requests returns the requests parsed so far.
func (c *CDN) Requests() []Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reqs)
}

// Close stops the listener, closes every connection and waits for all
// goroutines. It is safe to call more than once.
func (c *CDN) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		err = c.ln.Close()
		c.mu.Lock()
		for k := range c.conns {
			_ = k.Close()
		}
		c.mu.Unlock()
	})
	c.wg.Wait()
	return err
}

func (c *CDN) track(k net.Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		_ = k.Close()
		return false
	}
	c.conns[k] = struct{}{}
	return true
}

func (c *CDN) untrack(k net.Conn) {
	c.mu.Lock()
	delete(c.conns, k)
	c.mu.Unlock()
	_ = k.Close()
}

func (c *CDN) acceptLoop() {
	defer c.wg.Done()
	for {
		k, err := c.ln.Accept()
		if err != nil {
			return
		}
		if !c.track(k) {
			continue
		}
		c.wg.Add(1)
		go c.serve(k)
	}
}

func ray() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:]) + "-FRA"
}

func (c *CDN) serve(raw net.Conn) {
	defer c.wg.Done()
	defer c.untrack(raw)
	conn := raw
	var rec Request
	rec.Peer = raw.RemoteAddr().String()
	if c.cfg != nil {
		tc := tls.Server(raw, c.cfg)
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			c.hellos.Delete(rec.Peer)
			return
		}
		_ = tc.SetDeadline(time.Time{})
		st := tc.ConnectionState()
		rec.TLSVersion = st.Version
		if h, ok := c.hellos.LoadAndDelete(rec.Peer); ok {
			hi := h.(hello)
			rec.SNI, rec.ALPN = hi.sni, hi.alpn
		}
		if st.NegotiatedProtocol == "h2" {
			return // the fake edge speaks HTTP/1.1 only
		}
		conn = tc
	}
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	o := c.snapshot()
	if o.DropAll {
		_, _ = io.Copy(io.Discard, br)
		return
	}
	sum := sha256.Sum256([]byte(req.RequestURI))
	rec.Method, rec.PathHash = req.Method, hex.EncodeToString(sum[:4])
	rec.Header, rec.Host = req.Header.Clone(), req.Host
	rec.Upgrade = headerHasToken(req.Header, "Upgrade", "websocket")
	c.mu.Lock()
	c.reqs = append(c.reqs, rec)
	c.mu.Unlock()
	c.nRequests.Add(1)

	if o.Canned != nil {
		_, _ = conn.Write(o.Canned.bytes(ray()))
		return
	}
	for o.PauseOrigin { // a stalled origin: nothing happens
		if !c.sleep(10 * time.Millisecond) {
			return
		}
		o = c.snapshot()
	}
	oc, status := c.dialOrigin(o, req.Host)
	if oc == nil {
		_, _ = conn.Write(OriginError(status).bytes(ray()))
		return
	}
	defer c.untrack(oc.raw)
	if err := writeOriginRequest(oc.conn, req, br, o, raw, c.cfg == nil); err != nil {
		_, _ = conn.Write(OriginError(520).bytes(ray()))
		return
	}
	obr := bufio.NewReader(oc.conn)
	resp, err := http.ReadResponse(obr, &http.Request{Method: req.Method})
	if err != nil {
		_, _ = conn.Write(OriginError(520).bytes(ray()))
		return
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Write(conn)
		_ = resp.Body.Close()
		return
	}
	var head bytes.Buffer
	head.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_ = resp.Header.Write(&head)
	head.WriteString("\r\n")
	if _, err := conn.Write(head.Bytes()); err != nil {
		return
	}
	c.nUpgrades.Add(1)
	c.pump(conn, br, oc.conn, obr, oc.raw)
}

// originConn is the origin leg: conn is what is read and written, raw the
// underlying socket (tracked for Close).
type originConn struct {
	conn net.Conn
	raw  net.Conn
}

// dialOrigin connects to the origin like the edge does and returns the 52x
// status to answer when it cannot.
func (c *CDN) dialOrigin(o Options, host string) (*originConn, int) {
	d := net.Dialer{Timeout: 5 * time.Second}
	raw, err := d.DialContext(c.ctx, "tcp", o.OriginAddr)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, 522
		}
		return nil, 521
	}
	if !c.track(raw) {
		return nil, 520
	}
	oc := &originConn{conn: raw, raw: raw}
	if o.OriginTLS {
		name, _, serr := net.SplitHostPort(host)
		if serr != nil {
			name = host
		}
		tc := tls.Client(raw, &tls.Config{
			ServerName:         name,
			NextProtos:         []string{"http/1.1"},
			InsecureSkipVerify: true, // #nosec G402 -- fake edge: Cloudflare "Full" does not verify the origin certificate
		})
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			c.untrack(raw)
			return nil, 525
		}
		_ = tc.SetDeadline(time.Time{})
		oc.conn = tc
	}
	return oc, 0
}

// writeOriginRequest forwards the request as the edge does: no
// Sec-WebSocket-Extensions, the client address in CF-Connecting-IP and
// X-Forwarded-For.
func writeOriginRequest(dst net.Conn, req *http.Request, br *bufio.Reader, o Options, client net.Conn, plain bool) error {
	ip := o.ClientIP
	if ip == "" {
		ip, _, _ = net.SplitHostPort(client.RemoteAddr().String())
	}
	scheme := "https"
	if plain {
		scheme = "http"
	}
	h := req.Header.Clone()
	for _, k := range []string{"Sec-Websocket-Extensions", "Cf-Connecting-Ip", "X-Forwarded-For", "X-Forwarded-Proto", "Cf-Visitor", "Cf-Ray"} {
		h.Del(k)
	}
	h.Set("CF-Connecting-IP", ip)
	h.Set("X-Forwarded-For", ip)
	h.Set("X-Forwarded-Proto", scheme)
	h.Set("CF-Visitor", `{"scheme":"`+scheme+`"}`)
	h.Set("CF-RAY", ray())
	if req.ContentLength > 0 {
		h.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\n", req.Method, req.RequestURI, req.Host)
	if err := h.Write(&b); err != nil {
		return err
	}
	b.WriteString("\r\n")
	if _, err := dst.Write(b.Bytes()); err != nil {
		return err
	}
	if req.ContentLength > 0 {
		if _, err := io.CopyN(dst, br, req.ContentLength); err != nil {
			return err
		}
	}
	return nil
}

func headerHasToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// sleep waits d and reports false when the CDN was closed meanwhile.
func (c *CDN) sleep(d time.Duration) bool {
	if d <= 0 {
		return c.ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-c.ctx.Done():
		return false
	}
}

// session is the state of one proxied connection.
type session struct {
	last atomic.Int64 // unix nanoseconds of the last activity
}

func (s *session) touch() { s.last.Store(time.Now().UnixNano()) }

// pump copies both directions after the upgrade until one side ends, with the
// knobs applied, and applies the idle cut.
func (c *CDN) pump(client net.Conn, cbr io.Reader, origin net.Conn, obr io.Reader, originRaw net.Conn) {
	s := &session{}
	s.touch()
	var closeOnce sync.Once
	closeAll := func() {
		closeOnce.Do(func() {
			_ = client.Close()
			_ = origin.Close()
			_ = originRaw.Close()
		})
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); c.leg(Up, origin, cbr, s, closeAll) }()
	go func() { defer wg.Done(); c.leg(Down, client, obr, s, closeAll) }()
	go func() {
		defer wg.Done()
		tick := 250 * time.Millisecond
		if ic := c.snapshot().IdleCut; ic > 0 && ic/10 < tick {
			tick = max(ic/10, 2*time.Millisecond)
		}
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-c.ctx.Done():
				closeAll()
				return
			case <-t.C:
				ic := c.snapshot().IdleCut
				if ic == 0 {
					ic = DefaultIdleCut
				}
				if ic > 0 && time.Since(time.Unix(0, s.last.Load())) > ic {
					closeAll()
					return
				}
			}
		}
	}()
	wg.Wait()
	close(done)
	closeAll()
}

type closeWriter interface{ CloseWrite() error }

func (c *CDN) leg(dir Direction, dst net.Conn, src io.Reader, s *session, closeAll func()) {
	scan := &frameScanner{}
	buf := make([]byte, 32*1024)
	var sent int64
	for {
		n, err := src.Read(buf)
		if n > 0 && !c.forward(dir, dst, buf[:n], scan, &sent, s) {
			closeAll()
			return
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if cw, ok := dst.(closeWriter); ok {
					_ = cw.CloseWrite()
				}
			} else {
				closeAll()
			}
			return
		}
	}
}

// forward applies the knobs to one chunk read from the source and writes it.
// It reports false when the connection must be closed.
func (c *CDN) forward(dir Direction, dst net.Conn, p []byte, scan *frameScanner, sent *int64, s *session) bool {
	o := c.snapshot()
	for o.PauseOrigin {
		if !c.sleep(10 * time.Millisecond) {
			return false
		}
		o = c.snapshot()
	}
	out, act := scan.feed(p, o.SwallowPings)
	if act.data || (act.ping && o.PingsResetIdle) {
		s.touch()
	}
	if o.DropAll || len(out) == 0 {
		return true
	}
	cut := false
	if o.CutAfterBytes > 0 && (o.CutDir == Both || o.CutDir == dir) {
		rem := o.CutAfterBytes - *sent
		if rem <= 0 {
			return !o.CutClose
		}
		if int64(len(out)) >= rem {
			out = out[:rem]
			cut = true
		}
	}
	if !c.sleep(o.Delay) {
		return false
	}
	for len(out) > 0 {
		k := len(out)
		if o.Chunk > 0 && k > o.Chunk {
			k = o.Chunk
		}
		if _, err := dst.Write(out[:k]); err != nil {
			return false
		}
		out = out[k:]
		*sent += int64(k)
		if dir == Up {
			c.bytesUp.Add(int64(k))
		} else {
			c.bytesDown.Add(int64(k))
		}
		if o.BytesPerSec > 0 && !c.sleep(time.Duration(k)*time.Second/time.Duration(o.BytesPerSec)) {
			return false
		}
	}
	return !cut || !o.CutClose
}
