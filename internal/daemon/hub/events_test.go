package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/state"
)

const testBotToken = "123456:ABCdefGhIJKlmnoPQRstuVWxyz_0123456789"

// telegramOn enables Telegram notifications in the config.
func telegramOn(c *config.Config) {
	c.Hub.Notify.Telegram = config.Telegram{
		Enabled: true, BotTokenFile: "/etc/deyroute/secrets/telegram.token",
		ChatID: "123456789", Events: []string{"down", "node_offline"},
	}
}

// writeBotToken writes the bot token file under root.
func writeBotToken(t *testing.T, root string) {
	p := filepath.Join(root, config.SecretsDir, "telegram.token")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte(testBotToken+"\n"), 0o600))
}

func TestTelegramDirectAndViaNode(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		code := status
		mu.Unlock()
		require.Contains(t, r.URL.Path, "/bot"+testBotToken+"/sendMessage")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	env, o := prepareEnv(t, telegramOn)
	writeBotToken(t, env.root)
	o.TelegramAPIBase = srv.URL
	env.startEnv(o)
	require.NotNil(t, env.h.Notifier())

	env.h.Emit(state.Event{Type: state.EvTunnelDown, Tunnel: "main", Level: state.LevelError, Code: "DEY-F001", Message: "all rungs failed"})
	env.h.Emit(state.Event{Type: state.EvConfigApplied, Message: "not selected"})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(bodies) == 1
	}, testWait, 10*time.Millisecond)
	mu.Lock()
	var msg struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	require.NoError(t, json.Unmarshal([]byte(bodies[0]), &msg))
	mu.Unlock()
	require.Equal(t, "123456789", msg.ChatID)
	require.Contains(t, msg.Text, "tunnel_down")

	// Direct delivery fails: a node posts it (http.post).
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	var posted api.HTTPPostArgs
	postedCh := make(chan struct{}, 1)
	n.on(api.CmdHTTPPost, func(_ context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		decode(t, cmd, &posted)
		postedCh <- struct{}{}
		return api.HTTPPostResult{Status: 200, Body: `{"ok":true}`}, nil
	})
	mu.Lock()
	status = http.StatusBadGateway
	mu.Unlock()
	env.h.Emit(state.Event{Type: state.EvTunnelDown, Tunnel: "other", Level: state.LevelError, Message: "down"})
	select {
	case <-postedCh:
	case <-time.After(testWait):
		t.Fatal("no http.post through the node")
	}
	require.True(t, strings.HasSuffix(posted.URL, "/sendMessage"))
	require.Contains(t, string(posted.Body), "123456789")

	// The via-node sender alone.
	code, body, err := nodeSender{env.h}.Post(ctxT(t), srv.URL+"/x", "application/json", []byte("{}"))
	require.NoError(t, err)
	require.Equal(t, 200, code)
	require.Contains(t, string(body), "ok")
	n.on(api.CmdHTTPPost, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return nil, deyerr.New(deyerr.X050, deyerr.Params{"reason": "unreachable"})
	})
	_, _, err = nodeSender{env.h}.Post(ctxT(t), srv.URL+"/x", "application/json", nil)
	require.Equal(t, deyerr.X050, codeOf(err))
}

func TestNotifierOffAndBroken(t *testing.T) {
	env := startHub(t, nil)
	require.Nil(t, env.h.Notifier())
	_, _, err := nodeSender{env.h}.Post(ctxT(t), "https://api.telegram.org/x", "application/json", nil)
	require.Equal(t, deyerr.N012, codeOf(err))
	// Enabled with a missing token file: the notifier stays off.
	cfg := config.Clone(env.h.Config())
	cfg.Hub.Notify.Telegram = config.Telegram{Enabled: true, ChatID: "1"}
	env.h.reloadNotifier(cfg)
	require.Nil(t, env.h.Notifier())
}

func TestSubscribe(t *testing.T) {
	env := startHub(t, nil)
	ch, cancel := env.h.Subscribe(0)
	env.h.Emit(state.Event{Type: state.EvTunnelUp, Tunnel: "main"})
	select {
	case e := <-ch:
		require.Equal(t, state.EvTunnelUp, e.Type)
		require.NotZero(t, e.Seq)
		require.False(t, e.At.IsZero())
		require.Equal(t, state.LevelInfo, e.Level)
		require.Equal(t, state.EvTunnelUp, e.Message)
	case <-time.After(testWait):
		t.Fatal("no event")
	}
	cancel()
	cancel()
	_, open := <-ch
	require.False(t, open)

	// A slow subscriber loses events but never blocks the hub.
	slow, cancel2 := env.h.Subscribe(1)
	for range 5 {
		env.h.Emit(state.Event{Type: state.EvProbeError, Level: state.LevelWarn})
	}
	require.Len(t, slow, 1)
	// Subscriptions end when the hub stops.
	late, _ := env.h.Subscribe(4)
	env.stop()
	_, open = <-late
	require.False(t, open)
	cancel2()
	after, _ := env.h.Subscribe(1)
	_, open = <-after
	require.False(t, open)
}

func TestFirewallManager(t *testing.T) {
	env, o := prepareEnv(t, nil, func(o *Options, _ string) { o.DisableFirewall = false })
	env.startEnv(o)
	// Startup apply: no token, no node: the control port is restricted with
	// the rate-limited accept for unknown sources.
	require.Eventually(t, func() bool { return len(env.nftScripts()) > 0 }, testWait, 10*time.Millisecond)
	first := env.nftScripts()[0]
	require.Contains(t, first, "table inet deyroute")
	require.Contains(t, first, "limit rate 6/minute")

	// Joining: the join command opens the window at once (control port open
	// to all); consuming the only token closes it and the node IP is in
	// @nodes.
	env.joinNode("de-1")
	var opened bool
	for _, s := range env.nftScripts() {
		opened = opened || strings.Contains(s, "tcp dport 44433 accept")
	}
	require.True(t, opened)
	require.Eventually(t, func() bool {
		scripts := env.nftScripts()
		last := scripts[len(scripts)-1]
		return strings.Contains(last, "ip saddr @nodes accept") && strings.Contains(last, "127.0.0.1") &&
			strings.Contains(last, "limit rate 6/minute")
	}, testWait, 10*time.Millisecond)
	st := env.h.Firewall()
	require.True(t, st.Managed)
	require.True(t, st.Computed)
	require.False(t, st.Applied.IsZero())

	// A token that expires closes the window by itself.
	_, _, err := env.h.joins.Create(200 * time.Millisecond)
	require.NoError(t, err)
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	require.False(t, env.h.Firewall().Spec.RestrictControl)
	env.h.wakeFirewall()
	require.Eventually(t, func() bool { return env.h.Firewall().Spec.RestrictControl }, testWait, 10*time.Millisecond)
	require.Equal(t, []string{"127.0.0.1"}, env.h.Firewall().Spec.NodeIPs4)

	// An nft failure is remembered and shown as a warning.
	env.runner.OnPrefix("nft -f -", exec.Fail(1, "Error: syntax"))
	require.Error(t, env.h.applyFirewall(ctxT(t)))
	status, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	var p019 bool
	for _, w := range status.Warnings {
		p019 = p019 || w.Code == string(deyerr.P019)
	}
	require.True(t, p019, "%v", status.Warnings)
}
