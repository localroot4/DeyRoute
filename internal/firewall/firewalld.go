package firewall

import "strings"

// firewalldServices maps the few firewalld services checked without asking
// firewalld (section 10 heuristic).
var firewalldServices = map[string][]string{
	"ssh":   {"22/tcp"},
	"http":  {"80/tcp"},
	"https": {"443/tcp"},
}

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
