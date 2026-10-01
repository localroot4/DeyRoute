package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/state"
)

// writeProc writes fake /proc/sys values below root.
func writeProc(t *testing.T, root string, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		p := filepath.Join(root, "proc", "sys", strings.ReplaceAll(k, ".", "/"))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(v+"\n"), 0o644)) // #nosec G306 -- fake procfs
	}
}

func readProc(t *testing.T, root, key string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "proc", "sys", strings.ReplaceAll(key, ".", "/")))
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

func TestOptimizeApplyRevert(t *testing.T) {
	env, o := prepareEnv(t, nil)
	writeProc(t, env.root, map[string]string{
		"net.core.somaxconn":                        "4096",
		"net.ipv4.tcp_fin_timeout":                  "60",
		"net.ipv4.tcp_congestion_control":           "cubic",
		"net.ipv4.tcp_available_congestion_control": "reno cubic",
		"kernel.osrelease":                          "6.1.0-test",
	})
	require.NoError(t, os.WriteFile(filepath.Join(env.root, "proc", "meminfo"), []byte("MemTotal:        1000000 kB\n"), 0o644)) // #nosec G306 -- fake procfs
	env.startEnv(o)
	ctx := ctxT(t)
	n := env.joinNode("de-1")
	var mu sync.Mutex
	var got []api.SysctlArgs
	n.on(api.CmdSysctlApply, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SysctlArgs
		decode(t, cmd, &a)
		mu.Lock()
		got = append(got, a)
		mu.Unlock()
		// What the node skipped comes back to the owner.
		return api.SysctlResult{Warnings: []string{"skip net.ipv4.tcp_congestion_control = bbr: tcp_bbr is not available"}}, nil
	})
	n.start()
	env.waitOnline("de-1", true)
	env.joinNode("nl-1") // joined, never connected: offline

	st, err := env.client.OptimizeStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, st.Profile)
	require.False(t, st.BBRAvailable)
	require.Equal(t, uint64(1000000*1024), st.MemBytes)
	require.Equal(t, config.SysctlBalanced, st.Recommended)

	_, err = env.client.OptimizeApply(ctx, "turbo")
	require.Equal(t, deyerr.C013, codeOf(err))

	st, err = env.client.OptimizeApply(ctx, config.SysctlBalanced)
	require.NoError(t, err)
	require.Equal(t, config.SysctlBalanced, st.Profile)
	require.Equal(t, "65535", st.Applied["net.core.somaxconn"])
	require.Equal(t, "65535", readProc(t, env.root, "net.core.somaxconn"))
	require.Equal(t, "15", readProc(t, env.root, "net.ipv4.tcp_fin_timeout"))
	require.Equal(t, "cubic", readProc(t, env.root, "net.ipv4.tcp_congestion_control"), "BBR is skipped")
	joined := strings.Join(st.Warnings, "\n")
	require.Contains(t, joined, "tcp_bbr is not available")
	require.Contains(t, joined, "node nl-1 is offline")
	require.Contains(t, joined, "node de-1: skip net.ipv4.tcp_congestion_control = bbr")
	require.FileExists(t, filepath.Join(env.root, config.SysctlConfPath))
	require.Equal(t, config.SysctlBalanced, env.h.Config().Tuning.SysctlProfile)
	bbrOn := true
	mu.Lock()
	require.Equal(t, []api.SysctlArgs{{Profile: config.SysctlBalanced, BBR: &bbrOn}}, got)
	mu.Unlock()
	env.waitEvent(state.EvConfigApplied, "")

	st, err = env.client.OptimizeRevert(ctx)
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, st.Profile)
	require.Equal(t, "4096", readProc(t, env.root, "net.core.somaxconn"))
	require.Equal(t, "60", readProc(t, env.root, "net.ipv4.tcp_fin_timeout"))
	require.NoFileExists(t, filepath.Join(env.root, config.SysctlConfPath))
	require.Equal(t, config.SysctlOff, env.h.Config().Tuning.SysctlProfile)
	mu.Lock()
	require.Len(t, got, 2)
	require.Equal(t, config.SysctlOff, got[1].Profile)
	mu.Unlock()

	// The hub's tuning.bbr reaches the nodes (the hub config is the only
	// source of truth).
	_, err = env.h.mutate(func(c *config.Config) error { c.Tuning.BBR = false; return nil })
	require.NoError(t, err)
	_, err = env.client.OptimizeApply(ctx, config.SysctlBalanced)
	require.NoError(t, err)
	mu.Lock()
	require.Len(t, got, 3)
	require.NotNil(t, got[2].BBR)
	require.False(t, *got[2].BBR)
	mu.Unlock()

	// A node that refuses is reported, the hub still applies; aggressive on
	// this 1 GB hub is applied with a warning.
	n.on(api.CmdSysctlApply, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return nil, deyerr.New(deyerr.X033, deyerr.Params{"key": "net.core.somaxconn", "value": "65535"})
	})
	st, err = env.client.OptimizeApply(ctx, config.SysctlAggressive)
	require.NoError(t, err)
	joined = strings.Join(st.Warnings, "\n")
	require.Contains(t, joined, "node de-1: DEY-X033")
	require.Contains(t, joined, "aggressive is meant for servers with 4 GB RAM or more; this one has 976 MB")
}

// withIPForward creates the ip_forward switch a NAT transport sets.
func withIPForward(o *Options, root string) {
	p := filepath.Join(root, "proc", "sys", "net", "ipv4", "ip_forward")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("0\n"), 0o644) // #nosec G306 -- fake procfs
	_ = o
}

func TestIPForwardNeeds(t *testing.T) {
	te := startTunnelHub(t, withIPForward)
	te.tunnelNode("de-1")
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "wg", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trNAT, Failover: fastFailover(false)})
	hub, nodes := te.h.ipForwardNeeds()
	require.True(t, hub)
	require.Empty(t, nodes)
}

// telegramServer is a fake Bot API that records the posted texts.
type telegramServer struct {
	*httptest.Server
	mu    sync.Mutex
	texts []string
	code  int
}

func newTelegramServer(t *testing.T) *telegramServer {
	ts := &telegramServer{code: http.StatusOK}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var msg struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(data, &msg)
		ts.mu.Lock()
		ts.texts = append(ts.texts, msg.Text)
		code := ts.code
		ts.mu.Unlock()
		if !strings.Contains(r.URL.Path, "/bot"+testBotToken+"/sendMessage") {
			code = http.StatusNotFound
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"ok":true,"description":"test"}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *telegramServer) sent() []string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]string(nil), ts.texts...)
}

func TestNotifyTelegramSetTestOff(t *testing.T) {
	tg := newTelegramServer(t)
	env, o := prepareEnv(t, nil)
	o.TelegramAPIBase = tg.URL
	env.startEnv(o)
	ctx := ctxT(t)

	// Not configured yet.
	require.Equal(t, deyerr.C013, codeOf(env.client.NotifyTelegramTest(ctx)))
	require.Equal(t, deyerr.C013, codeOf(env.client.NotifyTelegramSet(ctx, "bot.token", "1", nil)))
	require.Equal(t, deyerr.S009, codeOf(env.client.NotifyTelegramSet(ctx, "/root/missing.token", "1", nil)))

	src := "/root/bot.token"
	require.NoError(t, os.MkdirAll(filepath.Join(env.root, "root"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(env.root, src), []byte(testBotToken+"\n"), 0o600))
	require.Equal(t, deyerr.C013, codeOf(env.client.NotifyTelegramSet(ctx, src, "123456789", []string{"bogus"})))
	require.Equal(t, deyerr.C013, codeOf(env.client.NotifyTelegramSet(ctx, src, "not a chat", nil)))

	require.NoError(t, env.client.NotifyTelegramSet(ctx, src, "123456789", []string{"down", "Switch"}))
	tgCfg := env.h.Config().Hub.Notify.Telegram
	require.True(t, tgCfg.Enabled)
	require.Equal(t, config.DefaultTelegramTokenFile, tgCfg.BotTokenFile)
	require.Equal(t, "123456789", tgCfg.ChatID)
	require.Equal(t, []string{"down", "switch"}, tgCfg.Events)
	// Status shows the settings (the menu's header and defaults), never the token.
	st, err := env.client.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, api.TelegramStatus{Enabled: true, ChatID: "123456789", TokenFile: config.DefaultTelegramTokenFile,
		Events: []string{"down", "switch"}}, st.Hub.Telegram)
	copied := filepath.Join(env.root, config.DefaultTelegramTokenFile)
	fi, err := os.Stat(copied)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	data, err := os.ReadFile(copied)
	require.NoError(t, err)
	require.Equal(t, testBotToken, strings.TrimSpace(string(data)))
	require.NotNil(t, env.h.Notifier())

	require.NoError(t, env.client.NotifyTelegramTest(ctx))
	require.NotEmpty(t, tg.sent())
	require.True(t, strings.HasPrefix(tg.sent()[0], "DEYROUTE test message"), tg.sent()[0])

	// Telegram refuses: the DEY code of the notifier.
	tg.mu.Lock()
	tg.code = http.StatusUnauthorized
	tg.mu.Unlock()
	require.Equal(t, deyerr.C050, codeOf(env.client.NotifyTelegramTest(ctx)))
	tg.mu.Lock()
	tg.code = http.StatusOK
	tg.mu.Unlock()

	require.NoError(t, env.client.NotifyTelegramOff(ctx))
	require.False(t, env.h.Config().Hub.Notify.Telegram.Enabled)
	st, err = env.client.Status(ctx)
	require.NoError(t, err)
	require.False(t, st.Hub.Telegram.Enabled)
	require.Equal(t, "123456789", st.Hub.Telegram.ChatID, "the chat is kept for the next set-up")
	require.Nil(t, env.h.Notifier())
	require.NoError(t, env.client.NotifyTelegramOff(ctx)) // already off
	// Saved settings can still be tested while off.
	require.NoError(t, env.client.NotifyTelegramTest(ctx))
	require.Len(t, tg.sent(), 3)
}

func TestSecurityFirewall(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	info, err := env.client.SecurityFirewall(ctx, "show")
	require.NoError(t, err)
	require.True(t, info.Managed)
	require.NotNil(t, info.Detected)

	_, err = env.client.SecurityFirewall(ctx, "explode")
	require.Equal(t, deyerr.C013, codeOf(err))

	info, err = env.client.SecurityFirewall(ctx, "disable")
	require.NoError(t, err)
	require.False(t, info.Managed)
	require.False(t, env.h.Config().Security.FirewallManaged)
	require.Contains(t, info.Ruleset, "table inet deyroute")
	require.Contains(t, info.Suggested, "deyroute security firewall apply")

	info, err = env.client.SecurityFirewall(ctx, "apply")
	require.NoError(t, err)
	require.True(t, info.Managed)
	require.True(t, env.h.Config().Security.FirewallManaged)
}

func TestSecurityFirewallRunsNft(t *testing.T) {
	env, o := prepareEnv(t, nil)
	o.DisableFirewall = false
	env.startEnv(o)
	env.runner.On("nft list table inet deyroute", exec.OK("table inet deyroute {\n}\n"))
	env.runner.On("ufw status verbose", exec.OK("Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"))
	env.runner.On("ufw status", exec.OK("Status: active\n"))
	ctx := ctxT(t)
	info, err := env.client.SecurityFirewall(ctx, "disable")
	require.NoError(t, err)
	require.True(t, env.runner.Called("nft delete table inet deyroute"))
	require.False(t, info.Managed)
	require.NotEmpty(t, info.Suggested, "ufw blocks the control port")
	require.Contains(t, strings.Join(info.Suggested, "\n"), "ufw allow 44433/tcp")
	info, err = env.client.SecurityFirewall(ctx, "apply")
	require.NoError(t, err)
	require.True(t, info.Managed)
	require.Equal(t, "table inet deyroute {\n}\n", info.Ruleset)
}

func TestSecurityAudit(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	env.runner.On("ss -Hlntup", exec.OK(strings.Join([]string{
		`tcp   LISTEN 0      4096         0.0.0.0:22        0.0.0.0:*    users:(("sshd",pid=10,fd=3))`,
		`tcp   LISTEN 0      511          0.0.0.0:8080      0.0.0.0:*    users:(("nginx",pid=20,fd=6))`,
		`tcp   LISTEN 0      244        127.0.0.1:5432      0.0.0.0:*    users:(("postgres",pid=30,fd=5))`,
		`tcp   LISTEN 0      4096            *:44433           *:*    users:(("deyroute",pid=1,fd=7))`,
		`tcp   LISTEN 0      4096      0.0.0.0:30001       0.0.0.0:*    users:(("backhaul",pid=40,fd=7))`,
		`udp   UNCONN 0      0            0.0.0.0:68        0.0.0.0:*    users:(("dhclient",pid=50,fd=6))`,
		`udp   UNCONN 0      0      127.0.0.53%lo:53        0.0.0.0:*    users:(("systemd-resolve",pid=60,fd=13))`,
		`udp   UNCONN 0      0               [::]:5353          [::]:*    users:(("avahi-daemon",pid=70,fd=13))`,
		"garbage",
	}, "\n")+"\n"))
	rep, err := env.client.SecurityAudit(ctx)
	require.NoError(t, err)
	byCheck := map[string][]api.AuditItem{}
	for _, it := range rep.Items {
		byCheck[it.Check] = append(byCheck[it.Check], it)
	}
	ports := byCheck[AuditPorts]
	require.Len(t, ports, 3, "%v", ports)
	require.Equal(t, sevOK, ports[0].Severity)
	require.Contains(t, ports[0].Message, "22/tcp")
	require.Contains(t, ports[1].Message, "5353/udp")
	require.Contains(t, ports[2].Message, "8080/tcp")
	require.Contains(t, ports[2].Message, "nginx")
	require.Equal(t, sevWarn, ports[2].Severity)
	require.Equal(t, sevOK, byCheck[AuditSecrets][0].Severity)
	require.Equal(t, sevOK, byCheck[AuditJoinTokens][0].Severity)
	require.Equal(t, sevOK, byCheck[AuditFirewall][0].Severity)
	require.Equal(t, sevOK, byCheck[AuditControl][0].Severity)
	require.Equal(t, sevOK, byCheck[AuditVersions][0].Severity)
	require.Equal(t, sevOK, byCheck[AuditCerts][0].Severity)
	require.False(t, rep.Clean)

	// Only deyroute ports, then problems of every other kind.
	env.runner.On("ss -Hlntup", exec.OK(`tcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=10,fd=3))`+"\n"))
	rep, err = env.client.SecurityAudit(ctx)
	require.NoError(t, err)
	require.True(t, rep.Clean, "%v", rep.Items)

	_, err = env.h.Local().NodeJoinCommand(ctx, 0)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Join(env.root, config.SecretsDir, "hub.key"), 0o644))
	_, err = env.h.mutate(func(c *config.Config) error {
		c.Security.FirewallManaged = false
		c.Security.RestrictControlToNodes = false
		return nil
	})
	require.NoError(t, err)
	old := env.joinNode("old")
	old.version = "0.0.1"
	old.start()
	env.waitOnline("old", true)
	env.runner.On("ss -Hlntup", exec.Fail(1, "ss: broken"))
	rep, err = env.client.SecurityAudit(ctx)
	require.NoError(t, err)
	require.False(t, rep.Clean)
	sev := map[string]string{}
	for _, it := range rep.Items {
		if sev[it.Check] != sevError {
			sev[it.Check] = it.Severity
		}
	}
	require.Equal(t, sevError, sev[AuditSecrets])
	require.Equal(t, sevWarn, sev[AuditJoinTokens])
	require.Equal(t, sevWarn, sev[AuditFirewall])
	require.Equal(t, sevWarn, sev[AuditControl])
	require.Equal(t, sevError, sev[AuditVersions])
	require.Equal(t, sevWarn, sev[AuditPorts])
}

func TestDecoyCheck(t *testing.T) {
	var mu sync.Mutex
	reachable := map[string]bool{"b.example.com": true}
	var tried []string
	env, o := prepareEnv(t, func(c *config.Config) { c.Hub.DecoySNIs = []string{"a.example.com", "b.example.com"} })
	o.DecoyCheck = func(_ context.Context, sni string) error {
		mu.Lock()
		defer mu.Unlock()
		tried = append(tried, sni)
		if reachable[sni] {
			return nil
		}
		return errors.New("handshake failed")
	}
	env.startEnv(o)
	require.Eventually(t, func() bool { return env.h.currentDecoy(env.h.Config()) == "b.example.com" }, testWait, 10*time.Millisecond)
	var saved string
	ok, err := env.h.st.GetMeta(metaDecoy, &saved)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "b.example.com", saved)

	// None answers: the choice stays and DEY-B042 is reported.
	mu.Lock()
	reachable = map[string]bool{}
	mu.Unlock()
	env.h.checkDecoys(ctxT(t))
	ev := env.waitEvent(state.EvProbeError, "")
	require.Equal(t, string(deyerr.B042), ev.Code)
	require.Contains(t, ev.Reason, "a.example.com: handshake failed")
	require.Equal(t, "b.example.com", env.h.currentDecoy(env.h.Config()))

	// The first answers again: it becomes the decoy.
	mu.Lock()
	reachable = map[string]bool{"a.example.com": true}
	mu.Unlock()
	env.h.checkDecoys(ctxT(t))
	require.Equal(t, "a.example.com", env.h.currentDecoy(env.h.Config()))
	// A decoy removed from the list is not used any more.
	cfg := config.Clone(env.h.Config())
	cfg.Hub.DecoySNIs = []string{"c.example.com"}
	require.Equal(t, "c.example.com", env.h.currentDecoy(cfg))
	require.Equal(t, backend.DecoyCandidates(nil)[0], env.h.currentDecoy(nil))
}

func TestCheckDecoyTLSRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// The name does not resolve: the handshake cannot even start.
	require.Error(t, checkDecoyTLS(ctx, "decoy.invalid"))
}

func TestCollectMetrics(t *testing.T) {
	procRoot := t.TempDir()
	te := startTunnelHub(t, func(o *Options, _ string) { o.ProcRoot = procRoot })
	te.tunnelNode("de-1")
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	hex := fmt.Sprintf("%04X", port)
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:" + hex + " 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 1\n" +
		"   1: 0100007F:" + hex + " 0100007F:D432 01 00000000:00000000 00:00000000 00000000     0        0 2\n" +
		"   2: 0100007F:" + hex + " 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3\n" +
		"   3: 0100007F:0016 0100007F:D433 01 00000000:00000000 00:00000000 00000000     0        0 4\n"
	require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "proc", "net"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(procRoot, "proc", "net", "tcp"), []byte(tcp), 0o644)) // #nosec G306 -- fake procfs
	te.h.collectMetrics(ctxT(t))
	m, ok, err := te.h.st.GetMetrics("main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2, m.ActiveConns)
	require.Equal(t, "ss", m.Source)

	// Without /proc the hub asks ss.
	require.NoError(t, os.RemoveAll(filepath.Join(procRoot, "proc")))
	te.runner.On("ss -Htn state established", exec.OK(
		"0 0 127.0.0.1:"+strconv.Itoa(port)+" 127.0.0.1:5555\n0 0 [::1]:"+strconv.Itoa(port)+" [::1]:5556\n0 0 10.0.0.1:22 10.0.0.2:1\n"))
	te.h.collectMetrics(ctxT(t))
	m, _, err = te.h.st.GetMetrics("main")
	require.NoError(t, err)
	require.Equal(t, 2, m.ActiveConns)
}

func TestCollectMetricsNATAsksNode(t *testing.T) {
	te := startTunnelHub(t, withIPForward)
	n := te.tunnelNode("de-1")
	n.on(api.CmdMetrics, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.MetricsArgs
		decode(t, cmd, &a)
		if len(a.Ports) != 1 {
			return nil, errors.New("want one target port")
		}
		return api.MetricsResult{ActiveConns: 7, BytesIn: 10, BytesOut: 20}, nil
	})
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "wg", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trNAT, Failover: fastFailover(false)})
	te.h.collectMetrics(ctxT(t))
	m, ok, err := te.h.st.GetMetrics("wg")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 7, m.ActiveConns)
	require.Equal(t, uint64(20), m.BytesOut)
	require.Equal(t, []int{443, 80}, targetPorts(config.Tunnel{Ports: []config.PortMap{
		{Listen: 1, Proto: "tcp", Target: "127.0.0.1:443"}, {Listen: 2, Proto: "tcp", Target: "127.0.0.1:443"},
		{Listen: 3, Proto: "udp", Target: "127.0.0.1:53"}, {Listen: 4, Proto: "tcp", Target: "x:80"},
	}}))
}
