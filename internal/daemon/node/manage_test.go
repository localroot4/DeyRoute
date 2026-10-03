package node

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
)

// tuneProc writes the fake kernel of a 2 GiB VM below the node's root.
func tuneProc(t *testing.T, e *env) {
	t.Helper()
	for k, v := range map[string]string{
		"net/core/somaxconn":                        "4096",
		"net/ipv4/tcp_fin_timeout":                  "60",
		"net/ipv4/tcp_congestion_control":           "cubic",
		"net/ipv4/tcp_available_congestion_control": "reno cubic bbr",
		"net/core/default_qdisc":                    "fq_codel",
		"net/ipv4/ip_local_reserved_ports":          "",
		"fs/nr_open":                                "1048576",
		"fs/file-max":                               "9223372036854775807",
	} {
		writeFile(t, e.path("proc/sys/"+k), v+"\n", 0o644)
	}
	writeFile(t, e.path("proc/meminfo"), "MemTotal:        2097152 kB\nMemAvailable:    1048576 kB\n", 0o644)
}

// treeOf returns every file below root with its content (proc included).
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSocket != 0 {
			return err
		}
		if strings.Contains(p, "/var/log/") || strings.Contains(p, "/run/") || strings.HasSuffix(p, ".json") {
			return nil // the agent's own log and state
		}
		data, err := os.ReadFile(p) // #nosec G304 -- test temp dir
		if err == nil {
			out[p] = string(data)
		}
		return nil
	}))
	return out
}

// TestTunePlanApplyCheck: tune.plan computes the node's automatic plan and
// changes nothing; sysctl.apply with the profile auto writes the conf (header
// profile auto), the kernel values and the node's resource drop-ins and
// records the hub's inputs hash, which the next hello reports with the
// feature list; a second plan is empty (idempotent); tune.check reports a
// value changed behind deyroute's back; another profile removes the
// drop-ins and the recorded hash.
func TestTunePlanApplyCheck(t *testing.T) {
	e := newEnv(t)
	tuneProc(t, e)
	s, stop := e.startStoppable()
	require.True(t, s.Hello.HasFeature(api.FeatureTuneAuto), "the agent announces the automatic profile")
	require.Equal(t, config.SysctlOff, s.Hello.TuneProfile)
	require.Empty(t, s.Hello.TuneHash)

	bbr := true
	args := api.SysctlArgs{Profile: config.SysctlAuto, BBR: &bbr, Reserved: []string{"30000-31999"}, PlanVersion: sysctl.PlanVersion}

	// The plan writes nothing.
	before := treeOf(t, e.root)
	plan, err := call[api.SysctlResult](t, s, api.CmdTunePlan, args)
	require.NoError(t, err)
	require.Equal(t, before, treeOf(t, e.root), "tune.plan must not change the host")
	require.NotEmpty(t, plan.Hash)
	require.NotNil(t, plan.Facts)
	require.Equal(t, uint64(2<<30), plan.Facts.MemBytes)
	keys := map[string]api.TuneChange{}
	for _, c := range plan.Changes {
		keys[c.Key] = c
	}
	require.Equal(t, "65535", keys["net.core.somaxconn"].To)
	require.Equal(t, "30000-31999", keys[sysctl.KeyReservedPorts].To)
	require.Contains(t, keys, systemd.TunTemplate+" OOMScoreAdjust", "the drop-ins are part of the plan")
	require.NotContains(t, keys, "fs.file-max", "a higher live value is never lowered")
	require.Empty(t, e.sys.serviceCalls())

	// Only auto has a plan.
	_, err = call[api.SysctlResult](t, s, api.CmdTunePlan, api.SysctlArgs{Profile: config.SysctlBalanced})
	requireCode(t, err, deyerr.N050)

	// Apply.
	res, err := call[api.SysctlResult](t, s, api.CmdSysctlApply, args)
	require.NoError(t, err)
	require.Equal(t, plan.Hash, res.Hash)
	conf, err := os.ReadFile(e.path(config.SysctlConfPath))
	require.NoError(t, err)
	require.Contains(t, string(conf), "(profile: auto)")
	require.Contains(t, string(conf), "net.core.somaxconn = 65535")
	require.Equal(t, "65535\n", readFileT(t, e.path("proc/sys/net/core/somaxconn")))
	dropin := readFileT(t, e.path(systemd.AutoDropInPath(systemd.TunTemplate)))
	require.Contains(t, dropin, systemd.AutoHeader)
	require.Contains(t, dropin, "OOMScoreAdjust=300")
	require.FileExists(t, e.path(systemd.SlicePath()))
	require.Equal(t, args.InputsHash()+"\n", readFileT(t, e.path(TuneHashFile)))

	// Idempotent: the same plan again changes nothing.
	again, err := call[api.SysctlResult](t, s, api.CmdTunePlan, args)
	require.NoError(t, err)
	require.Empty(t, again.Changes, "%v", again.Changes)

	// The next stream reports what was applied.
	stop()
	s, stop = e.startStoppable()
	require.Equal(t, config.SysctlAuto, s.Hello.TuneProfile)
	require.Equal(t, args.InputsHash(), s.Hello.TuneHash)

	// Check: clean, then a runtime change is drift.
	chk, err := call[api.TuneHostCheck](t, s, api.CmdTuneCheck, nil)
	require.NoError(t, err)
	require.Equal(t, testNode, chk.Host)
	require.Equal(t, config.RoleNode, chk.Role)
	require.Equal(t, config.SysctlAuto, chk.Profile)
	require.Empty(t, chk.Drift)
	writeFile(t, e.path("proc/sys/net/core/somaxconn"), "1024\n", 0o644)
	chk, err = call[api.TuneHostCheck](t, s, api.CmdTuneCheck, nil)
	require.NoError(t, err)
	require.Len(t, chk.Drift, 1)
	require.Equal(t, api.TuneDrift{Key: "net.core.somaxconn", Want: "65535", Live: "1024"}, chk.Drift[0])

	// Leaving auto: the drop-ins and the recorded hash go.
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlBalanced, BBR: &bbr})
	require.NoError(t, err)
	require.NoFileExists(t, e.path(systemd.AutoDropInPath(systemd.TunTemplate)))
	require.NoFileExists(t, e.path(systemd.SlicePath()))
	require.NoFileExists(t, e.path(TuneHashFile))
	stop()
	s, _ = e.startStoppable()
	require.Equal(t, config.SysctlBalanced, s.Hello.TuneProfile)
	require.Empty(t, s.Hello.TuneHash)
}

// TestTuneOptionsFromInstances: UDP rungs and NAT on the node feed its own
// plan (UDP buffers, conntrack sizing); the hub's inputs pass through.
func TestTuneOptionsFromInstances(t *testing.T) {
	a := newTestAgent(t, newFakeSys(), &recHooks{})
	bbr := false
	args := api.SysctlArgs{Profile: config.SysctlAuto, BBR: &bbr, IPForward: false, Reserved: []string{"30000-31999"}}
	o := a.tuneOptions(args)
	require.False(t, o.UDPRungs)
	require.False(t, o.Conntrack)
	require.False(t, o.BBR)
	require.Equal(t, []string{"30000-31999"}, o.Reserved)

	a.instMu.Lock()
	a.st.Instances["main-de-1-hy"] = instance{Tunnel: "main", Backend: "hysteria2"}
	a.st.HubNAT = []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToAddr: "127.0.0.1", ToPort: 443}}
	a.instMu.Unlock()
	o = a.tuneOptions(args)
	require.True(t, o.UDPRungs)
	require.True(t, o.Conntrack)
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p) // #nosec G304 -- test temp dir
	require.NoError(t, err)
	return string(data)
}
