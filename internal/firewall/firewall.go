// Package firewall manages deyroute's own nftables table and inspects the
// other firewalls on the server (spec sections 10, 11 and 7.7).
//
// deyroute only ever creates, replaces and deletes its own two tables:
// `table inet deyroute` (the firewall described here) and the verdict-free
// traffic accounting table `table inet deyroute_stats` (stats.go: named
// counters per tunnel, no accept, drop or reject anywhere); it never touches
// another table or another firewall on its own. Each table is rendered as
// text (Render, RenderStats, golden-tested) and applied with `nft -f -` in a
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
// Front mode: the CDN front port (Spec.FrontPort) is reached by the CDN
// edges, not by the nodes. By default it is accepted only from the
// Cloudflare ranges (the interval sets @cf4/@cf6, filled from
// internal/cfnets) and dropped for every other source, so scanners cannot
// reach the origin directly; Spec.FrontOpen accepts it from everyone (a lab,
// another CDN).
//
// NAT (section 7.7 WireGuard/AmneziaWG on the hub, section 7.6 Hysteria2
// port hopping on the node): a DNAT rule forwards a listen port to a
// tunnel peer (10.77.n.2:<port>) and masquerade rewrites the source on the
// tunnel interface so replies return through the hub. Only packets
// addressed to this host are translated (fib daddr type local), so traffic
// the host routes for others (containers, other networks) and the host's own
// outgoing connections to remote port 443 are never hijacked. A rule can be
// scoped further to source addresses and one destination address
// (backend.NATRule SAddr, DAddr; Hysteria2 port hopping matches only the
// hub's addresses, DR-18): it renders once per source family, as
// `ip saddr { … } ip daddr X …` and `ip6 saddr { … } …`. The same DNAT
// runs in the nat output hook so locally generated traffic to one of the
// host's own addresses traverses the tunnel too; that is how the hub's path
// probe reaches the node for NAT-based transports. Loopback (127.0.0.0/8,
// ::1) is excluded on purpose: a packet with a loopback source cannot leave
// through a real interface unless route_localnet=1, which exposes every
// localhost-only service to the network (the CVE-2020-8558 class), so
// deyroute does not enable it. The path probe of a NAT-based transport must
// therefore dial a non-loopback local address (the hub's public IP or its
// tunnel address 10.77.n.1), not 127.0.0.1. A forward filter chain clamps
// the TCP MSS of SYNs on every tunnel interface (masqueraded on the hub,
// NAT-matched on the node), because the tunnel MTU is smaller than the
// clients' and ICMP "fragmentation needed" is often filtered on the way:
// towards the tunnel (oifname) to the route MTU, and from the tunnel
// (iifname) to Spec.ClampMSS, since the route MTU of that direction is the
// outgoing interface's and would not lower anything. The kernel never
// raises an MSS, and the clamps are not verdicts, so they come before the
// confinement and change no accept or drop. An interface that NAT rules
// match on (the node side of WireGuard: DNAT of tunnel traffic to the local
// targets) is confined in the same chain: traffic from it that would be
// routed onward is dropped unless it was DNATed or belongs to an
// established flow, so a node with net.ipv4.ip_forward already on (Docker,
// another VPN) never routes for the hub (section 11, scenario S17).
//
// A node's changed public IP (section 11) is handled by re-rendering the
// Spec with the new address in NodeIPs4 and calling Apply again; the
// replacement is atomic and does not disturb established connections.
package firewall

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
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

// Names of the dynamic sets of the per-source admission of unknown
// control-port sources (Spec.UnknownControlRatePerSource): one element
// with its own rate bucket per source address, forgotten after
// ctlUnknownTimeout.
const (
	SetCtlUnknown4 = "ctl_unknown4"
	SetCtlUnknown6 = "ctl_unknown6"
	// ctlUnknownSize bounds each set; ctlUnknownTimeout is how long a
	// source's bucket lives.
	ctlUnknownSize    = 65535
	ctlUnknownTimeout = "10m"
)

// Names of the interval sets holding the Cloudflare edge ranges (rendered
// only for a front port that is not open to everyone).
const (
	SetCF4 = "cf4"
	SetCF6 = "cf6"
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
	// NAT are DNAT/redirect rules of NAT-based transports. The input
	// interface of a rule (Iface) is also confined in the forward chain.
	NAT []backend.NATRule
	// Masquerade are interfaces to masquerade on (postrouting).
	Masquerade []string
	// IPv6 renders @nodes6 (and its rules) even when no node has an IPv6
	// address yet (the hub has a public IPv6, section 10).
	IPv6 bool
	// UnknownControlRate, when non-empty and RestrictControl is true, admits
	// new control-port connections from addresses outside @nodes/@nodes6 at
	// this nft rate ("6/minute", optionally "… burst 5 packets") right
	// before the control-port drop, so a node whose public IP changed can
	// reconnect with its certificate (section 11, QUESTIONS.md C.23). mTLS
	// and the join limiter protect the port. "" = drop everyone else.
	// With UnknownControlRatePerSource it is the global cap behind the
	// per-source limit.
	UnknownControlRate string
	// UnknownControlRatePerSource, when non-empty together with
	// UnknownControlRate, limits the admission per source address (DR-15):
	// each source is added to the dynamic set @ctl_unknown4/@ctl_unknown6
	// with its own bucket at this rate (without a burst, a burst of the
	// rate's count), and UnknownControlRate caps all of them together, so
	// one scanner can no longer use up the admission of a node whose IP
	// changed. "" = the single global rule at UnknownControlRate (the
	// fallback for an nft that cannot render the per-source form, see
	// Applier).
	UnknownControlRatePerSource string
	// FrontPort is the hub's CDN front listener port (front mode, 0 = none).
	// It is reachable from the Internet side only, never through @nodes: the
	// traffic comes from the CDN edges, not from the nodes.
	FrontPort int
	// FrontOpen opens FrontPort to everyone ("tcp dport P accept"); false
	// (the default) accepts it only from the Cloudflare ranges embedded in
	// internal/cfnets, declared as the interval sets @cf4/@cf6, and drops
	// everything else to that port. No connection-rate limit is applied to
	// the front port in either mode (see inputRules).
	FrontOpen bool
	// ClampMSS is the TCP MSS that SYNs arriving from a tunnel interface
	// are clamped to: the tunnel MTU minus 40 (IPv4 and TCP headers).
	// 0 = DefaultClampMSS (the WireGuard default MTU 1420 minus 40).
	ClampMSS int
}

// MSS clamp bounds and default (Spec.ClampMSS).
const (
	// DefaultClampMSS is the WireGuard default MTU (1420) minus 40.
	DefaultClampMSS = 1380
	// MinClampMSS is the smallest MSS every IPv4 host must accept (RFC 879),
	// MaxClampMSS that of a 9000-byte jumbo MTU.
	MinClampMSS = 536
	MaxClampMSS = 8960
)

// ClampMSSForMTU returns the MSS for a tunnel MTU: mtu minus 40 (IPv4 and
// TCP headers), DefaultClampMSS for 0 (the backend default MTU).
func ClampMSSForMTU(mtu int) int {
	if mtu <= 0 {
		return DefaultClampMSS
	}
	return mtu - 40
}

// clampMSS returns s.ClampMSS, or DefaultClampMSS when it is unset or out of
// range (Validate reports the latter).
func (s Spec) clampMSS() int {
	if s.ClampMSS >= MinClampMSS && s.ClampMSS <= MaxClampMSS {
		return s.ClampMSS
	}
	return DefaultClampMSS
}

// validRate reports whether r can be rendered as `limit rate <r>`: packets
// per unit with an optional packet burst (config.ValidRate, which checks
// the configuration keys the same way).
func validRate(r string) bool { return config.ValidRate(r) }

// withBurst returns rate r with a burst of its own count when it has none
// ("3/minute" → "3/minute burst 3 packets"): nft's default burst of 5
// packets would let a source open more connections at once than its rate
// allows in a whole interval.
func withBurst(r string) string {
	if strings.Contains(r, " burst ") {
		return r
	}
	n, _, _ := strings.Cut(r, "/")
	return r + " burst " + n + " packets"
}

// ifaceRe is a Linux interface name (IFNAMSIZ-1 = 15 bytes; no '/', ':',
// whitespace or quotes).
var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.@+-]{1,15}$`)

// validIface reports whether name can be used as an interface name.
func validIface(name string) bool {
	return ifaceRe.MatchString(name) && name != "." && name != ".."
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// inCtlRange reports whether p lies in the valid backend control range
// low-high.
func inCtlRange(p, low, high int) bool {
	return validPort(low) && validPort(high) && low <= high && p >= low && p <= high
}

// Validate reports every field Render would have to drop, as DEY-P019
// wrapping the joined problems; nil when the Spec renders completely.
func (s Spec) Validate() error {
	var probs []string
	add := func(format string, a ...any) { probs = append(probs, fmt.Sprintf(format, a...)) }
	if s.ControlPort != 0 && !validPort(s.ControlPort) {
		add("control port %d out of range", s.ControlPort)
	}
	if s.UnknownControlRate != "" && !validRate(s.UnknownControlRate) {
		add("unknown control rate %q invalid (want e.g. 6/minute)", s.UnknownControlRate)
	}
	if s.UnknownControlRatePerSource != "" && !validRate(s.UnknownControlRatePerSource) {
		add("unknown control rate per source %q invalid (want e.g. 3/minute)", s.UnknownControlRatePerSource)
	}
	if s.CtlLow != 0 || s.CtlHigh != 0 {
		if !validPort(s.CtlLow) || !validPort(s.CtlHigh) || s.CtlLow > s.CtlHigh {
			add("backend control range %d-%d invalid", s.CtlLow, s.CtlHigh)
		}
	}
	if s.FrontPort != 0 {
		switch {
		case !validPort(s.FrontPort):
			add("front port %d out of range", s.FrontPort)
		case s.FrontPort == s.ControlPort:
			add("front port %d is the control port", s.FrontPort)
		case inCtlRange(s.FrontPort, s.CtlLow, s.CtlHigh):
			add("front port %d is inside the backend control range %d-%d", s.FrontPort, s.CtlLow, s.CtlHigh)
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
	if s.ClampMSS != 0 && (s.ClampMSS < MinClampMSS || s.ClampMSS > MaxClampMSS) {
		add("clamp mss %d out of range %d-%d", s.ClampMSS, MinClampMSS, MaxClampMSS)
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

// MaxNATSources is the most source addresses (backend.NATRule.SAddr) one
// NAT rule may name.
const MaxNATSources = 16

// natRule is a validated, normalized backend.NATRule. It is comparable, so
// the source scope is kept as the sorted, deduplicated addresses joined by
// ", " (sources splits it again).
type natRule struct {
	proto  string
	lo, hi int
	to     netip.Addr // invalid = redirect to a local port
	toPort int        // 0 = keep the destination port
	iface  string
	saddr  string     // "" = any source
	daddr  netip.Addr // invalid = any local address
}

// sources returns the source addresses of family 4 or 6.
func (n natRule) sources(family int) []string {
	var out []string
	for s := range strings.SplitSeq(n.saddr, ", ") {
		if a, err := netip.ParseAddr(s); err == nil && (a.Is4() == (family == 4)) {
			out = append(out, s)
		}
	}
	return out
}

// sameFamily reports whether a and b are both IPv4 or both IPv6.
func sameFamily(a, b netip.Addr) bool { return a.Is4() == b.Is4() }

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
	return normalizeScope(n, r)
}

// normalizeScope validates and adds the source and destination scope of r
// (DR-18): plain addresses, no loopback destination (see the package doc on
// loopback), and families that can match together: a source of the
// destination's and of the target's family, and a destination of the
// target's family.
func normalizeScope(n natRule, r backend.NATRule) (natRule, string) {
	if len(r.SAddr) > MaxNATSources {
		return n, fmt.Sprintf("more than %d source addresses", MaxNATSources)
	}
	var src []netip.Addr
	for _, s := range r.SAddr {
		a, ok := parseNodeIP(s)
		if !ok {
			return n, fmt.Sprintf("source address %q invalid", s)
		}
		if !slices.Contains(src, a) {
			src = append(src, a)
		}
	}
	slices.SortFunc(src, netip.Addr.Compare)
	if r.DAddr != "" {
		a, ok := parseNodeIP(r.DAddr)
		if !ok || a.IsLoopback() {
			return n, fmt.Sprintf("destination address %q invalid", r.DAddr)
		}
		n.daddr = a
	}
	hasFamily := func(b netip.Addr) bool {
		return slices.ContainsFunc(src, func(a netip.Addr) bool { return sameFamily(a, b) })
	}
	switch {
	case n.daddr.IsValid() && n.to.IsValid() && !sameFamily(n.daddr, n.to):
		return n, "destination and target address families differ"
	case len(src) > 0 && n.daddr.IsValid() && !hasFamily(n.daddr):
		return n, "no source address of the destination's family"
	case len(src) > 0 && n.to.IsValid() && !hasFamily(n.to):
		return n, "no source address of the target's family"
	}
	parts := make([]string, len(src))
	for i, a := range src {
		parts[i] = a.String()
	}
	n.saddr = strings.Join(parts, ", ")
	return n, ""
}
