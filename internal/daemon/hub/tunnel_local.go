package hub

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Step ids of the tunnel operations (section 6 progress screens).
const (
	stepInstallHub  = "install_hub"
	stepInstallNode = "install_node"
	stepRender      = "render"
	stepFirewall    = "firewall"
	stepStart       = "start"
	stepProbe       = "probe"
	stepCheckPorts  = "check_ports"
	stepEngine      = "engine"
	stepRestart     = "restart"
	stepStop        = "stop"
	stepRemove      = "remove"
	stepConfig      = "config"
	stepCleanup     = "cleanup"
	// Steps with a computed title.
	stepUp          = "up"
	stepBackupReady = "backup_ready"
	stepRungPrefix  = "rung:"
)

// Tunnel operation limits.
const (
	// tunnelDetailEvents is the number of events in TunnelShow.
	tunnelDetailEvents = 20
	// upPoll is how often TunnelAdd looks at the engine state.
	upPoll = 50 * time.Millisecond
)

// ---------------------------------------------------------------- helpers

// lockOps waits for the startup reconcile and takes the tunnel operation
// lock; the returned function releases it (safe to call twice).
func (h *Hub) lockOps(ctx context.Context) (func(), error) {
	if err := h.waitTunnelsReady(ctx); err != nil {
		return nil, err
	}
	h.tun.opMu.Lock()
	return sync.OnceFunc(h.tun.opMu.Unlock), nil
}

// configTunnel returns tunnel id of the current config (DEY-C021).
func (h *Hub) configTunnel(id string) (*config.Config, config.Tunnel, error) {
	cfg := h.Config()
	t, ok := cfg.Tunnel(strings.TrimSpace(id))
	if !ok {
		return cfg, config.Tunnel{}, deyerr.New(deyerr.C021, deyerr.Params{"tunnel": id})
	}
	return cfg, t.Clone(), nil
}

// runningCtl returns the controller of an enabled tunnel: DEY-C021 for an
// unknown tunnel, DEY-F007 when it has no running engine (disabled).
func (h *Hub) runningCtl(id string) (*tunnelCtl, *failover.Engine, error) {
	if _, _, err := h.configTunnel(id); err != nil {
		return nil, nil, err
	}
	c := h.tun.lookup(strings.TrimSpace(id))
	if c == nil {
		return nil, nil, deyerr.New(deyerr.F007, deyerr.Params{"tunnel": id})
	}
	e := c.eng()
	if e == nil {
		return nil, nil, deyerr.New(deyerr.F007, deyerr.Params{"tunnel": id})
	}
	return c, e, nil
}

// tunnelInfoOf is the dashboard row of tunnel id.
func (h *Hub) tunnelInfoOf(id string) api.TunnelInfo {
	cfg := h.Config()
	if t, ok := cfg.Tunnel(id); ok {
		return h.tunnelInfo(cfg, t)
	}
	return api.TunnelInfo{ID: id, Ports: []api.PortMapDTO{}, Nodes: []string{}, Ladder: []string{}}
}

// portMaps turns request port specs into port maps (proto tcp, target
// 127.0.0.1:<listen> and probe auto by default). The probe kind follows the
// config.yaml rule (DEY-C013: auto|tcp|tls|http, auto for UDP).
func portMaps(specs []api.PortSpec) ([]config.PortMap, error) {
	out := make([]config.PortMap, 0, len(specs))
	for _, s := range specs {
		proto := config.ProtoTCP
		if p := strings.TrimSpace(s.Proto); p != "" {
			np, ok := ports.NormalizeProto(p)
			if !ok {
				return nil, deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(s.Listen) + "/" + s.Proto})
			}
			proto = np
		}
		if !ports.ValidPort(s.Listen) {
			return nil, deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(s.Listen)})
		}
		target := strings.TrimSpace(s.Target)
		if target == "" {
			target = config.DefaultTarget(s.Listen)
		}
		if !config.ValidHostPort(target) {
			return nil, deyerr.New(deyerr.C004, deyerr.Params{"target": target, "tunnel": "-"})
		}
		key := config.ListenKey{Port: s.Listen, Proto: proto}
		probe, err := probeOf("ports["+key.String()+"].probe", proto, s.Probe)
		if err != nil {
			return nil, err
		}
		out = append(out, config.PortMap{Listen: s.Listen, Proto: proto, Target: target, Probe: probe})
	}
	return out, nil
}

// probeOf is the probe kind a request asks for a port map of proto (case
// and spaces do not matter; empty = auto), checked like config.yaml
// (DEY-C013 naming field).
func probeOf(field, proto, probe string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(probe))
	if p == "" {
		p = config.ProbeAuto
	}
	if err := config.CheckProbe(field, proto, p); err != nil {
		return "", err
	}
	return p, nil
}

// checkNewPorts applies the port rules of section 10 to listen ports a
// tunnel wants to add: not reserved (DEY-P011), not used by another tunnel
// or twice (DEY-C003), not bound on the hub by another process (DEY-P012
// with the process name).
func (h *Hub) checkNewPorts(ctx context.Context, cfg *config.Config, tunnel string, maps []config.PortMap, existing []config.PortMap) error {
	used := cfg.UsedListenPorts()
	seen := map[config.ListenKey]bool{}
	for _, pm := range existing {
		seen[config.ListenKey{Port: pm.Listen, Proto: pm.Proto}] = true
	}
	for _, pm := range maps {
		key := config.ListenKey{Port: pm.Listen, Proto: pm.Proto}
		if reserved, why := ports.Reserved(pm.Listen, cfg.Hub.ControlPort); reserved {
			return deyerr.New(deyerr.P011, deyerr.Params{"port": key.String(), "reason": why})
		}
		if seen[key] {
			return deyerr.New(deyerr.C003, deyerr.Params{"port": key.String(), "tunnel": tunnel, "other": tunnel}).
				WithWhy(fmt.Sprintf("tunnel %s would list %s twice; only one process can bind a port", tunnel, key))
		}
		seen[key] = true
		if other, ok := used[key]; ok && other != tunnel {
			return deyerr.New(deyerr.C003, deyerr.Params{"port": key.String(), "tunnel": other, "other": tunnel})
		}
		if b := h.o.BindCheck(ctx, pm.Listen, pm.Proto); !b.Free {
			return b.Err(pm.Listen)
		}
	}
	return nil
}

// overlayFailover copies the set fields of f onto dst: numbers greater
// than zero and the policy when not empty; failback always (the request
// carries the whole settings block).
func overlayFailover(dst *config.Failover, f api.FailoverSettings) {
	if f.Policy != "" {
		dst.Policy = f.Policy
	}
	set := func(p *int, v int) {
		if v > 0 {
			*p = v
		}
	}
	set(&dst.ProbeIntervalS, f.ProbeIntervalS)
	set(&dst.ProbeTimeoutS, f.ProbeTimeoutS)
	set(&dst.FailThreshold, f.FailThreshold)
	set(&dst.RecoverThreshold, f.RecoverThreshold)
	set(&dst.FailbackAfterS, f.FailbackAfterS)
	set(&dst.MaxSwitchesPerHour, f.MaxSwitchesPerHour)
	set(&dst.QuarantineS, f.QuarantineS)
	dst.Failback = f.Failback
}

// failoverDTO mirrors config.Failover for the UI.
func failoverDTO(f config.Failover) api.FailoverSettings {
	return api.FailoverSettings{
		Policy: f.Policy, ProbeIntervalS: f.ProbeIntervalS, ProbeTimeoutS: f.ProbeTimeoutS,
		FailThreshold: f.FailThreshold, RecoverThreshold: f.RecoverThreshold, Failback: f.Failback,
		FailbackAfterS: f.FailbackAfterS, MaxSwitchesPerHour: f.MaxSwitchesPerHour, QuarantineS: f.QuarantineS,
	}
}

// checkPolicy validates a failover policy.
func checkPolicy(p string) error {
	if slices.Contains(config.Policies, p) {
		return nil
	}
	return deyerr.New(deyerr.C013, deyerr.Params{"field": "failover.policy", "value": p, "allowed": strings.Join(config.Policies, ", ")})
}

// checkNodes validates an ordered node list of tunnel id: at least one,
// no repeats, every node joined.
func checkNodes(cfg *config.Config, id string, nodes []string) ([]string, error) {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !config.ValidNodeID(n) {
			return nil, deyerr.New(deyerr.C007, deyerr.Params{"kind": "node", "id": n})
		}
		if slices.Contains(out, n) {
			return nil, deyerr.New(deyerr.C002, deyerr.Params{"kind": "tunnel " + id + " node", "id": n})
		}
		if _, ok := cfg.NodeByID(n); !ok {
			return nil, deyerr.New(deyerr.C010, deyerr.Params{"node": n, "tunnel": id})
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, deyerr.New(deyerr.C008, deyerr.Params{"tunnel": id})
	}
	return out, nil
}

// cleanRungs trims an inline ladder and drops empty entries.
func cleanRungs(rungs []string) []string {
	out := make([]string, 0, len(rungs))
	for _, r := range rungs {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// newTunnel builds the config entry of a TunnelAdd request after checking
// it: id (derived from the name when empty; DEY-C007/C002), nodes
// (DEY-C008/C010/C002), ports (section 10 rules), ladder (profile, inline
// rungs or one fixed transport; it must keep a rung for the tunnel's
// protocols), policy, TLS mode and failover settings.
func (h *Hub) newTunnel(ctx context.Context, cfg *config.Config, req api.TunnelAddRequest) (config.Tunnel, error) {
	name := cleanName(req.Name)
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = config.UniqueID(config.Slugify(name), func(s string) bool {
			_, taken := cfg.Tunnel(s)
			return taken
		})
	}
	if !config.ValidID(id) {
		return config.Tunnel{}, deyerr.New(deyerr.C007, deyerr.Params{"kind": "tunnel", "id": id})
	}
	if _, taken := cfg.Tunnel(id); taken {
		return config.Tunnel{}, deyerr.New(deyerr.C002, deyerr.Params{"kind": "tunnel", "id": id})
	}
	if strings.TrimSpace(req.Node) == "" {
		return config.Tunnel{}, deyerr.New(deyerr.C008, deyerr.Params{"tunnel": id})
	}
	nodes, err := checkNodes(cfg, id, append([]string{req.Node}, req.Backups...))
	if err != nil {
		return config.Tunnel{}, err
	}
	maps, err := portMaps(req.Ports)
	if err != nil {
		return config.Tunnel{}, err
	}
	switch {
	case len(maps) == 0:
		return config.Tunnel{}, deyerr.New(deyerr.C013, deyerr.Params{"field": "ports", "value": "", "allowed": "at least one port, e.g. 443,2053"})
	case len(maps) > config.MaxPortMaps:
		return config.Tunnel{}, deyerr.New(deyerr.C015, deyerr.Params{"tunnel": id, "count": len(maps)})
	}
	if err := h.checkNewPorts(ctx, cfg, id, maps, nil); err != nil {
		return config.Tunnel{}, err
	}
	t := config.NewTunnel(id, name, nodes, maps)
	switch {
	case strings.TrimSpace(req.FixedTransport) != "":
		fixed := strings.TrimSpace(req.FixedTransport)
		if !backend.KnownTransport(fixed) {
			_, _, err := backend.Lookup(fixed)
			return config.Tunnel{}, err
		}
		t.Ladder = config.LadderRef{Inline: []string{fixed}}
	case len(cleanRungs(req.Rungs)) > 0:
		t.Ladder = config.LadderRef{Inline: cleanRungs(req.Rungs)}
	case strings.TrimSpace(req.Ladder) != "":
		name := strings.TrimSpace(req.Ladder)
		if _, ok := cfg.LadderProfile(name); !ok {
			return config.Tunnel{}, deyerr.New(deyerr.C012, deyerr.Params{"ladder": name, "tunnel": id})
		}
		t.Ladder = config.LadderRef{Name: name}
	}
	for _, r := range t.Ladder.Inline {
		if _, _, err := backend.Lookup(r); err != nil {
			return config.Tunnel{}, err
		}
	}
	if _, err := cfg.ResolveLadder(&t, supports); err != nil {
		return config.Tunnel{}, err
	}
	if req.Failover != nil {
		overlayFailover(&t.Failover, *req.Failover)
	}
	if p := strings.TrimSpace(req.Policy); p != "" {
		t.Failover.Policy = p
	}
	if err := checkPolicy(t.Failover.Policy); err != nil {
		return config.Tunnel{}, err
	}
	if m := strings.TrimSpace(req.TLSMode); m != "" {
		t.TLS.Mode = m
	}
	if err := checkACMEDomain(cfg, id, t.TLS.Mode); err != nil {
		return config.Tunnel{}, err
	}
	return t, nil
}

// firewallStep applies the firewall now (listen ports of the tunnels, NAT
// of the active candidates) and reports it; with security.firewall_managed
// false it is skipped (suggestions only, DEY-P031).
func (h *Hub) firewallStep(ctx context.Context, rep *steps) error {
	if !firewallManaged(h.Config()) {
		rep.emit(api.Step{ID: stepFirewall, Status: api.StepSkipped, Detail: deyerr.New(deyerr.P031, nil).Message()})
		h.requestFirewall()
		return nil
	}
	return rep.run(stepFirewall, func() (string, error) {
		if err := h.applyFirewall(ctx); err != nil {
			return "", err
		}
		return listenDetail(h.Firewall().Spec.ListenTCP, h.Firewall().Spec.ListenUDP), nil
	})
}

// listenDetail is "tcp 443, 2053; udp 27015".
func listenDetail(tcp, udp []int) string {
	join := func(ps []int) string {
		s := append([]int(nil), ps...)
		sort.Ints(s)
		parts := make([]string, len(s))
		for i, p := range s {
			parts[i] = strconv.Itoa(p)
		}
		return strings.Join(parts, ", ")
	}
	var out []string
	if len(tcp) > 0 {
		out = append(out, "tcp "+join(tcp))
	}
	if len(udp) > 0 {
		out = append(out, "udp "+join(udp))
	}
	return strings.Join(out, "; ")
}

// waitUp reports the start and probe steps of a new tunnel: it waits until
// the engine started its first candidate, then until the tunnel is UP (or
// DOWN, or TunnelUpWait passed), and reports "Tunnel <id> is UP via
// <transport> (<rtt>ms)". A tunnel that does not come up returns the last
// DEY error of its attempts (it stays configured and keeps trying).
func (h *Hub) waitUp(ctx context.Context, c *tunnelCtl, rep *steps) error {
	deadline := time.NewTimer(h.o.TunnelUpWait)
	defer deadline.Stop()
	poll := time.NewTicker(upPoll)
	defer poll.Stop()
	timeout := func() error {
		st := c.engineState()
		c.mu.Lock()
		if c.lastErr == nil {
			c.lastErr = deyerr.New(deyerr.B004, deyerr.Params{"transport": st.Active.Transport, "seconds": int(h.o.TunnelUpWait / time.Second)}).
				WithDetail(st.TransitionCause)
		}
		c.mu.Unlock()
		return c.failure()
	}
	gone := func() error { return deyerr.New(deyerr.F007, deyerr.Params{"tunnel": c.id}) }
	rep.emit(api.Step{ID: stepStart, Status: api.StepRunning})
	var startErr error
	for started := false; !started; {
		select {
		case startErr = <-c.first:
			started = true
		case <-poll.C:
			st, ok := c.liveState()
			if !ok || c.isStopped() {
				return gone()
			}
			switch st.State {
			case state.StateUp, state.StateDegraded, state.StateDown, state.StatePaused:
				started = true
			}
		case <-deadline.C:
			err := timeout()
			rep.emit(api.Step{ID: stepStart, Status: api.StepFailed, Error: api.ToDTO(err)})
			return err
		case <-ctx.Done():
			return deyerr.Wrap(deyerr.X031, ctx.Err(), deyerr.Params{"command": "tunnel add"})
		}
	}
	st := c.engineState()
	if startErr != nil {
		rep.emit(api.Step{ID: stepStart, Status: api.StepWarn, Detail: "trying the next rung", Error: api.ToDTO(startErr)})
	} else {
		rep.emit(api.Step{ID: stepStart, Status: api.StepOK, Detail: st.Active.Transport + " on " + st.Active.Node})
	}
	rep.emit(api.Step{ID: stepProbe, Status: api.StepRunning})
	for {
		st, ok := c.liveState()
		if !ok || c.isStopped() {
			return gone()
		}
		switch st.State {
		case state.StateUp, state.StateDegraded:
			rep.emit(api.Step{ID: stepProbe, Status: api.StepOK, Detail: strconv.Itoa(st.LastRTTms) + "ms"})
			rep.emitTitled(api.Step{ID: stepUp, Status: api.StepOK,
				Title: i18n.T(i18n.HubTitleTunnelUp, c.id, st.Active.Transport, st.LastRTTms)})
			return nil
		case state.StateDown:
			err := c.failure()
			if err == nil {
				err = deyerr.New(deyerr.F001, deyerr.Params{"tunnel": c.id}).WithDetail(st.TransitionCause)
			}
			rep.emit(api.Step{ID: stepProbe, Status: api.StepFailed, Error: api.ToDTO(err)})
			return err
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			err := timeout()
			rep.emit(api.Step{ID: stepProbe, Status: api.StepFailed, Error: api.ToDTO(err)})
			return err
		case <-ctx.Done():
			return deyerr.Wrap(deyerr.X031, ctx.Err(), deyerr.Params{"command": "tunnel add"})
		}
	}
}

// isStopped reports whether the controller was stopped.
func (c *tunnelCtl) isStopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

// ---------------------------------------------------------------- Local API

// TunnelAdd implements api.Local (section 6 Add tunnel, section 14 tunnel
// add): the request is checked (nodes, ports: DEY-P011/C003/P012, ladder,
// settings), config.yaml gets the tunnel after an automatic backup, and the
// progress steps follow: install backend on hub, install on node, render,
// firewall, start, probe, then "Tunnel <id> is UP via <transport>
// (<rtt>ms)". When a step fails its DEY error is returned and the tunnel
// stays configured (Retry = TunnelRestart); it keeps trying on its own.
func (l *local) TunnelAdd(ctx context.Context, req api.TunnelAddRequest, progress func(api.Step)) (api.TunnelInfo, error) {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	defer unlock()
	rep := &steps{progress: progress}
	t, err := h.newTunnel(ctx, h.Config(), req)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error { return c.AddTunnel(t) }); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	h.log.Info("tunnel added", dlog.Tunnel(t.ID), slog.String("nodes", strings.Join(t.Nodes, ",")),
		slog.String("ladder", t.Ladder.String()), slog.Int("ports", len(t.Ports)))
	c, err := h.startCtl(ctx, t.ID, rep)
	unlock()
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if err := h.waitUp(ctx, c, rep); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	return h.tunnelInfoOf(t.ID), nil
}

// TunnelList implements api.Local: every tunnel (config order) with its
// live state.
func (l *local) TunnelList(context.Context) ([]api.TunnelInfo, error) {
	return l.h.tunnelInfos(l.h.Config()), nil
}

// TunnelShow implements api.Local (`deyroute tunnel show`): ports, ladder
// with every rung on every node (warm, active, control port, skip reason,
// quarantine, unit state), failover settings, the probe history of the
// active candidate, metrics and the last events.
func (l *local) TunnelShow(ctx context.Context, id string) (api.TunnelDetail, error) {
	h := l.h
	cfg, t, err := h.configTunnel(id)
	if err != nil {
		return api.TunnelDetail{}, withLog(err)
	}
	info := h.tunnelInfo(cfg, &t)
	ts, _ := h.tunnelState(t.ID)
	d := api.TunnelDetail{
		TunnelInfo:    info,
		Failover:      failoverDTO(t.Failover),
		TLSMode:       t.TLS.Mode,
		FailbackDelay: ts.FailbackDelay,
		Rungs:         []api.RungStatus{},
		Probes:        []state.ProbeSample{},
		Events:        []state.Event{},
	}
	if pm, ok := t.ProbeTarget(); ok {
		d.ProbePort = pm.Listen
	}
	units := map[string]string{}
	if list, err := h.o.Systemd.ListInstances(ctx); err == nil {
		for _, u := range list {
			units[u.Unit] = u.ActiveState
		}
	}
	ctlPorts, _ := h.st.CtlPorts()
	var planned func(node, tr string) (int, bool)
	if c := h.tun.lookup(t.ID); c != nil {
		_, plan := c.snapshot()
		planned = func(node, tr string) (int, bool) {
			if pc, ok := plan.Candidate(node, tr); ok {
				return pc.ControlPort, true
			}
			return 0, false
		}
	}
	now := h.now()
	for _, n := range t.Nodes {
		for _, r := range info.Ladder {
			sc := state.Candidate{Node: n, Transport: r}
			unit := systemd.UnitName(systemd.InstanceName(t.ID, n, r))
			rs := api.RungStatus{Node: n, Transport: r, Unit: unit, UnitState: units[unit]}
			rs.ControlPort = ctlPorts[state.Key(t.ID, n, r)]
			if sk, ok := ts.Skipped[sc.Key()]; ok {
				rs.Skipped = firstNonEmpty(sk.Reason, sk.Code)
			}
			if q, ok := ts.Quarantine[sc.Key()]; ok && q.Until.After(now) {
				rs.Quarantine = q.Until
			}
			if planned != nil {
				if p, ok := planned(n, r); ok {
					rs.ControlPort, rs.Warm = p, rs.Skipped == ""
				}
			} else if _, ok := units[unit]; ok {
				rs.Warm = rs.Skipped == ""
			}
			rs.Active = ts.Active == sc && runningState(ts.State)
			d.Rungs = append(d.Rungs, rs)
		}
	}
	if !ts.Active.IsZero() {
		if probes, err := h.st.Probes(t.ID, ts.Active.Node, ts.Active.Transport); err == nil && probes != nil {
			d.Probes = probes
		}
	}
	if m, ok, err := h.st.GetMetrics(t.ID); err == nil && ok {
		d.Metrics = &m
	}
	if evs, err := h.st.Events(state.EventFilter{Tunnel: t.ID, Limit: tunnelDetailEvents}); err == nil && evs != nil {
		d.Events = evs
	}
	return d, nil
}

// TunnelEdit implements api.Local (`deyroute tunnel edit`, `deyroute port set`,
// Failover menu, Ports → Probe kind): name, ladder (profile or inline
// rungs), node order, policy, probe port, the probe kind of port maps, TLS
// mode (custom certificate files are validated) and failover settings.
// config.yaml changes after an automatic backup; the tunnel is re-planned
// and the engine takes the new ladder, nodes and settings (an active rung
// that is gone moves to rung 1; changed files restart the active transport).
func (l *local) TunnelEdit(ctx context.Context, id string, req api.TunnelEditRequest, progress func(api.Step)) (api.TunnelInfo, error) {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	defer unlock()
	cfg, t, err := h.configTunnel(id)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	next, err := h.editTunnel(cfg, t, req)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		tp, ok := c.Tunnel(t.ID)
		if !ok {
			return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
		}
		*tp = next.Clone()
		return nil
	}); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	h.log.Info("tunnel edited", dlog.Tunnel(t.ID))
	if c := h.tun.lookup(t.ID); c != nil {
		rep := &steps{progress: progress}
		if err := c.update(ctx, updateOpts{rep: rep, restartActive: true}); err != nil {
			return api.TunnelInfo{}, withLog(err)
		}
	}
	return h.tunnelInfoOf(t.ID), nil
}

// editTunnel applies an edit request to t and checks it.
func (h *Hub) editTunnel(cfg *config.Config, t config.Tunnel, req api.TunnelEditRequest) (config.Tunnel, error) {
	next := t.Clone()
	if req.Name != nil {
		name := cleanName(*req.Name)
		if name == "" {
			return next, deyerr.New(deyerr.C013, deyerr.Params{"field": "tunnels[" + t.ID + "].name", "value": *req.Name, "allowed": "1 to 64 printable characters"})
		}
		next.Name = name
	}
	switch rungs := cleanRungs(req.Rungs); {
	case len(rungs) > 0:
		for _, r := range rungs {
			if _, _, err := backend.Lookup(r); err != nil {
				return next, err
			}
		}
		next.Ladder = config.LadderRef{Inline: rungs}
	case req.Ladder != nil:
		name := strings.TrimSpace(*req.Ladder)
		if name == "" {
			name = config.DefaultLadderName
		}
		if _, ok := cfg.LadderProfile(name); !ok {
			return next, deyerr.New(deyerr.C012, deyerr.Params{"ladder": name, "tunnel": t.ID})
		}
		next.Ladder = config.LadderRef{Name: name}
	}
	if req.Nodes != nil {
		nodes, err := checkNodes(cfg, t.ID, req.Nodes)
		if err != nil {
			return next, err
		}
		next.Nodes = nodes
	}
	if req.Failover != nil {
		overlayFailover(&next.Failover, *req.Failover)
	}
	if req.Policy != nil {
		next.Failover.Policy = strings.TrimSpace(*req.Policy)
	}
	if err := checkPolicy(next.Failover.Policy); err != nil {
		return next, err
	}
	for _, s := range req.PortProbes {
		if err := setPortProbe(&next, s); err != nil {
			return next, err
		}
	}
	if req.ProbePort != nil {
		next.ProbePort = *req.ProbePort
		if p := next.ProbePort; p != 0 {
			if pm, ok := next.ProbeTarget(); !ok || pm.Listen != p {
				return next, deyerr.New(deyerr.C013, deyerr.Params{"field": "tunnels[" + t.ID + "].probe_port", "value": p, "allowed": "a TCP listen port of this tunnel, or 0 for the first one"})
			}
		}
	}
	if req.TLSMode != nil {
		next.TLS.Mode = strings.TrimSpace(*req.TLSMode)
	}
	if req.TLSCert != nil {
		next.TLS.CertFile = strings.TrimSpace(*req.TLSCert)
	}
	if req.TLSKey != nil {
		next.TLS.KeyFile = strings.TrimSpace(*req.TLSKey)
	}
	switch next.TLS.Mode {
	case config.TLSModeCustom:
		if err := tlsutil.ValidateCustom(next.TLS.CertFile, next.TLS.KeyFile, h.now()); err != nil {
			return next, err
		}
	case config.TLSModeAuto, config.TLSModeACME:
		next.TLS.CertFile, next.TLS.KeyFile = "", ""
	}
	if req.TLSMode != nil {
		if err := checkACMEDomain(cfg, t.ID, next.TLS.Mode); err != nil {
			return next, err
		}
	}
	if _, err := cfg.ResolveLadder(&next, supports); err != nil {
		return next, err
	}
	return next, nil
}

// setPortProbe sets the probe kind of the existing port map s names
// (`deyroute port set --probe`, Ports → Probe kind): a port the tunnel does
// not have and a kind config.yaml would refuse are DEY-C013.
func setPortProbe(t *config.Tunnel, s api.PortSpec) error {
	proto, err := normalizeProto(s.Listen, s.Proto)
	if err != nil {
		return err
	}
	key := config.ListenKey{Port: s.Listen, Proto: proto}
	idx := slices.IndexFunc(t.Ports, func(pm config.PortMap) bool { return pm.Listen == s.Listen && pm.Proto == proto })
	if idx < 0 {
		return deyerr.New(deyerr.C013, deyerr.Params{
			"field": "tunnels[" + t.ID + "].ports", "value": key.String(), "allowed": "a port of the tunnel: " + formatMaps(t.Ports),
		})
	}
	probe, err := probeOf("tunnels["+t.ID+"].ports["+key.String()+"].probe", proto, s.Probe)
	if err != nil {
		return err
	}
	t.Ports[idx].Probe = probe
	return nil
}

// TunnelSetEnabled implements api.Local (`deyroute tunnel enable|disable`):
// disabling stops the engine and the tunnel's units (state DISABLED, the
// listen ports leave the firewall; the rungs stay warm); enabling starts
// the controller again from rung 1.
func (l *local) TunnelSetEnabled(ctx context.Context, id string, enabled bool) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	_, t, err := h.configTunnel(id)
	if err != nil {
		return withLog(err)
	}
	if t.Enabled != enabled {
		if _, err := h.autoBackup(); err != nil {
			return withLog(err)
		}
		if _, err := h.mutate(func(c *config.Config) error {
			tp, ok := c.Tunnel(t.ID)
			if !ok {
				return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
			}
			tp.Enabled = enabled
			return nil
		}); err != nil {
			return withLog(err)
		}
	}
	if !enabled {
		if c := h.tun.remove(t.ID); c != nil {
			c.stop(ctx, true)
			h.persistDisabled(t.ID, c.engineState())
		} else {
			units, _ := h.o.Systemd.ListInstances(ctx)
			h.ensureTunnelStopped(ctx, t.ID, t.Nodes, units)
			ts, _ := h.tunnelState(t.ID)
			h.persistDisabled(t.ID, ts)
		}
		h.log.Info("tunnel disabled", dlog.Tunnel(t.ID))
		return withLog(h.firewallStep(ctx, nil))
	}
	if h.tun.lookup(t.ID) == nil {
		if _, err := h.startCtl(ctx, t.ID, nil); err != nil {
			h.log.Warn("tunnel enabled with problems", dlog.Tunnel(t.ID), dlog.Err(err))
			return withLog(err)
		}
	}
	h.log.Info("tunnel enabled", dlog.Tunnel(t.ID))
	return withLog(h.firewallStep(ctx, nil))
}

// TunnelRestart implements api.Local (`deyroute tunnel restart`, the Retry of
// a failed Add tunnel): the controller stops with its units, failed
// installations are retried, the failover history (quarantine, switch
// counts, failback delay) is cleared and the tunnel starts again at rung 1.
func (l *local) TunnelRestart(ctx context.Context, id string) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	_, t, err := h.configTunnel(id)
	if err != nil {
		return withLog(err)
	}
	if !t.Enabled {
		return withLog(deyerr.New(deyerr.F007, deyerr.Params{"tunnel": t.ID}))
	}
	if c := h.tun.remove(t.ID); c != nil {
		c.stop(ctx, true)
	}
	h.forgetInstallFailures(nil)
	ts, _, _ := h.st.GetTunnel(t.ID)
	fresh := state.TunnelState{ID: t.ID, Skipped: ts.Skipped, TransitionCause: "restarted by the owner", UpdatedAt: h.now()}
	if err := h.st.PutTunnel(fresh); err != nil {
		return withLog(err)
	}
	h.log.Info("tunnel restarting", dlog.Tunnel(t.ID))
	if _, err := h.startCtl(ctx, t.ID, nil); err != nil {
		return withLog(err)
	}
	return nil
}

// TunnelDelete implements api.Local (`deyroute tunnel delete`; section 7.3
// step 6): the engine stops, every instance of the tunnel is stopped and
// removed on the hub and on its nodes (files, drop-ins, canary), the entry
// leaves config.yaml (after an automatic backup), the firewall drops its
// ports and NAT, and its secrets and state go. Nothing is left behind; a
// node that is offline is cleaned when it reconnects.
func (l *local) TunnelDelete(ctx context.Context, id string, progress func(api.Step)) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	cfg, t, err := h.configTunnel(id)
	if err != nil {
		return withLog(err)
	}
	// The config step comes after the units are gone: an edit of
	// config.yaml that is not applied must stop the delete before them.
	if err := h.checkApplied(cfg); err != nil {
		return withLog(err)
	}
	rep := &steps{progress: progress}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	ladder := ladderOf(cfg, &t)
	units, _ := h.o.Systemd.ListInstances(ctx)
	if err := rep.run(stepStop, func() (string, error) {
		if c := h.tun.remove(t.ID); c != nil {
			c.stop(ctx, true)
		}
		h.ensureTunnelStopped(ctx, t.ID, t.Nodes, units)
		return "", nil
	}); err != nil {
		return withLog(err)
	}
	if err := rep.runWarn(stepRemove, func() (string, error, error) {
		return h.removeTunnelUnits(ctx, t, ladder, units)
	}); err != nil {
		return withLog(err)
	}
	if err := rep.run(stepConfig, func() (string, error) {
		_, err := h.mutate(func(c *config.Config) error {
			if !c.RemoveTunnel(t.ID) {
				return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
			}
			return nil
		})
		return h.cfgPath, err
	}); err != nil {
		return withLog(err)
	}
	if err := h.firewallStep(ctx, rep); err != nil {
		h.log.Warn("firewall not updated after a tunnel delete", dlog.Tunnel(t.ID), dlog.Err(err))
	}
	_ = rep.run(stepCleanup, func() (string, error) {
		h.forgetTunnel(t.ID)
		return "", nil
	})
	h.log.Info("tunnel deleted", dlog.Tunnel(t.ID))
	return nil
}

// removeTunnelUnits removes every instance of a tunnel: each rung on each
// node (hub and node side), the canary, units systemd still lists, and the
// tunnel's rendered directories. Offline nodes are reported as a warning.
func (h *Hub) removeTunnelUnits(ctx context.Context, t config.Tunnel, ladder []string, units []systemd.UnitState) (string, error, error) {
	insts := map[string]bool{systemd.CanaryInstance(t.ID): true}
	for _, n := range t.Nodes {
		for _, r := range ladder {
			insts[systemd.InstanceName(t.ID, n, r)] = true
		}
	}
	for _, u := range units {
		if in, err := systemd.ParseInstance(u.Instance); err == nil && in.Tunnel == t.ID {
			insts[u.Instance] = true
		}
	}
	var warn error
	removed := 0
	for _, inst := range sortedKeys(insts) {
		if err := h.removeHubInstance(ctx, inst); err != nil {
			warn = err
			continue
		}
		removed++
	}
	var offline []string
	var echo int
	if ok, err := h.st.GetMeta(metaCanaryEcho+t.ID, &echo); err != nil || !ok {
		echo = 0
	}
	for _, n := range t.Nodes {
		if !h.Online(n) {
			offline = append(offline, n)
			if echo > 0 && n == t.Nodes[0] {
				// The canary echo on the primary is stopped when it is back.
				h.addEchoOrphan(n, echo)
			}
			continue
		}
		nodeInsts := map[string]bool{systemd.CanaryInstance(t.ID): n == t.Nodes[0]}
		for _, r := range ladder {
			nodeInsts[systemd.InstanceName(t.ID, n, r)] = true
		}
		ns, _ := h.nodeState(n)
		for unit := range ns.Units {
			if inst, ok := systemd.InstanceOf(unit); ok {
				if in, err := systemd.ParseInstance(inst); err == nil && in.Tunnel == t.ID && (in.Canary || in.Node == n) {
					nodeInsts[inst] = true
				}
			}
		}
		for _, inst := range sortedKeys(nodeInsts) {
			if !nodeInsts[inst] {
				continue
			}
			in, err := systemd.ParseInstance(inst)
			if err != nil {
				continue
			}
			if err := h.removeNodeInstance(ctx, n, in); err != nil {
				warn = err
			}
		}
		if echo > 0 && n == t.Nodes[0] {
			cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
			if err := h.Call(cctx, n, api.CmdEchoStop, api.EchoArgs{Port: echo}, nil); deyerr.HasCode(err, deyerr.N003) {
				h.addEchoOrphan(n, echo)
			}
			cancel()
		}
	}
	if err := h.removeTunnelDirs(t.ID); err != nil {
		warn = err
	}
	if err := h.o.Systemd.DaemonReload(ctx); err != nil && warn == nil {
		warn = err
	}
	detail := fmt.Sprintf("%d units", removed)
	if len(offline) > 0 {
		detail += "; offline (cleaned when they reconnect): " + strings.Join(offline, ", ")
		if warn == nil {
			warn = deyerr.New(deyerr.N003, deyerr.Params{"node": offline[0]})
		}
	}
	return detail, warn, nil
}

// TunnelSwitch implements api.Local (`deyroute tunnel switch --transport |
// --node`): a manual switch through the failover engine (always allowed,
// not counted by the anti-flapping limit, section 8).
func (l *local) TunnelSwitch(ctx context.Context, id string, req api.SwitchRequest) error {
	c, e, err := l.h.runningCtl(id)
	if err != nil {
		return withLog(err)
	}
	tr, node := strings.TrimSpace(req.Transport), strings.TrimSpace(req.Node)
	switch {
	case tr != "" && node != "", tr == "" && node == "":
		return withLog(deyerr.New(deyerr.C013, deyerr.Params{"field": "switch", "value": tr + node, "allowed": "exactly one of --transport <id> or --node <id>"}))
	case tr != "":
		err = e.SwitchTransport(ctx, tr)
	default:
		err = e.SwitchNode(ctx, node)
	}
	l.h.log.Info("manual switch", dlog.Tunnel(c.id), dlog.Transport(tr), dlog.Node(node), dlog.Err(err))
	return withLog(err)
}

// TunnelReset implements api.Local (`deyroute tunnel reset`): back to rung 1
// of the primary node now.
func (l *local) TunnelReset(ctx context.Context, id string) error {
	c, e, err := l.h.runningCtl(id)
	if err != nil {
		return withLog(err)
	}
	err = e.Reset(ctx)
	l.h.log.Info("tunnel reset to rung 1", dlog.Tunnel(c.id), dlog.Err(err))
	return withLog(err)
}

// TunnelPause implements api.Local (`deyroute tunnel pause`): automatic
// switching stops (maintenance); probing goes on.
func (l *local) TunnelPause(ctx context.Context, id string) error {
	c, e, err := l.h.runningCtl(id)
	if err != nil {
		return withLog(err)
	}
	err = e.Pause(ctx)
	l.h.log.Info("failover paused", dlog.Tunnel(c.id), dlog.Err(err))
	return withLog(err)
}

// TunnelResume implements api.Local (`deyroute tunnel resume`).
func (l *local) TunnelResume(ctx context.Context, id string) error {
	c, e, err := l.h.runningCtl(id)
	if err != nil {
		return withLog(err)
	}
	err = e.Resume(ctx)
	l.h.log.Info("failover resumed", dlog.Tunnel(c.id), dlog.Err(err))
	return withLog(err)
}

// TunnelTestLadder implements api.Local (`deyroute tunnel test-ladder`, S18):
// every rung on every node is tried for up to 20 s (start, probe, RTT,
// stop), one progress line per rung, then the original candidate returns.
func (l *local) TunnelTestLadder(ctx context.Context, id string, progress func(api.Step)) ([]api.RungResult, error) {
	c, e, err := l.h.runningCtl(id)
	if err != nil {
		return nil, withLog(err)
	}
	rep := &steps{progress: progress}
	results, err := e.TestLadder(ctx, func(r api.RungResult) {
		st := api.Step{ID: stepRungPrefix + r.Node + "/" + r.Transport, Title: i18n.T(i18n.HubTitleRung, r.Transport, r.Node)}
		switch {
		case r.Skipped != "":
			st.Status, st.Detail = api.StepSkipped, r.Skipped
		case r.OK:
			st.Status, st.Detail = api.StepOK, strconv.Itoa(r.RTTms)+"ms"
		default:
			st.Status, st.Error = api.StepFailed, r.Error
			if r.Error != nil {
				st.Detail = r.Error.Message
			}
		}
		rep.emitTitled(st)
	})
	l.h.log.Info("ladder test finished", dlog.Tunnel(c.id), slog.Int("rungs", len(results)), dlog.Err(err))
	if results == nil {
		results = []api.RungResult{}
	}
	return results, withLog(err)
}

// TunnelBackupAdd implements api.Local (Add backup node, `deyroute tunnel
// backup add`): the node joins the tunnel's node list and every rung is
// installed and rendered there (warm), then "backup <node> ready (warm)".
func (l *local) TunnelBackupAdd(ctx context.Context, id, node string, progress func(api.Step)) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	cfg, t, err := h.configTunnel(id)
	if err != nil {
		return withLog(err)
	}
	node = strings.TrimSpace(node)
	if _, ok := cfg.NodeByID(node); !ok {
		return withLog(deyerr.New(deyerr.N008, deyerr.Params{"node": node}))
	}
	if slices.Contains(t.Nodes, node) {
		return withLog(deyerr.New(deyerr.C002, deyerr.Params{"kind": "tunnel " + t.ID + " node", "id": node}))
	}
	if !h.Online(node) {
		return withLog(deyerr.New(deyerr.N003, deyerr.Params{"node": node}))
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		tp, ok := c.Tunnel(t.ID)
		if !ok {
			return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
		}
		tp.Nodes = append(tp.Nodes, node)
		return nil
	}); err != nil {
		return withLog(err)
	}
	rep := &steps{progress: progress}
	c := h.tun.lookup(t.ID)
	var skips map[string]state.Skip
	if c != nil {
		if err := c.update(ctx, updateOpts{rep: rep, only: node, add: true}); err != nil {
			return withLog(err)
		}
		skips = c.engineState().Skipped
	} else {
		// A disabled tunnel: the node's rungs are made warm all the same.
		tmp := newTunnelCtl(h, t.ID)
		res, err := tmp.apply(ctx, applyOpts{rep: rep, only: node, add: true})
		if err == nil {
			err = res.fatal
		}
		if err != nil {
			return withLog(err)
		}
		skips = res.skips
	}
	warm, total := 0, 0
	for _, r := range ladderOf(h.Config(), &t) {
		total++
		if _, skipped := skips[state.Candidate{Node: node, Transport: r}.Key()]; !skipped {
			warm++
		}
	}
	rep.emitTitled(api.Step{ID: stepBackupReady, Status: api.StepOK, Title: i18n.T(i18n.HubTitleBackupReady, node),
		Detail: fmt.Sprintf("%d of %d rungs warm", warm, total)})
	h.log.Info("backup node added", dlog.Tunnel(t.ID), dlog.Node(node))
	return nil
}

// TunnelBackupRemove implements api.Local (`deyroute tunnel backup remove`):
// the node leaves the tunnel (the engine moves away when it was active
// there) and its rungs are removed on the node and on the hub. The last
// node cannot be removed (DEY-C008).
func (l *local) TunnelBackupRemove(ctx context.Context, id, node string) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	cfg, t, err := h.configTunnel(id)
	if err != nil {
		return withLog(err)
	}
	node = strings.TrimSpace(node)
	if !slices.Contains(t.Nodes, node) {
		return withLog(deyerr.New(deyerr.C010, deyerr.Params{"node": node, "tunnel": t.ID}))
	}
	if len(t.Nodes) == 1 {
		return withLog(deyerr.New(deyerr.C008, deyerr.Params{"tunnel": t.ID}))
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		tp, ok := c.Tunnel(t.ID)
		if !ok {
			return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
		}
		tp.Nodes = slices.DeleteFunc(tp.Nodes, func(n string) bool { return n == node })
		return nil
	}); err != nil {
		return withLog(err)
	}
	if c := h.tun.lookup(t.ID); c != nil {
		if err := c.update(ctx, updateOpts{}); err != nil {
			h.log.Warn("tunnel update after removing a backup node failed", dlog.Tunnel(t.ID), dlog.Err(err))
		}
	}
	if err := h.removeNodeCandidates(ctx, t.ID, node, ladderOf(cfg, &t)); err != nil {
		h.log.Warn("some units of the removed backup node could not be cleaned up", dlog.Tunnel(t.ID), dlog.Node(node), dlog.Err(err))
	}
	h.log.Info("backup node removed", dlog.Tunnel(t.ID), dlog.Node(node))
	return nil
}
