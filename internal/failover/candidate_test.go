package failover

import (
	"testing"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/stretchr/testify/require"
)

// TestNextCandidateDecisionTable is the mandatory decision table of section
// 17: policy × position × tried/quarantined/skipped.
func TestNextCandidateDecisionTable(t *testing.T) {
	nodes := []string{"a", "b", "c"}
	ladder := []string{"r/1", "r/2", "r/3", "direct/native"}
	const (
		ttn = config.PolicyTransportThenNode
		to  = config.PolicyTransportOnly
		no  = config.PolicyNodeOnly
	)
	all := func(node string) []string {
		out := []string{}
		for _, r := range ladder {
			out = append(out, node+"/"+r)
		}
		return out
	}
	join := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	cases := []struct {
		name     string
		policy   string
		cur      state.Candidate
		tried    []string
		excluded []string // quarantined or skipped
		want     string   // "" = none
	}{
		// transport_then_node
		{"ttn first rung → second", ttn, cand("a", "r/1"), nil, nil, "a/r/2"},
		{"ttn middle → next", ttn, cand("a", "r/2"), []string{"a/r/1"}, nil, "a/r/3"},
		{"ttn next quarantined → skip over it", ttn, cand("a", "r/1"), nil, []string{"a/r/2"}, "a/r/3"},
		{"ttn next skipped, then tried → last rung", ttn, cand("a", "r/1"), []string{"a/r/3"}, []string{"a/r/2"}, "a/direct/native"},
		{"ttn last rung wraps to untried earlier rung", ttn, cand("a", "direct/native"), nil, []string{"a/r/2"}, "a/r/1"},
		{"ttn node exhausted → next node rung 1", ttn, cand("a", "direct/native"), all("a"), nil, "b/r/1"},
		{"ttn node exhausted, next rung 1 quarantined", ttn, cand("a", "r/3"), all("a"), []string{"b/r/1"}, "b/r/2"},
		{"ttn node exhausted, rest of node excluded", ttn, cand("a", "r/1"), []string{"a/r/2"}, []string{"a/r/3", "a/direct/native"}, "b/r/1"},
		{"ttn on backup exhausted → third node", ttn, cand("b", "r/2"), all("b"), nil, "c/r/1"},
		{"ttn last node exhausted wraps to primary", ttn, cand("c", "r/1"), join(all("b"), all("c")), nil, "a/r/1"},
		{"ttn every candidate tried → none", ttn, cand("a", "r/1"), join(all("a"), all("b"), all("c")), nil, ""},
		{"ttn everything excluded → none", ttn, cand("a", "r/1"), nil, join(all("a"), all("b"), all("c")), ""},
		{"ttn zero position → primary rung 1", ttn, state.Candidate{}, nil, nil, "a/r/1"},
		{"ttn zero position, rung 1 skipped", ttn, state.Candidate{}, nil, []string{"a/r/1"}, "a/r/2"},
		{"ttn unknown node → primary rung 1", ttn, cand("x", "r/2"), nil, nil, "a/r/1"},
		{"ttn unknown rung → node rung 1", ttn, cand("b", "gone/x"), nil, nil, "b/r/1"},
		{"unknown policy behaves as ttn", "bogus", cand("a", "direct/native"), all("a"), nil, "b/r/1"},

		// transport_only
		{"to first → second", to, cand("a", "r/1"), nil, nil, "a/r/2"},
		{"to quarantined skipped", to, cand("a", "r/1"), nil, []string{"a/r/2"}, "a/r/3"},
		{"to last wraps on same node", to, cand("a", "direct/native"), nil, nil, "a/r/1"},
		{"to node exhausted → none (never next node)", to, cand("a", "direct/native"), all("a"), nil, ""},
		{"to stays on backup node", to, cand("b", "r/2"), nil, nil, "b/r/3"},
		{"to unknown node uses primary", to, cand("x", "r/1"), nil, nil, "a/r/2"},
		{"to no rung → node rung 1", to, state.Candidate{Node: "b"}, nil, nil, "b/r/1"},
		{"to everything excluded → none", to, cand("a", "r/1"), nil, all("a"), ""},

		// node_only
		{"no primary → backup same rung", no, cand("a", "r/1"), nil, nil, "b/r/1"},
		{"no backup quarantined → third node", no, cand("a", "r/2"), nil, []string{"b/r/2"}, "c/r/2"},
		{"no wraps to primary", no, cand("c", "r/2"), nil, nil, "a/r/2"},
		{"no skipped on next node", no, cand("b", "r/3"), nil, []string{"c/r/3"}, "a/r/3"},
		{"no all nodes tried → none", no, cand("a", "r/1"), []string{"b/r/1", "c/r/1"}, nil, ""},
		{"no never changes rung", no, cand("a", "r/1"), nil, []string{"b/r/1", "c/r/1"}, ""},
		{"no unknown rung → rung 1", no, cand("a", "gone/x"), nil, nil, "b/r/1"},
		{"no zero position → primary", no, state.Candidate{Transport: "r/3"}, nil, nil, "a/r/3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tried := map[string]bool{}
			for _, k := range tc.tried {
				tried[k] = true
			}
			ex := map[string]bool{}
			for _, k := range tc.excluded {
				ex[k] = true
			}
			got, ok := NextCandidate(tc.policy, nodes, ladder, tc.cur, tried, func(c state.Candidate) bool { return ex[c.Key()] })
			if tc.want == "" {
				require.False(t, ok, "got %s", got.Key())
				return
			}
			require.True(t, ok)
			require.Equal(t, tc.want, got.Key())
			require.NotEqual(t, tc.cur, got, "never returns the current candidate")
		})
	}
}

func TestNextCandidateEdges(t *testing.T) {
	_, ok := NextCandidate(config.PolicyTransportThenNode, nil, []string{"r/1"}, state.Candidate{}, nil, nil)
	require.False(t, ok)
	_, ok = NextCandidate(config.PolicyNodeOnly, []string{"a"}, nil, state.Candidate{}, nil, nil)
	require.False(t, ok)
	// nil excluded and nil tried are allowed.
	c, ok := NextCandidate(config.PolicyTransportOnly, []string{"a"}, []string{"r/1", "r/2"}, cand("a", "r/1"), nil, nil)
	require.True(t, ok)
	require.Equal(t, "a/r/2", c.Key())
	// A single-candidate tunnel has nothing after the current one.
	for _, p := range []string{config.PolicyTransportThenNode, config.PolicyTransportOnly, config.PolicyNodeOnly} {
		_, ok = NextCandidate(p, []string{"a"}, []string{"r/1"}, cand("a", "r/1"), nil, nil)
		require.False(t, ok, p)
	}
}

// TestNextCandidateCycleVisitsAll walks a whole switching cycle and checks
// the visiting order of each policy.
func TestNextCandidateCycleVisitsAll(t *testing.T) {
	nodes := []string{"a", "b"}
	ladder := []string{"r/1", "r/2", "r/3"}
	walk := func(policy string, start state.Candidate) []string {
		tried := map[string]bool{start.Key(): true}
		out := []string{}
		cur := start
		for {
			c, ok := NextCandidate(policy, nodes, ladder, cur, tried, nil)
			if !ok {
				return out
			}
			tried[c.Key()] = true
			out = append(out, c.Key())
			cur = c
		}
	}
	require.Equal(t, []string{"a/r/2", "a/r/3", "b/r/1", "b/r/2", "b/r/3"}, walk(config.PolicyTransportThenNode, cand("a", "r/1")))
	require.Equal(t, []string{"a/r/3", "a/r/1", "b/r/1", "b/r/2", "b/r/3"}, walk(config.PolicyTransportThenNode, cand("a", "r/2")))
	require.Equal(t, []string{"a/r/2", "a/r/3"}, walk(config.PolicyTransportOnly, cand("a", "r/1")))
	require.Equal(t, []string{"b/r/2"}, walk(config.PolicyNodeOnly, cand("a", "r/2")))
}

func TestHelpers(t *testing.T) {
	c, ok := parseKey("de-1/backhaul/wssmux")
	require.True(t, ok)
	require.Equal(t, cand("de-1", "backhaul/wssmux"), c)
	for _, bad := range []string{"", "de-1", "/x", "de-1/"} {
		_, ok := parseKey(bad)
		require.False(t, ok, bad)
	}
	require.Equal(t, []string{"a", "b"}, sortedKeys(map[string]bool{"b": true, "a": true, "c": false}))
	require.Equal(t, 2, mod(-1, 3))
	require.Equal(t, -1, indexOf([]string{"a"}, ""))
	h, ok := home([]string{"a", "b"}, []string{"r/1", "r/2"}, func(c state.Candidate) bool { return c.Node == "a" })
	require.True(t, ok)
	require.Equal(t, "b/r/1", h.Key())
	require.Len(t, nodeCandidates("a", []string{"r/1", "r/2"}), 2)
	require.True(t, validCandidate([]string{"a"}, []string{"r/1"}, cand("a", "r/1")))
	require.False(t, validCandidate([]string{"a"}, []string{"r/1"}, cand("b", "r/1")))
}

func TestEffectiveSettings(t *testing.T) {
	s := effective(config.Failover{})
	require.Equal(t, config.PolicyTransportThenNode, s.policy)
	require.Equal(t, 5*time.Second, s.probeInterval)
	require.Equal(t, 3*time.Second, s.probeTimeout)
	require.Equal(t, 3, s.failThreshold)
	require.Equal(t, 6, s.recoverThreshold)
	require.Equal(t, 6, s.maxSwitches)
	require.Equal(t, 10*time.Minute, s.quarantine)
	require.Equal(t, 300*time.Second, s.failbackAfter)
	require.False(t, s.failback, "failback comes from the config as is (ApplyDefaults sets true)")

	s = effective(config.Failover{Policy: config.PolicyNodeOnly, ProbeIntervalS: 2, FailThreshold: -1, QuarantineS: 60})
	require.Equal(t, config.PolicyNodeOnly, s.policy)
	require.Equal(t, 2*time.Second, s.probeInterval)
	require.Equal(t, 3, s.failThreshold)
	require.Equal(t, time.Minute, s.quarantine)
}
