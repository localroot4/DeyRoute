package log

import (
	stderrors "errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// TunnelLogFileMode is the mode of a tunnel log EnsureTunnelLog creates:
// the mode systemd gives a StandardOutput=append: file it creates itself
// (0666 masked by the default UMask 0022), so a log looks the same whoever
// created it. Its directory (01770 root:deyroute) keeps others out; the
// readers and the rotation run as root.
const TunnelLogFileMode os.FileMode = 0o644

// RefusedOwner means a tunnel log is a regular file with one link of
// another user than the daemon's (EnsureTunnelLog): the backends' user made
// it, so it could remove it and put a link there before systemd opens the
// name again.
const RefusedOwner = "owner"

// TunnelLogReplaced is the warning the hub and the node log, with the keys
// file and reason, when EnsureTunnelLog removed what stood at a tunnel
// log's name (once per file and reason while the daemon runs, WarnOnce).
const TunnelLogReplaced = "tunnel log replaced"

// EnsureTunnelLog prepares the tunnel log path before a tunnel unit starts.
// systemd opens the unit's StandardOutput=append: file as root before it
// drops privileges: it creates a missing file and follows a symbolic link
// (fs.protected_symlinks does not cover a directory that is not
// world-writable), and the backends' user can add names to the tunnel log
// directory. So only a regular file of this process's user (root in the
// daemons) with a single link may stand at path:
//   - such a file is kept as it is;
//   - anything else (a link, a FIFO, a socket, a directory with all it
//     holds, a file with a second hard link or of another user) is removed
//     like the rotator removes what stands at its names (removeUntrusted: a
//     link is removed, never followed), after refused is told the reason
//     (RefusedSymlink, RefusedNotRegular, RefusedHardlink, RefusedOwner);
//   - a missing log is created with O_CREAT|O_EXCL|O_NOFOLLOW and mode
//     TunnelLogFileMode, so it is never an existing file or a link target.
//
// In the sticky tunnel log directory (01770, systemd.TightenTunnelLogDir)
// the backends' user can then neither remove nor rename that file, so
// systemd appends to root's file. A missing directory is an error matching
// fs.ErrNotExist.
//
// Calls for the same path run one at a time in this process (the hub
// prepares a tunnel's log from several goroutines): a second call that saw
// the same planted link would otherwise remove the file the first had just
// created, and the name would be free again while a unit starts.
func EnsureTunnelLog(path string, refused func(reason string)) error {
	path = filepath.Clean(path)
	defer tunnelLogLocks.lock(path)()
	for retried := false; ; retried = true {
		fi, err := os.Lstat(path)
		switch {
		case err == nil:
			r := tunnelLogRefusal(fi)
			if r == "" {
				return nil
			}
			refused(r)
			if err := removeUntrusted(path); err != nil {
				return err
			}
		case !stderrors.Is(err, fs.ErrNotExist):
			return err
		}
		f, err := createFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, TunnelLogFileMode)
		if err == nil {
			// The umask may have narrowed the mode: set it exactly.
			cerr := f.Chmod(TunnelLogFileMode)
			if err := f.Close(); cerr == nil {
				cerr = err
			}
			return cerr
		}
		if retried || !stderrors.Is(err, fs.ErrExist) {
			return err
		}
		// Something appeared between the Lstat and the creation: look again.
	}
}

// tunnelLogLocks serialises EnsureTunnelLog per path.
var tunnelLogLocks = pathLocks{m: map[string]*pathLock{}}

// pathLocks is a mutex per path; an entry lives while a call holds or
// waits for it, so the map never grows with names seen once.
type pathLocks struct {
	mu sync.Mutex
	m  map[string]*pathLock
}

type pathLock struct {
	mu   sync.Mutex
	refs int
}

// lock locks path and returns the function that unlocks it.
func (l *pathLocks) lock(path string) (unlock func()) {
	l.mu.Lock()
	e := l.m[path]
	if e == nil {
		e = &pathLock{}
		l.m[path] = e
	}
	e.refs++
	l.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		l.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(l.m, path)
		}
		l.mu.Unlock()
	}
}

// tunnelLogRefusal returns why EnsureTunnelLog must not leave the log fi
// describes for systemd ("" = it may).
func tunnelLogRefusal(fi os.FileInfo) string {
	if r := refusedLog(fi); r != "" {
		return r
	}
	if uid, _, ok := fileOwner(fi); !ok || int64(uid) != int64(os.Geteuid()) {
		return RefusedOwner
	}
	return ""
}

// WarnOnce logs a warning with the keys file and reason at most once per
// file and reason. It remembers up to maxRefusalWarnings pairs: names
// planted by the thousand must not grow the daemon's memory, and beyond
// that nothing more is logged. RotateDirLoop uses one for "tunnel log
// rotation refused", the hub and the node one for TunnelLogReplaced. It is
// safe for concurrent use.
type WarnOnce struct {
	l    *slog.Logger
	msg  string
	mu   sync.Mutex
	seen map[warnKey]bool
}

type warnKey struct{ file, reason string }

// NewWarnOnce returns a WarnOnce logging msg to l.
func NewWarnOnce(l *slog.Logger, msg string) *WarnOnce {
	return &WarnOnce{l: l, msg: msg, seen: map[warnKey]bool{}}
}

// Warn logs the warning for file and reason unless it was logged before.
func (w *WarnOnce) Warn(file, reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	k := warnKey{file, reason}
	if w.seen[k] || len(w.seen) >= maxRefusalWarnings {
		return
	}
	w.seen[k] = true
	w.l.Warn(w.msg, slog.String("file", file), slog.String("reason", reason))
}

// ErrNotRegular is the error of OpenLogFile for a name that is not a
// regular file (a symbolic link, a FIFO, a socket, a directory …).
var ErrNotRegular = stderrors.New("not a regular file")

// ErrHardlink is the error of OpenLogFile for a regular file with more than
// one link. It matches ErrNotRegular too: the readers treat both alike.
var ErrHardlink = fmt.Errorf("%w with a single link", ErrNotRegular)

// OpenLogFile opens the log at path for reading and returns it with its
// Fstat. The daemons read the tunnel logs as root in a directory where the
// backends' user (deyroute) can add names, so the open never follows a link
// (O_NOFOLLOW) and never blocks on a FIFO (O_NONBLOCK), and only a regular
// file with a single link is accepted, as the rotation and doctor collect
// do: with fs.protected_hardlinks=0 that user can hard-link a file it cannot
// read (/etc/shadow) to a log name. Anything else is a *fs.PathError
// wrapping ErrNotRegular (ErrHardlink for a second link). A missing file
// matches fs.ErrNotExist. Reads of a regular file are not affected by
// O_NONBLOCK.
func OpenLogFile(path string) (*os.File, os.FileInfo, error) {
	path = filepath.Clean(path)
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		// ELOOP: the name is a link; ENXIO: a socket.
		if stderrors.Is(err, unix.ELOOP) || stderrors.Is(err, unix.ENXIO) {
			return nil, nil, &fs.PathError{Op: "open", Path: path, Err: ErrNotRegular}
		}
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, &fs.PathError{Op: "open", Path: path, Err: ErrNotRegular}
	}
	if _, nlink, ok := fileOwner(fi); !ok || nlink != 1 {
		_ = f.Close()
		return nil, nil, &fs.PathError{Op: "open", Path: path, Err: ErrHardlink}
	}
	return f, fi, nil
}
