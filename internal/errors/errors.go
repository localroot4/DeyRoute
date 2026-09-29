// Package errors implements the DEY-<letter><3 digits> error system.
//
// Every error shown to the owner carries a stable code plus three fixed
// lines (what happened, why, how to fix) taken from the catalog in codes.go.
// Messages are templates: "{name}" placeholders are filled from Params.
package errors

import (
	stderrors "errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Code is a stable error identifier such as "DEY-P012".
type Code string

// Exit codes for the non-interactive CLI (section 14).
const (
	ExitOK            = 0
	ExitUser          = 1 // DEY-C/P/T/N/B/F/S/I errors
	ExitSystem        = 2 // DEY-X errors
	ExitNeedConfirm   = 3 // destructive action without --yes on a non-TTY
	DefaultLogPath    = "/var/log/deyroute/deyroute.log"
	placeholderMarker = "?"
)

// Info is one catalog entry. Message, Why and Fix are mandatory; CI fails
// (see TestCatalogComplete) when any of them is empty.
type Info struct {
	Code    Code
	Message string
	Why     string
	Fix     string
}

// Params fills "{key}" placeholders in catalog templates.
type Params map[string]any

// Error is a structured DEY error. It wraps an optional cause.
type Error struct {
	Code   Code
	Params Params
	// Overrides; when empty the catalog template is used.
	MsgOverride string
	WhyOverride string
	FixOverride string
	// LogPath is the log file the owner should search; defaults to DefaultLogPath.
	LogPath string
	// Detail is extra multi-line context (e.g. last 40 lines of a backend log).
	Detail string
	Cause  error
}

var codeRe = regexp.MustCompile(`^DEY-[A-Z][0-9]{3}$`)

// ValidCode reports whether c has the DEY-<letter><3 digits> shape.
func ValidCode(c Code) bool { return codeRe.MatchString(string(c)) }

// New builds an error for code with optional params.
func New(code Code, params Params) *Error {
	return &Error{Code: code, Params: params}
}

// Wrap builds an error for code that wraps cause.
func Wrap(code Code, cause error, params Params) *Error {
	return &Error{Code: code, Params: params, Cause: cause}
}

// WithLog sets the log path hint and returns e.
func (e *Error) WithLog(path string) *Error { e.LogPath = path; return e }

// WithDetail attaches extra context (shown under the Fix line) and returns e.
func (e *Error) WithDetail(d string) *Error { e.Detail = d; return e }

// WithFix overrides the Fix line and returns e.
func (e *Error) WithFix(fix string) *Error { e.FixOverride = fix; return e }

// WithWhy overrides the Why line and returns e.
func (e *Error) WithWhy(why string) *Error { e.WhyOverride = why; return e }

// Info returns the catalog entry; unknown codes map to DEY-X000.
func (e *Error) Info() Info {
	if in, ok := catalog[e.Code]; ok {
		return in
	}
	return catalog[X000]
}

// Message returns the rendered "what happened" line.
func (e *Error) Message() string {
	if e.MsgOverride != "" {
		return e.MsgOverride
	}
	return expand(e.Info().Message, e.Params)
}

// Why returns the rendered "why" line.
func (e *Error) Why() string {
	if e.WhyOverride != "" {
		return e.WhyOverride
	}
	return expand(e.Info().Why, e.Params)
}

// Fix returns the rendered "fix" line.
func (e *Error) Fix() string {
	if e.FixOverride != "" {
		return e.FixOverride
	}
	return expand(e.Info().Fix, e.Params)
}

// Log returns the log path hint.
func (e *Error) Log() string {
	if e.LogPath != "" {
		return e.LogPath
	}
	return DefaultLogPath
}

// Error implements error. It is a single line suitable for logs.
func (e *Error) Error() string {
	s := string(e.Code) + " " + e.Message()
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

// Unwrap exposes the cause to errors.Is/As.
func (e *Error) Unwrap() error { return e.Cause }

// Is matches another *Error with the same code.
func (e *Error) Is(target error) bool {
	var t *Error
	if stderrors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

// ExitCode maps the error category to a CLI exit code.
func (e *Error) ExitCode() int { return ExitCodeFor(e.Code) }

// ExitCodeFor maps a code to the CLI exit code (section 14).
func ExitCodeFor(c Code) int {
	if strings.HasPrefix(string(c), "DEY-X") {
		return ExitSystem
	}
	return ExitUser
}

// Format renders the canonical block used identically by UI and CLI:
//
//	✖ DEY-P012  Port 443/tcp is already in use
//	  Why:  nginx (pid 1234) is listening on 0.0.0.0:443
//	  Fix:  choose another port, or stop nginx: systemctl stop nginx
//	  Log:  /var/log/deyroute/hub.log (search DEY-P012)
//
// When unicode is false the cross is replaced with "x".
func (e *Error) Format(unicode bool) string {
	mark := "✖"
	if !unicode {
		mark = "x"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s  %s\n", mark, e.Code, e.Message())
	fmt.Fprintf(&b, "  Why:  %s\n", e.Why())
	fmt.Fprintf(&b, "  Fix:  %s\n", e.Fix())
	fmt.Fprintf(&b, "  Log:  %s (search %s)\n", e.Log(), e.Code)
	if e.Detail != "" {
		for _, l := range strings.Split(strings.TrimRight(e.Detail, "\n"), "\n") {
			b.WriteString("  | " + l + "\n")
		}
	}
	return b.String()
}

// As extracts a *Error from err. Plain errors become DEY-X000 wrapping err,
// so every error reaching the UI has a code.
func As(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if stderrors.As(err, &e) {
		return e
	}
	return Wrap(X000, err, nil)
}

// HasCode reports whether err (or anything it wraps) carries code.
func HasCode(err error, code Code) bool {
	for err != nil {
		var e *Error
		if !stderrors.As(err, &e) {
			return false
		}
		if e.Code == code {
			return true
		}
		err = e.Cause
	}
	return false
}

// Lookup returns the catalog entry for code.
func Lookup(code Code) (Info, bool) {
	in, ok := catalog[code]
	return in, ok
}

// All returns every catalog entry sorted by code.
func All() []Info {
	out := make([]Info, 0, len(catalog))
	for _, in := range catalog {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Join is re-exported so callers need a single errors import.
func Join(errs ...error) error { return stderrors.Join(errs...) }

// Is is re-exported from the standard library.
func Is(err, target error) bool { return stderrors.Is(err, target) }

// Unwrap is re-exported from the standard library.
func Unwrap(err error) error { return stderrors.Unwrap(err) }

// Plain is re-exported errors.New for internal sentinel errors.
func Plain(text string) error { return stderrors.New(text) }

var phRe = regexp.MustCompile(`\{([a-z_]+)\}`)

func expand(tpl string, p Params) string {
	return phRe.ReplaceAllStringFunc(tpl, func(m string) string {
		key := m[1 : len(m)-1]
		if v, ok := p[key]; ok {
			return fmt.Sprint(v)
		}
		return placeholderMarker
	})
}
