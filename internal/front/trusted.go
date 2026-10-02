package front

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/localroot4/deyroute/internal/cfnets"
)

// headerCFConnectingIP is the canonical key of CF-Connecting-IP.
const headerCFConnectingIP = "Cf-Connecting-Ip"

// peerAddr returns the TCP peer address of c (zero Addr when it is not an
// IP address).
func peerAddr(c net.Conn) netip.AddrPort {
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		if a, ok := netip.AddrFromSlice(ta.IP); ok {
			return netip.AddrPortFrom(a.Unmap().WithZone(""), uint16(ta.Port)) // #nosec G115 -- a port is 0..65535
		}
	}
	if ap, err := netip.ParseAddrPort(c.RemoteAddr().String()); err == nil {
		return netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port())
	}
	return netip.AddrPort{}
}

// connectingIP returns the real client address from CF-Connecting-IP. The
// header must be present exactly once and hold one address that can be a
// client on the internet: loopback, private, link-local, unspecified,
// multicast and the reserved 240.0.0.0/4 are refused (a trusted proxy never
// sends those, so they are a sign of a forged or broken header).
func connectingIP(h http.Header) (netip.Addr, bool) {
	vals := h[headerCFConnectingIP]
	if len(vals) != 1 {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(vals[0]))
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if !publicUnicast(a) {
		return netip.Addr{}, false
	}
	return a, true
}

// publicUnicast reports whether a can be the address of a client on the
// internet.
func publicUnicast(a netip.Addr) bool {
	switch {
	case !a.IsValid(), a.IsLoopback(), a.IsPrivate(), a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(),
		a.IsInterfaceLocalMulticast(), a.IsUnspecified(), a.IsMulticast():
		return false
	case a.Is4():
		b := a.As4()
		if b[0] == 0 || b[0] >= 240 { // 0.0.0.0/8 "this network", 240.0.0.0/4 reserved and broadcast
			return false
		}
	}
	return true
}

// peerTrusted reports whether the TCP peer may tell the real client address:
// loopback (a reverse proxy or the test CDN on this host), a Cloudflare range,
// or one of the configured trusted proxies.
func peerTrusted(peer netip.Addr, extra []netip.Prefix) bool { return cfnets.Trusted(peer, extra) }
