package log

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// withFDLimit runs fn with the soft limit of open descriptors set to the
// ones open now plus extra, as a daemon near its LimitNOFILE sees it: a
// walk that keeps one descriptor per directory level fails with EMFILE.
func withFDLimit(t *testing.T, extra uint64, fn func()) {
	t.Helper()
	open, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	var old syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &old))
	lim := old
	lim.Cur = uint64(len(open)) + extra
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim))
	defer func() { require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &old)) }()
	fn()
}

// deepDir creates top and a chain of depth directories below it, with a
// link to victim and a file at the bottom, as the backends' user can at a
// log name. It works through descriptors, so the length of the path never
// matters.
func deepDir(t *testing.T, top string, depth int, victim string) {
	t.Helper()
	require.NoError(t, os.Mkdir(top, 0o700))
	fd, err := unix.Open(top, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	require.NoError(t, err)
	for range depth {
		require.NoError(t, unix.Mkdirat(fd, "d", 0o700))
		next, err := unix.Openat(fd, "d", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		require.NoError(t, err)
		require.NoError(t, unix.Close(fd))
		fd = next
	}
	require.NoError(t, unix.Symlinkat(victim, fd, "link"))
	ffd, err := unix.Openat(fd, "file", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, 0o600)
	require.NoError(t, err)
	require.NoError(t, unix.Close(ffd))
	require.NoError(t, unix.Close(fd))
}

// A backend can plant a directory chain of any depth at a log name before
// the services start (a million levels take it tens of seconds). Deleting
// it inline, as os.RemoveAll does with a descriptor and a stack frame per
// level, would hold up the start of the units for as long as the backend
// likes, or fail at LimitNOFILE and keep the unit from starting. The
// daemon renames it aside at once (<name>.removed-<16 hex digits>, a later
// SweepRemoved deletes it) and creates its own file; what the tree links to
// is never touched.
func TestEnsureTunnelLogDeepPlantedDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "tunnels")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Chmod(dir, os.ModeSticky|0o770))
	victim := filepath.Join(base, "cron")
	require.NoError(t, os.WriteFile(victim, victimData, 0o644))
	p := filepath.Join(dir, "t1.log")
	deepDir(t, p, 400, victim)

	var reasons []string
	withFDLimit(t, 32, func() {
		require.NoError(t, EnsureTunnelLog(p, func(r string) { reasons = append(reasons, r) }))
	})
	require.Equal(t, []string{RefusedNotRegular}, reasons)
	fi := requireOwnLog(t, p)
	require.Zero(t, fi.Size())
	requireContent(t, victim, victimData)
	aside, err := filepath.Glob(p + ".removed-*")
	require.NoError(t, err)
	require.Len(t, aside, 1, "the planted tree is moved aside, not deleted inline")
}

// The same for the names the rotation writes: a deep directory at
// <log>.1.gz.tmp is moved aside, and the rotation completes.
func TestRotateCopyTruncateDeepPlantedDirectory(t *testing.T) {
	f := newRotFix(t)
	live := f.live(t, "t.log", liveContent)
	victim := f.file(t, "cron", victimData)
	deepDir(t, live+".1.gz.tmp", 400, victim)

	var rotated bool
	var err error
	withFDLimit(t, 32, func() { rotated, err = f.rotate(live, 10, 3) })
	require.NoError(t, err)
	require.True(t, rotated)
	requireContent(t, live, nil)
	require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
	requireAbsent(t, live+".1.gz.tmp")
	requireContent(t, victim, victimData)
	require.Equal(t, []string{"t.log staging"}, f.refusals())
}

// What was moved aside is deleted later by the rotation loop, in bounded
// steps: each SweepRemoved call spends at most its budget of removals and
// a few descriptors whatever the depth (a chain deeper than the descriptor
// limit allows to walk), a link inside is removed and never followed, a
// non-directory at such a name is unlinked, and only names moved aside
// (<name>.removed-<16 hex digits>) are touched.
func TestSweepRemovedBounded(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "tunnels")
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.Chmod(dir, os.ModeSticky|0o770))
	outside := filepath.Join(base, "etc")
	require.NoError(t, os.Mkdir(outside, 0o755))
	victim := filepath.Join(outside, "cron")
	require.NoError(t, os.WriteFile(victim, victimData, 0o644))
	snapshot := treeOf(t, outside)

	// A deep chain and a wide tree at two log names, moved aside by the
	// preparation of the logs.
	deep := filepath.Join(dir, "t1.log")
	deepDir(t, deep, 1000, victim)
	wide := filepath.Join(dir, "t2.log")
	require.NoError(t, os.Mkdir(wide, 0o700))
	for i := range 40 {
		sub := filepath.Join(wide, "s"+strconv.Itoa(i))
		require.NoError(t, os.MkdirAll(filepath.Join(sub, "a", "b"), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "a", "f"), victimData, 0o600))
		require.NoError(t, os.Symlink(outside, filepath.Join(sub, "a", "b", "dirlink")))
		require.NoError(t, os.Symlink(victim, filepath.Join(wide, "l"+strconv.Itoa(i))))
	}
	for _, p := range []string{deep, wide} {
		require.NoError(t, EnsureTunnelLog(p, func(string) {}))
	}
	// A link at a moved-aside name is unlinked, not followed.
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "t3.log.removed-0123456789abcdef")))
	// Names the sweep leaves alone: a log, a directory and look-alikes.
	keep := []string{"other.log", "keep.d", "x.removed-0123", "x.removed-0123456789ABCDEF",
		"x.removed-0123456789abcdefa", ".removed-0123456789abcdef"}
	for _, n := range keep {
		require.NoError(t, os.Mkdir(filepath.Join(dir, n), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, n, "f"), nil, 0o600))
	}

	const budget = 64
	calls, total := 0, 0
	withFDLimit(t, 16, func() {
		for ; calls < 1000; calls++ {
			spent, err := SweepRemoved(dir, budget)
			require.NoError(t, err)
			require.LessOrEqual(t, spent, budget)
			if spent == 0 {
				break
			}
			total += spent
		}
	})
	require.Less(t, calls, 1000, "everything moved aside is deleted in the end")
	require.Greater(t, calls, 10, "in steps of at most the budget (%d removals)", total)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := append([]string{"t1.log", "t2.log"}, keep...)
	sort.Strings(want)
	require.Equal(t, want, names)
	for _, n := range keep {
		requireContent(t, filepath.Join(dir, n, "f"), nil)
	}
	requireOwnLog(t, deep)
	requireOwnLog(t, wide)
	require.Equal(t, snapshot, treeOf(t, outside), "nothing a link pointed to was touched")
	requireContent(t, victim, victimData)

	spent, err := SweepRemoved(filepath.Join(base, "missing"), budget)
	require.NoError(t, err)
	require.Zero(t, spent)

	// A link at the directory's name is not followed, and like a file
	// there it is nothing to do: no tunnel log is prepared in such a
	// directory (EnsureTunnelLog refuses it, the services warn once when
	// they start), so the rotation loop does not report it every minute.
	aside := filepath.Join(outside, "x.log.removed-0123456789abcdef")
	require.NoError(t, os.Mkdir(aside, 0o700))
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(outside, link))
	file := filepath.Join(base, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	for _, p := range []string{link, file} {
		spent, err := SweepRemoved(p, budget)
		require.NoError(t, err, p)
		require.Zero(t, spent, p)
	}
	_, err = os.Lstat(aside)
	require.NoError(t, err, "nothing is removed through the link")
}

// The rotation loop deletes what was moved aside in the tunnel log
// directory and in the staging directory.
func TestRotateDirLoopSweepsRemoved(t *testing.T) {
	f := newRotFix(t)
	require.NoError(t, os.MkdirAll(f.staging, 0o700))
	victim := f.file(t, "cron", victimData)
	inLog := filepath.Join(f.dir, "t.log.1.gz.tmp.removed-00112233445566ff")
	inStaging := filepath.Join(f.staging, "tunnel-log-t.log.rotating.removed-ffeeddccbbaa0099")
	deepDir(t, inLog, 50, victim)
	deepDir(t, inStaging, 50, victim)

	var buf syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.loop(ctx, time.Millisecond, slog.New(NewHandler(&buf, slog.LevelInfo, "node")))
	}()
	gone := func(p string) bool { _, err := os.Lstat(p); return errors.Is(err, fs.ErrNotExist) }
	require.Eventually(t, func() bool { return gone(inLog) && gone(inStaging) }, 10*time.Second, 5*time.Millisecond)
	cancel()
	<-done
	requireContent(t, victim, victimData)
	for _, m := range buf.lines(t) {
		require.NotEqual(t, "warn", m[KeyLevel], "%v", m)
	}
}
