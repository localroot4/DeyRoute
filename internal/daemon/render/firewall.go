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
//   - every listen port of every enabled tunnel (tcp/udp);
//   - NAT and masquerade only from activeHub, the hub sides of the ACTIVE
//     candidates (warm rungs never have NAT rules);
//   - IPv6 sets when the hub has a public IPv6.
func FirewallSpec(cfg *config.Config, activeHub []Side, joinWindow bool, unknownRate string) firewall.Spec {
	var s firewall.Spec
	if cfg == nil {
		return s
	}
	restrict := true
	if cfg.Security != nil {
		restrict = cfg.Security.RestrictControlToNodes
	}
	if cfg.Hub != nil {
		s.ControlPort = cfg.Hub.ControlPort
		s.IPv6 = strings.TrimSpace(cfg.Hub.PublicIP6) != ""
	}
	s.RestrictControl = restrict && !joinWindow
	if s.RestrictControl {
		s.UnknownControlRate = unknownRate
	}
	for _, n := range cfg.Nodes {
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
