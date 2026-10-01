package render

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/firewall"
)

func fwConfig() *config.Config {
	cfg := config.NewHub("ir-1", "5.6.7.8", 44433)
	cfg.Nodes = []config.Node{
		{ID: "de-1", PublicIP: "1.2.3.4"},
		{ID: "nl-1", PublicIP: "2001:db8::9"},
		{ID: "fr-1", PublicIP: "::ffff:9.8.7.6"},
		{ID: "xx-1", PublicIP: "not-an-ip"},
	}
	a := config.NewTunnel("main", "", []string{"de-1"}, []config.PortMap{{Listen: 443}, {Listen: 2053}, {Listen: 443, Proto: "udp"}})
	b := config.NewTunnel("games", "", []string{"de-1"}, []config.PortMap{{Listen: 27015, Proto: "udp"}, {Listen: 2053}})
	off := config.NewTunnel("off", "", []string{"de-1"}, []config.PortMap{{Listen: 8443}})
	off.Enabled = false
	cfg.Tunnels = []config.Tunnel{a, b, off}
	return cfg
}

func TestFirewallSpec(t *testing.T) {
	cfg := fwConfig()
	active := Side{
		NAT:        []backend.NATRule{{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "10.77.0.2", ToPort: 443}},
		Masquerade: []string{"dey-main"},
	}
	s := FirewallSpec(cfg, []Side{active, {}}, false, DefaultUnknownControlRate)
	require.Equal(t, firewall.Spec{
		ControlPort:        44433,
		RestrictControl:    true,
		UnknownControlRate: "6/minute",
		NodeIPs4:           []string{"1.2.3.4", "9.8.7.6"},
		NodeIPs6:           []string{"2001:db8::9"},
		CtlLow:             30000,
		CtlHigh:            31999,
		ListenTCP:          []int{443, 2053},
		ListenUDP:          []int{443, 27015},
		NAT:                active.NAT,
		Masquerade:         []string{"dey-main"},
	}, s)
	require.NoError(t, s.Validate())
	out := firewall.Render(s)
	require.Contains(t, out, "tcp dport 44433 ip saddr @nodes accept")
	require.Contains(t, out, "tcp dport 44433 ct state new limit rate 6/minute accept")
	require.Contains(t, out, "tcp dport 44433 drop")
	require.Contains(t, out, "dnat ip to 10.77.0.2:443")
	require.NotContains(t, out, "8443", "disabled tunnels are not opened")

	// Join window: control port open to everyone, no rate rule.
	j := FirewallSpec(cfg, nil, true, DefaultUnknownControlRate)
	require.False(t, j.RestrictControl)
	require.Empty(t, j.UnknownControlRate)
	require.Empty(t, j.NAT, "NAT only for active candidates")
	require.Empty(t, j.Masquerade)
	out = firewall.Render(j)
	require.Contains(t, out, "tcp dport 44433 accept")
	require.NotContains(t, out, "tcp dport 44433 drop")
	require.Contains(t, out, "tcp dport 30000-31999 drop", "the backend range stays restricted")

	// No rate: unknown sources are dropped.
	d := FirewallSpec(cfg, nil, false, "")
	require.True(t, d.RestrictControl)
	require.NotContains(t, firewall.Render(d), "limit rate")

	// IPv6 hub.
	cfg.Hub.PublicIP6 = "2001:db8::1"
	require.True(t, FirewallSpec(cfg, nil, false, "").IPv6)

	// restrict_control_to_nodes: false.
	cfg.Security.RestrictControlToNodes = false
	r := FirewallSpec(cfg, nil, false, DefaultUnknownControlRate)
	require.False(t, r.RestrictControl)
	require.Empty(t, r.UnknownControlRate)

	// Missing sections.
	require.Equal(t, firewall.Spec{}, FirewallSpec(nil, nil, false, ""))
	bare := &config.Config{Role: config.RoleHub}
	bs := FirewallSpec(bare, nil, false, "")
	require.True(t, bs.RestrictControl)
	require.Zero(t, bs.ControlPort)
}

func TestNodePayload(t *testing.T) {
	s := Side{
		Instance:  "main.de-1.fwd-quic",
		ConfigDir: "/etc/deyroute/backends/fwd/main/de-1/quic",
		Files:     map[string][]byte{"config.toml": []byte("x")},
		Unit:      backend.UnitSpec{ExecStart: []string{"/bin/true"}},
		NAT:       []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30001}},
		IPForward: true,
	}
	p := NodePayload("main", s)
	require.Equal(t, "main.de-1.fwd-quic", p.Instance)
	require.Equal(t, "main", p.Tunnel)
	require.Equal(t, s.ConfigDir, p.ConfigDir)
	require.Equal(t, s.Files, p.Files)
	require.Equal(t, s.Unit, p.Unit)
	require.Equal(t, s.NAT, p.NAT)
	require.True(t, p.IPForward)
	// Copies, not aliases.
	p.Files["config.toml"][0] = 'y'
	require.Equal(t, "x", string(s.Files["config.toml"]))
}
