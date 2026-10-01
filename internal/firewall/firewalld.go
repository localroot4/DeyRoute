package firewall

import (
	"slices"
	"strings"
)

// firewalldServices maps the few firewalld services checked without asking
// firewalld (section 10 heuristic).
var firewalldServices = map[string][]string{
	"ssh":   {"22/tcp"},
	"http":  {"80/tcp"},
	"https": {"443/tcp"},
}

// maxServiceLookups bounds the `firewall-cmd --info-service` calls of one
// check (services outside firewalldServices).
const maxServiceLookups = 8

// firewalldOpen reports whether port/proto is open in the default zone
// given the output of `firewall-cmd --list-ports` ("443/tcp 2000-2010/udp")
// and `firewall-cmd --list-services` ("dhcpv6-client ssh https").
func firewalldOpen(portsOut, servicesOut string, port int, proto string) bool {
	entries := strings.Fields(portsOut)
	for _, svc := range strings.Fields(servicesOut) {
		entries = append(entries, firewalldServices[svc]...)
	}
	for _, e := range entries {
		list, p, ok := strings.Cut(e, "/")
		if ok && p == proto && portInList([]string{list}, "-", port) {
			return true
		}
	}
	return false
}

// serviceLookup returns the ports ("80/tcp 443/tcp") and whole protocols of
// a firewalld service; ok is false when the service is unknown.
type serviceLookup func(name string) (ports string, protocols []string, ok bool)

// builtinService resolves the services of firewalldServices only.
func builtinService(name string) (string, []string, bool) {
	ports, ok := firewalldServices[name]
	return strings.Join(ports, " "), nil, ok
}

// firewalldZone is what the check reads from `firewall-cmd --list-all`
// (the default zone) besides its ports and services.
type firewalldZone struct {
	target    string   // "default", "ACCEPT" (trusted), "DROP", "%%REJECT%%"
	protocols []string // whole protocols opened ("tcp")
	rich      []string // rich rules, one per line
}

// parseFirewalldZone parses `firewall-cmd --list-all`:
//
//	public (active)
//	  target: default
//	  services: dhcpv6-client ssh
//	  ports: 443/tcp
//	  protocols:
//	  rich rules:
//		rule family="ipv4" source address="10.0.0.0/8" port port="8443" protocol="tcp" accept
func parseFirewalldZone(out string) firewalldZone {
	var z firewalldZone
	inRich := false
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		key, val, isKV := strings.Cut(line, ":")
		switch {
		case strings.HasPrefix(line, "rule "):
			if inRich {
				z.rich = append(z.rich, line)
			}
		case !isKV:
		case key == "rich rules":
			inRich = true
		default:
			inRich = false
			switch key {
			case "target":
				z.target = strings.TrimSpace(val)
			case "protocols":
				z.protocols = strings.Fields(val)
			}
		}
	}
	return z
}

// parseFirewalldService reads the ports ("9090/tcp 9091/udp") and whole
// protocols of `firewall-cmd --info-service=<name>`.
func parseFirewalldService(out string) (ports string, protocols []string) {
	for _, raw := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(raw), ":")
		switch {
		case !ok:
		case key == "ports":
			ports = strings.TrimSpace(val)
		case key == "protocols":
			protocols = strings.Fields(val)
		}
	}
	return ports, protocols
}

// firewalldRich evaluates one rich rule for a new IPv4 connection from an
// arbitrary client to port/proto: yes when it certainly accepts it, maybe
// when it may, no otherwise (another port or family, a restricted source, a
// non-accept action).
//
//	rule family="ipv4" port port="443" protocol="tcp" accept              → yes
//	rule family="ipv4" source address="10.0.0.0/8" port port="443" … accept → no
//	rule service name="cockpit" accept                → as the service's ports
//
// services resolves a service name to its ports and protocols (nil: only
// the builtin map); a service it cannot resolve makes the rule a maybe.
func firewalldRich(rule string, port int, proto string, services serviceLookup) tri {
	f := splitArgs(rule)
	if len(f) == 0 || f[0] != "rule" {
		return maybe
	}
	var (
		m                 = yes
		accept, negate    bool
		elem              string
		hasPort, hasSvc   bool
		portVal, protoVal string
		svcName, protoEl  string
	)
	for _, t := range f[1:] {
		key, val, isKV := strings.Cut(t, "=")
		if !isKV {
			switch t {
			case "accept":
				accept = true
			case "reject", "drop", "mark":
				return no
			case "NOT":
				negate = true
			default:
				elem, negate = t, false
			}
			continue
		}
		switch {
		case key == "family":
			if val == "ipv6" {
				return no
			}
		case elem == "source" && key == "address" && !negate && isAnyAddr(val):
			// every IPv4 source
		case elem == "source" && (key == "address" || key == "ipset" || key == "mac"):
			if negate {
				m = and(m, maybe) // everyone except some sources
			} else {
				return no // only some sources
			}
		case elem == "destination":
			m = and(m, maybe) // may be one of this host's addresses
		case elem == "port" && key == "port":
			hasPort, portVal = true, val
		case elem == "port" && key == "protocol":
			protoVal = val
		case elem == "service" && key == "name":
			hasSvc, svcName = true, val
		case elem == "protocol" && key == "value":
			protoEl = val
		case elem == "source-port" || elem == "icmp-block" || elem == "icmp-type" || elem == "masquerade":
			return no
		case elem == "forward-port":
			m = and(m, maybe)
		}
	}
	if !accept {
		return no
	}
	switch {
	case hasPort:
		m = and(m, boolTri(protoVal == proto && portInList([]string{portVal}, "-", port)))
	case hasSvc:
		if services == nil {
			services = builtinService
		}
		if ports, protos, ok := services(svcName); ok {
			m = and(m, boolTri(firewalldOpen(ports, "", port, proto) || slices.Contains(protos, proto)))
		} else {
			m = and(m, maybe)
		}
	case protoEl != "":
		m = and(m, protoTri(protoEl, proto))
	}
	return m
}
