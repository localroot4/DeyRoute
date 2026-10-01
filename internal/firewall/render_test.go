package firewall

import (
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
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
	// Restricted control port with a rate-limited accept for unknown sources
	// (a node whose IP changed reconnects with its certificate, C.23).
	"hub_unknown_rate": func() Spec {
		s := hubSpec()
		s.IPv6 = true
		s.UnknownControlRate = "6/minute"
		return s
	}(),
	// Unrestricted control port and no backend range: no drop rules, so no
	// conntrack rule either.
	"hub_accept_only": {ControlPort: 44433, ListenTCP: []int{443}, ListenUDP: []int{443}},
	// Node side of a WireGuard tunnel (section 7.7): DNAT on the tunnel
	// interface to local targets, and the interface is confined (S17).
	"node_wireguard_nat": {
		NAT: []backend.NATRule{
			{Proto: "udp", DportLow: 27015, DportHigh: 27015, ToAddr: "127.0.0.1", ToPort: 27015, Iface: "dey-main"},
			{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "127.0.0.1", ToPort: 443, Iface: "dey-main"},
		},
	},
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
		`iif "lo" accept`,
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

	bad := Spec{ControlPort: 70000, CtlLow: 31999, CtlHigh: 30000, ListenUDP: []int{-1}, UnknownControlRate: "6 per minute; drop"}
	err = bad.Validate()
	require.ErrorContains(t, err, "control port 70000")
	require.ErrorContains(t, err, `unknown control rate "6 per minute; drop"`)
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

func TestRenderUnknownControlRate(t *testing.T) {
	s := hubSpec()
	s.UnknownControlRate = "6/minute"
	require.NoError(t, s.Validate())
	lines := strings.Split(Render(s), "\n")
	idx := func(rule string) int {
		for i, l := range lines {
			if strings.TrimSpace(l) == rule {
				return i
			}
		}
		return -1
	}
	nodes := idx("tcp dport 44433 ip saddr @nodes accept")
	limit := idx("tcp dport 44433 ct state new limit rate 6/minute accept")
	drop := idx("tcp dport 44433 drop")
	require.True(t, nodes >= 0 && limit == nodes+1 && drop == limit+1, "order nodes/limit/drop: %d %d %d", nodes, limit, drop)

	// A burst is accepted too.
	s.UnknownControlRate = "10/second burst 20 packets"
	require.NoError(t, s.Validate())
	require.Contains(t, Render(s), "tcp dport 44433 ct state new limit rate 10/second burst 20 packets accept")

	// The rate only applies to a restricted control port.
	s.RestrictControl = false
	require.NotContains(t, Render(s), "limit rate")
	require.Contains(t, Render(s), "tcp dport 44433 accept")

	// Invalid rates are not rendered (and reported by Validate).
	for _, bad := range []string{"6/minutes", "0/minute", "6/minute; drop", "fast", "6/minute burst 5"} {
		s := hubSpec()
		s.UnknownControlRate = bad
		require.Error(t, s.Validate(), bad)
		require.Equal(t, Render(hubSpec()), Render(s), bad)
	}
}

// TestRenderConfinesNATInterfaces: an interface that NAT rules match on
// (the node side of WireGuard) gets a forward drop after the DNATed and
// established flows, once per interface; NAT rules without an interface
// (the hub's DNAT) confine nothing, and masquerading keeps its MSS clamps
// ahead of the confinement.
func TestRenderConfinesNATInterfaces(t *testing.T) {
	node := Render(Spec{NAT: []backend.NATRule{
		{Proto: "tcp", DportLow: 443, ToAddr: "127.0.0.1", ToPort: 443, Iface: "dey-main"},
		{Proto: "udp", DportLow: 27015, ToAddr: "127.0.0.1", ToPort: 27015, Iface: "dey-main"},
		{Proto: "tcp", DportLow: 8443, ToAddr: "127.0.0.1", ToPort: 8443, Iface: "deyc-3"},
	}})
	fwd := node[strings.Index(node, "chain forward"):]
	require.Equal(t, 1, strings.Count(fwd, `iifname "dey-main" drop`))
	for _, i := range []string{`"dey-main"`, `"deyc-3"`} {
		est := strings.Index(fwd, "iifname "+i+" ct state established,related accept")
		dnat := strings.Index(fwd, "iifname "+i+" ct status dnat accept")
		drop := strings.Index(fwd, "iifname "+i+" drop")
		require.True(t, est >= 0 && est < dnat && dnat < drop, "%s: %s", i, fwd)
	}
	require.Less(t, strings.Index(fwd, `"dey-main"`), strings.Index(fwd, `"deyc-3"`))

	hub := Render(Spec{
		NAT:        []backend.NATRule{{Proto: "tcp", DportLow: 443, ToAddr: "10.77.3.2", ToPort: 443}},
		Masquerade: []string{"dey-main"},
	})
	require.NotContains(t, hub, " drop\n\t}\n}")
	require.NotContains(t, hub, "ct status dnat")

	both := Render(Spec{
		NAT:        []backend.NATRule{{Proto: "tcp", DportLow: 443, ToAddr: "127.0.0.1", ToPort: 443, Iface: "dey-x"}},
		Masquerade: []string{"dey-main"},
	})
	fwd = both[strings.Index(both, "chain forward"):]
	require.Less(t, strings.Index(fwd, "maxseg"), strings.Index(fwd, `iifname "dey-x" ct state`))
	require.Equal(t, 1, strings.Count(both, "chain forward"))
}

// TestRenderNATOrderIndependent: rules that tie on protocol and ports are
// ordered by interface, address and target port, so every input order
// renders the same table.
func TestRenderNATOrderIndependent(t *testing.T) {
	rules := []backend.NATRule{
		{Proto: "tcp", DportLow: 443, ToAddr: "10.77.3.2", ToPort: 443},
		{Proto: "tcp", DportLow: 443, ToAddr: "10.77.3.2", ToPort: 8443},
		{Proto: "tcp", DportLow: 443, ToAddr: "10.77.4.2", ToPort: 443},
		{Proto: "tcp", DportLow: 443, ToAddr: "10.77.3.2", ToPort: 443, Iface: "eth1"},
		{Proto: "tcp", DportLow: 443, ToAddr: "10.77.3.2", ToPort: 443, Iface: "eth0"},
		{Proto: "tcp", DportLow: 443, DportHigh: 450, ToAddr: "10.77.3.2"},
		{Proto: "udp", DportLow: 443, ToPort: 30000},
		{Proto: "tcp", DportLow: 80, ToAddr: "fd77::2"},
	}
	want := Render(Spec{NAT: rules})
	rng := rand.New(rand.NewPCG(7, 7))
	for i := 0; i < 50; i++ {
		shuffled := slices.Clone(rules)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		require.Equal(t, want, Render(Spec{NAT: shuffled}))
	}
	pre := want[strings.Index(want, "chain prerouting"):strings.Index(want, "chain output")]
	require.Less(t, strings.Index(pre, "tcp dport 80 "), strings.Index(pre, "tcp dport 443 "))
	require.Less(t, strings.Index(pre, `dnat ip to 10.77.3.2:443`), strings.Index(pre, `dnat ip to 10.77.3.2:8443`))
	require.Less(t, strings.Index(pre, `iifname "eth0"`), strings.Index(pre, `iifname "eth1"`))
	require.Contains(t, pre, "udp dport 443 redirect to :30000")
	require.Contains(t, pre, "dnat ip6 to fd77::2")
}

func TestPortSet(t *testing.T) {
	require.Equal(t, "443", portSet([]int{443}))
	require.Equal(t, "{ 443, 444 }", portSet([]int{443, 444}))
	require.Equal(t, "443-445", portSet([]int{443, 444, 445}))
	require.Equal(t, "{ 80, 443-445, 8443 }", portSet([]int{80, 443, 444, 445, 8443}))
	require.Equal(t, "30000", portRange(30000, 30000))
}
