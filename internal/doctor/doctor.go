// Package doctor implements `deyroute doctor` (spec section 13), the main
// support tool:
//
//  1. collection — Collector gathers named, already redacted text sections
//     (OS, versions, units, logs, firewall, sysctl, listening ports,
//     certificate expiry) plus the measurements the rules need; the daemon
//     (root) runs it and adds its own sections (status, events, ladder
//     probes, port checks) through the Local API (api.DoctorData);
//  2. analysis — Run applies the 15 simple rules R01..R15 to Facts and
//     returns only the problems, in plain English (internal/i18n);
//  3. output — Summary renders the colored summary of at most 20 lines and
//     WriteBundle writes /root/deyroute-doctor-<UTC>.tar.gz (0600) with every
//     part passed through the central secret filter (internal/log) and a
//     final scan that refuses to write the file (DEY-X060) if any secret
//     survived (section 11, scenario S26).
//
// `deyroute doctor --node <id>` adds the node's sections to the same bundle
// under node-<id>/ (NodePrefix, SectionParts).
package doctor

import (
	"bytes"
	"strings"
	"unicode"

	dlog "github.com/localroot4/deyroute/internal/log"
)

// Stable section names. They are the keys of api.DoctorData.Sections and,
// through SectionParts, the file names inside the bundle ("os" → os.txt;
// names that already carry an extension are kept, e.g. logs/hub.log).
const (
	SectionOS       = "os"       // os-release, kernel, arch, uptime, memory, disk
	SectionVersions = "versions" // deyroute build and installed backend versions
	SectionUnits    = "units"    // deyroute-hub/node and every deyroute-tun@ instance
	SectionFirewall = "firewall" // detected firewalls + nft list table inet deyroute
	SectionSysctl   = "sysctl"   // section 12 keys as the kernel reports them
	SectionPorts    = "ports"    // listening sockets (ss -Hlntup or /proc/net)
	SectionCerts    = "certs"    // expiry of CA, hub/node and tunnel certificates
	// LogSectionPrefix starts the name of every log section:
	// "logs/<path relative to /var/log/deyroute>", e.g. "logs/tunnels/main.log".
	LogSectionPrefix = "logs/"

	// Sections supplied by the daemon (it owns the data); doctor provides
	// the formatting helpers StatusSection and EventsSection.
	SectionStatus     = "status"      // api.Status as JSON (tunnels, nodes, control RTT)
	SectionEvents     = "events"      // the last 50 events
	SectionLadder     = "ladder"      // ladder probe/validate result of every rung
	SectionPortChecks = "port-checks" // four-stage check of every port map
)

// Bundle files written by WriteBundle next to the parts.
const (
	FindingsFile = "findings.txt"
	SummaryFile  = "summary.txt"
)

// Limits (section 13).
const (
	// LogTailLines is how many trailing lines of each log file are collected.
	LogTailLines = 200
	// EventCount is how many recent events the events section holds.
	EventCount = 50
	// SummaryMaxLines bounds the terminal summary.
	SummaryMaxLines = 20
)

// redactText passes s through the central secret filter, including the
// multi-line private-key handling of log.RedactingWriter.
func redactText(s string) string {
	if s == "" {
		return s
	}
	var b bytes.Buffer
	w := dlog.RedactingWriter(&b)
	_, _ = w.Write([]byte(s)) // a bytes.Buffer never fails
	_ = w.Close()
	return b.String()
}

// oneLine collapses line breaks so a message stays on one summary line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// cleanText makes untrusted text (process names, node-supplied warnings,
// versions, addresses) safe to print on a terminal: one line, and every
// control character (C0, DEL, C1 — ESC starts terminal escape sequences)
// and bidirectional-override character replaced by "?".
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return '?'
		}
		return r
	}, oneLine(s))
}
