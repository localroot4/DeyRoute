// Package log sets up structured logging for every deyroute process
// (section 13): JSON lines through log/slog with the fixed fields ts, level,
// component, tunnel, node, transport, code, msg and err; built-in rotation
// (20 MB × 5 gzip files); and the central secret filter of section 11 that
// masks tokens, keys and passwords with "***" before anything reaches a
// file or a terminal.
package log

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Field names written by every logger (section 13).
const (
	KeyTS        = "ts"
	KeyLevel     = "level"
	KeyComponent = "component"
	KeyTunnel    = "tunnel"
	KeyNode      = "node"
	KeyTransport = "transport"
	KeyCode      = "code"
	KeyMsg       = "msg"
	KeyErr       = "err"
)

// Defaults and environment.
const (
	// DefaultMaxBytes is the rotation threshold (section 12: 20 MB).
	DefaultMaxBytes int64 = 20 << 20
	// DefaultKeep is the number of rotated, gzip-compressed files kept.
	DefaultKeep = 5
	// DebugEnv enables debug logging (and a stderr mirror) when set to 1.
	DebugEnv = "DEYROUTE_DEBUG"
	// DefaultComponent is used when Options.Component is empty.
	DefaultComponent = "deyroute"
	// TimeLayout is the "ts" format: RFC 3339, UTC, millisecond precision.
	TimeLayout = "2006-01-02T15:04:05.000Z07:00"
)

// Options configures New.
type Options struct {
	// Component is the value of the "component" field (hub, node, cli…).
	Component string
	// File is the log file (e.g. /var/log/deyroute/hub.log); empty means
	// stderr only.
	File string
	// Debug lowers the level to debug and mirrors output to stderr. The
	// DEYROUTE_DEBUG=1 environment variable has the same effect.
	Debug bool
	// Stderr mirrors the output to stderr even without debug.
	Stderr bool
	// MaxBytes is the rotation size (default 20 MB).
	MaxBytes int64
	// Keep is the number of rotated files kept (default 5).
	Keep int
	// StderrWriter replaces os.Stderr for the mirror (tests).
	StderrWriter io.Writer
}

// DebugFromEnv reports whether DEYROUTE_DEBUG requests debug logging
// ("1", "true", "yes" or "on").
func DebugFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DebugEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// New builds a JSON logger. With a File the output goes through a
// RotatingWriter (directory 0750, file 0600, rotation at MaxBytes, Keep
// gzip files); with Debug (or DEYROUTE_DEBUG=1) the level is debug and the
// output is mirrored to stderr, as it is with Stderr or when File is empty.
// Every string reaching the output is redacted. The returned Closer closes
// the log file (it never closes stderr). Errors are DEY-X022.
func New(opt Options) (*slog.Logger, io.Closer, error) {
	debug := opt.Debug || DebugFromEnv()
	var (
		sinks  []io.Writer
		closer io.Closer = nopCloser{}
	)
	if opt.File != "" {
		rw, err := NewRotatingWriter(opt.File, opt.MaxBytes, opt.Keep)
		if err != nil {
			return nil, nil, err
		}
		sinks = append(sinks, rw)
		closer = rw
	}
	if opt.File == "" || opt.Stderr || debug {
		se := opt.StderrWriter
		if se == nil {
			se = os.Stderr
		}
		sinks = append(sinks, se)
	}
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	out := sinks[0]
	if len(sinks) > 1 {
		out = fanout(sinks)
	}
	return slog.New(NewHandler(out, level, opt.Component)), closer, nil
}

// NewHandler returns the deyroute JSON handler writing to w at the given
// minimum level with a fixed "component" field. Adding a top-level
// "component" attribute (logger.With("component", "failover")) replaces the
// field instead of duplicating it.
func NewHandler(w io.Writer, level slog.Leveler, component string) slog.Handler {
	if component == "" {
		component = DefaultComponent
	}
	root := slog.NewJSONHandler(secretFilter{w: w}, &slog.HandlerOptions{Level: level, ReplaceAttr: replaceAttr})
	h := &handler{root: root, component: component}
	h.inner = h.build()
	return h
}

// Discard returns a logger that drops everything (tests, silent callers).
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// ---------------------------------------------------------------- attribute helpers

// Tunnel is the "tunnel" field.
func Tunnel(id string) slog.Attr { return slog.String(KeyTunnel, id) }

// Node is the "node" field.
func Node(id string) slog.Attr { return slog.String(KeyNode, id) }

// Transport is the "transport" field ("backhaul/wssmux").
func Transport(id string) slog.Attr { return slog.String(KeyTransport, id) }

// Component is the "component" field.
func Component(name string) slog.Attr { return slog.String(KeyComponent, name) }

// Code is the "code" field ("DEY-P012").
func Code(c deyerr.Code) slog.Attr { return slog.String(KeyCode, string(c)) }

// Err is the "err" field; a nil error yields an empty attribute, which
// handlers ignore.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.String(KeyErr, safeText(err, err.Error))
}

// ErrAttrs returns the "code" (for DEY errors) and "err" fields of err, for
// logger.Error("msg", log.ErrAttrs(err)...).
func ErrAttrs(err error) []any {
	if err == nil {
		return nil
	}
	var de *deyerr.Error
	if stderrors.As(err, &de) && de != nil {
		return []any{Code(de.Code), Err(err)}
	}
	return []any{Err(err)}
}

// safeText calls f (an Error or String method of v) and turns a panic into
// text instead of crashing the process, as slog does for its own values:
// the usual cause is a nil pointer stored in a non-nil error interface.
func safeText(v any, f func() string) (s string) {
	defer func() {
		if r := recover(); r != nil {
			if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
				s = "<nil>"
				return
			}
			s = fmt.Sprintf("!PANIC: %v", r)
		}
	}()
	return f()
}

// ---------------------------------------------------------------- handler

// handler wraps slog.JSONHandler to keep "component" a single top-level
// field that sub-loggers can override.
type handler struct {
	root      *slog.JSONHandler
	component string
	ops       []handlerOp // WithGroup/WithAttrs calls after the component
	groups    int
	inner     slog.Handler
}

type handlerOp struct {
	group string
	attrs []slog.Attr
}

func (h *handler) build() slog.Handler {
	in := h.root.WithAttrs([]slog.Attr{slog.String(KeyComponent, h.component)})
	for _, op := range h.ops {
		if op.group != "" {
			in = in.WithGroup(op.group)
		} else {
			in = in.WithAttrs(op.attrs)
		}
	}
	return in
}

func (h *handler) clone() *handler {
	c := *h
	c.ops = slices.Clip(h.ops)
	return &c
}

// Enabled implements slog.Handler.
func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

// Handle implements slog.Handler. A top-level "component" attribute on the
// record replaces the handler's component for this record.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	if h.groups == 0 {
		comp, found := "", false
		r.Attrs(func(a slog.Attr) bool {
			if v, ok := componentValue(a); ok {
				comp, found = v, true
			}
			return true
		})
		if found {
			nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
			r.Attrs(func(a slog.Attr) bool {
				if _, isComp := componentValue(a); !isComp {
					nr.AddAttrs(a)
				}
				return true
			})
			c := h.clone()
			c.component = comp
			return c.build().Handle(ctx, nr)
		}
	}
	return h.inner.Handle(ctx, r)
}

// componentValue reports whether a is a top-level "component" attribute
// (any scalar kind) and returns its text.
func componentValue(a slog.Attr) (string, bool) {
	if a.Key != KeyComponent {
		return "", false
	}
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		return "", false
	}
	return v.String(), true
}

// WithAttrs implements slog.Handler.
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	c := h.clone()
	if h.groups == 0 {
		rest := make([]slog.Attr, 0, len(attrs))
		for _, a := range attrs {
			if v, ok := componentValue(a); ok {
				c.component = v
				continue
			}
			rest = append(rest, a)
		}
		if len(rest) > 0 {
			c.ops = append(c.ops, handlerOp{attrs: rest})
		}
		if c.component != h.component {
			c.inner = c.build()
			return c
		}
		if len(rest) == 0 {
			return h
		}
		c.inner = h.inner.WithAttrs(rest)
		return c
	}
	c.ops = append(c.ops, handlerOp{attrs: attrs})
	c.inner = h.inner.WithAttrs(attrs)
	return c
}

// WithGroup implements slog.Handler.
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := h.clone()
	c.groups++
	c.ops = append(c.ops, handlerOp{group: name})
	c.inner = h.inner.WithGroup(name)
	return c
}

// replaceAttr renames and formats the built-in fields and redacts every
// value (called by slog for each non-group attribute, nested ones too).
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.TimeKey:
			if a.Value.Kind() == slog.KindTime {
				return slog.String(KeyTS, a.Value.Time().UTC().Format(TimeLayout))
			}
		case slog.LevelKey:
			if l, ok := a.Value.Any().(slog.Level); ok {
				return slog.String(KeyLevel, LevelName(l))
			}
		case slog.MessageKey:
			return slog.String(KeyMsg, Redact(a.Value.String()))
		}
	}
	return redactAttr(a, sensitiveGroup(groups))
}

// LevelName maps a slog level to the lowercase names written to the log:
// debug, info, warn, error.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// redactAttr masks the value of a sensitive attribute (by its own name or,
// with inSecretGroup, by an enclosing group's name) and redacts every other
// string-bearing value. Booleans, durations and times cannot carry a
// secret and are never masked.
func redactAttr(a slog.Attr, inSecretGroup bool) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindGroup:
		return a
	case slog.KindBool, slog.KindDuration, slog.KindTime:
	default:
		if inSecretGroup || sensitiveKey(a.Key) {
			if a.Value.Kind() == slog.KindString && a.Value.String() == "" {
				return a
			}
			return slog.String(a.Key, Mask)
		}
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(a.Value.String()))
	case slog.KindTime:
		// Section 2: times in logs are UTC.
		return slog.Time(a.Key, a.Value.Time().UTC())
	case slog.KindAny:
		return slog.Attr{Key: a.Key, Value: redactAny(a.Value.Any())}
	default:
		return a
	}
}

// redactAny turns arbitrary values into redacted strings or redacted JSON.
// Structured values are encoded to JSON and re-encoded by redactJSON, so
// every string leaf is redacted on its raw (unescaped) text and members
// with a secret-looking name (token, password, private_key…) are masked
// whole whatever their type.
func redactAny(v any) slog.Value {
	switch x := v.(type) {
	case nil:
		return slog.AnyValue(nil)
	case error:
		return slog.StringValue(Redact(safeText(x, x.Error)))
	case []byte:
		return slog.StringValue(Redact(string(x)))
	case fmt.Stringer:
		if _, isJSON := v.(json.Marshaler); !isJSON {
			return slog.StringValue(Redact(safeText(x, x.String)))
		}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return slog.StringValue(Redact(fmt.Sprintf("%+v", v)))
	}
	red, err := redactJSON(data)
	if err != nil {
		// json.Marshal output is always valid JSON; keep a safe fallback.
		return slog.StringValue(Redact(string(data)))
	}
	return slog.AnyValue(json.RawMessage(red))
}

// ---------------------------------------------------------------- writers

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// fanout writes every line to each sink; a failing sink does not stop the
// others (a closed terminal must not stop the log file). The first error
// is returned.
type fanout []io.Writer

func (f fanout) Write(p []byte) (int, error) {
	var first error
	for _, w := range f {
		if _, err := w.Write(p); err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		return 0, first
	}
	return len(p), nil
}

// now is the clock of this package (tests may replace it).
var now = time.Now
