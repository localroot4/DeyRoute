package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/tui"
)

// Chart styles of `deyroute stats --style`.
const (
	statsStyleBlocks  = "blocks"
	statsStyleBraille = "braille"
)

// Layout of the `deyroute stats` table.
const (
	statsTrendWide   = 30 // sparkline columns from 100 columns on
	statsTrendNarrow = 8  // below 100 columns: the boxed table still fits 80
	statsChartRows   = 6
)

// statsReq is one checked `deyroute stats` command line.
type statsReq struct {
	q     api.TrafficQuery
	table bool // no target: the table of every tunnel
	style tui.ChartStyle
}

func newStatsCmd(g *Globals) *cobra.Command {
	var period, style string
	var watch bool
	cmd := &cobra.Command{
		Use:     "stats [<tunnel>|tunnel:<id>|node:<id>|hub]...",
		Short:   i18n.T(i18n.CLIStatsShort),
		Long:    i18n.T(i18n.CLIStatsLong),
		Example: i18n.T(i18n.CLIStatsExample),
		RunE: func(cmd *cobra.Command, args []string) error {
			req, err := g.statsRequest(args, period, style)
			if err != nil {
				return err
			}
			l, err := g.local()
			if err != nil {
				return err
			}
			if watch {
				return g.watchStats(cmd.Context(), l, req)
			}
			ctx, cancel := callCtx(cmd.Context())
			defer cancel()
			d, err := g.statsFetch(ctx, l, req)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(d.rep)
			}
			g.printf("%s", g.statsText(d, req))
			return nil
		},
	}
	cmd.Flags().StringVar(&period, "period", api.TrafficPeriod1h, i18n.T(i18n.CLIFlagPeriod))
	cmd.Flags().StringVar(&style, "style", statsStyleBlocks, i18n.T(i18n.CLIFlagStyle))
	cmd.Flags().BoolVar(&watch, "watch", false, i18n.T(i18n.CLIFlagStatsWatch))
	return cmd
}

// statsRequest checks the targets, the period and the style before the
// daemon is asked: a wrong one is DEY-C027 (exit 1).
func (g *Globals) statsRequest(args []string, period, style string) (statsReq, error) {
	q, err := api.TrafficQuery{Targets: args, Period: period}.Normalize()
	if err != nil {
		return statsReq{}, err
	}
	req := statsReq{q: q, table: len(q.Targets) == 0}
	switch style {
	case statsStyleBlocks:
	case statsStyleBraille:
		req.style = tui.ChartBraille
	default:
		return statsReq{}, deyerr.New(deyerr.C027, deyerr.Params{
			"field": "style", "value": style, "allowed": statsStyleBlocks + ", " + statsStyleBraille,
		})
	}
	switch {
	case g.JSON:
		q.MaxPoints = api.DefaultTrafficPoints // the documented raw report
	case req.table:
		q.MaxPoints = g.statsTrendWidth()
	default:
		n := g.statsWidth() - 12 // about one point per chart column
		if req.style == tui.ChartBraille && g.unicode() {
			n *= 2
		}
		q.MaxPoints = min(max(n, tui.TrafficSparkPoints), api.MaxTrafficPoints)
	}
	req.q = q
	return req, nil
}

// statsWidth is the width of the charts: the terminal's, 80 when unknown.
func (g *Globals) statsWidth() int {
	if w := g.caps().Width; w > 0 {
		return w
	}
	return 80
}

// statsTrendWidth is the sparkline width of the table.
func (g *Globals) statsTrendWidth() int {
	if g.narrow() {
		return statsTrendNarrow
	}
	return statsTrendWide
}

// statsData is one answer: the report and, for the table, the tunnels
// (their current rates; nil when the list failed).
type statsData struct {
	rep     api.TrafficReport
	tunnels []api.TunnelInfo
}

// statsFetch asks the daemon for the report and, for the table, the
// tunnel list (whose failure only leaves the NOW column empty).
func (g *Globals) statsFetch(ctx context.Context, l api.Local, req statsReq) (statsData, error) {
	rep, err := l.Traffic(ctx, req.q)
	if err != nil {
		return statsData{}, err
	}
	d := statsData{rep: rep}
	if req.table && !g.JSON {
		if ts, err := l.TunnelList(ctx); err == nil {
			d.tunnels = ts
		}
	}
	return d, nil
}

// watchStats prints the statistics every WatchInterval until ctx ends
// (Ctrl-C); with --json one compact document per refresh. A failed refresh
// is shown and retried, except a wrong target (a user error), which ends
// the command.
func (g *Globals) watchStats(ctx context.Context, l api.Local, req statsReq) error {
	t := time.NewTicker(g.WatchInterval)
	defer t.Stop()
	for {
		cctx, cancel := callCtx(ctx)
		d, err := g.statsFetch(cctx, l, req)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && deyerr.As(err).ExitCode() == deyerr.ExitUser {
			return err
		}
		switch {
		case g.JSON && err != nil:
			_ = g.emitJSONLine(map[string]any{"error": api.ToDTO(err)})
		case g.JSON:
			_ = g.emitJSONLine(d.rep)
		default:
			if g.OutTTY && !g.caps().Dumb {
				g.printf("%s", clearScreen)
			}
			if err != nil {
				for _, e := range deyErrors(err) {
					g.printf("%s", g.text(e.Format(g.unicode())))
				}
			} else {
				g.printf("%s", g.statsText(d, req))
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

// statsText renders the human output: the table of every tunnel, or the
// panels of each target.
func (g *Globals) statsText(d statsData, req statsReq) string {
	var b strings.Builder
	if req.table {
		b.WriteString(g.statsTable(d))
	} else {
		c := g.outCaps()
		view := tui.TrafficView{Loc: time.Local, Style: req.style, Rows: statsChartRows, Width: g.statsWidth()}
		for i, s := range d.rep.Series {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(g.text(" "+g.seriesTitle(s, d.rep.Period)) + "\n")
			for _, l := range tui.TrafficPanels(c, d.rep, s, view) {
				b.WriteString(g.text(l) + "\n")
			}
		}
		if len(d.rep.Series) == 0 {
			b.WriteString("  " + i18n.T(i18n.CLIStatsNoSeries) + "\n")
		}
	}
	return b.String()
}

// seriesTitle is "Tunnel main (Main) · last 24h".
func (g *Globals) seriesTitle(s api.TrafficSeries, period string) string {
	kind := i18n.T(i18n.CLIStatsKindTunnel)
	switch s.Kind {
	case api.TrafficKindNode:
		kind = i18n.T(i18n.CLIStatsKindNode)
	case api.TrafficKindHub:
		kind = i18n.T(i18n.CLIStatsKindHub)
	}
	who := s.ID
	if s.Kind == api.TrafficKindHub {
		who = ""
	}
	if s.Name != "" && s.Name != s.ID {
		who = strings.TrimSpace(who + " (" + clean(s.Name) + ")")
	}
	return i18n.T(i18n.CLIStatsSeriesTitle, strings.TrimSpace(kind+" "+clean(who)), period)
}

// statsTable is the table of every tunnel: TUNNEL, NOW ↓/↑, TODAY and 30
// DAYS (download + upload), QUOTA and the sparkline of the period. A hub
// that cannot count bytes shows "—" and the reason (DEY-X061) under the
// table.
func (g *Globals) statsTable(d statsData) string {
	if len(d.rep.Series) == 0 {
		return "  " + i18n.T(i18n.CLIStatusNoTunnels) + "\n"
	}
	c := g.outCaps()
	now := map[string]*api.TrafficNow{}
	for _, t := range d.tunnels {
		now[t.ID] = t.Traffic
	}
	dash := i18n.T(i18n.TUITrafficNone)
	header := []string{i18n.T(i18n.CLIStatsColTunnel), i18n.T(i18n.CLIStatsColNow), i18n.T(i18n.CLIStatsColToday),
		i18n.T(i18n.CLIStatsCol30), i18n.T(i18n.CLIStatsColQuota), i18n.T(i18n.CLIStatsColTrend, d.rep.Period)}
	rows := make([][]string, 0, len(d.rep.Series))
	for _, s := range d.rep.Series {
		if s.Kind != api.TrafficKindTunnel {
			continue
		}
		rate, today, days := dash, dash, dash
		if t := now[s.ID]; t != nil && t.Available {
			rate = i18n.T(i18n.CLIStatsRate, tui.FormatRate(float64(t.RateOutBitS)), tui.FormatRate(float64(t.RateInBitS)))
		}
		if tot := s.Totals; tot != nil && d.rep.Available && s.Available {
			today = tui.FormatBytes(tot.TodayIn + tot.TodayOut)
			days = tui.FormatBytes(tot.Days30In + tot.Days30Out)
		}
		rows = append(rows, []string{s.ID, rate, today, days, tui.QuotaText(d.rep, s), tui.TrafficSpark(c, d.rep, s, g.statsTrendWidth())})
	}
	var b strings.Builder
	out := g.Out
	g.Out = &b
	g.table(header, rows)
	g.Out = out
	for _, l := range wrapText(i18n.T(i18n.CLIStatsLegend), g.lineWidth()-2) {
		b.WriteString(g.text("  "+l) + "\n")
	}
	reason := d.rep.Reason
	if reason == nil {
		for _, s := range d.rep.Series {
			if s.Reason != nil {
				reason = s.Reason
				break
			}
		}
	}
	if reason != nil {
		b.WriteString("\n" + g.text(reason.Err().Format(g.unicode())))
	}
	return b.String()
}
