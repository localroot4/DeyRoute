package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/tui"
)

func TestTuneValueAndKeys(t *testing.T) {
	for key, c := range map[string][2]string{
		"net.core.rmem_max":                    {"16777216", "16 MiB"},
		"net.ipv4.tcp_rmem":                    {"4096 87380 33554432", "4 KiB · 87380 B · 32 MiB"},
		"net.ipv4.tcp_notsent_lowat":           {"4294967295", "no limit"},
		"net.ipv4.tcp_congestion_control":      {"bbr", "bbr"},
		"net.netfilter.nf_conntrack_max":       {"65536", "65536"},
		"net.ipv4.ip_local_reserved_ports":     {"2082,30000-31999", "2082,30000-31999"},
		"deyroute-tun@.service OOMScoreAdjust": {"300", "300"},
	} {
		require.Equal(t, c[1], tuneValue(key, c[0]), key)
	}
	require.Equal(t, "tcp_rmem", shortKey("net.ipv4.tcp_rmem"))
	require.Equal(t, "nr_open", shortKey("fs.nr_open"))
	require.Equal(t, "/etc/modprobe.d/deyroute.conf", shortKey("/etc/modprobe.d/deyroute.conf"))
	require.Equal(t, "deyroute-tun@.service Slice", shortKey("deyroute-tun@.service Slice"))

	groups := groupKeys([]string{"net.core.rmem_max", "deyroute-tun@.service Slice", "net.ipv4.tcp_congestion_control",
		"net.netfilter.nf_conntrack_max", "net.ipv4.tcp_keepalive_time", "net.core.somaxconn", "vm.swappiness"})
	var titles []string
	for _, g := range groups {
		titles = append(titles, g.title)
	}
	require.Equal(t, []string{i18n.T(i18n.CLITuneGroupSpeed), i18n.T(i18n.CLITuneGroupBuffers), i18n.T(i18n.CLITuneGroupConns),
		i18n.T(i18n.CLITuneGroupKeepalive), i18n.T(i18n.CLITuneGroupConntrack), i18n.T(i18n.CLITuneGroupServices),
		i18n.T(i18n.CLITuneGroupOther)}, titles, "every group in its fixed order")
}

func TestLayoutHelpers(t *testing.T) {
	e := newEnv(t)
	for _, w := range []int{20, 60, 100, 300} {
		e.g.Caps = func() tui.Caps { return tui.Caps{Unicode: true, Width: w} }
		want := min(max(w, minLineWidth), maxLineWidth)
		require.Equal(t, want, e.g.lineWidth())
		require.Equal(t, want, width(e.g.sectionHead("TUNNELS", "")), w)
		require.LessOrEqual(t, width(e.g.sectionHead("TRAFFIC", "last hour · download · upload")), want, w)
		require.LessOrEqual(t, width(e.g.fit(strings.Repeat("x", 400))), want)
	}
	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 60} }
	require.True(t, strings.HasPrefix(e.g.sectionHead("NODES", ""), "-- NODES ---"), "ASCII rule")

	require.Equal(t, []string{"a · b", "c"}, joinWrap([]string{"a", "b", "c"}, " · ", 6))
	require.Equal(t, []string{"one two", "three"}, wrapText("one two three", 8))
	require.Equal(t, []string{
		"  Profile   auto",
		"  Server    alpha beta",
		"            gamma delta",
	}, kvWrapLines("  ", [][2]string{{"Profile", "auto"}, {"Empty", ""}, {"Server", "alpha beta gamma delta"}}, 24))
}
