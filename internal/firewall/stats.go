package firewall

import (
	"context"
	stderrors "errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// The traffic accounting table (ARCHITECTURE.md §8.1). It is separate from
// `inet deyroute`, so Apply (which replaces that table on every node, IP,
// join or token change) never resets the counters, and it has no verdict
// at all: every chain has policy accept and no rule accepts, drops,
// rejects, jumps or goes to anything, so it can never block traffic.
//
// Counting needs no conntrack. Bytes towards a tunnel listen port ("in" =
// upload from users) are counted in an input-hook chain keyed by
// `meta l4proto . th dport`; bytes from a listen port ("out" = download to
// users) in an output-hook chain keyed by `meta l4proto . th sport`. Both
// chains run at priority statsPriority, after the filter chains, so a packet
// another firewall drops is not counted, and both skip loopback (the
// canary, local probes and diagnostics run over lo). A kernel-NAT rung
// (WireGuard/AmneziaWG DNAT on the hub) is routed, not delivered locally:
// only when a tunnel has such a rung (StatsTunnel.NAT) a forward-hook chain
// counts the DNATed flows by their original destination port with `ct`
// matches, because the DNAT already needs conntrack. Counts are L3 bytes.
const (
	StatsTableName = "deyroute_stats"
	// StatsTable is "inet deyroute_stats" as written in nft commands.
	StatsTable = TableFamily + " " + StatsTableName
)

// Names inside the accounting table.
const (
	statsMapIn    = "acct_in"
	statsMapOut   = "acct_out"
	statsChainIn  = "count_in"
	statsChainOut = "count_out"
	statsChainNAT = "count_fwd"
	// statsPriority is above every usual filter priority (0, and -10 for
	// `inet deyroute`), so only packets that passed them are counted.
	statsPriority = "300"
	// statsCounterPrefix starts every counter name deyroute owns.
	statsCounterPrefix = "tun_"
)

// statsScriptHeader is the first line of the rendered accounting table.
const statsScriptHeader = "# Managed by deyroute. Traffic accounting only: no accept, drop or reject. Rebuilt when the tunnel listen ports change.\n"

// statsReplacePrefix replaces the accounting table in one transaction (see
// replacePrefix).
const statsReplacePrefix = "table " + StatsTable + " {}\ndelete table " + StatsTable + "\n"

// ErrNoStatsTable is returned (wrapped in DEY-X062) by ReadCounters when
// `table inet deyroute_stats` does not exist: after a reboot, after
// `systemctl restart nftables` (flush ruleset) or before the first
// ApplyStats. The caller rebuilds the table, seeded with its last reading.
var ErrNoStatsTable = stderrors.New("table " + StatsTable + " does not exist")

// Counter is the value of one nft named counter.
type Counter struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// StatsTunnel is one tunnel of the accounting table: its id and the listen
// ports users reach it on.
type StatsTunnel struct {
	// ID is the tunnel id ([a-z0-9-]{2,32}).
	ID string
	// TCP and UDP are the listen ports (1-65535).
	TCP, UDP []int
	// NAT is set when the tunnel has a kernel-NAT rung on the hub
	// (WireGuard/AmneziaWG DNAT): its traffic is forwarded, not delivered
	// locally, and is counted in the forward hook through conntrack. Take
	// it from the configured ladder, not from the active rung, so a
	// failover does not rebuild the table.
	NAT bool
}

// StatsSpec is everything `table inet deyroute_stats` is rendered from.
type StatsSpec struct {
	Tunnels []StatsTunnel
	// Seed are the start values of the counters by counter name (the last
	// ReadCounters result), so a rebuild keeps the totals; missing = 0.
	Seed map[string]Counter
}

// CounterIn and CounterOut name the counters of tunnel id: "tun_<id>_in"
// (bytes to the listen ports, upload from users) and "tun_<id>_out" (bytes
// from them, download to users).
func CounterIn(id string) string { return statsCounterPrefix + id + "_in" }

// CounterOut names the out counter of tunnel id (see CounterIn).
func CounterOut(id string) string { return statsCounterPrefix + id + "_out" }

// TunnelCounters returns tunnel id's in and out counters from a
// ReadCounters result; ok is false when either is missing (the table was
// built without the tunnel, or replaced by something else).
func TunnelCounters(m map[string]Counter, id string) (in, out Counter, ok bool) {
	in, okIn := m[CounterIn(id)]
	out, okOut := m[CounterOut(id)]
	return in, out, okIn && okOut
}

// statsIDRe is a tunnel id (config.ValidID); the counter names built from
// it are valid nft identifiers.
var statsIDRe = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)

// Validate reports every problem that keeps s from rendering exactly, as
// DEY-X061 with the problems as detail; nil when it renders completely. Ids
// must be valid and unique, ports 1-65535, and no port may belong to two
// tunnels on the same protocol (the bytes would be counted for one only).
func (s StatsSpec) Validate() error {
	var probs []string
	add := func(format string, a ...any) { probs = append(probs, fmt.Sprintf(format, a...)) }
	ids := map[string]bool{}
	owner := map[string]string{} // "tcp . 443" → tunnel
	for _, t := range s.Tunnels {
		if !statsIDRe.MatchString(t.ID) {
			add("tunnel id %q invalid", t.ID)
			continue
		}
		if ids[t.ID] {
			add("tunnel %s listed twice", t.ID)
			continue
		}
		ids[t.ID] = true
		for _, pp := range []struct {
			proto string
			ports []int
		}{{ProtoTCP, t.TCP}, {ProtoUDP, t.UDP}} {
			for _, p := range uniqueInts(pp.ports) {
				if !validPort(p) {
					add("tunnel %s: listen port %d/%s out of range", t.ID, p, pp.proto)
					continue
				}
				k := statsKey(pp.proto, p)
				if o, dup := owner[k]; dup {
					add("listen port %d/%s belongs to tunnels %s and %s", p, pp.proto, o, t.ID)
					continue
				}
				owner[k] = t.ID
			}
		}
	}
	if len(probs) == 0 {
		return nil
	}
	detail := strings.Join(probs, "; ")
	return deyerr.Wrap(deyerr.X061, deyerr.Plain("invalid accounting spec: "+detail),
		deyerr.Params{"reason": "the accounting rules deyroute generated for table " + StatsTable + " are invalid, so nft was not run"}).
		WithDetail(detail)
}

func statsKey(proto string, port int) string { return proto + " . " + strconv.Itoa(port) }

func uniqueInts(in []int) []int {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// statsElem is one map element: protocol and port to a tunnel.
type statsElem struct {
	proto string
	port  int
	id    string
}

// normalizeStats drops what Validate reports (bad ids, duplicate ids, bad
// ports, ports already owned on the protocol by a tunnel that sorts
// earlier) and sorts everything, so RenderStats is deterministic.
func normalizeStats(s StatsSpec) (ids []string, elems []statsElem, nat bool) {
	tunnels := slices.Clone(s.Tunnels)
	slices.SortStableFunc(tunnels, func(a, b StatsTunnel) int { return strings.Compare(a.ID, b.ID) })
	owned := map[string]bool{}
	for _, t := range tunnels {
		if !statsIDRe.MatchString(t.ID) || slices.Contains(ids, t.ID) {
			continue
		}
		ids = append(ids, t.ID)
		nat = nat || t.NAT
		for _, pp := range []struct {
			proto string
			ports []int
		}{{ProtoTCP, t.TCP}, {ProtoUDP, t.UDP}} {
			for _, p := range uniqueInts(pp.ports) {
				k := statsKey(pp.proto, p)
				if !validPort(p) || owned[k] {
					continue
				}
				owned[k] = true
				elems = append(elems, statsElem{proto: pp.proto, port: p, id: t.ID})
			}
		}
	}
	slices.SortFunc(elems, func(a, b statsElem) int {
		if a.proto != b.proto {
			return strings.Compare(a.proto, b.proto)
		}
		return a.port - b.port
	})
	return ids, elems, nat
}

// RenderStats returns the complete `table inet deyroute_stats { … }` for s
// as an nft script. The output is deterministic (tunnels, protocols and
// ports sorted; invalid entries dropped, see Validate) and has no verdict.
// A spec without tunnels renders an empty table; callers remove the table
// instead (ApplyStats does).
func RenderStats(s StatsSpec) string {
	ids, elems, nat := normalizeStats(s)
	var blocks []string
	for _, id := range ids {
		for _, name := range []string{CounterIn(id), CounterOut(id)} {
			c := s.Seed[name]
			blocks = append(blocks, fmt.Sprintf("\tcounter %s {\n\t\tpackets %d bytes %d\n\t}\n", name, c.Packets, c.Bytes))
		}
	}
	if len(elems) > 0 {
		blocks = append(blocks,
			renderStatsMap(statsMapIn, elems, CounterIn),
			renderStatsMap(statsMapOut, elems, CounterOut),
			renderChain(statsChainIn, "type filter hook input priority "+statsPriority+"; policy accept;", []string{
				`iif != "lo" counter name meta l4proto . th dport map @` + statsMapIn,
			}),
			renderChain(statsChainOut, "type filter hook output priority "+statsPriority+"; policy accept;", []string{
				`oif != "lo" counter name meta l4proto . th sport map @` + statsMapOut,
			}))
		if nat {
			blocks = append(blocks, renderChain(statsChainNAT, "type filter hook forward priority "+statsPriority+"; policy accept;", []string{
				"ct status dnat ct direction original counter name meta l4proto . ct original proto-dst map @" + statsMapIn,
				"ct status dnat ct direction reply counter name meta l4proto . ct original proto-dst map @" + statsMapOut,
			}))
		}
	}
	var b strings.Builder
	b.WriteString(statsScriptHeader)
	b.WriteString("table " + StatsTable + " {\n")
	b.WriteString(strings.Join(blocks, "\n"))
	b.WriteString("}\n")
	return b.String()
}

func renderStatsMap(name string, elems []statsElem, counter func(string) string) string {
	items := make([]string, len(elems))
	for i, e := range elems {
		items[i] = statsKey(e.proto, e.port) + " : " + quote(counter(e.id))
	}
	return "\tmap " + name + " {\n" +
		"\t\ttype inet_proto . inet_service : counter\n" +
		"\t\telements = { " + strings.Join(items, ", ") + " }\n" +
		"\t}\n"
}

// StatsScript returns the exact script ApplyStats pipes to `nft -f -`.
func StatsScript(s StatsSpec) string { return statsReplacePrefix + RenderStats(s) }

// ApplyStats replaces `table inet deyroute_stats` with RenderStats(s)
// atomically through one `nft -f -` (create, delete and the full table in
// one transaction). A spec without tunnels removes the table instead. An
// invalid spec is DEY-X061 and nothing runs; an nft failure is DEY-X061
// wrapping DEY-X005 (which wraps the runner error, DEY-X030 when nft is
// missing) with nft's stderr as detail.
func ApplyStats(ctx context.Context, r exec.Runner, s StatsSpec) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if len(s.Tunnels) == 0 {
		return RemoveStats(ctx, r)
	}
	ctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	_, stderr, err := r.Run(ctx, "nft", []string{"-f", "-"}, []byte(StatsScript(s)))
	if err != nil {
		return statsError(deyerr.X061, "nft refused table "+StatsTable, err, stderr)
	}
	return nil
}

// RemoveStats deletes `table inet deyroute_stats` and nothing else. It is
// idempotent: a missing table, or a system without nft, is success.
func RemoveStats(ctx context.Context, r exec.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	_, stderr, err := r.Run(ctx, "nft", []string{"delete", "table", TableFamily, StatsTableName}, nil)
	if err == nil || noSuchTable(err, stderr) || deyerr.HasCode(err, deyerr.X030) {
		return nil
	}
	return nftError(err, stderr)
}

// ReadCounters returns every deyroute counter of `table inet
// deyroute_stats` by name (one `nft list counters table inet
// deyroute_stats`). Counters without the "tun_" prefix are ignored. A
// missing table returns an empty map and DEY-X062 wrapping ErrNoStatsTable;
// any other failure, including output that cannot be parsed, is DEY-X062.
func ReadCounters(ctx context.Context, r exec.Runner) (map[string]Counter, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	out, stderr, err := r.Run(ctx, "nft", []string{"list", "counters", "table", TableFamily, StatsTableName}, nil)
	if err != nil {
		if noSuchTable(err, stderr) {
			return map[string]Counter{}, deyerr.Wrap(deyerr.X062, ErrNoStatsTable, deyerr.Params{"reason": ErrNoStatsTable.Error()})
		}
		return nil, statsError(deyerr.X062, "nft list counters failed", err, stderr)
	}
	return ParseCounters(string(out))
}

// ParseCounters parses the text of `nft list counters table inet
// deyroute_stats`:
//
//	table inet deyroute_stats {
//		counter tun_main_in {
//			packets 12 bytes 3456
//		}
//	}
//
// It keeps the counters whose names start with "tun_". Anything it does not
// understand is DEY-X062 naming the line.
func ParseCounters(text string) (map[string]Counter, error) {
	out := map[string]Counter{}
	bad := func(n int, line, why string) error {
		reason := fmt.Sprintf("unexpected nft output on line %d (%s)", n, why)
		return deyerr.New(deyerr.X062, deyerr.Params{"reason": reason}).WithDetail(strings.TrimSpace(line))
	}
	var (
		inTable   bool
		name      string // counter being read; "" = none
		seen      bool   // its packets/bytes line was read
		cur       Counter
		lineNo    int
		tableOpen = "table " + StatsTable + " {"
	)
	for _, raw := range strings.Split(text, "\n") {
		lineNo++
		line := strings.TrimSpace(raw)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case !inTable:
			if line != tableOpen {
				return nil, bad(lineNo, raw, "expected "+tableOpen)
			}
			inTable = true
		case name == "" && line == "}":
			inTable = false
		case name == "":
			f := strings.Fields(line)
			if len(f) != 3 || f[0] != "counter" || f[2] != "{" {
				return nil, bad(lineNo, raw, "expected a counter")
			}
			name, seen, cur = strings.Trim(f[1], `"`), false, Counter{}
		case line == "}":
			if !seen {
				return nil, bad(lineNo, raw, "counter "+name+" has no value")
			}
			if strings.HasPrefix(name, statsCounterPrefix) {
				out[name] = cur
			}
			name = ""
		case strings.HasPrefix(line, "comment "):
		default:
			f := strings.Fields(line)
			if len(f) != 4 || f[0] != "packets" || f[2] != "bytes" {
				return nil, bad(lineNo, raw, "expected packets and bytes")
			}
			p, perr := strconv.ParseUint(f[1], 10, 64)
			b, berr := strconv.ParseUint(f[3], 10, 64)
			if perr != nil || berr != nil {
				return nil, bad(lineNo, raw, "counter values are not numbers")
			}
			cur, seen = Counter{Packets: p, Bytes: b}, true
		}
	}
	if inTable || name != "" {
		return nil, bad(lineNo, "", "the listing ends inside a block")
	}
	return out, nil
}

// StatsSupport reports whether traffic accounting can work on this host:
// nil when `nft list tables` runs, otherwise DEY-X061 with the reason (nft
// is not installed, or the kernel has no usable nf_tables, e.g. in an
// unprivileged container). Monitoring then reports available: false.
func StatsSupport(ctx context.Context, r exec.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	_, stderr, err := r.Run(ctx, cmdNFTTables[0], cmdNFTTables[1:], nil)
	if err != nil {
		return statsError(deyerr.X061, "", err, stderr)
	}
	return nil
}

// StatsAvailable reports whether StatsSupport finds nothing missing.
func StatsAvailable(ctx context.Context, r exec.Runner) bool { return StatsSupport(ctx, r) == nil }

// statsError wraps a failed nft run as code (DEY-X061 or DEY-X062) → DEY-X005
// → cause, with nft's stderr as detail. A missing nft is named as the
// reason; otherwise reason is used ("" = nf_tables is not usable).
func statsError(code deyerr.Code, reason string, err error, stderr []byte) error {
	detail := strings.TrimSpace(string(stderr))
	switch {
	case deyerr.HasCode(err, deyerr.X030):
		reason = "nft is not installed"
	case reason == "":
		reason = "nftables does not work on this host (nft list tables failed)"
	}
	if detail != "" {
		reason += ": " + firstLine(detail)
	}
	x := deyerr.Wrap(deyerr.X005, err, nil)
	if detail != "" {
		x = x.WithDetail(detail)
	}
	e := deyerr.Wrap(code, x, deyerr.Params{"reason": reason})
	if detail != "" {
		e = e.WithDetail(detail)
	}
	return e
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
