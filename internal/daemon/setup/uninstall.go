package setup

import (
	"context"
	stderrors "errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
)

// UninstallOptions configure Uninstall (`deyroute uninstall
// [--keep-backups]`). The CLI asks for the typed "yes" and, on a hub, for
// uninstalling the nodes (Local UninstallNodes) before calling it.
type UninstallOptions struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Runner runs systemctl and nft (exec.NewRunner() when nil).
	Runner exec.Runner
	// KeepBackups keeps /var/lib/deyroute/backups.
	KeepBackups bool
	// Progress receives every step update (may be nil).
	Progress func(api.Step)
	// Logger receives one line per step (discarded when nil).
	Logger *slog.Logger
}

// Uninstall removes deyroute from this server (spec section 5). Steps, in
// order: stop_units (deyroute-hub and deyroute-node first, so no daemon
// restarts a tunnel, then every deyroute-tun@ instance: stop + disable),
// unit_files (unit files, drop-in directories — the resource drop-ins and
// the deyroute-tunnels.slice of the automatic tuning included — and .wants
// links, then daemon-reload), firewall_remove (tables inet deyroute and inet
// deyroute_stats), sysctl_revert (compare-and-restore of the values of
// sysctl-before-deyroute.conf, the conntrack hash size and the modules-load.d
// and modprobe.d files), files (/etc/deyroute and its
// restore leftovers, /var/lib/deyroute — without backups/ when KeepBackups —,
// /var/log/deyroute, /run/deyroute) and binary (/usr/local/bin/dey and
// /usr/local/bin/deyroute, last; Linux keeps the running binary's inode).
//
// Missing units, tables and files are not errors, so Uninstall is
// idempotent. A failing step does not stop the others; each failure is
// DEY-I022 {step} wrapping the cause and all of them are returned joined.
// When any step failed the binary is kept, so `deyroute uninstall` can be run
// again after fixing the cause.
func Uninstall(ctx context.Context, o UninstallOptions) error {
	e := newEnv(envOptions{Root: o.Root, Runner: o.Runner, Progress: o.Progress, Logger: o.Logger})
	var errs []error
	run := func(step string, fn func() (string, error)) {
		e.rep.start(step)
		detail, err := fn()
		if err != nil {
			e.rep.fail(step, err)
			errs = append(errs, stepError(deyerr.I022, step, err))
			return
		}
		e.rep.ok(step, detail)
	}

	var units []string
	run(StepStopUnits, func() (string, error) {
		var err error
		units, err = e.stopUnits(ctx)
		return strconv.Itoa(len(units)) + " units", err
	})
	run(StepUnitFiles, func() (string, error) { return e.removeUnitFiles(ctx, units) })
	run(StepFirewallRemove, func() (string, error) {
		// Both deyroute tables: the firewall and the traffic accounting.
		return firewall.TableRef + ", " + firewall.StatsTable,
			stderrors.Join(firewall.Remove(ctx, e.runner), firewall.RemoveStats(ctx, e.runner))
	})
	sysctlKept := false
	run(StepSysctlRevert, func() (string, error) {
		// Compare-and-restore: the kernel values, the conntrack hash size
		// and the modules-load.d/modprobe.d files of the automatic profile.
		// (Its resource drop-ins and slice went with the unit files.)
		warnings, err := sysctl.Manager{Root: e.root}.RevertWithWarnings()
		for _, w := range warnings {
			e.log.Warn("sysctl: " + w)
		}
		sysctlKept = err != nil // keep the backup for the next run
		return config.SysctlBackup, err
	})
	run(StepFiles, func() (string, error) {
		paths := e.dataPaths(o.KeepBackups, sysctlKept)
		return strconv.Itoa(len(paths)) + " paths", install.RemovePaths(e.root, paths)
	})
	run(StepAccount, func() (string, error) { return e.removeAccount(ctx) })
	if len(errs) > 0 {
		e.rep.skip(StepBinary, "kept so that deyroute uninstall can run again")
		return stderrors.Join(errs...)
	}
	run(StepBinary, func() (string, error) {
		// The short link first, the binary itself last.
		return config.BinaryPath, install.RemovePaths(e.root, []string{
			e.path(config.ShortLinkPath), e.path(config.BinaryPath),
		})
	})
	return stderrors.Join(errs...)
}

// stopUnits stops and disables deyroute-hub, deyroute-node and every
// deyroute-tun@ instance known to systemd or present on disk. It returns
// every unit it handled (for reset-failed after the files are gone).
func (e *env) stopUnits(ctx context.Context) ([]string, error) {
	units := []string{systemd.HubUnit, systemd.NodeUnit}
	insts, lerr := e.systemd().ListInstances(ctx)
	if noSystemctl(lerr) {
		// Without systemctl no deyroute unit can be running; the files are
		// still removed by the next steps.
		e.log.Warn("systemctl is not available; no unit to stop", slog.String("err", lerr.Error()))
		return nil, nil
	}
	if lerr != nil {
		// systemctl could not list units: still handle what is on disk.
		insts = e.instancesOnDisk()
	}
	for _, st := range insts {
		units = append(units, st.Unit)
	}
	var errs []error
	if lerr != nil {
		errs = append(errs, lerr)
	}
	for _, u := range units {
		for _, verb := range []string{"stop", "disable"} {
			if _, _, err := e.runner.Run(ctx, "systemctl", []string{verb, u}, nil); err != nil && !notLoaded(err) {
				errs = append(errs, err)
			}
		}
	}
	return units, stderrors.Join(errs...)
}

// noSystemctl reports a runner error meaning systemctl is not installed.
func noSystemctl(err error) bool {
	return err != nil && (deyerr.HasCode(err, deyerr.X002) || deyerr.HasCode(err, deyerr.X030))
}

// instancesOnDisk lists deyroute-tun@ instances that have a drop-in directory.
func (e *env) instancesOnDisk() []systemd.UnitState {
	ents, _ := os.ReadDir(e.path(systemd.UnitDir))
	var out []systemd.UnitState
	for _, ent := range ents {
		unit, ok := strings.CutSuffix(ent.Name(), ".d")
		if !ok || !ent.IsDir() {
			continue
		}
		if inst, ok := systemd.InstanceOf(unit); ok && systemd.ValidInstance(inst) {
			out = append(out, systemd.UnitState{Unit: unit, Instance: inst})
		}
	}
	return out
}

// removeUnitFiles deletes every deyroute unit file, drop-in directory and
// .wants link under Root/etc/systemd/system, reloads systemd and clears
// the failed state of the removed units (best effort).
func (e *env) removeUnitFiles(ctx context.Context, units []string) (string, error) {
	dir := e.path(systemd.UnitDir)
	var paths []string
	ents, err := os.ReadDir(dir)
	if err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	for _, ent := range ents {
		name := ent.Name()
		if strings.HasPrefix(name, "deyroute-") {
			paths = append(paths, filepath.Join(dir, name))
			continue
		}
		if !ent.IsDir() || !strings.HasSuffix(name, ".wants") {
			continue
		}
		links, _ := os.ReadDir(filepath.Join(dir, name))
		for _, l := range links {
			if strings.HasPrefix(l.Name(), "deyroute-") {
				paths = append(paths, filepath.Join(dir, name, l.Name()))
			}
		}
	}
	sort.Strings(paths)
	var errs []error
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}))
		}
	}
	if _, _, err := e.runner.Run(ctx, "systemctl", []string{"daemon-reload"}, nil); err != nil && !noSystemctl(err) {
		errs = append(errs, err)
	}
	for _, u := range units {
		// Only clears "failed" bookkeeping of units that no longer exist.
		_, _, _ = e.runner.Run(ctx, "systemctl", []string{"reset-failed", u}, nil)
	}
	return strconv.Itoa(len(paths)) + " files", stderrors.Join(errs...)
}

// dataPaths is install.UninstallPlan without the binaries (removed last)
// plus the restore leftovers /etc/deyroute.restore-tmp and
// /etc/deyroute.pre-restore-* (they hold older secrets).
func (e *env) dataPaths(keepBackups, keepSysctlBackup bool) []string {
	bin := map[string]bool{e.path(config.BinaryPath): true, e.path(config.ShortLinkPath): true}
	var keep []string
	if keepSysctlBackup {
		keep = append(keep, filepath.Base(config.SysctlBackup))
	}
	var out []string
	for _, p := range install.UninstallPlanKeeping(e.root, keepBackups, keep) {
		if !bin[p] {
			out = append(out, p)
		}
	}
	etc := e.path(config.EtcDir)
	parent, base := filepath.Dir(etc), filepath.Base(etc)
	ents, _ := os.ReadDir(parent)
	for _, ent := range ents {
		n := ent.Name()
		if n == base+".restore-tmp" || strings.HasPrefix(n, base+".pre-restore-") {
			out = append(out, filepath.Join(parent, n))
		}
	}
	sort.Strings(out)
	return out
}

// RemoveAccount deletes the system user and group the installer created
// (config.SystemUser) below root; the node agent's hub-initiated uninstall
// uses it too. See removeAccount.
func RemoveAccount(ctx context.Context, root string, r exec.Runner) (string, error) {
	return newEnv(envOptions{Root: root, Runner: r}).removeAccount(ctx)
}

// removeAccount deletes the system user and group the installer created
// (config.SystemUser). Accounts that do not exist are not errors; a system
// without userdel/groupdel keeps them and says so in the step detail.
func (e *env) removeAccount(ctx context.Context) (string, error) {
	var removed []string
	for _, a := range []struct{ db, tool string }{
		{"etc/passwd", "userdel"},
		{"etc/group", "groupdel"},
	} {
		// userdel usually removes the user's group as well: re-read the
		// database before each step.
		if !hasAccount(e.path(a.db), config.SystemUser) {
			continue
		}
		if _, _, err := e.runner.Run(ctx, a.tool, []string{config.SystemUser}, nil); err != nil {
			if noTool(err) {
				return a.tool + " is not installed; remove the " + config.SystemUser + " account by hand", nil
			}
			return "", err
		}
		removed = append(removed, a.db)
	}
	if len(removed) == 0 {
		return "no " + config.SystemUser + " account", nil
	}
	return config.SystemUser + " (" + strings.Join(removed, ", ") + ")", nil
}

// hasAccount reports whether the passwd/group database at path has an entry
// named name.
func hasAccount(path, name string) bool {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed system database below Root
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, name+":") {
			return true
		}
	}
	return false
}

// noTool reports a runner error meaning the program is not installed.
func noTool(err error) bool {
	return deyerr.HasCode(err, deyerr.X002) || deyerr.HasCode(err, deyerr.X030)
}
