package firewall

import (
	"slices"
	"strconv"
	"strings"
)

// nftTable is one table of `nft list ruleset` output.
type nftTable struct {
	family, name string
	sets         map[string][]string // named set → element values
	maps         map[string][]string // named map → "key : value" entries
	chains       []*nftChain
}

// ref returns "family name".
func (t *nftTable) ref() string { return t.family + " " + t.name }

// nftChain is one chain; hook is empty for regular chains.
type nftChain struct {
	name   string
	typ    string
	hook   string
	policy verdict
	rules  []string
}

type nftFrame uint8

const (
	frameTable nftFrame = iota
	frameChain
	frameSet
	frameOther
)

// parseNFTRuleset parses the text of `nft list ruleset`. Unknown blocks
// (flowtables, counters, ct helpers, …) are skipped.
func parseNFTRuleset(out string) []*nftTable {
	var (
		tables   []*nftTable
		tbl      *nftTable
		ch       *nftChain
		stack    []nftFrame
		setName  string
		setIsMap bool
		setBody  strings.Builder
		setDepth int
		pending  strings.Builder
		pendD    int
	)
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		opens, closes := strings.Count(line, "{"), strings.Count(line, "}")
		if len(stack) == 0 {
			f := strings.Fields(line)
			if len(f) >= 3 && f[0] == "table" && strings.HasSuffix(line, "{") {
				tbl = &nftTable{family: "ip", sets: map[string][]string{}, maps: map[string][]string{}}
				if len(f) >= 4 {
					tbl.family, tbl.name = f[1], f[2]
				} else {
					tbl.name = f[1]
				}
				stack = append(stack, frameTable)
			}
			continue
		}
		switch stack[len(stack)-1] {
		case frameTable:
			if line == "}" {
				tables = append(tables, tbl)
				stack = stack[:len(stack)-1]
				continue
			}
			if !strings.HasSuffix(line, "{") {
				continue // table flags, comments
			}
			f := strings.Fields(line)
			switch {
			case f[0] == "chain" && len(f) >= 3:
				ch = &nftChain{name: f[1], policy: vAccept}
				stack = append(stack, frameChain)
			case (f[0] == "set" || f[0] == "map") && len(f) >= 3:
				setName, setIsMap, setDepth = f[1], f[0] == "map", 0
				setBody.Reset()
				stack = append(stack, frameSet)
			default:
				stack = append(stack, frameOther)
			}
		case frameChain:
			if pendD > 0 {
				pending.WriteString(" " + line)
				pendD += opens - closes
				if pendD <= 0 {
					ch.rules = append(ch.rules, pending.String())
					pending.Reset()
					pendD = 0
				}
				continue
			}
			switch {
			case line == "}":
				tbl.chains = append(tbl.chains, ch)
				stack = stack[:len(stack)-1]
			case strings.HasPrefix(line, "type "):
				parseChainHeader(ch, line)
			case strings.HasPrefix(line, "policy "):
				parseChainHeader(ch, line)
			case strings.HasPrefix(line, "comment "):
			case opens > closes:
				pending.WriteString(line)
				pendD = opens - closes
			default:
				ch.rules = append(ch.rules, line)
			}
		case frameSet:
			if line == "}" && setDepth == 0 {
				if setIsMap {
					tbl.maps[setName] = parseMapElements(setBody.String())
				} else {
					tbl.sets[setName] = parseElements(setBody.String())
				}
				stack = stack[:len(stack)-1]
				continue
			}
			setDepth += opens - closes
			setBody.WriteString(line + "\n")
		case frameOther:
			if line == "}" {
				stack = stack[:len(stack)-1]
			} else if strings.HasSuffix(line, "{") {
				stack = append(stack, frameOther)
			}
		}
	}
	return tables
}

// parseChainHeader reads "type filter hook input priority filter; policy drop;".
func parseChainHeader(ch *nftChain, line string) {
	for _, part := range strings.Split(line, ";") {
		f := strings.Fields(part)
		for i := 0; i+1 < len(f); i++ {
			switch f[i] {
			case "type":
				ch.typ = f[i+1]
			case "hook":
				ch.hook = f[i+1]
			case "policy":
				if f[i+1] == "drop" {
					ch.policy = vDrop
				} else {
					ch.policy = vAccept
				}
			}
		}
	}
}

// parseElements extracts the values of "elements = { a, b, … }".
func parseElements(body string) []string {
	var out []string
	for _, e := range elementList(body) {
		// Elements may carry annotations ("1.2.3.4 timeout 1h expires 5m").
		if f := strings.Fields(e); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

// parseMapElements extracts the entries of a map's
// "elements = { 80 : accept, 8080 : jump web }" as "key : value" strings.
func parseMapElements(body string) []string {
	var out []string
	for _, e := range elementList(body) {
		if e = strings.Join(strings.Fields(e), " "); e != "" {
			out = append(out, unquote(e))
		}
	}
	return out
}

// elementList returns the comma-separated items between the braces of the
// "elements = { … }" statement in body.
func elementList(body string) []string {
	i := strings.Index(body, "elements")
	if i < 0 {
		return nil
	}
	body = body[i:]
	start := strings.IndexByte(body, '{')
	end := strings.LastIndexByte(body, '}')
	if start < 0 || end < start {
		return nil
	}
	return strings.Split(body[start+1:end], ",")
}

// ruleset converts the table to the evaluation model for port/proto.
func (t *nftTable) ruleset(port int, proto string) ruleset {
	rs := ruleset{}
	for _, c := range t.chains {
		ec := &evalChain{policy: c.policy}
		for _, r := range c.rules {
			ec.rules = append(ec.rules, nftRuleIn(r, t.sets, t.maps, port, proto))
		}
		rs[c.name] = ec
	}
	return rs
}

// inputChains returns the filter chains on the input hook.
func (t *nftTable) inputChains() []*nftChain {
	var out []*nftChain
	for _, c := range t.chains {
		if c.hook == "input" && c.typ == "filter" {
			out = append(out, c)
		}
	}
	return out
}

// nftServices maps service names nft may print instead of port numbers.
var nftServices = map[string]int{
	"ftp": 21, "ssh": 22, "telnet": 23, "smtp": 25, "domain": 53, "http": 80,
	"pop3": 110, "ntp": 123, "imap": 143, "snmp": 161, "https": 443,
	"submissions": 465, "smtps": 465, "submission": 587, "domain-s": 853,
	"imaps": 993, "pop3s": 995, "openvpn": 1194, "mysql": 3306,
	"postgresql": 5432, "http-alt": 8080,
}

// nftPort parses a port value, a range "a-b" or a service name.
func nftPort(v string) (lo, hi int, ok bool) {
	if a, b, isRange := strings.Cut(v, "-"); isRange {
		x, err1 := strconv.Atoi(a)
		y, err2 := strconv.Atoi(b)
		return x, y, err1 == nil && err2 == nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n, n, true
	}
	if n, ok := nftServices[v]; ok {
		return n, n, true
	}
	return 0, 0, false
}

// nftRule interprets one rule line for a new IPv4 connection to port/proto
// in a table without named maps.
func nftRule(line string, sets map[string][]string, port int, proto string) evalRule {
	return nftRuleIn(line, sets, nil, port, proto)
}

// nftRuleIn interprets one rule line of a table with the given named sets
// and maps.
func nftRuleIn(line string, sets, maps map[string][]string, port int, proto string) evalRule {
	toks := tokenize(line, true)
	// nft prints a rule's comment after its verdict: `… accept comment "x"`.
	if n := len(toks); n >= 2 && toks[n-2] == "comment" {
		toks = toks[:n-2]
	}
	r := evalRule{text: "nftables: " + line}
	v, target, end, unsure := nftVerdict(toks)
	r.verdict, r.target = v, target
	if slices.Contains(toks[:end], ".") {
		// A concatenation ("ip saddr . tcp dport @allowed"): its key cannot be
		// evaluated here. It may match; a verdict map may accept.
		r.match = maybe
		if slices.Contains(toks, "vmap") {
			r.verdict = vAccept
		}
		return r
	}
	p := nftParser{toks: toks[:end], sets: sets, maps: maps, port: port, proto: proto, match: yes}
	p.run()
	r.match = p.match
	if unsure {
		r.match = and(r.match, maybe)
	}
	switch {
	case p.vmapSet:
		r.verdict, r.target = p.vmapVerdict, p.vmapTarget
	case slices.Contains(toks, "vmap"):
		// A verdict map on a selector the heuristic does not resolve
		// ("meta mark vmap { … }"): it may accept.
		r.verdict = vAccept
		r.match = and(r.match, maybe)
	}
	return r
}

// nftVerdict finds the rule's verdict at its end; end is where the match
// part stops. unsure is true for a verdict the heuristic cannot know
// (`queue` hands the packet to a userspace program that may accept it; it
// is modelled as a possible accept).
func nftVerdict(toks []string) (v verdict, target string, end int, unsure bool) {
	n := len(toks)
	if slices.Contains(toks, "vmap") { // the verdict comes from the map
		return vNone, "", n, false
	}
	if n >= 2 {
		switch toks[n-2] {
		case "jump":
			return vJump, toks[n-1], n - 2, false
		case "goto":
			return vGoto, toks[n-1], n - 2, false
		}
	}
	if n >= 1 {
		switch toks[n-1] {
		case "accept":
			return vAccept, "", n - 1, false
		case "drop":
			return vDrop, "", n - 1, false
		case "return":
			return vReturn, "", n - 1, false
		case "continue":
			return vNone, "", n - 1, false
		}
	}
	for i, t := range toks {
		switch t {
		case "reject":
			return vDrop, "", i, false
		case "queue":
			return vAccept, "", i, true
		}
	}
	return vNone, "", n, false
}

// isLogOption reports whether t is an option keyword of the log statement.
func isLogOption(t string) bool {
	switch t {
	case "prefix", "level", "flags", "group", "snaplen", "queue-threshold":
		return true
	}
	return false
}

// nftParser walks the match part of a rule.
type nftParser struct {
	toks        []string
	i           int
	sets        map[string][]string
	maps        map[string][]string
	port        int
	proto       string
	match       tri
	vmapSet     bool
	vmapVerdict verdict
	vmapTarget  string
}

func (p *nftParser) at(i int) string {
	if i < len(p.toks) {
		return p.toks[i]
	}
	return ""
}

func (p *nftParser) and(t tri) { p.match = and(p.match, t) }

var nftOps = map[string]string{
	"==": "==", "eq": "==", "!=": "!=", "ne": "!=",
	"<": "<", "lt": "<", ">": ">", "gt": ">", "<=": "<=", "le": "<=", ">=": ">=", "ge": ">=",
}

// value reads an optional operator and a value (single token, comma list
// or { … } set) at p.i. op is "vmap" for a verdict map, whose entries are
// returned as "key : verdict" strings.
func (p *nftParser) value() (op string, vals []string) {
	op = "=="
	if o, ok := nftOps[p.at(p.i)]; ok {
		op = o
		p.i++
	}
	if p.at(p.i) == "vmap" {
		op = "vmap"
		p.i++
	}
	if p.at(p.i) == "{" {
		var parts []string
		p.i++
		for p.i < len(p.toks) && p.toks[p.i] != "}" {
			parts = append(parts, p.toks[p.i])
			p.i++
		}
		p.i++ // "}"
		for _, v := range strings.Split(strings.Join(parts, " "), ",") {
			if v = strings.TrimSpace(v); v != "" {
				vals = append(vals, unquote(v))
			}
		}
		return op, vals
	}
	tok := p.at(p.i)
	p.i++
	for _, v := range strings.Split(tok, ",") {
		vals = append(vals, unquote(v))
	}
	return op, vals
}

// unquote removes the double quotes nft prints around strings; inside a
// verdict-map entry ("\"lo\" : accept") only the key's quotes go.
func unquote(v string) string {
	if k, rest, ok := strings.Cut(v, " : "); ok {
		return unquote(k) + " : " + rest
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// statement skips "<selector> set <value>" mangling statements.
func (p *nftParser) statement() bool {
	if p.at(p.i) == "set" {
		p.i += 2
		return true
	}
	return false
}

func (p *nftParser) run() {
	for p.i < len(p.toks) && p.match != no {
		t := p.toks[p.i]
		p.i++
		switch t {
		case "tcp", "udp", "th", "udplite", "sctp", "dccp":
			p.l4(t)
		case "ip":
			p.ip()
		case "ip6", "icmp", "icmpv6", "igmp", "arp", "vlan", "ether":
			p.and(no)
		case "meta":
			p.meta()
		case "iif", "iifname":
			p.iface()
		case "oif", "oifname":
			p.value()
		case "ct":
			p.ct()
		case "fib":
			if p.at(p.i) == "daddr" && p.at(p.i+1) == "type" {
				p.i += 2
				op, vals := p.value()
				p.cmp(op, boolTri(containsFold(vals, "local")))
			} else {
				p.and(maybe)
			}
		case "counter":
			switch p.at(p.i) {
			case "packets":
				p.i += 4
			case "name":
				p.i += 2
			}
		case "log":
			for isLogOption(p.at(p.i)) {
				p.i += 2
			}
		case "limit":
			p.limit()
		case "comment":
			p.i++
		default:
			p.and(maybe)
		}
	}
}

// cmp applies a match result under operator op.
func (p *nftParser) cmp(op string, t tri) {
	switch op {
	case "==":
		p.and(t)
	case "!=":
		p.and(not(t))
	default:
		p.and(maybe)
	}
}

func (p *nftParser) l4(t string) {
	if t != "th" && t != p.proto {
		p.and(no)
		return
	}
	field := p.at(p.i)
	p.i++
	switch field {
	case "dport":
		op, vals := p.value()
		if op == "vmap" {
			p.vmap(vals, func(k string) tri { return p.portIn([]string{k}) })
			return
		}
		switch op {
		case "==", "!=":
			p.cmp(op, p.portIn(vals))
		default:
			p.and(p.portCompare(op, vals))
		}
	case "sport":
		op, _ := p.value()
		if op == "!=" {
			p.and(maybe)
		} else {
			p.and(no)
		}
	case "flags":
		// "syn", "& (fin|syn|rst|ack) == syn", "syn / fin,syn,rst,ack"
		for p.i < len(p.toks) && isTCPFlagsToken(p.toks[p.i]) {
			p.i++
		}
		p.and(maybe)
	default:
		p.and(maybe)
	}
}

// portIn matches the port against values (numbers, ranges, service
// names, @sets).
func (p *nftParser) portIn(vals []string) tri {
	unknown := false
	check := func(v string) bool {
		lo, hi, ok := nftPort(v)
		if !ok {
			unknown = true
			return false
		}
		return p.port >= lo && p.port <= hi
	}
	for _, v := range vals {
		if strings.HasPrefix(v, "@") {
			elems, ok := p.sets[v[1:]]
			if !ok {
				unknown = true
				continue
			}
			for _, e := range elems {
				if check(e) {
					return yes
				}
			}
			continue
		}
		if check(v) {
			return yes
		}
	}
	if unknown {
		return maybe
	}
	return no
}

func (p *nftParser) portCompare(op string, vals []string) tri {
	if len(vals) != 1 {
		return maybe
	}
	n, err := strconv.Atoi(vals[0])
	if err != nil {
		return maybe
	}
	switch op {
	case "<":
		return boolTri(p.port < n)
	case ">":
		return boolTri(p.port > n)
	case "<=":
		return boolTri(p.port <= n)
	case ">=":
		return boolTri(p.port >= n)
	}
	return maybe
}

func (p *nftParser) ip() {
	field := p.at(p.i)
	p.i++
	switch field {
	case "saddr":
		op, vals := p.value()
		switch {
		case op == "vmap":
			// Only an any-address key applies to every client.
			p.vmap(vals, func(k string) tri { return boolTri(isAnyAddr(k)) })
		case op == "==" && len(vals) == 1 && isAnyAddr(vals[0]):
		case op == "==":
			p.and(no) // restricted to some sources
		default:
			p.and(maybe)
		}
	case "daddr":
		op, vals := p.value()
		if op == "vmap" {
			p.vmap(vals, destTri)
			return
		}
		loop := false
		for _, v := range vals {
			loop = loop || strings.HasPrefix(v, "127.")
		}
		switch {
		case loop:
			p.cmp(op, no)
		case op == "==" && len(vals) == 1 && isAnyAddr(vals[0]):
		default:
			p.and(maybe)
		}
	case "protocol":
		op, vals := p.value()
		if op == "vmap" {
			p.vmap(vals, func(k string) tri { return protoTri(k, p.proto) })
			return
		}
		p.cmp(op, anyProto(vals, p.proto))
	case "version":
		op, vals := p.value()
		p.cmp(op, boolTri(len(vals) == 1 && vals[0] == "4"))
	default:
		if !p.statement() {
			p.and(maybe)
		}
	}
}

func anyProto(vals []string, proto string) tri {
	for _, v := range vals {
		if protoTri(v, proto) == yes {
			return yes
		}
	}
	return no
}

func (p *nftParser) meta() {
	field := p.at(p.i)
	p.i++
	if p.statement() {
		return
	}
	switch field {
	case "l4proto":
		op, vals := p.value()
		if op == "vmap" {
			p.vmap(vals, func(k string) tri { return protoTri(k, p.proto) })
			return
		}
		p.cmp(op, anyProto(vals, p.proto))
	case "nfproto":
		op, vals := p.value()
		p.cmp(op, boolTri(containsFold(vals, "ipv4")))
	case "iif", "iifname":
		p.iface()
	case "oif", "oifname":
		p.value()
	case "pkttype":
		op, vals := p.value()
		p.cmp(op, boolTri(containsFold(vals, "host")))
	default:
		p.and(maybe)
	}
}

func (p *nftParser) iface() {
	op, vals := p.value()
	lo := containsFold(vals, "lo")
	switch {
	case op == "vmap":
		// Only "lo" is known not to be the Internet interface.
		p.vmap(vals, func(k string) tri {
			if strings.EqualFold(k, "lo") {
				return no
			}
			return maybe
		})
	case op == "!=" && lo:
		// not loopback: every client packet
	case op == "==" && lo && len(vals) == 1:
		p.and(no)
	default:
		p.and(maybe)
	}
}

func (p *nftParser) ct() {
	field := p.at(p.i)
	p.i++
	if p.statement() {
		return
	}
	switch field {
	case "state":
		op, vals := p.value()
		if op == "vmap" {
			p.vmap(vals, func(k string) tri { return boolTri(strings.EqualFold(k, "new")) })
			return
		}
		p.cmp(op, boolTri(containsFold(vals, "new")))
	case "direction":
		op, vals := p.value()
		p.cmp(op, boolTri(containsFold(vals, "original")))
	default:
		p.value()
		p.and(maybe)
	}
}

// vmap resolves a verdict map (inline entries or a named map "@name"): the
// entry whose key certainly matches decides the rule's verdict. Entries that
// may match make the rule a possible accept when their verdict can accept
// (accept, jump, goto), so a later drop is reported as uncertain; without
// any matching entry the rule does nothing. A map that cannot be read is a
// possible accept.
func (p *nftParser) vmap(entries []string, match func(key string) tri) {
	var all []string
	for _, e := range entries {
		if name, ok := strings.CutPrefix(e, "@"); ok {
			m, known := p.maps[name]
			if !known {
				p.vmapSet, p.vmapVerdict = true, vAccept
				p.and(maybe)
				return
			}
			all = append(all, m...)
			continue
		}
		all = append(all, e)
	}
	mayAccept := false
	for _, e := range all {
		// nft prints "key : verdict"; the spaces keep IPv6 keys whole.
		k, v, ok := strings.Cut(e, " : ")
		if !ok {
			k, v, ok = strings.Cut(e, ":")
		}
		if !ok {
			continue
		}
		m := match(strings.TrimSpace(k))
		if m == no {
			continue
		}
		vv, target, ok := mapVerdict(strings.Fields(v))
		if m == maybe {
			mayAccept = mayAccept || vv == vAccept || vv == vJump || vv == vGoto || !ok
			continue
		}
		if !ok { // a verdict we cannot read may accept
			vv, target = vAccept, ""
			p.and(maybe)
		}
		p.vmapSet, p.vmapVerdict, p.vmapTarget = true, vv, target
		return
	}
	p.vmapSet = true
	if mayAccept {
		p.vmapVerdict = vAccept
		p.and(maybe)
		return
	}
	p.vmapVerdict = vNone
	p.and(no)
}

// mapVerdict reads the verdict of one verdict-map entry ("accept",
// "jump web"); ok is false when it is not understood.
func mapVerdict(f []string) (v verdict, target string, ok bool) {
	if len(f) == 0 {
		return vNone, "", false
	}
	switch f[0] {
	case "accept":
		return vAccept, "", true
	case "drop", "reject":
		return vDrop, "", true
	case "return":
		return vReturn, "", true
	case "continue":
		return vNone, "", true
	case "jump", "goto":
		if len(f) < 2 {
			return vNone, "", false
		}
		if f[0] == "goto" {
			return vGoto, f[1], true
		}
		return vJump, f[1], true
	}
	return vNone, "", false
}

// limit skips "limit rate [over] N/second [burst N packets]".
func (p *nftParser) limit() {
	if p.at(p.i) == "rate" {
		p.i++
	}
	if p.at(p.i) == "over" {
		p.and(maybe)
		p.i++
	}
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		if t == "burst" || t == "packets" || t == "bytes" || strings.HasSuffix(t, "bytes") ||
			strings.Contains(t, "/") || (t != "" && t[0] >= '0' && t[0] <= '9') {
			p.i++
			continue
		}
		break
	}
}

// isTCPFlagsToken reports whether t is part of a "tcp flags" expression.
func isTCPFlagsToken(t string) bool {
	switch t {
	case "&", "|", "==", "!=", "/":
		return true
	}
	words := strings.FieldsFunc(t, func(r rune) bool {
		return r == '(' || r == ')' || r == '|' || r == ',' || r == '&' || r == '!' || r == '=' || r == '/'
	})
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		switch w {
		case "fin", "syn", "rst", "psh", "ack", "urg", "ecn", "cwr":
		default:
			return false
		}
	}
	return true
}
