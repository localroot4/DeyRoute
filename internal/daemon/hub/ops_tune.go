package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend/backhaul"
	"github.com/localroot4/deyroute/internal/backend/frp"
	"github.com/localroot4/deyroute/internal/backend/hysteria2"
	"github.com/localroot4/deyroute/internal/backend/waterwall"
	"github.com/localroot4/deyroute/internal/backend/wireguard"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// Automatic tuning (`deyroute optimize auto`, check, revert; section 12 and
// the automatic profile): every change is listed before it is applied, the
// owner confirms the list once (its hash), it is applied idempotently and
// undone by `optimize revert`; the periodic check only reports.

// DefaultTuneCheckInterval is how often the tuned kernel values are
// compared with the live ones (Options.TuneCheckInterval); the check only
// reports (event tune_drift), it never changes anything.
const DefaultTuneCheckInterval = 6 * time.Hour

// Step ids of OptimizeAutoApply (titles: hub.step.<id>, en_tune_hub.go).
const (
	stepTunePlan    = "tune_plan"
	stepTuneHub     = "tune_hub"
	stepTuneDropins = "tune_dropins"
	stepTuneNodes   = "tune_nodes"
	stepTuneTunnels = "tune_tunnels"
)

// tuneHubHost is TuneHost.Host of the hub.
const tuneHubHost = "hub"

// tuneSentBalanced is nodeRuntime.tuneSent after an old agent got balanced.
const tuneSentBalanced = "balanced"

// tierBackends render buffers or pools by tuning.backend_tier; wgBackends
// render the interface MTU tuning.wg_mtu; udpBackends need UDP buffers.
var (
	tierBackends = []string{backhaul.Name, frp.Name, waterwall.Name}
	wgBackends   = []string{wireguard.Name, wireguard.AWGName}
	udpBackends  = []string{hysteria2.Name, wireguard.AWGName}
)

// tuningBBR is tuning.bbr (default true).
func tuningBBR(cfg *config.Config) bool { return cfg == nil || cfg.Tuning == nil || cfg.Tuning.BBR }

// autoProfile reports whether the hub runs the automatic profile.
func autoProfile(cfg *config.Config) bool {
	return cfg != nil && cfg.Tuning != nil && cfg.Tuning.SysctlProfile == config.SysctlAuto
}

// nodesFollowAuto reports whether the nodes follow the hub's automatic
// tuning (the owner's consent, tuning.nodes_auto, only while the hub itself
// runs auto).
func nodesFollowAuto(cfg *config.Config) bool { return autoProfile(cfg) && cfg.Tuning.NodesAuto }

// hubTuneOptions are the inputs of the hub's automatic plan. No bandwidth
// measurement is kept yet, so the buffers are sized by RAM (BDPBytes 0).
func (h *Hub) hubTuneOptions(cfg *config.Config) sysctl.ApplyOptions {
	hubFwd, _ := h.ipForwardNeeds()
	udp := len(h.tunnelsUsing(func(c render.Candidate) bool { return slices.Contains(udpBackends, c.Backend) })) > 0
	return sysctl.ApplyOptions{
		Profile: config.SysctlAuto, BBR: tuningBBR(cfg), IPForward: hubFwd, UDPRungs: udp,
		// The managed firewall and NAT rungs use connection tracking.
		Conntrack: hubFwd || firewallManaged(cfg),
		Reserved:  setup.HubReserved(cfg),
	}
}

// nodeTuneArgs are the arguments of the automatic profile the hub sends to
// node (sysctl.apply and tune.plan); their InputsHash is what a node that
// follows the hub reports in its hello.
func nodeTuneArgs(cfg *config.Config, node string, fwd map[string]bool) api.SysctlArgs {
	bbr := tuningBBR(cfg)
	return api.SysctlArgs{Profile: config.SysctlAuto, IPForward: fwd[node], BBR: &bbr,
		Reserved: setup.NodeReserved(), PlanVersion: sysctl.PlanVersion}
}

// nodeHello returns the hello of node's current stream and whether it is
// online.
func (h *Hub) nodeHello(node string) (api.Hello, bool) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	nr := h.nodes[node]
	if nr == nil {
		return api.Hello{}, false
	}
	return nr.hello, nr.sess != nil && nr.st.Online
}

// noteNodeTune records that node applied profile with inputs hash (so the
// status is right before its next hello) and that it was sent.
func (h *Hub) noteNodeTune(node, profile, hash, sent string) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	if nr := h.nodes[node]; nr != nil {
		nr.hello.TuneProfile, nr.hello.TuneHash = profile, hash
		if sent != "" {
			nr.tuneSent = sent
		}
	}
}

// markTuneSent records that key is being sent to node; false when it was
// sent already (convergence sends the same inputs once).
func (h *Hub) markTuneSent(node, key string) bool {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	nr := h.nodes[node]
	if nr == nil || nr.tuneSent == key {
		return false
	}
	nr.tuneSent = key
	return true
}

// tunnelsUsing returns the ids of the tunnels with a planned candidate
// matching pred, sorted.
func (h *Hub) tunnelsUsing(pred func(c render.Candidate) bool) []string {
	var out []string
	for _, c := range h.tun.all() {
		_, plan := c.snapshot()
		if slices.ContainsFunc(plan.Candidates, pred) {
			out = append(out, c.id)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// forNodes runs fn for every id concurrently and waits.
func forNodes(ids []string, fn func(i int, id string)) {
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() { fn(i, id) })
	}
	wg.Wait()
}

func hashText(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}

// ---------------------------------------------------------------- plan

// Node states of an automatic plan.
const (
	tuneNodeOnline  = iota // asked for its plan (or the call failed: Error)
	tuneNodeOffline        // pending: applies when it reconnects
	tuneNodeOld            // an agent without FeatureTuneAuto: gets balanced
)

// nodeTune is one node of an automatic plan.
type nodeTune struct {
	id      string
	state   int
	version string
	args    api.SysctlArgs
}

// autoPlan is the plan of every host, as shown and as applied.
type autoPlan struct {
	report api.TunePlanReport
	hub    setup.HostTune
	nodes  []nodeTune
	// want is the configuration with the backend items (tiers, WireGuard
	// MTU) set; nil without them. affected lists the tunnels they restart.
	want     *config.Config
	affected []string
}

// planAuto computes the automatic plan of the hub and of every node: the
// hub's own (PlanHostTune), each online node's through tune.plan (nothing
// changes there), offline nodes marked pending, old agents marked (they
// get balanced) and, with backends, the backend tiers and the WireGuard
// MTU. The report hash covers all of it, so OptimizeAutoApply can refuse a
// plan other than the one shown (DEY-X065).
func (h *Hub) planAuto(ctx context.Context, backends bool) (*autoPlan, error) {
	cfg := h.Config()
	p := &autoPlan{hub: setup.PlanHostTune(h.o.Root, config.RoleHub, h.hubTuneOptions(cfg))}
	hubHost := p.hub.API(tuneHubHost)
	_, fwd := h.ipForwardNeeds()

	ids := make([]string, len(cfg.Nodes))
	for i, n := range cfg.Nodes {
		ids[i] = n.ID
	}
	hosts := make([]api.TuneHost, len(ids))
	p.nodes = make([]nodeTune, len(ids))
	forNodes(ids, func(i int, id string) {
		nt := nodeTune{id: id, args: nodeTuneArgs(cfg, id, fwd)}
		host := api.TuneHost{Host: id, Role: config.RoleNode, Changes: []api.TuneChange{}}
		hello, online := h.nodeHello(id)
		nt.version = hello.Version
		switch {
		case !online:
			nt.state = tuneNodeOffline
			host.Pending = true
		case !hello.HasFeature(api.FeatureTuneAuto):
			nt.state = tuneNodeOld
			host.Skips = []api.TuneSkip{{Key: config.SysctlAuto, Reason: i18n.T(i18n.TuneSkipOldAgent, hello.Version)}}
		default:
			var res api.SysctlResult
			cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
			err := h.Call(cctx, id, api.CmdTunePlan, nt.args, &res)
			cancel()
			if err != nil {
				host.Error = api.ToDTO(err)
				break
			}
			host.Facts, host.Skips, host.Hash = res.Facts, res.Skips, res.Hash
			if res.Changes != nil {
				host.Changes = res.Changes
			}
		}
		hosts[i], p.nodes[i] = host, nt
	})
	if backends {
		changes := h.planBackends(cfg, p, hubHost.Facts, hosts)
		hubHost.Changes = append(hubHost.Changes, changes...)
		lines := []string{"hub+backends", hubHost.Hash}
		for _, c := range changes {
			lines = append(lines, c.Key+"|"+c.From+"|"+c.To+"|"+c.Reason)
		}
		hubHost.Hash = hashText(lines)
	}
	if hubHost.Changes == nil {
		hubHost.Changes = []api.TuneChange{}
	}
	p.report.Hosts = append([]api.TuneHost{hubHost}, hosts...)
	lines := []string{"tune-report", strconv.Itoa(sysctl.PlanVersion), strconv.FormatBool(backends)}
	for _, host := range p.report.Hosts {
		code := ""
		if host.Error != nil {
			code = host.Error.Code
		}
		lines = append(lines, strings.Join([]string{host.Host, host.Role, host.Hash,
			strconv.FormatBool(host.Pending), code, strconv.Itoa(len(host.Skips))}, "|"))
	}
	for _, nt := range p.nodes {
		if nt.state == tuneNodeOld {
			lines = append(lines, "old|"+nt.id+"|"+nt.version)
		}
	}
	p.report.Hash = hashText(lines)
	for _, nt := range p.nodes {
		if nt.state == tuneNodeOffline {
			p.report.Warnings = append(p.report.Warnings, i18n.T(i18n.TuneWarnNodeOffline, nt.id))
		}
	}
	for _, host := range hosts {
		if host.Error != nil {
			p.report.Warnings = append(p.report.Warnings, i18n.T(i18n.TuneWarnNodeFailed, host.Host, host.Error.Code+" "+host.Error.Message))
		}
	}
	return p, nil
}

// tierFor is the backend tier of a host: small below 1 GiB of RAM or with
// one CPU, medium below 4 GiB, large from 4 GiB; "" when the RAM is unknown.
func tierFor(mem uint64, cpus int) string {
	switch {
	case mem == 0:
		return ""
	case mem < 1<<30 || cpus == 1:
		return config.BackendTierSmall
	case mem < 4<<30:
		return config.BackendTierMedium
	}
	return config.BackendTierLarge
}

// wgMTUFor is tuning.wg_mtu for the smallest known interface MTU: 0 (the
// backend default, 1420) when every interface carries 1500 bytes, else
// min(1420, MTU - 80) and at least 1280.
func wgMTUFor(minMTU int) int {
	if minMTU <= 0 || minMTU >= 1500 {
		return 0
	}
	return min(config.MaxWGMTU, max(config.MinWGMTU, minMTU-80))
}

// planBackends adds the backend items to p (p.want, p.affected) and
// returns them as changes of the hub's plan: the tier of every host whose
// RAM is known (the hub, the nodes from their plan or their hello) and the
// WireGuard MTU from the smallest interface MTU. Each change names the
// tunnels whose active rung restarts.
func (h *Hub) planBackends(cfg *config.Config, p *autoPlan, hubFacts *api.TuneFacts, nodes []api.TuneHost) []api.TuneChange {
	want := config.Clone(cfg)
	if want.Tuning == nil {
		want.Tuning = config.DefaultTuning()
	}
	type host struct {
		mem  uint64
		cpus int
	}
	facts := map[string]host{}
	minMTU := 0
	noteMTU := func(m int) {
		if m > 0 && (minMTU == 0 || m < minMTU) {
			minMTU = m
		}
	}
	if hubFacts != nil {
		facts[tuneHubHost] = host{hubFacts.MemBytes, hubFacts.CPUs}
		noteMTU(hubFacts.NICMTU)
	}
	for _, n := range nodes {
		if n.Facts != nil {
			facts[n.Host] = host{n.Facts.MemBytes, n.Facts.CPUs}
			noteMTU(n.Facts.NICMTU)
			continue
		}
		hello, _ := h.nodeHello(n.Host)
		ns, _ := h.nodeState(n.Host)
		if hello.MemTotal > 0 {
			facts[n.Host] = host{hello.MemTotal, ns.CPUs}
		}
	}
	var changes []api.TuneChange
	affectedText := func(ids []string) string {
		if len(ids) == 0 {
			return i18n.T(i18n.TuneNoTunnels)
		}
		return strings.Join(ids, ", ")
	}
	tierChange := func(key, who, from, to string, f host, pred func(render.Candidate) bool) {
		if from == to {
			return
		}
		ids := h.tunnelsUsing(pred)
		reason := i18n.T(i18n.TuneReasonBackendTier, to, sysctlSize(f.mem), strconv.Itoa(f.cpus), affectedText(ids))
		if to == "" {
			reason = i18n.T(i18n.TuneReasonBackendDefault, who, affectedText(ids))
		}
		changes = append(changes, api.TuneChange{Kind: api.TuneKindBackend, Key: key, From: from, To: to,
			Reason: reason, Effect: api.TuneEffectRestartsTunnels})
		p.affected = append(p.affected, ids...)
	}
	tiered := func(c render.Candidate) bool { return slices.Contains(tierBackends, c.Backend) }
	if f, ok := facts[tuneHubHost]; ok {
		want.Tuning.BackendTier = tierFor(f.mem, f.cpus)
		tierChange("tuning.backend_tier", tuneHubHost, cfg.BackendTier(""), want.Tuning.BackendTier, f, tiered)
	}
	for i := range want.Nodes {
		n := &want.Nodes[i]
		f, ok := facts[n.ID]
		if !ok {
			continue
		}
		n.BackendTier = tierFor(f.mem, f.cpus)
		id := n.ID
		tierChange("nodes."+id+".backend_tier", id, cfg.BackendTier(id), n.BackendTier, f, func(c render.Candidate) bool {
			return c.Node == id && tiered(c)
		})
	}
	want.Tuning.WGMTU = wgMTUFor(minMTU)
	if cur := wgMTU(cfg); cur != want.Tuning.WGMTU {
		ids := h.tunnelsUsing(func(c render.Candidate) bool { return slices.Contains(wgBackends, c.Backend) })
		reason := i18n.T(i18n.TuneReasonWGMTU, strconv.Itoa(minMTU), affectedText(ids))
		if want.Tuning.WGMTU == 0 {
			reason = i18n.T(i18n.TuneReasonWGMTUDefault, affectedText(ids))
		}
		changes = append(changes, api.TuneChange{Kind: api.TuneKindBackend, Key: "tuning.wg_mtu",
			From: mtuText(cur), To: mtuText(want.Tuning.WGMTU), Reason: reason, Effect: api.TuneEffectRestartsTunnels})
		p.affected = append(p.affected, ids...)
	}
	slices.Sort(p.affected)
	p.affected = slices.Compact(p.affected)
	p.want = want
	return changes
}

// mtuText shows tuning.wg_mtu ("" = the default).
func mtuText(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// sysctlSize renders a RAM size like the plan reasons ("2 GiB", "512 MiB").
func sysctlSize(b uint64) string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return strconv.FormatUint(b>>30, 10) + " GiB"
	case b >= 1<<30:
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GiB"
	}
	return strconv.FormatUint(b>>20, 10) + " MiB"
}

// setBackendItems copies the backend items of want into c.
func setBackendItems(c, want *config.Config) {
	if c.Tuning == nil {
		c.Tuning = config.DefaultTuning()
	}
	c.Tuning.BackendTier, c.Tuning.WGMTU = want.Tuning.BackendTier, want.Tuning.WGMTU
	for i := range c.Nodes {
		if n, ok := want.NodeByID(c.Nodes[i].ID); ok {
			c.Nodes[i].BackendTier = n.BackendTier
		}
	}
}

// clearAutoConfig removes the keys of the automatic profile from c (the
// owner's consent for nodes, backend tiers, WireGuard MTU).
func clearAutoConfig(c *config.Config) {
	if c.Tuning != nil {
		c.Tuning.NodesAuto, c.Tuning.BackendTier, c.Tuning.WGMTU = false, "", 0
	}
	for i := range c.Nodes {
		c.Nodes[i].BackendTier = ""
	}
}

// autoTierTunnels returns the tunnels whose rendered files depend on the
// backend items cfg sets (they restart when the items are cleared).
func (h *Hub) autoTierTunnels(cfg *config.Config) []string {
	tiered := map[string]bool{}
	hubTier := cfg.BackendTier("") != ""
	for _, n := range cfg.Nodes {
		tiered[n.ID] = n.BackendTier != ""
	}
	mtu := wgMTU(cfg) != 0
	return h.tunnelsUsing(func(c render.Candidate) bool {
		if slices.Contains(tierBackends, c.Backend) && (hubTier || tiered[c.Node]) {
			return true
		}
		return mtu && slices.Contains(wgBackends, c.Backend)
	})
}

// restartTunnels re-renders the tunnels ids after their backend items
// changed; only an active rung whose files changed restarts (opMu held).
// Failures are returned as warnings.
func (h *Hub) restartTunnels(ctx context.Context, ids []string) []string {
	var warnings []string
	for _, id := range ids {
		c := h.tun.lookup(id)
		if c == nil {
			continue
		}
		if err := c.update(ctx, updateOpts{restartActive: true, quietInstall: true}); err != nil {
			e := deyerr.As(err)
			warnings = append(warnings, i18n.T(i18n.TuneWarnTunnelFailed, id, string(e.Code)+" "+e.Message()))
			h.log.Warn("tunnel update after a tuning change finished with errors", dlog.Tunnel(id), dlog.Err(err))
		}
	}
	return warnings
}

// OptimizeAutoPlan implements api.Local (`deyroute optimize auto
// --dry-run`, and the list shown before the confirmation): the automatic
// plan of the hub and every node; nothing changes anywhere.
func (l *local) OptimizeAutoPlan(ctx context.Context, opts api.AutoOptions) (api.TunePlanReport, error) {
	p, err := l.h.planAuto(ctx, opts.Backends)
	if err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	return p.report, nil
}

// OptimizeAutoApply implements api.Local (`deyroute optimize auto`): the
// plan is computed again and refused with DEY-X065 when its hash is not the
// one the owner confirmed; then the automatic backup, the hub's kernel
// values, the hub's resource drop-ins (one daemon-reload, nothing
// restarts), config.yaml (tuning.sysctl_profile auto, tuning.nodes_auto:
// the owner's consent for the nodes, the backend items), every online node
// (offline nodes apply when they reconnect, old agents get balanced) and,
// with Backends, the tunnels whose active rung the backend items change.
func (l *local) OptimizeAutoApply(ctx context.Context, req api.AutoApply, progress func(api.Step)) (api.TunePlanReport, error) {
	h := l.h
	rep := &steps{progress: progress}
	if err := h.checkApplied(h.Config()); err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	defer unlock()
	var p *autoPlan
	if err := rep.run(stepTunePlan, func() (string, error) {
		var err error
		if p, err = h.planAuto(ctx, req.Backends); err != nil {
			return "", err
		}
		if p.report.Hash != req.Hash {
			return "", deyerr.New(deyerr.X065, nil)
		}
		return countChanges(p.report) + " changes", nil
	}); err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	if err := rep.run(stepBackup, func() (string, error) { return h.autoBackup() }); err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	var warnings []string
	if err := rep.run(stepTuneHub, func() (string, error) {
		plan := p.hub.Sysctl
		_, w, err := h.sysctlManager().ApplyWith(sysctl.ApplyOptions{Profile: config.SysctlAuto, Plan: &plan})
		warnings = append(warnings, w...)
		return strconv.Itoa(len(plan.Changes)) + " changes", err
	}); err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	_ = rep.runWarn(stepTuneDropins, func() (string, error, error) {
		changed, err := h.o.Systemd.WriteAutoDropIns(ctx, p.hub.DropIns)
		if err != nil {
			warnings = append(warnings, i18n.T(i18n.TuneWarnDropins, deyerr.As(err).Message()))
			return "", err, nil
		}
		if !changed {
			return "unchanged", nil, nil
		}
		return strconv.Itoa(len(p.hub.DropIns)) + " files", nil, nil
	})
	if err := rep.run(stepConfig, func() (string, error) {
		_, err := h.mutate(func(c *config.Config) error {
			if c.Tuning == nil {
				c.Tuning = config.DefaultTuning()
			}
			c.Tuning.SysctlProfile = config.SysctlAuto
			c.Tuning.NodesAuto = true
			if p.want != nil {
				setBackendItems(c, p.want)
			}
			return nil
		})
		return config.DefaultPath, err
	}); err != nil {
		return api.TunePlanReport{}, withLog(err)
	}
	_ = rep.run(stepTuneNodes, func() (string, error) {
		w, detail := h.tuneNodes(ctx, p.nodes)
		warnings = append(warnings, w...)
		return detail, nil
	})
	if p.want != nil && len(p.affected) > 0 {
		_ = rep.run(stepTuneTunnels, func() (string, error) {
			warnings = append(warnings, h.restartTunnels(ctx, p.affected)...)
			return strings.Join(p.affected, ", "), nil
		})
	}
	for _, w := range warnings {
		h.log.Warn("automatic tuning: " + dlog.Redact(w))
	}
	h.log.Info("automatic tuning applied", slog.String("plan", p.report.Hash), slog.Bool("backends", req.Backends))
	h.opsEvent("", "automatic tuning applied ("+countChanges(p.report)+" changes)")
	out := p.report
	out.Applied = true
	out.Warnings = append(out.Warnings, warnings...)
	return out, nil
}

// countChanges is the number of changes of every host, as text.
func countChanges(r api.TunePlanReport) string {
	n := 0
	for _, host := range r.Hosts {
		n += len(host.Changes)
	}
	return strconv.Itoa(n)
}

// tuneNodes applies the automatic profile to the nodes of a plan:
// online nodes now, old agents get balanced (with a warning event),
// offline nodes later (convergence). It returns the warnings and a step
// detail.
func (h *Hub) tuneNodes(ctx context.Context, nodes []nodeTune) (warnings []string, detail string) {
	var applied, pending, old int
	cfg := h.Config()
	_, fwd := h.ipForwardNeeds()
	for _, nt := range nodes {
		switch nt.state {
		case tuneNodeOffline:
			pending++
			warnings = append(warnings, i18n.T(i18n.TuneWarnNodeOffline, nt.id))
			continue
		case tuneNodeOld:
			old++
			if w := h.tuneOldNode(ctx, cfg, nt.id, nt.version, fwd[nt.id]); w != "" {
				warnings = append(warnings, w)
			}
			continue
		}
		w, err := h.applyNodeTune(ctx, nt.id, nt.args)
		warnings = append(warnings, w...)
		if err == nil {
			applied++
		}
	}
	return warnings, strconv.Itoa(applied) + " applied, " + strconv.Itoa(pending) + " pending, " + strconv.Itoa(old) + " balanced"
}

// applyNodeTune sends the automatic profile to node; the node's warnings
// (and a failure) come back prefixed with the node id.
func (h *Hub) applyNodeTune(ctx context.Context, node string, args api.SysctlArgs) ([]string, error) {
	var res api.SysctlResult
	cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
	err := h.Call(cctx, node, api.CmdSysctlApply, args, &res)
	cancel()
	if err != nil {
		e := deyerr.As(err)
		h.log.Warn("node did not apply the automatic tuning", dlog.Node(node), dlog.Err(err))
		return []string{i18n.T(i18n.TuneWarnNodeFailed, node, string(e.Code)+" "+e.Message())}, err
	}
	hash := args.InputsHash()
	h.noteNodeTune(node, config.SysctlAuto, hash, hash)
	var warnings []string
	for _, w := range res.Warnings {
		warnings = append(warnings, i18n.T(i18n.TuneWarnNode, node, dlog.Redact(w)))
	}
	keys := make([]string, 0, len(res.Changes))
	for _, c := range res.Changes {
		keys = append(keys, c.Key)
	}
	msg := "automatic tuning applied on node " + node + " (" + strconv.Itoa(len(res.Changes)) + " changes"
	if len(keys) > 0 {
		msg += ": " + strings.Join(keys, ", ")
	}
	h.Emit(state.Event{Type: state.EvConfigApplied, Level: state.LevelInfo, Node: node, Message: msg + ")"})
	return warnings, nil
}

// tuneOldNode gives an agent that predates the automatic profile the
// balanced one (it ignores "auto" safely only from FeatureTuneAuto on) and
// emits a warning event; it returns the owner's warning ("" when even
// balanced failed: that is logged).
func (h *Hub) tuneOldNode(ctx context.Context, cfg *config.Config, node, ver string, fwd bool) string {
	bbr := tuningBBR(cfg)
	cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
	err := h.Call(cctx, node, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlBalanced, IPForward: fwd, BBR: &bbr}, nil)
	cancel()
	if err != nil {
		e := deyerr.As(err)
		h.log.Warn("old node did not apply the balanced profile", dlog.Node(node), dlog.Err(err))
		return i18n.T(i18n.TuneWarnNodeFailed, node, string(e.Code)+" "+e.Message())
	}
	h.noteNodeTune(node, config.SysctlBalanced, "", tuneSentBalanced)
	w := i18n.T(i18n.TuneWarnNodeOld, node, ver)
	h.Emit(state.Event{Type: state.EvConfigApplied, Level: state.LevelWarn, Node: node, Message: w})
	return w
}

// convergeTuning runs when a node connects: while the nodes follow the
// hub's automatic tuning, a node whose reported inputs differ from the
// hub's gets them (once per input change, so a flapping link is not
// re-tuned on every reconnect), and an old agent gets balanced. Nothing is
// sent otherwise. It ends with the stream s.
func (h *Hub) convergeTuning(s *api.Session) {
	cfg := h.Config()
	if !nodesFollowAuto(cfg) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	node := s.NodeID
	_, fwd := h.ipForwardNeeds()
	if !s.Hello.HasFeature(api.FeatureTuneAuto) {
		if s.Hello.TuneProfile == config.SysctlBalanced || !h.markTuneSent(node, tuneSentBalanced) {
			return
		}
		h.tuneOldNode(ctx, cfg, node, s.Hello.Version, fwd[node])
		return
	}
	args := nodeTuneArgs(cfg, node, fwd)
	want := args.InputsHash()
	if s.Hello.TuneProfile == config.SysctlAuto && s.Hello.TuneHash == want {
		return
	}
	if !h.markTuneSent(node, want) {
		return
	}
	h.log.Info("node runs other tuning inputs than the hub's: applying the automatic tuning", dlog.Node(node),
		slog.String("profile", s.Hello.TuneProfile))
	if _, err := h.applyNodeTune(ctx, node, args); err != nil {
		e := deyerr.As(err)
		h.Emit(state.Event{Type: state.EvConfigApplied, Level: state.LevelWarn, Node: node, Code: string(e.Code),
			Message: "automatic tuning could not be applied on node " + node + ": " + e.Message()})
	}
}

// ---------------------------------------------------------------- leave auto

// leaveAuto runs when the hub leaves the profile auto (another profile,
// revert): the hub's resource drop-ins go (one daemon-reload), the backend
// items and tuning.nodes_auto are cleared with the profile set by
// setProfile, and the tunnels the backend items changed are re-rendered.
// It returns warnings.
func (h *Hub) leaveAuto(ctx context.Context, profile string) ([]string, error) {
	var warnings []string
	if _, err := h.o.Systemd.RemoveAutoDropIns(ctx); err != nil {
		warnings = append(warnings, i18n.T(i18n.TuneWarnDropins, deyerr.As(err).Message()))
	}
	affected := h.autoTierTunnels(h.Config())
	var unlock func()
	if len(affected) > 0 {
		u, err := h.lockOps(ctx)
		if err != nil {
			return warnings, err
		}
		unlock = u
		defer unlock()
	}
	if _, err := h.mutate(func(c *config.Config) error {
		if c.Tuning == nil {
			c.Tuning = config.DefaultTuning()
		}
		c.Tuning.SysctlProfile = profile
		clearAutoConfig(c)
		return nil
	}); err != nil {
		return warnings, err
	}
	warnings = append(warnings, h.restartTunnels(ctx, affected)...)
	return warnings, nil
}

// ---------------------------------------------------------------- check

// tuneCheck compares the tuned values with the live kernel on the hub and
// every node (`optimize check`, the periodic check): offline nodes and old
// agents carry an error instead.
func (h *Hub) tuneCheck(ctx context.Context) api.TuneCheck {
	hub, err := setup.CheckHost(h.o.Root, tuneHubHost, config.RoleHub)
	if err != nil {
		hub.Error = api.ToDTO(err)
	}
	cfg := h.Config()
	ids := make([]string, len(cfg.Nodes))
	for i, n := range cfg.Nodes {
		ids[i] = n.ID
	}
	hosts := make([]api.TuneHostCheck, len(ids))
	forNodes(ids, func(i int, id string) {
		out := api.TuneHostCheck{Host: id, Role: config.RoleNode}
		hello, online := h.nodeHello(id)
		switch {
		case !online:
			out.Error = api.ToDTO(deyerr.New(deyerr.N003, deyerr.Params{"node": id}))
		case !hello.HasFeature(api.FeatureTuneAuto):
			out.Profile = hello.TuneProfile
			out.Error = api.ToDTO(deyerr.New(deyerr.X008, deyerr.Params{"feature": "tuning check on deyroute " + hello.Version}))
		default:
			cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
			err := h.Call(cctx, id, api.CmdTuneCheck, nil, &out)
			cancel()
			out.Host, out.Role = id, config.RoleNode
			if err != nil {
				out.Error = api.ToDTO(err)
			}
		}
		hosts[i] = out
	})
	res := api.TuneCheck{Clean: true, Hosts: append([]api.TuneHostCheck{hub}, hosts...)}
	for _, host := range res.Hosts {
		if len(host.Drift) > 0 || len(host.Findings) > 0 {
			res.Clean = false
		}
	}
	return res
}

// OptimizeCheck implements api.Local (`deyroute optimize check`): the
// drift and findings of the hub and every node. Nothing changes.
func (l *local) OptimizeCheck(ctx context.Context) (api.TuneCheck, error) {
	return l.h.tuneCheck(ctx), nil
}

// tuneCheckLoop sets the hub's tuned conntrack keys again after the first
// firewall apply (Reassert: nf_conntrack may have loaded after
// systemd-sysctl ran) and then runs the tuning check every
// TuneCheckInterval. The check only reports: one tune_drift warning per
// drifted key until that key is fine again (tuneCheckOnce). It never runs
// with Options.DisableTuning.
func (h *Hub) tuneCheckLoop(ctx context.Context) {
	if h.o.DisableTuning || ctx.Err() != nil {
		return
	}
	h.waitFirewallComputed(ctx)
	h.reassertTuning()
	reported := map[string]bool{}
	every(ctx, h.o.TuneCheckInterval, h.o.TuneCheckInterval, func() { h.tuneCheckOnce(ctx, reported) })
}

// waitFirewallComputed waits (at most a minute) until the first firewall
// computation of this process ran.
func (h *Hub) waitFirewallComputed(ctx context.Context) {
	deadline := time.NewTimer(time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		h.fwMu.Lock()
		done := h.fw.done
		h.fwMu.Unlock()
		if done {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

// reassertTuning is sysctl Reassert on the hub with one config_applied
// event per key set again.
func (h *Hub) reassertTuning() {
	kvs, warnings, err := h.sysctlManager().Reassert()
	for _, kv := range kvs {
		h.opsEvent("", "tuned kernel value "+kv.Key+" = "+kv.Value+" set again after the boot (nf_conntrack loaded after the boot settings)")
	}
	for _, w := range warnings {
		h.log.Warn("sysctl: " + w)
	}
	if err != nil {
		h.log.Warn("cannot set the tuned kernel values again", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
}

// tuneCheckOnce runs the tuning check and emits one tune_drift warning per
// host and key that drifted since the last report (DEY-X067); a key that is
// fine again is forgotten, so a later drift is reported again. reported is
// the loop's memory.
func (h *Hub) tuneCheckOnce(ctx context.Context, reported map[string]bool) {
	chk := h.tuneCheck(ctx)
	seen := map[string]bool{}
	for _, host := range chk.Hosts {
		if host.Error != nil {
			// Not checked this time: keep what was reported.
			for k := range reported {
				if strings.HasPrefix(k, host.Host+"|") {
					seen[k] = true
				}
			}
			continue
		}
		for _, d := range host.Drift {
			k := host.Host + "|" + d.Key
			seen[k] = true
			if reported[k] {
				continue
			}
			reported[k] = true
			where := d.OverriddenBy
			if where == "" {
				where = "a runtime change"
			}
			e := deyerr.New(deyerr.X067, deyerr.Params{"key": d.Key, "where": where})
			ev := state.Event{Type: state.EvTuneDrift, Level: state.LevelWarn, Code: string(e.Code), Reason: e.Why(),
				Message: host.Host + ": " + e.Message() + " (live " + d.Live + ", deyroute set " + d.Want + ")"}
			if host.Role == config.RoleNode {
				ev.Node = host.Host
			}
			h.Emit(ev)
		}
	}
	for k := range reported {
		if !seen[k] {
			delete(reported, k)
		}
	}
}

// ---------------------------------------------------------------- status

// nodeTuneRows returns the tuning state of every node for OptimizeStatus.
func (h *Hub) nodeTuneRows(cfg *config.Config) []api.NodeTuneStatus {
	follow := nodesFollowAuto(cfg)
	_, fwd := h.ipForwardNeeds()
	out := make([]api.NodeTuneStatus, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		hello, online := h.nodeHello(n.ID)
		row := api.NodeTuneStatus{Node: n.ID, Online: online, Profile: hello.TuneProfile, Hash: hello.TuneHash,
			AutoCapable: hello.HasFeature(api.FeatureTuneAuto)}
		if follow {
			switch {
			case hello.Version == "":
				row.Pending = true // not seen since the hub started
			case row.AutoCapable:
				row.Pending = hello.TuneProfile != config.SysctlAuto || hello.TuneHash != nodeTuneArgs(cfg, n.ID, fwd).InputsHash()
			}
		}
		out = append(out, row)
	}
	return out
}

// hubFacts are the hub's measured host facts.
func (h *Hub) hubFacts() *api.TuneFacts {
	f := api.TuneFacts(sysinfo.Collect(h.o.Root))
	return &f
}
