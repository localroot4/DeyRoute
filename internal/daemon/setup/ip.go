package setup

import (
	"context"
	"net/netip"
	"strings"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// Probe destinations for the source-address lookup (spec section 5:
// `ip route get 1.1.1.1`). Nothing is sent to them.
const (
	RouteProbe4 = "1.1.1.1"
	RouteProbe6 = "2606:4700:4700::1111"
)

// ipTimeout bounds one `ip route get` call.
const ipTimeout = 10 * time.Second

// cgnat is the shared address space of RFC 6598 (carrier-grade NAT).
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// DetectPublicIP returns the IPv4 source address the kernel uses to reach
// the internet (`ip -4 route get 1.1.1.1` → "src"). private is true for
// RFC 1918, CGNAT (100.64.0.0/10), loopback, link-local and other
// non-global addresses: the CLI then warns (DEY-I021) and lets the owner
// type the public IP. A missing ip command is DEY-I010; no route or no
// source address is DEY-I020.
func DetectPublicIP(ctx context.Context, r exec.Runner) (ip string, private bool, err error) {
	a, err := routeSource(ctx, r, "-4", RouteProbe4)
	if err != nil {
		return "", false, err
	}
	return a.String(), !IsPublicIP(a), nil
}

// DetectPublicIP6 is DetectPublicIP for IPv6 (`ip -6 route get
// 2606:4700:4700::1111`). A server without an IPv6 route is not an error:
// it returns "", false, nil. private is true for ULA (fc00::/7),
// link-local and other non-global addresses.
func DetectPublicIP6(ctx context.Context, r exec.Runner) (ip string, private bool, err error) {
	a, err := routeSource(ctx, r, "-6", RouteProbe6)
	if err != nil {
		if deyerr.HasCode(err, deyerr.I020) {
			return "", false, nil
		}
		return "", false, err
	}
	return a.String(), !IsPublicIP(a), nil
}

// IsPublicIP reports whether a is a global unicast address that can be
// reached from the internet: not private (RFC 1918, fc00::/7), CGNAT,
// loopback, link-local, multicast or unspecified.
func IsPublicIP(a netip.Addr) bool {
	a = a.Unmap()
	switch {
	case !a.IsValid(), a.IsUnspecified(), a.IsLoopback(), a.IsPrivate(), a.IsMulticast(),
		a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(), a.IsInterfaceLocalMulticast():
		return false
	case a.Is4() && cgnat.Contains(a):
		return false
	}
	return a.IsGlobalUnicast()
}

// routeSource runs `ip <family> route get <dst>` and returns the address
// after "src".
func routeSource(ctx context.Context, r exec.Runner, family, dst string) (netip.Addr, error) {
	if r == nil {
		r = exec.NewRunner()
	}
	ctx, cancel := context.WithTimeout(ctx, ipTimeout)
	defer cancel()
	cmd := "ip " + family + " route get " + dst
	out, _, err := r.Run(ctx, "ip", []string{family, "route", "get", dst}, nil)
	if err != nil {
		if deyerr.HasCode(err, deyerr.X030) {
			return netip.Addr{}, deyerr.Wrap(deyerr.I010, err, nil)
		}
		if _, exited := exec.ExitCode(err); exited {
			return netip.Addr{}, deyerr.Wrap(deyerr.I020, err, deyerr.Params{
				"reason": cmd + " found no route to the internet (no default gateway?)",
			}).WithDetail(deyerr.As(err).Detail)
		}
		return netip.Addr{}, err
	}
	a, ok := parseRouteSource(string(out))
	if !ok {
		return netip.Addr{}, deyerr.New(deyerr.I020, deyerr.Params{
			"reason": cmd + " printed no source address",
		}).WithDetail(strings.TrimSpace(string(out)))
	}
	return a, nil
}

// parseRouteSource extracts the "src <addr>" token of `ip route get`
// output, e.g. "1.1.1.1 via 10.0.0.1 dev eth0 src 10.0.0.5 uid 0".
func parseRouteSource(out string) (netip.Addr, bool) {
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		if f[i] != "src" {
			continue
		}
		a, err := netip.ParseAddr(f[i+1])
		if err != nil || a.Zone() != "" {
			return netip.Addr{}, false
		}
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}
