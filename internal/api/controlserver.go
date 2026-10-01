package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Control server defaults (section 3).
const (
	// DefaultHandshakeTimeout bounds the TLS handshake, the HTTP/2 preface
	// and the node's hello.
	DefaultHandshakeTimeout = 10 * time.Second
	// DefaultIdleTimeout closes a connection without open streams.
	DefaultIdleTimeout = 120 * time.Second
	// streamWriteTimeout bounds one hub → node message write.
	streamWriteTimeout = 30 * time.Second
	// h2ReadIdle / h2PingTimeout detect dead peers with HTTP/2 pings.
	h2ReadIdle    = 30 * time.Second
	h2PingTimeout = 15 * time.Second
)

// ControlHandler is the hub logic behind the Control API.
type ControlHandler interface {
	// Join handles POST /v1/join (no client certificate). It validates the
	// one-time token (with the per-IP failure limiter, which needs
	// remoteIP), signs the CSR and returns the node certificate and CA.
	Join(ctx context.Context, req JoinRequest, remoteIP string) (JoinResponse, error)
	// Authenticate runs for every other request after mTLS verification:
	// nodeID is the certificate CN (tlsutil.PeerIdentity). It must refuse
	// unknown nodes and certificates whose fingerprint differs from the
	// node's cert_fingerprint (a removed or re-joined node).
	Authenticate(nodeID, certFingerprint, remoteIP string) error
	// Session is called in its own goroutine when a node stream opens (after
	// the node's hello). It should return when s.Done() is closed; the
	// stream ends when it returns. A newer stream of the same node closes
	// the older session first.
	Session(s *Session)
	// Upload receives the body of POST /v1/upload/{id} from nodeID
	// (fetch.proxy payloads, doctor bundles).
	Upload(ctx context.Context, id string, nodeID string, body io.Reader) error
	// Asset opens the hub's deyroute binary for arch (GET /v1/assets/deyroute/{arch}).
	Asset(ctx context.Context, arch string) (AssetInfo, error)
}

// AssetInfo is an asset served to nodes; Reader is closed by the server.
type AssetInfo struct {
	Reader  io.ReadCloser
	Size    int64  // bytes; <= 0 when unknown
	SHA256  string // lowercase hex
	Version string
}

// ControlServer is the hub side of the Control API: mTLS (TLS 1.3, ALPN
// "deyroute/1") with HTTP/2 served directly on every accepted *tls.Conn.
type ControlServer struct {
	// TLSConfig is tlsutil.ServerTLSConfig (VerifyClientCertIfGiven).
	TLSConfig *tls.Config
	Handler   ControlHandler
	Logger    *slog.Logger
	// Clock is used for ping RTTs and heartbeats without a time; default time.Now.
	Clock func() time.Time

	// HandshakeTimeout bounds TLS handshake, preface and hello; default 10 s.
	HandshakeTimeout time.Duration
	// IdleTimeout closes connections without streams; default 120 s.
	IdleTimeout time.Duration
	// CallTimeout is the default Session.Call timeout; default 30 s.
	CallTimeout time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
	conns    map[net.Conn]struct{}
}

// Session returns the current stream of nodeID, if connected.
func (s *ControlServer) Session(nodeID string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[nodeID]
	return sess, ok
}

func (s *ControlServer) logger() *slog.Logger {
	if s.Logger == nil {
		return dlog.Discard()
	}
	return s.Logger
}

func (s *ControlServer) clock() func() time.Time {
	if s.Clock == nil {
		return time.Now
	}
	return s.Clock
}

func (s *ControlServer) handshakeTimeout() time.Duration {
	if s.HandshakeTimeout > 0 {
		return s.HandshakeTimeout
	}
	return DefaultHandshakeTimeout
}

// Serve accepts connections on ln until ctx is done, then closes every
// session and connection and returns nil. ln is a plain TCP listener (TLS
// is done here) or a tls listener with the same config.
func (s *ControlServer) Serve(ctx context.Context, ln net.Listener) error {
	if s.TLSConfig == nil || s.Handler == nil {
		return deyerr.Wrap(deyerr.X000, errors.New("control server needs TLSConfig and Handler"), nil)
	}
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	if s.conns == nil {
		s.conns = map[net.Conn]struct{}{}
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	idle := s.IdleTimeout
	if idle <= 0 {
		idle = DefaultIdleTimeout
	}
	h2 := &http2.Server{IdleTimeout: idle, ReadIdleTimeout: h2ReadIdle, PingTimeout: h2PingTimeout}
	base := &http.Server{
		ReadHeaderTimeout: s.handshakeTimeout(),
		IdleTimeout:       idle,
		ErrorLog:          slog.NewLogLogger(s.logger().Handler(), slog.LevelDebug),
	}
	// http2.Server.ServeConn does not wait for its handler goroutines, so a
	// handler may start after its connection was closed. The tracker refuses
	// handlers once shutdown began, which keeps WaitGroup.Add from racing
	// with Wait.
	var handlers handlerTracker
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !handlers.enter() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer handlers.leave()
		s.route(w, r)
	})
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()

	var wg sync.WaitGroup
	var result error
	var delay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(err, net.ErrClosed) {
				result = deyerr.Wrap(deyerr.X000, fmt.Errorf("control API accept: %w", err), nil)
				break
			}
			// Temporary failures (EMFILE/ENFILE under a connection flood,
			// ECONNABORTED, timeouts) must not stop the control API: back
			// off like net/http does and keep accepting.
			delay = acceptBackoff(delay)
			s.logger().Warn("control: accept failed; retrying", slog.Duration("in", delay), dlog.Err(err))
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
			case <-t.C:
			}
			continue
		}
		delay = 0
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.serveConn(ctx, conn, h2, base, handler)
		}()
	}
	cancel()
	s.mu.Lock()
	for _, sess := range s.sessions {
		sess.Close()
	}
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	wg.Wait()
	handlers.closeAndWait()
	return result
}

// acceptBackoff returns the next delay after a failed Accept (5 ms doubling
// up to 1 s).
func acceptBackoff(prev time.Duration) time.Duration {
	if prev == 0 {
		return 5 * time.Millisecond
	}
	return min(2*prev, time.Second)
}

// handlerTracker counts running HTTP handlers and refuses new ones after
// closeAndWait started.
type handlerTracker struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// enter registers a handler; false when the server is shutting down.
func (h *handlerTracker) enter() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.wg.Add(1)
	return true
}

func (h *handlerTracker) leave() { h.wg.Done() }

// closeAndWait refuses new handlers and waits for the running ones.
func (h *handlerTracker) closeAndWait() {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.wg.Wait()
}

func (s *ControlServer) serveConn(ctx context.Context, conn net.Conn, h2 *http2.Server, base *http.Server, h http.Handler) {
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		_ = conn.Close()
		return
	}
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	tconn, ok := conn.(*tls.Conn)
	if !ok {
		tconn = tls.Server(conn, s.TLSConfig)
	}
	hctx, cancel := context.WithTimeout(ctx, s.handshakeTimeout())
	_ = conn.SetDeadline(time.Now().Add(s.handshakeTimeout()))
	err := tconn.HandshakeContext(hctx)
	cancel()
	if err != nil {
		s.logger().Debug("control: TLS handshake failed", slog.String("remote", conn.RemoteAddr().String()), dlog.Err(err))
		return
	}
	_ = conn.SetDeadline(time.Time{})
	h2.ServeConn(tconn, &http2.ServeConnOpts{Context: ctx, BaseConfig: base, Handler: h})
}

// route dispatches one request. Only POST /v1/join is served without a
// verified node certificate.
func (s *ControlServer) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == PathJoin && r.Method == http.MethodPost {
		s.handleJoin(w, r)
		return
	}
	ip := remoteIP(r)
	nodeID, fp, err := s.authenticate(r, ip)
	if err != nil {
		s.logger().Info("control: request refused", slog.String("path", r.URL.Path),
			slog.String("remote_ip", ip), dlog.Err(err))
		// Authentication failures are always 401, whatever the hub's code.
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusUnauthorized)
		_ = writeJSONLine(w, controlEnvelope{Error: ToDTO(err)})
		return
	}
	switch {
	case r.URL.Path == PathStream:
		if !allowMethod(w, r, http.MethodPost) {
			return
		}
		s.handleStream(w, r, nodeID, fp, ip)
	case strings.HasPrefix(r.URL.Path, PathUploadPrefix):
		if !allowMethod(w, r, http.MethodPost) {
			return
		}
		s.handleUpload(w, r, nodeID)
	case strings.HasPrefix(r.URL.Path, PathAssetsPrefix):
		if !allowMethod(w, r, http.MethodGet) {
			return
		}
		s.handleAsset(w, r)
	default:
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusNotFound)
		_ = writeJSONLine(w, controlEnvelope{Error: ToDTO(protoErr(nodeID, "unknown path "+r.URL.Path))})
	}
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(http.StatusMethodNotAllowed)
	_ = writeJSONLine(w, controlEnvelope{Error: ToDTO(protoErr("", "method "+r.Method+" not allowed on "+r.URL.Path))})
	return false
}

func protoErr(node, reason string) *deyerr.Error {
	if node == "" {
		node = "?"
	}
	return deyerr.New(deyerr.N015, deyerr.Params{"node": node, "reason": reason})
}

// authenticate checks the verified client certificate and asks the handler.
func (s *ControlServer) authenticate(r *http.Request, ip string) (nodeID, fp string, err error) {
	if r.TLS == nil {
		return "", "", deyerr.New(deyerr.N013, deyerr.Params{"path": r.URL.Path})
	}
	cn, fp, ok := tlsutil.PeerIdentity(*r.TLS)
	if !ok {
		return "", "", deyerr.New(deyerr.N013, deyerr.Params{"path": r.URL.Path})
	}
	if err := s.Handler.Authenticate(cn, fp, ip); err != nil {
		return "", "", err
	}
	return cn, fp, nil
}

func (s *ControlServer) handleJoin(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	// Unauthenticated peers must not hold a stream open with a slow body.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(s.handshakeTimeout()))
	body := http.MaxBytesReader(w, r.Body, MaxJoinRequest)
	var req JoinRequest
	err := json.NewDecoder(body).Decode(&req)
	_ = rc.SetReadDeadline(time.Time{})
	if err != nil {
		reason := "invalid join request"
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			reason = "join request larger than " + strconv.Itoa(MaxJoinRequest) + " bytes"
		} else if errors.Is(err, os.ErrDeadlineExceeded) {
			reason = "join request not received in time"
		}
		writeControlError(w, protoErr("joining node "+ip, reason))
		return
	}
	resp, err := s.callJoin(r.Context(), req, ip)
	if err != nil {
		s.logger().Info("control: join refused", slog.String("remote_ip", ip), dlog.Err(err))
		writeControlError(w, err)
		return
	}
	s.logger().Info("control: node joined", dlog.Node(resp.NodeID), slog.String("remote_ip", ip))
	writeControlResult(w, resp)
}

// callJoin runs Handler.Join, turning a panic into DEY-X000.
func (s *ControlServer) callJoin(ctx context.Context, req JoinRequest, ip string) (resp JoinResponse, err error) {
	defer func() {
		if p := recover(); p != nil {
			s.logPanic("join", p)
			err = deyerr.Wrap(deyerr.X000, fmt.Errorf("panic in join: %v", p), nil)
		}
	}()
	return s.Handler.Join(ctx, req, ip)
}

func (s *ControlServer) logPanic(what string, p any) {
	s.logger().Error("control: handler panicked", slog.String("handler", what),
		slog.String("panic", fmt.Sprint(p)), slog.String("stack", string(debug.Stack())), dlog.Code(deyerr.X000))
}

func (s *ControlServer) handleUpload(w http.ResponseWriter, r *http.Request, nodeID string) {
	id := strings.TrimPrefix(r.URL.Path, PathUploadPrefix)
	if !uploadIDRe.MatchString(id) {
		writeControlError(w, protoErr(nodeID, "invalid upload id"))
		return
	}
	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				s.logPanic("upload", p)
				err = deyerr.Wrap(deyerr.X000, fmt.Errorf("panic in upload: %v", p), nil)
			}
		}()
		return s.Handler.Upload(r.Context(), id, nodeID, r.Body)
	}()
	if err != nil {
		writeControlError(w, err)
		return
	}
	writeControlResult(w, nil)
}

func (s *ControlServer) handleAsset(w http.ResponseWriter, r *http.Request) {
	arch := strings.TrimPrefix(r.URL.Path, PathAssetsPrefix)
	if !archRe.MatchString(arch) {
		writeControlError(w, protoErr("", "invalid architecture"))
		return
	}
	info, err := func() (info AssetInfo, err error) {
		defer func() {
			if p := recover(); p != nil {
				s.logPanic("asset", p)
				err = deyerr.Wrap(deyerr.X000, fmt.Errorf("panic in asset: %v", p), nil)
			}
		}()
		return s.Handler.Asset(r.Context(), arch)
	}()
	if err != nil {
		writeControlError(w, err)
		return
	}
	if info.Reader == nil {
		writeControlError(w, deyerr.Wrap(deyerr.X000, errors.New("asset without reader"), nil))
		return
	}
	defer func() { _ = info.Reader.Close() }()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	if info.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(info.Size, 10))
	}
	if info.SHA256 != "" {
		h.Set(HeaderSHA256, strings.ToLower(info.SHA256))
	}
	if info.Version != "" {
		h.Set(HeaderVersion, info.Version)
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, info.Reader); err != nil {
		s.logger().Debug("control: asset transfer ended", slog.String("arch", arch), dlog.Err(err))
	}
}

// handleStream serves POST /v1/stream: NodeMessage lines in, HubMessage
// lines out, until the session ends.
func (s *ControlServer) handleStream(w http.ResponseWriter, r *http.Request, nodeID, fp, ip string) {
	log := s.logger().With(dlog.Node(nodeID))
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", ContentTypeNDJSON)
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return
	}
	lr := newLineReader(r.Body, MaxControlLine)

	// The node's hello must arrive within the handshake timeout.
	_ = rc.SetReadDeadline(time.Now().Add(s.handshakeTimeout()))
	line, err := lr.next()
	_ = rc.SetReadDeadline(time.Time{})
	if err != nil {
		log.Info("control: stream closed before hello", dlog.Err(err))
		return
	}
	var first NodeMessage
	if err := json.Unmarshal(line, &first); err != nil || first.Type != MsgHello || first.Hello == nil {
		log.Warn("control: stream did not start with hello", dlog.Err(protoErr(nodeID, "first message is not hello")), dlog.Code(deyerr.N015))
		return
	}
	if first.Hello.NodeID != "" && first.Hello.NodeID != nodeID {
		log.Warn("control: hello node id differs from the certificate; using the certificate",
			slog.String("hello_node", first.Hello.NodeID))
	}
	sess := newSession(nodeID, ip, fp, *first.Hello, s.Logger, s.clock(), s.CallTimeout)
	sess.setAbort(func() {
		// Break a blocked write and the body read of this stream only.
		_ = rc.SetWriteDeadline(time.Now().Add(-time.Second))
		_ = r.Body.Close()
	})

	s.mu.Lock()
	old := s.sessions[nodeID]
	s.sessions[nodeID] = sess
	s.mu.Unlock()
	if old != nil {
		log.Info("control: new stream replaces the previous one")
		old.Close()
	}
	log.Info("control: node connected", slog.String("remote_ip", ip), slog.String("version", first.Hello.Version))

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer sess.Close()
		for {
			line, err := lr.next()
			if err != nil {
				return
			}
			var m NodeMessage
			if err := json.Unmarshal(line, &m); err != nil {
				log.Warn("control: invalid message from node", dlog.Err(protoErr(nodeID, "invalid JSON")), dlog.Code(deyerr.N015))
				return
			}
			sess.handle(m)
		}
	}()
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		defer sess.Close()
		defer func() {
			if p := recover(); p != nil {
				s.logPanic("session", p)
			}
		}()
		s.Handler.Session(sess)
	}()

	s.writeLoop(sess, w, rc)

	sess.setAbort(nil)
	_ = r.Body.Close()
	s.mu.Lock()
	if s.sessions[nodeID] == sess {
		delete(s.sessions, nodeID)
	}
	s.mu.Unlock()
	<-readerDone
	<-handlerDone
	log.Info("control: node disconnected")
}

// writeLoop sends queued hub messages until the session ends.
func (s *ControlServer) writeLoop(sess *Session, w http.ResponseWriter, rc *http.ResponseController) {
	for {
		select {
		case m := <-sess.out:
			line, err := json.Marshal(m)
			if err != nil {
				sess.logger.Error("control: cannot encode message", dlog.Err(err))
				continue
			}
			_ = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
			_, err = w.Write(append(line, '\n'))
			if err == nil {
				err = rc.Flush()
			}
			_ = rc.SetWriteDeadline(time.Time{})
			if err != nil {
				sess.Close()
				return
			}
		case <-sess.done:
			return
		}
	}
}
