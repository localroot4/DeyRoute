package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/sysctl"
)

// TestNodeSysctlOffSkipsHubPush: a node whose join opted out of tuning
// (the root-only marker /var/lib/deyroute/hub-tuning-off) refuses the hub's
// sysctl.apply of balanced, aggressive and auto and its tune.plan with
// DEY-N052 and changes nothing; off (a revert) still passes. Removing the
// marker lets the hub tune the node again. A node without the marker (every
// existing node) follows the hub as before.
func TestNodeSysctlOffSkipsHubPush(t *testing.T) {
	e := newEnv(t)
	tuneProc(t, e)
	marker := e.path(setup.HubTuningOffFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(marker), 0o755))
	require.NoError(t, os.WriteFile(marker, nil, 0o600))
	s, _ := e.startStoppable()
	bbr := true

	before := treeOf(t, e.root)
	for _, p := range []string{config.SysctlBalanced, config.SysctlAggressive, config.SysctlAuto} {
		_, err := call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: p, BBR: &bbr, Reserved: setup.NodeReserved()})
		de := requireCode(t, err, deyerr.N052)
		require.Contains(t, de.Fix(), "rm /var/lib/deyroute/hub-tuning-off")
		require.Contains(t, de.Fix(), "deyroute optimize status")
	}
	_, err := call[api.SysctlResult](t, s, api.CmdTunePlan, api.SysctlArgs{Profile: config.SysctlAuto, BBR: &bbr})
	requireCode(t, err, deyerr.N052)
	require.Equal(t, before, treeOf(t, e.root), "nothing changed on the node")
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlOff})
	require.NoError(t, err, "a revert still passes")

	// Lifted: the hub's profile applies again.
	require.NoError(t, os.Remove(marker))
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlBalanced, BBR: &bbr})
	require.NoError(t, err)
	require.Equal(t, "65535\n", readFileT(t, e.path("proc/sys/net/core/somaxconn")))
}

// TestNodeAppliesSavedReservePorts: the ports of join --reserve-ports
// (/var/lib/deyroute/reserved-ports) and the control range are reserved by
// every profile the hub pushes except off, so a revert followed by apply or
// auto keeps them; the revert removes them with the rest.
func TestNodeAppliesSavedReservePorts(t *testing.T) {
	e := newEnv(t)
	tuneProc(t, e)
	writeFile(t, e.path("proc/sys/net/ipv4/ip_local_reserved_ports"), "5000\n", 0o644)
	writeFile(t, e.path(setup.ReservePortsFile), "8443\n", 0o600)
	s, _ := e.startStoppable()
	bbr := true
	reserved := func() string { return readFileT(t, e.path("proc/sys/net/ipv4/ip_local_reserved_ports")) }

	_, err := call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlBalanced, BBR: &bbr})
	require.NoError(t, err)
	require.Equal(t, "5000,8443,30000-31999\n", reserved())
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlOff})
	require.NoError(t, err)
	require.Equal(t, "5000\n", reserved())
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlAggressive, BBR: &bbr})
	require.NoError(t, err)
	require.Equal(t, "5000,8443,30000-31999\n", reserved())
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: config.SysctlOff})
	require.NoError(t, err)

	args := api.SysctlArgs{Profile: config.SysctlAuto, BBR: &bbr, Reserved: setup.NodeReserved(), PlanVersion: sysctl.PlanVersion}
	plan, err := call[api.SysctlResult](t, s, api.CmdTunePlan, args)
	require.NoError(t, err)
	found := false
	for _, c := range plan.Changes {
		if c.Key == sysctl.KeyReservedPorts {
			found = true
			require.Equal(t, "5000,8443,30000-31999", c.To)
		}
	}
	require.True(t, found, "the plan reserves the saved ports")
	_, err = call[api.SysctlResult](t, s, api.CmdSysctlApply, args)
	require.NoError(t, err)
	require.Equal(t, "5000,8443,30000-31999\n", reserved())
}
