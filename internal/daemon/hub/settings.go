package hub

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
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
	// The front follows hub.front at once (enable, disable, a new port), and
	// a front that could not start (DEY-X053) is tried again here.
	h.reloadFront(h.Config())
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
		if !slices.Equal(prev.Hub.DecoySNIs, next.Hub.DecoySNIs) {
			// An edited decoy list is tested at once, as one changed in
			// Settings (section 7.4): the first reachable decoy is used.
			h.requestDecoyCheck()
		}
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
	if len(res.Front) > 0 {
		msg += "; front: unchanged (" + strings.Join(res.Front, ", ") + "): they reach the hub through the CDN front, not this address"
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

// SettingsSet implements api.Local (menu 12 Settings and 8 Security > TLS
// certificates, `deyroute settings`, `deyroute security tls domain|acme`):
// ui_mode, language (one of i18n.Languages), decoy SNIs, the domain and the
// ACME options of section 10 (e-mail, Cloudflare token file for DNS-01).
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
	acme, err := h.acmeRequest(req)
	if err != nil {
		return withLog(err)
	}
	if mode == "" && lang == "" && len(decoys) == 0 && !acme.any() {
		return nil
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	var commit func() error
	if acme.token != "" && acme.tokenSrc != config.DefaultCloudflareTokenFile {
		// The token is copied before config.yaml points at it, once the
		// change was accepted: a refused change keeps the stored token.
		commit = func() error {
			return tlsutil.WriteSecret(h.path(config.DefaultCloudflareTokenFile), []byte(acme.token+"\n"))
		}
	}
	rerender, decoysChanged, prevTokenFile := false, false, ""
	_, err = h.mutateCommit(func(c *config.Config) error {
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
		if acme.domain != nil && *acme.domain != c.Hub.Domain {
			if *acme.domain == "" {
				if err := acmeTunnelsNeedDomain(c); err != nil {
					return err
				}
			}
			c.Hub.Domain = *acme.domain
			rerender = true
		}
		if acme.email != nil || acme.tokenSet {
			var a config.ACME
			if c.Hub.ACME != nil {
				a = *c.Hub.ACME
			}
			prevTokenFile = a.CloudflareTokenFile
			if acme.email != nil {
				a.Email = *acme.email
			}
			if acme.tokenSet {
				a.CloudflareTokenFile = ""
				if acme.token != "" {
					a.CloudflareTokenFile = config.DefaultCloudflareTokenFile
				}
			}
			c.Hub.ACME = nil
			if a != (config.ACME{}) {
				c.Hub.ACME = &a
			}
		}
		return nil
	}, commit)
	if err != nil {
		return withLog(err)
	}
	if acme.tokenSet && acme.token == "" && prevTokenFile == config.DefaultCloudflareTokenFile {
		// Removed from the configuration: the stored copy goes too.
		if err := os.Remove(h.path(prevTokenFile)); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
			h.log.Warn("cannot remove the Cloudflare token file", slog.String("path", prevTokenFile), dlog.Err(err))
		}
	}
	h.log.Info("settings changed", slog.String("ui_mode", mode), slog.String("language", lang), slog.Bool("rerender", rerender),
		slog.Bool("acme", acme.email != nil || acme.tokenSet))
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

// acmeSettings is the checked domain/ACME part of a SettingsRequest.
type acmeSettings struct {
	domain   *string // normalized hub.domain ("" removes it)
	email    *string // hub.acme.email ("" removes it)
	tokenSet bool    // the Cloudflare token changes
	token    string  // the new token ("" removes it)
	tokenSrc string  // the file it was read from
}

func (a acmeSettings) any() bool { return a.domain != nil || a.email != nil || a.tokenSet }

// acmeRequest checks the domain, ACME e-mail and Cloudflare token file of
// req and reads the token (it is registered as a secret, never logged).
func (h *Hub) acmeRequest(req api.SettingsRequest) (acmeSettings, error) {
	var out acmeSettings
	if req.Domain != nil {
		d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(*req.Domain), "."))
		if d != "" && !tlsutil.ValidDomain(d) {
			return out, deyerr.New(deyerr.C013, deyerr.Params{
				"field": "hub.domain", "value": *req.Domain, "allowed": "a DNS name such as vpn.example.com (no IP address, wildcard or single label), or empty",
			})
		}
		if d == "" {
			if err := acmeTunnelsNeedDomain(h.Config()); err != nil {
				return out, err
			}
		}
		out.domain = &d
	}
	if req.ACMEEmail != nil {
		e := strings.TrimSpace(*req.ACMEEmail)
		if e != "" {
			if addr, err := mail.ParseAddress(e); err != nil || addr.Address != e {
				return out, deyerr.New(deyerr.C013, deyerr.Params{
					"field": "hub.acme.email", "value": e, "allowed": "an e-mail address such as owner@example.com, or empty",
				})
			}
		}
		out.email = &e
	}
	if req.CloudflareTokenFile != nil {
		out.tokenSet = true
		f := strings.TrimSpace(*req.CloudflareTokenFile)
		if f == "" {
			return out, nil
		}
		if !filepath.IsAbs(f) {
			return out, deyerr.New(deyerr.C013, deyerr.Params{
				"field": "cloudflare token file", "value": f, "allowed": "the absolute path of a file holding the Cloudflare API token",
			})
		}
		tok, err := secrets.CloudflareToken(h.path(f))
		if err != nil {
			return out, err
		}
		out.token, out.tokenSrc = tok, filepath.Clean(f)
	}
	return out, nil
}

// acmeTunnelsNeedDomain refuses to remove hub.domain while tunnels use
// tls.mode acme (config validation would refuse it with less help).
func acmeTunnelsNeedDomain(c *config.Config) error {
	var ids []string
	for _, t := range c.Tunnels {
		if t.TLS.Mode == config.TLSModeACME {
			ids = append(ids, t.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return deyerr.New(deyerr.C013, deyerr.Params{
		"field": "hub.domain", "value": "", "allowed": "a domain name while tunnels use tls.mode acme (" + strings.Join(ids, ", ") + ")",
	}).WithFix("switch those tunnels to another TLS mode first: deyroute tunnel edit " + ids[0] + " --tls-mode auto")
}

// checkACMEDomain refuses tls.mode acme for tunnel id while hub.domain is
// empty, naming the command that sets the domain.
func checkACMEDomain(cfg *config.Config, id, mode string) error {
	if mode != config.TLSModeACME || cfg.Hub == nil || strings.TrimSpace(cfg.Hub.Domain) != "" {
		return nil
	}
	return deyerr.New(deyerr.C013, deyerr.Params{
		"field": "tunnels[" + id + "].tls.mode", "value": mode, "allowed": "auto or custom until hub.domain is set",
	}).WithFix(fixSetDomain + ", then choose acme again")
}
