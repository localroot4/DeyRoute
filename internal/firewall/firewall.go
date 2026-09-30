// Package firewall manages deyroute's own nftables table and inspects the
// other firewalls on the server (spec sections 10, 11 and 7.7).
//
// deyroute only ever creates, replaces and deletes `table inet deyroute`; it
// never touches another table or another firewall on its own. The table is
// rendered as text (Render, golden-tested) and applied with `nft -f -` in a
// single transaction that deletes and recreates it (QUESTIONS.md C.7), so a
// change is atomic: either the new table is live or the old one still is.
//
// nftables semantics matter for what the table can and cannot do: every
// base chain on a hook runs, an accept verdict only ends its own chain, and
// a drop anywhere is final. The accept rules of `inet deyroute` therefore
// document intent but cannot open a port that ufw, firewalld, iptables or
// another nftables table drops. Check (and Blocks) detects such external
// blocks heuristically and returns the exact command that would open the
// port; the command runs only after the owner confirms (Open), never on its
// own.
//
// Hub table (section 11): the control port and the backend control range
// 30000-31999 are reachable only from the node addresses in the sets
// @nodes/@nodes6 and dropped for everyone else; tunnel listen ports are
// accepted. Replies to connections this server opened are accepted first
// (ct state established,related) because the balanced sysctl profile widens
// ip_local_port_range to 10240-65535, which overlaps 30000-31999: without
// that rule the replies to an outgoing connection whose local port falls in
// the backend range would be dropped. Loopback traffic (iif "lo") is
// accepted next: the drops are meant for the Internet, and local processes
// reach these ports over 127.0.0.1 (the canary unit's loopback port of
// section 9, local probes and diagnostics).
//
// NAT (section 7.7 WireGuard/AmneziaWG on the hub, section 7.6 Hysteria2
// port hopping on the node): a DNAT rule forwards a listen port to a
// tunnel peer (10.77.n.2:<port>) and masquerade rewrites the source on the
// tunnel interface so replies return through the hub. Only packets
// addressed to this host are translated (fib daddr type local), so traffic
// the host routes for others (containers, other networks) and the host's own
// outgoing connections to remote port 443 are never hijacked. The same DNAT
// runs in the nat output hook so locally generated traffic to one of the
// host's own addresses traverses the tunnel too; that is how the hub's path
// probe reaches the node for NAT-based transports. Loopback (127.0.0.0/8,
// ::1) is excluded on purpose: a packet with a loopback source cannot leave
// through a real interface unless route_localnet=1, which exposes every
// localhost-only service to the network (the CVE-2020-8558 class), so
// deyroute does not enable it. The path probe of a NAT-based transport must
// therefore dial a non-loopback local address (the hub's public IP or its
// tunnel address 10.77.n.1), not 127.0.0.1. A forward filter chain clamps
// the TCP MSS to the route MTU on masqueraded interfaces, because the
// tunnel MTU is smaller than the clients' and ICMP "fragmentation needed"
// is often filtered on the way.
//
// A node's changed public IP (section 11) is handled by re-rendering the
// Spec with the new address in NodeIPs4 and calling Apply again; the
// replacement is atomic and does not disturb established connections.
package firewall

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Kind names a firewall implementation.
type Kind string

// Firewall kinds, in detection order (section 10).
const (
	NFTables  Kind = "nftables"
	UFW       Kind = "ufw"
	Firewalld Kind = "firewalld"
	IPTables  Kind = "iptables"
)

// The table deyroute owns.
const (
	TableFamily = "inet"
	TableName   = "deyroute"
	// TableRef is "inet deyroute" as written in nft commands.
	TableRef = TableFamily + " " + TableName
)

// Names of the sets holding the joined nodes' public addresses.
const (
	SetNodes  = "nodes"
	SetNodes6 = "nodes6"
)

// Protocols of listen ports and NAT rules.
const (
	ProtoTCP = "tcp"
	ProtoUDP = "udp"
)

// Spec is everything the `inet deyroute` table is rendered from.
type Spec struct {
	// ControlPort is the hub Control API port (0 = no rule, e.g. on a node).
	ControlPort int
	// RestrictControl limits ControlPort to @nodes/@nodes6 and drops the
	// rest; false renders a plain accept (e.g. while a join is pending).
	RestrictControl bool
	// NodeIPs4 and NodeIPs6 are the joined nodes' public addresses. An
	// address in the wrong list is placed in the set of its family.
	NodeIPs4, NodeIPs6 []string
	// CtlLow-CtlHigh is the backend control range (30000-31999), reachable
	// only from the nodes; 0 = no rules.
	CtlLow, CtlHigh int
	// ListenTCP and ListenUDP are the active tunnel listen ports.
	ListenTCP, ListenUDP []int
	// NAT are DNAT/redirect rules of NAT-based transports.
	NAT []backend.NATRule
	// Masquerade are interfaces to masquerade on (postrouting).
	Masquerade []string
	// IPv6 renders @nodes6 (and its rules) even when no node has an IPv6
	// address yet (the hub has a public IPv6, section 10).
	IPv6 bool
}

// ifaceRe is a Linux interface name (IFNAMSIZ-1 = 15 bytes; no '/', ':',
// whitespace or quotes).
var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.@+-]{1,15}$`)

// validIface reports whether name can be used as an interface name.
func validIface(name string) bool {
	return ifaceRe.MatchString(name) && name != "." && name != ".."
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// Validate reports every field Render would have to drop, as DEY-P019
// wrapping the joined problems; nil when the Spec renders completely.
func (s Spec) Validate() error {
	var probs []string
	add := func(format string, a ...any) { probs = append(probs, fmt.Sprintf(format, a...)) }
	if s.ControlPort != 0 && !validPort(s.ControlPort) {
		add("control port %d out of range", s.ControlPort)
	}
	if s.CtlLow != 0 || s.CtlHigh != 0 {
		if !validPort(s.CtlLow) || !validPort(s.CtlHigh) || s.CtlLow > s.CtlHigh {
			add("backend control range %d-%d invalid", s.CtlLow, s.CtlHigh)
		}
	}
	for _, list := range [][]string{s.NodeIPs4, s.NodeIPs6} {
		for _, ip := range list {
			if _, ok := parseNodeIP(ip); !ok {
				add("node address %q invalid", ip)
			}
		}
	}
	for _, p := range s.ListenTCP {
		if !validPort(p) {
			add("listen port %d/tcp out of range", p)
		}
	}
	for _, p := range s.ListenUDP {
		if !validPort(p) {
			add("listen port %d/udp out of range", p)
		}
	}
	for _, r := range s.NAT {
		if _, err := normalizeNAT(r); err != "" {
			add("nat rule %+v: %s", r, err)
		}
	}
	for _, m := range s.Masquerade {
		if !validIface(m) {
			add("masquerade interface %q invalid", m)
		}
	}
	if len(probs) == 0 {
		return nil
	}
	detail := strings.Join(probs, "; ")
	return deyerr.Wrap(deyerr.P019, deyerr.Plain("invalid firewall spec: "+detail),
		deyerr.Params{"firewall": string(NFTables)}).
		WithWhy("the rules deyroute generated for table inet deyroute are invalid, so nft was not run").
		WithDetail(detail)
}

// parseNodeIP parses a node address; zones and unspecified addresses are
// rejected.
func parseNodeIP(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.Zone() != "" || a.IsUnspecified() {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// natRule is a validated, normalized backend.NATRule.
type natRule struct {
	proto  string
	lo, hi int
	to     netip.Addr // invalid = redirect to a local port
	toPort int        // 0 = keep the destination port
	iface  string
}

// normalizeNAT validates r; the second result is the problem ("" = ok).
func normalizeNAT(r backend.NATRule) (natRule, string) {
	n := natRule{proto: strings.ToLower(r.Proto), lo: r.DportLow, hi: r.DportHigh, toPort: r.ToPort, iface: r.Iface}
	if n.proto != ProtoTCP && n.proto != ProtoUDP {
		return n, "protocol must be tcp or udp"
	}
	if n.hi == 0 {
		n.hi = n.lo
	}
	if !validPort(n.lo) || !validPort(n.hi) || n.lo > n.hi {
		return n, "destination port range invalid"
	}
	if n.toPort != 0 && !validPort(n.toPort) {
		return n, "target port out of range"
	}
	if r.ToAddr != "" {
		a, ok := parseNodeIP(r.ToAddr)
		if !ok {
			return n, "target address invalid"
		}
		n.to = a
	}
	if n.iface != "" && !validIface(n.iface) {
		return n, "interface name invalid"
	}
	return n, ""
}
