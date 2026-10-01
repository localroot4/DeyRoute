package failover

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/stretchr/testify/require"
)

var (
	r1 = cand("de-1", "backhaul/wssmux")
	r2 = cand("de-1", "backhaul/tcpmux")
	r3 = cand("de-1", "rathole/noise")
	dn = cand("de-1", "direct/native")
	b1 = cand("nl-1", "backhaul/wssmux")
	b2 = cand("nl-1", "backhaul/tcpmux")
)

func isUpOn(c state.Candidate) func(state.TunnelState) bool {
	return func(s state.TunnelState) bool { return s.State == state.StateUp && s.Active == c }
}

// ---- INIT / STARTING

func TestInitStartsRungOneAndGoesUp(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.requireActive(r1, state.StateUp)
	require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"))
	require.Equal(t, []string{state.StateInit, state.StateStarting, state.StateUp}, h.act.persistedStates())
	ups := h.act.eventsOf(state.EvTunnelUp)
	require.Len(t, ups, 1)
	require.Equal(t, "main", ups[0].Tunnel)
	require.Equal(t, "de-1", ups[0].Node)
	require.Equal(t, r1.Transport, ups[0].ToTransport)
	require.Equal(t, t0, ups[0].At)
	s := h.st()
	require.Equal(t, t0, s.UpSince)
	require.Equal(t, t0, s.StableSince)
	require.Equal(t, 20, s.LastRTTms)
	require.Equal(t, 300*time.Second, s.FailbackDelay)

	// Steady UP: one probe per interval, nothing else.
	h.act.clearCalls()
	h.advance(time.Minute)
	require.Len(t, h.act.callsOf("probe", r1.Key()), 12)
	require.Empty(t, h.act.callsOf("start", ""))
	require.Equal(t, state.StateUp, h.st().State)
}

func TestInitUsesPersistedActive(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{State: state.StateInit, Active: r3}, nil).run()
	h.requireActive(r3, state.StateUp)
	require.Equal(t, []string{"start " + r3.Key()}, h.act.opSequence("start"))
}

func TestInitSkipsSkippedRungOne(t *testing.T) {
	st := state.TunnelState{Skipped: map[string]state.Skip{r1.Key(): {Reason: "validate failed", Code: string(deyerr.B006)}}}
	h := newHarness(t, twoNodes(), st, nil).run()
	h.requireActive(r2, state.StateUp)
}

func TestInitNoUsableCandidateGoesDown(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"hysteria2/udp"}
	st := state.TunnelState{Skipped: map[string]state.Skip{"de-1/hysteria2/udp": {Reason: "udp blocked"}}}
	h := newHarness(t, tun, st, nil).run()
	require.Equal(t, state.StateDown, h.st().State)
	down := h.act.eventsOf(state.EvTunnelDown)
	require.Len(t, down, 1)
	require.Equal(t, string(deyerr.F001), down[0].Code)
	require.Empty(t, h.act.callsOf("start", ""))
}

func TestStartingFailsThenSwitches(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r1.Key()] = true }).run()
	require.Equal(t, state.StateStarting, h.st().State)
	took := h.advanceUntil(time.Minute, isUpOn(r2))
	require.Equal(t, StartWait, took, "STARTING waits 15s for the first probe")
	require.Len(t, h.act.callsOf("probe", r1.Key()), 16, "polled every second")
	require.Equal(t, []string{"start " + r1.Key(), "stop " + r1.Key(), "start " + r2.Key()}, h.act.opSequence("start", "stop"))
	s := h.st()
	require.True(t, Quarantined(s.Quarantine, r1, h.clk.Now()))
	require.Equal(t, 10*time.Minute, s.Quarantine[r1.Key()].Duration)
	require.Len(t, h.act.eventsOf(state.EvProbeError), 1)
	require.Equal(t, string(deyerr.B004), h.act.eventsOf(state.EvProbeError)[0].Code)
	sw := h.act.eventsOf(state.EvSwitchTransport)
	require.Len(t, sw, 1)
	require.Equal(t, r1.Transport, sw[0].FromTransport)
	require.Equal(t, r2.Transport, sw[0].ToTransport)
	require.Contains(t, h.act.persistedStates(), state.StateSwitching)
}

func TestStartErrorCountsAsFailure(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) {
		f.startErr[r1.Key()] = deyerr.New(deyerr.B003, deyerr.Params{"unit": "deyroute-tun@main.de-1.backhaul-wssmux.service"})
	}).run()
	h.requireActive(r2, state.StateUp)
	pe := h.act.eventsOf(state.EvProbeError)
	require.Len(t, pe, 1)
	require.Equal(t, string(deyerr.B003), pe[0].Code)
	require.Contains(t, pe[0].Message, "deyroute-tun@main.de-1.backhaul-wssmux.service")
	require.Equal(t, []string{"start " + r1.Key(), "stop " + r1.Key(), "start " + r2.Key()}, h.act.opSequence("start", "stop"))
}

// ---- UP / DEGRADED

func TestDegradedAndBack(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.block(r1.Key(), true)
	h.advance(5 * time.Second)
	s := h.st()
	require.Equal(t, state.StateDegraded, s.State)
	require.Equal(t, 1, s.FailCount)
	require.Equal(t, "i/o timeout", s.LastProbeErr)
	require.True(t, s.StableSince.IsZero())
	h.advance(5 * time.Second)
	require.Equal(t, 2, h.st().FailCount)
	require.Len(t, h.act.eventsOf(state.EvTunnelDegraded), 1, "one event per transition")

	h.act.block(r1.Key(), false)
	h.advance(5 * time.Second)
	s = h.st()
	require.Equal(t, state.StateUp, s.State)
	require.Zero(t, s.FailCount)
	require.Equal(t, h.clk.Now(), s.StableSince)
	require.Equal(t, t0, s.UpSince, "UP since is not reset by a DEGRADED blip")
	require.Len(t, h.act.eventsOf(state.EvTunnelUp), 2)
	require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"))
}

func TestDegradedByRTT(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.advance(11 * time.Minute)
	h.act.set(func(f *fakeActions) { f.rtt[r1.Key()] = 100 * time.Millisecond })
	h.advance(55 * time.Second)
	require.Equal(t, state.StateUp, h.st().State, "not yet 60s")
	h.advance(10 * time.Second)
	s := h.st()
	require.Equal(t, state.StateDegraded, s.State)
	require.Contains(t, s.TransitionCause, "rtt 100ms above 3x the 10-minute median 20ms")
	require.Equal(t, 100, s.LastRTTms)
	h.advance(time.Minute)
	require.Equal(t, state.StateDegraded, h.st().State, "RTT degradation never switches")
	require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"))
	h.act.set(func(f *fakeActions) { f.rtt[r1.Key()] = 20 * time.Millisecond })
	h.advance(5 * time.Second)
	require.Equal(t, state.StateUp, h.st().State)
}

// Acceptance (phase 5, S08): the active rung is blocked → UP on the next
// rung within 35s with the default thresholds.
func TestAcceptanceBlockedRungSwitchesWithin35s(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.advance(2 * time.Minute)
	h.act.block(r1.Key(), true)
	took := h.advanceUntil(time.Minute, isUpOn(r2))
	require.LessOrEqual(t, took, 35*time.Second)
	require.Equal(t, 15*time.Second, took, "three failed probes, then the next rung passes at once")
	require.Equal(t, []string{state.EvTunnelUp, state.EvTunnelDegraded, state.EvSwitchTransport}, h.act.eventTypes())
	sw := h.act.eventsOf(state.EvSwitchTransport)
	require.Len(t, sw, 1)
	require.Equal(t, state.LevelWarn, sw[0].Level)
	require.Equal(t, "i/o timeout", sw[0].Reason)
	require.Empty(t, h.act.eventsOf(state.EvSwitchNode))
	s := h.st()
	require.Equal(t, r1, s.Previous)
	require.Len(t, s.SwitchTimes, 1)
	require.True(t, Quarantined(s.Quarantine, r1, h.clk.Now()), "the blocked rung is quarantined")
	require.Nil(t, s.Tried)
	require.Equal(t, map[string]bool{r2.Key(): true}, h.act.runningKeys(), "only one transport runs")
}

// Acceptance (S09): unblocking → failback to rung 1 within failback_after_s+20s.
func TestAcceptanceFailbackAfterDelay(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r2))
	h.act.block(r1.Key(), false)
	took := h.advanceUntil(10*time.Minute, isUpOn(r1))
	require.LessOrEqual(t, took, 300*time.Second+20*time.Second)
	require.GreaterOrEqual(t, took, 300*time.Second, "blind failback waits failback_after_s")
	fb := h.act.eventsOf(state.EvFailback)
	require.Len(t, fb, 1)
	require.Equal(t, r2.Transport, fb[0].FromTransport)
	require.Equal(t, r1.Transport, fb[0].ToTransport)
	s := h.st()
	require.Equal(t, 300*time.Second, s.FailbackDelay)
	require.NotContains(t, s.Quarantine, r1.Key(), "a passing candidate leaves quarantine")
	require.Len(t, s.SwitchTimes, 2, "failback is an automatic switch")
}

func TestFailbackAlwaysToRungOne(t *testing.T) {
	st := upOn(r3, t0)
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) { f.running[r3.Key()] = true }).run()
	h.advanceUntil(6*time.Minute, isUpOn(r1))
	require.Equal(t, []string{"stop " + r3.Key(), "start " + r1.Key()}, h.act.opSequence("start", "stop"))
}

// Acceptance (S10): failback fails → back to the previous rung at once and
// the delay doubles (persisted); success resets it.
func TestAcceptanceFailbackFailsRevertsAndDoubles(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r2))
	stable := h.st().StableSince

	h.advanceUntil(10*time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.Equal(t, stable.Add(300*time.Second), h.clk.Now(), "failback attempted after failback_after_s")
	h.advanceUntil(time.Minute, isUpOn(r2))
	ff := h.act.eventsOf(state.EvFailbackFailed)
	require.Len(t, ff, 1)
	require.Equal(t, string(deyerr.F003), ff[0].Code)
	require.Equal(t, r1.Transport, ff[0].FromTransport)
	require.Equal(t, r2.Transport, ff[0].ToTransport)
	s := h.st()
	require.Equal(t, 600*time.Second, s.FailbackDelay)
	var persisted time.Duration
	h.act.set(func(f *fakeActions) { persisted = f.persisted[len(f.persisted)-1].FailbackDelay })
	require.Equal(t, 600*time.Second, persisted, "the doubled delay is persisted")
	require.Empty(t, h.act.eventsOf(state.EvFailback))
	revertAt := s.StableSince

	// The next attempt waits the doubled delay, fails again → 1200s.
	h.advanceUntil(20*time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.Equal(t, revertAt.Add(600*time.Second), h.clk.Now())
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Equal(t, 1200*time.Second, h.st().FailbackDelay)

	// Unblock: the next failback succeeds and resets the delay.
	h.act.block(r1.Key(), false)
	h.advanceUntil(30*time.Minute, isUpOn(r1))
	require.Equal(t, 300*time.Second, h.st().FailbackDelay)
	require.Len(t, h.act.eventsOf(state.EvFailback), 1)
}

func TestFailbackDelayCap(t *testing.T) {
	st := upOn(r2, t0.Add(-20*time.Hour))
	st.FailbackDelay = 20 * time.Hour
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.FailbackDelay != 20*time.Hour })
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Equal(t, FailbackDelayMax, h.st().FailbackDelay)
}

func TestFailbackRevertFailsStartsSwitching(t *testing.T) {
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	// The failback to r1 is under way: block r2 so the revert fails too.
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	h.act.block(r2.Key(), true)
	h.advanceUntil(2*time.Minute, isUpOn(r3))
	require.Len(t, h.act.eventsOf(state.EvFailbackFailed), 1)
	s := h.st()
	require.True(t, Quarantined(s.Quarantine, r2, h.clk.Now()))
	require.NotContains(t, s.Quarantine, r1.Key(), "a failed failback does not quarantine rung 1")
}

func TestFailbackDisabled(t *testing.T) {
	tun := twoNodes()
	tun.Settings.Failback = false
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, tun, st, func(f *fakeActions) { f.running[r2.Key()] = true }).run()
	h.advance(20 * time.Minute)
	require.Equal(t, r2, h.st().Active)
	require.Empty(t, h.act.callsOf("canary", ""))
}

func TestFailbackWaitsForPrimaryNode(t *testing.T) {
	st := upOn(b1, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[b1.Key()] = true
		f.offlineSince["de-1"] = t0
	}).run()
	h.advance(10 * time.Minute)
	require.Equal(t, b1, h.st().Active, "no failback while the primary node is offline")
	h.act.set(func(f *fakeActions) {
		delete(f.offlineSince, "de-1")
		f.svcDown["de-1"] = true
	})
	h.advance(time.Minute)
	require.Equal(t, b1, h.st().Active, "no failback while the primary service is down")
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = false })
	h.advanceUntil(time.Minute, isUpOn(r1))
	fb := h.act.eventsOf(state.EvFailback)
	require.Len(t, fb, 1)
	require.Equal(t, "nl-1", fb[0].FromNode)
	require.Equal(t, "de-1", fb[0].ToNode)
}

// Phase 8: with a canary, failback waits for recover_threshold (6)
// consecutive canary passes instead of the blind timer.
func TestCanaryFailback(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.canaryOn = true }).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r2))
	h.act.block(r1.Key(), false)
	h.advance(10 * time.Minute)
	require.Equal(t, r2, h.st().Active, "no blind failback when a canary exists")
	require.Zero(t, h.st().CanaryPasses)

	h.act.set(func(f *fakeActions) { f.canaryOK = true })
	h.advance(25 * time.Second)
	require.Equal(t, 5, h.st().CanaryPasses)
	require.Equal(t, r2, h.st().Active)
	// A failing canary probe resets the count.
	h.act.set(func(f *fakeActions) { f.canaryOK = false })
	h.advance(5 * time.Second)
	require.Zero(t, h.st().CanaryPasses)
	h.act.set(func(f *fakeActions) { f.canaryOK = true })
	took := h.advanceUntil(time.Minute, isUpOn(r1))
	require.Equal(t, 30*time.Second, took, "six passes in a row at 5s")
	require.Len(t, h.act.eventsOf(state.EvFailback), 1)
	require.Contains(t, h.act.eventsOf(state.EvFailback)[0].Reason, "canary passed 6 probes in a row")
	require.Zero(t, h.st().CanaryPasses)
}

// A failed canary failback doubles the delay like a blind one (section 9):
// the canary keeps passing, but the next attempt waits for the doubled
// delay instead of the next six passes; a success resets it.
func TestCanaryFailbackFailsWaitsDoubledDelay(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) {
		f.canaryOn = true
		f.canaryOK = true
	}).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r2))
	// The canary passes, the real rung 1 does not.
	took := h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.Equal(t, 30*time.Second, took, "the first attempt follows the canary")
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Len(t, h.act.eventsOf(state.EvFailbackFailed), 1)
	s := h.st()
	require.Equal(t, 600*time.Second, s.FailbackDelay)
	revertAt := s.StableSince

	h.advanceUntil(20*time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.Equal(t, revertAt.Add(600*time.Second), h.clk.Now(), "the next attempt waits for the doubled delay")
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Len(t, h.act.eventsOf(state.EvFailbackFailed), 2)
	require.Equal(t, 1200*time.Second, h.st().FailbackDelay)
	revertAt = h.st().StableSince

	// Rung 1 works again: the failback succeeds after 1200s and resets the
	// delay; the next one follows the canary alone again.
	h.act.block(r1.Key(), false)
	h.advanceUntil(30*time.Minute, isUpOn(r1))
	require.Equal(t, revertAt.Add(1200*time.Second), h.clk.Now())
	fb := h.act.eventsOf(state.EvFailback)
	require.Len(t, fb, 1)
	require.Contains(t, fb[0].Reason, "up on "+r2.Key()+" for 20m0s")
	require.Equal(t, 300*time.Second, h.st().FailbackDelay)

	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r2))
	h.act.block(r1.Key(), false)
	took = h.advanceUntil(time.Minute, isUpOn(r1))
	require.Equal(t, 30*time.Second, took)
	require.Len(t, h.act.eventsOf(state.EvFailback), 2)
}

// ---- node service / control / node failover

// Acceptance (S11): the service on the primary node goes down → switch to
// the backup node with the same transport, no transport switch.
func TestAcceptanceServiceDownSwitchesNode(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.advance(time.Minute)
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = true })
	took := h.advanceUntil(time.Minute, isUpOn(b1))
	require.LessOrEqual(t, took, 35*time.Second)
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
	sd := h.act.eventsOf(state.EvServiceDown)
	require.Len(t, sd, 1)
	require.Equal(t, string(deyerr.F005), sd[0].Code)
	require.Equal(t, "de-1", sd[0].Node)
	sn := h.act.eventsOf(state.EvSwitchNode)
	require.Len(t, sn, 1)
	require.Equal(t, "de-1", sn[0].FromNode)
	require.Equal(t, "nl-1", sn[0].ToNode)
	require.Equal(t, sn[0].FromTransport, sn[0].ToTransport)
	s := h.st()
	require.False(t, s.ServiceDown)
	require.Empty(t, s.Quarantine, "a service outage does not quarantine transports")
	require.Equal(t, []string{"start " + r1.Key(), "stop " + r1.Key(), "start " + b1.Key()}, h.act.opSequence("start", "stop"))
}

func TestServiceDownSameRungExcludedGoesToRungOneOfBackup(t *testing.T) {
	st := upOn(r2, t0)
	st.Quarantine = map[string]state.Quarantine{b2.Key(): {Until: t0.Add(time.Hour), Duration: time.Hour}}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) { f.running[r2.Key()] = true }).run()
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = true })
	h.advanceUntil(time.Minute, isUpOn(b1))
	require.Len(t, h.act.eventsOf(state.EvSwitchNode), 1)
}

func TestServiceDownWithoutBackupStaysDegraded(t *testing.T) {
	for _, tc := range []struct {
		name string
		tun  Tunnel
	}{
		{"single node", oneNode()},
		{"transport_only", func() Tunnel {
			t := twoNodes()
			t.Settings.Policy = config.PolicyTransportOnly
			return t
		}()},
		{"backup service down too", twoNodes()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.tun, state.TunnelState{}, nil).run()
			h.act.set(func(f *fakeActions) {
				f.svcDown["de-1"] = true
				f.svcDown["nl-1"] = true
			})
			h.advance(20 * time.Second)
			s := h.st()
			require.Equal(t, state.StateDegraded, s.State)
			require.True(t, s.ServiceDown)
			require.Contains(t, s.TransitionCause, "service down on node de-1")
			h.advance(2 * time.Minute)
			require.Equal(t, state.StateDegraded, h.st().State)
			sd := h.act.eventsOf(state.EvServiceDown)
			require.Len(t, sd, 1, "emitted once")
			require.Equal(t, "Tunnel main: service behind the tunnel is down on node de-1", sd[0].Message)
			require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"), "no switch")

			h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = false })
			h.advance(5 * time.Second)
			s = h.st()
			require.Equal(t, state.StateUp, s.State)
			require.False(t, s.ServiceDown)
		})
	}
}

func TestServiceDownThenPathStillFailsSwitchesTransport(t *testing.T) {
	h := newHarness(t, oneNode(), state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = true })
	h.advance(20 * time.Second)
	require.True(t, h.st().ServiceDown)
	// The service is back but the transport is (also) blocked.
	h.act.set(func(f *fakeActions) {
		f.svcDown["de-1"] = false
		f.blocked[r1.Key()] = true
	})
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.False(t, h.st().ServiceDown)
}

func TestServiceTargetInMessage(t *testing.T) {
	tun := oneNode()
	tun.ServiceTarget = "127.0.0.1:443"
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) { f.svcDown["de-1"] = true })
	h.advance(20 * time.Second)
	require.Equal(t, "Tunnel main: service 127.0.0.1:443 is down on node de-1", h.act.eventsOf(state.EvServiceDown)[0].Message)
}

// Control offline but the path passes: nothing switches (section 9).
func TestControlOfflinePathOKNoSwitch(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) { f.offlineSince["de-1"] = h.clk.Now() })
	h.advance(10 * time.Minute)
	h.requireActive(r1, state.StateUp)
	require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"))
	require.Empty(t, h.act.eventsOf(state.EvNodeOffline), "node_offline is the daemon's event")
}

// S12: the primary node's network is gone (control offline + every path
// failing) → the backup node within 45s.
func TestNodeBrokenGoesToBackupNode(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.advance(time.Minute)
	h.act.set(func(f *fakeActions) {
		f.offlineSince["de-1"] = f.clk.Now()
		for _, r := range config.DefaultLadder {
			f.blocked["de-1/"+r] = true
		}
	})
	took := h.advanceUntil(2*time.Minute, isUpOn(b1))
	require.LessOrEqual(t, took, 45*time.Second)
	s := h.st()
	require.NotContains(t, s.Quarantine, r2.Key(), "a node outage does not quarantine the rung tried meanwhile")
	require.Len(t, h.act.eventsOf(state.EvSwitchNode), 1)
	// Once the node counts as broken (offline > 30s) the attempt in progress
	// ends at the next poll and its remaining rungs are skipped.
	require.Empty(t, h.act.callsOf("start", "de-1/frp/tcp"), "remaining rungs of the broken node are skipped")
	require.LessOrEqual(t, len(h.act.callsOf("probe", r3.Key())), 2)
}

func TestNodeAlreadyBrokenAtThreshold(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) {
		f.offlineSince["de-1"] = f.clk.Now().Add(-time.Minute)
		f.blocked[r1.Key()] = true
	})
	h.advanceUntil(time.Minute, isUpOn(b1))
	require.Empty(t, h.act.callsOf("start", r2.Key()))
	require.Empty(t, h.st().Quarantine)
}

func TestNodeOnlyPolicy(t *testing.T) {
	tun := twoNodes()
	tun.Settings.Policy = config.PolicyNodeOnly
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(b1))
	require.Len(t, h.act.eventsOf(state.EvSwitchNode), 1)
	require.Empty(t, h.act.callsOf("start", r2.Key()))
}

func TestTransportOnlyNeverLeavesNode(t *testing.T) {
	tun := twoNodes()
	tun.Settings.Policy = config.PolicyTransportOnly
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	})
	h.advanceUntil(2*time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	require.Empty(t, h.act.callsOf("start", b1.Key()))
	require.Len(t, h.act.eventsOf(state.EvTunnelDown), 1)
}

// ---- anti-flapping

// Acceptance (S13): more than max_switches_per_hour automatic switches →
// flapping, the tunnel goes to direct/native and stays there.
func TestAcceptanceFlappingGoesToDirectNative(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"a/1", "a/2", "a/3", "a/4", "a/5", "a/6", "a/7", "a/8", "direct/native"}
	tun.Settings.Failback = false
	h := newHarness(t, tun, state.TunnelState{}, nil).run()
	for i := 1; i <= 6; i++ {
		h.act.block(cand("de-1", tun.Ladder[i-1]).Key(), true)
		h.advanceUntil(time.Minute, isUpOn(cand("de-1", tun.Ladder[i])))
		require.False(t, h.st().Flapping, "switch %d is within the limit", i)
	}
	require.Len(t, h.st().SwitchTimes, 6)
	// Seventh block in the same hour.
	h.act.block(cand("de-1", "a/7").Key(), true)
	h.advanceUntil(time.Minute, isUpOn(dn))
	s := h.st()
	require.True(t, s.Flapping)
	fl := h.act.eventsOf(state.EvFlapping)
	require.Len(t, fl, 1)
	require.Equal(t, string(deyerr.F002), fl[0].Code)
	require.Equal(t, "direct/native", fl[0].ToTransport)
	require.Equal(t, "Tunnel main: flapping limit reached (6 switches/hour)", fl[0].Message)
	require.Empty(t, h.act.callsOf("start", "de-1/a/8"), "the next rung is not tried while flapping")
	require.Len(t, s.SwitchTimes, 6, "the flapping fallback is not counted")

	// Even when direct/native fails, the tunnel holds it while flapping.
	h.act.block(dn.Key(), true)
	h.advance(5 * time.Minute)
	s = h.st()
	require.Equal(t, dn, s.Active)
	require.Equal(t, state.StateDegraded, s.State)
	require.Empty(t, h.act.callsOf("start", "de-1/a/8"))
	require.NotContains(t, s.Quarantine, dn.Key(), "direct/native is never quarantined")

	// When the hour window clears, automatic switching resumes.
	h.advanceUntil(time.Hour, func(s state.TunnelState) bool { return s.Active == cand("de-1", "a/8") })
	require.False(t, h.st().Flapping)
	require.Len(t, h.act.eventsOf(state.EvFlapping), 1)
}

func TestFlappingWhileUpHoldsFailback(t *testing.T) {
	st := upOn(r2, t0.Add(-10*time.Minute))
	for i := 0; i < 6; i++ {
		st.SwitchTimes = append(st.SwitchTimes, t0.Add(-time.Duration(50-i)*time.Minute))
	}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) { f.running[r2.Key()] = true }).run()
	h.advance(time.Minute)
	s := h.st()
	require.Equal(t, r2, s.Active, "if UP, stay")
	require.True(t, s.Flapping)
	require.Len(t, h.act.eventsOf(state.EvFlapping), 1)
	require.Empty(t, h.act.callsOf("start", ""))
	// The oldest switch leaves the window 10 minutes later; failback follows.
	h.advanceUntil(15*time.Minute, isUpOn(r1))
	require.GreaterOrEqual(t, h.clk.Now().Sub(t0), 10*time.Minute)
	require.False(t, h.st().Flapping)
}

func TestFlappingWithoutFallbackHolds(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"a/1", "a/2"}
	st := upOn(cand("de-1", "a/1"), t0)
	for i := 0; i < 6; i++ {
		st.SwitchTimes = append(st.SwitchTimes, t0.Add(-time.Minute))
	}
	h := newHarness(t, tun, st, func(f *fakeActions) {
		f.running["de-1/a/1"] = true
		f.blocked["de-1/a/1"] = true
	}).run()
	h.advance(time.Minute)
	s := h.st()
	require.True(t, s.Flapping)
	require.Equal(t, state.StateDegraded, s.State)
	require.Empty(t, h.act.callsOf("start", ""))
}

func TestServiceDownWhileFlappingHolds(t *testing.T) {
	st := upOn(r1, t0)
	for i := 0; i < 6; i++ {
		st.SwitchTimes = append(st.SwitchTimes, t0.Add(-time.Minute))
	}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r1.Key()] = true
		f.svcDown["de-1"] = true
	}).run()
	h.advance(time.Minute)
	s := h.st()
	require.True(t, s.Flapping)
	require.True(t, s.ServiceDown)
	require.Equal(t, r1, s.Active)
	require.Empty(t, h.act.callsOf("start", ""))
}

// ---- manual commands

func TestManualSwitchNotCounted(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	targets := []string{"rathole/noise", "frp/tcp", "backhaul/tcpmux", "xray/reality", "direct/native", "backhaul/wssmux", "rathole/noise", "frp/tcp"}
	for _, tr := range targets {
		require.NoError(t, h.eng.SwitchTransport(ctx, tr))
		h.idle()
		h.requireActive(cand("de-1", tr), state.StateUp)
	}
	s := h.st()
	require.Empty(t, s.SwitchTimes, "manual switches are not counted")
	require.False(t, s.Flapping)
	// Each manual move is a switch_transport event (section 9: fixed names),
	// info level, with the owner's request as the reason.
	ms := h.act.eventsOf(state.EvSwitchTransport)
	require.Len(t, ms, len(targets))
	require.Equal(t, "Tunnel main switched transport backhaul/wssmux -> rathole/noise on node de-1", ms[0].Message)
	require.Equal(t, "manual switch to transport rathole/noise", ms[0].Reason)
	for _, ev := range ms {
		require.Equal(t, state.LevelInfo, ev.Level)
	}
	require.Empty(t, h.act.eventsOf(state.EvSwitchNode))

	// Automatic switching still works normally afterwards.
	h.act.block(cand("de-1", "frp/tcp").Key(), true)
	h.advanceUntil(time.Minute, isUpOn(cand("de-1", "xray/reality")))
	require.False(t, h.st().Flapping)

	// Switching to the active candidate is a no-op.
	h.act.clearCalls()
	require.NoError(t, h.eng.SwitchTransport(ctx, "xray/reality"))
	require.Empty(t, h.act.callsOf("start", ""))
}

func TestManualSwitchAllowedWhileFlappingAndQuarantined(t *testing.T) {
	st := upOn(r2, t0)
	st.Flapping = true
	st.Quarantine = map[string]state.Quarantine{r3.Key(): {Until: t0.Add(time.Hour), Duration: time.Hour}}
	for i := 0; i < 6; i++ {
		st.SwitchTimes = append(st.SwitchTimes, t0.Add(-time.Minute))
	}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) { f.running[r2.Key()] = true }).run()
	require.NoError(t, h.eng.SwitchTransport(context.Background(), r3.Transport))
	h.requireActive(r3, state.StateUp)
	require.NotContains(t, h.st().Quarantine, r3.Key())
}

func TestManualSwitchFailureReverts(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r3.Key()] = true }).run()
	ch := h.async(func() error { return h.eng.SwitchTransport(context.Background(), r3.Transport) })
	h.advance(20 * time.Second)
	err := h.wait(ch)
	require.Equal(t, deyerr.B004, codeOf(err))
	require.Equal(t, "Transport rathole/noise did not pass the probe within 15s", deyerr.As(err).Message())
	h.requireActive(r1, state.StateUp)
	require.Equal(t, []string{"start " + r1.Key(), "stop " + r1.Key(), "start " + r3.Key(), "stop " + r3.Key(), "start " + r1.Key()},
		h.act.opSequence("start", "stop"))
	s := h.st()
	require.Empty(t, s.Quarantine, "a failed manual switch does not quarantine")
	require.Empty(t, s.SwitchTimes)
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
}

func TestManualSwitchFailureAndRevertFailure(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r3.Key()] = true }).run()
	h.act.block(r1.Key(), true) // r1 goes bad right when the owner switches
	ch := h.async(func() error { return h.eng.SwitchTransport(context.Background(), r3.Transport) })
	h.advance(31 * time.Second)
	require.Equal(t, deyerr.B004, codeOf(h.wait(ch)))
	h.advanceUntil(time.Minute, isUpOn(r2))
}

func TestManualSwitchErrors(t *testing.T) {
	ctx := context.Background()
	st := state.TunnelState{Skipped: map[string]state.Skip{
		"de-1/hysteria2/udp": {Reason: "UDP is blocked between hub and node", Code: string(deyerr.B007)},
		"de-1/frp/tcp":       {Reason: "validate failed", Code: "not-a-code"},
	}}
	h := newHarness(t, twoNodes(), st, nil).run()
	require.Equal(t, deyerr.F006, codeOf(h.eng.SwitchTransport(ctx, "nope/x")))
	require.Equal(t, deyerr.F006, codeOf(h.eng.SwitchNode(ctx, "xx-1")))
	err := h.eng.SwitchTransport(ctx, "hysteria2/udp")
	require.Equal(t, deyerr.B007, codeOf(err))
	require.Equal(t, "UDP is blocked between hub and node", deyerr.As(err).Detail)
	require.Equal(t, deyerr.F006, codeOf(h.eng.SwitchTransport(ctx, "frp/tcp")))
	h.requireActive(r1, state.StateUp)
}

func TestManualSwitchNodeAndReset(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	require.NoError(t, h.eng.SwitchTransport(ctx, r3.Transport))
	require.NoError(t, h.eng.SwitchNode(ctx, "nl-1"))
	h.requireActive(cand("nl-1", r3.Transport), state.StateUp)
	require.Len(t, h.act.eventsOf(state.EvSwitchTransport), 1)
	ms := h.act.eventsOf(state.EvSwitchNode)
	require.Len(t, ms, 1, "a manual node switch is switch_node")
	require.Equal(t, "de-1", ms[0].FromNode)
	require.Equal(t, "nl-1", ms[0].ToNode)
	require.Equal(t, "manual switch to node nl-1", ms[0].Reason)

	// Reset from nl-1 to rung 1 of de-1 changes the node too.
	require.NoError(t, h.eng.Reset(ctx))
	h.requireActive(r1, state.StateUp)
	ms = h.act.eventsOf(state.EvSwitchNode)
	require.Len(t, ms, 2)
	require.Equal(t, "manual reset to rung 1", ms[1].Reason)
	require.Equal(t, r1.Transport, ms[1].ToTransport)
	require.Empty(t, h.st().SwitchTimes)
	// Reset on rung 1 is a no-op.
	h.act.clearCalls()
	require.NoError(t, h.eng.Reset(ctx))
	require.Empty(t, h.act.callsOf("start", ""))
}

func TestManualSwitchFromDown(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	retryAt := h.st().DownRetryAt

	// A failing manual switch leaves the tunnel DOWN with its retry schedule.
	ch := h.async(func() error { return h.eng.SwitchTransport(context.Background(), "direct/native") })
	h.advance(16 * time.Second)
	require.Equal(t, deyerr.B004, codeOf(h.wait(ch)))
	s := h.st()
	require.Equal(t, state.StateDown, s.State)
	require.Equal(t, retryAt, s.DownRetryAt)
	require.Empty(t, h.act.runningKeys())
	require.Equal(t, dn, s.Active, "DOWN keeps the last candidate tried")

	// A passing one brings it up.
	h.act.block(dn.Key(), false)
	n := len(h.act.eventsOf(state.EvTunnelUp))
	require.NoError(t, h.eng.SwitchTransport(context.Background(), "direct/native"))
	h.requireActive(dn, state.StateUp)
	require.True(t, h.st().DownRetryAt.IsZero())
	// The same candidate started again: tunnel_up, not a switch.
	ups := h.act.eventsOf(state.EvTunnelUp)
	require.Len(t, ups, n+1)
	require.Equal(t, "manual switch to transport direct/native", ups[n].Reason)
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
}

// ---- pause / resume

// Acceptance: pause stops switching but not probing.
func TestAcceptancePauseStopsSwitchingNotProbing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	require.NoError(t, h.eng.Pause(ctx))
	require.NoError(t, h.eng.Pause(ctx), "idempotent")
	s := h.st()
	require.Equal(t, state.StatePaused, s.State)
	require.True(t, s.Paused)

	h.act.block(r1.Key(), true)
	h.act.clearCalls()
	h.advance(time.Minute)
	require.Len(t, h.act.callsOf("probe", r1.Key()), 12, "probing continues")
	require.Empty(t, h.act.callsOf("start", ""), "no switching")
	s = h.st()
	require.Equal(t, state.StatePaused, s.State)
	require.Equal(t, 12, s.FailCount)
	require.Equal(t, "i/o timeout", s.LastProbeErr)

	// Resume: the probes imply a switch, which happens at once.
	require.NoError(t, h.eng.Resume(ctx))
	h.idle()
	h.requireActive(r2, state.StateUp)
	require.False(t, h.st().Paused)
	require.Len(t, h.act.eventsOf(state.EvSwitchTransport), 1)
	require.NoError(t, h.eng.Resume(ctx), "idempotent")
}

func TestResumeImpliedStates(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	require.NoError(t, h.eng.Pause(ctx))
	require.NoError(t, h.eng.Resume(ctx))
	require.Equal(t, state.StateUp, h.st().State)

	require.NoError(t, h.eng.Pause(ctx))
	h.act.block(r1.Key(), true)
	h.advance(5 * time.Second)
	require.NoError(t, h.eng.Resume(ctx))
	require.Equal(t, state.StateDegraded, h.st().State, "one failure → DEGRADED")
	h.act.block(r1.Key(), false)
	h.advance(5 * time.Second)
	require.Equal(t, state.StateUp, h.st().State)
}

func TestPauseDuringSwitchingStopsTheCycle(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r2.Key()] = true }).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateSwitching && s.Active == r2 })
	require.NoError(t, h.eng.Pause(ctx))
	// The pause takes effect once the start of r2 in flight returns.
	require.Eventually(t, func() bool { return h.st().State == state.StatePaused }, idleWait, time.Millisecond)
	h.idle()
	s := h.st()
	require.Equal(t, r2, s.Active, "the candidate being tried keeps running")
	h.advance(2 * time.Minute)
	require.Empty(t, h.act.callsOf("start", r3.Key()))
	require.Equal(t, r2, h.st().Active)

	require.NoError(t, h.eng.Resume(ctx))
	h.advanceUntil(time.Minute, isUpOn(r3))
}

func TestPauseWhileDownAndResume(t *testing.T) {
	ctx := context.Background()
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) { f.blocked[r1.Key()] = true }).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	require.NoError(t, h.eng.Pause(ctx))
	require.Eventually(t, func() bool { return h.st().State == state.StatePaused }, idleWait, time.Millisecond)
	h.act.clearCalls()
	h.advance(10 * time.Minute)
	require.Empty(t, h.act.callsOf("start", ""), "no DOWN retries while paused")
	h.act.block(r1.Key(), false)
	require.NoError(t, h.eng.Resume(ctx))
	h.advanceUntil(time.Minute, isUpOn(r1))
}

// ---- DOWN

// Acceptance: DOWN retries the ladder from rung 1 after 30s, doubling up to 5
// minutes; direct/native is never quarantined.
func TestAcceptanceDownRetryBackoff(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	downAt := h.clk.Now()
	s := h.st()
	require.Equal(t, 30*time.Second, s.DownBackoff)
	require.Equal(t, downAt.Add(30*time.Second), s.DownRetryAt)
	require.True(t, Quarantined(s.Quarantine, r1, downAt))
	require.NotContains(t, s.Quarantine, dn.Key(), "direct/native is never quarantined")
	require.Empty(t, h.act.runningKeys(), "nothing runs in DOWN")
	down := h.act.eventsOf(state.EvTunnelDown)
	require.Len(t, down, 1)
	require.Equal(t, "Tunnel main: all candidates exhausted", down[0].Message)
	probeErrors := len(h.act.eventsOf(state.EvProbeError))

	h.advance(25 * time.Minute)
	starts := h.act.callsOf("start", r1.Key())
	require.GreaterOrEqual(t, len(starts), 7)
	var gaps []time.Duration
	prevEnd := downAt
	for _, st := range starts[1:7] {
		gaps = append(gaps, st.Sub(prevEnd))
		prevEnd = st.Add(2 * StartWait) // each retry tries two rungs for 15s each
	}
	require.Equal(t, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}, gaps)
	require.Len(t, h.act.eventsOf(state.EvTunnelDown), 1, "tunnel_down once")
	require.Equal(t, probeErrors, len(h.act.eventsOf(state.EvProbeError)), "retries do not flood the event ring")
	require.Equal(t, state.StateDown, h.st().State)
	require.Equal(t, DownRetryMax, h.st().DownBackoff)

	// Recovery at the next retry: tunnel_up, backoff cleared.
	h.act.block(r1.Key(), false)
	h.advanceUntil(6*time.Minute, isUpOn(r1))
	s = h.st()
	require.Zero(t, s.DownBackoff)
	require.True(t, s.DownRetryAt.IsZero())
	require.Len(t, h.act.eventsOf(state.EvTunnelUp), 1)
	require.NotContains(t, s.Quarantine, r1.Key())
}

func TestDownRetryTriesBackupNode(t *testing.T) {
	tun := twoNodes()
	tun.Ladder = []string{"backhaul/wssmux"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[b1.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	h.act.block(b1.Key(), false)
	h.advanceUntil(2*time.Minute, isUpOn(b1))
	up := h.act.eventsOf(state.EvTunnelUp)
	require.Equal(t, "nl-1", up[len(up)-1].ToNode)
}

// ---- restart / reconcile

// Acceptance (S14): the hub restarts in the middle of SWITCHING; the state is
// recovered and the healthy tunnel is not restarted.
func TestAcceptanceRestartMidSwitchingDoesNotRestart(t *testing.T) {
	persisted := state.TunnelState{
		ID: "main", State: state.StateSwitching, Active: r2, Previous: r1,
		Tried: []string{r1.Key(), r2.Key()}, FailbackDelay: 600 * time.Second,
		Quarantine: map[string]state.Quarantine{r1.Key(): {Until: t0.Add(5 * time.Minute), Duration: 10 * time.Minute}},
	}
	units := map[string]bool{r2.Key(): true}
	rec := Reconcile(persisted, units, twoNodes())
	require.Equal(t, state.StateUp, rec.State)
	require.Empty(t, StrayUnits(rec, units))

	h := newHarness(t, twoNodes(), rec, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	h.advance(5 * time.Minute)
	require.Empty(t, h.act.callsOf("start", ""), "a healthy tunnel is never restarted")
	require.Empty(t, h.act.callsOf("stop", ""))
	require.GreaterOrEqual(t, len(h.act.callsOf("probe", r2.Key())), 60)
	h.requireActive(r2, state.StateUp)
	require.Equal(t, 600*time.Second, h.st().FailbackDelay, "persisted failback delay survives")
}

func TestBootWithUnreconciledSwitchingStartsActive(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{State: state.StateSwitching, Active: r2}, nil).run()
	h.requireActive(r2, state.StateUp)
	require.Equal(t, []string{"start " + r2.Key()}, h.act.opSequence("start"))
}

func TestBootWithRemovedActiveCandidate(t *testing.T) {
	gone := cand("de-1", "gone/x")
	h := newHarness(t, twoNodes(), upOn(gone, t0), func(f *fakeActions) { f.running[gone.Key()] = true }).run()
	h.requireActive(r1, state.StateUp)
	require.Equal(t, []string{"stop " + gone.Key(), "start " + r1.Key()}, h.act.opSequence("start", "stop"))
}

func TestBootPausedAndDown(t *testing.T) {
	st := state.TunnelState{State: state.StatePaused, Paused: true, Active: r1}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) { f.running[r1.Key()] = true }).run()
	require.Equal(t, state.StatePaused, h.st().State)
	h.advance(10 * time.Second)
	require.Len(t, h.act.callsOf("probe", r1.Key()), 3)
	h.stop()

	st = state.TunnelState{State: state.StateDown, Active: r1, DownRetryAt: t0.Add(10 * time.Second)}
	h = newHarness(t, twoNodes(), st, nil).run()
	require.Equal(t, 30*time.Second, h.st().DownBackoff)
	h.advanceUntil(time.Minute, isUpOn(r1))
	require.Equal(t, t0.Add(10*time.Second), h.act.callsOf("start", r1.Key())[0])
}

func TestStartingWhilePaused(t *testing.T) {
	st := state.TunnelState{State: state.StateStarting, Paused: true, Active: r1}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) { f.blocked[r1.Key()] = true }).run()
	s := h.st()
	require.Equal(t, state.StatePaused, s.State)
	require.Equal(t, r1, s.Active)
	h.advance(time.Minute)
	require.Equal(t, []string{"start " + r1.Key()}, h.act.opSequence("start", "stop"), "paused: no switching")

	st = state.TunnelState{State: state.StateStarting, Paused: true, Active: r1}
	h2 := newHarness(t, twoNodes(), st, nil).run()
	require.Equal(t, state.StatePaused, h2.st().State)
	require.Zero(t, h2.st().FailCount)
}

// ---- test ladder

// Acceptance (S18): every rung on every node for up to 20s, RTT reported,
// original candidate restored.
func TestAcceptanceTestLadderRestoresOriginal(t *testing.T) {
	tun := twoNodes()
	tun.Ladder = []string{"backhaul/wssmux", "backhaul/tcpmux", "hysteria2/udp", "direct/native"}
	st := state.TunnelState{Skipped: map[string]state.Skip{"nl-1/hysteria2/udp": {Reason: "UDP blocked", Code: string(deyerr.B007)}}}
	h := newHarness(t, tun, st, func(f *fakeActions) {
		f.blocked[r2.Key()] = true
		f.rtt[b1.Key()] = 150 * time.Millisecond
	}).run()
	var rows []api.RungResult
	var got []api.RungResult
	ch := h.async(func() error {
		var err error
		got, err = h.eng.TestLadder(context.Background(), func(r api.RungResult) { rows = append(rows, r) })
		return err
	})
	h.advance(time.Minute)
	require.NoError(t, h.wait(ch))
	require.Equal(t, rows, got)
	require.Len(t, got, 8)
	byKey := map[string]api.RungResult{}
	for _, r := range got {
		byKey[r.Node+"/"+r.Transport] = r
	}
	require.True(t, byKey[r1.Key()].OK)
	require.Equal(t, 20, byKey[r1.Key()].RTTms)
	require.False(t, byKey[r2.Key()].OK)
	require.Equal(t, string(deyerr.B004), byKey[r2.Key()].Error.Code)
	require.Equal(t, "Transport backhaul/tcpmux did not pass the probe within 20s", byKey[r2.Key()].Error.Message)
	require.Equal(t, "UDP blocked", byKey["nl-1/hysteria2/udp"].Skipped)
	require.Equal(t, 150, byKey[b1.Key()].RTTms)

	h.requireActive(r1, state.StateUp)
	require.Equal(t, map[string]bool{r1.Key(): true}, h.act.runningKeys())
	s := h.st()
	require.Empty(t, s.SwitchTimes)
	require.Empty(t, s.Quarantine, "test results do not quarantine")
	require.Empty(t, h.act.eventsOf(state.EvSwitchTransport))
	require.Empty(t, h.act.eventsOf(state.EvProbeError))
	// Each tested candidate is started and stopped; r2 is polled for 20s.
	require.Len(t, h.act.callsOf("probe", r2.Key()), 21)
	require.Len(t, h.act.callsOf("start", b1.Key()), 1)
	require.Len(t, h.act.callsOf("stop", b1.Key()), 1)
}

func TestTestLadderCancelRestores(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r2.Key()] = true }).run()
	ctx, cancel := context.WithCancel(context.Background())
	ch := h.async(func() error {
		_, err := h.eng.TestLadder(ctx, nil)
		return err
	})
	h.advance(3 * time.Second) // r1 passed, r2 is being polled
	cancel()
	require.Equal(t, deyerr.F008, codeOf(h.wait(ch)))
	require.Eventually(t, func() bool { return isUpOn(r1)(h.st()) }, idleWait, time.Millisecond)
	h.idle()
	require.Empty(t, h.act.callsOf("start", r3.Key()))
	require.Equal(t, map[string]bool{r1.Key(): true}, h.act.runningKeys())
}

func TestTestLadderWhileDownAndRestoreFailure(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	ch := h.async(func() error {
		_, err := h.eng.TestLadder(context.Background(), nil)
		return err
	})
	h.advance(45 * time.Second)
	require.NoError(t, h.wait(ch))
	// Back to DOWN; the retry that fell due during the test runs next.
	require.Equal(t, state.StateDown, h.st().State)
	require.Len(t, h.act.eventsOf(state.EvTunnelDown), 1)
	h.stop()

	// The original does not come back after the test: automatic switching.
	h = newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	ch = h.async(func() error {
		_, err := h.eng.TestLadder(context.Background(), nil)
		return err
	})
	h.act.block(r1.Key(), true) // r1 was tested first; it breaks meanwhile
	h.advance(2 * time.Minute)
	require.NoError(t, h.wait(ch))
	h.advanceUntil(time.Minute, isUpOn(r2))
	require.Len(t, h.act.eventsOf(state.EvSwitchTransport), 1)
}

// ---- configuration edits and skipped rungs

func TestUpdateConfig(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.block(r3.Key(), true)
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r2))
	h.act.block(r1.Key(), false)

	tun := twoNodes()
	tun.Ladder = []string{"backhaul/wssmux", "rathole/noise", "direct/native"}
	tun.Settings.FailbackAfterS = 120
	require.NoError(t, h.eng.UpdateConfig(ctx, tun))
	h.idle()
	h.requireActive(r1, state.StateUp)
	require.Equal(t, 120*time.Second, h.st().FailbackDelay)
	ms := h.act.eventsOf(state.EvSwitchTransport)
	require.Len(t, ms, 2, "the automatic switch to r2, then the move back to rung 1")
	require.Equal(t, "active candidate removed by a configuration change", ms[1].Reason)
	require.Equal(t, r2.Transport, ms[1].FromTransport)
	require.Equal(t, r1.Transport, ms[1].ToTransport)

	// Unchanged active candidate: nothing moves; stale entries are dropped.
	h.act.clearCalls()
	tun.Nodes = []string{"de-1"}
	require.NoError(t, h.eng.UpdateConfig(ctx, tun))
	require.Empty(t, h.act.callsOf("start", ""))
	for k := range h.st().Quarantine {
		require.True(t, strings.HasPrefix(k, "de-1/"), k)
	}

	// No usable candidate left: DOWN.
	tun.Ladder = []string{"hysteria2/udp"}
	require.NoError(t, h.eng.SetSkipped(ctx, cand("de-1", "hysteria2/udp"), state.Skip{Reason: "udp"}))
	require.NoError(t, h.eng.UpdateConfig(ctx, tun))
	require.Equal(t, state.StateDown, h.st().State)
}

func TestSkippedRungsAreNotTried(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	require.NoError(t, h.eng.SetSkipped(ctx, r2, state.Skip{Reason: "validate failed", Code: string(deyerr.B006), RecheckAt: t0.Add(30 * time.Minute)}))
	require.Contains(t, h.st().Skipped, r2.Key())
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, isUpOn(r3))
	require.Empty(t, h.act.callsOf("start", r2.Key()))
	require.NoError(t, h.eng.ClearSkipped(ctx, r2))
	require.NotContains(t, h.st().Skipped, r2.Key())
}

// ---- command plumbing

func TestCommandsWhenNotRunning(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := h.eng.Pause(ctx)
	require.Equal(t, deyerr.F008, codeOf(err))
	require.Equal(t, "Tunnel main: pause did not finish in time", deyerr.As(err).Message())
	require.True(t, errors.Is(err, context.DeadlineExceeded))

	h.run()
	h.stop()
	<-h.eng.Done()
	err = h.eng.Resume(context.Background())
	require.Equal(t, deyerr.F007, codeOf(err))
	_, err = h.eng.TestLadder(context.Background(), nil)
	require.Equal(t, deyerr.F007, codeOf(err))
	require.Error(t, h.eng.Run(context.Background()), "Run twice")
}

func TestQueuedCommandWithExpiredContextIsNotApplied(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r3.Key()] = true }).run()
	first := h.async(func() error { return h.eng.SwitchTransport(context.Background(), r3.Transport) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	second := h.async(func() error { return h.eng.SwitchNode(ctx, "nl-1") })
	require.Equal(t, deyerr.F008, codeOf(h.wait(second)))
	h.advance(20 * time.Second)
	require.Equal(t, deyerr.B004, codeOf(h.wait(first)))
	h.advance(5 * time.Second)
	h.requireActive(r1, state.StateUp)
	require.Empty(t, h.act.callsOf("start", b1.Key()), "the expired command was dropped")
}

func TestManualCommandInterruptsAutomaticCycle(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r2.Key()] = true }).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateSwitching && s.Active == r2 })
	require.NoError(t, h.wait(h.async(func() error { return h.eng.SwitchNode(context.Background(), "nl-1") })))
	h.idle()
	h.requireActive(cand("nl-1", r2.Transport), state.StateUp)
	h.advance(time.Minute)
	require.Empty(t, h.act.callsOf("start", r3.Key()), "the interrupted cycle does not continue")
}

func TestSkipAppliedWhileBusyAndInterruptedCycleResumes(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, func(f *fakeActions) { f.blocked[r2.Key()] = true }).run()
	h.act.block(r1.Key(), true)
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateSwitching && s.Active == r2 })
	// Served immediately while the cycle waits.
	require.NoError(t, h.eng.SetSkipped(ctx, r3, state.Skip{Reason: "x"}))
	require.NoError(t, h.eng.Resume(ctx))
	// A configuration update interrupts the cycle; the cycle then resumes by
	// verifying the candidate it was trying.
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(ctx, twoNodes()) })))
	h.advanceUntil(time.Minute, isUpOn(cand("de-1", "frp/tcp")))
	require.Empty(t, h.act.callsOf("start", r3.Key()))
}

func TestPersistence(t *testing.T) {
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	count := func() int {
		var n int
		h.act.set(func(f *fakeActions) { n = len(f.persisted) })
		return n
	}
	base := count()
	h.advance(59 * time.Second)
	require.Equal(t, base, count(), "steady probes are not persisted every tick")
	h.advance(5 * time.Second)
	require.Equal(t, base+1, count(), "but at least every minute")

	// A failing store is retried on every commit until it works again.
	h.act.set(func(f *fakeActions) { f.persistErr = errors.New("disk full") })
	require.NoError(t, h.eng.SetSkipped(context.Background(), b2, state.Skip{Reason: "x"}))
	c0 := count()
	require.Equal(t, base+2, c0)
	h.advance(5 * time.Second)
	h.advance(5 * time.Second)
	require.Equal(t, c0+2, count())
	h.act.set(func(f *fakeActions) { f.persistErr = nil })
	h.advance(5 * time.Second)
	h.advance(5 * time.Second)
	require.Equal(t, c0+3, count())
	var last state.TunnelState
	h.act.set(func(f *fakeActions) { last = f.persisted[len(f.persisted)-1] })
	require.Equal(t, "main", last.ID)
	require.False(t, last.UpdatedAt.IsZero())
	require.False(t, last.UpdatedAt.After(h.clk.Now()))
	require.Contains(t, last.Skipped, b2.Key())
}

func TestNeverQuarantineCatalog(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "wireguard/kernel", "direct/native"}
	tun.NeverQuarantine = func(id string) bool { return id == "wireguard/kernel" }
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked["de-1/wireguard/kernel"] = true
	}).run()
	h.advanceUntil(time.Minute, isUpOn(dn))
	q := h.st().Quarantine
	require.Contains(t, q, r1.Key())
	require.NotContains(t, q, "de-1/wireguard/kernel")
}

// ---- remaining paths

func TestPausedStartErrorHolds(t *testing.T) {
	st := state.TunnelState{State: state.StateStarting, Paused: true, Active: r1}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.startErr[r1.Key()] = deyerr.New(deyerr.B003, deyerr.Params{"unit": "u"})
	}).run()
	s := h.st()
	require.Equal(t, state.StatePaused, s.State)
	require.Equal(t, 1, s.FailCount)
	require.Contains(t, s.LastProbeErr, "DEY-B003")
	require.Equal(t, r1, h.eng.Snapshot().Active)
}

func TestStartingWithRemovedCandidateStartsRungOne(t *testing.T) {
	st := state.TunnelState{State: state.StateStarting, Active: cand("de-1", "gone/x")}
	h := newHarness(t, twoNodes(), st, nil).run()
	h.requireActive(r1, state.StateUp)

	tun := oneNode()
	tun.Ladder = []string{"hysteria2/udp"}
	st = state.TunnelState{State: state.StateStarting, Active: cand("de-1", "gone/x"),
		Skipped: map[string]state.Skip{"de-1/hysteria2/udp": {Reason: "udp"}}}
	h2 := newHarness(t, tun, st, nil).run()
	require.Equal(t, state.StateDown, h2.st().State)
}

func TestInterruptedCycleResumesAndCompletesSwitch(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	h.act.set(func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blockedUntil[r2.Key()] = f.clk.Now().Add(20 * time.Second) // slow to come up
	})
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateSwitching && s.Active == r2 })
	require.NoError(t, h.wait(h.async(func() error { return h.eng.UpdateConfig(ctx, twoNodes()) })))
	h.advanceUntil(time.Minute, isUpOn(r2))
	sw := h.act.eventsOf(state.EvSwitchTransport)
	require.Len(t, sw, 1)
	require.Equal(t, r1.Transport, sw[0].FromTransport)
	require.Len(t, h.act.callsOf("start", r2.Key()), 1, "the interrupted candidate is verified, not restarted")
}

func TestDownRetrySeedPerPolicy(t *testing.T) {
	tun := twoNodes()
	tun.Settings.Policy = config.PolicyNodeOnly
	tun.Ladder = []string{"backhaul/wssmux", "backhaul/tcpmux"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		for _, k := range []string{r1.Key(), r2.Key(), b1.Key(), b2.Key()} {
			f.blocked[k] = true
		}
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	h.act.clearCalls()
	h.advance(time.Minute)
	require.Equal(t, []string{"start " + r1.Key(), "stop " + r1.Key(), "start " + b1.Key(), "stop " + b1.Key()},
		h.act.opSequence("start", "stop"), "node_only retries the same rung on every node")

	tun.Settings.Policy = config.PolicyTransportOnly
	st := state.TunnelState{State: state.StateDown, Active: b2, DownRetryAt: t0}
	h2 := newHarness(t, tun, st, func(f *fakeActions) {
		for _, k := range []string{b1.Key(), b2.Key()} {
			f.blocked[k] = true
		}
	}).run()
	h2.advance(40 * time.Second)
	require.Equal(t, []string{"start " + b1.Key(), "stop " + b1.Key(), "start " + b2.Key(), "stop " + b2.Key()},
		h2.act.opSequence("start", "stop"), "transport_only retries the rungs of its node")
}

func TestDownRetryInterruptedByManualSwitch(t *testing.T) {
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 }) // retry under way
	h.act.block(dn.Key(), false)
	require.NoError(t, h.wait(h.async(func() error { return h.eng.SwitchTransport(context.Background(), "direct/native") })))
	h.idle()
	h.requireActive(dn, state.StateUp)
}

func TestFlappingFallbackFailsHolds(t *testing.T) {
	st := upOn(r1, t0)
	for i := 0; i < 6; i++ {
		st.SwitchTimes = append(st.SwitchTimes, t0.Add(-time.Minute))
	}
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r1.Key()] = true
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advance(40 * time.Second)
	s := h.st()
	require.Equal(t, dn, s.Active)
	require.Equal(t, state.StateDegraded, s.State)
	require.True(t, s.Flapping)
	require.Contains(t, s.TransitionCause, "does not pass either")
	h.advance(5 * time.Minute)
	require.Len(t, h.act.callsOf("start", ""), 1, "held on direct/native")
}

func TestPauseDuringFailback(t *testing.T) {
	st := upOn(r2, t0.Add(-time.Hour))
	h := newHarness(t, twoNodes(), st, func(f *fakeActions) {
		f.running[r2.Key()] = true
		f.blocked[r1.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.Active == r1 })
	require.NoError(t, h.eng.Pause(context.Background()))
	// Pause is answered inside the failback's wait; the sequence then leaves
	// (afterPreempt) a moment later, while its old timer still counts as a
	// sleeper for idle(). Wait for the state the sequence ends in.
	require.Eventually(t, func() bool { return h.st().State == state.StatePaused }, idleWait, time.Millisecond)
	h.idle()
	s := h.st()
	require.Equal(t, state.StatePaused, s.State)
	require.Equal(t, r1, s.Active)
	require.Empty(t, h.act.eventsOf(state.EvFailbackFailed))
}

func TestManualSwitchWhilePaused(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	require.NoError(t, h.eng.Pause(ctx))
	require.NoError(t, h.eng.SwitchTransport(ctx, r3.Transport))
	h.requireActive(r3, state.StatePaused)

	// Target and previous both fail: held (paused, no switching).
	h.act.set(func(f *fakeActions) {
		f.blocked[r3.Key()] = true
		f.blocked[r2.Key()] = true
	})
	ch := h.async(func() error { return h.eng.SwitchTransport(ctx, r2.Transport) })
	h.advance(40 * time.Second)
	require.Equal(t, deyerr.B004, codeOf(h.wait(ch)))
	h.requireActive(r3, state.StatePaused)
	require.Empty(t, h.act.callsOf("start", "de-1/frp/tcp"))
}

func TestManualSwitchWhilePausedAndDown(t *testing.T) {
	ctx := context.Background()
	tun := oneNode()
	tun.Ladder = []string{"backhaul/wssmux", "direct/native"}
	h := newHarness(t, tun, state.TunnelState{}, func(f *fakeActions) {
		f.blocked[r1.Key()] = true
		f.blocked[dn.Key()] = true
	}).run()
	h.advanceUntil(time.Minute, func(s state.TunnelState) bool { return s.State == state.StateDown })
	require.NoError(t, h.eng.Pause(ctx))
	ch := h.async(func() error { return h.eng.SwitchTransport(ctx, "direct/native") })
	h.advance(20 * time.Second)
	require.Equal(t, deyerr.B004, codeOf(h.wait(ch)))
	require.Equal(t, state.StatePaused, h.st().State)

	// Resumed during a ladder test: back to DOWN with an immediate retry.
	ch = h.async(func() error {
		_, err := h.eng.TestLadder(ctx, nil)
		return err
	})
	require.NoError(t, h.eng.Resume(ctx))
	h.advance(45 * time.Second)
	require.NoError(t, h.wait(ch))
	require.Equal(t, state.StateDown, h.st().State)
}

func TestTestLadderWhilePaused(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, twoNodes(), state.TunnelState{}, nil).run()
	require.NoError(t, h.eng.Pause(ctx))
	ch := h.async(func() error {
		_, err := h.eng.TestLadder(ctx, nil)
		return err
	})
	h.advance(time.Minute)
	require.NoError(t, h.wait(ch))
	h.requireActive(r1, state.StatePaused)
}
