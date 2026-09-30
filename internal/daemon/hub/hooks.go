package hub

import (
	"context"
	stderrors "errors"
	"log/slog"

	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// Extension hooks of the tunnel controller (ARCHITECTURE.md §7.3). The hub
// core calls them at the right moments; the tunnel manager (tunnels.go)
// and the controllers (tunnel_ctl.go) implement them.

// reconcileAll brings every tunnel in line with the current configuration:
// install backends, render every rung on every node (both sides), write
// drop-ins, start/stop controllers (failover engines), remove stale
// instances. It is called after ConfigApply, after settings that change
// rendering (domain, decoys) and after a node was removed.
func (h *Hub) reconcileAll(ctx context.Context) error {
	if err := h.waitTunnelsReady(ctx); err != nil {
		return err
	}
	h.tun.opMu.Lock()
	defer h.tun.opMu.Unlock()
	return h.reconcileLocked(ctx, false)
}

// onNodeRemoved runs before a node is removed from config.yaml while its
// control stream is still open: every tunnel that uses the node moves away
// from it (the failover engine forgets the node; an active candidate there
// is replaced by rung 1 of the next node), then the node's instances are
// removed on the node (best effort for an offline node) and on the hub,
// and their control ports are released.
func (h *Hub) onNodeRemoved(ctx context.Context, node string) error {
	if err := h.waitTunnelsReady(ctx); err != nil {
		return err
	}
	h.tun.opMu.Lock()
	defer h.tun.opMu.Unlock()
	var errs []error
	for _, c := range h.tun.all() {
		if !c.usesNode(node) {
			continue
		}
		if err := c.dropNode(ctx, node); err != nil {
			h.log.Warn("tunnel could not move away from the removed node", dlog.Tunnel(c.id), dlog.Node(node), dlog.Err(err))
			errs = append(errs, err)
		}
	}
	// Tunnels without a controller (disabled) lose the node's warm rungs too.
	cfg := h.Config()
	for _, id := range cfg.TunnelsUsingNode(node) {
		if h.tun.lookup(id) != nil {
			continue
		}
		t, _ := cfg.Tunnel(id)
		if err := h.removeNodeCandidates(ctx, t.ID, node, ladderOf(cfg, t)); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		h.log.Info("node removal: some tunnel units could not be cleaned up", dlog.Node(node), slog.Int("errors", len(errs)))
	}
	return stderrors.Join(errs...)
}

// activeHubSides returns the hub sides of the ACTIVE candidates (plus a
// running canary); their NAT and masquerade rules go into table inet deyroute
// (warm rungs never have NAT rules).
func (h *Hub) activeHubSides() []render.Side {
	var out []render.Side
	for _, c := range h.tun.all() {
		out = append(out, c.startedSides()...)
	}
	return out
}

// stopEngines stops every failover engine (StopAll, uninstall) so that no
// engine restarts the units that are being stopped. The controllers are
// dropped; reconcileAll (or a hub restart) creates them again.
func (h *Hub) stopEngines(ctx context.Context) error {
	h.tun.opMu.Lock()
	defer h.tun.opMu.Unlock()
	for _, c := range h.tun.all() {
		h.tun.remove(c.id)
		c.halt()
	}
	_ = ctx
	return nil
}

// tunnelState returns the current state machine of a tunnel: live from its
// failover engine (RTT, fail counts) when it runs, else the persisted
// record.
func (h *Hub) tunnelState(id string) (state.TunnelState, bool) {
	if c := h.tun.lookup(id); c != nil {
		if ts, ok := c.liveState(); ok {
			return ts, true
		}
	}
	ts, ok, err := h.st.GetTunnel(id)
	if err != nil {
		h.log.Warn("cannot read the tunnel state", dlog.Tunnel(id), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return state.TunnelState{ID: id}, false
	}
	return ts, ok
}
