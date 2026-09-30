package firewall

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func hubSpec() Spec {
	return Spec{
		ControlPort:     44433,
		RestrictControl: true,
		NodeIPs4:        []string{"9.8.7.6", "1.2.3.4"},
		CtlLow:          30000,
		CtlHigh:         31999,
		ListenTCP:       []int{2053, 443},
	}
}

// goldenSpecs are the rendered cases; every golden is also checked with the
// real nft by scripts in the test log when available (see TestGoldenNFTCheck).
var goldenSpecs = map[string]Spec{
	// Section 11 sample: hub with two nodes and two TCP listen ports.
	"hub_nodes": hubSpec(),
	// IPv6 hub: @nodes6, v6 node, UDP port, a TCP range.
	"hub_ipv6": func() Spec {
		s := hubSpec()
		s.IPv6 = true
		s.NodeIPs6 = []string{"2001:db8::20", "2001:db8::10"}
		s.ListenTCP = []int{443, 2000, 2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008, 2009, 2010, 8443, 8444}
		s.ListenUDP = []int{27015}
		return s
	}(),
	// IPv6 enabled before any node has an IPv6 address.
	"hub_ipv6_no_v6_nodes": func() Spec {
		s := hubSpec()
		s.IPv6 = true
		return s
	}(),
	// WireGuard forwarding on the hub (section 7.7): DNAT + masquerade.
	"hub_wireguard_nat": func() Spec {
		s := hubSpec()
		s.ListenTCP = nil
		s.NAT = []backend.NATRule{
			{Proto: "udp", DportLow: 27015, DportHigh: 27015, ToAddr: "10.77.3.2", ToPort: 27015},
			{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "10.77.3.2", ToPort: 443},
			{Proto: "tcp", DportLow: 2053, ToAddr: "10.77.3.2", ToPort: 8443},
			{Proto: "tcp", DportLow: 3000, DportHigh: 3010, ToAddr: "10.77.4.2"},
			{Proto: "tcp", DportLow: 5000, ToAddr: "fd77::2", ToPort: 5000},
			{Proto: "tcp", DportLow: 6000, ToAddr: "10.77.5.2", ToPort: 6000, Iface: "eth0"},
		}
		s.Masquerade = []string{"dey-main", "dey-games"}
		return s
	}(),
	// No listen ports yet (fresh hub).
	"hub_no_listen": func() Spec {
		s := hubSpec()
		s.ListenTCP = nil
		return s
	}(),
	// Control port open to everyone (e.g. while a join is pending).
	"hub_unrestricted_control": func() Spec {
		s := hubSpec()
		s.RestrictControl = false
		return s
	}(),
	// Unrestricted control port and no backend range: no drop rules, so no
	// conntrack rule either.
	"hub_accept_only": {ControlPort: 44433, ListenTCP: []int{443}, ListenUDP: []int{443}},
	// Node side of Hysteria2 port hopping (section 7.6): redirect only.
	"node_port_hopping": {
		NAT: []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123}},
	},
	// Nothing configured.
	"empty": {},
}

func TestRenderGolden(t *testing.T) {
	for name, spec := range goldenSpecs {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, spec.Validate())
			got := Render(spec)
			path := filepath.Join("testdata", name+".nft.golden")
			if *update {
				require.NoError(t, os.MkdirAll("testdata", 0o755))
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "run: go test ./internal/firewall -run TestRenderGolden -update")
			require.Equal(t, string(want), got)
			// Deterministic.
			require.Equal(t, got, Render(spec))
		})
	}
}

func TestRenderSection11Shape(t *testing.T) {
	got := Render(hubSpec())
	// The rules of the section 11 sample, in order.
	want := []string{
		"table inet deyroute {",
		"set nodes {",
		"type ipv4_addr",
		"elements = { 1.2.3.4, 9.8.7.6 }",
		"chain input {",
		"type filter hook input priority -10; policy accept;",
		"ct state established,related accept",
		"tcp dport 44433 ip saddr @nodes accept",
		"tcp dport 44433 drop",
		"tcp dport 30000-31999 ip saddr @nodes accept",
		"udp dport 30000-31999 ip saddr @nodes accept",
		"tcp dport 30000-31999 drop",
		"udp dport 30000-31999 drop",
		"tcp dport { 443, 2053 } accept",
	}
	idx := 0
	for _, line := range strings.Split(got, "\n") {
		if idx < len(want) && strings.TrimSpace(line) == want[idx] {
			idx++
		}
	}
	require.Equal(t, len(want), idx, "missing %q in:\n%s", want[min(idx, len(want)-1)], got)
	require.NotContains(t, got, "nodes6")
	require.NotContains(t, got, "nat hook")
}

func TestRenderNormalizes(t *testing.T) {
	clean := hubSpec()
	clean.NAT = []backend.NATRule{{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "10.77.3.2", ToPort: 443}}
	clean.Masquerade = []string{"dey-main"}

	messy := clean
	messy.NodeIPs4 = []string{"1.2.3.4", "garbage", "9.8.7.6", " 1.2.3.4 ", "::ffff:9.8.7.6", "0.0.0.0", "fe80::1%eth0"}
	messy.ListenTCP = []int{443, 0, 2053, 443, 70000}
	messy.NAT = []backend.NATRule{
		{Proto: "TCP", DportLow: 443, ToAddr: "10.77.3.2", ToPort: 443}, // same as clean after normalization
		{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "10.77.3.2", ToPort: 443},
		{Proto: "icmp", DportLow: 1},                                          // bad proto
		{Proto: "tcp", DportLow: 10, DportHigh: 5},                            // bad range
		{Proto: "tcp", DportLow: 10, ToPort: 70000},                           // bad target port
		{Proto: "tcp", DportLow: 10, ToAddr: "nope"},                          // bad address
		{Proto: "tcp", DportLow: 10, ToAddr: "10.0.0.1", Iface: "bad iface!"}, // bad iface
	}
	messy.Masquerade = []string{"dey-main", "dey-main", "a/b", "waytoolonginterfacename"}
	require.Equal(t, Render(clean), Render(messy))

	err := messy.Validate()
	require.Error(t, err)
	e := deyerr.As(err)
	require.Equal(t, deyerr.P019, e.Code)
	for _, frag := range []string{`"garbage"`, `"0.0.0.0"`, `"fe80::1%eth0"`, "0/tcp", "70000/tcp",
		"protocol must be tcp or udp", "destination port range invalid", "target port out of range",
		"target address invalid", "interface name invalid", `"a/b"`, `"waytoolonginterfacename"`} {
		require.Contains(t, err.Error(), frag)
	}

	bad := Spec{ControlPort: 70000, CtlLow: 31999, CtlHigh: 30000, ListenUDP: []int{-1}}
	err = bad.Validate()
	require.ErrorContains(t, err, "control port 70000")
	require.ErrorContains(t, err, "backend control range 31999-30000")
	require.ErrorContains(t, err, "-1/udp")
	// Invalid control settings are simply not rendered.
	require.Equal(t, Render(Spec{}), Render(bad))

	// An IPv6 address in the IPv4 list lands in @nodes6.
	s := Spec{NodeIPs4: []string{"2001:db8::1"}, ControlPort: 44433, RestrictControl: true}
	out := Render(s)
	require.Contains(t, out, "set nodes6")
	require.Contains(t, out, "elements = { 2001:db8::1 }")
	require.Contains(t, out, "tcp dport 44433 ip6 saddr @nodes6 accept")
}

func TestPortSet(t *testing.T) {
	require.Equal(t, "443", portSet([]int{443}))
	require.Equal(t, "{ 443, 444 }", portSet([]int{443, 444}))
	require.Equal(t, "443-445", portSet([]int{443, 444, 445}))
	require.Equal(t, "{ 80, 443-445, 8443 }", portSet([]int{80, 443, 444, 445, 8443}))
	require.Equal(t, "30000", portRange(30000, 30000))
}
