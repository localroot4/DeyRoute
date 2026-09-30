package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Tunnel controller defaults and limits (sections 6, 7, 8, 9, 10).
const (
	// DefaultTunnelUpWait is how long TunnelAdd waits for a new tunnel to
	// come up before it reports the last error (the tunnel stays configured).
	DefaultTunnelUpWait = 60 * time.Second
	// DefaultRecheckInterval re-tests skipped rungs and UDP reachability
	// (sections 8 and 10: every 30 minutes).
	DefaultRecheckInterval = 30 * time.Minute
	// DefaultReportInterval probes every port map for reporting only
	// (section 9: every 60 seconds).
	DefaultReportInterval = health.AllPortsInterval
	// DefaultUnitStartCheck is how long a started hub unit runs before its
	// state is read (a backend that crashes at once is DEY-B003).
	DefaultUnitStartCheck = 300 * time.Millisecond
	// unitStartWatch bounds how long an "activating" hub unit is watched.
	unitStartWatch = 3 * time.Second
	// unitStartPoll is the state poll interval while a unit is activating.
	unitStartPoll = 100 * time.Millisecond
	// installTimeout bounds one backend installation (download included).
	installTimeout = 10 * time.Minute
	// nodeCmdTimeout bounds one node command of the tunnel controller.
	nodeCmdTimeout = 30 * time.Second
	// plainCheckTTL is how long the node's verdict "the target closes
	// without speaking TLS" is kept (AcceptCleanClose of the path probe).
	plainCheckTTL = 5 * time.Minute
	// stopTimeout bounds stopping the units of a tunnel.
	stopTimeout = time.Minute
	// netIndexMax is the largest per-tunnel network index (10.77.<n>.0/30).
	netIndexMax = 255
	// portsProbeNode is the node element of the probe history of the
	// all-ports report ("<tunnel>/*/<listen>/<proto>"); it is never a node id.
	portsProbeNode = "*"
	// metaCanaryEcho + tunnel remembers the canary echo port on the node.
	metaCanaryEcho = "canary-echo/"
	// ProbeKindPath is the Kind of path probe samples (state.ProbeSample).
	ProbeKindPath = "path"
	// nodeSyncQueue bounds the pending node cleanups (first heartbeats).
	nodeSyncQueue = 64
)

// tunnelCtl is the controller of one enabled tunnel (ARCHITECTURE.md
// §7.3): it plans the warm set (every rung on every node, both sides),
// installs the backends, renders them, keeps the skipped rungs of section 8
// up to date, runs the failover engine with the real Actions (engine.go of
// internal/failover) and the periodic jobs (30-minute re-check, 60-second
// all-ports report, canary).
type tunnelCtl struct {
	h  *Hub
	id string

	// applyMu serialises planning, installing and rendering.
	applyMu sync.Mutex

	mu      sync.Mutex
	tun     config.Tunnel
	plan    render.TunnelPlan
	planned bool
	problem string                 // why the tunnel cannot be planned (dashboard warning)
	started map[string]render.Side // candidate key → hub side, while started
	sent    map[string]string      // instance → hash of the payload its node has
	pending map[string]bool        // nodes that were offline during the last apply
	fresh   map[string]bool        // nodes that attached since the last sync
	svc     map[string]svcEntry    // node_service cache
	plain   map[string]plainEntry  // node verdict: the target closes without TLS
	lastErr error                  // last start or probe failure (TunnelAdd)
	first   chan error             // the result of the first Start
	firstOK bool
	warned  map[string]bool // plan warnings already reported
	can     canaryState
	// canWork is held while the canary is set up or taken down, so diag
	// speed can borrow the canary slot (ops_diag.go).
	canWork sync.Mutex

	engine  *failover.Engine
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped bool

	kick    chan struct{}
	canKick chan struct{}
}

type svcEntry struct {
	up, known bool
	at        time.Time
}

type plainEntry struct {
	closes bool
	at     time.Time
}

func newTunnelCtl(h *Hub, id string) *tunnelCtl {
	return &tunnelCtl{
		h: h, id: id,
		started: map[string]render.Side{},
		sent:    map[string]string{},
		pending: map[string]bool{},
		fresh:   map[string]bool{},
		svc:     map[string]svcEntry{},
		plain:   map[string]plainEntry{},
		warned:  map[string]bool{},
		first:   make(chan error, 1),
		kick:    make(chan struct{}, 1),
		canKick: make(chan struct{}, 1),
	}
}

// ---------------------------------------------------------------- accessors

// snapshot returns the tunnel configuration and the plan in use. The plan
// is replaced as a whole on every apply and never modified, so it can be
// read without the lock.
func (c *tunnelCtl) snapshot() (config.Tunnel, render.TunnelPlan) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tun.Clone(), c.plan
}

// candidate returns the planned candidate sc.
func (c *tunnelCtl) candidate(sc state.Candidate) (render.Candidate, bool) {
	_, plan := c.snapshot()
	pc, ok := plan.Candidate(sc.Node, sc.Transport)
	if !ok {
		return render.Candidate{}, false
	}
	return *pc, true
}

// eng returns the failover engine (nil before launch).
func (c *tunnelCtl) eng() *failover.Engine {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.engine
}

// liveState returns the engine's current state.
func (c *tunnelCtl) liveState() (state.TunnelState, bool) {
	e := c.eng()
	if e == nil {
		return state.TunnelState{}, false
	}
	return e.State(), true
}

// engineState is liveState without the flag.
func (c *tunnelCtl) engineState() state.TunnelState {
	ts, _ := c.liveState()
	return ts
}

// usesNode reports whether node is one of the tunnel's nodes.
func (c *tunnelCtl) usesNode(node string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.tun.Nodes, node)
}

// setProblem records why the tunnel cannot be planned (nil clears it).
func (c *tunnelCtl) setProblem(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.problem = ""
		return
	}
	e := deyerr.As(err)
	c.problem = string(e.Code) + " " + e.Message()
}

// warnings are the controller's dashboard lines for the tunnel.
func (c *tunnelCtl) warnings() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	if c.problem != "" {
		out = append(out, c.problem)
	}
	for _, n := range sortedKeys(c.pending) {
		if c.pending[n] {
			out = append(out, "node "+n+" is offline: its rungs are rendered when it reconnects")
		}
	}
	return out
}

// markStarted records that sc has been started (its hub side feeds the
// firewall NAT rules and Stop).
func (c *tunnelCtl) markStarted(sc state.Candidate) {
	_, plan := c.snapshot()
	var side render.Side
	if pc, ok := plan.Candidate(sc.Node, sc.Transport); ok {
		side = pc.Hub
	}
	c.mu.Lock()
	c.started[sc.Key()] = side
	c.mu.Unlock()
}

// markStopped forgets sc and returns its hub side (and whether it was
// started).
func (c *tunnelCtl) markStopped(sc state.Candidate) (render.Side, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	side, ok := c.started[sc.Key()]
	delete(c.started, sc.Key())
	return side, ok
}

// isStarted reports whether sc is started.
func (c *tunnelCtl) isStarted(sc state.Candidate) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.started[sc.Key()]
	return ok
}

// startedSides returns the hub sides of the started candidates and of a
// running canary (activeHubSides).
func (c *tunnelCtl) startedSides() []render.Side {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]render.Side, 0, len(c.started)+1)
	for _, k := range sortedKeys(c.started) {
		out = append(out, c.started[k])
	}
	if c.can.ready && c.can.cand != nil {
		out = append(out, c.can.cand.Hub)
	}
	return out
}

// startedInstances returns the instance names of the started candidates:
// they stay until the engine moved away, even when config.yaml dropped
// their rung or node.
func (c *tunnelCtl) startedInstances() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.started))
	for _, k := range sortedKeys(c.started) {
		node, tr, ok := strings.Cut(k, "/")
		if ok {
			out = append(out, systemd.InstanceName(c.id, node, tr))
		}
	}
	return out
}

// kickLoop wakes the controller goroutine (a node attached).
func (c *tunnelCtl) kickLoop() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// nodeAttached forgets what node has (it may have rebooted) and makes the
// controller sync it.
func (c *tunnelCtl) nodeAttached(node string) {
	prefix := c.id + "." + node + "."
	c.mu.Lock()
	for inst := range c.sent {
		if strings.HasPrefix(inst, prefix) {
			delete(c.sent, inst)
		}
	}
	c.fresh[node] = true
	if len(c.tun.Nodes) > 0 && c.tun.Nodes[0] == node {
		// The canary's node side and echo are set up again.
		c.can.ready = false
	}
	c.mu.Unlock()
	c.kickLoop()
}

// ---------------------------------------------------------------- planning

// planInput is the planner input of tunnel t (ARCHITECTURE.md §7.1): paths
// are system paths (the writer adds Options.Root), control ports are stable
// per (tunnel, node, transport) and skip ports some process holds on the
// hub for rungs whose hub side binds them.
func (h *Hub) planInput(cfg *config.Config, t config.Tunnel) render.Input {
	nodes := make(map[string]config.Node, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		nodes[n.ID] = n
	}
	return render.Input{
		Cfg:        cfg,
		Tunnel:     t,
		Hub:        cfg.Hub.Info(),
		Nodes:      nodes,
		CtlPort:    h.allocCtlPort,
		NetIndex:   func(tunnel string) (int, error) { return h.st.AllocNetIndex(tunnel, netIndexMax) },
		Secrets:    h.secretStore(),
		Registry:   hubRegistry{h},
		Layout:     install.Layout{Root: "/"},
		Decoy:      h.currentDecoy(cfg),
		FirstRun:   h.firstRun,
		CAPEM:      h.tunnelCAPEM(),
		UDPBlocked: h.udpBlocked,
	}
}

// allocCtlPort allocates the stable control port of key. Ports another
// process holds on the hub are skipped for keys whose hub side binds the
// port (reverse transports and the canary).
func (h *Hub) allocCtlPort(key string) (int, error) {
	hubBinds := true
	if _, node, tr, ok := state.SplitKey(key); ok && node != render.CanaryDirName {
		if _, t, err := backend.Lookup(tr); err == nil {
			hubBinds = t.Direction == backend.Reverse
		}
	}
	busy := func(port int) bool {
		if !hubBinds {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return !h.o.BindCheck(ctx, port, config.ProtoTCP).Free || !h.o.BindCheck(ctx, port, config.ProtoUDP).Free
	}
	return h.st.AllocCtlPort(key, config.CtlRangeLow, config.CtlRangeHigh, busy)
}

// firstRun reports whether an instance was never rendered on the hub (its
// first start logs verbosely, Waterwall).
func (h *Hub) firstRun(instance string) bool {
	in, err := systemd.ParseInstance(instance)
	if err != nil {
		return false
	}
	dir, ok := configDirOf(in)
	if !ok {
		return false
	}
	_, err = os.Stat(h.path(dir))
	return stderrors.Is(err, os.ErrNotExist)
}

// udpBlocked reports that the last UDP echo probe to node failed.
func (h *Hub) udpBlocked(node string) bool {
	ns, _ := h.nodeState(node)
	return ns.UDPOK != nil && !*ns.UDPOK
}

// udpRung returns the first rung of t that needs UDP between hub and node
// ("" when none does).
func udpRung(cfg *config.Config, t *config.Tunnel) string {
	ladder, err := cfg.ResolveLadder(t, supports)
	if err != nil {
		return ""
	}
	for _, r := range ladder {
		if _, tr, err := backend.Lookup(r); err == nil && tr.NeedsUDP {
			return r
		}
	}
	return ""
}

// ladderOf returns the resolved ladder of t (nil when it has none).
func ladderOf(cfg *config.Config, t *config.Tunnel) []string {
	ladder, err := cfg.ResolveLadder(t, supports)
	if err != nil {
		return nil
	}
	return ladder
}

// ensureUDP runs the UDP reachability probe of section 10 against node
// when its last result is older than notBefore (RecheckInterval ago
// normally, the start of a re-check when forced): the node opens a UDP
// echo on the control port of a UDP rung (probe.udp_listen) and the hub
// sends 3 nonce packets (health.UDPEcho). When that port is busy (the rung
// runs) a free port of the control range is used. The result is kept in
// the node's state (UDPOK); an offline node keeps its last result.
func (h *Hub) ensureUDP(ctx context.Context, tunnel, node, rung string, notBefore time.Time) {
	fresh := func() bool {
		ns, _ := h.nodeState(node)
		return !ns.UDPCheckedAt.IsZero() && !ns.UDPCheckedAt.Before(notBefore)
	}
	if !h.Online(node) || fresh() {
		return
	}
	h.tun.udpMu.Lock()
	defer h.tun.udpMu.Unlock()
	if fresh() {
		return
	}
	n, ok := h.Config().NodeByID(node)
	if !ok {
		return
	}
	ns, _ := h.nodeState(node)
	host := firstNonEmpty(n.PublicIP, ns.RemoteIP)
	tested, udpOK := false, false
	if port, err := h.allocCtlPort(state.Key(tunnel, node, rung)); err == nil {
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		err := h.Call(cctx, node, api.CmdProbeUDPListen, api.UDPListenArgs{Port: port, Seconds: udpListenSeconds}, nil)
		cancel()
		if err == nil {
			r := health.UDPEcho(ctx, net.JoinHostPort(host, strconv.Itoa(port)), health.UDPTries, h.o.UDPProbeTimeout)
			tested, udpOK = true, r.OK
		}
	}
	if !tested {
		ok, _, err := h.udpTest(ctx, node, host)
		if err != nil {
			h.log.Info("UDP reachability of a node could not be tested", dlog.Node(node), dlog.Err(err))
			return
		}
		udpOK = ok
	}
	if ctx.Err() != nil {
		return
	}
	now := h.now()
	h.updateNode(node, func(ns *state.NodeState) {
		v := udpOK
		ns.UDPOK = &v
		ns.UDPCheckedAt = now
	})
	if !udpOK {
		h.log.Warn("UDP between the hub and a node is blocked; rungs that need UDP are skipped", dlog.Node(node), dlog.Code(deyerr.P015))
	}
}

// applyOpts tune apply.
type applyOpts struct {
	rep          *steps // progress (TunnelAdd, TunnelEdit…); nil = none
	add          bool   // no online node of the tunnel is a failed step (N003)
	force        bool   // retry failed installations and the UDP probe now
	only         string // node work only on this node (TunnelBackupAdd); "" = all
	quietInstall bool   // do not report the install steps (port changes)
}

// applyResult is what apply found.
type applyResult struct {
	skips       map[string]state.Skip // every skipped candidate (key node/transport)
	changedHub  map[string]bool       // hub instance → its files or drop-in changed
	changedNode map[string]bool       // instance → its node payload changed
	stale       []render.Candidate    // planned before, not any more
	fatal       error                 // the failed step (reported, not fatal for the controller)
}

// apply plans the tunnel from the current config.yaml and makes the hub
// and the nodes match the plan: UDP probe (when a rung needs UDP), backend
// installation on the hub and on every online node, rendering of every
// candidate (hub files + drop-ins with one daemon-reload, backend.render to
// the nodes), and the skip set of section 8 (validation failures, UDP
// blocked, failed installations and renders). Progress goes to o.rep as the
// section 6 steps. It returns an error only when the tunnel cannot be
// planned at all.
func (c *tunnelCtl) apply(ctx context.Context, o applyOpts) (applyResult, error) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()
	h := c.h
	res := applyResult{skips: map[string]state.Skip{}, changedHub: map[string]bool{}, changedNode: map[string]bool{}}
	cfg := h.Config()
	tp, ok := cfg.Tunnel(c.id)
	if !ok {
		return res, deyerr.New(deyerr.C021, deyerr.Params{"tunnel": c.id})
	}
	t := tp.Clone()
	c.mu.Lock()
	c.tun = t.Clone()
	c.mu.Unlock()
	if rung := udpRung(cfg, &t); rung != "" {
		notBefore := h.now().Add(-h.o.RecheckInterval)
		if o.force {
			notBefore = h.now()
		}
		for _, n := range t.Nodes {
			if o.only == "" || n == o.only {
				h.ensureUDP(ctx, t.ID, n, rung, notBefore)
			}
		}
	}
	plan, err := render.Plan(h.planInput(cfg, t))
	if err != nil {
		return res, err
	}
	c.reportPlanWarnings(plan)
	rep := o.rep
	failed := func(err error) {
		if res.fatal == nil {
			res.fatal = err
			rep = nil // later steps are not reported after a failed one
		}
	}
	installRep := func() *steps {
		if o.quietInstall {
			return nil
		}
		return rep
	}
	backends := sortedKeys(plan.Backends)

	hubFail := map[string]error{}
	if err := installRep().runWarn(stepInstallHub, func() (string, error, error) {
		return c.installOnHub(ctx, plan, backends, o.force, hubFail)
	}); err != nil {
		failed(err)
	}

	nodeFail := map[string]map[string]error{}
	pending := map[string]bool{}
	if err := installRep().runWarn(stepInstallNode, func() (string, error, error) {
		return c.installOnNodes(ctx, t, backends, o, hubFail, nodeFail, pending)
	}); err != nil {
		failed(err)
	}

	renderFail := map[string]error{}
	if err := rep.runWarn(stepRender, func() (string, error, error) {
		return c.renderAll(ctx, t, &plan, o, pending, renderFail, &res)
	}); err != nil {
		failed(err)
	}

	recheck := h.now().Add(h.o.RecheckInterval)
	for _, s := range plan.Skipped {
		res.skips[state.Candidate{Node: s.Node, Transport: s.TransportID}.Key()] = state.Skip{
			Reason: s.Reason, Code: string(s.Code), RecheckAt: recheck,
		}
	}
	for i := range plan.Candidates {
		pc := &plan.Candidates[i]
		key := pc.StateCandidate().Key()
		switch {
		case hubFail[pc.Backend] != nil:
			res.skips[key] = installSkip(pc, "the hub", hubFail[pc.Backend], recheck)
		case nodeFail[pc.Node][pc.Backend] != nil:
			res.skips[key] = installSkip(pc, "node "+pc.Node, nodeFail[pc.Node][pc.Backend], recheck)
		case renderFail[key] != nil:
			e := deyerr.As(renderFail[key])
			res.skips[key] = state.Skip{Reason: reasonOf(e), Code: string(e.Code), RecheckAt: recheck}
		}
	}

	c.mu.Lock()
	old, hadOld := c.plan, c.planned
	c.plan, c.planned, c.problem = plan, true, ""
	for n := range c.pending {
		if o.only == "" || n == o.only {
			delete(c.pending, n)
		}
	}
	for n := range pending {
		c.pending[n] = true
	}
	c.mu.Unlock()
	if hadOld {
		for _, oc := range old.Candidates {
			nc, ok := plan.Candidate(oc.Node, oc.TransportID)
			if !ok {
				res.stale = append(res.stale, oc)
				continue
			}
			if payloadHash(c.id, oc.NodeSide) != payloadHash(c.id, nc.NodeSide) {
				res.changedNode[nc.NodeSide.Instance] = true
			}
		}
	}
	return res, nil
}

// installSkip is the skip entry of a candidate whose backend could not be
// installed (section 8: only that backend's rungs are skipped; DEY-B001).
func installSkip(pc *render.Candidate, where string, err error, recheck time.Time) state.Skip {
	e := deyerr.As(err)
	return state.Skip{
		Reason:    fmt.Sprintf("%s %s cannot be installed on %s: %s", pc.Backend, pc.Version, where, reasonOf(e)),
		Code:      string(deyerr.B001),
		RecheckAt: recheck,
	}
}

// reasonOf is the one-line reason of a DEY error (message and why).
func reasonOf(e *deyerr.Error) string {
	s := e.Message()
	if w := e.Why(); w != "" {
		s += ": " + w
	}
	return s
}

// usableCandidates counts the candidates whose backend is on the hub.
func usableCandidates(plan *render.TunnelPlan, hubFail map[string]error) int {
	n := 0
	for i := range plan.Candidates {
		if hubFail[plan.Candidates[i].Backend] == nil {
			n++
		}
	}
	return n
}

// installOnHub installs every backend of the plan on the hub.
func (c *tunnelCtl) installOnHub(ctx context.Context, plan render.TunnelPlan, backends []string, force bool, hubFail map[string]error) (string, error, error) {
	var parts, failed []string
	var first error
	for _, name := range backends {
		e, ok := c.h.backendEntry(name)
		if !ok {
			continue
		}
		if e.Builtin || e.System {
			parts = append(parts, name+" (built in)")
			continue
		}
		if err := c.h.installHub(ctx, e, force); err != nil {
			hubFail[name] = err
			failed = append(failed, name)
			if first == nil {
				first = err
			}
			continue
		}
		parts = append(parts, name+" "+e.Version)
	}
	detail := strings.Join(parts, ", ")
	if first == nil {
		return detail, nil, nil
	}
	if detail != "" {
		detail += "; "
	}
	detail += "not installed: " + strings.Join(failed, ", ")
	if len(plan.Candidates) > 0 && usableCandidates(&plan, hubFail) == 0 {
		return detail, nil, first
	}
	return detail, first, nil
}

// installOnNodes installs every backend of the plan on the online nodes of
// t (those the hub could install); offline nodes become pending.
func (c *tunnelCtl) installOnNodes(ctx context.Context, t config.Tunnel, backends []string, o applyOpts,
	hubFail map[string]error, nodeFail map[string]map[string]error, pending map[string]bool) (string, error, error) {
	var parts []string
	var warn, offline error
	online, wanted := 0, 0
	for _, n := range t.Nodes {
		if o.only != "" && n != o.only {
			continue
		}
		wanted++
		if !c.h.Online(n) {
			pending[n] = true
			parts = append(parts, n+" offline")
			if offline == nil {
				offline = deyerr.New(deyerr.N003, deyerr.Params{"node": n})
			}
			continue
		}
		online++
		ok := true
		for _, name := range backends {
			e, found := c.h.backendEntry(name)
			if !found || e.Builtin || e.System || hubFail[name] != nil {
				continue
			}
			err := c.h.installNode(ctx, n, e, o.force)
			if err == nil {
				continue
			}
			if deyerr.HasCode(err, deyerr.N003) || deyerr.HasCode(err, deyerr.N004) {
				pending[n] = true
				warn, ok = err, false
				break
			}
			if nodeFail[n] == nil {
				nodeFail[n] = map[string]error{}
			}
			nodeFail[n][name] = err
			parts = append(parts, n+": "+name+" not installed")
			warn, ok = err, false
		}
		if ok {
			parts = append(parts, n)
		}
	}
	detail := strings.Join(parts, ", ")
	if wanted > 0 && online == 0 {
		if o.add {
			return detail, nil, offline
		}
		return detail, offline, nil
	}
	if warn == nil {
		warn = offline
	}
	return detail, warn, nil
}

// renderAll writes every candidate's hub side (one daemon-reload when
// something changed) and sends every node side to its online node.
func (c *tunnelCtl) renderAll(ctx context.Context, t config.Tunnel, plan *render.TunnelPlan, o applyOpts,
	pending map[string]bool, renderFail map[string]error, res *applyResult) (string, error, error) {
	h := c.h
	var warn error
	hubChanged, hubOK := false, 0
	w := h.hubWriter()
	for i := range plan.Candidates {
		pc := &plan.Candidates[i]
		changed, err := w.Write(ctx, pc.Hub)
		if err != nil {
			renderFail[pc.StateCandidate().Key()] = err
			warn = err
			continue
		}
		hubOK++
		if changed {
			res.changedHub[pc.Hub.Instance] = true
			hubChanged = true
		}
	}
	if hubChanged {
		if err := h.o.Systemd.DaemonReload(ctx); err != nil {
			return "", nil, err
		}
	}
	nodeOK := 0
	for _, n := range t.Nodes {
		if (o.only != "" && n != o.only) || pending[n] {
			continue
		}
		if !h.Online(n) {
			pending[n] = true
			continue
		}
		for i := range plan.Candidates {
			pc := &plan.Candidates[i]
			if pc.Node != n || renderFail[pc.StateCandidate().Key()] != nil {
				continue
			}
			err := c.renderOnNode(ctx, pc)
			if err == nil {
				nodeOK++
				continue
			}
			if deyerr.HasCode(err, deyerr.N003) || deyerr.HasCode(err, deyerr.N004) {
				pending[n] = true
				warn = err
				break
			}
			renderFail[pc.StateCandidate().Key()] = err
			warn = err
		}
	}
	detail := fmt.Sprintf("%d rungs on the hub, %d on nodes", hubOK, nodeOK)
	if len(plan.Candidates) > 0 && len(renderFail) == len(plan.Candidates) {
		return detail, nil, warn
	}
	if len(plan.Candidates) == 0 {
		return "no usable rung", nil, deyerr.New(deyerr.F001, deyerr.Params{"tunnel": t.ID}).
			WithDetail(skippedDetail(plan.Skipped))
	}
	return detail, warn, nil
}

// skippedDetail lists the skipped rungs of a plan.
func skippedDetail(skips []render.SkippedCandidate) string {
	lines := make([]string, 0, len(skips))
	for _, s := range skips {
		lines = append(lines, s.TransportID+" on "+s.Node+": "+s.Reason)
	}
	return strings.Join(lines, "\n")
}

// payloadHash identifies the content of a node side.
func payloadHash(tunnel string, s render.Side) string {
	data, err := json.Marshal(render.NodePayload(tunnel, s))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// renderOnNode sends the node side of pc (backend.render) unless the node
// already has exactly this payload.
func (c *tunnelCtl) renderOnNode(ctx context.Context, pc *render.Candidate) error {
	inst := pc.NodeSide.Instance
	sum := payloadHash(c.id, pc.NodeSide)
	c.mu.Lock()
	have := c.sent[inst]
	c.mu.Unlock()
	if have == sum && sum != "" {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	defer cancel()
	if err := c.h.Call(cctx, pc.Node, api.CmdBackendRender, render.NodePayload(c.id, pc.NodeSide), nil); err != nil {
		return err
	}
	c.mu.Lock()
	c.sent[inst] = sum
	c.mu.Unlock()
	return nil
}

// reportPlanWarnings turns plan warnings into events once per controller:
// DEY-T003 (ACME failed, the tunnel fell back to auto) is acme_failed.
func (c *tunnelCtl) reportPlanWarnings(plan render.TunnelPlan) {
	for _, w := range plan.Warnings {
		e := deyerr.As(w)
		c.mu.Lock()
		seen := c.warned[string(e.Code)]
		c.warned[string(e.Code)] = true
		c.mu.Unlock()
		if seen {
			continue
		}
		if e.Code == deyerr.T003 {
			c.h.Emit(state.Event{Type: state.EvACMEFailed, Level: state.LevelWarn, Tunnel: c.id,
				Code: string(e.Code), Reason: e.Why(), Message: "Tunnel " + c.id + ": " + e.Message() + "; using the internal certificate"})
			continue
		}
		c.h.log.Warn("tunnel TLS warning", dlog.Tunnel(c.id), dlog.Code(e.Code), dlog.Err(e))
	}
}

// ---------------------------------------------------------------- skips

// skipKeyCandidate parses a skip key.
func skipKeyCandidate(key string) (state.Candidate, bool) {
	sc, err := parseCandidateKey(key)
	return sc, err == nil
}

// emitSkip reports a newly skipped rung (yellow warning, section 8).
func (c *tunnelCtl) emitSkip(key string, s state.Skip) {
	sc, _ := skipKeyCandidate(key)
	c.h.Emit(state.Event{
		Type: state.EvRungSkipped, Level: state.LevelWarn, Tunnel: c.id, Node: sc.Node,
		ToTransport: sc.Transport, Code: s.Code, Reason: s.Reason,
		Message: fmt.Sprintf("Tunnel %s: %s on %s is skipped: %s", c.id, sc.Transport, sc.Node, s.Reason),
	})
}

// emitRestored reports a rung that passed its re-check.
func (c *tunnelCtl) emitRestored(key string) {
	sc, _ := skipKeyCandidate(key)
	c.h.Emit(state.Event{
		Type: state.EvRungRestored, Level: state.LevelInfo, Tunnel: c.id, Node: sc.Node, ToTransport: sc.Transport,
		Message: fmt.Sprintf("Tunnel %s: %s on %s is back in the ladder", c.id, sc.Transport, sc.Node),
	})
}

// sameSkip reports whether a known skip may stay as it is: same code and
// its re-check is not due yet.
func (c *tunnelCtl) sameSkip(old, s state.Skip) bool {
	return old.Code == s.Code && c.h.now().Before(old.RecheckAt)
}

// mergeSkips computes the skip set a new engine starts with from the
// persisted one (cur) and the wanted one, with rung_skipped / rung_restored
// events for the differences.
func (c *tunnelCtl) mergeSkips(cur, want map[string]state.Skip) map[string]state.Skip {
	out := make(map[string]state.Skip, len(want))
	for _, k := range sortedKeys(want) {
		s := want[k]
		old, ok := cur[k]
		switch {
		case !ok:
			c.emitSkip(k, s)
		case c.sameSkip(old, s):
			s.RecheckAt = old.RecheckAt
		}
		out[k] = s
	}
	for _, k := range sortedKeys(cur) {
		if _, ok := want[k]; !ok {
			c.emitRestored(k)
		}
	}
	return out
}

// applySkipDiff brings the engine's skip set to want (SetSkipped /
// ClearSkipped, with events).
func (c *tunnelCtl) applySkipDiff(ctx context.Context, eng *failover.Engine, want map[string]state.Skip) error {
	cur := eng.State().Skipped
	var errs []error
	for _, k := range sortedKeys(want) {
		s := want[k]
		old, ok := cur[k]
		if ok && c.sameSkip(old, s) {
			continue
		}
		sc, valid := skipKeyCandidate(k)
		if !valid {
			continue
		}
		if err := eng.SetSkipped(ctx, sc, s); err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			c.emitSkip(k, s)
		}
	}
	for _, k := range sortedKeys(cur) {
		if _, ok := want[k]; ok {
			continue
		}
		sc, valid := skipKeyCandidate(k)
		if !valid {
			continue
		}
		if err := eng.ClearSkipped(ctx, sc); err != nil {
			errs = append(errs, err)
			continue
		}
		c.emitRestored(k)
	}
	return stderrors.Join(errs...)
}

// ---------------------------------------------------------------- engine

// neverQuarantine is the backend catalog's NeverQuarantine flag.
func neverQuarantine(id string) bool {
	_, tr, err := backend.Lookup(id)
	return err == nil && tr.NeverQuarantine
}

// engineTunnel is the failover engine's view of the tunnel.
func engineTunnel(t config.Tunnel, plan render.TunnelPlan) failover.Tunnel {
	pm, _ := t.ProbeTarget()
	return failover.Tunnel{
		ID:              t.ID,
		Nodes:           append([]string(nil), t.Nodes...),
		Ladder:          append([]string(nil), plan.Ladder...),
		Settings:        t.Failover,
		NeverQuarantine: neverQuarantine,
		ServiceTarget:   pm.Target,
	}
}

// runningUnits maps the candidates of a tunnel whose units run to true:
// onHub from systemctl on the hub, anywhere adds the nodes' units from
// their last heartbeat. Only a candidate whose hub side runs is adopted
// after a restart (the hub side is always needed: the reverse server, the
// forward client or the WireGuard interface); a candidate that runs only
// on a node is started again, which is harmless for the running side.
func (h *Hub) runningUnits(ctx context.Context, tunnel string, nodes []string) (onHub, anywhere map[string]bool) {
	onHub, anywhere = map[string]bool{}, map[string]bool{}
	if list, err := h.o.Systemd.ListInstances(ctx); err == nil {
		for _, u := range list {
			in, err := systemd.ParseInstance(u.Instance)
			if err != nil || in.Canary || in.Tunnel != tunnel || !unitRunning(u.ActiveState) {
				continue
			}
			key := state.Candidate{Node: in.Node, Transport: in.Transport}.Key()
			onHub[key], anywhere[key] = true, true
		}
	} else {
		h.log.Warn("cannot list the tunnel units", dlog.Tunnel(tunnel), dlog.Err(err))
	}
	for _, n := range nodes {
		ns, _ := h.nodeState(n)
		for unit, st := range ns.Units {
			inst, ok := systemd.InstanceOf(unit)
			if !ok || !unitRunning(st) {
				continue
			}
			in, err := systemd.ParseInstance(inst)
			if err != nil || in.Canary || in.Tunnel != tunnel || in.Node != n {
				continue
			}
			anywhere[state.Candidate{Node: in.Node, Transport: in.Transport}.Key()] = true
		}
	}
	return onHub, anywhere
}

// canaryRunning reports whether the tunnel's canary unit runs on the hub.
func (h *Hub) canaryRunning(ctx context.Context, tunnel string) bool {
	st, err := h.o.Systemd.Show(ctx, systemd.UnitName(systemd.CanaryInstance(tunnel)))
	return err == nil && unitRunning(st.ActiveState)
}

// launch starts the failover engine of the tunnel from its persisted state
// reconciled with the units that actually run (section 9: after a hub
// restart a running unit is adopted, never restarted; other running
// candidates are stopped) and the controller goroutine.
func (c *tunnelCtl) launch(parent context.Context, skips map[string]state.Skip) {
	h := c.h
	ctx, cancel := context.WithCancel(parent)
	t, plan := c.snapshot()
	persisted, ok, err := h.st.GetTunnel(c.id)
	if err != nil {
		h.log.Warn("cannot read the tunnel state; starting fresh", dlog.Tunnel(c.id), dlog.Err(err))
	}
	if !ok {
		persisted = state.TunnelState{ID: c.id}
	}
	if persisted.State == state.StateDisabled {
		persisted.State = ""
	}
	running, anywhere := h.runningUnits(ctx, c.id, t.Nodes)
	ft := engineTunnel(t, plan)
	st := failover.Reconcile(persisted, running, ft)
	for _, sc := range failover.StrayUnits(st, anywhere) {
		h.log.Info("stopping a tunnel unit that must not run", dlog.Tunnel(c.id), dlog.Node(sc.Node), dlog.Transport(sc.Transport))
		if err := c.stopCandidate(ctx, sc); err != nil {
			h.log.Warn("cannot stop a stray tunnel unit", dlog.Tunnel(c.id), dlog.Err(err))
		}
	}
	if running[st.Active.Key()] {
		c.markStarted(st.Active)
		h.log.Info("running tunnel unit adopted", dlog.Tunnel(c.id), dlog.Node(st.Active.Node), dlog.Transport(st.Active.Transport))
	}
	if h.canaryRunning(ctx, c.id) {
		// The engine asks for the canary again when it needs it.
		c.stopCanaryUnits(ctx)
	}
	st.Skipped = c.mergeSkips(st.Skipped, skips)
	eng := failover.NewEngine(ft, engineActions{c}, h.o.FailoverClock, st)
	c.mu.Lock()
	c.engine, c.cancel = eng, cancel
	c.mu.Unlock()
	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		_ = eng.Run(ctx)
	}()
	go func() {
		defer c.wg.Done()
		c.loop(ctx)
	}()
	h.log.Info("tunnel controller started", dlog.Tunnel(c.id), slog.String("state", st.State),
		slog.String("active", st.Active.Key()), slog.Int("skipped", len(st.Skipped)))
}

// halt stops the engine and the controller goroutine; the units keep
// running (hub shutdown, StopAll).
func (c *tunnelCtl) halt() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
}

// stop halts the controller and, with units, stops every started
// candidate and the canary (disable, delete, restart).
func (c *tunnelCtl) stop(ctx context.Context, units bool) {
	c.halt()
	if !units {
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopTimeout)
	defer cancel()
	c.mu.Lock()
	keys := sortedKeys(c.started)
	c.mu.Unlock()
	for _, k := range keys {
		sc, err := parseCandidateKey(k)
		if err != nil {
			continue
		}
		if err := c.stopCandidate(sctx, sc); err != nil {
			c.h.log.Warn("cannot stop a tunnel unit", dlog.Tunnel(c.id), dlog.Node(sc.Node), dlog.Transport(sc.Transport), dlog.Err(err))
		}
	}
	c.canaryDown(sctx)
}

// updateOpts tune update.
type updateOpts struct {
	rep           *steps
	restartActive bool   // restart the active candidate when its files changed
	force         bool   // retry failed installations and the UDP probe now
	only          string // node work only on this node
	add           bool   // no online node is a failed step
	quietInstall  bool   // do not report the install steps
}

// update re-plans a running tunnel after a change (config, node attached,
// re-check): apply, then the skip set, the engine's tunnel view (ladder,
// nodes, settings: an active candidate that is gone moves to rung 1), a
// restart of the active candidate when its rendered files changed (port
// changes: only the active transport restarts, section 10), removal of the
// candidates no longer planned and a canary that no longer matches rung 1.
func (c *tunnelCtl) update(ctx context.Context, o updateOpts) error {
	res, err := c.apply(ctx, applyOpts{rep: o.rep, force: o.force, only: o.only, add: o.add, quietInstall: o.quietInstall})
	if err != nil {
		c.setProblem(err)
		return err
	}
	eng := c.eng()
	if eng == nil {
		return res.fatal
	}
	t, plan := c.snapshot()
	if err := c.applySkipDiff(ctx, eng, res.skips); err != nil && ctx.Err() == nil {
		c.h.log.Warn("cannot update the skipped rungs", dlog.Tunnel(c.id), dlog.Err(err))
	}
	rep := o.rep
	if res.fatal != nil {
		rep = nil // steps after a failed one are not reported
	}
	if err := rep.run(stepEngine, func() (string, error) {
		if err := eng.UpdateConfig(ctx, engineTunnel(t, plan)); err != nil {
			return "", err
		}
		st := eng.State()
		return st.State + " " + st.Active.Key(), nil
	}); err != nil {
		c.h.log.Warn("failover engine did not accept the new configuration", dlog.Tunnel(c.id), dlog.Err(err))
		if res.fatal == nil {
			res.fatal = err
		}
	}
	if o.restartActive {
		c.restartChanged(ctx, eng, res, rep)
	}
	c.removeStaleCandidates(ctx, res.stale)
	c.checkCanary(ctx, t, plan)
	c.ensureRunningOnFresh(ctx, eng)
	return res.fatal
}

// restartChanged restarts the sides of the active candidate whose rendered
// files changed (server side first).
func (c *tunnelCtl) restartChanged(ctx context.Context, eng *failover.Engine, res applyResult, rep *steps) {
	active := eng.State().Active
	pc, ok := c.candidate(active)
	hubCh := ok && res.changedHub[pc.Hub.Instance]
	nodeCh := ok && res.changedNode[pc.NodeSide.Instance]
	if !c.isStarted(active) || (!hubCh && !nodeCh) {
		if rep != nil {
			rep.emit(api.Step{ID: stepRestart, Status: api.StepSkipped, Detail: "the active transport did not change"})
		}
		return
	}
	_ = rep.run(stepRestart, func() (string, error) {
		h := c.h
		restartHub := func() error {
			if !hubCh {
				return nil
			}
			unit := systemd.UnitName(pc.Hub.Instance)
			if err := h.o.Systemd.Restart(ctx, unit); err != nil {
				return h.unitFailed(unit, c.id, err)
			}
			return h.watchStart(ctx, unit, c.id)
		}
		restartNode := func() error {
			if !nodeCh {
				return nil
			}
			cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
			defer cancel()
			return h.Call(cctx, pc.Node, api.CmdUnitRestart, api.UnitArgs{Instance: pc.NodeSide.Instance}, nil)
		}
		first, second := restartHub, restartNode
		if pc.Transport.Direction.ServerSide() == backend.SideNode {
			first, second = restartNode, restartHub
		}
		if err := first(); err != nil {
			return "", err
		}
		if err := second(); err != nil {
			return "", err
		}
		h.log.Info("active transport restarted after a change", dlog.Tunnel(c.id), dlog.Node(pc.Node), dlog.Transport(pc.TransportID))
		return pc.TransportID + " on " + pc.Node, nil
	})
}

// removeStaleCandidates removes candidates that are no longer planned (not
// while they run: the engine moves away first).
func (c *tunnelCtl) removeStaleCandidates(ctx context.Context, stale []render.Candidate) {
	h := c.h
	for i := range stale {
		oc := &stale[i]
		sc := oc.StateCandidate()
		if c.isStarted(sc) {
			continue
		}
		if err := h.hubWriter().Remove(ctx, oc.Hub.Instance, oc.Hub.ConfigDir); err != nil {
			h.log.Warn("cannot remove a stale tunnel unit", dlog.Tunnel(c.id), slog.String("instance", oc.Hub.Instance), dlog.Err(err))
		}
		if h.Online(oc.Node) {
			cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
			err := h.Call(cctx, oc.Node, api.CmdBackendRemove, api.BackendRemoveArgs{Instance: oc.NodeSide.Instance, ConfigDir: oc.NodeSide.ConfigDir}, nil)
			cancel()
			if err != nil {
				h.log.Warn("cannot remove a stale tunnel unit on its node", dlog.Tunnel(c.id), dlog.Node(oc.Node), dlog.Err(err))
			}
		}
		c.mu.Lock()
		delete(c.sent, oc.NodeSide.Instance)
		c.mu.Unlock()
		if err := h.st.ReleaseCtlPort(state.Key(c.id, oc.Node, oc.TransportID)); err != nil {
			h.log.Warn("cannot release a control port", dlog.Tunnel(c.id), dlog.Err(err))
		}
	}
}

// ensureRunningOnFresh starts the node side of the active candidate again
// on nodes that attached since the last sync when it does not run there
// (the node rebooted while the hub kept the candidate).
func (c *tunnelCtl) ensureRunningOnFresh(ctx context.Context, eng *failover.Engine) {
	c.mu.Lock()
	fresh := c.fresh
	c.fresh = map[string]bool{}
	c.mu.Unlock()
	active := eng.State().Active
	if !fresh[active.Node] || !c.isStarted(active) {
		return
	}
	inst := systemd.InstanceName(c.id, active.Node, active.Transport)
	var us api.UnitStatus
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	defer cancel()
	if err := c.h.Call(cctx, active.Node, api.CmdUnitStatus, api.UnitArgs{Instance: inst}, &us); err != nil || unitRunning(us.ActiveState) {
		return
	}
	c.h.log.Info("starting the active tunnel unit again on a node that reconnected", dlog.Tunnel(c.id), dlog.Node(active.Node))
	if err := c.h.Call(cctx, active.Node, api.CmdUnitStart, api.UnitArgs{Instance: inst}, nil); err != nil {
		c.h.log.Warn("cannot start the active tunnel unit on its node", dlog.Tunnel(c.id), dlog.Node(active.Node), dlog.Err(err))
	}
}

// dropNode makes the tunnel leave node before it is removed: the engine
// forgets it (an active candidate there moves to the next node), the
// canary stops when node is the primary, and the node's instances go.
func (c *tunnelCtl) dropNode(ctx context.Context, node string) error {
	t, plan := c.snapshot()
	var nodes []string
	for _, n := range t.Nodes {
		if n != node {
			nodes = append(nodes, n)
		}
	}
	if eng := c.eng(); eng != nil && len(nodes) > 0 {
		ft := engineTunnel(t, plan)
		ft.Nodes = nodes
		if err := eng.UpdateConfig(ctx, ft); err != nil {
			return err
		}
	}
	if len(t.Nodes) > 0 && t.Nodes[0] == node {
		c.canaryDown(ctx)
	}
	for _, k := range func() []string { c.mu.Lock(); defer c.mu.Unlock(); return sortedKeys(c.started) }() {
		if sc, err := parseCandidateKey(k); err == nil && sc.Node == node {
			_ = c.stopCandidate(ctx, sc)
		}
	}
	c.mu.Lock()
	c.tun.Nodes = nodes
	delete(c.pending, node)
	c.mu.Unlock()
	return c.h.removeNodeCandidates(ctx, t.ID, node, plan.Ladder)
}

// removeNodeCandidates removes every rung of tunnel on node: on the node
// (when online), on the hub, and releases their control ports.
func (h *Hub) removeNodeCandidates(ctx context.Context, tunnel, node string, ladder []string) error {
	var errs []error
	for _, r := range ladder {
		inst := systemd.InstanceName(tunnel, node, r)
		in, err := systemd.ParseInstance(inst)
		if err != nil {
			continue
		}
		if h.Online(node) {
			if err := h.removeNodeInstance(ctx, node, in); err != nil {
				errs = append(errs, err)
			}
		}
		if err := h.removeHubInstance(ctx, inst); err != nil {
			errs = append(errs, err)
		}
	}
	if err := h.st.ReleaseCtlPorts(tunnel + "/" + node + "/"); err != nil {
		errs = append(errs, err)
	}
	return stderrors.Join(errs...)
}

// ---------------------------------------------------------------- loop

// loop is the controller goroutine: node syncs, the 30-minute re-check of
// skipped rungs (section 8), the 60-second all-ports report (section 9)
// and the canary (section 9, phase 8).
func (c *tunnelCtl) loop(ctx context.Context) {
	h := c.h
	recheck := time.NewTimer(h.o.RecheckInterval)
	report := time.NewTicker(h.o.ReportInterval)
	defer recheck.Stop()
	defer report.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
			if err := c.update(ctx, updateOpts{}); err != nil && ctx.Err() == nil {
				h.log.Info("tunnel sync finished with errors", dlog.Tunnel(c.id), dlog.Err(err))
			}
		case <-recheck.C:
			c.recheck(ctx)
			recheck.Reset(h.o.RecheckInterval)
		case <-report.C:
			c.report(ctx)
		case <-c.canKick:
			c.canaryWork(ctx)
		}
	}
}

// recheck re-tests the skipped rungs: failed installations are retried,
// the UDP probe runs again, every rung is validated again; a rung that now
// passes is restored (rung_restored). A canary that could not be built is
// tried again.
func (c *tunnelCtl) recheck(ctx context.Context) {
	_, plan := c.snapshot()
	c.h.forgetInstallFailures(plan.Backends)
	c.mu.Lock()
	c.can.unusable = false
	c.mu.Unlock()
	if err := c.update(ctx, updateOpts{force: true}); err != nil && ctx.Err() == nil {
		c.h.log.Info("tunnel re-check finished with errors", dlog.Tunnel(c.id), dlog.Err(err))
	}
}

// report probes every TCP port map of a running tunnel for reporting only
// (section 9: every 60 seconds; the failover decision uses the probe port
// alone). Results go to the probe history "<tunnel>/*/<listen>/<proto>".
func (c *tunnelCtl) report(ctx context.Context) {
	h := c.h
	eng := c.eng()
	if eng == nil {
		return
	}
	st := eng.State()
	if st.Active.IsZero() || !runningState(st.State) {
		return
	}
	t, plan := c.snapshot()
	c.refreshPlain(ctx, st.Active.Node, t)
	host := c.probeHost(plan, st.Active)
	var maps []config.PortMap
	var targets []health.Target
	for _, pm := range t.Ports {
		if pm.Proto != config.ProtoTCP {
			continue
		}
		maps = append(maps, pm)
		targets = append(targets, health.Target{Addr: net.JoinHostPort(host, strconv.Itoa(pm.Listen)), Kind: probeKind(pm)})
	}
	if len(targets) == 0 {
		return
	}
	results := health.PathAll(ctx, h.tun.pool, targets, probeTimeout(t))
	now := h.now()
	for i, r := range results {
		pm := maps[i]
		err := h.st.AppendProbe(t.ID, portsProbeNode, strconv.Itoa(pm.Listen)+"/"+pm.Proto, state.ProbeSample{
			At: now, OK: r.OK, RTT: r.RTT, Kind: "port:" + strconv.Itoa(pm.Listen), Error: r.Err,
		})
		if err != nil {
			h.log.Debug("cannot store a port report", dlog.Tunnel(t.ID), dlog.Err(err))
		}
	}
}

// runningState reports states in which a candidate runs and is probed.
func runningState(s string) bool {
	return s == state.StateUp || s == state.StateDegraded || s == state.StatePaused
}

// probeKind is the probe kind of a port map (auto by default).
func probeKind(pm config.PortMap) string {
	if pm.Probe == "" {
		return config.ProbeAuto
	}
	return pm.Probe
}

// probeTimeout is the tunnel's probe_timeout_s.
func probeTimeout(t config.Tunnel) time.Duration {
	if t.Failover.ProbeTimeoutS > 0 {
		return time.Duration(t.Failover.ProbeTimeoutS) * time.Second
	}
	return health.DefaultTimeout
}

// probeHost is the address path probes dial for candidate sc: the hub's
// public IP when its hub side forwards with NAT (output DNAT excludes
// loopback, ARCHITECTURE.md §7.5), otherwise Options.ProbeHost.
func (c *tunnelCtl) probeHost(plan render.TunnelPlan, sc state.Candidate) string {
	if pc, ok := plan.Candidate(sc.Node, sc.Transport); ok && len(pc.Hub.NAT) > 0 {
		if cfg := c.h.Config(); cfg.Hub != nil && cfg.Hub.PublicIP != "" {
			return cfg.Hub.PublicIP
		}
	}
	return c.h.o.ProbeHost
}
