package node

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// hopRule is the node side of Hysteria2 port hopping as a hub announcing
// DR-18 sends it: scoped to the hub's address and the node's public one.
func hopRule(daddr string) backend.NATRule {
	return backend.NATRule{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123,
		SAddr: []string{"5.6.7.8"}, DAddr: daddr}
}

// TestNodeCheckNATValidatesAddrs: the node refuses (DEY-N050, nothing
// applied) a NAT rule whose source scope or destination is not a plain
// unicast address, or whose families cannot match, and accepts a valid
// scope (DR-18). It announces api.FeatureNATScope, so the hub sends it
// the scoped rule.
func TestNodeCheckNATValidatesAddrs(t *testing.T) {
	sys := newFakeSys()
	a := newTestAgent(t, sys, &recHooks{})
	a.o.LocalAddrs = func() ([]netip.Addr, error) { return []netip.Addr{netip.MustParseAddr("1.2.3.4")}, nil }
	ctx := context.Background()
	hello := a.hello()
	require.True(t, hello.HasFeature(api.FeatureNATScope))

	bad := map[string]backend.NATRule{
		"source not an address":   {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, SAddr: []string{"hub.example"}},
		"source with zone":        {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, SAddr: []string{"fe80::1%eth0"}},
		"source unspecified":      {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, SAddr: []string{"0.0.0.0"}},
		"source loopback":         {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, SAddr: []string{"127.0.0.1"}},
		"source multicast":        {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, SAddr: []string{"224.0.0.1"}},
		"source link-local":       {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, SAddr: []string{"fe80::1"}},
		"destination prefix":      hopRule("1.2.3.0/24"),
		"destination loopback":    hopRule("127.0.0.1"),
		"destination multicast":   hopRule("ff02::1"),
		"destination link-local":  {Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123, DAddr: "169.254.0.1"},
		"families cannot match":   hopRule("2001:db8::7"),
		"target family differs":   {Proto: "tcp", DportLow: 443, ToAddr: "127.0.0.1", ToPort: 443, DAddr: "2001:db8::7"},
		"no source of the target": {Proto: "tcp", DportLow: 443, ToAddr: "127.0.0.1", ToPort: 443, SAddr: []string{"2001:db8::5"}},
	}
	for name, r := range bad {
		err := a.nodeFirewall(ctx, api.NodeFirewallArgs{NAT: []backend.NATRule{r}})
		require.Error(t, err, name)
		require.Equal(t, deyerr.N050, deyerr.As(err).Code, name)
		require.Empty(t, sys.nftScripts(), name)
		require.Error(t, a.checkNAT(api.CmdBackendRender, []backend.NATRule{r}), name)
	}

	// A valid scope is applied as it came.
	require.NoError(t, a.nodeFirewall(ctx, api.NodeFirewallArgs{NAT: []backend.NATRule{hopRule("1.2.3.4")}}))
	scripts := sys.nftScripts()
	require.Len(t, scripts, 1)
	require.Contains(t, scripts[0], "ip saddr { 5.6.7.8 } ip daddr 1.2.3.4 udp dport 20000-20999 redirect to :30123")

	// A changed scope (the hub moved) is a changed rule set: applied again.
	moved := hopRule("1.2.3.4")
	moved.SAddr = []string{"7.7.7.7"}
	require.NoError(t, a.nodeFirewall(ctx, api.NodeFirewallArgs{NAT: []backend.NATRule{moved}}))
	scripts = sys.nftScripts()
	require.Len(t, scripts, 2)
	require.Contains(t, scripts[1], "ip saddr { 7.7.7.7 } ip daddr 1.2.3.4 udp dport")
}

// TestHysteria2HoppingNATBehindNAT: on a node whose public_ip is not one of
// its own addresses (a cloud VPS behind 1:1 NAT: packets arrive with the
// private interface address as destination), the node applies the hopping
// rule with its source scope and without the destination, which would
// never match, and logs that once (DR-18). When the address is its own,
// the destination stays. An unreadable address list keeps the source scope
// too.
func TestHysteria2HoppingNATBehindNAT(t *testing.T) {
	sys := newFakeSys()
	a := newTestAgent(t, sys, &recHooks{})
	logs := &syncBuffer{}
	a.log = slog.New(dlog.NewHandler(logs, slog.LevelDebug, "node"))
	local := []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.0.0.5")}
	var addrErr error
	a.o.LocalAddrs = func() ([]netip.Addr, error) { return local, addrErr }
	ctx := context.Background()
	hop := api.NodeFirewallArgs{NAT: []backend.NATRule{hopRule("1.2.3.4")}}

	require.NoError(t, a.nodeFirewall(ctx, hop))
	scripts := sys.nftScripts()
	require.Len(t, scripts, 1)
	require.Contains(t, scripts[0], "fib daddr type local ip saddr { 5.6.7.8 } udp dport 20000-20999 redirect to :30123")
	require.NotContains(t, scripts[0], "daddr 1.2.3.4")
	require.Equal(t, 1, strings.Count(logs.String(), "is not an address of this node"), logs.String())
	require.Contains(t, logs.String(), `"daddr":"1.2.3.4"`)

	// Applied again (a re-render, the periodic re-assert): logged once.
	require.NoError(t, a.syncFirewall(ctx, true))
	require.Len(t, sys.nftScripts(), 2)
	require.NotContains(t, sys.nftScripts()[1], "daddr 1.2.3.4")
	require.Equal(t, 1, strings.Count(logs.String(), "is not an address of this node"))

	// The address moved onto the node (a floating IP): the destination
	// is matched again, and the rule set counts as changed.
	local = append(local, netip.MustParseAddr("1.2.3.4"))
	require.NoError(t, a.syncFirewall(ctx, false))
	scripts = sys.nftScripts()
	require.Len(t, scripts, 3)
	require.Contains(t, scripts[2], "ip saddr { 5.6.7.8 } ip daddr 1.2.3.4 udp dport 20000-20999 redirect to :30123")

	// The addresses cannot be read: the source scope stays, the
	// destination goes.
	addrErr = errors.New("netlink: permission denied")
	require.NoError(t, a.syncFirewall(ctx, false))
	scripts = sys.nftScripts()
	require.Len(t, scripts, 4)
	require.Contains(t, scripts[3], "ip saddr { 5.6.7.8 } udp dport 20000-20999 redirect to :30123")
	require.NotContains(t, scripts[3], "daddr 1.2.3.4")
	require.Contains(t, logs.String(), "netlink: permission denied")
}
