package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// This file renders traffic and load for the dashboard's TRAFFIC block, the
// Traffic screen, the tunnel detail and `deyroute stats` (the cli package
// calls the exported functions, so both front-ends draw the same lines).
// Everything here is plain text clipped to the width: the dashboard and the
// CLI print it as it is, and nothing depends on colour alone (download and
// upload are separate, labelled panels).

// TrafficSparkPoints is the number of points of the dashboard sparklines:
// the last hour in 2-minute points (see TrafficBlockQuery).
const TrafficSparkPoints = 30

// Layout of the TRAFFIC block.
const (
	trafficNameCap  = 14 // tunnel name column
	trafficSparkMin = 8  // a narrower sparkline is left out
	trafficRateW    = 9  // "1.23 Gb/s"
)

// Colours of the Traffic panels (16-colour indexes). The panel titles name
// what is drawn, so the colours only help.
const (
	trafficColDown = 6 // cyan
	trafficColUp   = 5 // magenta
	trafficColCPU  = 3 // yellow
	trafficColRAM  = 4 // blue
)

// trafficText adapts s to the terminal: without UTF-8 every symbol becomes
// ASCII through ToASCII ("↓" is "v", "↑" is "^", "—" and "·" are "-").
func trafficText(c Caps, s string) string {
	if c.Unicode {
		return s
	}
	return ToASCII(s)
}

// trafficClip cuts a plain line to the width (0 = no limit).
func trafficClip(c Caps, w int, s string) string {
	s = trafficText(c, s)
	if w > 1 && width(s) > w {
		ell := "…"
		if !c.Unicode {
			ell = "..."
		}
		s = trunc(s, w, ell)
	}
	return s
}

// TrafficBlockQuery is the Traffic call behind the dashboard sparklines:
// every tunnel, the last hour in TrafficSparkPoints points.
func TrafficBlockQuery() api.TrafficQuery {
	return api.TrafficQuery{Period: api.TrafficPeriod1h, MaxPoints: TrafficSparkPoints}
}

// HasTraffic reports whether any tunnel carries traffic numbers: the
// TRAFFIC block is shown only then (monitoring on and sampled).
func HasTraffic(ts []api.TunnelInfo) bool {
	for _, t := range ts {
		if t.Traffic != nil {
			return true
		}
	}
	return false
}

// TrafficBlockTitle returns the title of the TRAFFIC block and its hint
// (what the sparkline covers and what the arrows mean).
func TrafficBlockTitle(c Caps) (title, hint string) {
	return i18n.T(i18n.TUITrafficTitle), trafficText(c, i18n.T(i18n.TUITrafficHint))
}

// seriesOf returns the series of a tunnel, node or the hub in rep.
func seriesOf(rep *api.TrafficReport, kind, id string) (api.TrafficSeries, bool) {
	if rep == nil {
		return api.TrafficSeries{}, false
	}
	for _, s := range rep.Series {
		if s.Kind == kind && s.ID == id {
			return s, true
		}
	}
	return api.TrafficSeries{}, false
}

// seriesBytes returns the bit rates of a tunnel series: download (out),
// upload (in) or both (total), one value per point, and its gaps.
func seriesBytes(s api.TrafficSeries, out, in bool) ([]float64, []bool) {
	step := float64(max(s.StepS, 1))
	vals := make([]float64, len(s.Points))
	gaps := make([]bool, len(s.Points))
	for i, p := range s.Points {
		var b uint64
		if out {
			b += p.BytesOut
		}
		if in {
			b += p.BytesIn
		}
		vals[i] = float64(b) * 8 / step
		gaps[i] = p.Gap
	}
	return vals, gaps
}

// seriesConns returns the connection counts of a tunnel series; known is
// false when no point has one (UDP has no connection state).
func seriesConns(s api.TrafficSeries) (vals []float64, gaps []bool, known bool) {
	vals = make([]float64, len(s.Points))
	gaps = make([]bool, len(s.Points))
	for i, p := range s.Points {
		if p.Gap || p.Conns == nil {
			gaps[i] = true
			continue
		}
		vals[i] = float64(*p.Conns)
		known = true
	}
	return vals, gaps, known
}

// seriesHost returns the CPU percentages or the RAM bytes of a host series.
func seriesHost(s api.TrafficSeries, ram bool) ([]float64, []bool) {
	vals := make([]float64, len(s.Points))
	gaps := make([]bool, len(s.Points))
	for i, p := range s.Points {
		gaps[i] = p.Gap
		if ram {
			vals[i] = float64(p.RAMBytes)
		} else {
			vals[i] = p.CPUPercent
		}
	}
	return vals, gaps
}

// avgPeak returns the mean and the maximum of the values that are not
// gaps; ok is false when every value is a gap.
func avgPeak(vals []float64, gaps []bool) (avg, peak float64, ok bool) {
	n := 0
	for i, v := range vals {
		if i < len(gaps) && gaps[i] {
			continue
		}
		v = clean0(v)
		avg += v
		peak = max(peak, v)
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	return avg / float64(n), peak, true
}

// lastValue is the newest value that is not a gap.
func lastValue(vals []float64, gaps []bool) (float64, bool) {
	for i := len(vals) - 1; i >= 0; i-- {
		if i >= len(gaps) || !gaps[i] {
			return vals[i], true
		}
	}
	return 0, false
}

// bytesAvailable reports whether the byte counts of s can be shown: the
// hub counts bytes and the series has them.
func bytesAvailable(rep api.TrafficReport, s api.TrafficSeries) bool {
	return rep.Available && s.Available
}

// none is the cell of a number that is not known ("—", never "0 B").
func none() string { return i18n.T(i18n.TUITrafficNone) }

// trafficNow is "↓ 12.3 Mb/s  ↑ 1.20 Mb/s" (download first) or the same
// with "—" when the hub cannot count bytes.
func trafficNow(t *api.TrafficNow) string {
	down, up := none(), none()
	if t != nil && t.Available {
		down, up = FormatRate(float64(t.RateOutBitS)), FormatRate(float64(t.RateInBitS))
	}
	return i18n.T(i18n.TUITrafficNow, padLeft(down, trafficRateW), padLeft(up, trafficRateW))
}

// trafficToday is "today ↓ 3.8 GiB ↑ 512 MiB" or "today —".
func trafficToday(t *api.TrafficNow) string {
	if t == nil || !t.Available {
		return i18n.T(i18n.TUITrafficTodayNone, none())
	}
	return i18n.T(i18n.TUITrafficToday, FormatBytes(t.TodayOut), FormatBytes(t.TodayIn))
}

// blockLine is one tunnel of the TRAFFIC block.
type blockLine struct {
	idx   int
	name  string
	t     *api.TrafficNow
	rate  uint64
	spark []float64
	gaps  []bool
	// sparkState: 0 no data (blank), 1 drawn, 2 not counted ("—").
	sparkState int
}

// TrafficBlock returns the lines of the dashboard's TRAFFIC block under its
// title: one line per tunnel with its name, a sparkline of the last hour
// (from rep, the answer to TrafficBlockQuery; nil leaves it blank), the
// current download and upload rate and, from 100 columns, today's volume.
// A hub that cannot count bytes shows "—", never "0 B". It returns nil
// when no tunnel carries traffic numbers (the block is then left out).
//
// maxRows bounds the lines (0 = no limit): the busiest tunnels are kept, in
// the order of the TUNNELS table, and the last line says how many more the
// Traffic screen shows, so the block never pushes the warnings off a short
// terminal.
func TrafficBlock(c Caps, ts []api.TunnelInfo, rep *api.TrafficReport, maxRows int) []string {
	if !HasTraffic(ts) || maxRows < 0 {
		return nil
	}
	ell := "…"
	if !c.Unicode {
		ell = "..."
	}
	var lines []blockLine
	for i, t := range ts {
		if t.Traffic == nil && !t.Enabled {
			continue
		}
		name := t.Name
		if name == "" {
			name = t.ID
		}
		bl := blockLine{idx: i, name: trunc(clean(name), trafficNameCap, ell), t: t.Traffic}
		if t.Traffic != nil && t.Traffic.Available {
			bl.rate = t.Traffic.RateInBitS + t.Traffic.RateOutBitS
		}
		switch s, ok := seriesOf(rep, api.TrafficKindTunnel, t.ID); {
		case ok && rep.Available && s.Available:
			bl.spark, bl.gaps = seriesBytes(s, true, true)
			bl.sparkState = 1
		case (ok && (!rep.Available || !s.Available)) || (t.Traffic != nil && !t.Traffic.Available):
			bl.sparkState = 2
		}
		lines = append(lines, bl)
	}
	more := 0
	if maxRows > 0 && len(lines) > maxRows {
		keep := maxRows - 1
		more = len(lines) - keep
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].rate > lines[j].rate })
		lines = lines[:keep]
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].idx < lines[j].idx })
	}

	nameW := 0
	for _, l := range lines {
		nameW = max(nameW, width(l.name))
	}
	nowW := width(trafficNow(nil))
	wide := !c.Narrow()
	todayW := 0
	if wide {
		for _, l := range lines {
			todayW = max(todayW, width(trafficToday(l.t)))
		}
	}
	sparkW := TrafficSparkPoints
	if c.Width > 0 {
		used := 2 + nameW + 2 + 2 + nowW
		if wide {
			used += 2 + todayW
		}
		sparkW = min(sparkW, c.Width-used)
	}
	if sparkW < trafficSparkMin {
		sparkW = 0
	}

	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		line := "  " + pad(l.name, nameW+2)
		if sparkW > 0 {
			var sp string
			switch l.sparkState {
			case 1:
				sp = Sparkline(l.spark, l.gaps, c, sparkW)
			case 2:
				sp = none()
			}
			line += pad(sp, sparkW) + "  "
		}
		line += trafficNow(l.t)
		if wide {
			line += "  " + trafficToday(l.t)
		}
		out = append(out, trafficClip(c, c.Width, strings.TrimRight(line, " ")))
	}
	if more > 0 {
		out = append(out, trafficClip(c, c.Width, "  "+i18n.T(i18n.TUITrafficMore, more)))
	}
	return out
}

// TrafficView configures TrafficPanels.
type TrafficView struct {
	// Loc is the zone of the x labels (the viewer's); nil = time.Local.
	Loc *time.Location
	// Style selects blocks or braille for the charts.
	Style ChartStyle
	// Rows is the height of each byte chart; 0 = 6. Host charts and the
	// connections row are smaller.
	Rows int
	// Width of every line; 0 = Caps.Width, then 80.
	Width int
}

// TrafficPanels renders one series of a Traffic report. A tunnel gets the
// panels "Download (to users)" and "Upload (from users)" with their
// average and peak, a connections row and the totals (today, 30 days, the
// quota period); a node or the hub gets CPU and RAM panels. When the hub
// cannot count bytes the byte panels are replaced by the reason (DEY-X061)
// and its fix; nothing unknown is ever drawn as 0.
func TrafficPanels(c Caps, rep api.TrafficReport, s api.TrafficSeries, v TrafficView) []string {
	w := v.Width
	if w <= 0 {
		w = c.Width
	}
	if w <= 0 {
		w = chartDefaultWidth
	}
	rows := v.Rows
	if rows <= 0 {
		rows = chartDefaultRows
	}
	loc := v.Loc
	if loc == nil {
		loc = time.Local
	}
	cc := c
	cc.Width = w
	base := ChartSpec{Width: max(w-1, 1), Loc: loc, Style: v.Style, Step: time.Duration(max(s.StepS, 1)) * time.Second}
	if len(s.Points) > 0 {
		base.Start = s.Points[0].At
	}
	line := func(s string) string { return trafficClip(cc, w, s) }

	var out []string
	empty := len(s.Points) == 0
	if s.Kind != api.TrafficKindTunnel {
		if empty {
			return []string{line("  " + i18n.T(i18n.TUITrafficNoSamples))}
		}
		cpu, cg := seriesHost(s, false)
		ram, rg := seriesHost(s, true)
		spec := base
		spec.Rows = max(3, rows/2)
		spec.Values, spec.Gaps, spec.Color = cpu, cg, trafficColCPU
		spec.YFormat = func(x float64) string { return i18n.T(i18n.TUITrafficPercent, x) }
		avg, peak, _ := avgPeak(cpu, cg)
		spec.Title = trafficText(cc, i18n.T(i18n.TUITrafficCPU, i18n.T(i18n.TUITrafficPercent, avg), i18n.T(i18n.TUITrafficPercent, peak)))
		out = append(out, indentChart(Chart(spec, cc))...)
		spec.Values, spec.Gaps, spec.Color = ram, rg, trafficColRAM
		spec.YFormat = func(x float64) string { return FormatBytes(uint64(clean0(x))) }
		_, peak, _ = avgPeak(ram, rg)
		spec.Title = trafficText(cc, i18n.T(i18n.TUITrafficRAM, FormatBytes(uint64(peak))))
		return append(out, indentChart(Chart(spec, cc))...)
	}

	if !bytesAvailable(rep, s) {
		out = append(out, line("  "+i18n.T(i18n.TUITrafficUnavailable, none())))
		reason := s.Reason
		if reason == nil {
			reason = rep.Reason
		}
		if reason != nil {
			for _, l := range strings.Split(strings.TrimRight(reason.Err().Format(cc.Unicode), "\n"), "\n") {
				out = append(out, line("    "+clean(l)))
			}
		}
	} else if empty {
		out = append(out, line("  "+i18n.T(i18n.TUITrafficNoSamples)))
	} else {
		for _, p := range []struct {
			title  i18n.Key
			out    bool
			colour int
		}{{i18n.TUITrafficDownload, true, trafficColDown}, {i18n.TUITrafficUpload, false, trafficColUp}} {
			vals, gaps := seriesBytes(s, p.out, !p.out)
			spec := base
			spec.Rows, spec.Values, spec.Gaps, spec.Color = rows, vals, gaps, p.colour
			avg, peak, _ := avgPeak(vals, gaps)
			spec.Title = trafficText(cc, i18n.T(p.title, FormatRate(avg), FormatRate(peak)))
			out = append(out, indentChart(Chart(spec, cc))...)
		}
	}

	// Connections: a sparkline under the charts, "n/a" for UDP.
	label := i18n.T(i18n.TUITrafficConns)
	if vals, gaps, known := seriesConns(s); known {
		last, _ := lastValue(vals, gaps)
		_, peak, _ := avgPeak(vals, gaps)
		tail := "  " + i18n.T(i18n.TUITrafficConnsNow, int(last), int(peak))
		sparkW := min(w-2-width(label)-2-width(tail), len(vals))
		if sparkW >= trafficSparkMin {
			out = append(out, line("  "+label+"  "+Sparkline(vals, gaps, cc, sparkW)+tail))
		} else {
			out = append(out, line("  "+label+tail))
		}
	} else if !empty {
		out = append(out, line("  "+label+"  "+i18n.T(i18n.TUITrafficConnsNA)))
	}

	if t := s.Totals; t != nil && bytesAvailable(rep, s) {
		out = append(out, line("  "+i18n.T(i18n.TUITrafficTotals,
			FormatBytes(t.TodayOut), FormatBytes(t.TodayIn), FormatBytes(t.Days30Out), FormatBytes(t.Days30In))))
		hub := hubZone(rep.Timezone)
		period := i18n.T(i18n.TUITrafficPeriod, t.PeriodStart.In(hub).Format("2006-01-02"), FormatBytes(t.PeriodOut), FormatBytes(t.PeriodIn))
		if t.QuotaBytes > 0 {
			period += " " + i18n.T(i18n.TUITrafficQuota, quotaPercent(*t), FormatBytes(t.QuotaBytes))
		}
		out = append(out, line("  "+period))
		if rep.Timezone != "" {
			out = append(out, line("  "+i18n.T(i18n.TUITrafficZone, clean(rep.Timezone))))
		}
	}
	return out
}

// indentChart moves chart lines one column right (they are drawn one
// column narrower), in line with the page's text.
func indentChart(lines []string) []string {
	for i, l := range lines {
		lines[i] = " " + l
	}
	return lines
}

// quotaPercent is the share of the monthly quota used in the current
// period (in + out), rounded down.
func quotaPercent(t api.TrafficTotals) int {
	if t.QuotaBytes == 0 {
		return 0
	}
	used := float64(t.PeriodIn) + float64(t.PeriodOut)
	return int(used * 100 / float64(t.QuotaBytes))
}

// QuotaText is the QUOTA cell of `deyroute stats`: "45%", or "—" without a
// quota or without byte counts.
func QuotaText(rep api.TrafficReport, s api.TrafficSeries) string {
	if s.Totals == nil || s.Totals.QuotaBytes == 0 || !bytesAvailable(rep, s) {
		return none()
	}
	return i18n.T(i18n.TUITrafficPercentInt, quotaPercent(*s.Totals))
}

// TrafficSpark is the sparkline of a tunnel series' total rate (in + out)
// in at most w columns, "—" when the hub does not count its bytes.
func TrafficSpark(c Caps, rep api.TrafficReport, s api.TrafficSeries, w int) string {
	if !bytesAvailable(rep, s) {
		return trafficText(c, none())
	}
	vals, gaps := seriesBytes(s, true, true)
	return Sparkline(vals, gaps, c, w)
}

// hubZone returns the hub-local zone named in TrafficReport.Timezone: an
// IANA name, or "UTC+03:30"; UTC when it cannot be read.
func hubZone(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	rest, ok := strings.CutPrefix(tz, "UTC")
	if !ok || len(rest) < 2 {
		return time.UTC
	}
	sign := 1
	switch rest[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return time.UTC
	}
	hh, mm, _ := strings.Cut(rest[1:], ":")
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if mm == "" {
		m, err2 = 0, nil
	}
	if err1 != nil || err2 != nil || h > 14 || m > 59 {
		return time.UTC
	}
	return time.FixedZone(tz, sign*(h*3600+m*60))
}
