package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// A new node order (tunnel edit --node, DR-13) does not move traffic: the
// active node stays active, and the canary fails back to the new primary
// on its normal schedule.
func TestTunnelEditReorderKeepsActiveUntilFailback(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	nl := te.tunnelNode("nl-1")
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Backups: []string{"nl-1"}, Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trAlpha}, Failover: fastFailover(true)})
	id := info.ID
	te.waitActive(id, "de-1", trAlpha)

	got, err := te.client.TunnelEdit(ctxT(t), id, api.TunnelEditRequest{Nodes: []string{"nl-1", "de-1"}}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"nl-1", "de-1"}, got.Nodes)
	require.Equal(t, "de-1", got.ActiveNode, "the edit itself moves no traffic")
	d, err := te.client.TunnelShow(ctxT(t), id)
	require.NoError(t, err)
	require.Equal(t, "nl-1", d.Nodes[0])
	require.Equal(t, "de-1", d.ActiveNode)
	tn, _ := te.h.Config().Tunnel(id)
	require.Equal(t, []string{"nl-1", "de-1"}, tn.Nodes)
	require.Zero(t, nl.startedCount(systemd.InstanceName(id, "nl-1", trAlpha)))

	// The canary of the new primary passes recover_threshold (2) probes:
	// the failback moves the tunnel to nl-1.
	fb := te.waitEvent(state.EvFailback, "nl-1")
	require.Equal(t, "de-1", fb.FromNode)
	require.Contains(t, fb.Reason, "canary")
	te.waitActive(id, "nl-1", trAlpha)
}
