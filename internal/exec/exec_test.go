package exec

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// script writes an executable shell script named name into a temp dir and
// returns its absolute path. name must be allow-listed for the runner to
// accept it.
func script(t *testing.T, name, body string) string {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh not available")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700)) // #nosec G306 -- test helper
	return p
}

func TestIsAllowed(t *testing.T) {
	for _, n := range Allowed {
		require.True(t, IsAllowed(n), n)
	}
	require.True(t, IsAllowed("/var/lib/deyroute/bin/xray/v26.3.27/xray"))
	require.True(t, IsAllowed("/var/lib/deyroute/bin/rathole/v0.5.0/rathole"))
	require.True(t, IsAllowed("/usr/sbin/nft"))
	require.False(t, IsAllowed(""))
	require.False(t, IsAllowed("bash"))
	require.False(t, IsAllowed("/bin/sh"))
	require.False(t, IsAllowed("./nft"))
	require.False(t, IsAllowed("bin/nft"))
	require.False(t, IsAllowed("wg"))
	require.False(t, IsAllowed("/usr/sbin/nft/"+"..")) // cleans to /usr/sbin
	require.Len(t, Allowed, 10)
}

func TestRunDisallowed(t *testing.T) {
	r := NewRunner()
	for _, n := range []string{"bash", "/bin/sh", "./ss", "curl", ""} {
		_, _, err := r.Run(context.Background(), n, []string{"-c", "true"}, nil)
		require.Error(t, err, n)
		require.True(t, deyerr.HasCode(err, deyerr.X004), "%s: %v", n, err)
	}
}

// Section 15 allows xray only as "xray x25519" and rathole only as
// "rathole --genkey"; backends run under systemd, never through exec.
func TestPermittedOperations(t *testing.T) {
	for _, ok := range [][]string{
		{"xray", "x25519"}, {"xray", "x25519", "-i", "key"}, {"/var/lib/deyroute/bin/xray/v26.3.27/xray", "x25519"},
		{"rathole", "--genkey"}, {"rathole", "--genkey", "x25519"}, {"systemctl"}, {"nft", "-f", "-"},
	} {
		require.True(t, Permitted(ok[0], ok[1:]), "%v", ok)
	}
	for _, bad := range [][]string{
		{"xray"}, {"xray", "run", "-c", "/tmp/c.json"}, {"/opt/xray", "version"}, {"rathole", "server.toml"},
		{"rathole"}, {"bash", "-c", "id"}, {"./ip", "link"},
	} {
		require.False(t, Permitted(bad[0], bad[1:]), "%v", bad)
	}

	p := script(t, "xray", "echo should-not-run")
	_, _, err := NewRunner().Run(context.Background(), p, []string{"run", "-c", "x.json"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X004), "%v", err)
	out, _, err := NewRunner().Run(context.Background(), p, []string{"x25519"}, nil)
	require.NoError(t, err)
	require.Equal(t, "should-not-run\n", string(out))

	_, _, err = NewFake().On("xray run").Run(context.Background(), "xray", []string{"run"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X004), "%v", err)
}

func TestRunSuccess(t *testing.T) {
	p := script(t, "ss", `echo "args:$*"; cat; echo "warn" >&2`)
	stdout, stderr, err := NewRunner().Run(context.Background(), p, []string{"-tlnp", "a b"}, []byte("from-stdin\n"))
	require.NoError(t, err)
	require.Equal(t, "args:-tlnp a b\nfrom-stdin\n", string(stdout))
	require.Equal(t, "warn\n", string(stderr))
}

func TestRunNoOutput(t *testing.T) {
	p := script(t, "ip", `exit 0`)
	stdout, stderr, err := NewRunner().Run(context.Background(), p, nil, nil)
	require.NoError(t, err)
	require.Nil(t, stdout)
	require.Nil(t, stderr)
}

func TestRunChildEnvironment(t *testing.T) {
	t.Setenv("LC_ALL", "fa_IR.UTF-8")
	t.Setenv("TZ", "Asia/Tehran")
	p := script(t, "ip", `echo "$LC_ALL|$TZ|$LANG"`)
	stdout, _, err := NewRunner().Run(context.Background(), p, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "C|UTC|C\n", string(stdout))
}

func TestRunNonZeroExit(t *testing.T) {
	p := script(t, "nft", `echo partial; echo "  Error: No such file or directory  " >&2; exit 3`)
	stdout, stderr, err := NewRunner().Run(context.Background(), p, []string{"delete", "table", "inet", "deyroute"}, nil)
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, deyerr.X007), err.Error())
	require.Equal(t, "partial\n", string(stdout))
	require.Contains(t, string(stderr), "No such file")

	code, ok := ExitCode(err)
	require.True(t, ok)
	require.Equal(t, 3, code)

	de := deyerr.As(err)
	require.Contains(t, de.Message(), "nft delete table inet deyroute")
	require.Equal(t, "Error: No such file or directory", de.Detail)
	require.Contains(t, err.Error(), "exit status 3: Error: No such file or directory")

	var xe *ExitError
	require.True(t, stderrors.As(err, &xe))
	require.Equal(t, 3, xe.Code)
}

func TestRunNonZeroWithoutStderr(t *testing.T) {
	p := script(t, "ufw", `exit 1`)
	_, _, err := NewRunner().Run(context.Background(), p, []string{"status"}, nil)
	require.Error(t, err)
	var xe *ExitError
	require.True(t, stderrors.As(err, &xe))
	require.Equal(t, "exit status 1", xe.Error())
}

func TestRunContextDeadline(t *testing.T) {
	p := script(t, "ss", `exec sleep 10`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := NewRunner().Run(ctx, p, nil, nil)
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, deyerr.X031), err.Error())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)
	_, ok := ExitCode(err)
	require.False(t, ok)
}

func TestRunDefaultTimeout(t *testing.T) {
	p := script(t, "ss", `exec sleep 10`)
	r := &OSRunner{Timeout: 150 * time.Millisecond}
	_, _, err := r.Run(context.Background(), p, nil, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X031), "%v", err)
}

func TestRunOutputCap(t *testing.T) {
	p := script(t, "ss", `i=0; while [ $i -lt 100 ]; do printf 0123456789; printf abc >&2; i=$((i+1)); done`)
	r := &OSRunner{MaxOutput: 25}
	stdout, stderr, err := r.Run(context.Background(), p, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "0123456789012345678901234", string(stdout))
	require.Len(t, stderr, 25)
}

func TestRunMissingAbsolute(t *testing.T) {
	_, _, err := NewRunner().Run(context.Background(), filepath.Join(t.TempDir(), "xray"), []string{"x25519"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X030), "%v", err)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestRunNotExecutable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rathole")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600))
	_, _, err := NewRunner().Run(context.Background(), p, []string{"--genkey"}, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X007), "%v", err)
}

func TestRunResolve(t *testing.T) {
	p := script(t, "systemctl", `echo "systemd 255 (255.4-1)"`)
	r := &OSRunner{Resolve: func(name string) (string, error) {
		require.Equal(t, "systemctl", name)
		return p, nil
	}}
	out, _, err := r.Run(context.Background(), "systemctl", []string{"--version"}, nil)
	require.NoError(t, err)
	require.Equal(t, "systemd 255 (255.4-1)\n", string(out))

	r = &OSRunner{Resolve: func(string) (string, error) { return "", deyerr.New(deyerr.X002, nil) }}
	_, _, err = r.Run(context.Background(), "systemctl", nil, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X002))
}

// A resolver may locate the requested program but never substitute another
// one: the allow-list applies to what actually executes.
func TestRunResolveCannotSubstitute(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	sh := script(t, "sh", "touch "+marker)
	nft := script(t, "nft", "touch "+marker)
	for _, resolved := range []string{sh, nft, "relative/ip", "ip"} {
		r := &OSRunner{Resolve: func(string) (string, error) { return resolved, nil }}
		_, _, err := r.Run(context.Background(), "ip", []string{"link"}, nil)
		require.True(t, deyerr.HasCode(err, deyerr.X004), "%s: %v", resolved, err)
	}
	_, err := os.Stat(marker)
	require.True(t, os.IsNotExist(err), "a substituted program was executed")
}

func TestRunBareNameViaPath(t *testing.T) {
	p := script(t, "iptables", `echo ok`)
	t.Setenv("PATH", filepath.Dir(p))
	old := searchDirs
	searchDirs = nil
	t.Cleanup(func() { searchDirs = old })
	out, _, err := NewRunner().Run(context.Background(), "iptables", []string{"-S"}, nil)
	require.NoError(t, err)
	require.Equal(t, "ok\n", string(out))
}

func TestLookPath(t *testing.T) {
	dir := t.TempDir()
	nft := filepath.Join(dir, "nft")
	require.NoError(t, os.WriteFile(nft, []byte("#!/bin/sh\n"), 0o700)) // #nosec G306 -- test fixture
	notExec := filepath.Join(dir, "ss")
	require.NoError(t, os.WriteFile(notExec, []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "ip"), 0o700))

	t.Setenv("PATH", t.TempDir()) // empty PATH directory
	old := searchDirs
	searchDirs = []string{dir}
	t.Cleanup(func() { searchDirs = old })

	p, err := LookPath("nft")
	require.NoError(t, err)
	require.Equal(t, nft, p)

	p, err = LookPath(nft)
	require.NoError(t, err)
	require.Equal(t, nft, p)

	_, err = LookPath("bash")
	require.True(t, deyerr.HasCode(err, deyerr.X004))

	for _, n := range []string{"ss", "ip", "iptables", notExec} {
		_, err = LookPath(n)
		require.True(t, deyerr.HasCode(err, deyerr.X030), "%s: %v", n, err)
		require.ErrorIs(t, err, ErrNotFound)
		require.Contains(t, deyerr.As(err).Message(), filepath.Base(n))
	}

	_, err = LookPath("systemctl")
	require.True(t, deyerr.HasCode(err, deyerr.X002), "%v", err)
	require.ErrorIs(t, err, ErrNotFound)

	// Found through PATH.
	t.Setenv("PATH", dir)
	searchDirs = nil
	p, err = LookPath("nft")
	require.NoError(t, err)
	require.Equal(t, nft, p)
}

func TestChildEnv(t *testing.T) {
	env := ChildEnv([]string{"HOME=/root", "LC_ALL=de_DE", "LANG=de", "LANGUAGE=de", "TZ=Asia/Tehran",
		"NOTIFY_SOCKET=/x", "WATCHDOG_USEC=30000000", "WATCHDOG_PID=1", "LISTEN_FDS=1", "LISTEN_PID=1", "LISTEN_FDNAMES=a",
		"DEYROUTE_DEBUG=1"})
	require.Equal(t, []string{"HOME=/root", "DEYROUTE_DEBUG=1", "PATH=" + defaultPath, "LC_ALL=C", "LANG=C", "TZ=UTC"}, env)

	env = ChildEnv([]string{"PATH=/bin"})
	require.Equal(t, []string{"PATH=/bin", "LC_ALL=C", "LANG=C", "TZ=UTC"}, env)
}

func TestDescribe(t *testing.T) {
	require.Equal(t, "systemctl start deyroute-hub.service", Describe("systemctl", []string{"start", "deyroute-hub.service"}))
	require.Equal(t, `nft -f "" "a b" "it's"`, Describe("nft", []string{"-f", "", "a b", "it's"}))
	long := Describe("ip", []string{strings.Repeat("x", 500)})
	require.Len(t, long, maxCommandParam+3)
	require.True(t, strings.HasSuffix(long, "..."))

	// Truncation never splits a multi-byte character.
	for pad := 0; pad < 4; pad++ {
		d := Describe("ip", []string{strings.Repeat("x", pad) + strings.Repeat("ایران", 100)})
		require.True(t, utf8.ValidString(d), "pad %d: %q", pad, d)
		require.LessOrEqual(t, len(d), maxCommandParam+3)
	}
}

func TestRunChildEnvironmentStripsNotify(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "/run/systemd/notify")
	t.Setenv("WATCHDOG_USEC", "30000000")
	p := script(t, "ip", `echo "[$NOTIFY_SOCKET][$WATCHDOG_USEC]"`)
	stdout, _, err := NewRunner().Run(context.Background(), p, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "[][]\n", string(stdout))
}

func TestTrimStderr(t *testing.T) {
	require.Equal(t, "", trimStderr(nil))
	require.Equal(t, "a", trimStderr([]byte("\n a \n")))
	big := strings.Repeat("y", maxStderrInError) + "END"
	got := trimStderr([]byte(big))
	require.True(t, strings.HasPrefix(got, "..."))
	require.True(t, strings.HasSuffix(got, "END"))
	require.Len(t, got, maxStderrInError+3)

	for pad := 0; pad < 4; pad++ {
		got = trimStderr([]byte(strings.Repeat("خطا", 1000) + strings.Repeat("y", pad)))
		require.True(t, utf8.ValidString(got), "pad %d", pad)
		require.True(t, strings.HasPrefix(got, "..."))
		require.LessOrEqual(t, len(got), maxStderrInError+3)
	}
	require.Equal(t, 0, runeStart("", 0))
	require.Equal(t, 3, runeStart("abc", 3))
	require.Equal(t, 1, runeStart("aخ", 2))
	require.Equal(t, 3, nextRuneStart("aخb", 2))
	require.Equal(t, 0, nextRuneStart("abc", 0))
}

func TestExitCodeHelper(t *testing.T) {
	_, ok := ExitCode(nil)
	require.False(t, ok)
	_, ok = ExitCode(stderrors.New("x"))
	require.False(t, ok)
	code, ok := ExitCode(deyerr.Wrap(deyerr.X007, &ExitError{Code: 5}, nil))
	require.True(t, ok)
	require.Equal(t, 5, code)
}

func TestCappedBuffer(t *testing.T) {
	c := &cappedBuffer{limit: 4}
	n, err := c.Write([]byte("ab"))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	n, _ = c.Write([]byte("cdef"))
	require.Equal(t, 4, n)
	n, _ = c.Write([]byte("gh"))
	require.Equal(t, 2, n)
	require.Equal(t, "abcd", string(c.Bytes()))
	require.Nil(t, (&cappedBuffer{limit: 1}).Bytes())
}
