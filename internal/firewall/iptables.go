package firewall

import (
	"strings"
)

// iptablesNonTerminal are targets that do not end rule traversal.
var iptablesNonTerminal = map[string]bool{
	"LOG": true, "NFLOG": true, "ULOG": true, "MARK": true, "CONNMARK": true,
	"TCPMSS": true, "CT": true, "NOTRACK": true, "TRACE": true, "AUDIT": true,
	"SET": true, "CLASSIFY": true, "DSCP": true, "TOS": true, "TTL": true,
	"CHECKSUM": true, "CONNSECMARK": true, "SECMARK": true,
}

// iptablesModules are match modules the heuristic understands (their
// options are handled below); any other module makes a rule "maybe".
var iptablesModules = map[string]bool{
	"tcp": true, "udp": true, "multiport": true, "comment": true,
	"state": true, "conntrack": true, "limit": true, "addrtype": true,
	"pkttype": true,
}

// parseIPTables converts `iptables -S` output (filter table, all chains)
// into the evaluation model for a new IPv4 connection to port/proto.
func parseIPTables(out string, port int, proto string) ruleset {
	rs := ruleset{}
	get := func(name string) *evalChain {
		c := rs[name]
		if c == nil {
			c = &evalChain{policy: vAccept}
			rs[name] = c
		}
		return c
	}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		f := splitArgs(line)
		if len(f) < 2 || strings.HasPrefix(line, "#") {
			continue
		}
		switch f[0] {
		case "-P":
			if len(f) >= 3 {
				c := get(f[1])
				c.policy = vAccept
				if f[2] == "DROP" || f[2] == "REJECT" {
					c.policy = vDrop
				}
			}
		case "-N":
			get(f[1])
		case "-A":
			m, v, target := iptablesRule(f[2:], port, proto)
			get(f[1]).rules = append(get(f[1]).rules, evalRule{match: m, verdict: v, target: target, text: "iptables: " + line})
		}
	}
	// Jumps to targets that are neither chains nor known targets (an
	// extension target such as DOCKER or a module we do not know) are
	// treated as non-terminating "maybe" rules.
	for _, c := range rs {
		for i, r := range c.rules {
			if (r.verdict == vJump || r.verdict == vGoto) && rs[r.target] == nil {
				c.rules[i].verdict = vNone
				c.rules[i].match = and(r.match, maybe)
			}
		}
	}
	return rs
}

// iptablesRule interprets the arguments of one -A rule.
func iptablesRule(args []string, port int, proto string) (tri, verdict, string) {
	m := yes
	neg := false
	next := func(i *int) string {
		if *i+1 < len(args) {
			*i++
			return args[*i]
		}
		return ""
	}
	apply := func(t tri) {
		if neg {
			t = not(t)
		}
		m = and(m, t)
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "!" {
			neg = true
			continue
		}
		switch a {
		case "-p", "--protocol":
			apply(protoTri(next(&i), proto))
		case "--dport", "--destination-port", "--dports", "--destination-ports":
			apply(boolTri(portInList(strings.Split(next(&i), ","), ":", port)))
		case "--sport", "--source-port", "--sports", "--source-ports":
			next(&i)
			apply(no) // restricted to a client source port: not "everyone"
		case "-s", "--source":
			if v := next(&i); !isAnyAddr(v) {
				// Restricted to some sources: not "everyone". Negated
				// ("all but these") it applies to an arbitrary client.
				apply(no)
			}
		case "-d", "--destination":
			apply(destTri(next(&i)))
		case "-i", "--in-interface":
			if next(&i) == "lo" {
				apply(no)
			} else {
				apply(maybe)
			}
		case "-o", "--out-interface":
			next(&i)
		case "-m", "--match":
			if !iptablesModules[next(&i)] {
				apply(maybe)
			}
		case "--state", "--ctstate":
			apply(boolTri(containsFold(strings.Split(next(&i), ","), "NEW")))
		case "--dst-type":
			apply(boolTri(strings.EqualFold(next(&i), "LOCAL")))
		case "--pkt-type":
			apply(boolTri(strings.EqualFold(next(&i), "host")))
		case "--syn":
			apply(yes)
		case "-f", "--fragment":
			apply(no)
		case "--comment", "--limit", "--limit-burst":
			next(&i)
		case "-j", "--jump", "-g", "--goto":
			target := next(&i)
			return m, iptablesTarget(target, a == "-g" || a == "--goto"), target
		default:
			// Unknown option: it may restrict the match; skip its value.
			m = and(m, maybe)
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && args[i+1] != "!" {
				i++
			}
		}
		neg = false
	}
	return m, vNone, ""
}

func iptablesTarget(t string, isGoto bool) verdict {
	switch {
	case t == "ACCEPT":
		return vAccept
	case t == "DROP" || t == "REJECT":
		return vDrop
	case t == "RETURN":
		return vReturn
	case iptablesNonTerminal[t]:
		return vNone
	case isGoto:
		return vGoto
	}
	return vJump
}

// protoTri matches an iptables/nft protocol value against proto.
func protoTri(v, proto string) tri {
	switch strings.ToLower(v) {
	case "all", "0":
		return yes
	case "tcp", "6":
		return boolTri(proto == ProtoTCP)
	case "udp", "17":
		return boolTri(proto == ProtoUDP)
	}
	return no
}

// destTri matches a destination address against "one of this host's
// public addresses".
func destTri(v string) tri {
	switch {
	case isAnyAddr(v):
		return yes
	case strings.HasPrefix(v, "127."):
		return no
	}
	return maybe
}

func isAnyAddr(v string) bool { return v == "0.0.0.0/0" || v == "0/0" || v == "0.0.0.0" }

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), s) {
			return true
		}
	}
	return false
}

// splitArgs splits a command line on spaces, honouring double quotes (as
// printed by iptables -S for comments) and backslash escapes inside them.
// The quotes are removed.
func splitArgs(s string) []string { return tokenize(s, false) }

// tokenize is splitArgs; keepQuotes leaves the quotes on quoted tokens so
// a quoted "drop" is not mistaken for a verdict (nft).
func tokenize(s string, keepQuotes bool) []string {
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\\' && i+1 < len(s):
			i++
			if keepQuotes {
				cur.WriteByte('\\')
			}
			cur.WriteByte(s[i])
		case c == '"':
			inQuote = !inQuote
			have = true
			if keepQuotes {
				cur.WriteByte(c)
			}
		case !inQuote && (c == ' ' || c == '\t'):
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteByte(c)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}
