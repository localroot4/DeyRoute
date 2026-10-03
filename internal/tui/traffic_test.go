package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// ---- fixtures

const gib = 1 << 30

func intp(n int) *int { return &n }

// trafficNowFixture is the TrafficNow of the two sample tunnels.
func trafficNowFixture() map[string]*api.TrafficNow {
	return map[string]*api.TrafficNow{
		"main":  {At: testNow, Available: true, RateInBitS: 1_200_000, RateOutBitS: 12_300_000, TodayIn: 512 << 20, TodayOut: 3*gib + 800<<20},
		"games": {At: testNow, Available: true, RateInBitS: 64_000, RateOutBitS: 250_000, TodayIn: 20 << 20, TodayOut: 95 << 20},
	}
}

// trafficStatus is the section 6 sample with traffic numbers.
func trafficStatus() api.Status {
	st := sampleStatus()
	now := trafficNowFixture()
	for i := range st.Tunnels {
		st.Tunnels[i].Traffic = now[st.Tunnels[i].ID]
	}
	return st
}

// tunnelSeries is a deterministic tunnel series of n points of step
// seconds ending at testNow: download 1-10 Mb/s in a saw, upload a tenth
// of it, connections 8-12 (none for UDP), and gaps where gap says.
func tunnelSeries(id, name string, n, step int, udp bool, gap func(i int) bool) api.TrafficSeries {
	s := api.TrafficSeries{ID: id, Kind: api.TrafficKindTunnel, Name: name, Available: true, StepS: step}
	start := testNow.Add(-time.Duration(n*step) * time.Second)
	for i := range n {
		p := api.TrafficPoint{At: start.Add(time.Duration(i*step) * time.Second)}
		if gap != nil && gap(i) {
			p.Gap = true
		} else {
			mbit := uint64(1 + i%10)
			p.BytesOut = mbit * 125_000 * uint64(step)
			p.BytesIn = p.BytesOut / 10
			if !udp {
				p.Conns = intp(8 + i%5)
			}
		}
		s.Points = append(s.Points, p)
	}
	s.Totals = &api.TrafficTotals{
		TodayStart: time.Date(2026, 9, 29, 20, 30, 0, 0, time.UTC),
		TodayIn:    512 << 20, TodayOut: 3*gib + 800<<20,
		Days30In: 40 * gib, Days30Out: 410 * gib,
		PeriodStart: time.Date(2026, 8, 31, 20, 30, 0, 0, time.UTC),
		PeriodIn:    30 * gib, PeriodOut: 15 * gib,
		QuotaBytes: 100 * gib,
	}
	return s
}

// hostSeries is a node or hub series: CPU 2-40 %, RAM 100-300 MiB.
func hostSeries(kind, id string, n, step int) api.TrafficSeries {
	s := api.TrafficSeries{ID: id, Kind: kind, Available: true, StepS: step}
	start := testNow.Add(-time.Duration(n*step) * time.Second)
	for i := range n {
		s.Points = append(s.Points, api.TrafficPoint{
			At:         start.Add(time.Duration(i*step) * time.Second),
			CPUPercent: float64(2 + (i*7)%39),
			RAMBytes:   uint64(100+(i*13)%201) << 20,
			Gap:        i == 3,
		})
	}
	return s
}

// blockReport is the answer to TrafficBlockQuery for the sample tunnels.
func blockReport() *api.TrafficReport {
	return &api.TrafficReport{
		GeneratedAt: testNow, Period: api.TrafficPeriod1h, Available: true, Timezone: "Asia/Tehran",
		Series: []api.TrafficSeries{
			tunnelSeries("main", "Main 443/2053", 30, 120, false, nil),
			tunnelSeries("games", "Games UDP", 30, 120, true, func(i int) bool { return i >= 4 && i < 7 }),
		},
	}
}

// x061 is the reason of a hub without byte counting.
func x061() *api.ErrorDTO {
	return api.ToDTO(deyerr.New(deyerr.X061, deyerr.Params{"reason": "nft is not installed"}))
}

// ---- the dashboard's TRAFFIC block

func TestDashboardTrafficGolden(t *testing.T) {
	st := trafficStatus()
	uni := renderDashboard(dashApp(Caps{Unicode: true, Width: 120}), st, blockReport(), testNow, 0)
	golden(t, "dashboard_traffic_unicode_120.golden", uni)
	for _, want := range []string{
		" TRAFFIC  last hour · ↓ download to users · ↑ upload from users\n",
		"↓ 12.3 Mb/s  ↑ 1.20 Mb/s  today ↓ 3.8 GiB ↑ 512.0 MiB\n",
		"↓  250 kb/s  ↑ 64.0 kb/s  today ↓ 95.0 MiB ↑ 20.0 MiB\n",
	} {
		require.Contains(t, uni, want)
	}
	// TUNNELS stays the section 6 table and TRAFFIC comes right under it.
	plain := renderStatus(dashApp(Caps{Unicode: true, Width: 120}), sampleStatus(), testNow)
	require.True(t, strings.HasPrefix(uni, plain[:strings.Index(plain, " NODES\n")]))
	require.Less(t, strings.Index(uni, " TRAFFIC"), strings.Index(uni, " NODES"))

	a := dashApp(Caps{Unicode: false, Width: 80})
	block := TrafficBlock(a.caps, st.Tunnels, blockReport(), 0)
	for _, l := range block {
		require.Equal(t, asciiOnly(l), l, "the ASCII block must need no substitution")
		require.LessOrEqual(t, len(l), 80)
	}
	asc := asciiOnly(renderDashboard(a, st, blockReport(), testNow, 0))
	golden(t, "dashboard_traffic_ascii_80.golden", asc)
	require.NotContains(t, asc, "today") // below 100 columns the today column goes
	require.Contains(t, asc, "v 12.3 Mb/s  ^ 1.20 Mb/s")
	for _, l := range strings.Split(asc, "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
	}
}

// Without traffic numbers the dashboard is the section 6 sample, byte for
// byte; with them but without sparklines yet the rates are still shown.
func TestDashboardTrafficOnlyWhenPresent(t *testing.T) {
	a := dashApp(Caps{Unicode: true, Width: 120})
	require.Equal(t, renderStatus(a, sampleStatus(), testNow), renderDashboard(a, sampleStatus(), blockReport(), testNow, 0))
	require.NotContains(t, renderStatus(a, sampleStatus(), testNow), "TRAFFIC")
	require.Nil(t, TrafficBlock(a.caps, sampleTunnels(), blockReport(), 0))

	out := renderStatus(a, trafficStatus(), testNow)
	require.Contains(t, out, " TRAFFIC")
	require.Contains(t, out, "↓ 12.3 Mb/s")
	require.False(t, strings.ContainsAny(out, "▁▂▃▄▅▆▇█"), out)
	// -1 leaves the block out (no room on a short window).
	require.NotContains(t, renderDashboard(a, trafficStatus(), blockReport(), testNow, -1), "TRAFFIC")
}

// A hub that cannot count bytes shows "—" and never "0 B" or "0 b/s".
func TestDashboardTrafficUnavailable(t *testing.T) {
	st := sampleStatus()
	for i := range st.Tunnels {
		st.Tunnels[i].Traffic = &api.TrafficNow{At: testNow}
	}
	rep := &api.TrafficReport{Period: api.TrafficPeriod1h, Reason: x061(), Series: []api.TrafficSeries{
		{ID: "main", Kind: api.TrafficKindTunnel, Reason: x061(), StepS: 120},
	}}
	for _, c := range []Caps{{Unicode: true, Width: 120}, {Unicode: false, Width: 80}} {
		lines := TrafficBlock(c, st.Tunnels, rep, 0)
		require.Len(t, lines, 2)
		for _, l := range lines {
			require.NotContains(t, l, "0 B")
			require.NotContains(t, l, "0 b/s")
		}
		if c.Unicode {
			require.Contains(t, lines[0], "—")
			require.Contains(t, lines[0], "↓         —  ↑         —  today —")
		} else {
			require.Contains(t, lines[0], "v         -  ^         -")
		}
	}
}

// The block keeps the busiest tunnels in table order and says how many
// more there are.
func TestTrafficBlockMaxRows(t *testing.T) {
	var ts []api.TunnelInfo
	for i := range 5 {
		ts = append(ts, api.TunnelInfo{ID: fmt.Sprintf("t%d", i), Enabled: true,
			Traffic: &api.TrafficNow{Available: true, RateOutBitS: uint64([]int{1, 50, 3, 40, 2}[i]) * 1_000_000}})
	}
	ts = append(ts, api.TunnelInfo{ID: "off"}) // disabled, no numbers: not listed
	lines := TrafficBlock(Caps{Unicode: true, Width: 120}, ts, nil, 3)
	require.Len(t, lines, 3)
	require.True(t, strings.HasPrefix(lines[0], "  t1 "), lines[0])
	require.True(t, strings.HasPrefix(lines[1], "  t3 "), lines[1])
	require.Equal(t, "  +3 more: t opens the traffic of every tunnel", lines[2])
	require.Len(t, TrafficBlock(Caps{Unicode: true, Width: 120}, ts, nil, 0), 5)
	// Too narrow for a sparkline: it is left out, the rates stay.
	for _, l := range TrafficBlock(Caps{Unicode: true, Width: 40}, trafficStatus().Tunnels, blockReport(), 0) {
		require.LessOrEqual(t, width(l), 40)
		require.False(t, strings.ContainsAny(l, "▁▂▃▄▅▆▇█"), l)
	}
}

// On a short window the TRAFFIC block shrinks so the warnings stay
// visible (fit cuts the lines just above the footer first).
func TestDashboardTrafficKeepsWarnings(t *testing.T) {
	st := trafficStatus()
	st.Nodes = st.Nodes[:1]
	st.Events = st.Events[:1]
	st.Warnings = []api.Warning{{Message: "TLS certificate of main expires in 12 days", Tunnel: "main"}}
	for i := range 8 {
		id := fmt.Sprintf("extra-%d", i)
		st.Tunnels = append(st.Tunnels, api.TunnelInfo{ID: id, Name: id, Enabled: true, State: state.StateUp,
			Traffic: &api.TrafficNow{Available: true, RateOutBitS: uint64(i) * 1000}})
	}
	for i := range st.Tunnels {
		st.Tunnels[i].Warnings = nil
	}
	stub := &apitest.Stub{
		StatusFn:  func(context.Context) (api.Status, error) { return st, nil },
		TrafficFn: func(context.Context, api.TrafficQuery) (api.TrafficReport, error) { return *blockReport(), nil },
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: stub})
	nm, _ := h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	h.m = nm
	h.choose("1")
	v := h.view()
	require.LessOrEqual(t, len(strings.Split(strings.TrimSuffix(v, "\n"), "\n")), 36, v)
	h.must("TRAFFIC", "more: t opens the traffic", "! main: TLS certificate of main expires in 12 days", "LAST EVENTS")
	h.mustNot("more lines")

	// Even shorter: the block goes entirely, the warning stays.
	nm, _ = h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 27})
	h.m = nm
	v = h.view()
	require.NotContains(t, v, " TRAFFIC")
	require.Contains(t, v, "! main: TLS certificate")
}

// The dashboard asks for the sparklines with Status, every 30 seconds;
// a daemon without traffic statistics is not asked again.
func TestDashboardTrafficFetch(t *testing.T) {
	var mu sync.Mutex
	var queries []api.TrafficQuery
	fail := false
	stub := &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) { return trafficStatus(), nil },
		TrafficFn: func(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
			mu.Lock()
			defer mu.Unlock()
			queries = append(queries, q)
			if fail {
				return api.TrafficReport{}, deyerr.New(deyerr.X008, deyerr.Params{"feature": "traffic statistics"})
			}
			return *blockReport(), nil
		},
	}
	now := testNow
	var clock sync.Mutex
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: stub,
		Now: func() time.Time { clock.Lock(); defer clock.Unlock(); return now }})
	h.choose("1")
	h.must("TRAFFIC", "▂", "t: traffic charts")
	mu.Lock()
	require.Equal(t, []api.TrafficQuery{TrafficBlockQuery()}, queries)
	mu.Unlock()
	h.press("r") // within 30 s: Status only
	mu.Lock()
	require.Len(t, queries, 1)
	fail = true
	mu.Unlock()
	clock.Lock()
	now = now.Add(31 * time.Second)
	clock.Unlock()
	h.press("r")
	h.must("TRAFFIC", "↓ 12.3 Mb/s") // the rates stay, the dashboard never fails
	h.mustNot("DEY-X008", "▂")
	clock.Lock()
	now = now.Add(time.Minute)
	clock.Unlock()
	h.press("r")
	mu.Lock()
	require.Len(t, queries, 2, "not asked again after DEY-X008")
	mu.Unlock()
}

// ---- the Traffic screen

func trafficStub(mu *sync.Mutex, queries *[]api.TrafficQuery) *apitest.Stub {
	return &apitest.Stub{
		StatusFn:     func(context.Context) (api.Status, error) { return trafficStatus(), nil },
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) { return trafficStatus().Tunnels, nil },
		NodeListFn:   func(context.Context) ([]api.NodeInfo, error) { return sampleNodes(), nil },
		TrafficFn: func(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
			mu.Lock()
			*queries = append(*queries, q)
			mu.Unlock()
			rep := api.TrafficReport{GeneratedAt: testNow, Period: q.Period, Available: true, Timezone: "UTC+03:30"}
			for _, target := range q.Targets {
				kind, id, _ := api.ParseTrafficTarget(target)
				if kind == api.TrafficKindTunnel {
					rep.Series = append(rep.Series, tunnelSeries(id, "", 60, 60, false, func(i int) bool { return i == 20 }))
				} else {
					rep.Series = append(rep.Series, hostSeries(kind, id, 60, 60))
				}
			}
			if len(q.Targets) == 0 {
				rep.Series = blockReport().Series
			}
			return rep, nil
		},
	}
}

func TestTrafficScreen(t *testing.T) {
	var mu sync.Mutex
	var queries []api.TrafficQuery
	last := func() api.TrafficQuery {
		mu.Lock()
		defer mu.Unlock()
		return queries[len(queries)-1]
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: trafficStub(&mu, &queries)})
	h.choose("6")
	h.must("6) Traffic and load")
	h.choose("6")
	h.must("Show the traffic of which tunnel", "tunnel  main", "Main 443/2053", "node    de-1", "hub     ir-1", "this server")
	h.choose("1")
	h.must("Traffic and load: main", "Period: [1h] 24h 7d 30d · chart: blocks",
		"Download (to users)", "Upload (from users)", "Connections", "now 12 · peak 12",
		"Today ↓ 3.8 GiB ↑ 512.0 MiB · 30 days ↓ 410.0 GiB ↑ 40.0 GiB",
		"Since 2026-09-01 (quota period) ↓ 15.0 GiB ↑ 30.0 GiB · quota 45% of 100.0 GiB",
		"hub's zone (UTC+03:30)", "Updated 12:45:00 · refreshes every 2s")
	q := last()
	require.Equal(t, []string{"tunnel:main"}, q.Targets)
	require.Equal(t, api.TrafficPeriod1h, q.Period)
	require.Equal(t, 120-chartLabelWidth-3, q.MaxPoints)

	// The 1h view refreshes by itself; the others do not.
	h.mu.Lock()
	tick := h.ticks[len(h.ticks)-1]
	h.mu.Unlock()
	n := len(queries)
	h.handle(tick)
	h.settle()
	require.Len(t, queries, n+1)

	for key, period := range map[string]string{"2": "24h", "3": "7d", "4": "30d", "1": "1h"} {
		h.press(key)
		require.Equal(t, period, last().Period, key)
		h.must("[" + period + "]")
	}
	h.press("4")
	h.mu.Lock()
	ticks := len(h.ticks)
	h.mu.Unlock()
	h.press("r")
	h.mu.Lock()
	require.Equal(t, ticks, len(h.ticks), "30d does not refresh by itself")
	h.mu.Unlock()
	h.must("Updated 12:45:00 · r refreshes")

	// b switches to braille: twice the points, braille dots in the charts.
	h.press("b")
	require.Equal(t, 2*(120-chartLabelWidth-3), last().MaxPoints)
	h.must("chart: braille")
	require.True(t, strings.ContainsFunc(h.view(), func(r rune) bool { return r > 0x2800 && r <= 0x28ff }), h.view())
	h.press("b")
	h.must("chart: blocks")

	// Enter (line mode sends it after a digit) stays on the screen.
	depth := h.depth()
	h.press("2", "enter")
	require.Equal(t, depth, h.depth())
	require.Equal(t, "24h", last().Period)

	// A node shows CPU and RAM.
	h.press("q")
	h.must("Show the traffic of which tunnel")
	h.choose("3")
	h.must("Traffic and load: de-1", "CPU  avg", "RAM  peak")
	h.mustNot("Download (to users)")
	require.Equal(t, []string{"node:de-1"}, last().Targets)
	h.press("q")
	h.choose("5")
	h.must("Traffic and load: hub", "CPU  avg")
	require.Equal(t, []string{"hub"}, last().Targets)
}

// The dashboard key t opens the same picker.
func TestDashboardKeyT(t *testing.T) {
	var mu sync.Mutex
	var queries []api.TrafficQuery
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: trafficStub(&mu, &queries)})
	h.choose("1")
	h.press("t")
	h.must("Show the traffic of which tunnel", "tunnel  games")
	h.choose("2")
	h.must("Traffic and load: games")
	h.press("q", "q")
	h.must("TRAFFIC")
}

// Without traffic statistics (an older daemon) or for an unknown target
// the screen shows the error once and does not keep refreshing.
func TestTrafficScreenErrors(t *testing.T) {
	stub := &apitest.Stub{
		StatusFn:     func(context.Context) (api.Status, error) { return sampleStatus(), nil },
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) { return sampleTunnels(), nil },
		NodeListFn:   func(context.Context) ([]api.NodeInfo, error) { return nil, deyerr.New(deyerr.X042, nil) },
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: stub})
	h.choose("6").choose("6")
	h.mustNot("node    de-1") // a failed node list only leaves the nodes out
	h.choose("1")
	h.must("DEY-X008")
	h.mu.Lock()
	require.Empty(t, h.ticks)
	h.mu.Unlock()
	require.False(t, retryable(deyerr.New(deyerr.C027, nil)))
	require.True(t, retryable(deyerr.New(deyerr.X042, nil)))
	require.True(t, retryable(nil))

	// ASCII: braille is refused, blocks stay.
	a := NewModel(Options{Caps: Caps{Width: 80}}).a
	s := newTrafficScreen(trafficTarget{target: "tunnel:main", kind: api.TrafficKindTunnel, id: "main", label: "main"})
	a.push(s)
	s.update(a, keyMsg("b"))
	require.Equal(t, ChartBlocks, s.style)
	require.Contains(t, s.view(a), "Braille needs a UTF-8 terminal")
}

// ---- TrafficPanels

func panelReport(available bool) (api.TrafficReport, []api.TrafficSeries) {
	rep := api.TrafficReport{GeneratedAt: testNow, Period: api.TrafficPeriod24h, Available: available, Timezone: "UTC+03:30"}
	tun := tunnelSeries("main", "Main", 144, 600, false, func(i int) bool { return i >= 30 && i < 34 })
	udp := tunnelSeries("games", "Games", 144, 600, true, nil)
	if !available {
		rep.Reason = x061()
		tun.Available, tun.Reason, tun.Totals = false, x061(), nil
		for i := range tun.Points {
			tun.Points[i].BytesIn, tun.Points[i].BytesOut = 0, 0
		}
	}
	return rep, []api.TrafficSeries{tun, udp, hostSeries(api.TrafficKindNode, "de-1", 144, 600), hostSeries(api.TrafficKindHub, "hub", 144, 600)}
}

func renderPanels(c Caps, style ChartStyle) string {
	var b strings.Builder
	for _, avail := range []bool{true, false} {
		rep, series := panelReport(avail)
		for _, s := range series {
			if !avail && s.Kind != api.TrafficKindTunnel {
				continue
			}
			for _, w := range []int{0, 80, 120, 200} {
				cc := c
				cc.Width = w
				fmt.Fprintf(&b, "== %s %s available=%v width=%d\n", s.Kind, s.ID, avail, w)
				for _, l := range TrafficPanels(cc, rep, s, TrafficView{Loc: time.UTC, Style: style, Rows: 4}) {
					b.WriteString(l + "\n")
				}
			}
		}
	}
	return b.String()
}

func TestTrafficPanelsGolden(t *testing.T) {
	uni := renderPanels(Caps{Unicode: true}, ChartBlocks)
	golden(t, "traffic_panels_unicode.golden", uni)
	golden(t, "traffic_panels_braille.golden", renderPanels(Caps{Unicode: true}, ChartBraille))
	asc := renderPanels(Caps{}, ChartBraille) // ASCII falls back to blocks
	golden(t, "traffic_panels_ascii.golden", asc)
	require.Equal(t, asciiOnly(asc), asc, "ASCII panels must need no substitution")

	for _, out := range []string{uni, asc} {
		w := 80
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "== ") {
				fmt.Sscanf(l[strings.LastIndex(l, "width=")+6:], "%d", &w)
				if w == 0 {
					w = 80
				}
				continue
			}
			require.LessOrEqual(t, width(l), w, "too wide: %q", l)
		}
	}
	require.Contains(t, uni, "Connections  n/a (UDP has no connection state)")
	require.Contains(t, uni, "Download and upload: — (this hub does not count bytes)")
	require.Contains(t, uni, "DEY-X061")
	require.Contains(t, uni, "nft is not installed")
	require.Contains(t, asc, "Download and upload: - (this hub does not count bytes)")
	unavailable := uni[strings.Index(uni, "available=false"):]
	for _, never := range []string{"↓ 0 B", "↑ 0 B", "0 b/s", "Today", "Download (to users)"} {
		require.NotContains(t, unavailable, never)
	}
}

// Colour never changes the text, and the gap glyph is not a level.
func TestTrafficPanelsColour(t *testing.T) {
	rep, series := panelReport(true)
	plain := TrafficPanels(Caps{Unicode: true, Width: 100}, rep, series[0], TrafficView{Loc: time.UTC})
	coloured := TrafficPanels(Caps{Unicode: true, Color: true, Width: 100}, rep, series[0], TrafficView{Loc: time.UTC})
	require.Len(t, coloured, len(plain))
	for i := range plain {
		require.Equal(t, plain[i], stripANSI(coloured[i]))
	}
	require.Contains(t, strings.Join(plain, "\n"), ChartGapUnicode)
	require.NotContains(t, sparkASCII, []rune(ChartGapASCII)[0])
	none := TrafficPanels(Caps{Unicode: true}, api.TrafficReport{Available: true}, api.TrafficSeries{ID: "x", Kind: api.TrafficKindTunnel, Available: true}, TrafficView{})
	require.Equal(t, []string{"  No samples in this period yet."}, none)
	host := TrafficPanels(Caps{Unicode: true}, api.TrafficReport{}, api.TrafficSeries{ID: "hub", Kind: api.TrafficKindHub}, TrafficView{})
	require.Equal(t, []string{"  No samples in this period yet."}, host)
}

// ---- tunnel detail

func TestTunnelDetailTraffic(t *testing.T) {
	a := dashApp(Caps{Unicode: true, Width: 120})
	d := api.TunnelDetail{TunnelInfo: trafficStatus().Tunnels[0]}
	out := renderDetail(a, tunnelDetail{d: d, traffic: blockReport()})
	for _, want := range []string{
		"Traffic    now ↓ 12.3 Mb/s ↑ 1.20 Mb/s · today ↓ 3.8 GiB ↑ 512.0 MiB",
		"Last hour  ▂", "30 days    ↓ 410.0 GiB ↑ 40.0 GiB · quota 45% of 100.0 GiB",
	} {
		require.Contains(t, out, want)
	}
	// Without the series (the call failed): the rates only.
	out = renderDetail(a, tunnelDetail{d: d})
	require.Contains(t, out, "now ↓ 12.3 Mb/s")
	require.NotContains(t, out, "Last hour")

	// Not counted: "—" with the reason, never 0.
	d.Traffic = &api.TrafficNow{At: testNow}
	rep := &api.TrafficReport{Reason: x061(), Series: []api.TrafficSeries{{ID: "main", Kind: api.TrafficKindTunnel, Reason: x061()}}}
	out = renderDetail(a, tunnelDetail{d: d, traffic: rep})
	require.Contains(t, out, "Traffic    — (DEY-X061 Traffic accounting is not available)")
	require.NotContains(t, out, "0 B")
	asc := renderDetail(dashApp(Caps{Width: 80}), tunnelDetail{d: d, traffic: rep})
	require.Contains(t, asc, "Traffic    - (DEY-X061")

	// No monitoring: no traffic rows at all.
	d.Traffic = nil
	require.NotContains(t, renderDetail(a, tunnelDetail{d: d}), "Traffic")
}

func TestShowTunnelAsksTraffic(t *testing.T) {
	var mu sync.Mutex
	var queries []api.TrafficQuery
	stub := trafficStub(&mu, &queries)
	stub.TunnelShowFn = func(_ context.Context, id string) (api.TunnelDetail, error) {
		return api.TunnelDetail{TunnelInfo: trafficStatus().Tunnels[0]}, nil
	}
	stub.TransportListFn = func(context.Context) ([]api.TransportInfo, error) { return nil, nil }
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: stub})
	h.choose("2").choose("7").choose("1")
	h.must("Traffic", "Last hour", "30 days")
	mu.Lock()
	require.Equal(t, []string{"tunnel:main"}, queries[len(queries)-1].Targets)
	mu.Unlock()
}

// ---- small pieces

func TestTrafficEventWordsAndArrows(t *testing.T) {
	require.Equal(t, "quota", EventWord(state.EvTrafficQuota))
	require.Equal(t, "tuning", EventWord(state.EvTuneDrift))
	require.Equal(t, "v 1 ^ 2 - x", ToASCII("↓ 1 ↑ 2 — x"))
	require.Equal(t, 45, quotaPercent(api.TrafficTotals{PeriodIn: 30, PeriodOut: 15, QuotaBytes: 100}))
	require.Equal(t, 0, quotaPercent(api.TrafficTotals{PeriodIn: 30}))
	rep, series := panelReport(true)
	require.Equal(t, "45%", QuotaText(rep, series[0]))
	series[0].Totals.QuotaBytes = 0
	require.Equal(t, "—", QuotaText(rep, series[0]))
	require.Equal(t, "—", TrafficSpark(Caps{Unicode: true}, api.TrafficReport{}, series[0], 10))
	require.Equal(t, "-", TrafficSpark(Caps{}, api.TrafficReport{}, series[0], 10))
}

func TestHubZone(t *testing.T) {
	at := time.Date(2026, 8, 31, 20, 30, 0, 0, time.UTC)
	require.Equal(t, "2026-09-01 00:00", at.In(hubZone("UTC+03:30")).Format("2006-01-02 15:04"))
	require.Equal(t, "2026-08-31 15:30", at.In(hubZone("UTC-05")).Format("2006-01-02 15:04"))
	require.Equal(t, time.UTC, hubZone(""))
	for _, bad := range []string{"Mars/Base", "UTC", "UTC*3", "UTC+99:00", "UTC+3:xx"} {
		require.Equal(t, time.UTC, hubZone(bad), bad)
	}
	if loc, err := time.LoadLocation("Asia/Tehran"); err == nil {
		require.Equal(t, loc.String(), hubZone("Asia/Tehran").String())
	}
}
