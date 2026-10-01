package exec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestEditorCommand(t *testing.T) {
	name, args := EditorCommand("")
	require.Equal(t, DefaultEditor, name)
	require.Empty(t, args)
	name, args = EditorCommand("  vim -u NONE ")
	require.Equal(t, "vim", name)
	require.Equal(t, []string{"-u", "NONE"}, args)
}

func TestEditorAllowed(t *testing.T) {
	for _, n := range []string{"vi", "vim", "nano", "/usr/bin/vim", "/bin/nano"} {
		require.True(t, EditorAllowed(n, ""), n)
	}
	require.True(t, EditorAllowed("emacs", "emacs -nw"))
	require.True(t, EditorAllowed("/usr/bin/micro", "micro"))
	require.True(t, EditorAllowed("/opt/ed/myedit", "/opt/ed/myedit --wait"))
	for _, n := range []string{"", "bash", "sh", "./vi", "bin/vim", "emacs"} {
		require.False(t, EditorAllowed(n, ""), n)
	}
	require.False(t, EditorAllowed("bash", "emacs"))
	require.False(t, EditorAllowed("./myedit", "./myedit"))
	// The editor allow-list never widens the runner's allow-list.
	require.False(t, IsAllowed("vi"))
	require.False(t, Permitted("nano", nil))
}

func TestRunEditor(t *testing.T) {
	editor := script(t, "myedit", `echo "edited" >> "$1"; echo "tty-out"; echo "tty-err" >&2`)
	file := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("a: 1\n"), 0o600))
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, RunEditor(ctx, editor, file, bytes.NewReader(nil), &out, &errOut))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "a: 1\nedited\n", string(data))
	require.Equal(t, "tty-out\n", out.String())
	require.Equal(t, "tty-err\n", errOut.String())

	// Arguments from $EDITOR are passed before the file.
	argEditor := script(t, "argedit", `echo "$1" > "$2"`)
	require.NoError(t, RunEditor(ctx, argEditor+" --flag", file, nil, &out, &errOut))
	data, err = os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "--flag\n", string(data))
}

func TestRunEditorErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := RunEditor(ctx, "./myedit", "/tmp/x", nil, &out, &out)
	require.True(t, deyerr.HasCode(err, deyerr.X004), "%v", err)

	err = RunEditor(ctx, filepath.Join(t.TempDir(), "vi"), "/tmp/x", nil, &out, &out)
	require.True(t, deyerr.HasCode(err, deyerr.X030), "%v", err)

	failing := script(t, "failedit", `exit 3`)
	err = RunEditor(ctx, failing, "/tmp/x", nil, &out, &out)
	require.True(t, deyerr.HasCode(err, deyerr.X007), "%v", err)
	code, ok := ExitCode(err)
	require.True(t, ok)
	require.Equal(t, 3, code)

	slow := script(t, "slowedit", `sleep 30`)
	short, cancelShort := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelShort()
	err = RunEditor(short, slow, "/tmp/x", nil, &out, &out)
	require.True(t, deyerr.HasCode(err, deyerr.X031), "%v", err)

	t.Setenv("PATH", t.TempDir())
	err = RunEditor(ctx, "", "/tmp/x", nil, &out, &out)
	require.True(t, deyerr.HasCode(err, deyerr.X030), "%v", err)
}

func TestEditorEnvironment(t *testing.T) {
	env := editorEnvironment([]string{"LANG=fa_IR.UTF-8", "NOTIFY_SOCKET=/run/x", "TERM=xterm", "WATCHDOG_USEC=1", "LISTEN_FDS=2"})
	require.Equal(t, []string{"LANG=fa_IR.UTF-8", "TERM=xterm"}, env)
}
