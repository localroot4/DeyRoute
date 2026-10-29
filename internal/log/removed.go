package log

import (
	"crypto/rand"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// A directory found at a name the daemons write in the tunnel log directory
// (a log name, <log>.rotating, <log>.1.gz.tmp, <log>.N.gz) belongs to the
// backends' user, who can make it as deep and wide as it likes: a chain of a
// million directories takes it tens of seconds. Deleting it where it is
// found would hold up the start of the units, and os.RemoveAll keeps a
// descriptor and a stack frame per level (EMFILE at LimitNOFILE). So such a
// directory is renamed aside at once, which frees the name (moveAside), and
// the rotation loop deletes it later in bounded steps (SweepRemoved).

// removedInfix marks a name moveAside gave: <name>.removed-<16 hex digits>.
const removedInfix = ".removed-"

// SweepBudget is the number of removals (unlink, rmdir or rename) one
// SweepRemoved call of RotateDirLoop may spend per directory and tick:
// about a quarter of a second of system calls, some 2500 levels of a
// planted chain a minute.
const SweepBudget = 10000

// maxSweepScan bounds the names one SweepRemoved call reads in its
// directory, which the backends' user can fill with names.
const maxSweepScan = 1 << 16

// moveAside renames p, a directory the daemon did not create, to
// <p>.removed-<16 hex digits> in the same directory. A missing p (removed
// meanwhile) is not an error.
func moveAside(p string) error {
	for try := 0; ; try++ {
		err := os.Rename(p, p+removedInfix+randomHex())
		switch {
		case err == nil, stderrors.Is(err, fs.ErrNotExist):
			return nil
		case try < 2 && (stderrors.Is(err, unix.EEXIST) || stderrors.Is(err, unix.ENOTEMPTY)):
			// The random name was taken: draw another.
		default:
			return err
		}
	}
}

// randomHex returns 16 random lowercase hex digits.
func randomHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // never fails on Linux
	return hex.EncodeToString(b[:])
}

// isRemovedName reports whether name is one moveAside gives.
func isRemovedName(name string) bool {
	i := strings.LastIndex(name, removedInfix)
	if i <= 0 {
		return false
	}
	h := name[i+len(removedInfix):]
	if len(h) != 16 {
		return false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// SweepRemoved deletes, in bounded steps, what moveAside renamed in dir:
// every entry named <name>.removed-<16 hex digits>, with all it holds. One
// call spends at most budget removals (unlink, rmdir or rename) and reads
// at most maxSweepScan names of dir; what is left is deleted by a later
// call. A tree is never walked down: a directory is emptied one level at a
// time, its own subdirectories moved up into the directory being deleted,
// so any depth costs three descriptors, no recursion and no path. A link
// is removed, never followed. Other names in dir are never touched, and a
// missing dir is nothing to do. It returns the removals spent and the first
// error; an entry that fails does not stop the others.
func SweepRemoved(dir string, budget int) (int, error) {
	d, err := os.OpenFile(filepath.Clean(dir), os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer func() { _ = d.Close() }()
	dfd := int(d.Fd())
	s := sweep{left: budget}
	var first error
	for scanned := 0; scanned < maxSweepScan && s.left > 0; {
		names, rerr := d.Readdirnames(256)
		for _, n := range names {
			scanned++
			if !isRemovedName(n) || s.left <= 0 {
				continue
			}
			if err := s.purge(dfd, n); err != nil && first == nil {
				first = fmt.Errorf("remove %s: %w", filepath.Join(dir, n), err)
			}
		}
		if rerr != nil {
			if rerr != io.EOF && first == nil {
				first = rerr
			}
			break
		}
	}
	return budget - s.left, first
}

// sweep counts the removals SweepRemoved may still spend.
type sweep struct{ left int }

// spend takes one removal from the budget; false when none is left.
func (s *sweep) spend() bool {
	if s.left <= 0 {
		return false
	}
	s.left--
	return true
}

// purge deletes the entry name of the directory dfd as far as the budget
// goes: a non-directory is unlinked; a directory is emptied by passes of
// flatten and then removed.
func (s *sweep) purge(dfd int, name string) error {
	if !s.spend() {
		return nil
	}
	err := unix.Unlinkat(dfd, name, 0)
	if !stderrors.Is(err, unix.EISDIR) {
		return ignoreGone(err)
	}
	for s.left > 0 {
		seen, err := s.flatten(dfd, name)
		if err != nil {
			return err
		}
		if seen == 0 {
			break
		}
	}
	if !s.spend() {
		return nil
	}
	return ignoreGone(unix.Unlinkat(dfd, name, unix.AT_REMOVEDIR))
}

// flatten makes one pass over the directory name of dfd: a non-directory
// in it is unlinked, a subdirectory is emptied one level (lift) and
// removed. Subdirectories lift moved up are handled by the next pass. It
// returns how many entries it handled.
func (s *sweep) flatten(dfd int, name string) (int, error) {
	q, err := openDirAt(dfd, name)
	if err != nil {
		return 0, ignoreGone(err)
	}
	defer func() { _ = q.Close() }()
	qfd := int(q.Fd())
	seen := 0
	for {
		names, rerr := q.Readdirnames(256)
		for _, n := range names {
			if !s.spend() {
				return seen, nil
			}
			seen++
			err := unix.Unlinkat(qfd, n, 0)
			if !stderrors.Is(err, unix.EISDIR) {
				if err := ignoreGone(err); err != nil {
					return seen, err
				}
				continue
			}
			if err := s.lift(qfd, n); err != nil {
				return seen, err
			}
			if !s.spend() {
				return seen, nil
			}
			if err := ignoreGone(unix.Unlinkat(qfd, n, unix.AT_REMOVEDIR)); err != nil {
				return seen, err
			}
		}
		if rerr == io.EOF {
			return seen, nil
		}
		if rerr != nil {
			return seen, rerr
		}
	}
}

// lift empties the directory name of qfd by one level: its non-directories
// are unlinked and its subdirectories are moved up into qfd under new
// names (renameat between the two descriptors), where the next pass of
// flatten finds them.
func (s *sweep) lift(qfd int, name string) error {
	c, err := openDirAt(qfd, name)
	if err != nil {
		return ignoreGone(err)
	}
	defer func() { _ = c.Close() }()
	cfd := int(c.Fd())
	for {
		names, rerr := c.Readdirnames(256)
		for _, n := range names {
			if !s.spend() {
				return nil
			}
			err := unix.Unlinkat(cfd, n, 0)
			if stderrors.Is(err, unix.EISDIR) {
				if !s.spend() {
					return nil
				}
				err = unix.Renameat(cfd, n, qfd, randomHex())
			}
			if err := ignoreGone(err); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// openDirAt opens the directory name of dfd without following a link.
func openDirAt(dfd int, name string) (*os.File, error) {
	fd, err := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// ignoreGone drops the errors of an entry that changed since it was read
// (removed, replaced by a link or a file, or filled again): a later pass or
// sweep sees it as it is now.
func ignoreGone(err error) error {
	switch {
	case err == nil,
		stderrors.Is(err, unix.ENOENT),
		stderrors.Is(err, unix.ELOOP),
		stderrors.Is(err, unix.ENOTDIR),
		stderrors.Is(err, unix.ENOTEMPTY),
		stderrors.Is(err, unix.EEXIST):
		return nil
	}
	return err
}
