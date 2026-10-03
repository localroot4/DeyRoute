package doctor

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

// Severities of api.DoctorFinding.
const (
	SevOK    = "ok"
	SevInfo  = "info"
	SevWarn  = "warn"
	SevError = "error"
)

// Rule ids (stable; documented in docs and referenced by support).
const (
	RuleControlOffline = "R01" // node offline but tunnel path OK
	RuleIPBlocked      = "R02" // tunnel DOWN, every rung failed/quarantined
	RuleServiceDown    = "R03" // service behind the tunnel down on the node
	RuleFirewallBlocks = "R04" // external firewall blocks a deyroute port
	RulePortConflict   = "R05" // listen port used by another process
	RuleServiceUnit    = "R06" // deyroute-hub / deyroute-node not active
	RuleCrashLoop      = "R07" // tunnel unit restarting again and again
	RuleCertExpiry     = "R08" // certificate expired / expiring within 14 days
	RuleVersion        = "R09" // hub/node major.minor mismatch
	RuleUDPBlocked     = "R10" // UDP rungs skipped
	RuleTuning         = "R11" // kernel tuning: off, BBR not active, drift, boot overrides, nofile, qdisc, UDP buffers, container
	RuleResources      = "R12" // low disk / memory, conntrack and file handles, state.db budget, traffic accounting table
	RuleClockSkew      = "R13" // hub/node clock difference
	RuleFlapping       = "R14" // flapping in the last hour
	RuleSecrets        = "R15" // secret permissions / join tokens (expired but kept, long-lived)
)

// Rule thresholds.
const (
	CrashLoopRestarts = 5
	CertWarnBefore    = 14 * 24 * time.Hour
	LowDiskPct        = 10.0
	LowMemPct         = 10.0
	MaxClockSkew      = 60 * time.Second
	FlapWindow        = time.Hour

	// FillWarnPct and FillErrorPct are the conntrack table fill levels of
	// R12 (warn above 80 %, error above 95 %); FillWarnPct is also the
	// file-handle and state.db budget level.
	FillWarnPct  = 80
	FillErrorPct = 95
	// UDPMinRmem is the net.core.rmem_max below which UDP rungs (QUIC
	// needs about 7 MiB of receive buffer) are limited.
	UDPMinRmem = 7 << 20
	// maxListed bounds the items one finding names.
	maxListed = 3
)

// Facts is everything the rules look at. The daemon fills it: Role and
// Status from its own state, the measurements from Collector.Collect
// (Collection.Apply) and the rest from its stores. Zero values mean
// "not measured" and never produce a finding: MemAvailPct 0, an empty
// SysctlProfile (R11 is skipped), nil maps and slices.
type Facts struct {
	Role     string // hub | node; Status.Role when empty
	Status   api.Status
	Sections map[string]string
	Now      time.Time // time.Now when zero

	DiskFreePct map[string]float64       // mount → percent free
	MemAvailPct float64                  // percent available; 0 = unknown
	ClockSkew   map[string]time.Duration // node id → node clock minus hub clock

	SecretPermProblems []string // one line per DEY-S002 problem
	// ExpiredJoinTokens counts join tokens still stored after their expiry
	// although the hub pruned the file just before (the prune failed; they
	// are never accepted). LongJoinTokens counts the valid tokens created
	// with a TTL over 15 minutes (join-command --ttl) and LongJoinUntil is
	// the latest of their expiries: until then the control port accepts
	// every address (join window).
	ExpiredJoinTokens int
	LongJoinTokens    int
	LongJoinUntil     time.Time

	BBRActive     bool
	SysctlProfile string // off|balanced|aggressive; "" = unknown
	// BBRAvailable reports that the kernel has tcp_bbr and BBRApplied that
	// the applied profile sets it. A profile applied without BBR (a kernel
	// without tcp_bbr, or tuning.bbr false) is a choice, not a problem:
	// R11 reports BBR only when the profile sets it and the kernel, which
	// has it, does not use it.
	BBRAvailable bool
	BBRApplied   bool

	// ExternalFirewallBlocks lists ports an external firewall (ufw,
	// firewalld, iptables) blocks, one entry per port, built with
	// FirewallBlock ("44433/tcp ufw"); free text is shown as it is.
	ExternalFirewallBlocks []string

	UnitRestarts map[string]int    // unit → NRestarts
	UnitStates   map[string]string // unit → ActiveState

	// Tune holds the measurements of the tuning and monitoring checks
	// (R11, R12); Collection.Apply fills it.
	Tune TuneFacts

	// Additions to the minimum contract.
	Certs         []CertExpiry  // certificate expiry (R08)
	PortConflicts []string      // port conflict descriptions (R05), besides DEY-P012 status warnings
	Events        []state.Event // recent events, newest first; Status.Events when nil (R14)
}

// Values of TuneFacts.Stats.
const (
	StatsPresent     = "present"     // table inet deyroute_stats exists and was read
	StatsMissing     = "missing"     // nft works but the table does not exist
	StatsUnavailable = "unavailable" // nft is missing or nf_tables does not work (DEY-X061)
	StatsUnreadable  = "unreadable"  // the counters could not be read (DEY-X062)
)

// TuneFacts are the measurements of the automatic tuning and the traffic
// monitoring checks. Zero values mean "not measured" and give no finding.
type TuneFacts struct {
	// Virt is the container type (internal/sysinfo); "" on a VM or bare
	// metal.
	Virt string
	// Profile is the profile recorded in 99-deyroute.conf (used to name the
	// command that applies it again); "" when unknown.
	Profile string
	// Drift are the keys of 99-deyroute.conf whose live value differs, or
	// that a sysctl file applied after it sets again at boot
	// (OverriddenBy), from sysctl.Check.
	Drift []api.TuneDrift
	// ConntrackCount and ConntrackMax are the entries in use and the size
	// of the conntrack table (0 when nf_conntrack is not loaded).
	ConntrackCount, ConntrackMax int
	// FilesUsed and FilesMax are the allocated file handles and
	// fs.file-max (fs.file-nr).
	FilesUsed, FilesMax uint64
	// NROpen is fs.nr_open and Nofile the open-files limits of the
	// deyroute units: configured (LimitNOFILE of the unit file and its
	// drop-ins) and of the running processes (/proc/<pid>/limits).
	NROpen uint64
	Nofile []NofileLimit
	// NIC is the default-route interface and Qdisc its live root qdisc;
	// QdiscFQ is set when 99-deyroute.conf sets net.core.default_qdisc = fq.
	NIC, Qdisc string
	QdiscFQ    bool
	// UDPRungs is set when Hysteria2 or AmneziaWG rungs run here, and
	// RmemMax is net.core.rmem_max.
	UDPRungs bool
	RmemMax  uint64
	// StateDBBytes is the size of state.db counted against StateDBBudget
	// (state.SizeBudget when 0).
	StateDBBytes  int64
	StateDBBudget int64
	// Monitoring is monitoring.enabled on a hub, MonitoredTunnels the number
	// of enabled tunnels (the accounting table exists only with at least
	// one), Stats the state of table inet deyroute_stats (Stats*) and
	// StatsReason why it is unavailable or unreadable.
	Monitoring       bool
	MonitoredTunnels int
	Stats            string
	StatsReason      string
}

// NofileLimit is the open-files limit of a deyroute unit: configured in
// its unit file (PID 0) or of its running main process.
type NofileLimit struct {
	Unit  string
	PID   int
	Limit uint64
}

// FirewallBlock formats one Facts.ExternalFirewallBlocks entry.
func FirewallBlock(port int, proto string, by firewall.Kind) string {
	return fmt.Sprintf("%d/%s %s", port, proto, by)
}

// Run applies the 15 rules and returns only the problems (an empty result
// means healthy), in rule order R01..R15. Messages and fixes are plain
// English from internal/i18n, passed through the secret filter.
func Run(f Facts) []api.DoctorFinding {
	if f.Now.IsZero() {
		f.Now = time.Now()
	}
	f.Now = f.Now.UTC()
	if f.Role == "" {
		f.Role = f.Status.Role
	}
	var out []api.DoctorFinding
	for _, rule := range []func(Facts) []api.DoctorFinding{
		ruleControlOffline, ruleIPBlocked, ruleServiceDown, ruleFirewallBlocks, rulePortConflict,
		ruleServiceUnit, ruleCrashLoop, ruleCertExpiry, ruleVersion, ruleUDPBlocked,
		ruleTuning, ruleResources, ruleClockSkew, ruleFlapping, ruleSecrets,
	} {
		out = append(out, rule(f)...)
	}
	for i := range out {
		out[i].Message = dlog.Redact(cleanText(out[i].Message))
		out[i].Fix = dlog.Redact(cleanText(out[i].Fix))
	}
	return out
}

func finding(rule, sev string, msg, fix string) api.DoctorFinding {
	return api.DoctorFinding{Rule: rule, Severity: sev, Message: msg, Fix: fix}
}

func isHub(f Facts) bool { return f.Role == "hub" }

func isNode(f Facts) bool { return f.Role == "node" }

// tunnelNode is the node a tunnel runs on (active node, else primary).
func tunnelNode(t api.TunnelInfo) string {
	if t.ActiveNode != "" {
		return t.ActiveNode
	}
	if len(t.Nodes) > 0 {
		return t.Nodes[0]
	}
	return "?"
}

// pathOK reports a tunnel state in which traffic flows.
func pathOK(s string) bool { return s == state.StateUp || s == state.StateDegraded }

// downTunnel reports a tunnel R02 reports: enabled, not paused, DOWN.
func downTunnel(t api.TunnelInfo) bool {
	return t.Enabled && !t.Paused && t.State == state.StateDown
}

// tunnelNodes is every node of a tunnel (primary first), or its active
// node when the list is empty.
func tunnelNodes(t api.TunnelInfo) []string {
	if len(t.Nodes) > 0 {
		return t.Nodes
	}
	return []string{tunnelNode(t)}
}

// R01: a node is offline on the control channel. The spec example: node
// offline + tunnel path OK → the control network has a problem, the tunnel
// is healthy. An offline node without a working tunnel is reported too
// (the node server or its deyroute-node service is down), unless one of its
// tunnels is DOWN: R02 then reports both together.
func ruleControlOffline(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	if isHub(f) {
		for _, n := range f.Status.Nodes {
			if n.Online {
				continue
			}
			var tunnels []string
			coveredByR02 := false
			for _, t := range f.Status.Tunnels {
				if t.Enabled && t.ActiveNode == n.ID && pathOK(t.State) {
					tunnels = append(tunnels, t.ID)
				}
				if downTunnel(t) && slices.Contains(tunnelNodes(t), n.ID) {
					coveredByR02 = true
				}
			}
			switch {
			case len(tunnels) > 0:
				sort.Strings(tunnels)
				out = append(out, finding(RuleControlOffline, SevWarn,
					i18n.T(i18n.DoctorR01Msg, n.ID, strings.Join(tunnels, ", ")),
					i18n.T(i18n.DoctorR01Fix, n.ID)))
			case !coveredByR02:
				last := i18n.T(i18n.DoctorNever)
				if !n.LastHeartbeat.IsZero() {
					last = n.LastHeartbeat.UTC().Format("2006-01-02 15:04 UTC")
				}
				out = append(out, finding(RuleControlOffline, SevWarn,
					i18n.T(i18n.DoctorR01MsgIdle, n.ID, last), i18n.T(i18n.DoctorR01FixIdle, n.ID)))
			}
		}
	}
	if isNode(f) && f.Status.NodeSelf != nil && !f.Status.NodeSelf.Connected {
		tunnels := map[string]bool{}
		for unit, st := range f.UnitStates {
			inst, ok := systemd.InstanceOf(unit)
			if !ok || st != "active" {
				continue
			}
			if in, err := systemd.ParseInstance(inst); err == nil && !in.Canary {
				tunnels[in.Tunnel] = true
			}
		}
		if len(tunnels) > 0 {
			id := f.Status.NodeSelf.ID
			out = append(out, finding(RuleControlOffline, SevWarn,
				i18n.T(i18n.DoctorR01Msg, id, strings.Join(sortedKeys(tunnels), ", ")),
				i18n.T(i18n.DoctorR01Fix, id)))
		}
	}
	return out
}

// R02: tunnel DOWN — the failover engine exhausted every rung on every
// node (failed or quarantined): the node IP is probably blocked.
// When every node of the tunnel is also offline on the control channel the
// node server itself may be down, so the message says both.
func ruleIPBlocked(f Facts) []api.DoctorFinding {
	online := map[string]bool{}
	for _, n := range f.Status.Nodes {
		online[n.ID] = n.Online
	}
	var out []api.DoctorFinding
	for _, t := range f.Status.Tunnels {
		if !downTunnel(t) {
			continue
		}
		ids := tunnelNodes(t)
		nodes := strings.Join(ids, ", ")
		first := tunnelNode(t)
		allOffline := isHub(f)
		for _, id := range ids {
			if on, known := online[id]; !known || on {
				allOffline = false
			}
		}
		if allOffline {
			out = append(out, finding(RuleIPBlocked, SevError,
				i18n.T(i18n.DoctorR02MsgOffline, t.ID, nodes),
				i18n.T(i18n.DoctorR02FixOffline, t.ID)))
			continue
		}
		out = append(out, finding(RuleIPBlocked, SevError,
			i18n.T(i18n.DoctorR02Msg, t.ID, nodes),
			i18n.T(i18n.DoctorR02Fix, first, t.ID)))
	}
	return out
}

// R03: the service behind the tunnel does not answer on the node.
func ruleServiceDown(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	for _, t := range f.Status.Tunnels {
		if !t.Enabled || !t.ServiceDown {
			continue
		}
		target := probeTarget(t.Ports)
		node := tunnelNode(t)
		out = append(out, finding(RuleServiceDown, SevError,
			i18n.T(i18n.DoctorR03Msg, t.ID, node, target),
			i18n.T(i18n.DoctorR03Fix, node, target)))
	}
	return out
}

// probeTarget is the node-side service address of a tunnel: the target of
// its first TCP port map, else of its first port map (default
// 127.0.0.1:<listen>).
func probeTarget(maps []api.PortMapDTO) string {
	if len(maps) == 0 {
		return "?"
	}
	p := maps[0]
	for _, m := range maps {
		if m.Proto == "tcp" {
			p = m
			break
		}
	}
	if p.Target != "" {
		return p.Target
	}
	return "127.0.0.1:" + strconv.Itoa(p.Listen)
}

// R04: an external firewall blocks a deyroute port (hub control port,
// tunnel listen ports).
func ruleFirewallBlocks(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	for _, e := range dedupe(f.ExternalFirewallBlocks) {
		port, proto, kind, ok := parseFirewallBlock(e)
		if !ok {
			out = append(out, finding(RuleFirewallBlocks, SevError,
				i18n.T(i18n.DoctorR04MsgRaw, e), i18n.T(i18n.DoctorR04FixGeneric)))
			continue
		}
		fix := i18n.T(i18n.DoctorR04FixGeneric)
		if cmd := firewall.OpenCommand(kind, port, proto); len(cmd) > 0 {
			fix = i18n.T(i18n.DoctorR04Fix, strings.Join(cmd, "; "))
		}
		out = append(out, finding(RuleFirewallBlocks, SevError,
			i18n.T(i18n.DoctorR04Msg, port, proto, kind), fix))
	}
	return out
}

// parseFirewallBlock parses a FirewallBlock entry.
func parseFirewallBlock(s string) (port int, proto string, kind firewall.Kind, ok bool) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0, "", "", false
	}
	ps, proto, found := strings.Cut(f[0], "/")
	if !found || (proto != "tcp" && proto != "udp") {
		return 0, "", "", false
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", "", false
	}
	switch k := firewall.Kind(f[1]); k {
	case firewall.NFTables, firewall.UFW, firewall.Firewalld, firewall.IPTables:
		return port, proto, k, true
	}
	return 0, "", "", false
}

// R05: listen port used by another process / port conflict warnings.
func rulePortConflict(f Facts) []api.DoctorFinding {
	msgs := append([]string(nil), f.PortConflicts...)
	for _, w := range f.Status.Warnings {
		if w.Code == string(deyerr.P012) || w.Code == string(deyerr.C003) {
			msgs = append(msgs, w.Message)
		}
	}
	var out []api.DoctorFinding
	for _, m := range dedupe(msgs) {
		out = append(out, finding(RulePortConflict, SevWarn, i18n.T(i18n.DoctorR05Msg, m), i18n.T(i18n.DoctorR05Fix)))
	}
	return out
}

// R06: the deyroute service of this server's role is not active.
func ruleServiceUnit(f Facts) []api.DoctorFinding {
	var unit string
	switch {
	case isHub(f):
		unit = systemd.HubUnit
	case isNode(f):
		unit = systemd.NodeUnit
	default:
		return nil
	}
	st, ok := f.UnitStates[unit]
	if !ok || st == "" || st == "active" || st == "reloading" {
		return nil
	}
	return []api.DoctorFinding{finding(RuleServiceUnit, SevError,
		i18n.T(i18n.DoctorR06Msg, unit, st), i18n.T(i18n.DoctorR06Fix, unit, unit))}
}

// R07: a tunnel unit restarted CrashLoopRestarts times or more.
func ruleCrashLoop(f Facts) []api.DoctorFinding {
	units := make([]string, 0, len(f.UnitRestarts))
	for u := range f.UnitRestarts {
		units = append(units, u)
	}
	sort.Strings(units)
	var out []api.DoctorFinding
	for _, u := range units {
		n := f.UnitRestarts[u]
		inst, ok := systemd.InstanceOf(u)
		if !ok || n < CrashLoopRestarts {
			continue
		}
		tunnel := inst
		if in, err := systemd.ParseInstance(inst); err == nil {
			tunnel = in.Tunnel
		}
		out = append(out, finding(RuleCrashLoop, SevWarn,
			i18n.T(i18n.DoctorR07Msg, u, n), i18n.T(i18n.DoctorR07Fix, tunnel, tunnel)))
	}
	return out
}

// R08: certificate expired or expiring within 14 days.
func ruleCertExpiry(f Facts) []api.DoctorFinding {
	certs := append([]CertExpiry(nil), f.Certs...)
	sort.SliceStable(certs, func(i, j int) bool { return certs[i].Name < certs[j].Name })
	var out []api.DoctorFinding
	for _, c := range certs {
		if c.NotAfter.IsZero() {
			continue
		}
		left := c.NotAfter.Sub(f.Now)
		if left > CertWarnBefore {
			continue
		}
		fix := i18n.T(i18n.DoctorR08FixInternal)
		if c.Kind == "tunnel" && c.Tunnel != "" {
			fix = i18n.T(i18n.DoctorR08FixTunnel, c.Tunnel)
		}
		date := c.NotAfter.UTC().Format("2006-01-02")
		if left < 0 {
			out = append(out, finding(RuleCertExpiry, SevError, i18n.T(i18n.DoctorR08MsgExpired, c.Name, date), fix))
			continue
		}
		out = append(out, finding(RuleCertExpiry, SevWarn,
			i18n.T(i18n.DoctorR08MsgSoon, c.Name, int(left/(24*time.Hour)), date), fix))
	}
	return out
}

// R09: hub and node major.minor differ.
func ruleVersion(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	own := f.Status.Version
	if own == "" {
		return nil
	}
	if isHub(f) {
		for _, n := range f.Status.Nodes {
			if n.Version == "" || version.Compatible(own, n.Version) {
				continue
			}
			out = append(out, finding(RuleVersion, SevError,
				i18n.T(i18n.DoctorR09Msg, own, n.ID, n.Version), i18n.T(i18n.DoctorR09Fix)))
		}
	}
	if isNode(f) && f.Status.NodeSelf != nil {
		ns := f.Status.NodeSelf
		if ns.HubVersion != "" && !version.Compatible(ns.HubVersion, own) {
			out = append(out, finding(RuleVersion, SevError,
				i18n.T(i18n.DoctorR09Msg, ns.HubVersion, ns.ID, own), i18n.T(i18n.DoctorR09Fix)))
		}
	}
	return out
}

// R10: UDP blocked between hub and node: UDP rungs are skipped (info).
func ruleUDPBlocked(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	for _, n := range f.Status.Nodes {
		if n.UDPOK != nil && !*n.UDPOK {
			out = append(out, finding(RuleUDPBlocked, SevInfo, i18n.T(i18n.DoctorR10Msg, n.ID), i18n.T(i18n.DoctorR10Fix)))
		}
	}
	return out
}

// R11: kernel tuning (section 12 and the automatic profile):
//
//   - tuning off (info), or BBR set by the applied profile but not in use
//     (info); skipped when the profile is unknown; a kernel without tcp_bbr
//     and tuning.bbr false are not findings (BBR is then skipped with a
//     warning, and applying the profile again cannot change it);
//   - a sysctl file applied after 99-deyroute.conf sets a tuned key again
//     at boot (warn, one finding per file, naming it);
//   - tuned keys whose live value drifted from 99-deyroute.conf (warn);
//   - an open-files limit of a deyroute unit above fs.nr_open (warn);
//   - fq set as the default qdisc while the live root qdisc differs (info:
//     it applies after the next reboot);
//   - UDP rungs while net.core.rmem_max is below 7 MiB (warn);
//   - a container, where kernel tuning is skipped (info).
func ruleTuning(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	switch {
	case f.SysctlProfile == "":
	case f.SysctlProfile == "off":
		out = append(out, finding(RuleTuning, SevInfo, i18n.T(i18n.DoctorR11MsgOff), i18n.T(i18n.DoctorR11Fix)))
	case !f.BBRActive && f.BBRAvailable && f.BBRApplied:
		out = append(out, finding(RuleTuning, SevInfo, i18n.T(i18n.DoctorR11MsgBBR, f.SysctlProfile),
			i18n.T(i18n.DoctorR11FixBBR, f.SysctlProfile)))
	}
	t := f.Tune
	out = append(out, tuneDrift(f)...)
	if t.NROpen > 0 {
		var items []string
		seen := map[string]bool{}
		for _, n := range t.Nofile {
			if n.Limit <= t.NROpen || seen[n.Unit] {
				continue
			}
			seen[n.Unit] = true
			if n.PID > 0 {
				items = append(items, i18n.T(i18n.DoctorTuneNofileItemPID, n.Unit, n.PID, n.Limit))
				continue
			}
			items = append(items, i18n.T(i18n.DoctorTuneNofileItem, n.Unit, n.Limit))
		}
		if len(items) > 0 {
			out = append(out, finding(RuleTuning, SevWarn,
				i18n.T(i18n.DoctorTuneNofileMsg, t.NROpen, listed(items)), i18n.T(i18n.DoctorTuneNofileFix)))
		}
	}
	if t.QdiscFQ && t.NIC != "" && t.Qdisc != "" && !slices.Contains([]string{"fq", "mq", "noqueue"}, t.Qdisc) {
		out = append(out, finding(RuleTuning, SevInfo, i18n.T(i18n.DoctorTuneQdiscMsg, t.NIC, t.Qdisc), i18n.T(i18n.DoctorTuneQdiscFix)))
	}
	if t.UDPRungs && t.RmemMax > 0 && t.RmemMax < UDPMinRmem {
		fix := i18n.T(i18n.DoctorTuneUDPFix)
		if t.Virt != "" {
			fix = i18n.T(i18n.DoctorTuneUDPFixContainer, t.Virt)
		}
		out = append(out, finding(RuleTuning, SevWarn, i18n.T(i18n.DoctorTuneUDPMsg, sizeIEC(t.RmemMax)), fix))
	}
	if t.Virt != "" {
		out = append(out, finding(RuleTuning, SevInfo, i18n.T(i18n.DoctorTuneContainerMsg, t.Virt), i18n.T(i18n.DoctorTuneContainerFix)))
	}
	return out
}

// raiseOnlyKeys are the limit keys deyroute only ever raises: a live value
// above what 99-deyroute.conf says (raised by someone else later) is not
// drift.
var raiseOnlyKeys = map[string]bool{
	sysctl.KeySomaxconn: true, sysctl.KeySynBacklog: true, sysctl.KeyNetdevBacklog: true,
	sysctl.KeyRmemMax: true, sysctl.KeyWmemMax: true, sysctl.KeyTCPRmem: true, sysctl.KeyTCPWmem: true,
	sysctl.KeyRmemDefault: true, sysctl.KeyWmemDefault: true,
	sysctl.KeyFileMax: true, sysctl.KeyNROpen: true, sysctl.KeyConntrackMax: true, sysctl.PathHashsize: true,
}

// raisedAbove reports whether every field of live is at least the field of
// want and one is above it (numbers only).
func raisedAbove(live, want string) bool {
	lf, wf := strings.Fields(live), strings.Fields(want)
	if len(lf) != len(wf) || len(wf) == 0 {
		return false
	}
	above := false
	for i := range wf {
		l, err1 := strconv.ParseUint(lf[i], 10, 64)
		w, err2 := strconv.ParseUint(wf[i], 10, 64)
		if err1 != nil || err2 != nil || l < w {
			return false
		}
		above = above || l > w
	}
	return above
}

// tuneDrift reports the drift of 99-deyroute.conf: one finding per file
// that overrides tuned keys at boot, and one for the keys whose live value
// changed. Keys the kernel does not have (no live value), BBR (R11 reports
// it above) and limit keys raised further by someone else are left out.
func tuneDrift(f Facts) []api.DoctorFinding {
	overrides := map[string][]string{}
	var changed []string
	for _, d := range f.Tune.Drift {
		switch {
		case d.OverriddenBy != "":
			overrides[d.OverriddenBy] = append(overrides[d.OverriddenBy], d.Key)
		case d.Live == "" || d.Key == sysctl.KeyCongestion:
		case raiseOnlyKeys[d.Key] && raisedAbove(d.Live, d.Want):
		default:
			changed = append(changed, i18n.T(i18n.DoctorTuneDriftItem, d.Key, d.Live, d.Want))
		}
	}
	var out []api.DoctorFinding
	for _, file := range sortedKeys(overrides) {
		out = append(out, finding(RuleTuning, SevWarn,
			i18n.T(i18n.DoctorTuneOverrideMsg, file, strings.Join(dedupe(overrides[file]), ", ")),
			i18n.T(i18n.DoctorTuneOverrideFix, file)))
	}
	if len(changed) > 0 {
		cmd := i18n.T(i18n.DoctorTuneFixAuto)
		if p := f.Tune.Profile; p == config.SysctlBalanced || p == config.SysctlAggressive {
			cmd = i18n.T(i18n.DoctorTuneFixProfile, p)
		}
		out = append(out, finding(RuleTuning, SevWarn,
			i18n.T(i18n.DoctorTuneDriftMsg, len(changed), listed(changed)), i18n.T(i18n.DoctorTuneDriftFix, cmd)))
	}
	return out
}

// listed joins items, naming at most maxListed and counting the rest.
func listed(items []string) string {
	if len(items) <= maxListed {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:maxListed], "; ") + "; " + i18n.T(i18n.DoctorTuneListMore, len(items)-maxListed)
}

// sizeIEC formats bytes in IEC units: "512 KiB", "6 MiB", "47.5 MiB".
func sizeIEC(n uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64) + " " + units[i]
}

// sizeIEC64 is sizeIEC for a signed size (negative counts as 0).
func sizeIEC64(n int64) string {
	return sizeIEC(uint64(max(n, 0))) // #nosec G115 -- not negative
}

// R12: less than 10 % free disk or available memory; the conntrack table
// more than 80 % full (error above 95 %); more than 80 % of the file
// handles in use; state.db above 80 % of its budget (error above it); on a
// hub with monitoring on, the traffic accounting table missing (warn),
// unreadable (warn) or impossible on this host (info).
func ruleResources(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	mounts := make([]string, 0, len(f.DiskFreePct))
	for m := range f.DiskFreePct {
		mounts = append(mounts, m)
	}
	sort.Strings(mounts)
	for _, m := range mounts {
		if p := f.DiskFreePct[m]; p < LowDiskPct {
			out = append(out, finding(RuleResources, SevWarn, i18n.T(i18n.DoctorR12MsgDisk, m, p), i18n.T(i18n.DoctorR12FixDisk, m)))
		}
	}
	if f.MemAvailPct > 0 && f.MemAvailPct < LowMemPct {
		out = append(out, finding(RuleResources, SevWarn, i18n.T(i18n.DoctorR12MsgMem, f.MemAvailPct), i18n.T(i18n.DoctorR12FixMem)))
	}
	t := f.Tune
	if t.ConntrackMax > 0 && t.ConntrackCount*100 > t.ConntrackMax*FillWarnPct {
		sev := SevWarn
		if t.ConntrackCount*100 > t.ConntrackMax*FillErrorPct {
			sev = SevError
		}
		out = append(out, finding(RuleResources, sev,
			i18n.T(i18n.DoctorTuneConntrackMsg, t.ConntrackCount*100/t.ConntrackMax, t.ConntrackCount, t.ConntrackMax),
			i18n.T(i18n.DoctorTuneConntrackFix)))
	}
	if t.FilesMax > 0 && t.FilesUsed > t.FilesMax/100*FillWarnPct {
		out = append(out, finding(RuleResources, SevWarn,
			i18n.T(i18n.DoctorTuneFilesMsg, t.FilesUsed*100/t.FilesMax, t.FilesUsed, t.FilesMax), i18n.T(i18n.DoctorTuneFilesFix)))
	}
	if t.StateDBBytes > 0 {
		budget := t.StateDBBudget
		if budget <= 0 {
			budget = state.SizeBudget
		}
		if t.StateDBBytes*100 > budget*FillWarnPct {
			sev := SevWarn
			if t.StateDBBytes > budget {
				sev = SevError
			}
			out = append(out, finding(RuleResources, sev,
				i18n.T(i18n.DoctorTuneStateDBMsg, sizeIEC64(t.StateDBBytes), sizeIEC64(budget), t.StateDBBytes*100/budget),
				i18n.T(i18n.DoctorTuneStateDBFix)))
		}
	}
	if isHub(f) && t.Monitoring {
		switch t.Stats {
		case StatsMissing:
			if t.MonitoredTunnels > 0 {
				out = append(out, finding(RuleResources, SevWarn, i18n.T(i18n.DoctorTuneStatsMissingMsg), i18n.T(i18n.DoctorTuneStatsMissingFix)))
			}
		case StatsUnreadable:
			out = append(out, finding(RuleResources, SevWarn, i18n.T(i18n.DoctorTuneStatsUnreadMsg, t.StatsReason), i18n.T(i18n.DoctorTuneStatsUnreadFix)))
		case StatsUnavailable:
			out = append(out, finding(RuleResources, SevInfo, i18n.T(i18n.DoctorTuneStatsUnavailMsg, t.StatsReason), i18n.T(i18n.DoctorTuneStatsUnavailFix)))
		}
	}
	return out
}

// R13: clock difference between hub and a node above 60 s.
func ruleClockSkew(f Facts) []api.DoctorFinding {
	nodes := make([]string, 0, len(f.ClockSkew))
	for n := range f.ClockSkew {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	var out []api.DoctorFinding
	for _, n := range nodes {
		d := f.ClockSkew[n]
		if d < 0 {
			d = -d
		}
		if d <= MaxClockSkew {
			continue
		}
		out = append(out, finding(RuleClockSkew, SevError, i18n.T(i18n.DoctorR13Msg, n, d.Round(time.Second)), i18n.T(i18n.DoctorR13Fix)))
	}
	return out
}

// R14: flapping events in the last hour.
func ruleFlapping(f Facts) []api.DoctorFinding {
	events := f.Events
	if events == nil {
		events = f.Status.Events
	}
	count := map[string]int{}
	since := f.Now.Add(-FlapWindow)
	for _, e := range events {
		if e.Type == state.EvFlapping && e.At.After(since) && e.Tunnel != "" {
			count[e.Tunnel]++
		}
	}
	var out []api.DoctorFinding
	for _, t := range sortedKeys(count) {
		out = append(out, finding(RuleFlapping, SevWarn, i18n.T(i18n.DoctorR14Msg, t, count[t]), i18n.T(i18n.DoctorR14Fix, t, t)))
	}
	return out
}

// R15: secret permission problems; expired join tokens the hub could not
// remove (warn); valid join tokens made with a TTL over 15 minutes, which
// keep the join window open (info, with their expiry).
func ruleSecrets(f Facts) []api.DoctorFinding {
	var out []api.DoctorFinding
	for _, p := range dedupe(f.SecretPermProblems) {
		out = append(out, finding(RuleSecrets, SevError, i18n.T(i18n.DoctorR15MsgPerm, p), i18n.T(i18n.DoctorR15FixPerm)))
	}
	if f.ExpiredJoinTokens > 0 {
		out = append(out, finding(RuleSecrets, SevWarn, i18n.T(i18n.DoctorR15MsgJoin, f.ExpiredJoinTokens), i18n.T(i18n.DoctorR15FixJoin)))
	}
	if f.LongJoinTokens > 0 && f.LongJoinUntil.After(f.Now) {
		out = append(out, finding(RuleSecrets, SevInfo,
			i18n.T(i18n.DoctorR15MsgJoinLong, f.LongJoinTokens, f.LongJoinUntil.UTC().Format("2006-01-02 15:04 UTC")),
			i18n.T(i18n.DoctorR15FixJoinLong)))
	}
	return out
}

// dedupe returns the non-empty entries of in without repeats, in order.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
