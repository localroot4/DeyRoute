// Package exec is the only package in deyroute allowed to start external
// programs (section 15). Every call goes through a Runner, which refuses any
// program that is not in the allow-list (allowlist.go), applies a timeout,
// caps captured output and turns failures into DEY errors. Tests in other
// packages use Fake instead of the real runner.
package exec

import (
	"bytes"
	"context"
	stderrors "errors"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Defaults of the real runner.
const (
	// DefaultMaxOutput caps stdout and stderr (each) at 4 MiB; anything beyond
	// is read and discarded so the child never blocks on a full pipe.
	DefaultMaxOutput = 4 << 20
	// DefaultTimeout applies when the caller's context has no deadline.
	DefaultTimeout = 2 * time.Minute
	// DefaultWaitDelay is how long a cancelled child gets between SIGTERM and
	// SIGKILL (and how long we wait for its output pipes to close).
	DefaultWaitDelay = 3 * time.Second
	// maxStderrInError bounds the stderr text carried inside errors.
	maxStderrInError = 2048
	// maxCommandParam bounds the {command} parameter of DEY errors.
	maxCommandParam = 200
)

// defaultPath is used when the environment has no PATH (e.g. a minimal
// systemd environment) and as the fallback search list of LookPath.
const defaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// searchDirs are consulted by LookPath when a program is not found in PATH
// (a non-root shell often lacks the sbin directories). Tests override it.
var searchDirs = filepath.SplitList(defaultPath)

// Runner executes one allow-listed program and returns its captured output.
// A non-nil error is always a *deyerr.Error (X004, X007, X030 or X031);
// stdout and stderr are returned even when err is non-nil.
//
// The command line becomes part of error messages (and is visible to every
// local user in /proc/<pid>/cmdline), so callers never pass secrets as
// arguments; secrets go through stdin or files.
type Runner interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

// ExitError describes a program that ran and exited with a non-zero status.
// It is the Cause of the DEY-X007 error returned by runners; use ExitCode to
// read the status.
type ExitError struct {
	Command string // program and arguments, shortened for display
	Code    int    // exit status; -1 when killed by a signal
	Stderr  string // trimmed tail of stderr
}

// Error implements error.
func (e *ExitError) Error() string {
	s := "exit status " + strconv.Itoa(e.Code)
	if e.Stderr != "" {
		s += ": " + e.Stderr
	}
	return s
}

// ExitCode returns the exit status carried by err (a runner error) and true,
// or 0 and false when the program did not run to completion.
func ExitCode(err error) (int, bool) {
	var xe *ExitError
	if stderrors.As(err, &xe) {
		return xe.Code, true
	}
	return 0, false
}

// OSRunner is the real Runner. The zero value is ready to use.
type OSRunner struct {
	// MaxOutput caps stdout and stderr (each); 0 means DefaultMaxOutput.
	MaxOutput int
	// Timeout is applied when ctx has no deadline; 0 means DefaultTimeout.
	Timeout time.Duration
	// Env replaces the child environment; nil means ChildEnv(os.Environ()).
	Env []string
	// Resolve maps a bare program name to a path; nil means LookPath.
	Resolve func(name string) (string, error)
}

// NewRunner returns the real, allow-list enforcing runner.
func NewRunner() Runner { return &OSRunner{} }

// Run implements Runner.
func (r *OSRunner) Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, []byte, error) {
	desc := Describe(name, args)
	if !Permitted(name, args) {
		return nil, nil, deyerr.New(deyerr.X004, deyerr.Params{"command": desc})
	}
	path := name
	if !filepath.IsAbs(name) {
		resolve := r.Resolve
		if resolve == nil {
			resolve = LookPath
		}
		p, err := resolve(name)
		if err != nil {
			return nil, nil, err
		}
		// The allow-list is enforced on what actually runs: a resolver may
		// only locate the requested program, never substitute another one
		// ("ip" → /bin/sh).
		if !filepath.IsAbs(p) || filepath.Base(filepath.Clean(p)) != name {
			return nil, nil, deyerr.New(deyerr.X004, deyerr.Params{"command": Describe(p, args)})
		}
		path = p
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	limit := r.MaxOutput
	if limit <= 0 {
		limit = DefaultMaxOutput
	}
	env := r.Env
	if env == nil {
		env = ChildEnv(os.Environ())
	}

	cmd := osexec.CommandContext(ctx, path, args...)
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: limit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = DefaultWaitDelay

	err := cmd.Run()
	so, se := stdout.Bytes(), stderr.Bytes()
	if err == nil {
		return so, se, nil
	}
	params := deyerr.Params{"command": desc}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return so, se, deyerr.Wrap(deyerr.X031, ctxErr, params).WithDetail(trimStderr(se))
	}
	var ee *osexec.ExitError
	if stderrors.As(err, &ee) {
		xe := &ExitError{Command: desc, Code: ee.ExitCode(), Stderr: trimStderr(se)}
		return so, se, deyerr.Wrap(deyerr.X007, xe, params).WithDetail(xe.Stderr)
	}
	if stderrors.Is(err, fs.ErrNotExist) {
		return so, se, notFound(name, err)
	}
	return so, se, deyerr.Wrap(deyerr.X007, err, params)
}

// LookPath resolves an allow-listed program name to an executable path. It
// searches PATH first and then the standard system directories, so tools in
// /usr/sbin are found even from a shell without sbin in PATH. Names outside
// the allow-list return DEY-X004; missing programs DEY-X030 (DEY-X002 for
// systemctl).
func LookPath(name string) (string, error) {
	if !IsAllowed(name) {
		return "", deyerr.New(deyerr.X004, deyerr.Params{"command": Describe(name, nil)})
	}
	if filepath.IsAbs(name) {
		if isExecutable(name) {
			return name, nil
		}
		return "", notFound(name, fs.ErrNotExist)
	}
	if p, err := osexec.LookPath(name); err == nil && filepath.IsAbs(p) {
		return p, nil
	}
	for _, dir := range searchDirs {
		p := filepath.Join(dir, name)
		if isExecutable(p) {
			return p, nil
		}
	}
	return "", notFound(name, osexec.ErrNotFound)
}

// ErrNotFound is matched (errors.Is) by the DEY-X030/X002 errors returned
// when a program is not installed.
var ErrNotFound = osexec.ErrNotFound

func notFound(name string, cause error) error {
	if !stderrors.Is(cause, osexec.ErrNotFound) {
		cause = &notFoundError{cause: cause}
	}
	if filepath.Base(name) == "systemctl" {
		return deyerr.Wrap(deyerr.X002, cause, nil)
	}
	return deyerr.Wrap(deyerr.X030, cause, deyerr.Params{"command": filepath.Base(name)})
}

// notFoundError makes fs "no such file" errors also match ErrNotFound.
type notFoundError struct{ cause error }

func (e *notFoundError) Error() string { return e.cause.Error() }

func (e *notFoundError) Unwrap() []error { return []error{e.cause, osexec.ErrNotFound} }

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// ChildEnv derives the environment of child processes from base: the locale
// is forced to C and the time zone to UTC so that output parsed by deyroute
// (systemctl timestamps, error texts) is stable, PATH gets a sane default
// when missing, and the service manager's per-process variables
// (NOTIFY_SOCKET, WATCHDOG_*, LISTEN_*) are removed: they address the deyroute
// daemon itself and must not be inherited by the programs it runs
// (sd_notify(3)).
func ChildEnv(base []string) []string {
	out := make([]string, 0, len(base)+4)
	hasPath := false
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "LC_ALL", "LANG", "LANGUAGE", "TZ",
			"NOTIFY_SOCKET", "WATCHDOG_USEC", "WATCHDOG_PID",
			"LISTEN_FDS", "LISTEN_PID", "LISTEN_FDNAMES":
			continue
		case "PATH":
			hasPath = true
		}
		out = append(out, kv)
	}
	if !hasPath {
		out = append(out, "PATH="+defaultPath)
	}
	return append(out, "LC_ALL=C", "LANG=C", "TZ=UTC")
}

// Describe renders a command line for messages: arguments containing
// whitespace or quotes are quoted and the result is shortened to 200 bytes.
func Describe(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\n\"'") {
			a = strconv.Quote(a)
		}
		parts = append(parts, a)
	}
	s := strings.Join(parts, " ")
	if len(s) > maxCommandParam {
		s = s[:runeStart(s, maxCommandParam)] + "..."
	}
	return s
}

// trimStderr keeps the tail of stderr, trimmed of surrounding whitespace.
// The cut never splits a UTF-8 sequence, so messages stay valid UTF-8 (they
// end up in JSON and the TUI).
func trimStderr(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > maxStderrInError {
		s = "..." + s[nextRuneStart(s, len(s)-maxStderrInError):]
	}
	return s
}

// runeStart moves byte index i of s back to the start of the UTF-8 sequence
// it falls into (at most utf8.UTFMax-1 bytes), so s[:i] never ends inside a
// character and is never longer than s[:i] was.
func runeStart(s string, i int) int {
	for j := 0; j < utf8.UTFMax-1 && i > 0 && i < len(s) && !utf8.RuneStart(s[i]); j++ {
		i--
	}
	return i
}

// nextRuneStart moves byte index i of s forward to the next UTF-8 sequence
// start (at most utf8.UTFMax-1 bytes), so s[i:] never begins inside a
// character and is never longer than s[i:] was.
func nextRuneStart(s string, i int) int {
	for j := 0; j < utf8.UTFMax-1 && i > 0 && i < len(s) && !utf8.RuneStart(s[i]); j++ {
		i++
	}
	return i
}

// cappedBuffer stores up to limit bytes and silently discards the rest.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	room := c.limit - c.buf.Len()
	switch {
	case room <= 0:
	case len(p) > room:
		c.buf.Write(p[:room])
	default:
		c.buf.Write(p)
	}
	return len(p), nil
}

// Bytes returns the captured data (nil when nothing was written).
func (c *cappedBuffer) Bytes() []byte {
	if c.buf.Len() == 0 {
		return nil
	}
	return c.buf.Bytes()
}
