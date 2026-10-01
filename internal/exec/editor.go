package exec

import (
	"context"
	stderrors "errors"
	"io"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"syscall"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Editors are the text editors `deyroute config edit` may open besides the
// program named by the owner's $EDITOR (spec section 14: "edit opens
// $EDITOR"). They are a separate, interactive allow-list: Runner never runs
// them, and RunEditor runs nothing else.
var Editors = []string{"vi", "vim", "nano"}

// DefaultEditor is opened when $EDITOR is empty.
const DefaultEditor = "vi"

// EditorEnv is the environment variable naming the owner's editor.
const EditorEnv = "EDITOR"

// EditorCommand splits the value of $EDITOR into a program and its
// arguments ("vim -u NONE", "code --wait"); an empty value means
// DefaultEditor.
func EditorCommand(editorEnv string) (name string, args []string) {
	f := strings.Fields(editorEnv)
	if len(f) == 0 {
		return DefaultEditor, nil
	}
	return f[0], f[1:]
}

// EditorAllowed reports whether name may be started as the config editor:
// its base name is vi, vim, nano or the base name of the program $EDITOR
// names (editorEnv). Relative paths containing a slash are never allowed.
func EditorAllowed(name, editorEnv string) bool {
	if name == "" || (containsSlash(name) && !filepath.IsAbs(name)) {
		return false
	}
	base := filepath.Base(filepath.Clean(name))
	for _, e := range Editors {
		if e == base {
			return true
		}
	}
	prog, _ := EditorCommand(editorEnv)
	if prog == "" || (containsSlash(prog) && !filepath.IsAbs(prog)) {
		return false
	}
	return filepath.Base(filepath.Clean(prog)) == base
}

// RunEditor opens path in the owner's editor ($EDITOR, given as editorEnv;
// "" = vi) with the terminal attached and waits until it exits. It is the
// only interactive program deyroute starts. Errors: DEY-X004 when the editor
// is not allowed (EditorAllowed), DEY-X030 when it is not installed,
// DEY-X007 when it exits with a non-zero status and DEY-X031 when ctx ends
// first.
func RunEditor(ctx context.Context, editorEnv, path string, stdin io.Reader, stdout, stderr io.Writer) error {
	name, args := EditorCommand(editorEnv)
	desc := Describe(name, append(append([]string(nil), args...), path))
	if !EditorAllowed(name, editorEnv) {
		return deyerr.New(deyerr.X004, deyerr.Params{"command": desc})
	}
	bin := name
	if !filepath.IsAbs(name) {
		p, err := osexec.LookPath(name)
		if err != nil || !filepath.IsAbs(p) {
			return notFound(name, osexec.ErrNotFound)
		}
		bin = p
	}
	cmd := osexec.CommandContext(ctx, bin, append(args, path)...)
	cmd.Env = editorEnvironment(os.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = DefaultWaitDelay
	err := cmd.Run()
	if err == nil {
		return nil
	}
	params := deyerr.Params{"command": desc}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return deyerr.Wrap(deyerr.X031, ctxErr, params)
	}
	var ee *osexec.ExitError
	if stderrors.As(err, &ee) {
		return deyerr.Wrap(deyerr.X007, &ExitError{Command: desc, Code: ee.ExitCode()}, params)
	}
	if stderrors.Is(err, fs.ErrNotExist) {
		return notFound(name, err)
	}
	return deyerr.Wrap(deyerr.X007, err, params)
}

// editorEnvironment keeps the owner's locale and terminal settings (the
// editor must display UTF-8 text) and only drops the service manager's
// per-process variables, like ChildEnv.
func editorEnvironment(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "NOTIFY_SOCKET", "WATCHDOG_USEC", "WATCHDOG_PID",
			"LISTEN_FDS", "LISTEN_PID", "LISTEN_FDNAMES":
			continue
		}
		out = append(out, kv)
	}
	return out
}
