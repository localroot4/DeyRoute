package hub

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Echo probe (DR-17): a raw-TCP service that never answers a TLS hello
// (VMess or Shadowsocks-2022 without TLS) fails the auto probe on every
// rung. The echo probe proves the transport end to end through one hidden
// port map of the active rung to the node's probe echo, and the node
// proves the service with a TCP connect.

// echoTunnel is the add request of the echo probe tests: tunnel main on
// de-1 with the rungs alpha, beta and the given probe kind.
func echoTunnel(port int, probe string) api.TunnelAddRequest {
	return api.TunnelAddRequest{
		ID: "main", Node: "de-1", Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false),
		Ports: []api.PortSpec{{Listen: port, Probe: probe}},
	}
}

// hiddenTarget is the node side line of the hidden port map in the test
// backend's rendering ("" when the tunnel has no echo port allocated).
func hiddenTarget(te *tunnelEnv, tunnel string) string {
	ports, _ := te.h.st.CtlPorts()
	listen, echo := ports[render.ProbeListenKey(tunnel)], ports[render.ProbeEchoKey(tunnel)]
	if listen == 0 || echo == 0 {
		return ""
	}
	return "target=" + strconv.Itoa(listen) + "/tcp->127.0.0.1:" + strconv.Itoa(echo)
}

// probePort returns the port field of the probe port in tunnel show.
func probePort(t *testing.T, te *tunnelEnv, id string) api.PortMapDTO {
	t.Helper()
	d, err := te.client.TunnelShow(ctxT(t), id)
	require.NoError(t, err)
	for _, p := range d.Ports {
		if p.Listen == d.ProbePort {
			return p
		}
	}
	t.Fatalf("no probe port in %+v", d.Ports)
	return api.PortMapDTO{}
}

// TestProbeEchoKind: probe: echo brings a silent service UP through the
// echo port, the hidden map shows up nowhere as a user port, and the
// probe fails as soon as the node reports the service down.
func TestProbeEchoKind(t *testing.T) {
	te := startTunnelHub(t)
	te.sd.silent = true
	n := te.tunnelNode("de-1")
	port := freePort(t)
	te.addTunnelUp(echoTunnel(port, config.ProbeEcho))
	ts := te.waitActive("main", "de-1", trAlpha)
	require.NotEmpty(t, n.probeEchoList(), "the node runs the probe echo")

	inst := systemd.InstanceName("main", "de-1", trAlpha)
	hidden := hiddenTarget(te, "main")
	require.NotEmpty(t, hidden)
	require.Contains(t, string(n.renderedArgs(inst).Files["test.conf"]), hidden, "the active rung carries the hidden map")

	// Not a user port: not in the tunnel's ports, not opened in the firewall.
	info := te.h.tunnelInfoOf("main")
	require.Len(t, info.Ports, 1)
	pp := probePort(t, te, "main")
	require.Equal(t, port, pp.Listen)
	require.Equal(t, config.ProbeEcho, pp.Probe)
	require.Equal(t, config.ProbeEcho, pp.ProbeEffective)
	ports, _ := te.h.st.CtlPorts()
	listen := ports[render.ProbeListenKey("main")]
	spec := render.FirewallSpec(te.h.Config(), nil, false, "")
	require.NotContains(t, spec.ListenTCP, listen)
	require.GreaterOrEqual(t, listen, config.CtlRangeLow, "the hidden port lies in the backend control range (nodes and loopback only)")
	require.LessOrEqual(t, listen, config.CtlRangeHigh)

	samples, err := te.h.st.Probes("main", "de-1", trAlpha)
	require.NoError(t, err)
	require.NotEmpty(t, samples)
	require.True(t, samples[len(samples)-1].OK, "%+v", samples[len(samples)-1])
	require.Equal(t, state.StateUp, ts.State)

	// diag probe and the 60-second report use the echo probe on the probe
	// port too (health.Path has no kind "echo").
	reps, err := te.client.DiagProbe(ctxT(t), "main", true)
	require.NoError(t, err)
	require.Len(t, reps, 1)
	require.Equal(t, config.ProbeEcho, reps[0].Kind)
	require.True(t, reps[0].OK, reps[0].Error)
	te.h.tun.lookup("main").report(ctxT(t))
	ps, err := te.h.st.Probes("main", portsProbeNode, strconv.Itoa(port)+"/tcp")
	require.NoError(t, err)
	require.NotEmpty(t, ps)
	require.True(t, ps[len(ps)-1].OK, ps[len(ps)-1].Error)

	// Success needs both: the node now reports the service down.
	n.setTarget("", true)
	require.Eventually(t, func() bool {
		ts, _ := te.h.tunnelState("main")
		return ts.ServiceDown
	}, testWait, 20*time.Millisecond, "a service the node reports down fails the echo probe")
}

// TestAutoProbePicksEchoForSilentTarget: with the default probe (auto) a
// target that times out on the node's TLS hello while a TCP connect
// succeeds is marked silent, and the tunnel comes UP on the echo probe.
func TestAutoProbePicksEchoForSilentTarget(t *testing.T) {
	te := startTunnelHub(t)
	te.sd.silent = true
	n := te.tunnelNode("de-1")
	n.setTarget(health.ReasonTimeout, false)
	port := freePort(t)
	te.addTunnelUp(echoTunnel(port, ""))
	te.waitActive("main", "de-1", trAlpha)

	pp := probePort(t, te, "main")
	require.Equal(t, config.ProbeAuto, pp.Probe, "the configured kind stays auto")
	require.Equal(t, config.ProbeEcho, pp.ProbeEffective)
	require.Contains(t, pp.ProbeReason, "silent")
	require.NotEmpty(t, n.probeEchoList())
	inst := systemd.InstanceName("main", "de-1", trAlpha)
	require.Contains(t, string(n.renderedArgs(inst).Files["test.conf"]), hiddenTarget(te, "main"))

	// A reset (Shadowsocks-2022) is silent too.
	te2 := startTunnelHub(t)
	te2.sd.silent = true
	n2 := te2.tunnelNode("de-1")
	n2.setTarget(health.ReasonReset, false)
	te2.addTunnelUp(echoTunnel(freePort(t), ""))
	require.Equal(t, config.ProbeEcho, probePort(t, te2, "main").ProbeEffective)

	// Deleting the tunnel stops the probe echo and forgets the verdicts.
	echo := n.probeEchoList()[0]
	require.NoError(t, te.client.TunnelDelete(ctxT(t), "main", nil))
	n.tmu.Lock()
	_, running := n.echoes[echo]
	n.tmu.Unlock()
	require.False(t, running, "the probe echo is stopped")
	var rec map[string]echoRecord
	ok, err := te.h.st.GetMeta(metaProbeVerdict+"main", &rec)
	require.NoError(t, err)
	require.False(t, ok)
}

// TestAutoProbeKeepsTLSForTLSTarget: a target that answers the TLS hello
// keeps the auto probe: no echo port, no probe echo, nothing rendered.
func TestAutoProbeKeepsTLSForTLSTarget(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	te.addTunnelUp(echoTunnel(freePort(t), ""))
	te.waitActive("main", "de-1", trAlpha)

	pp := probePort(t, te, "main")
	require.Equal(t, config.ProbeAuto, pp.ProbeEffective)
	require.Empty(t, pp.ProbeReason)
	require.Empty(t, n.probeEchoList())
	require.Empty(t, hiddenTarget(te, "main"), "no echo port is allocated")
	inst := systemd.InstanceName("main", "de-1", trAlpha)
	require.Equal(t, 1, strings.Count(string(n.renderedArgs(inst).Files["test.conf"]), "target="), "the user port map only")
}

// TestEchoProbeFailsWhenRungBlackholed: the echo probe is not "silence is
// fine": a rung that accepts connections and forwards nothing fails it,
// and the tunnel switches to the next rung, which passes.
func TestEchoProbeFailsWhenRungBlackholed(t *testing.T) {
	te := startTunnelHub(t)
	te.sd.silent = true
	n := te.tunnelNode("de-1")
	n.setTarget(health.ReasonTimeout, false)
	te.addTunnelUp(echoTunnel(freePort(t), ""))
	te.waitActive("main", "de-1", trAlpha)

	te.sd.setBlackhole(trAlpha, true)
	ts := te.waitActive("main", "de-1", trBeta)
	require.Equal(t, trAlpha, ts.Previous.Transport)
	require.Contains(t, string(n.renderedArgs(systemd.InstanceName("main", "de-1", trBeta)).Files["test.conf"]), hiddenTarget(te, "main"),
		"every rung carries the hidden map")
	samples, err := te.h.st.Probes("main", "de-1", trAlpha)
	require.NoError(t, err)
	var failed bool
	for _, s := range samples {
		failed = failed || (!s.OK && s.Error == health.ReasonTimeout)
	}
	require.True(t, failed, "the black-holed rung failed the echo probe: %+v", samples)
}

// TestEchoProbeOldNodeKeepsAuto: a node without FeatureEchoProbe keeps
// today's probe; the silent target is reported as a warning.
func TestEchoProbeOldNodeKeepsAuto(t *testing.T) {
	te := startTunnelHub(t)
	te.oldAgents = true
	n := te.tunnelNode("de-1")
	n.setTarget(health.ReasonTimeout, false)
	te.addTunnelUp(echoTunnel(freePort(t), ""))
	te.waitActive("main", "de-1", trAlpha)

	pp := probePort(t, te, "main")
	require.Equal(t, config.ProbeAuto, pp.ProbeEffective)
	require.Contains(t, pp.ProbeReason, api.FeatureEchoProbe)
	require.Empty(t, n.probeEchoList())
	require.Empty(t, hiddenTarget(te, "main"))
	require.Eventually(t, func() bool {
		for _, w := range te.h.tunnelInfoOf("main").Warnings {
			if strings.Contains(w, "de-1") && strings.Contains(w, "silent") {
				return true
			}
		}
		return false
	}, testWait, 20*time.Millisecond)
}

// TestEchoProbeKeptAcrossHubRestart: a restarted hub plans the hidden map
// before the node reconnects (its record says the probe echo ran), so the
// active rung is neither re-rendered nor restarted, and it stays UP on the
// echo probe.
func TestEchoProbeKeptAcrossHubRestart(t *testing.T) {
	te := startTunnelHub(t)
	te.sd.silent = true
	n := te.tunnelNode("de-1")
	n.setTarget(health.ReasonTimeout, false)
	te.addTunnelUp(echoTunnel(freePort(t), ""))
	te.waitActive("main", "de-1", trAlpha)
	alpha := hubUnit("main", "de-1", trAlpha)
	inst := systemd.InstanceName("main", "de-1", trAlpha)
	payload := string(n.renderedArgs(inst).Files["test.conf"])
	n.stop()
	te.stop()

	o := te.o
	o.OnReady = nil
	env2 := &testEnv{t: t, root: te.root, sock: te.sock, runner: te.runner, ca: te.ca}
	te.sd.env = env2
	env2.startEnv(o)
	require.Eventually(t, env2.h.tunnelsReady, testWait, 20*time.Millisecond)
	n.addr = env2.h.ControlAddr().String()
	n.start()
	env2.waitOnline("de-1", true)
	c := env2.h.tun.lookup("main")
	require.NotNil(t, c)
	_, plan := c.snapshot()
	pc, ok := plan.Candidate("de-1", trAlpha)
	require.True(t, ok)
	require.NotZero(t, pc.ProbeEcho, "planned with the hidden map before the node was back")
	before, _ := env2.h.st.Probes("main", "de-1", trAlpha)
	require.Eventually(t, func() bool {
		ps, _ := env2.h.st.Probes("main", "de-1", trAlpha)
		return len(ps) > len(before)+2 && ps[len(ps)-1].OK
	}, testWait, 50*time.Millisecond)
	require.Zero(t, te.sd.count("restart", alpha), "the active rung was not restarted")
	require.Empty(t, n.restartedList())
	require.Equal(t, payload, string(n.renderedArgs(inst).Files["test.conf"]))
	ts, _ := env2.h.tunnelState("main")
	require.Equal(t, state.StateUp, ts.State)
}

// TestEchoVerdictKeptWhileServiceDown: a refused TLS probe (the service is
// down) says nothing about how the service answers: the silent verdict and
// the echo probe stay, so the tunnel is not planned twice around an outage.
func TestEchoVerdictKeptWhileServiceDown(t *testing.T) {
	for _, tc := range []struct {
		res        api.ProbeResultDTO
		silent, ok bool
	}{
		{api.ProbeResultDTO{OK: true}, false, true},
		{api.ProbeResultDTO{Error: health.ReasonNotTLS}, false, true},
		{api.ProbeResultDTO{Error: health.ReasonClosedNoData}, false, true},
		{api.ProbeResultDTO{Error: health.ReasonTimeout}, true, true},
		{api.ProbeResultDTO{Error: health.ReasonReset}, true, true},
		{api.ProbeResultDTO{Error: health.ReasonRefused}, false, false},
		{api.ProbeResultDTO{Error: health.ReasonUnreachable}, false, false},
	} {
		v, ok := verdictOf(tc.res)
		require.Equal(t, tc.silent, v.Silent, tc.res.Error)
		require.Equal(t, tc.ok, ok, tc.res.Error)
	}

	te := startTunnelHub(t)
	te.sd.silent = true
	n := te.tunnelNode("de-1")
	n.setTarget(health.ReasonTimeout, false)
	te.addTunnelUp(echoTunnel(freePort(t), ""))
	te.waitActive("main", "de-1", trAlpha)
	c := te.h.tun.lookup("main")
	tun, _ := c.snapshot()
	n.setTarget(health.ReasonRefused, false)
	c.mu.Lock()
	e := c.plain["de-1"]
	e.at = time.Time{} // due again
	c.plain["de-1"] = e
	c.mu.Unlock()
	c.refreshPlain(ctxT(t), "de-1", tun)
	v, known := c.verdict("de-1")
	require.True(t, known)
	require.True(t, v.Silent, "a refused probe keeps the verdict")
	require.False(t, c.echoStale("de-1"))
	require.Equal(t, config.ProbeEcho, probePort(t, te, "main").ProbeEffective)

	// A TLS answer is conclusive: the verdict changes and a re-plan is due.
	n.setTarget("", false)
	c.mu.Lock()
	e = c.plain["de-1"]
	e.at = time.Time{}
	c.plain["de-1"] = e
	c.mu.Unlock()
	c.refreshPlain(ctxT(t), "de-1", tun)
	v, _ = c.verdict("de-1")
	require.False(t, v.Silent)
	require.True(t, c.echoStale("de-1"))
}

// TestEchoProbeStopsWhenNodeLeaves: a node taken out of the tunnel (tunnel
// edit --node) gets its probe echo stopped and its record dropped.
func TestEchoProbeStopsWhenNodeLeaves(t *testing.T) {
	te := startTunnelHub(t)
	te.sd.silent = true
	de, nl := te.tunnelNode("de-1"), te.tunnelNode("nl-1")
	de.setTarget(health.ReasonTimeout, false)
	nl.setTarget(health.ReasonReset, false)
	req := echoTunnel(freePort(t), "")
	req.Backups = []string{"nl-1"}
	te.addTunnelUp(req)
	te.waitActive("main", "de-1", trAlpha)
	require.NotEmpty(t, nl.probeEchoList(), "the backup node runs the probe echo too")
	echo := nl.probeEchoList()[0]

	_, err := te.client.TunnelEdit(ctxT(t), "main", api.TunnelEditRequest{Nodes: []string{"de-1"}}, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		nl.tmu.Lock()
		defer nl.tmu.Unlock()
		return slices.Contains(nl.echoStops, echo)
	}, testWait, 20*time.Millisecond, "the probe echo of nl-1 is stopped")
	de.tmu.Lock()
	require.NotContains(t, de.echoStops, echo, "de-1 keeps its probe echo")
	de.tmu.Unlock()
	_, ok := te.h.tun.lookup("main").records()["nl-1"]
	require.False(t, ok)
	_, ok = te.h.tun.lookup("main").records()["de-1"]
	require.True(t, ok)
}
