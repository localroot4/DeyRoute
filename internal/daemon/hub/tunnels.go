package hub

import (
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// tunnelManager owns the tunnel controllers of the hub (ARCHITECTURE.md
// §7.3): one controller per enabled tunnel. Controllers are created at
// start (reconcile after a hub restart, section 9), by TunnelAdd, enable
// and reconcileAll, and stopped on disable, delete and shutdown.
type tunnelManager struct {
	mu     sync.Mutex
	ctx    context.Context // Serve's context; nil before Serve
	closed bool
	ctls   map[string]*tunnelCtl

	// opMu serialises tunnel operations: Local API changes, reconcile and
	// node removal. Failover engines never take it, so an operation may wait
	// for an engine command while holding it.
	opMu sync.Mutex

	// pool runs the 60-second all-ports report probes (section 12: 8).
	pool  *health.Pool
	ready chan struct{} // closed when the startup reconcile finished

	// nodeSync carries the ids of nodes whose first heartbeat arrived: the
	// manager removes their leftovers (instances of tunnels deleted while
	// the node was offline).
	nodeSync chan string

	instMu    sync.Mutex
	installed map[string]installEntry

	// udpMu serialises UDP reachability probes (one node at a time).
	udpMu sync.Mutex
}

// installEntry is the outcome of one backend installation on the hub or on
// a node ("hub|backhaul@v0.7.2", "de-1|backhaul@v0.7.2").
type installEntry struct {
	err error
	at  time.Time
}

// init prepares the zero manager (New).
func (m *tunnelManager) init() {
	m.ctls = map[string]*tunnelCtl{}
	m.installed = map[string]installEntry{}
	m.ready = make(chan struct{})
	m.nodeSync = make(chan string, nodeSyncQueue)
}

// register adds c; it fails once the hub is stopping (or before Serve).
func (m *tunnelManager) register(c *tunnelCtl) (context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx == nil {
		return nil, deyerr.New(deyerr.F007, deyerr.Params{"tunnel": c.id})
	}
	m.ctls[c.id] = c
	return m.ctx, nil
}

// lookup returns the controller of tunnel id (nil when it has none).
func (m *tunnelManager) lookup(id string) *tunnelCtl {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ctls[id]
}

// remove takes the controller of id out of the manager.
func (m *tunnelManager) remove(id string) *tunnelCtl {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.ctls[id]
	delete(m.ctls, id)
	return c
}

// all returns every controller sorted by tunnel id.
func (m *tunnelManager) all() []*tunnelCtl {
	m.mu.Lock()
	out := make([]*tunnelCtl, 0, len(m.ctls))
	for _, c := range m.ctls {
		out = append(out, c)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// startTunnels prepares the manager for Serve's context (before the Local
// API accepts calls).
func (h *Hub) startTunnels(ctx context.Context) {
	h.tun.mu.Lock()
	h.tun.ctx = ctx
	h.tun.pool = health.NewPool(health.PoolSize)
	h.tun.mu.Unlock()
}

// runTunnels is the manager goroutine of Serve: the startup reconcile
// (every enabled tunnel gets its controller; units that run are adopted,
// never restarted), then on shutdown every controller stops. Units keep
// running when the hub stops (section 3: a hub crash never takes an active
// tunnel down).
func (h *Hub) runTunnels(ctx context.Context) {
	h.tun.opMu.Lock()
	if err := h.reconcileLocked(ctx, true); err != nil && ctx.Err() == nil {
		h.log.Warn("tunnel reconcile at start finished with errors", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	h.tun.opMu.Unlock()
	close(h.tun.ready)
	// The controllers know the running candidates now: the table drops the
	// NAT rules carried over from the previous hub process that no adopted
	// candidate uses any more.
	h.requestFirewall()
	for done := false; !done; {
		select {
		case <-ctx.Done():
			done = true
		case node := <-h.tun.nodeSync:
			h.cleanNodeLeftovers(ctx, node)
		}
	}
	h.tun.mu.Lock()
	h.tun.closed = true
	ctls := make([]*tunnelCtl, 0, len(h.tun.ctls))
	for _, c := range h.tun.ctls {
		ctls = append(ctls, c)
	}
	h.tun.ctls = map[string]*tunnelCtl{}
	pool := h.tun.pool
	h.tun.mu.Unlock()
	for _, c := range ctls {
		c.halt()
	}
	if pool != nil {
		pool.Close()
	}
}

// requestNodeCleanup asks the manager to remove the leftovers on node once
// its unit list is known (first heartbeat of a stream). It never blocks: a
// full queue drops the request, the next reconcile removes them anyway.
func (h *Hub) requestNodeCleanup(node string) {
	select {
	case h.tun.nodeSync <- node:
	default:
		h.log.Debug("node cleanup queue is full", dlog.Node(node))
	}
}

// cleanNodeLeftovers removes the instances on node that config.yaml no
// longer uses (tunnels or rungs deleted while the node was offline).
func (h *Hub) cleanNodeLeftovers(ctx context.Context, node string) {
	h.tun.opMu.Lock()
	defer h.tun.opMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	cfg := h.Config()
	if _, ok := cfg.NodeByID(node); !ok {
		return
	}
	h.removeStaleOnNode(ctx, cfg, node, h.keepInstances(cfg))
	h.stopEchoOrphans(ctx, node)
	for _, c := range h.tun.all() {
		if c.usesNode(node) {
			c.stopNodeStrays(ctx, node)
		}
	}
}

// waitTunnelsReady waits until the startup reconcile finished (or ctx ends).
func (h *Hub) waitTunnelsReady(ctx context.Context) error {
	select {
	case <-h.tun.ready:
		return nil
	case <-ctx.Done():
		return deyerr.Wrap(deyerr.X031, ctx.Err(), deyerr.Params{"command": "tunnel reconcile at start"})
	}
}

// reconcileLocked brings every tunnel in line with config.yaml (opMu held):
// controllers of removed or disabled tunnels stop (their units too), every
// enabled tunnel gets a controller or is re-planned, units of disabled
// tunnels are stopped, stale instances (tunnels, nodes or rungs that are
// gone) are removed on the hub and on online nodes, and the records of
// tunnels deleted from config.yaml by hand are cleaned up. At startup
// running units are adopted and nothing is restarted.
func (h *Hub) reconcileLocked(ctx context.Context, startup bool) error {
	cfg := h.Config()
	var errs []error
	for _, c := range h.tun.all() {
		t, ok := cfg.Tunnel(c.id)
		if ok && t.Enabled {
			continue
		}
		h.tun.remove(c.id)
		c.stop(ctx, true)
		if ok {
			h.persistDisabled(c.id, c.engineState())
		}
	}
	units, err := h.o.Systemd.ListInstances(ctx)
	if err != nil {
		h.log.Warn("cannot list the tunnel units", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	for i := range cfg.Tunnels {
		t := cfg.Tunnels[i]
		if ctx.Err() != nil {
			break
		}
		if !t.Enabled {
			h.ensureTunnelStopped(ctx, t.ID, t.Nodes, units)
			if ts, ok := h.tunnelState(t.ID); !ok || ts.State != state.StateDisabled {
				h.persistDisabled(t.ID, ts)
			}
			continue
		}
		if c := h.tun.lookup(t.ID); c != nil {
			if err := c.update(ctx, updateOpts{restartActive: !startup}); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if _, err := h.startCtl(ctx, t.ID, nil); err != nil {
			errs = append(errs, err)
		}
	}
	h.forgetRemovedTunnels(cfg)
	h.removeStale(ctx, cfg, units)
	h.requestFirewall()
	return stderrors.Join(errs...)
}

// startCtl creates, prepares (plan, install, render; with rep the progress
// steps of section 6 including the firewall step) and launches the
// controller of tunnel id. The controller runs even when preparation failed
// partly (offline node, failed install): it retries on its own, and the
// first failed step is returned. opMu is held.
func (h *Hub) startCtl(ctx context.Context, id string, rep *steps) (*tunnelCtl, error) {
	c := newTunnelCtl(h, id)
	res, err := c.apply(ctx, applyOpts{rep: rep, add: rep != nil})
	fatal := err
	if err != nil {
		c.setProblem(err)
	} else {
		fatal = res.fatal
	}
	if rep != nil {
		fwRep := rep
		if fatal != nil {
			fwRep = nil // steps after a failed one are not reported
		}
		if err := h.firewallStep(ctx, fwRep); err != nil && fatal == nil {
			fatal = err
		}
	}
	parent, rerr := h.tun.register(c)
	if rerr != nil {
		return nil, rerr
	}
	c.launch(parent, res.skips)
	h.requestFirewall()
	return c, fatal
}

// persistDisabled stores the DISABLED state of a tunnel (its failover
// history is kept; the next start begins at rung 1 of the primary node).
func (h *Hub) persistDisabled(id string, ts state.TunnelState) {
	ts.ID = id
	ts.State = state.StateDisabled
	ts.Active = state.Candidate{}
	ts.Tried = nil
	ts.UpSince = time.Time{}
	ts.StableSince = time.Time{}
	ts.TransitionCause = "tunnel disabled"
	ts.UpdatedAt = h.now()
	if err := h.st.PutTunnel(ts); err != nil {
		h.log.Warn("cannot save the tunnel state", dlog.Tunnel(id), dlog.Err(err))
	}
}

// ensureTunnelStopped stops every running unit of a tunnel without a
// controller (disabled): on the hub and on the online nodes.
func (h *Hub) ensureTunnelStopped(ctx context.Context, tunnel string, nodes []string, units []systemd.UnitState) {
	for _, u := range units {
		in, err := systemd.ParseInstance(u.Instance)
		if err != nil || in.Tunnel != tunnel || !unitRunning(u.ActiveState) {
			continue
		}
		if err := h.o.Systemd.Stop(ctx, u.Unit); err != nil {
			h.log.Warn("cannot stop a unit of a disabled tunnel", dlog.Tunnel(tunnel), slog.String("unit", u.Unit), dlog.Err(err))
		}
	}
	for _, n := range nodes {
		if !h.Online(n) {
			continue
		}
		ns, _ := h.nodeState(n)
		for _, unit := range sortedKeys(ns.Units) {
			inst, ok := systemd.InstanceOf(unit)
			if !ok || !unitRunning(ns.Units[unit]) {
				continue
			}
			if in, err := systemd.ParseInstance(inst); err != nil || in.Tunnel != tunnel {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
			err := h.Call(cctx, n, api.CmdUnitStop, api.UnitArgs{Instance: inst}, nil)
			cancel()
			if err != nil {
				h.log.Warn("cannot stop a unit of a disabled tunnel on its node", dlog.Tunnel(tunnel), dlog.Node(n), dlog.Err(err))
			}
		}
	}
}

// forgetRemovedTunnels deletes the state records, secrets and meta data of
// tunnels that are no longer in config.yaml (deleted by hand and applied).
func (h *Hub) forgetRemovedTunnels(cfg *config.Config) {
	gone := map[string]bool{}
	if list, err := h.st.ListTunnels(); err == nil {
		for _, ts := range list {
			gone[ts.ID] = true
		}
	}
	if list, err := h.secretStore().Tunnels(); err == nil {
		for _, id := range list {
			gone[id] = true
		}
	}
	for _, t := range cfg.Tunnels {
		delete(gone, t.ID)
	}
	for _, id := range sortedKeys(gone) {
		h.forgetTunnel(id)
		h.log.Info("records of a removed tunnel deleted", dlog.Tunnel(id))
	}
}

// forgetTunnel deletes everything state.db and secrets/ hold for a tunnel
// (DeleteTunnel also releases its control ports and network index).
func (h *Hub) forgetTunnel(id string) {
	if !config.ValidID(id) {
		return
	}
	if err := h.st.DeleteTunnel(id); err != nil {
		h.log.Warn("cannot delete the tunnel state", dlog.Tunnel(id), dlog.Err(err))
	}
	h.forgetTraffic(id)
	if err := h.st.DeleteMeta(metaCanaryEcho + id); err != nil {
		h.log.Warn("cannot delete the canary record", dlog.Tunnel(id), dlog.Err(err))
	}
	if err := h.secretStore().DeleteTunnel(id); err != nil {
		h.log.Warn("cannot delete the tunnel secrets", dlog.Tunnel(id), dlog.Err(err))
	}
}

// desiredInstances is every deyroute-tun@ instance config.yaml can use: every
// rung of every tunnel (enabled or not: disabled tunnels stay warm) on every
// node of the tunnel, plus the canary instance of each tunnel.
func desiredInstances(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	for i := range cfg.Tunnels {
		t := &cfg.Tunnels[i]
		out[systemd.CanaryInstance(t.ID)] = true
		ladder, err := cfg.ResolveLadder(t, supports)
		if err != nil {
			continue
		}
		for _, n := range t.Nodes {
			for _, r := range ladder {
				out[systemd.InstanceName(t.ID, n, r)] = true
			}
		}
	}
	return out
}

// keepInstances is desiredInstances plus the started candidates of every
// controller (an active candidate whose rung or node left config.yaml runs
// until its engine moved away; the controller removes it then).
func (h *Hub) keepInstances(cfg *config.Config) map[string]bool {
	out := desiredInstances(cfg)
	for _, c := range h.tun.all() {
		for _, inst := range c.startedInstances() {
			out[inst] = true
		}
	}
	return out
}

// removeStale removes the instances config.yaml no longer uses: on the hub
// (render.Stale over the listed units) and on every online node (from its
// heartbeat).
func (h *Hub) removeStale(ctx context.Context, cfg *config.Config, units []systemd.UnitState) {
	desired := h.keepInstances(cfg)
	for _, inst := range render.Stale(units, desired) {
		if err := h.removeHubInstance(ctx, inst); err != nil {
			h.log.Warn("cannot remove a stale tunnel unit", slog.String("instance", inst), dlog.Err(err))
			continue
		}
		h.log.Info("stale tunnel unit removed", slog.String("instance", inst))
	}
	for _, n := range cfg.Nodes {
		h.removeStaleOnNode(ctx, cfg, n.ID, desired)
	}
}

// removeStaleOnNode removes the instances of node that are not desired. A
// canary lives only on its tunnel's primary node: one left on a former
// primary goes too.
func (h *Hub) removeStaleOnNode(ctx context.Context, cfg *config.Config, node string, desired map[string]bool) {
	if !h.Online(node) {
		return
	}
	ns, _ := h.nodeState(node)
	for _, unit := range sortedKeys(ns.Units) {
		inst, ok := systemd.InstanceOf(unit)
		if !ok {
			continue
		}
		in, err := systemd.ParseInstance(inst)
		if err != nil || (!in.Canary && in.Node != node) {
			continue
		}
		if desired[inst] && (!in.Canary || primaryOf(cfg, in.Tunnel) == node) {
			continue
		}
		if err := h.removeNodeInstance(ctx, node, in); err != nil {
			h.log.Warn("cannot remove a stale tunnel unit on its node", dlog.Node(node), slog.String("instance", inst), dlog.Err(err))
		}
	}
}

// primaryOf is the primary node of tunnel ("" when it has none).
func primaryOf(cfg *config.Config, tunnel string) string {
	if t, ok := cfg.Tunnel(tunnel); ok && len(t.Nodes) > 0 {
		return t.Nodes[0]
	}
	return ""
}

// metaEchoOrphans + node lists canary echo ports a deleted tunnel left on
// the node while it was offline; they are stopped when it reconnects.
const metaEchoOrphans = "canary-echo-orphans/"

// addEchoOrphan remembers an echo port to stop on node.
func (h *Hub) addEchoOrphan(node string, port int) {
	var ports []int
	if _, err := h.st.GetMeta(metaEchoOrphans+node, &ports); err != nil {
		ports = nil
	}
	if slices.Contains(ports, port) {
		return
	}
	if err := h.st.PutMeta(metaEchoOrphans+node, append(ports, port)); err != nil {
		h.log.Warn("cannot remember a canary echo to stop", dlog.Node(node), dlog.Err(err))
	}
}

// stopEchoOrphans stops the remembered echo ports on node (first
// heartbeat after it reconnected).
func (h *Hub) stopEchoOrphans(ctx context.Context, node string) {
	var ports []int
	if ok, err := h.st.GetMeta(metaEchoOrphans+node, &ports); err != nil || !ok {
		return
	}
	var left []int
	for _, p := range ports {
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		err := h.Call(cctx, node, api.CmdEchoStop, api.EchoArgs{Port: p}, nil)
		cancel()
		if deyerr.HasCode(err, deyerr.N003) || ctx.Err() != nil {
			left = append(left, p)
		}
	}
	var err error
	if len(left) > 0 {
		err = h.st.PutMeta(metaEchoOrphans+node, left)
	} else {
		err = h.st.DeleteMeta(metaEchoOrphans + node)
	}
	if err != nil {
		h.log.Warn("cannot update the canary echoes to stop", dlog.Node(node), dlog.Err(err))
	}
}

// configDirOf returns the config directory of a warm instance.
func configDirOf(in systemd.Instance) (string, bool) {
	b, tr, ok := strings.Cut(in.Transport, "/")
	if in.Canary || !ok {
		return "", false
	}
	return render.ConfigDir(b, in.Tunnel, in.Node, tr), true
}

// removeHubInstance stops and removes one hub instance with its config
// directory (a canary: every <backend>/<tunnel>/canary directory).
func (h *Hub) removeHubInstance(ctx context.Context, inst string) error {
	in, err := systemd.ParseInstance(inst)
	if err != nil {
		return h.o.Systemd.RemoveInstance(ctx, inst)
	}
	if dir, ok := configDirOf(in); ok {
		return h.hubWriter().Remove(ctx, inst, dir)
	}
	dirs := h.canaryDirs(in.Tunnel)
	if len(dirs) == 0 {
		return h.o.Systemd.RemoveInstance(ctx, inst)
	}
	var errs []error
	for _, dir := range dirs {
		errs = append(errs, h.hubWriter().Remove(ctx, inst, dir))
	}
	return stderrors.Join(errs...)
}

// canaryDirs lists the canary config directories of tunnel on the hub.
func (h *Hub) canaryDirs(tunnel string) []string {
	entries, err := os.ReadDir(h.path(config.BackendsConfDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		dir := render.CanaryConfigDir(e.Name(), tunnel)
		if fi, err := os.Stat(h.path(dir)); err == nil && fi.IsDir() && render.CheckConfigDir(dir) == nil {
			out = append(out, dir)
		}
	}
	return out
}

// removeNodeInstance removes one instance on node (backend.remove). A
// canary instance names no backend, so it is removed under every backend
// name (only the one that exists has files).
func (h *Hub) removeNodeInstance(ctx context.Context, node string, in systemd.Instance) error {
	var dirs []string
	if dir, ok := configDirOf(in); ok {
		dirs = []string{dir}
	} else {
		for _, b := range backend.All() {
			dirs = append(dirs, render.CanaryConfigDir(b.Name(), in.Tunnel))
		}
	}
	var errs []error
	for _, dir := range dirs {
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		err := h.Call(cctx, node, api.CmdBackendRemove, api.BackendRemoveArgs{Instance: in.String(), ConfigDir: dir}, nil)
		cancel()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return stderrors.Join(errs...)
}

// removeTunnelDirs deletes what is left of a tunnel's rendered directories
// on the hub (/etc/deyroute/backends/<backend>/<tunnel>).
func (h *Hub) removeTunnelDirs(tunnel string) error {
	if !config.ValidID(tunnel) {
		return nil
	}
	entries, err := os.ReadDir(h.path(config.BackendsConfDir))
	if err != nil {
		return nil
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(h.path(config.BackendsConfDir), e.Name(), tunnel)
		if err := os.RemoveAll(dir); err != nil {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir}))
			continue
		}
		// The backend directory itself goes when it became empty.
		_ = os.Remove(filepath.Join(h.path(config.BackendsConfDir), e.Name()))
	}
	return stderrors.Join(errs...)
}

// hubWriter writes hub sides below Options.Root (files root:deyroute).
func (h *Hub) hubWriter() *render.HubWriter {
	return &render.HubWriter{Root: h.o.Root, Systemd: h.o.Systemd, Chown: h.o.Chown, DeyrouteIDs: h.o.DeyrouteIDs}
}

// nodeAttached is called for every new control stream of a compatible
// node: the node may have rebooted or lost files, so its install and
// render caches are cleared and every controller that uses it syncs.
func (h *Hub) nodeAttached(node string) {
	h.forgetNodeInstalls(node)
	for _, c := range h.tun.all() {
		if c.usesNode(node) {
			c.nodeAttached(node)
		}
	}
}

// forgetNodeInstalls drops the installation results of node from the cache.
func (h *Hub) forgetNodeInstalls(node string) {
	h.tun.instMu.Lock()
	defer h.tun.instMu.Unlock()
	for k := range h.tun.installed {
		if strings.HasPrefix(k, node+"|") {
			delete(h.tun.installed, k)
		}
	}
}

// installKey names one installation in the cache.
func installKey(where string, e backend.ManifestEntry) string {
	return where + "|" + e.Name + "@" + e.Version
}

// cachedInstall returns a remembered installation result: done reports a
// success, or a failure younger than RecheckInterval (unless force), whose
// error is err.
func (h *Hub) cachedInstall(key string, force bool) (done bool, err error) {
	h.tun.instMu.Lock()
	defer h.tun.instMu.Unlock()
	e, ok := h.tun.installed[key]
	switch {
	case !ok:
		return false, nil
	case e.err == nil:
		return true, nil
	case !force && h.now().Sub(e.at) < h.o.RecheckInterval:
		return true, e.err
	}
	return false, nil
}

// rememberInstall stores an installation result.
func (h *Hub) rememberInstall(key string, err error) {
	h.tun.instMu.Lock()
	h.tun.installed[key] = installEntry{err: err, at: h.now()}
	h.tun.instMu.Unlock()
}

// installHub installs a backend version on the hub (section 7: pinned,
// sha256-verified, never overwritten) through the download chain (via a
// node first, then directly; section 5).
func (h *Hub) installHub(ctx context.Context, e backend.ManifestEntry, force bool) error {
	if e.Builtin || e.System {
		return nil
	}
	key := installKey("hub", e)
	if done, err := h.cachedInstall(key, force); done {
		return err
	}
	ictx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	mirror := firstNonEmpty(h.o.Getenv(install.MirrorEnv), h.Config().Hub.Mirror)
	_, err := install.Layout{Root: h.o.Root}.InstallBackend(ictx, e, h.o.Arch, h.Fetcher(), mirror)
	if ctx.Err() != nil {
		return err
	}
	h.rememberInstall(key, err)
	if err != nil {
		h.log.Warn("backend installation on the hub failed", slog.String("backend", e.Name),
			slog.String("version", e.Version), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	} else {
		h.log.Info("backend available on the hub", slog.String("backend", e.Name), slog.String("version", e.Version))
	}
	return err
}

// installNode installs a backend version on node (backend.install). An
// offline or incompatible node is not remembered as a failure.
func (h *Hub) installNode(ctx context.Context, node string, e backend.ManifestEntry, force bool) error {
	if e.Builtin || e.System {
		return nil
	}
	key := installKey(node, e)
	if done, err := h.cachedInstall(key, force); done {
		return err
	}
	ictx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	var res api.BackendInstallResult
	err := h.Call(ictx, node, api.CmdBackendInstall, api.BackendInstallArgs{Entry: e, Name: e.Name}, &res)
	if ctx.Err() != nil || deyerr.HasCode(err, deyerr.N003) || deyerr.HasCode(err, deyerr.N004) {
		return err
	}
	h.rememberInstall(key, err)
	if err != nil {
		h.log.Warn("backend installation on a node failed", dlog.Node(node), slog.String("backend", e.Name),
			slog.String("version", e.Version), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	return err
}

// forgetInstallFailures makes the next apply retry failed installations of
// the backends in names (nil = every backend): a tunnel restart, the
// 30-minute re-check.
func (h *Hub) forgetInstallFailures(names map[string]string) {
	h.tun.instMu.Lock()
	defer h.tun.instMu.Unlock()
	for k, e := range h.tun.installed {
		if e.err == nil {
			continue
		}
		_, rest, _ := strings.Cut(k, "|")
		name, _, _ := strings.Cut(rest, "@")
		if _, ok := names[name]; ok || names == nil {
			delete(h.tun.installed, k)
		}
	}
}

// backendByName returns the registered backend called name.
func backendByName(name string) (backend.Backend, bool) {
	for _, b := range backend.All() {
		if b.Name() == name {
			return b, true
		}
	}
	return nil, false
}

// manifestOf returns the manifest entry of a backend (Name filled in).
func manifestOf(name string) (backend.ManifestEntry, bool) {
	b, ok := backendByName(name)
	if !ok {
		return backend.ManifestEntry{}, false
	}
	e := b.Manifest()
	if e.Name == "" {
		e.Name = name
	}
	return e, true
}
