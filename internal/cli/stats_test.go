package cli

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tui"
)

var statsUpdate = flag.Bool("stats-update", false, "rewrite the stats golden files")

// statsGolden compares got with testdata/<name>.
func statsGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *statsUpdate {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}
	want, err := os.ReadFile(path) // #nosec G304 -- test fixture
	require.NoError(t, err, "run with -stats-update")
	require.Equal(t, string(want), got)
}

const gib = 1 << 30

func intp(n int) *int { return &n }

// statsSeries is a deterministic tunnel series of n points of step
// seconds ending at testNow: download 1-10 Mb/s, upload a tenth.
func statsSeries(id, name string, n, step int, udp bool) api.TrafficSeries {
	s := api.TrafficSeries{ID: id, Kind: api.TrafficKindTunnel, Name: name, Available: true, StepS: step}
	start := testNow.Add(-time.Duration(n*step) * time.Second)
	for i := range n {
		p := api.TrafficPoint{At: start.Add(time.Duration(i*step) * time.Second), Gap: i == n/3}
		if !p.Gap {
			p.BytesOut = uint64(1+i%10) * 125_000 * uint64(step)
			p.BytesIn = p.BytesOut / 10
			if !udp {
				p.Conns = intp(8 + i%5)
			}
		}
		s.Points = append(s.Points, p)
	}
	s.Totals = &api.TrafficTotals{
		TodayIn: 512 << 20, TodayOut: 3*gib + 800<<20, Days30In: 40 * gib, Days30Out: 410 * gib,
		PeriodStart: time.Date(2026, 8, 31, 20, 30, 0, 0, time.UTC), PeriodIn: 30 * gib, PeriodOut: 15 * gib,
	}
	if !udp {
		s.Totals.QuotaBytes = 100 * gib
	}
	return s
}

func statsHost(kind, id string, n, step int) api.TrafficSeries {
	s := api.TrafficSeries{ID: id, Kind: kind, Available: true, StepS: step}
	start := testNow.Add(-time.Duration(n*step) * time.Second)
	for i := range n {
		s.Points = append(s.Points, api.TrafficPoint{At: start.Add(time.Duration(i*step) * time.Second),
			CPUPercent: float64(2 + (i*7)%39), RAMBytes: uint64(100+(i*13)%201) << 20})
	}
	return s
}

// statsTunnels are the tunnels with their current traffic.
func statsTunnels() []api.TunnelInfo {
	st := sampleStatus()
	st.Tunnels[0].Traffic = &api.TrafficNow{At: testNow, Available: true, RateInBitS: 1_200_000, RateOutBitS: 12_300_000,
		TodayIn: 512 << 20, TodayOut: 3*gib + 800<<20}
	st.Tunnels[1].Traffic = &api.TrafficNow{At: testNow, Available: true, RateInBitS: 64_000, RateOutBitS: 250_000,
		TodayIn: 20 << 20, TodayOut: 95 << 20}
	return st.Tunnels[:2]
}

// statsEnv is a CLI environment whose daemon answers Traffic with the
// fixtures above and records every query.
func statsEnv(t *testing.T) (*env, func() []api.TrafficQuery) {
	e := newEnv(t)
	var mu sync.Mutex
	var qs []api.TrafficQuery
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { return statsTunnels(), nil }
	e.stub.TrafficFn = func(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
		mu.Lock()
		qs = append(qs, q)
		mu.Unlock()
		rep := api.TrafficReport{GeneratedAt: testNow, Period: q.Period, Available: true, Timezone: "Asia/Tehran"}
		if len(q.Targets) == 0 {
			rep.Series = []api.TrafficSeries{statsSeries("main", "Main 443/2053", 30, 120, false), statsSeries("games", "Games UDP", 30, 120, true)}
		}
		for _, target := range q.Targets {
			kind, id, _ := api.ParseTrafficTarget(target)
			if kind == api.TrafficKindTunnel {
				rep.Series = append(rep.Series, statsSeries(id, "Main 443/2053", 144, 600, false))
			} else {
				rep.Series = append(rep.Series, statsHost(kind, id, 144, 600))
			}
		}
		return rep, nil
	}
	return e, func() []api.TrafficQuery {
		mu.Lock()
		defer mu.Unlock()
		return append([]api.TrafficQuery(nil), qs...)
	}
}

func TestStatsTable(t *testing.T) {
	e, queries := statsEnv(t)
	out := e.ok("stats")
	statsGolden(t, "stats_table_unicode_120.golden", out)
	require.Equal(t, []api.TrafficQuery{{Period: "1h", MaxPoints: 30}}, queries())
	for _, want := range []string{"TUNNEL", "NOW ↓ / ↑", "TODAY", "30 DAYS", "QUOTA", "LAST 1h",
		"12.3 Mb/s / 1.20 Mb/s", "4.3 GiB", "450.0 GiB", "45%", "↓ download to users"} {
		require.Contains(t, out, want)
	}

	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 80} }
	out = e.ok("stats", "--period", "7d")
	statsGolden(t, "stats_table_ascii_80.golden", out)
	require.Equal(t, api.TrafficQuery{Period: "7d", MaxPoints: 8}, queries()[1])
	for _, r := range out {
		require.Less(t, r, rune(128), "non-ASCII in %q", out)
	}
	require.Contains(t, out, "v download to users")

	// The tunnel list failing only empties NOW.
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { return nil, deyerr.New(deyerr.X042, nil) }
	require.Contains(t, e.ok("stats"), "| main   | -         | 4.3 GiB |")
}

func TestStatsTargets(t *testing.T) {
	e, queries := statsEnv(t)
	out := e.ok("stats", "main", "--period", "24h")
	statsGolden(t, "stats_main_24h_unicode_120.golden", out)
	q := queries()[0]
	require.Equal(t, []string{"tunnel:main"}, q.Targets)
	require.Equal(t, "24h", q.Period)
	require.Equal(t, 108, q.MaxPoints)
	for _, want := range []string{"Tunnel main (Main 443/2053) · last 24h", "Download (to users)", "Upload (from users)",
		"Connections", "Today ↓ 3.8 GiB ↑ 512.0 MiB", "quota 45% of 100.0 GiB", "hub's zone (Asia/Tehran)"} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "\x1b[", "no colour when stdout is not a terminal")

	out = e.ok("stats", "node:de-1", "hub", "--style", "braille")
	require.Equal(t, []string{"node:de-1", "hub"}, queries()[1].Targets)
	require.Equal(t, 216, queries()[1].MaxPoints)
	require.Contains(t, out, "Node de-1 · last 1h")
	require.Contains(t, out, "Hub · last 1h")
	require.Contains(t, out, "CPU  avg")
	require.Contains(t, out, "RAM  peak")
	require.True(t, strings.ContainsFunc(out, func(r rune) bool { return r > 0x2800 && r <= 0x28ff }))

	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 80} }
	out = e.ok("stats", "tunnel:main", "--style", "braille") // ASCII: blocks
	statsGolden(t, "stats_main_1h_ascii_80.golden", out)
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, len(l), 80, l)
		for _, r := range l {
			require.Less(t, r, rune(128), "non-ASCII in %q", l)
		}
	}

	e.stub.TrafficFn = func(context.Context, api.TrafficQuery) (api.TrafficReport, error) {
		return api.TrafficReport{Period: "1h", Available: true}, nil
	}
	require.Contains(t, e.ok("stats", "main"), "No data for these targets.")
	require.Contains(t, e.ok("stats"), "No tunnels yet")
}

// --json is the raw TrafficReport of docs/cli-json.md, never a chart.
func TestStatsJSON(t *testing.T) {
	e, queries := statsEnv(t)
	doc := e.json("stats", "main", "--period", "30d")
	require.Equal(t, api.TrafficQuery{Targets: []string{"tunnel:main"}, Period: "30d", MaxPoints: 120}, queries()[0])
	for _, k := range []string{"generated_at", "period", "available", "timezone", "series"} {
		require.Contains(t, doc, k)
	}
	series := doc["series"].([]any)
	require.Len(t, series, 1)
	s := series[0].(map[string]any)
	for _, k := range []string{"id", "kind", "name", "available", "step_s", "points", "totals"} {
		require.Contains(t, s, k)
	}
	p := s["points"].([]any)[0].(map[string]any)
	for _, k := range []string{"at", "bytes_in", "bytes_out", "conns"} {
		require.Contains(t, p, k)
	}
	tot := s["totals"].(map[string]any)
	for _, k := range []string{"today_start", "today_in", "today_out", "days30_in", "days30_out", "period_start", "period_in", "period_out", "quota_bytes"} {
		require.Contains(t, tot, k)
	}
	doc = e.json("stats")
	require.Equal(t, api.TrafficQuery{Period: "1h", MaxPoints: 120}, queries()[1])
	require.Len(t, doc["series"], 2)
	require.NotContains(t, e.out.String(), "▁")
}

// --watch prints one document per refresh; a failed refresh is shown and
// retried; a user error ends the command.
func TestStatsWatch(t *testing.T) {
	e, _ := statsEnv(t)
	e.g.WatchInterval = 5 * time.Millisecond
	traffic := e.stub.TrafficFn
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.stub.TrafficFn = func(c context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
		switch n.Add(1) {
		case 1:
			return api.TrafficReport{}, deyerr.New(deyerr.X042, nil)
		case 3:
			cancel()
		}
		return traffic(c, q)
	}
	code := Run(ctx, e.g, []string{"stats", "main", "--watch", "--json"})
	require.Equal(t, 0, code, e.errOut.String())
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	for _, l := range lines {
		var d map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &d), l)
		require.EqualValues(t, 1, d["schema"])
	}
	require.Contains(t, lines[0], "DEY-X042")
	require.Contains(t, lines[1], `"series"`)

	e.out.Reset()
	n.Store(0)
	e.g.OutTTY = true
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	e.stub.TrafficFn = func(c context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
		if n.Add(1) == 2 {
			cancel2()
		}
		return traffic(c, q)
	}
	e.g.JSON = false
	require.Equal(t, 0, Run(ctx2, e.g, []string{"stats", "--watch"}))
	out := e.out.String()
	require.Contains(t, out, clearScreen)
	require.Contains(t, out, "TUNNEL")
	require.Contains(t, out, "refreshes every 0s")

	e.stub.TrafficFn = func(context.Context, api.TrafficQuery) (api.TrafficReport, error) {
		return api.TrafficReport{}, deyerr.New(deyerr.C027, deyerr.Params{"field": "target", "value": "nope", "allowed": "x"})
	}
	require.Contains(t, e.fail(1, "stats", "nope", "--watch"), "DEY-C027")
}

// A wrong period, target or style is DEY-C027 (exit 1) before the daemon
// is asked.
func TestStatsBadInput(t *testing.T) {
	e, queries := statsEnv(t)
	for _, args := range [][]string{
		{"stats", "--period", "2h"}, {"stats", "Bad_ID"}, {"stats", "node:"}, {"stats", "--style", "dots"},
	} {
		errOut := e.fail(1, args...)
		require.Contains(t, errOut, "DEY-C027", args)
	}
	require.Contains(t, e.fail(1, "stats", "--period", "2h"), "1h, 24h, 7d, 30d")
	require.Zero(t, e.dialed)
	require.Empty(t, queries())
	e.fail(1, "stats", "--period", "2h", "--json")
	require.Contains(t, e.out.String(), `"code": "DEY-C027"`)
	require.Contains(t, e.out.String(), `"exit_code": 1`)
}

// A hub that cannot count bytes shows "—" and DEY-X061, never "0 B".
func TestStatsUnavailable(t *testing.T) {
	e, _ := statsEnv(t)
	reason := api.ToDTO(deyerr.New(deyerr.X061, deyerr.Params{"reason": "nft is not installed"}))
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) {
		ts := statsTunnels()
		for i := range ts {
			ts[i].Traffic = &api.TrafficNow{At: testNow}
		}
		return ts, nil
	}
	e.stub.TrafficFn = func(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
		s := api.TrafficSeries{ID: "main", Kind: api.TrafficKindTunnel, Reason: reason, StepS: 120}
		return api.TrafficReport{Period: q.Period, Reason: reason, Series: []api.TrafficSeries{s}}, nil
	}
	out := e.ok("stats")
	require.Contains(t, out, "│ main   │ —         │ —     │ —       │ —     │ —       │")
	require.Contains(t, out, "DEY-X061")
	require.Contains(t, out, "nft is not installed")
	require.NotContains(t, out, "0 B ")
	out = e.ok("stats", "main")
	require.Contains(t, out, "Download and upload: — (this hub does not count bytes)")
	require.Contains(t, out, "DEY-X061")
}

func TestTunnelShowTrafficSummary(t *testing.T) {
	e, queries := statsEnv(t)
	d := sampleDetail()
	d.Traffic = statsTunnels()[0].Traffic
	e.stub.TunnelShowFn = func(context.Context, string) (api.TunnelDetail, error) { return d, nil }
	out := e.ok("tunnel", "show", "main")
	require.Contains(t, out, "Traffic:     today ↓ 3.8 GiB ↑ 512.0 MiB · 30 days ↓ 410.0 GiB ↑ 40.0 GiB · now ↓ 12.3 Mb/s ↑ 1.20 Mb/s · 12 connections\n")
	require.Equal(t, api.TrafficQuery{Targets: []string{"tunnel:main"}, Period: "1h", MaxPoints: 1}, queries()[0])

	// --json is TunnelDetail only (no second call).
	doc := e.json("tunnel", "show", "main")
	require.Contains(t, doc, "traffic")
	require.Len(t, queries(), 1)

	// ASCII: arrows become v and ^.
	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 80} }
	require.Contains(t, e.ok("tunnel", "show", "main"), "today v 3.8 GiB ^ 512.0 MiB")

	// The totals call failing leaves the 30-day part out.
	e.stub.TrafficFn = func(context.Context, api.TrafficQuery) (api.TrafficReport, error) {
		return api.TrafficReport{}, deyerr.New(deyerr.X008, nil)
	}
	e.g.Caps = func() tui.Caps { return tui.Caps{Unicode: true, Width: 120} }
	require.Contains(t, e.ok("tunnel", "show", "main"), "Traffic:     today ↓ 3.8 GiB ↑ 512.0 MiB · now ↓ 12.3 Mb/s")

	// Not counted: "—" with the reason, never "0 B".
	d.Traffic = &api.TrafficNow{At: testNow}
	reason := api.ToDTO(deyerr.New(deyerr.X061, deyerr.Params{"reason": "nft is not installed"}))
	e.stub.TrafficFn = func(context.Context, api.TrafficQuery) (api.TrafficReport, error) {
		return api.TrafficReport{Reason: reason, Series: []api.TrafficSeries{{ID: "main", Kind: api.TrafficKindTunnel, Reason: reason}}}, nil
	}
	out = e.ok("tunnel", "show", "main")
	require.Contains(t, out, "Traffic:     — (DEY-X061 Traffic accounting is not available) · 12 connections\n")
	require.NotContains(t, out, "0 B")

	// Without monitoring nor metrics there is no traffic line.
	d.Traffic, d.Metrics = nil, nil
	require.NotContains(t, e.ok("tunnel", "show", "main"), "Traffic:")
}

func TestStatusTrafficBlock(t *testing.T) {
	e, queries := statsEnv(t)
	st := sampleStatus()
	copy(st.Tunnels, statsTunnels())
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return st, nil }
	out := e.ok("status")
	require.Regexp(t, `── TRAFFIC ─+ last hour · ↓ download to users · ↑ upload from users\n`, out)
	require.Contains(t, out, "↓ 12.3 Mb/s  ↑ 1.20 Mb/s  today ↓ 3.8 GiB ↑ 512.0 MiB")
	require.Less(t, strings.Index(out, " TRAFFIC "), strings.Index(out, " NODES "))
	require.Equal(t, []api.TrafficQuery{tui.TrafficBlockQuery()}, queries())
	require.Contains(t, e.json("status"), "tunnels") // --json: Status only
	require.Len(t, queries(), 1)

	// The sparklines failing keeps the block with the rates.
	e.stub.TrafficFn = func(context.Context, api.TrafficQuery) (api.TrafficReport, error) {
		return api.TrafficReport{}, deyerr.New(deyerr.X008, nil)
	}
	out = e.ok("status")
	require.Contains(t, out, "↓ 12.3 Mb/s")
	require.NotContains(t, out, "DEY-X008")

	// No traffic numbers: no block and no call.
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return sampleStatus(), nil }
	e.stub.TrafficFn = func(context.Context, api.TrafficQuery) (api.TrafficReport, error) {
		t.Fatal("Traffic called without traffic numbers")
		return api.TrafficReport{}, nil
	}
	require.NotContains(t, e.ok("status"), "TRAFFIC")
	require.Equal(t, "quota", eventWord(state.EvTrafficQuota))
}

func TestHumanBytesIEC(t *testing.T) {
	for n, want := range map[uint64]string{
		0: "0 B", 12: "12 B", 1023: "1023 B", 1024: "1 KiB", 1536: "2 KiB", 1023 << 10: "1023 KiB",
		1 << 20: "1.0 MiB", 3 << 29: "1.5 GiB", 3 << 30: "3.0 GiB", 2 << 40: "2.0 TiB",
	} {
		require.Equal(t, want, humanBytes(n), n)
	}
}
