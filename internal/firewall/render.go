package firewall

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// Chain headers of the table. The input chain runs before the usual
// filter priority (0) as in section 11.
const (
	hdrInput       = "type filter hook input priority -10; policy accept;"
	hdrForward     = "type filter hook forward priority -10; policy accept;"
	hdrPrerouting  = "type nat hook prerouting priority dstnat; policy accept;"
	hdrOutput      = "type nat hook output priority -100; policy accept;"
	hdrPostrouting = "type nat hook postrouting priority srcnat; policy accept;"
)

// renderHeader is the first line of every rendered table.
const renderHeader = "# Managed by deyroute. Do not edit: this table is replaced on every change.\n"

// normalized is a Spec with invalid entries dropped, duplicates removed and
// everything sorted, so Render is deterministic whatever the input order.
type normalized struct {
	controlPort     int
	restrict        bool
	unknownRate     string
	v4, v6          []netip.Addr
	withV6          bool
	ctlLow, ctlHigh int
	tcp, udp        []int
	nat             []natRule
	masq            []string
}

func normalize(s Spec) normalized {
	n := normalized{restrict: s.RestrictControl}
	if validRate(s.UnknownControlRate) {
		n.unknownRate = s.UnknownControlRate
	}
	if validPort(s.ControlPort) {
		n.controlPort = s.ControlPort
	}
	if validPort(s.CtlLow) && validPort(s.CtlHigh) && s.CtlLow <= s.CtlHigh {
		n.ctlLow, n.ctlHigh = s.CtlLow, s.CtlHigh
	}
	seen := map[netip.Addr]bool{}
	for _, list := range [][]string{s.NodeIPs4, s.NodeIPs6} {
		for _, ip := range list {
			a, ok := parseNodeIP(ip)
			if !ok || seen[a] {
				continue
			}
			seen[a] = true
			if a.Is4() {
				n.v4 = append(n.v4, a)
			} else {
				n.v6 = append(n.v6, a)
			}
		}
	}
	slices.SortFunc(n.v4, netip.Addr.Compare)
	slices.SortFunc(n.v6, netip.Addr.Compare)
	n.withV6 = s.IPv6 || len(n.v6) > 0
	n.tcp = uniquePorts(s.ListenTCP)
	n.udp = uniquePorts(s.ListenUDP)

	natSeen := map[natRule]bool{}
	for _, r := range s.NAT {
		nr, bad := normalizeNAT(r)
		if bad != "" || natSeen[nr] {
			continue
		}
		natSeen[nr] = true
		n.nat = append(n.nat, nr)
	}
	slices.SortFunc(n.nat, compareNAT)

	for _, m := range s.Masquerade {
		if validIface(m) && !slices.Contains(n.masq, m) {
			n.masq = append(n.masq, m)
		}
	}
	slices.Sort(n.masq)
	return n
}

func compareNAT(a, b natRule) int {
	switch {
	case a.proto != b.proto:
		return strings.Compare(a.proto, b.proto)
	case a.lo != b.lo:
		return a.lo - b.lo
	case a.hi != b.hi:
		return a.hi - b.hi
	case a.iface != b.iface:
		return strings.Compare(a.iface, b.iface)
	case a.to != b.to:
		return a.to.Compare(b.to)
	}
	return a.toPort - b.toPort
}

func uniquePorts(in []int) []int {
	var out []int
	for _, p := range in {
		if validPort(p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// Render returns the complete `table inet deyroute { … }` for s as an nft
// script (section 11). The output is deterministic: invalid entries are
// dropped (see Spec.Validate), duplicates removed and lists sorted.
func Render(s Spec) string {
	n := normalize(s)
	var blocks []string

	blocks = append(blocks, renderSet(SetNodes, "ipv4_addr", n.v4))
	if n.withV6 {
		blocks = append(blocks, renderSet(SetNodes6, "ipv6_addr", n.v6))
	}
	if rules := inputRules(n); len(rules) > 0 {
		blocks = append(blocks, renderChain("input", hdrInput, rules))
	}
	if len(n.nat) > 0 {
		var pre, out []string
		for _, r := range n.nat {
			pre = append(pre, natRuleText(r))
			if r.to.IsValid() && r.iface == "" {
				out = append(out, natRuleText(r))
			}
		}
		blocks = append(blocks, renderChain("prerouting", hdrPrerouting, pre))
		if len(out) > 0 {
			blocks = append(blocks, renderChain("output", hdrOutput, out))
		}
	}
	var fwd []string
	if len(n.masq) > 0 {
		var post []string
		for _, m := range n.masq {
			post = append(post, fmt.Sprintf("oifname %s masquerade", quote(m)))
		}
		for _, m := range n.masq {
			fwd = append(fwd,
				fmt.Sprintf("oifname %s tcp flags & (syn | rst) == syn tcp option maxseg size set rt mtu", quote(m)),
				fmt.Sprintf("iifname %s tcp flags & (syn | rst) == syn tcp option maxseg size set rt mtu", quote(m)))
		}
		blocks = append(blocks, renderChain("postrouting", hdrPostrouting, post))
	}
	// An interface the NAT rules match on (the node side of a WireGuard
	// tunnel) is confined: what arrives there is only DNATed to the rules'
	// targets, and anything the host would route onward from it is dropped,
	// so the node never routes for the hub even when net.ipv4.ip_forward is
	// on (section 11, scenario S17). DNATed and established flows pass.
	for _, i := range n.natIfaces() {
		fwd = append(fwd,
			"iifname "+quote(i)+" ct state established,related accept",
			"iifname "+quote(i)+" ct status dnat accept",
			"iifname "+quote(i)+" drop")
	}
	if len(fwd) > 0 {
		blocks = append(blocks, renderChain("forward", hdrForward, fwd))
	}

	var b strings.Builder
	b.WriteString(renderHeader)
	b.WriteString("table " + TableRef + " {\n")
	b.WriteString(strings.Join(blocks, "\n"))
	b.WriteString("}\n")
	return b.String()
}

// inputRules returns the rules of the input chain (section 11).
func inputRules(n normalized) []string {
	var rules []string
	drops := false
	fromNodes := func(match string) {
		rules = append(rules, match+" ip saddr @"+SetNodes+" accept")
		if n.withV6 {
			rules = append(rules, match+" ip6 saddr @"+SetNodes6+" accept")
		}
	}
	if n.controlPort != 0 {
		m := "tcp dport " + strconv.Itoa(n.controlPort)
		if n.restrict {
			fromNodes(m)
			if n.unknownRate != "" {
				rules = append(rules, m+" ct state new limit rate "+n.unknownRate+" accept")
			}
			rules = append(rules, m+" drop")
			drops = true
		} else {
			rules = append(rules, m+" accept")
		}
	}
	if n.ctlLow != 0 {
		r := portRange(n.ctlLow, n.ctlHigh)
		rules = append(rules,
			"tcp dport "+r+" ip saddr @"+SetNodes+" accept",
			"udp dport "+r+" ip saddr @"+SetNodes+" accept")
		if n.withV6 {
			rules = append(rules,
				"tcp dport "+r+" ip6 saddr @"+SetNodes6+" accept",
				"udp dport "+r+" ip6 saddr @"+SetNodes6+" accept")
		}
		rules = append(rules, "tcp dport "+r+" drop", "udp dport "+r+" drop")
		drops = true
	}
	if len(n.tcp) > 0 {
		rules = append(rules, "tcp dport "+portSet(n.tcp)+" accept")
	}
	if len(n.udp) > 0 {
		rules = append(rules, "udp dport "+portSet(n.udp)+" accept")
	}
	if drops {
		rules = append([]string{ruleEstablished, ruleLoopback}, rules...)
	}
	return rules
}

// Rules that precede the drops of the input chain (see the package doc).
const (
	// ruleEstablished accepts replies to connections this host opened.
	ruleEstablished = "ct state established,related accept"
	// ruleLoopback accepts traffic between local processes: the canary's
	// 127.0.0.1:<port> and local probes of control ports must not hit the
	// drops meant for the Internet. Only locally generated packets arrive on
	// lo (the kernel drops 127.0.0.0/8 from real interfaces).
	ruleLoopback = `iif "lo" accept`
)

// natIfaces returns the sorted input interfaces of the NAT rules.
func (n normalized) natIfaces() []string {
	var out []string
	for _, r := range n.nat {
		if r.iface != "" && !slices.Contains(out, r.iface) {
			out = append(out, r.iface)
		}
	}
	slices.Sort(out)
	return out
}

// natRuleText renders one prerouting/output NAT rule.
func natRuleText(r natRule) string {
	var parts []string
	if r.iface != "" {
		parts = append(parts, "iifname "+quote(r.iface))
	}
	parts = append(parts, "fib daddr type local")
	switch {
	case r.to.Is4():
		parts = append(parts, "ip daddr != 127.0.0.0/8")
	case r.to.Is6():
		parts = append(parts, "ip6 daddr != ::1")
	}
	parts = append(parts, r.proto+" dport "+portRange(r.lo, r.hi))
	switch {
	case r.to.Is4() && r.toPort != 0:
		parts = append(parts, "dnat ip to "+r.to.String()+":"+strconv.Itoa(r.toPort))
	case r.to.Is4():
		parts = append(parts, "dnat ip to "+r.to.String())
	case r.to.Is6() && r.toPort != 0:
		parts = append(parts, "dnat ip6 to ["+r.to.String()+"]:"+strconv.Itoa(r.toPort))
	case r.to.Is6():
		parts = append(parts, "dnat ip6 to "+r.to.String())
	case r.toPort != 0:
		parts = append(parts, "redirect to :"+strconv.Itoa(r.toPort))
	default:
		parts = append(parts, "redirect")
	}
	return strings.Join(parts, " ")
}

func renderSet(name, typ string, addrs []netip.Addr) string {
	var b strings.Builder
	b.WriteString("\tset " + name + " {\n")
	b.WriteString("\t\ttype " + typ + "\n")
	if len(addrs) > 0 {
		s := make([]string, len(addrs))
		for i, a := range addrs {
			s[i] = a.String()
		}
		b.WriteString("\t\telements = { " + strings.Join(s, ", ") + " }\n")
	}
	b.WriteString("\t}\n")
	return b.String()
}

func renderChain(name, header string, rules []string) string {
	var b strings.Builder
	b.WriteString("\tchain " + name + " {\n")
	b.WriteString("\t\t" + header + "\n")
	for _, r := range rules {
		b.WriteString("\t\t" + r + "\n")
	}
	b.WriteString("\t}\n")
	return b.String()
}

// portRange renders "443" or "30000-31999".
func portRange(lo, hi int) string {
	if lo == hi {
		return strconv.Itoa(lo)
	}
	return strconv.Itoa(lo) + "-" + strconv.Itoa(hi)
}

// portSet renders sorted unique ports as "443" or "{ 443, 2053, 2000-2010 }";
// runs of three or more consecutive ports become an interval.
func portSet(ports []int) string {
	if len(ports) == 1 {
		return strconv.Itoa(ports[0])
	}
	var items []string
	for i := 0; i < len(ports); {
		j := i
		for j+1 < len(ports) && ports[j+1] == ports[j]+1 {
			j++
		}
		if j-i >= 2 {
			items = append(items, portRange(ports[i], ports[j]))
			i = j + 1
			continue
		}
		items = append(items, strconv.Itoa(ports[i]))
		i++
	}
	if len(items) == 1 {
		return items[0]
	}
	return "{ " + strings.Join(items, ", ") + " }"
}

// quote renders an nft string literal (names are validated, so no escaping
// is needed).
func quote(s string) string { return `"` + s + `"` }
