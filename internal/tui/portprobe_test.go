package tui

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
)

// Advanced mode sets the probe kind of a port map (section 9): Ports ->
// Add port to tunnel asks it for the new TCP ports, Ports -> Probe kind *
// changes it for a port the tunnel has. Only the values config.yaml
// accepts can be entered; UDP maps are never asked.
func TestPortsProbeKind(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	var mu sync.Mutex
	tunnels := sampleTunnels()
	tunnels[0].Ports[0].Probe, tunnels[0].Ports[1].Probe = "auto", "tls"
	var added []api.PortSpec
	var edit api.TunnelEditRequest
	var editID string
	stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		out := sampleTunnels()
		out[0].Ports = append([]api.PortMapDTO(nil), tunnels[0].Ports...)
		return out, nil
	}
	stub.PortAddFn = func(_ context.Context, id string, specs []api.PortSpec, _ func(api.Step)) (api.TunnelInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		added = specs
		return tunnels[0], nil
	}
	stub.TunnelEditFn = func(_ context.Context, id string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		editID, edit = id, r
		for _, p := range r.PortProbes {
			for i := range tunnels[0].Ports {
				if tunnels[0].Ports[i].Listen == p.Listen {
					tunnels[0].Ports[i].Probe = p.Probe
				}
			}
		}
		return tunnels[0], nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4")
	h.must(" 5) Probe kind *")

	// Add: one question for the TCP ports of the input.
	h.choose("1").choose("1")
	h.typeLine("8080,27015/udp")
	h.must("Probe kind of the TCP ports (auto, tcp, tls, http) [auto]: _", "http: an HTTP status line")
	h.typeLine("ping")
	h.must("Enter one of: auto, tcp, tls, http")
	h.press("ctrl+u")
	h.typeLine("http")
	h.must("Adding 8080,27015/udp to tunnel main")
	h.press("enter")
	mu.Lock()
	require.Equal(t, []api.PortSpec{{Listen: 8080, Proto: "tcp", Target: "127.0.0.1:8080", Probe: "http"}, {Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"}}, added)
	mu.Unlock()
	h.press("esc", "esc")
	// UDP ports only: no question.
	h.choose("1").choose("1")
	h.typeLine("27016/udp")
	h.must("Adding 27016/udp to tunnel main")
	h.press("esc", "esc")

	// Probe kind *: the TCP ports with their kind, then the four kinds.
	h.choose("5").choose("1")
	h.must("Probe kind: main", "Choose the port whose probe kind to change:", " 1) 443/tcp  probe: auto", " 2) 2053/tcp  probe: tls")
	h.choose("2")
	h.must("Probe kind: main 2053/tcp", "How should the health probe test 2053/tcp of tunnel main (now tls)?",
		" 1) auto  a TLS hello; any answer counts (default)", " 3) tls   a TLS handshake or TLS alert comes back (current)",
		" 4) http  an HTTP status line comes back (HEAD /)")
	h.choose("3")
	h.must("Nothing changed.")
	mu.Lock()
	require.Empty(t, editID)
	mu.Unlock()
	h.choose("2").choose("4")
	h.must("Probe kind of 2053/tcp in tunnel main: http.")
	mu.Lock()
	require.Equal(t, "main", editID)
	require.Equal(t, api.TunnelEditRequest{PortProbes: []api.PortSpec{{Listen: 2053, Proto: "tcp", Probe: "http"}}}, edit)
	mu.Unlock()
	h.press("esc")
	h.must(" 2) 2053/tcp  probe: http")

	// A UDP-only tunnel has nothing to choose.
	h.press("esc")
	h.choose("2")
	h.must("Tunnel games has no TCP ports; UDP port maps are not probed by type.")
}

// Simple mode neither lists Probe kind nor asks it when ports are added.
func TestPortsProbeKindSimple(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	stub.PortAddFn = func(_ context.Context, id string, _ []api.PortSpec, _ func(api.Step)) (api.TunnelInfo, error) {
		return api.TunnelInfo{ID: id}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4")
	h.mustNot("Probe kind")
	h.choose("1").choose("1")
	h.typeLine("8443")
	h.must("Adding 8443 to tunnel main")
	h.mustNot("Probe kind")
}

// Show details names a probe kind other than auto in Advanced mode.
func TestTunnelDetailShowsProbeKind(t *testing.T) {
	ti := sampleTunnels()[0]
	ti.Ports[0].Probe, ti.Ports[1].Probe = "auto", "tls"
	td := tunnelDetail{d: api.TunnelDetail{TunnelInfo: ti, Failover: defaultFailover(), TLSMode: "auto"}}
	a := dashApp(Caps{Unicode: true, Width: 120})
	a.advanced = true
	out := renderDetail(a, td)
	require.Contains(t, out, "443/tcp → 127.0.0.1:443, 2053/tcp → 127.0.0.1:2053 (probe tls)")
	a.advanced = false
	out = renderDetail(a, td)
	require.Contains(t, out, "443/tcp → 127.0.0.1:443, 2053/tcp → 127.0.0.1:2053\n")
}
