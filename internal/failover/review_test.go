package failover

import (
	"context"
	"testing"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/stretchr/testify/require"
)

// Regression tests added by the review of the failover engine.

// A tunnel without nodes must not crash the hub on a manual switch.
func TestManualSwitchTransportWithoutNodes(t *testing.T) {
	tun := twoNodes()
	tun.Nodes = nil
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	require.Equal(t, state.StateDown, h.st().State)
	require.Equal(t, deyerr.F006, codeOf(h.eng.SwitchTransport(context.Background(), "backhaul/wssmux")))
	require.Equal(t, deyerr.F006, codeOf(h.eng.SwitchNode(context.Background(), "de-1")))
	require.Equal(t, deyerr.F006, codeOf(h.eng.Reset(context.Background())))
}

// Sections 8/9: direct/native is never quarantined, even when the tunnel's
// anti-flapping fallback is another transport.
func TestDirectNativeNeverQuarantinedWithCustomFallback(t *testing.T) {
	tun := oneNode()
	haproxy := cand("de-1", "direct/haproxy")
	tun.Ladder = []string{"backhaul/wssmux", "direct/native", "direct/haproxy"}
	tun.FallbackTransport = "direct/haproxy"
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
		f.blocked[haproxy.Key()] = true
	}).run()
	h.advanceUntil(2*time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	q := h.st().Quarantine
	require.Contains(t, q, r1.Key())
	require.NotContains(t, q, dn.Key(), "direct/native is never quarantined")
	require.NotContains(t, q, haproxy.Key(), "nor is the configured fallback")
}

// Section 9: node_service is checked before any transport switch, also when
// the very first start of the tunnel does not pass (STARTING → SWITCHING):
// the backup node is used at once, no primary rung is tried or quarantined.
func TestServiceDownAtStartGoesToBackupNode(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.svcDown["de-1"] = true }).run()
	took := h.advanceUntil(time.Minute, isUpOn(b1))
	require.Equal(t, StartWait, took)
	require.Equal(t, []string{"start " + r1.Key(), "stop " + r1.Key(), "start " + b1.Key()}, h.act.opSequence("start", "stop"))
	require.Empty(t, h.st().Quarantine, "a service outage does not quarantine transports")
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
	sd := h.act.eventsOf(state.EvServiceDown)
	require.Len(t, sd, 1)
	require.Equal(t, string(deyerr.F005), sd[0].Code)
	require.Len(t, h.act.eventsOf(state.EvSwitchNode), 1)
}

func TestServiceDownAtStartWithoutBackupHolds(t *testing.T) {
	h := newHarness(t, oneNode(), state.TunnelState{}, func(f *fakeActions) { f.svcDown["de-1"] = true }).run()
	h.advance(StartWait)
	s := h.st()
	require.Equal(t, state.StateDegraded, s.State)
	require.True(t, s.ServiceDown)
	require.Equal(t, r1, s.Active)
	require.GreaterOrEqual(t, s.FailCount, 3)
	h.advance(2 * time.Minute)
	require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"), "transport is not switched")
	require.Len(t, h.act.eventsOf(state.EvServiceDown), 1)
	require.Empty(t, h.st().Quarantine)

	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = false })
	h.advance(5 * time.Second)
	require.Equal(t, state.StateUp, h.st().State)
}

// A failback interrupted by an owner command is finished as a failback: the
// failback event, the delay reset, and one anti-flapping count only.
func TestInterruptedFailbackSucceedsAsFailback(t *testing.T) {
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blockedUntil[r1.Key()] = t0.Add(8 * time.Second) // rung 1 is slow to come up
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), twoNodes()) })))
	require.Equal(t, state.StateSwitching, h.st().State)
	h.advanceUntil(time.Minute, isUpOn(r1))
	require.Equal(t, []string{state.EvFailback}, h.act.eventTypes())
	s := h.st()
	require.Len(t, s.SwitchTimes, 1, "the failback is counted once")
	require.Equal(t, r2, s.Previous)
	require.Equal(t, 300*time.Second, s.FailbackDelay)
}

func TestInterruptedFailbackFailsRevertsAndDoubles(t *testing.T) {
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), twoNodes()) })))
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Equal(t, []string{state.EvFailbackFailed}, h.act.eventTypes())
	s := h.st()
	require.Equal(t, 600*time.Second, s.FailbackDelay, "the failback delay doubles")
	require.Len(t, s.SwitchTimes, 1)
	require.NotContains(t, s.Quarantine, r1.Key())
}

// The return to the previous candidate after a failed failback, interrupted
// and then finished: no extra event, not counted, Previous is rung 1.
func TestInterruptedRevertAfterFailedFailback(t *testing.T) {
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	h.act.set(func(f *fakeActions) { f.blockedUntil[r2.Key()] = f.clk.Now().Add(20 * time.Second) })
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r2 })
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), twoNodes()) })))
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Equal(t, []string{state.EvFailbackFailed}, h.act.eventTypes())
	s := h.st()
	require.Equal(t, r1, s.Previous)
	require.Len(t, s.SwitchTimes, 1)
	require.Equal(t, 600*time.Second, s.FailbackDelay)
}

func TestInterruptedRevertFailsSwitches(t *testing.T) {
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	h.act.block(r2.Key(), true)
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r2 })
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), twoNodes()) })))
	h.advanceUntil(2*time.Minute, isUpOn(r3))
	require.Empty(t, h.act.callsOf("start", r1.Key())[1:], "rung 1 that just failed is not retried in the cycle")
	require.True(t, Quarantined(h.st().Quarantine, r2, h.clk.Now()))
}

// The move to direct/native while flapping, interrupted and finished, is
// still not counted and emits no switch event.
func TestInterruptedFlappingMove(t *testing.T) {
	for _, pass := range []bool{true, false} {
		st := upOn(r1, t0)
		for i := 0; i < 6; i++ {
			st.SwitchTimes = append(st.SwitchTimes, t0.Add(-time.Minute))
		}
		h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
			f.running[r1.Key()] = true
			f.blocked[r1.Key()] = true
			if pass {
				f.blockedUntil[dn.Key()] = t0.Add(20 * time.Second)
			} else {
				f.blocked[dn.Key()] = true
			}
		}).run()
		h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == dn })
		require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), twoNodes()) })))
		h.advance(time.Minute)
		s := h.st()
		require.Equal(t, dn, s.Active)
		require.Len(t, s.SwitchTimes, 6, "not counted")
		require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
		require.Len(t, h.act.callsOf("start", ""), 1, "held on direct/native")
		if pass {
			require.Equal(t, state.StateUp, s.State)
		} else {
			require.Equal(t, state.StateDegraded, s.State)
			require.True(t, s.Flapping)
		}
		h.stop()
	}
}

// A DOWN retry interrupted by an owner command goes back to DOWN and
// retries; it does not re-enter DOWN (no second tunnel_down, backoff kept).
func TestInterruptedDownRetryReturnsToDown(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 }) // first retry under way
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), tun) })))
	h.advance(90 * time.Second)
	s := h.st()
	require.Equal(t, state.StateDown, s.State)
	require.Equal(t, time.Minute, s.DownBackoff, "the retry after the interruption doubled the backoff once")
	require.Len(t, h.act.eventsOf(state.EvTunnelDown), 1)
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))

	// And a retry interrupted while its candidate recovers reports tunnel_up.
	h.advanceUntil(3*time.Minute, func(s state.TunnelState) bool { return s.Active == r1 && s.DownBackoff == time.Minute })
	h.act.set(func(f *fakeActions) {
		f.blocked[r1.Key()] = false
		f.blockedUntil[r1.Key()] = f.clk.Now().Add(5 * time.Second)
	})
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(context.Background(), tun) })))
	h.advanceUntil(time.Minute, isUpOn(r1))
	up := h.act.eventsOf(state.EvTunnelUp)
	require.Len(t, up, 1, "the tunnel never came up before")
	require.Equal(t, dn.Transport, up[0].FromTransport, "from the candidate the tunnel went down on")
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
}

// Quarantine in the engine: an expired entry is tried again and a repeated
// failure doubles its duration (10 → 20 minutes).
func TestEngineQuarantineExpiryAndDoubling(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"a/1", "a/2", "a/3"}
	tun.Settings.Failback = false
	a1, a2, a3 := cand("de-1", "a/1"), cand("de-1", "a/2"), cand("de-1", "a/3")
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	h.act.block(a1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(a2))
	h.act.block(a2.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(a3))
	require.True(t, Quarantined(h.st().Quarantine, a1, h.clk.Now()))

	// Within the quarantine a failure of a/3 finds nothing usable: DOWN.
	h.act.block(a2.Key(), false)
	h.act.block(a3.Key(), true)
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	require.Len(t, h.act.callsOf("start", a1.Key()), 1, "a quarantined candidate is not tried by a switching cycle")
	// The DOWN retry ignores quarantine and recovers on a/2.
	h.advanceUntil(2*time.Minute, isUpOn(a2))

	// After expiry a/1 is tried again by a cycle; its second failure doubles.
	h.advance(10 * time.Minute)
	h.act.block(a2.Key(), true)
	h.act.block(a3.Key(), false)
	h.advanceUntil(2*time.Minute, isUpOn(a3))
	q := h.st().Quarantine
	require.Equal(t, 10*time.Minute, q[a2.Key()].Duration, "a/2 passed in between: a fresh 10 minutes")
	h.act.block(a3.Key(), true)
	h.act.block(a2.Key(), false)
	h.advanceUntil(2*time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown || isUpOn(a2)(s) })
	q = h.st().Quarantine
	require.Equal(t, 20*time.Minute, q[a1.Key()].Duration, "second failure of a/1 within the hour doubles")
}

// fail_threshold 1: UP goes straight to SWITCHING (no DEGRADED step), and
// the owner's probe_interval_s / probe_timeout_s / quarantine_s are used.
func TestCustomSettings(t *testing.T) {
	tun := twoNodes()
	tun.Settings.FailThreshold = 1
	tun.Settings.ProbeIntervalS = 2
	tun.Settings.ProbeTimeoutS = 1
	tun.Settings.QuarantineS = 120
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	h.act.clearCalls()
	h.advance(10 * time.Second)
	require.Len(t, h.act.callsOf("probe", r1.Key()), 5, "probe_interval_s 2")
	h.act.set(func(f *fakeActions) {
		for _, b := range f.probeBudgets[1:] { // the first is the start window's poll
			require.LessOrEqual(t, b, time.Second, "probe_timeout_s 1 is the probe ctx deadline")
			require.Greater(t, b, 500*time.Millisecond)
		}
	})

	h.act.block(r1.Key(), true)
	took := h.advanceUntil(time.Minute, isUpOn(r2))
	require.Equal(t, 2*time.Second, took, "one failure is enough with fail_threshold 1")
	require.Empty(t, h.act.eventsOf(state.EvTunnelDegraded), "UP → SWITCHING directly")
	require.Equal(t, 2*time.Minute, h.st().Quarantine[r1.Key()].Duration, "quarantine_s 120")
}

// A passing probe clears ServiceDown also while failover is paused.
func TestServiceDownClearedWhilePaused(t *testing.T) {
	h := newHarness(t, oneNode(), state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = true })
	h.advance(20 * time.Second)
	require.True(t, h.st().ServiceDown)
	require.NoError(t, h.eng.Pause(context.Background()))
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = false })
	h.advance(5 * time.Second)
	s := h.st()
	require.Equal(t, state.StatePaused, s.State)
	require.False(t, s.ServiceDown)
	require.Zero(t, s.FailCount)
}
