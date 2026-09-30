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
// node), 4. for a tunnel port, the path probe through the tunnel. Free
// ports are suggested when the port is busy or closed.
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
	if reserved, why := ports.Reserved(req.Port, cfg.Hub.ControlPort); reserved {
		notes = append(notes, why)
	}

	// 2. firewall.
	v, err := firewall.Check(ctx, h.o.Runner, req.Port, proto)
	switch {
	case err != nil:
		res.FirewallOpen, res.FirewallName = true, "unknown"
		notes = append(notes, "the firewall could not be checked: "+deyerr.As(err).Message())
	case v.Blocked:
		res.FirewallName = string(v.By)
		res.FirewallCommand = strings.Join(v.Commands, " && ")
	default:
		res.FirewallOpen = true
		res.FirewallName = firewallNames(ctx, h, cfg, tunnelPort)
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
	out := ports.Suggest(n, busy, cfg.Hub.ControlPort)
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
