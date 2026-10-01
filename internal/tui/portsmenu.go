package tui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
)

// ---- 4 Ports

func portsMenu(a *app) screen {
	title := i18n.T(i18n.MenuPorts)
	return newMenu(a, i18n.MenuPorts, i18n.TUIHelpPorts, []menuItem{
		{label: i18n.TUIPtAdd, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUIPtAdd), addPorts))
		}},
		{label: i18n.TUIPtRemove, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUIPtRemove), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(pickPort(t))
			}))
		}},
		{label: i18n.TUIPtCheck, act: func(a *app) tea.Cmd { return a.push(portCheckForm(a)) }},
		{label: i18n.TUIPtFirewall, act: func(a *app) tea.Cmd { return a.push(firewallTask("show")) }},
	})
}

// checkPortInput validates the owner's port syntax (DEY-C020/P010/...).
func checkPortInput(v string, _ map[string]string) error {
	_, err := ports.ParseInput(v)
	return err
}

func addPorts(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUIPtAdd) + ": " + id
	form := newForm(title, "", []field{{
		key: "ports", label: i18n.T(i18n.TUIPtInput), hint: i18n.T(i18n.TUIWizPortsHint), check: checkPortInput,
	}}, func(a *app, v map[string]string) tea.Cmd {
		specs, _ := ports.ParseInput(v["ports"])
		req := make([]api.PortSpec, len(specs))
		for i, s := range specs {
			req[i] = api.PortSpec{Listen: s.Listen, Proto: s.Proto, Target: s.Target}
		}
		list := ports.FormatList(specs)
		return a.replace(newConfirm(title, i18n.T(i18n.TUIPtAddConfirm, list, id), false, func(a *app) tea.Cmd {
			return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
				return l.PortAdd(ctx, id, req, progress)
			}, func(a *app, v any) string {
				ti, _ := v.(api.TunnelInfo)
				return indent(i18n.T(i18n.TUIPtAdded, id, portsText(ti.Ports))) + "\n"
			}))
		}))
	})
	return a.push(form)
}

// pickPort lists the ports of a tunnel for removal.
func pickPort(t api.TunnelInfo) *listScreen {
	id := t.ID
	title := i18n.T(i18n.TUIPtRemove) + ": " + id
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIPtPick), empty: i18n.T(i18n.TUIPtNoPorts, id)}
	for _, p := range t.Ports {
		label := strconv.Itoa(p.Listen) + "/" + p.Proto
		if p.Target != "" && p.Target != ports.DefaultTarget(p.Listen) {
			label += " -> " + p.Target
		}
		l.fixed = append(l.fixed, choice{label: label, value: p})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		p := c.value.(api.PortMapDTO)
		spec := strconv.Itoa(p.Listen) + "/" + p.Proto
		return a.push(newConfirm(title, i18n.T(i18n.TUIPtRemoveLost, spec, id), true, func(a *app) tea.Cmd {
			return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.PortRemove(ctx, id, p.Listen, p.Proto)
			}, func(a *app, v any) string {
				ti, _ := v.(api.TunnelInfo)
				return indent(i18n.T(i18n.TUIPtAdded, id, portsText(ti.Ports))) + "\n"
			}))
		}))
	}
	return l
}

// portCheckForm asks for one port (and, in Advanced mode, the node) and
// shows the four-line check of sections 6 and 10.
func portCheckForm(a *app) screen {
	fields := []field{{key: "port", label: i18n.T(i18n.TUIPCPort), check: func(v string, _ map[string]string) error {
		specs, err := ports.ParseInput(v)
		if err != nil {
			return err
		}
		if len(specs) != 1 {
			return uiErr(i18n.TUIPCOnePort)
		}
		return nil
	}}}
	if a.advanced {
		fields = append(fields, field{key: "node", label: i18n.T(i18n.TUIPCNode), optional: true})
	}
	title := i18n.T(i18n.TUIPtCheck)
	return newForm(title, "", fields, func(a *app, v map[string]string) tea.Cmd {
		specs, _ := ports.ParseInput(v["port"])
		req := api.PortCheckRequest{Port: specs[0].Listen, Proto: specs[0].Proto, Node: v["node"]}
		t := newTask(title+": "+specs[0].String(), checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return l.PortCheck(ctx, req)
		}, func(a *app, v any) string {
			r, _ := v.(api.PortCheckResult)
			return renderPortCheck(a, r)
		})
		t.refreshable = true
		return a.replace(t)
	})
}

// renderPortCheck renders exactly four lines: local bind, firewall,
// reachable from node, reachable via tunnel (with the note that filtering
// inside Iran is not measured).
func renderPortCheck(a *app, r api.PortCheckResult) string {
	s := a.sym()
	good := func(t string) string { return a.paint(colGreen, s.ok) + " " + t }
	bad := func(t string) string { return a.paint(colRed, s.fail) + " " + a.paint(colRed, t) }
	unknown := func(t string) string { return a.paint(colGray, s.skip) + " " + t }

	var bind string
	switch {
	case r.BindFree:
		bind = good(i18n.T(i18n.TUIPCFree))
	case r.BindProcess != "" && r.BindByDey:
		bind = bad(i18n.T(i18n.TUIPCUsedByDey, r.BindProcess))
	case r.BindProcess != "":
		bind = bad(i18n.T(i18n.TUIPCUsedBy, r.BindProcess))
	default:
		bind = bad(i18n.T(i18n.TUIPCUsedUnknown))
	}
	if !r.BindFree && len(r.SuggestedPorts) > 0 {
		ss := make([]string, len(r.SuggestedPorts))
		for i, p := range r.SuggestedPorts {
			ss[i] = strconv.Itoa(p)
		}
		bind += "; " + i18n.T(i18n.TUIPCSuggest, strings.Join(ss, ", "))
	}

	fwName := r.FirewallName
	if fwName == "" {
		fwName = i18n.T(i18n.TUIDash)
	}
	fw := good(i18n.T(i18n.TUIPCOpen, fwName))
	if !r.FirewallOpen {
		fw = bad(i18n.T(i18n.TUIPCClosed, fwName))
		if r.FirewallCommand != "" {
			fw += "; " + i18n.T(i18n.TUIPCOpenCmd, r.FirewallCommand)
		}
	}

	fromLabel := i18n.T(i18n.TUIPCFromNodeNone)
	if r.Node != "" {
		fromLabel = i18n.T(i18n.TUIPCFromNode, r.Node)
	}
	var from string
	switch {
	case r.NodeReachable == nil:
		from = unknown(i18n.T(i18n.TUIPCNotTested))
	case *r.NodeReachable:
		from = good(i18n.T(i18n.TUIPCYesRTT, ms(r.NodeRTTms)))
	default:
		from = bad(i18n.T(i18n.TUINo))
	}

	viaLabel := i18n.T(i18n.TUIPCViaTunnelNone)
	if r.Tunnel != "" {
		viaLabel = i18n.T(i18n.TUIPCViaTunnel, r.Tunnel)
	}
	var via string
	switch {
	case r.TunnelOK == nil:
		via = unknown(i18n.T(i18n.TUIPCNoTunnel))
	case *r.TunnelOK:
		via = good(i18n.T(i18n.TUIPCYesRTT, ms(r.TunnelRTTms)))
	default:
		via = bad(i18n.T(i18n.TUINo))
	}
	note := r.Note
	if note == "" {
		note = i18n.T(i18n.TUIPCNoteIran)
	}
	via += "  " + a.paint(colGray, "("+note+")")

	labels := []string{i18n.T(i18n.TUIPCBind), i18n.T(i18n.TUIPCFirewall), fromLabel, viaLabel}
	lw := 0
	for _, l := range labels {
		lw = max(lw, width(l))
	}
	values := []string{bind, fw, from, via}
	var b strings.Builder
	for i := range labels {
		b.WriteString("  " + pad(labels[i]+":", lw+2) + values[i] + "\n")
	}
	return b.String()
}

// firewallTask runs SecurityFirewall(action) and shows the firewall state.
func firewallTask(action string) *taskScreen {
	title := i18n.T(i18n.TUIPtFirewall)
	t := newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.SecurityFirewall(ctx, action)
	}, renderFirewall)
	t.refreshable = action == "show"
	return t
}

func renderFirewall(a *app, v any) string {
	f, _ := v.(api.FirewallInfo)
	var b strings.Builder
	b.WriteString(" " + i18n.T(i18n.TUISeFwManaged, yesNo(f.Managed)) + "\n")
	det := strings.Join(f.Detected, ", ")
	if det == "" {
		det = i18n.T(i18n.TUINone)
	}
	b.WriteString(" " + i18n.T(i18n.TUISeFwDetected, det) + "\n")
	if len(f.Suggested) > 0 {
		b.WriteString("\n " + a.paint(colYellow, i18n.T(i18n.TUISeFwSuggested)) + "\n")
		for _, s := range f.Suggested {
			b.WriteString("   " + s + "\n")
		}
	}
	if strings.TrimSpace(f.Ruleset) != "" {
		b.WriteString("\n " + i18n.T(i18n.TUISeFwRules) + "\n")
		for _, l := range strings.Split(strings.TrimRight(f.Ruleset, "\n"), "\n") {
			b.WriteString("   " + clean(l) + "\n")
		}
	}
	return b.String()
}
