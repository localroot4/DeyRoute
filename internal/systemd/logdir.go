package systemd

import (
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
)

// TunnelLogDirMode is the mode of TunnelLogDir, owner root, group deyroute
// (installer/install.sh, setup, InstallTemplates and the hub and node
// daemons create it so; QUESTIONS.md C.48). systemd opens a tunnel unit's
// StandardOutput=append: file as root before it drops privileges, so the
// backends (user deyroute) only write through the descriptor they are
// given. The group may still add files, but the sticky bit keeps it from
// removing or renaming root's: the live logs systemd creates and the rotated
// ones. Without the deyroute group the group bits are cleared.
const TunnelLogDirMode = fs.ModeSticky | 0o770

// EnsureTunnelLogDir creates the tunnel log directory dir (Root +
// TunnelLogDir) when it is missing: mode TunnelLogDirMode and group gid
// (gid < 0: there is no deyroute group, and the group bits are cleared),
// its parents like /var/log/deyroute (0750). An existing directory keeps
// its mode and owner (the installer and setup set them). systemd refuses to
// start a tunnel unit whose StandardOutput=append: directory is missing
// (209/STDOUT), and one with ReadWritePaths=/var/log/deyroute when that is
// missing (226/NAMESPACE). The hub and node services add the sticky bit to
// an existing directory when they start (TightenTunnelLogDir). Errors are
// DEY-X032.
func EnsureTunnelLogDir(dir string, gid int, chown func(path string, uid, gid int) error) error {
	fi, err := os.Stat(dir)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		return deyerr.New(deyerr.X032, deyerr.Params{"path": dir}).WithWhy(dir + " exists and is not a directory")
	case !stderrors.Is(err, fs.ErrNotExist):
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	if err := os.MkdirAll(filepath.Dir(dir), deylog.DirMode); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Dir(dir)})
	}
	mode := TunnelLogDirMode
	if gid < 0 {
		mode &^= 0o070
	}
	if err := os.Mkdir(dir, mode); err != nil && !stderrors.Is(err, fs.ErrExist) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	// Mkdir applies the umask; set the exact mode, sticky bit included.
	if err := os.Chmod(dir, mode); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	if gid >= 0 {
		if err := chown(dir, -1, gid); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
		}
	}
	return nil
}

// TightenTunnelLogDir adds the sticky bit to an existing tunnel log
// directory dir that belongs to this process's user (root in the hub and
// node services, which call it when they start). An install made before
// the directory became TunnelLogDirMode left it 0770, where the backends'
// user can remove root's logs and plant a link at a log name that systemd
// then follows as root; EnsureTunnelLogDir and the installer's repair run
// keep an existing directory as it is. Only the sticky bit is added: no
// other bit changes. A missing dir is left alone (no error: it is created
// with TunnelLogDirMode when a unit starts). A link, anything that is not a
// directory and a directory of another user are left alone too, but with an
// error naming the case, which the services log: dlog.EnsureTunnelLog then
// refuses every tunnel log there (DEY-X022). from and to are the mode bits
// (permissions, setuid, setgid, sticky) before and after; from == to when
// nothing changed.
func TightenTunnelLogDir(dir string) (from, to uint32, err error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	switch {
	case err == nil:
	case stderrors.Is(err, unix.ENOENT):
		return 0, 0, nil
	case stderrors.Is(err, unix.ELOOP), stderrors.Is(err, unix.ENOTDIR):
		// O_DIRECTORY|O_NOFOLLOW fails with ENOTDIR on a link to a
		// directory: Lstat tells the two apart.
		what := "not a directory"
		if fi, lerr := os.Lstat(dir); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
			what = "a symbolic link"
		}
		return 0, 0, fmt.Errorf("%s is %s; it is left as it is", dir, what)
	default:
		return 0, 0, &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, 0, &fs.PathError{Op: "fstat", Path: dir, Err: err}
	}
	from = st.Mode & 0o7777
	if euid := os.Geteuid(); int64(st.Uid) != int64(euid) {
		return from, from, fmt.Errorf("%s belongs to uid %d, not uid %d; it is left as it is", dir, st.Uid, euid)
	}
	if from&unix.S_ISVTX != 0 {
		return from, from, nil
	}
	to = from | unix.S_ISVTX
	if err := unix.Fchmod(fd, to); err != nil {
		return from, from, &fs.PathError{Op: "chmod", Path: dir, Err: err}
	}
	return from, to, nil
}

// TunnelLogError is the DEY-X022 of the tunnel log path that
// deylog.EnsureTunnelLog could not prepare (err): the hub and the node then
// do not start the unit. When the directory itself was refused
// (deylog.ErrUnsafeLogDir: a link, not root's, or group-writable without
// the sticky bit), the why names the reason and the fix restores the mode
// the installer sets.
func TunnelLogError(path string, err error) *deyerr.Error {
	e := deyerr.Wrap(deyerr.X022, err, deyerr.Params{"path": path})
	if stderrors.Is(err, deylog.ErrUnsafeLogDir) {
		dir := filepath.Dir(path)
		e = e.WithWhy(err.Error()).
			WithFix("make " + dir + " root's own directory with the sticky bit, not a link: chown root:deyroute " + dir +
				" && chmod 1770 " + dir + ", then retry")
	}
	return e
}

// lookupGroupID returns the numeric id of a system group (Manager.GroupID
// when nil).
func lookupGroupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}
