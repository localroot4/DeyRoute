package health

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Result is the outcome of one probe.
type Result struct {
	// OK reports success according to the probe kind's rules.
	OK bool
	// RTT is the time from the start of the probe (before connecting) to
	// the first response byte; for tcp probes, and for a clean close that
	// AcceptCleanClose turned into a success, it is the connect time. Zero
	// when nothing was measured.
	RTT time.Duration
	// Err is a short reason when OK is false (see the Reason* constants).
	Err string
	// TLS reports that a TLS record (handshake or alert) was received.
	TLS bool
	// ClosedNoData reports that the peer closed the connection cleanly
	// without sending a single byte.
	ClosedNoData bool
}

// PathOptions tunes Path.
type PathOptions struct {
	// SNI is the TLS server name sent by tls/auto probes and the Host
	// header of http probes. Empty: the host of addr when it is a name
	// (no SNI for IP addresses) and addr as Host header.
	SNI string
	// AcceptCleanClose makes an auto probe succeed when the peer closes the
	// connection cleanly without sending anything. It is off by default:
	// through a reverse tunnel the hub-side backend accepts locally and
	// closes at once when the far side is broken, so a clean close proves
	// nothing about the path.
	AcceptCleanClose bool
	// HTTPPath is the request path of http probes (default "/").
	HTTPPath string
}

// Path runs one probe of the given kind (auto|tcp|tls|http; empty = auto)
// against addr (host:port) and gives up after timeout (DefaultTimeout when
// zero or negative).
//
//   - tcp: the TCP connect succeeds.
//   - tls: a full TLS handshake completes, or the server answers with a TLS
//     record (for example an alert). The certificate is not verified: only
//     the reachability of the service is tested.
//   - http: "HEAD <path> HTTP/1.1" gets any HTTP status line back.
//   - auto: a real TLS ClientHello is sent; any TLS record back (ServerHello
//     or alert) is a success (TLS=true), any other byte too (the service is
//     not TLS); a clean close without data sets ClosedNoData and succeeds
//     only with AcceptCleanClose; timeouts, refused and reset connections
//     fail.
func Path(ctx context.Context, addr, kind string, timeout time.Duration, opts PathOptions) Result {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if !validAddr(addr) {
		return Result{Err: ReasonBadAddress}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", KindAuto:
		return tlsProbe(ctx, addr, false, opts)
	case KindTLS:
		return tlsProbe(ctx, addr, true, opts)
	case KindTCP:
		return tcpProbe(ctx, addr)
	case KindHTTP:
		host := opts.SNI
		if host == "" {
			host = addr
		}
		path := opts.HTTPPath
		if path == "" {
			path = "/"
		}
		return httpProbe(ctx, addr, host, path, false, "")
	default:
		return Result{Err: ReasonBadKind}
	}
}

// TCP reports whether a TCP connection to addr can be opened (node_service
// probe: `probe.tcp 127.0.0.1:<target>`).
func TCP(ctx context.Context, addr string, timeout time.Duration) Result {
	return Path(ctx, addr, KindTCP, timeout, PathOptions{})
}

// TLS reports whether a TLS server answers on addr (handshake completed or
// TLS alert received).
func TLS(ctx context.Context, addr, sni string, timeout time.Duration) Result {
	return Path(ctx, addr, KindTLS, timeout, PathOptions{SNI: sni})
}

// HTTP sends "HEAD" for rawURL (http:// or https://; the certificate is not
// verified) and succeeds on any HTTP status line.
func HTTP(ctx context.Context, rawURL string, timeout time.Duration) Result {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return Result{Err: ReasonBadAddress}
	}
	var useTLS bool
	var port string
	switch strings.ToLower(u.Scheme) {
	case "http":
		port = "80"
	case "https":
		useTLS, port = true, "443"
	default:
		return Result{Err: ReasonBadAddress}
	}
	if p := u.Port(); p != "" {
		port = p
	}
	addr := net.JoinHostPort(u.Hostname(), port)
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	sni := ""
	if net.ParseIP(u.Hostname()) == nil {
		sni = u.Hostname()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return httpProbe(ctx, addr, u.Host, path, useTLS, sni)
}

// TCPEcho sends a "DEYE" nonce packet to a TCP echo service (ServeTCPEcho,
// e.g. the canary loopback echo reached through a tunnel) and succeeds only
// when the exact bytes come back. Unlike an auto probe this proves the whole
// path end to end.
func TCPEcho(ctx context.Context, addr string, timeout time.Duration) Result {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if !validAddr(addr) {
		return Result{Err: ReasonBadAddress}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	raw, err := dialTCP(ctx, addr)
	if err != nil {
		return Result{Err: reasonFor(ctx, err)}
	}
	defer closeQuietly(raw)
	setDeadlineFrom(ctx, raw)
	defer interruptOnDone(ctx, raw)()
	pkt := newEchoPacket()
	if _, err := raw.Write(pkt[:]); err != nil {
		return Result{Err: reasonFor(ctx, err)}
	}
	oc := &observedConn{Conn: raw}
	got := make([]byte, len(pkt))
	_, err = io.ReadFull(oc, got)
	obs := oc.snapshot()
	switch {
	case err == nil && string(got) == string(pkt[:]):
		return Result{OK: true, RTT: time.Since(start)}
	case err == nil:
		return Result{Err: ReasonEchoMismatch, RTT: obs.firstAt.Sub(start)}
	case obs.n == 0 && errors.Is(obs.readErr, io.EOF) && ctx.Err() == nil:
		return Result{Err: ReasonClosedNoData, ClosedNoData: true}
	case obs.n > 0 && errors.Is(err, io.ErrUnexpectedEOF):
		return Result{Err: ReasonEchoMismatch, RTT: obs.firstAt.Sub(start)}
	default:
		return Result{Err: reasonFor(ctx, err)}
	}
}

// tcpProbe is the tcp kind: the connect time is the RTT.
func tcpProbe(ctx context.Context, addr string) Result {
	start := time.Now()
	c, err := dialTCP(ctx, addr)
	if err != nil {
		return Result{Err: reasonFor(ctx, err)}
	}
	rtt := time.Since(start)
	closeQuietly(c)
	return Result{OK: true, RTT: rtt}
}

// tlsProbe implements the tls (strict) and auto kinds: a real crypto/tls
// ClientHello is sent over a connection that records what comes back.
func tlsProbe(ctx context.Context, addr string, strict bool, opts PathOptions) Result {
	start := time.Now()
	raw, err := dialTCP(ctx, addr)
	if err != nil {
		return Result{Err: reasonFor(ctx, err)}
	}
	connected := time.Since(start)
	defer closeQuietly(raw)
	setDeadlineFrom(ctx, raw)
	oc := &observedConn{Conn: raw}
	herr := tls.Client(oc, probeTLSConfig(opts.SNI, addr)).HandshakeContext(ctx)
	obs := oc.snapshot()

	var res Result
	if obs.n > 0 {
		res.RTT = obs.firstAt.Sub(start)
	}
	switch {
	case herr == nil:
		res.OK, res.TLS = true, true
		if res.RTT <= 0 {
			res.RTT = time.Since(start)
		}
	case obs.n > 0 && isTLSRecordType(obs.first):
		// ServerHello followed by a failure we do not care about, or an
		// alert ("remote error: tls: …"): a TLS server answered.
		res.OK, res.TLS = true, true
	case obs.n > 0:
		if strict {
			res.Err = ReasonNotTLS
		} else {
			res.OK = true
		}
	case ctx.Err() == nil && (isPeerReset(obs.readErr) || isPeerReset(herr)):
		// The peer sent no byte and the connection ended in a reset (it
		// closed before our ClientHello arrived, or aborted). Recorded as
		// closed-without-data but never a success, even with
		// AcceptCleanClose: only a clean FIN may count.
		res.ClosedNoData = true
		res.Err = ReasonReset
	case errors.Is(obs.readErr, io.EOF) && ctx.Err() == nil:
		res.ClosedNoData = true
		res.RTT = connected
		if !strict && opts.AcceptCleanClose {
			res.OK = true
		} else {
			res.Err = ReasonClosedNoData
		}
	default:
		cause := herr
		if obs.readErr != nil && !errors.Is(obs.readErr, io.EOF) {
			cause = obs.readErr
		}
		res.Err = reasonFor(ctx, cause)
	}
	return res
}

// httpProbe sends a HEAD request (optionally over TLS) and checks the status
// line.
func httpProbe(ctx context.Context, addr, host, path string, useTLS bool, sni string) Result {
	if !validHTTPToken(path) || !strings.HasPrefix(path, "/") || !validHTTPToken(host) {
		return Result{Err: "invalid http request"}
	}
	start := time.Now()
	raw, err := dialTCP(ctx, addr)
	if err != nil {
		return Result{Err: reasonFor(ctx, err)}
	}
	defer closeQuietly(raw)
	setDeadlineFrom(ctx, raw)
	defer interruptOnDone(ctx, raw)()
	conn := raw
	if useTLS {
		oc := &observedConn{Conn: raw}
		tc := tls.Client(oc, probeTLSConfig(sni, addr))
		if err := tc.HandshakeContext(ctx); err != nil {
			obs := oc.snapshot()
			if obs.n == 0 && errors.Is(obs.readErr, io.EOF) && ctx.Err() == nil {
				return Result{Err: ReasonClosedNoData, ClosedNoData: true}
			}
			if obs.n > 0 && !isTLSRecordType(obs.first) {
				return Result{Err: ReasonNotTLS}
			}
			return Result{Err: "tls: " + reasonFor(ctx, err), TLS: obs.n > 0}
		}
		conn = tc
	}
	req := "HEAD " + path + " HTTP/1.1\r\nHost: " + host + "\r\nAccept: */*\r\nConnection: close\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return Result{Err: reasonFor(ctx, err), TLS: useTLS}
	}
	ro := &observedConn{Conn: conn}
	line := readStatusLine(ro)
	obs := ro.snapshot()
	switch {
	case obs.n == 0 && errors.Is(obs.readErr, io.EOF) && ctx.Err() == nil:
		return Result{Err: ReasonClosedNoData, ClosedNoData: true, TLS: useTLS}
	case obs.n == 0:
		return Result{Err: reasonFor(ctx, obs.readErr), TLS: useTLS}
	case isHTTPStatusLine(line):
		return Result{OK: true, RTT: obs.firstAt.Sub(start), TLS: useTLS}
	default:
		return Result{Err: ReasonNotHTTP, RTT: obs.firstAt.Sub(start), TLS: useTLS || isTLSRecordType(obs.first)}
	}
}

// probeTLSConfig is the client configuration of every TLS probe.
func probeTLSConfig(sni, addr string) *tls.Config {
	name := sni
	if name == "" {
		if host, _, err := net.SplitHostPort(addr); err == nil && net.ParseIP(host) == nil {
			name = host
		}
	}
	return &tls.Config{
		ServerName: name,
		// Reachability probe only: whether a TLS server answers matters,
		// not who it is. User TLS is never terminated by the tunnel.
		InsecureSkipVerify: true, //nolint:gosec // G402: see above
		MinVersion:         tls.VersionTLS12,
	}
}

// isTLSRecordType reports a TLS alert (21) or handshake (22) record header.
func isTLSRecordType(b byte) bool { return b == 0x15 || b == 0x16 }

// readStatusLine reads until the first newline (at most 512 bytes), and
// stops early as soon as the bytes cannot start an HTTP response.
func readStatusLine(r io.Reader) []byte {
	const prefix = "HTTP/"
	buf := make([]byte, 0, 512)
	for len(buf) < cap(buf) {
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if bytes.IndexByte(buf, '\n') >= 0 {
			break
		}
		if k := min(len(buf), len(prefix)); string(buf[:k]) != prefix[:k] {
			break
		}
		if err != nil {
			break
		}
	}
	return buf
}

// isHTTPStatusLine accepts "HTTP/1.x NNN ...".
func isHTTPStatusLine(line []byte) bool {
	f := strings.Fields(string(line))
	if len(f) < 2 || !strings.HasPrefix(f[0], "HTTP/") || len(f[1]) != 3 {
		return false
	}
	for _, c := range f[1] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// validHTTPToken rejects whitespace and control characters that would break
// the request line or inject headers.
func validHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c <= ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

// validAddr reports a host:port address with a non-empty port.
func validAddr(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	return err == nil && port != ""
}

// aLongTimeAgo is a deadline in the past that interrupts blocked I/O.
var aLongTimeAgo = time.Unix(1, 0)

// dialTCP opens a TCP connection honouring ctx.
func dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{KeepAlive: -1}
	return d.DialContext(ctx, "tcp", addr)
}

// setDeadlineFrom applies ctx's deadline to c.
func setDeadlineFrom(ctx context.Context, c net.Conn) {
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
}

// interruptOnDone unblocks any pending I/O on c when ctx is done (the
// caller's cancellation, which a deadline cannot express). The returned
// function stops the watcher.
func interruptOnDone(ctx context.Context, c interface{ SetDeadline(time.Time) error }) func() {
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(aLongTimeAgo) })
	return func() { stop() }
}

// closeQuietly closes c; a probe has nothing useful to do with the error.
func closeQuietly(c io.Closer) { _ = c.Close() }

// EchoMagic prefixes every echo packet (UDP and TCP nonce probes). The UDP
// echo server answers only packets that start with it, and replies with
// EchoReplyMagic in its place.
const EchoMagic = "DEYE"

// EchoPacketSize is the size of a probe packet: magic + 16-byte nonce.
const EchoPacketSize = len(EchoMagic) + 16

// newEchoPacket returns "DEYE" followed by a random 16-byte nonce.
func newEchoPacket() [EchoPacketSize]byte {
	var p [EchoPacketSize]byte
	copy(p[:], EchoMagic)
	_, _ = rand.Read(p[len(EchoMagic):]) // never fails since Go 1.24
	return p
}

// observedConn records what the peer sent: the number of bytes, the first
// byte and when it arrived, and the first read error.
type observedConn struct {
	net.Conn
	mu      sync.Mutex
	n       int64
	first   byte
	firstAt time.Time
	readErr error
}

// observation is a snapshot of an observedConn.
type observation struct {
	n       int64
	first   byte
	firstAt time.Time
	readErr error
}

// Read implements net.Conn and records the outcome.
func (c *observedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.mu.Lock()
	if n > 0 {
		if c.n == 0 {
			c.first = p[0]
			c.firstAt = time.Now()
		}
		c.n += int64(n)
	}
	if err != nil && c.readErr == nil {
		c.readErr = err
	}
	c.mu.Unlock()
	return n, err
}

func (c *observedConn) snapshot() observation {
	c.mu.Lock()
	defer c.mu.Unlock()
	return observation{n: c.n, first: c.first, firstAt: c.firstAt, readErr: c.readErr}
}

// reasonFor maps an error to a short reason, preferring the context state
// (the probe's own timeout or the caller's cancellation).
func reasonFor(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return ReasonTimeout
	case errors.Is(ctx.Err(), context.Canceled):
		return ReasonCanceled
	}
	return Classify(err)
}

// Classify turns a network error into a short, stable reason such as
// "timeout", "connection refused" or "connection reset". Unknown errors keep
// their innermost message (at most 120 characters).
func Classify(err error) string {
	if err == nil {
		return ""
	}
	var dnsErr *net.DNSError
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return ReasonCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return ReasonTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ReasonRefused
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.ECONNABORTED):
		return ReasonReset
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH),
		errors.Is(err, syscall.EHOSTDOWN), errors.Is(err, syscall.ENETDOWN):
		return ReasonUnreachable
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	case errors.As(err, &dnsErr):
		return "dns: " + dnsErr.Err
	case errors.As(err, &ne) && ne.Timeout():
		return ReasonTimeout
	}
	inner := err
	for {
		next := errors.Unwrap(inner)
		if next == nil {
			break
		}
		inner = next
	}
	msg := strings.TrimSpace(inner.Error())
	if len(msg) > 120 {
		msg = strings.ToValidUTF8(msg[:120], "")
	}
	return msg
}

// isPeerReset reports a reset/broken pipe caused by the peer closing first.
func isPeerReset(err error) bool {
	return err != nil && (errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE))
}
