package tui

import (
	"context"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
)

// ---- 7 Optimize

func loadOptimize(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
	return l.OptimizeStatus(ctx)
}

func bbrWord(o api.OptimizeStatus) string {
	switch {
	case o.BBRActive:
		return i18n.T(i18n.TUIOpBBRActive)
	case o.BBRAvailable:
		return i18n.T(i18n.TUIOpBBRAvail)
	}
	return i18n.T(i18n.TUIOpBBRNone)
}

func renderOptimize(a *app, v any) string {
	o, _ := v.(api.OptimizeStatus)
	var b strings.Builder
	b.WriteString(" " + i18n.T(i18n.TUIOpHeader, o.Profile, bbrWord(o)) + "\n")
	for _, w := range o.Warnings {
		b.WriteString(a.paint(colYellow, "  "+a.sym().warn+" "+w) + "\n")
	}
	return b.String()
}

func optimizeMenu(a *app) screen {
	m := newMenu(a, i18n.MenuOptimize, i18n.TUIHelpOptimize, []menuItem{
		{label: i18n.TUIOpApply, act: func(a *app) tea.Cmd { return a.push(pickProfile()) }},
		{label: i18n.TUIOpRevert, act: func(a *app) tea.Cmd {
			title := i18n.T(i18n.TUIOpRevert)
			return a.push(newConfirm(title, i18n.T(i18n.TUIOpRevertConfirm), false, func(a *app) tea.Cmd {
				return a.replace(newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
					return l.OptimizeRevert(ctx)
				}, func(a *app, v any) string {
					o, _ := v.(api.OptimizeStatus)
					return indent(i18n.T(i18n.TUIOpReverted, o.Profile)) + "\n" + renderOptimize(a, v)
				}))
			}))
		}},
		{label: i18n.TUIOpBBR, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUIOpBBR), callTimeout, loadOptimize, func(a *app, v any) string {
				return renderOptimize(a, v) + "\n" + indent(i18n.T(i18n.TUIOpBBRHint)) + "\n"
			})
			t.refreshable = true
			return a.push(t)
		}},
		{label: i18n.TUIOpLimits, adv: true, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUIOpLimits), callTimeout, loadOptimize, func(a *app, v any) string {
				o, _ := v.(api.OptimizeStatus)
				if len(o.Applied) == 0 {
					return indent(i18n.T(i18n.TUIOpNoValues)) + "\n"
				}
				return renderKV(o.Applied, " = ")
			})
			t.refreshable = true
			return a.push(t)
		}},
	})
	m.load = loadOptimize
	m.header = renderOptimize
	return m
}

// renderKV lists a map sorted by key.
func renderKV(m map[string]string, sep string) string {
	keys := make([]string, 0, len(m))
	w := 0
	for k := range m {
		keys = append(keys, k)
		w = max(w, width(k))
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("  " + pad(k, w) + sep + m[k] + "\n")
	}
	return b.String()
}

func pickProfile() *listScreen {
	title := i18n.T(i18n.TUIOpApply)
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIOpPick)}
	for _, p := range []struct {
		id  string
		key i18n.Key
	}{
		{config.SysctlBalanced, i18n.TUIOpBalanced},
		{config.SysctlAggressive, i18n.TUIOpAggressive},
		{config.SysctlOff, i18n.TUIOpOff},
	} {
		l.fixed = append(l.fixed, choice{label: i18n.T(p.key), value: p.id})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		profile := c.value.(string)
		return a.replace(newConfirm(title, i18n.T(i18n.TUIOpApplyConfirm, profile), false, func(a *app) tea.Cmd {
			return a.replace(newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.OptimizeApply(ctx, profile)
			}, func(a *app, v any) string {
				return indent(i18n.T(i18n.TUIOpApplied, profile)) + "\n" + renderOptimize(a, v)
			}))
		}))
	}
	return l
}

// ---- 8 Security

func securityMenu(a *app) screen {
	title := i18n.T(i18n.MenuSecurity)
	return newMenu(a, i18n.MenuSecurity, i18n.TUIHelpSecurity, []menuItem{
		{label: i18n.TUISeRotate, act: func(a *app) tea.Cmd { return a.push(pickRotate()) }},
		{label: i18n.TUISeTLS, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUISeTLS), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.SecurityTLSShow(ctx, "")
			}, renderCerts)
			t.refreshable = true
			return a.push(t)
		}},
		{label: i18n.TUISeRenew, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUISeRenew), func(a *app, t api.TunnelInfo) tea.Cmd {
				id := t.ID
				return a.push(newTask(i18n.T(i18n.TUISeRenew)+": "+id, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
					return l.SecurityTLSRenew(ctx, id)
				}, renderCerts))
			}))
		}},
		{label: i18n.TUISeFirewall, act: func(a *app) tea.Cmd { return a.push(firewallMenu(a)) }},
		{label: i18n.TUISeAudit, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUISeAudit), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.SecurityAudit(ctx)
			}, renderAudit)
			t.refreshable = true
			return a.push(t)
		}},
		{label: i18n.TUISeFingerprints, adv: true, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUISeFingerprints), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				certs, err := l.SecurityTLSShow(ctx, "")
				if err != nil {
					return nil, err
				}
				nodes, err := l.NodeList(ctx)
				if err != nil {
					return nil, err
				}
				return fingerprints{certs: certs, nodes: nodes}, nil
			}, renderFingerprints)
			t.refreshable = true
			return a.push(t)
		}},
	})
}

func pickRotate() *listScreen {
	title := i18n.T(i18n.TUISeRotate)
	return &listScreen{
		screenBase: screenBase{title: title},
		intro:      i18n.T(i18n.TUIPickTunnel),
		load:       loadTunnels,
		derive: func(a *app, v any) []choice {
			ts, _ := v.([]api.TunnelInfo)
			out := []choice{{label: i18n.T(i18n.TUISeAllTunnels), value: ""}}
			for _, t := range ts {
				out = append(out, choice{label: a.tunnelLabel(t), value: t.ID})
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd {
			id := c.value.(string)
			what := id
			if id == "" {
				what = i18n.T(i18n.TUISeAllTunnels)
			}
			return a.replace(newConfirm(title, i18n.T(i18n.TUISeRotateLost, what), true, func(a *app) tea.Cmd {
				return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
					return nil, l.SecurityRotateTokens(ctx, id, progress)
				}, textResult(i18n.T(i18n.TUISeRotated, what))))
			}))
		},
	}
}

func renderCerts(a *app, v any) string {
	cs, _ := v.([]api.CertInfo)
	if len(cs) == 0 {
		return indent(i18n.T(i18n.TUISeNoCerts)) + "\n"
	}
	kw, sw := 0, 0
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Kind
		if c.Tunnel != "" {
			names[i] += " " + c.Tunnel
		}
		if c.Mode != "" {
			names[i] += " (" + c.Mode + ")"
		}
		kw, sw = max(kw, width(names[i])), max(sw, width(c.Subject))
	}
	var b strings.Builder
	for i, c := range cs {
		exp := i18n.T(i18n.TUISeExpires, c.NotAfter.In(a.opts.Location).Format("2006-01-02"), c.DaysLeft)
		switch {
		case c.DaysLeft <= 0:
			exp = a.paint(colRed, exp)
		case c.DaysLeft <= 14 || c.Warning != "":
			exp = a.paint(colYellow, exp)
		}
		b.WriteString("  " + pad(names[i], kw+2) + pad(c.Subject, sw+2) + exp + "\n")
		if c.Warning != "" {
			b.WriteString(a.paint(colYellow, "    "+a.sym().warn+" "+c.Warning) + "\n")
		}
		if a.advanced && c.Fingerprint != "" {
			b.WriteString(a.paint(colGray, "    "+c.Fingerprint) + "\n")
		}
	}
	return b.String()
}

func renderAudit(a *app, v any) string {
	r, _ := v.(api.AuditReport)
	s := a.sym()
	var b strings.Builder
	for _, it := range r.Items {
		mark := a.paint(colGreen, s.ok)
		switch it.Severity {
		case "warn":
			mark = a.paint(colYellow, s.warn)
		case "error":
			mark = a.paint(colRed, s.fail)
		}
		b.WriteString("  " + mark + " " + clean(it.Check) + ": " + clean(it.Message) + "\n")
	}
	if r.Clean {
		b.WriteString("\n" + a.paint(colGreen, " "+i18n.T(i18n.TUISeAuditClean)) + "\n")
	}
	return b.String()
}

type fingerprints struct {
	certs []api.CertInfo
	nodes []api.NodeInfo
}

func renderFingerprints(a *app, v any) string {
	f, _ := v.(fingerprints)
	var b strings.Builder
	for _, c := range f.certs {
		name := c.Kind
		if c.Tunnel != "" {
			name += " " + c.Tunnel
		}
		b.WriteString("  " + pad(name, 16) + c.Fingerprint + "\n")
	}
	for _, n := range f.nodes {
		b.WriteString("  " + pad(i18n.T(i18n.TUISeFpNode, n.ID), 16) + n.Fingerprint + "\n")
	}
	return b.String()
}

func firewallMenu(a *app) screen {
	title := i18n.T(i18n.TUISeFirewall)
	m := newMenu(a, i18n.TUISeFirewall, i18n.TUIHelpSecurity, []menuItem{
		{label: i18n.TUISeFwShow, act: func(a *app) tea.Cmd { return a.push(firewallTask("show")) }},
		{label: i18n.TUISeFwApply, act: func(a *app) tea.Cmd {
			return a.push(newConfirm(title, i18n.T(i18n.TUISeFwApplyConfirm), false, func(a *app) tea.Cmd {
				return a.replace(firewallTask("apply"))
			}))
		}},
		{label: i18n.TUISeFwDisable, act: func(a *app) tea.Cmd {
			return a.push(newConfirm(title, i18n.T(i18n.TUISeFwDisableLost), true, func(a *app) tea.Cmd {
				return a.replace(firewallTask("disable"))
			}))
		}},
	})
	return m
}

// ---- 9 Notifications

func notifyMenu(a *app) screen {
	return newMenu(a, i18n.MenuNotifications, i18n.TUIHelpNotify, []menuItem{
		{label: i18n.TUINtSet, act: func(a *app) tea.Cmd {
			title := i18n.T(i18n.TUINtSet)
			return a.push(newForm(title, "", []field{
				{key: "file", label: i18n.T(i18n.TUINtTokenFile), hint: i18n.T(i18n.TUINtTokenHint)},
				{key: "chat", label: i18n.T(i18n.TUINtChat)},
				{key: "events", label: i18n.T(i18n.TUINtEvents), def: strings.Join(config.DefaultTelegramEvents, ",")},
			}, func(a *app, v map[string]string) tea.Cmd {
				file, chat := v["file"], v["chat"]
				var events []string
				for _, e := range strings.Split(v["events"], ",") {
					if e = strings.TrimSpace(e); e != "" {
						events = append(events, e)
					}
				}
				return a.replace(newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
					return nil, l.NotifyTelegramSet(ctx, file, chat, events)
				}, textResult(i18n.T(i18n.TUINtOn))))
			}))
		}},
		{label: i18n.TUINtTest, act: func(a *app) tea.Cmd {
			return a.push(newTask(i18n.T(i18n.TUINtTest), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return nil, l.NotifyTelegramTest(ctx)
			}, textResult(i18n.T(i18n.TUINtSent))))
		}},
		{label: i18n.TUINtOff, act: func(a *app) tea.Cmd {
			title := i18n.T(i18n.TUINtOff)
			return a.push(newConfirm(title, i18n.T(i18n.TUINtOffConfirm), false, func(a *app) tea.Cmd {
				return a.replace(newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
					return nil, l.NotifyTelegramOff(ctx)
				}, textResult(i18n.T(i18n.TUINtOffDone))))
			}))
		}},
	})
}

// ---- 10 Backup & Restore

func backupMenu(a *app) screen {
	return newMenu(a, i18n.MenuBackup, i18n.TUIHelpBackup, []menuItem{
		{label: i18n.TUIBuCreate, act: func(a *app) tea.Cmd { return a.push(backupForm(a)) }},
		{label: i18n.TUIBuRestore, act: func(a *app) tea.Cmd { return a.push(restoreForm(a)) }},
	})
}

func noEncrypt(v map[string]string) bool {
	y, _ := parseYes(v["encrypt"])
	return !y
}

func backupForm(a *app) screen {
	title := i18n.T(i18n.TUIBuCreate)
	fn := a.opts.Backup
	return newForm(title, "", []field{
		{key: "out", label: i18n.T(i18n.TUIBuOut), optional: true},
		{key: "encrypt", label: i18n.T(i18n.TUIBuEncrypt), def: "y", check: checkYes},
		{key: "pass", label: i18n.T(i18n.TUIBuPass), masked: true, skip: noEncrypt},
		{key: "pass2", label: i18n.T(i18n.TUIBuPass2), masked: true, skip: noEncrypt,
			check: func(v string, vals map[string]string) error {
				if v != vals["pass"] {
					return uiErr(i18n.TUIBuMismatch)
				}
				return nil
			}},
	}, func(a *app, v map[string]string) tea.Cmd {
		out, pass, plain := v["out"], v["pass"], noEncrypt(v)
		return a.replace(newLocalTask(title, func(ctx context.Context) (any, error) {
			if fn == nil {
				return nil, uiErr(i18n.TUINotAvailable)
			}
			return fn(ctx, out, pass, plain)
		}, func(a *app, v any) string {
			path, _ := v.(string)
			s := indent(i18n.T(i18n.TUIBuSaved, path)) + "\n"
			if plain {
				s += a.paint(colYellow, indent(i18n.T(i18n.TUIBuPlainWarn))) + "\n"
			}
			return s
		}))
	})
}

func restoreForm(a *app) screen {
	title := i18n.T(i18n.TUIBuRestore)
	fn := a.opts.Restore
	return newForm(title, "", []field{
		{key: "path", label: i18n.T(i18n.TUIBuPath)},
		{key: "pass", label: i18n.T(i18n.TUIBuRestPass), masked: true, optional: true},
	}, func(a *app, v map[string]string) tea.Cmd {
		path, pass := v["path"], v["pass"]
		return a.replace(newConfirm(title, i18n.T(i18n.TUIBuRestoreLost, path), true, func(a *app) tea.Cmd {
			return a.replace(newLocalTask(title, func(ctx context.Context) (any, error) {
				if fn == nil {
					return nil, uiErr(i18n.TUINotAvailable)
				}
				return nil, fn(ctx, path, pass)
			}, textResult(i18n.T(i18n.TUIBuRestored))))
		}))
	})
}

// ---- 11 Update

func updateMenu(a *app) screen {
	return newMenu(a, i18n.MenuUpdate, i18n.TUIHelpUpdate, []menuItem{
		{label: i18n.TUIUpCheck, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUIUpCheck), checkTimeout, loadUpdate, renderUpdate)
			t.refreshable = true
			return a.push(t)
		}},
		{label: i18n.TUIUpApply, act: func(a *app) tea.Cmd { return a.push(applyUpdate()) }},
		{label: i18n.TUIUpBackends, act: func(a *app) tea.Cmd {
			title := i18n.T(i18n.TUIUpBackends)
			return a.push(newConfirm(title, i18n.T(i18n.TUIUpBackendsConfirm), false, func(a *app) tea.Cmd {
				return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
					return l.UpdateBackends(ctx, "", progress)
				}, renderBackendUpdates))
			}))
		}},
		{label: i18n.TUIUpManifest, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUIUpManifest), checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.UpdateManifest(ctx)
			}, func(a *app, v any) string {
				m, _ := v.(api.ManifestInfo)
				return " " + i18n.T(i18n.TUIUpManifestSource, m.Source) + "\n\n" + renderKV(m.Versions, "  ")
			})
			t.refreshable = true
			return a.push(t)
		}},
		{label: i18n.TUIUpRollback, act: func(a *app) tea.Cmd {
			title := i18n.T(i18n.TUIUpRollback)
			return a.push(newConfirm(title, i18n.T(i18n.TUIUpRollbackConfirm), false, func(a *app) tea.Cmd {
				return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
					return l.UpdateRollback(ctx)
				}, func(a *app, v any) string {
					u, _ := v.(api.UpdateInfo)
					return indent(i18n.T(i18n.TUIUpRolledBack, u.Current)) + "\n"
				}))
			}))
		}},
	})
}

func loadUpdate(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
	return l.UpdateCheck(ctx)
}

func renderUpdate(a *app, v any) string {
	u, _ := v.(api.UpdateInfo)
	var b strings.Builder
	b.WriteString(" " + i18n.T(i18n.TUIUpCurrent, u.Current) + "\n")
	if u.Latest != "" {
		b.WriteString(" " + i18n.T(i18n.TUIUpLatest, u.Latest) + "\n")
	}
	if u.Available {
		b.WriteString("\n" + a.paint(colYellow, " "+i18n.T(i18n.TUIUpAvailable)) + "\n")
	} else {
		b.WriteString("\n" + a.paint(colGreen, " "+i18n.T(i18n.TUIUpNone)) + "\n")
	}
	if strings.TrimSpace(u.Changelog) != "" {
		b.WriteString("\n " + i18n.T(i18n.TUIUpChangelog) + "\n")
		b.WriteString(indent(indent(cleanLines(u.Changelog))) + "\n")
	}
	return b.String()
}

// applyUpdate checks first; when a release is available it asks, then
// runs UpdateApply with progress.
func applyUpdate() *taskScreen {
	title := i18n.T(i18n.TUIUpApply)
	t := newTask(title, checkTimeout, loadUpdate, renderUpdate)
	t.cancellable = true // the check only reads; the update itself is confirmed next
	t.next = func(a *app, v any) screen {
		u, _ := v.(api.UpdateInfo)
		if !u.Available {
			return nil
		}
		text := i18n.T(i18n.TUIUpApplyConfirm, u.Current, u.Latest)
		if strings.TrimSpace(u.Changelog) != "" {
			text += "\n\n" + i18n.T(i18n.TUIUpChangelog) + "\n" + cleanLines(u.Changelog)
		}
		version := u.Latest
		return newConfirm(title, text, false, func(a *app) tea.Cmd {
			return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
				return l.UpdateApply(ctx, version, progress)
			}, func(a *app, v any) string {
				r, _ := v.(api.UpdateInfo)
				return a.paint(colGreen, " "+i18n.T(i18n.TUIUpApplied, r.Current)) + "\n"
			}))
		})
	}
	return t
}

func renderBackendUpdates(a *app, v any) string {
	us, _ := v.([]api.BackendUpdate)
	if len(us) == 0 {
		return indent(i18n.T(i18n.TUIUpNoBackends)) + "\n"
	}
	s := a.sym()
	var b strings.Builder
	for _, u := range us {
		line := "  " + pad(u.Backend, 12) + u.From + " " + s.arrow + " " + u.To + "  "
		switch u.Status {
		case "updated":
			line += a.paint(colGreen, u.Status)
		case "unchanged":
			line += a.paint(colGray, u.Status)
		default:
			line += a.paint(colRed, u.Status)
		}
		b.WriteString(line + "\n")
		if u.Error != nil {
			b.WriteString(a.errBlock(u.Error.Err()))
		}
	}
	return b.String()
}

// ---- 12 Settings

func settingsMenu(a *app) screen {
	return newMenu(a, i18n.MenuSettings, i18n.TUIHelpSettings, []menuItem{
		{label: i18n.TUIStMode, act: func(a *app) tea.Cmd { return a.push(pickMode(a)) }},
		{label: i18n.TUIStLang, act: func(a *app) tea.Cmd { return a.push(pickLanguage()) }},
		{label: i18n.TUIStUninstall, act: func(a *app) tea.Cmd { return a.push(uninstallForm(a)) }},
	})
}

func pickMode(a *app) *listScreen {
	title := i18n.T(i18n.TUIStMode)
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIStModePick)}
	for _, m := range []struct {
		id  string
		key i18n.Key
		adv bool
	}{{uiSimple, i18n.TUIStSimpleDesc, false}, {uiAdvanced, i18n.TUIStAdvDesc, true}} {
		label := i18n.T(m.key)
		if m.adv == a.advanced {
			label += i18n.T(i18n.TUICurrent)
		}
		l.fixed = append(l.fixed, choice{label: label, value: m.id})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		mode := c.value.(string)
		word := i18n.T(i18n.ModeSimple)
		if mode == uiAdvanced {
			word = i18n.T(i18n.ModeAdvanced)
		}
		t := newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.SettingsSet(ctx, api.SettingsRequest{UIMode: mode})
		}, textResult(i18n.T(i18n.TUIStModeSet, word)))
		t.onOK = func(a *app, _ any) { a.advanced = mode == uiAdvanced }
		return a.replace(t)
	}
	return l
}

func pickLanguage() *listScreen {
	title := i18n.T(i18n.TUIStLang)
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIStLangPick)}
	for _, lang := range i18n.Languages() {
		label := lang
		if lang == "en" {
			label = i18n.T(i18n.TUIStLangEn)
		}
		if lang == i18n.Language() {
			label += i18n.T(i18n.TUICurrent)
		}
		l.fixed = append(l.fixed, choice{label: label, value: lang})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		lang := c.value.(string)
		t := newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.SettingsSet(ctx, api.SettingsRequest{Language: lang})
		}, textResult(i18n.T(i18n.TUIStLangSet, lang)))
		t.onOK = func(*app, any) { _ = i18n.SetLanguage(lang) }
		return a.replace(t)
	}
	return l
}

func uninstallForm(a *app) screen {
	title := i18n.T(i18n.TUIStUninstall)
	fn := a.opts.Uninstall
	isHub := a.status.Role != "node"
	fields := []field{
		{key: "keep", label: i18n.T(i18n.TUIStKeepBackups), def: "y", check: checkYes},
		{key: "nodes", label: i18n.T(i18n.TUIStNodes), def: "n", check: checkYes,
			skip: func(map[string]string) bool { return !isHub }},
	}
	return newForm(title, "", fields, func(a *app, v map[string]string) tea.Cmd {
		keep, _ := parseYes(v["keep"])
		nodes, _ := parseYes(v["nodes"])
		backups := i18n.T(i18n.TUIStUninstallAll)
		if keep {
			backups = i18n.T(i18n.TUIStUninstallKeep)
		}
		nodeLine := ""
		if nodes {
			nodeLine = i18n.T(i18n.TUIStUninstallNodes)
		}
		text := i18n.T(i18n.TUIStUninstallLost, backups, nodeLine)
		return a.replace(newConfirm(title, text, true, func(a *app) tea.Cmd {
			return a.replace(newLocalTask(title, func(ctx context.Context) (any, error) {
				if fn == nil {
					return nil, uiErr(i18n.TUINotAvailable)
				}
				return nil, fn(ctx, keep, nodes)
			}, textResult(i18n.T(i18n.TUIStUninstalled))))
		}))
	})
}
