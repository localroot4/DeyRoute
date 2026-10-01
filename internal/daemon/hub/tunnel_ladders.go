package hub

import (
	"context"
	"log/slog"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// LadderList implements api.Local (`deyroute ladder list`): the builtin
// profiles ("default", "udp-default") and those of ladders:, with the
// tunnels that use each one.
func (l *local) LadderList(context.Context) ([]api.Ladder, error) {
	cfg := l.h.Config()
	names := cfg.LadderNames()
	out := make([]api.Ladder, 0, len(names))
	for _, name := range names {
		rungs, _ := cfg.LadderProfile(name)
		lad := api.Ladder{Name: name, Rungs: rungs, Builtin: config.IsBuiltinLadder(name), UsedBy: cfg.LadderUsers(name)}
		if lad.Rungs == nil {
			lad.Rungs = []string{}
		}
		out = append(out, lad)
	}
	return out, nil
}

// LadderSave implements api.Local (`deyroute ladder create|set`): a named
// profile under ladders: (builtin profiles are read-only, DEY-C022;
// create refuses an existing name, DEY-C002; set needs one, DEY-C012).
// Tunnels that use the profile are re-planned (their engines take the new
// order; a removed active rung moves the tunnel to rung 1).
func (l *local) LadderSave(ctx context.Context, name string, rungs []string, create bool) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	name = strings.TrimSpace(name)
	if !config.ValidID(name) {
		return withLog(deyerr.New(deyerr.C007, deyerr.Params{"kind": "ladder", "id": name}))
	}
	if config.IsBuiltinLadder(name) {
		return withLog(deyerr.New(deyerr.C022, deyerr.Params{"ladder": name}))
	}
	clean := cleanRungs(rungs)
	if len(clean) == 0 {
		return withLog(deyerr.New(deyerr.C013, deyerr.Params{"field": "ladders." + name, "value": "", "allowed": "at least one transport"}))
	}
	for _, r := range clean {
		if _, _, err := backend.Lookup(r); err != nil {
			return withLog(err)
		}
	}
	cfg := h.Config()
	_, exists := cfg.Ladders[name]
	switch {
	case create && exists:
		return withLog(deyerr.New(deyerr.C002, deyerr.Params{"kind": "ladder", "id": name}))
	case !create && !exists:
		return withLog(deyerr.New(deyerr.C012, deyerr.Params{"ladder": name, "tunnel": "-"}))
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	next, err := h.mutate(func(c *config.Config) error {
		if c.Ladders == nil {
			c.Ladders = map[string][]string{}
		}
		c.Ladders[name] = clean
		return nil
	})
	if err != nil {
		return withLog(err)
	}
	h.log.Info("ladder saved", slog.String("ladder", name), slog.String("rungs", strings.Join(clean, ",")))
	for _, id := range next.LadderUsers(name) {
		if c := h.tun.lookup(id); c != nil {
			if err := c.update(ctx, updateOpts{restartActive: true}); err != nil {
				h.log.Warn("tunnel update after a ladder change failed", dlog.Tunnel(id), dlog.Err(err))
			}
		}
	}
	return nil
}

// LadderDelete implements api.Local (`deyroute ladder delete`): builtin
// profiles are read-only (DEY-C022), an unknown name is DEY-C012 and a
// profile a tunnel uses cannot go (DEY-C023).
func (l *local) LadderDelete(ctx context.Context, name string) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	name = strings.TrimSpace(name)
	if config.IsBuiltinLadder(name) {
		return withLog(deyerr.New(deyerr.C022, deyerr.Params{"ladder": name}))
	}
	cfg := h.Config()
	if _, ok := cfg.Ladders[name]; !ok {
		return withLog(deyerr.New(deyerr.C012, deyerr.Params{"ladder": name, "tunnel": "-"}))
	}
	if users := cfg.LadderUsers(name); len(users) > 0 {
		return withLog(deyerr.New(deyerr.C023, deyerr.Params{"ladder": name, "tunnels": strings.Join(users, ", ")}))
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		delete(c.Ladders, name)
		return nil
	}); err != nil {
		return withLog(err)
	}
	h.log.Info("ladder deleted", slog.String("ladder", name))
	return nil
}

// TransportList implements api.Local: every registered transport with its
// direction, protocols, UDP/TLS needs, stealth, whether the service sees
// the client IP (section 10: client IP preserved / masked), whether it is
// optional and the pinned backend version.
func (l *local) TransportList(context.Context) ([]api.TransportInfo, error) {
	trs := backend.AllTransports()
	out := make([]api.TransportInfo, 0, len(trs))
	for _, tr := range trs {
		ti := api.TransportInfo{
			ID:                tr.ID(),
			Backend:           tr.Backend,
			Direction:         tr.Direction.String(),
			Protos:            append([]string{}, tr.Protos...),
			NeedsUDP:          tr.NeedsUDP,
			NeedsTLS:          tr.NeedsTLS,
			Stealth:           tr.Stealth,
			ClientIPPreserved: tr.ClientIPPreserved,
			Optional:          tr.Optional,
		}
		if e, ok := l.h.backendEntry(tr.Backend); ok {
			ti.Version = e.Version
		}
		out = append(out, ti)
	}
	return out, nil
}
