package front

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/wsconn"
)

// Dial budgets. The TCP connect has its own timeout; the upgrade (outer TLS
// plus the HTTP exchange) has one more even when the caller's context has no
// deadline, so a black-holed or silent edge can never hang a reconnect.
const (
	defaultDialTimeout    = 10 * time.Second
	defaultUpgradeTimeout = 20 * time.Second
	tcpKeepAlive          = 30 * time.Second
	maxResponseHead       = 8 << 10 // the whole answer head, status line included
	maxErrorBody          = 2 << 10 // read of a non-101 body, to find "Error 1xxx"
)

// userAgent is the constant browser-like User-Agent of the upgrade request.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Dialer opens control connections through a front. The zero value is what
// DialControl uses. The fields exist for tests and for callers that must
// trust another root pool; none of them can turn off certificate
// verification.
type Dialer struct {
	// RootCAs verifies the outer TLS certificate (nil = the system roots).
	RootCAs *x509.CertPool
	// DialTimeout bounds the TCP connect (0 = 10 s).
	DialTimeout time.Duration
	// UpgradeTimeout bounds the outer TLS handshake and the HTTP upgrade
	// together (0 = 20 s), also when the context has no deadline.
	UpgradeTimeout time.Duration
	// DialContext replaces the TCP dial (tests); nil = a net.Dialer with
	// Timeout DialTimeout, KeepAlive 30 s and dual-stack fallback. Proxy
	// environment variables are never consulted either way.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// DialControl connects to the front t and returns the upgraded connection,
// whose bytes are the payload of binary WebSocket frames. It is the control
// dial: GET /<secret>/c. Failures are *DialError.
func DialControl(ctx context.Context, t Target) (net.Conn, error) {
	var d Dialer
	return d.DialControl(ctx, t)
}

// DialControl is the method form of the package-level DialControl.
func (d *Dialer) DialControl(ctx context.Context, t Target) (net.Conn, error) {
	fail := func(class, reason string, err error) error {
		return &DialError{Class: class, Reason: reason, Addr: t.Addr(), Err: err}
	}
	if err := ctx.Err(); err != nil {
		return nil, fail(ClassDial, ctxReason(err), err)
	}
	raw, err := d.dialTCP(ctx, t)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fail(ClassDial, ctxReason(ctx.Err()), ctx.Err())
		}
		return nil, fail(ClassDial, dialReason(err, t.Host), err)
	}

	budget := d.UpgradeTimeout
	if budget <= 0 {
		budget = defaultUpgradeTimeout
	}
	uctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// The deadline on the socket and the close on expiry make every blocking
	// step below (TLS, write, read) end when the budget does.
	if dl, ok := uctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	}
	stop := context.AfterFunc(uctx, func() { _ = raw.Close() })
	// The socket deadline, the close on expiry and the context all end together;
	// give the context a moment to record it so a failure caused by the budget
	// or by the caller is reported as such, not as a vague I/O error.
	conclude := func(class string, e *DialError) error {
		stop()
		_ = raw.Close()
		if isTimeoutErr(e.Err) || errors.Is(e.Err, net.ErrClosed) {
			select {
			case <-uctx.Done():
			case <-time.After(100 * time.Millisecond):
			}
		}
		switch {
		case ctx.Err() != nil:
			return fail(class, ctxReason(ctx.Err()), ctx.Err())
		case uctx.Err() != nil && e.Status == 0:
			if class == ClassTLS {
				return fail(class, "the TLS handshake timed out (a middlebox may be dropping it)", e.Err)
			}
			return fail(class, "timed out waiting for the front to answer", e.Err)
		}
		return e
	}

	conn := raw
	if t.TLS {
		tc := tls.Client(raw, d.tlsConfig(t))
		if err := tc.HandshakeContext(uctx); err != nil {
			return nil, conclude(ClassTLS, &DialError{Class: ClassTLS, Reason: tlsReason(err, t.Host), Addr: t.Addr(), Err: err})
		}
		conn = tc
	}
	br, derr := upgrade(conn, t)
	if derr != nil {
		return nil, conclude(derr.Class, derr)
	}
	if !stop() { // the budget or the context ended while the answer was read
		_ = raw.Close()
		if ctx.Err() != nil {
			return nil, fail(ClassDial, ctxReason(ctx.Err()), ctx.Err())
		}
		return nil, fail(ClassDial, "timed out waiting for the front to answer", context.DeadlineExceeded)
	}
	_ = raw.SetDeadline(time.Time{})
	return wsconn.New(conn, br, wsconn.Config{Client: true}), nil
}

// dialTCP opens the TCP connection to the edge address (or the host) with
// keepalive, a connect timeout and dual-stack fallback.
func (d *Dialer) dialTCP(ctx context.Context, t Target) (net.Conn, error) {
	if d.DialContext != nil {
		return d.DialContext(ctx, "tcp", t.dialAddr())
	}
	timeout := d.DialTimeout
	if timeout <= 0 {
		timeout = defaultDialTimeout
	}
	nd := net.Dialer{Timeout: timeout, KeepAlive: tcpKeepAlive}
	return nd.DialContext(ctx, "tcp", t.dialAddr())
}

// upgrade performs the HTTP/1.1 WebSocket upgrade on conn and returns the
// reader that holds whatever followed the answer head.
func upgrade(conn net.Conn, t Target) (*bufio.Reader, *DialError) {
	fail := func(class string, status int, reason string, err error) *DialError {
		return &DialError{Class: class, Status: status, Reason: reason, Addr: t.Addr(), Err: err}
	}
	key := wsconn.NewKey()
	if _, err := io.WriteString(conn, upgradeRequest(t, key)); err != nil {
		return nil, fail(ClassDial, 0, ioReason(err), err)
	}
	cr := &capReader{r: conn, n: maxResponseHead}
	br := bufio.NewReaderSize(cr, 4096)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		if errors.Is(err, errHeadTooLarge) {
			return nil, fail(ClassProtocol, 0, "the front's answer head is larger than 8 KiB: not a hub front", err)
		}
		if isTransportErr(err) {
			return nil, fail(ClassDial, 0, ioReason(err), err)
		}
		return nil, fail(ClassProtocol, 0, "the front answered something that is not HTTP", errors.New("malformed HTTP answer"))
	}
	status := resp.StatusCode
	if status != http.StatusSwitchingProtocols {
		cr.n = maxErrorBody + 4096 // body bytes still to come
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		challenge := resp.Header.Get("Cf-Mitigated") != ""
		retry := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		code := cfErrorCode(body)
		return nil, &DialError{
			Class: ClassStatus, Status: status, Addr: t.Addr(), CFCode: code, Challenge: challenge, retry: retry,
			Reason: statusReason(status, challenge, code, retry),
			Err:    errors.New("HTTP " + strconv.Itoa(status)),
		}
	}
	// 101: validate it strictly.
	switch {
	case !headerHasToken(resp.Header, "Upgrade", "websocket"), !headerHasToken(resp.Header, "Connection", "upgrade"):
		return nil, fail(ClassProtocol, status, "the 101 answer does not upgrade to a WebSocket", errors.New("bad upgrade headers"))
	case resp.Header.Get("Sec-WebSocket-Accept") != wsconn.AcceptKey(key):
		return nil, fail(ClassProtocol, status, "the 101 answer has a wrong Sec-WebSocket-Accept: not the hub front, or a proxy rewrote the handshake", errors.New("bad accept key"))
	case len(resp.Header.Values("Sec-WebSocket-Extensions")) > 0, len(resp.Header.Values("Sec-WebSocket-Protocol")) > 0:
		return nil, fail(ClassProtocol, status, "the 101 answer negotiates a WebSocket extension or subprotocol that was not offered", errors.New("unexpected negotiation"))
	}
	cr.n = -1 // the cap is for the head only; the stream that follows is unlimited
	return br, nil
}

// upgradeRequest renders the request. It is written by hand so the bytes are
// exactly what a browser-like client sends and nothing else: no
// Sec-WebSocket-Extensions or -Protocol, no cf-* headers.
func upgradeRequest(t Target, key string) string {
	var b strings.Builder
	b.WriteString("GET /" + t.Secret + "/c HTTP/1.1\r\n")
	b.WriteString("Host: " + t.hostHeader() + "\r\n")
	b.WriteString("User-Agent: " + userAgent + "\r\n")
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	b.WriteString("Sec-WebSocket-Key: " + key + "\r\n")
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	b.WriteString("Origin: " + t.origin() + "\r\n")
	b.WriteString("\r\n")
	return b.String()
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

// isTransportErr reports whether err is an I/O failure (as opposed to a
// parse failure of what arrived).
func isTransportErr(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isTimeoutErr(err) {
		return true
	}
	var ne *net.OpError
	return errors.As(err, &ne)
}

var errHeadTooLarge = errors.New("front: answer head too large")

// capReader fails with errHeadTooLarge once n bytes were read. n < 0 turns the
// cap off.
type capReader struct {
	r io.Reader
	n int
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n < 0 {
		return c.r.Read(p)
	}
	if c.n == 0 {
		return 0, errHeadTooLarge
	}
	if len(p) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= n
	return n, err
}
