package front

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"
)

// The data plane of front mode: a backend client on the node (Backhaul,
// rathole, FRP ...) dials 127.0.0.1:<control port>, where the shim
// (RunShim) listens. For every accepted connection the shim opens a
// WebSocket GET /<secret>/t/<port> through the CDN, runs the INNER TLS inside
// it (the hub's control certificate, pinned to the deyroute CA, so the CDN
// only sees ciphertext) and sends a preface: who it is and an HMAC with the
// tunnel token. The hub (DataHandler) checks the preface, connects to the
// backend server on 127.0.0.1:<port> and answers one status byte; then both
// ends copy bytes, half-close carried by TLS close_notify.
//
// Preface (inside the inner TLS), all integers big endian:
//
//	ver(1)=1 | len(node)(1) | node | ts(8, unix seconds) | nonce(16) | mac(32)
//	mac = HMAC-SHA256(token, "dey-front-v1" | port(2) | node | ts | nonce)

// Data status bytes, the hub's answer to a preface.
const (
	DataOK          byte = 0 // the backend server is connected
	DataRefused     byte = 1 // the preface failed (port, node, MAC, time or replay)
	DataUnreachable byte = 2 // the backend server on the hub did not accept
)

const (
	controlPath     = "c"
	dataPrefix      = "t"
	prefaceVersion  = 1
	prefaceNonceLen = 16
	prefaceMACLen   = sha256.Size
	prefaceLabel    = "dey-front-v1"
	// PrefaceWindow is how far the preface time may be from the hub's clock.
	PrefaceWindow = 2 * time.Minute
	// maxNodeIDLen bounds the node id in a preface.
	maxNodeIDLen = 64
	// DefaultMaxDataConns bounds the hub's concurrent data connections.
	DefaultMaxDataConns = 4096
	// dataSetupTimeout bounds the inner TLS handshake, the preface and the
	// backend dial on the hub, and the same steps on the shim.
	dataSetupTimeout = 15 * time.Second
	// backendDialTimeout bounds the hub's dial of the backend server.
	backendDialTimeout = 5 * time.Second
)

// dataPath is the path segment after the secret of a data connection.
func dataPath(port int) string { return dataPrefix + "/" + strconv.Itoa(port) }

// dataPort returns the port of a /<secret>/t/<port> path (segments after the
// secret), or 0. The number must be canonical: 1-65535, no sign, no leading
// zero.
func dataPort(segs []string) int {
	if len(segs) != 3 || segs[1] != dataPrefix {
		return 0
	}
	s := segs[2]
	if s == "" || len(s) > 5 || s[0] == '0' {
		return 0
	}
	n := 0
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if n > 65535 {
		return 0
	}
	return n
}

// Preface is the decoded first message of a data connection.
type Preface struct {
	Node  string
	Time  time.Time
	nonce [prefaceNonceLen]byte
	mac   [prefaceMACLen]byte
}

func prefaceMAC(token string, port int, node string, ts int64, nonce []byte) []byte {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(prefaceLabel))
	var b [10]byte
	binary.BigEndian.PutUint16(b[:2], uint16(port)) // #nosec G115 -- a port
	m.Write(b[:2])
	m.Write([]byte(node))
	binary.BigEndian.PutUint64(b[2:], uint64(ts)) // #nosec G115 -- unix seconds
	m.Write(b[2:])
	m.Write(nonce)
	return m.Sum(nil)
}

// EncodePreface returns the preface for port, signed with the tunnel token.
func EncodePreface(token string, port int, node string, now time.Time) ([]byte, error) {
	if node == "" || len(node) > maxNodeIDLen {
		return nil, errors.New("front: invalid node id for the preface")
	}
	var nonce [prefaceNonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	ts := now.Unix()
	var b bytes.Buffer
	b.WriteByte(prefaceVersion)
	b.WriteByte(byte(len(node)))
	b.WriteString(node)
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], uint64(ts)) // #nosec G115 -- unix seconds
	b.Write(t[:])
	b.Write(nonce[:])
	b.Write(prefaceMAC(token, port, node, ts, nonce[:]))
	return b.Bytes(), nil
}

// ReadPreface reads a preface from r (it does not verify it).
func ReadPreface(r io.Reader) (Preface, error) {
	var p Preface
	var head [2]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return p, err
	}
	if head[0] != prefaceVersion {
		return p, fmt.Errorf("front: preface version %d", head[0])
	}
	n := int(head[1])
	if n == 0 || n > maxNodeIDLen {
		return p, errors.New("front: preface node id length")
	}
	rest := make([]byte, n+8+prefaceNonceLen+prefaceMACLen)
	if _, err := io.ReadFull(r, rest); err != nil {
		return p, err
	}
	p.Node = string(rest[:n])
	p.Time = time.Unix(int64(binary.BigEndian.Uint64(rest[n:n+8])), 0) // #nosec G115 -- unix seconds
	copy(p.nonce[:], rest[n+8:])
	copy(p.mac[:], rest[n+8+prefaceNonceLen:])
	return p, nil
}

// Verify checks the MAC of p for port with token and that its time is
// within PrefaceWindow of now.
func (p Preface) Verify(token string, port int, now time.Time) bool {
	if d := now.Sub(p.Time); d > PrefaceWindow || d < -PrefaceWindow {
		return false
	}
	want := prefaceMAC(token, port, p.Node, p.Time.Unix(), p.nonce[:])
	return hmac.Equal(want, p.mac[:])
}

// replayCache remembers the nonces of accepted prefaces for twice the
// window, so a recorded preface cannot be played again.
type replayCache struct {
	mu   sync.Mutex
	seen map[[prefaceNonceLen]byte]time.Time
	max  int
}

func newReplayCache(max int) *replayCache {
	return &replayCache{seen: map[[prefaceNonceLen]byte]time.Time{}, max: max}
}

// fresh records nonce and reports whether it was not seen before.
func (c *replayCache) fresh(nonce [prefaceNonceLen]byte, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[nonce]; ok {
		return false
	}
	if len(c.seen) >= c.max {
		for k, t := range c.seen {
			if now.Sub(t) > 2*PrefaceWindow {
				delete(c.seen, k)
			}
		}
		if len(c.seen) >= c.max {
			return false // flooded with valid prefaces: refuse rather than forget
		}
	}
	c.seen[nonce] = now
	return true
}

// DataAuthorizer returns the tunnel token that signs data connections of
// node for the backend control port port, or an error when node may not use
// that port.
type DataAuthorizer func(port int, node string) (token string, err error)

// DataHandlerConfig configures a DataHandler.
type DataHandlerConfig struct {
	// TLS is the hub's inner (control) server config.
	TLS *tls.Config
	// Authorize decides which port a node may reach and with which token.
	Authorize DataAuthorizer
	// Dial connects to the backend server of port (nil = 127.0.0.1:port).
	Dial func(ctx context.Context, port int) (net.Conn, error)
	// Logger receives warnings; nil discards them.
	Logger *slog.Logger
	// Now is the clock (nil = time.Now).
	Now func() time.Time
}

// DataHandler serves upgraded /<secret>/t/<port> connections on the hub.
type DataHandler struct {
	c      DataHandlerConfig
	replay *replayCache
	log    *slog.Logger
}

// NewDataHandler returns a handler for cfg.
func NewDataHandler(cfg DataHandlerConfig) *DataHandler {
	h := &DataHandler{c: cfg, replay: newReplayCache(1 << 16), log: cfg.Logger}
	if h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	if h.c.Now == nil {
		h.c.Now = time.Now
	}
	if h.c.Dial == nil {
		h.c.Dial = func(ctx context.Context, port int) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		}
	}
	return h
}

// Serve runs one data connection to its end; it owns c.
func (h *DataHandler) Serve(c net.Conn, port int) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(dataSetupTimeout))
	tc := tls.Server(c, h.c.TLS)
	if err := tc.Handshake(); err != nil {
		h.log.Debug("front data: inner TLS handshake failed", "port", port, "error", err)
		return
	}
	p, err := ReadPreface(tc)
	if err != nil {
		h.log.Debug("front data: no valid preface", "port", port, "error", err)
		return
	}
	now := h.c.Now()
	token, err := h.c.Authorize(port, p.Node)
	if err != nil || !p.Verify(token, port, now) || !h.replay.fresh(p.nonce, now) {
		h.log.Warn("front data: connection refused (unknown port, wrong node, bad signature, clock skew or replay)",
			"port", port, "node", p.Node)
		_, _ = tc.Write([]byte{DataRefused})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), backendDialTimeout)
	b, err := h.c.Dial(ctx, port)
	cancel()
	if err != nil {
		h.log.Warn("front data: the backend server is not reachable", "port", port, "node", p.Node, "error", err)
		_, _ = tc.Write([]byte{DataUnreachable})
		return
	}
	defer func() { _ = b.Close() }()
	if _, err := tc.Write([]byte{DataOK}); err != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	pipe(tc, b)
}

// pipe copies between the inner TLS connection t and the plain TCP
// connection p until both directions ended. A clean end of one direction
// (close_notify from t, EOF from p) is passed on as a half-close; any other
// end aborts both.
func pipe(t *tls.Conn, p net.Conn) {
	var wg sync.WaitGroup
	var once sync.Once
	abort := func() {
		once.Do(func() {
			if tcp, ok := p.(*net.TCPConn); ok {
				_ = tcp.SetLinger(0) // reset: the peer must see the stream was cut
			}
			_ = p.Close()
			_ = t.Close()
		})
	}
	wg.Add(2)
	go func() { // t -> p
		defer wg.Done()
		_, err := io.Copy(p, t)
		if err != nil { // io.Copy reports a clean close_notify as nil
			abort()
			return
		}
		if cw, ok := p.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() { // p -> t
		defer wg.Done()
		_, err := io.Copy(t, p)
		if err != nil {
			abort()
			return
		}
		_ = t.CloseWrite()
	}()
	wg.Wait()
	_ = t.Close()
	_ = p.Close()
}
