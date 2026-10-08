package cli

import (
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/i18n"
)

// The tuning keys are shown in groups an owner can scan (what a group is
// for is in its title), with short names and readable values; --json keeps
// the full keys and the raw values.

// tuneGroupOrder is the order of the groups.
var tuneGroupOrder = []i18n.Key{
	i18n.CLITuneGroupSpeed, i18n.CLITuneGroupBuffers, i18n.CLITuneGroupConns,
	i18n.CLITuneGroupKeepalive, i18n.CLITuneGroupConntrack, i18n.CLITuneGroupServices,
	i18n.CLITuneGroupOther,
}

// tuneGroupOf returns the group of a tuning key (a sysctl name, a file, or
// "<unit> <setting>").
func tuneGroupOf(key string) i18n.Key {
	k := shortKey(key)
	switch {
	case strings.Contains(key, ".service") || strings.Contains(key, ".slice") || strings.HasPrefix(key, "tuning."):
		return i18n.CLITuneGroupServices
	case strings.Contains(key, "conntrack"):
		return i18n.CLITuneGroupConntrack
	case strings.HasPrefix(k, "tcp_keepalive_"):
		return i18n.CLITuneGroupKeepalive
	case strings.Contains(k, "rmem") || strings.Contains(k, "wmem"):
		return i18n.CLITuneGroupBuffers
	}
	switch k {
	case "tcp_congestion_control", "default_qdisc", "tcp_notsent_lowat", "tcp_slow_start_after_idle",
		"tcp_fastopen", "tcp_mtu_probing":
		return i18n.CLITuneGroupSpeed
	case "somaxconn", "tcp_max_syn_backlog", "netdev_max_backlog", "ip_local_port_range",
		"ip_local_reserved_ports", "tcp_fin_timeout", "tcp_tw_reuse", "nr_open", "file-max":
		return i18n.CLITuneGroupConns
	}
	return i18n.CLITuneGroupOther
}

// shortKey drops the well-known sysctl prefixes: net.ipv4.tcp_rmem is
// tcp_rmem. Files and unit settings stay as they are.
func shortKey(key string) string {
	if strings.ContainsAny(key, " /") {
		return key
	}
	for _, p := range []string{"net.ipv4.", "net.core.", "net.netfilter.", "net.ipv6.", "fs."} {
		if s, ok := strings.CutPrefix(key, p); ok {
			return s
		}
	}
	return key
}

// byteKeys are the keys whose values are byte counts.
var byteKeys = map[string]bool{
	"rmem_max": true, "wmem_max": true, "rmem_default": true, "wmem_default": true,
	"udp_rmem_min": true, "udp_wmem_min": true, "tcp_rmem": true, "tcp_wmem": true,
	"tcp_notsent_lowat": true,
}

// tuneValue renders a value of key for reading: byte counts in KiB/MiB
// ("4 KiB · 128 KiB · 16 MiB" for the triples), the "no limit" value of
// tcp_notsent_lowat by name; anything else as it is.
func tuneValue(key, v string) string {
	k := shortKey(key)
	if !byteKeys[k] {
		return v
	}
	if k == "tcp_notsent_lowat" && v == "4294967295" {
		return i18n.T(i18n.CLITuneNoLimit)
	}
	fields := strings.Fields(v)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.ParseInt(f, 10, 64)
		if err != nil || n < 0 {
			return v
		}
		out = append(out, tuneBytes(n))
	}
	return strings.Join(out, " · ")
}

// groupKeys orders keys by group (tuneGroupOrder) and, inside a group, as
// given; empty groups are left out.
func groupKeys(keys []string) []tuneKeyGroup {
	by := map[i18n.Key][]string{}
	for _, k := range keys {
		g := tuneGroupOf(k)
		by[g] = append(by[g], k)
	}
	var out []tuneKeyGroup
	for _, g := range tuneGroupOrder {
		if len(by[g]) > 0 {
			out = append(out, tuneKeyGroup{title: i18n.T(g), keys: by[g]})
		}
	}
	return out
}

// tuneKeyGroup is one titled group of keys.
type tuneKeyGroup struct {
	title string
	keys  []string
}
