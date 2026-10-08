// Package demo is the made-up deployment the documentation screenshots
// show (make screens): one hub, two nodes, three tunnels, their traffic,
// a tuning plan and a doctor result. Every address is from the ranges
// reserved for documentation (RFC 5737), the names are invented, and every
// value is computed with integer arithmetic from the arguments only, so
// the same call gives the same data on every machine.
package demo

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// Addresses and names of the demo deployment.
const (
	HubName     = "ir-1"
	HubIP       = "203.0.113.10"
	ControlPort = 44433
	Version     = "1.0.0"
	// Zone is the hub's time zone as reports name it.
	Zone = "Asia/Tehran"
)

// zoneOffset is Asia/Tehran's offset (+03:30, no daylight saving time).
const zoneOffset = 3*time.Hour + 30*time.Minute

// JoinLink is the made-up join link (its token is not a real token).
const JoinLink = "dey://EXAMPLE-TOKEN@" + HubIP + ":44433#sha256:3f9a5c"

// Nodes are the two nodes, both online.
func Nodes(now time.Time) []api.NodeInfo {
	return []api.NodeInfo{
		{ID: "de-1", Name: "Frankfurt", PublicIP: "198.51.100.21", Online: true, ControlRTTms: 38, Version: Version,
			Compatible: true, CPUPercent: 4, RAMBytes: 182 << 20, LastHeartbeat: now.Add(-3 * time.Second),
			Tunnels: []string{"main", "panel"}, Fingerprint: "sha256:5b2e"},
		{ID: "nl-1", Name: "Amsterdam", PublicIP: "198.51.100.34", Online: true, ControlRTTms: 45, Version: Version,
			Compatible: true, CPUPercent: 2, RAMBytes: 141 << 20, LastHeartbeat: now.Add(-2 * time.Second),
			Tunnels: []string{"games"}, Fingerprint: "sha256:c41d"},
	}
}

// tunnelDef is one demo tunnel with its traffic profile.
type tunnelDef struct {
	info    api.TunnelInfo
	peakBit uint64 // download rate at the evening peak, bit/s
	udp     bool
}

func tunnels(now time.Time) []tunnelDef {
	tcp := func(ports ...int) []api.PortMapDTO {
		var out []api.PortMapDTO
		for _, p := range ports {
			out = append(out, api.PortMapDTO{Listen: p, Proto: "tcp", Target: "127.0.0.1:" + strconv.Itoa(p)})
		}
		return out
	}
	return []tunnelDef{
		{peakBit: 48_000_000, info: api.TunnelInfo{
			ID: "main", Name: "Xray 443", Enabled: true, State: state.StateUp,
			ActiveNode: "de-1", ActiveNodeName: "Frankfurt", ActiveTransport: "backhaul/wssmux", RTTms: 41,
			UpSince: now.Add(-(3*24*time.Hour + 4*time.Hour + 12*time.Minute)), Ports: tcp(443, 2053),
			Nodes: []string{"de-1", "nl-1"}, LadderName: "default",
			Ladder: []string{"backhaul/wssmux", "backhaul/tcpmux", "rathole/noise", "frp/tcp", "direct/native"},
			Policy: "transport_then_node", ClientIP: "masked",
		}},
		{peakBit: 9_000_000, udp: true, info: api.TunnelInfo{
			ID: "games", Name: "Games UDP", Enabled: true, State: state.StateUp,
			ActiveNode: "nl-1", ActiveNodeName: "Amsterdam", ActiveTransport: "hysteria2/udp", RTTms: 52,
			UpSince: now.Add(-(26*time.Hour + 41*time.Minute)),
			Ports:   []api.PortMapDTO{{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"}},
			Nodes:   []string{"nl-1"}, LadderName: "default-udp",
			Ladder: []string{"backhaul/udp", "hysteria2/udp", "wireguard/kernel", "direct/native"},
			Policy: "transport_then_node", ClientIP: "masked",
		}},
		{peakBit: 3_000_000, info: api.TunnelInfo{
			ID: "panel", Name: "Panel 8443", Enabled: true, State: state.StateUp,
			ActiveNode: "de-1", ActiveNodeName: "Frankfurt", ActiveTransport: "xray/reality", RTTms: 44,
			UpSince: now.Add(-(9*time.Hour + 5*time.Minute)), Ports: tcp(8443),
			Nodes: []string{"de-1"}, LadderName: "default",
			Ladder: []string{"backhaul/wssmux", "xray/reality", "direct/native"},
			Policy: "transport_then_node", ClientIP: "masked",
		}},
	}
}

// Tunnels are the three tunnels, all UP, with their current traffic.
func Tunnels(now time.Time) []api.TunnelInfo {
	var out []api.TunnelInfo
	for _, d := range tunnels(now) {
		t := d.info
		down := rate(d, now, 0, 600)
		today := todayBytes(d, now)
		t.Traffic = &api.TrafficNow{At: now, Available: true, RateOutBitS: down, RateInBitS: down / 9,
			TodayOut: today, TodayIn: today / 9}
		out = append(out, t)
	}
	return out
}

// Status is the hub's status: the banner, the dashboard and doctor read it.
func Status(now time.Time) api.Status {
	return api.Status{
		Schema: 1, Role: "hub", Version: Version, GeneratedAt: now,
		Hub:     &api.HubStatus{Name: HubName, PublicIP: HubIP, ControlPort: ControlPort, UIMode: "simple", Language: "en"},
		Tunnels: Tunnels(now),
		Nodes:   Nodes(now),
		Events: []state.Event{
			{At: now.Add(-(13*time.Minute + 53*time.Second)), Level: "info", Type: state.EvSwitchTransport, Tunnel: "main",
				Message: "backhaul/tcpmux → backhaul/wssmux (failback, primary healthy 5m)"},
			{At: now.Add(-(19*time.Minute + 8*time.Second)), Level: "warn", Type: state.EvSwitchTransport, Tunnel: "main",
				Message: "backhaul/wssmux → backhaul/tcpmux (probe failed 3x)"},
			{At: now.Add(-(9*time.Hour + 5*time.Minute)), Level: "info", Type: state.EvTunnelUp, Tunnel: "panel", Message: "UP via xray/reality (44ms)"},
		},
	}
}

// Traffic answers a Traffic query: each tunnel follows the same daily
// curve (quiet at dawn, busiest in the evening, hub time), scaled to its
// peak, with a small fixed ripple.
func Traffic(now time.Time, q api.TrafficQuery) api.TrafficReport {
	q, err := q.Normalize()
	if err != nil {
		return api.TrafficReport{}
	}
	period, _ := api.TrafficPeriodDuration(q.Period)
	n := q.MaxPoints
	step := int(period / time.Second / time.Duration(n))
	end := now.Truncate(time.Duration(step) * time.Second)
	start := end.Add(-time.Duration(n*step) * time.Second)
	ustep := uint64(step) // #nosec G115 -- a period over at most MaxTrafficPoints points: positive
	rep := api.TrafficReport{GeneratedAt: now, Period: q.Period, Available: true, Timezone: Zone}
	defs := tunnels(now)
	want := map[string]bool{}
	for _, t := range q.Targets {
		want[t] = true
	}
	for _, d := range defs {
		if len(want) > 0 && !want[api.TrafficTarget(api.TrafficKindTunnel, d.info.ID)] {
			continue
		}
		s := api.TrafficSeries{ID: d.info.ID, Kind: api.TrafficKindTunnel, Name: d.info.Name, Available: true, StepS: step}
		for i := range n {
			at := start.Add(time.Duration(i*step) * time.Second)
			down := rate(d, at, i, step)
			p := api.TrafficPoint{At: at, BytesOut: down / 8 * ustep, BytesIn: down / 72 * ustep}
			if !d.udp {
				c := int(20 + load(at)*d.peakBit/48_000_000) // #nosec G115 -- at most 120
				p.Conns = &c
			}
			s.Points = append(s.Points, p)
		}
		today := todayBytes(d, now)
		month := d.peakBit / 8 * 3600 * 24 * 12 / 100 // about 12 hours a day at the peak
		s.Totals = &api.TrafficTotals{
			TodayStart: localMidnight(now),
			TodayIn:    today / 9, TodayOut: today,
			Days30In: month / 9, Days30Out: month,
			PeriodStart: time.Date(2026, 8, 31, 20, 30, 0, 0, time.UTC),
			PeriodIn:    month / 9, PeriodOut: month,
		}
		if !d.udp {
			s.Totals.QuotaBytes = 2 << 40
		}
		rep.Series = append(rep.Series, s)
	}
	return rep
}

// daily is the load in percent of the peak for each hour of the hub's day.
var daily = [24]uint64{38, 26, 18, 12, 9, 8, 10, 16, 24, 30, 34, 38, 42, 44, 46, 50, 56, 64, 74, 86, 96, 100, 88, 62}

// load is the load in percent at t: the hourly curve, linearly interpolated.
func load(t time.Time) uint64 {
	l := t.UTC().Add(zoneOffset)
	h, m := l.Hour(), uint64(l.Minute()) // #nosec G115 -- 0-59
	a, b := daily[h], daily[(h+1)%24]
	return (a*(60-m) + b*m) / 60
}

// rate is the download rate of tunnel d at t (bit/s); i varies the
// ripple. Hours vary little (94-106 %); minutes are bursty (25-130 %), as
// real user traffic is, so the one-hour sparklines show their shape.
func rate(d tunnelDef, t time.Time, i, step int) uint64 {
	mbit := int(d.peakBit / 1_000_000)      // #nosec G115 -- a few dozen
	ripple := uint64(94 + (i*7919+mbit)%13) // #nosec G115 -- 94-106
	if step < 600 {
		ripple = uint64(25 + (i*7919+mbit*31)%106) // #nosec G115 -- 25-130
	}
	return d.peakBit / 100 * load(t) / 100 * ripple
}

// localMidnight is the start of the hub's day containing now.
func localMidnight(now time.Time) time.Time {
	l := now.UTC().Add(zoneOffset)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC).Add(-zoneOffset)
}

// todayBytes is the download volume of d since the hub's midnight.
func todayBytes(d tunnelDef, now time.Time) uint64 {
	var sum uint64
	for t := localMidnight(now); t.Before(now); t = t.Add(10 * time.Minute) {
		sum += rate(d, t, 0, 600) / 8 * 600
	}
	return sum
}

// AddSteps are the progress steps of adding a tunnel, as the hub reports
// them (titles from internal/i18n).
func AddSteps() []api.Step {
	step := func(id, detail string) api.Step {
		return api.Step{ID: id, Title: i18n.T(i18n.Key("hub.step." + id)), Status: api.StepOK, Detail: detail}
	}
	return []api.Step{
		step("install_hub", "backhaul"),
		step("install_node", "de-1"),
		step("render", ""),
		step("firewall", "443/tcp, 2053/tcp"),
		step("start", "backhaul/wssmux on de-1"),
		step("probe", "41ms"),
	}
}

// AddedTunnel is the tunnel AddSteps creates.
func AddedTunnel(now time.Time) api.TunnelInfo { return Tunnels(now)[0] }

// hubLive and nodeLive are the kernel values of a fresh Ubuntu server.
var hubLive = map[string]string{
	"net.core.default_qdisc":             "fq_codel",
	"net.ipv4.tcp_congestion_control":    "cubic",
	"net.core.somaxconn":                 "4096",
	"net.ipv4.tcp_max_syn_backlog":       "512",
	"net.core.netdev_max_backlog":        "1000",
	"net.ipv4.ip_local_port_range":       "32768 60999",
	"net.ipv4.tcp_fin_timeout":           "60",
	"net.ipv4.tcp_tw_reuse":              "2",
	"net.ipv4.tcp_keepalive_time":        "7200",
	"net.ipv4.tcp_keepalive_intvl":       "75",
	"net.ipv4.tcp_keepalive_probes":      "9",
	"net.ipv4.tcp_fastopen":              "1",
	"net.ipv4.tcp_mtu_probing":           "0",
	"net.core.rmem_max":                  "212992",
	"net.core.wmem_max":                  "212992",
	"net.ipv4.tcp_rmem":                  "4096 131072 6291456",
	"net.ipv4.tcp_wmem":                  "4096 16384 4194304",
	"net.ipv4.udp_rmem_min":              "4096",
	"net.ipv4.udp_wmem_min":              "4096",
	"fs.file-max":                        "9223372036854775807",
	"net.ipv4.tcp_notsent_lowat":         "4294967295",
	"net.ipv4.tcp_slow_start_after_idle": "1",
	"net.core.rmem_default":              "212992",
	"net.core.wmem_default":              "212992",
	"fs.nr_open":                         "1048576",
	"net.ipv4.ip_local_reserved_ports":   "",
}

// hostPlan is the automatic plan of one demo host. Every key but those in
// keep already has its planned value, so the plan lists a few changes, as
// on a server that was partly tuned by hand.
func hostPlan(host, role string, f sysinfo.Facts, in sysctl.AutoInputs, keep ...string) api.TuneHost {
	live := map[string]string{}
	for k, v := range hubLive {
		live[k] = v
	}
	in.Live = func(k string) (string, bool) {
		v, ok := live[k]
		return v, ok
	}
	for _, c := range sysctl.AutoPlan(f, in).Changes {
		if !slices.Contains(keep, c.Key) {
			live[c.Key] = c.To
		}
	}
	p := sysctl.AutoPlan(f, in)
	facts := api.TuneFacts(f)
	return api.TuneHost{Host: host, Role: role, Facts: &facts, Changes: p.APIChanges(), Skips: p.APISkips(), Hash: p.Hash}
}

// TunePlan is `deyroute optimize auto --dry-run` on the demo deployment:
// the hub (2 GiB) and de-1 (1 GiB) computed by the real planner from fixed
// facts, nl-1 offline (it applies the plan when it reconnects).
func TunePlan() api.TunePlanReport {
	hub := hostPlan("hub", "hub", sysinfo.Facts{MemBytes: 2 << 30, CPUs: 2, Kernel: "6.8.0-45-generic", BBRAvailable: true,
		FQAvailable: true, Qdisc: "fq", NIC: "eth0", NICMTU: 1500},
		sysctl.AutoInputs{BBR: true, UDPRungs: true, Reserved: []string{"30000-31999", strconv.Itoa(ControlPort)}},
		"net.ipv4.tcp_congestion_control", "net.core.rmem_max", "net.core.wmem_max", "net.ipv4.tcp_slow_start_after_idle")
	de := hostPlan("de-1", "node", sysinfo.Facts{MemBytes: 1 << 30, CPUs: 1, Kernel: "6.1.0-25-amd64", BBRAvailable: true,
		FQAvailable: true, Qdisc: "fq", NIC: "ens3", NICMTU: 1500},
		sysctl.AutoInputs{BBR: true, Reserved: []string{"30000-31999"}},
		"net.core.rmem_max", "net.core.wmem_max")
	hosts := []api.TuneHost{hub, de, {Host: "nl-1", Role: "node", Pending: true, Changes: []api.TuneChange{}}}
	return api.TunePlanReport{Hash: (hub.Hash + de.Hash)[:12], Hosts: hosts,
		Warnings: []string{i18n.T(i18n.TuneWarnNodeOffline, "nl-1")}}
}

// DoctorFindings are the findings of `deyroute doctor` on the demo hub
// (messages from internal/i18n, as the rules write them).
func DoctorFindings() []api.DoctorFinding {
	return []api.DoctorFinding{
		{Rule: "R08", Severity: "warn", Message: i18n.T(i18n.DoctorR08MsgSoon, "of tunnel panel", 12, "2026-10-12"),
			Fix: i18n.T(i18n.DoctorR08FixTunnel, "panel")},
		{Rule: "R10", Severity: "info", Message: i18n.T(i18n.DoctorR10Msg, "de-1"), Fix: i18n.T(i18n.DoctorR10Fix)},
	}
}

// OptimizeStatus is `deyroute optimize status` on the demo hub after
// `optimize auto`: what the plan of TunePlan set on the hub, BBR active,
// de-1 tuned and nl-1 still to apply it.
func OptimizeStatus() api.OptimizeStatus {
	plan := TunePlan()
	hub := plan.Hosts[0]
	applied := map[string]string{}
	for _, c := range hub.Changes {
		if !strings.Contains(c.Key, " ") && !strings.HasPrefix(c.Key, "/") {
			applied[c.Key] = c.To
		}
	}
	return api.OptimizeStatus{
		Profile: "auto", BBRAvailable: true, BBRActive: true, Applied: applied, Facts: hub.Facts,
		Nodes: []api.NodeTuneStatus{
			{Node: "de-1", Online: true, Profile: "auto", AutoCapable: true},
			{Node: "nl-1", Online: false, Profile: "auto", Pending: true, AutoCapable: true},
		},
	}
}
