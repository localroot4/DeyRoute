package i18n

// Traffic monitoring in the UI: the dashboard's TRAFFIC block, the Traffic
// screen (Diagnostics → Traffic and load, dashboard key t), the tunnel
// detail and `deyroute stats` (tui/traffic.go, tui/traffic_screen.go,
// cli/stats.go). Directions are seen from the users: ↓ download = from the
// hub to the users, ↑ upload = from the users to the hub.
const (
	TUIDgTraffic          Key = "tui.dg.traffic"
	TUIHelpTraffic        Key = "tui.help.traffic"
	TUITrafficTitle       Key = "tui.traffic.title"
	TUITrafficHint        Key = "tui.traffic.hint"
	TUITrafficNone        Key = "tui.traffic.none"
	TUITrafficNow         Key = "tui.traffic.now"
	TUITrafficToday       Key = "tui.traffic.today"
	TUITrafficTodayNone   Key = "tui.traffic.today_none"
	TUITrafficMore        Key = "tui.traffic.more"
	TUITrafficDashKey     Key = "tui.traffic.dash_key"
	TUITrafficNoSamples   Key = "tui.traffic.no_samples"
	TUITrafficNoData      Key = "tui.traffic.no_data"
	TUITrafficPercent     Key = "tui.traffic.percent"
	TUITrafficPercentInt  Key = "tui.traffic.percent_int"
	TUITrafficCPU         Key = "tui.traffic.cpu"
	TUITrafficRAM         Key = "tui.traffic.ram"
	TUITrafficUnavailable Key = "tui.traffic.unavailable"
	TUITrafficDownload    Key = "tui.traffic.download"
	TUITrafficUpload      Key = "tui.traffic.upload"
	TUITrafficConns       Key = "tui.traffic.conns"
	TUITrafficConnsNow    Key = "tui.traffic.conns_now"
	TUITrafficConnsNA     Key = "tui.traffic.conns_na"
	TUITrafficTotals      Key = "tui.traffic.totals"
	TUITrafficPeriod      Key = "tui.traffic.period"
	TUITrafficQuota       Key = "tui.traffic.quota"
	TUITrafficZone        Key = "tui.traffic.zone"
	TUITrafficPick        Key = "tui.traffic.pick"
	TUITrafficKindTunnel  Key = "tui.traffic.kind_tunnel"
	TUITrafficKindNode    Key = "tui.traffic.kind_node"
	TUITrafficKindHub     Key = "tui.traffic.kind_hub"
	TUITrafficThisServer  Key = "tui.traffic.this_server"
	TUITrafficNoBraille   Key = "tui.traffic.no_braille"
	TUITrafficKeys        Key = "tui.traffic.keys"
	TUITrafficUpdated     Key = "tui.traffic.updated"
	TUITrafficHeader      Key = "tui.traffic.header"
	TUITrafficBlocks      Key = "tui.traffic.blocks"
	TUITrafficBraille     Key = "tui.traffic.braille"
	TUITrafficDetTraffic  Key = "tui.traffic.det_traffic"
	TUITrafficDetNone     Key = "tui.traffic.det_none"
	TUITrafficDetNow      Key = "tui.traffic.det_now"
	TUITrafficDetHour     Key = "tui.traffic.det_hour"
	TUITrafficDetDays     Key = "tui.traffic.det_days"
	TUITrafficDet30       Key = "tui.traffic.det_30"
	TUIEvTrafficQuota     Key = "tui.dash.ev_traffic_quota"
	TUIEvTuneDrift        Key = "tui.dash.ev_tune_drift"

	CLIStatsShort       Key = "cli.stats_short"
	CLIStatsLong        Key = "cli.stats_long"
	CLIStatsExample     Key = "cli.stats_example"
	CLIFlagPeriod       Key = "cli.flag_period"
	CLIFlagStyle        Key = "cli.flag_style"
	CLIFlagStatsWatch   Key = "cli.flag_stats_watch"
	CLIStatsColTunnel   Key = "cli.stats_col_tunnel"
	CLIStatsColNow      Key = "cli.stats_col_now"
	CLIStatsColToday    Key = "cli.stats_col_today"
	CLIStatsCol30       Key = "cli.stats_col_30"
	CLIStatsColQuota    Key = "cli.stats_col_quota"
	CLIStatsColTrend    Key = "cli.stats_col_trend"
	CLIStatsRate        Key = "cli.stats_rate"
	CLIStatsLegend      Key = "cli.stats_legend"
	CLIStatsSeriesTitle Key = "cli.stats_series_title"
	CLIStatsNoSeries    Key = "cli.stats_no_series"
	CLIStatsKindTunnel  Key = "cli.stats_kind_tunnel"
	CLIStatsKindNode    Key = "cli.stats_kind_node"
	CLIStatsKindHub     Key = "cli.stats_kind_hub"
	CLITrafficDays30    Key = "cli.traffic_days30"
	CLITrafficNow       Key = "cli.traffic_now"
	CLITrafficConns     Key = "cli.traffic_conns"
)

var trafficEN = map[Key]string{
	TUIDgTraffic: "Traffic and load",
	TUIHelpTraffic: "Charts of one tunnel, node or the hub. A tunnel shows Download (from the hub to the users) and\n" +
		"Upload (from the users to the hub) as separate panels, its connections and its totals; a node or\n" +
		"the hub shows CPU and RAM. 1, 2, 3 and 4 pick the last hour, 24 hours, 7 days or 30 days;\n" +
		"b switches between blocks and braille; r refreshes (the last hour refreshes every 2 seconds).\n" +
		"A gap (no sample) is drawn as · (? without UTF-8), never as zero. 'Today' and the quota period\n" +
		"follow the hub's time zone; the chart times are yours. q or Esc goes back.",
	TUITrafficTitle:       "TRAFFIC",
	TUITrafficHint:        "last hour · ↓ download to users · ↑ upload from users",
	TUITrafficNone:        "—",
	TUITrafficNow:         "↓ %s  ↑ %s",
	TUITrafficToday:       "today ↓ %s ↑ %s",
	TUITrafficTodayNone:   "today %s",
	TUITrafficMore:        "+%d more: t opens the traffic of every tunnel",
	TUITrafficDashKey:     " · t: traffic charts",
	TUITrafficNoSamples:   "No samples in this period yet.",
	TUITrafficNoData:      "No data for this target: it may have been removed.",
	TUITrafficPercent:     "%.0f%%",
	TUITrafficPercentInt:  "%d%%",
	TUITrafficCPU:         "CPU  avg %s · peak %s",
	TUITrafficRAM:         "RAM  peak %s",
	TUITrafficUnavailable: "Download and upload: %s (this hub does not count bytes)",
	TUITrafficDownload:    "Download (to users)  avg %s · peak %s",
	TUITrafficUpload:      "Upload (from users)  avg %s · peak %s",
	TUITrafficConns:       "Connections",
	TUITrafficConnsNow:    "now %d · peak %d",
	TUITrafficConnsNA:     "n/a (UDP has no connection state)",
	TUITrafficTotals:      "Today ↓ %s ↑ %s · 30 days ↓ %s ↑ %s",
	TUITrafficPeriod:      "Since %s (quota period) ↓ %s ↑ %s",
	TUITrafficQuota:       "· quota %d%% of %s",
	TUITrafficZone:        "Days and the quota period follow the hub's zone (%s); chart times are yours.",
	TUITrafficPick:        "Show the traffic of which tunnel, or the load of which server?",
	TUITrafficKindTunnel:  "tunnel",
	TUITrafficKindNode:    "node",
	TUITrafficKindHub:     "hub",
	TUITrafficThisServer:  "this server",
	TUITrafficNoBraille:   "Braille needs a UTF-8 terminal; blocks are kept.",
	TUITrafficKeys:        "1 hour · 2 24 hours · 3 7 days · 4 30 days · b blocks/braille · r refresh · q back",
	TUITrafficUpdated:     "Updated %s · r refreshes",
	TUITrafficHeader:      "Period: %s · chart: %s",
	TUITrafficBlocks:      "blocks",
	TUITrafficBraille:     "braille",
	TUITrafficDetTraffic:  "Traffic",
	TUITrafficDetNone:     "%s (%s %s)",
	TUITrafficDetNow:      "now ↓ %s ↑ %s · today ↓ %s ↑ %s",
	TUITrafficDetHour:     "Last hour",
	TUITrafficDetDays:     "30 days",
	TUITrafficDet30:       "↓ %s ↑ %s",
	TUIEvTrafficQuota:     "quota",
	TUIEvTuneDrift:        "tuning",

	CLIStatsShort: "Traffic of the tunnels and load of the servers (charts in the terminal)",
	CLIStatsLong: "Without a target: one line per tunnel with the current rate, today's and the last 30 days'\n" +
		"volume (download + upload), the quota used and a sparkline of the period.\n" +
		"With targets (a tunnel id, tunnel:<id>, node:<id> or hub): the Download (to users) and\n" +
		"Upload (from users) charts with the connections and totals of a tunnel, or the CPU and RAM\n" +
		"charts of a node or the hub. Bytes are counted on the hub's listen ports; a hub that cannot\n" +
		"count them (no nftables) says so with DEY-X061 instead of showing 0.\n" +
		"--json prints the raw report (docs/cli-json.md); --watch refreshes every 2 seconds.",
	CLIStatsExample: "  deyroute stats\n  deyroute stats main --period 24h\n  deyroute stats node:de-1 hub --period 7d\n" +
		"  deyroute stats main --watch --style braille\n  deyroute stats --period 30d --json | jq '.series[] | {id, totals}'",
	CLIFlagPeriod:       "period: 1h, 24h, 7d or 30d",
	CLIFlagStyle:        "chart style: blocks or braille (braille needs UTF-8)",
	CLIFlagStatsWatch:   "refresh every 2 seconds until Ctrl-C (--json: one document per line)",
	CLIStatsColTunnel:   "TUNNEL",
	CLIStatsColNow:      "NOW ↓ / ↑",
	CLIStatsColToday:    "TODAY",
	CLIStatsCol30:       "30 DAYS",
	CLIStatsColQuota:    "QUOTA",
	CLIStatsColTrend:    "LAST %s",
	CLIStatsRate:        "%s / %s",
	CLIStatsLegend:      "↓ download to users, ↑ upload from users; TODAY and 30 DAYS count both directions.",
	CLIStatsSeriesTitle: "%s · last %s",
	CLIStatsNoSeries:    "No data for these targets.",
	CLIStatsKindTunnel:  "Tunnel",
	CLIStatsKindNode:    "Node",
	CLIStatsKindHub:     "Hub",
	CLITrafficDays30:    "30 days ↓ %s ↑ %s",
	CLITrafficNow:       "now ↓ %s ↑ %s",
	CLITrafficConns:     "%d connections",
}

// traffic — merge the strings above into the English table.
func init() {
	for k, v := range trafficEN {
		en[k] = v
	}
}
