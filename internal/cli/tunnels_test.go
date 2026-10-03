package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tui"
)

func TestTunnelAdd(t *testing.T) {
	e := newEnv(t)
	var req api.TunnelAddRequest
	e.stub.TunnelAddFn = func(_ context.Context, r api.TunnelAddRequest, progress func(api.Step)) (api.TunnelInfo, error) {
		req = r
		steps(progress,
			api.Step{ID: "install_hub", Title: "install backend on hub", Status: api.StepOK},
			api.Step{ID: "install_node", Title: "install on node", Status: api.StepOK, Detail: "de-1"},
			api.Step{ID: "udp", Title: "udp probe", Status: api.StepWarn, Error: &api.ErrorDTO{Code: "DEY-P015", Message: "UDP blocked"}},
			api.Step{ID: "x", Title: "optional", Status: api.StepSkipped},
		)
		return api.TunnelInfo{ID: "main", State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 41}, nil
	}
	out := e.ok("tunnel", "add", "--node", "de-1", "--ports", "443,2053,27015/udp", "--name", "Main", "--backup", "nl-1", "--backup", "fr-1,uk-1")
	require.Equal(t, "de-1", req.Node)
	require.Equal(t, "Main", req.Name)
	require.Equal(t, []string{"nl-1", "fr-1", "uk-1"}, req.Backups)
	require.Equal(t, []api.PortSpec{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"}, {Listen: 2053, Proto: "tcp", Target: "127.0.0.1:2053"}, {Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"}}, req.Ports)
	require.Empty(t, req.Ladder)
	require.Nil(t, req.Rungs)
	for _, want := range []string{"  ✔ install backend on hub", "  ✔ install on node  de-1", "  ! udp probe  (DEY-P015 UDP blocked)", "  – optional",
		"Tunnel main is UP via backhaul/wssmux (41ms)", "Backup only works if the same service runs on both nodes."} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "running")

	// Inline ladder and profile ladder.
	e.ok("tunnel", "add", "--node", "de-1", "--ports", "443", "--ladder", "backhaul/wssmux, rathole/noise")
	require.Equal(t, []string{"backhaul/wssmux", "rathole/noise"}, req.Rungs)
	require.Empty(t, req.Ladder)
	doc := e.json("tunnel", "add", "--node", "de-1", "--ports", "443", "--ladder", "stealth")
	require.Equal(t, "stealth", req.Ladder)
	require.Len(t, doc["steps"], 4)
	require.Equal(t, "main", doc["tunnel"].(map[string]any)["id"])

	// ASCII terminals get OK/x marks.
	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 80} }
	require.Contains(t, e.ok("tunnel", "add", "--node", "de-1", "--ports", "443"), "  OK install backend on hub")

	// A TTY shows the summary and asks; "n" aborts before any call.
	e.g.Caps = func() tui.Caps { return tui.Caps{Unicode: true} }
	req = api.TunnelAddRequest{}
	e.tty("n")
	require.Contains(t, e.fail(1, "tunnel", "add", "--node", "de-1", "--ports", "443", "--backup", "nl-1"), "Aborted.")
	require.Contains(t, e.out.String(), "New tunnel")
	require.Empty(t, req.Node)
	e.tty("")
	e.ok("tunnel", "add", "--node", "de-1", "--ports", "443")
	require.Equal(t, "de-1", req.Node)
	e.tty()
	e.ok("tunnel", "add", "--node", "de-2", "--ports", "443", "--yes")
	require.Equal(t, "de-2", req.Node)
	e.g.IsTTY = false

	// Not UP yet: the state is printed.
	e.stub.TunnelAddFn = func(context.Context, api.TunnelAddRequest, func(api.Step)) (api.TunnelInfo, error) {
		return api.TunnelInfo{ID: "main", Enabled: true, State: state.StateStarting}, nil
	}
	require.Contains(t, e.ok("tunnel", "add", "--node", "de-1", "--ports", "443"), "Tunnel main: ◐ STARTING")

	// Usage and port errors.
	require.Contains(t, e.fail(1, "tunnel", "add", "--ports", "443"), "--node")
	require.Contains(t, e.fail(1, "tunnel", "add", "--node", "de-1"), "--ports")
	require.Contains(t, e.fail(1, "tunnel", "add", "--node", "de-1", "--ports", "70000"), "DEY-P010")
	require.Contains(t, e.fail(1, "tunnel", "add", "--node", "de-1", "--ports", "443", "--bogus"), "unknown flag")
	e.stub.TunnelAddFn = func(context.Context, api.TunnelAddRequest, func(api.Step)) (api.TunnelInfo, error) {
		return api.TunnelInfo{}, deyerr.New(deyerr.P012, deyerr.Params{"port": "443/tcp", "process": "nginx (pid 1234)", "addr": "0.0.0.0:443"})
	}
	errOut := e.fail(1, "tunnel", "add", "--node", "de-1", "--ports", "443")
	require.Contains(t, errOut, "✖ DEY-P012")
	require.Contains(t, errOut, "Why:")
	require.Contains(t, errOut, "Fix:")
	require.Contains(t, errOut, "Log:")
	e.down()
	require.Contains(t, e.fail(2, "tunnel", "add", "--node", "de-1", "--ports", "443"), "DEY-X003")
}

func sampleDetail() api.TunnelDetail {
	return api.TunnelDetail{
		TunnelInfo: api.TunnelInfo{ID: "main", Name: "Main", Enabled: true, State: state.StateUp, ActiveNode: "de-1",
			ActiveTransport: "backhaul/wssmux", RTTms: 41, UpSince: testNow.Add(-time.Hour), Nodes: []string{"de-1", "nl-1"},
			Ports: []api.PortMapDTO{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443", Probe: "tls"}}, LadderName: "default",
			Ladder: []string{"backhaul/wssmux", "rathole/noise"}, ClientIP: "masked", Warnings: []string{"certificate expires soon"}},
		Failover: api.FailoverSettings{Policy: "transport_then_node", ProbeIntervalS: 5, ProbeTimeoutS: 3, FailThreshold: 3, RecoverThreshold: 6,
			Failback: true, FailbackAfterS: 300, MaxSwitchesPerHour: 6, QuarantineS: 600},
		TLSMode: "auto", ProbePort: 443, FailbackDelay: 600 * time.Second,
		Rungs: []api.RungStatus{
			{Node: "de-1", Transport: "backhaul/wssmux", Warm: true, Active: true, ControlPort: 30001, UnitState: "active"},
			{Node: "de-1", Transport: "hysteria2/udp", Skipped: "UDP blocked"},
			{Node: "de-1", Transport: "rathole/noise", Warm: true, Quarantine: testNow.Add(10 * time.Minute)},
			{Node: "nl-1", Transport: "backhaul/wssmux", Warm: true},
			{Node: "nl-1", Transport: "frp/wss"},
		},
		Probes: []state.ProbeSample{
			{At: testNow.Add(-10 * time.Second), OK: true, RTT: 40 * time.Millisecond},
			{At: testNow.Add(-5 * time.Second), OK: false, Error: "timeout"},
			{At: testNow, OK: true, RTT: 42 * time.Millisecond},
		},
		Metrics: &state.Metrics{BytesIn: 1536, BytesOut: 3 << 30, ActiveConns: 12},
		Events:  []state.Event{{At: testNow, Type: state.EvTunnelUp, Tunnel: "main", Message: "up"}},
	}
}

func TestTunnelListShow(t *testing.T) {
	e := newEnv(t)
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { return nil, nil }
	require.Contains(t, e.ok("tunnel", "list"), "No tunnels yet")
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { return sampleStatus().Tunnels, nil }
	out := e.ok("tunnel", "list")
	for _, want := range []string{"ID", "main", "Main 443/2053", "● UP", "de-1", "backhaul/wssmux", "41ms", "443,2053"} {
		require.Contains(t, out, want)
	}
	doc := e.json("tunnel", "list")
	require.Len(t, doc["tunnels"], 9)

	var shown string
	e.stub.TunnelShowFn = func(_ context.Context, id string) (api.TunnelDetail, error) { shown = id; return sampleDetail(), nil }
	out = e.ok("tunnel", "show", "main")
	require.Equal(t, "main", shown)
	for _, want := range []string{
		"main (Main)  ● UP", "de-1 (primary), nl-1 (backup)", "de-1 via backhaul/wssmux, 41ms, up 01:00:00",
		"443/tcp → 127.0.0.1:443 (tls)", "default: backhaul/wssmux, rathole/noise", "transport_then_node  (failover paused: no)",
		"port 443 · tunnel TLS auto · client IP masked", "failback after 600s", "quarantine 600s",
		"in 2 KiB · out 3.0 GiB · 12 connections", "! certificate expires soon", "RUNGS", "● active", "30001",
		"! skipped: UDP blocked", "quarantined until", "warm", "not rendered", "PROBES", "3 probes: 2 ok, 1 failed, median RTT 42ms",
		"last failure", "(timeout)", "✔✖✔", "LAST EVENTS",
	} {
		require.Contains(t, out, want)
	}
	doc = e.json("tunnel", "show", "main")
	require.Equal(t, "auto", doc["tls_mode"])
	require.Len(t, doc["rungs"], 5)

	d := sampleDetail()
	d.Probes, d.Rungs, d.Metrics, d.ActiveTransport, d.Failover.Failback = nil, nil, nil, "", false
	e.stub.TunnelShowFn = func(context.Context, string) (api.TunnelDetail, error) { return d, nil }
	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 120} }
	out = e.ok("tunnel", "show", "main")
	require.Contains(t, out, "No probe results yet.")
	require.Contains(t, out, "failback no")
	require.Contains(t, out, "443/tcp -> 127.0.0.1:443")
	require.Contains(t, e.fail(1, "tunnel", "show"), "needs 1 argument")
}

func TestTunnelEdit(t *testing.T) {
	e := newEnv(t)
	var req api.TunnelEditRequest
	var id string
	e.stub.TunnelEditFn = func(_ context.Context, i string, r api.TunnelEditRequest, progress func(api.Step)) (api.TunnelInfo, error) {
		id, req = i, r
		steps(progress, api.Step{Title: "render", Status: api.StepOK})
		return api.TunnelInfo{ID: i, State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 40}, nil
	}
	out := e.ok("tunnel", "edit", "main", "--name", "Main 443", "--policy", "node_only", "--probe-port", "2053", "--ladder", "stealth")
	require.Equal(t, "main", id)
	require.Equal(t, "Main 443", *req.Name)
	require.Equal(t, "node_only", *req.Policy)
	require.Equal(t, 2053, *req.ProbePort)
	require.Equal(t, "stealth", *req.Ladder)
	require.Contains(t, out, "Tunnel main updated.")
	require.Contains(t, out, "✔ render")

	e.ok("tunnel", "edit", "main", "--ladder", "xray/reality,backhaul/wssmux")
	require.Nil(t, req.Name)
	require.Nil(t, req.Ladder)
	require.Nil(t, req.Policy)
	require.Nil(t, req.ProbePort)
	require.Equal(t, []string{"xray/reality", "backhaul/wssmux"}, req.Rungs)

	doc := e.json("tunnel", "edit", "main", "--probe-port", "0")
	require.Equal(t, 0, *req.ProbePort)
	require.Len(t, doc["steps"], 1)
	require.Contains(t, e.fail(1, "tunnel", "edit", "main"), "nothing to change")
	require.Contains(t, e.fail(1, "tunnel", "edit", "main", "--name", ""), "printable")

	// TLS mode (section 10): acme, or custom with files resolved where
	// they were typed (the daemon has another working directory).
	e.ok("tunnel", "edit", "main", "--tls-mode", "ACME")
	require.Equal(t, "acme", *req.TLSMode)
	require.Nil(t, req.TLSCert)
	e.ok("tunnel", "edit", "main", "--tls-mode", "custom", "--tls-cert", "fullchain.pem", "--tls-key", "/etc/ssl/key.pem")
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, "custom", *req.TLSMode)
	require.Equal(t, filepath.Join(wd, "fullchain.pem"), *req.TLSCert)
	require.Equal(t, "/etc/ssl/key.pem", *req.TLSKey)
	require.Contains(t, e.fail(1, "tunnel", "edit", "main", "--tls-mode", "letsencrypt"), "DEY-C013")
	require.Contains(t, e.fail(1, "tunnel", "edit", "main", "--tls-cert", ""), "DEY-C013")
}

func TestTunnelAddTLSMode(t *testing.T) {
	e := newEnv(t)
	var req api.TunnelAddRequest
	e.stub.TunnelAddFn = func(_ context.Context, r api.TunnelAddRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		req = r
		return api.TunnelInfo{ID: "main", State: state.StateUp, ActiveTransport: "backhaul/wssmux"}, nil
	}
	e.ok("tunnel", "add", "--node", "de-1", "--ports", "443")
	require.Empty(t, req.TLSMode)
	e.ok("tunnel", "add", "--node", "de-1", "--ports", "443", "--tls-mode", "acme")
	require.Equal(t, "acme", req.TLSMode)
	// custom needs certificate files: it is set with tunnel edit.
	out := e.fail(1, "tunnel", "add", "--node", "de-1", "--ports", "443", "--tls-mode", "custom")
	require.Contains(t, out, "DEY-C013")
	require.Contains(t, out, "auto, acme")
}

func TestTunnelLifecycle(t *testing.T) {
	e := newEnv(t)
	calls := map[string]string{}
	e.stub.TunnelSetEnabledFn = func(_ context.Context, id string, en bool) error {
		if en {
			calls["enable"] = id
		} else {
			calls["disable"] = id
		}
		return nil
	}
	e.stub.TunnelRestartFn = func(_ context.Context, id string) error { calls["restart"] = id; return nil }
	e.stub.TunnelDeleteFn = func(_ context.Context, id string, progress func(api.Step)) error {
		calls["delete"] = id
		steps(progress, api.Step{Title: "stop units", Status: api.StepOK})
		return nil
	}
	e.stub.TunnelResetFn = func(_ context.Context, id string) error { calls["reset"] = id; return nil }
	e.stub.TunnelPauseFn = func(_ context.Context, id string) error { calls["pause"] = id; return nil }
	e.stub.TunnelResumeFn = func(_ context.Context, id string) error { calls["resume"] = id; return nil }
	var sw api.SwitchRequest
	e.stub.TunnelSwitchFn = func(_ context.Context, id string, r api.SwitchRequest) error { calls["switch"], sw = id, r; return nil }

	require.Contains(t, e.ok("tunnel", "enable", "a"), "Tunnel a enabled.")
	require.Contains(t, e.fail(3, "tunnel", "disable", "b"), "stops forwarding its ports")
	require.Empty(t, calls["disable"])
	require.Contains(t, e.ok("tunnel", "disable", "b", "--yes"), "Tunnel b disabled.")
	out := e.ok("tunnel", "restart", "c")
	require.Contains(t, out, "interrupts its connections")
	require.Contains(t, out, "Tunnel c restarted.")
	require.Contains(t, e.fail(3, "tunnel", "delete", "d"), "permanently removes")
	require.Empty(t, calls["delete"])
	e.tty("yes")
	out = e.ok("tunnel", "delete", "d")
	require.Contains(t, out, "✔ stop units")
	require.Contains(t, out, "Tunnel d deleted.")
	e.g.IsTTY = false
	doc := e.json("tunnel", "delete", "d2", "--yes")
	require.Equal(t, "d2", doc["tunnel"])
	require.Len(t, doc["steps"], 1)
	require.Contains(t, e.ok("tunnel", "reset", "e"), "back to rung 1")
	require.Contains(t, e.ok("tunnel", "pause", "f"), "paused")
	require.Contains(t, e.ok("tunnel", "resume", "g"), "resumed")
	require.Contains(t, e.ok("tunnel", "switch", "h", "--transport", "backhaul/tcpmux"), "switching to backhaul/tcpmux")
	require.Equal(t, api.SwitchRequest{Transport: "backhaul/tcpmux"}, sw)
	doc = e.json("tunnel", "switch", "h", "--node", "nl-1")
	require.Equal(t, api.SwitchRequest{Node: "nl-1"}, sw)
	require.Equal(t, "nl-1", doc["node"])
	require.Contains(t, e.fail(1, "tunnel", "switch", "h"), "exactly one")
	require.Contains(t, e.fail(1, "tunnel", "switch", "h", "--node", "a", "--transport", "b"), "exactly one")
	require.Equal(t, map[string]string{"enable": "a", "disable": "b", "restart": "c", "delete": "d2", "reset": "e", "pause": "f", "resume": "g", "switch": "h"}, calls)
	for _, v := range []string{"reset", "pause", "resume", "enable", "restart"} {
		doc = e.json("tunnel", v, "z")
		require.Equal(t, true, doc["ok"])
	}
	e.stub.TunnelPauseFn = func(context.Context, string) error { return deyerr.New(deyerr.F007, deyerr.Params{"tunnel": "z"}) }
	require.Contains(t, e.fail(1, "tunnel", "pause", "z"), "DEY-F007")
}

func TestTunnelTestLadderAndBackup(t *testing.T) {
	e := newEnv(t)
	called := false
	e.stub.TunnelTestLadderFn = func(_ context.Context, id string, progress func(api.Step)) ([]api.RungResult, error) {
		called = true
		steps(progress, api.Step{Title: "backhaul/wssmux", Status: api.StepOK})
		return []api.RungResult{
			{Node: "de-1", Transport: "backhaul/wssmux", OK: true, RTTms: 41},
			{Node: "de-1", Transport: "rathole/noise", Error: &api.ErrorDTO{Code: "DEY-B004", Message: "probe timeout"}},
			{Node: "de-1", Transport: "hysteria2/udp", Skipped: "UDP blocked"},
		}, nil
	}
	errOut := e.fail(3, "tunnel", "test-ladder", "main")
	require.Contains(t, errOut, "20 seconds")
	require.False(t, called)
	out := e.ok("tunnel", "test-ladder", "main", "--yes")
	for _, want := range []string{"RESULT", "✔ ok", "41ms", "✖ failed", "DEY-B004 probe timeout", "– skipped", "UDP blocked"} {
		require.Contains(t, out, want)
	}
	doc := e.json("tunnel", "test-ladder", "main", "--yes")
	require.Len(t, doc["results"], 3)

	var added, removed [2]string
	e.stub.TunnelBackupAddFn = func(_ context.Context, id, node string, progress func(api.Step)) error {
		added = [2]string{id, node}
		steps(progress, api.Step{Title: "warm", Status: api.StepOK})
		return nil
	}
	e.stub.TunnelBackupRemoveFn = func(_ context.Context, id, node string) error { removed = [2]string{id, node}; return nil }
	out = e.ok("tunnel", "backup", "add", "main", "--node", "nl-1")
	require.Equal(t, [2]string{"main", "nl-1"}, added)
	require.Contains(t, out, "backup nl-1 ready (warm)")
	require.Contains(t, out, "Backup only works")
	require.Contains(t, e.fail(3, "tunnel", "backup", "remove", "main", "--node", "nl-1"), "deletes the warm units")
	require.Empty(t, removed[0], "no confirmation, nothing removed")
	out = e.ok("tunnel", "backup", "remove", "main", "--node", "nl-1", "--yes")
	require.Equal(t, [2]string{"main", "nl-1"}, removed)
	require.Contains(t, out, "Backup node nl-1 removed from tunnel main.")
	doc = e.json("tunnel", "backup", "add", "main", "--node", "nl-1")
	require.Len(t, doc["steps"], 1)
	require.Contains(t, e.fail(1, "tunnel", "backup", "add", "main"), "--node")
	added = [2]string{}
	require.Contains(t, e.fail(1, "tunnel", "backup", "add", "main", "--node", "nl-1,de-2"), "one backup node per command")
	require.Contains(t, e.fail(1, "tunnel", "backup", "add", "main", "--node", "nl-1", "--node", "de-2"), "one backup node per command")
	require.Empty(t, added[0])
	require.Contains(t, e.ok("tunnel", "backup"), "add")
}

func TestPortCommands(t *testing.T) {
	e := newEnv(t)
	var added []api.PortSpec
	e.stub.PortAddFn = func(_ context.Context, tunnel string, specs []api.PortSpec, progress func(api.Step)) (api.TunnelInfo, error) {
		added = specs
		steps(progress, api.Step{Title: "check 8443/tcp", Status: api.StepOK})
		return api.TunnelInfo{ID: tunnel, State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 40}, nil
	}
	out := e.ok("port", "add", "main", "8443")
	require.Equal(t, []api.PortSpec{{Listen: 8443, Proto: "tcp", Target: "127.0.0.1:8443"}}, added)
	require.Contains(t, out, "restarts to apply the change")
	require.Contains(t, out, "Port 8443/tcp added to tunnel main (target 127.0.0.1:8443).")
	e.ok("port", "add", "main", "8443/tcp", "--target", "127.0.0.1:9443")
	require.Equal(t, "127.0.0.1:9443", added[0].Target)
	e.ok("port", "add", "main", "27015/udp")
	require.Equal(t, "udp", added[0].Proto)
	doc := e.json("port", "add", "main", "2000-2002")
	require.Len(t, added, 3)
	require.Len(t, doc["steps"], 1)
	require.Contains(t, e.fail(1, "port", "add", "main", "443,2053", "--target", "127.0.0.1:1"), "exactly one port")
	require.Contains(t, e.fail(1, "port", "add", "main", "443", "--target", "bad"), "DEY-C004")
	require.Contains(t, e.fail(1, "port", "add", "main", "abc"), "DEY-")

	var rm [2]any
	e.stub.PortRemoveFn = func(_ context.Context, tunnel string, listen int, proto string) (api.TunnelInfo, error) {
		rm = [2]any{listen, proto}
		return api.TunnelInfo{ID: tunnel}, nil
	}
	require.Contains(t, e.fail(3, "port", "remove", "main", "8443/udp"), "stops forwarding that port for good")
	require.Nil(t, rm[0], "no confirmation, nothing removed")
	require.Contains(t, e.ok("port", "remove", "main", "8443/udp", "--yes"), "Port 8443/udp removed from tunnel main.")
	require.Equal(t, [2]any{8443, "udp"}, rm)
	e.json("port", "remove", "main", "8443", "--yes")
	require.Equal(t, [2]any{8443, "tcp"}, rm)
	require.Contains(t, e.fail(1, "port", "remove", "main", "443,2053"), "DEY-C020")
	require.Contains(t, e.fail(1, "port", "remove", "main", "443:8443"), "DEY-C020")

	yes, no := true, false
	var creq api.PortCheckRequest
	e.stub.PortCheckFn = func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		creq = r
		if r.Port == 443 {
			return api.PortCheckResult{Port: 443, Proto: "tcp", BindFree: false, BindProcess: "nginx (pid 1234)", BindAddr: "0.0.0.0:443",
				FirewallOpen: false, FirewallName: "ufw", FirewallCommand: "ufw allow 443/tcp", Node: "de-1", NodeReachable: &no,
				Tunnel: "main", TunnelOK: &no, SuggestedPorts: []int{2053, 2083}}, nil
		}
		return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, BindByDey: true, FirewallOpen: true, FirewallName: "nftables",
			Node: "de-1", NodeReachable: &yes, NodeRTTms: 39, Tunnel: "main", TunnelOK: &yes, TunnelRTTms: 41, Note: "custom note"}, nil
	}
	out = e.ok("port", "check", "443")
	for _, want := range []string{"Port 443/tcp", "1. local bind", "✖ used by nginx (pid 1234) on 0.0.0.0:443", "2. firewall", "closed (ufw); open it with: ufw allow 443/tcp",
		"3. reachable from node", "de-1: no", "4. reachable via tunnel", "main: no", "filtering inside Iran", "Free ports: 2053, 2083"} {
		require.Contains(t, out, want)
	}
	out = e.ok("port", "check", "2053/udp", "--node", "nl-1")
	require.Equal(t, api.PortCheckRequest{Port: 2053, Proto: "udp", Node: "nl-1"}, creq)
	for _, want := range []string{"✔ free", "open (nftables)", "de-1: yes (39ms)", "main: yes (41ms)", "custom note"} {
		require.Contains(t, out, want)
	}
	e.stub.PortCheckFn = func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindByDey: true}, nil
	}
	out = e.ok("port", "check", "80")
	require.Contains(t, out, "not tested (no online node)")
	require.Contains(t, out, "not tested (the port is in no tunnel)")
	require.Contains(t, out, "(a deyroute unit)")
	doc = e.json("port", "check", "80")
	require.EqualValues(t, 80, doc["port"])

	var count int
	e.stub.PortSuggestFn = func(_ context.Context, n int) ([]int, error) { count = n; return []int{443, 2053, 2083}, nil }
	require.Contains(t, e.ok("port", "suggest"), "Free ports: 443, 2053, 2083")
	require.Equal(t, DefaultSuggestCount, count)
	doc = e.json("port", "suggest", "--count", "5")
	require.Equal(t, 5, count)
	require.Len(t, doc["ports"], 3)
	require.Contains(t, e.fail(1, "port", "suggest", "--count", "0"), "--count")
}

func TestLadderCommands(t *testing.T) {
	e := newEnv(t)
	e.stub.LadderListFn = func(context.Context) ([]api.Ladder, error) {
		return []api.Ladder{
			{Name: "default", Builtin: true, Rungs: []string{"backhaul/wssmux", "rathole/noise"}, UsedBy: []string{"main"}},
			{Name: "stealth", Rungs: []string{"xray/reality"}},
		}, nil
	}
	out := e.ok("ladder", "list")
	for _, want := range []string{"NAME", "KIND", "USED BY", "RUNGS", "default", "built-in", "main", "backhaul/wssmux, rathole/noise", "stealth", "custom"} {
		require.Contains(t, out, want)
	}
	doc := e.json("ladder", "list")
	require.Len(t, doc["ladders"], 2)
	out = e.ok("ladder", "show", "default")
	require.Contains(t, out, "Ladder default (built-in), used by: main")
	require.Contains(t, out, "   1) backhaul/wssmux")
	require.Contains(t, out, "   2) rathole/noise")
	require.Contains(t, e.ok("ladder", "show", "stealth"), "(custom)")
	doc = e.json("ladder", "show", "stealth")
	require.Equal(t, "stealth", doc["name"])
	require.Contains(t, e.fail(1, "ladder", "show", "nope"), "DEY-C012")

	var saved struct {
		name   string
		rungs  []string
		create bool
	}
	e.stub.LadderSaveFn = func(_ context.Context, name string, rungs []string, create bool) error {
		saved.name, saved.rungs, saved.create = name, rungs, create
		return nil
	}
	require.Contains(t, e.ok("ladder", "create", "fast", "--rungs", "backhaul/tcpmux,rathole/noise"), "Ladder fast created: backhaul/tcpmux, rathole/noise")
	require.True(t, saved.create)
	require.Equal(t, []string{"backhaul/tcpmux", "rathole/noise"}, saved.rungs)
	require.Contains(t, e.ok("ladder", "set", "fast", "--rungs", "a/b", "--rungs", "c/d"), "Ladder fast saved")
	require.False(t, saved.create)
	require.Equal(t, []string{"a/b", "c/d"}, saved.rungs)
	require.Contains(t, e.fail(1, "ladder", "create", "x"), "--rungs")
	deleted := ""
	e.stub.LadderDeleteFn = func(_ context.Context, name string) error { deleted = name; return nil }
	require.Contains(t, e.ok("ladder", "delete", "fast"), "Ladder fast deleted.")
	require.Equal(t, "fast", deleted)
	doc = e.json("ladder", "delete", "fast")
	require.Equal(t, "fast", doc["ladder"])
}
