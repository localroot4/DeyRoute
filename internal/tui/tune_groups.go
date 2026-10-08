package tui

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

// TuneGroupOf returns the group of a tuning key (a sysctl name, a file, or
// "<unit> <setting>").
func TuneGroupOf(key string) i18n.Key {
	k := TuneShortKey(key)
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

// TuneShortKey drops the well-known sysctl prefixes: net.ipv4.tcp_rmem is
// tcp_rmem. Files and unit settings stay as they are.
func TuneShortKey(key string) string {
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

// tuneByteKeys are the keys whose values are byte counts.
var tuneByteKeys = map[string]bool{
	"rmem_max": true, "wmem_max": true, "rmem_default": true, "wmem_default": true,
	"udp_rmem_min": true, "udp_wmem_min": true, "tcp_rmem": true, "tcp_wmem": true,
	"tcp_notsent_lowat": true,
}

// TuneValue renders a value of key for reading: byte counts in KiB/MiB
// ("4 KiB · 128 KiB · 16 MiB" for the triples), the "no limit" value of
// tcp_notsent_lowat by name; anything else as it is.
func TuneValue(key, v string) string {
	k := TuneShortKey(key)
	if !tuneByteKeys[k] {
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
		out = append(out, TuneBytes(n))
	}
	return strings.Join(out, " · ")
}

// TuneGroups orders keys by group (tuneGroupOrder) and, inside a group, as
// given; empty groups are left out.
func TuneGroups(keys []string) []TuneKeyGroup {
	by := map[i18n.Key][]string{}
	for _, k := range keys {
		g := TuneGroupOf(k)
		by[g] = append(by[g], k)
	}
	var out []TuneKeyGroup
	for _, g := range tuneGroupOrder {
		if len(by[g]) > 0 {
			out = append(out, TuneKeyGroup{ID: g, Title: i18n.T(g), Keys: by[g]})
		}
	}
	return out
}

// TuneKeyGroup is one titled group of keys.
type TuneKeyGroup struct {
	ID    i18n.Key // the group's title key: a stable id
	Title string
	Keys  []string
}

// TuneBytes renders a byte count the way the tuning keys are thought of:
// "16 MiB", "128 KiB"; a count that is no whole KiB stays in bytes
// ("87380 B").
func TuneBytes(n int64) string {
	switch {
	case n >= 1<<30 && n%(1<<30) == 0:
		return strconv.FormatInt(n>>30, 10) + " GiB"
	case n >= 1<<20 && n%(1<<20) == 0:
		return strconv.FormatInt(n>>20, 10) + " MiB"
	case n >= 1<<10 && n%(1<<10) == 0:
		return strconv.FormatInt(n>>10, 10) + " KiB"
	}
	return strconv.FormatInt(n, 10) + " B"
}

// TuneGroupSummary is the one-line meaning of a group's values (values maps
// the full keys of the group to what is in effect): "BBR · fq", "up to
// 16 MiB per connection", "dead connections found in about 8 min"; the
// number of settings when the group has no summary of its own.
func TuneGroupSummary(g TuneKeyGroup, values map[string]string) string {
	get := func(short string) string {
		for _, k := range g.Keys {
			if TuneShortKey(k) == short {
				return strings.TrimSpace(values[k])
			}
		}
		return ""
	}
	num := func(short string) int64 {
		n, err := strconv.ParseInt(get(short), 10, 64)
		if err != nil {
			return -1
		}
		return n
	}
	var parts []string
	switch g.ID {
	case i18n.CLITuneGroupSpeed:
		if v := get("tcp_congestion_control"); v != "" {
			parts = append(parts, strings.ToUpper(v))
		}
		if v := get("default_qdisc"); v != "" {
			parts = append(parts, v)
		}
	case i18n.CLITuneGroupBuffers:
		if n := max(num("rmem_max"), num("wmem_max")); n > 0 {
			parts = append(parts, i18n.T(i18n.TuneSumBuffers, TuneBytes(n)))
		}
	case i18n.CLITuneGroupConns:
		if n := num("somaxconn"); n > 0 {
			parts = append(parts, i18n.T(i18n.TuneSumQueue, n))
		}
		if f := strings.Fields(get("ip_local_port_range")); len(f) == 2 {
			parts = append(parts, i18n.T(i18n.TuneSumPorts, f[0], f[1]))
		}
	case i18n.CLITuneGroupKeepalive:
		t, iv, p := num("tcp_keepalive_time"), num("tcp_keepalive_intvl"), num("tcp_keepalive_probes")
		if t > 0 && iv > 0 && p > 0 {
			parts = append(parts, i18n.T(i18n.TuneSumKeepalive, (t+iv*p+59)/60))
		}
	case i18n.CLITuneGroupConntrack:
		if n := num("nf_conntrack_max"); n > 0 {
			parts = append(parts, i18n.T(i18n.TuneSumConntrack, n))
		}
	}
	if len(parts) == 0 {
		return i18n.T(i18n.TuneSumCount, len(g.Keys))
	}
	return strings.Join(parts, " · ")
}

// TuneGroupHelp says in one or two sentences what a group of settings is
// for ("" for the catch-all group).
func TuneGroupHelp(id i18n.Key) string {
	switch id {
	case i18n.CLITuneGroupSpeed:
		return i18n.T(i18n.TuneHelpSpeed)
	case i18n.CLITuneGroupBuffers:
		return i18n.T(i18n.TuneHelpBuffers)
	case i18n.CLITuneGroupConns:
		return i18n.T(i18n.TuneHelpConns)
	case i18n.CLITuneGroupKeepalive:
		return i18n.T(i18n.TuneHelpKeepalive)
	case i18n.CLITuneGroupConntrack:
		return i18n.T(i18n.TuneHelpConntrack)
	case i18n.CLITuneGroupServices:
		return i18n.T(i18n.TuneHelpServices)
	}
	return ""
}
