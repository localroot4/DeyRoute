package hub

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/systemd"
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

// A node agent that has not listed its units yet says so in its first
// heartbeats: the hub keeps the last list and runs the per-stream cleanup
// (stray units) on the first beat that carries the list, instead of on
// the first beat, where it would find nothing to stop.
func TestNodeCleanupWaitsForTheUnitList(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	beta := systemd.InstanceName(info.ID, "de-1", trBeta)
	before, _ := te.h.nodeState("de-1")
	require.NotEmpty(t, before.Units)

	n.stop()
	n.setUnit(beta, "active")
	n.unitsUnknown.Store(true)
	reconnected := time.Now()
	n.start()
	require.Eventually(t, func() bool {
		ns, _ := te.h.nodeState("de-1")
		return ns.Online && ns.LastHeartbeat.After(reconnected.Add(200*time.Millisecond))
	}, testWait, 10*time.Millisecond, "several heartbeats without a unit list")
	require.NotContains(t, n.stoppedList(), beta)
	ns, _ := te.h.nodeState("de-1")
	require.Equal(t, before.Units, ns.Units, "the last list is kept")

	n.unitsUnknown.Store(false)
	require.Eventually(t, func() bool { return slices.Contains(n.stoppedList(), beta) }, testWait, 20*time.Millisecond)
	te.waitActive(info.ID, "de-1", trAlpha)
}
