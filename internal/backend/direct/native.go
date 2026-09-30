package direct

import (
	"net"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// renderNative renders relay.json and the unit of one direct/native half.
func renderNative(in backend.RenderInput, ports []portMap, side backend.Side) (backend.Rendered, error) {
	cfg := RelayConfig{
		Version:         RelayConfigVersion,
		Tunnel:          in.Tunnel.ID,
		Token:           in.Secrets.Token,
		IdleTimeoutS:    int(DefaultIdleTimeout.Seconds()),
		DialTimeoutS:    int(DefaultDialTimeout.Seconds()),
		UDPIdleTimeoutS: int(DefaultUDPIdleTimeout.Seconds()),
		MaxUDPSessions:  DefaultMaxUDPSessions,
	}
	var binds []backend.PortUse
	if side == backend.SideNode {
		// The node half listens on the backend control port (tcp and/or
		// udp) and knows only the targets: its allow-list.
		host := nodeBindHost(in.Node.PublicIP)
		cfg.Role = RoleNode
		cfg.Bind = backend.HostPort(host, in.ControlPort)
		hasTCP, hasUDP := false, false
		for _, p := range ports {
			cfg.Ports = append(cfg.Ports, RelayPort{Index: p.index, Proto: p.proto, Target: p.target})
			hasTCP = hasTCP || p.proto == config.ProtoTCP
			hasUDP = hasUDP || p.proto == config.ProtoUDP
		}
		if hasTCP {
			binds = append(binds, backend.PortUse{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: host, Purpose: "control"})
		}
		if hasUDP {
			binds = append(binds, backend.PortUse{Port: in.ControlPort, Proto: config.ProtoUDP, Addr: host, Purpose: "control"})
		}
	} else {
		// The hub half binds the user ports and dials the node half.
		addr := listenAddr(in)
		cfg.Role = RoleHub
		cfg.Node = backend.HostPort(strings.TrimSpace(in.Node.PublicIP), in.ControlPort)
		for _, p := range ports {
			cfg.Ports = append(cfg.Ports, RelayPort{Index: p.index, Proto: p.proto, Listen: backend.HostPort(addr, p.listen)})
			binds = append(binds, backend.PortUse{Port: p.listen, Proto: p.proto, Addr: addr, Purpose: "user"})
		}
	}
	data, err := backend.JSONIndent(cfg)
	if err != nil {
		return backend.Rendered{}, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": Native})
	}
	return backend.Rendered{
		Files: map[string][]byte{RelayConfigFile: data},
		Unit:  relayUnit(in),
		Binds: binds,
	}, nil
}

// nodeBindHost is the node relay listen address: every IPv4 address, or
// every address when the node is reached over IPv6.
func nodeBindHost(nodeIP string) string {
	if ip := net.ParseIP(strings.TrimSpace(nodeIP)); ip != nil && ip.To4() == nil {
		return "::"
	}
	return "0.0.0.0"
}
