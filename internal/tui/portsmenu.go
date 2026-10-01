package tui

import (
	"context"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
)

// ---- 4 Ports

func portsMenu(a *app) screen {
	sub := func(k i18n.Key) string { return subTitle(i18n.MenuPorts, k) }
	return newMenu(a, i18n.MenuPorts, i18n.TUIHelpPorts, []menuItem{
		{label: i18n.TUIPtAdd, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIPtAdd), addPorts))
		}},
		{label: i18n.TUIPtRemove, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIPtRemove), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(pickPort(a, t))
			}))
		}},
		{label: i18n.TUIPtCheck, act: func(a *app) tea.Cmd { return a.push(portCheckForm(a)) }},
		{label: i18n.TUIPtFirewall, act: func(a *app) tea.Cmd { return a.push(firewallTask("show")) }},
		{label: i18n.TUIPtProbe, adv: true, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIPtProbe), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(pickProbePort(t.ID))
			}))
		}},
	})
}

// checkPortInput validates the owner's port syntax (DEY-C020/P010/...).
func checkPortInput(v string, _ map[string]string) error {
	_, err := ports.ParseInput(v)
	return err
}

// checkProbe accepts a probe kind config.yaml accepts for a TCP port map
// (ports[].probe, section 9); the probe questions are only asked for TCP
// ports, as UDP maps are always auto.
var checkProbe = checkOneOf(config.ProbeKinds...)

// probeOpts are the probe kinds as numbered answers (section 6: fixed
// options are numbers); a typed kind is accepted too.
func probeOpts() []fieldOpt {
	return []fieldOpt{
		{value: config.ProbeAuto, label: i18n.T(i18n.TUIProbeAuto)},
		{value: config.ProbeTCP, label: i18n.T(i18n.TUIProbeTCP)},
		{value: config.ProbeTLS, label: i18n.T(i18n.TUIProbeTLS)},
		{value: config.ProbeHTTP, label: i18n.T(i18n.TUIProbeHTTP)},
	}
}

// hasTCP reports whether the port input v names a TCP port.
func hasTCP(v string) bool {
	specs, _ := ports.ParseInput(v)
	for _, s := range specs {
		if s.Proto != config.ProtoUDP {
			return true
		}
	}
	return false
}

// addPorts is Ports -> Add port to tunnel; Advanced mode also asks the
// probe kind of the new TCP ports.
func addPorts(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := titleOf(i18n.TUIPtAdd, id)
	fields := []field{{key: "ports", label: i18n.T(i18n.TUIPtInput), hint: i18n.T(i18n.TUIWizPortsHint), check: checkPortInput}}
	if a.advanced {
		fields = append(fields, field{key: "probe", label: i18n.T(i18n.TUIPtProbeField), hint: i18n.T(i18n.TUIPtProbeHint),
			def: config.ProbeAuto, opts: probeOpts(), check: checkProbe, skip: func(v map[string]string) bool { return !hasTCP(v["ports"]) }})
	}
	form := newForm(title, "", fields, func(a *app, v map[string]string) tea.Cmd {
		specs, _ := ports.ParseInput(v["ports"])
		req := make([]api.PortSpec, len(specs))
		for i, s := range specs {
			req[i] = api.PortSpec{Listen: s.Listen, Proto: s.Proto, Target: s.Target}
			if s.Proto != config.ProtoUDP {
				req[i].Probe = v["probe"]
			}
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
func pickPort(a *app, t api.TunnelInfo) *listScreen {
	id := t.ID
	title := titleOf(i18n.TUIPtRemove, id)
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIPtPick), empty: i18n.T(i18n.TUIPtNoPorts, id)}
	for _, p := range t.Ports {
		label := strconv.Itoa(p.Listen) + "/" + p.Proto
		if p.Target != "" && p.Target != ports.DefaultTarget(p.Listen) {
			label += " " + a.sym().arrow + " " + p.Target
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

// pickProbePort lists the TCP ports of tunnel id with their probe kind
// (Ports -> Probe kind, Advanced). The list is loaded again when the owner
// comes back, so a changed kind shows at once.
func pickProbePort(id string) *listScreen {
	return &listScreen{
		screenBase: screenBase{title: i18n.T(i18n.TUIPtProbeTitle, id)},
		intro:      i18n.T(i18n.TUIPtProbePick),
		empty:      i18n.T(i18n.TUIPtProbeNoTCP, id),
		load:       loadTunnels,
		derive: func(_ *app, v any) []choice {
			ts, _ := v.([]api.TunnelInfo)
			var out []choice
			for _, t := range ts {
				if t.ID != id {
					continue
				}
				for _, p := range t.Ports {
					if p.Proto == config.ProtoUDP {
						continue
					}
					label := i18n.T(i18n.TUIPtProbePort, specOf(p.Listen, p.Proto), orDefault(p.Probe, config.ProbeAuto))
					out = append(out, choice{label: label, value: p})
				}
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd { return a.push(pickProbeKind(id, c.value.(api.PortMapDTO))) },
	}
}

// pickProbeKind sets the probe kind of one port map (TunnelEdit; nothing
// restarts).
func pickProbeKind(id string, p api.PortMapDTO) *listScreen {
	spec := specOf(p.Listen, p.Proto)
	cur := orDefault(p.Probe, config.ProbeAuto)
	title := i18n.T(i18n.TUIPtProbeTitle, id+" "+spec)
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIPtProbeKind, spec, id, cur)}
	for _, k := range []struct {
		kind string
		key  i18n.Key
	}{
		{config.ProbeAuto, i18n.TUIProbeAuto},
		{config.ProbeTCP, i18n.TUIProbeTCP},
		{config.ProbeTLS, i18n.TUIProbeTLS},
		{config.ProbeHTTP, i18n.TUIProbeHTTP},
	} {
		label := i18n.T(k.key)
		if k.kind == cur {
			label += i18n.T(i18n.TUICurrent)
		}
		l.fixed = append(l.fixed, choice{label: label, value: k.kind})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		kind := c.value.(string)
		if kind == cur {
			return a.back(i18n.T(i18n.TUINothingChanged))
		}
		req := api.TunnelEditRequest{PortProbes: []api.PortSpec{{Listen: p.Listen, Proto: p.Proto, Probe: kind}}}
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return l.TunnelEdit(ctx, id, req, progress)
		}, textResult(i18n.T(i18n.TUIPtProbeSet, spec, id, kind))))
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
		t := newTask(titleOf(i18n.TUIPtCheck, specs[0].String()), checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return l.PortCheck(ctx, req)
		}, func(a *app, v any) string {
			r, _ := v.(api.PortCheckResult)
			out := renderPortCheck(a, r)
			if r.NodeError != nil {
				// DEY-P014 in the three-line format (section 13).
				out += "\n" + a.errBlock(r.NodeError.Err())
			}
			return out
		})
		t.refreshable = true
		t.option = func(a *app, v any) (string, func(a *app) tea.Cmd) {
			return openFirewallOption(a, v, func() { t.stale = true })
		}
		return a.replace(t)
	})
}

// openFirewallOption offers "1) Open it in the firewall: <command>" on a
// port check whose external firewall blocks the port (section 10). The
// owner confirms the exact command (typed yes); once it runs, recheck marks
// the port check for a new run when the owner comes back.
func openFirewallOption(_ *app, v any, recheck func()) (string, func(a *app) tea.Cmd) {
	r, _ := v.(api.PortCheckResult)
	if r.FirewallOpen || r.FirewallCommand == "" {
		return "", nil
	}
	return i18n.T(i18n.TUIPCOpenItem, r.FirewallCommand), func(a *app) tea.Cmd {
		spec := specOf(r.Port, r.Proto)
		title := i18n.T(i18n.TUIPCOpenTitle, spec)
		req := api.PortOpenRequest{Port: r.Port, Proto: r.Proto, Command: r.FirewallCommand}
		text := i18n.T(i18n.TUIPCOpenConfirm, spec, r.FirewallName, r.FirewallCommand)
		return a.push(newConfirm(title, text, true, func(a *app) tea.Cmd {
			recheck()
			return a.replace(newTask(title, checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return l.PortOpenFirewall(ctx, req)
			}, renderPortOpen))
		}))
	}
}

// specOf is "443/tcp" (tcp when proto is empty).
func specOf(port int, proto string) string {
	if proto == "" {
		proto = config.ProtoTCP
	}
	return ports.FormatSpec(ports.Spec{Listen: port, Proto: proto})
}

// renderPortOpen shows what ran and the firewall afterwards; another
// firewall that still blocks the port is offered on the port check again.
func renderPortOpen(a *app, v any) string {
	r, _ := v.(api.PortOpenResult)
	s := a.sym()
	spec := specOf(r.Port, r.Proto)
	var b strings.Builder
	if r.Ran == "" {
		b.WriteString(indent(i18n.T(i18n.TUIPCOpenNothing, spec)) + "\n")
	} else {
		b.WriteString(indent(i18n.T(i18n.TUIPCOpenRan, r.Ran)) + "\n")
	}
	name := r.FirewallName
	if name == "" {
		name = i18n.T(i18n.TUIDash)
	}
	if r.FirewallOpen {
		b.WriteString(" " + a.paint(colGreen, s.ok) + " " + i18n.T(i18n.TUIPCOpenNow, spec, name) + "\n")
	} else {
		line := i18n.T(i18n.TUIPCOpenStill, spec, name)
		if r.FirewallCommand != "" {
			line += " " + i18n.T(i18n.TUIPCOpenCmd, r.FirewallCommand)
		}
		b.WriteString(" " + a.paint(colRed, s.fail+" "+line) + "\n")
	}
	if r.Note != "" {
		b.WriteString(a.paint(colGray, indent(r.Note)) + "\n")
	}
	b.WriteString("\n" + indent(i18n.T(i18n.TUIPCOpenBack)) + "\n")
	return b.String()
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
