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
	// tr is the last answer of TrafficBlockQuery (the sparklines of the
	// TRAFFIC block), fetched at trAt; it is kept when a later call fails.
	// trOff is set once the daemon answers that it has no traffic
	// statistics (DEY-X008, DEY-X009): it is not asked again.
	tr    *api.TrafficReport
	trAt  time.Time
	trOff bool
}

// dashTrafficEvery is how often the dashboard reloads its sparklines
// (2-minute points); the rates come with every Status.
const dashTrafficEvery = 30 * time.Second

// dashData is one dashboard refresh: Status and, when it was due, the
// sparklines (a failed Traffic call never hides the dashboard).
type dashData struct {
	st      api.Status
	tr      *api.TrafficReport
	trErr   error
	fetched bool
}

func newDashboard(*app) screen {
	return &dashboard{screenBase: screenBase{title: i18n.T(i18n.MenuDashboard), help: i18n.TUIHelpDashboard}}
}

func (d *dashboard) start(a *app) tea.Cmd { return d.load(a) }

// load fetches Status and, every dashTrafficEvery while a tunnel has
// traffic numbers, the sparklines, in one operation (a second operation of
// the screen would cancel the first).
func (d *dashboard) load(a *app) tea.Cmd {
	d.loading = true
	traffic := !d.trOff && (d.trAt.IsZero() || a.opts.Now().Sub(d.trAt) >= dashTrafficEvery)
	return a.call(d, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		st, err := l.Status(ctx)
		if err != nil {
			return nil, err
		}
		out := dashData{st: st}
		if traffic && HasTraffic(st.Tunnels) {
			rep, err := l.Traffic(ctx, TrafficBlockQuery())
			out.fetched, out.trErr = true, err
			if err == nil {
				out.tr = &rep
			}
		}
		return out, nil
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
		if v, ok := p.v.(dashData); ok && p.err == nil {
			st := v.st
			d.st = &st
			d.at = a.opts.Now()
			a.applyStatus(st)
			if v.fetched {
				d.trAt = d.at
				switch {
				case v.trErr == nil:
					d.tr = v.tr
				case !retryable(v.trErr):
					d.trOff, d.tr = true, nil
				}
			}
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
			case "t":
				if a.role() == roleHub {
					return a.push(trafficPicker())
				}
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
	now := a.opts.Now()
	rows := 0 // no limit
	if a.sized && !a.lineMode && HasTraffic(d.st.Tunnels) {
		rows = d.trafficRows(a, b.String(), now)
	}
	b.WriteString(renderDashboard(a, *d.st, d.tr, now, rows))
	at := d.at.In(a.opts.Location).Format("15:04:05")
	updated := i18n.T(i18n.TUIDashUpdated, at, int(a.opts.Refresh/time.Second))
	if a.lineMode {
		updated = i18n.T(i18n.TUIDashUpdatedManual, at)
	}
	if HasTraffic(d.st.Tunnels) && a.role() == roleHub {
		updated += i18n.T(i18n.TUITrafficDashKey)
	}
	b.WriteString("\n" + a.paint(colGray, " "+updated) + "\n")
	return b.String()
}

// dashChrome is the number of page lines around a screen body once fit
// dropped the banner art: the status line, a blank line, the title, a
// blank line, and the blank line and footer under the body.
const dashChrome = 6

// trafficRows is how many lines the TRAFFIC block may take on a window of
// a.height lines (head is what the body shows above the dashboard): what
// is left after everything else, so the events and the warnings stay on
// screen. -1 leaves the block out (its title and one tunnel do not fit).
func (d *dashboard) trafficRows(a *app, head string, now time.Time) int {
	rest := renderDashboard(a, *d.st, nil, now, -1)
	used := dashChrome + strings.Count(head, "\n") + strings.Count(rest, "\n") + 2 // blank line and "Updated"
	free := a.height - used - 1                                                    // the block's title
	if free < 1 {
		return -1
	}
	return free
}

// renderStatus renders the dashboard body of the spec sample:
// TUNNELS, NODES, LAST EVENTS and yellow warnings under the tables (and
// the TRAFFIC block, without sparklines, when a tunnel has traffic
// numbers).
func renderStatus(a *app, st api.Status, now time.Time) string {
	return renderDashboard(a, st, nil, now, 0)
}

// renderDashboard is renderStatus with the TRAFFIC block under TUNNELS: tr
// holds its sparklines (nil = none yet) and trafficRows bounds its lines
// (0 = no limit, -1 = left out; see TrafficBlock).
func renderDashboard(a *app, st api.Status, tr *api.TrafficReport, now time.Time, trafficRows int) string {
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
	if lines := TrafficBlock(a.caps, st.Tunnels, tr, trafficRows); len(lines) > 0 {
		title, hint := TrafficBlockTitle(a.caps)
		b.WriteString(clipANSI(a, " "+a.bold(title)+"  "+a.paint(colGray, hint)) + "\n")
		for _, l := range lines {
			b.WriteString(l + "\n")
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
	verOf := func(n api.NodeInfo) string {
		if n.Version == "" {
			return i18n.T(i18n.TUIDash)
		}
		return i18n.T(i18n.TUINodeVersion, strings.TrimPrefix(n.Version, "v"))
	}
	// A pre-release version (0.3.0-edge.18) is longer than the usual 9.
	idW, nameW, ipW, verW := 0, 0, 0, 9
	for _, n := range ns {
		idW = max(idW, width(n.ID))
		nameW = max(nameW, width(n.Name))
		ipW = max(ipW, width(n.PublicIP))
		verW = max(verW, width(verOf(n))+1)
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
		ver := verOf(n)
		if !n.Compatible && n.Version != "" {
			ver = a.paint(colYellow, ver) + strings.Repeat(" ", max(0, verW-width(ver)))
		} else {
			ver = pad(ver, verW)
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

// eventWords is the short word of every event type in LAST EVENTS.
var eventWords = map[string]i18n.Key{
	state.EvTunnelUp:          i18n.TUIEvUp,
	state.EvTunnelDegraded:    i18n.TUIEvDegraded,
	state.EvTunnelDown:        i18n.TUIEvDown,
	state.EvSwitchTransport:   i18n.TUIEvSwitch,
	state.EvSwitchNode:        i18n.TUIEvSwitch,
	state.EvFailback:          i18n.TUIEvFailback,
	state.EvFailbackFailed:    i18n.TUIEvFailbackFailed,
	state.EvFlapping:          i18n.TUIEvFlapping,
	state.EvNodeOnline:        i18n.TUIEvNodeOnline,
	state.EvNodeOffline:       i18n.TUIEvNodeOffline,
	state.EvServiceDown:       i18n.TUIEvServiceDown,
	state.EvBackendCrash:      i18n.TUIEvBackendCrash,
	state.EvProbeError:        i18n.TUIEvProbeError,
	state.EvUpdateApplied:     i18n.TUIEvUpdated,
	state.EvUpdateRolledBack:  i18n.TUIEvRolledBack,
	state.EvBackendRolledBack: i18n.TUIEvBackendRollback,
	state.EvNodeIPChanged:     i18n.TUIEvNodeIP,
	state.EvACMEFailed:        i18n.TUIEvACMEFailed,
	state.EvRungSkipped:       i18n.TUIEvRungSkipped,
	state.EvRungRestored:      i18n.TUIEvRungRestored,
	state.EvConfigApplied:     i18n.TUIEvConfigApplied,
	state.EvTrafficQuota:      i18n.TUIEvTrafficQuota,
	state.EvTuneDrift:         i18n.TUIEvTuneDrift,
	eventUpdateAvailable:      i18n.TUIEvUpdateAvailable,
}

// eventUpdateAvailable is the hub's notice of a new release (an event type
// of the hub daemon, not of the failover engine).
const eventUpdateAvailable = "update_available"

// Column caps of LAST EVENTS: a long tunnel id or an unknown event type is
// cut with an ellipsis instead of pushing the messages off the screen.
const (
	eventWhoCap  = 12
	eventTypeCap = 12
)

// EventWord is the short word of an event type shown in LAST EVENTS
// ("switch", "node offline"); an unknown type is shown with spaces for
// its underscores. The CLI status dashboard uses it too.
func EventWord(t string) string {
	if k, ok := eventWords[t]; ok {
		return i18n.T(k)
	}
	return strings.ReplaceAll(t, "_", " ")
}

// EventMessage is the text of an event: its message, else its reason; a
// message that only repeats the type (the hub's default) is dropped.
func EventMessage(e state.Event) string {
	msg := e.Message
	if msg == "" || msg == e.Type {
		msg = e.Reason
	}
	return clean(msg)
}

// renderEvents renders LAST EVENTS in local time. The tunnel (or node) and
// type columns are as wide as their longest cell up to a cap.
func renderEvents(a *app, evs []state.Event) string {
	ell := a.sym().ell
	whoW, typW := 0, 0
	for _, e := range evs {
		whoW = max(whoW, min(eventWhoCap, width(eventWho(e))))
		typW = max(typW, min(eventTypeCap, width(EventWord(e.Type))))
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
		typ := trunc(EventWord(e.Type), typW, ell)
		cell := pad(typ, typW+3)
		if col != "" {
			cell = a.paint(col, typ) + strings.Repeat(" ", typW+3-width(typ))
		}
		who := pad(trunc(eventWho(e), whoW, ell), whoW+3)
		line := "  " + e.At.In(a.opts.Location).Format("15:04:05") + "  " + who + cell + EventMessage(e)
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
	hub := n.HubAddr
	if n.Front {
		hub += " (" + i18n.T(i18n.CLIViaFront) + ")"
	}
	line := "  " + n.ID + "  " + i18n.T(i18n.TUIDashHub, hub) + "  " + conn
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
