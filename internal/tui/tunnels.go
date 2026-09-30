package tui

import (
	"context"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
)

// ---- shared pickers

func loadTunnels(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
	return l.TunnelList(ctx)
}

func loadNodes(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
	return l.NodeList(ctx)
}

// tunnelLabel is one tunnel in a pick list: "main  Main 443/2053  ● UP  443,2053".
func (a *app) tunnelLabel(t api.TunnelInfo) string {
	parts := []string{t.ID}
	if t.Name != "" && t.Name != t.ID {
		parts = append(parts, t.Name)
	}
	parts = append(parts, a.stateText(t))
	if p := portsText(t.Ports); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, "  ")
}

// nodeLabel is one node in a pick list: "de-1  Germany 1  ● online".
func (a *app) nodeLabel(n api.NodeInfo) string {
	s := a.sym()
	st := s.up + " " + i18n.T(i18n.TUIOnline)
	if !n.Online {
		st = s.down + " " + i18n.T(i18n.TUIOffline)
	}
	parts := []string{n.ID}
	if n.Name != "" && n.Name != n.ID {
		parts = append(parts, n.Name)
	}
	return strings.Join(append(parts, st), "  ")
}

// pickTunnel lists the tunnels and runs then with the chosen one.
func pickTunnel(title string, then func(a *app, t api.TunnelInfo) tea.Cmd) *listScreen {
	return &listScreen{
		screenBase: screenBase{title: title},
		intro:      i18n.T(i18n.TUIPickTunnel),
		load:       loadTunnels,
		empty:      i18n.T(i18n.TUINoTunnels),
		derive: func(a *app, v any) []choice {
			ts, _ := v.([]api.TunnelInfo)
			out := make([]choice, 0, len(ts))
			for _, t := range ts {
				out = append(out, choice{label: a.tunnelLabel(t), value: t})
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd { return then(a, c.value.(api.TunnelInfo)) },
	}
}

// pickNode lists the nodes accepted by filter (nil = all) and runs then.
func pickNode(title, empty string, filter func(n api.NodeInfo) bool, then func(a *app, n api.NodeInfo) tea.Cmd) *listScreen {
	if empty == "" {
		empty = i18n.T(i18n.TUINoNodes)
	}
	return &listScreen{
		screenBase: screenBase{title: title},
		intro:      i18n.T(i18n.TUIPickNode),
		load:       loadNodes,
		empty:      empty,
		derive: func(a *app, v any) []choice {
			ns, _ := v.([]api.NodeInfo)
			var out []choice
			for _, n := range ns {
				if filter == nil || filter(n) {
					out = append(out, choice{label: a.nodeLabel(n), value: n})
				}
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd { return then(a, c.value.(api.NodeInfo)) },
	}
}

// ---- 2 Tunnels

func tunnelsMenu(a *app) screen {
	title := i18n.T(i18n.MenuTunnels)
	m := newMenu(a, i18n.MenuTunnels, i18n.TUIHelpTunnels, []menuItem{
		{label: i18n.TUITunAdd, act: func(a *app) tea.Cmd { return a.push(newWizard()) }},
		{label: i18n.TUITunEdit, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUITunEdit), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(editForm(a, t))
			}))
		}},
		{label: i18n.TUITunToggle, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUITunToggle), toggleTunnel))
		}},
		{label: i18n.TUITunRestart, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUITunRestart), restartTunnel))
		}},
		{label: i18n.TUITunSwitch, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUITunSwitch), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(pickSwitch(t))
			}))
		}},
		{label: i18n.TUITunDelete, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUITunDelete), deleteTunnel))
		}},
		{label: i18n.TUITunShow, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(title+" - "+i18n.T(i18n.TUITunShow), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(showTunnel(t.ID))
			}))
		}},
	})
	m.load = loadTunnels
	m.header = func(a *app, v any) string {
		ts, _ := v.([]api.TunnelInfo)
		if len(ts) == 0 {
			return indent(i18n.T(i18n.TUIDashNoTunnels)) + "\n"
		}
		return renderTunnelTable(a, ts, a.opts.Now())
	}
	return m
}

// editForm edits the mutable fields of a tunnel (TunnelEdit).
func editForm(a *app, t api.TunnelInfo) screen {
	fields := []field{{key: "name", label: i18n.T(i18n.TUIEditName), def: t.Name}}
	if a.advanced {
		fields = append(fields,
			field{key: "policy", label: i18n.T(i18n.TUIEditPolicy), def: orDefault(t.Policy, config.PolicyTransportThenNode),
				check: checkOneOf(config.PolicyTransportThenNode, config.PolicyTransportOnly, config.PolicyNodeOnly)},
			field{key: "ladder", label: i18n.T(i18n.TUIEditLadder), def: t.LadderName, optional: true},
			field{key: "tls", label: i18n.T(i18n.TUIEditTLS), optional: true,
				check: checkOneOf(config.TLSModeAuto, config.TLSModeACME, config.TLSModeCustom)},
			field{key: "cert", label: i18n.T(i18n.TUIEditTLSCert), skip: notCustomTLS},
			field{key: "key", label: i18n.T(i18n.TUIEditTLSKey), skip: notCustomTLS},
			field{key: "probe", label: i18n.T(i18n.TUIEditProbe), optional: true, check: optionalInt(1)},
		)
	}
	title := i18n.T(i18n.TUITunEdit) + ": " + t.ID
	return newForm(title, i18n.T(i18n.TUIKeepHint), fields, func(a *app, v map[string]string) tea.Cmd {
		var req api.TunnelEditRequest
		changed := false
		set := func(val, cur string, dst **string) {
			if val != "" && val != cur {
				s := val
				*dst = &s
				changed = true
			}
		}
		set(v["name"], t.Name, &req.Name)
		set(v["policy"], t.Policy, &req.Policy)
		set(v["ladder"], t.LadderName, &req.Ladder)
		set(v["tls"], "", &req.TLSMode)
		set(v["cert"], "", &req.TLSCert)
		set(v["key"], "", &req.TLSKey)
		if p := v["probe"]; p != "" {
			n := atoi(p)
			req.ProbePort = &n
			changed = true
		}
		if !changed {
			return a.back(i18n.T(i18n.TUINothingChanged))
		}
		id := t.ID
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return l.TunnelEdit(ctx, id, req, progress)
		}, textResult(i18n.T(i18n.TUITunUpdated, id))))
	})
}

func notCustomTLS(v map[string]string) bool { return v["tls"] != config.TLSModeCustom }

// optionalInt accepts an empty value or a whole number >= min.
func optionalInt(min int) func(string, map[string]string) error {
	return func(v string, vals map[string]string) error {
		if v == "" {
			return nil
		}
		return checkInt(min)(v, vals)
	}
}

func toggleTunnel(a *app, t api.TunnelInfo) tea.Cmd {
	id, enable := t.ID, !t.Enabled
	msg := i18n.T(i18n.TUITunEnabled, id)
	if !enable {
		msg = i18n.T(i18n.TUITunDisabled, id)
	}
	task := newTask(i18n.T(i18n.TUITunToggle)+": "+id, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return nil, l.TunnelSetEnabled(ctx, id, enable)
	}, textResult(msg))
	if enable {
		return a.push(task)
	}
	return a.push(newConfirm(task.title, i18n.T(i18n.TUITunDisableConfirm, id, portsText(t.Ports)), false,
		func(a *app) tea.Cmd { return a.replace(task) }))
}

func restartTunnel(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUITunRestart) + ": " + id
	return a.push(newConfirm(title, i18n.T(i18n.TUITunRestartConfirm, id), false, func(a *app) tea.Cmd {
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.TunnelRestart(ctx, id)
		}, textResult(i18n.T(i18n.TUITunRestarted, id))))
	}))
}

func deleteTunnel(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUITunDelete) + ": " + id
	nodes := strings.Join(t.Nodes, ", ")
	if nodes == "" {
		nodes = i18n.T(i18n.TUINone)
	}
	text := i18n.T(i18n.TUITunDeleteLost, id, nodes, portsText(t.Ports))
	return a.push(newConfirm(title, text, true, func(a *app) tea.Cmd {
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return nil, l.TunnelDelete(ctx, id, progress)
		}, textResult(i18n.T(i18n.TUITunDeleted, id))))
	}))
}

// switchData is loaded by the switch picker: the tunnel's ladder with the
// client-IP property of every transport (section 10).
type switchData struct {
	t  api.TunnelInfo
	tr map[string]api.TransportInfo
}

func transportMap(ctx context.Context, l api.Local) (map[string]api.TransportInfo, error) {
	list, err := l.TransportList(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]api.TransportInfo, len(list))
	for _, x := range list {
		m[x.ID] = x
	}
	return m, nil
}

// clientIPNote is "client IP: preserved|masked" for a transport.
func clientIPNote(tr map[string]api.TransportInfo, id string) string {
	x, ok := tr[id]
	if !ok {
		return ""
	}
	w := i18n.T(i18n.TUIClientMasked)
	if x.ClientIPPreserved {
		w = i18n.T(i18n.TUIClientKept)
	}
	return i18n.T(i18n.TUIClientIP, w)
}

func pickSwitch(t api.TunnelInfo) *listScreen {
	id := t.ID
	title := i18n.T(i18n.TUITunSwitch) + ": " + id
	return &listScreen{
		screenBase: screenBase{title: title},
		intro:      i18n.T(i18n.TUITunSwitchPick, id),
		load: func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			ts, err := l.TunnelList(ctx)
			if err != nil {
				return nil, err
			}
			cur := t
			for _, x := range ts {
				if x.ID == id {
					cur = x
				}
			}
			tr, err := transportMap(ctx, l)
			if err != nil {
				return nil, err
			}
			return switchData{t: cur, tr: tr}, nil
		},
		derive: func(a *app, v any) []choice {
			d, _ := v.(switchData)
			w := 0
			for _, r := range d.t.Ladder {
				w = max(w, width(r))
			}
			var out []choice
			for _, r := range d.t.Ladder {
				label := pad(r, w+2) + clientIPNote(d.tr, r)
				if r == d.t.ActiveTransport {
					label = strings.TrimRight(label, " ") + i18n.T(i18n.TUICurrent)
				}
				out = append(out, choice{label: strings.TrimRight(label, " "), value: api.SwitchRequest{Transport: r}})
			}
			if len(d.t.Nodes) > 1 {
				for _, n := range d.t.Nodes {
					label := i18n.T(i18n.TUITunSwitchNode, n)
					if n == d.t.ActiveNode {
						label += i18n.T(i18n.TUICurrent)
					}
					out = append(out, choice{label: label, value: api.SwitchRequest{Node: n}})
				}
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd {
			req := c.value.(api.SwitchRequest)
			target := req.Transport + req.Node
			return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return nil, l.TunnelSwitch(ctx, id, req)
			}, textResult(i18n.T(i18n.TUITunSwitched, id, target))))
		},
	}
}

// showTunnel renders TunnelShow (refreshable).
func showTunnel(id string) *taskScreen {
	t := newTask(i18n.T(i18n.TUITunShow)+": "+id, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		d, err := l.TunnelShow(ctx, id)
		if err != nil {
			return nil, err
		}
		tr, err := transportMap(ctx, l)
		if err != nil {
			tr = nil // the property column is optional
		}
		return tunnelDetail{d: d, tr: tr}, nil
	}, renderDetail)
	t.refreshable = true
	return t
}

type tunnelDetail struct {
	d  api.TunnelDetail
	tr map[string]api.TransportInfo
}

func renderDetail(a *app, v any) string {
	td, _ := v.(tunnelDetail)
	d := td.d
	var b strings.Builder
	b.WriteString(kv(i18n.T(i18n.TUIDetID), d.ID))
	if d.Name != "" {
		b.WriteString(kv(i18n.T(i18n.TUIDetName), d.Name))
	}
	sy, w, col := a.stateLook(d.TunnelInfo)
	st := a.paint(col, sy+" "+w)
	if d.ActiveTransport != "" {
		st += "  " + i18n.T(i18n.TUIDetVia, d.ActiveTransport, d.ActiveNode)
		if d.RTTms > 0 {
			st += " (" + ms(d.RTTms) + ")"
		}
	}
	b.WriteString(kv(i18n.T(i18n.TUIDetState), st))
	var ps []string
	for _, p := range d.Ports {
		ps = append(ps, strconv.Itoa(p.Listen)+"/"+p.Proto+" "+a.sym().arrow+" "+p.Target)
	}
	b.WriteString(kv(i18n.T(i18n.TUIDetPorts), strings.Join(ps, ", ")))
	var ns []string
	for i, n := range d.Nodes {
		role := i18n.T(i18n.TUIDetBackup)
		if i == 0 {
			role = i18n.T(i18n.TUIDetPrimary)
		}
		ns = append(ns, n+" ("+role+")")
	}
	b.WriteString(kv(i18n.T(i18n.TUIDetNodes), strings.Join(ns, ", ")))
	if d.ClientIP != "" {
		b.WriteString(kv(i18n.T(i18n.TUIDetClientIP), d.ClientIP))
	}
	if a.advanced {
		b.WriteString(kv(i18n.T(i18n.TUIDetPolicy), d.Policy))
		lad := strings.Join(d.Ladder, " "+a.sym().arrow+" ")
		if d.LadderName != "" {
			lad = d.LadderName + ": " + lad
		}
		b.WriteString(kv(i18n.T(i18n.TUIDetLadder), lad))
		if d.TLSMode != "" {
			b.WriteString(kv(i18n.T(i18n.TUIDetTLS), d.TLSMode))
		}
		if len(d.Rungs) > 0 {
			b.WriteString("\n  " + i18n.T(i18n.TUIDetRungs) + "\n")
			b.WriteString(renderRungs(a, d.Rungs, td.tr))
		}
	}
	for _, w := range d.Warnings {
		b.WriteString(a.paint(colYellow, "  "+a.sym().warn+" "+clean(w)) + "\n")
	}
	if len(d.Events) > 0 {
		b.WriteString("\n  " + i18n.T(i18n.TUIDetEvents) + "\n")
		b.WriteString(renderEvents(a, d.Events))
	}
	return b.String()
}

func renderRungs(a *app, rs []api.RungStatus, tr map[string]api.TransportInfo) string {
	rs = append([]api.RungStatus(nil), rs...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Node < rs[j].Node })
	nw, tw := 0, 0
	for _, r := range rs {
		nw, tw = max(nw, width(r.Node)), max(tw, width(r.Transport))
	}
	var b strings.Builder
	for _, r := range rs {
		st := ""
		switch {
		case r.Active:
			st = a.paint(colGreen, i18n.T(i18n.TUIDetActive))
		case r.Skipped != "":
			st = a.paint(colYellow, i18n.T(i18n.TUIDetSkipped, r.Skipped))
		case r.Quarantine.After(a.opts.Now()):
			st = a.paint(colYellow, i18n.T(i18n.TUIDetQuarantine, r.Quarantine.In(a.opts.Location).Format("15:04:05")))
		case r.Warm:
			st = i18n.T(i18n.TUIDetWarm)
		}
		line := "    " + pad(r.Node, nw+2) + pad(r.Transport, tw+2) + st
		if n := clientIPNote(tr, r.Transport); n != "" {
			line += "  " + a.paint(colGray, n)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
