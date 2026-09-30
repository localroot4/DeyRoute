package failover

import (
	"context"
	"fmt"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// outcome of moving to a candidate and waiting for its probe.
type outcome int

const (
	outOK      outcome = iota // the probe passed
	outFail                   // start failed or no probe passed in the window
	outStop                   // the engine context is done
	outPreempt                // interrupted (owner command, pause, aborted request)
)

type attempt struct {
	out        outcome
	probe      Probe
	err        error // Start error
	nodeBroken bool  // failed while the candidate's node is broken
	reason     string
}

// moveTo makes c the active candidate: it stops the running one (if
// different), persists the new active candidate before starting it (so a hub
// restart can reconcile), starts it unless it already runs, and waits up to
// window for a successful probe.
func (e *Engine) moveTo(ctx context.Context, c state.Candidate, window time.Duration, w waitOpts) attempt {
	var stopErr error
	if e.running && e.st.Active != c {
		stopErr = e.stop(ctx, e.st.Active)
		e.running = false
	}
	if ctx.Err() != nil {
		return attempt{out: outStop}
	}
	if e.st.Active != c {
		e.st.Active = c
		e.hist.reset()
	}
	e.commit(true)
	if !e.running {
		err := e.start(ctx, c)
		// Even a failed start may leave one side running: the caller stops it.
		e.running = true
		if ctx.Err() != nil {
			return attempt{out: outStop}
		}
		if err != nil {
			return attempt{out: outFail, err: err, reason: "start " + c.Key() + ": " + err.Error()}
		}
	}
	a := e.waitProbe(ctx, c, window, w)
	if stopErr != nil && a.out == outFail {
		a.reason += " (stopping the previous candidate failed: " + stopErr.Error() + ")"
	}
	return a
}

// waitProbe polls c every StartPoll until a probe passes or window elapses.
// A failing probe while c's node is broken ends the wait early.
func (e *Engine) waitProbe(ctx context.Context, c state.Candidate, window time.Duration, w waitOpts) attempt {
	deadline := e.clk.Now().Add(window)
	for {
		began := e.clk.Now()
		// Within the window; the last poll (at the deadline) gets StartPoll.
		limit := deadline.Sub(began)
		if limit < StartPoll {
			limit = StartPoll
		}
		p := e.probePath(ctx, c, limit)
		if ctx.Err() != nil {
			return attempt{out: outStop}
		}
		if p.OK {
			return attempt{out: outOK, probe: p}
		}
		if e.nodeBroken(c.Node) {
			return attempt{out: outFail, probe: p, nodeBroken: true,
				reason: fmt.Sprintf("%s: %s; control connection of node %s offline", c.Key(), p.Err, c.Node)}
		}
		if !e.clk.Now().Before(deadline) {
			return attempt{out: outFail, probe: p,
				reason: fmt.Sprintf("%s: no successful probe within %s: %s", c.Key(), window, p.Err)}
		}
		wake := began.Add(StartPoll)
		if wake.After(deadline) {
			wake = deadline
		}
		switch e.sleepUntil(ctx, wake, w) {
		case sleepStop:
			return attempt{out: outStop}
		case sleepPreempt, sleepAbort:
			return attempt{out: outPreempt, probe: p}
		}
	}
}

// attemptErr is the owner-visible error of a failed attempt.
func attemptErr(c state.Candidate, a attempt, window time.Duration) *deyerr.Error {
	if a.err != nil {
		return deyerr.As(a.err)
	}
	return deyerr.New(deyerr.B004, deyerr.Params{"transport": c.Transport, "seconds": int(window / time.Second)}).
		WithDetail(a.reason)
}

// upAfterMove records a successful move to the active candidate and sets UP
// (PAUSED while paused). Callers emit their event and commit.
func (e *Engine) upAfterMove(p Probe, cause string) {
	now := e.clk.Now()
	st := &e.st
	st.FailCount = 0
	st.RecoverCount = 1
	st.LastRTTms = ms(p.RTT)
	st.LastProbeErr = ""
	st.ServiceDown = false
	st.Tried = nil
	st.DownBackoff = 0
	st.DownRetryAt = time.Time{}
	st.CanaryPasses = 0
	st.UpSince = now
	delete(st.Quarantine, st.Active.Key())
	e.hist.reset()
	e.hist.add(now, p.RTT)
	e.nextProbe = now.Add(e.cfg.probeInterval)
	if st.Paused {
		e.setState(state.StatePaused, cause)
		return
	}
	e.setState(state.StateUp, cause)
	st.StableSince = now
}

// holdPaused keeps a failing candidate while failover is paused.
func (e *Engine) holdPaused(reason string) {
	e.st.Tried = nil
	if e.st.FailCount < 1 {
		e.st.FailCount = 1
	}
	e.st.LastProbeErr = reason
	e.nextProbe = e.clk.Now().Add(e.cfg.probeInterval)
	e.setState(state.StatePaused, "failover paused: "+reason)
	e.commit(true)
}

// seqKind names the automatic sequence that was interrupted.
type seqKind int

const (
	seqStarting  seqKind = iota // STARTING (or an unknown sequence): success = tunnel_up
	seqCycle                    // switching cycle: success = switch_transport / switch_node
	seqFailback                 // failback to rung 1: success = failback, failure = revert + doubled delay
	seqRevert                   // return to the previous candidate after a failed failback
	seqFlapping                 // move to the fallback transport while flapping (not counted)
	seqDownRetry                // DOWN retry: success = tunnel_up, failure = back to DOWN
)

// interruption records an automatic sequence cut short by an owner command.
type interruption struct {
	kind   seqKind
	origin state.Candidate // the candidate before the sequence (failback: revert target; revert: the failed rung 1)
	target state.Candidate // the candidate that was being tried (still running, unverified)
	why    string
}

// afterPreempt leaves an automatic sequence for an owner command: the
// candidate being tried keeps running, unverified; resumeSwitching finishes
// the sequence later (it) unless the tunnel is paused.
func (e *Engine) afterPreempt(it interruption) {
	e.st.Tried = nil
	e.nextProbe = e.clk.Now()
	if e.st.Paused {
		e.interrupted = interruption{}
		e.st.FailCount = 0
		e.setState(state.StatePaused, "failover paused by owner")
	} else {
		e.interrupted = it
		e.setState(state.StateSwitching, "interrupted by an owner command")
	}
	e.commit(true)
}

// afterStartFailure handles a candidate that did not pass its probe after
// being started outside a switching cycle (STARTING, an interrupted
// sequence, a revert, the end of a ladder test): it is the same decision as
// fail_threshold consecutive failures, so node_service is checked before any
// transport switch (section 9). alsoTried are excluded from the cycle.
func (e *Engine) afterStartFailure(ctx context.Context, c state.Candidate, a attempt, alsoTried ...state.Candidate) {
	e.emitAttemptFailure(c, a)
	if e.st.FailCount < e.cfg.failThreshold {
		e.st.FailCount = e.cfg.failThreshold
	}
	e.st.RecoverCount = 0
	e.st.LastProbeErr = a.reason
	e.nextProbe = e.clk.Now().Add(e.cfg.probeInterval)
	e.onThreshold(ctx, a.reason, alsoTried...)
}

// ---- INIT / STARTING

// initialize picks the initial candidate: the persisted active one when it
// is still valid, else rung 1 of the primary node.
func (e *Engine) initialize(ctx context.Context) {
	c := e.st.Active
	if !e.valid(c) || e.skipped(c) {
		h, ok := e.home()
		if !ok {
			e.enterDown(ctx, "no usable candidate: every rung is skipped")
			return
		}
		c = h
	}
	e.st.Active = c
	e.setState(state.StateStarting, "starting "+c.Key())
	e.commit(true)
}

// runStarting starts the active candidate and waits for its first probe.
func (e *Engine) runStarting(ctx context.Context) {
	c := e.st.Active
	if !e.valid(c) {
		h, ok := e.home()
		if !ok {
			e.enterDown(ctx, "no usable candidate: every rung is skipped")
			return
		}
		c = h
	}
	a := e.moveTo(ctx, c, StartWait, waitOpts{preemptible: true})
	switch a.out {
	case outStop:
		return
	case outPreempt:
		e.afterPreempt(interruption{kind: seqStarting, origin: e.st.Previous, target: c})
		return
	case outOK:
		e.startedUp(e.st.Previous, c, a.probe, "first probe passed after start")
		return
	}
	if e.st.Paused {
		e.holdPaused(a.reason)
		return
	}
	e.afterStartFailure(ctx, c, a)
}

// startedUp records a started candidate that passed its probe outside a
// switch (STARTING, DOWN retry): UP + tunnel_up, not counted.
func (e *Engine) startedUp(from, c state.Candidate, p Probe, cause string) {
	e.upAfterMove(p, cause)
	e.emit(evt{typ: state.EvTunnelUp, level: state.LevelInfo, from: from, to: c, reason: cause})
	e.commit(true)
}

// resumeSwitching finishes an automatic sequence an owner command
// interrupted (see afterPreempt): the candidate that was being tried is
// verified first, then the sequence ends with its own semantics (a failback
// still emits failback / failback_failed and is not counted twice, a DOWN
// retry goes back to DOWN, and so on).
func (e *Engine) resumeSwitching(ctx context.Context) {
	it := e.interrupted
	e.interrupted = interruption{}
	c := e.st.Active
	if !e.valid(c) {
		h, ok := e.home()
		if !ok {
			e.enterDown(ctx, "no usable candidate: every rung is skipped")
			return
		}
		c = h
	}
	if it.target != c {
		// No record of the sequence (or the active candidate changed since):
		// verify it like a start.
		it = interruption{kind: seqStarting, origin: e.st.Previous, target: c}
	}
	a := e.moveTo(ctx, c, StartWait, waitOpts{preemptible: true})
	switch a.out {
	case outStop:
		return
	case outPreempt:
		e.afterPreempt(it)
		return
	case outOK:
		cause := "probe passed on " + c.Key()
		switch {
		case it.kind == seqCycle && !it.origin.IsZero() && it.origin != c:
			e.switchSucceeded(it.origin, c, a.probe, it.why)
		case it.kind == seqFailback:
			e.failbackSucceeded(it.origin, c, a.probe, it.why)
		case it.kind == seqRevert:
			e.revertSucceeded(it.origin, c, a.probe)
		case it.kind == seqFlapping:
			e.flappingHeld(c, a.probe)
		default: // seqStarting, seqDownRetry
			if it.kind == seqDownRetry {
				e.st.Previous = it.origin
			}
			e.startedUp(it.origin, c, a.probe, cause)
		}
		return
	}
	if e.st.Paused {
		e.holdPaused(a.reason)
		return
	}
	switch it.kind {
	case seqFailback:
		e.failbackFailed(ctx, it.origin, c, a)
	case seqRevert:
		e.afterStartFailure(ctx, c, a, it.origin)
	case seqFlapping:
		e.flappingFallbackFailed(c, a)
	case seqDownRetry:
		// Retry the whole ladder again at once (the interrupted retry did
		// not finish); the backoff is unchanged.
		e.stopActive(ctx)
		if ctx.Err() != nil {
			return
		}
		e.st.Tried = nil
		e.st.DownRetryAt = e.clk.Now()
		e.setState(state.StateDown, "retry interrupted: "+a.reason)
		e.commit(true)
	default: // seqStarting, seqCycle
		e.afterStartFailure(ctx, c, a)
	}
}

// ---- probing (UP, DEGRADED, PAUSED)

func (e *Engine) probeTick(ctx context.Context, now time.Time) {
	e.nextProbe = now.Add(e.cfg.probeInterval)
	c := e.st.Active
	p := e.probePath(ctx, c, e.cfg.probeTimeout)
	if ctx.Err() != nil {
		return
	}
	var high bool
	var median time.Duration
	if p.OK {
		e.st.LastRTTms = ms(p.RTT)
		e.st.LastProbeErr = ""
		e.st.FailCount = 0
		e.st.RecoverCount++
		e.st.ServiceDown = false
		high, median = e.hist.add(now, p.RTT)
	} else {
		e.st.LastProbeErr = p.Err
		e.st.FailCount++
		e.st.RecoverCount = 0
	}
	if e.st.Paused {
		e.commit(false)
		return
	}
	e.canaryTick(ctx)
	if p.OK {
		if high {
			e.toDegraded(fmt.Sprintf("rtt %dms above %dx the 10-minute median %dms for %s",
				ms(p.RTT), RTTHighFactor, ms(median), RTTHighFor))
		} else {
			e.toUp("probe passed")
		}
		e.maybeFailback(ctx)
		e.commit(false)
		return
	}
	if e.st.FailCount < e.cfg.failThreshold {
		e.toDegraded(p.Err)
		return
	}
	e.onThreshold(ctx, p.Err)
}

// toUp is the probe-driven transition to UP.
func (e *Engine) toUp(cause string) {
	if e.st.State == state.StateUp {
		return
	}
	now := e.clk.Now()
	e.setState(state.StateUp, cause)
	e.st.StableSince = now
	if e.st.UpSince.IsZero() {
		e.st.UpSince = now
	}
	e.emit(evt{typ: state.EvTunnelUp, level: state.LevelInfo, from: e.st.Active, to: e.st.Active, reason: cause})
	e.commit(true)
}

// toDegraded is the transition to DEGRADED (yellow; no action by itself).
func (e *Engine) toDegraded(cause string) {
	if e.st.State == state.StateDegraded {
		e.commit(false)
		return
	}
	e.setState(state.StateDegraded, cause)
	e.emit(evt{typ: state.EvTunnelDegraded, level: state.LevelWarn, from: e.st.Active, to: e.st.Active, reason: cause})
	e.commit(true)
}

// onThreshold runs when fail_threshold consecutive probes failed (or a
// started candidate did not pass): service down → backup node or hold,
// flapping → fallback transport or hold, else a switching cycle that skips
// alsoTried.
func (e *Engine) onThreshold(ctx context.Context, reason string, alsoTried ...state.Candidate) {
	active := e.st.Active
	up, known := e.nodeService(ctx, active.Node)
	if ctx.Err() != nil {
		return
	}
	if known && !up {
		e.onServiceDown(ctx, reason)
		return
	}
	if e.flapLimited() {
		e.onFlapping(ctx, reason)
		return
	}
	opts := cycleOpts{reason: reason, quarantineOrigin: true, alsoTried: append([]state.Candidate(nil), alsoTried...)}
	if e.nodeBroken(active.Node) {
		// Transport switches on a node that is gone are pointless.
		opts.quarantineOrigin = false
		opts.alsoTried = append(opts.alsoTried, nodeCandidates(active.Node, e.t.Ladder)...)
		opts.reason = reason + "; control connection of node " + active.Node + " offline"
	}
	e.switchCycle(ctx, opts)
}

// onServiceDown: the service behind the tunnel is down on the active node,
// so switching transport would not help (section 9). Go to a backup node
// when the policy allows, else stay DEGRADED (service down).
func (e *Engine) onServiceDown(ctx context.Context, reason string) {
	active := e.st.Active
	if !e.st.ServiceDown {
		e.st.ServiceDown = true
		e.emit(evt{typ: state.EvServiceDown, level: state.LevelError, from: active, to: active, reason: reason,
			err: deyerr.New(deyerr.F005, deyerr.Params{"node": active.Node, "target": e.serviceTarget()})})
	}
	cause := "service down on node " + active.Node + ": " + reason
	if target, ok := e.serviceDownTarget(ctx); ok {
		if !e.flapLimited() {
			e.switchCycle(ctx, cycleOpts{reason: cause, first: target, alsoTried: nodeCandidates(active.Node, e.t.Ladder)})
			return
		}
		e.markFlapping(reason, active)
		cause += "; flapping limit reached"
	}
	e.toDegraded(cause)
	e.st.TransitionCause = cause
	e.commit(true)
}

func (e *Engine) serviceTarget() string {
	if e.t.ServiceTarget != "" {
		return e.t.ServiceTarget
	}
	return "behind the tunnel"
}

// serviceDownTarget is the same rung on the next node (or, for
// transport_then_node, rung 1 of the next node when that rung is excluded),
// on a node whose service is not known to be down.
func (e *Engine) serviceDownTarget(ctx context.Context) (state.Candidate, bool) {
	if e.cfg.policy == config.PolicyTransportOnly {
		return state.Candidate{}, false
	}
	active := e.st.Active
	tried := map[string]bool{}
	for _, c := range nodeCandidates(active.Node, e.t.Ladder) {
		tried[c.Key()] = true
	}
	base := e.excluder(false)
	downNodes := map[string]bool{active.Node: true}
	exc := func(c state.Candidate) bool {
		if base(c) {
			return true
		}
		down, seen := downNodes[c.Node]
		if !seen {
			up, known := e.nodeService(ctx, c.Node)
			down = known && !up
			downNodes[c.Node] = down
		}
		return down
	}
	if c, ok := NextCandidate(config.PolicyNodeOnly, e.t.Nodes, e.t.Ladder, active, tried, exc); ok {
		return c, true
	}
	if e.cfg.policy == config.PolicyTransportThenNode {
		return NextCandidate(config.PolicyTransportThenNode, e.t.Nodes, e.t.Ladder, active, tried, exc)
	}
	return state.Candidate{}, false
}

// ---- SWITCHING

type cycleOpts struct {
	reason           string
	first            state.Candidate   // forced first candidate (service down → backup node)
	alsoTried        []state.Candidate // treated as already tried in this cycle
	quarantineOrigin bool              // the active candidate failed its probes
}

// switchCycle is the SWITCHING state: next candidate per policy → stop the
// active one → start the candidate → wait up to StartWait for its probe;
// success = UP + switch event, failure = quarantine and the next one; none
// left = DOWN.
func (e *Engine) switchCycle(ctx context.Context, o cycleOpts) {
	origin := e.st.Active
	e.st.Previous = origin
	tried := map[string]bool{origin.Key(): true}
	for _, c := range o.alsoTried {
		tried[c.Key()] = true
	}
	if o.quarantineOrigin {
		e.quarantine(origin)
	}
	e.st.Tried = sortedKeys(tried)
	e.setState(state.StateSwitching, o.reason)
	e.commit(true)

	cur := origin
	next := o.first
	for {
		if next.IsZero() {
			c, ok := NextCandidate(e.cfg.policy, e.t.Nodes, e.t.Ladder, cur, tried, e.excluder(false))
			if !ok {
				e.enterDown(ctx, o.reason)
				return
			}
			next = c
		}
		c := next
		next = state.Candidate{}
		tried[c.Key()] = true
		e.st.Tried = sortedKeys(tried)
		a := e.moveTo(ctx, c, StartWait, waitOpts{preemptible: true})
		switch a.out {
		case outStop:
			return
		case outPreempt:
			e.afterPreempt(interruption{kind: seqCycle, origin: origin, target: c, why: o.reason})
			return
		case outOK:
			e.switchSucceeded(origin, c, a.probe, o.reason)
			return
		}
		e.emitAttemptFailure(c, a)
		e.stopActive(ctx)
		if ctx.Err() != nil {
			return
		}
		if a.nodeBroken {
			for _, x := range nodeCandidates(c.Node, e.t.Ladder) {
				tried[x.Key()] = true
			}
		} else {
			e.quarantine(c)
		}
		cur = c
	}
}

// switchSucceeded finishes an automatic switch from origin to c.
func (e *Engine) switchSucceeded(origin, c state.Candidate, p Probe, reason string) {
	now := e.clk.Now()
	e.st.Previous = origin
	e.st.SwitchTimes = append(e.st.SwitchTimes, now)
	e.st.LastSwitch = now
	e.upAfterMove(p, "switched from "+origin.Key()+": "+reason)
	typ := state.EvSwitchTransport
	if c.Node != origin.Node {
		typ = state.EvSwitchNode
	}
	e.emit(evt{typ: typ, level: state.LevelWarn, from: origin, to: c, reason: reason})
	e.commit(true)
}

// emitAttemptFailure reports one failed candidate of a switching cycle.
func (e *Engine) emitAttemptFailure(c state.Candidate, a attempt) {
	e.emit(evt{typ: state.EvProbeError, level: state.LevelWarn, from: c, to: c, reason: a.reason,
		err: attemptErr(c, a, StartWait)})
}

// enterDown: no candidate left (F001). Nothing runs in DOWN; the ladder is
// retried from rung 1 after DownRetryInitial, doubling up to DownRetryMax.
func (e *Engine) enterDown(ctx context.Context, reason string) {
	e.stopActive(ctx)
	if ctx.Err() != nil {
		return
	}
	now := e.clk.Now()
	e.st.Tried = nil
	e.st.FailCount = 0
	e.st.ServiceDown = false
	e.st.UpSince = time.Time{}
	e.st.DownBackoff = DownRetryInitial
	e.st.DownRetryAt = now.Add(DownRetryInitial)
	e.setState(state.StateDown, reason)
	e.emit(evt{typ: state.EvTunnelDown, level: state.LevelError, from: e.st.Active, reason: reason,
		err: deyerr.New(deyerr.F001, nil)})
	e.commit(true)
}

// retrySeed is the position NextCandidate starts a DOWN retry from: rung 1
// of the primary node (transport_then_node), rung 1 of the current node
// (transport_only), or the current rung on the primary node (node_only).
func (e *Engine) retrySeed() state.Candidate {
	switch e.cfg.policy {
	case config.PolicyTransportOnly:
		return state.Candidate{Node: e.st.Active.Node}
	case config.PolicyNodeOnly:
		return state.Candidate{Transport: e.st.Active.Transport}
	}
	return state.Candidate{}
}

// downRetry tries the whole ladder again (quarantine does not apply: every
// candidate failed, so the cause is not one transport). Failed attempts
// here are not quarantined and emit no per-candidate event.
func (e *Engine) downRetry(ctx context.Context) {
	exc := e.excluder(true)
	origin := e.st.Active
	tried := map[string]bool{}
	reason := "no usable candidate"
	c, ok := NextCandidate(e.cfg.policy, e.t.Nodes, e.t.Ladder, e.retrySeed(), tried, exc)
	for ok {
		tried[c.Key()] = true
		e.st.Tried = sortedKeys(tried)
		a := e.moveTo(ctx, c, StartWait, waitOpts{preemptible: true})
		switch a.out {
		case outStop:
			return
		case outPreempt:
			e.afterPreempt(interruption{kind: seqDownRetry, origin: origin, target: c})
			return
		case outOK:
			e.st.Previous = origin
			e.startedUp(origin, c, a.probe, "recovered: probe passed on "+c.Key())
			return
		}
		reason = a.reason
		e.stopActive(ctx)
		if ctx.Err() != nil {
			return
		}
		if a.nodeBroken {
			for _, x := range nodeCandidates(c.Node, e.t.Ladder) {
				tried[x.Key()] = true
			}
		}
		c, ok = NextCandidate(e.cfg.policy, e.t.Nodes, e.t.Ladder, c, tried, exc)
	}
	b := e.st.DownBackoff * 2
	if b < DownRetryInitial {
		b = DownRetryInitial
	}
	if b > DownRetryMax {
		b = DownRetryMax
	}
	e.st.DownBackoff = b
	e.st.DownRetryAt = e.clk.Now().Add(b)
	e.st.Tried = nil
	e.setState(state.StateDown, "retry failed: "+reason)
	e.commit(true)
}

// ---- anti-flapping

// markFlapping sets the flapping flag and emits the event once (F002).
func (e *Engine) markFlapping(reason string, to state.Candidate) {
	if e.st.Flapping {
		return
	}
	e.st.Flapping = true
	e.emit(evt{typ: state.EvFlapping, level: state.LevelError, from: e.st.Active, to: to, reason: reason,
		err: deyerr.New(deyerr.F002, deyerr.Params{"count": len(e.st.SwitchTimes)})})
}

// onFlapping: a switch is needed but max_switches_per_hour is reached. The
// tunnel moves to the fallback transport on its node and stays there; no
// further automatic switch happens until the hour window clears.
func (e *Engine) onFlapping(ctx context.Context, reason string) {
	active := e.st.Active
	fb := state.Candidate{Node: active.Node, Transport: e.t.FallbackTransport}
	canMove := active != fb && indexOf(e.t.Ladder, fb.Transport) >= 0 && !e.skipped(fb)
	if !canMove {
		e.markFlapping(reason, active)
		e.toDegraded("flapping limit reached, holding " + active.Key() + ": " + reason)
		e.commit(true)
		return
	}
	e.markFlapping(reason, fb)
	e.st.Previous = active
	e.setState(state.StateSwitching, "flapping limit reached: moving to "+fb.Key())
	e.commit(true)
	a := e.moveTo(ctx, fb, StartWait, waitOpts{preemptible: true})
	switch a.out {
	case outStop:
		return
	case outPreempt:
		e.afterPreempt(interruption{kind: seqFlapping, origin: active, target: fb, why: reason})
		return
	case outOK:
		e.flappingHeld(fb, a.probe)
		return
	}
	e.flappingFallbackFailed(fb, a)
}

// flappingHeld: the fallback transport passed; the tunnel holds it (the move
// is not counted as a switch).
func (e *Engine) flappingHeld(fb state.Candidate, p Probe) {
	e.upAfterMove(p, "flapping limit reached: holding on "+fb.Key())
	e.commit(true)
}

// flappingFallbackFailed: even the fallback does not pass; hold it anyway
// (no more automatic switching until the hour window clears).
func (e *Engine) flappingFallbackFailed(fb state.Candidate, a attempt) {
	e.st.FailCount = e.cfg.failThreshold
	e.st.RecoverCount = 0
	e.st.LastProbeErr = a.reason
	e.nextProbe = e.clk.Now().Add(e.cfg.probeInterval)
	e.toDegraded("flapping limit reached; " + fb.Key() + " does not pass either: " + a.reason)
	e.commit(true)
}

// ---- failback (phase 5 blind, phase 8 canary)

func (e *Engine) canaryTick(ctx context.Context) {
	target, ok := e.home()
	if !e.cfg.failback || !ok || target == e.st.Active {
		e.st.CanaryPasses = 0
		return
	}
	pctx, cancel := context.WithTimeout(ctx, e.cfg.probeTimeout)
	p, configured := e.a.ProbeCanary(pctx)
	cancel()
	e.canary = configured
	switch {
	case !configured || !p.OK:
		e.st.CanaryPasses = 0
	default:
		e.st.CanaryPasses++
	}
}

// maybeFailback returns to rung 1 of the primary node when the tunnel has
// been UP elsewhere for FailbackDelay (blind) or the canary passed
// recover_threshold times in a row.
func (e *Engine) maybeFailback(ctx context.Context) {
	if !e.cfg.failback || e.st.Paused || e.st.State != state.StateUp {
		return
	}
	target, ok := e.home()
	if !ok || target == e.st.Active {
		return
	}
	now := e.clk.Now()
	var why string
	if e.canary {
		if e.st.CanaryPasses < e.cfg.recoverThreshold {
			return
		}
		why = fmt.Sprintf("canary passed %d probes in a row", e.st.CanaryPasses)
	} else {
		if now.Sub(e.st.StableSince) < e.st.FailbackDelay {
			return
		}
		why = fmt.Sprintf("up on %s for %s", e.st.Active.Key(), e.st.FailbackDelay)
	}
	if online, _ := e.a.ControlOnline(target.Node); !online {
		return
	}
	if up, known := e.nodeService(ctx, target.Node); known && !up {
		return
	}
	if ctx.Err() != nil {
		return
	}
	if e.flapLimited() {
		// "If UP, stay": the failback is an automatic switch too.
		e.markFlapping("failback held: "+why, e.st.Active)
		e.commit(true)
		return
	}
	e.failback(ctx, target, why)
}

func (e *Engine) failback(ctx context.Context, target state.Candidate, why string) {
	origin := e.st.Active
	now := e.clk.Now()
	e.st.Previous = origin
	e.st.SwitchTimes = append(e.st.SwitchTimes, now)
	e.st.LastSwitch = now
	e.setState(state.StateSwitching, "failback to "+target.Key()+": "+why)
	e.commit(true)
	a := e.moveTo(ctx, target, StartWait, waitOpts{preemptible: true})
	switch a.out {
	case outStop:
		return
	case outPreempt:
		e.afterPreempt(interruption{kind: seqFailback, origin: origin, target: target, why: why})
		return
	case outOK:
		e.failbackSucceeded(origin, target, a.probe, why)
		return
	}
	e.failbackFailed(ctx, origin, target, a)
}

// failbackSucceeded: rung 1 passed; the failback delay is reset.
func (e *Engine) failbackSucceeded(origin, target state.Candidate, p Probe, why string) {
	e.st.Previous = origin
	e.st.FailbackDelay = e.cfg.failbackAfter
	e.upAfterMove(p, "failback: "+why)
	e.emit(evt{typ: state.EvFailback, level: state.LevelInfo, from: origin, to: target, reason: why})
	e.commit(true)
}

// revertSucceeded: the previous candidate is back after a failed failback.
func (e *Engine) revertSucceeded(failed, c state.Candidate, p Probe) {
	e.st.Previous = failed
	e.upAfterMove(p, "failback failed; back on "+c.Key())
	e.commit(true)
}

// failbackFailed: rung 1 did not pass within StartWait. Return to the
// previous candidate at once and double the failback delay (cap 24 h,
// persisted) — section 9, phase 5.
func (e *Engine) failbackFailed(ctx context.Context, origin, target state.Candidate, a attempt) {
	e.stopActive(ctx)
	if ctx.Err() != nil {
		return
	}
	d := e.st.FailbackDelay * 2
	if d < e.cfg.failbackAfter {
		d = e.cfg.failbackAfter
	}
	if d > FailbackDelayMax {
		d = FailbackDelayMax
	}
	e.st.FailbackDelay = d
	e.st.CanaryPasses = 0
	e.emit(evt{typ: state.EvFailbackFailed, level: state.LevelWarn, from: target, to: origin, reason: a.reason,
		err: deyerr.New(deyerr.F003, deyerr.Params{"transport": target.Transport})})
	e.commit(true)
	b := e.moveTo(ctx, origin, StartWait, waitOpts{preemptible: true})
	switch b.out {
	case outStop:
		return
	case outPreempt:
		e.afterPreempt(interruption{kind: seqRevert, origin: target, target: origin})
		return
	case outOK:
		e.revertSucceeded(target, origin, b.probe)
		return
	}
	if e.st.Paused {
		e.holdPaused(b.reason)
		return
	}
	b.reason = "previous candidate did not recover after the failback: " + b.reason
	e.afterStartFailure(ctx, origin, b, target)
}

// ---- resume

// resumeImplied moves a resumed tunnel to the state its probes imply. It
// reports whether fail_threshold is already reached, i.e. the caller must
// run the switching decision (onThreshold) now.
func (e *Engine) resumeImplied() bool {
	now := e.clk.Now()
	if !e.running {
		if e.st.DownBackoff <= 0 {
			e.st.DownBackoff = DownRetryInitial
		}
		e.st.DownRetryAt = now
		e.setState(state.StateDown, "failover resumed; no candidate running")
		e.commit(true)
		return false
	}
	switch {
	case e.st.FailCount >= e.cfg.failThreshold:
		e.toDegraded("failover resumed: " + e.st.LastProbeErr)
		return true
	case e.st.FailCount > 0:
		e.toDegraded("failover resumed: " + e.st.LastProbeErr)
	case e.hist.high(now):
		e.toDegraded("failover resumed: rtt high")
	default:
		e.toUp("failover resumed")
	}
	return false
}

// ---- manual commands (always allowed, never counted)

func (e *Engine) invalidTarget(target string) error {
	return deyerr.New(deyerr.F006, deyerr.Params{"tunnel": e.id, "target": target})
}

func (e *Engine) manualSwitchTransport(ctx context.Context, id string) error {
	if indexOf(e.t.Ladder, id) < 0 || len(e.t.Nodes) == 0 {
		return e.invalidTarget(id)
	}
	node := e.st.Active.Node
	if indexOf(e.t.Nodes, node) < 0 {
		node = e.t.Nodes[0]
	}
	return e.manualMove(ctx, state.Candidate{Node: node, Transport: id}, "manual switch to transport "+id)
}

func (e *Engine) manualSwitchNode(ctx context.Context, id string) error {
	if indexOf(e.t.Nodes, id) < 0 || len(e.t.Ladder) == 0 {
		return e.invalidTarget(id)
	}
	tr := e.st.Active.Transport
	if indexOf(e.t.Ladder, tr) < 0 {
		tr = e.t.Ladder[0]
	}
	return e.manualMove(ctx, state.Candidate{Node: id, Transport: tr}, "manual switch to node "+id)
}

func (e *Engine) manualReset(ctx context.Context) error {
	target, ok := e.home()
	if !ok {
		return e.invalidTarget("rung 1")
	}
	return e.manualMove(ctx, target, "reset to rung 1")
}

// manualMove switches to target on the owner's request. On failure the
// previous candidate is restored and the error returned.
func (e *Engine) manualMove(ctx context.Context, target state.Candidate, why string) error {
	if s, ok := e.st.Skipped[target.Key()]; ok {
		code := deyerr.Code(s.Code)
		if !deyerr.ValidCode(code) {
			code = deyerr.F006
		}
		return deyerr.New(code, deyerr.Params{"tunnel": e.id, "target": target.Key(), "transport": target.Transport,
			"reason": s.Reason}).WithDetail(s.Reason)
	}
	origin := e.st.Active
	wasRunning := e.running
	prev := e.st.State
	if wasRunning && origin == target &&
		(prev == state.StateUp || prev == state.StateDegraded || prev == state.StatePaused) {
		return nil
	}
	e.st.Previous = origin
	e.setState(state.StateSwitching, why)
	e.commit(true)
	a := e.moveTo(ctx, target, StartWait, waitOpts{})
	if a.out == outStop {
		return e.notRunning()
	}
	if a.out == outOK {
		e.upAfterMove(a.probe, why)
		e.emit(evt{typ: state.EvManualSwitch, level: state.LevelInfo, from: origin, to: target, reason: why})
		e.commit(true)
		return nil
	}
	err := attemptErr(target, a, StartWait)
	e.stopActive(ctx)
	if ctx.Err() != nil {
		return err
	}
	switch {
	case wasRunning && origin != target && e.valid(origin):
		b := e.moveTo(ctx, origin, StartWait, waitOpts{})
		switch b.out {
		case outStop:
			return err
		case outOK:
			e.st.Previous = target
			e.upAfterMove(b.probe, why+" failed; back on "+origin.Key())
			e.commit(true)
			return err
		}
		if e.st.Paused {
			e.holdPaused(b.reason)
			return err
		}
		b.reason = why + " failed and " + origin.Key() + " did not recover: " + b.reason
		e.afterStartFailure(ctx, origin, b, target)
	case e.st.Paused:
		e.setState(state.StatePaused, why+" failed")
		e.commit(true)
	case wasRunning:
		e.switchCycle(ctx, cycleOpts{reason: why + " failed: " + a.reason})
	default:
		if e.st.DownBackoff <= 0 {
			e.st.DownBackoff = DownRetryInitial
		}
		if e.st.DownRetryAt.IsZero() {
			e.st.DownRetryAt = e.clk.Now().Add(e.st.DownBackoff)
		}
		e.setState(state.StateDown, why+" failed")
		e.commit(true)
	}
	return err
}

// ---- test ladder

func (e *Engine) testLadder(ctx, req context.Context, onRung func(api.RungResult)) ([]api.RungResult, error) {
	if req == nil {
		req = context.Background()
	}
	original := e.st.Active
	wasRunning := e.running
	prevState := e.st.State
	prevCause := e.st.TransitionCause
	e.setState(state.StateSwitching, "testing the ladder (requested by owner)")
	e.commit(true)
	report := func(r api.RungResult, out *[]api.RungResult) {
		*out = append(*out, r)
		if onRung != nil {
			onRung(r)
		}
	}
	var results []api.RungResult
	var abortErr error
outer:
	for _, node := range e.t.Nodes {
		for _, rung := range e.t.Ladder {
			c := state.Candidate{Node: node, Transport: rung}
			r := api.RungResult{Node: node, Transport: rung}
			if s, ok := e.st.Skipped[c.Key()]; ok {
				r.Skipped = s.Reason
				if r.Skipped == "" {
					r.Skipped = "skipped"
				}
				report(r, &results)
				continue
			}
			if req.Err() != nil {
				abortErr = deyerr.Wrap(deyerr.F008, req.Err(), deyerr.Params{"tunnel": e.id, "command": cmdNames[cmdTestLadder]})
				break outer
			}
			a := e.moveTo(ctx, c, TestLadderRungWait, waitOpts{abort: req})
			switch a.out {
			case outStop:
				return results, e.notRunning()
			case outPreempt:
				e.stopActive(ctx)
				abortErr = deyerr.Wrap(deyerr.F008, req.Err(), deyerr.Params{"tunnel": e.id, "command": cmdNames[cmdTestLadder]})
				break outer
			case outOK:
				r.OK = true
				r.RTTms = ms(a.probe.RTT)
			default:
				r.Error = api.ToDTO(attemptErr(c, a, TestLadderRungWait))
			}
			e.stopActive(ctx)
			if ctx.Err() != nil {
				return results, e.notRunning()
			}
			report(r, &results)
		}
	}
	e.restoreAfterTest(ctx, original, wasRunning, prevState, prevCause)
	return results, abortErr
}

// restoreAfterTest brings back the candidate that was active before the
// ladder test.
func (e *Engine) restoreAfterTest(ctx context.Context, original state.Candidate, wasRunning bool, prevState, prevCause string) {
	if ctx.Err() != nil {
		return
	}
	if !e.valid(original) {
		e.stopActive(ctx)
		e.setState(state.StateInit, "ladder test finished")
		e.commit(true)
		return
	}
	if !wasRunning {
		// DOWN (or paused while down): nothing ran before the test.
		e.stopActive(ctx)
		e.st.Active = original
		switch {
		case e.st.Paused:
			e.setState(state.StatePaused, "ladder test finished")
		case prevState == state.StateDown:
			e.setState(state.StateDown, prevCause)
		default: // resumed during the test: retry now
			if e.st.DownBackoff <= 0 {
				e.st.DownBackoff = DownRetryInitial
			}
			e.st.DownRetryAt = e.clk.Now()
			e.setState(state.StateDown, "ladder test finished; no candidate running")
		}
		e.commit(true)
		return
	}
	a := e.moveTo(ctx, original, StartWait, waitOpts{})
	switch a.out {
	case outStop:
		return
	case outOK:
		e.upAfterMove(a.probe, "ladder test finished; back on "+original.Key())
		e.commit(true)
		return
	}
	if e.st.Paused {
		e.holdPaused(a.reason)
		return
	}
	a.reason = "active candidate did not recover after the ladder test: " + a.reason
	e.afterStartFailure(ctx, original, a)
}

// ---- configuration edits

func (e *Engine) updateConfig(ctx context.Context, t Tunnel) error {
	oldBase := e.cfg.failbackAfter
	t.ID = e.id // tunnel ids are immutable
	e.t = normalizeTunnel(t)
	e.cfg = effective(t.Settings)
	if e.cfg.failbackAfter != oldBase || e.st.FailbackDelay <= 0 {
		e.st.FailbackDelay = e.cfg.failbackAfter
	}
	for k := range e.st.Quarantine {
		if c, ok := parseKey(k); !ok || !e.valid(c) {
			delete(e.st.Quarantine, k)
		}
	}
	for k := range e.st.Skipped {
		if c, ok := parseKey(k); !ok || !e.valid(c) {
			delete(e.st.Skipped, k)
		}
	}
	if !e.valid(e.st.Active) {
		target, ok := e.home()
		switch {
		case !ok:
			e.enterDown(ctx, "no usable candidate after a configuration change")
			return nil
		case e.running:
			return e.manualMove(ctx, target, "active candidate removed by a configuration change")
		default:
			e.st.Active = target
		}
	}
	e.commit(true)
	return nil
}
