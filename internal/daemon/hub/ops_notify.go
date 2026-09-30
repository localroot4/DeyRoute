package hub

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/notify"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// NotifyTelegramSet implements api.Local (`deyroute notify telegram set
// --token-file F --chat-id C`, section 9): the bot token is read from F
// (an absolute path; the CLI resolves it), the token, chat id and event
// list are validated, the token is copied to
// /etc/deyroute/secrets/telegram.token (0600; F may be deleted afterwards)
// and hub.notify.telegram is enabled. events nil keeps the configured list
// (default: down, switch, failback, node_offline).
func (l *local) NotifyTelegramSet(_ context.Context, tokenFile, chatID string, events []string) error {
	h := l.h
	tokenFile = strings.TrimSpace(tokenFile)
	if tokenFile == "" || !filepath.IsAbs(tokenFile) {
		return withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "--token-file", "value": tokenFile, "allowed": "the absolute path of a file holding the bot token",
		}))
	}
	token, err := secrets.TelegramToken(h.path(tokenFile))
	if err != nil {
		return withLog(err)
	}
	dlog.RegisterSecret(token)
	chatID = strings.TrimSpace(chatID)
	var evs []string
	for _, e := range events {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			evs = append(evs, e)
		}
	}
	if err := notify.Validate(evs); err != nil {
		return withLog(err)
	}
	// NewTelegram checks the token and chat id formats (DEY-C013).
	if _, err := notify.NewTelegram(notify.TelegramOptions{Token: token, ChatID: chatID, Events: evs}); err != nil {
		return withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	dst := config.DefaultTelegramTokenFile
	if filepath.Clean(tokenFile) != dst {
		if err := tlsutil.WriteSecret(h.path(dst), []byte(token+"\n")); err != nil {
			return withLog(err)
		}
	}
	cfg, err := h.mutate(func(c *config.Config) error {
		tg := &c.Hub.Notify.Telegram
		tg.Enabled = true
		tg.BotTokenFile = dst
		tg.ChatID = chatID
		if evs != nil {
			tg.Events = evs
		}
		return nil
	})
	if err != nil {
		return withLog(err)
	}
	h.reloadNotifier(cfg)
	h.log.Info("Telegram notifications enabled", slog.String("events", strings.Join(cfg.Hub.Notify.Telegram.Events, ",")))
	return nil
}

// NotifyTelegramTest implements api.Local (`deyroute notify telegram test`):
// "DEYROUTE test message" is posted directly and, when that fails, through
// an online node (QUESTIONS.md C.18). Settings that are saved but disabled
// can be tested too.
func (l *local) NotifyTelegramTest(ctx context.Context) error {
	h := l.h
	t := h.Notifier()
	if t == nil {
		cfg := config.Clone(h.Config())
		tg := &cfg.Hub.Notify.Telegram
		if strings.TrimSpace(tg.ChatID) == "" {
			return withLog(deyerr.New(deyerr.C013, deyerr.Params{
				"field": "hub.notify.telegram", "value": "not configured", "allowed": "a bot token and chat id",
			}).WithFix("set them first: deyroute notify telegram set --token-file F --chat-id C"))
		}
		tg.Enabled = true
		built, err := h.buildNotifier(cfg)
		if err != nil {
			return withLog(err)
		}
		t = built
	}
	tctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	if err := t.Test(tctx); err != nil {
		return withLog(err)
	}
	h.log.Info("Telegram test message sent")
	return nil
}

// NotifyTelegramOff implements api.Local (`deyroute notify telegram off`):
// notifications stop; the token file and chat id are kept.
func (l *local) NotifyTelegramOff(context.Context) error {
	h := l.h
	if !h.Config().Hub.Notify.Telegram.Enabled {
		h.reloadNotifier(h.Config())
		return nil
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	cfg, err := h.mutate(func(c *config.Config) error {
		c.Hub.Notify.Telegram.Enabled = false
		return nil
	})
	if err != nil {
		return withLog(err)
	}
	h.reloadNotifier(cfg)
	h.log.Info("Telegram notifications disabled")
	return nil
}
