package hub

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// security.firewall_managed switched off while the hub was not running (a
// manual edit and a restart, a restore): the table an earlier hub process
// applied is removed at start, once; without such a table, or without nft,
// nothing is removed.
func TestUnmanagedFirewallAtStartRemovesAStaleTable(t *testing.T) {
	for name, tc := range map[string]struct {
		list    exec.Response
		deletes int
	}{
		"stale table": {list: exec.OK("table inet deyroute {\n\tset nodes {\n\t}\n}\n"), deletes: 1},
		"no table":    {list: exec.Fail(1, "Error: No such file or directory; did you mean table 'filter' in family inet?"), deletes: 0},
		"no nft":      {list: exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})}, deletes: 0},
	} {
		t.Run(name, func(t *testing.T) {
			env, o := prepareEnv(t, func(c *config.Config) { c.Security.FirewallManaged = false })
			o.DisableFirewall = false
			env.runner.On("nft list table inet deyroute", tc.list)
			env.startEnv(o)
			require.Eventually(t, func() bool { return env.h.Firewall().Computed }, testWait, 10*time.Millisecond)
			require.Equal(t, tc.deletes, env.runner.Count("nft delete table inet deyroute"))
			fw := env.h.Firewall()
			require.False(t, fw.Managed)
			require.NoError(t, fw.Err)

			// Later applies neither look again nor apply anything.
			lists := env.runner.Count("nft list table inet deyroute")
			require.NoError(t, env.h.applyFirewall(ctxT(t)))
			require.Equal(t, lists, env.runner.Count("nft list table inet deyroute"))
			require.Equal(t, tc.deletes, env.runner.Count("nft delete table inet deyroute"))
			require.Empty(t, env.nftScripts())
		})
	}
}
