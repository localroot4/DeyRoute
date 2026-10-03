package tui

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

// chartStart is the first sample of every chart fixture (UTC).
var chartStart = time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC)

type chartFixture struct {
	name string
	vals []float64
	gaps []bool
	step time.Duration
	rows int
	yfmt func(float64) string
}

// chartFixtures are the series of the goldens: a realistic hour with a gap,
// a flat zero day, one spike in a day, a month without data and one point.
func chartFixtures() []chartFixture {
	hour := make([]float64, 360)
	hourGaps := make([]bool, 360)
	for i := range hour {
		// a deterministic saw of 0.5-2.5 Mb/s, no floating point library
		hour[i] = float64(500_000 + (i*37%101)*20_000)
		hourGaps[i] = i >= 100 && i < 112
	}
	zero := make([]float64, 60)
	spike := make([]float64, 1440)
	spike[700] = 50_000_000
	month := make([]float64, 30)
	monthGaps := make([]bool, 30)
	for i := range monthGaps {
		monthGaps[i] = true
	}
	return []chartFixture{
		{name: "hour", vals: hour, gaps: hourGaps, step: 10 * time.Second, rows: 6},
		{name: "flat zero", vals: zero, step: time.Minute, rows: 3},
		{name: "single spike", vals: spike, step: time.Minute, rows: 4},
		{name: "all gaps", vals: month, gaps: monthGaps, step: 24 * time.Hour, rows: 3},
		{name: "one point", vals: []float64{1_500_000}, step: 30 * time.Minute, rows: 3},
		{name: "bytes", vals: []float64{0, 1 << 20, 3 << 20, 2 << 20}, step: time.Hour, rows: 3,
			yfmt: func(v float64) string { return FormatBytes(uint64(v)) }},
	}
}

func renderFixtures(style ChartStyle, caps Caps, w int) string {
	var b strings.Builder
	for _, f := range chartFixtures() {
		fmt.Fprintf(&b, "== %s\n", f.name)
		lines := Chart(ChartSpec{
			Title: f.name, Values: f.vals, Gaps: f.gaps, Start: chartStart, Step: f.step,
			Loc: time.UTC, Rows: f.rows, Width: w, Style: style, YFormat: f.yfmt,
		}, caps)
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

func TestChartGoldens(t *testing.T) {
	styles := []struct {
		name  string
		style ChartStyle
		caps  Caps
	}{
		{"blocks", ChartBlocks, Caps{Unicode: true}},
		{"braille", ChartBraille, Caps{Unicode: true}},
		{"ascii", ChartBraille, Caps{Unicode: false}}, // braille falls back to ASCII blocks
	}
	for _, s := range styles {
		for _, w := range []int{0, 60, 80, 100, 120, 200} {
			t.Run(fmt.Sprintf("%s_%d", s.name, w), func(t *testing.T) {
				got := renderFixtures(s.style, s.caps, w)
				limit := w
				if w == 0 {
					limit = 80
				}
				for _, l := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
					require.LessOrEqual(t, lipgloss.Width(l), limit, "%q", l)
				}
				golden(t, fmt.Sprintf("chart_%s_%d.golden", s.name, w), got)
			})
		}
	}
}

func TestSparklineGolden(t *testing.T) {
	var b strings.Builder
	for _, f := range chartFixtures() {
		for _, c := range []Caps{{Unicode: true}, {Unicode: false}} {
			for _, w := range []int{1, 10, 30, 60} {
				fmt.Fprintf(&b, "%-12s u=%-5v w=%-2d |%s|\n", f.name, c.Unicode, w, Sparkline(f.vals, f.gaps, c, w))
			}
		}
	}
	golden(t, "chart_sparkline.golden", b.String())
}

func TestSparklineLevels(t *testing.T) {
	u := Caps{Unicode: true}
	require.Equal(t, "▁▂▅█", Sparkline([]float64{0, 1, 5, 10}, nil, u, 4))
	require.Equal(t, " .=#", Sparkline([]float64{0, 1, 5, 10}, nil, Caps{}, 4))
	// right-aligned when there are fewer values than columns
	require.Equal(t, "  ▁█", Sparkline([]float64{0, 3}, nil, u, 4))
	// gaps, NaN, Inf and negative values
	require.Equal(t, "·▁▁▁█", Sparkline([]float64{9, math.NaN(), math.Inf(1), -4, 2}, []bool{true}, u, 5))
	require.Equal(t, "?   #", Sparkline([]float64{9, math.NaN(), math.Inf(-1), -4, 2}, []bool{true}, Caps{}, 5))
	require.Equal(t, "", Sparkline([]float64{1}, nil, u, 0))
	require.Equal(t, "   ", Sparkline(nil, nil, u, 3))
	// a tiny positive value never looks idle
	require.Equal(t, "▂█", Sparkline([]float64{1, 1e9}, nil, u, 2))
}

// The gap glyph is none of the levels in either ramp.
func TestChartGapGlyphsDistinct(t *testing.T) {
	require.NotContains(t, string(sparkUnicode), ChartGapUnicode)
	require.NotContains(t, string(eighthBlocks), ChartGapUnicode)
	require.NotContains(t, string(sparkASCII), ChartGapASCII)
	require.NotContains(t, "#. ", ChartGapASCII)
	require.Less(t, ChartGapASCII[0], byte(0x80))
}

// A constant rate stays flat when uneven bins are merged (360 points into
// 50 columns gives 7 or 8 points per column).
func TestResampleConstantIsFlat(t *testing.T) {
	vals := make([]float64, 360)
	for i := range vals {
		vals[i] = 1e6
	}
	v, g := resample(vals, nil, 50)
	for i := range v {
		require.False(t, g[i])
		require.InDelta(t, 1e6, v[i], 1e-6)
	}
	line := Sparkline(vals, nil, Caps{Unicode: true}, 50)
	require.Equal(t, strings.Repeat("█", 50), line)
	// upsampling repeats values
	v, g = resample([]float64{1, 2}, []bool{false, true}, 4)
	require.Equal(t, []float64{1, 1, 0, 0}, v)
	require.Equal(t, []bool{false, false, true, true}, g)
	// a column is a gap only when all of its points are
	v, g = resample([]float64{4, 8, 6, 2}, []bool{false, true, true, true}, 2)
	require.Equal(t, []float64{4, 0}, v)
	require.Equal(t, []bool{false, true}, g)
}

// Property: every line fits the requested width, at every width from 1 to
// 240, and the ASCII mode prints only ASCII bytes.
func TestChartLinesFitWidth(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 2)) // #nosec G404 -- test data
	for iter := 0; iter < 3000; iter++ {
		w := 1 + rng.IntN(240)
		n := rng.IntN(2000)
		vals := make([]float64, n)
		gaps := make([]bool, n)
		scale := math.Pow(10, float64(rng.IntN(13)))
		for i := range vals {
			vals[i] = rng.Float64() * scale
			gaps[i] = rng.IntN(10) == 0
			if rng.IntN(500) == 0 {
				vals[i] = math.NaN()
			}
		}
		caps := Caps{Unicode: rng.IntN(2) == 0, Color: rng.IntN(2) == 0}
		spec := ChartSpec{
			Title: "Download " + strings.Repeat("x", rng.IntN(300)), Values: vals, Gaps: gaps,
			Start: chartStart.Add(time.Duration(rng.IntN(86400)) * time.Second),
			Step:  []time.Duration{10 * time.Second, time.Minute, 30 * time.Minute, time.Hour}[rng.IntN(4)],
			Loc:   time.FixedZone("IRST", 12600), Rows: rng.IntN(13), Width: w,
			Style: ChartStyle(rng.IntN(2)), Color: rng.IntN(16),
		}
		lines := Chart(spec, caps)
		require.NotEmpty(t, lines)
		sp := Sparkline(vals, gaps, caps, w)
		for _, l := range append(lines, sp) {
			require.LessOrEqual(t, lipgloss.Width(l), w, "w=%d n=%d %q", w, n, l)
			if !caps.Unicode {
				for i := 0; i < len(l); i++ {
					require.Less(t, l[i], byte(0x80), "non-ASCII byte in %q", l)
				}
			}
		}
		require.Equal(t, w, lipgloss.Width(sp))
	}
}

func TestChartNarrowFallsBackToSparkline(t *testing.T) {
	spec := ChartSpec{Title: "Download", Values: []float64{1, 2, 3}, Step: time.Minute, Width: 12}
	lines := Chart(spec, Caps{Unicode: true})
	require.Equal(t, []string{"Download", "         ▃▆█"}, lines)
	// Width 0 means 80 and Rows 0 means 6, so the default has 6+2 lines
	spec.Width, spec.Title = 0, ""
	lines = Chart(spec, Caps{Unicode: true})
	require.Len(t, lines, 8)
}

// The x labels are drawn in the given zone.
func TestChartTicksUseLocation(t *testing.T) {
	vals := make([]float64, 360)
	spec := ChartSpec{Values: vals, Start: chartStart, Step: 10 * time.Second, Rows: 2, Width: 100}
	spec.Loc = time.UTC
	utc := Chart(spec, Caps{Unicode: true})
	spec.Loc = time.FixedZone("IRST", 3*3600+1800)
	teh := Chart(spec, Caps{Unicode: true})
	require.NotEqual(t, utc[len(utc)-1], teh[len(teh)-1])
	require.Contains(t, utc[len(utc)-1], "11:10")
	require.Contains(t, teh[len(teh)-1], "14:40")
	require.NotContains(t, teh[len(teh)-1], "11:10")
	// day steps label dates, and local midnight shows the date
	spec.Values, spec.Step, spec.Loc, spec.Width = make([]float64, 30), 24*time.Hour, time.UTC, 80
	month := Chart(spec, Caps{Unicode: true})
	require.Contains(t, month[len(month)-1], "10-07")
	spec.Values, spec.Step = make([]float64, 1440), time.Minute
	day := Chart(spec, Caps{Unicode: true})
	require.Contains(t, day[len(day)-1], "10-01")
}

// Colour wraps only the plot glyphs: without the escape codes the chart is
// the same as without colour.
func TestChartColorIsOptional(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(2) // termenv.ANSI
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	f := chartFixtures()[0]
	spec := ChartSpec{Title: "Download", Values: f.vals, Gaps: f.gaps, Start: chartStart, Step: f.step, Loc: time.UTC, Color: 2}
	colored := Chart(spec, Caps{Unicode: true, Color: true})
	plain := Chart(spec, Caps{Unicode: true})
	require.Contains(t, strings.Join(colored, "\n"), "\x1b[")
	for i := range colored {
		require.Equal(t, plain[i], stripANSI(colored[i]))
	}
	spec.Color = 0
	require.Equal(t, plain, Chart(spec, Caps{Unicode: true, Color: true}))
}

func TestFormatRate(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0 b/s"},
		{-5, "0 b/s"},
		{math.NaN(), "0 b/s"},
		{math.Inf(1), "0 b/s"},
		{0.4, "0 b/s"},
		{12.6, "13 b/s"},
		{999, "999 b/s"},
		{999.4, "999 b/s"},
		{999.5, "1.00 kb/s"},
		{1000, "1.00 kb/s"},
		{1023, "1.02 kb/s"},
		{1024, "1.02 kb/s"},
		{12_345, "12.3 kb/s"},
		{99_960, "100 kb/s"},
		{999_499, "999 kb/s"},
		{999_500, "1.00 Mb/s"},
		{2_500_000, "2.50 Mb/s"},
		{123_456_789, "123 Mb/s"},
		{1e9, "1.00 Gb/s"},
		{4.56e11, "456 Gb/s"},
		{1e12, "1.00 Tb/s"},
		{1.5e15, "1500 Tb/s"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, FormatRate(c.in), "%v", c.in)
		require.LessOrEqual(t, len(FormatRate(c.in)), chartLabelWidth)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1000 B"},
		{1023, "1023 B"},
		{1024, "1 KiB"},
		{1025, "2 KiB"},
		{1536, "2 KiB"},
		{1023 * 1024, "1023 KiB"},
		{1023*1024 + 1, "1.0 MiB"},
		{1 << 20, "1.0 MiB"},
		{3 << 19, "1.5 MiB"},
		{1<<30 - 1, "1.0 GiB"},
		{1 << 30, "1.0 GiB"},
		{38 << 27, "4.8 GiB"},
		{1 << 40, "1.0 TiB"},
		{math.MaxUint64, "16777216.0 TiB"},
	}
	for _, c := range cases {
		require.Equal(t, c.want, FormatBytes(c.in), "%d", c.in)
	}
	// the same forms as sizeText where both apply
	for _, n := range []uint64{1 << 20, 5 << 20, 7 << 30, 2048} {
		require.Equal(t, sizeText(int64(n)), FormatBytes(n)) // #nosec G115 -- small test values
	}
}

func TestNiceCeil(t *testing.T) {
	for in, want := range map[float64]float64{
		0: 0, -1: 0, 1: 1, 1.1: 2, 2: 2, 2.2: 2.5, 3: 5, 5: 5, 7: 10, 10: 10, 11: 20,
		2.6e6: 5e6, 0.3: 0.5, 999: 1000, 1001: 2000,
	} {
		require.InDelta(t, want, niceCeil(in), want*1e-12, "%v", in)
	}
	require.Equal(t, 0.0, niceCeil(math.Inf(1)))
	require.Equal(t, 0.0, niceCeil(math.NaN()))
}
