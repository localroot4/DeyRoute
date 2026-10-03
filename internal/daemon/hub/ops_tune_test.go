package hub

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/sysinfo"
	"github.com/localroot4/deyroute/internal/systemd"
)

// tuneHubRoot writes the fake kernel of a 2 GiB VM below root; nicMTU > 0
// adds a default route over eth0 with that MTU.
func tuneHubRoot(t *testing.T, root string, nicMTU int) {
	t.Helper()
	writeProc(t, root, map[string]string{
		"net.core.somaxconn":                        "4096",
		"net.ipv4.tcp_fin_timeout":                  "60",
		"net.ipv4.ip_local_reserved_ports":          "",
		"fs.nr_open":                                "1048576",
		"fs.file-max":                               "9223372036854775807",
		"kernel.osrelease":                          "6.1.0-test",
		"net.ipv4.tcp_congestion_control":           "cubic",
		"net.ipv4.tcp_available_congestion_control": "reno cubic",
	})
	writeTestFile(t, filepath.Join(root, "proc", "meminfo"), "MemTotal:        2097152 kB\nMemAvailable:    1048576 kB\n")
	if nicMTU > 0 {
		writeTestFile(t, filepath.Join(root, "proc", "net", "route"),
			"Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"+
				"eth0\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n")
		writeTestFile(t, filepath.Join(root, "sys", "class", "net", "eth0", "mtu"), strconv.Itoa(nicMTU)+"\n")
	}
}

func writeTestFile(t *testing.T, p, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(data), 0o644)) // #nosec G306 -- fake procfs
}

// tuneNode is a fake node agent that knows the automatic profile (or, with
// features false, an older agent): it answers tune.plan with one change
// until it applied, records every sysctl.apply and reports its tuning in
// the hello like the real agent.
type tuneNode struct {
	*fakeNode
	tmu      sync.Mutex
	features bool
	profile  string
	hash     string
	applied  bool
	drift    []api.TuneDrift
	plans    []api.SysctlArgs
	applies  []api.SysctlArgs
}

func (env *testEnv) tuneNode(id string, features bool, profile, hash string) *tuneNode {
	t := env.t
	tn := &tuneNode{fakeNode: env.joinNode(id), features: features, profile: profile, hash: hash}
	tn.on(api.CmdTunePlan, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SysctlArgs
		decode(t, cmd, &a)
		tn.tmu.Lock()
		defer tn.tmu.Unlock()
		tn.plans = append(tn.plans, a)
		res := api.SysctlResult{Facts: &api.TuneFacts{MemBytes: 512 << 20, CPUs: 1, NICMTU: 1400}, Hash: "plan-" + id}
		if !tn.applied {
			res.Changes = []api.TuneChange{{Kind: api.TuneKindSysctl, Key: "net.core.somaxconn", From: "4096", To: "65535",
				Reason: "r", Effect: api.TuneEffectNow}}
			res.Hash += "-new"
		}
		return res, nil
	})
	tn.on(api.CmdSysctlApply, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SysctlArgs
		decode(t, cmd, &a)
		tn.tmu.Lock()
		defer tn.tmu.Unlock()
		tn.applies = append(tn.applies, a)
		tn.profile, tn.hash = a.Profile, ""
		if a.Profile == config.SysctlAuto {
			tn.applied, tn.hash = true, a.InputsHash()
		}
		return api.SysctlResult{Warnings: []string{"skip x: y"},
			Changes: []api.TuneChange{{Kind: api.TuneKindSysctl, Key: "net.core.somaxconn", To: "65535"}}}, nil
	})
	tn.on(api.CmdTuneCheck, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		tn.tmu.Lock()
		defer tn.tmu.Unlock()
		return api.TuneHostCheck{Profile: tn.profile, Drift: tn.drift}, nil
	})
	return tn
}

// connect starts the control stream with the agent's hello.
func (tn *tuneNode) connect() *tuneNode {
	ctx, cancel := context.WithCancel(context.Background())
	tn.cancel = cancel
	tn.done = make(chan struct{})
	c := tn.client()
	c.Hello = func() api.Hello {
		h := api.Hello{NodeID: tn.id, Version: tn.version, Arch: "amd64", OS: "Test OS", Kernel: "6.1", MemTotal: 512 << 20}
		tn.tmu.Lock()
		defer tn.tmu.Unlock()
		if tn.features {
			h.Features = []string{api.FeatureTuneAuto}
		}
		h.TuneProfile, h.TuneHash = tn.profile, tn.hash
		return h
	}
	done := tn.done
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	tn.t.Cleanup(tn.stop)
	return tn
}

func (tn *tuneNode) appliesCopy() []api.SysctlArgs {
	tn.tmu.Lock()
	defer tn.tmu.Unlock()
	return slices.Clone(tn.applies)
}

func changeKeys(cs []api.TuneChange) map[string]api.TuneChange {
	out := map[string]api.TuneChange{}
	for _, c := range cs {
		out[c.Key] = c
	}
	return out
}

func hubDropIn(root, unit string) string { return filepath.Join(root, systemd.AutoDropInPath(unit)) }

// TestOptimizeAutoPlanApply: the plan lists the hub's and every node's
// changes without changing anything (offline nodes pending); a stale hash
// is refused (DEY-X065) before anything changes; the confirmed plan is
// applied on the hub (kernel, conf, drop-ins), saved (profile auto, the
// owner's consent for nodes) and sent to the online nodes; a second plan is
// empty; the status shows every node; another profile leaves auto.
func TestOptimizeAutoPlanApply(t *testing.T) {
	env := startHub(t, nil)
	tuneHubRoot(t, env.root, 0)
	ctx := ctxT(t)
	de := env.tuneNode("de-1", true, "", "").connect()
	env.waitOnline("de-1", true)
	env.tuneNode("nl-1", true, "", "").connect()
	env.waitOnline("nl-1", true)
	env.joinNode("fr-1") // joined, never connected: offline

	rep, err := env.client.OptimizeAutoPlan(ctx, api.AutoOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, rep.Hash)
	require.False(t, rep.Applied)
	require.Len(t, rep.Hosts, 4)
	hub := rep.Hosts[0]
	require.Equal(t, "hub", hub.Host)
	require.Equal(t, config.RoleHub, hub.Role)
	require.NotNil(t, hub.Facts)
	require.Equal(t, uint64(2<<30), hub.Facts.MemBytes)
	keys := changeKeys(hub.Changes)
	require.Equal(t, "65535", keys["net.core.somaxconn"].To)
	reserved := keys[sysctl.KeyReservedPorts].To
	require.Contains(t, reserved, "30000-31999")
	require.Contains(t, reserved, strconv.Itoa(env.h.Config().Hub.ControlPort))
	require.Contains(t, keys, systemd.TunTemplate+" OOMScoreAdjust")
	require.Equal(t, "256MiB", strings.TrimPrefix(keys[systemd.HubUnit+" Environment"].To, "GOMEMLIMIT="))
	require.NotContains(t, keys, "fs.file-max", "a higher live value is never lowered")
	require.Equal(t, "plan-de-1-new", rep.Hosts[1].Hash)
	require.Len(t, rep.Hosts[1].Changes, 1)
	require.Equal(t, "fr-1", rep.Hosts[3].Host)
	require.True(t, rep.Hosts[3].Pending)
	require.Empty(t, rep.Hosts[3].Changes)
	require.Contains(t, strings.Join(rep.Warnings, "\n"), "node fr-1 is offline")
	// Nothing changed anywhere.
	require.NoFileExists(t, filepath.Join(env.root, config.SysctlConfPath))
	require.NoFileExists(t, hubDropIn(env.root, systemd.TunTemplate))
	require.Equal(t, "4096", readProc(t, env.root, "net.core.somaxconn"))
	require.Empty(t, de.appliesCopy())
	again, err := env.client.OptimizeAutoPlan(ctx, api.AutoOptions{})
	require.NoError(t, err)
	require.Equal(t, rep.Hash, again.Hash, "the same facts give the same plan")

	// A plan other than the one shown is refused before anything changes.
	_, err = env.client.OptimizeAutoApply(ctx, api.AutoApply{Hash: "stale"}, nil)
	require.Equal(t, deyerr.X065, codeOf(err))
	require.NoFileExists(t, filepath.Join(env.root, config.SysctlConfPath))
	require.NotEqual(t, config.SysctlAuto, env.h.Config().Tuning.SysctlProfile)
	require.Empty(t, de.appliesCopy())

	var log stepLog
	out, err := env.client.OptimizeAutoApply(ctx, api.AutoApply{Hash: rep.Hash}, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.True(t, out.Applied)
	require.Equal(t, []string{"tune_plan:ok", "backup:ok", "tune_hub:ok", "tune_dropins:ok", "config:ok", "tune_nodes:ok"}, log.finished())
	conf, err := os.ReadFile(filepath.Join(env.root, config.SysctlConfPath))
	require.NoError(t, err)
	require.Contains(t, string(conf), "(profile: auto)")
	require.Equal(t, "65535", readProc(t, env.root, "net.core.somaxconn"))
	hubDrop, err := os.ReadFile(hubDropIn(env.root, systemd.HubUnit))
	require.NoError(t, err)
	require.Contains(t, string(hubDrop), "GOMEMLIMIT=256MiB")
	require.FileExists(t, hubDropIn(env.root, systemd.TunTemplate))
	cfg := env.h.Config()
	require.Equal(t, config.SysctlAuto, cfg.Tuning.SysctlProfile)
	require.True(t, cfg.Tuning.NodesAuto)
	require.Empty(t, cfg.Tuning.BackendTier, "backend items only with --backends")
	applies := de.appliesCopy()
	require.Len(t, applies, 1)
	require.Equal(t, config.SysctlAuto, applies[0].Profile)
	require.Equal(t, []string{"30000-31999"}, applies[0].Reserved)
	require.Equal(t, sysctl.PlanVersion, applies[0].PlanVersion)
	joined := strings.Join(out.Warnings, "\n")
	require.Contains(t, joined, "node de-1: skip x: y")
	require.Contains(t, joined, "node fr-1 is offline")
	env.waitEvent(state.EvConfigApplied, "de-1")

	// Idempotent: nothing left to change.
	rep, err = env.client.OptimizeAutoPlan(ctx, api.AutoOptions{})
	require.NoError(t, err)
	require.Empty(t, rep.Hosts[0].Changes, "%v", rep.Hosts[0].Changes)
	require.Empty(t, rep.Hosts[1].Changes)

	st, err := env.client.OptimizeStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, st.Profile)
	require.NotNil(t, st.Facts)
	require.Len(t, st.Nodes, 3)
	require.Equal(t, api.NodeTuneStatus{Node: "de-1", Online: true, Profile: config.SysctlAuto, Hash: applies[0].InputsHash(),
		AutoCapable: true}, st.Nodes[0])
	require.Equal(t, "fr-1", st.Nodes[2].Node)
	require.True(t, st.Nodes[2].Pending)

	// Another profile leaves auto: drop-ins and the consent go.
	st, err = env.client.OptimizeApply(ctx, config.SysctlBalanced)
	require.NoError(t, err)
	require.Equal(t, config.SysctlBalanced, st.Profile)
	require.NoFileExists(t, hubDropIn(env.root, systemd.TunTemplate))
	require.NoFileExists(t, hubDropIn(env.root, systemd.HubUnit))
	cfg = env.h.Config()
	require.Equal(t, config.SysctlBalanced, cfg.Tuning.SysctlProfile)
	require.False(t, cfg.Tuning.NodesAuto)
	applies = de.appliesCopy()
	require.Equal(t, config.SysctlBalanced, applies[len(applies)-1].Profile)

	// optimize apply --profile auto is optimize auto --yes.
	st, err = env.client.OptimizeApply(ctx, config.SysctlAuto)
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, st.Profile)
	require.True(t, env.h.Config().Tuning.NodesAuto)
}

// TestTuneConvergence: while the nodes follow the hub's automatic tuning, a
// node that reconnects with other inputs gets them exactly once (a flapping
// link is not re-tuned), a node with the hub's inputs gets nothing, an old
// agent gets balanced with a warning event, and nothing is sent without the
// owner's consent (nodes_auto false).
func TestTuneConvergence(t *testing.T) {
	env := startHub(t, func(c *config.Config) {
		c.Tuning.SysctlProfile = config.SysctlAuto
		c.Tuning.NodesAuto = true
	})
	stale := env.tuneNode("de-1", true, config.SysctlAuto, "old").connect()
	require.Eventually(t, func() bool { return len(stale.appliesCopy()) == 1 }, testWait, 10*time.Millisecond)
	require.Equal(t, config.SysctlAuto, stale.appliesCopy()[0].Profile)
	env.waitEvent(state.EvConfigApplied, "de-1")

	// The node reconnects still reporting the old inputs: not sent again.
	stale.stop()
	stale.tmu.Lock()
	stale.hash = "old"
	stale.tmu.Unlock()
	stale.connect()
	require.Eventually(t, func() bool { return stale.conns.Load() >= 2 }, testWait, 10*time.Millisecond)
	require.Never(t, func() bool { return len(stale.appliesCopy()) > 1 }, 300*time.Millisecond, 20*time.Millisecond)

	// The hub's own inputs: nothing to do.
	want := nodeTuneArgs(env.h.Config(), "nl-1", nil).InputsHash()
	same := env.tuneNode("nl-1", true, config.SysctlAuto, want).connect()
	env.waitOnline("nl-1", true)
	require.Never(t, func() bool { return len(same.appliesCopy()) > 0 }, 300*time.Millisecond, 20*time.Millisecond)

	// An agent that predates the automatic profile gets balanced.
	old := env.tuneNode("old-1", false, "", "").connect()
	require.Eventually(t, func() bool { return len(old.appliesCopy()) == 1 }, testWait, 10*time.Millisecond)
	require.Equal(t, config.SysctlBalanced, old.appliesCopy()[0].Profile)
	ev := env.waitEvent(state.EvConfigApplied, "old-1")
	require.Equal(t, state.LevelWarn, ev.Level)
	require.Contains(t, ev.Message, "predates automatic tuning")
	st, err := env.client.OptimizeStatus(ctxT(t))
	require.NoError(t, err)
	require.False(t, st.Nodes[2].AutoCapable)
	require.False(t, st.Nodes[2].Pending)

	// Without the owner's consent nothing is sent.
	_, err = env.h.mutate(func(c *config.Config) error { c.Tuning.NodesAuto = false; return nil })
	require.NoError(t, err)
	other := env.tuneNode("fr-1", true, config.SysctlAuto, "old").connect()
	env.waitOnline("fr-1", true)
	require.Never(t, func() bool { return len(other.appliesCopy()) > 0 }, 300*time.Millisecond, 20*time.Millisecond)
}

// TestOptimizeAutoBackends: --backends lists the backend tier of the hub
// with the tunnels it restarts; without it nothing is rendered again;
// applying it saves the tier and restarts the active rung whose files
// changed; revert removes the drop-ins, clears the tier (the rung restarts
// again), returns the warnings and turns the nodes off.
func TestOptimizeAutoBackends(t *testing.T) {
	saved := tierBackends
	tierBackends = append(slices.Clone(tierBackends), "tfa") // the test backend scales like Backhaul
	t.Cleanup(func() { tierBackends = saved })
	te := startTunnelHub(t)
	tuneHubRoot(t, te.root, 1400)
	ctx := ctxT(t)
	n := te.tunnelNode("de-1")
	var mu sync.Mutex
	var nodeProfiles []string
	n.on(api.CmdSysctlApply, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SysctlArgs
		decode(t, cmd, &a)
		mu.Lock()
		nodeProfiles = append(nodeProfiles, a.Profile)
		mu.Unlock()
		return api.SysctlResult{}, nil
	})
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	unit := hubUnit("main", "de-1", trAlpha)
	te.waitActive("main", "de-1", trAlpha)
	restarts := te.sd.count("restart", unit)

	// Without --backends: no backend item, nothing rendered again.
	rep, err := te.client.OptimizeAutoPlan(ctx, api.AutoOptions{})
	require.NoError(t, err)
	require.NotContains(t, changeKeys(rep.Hosts[0].Changes), "tuning.backend_tier")
	_, err = te.client.OptimizeAutoApply(ctx, api.AutoApply{Hash: rep.Hash}, nil)
	require.NoError(t, err)
	require.Equal(t, restarts, te.sd.count("restart", unit))
	require.Empty(t, te.h.Config().Tuning.BackendTier)

	wantTier := tierFor(2<<30, sysinfo.CPUs(te.root))
	rep, err = te.client.OptimizeAutoPlan(ctx, api.AutoOptions{Backends: true})
	require.NoError(t, err)
	keys := changeKeys(rep.Hosts[0].Changes)
	tier := keys["tuning.backend_tier"]
	require.Equal(t, wantTier, tier.To)
	require.Equal(t, api.TuneEffectRestartsTunnels, tier.Effect)
	require.Contains(t, tier.Reason, "main")
	require.Equal(t, "1320", keys["tuning.wg_mtu"].To, "1400 - 80")
	plain, err := te.client.OptimizeAutoPlan(ctx, api.AutoOptions{})
	require.NoError(t, err)
	require.NotEqual(t, plain.Hash, rep.Hash, "the backend items are part of the confirmed plan")

	var log stepLog
	out, err := te.client.OptimizeAutoApply(ctx, api.AutoApply{Hash: rep.Hash, Backends: true}, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.True(t, out.Applied)
	require.Contains(t, log.finished(), "tune_tunnels:ok")
	cfg := te.h.Config()
	require.Equal(t, wantTier, cfg.Tuning.BackendTier)
	require.Equal(t, 1320, cfg.Tuning.WGMTU)
	require.Equal(t, restarts+1, te.sd.count("restart", unit), "the active rung's files changed")

	// Revert: drop-ins gone, tier cleared (the rung restarts), nodes off.
	writeProc(t, te.root, map[string]string{"net.ipv4.tcp_fin_timeout": "30"}) // changed by someone else
	st, err := te.client.OptimizeRevert(ctx)
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, st.Profile)
	require.Contains(t, strings.Join(st.Warnings, "\n"), "net.ipv4.tcp_fin_timeout changed by another program")
	require.NoFileExists(t, hubDropIn(te.root, systemd.TunTemplate))
	require.NoFileExists(t, filepath.Join(te.root, systemd.SlicePath()))
	cfg = te.h.Config()
	require.Empty(t, cfg.Tuning.BackendTier)
	require.Zero(t, cfg.Tuning.WGMTU)
	require.False(t, cfg.Tuning.NodesAuto)
	require.Equal(t, restarts+2, te.sd.count("restart", unit))
	require.Equal(t, "4096", readProc(t, te.root, "net.core.somaxconn"))
	mu.Lock()
	require.Equal(t, config.SysctlOff, nodeProfiles[len(nodeProfiles)-1])
	mu.Unlock()
}

// TestTuneCheckDrift: optimize check reports drift per host; the periodic
// check emits one tune_drift event per key, not one per tick, and reports
// a key again only after it was fine in between.
func TestTuneCheckDrift(t *testing.T) {
	env := startHub(t, nil)
	tuneHubRoot(t, env.root, 0)
	ctx := ctxT(t)
	n := env.tuneNode("de-1", true, "", "").connect()
	env.waitOnline("de-1", true)
	env.joinNode("fr-1")
	_, err := env.client.OptimizeApply(ctx, config.SysctlBalanced)
	require.NoError(t, err)

	chk, err := env.client.OptimizeCheck(ctx)
	require.NoError(t, err)
	require.True(t, chk.Clean, "%+v", chk)
	require.Len(t, chk.Hosts, 3)
	require.Equal(t, config.SysctlBalanced, chk.Hosts[0].Profile)
	require.Equal(t, string(deyerr.N003), chk.Hosts[2].Error.Code)

	writeProc(t, env.root, map[string]string{"net.core.somaxconn": "1024"})
	n.tmu.Lock()
	n.drift = []api.TuneDrift{{Key: "fs.file-max", Want: "2097152", Live: "100"}}
	n.tmu.Unlock()
	chk, err = env.client.OptimizeCheck(ctx)
	require.NoError(t, err)
	require.False(t, chk.Clean)
	require.Equal(t, []api.TuneDrift{{Key: "net.core.somaxconn", Want: "65535", Live: "1024"}}, chk.Hosts[0].Drift)
	require.Len(t, chk.Hosts[1].Drift, 1)

	reported := map[string]bool{}
	for range 3 {
		env.h.tuneCheckOnce(ctx, reported)
	}
	require.Eventually(t, func() bool { return env.countEvents(state.EvTuneDrift) == 2 }, testWait, 10*time.Millisecond)
	require.Never(t, func() bool { return env.countEvents(state.EvTuneDrift) > 2 }, 200*time.Millisecond, 20*time.Millisecond)
	ev := env.waitEvent(state.EvTuneDrift, "de-1")
	require.Equal(t, state.LevelWarn, ev.Level)
	require.Equal(t, string(deyerr.X067), ev.Code)

	// Fixed, then drifted again: reported again.
	writeProc(t, env.root, map[string]string{"net.core.somaxconn": "65535"})
	env.h.tuneCheckOnce(ctx, reported)
	require.NotContains(t, reported, "hub|net.core.somaxconn")
	writeProc(t, env.root, map[string]string{"net.core.somaxconn": "2048"})
	env.h.tuneCheckOnce(ctx, reported)
	require.Eventually(t, func() bool { return env.countEvents(state.EvTuneDrift) == 3 }, testWait, 10*time.Millisecond)
}

// TestTierAndMTU: the tier and MTU rules of the backend items.
func TestTierAndMTU(t *testing.T) {
	require.Empty(t, tierFor(0, 4))
	require.Equal(t, config.BackendTierSmall, tierFor(900<<20, 2))
	require.Equal(t, config.BackendTierSmall, tierFor(8<<30, 1))
	require.Equal(t, config.BackendTierMedium, tierFor(2<<30, 2))
	require.Equal(t, config.BackendTierLarge, tierFor(4<<30, 2))
	require.Zero(t, wgMTUFor(0))
	require.Zero(t, wgMTUFor(1500))
	require.Zero(t, wgMTUFor(9000))
	require.Equal(t, 1419, wgMTUFor(1499))
	require.Equal(t, 1320, wgMTUFor(1400))
	require.Equal(t, config.MinWGMTU, wgMTUFor(1300))
}
