package cli

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tui"
	"github.com/localroot4/deyroute/internal/version"
)

// clearScreen moves the cursor home and clears the terminal.
const clearScreen = "\x1b[H\x1b[2J"

func newStatusCmd(g *Globals) *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:     "status",
		Short:   i18n.T(i18n.CLIStatusShort),
		Long:    i18n.T(i18n.CLIStatusLong),
		Example: i18n.T(i18n.CLIStatusExample),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := g.local()
			if err != nil {
				return err
			}
			if watch {
				return g.watchStatus(cmd.Context(), l)
			}
			ctx, cancel := callCtx(cmd.Context())
			defer cancel()
			st, err := l.Status(ctx)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(st)
			}
			g.printf("%s", g.dashboard(st, g.sparklines(ctx, l, st)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, i18n.T(i18n.CLIFlagWatch))
	return cmd
}

// watchStatus refreshes the dashboard every WatchInterval until ctx ends
// (Ctrl-C). A failed refresh is shown and retried; the loop never exits on
// its own.
func (g *Globals) watchStatus(ctx context.Context, l api.Local) error {
	t := time.NewTicker(g.WatchInterval)
	defer t.Stop()
	for {
		cctx, cancel := callCtx(ctx)
		st, err := l.Status(cctx)
		var tr *api.TrafficReport
		if err == nil && !g.JSON {
			tr = g.sparklines(cctx, l, st)
		}
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case g.JSON && err != nil:
			_ = g.emitJSONLine(map[string]any{"error": api.ToDTO(err)})
		case g.JSON:
			_ = g.emitJSONLine(st)
		default:
			if g.OutTTY && !g.caps().Dumb {
				g.printf("%s", clearScreen)
			}
			if err != nil {
				for _, e := range deyErrors(err) {
					g.printf("%s", g.text(e.Format(g.unicode())))
				}
			} else {
				g.printf("%s", g.dashboard(st, tr))
			}
			g.println(g.text(" " + i18n.T(i18n.CLIWatchFooter, localTime(g.Now(), "15:04:05"), int(g.WatchInterval/time.Second))))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// sparklines returns the last hour of every tunnel for the TRAFFIC block
// of the dashboard, nil when no tunnel has traffic numbers or the call
// fails (the block then shows the rates without sparklines).
func (g *Globals) sparklines(ctx context.Context, l api.Local, st api.Status) *api.TrafficReport {
	if !tui.HasTraffic(st.Tunnels) {
		return nil
	}
	rep, err := l.Traffic(ctx, tui.TrafficBlockQuery())
	if err != nil {
		return nil
	}
	return &rep
}

// dashboard renders the dashboard of spec section 6 as plain text: the
// banner status line, TUNNELS, the TRAFFIC block (when a tunnel has traffic
// numbers; tr holds its sparklines), NODES (hub) or NODE (node), LAST
// EVENTS and the yellow warnings. It uses the same strings and layout as
// the TUI.
func (g *Globals) dashboard(st api.Status, tr *api.TrafficReport) string {
	var b strings.Builder
	for _, l := range g.statusLines(st) {
		b.WriteString(l + "\n")
	}
	if line := frontLine(st); line != "" {
		b.WriteString(" " + g.fit(line) + "\n")
	}
	section := func(title, hint string) {
		b.WriteString("\n" + g.sectionHead(title, hint) + "\n")
	}
	if st.NodeSelf != nil {
		section(i18n.T(i18n.TUIDashNode), "")
		b.WriteString(g.nodeSelfLines(*st.NodeSelf))
	}
	if st.NodeSelf == nil || len(st.Tunnels) > 0 {
		section(i18n.T(i18n.TUIDashTunnels), "")
		if len(st.Tunnels) == 0 {
			b.WriteString("  " + i18n.T(i18n.CLIStatusNoTunnels) + "\n")
		} else {
			b.WriteString(g.tunnelTable(st.Tunnels))
		}
	}
	if lines := tui.TrafficBlock(g.outCaps(), st.Tunnels, tr, 0); len(lines) > 0 {
		title, hint := tui.TrafficBlockTitle(g.outCaps())
		section(strings.TrimSpace(title), strings.TrimSpace(hint))
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	if st.NodeSelf == nil {
		section(i18n.T(i18n.TUIDashNodes), "")
		if len(st.Nodes) == 0 {
			b.WriteString("  " + i18n.T(i18n.CLIStatusNoNodes) + "\n")
		} else {
			b.WriteString(g.nodeTable(st.Nodes))
		}
	}
	section(i18n.T(i18n.TUIDashEvents), "")
	if len(st.Events) == 0 {
		b.WriteString("  " + i18n.T(i18n.TUIDashNoEvents) + "\n")
	} else {
		b.WriteString(g.eventLines(st.Events))
	}
	if w := g.warningLines(st); w != "" {
		section(i18n.T(i18n.CLIStatusWarnings), "")
		b.WriteString(w)
	}
	return g.text(b.String())
}

// frontLine is the short front line of the hub dashboard ("" when front mode
// is off): the domain and port the nodes dial, whether the listener runs and
// who may reach it. The path secret is never part of the status.
func frontLine(st api.Status) string {
	if st.Hub == nil || st.Hub.Front == nil || !st.Hub.Front.Enabled {
		return ""
	}
	f := st.Hub.Front
	up := i18n.T(i18n.CLIStatusFrontUp)
	if !f.Listening {
		up = i18n.T(i18n.CLIStatusFrontDown)
	}
	who := i18n.T(i18n.CLIStatusFrontAll)
	if f.CFOnly {
		who = i18n.T(i18n.CLIStatusFrontCF)
	}
	return i18n.T(i18n.CLIStatusFront, f.Domain, f.Port, up, who, f.TLS)
}

// statusLines are the two lines on top of the dashboard: the product and
// version, then the server and the counts ("Hub: ir-1 (5.6.7.8) · Mode:
// Simple · nodes 2/2 online · tunnels 1/1 UP").
func (g *Globals) statusLines(st api.Status) []string {
	sep := "  " + g.sym().sep + "  "
	head := " " + g.styleOut(styleBold, i18n.T(i18n.BannerProduct)) + "  " + version.Display()
	var parts []string
	switch {
	case st.Hub != nil:
		parts = append(parts, i18n.T(i18n.BannerHub, st.Hub.Name, st.Hub.PublicIP))
		mode := i18n.T(i18n.ModeSimple)
		if st.Hub.UIMode == "advanced" {
			mode = i18n.T(i18n.ModeAdvanced)
		}
		online := 0
		for _, n := range st.Nodes {
			if n.Online {
				online++
			}
		}
		parts = append(parts, i18n.T(i18n.BannerMode, mode), i18n.T(i18n.CLIStatusNodesOnline, online, len(st.Nodes)))
	case st.NodeSelf != nil:
		parts = append(parts, i18n.T(i18n.BannerNode, st.NodeSelf.ID, hubLabel(*st.NodeSelf)))
	}
	if st.NodeSelf == nil {
		up, enabled := 0, 0
		for _, t := range st.Tunnels {
			if t.Enabled {
				enabled++
				if t.State == state.StateUp {
					up++
				}
			}
		}
		parts = append(parts, i18n.T(i18n.CLIStatusTunnelsUp, up, enabled))
	}
	lines := []string{head}
	for _, l := range joinWrap(parts, sep, g.lineWidth()-1) {
		lines = append(lines, " "+l)
	}
	return lines
}

// stateCell is "● UP": the word is always there, never only a color.
func (g *Globals) stateCell(t api.TunnelInfo) string {
	s := g.sym()
	switch {
	case !t.Enabled || t.State == state.StateDisabled:
		return s.down + " " + i18n.T(i18n.TUIStateDisabled)
	case t.Paused || t.State == state.StatePaused:
		return s.half + " " + i18n.T(i18n.TUIStatePaused)
	}
	switch t.State {
	case state.StateUp:
		return s.up + " " + i18n.T(i18n.TUIStateUp)
	case state.StateDegraded:
		if t.ServiceDown {
			return s.half + " " + i18n.T(i18n.TUIStateDegradedSvc)
		}
		return s.half + " " + i18n.T(i18n.TUIStateDegraded)
	case state.StateSwitching:
		return s.half + " " + i18n.T(i18n.TUIStateSwitching)
	case state.StateStarting:
		return s.half + " " + i18n.T(i18n.TUIStateStarting)
	case state.StateDown:
		return s.down + " " + i18n.T(i18n.TUIStateDown)
	case state.StateInit, "":
		return s.down + " " + i18n.T(i18n.TUIStateInit)
	}
	return s.down + " " + t.State
}

// portsText lists the listen ports of a tunnel: "443,2053,27015/udp".
func portsText(ps []api.PortMapDTO) string {
	specs := make([]ports.Spec, 0, len(ps))
	for _, p := range ps {
		specs = append(specs, ports.Spec{Listen: p.Listen, Proto: p.Proto})
	}
	return ports.FormatList(specs)
}

// narrow reports whether low-priority columns must go (< 100 columns).
func (g *Globals) narrow() bool { return g.caps().Narrow() }

// tunnelTable renders the TUNNELS table; below 100 columns RTT and UP-TIME
// are dropped instead of breaking lines.
func (g *Globals) tunnelTable(ts []api.TunnelInfo) string {
	narrow := g.narrow()
	ell := g.sym().ell
	now := g.Now()
	num := len(strconv.Itoa(len(ts)))
	tight := g.lineWidth() < 80 // a phone: the node id only, shorter names
	nodeCol := i18n.T(i18n.TUIColNode)
	if tight {
		nodeCol = i18n.T(i18n.CLIColNode)
	}
	header := []string{i18n.T(i18n.TUIColName), nodeCol, i18n.T(i18n.TUIColTransport), i18n.T(i18n.TUIColState)}
	if !narrow {
		header = append(header, i18n.T(i18n.TUIColRTT), i18n.T(i18n.TUIColUptime))
	}
	minW := []int{16, 16, 19, 8, 7, 11}
	caps := []int{16, 16, 0, 0, 0, 0}
	if tight {
		minW = []int{8, 6, 10, 6, 7, 11}
		caps = []int{12, 10, 17, 0, 0, 0}
	}
	rows := make([][]string, len(ts))
	for i, t := range ts {
		name := t.Name
		if name == "" {
			name = t.ID
		}
		node := t.ActiveNode
		if !tight && t.ActiveNodeName != "" && t.ActiveNodeName != t.ActiveNode {
			node += " " + t.ActiveNodeName
		}
		node = orDash(strings.TrimSpace(node))
		if t.ActiveNode == "" {
			node = orDash("")
		}
		row := []string{name, node, orDash(t.ActiveTransport), g.stateCell(t)}
		if !narrow {
			live := t.Enabled && (t.State == state.StateUp || t.State == state.StateDegraded)
			r, u := orDash(""), orDash("")
			if live && t.RTTms > 0 {
				r = ms(t.RTTms)
			}
			if live && !t.UpSince.IsZero() {
				u = upTime(now.Sub(t.UpSince))
			}
			row = append(row, r, u)
		}
		rows[i] = row
	}
	widths := make([]int, len(header))
	for c := range header {
		widths[c] = max(minW[c], width(header[c])+2)
		for _, r := range rows {
			widths[c] = max(widths[c], width(r[c])+2)
		}
		if caps[c] > 0 {
			widths[c] = min(widths[c], caps[c])
		}
	}
	var b strings.Builder
	h := "  " + padLeft(i18n.T(i18n.TUIColNum), num) + "  "
	for c, t := range header {
		h += pad(t, widths[c])
	}
	b.WriteString(h + i18n.T(i18n.TUIColPorts) + "\n")
	for i, r := range rows {
		line := "  " + padLeft(strconv.Itoa(i+1), num) + "  "
		for c, cell := range r {
			line += pad(trunc(cell, widths[c]-2, ell), widths[c])
		}
		b.WriteString(g.fit(strings.TrimRight(line+portsText(ts[i].Ports), " ")) + "\n")
	}
	return b.String()
}

// nodeTable renders the NODES table of the dashboard with a header row;
// the NAME column only when a name differs from its id, and below 100
// columns without CONTROL.
func (g *Globals) nodeTable(ns []api.NodeInfo) string {
	s := g.sym()
	named := false
	for _, n := range ns {
		if g.lineWidth() < 80 {
			break // a phone: the id is enough
		}
		named = named || (n.Name != "" && n.Name != n.ID)
	}
	header := []string{i18n.T(i18n.CLIColNode)}
	if named {
		header = append(header, i18n.T(i18n.TUIColName))
	}
	header = append(header, i18n.T(i18n.CLIColAddress), i18n.T(i18n.TUIColState))
	if !g.narrow() {
		header = append(header, i18n.T(i18n.CLIColControl))
	}
	header = append(header, i18n.T(i18n.CLIColVersion), i18n.T(i18n.CLIColCPU), i18n.T(i18n.CLIColRAM))
	rows := make([][]string, 0, len(ns))
	for _, n := range ns {
		st := s.up + " " + i18n.T(i18n.TUIOnline)
		if !n.Online {
			st = s.down + " " + i18n.T(i18n.TUIOffline)
		}
		row := []string{n.ID}
		if named {
			row = append(row, orDash(n.Name))
		}
		row = append(row, orDash(nodeAddr(n)), st)
		if !g.narrow() {
			ctl := orDash("")
			if n.Online && n.ControlRTTms > 0 {
				ctl = ms(n.ControlRTTms)
			}
			row = append(row, ctl)
		}
		ver := orDash("")
		if n.Version != "" {
			ver = i18n.T(i18n.TUINodeVersion, strings.TrimPrefix(n.Version, "v"))
			if !n.Compatible {
				ver += "!"
			}
		}
		row = append(row, ver, i18n.T(i18n.CLINodeCPUValue, n.CPUPercent), i18n.T(i18n.CLINodeRAMValue, n.RAMBytes/(1<<20)))
		rows = append(rows, row)
	}
	var b strings.Builder
	for _, l := range tableLines(header, rows) {
		b.WriteString(g.fit(l) + "\n")
	}
	return b.String()
}

// nodeAddr is the address column of a node: its public IP, or "via front"
// for a front node whose address the CDN hides.
func nodeAddr(n api.NodeInfo) string {
	if n.PublicIP == "" && n.Route != "" {
		return i18n.T(i18n.CLIStatusViaFront)
	}
	return n.PublicIP
}

// eventWord is the short event type of LAST EVENTS ("switch", "node
// offline"), the same word as in the menu's dashboard.
func eventWord(t string) string { return tui.EventWord(t) }

// eventCap caps the tunnel and type columns of LAST EVENTS (as the menu
// does): a long id is cut instead of pushing the messages away.
const eventCap = 12

// eventWho is the tunnel or node an event is about.
func eventWho(e state.Event) string {
	switch {
	case e.Tunnel != "":
		return e.Tunnel
	case e.Node != "":
		return e.Node
	}
	return orDash("")
}

// eventMessage is the event's text, its probe reason when it has none.
func eventMessage(e state.Event) string { return tui.EventMessage(e) }

// eventLines renders LAST EVENTS in local time.
func (g *Globals) eventLines(evs []state.Event) string {
	ell := g.sym().ell
	whoW, typW := 0, 0
	for _, e := range evs {
		whoW = max(whoW, min(eventCap, width(eventWho(e))))
		typW = max(typW, min(eventCap, width(eventWord(e.Type))))
	}
	var b strings.Builder
	for _, e := range evs {
		who, typ := trunc(eventWho(e), whoW, ell), trunc(eventWord(e.Type), typW, ell)
		line := "  " + localTime(e.At, "15:04:05") + "  " + pad(who, whoW+3) + pad(typ, typW+3) + eventMessage(e)
		b.WriteString(g.fit(strings.TrimRight(line, " ")) + "\n")
	}
	return b.String()
}

// warningLines renders the dashboard warnings ("! main: …").
func (g *Globals) warningLines(st api.Status) string {
	var lines []string
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
	for _, l := range lines {
		b.WriteString(g.fit("  "+g.sym().warn+" "+l) + "\n")
	}
	return b.String()
}

// hubLabel is the hub as shown on a node: its address (the front domain and
// port in front mode) with a "via front" marker when the node uses the front.
func hubLabel(n api.NodeSelf) string {
	if n.Front {
		return n.HubAddr + " (" + i18n.T(i18n.CLIViaFront) + ")"
	}
	return n.HubAddr
}

// nodeSelfLines renders the NODE section on a node server.
func (g *Globals) nodeSelfLines(n api.NodeSelf) string {
	s := g.sym()
	conn := s.up + " " + i18n.T(i18n.TUIDashConnected)
	if !n.Connected {
		conn = s.down + " " + i18n.T(i18n.TUIDashDisconnected)
	}
	line := "  " + n.ID + "  " + i18n.T(i18n.TUIDashHub, hubLabel(n)) + "  " + conn
	if !n.LastContact.IsZero() {
		line += "  " + i18n.T(i18n.TUIDashLastContact, localTime(n.LastContact, "15:04:05"))
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
