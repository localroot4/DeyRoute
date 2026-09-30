package render

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestDiagPlan(t *testing.T) {
	e := newEnv(t)
	c, loop, err := DiagPlan(e.input(), "de-1", "rev/plain", 9100)
	require.NoError(t, err)
	require.Equal(t, "de-1", c.Node)
	require.Equal(t, "rev/plain", c.TransportID)
	require.Equal(t, "main.canary", c.Hub.Instance)
	require.Equal(t, "main.canary", c.NodeSide.Instance)
	require.Equal(t, "/etc/deyroute/backends/rev/main/canary", c.Hub.ConfigDir)
	ports, err := e.store.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, ports["main/canary/ctl"], c.ControlPort)
	require.Equal(t, ports["main/canary/loopback"], loop)
	require.NotZero(t, loop)
	ri, ok := e.fakes["rev"].lastInput("de-1", "plain")
	require.True(t, ok)
	require.True(t, ri.Canary)
	require.Equal(t, "127.0.0.1", ri.ListenAddr)
	require.Equal(t, []config.PortMap{{Listen: loop, Proto: "tcp", Target: "127.0.0.1:9100", Probe: "tcp"}}, ri.Tunnel.Ports)
	require.Zero(t, ri.Tunnel.ProbePort)
	// The tunnel's real ports are untouched.
	require.Len(t, e.cfg.Tunnels[0].Ports, 2)

	// The same slot as the canary: the same ports again.
	c2, loop2, err := DiagPlan(e.input(), "de-1", "rev/tls", 9100)
	require.NoError(t, err)
	require.Equal(t, c.ControlPort, c2.ControlPort)
	require.Equal(t, loop, loop2)

	// First run is reported for the canary instance.
	in := e.input()
	var asked string
	in.FirstRun = func(inst string) bool { asked = inst; return true }
	_, _, err = DiagPlan(in, "de-1", "rev/tls", 9100)
	require.NoError(t, err)
	require.Equal(t, "main.canary", asked)
	ri, _ = e.fakes["rev"].lastInput("de-1", "tls")
	require.True(t, ri.FirstRun)
}

func TestDiagPlanErrors(t *testing.T) {
	e := newEnv(t)
	code := func(err error) deyerr.Code {
		require.Error(t, err)
		return deyerr.As(err).Code
	}
	_, _, err := DiagPlan(e.input(), "de-1", "rev/tls", 0)
	require.Equal(t, deyerr.P010, code(err))
	_, _, err = DiagPlan(e.input(), "de-1", "rev/tls", 70000)
	require.Equal(t, deyerr.P010, code(err))
	_, _, err = DiagPlan(e.input(), "xx-9", "rev/tls", 9100)
	require.Equal(t, deyerr.C010, code(err))
	_, _, err = DiagPlan(e.input(), "de-1", "ghost/x", 9100)
	require.Equal(t, deyerr.C005, code(err))
	_, _, err = DiagPlan(e.input(), "de-1", "udponly/udp", 9100)
	require.Equal(t, deyerr.B010, code(err))

	in := e.input()
	in.CtlPort = func(string) (int, error) { return 0, deyerr.New(deyerr.P020, nil) }
	_, _, err = DiagPlan(in, "de-1", "rev/tls", 9100)
	require.Equal(t, deyerr.P020, code(err))
	in = e.input()
	calls := 0
	in.CtlPort = func(key string) (int, error) {
		calls++
		if calls > 1 {
			return 0, deyerr.New(deyerr.P020, nil)
		}
		return 30500, nil
	}
	_, _, err = DiagPlan(in, "de-1", "rev/tls", 9100)
	require.Equal(t, deyerr.P020, code(err))
	in = e.input()
	in.Tunnel.Nodes = nil
	_, _, err = DiagPlan(in, "de-1", "rev/tls", 9100)
	require.Equal(t, deyerr.C008, code(err))
	// A node render that fails is reported.
	_, _, err = DiagPlan(e.input(), "de-1", "boom/y", 9100)
	require.Error(t, err)
}
