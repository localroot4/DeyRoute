package setup

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/sysctl"
)

// reservedProc gives the fake kernel below root a reservation of its own.
func reservedProc(t *testing.T, root, value string) {
	t.Helper()
	p := filepath.Join(root, "proc/sys/net/ipv4/ip_local_reserved_ports")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(value+"\n"), 0o644))
}

// procTree returns every file below root/proc with its content.
func procTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "proc"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) // #nosec G304 -- test temp dir
		out[p] = string(b)
		return err
	}))
	return out
}

// TestJoinSysctlOffWritesNothing: a join that opts out (join --sysctl off
// or "no" to the kernel question) writes nothing under /etc/sysctl.d and
// changes no kernel value, records sysctl_profile off and leaves the root-only
// marker that makes the node refuse the hub's tuning. A join that only skips
// the profile without opting out, or whose sysctl step fails, leaves no
// marker.
func TestJoinSysctlOffWritesNothing(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	fakeProc(t, root)
	reservedProc(t, root, "5000")
	before := procTree(t, root)
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o.ApplySysctl, o.OptOut = false, true
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, res.SysctlProfile)
	require.Empty(t, res.ReservedPorts)
	require.Equal(t, before, procTree(t, root), "no kernel value changes")
	require.NoDirExists(t, filepath.Join(root, "etc/sysctl.d"))
	require.NoFileExists(t, filepath.Join(root, config.SysctlBackup))
	require.NoFileExists(t, filepath.Join(root, ReservePortsFile))
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, cfg.Tuning.SysctlProfile)
	marker := filepath.Join(root, HubTuningOffFile)
	fi, err := os.Lstat(marker)
	require.NoError(t, err)
	require.True(t, fi.Mode().IsRegular())
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	require.True(t, HubTuningOff(root))

	// A link is never the marker (it is read without following links).
	require.NoError(t, os.Remove(marker))
	require.NoError(t, os.Symlink("/etc/passwd", marker))
	require.False(t, HubTuningOff(root))

	// Not an opt-out: no marker (a stale one from an earlier attempt goes).
	root = t.TempDir()
	fakeProc(t, root)
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, HubTuningOffFile)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, HubTuningOffFile), nil, 0o600))
	o = joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o.ApplySysctl = false
	_, err = Join(ctxT(t), o)
	require.NoError(t, err)
	require.False(t, HubTuningOff(root))

	// A failed sysctl step is never an opt-out.
	root = t.TempDir()
	fakeProc(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/sysctl.d"), nil, 0o644)) // conf cannot be written
	steps := &stepLog{}
	o = joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), steps)
	o.ApplySysctl = true
	res, err = Join(ctxT(t), o)
	require.NoError(t, err)
	require.Contains(t, steps.final(), "sysctl=warn")
	require.Equal(t, config.SysctlOff, res.SysctlProfile)
	require.Empty(t, res.ReservedPorts)
	require.False(t, HubTuningOff(root))
}

// TestJoinBalancedReservesControlRange: the balanced profile of a join
// adds the backend control range to the live reservation (a set union: a
// reservation of another program stays) and to 99-deyroute.conf.
func TestJoinBalancedReservesControlRange(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	fakeProc(t, root)
	reservedProc(t, root, "5000")
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o.ApplySysctl = true
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, config.SysctlBalanced, res.SysctlProfile)
	require.Equal(t, []string{"30000-31999"}, res.ReservedPorts)
	require.Equal(t, "5000,30000-31999", procValue(t, root, sysctl.KeyReservedPorts))
	conf, err := os.ReadFile(filepath.Join(root, config.SysctlConfPath))
	require.NoError(t, err)
	require.Contains(t, string(conf), "(profile: balanced)")
	require.Contains(t, string(conf), "\nnet.ipv4.ip_local_reserved_ports = 5000,30000-31999\n")
	require.False(t, HubTuningOff(root))
	require.NoFileExists(t, filepath.Join(root, ReservePortsFile))

	// The revert gives back exactly the value from before.
	require.NoError(t, sysctl.Manager{Root: root}.Revert())
	require.Equal(t, "5000", procValue(t, root, sysctl.KeyReservedPorts))
}

// TestJoinReservePortsFlag: join --reserve-ports adds the entries to the
// control range for the profile applied (balanced and auto) and keeps them
// in /var/lib/deyroute/reserved-ports (root only) for the profiles the hub
// pushes later.
func TestJoinReservePortsFlag(t *testing.T) {
	for _, profile := range []string{config.SysctlBalanced, config.SysctlAuto} {
		t.Run(profile, func(t *testing.T) {
			hub := startTestHub(t)
			root := t.TempDir()
			fakeProc(t, root)
			reservedProc(t, root, "")
			o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
			f := exec.NewFake()
			f.OnPrefix("systemctl ", exec.OK(""))
			o.Runner = f
			o.ApplySysctl, o.SysctlProfile = true, profile
			o.ReservePorts = []string{"8443", "20000-20999"}
			res, err := Join(ctxT(t), o)
			require.NoError(t, err)
			require.Equal(t, profile, res.SysctlProfile)
			require.Equal(t, []string{"30000-31999", "8443", "20000-20999"}, res.ReservedPorts)
			require.Equal(t, "8443,20000-20999,30000-31999", procValue(t, root, sysctl.KeyReservedPorts))
			p := filepath.Join(root, ReservePortsFile)
			require.Equal(t, os.FileMode(0o600), mode(t, p))
			got, err := SavedReservePorts(root)
			require.NoError(t, err)
			require.Equal(t, []string{"8443", "20000-20999"}, got)
			all, err := NodeTuneReserved(root, []string{"30000-31999", "44433"})
			require.NoError(t, err)
			require.Equal(t, []string{"30000-31999", "44433", "8443", "20000-20999"}, all)
			conf, err := os.ReadFile(filepath.Join(root, config.SysctlConfPath))
			require.NoError(t, err)
			require.True(t, strings.Contains(string(conf), "net.ipv4.ip_local_reserved_ports = 8443,20000-20999,30000-31999\n"), string(conf))
		})
	}

	// The saved list is read without following links and only as a regular
	// file.
	root := t.TempDir()
	p := filepath.Join(root, ReservePortsFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	target := filepath.Join(root, "elsewhere")
	require.NoError(t, os.WriteFile(target, []byte("8443\n"), 0o600))
	require.NoError(t, os.Symlink(target, p))
	_, err := SavedReservePorts(root)
	require.Error(t, err)
	all, err := NodeTuneReserved(root, nil)
	require.Error(t, err)
	require.Equal(t, NodeReserved(), all, "the control range stays reserved")
}

// TestUninstallRestoresReservedPorts: revert and uninstall remove only the
// entries deyroute requested. Original 5000; join --reserve-ports 8443;
// another program reserves 2053; the hub pushes balanced; uninstall leaves
// 5000 and 2053.
func TestUninstallRestoresReservedPorts(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	fakeProc(t, root)
	reservedProc(t, root, "5000")
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o.ApplySysctl, o.ReservePorts = true, []string{"8443"}
	_, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, "5000,8443,30000-31999", procValue(t, root, sysctl.KeyReservedPorts))

	reservedProc(t, root, "2053,5000,8443,30000-31999") // another program
	reserved, err := NodeTuneReserved(root, nil)        // what the node adds to a hub push
	require.NoError(t, err)
	_, _, err = sysctl.Manager{Root: root}.ApplyWith(sysctl.ApplyOptions{Profile: config.SysctlBalanced, BBR: true, Reserved: reserved})
	require.NoError(t, err)
	require.Equal(t, "2053,5000,8443,30000-31999", procValue(t, root, sysctl.KeyReservedPorts))

	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: newFakeSystem(root).runner()}))
	require.Equal(t, "2053,5000", procValue(t, root, sysctl.KeyReservedPorts))
	require.NoFileExists(t, filepath.Join(root, config.SysctlBackup))
}
