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

	mu     sync.Mutex
	loaded map[string]bool
	table  bool
	nftErr string
}

func newFakeSystem(root string) *fakeSystem {
	return &fakeSystem{root: root, table: true, loaded: map[string]bool{
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
		return exec.OK(""), true
	case line == "systemctl daemon-reload", strings.HasPrefix(line, "systemctl reset-failed "):
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
	}
	return exec.Response{}, false
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
		"stop_units=ok", "unit_files=ok", "firewall_remove=ok", "sysctl_revert=ok", "files=ok", "binary=ok",
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
		"stop_units=ok", "unit_files=ok", "firewall_remove=failed", "sysctl_revert=ok", "files=ok", "binary=skipped",
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
	err := Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: f})
	e := requireTop(t, err, deyerr.I022)
	require.Contains(t, e.Message(), "stop_units")
	// The instance on disk was still handled although listing failed.
	require.True(t, f.Called("systemctl disable "+tunInstanceUnit))
	require.FileExists(t, filepath.Join(root, "usr/local/bin/deyroute"))
	require.NoDirExists(t, filepath.Join(root, "etc/deyroute"))
	require.NoFileExists(t, filepath.Join(root, config.SysctlConfPath))
}
