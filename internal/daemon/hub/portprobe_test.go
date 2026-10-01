package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// The probe kind of a port map (section 9, Advanced) is set by PortAdd and
// changed by TunnelEdit (`port add|set --probe`, Ports → Probe kind) with
// the config.yaml rule; a change of the kind alone restarts nothing.
func TestPortProbeKind(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	p1, p2, p3 := freePort(t), freePort(t), freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: p1}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	ctx := ctxT(t)
	probeOfPort := func(port int, proto string) string {
		t.Helper()
		tun, ok := te.h.Config().Tunnel(id)
		require.True(t, ok)
		for _, pm := range tun.Ports {
			if pm.Listen == port && pm.Proto == proto {
				return pm.Probe
			}
		}
		t.Fatalf("no port map %d/%s", port, proto)
		return ""
	}
	require.Equal(t, config.ProbeAuto, probeOfPort(p1, config.ProtoTCP))

	// PortAdd: case and spaces do not matter; UDP stays auto.
	out, err := te.client.PortAdd(ctx, id, []api.PortSpec{{Listen: p2, Probe: " TLS "}, {Listen: p3, Proto: "udp", Probe: "auto"}}, nil)
	require.NoError(t, err)
	require.Equal(t, config.ProbeTLS, probeOfPort(p2, config.ProtoTCP))
	require.Equal(t, config.ProbeAuto, probeOfPort(p3, config.ProtoUDP))
	require.Contains(t, out.Ports, api.PortMapDTO{Listen: p2, Proto: config.ProtoTCP, Target: config.DefaultTarget(p2), Probe: config.ProbeTLS})

	// Refused like config.yaml: an unknown kind, a kind on a UDP map.
	p4 := freePort(t)
	_, err = te.client.PortAdd(ctx, id, []api.PortSpec{{Listen: p4, Probe: "icmp"}}, nil)
	e := deyerr.As(err)
	require.Equal(t, deyerr.C013, e.Code)
	require.Contains(t, e.Why(), "auto, tcp, tls, http")
	_, err = te.client.PortAdd(ctx, id, []api.PortSpec{{Listen: p4, Proto: "udp", Probe: "tls"}}, nil)
	e = deyerr.As(err)
	require.Equal(t, deyerr.C013, e.Code)
	require.Contains(t, e.Why(), "UDP port maps are not probed by type")
	require.Len(t, te.h.Config().Tunnels[0].Ports, 3, "nothing was added")

	// TunnelEdit changes existing maps; nothing restarts.
	alpha := hubUnit(id, "de-1", trAlpha)
	restarts := te.sd.count("restart", alpha)
	_, err = te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{PortProbes: []api.PortSpec{
		{Listen: p2, Probe: "http"}, {Listen: p1, Proto: "tcp", Probe: "tcp"},
	}}, nil)
	require.NoError(t, err)
	require.Equal(t, config.ProbeHTTP, probeOfPort(p2, config.ProtoTCP))
	require.Equal(t, config.ProbeTCP, probeOfPort(p1, config.ProtoTCP))
	require.Equal(t, restarts, te.sd.count("restart", alpha))
	d, err := te.client.TunnelShow(ctx, id)
	require.NoError(t, err)
	require.Equal(t, config.ProbeHTTP, d.Ports[1].Probe)
	te.waitActive(id, "de-1", trAlpha)

	// Edit errors: a port the tunnel does not have, a kind config.yaml
	// refuses, an unknown protocol. Nothing changes.
	cases := []struct {
		spec api.PortSpec
		code deyerr.Code
		want string
	}{
		{api.PortSpec{Listen: p4, Probe: "tls"}, deyerr.C013, "a port of the tunnel"},
		{api.PortSpec{Listen: p1, Proto: "udp", Probe: "tls"}, deyerr.C013, "a port of the tunnel"},
		{api.PortSpec{Listen: p3, Proto: "udp", Probe: "http"}, deyerr.C013, "UDP port maps are not probed by type"},
		{api.PortSpec{Listen: p1, Probe: "ping"}, deyerr.C013, "auto, tcp, tls, http"},
		{api.PortSpec{Listen: p1, Proto: "sctp", Probe: "tls"}, deyerr.P010, ""},
	}
	for i, c := range cases {
		_, err := te.client.TunnelEdit(ctx, id, api.TunnelEditRequest{PortProbes: []api.PortSpec{c.spec}}, nil)
		e := deyerr.As(err)
		require.Equal(t, c.code, e.Code, "case %d: %v", i, err)
		if c.want != "" {
			require.Contains(t, e.Why(), c.want, "case %d", i)
		}
	}
	require.Equal(t, config.ProbeTCP, probeOfPort(p1, config.ProtoTCP))
	require.Equal(t, config.ProbeAuto, probeOfPort(p3, config.ProtoUDP))
}
