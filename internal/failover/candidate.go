package failover

import (
	"sort"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/state"
)

// NextCandidate chooses the candidate to try after cur (section 9, "انتخاب
// کاندید"). It is pure: nodes is the tunnel's ordered node list (primary
// first), ladder its resolved rungs, tried the candidate keys already tried
// in the current switching cycle and excluded reports candidates that must
// not be used now (quarantined or skipped). cur itself is never returned.
//
// Policies:
//
//   - transport_then_node (default, also used for unknown values): the rungs
//     after cur on cur's node, then the earlier rungs of that node not tried
//     in this cycle; when every rung of the node was tried, the other nodes
//     in list order (wrapping around to nodes before cur's), each from rung 1.
//   - transport_only: only the rungs of cur's node (the first node when cur's
//     node is not part of the tunnel), in the same order.
//   - node_only: the same rung on the other nodes, in list order after cur's
//     node, wrapping around (rung 1 when cur's rung is not in the ladder).
//
// A zero or unknown cur position starts at the beginning: with an unknown
// node, transport_then_node starts at rung 1 of the primary node; with an
// unknown rung, the node's rungs start at rung 1.
func NextCandidate(policy string, nodes, ladder []string, cur state.Candidate, tried map[string]bool, excluded func(state.Candidate) bool) (state.Candidate, bool) {
	if len(nodes) == 0 || len(ladder) == 0 {
		return state.Candidate{}, false
	}
	usable := func(c state.Candidate) bool {
		if c == cur || tried[c.Key()] {
			return false
		}
		return excluded == nil || !excluded(c)
	}
	ni := indexOf(nodes, cur.Node)
	ri := indexOf(ladder, cur.Transport)

	// rungsOn walks the rungs of node after position ri, wrapping around.
	rungsOn := func(node string, from int) (state.Candidate, bool) {
		for k := 1; k <= len(ladder); k++ {
			c := state.Candidate{Node: node, Transport: ladder[mod(from+k, len(ladder))]}
			if usable(c) {
				return c, true
			}
		}
		return state.Candidate{}, false
	}

	switch policy {
	case config.PolicyTransportOnly:
		node := cur.Node
		if ni < 0 {
			node = nodes[0]
		}
		return rungsOn(node, ri)
	case config.PolicyNodeOnly:
		rung := cur.Transport
		if ri < 0 {
			rung = ladder[0]
		}
		for k := 1; k <= len(nodes); k++ {
			c := state.Candidate{Node: nodes[mod(ni+k, len(nodes))], Transport: rung}
			if usable(c) {
				return c, true
			}
		}
		return state.Candidate{}, false
	default: // transport_then_node
		if ni >= 0 {
			if c, ok := rungsOn(nodes[ni], ri); ok {
				return c, true
			}
		}
		for k := 1; k <= len(nodes); k++ {
			idx := mod(ni+k, len(nodes))
			if idx == ni {
				continue
			}
			if c, ok := rungsOn(nodes[idx], -1); ok {
				return c, true
			}
		}
		return state.Candidate{}, false
	}
}

// home returns the failback/initial target: rung 1 of the primary node, or
// the first rung (primary node first) that exc does not exclude.
func home(nodes, ladder []string, exc func(state.Candidate) bool) (state.Candidate, bool) {
	return NextCandidate(config.PolicyTransportThenNode, nodes, ladder, state.Candidate{}, nil, exc)
}

// validCandidate reports whether c is part of the tunnel (node and rung).
func validCandidate(nodes, ladder []string, c state.Candidate) bool {
	return indexOf(nodes, c.Node) >= 0 && indexOf(ladder, c.Transport) >= 0
}

// nodeCandidates returns every candidate of node in ladder order.
func nodeCandidates(node string, ladder []string) []state.Candidate {
	out := make([]state.Candidate, 0, len(ladder))
	for _, r := range ladder {
		out = append(out, state.Candidate{Node: node, Transport: r})
	}
	return out
}

// parseKey splits a candidate key "node/backend/transport" (node ids never
// contain a slash).
func parseKey(key string) (state.Candidate, bool) {
	node, transport, ok := strings.Cut(key, "/")
	if !ok || node == "" || transport == "" {
		return state.Candidate{}, false
	}
	return state.Candidate{Node: node, Transport: transport}, true
}

// sortedKeys returns the keys of a set in lexical order.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func indexOf(list []string, s string) int {
	if s == "" {
		return -1
	}
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// mod is the non-negative remainder.
func mod(a, n int) int {
	r := a % n
	if r < 0 {
		r += n
	}
	return r
}
