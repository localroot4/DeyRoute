package render

import (
	"bytes"
	"context"
	stderrors "errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Permissions of rendered backend directories and files (§7.1,
// QUESTIONS.md C.16): the backend runs as user deyroute and must read its
// files, nobody else may.
const (
	DirMode  fs.FileMode = 0o750
	FileMode fs.FileMode = 0o640
	// etcDirMode is used only when /etc/deyroute does not exist yet (tests,
	// broken installs); the installer creates it 0710 root:deyroute and it is
	// never chmodded here (§7.5).
	etcDirMode fs.FileMode = 0o710
	// maxRenderedFile bounds files read back for comparison.
	maxRenderedFile = 16 << 20
)

// HubWriter writes the hub side of planned candidates to disk: the
// rendered files into the config directory (dirs 0750, files 0640, owner
// root:deyroute) and the drop-in through systemd.Manager. It never runs
// daemon-reload in Write; the caller reloads once after writing every
// changed side.
type HubWriter struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Systemd writes drop-ins and removes instances; nil = a Manager with
	// the same Root.
	Systemd *systemd.Manager
	// Chown changes ownership; nil = os.Lchown.
	Chown func(path string, uid, gid int) error
	// DeyrouteIDs returns the uid and gid of the system user deyroute; nil =
	// LookupDeyrouteIDs.
	DeyrouteIDs func() (uid, gid int, err error)
}

// LookupDeyrouteIDs looks up the system user "deyroute" (created by the
// installer) and returns its uid and gid.
func LookupDeyrouteIDs() (uid, gid int, err error) {
	u, err := user.Lookup(config.SystemUser)
	if err != nil {
		return 0, 0, err
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func (w *HubWriter) root() string {
	if w.Root == "" {
		return "/"
	}
	return w.Root
}

func (w *HubWriter) manager() *systemd.Manager {
	if w.Systemd != nil {
		return w.Systemd
	}
	return &systemd.Manager{Root: w.Root}
}

func (w *HubWriter) chown(path string, gid int) error {
	f := w.Chown
	if f == nil {
		f = os.Lchown
	}
	if err := f(path, 0, gid); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	return nil
}

func (w *HubWriter) group() (int, error) {
	f := w.DeyrouteIDs
	if f == nil {
		f = LookupDeyrouteIDs
	}
	_, gid, err := f()
	if err != nil {
		return 0, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Join(w.root(), config.BackendsConfDir)}).
			WithWhy("the system user " + config.SystemUser + " does not exist, so the backend files cannot be made readable for it").
			WithFix("run deyroute setup again (it creates the " + config.SystemUser + " user) or: systemd-sysusers")
	}
	return gid, nil
}

// CheckConfigDir verifies that dir is a clean absolute path strictly below
// /etc/deyroute/backends with at least <backend>/<tunnel>/<leaf>, so a
// malformed side can never make the writer touch another directory.
func CheckConfigDir(dir string) error {
	clean := filepath.Clean(dir)
	prefix := config.BackendsConfDir + "/"
	rel := strings.TrimPrefix(clean, prefix)
	if clean != dir || !strings.HasPrefix(clean, prefix) || len(strings.Split(rel, "/")) < 3 {
		return deyerr.New(deyerr.X032, deyerr.Params{"path": dir}).
			WithWhy("rendered backend directories must live under " + config.BackendsConfDir + "/<backend>/<tunnel>/")
	}
	return nil
}

// checkFileName verifies a rendered file name is a clean relative path.
func checkFileName(dir, name string) error {
	if name == "" || !filepath.IsLocal(name) || filepath.Clean(name) != name {
		return deyerr.New(deyerr.X032, deyerr.Params{"path": filepath.Join(dir, name)}).
			WithWhy("a rendered file name must be a plain relative path")
	}
	return nil
}

// Write makes the hub config directory of side exactly side.Files and
// writes its drop-in. changed reports whether any file, the directory
// content or the drop-in differed (the caller then runs daemon-reload once
// and restarts the unit if it is active).
func (w *HubWriter) Write(ctx context.Context, side Side) (changed bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, deyerr.Wrap(deyerr.X031, err, deyerr.Params{"command": "write " + side.Instance})
	}
	if err := CheckConfigDir(side.ConfigDir); err != nil {
		return false, err
	}
	// Check the drop-in inputs before any file is touched, so a malformed
	// side never leaves a half-written directory behind.
	if !systemd.ValidInstance(side.Instance) {
		return false, deyerr.New(deyerr.X034, deyerr.Params{"field": "instance", "value": side.Instance})
	}
	if len(bytes.TrimSpace(side.DropIn)) == 0 {
		return false, deyerr.New(deyerr.X034, deyerr.Params{"field": "drop-in of " + side.Instance, "value": ""})
	}
	for name := range side.Files {
		if err := checkFileName(side.ConfigDir, name); err != nil {
			return false, err
		}
	}
	gid, err := w.group()
	if err != nil {
		return false, err
	}
	dir := filepath.Join(w.root(), side.ConfigDir)
	if err := w.ensureDirs(side.ConfigDir, gid); err != nil {
		return false, err
	}
	names := make([]string, 0, len(side.Files))
	for name := range side.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return changed, deyerr.Wrap(deyerr.X031, err, deyerr.Params{"command": "write " + side.Instance})
		}
		c, err := w.writeFile(dir, name, side.Files[name], gid)
		if err != nil {
			return changed, err
		}
		changed = changed || c
	}
	removed, err := removeStale(dir, side.Files)
	if err != nil {
		return changed, err
	}
	changed = changed || removed
	c, err := w.manager().WriteDropIn(side.Instance, side.DropIn)
	if err != nil {
		return changed, err
	}
	return changed || c, nil
}

// ensureDirs creates /etc/deyroute (only if missing) and every directory from
// /etc/deyroute/backends down to configDir with 0750 root:deyroute.
func (w *HubWriter) ensureDirs(configDir string, gid int) error {
	etc := filepath.Join(w.root(), config.EtcDir)
	if _, err := os.Lstat(etc); stderrors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(etc, etcDirMode); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": etc})
		}
	}
	cur := config.BackendsConfDir
	rel := strings.TrimPrefix(configDir, config.BackendsConfDir+"/")
	parts := append([]string{""}, strings.Split(rel, "/")...)
	for _, part := range parts {
		if part != "" {
			cur = cur + "/" + part
		}
		if err := w.ensureDir(filepath.Join(w.root(), cur), gid); err != nil {
			return err
		}
	}
	return nil
}

// ensureDir creates dir (0750) when missing, refuses symlinks, and enforces
// mode and ownership.
func (w *HubWriter) ensureDir(dir string, gid int) error {
	info, err := os.Lstat(dir)
	switch {
	case stderrors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(dir, DirMode); err != nil && !stderrors.Is(err, fs.ErrExist) {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
		}
	case err != nil:
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	case !info.IsDir():
		return deyerr.New(deyerr.X032, deyerr.Params{"path": dir}).WithWhy("the path exists and is not a directory (symlinks are refused)")
	}
	if err := os.Chmod(dir, DirMode); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	return w.chown(dir, gid)
}

// writeFile writes one rendered file atomically when its content differs
// and always enforces mode and ownership.
func (w *HubWriter) writeFile(dir, name string, data []byte, gid int) (bool, error) {
	target := filepath.Join(dir, name)
	if sub := filepath.Dir(name); sub != "." {
		cur := dir
		for _, part := range strings.Split(filepath.ToSlash(sub), "/") {
			cur = filepath.Join(cur, part)
			if err := w.ensureDir(cur, gid); err != nil {
				return false, err
			}
		}
	}
	info, err := os.Lstat(target)
	if err == nil && info.Mode().IsRegular() && info.Size() <= maxRenderedFile {
		old, rerr := os.ReadFile(target) // #nosec G304 -- path below the validated config dir
		if rerr == nil && bytes.Equal(old, data) {
			if err := os.Chmod(target, FileMode); err != nil {
				return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
			}
			return false, w.chown(target, gid)
		}
	}
	if err == nil && info.IsDir() {
		if err := os.RemoveAll(target); err != nil {
			return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
		}
	}
	return true, w.atomicWrite(target, data, gid)
}

// atomicWrite writes data to a temporary file next to target (0640,
// root:deyroute), fsyncs it and renames it over target (replacing a symlink
// instead of following it).
func (w *HubWriter) atomicWrite(target string, data []byte, gid int) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	if err := tmp.Chmod(FileMode); err != nil {
		return fail(err)
	}
	if err := w.chown(name, gid); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	if err := os.Rename(name, target); err != nil {
		_ = os.Remove(name)
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": target})
	}
	syncDir(dir)
	return nil
}

// removeStale deletes files in dir that are not rendered any more. Only
// the top level and subdirectories that hold rendered files are visited:
// other subdirectories belong to someone else (the canary directory
// "<tunnel>/canary" can be the parent of a node named "canary").
func removeStale(dir string, files map[string][]byte) (bool, error) {
	keep := map[string]bool{}
	visit := map[string]bool{".": true}
	for name := range files {
		keep[name] = true
		for d := filepath.Dir(name); d != "."; d = filepath.Dir(d) {
			visit[d] = true
		}
	}
	removed := false
	var walk func(rel string) error
	walk = func(rel string) error {
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Join(dir, rel)})
		}
		for _, e := range entries {
			name := filepath.Join(rel, e.Name())
			if rel == "." {
				name = e.Name()
			}
			if e.IsDir() {
				if visit[name] {
					if err := walk(name); err != nil {
						return err
					}
				}
				continue
			}
			if keep[name] {
				continue
			}
			p := filepath.Join(dir, name)
			if err := os.Remove(p); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
				return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
			}
			removed = true
		}
		return nil
	}
	return removed, walk(".")
}

// Remove stops and removes the instance (systemd.Manager.RemoveInstance:
// stop, disable, drop-in removal, daemon-reload, reset-failed), deletes its
// config directory and then every parent directory up to (excluding)
// /etc/deyroute/backends that became empty.
func (w *HubWriter) Remove(ctx context.Context, instance, configDir string) error {
	if err := CheckConfigDir(configDir); err != nil {
		return err
	}
	if err := w.manager().RemoveInstance(ctx, instance); err != nil {
		return err
	}
	dir := filepath.Join(w.root(), configDir)
	if err := w.removeDir(dir, configDir); err != nil {
		return err
	}
	stop := filepath.Join(w.root(), config.BackendsConfDir)
	for d := filepath.Dir(dir); d != stop && strings.HasPrefix(d, stop+string(filepath.Separator)); d = filepath.Dir(d) {
		if err := os.Remove(d); err != nil {
			break // not empty (or already gone): parents stay
		}
	}
	return nil
}

// removeDir deletes the config directory dir (configDir without Root).
// The canary directory <backend>/<tunnel>/canary is special: it is also
// the parent of the warm directories of a node whose id is "canary"
// (<backend>/<tunnel>/canary/<transport>), so a subdirectory that belongs
// to such an instance (its drop-in exists) is kept and the canary
// directory itself is removed only when it ends up empty.
func (w *HubWriter) removeDir(dir, configDir string) error {
	backendName, tunnel, ok := canaryParts(configDir)
	if !ok {
		if err := os.RemoveAll(dir); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
		}
		return nil
	}
	entries, err := os.ReadDir(dir)
	if stderrors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	m := w.manager()
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			inst := systemd.InstanceName(tunnel, CanaryDirName, backendName+"/"+e.Name())
			if _, err := os.Lstat(m.DropInPath(inst)); err == nil {
				continue // warm directory of node "canary"
			}
		}
		if err := os.RemoveAll(p); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
	}
	if err := os.Remove(dir); err != nil && !stderrors.Is(err, fs.ErrNotExist) && !isNotEmpty(err) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	return nil
}

// canaryParts reports whether configDir is a canary directory
// (/etc/deyroute/backends/<backend>/<tunnel>/canary) and returns its backend
// and tunnel.
func canaryParts(configDir string) (backendName, tunnel string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(configDir, config.BackendsConfDir+"/"), "/")
	if len(parts) != 3 || parts[2] != CanaryDirName || configDir != CanaryConfigDir(parts[0], parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// isNotEmpty reports a "directory not empty" failure of os.Remove.
func isNotEmpty(err error) bool {
	return stderrors.Is(err, syscall.ENOTEMPTY) || stderrors.Is(err, syscall.EEXIST)
}

// syncDir fsyncs a directory so a rename survives a crash (best effort).
func syncDir(dir string) {
	d, err := os.Open(dir) // #nosec G304 -- directory deyroute writes into
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
