package firewall

import (
	"regexp"
	"strconv"
	"strings"
)

// ufwApps maps the application profiles shipped with common packages to
// their ports, so rules like "Nginx Full ALLOW Anywhere" are understood.
var ufwApps = map[string]string{
	"OpenSSH":       "22/tcp",
	"Nginx HTTP":    "80/tcp",
	"Nginx HTTPS":   "443/tcp",
	"Nginx Full":    "80,443/tcp",
	"Nginx QUIC":    "443/udp",
	"Apache":        "80/tcp",
	"Apache Secure": "443/tcp",
	"Apache Full":   "80,443/tcp",
	"WWW":           "80/tcp",
	"WWW Secure":    "443/tcp",
	"WWW Full":      "80,443/tcp",
}

// ufwRuleRe splits a status line into To, Action, direction and From.
var ufwRuleRe = regexp.MustCompile(`^(.+?)\s+(ALLOW|DENY|REJECT|LIMIT)(?:\s+(IN|OUT|FWD))?\s+(.+)$`)

// ufwStatus is the parsed output of `ufw status verbose` (or `ufw status`).
type ufwStatus struct {
	active    bool
	defaultIn verdict
	lines     []string // rule lines, in evaluation order
}

// parseUFW parses the status output.
func parseUFW(out string) ufwStatus {
	st := ufwStatus{defaultIn: vDrop} // ufw's default incoming policy is deny
	inRules := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "Status:"):
			st.active = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
		case strings.HasPrefix(line, "Default:"):
			// "Default: deny (incoming), allow (outgoing), disabled (routed)"
			for _, part := range strings.Split(strings.TrimPrefix(line, "Default:"), ",") {
				f := strings.Fields(part)
				if len(f) == 2 && f[1] == "(incoming)" {
					if f[0] == "allow" {
						st.defaultIn = vAccept
					} else {
						st.defaultIn = vDrop
					}
				}
			}
		case strings.HasPrefix(line, "--"):
			inRules = true
		case inRules && line != "":
			st.lines = append(st.lines, line)
		}
	}
	return st
}

// ruleset converts the status to the evaluation model for port/proto.
func (st ufwStatus) ruleset(port int, proto string) ruleset {
	c := &evalChain{policy: st.defaultIn}
	for _, line := range st.lines {
		m, v := ufwRule(line, port, proto)
		c.rules = append(c.rules, evalRule{match: m, verdict: v, text: "ufw: " + line})
	}
	return ruleset{"ufw": c}
}

// ufwRule interprets one status line.
func ufwRule(line string, port int, proto string) (tri, verdict) {
	// Drop a trailing "# comment".
	if i := strings.Index(line, " #"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	g := ufwRuleRe.FindStringSubmatch(line)
	if g == nil {
		return maybe, vNone
	}
	to, action, dir, from := g[1], g[2], g[3], strings.TrimSpace(g[4])
	v := vAccept
	if action == "DENY" || action == "REJECT" {
		v = vDrop
	}
	if dir == "OUT" || dir == "FWD" {
		return no, v
	}
	// IPv4 only: (v6) rules do not apply.
	if strings.Contains(to, "(v6)") || strings.Contains(from, "(v6)") {
		return no, v
	}
	// The source must be everyone for the rule to open (or close) the port
	// for users.
	if from != "Anywhere" {
		return no, v
	}
	m := yes
	if i := strings.Index(to, " on "); i >= 0 {
		m = maybe // interface-specific
		to = strings.TrimSpace(to[:i])
	}
	if ports, ok := ufwApps[to]; ok {
		return and(m, ufwPortMatch(ports, port, proto)), v
	}
	f := strings.Fields(to)
	switch {
	case len(f) == 1 && f[0] == "Anywhere":
		return m, v
	case len(f) == 1 && strings.HasPrefix(f[0], "Anywhere/"):
		return and(m, boolTri(strings.TrimPrefix(f[0], "Anywhere/") == proto)), v
	case len(f) == 1 && looksLikeUFWPorts(f[0]):
		return and(m, ufwPortMatch(f[0], port, proto)), v
	case len(f) == 2 && looksLikeUFWPorts(f[1]):
		// "<destination address> 443/tcp": may be this host's address.
		return and(maybe, and(m, ufwPortMatch(f[1], port, proto))), v
	}
	// A destination address only, or an unknown application profile.
	return maybe, v
}

var ufwPortsRe = regexp.MustCompile(`^[0-9][0-9,:]*(/(tcp|udp))?$`)

func looksLikeUFWPorts(s string) bool { return ufwPortsRe.MatchString(s) }

// ufwPortMatch matches "443", "443/tcp", "2000:2010/udp", "80,443/tcp".
func ufwPortMatch(spec string, port int, proto string) tri {
	list, p, hasProto := strings.Cut(spec, "/")
	if hasProto && p != proto {
		return no
	}
	return boolTri(portInList(strings.Split(list, ","), ":", port))
}

// portInList reports whether port is one of items ("443" or ranges joined
// by sep).
func portInList(items []string, sep string, port int) bool {
	for _, it := range items {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(it), sep)
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		if port >= a && port <= b {
			return true
		}
	}
	return false
}
