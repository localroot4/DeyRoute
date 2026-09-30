package failover

import (
	"testing"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/stretchr/testify/require"
)

// TestQuarantineDoublingAndExpiry: 10 minutes, doubling on repeat up to one
// hour; entries stop excluding at Until and are forgotten an hour later.
func TestQuarantineDoublingAndExpiry(t *testing.T) {
	q := map[string]state.Quarantine{}
	c := cand("de-1", "backhaul/wssmux")
	now := t0
	require.False(t, Quarantined(q, c, now))

	want := []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour}
	for i, w := range want {
		got := ApplyQuarantine(q, c, now, 0)
		require.Equal(t, w, got, "failure %d", i+1)
		require.True(t, Quarantined(q, c, now))
		require.True(t, Quarantined(q, c, now.Add(w-time.Second)))
		require.False(t, Quarantined(q, c, now.Add(w)), "expires by time")
		now = now.Add(w) // retried when the quarantine expired, failed again
	}

	// Expired but remembered: the next failure still doubles.
	PruneQuarantine(q, now.Add(30*time.Minute))
	require.Contains(t, q, c.Key())
	// Forgotten an hour after expiry: the next failure starts again at base.
	PruneQuarantine(q, now.Add(QuarantineForget))
	require.NotContains(t, q, c.Key())
	require.Equal(t, 10*time.Minute, ApplyQuarantine(q, c, now, 10*time.Minute))

	// A base above one hour is its own ceiling.
	q2 := map[string]state.Quarantine{}
	require.Equal(t, 2*time.Hour, ApplyQuarantine(q2, c, now, 2*time.Hour))
	require.Equal(t, 2*time.Hour, ApplyQuarantine(q2, c, now, 2*time.Hour))
}

func TestRTTHistory(t *testing.T) {
	var h rttHistory
	now := t0
	// No baseline yet: never high.
	high, med := h.add(now, time.Second)
	require.False(t, high)
	require.Zero(t, med)
	h.reset()

	// Ten minutes of 20ms samples every 5s.
	for i := 0; i < 120; i++ {
		now = now.Add(5 * time.Second)
		high, _ = h.add(now, 20*time.Millisecond)
		require.False(t, high)
	}
	// 70ms is above 3x20ms: high only after 60s.
	start := now.Add(5 * time.Second)
	for now.Add(5*time.Second).Sub(start) < RTTHighFor {
		now = now.Add(5 * time.Second)
		high, med = h.add(now, 70*time.Millisecond)
		require.False(t, high, "at %s", now.Sub(start))
		require.Equal(t, 20*time.Millisecond, med)
	}
	now = now.Add(5 * time.Second)
	high, _ = h.add(now, 70*time.Millisecond)
	require.True(t, high)
	require.True(t, h.high(now))
	// One normal sample ends the run.
	now = now.Add(5 * time.Second)
	high, _ = h.add(now, 20*time.Millisecond)
	require.False(t, high)
	require.False(t, h.high(now))

	// Even median of an even count.
	require.Equal(t, 15*time.Millisecond, medianOf([]rttSample{{rtt: 10 * time.Millisecond}, {rtt: 20 * time.Millisecond}}))

	// The sample cap holds.
	var big rttHistory
	for i := 0; i < maxRTTSamples+10; i++ {
		big.add(t0.Add(time.Duration(i)*time.Millisecond), time.Millisecond)
	}
	require.Len(t, big.samples, maxRTTSamples)
}

func TestFakeClock(t *testing.T) {
	clk := NewFakeClock(t0)
	require.Equal(t, t0, clk.Now())
	_, ok := clk.NextDeadline()
	require.False(t, ok)

	a := clk.NewTimer(2 * time.Second)
	b := clk.NewTimer(time.Second)
	c := clk.NewTimer(-time.Second) // fires at once
	require.Equal(t, 3, clk.Pending())
	next, ok := clk.NextDeadline()
	require.True(t, ok)
	require.Equal(t, t0, next)

	require.True(t, c.Stop())
	require.False(t, c.Stop())
	clk.Advance(1500 * time.Millisecond)
	require.Equal(t, t0.Add(time.Second), <-b.C())
	require.Equal(t, t0.Add(1500*time.Millisecond), clk.Now())
	require.Equal(t, 1, clk.Pending())
	require.False(t, b.Stop(), "already fired")
	clk.Advance(time.Second)
	require.Equal(t, t0.Add(2*time.Second), <-a.C())

	require.False(t, clk.BlockUntil(1, 10*time.Millisecond))
	go func() { clk.NewTimer(time.Minute) }()
	require.True(t, clk.BlockUntil(1, time.Second))

	var rc RealClock
	require.WithinDuration(t, time.Now(), rc.Now(), time.Second)
	rt := rc.NewTimer(time.Millisecond)
	<-rt.C()
	require.False(t, rt.Stop())
}

func TestReconcile(t *testing.T) {
	tun := twoNodes()
	r1 := cand("de-1", "backhaul/wssmux")
	r2 := cand("de-1", "backhaul/tcpmux")
	nl1 := cand("nl-1", "backhaul/wssmux")
	since := t0.Add(-time.Hour)

	cases := []struct {
		name       string
		persisted  state.TunnelState
		active     map[string]bool
		wantState  string
		wantActive state.Candidate
		keepStable bool
	}{
		{"up and running: kept as is", upOn(r2, since), map[string]bool{r2.Key(): true}, state.StateUp, r2, true},
		{"degraded and running: kept", state.TunnelState{State: state.StateDegraded, Active: r2, FailCount: 1},
			map[string]bool{r2.Key(): true}, state.StateDegraded, r2, false},
		{"mid-switching, candidate running: adopted as UP", state.TunnelState{State: state.StateSwitching, Active: r2, Previous: r1, Tried: []string{r1.Key(), r2.Key()}},
			map[string]bool{r2.Key(): true}, state.StateUp, r2, false},
		{"persisted candidate not running, another one is: adopted", state.TunnelState{State: state.StateSwitching, Active: r2, Previous: r1},
			map[string]bool{r1.Key(): true}, state.StateUp, r1, false},
		{"paused and running: stays paused", state.TunnelState{State: state.StatePaused, Paused: true, Active: nl1},
			map[string]bool{nl1.Key(): true}, state.StatePaused, nl1, false},
		{"down but a unit runs: adopted", state.TunnelState{State: state.StateDown, Active: r2, DownBackoff: time.Minute},
			map[string]bool{r2.Key(): true}, state.StateUp, r2, false},
		{"nothing running: start the persisted candidate", upOn(r2, since), nil, state.StateStarting, r2, false},
		{"nothing running, persisted candidate gone: rung 1", upOn(cand("de-1", "gone/x"), since), nil, state.StateStarting, r1, false},
		{"nothing running, init", state.TunnelState{}, map[string]bool{}, state.StateStarting, r1, false},
		{"down and nothing running: stays down", state.TunnelState{State: state.StateDown, Active: r2, DownBackoff: time.Minute},
			nil, state.StateDown, r2, false},
		{"unit of a removed node is not adopted", upOn(cand("xx-1", "backhaul/wssmux"), since),
			map[string]bool{"xx-1/backhaul/wssmux": true}, state.StateStarting, r1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Reconcile(tc.persisted, tc.active, tun)
			require.Equal(t, tc.wantState, got.State, got.TransitionCause)
			require.Equal(t, tc.wantActive, got.Active)
			require.Equal(t, "main", got.ID)
			require.Nil(t, got.Tried)
			require.NotEmpty(t, got.TransitionCause)
			if tc.keepStable {
				require.Equal(t, since, got.StableSince)
			}
			if got.State == state.StateUp && tc.persisted.State != state.StateUp {
				require.Zero(t, got.FailCount)
			}
		})
	}

	// Quarantine entries of removed candidates are dropped; the input is not
	// modified.
	p := upOn(r2, since)
	p.Quarantine = map[string]state.Quarantine{r1.Key(): {Until: t0}, "gone/x/y": {Until: t0}}
	got := Reconcile(p, map[string]bool{r2.Key(): true}, tun)
	require.Contains(t, got.Quarantine, r1.Key())
	require.NotContains(t, got.Quarantine, "gone/x/y")
	require.Contains(t, p.Quarantine, "gone/x/y")

	// A skipped persisted candidate is replaced by the first usable rung.
	p = state.TunnelState{State: state.StateUp, Active: r1, Skipped: map[string]state.Skip{r1.Key(): {Reason: "udp"}}}
	got = Reconcile(p, nil, tun)
	require.Equal(t, r2, got.Active)
	require.Equal(t, state.StateStarting, got.State)
}

func TestStrayUnits(t *testing.T) {
	st := state.TunnelState{Active: cand("de-1", "backhaul/wssmux")}
	got := StrayUnits(st, map[string]bool{
		"de-1/backhaul/wssmux": true,
		"nl-1/rathole/noise":   true,
		"de-1/backhaul/tcpmux": true,
		"de-1/frp/wss":         false,
		"garbage":              true,
	})
	require.Equal(t, []state.Candidate{cand("de-1", "backhaul/tcpmux"), cand("nl-1", "rathole/noise")}, got)
}

func TestSignificantChange(t *testing.T) {
	a := state.TunnelState{State: state.StateUp, LastRTTms: 10}
	b := a
	b.LastRTTms = 99
	b.RecoverCount = 7
	b.UpdatedAt = t0
	b.Quarantine = map[string]state.Quarantine{}
	require.False(t, significantChange(a, b))
	b.FailCount = 1
	require.True(t, significantChange(a, b))

	c := cloneState(state.TunnelState{SwitchTimes: []time.Time{t0}, Tried: []string{"x"},
		Quarantine: map[string]state.Quarantine{"a": {}}, Skipped: map[string]state.Skip{"b": {}}})
	d := cloneState(c)
	d.SwitchTimes[0] = t0.Add(time.Hour)
	d.Tried[0] = "y"
	d.Quarantine["z"] = state.Quarantine{}
	d.Skipped["z"] = state.Skip{}
	require.Equal(t, t0, c.SwitchTimes[0])
	require.Equal(t, "x", c.Tried[0])
	require.Len(t, c.Quarantine, 1)
	require.Len(t, c.Skipped, 1)
}

func TestEventMessages(t *testing.T) {
	a, b := cand("de-1", "backhaul/wssmux"), cand("nl-1", "rathole/noise")
	for typ, want := range map[string]string{
		state.EvTunnelUp:        "Tunnel main is up on nl-1/rathole/noise",
		state.EvTunnelDegraded:  "Tunnel main is degraded on nl-1/rathole/noise",
		state.EvSwitchTransport: "Tunnel main switched transport backhaul/wssmux -> rathole/noise on node nl-1",
		state.EvSwitchNode:      "Tunnel main switched node de-1 -> nl-1 (rathole/noise)",
		state.EvFailback:        "Tunnel main failed back to nl-1/rathole/noise",
		state.EvManualSwitch:    "Tunnel main switched to nl-1/rathole/noise by the owner",
		"other":                 "Tunnel main: other",
	} {
		require.Equal(t, want, eventMessage("main", evt{typ: typ, from: a, to: b}))
	}
	_ = config.DefaultLadder
}
