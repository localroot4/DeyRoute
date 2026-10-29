package node

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// TestNodeFirewallReassertBypassesCache: firewall.apply with unchanged NAT
// rules short-circuits on the cached rule set, so a flushed table stays
// gone; the periodic check sees the missing or changed table and applies
// it again past the cache (DR-14). A node without NAT rules has no table
// to restore and lists nothing.
func TestNodeFirewallReassertBypassesCache(t *testing.T) {
	sys := newFakeSys()
	a := newTestAgent(t, sys, &recHooks{})
	logs := &syncBuffer{}
	a.log = slog.New(dlog.NewHandler(logs, slog.LevelDebug, "node"))
	ctx := context.Background()
	hop := api.NodeFirewallArgs{NAT: []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123}}}
	require.NoError(t, a.nodeFirewall(ctx, hop))
	require.Len(t, sys.nftScripts(), 1)
	want := sys.liveTable()
	require.Contains(t, want, "dport 20000-20999")

	require.Empty(t, a.reassertFirewall(ctx), "unchanged: nothing to do")
	require.Len(t, sys.nftScripts(), 1)

	sys.setTable("")
	require.NoError(t, a.nodeFirewall(ctx, hop))
	require.Len(t, sys.nftScripts(), 1, "the cache short-circuits firewall.apply")
	require.Equal(t, reassertMissing, a.reassertFirewall(ctx))
	require.Len(t, sys.nftScripts(), 2)
	require.Equal(t, want, sys.liveTable())
	require.Contains(t, logs.String(), "firewall_reapplied")
	require.Contains(t, logs.String(), `"reason":"missing"`)

	sys.setTable(strings.Replace(want, "redirect to :30123", "redirect to :30999", 1))
	require.Equal(t, reassertDrift, a.reassertFirewall(ctx))
	require.Len(t, sys.nftScripts(), 3)
	require.Equal(t, want, sys.liveTable())

	// A second flush within 10 minutes: applied again, logged without a
	// second warning.
	sys.setTable("")
	require.Equal(t, reassertMissing, a.reassertFirewall(ctx))
	require.Equal(t, 1, strings.Count(logs.String(), `"level":"warn","msg":"table inet deyroute was missing`))

	// No NAT rules: the table is removed and nothing is restored.
	require.NoError(t, a.nodeFirewall(ctx, api.NodeFirewallArgs{}))
	require.Empty(t, sys.liveTable())
	lists := sys.lists()
	require.Empty(t, a.reassertFirewall(ctx))
	require.Equal(t, lists, sys.lists())
	require.Len(t, sys.nftScripts(), 4)
}

// TestNodeFirewallReassertOff: a node whose config.yaml sets
// security.firewall_managed false, or firewall_reassert_s 0, never checks.
func TestNodeFirewallReassertOff(t *testing.T) {
	off := 0
	for name, sec := range map[string]*config.Security{
		"unmanaged":  {FirewallManaged: false},
		"interval 0": {FirewallManaged: true, FirewallReassertS: &off},
	} {
		t.Run(name, func(t *testing.T) {
			sys := newFakeSys()
			root := t.TempDir()
			cfg := config.NewNode(testNode, "5.6.7.8:44433", "sha256:"+strings.Repeat("a", 64))
			cfg.Security = sec
			a := newAgent(Options{Root: root, Runner: sys.runner(), Hooks: &recHooks{}}.withDefaults(), cfg,
				root+config.DefaultPath, dlog.Discard())
			require.Zero(t, a.reassertEvery)
			ctx := context.Background()
			hop := api.NodeFirewallArgs{NAT: []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123}}}
			require.NoError(t, a.nodeFirewall(ctx, hop))
			sys.setTable("")
			require.Empty(t, a.reassertFirewall(ctx))
			require.Zero(t, sys.lists())
			require.Len(t, sys.nftScripts(), 1)
		})
	}
	a := newTestAgent(t, newFakeSys(), &recHooks{})
	require.Equal(t, time.Minute, a.reassertEvery)
}

// TestNodeSelfUpdateRefusedWithFirewallReassertKey: the node reads
// security.firewall_reassert_s from its own config.yaml, so while it is set
// a self.update to a release older than the key's first one is refused
// (DEY-S011) like for node.allow_unsigned_updates: that binary would refuse
// config.yaml and the node could not start again. Newer targets and a node
// without the key are not refused.
func TestNodeSelfUpdateRefusedWithFirewallReassertKey(t *testing.T) {
	withVersion(t, "0.3.1-edge.5")
	cfg := config.NewNode(testNode, "5.6.7.8:44433", "sha256:"+strings.Repeat("a", 64))
	require.NoError(t, refuseOlderThanNodeKeys(cfg, "0.3.1-edge.4"))
	every := 30
	cfg.Security = &config.Security{FirewallManaged: true, FirewallReassertS: &every}
	for _, v := range []string{"0.3.1-edge.4", "0.3.0"} {
		de := requireCode(t, refuseOlderThanNodeKeys(cfg, v), deyerr.S011)
		require.Contains(t, de.Message(), "security.firewall_reassert_s (since 0.3.1)", v)
		require.Contains(t, de.Fix(), "/etc/deyroute/config.yaml", v)
	}
	require.NoError(t, refuseOlderThanNodeKeys(cfg, "0.3.1-edge.6"))
	require.NoError(t, refuseOlderThanNodeKeys(cfg, "0.3.1"))
}
