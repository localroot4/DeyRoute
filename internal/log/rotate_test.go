package log

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gunzipLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	data, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NoError(t, zr.Close())
	return nonEmptyLines(string(data))
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestRotationKeepsNewestGzipFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "var", "log", "deyroute")
	path := filepath.Join(dir, "deyroute.log")
	w, err := NewRotatingWriter(path, 1000, 3)
	require.NoError(t, err)
	require.Equal(t, path, w.Path())
	const n = 200
	for i := 0; i < n; i++ {
		line := fmt.Sprintf("line %04d %s\n", i, strings.Repeat("x", 80))
		m, err := w.Write([]byte(line))
		require.NoError(t, err)
		require.Equal(t, len(line), m)
	}
	require.NoError(t, w.Sync())
	require.NoError(t, w.LastError())
	require.NoError(t, w.Close())

	st, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, DirMode, st.Mode().Perm())
	for i := 1; i <= 3; i++ {
		st, err := os.Stat(w.RotatedPath(i))
		require.NoError(t, err, "rotated file %d", i)
		require.Equal(t, FileMode, st.Mode().Perm())
	}
	_, err = os.Stat(w.RotatedPath(4))
	require.True(t, os.IsNotExist(err), "only keep files are kept")
	_, err = os.Stat(path + ".rotating")
	require.True(t, os.IsNotExist(err))

	live, err := os.ReadFile(path)
	require.NoError(t, err)
	require.LessOrEqual(t, len(live), 1000)

	// The files hold the newest lines, contiguous and in order:
	// .3.gz (oldest) … .1.gz, then the live file.
	var seq []string
	for i := 3; i >= 1; i-- {
		lines := gunzipLines(t, w.RotatedPath(i))
		require.NotEmpty(t, lines)
		for _, l := range lines {
			require.LessOrEqual(t, len(l)+1, 1000)
		}
		seq = append(seq, lines...)
	}
	seq = append(seq, nonEmptyLines(string(live))...)
	require.Equal(t, fmt.Sprintf("line %04d", n-1), seq[len(seq)-1][:9])
	first, err := strconv.Atoi(seq[0][5:9])
	require.NoError(t, err)
	for i, l := range seq {
		require.Equal(t, fmt.Sprintf("line %04d", first+i), l[:9])
	}
}

func TestRotatingWriterDefaultsAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.log")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))
	w, err := NewRotatingWriter(path, 0, 0)
	require.NoError(t, err)
	require.Equal(t, DefaultMaxBytes, w.maxBytes)
	require.Equal(t, DefaultKeep, w.keep)
	require.Equal(t, int64(4), w.size, "appends to the existing file")
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm(), "existing file tightened to 0600")
	_, err = w.Write([]byte("new\n"))
	require.NoError(t, err)
	require.NoError(t, w.Rotate())
	require.Equal(t, []string{"old", "new"}, gunzipLines(t, w.RotatedPath(1)))
	require.NoError(t, w.Rotate(), "empty file: no-op")
	_, err = os.Stat(w.RotatedPath(2))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, w.Close())
	require.NoError(t, w.Close(), "idempotent")
	_, err = w.Write([]byte("x"))
	require.ErrorIs(t, err, os.ErrClosed)
	require.ErrorIs(t, w.Rotate(), os.ErrClosed)

	_, err = NewRotatingWriter("", 0, 0)
	require.Error(t, err)
}

func TestRotatingWriterFinishesInterruptedRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node.log")
	require.NoError(t, os.WriteFile(path+".rotating", []byte("from before the crash\n"), 0o600))
	require.NoError(t, os.WriteFile(path+".1.gz", gz(t, "older\n"), 0o600))
	// Files beyond keep (e.g. keep was lowered) are removed.
	require.NoError(t, os.WriteFile(path+".2.gz", gz(t, "x\n"), 0o600))
	require.NoError(t, os.WriteFile(path+".3.gz", gz(t, "y\n"), 0o600))
	w, err := NewRotatingWriter(path, 1<<20, 2)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close()) }()
	require.NoError(t, w.LastError())
	require.Equal(t, []string{"from before the crash"}, gunzipLines(t, w.RotatedPath(1)))
	require.Equal(t, []string{"older"}, gunzipLines(t, w.RotatedPath(2)))
	_, err = os.Stat(w.RotatedPath(3))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(path + ".rotating")
	require.True(t, os.IsNotExist(err))
}

func TestRotationFailureNeverLosesLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deyroute.log")
	w, err := NewRotatingWriter(path, 10, 2)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close()) }()
	clk := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return clk }
	_, err = w.Write([]byte("first line\n"))
	require.NoError(t, err)
	// Block compression: the temp output path is a non-empty directory.
	require.NoError(t, os.Mkdir(w.RotatedPath(1)+".tmp", 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(w.RotatedPath(1)+".tmp", "f"), nil, 0o600))
	_, err = w.Write([]byte("second line\n"))
	require.NoError(t, err, "the line is written even if rotation fails")
	require.Error(t, w.LastError())
	pending, err := os.ReadFile(path + ".rotating")
	require.NoError(t, err)
	require.Equal(t, "first line\n", string(pending), "uncompressed data kept")
	live, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "second line\n", string(live))

	// Next rotation (after the retry delay) first finishes the pending one,
	// then rotates again.
	require.NoError(t, os.RemoveAll(w.RotatedPath(1)+".tmp"))
	clk = clk.Add(rotateRetryAfter)
	_, err = w.Write([]byte("third line\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"second line"}, gunzipLines(t, w.RotatedPath(1)))
	require.Equal(t, []string{"first line"}, gunzipLines(t, w.RotatedPath(2)))
}

// The owner deletes (or an external logrotate moves) the live file: the
// writer must notice and continue in a new, visible file instead of an
// unlinked inode.
func TestRotatingWriterReopensDeletedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.log")
	w, err := NewRotatingWriter(path, 1<<20, 2)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close()) }()
	clk := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return clk }
	w.checkAt = clk

	_, err = w.Write([]byte("before\n"))
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	_, err = w.Write([]byte("right after\n")) // within the check interval
	require.NoError(t, err)
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err), "no stat on every line")

	clk = clk.Add(identityCheckEvery)
	_, err = w.Write([]byte("after\n"))
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "after\n", string(data))
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())

	// Replaced by another file (logrotate "create"): appended to the new one.
	require.NoError(t, os.Rename(path, path+".old"))
	require.NoError(t, os.WriteFile(path, []byte("new file\n"), 0o600))
	clk = clk.Add(identityCheckEvery)
	_, err = w.Write([]byte("into new\n"))
	require.NoError(t, err)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "new file\ninto new\n", string(data))
	require.NoError(t, w.LastError())
}

// A failing rotation must not be retried (re-reading and compressing the
// whole file) on every line; it is retried after rotateRetryAfter.
func TestRotationFailureBackoff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deyroute.log")
	w, err := NewRotatingWriter(path, 10, 2)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close()) }()
	clk := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return clk }

	_, err = w.Write([]byte("first line\n"))
	require.NoError(t, err)
	blocker := w.RotatedPath(1) + ".tmp"
	require.NoError(t, os.Mkdir(blocker, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(blocker, "f"), nil, 0o600))
	_, err = w.Write([]byte("second line\n")) // rotation fails: first line pending
	require.NoError(t, err)
	require.Error(t, w.LastError())
	// Unblock; within the retry delay nothing is rotated.
	require.NoError(t, os.RemoveAll(blocker))
	_, err = w.Write([]byte("third line\n"))
	require.NoError(t, err)
	_, err = os.Stat(w.RotatedPath(1))
	require.True(t, os.IsNotExist(err), "no retry before the delay")
	live, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "second line\nthird line\n", string(live))

	clk = clk.Add(rotateRetryAfter)
	_, err = w.Write([]byte("fourth line\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"second line", "third line"}, gunzipLines(t, w.RotatedPath(1)))
	require.Equal(t, []string{"first line"}, gunzipLines(t, w.RotatedPath(2)))
	live, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "fourth line\n", string(live))
}

// Rotate after the live file could not be reopened must not panic and
// must report the problem.
func TestRotateWithoutOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.log")
	w, err := NewRotatingWriter(path, 1<<20, 2)
	require.NoError(t, err)
	_, err = w.Write([]byte("a\n"))
	require.NoError(t, err)
	w.mu.Lock()
	require.NoError(t, w.f.Close())
	w.f = nil
	w.mu.Unlock()
	require.NoError(t, w.Rotate(), "rotates the file on disk and reopens")
	require.Equal(t, []string{"a"}, gunzipLines(t, w.RotatedPath(1)))
	require.NoError(t, w.Close())
}

func TestRotatingWriterConcurrentLinesStayWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.log")
	w, err := NewRotatingWriter(path, 4096, 50)
	require.NoError(t, err)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, err := fmt.Fprintf(w, "g%d-%03d|%s|end\n", g, i, strings.Repeat("z", 40))
				assert.NoError(t, err)
			}
		}(g)
	}
	wg.Wait()
	require.NoError(t, w.Close())
	files, err := filepath.Glob(path + "*")
	require.NoError(t, err)
	total := 0
	for _, f := range files {
		var lines []string
		if strings.HasSuffix(f, ".gz") {
			lines = gunzipLines(t, f)
		} else {
			data, err := os.ReadFile(f)
			require.NoError(t, err)
			lines = nonEmptyLines(string(data))
		}
		for _, l := range lines {
			require.True(t, strings.HasSuffix(l, "|end"), l)
		}
		total += len(lines)
	}
	require.Equal(t, 800, total)
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return b.Bytes()
}

func TestGzipHeaderName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.log")
	w, err := NewRotatingWriter(path, 1<<20, 5)
	require.NoError(t, err)
	_, err = w.Write([]byte("e\n"))
	require.NoError(t, err)
	require.NoError(t, w.Rotate())
	require.NoError(t, w.Close())
	f, err := os.Open(w.RotatedPath(1))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(bufio.NewReader(f))
	require.NoError(t, err)
	require.Equal(t, "events.log", zr.Name)
	require.NoError(t, zr.Close())
}
