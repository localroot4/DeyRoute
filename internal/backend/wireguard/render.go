package wireguard

import (
	"net/netip"
	"path/filepath"
	"strconv"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// AWGRuntimeDir is where amneziawg-go v1.0.4 creates its UAPI socket
// (ipc.socketDirectory "/var/run/amneziawg"; /var/run is a symlink to /run).
// The unit gets it as an optional writable path; PreStart (and Up) create it
// when missing.
const AWGRuntimeDir = "/run/amneziawg"

// awgSocketDir is the directory as amneziawg-go spells it.
const awgSocketDir = "/var/run/amneziawg"

// renderHub renders the hub side: wg.json (dials the node), the unit, the
// user ports and the DNAT/masquerade rules of spec section 7.7.
func (b *Backend) renderHub(in backend.RenderInput, p plan) (backend.Rendered, error) {
	c := b.baseConfig(in, p, backend.SideHub)
	c.Address = netip.PrefixFrom(p.hubAddr, prefixLen).String()
	c.PrivateKey = p.keys.hubPriv
	c.Peer = Peer{
		PublicKey:           p.keys.nodePub,
		Endpoint:            p.endpoint.String(),
		AllowedIPs:          []string{netip.PrefixFrom(p.nodeAddr, 32).String()},
		PersistentKeepalive: Keepalive,
	}
	data, err := backend.JSONIndent(c)
	if err != nil {
		return backend.Rendered{}, renderErr(in, err)
	}

	addr := in.ListenAddrOrDefault()
	binds := make([]backend.PortUse, 0, len(p.maps))
	nat := make([]backend.NATRule, 0, len(p.maps))
	for _, m := range p.maps {
		// The hub does not open sockets for user ports; the DNAT below
		// owns them, so they are reported as binds for the conflict check
		// and the firewall.
		binds = append(binds, backend.PortUse{Port: m.listen, Proto: m.proto, Addr: addr, Purpose: "user"})
		nat = append(nat, backend.NATRule{
			Proto: m.proto, DportLow: m.listen, DportHigh: m.listen,
			ToAddr: p.nodeAddr.String(), ToPort: m.port,
		})
	}
	return backend.Rendered{
		Files:      map[string][]byte{ConfigFile: data},
		Unit:       b.unit(in, p, false),
		Binds:      binds,
		NAT:        nat,
		Masquerade: []string{p.iface},
		IPForward:  true,
	}, nil
}

// renderNode renders the node side: wg.json (listens on UDP <ctl>), the
// unit, and one DNAT per target that maps tunnel traffic for
// 10.77.n.2:<target port> to exactly that target. Nothing else is
// forwarded: no masquerade, no ip_forward (spec section 11, S17).
func (b *Backend) renderNode(in backend.RenderInput, p plan) (backend.Rendered, error) {
	c := b.baseConfig(in, p, backend.SideNode)
	c.Address = netip.PrefixFrom(p.nodeAddr, prefixLen).String()
	c.PrivateKey = p.keys.nodePriv
	c.ListenPort = in.ControlPort
	c.RouteLocalnet = p.routeLocalnet
	c.Peer = Peer{
		PublicKey:  p.keys.hubPub,
		AllowedIPs: []string{netip.PrefixFrom(p.hubAddr, 32).String()},
	}
	data, err := backend.JSONIndent(c)
	if err != nil {
		return backend.Rendered{}, renderErr(in, err)
	}

	var nat []backend.NATRule
	seen := map[string]bool{}
	for _, m := range p.maps {
		key := m.proto + "/" + strconv.Itoa(m.port)
		if seen[key] {
			continue
		}
		seen[key] = true
		nat = append(nat, backend.NATRule{
			Proto: m.proto, DportLow: m.port, DportHigh: m.port,
			ToAddr: m.host.String(), ToPort: m.port, Iface: p.iface,
		})
	}
	return backend.Rendered{
		Files: map[string][]byte{ConfigFile: data},
		Unit:  b.unit(in, p, p.routeLocalnet),
		Binds: []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoUDP, Addr: "0.0.0.0", Purpose: "control"}},
		NAT:   nat,
	}, nil
}

// baseConfig fills the side-independent part of wg.json.
func (b *Backend) baseConfig(in backend.RenderInput, p plan, side backend.Side) Config {
	c := Config{
		Mode:      ModeKernel,
		Side:      side.String(),
		Tunnel:    in.Tunnel.ID,
		Interface: p.iface,
		MTU:       MTU,
	}
	if b.awg {
		c.Mode = ModeUserspace
		a := *p.keys.awg
		c.AWG = &a
	}
	return c
}

// unit returns the drop-in data. Both flavours run as root with
// CAP_NET_ADMIN and AF_NETLINK (spec section 7 common rules).
//
// wireguard/kernel: a oneshot "deyroute wg up" that creates and configures
// the kernel interface and stays active (RemainAfterExit); stopping the
// unit runs "deyroute wg down", which deletes the interface. When the node
// DNATs to 127.0.0.1 it must write net.ipv4.conf.<iface>.route_localnet,
// which ProtectKernelTunables would forbid.
//
// awg/userspace: amneziawg-go in the foreground ("-f", no daemonizing);
// stopping the unit ends the process, which removes the TUN interface. It
// creates its UAPI socket in /run/amneziawg (optional writable path: the
// directory is created by PreStart before the unit starts). The device is
// configured after start by PostStart (Up).
func (b *Backend) unit(in backend.RenderInput, p plan, needsTunables bool) backend.UnitSpec {
	cfg := filepath.Join(in.Paths.ConfigDir, ConfigFile)
	u := backend.UnitSpec{
		WorkingDirectory: in.Paths.ConfigDir,
		RunAsRoot:        true,
		ExtraCaps:        []string{"CAP_NET_ADMIN"},
		AddressFamilies:  []string{"AF_NETLINK"},
	}
	if b.awg {
		u.ExecStart = []string{in.Paths.Binary, "-f", p.iface}
		u.Env = map[string]string{"LOG_LEVEL": "error"}
		u.ReadWritePaths = []string{"-" + AWGRuntimeDir}
		return u
	}
	u.Type = "oneshot"
	u.RemainAfterExit = true
	u.ExecStart = []string{in.Paths.SelfBinary, "wg", "up", "--config", cfg}
	u.ExecStop = [][]string{{in.Paths.SelfBinary, "wg", "down", "--config", cfg}}
	if needsTunables {
		u.DropHardening = map[string]string{
			"ProtectKernelTunables": "deyroute wg up sets net.ipv4.conf.<iface>.route_localnet=1 so the node can DNAT tunnel traffic to 127.0.0.1 targets",
		}
	}
	return u
}

func renderErr(in backend.RenderInput, err error) error {
	return deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": in.Transport.Backend, "transport": in.Transport.Name})
}
