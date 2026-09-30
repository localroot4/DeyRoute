package hub

import (
	"context"
	stderrors "errors"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// engineActions is the hub's failover.Actions (ARCHITECTURE.md §7.3 item
// 3). Every method runs on the engine goroutine of one tunnel.
type engineActions struct{ c *tunnelCtl }

var _ failover.Actions = engineActions{}

// Start implements failover.Actions: the server side of the backend first,
// then the client side (section 9: reverse = hub then node, forward = node
// then hub), each checked after the start (DEY-B003 with the last 40 log
// lines), then the candidate's NAT on the hub (the node agent applies the
// node's NAT when its unit starts).
func (a engineActions) Start(ctx context.Context, sc state.Candidate) error {
	err := a.c.startCandidate(ctx, sc)
	a.c.noteStart(err)
	return err
}

// Stop implements failover.Actions: client side first, then server side;
// idempotent. The candidate's NAT leaves the hub firewall.
func (a engineActions) Stop(ctx context.Context, sc state.Candidate) error {
	return a.c.stopCandidate(ctx, sc)
}

// ProbePath implements failover.Actions (path probe of section 9).
func (a engineActions) ProbePath(ctx context.Context, sc state.Candidate) failover.Probe {
	return a.c.probePath(ctx, sc)
}

// ProbeCanary implements failover.Actions (canary of section 9, phase 8).
func (a engineActions) ProbeCanary(ctx context.Context) (failover.Probe, bool) {
	return a.c.probeCanary(ctx)
}

// NodeService implements failover.Actions (node_service probe).
func (a engineActions) NodeService(ctx context.Context, node string) (bool, bool) {
	return a.c.nodeService(ctx, node)
}

// ControlOnline implements failover.Actions (control probe: the node
// registry).
func (a engineActions) ControlOnline(node string) (bool, time.Duration) {
	return a.c.h.ControlOnline(node)
}

// Emit implements failover.Actions (event bus).
func (a engineActions) Emit(e state.Event) {
	a.c.noteEvent(e)
	a.c.h.Emit(e)
}

// Persist implements failover.Actions (tunnels/<id> in state.db).
func (a engineActions) Persist(ts state.TunnelState) error {
	if err := a.c.h.st.PutTunnel(ts); err != nil {
		a.c.h.log.Warn("cannot save the tunnel state", dlog.Tunnel(ts.ID), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return err
	}
	return nil
}

// ---------------------------------------------------------------- start / stop

// noteStart records the outcome of a Start (TunnelAdd waits for the first).
func (c *tunnelCtl) noteStart(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = err
	}
	if !c.firstOK {
		c.firstOK = true
		c.first <- err
	}
}

// noteEvent keeps the last failure of an attempt (TunnelAdd reports it).
func (c *tunnelCtl) noteEvent(e state.Event) {
	if e.Type != state.EvProbeError || e.Code == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.Code == string(deyerr.B004) {
		c.lastErr = deyerr.New(deyerr.B004, deyerr.Params{
			"transport": e.ToTransport, "seconds": int(failover.StartWait / time.Second),
		}).WithDetail(e.Reason)
		return
	}
	if c.lastErr == nil || string(deyerr.As(c.lastErr).Code) != e.Code {
		c.lastErr = deyerr.New(deyerr.Code(e.Code), deyerr.Params{"transport": e.ToTransport, "tunnel": c.id}).WithDetail(e.Reason)
	}
}

// failure returns the last start or probe failure of the tunnel. A probe
// timeout (DEY-B004) carries the last 40 lines of the tunnel log (section
// 7: "the last 40 log lines of that backend").
func (c *tunnelCtl) failure() error {
	c.mu.Lock()
	err := c.lastErr
	c.mu.Unlock()
	if err == nil {
		return nil
	}
	e := deyerr.As(err)
	if e.Code != deyerr.B004 {
		return err
	}
	out := *e
	if tail, lerr := systemd.LogTail(c.h.path(systemd.TunnelLogFile(c.id)), systemd.FailureLogLines); lerr == nil && len(tail) > 0 {
		for i := range tail {
			tail[i] = dlog.Redact(tail[i])
		}
		out.Detail = strings.TrimSpace(out.Detail + "\n" + strings.Join(tail, "\n"))
		out.LogPath = systemd.TunnelLogFile(c.id)
	}
	return &out
}

// isHome reports whether sc is rung 1 of the primary node (the first rung
// that is not skipped).
func (c *tunnelCtl) isHome(sc state.Candidate) bool {
	t, plan := c.snapshot()
	if len(t.Nodes) == 0 || sc.Node != t.Nodes[0] {
		return false
	}
	skipped := c.engineState().Skipped
	for _, r := range plan.Ladder {
		if _, ok := skipped[state.Candidate{Node: sc.Node, Transport: r}.Key()]; ok {
			continue
		}
		return r == sc.Transport
	}
	return false
}

// startCandidate starts both sides of sc in the section 9 order.
func (c *tunnelCtl) startCandidate(ctx context.Context, sc state.Candidate) error {
	h := c.h
	pc, ok := c.candidate(sc)
	if !ok {
		b, tr, _ := strings.Cut(sc.Transport, "/")
		return deyerr.New(deyerr.B002, deyerr.Params{"backend": b, "transport": tr}).
			WithDetail("the rung is not rendered for node " + sc.Node + ": it is skipped or the tunnel could not be planned")
	}
	if c.isHome(sc) {
		// Failback (or a start on rung 1): the canary is not needed any more.
		c.requestCanary(false)
	}
	c.markStarted(sc)
	hubSide := func() error { return h.startHubSide(ctx, c.id, pc.Backend, pc.Hub) }
	nodeSide := func() error { return c.startNodeSide(ctx, &pc) }
	first, second := hubSide, nodeSide
	if pc.Transport.Direction.ServerSide() == backend.SideNode {
		first, second = nodeSide, hubSide
	}
	if err := first(); err != nil {
		return err
	}
	if err := second(); err != nil {
		return err
	}
	if len(pc.Hub.NAT) > 0 || len(pc.Hub.Masquerade) > 0 || pc.Hub.IPForward {
		if pc.Hub.IPForward {
			if err := h.ensureIPForward(); err != nil {
				return err
			}
		}
		if err := h.applyFirewall(ctx); err != nil {
			return err
		}
	}
	return nil
}

// startNodeSide starts the node side of pc (unit.start); the node answers
// DEY-B003 with the last 40 lines of the tunnel log when the unit fails.
// A node side that was never delivered is rendered first.
func (c *tunnelCtl) startNodeSide(ctx context.Context, pc *render.Candidate) error {
	c.mu.Lock()
	_, sent := c.sent[pc.NodeSide.Instance]
	c.mu.Unlock()
	if !sent {
		if err := c.renderOnNode(ctx, pc); err != nil {
			return err
		}
	}
	var us api.UnitStatus
	return c.h.Call(ctx, pc.Node, api.CmdUnitStart, api.UnitArgs{Instance: pc.NodeSide.Instance}, &us)
}

// stopCandidate stops both sides of sc (client side first) and removes its
// NAT from the hub firewall. Every side is tried; the first error is
// returned (an offline node is DEY-N003).
func (c *tunnelCtl) stopCandidate(ctx context.Context, sc state.Candidate) error {
	h := c.h
	inst := systemd.InstanceName(c.id, sc.Node, sc.Transport)
	in, err := systemd.ParseInstance(inst)
	if err != nil {
		c.markStopped(sc)
		return err
	}
	dir, _ := configDirOf(in)
	b, _, _ := strings.Cut(sc.Transport, "/")
	serverHub := true
	if _, tr, err := backend.Lookup(sc.Transport); err == nil {
		serverHub = tr.Direction.ServerSide() == backend.SideHub
	}
	var errs []error
	hubSide := func() { errs = append(errs, h.stopHubSide(ctx, inst, b, dir)) }
	nodeSide := func() {
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		errs = append(errs, h.Call(cctx, sc.Node, api.CmdUnitStop, api.UnitArgs{Instance: inst}, nil))
	}
	if serverHub {
		nodeSide()
		hubSide()
	} else {
		hubSide()
		nodeSide()
	}
	side, _ := c.markStopped(sc)
	if len(side.NAT) > 0 || len(side.Masquerade) > 0 {
		errs = append(errs, h.applyFirewall(ctx))
	}
	c.removeUnwanted(ctx, sc, in)
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// removeUnwanted removes a stopped candidate that config.yaml no longer
// uses: its rung or node was dropped while it was active, so the update
// that dropped it kept it running until the engine moved away.
func (c *tunnelCtl) removeUnwanted(ctx context.Context, sc state.Candidate, in systemd.Instance) {
	h := c.h
	cfg := h.Config()
	if _, ok := cfg.Tunnel(c.id); !ok || desiredInstances(cfg)[in.String()] {
		return
	}
	if err := h.removeHubInstance(ctx, in.String()); err != nil {
		h.log.Warn("cannot remove a stale tunnel unit", dlog.Tunnel(c.id), slog.String("instance", in.String()), dlog.Err(err))
	}
	if h.Online(sc.Node) {
		if err := h.removeNodeInstance(ctx, sc.Node, in); err != nil {
			h.log.Warn("cannot remove a stale tunnel unit on its node", dlog.Tunnel(c.id), dlog.Node(sc.Node), dlog.Err(err))
		}
	}
	c.mu.Lock()
	delete(c.sent, in.String())
	c.mu.Unlock()
	if err := h.st.ReleaseCtlPort(state.Key(c.id, sc.Node, sc.Transport)); err != nil {
		h.log.Warn("cannot release a control port", dlog.Tunnel(c.id), dlog.Err(err))
	}
}

// startHubSide starts one hub unit: the backend's pre-start step, systemctl
// start, the start check, the post-start step (awg device configuration).
func (h *Hub) startHubSide(ctx context.Context, tunnel, backendName string, side render.Side) error {
	unit := systemd.UnitName(side.Instance)
	if err := h.ensureTunnelLogDir(); err != nil {
		return err
	}
	if err := h.hubHook(ctx, hookPreStart, backendName, side.ConfigDir); err != nil {
		return err
	}
	if err := h.o.Systemd.Start(ctx, unit); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return h.unitFailed(unit, tunnel, err)
	}
	if err := h.watchStart(ctx, unit, tunnel); err != nil {
		return err
	}
	return h.hubHook(ctx, hookPostStart, backendName, side.ConfigDir)
}

// stopHubSide stops one hub unit (a unit systemd does not know is already
// stopped) and runs the backend's post-stop step.
func (h *Hub) stopHubSide(ctx context.Context, inst, backendName, configDir string) error {
	err := h.o.Systemd.Stop(ctx, systemd.UnitName(inst))
	if err != nil && isNotLoaded(err) {
		err = nil
	}
	if configDir != "" {
		if herr := h.hubHook(ctx, hookPostStop, backendName, configDir); herr != nil && err == nil {
			err = herr
		}
	}
	return err
}

// isNotLoaded reports "no such unit" from systemctl (exit status 5).
func isNotLoaded(err error) bool {
	var xe *exec.ExitError
	if !stderrors.As(err, &xe) {
		return false
	}
	return xe.Code == 5 || strings.Contains(xe.Stderr, "not loaded") || strings.Contains(xe.Stderr, "not found")
}

// watchStart checks a unit after systemctl start: after UnitStartCheck its
// state is read; failed, auto-restart (crash loop) and inactive are
// DEY-B003 with the last 40 lines of the tunnel log; an activating unit is
// watched a little longer.
func (h *Hub) watchStart(ctx context.Context, unit, tunnel string) error {
	if err := sleepCtx(ctx, h.o.UnitStartCheck); err != nil {
		return err
	}
	deadline := time.Now().Add(unitStartWatch)
	for {
		st, err := h.o.Systemd.Show(ctx, unit)
		if err != nil {
			return err
		}
		switch {
		case st.Failed() || st.SubState == "auto-restart" || st.ActiveState == "inactive":
			return h.unitFailed(unit, tunnel, nil)
		case st.ActiveState != "activating" || time.Now().After(deadline):
			return nil
		}
		if err := sleepCtx(ctx, unitStartPoll); err != nil {
			return err
		}
	}
}

// unitFailed is DEY-B003 for a hub unit with the last 40 (redacted) lines
// of the tunnel log.
func (h *Hub) unitFailed(unit, tunnel string, cause error) error {
	e := deyerr.New(deyerr.B003, deyerr.Params{"unit": unit})
	if cause != nil {
		e = deyerr.Wrap(deyerr.B003, cause, deyerr.Params{"unit": unit})
	}
	var lines []string
	if config.ValidID(tunnel) {
		if tail, err := systemd.LogTail(h.path(systemd.TunnelLogFile(tunnel)), systemd.FailureLogLines); err == nil {
			lines = tail
		}
	}
	if len(lines) == 0 && cause != nil {
		lines = []string{cause.Error()}
	}
	for i := range lines {
		lines[i] = dlog.Redact(lines[i])
	}
	return e.WithDetail(strings.Join(lines, "\n")).WithLog(systemd.TunnelLogFile(tunnel))
}

// ensureTunnelLogDir creates /var/log/deyroute/tunnels: systemd refuses to
// start a unit whose StandardOutput=append: directory is missing.
func (h *Hub) ensureTunnelLogDir() error {
	p := h.path(systemd.TunnelLogDir)
	if err := os.MkdirAll(p, dlog.DirMode); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	return nil
}

// ensureIPForward sets net.ipv4.ip_forward=1 for a NAT transport (runtime;
// the sysctl profile persists it, section 12).
func (h *Hub) ensureIPForward() error {
	p := h.path("/proc/sys/net/ipv4/ip_forward")
	data, err := os.ReadFile(p) // #nosec G304 -- fixed /proc path below Root
	if err == nil && strings.TrimSpace(string(data)) == "1" {
		return nil
	}
	if err := os.WriteFile(p, []byte("1\n"), 0o600); err != nil {
		return deyerr.Wrap(deyerr.X033, err, deyerr.Params{"key": "net.ipv4.ip_forward", "value": "1"})
	}
	return nil
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---------------------------------------------------------------- backend hooks

// hookKind names a backend step around a hub unit.
type hookKind int

const (
	hookPreStart hookKind = iota
	hookPostStart
	hookPostStop
)

// hookRunner is the unnamed runner interface the backends' optional hooks
// take (wireguard.Runner is an alias of the same type), so they are found
// by a structural type assertion without importing any backend package.
type hookRunner = interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

type preStarter interface {
	PreStart(ctx context.Context, configDir string, r hookRunner) error
}

type postStarter interface {
	PostStart(ctx context.Context, configDir string, r hookRunner) error
}

type postStopper interface {
	PostStop(ctx context.Context, configDir string, r hookRunner) error
}

// hubHook runs the optional PreStart / PostStart / PostStop step of a
// backend for a hub unit (awg/userspace: UAPI directory, device
// configuration after start, interface cleanup after stop).
func (h *Hub) hubHook(ctx context.Context, kind hookKind, backendName, configDir string) error {
	b, ok := backendByName(backendName)
	if !ok || configDir == "" {
		return nil
	}
	dir := h.path(configDir)
	switch kind {
	case hookPreStart:
		if p, ok := b.(preStarter); ok {
			return p.PreStart(ctx, dir, h.o.Runner)
		}
	case hookPostStart:
		if p, ok := b.(postStarter); ok {
			return p.PostStart(ctx, dir, h.o.Runner)
		}
	case hookPostStop:
		if p, ok := b.(postStopper); ok {
			return p.PostStop(ctx, dir, h.o.Runner)
		}
	}
	return nil
}

// ---------------------------------------------------------------- probes

// remaining is the time left until ctx's deadline (the probe timeout).
func remaining(ctx context.Context, def time.Duration) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl); d > 0 {
			return d
		}
		return time.Millisecond
	}
	return def
}

// probePath is the path probe of section 9: the hub connects to its own
// listen port (the probe port) and reaches the target on the node through
// the tunnel; auto sends a TLS ClientHello and any answer passes. A clean
// close without data passes only when the node found that the target
// itself closes that way. A UDP-only tunnel has no TCP port to probe: its
// units are checked instead (hub unit active, node unit active). Every
// sample goes into the probe history.
func (c *tunnelCtl) probePath(ctx context.Context, sc state.Candidate) failover.Probe {
	h := c.h
	t, plan := c.snapshot()
	pm, tcp := t.ProbeTarget()
	var r health.Result
	if tcp {
		addr := net.JoinHostPort(c.probeHost(plan, sc), strconv.Itoa(pm.Listen))
		opts := health.PathOptions{AcceptCleanClose: c.closesWithoutData(sc.Node)}
		r = health.Path(ctx, addr, probeKind(pm), remaining(ctx, probeTimeout(t)), opts)
	} else {
		r = c.unitProbe(ctx, sc)
	}
	err := h.st.AppendProbe(t.ID, sc.Node, sc.Transport, state.ProbeSample{
		At: h.now(), OK: r.OK, RTT: r.RTT, Kind: ProbeKindPath, Error: r.Err,
	})
	if err != nil {
		h.log.Debug("cannot store a probe sample", dlog.Tunnel(t.ID), dlog.Err(err))
	}
	return failover.Probe{OK: r.OK, RTT: r.RTT, Err: r.Err}
}

// unitProbe checks a candidate by its units (UDP-only tunnels): the hub
// unit is active and, when the node is online, so is its node unit. The
// RTT is the node's control RTT.
func (c *tunnelCtl) unitProbe(ctx context.Context, sc state.Candidate) health.Result {
	h := c.h
	unit := systemd.UnitName(systemd.InstanceName(c.id, sc.Node, sc.Transport))
	st, err := h.o.Systemd.Show(ctx, unit)
	if err != nil {
		return health.Result{Err: deyerr.As(err).Message()}
	}
	if st.ActiveState != "" && !st.Active() {
		return health.Result{Err: "hub unit is " + st.ActiveState}
	}
	ns, _ := h.nodeState(sc.Node)
	if h.Online(sc.Node) {
		if s, ok := ns.Units[unit]; ok && s != "active" {
			return health.Result{Err: "node unit is " + s}
		}
	}
	return health.Result{OK: true, RTT: time.Duration(ns.ControlRTTms) * time.Millisecond}
}

// nodeService is the node_service probe of section 9: probe.tcp of the
// tunnel's probe target on node, cached for one probe interval; unknown
// when the node is offline (or the tunnel has no TCP target).
func (c *tunnelCtl) nodeService(ctx context.Context, node string) (up, known bool) {
	h := c.h
	t, _ := c.snapshot()
	pm, tcp := t.ProbeTarget()
	if !tcp || !h.Online(node) {
		return false, false
	}
	interval := time.Duration(max(t.Failover.ProbeIntervalS, 1)) * time.Second
	c.mu.Lock()
	e, ok := c.svc[node]
	c.mu.Unlock()
	if ok && h.now().Sub(e.at) < interval {
		return e.up, e.known
	}
	var res api.ProbeResultDTO
	err := h.Call(ctx, node, api.CmdProbeTCP, api.ProbeArgs{Target: pm.Target, TimeoutMs: int(probeTimeout(t) / time.Millisecond)}, &res)
	if err != nil {
		return false, false
	}
	c.mu.Lock()
	c.svc[node] = svcEntry{up: res.OK, known: true, at: h.now()}
	c.mu.Unlock()
	if res.OK {
		c.refreshPlain(ctx, node, t)
	}
	return res.OK, true
}

// closesWithoutData reports the node's verdict that the tunnel target
// closes a TLS ClientHello cleanly without sending anything (then a clean
// close through the tunnel is a success, ARCHITECTURE.md §7.5).
func (c *tunnelCtl) closesWithoutData(node string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.plain[node]
	return ok && e.closes && c.h.now().Sub(e.at) < 2*plainCheckTTL
}

// refreshPlain asks node (probe.tls on the probe target) whether the
// target closes without speaking TLS; kept for plainCheckTTL.
func (c *tunnelCtl) refreshPlain(ctx context.Context, node string, t config.Tunnel) {
	h := c.h
	pm, tcp := t.ProbeTarget()
	if !tcp || !h.Online(node) {
		return
	}
	c.mu.Lock()
	e, ok := c.plain[node]
	c.mu.Unlock()
	if ok && h.now().Sub(e.at) < plainCheckTTL {
		return
	}
	var res api.ProbeResultDTO
	cctx, cancel := context.WithTimeout(ctx, probeTimeout(t)+time.Second)
	defer cancel()
	if err := h.Call(cctx, node, api.CmdProbeTLS, api.ProbeArgs{Target: pm.Target, TimeoutMs: int(probeTimeout(t) / time.Millisecond)}, &res); err != nil {
		return
	}
	c.mu.Lock()
	c.plain[node] = plainEntry{closes: !res.OK && res.Error == health.ReasonClosedNoData, at: h.now()}
	c.mu.Unlock()
}
