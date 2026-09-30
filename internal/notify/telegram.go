package notify

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// Telegram defaults.
const (
	// DefaultAPIBase is the Telegram Bot API endpoint.
	DefaultAPIBase = "https://api.telegram.org"
	// DefaultRateLimit is the minimum spacing of messages per (tunnel, type).
	DefaultRateLimit = 60 * time.Second
	// TestMessage is the first line of the message sent by Test.
	TestMessage = "DEYROUTE test message"
	// sendTimeout bounds one delivery across every sender of the chain.
	sendTimeout = 60 * time.Second
	// maxLimitKeys triggers pruning of idle rate-limit entries.
	maxLimitKeys = 4096
)

var (
	tokenRe  = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
	chatIDRe = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]{4,64})$`)
)

// TelegramOptions configures NewTelegram.
type TelegramOptions struct {
	// Token is the bot token (content of hub.notify.telegram.bot_token_file).
	Token string
	// ChatID is hub.notify.telegram.chat_id (numeric id or @channel).
	ChatID string
	// Events are aliases or event names; empty selects DefaultEvents.
	Events []string
	// Senders are tried in order until one gets a 2xx (direct, then via a
	// node). Empty: a direct HTTPSender.
	Senders []Sender
	// Now is the clock of the rate limiter (time.Now when nil).
	Now func() time.Time
	// APIBase is the Bot API base URL (DefaultAPIBase when empty).
	APIBase string
	// Hub is the hub name shown in every message.
	Hub string
	// RateLimit overrides DefaultRateLimit (tests).
	RateLimit time.Duration
}

// Telegram sends event notifications to one chat. It is safe for
// concurrent use.
type Telegram struct {
	token   string
	chatID  string
	hub     string
	apiBase string
	events  map[string]bool
	sender  Sender
	now     func() time.Time
	rate    time.Duration

	mu     sync.Mutex
	limits map[string]*limitState
}

// limitState is the rate limiter of one (tunnel, type) key.
type limitState struct {
	last       time.Time
	suppressed int
}

// NewTelegram validates o and returns a notifier. Invalid settings are
// DEY-C013 (token and chat id format, unknown event names). The token is
// registered with the log redactor.
func NewTelegram(o TelegramOptions) (*Telegram, error) {
	token := strings.TrimSpace(o.Token)
	if !tokenRe.MatchString(token) {
		return nil, deyerr.New(deyerr.C013, deyerr.Params{
			"field":   "hub.notify.telegram.bot_token_file",
			"value":   log.Mask,
			"allowed": "a bot token from @BotFather (digits:letters)",
		})
	}
	log.RegisterSecret(token)
	chatID := strings.TrimSpace(o.ChatID)
	if !chatIDRe.MatchString(chatID) {
		return nil, deyerr.New(deyerr.C013, deyerr.Params{
			"field":   "hub.notify.telegram.chat_id",
			"value":   chatID,
			"allowed": "a numeric chat id (e.g. 123456789 or -1001234567890) or @channelname",
		})
	}
	events, err := Resolve(o.Events)
	if err != nil {
		return nil, err
	}
	var sender Sender = HTTPSender{}
	switch len(o.Senders) {
	case 0:
	case 1:
		sender = o.Senders[0]
	default:
		sender = ChainSender{Senders: append([]Sender(nil), o.Senders...)}
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	base := strings.TrimRight(strings.TrimSpace(o.APIBase), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	rate := o.RateLimit
	if rate <= 0 {
		rate = DefaultRateLimit
	}
	return &Telegram{
		token:   token,
		chatID:  chatID,
		hub:     strings.TrimSpace(o.Hub),
		apiBase: base,
		events:  events,
		sender:  sender,
		now:     now,
		rate:    rate,
		limits:  make(map[string]*limitState),
	}, nil
}

// Selected reports whether events of type typ are sent.
func (t *Telegram) Selected(typ string) bool { return t.events[typ] }

// Format renders e as Notify would send it (with the hub name).
func (t *Telegram) Format(e state.Event) string {
	return t.redact(format(t.hub, t.stamp(e), 0))
}

// Notify sends one message for e when its type is selected and the
// (tunnel, type) key sent nothing in the last 60 seconds. Rate-limited
// events are dropped, not queued; the next message of the same key says
// "(+N suppressed)". Unselected and suppressed events return nil. Delivery
// failures are DEY-X050 (unreachable) or DEY-C050 (rejected by Telegram);
// they never contain the bot token.
func (t *Telegram) Notify(ctx context.Context, e state.Event) error {
	if !t.events[e.Type] {
		return nil
	}
	e = t.stamp(e)
	key := rateKey(e)
	now := t.now()

	t.mu.Lock()
	st := t.limits[key]
	if st == nil {
		t.pruneLocked(now)
		st = &limitState{}
		t.limits[key] = st
	}
	if d := now.Sub(st.last); !st.last.IsZero() && d >= 0 && d < t.rate {
		st.suppressed++
		t.mu.Unlock()
		return nil
	}
	suppressed := st.suppressed
	st.last, st.suppressed = now, 0
	t.mu.Unlock()

	if err := t.Send(ctx, format(t.hub, e, suppressed)); err != nil {
		// The attempt still counts for the rate limit (no hammering an
		// unreachable API); the lost message is reported with the next one.
		t.mu.Lock()
		st.suppressed += suppressed + 1
		t.mu.Unlock()
		return err
	}
	return nil
}

// Suppressed returns how many events of the (tunnel, type) key were dropped
// since its last message. For node events without a tunnel pass the node
// id as tunnel.
func (t *Telegram) Suppressed(tunnel, typ string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range []string{tunnel + "\x00" + typ, "node:" + tunnel + "\x00" + typ} {
		if st, ok := t.limits[k]; ok {
			return st.suppressed
		}
	}
	return 0
}

// Test sends TestMessage (menu "Notifications → test message", `deyroute
// notify telegram test`), bypassing the event filter and the rate limit.
func (t *Telegram) Test(ctx context.Context) error {
	lines := []string{TestMessage}
	if t.hub != "" {
		lines = append(lines, "hub: "+clean(t.hub))
	}
	lines = append(lines, t.now().UTC().Format("2006-01-02 15:04:05")+" UTC")
	return t.Send(ctx, strings.Join(lines, "\n"))
}

// sendMessage is the Bot API sendMessage request body.
type sendMessage struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// apiResponse is the part of a Bot API response we read.
type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

// Send posts text as one plain-text message (sendMessage).
func (t *Telegram) Send(ctx context.Context, text string) error {
	body, err := json.Marshal(sendMessage{ChatID: t.chatID, Text: t.redact(text), DisableWebPagePreview: true})
	if err != nil {
		return deyerr.New(deyerr.X050, deyerr.Params{"reason": "encode: " + t.redact(err.Error())})
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	status, resp, err := t.sender.Post(ctx, t.apiBase+"/bot"+t.token+"/sendMessage", "application/json", body)
	if err != nil {
		return deyerr.New(deyerr.X050, deyerr.Params{"reason": t.redact(err.Error())})
	}
	var ar apiResponse
	parsed := json.Unmarshal(resp, &ar) == nil
	if status >= 200 && status < 300 && (!parsed || ar.OK) {
		return nil
	}
	desc := clean(ar.Description)
	if desc == "" {
		desc = clean(string(resp))
	}
	if desc == "" {
		desc = "no description"
	}
	desc = t.redact(desc)
	switch status {
	case 400, 401, 403, 404:
		// Wrong token (401/404), unknown chat (400) or the bot was removed
		// from the chat (403): the owner's settings need fixing.
		return deyerr.New(deyerr.C050, deyerr.Params{"status": status, "reason": desc})
	default:
		if status >= 200 && status < 300 {
			return deyerr.New(deyerr.C050, deyerr.Params{"status": status, "reason": desc})
		}
		return deyerr.New(deyerr.X050, deyerr.Params{"reason": "HTTP " + strconv.Itoa(status) + ": " + desc})
	}
}

// redact removes the bot token and every known secret pattern from s.
func (t *Telegram) redact(s string) string {
	if t.token != "" {
		s = strings.ReplaceAll(s, t.token, log.Mask)
	}
	return log.Redact(s)
}

// stamp fills a missing event time from the clock.
func (t *Telegram) stamp(e state.Event) state.Event {
	if e.At.IsZero() {
		e.At = t.now()
	}
	return e
}

// pruneLocked drops idle rate-limit entries when there are too many.
func (t *Telegram) pruneLocked(now time.Time) {
	if len(t.limits) < maxLimitKeys {
		return
	}
	for k, st := range t.limits {
		if st.suppressed == 0 && now.Sub(st.last) >= t.rate {
			delete(t.limits, k)
		}
	}
}

// rateKey is the (tunnel, type) key; node events without a tunnel are keyed
// by node so two nodes going offline are both reported.
func rateKey(e state.Event) string {
	scope := e.Tunnel
	if scope == "" && e.Node != "" {
		scope = "node:" + e.Node
	}
	return scope + "\x00" + e.Type
}
