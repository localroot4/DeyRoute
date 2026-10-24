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
	"sync/atomic"
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

// sendMessageBody is the part of a sendMessage request the tests check.
type sendMessageBody struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

func TestTelegramViaNodeFirst(t *testing.T) {
	// srv stands for api.telegram.org reached directly; it counts the posts.
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		require.Contains(t, r.URL.Path, "/bot"+testBotToken+"/sendMessage")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	direct := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), bodies...)
	}
	env, o := prepareEnv(t, telegramOn)
	writeBotToken(t, env.root)
	o.TelegramAPIBase = srv.URL
	env.startEnv(o)
	tg := env.h.Notifier()
	require.NotNil(t, tg)

	// No node online (before the first join): the node sender gives up at
	// once (DEY-N012) and the message is posted directly, once.
	require.NoError(t, tg.Test(ctxT(t)))
	require.Len(t, direct(), 1)
	var msg sendMessageBody
	require.NoError(t, json.Unmarshal([]byte(direct()[0]), &msg))
	require.Equal(t, "123456789", msg.ChatID)
	require.Contains(t, msg.Text, "DEYROUTE test message")

	// An online node answers http.post: events go through it and nothing is
	// posted directly.
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	posted := make(chan api.Command, 4)
	n.on(api.CmdHTTPPost, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		posted <- cmd
		return api.HTTPPostResult{Status: 200, Body: `{"ok":true}`}, nil
	})
	nextPost := func() api.Command {
		t.Helper()
		select {
		case cmd := <-posted:
			return cmd
		case <-time.After(testWait):
			t.Fatal("no http.post through the node")
		}
		return api.Command{}
	}
	env.h.Emit(state.Event{Type: state.EvTunnelDown, Tunnel: "main", Level: state.LevelError, Code: "DEY-F001", Message: "all rungs failed"})
	env.h.Emit(state.Event{Type: state.EvConfigApplied, Message: "not selected"})
	first := nextPost()
	var args api.HTTPPostArgs
	decode(t, first, &args)
	require.True(t, strings.HasSuffix(args.URL, "/bot"+testBotToken+"/sendMessage"))
	require.NoError(t, json.Unmarshal(args.Body, &msg))
	require.Equal(t, "123456789", msg.ChatID)
	require.Contains(t, msg.Text, "tunnel_down")
	// The nodes get at most DefaultNotifyViaNode of the one-minute budget;
	// the rest stays for the direct post.
	require.Positive(t, first.TimeoutMs)
	require.LessOrEqual(t, first.TimeoutMs, int(DefaultNotifyViaNode/time.Millisecond))
	require.NoError(t, tg.Test(ctxT(t)))
	nextPost()
	require.Len(t, direct(), 1, "nothing is posted directly while a node delivers")

	// The node cannot reach Telegram: the message is posted directly.
	n.on(api.CmdHTTPPost, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		posted <- cmd
		return nil, deyerr.New(deyerr.X050, deyerr.Params{"reason": "unreachable"})
	})
	require.NoError(t, tg.Test(ctxT(t)))
	nextPost()
	require.Len(t, direct(), 2)
	require.NoError(t, json.Unmarshal([]byte(direct()[1]), &msg))
	require.Contains(t, msg.Text, "DEYROUTE test message")

	// A node that hangs does not use up a caller's deadline shorter than a
	// minute (the TUI's): the nodes get half of it, the direct post the rest.
	n.on(api.CmdHTTPPost, func(ctx context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		posted <- cmd
		<-ctx.Done()
		return nil, ctx.Err()
	})
	short, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	require.NoError(t, tg.Test(short))
	hung := nextPost()
	require.Positive(t, hung.TimeoutMs)
	require.LessOrEqual(t, hung.TimeoutMs, 2000)
	require.Len(t, direct(), 3)

	// The via-node sender alone.
	n.on(api.CmdHTTPPost, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.HTTPPostResult{Status: 200, Body: `{"ok":true}`}, nil
	})
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

// A node that hangs (its control link died, but it still counts as online)
// gets only its share of the via-node budget: the next node, or else the
// direct post, still delivers.
func TestTelegramNodeHangs(t *testing.T) {
	var directPosts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directPosts.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	const budget = time.Second
	env, o := prepareEnv(t, telegramOn, func(o *Options, _ string) { o.NotifyViaNode = budget })
	writeBotToken(t, env.root)
	o.TelegramAPIBase = srv.URL
	env.startEnv(o)
	tg := env.h.Notifier()
	require.NotNil(t, tg)

	// call is one http.post a node received.
	type call struct {
		node string
		cmd  api.Command
	}
	var mu sync.Mutex
	var calls []call
	// hangFirst: the first http.post (whichever node gets it) hangs until
	// its deadline; the next ones are delivered.
	hangFirst := func(ctx context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		mu.Lock()
		calls = append(calls, call{n.id, cmd})
		first := len(calls) == 1
		mu.Unlock()
		if first {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return api.HTTPPostResult{Status: 200, Body: `{"ok":true}`}, nil
	}
	received := func() []call {
		mu.Lock()
		defer mu.Unlock()
		return append([]call(nil), calls...)
	}

	// The only online node hangs: after NotifyViaNode the message is posted
	// directly, once.
	a := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	a.on(api.CmdHTTPPost, hangFirst)
	require.NoError(t, tg.Test(ctxT(t)))
	require.EqualValues(t, 1, directPosts.Load())
	got := received()
	require.Len(t, got, 1)
	require.Positive(t, got[0].cmd.TimeoutMs)
	require.LessOrEqual(t, got[0].cmd.TimeoutMs, int(budget/time.Millisecond))

	// Two online nodes, the first one tried hangs: it gets half of the
	// budget and the second one delivers; nothing is posted directly.
	b := env.joinNode("de-2").start()
	env.waitOnline("de-2", true)
	b.on(api.CmdHTTPPost, hangFirst)
	mu.Lock()
	calls = nil
	mu.Unlock()
	require.NoError(t, tg.Test(ctxT(t)))
	require.EqualValues(t, 1, directPosts.Load())
	got = received()
	require.Len(t, got, 2)
	require.NotEqual(t, got[0].node, got[1].node)
	require.Positive(t, got[0].cmd.TimeoutMs)
	require.LessOrEqual(t, got[0].cmd.TimeoutMs, int(budget/2/time.Millisecond))
	require.Positive(t, got[1].cmd.TimeoutMs)
}

func TestViaNodeBudget(t *testing.T) {
	h := &Hub{o: Options{NotifyViaNode: DefaultNotifyViaNode}}
	require.Equal(t, DefaultNotifyViaNode, h.viaNodeBudget(context.Background()))
	for _, c := range []struct{ left, want time.Duration }{
		// The notifier's minute: 20 s stay for the direct post.
		{time.Minute, 40 * time.Second},
		{2 * time.Minute, 40 * time.Second},
		{45 * time.Second, 25 * time.Second},
		// Shorter callers (the TUI): never less than half for the nodes.
		{30 * time.Second, 15 * time.Second},
		{4 * time.Second, 2 * time.Second},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), c.left)
		got := h.viaNodeBudget(ctx)
		cancel()
		require.InDelta(t, float64(c.want), float64(got), float64(500*time.Millisecond), "%s left", c.left)
	}
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
