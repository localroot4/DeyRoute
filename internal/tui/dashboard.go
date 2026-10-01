package tui

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/state"
)

// dashboard is screen 1: Status() every 2 seconds (section 6).
type dashboard struct {
	screenBase
	st      *api.Status
	at      time.Time
	err     error
	loading bool
	seq     int
}

func newDashboard(*app) screen {
	return &dashboard{screenBase: screenBase{title: i18n.T(i18n.MenuDashboard), help: i18n.TUIHelpDashboard}}
}

func (d *dashboard) start(a *app) tea.Cmd { return d.load(a) }

func (d *dashboard) load(a *app) tea.Cmd {
	d.loading = true
	return a.call(d, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.Status(ctx)
	})
}

func (d *dashboard) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		p, ok := msg.payload.(donePayload)
		if !ok {
			return nil
		}
		d.loading = false
		d.err = p.err
		if st, ok := p.v.(api.Status); ok && p.err == nil {
			d.st = &st
			d.at = a.opts.Now()
			a.applyStatus(st)
		}
		d.seq++
		return a.opts.tick(a.opts.Refresh, tickMsg{sid: d.id, seq: d.seq})
	case tickMsg:
		if msg.seq == d.seq {
			return d.load(a)
		}
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			return a.pop()
		case tea.KeyRunes:
			switch msg.String() {
			case "r":
				return d.load(a)
			case "0":
				return a.pop()
			}
		}
	}
	return nil
}

func (d *dashboard) view(a *app) string {
	var b strings.Builder
	if d.err != nil {
		b.WriteString(a.errBlock(d.err) + "\n")
	}
	if d.st == nil {
		if d.loading {
			b.WriteString(" " + i18n.T(i18n.Loading) + "\n")
		}
		return b.String()
	}
	b.WriteString(renderStatus(a, *d.st, a.opts.Now()))
	at := d.at.In(a.opts.Location).Format("15:04:05")
	updated := i18n.T(i18n.TUIDashUpdated, at, int(a.opts.Refresh/time.Second))
	if a.lineMode {
		updated = i18n.T(i18n.TUIDashUpdatedManual, at)
	}
	b.WriteString("\n" + a.paint(colGray, " "+updated) + "\n")
	return b.String()
}

// renderStatus renders the dashboard body of the spec sample:
// TUNNELS, NODES, LAST EVENTS and yellow warnings under the tables.
func renderStatus(a *app, st api.Status, now time.Time) string {
	var b strings.Builder
	if st.NodeSelf != nil {
		b.WriteString(" " + a.bold(i18n.T(i18n.TUIDashNode)) + "\n")
		b.WriteString(renderNodeSelf(a, *st.NodeSelf))
	}
	if st.NodeSelf == nil || len(st.Tunnels) > 0 {
		b.WriteString(" " + a.bold(i18n.T(i18n.TUIDashTunnels)) + "\n")
		if len(st.Tunnels) == 0 {
			b.WriteString("  " + i18n.T(i18n.TUIDashNoTunnels) + "\n")
		} else {
			b.WriteString(renderTunnelTable(a, st.Tunnels, now))
		}
	}
	if st.NodeSelf == nil {
		b.WriteString(" " + a.bold(i18n.T(i18n.TUIDashNodes)) + "\n")
		if len(st.Nodes) == 0 {
			b.WriteString("  " + i18n.T(i18n.TUIDashNoNodes) + "\n")
		} else {
			b.WriteString(renderNodeTable(a, st.Nodes))
		}
	}
	b.WriteString(" " + a.bold(i18n.T(i18n.TUIDashEvents)) + "\n")
	if len(st.Events) == 0 {
		b.WriteString("  " + i18n.T(i18n.TUIDashNoEvents) + "\n")
	} else {
		b.WriteString(renderEvents(a, st.Events))
	}
	b.WriteString(renderWarnings(a, st))
	return b.String()
}

// stateLook returns the symbol, the word and the color of a tunnel state.
// The word is always shown; color is never the only signal.
func (a *app) stateLook(t api.TunnelInfo) (sym, word, col string) {
	s := a.sym()
	switch {
	case !t.Enabled || t.State == state.StateDisabled:
		return s.down, i18n.T(i18n.TUIStateDisabled), colGray
	case t.Paused || t.State == state.StatePaused:
		return s.half, i18n.T(i18n.TUIStatePaused), colGray
	}
	switch t.State {
	case state.StateUp:
		return s.up, i18n.T(i18n.TUIStateUp), colGreen
	case state.StateDegraded:
		if t.ServiceDown {
			// The tunnel path works; the service behind the node does not.
			return s.half, i18n.T(i18n.TUIStateDegradedSvc), colYellow
		}
		return s.half, i18n.T(i18n.TUIStateDegraded), colYellow
	case state.StateSwitching:
		return s.half, i18n.T(i18n.TUIStateSwitching), colBlue
	case state.StateStarting:
		return s.half, i18n.T(i18n.TUIStateStarting), colBlue
	case state.StateDown:
		return s.down, i18n.T(i18n.TUIStateDown), colRed
	case state.StateInit, "":
		return s.down, i18n.T(i18n.TUIStateInit), colGray
	}
	return s.down, t.State, colGray
}

// stateText is "● UP" without color (pick lists, summaries).
func (a *app) stateText(t api.TunnelInfo) string {
	sy, w, _ := a.stateLook(t)
	return sy + " " + w
}

// portsText lists the listen ports of a tunnel: "443,2053,27015/udp".
func portsText(ps []api.PortMapDTO) string {
	specs := make([]ports.Spec, 0, len(ps))
	for _, p := range ps {
		specs = append(specs, ports.Spec{Listen: p.Listen, Proto: p.Proto})
	}
	return ports.FormatList(specs)
}

// tableCol is one column of a table: its preferred minimum width, an
// optional cap (longer cells are cut with an ellipsis, as in the section 6
// sample) and the floor it may shrink to on a narrow terminal.
type tableCol struct {
	title string
	min   int
	cap   int
	floor int
	cells []string
	w     int
}

// layoutCols computes column widths: at least min, grown to content + 2,
// then shrunk (down to min) until the line fits the terminal.
func (a *app) layoutCols(cols []*tableCol, lead int) {
	total := lead
	for _, c := range cols {
		c.w = max(c.min, width(c.title)+2)
		for _, s := range c.cells {
			c.w = max(c.w, width(s)+2)
		}
		if c.cap > 0 {
			c.w = min(c.w, c.cap)
		}
		if c.floor == 0 {
			c.floor = c.w // not shrinkable
		} else {
			c.floor = max(c.floor, width(c.title)+1)
		}
		total += c.w
	}
	if a.caps.Width <= 0 {
		return
	}
	for total > a.caps.Width {
		var widest *tableCol
		for _, c := range cols {
			if c.w > c.floor && (widest == nil || c.w-c.floor > widest.w-widest.floor) {
				widest = c
			}
		}
		if widest == nil {
			return
		}
		widest.w--
		total--
	}
}

// renderTunnelTable renders the TUNNELS table. Below 100 columns the RTT and
// UP-TIME columns are dropped instead of breaking lines.
func renderTunnelTable(a *app, ts []api.TunnelInfo, now time.Time) string {
	narrow := a.caps.Narrow()
	ell := a.sym().ell
	num := len(strconv.Itoa(len(ts)))
	name := &tableCol{title: i18n.T(i18n.TUIColName), min: 16, cap: 16, floor: 12}
	node := &tableCol{title: i18n.T(i18n.TUIColNode), min: 16, cap: 16, floor: 12}
	tr := &tableCol{title: i18n.T(i18n.TUIColTransport), min: 19, floor: 14}
	stc := &tableCol{title: i18n.T(i18n.TUIColState), min: 8}
	rtt := &tableCol{title: i18n.T(i18n.TUIColRTT), min: 7}
	up := &tableCol{title: i18n.T(i18n.TUIColUptime), min: 11}
	colors := make([]string, len(ts))
	dash := i18n.T(i18n.TUIDash)
	for i, t := range ts {
		n := t.Name
		if n == "" {
			n = t.ID
		}
		name.cells = append(name.cells, n)
		nd := dash
		if t.ActiveNode != "" {
			nd = strings.TrimSpace(t.ActiveNode + " " + t.ActiveNodeName)
		}
		node.cells = append(node.cells, nd)
		trs := t.ActiveTransport
		if trs == "" {
			trs = dash
		}
		tr.cells = append(tr.cells, trs)
		sy, w, col := a.stateLook(t)
		stc.cells = append(stc.cells, sy+" "+w)
		colors[i] = col
		live := t.Enabled && (t.State == state.StateUp || t.State == state.StateDegraded)
		r, u := dash, dash
		if live && t.RTTms > 0 {
			r = ms(t.RTTms)
		}
		if live && !t.UpSince.IsZero() {
			u = upTime(now.Sub(t.UpSince))
		}
		rtt.cells = append(rtt.cells, r)
		up.cells = append(up.cells, u)
	}
	cols := []*tableCol{name, node, tr, stc}
	if !narrow {
		cols = append(cols, rtt, up)
	}
	portsCells := make([]string, len(ts))
	portsW := width(i18n.T(i18n.TUIColPorts))
	for i, t := range ts {
		portsCells[i] = portsText(t.Ports)
		portsW = max(portsW, width(portsCells[i]))
	}
	a.layoutCols(cols, 2+num+2+portsW)
	var b strings.Builder
	hdr := "  " + padLeft(i18n.T(i18n.TUIColNum), num) + "  "
	for _, c := range cols {
		hdr += pad(c.title, c.w)
	}
	b.WriteString(a.clip(hdr+i18n.T(i18n.TUIColPorts)) + "\n")
	for i := range ts {
		line := "  " + padLeft(strconv.Itoa(i+1), num) + "  "
		for _, c := range cols {
			cell := pad(trunc(c.cells[i], c.w-2, ell), c.w)
			if c == stc && a.caps.Color {
				cell = a.paint(colors[i], strings.TrimRight(cell, " ")) + strings.Repeat(" ", c.w-width(strings.TrimRight(cell, " ")))
			}
			line += cell
		}
		line += portsCells[i]
		b.WriteString(clipANSI(a, line) + "\n")
	}
	return b.String()
}

func padLeft(s string, w int) string {
	if n := width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// renderNodeTable renders the NODES table of the dashboard. Below 100
// columns the control RTT column is dropped.
func renderNodeTable(a *app, ns []api.NodeInfo) string {
	narrow := a.caps.Narrow()
	s := a.sym()
	idW, nameW, ipW := 0, 0, 0
	for _, n := range ns {
		idW = max(idW, width(n.ID))
		nameW = max(nameW, width(n.Name))
		ipW = max(ipW, width(n.PublicIP))
	}
	var b strings.Builder
	for _, n := range ns {
		stSym, stWord, col := s.up, i18n.T(i18n.TUIOnline), colGreen
		if !n.Online {
			stSym, stWord, col = s.down, i18n.T(i18n.TUIOffline), colRed
		}
		stCell := stSym + " " + stWord
		line := "  " + pad(n.ID, idW+2) + pad(n.Name, nameW+2) + pad(n.PublicIP, ipW+3)
		line += a.paint(col, stCell) + strings.Repeat(" ", max(0, 11-width(stCell)))
		if !narrow {
			ctl := i18n.T(i18n.TUIDash)
			if n.Online && n.ControlRTTms > 0 {
				ctl = ms(n.ControlRTTms)
			}
			line += pad(i18n.T(i18n.TUINodeCtl, ctl), 11)
		}
		ver := i18n.T(i18n.TUIDash)
		if n.Version != "" {
			ver = i18n.T(i18n.TUINodeVersion, strings.TrimPrefix(n.Version, "v"))
		}
		if !n.Compatible && n.Version != "" {
			ver = a.paint(colYellow, ver) + strings.Repeat(" ", max(0, 9-width(ver)))
		} else {
			ver = pad(ver, 9)
		}
		line += ver
		line += pad(i18n.T(i18n.TUINodeCPU, n.CPUPercent), 8)
		line += i18n.T(i18n.TUINodeRAM, n.RAMBytes/(1<<20))
		b.WriteString(clipANSI(a, line) + "\n")
	}
	return b.String()
}

// clipANSI clips a line that may contain color codes; colored lines are
// only clipped when they really exceed the width (lipgloss measures them).
func clipANSI(a *app, line string) string {
	if a.caps.Width <= 1 {
		return line
	}
	measured := line
	if !a.caps.Unicode {
		// Measure what the terminal really prints: "→" becomes "->".
		measured = asciiOnly(line)
	}
	if width(measured) <= a.caps.Width {
		return line
	}
	return a.clip(stripANSI(line))
}

// stripANSI removes SGR color sequences.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// eventWord is the short type shown in LAST EVENTS ("switch", "down").
func eventWord(t string) string {
	switch t {
	case state.EvTunnelUp:
		return i18n.T(i18n.TUIEvUp)
	case state.EvTunnelDegraded:
		return i18n.T(i18n.TUIEvDegraded)
	case state.EvTunnelDown:
		return i18n.T(i18n.TUIEvDown)
	case state.EvSwitchTransport, state.EvSwitchNode:
		return i18n.T(i18n.TUIEvSwitch)
	}
	return t
}

// renderEvents renders LAST EVENTS in local time.
func renderEvents(a *app, evs []state.Event) string {
	whoW, typW := 0, 0
	for _, e := range evs {
		whoW = max(whoW, width(eventWho(e)))
		typW = max(typW, width(eventWord(e.Type)))
	}
	var b strings.Builder
	for _, e := range evs {
		col := ""
		switch e.Level {
		case state.LevelError:
			col = colRed
		case state.LevelWarn:
			col = colYellow
		}
		typ := eventWord(e.Type)
		cell := pad(typ, typW+3)
		if col != "" {
			cell = a.paint(col, typ) + strings.Repeat(" ", typW+3-width(typ))
		}
		msg := e.Message
		if msg == "" {
			msg = e.Reason
		}
		line := "  " + e.At.In(a.opts.Location).Format("15:04:05") + "  " + pad(eventWho(e), whoW+3) + cell + clean(msg)
		b.WriteString(clipANSI(a, line) + "\n")
	}
	return b.String()
}

func eventWho(e state.Event) string {
	switch {
	case e.Tunnel != "":
		return e.Tunnel
	case e.Node != "":
		return e.Node
	}
	return i18n.T(i18n.TUIDash)
}

// renderWarnings renders the yellow warnings under the tables.
func renderWarnings(a *app, st api.Status) string {
	var lines []string
	w := a.sym().warn
	for _, x := range st.Warnings {
		msg := clean(x.Message)
		if x.Code != "" {
			msg = x.Code + " " + msg
		}
		switch {
		case x.Tunnel != "":
			msg = x.Tunnel + ": " + msg
		case x.Node != "":
			msg = x.Node + ": " + msg
		}
		lines = append(lines, msg)
	}
	for _, t := range st.Tunnels {
		for _, m := range t.Warnings {
			lines = append(lines, t.ID+": "+clean(m))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, l := range lines {
		b.WriteString(a.paint(colYellow, a.clip("  "+w+" "+l)) + "\n")
	}
	return b.String()
}

// renderNodeSelf renders the NODE section on a node server.
func renderNodeSelf(a *app, n api.NodeSelf) string {
	s := a.sym()
	conn := a.paint(colGreen, s.up+" "+i18n.T(i18n.TUIDashConnected))
	if !n.Connected {
		conn = a.paint(colRed, s.down+" "+i18n.T(i18n.TUIDashDisconnected))
	}
	line := "  " + n.ID + "  " + i18n.T(i18n.TUIDashHub, n.HubAddr) + "  " + conn
	if !n.LastContact.IsZero() {
		line += "  " + i18n.T(i18n.TUIDashLastContact, n.LastContact.In(a.opts.Location).Format("15:04:05"))
	}
	if n.HubVersion != "" {
		line += "  " + i18n.T(i18n.TUINodeVersion, strings.TrimPrefix(n.HubVersion, "v"))
	}
	out := line + "\n"
	if len(n.Units) > 0 {
		units := append([]string(nil), n.Units...)
		sort.Strings(units)
		out += "  " + i18n.T(i18n.TUIDashUnits, strings.Join(units, ", ")) + "\n"
	}
	return out
}
