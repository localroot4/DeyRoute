package exec

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Response is a scripted result of Fake.
type Response struct {
	Stdout string
	Stderr string
	// ExitCode, when non-zero (and Err is nil), makes Fake return the same
	// DEY-X007 error the real runner returns for a failing program.
	ExitCode int
	// Err, when set, is returned verbatim (e.g. a DEY-X030 not-found error).
	Err error
}

// OK is a successful Response with the given stdout.
func OK(stdout string) Response { return Response{Stdout: stdout} }

// Fail is a Response for a program exiting with code and stderr.
func Fail(code int, stderr string) Response { return Response{ExitCode: code, Stderr: stderr} }

// Call is one recorded invocation of Fake.
type Call struct {
	Name  string
	Args  []string
	Stdin []byte
}

// Line returns "name arg1 arg2" (single spaces, no quoting).
func (c Call) Line() string { return CommandLine(c.Name, c.Args) }

// CommandLine joins a program name and its arguments with single spaces; it
// is the key format of Fake.
func CommandLine(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

// Fake is a scripted Runner for tests. Commands are matched by their command
// line ("name arg1 arg2"): first Handler, then exact entries (On / Responses),
// then the longest matching prefix (OnPrefix / Prefixes), then Default. An
// absolute program path also matches entries written with its base name, so
// "/var/lib/deyroute/bin/xray/v1/xray x25519" matches "xray x25519".
//
// Like the real runner, Fake refuses command lines that are not Permitted
// (outside the allow-list, or "xray"/"rathole" with another operation) with
// DEY-X004, and an unmatched command fails with DEY-X007 (exit status 127).
// Fake is safe for concurrent use; configure it before sharing it.
type Fake struct {
	// Responses maps an exact command line to its result.
	Responses map[string]Response
	// Prefixes maps a command-line prefix to its result; the longest wins.
	Prefixes map[string]Response
	// Handler, when set, is consulted first; ok=false falls through.
	Handler func(c Call) (r Response, ok bool)
	// Default answers unmatched commands; nil means "fail with exit 127".
	Default *Response

	mu     sync.Mutex
	queues map[string][]Response
	calls  []Call
}

// NewFake returns an empty Fake.
func NewFake() *Fake { return &Fake{} }

// On scripts the exact command line. With several responses they are
// returned in order and the last one repeats.
func (f *Fake) On(line string, rs ...Response) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(rs) == 0 {
		rs = []Response{{}}
	}
	if f.queues == nil {
		f.queues = map[string][]Response{}
	}
	f.queues[line] = append([]Response(nil), rs...)
	return f
}

// OnPrefix scripts every command line starting with prefix.
func (f *Fake) OnPrefix(prefix string, r Response) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Prefixes == nil {
		f.Prefixes = map[string]Response{}
	}
	f.Prefixes[prefix] = r
	return f
}

// Run implements Runner.
func (f *Fake) Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, []byte, error) {
	c := Call{Name: name, Args: append([]string(nil), args...)}
	if stdin != nil {
		c.Stdin = append([]byte{}, stdin...)
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()

	desc := Describe(name, args)
	if !Permitted(name, args) {
		return nil, nil, deyerr.New(deyerr.X004, deyerr.Params{"command": desc})
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, deyerr.Wrap(deyerr.X031, err, deyerr.Params{"command": desc})
	}
	r := f.match(c)
	var so, se []byte
	if r.Stdout != "" {
		so = []byte(r.Stdout)
	}
	if r.Stderr != "" {
		se = []byte(r.Stderr)
	}
	switch {
	case r.Err != nil:
		return so, se, r.Err
	case r.ExitCode != 0:
		xe := &ExitError{Command: desc, Code: r.ExitCode, Stderr: trimStderr(se)}
		return so, se, deyerr.Wrap(deyerr.X007, xe, deyerr.Params{"command": desc}).WithDetail(xe.Stderr)
	}
	return so, se, nil
}

func (f *Fake) match(c Call) Response {
	if f.Handler != nil {
		if r, ok := f.Handler(c); ok {
			return r
		}
	}
	lines := []string{CommandLine(c.Name, c.Args)}
	if filepath.IsAbs(c.Name) {
		lines = append(lines, CommandLine(filepath.Base(c.Name), c.Args))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range lines {
		if q, ok := f.queues[l]; ok {
			r := q[0]
			if len(q) > 1 {
				f.queues[l] = q[1:]
			}
			return r
		}
		if r, ok := f.Responses[l]; ok {
			return r
		}
	}
	best, found := "", false
	var br Response
	for _, l := range lines {
		for p, r := range f.Prefixes {
			if strings.HasPrefix(l, p) && (!found || len(p) > len(best)) {
				best, br, found = p, r, true
			}
		}
	}
	if found {
		return br
	}
	if f.Default != nil {
		return *f.Default
	}
	return Response{ExitCode: 127, Stderr: "exec.Fake: no scripted response for " + lines[0]}
}

// Calls returns a copy of every recorded call in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// Lines returns the command lines of every recorded call in order.
func (f *Fake) Lines() []string {
	calls := f.Calls()
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Line()
	}
	return out
}

// Called reports whether the exact command line was run.
func (f *Fake) Called(line string) bool {
	for _, l := range f.Lines() {
		if l == line {
			return true
		}
	}
	return false
}

// Count returns how many times the exact command line was run.
func (f *Fake) Count(line string) int {
	n := 0
	for _, l := range f.Lines() {
		if l == line {
			n++
		}
	}
	return n
}

// Reset forgets recorded calls (scripted responses are kept).
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// Scripted returns the scripted exact command lines, sorted (for debugging
// test failures).
func (f *Fake) Scripted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for l := range f.queues {
		out = append(out, l)
	}
	for l := range f.Responses {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
