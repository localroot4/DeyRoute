package tui

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/localroot4/deyroute/internal/i18n"
)

// This file draws the traffic and load charts: one-line sparklines and
// multi-row charts with a y axis in bit/s and an x axis in the viewer's time
// zone. It is pure (no I/O, no daemon types) so the TUI and the CLI share it.
//
// Glyphs:
//   - Unicode blocks: the eighth blocks ▁▂▃▄▅▆▇█.
//   - Unicode braille: 2×4 dots per cell, only with Style ChartBraille.
//   - ASCII (Caps.Unicode false): the ramp " .:-=+*#" for sparklines and
//     '#' with '.' for a partly filled cell in charts.
//
// A gap (no sample) is drawn with its own glyph that is none of the levels:
// '·' in Unicode and '?' (no data) in ASCII.

// ChartStyle selects the glyphs of a multi-row chart.
type ChartStyle int

const (
	// ChartBlocks draws eighth blocks (the default).
	ChartBlocks ChartStyle = iota
	// ChartBraille draws braille dots: twice the horizontal and four times
	// the vertical resolution per cell. ASCII terminals fall back to blocks.
	ChartBraille
)

// The gap glyphs: a column with no sample. Neither is a level of a ramp.
const (
	ChartGapUnicode = "·"
	ChartGapASCII   = "?"
)

const (
	chartDefaultWidth = 80
	chartDefaultRows  = 6
	chartMaxRows      = 64
	chartMaxWidth     = 4096
	chartLabelWidth   = 9 // "1.23 Gb/s" is the widest FormatRate label
	chartMinPlot      = 8 // below this the chart falls back to a sparkline
	chartTickGap      = 2 // columns kept free between two x labels
	// chartMaxSpanMS bounds the x span (about 8900 years) so the column
	// arithmetic cannot overflow; longer spans get no x labels.
	chartMaxSpanMS = int64(1) << 48
)

var (
	sparkUnicode = []rune("▁▂▃▄▅▆▇█")
	sparkASCII   = []rune(" .:-=+*#")
	// eighthBlocks[k] fills k eighths of a cell from the bottom.
	eighthBlocks = []rune(" ▁▂▃▄▅▆▇█")
)

// braille dot bits, from the bottom row up, for the left and right column.
var (
	brailleLeft  = [4]rune{0x40, 0x04, 0x02, 0x01}
	brailleRight = [4]rune{0x80, 0x20, 0x10, 0x08}
)

// ChartSpec describes one multi-row chart.
type ChartSpec struct {
	// Title is the first line. It names what is drawn (for example
	// "Download"), so colour is never needed to read the chart.
	Title string
	// Values are the samples, oldest first. Values[i] covers
	// [Start+i*Step, Start+(i+1)*Step). NaN, ±Inf and negative values
	// count as 0.
	Values []float64
	// Gaps marks samples that do not exist (same index as Values).
	Gaps []bool
	// Start and Step place the samples on the x axis.
	Start time.Time
	Step  time.Duration
	// Loc is the time zone of the x labels; nil means time.Local.
	Loc *time.Location
	// Rows is the height of the plot in lines; 0 means 6.
	Rows int
	// Width is the width of every line in columns; 0 means 80.
	Width int
	// Style selects blocks or braille.
	Style ChartStyle
	// YFormat formats the y labels; nil means FormatRate (bit/s).
	YFormat func(float64) string
	// Color is a 16-colour ANSI index (1-15) for the plot glyphs; 0 draws
	// them in the default colour. It is ignored without Caps.Color.
	Color int
}

// Sparkline draws vals in one line of at most width columns. With more
// values than columns each column shows the mean of the values it covers
// (a constant rate stays flat); with fewer, the line is right-aligned so the
// newest value sits at the right edge. The line is scaled to its maximum with
// a zero floor: 0 is the lowest level and any positive value shows at least
// the second. A column whose every value is a gap shows the gap glyph.
func Sparkline(vals []float64, gaps []bool, caps Caps, width int) string {
	if width <= 0 {
		return ""
	}
	width = min(width, chartMaxWidth)
	cols := min(len(vals), width)
	v, g := resample(vals, gaps, cols)
	top := maxValue(v, g)
	ramp, gap := sparkUnicode, ChartGapUnicode
	if !caps.Unicode {
		ramp, gap = sparkASCII, ChartGapASCII
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-cols))
	for i := range v {
		if g[i] {
			b.WriteString(gap)
			continue
		}
		b.WriteRune(ramp[level(v[i], top, len(ramp)-1)])
	}
	return b.String()
}

// Chart draws spec as lines of at most spec.Width columns: the title, Rows
// plot lines with y labels, the x axis and the x labels. A width too small
// for the axes falls back to the title and a sparkline.
func Chart(spec ChartSpec, caps Caps) []string {
	w := spec.Width
	if w <= 0 {
		w = chartDefaultWidth
	}
	w = min(w, chartMaxWidth)
	rows := spec.Rows
	if rows <= 0 {
		rows = chartDefaultRows
	}
	rows = min(rows, chartMaxRows)
	yf := spec.YFormat
	if yf == nil {
		yf = FormatRate
	}
	loc := spec.Loc
	if loc == nil {
		loc = time.Local
	}
	braille := spec.Style == ChartBraille && caps.Unicode

	var out []string
	if spec.Title != "" {
		out = append(out, chartClip(caps, w, spec.Title))
	}

	labelW := chartLabelWidth
	labelW = max(labelW, width(yf(niceCeil(maxValue(sanitized(spec.Values, spec.Gaps))))), width(yf(0)))
	plotW := w - labelW - 3 // label, " ", axis, plot, one free column
	if plotW < chartMinPlot {
		return append(out, chartClip(caps, w, Sparkline(spec.Values, spec.Gaps, caps, w)))
	}

	sub := 1
	if braille {
		sub = 2
	}
	v, g := resample(spec.Values, spec.Gaps, plotW*sub)
	top := niceCeil(maxValue(v, g))

	glyphs := chartGlyphs(caps)
	for i := range rows {
		r := rows - 1 - i // row from the bottom
		label := ""
		switch {
		case top <= 0:
		case i == 0:
			label = yf(top)
		case rows >= 4 && i == rows/2:
			label = yf(top * float64(rows-i) / float64(rows))
		}
		axis := glyphs.axis
		if label != "" {
			axis = glyphs.tickY
		}
		var cells string
		if braille {
			cells = brailleRow(v, g, top, rows, r)
		} else {
			cells = blockRow(v, g, top, rows, r, caps.Unicode)
		}
		line := chartPadLeft(label, labelW) + " " + axis + chartPaint(caps, spec.Color, strings.TrimRight(cells, " "))
		out = append(out, chartClip(caps, w, line))
	}

	axisLine, labels := xAxis(spec, len(spec.Values), plotW, loc, glyphs)
	out = append(out,
		chartClip(caps, w, chartPadLeft(yf(0), labelW)+" "+glyphs.corner+axisLine),
		chartClip(caps, w, strings.TrimRight(strings.Repeat(" ", labelW+2)+labels, " ")))
	return out
}

// chartGlyphs are the axis glyphs of one terminal.
type axisGlyphs struct {
	axis, tickY, corner, line, tickX string
}

func chartGlyphs(caps Caps) axisGlyphs {
	if caps.Unicode {
		return axisGlyphs{axis: "│", tickY: "┤", corner: "└", line: "─", tickX: "┬"}
	}
	return axisGlyphs{axis: "|", tickY: "+", corner: "+", line: "-", tickX: "+"}
}

// blockRow draws row r (0 = bottom) of a block chart.
func blockRow(v []float64, g []bool, top float64, rows, r int, unicode bool) string {
	var b strings.Builder
	for c := range v {
		if g[c] {
			if r == 0 {
				b.WriteString(gapGlyph(unicode))
			} else {
				b.WriteByte(' ')
			}
			continue
		}
		fill := min(max(level(v[c], top, rows*8)-r*8, 0), 8)
		switch {
		case unicode:
			b.WriteRune(eighthBlocks[fill])
		case fill == 8:
			b.WriteByte('#')
		case fill > 0:
			b.WriteByte('.')
		default:
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// brailleRow draws row r (0 = bottom) of a braille chart; v holds two
// samples per cell.
func brailleRow(v []float64, g []bool, top float64, rows, r int) string {
	var b strings.Builder
	for c := 0; c+1 < len(v); c += 2 {
		if g[c] && g[c+1] {
			if r == 0 {
				b.WriteString(ChartGapUnicode)
			} else {
				b.WriteByte(' ')
			}
			continue
		}
		var bits rune
		for side, dots := range [2][4]rune{brailleLeft, brailleRight} {
			if g[c+side] {
				continue
			}
			k := min(max(level(v[c+side], top, rows*4)-r*4, 0), 4)
			for d := range k {
				bits |= dots[d]
			}
		}
		if bits == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(0x2800 + bits)
	}
	return b.String()
}

func gapGlyph(unicode bool) string {
	if unicode {
		return ChartGapUnicode
	}
	return ChartGapASCII
}

// xAxis returns the axis line (plotW columns, tick marks included) and the
// line of tick labels for spec in loc.
func xAxis(spec ChartSpec, n, plotW int, loc *time.Location, gl axisGlyphs) (axis, labels string) {
	line := make([]string, plotW)
	for i := range line {
		line[i] = gl.line
	}
	lab := []byte(strings.Repeat(" ", plotW))
	stepMS := spec.Step.Milliseconds()
	if n <= 0 || stepMS <= 0 || stepMS > chartMaxSpanMS/int64(n) || plotW <= 0 {
		return strings.Join(line, ""), ""
	}
	spanMS := stepMS * int64(n)
	if spanMS <= 0 {
		return strings.Join(line, ""), ""
	}
	const labelLen = 5 // "15:04" and "01-02"
	step := tickStep(spanMS, plotW, labelLen+chartTickGap)
	start := spec.Start.In(loc)
	end := spec.Start.Add(time.Duration(spanMS) * time.Millisecond)
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	next := 0 // first label column still free
	for k := 0; ; k++ {
		var t time.Time
		if step >= 24*time.Hour {
			t = day.AddDate(0, 0, k*int(step/(24*time.Hour)))
		} else {
			t = day.Add(time.Duration(k) * step)
		}
		if !t.Before(end) {
			break
		}
		if t.Before(spec.Start) {
			continue
		}
		col := int(t.Sub(spec.Start).Milliseconds() * int64(plotW) / spanMS)
		if col < 0 || col >= plotW {
			continue
		}
		line[col] = gl.tickX
		text := t.Format("15:04")
		if step >= 24*time.Hour || (t.Hour() == 0 && t.Minute() == 0) {
			text = t.Format("01-02")
		}
		// the label starts under its tick; the last one may shift left
		at := min(col, plotW-labelLen)
		if at < next || at < 0 {
			continue
		}
		copy(lab[at:], text)
		next = at + labelLen + 1
	}
	return strings.Join(line, ""), string(lab)
}

// tickSteps are the x tick steps a chart may use, smallest first.
var tickSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour,
	12 * time.Hour, 24 * time.Hour, 2 * 24 * time.Hour, 7 * 24 * time.Hour,
	14 * 24 * time.Hour, 28 * 24 * time.Hour,
}

// tickStep picks the smallest step whose ticks are at least minCols apart
// on a plot of plotW columns that spans spanMS milliseconds.
func tickStep(spanMS int64, plotW, minCols int) time.Duration {
	for _, s := range tickSteps {
		if s.Milliseconds()*int64(plotW) >= int64(minCols)*spanMS {
			return s
		}
	}
	return tickSteps[len(tickSteps)-1]
}

// resample maps vals onto w columns. A column takes the mean of the non-gap
// values in its index range (so uneven bins never turn a constant rate into a
// sawtooth); with fewer values than columns a column repeats the value under
// it. A column is a gap when every value it covers is a gap, and every
// column is a gap when there are no values.
func resample(vals []float64, gaps []bool, w int) ([]float64, []bool) {
	out, og := make([]float64, max(w, 0)), make([]bool, max(w, 0))
	n := len(vals)
	for c := range out {
		if n == 0 {
			og[c] = true
			continue
		}
		lo, hi := c*n/w, (c+1)*n/w
		if hi <= lo {
			hi = lo + 1
		}
		sum, cnt := 0.0, 0
		for i := lo; i < hi; i++ {
			if i < len(gaps) && gaps[i] {
				continue
			}
			sum += clean0(vals[i])
			cnt++
		}
		if cnt == 0 {
			og[c] = true
			continue
		}
		out[c] = sum / float64(cnt)
	}
	return out, og
}

// sanitized returns vals with NaN, ±Inf and negative values set to 0 and
// gaps zeroed, for scale decisions.
func sanitized(vals []float64, gaps []bool) ([]float64, []bool) {
	out := make([]float64, len(vals))
	og := make([]bool, len(vals))
	for i, x := range vals {
		og[i] = i < len(gaps) && gaps[i]
		out[i] = clean0(x)
	}
	return out, og
}

// clean0 maps NaN, ±Inf and negative values to 0.
func clean0(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
		return 0
	}
	return x
}

// maxValue is the largest non-gap value, 0 when there is none.
func maxValue(v []float64, g []bool) float64 {
	m := 0.0
	for i, x := range v {
		if !g[i] && x > m {
			m = x
		}
	}
	return m
}

// level scales x in [0, top] to 0..steps: 0 stays 0 and any positive value
// is at least 1, so a small rate is never drawn as idle.
func level(x, top float64, steps int) int {
	if x <= 0 || top <= 0 {
		return 0
	}
	l := int(math.Round(x / top * float64(steps)))
	return min(max(l, 1), steps)
}

// niceCeil rounds v up to 1, 2, 2.5 or 5 times a power of ten, so the y
// labels are round numbers. It loops instead of using logarithms so the
// result is the same on every CPU.
func niceCeil(v float64) float64 {
	if !(v > 0) || math.IsInf(v, 0) {
		return 0
	}
	base := 1.0
	for base*10 <= v {
		base *= 10
	}
	for base > v {
		base /= 10
	}
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*base >= v {
			return m * base
		}
	}
	return 10 * base
}

// chartPadLeft right-aligns s in w columns, cutting it from the left when wider.
func chartPadLeft(s string, w int) string {
	if n := width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	} else if n > w {
		return ansi.TruncateLeft(s, n-w, "")
	}
	return s
}

// chartPaint colours s with a 16-colour index when the terminal has colour.
func chartPaint(caps Caps, col int, s string) string {
	if !caps.Color || col < 1 || col > 15 || s == "" {
		return s
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(strconv.Itoa(col))).Render(s)
}

// chartClip clips a chart line to w columns through clipANSI, then cuts
// whatever an ellipsis could not (a width of a few columns).
func chartClip(caps Caps, w int, line string) string {
	a := &app{caps: Caps{Unicode: caps.Unicode, Color: caps.Color, Width: w}}
	line = clipANSI(a, line)
	if width(line) > w {
		line = ansi.Truncate(line, w, "")
	}
	return line
}

// FormatRate formats a rate in bit/s with three significant digits:
// "0 b/s", "999 b/s", "1.00 kb/s", "12.3 Mb/s", "456 Gb/s".
func FormatRate(bps float64) string {
	units := []i18n.Key{i18n.TUIRateBit, i18n.TUIRateKbit, i18n.TUIRateMbit, i18n.TUIRateGbit, i18n.TUIRateTbit}
	v := clean0(bps)
	u := 0
	for u < len(units)-1 && v >= 999.5 {
		v /= 1000
		u++
	}
	var num string
	switch {
	case u == 0 || v >= 99.95:
		num = strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	case v >= 9.995:
		num = strconv.FormatFloat(v, 'f', 1, 64)
	default:
		num = strconv.FormatFloat(v, 'f', 2, 64)
	}
	return i18n.T(units[u], num)
}

// FormatBytes formats a byte count in IEC units with the same forms as
// sizeText: "12 B", "4 KiB" (rounded up), "1.5 MiB", "3.8 GiB", "2.0 TiB".
func FormatBytes(n uint64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
		tib = 1 << 40
	)
	switch {
	case n < kib:
		return i18n.T(i18n.TUIBytes, n)
	case n <= 1023*kib:
		return i18n.T(i18n.TUIKiB, (n+kib-1)/kib)
	case float64(n)/mib < 1023.95:
		return i18n.T(i18n.TUIMiB, float64(n)/mib)
	case float64(n)/gib < 1023.95:
		return i18n.T(i18n.TUIGiB, float64(n)/gib)
	}
	return i18n.T(i18n.TUITiB, float64(n)/tib)
}
