package install

import (
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// UninstallPlan lists the paths `deyroute uninstall` deletes (spec section 5)
// after units are stopped/disabled, the nftables table is removed and sysctl
// is reverted (internal/sysctl owns /etc/sysctl.d/99-deyroute.conf and its
// backup, so it is not listed):
//
//   - /etc/deyroute
//   - /var/lib/deyroute, or with keepBackups every entry of it except backups/
//     (state, backend binaries, deyroute.prev, …)
//   - /usr/local/bin/deyroute and /usr/local/bin/dey
//   - /etc/systemd/system/deyroute-* (units and drop-in directories) and
//     /etc/systemd/system/*.wants/deyroute-* links
//   - /var/log/deyroute and /run/deyroute
//
// Paths are under root, sorted, and only those that exist are returned.
func UninstallPlan(root string, keepBackups bool) []string {
	return UninstallPlanKeeping(root, keepBackups, nil)
}

// UninstallPlanKeeping is UninstallPlan that also keeps the given paths
// below /var/lib/deyroute (given relative to it, e.g. "sysctl-before-deyroute.conf":
// uninstall keeps the sysctl backup when restoring the kernel settings
// failed, so a second run can still restore them).
func UninstallPlanKeeping(root string, keepBackups bool, keepInLib []string) []string {
	l := Layout{Root: root}
	var out []string
	add := func(p string) {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	add(l.Path(config.EtcDir))
	lib := l.Path(config.LibDir)
	if keepBackups || len(keepInLib) > 0 {
		keep := map[string]bool{}
		if keepBackups {
			keep[l.Path(config.BackupDir)] = true
		}
		for _, k := range keepInLib {
			keep[filepath.Join(lib, k)] = true
		}
		if ents, err := os.ReadDir(lib); err == nil {
			for _, e := range ents {
				if p := filepath.Join(lib, e.Name()); !keep[p] {
					add(p)
				}
			}
		}
	} else {
		add(lib)
	}
	add(l.Path(config.BinaryPath))
	add(l.Path(config.ShortLinkPath))
	units := l.Path("/etc/systemd/system")
	for _, pat := range []string{"deyroute-*", "*.wants/deyroute-*"} {
		if m, err := filepath.Glob(filepath.Join(units, pat)); err == nil {
			out = append(out, m...)
		}
	}
	add(l.Path(config.LogDir))
	add(l.Path(config.RunDir))
	sort.Strings(out)
	return out
}

// RemovePaths deletes every path (recursively). Each path must lie strictly
// inside root; anything else is refused. Every failure is reported
// (DEY-X032, joined) after trying all paths; missing paths are fine.
func RemovePaths(root string, paths []string) error {
	if root == "" {
		root = "/"
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": root})
	}
	var errs []error
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}))
			continue
		}
		rel, err := filepath.Rel(cleanRoot, abs)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
			strings.Count(filepath.ToSlash(rel), "/") == 0 {
			// Refuse the root itself, anything outside it and top-level
			// directories such as /etc or /usr.
			errs = append(errs, deyerr.New(deyerr.X032, deyerr.Params{"path": p}).
				WithWhy(fmt.Sprintf("refusing to delete %s: not a deyroute path under %s", p, cleanRoot)))
			continue
		}
		if err := os.RemoveAll(abs); err != nil {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}))
		}
	}
	return stderrors.Join(errs...)
}
