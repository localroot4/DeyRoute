package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

const testToken = "123456789:AAEhBP0av28sAbCdEfGhIjKlMnOpQrStUvW"

var at = time.Date(2026, 9, 29, 12, 41, 3, 0, time.UTC)

// fakeClock is a manual clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// apiRequest is one recorded sendMessage call.
type apiRequest struct {
	Path        string
	ContentType string
	Body        sendMessage
}

// fakeAPI is a Telegram Bot API stand-in.
type fakeAPI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	reqs   []apiRequest
	status int
	reply  string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{status: http.StatusOK, reply: `{"ok":true,"result":{"message_id":1}}`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var m sendMessage
		_ = json.Unmarshal(data, &m)
		f.mu.Lock()
		f.reqs = append(f.reqs, apiRequest{Path: r.URL.Path, ContentType: r.Header.Get("Content-Type"), Body: m})
		status, reply := f.status, f.reply
		f.mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) set(status int, reply string) {
	f.mu.Lock()
	f.status, f.reply = status, reply
	f.mu.Unlock()
}

func (f *fakeAPI) requests() []apiRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiRequest(nil), f.reqs...)
}

func (f *fakeAPI) texts() []string {
	var out []string
	for _, r := range f.requests() {
		out = append(out, r.Body.Text)
	}
	return out
}

func newTestTelegram(t *testing.T, api *fakeAPI, clk *fakeClock, events ...string) *Telegram {
	t.Helper()
	tg, err := NewTelegram(TelegramOptions{
		Token:   testToken,
		ChatID:  "-1001234567890",
		Events:  events,
		Senders: []Sender{HTTPSender{Client: api.srv.Client()}},
		Now:     clk.Now,
		APIBase: api.srv.URL + "/",
		Hub:     "ir-1",
	})
	require.NoError(t, err)
	return tg
}

func switchEvent(tunnel string) state.Event {
	return state.Event{
		At: at, Level: state.LevelWarn, Type: state.EvSwitchTransport, Tunnel: tunnel, Node: "de-1",
		FromNode: "de-1", ToNode: "de-1", FromTransport: "backhaul/tcpmux", ToTransport: "backhaul/wssmux",
		Reason: "probe failed 3x (timeout)", Message: "switched",
	}
}

func TestNotifySendsOneMessage(t *testing.T) {
	api := newFakeAPI(t)
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk)

	require.NoError(t, tg.Notify(context.Background(), switchEvent("main")))
	reqs := api.requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "/bot"+testToken+"/sendMessage", reqs[0].Path)
	assert.Equal(t, "application/json", reqs[0].ContentType)
	assert.Equal(t, "-1001234567890", reqs[0].Body.ChatID)
	assert.True(t, reqs[0].Body.DisableWebPagePreview)
	assert.Equal(t, "DEYROUTE ir-1 · main · switch_transport\nbackhaul/tcpmux -> backhaul/wssmux\nreason: probe failed 3x (timeout)\n2026-09-29 12:41:03 UTC", reqs[0].Body.Text)
}

func TestNotifyFiltersEvents(t *testing.T) {
	api := newFakeAPI(t)
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk) // defaults: down, switch, failback, node_offline
	assert.True(t, tg.Selected(state.EvSwitchNode))
	assert.False(t, tg.Selected(state.EvTunnelUp))
	require.NoError(t, tg.Notify(context.Background(), state.Event{Type: state.EvTunnelUp, Tunnel: "main"}))
	require.NoError(t, tg.Notify(context.Background(), state.Event{Type: state.EvProbeError, Tunnel: "main"}))
	assert.Empty(t, api.requests())

	tg2 := newTestTelegram(t, api, clk, "up")
	require.NoError(t, tg2.Notify(context.Background(), state.Event{Type: state.EvTunnelUp, Tunnel: "main"}))
	require.NoError(t, tg2.Notify(context.Background(), switchEvent("main")))
	assert.Len(t, api.requests(), 1)
}

func TestNotifyRateLimit(t *testing.T) {
	api := newFakeAPI(t)
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk)
	ctx := context.Background()

	require.NoError(t, tg.Notify(ctx, switchEvent("main")))
	clk.Advance(10 * time.Second)
	require.NoError(t, tg.Notify(ctx, switchEvent("main")))
	clk.Advance(10 * time.Second)
	require.NoError(t, tg.Notify(ctx, switchEvent("main")))
	assert.Len(t, api.requests(), 1, "one message per 60 s per (tunnel, type)")
	assert.Equal(t, 2, tg.Suppressed("main", state.EvSwitchTransport))

	// Other keys are independent.
	require.NoError(t, tg.Notify(ctx, switchEvent("backup")))
	require.NoError(t, tg.Notify(ctx, state.Event{Type: state.EvTunnelDown, Tunnel: "main", At: at}))
	require.NoError(t, tg.Notify(ctx, state.Event{Type: state.EvNodeOffline, Node: "de-1", At: at}))
	require.NoError(t, tg.Notify(ctx, state.Event{Type: state.EvNodeOffline, Node: "nl-1", At: at}))
	require.NoError(t, tg.Notify(ctx, state.Event{Type: state.EvNodeOffline, Node: "nl-1", At: at}))
	assert.Len(t, api.requests(), 5)
	assert.Equal(t, 1, tg.Suppressed("nl-1", state.EvNodeOffline))
	assert.Equal(t, 0, tg.Suppressed("nobody", state.EvNodeOffline))

	clk.Advance(41 * time.Second) // 61 s after the first message
	require.NoError(t, tg.Notify(ctx, switchEvent("main")))
	texts := api.texts()
	require.Len(t, texts, 6)
	assert.True(t, strings.HasPrefix(texts[5], "DEYROUTE ir-1 · main · switch_transport (+2 suppressed)\n"), texts[5])
	assert.Equal(t, 0, tg.Suppressed("main", state.EvSwitchTransport))

	// A clock that jumps backwards does not silence a key forever.
	clk.Advance(-time.Hour)
	require.NoError(t, tg.Notify(ctx, switchEvent("main")))
	assert.Len(t, api.requests(), 7)
}

func TestNotifyFailureCountsAsSuppressed(t *testing.T) {
	api := newFakeAPI(t)
	api.set(http.StatusBadGateway, "<html>bad gateway</html>")
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk)
	ctx := context.Background()

	err := tg.Notify(ctx, switchEvent("main"))
	require.Error(t, err)
	assert.True(t, deyerr.HasCode(err, deyerr.X050))
	assert.Contains(t, deyerr.As(err).Why(), "HTTP 502")
	assert.NotContains(t, deyerr.As(err).Format(true), testToken)

	api.set(http.StatusOK, `{"ok":true}`)
	require.NoError(t, tg.Notify(ctx, switchEvent("main")), "rate limited: dropped")
	assert.Len(t, api.requests(), 1)
	clk.Advance(time.Minute)
	require.NoError(t, tg.Notify(ctx, switchEvent("main")))
	texts := api.texts()
	require.Len(t, texts, 2)
	assert.Contains(t, texts[1], "(+2 suppressed)")
}

func TestSendErrors(t *testing.T) {
	api := newFakeAPI(t)
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk)
	ctx := context.Background()

	cases := []struct {
		status int
		reply  string
		code   deyerr.Code
		text   string
	}{
		{401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, deyerr.C050, "Unauthorized"},
		{404, `{"ok":false,"error_code":404,"description":"Not Found"}`, deyerr.C050, "Not Found"},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, deyerr.C050, "chat not found"},
		{403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked"}`, deyerr.C050, "kicked"},
		{200, `{"ok":false,"description":"strange"}`, deyerr.C050, "strange"},
		{429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5"}`, deyerr.X050, "HTTP 429: Too Many Requests"},
		{500, ``, deyerr.X050, "no description"},
		{503, `upstream ` + testToken + ` down`, deyerr.X050, "upstream *** down"},
	}
	for _, c := range cases {
		api.set(c.status, c.reply)
		err := tg.Test(ctx)
		require.Error(t, err, c.status)
		assert.True(t, deyerr.HasCode(err, c.code), "%d: %v", c.status, err)
		e := deyerr.As(err)
		full := e.Error() + e.Format(true)
		assert.Contains(t, full, c.text)
		assert.NotContains(t, full, testToken)
		assert.NotContains(t, full, "AAEhBP0av28s")
	}

	// 2xx with a body that is not JSON counts as delivered.
	api.set(http.StatusOK, "ok")
	assert.NoError(t, tg.Test(ctx))
}

func TestSendNetworkErrorHidesToken(t *testing.T) {
	api := newFakeAPI(t)
	base := api.srv.URL
	api.srv.Close()
	tg, err := NewTelegram(TelegramOptions{Token: testToken, ChatID: "42", APIBase: base})
	require.NoError(t, err)
	err = tg.Test(context.Background())
	require.Error(t, err)
	assert.True(t, deyerr.HasCode(err, deyerr.X050))
	e := deyerr.As(err)
	full := e.Error() + e.Format(false)
	assert.NotContains(t, full, testToken)
	assert.NotContains(t, full, "sendMessage")
	assert.Nil(t, errors.Unwrap(err), "no cause that could carry the URL")

	// A sender (e.g. the node relay) that echoes the URL in its error.
	leaky := senderFunc(func(_ context.Context, u, _ string, _ []byte) (int, []byte, error) {
		return 0, nil, fmt.Errorf("node de-1: POST %s: dial timeout", u)
	})
	tg, err = NewTelegram(TelegramOptions{Token: testToken, ChatID: "42", Senders: []Sender{leaky}})
	require.NoError(t, err)
	err = tg.Test(context.Background())
	require.Error(t, err)
	why := deyerr.As(err).Why()
	assert.NotContains(t, why, testToken)
	assert.Contains(t, why, "https://api.telegram.org/bot***/sendMessage")
}

func TestTestMessage(t *testing.T) {
	api := newFakeAPI(t)
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk, "down")
	require.NoError(t, tg.Test(context.Background()))
	assert.Equal(t, []string{"DEYROUTE test message\nhub: ir-1\n2026-09-29 12:41:03 UTC"}, api.texts())

	// Without a hub name.
	var got []byte
	rec := senderFunc(func(_ context.Context, _, _ string, body []byte) (int, []byte, error) {
		got = body
		return 200, []byte(`{"ok":true}`), nil
	})
	tg, err := NewTelegram(TelegramOptions{Token: testToken, ChatID: "@deyroute_alerts", Senders: []Sender{rec}, Now: clk.Now})
	require.NoError(t, err)
	require.NoError(t, tg.Test(context.Background()))
	var m sendMessage
	require.NoError(t, json.Unmarshal(got, &m))
	assert.Equal(t, "DEYROUTE test message\n2026-09-29 12:41:03 UTC", m.Text)
	assert.Equal(t, "@deyroute_alerts", m.ChatID)
}

func TestNewTelegramValidation(t *testing.T) {
	for _, tok := range []string{"", "abc", "123:abc def", "123/abc", "123:abc?x=1"} {
		_, err := NewTelegram(TelegramOptions{Token: tok, ChatID: "42"})
		require.Error(t, err, tok)
		assert.True(t, deyerr.HasCode(err, deyerr.C013))
		assert.Contains(t, err.Error(), "bot_token_file")
	}
	for _, chat := range []string{"", "abc", "12 34", "@x"} {
		_, err := NewTelegram(TelegramOptions{Token: testToken, ChatID: chat})
		require.Error(t, err, chat)
		assert.True(t, deyerr.HasCode(err, deyerr.C013))
		assert.Contains(t, err.Error(), "chat_id")
	}
	_, err := NewTelegram(TelegramOptions{Token: testToken, ChatID: "42", Events: []string{"sometimes"}})
	assert.True(t, deyerr.HasCode(err, deyerr.C013))

	// A token with a trailing newline (token file) is accepted and
	// registered with the log redactor.
	tg, err := NewTelegram(TelegramOptions{Token: testToken + "\n", ChatID: " 42 "})
	require.NoError(t, err)
	assert.Equal(t, DefaultAPIBase, tg.apiBase)
	assert.Equal(t, DefaultRateLimit, tg.rate)
	assert.Equal(t, "x *** y", log.Redact("x "+testToken+" y"))
}

func TestTelegramFormatMethod(t *testing.T) {
	api := newFakeAPI(t)
	clk := &fakeClock{now: at}
	tg := newTestTelegram(t, api, clk)
	e := state.Event{Type: state.EvTunnelDown, Tunnel: "main", Reason: "bot " + testToken + " leaked"}
	s := tg.Format(e)
	assert.NotContains(t, s, testToken)
	assert.True(t, strings.HasPrefix(s, "DEYROUTE ir-1 · main · tunnel_down\n"), s)
	assert.True(t, strings.HasSuffix(s, "2026-09-29 12:41:03 UTC"), "missing time comes from the clock")
}

func TestRateLimitPruning(t *testing.T) {
	clk := &fakeClock{now: at}
	var mu sync.Mutex
	sent := 0
	rec := senderFunc(func(context.Context, string, string, []byte) (int, []byte, error) {
		mu.Lock()
		sent++
		mu.Unlock()
		return 200, nil, nil
	})
	tg, err := NewTelegram(TelegramOptions{Token: testToken, ChatID: "42", Senders: []Sender{rec}, Now: clk.Now, RateLimit: time.Second})
	require.NoError(t, err)
	for i := 0; i < maxLimitKeys+10; i++ {
		require.NoError(t, tg.Notify(context.Background(), state.Event{Type: state.EvTunnelDown, Tunnel: fmt.Sprintf("t%d", i)}))
	}
	clk.Advance(2 * time.Second)
	require.NoError(t, tg.Notify(context.Background(), state.Event{Type: state.EvTunnelDown, Tunnel: "fresh"}))
	tg.mu.Lock()
	n := len(tg.limits)
	tg.mu.Unlock()
	assert.Less(t, n, maxLimitKeys, "idle keys are pruned")
	assert.Equal(t, maxLimitKeys+11, sent)
}

func TestNotifyConcurrent(t *testing.T) {
	clk := &fakeClock{now: at}
	var mu sync.Mutex
	sent := 0
	rec := senderFunc(func(context.Context, string, string, []byte) (int, []byte, error) {
		mu.Lock()
		sent++
		mu.Unlock()
		return 200, nil, nil
	})
	tg, err := NewTelegram(TelegramOptions{Token: testToken, ChatID: "42", Senders: []Sender{rec}, Now: clk.Now})
	require.NoError(t, err)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = tg.Notify(context.Background(), switchEvent("main"))
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, sent)
	assert.Equal(t, 16*20-1, tg.Suppressed("main", state.EvSwitchTransport))
}
