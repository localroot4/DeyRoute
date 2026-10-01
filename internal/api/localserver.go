package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// Local API wire constants (ARCHITECTURE.md section 6).
const (
	// PathRPCPrefix is the URL prefix of every Local API method.
	PathRPCPrefix = "/v1/rpc/"
	// ContentTypeJSON is the content type of a non-streaming response.
	ContentTypeJSON = "application/json"
	// ContentTypeNDJSON is the content type of a streaming response.
	ContentTypeNDJSON = "application/x-ndjson"
	// DefaultSocketPath is the daemon's Local API socket.
	DefaultSocketPath = "/run/deyroute/daemon.sock"
	// DefaultService is the unit named in DEY-X003 when DialOptions.Service is empty.
	DefaultService = "deyroute-hub"
	// DefaultCallTimeout bounds a non-streaming Local API call whose context has no deadline.
	DefaultCallTimeout = 2 * time.Minute

	maxRPCRequest   = 8 << 20 // request body limit
	maxRPCFrame     = 64 << 20
	shutdownTimeout = 5 * time.Second
)

// rpcMethod is one entry of the generated dispatch table.
type rpcMethod struct {
	arity  int  // number of JSON arguments
	stream bool // responds with NDJSON (has a progress/emit callback)
	call   func(ctx context.Context, args []json.RawMessage, cb *rpcCallbacks) (any, error)
}

// rpcFrame is one response object (a whole response, or one NDJSON line).
type rpcFrame struct {
	Step   *Step           `json:"step,omitempty"`
	Log    *LogLine        `json:"log,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ErrorDTO       `json:"error,omitempty"`
}

// errStreamClosed is returned by an emit callback after the call ended or
// the client went away.
var errStreamClosed = deyerr.Plain("api: local API stream closed")

// rpcCallbacks writes progress and log lines of a streaming call. It is safe
// for concurrent use; lines written after the method returned are dropped.
type rpcCallbacks struct {
	mu     sync.Mutex
	w      io.Writer
	rc     *http.ResponseController
	closed bool
	err    error
}

// Step is passed to the implementation as its progress callback.
func (cb *rpcCallbacks) Step(s Step) {
	_ = cb.write(rpcFrame{Step: &s})
}

// Log is passed to the implementation as its emit callback; it fails when
// the client disconnected, which must end a follow.
func (cb *rpcCallbacks) Log(l LogLine) error {
	return cb.write(rpcFrame{Log: &l})
}

func (cb *rpcCallbacks) write(f rpcFrame) error {
	line, err := json.Marshal(f)
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.closed {
		return errStreamClosed
	}
	if cb.err != nil {
		return cb.err
	}
	if _, err := cb.w.Write(append(line, '\n')); err != nil {
		cb.err = errStreamClosed
		return cb.err
	}
	if err := cb.rc.Flush(); err != nil {
		cb.err = errStreamClosed
		return cb.err
	}
	return nil
}

// finish writes the final frame and disables the callbacks.
func (cb *rpcCallbacks) finish(f rpcFrame) {
	_ = cb.write(f)
	cb.mu.Lock()
	cb.closed = true
	cb.mu.Unlock()
}

// decodeArg decodes argument i of a Local API call into v.
func decodeArg(args []json.RawMessage, i int, v any) error {
	if i >= len(args) {
		return badRequest(fmt.Sprintf("argument %d missing", i))
	}
	if err := json.Unmarshal(args[i], v); err != nil {
		return deyerr.Wrap(deyerr.X006, err, deyerr.Params{"status": fmt.Sprintf("400 argument %d invalid", i)})
	}
	return nil
}

func badRequest(what string) *deyerr.Error {
	return deyerr.New(deyerr.X006, deyerr.Params{"status": "400 " + what})
}

// rpcHandler serves the generated dispatch table.
type rpcHandler struct {
	logger  *slog.Logger
	methods map[string]rpcMethod
}

func newRPCHandler(logger *slog.Logger, methods map[string]rpcMethod) http.Handler {
	if logger == nil {
		logger = dlog.Discard()
	}
	return &rpcHandler{logger: logger, methods: methods}
}

// ServeHTTP implements http.Handler.
func (h *rpcHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, PathRPCPrefix)
	if !ok {
		writeJSONError(w, http.StatusNotFound, badRequest("unknown path "+r.URL.Path).WithWhy("the Local API only serves "+PathRPCPrefix+"<Method>"))
		return
	}
	m, ok := h.methods[name]
	if !ok {
		writeJSONError(w, http.StatusNotFound, deyerr.New(deyerr.X006, deyerr.Params{"status": "404 unknown method " + name}).
			WithWhy("the CLI and the daemon are different versions"))
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, badRequest("method "+r.Method+" not allowed"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRPCRequest+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, badRequest("unreadable request body"))
		return
	}
	if len(body) > maxRPCRequest {
		writeJSONError(w, http.StatusRequestEntityTooLarge, badRequest("request too large"))
		return
	}
	var args []json.RawMessage
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &args); err != nil {
			writeJSONError(w, http.StatusBadRequest, badRequest("arguments must be a JSON array"))
			return
		}
	}
	if len(args) != m.arity {
		writeJSONError(w, http.StatusBadRequest, badRequest(fmt.Sprintf("%s takes %d arguments, got %d", name, m.arity, len(args))))
		return
	}
	if m.stream {
		h.serveStream(w, r, name, m, args)
		return
	}
	res, err := h.invoke(r.Context(), name, m, args, nil)
	f := resultFrame(res, err)
	line, merr := json.Marshal(f)
	if merr != nil {
		writeJSONError(w, http.StatusOK, deyerr.Wrap(deyerr.X000, merr, nil))
		return
	}
	w.Header().Set("Content-Type", ContentTypeJSON)
	_, _ = w.Write(append(line, '\n'))
}

func (h *rpcHandler) serveStream(w http.ResponseWriter, r *http.Request, name string, m rpcMethod, args []json.RawMessage) {
	w.Header().Set("Content-Type", ContentTypeNDJSON)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	cb := &rpcCallbacks{w: w, rc: rc}
	res, err := h.invoke(r.Context(), name, m, args, cb)
	cb.finish(resultFrame(res, err))
}

// invoke runs one method, turning a panic into DEY-X000.
func (h *rpcHandler) invoke(ctx context.Context, name string, m rpcMethod, args []json.RawMessage, cb *rpcCallbacks) (res any, err error) {
	defer func() {
		if p := recover(); p != nil {
			h.logger.Error("local API handler panicked",
				slog.String("method", name), slog.String("panic", fmt.Sprint(p)),
				slog.String("stack", string(debug.Stack())), dlog.Code(deyerr.X000))
			res = nil
			err = deyerr.Wrap(deyerr.X000, fmt.Errorf("panic in %s: %v", name, p), nil)
		}
	}()
	res, err = m.call(ctx, args, cb)
	if err != nil {
		if deyerr.As(err).Code == deyerr.X000 {
			// The owner only sees "Unexpected error ... details are in the
			// log": the cause must be logged, not only at debug level.
			h.logger.Error("local API call failed unexpectedly", slog.String("method", name),
				dlog.Err(err), dlog.Code(deyerr.X000))
		} else {
			h.logger.Debug("local API call failed", slog.String("method", name), dlog.Err(err))
		}
	}
	return res, err
}

// resultFrame builds the final frame of a call.
func resultFrame(res any, err error) rpcFrame {
	if err != nil {
		return rpcFrame{Error: ToDTO(err)}
	}
	data, merr := json.Marshal(res)
	if merr != nil {
		return rpcFrame{Error: ToDTO(deyerr.Wrap(deyerr.X000, merr, nil))}
	}
	return rpcFrame{Result: data}
}

func writeJSONError(w http.ResponseWriter, status int, err error) {
	line, _ := json.Marshal(rpcFrame{Error: ToDTO(err)})
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(append(line, '\n'))
}

// ServeLocal serves h on the unix socket socketPath until ctx is done:
// it creates the parent directory (0700), removes a stale socket (only a
// socket nobody answers on), listens, restricts the socket to 0600, and on
// cancellation shuts down gracefully (5 s) and removes the socket.
// A live socket is DEY-X040; any other listen problem is DEY-X041.
func ServeLocal(ctx context.Context, socketPath string, h http.Handler, logger *slog.Logger) error {
	return ServeLocalReady(ctx, socketPath, h, logger, nil)
}

// ServeLocalReady is ServeLocal with a callback run once the socket accepts
// connections (e.g. to send sd_notify READY=1).
func ServeLocalReady(ctx context.Context, socketPath string, h http.Handler, logger *slog.Logger, ready func()) error {
	if logger == nil {
		logger = dlog.Discard()
	}
	ln, err := listenLocal(socketPath)
	if err != nil {
		return err
	}
	baseCtx, cancelBase := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBase()
	// http.Server.Close does not wait for running handlers: track them so
	// that no Local method still runs after ServeLocal returned.
	var handlers handlerTracker
	tracked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !handlers.enter() {
			writeJSONError(w, http.StatusServiceUnavailable, badRequest("the daemon is stopping"))
			return
		}
		defer handlers.leave()
		h.ServeHTTP(w, r)
	})
	srv := &http.Server{
		Handler:           tracked,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelDebug),
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	if ready != nil {
		ready()
	}
	logger.Info("local API listening", slog.String("socket", socketPath))

	var result error
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := srv.Shutdown(sctx); err != nil {
			// Streams (logs --follow) did not end in time: cancel them and close.
			cancelBase()
			_ = srv.Close()
		}
		cancel()
		<-serveErr
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			result = deyerr.Wrap(deyerr.X041, err, deyerr.Params{"path": socketPath, "reason": err.Error()})
		}
		cancelBase()
		_ = srv.Close()
	}
	// Every request context is cancelled by now; wait for the handlers.
	cancelBase()
	waitHandlers(&handlers, shutdownTimeout, func() {
		logger.Warn("local API handlers still running after shutdown", slog.String("socket", socketPath))
	})
	_ = ln.Close()
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("cannot remove local API socket", slog.String("socket", socketPath), dlog.Err(err))
	}
	return result
}

// waitHandlers refuses new handlers and waits up to timeout for the running
// ones; onTimeout runs when some are still busy (an implementation that
// ignores its cancelled context).
func waitHandlers(h *handlerTracker, timeout time.Duration, onTimeout func()) {
	done := make(chan struct{})
	go func() {
		h.closeAndWait()
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		onTimeout()
	}
}

// listenLocal prepares the directory, clears a stale socket and listens.
func listenLocal(socketPath string) (net.Listener, error) {
	lerr := func(err error, reason string) error {
		return deyerr.Wrap(deyerr.X041, err, deyerr.Params{"path": socketPath, "reason": reason})
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, lerr(err, err.Error())
	}
	if fi, err := os.Lstat(socketPath); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, lerr(nil, "the path exists and is not a socket")
		}
		conn, derr := net.DialTimeout("unix", socketPath, time.Second)
		if derr == nil {
			_ = conn.Close()
			return nil, deyerr.New(deyerr.X040, deyerr.Params{"path": socketPath})
		}
		if !staleSocket(derr) {
			// EAGAIN (a live daemon whose accept backlog is full), EACCES,
			// timeouts: the socket may be in use, never delete it.
			return nil, lerr(derr, "the existing socket cannot be checked: "+derr.Error())
		}
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, lerr(err, "cannot remove the stale socket: "+err.Error())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, lerr(err, err.Error())
	}
	// Bind under a temporary name, restrict it to 0600 and only then link it
	// to the real path, so the socket is never reachable with the umask's
	// (wider) mode, whatever the mode of the parent directory. link(2) fails
	// when another daemon created the path meanwhile.
	tmp := socketPath + ".tmp" + strconv.Itoa(os.Getpid())
	if fi, err := os.Lstat(tmp); err == nil && fi.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(tmp)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: tmp, Net: "unix"})
	if err != nil {
		return nil, lerr(err, err.Error())
	}
	ln.SetUnlinkOnClose(false)
	fail := func(err error, reason string) (net.Listener, error) {
		_ = ln.Close()
		_ = os.Remove(tmp)
		return nil, lerr(err, reason)
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return fail(err, "chmod 0600: "+err.Error())
	}
	if err := os.Link(tmp, socketPath); err != nil {
		return fail(err, err.Error())
	}
	if err := os.Remove(tmp); err != nil {
		_ = os.Remove(socketPath)
		return fail(err, err.Error())
	}
	return ln, nil
}

// staleSocket reports whether a dial error proves that nobody serves the
// socket (connection refused), which is the only case where it is removed.
func staleSocket(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
