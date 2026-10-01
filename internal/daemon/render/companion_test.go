package render

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	_ "github.com/localroot4/deyroute/internal/backend/backhaul"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// A tunnel with TCP and UDP maps keeps the TCP-only Backhaul rungs: their
// UDP maps run in a backhaul/udp companion process of the same unit.
func TestPlanBackhaulMixedTunnelUsesAUDPCompanion(t *testing.T) {
	e := newEnv(t)
	tun := &e.cfg.Tunnels[0]
	tun.Nodes = []string{"de-1"}
	tun.Ports = []config.PortMap{
		{Listen: 443, Proto: config.ProtoTCP},
		{Listen: 443, Proto: config.ProtoUDP},
		{Listen: 27015, Proto: config.ProtoUDP},
	}
	tun.Ladder = config.LadderRef{Inline: []string{"backhaul/tcpmux", "backhaul/ws"}}
	in := e.input()
	in.Registry = BackendRegistry{}
	plan, err := Plan(in)
	require.NoError(t, err)
	require.Empty(t, plan.Skipped)
	require.Equal(t, []string{"de-1|backhaul/tcpmux", "de-1|backhaul/ws"}, candidateIDs(plan))

	c, _ := plan.Candidate("de-1", "backhaul/tcpmux")
	udpCtl, err := e.store.AllocCtlPort(CompanionCtlKey("main", "de-1", "backhaul/tcpmux"), config.CtlRangeLow, config.CtlRangeHigh, nil)
	require.NoError(t, err)
	require.NotEqual(t, c.ControlPort, udpCtl)
	require.Contains(t, string(c.Hub.Files["server-udp.toml"]), `bind_addr = "0.0.0.0:`+strconv.Itoa(udpCtl)+`"`)
	require.Contains(t, string(c.NodeSide.Files["client-udp.toml"]), `remote_addr = "5.6.7.8:`+strconv.Itoa(udpCtl)+`"`)
	require.Equal(t, []string{"/usr/local/bin/deyroute", "pair"}, c.Hub.Unit.ExecStart[:2])
	require.Equal(t, []string{"/usr/local/bin/deyroute", "pair"}, c.NodeSide.Unit.ExecStart[:2])
	require.Contains(t, string(c.Hub.DropIn), "server-udp.toml")
	require.Equal(t, "main.de-1.backhaul-tcpmux", c.Hub.Instance)

	// The companion needs UDP between hub and node (section 7.6).
	in.UDPProbe = func(string) (bool, bool) { return false, true }
	plan, err = Plan(in)
	require.NoError(t, err)
	require.Empty(t, plan.Candidates)
	require.Len(t, plan.Skipped, 2)
	require.Equal(t, deyerr.B007, plan.Skipped[0].Code)

	// A TCP-only tunnel renders the plain single process.
	tun.Ports = []config.PortMap{{Listen: 443, Proto: config.ProtoTCP}}
	in = e.input()
	in.Registry = BackendRegistry{}
	plan, err = Plan(in)
	require.NoError(t, err)
	c, ok := plan.Candidate("de-1", "backhaul/tcpmux")
	require.True(t, ok)
	require.NotContains(t, c.Hub.Files, "server-udp.toml")
	require.NotEqual(t, "pair", c.Hub.Unit.ExecStart[1])
}
