package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
)

// autoProc adds what the automatic profile reads to fakeProc: 2 GiB of RAM,
// the reserved ports and fs.nr_open.
func autoProc(t *testing.T, root string) {
	t.Helper()
	fakeProc(t, root)
	for k, v := range map[string]string{
		"proc/sys/net/ipv4/ip_local_reserved_ports": "",
		"proc/sys/fs/nr_open":                       "1048576",
		"proc/meminfo":                              "MemTotal:        2097152 kB\nMemAvailable:    1048576 kB",
	} {
		p := filepath.Join(root, k)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(v+"\n"), 0o644))
	}
}

func TestReservedPorts(t *testing.T) {
	require.Equal(t, []string{"30000-31999"}, NodeReserved())
	cfg := config.NewHub("ir-1", "5.6.7.8", 44433)
	require.Equal(t, []string{"30000-31999", "44433"}, HubReserved(cfg))
	cfg.Hub.Front = config.HubFront{Enabled: true, Port: 8443}
	cfg.Tunnels = []config.Tunnel{
		{ID: "main", Enabled: true, Ports: []config.PortMap{{Listen: 443}, {Listen: 8443}}},
		{ID: "off", Enabled: false, Ports: []config.PortMap{{Listen: 2053}}},
	}
	require.Equal(t, []string{"30000-31999", "443", "8443", "44433"}, HubReserved(cfg), "sorted, unique, enabled tunnels only")
	require.Equal(t, []string{"30000-31999"}, HubReserved(nil))
}

// TestSetupHubAuto: the wizard's plan is applied as shown (kernel values,
// conf with header auto, the hub's drop-ins with one daemon-reload), the
// profile auto is saved without the consent for nodes, a second plan is
// empty, and uninstall removes all of it.
func TestSetupHubAuto(t *testing.T) {
	root := t.TempDir()
	autoProc(t, root)
	f := hubFake()
	steps := &stepLog{}
	o := hubOpts(root, f, steps, nil)
	o.SysctlProfile = config.SysctlAuto
	shown := PlanHostTune(root, config.RoleHub, sysctl.ApplyOptions{BBR: true,
		Reserved: HubReserved(config.NewHub("ir-1", "5.6.7.8", config.DefaultControlPort))})
	require.True(t, shown.Changed())
	host := shown.API("hub")
	require.Equal(t, uint64(2<<30), host.Facts.MemBytes)
	keys := map[string]api.TuneChange{}
	for _, c := range host.Changes {
		keys[c.Key] = c
	}
	require.Equal(t, "65535", keys["net.core.somaxconn"].To)
	require.Equal(t, "GOMEMLIMIT=256MiB", keys[systemd.HubUnit+" Environment"].To)
	require.Equal(t, api.TuneKindDropin, keys[systemd.HubUnit+" Environment"].Kind)
	require.Equal(t, "4096", procValue(t, root, "net.core.somaxconn"), "planning changes nothing")
	o.TunePlan = &shown

	res, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, res.SysctlProfile)
	st, _ := steps.get(StepSysctl)
	require.NotEqual(t, api.StepFailed, st.Status)
	conf, err := os.ReadFile(filepath.Join(root, config.SysctlConfPath))
	require.NoError(t, err)
	require.Contains(t, string(conf), "(profile: auto)")
	require.Equal(t, "65535", procValue(t, root, "net.core.somaxconn"))
	require.Equal(t, "30000-31999,44433", procValue(t, root, sysctl.KeyReservedPorts))
	require.Equal(t, "9223372036854775807", procValue(t, root, "fs.file-max"), "never lowered")
	drop, err := os.ReadFile(filepath.Join(root, systemd.AutoDropInPath(systemd.HubUnit)))
	require.NoError(t, err)
	require.Contains(t, string(drop), "GOMEMLIMIT=256MiB")
	require.FileExists(t, filepath.Join(root, systemd.AutoDropInPath(systemd.TunTemplate)))
	require.True(t, f.Called("systemctl daemon-reload"))
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, cfg.Tuning.SysctlProfile)
	require.False(t, cfg.Tuning.NodesAuto, "the nodes follow only after optimize auto on the hub")

	again := PlanHostTune(root, config.RoleHub, sysctl.ApplyOptions{BBR: true, Reserved: HubReserved(cfg)})
	require.False(t, again.Changed(), "%v %v", again.Sysctl.Changes, again.DropInChanges)
	chk, err := CheckHost(root, "hub", config.RoleHub)
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, chk.Profile)
	require.Empty(t, chk.Drift)

	sys := newFakeSystem(root)
	require.NoError(t, Uninstall(ctxT(t), UninstallOptions{Root: root, Runner: sys.runner()}))
	for _, p := range systemd.AutoDropInPaths() {
		require.NoFileExists(t, filepath.Join(root, p))
	}
	require.NoFileExists(t, filepath.Join(root, config.SysctlConfPath))
	require.Equal(t, "4096", procValue(t, root, "net.core.somaxconn"))
	require.Equal(t, "", procValue(t, root, sysctl.KeyReservedPorts))
}

// TestJoinAuto: join with the profile auto applies the node's own plan with
// the backend control range reserved and the node's drop-ins.
func TestJoinAuto(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	autoProc(t, root)
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	f := exec.NewFake()
	f.OnPrefix("systemctl ", exec.OK(""))
	o.Runner = f
	o.ApplySysctl = true
	o.SysctlProfile = config.SysctlAuto
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, res.SysctlProfile)
	require.Equal(t, "30000-31999", procValue(t, root, sysctl.KeyReservedPorts))
	require.FileExists(t, filepath.Join(root, systemd.AutoDropInPath(systemd.TunTemplate)))
	require.NoFileExists(t, filepath.Join(root, systemd.AutoDropInPath(systemd.HubUnit)))
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, cfg.Tuning.SysctlProfile)
}
