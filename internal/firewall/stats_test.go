package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// statsGoldenSpecs are the rendered accounting tables
// (testdata/stats_<name>.nft.golden); TestRealNFTStats loads each into the
// real nft.
var statsGoldenSpecs = map[string]StatsSpec{
	// One tunnel, one TCP port.
	"single_tcp": {Tunnels: []StatsTunnel{{ID: "main", TCP: []int{443}}}},
	// TCP and UDP on the same port, plus a second TCP port.
	"tcp_udp": {Tunnels: []StatsTunnel{{ID: "main", TCP: []int{2053, 443, 443}, UDP: []int{443}}}},
	// Several tunnels given out of order: tunnels, protocols and ports sort.
	"several": {Tunnels: []StatsTunnel{
		{ID: "web", TCP: []int{8443, 443}},
		{ID: "games", UDP: []int{27015, 27016}, TCP: []int{27015}},
		{ID: "dns", UDP: []int{53}},
	}},
	// A rebuild seeded with the last reading; foreign and unknown names in
	// the seed are ignored.
	"seeded": {
		Tunnels: []StatsTunnel{{ID: "main", TCP: []int{443}}, {ID: "games", UDP: []int{27015}}},
		Seed: map[string]Counter{
			"tun_main_in": {Packets: 1200, Bytes: 987654321}, "tun_main_out": {Packets: 3400, Bytes: 18446744073709551615},
			"tun_games_in": {Packets: 1, Bytes: 60}, "tun_gone_in": {Packets: 5, Bytes: 500}, "other": {Packets: 1, Bytes: 1},
		},
	},
	// Hyphenated ids, and a tunnel without ports (counters only).
	"hyphenated": {Tunnels: []StatsTunnel{{ID: "ir-de-1", TCP: []int{443}}, {ID: "no-ports"}, {ID: "x-2", UDP: []int{8443}}}},
	// A tunnel with a kernel-NAT rung adds the forward-hook chain.
	"nat": {Tunnels: []StatsTunnel{{ID: "wg", UDP: []int{51820}, NAT: true}, {ID: "main", TCP: []int{443}}}},
	// No tunnels: an empty table (ApplyStats removes it instead).
	"empty": {},
}

func TestRenderStatsGolden(t *testing.T) {
	for name, spec := range statsGoldenSpecs {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, spec.Validate())
			got := RenderStats(spec)
			path := filepath.Join("testdata", "stats_"+name+".nft.golden")
			if *update {
				require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "run: go test ./internal/firewall -run TestRenderStatsGolden -update")
			require.Equal(t, string(want), got)
			for i := 0; i < 20; i++ {
				require.Equal(t, got, RenderStats(spec), "byte-stable")
			}
		})
	}
}

// verdictRe finds a verdict statement in a rule.
var verdictRe = regexp.MustCompile(`\b(accept|drop|reject|jump|goto|queue|return|continue)\b`)

// natRe finds an address translation statement or a nat chain.
var natRe = regexp.MustCompile(`(^|\s)(snat|dnat|redirect|masquerade)(\s+(to|ip|ip6)\b|$)|type nat hook`)

// TestRenderStatsHasNoVerdict: the accounting table can never block or
// divert traffic. Only the chain policies say accept; no rule has a verdict.
func TestRenderStatsHasNoVerdict(t *testing.T) {
	for name, spec := range statsGoldenSpecs {
		out := RenderStats(spec)
		for _, line := range strings.Split(out, "\n") {
			l := strings.TrimSpace(line)
			if strings.HasPrefix(l, "#") {
				continue
			}
			if strings.HasPrefix(l, "type filter hook ") {
				require.True(t, strings.HasSuffix(l, "; policy accept;"), "%s: %s", name, l)
				l = strings.TrimSuffix(l, " policy accept;")
			}
			require.False(t, verdictRe.MatchString(l), "%s: verdict in %q", name, l)
			require.False(t, natRe.MatchString(l), "%s: translation in %q", name, l)
		}
		require.NotContains(t, out, "table "+TableRef+" ", name)
	}
}

func TestRenderStatsShape(t *testing.T) {
	out := RenderStats(statsGoldenSpecs["several"])
	// Counting runs after the filters, without conntrack, skipping lo.
	require.Contains(t, out, "\t\ttype filter hook input priority 300; policy accept;\n\t\tiif != \"lo\" counter name meta l4proto . th dport map @acct_in\n")
	require.Contains(t, out, "\t\ttype filter hook output priority 300; policy accept;\n\t\toif != \"lo\" counter name meta l4proto . th sport map @acct_out\n")
	require.NotContains(t, out, "ct ", "no conntrack without a NAT rung")
	require.NotContains(t, out, "fib ")
	require.NotContains(t, out, "prerouting")
	require.Contains(t, out, `elements = { tcp . 443 : "tun_web_in", tcp . 8443 : "tun_web_in", tcp . 27015 : "tun_games_in", udp . 53 : "tun_dns_in", udp . 27015 : "tun_games_in", udp . 27016 : "tun_games_in" }`)
	for _, c := range []string{"dns", "games", "web"} {
		require.Contains(t, out, "\tcounter tun_"+c+"_in {\n\t\tpackets 0 bytes 0\n\t}\n")
		require.Contains(t, out, "\tcounter tun_"+c+"_out {\n\t\tpackets 0 bytes 0\n\t}\n")
	}
	require.Less(t, strings.Index(out, "tun_dns_in {"), strings.Index(out, "tun_games_in {"))

	// The forward chain (and with it conntrack) only with a NAT rung.
	nat := RenderStats(statsGoldenSpecs["nat"])
	require.Contains(t, nat, "type filter hook forward priority 300; policy accept;\n"+
		"\t\tct status dnat ct direction original counter name meta l4proto . ct original proto-dst map @acct_in\n"+
		"\t\tct status dnat ct direction reply counter name meta l4proto . ct original proto-dst map @acct_out\n")

	// Seeds land in their counters only.
	seeded := RenderStats(statsGoldenSpecs["seeded"])
	require.Contains(t, seeded, "\tcounter tun_main_out {\n\t\tpackets 3400 bytes 18446744073709551615\n\t}\n")
	require.Contains(t, seeded, "\tcounter tun_games_out {\n\t\tpackets 0 bytes 0\n\t}\n")
	require.NotContains(t, seeded, "tun_gone")
	require.NotContains(t, seeded, "other")

	// No tunnels: no counters, maps or chains.
	require.Equal(t, statsScriptHeader+"table inet deyroute_stats {\n}\n", RenderStats(StatsSpec{}))
	// Tunnels without ports: counters, but nothing to count with.
	bare := RenderStats(StatsSpec{Tunnels: []StatsTunnel{{ID: "main"}}})
	require.Contains(t, bare, "counter tun_main_in")
	require.NotContains(t, bare, "chain")
	require.NotContains(t, bare, "map")
}

func TestStatsValidate(t *testing.T) {
	ok := StatsSpec{Tunnels: []StatsTunnel{{ID: "main", TCP: []int{443}, UDP: []int{443}}, {ID: "games", TCP: []int{444}}}}
	require.NoError(t, ok.Validate())
	// The same port on the same protocol within one tunnel is just a duplicate.
	require.NoError(t, StatsSpec{Tunnels: []StatsTunnel{{ID: "main", TCP: []int{443, 443}}}}.Validate())

	bad := StatsSpec{Tunnels: []StatsTunnel{
		{ID: "main", TCP: []int{443, 0}, UDP: []int{70000}},
		{ID: "web", TCP: []int{443}, UDP: []int{443}},
		{ID: "main"},
		{ID: "Bad_ID"},
		{ID: "x"},
	}}
	err := bad.Validate()
	require.Error(t, err)
	e := deyerr.As(err)
	require.Equal(t, deyerr.X061, e.Code)
	require.Contains(t, e.Why(), "nft was not run")
	for _, frag := range []string{
		"tunnel main: listen port 0/tcp out of range", "tunnel main: listen port 70000/udp out of range",
		"listen port 443/tcp belongs to tunnels main and web", "tunnel main listed twice",
		`tunnel id "Bad_ID" invalid`, `tunnel id "x" invalid`,
	} {
		require.Contains(t, e.Detail, frag)
	}
	require.NotContains(t, e.Detail, "443/udp", "udp 443 has a single owner")

	// What Validate reports is dropped when rendering: the first owner (by
	// id) keeps a shared port.
	out := RenderStats(bad)
	require.Contains(t, out, `tcp . 443 : "tun_main_in"`)
	require.NotContains(t, out, `tcp . 443 : "tun_web_in"`)
	require.Contains(t, out, `udp . 443 : "tun_web_in"`)
	require.NotContains(t, out, "Bad_ID")
	require.NotContains(t, out, "tun_x_")
	require.Equal(t, 1, strings.Count(out, "counter tun_main_in {"))
}

func TestCounterNames(t *testing.T) {
	require.Equal(t, "tun_ir-de-1_in", CounterIn("ir-de-1"))
	require.Equal(t, "tun_ir-de-1_out", CounterOut("ir-de-1"))
	m := map[string]Counter{"tun_main_in": {1, 2}, "tun_main_out": {3, 4}, "tun_half_in": {5, 6}}
	in, out, ok := TunnelCounters(m, "main")
	require.True(t, ok)
	require.Equal(t, Counter{1, 2}, in)
	require.Equal(t, Counter{3, 4}, out)
	_, _, ok = TunnelCounters(m, "half")
	require.False(t, ok)
	_, _, ok = TunnelCounters(m, "gone")
	require.False(t, ok)
}

func TestParseCounters(t *testing.T) {
	m, err := ParseCounters(fixture(t, "nft_counters.txt"))
	require.NoError(t, err)
	require.Equal(t, map[string]Counter{
		"tun_games_in":    {Packets: 12, Bytes: 3456},
		"tun_games_out":   {},
		"tun_ir-de-1_in":  {Packets: 1048576, Bytes: 18446744073709551615},
		"tun_ir-de-1_out": {Packets: 7, Bytes: 1400},
	}, m, "foreign counters are ignored")

	// What RenderStats writes reads back as its seed.
	seeded := statsGoldenSpecs["seeded"]
	_, err = ParseCounters(RenderStats(StatsSpec{Tunnels: seeded.Tunnels, Seed: seeded.Seed}))
	require.Error(t, err, "a full table has maps and chains, not just counters")
	listing := "table inet deyroute_stats {\n"
	for _, n := range []string{"tun_games_in", "tun_games_out", "tun_main_in", "tun_main_out"} {
		c := seeded.Seed[n]
		listing += "\tcounter " + n + " {\n\t\tpackets " + strconv.FormatUint(c.Packets, 10) + " bytes " + strconv.FormatUint(c.Bytes, 10) + "\n\t}\n"
	}
	m, err = ParseCounters(listing + "}\n")
	require.NoError(t, err)
	require.Len(t, m, 4)
	require.Equal(t, seeded.Seed["tun_main_out"], m["tun_main_out"])

	// Empty table, and no output at all.
	for _, empty := range []string{"table inet deyroute_stats {\n}\n", "", "\n"} {
		m, err = ParseCounters(empty)
		require.NoError(t, err)
		require.Empty(t, m)
		require.NotNil(t, m)
	}

	// Garbage is DEY-X062 naming the line.
	for _, garbage := range []string{
		"warning: something\n",
		"table inet other {\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tpackets x bytes 1\n\t}\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tpackets -1 bytes 1\n\t}\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tpackets 1 bytes 99999999999999999999\n\t}\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tbytes 1 packets 1\n\t}\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t}\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tpackets 1 bytes 1\n",
		"table inet deyroute_stats {\n\tset s {\n\t}\n}\n",
		"table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tpackets 1 bytes 1\n\t}\n}\nhello\n",
	} {
		_, err := ParseCounters(garbage)
		require.Error(t, err, garbage)
		e := deyerr.As(err)
		require.Equal(t, deyerr.X062, e.Code, garbage)
		require.Contains(t, e.Why(), "unexpected nft output on line", garbage)
	}
}

func TestApplyStats(t *testing.T) {
	ctx := context.Background()
	spec := statsGoldenSpecs["seeded"]
	f := exec.NewFake().On("nft -f -")
	require.NoError(t, ApplyStats(ctx, f, spec))
	calls := f.Calls()
	require.Len(t, calls, 1, "one nft run")
	require.Equal(t, []string{"-f", "-"}, calls[0].Args)
	script := string(calls[0].Stdin)
	require.Equal(t, StatsScript(spec), script)
	require.True(t, strings.HasPrefix(script, "table inet deyroute_stats {}\ndelete table inet deyroute_stats\n"), "create, delete, recreate in one transaction")
	require.True(t, strings.HasSuffix(script, RenderStats(spec)))
	require.NotContains(t, script, "table inet deyroute {", "the firewall table is not touched")

	// No tunnels: the table is removed instead.
	f = exec.NewFake().On("nft delete table inet deyroute_stats")
	require.NoError(t, ApplyStats(ctx, f, StatsSpec{}))
	require.Equal(t, []string{"nft delete table inet deyroute_stats"}, f.Lines())

	// Invalid spec: nothing runs.
	f = exec.NewFake()
	err := ApplyStats(ctx, f, StatsSpec{Tunnels: []StatsTunnel{{ID: "a", TCP: []int{443}}}})
	require.Equal(t, deyerr.X061, deyerr.As(err).Code)
	require.Empty(t, f.Calls())

	// nft refuses the table: X061 → X005 → X007, stderr as detail.
	stderr := "Error: Could not process rule: Operation not supported"
	f = exec.NewFake().On("nft -f -", exec.Fail(1, stderr))
	err = ApplyStats(ctx, f, spec)
	e := deyerr.As(err)
	require.Equal(t, deyerr.X061, e.Code)
	require.True(t, deyerr.HasCode(err, deyerr.X005))
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	require.Equal(t, stderr, e.Detail)
	require.Contains(t, e.Why(), "nft refused table inet deyroute_stats: "+stderr)

	// nft missing.
	f = exec.NewFake().On("nft -f -", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	err = ApplyStats(ctx, f, spec)
	require.Equal(t, deyerr.X061, deyerr.As(err).Code)
	require.True(t, deyerr.HasCode(err, deyerr.X030))
	require.Contains(t, deyerr.As(err).Why(), "nft is not installed")
}

func TestRemoveStats(t *testing.T) {
	ctx := context.Background()
	f := exec.NewFake().On("nft delete table inet deyroute_stats")
	require.NoError(t, RemoveStats(ctx, f))
	require.Equal(t, []string{"nft delete table inet deyroute_stats"}, f.Lines(), "only the accounting table")

	f = exec.NewFake().On("nft delete table inet deyroute_stats", exec.Fail(1, "Error: Could not process rule: No such file or directory"))
	require.NoError(t, RemoveStats(ctx, f), "idempotent")
	f = exec.NewFake().On("nft delete table inet deyroute_stats", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	require.NoError(t, RemoveStats(ctx, f), "no nft, no table")

	f = exec.NewFake().On("nft delete table inet deyroute_stats", exec.Fail(1, "Error: Operation not permitted"))
	err := RemoveStats(ctx, f)
	require.True(t, deyerr.HasCode(err, deyerr.X005))
	require.Contains(t, deyerr.As(err).Detail, "Operation not permitted")
}

func TestReadCounters(t *testing.T) {
	ctx := context.Background()
	const line = "nft list counters table inet deyroute_stats"
	f := exec.NewFake().On(line, exec.OK(fixture(t, "nft_counters.txt")))
	m, err := ReadCounters(ctx, f)
	require.NoError(t, err)
	require.Len(t, m, 4)
	require.Equal(t, Counter{Packets: 12, Bytes: 3456}, m["tun_games_in"])
	require.Equal(t, []string{line}, f.Lines(), "exactly one nft run per read")

	// Missing table: empty map and the typed error.
	f = exec.NewFake().On(line, exec.Fail(1, "Error: No such file or directory\nlist counters table inet deyroute_stats\n                         ^^^^^^^^^^^^^^^"))
	m, err = ReadCounters(ctx, f)
	require.ErrorIs(t, err, ErrNoStatsTable)
	require.Equal(t, deyerr.X062, deyerr.As(err).Code)
	require.NotNil(t, m)
	require.Empty(t, m)

	// Other failures: X062, never the typed error.
	f = exec.NewFake().On(line, exec.Fail(1, "Error: Operation not permitted"))
	m, err = ReadCounters(ctx, f)
	require.Nil(t, m)
	require.Equal(t, deyerr.X062, deyerr.As(err).Code)
	require.False(t, errors.Is(err, ErrNoStatsTable))
	require.Contains(t, deyerr.As(err).Why(), "Operation not permitted")
	f = exec.NewFake().On(line, exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	_, err = ReadCounters(ctx, f)
	require.Equal(t, deyerr.X062, deyerr.As(err).Code)
	require.True(t, deyerr.HasCode(err, deyerr.X030))

	// Unparsable output.
	f = exec.NewFake().On(line, exec.OK("table inet deyroute_stats {\n\tcounter tun_a_in {\n\t\tpackets lots\n\t}\n}\n"))
	_, err = ReadCounters(ctx, f)
	require.Equal(t, deyerr.X062, deyerr.As(err).Code)
	require.Contains(t, deyerr.As(err).Detail, "packets lots")
}

func TestStatsSupport(t *testing.T) {
	ctx := context.Background()
	f := exec.NewFake().On("nft list tables", exec.OK("table inet deyroute\n"))
	require.NoError(t, StatsSupport(ctx, f))
	require.True(t, StatsAvailable(ctx, f))

	f = exec.NewFake().On("nft list tables", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	err := StatsSupport(ctx, f)
	require.Equal(t, deyerr.X061, deyerr.As(err).Code)
	require.Contains(t, deyerr.As(err).Why(), "nft is not installed")
	require.False(t, StatsAvailable(ctx, f))

	f = exec.NewFake().On("nft list tables", exec.Fail(1, "netlink: Error: cache initialization failed: Operation not permitted"))
	err = StatsSupport(ctx, f)
	require.Equal(t, deyerr.X061, deyerr.As(err).Code)
	require.Contains(t, deyerr.As(err).Why(), "nftables does not work on this host (nft list tables failed): netlink: Error: cache initialization failed: Operation not permitted")
	require.False(t, StatsAvailable(ctx, f))
}
