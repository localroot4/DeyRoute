package cli

import (
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/tui"
)

// The layout of `deyroute optimize status` (and of apply / revert): a line
// of labelled facts, the server in a frame with a fill meter for the
// connection table, the settings in effect as a table of groups (every key
// with --details) and the nodes with their tuning state.

// printOptimize prints an optimize answer; k is its first line.
func (g *Globals) printOptimize(st api.OptimizeStatus, k i18n.Key) error {
	if g.JSON {
		return g.emitJSON(st)
	}
	if k != i18n.CLIOptimizeStatusLine { // status names the profile in its head
		g.say(k, orDash(st.Profile))
		g.println()
	}
	g.println(g.sectionHead(i18n.T(i18n.CLITuneStatusSection), ""))
	sep := "   " + g.styleOut(styleGray, g.text("│")) + "   "
	for _, l := range joinWrap(g.tuneHeadFacts(st), sep, g.lineWidth()-1) {
		g.println(" " + l)
	}
	if len(st.Warnings) > 0 {
		g.println()
	}
	for _, w := range st.Warnings {
		g.printWarn(g.Out, clean(w))
	}
	if rows := g.tuneServerRows(st.Facts); len(rows) > 0 {
		g.println()
		g.println(g.sectionHead(i18n.T(i18n.CLITuneServerTitle), ""))
		cols := []boxCol{{min: 6}, {min: 10, flex: true}}
		for _, l := range g.boxTable(cols, rows, func(_, c int) string {
			if c == 0 {
				return styleGray
			}
			return ""
		}) {
			g.println(l)
		}
	}
	if len(st.Applied) > 0 {
		g.println()
		g.println(g.sectionHead(i18n.T(i18n.CLITuneSettingsSection, tuneHostTitle(config.RoleHub)), ""))
		g.printTuneSettings(st.Applied)
	}
	if len(st.Nodes) > 0 {
		g.println()
		g.println(g.sectionHead(i18n.T(i18n.TUIDashNodes), ""))
		g.printTuneNodes(st.Nodes)
	}
	return nil
}

// tuneHeadFacts are the labelled facts on top: profile, BBR and how many
// nodes run the hub's tuning.
func (g *Globals) tuneHeadFacts(st api.OptimizeStatus) []string {
	s := g.sym()
	fact := func(label, mark, style, value string) string {
		return g.text(g.styleOut(styleGray, label) + " " + g.styleOut(style, strings.TrimSpace(mark+" "+value)))
	}
	profStyle, profMark := styleGreen, s.up
	if st.Profile == "" || st.Profile == "off" || st.Profile == "none" {
		profStyle, profMark = styleGray, s.down
	}
	out := []string{fact(i18n.T(i18n.CLITuneHeadProfile), profMark, profStyle, orDash(st.Profile))}
	switch {
	case st.BBRActive:
		out = append(out, fact(i18n.T(i18n.CLITuneHeadBBR), s.up, styleGreen, i18n.T(i18n.CLIBBRValActive)))
	case st.BBRAvailable:
		out = append(out, fact(i18n.T(i18n.CLITuneHeadBBR), s.half, styleYellow, i18n.T(i18n.CLIBBRValInactive)))
	default:
		out = append(out, fact(i18n.T(i18n.CLITuneHeadBBR), s.down, styleRed, i18n.T(i18n.CLIBBRValMissing)))
	}
	if len(st.Nodes) > 0 {
		synced := 0
		for _, n := range st.Nodes {
			if n.Online && !n.Pending {
				synced++
			}
		}
		style, mark := styleGreen, s.up
		switch {
		case synced == 0:
			style, mark = styleRed, s.down
		case synced < len(st.Nodes):
			style, mark = styleYellow, s.half
		}
		out = append(out, fact(i18n.T(i18n.CLITuneHeadNodes), mark, style, i18n.T(i18n.CLITuneHeadInSync, synced, len(st.Nodes))))
	}
	return out
}

// tuneServerRows are the measured facts of the hub, one per row.
func (g *Globals) tuneServerRows(f *api.TuneFacts) [][]string {
	if f == nil {
		return nil
	}
	var rows [][]string
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, []string{label, value})
		}
	}
	switch {
	case f.MemBytes > 0 && f.MemAvailableBytes > 0 && f.MemAvailableBytes <= f.MemBytes:
		used := f.MemBytes - f.MemAvailableBytes
		add(i18n.T(i18n.CLITuneSrvMemory), g.meterLine(float64(used)/float64(f.MemBytes),
			i18n.T(i18n.CLITuneSrvUsed, tui.FormatBytes(used)),
			i18n.T(i18n.CLITuneSrvFree, tui.FormatBytes(f.MemAvailableBytes)),
			i18n.T(i18n.CLITuneSrvTotal, tui.FormatBytes(f.MemBytes))))
	case f.MemBytes > 0:
		add(i18n.T(i18n.CLITuneSrvMemory), tui.FormatBytes(f.MemBytes))
	}
	if f.CPUs > 0 {
		cores := i18n.T(i18n.CLITuneSrvCores, f.CPUs)
		if f.CPUs == 1 {
			cores = i18n.T(i18n.CLITuneSrvCore)
		}
		add(i18n.T(i18n.CLITuneSrvCPU), cores)
	}
	add(i18n.T(i18n.CLITuneSrvKernel), clean(f.Kernel))
	if f.NIC != "" {
		nic := clean(f.NIC)
		if f.NICMTU > 0 {
			nic = i18n.T(i18n.CLITuneSrvNetVal, nic, f.NICMTU)
		}
		if f.NICSpeedMbps > 0 {
			nic = i18n.T(i18n.CLITuneSrvSpeed, nic, f.NICSpeedMbps)
		}
		add(i18n.T(i18n.CLITuneSrvNetwork), nic)
	}
	add(i18n.T(i18n.CLITuneSrvQdisc), clean(f.Qdisc))
	switch {
	case f.ConntrackLoaded && f.ConntrackMax > 0:
		frac := float64(f.ConntrackCount) / float64(f.ConntrackMax)
		add(i18n.T(i18n.CLITuneSrvConntrack), g.meterLine(frac,
			i18n.T(i18n.CLITuneSrvCTUsed, thousands(f.ConntrackCount)),
			i18n.T(i18n.CLITuneSrvFree, thousands(f.ConntrackMax-f.ConntrackCount)),
			i18n.T(i18n.CLITuneSrvTotal, thousands(f.ConntrackMax))))
	case !f.ConntrackLoaded:
		add(i18n.T(i18n.CLITuneSrvConntrack), i18n.T(i18n.CLITuneSrvCTOff))
	}
	add(i18n.T(i18n.CLITuneSrvVirt), clean(f.Virt))
	return rows
}

// meterLine is a fill bar with its percentage in the bar's color and the
// figures after it, separated in gray: "████░░░░  27%  used · free · total".
func (g *Globals) meterLine(frac float64, figures ...string) string {
	bar, style := g.meter(frac)
	pct := padLeft(strconv.Itoa(int(min(max(frac, 0), 1)*100+0.5))+"%", 4)
	sep := "  " + g.styleOut(styleGray, g.sym().sep) + "  "
	return bar + " " + g.styleOut(style, pct) + "   " + strings.Join(figures, sep)
}

// meter is a 16-cell fill bar and its style: green below 60 %, yellow below
// 85 %, red above.
func (g *Globals) meter(frac float64) (string, string) {
	const cells = 16
	frac = min(max(frac, 0), 1)
	n := int(frac*cells + 0.5)
	if frac > 0 && n == 0 {
		n = 1
	}
	full, empty := "█", "░"
	if !g.unicode() {
		full, empty = "#", "."
	}
	style := styleGreen
	switch {
	case frac >= 0.85:
		style = styleRed
	case frac >= 0.6:
		style = styleYellow
	}
	return g.styleOut(style, strings.Repeat(full, n)) + g.styleOut(styleGray, strings.Repeat(empty, cells-n)), style
}

// thousands writes n with thousands separators: 65,536.
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// printTuneSettings shows the settings in effect: one row per group with
// what it means now, or with --details every key and value under its
// group's title and explanation.
func (g *Globals) printTuneSettings(applied map[string]string) {
	keys := make([]string, 0, len(applied))
	for key := range applied {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	groups := tui.TuneGroups(keys)
	ok := g.sym().ok
	if !g.details {
		rows := make([][]string, 0, len(groups))
		for _, grp := range groups {
			rows = append(rows, []string{ok + " " + grp.Title, tui.TuneGroupSummary(grp, applied)})
		}
		cols := []boxCol{{title: i18n.T(i18n.CLITuneColGroup), min: 12}, {title: i18n.T(i18n.CLITuneColNow), min: 12, flex: true}}
		for _, l := range g.boxTable(cols, rows, func(_, c int) string {
			if c == 1 {
				return styleGreen
			}
			return ""
		}) {
			g.println(strings.Replace(l, ok, g.styleOut(styleGreen, ok), 1))
		}
		g.println("  " + g.styleOut(styleGray, g.text(i18n.T(i18n.CLITuneDetailsHint))))
		return
	}
	for i, grp := range groups {
		if i > 0 {
			g.println()
		}
		g.println(g.text("  " + g.styleOut(styleGreen, ok) + " " + g.styleOut(styleBold, grp.Title) + "  " +
			g.styleOut(styleGray, g.sym().sep) + "  " + g.styleOut(styleGreen, tui.TuneGroupSummary(grp, applied))))
		for _, l := range wrapText(tui.TuneGroupHelp(grp.ID), g.lineWidth()-4) {
			g.println(g.text("    " + g.styleOut(styleGray, l)))
		}
		rows := make([][]string, 0, len(grp.Keys))
		for _, key := range grp.Keys {
			rows = append(rows, []string{tui.TuneShortKey(key), orDash(tui.TuneValue(key, applied[key]))})
		}
		cols := []boxCol{{title: i18n.T(i18n.CLITuneColSetting), min: 10, flex: true}, {title: i18n.T(i18n.CLITuneColValue), min: 8, flex: true}}
		for _, l := range g.boxTable(cols, rows, nil) {
			g.println(l)
		}
	}
}

// printTuneNodes is the nodes table: whether each is online, its profile
// and whether it runs the hub's tuning.
func (g *Globals) printTuneNodes(nodes []api.NodeTuneStatus) {
	s := g.sym()
	rows := make([][]string, 0, len(nodes))
	styles := make([][2]string, 0, len(nodes))
	for _, n := range nodes {
		online, onStyle := s.up+" "+i18n.T(i18n.TUIOnline), styleGreen
		if !n.Online {
			online, onStyle = s.down+" "+i18n.T(i18n.TUIOffline), styleRed
		}
		tuning, tStyle := s.ok+" "+i18n.T(i18n.CLITuneNodeSynced), styleGreen
		switch {
		case n.Pending:
			tuning, tStyle = s.half+" "+i18n.T(i18n.CLITuneNodeWaiting), styleYellow
		case n.Online && !n.AutoCapable:
			tuning, tStyle = s.warn+" "+i18n.T(i18n.CLITuneNodeBalanced), styleYellow
		}
		rows = append(rows, []string{n.Node, online, orDash(n.Profile), tuning})
		styles = append(styles, [2]string{onStyle, tStyle})
	}
	cols := []boxCol{
		{title: i18n.T(i18n.CLIColNode), min: 6, flex: true},
		{title: i18n.T(i18n.CLITuneColState)},
		{title: i18n.T(i18n.CLIColProfile)},
		{title: i18n.T(i18n.CLITuneColTuning), min: 8, flex: true},
	}
	for _, l := range g.boxTable(cols, rows, func(r, c int) string {
		switch c {
		case 1:
			return styles[r][0]
		case 3:
			return styles[r][1]
		}
		return ""
	}) {
		g.println(l)
	}
}
