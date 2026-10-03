// Package cfnets embeds the Cloudflare edge address ranges that front mode
// needs in two places: the hub firewall opens the front port to these
// source ranges only (internal/firewall renders them as nft interval sets),
// and the front listener trusts the CF-Connecting-IP header only on
// connections that come from one of them.
//
// The lists are the IPv4 and IPv6 ranges Cloudflare publishes at
// https://www.cloudflare.com/ips-v4 and https://www.cloudflare.com/ips-v6
// (also served as JSON by the provider's API under /client/v4/ips). The
// provider changes them rarely and announces changes in advance; when the
// published list changes, refresh the two lists below from those pages in a
// normal release. A stale list is not an outage by itself: a range that is
// missing from it makes the firewall drop (and the front distrust) that
// range, so affected requests fail closed instead of corrupting a node's
// address record (see Trusted). Hubs behind another CDN, or tests, widen the
// trust with trusted_proxies and open the port with cf_only: false.
//
// The package is a leaf: it imports only the standard library.
package cfnets

import (
	"net/netip"
	"slices"
)

// v4Ranges and v6Ranges are the published ranges, in the provider's order.
var (
	v4Ranges = []string{
		"173.245.48.0/20",
		"103.21.244.0/22",
		"103.22.200.0/22",
		"103.31.4.0/22",
		"141.101.64.0/18",
		"108.162.192.0/18",
		"190.93.240.0/20",
		"188.114.96.0/20",
		"197.234.240.0/22",
		"198.41.128.0/17",
		"162.158.0.0/15",
		"104.16.0.0/13",
		"104.24.0.0/14",
		"172.64.0.0/13",
		"131.0.72.0/22",
	}
	v6Ranges = []string{
		"2400:cb00::/32",
		"2606:4700::/32",
		"2803:f800::/32",
		"2405:b500::/32",
		"2405:8100::/32",
		"2a06:98c0::/29",
		"2c0f:f248::/32",
	}
)

// The ranges as prefixes. A malformed literal is a programming error caught
// by TestRangesAreValid; it is skipped here so the package can never panic
// inside a daemon.
var parsed4, parsed6 = parseAll(v4Ranges), parseAll(v6Ranges)

func parseAll(in []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(in))
	for _, s := range in {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// V4 returns the embedded IPv4 ranges. The slice is a copy.
func V4() []netip.Prefix { return slices.Clone(parsed4) }

// V6 returns the embedded IPv6 ranges. The slice is a copy.
func V6() []netip.Prefix { return slices.Clone(parsed6) }

// Contains reports whether addr lies in one of the embedded ranges. An
// IPv4-mapped IPv6 address (::ffff:a.b.c.d) is looked up as IPv4 and a zone
// is ignored; the zero Addr is in none.
func Contains(addr netip.Addr) bool { return in(addr, nil) }

// Trusted reports whether a connection from addr may carry a client address
// in a forwarded header (CF-Connecting-IP): addr is a loopback address (a
// reverse proxy or the test CDN on the same host, which the owner already
// trusts), one of the embedded ranges, or inside one of the extra prefixes
// (hub.front.trusted_proxies). Any other peer is never trusted, so a client
// that reaches the port directly cannot choose the address it is recorded
// under.
func Trusted(addr netip.Addr, extra []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	if addr.WithZone("").Unmap().IsLoopback() {
		return true
	}
	return in(addr, extra)
}

func in(addr netip.Addr, extra []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.WithZone("").Unmap()
	ranges := parsed6
	if addr.Is4() {
		ranges = parsed4
	}
	for _, p := range ranges {
		if p.Contains(addr) {
			return true
		}
	}
	for _, p := range extra {
		if p.IsValid() && p.Contains(addr) {
			return true
		}
	}
	return false
}
