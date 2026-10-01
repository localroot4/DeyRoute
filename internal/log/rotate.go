package log

import (
	"compress/gzip"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// File modes of the log directory, the live file and rotated files.
const (
	DirMode  os.FileMode = 0o750
	FileMode os.FileMode = 0o600
)

// Timing of the self-healing checks in RotatingWriter.Write.
const (
	// identityCheckEvery is how often Write verifies that the path still
	// names the open file (the owner deleted it, or an external logrotate
	// moved it away); the file is then reopened so logging continues in
	// a visible file instead of an unlinked inode.
	identityCheckEvery = time.Second
	// rotateRetryAfter is how long an automatic rotation is not retried
	// after it failed (disk full, permission): each attempt reads and
	// compresses up to MaxBytes, which must not happen on every line.
	rotateRetryAfter = time.Minute
)

// RotatingWriter is an append-only log file that rotates itself: when a
// write would grow the file beyond MaxBytes, the file is renamed aside, a
// new empty file is opened, and the old content is gzip-compressed into
// <path>.1.gz after shifting <path>.N.gz to <path>.N+1.gz (only Keep files
// are kept: deyroute.log.1.gz … deyroute.log.5.gz). It is safe for concurrent
// use; each Write is written whole to one file.
//
// A crash in the middle of a rotation leaves <path>.rotating behind; the
// next NewRotatingWriter finishes compressing it. When the live file is
// deleted or replaced by someone else it is reopened within a second.
type RotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	ident    os.FileInfo // identity of f (os.SameFile against the path)
	size     int64
	lastErr  error
	closed   bool
	clock    func() time.Time
	checkAt  time.Time // next identity check
	retryAt  time.Time // no automatic rotation before this instant
}

// NewRotatingWriter opens (creating as needed) path for appending. The
// directory is created with mode 0750 and the file with 0600 (an existing
// file is tightened to 0600). maxBytes <= 0 means 20 MB, keep <= 0 means 5.
// Errors are DEY-X022.
func NewRotatingWriter(path string, maxBytes int64, keep int) (*RotatingWriter, error) {
	if path == "" {
		return nil, deyerr.Wrap(deyerr.X022, stderrors.New("empty log path"), deyerr.Params{"path": path})
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), DirMode); err != nil {
		return nil, deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": path})
	}
	w := &RotatingWriter{path: path, maxBytes: maxBytes, keep: keep, clock: now}
	if _, err := os.Stat(w.pendingPath()); err == nil {
		// Finish a rotation interrupted by a crash.
		if err := w.compressAndShift(); err != nil {
			w.lastErr = err
		}
	}
	if err := w.open(); err != nil {
		return nil, deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": path})
	}
	return w, nil
}

// Path returns the live log file path.
func (w *RotatingWriter) Path() string { return w.path }

// RotatedPath returns the path of the n-th rotated file (<path>.<n>.gz).
func (w *RotatingWriter) RotatedPath(n int) string { return w.path + "." + strconv.Itoa(n) + ".gz" }

func (w *RotatingWriter) pendingPath() string { return w.path + ".rotating" }

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, FileMode)
	if err != nil {
		return err
	}
	if err := f.Chmod(FileMode); err != nil {
		_ = f.Close()
		return err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.ident = st
	w.size = st.Size()
	w.checkAt = w.clock().Add(identityCheckEvery)
	return nil
}

// checkIdentity reopens the path when it no longer names the open file
// (deleted or replaced from outside). At most once per identityCheckEvery.
// Called with w.mu held.
func (w *RotatingWriter) checkIdentity() {
	if w.f == nil {
		return
	}
	t := w.clock()
	if t.Before(w.checkAt) {
		return
	}
	w.checkAt = t.Add(identityCheckEvery)
	st, err := os.Stat(w.path)
	if err == nil && os.SameFile(st, w.ident) {
		return
	}
	if err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return // transient stat problem: keep the current file
	}
	_ = w.f.Close()
	w.f = nil
	if err := w.open(); err != nil {
		w.lastErr = err
	}
}

// Write appends p, rotating first when the file would exceed MaxBytes. A
// rotation failure never loses the line: it is still written (to the old
// or the new file), the failure is reported by LastError and the automatic
// rotation is retried after rotateRetryAfter.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	w.checkIdentity()
	if w.f != nil && w.size > 0 && w.size+int64(len(p)) > w.maxBytes && !w.clock().Before(w.retryAt) {
		if err := w.rotate(); err != nil {
			w.lastErr = err
			w.retryAt = w.clock().Add(rotateRetryAfter)
		} else {
			w.retryAt = time.Time{}
		}
	}
	if w.f == nil {
		if err := w.open(); err != nil {
			w.lastErr = err
			return 0, deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": w.path})
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	if err != nil {
		return n, deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": w.path})
	}
	return n, nil
}

// Rotate forces a rotation now (no-op on an empty file), regardless of a
// pending retry delay.
func (w *RotatingWriter) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	if w.size == 0 {
		return nil
	}
	if err := w.rotate(); err != nil {
		w.lastErr = err
		return err
	}
	w.retryAt = time.Time{}
	return nil
}

// LastError returns the most recent rotation or reopen failure (nil when
// everything worked).
func (w *RotatingWriter) LastError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastErr
}

// Sync flushes the live file to disk.
func (w *RotatingWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	return w.f.Sync()
}

// Close closes the live file. Further writes return os.ErrClosed.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// rotate moves the live file aside, reopens a fresh one and compresses the
// old content. Called with w.mu held.
func (w *RotatingWriter) rotate() error {
	if _, err := os.Stat(w.pendingPath()); err == nil {
		// A previous compression failed; never overwrite its input.
		if err := w.compressAndShift(); err != nil {
			return err
		}
	}
	if w.f != nil {
		err := w.f.Close()
		w.f = nil
		if err != nil {
			return err // the next Write reopens and appends to the same file
		}
	}
	if err := os.Rename(w.path, w.pendingPath()); err != nil {
		// Keep appending to the current file; retried on the next write.
		if oerr := w.open(); oerr != nil {
			return stderrors.Join(err, oerr)
		}
		return err
	}
	if err := w.open(); err != nil {
		return err
	}
	return w.compressAndShift()
}

// compressAndShift turns <path>.rotating into <path>.1.gz, shifting older
// files up and deleting those beyond keep.
func (w *RotatingWriter) compressAndShift() error {
	src := w.pendingPath()
	tmp := w.RotatedPath(1) + ".tmp"
	if err := gzipFile(src, tmp, filepath.Base(w.path)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("compress %s: %w", src, err)
	}
	// Drop everything at or beyond keep, then shift keep-1 … 1 up by one.
	for n := w.keep; ; n++ {
		err := os.Remove(w.RotatedPath(n))
		if err != nil && stderrors.Is(err, fs.ErrNotExist) && n > w.keep {
			break
		}
		if err != nil && !stderrors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for n := w.keep - 1; n >= 1; n-- {
		if err := os.Rename(w.RotatedPath(n), w.RotatedPath(n+1)); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(tmp, w.RotatedPath(1)); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return err
	}
	syncDir(filepath.Dir(w.path))
	return nil
}

// gzipFile compresses src into dst (mode 0600, fsynced).
func gzipFile(src, dst, name string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(filepath.Clean(dst), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, FileMode)
	if err != nil {
		return err
	}
	// A leftover temp file keeps its old mode on O_TRUNC; rotated logs are 0600.
	if err := out.Chmod(FileMode); err != nil {
		_ = out.Close()
		return err
	}
	zw, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		_ = out.Close()
		return err
	}
	zw.Name = name
	zw.ModTime = now().UTC()
	_, cerr := io.Copy(zw, in)
	if err := zw.Close(); cerr == nil {
		cerr = err
	}
	if cerr == nil {
		cerr = out.Sync()
	}
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	return cerr
}

func syncDir(dir string) {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// RotateCopyTruncate rotates a log that another process keeps open with
// O_APPEND — the tunnel backends write through systemd
// StandardOutput=append: — so it cannot be renamed away: when path is
// larger than maxBytes its content is copied to <path>.rotating, the live
// file is truncated to zero (the next append lands at offset 0) and the
// copy becomes <path>.1.gz, older files shifted and only keep kept, as in
// RotatingWriter. Lines written between the copy and the truncate are lost
// (logrotate's copytruncate has the same window). It reports whether the
// file was rotated.
func RotateCopyTruncate(path string, maxBytes int64, keep int) (bool, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if keep <= 0 {
		keep = DefaultKeep
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() <= maxBytes {
		return false, nil
	}
	w := &RotatingWriter{path: path, keep: keep}
	if err := copyFile(path, w.pendingPath()); err != nil {
		_ = os.Remove(w.pendingPath())
		return false, fmt.Errorf("copy %s: %w", path, err)
	}
	if err := os.Truncate(path, 0); err != nil {
		_ = os.Remove(w.pendingPath())
		return false, fmt.Errorf("truncate %s: %w", path, err)
	}
	return true, w.compressAndShift()
}

// RotateDir runs RotateCopyTruncate on every *.log file of dir (missing
// dir: nothing to do) and returns the rotated paths and the first error.
func RotateDir(dir string, maxBytes int64, keep int) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		return nil, err
	}
	var rotated []string
	var first error
	for _, p := range paths {
		ok, err := RotateCopyTruncate(p, maxBytes, keep)
		if ok {
			rotated = append(rotated, p)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return rotated, first
}

// copyFile copies src to dst (mode FileMode).
func copyFile(src, dst string) error {
	in, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(filepath.Clean(dst), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, FileMode)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	return cerr
}

// RotateDirInterval is how often the daemons check the tunnel logs.
const RotateDirInterval = time.Minute

// RotateDirLoop runs RotateDir on dir now and then every interval until ctx
// ends, logging each rotation and failure to l. The hub and the node agent
// run it for /var/log/deyroute/tunnels (section 12: 20 MB, 5 files kept).
func RotateDirLoop(ctx context.Context, dir string, interval time.Duration, l *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		rotated, err := RotateDir(dir, DefaultMaxBytes, DefaultKeep)
		for _, p := range rotated {
			l.Info("tunnel log rotated", slog.String("file", p))
		}
		if err != nil {
			l.Warn("tunnel log rotation failed", Err(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
