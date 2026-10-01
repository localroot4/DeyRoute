package install

import (
	"io"
	"os"
	"path/filepath"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// mkdirPublic creates dir (and parents) with mode 0755: binary directories
// (/usr/local/bin, /var/lib/deyroute/bin/<b>/<ver>) must be traversable by the
// unprivileged deyroute user that runs backends.
func mkdirPublic(dir string) error {
	return os.MkdirAll(dir, 0o755) // #nosec G301 -- world-traversable binary directories by design (section 7)
}

// writeFileAtomic writes data to path via a temp file in the same directory
// (fsync + rename + directory fsync). Failures are DEY-X032.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := finishTemp(tmp, path, perm); err != nil {
		return err
	}
	ok = true
	return nil
}

// finishTemp chmods, fsyncs, closes and renames tmp onto path.
func finishTemp(tmp *os.File, path string, perm os.FileMode) error {
	if err := tmp.Chmod(perm); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := tmp.Sync(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := tmp.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	syncDir(filepath.Dir(path))
	return nil
}

// copyFileAtomic copies src to dst (mode perm) through a temp file next to
// dst, so dst is replaced in one rename even across filesystems.
func copyFileAtomic(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- caller-chosen local binary
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": src})
	}
	defer func() { _ = in.Close() }()
	dir := filepath.Dir(dst)
	if err := mkdirPublic(dir); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".new-*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := finishTemp(tmp, dst, perm); err != nil {
		return err
	}
	ok = true
	return nil
}
