package hub

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// ConfigApply implements api.Local (section 4: config.yaml is the single
// source of truth; `deyroute config apply`): config.yaml is re-read and
// validated (ids stay immutable), an automatic backup is taken before
// anything changes (section 5), the new configuration becomes active
// (notifier, firewall) and every tunnel is reconciled.
func (l *local) ConfigApply(ctx context.Context, progress func(api.Step)) (api.ApplyResult, error) {
	h := l.h
	rep := &steps{progress: progress}
	res, err := h.applyConfigFile(rep)
	if err != nil {
		return api.ApplyResult{}, withLog(err)
	}
	if cfg := h.Config(); h.ctlPort != 0 && cfg.Hub.ControlPort != h.ctlPort {
		res.Warnings = append(res.Warnings, h.announcePort(ctx, cfg))
	}
	// Reconcile outside the config lock: the tunnel controller mutates
	// config.yaml itself.
	if err := rep.run(stepReconcile, func() (string, error) { return "", h.reconcileAll(ctx) }); err != nil {
		res.Warnings = append(res.Warnings, deyerr.As(err).Error())
	}
	msg := "configuration applied"
	if len(res.Changed) > 0 {
		msg += ": " + strings.Join(res.Changed, ", ")
	}
	h.Emit(state.Event{Type: state.EvConfigApplied, Level: state.LevelInfo, Message: msg})
	h.log.Info("configuration applied", slog.String("backup", res.Backup), slog.String("changed", strings.Join(res.Changed, ",")))
	return res, nil
}

// applyConfigFile runs the validate, backup and apply steps of ConfigApply
// under the config lock.
func (h *Hub) applyConfigFile(rep *steps) (api.ApplyResult, error) {
	h.mutMu.Lock()
	defer h.mutMu.Unlock()
	prev := h.Config()
	var next *config.Config
	err := rep.run(stepValidate, func() (string, error) {
		c, err := config.LoadWith(h.cfgPath, h.valOpts)
		if err != nil {
			return "", err
		}
		if c.Role != config.RoleHub || c.Hub == nil {
			return "", deyerr.New(deyerr.C016, deyerr.Params{"role": c.Role})
		}
		if err := config.CheckImmutable(prev, c); err != nil {
			return "", err
		}
		next = c
		return h.cfgPath, nil
	})
	if err != nil {
		return api.ApplyResult{}, err
	}
	res := api.ApplyResult{Changed: diffConfig(prev, next)}
	if res.Changed == nil {
		res.Changed = []string{}
	}
	err = rep.run(stepBackup, func() (string, error) {
		// config.yaml already holds the edit: back up what was running.
		p, err := h.autoBackupOf(prev)
		res.Backup = p
		return p, err
	})
	if err != nil {
		return api.ApplyResult{}, err
	}
	_ = rep.run(stepApply, func() (string, error) {
		h.setConfig(next)
		h.reloadNotifier(next)
		h.requestFirewall()
		if !reflect.DeepEqual(prev.Tuning, next.Tuning) && next.Tuning != nil {
			// Kernel settings change only on the owner's explicit command
			// (section 12).
			res.Warnings = append(res.Warnings, "tuning changed: apply it on the hub and the nodes with deyroute optimize apply --profile "+
				firstNonEmpty(next.Tuning.SysctlProfile, config.SysctlOff))
		}
		if firewallManaged(prev) && !firewallManaged(next) {
			res.Warnings = append(res.Warnings, "security.firewall_managed is false: table inet deyroute is removed and deyroute only suggests firewall commands (deyroute security firewall show)")
		}
		return strings.Join(res.Changed, ", "), nil
	})
	return res, nil
}

// announcePort tells every node the hub's new control address after
// hub.control_port changed (section 5 hub move, same command as
// announce-move). The nodes store it and dial it at their next reconnect,
// which happens when the hub restarts on the new port; until then the hub
// and its firewall stay on the old port. It returns the warning for the
// owner: restart now, open the port at the provider, and run set-hub on
// the nodes that were offline.
func (h *Hub) announcePort(ctx context.Context, cfg *config.Config) string {
	addr := net.JoinHostPort(cfg.Hub.PublicIP, strconv.Itoa(cfg.Hub.ControlPort))
	res, _ := (&local{h: h}).HubAnnounceMove(ctx, addr)
	msg := fmt.Sprintf("hub.control_port changed to %d: allow %d/tcp in the provider firewall, then restart the hub (systemctl restart %s); "+
		"until then it keeps listening on %d", cfg.Hub.ControlPort, cfg.Hub.ControlPort, ServiceName, h.ctlPort)
	if len(res.Accepted) > 0 {
		msg += "; nodes that switch to " + addr + " at the restart: " + strings.Join(res.Accepted, ", ")
	}
	if len(res.Offline) > 0 {
		msg += "; offline nodes, run on each: deyroute node set-hub " + addr + " (" + strings.Join(res.Offline, ", ") + ")"
	}
	return msg
}

// diffConfig lists what changed between two configurations: "hub",
// "nodes/<id>", "tunnels/<id>", "ladders/<name>", "tuning", "security".
func diffConfig(a, b *config.Config) []string {
	var out []string
	if !reflect.DeepEqual(a.Hub, b.Hub) {
		out = append(out, "hub")
	}
	nodes := func(c *config.Config) map[string]config.Node {
		m := map[string]config.Node{}
		for _, n := range c.Nodes {
			m[n.ID] = n
		}
		return m
	}
	tunnels := func(c *config.Config) map[string]config.Tunnel {
		m := map[string]config.Tunnel{}
		for _, t := range c.Tunnels {
			m[t.ID] = t
		}
		return m
	}
	out = append(out, diffMaps("nodes/", nodes(a), nodes(b))...)
	out = append(out, diffMaps("tunnels/", tunnels(a), tunnels(b))...)
	out = append(out, diffMaps("ladders/", a.Ladders, b.Ladders)...)
	if !reflect.DeepEqual(a.Tuning, b.Tuning) {
		out = append(out, "tuning")
	}
	if !reflect.DeepEqual(a.Security, b.Security) {
		out = append(out, "security")
	}
	return out
}

// diffMaps returns prefix+key for every key added, removed or changed.
func diffMaps[V any](prefix string, a, b map[string]V) []string {
	var out []string
	for k, va := range a {
		if vb, ok := b[k]; !ok || !reflect.DeepEqual(va, vb) {
			out = append(out, prefix+k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, prefix+k)
		}
	}
	sort.Strings(out)
	return out
}

// SettingsSet implements api.Local (menu 12 Settings, `deyroute settings`):
// ui_mode, language (one of i18n.Languages), decoy SNIs and the domain.
// Empty fields are unchanged. A changed domain or decoy list re-renders the
// tunnels (TLS SANs, Reality decoys).
func (l *local) SettingsSet(ctx context.Context, req api.SettingsRequest) error {
	h := l.h
	lang := strings.TrimSpace(req.Language)
	if lang != "" && !slices.Contains(i18n.Languages(), lang) {
		return withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "hub.language", "value": lang, "allowed": strings.Join(i18n.Languages(), ", "),
		}))
	}
	mode := strings.TrimSpace(req.UIMode)
	if mode != "" && !slices.Contains(config.UIModes, mode) {
		return withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "hub.ui_mode", "value": mode, "allowed": strings.Join(config.UIModes, ", "),
		}))
	}
	var decoys []string
	for _, d := range req.Decoys {
		d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
		if d != "" && !slices.Contains(decoys, d) {
			decoys = append(decoys, d)
		}
	}
	if mode == "" && lang == "" && len(decoys) == 0 && req.Domain == nil {
		return nil
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	rerender, decoysChanged := false, false
	_, err := h.mutate(func(c *config.Config) error {
		if mode != "" {
			c.Hub.UIMode = mode
		}
		if lang != "" {
			c.Hub.Language = lang
		}
		if len(decoys) > 0 && !slices.Equal(decoys, c.Hub.DecoySNIs) {
			c.Hub.DecoySNIs = decoys
			rerender, decoysChanged = true, true
		}
		if req.Domain != nil {
			d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(*req.Domain), "."))
			if d != c.Hub.Domain {
				c.Hub.Domain = d
				rerender = true
			}
		}
		return nil
	})
	if err != nil {
		return withLog(err)
	}
	h.log.Info("settings changed", slog.String("ui_mode", mode), slog.String("language", lang), slog.Bool("rerender", rerender))
	if decoysChanged {
		// The new list is tested from the hub; the first reachable decoy
		// is used (section 7.4). Until then its first entry is.
		h.requestDecoyCheck()
	}
	if rerender {
		if err := h.reconcileAll(ctx); err != nil {
			h.log.Warn("reconcile after a settings change failed", dlog.Err(err))
			return withLog(err)
		}
	}
	return nil
}
