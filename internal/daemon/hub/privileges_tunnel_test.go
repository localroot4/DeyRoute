package hub

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Test transports whose hub side asks for more than the privilege
// allowlist grants (the tfpriv backend has no row, so it gets the template
// alone):
//
//	tfpriv/root    every hub unit runs as root
//	tfpriv/canary  the canary and the diag speed copy (RenderInput.Canary)
//	               add CAP_SYS_ADMIN; its warm units are plain
const (
	trPrivRoot   = "tfpriv/root"
	trPrivCanary = "tfpriv/canary"
)

func init() {
	backend.Register(&privBackend{testBackend{name: "tfpriv", version: "1.0", builtin: true, transports: []backend.Transport{
		{Backend: "tfpriv", Name: "root", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
		{Backend: "tfpriv", Name: "canary", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
	}}})
}

// privBackend is the test backend with the privileges of tfpriv on its hub
// side (a renderer bug the hub must catch before it writes anything).
type privBackend struct{ testBackend }

func (b *privBackend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	r, err := b.testBackend.Render(in, side)
	if side != backend.SideHub {
		return r, err
	}
	switch {
	case in.Transport.Name == "root":
		r.Unit.RunAsRoot = true
	case in.Canary:
		r.Unit.ExtraCaps = []string{"CAP_SYS_ADMIN"}
	}
	return r, err
}

// hubSideAbsent requires that the hub wrote neither the drop-in of inst nor
// the config directory dir, and started no unit of inst.
func (te *tunnelEnv) hubSideAbsent(inst, dir string) {
	te.t.Helper()
	_, err := os.Stat(te.h.o.Systemd.DropInPath(inst))
	require.Truef(te.t, os.IsNotExist(err), "no drop-in is written for %s (%v)", inst, err)
	require.Falsef(te.t, dirExists(te.root, dir), "no config directory %s", dir)
	require.Zero(te.t, te.sd.count("start", systemd.UnitName(inst)), "the unit of %s is never started", inst)
}

// A warm rung whose hub side is outside the allowlist fails at the render
// step (DEY-X034, the rung is skipped): the hub writes nothing for it, the
// node never receives its side, and the tunnel runs on the next rung.
func TestHubRenderRefusesRungOutsideTheAllowlist(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	var log stepLog
	info, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trPrivRoot, trAlpha}, Failover: fastFailover(false)}, log.add)
	require.NoError(t, err, "steps: %v", log.finished())
	id := info.ID
	require.Equal(t, trAlpha, info.ActiveTransport)
	require.Contains(t, log.finished(), stepRender+":warn")

	ts, _ := te.h.tunnelState(id)
	skip := ts.Skipped["de-1/"+trPrivRoot]
	require.Equal(t, string(deyerr.X034), skip.Code, "skips: %v", skipCodes(ts))
	require.Contains(t, skip.Reason, "RunAsRoot")
	require.Contains(t, skip.Reason, "backend "+trPrivRoot+" may not run as root")

	inst := systemd.InstanceName(id, "de-1", trPrivRoot)
	te.hubSideAbsent(inst, render.ConfigDir("tfpriv", id, "de-1", "root"))
	require.NotContains(t, n.renderedFor(id), inst, "the node side is not sent")
	require.Zero(t, n.startedCount(inst))
	// The allowed rung was written and runs.
	_, err = os.Stat(te.h.o.Systemd.DropInPath(systemd.InstanceName(id, "de-1", trAlpha)))
	require.NoError(t, err)
}

// A canary whose hub side is outside the allowlist is never written: the
// canary is unusable (blind failback) and nothing of it reaches systemd on
// either side.
func TestHubCanaryOutsideTheAllowlistIsNotWritten(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trPrivCanary, trAlpha}, Failover: fastFailover(true)})
	id := info.ID
	require.Equal(t, trPrivCanary, info.ActiveTransport, "the warm units of tfpriv/canary are inside the allowlist")

	// Away from rung 1 the hub builds the canary for tfpriv/canary.
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trAlpha}))
	te.waitActive(id, "de-1", trAlpha)
	c := te.h.tun.lookup(id)
	require.NotNil(t, c)
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.can.unusable
	}, testWait, 20*time.Millisecond, "the canary cannot be built")

	inst := systemd.CanaryInstance(id)
	te.hubSideAbsent(inst, render.CanaryConfigDir("tfpriv", id))
	n.tmu.Lock()
	_, rendered := n.rendered[inst]
	n.tmu.Unlock()
	require.False(t, rendered, "the node side of the canary is not sent")
	require.Zero(t, n.startedCount(inst))
}

// diag speed renders a temporary copy of the active transport in the
// canary slot; a copy outside the allowlist fails its render step with
// DEY-X034 before anything is written.
func TestHubDiagSpeedOutsideTheAllowlistIsNotWritten(t *testing.T) {
	te := startTunnelHub(t, func(o *Options, _ string) { o.DiagReadyWait = 500 * time.Millisecond })
	n := te.tunnelNode("de-1")
	speedNode(t, n)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trPrivCanary, Failover: fastFailover(false)})

	var log stepLog
	_, err := te.client.DiagSpeed(ctxT(t), "main", 1, log.add)
	require.Equal(t, deyerr.X034, codeOf(err), "steps: %v, err: %v", log.finished(), err)
	require.Contains(t, deyerr.As(err).Message(), "ExtraCaps")
	require.Contains(t, deyerr.As(err).Why(), "backend "+trPrivCanary+" may not add the capability \"CAP_SYS_ADMIN\"")
	require.Contains(t, log.finished(), stepDiagRender+":failed")

	inst := systemd.CanaryInstance("main")
	te.hubSideAbsent(inst, render.CanaryConfigDir("tfpriv", "main"))
	require.Zero(t, n.startedCount(inst))
	te.waitActive("main", "de-1", trPrivCanary)
}
