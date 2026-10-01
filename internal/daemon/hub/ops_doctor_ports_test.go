package hub

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/doctor"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// TestDoctorChecksEveryPortMap: the doctor bundle has the 4-stage check of
// every port map, not only the first 16 (section 13).
func TestDoctorChecksEveryPortMap(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	var specs []api.PortSpec
	for len(specs) < 20 {
		p := freePort(t)
		dup := false
		for _, s := range specs {
			dup = dup || s.Listen == p
		}
		if !dup {
			specs = append(specs, api.PortSpec{Listen: p})
		}
	}
	te.addTunnelUp(api.TunnelAddRequest{ID: "many", Node: "de-1", Ports: specs, Rungs: []string{trAlpha}, Failover: fastFailover(false)})

	d, err := te.client.DoctorCollect(ctxT(t), "")
	require.NoError(t, err)
	text := d.Sections[doctor.SectionPortChecks]
	require.NotContains(t, text, "not checked")
	require.Equal(t, len(specs), strings.Count(text, "  1 bind: "), text)
	// In config order.
	last := -1
	for _, s := range specs {
		i := strings.Index(text, strconv.Itoa(s.Listen)+"/tcp")
		require.Greater(t, i, last, "port %d", s.Listen)
		last = i
	}
}

// TestPortCheckNodeUnreachableIsP014: when the node cannot connect to the
// port, stage 3 carries DEY-P014 with its Why and Fix (section 13).
func TestPortCheckNodeUnreachableIsP014(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	n.on(api.CmdPortCheckRemote, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.ProbeResultDTO{Error: "dial tcp 5.6.7.8:2053: i/o timeout"}, nil
	})
	port := freePort(t)
	res, err := te.client.PortCheck(ctxT(t), api.PortCheckRequest{Port: port, Node: "de-1"})
	require.NoError(t, err)
	require.NotNil(t, res.NodeReachable)
	require.False(t, *res.NodeReachable)
	require.NotNil(t, res.NodeError)
	e := res.NodeError.Err()
	require.Equal(t, deyerr.P014, e.Code)
	spec := strconv.Itoa(port) + "/tcp"
	require.Equal(t, "Port "+spec+" is not reachable from node de-1", e.Message())
	require.NotEmpty(t, e.Why())
	require.Contains(t, e.Fix(), "deyroute port check "+spec)
	require.Equal(t, LogFile, e.LogPath)
	require.Equal(t, "dial tcp 5.6.7.8:2053: i/o timeout", e.Detail)

	// Reachable: no error.
	n.on(api.CmdPortCheckRemote, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.ProbeResultDTO{OK: true, RTTms: 9}, nil
	})
	res, err = te.client.PortCheck(ctxT(t), api.PortCheckRequest{Port: port, Node: "de-1"})
	require.NoError(t, err)
	require.True(t, *res.NodeReachable)
	require.Nil(t, res.NodeError)
}
