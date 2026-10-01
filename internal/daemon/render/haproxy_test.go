package render

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend/direct"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// direct/haproxy runs the hub's haproxy: the planner resolves it with
// LookPath; without it the rung is skipped with DEY-B006.
func TestHAProxyUsesTheHubBinary(t *testing.T) {
	e := newEnv(t)
	e.reg["direct"] = direct.New()
	e.cfg.Nodes = e.cfg.Nodes[:1]
	tun := e.cfg.Tunnels[0]
	tun.Nodes = []string{"de-1"}
	tun.Ports = []config.PortMap{{Listen: 443, Proto: config.ProtoTCP, Target: "0.0.0.0:443"}}
	tun.Ladder = config.LadderRef{Inline: []string{"direct/haproxy"}}
	e.cfg.Tunnels[0] = tun

	in := e.input()
	p, err := Plan(in)
	require.NoError(t, err)
	require.Empty(t, p.Candidates)
	require.Equal(t, deyerr.B006, skippedCodes(p)["de-1|direct/haproxy"], "haproxy is not installed")

	in.LookPath = func(name string) (string, error) {
		require.Equal(t, "haproxy", name)
		return "/usr/sbin/haproxy", nil
	}
	p, err = Plan(in)
	require.NoError(t, err)
	require.Equal(t, []string{"de-1|direct/haproxy"}, candidateIDs(p))
	require.Equal(t, "/usr/sbin/haproxy", p.Candidates[0].Hub.Unit.ExecStart[0])
}
