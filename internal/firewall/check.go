package firewall

import (
	"context"
	"slices"
	"strconv"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// Commands the detection runs (section 10 order).
var (
	cmdNFTTables       = []string{"nft", "list", "tables"}
	cmdNFTRuleset      = []string{"nft", "list", "ruleset"}
	cmdUFWStatus       = []string{"ufw", "status"}
	cmdUFWVerbose      = []string{"ufw", "status", "verbose"}
	cmdFirewalldState  = []string{"firewall-cmd", "--state"}
	cmdFirewalldPorts  = []string{"firewall-cmd", "--list-ports"}
	cmdFirewalldSvcs   = []string{"firewall-cmd", "--list-services"}
	cmdFirewalldAll    = []string{"firewall-cmd", "--list-all"}
	cmdIPTablesInput   = []string{"iptables", "-S", "INPUT"}
	cmdIPTablesRuleset = []string{"iptables", "-S"}
)

// run executes argv with a timeout and reports stdout and success. Missing
// programs and failures are "not present" for detection purposes.
func run(ctx context.Context, r exec.Runner, argv []string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	out, _, err := r.Run(ctx, argv[0], argv[1:], nil)
	if err != nil {
		return string(out), false
	}
	return string(out), true
}

// Detect returns the firewalls present on this server in section 10 order:
// nftables when `nft list tables` works, ufw when `ufw status` reports
// "Status: active", firewalld when `firewall-cmd --state` reports
// "running", and raw iptables when `iptables -S INPUT` shows rules or a
// non-ACCEPT policy. iptables is not reported separately while ufw or
// firewalld is active, because then the iptables rules are theirs.
func Detect(ctx context.Context, r exec.Runner) []Kind {
	var kinds []Kind
	if _, ok := run(ctx, r, cmdNFTTables); ok {
		kinds = append(kinds, NFTables)
	}
	frontEnd := false
	if out, ok := run(ctx, r, cmdUFWStatus); ok && parseUFW(out).active {
		kinds = append(kinds, UFW)
		frontEnd = true
	}
	if out, ok := run(ctx, r, cmdFirewalldState); ok && strings.TrimSpace(out) == "running" {
		kinds = append(kinds, Firewalld)
		frontEnd = true
	}
	if !frontEnd {
		if out, ok := run(ctx, r, cmdIPTablesInput); ok && iptablesActive(out) {
			kinds = append(kinds, IPTables)
		}
	}
	return kinds
}

// iptablesActive reports whether `iptables -S INPUT` shows a rule or a
// policy other than ACCEPT.
func iptablesActive(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) >= 2 && f[0] == "-A":
			return true
		case len(f) >= 3 && f[0] == "-P" && f[2] != "ACCEPT":
			return true
		}
	}
	return false
}

// Verdict is the result of Check for one port.
type Verdict struct {
	// Blocked is true when an external firewall certainly drops new
	// connections to the port.
	Blocked bool `json:"blocked"`
	// By names the blocking firewall.
	By Kind `json:"by,omitempty"`
	// Table and Chain name the blocking nftables base chain (By=nftables).
	Table string `json:"table,omitempty"`
	Chain string `json:"chain,omitempty"`
	// Commands are the exact suggestion lines that open the port; they run
	// only after the owner confirms (Verdict.Open).
	Commands []string `json:"commands,omitempty"`
	// Uncertain is true when a rule could not be interpreted; the port is
	// then reported open (section 10: conservative) and Detail says why.
	Uncertain bool `json:"uncertain,omitempty"`
	// Detail is technical context for logs and doctor: the rule or policy
	// that blocks, or the rule that was not understood.
	Detail string `json:"detail,omitempty"`

	argv [][]string
}

// Err returns nil when the port is not blocked, else DEY-P013 with the
// suggested command in its Fix line.
func (v Verdict) Err(port int, proto string) error {
	if !v.Blocked {
		return nil
	}
	e := deyerr.New(deyerr.P013, deyerr.Params{
		"port": strconv.Itoa(port) + "/" + proto, "firewall": string(v.By),
		"command": strings.Join(v.Commands, " ; "),
	})
	if v.Detail != "" {
		e = e.WithDetail(v.Detail)
	}
	return e
}

// Open runs the verdict's commands (only after the owner confirmed).
func (v Verdict) Open(ctx context.Context, r exec.Runner) error {
	if !v.Blocked {
		return nil
	}
	return runArgv(ctx, r, v.By, v.argv)
}

// Blocks reports whether an external firewall blocks new connections to
// port/proto and which one (see Check).
func Blocks(ctx context.Context, r exec.Runner, port int, proto string) (blocked bool, by Kind, err error) {
	v, err := Check(ctx, r, port, proto)
	return v.Blocked, v.By, err
}

// Check inspects the external firewalls in order — ufw, firewalld, raw
// iptables (only when neither front end is active) and every other
// nftables table with an input filter chain — and reports the first one
// that certainly drops a new IPv4 connection to port/proto. `inet deyroute`
// itself is never considered. Firewalls that are absent or whose tools fail
// are skipped. The heuristics are conservative: a rule that is not
// understood never produces "blocked"; it sets Uncertain instead.
//
//   - ufw (`ufw status verbose`): rules are evaluated in order (first
//     match wins) for source "Anywhere"; no match means the default
//     incoming policy (deny unless "allow (incoming)").
//   - firewalld (`--list-ports`, `--list-services` of the default zone,
//     with https/http/ssh known): blocked when not listed, unless
//     `--list-all` shows a zone target ACCEPT, the whole protocol or a rich
//     rule opening it, or `--info-service` shows another listed service
//     containing it (a rich rule that may open it makes it uncertain).
//   - iptables (`iptables -S`): INPUT with jumps into user chains, then
//     its policy.
//   - nftables (`nft list ruleset`): every input-hook filter chain of the
//     ip and inet families, each evaluated independently.
//
// An invalid port or protocol returns DEY-P010; a cancelled context
// DEY-X031.
func Check(ctx context.Context, r exec.Runner, port int, proto string) (Verdict, error) {
	if !validPort(port) || (proto != ProtoTCP && proto != ProtoUDP) {
		return Verdict{}, deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(port) + "/" + proto})
	}
	var res Verdict
	uncertain := func(detail string) {
		res.Uncertain = true
		if res.Detail == "" {
			res.Detail = detail
		}
	}
	blocked := func(k Kind, detail string) Verdict {
		return Verdict{Blocked: true, By: k, Detail: detail,
			Commands: OpenCommand(k, port, proto), argv: openArgv(k, port, proto)}
	}
	cancelled := func() error {
		if err := ctx.Err(); err != nil {
			return deyerr.Wrap(deyerr.X031, err, deyerr.Params{"command": "firewall check"})
		}
		return nil
	}
	skip := map[string]bool{TableRef: true}
	frontEnd := false

	// 1. ufw
	if out, ok := run(ctx, r, cmdUFWVerbose); ok {
		if st := parseUFW(out); st.active {
			frontEnd = true
			skip["ip filter"], skip["ip6 filter"] = true, true
			b, u, detail := st.ruleset(port, proto).blocks("ufw")
			if b {
				return blocked(UFW, detail), nil
			}
			if u {
				uncertain(detail)
			}
		}
	}
	if err := cancelled(); err != nil {
		return Verdict{}, err
	}

	// 2. firewalld
	if out, ok := run(ctx, r, cmdFirewalldState); ok && strings.TrimSpace(out) == "running" {
		frontEnd = true
		skip["inet firewalld"], skip["ip firewalld"], skip["ip6 firewalld"] = true, true, true
		portsOut, ok1 := run(ctx, r, cmdFirewalldPorts)
		svcOut, ok2 := run(ctx, r, cmdFirewalldSvcs)
		switch {
		case !ok1 || !ok2:
			uncertain("firewalld: could not list open ports/services")
		case !firewalldOpen(portsOut, svcOut, port, proto):
			open, unsure := firewalldRefine(ctx, r, svcOut, port, proto)
			if err := cancelled(); err != nil {
				return Verdict{}, err
			}
			switch {
			case open:
			case unsure != "":
				uncertain(unsure)
			default:
				return blocked(Firewalld, "firewalld: "+strconv.Itoa(port)+"/"+proto+
					" is not in the default zone's ports ("+strings.TrimSpace(portsOut)+
					") or services ("+strings.TrimSpace(svcOut)+")"), nil
			}
		}
	}
	if err := cancelled(); err != nil {
		return Verdict{}, err
	}

	// 3. raw iptables
	if !frontEnd {
		if out, ok := run(ctx, r, cmdIPTablesRuleset); ok {
			skip["ip filter"] = true
			b, u, detail := parseIPTables(out, port, proto).blocks("INPUT")
			if b {
				return blocked(IPTables, detail), nil
			}
			if u {
				uncertain(detail)
			}
		}
	}
	if err := cancelled(); err != nil {
		return Verdict{}, err
	}

	// 4. other nftables tables
	if out, ok := run(ctx, r, cmdNFTRuleset); ok {
		for _, t := range parseNFTRuleset(out) {
			if skip[t.ref()] || (t.family != "ip" && t.family != "inet") {
				continue
			}
			rs := t.ruleset(port, proto)
			for _, c := range t.inputChains() {
				b, u, detail := rs.blocks(c.name)
				if b {
					v := Verdict{Blocked: true, By: NFTables, Table: t.ref(), Chain: c.name,
						Detail: t.ref() + " chain " + c.name + ": " + detail}
					v.argv = [][]string{nftInsertArgv(t, c.name, port, proto)}
					v.Commands = []string{strings.Join(v.argv[0], " ")}
					return v, nil
				}
				if u {
					uncertain(t.ref() + " chain " + c.name + ": " + detail)
				}
			}
		}
	}
	if err := cancelled(); err != nil {
		return Verdict{}, err
	}
	return res, nil
}

// firewalldRefine looks past `--list-ports` and `--list-services` before a
// port is called blocked by firewalld: a zone whose target is ACCEPT (the
// trusted zone) or that opens the whole protocol, a rich rule that accepts
// the port for everyone, or a listed service outside the builtin map whose
// definition (`--info-service`, at most maxServiceLookups calls) contains
// the port all open it. unsure explains a rich rule that may open it (the
// check then reports "not blocked, uncertain"). When the extra commands
// fail, the port stays blocked as the task's heuristic defines.
func firewalldRefine(ctx context.Context, r exec.Runner, services string, port int, proto string) (open bool, unsure string) {
	type info struct {
		ports  string
		protos []string
		ok     bool
	}
	cache := map[string]info{}
	lookup := func(name string) (string, []string, bool) {
		if ports, protos, ok := builtinService(name); ok {
			return ports, protos, true
		}
		if in, seen := cache[name]; seen {
			return in.ports, in.protos, in.ok
		}
		var in info
		if len(cache) < maxServiceLookups {
			if out, ok := run(ctx, r, []string{"firewall-cmd", "--info-service=" + name}); ok {
				in.ports, in.protos = parseFirewalldService(out)
				in.ok = true
			}
		}
		cache[name] = in
		return in.ports, in.protos, in.ok
	}
	for _, svc := range strings.Fields(services) {
		if ports, protos, ok := lookup(svc); ok && (firewalldOpen(ports, "", port, proto) || slices.Contains(protos, proto)) {
			return true, ""
		}
	}
	out, ok := run(ctx, r, cmdFirewalldAll)
	if !ok {
		return false, ""
	}
	z := parseFirewalldZone(out)
	if strings.EqualFold(z.target, "ACCEPT") || slices.Contains(z.protocols, proto) {
		return true, ""
	}
	for _, rr := range z.rich {
		switch firewalldRich(rr, port, proto, lookup) {
		case yes:
			return true, ""
		case maybe:
			if unsure == "" {
				unsure = "firewalld: rich rule may open the port: " + rr
			}
		}
	}
	return false, unsure
}

// nftInsertArgv inserts an accept rule at the head of another table's
// chain.
func nftInsertArgv(t *nftTable, chain string, port int, proto string) []string {
	return []string{"nft", "insert", "rule", t.family, t.name, chain, proto, "dport", strconv.Itoa(port), "accept"}
}

// openArgv returns the commands that open port/proto in firewall k.
func openArgv(k Kind, port int, proto string) [][]string {
	if !validPort(port) || (proto != ProtoTCP && proto != ProtoUDP) {
		return nil
	}
	p := strconv.Itoa(port)
	switch k {
	case UFW:
		return [][]string{{"ufw", "allow", p + "/" + proto}}
	case Firewalld:
		return [][]string{
			{"firewall-cmd", "--permanent", "--add-port=" + p + "/" + proto},
			{"firewall-cmd", "--reload"},
		}
	case IPTables:
		return [][]string{{"iptables", "-I", "INPUT", "-p", proto, "--dport", p, "-j", "ACCEPT"}}
	}
	return nil
}

// OpenCommand returns the exact suggestion shown to the owner to open
// port/proto in firewall k (section 10):
//
//	ufw        ufw allow 443/tcp
//	firewalld  firewall-cmd --permanent --add-port=443/tcp && firewall-cmd --reload
//	iptables   iptables -I INPUT -p tcp --dport 443 -j ACCEPT
//
// nftables has no generic command (the blocking table and chain matter);
// use Check, whose Verdict carries it. nil for nftables or invalid input.
func OpenCommand(k Kind, port int, proto string) []string {
	argv := openArgv(k, port, proto)
	if len(argv) == 0 {
		return nil
	}
	lines := make([]string, len(argv))
	for i, a := range argv {
		lines[i] = strings.Join(a, " ")
	}
	if k == Firewalld {
		return []string{strings.Join(lines, " && ")}
	}
	return lines
}

// Open runs OpenCommand(k, port, proto); only after the owner confirmed.
// Errors are DEY-P019 wrapping the runner error. nftables returns DEY-P019
// without running anything (use Verdict.Open).
func Open(ctx context.Context, r exec.Runner, k Kind, port int, proto string) error {
	argv := openArgv(k, port, proto)
	if len(argv) == 0 {
		return deyerr.New(deyerr.P019, deyerr.Params{"firewall": string(k)}).
			WithWhy("there is no generic command to open a port in this firewall; run the port check to get the exact command")
	}
	return runArgv(ctx, r, k, argv)
}

func runArgv(ctx context.Context, r exec.Runner, k Kind, argv [][]string) error {
	for _, a := range argv {
		cctx, cancel := context.WithTimeout(ctx, applyTimeout)
		_, stderr, err := r.Run(cctx, a[0], a[1:], nil)
		cancel()
		if err != nil {
			e := deyerr.Wrap(deyerr.P019, err, deyerr.Params{"firewall": string(k)})
			if d := strings.TrimSpace(string(stderr)); d != "" {
				e = e.WithDetail(d)
			}
			return e
		}
	}
	return nil
}
