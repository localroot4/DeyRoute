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

// tunnelLabels are the tunnels of a pick list in aligned columns:
// " 1) main   Main 443/2053  ● UP    443,2053".
func (a *app) tunnelLabels(ts []api.TunnelInfo) []string {
	rows := make([][]string, len(ts))
	for i, t := range ts {
		name := t.Name
		if name == t.ID {
			name = ""
		}
		rows[i] = []string{t.ID, name, a.stateText(t), portsText(t.Ports)}
	}
	return columns(rows)
}

// nodeState is "● online" or "○ offline".
func (a *app) nodeState(n api.NodeInfo) string {
	s := a.sym()
	if !n.Online {
		return s.down + " " + i18n.T(i18n.TUIOffline)
	}
	return s.up + " " + i18n.T(i18n.TUIOnline)
}

// nodeLabel is one node on its own: "de-1  Germany 1  ● online".
func (a *app) nodeLabel(n api.NodeInfo) string { return a.nodeLabels([]api.NodeInfo{n})[0] }

// nodeLabels are the nodes of a pick list in aligned columns: id, name,
// state.
func (a *app) nodeLabels(ns []api.NodeInfo) []string {
	rows := make([][]string, len(ns))
	for i, n := range ns {
		name := n.Name
		if name == n.ID {
			name = ""
		}
		rows[i] = []string{n.ID, name, a.nodeState(n)}
	}
	return columns(rows)
}

// columns joins the cells of every row in aligned columns two spaces
// apart; a column that is empty in every row takes no room.
func columns(rows [][]string) []string {
	var w []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(w) {
				w = append(w, 0)
			}
			w[i] = max(w[i], width(c))
		}
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		var b strings.Builder
		for j, c := range r {
			if w[j] > 0 {
				b.WriteString(pad(c, w[j]+2))
			}
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
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
			for i, label := range a.tunnelLabels(ts) {
				out = append(out, choice{label: label, value: ts[i]})
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
			all, _ := v.([]api.NodeInfo)
			var ns []api.NodeInfo
			for _, n := range all {
				if filter == nil || filter(n) {
					ns = append(ns, n)
				}
			}
			out := make([]choice, 0, len(ns))
			for i, label := range a.nodeLabels(ns) {
				out = append(out, choice{label: label, value: ns[i]})
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd { return then(a, c.value.(api.NodeInfo)) },
	}
}

// ---- 2 Tunnels

func tunnelsMenu(a *app) screen {
	sub := func(k i18n.Key) string { return subTitle(i18n.MenuTunnels, k) }
	m := newMenu(a, i18n.MenuTunnels, i18n.TUIHelpTunnels, []menuItem{
		{label: i18n.TUITunAdd, act: func(a *app) tea.Cmd { return a.push(newWizard()) }},
		{label: i18n.TUITunEdit, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUITunEdit), editTunnel))
		}},
		{label: i18n.TUITunToggle, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUITunToggle), toggleTunnel))
		}},
		{label: i18n.TUITunRestart, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUITunRestart), restartTunnel))
		}},
		{label: i18n.TUITunSwitch, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUITunSwitch), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(pickSwitch(t))
			}))
		}},
		{label: i18n.TUITunDelete, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUITunDelete), deleteTunnel))
		}},
		{label: i18n.TUITunShow, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUITunShow), func(a *app, t api.TunnelInfo) tea.Cmd {
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

// editData is what Edit tunnel loads first in Advanced mode: the tunnel
// (with its TLS mode) and the ladder profiles.
type editData struct {
	t       api.TunnelInfo
	tls     string
	ladders []api.Ladder
}

// editTunnel opens Edit tunnel. In Advanced mode the tunnel and the ladder
// profiles are loaded first, so that the fixed answers (policy, ladder
// profile, TLS mode) are numbered lists with the current value marked.
func editTunnel(a *app, t api.TunnelInfo) tea.Cmd {
	if !a.advanced {
		return a.push(editForm(a, editData{t: t}))
	}
	id := t.ID
	load := newTask(titleOf(i18n.TUITunEdit, id), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		d, err := l.TunnelShow(ctx, id)
		if err != nil {
			return nil, err
		}
		lads, err := l.LadderList(ctx)
		if err != nil {
			return nil, err
		}
		return editData{t: d.TunnelInfo, tls: d.TLSMode, ladders: lads}, nil
	}, nil)
	load.cancellable = true // reads only; the form comes next
	load.next = func(a *app, v any) screen {
		d, _ := v.(editData)
		return editForm(a, d)
	}
	return a.push(load)
}

// policyOpts are the failover policies as numbered answers; cur is marked
// "(current)".
func policyOpts(cur string) []fieldOpt {
	var out []fieldOpt
	for _, p := range []struct {
		id  string
		key i18n.Key
	}{
		{config.PolicyTransportThenNode, i18n.TUIPolTTN},
		{config.PolicyTransportOnly, i18n.TUIPolTO},
		{config.PolicyNodeOnly, i18n.TUIPolNO},
	} {
		out = append(out, fieldOpt{value: p.id, label: markCurrent(i18n.T(p.key), p.id == cur)})
	}
	return out
}

// tlsOpts are the TLS modes as numbered answers (custom only where the
// certificate files can be given too); cur is marked "(current)".
func tlsOpts(custom bool, cur string) []fieldOpt {
	out := []fieldOpt{
		{value: config.TLSModeAuto, label: markCurrent(i18n.T(i18n.TUITLSAuto), cur == config.TLSModeAuto)},
		{value: config.TLSModeACME, label: markCurrent(i18n.T(i18n.TUITLSACME), cur == config.TLSModeACME)},
	}
	if custom {
		out = append(out, fieldOpt{value: config.TLSModeCustom, label: markCurrent(i18n.T(i18n.TUITLSCustom), cur == config.TLSModeCustom)})
	}
	return out
}

// ladderOpts are the ladder profiles as numbered answers. A tunnel with
// its own rung order (no profile) can keep it: the first answer, empty.
func ladderOpts(ls []api.Ladder, cur string) []fieldOpt {
	var out []fieldOpt
	if cur == "" {
		out = append(out, fieldOpt{label: markCurrent(i18n.T(i18n.TUIEditLadderCustom), true)})
	}
	for _, l := range ls {
		label := l.Name
		if l.Builtin {
			label += i18n.T(i18n.TUILadBuiltin)
		}
		out = append(out, fieldOpt{value: l.Name, label: markCurrent(label, l.Name == cur)})
	}
	return out
}

// markCurrent appends " (current)" to the label of the current value.
func markCurrent(label string, cur bool) string {
	if cur {
		return label + i18n.T(i18n.TUICurrent)
	}
	return label
}

// editForm edits the mutable fields of a tunnel (TunnelEdit). Enter keeps
// every current value; only what changed is sent.
func editForm(a *app, d editData) screen {
	t := d.t
	policy := orDefault(t.Policy, config.PolicyTransportThenNode)
	// With custom TLS in place, Enter on the file questions keeps the
	// files; switching to custom needs both.
	keepFiles := d.tls == config.TLSModeCustom
	fields := []field{{key: "name", label: i18n.T(i18n.TUIEditName), def: t.Name}}
	if a.advanced {
		fields = append(fields,
			field{key: "policy", label: i18n.T(i18n.TUIEditPolicy), def: policy, opts: policyOpts(policy)},
			field{key: "ladder", label: i18n.T(i18n.TUIEditLadder), def: t.LadderName, optional: true, opts: ladderOpts(d.ladders, t.LadderName)},
			field{key: "tls", label: i18n.T(i18n.TUIEditTLS), def: d.tls, optional: true, opts: tlsOpts(true, d.tls)},
			field{key: "cert", label: i18n.T(i18n.TUIEditTLSCert), optional: keepFiles, skip: notCustomTLS},
			field{key: "key", label: i18n.T(i18n.TUIEditTLSKey), optional: keepFiles, skip: notCustomTLS},
			field{key: "probe", label: i18n.T(i18n.TUIEditProbe), optional: true, check: optionalInt(1)},
		)
	}
	title := titleOf(i18n.TUITunEdit, t.ID)
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
		set(v["policy"], policy, &req.Policy)
		set(v["ladder"], t.LadderName, &req.Ladder)
		set(v["tls"], d.tls, &req.TLSMode)
		if v["tls"] == config.TLSModeCustom && (v["cert"] != "" || v["key"] != "") {
			set(config.TLSModeCustom, "", &req.TLSMode) // new files for custom TLS
			set(v["cert"], "", &req.TLSCert)
			set(v["key"], "", &req.TLSKey)
		}
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
	task := newTask(titleOf(i18n.TUITunToggle, id), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
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
	title := titleOf(i18n.TUITunRestart, id)
	return a.push(newConfirm(title, i18n.T(i18n.TUITunRestartConfirm, id), false, func(a *app) tea.Cmd {
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.TunnelRestart(ctx, id)
		}, textResult(i18n.T(i18n.TUITunRestarted, id))))
	}))
}

func deleteTunnel(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := titleOf(i18n.TUITunDelete, id)
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

// clientIPNote is "client IP: preserved|masked" for a transport (section
// 10). A transport that can keep the client IP keeps it only with the
// tunnel's advanced.proxy_protocol (proxy); without a tunnel (the Add
// tunnel wizard) proxy is false and the note says how to keep it.
func clientIPNote(tr map[string]api.TransportInfo, id string, proxy bool) string {
	x, ok := tr[id]
	if !ok {
		return ""
	}
	w := i18n.T(i18n.TUIClientMasked)
	switch {
	case x.ClientIPPreserved && proxy:
		w = i18n.T(i18n.TUIClientKept)
	case x.ClientIPPreserved:
		w = i18n.T(i18n.TUIClientMaskedPP)
	}
	return i18n.T(i18n.TUIClientIP, w)
}

func pickSwitch(t api.TunnelInfo) *listScreen {
	id := t.ID
	title := titleOf(i18n.TUITunSwitch, id)
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
				label := pad(r, w+2) + clientIPNote(d.tr, r, d.t.ProxyProtocol)
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
	t := newTask(titleOf(i18n.TUITunShow, id), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
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
	var t kvTable
	t.add(i18n.T(i18n.TUIDetID), d.ID)
	if d.Name != "" {
		t.add(i18n.T(i18n.TUIDetName), d.Name)
	}
	sy, w, col := a.stateLook(d.TunnelInfo)
	st := a.paint(col, sy+" "+w)
	if d.ActiveTransport != "" {
		st += "  " + i18n.T(i18n.TUIDetVia, d.ActiveTransport, d.ActiveNode)
		if d.RTTms > 0 {
			st += " (" + ms(d.RTTms) + ")"
		}
	}
	t.add(i18n.T(i18n.TUIDetState), st)
	var ps []string
	for _, p := range d.Ports {
		x := strconv.Itoa(p.Listen) + "/" + p.Proto + " " + a.sym().arrow + " " + p.Target
		if a.advanced && p.Probe != "" && p.Probe != config.ProbeAuto {
			x += " (" + i18n.T(i18n.TUIWizSumProbe, p.Probe) + ")"
		}
		ps = append(ps, x)
	}
	t.add(i18n.T(i18n.TUIDetPorts), strings.Join(ps, ", "))
	var ns []string
	for i, n := range d.Nodes {
		role := i18n.T(i18n.TUIDetBackup)
		if i == 0 {
			role = i18n.T(i18n.TUIDetPrimary)
		}
		ns = append(ns, n+" ("+role+")")
	}
	t.add(i18n.T(i18n.TUIDetNodes), strings.Join(ns, ", "))
	if d.ClientIP != "" {
		t.add(i18n.T(i18n.TUIDetClientIP), d.ClientIP)
	}
	if a.advanced {
		t.add(i18n.T(i18n.TUIDetPolicy), d.Policy)
		lad := strings.Join(d.Ladder, " "+a.sym().arrow+" ")
		if d.LadderName != "" {
			lad = i18n.T(i18n.TUITitleOf, d.LadderName, lad)
		}
		t.add(i18n.T(i18n.TUIDetLadder), lad)
		if d.TLSMode != "" {
			t.add(i18n.T(i18n.TUIDetTLS), d.TLSMode)
		}
	}
	b.WriteString(t.String())
	if a.advanced && len(d.Rungs) > 0 {
		b.WriteString("\n  " + i18n.T(i18n.TUIDetRungs) + "\n")
		b.WriteString(renderRungs(a, d.Rungs, td.tr, d.ProxyProtocol))
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

func renderRungs(a *app, rs []api.RungStatus, tr map[string]api.TransportInfo, proxy bool) string {
	rs = append([]api.RungStatus(nil), rs...)
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Node < rs[j].Node })
	nw, tw, sw := 0, 0, 0
	sts := make([]string, len(rs))
	cols := make([]string, len(rs))
	for i, r := range rs {
		nw, tw = max(nw, width(r.Node)), max(tw, width(r.Transport))
		switch {
		case r.Active:
			sts[i], cols[i] = i18n.T(i18n.TUIDetActive), colGreen
		case r.Skipped != "":
			sts[i], cols[i] = i18n.T(i18n.TUIDetSkipped, clean(r.Skipped)), colYellow
		case r.Quarantine.After(a.opts.Now()):
			sts[i], cols[i] = i18n.T(i18n.TUIDetQuarantine, r.Quarantine.In(a.opts.Location).Format("15:04:05")), colYellow
		case r.Warm:
			sts[i] = i18n.T(i18n.TUIDetWarm)
		}
		sw = max(sw, width(sts[i]))
	}
	var b strings.Builder
	for i, r := range rs {
		// The status is padded before it is colored, so that the client-IP
		// notes after it line up.
		st := a.paint(cols[i], sts[i]) + strings.Repeat(" ", sw-width(sts[i]))
		if cols[i] == "" {
			st = pad(sts[i], sw)
		}
		line := "    " + pad(r.Node, nw+2) + pad(r.Transport, tw+2) + st
		if n := clientIPNote(tr, r.Transport, proxy); n != "" {
			line += "  " + a.paint(colGray, n)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}
