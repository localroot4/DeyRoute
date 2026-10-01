package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ShortLinkTarget is the target of /usr/local/bin/dey: relative, so the link
// resolves to /usr/local/bin/deyroute (installer/install.sh uses the same).
const ShortLinkTarget = "deyroute"

// SelfUpdater replaces the deyroute binary under Root (spec section 5,
// `deyroute update` and `deyroute update --rollback`). Tunnel units are separate
// processes and are not touched; the caller restarts deyroute-hub/node.
type SelfUpdater struct {
	Root string
}

func (s SelfUpdater) layout() Layout { return Layout(s) }

// BinaryPath is Root/usr/local/bin/deyroute.
func (s SelfUpdater) BinaryPath() string { return s.layout().Path(config.BinaryPath) }

// PrevPath is Root/var/lib/deyroute/bin/deyroute.prev.
func (s SelfUpdater) PrevPath() string { return s.layout().Path(config.PrevBinaryPath) }

// LinkPath is Root/usr/local/bin/dey.
func (s SelfUpdater) LinkPath() string { return s.layout().Path(config.ShortLinkPath) }

// Install keeps the current binary as deyroute.prev, installs newBinaryPath as
// /usr/local/bin/deyroute (temp file 0755 + rename, so a running process keeps
// its old inode) and makes sure the dey symlink exists. The new binary must
// already be verified (DownloadRelease).
func (s SelfUpdater) Install(newBinaryPath string) error {
	fi, err := os.Stat(newBinaryPath)
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": newBinaryPath})
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 {
		return deyerr.New(deyerr.S001, deyerr.Params{"file": newBinaryPath}).
			WithWhy("the new binary is empty or not a regular file")
	}
	cur := s.BinaryPath()
	if _, err := os.Stat(cur); err == nil {
		if err := copyFileAtomic(cur, s.PrevPath(), 0o755); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": cur})
	}
	if err := copyFileAtomic(newBinaryPath, cur, 0o755); err != nil {
		return err
	}
	return s.EnsureLink()
}

// Rollback swaps /usr/local/bin/deyroute and deyroute.prev, so a second Rollback
// returns to the newer binary. No deyroute.prev → DEY-S007.
func (s SelfUpdater) Rollback() error {
	prev := s.PrevPath()
	fi, err := os.Stat(prev)
	if os.IsNotExist(err) {
		return deyerr.New(deyerr.S007, nil)
	}
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": prev})
	}
	if !fi.Mode().IsRegular() || fi.Size() == 0 {
		return deyerr.New(deyerr.S007, nil).WithWhy(prev + " is empty or not a regular file")
	}
	cur := s.BinaryPath()
	// Stage the current binary next to prev first; the two renames below are
	// each atomic, and at every point /usr/local/bin/deyroute is a whole binary.
	stagedPrev := ""
	if _, err := os.Stat(cur); err == nil {
		stagedPrev = prev + ".swap"
		if err := copyFileAtomic(cur, stagedPrev, 0o755); err != nil {
			return err
		}
	}
	if err := copyFileAtomic(prev, cur, 0o755); err != nil {
		if stagedPrev != "" {
			_ = os.Remove(stagedPrev)
		}
		return err
	}
	if stagedPrev != "" {
		if err := os.Rename(stagedPrev, prev); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": prev})
		}
	} else if err := os.Remove(prev); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": prev})
	}
	return s.EnsureLink()
}

// HasPrevious reports whether a rollback binary exists.
func (s SelfUpdater) HasPrevious() bool {
	fi, err := os.Stat(s.PrevPath())
	return err == nil && fi.Mode().IsRegular()
}

// EnsureLink makes Root/usr/local/bin/dey a symlink to deyroute, replacing
// anything else at that path atomically.
func (s SelfUpdater) EnsureLink() error {
	link := s.LinkPath()
	if t, err := os.Readlink(link); err == nil && (t == ShortLinkTarget || t == config.BinaryPath) {
		return nil
	}
	dir := filepath.Dir(link)
	if err := mkdirPublic(dir); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".dey.link-%d", os.Getpid()))
	_ = os.Remove(tmp)
	if err := os.Symlink(ShortLinkTarget, tmp); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": link})
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": link})
	}
	return nil
}

// SelfUpdate is SelfUpdater{Root: l.Root}.Install (architecture contract).
func (l Layout) SelfUpdate(newBinary string) error {
	return SelfUpdater(l).Install(newBinary)
}

// Rollback is SelfUpdater{Root: l.Root}.Rollback (architecture contract).
func (l Layout) Rollback() error { return SelfUpdater(l).Rollback() }
