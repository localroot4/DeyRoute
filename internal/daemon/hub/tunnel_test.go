package hub

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

func TestTunnelAddHappyPath(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, log := te.addTunnelUp(api.TunnelAddRequest{
		Name: "Main " + strconv.Itoa(port), Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta, "direct/native"}, Failover: fastFailover(false),
	})
	id := info.ID
	require.Equal(t, "main-"+strconv.Itoa(port), id)
	require.Equal(t, trAlpha, info.ActiveTransport)
	require.Equal(t, "de-1", info.ActiveNode)
	require.Equal(t, ClientIPMasked, info.ClientIP)
	require.Equal(t, []string{
		"install_hub:ok", "install_node:ok", "render:ok", "firewall:ok", "start:ok", "probe:ok", "up:ok",
	}, log.finished())
	last := log.last()
	require.Equal(t, "Tunnel "+id+" is UP via tfa/alpha ("+strconv.Itoa(info.RTTms)+"ms)", last.Title)
	for _, st := range log.steps {
		if st.ID == stepInstallHub {
			require.Equal(t, "install backend on hub", st.Title)
		}
	}

	// Every rung is warm on both sides; only rung 1 runs, server side (hub,
	// reverse) first.
	require.Equal(t, []string{id + ".de-1.direct-native", id + ".de-1.tfa-alpha", id + ".de-1.tfa-beta"}, n.renderedFor(id))
	for _, tr := range []string{trAlpha, trBeta, "direct/native"} {
		inst := systemd.InstanceName(id, "de-1", tr)
		require.FileExists(t, (&systemd.Manager{Root: te.root}).DropInPath(inst))
	}
	require.Equal(t, []string{hubUnit(id, "de-1", trAlpha)}, te.sd.active())
	order := te.order()
	require.GreaterOrEqual(t, len(order), 2)
	require.Equal(t, "hub start "+id+".de-1.tfa-alpha", order[0])
	require.Equal(t, "node start "+id+".de-1.tfa-alpha", order[1])

	// The config, the firewall and the dashboard.
	cfg := te.h.Config()
	tn, ok := cfg.Tunnel(id)
	require.True(t, ok)
	require.Equal(t, []string{trAlpha, trBeta, "direct/native"}, tn.Ladder.Inline)
	require.Contains(t, te.h.Firewall().Spec.ListenTCP, port)
	st, err := te.client.Status(ctxT(t))
	require.NoError(t, err)
	require.Len(t, st.Tunnels, 1)
	require.Equal(t, state.StateUp, st.Tunnels[0].State)
	require.Equal(t, trAlpha, st.Tunnels[0].ActiveTransport)
	require.False(t, st.Tunnels[0].UpSince.IsZero())
	list, err := te.client.TunnelList(ctxT(t))
	require.NoError(t, err)
	require.Len(t, list, 1)
	backups, err := os.ReadDir(filepath.Join(te.root, config.AutoBackupDir))
	require.NoError(t, err)
	require.NotEmpty(t, backups)
	te.waitEvent(state.EvTunnelUp, "de-1")

	// The engine probes: samples land in the probe history.
	require.Eventually(t, func() bool {
		ps, err := te.h.st.Probes(id, "de-1", trAlpha)
		return err == nil && len(ps) >= 2 && ps[len(ps)-1].OK
	}, testWait, 50*time.Millisecond)

	// Show: rungs, active, control ports, probes, events.
	d, err := te.client.TunnelShow(ctxT(t), id)
	require.NoError(t, err)
	require.Equal(t, port, d.ProbePort)
	require.Equal(t, config.TLSModeAuto, d.TLSMode)
	require.Len(t, d.Rungs, 3)
	require.True(t, d.Rungs[0].Active)
	require.True(t, d.Rungs[1].Warm)
	require.GreaterOrEqual(t, d.Rungs[0].ControlPort, config.CtlRangeLow)
	require.NotEmpty(t, d.Probes)
	require.NotEmpty(t, d.Events)
	require.Equal(t, 1, d.Failover.ProbeIntervalS)
	_, err = te.client.TunnelShow(ctxT(t), "nope")
	require.Equal(t, deyerr.C021, codeOf(err))
}

func TestTunnelAddRefusesBusyAndConflictingPorts(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	ctx := ctxT(t)

	// A port held by another process: DEY-P012 naming the process.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	_, err = te.client.TunnelAdd(ctx, api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: busy}}, Rungs: []string{trAlpha}}, nil)
	require.Equal(t, deyerr.P012, codeOf(err))
	e := deyerr.As(err)
	require.Contains(t, e.Message(), strconv.Itoa(busy)+"/tcp")
	require.Contains(t, e.Why(), "pid "+strconv.Itoa(os.Getpid()))
	require.Empty(t, te.h.Config().Tunnels)

	// Reserved ports: DEY-P011.
	for _, p := range []int{22, config.DefaultControlPort, 30500} {
		_, err = te.client.TunnelAdd(ctx, api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p}}}, nil)
		require.Equal(t, deyerr.P011, codeOf(err), p)
	}

	// Another tunnel's port: DEY-C003; the same port twice too.
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Name: "one", Node: "de-1", Ports: []api.PortSpec{{Listen: port}}, Rungs: []string{trAlpha}, Failover: fastFailover(false)})
	_, err = te.client.TunnelAdd(ctx, api.TunnelAddRequest{Name: "two", Node: "de-1", Ports: []api.PortSpec{{Listen: port}}, Rungs: []string{trAlpha}}, nil)
	require.Equal(t, deyerr.C003, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), info.ID)
	p2 := freePort(t)
	_, err = te.client.TunnelAdd(ctx, api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2}, {Listen: p2}}}, nil)
	require.Equal(t, deyerr.C003, codeOf(err))

	// Request checks.
	cases := []struct {
		req  api.TunnelAddRequest
		code deyerr.Code
	}{
		{api.TunnelAddRequest{Ports: []api.PortSpec{{Listen: p2}}}, deyerr.C008},
		{api.TunnelAddRequest{Node: "xx-1", Ports: []api.PortSpec{{Listen: p2}}}, deyerr.C010},
		{api.TunnelAddRequest{Node: "de-1", Backups: []string{"de-1"}, Ports: []api.PortSpec{{Listen: p2}}}, deyerr.C002},
		{api.TunnelAddRequest{Node: "de-1"}, deyerr.C013},
		{api.TunnelAddRequest{ID: "Bad Id", Node: "de-1", Ports: []api.PortSpec{{Listen: p2}}}, deyerr.C007},
		{api.TunnelAddRequest{ID: info.ID, Node: "de-1", Ports: []api.PortSpec{{Listen: p2}}}, deyerr.C002},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: 70000}}}, deyerr.P010},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2, Proto: "sctp"}}}, deyerr.P010},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2, Target: "nowhere"}}}, deyerr.C004},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2}}, Ladder: "missing"}, deyerr.C012},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2}}, Rungs: []string{"nope/x"}}, deyerr.C005},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2}}, FixedTransport: "nope/x"}, deyerr.C005},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2}}, Policy: "random"}, deyerr.C013},
		{api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p2, Proto: "udp"}}, Rungs: []string{trDelta}}, deyerr.C009},
	}
	for i, c := range cases {
		_, err := te.client.TunnelAdd(ctx, c.req, nil)
		require.Equal(t, c.code, codeOf(err), "case %d: %v", i, err)
	}
	many := make([]api.PortSpec, config.MaxPortMaps+1)
	for i := range many {
		many[i] = api.PortSpec{Listen: 40000 + i}
	}
	_, err = te.client.TunnelAdd(ctx, api.TunnelAddRequest{Node: "de-1", Ports: many}, nil)
	require.Equal(t, deyerr.C015, codeOf(err))
	require.Len(t, te.h.Config().Tunnels, 1)
}

func TestTunnelSkippedRungs(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	n.tmu.Lock()
	n.udpBlocked = true
	n.tmu.Unlock()
	port := freePort(t)
	var log stepLog
	info, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{
		Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs:    []string{trDelta, trGamma, trDL, trAlpha},
		Failover: fastFailover(false),
	}, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Equal(t, trAlpha, info.ActiveTransport)
	// The hub could not install tfdl: that step warns, the tunnel comes up.
	require.Contains(t, log.finished(), "install_hub:warn")

	ev := te.waitEvent(state.EvRungSkipped, "de-1")
	require.Equal(t, state.LevelWarn, ev.Level)
	codes := map[string]string{}
	evs, err := te.h.st.Events(state.EventFilter{Types: []string{state.EvRungSkipped}})
	require.NoError(t, err)
	for _, e := range evs {
		codes[e.ToTransport] = e.Code
	}
	require.Equal(t, map[string]string{
		trDelta: string(deyerr.B006), // Validate refused it
		trGamma: string(deyerr.B007), // UDP between hub and node is blocked
		trDL:    string(deyerr.B001), // the binary could not be installed
	}, codes)
	ns, _ := te.h.nodeState("de-1")
	require.NotNil(t, ns.UDPOK)
	require.False(t, *ns.UDPOK)

	ts, _ := te.h.tunnelState(info.ID)
	require.Len(t, ts.Skipped, 3)
	for _, sk := range ts.Skipped {
		require.WithinDuration(t, time.Now().Add(DefaultRecheckInterval), sk.RecheckAt, time.Minute)
	}
	st, err := te.client.Status(ctxT(t))
	require.NoError(t, err)
	var yellow int
	for _, w := range st.Warnings {
		if w.Tunnel == info.ID && strings.Contains(w.Message, "is skipped") {
			yellow++
		}
	}
	require.Equal(t, 3, yellow)

	// A manual switch to a skipped rung is refused with its reason.
	err = te.client.TunnelSwitch(ctxT(t), info.ID, api.SwitchRequest{Transport: trGamma})
	require.Equal(t, deyerr.B007, codeOf(err))

	// The re-check: UDP works again, the rung comes back (rung_restored).
	n.tmu.Lock()
	n.udpBlocked = false
	n.tmu.Unlock()
	c := te.h.tun.lookup(info.ID)
	require.NotNil(t, c)
	c.recheck(ctxT(t))
	restored := te.waitEvent(state.EvRungRestored, "de-1")
	require.Equal(t, trGamma, restored.ToTransport)
	ts, _ = te.h.tunnelState(info.ID)
	require.Len(t, ts.Skipped, 2)
	require.NotContains(t, ts.Skipped, state.Candidate{Node: "de-1", Transport: trGamma}.Key())
	require.Equal(t, 1, te.countEvents(state.EvRungRestored))
}

func TestTunnelSwitchResetPause(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta, "direct/native"}, Failover: fastFailover(false)})
	id := info.ID
	ctx := ctxT(t)

	require.NoError(t, te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: trBeta}))
	ts := te.waitActive(id, "de-1", trBeta)
	require.Empty(t, ts.SwitchTimes, "a manual switch is not counted")
	require.Equal(t, []string{hubUnit(id, "de-1", trBeta)}, te.sd.active())
	require.Equal(t, 1, te.sd.count("stop", hubUnit(id, "de-1", trAlpha)))
	ev := te.waitEvent(state.EvManualSwitch, "de-1")
	require.Equal(t, trAlpha, ev.FromTransport)
	require.Equal(t, trBeta, ev.ToTransport)
	require.Zero(t, te.countEvents(state.EvSwitchTransport))

	// Pause / resume.
	require.NoError(t, te.client.TunnelPause(ctx, id))
	ts, _ = te.h.tunnelState(id)
	require.Equal(t, state.StatePaused, ts.State)
	require.True(t, ts.Paused)
	st, err := te.client.Status(ctx)
	require.NoError(t, err)
	require.True(t, st.Tunnels[0].Paused)
	require.NoError(t, te.client.TunnelResume(ctx, id))
	te.waitActive(id, "de-1", trBeta)

	// Reset: back to rung 1 now.
	require.NoError(t, te.client.TunnelReset(ctx, id))
	te.waitActive(id, "de-1", trAlpha)
	require.Equal(t, []string{hubUnit(id, "de-1", trAlpha)}, te.sd.active())

	// Switch errors.
	require.Equal(t, deyerr.C013, codeOf(te.client.TunnelSwitch(ctx, id, api.SwitchRequest{})))
	require.Equal(t, deyerr.C013, codeOf(te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: trBeta, Node: "de-1"})))
	require.Equal(t, deyerr.F006, codeOf(te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: trGamma})))
	require.Equal(t, deyerr.F006, codeOf(te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Node: "nl-1"})))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelSwitch(ctx, "nope", api.SwitchRequest{Node: "de-1"})))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelReset(ctx, "nope")))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelPause(ctx, "nope")))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelResume(ctx, "nope")))

	// A switch to a rung whose unit fails reports DEY-B003 and stays.
	te.sd.mu.Lock()
	te.sd.crash[trBeta] = true
	te.sd.mu.Unlock()
	err = te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: trBeta})
	require.Equal(t, deyerr.B003, codeOf(err))
	te.waitActive(id, "de-1", trAlpha)
	_ = n
}

func TestTunnelDeleteLeavesNothing(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	require.FileExists(t, te.h.secrets.TokenPath(id))
	var log stepLog
	require.NoError(t, te.client.TunnelDelete(ctxT(t), id, log.add))
	require.Equal(t, []string{"stop:ok", "remove:ok", "config:ok", "firewall:ok", "cleanup:ok"}, log.finished())

	// Config, state, secrets, ports.
	_, ok := te.h.Config().Tunnel(id)
	require.False(t, ok)
	_, ok, err := te.h.st.GetTunnel(id)
	require.NoError(t, err)
	require.False(t, ok)
	ports, err := te.h.st.CtlPorts()
	require.NoError(t, err)
	for k := range ports {
		require.False(t, strings.HasPrefix(k, id+"/"), k)
	}
	probes, err := te.h.st.Probes(id, "de-1", trAlpha)
	require.NoError(t, err)
	require.Empty(t, probes)
	require.NoFileExists(t, te.h.secrets.TokenPath(id))
	require.NotContains(t, te.h.Firewall().Spec.ListenTCP, port)

	// Units and files on the hub.
	require.Empty(t, te.sd.active())
	require.False(t, dirExists(te.root, filepath.Join(config.BackendsConfDir, "tfa", id)))
	entries, err := os.ReadDir(filepath.Join(te.root, systemd.UnitDir))
	if err == nil {
		for _, e := range entries {
			require.False(t, strings.Contains(e.Name(), "@"+id+"."), e.Name())
		}
	}
	// And on the node.
	require.Empty(t, n.renderedFor(id))
	require.Contains(t, n.removedList(), id+".de-1.tfa-alpha")
	require.Contains(t, n.removedList(), id+".de-1.tfa-beta")
	require.Nil(t, te.h.tun.lookup(id))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelDelete(ctxT(t), id, nil)))

	// The listen port is free again.
	ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	require.NoError(t, err)
	_ = ln.Close()
}

func TestTunnelBackupAddWarmsTheNode(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	nl := te.tunnelNode("nl-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	require.Empty(t, nl.renderedFor(id))

	var log stepLog
	require.NoError(t, te.client.TunnelBackupAdd(ctxT(t), id, "nl-1", log.add))
	last := log.last()
	require.Equal(t, "backup nl-1 ready (warm)", last.Title)
	require.Equal(t, api.StepOK, last.Status)
	require.Equal(t, "2 of 2 rungs warm", last.Detail)
	require.Contains(t, log.finished(), "render:ok")
	require.Equal(t, []string{id + ".nl-1.tfa-alpha", id + ".nl-1.tfa-beta"}, nl.renderedFor(id))
	tn, _ := te.h.Config().Tunnel(id)
	require.Equal(t, []string{"de-1", "nl-1"}, tn.Nodes)
	// Warm only: nothing started on the backup.
	require.Zero(t, nl.startedCount(id+".nl-1.tfa-alpha"))
	d, err := te.client.TunnelShow(ctxT(t), id)
	require.NoError(t, err)
	require.Len(t, d.Rungs, 4)

	// The engine knows the backup: a node switch goes there.
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Node: "nl-1"}))
	te.waitActive(id, "nl-1", trAlpha)
	require.Equal(t, 1, nl.startedCount(id+".nl-1.tfa-alpha"))

	// Errors.
	require.Equal(t, deyerr.C002, codeOf(te.client.TunnelBackupAdd(ctxT(t), id, "nl-1", nil)))
	require.Equal(t, deyerr.N008, codeOf(te.client.TunnelBackupAdd(ctxT(t), id, "xx-1", nil)))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelBackupAdd(ctxT(t), "nope", "nl-1", nil)))

	// Removing the backup it runs on moves the tunnel back to de-1.
	require.NoError(t, te.client.TunnelBackupRemove(ctxT(t), id, "nl-1"))
	te.waitActive(id, "de-1", trAlpha)
	require.Empty(t, nl.renderedFor(id))
	tn, _ = te.h.Config().Tunnel(id)
	require.Equal(t, []string{"de-1"}, tn.Nodes)
	require.Equal(t, deyerr.C008, codeOf(te.client.TunnelBackupRemove(ctxT(t), id, "de-1")))
	require.Equal(t, deyerr.C010, codeOf(te.client.TunnelBackupRemove(ctxT(t), id, "nl-1")))
}

func TestPortAddRestartsOnlyTheActiveCandidate(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	p1 := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p1}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	alpha, beta := hubUnit(id, "de-1", trAlpha), hubUnit(id, "de-1", trBeta)
	require.Equal(t, 1, te.sd.count("start", alpha))

	p2 := freePort(t)
	var log stepLog
	out, err := te.client.PortAdd(ctxT(t), id, []api.PortSpec{{Listen: p2}}, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Len(t, out.Ports, 2)
	require.Equal(t, []string{"check_ports:ok", "firewall:ok", "render:ok", "engine:ok", "restart:ok"}, log.finished())
	require.Equal(t, 1, te.sd.count("restart", alpha))
	require.Zero(t, te.sd.count("restart", beta))
	require.Zero(t, te.sd.count("start", beta))
	require.Equal(t, []string{id + ".de-1.tfa-alpha"}, n.restartedList())
	require.Contains(t, te.h.Firewall().Spec.ListenTCP, p2)
	te.waitActive(id, "de-1", trAlpha)

	// The new port works through the tunnel: the 4-stage port check.
	res, err := te.client.PortCheck(ctxT(t), api.PortCheckRequest{Port: p2})
	require.NoError(t, err)
	require.False(t, res.BindFree, "the active unit holds it")
	require.True(t, res.FirewallOpen)
	require.Equal(t, "de-1", res.Node)
	require.NotNil(t, res.NodeReachable)
	require.True(t, *res.NodeReachable)
	require.Equal(t, 7, res.NodeRTTms)
	require.Equal(t, id, res.Tunnel)
	require.NotNil(t, res.TunnelOK)
	require.True(t, *res.TunnelOK)
	require.Contains(t, res.Note, "filtering inside Iran is not measured")

	// Refusals: busy, reserved, twice, another tunnel.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	defer ln.Close()
	_, err = te.client.PortAdd(ctxT(t), id, []api.PortSpec{{Listen: ln.Addr().(*net.TCPAddr).Port}}, nil)
	require.Equal(t, deyerr.P012, codeOf(err))
	_, err = te.client.PortAdd(ctxT(t), id, []api.PortSpec{{Listen: 22}}, nil)
	require.Equal(t, deyerr.P011, codeOf(err))
	_, err = te.client.PortAdd(ctxT(t), id, []api.PortSpec{{Listen: p2}}, nil)
	require.Equal(t, deyerr.C003, codeOf(err))
	_, err = te.client.PortAdd(ctxT(t), id, nil, nil)
	require.Equal(t, deyerr.C013, codeOf(err))
	_, err = te.client.PortAdd(ctxT(t), "nope", []api.PortSpec{{Listen: p2}}, nil)
	require.Equal(t, deyerr.C021, codeOf(err))

	// Remove: again only the active transport restarts.
	out, err = te.client.PortRemove(ctxT(t), id, p2, "")
	require.NoError(t, err)
	require.Len(t, out.Ports, 1)
	require.Equal(t, 2, te.sd.count("restart", alpha))
	require.Zero(t, te.sd.count("restart", beta))
	require.NotContains(t, te.h.Firewall().Spec.ListenTCP, p2)
	_, err = te.client.PortRemove(ctxT(t), id, p1, "tcp")
	require.Equal(t, deyerr.C013, codeOf(err), "the last port stays")
	_, err = te.client.PortRemove(ctxT(t), id, 9, "tcp")
	require.Equal(t, deyerr.C013, codeOf(err))
	_, err = te.client.PortRemove(ctxT(t), id, p1, "sctp")
	require.Equal(t, deyerr.P010, codeOf(err))
}

func TestReconcileAfterRestartAdoptsRunningUnit(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	alpha := hubUnit(id, "de-1", trAlpha)
	// A stray unit of the same tunnel runs as well (it must be stopped).
	te.sd.setUnit(hubUnit(id, "de-1", trBeta), "active")
	n.stop()
	te.stop()
	require.Equal(t, "active", te.sd.state(alpha), "a hub stop never stops tunnel units")
	startsBefore := te.sd.count("start", alpha)
	nodeStartsBefore := n.startedCount(id + ".de-1.tfa-alpha")

	// A new hub on the same root adopts the running unit without a restart.
	o := te.o
	o.OnReady = nil
	env2 := &testEnv{t: t, root: te.root, sock: te.sock, runner: te.runner, ca: te.ca}
	te.sd.env = env2
	env2.startEnv(o)
	var ts state.TunnelState
	require.Eventually(t, func() bool {
		ts, _ = env2.h.tunnelState(id)
		return ts.State == state.StateUp && ts.Active.Transport == trAlpha && ts.LastRTTms >= 0 && env2.h.tun.lookup(id) != nil
	}, testWait, 20*time.Millisecond)
	require.Contains(t, ts.TransitionCause, "reconciled after hub restart")
	require.Equal(t, startsBefore, te.sd.count("start", alpha))
	require.Zero(t, te.sd.count("restart", alpha))
	require.Equal(t, nodeStartsBefore, n.startedCount(id+".de-1.tfa-alpha"))
	require.Equal(t, "inactive", te.sd.state(hubUnit(id, "de-1", trBeta)), "the stray unit is stopped")
	// The adopted candidate keeps being probed.
	before, _ := env2.h.st.Probes(id, "de-1", trAlpha)
	require.Eventually(t, func() bool {
		ps, _ := env2.h.st.Probes(id, "de-1", trAlpha)
		return len(ps) > len(before) && ps[len(ps)-1].OK
	}, testWait, 50*time.Millisecond)
	st, err := env2.client.Status(ctxT(t))
	require.NoError(t, err)
	require.Equal(t, state.StateUp, st.Tunnels[0].State)
}

func TestTunnelEditEnableDisableRestart(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	te.tunnelNode("nl-1")
	p1, p2 := freePort(t), freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p1}, {Listen: p2}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	ctx := ctxT(t)

	// Edit: name, policy, probe port, node order, ladder: the active rung
	// disappears, so the engine moves to the new rung 1.
	name, policy, probe := "Renamed", config.PolicyTransportOnly, p2
	var log stepLog
	out, err := te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{
		Name: &name, Policy: &policy, ProbePort: &probe, Nodes: []string{"de-1", "nl-1"},
		Rungs: []string{trBeta, "direct/native"},
	}, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Equal(t, "Renamed", out.Name)
	require.Equal(t, config.PolicyTransportOnly, out.Policy)
	require.Equal(t, []string{trBeta, "direct/native"}, out.Ladder)
	require.Contains(t, log.finished(), "engine:ok")
	te.waitActive(id, "de-1", trBeta)
	d, err := te.client.TunnelShow(ctx, id)
	require.NoError(t, err)
	require.Equal(t, p2, d.ProbePort)
	require.Len(t, d.Rungs, 4)
	// tfa/alpha is gone everywhere.
	require.False(t, dirExists(te.root, filepath.Join(config.BackendsConfDir, "tfa", id, "de-1", "alpha")))

	// Edit errors.
	bad := "x"
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{Ladder: &bad}, nil)
	require.Equal(t, deyerr.C012, codeOf(err))
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{Nodes: []string{"xx-1"}}, nil)
	require.Equal(t, deyerr.C010, codeOf(err))
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{Rungs: []string{"no/pe"}}, nil)
	require.Equal(t, deyerr.C005, codeOf(err))
	wrong := 1
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{ProbePort: &wrong}, nil)
	require.Equal(t, deyerr.C013, codeOf(err))
	empty := " "
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{Name: &empty}, nil)
	require.Equal(t, deyerr.C013, codeOf(err))
	custom := config.TLSModeCustom
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{TLSMode: &custom}, nil)
	require.Error(t, err)
	_, err = te.client.TunnelEdit(ctx, "nope", api.TunnelEditRequest{Name: &name}, nil)
	require.Equal(t, deyerr.C021, codeOf(err))
	fo := fastFailover(true)
	fo.Policy = config.PolicyTransportThenNode
	out, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{Failover: fo}, nil)
	require.NoError(t, err)
	require.Equal(t, config.PolicyTransportThenNode, out.Policy)

	// Disable: units stop, DISABLED, ports leave the firewall.
	require.NoError(t, te.client.TunnelSetEnabled(ctx, id, false))
	require.Empty(t, te.sd.active())
	ts, _ := te.h.tunnelState(id)
	require.Equal(t, state.StateDisabled, ts.State)
	require.NotContains(t, te.h.Firewall().Spec.ListenTCP, p1)
	require.Equal(t, deyerr.F007, codeOf(te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: trBeta})))
	require.Equal(t, deyerr.F007, codeOf(te.client.TunnelRestart(ctx, id)))
	st, err := te.client.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, state.StateDisabled, st.Tunnels[0].State)
	// Warm files stay while disabled.
	require.True(t, dirExists(te.root, filepath.Join(config.BackendsConfDir, "tfa", id, "de-1", "beta")))

	// Enable: from rung 1 again.
	require.NoError(t, te.client.TunnelSetEnabled(ctx, id, true))
	te.waitActive(id, "de-1", trBeta)
	require.Contains(t, te.h.Firewall().Spec.ListenTCP, p1)
	require.NoError(t, te.client.TunnelSetEnabled(ctx, id, true))

	// Restart: stop, clear the failover history, start at rung 1.
	require.NoError(t, te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: "direct/native"}))
	te.waitActive(id, "de-1", "direct/native")
	require.NoError(t, te.client.TunnelRestart(ctx, id))
	te.waitActive(id, "de-1", trBeta)
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelSetEnabled(ctx, "nope", true)))
	require.Equal(t, deyerr.C021, codeOf(te.client.TunnelRestart(ctx, "nope")))
}

func TestTunnelTestLadderAndCanaryFailback(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	fo := fastFailover(true)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trDelta, trBeta}, Failover: fo})
	id := info.ID
	ctx := ctxT(t)

	// Test ladder: one line per rung, the skipped one included; back on rung 1.
	var log stepLog
	res, err := te.client.TunnelTestLadder(ctx, id, log.add)
	require.NoError(t, err)
	require.Len(t, res, 3)
	require.True(t, res[0].OK)
	require.NotEmpty(t, res[1].Skipped)
	require.True(t, res[2].OK)
	require.Equal(t, []string{"rung:de-1/tfa/alpha:ok", "rung:de-1/tfa/delta:skipped", "rung:de-1/tfa/beta:ok"}, log.finished())
	require.Equal(t, "tfa/beta on de-1", log.steps[2].Title)
	te.waitActive(id, "de-1", trAlpha)
	_, err = te.client.TunnelTestLadder(ctx, "nope", nil)
	require.Equal(t, deyerr.C021, codeOf(err))

	// Away from rung 1 the canary runs; after recover_threshold (2) canary
	// passes the tunnel fails back and the canary stops.
	require.NoError(t, te.client.TunnelSwitch(ctx, id, api.SwitchRequest{Transport: trBeta}))
	canary := systemd.UnitName(systemd.CanaryInstance(id))
	require.Eventually(t, func() bool { return te.sd.count("start", canary) >= 1 }, testWait, 20*time.Millisecond)
	fb := te.waitEvent(state.EvFailback, "de-1")
	require.Equal(t, trBeta, fb.FromTransport)
	require.Equal(t, trAlpha, fb.ToTransport)
	require.Contains(t, fb.Reason, "canary")
	te.waitActive(id, "de-1", trAlpha)
	require.Eventually(t, func() bool { return te.sd.state(canary) == "inactive" }, testWait, 20*time.Millisecond)
	require.Equal(t, 1, n.startedCount(id+".canary"))
	require.True(t, dirExists(te.root, canaryDirOf(id)))
	n.tmu.Lock()
	echoes := len(n.echoes)
	n.tmu.Unlock()
	require.Zero(t, echoes, "the canary echo is stopped")

	// Deleting the tunnel removes the canary as well.
	require.NoError(t, te.client.TunnelDelete(ctx, id, nil))
	require.False(t, dirExists(te.root, canaryDirOf(id)))
	require.Contains(t, n.removedList(), id+".canary")
}

// canaryDirOf is the canary directory of the tfa backend.
func canaryDirOf(id string) string {
	return filepath.Join(config.BackendsConfDir, "tfa", id, "canary")
}

func TestNodeRemoveMovesTheTunnel(t *testing.T) {
	te := startTunnelHub(t)
	de := te.tunnelNode("de-1")
	te.tunnelNode("nl-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Backups: []string{"nl-1"}, Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)})
	id := info.ID
	require.Equal(t, []string{"de-1", "nl-1"}, info.Nodes)
	require.NoError(t, te.client.NodeRemove(ctxT(t), "de-1"))
	te.waitActive(id, "nl-1", trAlpha)
	require.Contains(t, de.removedList(), id+".de-1.tfa-alpha")
	require.False(t, dirExists(te.root, filepath.Join(config.BackendsConfDir, "tfa", id, "de-1")))
	tn, _ := te.h.Config().Tunnel(id)
	require.Equal(t, []string{"nl-1"}, tn.Nodes)
}

func TestNodeReconnectSyncsAndRestartsItsSide(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	inst := id + ".de-1.tfa-alpha"
	// The node "reboots": its units stop and its files are gone.
	n.stop()
	n.mu.Lock()
	for u := range n.units {
		n.units[u] = "inactive"
	}
	n.mu.Unlock()
	n.tmu.Lock()
	n.rendered = map[string]api.BackendRenderArgs{}
	n.tmu.Unlock()
	n.start()
	te.waitOnline("de-1", true)
	require.Eventually(t, func() bool { return len(n.renderedFor(id)) == 2 && n.startedCount(inst) == 2 }, testWait, 20*time.Millisecond)
	te.waitActive(id, "de-1", trAlpha)
}

func TestDeleteWhileNodeOfflineCleansOnReconnect(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	n.stop()
	te.waitOnline("de-1", false)

	var log stepLog
	require.NoError(t, te.client.TunnelDelete(ctxT(t), id, log.add))
	require.Contains(t, log.finished(), "remove:"+api.StepWarn)
	_, ok := te.h.Config().Tunnel(id)
	require.False(t, ok)
	require.Empty(t, n.removedList())
	require.Len(t, n.renderedFor(id), 2)

	// The node comes back: its first heartbeat lists the leftovers, which
	// the hub removes.
	n.start()
	te.waitOnline("de-1", true)
	require.Eventually(t, func() bool { return len(n.renderedFor(id)) == 0 }, testWait, 20*time.Millisecond)
	require.Contains(t, n.removedList(), id+".de-1.tfa-alpha")
	require.Contains(t, n.removedList(), id+".de-1.tfa-beta")
	require.Eventually(t, func() bool {
		ns, _ := te.h.nodeState("de-1")
		for u := range ns.Units {
			if strings.Contains(u, "@"+id+".") {
				return false
			}
		}
		return true
	}, testWait, 20*time.Millisecond)
}

func TestStoppedCandidateOutsideConfigIsRemoved(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	inst := id + ".de-1.tfa-alpha"
	// config.yaml drops the active rung while it runs (the controller has
	// not re-planned yet); once the engine moves away it goes.
	_, err := te.h.mutate(func(c *config.Config) error {
		tn, _ := c.Tunnel(id)
		tn.Ladder = config.LadderRef{Inline: []string{trBeta}}
		return nil
	})
	require.NoError(t, err)
	require.False(t, desiredInstances(te.h.Config())[inst])
	require.True(t, te.h.keepInstances(te.h.Config())[inst], "the active candidate is kept")
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trBeta}))
	te.waitActive(id, "de-1", trBeta)
	require.Eventually(t, func() bool { return slices.Contains(n.removedList(), inst) }, testWait, 20*time.Millisecond)
	require.False(t, dirExists(te.root, render.ConfigDir("tfa", id, "de-1", "alpha")))
	require.True(t, dirExists(te.root, render.ConfigDir("tfa", id, "de-1", "beta")))
	require.NotContains(t, n.removedList(), id+".de-1.tfa-beta")
}

func TestLaddersAndTransports(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	ctx := ctxT(t)

	list, err := te.client.LadderList(ctx)
	require.NoError(t, err)
	names := []string{}
	for _, l := range list {
		names = append(names, l.Name)
		if l.Name == config.DefaultLadderName {
			require.True(t, l.Builtin)
			require.Equal(t, config.DefaultLadder, l.Rungs)
		}
	}
	require.Equal(t, []string{"default", "udp-default"}, names)

	require.NoError(t, te.client.LadderSave(ctx, "fast", []string{trAlpha, " ", trBeta}, true))
	require.Equal(t, deyerr.C002, codeOf(te.client.LadderSave(ctx, "fast", []string{trAlpha}, true)))
	require.Equal(t, deyerr.C012, codeOf(te.client.LadderSave(ctx, "other", []string{trAlpha}, false)))
	require.Equal(t, deyerr.C022, codeOf(te.client.LadderSave(ctx, "default", []string{trAlpha}, false)))
	require.Equal(t, deyerr.C007, codeOf(te.client.LadderSave(ctx, "Bad Name", []string{trAlpha}, true)))
	require.Equal(t, deyerr.C013, codeOf(te.client.LadderSave(ctx, "empty", nil, true)))
	require.Equal(t, deyerr.C005, codeOf(te.client.LadderSave(ctx, "unknown", []string{"no/pe"}, true)))

	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}}, Ladder: "fast", Failover: fastFailover(false)})
	require.Equal(t, "fast", info.LadderName)
	require.Equal(t, []string{trAlpha, trBeta}, info.Ladder)
	list, err = te.client.LadderList(ctx)
	require.NoError(t, err)
	for _, l := range list {
		if l.Name == "fast" {
			require.Equal(t, []string{info.ID}, l.UsedBy)
			require.False(t, l.Builtin)
		}
	}

	// Changing the profile re-plans its tunnels: rung 1 changes, so the
	// engine follows (the active rung is gone).
	require.NoError(t, te.client.LadderSave(ctx, "fast", []string{trBeta}, false))
	te.waitActive(info.ID, "de-1", trBeta)
	require.Equal(t, deyerr.C023, codeOf(te.client.LadderDelete(ctx, "fast")))
	require.Equal(t, deyerr.C022, codeOf(te.client.LadderDelete(ctx, "udp-default")))
	require.Equal(t, deyerr.C012, codeOf(te.client.LadderDelete(ctx, "missing")))
	def := config.DefaultLadderName
	_, err = te.client.TunnelEdit(ctx, info.ID, api.TunnelEditRequest{Ladder: &def}, nil)
	require.NoError(t, err)
	require.NoError(t, te.client.LadderDelete(ctx, "fast"))
	_, ok := te.h.Config().Ladders["fast"]
	require.False(t, ok)

	trs, err := te.client.TransportList(ctx)
	require.NoError(t, err)
	byID := map[string]api.TransportInfo{}
	for _, tr := range trs {
		byID[tr.ID] = tr
	}
	require.Equal(t, "reverse", byID["backhaul/wssmux"].Direction)
	require.NotEmpty(t, byID["backhaul/wssmux"].Version)
	require.Equal(t, "forward", byID["direct/native"].Direction)
	require.True(t, byID["direct/haproxy"].ClientIPPreserved)
	require.True(t, byID["hysteria2/udp"].NeedsUDP)
	require.True(t, slices.IsSortedFunc(trs, func(a, b api.TransportInfo) int { return strings.Compare(a.ID, b.ID) }))
}

func TestPortCheckSuggestAndDiagProbe(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	ctx := ctxT(t)

	// A free port that belongs to no tunnel.
	free := freePort(t)
	res, err := te.client.PortCheck(ctx, api.PortCheckRequest{Port: free, Proto: "tcp", Node: "de-1"})
	require.NoError(t, err)
	require.True(t, res.BindFree)
	require.True(t, res.FirewallOpen)
	require.NotEmpty(t, res.FirewallName)
	require.NotNil(t, res.NodeReachable)
	require.Nil(t, res.TunnelOK)
	require.Empty(t, res.Tunnel)
	require.Empty(t, res.SuggestedPorts)

	// A busy port: the process and free suggestions.
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port
	res, err = te.client.PortCheck(ctx, api.PortCheckRequest{Port: busy})
	require.NoError(t, err)
	require.False(t, res.BindFree)
	require.Contains(t, res.BindProcess, "pid "+strconv.Itoa(os.Getpid()))
	require.False(t, res.BindByDey)
	require.NotEmpty(t, res.SuggestedPorts)
	for _, p := range res.SuggestedPorts {
		r, _ := config.ReservedListen(p, config.DefaultControlPort, nil)
		require.False(t, r)
	}

	// A firewall that blocks: ufw with a default deny.
	te.runner.On("ufw status verbose", exec.OK("Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"))
	res, err = te.client.PortCheck(ctx, api.PortCheckRequest{Port: free})
	require.NoError(t, err)
	require.False(t, res.FirewallOpen)
	require.Equal(t, "ufw", res.FirewallName)
	require.Contains(t, res.FirewallCommand, "ufw allow "+strconv.Itoa(free)+"/tcp")
	require.NotEmpty(t, res.SuggestedPorts)
	te.runner.On("ufw status verbose", exec.OK("Status: inactive\n"))

	_, err = te.client.PortCheck(ctx, api.PortCheckRequest{Port: 0})
	require.Equal(t, deyerr.P010, codeOf(err))
	_, err = te.client.PortCheck(ctx, api.PortCheckRequest{Port: 443, Proto: "icmp"})
	require.Equal(t, deyerr.P010, codeOf(err))
	res, err = te.client.PortCheck(ctx, api.PortCheckRequest{Port: 22})
	require.NoError(t, err)
	require.Contains(t, res.Note, "SSH")

	ports, err := te.client.PortSuggest(ctx, 0)
	require.NoError(t, err)
	require.Len(t, ports, DefaultSuggestCount)
	ports, err = te.client.PortSuggest(ctx, 5)
	require.NoError(t, err)
	require.Len(t, ports, 5)

	// Diag probe of a tunnel with a TCP and a UDP port.
	p1 := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p1}, {Listen: p1, Proto: "udp"}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)})
	reps, err := te.client.DiagProbe(ctx, info.ID, false)
	require.NoError(t, err)
	require.Len(t, reps, 1)
	require.True(t, reps[0].OK)
	require.Equal(t, "auto", reps[0].Kind)
	// The UDP map reports the node's unit state from its heartbeat, which
	// can still predate the start of the unit for one interval.
	require.Eventually(t, func() bool {
		reps, err = te.client.DiagProbe(ctx, info.ID, true)
		return err == nil && len(reps) == 2 && reps[1].OK
	}, testWait, 20*time.Millisecond, "the udp unit probe did not pass")
	require.Equal(t, "udp", reps[1].Proto)
	require.Equal(t, "unit", reps[1].Kind)
	_, err = te.client.DiagProbe(ctx, "nope", true)
	require.Equal(t, deyerr.C021, codeOf(err))
}

func TestAllPortsReport(t *testing.T) {
	te := startTunnelHub(t, func(o *Options, _ string) { o.ReportInterval = 100 * time.Millisecond })
	te.tunnelNode("de-1")
	p1, p2 := freePort(t), freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p1}, {Listen: p2}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)})
	require.Eventually(t, func() bool {
		ps, err := te.h.st.Probes(info.ID, portsProbeNode, strconv.Itoa(p2)+"/tcp")
		return err == nil && len(ps) > 0 && ps[len(ps)-1].OK && ps[len(ps)-1].Kind == "port:"+strconv.Itoa(p2)
	}, testWait, 50*time.Millisecond)
}

func TestTunnelAddWithoutOnlineNode(t *testing.T) {
	clk := failover.NewFakeClock(time.Now())
	te := startTunnelHub(t, func(o *Options, _ string) { o.FailoverClock = clk })
	n := te.tunnelNode("de-1")
	n.stop()
	te.waitOnline("de-1", false)
	var log stepLog
	port := freePort(t)
	_, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)}, log.add)
	require.Equal(t, deyerr.N003, codeOf(err))
	require.Equal(t, []string{"install_hub:ok", "install_node:failed"}, log.finished())
	// The tunnel stays configured: its first start fails (node offline),
	// it is DOWN and retries on the section 9 schedule.
	tn, ok := te.h.Config().Tunnel("tunnel")
	require.True(t, ok)
	require.Eventually(t, func() bool {
		ts, _ := te.h.tunnelState(tn.ID)
		return ts.State == state.StateDown
	}, testWait, 20*time.Millisecond)
	st, err := te.client.Status(ctxT(t))
	require.NoError(t, err)
	require.NotEmpty(t, st.Tunnels[0].Warnings)
	// The node connects: it is rendered at once; the next DOWN retry
	// brings the tunnel up.
	n.start()
	te.waitOnline("de-1", true)
	require.Eventually(t, func() bool { return len(n.renderedFor(tn.ID)) == 1 }, testWait, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		st, err := te.client.Status(ctxT(t))
		return err == nil && len(st.Tunnels[0].Warnings) == 0
	}, testWait, 20*time.Millisecond)
	clk.Advance(failover.DownRetryInitial + time.Second)
	te.waitActive(tn.ID, "de-1", trAlpha)
}

func TestConfigApplyReconcilesTunnels(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)})
	id := info.ID
	// The owner deletes the tunnel from config.yaml by hand and applies.
	path := filepath.Join(te.root, config.DefaultPath)
	c, err := config.LoadWith(path, testValidate)
	require.NoError(t, err)
	require.True(t, c.RemoveTunnel(id))
	require.NoError(t, config.SaveWith(path, c, testValidate))
	_, err = te.client.ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	require.Nil(t, te.h.tun.lookup(id))
	require.Empty(t, te.sd.active())
	_, ok, err := te.h.st.GetTunnel(id)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoFileExists(t, te.h.secrets.TokenPath(id))
	require.False(t, dirExists(te.root, filepath.Join(config.BackendsConfDir, "tfa", id)))
	// Stop all halts the engines.
	require.NoError(t, te.client.StopAll(ctxT(t)))
}

func TestTunnelAddFailures(t *testing.T) {
	clk := failover.NewFakeClock(time.Now())
	te := startTunnelHub(t, func(o *Options, _ string) { o.FailoverClock = clk })
	te.tunnelNode("de-1")
	logFile := filepath.Join(te.root, systemd.TunnelLogFile("crashy"))
	require.NoError(t, os.MkdirAll(filepath.Dir(logFile), 0o750))
	require.NoError(t, os.WriteFile(logFile, []byte("backend: bind failed token=supersecret1234\n"), 0o600))

	// The only rung fails to start: DEY-B003 with the tunnel log lines; the
	// tunnel stays configured (DOWN).
	te.sd.mu.Lock()
	te.sd.crash[trAlpha] = true
	te.sd.mu.Unlock()
	var log stepLog
	_, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{ID: "crashy", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)}, log.add)
	require.Equal(t, deyerr.B003, codeOf(err))
	e := deyerr.As(err)
	require.Contains(t, e.Detail, "backend: bind failed")
	require.Contains(t, e.Detail, "token=***")
	require.Equal(t, systemd.TunnelLogFile("crashy"), e.Log())
	require.Equal(t, []string{"install_hub:ok", "install_node:ok", "render:ok", "firewall:ok", "start:warn", "probe:failed"}, log.finished())
	_, ok := te.h.Config().Tunnel("crashy")
	require.True(t, ok)

	// A rung that starts but never answers: DEY-B004 after 15 s (engine
	// time) with the log tail.
	te.sd.mu.Lock()
	te.sd.broken[trBeta] = true
	te.sd.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{ID: "silent", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
			Rungs: []string{trBeta}, Failover: fastFailover(false)}, nil)
		done <- err
	}()
	var addErr error
	require.Eventually(t, func() bool {
		select {
		case addErr = <-done:
			return true
		default:
			clk.Advance(time.Second)
			return false
		}
	}, testWait, 5*time.Millisecond)
	require.Equal(t, deyerr.B004, codeOf(addErr))
	require.Contains(t, deyerr.As(addErr).Message(), trBeta)
	ev := te.waitEvent(state.EvProbeError, "de-1")
	require.Equal(t, string(deyerr.B004), ev.Code)
}

func TestInstallsAndNATCandidate(t *testing.T) {
	te := startTunnelHub(t, func(o *Options, root string) {
		o.Fetcher = rawFetcher{}
		p := filepath.Join(root, "proc/sys/net/ipv4")
		if err := os.MkdirAll(p, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(p, "ip_forward"), []byte("0\n"), 0o600)
		}
	})
	de := te.tunnelNode("de-1")
	nl := te.tunnelNode("nl-1")
	var installs []string
	de.on(api.CmdBackendInstall, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.BackendInstallArgs
		decode(t, cmd, &args)
		installs = append(installs, args.Name+"@"+args.Entry.Version)
		return api.BackendInstallResult{}, nil
	})
	nl.on(api.CmdBackendInstall, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return nil, deyerr.New(deyerr.N051, deyerr.Params{"node": "nl-1", "file": "tfdn"})
	})
	port := freePort(t)
	var log stepLog
	info, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{Node: "de-1", Backups: []string{"nl-1"},
		Ports: []api.PortSpec{{Listen: port}}, Rungs: []string{trDN, trNAT, trAlpha}, Failover: fastFailover(false)}, log.add)
	require.NoError(t, err, "%v", log.finished())
	id := info.ID
	require.Equal(t, trDN, info.ActiveTransport)
	// The hub installed the pinned binary (via the direct fetcher after the
	// nodes could not fetch it); de-1 installed it; nl-1 failed, so only
	// its tfdn rung is skipped.
	bin := filepath.Join(te.root, config.BinDir, "tfdn", "1.0", "tfdn")
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, tfdnBinary, data)
	require.Equal(t, []string{"tfdn@1.0"}, installs)
	require.Contains(t, log.finished(), "install_node:warn")
	ts, _ := te.h.tunnelState(id)
	require.Equal(t, map[string]string{"nl-1/" + trDN: string(deyerr.B001)}, skipCodes(ts))
	require.Contains(t, ts.Skipped["nl-1/"+trDN].Reason, "node nl-1")

	// A NAT candidate: its rules enter the firewall only while it runs, and
	// IP forwarding is switched on.
	require.Empty(t, te.h.Firewall().Spec.NAT)
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trNAT}))
	te.waitActive(id, "de-1", trNAT)
	spec := te.h.Firewall().Spec
	require.Len(t, spec.NAT, 1)
	require.Equal(t, port, spec.NAT[0].DportLow)
	require.Equal(t, []string{"dey-" + id}, spec.Masquerade)
	fwd, err := os.ReadFile(filepath.Join(te.root, "proc/sys/net/ipv4/ip_forward"))
	require.NoError(t, err)
	require.Equal(t, "1\n", string(fwd))
	// Forward: the node (server) side starts before the hub side.
	order := te.order()
	iNode := slices.Index(order, "node start "+id+".de-1.tfa-nat")
	iHub := slices.Index(order, "hub start "+id+".de-1.tfa-nat")
	require.True(t, iNode >= 0 && iHub > iNode, "%v", order)
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trAlpha}))
	te.waitActive(id, "de-1", trAlpha)
	require.Empty(t, te.h.Firewall().Spec.NAT)
	// Stopping: client side (hub, forward) before the server side (node).
	order = te.order()
	iHubStop := slices.Index(order, "hub stop "+id+".de-1.tfa-nat")
	iNodeStop := slices.Index(order, "node stop "+id+".de-1.tfa-nat")
	require.True(t, iNodeStop > iHubStop || iHubStop < 0, "%v", order)
}

// skipCodes maps skipped candidates to their codes.
func skipCodes(ts state.TunnelState) map[string]string {
	out := map[string]string{}
	for k, s := range ts.Skipped {
		out[k] = s.Code
	}
	return out
}

func TestCanaryFollowsRungOneAndStrayCanaryStops(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(true)})
	id := info.ID
	canary := systemd.UnitName(systemd.CanaryInstance(id))
	// Away from rung 1 the canary is built for tfa/alpha.
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trBeta}))
	require.Eventually(t, func() bool { return te.sd.state(canary) == "active" }, testWait, 20*time.Millisecond)
	// Rung 1 changes: the canary goes (units and files), a new one follows
	// the new rung 1 and the tunnel fails back to it.
	_, err := te.client.TunnelEdit(ctxT(t), id, api.TunnelEditRequest{Rungs: []string{trBeta, trAlpha}}, nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return slices.Contains(n.removedList(), id+".canary") }, testWait, 20*time.Millisecond)
	te.waitActive(id, "de-1", trBeta)

	// A canary left running by an earlier hub process is stopped at start.
	te.sd.setUnit(canary, "active")
	te.stop()
	o := te.o
	o.OnReady = nil
	env2 := &testEnv{t: t, root: te.root, sock: te.sock, runner: te.runner, ca: te.ca}
	te.sd.env = env2
	env2.startEnv(o)
	require.Eventually(t, func() bool { return te.sd.state(canary) == "inactive" }, testWait, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		ts, _ := env2.h.tunnelState(id)
		return ts.State == state.StateUp && ts.Active.Transport == trBeta
	}, testWait, 20*time.Millisecond)
}
