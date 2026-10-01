package doctor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

func manyFindings(n int) []api.DoctorFinding {
	sevs := []string{SevInfo, SevWarn, SevError}
	out := make([]api.DoctorFinding, n)
	for i := range out {
		out[i] = api.DoctorFinding{
			Rule: fmt.Sprintf("R%02d", i%15+1), Severity: sevs[i%3],
			Message: fmt.Sprintf("problem number %d → details", i), Fix: fmt.Sprintf("do thing %d", i),
		}
	}
	return out
}

func lines(s string) []string { return strings.Split(strings.TrimSuffix(s, "\n"), "\n") }

func hubStatus() api.Status {
	return api.Status{
		Role: "hub", Version: "1.0.0", GeneratedAt: testNow,
		Tunnels: []api.TunnelInfo{{ID: "main", State: state.StateUp}, {ID: "games", State: state.StateDown}},
		Nodes:   []api.NodeInfo{{ID: "de-1", Online: true}, {ID: "nl-1"}},
	}
}

func TestSummaryHealthy(t *testing.T) {
	s := Summary(nil, hubStatus(), true, false)
	ls := lines(s)
	require.Len(t, ls, 3)
	require.Contains(t, ls[0], "DEYROUTE doctor: hub 1.0.0, 2026-09-30 10:00 UTC")
	require.Contains(t, ls[1], "Tunnels: 1/2 UP")
	require.Contains(t, ls[1], "Nodes: 1/2 online")
	require.Contains(t, ls[2], "OK")
	require.Contains(t, ls[2], "No problems found")
}

func TestSummaryLineLimit(t *testing.T) {
	for _, n := range []int{1, 5, 8, 9, 10, 30} {
		fs := manyFindings(n)
		s := Summary(fs, hubStatus(), true, false)
		ls := lines(s)
		require.LessOrEqual(t, len(ls), SummaryMaxLines, "n=%d", n)
		shown := 0
		for _, l := range ls {
			if strings.Contains(l, "problem number") {
				shown++
			}
		}
		if shown < n {
			require.Contains(t, ls[len(ls)-1], fmt.Sprintf("%d more finding(s)", n-shown), "n=%d", n)
		}
		// Most severe first.
		if n >= 3 {
			require.Contains(t, ls[3], "ERROR")
		}
	}
	// 30 findings: 10 errors, first shown finding is an error, counts line exact.
	s := Summary(manyFindings(30), hubStatus(), true, false)
	require.Contains(t, s, "Result: 10 ERROR, 10 WARN, 10 INFO")
}

func TestSummaryWordsAndColor(t *testing.T) {
	fs := []api.DoctorFinding{
		{Rule: "R10", Severity: SevInfo, Message: "udp", Fix: "none"},
		{Rule: "R02", Severity: SevError, Message: "down", Fix: "test"},
		{Rule: "R14", Severity: SevWarn, Message: "flap"},
	}
	plain := Summary(fs, hubStatus(), true, false)
	colored := Summary(fs, hubStatus(), true, true)
	require.NotContains(t, plain, "\x1b[")
	require.Contains(t, colored, "\x1b[", "labels are colored")
	for _, w := range []string{"ERROR", "WARN", "INFO"} {
		require.Contains(t, plain, w)
		require.Contains(t, colored, w, "never color only")
	}
	require.Equal(t, plain, StripANSI(colored))
	ls := lines(plain)
	require.Contains(t, ls[3], "✖ ERROR R02")
	require.Contains(t, ls[5], "WARN")
	require.Contains(t, ls[6], "INFO")
	require.Contains(t, ls[7], "Fix: none")
}

func TestSummaryASCII(t *testing.T) {
	st := hubStatus()
	st.Nodes[0].ID = "dé-1"
	fs := append(manyFindings(4), api.DoctorFinding{Rule: "R01", Severity: SevWarn, Message: "naïve…", Fix: "a · b"})
	s := Summary(fs, st, false, true)
	for _, r := range s {
		require.Less(t, r, rune(0x80), "non-ASCII rune %q in %q", r, s)
	}
	require.Contains(t, s, "x ERROR")
	require.Contains(t, s, "->")
	require.Contains(t, s, "...")
	ok := Summary(nil, st, false, false)
	require.Contains(t, ok, "+ OK")
}

func TestSummaryNodeRoleAndRedaction(t *testing.T) {
	secret := "summary-secret-abcdef-987"
	dlog.RegisterSecret(secret)
	st := api.Status{Role: "node", Version: "1.0.0", NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8:44433"}}
	s := Summary([]api.DoctorFinding{{Rule: "R05", Severity: SevWarn, Message: "uses " + secret}}, st, true, false)
	require.Contains(t, s, "Node de-1")
	require.Contains(t, s, "not connected")
	require.NotContains(t, s, secret)
	st.NodeSelf.Connected = true
	require.Contains(t, Summary(nil, st, true, false), "Hub 5.6.7.8:44433: connected")

	// Unknown fields do not break the header.
	require.Contains(t, Summary(nil, api.Status{}, true, false), "DEYROUTE doctor: ? ?, -")
}

func TestSortFindingsStable(t *testing.T) {
	in := []api.DoctorFinding{
		{Rule: "R11", Severity: SevInfo}, {Rule: "R02", Severity: SevError},
		{Rule: "R05", Severity: SevWarn}, {Rule: "R03", Severity: SevError}, {Rule: "R99", Severity: "weird"},
	}
	out := SortFindings(in)
	require.Equal(t, []string{"R02", "R03", "R05", "R11", "R99"}, rulesOf(out))
	require.Equal(t, "R11", in[0].Rule, "input untouched")
}
