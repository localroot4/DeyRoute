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
	"syscall"
	"time"

	"golang.org/x/sys/unix"

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
func (w *RotatingWriter) RotatedPath(n int) string { return rotatedPath(w.path, n) }

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
	in, err := os.OpenFile(filepath.Clean(src), os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("compress %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	if err := compressAndShift(w.path, w.keep, in, func(string) {}); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return err
	}
	syncDir(filepath.Dir(w.path))
	return nil
}

// rotatedPath is the n-th rotated file of the log path (<path>.<n>.gz).
func rotatedPath(path string, n int) string { return path + "." + strconv.Itoa(n) + ".gz" }

// compressAndShift compresses in into <path>.1.gz, shifting <path>.N.gz up
// and deleting those beyond keep. The rotated names live in the log
// directory, where a tunnel backend may have planted anything: the
// compressed file is first written to <path>.1.gz.tmp, created exclusively
// (createExclusive), and a name that is not a regular file is removed (a
// directory with what it holds, removeUntrusted), never renamed along or
// renamed onto (renameRotated). refuse is told about each such name
// (RefusedStaging).
func compressAndShift(path string, keep int, in io.Reader, refuse func(reason string)) error {
	tmp := rotatedPath(path, 1) + ".tmp"
	if err := gzipFile(in, tmp, filepath.Base(path), refuse); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("compress %s: %w", path, err)
	}
	// Drop everything at or beyond keep, then shift keep-1 … 1 up by one.
	for n := keep; ; n++ {
		p := rotatedPath(path, n)
		fi, err := os.Lstat(p)
		if stderrors.Is(err, fs.ErrNotExist) {
			if n > keep {
				break
			}
			continue
		}
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			refuse(RefusedStaging)
		}
		if err := removeUntrusted(p); err != nil {
			return err
		}
	}
	for n := keep - 1; n >= 1; n-- {
		if err := renameRotated(rotatedPath(path, n), rotatedPath(path, n+1), refuse); err != nil {
			return err
		}
	}
	return renameRotated(tmp, rotatedPath(path, 1), refuse)
}

// renameRotated renames the rotated file from to to. Both names are checked
// with Lstat first: a from that is not a regular file (a link, a FIFO or a
// directory planted there) is removed instead of moved along, and a to
// that is not a regular file is removed before the rename, so a rename
// never lands on a link the rotator did not create. A missing from is
// nothing to do.
func renameRotated(from, to string, refuse func(reason string)) error {
	fi, err := os.Lstat(from)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		refuse(RefusedStaging)
		return removeUntrusted(from)
	}
	if ti, err := os.Lstat(to); err == nil && !ti.Mode().IsRegular() {
		refuse(RefusedStaging)
		if err := removeUntrusted(to); err != nil {
			return err
		}
	}
	return os.Rename(from, to)
}

// removeName removes p (a missing p is not an error).
func removeName(p string) error {
	if err := os.Remove(p); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// removeUntrusted removes what stands at p, a name the rotator writes: a
// directory goes with everything in it (os.RemoveAll works below p with
// descriptors and O_NOFOLLOW, so a link inside is removed, never followed),
// anything else is unlinked. A directory planted there with files in it must
// not stop every later rotation. A missing p is not an error.
func removeUntrusted(p string) error {
	fi, err := os.Lstat(p)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(p)
	}
	return removeName(p)
}

// gzipFile compresses in into dst, created by createExclusive (mode 0600,
// fsynced).
func gzipFile(in io.Reader, dst, name string, refuse func(reason string)) error {
	out, err := createExclusive(dst, refuse)
	if err != nil {
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

// createExclusive creates p for writing with O_CREAT|O_EXCL|O_NOFOLLOW and
// mode FileMode: it never opens an existing file or follows a link. When
// something is already there it is removed and the creation is retried once;
// unless it is a leftover of the rotator itself (ownLeftover), refuse is
// told first (RefusedStaging).
func createExclusive(p string, refuse func(reason string)) (*os.File, error) {
	for retried := false; ; retried = true {
		f, err := createFile(filepath.Clean(p), os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, FileMode)
		if err == nil || retried || !stderrors.Is(err, fs.ErrExist) {
			return f, err
		}
		if fi, lerr := os.Lstat(p); lerr == nil && !ownLeftover(fi) {
			refuse(RefusedStaging)
		}
		if err := removeUntrusted(p); err != nil {
			return nil, err
		}
	}
}

// createFile opens the files createExclusive creates (os.OpenFile; tests
// make it fail as a full disk would).
var createFile = os.OpenFile

// ownLeftover reports whether fi is a file the rotator may have left behind
// itself: a regular file owned by this process's user (root in the
// daemons) with a single link. Anything else at a name the rotator uses was
// put there by someone else.
func ownLeftover(fi os.FileInfo) bool {
	uid, nlink, ok := fileOwner(fi)
	return ok && fi.Mode().IsRegular() && nlink == 1 && int64(uid) == int64(os.Geteuid())
}

// fileOwner returns the owner and the link count of fi.
func fileOwner(fi os.FileInfo) (uid uint32, nlink uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, uint64(st.Nlink), true
}

func syncDir(dir string) {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// Reasons of a refused tunnel log ("tunnel log rotation refused", see
// RotateDirLoop and CopyTruncateOptions.Refused).
const (
	// RefusedSymlink means the *.log name is a symbolic link; it is skipped.
	RefusedSymlink = "symlink"
	// RefusedNotRegular means the *.log name is not a regular file (a FIFO,
	// a directory, a socket …); it is skipped.
	RefusedNotRegular = "not_regular"
	// RefusedHardlink means the log has more than one hard link; it is
	// skipped.
	RefusedHardlink = "hardlink"
	// RefusedStaging means something the rotator did not create stood at a
	// name it writes (<log>.rotating, <log>.1.gz.tmp, <log>.N.gz) and was
	// removed (the rotation went on), or the staging directory is not
	// private to the daemon's user (the log is skipped).
	RefusedStaging = "staging"
)

// stagingPrefix names the copies RotateCopyTruncate stages in its staging
// directory: <staging>/tunnel-log-<log base name>.rotating.
const stagingPrefix = "tunnel-log-"

// CopyTruncateOptions configures RotateCopyTruncate and RotateDir.
type CopyTruncateOptions struct {
	// MaxBytes is the size above which a log is rotated; <= 0 means
	// DefaultMaxBytes.
	MaxBytes int64
	// Keep is the number of rotated files kept; <= 0 means DefaultKeep.
	Keep int
	// Staging is the directory the copy is staged in before it is
	// compressed (the daemons use /var/lib/deyroute/tmp). It must be
	// writable by the daemon's user only; it is created with mode 0700 when
	// missing. Required.
	Staging string
	// Refused, when set, is told about every log the rotator refused to
	// touch and every untrusted name it removed (file is the log, reason
	// one of the Refused* constants).
	Refused func(file, reason string)
}

func (o CopyTruncateOptions) withDefaults() CopyTruncateOptions {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.Keep <= 0 {
		o.Keep = DefaultKeep
	}
	if o.Refused == nil {
		o.Refused = func(string, string) {}
	}
	return o
}

// RotateCopyTruncate rotates a log that another process keeps open with
// O_APPEND — the tunnel backends write through systemd
// StandardOutput=append: — so it cannot be renamed away: when path is
// larger than MaxBytes its content is copied aside, the live file is
// truncated to zero (the next append lands at offset 0) and the copy becomes
// <path>.1.gz, older files shifted and only Keep kept, as in RotatingWriter.
// Lines written between the copy and the truncate are lost (logrotate's
// copytruncate has the same window). It reports whether the file was
// rotated.
//
// The daemons run it as root over /var/log/deyroute/tunnels, where the
// backends' user deyroute can create files, so it never follows a link and
// never writes through a name that user can create first:
//   - the log is opened once with O_NOFOLLOW and must be a regular file
//     with a single link (else it is skipped and reported as RefusedSymlink,
//     RefusedNotRegular or RefusedHardlink); its size, the copy and the
//     truncate all use that descriptor (ftruncate), never the path. It is
//     opened read-write because ftruncate needs it; nothing is written
//     through it.
//   - the copy is staged outside the log directory, in
//     CopyTruncateOptions.Staging (root-only /var/lib/deyroute/tmp, created
//     0700 on demand and checked to be private), as
//     <staging>/tunnel-log-<name>.rotating, created with O_EXCL|O_NOFOLLOW.
//     This is the staging design chosen over an exclusive <path>.rotating
//     next to the log: the backends' user can neither create nor replace
//     the staged copy, so a leftover there of an interrupted rotation is
//     the rotator's own and is finished first.
//   - the compressed file is created in the log directory with
//     O_EXCL|O_NOFOLLOW as <path>.1.gz.tmp and renamed (within the
//     directory) to <path>.1.gz; see compressAndShift.
//   - <path>.rotating in the log directory, where versions before this one
//     staged the copy, is finished when it is a leftover of the rotator
//     (root's regular file with one link) and removed otherwise.
func RotateCopyTruncate(path string, o CopyTruncateOptions) (bool, error) {
	o = o.withDefaults()
	path = filepath.Clean(path)
	refuse := func(reason string) { o.Refused(path, reason) }
	src, err := openTunnelLog(path, o.MaxBytes, refuse)
	if src == nil {
		return false, err
	}
	defer func() { _ = src.Close() }()
	private, err := stagingDir(o.Staging)
	if err != nil {
		return false, fmt.Errorf("staging directory %s: %w", o.Staging, err)
	}
	if !private {
		refuse(RefusedStaging)
		return false, nil
	}
	// Leftovers hold older lines than the live file: they go first.
	if err := finishLeftover(path+".rotating", path, o.Keep, refuse); err != nil {
		return false, err
	}
	staged := filepath.Join(o.Staging, stagingPrefix+filepath.Base(path)+".rotating")
	if err := finishLeftover(staged, path, o.Keep, refuse); err != nil {
		return false, err
	}
	out, err := createExclusive(staged, refuse)
	if err != nil {
		return false, fmt.Errorf("copy %s: %w", path, err)
	}
	_, cerr := io.Copy(out, src)
	if cerr == nil {
		cerr = out.Sync()
	}
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(staged)
		return false, fmt.Errorf("copy %s: %w", path, cerr)
	}
	if err := src.Truncate(0); err != nil {
		_ = os.Remove(staged)
		return false, fmt.Errorf("truncate %s: %w", path, err)
	}
	return true, finishStaged(staged, path, o.Keep, refuse)
}

// openTunnelLog opens the log path for RotateCopyTruncate when it is larger
// than maxBytes: O_RDWR (ftruncate needs a descriptor open for writing),
// O_NOFOLLOW, O_NONBLOCK (a FIFO put there after the Lstat does not block
// the open) and O_CLOEXEC; Lstat before and Fstat after the open must both
// show a regular file with a single link. A refused name is reported and,
// like a missing or small file, returns no file and no error.
func openTunnelLog(path string, maxBytes int64, refuse func(reason string)) (*os.File, error) {
	fi, err := os.Lstat(path)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r := refusedLog(fi); r != "" {
		refuse(r)
		return nil, nil
	}
	if fi.Size() <= maxBytes {
		return nil, nil
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	switch {
	case err == nil:
	case stderrors.Is(err, unix.ELOOP):
		refuse(RefusedSymlink)
		return nil, nil
	case stderrors.Is(err, unix.EISDIR), stderrors.Is(err, unix.ENXIO):
		refuse(RefusedNotRegular)
		return nil, nil
	case stderrors.Is(err, fs.ErrNotExist):
		return nil, nil
	default:
		return nil, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if r := refusedLog(st); r != "" {
		_ = f.Close()
		refuse(r)
		return nil, nil
	}
	if st.Size() <= maxBytes {
		_ = f.Close()
		return nil, nil
	}
	return f, nil
}

// refusedLog returns why the rotator must not touch the log fi describes
// ("" = it may).
func refusedLog(fi os.FileInfo) string {
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return RefusedSymlink
	case !fi.Mode().IsRegular():
		return RefusedNotRegular
	}
	if _, nlink, ok := fileOwner(fi); !ok || nlink != 1 {
		return RefusedHardlink
	}
	return ""
}

// stagingDir creates dir (0700) when missing and reports whether only this
// process's user can write to it: a directory, not a link, owned by that
// user, without group or other write permission.
func stagingDir(dir string) (bool, error) {
	if dir == "" {
		return false, stderrors.New("no staging directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return false, err
	}
	uid, _, ok := fileOwner(fi)
	return ok && fi.IsDir() && fi.Mode().Perm()&0o022 == 0 && int64(uid) == int64(os.Geteuid()), nil
}

// finishLeftover finishes the staged copy p of the log path that an
// interrupted rotation left behind (compressed into <path>.1.gz, older files
// shifted). Something at p that the rotator did not leave (ownLeftover) is
// removed and reported instead. A missing p is nothing to do.
func finishLeftover(p, path string, keep int, refuse func(reason string)) error {
	fi, err := os.Lstat(p)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownLeftover(fi) {
		refuse(RefusedStaging)
		return removeUntrusted(p)
	}
	return finishStaged(p, path, keep, refuse)
}

// finishStaged compresses the staged copy p into <path>.1.gz, shifting the
// older files, and removes p. p is opened with O_NOFOLLOW and must still be
// the rotator's own file; otherwise it is removed and reported.
func finishStaged(p, path string, keep int, refuse func(reason string)) error {
	in, err := os.OpenFile(filepath.Clean(p), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if stderrors.Is(err, unix.ELOOP) {
			refuse(RefusedStaging)
			return removeName(p)
		}
		return err
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if !ownLeftover(st) {
		refuse(RefusedStaging)
		return removeUntrusted(p)
	}
	if err := compressAndShift(path, keep, in, refuse); err != nil {
		return err
	}
	if err := removeName(p); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

// RotateDir runs RotateCopyTruncate on every *.log file of dir (missing
// dir: nothing to do) and returns the rotated paths and the first error. A
// *.log that is not a regular file with a single link is skipped and
// reported to o.Refused.
func RotateDir(dir string, o CopyTruncateOptions) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		return nil, err
	}
	var rotated []string
	var first error
	for _, p := range paths {
		ok, err := RotateCopyTruncate(p, o)
		if ok {
			rotated = append(rotated, p)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return rotated, first
}

// RotateDirInterval is how often the daemons check the tunnel logs.
const RotateDirInterval = time.Minute

// maxRefusalWarnings bounds the (file, reason) pairs RotateDirLoop
// remembers: names planted by the thousand must not grow the daemon's
// memory. Refusals beyond it are not logged.
const maxRefusalWarnings = 1024

// RotateDirLoop runs RotateDir on dir now and then every interval until ctx
// ends, staging the copies in staging and logging each rotation and failure
// to l. The hub and the node agent run it for /var/log/deyroute/tunnels
// (section 12: 20 MB, 5 files kept) with /var/lib/deyroute/tmp. A refused
// log is logged as the warning "tunnel log rotation refused" with its file
// and reason, once per file and reason while the daemon runs.
func RotateDirLoop(ctx context.Context, dir, staging string, interval time.Duration, l *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	type refusal struct{ file, reason string }
	warned := map[refusal]bool{}
	o := CopyTruncateOptions{Staging: staging, Refused: func(file, reason string) {
		k := refusal{file, reason}
		if warned[k] || len(warned) >= maxRefusalWarnings {
			return
		}
		warned[k] = true
		l.Warn("tunnel log rotation refused", slog.String("file", file), slog.String("reason", reason))
	}}
	for {
		rotated, err := RotateDir(dir, o)
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
