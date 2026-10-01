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
			g.printf("%s", g.dashboard(st))
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
				g.printf("%s", g.dashboard(st))
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

// dashboard renders the dashboard of spec section 6 as plain text: the
// banner status line, TUNNELS, NODES (hub) or NODE (node), LAST EVENTS and
// the yellow warnings. It uses the same strings and layout as the TUI.
func (g *Globals) dashboard(st api.Status) string {
	var b strings.Builder
	b.WriteString(g.statusLine(st) + "\n")
	if st.NodeSelf != nil {
		b.WriteString(" " + i18n.T(i18n.TUIDashNode) + "\n")
		b.WriteString(g.nodeSelfLines(*st.NodeSelf))
	}
	if st.NodeSelf == nil || len(st.Tunnels) > 0 {
		b.WriteString(" " + i18n.T(i18n.TUIDashTunnels) + "\n")
		if len(st.Tunnels) == 0 {
			b.WriteString("  " + i18n.T(i18n.CLIStatusNoTunnels) + "\n")
		} else {
			b.WriteString(g.tunnelTable(st.Tunnels))
		}
	}
	if st.NodeSelf == nil {
		b.WriteString(" " + i18n.T(i18n.TUIDashNodes) + "\n")
		if len(st.Nodes) == 0 {
			b.WriteString("  " + i18n.T(i18n.CLIStatusNoNodes) + "\n")
		} else {
			b.WriteString(g.nodeTable(st.Nodes))
		}
	}
	b.WriteString(" " + i18n.T(i18n.TUIDashEvents) + "\n")
	if len(st.Events) == 0 {
		b.WriteString("  " + i18n.T(i18n.TUIDashNoEvents) + "\n")
	} else {
		b.WriteString(g.eventLines(st.Events))
	}
	b.WriteString(g.warningLines(st))
	return g.text(b.String())
}

// statusLine is the line under the banner: "DEYROUTE Tunnel Manager  v1.0.0
// · Hub: ir-1 (5.6.7.8) · Mode: Simple · 2 nodes · 1 tunnel UP".
func (g *Globals) statusLine(st api.Status) string {
	sep := "  " + g.sym().sep + "  "
	parts := []string{i18n.T(i18n.BannerProduct) + "  " + version.Display()}
	switch {
	case st.Hub != nil:
		parts = append(parts, i18n.T(i18n.BannerHub, st.Hub.Name, st.Hub.PublicIP))
		mode := i18n.T(i18n.ModeSimple)
		if st.Hub.UIMode == "advanced" {
			mode = i18n.T(i18n.ModeAdvanced)
		}
		parts = append(parts, i18n.T(i18n.BannerMode, mode), i18n.T(i18n.BannerNodes, len(st.Nodes)))
	case st.NodeSelf != nil:
		parts = append(parts, i18n.T(i18n.BannerNode, st.NodeSelf.ID, st.NodeSelf.HubAddr))
	}
	up := 0
	for _, t := range st.Tunnels {
		if t.Enabled && t.State == state.StateUp {
			up++
		}
	}
	if st.NodeSelf == nil {
		parts = append(parts, i18n.T(i18n.BannerTunnelsUp, up))
	}
	return " " + strings.Join(parts, sep)
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
	header := []string{i18n.T(i18n.TUIColName), i18n.T(i18n.TUIColNode), i18n.T(i18n.TUIColTransport), i18n.T(i18n.TUIColState)}
	if !narrow {
		header = append(header, i18n.T(i18n.TUIColRTT), i18n.T(i18n.TUIColUptime))
	}
	minW := []int{16, 16, 19, 8, 7, 11}
	caps := []int{16, 16, 0, 0, 0, 0}
	rows := make([][]string, len(ts))
	for i, t := range ts {
		name := t.Name
		if name == "" {
			name = t.ID
		}
		node := orDash(strings.TrimSpace(t.ActiveNode + " " + t.ActiveNodeName))
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
		b.WriteString(strings.TrimRight(line+portsText(ts[i].Ports), " ") + "\n")
	}
	return b.String()
}

// nodeTable renders the NODES table of the dashboard.
func (g *Globals) nodeTable(ns []api.NodeInfo) string {
	s := g.sym()
	idW, nameW, ipW := 0, 0, 0
	for _, n := range ns {
		idW = max(idW, width(n.ID))
		nameW = max(nameW, width(n.Name))
		ipW = max(ipW, width(n.PublicIP))
	}
	var b strings.Builder
	for _, n := range ns {
		st := s.up + " " + i18n.T(i18n.TUIOnline)
		if !n.Online {
			st = s.down + " " + i18n.T(i18n.TUIOffline)
		}
		line := "  " + pad(n.ID, idW+2) + pad(n.Name, nameW+2) + pad(n.PublicIP, ipW+3) + pad(st, 11)
		if !g.narrow() {
			ctl := orDash("")
			if n.Online && n.ControlRTTms > 0 {
				ctl = ms(n.ControlRTTms)
			}
			line += pad(i18n.T(i18n.TUINodeCtl, ctl), 11)
		}
		ver := orDash("")
		if n.Version != "" {
			ver = i18n.T(i18n.TUINodeVersion, strings.TrimPrefix(n.Version, "v"))
			if !n.Compatible {
				ver += "!"
			}
		}
		line += pad(ver, 9) + pad(i18n.T(i18n.TUINodeCPU, n.CPUPercent), 8) + i18n.T(i18n.TUINodeRAM, n.RAMBytes/(1<<20))
		b.WriteString(line + "\n")
	}
	return b.String()
}

// eventWord is the short event type of LAST EVENTS ("switch", "node
// offline"), the same word as in the menu's dashboard.
func eventWord(t string) string { return tui.EventWord(t) }

// eventCap caps the tunnel and type columns of LAST EVENTS (as the menu
// does): a long id is cut instead of pushing the messages away.
const eventCap = 16

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
		b.WriteString(strings.TrimRight(line, " ") + "\n")
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
	b.WriteString("\n")
	for _, l := range lines {
		b.WriteString("  " + g.sym().warn + " " + l + "\n")
	}
	return b.String()
}

// nodeSelfLines renders the NODE section on a node server.
func (g *Globals) nodeSelfLines(n api.NodeSelf) string {
	s := g.sym()
	conn := s.up + " " + i18n.T(i18n.TUIDashConnected)
	if !n.Connected {
		conn = s.down + " " + i18n.T(i18n.TUIDashDisconnected)
	}
	line := "  " + n.ID + "  " + i18n.T(i18n.TUIDashHub, n.HubAddr) + "  " + conn
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
