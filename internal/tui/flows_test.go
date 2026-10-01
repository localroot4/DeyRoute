package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

func addSteps(progress func(api.Step)) {
	for _, s := range []struct{ id, title string }{
		{"install_hub", "install backend on hub"}, {"install_node", "install on node"},
		{"render", "render"}, {"firewall", "firewall"}, {"start", "start"}, {"probe", "probe"},
	} {
		progress(api.Step{ID: s.id, Title: s.title, Status: api.StepRunning})
		progress(api.Step{ID: s.id, Title: s.title, Status: api.StepOK})
	}
}

// Simple mode, one online node: node announced, ports checked, summary,
// Enter creates, progress screen ends with the spec sentence.
func TestAddTunnelWizardHappyPath(t *testing.T) {
	var got api.TunnelAddRequest
	var mu sync.Mutex
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) {
			return []api.NodeInfo{sampleNodes()[0], {ID: "off-1", Online: false}}, nil
		},
		PortCheckFn: func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			require.Equal(t, "de-1", r.Node)
			return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true}, nil
		},
		TunnelAddFn: func(_ context.Context, req api.TunnelAddRequest, progress func(api.Step)) (api.TunnelInfo, error) {
			mu.Lock()
			got = req
			mu.Unlock()
			addSteps(progress)
			return api.TunnelInfo{ID: "main", State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 41}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1")
	h.must("Add tunnel", "1. Which node?", "Only one node is online: de-1 (Germany 1). It is used for this tunnel.", "2. Ports?", "Ports: _")
	h.typeLine("443,2053")
	h.must("✔ 443/tcp is free", "✔ 2053/tcp is free", "3. Confirm",
		"Node         de-1 (Germany 1)", "Ports        443/tcp, 2053/tcp", "Ladder       default (default)", "Backup       none",
		"Press Enter to create the tunnel.")
	h.mustNot("Policy", "TLS mode") // Simple mode: three questions only
	h.press("enter")
	h.must("install backend on hub ✔", "install on node ✔", "render ✔", "firewall ✔", "start ✔", "probe ✔",
		"Tunnel main is UP via backhaul/wssmux (41ms)")
	mu.Lock()
	require.Equal(t, "de-1", got.Node)
	require.Equal(t, []api.PortSpec{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"}, {Listen: 2053, Proto: "tcp", Target: "127.0.0.1:2053"}}, got.Ports)
	require.Empty(t, got.Rungs)
	require.Nil(t, got.Failover)
	mu.Unlock()
	// Back goes to the tunnels menu, not to the wizard.
	h.press("enter")
	h.must("Add tunnel\n")
	require.Equal(t, 2, h.depth())
}

// Two nodes, a busy port: the process is named, Change port re-checks.
func TestAddTunnelWizardBusyPort(t *testing.T) {
	busy := map[int]api.PortCheckResult{
		443:  {BindFree: false, BindProcess: "nginx (pid 1234)", SuggestedPorts: []int{2053, 2083}},
		8443: {BindFree: false, BindProcess: "rathole (pid 77)", BindByDey: true, Tunnel: "old"},
	}
	var disabled []string
	var mu sync.Mutex
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes(), nil },
		PortCheckFn: func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			mu.Lock()
			defer mu.Unlock()
			if r.Port == 22 {
				return api.PortCheckResult{}, deyerr.New(deyerr.P011, deyerr.Params{"port": "22/tcp", "reason": "SSH"})
			}
			if res, ok := busy[r.Port]; ok {
				return res, nil
			}
			return api.PortCheckResult{BindFree: true}, nil
		},
		TunnelSetEnabledFn: func(_ context.Context, id string, enabled bool) error {
			mu.Lock()
			defer mu.Unlock()
			require.False(t, enabled)
			disabled = append(disabled, id)
			delete(busy, 8443)
			return nil
		},
		TunnelAddFn: func(_ context.Context, req api.TunnelAddRequest, _ func(api.Step)) (api.TunnelInfo, error) {
			require.Equal(t, "nl-1", req.Node)
			require.Len(t, req.Ports, 2)
			require.Equal(t, 2053, req.Ports[0].Listen)
			require.Equal(t, 8443, req.Ports[1].Listen)
			return api.TunnelInfo{ID: "t2", State: state.StateDown, Enabled: true}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1")
	h.must("1. Which node?", " 1) de-1  Germany 1  ● online", " 2) nl-1  Netherlands 1  ● online")
	h.choose("7")
	h.must("Invalid choice: 7")
	h.choose("2")
	h.must("nl-1 (Netherlands 1)", "Ports: _")
	h.typeLine("80-")
	h.must("DEY-C020")
	h.press("ctrl+u")
	h.typeLine("443,22,8443")
	h.must("✖ 443/tcp is used by nginx (pid 1234)", " 1) Change port", " 2) Skip\n")
	h.mustNot("Stop that service")
	// Change port with a suggestion shown.
	h.choose("1")
	h.must("Free suggestions: 2053, 2083", "New port for 443/tcp: _")
	h.typeLine("99999")
	h.must("DEY-P010")
	h.press("ctrl+u")
	h.typeLine("8443")
	h.must("8443/tcp is already in the list.")
	h.press("ctrl+u")
	h.typeLine("2053")
	h.must("✔ 2053/tcp is free", "✖ 22/tcp cannot be used:", "DEY-P011")
	h.choose("2") // skip 22
	h.must("✖ 8443/tcp is used by rathole (pid 77)", " 3) Stop that service (deyroute tunnel old)")
	h.choose("3")
	h.must("Disabling tunnel old stops forwarding all of its ports", "Type yes to continue: ")
	h.typeLine("yes")
	mu.Lock()
	require.Equal(t, []string{"old"}, disabled)
	mu.Unlock()
	h.must("✔ 8443/tcp is free", "3. Confirm")
	// Esc goes back one question, Enter on the ports re-checks.
	h.press("esc")
	h.must("Ports: ")
	h.press("esc")
	h.must(" 1) de-1")
	h.choose("2")
	h.typeLine("2053,8443")
	h.must("3. Confirm")
	h.choose("1")
	h.must("Tunnel t2 was created; it is ○ DOWN now.")
}

// A failed step shows the DEY block (red line, Why, Fix, Log) and Retry.
func TestAddTunnelFailureAndRetry(t *testing.T) {
	var calls int
	var mu sync.Mutex
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn: func(context.Context, api.PortCheckRequest) (api.PortCheckResult, error) {
			return api.PortCheckResult{BindFree: true}, nil
		},
		TunnelAddFn: func(_ context.Context, _ api.TunnelAddRequest, progress func(api.Step)) (api.TunnelInfo, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			progress(api.Step{ID: "install_hub", Title: "install backend on hub", Status: api.StepOK})
			if n == 1 {
				err := deyerr.New(deyerr.P013, deyerr.Params{"port": "443/tcp", "firewall": "ufw", "command": "ufw allow 443/tcp"}).WithLog("/var/log/deyroute/hub.log")
				progress(api.Step{ID: "firewall", Title: "firewall", Status: api.StepFailed, Error: api.ToDTO(err)})
				return api.TunnelInfo{}, err
			}
			return api.TunnelInfo{ID: "main", State: state.StateUp, ActiveTransport: "backhaul/tcpmux", RTTms: 50}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1").typeLine("443").press("enter")
	h.must("install backend on hub ✔", "firewall ✖", "✖ DEY-P013", "Why:  ", "Fix:  ", "Log:  /var/log/deyroute/hub.log (search DEY-P013)", "1) Retry")
	h.mustNot("goroutine", "panic")
	h.choose("1")
	h.must("Tunnel main is UP via backhaul/tcpmux (50ms)")
	mu.Lock()
	require.Equal(t, 2, calls)
	mu.Unlock()
	h.press("r") // no retry after success
	mu.Lock()
	require.Equal(t, 2, calls)
	mu.Unlock()
}

// A step that fails after the tunnel was saved (its port is taken now):
// Retry restarts that tunnel instead of adding it again (DEY-C003).
func TestAddTunnelRetryRestartsTheSavedTunnel(t *testing.T) {
	log := &callLog{}
	saved := false
	var mu sync.Mutex
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn: func(context.Context, api.PortCheckRequest) (api.PortCheckResult, error) {
			return api.PortCheckResult{BindFree: true}, nil
		},
		TunnelAddFn: func(context.Context, api.TunnelAddRequest, func(api.Step)) (api.TunnelInfo, error) {
			log.add("add")
			mu.Lock()
			saved = true
			mu.Unlock()
			return api.TunnelInfo{}, deyerr.New(deyerr.B003, deyerr.Params{"unit": "deyroute-tun@tunnel.de-1.backhaul-wssmux"})
		},
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) {
			mu.Lock()
			defer mu.Unlock()
			if !saved {
				return nil, nil
			}
			return []api.TunnelInfo{{ID: "tunnel", Ports: []api.PortMapDTO{{Listen: 443, Proto: "tcp"}}}}, nil
		},
		TunnelRestartFn: func(_ context.Context, id string) error { log.add("restart " + id); return nil },
		TunnelShowFn: func(_ context.Context, id string) (api.TunnelDetail, error) {
			return api.TunnelDetail{TunnelInfo: api.TunnelInfo{ID: id, State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 40}}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1").typeLine("443").press("enter")
	h.must("✖ DEY-B003", "1) Retry")
	h.choose("1")
	h.must("Tunnel tunnel is UP via backhaul/wssmux (40ms)")
	require.Equal(t, 1, log.count("add"), "not added twice")
	require.True(t, log.has("restart tunnel"))
}

// Stages 2 and 3 of the port check do not block, but are shown.
func TestAddTunnelShowsFirewallAndNodeWarnings(t *testing.T) {
	no := false
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn: func(context.Context, api.PortCheckRequest) (api.PortCheckResult, error) {
			return api.PortCheckResult{BindFree: true, FirewallName: "ufw", FirewallCommand: "ufw allow 443/tcp",
				Node: "de-1", NodeReachable: &no}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 200}, Local: stub})
	h.choose("2").choose("1").typeLine("443")
	h.must("443/tcp is free",
		"the firewall (ufw) blocks it: users cannot reach it until it is opened. Open it with: ufw allow 443/tcp",
		"node de-1 cannot reach it: open it in the provider's firewall panel (DEY-P014).")
}

func TestAddTunnelNoOnlineNode(t *testing.T) {
	stub := &apitest.Stub{NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return nil, nil }}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1")
	h.must("No node is online. Join a node first")
	h.press("esc")
	require.Equal(t, 2, h.depth())
}

// Advanced add tunnel: ladder editor, targets, backup, policy, TLS, thresholds.
func TestAddTunnelAdvanced(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	var got api.TunnelAddRequest
	var mu sync.Mutex
	stub.TunnelAddFn = func(_ context.Context, req api.TunnelAddRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		mu.Lock()
		got = req
		mu.Unlock()
		return api.TunnelInfo{ID: "web", State: state.StateUp, ActiveTransport: "backhaul/tcpmux", RTTms: 12}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1").choose("1").typeLine("443")
	// Ladder editor with the section 8 guidance table.
	h.must("Ladder of default", "speed matters more than hiding", "put tcpmux first", "filtering recognizes the tunnel TLS",
		" 1) backhaul/wssmux", "client IP: masked")
	// Numbers + Enter only (section 6): a rung number opens its actions,
	// the numbers after the rungs add, load a profile or save.
	h.must(" 9) Add a transport", "10) Load a ladder profile", "11) Save this ladder")
	h.choose("2")
	h.must("Rung 2: backhaul/tcpmux", "1) Move up", "2) Move down", "3) Remove from the ladder")
	h.choose("1") // tcpmux first
	h.must(" 1) backhaul/tcpmux", " 2) backhaul/wssmux")
	h.choose("8").choose("3") // remove direct/native
	h.mustNot("direct/native  ")
	h.choose("8") // add (7 rungs left)
	h.must("Add a transport:", "direct/haproxy  client IP: preserved")
	h.choose("1")  // direct/haproxy
	h.choose("11") // save (8 rungs)
	h.must("Advanced options.", "Tunnel name (empty = automatic): _")
	h.typeLine("web")
	h.typeLine("") // target default
	// The probe kind of the TCP port (section 9), checked like config.yaml.
	h.must("Probe kind of 443/tcp (auto, tcp, tls, http) [auto]: _", "tls: a TLS handshake or alert")
	h.typeLine("icmp")
	h.must("Enter one of: auto, tcp, tls, http")
	h.press("ctrl+u")
	h.typeLine("tls")
	h.typeLine("nl-9") // unknown backup
	h.must("nl-9 is not an available node.")
	h.press("ctrl+u")
	h.typeLine("nl-1")
	h.typeLine("bogus")
	h.must("Enter one of: transport_then_node, transport_only, node_only")
	h.press("ctrl+u")
	h.typeLine("node_only")
	h.typeLine("")  // tls auto
	h.typeLine("y") // thresholds
	h.typeLine("")
	h.typeLine("")
	h.typeLine("5")
	for i := 0; i < 5; i++ {
		h.typeLine("")
	}
	h.must("3. Confirm", "Name         web", "Ports        443/tcp (probe tls)", "Backup       nl-1", "Policy       node_only", "Thresholds   custom",
		"backhaul/tcpmux → backhaul/wssmux")
	h.press("enter")
	h.must("Tunnel web is UP via backhaul/tcpmux (12ms)")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "web", got.Name)
	require.Equal(t, []api.PortSpec{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443", Probe: "tls"}}, got.Ports)
	require.Equal(t, []string{"nl-1"}, got.Backups)
	require.Equal(t, "node_only", got.Policy)
	require.Equal(t, "auto", got.TLSMode)
	require.NotNil(t, got.Failover)
	require.Equal(t, 5, got.Failover.FailThreshold)
	require.Equal(t, "backhaul/tcpmux", got.Rungs[0])
	require.Contains(t, got.Rungs, "direct/haproxy")
	require.NotContains(t, got.Rungs, "direct/native")
}

// Destructive actions need "yes" typed exactly and say what is lost.
func TestDeleteTunnelTypedYes(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	stub.TunnelDeleteFn = func(_ context.Context, id string, progress func(api.Step)) error {
		log.add("delete " + id)
		progress(api.Step{ID: "stop", Title: "stop units", Status: api.StepOK})
		return nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2")
	h.must("Main 443/2053", "6) Delete")
	h.choose("6")
	h.must("Choose a tunnel:", " 1) main  Main 443/2053  ● UP  443,2053")
	h.choose("1")
	h.must("Deleting tunnel main permanently removes:", "on node(s) de-1, nl-1", "Ports 443,2053 stop forwarding", "Type yes to continue: ")
	h.typeLine("y")
	h.must("Aborted.")
	require.False(t, log.has("delete main"))
	h.choose("1")
	h.typeLine("YES")
	require.False(t, log.has("delete main"))
	h.choose("1")
	h.typeLine("yes")
	require.True(t, log.has("delete main"))
	h.must("stop units ✔", "Tunnel main deleted.")
	h.press("enter")
	require.Equal(t, 3, h.depth()) // back on the tunnel picker, reloaded
}

func TestTunnelActions(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	stub.TunnelSetEnabledFn = func(_ context.Context, id string, en bool) error {
		if en {
			log.add("enable " + id)
		} else {
			log.add("disable " + id)
		}
		return nil
	}
	stub.TunnelRestartFn = func(_ context.Context, id string) error { log.add("restart " + id); return nil }
	stub.TunnelSwitchFn = func(_ context.Context, id string, r api.SwitchRequest) error {
		log.add("switch " + id + " " + r.Transport + r.Node)
		return nil
	}
	stub.TunnelEditFn = func(_ context.Context, id string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		if r.Name != nil {
			log.add("rename " + id + " " + *r.Name)
		}
		if r.ProbePort != nil {
			log.add("probe " + id)
		}
		return api.TunnelInfo{ID: id}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2")
	// edit
	h.choose("2").choose("1")
	h.must("Name [Main 443/2053]: _")
	h.typeLine("Main")
	for i := 0; i < 3; i++ {
		h.typeLine("")
	}
	h.typeLine("8443")
	h.must("Tunnel main updated.")
	require.True(t, log.has("rename main Main"))
	require.True(t, log.has("probe main"))
	h.press("esc", "esc")
	// edit without change
	h.choose("2").choose("1")
	for i := 0; i < 5; i++ {
		h.typeLine("")
	}
	h.must("Nothing changed.")
	h.press("esc")
	// disable (confirm) and enable
	h.choose("3").choose("1")
	h.must("Disabling tunnel main stops forwarding its ports (443,2053)")
	h.press("enter")
	require.True(t, log.has("disable main"))
	h.press("esc", "esc")
	// restart
	h.choose("4").choose("2")
	h.must("Restarting tunnel games interrupts", " 1) Continue\n 0) Cancel\n\nChoice [1]: _")
	// 0 is Back everywhere: it cancels, it never runs the action.
	h.choose("0")
	require.False(t, log.has("restart games"))
	h.must("Aborted.")
	h.choose("2")
	h.choose("1")
	require.True(t, log.has("restart games"))
	h.press("esc", "esc")
	// switch transport shows client IP and the current rung
	h.choose("5").choose("1")
	h.must("Switch tunnel main to:", "backhaul/wssmux  client IP: masked (current)", "node nl-1")
	h.choose("2")
	require.True(t, log.has("switch main backhaul/tcpmux"))
	h.must("Tunnel main switched to backhaul/tcpmux.")
	h.press("esc", "esc")
	// details (Advanced shows rungs)
	h.choose("7").choose("1")
	h.must("ID           main", "Client IP    masked", "Rungs", "skipped: UDP blocked", "quarantined until", "Recent events")
}

func TestPortCheckFourLines(t *testing.T) {
	no, yes := false, true
	stub := &apitest.Stub{PortCheckFn: func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		require.Equal(t, 443, r.Port)
		require.Equal(t, "tcp", r.Proto)
		return api.PortCheckResult{Port: 443, Proto: "tcp", BindFree: false, BindProcess: "nginx (pid 1234)", SuggestedPorts: []int{2053},
			FirewallOpen: false, FirewallName: "ufw", FirewallCommand: "ufw allow 443/tcp",
			Node: "de-1", NodeReachable: &no, Tunnel: "main", TunnelOK: &yes, TunnelRTTms: 41}, nil
	}}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4").choose("3")
	h.typeLine("443,444")
	h.must("Enter exactly one port.")
	h.press("ctrl+u")
	h.typeLine("443")
	v := h.view()
	start := strings.Index(v, "  local bind:")
	require.GreaterOrEqual(t, start, 0, v)
	body := strings.Split(strings.TrimSpace(v[start:strings.Index(v, " 1) Open it in the firewall")]), "\n")
	require.Len(t, body, 4, v)
	require.Contains(t, body[0], "✖ used by nginx (pid 1234); free ports: 2053")
	require.Contains(t, body[1], "firewall:")
	require.Contains(t, body[1], "✖ closed (ufw); open it: ufw allow 443/tcp")
	require.Contains(t, body[2], "reachable from node de-1:")
	require.Contains(t, body[2], "✖ no")
	require.Contains(t, body[3], "reachable via tunnel main:")
	require.Contains(t, body[3], "✔ yes (41ms)")
	require.Contains(t, body[3], "(filtering inside Iran is not measured)")

	a := dashApp(Caps{Unicode: true})
	out := renderPortCheck(a, api.PortCheckResult{BindFree: true, FirewallOpen: true, FirewallName: "nftables", Note: "custom note"})
	require.Contains(t, out, "✔ free")
	require.Contains(t, out, "✔ open (nftables)")
	require.Contains(t, out, "reachable from a node:")
	require.Contains(t, out, "not tested (no online node)")
	require.Contains(t, out, "not part of a tunnel  (custom note)")
	out = renderPortCheck(a, api.PortCheckResult{BindProcess: "rathole (pid 7)", BindByDey: true, TunnelOK: &no})
	require.Contains(t, out, "used by rathole (pid 7) (deyroute)")
	require.Contains(t, renderPortCheck(a, api.PortCheckResult{}), "used by another program")
}

func TestLogsStreamScrollAndStop(t *testing.T) {
	stopped := make(chan struct{})
	stub := &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) {
			return api.Status{Role: "hub", Hub: &api.HubStatus{UIMode: "simple"}}, nil
		},
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) { return sampleTunnels(), nil },
		LogsFn: func(ctx context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
			require.True(t, q.Follow)
			for i := 1; i <= 30; i++ {
				if err := emit(api.LogLine{Source: "hub", Line: "line " + itoa(i)}); err != nil {
					return err
				}
			}
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub, Height: 20})
	h.choose("6").choose("4")
	h.must(" 1) hub (deyroute-hub service)", " 2) tunnel main")
	h.choose("2")
	h.must("line 30", "live")
	h.mustNot("line 1\n")
	h.press("up", "up")
	h.must("lines 23-28 of 30")
	h.press("pgup", "home")
	h.must("line 1\n")
	h.press("pgdown", "down", "end")
	h.must("line 30", "live")
	h.press("q")
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("q did not stop the log stream")
	}
}

func TestLadderEditorSimpleAndAdvanced(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	var saved []string
	var mu sync.Mutex
	mode := "simple"
	stub.StatusFn = func(context.Context) (api.Status, error) {
		st := sampleStatus()
		mu.Lock()
		st.Hub.UIMode = mode
		mu.Unlock()
		return st, nil
	}
	stub.TunnelEditFn = func(_ context.Context, _ string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		mu.Lock()
		saved = r.Rungs
		mu.Unlock()
		return api.TunnelInfo{}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("5")
	h.mustNot("Thresholds *")
	// Ladder order is not a starred item: Simple mode opens it too.
	h.choose("2")
	h.must("Failover - Ladder order", "main")
	h.press("esc", "esc")
	require.Equal(t, 1, h.depth())
	// Switch to Advanced through Settings (one SettingsSet call).
	stub.SettingsSetFn = func(_ context.Context, r api.SettingsRequest) error {
		log.add("mode " + r.UIMode)
		mu.Lock()
		mode = r.UIMode
		mu.Unlock()
		return nil
	}
	h.choose("12").choose("1")
	h.must("Simple - the essentials only; ladders are automatic (current)")
	h.choose("2")
	h.must("UI mode: Advanced")
	require.True(t, log.has("mode advanced"))
	h.press("esc", "esc")
	h.must("Mode: Advanced", "thresholds *")
	h.choose("5")
	h.must("7) Thresholds *")
	h.choose("2").choose("1")
	h.must("Ladder of main", "UDP is open in the Iran datacenter", "put hysteria2/udp second")
	h.press("s")
	h.must("Nothing changed.")
	h.choose("1")
	h.press("p")
	h.must("Use a ladder profile:", "fast: backhaul/tcpmux → direct/native")
	h.choose("2")
	h.choose("1").choose("3")  // remove rung 1 (numbers only)
	h.choose("1").press("esc") // select rung 1, leave its actions
	h.must("> ")
	h.press("x") // the letter shortcut removes the selected rung
	h.must("The ladder is empty")
	h.choose("3") // save
	h.must("The ladder is empty")
	h.choose("1") // add
	h.choose("1")
	h.press("u", "d") // shortcuts on a single rung change nothing
	h.press("s")
	h.must("Ladder of main saved:")
	mu.Lock()
	require.Len(t, saved, 1)
	mu.Unlock()
}

func TestBackupRestoreAndUninstallLocalOps(t *testing.T) {
	var mu sync.Mutex
	var gotPass string
	var gotPlain bool
	var restored, uninstalled bool
	o := Options{
		Caps: Caps{Unicode: true},
		Backup: func(_ context.Context, out, pass string, plain bool) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			gotPass, gotPlain = pass, plain
			return "/var/lib/deyroute/backups/b1.tar.gz.age", nil
		},
		RestoreCheck: func(_ context.Context, path, _ string) (RestorePlan, error) {
			return RestorePlan{Lost: "Restoring " + path + " replaces this server's /etc/deyroute."}, nil
		},
		Restore: func(_ context.Context, path, pass, ip string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			restored = path == "/tmp/b1" && pass == "s3cret" && ip == ""
			return "Hub ir-1 restored.", nil
		},
		Uninstall: func(_ context.Context, keep, nodes bool) error {
			mu.Lock()
			defer mu.Unlock()
			uninstalled = keep && !nodes
			return nil
		},
	}
	h := newHarness(t, o)
	h.choose("10").choose("1")
	h.typeLine("")
	h.typeLine("")
	h.typeLine("s3cret")
	h.mustNot("s3cret")
	h.must("Passphrase: ******")
	h.typeLine("other")
	h.must("The passphrases do not match.")
	h.press("ctrl+u")
	h.typeLine("s3cret")
	h.must("Backup saved: /var/lib/deyroute/backups/b1.tar.gz.age")
	h.mustNot("s3cret")
	mu.Lock()
	require.Equal(t, "s3cret", gotPass)
	require.False(t, gotPlain)
	mu.Unlock()
	h.press("esc")
	// unencrypted backup warns
	h.choose("1").typeLine("/tmp/x").typeLine("n")
	h.must("This backup is not encrypted")
	h.press("esc")
	// restore
	h.choose("2").typeLine("/tmp/b1").typeLine("s3cret")
	h.must("Restoring /tmp/b1 replaces", "Type yes to continue: ")
	h.mustNot("address changes")
	h.typeLine("yes")
	h.must("Restore complete.", "Hub ir-1 restored.")
	h.press("esc", "esc")
	// uninstall
	h.choose("12").choose("3")
	h.typeLine("")
	h.typeLine("y")
	h.must("Uninstall removes from this server:", "the nftables table inet deyroute", "(backups are kept)", "deyroute on every online node", "Type yes")
	h.press("esc")
	h.choose("3").typeLine("").typeLine("n")
	h.mustNot("deyroute on every online node")
	h.typeLine("yes")
	h.must("deyroute was removed from this server.")
	mu.Lock()
	require.True(t, restored)
	require.True(t, uninstalled)
	mu.Unlock()
}

func TestLocalOpsNotAvailable(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.choose("6").choose("5")
	h.must("Not available here")
	h.press("esc", "esc")
	// Restore stops before its confirmation: the backup cannot be read.
	h.choose("10").choose("2").typeLine("/tmp/b").typeLine("")
	h.must("Not available here")
	h.mustNot("Type yes")
}

func TestDoctor(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Doctor: func(context.Context) (string, string, error) {
		return "DEYROUTE doctor: hub ir-1\nNo problems found", "/tmp/deyroute-doctor.tar.gz", nil
	}})
	h.choose("6").choose("5")
	h.must("No problems found", "Doctor file: /tmp/deyroute-doctor.tar.gz")
}

func TestNodesScreens(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	stub.NodeRenameFn = func(_ context.Context, id, name string) error { log.add("rename " + id + " " + name); return nil }
	stub.NodeRemoveFn = func(_ context.Context, id string) error { log.add("remove " + id); return nil }
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 200}, Local: stub})
	h.choose("3")
	h.must("de-1  Germany 1", "1) Show join command")
	h.choose("1")
	h.must("Run this one line on the new node (as root):",
		"\nbash <(curl -fsSL https://example.invalid/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:ab'\n",
		"Single use; expires at 13:00:00 (in 15m0s)",
		"The command was shown on a plain screen so it can be copied whole")
	h.press("esc")
	h.choose("2")
	h.must("Public IP    1.2.3.4", "Certificate  sha256:aa")
	h.press("esc")
	h.choose("3").choose("1").typeLine("Frankfurt")
	require.True(t, log.has("rename de-1 Frankfurt"))
	h.press("esc", "esc")
	h.choose("4").choose("2")
	h.must("Removing node nl-1 (Netherlands 1):", "tunnels: games", "revokes its certificate")
	h.typeLine("yes")
	require.True(t, log.has("remove nl-1"))
	h.press("esc", "esc")
	h.choose("5").choose("1")
	h.must("Control channel ● online (39ms)", "UDP echo     ✔ yes (40ms)", "arch", "kernel")
}

func TestFailoverScreens(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	stub.TunnelEditFn = func(_ context.Context, id string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		if r.Policy != nil {
			log.add("policy " + *r.Policy)
		}
		if r.Failover != nil {
			log.add("failover " + itoa(r.Failover.FailThreshold))
		}
		return api.TunnelInfo{ID: id}, nil
	}
	stub.TunnelBackupAddFn = func(_ context.Context, id, node string, progress func(api.Step)) error {
		progress(api.Step{ID: "warm", Title: "warm ladder on " + node, Status: api.StepOK})
		log.add("backup " + id + " " + node)
		return nil
	}
	stub.TunnelBackupRemoveFn = func(_ context.Context, id, node string) error { log.add("unbackup " + id + " " + node); return nil }
	stub.TunnelPauseFn = func(_ context.Context, id string) error { log.add("pause " + id); return nil }
	stub.TunnelResetFn = func(_ context.Context, id string) error { log.add("reset " + id); return nil }
	stub.TunnelTestLadderFn = func(_ context.Context, _ string, _ func(api.Step)) ([]api.RungResult, error) {
		return []api.RungResult{{Node: "de-1", Transport: "backhaul/wssmux", OK: true, RTTms: 41},
			{Node: "de-1", Transport: "hysteria2/udp", Skipped: "UDP blocked"},
			{Node: "de-1", Transport: "xray/reality", Error: &api.ErrorDTO{Code: "DEY-B001", Message: "start failed"}}}, nil
	}
	stub.NodeListFn = func(context.Context) ([]api.NodeInfo, error) {
		return append(sampleNodes(), api.NodeInfo{ID: "fi-1", Name: "Finland", Online: true}), nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("5")
	// policy
	h.choose("1").choose("1")
	h.must("transport_then_node - next transport on the same node, then the next node (current)")
	h.choose("3")
	require.True(t, log.has("policy node_only"))
	h.press("esc", "esc")
	// backup nodes: fixed warning, add, progress, ready line
	h.choose("3").choose("1")
	h.must("Backup only works if the same service runs on both nodes.", "Backup nodes of main: nl-1")
	h.choose("1")
	h.must("fi-1  Finland")
	h.mustNot("de-1  Germany")
	h.choose("1")
	h.must("Backup only works if the same service runs on both nodes.", "Node fi-1 becomes a backup of tunnel main")
	h.press("enter")
	h.must("warm ladder on fi-1 ✔", "backup fi-1 ready (warm)")
	require.True(t, log.has("backup main fi-1"))
	h.press("esc")
	h.choose("2").choose("1")
	h.must("Removing backup node nl-1 from tunnel main")
	h.typeLine("yes")
	require.True(t, log.has("unbackup main nl-1"))
	h.press("esc", "esc", "esc")
	// pause, reset, test ladder
	h.choose("4").choose("1")
	h.must("Failover of main is paused")
	require.True(t, log.has("pause main"))
	h.press("esc", "esc")
	h.choose("5").choose("1").press("enter")
	require.True(t, log.has("reset main"))
	h.press("esc", "esc")
	h.choose("6").choose("1")
	h.must("tries every transport for 20 seconds", "about 2 minutes")
	h.typeLine("yes")
	h.must("backhaul/wssmux  ✔ 41ms", "- skipped: UDP blocked", "✖ DEY-B001 start failed")
	h.press("esc", "esc")
	// thresholds (Advanced)
	h.choose("7").choose("1")
	h.must("Failover thresholds of main", "Probe interval (seconds) [5]: _")
	for i := 0; i < 8; i++ {
		h.typeLine("")
	}
	h.must("Nothing changed.")
	h.choose("1")
	h.typeLine("")
	h.typeLine("")
	h.typeLine("4")
	for i := 0; i < 5; i++ {
		h.typeLine("")
	}
	require.True(t, log.has("failover 4"))
}

func TestOptimizeSecurityNotifyUpdate(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	stub.OptimizeApplyFn = func(_ context.Context, p string) (api.OptimizeStatus, error) {
		log.add("apply " + p)
		return api.OptimizeStatus{Profile: p, BBRAvailable: true}, nil
	}
	stub.OptimizeRevertFn = func(context.Context) (api.OptimizeStatus, error) {
		log.add("revert")
		return api.OptimizeStatus{Profile: "off", Warnings: []string{"BBR not loaded"}}, nil
	}
	stub.SecurityRotateTokensFn = func(_ context.Context, id string, _ func(api.Step)) error { log.add("rotate [" + id + "]"); return nil }
	stub.SecurityTLSRenewFn = func(_ context.Context, id string) ([]api.CertInfo, error) {
		log.add("renew " + id)
		return []api.CertInfo{{Kind: "tunnel", Tunnel: id, NotAfter: testNow, DaysLeft: 0}}, nil
	}
	stub.NotifyTelegramSetFn = func(_ context.Context, file, chat string, events []string) error {
		log.add("tg " + file + " " + chat + " " + strings.Join(events, "|"))
		return nil
	}
	stub.NotifyTelegramTestFn = func(context.Context) error { log.add("tgtest"); return nil }
	stub.NotifyTelegramOffFn = func(context.Context) error { log.add("tgoff"); return nil }
	stub.UpdateApplyFn = func(_ context.Context, v string, _ func(api.Step)) (api.UpdateInfo, error) {
		log.add("update " + v)
		return api.UpdateInfo{Current: v}, nil
	}
	stub.UpdateBackendsFn = func(_ context.Context, _ string, _ func(api.Step)) ([]api.BackendUpdate, error) {
		return []api.BackendUpdate{{Backend: "backhaul", From: "0.6.4", To: "0.6.5", Status: "updated"},
			{Backend: "xray", From: "1", To: "1", Status: "unchanged"},
			{Backend: "frp", From: "1", To: "2", Status: "rolled_back", Error: &api.ErrorDTO{Code: "DEY-B009", Message: "probe failed", Why: "w", Fix: "f"}}}, nil
	}
	stub.UpdateRollbackFn = func(context.Context) (api.UpdateInfo, error) { return api.UpdateInfo{Current: "0.9.0"}, nil }
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})

	h.choose("7")
	h.must("Profile: balanced   BBR: active", "4) Limits *")
	h.choose("1").choose("2")
	h.must("The aggressive sysctl profile is written")
	h.press("enter")
	require.True(t, log.has("apply aggressive"))
	h.must("Profile aggressive applied.", "BBR: available, not active")
	h.press("esc")
	h.choose("2").press("enter")
	require.True(t, log.has("revert"))
	h.must("! BBR not loaded", "BBR: not available in this kernel")
	h.press("esc")
	h.choose("3")
	h.must("BBR is switched on by the balanced")
	h.press("esc")
	h.choose("4")
	h.must("net.core.rmem_max               = 16777216")
	h.press("esc", "esc")

	h.choose("8")
	h.choose("1").choose("1")
	h.must("Rotating tokens replaces the secret token of all tunnels")
	h.typeLine("yes")
	require.True(t, log.has("rotate []"))
	h.press("esc")
	h.choose("2")
	h.must("Domain       none (tls mode acme needs one)", "HTTP-01 on port 80")
	h.choose("1")
	h.must("ca  ", "deyroute CA", "(3650 days left)", "renew soon", "sha256:t1")
	h.press("esc", "esc")
	h.choose("3").choose("1")
	require.True(t, log.has("renew main"))
	h.must("(0 days left)")
	h.press("esc", "esc")
	h.choose("4")
	h.choose("1")
	h.must("Managed by deyroute: yes", "Detected firewalls: nftables, ufw", "ufw allow 443/tcp", "table inet deyroute {")
	h.press("esc")
	h.choose("2").press("enter")
	require.True(t, log.has("Firewall apply"))
	h.press("esc")
	h.choose("3")
	h.must("deletes the nftables table inet deyroute")
	h.typeLine("no")
	require.False(t, log.has("Firewall disable"))
	h.choose("3").typeLine("yes")
	require.True(t, log.has("Firewall disable"))
	h.press("esc", "esc")
	h.choose("5")
	h.must("✔ secrets: 0700", "! tokens: old join token")
	h.press("esc")
	h.choose("6")
	h.must("node de-1", "sha256:aa")
	h.press("esc", "esc")

	h.choose("9")
	h.choose("1")
	h.must("the token itself is never typed here")
	h.typeLine("/root/tg.token").typeLine("12345").typeLine("")
	require.True(t, log.has("tg /root/tg.token 12345 down|switch|failback|node_offline"))
	h.press("esc")
	h.choose("2")
	require.True(t, log.has("tgtest"))
	h.press("esc")
	h.choose("3").press("enter")
	require.True(t, log.has("tgoff"))
	h.press("esc", "esc")

	h.choose("11")
	h.choose("1")
	h.must("Installed: 1.0.0", "Latest:    1.0.1", "An update is available", "- fixes")
	h.press("esc")
	h.choose("2")
	h.must("Update deyroute 1.0.0 -> 1.0.1")
	h.press("enter")
	require.True(t, log.has("update 1.0.1"))
	h.must("deyroute updated to 1.0.1.")
	h.press("esc")
	h.choose("3").press("enter")
	h.must("backhaul    0.6.4 → 0.6.5  updated", "unchanged", "rolled_back", "DEY-B009")
	h.press("esc")
	h.choose("4")
	h.must("Source: embedded", "backhaul  0.6.5")
	h.press("esc")
	h.choose("5").press("enter")
	h.must("Rolled back to 0.9.0.")
	h.press("esc", "esc")

	h.choose("12").choose("2")
	h.must("English (current)")
	stub.SettingsSetFn = func(_ context.Context, r api.SettingsRequest) error { log.add("lang " + r.Language); return nil }
	h.choose("1")
	require.True(t, log.has("lang en"))
}

func TestDiagnosticsScreens(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	stub.DiagSpeedFn = func(_ context.Context, _ string, secs int, progress func(api.Step)) (api.SpeedResult, error) {
		progress(api.Step{ID: "dl", Title: "download", Status: api.StepRunning})
		return api.SpeedResult{Transport: "backhaul/wssmux", Seconds: float64(secs), DownloadMbps: 94.2, UploadMbps: 41, RTTms: 41}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("6")
	h.choose("1").typeLine("443").typeLine("")
	h.must("local bind:", "✔ free", "reachable from node de-1:", "✔ yes (44ms)")
	h.press("r")
	h.press("esc")
	h.choose("2").choose("1")
	h.must("443/tcp     tls    ✔ 41ms", "2053/tcp    auto   ✖ timeout")
	h.press("esc", "esc")
	h.choose("3").choose("1").typeLine("5")
	h.must("download 94.2 Mbps · upload 41.0 Mbps · RTT 41ms", "via backhaul/wssmux, 5 seconds")
}

func TestPortsAddRemove(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	stub.PortAddFn = func(_ context.Context, id string, specs []api.PortSpec, _ func(api.Step)) (api.TunnelInfo, error) {
		log.add("add " + id + " " + itoa(specs[0].Listen))
		t := sampleTunnels()[0]
		t.Ports = append(t.Ports, api.PortMapDTO{Listen: specs[0].Listen, Proto: specs[0].Proto})
		return t, nil
	}
	stub.PortRemoveFn = func(_ context.Context, id string, listen int, proto string) (api.TunnelInfo, error) {
		log.add("rm " + id + " " + itoa(listen) + "/" + proto)
		return api.TunnelInfo{ID: id, Ports: []api.PortMapDTO{{Listen: 443, Proto: "tcp"}}}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4")
	h.choose("1").choose("1")
	h.typeLine("abc")
	h.must("DEY-C020")
	h.press("ctrl+u")
	h.typeLine("8443")
	h.must("Adding 8443 to tunnel main re-renders it")
	h.press("enter")
	require.True(t, log.has("add main 8443"))
	h.must("Ports of main: 443,2053,8443")
	h.press("esc", "esc")
	h.choose("2").choose("1")
	h.must(" 1) 443/tcp", " 2) 2053/tcp")
	h.choose("2")
	h.must("Removing 2053/tcp from tunnel main stops forwarding")
	h.typeLine("yes")
	require.True(t, log.has("rm main 2053/tcp"))
	h.press("esc", "esc", "esc")
	h.choose("4")
	h.must("Managed by deyroute: yes")
}

// Leaving a screen cancels its call (no goroutine leak, stale answers dropped).
func TestLeavingCancelsCalls(t *testing.T) {
	started := make(chan struct{})
	stub := &apitest.Stub{UpdateCheckFn: func(ctx context.Context) (api.UpdateInfo, error) {
		close(started)
		<-ctx.Done()
		return api.UpdateInfo{}, ctx.Err()
	}}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("11").choose("1")
	<-started
	h.must("Working…")
	h.press("esc")
	h.must("Check for updates")
	require.Equal(t, 2, h.depth())
}

// Security > TLS certificates sets the domain and (Advanced) the ACME
// e-mail and the Cloudflare token file with SettingsSet; "-" removes a
// setting and the token is only ever a file path (section 10).
func TestTLSSettingsMenu(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	st := sampleStatus()
	st.Hub.UIMode = "advanced"
	st.Hub.Domain, st.Hub.ACMEChallenge, st.Hub.ACMEEmail = "vpn.example.com", api.ACMEDNS01, "owner@example.com"
	stub.StatusFn = func(context.Context) (api.Status, error) { return st, nil }
	stub.SettingsSetFn = func(_ context.Context, r api.SettingsRequest) error {
		switch {
		case r.Domain != nil && *r.Domain == "":
			return deyerr.New(deyerr.C013, deyerr.Params{"field": "hub.domain", "value": "", "allowed": "a domain name while tunnels use tls.mode acme (main)"}).
				WithFix("switch those tunnels to another TLS mode first: deyroute tunnel edit main --tls-mode auto")
		case r.Domain != nil:
			log.add("domain " + *r.Domain)
		case r.ACMEEmail != nil:
			log.add("email " + *r.ACMEEmail)
		case r.CloudflareTokenFile != nil:
			log.add("token " + *r.CloudflareTokenFile)
		}
		return nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: stub})
	h.choose("8").choose("2")
	h.must("Domain       vpn.example.com", "ACME check   DNS-01 through Cloudflare", "ACME e-mail  owner@example.com",
		" 1) Show certificates", " 2) Domain (for ACME)", " 3) ACME e-mail", " 4) Cloudflare token (DNS-01)")

	h.choose("2")
	h.must("DNS-only A record", "Domain [vpn.example.com]: _")
	h.typeLine("5.6.7.8")
	h.must("Enter a domain name")
	h.press("ctrl+u").typeLine("New.Example.com.")
	require.True(t, log.has("domain new.example.com"))
	h.must("Domain set to new.example.com.", "Renew TLS certificate")
	h.press("esc")
	h.choose("2").typeLine("-")
	h.must("DEY-C013", "deyroute tunnel edit main --tls-mode auto")
	h.press("esc")

	h.choose("4")
	h.must("Zone:DNS:Edit", "the token itself is never", "Cloudflare token file: _")
	h.typeLine("cf.token")
	h.must("Enter an absolute path")
	h.press("ctrl+u").typeLine("/root/cf.token")
	require.True(t, log.has("token /root/cf.token"))
	h.must("/etc/deyroute/secrets/cloudflare.token")
	h.press("esc")
	h.choose("4").typeLine("-")
	require.True(t, log.has("token "))
	h.must("HTTP-01 on port 80 again")
	h.press("esc")

	h.choose("3").typeLine("")
	require.True(t, log.has("email owner@example.com"))
	h.press("esc")
	h.choose("3").typeLine("-")
	require.True(t, log.has("email "))
	h.must("ACME e-mail removed.")
	h.press("esc", "esc", "esc")
	require.Equal(t, 1, h.depth())

	// Simple mode: the domain only.
	h = newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: fullStub(log, "simple")})
	h.choose("8").choose("2")
	h.must(" 2) Domain (for ACME)", "HTTP-01 on port 80")
	h.mustNot("ACME e-mail", "Cloudflare token")
}
