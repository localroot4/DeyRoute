package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

const (
	// sessionOutBuffer is the number of hub → node messages queued.
	sessionOutBuffer = 256
	// maxQueuedChunks bounds the log chunks waiting for a slow Stream reader.
	maxQueuedChunks = 4096
)

// Session is the hub side of one node's control stream (POST /v1/stream).
// It is created by ControlServer after the node's hello and handed to
// ControlHandler.Session. All methods are safe for concurrent use.
type Session struct {
	// NodeID is the node id (common name of the verified client certificate).
	NodeID string
	// RemoteIP is the node's current public IP as seen by the hub.
	RemoteIP string
	// Via names the transport the stream arrived through ("" = direct TCP).
	Via string
	// Trusted reports whether RemoteIP is the node's real address (always
	// true for direct TCP).
	Trusted bool
	// CertFingerprint is the fingerprint of the node's client certificate.
	CertFingerprint string
	// Hello is the node's first message (NodeID set from the certificate).
	Hello Hello

	logger      *slog.Logger
	clock       func() time.Time
	callTimeout time.Duration

	out       chan HubMessage
	done      chan struct{}
	closeOnce sync.Once
	hb        chan Heartbeat
	seq       atomic.Uint64

	mu      sync.Mutex
	abort   func() // aborts the HTTP stream; nil after the stream handler ended
	pending map[string]*pendingCall
	pings   map[string]chan time.Time
}

// pendingCall is one command waiting for its result.
type pendingCall struct {
	name   string
	result chan *Result // capacity 1

	qmu     sync.Mutex
	queue   [][]string
	dropped int
	notify  chan struct{} // capacity 1
}

func newSession(nodeID string, peer Peer, fingerprint string, hello Hello, logger *slog.Logger, clock func() time.Time, callTimeout time.Duration) *Session {
	if logger == nil {
		logger = dlog.Discard()
	}
	if clock == nil {
		clock = time.Now
	}
	if callTimeout <= 0 {
		callTimeout = DefaultCommandTimeout
	}
	hello.NodeID = nodeID
	return &Session{
		NodeID:          nodeID,
		RemoteIP:        peer.IP,
		Via:             peer.Via,
		Trusted:         peer.Trusted,
		CertFingerprint: fingerprint,
		Hello:           hello,
		logger:          logger.With(dlog.Node(nodeID)),
		clock:           clock,
		callTimeout:     callTimeout,
		out:             make(chan HubMessage, sessionOutBuffer),
		done:            make(chan struct{}),
		hb:              make(chan Heartbeat, 1),
		pending:         map[string]*pendingCall{},
		pings:           map[string]chan time.Time{},
	}
}

// Done is closed when the stream has ended (node disconnected, replaced by
// a newer stream of the same node, Close, or server shutdown).
func (s *Session) Done() <-chan struct{} { return s.done }

// Close ends the stream. Pending calls fail with DEY-N003.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		if s.abort != nil {
			s.abort()
			s.abort = nil
		}
		s.mu.Unlock()
	})
}

// setAbort installs (or with nil removes) the hook that breaks the HTTP
// stream on Close. It runs under s.mu so that it never runs after the
// stream handler removed it.
func (s *Session) setAbort(f func()) {
	s.mu.Lock()
	s.abort = f
	s.mu.Unlock()
}

// Heartbeats delivers the node's heartbeats. The channel holds only the
// latest one: an unread heartbeat is replaced by a newer one.
func (s *Session) Heartbeats() <-chan Heartbeat { return s.hb }

// SendHello sends the hub's hello (version, Compatible) to the node.
func (s *Session) SendHello(h Hello) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.callTimeout)
	defer cancel()
	return s.send(ctx, HubMessage{Type: MsgHello, Hello: &h})
}

// Call runs command name on the node with args (marshalled to JSON; nil =
// none) and decodes the result data into out (nil = ignore). Without a ctx
// deadline the call is bounded by 30 s. A timeout is DEY-N005 {node,
// command}; cancellation is DEY-N014 (the node is told to cancel); a closed
// stream is DEY-N003; an error reported by the node keeps its DEY code or
// becomes DEY-N011 when it has none.
func (s *Session) Call(ctx context.Context, name string, args any, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.callTimeout)
		defer cancel()
	}
	pc, id, err := s.start(ctx, name, args)
	if err != nil {
		return err
	}
	defer s.forget(id)
	select {
	case res := <-pc.result:
		return s.decodeResult(name, res, out)
	case <-ctx.Done():
		s.cancelRemote(id)
		return s.ctxErr(ctx, name)
	case <-s.done:
		select {
		case res := <-pc.result:
			return s.decodeResult(name, res, out)
		default:
		}
		return s.offlineErr()
	}
}

// Stream runs command name (e.g. logs.tail with Follow) and passes every
// LogChunk to onLines, in order, from the caller's goroutine. It returns
// when the node finishes the command (nil or its error), or when ctx ends
// (the node is told to cancel; DEY-N005 for a deadline, DEY-N014 for a
// cancellation). There is no default timeout. When onLines falls far
// behind, the oldest chunks are dropped and a marker line is delivered.
func (s *Session) Stream(ctx context.Context, name string, args any, onLines func([]string)) error {
	pc, id, err := s.start(ctx, name, args)
	if err != nil {
		return err
	}
	defer s.forget(id)
	for {
		select {
		case <-pc.notify:
			pc.drain(onLines)
		case res := <-pc.result:
			pc.drain(onLines)
			return s.decodeResult(name, res, nil)
		case <-ctx.Done():
			s.cancelRemote(id)
			return s.ctxErr(ctx, name)
		case <-s.done:
			pc.drain(onLines)
			select {
			case res := <-pc.result:
				return s.decodeResult(name, res, nil)
			default:
			}
			return s.offlineErr()
		}
	}
}

// Ping measures the control round-trip time (ping → pong). Without a ctx
// deadline it is bounded by 30 s (DEY-N005 {command: "ping"}).
func (s *Session) Ping(ctx context.Context) (time.Duration, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.callTimeout)
		defer cancel()
	}
	id := "p" + strconv.FormatUint(s.seq.Add(1), 10)
	ch := make(chan time.Time, 1)
	s.mu.Lock()
	s.pings[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pings, id)
		s.mu.Unlock()
	}()
	start := s.clock()
	if err := s.send(ctx, HubMessage{Type: MsgPing, PingID: id}); err != nil {
		return 0, err
	}
	select {
	case at := <-ch:
		rtt := at.Sub(start)
		if rtt < 0 {
			rtt = 0
		}
		return rtt, nil
	case <-ctx.Done():
		return 0, s.ctxErr(ctx, "ping")
	case <-s.done:
		return 0, s.offlineErr()
	}
}

// start registers a pending call and sends the command.
func (s *Session) start(ctx context.Context, name string, args any) (*pendingCall, string, error) {
	var raw json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			return nil, "", deyerr.Wrap(deyerr.X000, fmt.Errorf("marshal %s arguments: %w", name, err), nil)
		}
		raw = data
	}
	id := "c" + strconv.FormatUint(s.seq.Add(1), 10)
	cmd := &Command{ID: id, Name: name, Args: raw}
	if dl, ok := ctx.Deadline(); ok {
		ms := time.Until(dl).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		cmd.TimeoutMs = int(ms)
	}
	pc := &pendingCall{name: name, result: make(chan *Result, 1), notify: make(chan struct{}, 1)}
	s.mu.Lock()
	s.pending[id] = pc
	s.mu.Unlock()
	if err := s.send(ctx, HubMessage{Type: MsgCommand, Command: cmd}); err != nil {
		s.forget(id)
		return nil, "", err
	}
	return pc, id, nil
}

func (s *Session) forget(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// send queues one message for the node.
func (s *Session) send(ctx context.Context, m HubMessage) error {
	select {
	case <-s.done:
		return s.offlineErr()
	default:
	}
	select {
	case s.out <- m:
		return nil
	case <-s.done:
		return s.offlineErr()
	case <-ctx.Done():
		name := m.Type
		if m.Command != nil {
			name = m.Command.Name
		}
		return s.ctxErr(ctx, name)
	}
}

// cancelRemote tells the node to cancel command id (best effort).
func (s *Session) cancelRemote(id string) {
	t := time.NewTimer(time.Second)
	defer t.Stop()
	select {
	case s.out <- HubMessage{Type: MsgCancel, Cancel: id}:
	case <-s.done:
	case <-t.C:
		s.logger.Debug("control: cancel not sent, queue full", slog.String("command_id", id))
	}
}

func (s *Session) ctxErr(ctx context.Context, name string) error {
	p := deyerr.Params{"node": s.NodeID, "command": name}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return deyerr.Wrap(deyerr.N005, ctx.Err(), p)
	}
	return deyerr.Wrap(deyerr.N014, ctx.Err(), p)
}

func (s *Session) offlineErr() error {
	return deyerr.New(deyerr.N003, deyerr.Params{"node": s.NodeID})
}

// decodeResult turns a node Result into the call's error or out.
func (s *Session) decodeResult(name string, res *Result, out any) error {
	if res.Error != nil || !res.OK {
		d := res.Error
		if d == nil || d.Code == "" {
			e := deyerr.New(deyerr.N011, deyerr.Params{"node": s.NodeID, "command": name})
			if d != nil {
				// Shown to the owner: never let a secret from the node's
				// error text through.
				e = e.WithDetail(dlog.Redact(d.Message))
			}
			return e
		}
		return d.Err()
	}
	if out == nil || len(res.Data) == 0 || string(res.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(res.Data, out); err != nil {
		return deyerr.Wrap(deyerr.N011, err, deyerr.Params{"node": s.NodeID, "command": name}).
			WithDetail("the node returned data of an unexpected shape")
	}
	return nil
}

// handle dispatches one message read from the node.
func (s *Session) handle(m NodeMessage) {
	switch m.Type {
	case MsgHeartbeat:
		if m.Heartbeat == nil {
			return
		}
		hb := *m.Heartbeat
		if hb.At.IsZero() {
			hb.At = s.clock()
		}
		// Latest wins: replace an unread heartbeat.
		select {
		case s.hb <- hb:
		default:
			select {
			case <-s.hb:
			default:
			}
			select {
			case s.hb <- hb:
			default:
			}
		}
	case MsgResult:
		if m.Result == nil {
			return
		}
		s.mu.Lock()
		pc := s.pending[m.Result.ID]
		s.mu.Unlock()
		if pc == nil {
			s.logger.Debug("control: result for unknown command", slog.String("command_id", m.Result.ID))
			return
		}
		select {
		case pc.result <- m.Result:
		default:
		}
	case MsgLog:
		if m.Log == nil {
			return
		}
		s.mu.Lock()
		pc := s.pending[m.Log.CommandID]
		s.mu.Unlock()
		if pc != nil && len(m.Log.Lines) > 0 {
			pc.enqueue(m.Log.Lines)
		}
	case MsgPong:
		at := s.clock()
		s.mu.Lock()
		ch := s.pings[m.PingID]
		s.mu.Unlock()
		if ch != nil {
			select {
			case ch <- at:
			default:
			}
		}
	case MsgHello:
		s.logger.Debug("control: repeated hello ignored")
	default:
		s.logger.Debug("control: unknown message type ignored", slog.String("type", m.Type))
	}
}

func (pc *pendingCall) enqueue(lines []string) {
	pc.qmu.Lock()
	if len(pc.queue) >= maxQueuedChunks {
		pc.dropped += len(pc.queue[0])
		pc.queue = pc.queue[1:]
	}
	pc.queue = append(pc.queue, lines)
	pc.qmu.Unlock()
	select {
	case pc.notify <- struct{}{}:
	default:
	}
}

func (pc *pendingCall) drain(onLines func([]string)) {
	pc.qmu.Lock()
	q, dropped := pc.queue, pc.dropped
	pc.queue, pc.dropped = nil, 0
	pc.qmu.Unlock()
	if onLines == nil {
		return
	}
	if dropped > 0 {
		onLines([]string{fmt.Sprintf("[deyroute: %d log lines dropped, reader too slow]", dropped)})
	}
	for _, lines := range q {
		onLines(lines)
	}
}
