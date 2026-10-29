package hub

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"sync"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// Echo probe (DR-17). A raw-TCP service that waits for its client (VMess
// or Shadowsocks-2022 without TLS) never answers the TLS hello of the auto
// probe, so every rung fails it and the tunnel ends DOWN. The echo probe
// proves the two halves separately:
//
//   - transport: every rung of the node carries one hidden port map, a hub
//     listen port in the backend control range (render.ProbeListenKey;
//     the firewall admits that range from the nodes and loopback only)
//     forwarded to the node's probe echo on 127.0.0.1 (render.ProbeEchoKey,
//     echo.start with Probe). The hub sends a random nonce through it and
//     needs it back (health.TCPEcho);
//   - service: the node's TCP connect to the target (node_service).
//
// It is used for a port map with probe: echo, and for probe: auto when the
// node's own TLS hello to the target times out or is reset while a TCP
// connect succeeds (the "silent" verdict). Only nodes that announce
// api.FeatureEchoProbe get it; any other case keeps today's probe and is
// reported as a warning and in tunnel show (probe_effective, probe_reason).

// metaProbeVerdict + tunnel keeps, per node, the silent verdict and
// whether the probe echo ran, so a hub restart renders the same files
// before the nodes reconnect (they reopen their probe echo themselves)
// instead of dropping the hidden map and restarting the active rung when
// it comes back.
const metaProbeVerdict = "probe-verdict/"

// reasonServiceDown is the path probe error of an echo probe whose
// transport passed while the node reports the service down.
const reasonServiceDown = "service down on the node"

// probeVerdict is what the node's probes of the target showed.
type probeVerdict struct {
	Silent bool   `json:"silent"`
	Why    string `json:"why,omitempty"` // the TLS probe error (timeout, connection reset)
}

// echoRecord is the per-node record of metaProbeVerdict.
type echoRecord struct {
	probeVerdict
	Measured bool `json:"measured,omitempty"` // the verdict was measured
	Echo     bool `json:"echo,omitempty"`     // the probe echo ran (echoNode.ready)
}

// echoNode is the echo probe state of one node of the tunnel.
type echoNode struct {
	want  bool   // the node's probe should be the echo probe
	ready bool   // its probe echo runs: the plan carries the hidden map
	why   string // why it is wanted but not ready
}

// silentTLS reports a TLS probe error of a silent service.
func silentTLS(res api.ProbeResultDTO) bool {
	return !res.OK && (res.Error == health.ReasonTimeout || res.Error == health.ReasonReset)
}

// echoKind reports whether the probe port of t may use the echo probe:
// a TCP probe port whose kind is echo or auto.
func echoKind(t config.Tunnel) (kind string, ok bool) {
	pm, tcp := t.ProbeTarget()
	kind = probeKind(pm)
	return kind, tcp && (kind == config.ProbeEcho || kind == config.ProbeAuto)
}

// records reads the metaProbeVerdict records of the tunnel.
func (c *tunnelCtl) records() map[string]echoRecord {
	var all map[string]echoRecord
	if ok, err := c.h.st.GetMeta(metaProbeVerdict+c.id, &all); err != nil || !ok || all == nil {
		return map[string]echoRecord{}
	}
	return all
}

// keepRecord changes node's record with edit and stores it when it
// changed.
func (c *tunnelCtl) keepRecord(node string, edit func(r *echoRecord)) {
	c.recMu.Lock()
	defer c.recMu.Unlock()
	all := c.records()
	r := all[node]
	edit(&r)
	if old, ok := all[node]; ok && old == r {
		return
	}
	all[node] = r
	if err := c.h.st.PutMeta(metaProbeVerdict+c.id, all); err != nil {
		c.h.log.Warn("cannot keep the probe verdict", dlog.Tunnel(c.id), dlog.Node(node), dlog.Err(err))
	}
}

// forgetNode drops node's verdict and record (the node left the tunnel).
func (c *tunnelCtl) forgetNode(node string) {
	c.recMu.Lock()
	defer c.recMu.Unlock()
	all := c.records()
	if _, ok := all[node]; !ok {
		return
	}
	delete(all, node)
	if err := c.h.st.PutMeta(metaProbeVerdict+c.id, all); err != nil {
		c.h.log.Warn("cannot drop the probe verdict", dlog.Tunnel(c.id), dlog.Node(node), dlog.Err(err))
	}
}

// verdict returns the last silent verdict of node (memory first, then the
// one kept in state.db); known is false when none was measured.
func (c *tunnelCtl) verdict(node string) (v probeVerdict, known bool) {
	c.mu.Lock()
	e, ok := c.plain[node]
	c.mu.Unlock()
	if ok {
		return probeVerdict{Silent: e.silent, Why: e.why}, true
	}
	r, ok := c.records()[node]
	return r.probeVerdict, ok && r.Measured
}

// keepVerdict stores the silent verdict of node.
func (c *tunnelCtl) keepVerdict(node string, v probeVerdict) {
	c.keepRecord(node, func(r *echoRecord) { r.probeVerdict, r.Measured = v, true })
}

// echoStale reports that node's silent verdict no longer matches the echo
// state the current plan was made with (a re-plan is due).
func (c *tunnelCtl) echoStale(node string) bool {
	t, _ := c.snapshot()
	kind, ok := echoKind(t)
	if !ok || kind != config.ProbeAuto {
		return false
	}
	v, known := c.verdict(node)
	c.mu.Lock()
	defer c.mu.Unlock()
	return known && v.Silent != c.echo[node].want
}

// ensureEcho runs before every plan: it decides per node whether the
// probe is the echo probe, starts the probe echo on the nodes that get it
// (idempotent on the node) and stops it where it is no longer wanted.
// Nodes other than only (when set) keep their state. A node without a
// silent verdict yet is asked now (node_service and the TLS probe of the
// target, in parallel).
func (c *tunnelCtl) ensureEcho(ctx context.Context, t config.Tunnel, only string) {
	h := c.h
	kind, usable := echoKind(t)
	var nodes []string
	for _, n := range t.Nodes {
		if only == "" || n == only {
			nodes = append(nodes, n)
		}
	}
	if usable && kind == config.ProbeAuto {
		var wg sync.WaitGroup
		for _, n := range nodes {
			if _, known := c.verdict(n); known || !h.Online(n) {
				continue
			}
			wg.Go(func() {
				sctx, cancel := context.WithTimeout(ctx, probeTimeout(t)+nodeCmdTimeout)
				defer cancel()
				c.checkService(sctx, n)
			})
		}
		wg.Wait()
	}
	for _, n := range nodes {
		want := usable && kind == config.ProbeEcho
		if usable && kind == config.ProbeAuto {
			v, _ := c.verdict(n)
			want = v.Silent
		}
		c.setEcho(ctx, t, n, want)
	}
}

// setEcho brings node's probe echo to want.
func (c *tunnelCtl) setEcho(ctx context.Context, t config.Tunnel, node string, want bool) {
	h := c.h
	c.mu.Lock()
	old := c.echo[node]
	c.mu.Unlock()
	next := echoNode{want: want}
	hello, online := h.nodeHello(node)
	switch {
	case !online:
		// The node reopens its probe echo itself when it restarts; after
		// a hub restart the record says whether it ran.
		ran := old.ready
		if _, seen := c.echoSeen(node); !seen {
			ran = c.records()[node].Echo
		}
		next.ready, next.why = ran && want, old.why
	case !want:
		if old.ready {
			c.stopEcho(ctx, node)
		}
	case !hello.HasFeature(api.FeatureEchoProbe):
		next.why = "node " + node + " runs a deyroute without " + api.FeatureEchoProbe + ": it gets it when it follows the hub's version (deyroute update)"
	default:
		err := c.startEcho(ctx, node)
		switch {
		case err == nil:
			next.ready = true
		case deyerr.HasCode(err, deyerr.N003) || deyerr.HasCode(err, deyerr.N004) || ctx.Err() != nil:
			next.ready, next.why = old.ready, old.why // transient: keep what runs
		default:
			next.why = "the probe echo cannot run on node " + node + ": " + deyerr.As(err).Message()
		}
	}
	c.mu.Lock()
	c.echo[node] = next
	c.mu.Unlock()
	if online {
		c.keepRecord(node, func(r *echoRecord) { r.Echo = next.ready })
	}
	if next.want && !next.ready && next.why != "" && next.why != old.why {
		h.log.Warn("echo probe not available; the tunnel keeps the auto probe", dlog.Tunnel(t.ID), dlog.Node(node),
			slog.String("reason", next.why))
	}
	if next.ready && !old.ready {
		h.log.Info("echo probe in use", dlog.Tunnel(t.ID), dlog.Node(node))
	}
}

// echoSeen returns node's echo state of this hub process (seen false
// before the first plan).
func (c *tunnelCtl) echoSeen(node string) (echoNode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.echo[node]
	return e, ok
}

// echoPorts allocates (or returns) the hub listen port of the hidden map
// and the node's probe echo port.
func (c *tunnelCtl) echoPorts() (listen, echo int, err error) {
	if listen, err = c.h.allocCtlPort(render.ProbeListenKey(c.id)); err != nil {
		return 0, 0, err
	}
	if echo, err = c.h.allocCtlPort(render.ProbeEchoKey(c.id)); err != nil {
		return 0, 0, err
	}
	return listen, echo, nil
}

// startEcho starts the probe echo on node (echo.start with Probe).
func (c *tunnelCtl) startEcho(ctx context.Context, node string) error {
	_, echo, err := c.echoPorts()
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	defer cancel()
	var er api.EchoResult
	return c.h.Call(cctx, node, api.CmdEchoStart, api.EchoArgs{Port: echo, Probe: true}, &er)
}

// stopEcho stops the probe echo on node; an offline node stops it when it
// reconnects.
func (c *tunnelCtl) stopEcho(ctx context.Context, node string) {
	c.h.stopProbeEcho(ctx, c.id, node)
	c.mu.Lock()
	delete(c.echo, node)
	c.mu.Unlock()
}

// stopProbeEcho stops the probe echo of tunnel on node, if the tunnel has
// one; an offline node stops it when it reconnects (echo orphans).
func (h *Hub) stopProbeEcho(ctx context.Context, tunnel, node string) {
	ports, err := h.st.CtlPorts()
	echo := ports[render.ProbeEchoKey(tunnel)]
	if err != nil || echo == 0 {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	defer cancel()
	if err := h.Call(cctx, node, api.CmdEchoStop, api.EchoArgs{Port: echo, Probe: true}, nil); deyerr.HasCode(err, deyerr.N003) {
		h.addEchoOrphan(node, echo)
	}
}

// probeEchoMap is render.Input.ProbeEcho: the hidden port map of nodes
// whose probe echo runs.
func (c *tunnelCtl) probeEchoMap() func(node string) (config.PortMap, bool) {
	c.mu.Lock()
	ready := map[string]bool{}
	for n, e := range c.echo {
		if e.want && e.ready {
			ready[n] = true
		}
	}
	c.mu.Unlock()
	if len(ready) == 0 {
		return nil
	}
	listen, echo, err := c.echoPorts()
	if err != nil {
		c.h.log.Warn("cannot allocate the echo probe ports; the tunnel keeps the auto probe", dlog.Tunnel(c.id), dlog.Err(err))
		return nil
	}
	pm := config.PortMap{Listen: listen, Proto: config.ProtoTCP, Target: backend.HostPort("127.0.0.1", echo), Probe: config.ProbeTCP}
	return func(node string) (config.PortMap, bool) { return pm, ready[node] }
}

// echoActive reports whether the probe of candidate pc is the echo probe.
func (c *tunnelCtl) echoActive(pc *render.Candidate) bool {
	if pc == nil || pc.ProbeEcho == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.echo[pc.Node]
	return e.want && e.ready
}

// echoProbe is the path probe of an echo-probed candidate: the nonce
// through the hidden map, then the node's view of the service (cached for
// one probe interval; unknown while the node is offline, then the
// transport alone decides).
func (c *tunnelCtl) echoProbe(ctx context.Context, t config.Tunnel, plan render.TunnelPlan, sc state.Candidate, listen int) health.Result {
	addr := net.JoinHostPort(c.probeHost(plan, sc), strconv.Itoa(listen))
	r := health.TCPEcho(ctx, addr, remaining(ctx, probeTimeout(t)))
	if !r.OK {
		return r
	}
	if up, known := c.nodeService(ctx, sc.Node); known && !up {
		return health.Result{Err: reasonServiceDown}
	}
	return r
}

// probeEffective is the probe kind the hub uses for the tunnel now on
// node (or on candidate pc, when it runs) and why it differs from the
// configured kind ("" when it does not).
func (c *tunnelCtl) probeEffective(t config.Tunnel, node string, pc *render.Candidate) (kind, why string) {
	pm, tcp := t.ProbeTarget()
	configured := probeKind(pm)
	if !tcp {
		return configured, ""
	}
	c.mu.Lock()
	e := c.echo[node]
	c.mu.Unlock()
	v, _ := c.verdict(node)
	silent := "the target on node " + node + " stays silent after a TLS hello (" + v.Why + ") while a TCP connect succeeds"
	unavailable := func(reason string) (string, string) {
		if configured == config.ProbeEcho {
			return config.ProbeAuto, "the echo probe is not available: " + reason
		}
		return config.ProbeAuto, silent + "; the echo probe is not available: " + reason
	}
	switch {
	case configured == config.ProbeAuto && !e.want:
		return configured, ""
	case configured != config.ProbeAuto && configured != config.ProbeEcho:
		return configured, ""
	case !e.ready:
		return unavailable(firstNonEmpty(e.why, "node "+node+" has not been checked yet"))
	case pc != nil && pc.ProbeEcho == 0:
		return unavailable(pc.TransportID + " cannot carry the echo port: " + pc.ProbeEchoErr)
	case configured == config.ProbeEcho:
		return config.ProbeEcho, ""
	}
	return config.ProbeEcho, silent + ": the hub probes the tunnel end to end through the echo port"
}

// echoWarnings are the dashboard lines of nodes that should use the echo
// probe but cannot.
func (c *tunnelCtl) echoWarnings() []string {
	t, _ := c.snapshot()
	var out []string
	for _, n := range t.Nodes {
		c.mu.Lock()
		e := c.echo[n]
		c.mu.Unlock()
		if !e.want || e.ready || e.why == "" {
			continue
		}
		if _, why := c.probeEffective(t, n, nil); why != "" {
			out = append(out, "node "+n+": "+why)
		}
	}
	return out
}

// fillProbeEffective sets probe_effective and probe_reason of the probe
// port in tunnel show: for the active candidate while one runs, else for
// the primary node.
func (c *tunnelCtl) fillProbeEffective(t config.Tunnel, d *api.TunnelDetail, ts state.TunnelState, plan render.TunnelPlan) {
	pm, ok := t.ProbeTarget()
	if !ok || len(t.Nodes) == 0 {
		return
	}
	node := t.Nodes[0]
	var pc *render.Candidate
	if !ts.Active.IsZero() && runningState(ts.State) {
		node = ts.Active.Node
		pc, _ = plan.Candidate(ts.Active.Node, ts.Active.Transport)
	}
	kind, why := c.probeEffective(t, node, pc)
	for i := range d.Ports {
		if p := &d.Ports[i]; p.Listen == pm.Listen && p.Proto == pm.Proto {
			p.ProbeEffective, p.ProbeReason = kind, why
		}
	}
}

// refreshEcho runs with the 60-second report: while the tunnel is not
// passing its auto probe, each online node whose verdict is older than
// plainCheckTTL is asked again, so a target that became silent (or one
// that was down when the tunnel was planned) gets the echo probe without
// waiting for the 30-minute re-check. A passing tunnel has nothing to
// learn; an echo-probed one refreshes through its own node_service checks.
func (c *tunnelCtl) refreshEcho(ctx context.Context) {
	t, _ := c.snapshot()
	if kind, ok := echoKind(t); !ok || kind != config.ProbeAuto {
		return
	}
	eng := c.eng()
	if eng == nil {
		return
	}
	if st := eng.State(); (st.State == state.StateUp || st.State == state.StatePaused) && st.LastProbeErr == "" {
		return
	}
	for _, n := range t.Nodes {
		c.mu.Lock()
		e, ok := c.plain[n]
		c.mu.Unlock()
		if (ok && c.h.now().Sub(e.at) < plainCheckTTL) || !c.h.Online(n) {
			continue
		}
		c.nodeService(ctx, n)
	}
}
