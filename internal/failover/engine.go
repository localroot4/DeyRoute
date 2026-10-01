package failover

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// Timings of section 9 that are not owner settings.
const (
	// StartWait is how long a started candidate may take to pass its first
	// probe (STARTING→UP, SWITCHING, failback, manual switch).
	StartWait = 15 * time.Second
	// StartPoll is the probe period while waiting for a started candidate.
	StartPoll = time.Second
	// DownRetryInitial is the first DOWN retry delay; it doubles after every
	// failed retry up to DownRetryMax.
	DownRetryInitial = 30 * time.Second
	// DownRetryMax caps the DOWN retry backoff.
	DownRetryMax = 5 * time.Minute
	// TestLadderRungWait is the probe window per rung in `test ladder`.
	TestLadderRungWait = 20 * time.Second
	// DefaultQuarantine is quarantine_s when the setting is zero.
	DefaultQuarantine = time.Duration(config.DefaultQuarantineS) * time.Second
	// QuarantineMax caps the doubling quarantine duration.
	QuarantineMax = time.Hour
	// QuarantineForget is how long an expired quarantine entry is kept so a
	// repeated failure doubles the duration.
	QuarantineForget = time.Hour
	// RTTBaselineWindow is the window of the RTT median for DEGRADED.
	RTTBaselineWindow = 10 * time.Minute
	// RTTHighFor is how long the RTT must stay high before DEGRADED.
	RTTHighFor = 60 * time.Second
	// RTTHighFactor is the "3x the median" factor.
	RTTHighFactor = 3
	// RTTMinBaseline is the number of samples the median needs.
	RTTMinBaseline = 3
	// FailbackDelayMax caps the doubling failback delay.
	FailbackDelayMax = 24 * time.Hour
	// SwitchWindow is the anti-flapping counting window.
	SwitchWindow = time.Hour
	// NodeBrokenAfter: a node whose control connection has been offline
	// longer than this while its path fails is broken.
	NodeBrokenAfter = 30 * time.Second
	// StartTimeout bounds one Actions.Start call.
	StartTimeout = 30 * time.Second
	// StopTimeout bounds one Actions.Stop call.
	StopTimeout = 20 * time.Second
	// NodeServiceTimeout bounds one Actions.NodeService call.
	NodeServiceTimeout = 5 * time.Second
	// PersistEvery is the longest interval between two Persist calls while
	// only probe figures (RTT, counters) change.
	PersistEvery = time.Minute
	// FallbackTransport is the anti-flapping destination and the transport
	// that is never quarantined (section 9).
	FallbackTransport = "direct/native"
)

// Probe is the result of one path (or canary) probe.
type Probe struct {
	OK  bool
	RTT time.Duration
	Err string // probe text used as the event reason
}

// Actions is everything the engine asks of the outside world. The daemon
// implements it; every method is called from the engine goroutine only, so
// implementations must not call back into the Engine synchronously.
type Actions interface {
	// Start starts candidate c: the server side of the backend first, then
	// the client side (section 3; reverse: hub then node, forward: node then
	// hub). Starting an already running candidate must be harmless.
	Start(ctx context.Context, c state.Candidate) error
	// Stop stops both sides of candidate c (idempotent).
	Stop(ctx context.Context, c state.Candidate) error
	// ProbePath runs the path probe of c through the tunnel. ctx carries the
	// probe_timeout_s deadline.
	ProbePath(ctx context.Context, c state.Candidate) Probe
	// ProbeCanary probes the canary unit of rung 1 (phase 8). configured is
	// false when the tunnel has no canary; failback is then blind (phase 5).
	ProbeCanary(ctx context.Context) (p Probe, configured bool)
	// NodeService reports the node_service probe of node: up, and whether
	// the result is known at all (false when the node is offline).
	NodeService(ctx context.Context, node string) (up bool, known bool)
	// ControlOnline reports the control connection of node and, when
	// offline, for how long.
	ControlOnline(node string) (online bool, offlineFor time.Duration)
	// Emit publishes an event (events ring, log, notifier). It must not block
	// for long.
	Emit(e state.Event)
	// Persist stores the tunnel state (tunnels/<id> in bbolt).
	Persist(ts state.TunnelState) error
}

// Tunnel is the engine's view of one tunnel's configuration.
type Tunnel struct {
	ID       string
	Nodes    []string        // ordered: primary first, then backups
	Ladder   []string        // resolved rungs (transport ids), rung 1 first
	Settings config.Failover // zero numbers take the section 9 defaults
	// FallbackTransport is the anti-flapping destination; "" = direct/native.
	FallbackTransport string
	// NeverQuarantine reports transports that are never quarantined (the
	// backend catalog's Transport.NeverQuarantine). direct/native and the
	// fallback transport are never quarantined either way. May be nil.
	NeverQuarantine func(transportID string) bool
	// ServiceTarget names the service behind the tunnel on the node (e.g.
	// "127.0.0.1:443") for service_down messages. Optional.
	ServiceTarget string
}

// settings are the effective failover settings with defaults applied.
type settings struct {
	policy           string
	probeInterval    time.Duration
	probeTimeout     time.Duration
	failThreshold    int
	recoverThreshold int
	failback         bool
	failbackAfter    time.Duration
	maxSwitches      int
	quarantine       time.Duration
}

func effective(f config.Failover) settings {
	s := settings{
		policy:           f.Policy,
		probeInterval:    seconds(f.ProbeIntervalS, config.DefaultProbeIntervalS),
		probeTimeout:     seconds(f.ProbeTimeoutS, config.DefaultProbeTimeoutS),
		failThreshold:    positive(f.FailThreshold, config.DefaultFailThreshold),
		recoverThreshold: positive(f.RecoverThreshold, config.DefaultRecoverThresh),
		failback:         f.Failback,
		failbackAfter:    seconds(f.FailbackAfterS, config.DefaultFailbackAfterS),
		maxSwitches:      positive(f.MaxSwitchesPerHour, config.DefaultMaxSwitchesHour),
		quarantine:       seconds(f.QuarantineS, config.DefaultQuarantineS),
	}
	switch s.policy {
	case config.PolicyTransportOnly, config.PolicyTransportThenNode, config.PolicyNodeOnly:
	default:
		s.policy = config.PolicyTransportThenNode
	}
	return s
}

func seconds(v, def int) time.Duration { return time.Duration(positive(v, def)) * time.Second }

func positive(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func normalizeTunnel(t Tunnel) Tunnel {
	t.Nodes = append([]string(nil), t.Nodes...)
	t.Ladder = append([]string(nil), t.Ladder...)
	if t.FallbackTransport == "" {
		t.FallbackTransport = FallbackTransport
	}
	return t
}

// Engine is the per-tunnel failover actor (section 9): one goroutine (Run)
// owns the state machine; owner commands reach it through a channel.
type Engine struct {
	a   Actions
	clk Clock
	id  string // tunnel id, immutable (read by caller goroutines)

	cmds    chan *command
	done    chan struct{}
	started atomic.Bool
	cmdSeq  atomic.Uint64 // commands received by the actor (test synchronisation)

	mu   sync.Mutex
	snap state.TunnelState

	// Owned by the Run goroutine.
	t         Tunnel
	cfg       settings
	st        state.TunnelState
	running   bool // st.Active has been started (or adopted) and not stopped
	hist      rttHistory
	nextProbe time.Time
	pending   []*command
	canary    bool // the last ProbeCanary reported a configured canary
	// interrupted describes the automatic sequence an owner command cut short
	// (state SWITCHING); resumeSwitching finishes it with the right semantics.
	interrupted interruption
	lastSaved   state.TunnelState
	lastSaveAt  time.Time
	dirty       bool // the last Persist failed; retry on the next commit
}

// NewEngine returns an engine for tunnel t starting from the persisted (and,
// after a hub restart, reconciled — see Reconcile) state st. clk nil means
// RealClock. Nothing runs until Run is called.
func NewEngine(t Tunnel, a Actions, clk Clock, st state.TunnelState) *Engine {
	if clk == nil {
		clk = RealClock{}
	}
	e := &Engine{
		a:    a,
		clk:  clk,
		id:   t.ID,
		cmds: make(chan *command),
		done: make(chan struct{}),
		t:    normalizeTunnel(t),
		cfg:  effective(t.Settings),
		st:   cloneState(st),
	}
	e.st.ID = t.ID
	if e.st.Quarantine == nil {
		e.st.Quarantine = map[string]state.Quarantine{}
	}
	if e.st.Skipped == nil {
		e.st.Skipped = map[string]state.Skip{}
	}
	if e.st.FailbackDelay <= 0 {
		e.st.FailbackDelay = e.cfg.failbackAfter
	}
	e.snap = cloneState(e.st)
	return e
}

// State returns a copy of the latest tunnel state. It never blocks on the
// actor, so it is safe to call while a switch is in progress.
func (e *Engine) State() state.TunnelState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneState(e.snap)
}

// Snapshot is State (the name used by the daemon's status code).
func (e *Engine) Snapshot() state.TunnelState { return e.State() }

// Done is closed when Run has returned.
func (e *Engine) Done() <-chan struct{} { return e.done }

// Run executes the state machine until ctx is cancelled and then returns
// nil. It never stops backend units on the way out: a hub restart must not
// take a working tunnel down (section 3); Reconcile picks the state up again.
func (e *Engine) Run(ctx context.Context) error {
	if !e.started.CompareAndSwap(false, true) {
		return deyerr.Wrap(deyerr.X000, deyerr.Plain("failover engine for tunnel "+e.id+" is already running"), nil)
	}
	defer close(e.done)
	e.boot(ctx)
	for ctx.Err() == nil {
		e.step(ctx)
	}
	return nil
}

// boot turns the persisted state into a runnable one.
func (e *Engine) boot(ctx context.Context) {
	now := e.clk.Now()
	st := &e.st
	if st.State == state.StatePaused {
		st.Paused = true
	}
	switch st.State {
	case "", state.StateDisabled:
		e.setState(state.StateInit, "tunnel starting")
	case state.StateUp, state.StateDegraded, state.StatePaused:
		e.running = true
		if !e.valid(st.Active) {
			// The unit in use is no longer part of the tunnel.
			e.stopActive(ctx)
			st.Active = state.Candidate{}
			e.setState(state.StateInit, "active candidate is no longer part of the tunnel")
		}
	case state.StateSwitching:
		// Not reconciled: whether the unit runs is unknown; start it again
		// (starting a running unit is harmless).
		e.setState(state.StateStarting, "resuming after restart")
	case state.StateDown:
		if st.DownBackoff <= 0 {
			st.DownBackoff = DownRetryInitial
		}
	}
	if st.Paused && (e.running || st.State == state.StateDown) {
		e.setState(state.StatePaused, "failover paused by owner")
	}
	if st.State == state.StateUp && st.StableSince.IsZero() {
		st.StableSince = now
	}
	if (st.State == state.StateUp || st.State == state.StateDegraded) && st.UpSince.IsZero() {
		st.UpSince = now
	}
	e.nextProbe = now
	e.commit(true)
}

// step runs one iteration of the actor loop.
func (e *Engine) step(ctx context.Context) {
	if len(e.pending) > 0 {
		c := e.pending[0]
		e.pending = e.pending[1:]
		e.handle(ctx, c)
		return
	}
	switch e.st.State {
	case state.StateInit, "":
		e.initialize(ctx)
		return
	case state.StateStarting:
		e.runStarting(ctx)
		return
	case state.StateSwitching:
		e.resumeSwitching(ctx)
		return
	}
	t := e.clk.NewTimer(e.nextWake().Sub(e.clk.Now()))
	select {
	case <-ctx.Done():
		t.Stop()
	case c := <-e.cmds:
		t.Stop()
		e.cmdSeq.Add(1)
		e.handle(ctx, c)
	case <-t.C():
		e.tick(ctx)
	}
}

// nextWake is when the idle loop must act next.
func (e *Engine) nextWake() time.Time {
	switch {
	case e.st.State == state.StateDown && !e.st.Paused:
		if e.st.DownRetryAt.IsZero() {
			return e.clk.Now()
		}
		return e.st.DownRetryAt
	case e.running:
		return e.nextProbe
	default:
		return e.clk.Now().Add(e.cfg.probeInterval)
	}
}

// tick is one timer expiry of the idle loop.
func (e *Engine) tick(ctx context.Context) {
	now := e.clk.Now()
	e.housekeeping(now)
	switch {
	case e.st.State == state.StateDown && !e.st.Paused:
		e.downRetry(ctx)
	case e.running:
		e.probeTick(ctx, now)
	default:
		e.commit(false)
	}
}

// housekeeping expires the anti-flapping window and old quarantine entries.
func (e *Engine) housekeeping(now time.Time) {
	e.pruneSwitchTimes(now)
	if e.st.Flapping && len(e.st.SwitchTimes) < e.cfg.maxSwitches {
		e.st.Flapping = false
	}
	PruneQuarantine(e.st.Quarantine, now)
}

func (e *Engine) pruneSwitchTimes(now time.Time) {
	kept := e.st.SwitchTimes[:0]
	for _, t := range e.st.SwitchTimes {
		if now.Sub(t) < SwitchWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		kept = nil
	}
	e.st.SwitchTimes = kept
}

// flapLimited reports whether one more automatic switch would exceed
// max_switches_per_hour.
func (e *Engine) flapLimited() bool {
	e.pruneSwitchTimes(e.clk.Now())
	return len(e.st.SwitchTimes) >= e.cfg.maxSwitches
}

// ---- small helpers over the tunnel and the actions

func (e *Engine) valid(c state.Candidate) bool { return validCandidate(e.t.Nodes, e.t.Ladder, c) }

func (e *Engine) skipped(c state.Candidate) bool {
	_, ok := e.st.Skipped[c.Key()]
	return ok
}

// excluder returns the exclusion used by NextCandidate: skipped rungs and,
// unless ignoreQuarantine, quarantined candidates.
func (e *Engine) excluder(ignoreQuarantine bool) func(state.Candidate) bool {
	now := e.clk.Now()
	return func(c state.Candidate) bool {
		return e.skipped(c) || (!ignoreQuarantine && Quarantined(e.st.Quarantine, c, now))
	}
}

// home is rung 1 of the primary node (the first rung that is not skipped).
func (e *Engine) home() (state.Candidate, bool) { return home(e.t.Nodes, e.t.Ladder, e.skipped) }

// neverQuarantine: direct/native is never quarantined (sections 8 and 9),
// whatever the configured fallback; neither are the configured fallback and
// the catalog's NeverQuarantine transports.
func (e *Engine) neverQuarantine(transport string) bool {
	if transport == FallbackTransport || transport == e.t.FallbackTransport {
		return true
	}
	return e.t.NeverQuarantine != nil && e.t.NeverQuarantine(transport)
}

// quarantine records a failure of c. A candidate whose node has lost its
// control connection is not quarantined: the failure cannot be blamed on the
// transport.
func (e *Engine) quarantine(c state.Candidate) {
	if c.IsZero() || e.neverQuarantine(c.Transport) {
		return
	}
	if online, _ := e.a.ControlOnline(c.Node); !online {
		return
	}
	ApplyQuarantine(e.st.Quarantine, c, e.clk.Now(), e.cfg.quarantine)
}

func (e *Engine) start(ctx context.Context, c state.Candidate) error {
	sctx, cancel := context.WithTimeout(ctx, StartTimeout)
	defer cancel()
	return e.a.Start(sctx, c)
}

func (e *Engine) stop(ctx context.Context, c state.Candidate) error {
	sctx, cancel := context.WithTimeout(ctx, StopTimeout)
	defer cancel()
	return e.a.Stop(sctx, c)
}

// stopActive stops the running candidate, if any.
func (e *Engine) stopActive(ctx context.Context) {
	if !e.running {
		return
	}
	// A failed stop (e.g. node offline) is not fatal: the hub side is what
	// binds the user ports and the next start reports a real conflict.
	_ = e.stop(ctx, e.st.Active)
	e.running = false
}

// probePath probes c with probe_timeout_s, shortened to limit when that is
// smaller.
func (e *Engine) probePath(ctx context.Context, c state.Candidate, limit time.Duration) Probe {
	timeout := e.cfg.probeTimeout
	if limit < timeout {
		timeout = limit
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p := e.a.ProbePath(pctx, c)
	if !p.OK && p.Err == "" {
		p.Err = "probe failed"
	}
	return p
}

func (e *Engine) nodeService(ctx context.Context, node string) (up, known bool) {
	sctx, cancel := context.WithTimeout(ctx, NodeServiceTimeout)
	defer cancel()
	return e.a.NodeService(sctx, node)
}

// nodeBroken: control offline for more than NodeBrokenAfter (the caller
// has seen the path fail).
func (e *Engine) nodeBroken(node string) bool {
	online, offlineFor := e.a.ControlOnline(node)
	return !online && offlineFor > NodeBrokenAfter
}

func ms(d time.Duration) int { return int(d / time.Millisecond) }

// ---- sleeping inside sequences

type sleepResult int

const (
	sleepDone    sleepResult = iota // the timer fired
	sleepStop                       // the engine context is done
	sleepPreempt                    // an owner command interrupts an automatic sequence
	sleepAbort                      // the owner's request context (test ladder) is done
)

// waitOpts tune a wait inside a sequence.
type waitOpts struct {
	// preemptible: an owner command (or pause) interrupts the wait. Automatic
	// sequences are preemptible; manual ones are not.
	preemptible bool
	// abort, when set, is the owner's request context (test ladder).
	abort context.Context
}

// sleepUntil waits on the engine clock until when, serving commands
// meanwhile: pause/resume and skip changes apply at once, everything else is
// queued for the idle loop.
func (e *Engine) sleepUntil(ctx context.Context, when time.Time, w waitOpts) sleepResult {
	if w.preemptible && (len(e.pending) > 0 || e.st.Paused) {
		return sleepPreempt
	}
	var abort <-chan struct{}
	if w.abort != nil {
		abort = w.abort.Done()
	}
	t := e.clk.NewTimer(when.Sub(e.clk.Now()))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return sleepStop
		case <-abort:
			return sleepAbort
		case <-t.C():
			return sleepDone
		case c := <-e.cmds:
			pre := e.busyCommand(c) && w.preemptible
			if pre {
				t.Stop()
			}
			e.cmdSeq.Add(1)
			if pre {
				return sleepPreempt
			}
		}
	}
}

// ---- owner commands

type cmdKind int

const (
	cmdPause cmdKind = iota
	cmdResume
	cmdReset
	cmdSwitchTransport
	cmdSwitchNode
	cmdTestLadder
	cmdUpdate
	cmdSetSkipped
	cmdClearSkipped
)

var cmdNames = map[cmdKind]string{
	cmdPause:           "pause",
	cmdResume:          "resume",
	cmdReset:           "reset",
	cmdSwitchTransport: "switch transport",
	cmdSwitchNode:      "switch node",
	cmdTestLadder:      "test ladder",
	cmdUpdate:          "configuration update",
	cmdSetSkipped:      "skip rung",
	cmdClearSkipped:    "restore rung",
}

type command struct {
	kind   cmdKind
	arg    string
	cand   state.Candidate
	skip   state.Skip
	tun    Tunnel
	ctx    context.Context
	onRung func(api.RungResult)
	reply  chan cmdResult
}

type cmdResult struct {
	err   error
	rungs []api.RungResult
}

func (c *command) respond(r cmdResult) { c.reply <- r } // buffered (1)

// send delivers a command to the actor and waits for its answer.
func (e *Engine) send(ctx context.Context, c *command) cmdResult {
	c.ctx = ctx
	c.reply = make(chan cmdResult, 1)
	select {
	case e.cmds <- c:
	case <-e.done:
		return cmdResult{err: e.notRunning()}
	case <-ctx.Done():
		return cmdResult{err: e.cancelled(c, ctx.Err())}
	}
	select {
	case r := <-c.reply:
		return r
	case <-e.done:
		select {
		case r := <-c.reply:
			return r
		default:
			return cmdResult{err: e.notRunning()}
		}
	case <-ctx.Done():
		return cmdResult{err: e.cancelled(c, ctx.Err())}
	}
}

func (e *Engine) notRunning() error {
	return deyerr.New(deyerr.F007, deyerr.Params{"tunnel": e.id})
}

func (e *Engine) cancelled(c *command, cause error) error {
	return deyerr.Wrap(deyerr.F008, cause, deyerr.Params{"tunnel": e.id, "command": cmdNames[c.kind]})
}

// Pause stops automatic switching (maintenance). Probing continues.
func (e *Engine) Pause(ctx context.Context) error {
	return e.send(ctx, &command{kind: cmdPause}).err
}

// Resume re-enables automatic switching; the tunnel moves to the state its
// probes imply (possibly switching at once).
func (e *Engine) Resume(ctx context.Context) error {
	return e.send(ctx, &command{kind: cmdResume}).err
}

// Reset moves the tunnel to rung 1 of the primary node now (manual: always
// allowed, not counted by anti-flapping).
func (e *Engine) Reset(ctx context.Context) error {
	return e.send(ctx, &command{kind: cmdReset}).err
}

// SwitchTransport moves the tunnel to transport id on the current node
// (manual). It returns after the new transport passed its probe or, on
// failure, after returning to the previous candidate.
func (e *Engine) SwitchTransport(ctx context.Context, id string) error {
	return e.send(ctx, &command{kind: cmdSwitchTransport, arg: id}).err
}

// SwitchNode moves the tunnel to node id with the current transport (manual).
func (e *Engine) SwitchNode(ctx context.Context, id string) error {
	return e.send(ctx, &command{kind: cmdSwitchNode, arg: id}).err
}

// TestLadder tries every rung on every node for up to TestLadderRungWait
// each (start, probe, record RTT, stop), reports each row through onRung
// (may be nil) and restores the original candidate. Automatic failover is
// suspended while it runs. Cancelling ctx aborts the test and restores.
func (e *Engine) TestLadder(ctx context.Context, onRung func(api.RungResult)) ([]api.RungResult, error) {
	r := e.send(ctx, &command{kind: cmdTestLadder, onRung: onRung})
	return r.rungs, r.err
}

// UpdateConfig applies an edited tunnel (nodes, ladder, settings). When the
// active candidate is no longer part of the tunnel it moves to rung 1.
func (e *Engine) UpdateConfig(ctx context.Context, t Tunnel) error {
	return e.send(ctx, &command{kind: cmdUpdate, tun: t}).err
}

// SetSkipped marks candidate c as skipped (validation failed / UDP closed,
// section 8). The daemon owns the recheck schedule (RecheckAt).
func (e *Engine) SetSkipped(ctx context.Context, c state.Candidate, s state.Skip) error {
	return e.send(ctx, &command{kind: cmdSetSkipped, cand: c, skip: s}).err
}

// ClearSkipped puts candidate c back into the ladder.
func (e *Engine) ClearSkipped(ctx context.Context, c state.Candidate) error {
	return e.send(ctx, &command{kind: cmdClearSkipped, cand: c}).err
}

// busyCommand serves a command received while a sequence runs. It reports
// whether the command asks automatic sequences to yield.
func (e *Engine) busyCommand(c *command) bool {
	switch c.kind {
	case cmdPause:
		e.applyPause()
		c.respond(cmdResult{})
		return true
	case cmdResume:
		e.applyResume()
		c.respond(cmdResult{})
		return false
	case cmdSetSkipped, cmdClearSkipped:
		e.applySkip(c)
		c.respond(cmdResult{})
		return false
	default:
		e.pending = append(e.pending, c)
		return true
	}
}

// handle serves a command in the idle loop.
func (e *Engine) handle(ctx context.Context, c *command) {
	switch c.kind {
	case cmdPause:
		e.applyPause()
		c.respond(cmdResult{})
		return
	case cmdResume:
		switchNow := e.applyResume() && e.resumeImplied()
		c.respond(cmdResult{})
		if switchNow {
			e.onThreshold(ctx, e.st.LastProbeErr)
		}
		return
	case cmdSetSkipped, cmdClearSkipped:
		e.applySkip(c)
		c.respond(cmdResult{})
		return
	}
	if c.ctx != nil && c.ctx.Err() != nil {
		// The owner gave up before the command started: do not apply it.
		c.respond(cmdResult{err: e.cancelled(c, c.ctx.Err())})
		return
	}
	switch c.kind {
	case cmdReset:
		c.respond(cmdResult{err: e.manualReset(ctx)})
	case cmdSwitchTransport:
		c.respond(cmdResult{err: e.manualSwitchTransport(ctx, c.arg)})
	case cmdSwitchNode:
		c.respond(cmdResult{err: e.manualSwitchNode(ctx, c.arg)})
	case cmdTestLadder:
		rungs, err := e.testLadder(ctx, c.ctx, c.onRung)
		c.respond(cmdResult{rungs: rungs, err: err})
	case cmdUpdate:
		c.respond(cmdResult{err: e.updateConfig(ctx, c.tun)})
	}
}

func (e *Engine) applyPause() {
	if e.st.Paused {
		return
	}
	e.st.Paused = true
	switch e.st.State {
	case state.StateUp, state.StateDegraded, state.StateDown:
		e.setState(state.StatePaused, "failover paused by owner")
	}
	e.commit(true)
}

// applyResume clears the pause flag; it reports whether the idle loop must
// compute the state the probes imply.
func (e *Engine) applyResume() bool {
	if !e.st.Paused {
		return false
	}
	e.st.Paused = false
	e.commit(true)
	return e.st.State == state.StatePaused
}

func (e *Engine) applySkip(c *command) {
	if c.kind == cmdSetSkipped {
		e.st.Skipped[c.cand.Key()] = c.skip
	} else {
		delete(e.st.Skipped, c.cand.Key())
	}
	e.commit(true)
}
