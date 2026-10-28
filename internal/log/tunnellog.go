package log

import (
	stderrors "errors"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrNotRegular is the error of OpenLogFile for a name that is not a
// regular file (a symbolic link, a FIFO, a socket, a directory …).
var ErrNotRegular = stderrors.New("not a regular file")

// OpenLogFile opens the log at path for reading and returns it with its
// Fstat. The daemons read the tunnel logs as root in a directory where the
// backends' user (deyroute) can add names, so the open never follows a link
// (O_NOFOLLOW) and never blocks on a FIFO (O_NONBLOCK), and only a regular
// file is accepted: anything else is a *fs.PathError wrapping ErrNotRegular.
// A missing file matches fs.ErrNotExist. Reads of a regular file are not
// affected by O_NONBLOCK.
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
	return f, fi, nil
}
