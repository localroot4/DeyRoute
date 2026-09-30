package hub

import (
	"context"
	"log/slog"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/notify"
	"github.com/localroot4/deyroute/internal/state"
)

// Event bus limits.
const (
	// notifyQueueSize bounds events waiting for the notifier; when it is
	// full (Telegram slow or unreachable) new events are not queued rather
	// than blocking the failover engines.
	notifyQueueSize = 256
	// subscriberBuffer is the channel size of a Subscribe call without an
	// explicit buffer.
	subscriberBuffer = 64
	// notifyTimeout bounds one notification (direct, then via a node).
	notifyTimeout = time.Minute
)

// Emit publishes an event (section 9): it is appended to the events ring
// in state.db (Seq and UTC time assigned), written as one JSON line to
// events.log (redacted), handed to the Telegram notifier when enabled (one
// message per event, rate-limited per (tunnel, type)) and delivered to
// every subscriber. Emit never blocks for long: it is called from the
// failover engines.
func (h *Hub) Emit(e state.Event) {
	if e.At.IsZero() {
		e.At = h.now()
	}
	e.At = e.At.UTC()
	if e.Level == "" {
		e.Level = state.LevelInfo
	}
	if e.Message == "" {
		e.Message = e.Type
	}
	saved, err := h.st.AppendEvent(e)
	if err != nil {
		h.log.Error("cannot store an event", slog.String("event", e.Type), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		saved = e
	}
	h.logEvent(saved)
	h.publish(saved)
	if h.notifier.Load() != nil {
		select {
		case h.notifyQ <- saved:
		default:
			h.log.Warn("notification queue full; event not sent to Telegram", slog.String("event", saved.Type))
		}
	}
}

// logEvent writes one events.log line with the section 13 fields.
func (h *Hub) logEvent(e state.Event) {
	attrs := []slog.Attr{slog.String("event", e.Type), slog.Uint64("seq", e.Seq)}
	add := func(key, v string) {
		if v != "" {
			attrs = append(attrs, slog.String(key, v))
		}
	}
	add(dlog.KeyTunnel, e.Tunnel)
	add(dlog.KeyNode, e.Node)
	if e.ToTransport != "" {
		add(dlog.KeyTransport, e.ToTransport)
	}
	add("from_transport", e.FromTransport)
	add("to_transport", e.ToTransport)
	add("from_node", e.FromNode)
	add("to_node", e.ToNode)
	add("reason", e.Reason)
	add(dlog.KeyCode, e.Code)
	h.evLog.LogAttrs(context.Background(), levelOf(e.Level), e.Message, attrs...)
}

// levelOf maps an event level to a slog level.
func levelOf(l string) slog.Level {
	switch l {
	case state.LevelError:
		return slog.LevelError
	case state.LevelWarn:
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// Subscribe returns a channel receiving every event emitted from now on
// (buf <= 0 means 64). A subscriber that falls behind loses events rather
// than slowing the hub. cancel ends the subscription and closes the
// channel; the channel is also closed when the hub stops.
func (h *Hub) Subscribe(buf int) (<-chan state.Event, func()) {
	if buf <= 0 {
		buf = subscriberBuffer
	}
	ch := make(chan state.Event, buf)
	h.subMu.Lock()
	if h.subs == nil {
		// The hub already stopped.
		h.subMu.Unlock()
		close(ch)
		return ch, func() {}
	}
	id := h.nextSub
	h.nextSub++
	h.subs[id] = ch
	h.subMu.Unlock()
	return ch, func() {
		h.subMu.Lock()
		defer h.subMu.Unlock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
	}
}

// publish delivers e to every subscriber without blocking.
func (h *Hub) publish(e state.Event) {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// closeSubscribers ends every subscription (hub stopping).
func (h *Hub) closeSubscribers() {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	for id, ch := range h.subs {
		close(ch)
		delete(h.subs, id)
	}
	h.subs = nil
}

// notifyLoop sends queued events to Telegram until ctx ends.
func (h *Hub) notifyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-h.notifyQ:
			t := h.notifier.Load()
			if t == nil {
				continue
			}
			nctx, cancel := context.WithTimeout(ctx, notifyTimeout)
			err := t.Notify(nctx, e)
			cancel()
			if err != nil && ctx.Err() == nil {
				h.log.Warn("Telegram notification failed", slog.String("event", e.Type),
					dlog.Err(err), dlog.Code(deyerr.As(err).Code))
			}
		}
	}
}

// reloadNotifier builds the Telegram notifier from hub.notify.telegram
// (nil when disabled or unusable). Messages are posted directly first and
// through an online node second (QUESTIONS.md C.18).
func (h *Hub) reloadNotifier(cfg *config.Config) {
	t, err := h.buildNotifier(cfg)
	if err != nil {
		h.log.Error("Telegram notifications are off: the settings cannot be used",
			dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	h.notifier.Store(t)
}

func (h *Hub) buildNotifier(cfg *config.Config) (*notify.Telegram, error) {
	if cfg == nil || cfg.Hub == nil || !cfg.Hub.Notify.Telegram.Enabled {
		return nil, nil
	}
	tg := cfg.Hub.Notify.Telegram
	file := tg.BotTokenFile
	if file == "" {
		file = config.DefaultTelegramTokenFile
	}
	token, err := secrets.TelegramToken(h.path(file))
	if err != nil {
		return nil, err
	}
	return notify.NewTelegram(notify.TelegramOptions{
		Token:   token,
		ChatID:  tg.ChatID,
		Events:  tg.Events,
		Senders: []notify.Sender{notify.HTTPSender{}, nodeSender{h}},
		Now:     h.o.Now,
		APIBase: h.o.TelegramAPIBase,
		Hub:     cfg.Hub.Name,
	})
}

// Notifier returns the active Telegram notifier (nil when off).
func (h *Hub) Notifier() *notify.Telegram { return h.notifier.Load() }

// nodeSender posts through an online node (http.post), for hubs that
// cannot reach api.telegram.org directly.
type nodeSender struct{ h *Hub }

// Post implements notify.Sender.
func (s nodeSender) Post(ctx context.Context, url, contentType string, body []byte) (int, []byte, error) {
	nodes := s.h.onlineNodes()
	if len(nodes) == 0 {
		return 0, nil, deyerr.New(deyerr.N012, nil)
	}
	var last error
	for _, node := range nodes {
		var res api.HTTPPostResult
		err := s.h.Call(ctx, node, api.CmdHTTPPost, api.HTTPPostArgs{URL: url, ContentType: contentType, Body: body}, &res)
		if err == nil {
			return res.Status, []byte(res.Body), nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return 0, nil, last
}
