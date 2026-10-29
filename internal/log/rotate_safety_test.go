package log

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The tunnel log rotator runs as root over a directory the backends' user
// can write to. These tests plant what such a user could plant and check
// that the rotator never writes through it.

// rotFix is a tunnel log directory, the root-only staging directory and a
// directory of files the backends' user must never reach ("outside").
type rotFix struct {
	dir, staging, outside string
	mu                    sync.Mutex
	refused               []string // "<base name> <reason>"
}

func newRotFix(t *testing.T) *rotFix {
	t.Helper()
	base := t.TempDir()
	f := &rotFix{
		dir:     filepath.Join(base, "log", "tunnels"),
		staging: filepath.Join(base, "lib", "tmp"),
		outside: filepath.Join(base, "etc"),
	}
	require.NoError(t, os.MkdirAll(f.dir, 0o770))
	require.NoError(t, os.MkdirAll(filepath.Dir(f.staging), 0o750))
	require.NoError(t, os.MkdirAll(f.outside, 0o755))
	return f
}

func (f *rotFix) opts(maxBytes int64, keep int) CopyTruncateOptions {
	return CopyTruncateOptions{MaxBytes: maxBytes, Keep: keep, Staging: f.staging, Refused: f.refuse}
}

func (f *rotFix) refuse(file, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refused = append(f.refused, filepath.Base(file)+" "+reason)
}

func (f *rotFix) refusals() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.refused...)
	sort.Strings(out)
	return out
}

func (f *rotFix) rotate(path string, maxBytes int64, keep int) (bool, error) {
	return RotateCopyTruncate(path, f.opts(maxBytes, keep))
}

func (f *rotFix) rotateDir(maxBytes int64, keep int) ([]string, error) {
	return RotateDir(f.dir, f.opts(maxBytes, keep))
}

func (f *rotFix) loop(ctx context.Context, interval time.Duration, l *slog.Logger) {
	RotateDirLoop(ctx, f.dir, f.staging, interval, l)
}

// file writes a file of the backends' user outside the log directory (the
// target of a planted link).
func (f *rotFix) file(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(f.outside, name)
	require.NoError(t, os.WriteFile(p, data, 0o644))
	return p
}

// live writes a tunnel log of the directory.
func (f *rotFix) live(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func requireContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got), "content of %s", path)
}

func requireAbsent(t *testing.T, path string) {
	t.Helper()
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, fs.ErrNotExist, "%s must not exist", path)
}

func gunzipString(t *testing.T, path string) string {
	t.Helper()
	fh, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = fh.Close() }()
	zr, err := gzip.NewReader(fh)
	require.NoError(t, err)
	data, err := io.ReadAll(zr)
	require.NoError(t, err)
	return string(data)
}

var (
	liveContent = bytes.Repeat([]byte("live line\n"), 10)
	victimData  = []byte("* * * * * root /bin/true\n")
)

// A planted <log>.rotating link to a root file: the copy never goes through
// it, the link is removed and the log is still rotated.
func TestRotateCopyTruncateRefusesSymlinkedStaging(t *testing.T) {
	f := newRotFix(t)
	live := f.live(t, "t.log", liveContent)
	victim := f.file(t, "cron", victimData)
	require.NoError(t, os.Symlink(victim, live+".rotating"))

	rotated, err := f.rotate(live, 10, 5)
	require.NoError(t, err)
	require.True(t, rotated, "the log is still rotated")
	requireContent(t, victim, victimData)
	requireContent(t, live, nil)
	require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
	requireAbsent(t, live+".rotating")
	require.Equal(t, []string{"t.log staging"}, f.refusals())
}

// A log that is a link to a root file is neither copied nor truncated.
func TestRotateCopyTruncateRefusesSymlinkedLog(t *testing.T) {
	f := newRotFix(t)
	big := bytes.Repeat([]byte("v"), 100)
	victim := f.file(t, "shadow", big)
	live := filepath.Join(f.dir, "t.log")
	require.NoError(t, os.Symlink(victim, live))

	rotated, err := f.rotate(live, 10, 5)
	require.NoError(t, err)
	require.False(t, rotated)
	requireContent(t, victim, big)
	requireAbsent(t, live+".1.gz")
	require.Equal(t, []string{"t.log symlink"}, f.refusals())
}

// The temporary .gz is created exclusively: a planted <log>.1.gz.tmp link
// is removed, never written through (copy-and-truncate and RotatingWriter
// share the compression).
func TestGzipFileRefusesSymlinkedTmp(t *testing.T) {
	f := newRotFix(t)
	live := f.live(t, "t.log", liveContent)
	victim := f.file(t, "passwd", victimData)
	require.NoError(t, os.Symlink(victim, live+".1.gz.tmp"))

	rotated, err := f.rotate(live, 10, 5)
	require.NoError(t, err)
	require.True(t, rotated)
	requireContent(t, victim, victimData)
	require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
	requireAbsent(t, live+".1.gz.tmp")
	require.Equal(t, []string{"t.log staging"}, f.refusals())

	p := filepath.Join(t.TempDir(), "hub.log")
	w, err := NewRotatingWriter(p, 1<<20, 3)
	require.NoError(t, err)
	defer func() { _ = w.Close() }()
	_, err = w.Write([]byte("line\n"))
	require.NoError(t, err)
	victim2 := f.file(t, "group", victimData)
	require.NoError(t, os.Symlink(victim2, p+".1.gz.tmp"))
	require.NoError(t, w.Rotate())
	requireContent(t, victim2, victimData)
	require.Equal(t, "line\n", gunzipString(t, p+".1.gz"))
	requireAbsent(t, p+".1.gz.tmp")
}

// A FIFO, a directory and a link named *.log are skipped without an error
// and reported; the regular logs beside them are still rotated.
func TestRotateDirSkipsNonRegular(t *testing.T) {
	f := newRotFix(t)
	require.NoError(t, unix.Mkfifo(filepath.Join(f.dir, "fifo.log"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(f.dir, "dir.log"), 0o700))
	big := bytes.Repeat([]byte("v"), 100)
	victim := f.file(t, "target", big)
	require.NoError(t, os.Symlink(victim, filepath.Join(f.dir, "link.log")))
	regular := f.live(t, "a.log", liveContent)

	rotated, err := f.rotateDir(10, 5)
	require.NoError(t, err)
	require.Equal(t, []string{regular}, rotated)
	requireContent(t, victim, big)
	for _, n := range []string{"fifo.log", "dir.log", "link.log"} {
		requireAbsent(t, filepath.Join(f.dir, n+".1.gz"))
	}
	require.Equal(t, []string{"dir.log not_regular", "fifo.log not_regular", "link.log symlink"}, f.refusals())
}

// A log with a second hard link (to a file outside the directory) is
// neither copied nor truncated.
func TestRotateCopyTruncateHardlinkRefused(t *testing.T) {
	f := newRotFix(t)
	big := bytes.Repeat([]byte("v"), 100)
	victim := f.file(t, "target", big)
	live := filepath.Join(f.dir, "t.log")
	require.NoError(t, os.Link(victim, live))

	rotated, err := f.rotate(live, 10, 5)
	require.NoError(t, err)
	require.False(t, rotated)
	requireContent(t, victim, big)
	requireAbsent(t, live+".1.gz")
	require.Equal(t, []string{"t.log hardlink"}, f.refusals())
}

// A regular <log>.rotating that is not a leftover of the rotator (a second
// hard link, another owner) is removed and its content never reaches the
// rotated files; a leftover of a crash (the rotator's own file, one link) is
// finished before the new rotation, in the log directory (older versions)
// and in the staging directory.
func TestRotateCopyTruncatePlantedRegularStaging(t *testing.T) {
	planted := []byte("planted by the backend\n")

	t.Run("second hard link", func(t *testing.T) {
		f := newRotFix(t)
		live := f.live(t, "t.log", liveContent)
		mine := f.file(t, "mine", planted)
		require.NoError(t, os.Link(mine, live+".rotating"))

		rotated, err := f.rotate(live, 10, 5)
		require.NoError(t, err)
		require.True(t, rotated)
		requireContent(t, mine, planted)
		requireAbsent(t, live+".rotating")
		require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
		requireAbsent(t, live+".2.gz")
		require.Equal(t, []string{"t.log staging"}, f.refusals())
	})

	t.Run("another owner", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("chown to another user needs root")
		}
		f := newRotFix(t)
		live := f.live(t, "t.log", liveContent)
		require.NoError(t, os.WriteFile(live+".rotating", planted, 0o600))
		require.NoError(t, os.Lchown(live+".rotating", 65534, 65534))

		rotated, err := f.rotate(live, 10, 5)
		require.NoError(t, err)
		require.True(t, rotated)
		requireAbsent(t, live+".rotating")
		require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
		requireAbsent(t, live+".2.gz")
		require.Equal(t, []string{"t.log staging"}, f.refusals())
	})

	t.Run("leftover of a crash in the log directory", func(t *testing.T) {
		f := newRotFix(t)
		live := f.live(t, "t.log", liveContent)
		leftover := []byte("left by a crash\n")
		require.NoError(t, os.WriteFile(live+".rotating", leftover, 0o600))

		rotated, err := f.rotate(live, 10, 5)
		require.NoError(t, err)
		require.True(t, rotated)
		require.Equal(t, string(leftover), gunzipString(t, live+".2.gz"))
		require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
		requireAbsent(t, live+".rotating")
		require.Empty(t, f.refusals())
	})

	t.Run("leftover of a crash in the staging directory", func(t *testing.T) {
		f := newRotFix(t)
		live := f.live(t, "t.log", liveContent)
		leftover := []byte("left by a crash\n")
		require.NoError(t, os.MkdirAll(f.staging, 0o700))
		staged := filepath.Join(f.staging, "tunnel-log-t.log.rotating")
		require.NoError(t, os.WriteFile(staged, leftover, 0o600))

		rotated, err := f.rotate(live, 10, 5)
		require.NoError(t, err)
		require.True(t, rotated)
		require.Equal(t, string(leftover), gunzipString(t, live+".2.gz"))
		require.Equal(t, string(liveContent), gunzipString(t, live+".1.gz"))
		requireAbsent(t, staged)
		require.Empty(t, f.refusals())
	})
}

// A directory with files in it, planted at a name the rotation writes or
// shifts, is moved aside with what it holds (a link inside is never
// followed; SweepRemoved deletes it later) and the rotation completes: it
// never stops with the live log truncated and its copy stuck in the
// staging directory.
func TestRotateCopyTruncatePlantedDirectory(t *testing.T) {
	older := []byte("older rotation\n")
	for _, tc := range []struct {
		name string
		// at is the planted name, relative to the log.
		at string
		// prior rotated files of the log before the rotation.
		prior map[string][]byte
		// want are the rotated files afterwards.
		want map[string]string
	}{
		{name: "legacy staging", at: ".rotating",
			want: map[string]string{".1.gz": string(liveContent)}},
		{name: "temporary gz", at: ".1.gz.tmp",
			want: map[string]string{".1.gz": string(liveContent)}},
		{name: "shift target", at: ".2.gz", prior: map[string][]byte{".1.gz": older},
			want: map[string]string{".1.gz": string(liveContent), ".2.gz": string(older)}},
		{name: "shift source", at: ".1.gz",
			want: map[string]string{".1.gz": string(liveContent)}},
		{name: "beyond keep", at: ".3.gz", prior: map[string][]byte{".1.gz": older},
			want: map[string]string{".1.gz": string(liveContent), ".2.gz": string(older)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRotFix(t)
			live := f.live(t, "t.log", liveContent)
			for suffix, data := range tc.prior {
				var gz bytes.Buffer
				zw := gzip.NewWriter(&gz)
				_, err := zw.Write(data)
				require.NoError(t, err)
				require.NoError(t, zw.Close())
				require.NoError(t, os.WriteFile(live+suffix, gz.Bytes(), 0o600))
			}
			victim := f.file(t, "cron", victimData)
			planted := live + tc.at
			require.NoError(t, os.MkdirAll(filepath.Join(planted, "sub"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(planted, "sub", "file"), victimData, 0o600))
			require.NoError(t, os.Symlink(victim, filepath.Join(planted, "link")))
			require.NoError(t, os.Symlink(f.outside, filepath.Join(planted, "dirlink")))

			rotated, err := f.rotate(live, 10, 3)
			require.NoError(t, err)
			require.True(t, rotated)
			requireContent(t, live, nil)
			for suffix, want := range tc.want {
				require.Equal(t, want, gunzipString(t, live+suffix), "content of t.log%s", suffix)
			}
			if _, ok := tc.want[tc.at]; !ok {
				requireAbsent(t, planted)
			}
			requireContent(t, victim, victimData)
			entries, err := os.ReadDir(f.staging)
			require.NoError(t, err)
			require.Empty(t, entries, "nothing is left in the staging directory")
			require.Equal(t, []string{"t.log staging"}, f.refusals())

			// The next rotation goes on as usual.
			require.NoError(t, os.WriteFile(live, liveContent, 0o600))
			rotated, err = f.rotate(live, 10, 3)
			require.NoError(t, err)
			require.True(t, rotated)
			require.Equal(t, string(liveContent), gunzipString(t, live+".2.gz"))
		})
	}
}

// syncBuffer is a bytes.Buffer safe for the loop goroutine and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	b.mu.Lock()
	data := append([]byte(nil), b.buf.Bytes()...)
	b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), "line %q", sc.Text())
		out = append(out, m)
	}
	return out
}

// Every tick sees a planted link again; the daemon log has one warning per
// file and reason, with the file and the reason.
func TestRotateDirLoopWarnsOncePerFile(t *testing.T) {
	f := newRotFix(t)
	victim := f.file(t, "target", victimData)
	a := filepath.Join(f.dir, "a.log")
	require.NoError(t, os.Symlink(victim, a))
	var buf syncBuffer
	l := slog.New(NewHandler(&buf, slog.LevelInfo, "node"))
	refused := func(file string) []map[string]any {
		var out []map[string]any
		for _, m := range buf.lines(t) {
			if m[KeyMsg] == "tunnel log rotation refused" && m["file"] == file {
				out = append(out, m)
			}
		}
		return out
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.loop(ctx, time.Millisecond, l)
	}()
	defer func() {
		cancel()
		<-done
	}()
	require.Eventually(t, func() bool { return len(refused(a)) > 0 }, 10*time.Second, 5*time.Millisecond)
	// b.log appears after the tick that warned about a.log listed the
	// directory, so the tick that warns about b.log saw a.log a second time.
	b := filepath.Join(f.dir, "b.log")
	require.NoError(t, os.Symlink(victim, b))
	require.Eventually(t, func() bool { return len(refused(b)) > 0 }, 10*time.Second, 5*time.Millisecond)
	cancel()
	<-done

	got := refused(a)
	require.Len(t, got, 1, "one warning for a.log over several ticks")
	require.Equal(t, "warn", got[0][KeyLevel])
	require.Equal(t, "symlink", got[0]["reason"])
	require.Len(t, refused(b), 1)
	requireContent(t, victim, victimData)
}
