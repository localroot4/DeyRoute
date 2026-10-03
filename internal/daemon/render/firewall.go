package render

import (
	"net/netip"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/firewall"
)

// DefaultUnknownControlRate is the rate at which new control-port
// connections from addresses outside @nodes are admitted while no join
// window is open (ARCHITECTURE.md §7.5, QUESTIONS.md C.23).
const DefaultUnknownControlRate = "6/minute"

// NodePayload converts a node side into the backend.render command
// arguments (Control API, section 3). Files are copied.
func NodePayload(tunnel string, s Side) api.BackendRenderArgs {
	files := make(map[string][]byte, len(s.Files))
	for k, v := range s.Files {
		files[k] = append([]byte(nil), v...)
	}
	return api.BackendRenderArgs{
		Instance:  s.Instance,
		Tunnel:    tunnel,
		ConfigDir: s.ConfigDir,
		Files:     files,
		Unit:      s.Unit,
		NAT:       append([]backend.NATRule(nil), s.NAT...),
		IPForward: s.IPForward,
	}
}

// FirewallSpec assembles the hub's `table inet deyroute` (sections 10, 11):
//
//   - the control port restricted to the nodes' public IPs (split into
//     IPv4/IPv6), plus a rate-limited accept for unknown sources
//     (unknownRate, "" = drop) — unless security.restrict_control_to_nodes
//     is off or a join window is open (joinWindow: an unexpired join token
//     exists), then the port is open to everyone;
//   - the backend control range 30000-31999 from @nodes only;
//   - the CDN front port when front mode is enabled (hub.front): reachable
//     from the CDN ranges only, or from everyone with hub.front.cf_only
//     false. Nodes that joined through the front (route front) are never put
//     into @nodes: their traffic arrives from the CDN, and an address the hub
//     cannot vouch for must not open the control or backend ports;
//   - every listen port of every enabled tunnel (tcp/udp);
//   - NAT and masquerade only from activeHub, the hub sides of the ACTIVE
//     candidates (warm rungs never have NAT rules);
//   - IPv6 sets when the hub has a public IPv6;
//   - the MSS that SYNs from a WireGuard/AmneziaWG interface are clamped
//     to: tuning.wg_mtu minus 40 (1380 for the default MTU 1420).
func FirewallSpec(cfg *config.Config, activeHub []Side, joinWindow bool, unknownRate string) firewall.Spec {
	var s firewall.Spec
	if cfg == nil {
		return s
	}
	wgMTU := 0
	if cfg.Tuning != nil {
		wgMTU = cfg.Tuning.WGMTU
	}
	s.ClampMSS = firewall.ClampMSSForMTU(wgMTU)
	restrict := true
	if cfg.Security != nil {
		restrict = cfg.Security.RestrictControlToNodes
	}
	if cfg.Hub != nil {
		s.ControlPort = cfg.Hub.ControlPort
		s.IPv6 = strings.TrimSpace(cfg.Hub.PublicIP6) != ""
		// The hub overrides FrontPort with the port the listener is really
		// bound to (like ControlPort); this is what the configuration asks for.
		if p := cfg.Hub.FrontPort(); p != 0 {
			s.FrontPort = p
			s.FrontOpen = !cfg.Hub.Front.CFOnlyOrDefault()
		}
	}
	s.RestrictControl = restrict && !joinWindow
	if s.RestrictControl {
		s.UnknownControlRate = unknownRate
	}
	for _, n := range cfg.Nodes {
		if n.Route == config.RouteFront {
			continue
		}
		a, err := netip.ParseAddr(strings.TrimSpace(n.PublicIP))
		if err != nil {
			continue
		}
		a = a.Unmap()
		if a.Is4() {
			s.NodeIPs4 = append(s.NodeIPs4, a.String())
		} else {
			s.NodeIPs6 = append(s.NodeIPs6, a.String())
		}
	}
	s.CtlLow, s.CtlHigh = config.CtlRangeLow, config.CtlRangeHigh
	for _, t := range cfg.Tunnels {
		if !t.Enabled {
			continue
		}
		for _, pm := range t.Ports {
			if pm.Proto == config.ProtoUDP {
				s.ListenUDP = appendPort(s.ListenUDP, pm.Listen)
			} else {
				s.ListenTCP = appendPort(s.ListenTCP, pm.Listen)
			}
		}
	}
	for _, side := range activeHub {
		s.NAT = append(s.NAT, side.NAT...)
		s.Masquerade = append(s.Masquerade, side.Masquerade...)
	}
	return s
}

func appendPort(list []int, p int) []int {
	for _, x := range list {
		if x == p {
			return list
		}
	}
	return append(list, p)
}
