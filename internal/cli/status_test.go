package cli

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tui"
)

func sampleStatus() api.Status {
	udp := true
	return api.Status{
		Schema: 1, Role: "hub", Version: "1.0.0", GeneratedAt: testNow,
		Hub: &api.HubStatus{Name: "ir-1", PublicIP: "5.6.7.8", ControlPort: 44433, UIMode: "simple"},
		Tunnels: []api.TunnelInfo{
			{ID: "main", Name: "Main 443/2053", Enabled: true, State: state.StateUp, ActiveNode: "de-1", ActiveNodeName: "Germany 1",
				ActiveTransport: "backhaul/wssmux", RTTms: 41, UpSince: testNow.Add(-(3*24*time.Hour + 4*time.Hour + 12*time.Minute)),
				Ports: []api.PortMapDTO{{Listen: 443, Proto: "tcp"}, {Listen: 2053, Proto: "tcp"}}, Nodes: []string{"de-1"},
				Warnings: []string{"TLS certificate expires in 10 days"}},
			{ID: "games", Name: "Games UDP", Enabled: true, State: state.StateDegraded, ActiveNode: "nl-1", ActiveNodeName: "Netherlands 1",
				ActiveTransport: "hysteria2/udp", RTTms: 188, UpSince: testNow.Add(-190 * time.Second),
				Ports: []api.PortMapDTO{{Listen: 27015, Proto: "udp"}}},
			{ID: "off", Name: "Off", Enabled: false, State: state.StateDisabled},
			{ID: "p", Name: "Paused", Enabled: true, Paused: true, State: state.StateUp},
			{ID: "s", Name: "Switching", Enabled: true, State: state.StateSwitching},
			{ID: "st", Name: "Starting", Enabled: true, State: state.StateStarting},
			{ID: "d", Name: "Down", Enabled: true, State: state.StateDown},
			{ID: "i", Enabled: true, State: ""},
			{ID: "w", Enabled: true, State: "WEIRD"},
		},
		Nodes: []api.NodeInfo{
			{ID: "de-1", Name: "Germany 1", PublicIP: "1.2.3.4", Online: true, ControlRTTms: 39, Version: "1.0.0", Compatible: true, CPUPercent: 3, RAMBytes: 121 << 20, UDPOK: &udp},
			{ID: "nl-1", Name: "Netherlands 1", PublicIP: "9.8.7.6", Online: false, Version: "0.9.0", Compatible: false},
		},
		Events: []state.Event{
			{At: testNow.Add(-time.Minute), Level: state.LevelInfo, Type: state.EvSwitchTransport, Tunnel: "main", Message: "backhaul/tcpmux → backhaul/wssmux (failback)"},
			{At: testNow.Add(-2 * time.Minute), Level: state.LevelError, Type: state.EvTunnelDown, Tunnel: "main", Reason: "probe failed 3x (timeout)"},
			{At: testNow.Add(-3 * time.Minute), Level: state.LevelWarn, Type: "node_offline", Node: "nl-1", Message: "node offline"},
			{At: testNow.Add(-4 * time.Minute), Type: state.EvTunnelUp},
			{At: testNow.Add(-5 * time.Minute), Type: state.EvTunnelDegraded, Tunnel: "games"},
		},
		Warnings: []api.Warning{{Code: "DEY-B007", Message: "UDP blocked", Tunnel: "games"}, {Message: "old version", Node: "nl-1"}, {Message: "plain"}},
	}
}

func TestStatusHuman(t *testing.T) {
	e := newEnv(t)
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return sampleStatus(), nil }
	out := e.ok("status")
	for _, want := range []string{
		"DEYROUTE Tunnel Manager", "Hub: ir-1 (5.6.7.8)", "Mode: Simple", "2 nodes", "2 tunnels UP",
		"TUNNELS", "NAME", "NODE (active)", "TRANSPORT", "STATE", "RTT", "UP-TIME", "PORTS",
		"Main 443/2053", "de-1 Germany 1", "backhaul/wssmux", "● UP", "41ms", "3d 04:12", "443,2053",
		"◐ DEGR", "00:03:10", "27015/udp", "DISABLED", "PAUSED", "SWITCHING", "STARTING", "○ DOWN", "INIT", "WEIRD",
		"NODES", "● online", "ctl 39ms", "v1.0.0", "cpu 3%", "ram 121MB", "○ offline", "v0.9.0!",
		"LAST EVENTS", "switch", "down", "probe failed 3x", "node offline", "degraded",
		"! games: DEY-B007 UDP blocked", "! nl-1: old version", "! plain", "! main: TLS certificate expires",
	} {
		require.Contains(t, out, want)
	}

	// Narrow terminals drop RTT and UP-TIME; ASCII terminals get no UTF-8.
	e.g.Caps = func() tui.Caps { return tui.Caps{Unicode: false, Width: 80} }
	out = e.ok("status")
	require.NotContains(t, out, "UP-TIME")
	require.NotContains(t, out, "ctl 39ms")
	require.Contains(t, out, "* UP")
	for _, r := range out {
		require.Less(t, r, rune(128), "non-ASCII in %q", out)
	}
}

func TestStatusNodeAndEmpty(t *testing.T) {
	e := newEnv(t)
	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Schema: 1, Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8:44433", Connected: true,
			LastContact: testNow, HubVersion: "v1.0.0", Units: []string{"b", "a"}}}, nil
	}
	out := e.ok("status")
	require.Contains(t, out, "Node: de-1 → hub 5.6.7.8:44433")
	require.Contains(t, out, "hub 5.6.7.8:44433")
	require.Contains(t, out, "connected")
	require.Contains(t, out, "units: a, b")
	require.Contains(t, out, "No events yet.")
	require.NotContains(t, out, "TUNNELS")

	// LAST EVENTS uses the menu's words; a long id is cut, not widened.
	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1"}, Events: []state.Event{
			{At: testNow, Type: state.EvBackendRolledBack, Tunnel: "a-very-long-tunnel-id", Message: "backhaul rolled back"},
			{At: testNow, Type: state.EvNodeOffline, Node: "nl-1", Message: state.EvNodeOffline, Reason: "no heartbeat"},
		}}, nil
	}
	out = e.ok("status")
	require.Contains(t, out, "  a-very-long…   rolled back    backhaul rolled back\n")
	require.Contains(t, out, "  nl-1           node offline   no heartbeat\n")
	require.NotContains(t, out, "backend_update_rolled_back")

	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1"}}, nil
	}
	require.Contains(t, e.ok("status"), "not connected")

	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Role: "hub", Hub: &api.HubStatus{Name: "ir-1", UIMode: "advanced"}}, nil
	}
	out = e.ok("status")
	require.Contains(t, out, "No tunnels yet")
	require.Contains(t, out, "No nodes yet")
	require.Contains(t, out, "Mode: Advanced")
}

func TestStatusJSONAndErrors(t *testing.T) {
	e := newEnv(t)
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return sampleStatus(), nil }
	doc := e.json("status")
	require.Equal(t, "hub", doc["role"])
	require.Len(t, doc["tunnels"], 9)

	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{}, deyerr.New(deyerr.X009, deyerr.Params{"role": "node", "need": "hub"})
	}
	errOut := e.fail(2, "status")
	require.Contains(t, errOut, "✖ DEY-X009")
	e.fail(2, "status", "--json")
	var d map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &d))
	require.EqualValues(t, 2, d["exit_code"])
	require.Equal(t, "DEY-X009", d["error"].(map[string]any)["code"])
	require.Equal(t, deyerr.DefaultLogPath, d["error"].(map[string]any)["log"], "docs/cli-json.md: the log the human block names")

	e.down()
	require.Contains(t, e.fail(2, "status"), "DEY-X003")
}

func TestStatusWatch(t *testing.T) {
	e := newEnv(t)
	e.g.WatchInterval = 5 * time.Millisecond
	e.g.OutTTY = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var n atomic.Int32
	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		switch n.Add(1) {
		case 2:
			return api.Status{}, deyerr.New(deyerr.X042, deyerr.Params{"method": "Status", "service": "deyroute-hub"})
		case 4:
			cancel()
		}
		return sampleStatus(), nil
	}
	code := Run(ctx, e.g, []string{"status", "--watch"})
	require.Equal(t, 0, code, e.errOut.String())
	out := e.out.String()
	require.Contains(t, out, clearScreen)
	require.Contains(t, out, "DEY-X042")
	require.Contains(t, out, "refreshes every 0s")
	require.Contains(t, out, "TUNNELS")

	// --json --watch prints one document per line.
	e.out.Reset()
	n.Store(0)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		switch n.Add(1) {
		case 1:
			return api.Status{}, deyerr.New(deyerr.X042, nil)
		case 3:
			cancel2()
		}
		return sampleStatus(), nil
	}
	e.g.JSON = false
	code = Run(ctx2, e.g, []string{"status", "--watch", "--json"})
	require.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	for _, l := range lines {
		var d map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &d), l)
		require.EqualValues(t, 1, d["schema"])
	}
	require.Contains(t, lines[0], "DEY-X042")
}

// The CLI works over the real unix-socket transport too, and a missing
// socket is DEY-X003 naming the right service.
func TestStatusOverSocket(t *testing.T) {
	e := newEnv(t)
	stub := &apitest.Stub{StatusFn: func(context.Context) (api.Status, error) { return sampleStatus(), nil }}
	client := apitest.Serve(t, stub)
	e.g.Dial = func() (api.Local, error) { return client, nil }
	require.Contains(t, e.ok("status"), "Main 443/2053")

	// Default dialer: no socket, not set up → X003 with the setup fix.
	e2 := newEnv(t)
	e2.g.Dial = nil
	require.Contains(t, e2.fail(2, "status"), "not set up yet")
	// A node names deyroute-node.
	e3 := newEnv(t)
	e3.g.Dial = nil
	e3.writeConfig(nodeConfig)
	errOut := e3.fail(2, "status")
	require.Contains(t, errOut, "systemctl start deyroute-node")
}
