package setup

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

const tunInstanceUnit = "deyroute-tun@main.de-1.backhaul-wssmux.service"

// fakeSystem simulates systemctl and nft for uninstall: units stay loaded
// until stopped, disable needs the unit file, the nft table exists once.
type fakeSystem struct {
	root string

	mu         sync.Mutex
	loaded     map[string]bool
	table      bool
	statsTable bool
	nftErr     string
}

func newFakeSystem(root string) *fakeSystem {
	return &fakeSystem{root: root, table: true, statsTable: true, loaded: map[string]bool{
		"deyroute-hub.service": true, tunInstanceUnit: true,
	}}
}

func (s *fakeSystem) runner() *exec.Fake {
	f := exec.NewFake()
	f.Handler = s.handle
	return f
}

func (s *fakeSystem) handle(c exec.Call) (exec.Response, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := c.Line()
	switch {
	case line == "systemctl list-units deyroute-tun@* --all --full --no-legend --plain":
		var out []string
		for u := range s.loaded {
			if strings.HasPrefix(u, "deyroute-tun@") {
				out = append(out, u+" loaded active running DEYROUTE tunnel")
			}
		}
		return exec.OK(strings.Join(out, "\n")), true
	case c.Name == "systemctl" && len(c.Args) == 2 && c.Args[0] == "stop":
		if !s.loaded[c.Args[1]] {
			return exec.Fail(5, "Failed to stop "+c.Args[1]+": Unit "+c.Args[1]+" not loaded."), true
		}
		delete(s.loaded, c.Args[1])
		return exec.OK(""), true
	case c.Name == "systemctl" && len(c.Args) == 2 && c.Args[0] == "disable":
		file := c.Args[1]
		if strings.HasPrefix(file, "deyroute-tun@") {
			file = "deyroute-tun@.service"
		}
		if !exists(filepath.Join(s.root, "etc/systemd/system", file)) {
			return exec.Fail(1, "Failed to disable unit: Unit file "+c.Args[1]+" does not exist."), true
		}
		// Like systemd (rmdir_parents): every link of the unit goes and a
		// .wants directory its last link leaves empty is deleted too.
		wants, _ := filepath.Glob(filepath.Join(s.root, "etc/systemd/system", "*.wants", c.Args[1]))
		for _, l := range wants {
			_ = os.Remove(l)
			_ = os.Remove(filepath.Dir(l)) // fails unless empty
		}
		return exec.OK(""), true
	case line == "systemctl daemon-reload", strings.HasPrefix(line, "systemctl reset-failed "):
		return exec.OK(""), true
	case (c.Name == "userdel" || c.Name == "groupdel") && len(c.Args) == 1:
		db := "etc/passwd"
		if c.Name == "groupdel" {
			db = "etc/group"
		}
		full := filepath.Join(s.root, db)
		data, _ := os.ReadFile(full)
		var keep []string
		found := false
		for _, l := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if strings.HasPrefix(l, c.Args[0]+":") {
				found = true
				continue
			}
			keep = append(keep, l)
		}
		if !found {
			return exec.Fail(6, c.Name+": "+c.Args[0]+" does not exist"), true
		}
		_ = os.WriteFile(full, []byte(strings.Join(keep, "\n")+"\n"), 0o644)
		if c.Name == "userdel" {
			// Debian's USERGROUPS_ENAB: the user's own group goes too.
			s.dropGroup(c.Args[0])
		}
		return exec.OK(""), true
	case line == "nft delete table inet deyroute":
		if s.nftErr != "" {
			return exec.Fail(1, s.nftErr), true
		}
		if !s.table {
			return exec.Fail(1, "Error: Could not process rule: No such file or directory"), true
		}
		s.table = false
		return exec.OK(""), true
	case line == "nft delete table inet deyroute_stats":
		if s.nftErr != "" {
			return exec.Fail(1, s.nftErr), true
		}
		if !s.statsTable {
			return exec.Fail(1, "Error: Could not process rule: No such file or directory"), true
		}
		s.statsTable = false
		return exec.OK(""), true
	}
	return exec.Response{}, false
}

// dropGroup removes name from etc/group (userdel with USERGROUPS_ENAB).
func (s *fakeSystem) dropGroup(name string) {
	full := filepath.Join(s.root, "etc/group")
	data, err := os.ReadFile(full)
	if err != nil {
		return
	}
	var keep []string
	for _, l := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if !strings.HasPrefix(l, name+":") {
			keep = append(keep, l)
		}
	}
	_ = os.WriteFile(full, []byte(strings.Join(keep, "\n")+"\n"), 0o644)
}

// installedTree creates what an installed hub leaves on disk.
func installedTree(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"etc/deyroute/config.yaml":                                      "role: hub\n",
		"etc/deyroute/secrets/ca.key":                                   "key",
		"etc/deyroute/backends/backhaul/main/x.toml":                    "x",
		"etc/deyroute.pre-restore-20260101T000000Z/config.yaml":         "old",
		"var/lib/deyroute/state.db":                                     "db",
		"var/lib/deyroute/bin/xray/v1/xray":                             "bin",
		"var/lib/deyroute/bin/deyroute.prev":                            "prev",
		"var/lib/deyroute/backups/deyroute-backup-1.tar.gz.age":         "backup",
		"var/log/deyroute/hub.log":                                      "log",
		"run/deyroute/daemon.sock":                                      "",
		"usr/local/bin/deyroute":                                        "binary",
		"usr/local/bin/other":                                           "keep me",
		"etc/systemd/system/deyroute-hub.service":                       "[Unit]",
		"etc/systemd/system/deyroute-node.service":                      "[Unit]",
		"etc/systemd/system/deyroute-tun@.service":                      "[Unit]",
		"etc/systemd/system/" + tunInstanceUnit + ".d/10-deyroute.conf": "[Service]",
		"etc/systemd/system/ssh.service":                                "[Unit]",
		"etc/sysctl.d/99-deyroute.conf":                                 "# managed by deyroute (profile: balanced)\nnet.core.somaxconn = 65535\n",
		"var/lib/deyroute/sysctl-before-deyroute.conf":                  "net.core.somaxconn = 4096\n",
		"proc/sys/net/core/somaxconn":                                   "65535\n",
		"etc/passwd":                                                    "root:x:0:0:root:/root:/bin/bash\ndeyroute:x:999:996:DEYROUTE:/nonexistent:/usr/sbin/nologin\n",
		"etc/group":                                                     "root:x:0:\ndeyroute:x:996:\n",
	}
	for p, content := range files {
		full := filepath.Join(root, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	wants := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	require.NoError(t, os.MkdirAll(wants, 0o755))
	require.NoError(t, os.Symlink("/etc/systemd/system/deyroute-hub.service", filepath.Join(wants, "deyroute-hub.service")))
	require.NoError(t, os.Symlink("/lib/systemd/system/ssh.service", filepath.Join(wants, "ssh.service")))
	require.NoError(t, os.Symlink("deyroute", filepath.Join(root, "usr/local/bin/dey")))
}

func TestUninstallOrderAndIdempotence(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	sys := newFakeSystem(root)
	f := sys.runner()
	steps := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f, Progress: steps.add}))

	require.Equal(t, []string{
		"stop_units=ok", "unit_files=ok", "firewall_remove=ok", "sysctl_revert=ok", "files=ok", "account=ok", "binary=ok",
	}, steps.final())

	// The daemons stop before any tunnel unit (so nothing restarts them),
	// every unit is also disabled, units are reloaded after the files are gone.
	lines := f.Lines()
	idx := func(line string) int {
		for i, l := range lines {
			if l == line {
				return i
			}
		}
		t.Fatalf("%q not run; ran %v", line, lines)
		return -1
	}
	require.Less(t, idx("systemctl stop deyroute-hub.service"), idx("systemctl stop "+tunInstanceUnit))
	require.Less(t, idx("systemctl stop deyroute-node.service"), idx("systemctl stop "+tunInstanceUnit))
	idx("systemctl disable " + tunInstanceUnit)
	idx("systemctl disable deyroute-hub.service")
	require.Less(t, idx("systemctl disable "+tunInstanceUnit), idx("systemctl daemon-reload"))
	require.Less(t, idx("systemctl daemon-reload"), idx("nft delete table inet deyroute"))
	idx("nft delete table inet deyroute_stats")
	require.False(t, sys.table || sys.statsTable, "both deyroute tables are removed")

	for _, p := range []string{
		"etc/deyroute", "etc/deyroute.pre-restore-20260101T000000Z", "var/lib/deyroute", "var/log/deyroute", "run/deyroute",
		"usr/local/bin/deyroute", "usr/local/bin/dey", "etc/systemd/system/deyroute-hub.service",
		"etc/systemd/system/deyroute-tun@.service", "etc/systemd/system/" + tunInstanceUnit + ".d",
		"etc/systemd/system/multi-user.target.wants/deyroute-hub.service", "etc/sysctl.d/99-deyroute.conf",
	} {
		require.Falsef(t, exists(filepath.Join(root, p)), "%s still exists", p)
	}
	for _, p := range []string{
		"usr/local/bin/other", "etc/systemd/system/ssh.service", "etc/systemd/system/multi-user.target.wants/ssh.service",
	} {
		require.Truef(t, exists(filepath.Join(root, p)), "%s must stay", p)
	}
	require.Equal(t, "4096", procValue(t, root, "net.core.somaxconn"), "sysctl restored from the backup")

	// Running it again changes nothing and still succeeds.
	steps2 := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: sys.runner(), Progress: steps2.add}))
	require.Equal(t, steps.final(), steps2.final())
}

// A .wants directory that only holds deyroute's link (it was empty before
// the install) stays: the link is removed before systemctl disable, which
// would delete the directory together with its last link (S23).
func TestUninstallKeepsEmptyWantsDirectory(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	wants := filepath.Join(root, "etc/systemd/system/multi-user.target.wants")
	require.NoError(t, os.Remove(filepath.Join(wants, "ssh.service")))
	sys := newFakeSystem(root)
	f := sys.runner()
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f}))
	require.Contains(t, f.Lines(), "systemctl disable deyroute-hub.service")
	require.DirExists(t, wants)
	ents, err := os.ReadDir(wants)
	require.NoError(t, err)
	require.Empty(t, ents)

	// The fake really deletes a directory whose last link systemctl removes
	// (what the test above guards against).
	require.NoError(t, os.Symlink("/etc/systemd/system/deyroute-hub.service", filepath.Join(wants, "deyroute-hub.service")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/systemd/system/deyroute-hub.service"), []byte("[Unit]"), 0o600))
	_, _, err = f.Run(ctxT(t), "systemctl", []string{"disable", "deyroute-hub.service"}, nil)
	require.NoError(t, err)
	require.NoDirExists(t, wants)
}

func TestUninstallKeepBackups(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	sys := newFakeSystem(root)
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: sys.runner(), KeepBackups: true}))
	require.FileExists(t, filepath.Join(root, "var/lib/deyroute/backups/deyroute-backup-1.tar.gz.age"))
	require.NoFileExists(t, filepath.Join(root, "var/lib/deyroute/state.db"))
	require.NoDirExists(t, filepath.Join(root, "var/lib/deyroute/bin"))
	require.NoDirExists(t, filepath.Join(root, "etc/deyroute"))
}

func TestUninstallFailingStepKeepsGoingAndKeepsBinary(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	sys := newFakeSystem(root)
	sys.nftErr = "Error: Could not process rule: Operation not permitted"
	steps := &stepLog{}
	err := Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: sys.runner(), Progress: steps.add})
	e := requireTop(t, err, deyerr.I022)
	require.Contains(t, e.Message(), "firewall_remove")
	requireCode(t, err, deyerr.P019)
	require.Contains(t, e.Detail, "Operation not permitted")
	require.Equal(t, []string{
		"stop_units=ok", "unit_files=ok", "firewall_remove=failed", "sysctl_revert=ok", "files=ok", "account=ok", "binary=skipped",
	}, steps.final())
	st, _ := steps.get(StepFirewallRemove)
	require.Equal(t, "DEY-P019", st.Error.Code)
	require.NoDirExists(t, filepath.Join(root, "etc/deyroute"), "later steps still ran")
	require.FileExists(t, filepath.Join(root, "usr/local/bin/deyroute"), "binary kept for a re-run")

	// After the cause is fixed a re-run finishes the job.
	sys.nftErr = ""
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: sys.runner()}))
	require.NoFileExists(t, filepath.Join(root, "usr/local/bin/deyroute"))
}

func TestUninstallWithoutSystemctl(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	f := exec.NewFake()
	missing := exec.Response{Err: deyerr.New(deyerr.X002, nil)}
	f.OnPrefix("systemctl ", missing)
	f.On("nft delete table inet deyroute", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	f.On("nft delete table inet deyroute_stats", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	f.On("userdel deyroute", exec.OK(""))
	f.On("groupdel deyroute", exec.OK(""))
	steps := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f, Progress: steps.add}))
	require.NoFileExists(t, filepath.Join(root, "usr/local/bin/deyroute"))
	require.NoFileExists(t, filepath.Join(root, "etc/systemd/system/deyroute-hub.service"))
	st, _ := steps.get(StepStopUnits)
	require.Equal(t, api.StepOK, st.Status)
}

func TestUninstallStopErrorsAreCollected(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	f := exec.NewFake()
	f.On("systemctl list-units deyroute-tun@* --all --full --no-legend --plain", exec.Fail(1, "Failed to connect to bus"))
	f.OnPrefix("systemctl stop ", exec.Fail(1, "Failed to connect to bus"))
	f.OnPrefix("systemctl ", exec.OK(""))
	f.On("nft delete table inet deyroute", exec.OK(""))
	f.On("nft delete table inet deyroute_stats", exec.OK(""))
	err := Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f})
	e := requireTop(t, err, deyerr.I022)
	require.Contains(t, e.Message(), "stop_units")
	// The instance on disk was still handled although listing failed.
	require.True(t, f.Called("systemctl disable "+tunInstanceUnit))
	require.FileExists(t, filepath.Join(root, "usr/local/bin/deyroute"))
	require.NoDirExists(t, filepath.Join(root, "etc/deyroute"))
	require.NoFileExists(t, filepath.Join(root, config.SysctlConfPath))
}

func TestUninstallRemovesTheSystemAccount(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	sys := newFakeSystem(root)
	f := sys.runner()
	steps := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f, Progress: steps.add}))
	passwd, err := os.ReadFile(filepath.Join(root, "etc/passwd"))
	require.NoError(t, err)
	require.NotContains(t, string(passwd), "deyroute:")
	require.Contains(t, string(passwd), "root:")
	group, err := os.ReadFile(filepath.Join(root, "etc/group"))
	require.NoError(t, err)
	require.NotContains(t, string(group), "deyroute:")
	require.Contains(t, f.Lines(), "userdel deyroute")
	require.NotContains(t, f.Lines(), "groupdel deyroute", "userdel already removed the group")

	// A group left behind (no USERGROUPS_ENAB) is removed on its own; a
	// second run finds nothing to do.
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/group"), []byte("root:x:0:\ndeyroute:x:996:\n"), 0o644))
	f2 := sys.runner()
	steps2 := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f2, Progress: steps2.add}))
	require.Contains(t, f2.Lines(), "groupdel deyroute")
	st, _ := steps2.get(StepAccount)
	require.Contains(t, st.Detail, "etc/group")

	f3 := sys.runner()
	steps3 := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f3, Progress: steps3.add}))
	st, _ = steps3.get(StepAccount)
	require.Equal(t, "no deyroute account", st.Detail)
}

func TestUninstallWithoutUserdelKeepsGoing(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	sys := newFakeSystem(root)
	f := exec.NewFake()
	f.Handler = func(c exec.Call) (exec.Response, bool) {
		if c.Name == "userdel" {
			return exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"program": "userdel"})}, true
		}
		return sys.handle(c)
	}
	steps := &stepLog{}
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f, Progress: steps.add}))
	st, _ := steps.get(StepAccount)
	require.Contains(t, st.Detail, "userdel is not installed")
	require.Equal(t, "ok", st.Status)
}

// When the kernel settings cannot be restored, the sysctl backup survives the
// files step, so the next uninstall run can still restore them.
func TestUninstallKeepsTheSysctlBackupWhenRevertFails(t *testing.T) {
	root := t.TempDir()
	installedTree(t, root)
	// A read-only kernel entry (writes fail even for root) makes the revert
	// fail.
	ro := "/proc/sys/kernel/osrelease"
	if f, err := os.OpenFile(ro, os.O_WRONLY, 0); err == nil {
		_ = f.Close()
		t.Skip(ro + " is writable here")
	}
	proc := filepath.Join(root, "proc/sys/net/core/somaxconn")
	require.NoError(t, os.Remove(proc))
	require.NoError(t, os.Symlink(ro, proc))
	// The revert is compare-and-restore: only a value still equal to what
	// deyroute wrote is restored, so the conf names the value read back.
	live, err := os.ReadFile(ro)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/sysctl.d/99-deyroute.conf"),
		[]byte("# managed by deyroute (profile: balanced)\nnet.core.somaxconn = "+strings.TrimSpace(string(live))+"\n"), 0o600))
	sys := newFakeSystem(root)
	err = Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: sys.runner()})
	require.Error(t, err)
	require.FileExists(t, filepath.Join(root, "var/lib/deyroute/sysctl-before-deyroute.conf"))
	require.NoFileExists(t, filepath.Join(root, "var/lib/deyroute/state.db"), "everything else is removed")
	require.FileExists(t, filepath.Join(root, "usr/local/bin/deyroute"), "binary kept for a re-run")
}
