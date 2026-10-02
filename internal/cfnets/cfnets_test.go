package cfnets

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// TestRangesAreValid checks every embedded literal: it parses, is in
// canonical (masked) form, belongs to the list of its family, and no range
// overlaps another (an nft interval set rejects overlapping elements).
func TestRangesAreValid(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []string
		got  []netip.Prefix
		is4  bool
	}{
		{"v4", v4Ranges, V4(), true},
		{"v6", v6Ranges, V6(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NotEmpty(t, tc.raw)
			require.Len(t, tc.got, len(tc.raw), "every literal must parse")
			for i, s := range tc.raw {
				p, err := netip.ParsePrefix(s)
				require.NoError(t, err, s)
				require.Equal(t, p, tc.got[i])
				require.Equal(t, p, p.Masked(), "%s is not in canonical form", s)
				require.Equal(t, tc.is4, p.Addr().Is4(), s)
				require.False(t, p.Addr().Is4In6(), s)
				for j, q := range tc.got {
					if i != j {
						require.False(t, p.Overlaps(q), "%s overlaps %s", p, q)
					}
				}
			}
		})
	}
	require.Len(t, V4(), 15)
	require.Len(t, V6(), 7)
}

func TestListsAreCopies(t *testing.T) {
	a := V4()
	a[0] = netip.MustParsePrefix("10.0.0.0/8")
	require.NotEqual(t, a[0], V4()[0])
	b := V6()
	b[0] = netip.MustParsePrefix("fd00::/8")
	require.NotEqual(t, b[0], V6()[0])
	require.False(t, Contains(netip.MustParseAddr("10.1.1.1")))
}

func TestContains(t *testing.T) {
	for _, s := range []string{
		"173.245.48.1", "173.245.63.255", "103.21.244.0", "104.16.0.1", "104.23.255.255", "104.24.1.1",
		"131.0.72.9", "162.159.255.255", "198.41.200.1", "172.71.0.1",
		"2606:4700::1", "2606:4700:ffff::1", "2a06:98c0::1", "2a06:98c7:ffff::1", "2400:cb00:1::1",
		"::ffff:104.16.0.1", // IPv4-mapped
	} {
		require.True(t, Contains(netip.MustParseAddr(s)), s)
	}
	for _, s := range []string{
		"173.245.64.0", "104.15.255.255", "104.32.0.0", "131.0.76.0", "172.63.255.255", "172.72.0.0",
		"1.1.1.1", "8.8.8.8", "127.0.0.1", "10.0.0.1", "0.0.0.0",
		"2606:4701::1", "2a06:98c8::1", "::1", "2001:db8::1", "::ffff:8.8.8.8",
	} {
		require.False(t, Contains(netip.MustParseAddr(s)), s)
	}
	require.False(t, Contains(netip.Addr{}))
	// A zone does not hide the address.
	require.True(t, Contains(netip.MustParseAddr("2606:4700::1%eth0")))
}

// lastAddr returns the highest address of p.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

// TestEveryRangeEdges: the first and last address of each range are inside
// and the addresses just outside are not (unless another range adjoins).
func TestEveryRangeEdges(t *testing.T) {
	for _, p := range append(V4(), V6()...) {
		first, last := p.Addr(), lastAddr(p)
		require.True(t, p.Contains(last), p)
		require.True(t, Contains(first), p)
		require.True(t, Contains(last), p)
		if before := first.Prev(); before.IsValid() && !adjoins(p, before) {
			require.False(t, Contains(before), "%s before %s", before, p)
		}
		if after := last.Next(); after.IsValid() && !adjoins(p, after) {
			require.False(t, Contains(after), "%s after %s", after, p)
		}
	}
}

// adjoins reports whether a range other than p holds a.
func adjoins(p netip.Prefix, a netip.Addr) bool {
	for _, q := range append(V4(), V6()...) {
		if q != p && q.Contains(a) {
			return true
		}
	}
	return false
}

func TestTrusted(t *testing.T) {
	extra := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	cases := []struct {
		addr  string
		extra []netip.Prefix
		want  bool
	}{
		{"104.16.0.1", nil, true},
		{"2606:4700::5", nil, true},
		{"127.0.0.1", nil, true}, // loopback: a local reverse proxy or the test CDN
		{"::1", nil, true},
		{"::ffff:127.0.0.1", nil, true},
		{"8.8.8.8", nil, false},
		{"192.0.2.7", nil, false},
		{"192.0.2.7", extra, true},
		{"::ffff:192.0.2.7", extra, true},
		{"2001:db8::7", extra, true},
		{"192.0.3.7", extra, false},
		{"2001:db9::1", extra, false},
		{"104.16.0.1", extra, true},
	}
	for _, c := range cases {
		require.Equal(t, c.want, Trusted(netip.MustParseAddr(c.addr), c.extra), "%s extra=%v", c.addr, c.extra)
	}
	require.False(t, Trusted(netip.Addr{}, extra))
	// An invalid extra prefix never matches.
	require.False(t, Trusted(netip.MustParseAddr("8.8.8.8"), []netip.Prefix{{}}))
}
