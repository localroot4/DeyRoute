package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/state"
)

// `port add --probe` and `port set --probe` set the probe kind of port
// maps (section 9, Advanced) with the config.yaml rule: auto, tcp, tls or
// http, and auto for UDP maps.
func TestPortProbeKind(t *testing.T) {
	e := newEnv(t)
	var added []api.PortSpec
	e.stub.PortAddFn = func(_ context.Context, tunnel string, specs []api.PortSpec, _ func(api.Step)) (api.TunnelInfo, error) {
		added = specs
		return api.TunnelInfo{ID: tunnel, State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 40}, nil
	}
	e.ok("port", "add", "main", "8080", "--probe", "HTTP")
	require.Equal(t, []api.PortSpec{{Listen: 8080, Proto: "tcp", Target: "127.0.0.1:8080", Probe: "http"}}, added)
	e.ok("port", "add", "main", "8443/tcp", "--target", "127.0.0.1:9443", "--probe", "tls")
	require.Equal(t, "tls", added[0].Probe)
	e.ok("port", "add", "main", "443,27015/udp", "--probe", "auto")
	require.Equal(t, "auto", added[0].Probe)
	require.Equal(t, "auto", added[1].Probe)
	e.ok("port", "add", "main", "8443")
	require.Empty(t, added[0].Probe, "the daemon picks auto")
	added = nil
	out := e.fail(1, "port", "add", "main", "443,27015/udp", "--probe", "tls")
	require.Contains(t, out, "DEY-C013")
	require.Contains(t, out, "--probe of 27015/udp")
	require.Contains(t, out, "UDP port maps are not probed by type")
	require.Contains(t, out, "run it again with --probe auto, tcp, tls or http")
	require.NotContains(t, out, "config validate")
	out = e.fail(1, "port", "add", "main", "443", "--probe", "icmp")
	require.Contains(t, out, "auto, tcp, tls, http")
	require.Contains(t, e.fail(1, "port", "add", "main", "443", "--probe", ""), "DEY-C013")
	require.Nil(t, added, "nothing reached the daemon")

	var id string
	var req api.TunnelEditRequest
	edits := 0
	e.stub.TunnelEditFn = func(_ context.Context, i string, r api.TunnelEditRequest, progress func(api.Step)) (api.TunnelInfo, error) {
		id, req = i, r
		edits++
		steps(progress, api.Step{Title: "render", Status: api.StepOK})
		return api.TunnelInfo{ID: i, State: state.StateUp}, nil
	}
	out = e.ok("port", "set", "main", "443", "--probe", " TLS ")
	require.Equal(t, "main", id)
	require.Equal(t, api.TunnelEditRequest{PortProbes: []api.PortSpec{{Listen: 443, Proto: "tcp", Probe: "tls"}}}, req)
	require.Contains(t, out, "Probe kind of 443/tcp in tunnel main: tls.")
	require.NotContains(t, out, "restarts", "a probe kind change restarts nothing")
	e.ok("port", "set", "main", "8080-8081", "--probe", "http")
	require.Equal(t, []api.PortSpec{{Listen: 8080, Proto: "tcp", Probe: "http"}, {Listen: 8081, Proto: "tcp", Probe: "http"}}, req.PortProbes)
	e.ok("port", "set", "main", "27015/udp", "--probe", "auto")
	require.Equal(t, []api.PortSpec{{Listen: 27015, Proto: "udp", Probe: "auto"}}, req.PortProbes)
	doc := e.json("port", "set", "main", "443", "--probe", "tcp")
	require.Len(t, doc["steps"], 1)
	require.Equal(t, "main", doc["tunnel"].(map[string]any)["id"])

	edits = 0
	require.Contains(t, e.fail(1, "port", "set", "main", "443"), "--probe")
	require.Contains(t, e.fail(1, "port", "set", "main", "27015/udp", "--probe", "tls"), "UDP port maps are not probed by type")
	require.Contains(t, e.fail(1, "port", "set", "main", "443", "--probe", "ping"), "auto, tcp, tls, http")
	require.Contains(t, e.fail(1, "port", "set", "main", "443:8443", "--probe", "tls"), "DEY-C020")
	require.Contains(t, e.fail(1, "port", "set", "main", "abc", "--probe", "tls"), "DEY-")
	require.Contains(t, e.fail(1, "port", "set", "main"), "needs 2 argument(s)")
	require.Zero(t, edits, "nothing reached the daemon")
}
