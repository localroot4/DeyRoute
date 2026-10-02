package tui

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/sysctl"
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
			t := newTask(itemName(i18n.TUIOpLimits), callTimeout, loadOptimize, func(a *app, v any) string {
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

// pickProfile offers the three profiles. The hub's RAM decides which one
// is recommended (section 12: aggressive only from 4 GB), and aggressive on
// a smaller hub is confirmed with a warning.
func pickProfile() *listScreen {
	title := i18n.T(i18n.TUIOpApply)
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIOpPick), load: loadOptimize}
	l.header = func(_ *app, v any) string {
		o, _ := v.(api.OptimizeStatus)
		if o.MemBytes == 0 || o.Recommended == "" {
			return ""
		}
		return indent(i18n.T(i18n.TUIOpRecommend, o.MemBytes>>20, o.Recommended)) + "\n"
	}
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
		text := i18n.T(i18n.TUIOpApplyConfirm, profile)
		if o, _ := l.data.(api.OptimizeStatus); profile == config.SysctlAggressive && o.MemBytes > 0 && !sysctl.RecommendAggressive(o.MemBytes) {
			text = i18n.T(i18n.TUIOpSmallRAM, o.MemBytes>>20) + "\n\n" + text
		}
		return a.replace(newConfirm(title, text, false, func(a *app) tea.Cmd {
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
	return newMenu(a, i18n.MenuSecurity, i18n.TUIHelpSecurity, []menuItem{
		{label: i18n.TUISeRotate, act: func(a *app) tea.Cmd { return a.push(pickRotate()) }},
		{label: i18n.TUISeTLS, act: func(a *app) tea.Cmd { return a.push(tlsMenu(a)) }},
		{label: i18n.TUISeRenew, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(subTitle(i18n.MenuSecurity, i18n.TUISeRenew), func(a *app, t api.TunnelInfo) tea.Cmd {
				id := t.ID
				return a.push(newTask(titleOf(i18n.TUISeRenew, id), longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
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
			t := newTask(itemName(i18n.TUISeFingerprints), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
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

// clearValue typed in a settings form removes the setting.
const clearValue = "-"

// tlsMenu is Security > TLS certificates: the certificates, the domain
// tls.mode acme requests a certificate for and (Advanced) the ACME e-mail
// and the Cloudflare token file for DNS-01 (section 10). The header shows
// the current settings.
func tlsMenu(a *app) screen {
	m := newMenu(a, i18n.TUISeTLS, i18n.TUIHelpTLS, []menuItem{
		{label: i18n.TUISeTLSShow, act: func(a *app) tea.Cmd {
			t := newTask(i18n.T(i18n.TUISeTLSShow), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.SecurityTLSShow(ctx, "")
			}, renderCerts)
			t.refreshable = true
			return a.push(t)
		}},
		{label: i18n.TUISeDomain, act: func(a *app) tea.Cmd {
			f := field{key: "domain", label: i18n.T(i18n.TUISeDomainField), hint: i18n.T(i18n.TUISeDomainHint),
				def: tlsHub(a).Domain, check: checkDomain}
			return a.push(acmeForm(i18n.TUISeDomain, f, func(v string) (api.SettingsRequest, string) {
				d := strings.ToLower(strings.TrimSuffix(v, "."))
				return api.SettingsRequest{Domain: &d}, setOrCleared(d, i18n.TUISeDomainSet, i18n.TUISeDomainCleared, d)
			}))
		}},
		{label: i18n.TUISeACMEEmail, adv: true, act: func(a *app) tea.Cmd {
			f := field{key: "email", label: i18n.T(i18n.TUISeEmailField), hint: i18n.T(i18n.TUISeEmailHint), def: tlsHub(a).ACMEEmail}
			return a.push(acmeForm(i18n.TUISeACMEEmail, f, func(v string) (api.SettingsRequest, string) {
				return api.SettingsRequest{ACMEEmail: &v}, setOrCleared(v, i18n.TUISeEmailSet, i18n.TUISeEmailCleared, v)
			}))
		}},
		{label: i18n.TUISeCFToken, adv: true, act: func(a *app) tea.Cmd {
			f := field{key: "file", label: i18n.T(i18n.TUISeTokenField), hint: i18n.T(i18n.TUISeTokenHint), check: checkTokenFile}
			return a.push(acmeForm(i18n.TUISeCFToken, f, func(v string) (api.SettingsRequest, string) {
				return api.SettingsRequest{CloudflareTokenFile: &v},
					setOrCleared(v, i18n.TUISeTokenSet, i18n.TUISeTokenCleared, config.DefaultCloudflareTokenFile)
			}))
		}},
	})
	m.load = func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) { return l.Status(ctx) }
	m.header = renderTLSSettings
	return m
}

// tlsHub returns the hub settings the TLS or Notifications menu (the top
// screen while one of its items is picked) has loaded; zero before they
// are loaded.
func tlsHub(a *app) api.HubStatus {
	if l, ok := a.top().(*listScreen); ok {
		if st, ok := l.data.(api.Status); ok && st.Hub != nil {
			return *st.Hub
		}
	}
	return api.HubStatus{}
}

func renderTLSSettings(a *app, v any) string {
	st, _ := v.(api.Status)
	if st.Hub == nil {
		return ""
	}
	h := st.Hub
	domain := clean(h.Domain)
	if domain == "" {
		domain = a.paint(colYellow, i18n.T(i18n.TUISeNoDomain))
	}
	challenge := i18n.T(i18n.TUISeChHTTP)
	switch h.ACMEChallenge {
	case api.ACMEDNS01:
		challenge = i18n.T(i18n.TUISeChDNS)
	case api.ACMENone:
		challenge = a.paint(colYellow, i18n.T(i18n.TUISeChNone))
	}
	var t kvTable
	t.add(i18n.T(i18n.TUISeDomainLabel), domain)
	t.add(i18n.T(i18n.TUISeChLabel), challenge)
	if a.advanced && h.ACMEEmail != "" {
		t.add(i18n.T(i18n.TUISeEmailLabel), clean(h.ACMEEmail))
	}
	return t.String()
}

// acmeForm asks one TLS setting and saves it with SettingsSet ("-" removes
// it); apply turns the answer into the request and the result line. A
// changed domain renders every tunnel again (TLS SANs): long timeout.
func acmeForm(title i18n.Key, f field, apply func(v string) (api.SettingsRequest, string)) screen {
	name := i18n.T(title)
	return newForm(name, "", []field{f}, func(a *app, v map[string]string) tea.Cmd {
		val := strings.TrimSpace(v[f.key])
		if val == clearValue {
			val = ""
		}
		req, msg := apply(val)
		return a.replace(newTask(name, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.SettingsSet(ctx, req)
		}, textResult(msg)))
	})
}

// setOrCleared is the result line of a TLS setting: set (with arg) or
// cleared when v is empty.
func setOrCleared(v string, set, cleared i18n.Key, arg string) string {
	if v == "" {
		return i18n.T(cleared)
	}
	return i18n.T(set, arg)
}

// checkDomain accepts "-" or a DNS name (the hub checks it again).
func checkDomain(v string, _ map[string]string) error {
	d := strings.ToLower(strings.TrimSuffix(v, "."))
	if v == clearValue || config.ValidDomain(d) {
		return nil
	}
	return uiErr(i18n.TUIWantDomain)
}

// checkTokenFile accepts "-" or an absolute path (the hub reads the file
// with its own working directory).
func checkTokenFile(v string, _ map[string]string) error {
	if v == clearValue || filepath.IsAbs(v) {
		return nil
	}
	return uiErr(i18n.TUIWantAbsPath)
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
			for i, label := range a.tunnelLabels(ts) {
				out = append(out, choice{label: label, value: ts[i].ID})
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

// notifyMenu shows the Telegram settings as its header; Set up Telegram
// offers them as the defaults.
func notifyMenu(a *app) screen {
	m := newMenu(a, i18n.MenuNotifications, i18n.TUIHelpNotify, []menuItem{
		{label: i18n.TUINtSet, act: func(a *app) tea.Cmd {
			title := i18n.T(i18n.TUINtSet)
			tg := tlsHub(a).Telegram
			events := strings.Join(config.DefaultTelegramEvents, ",")
			if len(tg.Events) > 0 {
				events = strings.Join(tg.Events, ",")
			}
			return a.push(newForm(title, "", []field{
				{key: "file", label: i18n.T(i18n.TUINtTokenFile), hint: i18n.T(i18n.TUINtTokenHint), def: tg.TokenFile},
				{key: "chat", label: i18n.T(i18n.TUINtChat), def: tg.ChatID},
				{key: "events", label: i18n.T(i18n.TUINtEvents), hint: i18n.T(i18n.TUINtEventsHint), def: events},
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
	m.load = func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) { return l.Status(ctx) }
	m.header = renderTelegram
	return m
}

// renderTelegram is the Notifications header: whether Telegram is on, the
// chat and the events it sends.
func renderTelegram(a *app, v any) string {
	st, _ := v.(api.Status)
	if st.Hub == nil {
		return ""
	}
	tg := st.Hub.Telegram
	var t kvTable
	if !tg.Enabled {
		t.add(i18n.T(i18n.TUINtLabel), a.paint(colYellow, i18n.T(i18n.TUINtStateOff)))
		return t.String()
	}
	t.add(i18n.T(i18n.TUINtLabel), a.paint(colGreen, i18n.T(i18n.TUINtStateOn)))
	t.add(i18n.T(i18n.TUINtChatLabel), clean(tg.ChatID))
	events := tg.Events
	if len(events) == 0 {
		events = config.DefaultTelegramEvents
	}
	t.add(i18n.T(i18n.TUINtEventsLabel), clean(strings.Join(events, ", ")))
	return t.String()
}

// ---- 10 Backup & Restore

// backupMenu also holds the hub move (section 5): item 3 is Announce hub
// move on a hub and Set hub address on a node.
func backupMenu(a *app) screen {
	return newMenu(a, i18n.MenuBackup, i18n.TUIHelpBackup, []menuItem{
		{label: i18n.TUIBuCreate, act: func(a *app) tea.Cmd { return a.push(backupForm(a)) }},
		{label: i18n.TUIBuRestore, act: func(a *app) tea.Cmd { return a.push(restoreStart(a)) }},
		{label: i18n.TUIBuAnnounce, role: roleHub, act: func(a *app) tea.Cmd { return a.push(announceForm(a)) }},
		{label: i18n.TUIBuSetHub, role: roleNode, act: func(a *app) tea.Cmd { return a.push(setHubForm(a)) }},
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

// restoreStart is Restore from a backup: the backups on this server are
// listed by number, newest first, with "Another file" last; without that
// list (Options.Backups) the file's path is asked.
func restoreStart(a *app) screen {
	list := a.opts.Backups
	if list == nil {
		return restoreForm(a, "")
	}
	return &listScreen{
		screenBase: screenBase{title: i18n.T(i18n.TUIBuRestore)},
		local:      func(ctx context.Context) (any, error) { return list(ctx) },
		header: func(_ *app, v any) string {
			if fs, _ := v.([]BackupFile); len(fs) == 0 {
				return indent(i18n.T(i18n.TUIBuNoFiles, config.BackupDir)) + "\n"
			}
			return indent(i18n.T(i18n.TUIBuPick)) + "\n"
		},
		derive: func(a *app, v any) []choice {
			fs, _ := v.([]BackupFile)
			rows := make([][]string, len(fs))
			for i, f := range fs {
				tag := ""
				if f.Auto {
					tag = i18n.T(i18n.TUIBuAutoTag)
				}
				rows[i] = []string{f.ModTime.In(a.opts.Location).Format("2006-01-02 15:04"), clean(filepath.Base(f.Path)), sizeText(f.Size), tag}
			}
			out := make([]choice, 0, len(fs)+1)
			for i, label := range columns(rows) {
				out = append(out, choice{label: label, value: fs[i].Path})
			}
			return append(out, choice{label: i18n.T(i18n.TUIBuOther), value: ""})
		},
		pick: func(a *app, c choice) tea.Cmd { return a.push(restoreForm(a, c.value.(string))) },
	}
}

// restoreForm reads the backup first (RestoreCheck), asks the moved-hub
// question when a hub backup names another address than this server's,
// then states what the restore replaces, the address change included,
// before the typed yes (as deyroute restore does). file is the backup picked
// from the list ("" = ask for its path); an unencrypted one (.tar.gz)
// needs no passphrase.
func restoreForm(a *app, file string) screen {
	title := i18n.T(i18n.TUIBuRestore)
	if file != "" {
		title = titleOf(i18n.TUIBuRestore, filepath.Base(file))
	}
	check, fn := a.opts.RestoreCheck, a.opts.Restore
	return newForm(title, "", []field{
		{key: "path", label: i18n.T(i18n.TUIBuPath), skip: func(map[string]string) bool { return file != "" }},
		{key: "pass", label: i18n.T(i18n.TUIBuRestPass), masked: true, optional: true,
			skip: func(map[string]string) bool { return strings.HasSuffix(file, ".tar.gz") }},
	}, func(a *app, v map[string]string) tea.Cmd {
		path, pass := orDefault(v["path"], file), v["pass"]
		restore := func(ctx context.Context, ip string) (string, error) { return fn(ctx, path, pass, ip) }
		t := newLocalTask(title, func(ctx context.Context) (any, error) {
			if check == nil || fn == nil {
				return nil, uiErr(i18n.TUINotAvailable)
			}
			return check(ctx, path, pass)
		}, nil)
		t.cancellable = true // reads the backup only
		t.next = func(_ *app, v any) screen {
			p, _ := v.(RestorePlan)
			if p.MovedIP == "" {
				return restoreConfirm(title, p, "", restore)
			}
			return newForm(title, i18n.T(i18n.TUIBuMovedIntro, p.MovedIP, p.OldIP), []field{
				{key: "move", label: i18n.T(i18n.TUIBuMoveField), def: "y", check: checkYes},
			}, func(a *app, v map[string]string) tea.Cmd {
				ip := ""
				if move, _ := parseYes(v["move"]); move {
					ip = p.MovedIP
				}
				return a.replace(restoreConfirm(title, p, ip, restore))
			})
		}
		return a.replace(t)
	})
}

// restoreConfirm states what the restore replaces (and the new hub
// address ip, when the owner chose the move) and restores after the typed
// yes.
func restoreConfirm(title string, p RestorePlan, ip string, restore func(ctx context.Context, ip string) (string, error)) screen {
	text := p.Lost
	if ip != "" {
		text += "\n" + i18n.T(i18n.CLIRestoreMove, p.OldIP, ip)
	}
	return newConfirm(title, text, true, func(a *app) tea.Cmd {
		return a.replace(newLocalTask(title, func(ctx context.Context) (any, error) {
			return restore(ctx, ip)
		}, func(_ *app, v any) string {
			msg := i18n.T(i18n.TUIBuRestored)
			if s, _ := v.(string); s != "" {
				msg += "\n\n" + s
			}
			return indent(msg) + "\n"
		}))
	})
}

// announceForm is Announce hub move on the old hub (section 5, deyroute hub
// announce-move): every online node gets the new address; the offline
// ones are listed with what to run there.
func announceForm(a *app) screen {
	title := i18n.T(i18n.TUIBuAnnounce)
	port := a.ctlPort
	if port <= 0 {
		port = config.DefaultControlPort
	}
	return newForm(title, i18n.T(i18n.TUIBuAnnounceIntro), []field{
		{key: "addr", label: i18n.T(i18n.TUIBuNewAddr), hint: i18n.T(i18n.TUIBuNewAddrHint, port), check: checkHostPort},
	}, func(a *app, v map[string]string) tea.Cmd {
		addr := v["addr"]
		return a.replace(newConfirm(title, i18n.T(i18n.TUIBuAnnounceConfirm, addr), false, func(a *app) tea.Cmd {
			return a.replace(newTask(title, checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.HubAnnounceMove(ctx, addr)
			}, func(a *app, v any) string {
				r, _ := v.(api.AnnounceResult)
				sent := strings.Join(r.Accepted, ", ")
				if sent == "" {
					sent = i18n.T(i18n.TUIDash)
				}
				out := indent(i18n.T(i18n.CLIHubAnnounced, addr, sent)) + "\n"
				if len(r.Offline) > 0 {
					off := a.sym().warn + " " + i18n.T(i18n.TUIBuAnnounceOffline, strings.Join(r.Offline, ", "), addr)
					out += a.paint(colYellow, indent(off)) + "\n"
				}
				if len(r.Front) > 0 {
					out += indent(i18n.T(i18n.CLIHubAnnounceFront, strings.Join(r.Front, ", "))) + "\n"
				}
				return out
			}))
		}))
	})
}

// setHubForm is Set hub address on a node (deyroute node set-hub): the hub
// moved and this node connects to its new address. It works with the node
// agent stopped too (Options.SetHub saves the address for its next start).
func setHubForm(a *app) screen {
	title := i18n.T(i18n.TUIBuSetHub)
	fn, service := a.opts.SetHub, a.opts.Service
	return newForm(title, i18n.T(i18n.TUIBuSetHubIntro, a.status.HubAddr), []field{
		{key: "addr", label: i18n.T(i18n.TUIBuHubAddr), hint: i18n.T(i18n.TUIBuHubAddrHint), check: checkHostPort},
	}, func(a *app, v map[string]string) tea.Cmd {
		addr := v["addr"]
		t := newLocalTask(title, func(ctx context.Context) (any, error) {
			if fn == nil {
				return nil, uiErr(i18n.TUINotAvailable)
			}
			return fn(ctx, addr)
		}, func(_ *app, v any) string {
			if running, _ := v.(bool); !running {
				return indent(i18n.T(i18n.CLINodeSetHubOffline, addr, service)) + "\n"
			}
			return indent(i18n.T(i18n.CLINodeSetHubDone, addr)) + "\n"
		})
		t.onOK = func(a *app, _ any) { a.status.HubAddr = addr }
		return a.replace(t)
	})
}

// checkHostPort accepts the address of a hub: IP:port (or name:port).
func checkHostPort(v string, _ map[string]string) error {
	if config.ValidHostPort(v) {
		return nil
	}
	return uiErr(i18n.TUIWantHostPort)
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
		// UI mode and language are hub settings; a node only uninstalls.
		{label: i18n.TUIStMode, role: roleHub, act: func(a *app) tea.Cmd { return a.push(pickMode(a)) }},
		{label: i18n.TUIStLang, role: roleHub, act: func(a *app) tea.Cmd { return a.push(pickLanguage()) }},
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
		l.fixed = append(l.fixed, choice{label: markCurrent(langName(lang), lang == i18n.Language()), value: lang})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		lang := c.value.(string)
		t := newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.SettingsSet(ctx, api.SettingsRequest{Language: lang})
		}, textResult(i18n.T(i18n.TUIStLangSet, langName(lang))))
		t.onOK = func(*app, any) { _ = i18n.SetLanguage(lang) }
		return a.replace(t)
	}
	return l
}

// langName is the name of a language code ("English" for en).
func langName(code string) string {
	if code == "en" {
		return i18n.T(i18n.TUIStLangEn)
	}
	return code
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
