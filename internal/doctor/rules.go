package doctor

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
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
	RuleTuning         = "R11" // BBR set by the profile but not active / sysctl profile off
	RuleResources      = "R12" // low disk / low memory
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

	// Additions to the minimum contract.
	Certs         []CertExpiry  // certificate expiry (R08)
	PortConflicts []string      // port conflict descriptions (R05), besides DEY-P012 status warnings
	Events        []state.Event // recent events, newest first; Status.Events when nil (R14)
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

// R11: kernel tuning off, or BBR set by the applied profile but not in
// use (info). Skipped when the profile is unknown; a kernel without
// tcp_bbr and tuning.bbr false are not findings (section 12: BBR is then
// skipped with a warning, and applying the profile again cannot change it).
func ruleTuning(f Facts) []api.DoctorFinding {
	switch {
	case f.SysctlProfile == "":
		return nil
	case f.SysctlProfile == "off":
		return []api.DoctorFinding{finding(RuleTuning, SevInfo, i18n.T(i18n.DoctorR11MsgOff), i18n.T(i18n.DoctorR11Fix))}
	case !f.BBRActive && f.BBRAvailable && f.BBRApplied:
		return []api.DoctorFinding{finding(RuleTuning, SevInfo, i18n.T(i18n.DoctorR11MsgBBR, f.SysctlProfile),
			i18n.T(i18n.DoctorR11FixBBR, f.SysctlProfile))}
	}
	return nil
}

// R12: less than 10 % free disk or available memory.
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
