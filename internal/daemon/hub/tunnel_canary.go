package hub

import (
	"context"
	"net"
	"strconv"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// canaryState is the canary unit of a tunnel (section 9, phase 8): rung 1
// of the ladder on the primary node, rendered as "<tunnel>.canary" with its
// own control port and one loopback port (127.0.0.1:<loop> on the hub →
// the node's built-in echo). It runs only while the tunnel is away from
// rung 1 of the primary node; the engine fails back when the canary echo
// passed recover_threshold probes in a row.
type canaryState struct {
	want     bool              // the engine asked for it (ProbeCanary)
	ready    bool              // units started, echo running
	unusable bool              // cannot be built: blind failback until the next re-check
	borrowed bool              // diag speed uses the canary slot: the canary stays down
	cand     *render.Candidate // the rendered canary (kept after a stop for cleanup)
	loop     int               // hub loopback port
	echo     int               // node echo port
	node     string            // primary node it was built for
	rung     string            // rung it was built for
}

// requestCanary asks the controller goroutine to start (want) or stop the
// canary.
func (c *tunnelCtl) requestCanary(want bool) {
	c.mu.Lock()
	changed := c.can.want != want
	c.can.want = want
	needed := want && !c.can.ready && !c.can.unusable
	c.mu.Unlock()
	if changed || needed {
		select {
		case c.canKick <- struct{}{}:
		default:
		}
	}
}

// probeCanary is Actions.ProbeCanary: a TCP echo through the canary. While
// the canary is being set up the probe fails (configured); a canary that
// cannot be built reports configured=false so the engine uses the blind
// failback of phase 5.
func (c *tunnelCtl) probeCanary(ctx context.Context) (failover.Probe, bool) {
	c.mu.Lock()
	cs := c.can
	c.mu.Unlock()
	if cs.unusable {
		return failover.Probe{}, false
	}
	if !cs.ready || cs.cand == nil {
		c.requestCanary(true)
		return failover.Probe{Err: "canary starting"}, true
	}
	c.requestCanary(true)
	host := c.h.o.ProbeHost
	if len(cs.cand.Hub.NAT) > 0 {
		if cfg := c.h.Config(); cfg.Hub != nil && cfg.Hub.PublicIP != "" {
			host = cfg.Hub.PublicIP
		}
	}
	t, _ := c.snapshot()
	r := health.TCPEcho(ctx, net.JoinHostPort(host, strconv.Itoa(cs.loop)), remaining(ctx, probeTimeout(t)))
	return failover.Probe{OK: r.OK, RTT: r.RTT, Err: r.Err}, true
}

// canaryWork runs on the controller goroutine: it starts or stops the
// canary as requested.
func (c *tunnelCtl) canaryWork(ctx context.Context) {
	c.canWork.Lock()
	defer c.canWork.Unlock()
	c.mu.Lock()
	want, ready, unusable, borrowed := c.can.want, c.can.ready, c.can.unusable, c.can.borrowed
	c.mu.Unlock()
	switch {
	case borrowed:
		// diag speed runs in the canary slot; it kicks the canary when done.
	case want && !ready && !unusable:
		if err := c.canaryUp(ctx); err != nil {
			// A side that did start does not keep running on its own: the
			// canary is taken down whatever failed (it is built again when
			// the engine asks, unless it cannot be built at all).
			c.canaryDown(context.WithoutCancel(ctx))
			if ctx.Err() != nil {
				return
			}
			transient := deyerr.HasCode(err, deyerr.N003) || deyerr.HasCode(err, deyerr.N004)
			if !transient {
				c.mu.Lock()
				c.can.unusable = true
				c.mu.Unlock()
			}
			c.h.log.Warn("canary could not be started; failback is blind until the next re-check",
				dlog.Tunnel(c.id), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		}
	case !want && ready:
		c.canaryDown(ctx)
	}
}

// canaryUp renders and starts the canary: the node's loopback echo
// (echo.start, its port remembered), render.CanaryPlan, the hub files and
// drop-in, the node payload, then the units, server side first.
func (c *tunnelCtl) canaryUp(ctx context.Context) error {
	h := c.h
	t, plan := c.snapshot()
	if len(t.Nodes) == 0 || len(plan.Ladder) == 0 {
		return deyerr.New(deyerr.C009, deyerr.Params{"tunnel": c.id})
	}
	primary, rung := t.Nodes[0], plan.Ladder[0]
	if _, skipped := c.engineState().Skipped[state.Candidate{Node: primary, Transport: rung}.Key()]; skipped {
		return deyerr.New(deyerr.F006, deyerr.Params{"tunnel": c.id, "target": rung}).
			WithWhy("rung 1 is skipped on the primary node, so it has no canary")
	}
	_, tr, err := backend.Lookup(rung)
	if err != nil {
		return err
	}
	if !tr.Supports(config.ProtoTCP) {
		return deyerr.New(deyerr.B010, deyerr.Params{"transport": rung, "proto": config.ProtoTCP}).
			WithWhy("the canary echo is TCP; a UDP-only rung 1 has no canary")
	}
	if !h.Online(primary) {
		return deyerr.New(deyerr.N003, deyerr.Params{"node": primary})
	}
	var echoPort int
	if _, err := h.st.GetMeta(metaCanaryEcho+c.id, &echoPort); err != nil {
		echoPort = 0
	}
	var er api.EchoResult
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	err = h.Call(cctx, primary, api.CmdEchoStart, api.EchoArgs{Port: echoPort}, &er)
	if err != nil && echoPort != 0 && deyerr.HasCode(err, deyerr.P012) {
		// The remembered port is taken now: any free port.
		err = h.Call(cctx, primary, api.CmdEchoStart, api.EchoArgs{Port: 0}, &er)
	}
	cancel()
	if err != nil {
		return err
	}
	if er.Port != echoPort {
		if err := h.st.PutMeta(metaCanaryEcho+c.id, er.Port); err != nil {
			h.log.Warn("cannot remember the canary echo port", dlog.Tunnel(c.id), dlog.Err(err))
		}
	}
	cand, err := render.CanaryPlan(h.planInput(h.Config(), t), er.Port)
	if err != nil {
		return err
	}
	loop, err := h.allocCtlPort(render.CanaryLoopbackKey(c.id))
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.can.cand, c.can.loop, c.can.echo, c.can.node, c.can.rung = cand, loop, er.Port, primary, rung
	c.mu.Unlock()
	changed, err := h.hubWriter().Write(ctx, cand.Hub)
	if err != nil {
		return err
	}
	if changed {
		if err := h.o.Systemd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	rctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	err = h.Call(rctx, primary, api.CmdBackendRender, render.NodePayload(c.id, cand.NodeSide), nil)
	cancel()
	if err != nil {
		return err
	}
	hubSide := func() error { return h.startHubSide(ctx, c.id, cand.Backend, cand.Hub) }
	nodeSide := func() error {
		sctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		return h.Call(sctx, primary, api.CmdUnitStart, api.UnitArgs{Instance: cand.NodeSide.Instance}, nil)
	}
	first, second := hubSide, nodeSide
	if cand.Transport.Direction.ServerSide() == backend.SideNode {
		first, second = nodeSide, hubSide
	}
	if err := first(); err != nil {
		return err
	}
	if err := second(); err != nil {
		return err
	}
	c.mu.Lock()
	c.can.ready = true
	c.mu.Unlock()
	if len(cand.Hub.NAT) > 0 || len(cand.Hub.Masquerade) > 0 {
		if err := h.applyFirewall(ctx); err != nil {
			return err
		}
	}
	h.log.Info("canary started", dlog.Tunnel(c.id), dlog.Node(primary), dlog.Transport(rung))
	return nil
}

// canaryDown stops the canary units (client side first) and the node echo.
// The rendered files stay (warm) until the tunnel or its rung 1 changes.
func (c *tunnelCtl) canaryDown(ctx context.Context) {
	h := c.h
	c.mu.Lock()
	cs := c.can
	c.can.ready = false
	c.mu.Unlock()
	if cs.cand == nil {
		return
	}
	hubSide := func() {
		if err := h.stopHubSide(ctx, cs.cand.Hub.Instance, cs.cand.Backend, cs.cand.Hub.ConfigDir); err != nil {
			h.log.Warn("cannot stop the canary unit", dlog.Tunnel(c.id), dlog.Err(err))
		}
	}
	nodeSide := func() {
		if !h.Online(cs.node) {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		if err := h.Call(cctx, cs.node, api.CmdUnitStop, api.UnitArgs{Instance: cs.cand.NodeSide.Instance}, nil); err != nil {
			h.log.Warn("cannot stop the canary unit on its node", dlog.Tunnel(c.id), dlog.Node(cs.node), dlog.Err(err))
		}
		if cs.echo > 0 {
			if err := h.Call(cctx, cs.node, api.CmdEchoStop, api.EchoArgs{Port: cs.echo}, nil); err != nil {
				h.log.Warn("cannot stop the canary echo", dlog.Tunnel(c.id), dlog.Node(cs.node), dlog.Err(err))
			}
		}
	}
	if cs.cand.Transport.Direction.ServerSide() == backend.SideNode {
		hubSide()
		nodeSide()
	} else {
		nodeSide()
		hubSide()
	}
	if len(cs.cand.Hub.NAT) > 0 || len(cs.cand.Hub.Masquerade) > 0 {
		_ = h.applyFirewall(ctx)
	}
	if cs.ready {
		h.log.Info("canary stopped", dlog.Tunnel(c.id))
	}
}

// stopCanaryUnits stops a canary left running by an earlier hub process
// (its rendering is not known here): the hub unit, the node unit and the
// remembered echo.
func (c *tunnelCtl) stopCanaryUnits(ctx context.Context) {
	h := c.h
	inst := systemd.CanaryInstance(c.id)
	if err := h.o.Systemd.Stop(ctx, systemd.UnitName(inst)); err != nil && !isNotLoaded(err) {
		h.log.Warn("cannot stop the canary unit", dlog.Tunnel(c.id), dlog.Err(err))
	}
	t, _ := c.snapshot()
	if len(t.Nodes) == 0 || !h.Online(t.Nodes[0]) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	defer cancel()
	_ = h.Call(cctx, t.Nodes[0], api.CmdUnitStop, api.UnitArgs{Instance: inst}, nil)
	var echoPort int
	if ok, err := h.st.GetMeta(metaCanaryEcho+c.id, &echoPort); err == nil && ok && echoPort > 0 {
		_ = h.Call(cctx, t.Nodes[0], api.CmdEchoStop, api.EchoArgs{Port: echoPort}, nil)
	}
}

// checkCanary drops a canary that no longer matches the tunnel (rung 1 or
// the primary node changed): its units stop and its files go, a new one is
// built when the engine asks again.
func (c *tunnelCtl) checkCanary(ctx context.Context, t config.Tunnel, plan render.TunnelPlan) {
	// A canary that matches is kept; while diag speed borrows the slot its
	// units are the speed test's (releaseCanary makes the controller check
	// again afterwards).
	keep := func(cs canaryState) bool {
		return cs.cand == nil || cs.borrowed ||
			(len(t.Nodes) > 0 && len(plan.Ladder) > 0 && cs.node == t.Nodes[0] && cs.rung == plan.Ladder[0])
	}
	c.mu.Lock()
	cs := c.can
	c.mu.Unlock()
	if keep(cs) {
		return
	}
	// Not while the controller goroutine sets the canary up.
	c.canWork.Lock()
	defer c.canWork.Unlock()
	c.mu.Lock()
	cs = c.can
	c.mu.Unlock()
	if keep(cs) {
		return
	}
	c.canaryDown(ctx)
	c.removeCanary(ctx, cs)
	c.mu.Lock()
	c.can = canaryState{want: c.can.want}
	c.mu.Unlock()
}

// removeCanary deletes the canary's files and drop-ins on the hub and on
// its node.
func (c *tunnelCtl) removeCanary(ctx context.Context, cs canaryState) {
	h := c.h
	if cs.cand == nil {
		return
	}
	if err := h.hubWriter().Remove(ctx, cs.cand.Hub.Instance, cs.cand.Hub.ConfigDir); err != nil {
		h.log.Warn("cannot remove the canary files", dlog.Tunnel(c.id), dlog.Err(err))
	}
	if h.Online(cs.node) {
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		if err := h.Call(cctx, cs.node, api.CmdBackendRemove, api.BackendRemoveArgs{
			Instance: cs.cand.NodeSide.Instance, ConfigDir: cs.cand.NodeSide.ConfigDir,
		}, nil); err != nil {
			h.log.Warn("cannot remove the canary files on its node", dlog.Tunnel(c.id), dlog.Node(cs.node), dlog.Err(err))
		}
	}
}
