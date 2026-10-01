package failover

import (
	"sort"
	"time"

	"github.com/localroot4/deyroute/internal/state"
)

// Reconcile matches a persisted tunnel state with the units that actually
// run after a hub restart (section 9, "بازیابی بعد از restart Hub"): the
// state is corrected, a healthy tunnel is never restarted. activeUnits maps
// candidate keys (state.Candidate.Key) to whether the candidate's unit is
// active (systemctl show). It is pure.
//
//   - The persisted active candidate runs: it is kept. A tunnel that was
//     UP/DEGRADED/PAUSED stays so; one that was mid-SWITCHING, STARTING or
//     DOWN becomes UP and its probes decide from there (no restart).
//   - Another candidate of the tunnel runs: it is adopted (same rules).
//   - Nothing runs: STARTING with the persisted active candidate (rung 1 of
//     the primary node when it is no longer valid), except DOWN, which stays
//     DOWN and retries when due.
//
// Units of candidates other than the returned active one are left to the
// caller (see StrayUnits).
func Reconcile(p state.TunnelState, activeUnits map[string]bool, t Tunnel) state.TunnelState {
	st := cloneState(p)
	st.ID = t.ID
	if st.Quarantine == nil {
		st.Quarantine = map[string]state.Quarantine{}
	}
	if st.Skipped == nil {
		st.Skipped = map[string]state.Skip{}
	}
	valid := func(c state.Candidate) bool { return validCandidate(t.Nodes, t.Ladder, c) }
	for k := range st.Quarantine {
		if c, ok := parseKey(k); !ok || !valid(c) {
			delete(st.Quarantine, k)
		}
	}
	st.Tried = nil
	if st.State == state.StatePaused {
		st.Paused = true
	}

	adopted := state.Candidate{}
	if valid(st.Active) && activeUnits[st.Active.Key()] {
		adopted = st.Active
	} else {
	search:
		for _, n := range t.Nodes {
			for _, r := range t.Ladder {
				c := state.Candidate{Node: n, Transport: r}
				if activeUnits[c.Key()] {
					adopted = c
					break search
				}
			}
		}
	}

	if !adopted.IsZero() {
		if adopted != st.Active {
			if valid(st.Active) {
				st.Previous = st.Active
			}
			st.Active = adopted
			st.StableSince = time.Time{}
			st.FailCount = 0
		}
		st.DownBackoff = 0
		st.DownRetryAt = time.Time{}
		switch {
		case st.Paused:
			st.State = state.StatePaused
		case st.State == state.StateUp || st.State == state.StateDegraded:
		default:
			st.State = state.StateUp
			st.StableSince = time.Time{}
			st.FailCount = 0
		}
		st.TransitionCause = "reconciled after hub restart: " + adopted.Key() + " is running"
		return st
	}

	if !valid(st.Active) || isSkipped(st.Skipped, st.Active) {
		h, _ := home(t.Nodes, t.Ladder, func(c state.Candidate) bool { return isSkipped(st.Skipped, c) })
		st.Active = h
	}
	st.StableSince = time.Time{}
	if st.State == state.StateDown && !st.Paused {
		st.TransitionCause = "reconciled after hub restart: tunnel down, nothing running"
		return st
	}
	st.State = state.StateStarting
	st.UpSince = time.Time{}
	st.FailCount = 0
	st.TransitionCause = "reconciled after hub restart: no unit running"
	return st
}

// StrayUnits returns the running candidates other than st.Active, sorted by
// key; the daemon stops them after Reconcile (only one transport may bind
// the user ports).
func StrayUnits(st state.TunnelState, activeUnits map[string]bool) []state.Candidate {
	keys := make([]string, 0, len(activeUnits))
	for k, on := range activeUnits {
		if on && k != st.Active.Key() {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]state.Candidate, 0, len(keys))
	for _, k := range keys {
		if c, ok := parseKey(k); ok {
			out = append(out, c)
		}
	}
	return out
}

func isSkipped(m map[string]state.Skip, c state.Candidate) bool {
	_, ok := m[c.Key()]
	return ok
}
