package hub

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/systemd"
)

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

	n.stop()
	// The list to keep is the last one the hub got: a beat may still land
	// between the tunnel coming up and the stop.
	require.Eventually(t, func() bool { return !te.h.Online("de-1") }, testWait, 10*time.Millisecond)
	before, _ := te.h.nodeState("de-1")
	require.NotEmpty(t, before.Units)
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

// The last error a node agent reports in its heartbeat reaches the owner
// (node list, status), redacted; a later beat without one clears it.
func TestNodeLastErrorReachesTheOwner(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	n.lastError.Store("DEY-B003 Backend failed to start: token=supersecret1234")
	lastError := func() string {
		ns, err := env.client.NodeList(ctxT(t))
		require.NoError(t, err)
		require.Len(t, ns, 1)
		return ns[0].LastError
	}
	require.Eventually(t, func() bool { return strings.HasPrefix(lastError(), "DEY-B003 Backend failed to start") }, testWait, 10*time.Millisecond)
	require.NotContains(t, lastError(), "supersecret1234")
	st, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	require.Len(t, st.Nodes, 1)
	require.Equal(t, lastError(), st.Nodes[0].LastError)

	n.lastError.Store("")
	require.Eventually(t, func() bool { return lastError() == "" }, testWait, 10*time.Millisecond)
}
