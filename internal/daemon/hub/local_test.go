package hub

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

func TestNodeJoinCommandFormat(t *testing.T) {
	env := startHub(t, nil)
	before := time.Now()
	jc, err := env.client.NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(jc.Command, "bash <(curl -fsSL https://"), jc.Command)
	require.Contains(t, jc.Command, "/install.sh) join '"+jc.Link+"'")
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(jc.Link, "dey://"))
	require.Equal(t, "127.0.0.1", link.Host)
	require.Equal(t, 44433, link.Port)
	require.Equal(t, env.ca.Fingerprint(), link.Fingerprint)
	require.Contains(t, jc.Link, "#sha256:")
	require.WithinDuration(t, before.Add(api.JoinTokenTTL), jc.ExpiresAt, 5*time.Second)
	// dev builds do not pin a version.
	require.NotContains(t, jc.Command, "--version")

	jc2, err := env.client.NodeJoinCommand(ctxT(t), 2*time.Hour)
	require.NoError(t, err)
	require.NotEqual(t, jc.Link, jc2.Link)
	require.WithinDuration(t, before.Add(2*time.Hour), jc2.ExpiresAt, 5*time.Second)

	for _, bad := range []time.Duration{30 * time.Second, 48 * time.Hour} {
		_, err = env.client.NodeJoinCommand(ctxT(t), bad)
		require.Equal(t, deyerr.C013, codeOf(err))
	}
	// The token is registered as a secret: never logged.
	require.Equal(t, "***", dlog.Redact(link.Token))
}

func TestNodeJoinCommandMirrorAndRelease(t *testing.T) {
	old := version.Version
	version.Version = "1.4.2"
	defer func() { version.Version = old }()
	env := startHub(t, func(c *config.Config) { c.Hub.Mirror = "https://cdn.example" })
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	require.Contains(t, jc.Command, "bash <(curl -fsSL https://cdn.example/latest/install.sh) join '")
	require.True(t, strings.HasSuffix(jc.Command, " --version 1.4.2"), jc.Command)
}

func TestStatusEventsOverSocket(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1").start()
	env.joinNode("nl-1")
	env.waitOnline("de-1", true)
	env.addTunnel("main", []string{"de-1", "nl-1"}, 443)
	env.addTunnel("games", []string{"nl-1"}, 2053)
	_, err := env.h.mutate(func(c *config.Config) error {
		tn, _ := c.Tunnel("games")
		tn.Enabled = false
		return nil
	})
	require.NoError(t, err)
	up := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, env.h.st.PutTunnel(state.TunnelState{
		ID: "main", State: state.StateUp, Active: state.Candidate{Node: "de-1", Transport: "backhaul/wssmux"},
		LastRTTms: 41, UpSince: up,
		Skipped: map[string]state.Skip{"nl-1/hysteria2/udp": {Reason: "UDP is closed", Code: string(deyerr.B007)}},
	}))
	env.h.Emit(state.Event{Type: state.EvSwitchTransport, Tunnel: "main", FromTransport: "backhaul/tcpmux",
		ToTransport: "backhaul/wssmux", Message: "switched"})

	st, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	require.Equal(t, api.JSONSchemaVersion, st.Schema)
	require.Equal(t, config.RoleHub, st.Role)
	require.Equal(t, "ir-1", st.Hub.Name)
	require.Equal(t, 44433, st.Hub.ControlPort)
	require.Equal(t, "simple", st.Hub.UIMode)
	require.Equal(t, "en", st.Hub.Language)
	require.Equal(t, FirewallModeManaged, st.Hub.Firewall)
	require.Len(t, st.Nodes, 2)
	require.Equal(t, []string{"main"}, st.Nodes[0].Tunnels)
	require.Len(t, st.Tunnels, 2)
	main := st.Tunnels[0]
	require.Equal(t, "UP", main.State)
	require.Equal(t, "de-1", main.ActiveNode)
	require.Equal(t, "de-1", main.ActiveNodeName)
	require.Equal(t, "backhaul/wssmux", main.ActiveTransport)
	require.Equal(t, 41, main.RTTms)
	require.Equal(t, up, main.UpSince.UTC())
	require.Equal(t, ClientIPMasked, main.ClientIP)
	require.Equal(t, "default", main.LadderName)
	require.NotEmpty(t, main.Ladder)
	require.Equal(t, config.PolicyTransportThenNode, main.Policy)
	require.Equal(t, []api.PortMapDTO{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443", Probe: "auto"}}, main.Ports)
	require.Len(t, main.Warnings, 1)
	require.Equal(t, state.StateDisabled, st.Tunnels[1].State)
	require.NotEmpty(t, st.Events)
	require.LessOrEqual(t, len(st.Events), 10)
	require.Equal(t, state.EvSwitchTransport, st.Events[0].Type)
	var skipped bool
	for _, w := range st.Warnings {
		if w.Code == string(deyerr.B007) && w.Tunnel == "main" && w.Node == "nl-1" {
			skipped = true
			require.Contains(t, w.Message, "hysteria2/udp")
		}
	}
	require.True(t, skipped, "%v", st.Warnings)
	for _, w := range st.Warnings {
		// nl-1 joined but never connected: no version warning.
		require.NotEqual(t, string(deyerr.N004), w.Code)
	}

	// A paused tunnel is a warning with the command that resumes it.
	require.NoError(t, env.h.st.PutTunnel(state.TunnelState{ID: "main", State: state.StatePaused, Paused: true,
		Active: state.Candidate{Node: "de-1", Transport: "backhaul/wssmux"}}))
	st, err = env.client.Status(ctxT(t))
	require.NoError(t, err)
	var paused bool
	for _, w := range st.Warnings {
		if w.Code == string(deyerr.F004) && w.Tunnel == "main" {
			paused = true
			require.Contains(t, w.Message, "deyroute tunnel resume main")
		}
	}
	require.True(t, paused, "%v", st.Warnings)

	evs, err := env.client.Events(ctxT(t), api.EventQuery{Tunnel: "main"})
	require.NoError(t, err)
	require.Len(t, evs, 1)
	evs, err = env.client.Events(ctxT(t), api.EventQuery{Limit: 1})
	require.NoError(t, err)
	require.Len(t, evs, 1)
	evs, err = env.client.Events(ctxT(t), api.EventQuery{Since: time.Now().Add(time.Hour)})
	require.NoError(t, err)
	require.Empty(t, evs)
	_ = n
}

func TestLogsOverSocket(t *testing.T) {
	env, o := prepareEnv(t, nil)
	o.Logger = nil // real hub.log
	env.startEnv(o)
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	env.addTunnel("main", []string{"de-1"}, 443)
	tl := filepath.Join(env.root, systemd.TunnelLogFile("main"))
	require.NoError(t, os.MkdirAll(filepath.Dir(tl), 0o750))
	require.NoError(t, os.WriteFile(tl, []byte("backend line 1\nconnect password=hunter22secret\n"), 0o600))
	n.on(api.CmdLogsTail, func(ctx context.Context, n *fakeNode, cmd api.Command, stream func([]string)) (any, error) {
		var args api.LogsArgs
		decode(t, cmd, &args)
		if !args.Follow {
			return []string{"node " + args.Target + " line"}, nil
		}
		stream([]string{"node follow 1"})
		<-ctx.Done()
		return []string{}, nil
	})

	collect := func(q api.LogQuery) []api.LogLine {
		var mu sync.Mutex
		var out []api.LogLine
		err := env.client.Logs(ctxT(t), q, func(l api.LogLine) error {
			mu.Lock()
			out = append(out, l)
			mu.Unlock()
			return nil
		})
		require.NoError(t, err)
		return out
	}
	// Hub log (JSON lines of this hub).
	lines := collect(api.LogQuery{Target: "hub"})
	require.NotEmpty(t, lines)
	require.Equal(t, SourceHub, lines[0].Source)
	require.Contains(t, lines[0].Line, `"component":"hub"`)
	require.Empty(t, collect(api.LogQuery{Since: time.Now().Add(time.Hour)}))
	require.Len(t, collect(api.LogQuery{Target: "hub", Lines: 1}), 1)

	// Tunnel: hub file + node tail, redacted.
	lines = collect(api.LogQuery{Target: "main"})
	require.Equal(t, []api.LogLine{
		{Source: SourceHub, Line: "backend line 1"},
		{Source: SourceHub, Line: "connect password=***"},
		{Source: SourceNode, Line: "node main line"},
	}, lines)

	// Follow: the backlog, the node's stream and new hub lines until the
	// client cancels.
	ctx, cancel := context.WithCancel(ctxT(t))
	got := make(chan api.LogLine, 16)
	done := make(chan error, 1)
	go func() {
		done <- env.client.Logs(ctx, api.LogQuery{Target: "main", Follow: true}, func(l api.LogLine) error {
			got <- l
			return nil
		})
	}()
	want := map[string]bool{"backend line 1": false, "node follow 1": false, "appended": false}
	appended := false
	deadline := time.After(testWait)
	for !want["backend line 1"] || !want["node follow 1"] || !want["appended"] {
		select {
		case l := <-got:
			want[l.Line] = true
			if want["backend line 1"] && !appended {
				f, err := os.OpenFile(tl, os.O_APPEND|os.O_WRONLY, 0)
				require.NoError(t, err)
				_, err = f.WriteString("appended\n")
				require.NoError(t, err)
				require.NoError(t, f.Close())
				appended = true
			}
		case <-deadline:
			t.Fatalf("follow incomplete: %v", want)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(testWait):
		t.Fatal("follow did not end")
	}

	// Errors.
	err := env.client.Logs(ctxT(t), api.LogQuery{Target: "node"}, func(api.LogLine) error { return nil })
	require.Equal(t, deyerr.X009, codeOf(err))
	err = env.client.Logs(ctxT(t), api.LogQuery{Target: "nope"}, func(api.LogLine) error { return nil })
	require.Equal(t, deyerr.C021, codeOf(err))

	// An offline node yields an error line instead of failing the call.
	n.stop()
	env.waitOnline("de-1", false)
	lines = collect(api.LogQuery{Target: "main", Lines: 1})
	require.Equal(t, SourceNode, lines[len(lines)-1].Source)
	require.Contains(t, lines[len(lines)-1].Line, "DEY-N003")
}

func TestNodeRemoveRenameList(t *testing.T) {
	env := startHub(t, nil)
	de := env.joinNode("de-1").start()
	env.joinNode("nl-1")
	env.waitOnline("de-1", true)
	env.addTunnel("main", []string{"de-1", "nl-1"}, 443)
	env.addTunnel("solo", []string{"de-1"}, 2053)
	ctx := ctxT(t)

	// Unknown node: N008 for every node operation.
	require.Equal(t, deyerr.N008, codeOf(env.client.NodeRemove(ctx, "xx-1")))
	require.Equal(t, deyerr.N008, codeOf(env.client.NodeRename(ctx, "xx-1", "X")))
	_, err := env.client.NodeTest(ctx, "xx-1")
	require.Equal(t, deyerr.N008, codeOf(err))

	// Rename.
	require.Equal(t, deyerr.C013, codeOf(env.client.NodeRename(ctx, "de-1", "  ")))
	require.NoError(t, env.client.NodeRename(ctx, "de-1", "Germany 1"))
	list, err := env.client.NodeList(ctx)
	require.NoError(t, err)
	require.Equal(t, "Germany 1", list[0].Name)

	// A tunnel would be left without nodes: C008, nothing changes.
	err = env.client.NodeRemove(ctx, "de-1")
	require.Equal(t, deyerr.C008, codeOf(err))
	require.Len(t, env.h.Config().Nodes, 2)

	// After moving "solo" away, de-1 can go.
	_, err = env.h.mutate(func(c *config.Config) error {
		tn, _ := c.Tunnel("solo")
		tn.Nodes = []string{"nl-1"}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, env.client.NodeRemove(ctx, "de-1"))
	cfg := env.h.Config()
	require.Len(t, cfg.Nodes, 1)
	tn, _ := cfg.Tunnel("main")
	require.Equal(t, []string{"nl-1"}, tn.Nodes)
	_, ok, err := env.h.st.GetNode("de-1")
	require.NoError(t, err)
	require.False(t, ok)
	require.False(t, env.h.Online("de-1"))
	// Its certificate is no longer accepted.
	_, err = de.client().FetchAsset(ctx, "amd64", &strings.Builder{})
	require.Equal(t, deyerr.N008, codeOf(err))
	// Automatic backups were taken before the changes.
	backups, err := os.ReadDir(filepath.Join(env.root, config.AutoBackupDir))
	require.NoError(t, err)
	require.NotEmpty(t, backups)
}

func TestNodeTestAndCommands(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1").start()
	offline := env.joinNode("nl-1")
	env.waitOnline("de-1", true)
	var echoWG sync.WaitGroup
	echoCtx, stopEcho := context.WithCancel(context.Background())
	defer func() {
		stopEcho()
		echoWG.Wait()
	}()
	n.on(api.CmdSysinfo, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return map[string]string{"os": "Test OS", "kernel": "6.1"}, nil
	})
	var udpPorts []int
	n.on(api.CmdProbeUDPListen, func(_ context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.UDPListenArgs
		decode(t, cmd, &args)
		udpPorts = append(udpPorts, args.Port)
		if len(udpPorts) == 1 {
			return nil, deyerr.New(deyerr.P012, deyerr.Params{"port": args.Port, "process": "x", "addr": "0.0.0.0"})
		}
		conn, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(args.Port)))
		if err != nil {
			return nil, err
		}
		echoWG.Add(1)
		go func() {
			defer echoWG.Done()
			_ = health.ServeUDPEcho(echoCtx, conn)
		}()
		return nil, nil
	})
	res, err := env.client.NodeTest(ctxT(t), "de-1")
	require.NoError(t, err)
	require.True(t, res.Online)
	require.True(t, res.UDPOK)
	require.Equal(t, "Test OS", res.SysInfo["os"])
	require.Len(t, udpPorts, 2)
	for _, p := range udpPorts {
		require.GreaterOrEqual(t, p, config.CtlRangeLow)
		require.LessOrEqual(t, p, config.CtlRangeHigh)
	}
	list, err := env.client.NodeList(ctxT(t))
	require.NoError(t, err)
	require.NotNil(t, list[0].UDPOK)
	require.True(t, *list[0].UDPOK)
	_, err = env.client.NodeTest(ctxT(t), "nl-1")
	require.Equal(t, deyerr.N003, codeOf(err))

	// Hub move: online nodes accept, offline ones are listed.
	var setHub api.SetHubArgs
	n.on(api.CmdSetHub, func(_ context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		decode(t, cmd, &setHub)
		return nil, nil
	})
	ar, err := env.client.HubAnnounceMove(ctxT(t), "5.6.7.8:44433")
	require.NoError(t, err)
	require.Equal(t, []string{"de-1"}, ar.Accepted)
	require.Equal(t, []string{"nl-1"}, ar.Offline)
	require.Equal(t, "5.6.7.8:44433", setHub.Addr)
	_, err = env.client.HubAnnounceMove(ctxT(t), "nowhere")
	require.Equal(t, deyerr.C013, codeOf(err))

	// Uninstall nodes: only online nodes that accept.
	n.on(api.CmdUninstall, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) { return nil, nil })
	ids, err := env.client.UninstallNodes(ctxT(t))
	require.NoError(t, err)
	require.Equal(t, []string{"de-1"}, ids)
	_ = offline
}

func TestStopAll(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1")
	n.units["deyroute-tun@main.de-1.backhaul-wssmux.service"] = "active"
	n.units["deyroute-tun@main.de-1.rathole-noise.service"] = "inactive"
	var stopped []string
	var mu sync.Mutex
	n.on(api.CmdUnitStop, func(_ context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.UnitArgs
		decode(t, cmd, &args)
		mu.Lock()
		stopped = append(stopped, args.Instance)
		mu.Unlock()
		return api.UnitStatus{}, nil
	})
	n.start()
	env.waitOnline("de-1", true)
	require.Eventually(t, func() bool {
		ns, _ := env.h.nodeState("de-1")
		return len(ns.Units) == 2
	}, testWait, 10*time.Millisecond)
	env.runner.On("systemctl list-units deyroute-tun@* --all --full --no-legend --plain",
		exec.OK("deyroute-tun@main.de-1.backhaul-wssmux.service loaded active running deyroute tunnel\n"+
			"deyroute-tun@main.de-1.frp-tcp.service loaded inactive dead deyroute tunnel\n"))
	require.NoError(t, env.client.StopAll(ctxT(t)))
	require.True(t, env.runner.Called("systemctl stop deyroute-tun@main.de-1.backhaul-wssmux.service"))
	require.False(t, env.runner.Called("systemctl stop deyroute-tun@main.de-1.frp-tcp.service"))
	mu.Lock()
	require.Equal(t, []string{"main.de-1.backhaul-wssmux"}, stopped)
	mu.Unlock()

	// A failing stop is reported after everything was tried.
	env.runner.On("systemctl stop deyroute-tun@main.de-1.backhaul-wssmux.service", exec.Fail(1, "boom"))
	err := env.client.StopAll(ctxT(t))
	require.Equal(t, deyerr.X007, codeOf(err))
}

func TestSettingsAndConfigApply(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	require.NoError(t, env.client.SettingsSet(ctx, api.SettingsRequest{}))
	require.NoError(t, env.client.SettingsSet(ctx, api.SettingsRequest{UIMode: "advanced", Language: "en"}))
	dom := "Tunnel.Example.com."
	require.NoError(t, env.client.SettingsSet(ctx, api.SettingsRequest{Decoys: []string{" www.Example.org ", "www.example.org", ""}, Domain: &dom}))
	cfg := env.h.Config()
	require.Equal(t, "advanced", cfg.Hub.UIMode)
	require.Equal(t, "en", cfg.Hub.Language)
	require.Equal(t, []string{"www.example.org"}, cfg.Hub.DecoySNIs)
	require.Equal(t, "tunnel.example.com", cfg.Hub.Domain)
	require.Equal(t, deyerr.C013, codeOf(env.client.SettingsSet(ctx, api.SettingsRequest{Language: "xx"})))
	require.Equal(t, deyerr.C013, codeOf(env.client.SettingsSet(ctx, api.SettingsRequest{UIMode: "expert"})))
	st, err := env.client.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "advanced", st.Hub.UIMode)

	// config apply: the owner edits config.yaml by hand.
	path := filepath.Join(env.root, config.DefaultPath)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	edited := strings.Replace(string(data), "ui_mode: advanced", "ui_mode: simple", 1)
	edited = strings.Replace(edited, "firewall_managed: true", "firewall_managed: false", 1)
	require.NoError(t, os.WriteFile(path, []byte(edited), 0o600))
	var stepsSeen []api.Step
	var mu sync.Mutex
	res, err := env.client.ConfigApply(ctx, func(s api.Step) {
		mu.Lock()
		stepsSeen = append(stepsSeen, s)
		mu.Unlock()
	})
	require.NoError(t, err)
	require.Equal(t, []string{"hub", "security"}, res.Changed)
	require.FileExists(t, res.Backup)
	// The backup holds the configuration that was running, not the edit.
	saved := backupConfig(t, res.Backup)
	require.Contains(t, saved, "ui_mode: advanced")
	require.NotContains(t, saved, "ui_mode: simple")
	require.Equal(t, "simple", env.h.Config().Hub.UIMode)
	mu.Lock()
	require.NotEmpty(t, stepsSeen)
	require.Equal(t, stepValidate, stepsSeen[0].ID)
	require.Equal(t, "validate config.yaml", stepsSeen[0].Title)
	mu.Unlock()
	env.waitEvent(state.EvConfigApplied, "")
	st, err = env.client.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, FirewallModeSuggestOnly, st.Hub.Firewall)
	var p031 bool
	for _, w := range st.Warnings {
		p031 = p031 || w.Code == string(deyerr.P031)
	}
	require.True(t, p031)

	// A broken file is refused and the running config stays.
	require.NoError(t, os.WriteFile(path, []byte(edited+"bogus_key: 1\n"), 0o600))
	_, err = env.client.ConfigApply(ctx, nil)
	require.Equal(t, deyerr.C001, codeOf(err))
	require.Equal(t, "simple", env.h.Config().Hub.UIMode)
}

func TestWrongRoleAndUnknownTunnel(t *testing.T) {
	env := startHub(t, nil)
	require.Equal(t, deyerr.X009, codeOf(env.client.NodeSetHub(ctxT(t), "1.2.3.4:44433")))
	// Tunnel methods on an unknown tunnel: DEY-C021 with the hub log.
	_, err := env.client.TunnelShow(ctxT(t), "x")
	require.Equal(t, deyerr.C021, codeOf(err))
	require.Equal(t, LogFile, deyerr.As(err).Log())
	_, err = env.client.DiagSpeed(ctxT(t), "x", 1, nil)
	require.Equal(t, deyerr.C021, codeOf(err))
	require.Equal(t, LogFile, deyerr.As(err).Log())
	require.Equal(t, deyerr.C021, codeOf(env.client.SecurityRotateTokens(ctxT(t), "x", nil)))
	_, err = env.client.SecurityTLSShow(ctxT(t), "x")
	require.Equal(t, deyerr.C021, codeOf(err))
}

// backupConfig returns etc/deyroute/config.yaml of the plain backup at path.
func backupConfig(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		require.NoError(t, err, "no etc/deyroute/config.yaml in %s", path)
		if h.Name == "etc/deyroute/config.yaml" {
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			return string(data)
		}
	}
}
