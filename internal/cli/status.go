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
	if st.NodeSelf == nil {
		b.WriteString("\n" + g.attention(st))
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
	if w := g.warningLines(st); w != "" && st.NodeSelf != nil {
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

// tunnelTable renders the TUNNELS table as a boxed table; below 100
// columns RTT and UP-TIME are dropped, and on a phone the node column holds
// the id only.
func (g *Globals) tunnelTable(ts []api.TunnelInfo) string {
	narrow := g.narrow()
	now := g.Now()
	tight := g.lineWidth() < 80
	nodeCol := i18n.T(i18n.TUIColNode)
	if tight {
		nodeCol = i18n.T(i18n.CLIColNode)
	}
	cols := []boxCol{
		{title: i18n.T(i18n.TUIColNum), right: true},
		{title: i18n.T(i18n.TUIColName), min: 4, max: 20, flex: true},
		{title: nodeCol, min: 6, max: 28, flex: true},
		{title: i18n.T(i18n.TUIColTransport), min: 8, flex: true},
		{title: i18n.T(i18n.TUIColState), min: 4},
	}
	if !narrow {
		cols = append(cols, boxCol{title: i18n.T(i18n.TUIColRTT), right: true}, boxCol{title: i18n.T(i18n.TUIColUptime), right: true})
	}
	cols = append(cols, boxCol{title: i18n.T(i18n.TUIColPorts), min: 4, flex: true})
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
		row := []string{strconv.Itoa(i + 1), name, node, orDash(t.ActiveTransport), g.stateCell(t)}
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
		rows[i] = append(row, orDash(portsText(t.Ports)))
	}
	lines := g.boxTable(cols, rows, func(r, c int) string {
		if c == 4 {
			return g.stateStyle(ts[r])
		}
		return ""
	})
	return strings.Join(lines, "\n") + "\n"
}

// nodeTable renders the NODES table of the dashboard as a boxed table;
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
	cols := []boxCol{{title: i18n.T(i18n.CLIColNode), min: 6, max: 28, flex: true}}
	if named {
		cols = append(cols, boxCol{title: i18n.T(i18n.TUIColName), min: 4, max: 20, flex: true})
	}
	cols = append(cols, boxCol{title: i18n.T(i18n.CLIColAddress), min: 7, flex: true}, boxCol{title: i18n.T(i18n.TUIColState), min: 4})
	stateCol := len(cols) - 1
	if !g.narrow() {
		cols = append(cols, boxCol{title: i18n.T(i18n.CLIColControl), right: true})
	}
	cols = append(cols, boxCol{title: i18n.T(i18n.CLIColVersion), min: 5, flex: true},
		boxCol{title: i18n.T(i18n.CLIColCPU), right: true}, boxCol{title: i18n.T(i18n.CLIColRAM), right: true})
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
	lines := g.boxTable(cols, rows, func(r, c int) string {
		switch {
		case c != stateCol:
			return ""
		case ns[r].Online:
			return styleGreen
		}
		return styleRed
	})
	return strings.Join(lines, "\n") + "\n"
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

// eventLines renders LAST EVENTS in local time as a boxed table; the
// message column takes the room that is left.
func (g *Globals) eventLines(evs []state.Event) string {
	cols := []boxCol{
		{title: i18n.T(i18n.CLIColTime)},
		{title: i18n.T(i18n.CLIColWho), min: 4, max: eventCap},
		{title: i18n.T(i18n.CLIColType), min: 4, max: eventCap},
		{title: i18n.T(i18n.CLIColMessage), min: 10, flex: true},
	}
	rows := make([][]string, len(evs))
	for i, e := range evs {
		rows[i] = []string{localTime(e.At, "15:04:05"), eventWho(e), eventWord(e.Type), eventMessage(e)}
	}
	lines := g.boxTable(cols, rows, func(r, c int) string {
		if c == 2 {
			return eventStyle(evs[r])
		}
		return ""
	})
	return strings.Join(lines, "\n") + "\n"
}

// warningLines renders the dashboard warnings ("! main: …") on a node
// (the hub shows them in its attention section).
func (g *Globals) warningLines(st api.Status) string {
	lines := g.warningTexts(st)
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(g.fit("  "+g.sym().warn+" "+l) + "\n")
	}
	return b.String()
}

// warningTexts are the dashboard warnings as text ("main: …").
func (g *Globals) warningTexts(st api.Status) []string {
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
	return lines
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

// stateStyle colors a tunnel state: green when it carries traffic, yellow
// while it is degraded or changing, red when it is down; plain when it was
// turned off on purpose.
func (g *Globals) stateStyle(t api.TunnelInfo) string {
	switch {
	case !t.Enabled || t.State == state.StateDisabled || t.Paused || t.State == state.StatePaused:
		return ""
	case t.State == state.StateUp:
		return styleGreen
	case t.State == state.StateDegraded || t.State == state.StateSwitching || t.State == state.StateStarting:
		return styleYellow
	}
	return styleRed
}

// eventStyle colors the type of an event: red for errors and losses,
// yellow for warnings and switches, green when something came up.
func eventStyle(e state.Event) string {
	switch {
	case e.Level == state.LevelError || e.Type == state.EvTunnelDown || e.Type == state.EvNodeOffline:
		return styleRed
	case e.Level == state.LevelWarn || e.Type == state.EvTunnelDegraded || e.Type == state.EvSwitchTransport ||
		e.Type == state.EvSwitchNode:
		return styleYellow
	case e.Type == state.EvTunnelUp || e.Type == state.EvNodeOnline || e.Type == state.EvFailback:
		return styleGreen
	}
	return ""
}

// attention is the first section of the hub dashboard: what needs the
// owner now (tunnels not UP, nodes offline, the warnings), each line in red
// or yellow; or one green line when everything works.
func (g *Globals) attention(st api.Status) string {
	s := g.sym()
	type item struct{ style, text string }
	var items []item
	for _, t := range st.Tunnels {
		if !t.Enabled || t.Paused || t.State == state.StateUp {
			continue
		}
		name := t.Name
		if name == "" {
			name = t.ID
		}
		style := g.stateStyle(t)
		items = append(items, item{style, i18n.T(i18n.CLIAttnTunnel, name, attnState(t))})
	}
	online := 0
	for _, n := range st.Nodes {
		if n.Online {
			online++
			continue
		}
		items = append(items, item{styleRed, i18n.T(i18n.CLIAttnNodeOffline, n.ID)})
	}
	for _, l := range g.warningTexts(st) {
		items = append(items, item{styleYellow, l})
	}
	var b strings.Builder
	if len(items) == 0 {
		up := 0
		for _, t := range st.Tunnels {
			if t.Enabled && t.State == state.StateUp {
				up++
			}
		}
		b.WriteString(" " + g.styleOut(styleGreen, s.ok+" "+i18n.T(i18n.CLIAttnAllGood, up, online)) + "\n")
		return b.String()
	}
	b.WriteString(g.sectionHead(i18n.T(i18n.CLIAttnTitle, len(items)), "") + "\n")
	for _, it := range items {
		for i, l := range wrapText(it.text, g.lineWidth()-4) {
			lead := "  " + g.styleOut(it.style, s.warn) + " "
			if i > 0 {
				lead = "    "
			}
			b.WriteString(lead + g.styleOut(it.style, l) + "\n")
		}
	}
	return b.String()
}

// attnState says in words what is wrong with a tunnel that is not UP.
func attnState(t api.TunnelInfo) string {
	switch t.State {
	case state.StateDegraded:
		if t.ServiceDown {
			return i18n.T(i18n.CLIAttnServiceDown)
		}
		return i18n.T(i18n.CLIAttnDegraded)
	case state.StateSwitching:
		return i18n.T(i18n.CLIAttnSwitching)
	case state.StateStarting:
		return i18n.T(i18n.CLIAttnStarting)
	case state.StateDown:
		return i18n.T(i18n.CLIAttnDown)
	case state.StateInit, "":
		return i18n.T(i18n.CLIAttnInit)
	}
	return strings.ToLower(t.State)
}
