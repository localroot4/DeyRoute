package failover

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const idleWait = 5 * time.Second

func cand(node, transport string) state.Candidate {
	return state.Candidate{Node: node, Transport: transport}
}

// ---- scripted Actions

type call struct {
	op  string // start|stop|probe|canary|service
	key string
	at  time.Time
}

type fakeActions struct {
	mu  sync.Mutex
	clk *FakeClock

	blocked      map[string]bool          // candidate key → path probe fails
	blockedUntil map[string]time.Time     // candidate key → path probe fails until then
	rtt          map[string]time.Duration // candidate key → RTT (default 20ms)
	startErr     map[string]error
	svcDown      map[string]bool // node → service down (known)
	svcUnknown   map[string]bool // node → node_service unknown
	offlineSince map[string]time.Time
	canaryOn     bool
	canaryOK     bool
	persistErr   error

	running      map[string]bool
	violations   []string        // actions called without a deadline
	probeBudgets []time.Duration // real time left on each ProbePath ctx
	calls        []call
	events       []state.Event
	persisted    []state.TunnelState
}

func newFakeActions(clk *FakeClock) *fakeActions {
	return &fakeActions{
		clk:          clk,
		blocked:      map[string]bool{},
		blockedUntil: map[string]time.Time{},
		rtt:          map[string]time.Duration{},
		startErr:     map[string]error{},
		svcDown:      map[string]bool{},
		svcUnknown:   map[string]bool{},
		offlineSince: map[string]time.Time{},
		running:      map[string]bool{},
	}
}

func (f *fakeActions) checkDeadline(ctx context.Context, op string) {
	if _, ok := ctx.Deadline(); !ok {
		f.violations = append(f.violations, op+" without deadline")
	}
}

func (f *fakeActions) record(op, key string) {
	f.calls = append(f.calls, call{op: op, key: key, at: f.clk.Now()})
}

func (f *fakeActions) Start(ctx context.Context, c state.Candidate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkDeadline(ctx, "start")
	f.record("start", c.Key())
	f.running[c.Key()] = true
	return f.startErr[c.Key()]
}

func (f *fakeActions) Stop(ctx context.Context, c state.Candidate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkDeadline(ctx, "stop")
	f.record("stop", c.Key())
	delete(f.running, c.Key())
	return nil
}

func (f *fakeActions) ProbePath(ctx context.Context, c state.Candidate) Probe {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkDeadline(ctx, "probe")
	if dl, ok := ctx.Deadline(); ok {
		f.probeBudgets = append(f.probeBudgets, time.Until(dl))
	}
	f.record("probe", c.Key())
	switch {
	case !f.running[c.Key()]:
		return Probe{Err: "connection refused (unit not running)"}
	case f.blocked[c.Key()], f.clk.Now().Before(f.blockedUntil[c.Key()]):
		return Probe{Err: "i/o timeout"}
	case f.svcDown[c.Node]:
		return Probe{Err: "connection closed by node service"}
	}
	rtt := f.rtt[c.Key()]
	if rtt == 0 {
		rtt = 20 * time.Millisecond
	}
	return Probe{OK: true, RTT: rtt}
}

func (f *fakeActions) ProbeCanary(context.Context) (Probe, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("canary", "")
	if !f.canaryOn {
		return Probe{}, false
	}
	if f.canaryOK {
		return Probe{OK: true, RTT: 10 * time.Millisecond}, true
	}
	return Probe{Err: "canary timeout"}, true
}

func (f *fakeActions) NodeService(_ context.Context, node string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("service", node)
	if f.svcUnknown[node] {
		return false, false
	}
	return !f.svcDown[node], true
}

func (f *fakeActions) ControlOnline(node string) (bool, time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	since, off := f.offlineSince[node]
	if !off {
		return true, 0
	}
	return false, f.clk.Now().Sub(since)
}

func (f *fakeActions) Emit(e state.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeActions) Persist(ts state.TunnelState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persisted = append(f.persisted, ts)
	return f.persistErr
}

func (f *fakeActions) set(fn func(f *fakeActions)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeActions) block(key string, on bool) {
	f.set(func(f *fakeActions) { f.blocked[key] = on })
}

func (f *fakeActions) eventsOf(typ string) []state.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []state.Event
	for _, e := range f.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (f *fakeActions) eventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.events))
	for _, e := range f.events {
		out = append(out, e.Type)
	}
	return out
}

func (f *fakeActions) callsOf(op, key string) []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []time.Time
	for _, c := range f.calls {
		if c.op == op && (key == "" || c.key == key) {
			out = append(out, c.at)
		}
	}
	return out
}

func (f *fakeActions) opSequence(ops ...string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]bool{}
	for _, o := range ops {
		want[o] = true
	}
	var out []string
	for _, c := range f.calls {
		if want[c.op] {
			out = append(out, c.op+" "+c.key)
		}
	}
	return out
}

func (f *fakeActions) clearCalls() {
	f.set(func(f *fakeActions) { f.calls = nil })
}

func (f *fakeActions) runningKeys() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]bool{}
	for k, v := range f.running {
		out[k] = v
	}
	return out
}

func (f *fakeActions) persistedStates() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.persisted {
		if len(out) == 0 || out[len(out)-1] != p.State {
			out = append(out, p.State)
		}
	}
	return out
}

// ---- harness

type harness struct {
	t      *testing.T
	clk    *FakeClock
	act    *fakeActions
	eng    *Engine
	cancel context.CancelFunc
	errc   chan error
}

// twoNodes is the section 4 sample tunnel: primary de-1, backup nl-1 and the
// default ladder (8 rungs ending with direct/native).
func twoNodes() Tunnel {
	return Tunnel{
		ID:       "main",
		Nodes:    []string{"de-1", "nl-1"},
		Ladder:   append([]string(nil), config.DefaultLadder...),
		Settings: config.DefaultFailover(),
	}
}

func oneNode() Tunnel {
	t := twoNodes()
	t.Nodes = []string{"de-1"}
	return t
}

// newHarness builds an engine; setup runs before Run starts.
func newHarness(t *testing.T, tun Tunnel, st state.TunnelState, setup func(*fakeActions)) *harness {
	t.Helper()
	clk := NewFakeClock(t0)
	act := newFakeActions(clk)
	if setup != nil {
		setup(act)
	}
	h := &harness{t: t, clk: clk, act: act, eng: NewEngine(tun, act, clk, st), errc: make(chan error, 1)}
	return h
}

// run starts the engine and waits until it sleeps.
func (h *harness) run() *harness {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.errc <- h.eng.Run(ctx) }()
	h.t.Cleanup(h.stop)
	h.idle()
	return h
}

func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.cancel = nil
	select {
	case err := <-h.errc:
		require.NoError(h.t, err)
	case <-time.After(idleWait):
		h.t.Fatal("engine did not stop")
	}
	h.act.mu.Lock()
	defer h.act.mu.Unlock()
	require.Empty(h.t, h.act.violations)
}

func (h *harness) idle() {
	h.t.Helper()
	require.True(h.t, h.clk.BlockUntil(1, idleWait), "engine did not go back to sleep")
}

// advance moves the fake time by d, letting the engine act on every timer
// in order.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	target := h.clk.Now().Add(d)
	for {
		h.idle()
		next, ok := h.clk.NextDeadline()
		if !ok || next.After(target) {
			break
		}
		h.clk.Advance(next.Sub(h.clk.Now()))
	}
	h.clk.Advance(target.Sub(h.clk.Now()))
	h.idle()
}

// advanceUntil advances in steps of one second until cond holds (or max).
func (h *harness) advanceUntil(max time.Duration, cond func(state.TunnelState) bool) time.Duration {
	h.t.Helper()
	start := h.clk.Now()
	for h.clk.Now().Sub(start) <= max {
		if cond(h.eng.State()) {
			return h.clk.Now().Sub(start)
		}
		h.advance(time.Second)
	}
	h.t.Fatalf("condition not reached within %s; state %+v", max, h.eng.State())
	return 0
}

// async runs a blocking command in a goroutine and returns once the engine
// has received it.
func (h *harness) async(f func() error) <-chan error {
	h.t.Helper()
	n := h.eng.cmdSeq.Load()
	ch := make(chan error, 1)
	go func() { ch <- f() }()
	require.Eventually(h.t, func() bool { return h.eng.cmdSeq.Load() > n }, idleWait, time.Millisecond)
	return ch
}

func (h *harness) wait(ch <-chan error) error {
	h.t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(idleWait):
		h.t.Fatal("command did not return")
		return nil
	}
}

func (h *harness) st() state.TunnelState { return h.eng.State() }

func (h *harness) requireActive(c state.Candidate, st string) {
	h.t.Helper()
	s := h.st()
	require.Equal(h.t, c, s.Active, "active candidate (state %s, cause %q)", s.State, s.TransitionCause)
	require.Equal(h.t, st, s.State, "state (cause %q)", s.TransitionCause)
	require.True(h.t, h.act.runningKeys()[c.Key()] || st == state.StateDown, "active unit is running")
}

func codeOf(err error) deyerr.Code {
	if err == nil {
		return ""
	}
	return deyerr.As(err).Code
}

// upOn returns a tunnel state that is UP on c (as persisted by an earlier run).
func upOn(c state.Candidate, since time.Time) state.TunnelState {
	return state.TunnelState{ID: "main", State: state.StateUp, Active: c, UpSince: since, StableSince: since}
}
