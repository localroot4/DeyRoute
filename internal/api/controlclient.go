package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Node client defaults.
const (
	// DefaultMaxConcurrentCommands bounds commands running at once on a node.
	DefaultMaxConcurrentCommands = 16
	// DefaultJoinTimeout bounds Join when ctx has no deadline.
	DefaultJoinTimeout = 30 * time.Second
	dialTimeout        = 10 * time.Second
)

// CommandHandler executes one hub command on the node. stream sends log
// lines to the hub (logs.tail with Follow); data is marshalled into the
// Result. ctx ends when the hub cancels the command, its timeout passes or
// the connection drops. Handlers run concurrently (at most 16 at once).
type CommandHandler func(ctx context.Context, cmd Command, stream func(lines []string)) (data any, err error)

// AssetMeta describes a downloaded asset.
type AssetMeta struct {
	Size    int64
	SHA256  string
	Version string
}

// ControlClient is the node side of the Control API: one outbound HTTP/2
// stream to the hub, re-established with exponential backoff (1 s → 30 s
// with jitter), hello first, a heartbeat every 5 s, pings answered and hub
// commands executed through Handler.
type ControlClient struct {
	// HubAddr is the hub control address "ip:port".
	HubAddr string
	// AddrFunc, when set, is consulted on every (re)connect instead of
	// HubAddr (node set-hub / hub announce-move).
	AddrFunc func() string
	// TLSConfig is tlsutil.ClientTLSConfig(ca, nodeCert, nodeKey, "").
	TLSConfig *tls.Config
	// Hello returns the node's hello (sent first on every connection).
	Hello func() Hello
	// Heartbeat returns the current heartbeat (sent every HeartbeatInterval).
	Heartbeat func() Heartbeat
	// Handler executes hub commands; nil answers DEY-X008.
	Handler CommandHandler
	Logger  *slog.Logger
	// OnConnected / OnDisconnected are called when the stream opens / ends.
	OnConnected    func()
	OnDisconnected func()
	// OnHubHello is called with the hub's hello (version, Compatible).
	OnHubHello func(Hello)

	// BackoffMin / BackoffMax bound the reconnect delay (defaults 1 s / 30 s).
	BackoffMin, BackoffMax time.Duration
	// HeartbeatInterval defaults to 5 s.
	HeartbeatInterval time.Duration
	// MaxConcurrent bounds concurrently running commands (default 16).
	MaxConcurrent int
	// OpenTimeout bounds dial, handshake and the hub's answer to the stream
	// request (default 20 s).
	OpenTimeout time.Duration
}

func (c *ControlClient) logger() *slog.Logger {
	if c.Logger == nil {
		return dlog.Discard()
	}
	return c.Logger
}

func (c *ControlClient) addr() string {
	if c.AddrFunc != nil {
		if a := c.AddrFunc(); a != "" {
			return a
		}
	}
	return c.HubAddr
}

// openTimeout bounds opening the stream: dial, TLS handshake and the hub's
// response headers.
func (c *ControlClient) openTimeout() time.Duration {
	if c.OpenTimeout > 0 {
		return c.OpenTimeout
	}
	return dialTimeout + DefaultHandshakeTimeout
}

func (c *ControlClient) hello() Hello {
	if c.Hello == nil {
		return Hello{}
	}
	return c.Hello()
}

// newTransport returns an HTTP/2 transport that dials TLS with cfg (ALPN
// "deyroute/1"; the "h2" ALPN check of x/net is bypassed on purpose).
func newTransport(cfg *tls.Config, lastDialErr *dialErrBox) *http2.Transport {
	return &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}, Config: cfg.Clone()}
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil && lastDialErr != nil {
				lastDialErr.set(err)
			}
			return conn, err
		},
		ReadIdleTimeout:    h2ReadIdle,
		PingTimeout:        h2PingTimeout,
		DisableCompression: true,
	}
}

// dialErrBox keeps the last dial error (the transport may wrap it).
type dialErrBox struct {
	mu  sync.Mutex
	err error
}

func (b *dialErrBox) set(err error) {
	b.mu.Lock()
	b.err = err
	b.mu.Unlock()
}

func (b *dialErrBox) get() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

// netErr maps a transport failure to DEY errors: a DEY error from the TLS
// verification (DEY-N002) is kept, anything else is DEY-N009 {addr}.
func netErr(addr string, err error, box *dialErrBox) error {
	for _, e := range []error{err, box.get()} {
		if e == nil {
			continue
		}
		var de *deyerr.Error
		if errors.As(e, &de) {
			return de
		}
	}
	return deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": addr})
}

// Run keeps a stream to the hub open until ctx is done (then returns nil),
// reconnecting with exponential backoff and jitter.
func (c *ControlClient) Run(ctx context.Context) error {
	if c.TLSConfig == nil {
		return deyerr.Wrap(deyerr.X000, errors.New("control client needs TLSConfig"), nil)
	}
	minB, maxB := c.BackoffMin, c.BackoffMax
	if minB <= 0 {
		minB = ReconnectMin
	}
	if maxB < minB {
		maxB = max(ReconnectMax, minB)
	}
	backoff := minB
	for {
		start := time.Now()
		connected, err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		// Only a stream that stayed up resets the backoff; a hub that
		// accepts and immediately drops the stream (refused hello, crash
		// loop) is retried with a growing delay like an unreachable one.
		stable := connected && time.Since(start) >= maxB
		if stable {
			backoff = minB
		}
		delay := jitter(backoff, minB)
		c.logger().Info("control: connection to hub ended; reconnecting",
			slog.String("hub", c.addr()), slog.Duration("in", delay), dlog.Err(err))
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		if !stable {
			backoff = min(backoff*2, maxB)
		}
	}
}

// jitter returns d reduced by up to 20 %, never below floor.
func jitter(d, floor time.Duration) time.Duration {
	j := time.Duration(rand.Int64N(int64(d)/5 + 1)) // #nosec G404 -- jitter only, not security relevant
	return max(d-j, floor)
}

// clientConn is the state of one stream.
type clientConn struct {
	c      *ControlClient
	ctx    context.Context
	cancel context.CancelFunc
	out    chan NodeMessage
	log    *slog.Logger
	sem    chan struct{}

	mu       sync.Mutex
	running  map[string]context.CancelFunc
	hubHello *Hello
	wg       sync.WaitGroup
}

// runOnce opens one stream and serves it until it ends. connected reports
// whether the hub accepted the stream.
func (c *ControlClient) runOnce(parent context.Context) (connected bool, err error) {
	addr := c.addr()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	box := &dialErrBox{}
	tr := newTransport(c.TLSConfig, box)
	defer tr.CloseIdleConnections()

	maxc := c.MaxConcurrent
	if maxc <= 0 {
		maxc = DefaultMaxConcurrentCommands
	}
	cc := &clientConn{
		c: c, ctx: ctx, cancel: cancel,
		out:     make(chan NodeMessage, 256),
		log:     c.logger(),
		sem:     make(chan struct{}, maxc),
		running: map[string]context.CancelFunc{},
	}
	pr, pw := io.Pipe()

	h := c.hello()
	cc.out <- NodeMessage{Type: MsgHello, Hello: &h}
	cc.wg.Add(1)
	go func() {
		defer cc.wg.Done()
		cc.writeLoop(pw)
	}()
	// Deferred in reverse: cancel everything, unblock a writer stuck on the
	// pipe, then wait for writer, heartbeat and commands.
	defer cc.wg.Wait()
	defer func() { _ = pr.CloseWithError(io.ErrClosedPipe) }()
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+PathStream, pr)
	if err != nil {
		return false, deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": addr})
	}
	req.Header.Set("Content-Type", ContentTypeNDJSON)
	// A hub that accepts the connection but never answers the stream
	// request must not hold the node forever: give up after the dial and
	// handshake budget and reconnect.
	var hdrTimeout atomic.Bool
	hdrTimer := time.AfterFunc(c.openTimeout(), func() {
		hdrTimeout.Store(true)
		cancel()
	})
	resp, err := tr.RoundTrip(req)
	if !hdrTimer.Stop() && err == nil {
		// The timer fired just as the answer arrived: ctx is cancelled.
		_ = resp.Body.Close()
		err = context.DeadlineExceeded
	}
	if err != nil {
		if hdrTimeout.Load() && parent.Err() == nil {
			return false, deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": addr}).
				WithDetail("the hub did not answer the control stream request in time")
		}
		return false, netErr(addr, err, box)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return false, readControlResponse(resp, nil)
	}

	c.logger().Info("control: connected to hub", slog.String("hub", addr))
	if c.OnConnected != nil {
		c.OnConnected()
	}
	defer func() {
		if c.OnDisconnected != nil {
			c.OnDisconnected()
		}
	}()
	cc.wg.Add(1)
	go func() {
		defer cc.wg.Done()
		cc.heartbeatLoop()
	}()
	lr := newLineReader(resp.Body, MaxControlLine)
	for {
		line, err := lr.next()
		if err != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return true, deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": addr})
		}
		var m HubMessage
		if err := json.Unmarshal(line, &m); err != nil {
			return true, deyerr.Wrap(deyerr.N015, err, deyerr.Params{"node": h.NodeID, "reason": "invalid message from hub"})
		}
		cc.handle(m)
	}
}

// send queues a message for the hub; false when the stream ended.
func (cc *clientConn) send(m NodeMessage) bool {
	select {
	case cc.out <- m:
		return true
	case <-cc.ctx.Done():
		return false
	}
}

func (cc *clientConn) writeLoop(pw *io.PipeWriter) {
	defer func() { _ = pw.Close() }()
	for {
		select {
		case m := <-cc.out:
			if err := writeJSONLine(pw, m); err != nil {
				cc.cancel()
				return
			}
		case <-cc.ctx.Done():
			return
		}
	}
}

func (cc *clientConn) heartbeatLoop() {
	every := cc.c.HeartbeatInterval
	if every <= 0 {
		every = HeartbeatInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		hb := Heartbeat{}
		if cc.c.Heartbeat != nil {
			hb = cc.c.Heartbeat()
		}
		if hb.At.IsZero() {
			hb.At = time.Now().UTC()
		}
		if !cc.send(NodeMessage{Type: MsgHeartbeat, Heartbeat: &hb}) {
			return
		}
		select {
		case <-t.C:
		case <-cc.ctx.Done():
			return
		}
	}
}

func (cc *clientConn) handle(m HubMessage) {
	switch m.Type {
	case MsgPing:
		cc.send(NodeMessage{Type: MsgPong, PingID: m.PingID})
	case MsgHello:
		if m.Hello == nil {
			return
		}
		h := *m.Hello
		cc.mu.Lock()
		cc.hubHello = &h
		cc.mu.Unlock()
		if cc.c.OnHubHello != nil {
			cc.c.OnHubHello(h)
		}
	case MsgCancel:
		cc.mu.Lock()
		cancel := cc.running[m.Cancel]
		cc.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case MsgCommand:
		if m.Command == nil || m.Command.ID == "" {
			return
		}
		cc.startCommand(*m.Command)
	default:
		cc.log.Debug("control: unknown message type from hub ignored", slog.String("type", m.Type))
	}
}

func (cc *clientConn) startCommand(cmd Command) {
	ctx, cancel := context.WithCancel(cc.ctx)
	if cmd.TimeoutMs > 0 {
		ctx, cancel = context.WithTimeout(cc.ctx, time.Duration(cmd.TimeoutMs)*time.Millisecond)
	}
	cc.mu.Lock()
	if old := cc.running[cmd.ID]; old != nil {
		cc.mu.Unlock()
		cancel()
		cc.log.Warn("control: duplicate command id ignored", slog.String("command_id", cmd.ID))
		return
	}
	cc.running[cmd.ID] = cancel
	cc.mu.Unlock()
	cc.wg.Add(1)
	go func() {
		defer cc.wg.Done()
		defer func() {
			cc.mu.Lock()
			delete(cc.running, cmd.ID)
			cc.mu.Unlock()
			cancel()
		}()
		res := cc.runCommand(ctx, cmd)
		cc.send(NodeMessage{Type: MsgResult, Result: res})
	}()
}

// runCommand waits for a slot, runs the handler and builds the Result.
func (cc *clientConn) runCommand(ctx context.Context, cmd Command) *Result {
	node := cc.c.hello().NodeID
	fail := func(err error) *Result {
		return &Result{ID: cmd.ID, OK: false, Error: ToDTO(err)}
	}
	select {
	case cc.sem <- struct{}{}:
		defer func() { <-cc.sem }()
	case <-ctx.Done():
		return fail(deyerr.Wrap(deyerr.N014, ctx.Err(), deyerr.Params{"node": node, "command": cmd.Name}))
	}
	cc.mu.Lock()
	hub := cc.hubHello
	cc.mu.Unlock()
	if hub != nil && !hub.Compatible && cmd.Name != CmdSelfUpdate {
		return fail(deyerr.New(deyerr.N004, deyerr.Params{"node": node, "node_version": cc.c.hello().Version, "hub_version": hub.Version}))
	}
	if cc.c.Handler == nil {
		return fail(deyerr.New(deyerr.X008, deyerr.Params{"feature": "node command " + cmd.Name}))
	}
	var streamed atomic.Bool
	stream := func(lines []string) {
		if len(lines) == 0 || ctx.Err() != nil {
			return
		}
		streamed.Store(true)
		cp := append([]string(nil), lines...)
		cc.send(NodeMessage{Type: MsgLog, Log: &LogChunk{CommandID: cmd.ID, Lines: cp}})
	}
	data, err := cc.invoke(ctx, cmd, stream)
	if streamed.Load() {
		cc.send(NodeMessage{Type: MsgLog, Log: &LogChunk{CommandID: cmd.ID, EOF: true}})
	}
	if err != nil {
		var de *deyerr.Error
		if !errors.As(err, &de) {
			if ctx.Err() != nil {
				// The handler gave up because of the command deadline or a
				// hub cancel: report it as such, not as an unexpected error.
				p := deyerr.Params{"node": node, "command": cmd.Name}
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return fail(deyerr.Wrap(deyerr.N005, err, p))
				}
				return fail(deyerr.Wrap(deyerr.N014, err, p))
			}
			// A plain error has no DEY code: send its (redacted) text without
			// a code so that the hub reports DEY-N011 with that detail instead
			// of an information-free DEY-X000.
			cc.log.Warn("control: command failed", slog.String("command", cmd.Name), dlog.Err(err))
			return &Result{ID: cmd.ID, OK: false, Error: &ErrorDTO{Message: dlog.Redact(err.Error())}}
		}
		if de.Code == deyerr.X000 {
			cc.log.Error("control: command failed unexpectedly", slog.String("command", cmd.Name),
				dlog.Err(err), dlog.Code(deyerr.X000))
		}
		return fail(err)
	}
	res := &Result{ID: cmd.ID, OK: true}
	if data != nil {
		raw, merr := json.Marshal(data)
		if merr != nil {
			return fail(deyerr.Wrap(deyerr.X000, fmt.Errorf("marshal %s result: %w", cmd.Name, merr), nil))
		}
		res.Data = raw
	}
	return res
}

// invoke calls the handler, turning a panic into DEY-X000.
func (cc *clientConn) invoke(ctx context.Context, cmd Command, stream func([]string)) (data any, err error) {
	defer func() {
		if p := recover(); p != nil {
			cc.log.Error("control: command handler panicked", slog.String("command", cmd.Name),
				slog.String("panic", fmt.Sprint(p)), slog.String("stack", string(debug.Stack())), dlog.Code(deyerr.X000))
			data = nil
			err = deyerr.Wrap(deyerr.X000, fmt.Errorf("panic in %s: %v", cmd.Name, p), nil)
		}
	}()
	return cc.c.Handler(ctx, cmd, stream)
}

// request performs one non-stream request with the node certificate.
func (c *ControlClient) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, *http2.Transport, error) {
	addr := c.addr()
	if c.TLSConfig == nil {
		return nil, nil, deyerr.Wrap(deyerr.X000, errors.New("control client needs TLSConfig"), nil)
	}
	box := &dialErrBox{}
	tr := newTransport(c.TLSConfig, box)
	req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, body)
	if err != nil {
		tr.CloseIdleConnections()
		return nil, nil, deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": addr})
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		tr.CloseIdleConnections()
		return nil, nil, netErr(addr, err, box)
	}
	return resp, tr, nil
}

// Upload sends r as the payload of pending upload id (POST /v1/upload/{id}).
func (c *ControlClient) Upload(ctx context.Context, id string, r io.Reader) error {
	if !uploadIDRe.MatchString(id) {
		return protoErr(c.hello().NodeID, "invalid upload id")
	}
	resp, tr, err := c.request(ctx, http.MethodPost, PathUploadPrefix+id, r)
	if err != nil {
		return err
	}
	defer tr.CloseIdleConnections()
	defer func() { _ = resp.Body.Close() }()
	return readControlResponse(resp, nil)
}

// FetchAsset downloads the hub's deyroute binary for arch into w
// (GET /v1/assets/deyroute/{arch}), verifying the X-Deyroute-Sha256 header while
// streaming; a mismatch or missing checksum is DEY-S001. w has received the
// bytes even on a mismatch: write to a temporary file and discard it on error.
func (c *ControlClient) FetchAsset(ctx context.Context, arch string, w io.Writer) (AssetMeta, error) {
	if !archRe.MatchString(arch) {
		return AssetMeta{}, protoErr(c.hello().NodeID, "invalid architecture "+strconv.Quote(arch))
	}
	resp, tr, err := c.request(ctx, http.MethodGet, PathAssetsPrefix+arch, nil)
	if err != nil {
		return AssetMeta{}, err
	}
	defer tr.CloseIdleConnections()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return AssetMeta{}, readControlResponse(resp, nil)
	}
	file := "deyroute_linux_" + arch
	want := strings.ToLower(strings.TrimSpace(resp.Header.Get(HeaderSHA256)))
	if len(want) != sha256.Size*2 {
		return AssetMeta{}, deyerr.New(deyerr.S001, deyerr.Params{"file": file}).WithDetail("the hub sent no valid sha256 for the asset")
	}
	meta := AssetMeta{SHA256: want, Version: resp.Header.Get(HeaderVersion)}
	hsum := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, hsum), resp.Body)
	meta.Size = n
	if err != nil {
		if ctx.Err() != nil {
			return meta, deyerr.Wrap(deyerr.N009, ctx.Err(), deyerr.Params{"addr": c.addr()})
		}
		return meta, deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": c.addr()})
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return meta, deyerr.New(deyerr.S001, deyerr.Params{"file": file}).
			WithDetail("size " + strconv.FormatInt(n, 10) + " differs from " + strconv.FormatInt(resp.ContentLength, 10))
	}
	if got := hex.EncodeToString(hsum.Sum(nil)); got != want {
		return meta, deyerr.New(deyerr.S001, deyerr.Params{"file": file}).
			WithDetail("sha256 " + got + " differs from the expected " + want)
	}
	return meta, nil
}

// Join sends POST /v1/join to hubAddr before the node has any certificate,
// trusting only the CA whose fingerprint is pinned in the join link
// (tlsutil.JoinClientTLSConfig). A wrong CA is DEY-N002; hub-side errors
// keep their DEY codes (N001 token, N007 rate limit, N010, …); network
// problems are DEY-N009 {addr}. The returned CA is checked against the pin.
func Join(ctx context.Context, hubAddr, fingerprint string, req JoinRequest) (JoinResponse, error) {
	return joinWithConfig(ctx, hubAddr, fingerprint, req, tlsutil.JoinClientTLSConfig(fingerprint))
}

func joinWithConfig(ctx context.Context, hubAddr, fingerprint string, jr JoinRequest, cfg *tls.Config) (JoinResponse, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultJoinTimeout)
		defer cancel()
	}
	body, err := json.Marshal(jr)
	if err != nil {
		return JoinResponse{}, deyerr.Wrap(deyerr.X000, err, nil)
	}
	box := &dialErrBox{}
	tr := newTransport(cfg, box)
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+hubAddr+PathJoin, bytes.NewReader(body))
	if err != nil {
		return JoinResponse{}, deyerr.Wrap(deyerr.N009, err, deyerr.Params{"addr": hubAddr})
	}
	req.Header.Set("Content-Type", ContentTypeJSON)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return JoinResponse{}, netErr(hubAddr, err, box)
	}
	defer func() { _ = resp.Body.Close() }()
	var out JoinResponse
	if err := readControlResponse(resp, &out); err != nil {
		return JoinResponse{}, err
	}
	if _, err := tlsutil.VerifyCAPEM([]byte(out.CAPEM), fingerprint); err != nil {
		return JoinResponse{}, err
	}
	return out, nil
}
