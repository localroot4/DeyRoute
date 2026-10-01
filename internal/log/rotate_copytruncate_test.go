package log

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A tunnel log kept open with O_APPEND by another writer is copied to
// <path>.1.gz and truncated in place; the writer keeps appending to it.
func TestRotateCopyTruncate(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.log")
	w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	defer func() { _ = w.Close() }()
	big := bytes.Repeat([]byte("x"), int(DefaultMaxBytes)+10)
	_, err = w.Write(big)
	require.NoError(t, err)

	rotated, err := RotateCopyTruncate(p, 0, 0)
	require.NoError(t, err)
	require.True(t, rotated)
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Zero(t, fi.Size(), "the live file is truncated")
	_, err = w.Write([]byte("after\n"))
	require.NoError(t, err)
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "after\n", string(data), "the writer appends at offset 0 again")
	require.Equal(t, int64(len(big)), gunzipLen(t, p+".1.gz"))
	require.NoFileExists(t, p+".rotating")

	// Below the limit nothing happens; older files shift up to keep.
	rotated, err = RotateCopyTruncate(p, 0, 0)
	require.NoError(t, err)
	require.False(t, rotated)
	for i := 0; i < 3; i++ {
		require.NoError(t, os.WriteFile(p, []byte("0123456789"), 0o600))
		rotated, err = RotateCopyTruncate(p, 5, 2)
		require.NoError(t, err)
		require.True(t, rotated)
	}
	require.FileExists(t, p+".1.gz")
	require.FileExists(t, p+".2.gz")
	require.NoFileExists(t, p+".3.gz")
}

func TestRotateDirAndLoop(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.log"), bytes.Repeat([]byte("a"), 100), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.log"), []byte("b"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt"), bytes.Repeat([]byte("c"), 100), 0o600))
	rotated, err := RotateDir(dir, 10, 5)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "a.log")}, rotated)
	require.NoFileExists(t, filepath.Join(dir, "c.txt.1.gz"))
	none, err := RotateDir(filepath.Join(dir, "missing"), 10, 5)
	require.NoError(t, err)
	require.Empty(t, none)

	// The loop runs once at start and stops with its context.
	big := filepath.Join(dir, "main.log")
	require.NoError(t, os.WriteFile(big, bytes.Repeat([]byte("m"), int(DefaultMaxBytes)+1), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		RotateDirLoop(ctx, dir, time.Hour, Discard())
	}()
	require.Eventually(t, func() bool { _, err := os.Stat(big + ".1.gz"); return err == nil }, 10*time.Second, 10*time.Millisecond)
	cancel()
	<-done
}

func gunzipLen(t *testing.T, p string) int64 {
	t.Helper()
	f, err := os.Open(p)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	require.NoError(t, err)
	n, err := io.Copy(io.Discard, zr)
	require.NoError(t, err)
	return n
}
