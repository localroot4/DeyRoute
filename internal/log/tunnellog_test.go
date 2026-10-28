package log

import (
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// requireOwnLog checks that p is what EnsureTunnelLog leaves for systemd:
// a regular file of this process's user with a single link.
func requireOwnLog(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Lstat(p)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular(), "%s is %v", p, fi.Mode())
	st, ok := fi.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, uint64(1), uint64(st.Nlink), "%s has one link", p)
	require.Equal(t, os.Geteuid(), int(st.Uid), "%s belongs to the daemon's user", p)
	return fi
}

// A backend (user deyroute) can add names to the tunnel log directory.
// Before systemd opens <tunnel>.log as root for StandardOutput=append: (it
// follows a link and creates a missing file), the daemon leaves only its
// own regular file with one link there: anything else is removed (a
// directory with all it holds, a link inside never followed), reported with
// its reason, and replaced by a new empty file. What a link pointed to is
// never touched.
func TestEnsureTunnelLogReplacesPlantedLink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		// plant puts something at p; victim is a file outside the log
		// directory that must stay as it is.
		plant func(t *testing.T, p, victim, outside string)
	}{
		{"link to a root file", RefusedSymlink, func(t *testing.T, p, victim, _ string) {
			require.NoError(t, os.Symlink(victim, p))
		}},
		{"link to a missing file", RefusedSymlink, func(t *testing.T, p, _, outside string) {
			require.NoError(t, os.Symlink(filepath.Join(outside, "cron.d-new"), p))
		}},
		{"link to a directory", RefusedSymlink, func(t *testing.T, p, _, outside string) {
			require.NoError(t, os.Symlink(outside, p))
		}},
		{"FIFO", RefusedNotRegular, func(t *testing.T, p, _, _ string) {
			require.NoError(t, unix.Mkfifo(p, 0o666))
		}},
		{"socket", RefusedNotRegular, func(t *testing.T, p, _, _ string) {
			ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
			require.NoError(t, err)
			ln.SetUnlinkOnClose(false) // the name is the daemon's log by then
			t.Cleanup(func() { _ = ln.Close() })
		}},
		{"directory with a link inside", RefusedNotRegular, func(t *testing.T, p, victim, outside string) {
			require.NoError(t, os.MkdirAll(filepath.Join(p, "sub"), 0o777))
			require.NoError(t, os.Symlink(victim, filepath.Join(p, "sub", "link")))
			require.NoError(t, os.Symlink(outside, filepath.Join(p, "dirlink")))
		}},
		{"second hard link", RefusedHardlink, func(t *testing.T, p, _, outside string) {
			mine := filepath.Join(outside, "mine")
			require.NoError(t, os.WriteFile(mine, victimData, 0o644))
			require.NoError(t, os.Link(mine, p))
		}},
		{"another owner", RefusedOwner, func(t *testing.T, p, _, _ string) {
			if os.Geteuid() != 0 {
				t.Skip("chown to another user needs root")
			}
			require.NoError(t, os.WriteFile(p, []byte("planted by the backend\n"), 0o644))
			require.NoError(t, os.Lchown(p, 65534, 65534))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "log", "tunnels")
			outside := filepath.Join(base, "etc")
			require.NoError(t, os.MkdirAll(dir, 0o770))
			require.NoError(t, os.MkdirAll(outside, 0o755))
			victim := filepath.Join(outside, "cron")
			require.NoError(t, os.WriteFile(victim, victimData, 0o644))
			p := filepath.Join(dir, "t1.log")
			tc.plant(t, p, victim, outside)
			snapshot := treeOf(t, outside)

			var reasons []string
			require.NoError(t, EnsureTunnelLog(p, func(r string) { reasons = append(reasons, r) }))
			require.Equal(t, []string{tc.reason}, reasons)
			fi := requireOwnLog(t, p)
			require.Zero(t, fi.Size(), "a new, empty log")
			require.Equal(t, TunnelLogFileMode, fi.Mode().Perm())
			requireContent(t, victim, victimData)
			require.Equal(t, snapshot, treeOf(t, outside), "nothing outside the log directory changed")

			// The next start finds the daemon's own file and keeps it.
			require.NoError(t, os.WriteFile(p, []byte("a line\n"), 0o644))
			reasons = nil
			require.NoError(t, EnsureTunnelLog(p, func(r string) { reasons = append(reasons, r) }))
			require.Empty(t, reasons)
			requireContent(t, p, []byte("a line\n"))
		})
	}
}

// treeOf lists the names and sizes below dir (links not followed).
func treeOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		out[p] = fi.Mode().String() + " " + strconv.FormatInt(fi.Size(), 10)
		return nil
	}))
	return out
}

// A missing tunnel log is created as systemd would create it (mode 0644,
// whatever the daemon's umask), exclusively and without following a link;
// an existing log of the daemon's user is kept as it is.
func TestEnsureTunnelLogCreatesRegularFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "t1.log")
	old := syscall.Umask(0o077)
	err := EnsureTunnelLog(p, func(r string) { t.Errorf("unexpected refusal %s", r) })
	syscall.Umask(old)
	require.NoError(t, err)
	fi := requireOwnLog(t, p)
	require.Equal(t, TunnelLogFileMode, fi.Mode().Perm())
	require.Zero(t, fi.Size())

	// An existing log keeps its content and mode.
	q := filepath.Join(dir, "t2.log")
	require.NoError(t, os.WriteFile(q, []byte("backend output\n"), 0o600))
	require.NoError(t, EnsureTunnelLog(q, func(r string) { t.Errorf("unexpected refusal %s", r) }))
	fi = requireOwnLog(t, q)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	requireContent(t, q, []byte("backend output\n"))

	// A missing directory is an error, never a reason to create it here.
	err = EnsureTunnelLog(filepath.Join(dir, "missing", "t3.log"), func(string) {})
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// The daemons log each replaced tunnel log once per file and reason while
// they run, with the keys file and reason, and remember a bounded number
// of pairs.
func TestWarnOnceLogsOncePerFileAndReason(t *testing.T) {
	var buf syncBuffer
	w := NewWarnOnce(slog.New(NewHandler(&buf, slog.LevelInfo, "node")), TunnelLogReplaced)
	w.Warn("/var/log/deyroute/tunnels/a.log", RefusedSymlink)
	w.Warn("/var/log/deyroute/tunnels/a.log", RefusedSymlink)
	w.Warn("/var/log/deyroute/tunnels/a.log", RefusedOwner)
	w.Warn("/var/log/deyroute/tunnels/b.log", RefusedSymlink)
	var got []string
	for _, m := range buf.lines(t) {
		require.Equal(t, "tunnel log replaced", m[KeyMsg])
		require.Equal(t, "warn", m[KeyLevel])
		got = append(got, m["file"].(string)+" "+m["reason"].(string))
	}
	require.Equal(t, []string{
		"/var/log/deyroute/tunnels/a.log symlink",
		"/var/log/deyroute/tunnels/a.log owner",
		"/var/log/deyroute/tunnels/b.log symlink",
	}, got)

	for i := range maxRefusalWarnings + 10 {
		w.Warn("/x/"+strconv.Itoa(i)+".log", RefusedSymlink)
	}
	require.Len(t, buf.lines(t), maxRefusalWarnings, "at most maxRefusalWarnings pairs are remembered and logged")
}

// OpenLogFile opens only a regular file: a link (to a file or a directory),
// a FIFO, a socket and a directory at the name are ErrNotRegular, at once.
func TestOpenLogFileAcceptsOnlyRegular(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(victim, victimData, 0o600))

	p := filepath.Join(dir, "t.log")
	require.NoError(t, os.WriteFile(p, []byte("a\nb\n"), 0o600))
	f, fi, err := OpenLogFile(p)
	require.NoError(t, err)
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Equal(t, "a\nb\n", string(data))
	require.Equal(t, int64(4), fi.Size())

	_, _, err = OpenLogFile(filepath.Join(dir, "missing.log"))
	require.ErrorIs(t, err, fs.ErrNotExist)

	link := filepath.Join(dir, "link.log")
	require.NoError(t, os.Symlink(victim, link))
	dirLink := filepath.Join(dir, "dirlink.log")
	require.NoError(t, os.Symlink(dir, dirLink))
	fifo := filepath.Join(dir, "fifo.log")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	sub := filepath.Join(dir, "dir.log")
	require.NoError(t, os.Mkdir(sub, 0o700))
	sock := filepath.Join(dir, "sock.log")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	for _, name := range []string{link, dirLink, fifo, sub, sock} {
		f, fi, err := OpenLogFile(name)
		require.ErrorIs(t, err, ErrNotRegular, name)
		require.Nil(t, f)
		require.Nil(t, fi)
	}
	requireContent(t, victim, victimData)
}
