package hub

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/state"
)

// Port operation limits and texts (section 10).
const (
	// PortCheckNote is the note of stage 3 of the port check: reachability
	// from a node proves the port is open from the internet, not that it is
	// usable inside Iran.
	PortCheckNote = "reachable from the node means open from the internet; filtering inside Iran is not measured"
	// DefaultSuggestCount is the number of ports PortSuggest returns by
	// default; MaxSuggestCount caps it.
	DefaultSuggestCount = 3
	MaxSuggestCount     = 64
	// portCheckTimeout bounds stage 3 (the node connects to the hub).
	portCheckTimeout = 5 * time.Second
)

// normalizeProto returns "tcp" for "" and DEY-P010 for anything but tcp/udp.
func normalizeProto(port int, proto string) (string, error) {
	if strings.TrimSpace(proto) == "" {
		return config.ProtoTCP, nil
	}
	p, ok := ports.NormalizeProto(proto)
	if !ok {
		return "", deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(port) + "/" + proto})
	}
	return p, nil
}

// PortAdd implements api.Local (`deyroute port add`, Ports → add port to
// tunnel): the new ports pass the section 10 rules (reserved DEY-P011,
// another tunnel DEY-C003, bound on the hub DEY-P012; at most 64 maps
// DEY-C015), config.yaml changes after an automatic backup, the firewall
// opens them, the tunnel is rendered again and only the active transport
// restarts (an outage of at most a few seconds).
func (l *local) PortAdd(ctx context.Context, tunnel string, specs []api.PortSpec, progress func(api.Step)) (api.TunnelInfo, error) {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	defer unlock()
	cfg, t, err := h.configTunnel(tunnel)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	rep := &steps{progress: progress}
	maps, err := portMaps(specs)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if err := rep.run(stepCheckPorts, func() (string, error) {
		switch {
		case len(maps) == 0:
			return "", deyerr.New(deyerr.C013, deyerr.Params{"field": "ports", "value": "", "allowed": "at least one port, e.g. 8443"})
		case len(t.Ports)+len(maps) > config.MaxPortMaps:
			return "", deyerr.New(deyerr.C015, deyerr.Params{"tunnel": t.ID, "count": len(t.Ports) + len(maps)})
		}
		if err := h.checkNewPorts(ctx, cfg, t.ID, maps, t.Ports); err != nil {
			return "", err
		}
		return formatMaps(maps), nil
	}); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	h.extFirewallStep(ctx, rep, maps)
	if _, err := h.autoBackup(); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		tp, ok := c.Tunnel(t.ID)
		if !ok {
			return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
		}
		tp.Ports = append(tp.Ports, maps...)
		return nil
	}); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	h.log.Info("ports added", dlog.Tunnel(t.ID), slog.String("ports", formatMaps(maps)))
	if t.Enabled {
		if err := h.firewallStep(ctx, rep); err != nil {
			return api.TunnelInfo{}, withLog(err)
		}
	}
	if c := h.tun.lookup(t.ID); c != nil {
		if err := c.update(ctx, updateOpts{rep: rep, restartActive: true, quietInstall: true}); err != nil {
			return api.TunnelInfo{}, withLog(err)
		}
	}
	return h.tunnelInfoOf(t.ID), nil
}

// formatMaps is "443/tcp, 27015/udp".
func formatMaps(maps []config.PortMap) string {
	parts := make([]string, len(maps))
	for i, pm := range maps {
		parts[i] = strconv.Itoa(pm.Listen) + "/" + pm.Proto
	}
	return strings.Join(parts, ", ")
}

// PortRemove implements api.Local (`deyroute port remove`): the port map
// leaves the tunnel (a tunnel keeps at least one), the firewall closes it
// and only the active transport restarts.
func (l *local) PortRemove(ctx context.Context, tunnel string, listen int, proto string) (api.TunnelInfo, error) {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	defer unlock()
	_, t, err := h.configTunnel(tunnel)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	p, err := normalizeProto(listen, proto)
	if err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	idx := slices.IndexFunc(t.Ports, func(pm config.PortMap) bool { return pm.Listen == listen && pm.Proto == p })
	if idx < 0 {
		return api.TunnelInfo{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "tunnels[" + t.ID + "].ports", "value": strconv.Itoa(listen) + "/" + p, "allowed": "a port of the tunnel: " + formatMaps(t.Ports),
		}))
	}
	if len(t.Ports) == 1 {
		return api.TunnelInfo{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "tunnels[" + t.ID + "].ports", "value": strconv.Itoa(listen) + "/" + p,
			"allowed": "at least one port map (delete the tunnel instead: deyroute tunnel delete " + t.ID + ")",
		}))
	}
	if _, err := h.autoBackup(); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		tp, ok := c.Tunnel(t.ID)
		if !ok {
			return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": t.ID})
		}
		tp.Ports = slices.DeleteFunc(tp.Ports, func(pm config.PortMap) bool { return pm.Listen == listen && pm.Proto == p })
		if tp.ProbePort == listen {
			tp.ProbePort = 0
		}
		return nil
	}); err != nil {
		return api.TunnelInfo{}, withLog(err)
	}
	h.log.Info("port removed", dlog.Tunnel(t.ID), slog.String("port", strconv.Itoa(listen)+"/"+p))
	if c := h.tun.lookup(t.ID); c != nil {
		if err := c.update(ctx, updateOpts{restartActive: true}); err != nil {
			return api.TunnelInfo{}, withLog(err)
		}
	}
	if err := h.firewallStep(ctx, nil); err != nil {
		h.log.Warn("firewall not updated after a port removal", dlog.Tunnel(t.ID), dlog.Err(err))
	}
	return h.tunnelInfoOf(t.ID), nil
}

// PortCheck implements api.Local (`deyroute port check`, Ports → Check port;
// section 10): 1. local bind (free, or the process holding it; a deyroute
// unit names its tunnel), 2. the external firewall (open, or the command
// that opens it), 3. a TCP connect from a node to the hub's public ip:port
// (the requested node, else the tunnel's active node, else the first online
// node; a TCP port it cannot connect to carries DEY-P014 in NodeError), 4.
// for a tunnel port, the path probe through the tunnel. Free ports are
// suggested when the port is busy or closed.
func (l *local) PortCheck(ctx context.Context, req api.PortCheckRequest) (api.PortCheckResult, error) {
	h := l.h
	proto, err := normalizeProto(req.Port, req.Proto)
	if err != nil {
		return api.PortCheckResult{}, withLog(err)
	}
	if !ports.ValidPort(req.Port) {
		return api.PortCheckResult{}, withLog(deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(req.Port)}))
	}
	cfg := h.Config()
	res := api.PortCheckResult{Port: req.Port, Proto: proto}
	owner, tunnelPort := cfg.UsedListenPorts()[config.ListenKey{Port: req.Port, Proto: proto}]
	notes := []string{PortCheckNote}

	// 1. local bind.
	b := h.o.BindCheck(ctx, req.Port, proto)
	res.BindFree, res.BindProcess, res.BindAddr, res.BindByDey = b.Free, b.Process, b.Addr, b.Deyroute
	if reserved, why := ports.Reserved(req.Port, cfg.Hub.ControlPort, cfg.Hub.ReservedPorts()...); reserved {
		notes = append(notes, why)
	}

	// 2. firewall.
	fw := h.firewallStage(ctx, cfg, req.Port, proto, tunnelPort)
	res.FirewallOpen, res.FirewallName, res.FirewallCommand = fw.open, fw.name, fw.command
	if fw.note != "" {
		notes = append(notes, fw.note)
	}

	// 3. reachable from a node.
	node := strings.TrimSpace(req.Node)
	if node == "" && tunnelPort {
		if ts, ok := h.tunnelState(owner); ok && h.Online(ts.Active.Node) {
			node = ts.Active.Node
		}
	}
	if node == "" {
		if online := h.onlineNodes(); len(online) > 0 {
			node = online[0]
		}
	}
	if node != "" {
		res.Node = node
		if proto == config.ProtoTCP && res.BindFree {
			// Nothing listens on a free port, so the node's connect would
			// be refused even with the firewall open: answer it for the
			// length of the test.
			if stop := tempTCPListener(req.Port); stop != nil {
				defer stop()
			}
		}
		var pr api.ProbeResultDTO
		cctx, cancel := context.WithTimeout(ctx, portCheckTimeout+nodeCmdTimeout)
		err := h.Call(cctx, node, api.CmdPortCheckRemote, api.PortCheckArgs{
			IP: cfg.Hub.PublicIP, Port: req.Port, Proto: proto, TimeoutMs: int(portCheckTimeout / time.Millisecond),
		}, &pr)
		cancel()
		switch {
		case err != nil:
			notes = append(notes, "node "+node+" could not test: "+deyerr.As(err).Message())
		case proto == config.ProtoUDP && !pr.OK:
			notes = append(notes, pr.Error)
		default:
			ok := pr.OK
			res.NodeReachable, res.NodeRTTms = &ok, pr.RTTms
			if !ok {
				res.NodeError = h.portUnreachable(req.Port, proto, node, pr.Error)
			}
		}
	}

	// 4. reachable through the tunnel.
	if tunnelPort {
		res.Tunnel = owner
		if proto == config.ProtoTCP {
			r := h.probeTunnelPort(ctx, owner, req.Port)
			ok := r.OK
			res.TunnelOK, res.TunnelRTTms = &ok, int(r.RTT/time.Millisecond)
		}
	}
	busyHere := !res.BindFree && (!res.BindByDey || !tunnelPort)
	if busyHere || !res.FirewallOpen {
		res.SuggestedPorts = h.suggestPorts(ctx, DefaultSuggestCount)
	}
	res.Note = strings.Join(notes, "; ")
	return res, nil
}

// portUnreachable is DEY-P014 for stage 3 of the port check: node could
// not connect to the hub's public address on port (its dial error as the
// detail). It is logged so the Log line of the three-line block finds it.
func (h *Hub) portUnreachable(port int, proto, node, reason string) *api.ErrorDTO {
	spec := strconv.Itoa(port) + "/" + proto
	e := deyerr.New(deyerr.P014, deyerr.Params{"port": spec, "node": node})
	if reason = strings.TrimSpace(dlog.Redact(reason)); reason != "" {
		e = e.WithDetail(reason)
	}
	h.log.Warn(e.Message(), dlog.Code(deyerr.P014), dlog.Node(node), slog.String("port", spec))
	return api.ToDTO(withLog(e))
}

// fwStage is stage 2 of the port check: open, or the firewall that blocks
// the port and the command that opens it ("" when none is known).
type fwStage struct {
	open          bool
	name, command string
	note          string // the firewall could not be checked
}

// firewallStage checks the external firewalls for port/proto. A check that
// fails reports the port open ("unknown") with a note, as section 10 is
// conservative.
func (h *Hub) firewallStage(ctx context.Context, cfg *config.Config, port int, proto string, tunnelPort bool) fwStage {
	v, err := firewall.Check(ctx, h.o.Runner, port, proto)
	switch {
	case err != nil:
		return fwStage{open: true, name: "unknown", note: "the firewall could not be checked: " + deyerr.As(err).Message()}
	case v.Blocked:
		return fwStage{name: string(v.By), command: v.Command()}
	}
	return fwStage{open: true, name: firewallNames(ctx, h, cfg, tunnelPort)}
}

// PortOpenFirewall implements api.Local (`deyroute port check --open`, Ports
// → Check port and the Add tunnel wizard; section 10): it opens a port in
// the external firewall that blocks it, after the owner confirmed the exact
// command. The firewall is checked again and the command is built from the
// firewall found and the typed port and protocol (firewall.Check), never
// from text the client sent; when it is not req.Command, nothing runs
// (DEY-P032). Afterwards the port is checked once more: another firewall
// that still blocks it is reported with its own command (a new
// confirmation); the same command still being needed is DEY-P033 (an
// earlier rule of that firewall denies the port).
func (l *local) PortOpenFirewall(ctx context.Context, req api.PortOpenRequest) (api.PortOpenResult, error) {
	h := l.h
	proto, err := normalizeProto(req.Port, req.Proto)
	if err != nil {
		return api.PortOpenResult{}, withLog(err)
	}
	if !ports.ValidPort(req.Port) {
		return api.PortOpenResult{}, withLog(deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(req.Port)}))
	}
	spec := strconv.Itoa(req.Port) + "/" + proto
	res := api.PortOpenResult{Port: req.Port, Proto: proto}
	v, err := firewall.Check(ctx, h.o.Runner, req.Port, proto)
	if err != nil {
		return api.PortOpenResult{}, withLog(err)
	}
	if v.Blocked {
		cmd := v.Command()
		params := deyerr.Params{"port": spec, "firewall": string(v.By), "command": cmd}
		switch {
		case cmd == "":
			params["reason"] = "deyroute has no safe command for the rule that blocks it (" + v.Detail + ")"
			return api.PortOpenResult{}, withLog(deyerr.New(deyerr.P033, params))
		case strings.TrimSpace(req.Command) != cmd:
			return api.PortOpenResult{}, withLog(deyerr.New(deyerr.P032, params))
		}
		h.log.Info("opening a port in the external firewall (confirmed by the owner)",
			slog.String("port", spec), slog.String("firewall", string(v.By)), slog.String("command", cmd))
		if err := v.Open(ctx, h.o.Runner); err != nil {
			params["reason"] = cmd + " failed; the firewall tool's output is below"
			return api.PortOpenResult{}, withLog(deyerr.Wrap(deyerr.P033, err, params).WithDetail(deyerr.As(err).Detail))
		}
		res.Ran, res.By = cmd, string(v.By)
	}
	cfg := h.Config()
	_, tunnelPort := cfg.UsedListenPorts()[config.ListenKey{Port: req.Port, Proto: proto}]
	fw := h.firewallStage(ctx, cfg, req.Port, proto, tunnelPort)
	if res.Ran != "" && !fw.open && fw.command == res.Ran {
		return api.PortOpenResult{}, withLog(deyerr.New(deyerr.P033, deyerr.Params{"port": spec, "firewall": res.By,
			"reason": res.Ran + " ran, but " + res.By + " still blocks the port (an earlier rule of " + res.By + " denies it)"}))
	}
	res.FirewallOpen, res.FirewallName, res.FirewallCommand, res.Note = fw.open, fw.name, fw.command, fw.note
	if res.Ran != "" && !fw.open {
		h.log.Warn("port still blocked by another firewall", slog.String("port", spec), slog.String("firewall", fw.name))
	}
	return res, nil
}

// tempTCPListener accepts (and closes) connections on port of every
// address until the returned stop is called; nil when the port cannot be
// bound (taken meanwhile, or no permission).
func tempTCPListener(port int) (stop func()) {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return func() {
		_ = ln.Close()
		<-done
	}
}

// firewallNames names the firewalls that were checked ("nftables, ufw"),
// with deyroute's own table first when it opens a tunnel port.
func firewallNames(ctx context.Context, h *Hub, cfg *config.Config, tunnelPort bool) string {
	var names []string
	if tunnelPort && firewallManaged(cfg) {
		names = append(names, firewall.TableRef)
	}
	for _, k := range firewall.Detect(ctx, h.o.Runner) {
		names = append(names, string(k))
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// probeTunnelPort runs one path probe through tunnel on its listen port
// (the port's probe kind; the hub's public IP when the active candidate
// forwards with NAT).
func (h *Hub) probeTunnelPort(ctx context.Context, tunnel string, port int) health.Result {
	cfg := h.Config()
	t, ok := cfg.Tunnel(tunnel)
	if !ok {
		return health.Result{Err: "unknown tunnel"}
	}
	pm := config.PortMap{Listen: port, Proto: config.ProtoTCP, Probe: config.ProbeAuto}
	for _, p := range t.Ports {
		if p.Listen == port && p.Proto == config.ProtoTCP {
			pm = p
		}
	}
	host := h.o.ProbeHost
	var opts health.PathOptions
	if c := h.tun.lookup(tunnel); c != nil {
		st := c.engineState()
		_, plan := c.snapshot()
		host = c.probeHost(plan, st.Active)
		opts.AcceptCleanClose = c.closesWithoutData(st.Active.Node)
	}
	return health.Path(ctx, net.JoinHostPort(host, strconv.Itoa(port)), probeKind(pm), probeTimeout(*t), opts)
}

// suggestPorts returns up to n free ports (section 10: Cloudflare ports
// first, then random 1024-65535; never 22, the control port, 30000-31999,
// a tunnel port or a port bound on the hub).
func (h *Hub) suggestPorts(ctx context.Context, n int) []int {
	cfg := h.Config()
	used := cfg.UsedListenPorts()
	busy := func(port int, proto string) bool {
		if _, ok := used[config.ListenKey{Port: port, Proto: proto}]; ok {
			return true
		}
		return !h.o.BindCheck(ctx, port, proto).Free
	}
	out := ports.Suggest(n, busy, cfg.Hub.ControlPort, cfg.Hub.ReservedPorts()...)
	if out == nil {
		out = []int{}
	}
	return out
}

// PortSuggest implements api.Local (`deyroute port suggest`).
func (l *local) PortSuggest(ctx context.Context, count int) ([]int, error) {
	if count <= 0 {
		count = DefaultSuggestCount
	}
	return l.h.suggestPorts(ctx, min(count, MaxSuggestCount)), nil
}

// DiagProbe implements api.Local (`deyroute diag probe <tunnel>
// [--all-ports]`): the path probe through the tunnel on its probe port, or
// on every port map, each with its probe kind. UDP port maps are reported
// from the units of the active candidate.
func (l *local) DiagProbe(ctx context.Context, tunnel string, allPorts bool) ([]api.ProbeReport, error) {
	h := l.h
	_, t, err := h.configTunnel(tunnel)
	if err != nil {
		return nil, withLog(err)
	}
	maps := t.Ports
	if !allPorts {
		pm, _ := t.ProbeTarget()
		maps = []config.PortMap{pm}
	}
	c := h.tun.lookup(t.ID)
	var active state.Candidate
	if c != nil {
		active = c.engineState().Active
	}
	out := make([]api.ProbeReport, 0, len(maps))
	for _, pm := range maps {
		rep := api.ProbeReport{Tunnel: t.ID, Port: pm.Listen, Proto: pm.Proto, Kind: probeKind(pm)}
		var r health.Result
		switch {
		case pm.Proto == config.ProtoUDP && c != nil && !active.IsZero():
			rep.Kind = "unit"
			r = c.unitProbe(ctx, active)
		case pm.Proto == config.ProtoUDP:
			rep.Kind = "unit"
			r = health.Result{Err: "the tunnel has no running transport"}
		default:
			r = h.probeTunnelPort(ctx, t.ID, pm.Listen)
		}
		rep.OK, rep.RTTms, rep.Error = r.OK, int(r.RTT/time.Millisecond), r.Err
		out = append(out, rep)
	}
	return out, nil
}
