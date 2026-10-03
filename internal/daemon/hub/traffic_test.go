package hub

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// ---------------------------------------------------------------- fakes

const (
	lineListCounters = "nft list counters table inet deyroute_stats"
	lineDeleteStats  = "nft delete table inet deyroute_stats"
	lineApply        = "nft -f -"
)

// fakeNFT plays the kernel side of table inet deyroute_stats: `nft -f -`
// builds it with the seeded counters, `nft list counters` lists them,
// `nft delete table` removes it; traffic is added by the test.
type fakeNFT struct {
	mu       sync.Mutex
	table    bool
	counters map[string]firewall.Counter
	missing  bool // nft is not installed
	scripts  []string
}

var seedRe = regexp.MustCompile(`counter (tun_[a-z0-9-]+_(?:in|out)) \{\s*packets (\d+) bytes (\d+)`)

func (f *fakeNFT) handle(c exec.Call) (exec.Response, bool) {
	if c.Name != "nft" {
		return exec.Response{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing {
		return exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})}, true
	}
	switch line := c.Line(); {
	case line == lineApply && strings.Contains(string(c.Stdin), firewall.StatsTable):
		f.scripts = append(f.scripts, string(c.Stdin))
		f.table = true
		f.counters = map[string]firewall.Counter{}
		for _, m := range seedRe.FindAllStringSubmatch(string(c.Stdin), -1) {
			p, _ := strconv.ParseUint(m[2], 10, 64)
			b, _ := strconv.ParseUint(m[3], 10, 64)
			f.counters[m[1]] = firewall.Counter{Packets: p, Bytes: b}
		}
		return exec.Response{}, true
	case line == lineListCounters:
		if !f.table {
			return exec.Fail(1, "Error: No such file or directory; did you mean table 'deyroute' in family inet?"), true
		}
		names := make([]string, 0, len(f.counters))
		for n := range f.counters {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString("table inet deyroute_stats {\n")
		for _, n := range names {
			c := f.counters[n]
			b.WriteString("\tcounter " + n + " {\n\t\tpackets " + strconv.FormatUint(c.Packets, 10) +
				" bytes " + strconv.FormatUint(c.Bytes, 10) + "\n\t}\n")
		}
		b.WriteString("}\n")
		return exec.OK(b.String()), true
	case line == lineDeleteStats:
		if !f.table {
			return exec.Fail(1, "Error: No such file or directory"), true
		}
		f.table, f.counters = false, nil
		return exec.Response{}, true
	case line == "nft list tables":
		return exec.OK(""), true
	}
	return exec.Response{}, false
}

// add counts bytes on a tunnel's counters (nothing without the table).
func (f *fakeNFT) add(id string, in, out uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.table {
		return
	}
	for name, n := range map[string]uint64{firewall.CounterIn(id): in, firewall.CounterOut(id): out} {
		if c, ok := f.counters[name]; ok {
			c.Bytes += n
			c.Packets++
			f.counters[name] = c
		}
	}
}

// set overwrites a tunnel's counters (a reset).
func (f *fakeNFT) set(id string, in, out uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[firewall.CounterIn(id)] = firewall.Counter{Bytes: in}
	f.counters[firewall.CounterOut(id)] = firewall.Counter{Bytes: out}
}

// drop removes the table (a reboot, or `flush ruleset`).
func (f *fakeNFT) drop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.table, f.counters = false, nil
}

func (f *fakeNFT) value(name string) (firewall.Counter, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.counters[name]
	return c, ok
}

func (f *fakeNFT) has(id string) bool {
	_, ok := f.value(firewall.CounterIn(id))
	return ok
}

func (f *fakeNFT) lastScript() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.scripts) == 0 {
		return ""
	}
	return f.scripts[len(f.scripts)-1]
}

func (f *fakeNFT) applies() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.scripts)
}

func (f *fakeNFT) setMissing(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing = v
}

// seedOf returns the seeded bytes of a counter in an applied script.
func seedOf(t *testing.T, script, name string) uint64 {
	t.Helper()
	for _, m := range seedRe.FindAllStringSubmatch(script, -1) {
		if m[1] == name {
			v, err := strconv.ParseUint(m[3], 10, 64)
			require.NoError(t, err)
			return v
		}
	}
	t.Fatalf("counter %s not in the script", name)
	return 0
}

// fakeClock has a wall clock and a monotonic clock that tests move
// together (step) or apart (jump: the wall clock is set).
type fakeClock struct {
	mu   sync.Mutex
	wall time.Time
	mono time.Duration
}

func newFakeClock(at time.Time) *fakeClock { return &fakeClock{wall: at} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wall
}

func (c *fakeClock) monoNow() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

func (c *fakeClock) step(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall, c.mono = c.wall.Add(d), c.mono+d
}

func (c *fakeClock) jump(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wall = c.wall.Add(d)
}

// countingStore counts the traffic transactions and reads of a real store
// and can replace its size guard or fail its flushes.
type countingStore struct {
	*state.Store
	mu         sync.Mutex
	appends    int
	reads      int
	retentions []state.Retention
	failAppend error
	size       func(budget int64, wasOver bool) (bool, int64, error)
}

func (c *countingStore) AppendTraffic(b state.TrafficBatch, r state.Retention) (state.TrafficResult, error) {
	c.mu.Lock()
	c.appends++
	c.retentions = append(c.retentions, r)
	fail := c.failAppend
	c.mu.Unlock()
	if fail != nil {
		return state.TrafficResult{}, fail
	}
	return c.Store.AppendTraffic(b, r)
}

func (c *countingStore) TunnelPoints(tunnel, tier string, from, to time.Time) ([]state.TunnelPoint, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Store.TunnelPoints(tunnel, tier, from, to)
}

func (c *countingStore) HostPoints(node, tier string, from, to time.Time) ([]state.HostPoint, error) {
	c.mu.Lock()
	c.reads++
	c.mu.Unlock()
	return c.Store.HostPoints(node, tier, from, to)
}

func (c *countingStore) SizeGuard(budget int64, wasOver bool) (bool, int64, error) {
	c.mu.Lock()
	f := c.size
	c.mu.Unlock()
	if f != nil {
		return f(budget, wasOver)
	}
	return c.Store.SizeGuard(budget, wasOver)
}

func (c *countingStore) counts() (appends, reads int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appends, c.reads
}

func (c *countingStore) lastRetention() state.Retention {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retentions[len(c.retentions)-1]
}

// ---------------------------------------------------------------- rig

// tehran is the hub-local zone of the tests (a fixed zone: no tzdata).
var tehran = time.FixedZone("IRST", 3*3600+1800)

// trafficT0 is 11:30 in Tehran.
var trafficT0 = time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

const testInterval = 10 * time.Second

// trafficRig is a sampler over a real state.db, a fake nft and a fake
// clock, driven tick by tick.
type trafficRig struct {
	t      *testing.T
	s      *trafficSampler
	nft    *fakeNFT
	runner *exec.Fake
	clk    *fakeClock
	st     *state.Store
	store  *countingStore

	mu     sync.Mutex
	cfg    *config.Config
	events []state.Event
	down   map[string]bool
}

func trafficConfig(edit func(c *config.Config)) *config.Config {
	cfg := config.NewHub("ir-1", "203.0.113.1", 44433)
	cfg.Nodes = []config.Node{{ID: "de-1", Name: "Frankfurt", PublicIP: "198.51.100.7"}}
	cfg.Tunnels = []config.Tunnel{
		config.NewTunnel("main", "Main", []string{"de-1"}, []config.PortMap{
			{Listen: 443, Proto: config.ProtoTCP}, {Listen: 443, Proto: config.ProtoUDP},
		}),
		config.NewTunnel("games", "Games", []string{"de-1"}, []config.PortMap{{Listen: 27015, Proto: config.ProtoUDP}}),
	}
	if edit != nil {
		edit(cfg)
	}
	return cfg
}

func openTrafficStore(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newRig(t *testing.T, cfg *config.Config) *trafficRig {
	return rigWith(t, cfg, openTrafficStore(t), &fakeNFT{}, newFakeClock(trafficT0))
}

func rigWith(t *testing.T, cfg *config.Config, st *state.Store, nft *fakeNFT, clk *fakeClock) *trafficRig {
	t.Helper()
	r := &trafficRig{t: t, nft: nft, clk: clk, st: st, store: &countingStore{Store: st}, cfg: cfg, down: map[string]bool{}}
	for i := range cfg.Tunnels {
		require.NoError(t, st.PutTunnel(state.TunnelState{ID: cfg.Tunnels[i].ID, State: state.StateUp}))
	}
	for _, n := range cfg.Nodes {
		require.NoError(t, st.PutNode(state.NodeState{ID: n.ID}))
	}
	r.runner = exec.NewFake()
	r.runner.Handler = nft.handle
	r.s = newTrafficSampler(trafficDeps{
		runner:      r.runner,
		store:       r.store,
		log:         dlog.Discard(),
		now:         clk.now,
		mono:        clk.monoNow,
		loc:         tehran,
		interval:    testInterval,
		connsMaxAge: 70 * time.Second,
		config:      r.config,
		running:     func(id string) bool { r.mu.Lock(); defer r.mu.Unlock(); return !r.down[id] },
		nat:         func(string) bool { return false },
		host:        func() (uint16, uint64) { return 250, 1 << 30 },
		emit:        r.emit,
	})
	return r
}

func (r *trafficRig) config() *config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg
}

func (r *trafficRig) edit(fn func(c *config.Config)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := config.Clone(r.cfg)
	fn(c)
	r.cfg = c
}

func (r *trafficRig) emit(e state.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *trafficRig) eventsOf(typ string) []state.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []state.Event
	for _, e := range r.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (r *trafficRig) start() { r.s.start(context.Background()) }

// tick moves both clocks one interval and samples.
func (r *trafficRig) tick() {
	r.clk.step(testInterval)
	r.s.sample(context.Background())
}

func (r *trafficRig) ring(series string) []livePoint {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return append([]livePoint(nil), r.s.rings[series]...)
}

func (r *trafficRig) lastPoint(series string) livePoint {
	r.t.Helper()
	ring := r.ring(series)
	require.NotEmpty(r.t, ring, series)
	return ring[len(ring)-1]
}

func (r *trafficRig) now(id string) *api.TrafficNow {
	return r.s.tunnelNow(id, r.clk.now().UTC())
}

// stored sums the 1-minute points of a tunnel in state.db.
func (r *trafficRig) stored(id string) (in, out uint64) {
	r.t.Helper()
	pts, err := r.st.TunnelPoints(id, state.TierM1, time.Time{}, time.Time{})
	require.NoError(r.t, err)
	for _, p := range pts {
		in += p.In
		out += p.Out
	}
	return in, out
}

// ---------------------------------------------------------------- sampler

// TestTrafficSampleDeltas: one nft call per tick for every tunnel; deltas,
// rates, today's totals and the connection counts of the metrics pass.
func TestTrafficSampleDeltas(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	require.Equal(t, 1, r.nft.applies(), "the table is built at start")
	require.True(t, r.nft.has("main"))
	require.True(t, r.nft.has("games"))
	r.runner.Reset()

	r.nft.add("main", 1000, 5000)
	r.nft.add("games", 10, 20)
	r.tick()
	require.Equal(t, []string{lineListCounters}, r.runner.Lines(), "one nft call per tick for every tunnel")
	p := r.lastPoint("t:main")
	require.Equal(t, uint64(1000), p.in)
	require.Equal(t, uint64(5000), p.out)
	require.NotZero(t, p.flags&state.FlagGap, "the first sample of a process has no previous one")
	require.NotZero(t, p.flags&state.FlagConnsUnknown)
	require.Zero(t, r.now("main").RateInBitS, "no rate for a gap")

	r.s.noteConns("main", 5, true)
	r.s.noteConns("games", 0, false)
	r.nft.add("main", 2000, 8000)
	r.tick()
	p = r.lastPoint("t:main")
	require.Equal(t, livePoint{start: r.clk.now().Add(-testInterval).UTC(), secs: 10, in: 2000, out: 8000, conns: 5}, p)
	g := r.lastPoint("t:games")
	require.Zero(t, g.in)
	require.NotZero(t, g.flags&state.FlagConnsUnknown, "UDP has no connection count")
	hub := r.lastPoint("hub")
	require.Equal(t, uint16(250), hub.cpu)
	require.Equal(t, uint64(1<<30), hub.ram)

	n := r.now("main")
	require.NotNil(t, n)
	require.True(t, n.Available)
	require.Equal(t, uint64(2000*8/10), n.RateInBitS)
	require.Equal(t, uint64(8000*8/10), n.RateOutBitS)
	require.Equal(t, uint64(3000), n.TodayIn)
	require.Equal(t, uint64(13000), n.TodayOut)
	require.Len(t, r.runner.Lines(), 2)

	// A tunnel whose engine does not run: its points are gaps (the bytes
	// still count).
	r.mu.Lock()
	r.down["main"] = true
	r.mu.Unlock()
	r.nft.add("main", 7, 7)
	r.tick()
	p = r.lastPoint("t:main")
	require.NotZero(t, p.flags&state.FlagGap)
	require.Equal(t, uint64(7), p.in)
	require.Zero(t, r.now("main").RateInBitS)
	require.Equal(t, uint64(3007), r.now("main").TodayIn)

	c, ok := r.s.counter("main")
	require.True(t, ok)
	require.Equal(t, uint64(3007), c.In)
	require.True(t, c.Since.Equal(trafficT0.Add(testInterval)), "counting since the first reading")
}

// TestTrafficCounterReset: a counter that went down is a reset: the delta
// is the new value, the point is flagged and has no rate.
func TestTrafficCounterReset(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	r.tick()
	r.nft.add("main", 5000, 5000)
	r.tick()
	r.nft.set("main", 300, 400) // e.g. `nft reset counters`
	r.tick()
	p := r.lastPoint("t:main")
	require.Equal(t, uint64(300), p.in)
	require.Equal(t, uint64(400), p.out)
	require.NotZero(t, p.flags&state.FlagReset)
	require.Zero(t, r.now("main").RateInBitS)
	require.Equal(t, uint64(5300), r.now("main").TodayIn)
	c, _ := r.s.counter("main")
	require.True(t, c.Since.Equal(r.clk.now()), "the counters restarted now")

	r.nft.add("main", 100, 100)
	r.tick()
	p = r.lastPoint("t:main")
	require.Equal(t, uint64(100), p.in)
	require.Zero(t, p.flags&(state.FlagReset|state.FlagGap))
}

// TestTrafficUnavailable: without nft the accounting is unavailable with
// DEY-X061, the warning is emitted once, the sampler does not run nft on
// every tick and retries every trafficRetry; never "0 B" as a rate.
func TestTrafficUnavailable(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.nft.setMissing(true)
	r.start()
	ok, why := r.s.availability(false)
	require.False(t, ok)
	require.Equal(t, deyerr.X061, codeOf(why))
	require.Contains(t, deyerr.As(why).Why(), "nft is not installed")
	evs := r.eventsOf(state.EvProbeError)
	require.Len(t, evs, 1)
	require.Equal(t, string(deyerr.X061), evs[0].Code)
	require.Equal(t, state.LevelWarn, evs[0].Level)

	r.runner.Reset()
	for range 3 {
		r.tick()
	}
	require.Empty(t, r.runner.Lines(), "no nft call while unavailable")
	p := r.lastPoint("t:main")
	require.NotZero(t, p.flags&state.FlagGap)
	n := r.now("main")
	require.NotNil(t, n)
	require.False(t, n.Available)
	require.Zero(t, n.TodayIn)

	r.clk.step(trafficRetry)
	r.tick()
	require.NotEmpty(t, r.runner.Lines(), "retried")
	require.Len(t, r.eventsOf(state.EvProbeError), 1, "warned once")

	r.nft.setMissing(false)
	r.clk.step(trafficRetry)
	r.tick()
	ok, _ = r.s.availability(false)
	require.True(t, ok)
	require.True(t, r.nft.has("main"))
	require.Len(t, r.eventsOf(state.EvProbeError), 1)
	_, err := json.Marshal(r.now("main"))
	require.NoError(t, err)
}

// TestTrafficTableRebuiltAfterFlush: a table that disappears (flush
// ruleset, another firewall manager) is rebuilt at once, seeded with the
// last reading, at most every trafficRebuildGap; that interval is a gap and
// nothing is counted twice.
func TestTrafficTableRebuiltAfterFlush(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	r.tick()
	r.nft.add("main", 1000, 2000)
	r.tick()
	r.tick()
	r.nft.drop()
	r.tick() // 40 s after the start: the rebuild is due
	require.Equal(t, 2, r.nft.applies())
	require.Equal(t, uint64(1000), seedOf(t, r.nft.lastScript(), "tun_main_in"), "seeded with the last reading")
	require.Equal(t, uint64(2000), seedOf(t, r.nft.lastScript(), "tun_main_out"))
	require.NotZero(t, r.lastPoint("t:main").flags&state.FlagGap)

	r.nft.add("main", 700, 900)
	r.tick()
	p := r.lastPoint("t:main")
	require.Equal(t, uint64(700), p.in)
	require.Equal(t, uint64(900), p.out)
	require.Zero(t, p.flags&state.FlagReset, "no reset: the totals continue")
	require.Equal(t, uint64(1700), r.now("main").TodayIn)

	// Gone again right away: the rebuild waits for trafficRebuildGap
	// after the last one (40 s): the sample at 60 s skips it, the one at
	// 70 s rebuilds.
	r.nft.drop()
	r.tick()
	require.Equal(t, 2, r.nft.applies())
	require.NotZero(t, r.lastPoint("t:main").flags&state.FlagGap)
	r.tick()
	require.Equal(t, 3, r.nft.applies())
	require.Equal(t, uint64(1700), seedOf(t, r.nft.lastScript(), "tun_main_in"))

	// A counter missing from the table (replaced by something else) forces
	// a rebuild too.
	r.clk.step(trafficRebuildGap)
	r.nft.mu.Lock()
	delete(r.nft.counters, "tun_games_out")
	r.nft.mu.Unlock()
	r.tick()
	r.tick()
	require.Equal(t, 4, r.nft.applies())
	require.True(t, r.nft.has("games"))
}

// TestApplyStatsOnPortChange: the table is rebuilt only when the listen
// ports change, seeded with the live reading; firewall_managed=false does
// not matter; monitoring.enabled false removes the table.
func TestApplyStatsOnPortChange(t *testing.T) {
	r := newRig(t, trafficConfig(func(c *config.Config) { c.Security.FirewallManaged = false }))
	r.start()
	require.Equal(t, 1, r.nft.applies(), "built with firewall_managed false: it has no verdict")
	for range 3 {
		r.nft.add("main", 100, 100)
		r.tick()
		r.s.requestCheck()
		r.s.ensureStats(context.Background(), r.config(), r.clk.monoNow(), false)
	}
	require.Equal(t, 1, r.nft.applies(), "same ports: no rebuild")

	r.nft.add("main", 50, 0) // counted by the kernel after the last sample
	r.edit(func(c *config.Config) {
		c.Tunnels[0].Ports = append(c.Tunnels[0].Ports, config.PortMap{Listen: 8443, Proto: config.ProtoTCP, Target: "127.0.0.1:8443"})
	})
	r.tick()
	require.Equal(t, 2, r.nft.applies())
	require.Contains(t, r.nft.lastScript(), "tcp . 8443")
	require.Equal(t, uint64(350), seedOf(t, r.nft.lastScript(), "tun_main_in"), "seeded with the live counters")
	r.tick()
	require.Equal(t, uint64(350), r.now("main").TodayIn, "nothing lost, nothing twice")

	// A disabled tunnel leaves the table.
	r.edit(func(c *config.Config) { c.Tunnels[1].Enabled = false })
	r.tick()
	require.Equal(t, 3, r.nft.applies())
	require.False(t, r.nft.has("games"))

	// Monitoring off: the table goes (once) and nothing is sampled.
	off := false
	r.edit(func(c *config.Config) { c.Monitoring = &config.Monitoring{Enabled: &off} })
	r.runner.Reset()
	r.tick()
	r.tick()
	require.Equal(t, []string{lineDeleteStats}, r.runner.Lines())
	ok, why := r.s.availability(false)
	require.False(t, ok)
	require.Equal(t, deyerr.X061, codeOf(why))
	require.Nil(t, r.now("main"), "no traffic block while monitoring is off")

	// On again: rebuilt from the last reading.
	r.edit(func(c *config.Config) { c.Monitoring = nil })
	r.tick()
	require.Equal(t, 4, r.nft.applies())
	require.Equal(t, uint64(350), seedOf(t, r.nft.lastScript(), "tun_main_in"))

	// No tunnels left: the table is removed.
	r.edit(func(c *config.Config) { c.Tunnels = nil })
	r.runner.Reset()
	r.tick()
	require.Equal(t, []string{lineDeleteStats}, r.runner.Lines())
	r.tick()
	require.Equal(t, []string{lineDeleteStats}, r.runner.Lines(), "nothing to read without tunnels")
}

// TestTrafficFlushOneTransaction: a flush writes every series, the
// baseline and the cursor in one transaction; a flush without new samples
// writes nothing; a failed flush keeps the samples for the next one.
func TestTrafficFlushOneTransaction(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	var in, out uint64
	for i := range 6 {
		r.s.noteNode("de-1", float64(10*i), 512<<20)
		r.s.noteNode("de-1", float64(10*i+5), 600<<20)
		r.nft.add("main", 1000, 3000)
		r.nft.add("games", 1, 2)
		in, out = in+1000, out+3000
		r.tick()
	}
	r.s.flush()
	appends, _ := r.store.counts()
	require.Equal(t, 1, appends, "one transaction for every series")
	gotIn, gotOut := r.stored("main")
	require.Equal(t, in, gotIn)
	require.Equal(t, out, gotOut)
	gIn, _ := r.stored("games")
	require.Equal(t, uint64(6), gIn)
	nodePts, err := r.st.HostPoints("de-1", state.TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.NotEmpty(t, nodePts)
	require.Equal(t, uint64(600<<20), nodePts[len(nodePts)-1].RAM)
	hubPts, err := r.st.HostPoints("", state.TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.NotEmpty(t, hubPts)
	cp, ok, err := r.st.TrafficCheckpoint()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(6), cp.Cursor.Seq)
	require.Equal(t, in, cp.Baseline["main"].In)
	require.Equal(t, out, cp.Baseline["main"].Out)

	r.s.flush()
	appends, _ = r.store.counts()
	require.Equal(t, 1, appends, "nothing new: no transaction")

	// A failing flush keeps the samples; the next one stores them once.
	r.store.mu.Lock()
	r.store.failAppend = stderrors.New("disk full")
	r.store.mu.Unlock()
	r.nft.add("main", 77, 0)
	r.tick()
	r.s.flush()
	r.nft.add("main", 23, 0)
	r.tick()
	r.store.mu.Lock()
	r.store.failAppend = nil
	r.store.mu.Unlock()
	r.s.flush()
	r.s.flush()
	appends, _ = r.store.counts()
	require.Equal(t, 3, appends)
	gotIn, _ = r.stored("main")
	require.Equal(t, in+100, gotIn)
}

// TestTrafficRestartContinuity: a restarted hub continues from the stored
// baseline (bytes counted while it was away are counted once), and after
// a reboot (table gone) the table is rebuilt from the baseline; a lower
// counter is a reset, never a negative or doubled delta.
func TestTrafficRestartContinuity(t *testing.T) {
	st := openTrafficStore(t)
	nft := &fakeNFT{}
	clk := newFakeClock(trafficT0)
	cfg := trafficConfig(nil)

	a := rigWith(t, cfg, st, nft, clk)
	a.start()
	var total uint64
	for range 3 {
		nft.add("main", 1000, 0)
		total += 1000
		a.tick()
	}
	a.s.flush()
	for range 2 { // sampled but never flushed: the process dies
		nft.add("main", 500, 0)
		total += 500
		a.tick()
	}
	nft.add("main", 250, 0) // while no hub runs
	total += 250
	clk.step(time.Minute)

	b := rigWith(t, cfg, st, nft, clk)
	b.start()
	require.Equal(t, uint64(4250), seedOf(t, nft.lastScript(), "tun_main_in"), "the live table seeds the rebuild")
	nft.add("main", 100, 0)
	total += 100
	b.tick()
	p := b.lastPoint("t:main")
	require.Equal(t, uint64(1350), p.in, "everything since the stored baseline, once")
	require.NotZero(t, p.flags&state.FlagGap, "the downtime is a gap")
	require.Zero(t, p.flags&state.FlagReset)
	b.tick()
	b.s.flush()
	gotIn, _ := b.stored("main")
	require.Equal(t, total, gotIn, "no double counting across the restart")
	require.Equal(t, total, b.now("main").TodayIn)

	// Reboot: the kernel table is gone; the new hub seeds it from the
	// baseline and counts only what comes after.
	nft.drop()
	clk.step(2 * time.Minute)
	c := rigWith(t, cfg, st, nft, clk)
	c.start()
	require.Equal(t, total, seedOf(t, nft.lastScript(), "tun_main_in"))
	nft.add("main", 40, 0)
	total += 40
	c.tick()
	require.Equal(t, uint64(40), c.lastPoint("t:main").in)
	c.s.flush()
	gotIn, _ = c.stored("main")
	require.Equal(t, total, gotIn)

	// Counters recreated at zero by something else: a reset.
	nft.set("main", 5, 0)
	total += 5
	c.tick()
	require.Equal(t, uint64(5), c.lastPoint("t:main").in)
	require.NotZero(t, c.lastPoint("t:main").flags&state.FlagReset)
	c.s.flush()
	gotIn, _ = c.stored("main")
	require.Equal(t, total, gotIn)
}

// TestTrafficQuota: the quota events fire once per threshold and period;
// the period starts on quota_reset_day at 00:00 hub-local (Tehran, +03:30)
// and the sent events survive a restart.
func TestTrafficQuota(t *testing.T) {
	const gib = 1 << 30
	cfg := trafficConfig(func(c *config.Config) {
		c.Monitoring = &config.Monitoring{QuotaResetDay: 5}
		c.Tunnels[0].Advanced = &config.Advanced{MonthlyQuotaGiB: 1}
	})
	st := openTrafficStore(t)
	nft := &fakeNFT{}
	// 2026-03-04 23:00 in Tehran: the period started on 5 February.
	clk := newFakeClock(time.Date(2026, 3, 4, 19, 30, 0, 0, time.UTC))
	r := rigWith(t, cfg, st, nft, clk)
	r.start()
	r.tick()
	nft.add("main", gib/4, gib/4)
	r.tick()
	r.s.flush()
	require.Empty(t, r.eventsOf(state.EvTrafficQuota), "50 %")

	nft.add("main", gib/5, gib/7)
	r.tick()
	r.s.flush()
	r.s.flush()
	evs := r.eventsOf(state.EvTrafficQuota)
	require.Len(t, evs, 1, "80 % once")
	require.Equal(t, state.LevelWarn, evs[0].Level)
	require.Equal(t, "main", evs[0].Tunnel)
	require.Contains(t, evs[0].Message, "Tunnel main has used 84% of its traffic quota")

	// A restarted hub remembers the warning.
	r2 := rigWith(t, cfg, st, nft, clk)
	r2.start()
	r2.tick()
	r2.s.flush()
	require.Empty(t, r2.eventsOf(state.EvTrafficQuota))

	nft.add("main", gib/5, 0)
	r.tick()
	r.s.flush()
	evs = r.eventsOf(state.EvTrafficQuota)
	require.Len(t, evs, 2, "100 % once")
	require.Equal(t, state.LevelError, evs[1].Level)
	require.Contains(t, evs[1].Message, "since 2026-02-05")
	r.s.flush()
	require.Len(t, r.eventsOf(state.EvTrafficQuota), 2)

	// 5 March 00:00 in Tehran (4 March 20:30 UTC): a new period.
	clk.step(time.Hour)
	r.tick()
	nft.add("main", gib/10, 0)
	r.tick()
	r.s.flush()
	require.Len(t, r.eventsOf(state.EvTrafficQuota), 2, "10 % of the new period")
	tot := r.s.totals(r.config(), &r.config().Tunnels[0], clk.now().UTC())
	require.Equal(t, time.Date(2026, 3, 4, 20, 30, 0, 0, time.UTC), tot.PeriodStart)
	require.Equal(t, uint64(gib/10), tot.PeriodIn)
	require.Equal(t, uint64(gib), tot.QuotaBytes)
	nft.add("main", gib*3/4, 0)
	r.tick()
	r.s.flush()
	evs = r.eventsOf(state.EvTrafficQuota)
	require.Len(t, evs, 3, "the warning fires again in the new period")
	require.Equal(t, state.LevelWarn, evs[2].Level)
}

// TestTrafficClockJumps: a wall clock stepped by a year or back by two
// hours is a gap; the points in memory follow the new clock, nothing is
// wiped from state.db and every number still marshals.
func TestTrafficClockJumps(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	for range 3 {
		r.nft.add("main", 100, 100)
		r.tick()
	}
	r.s.flush()
	before := r.ring("t:main")

	year := 365 * 24 * time.Hour
	r.clk.jump(year)
	r.nft.add("main", 100, 100)
	r.tick()
	p := r.lastPoint("t:main")
	require.NotZero(t, p.flags&state.FlagGap, "a clock jump is a gap")
	require.Equal(t, uint64(100), p.in, "its bytes still count")
	after := r.ring("t:main")
	require.True(t, after[0].start.Equal(before[0].start.Add(year)), "moved onto the new clock")
	require.Equal(t, uint64(400), r.now("main").TodayIn, "today follows the new clock")
	r.s.flush()
	gotIn, _ := r.stored("main")
	require.Equal(t, uint64(400), gotIn, "history not wiped by the forward jump")

	r.clk.jump(-2 * time.Hour)
	r.nft.add("main", 10, 10)
	r.tick()
	require.NotZero(t, r.lastPoint("t:main").flags&state.FlagGap)
	r.nft.add("main", 10, 10)
	r.tick()
	require.Zero(t, r.lastPoint("t:main").flags&state.FlagGap, "steady again")
	require.Equal(t, uint64(10*8/10), r.now("main").RateInBitS)

	// Two samples at the same instant: no rate, no NaN.
	r.nft.add("main", 10, 10)
	r.s.sample(context.Background())
	require.NotZero(t, r.lastPoint("t:main").flags&state.FlagGap)
	n := r.now("main")
	require.Zero(t, n.RateInBitS)
	_, err := json.Marshal(n)
	require.NoError(t, err)
	r.s.flush()
}

// TestTrafficSizeGuard: above 80 % of the budget the 1-minute retention
// drops to 6 h and DEY-X063 is emitted once; below 60 % it is back.
func TestTrafficSizeGuard(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	var over bool
	r.store.size = func(budget int64, was bool) (bool, int64, error) {
		require.Equal(t, state.SizeBudget, budget)
		return over, 45 << 20, nil
	}
	r.start()
	r.tick()
	r.s.flush()
	require.Equal(t, state.DefaultM1Keep, r.store.lastRetention().M1)

	over = true
	r.tick()
	r.s.flush()
	r.tick()
	r.s.flush()
	require.Equal(t, state.ReducedM1Keep, r.store.lastRetention().M1)
	evs := r.eventsOf(state.EvProbeError)
	require.Len(t, evs, 1)
	require.Equal(t, string(deyerr.X063), evs[0].Code)
	var g guardRecord
	ok, err := r.st.GetMeta(metaTrafficGuard, &g)
	require.NoError(t, err)
	require.True(t, ok && g.Over)

	// A restarted hub keeps the reduced retention.
	r2 := rigWith(t, r.config(), r.st, r.nft, r.clk)
	r2.store.size = r.store.size
	r2.start()
	r2.tick()
	r2.s.flush()
	require.Equal(t, state.ReducedM1Keep, r2.store.lastRetention().M1)

	over = false
	r.tick()
	r.s.flush()
	r.tick()
	r.s.flush()
	require.Equal(t, state.DefaultM1Keep, r.store.lastRetention().M1)
	require.Len(t, r.eventsOf(state.EvProbeError), 1)
}

// TestTrafficForget: a deleted tunnel or node leaves memory.
func TestTrafficForget(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	r.s.noteNode("de-1", 5, 1)
	r.tick()
	r.s.forget("main")
	r.s.forgetNode("de-1")
	require.Empty(t, r.ring("t:main"))
	require.Empty(t, r.ring("n:de-1"))
	require.Nil(t, r.now("main"))
	_, ok := r.s.counter("main")
	require.False(t, ok)
}

// ---------------------------------------------------------------- query

func TestDownsample(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC() // a multiple of 30 s and 10 s
	var pts []livePoint
	for i := 1; i <= 360; i++ {
		pts = append(pts, livePoint{start: now.Add(-time.Duration(i) * 10 * time.Second), secs: 10, in: 10, out: 20, conns: uint32(i % 7)})
	}
	slices.Reverse(pts)
	pts[0].flags |= state.FlagGap
	pts[5].flags |= state.FlagConnsUnknown
	step, out := downsample(pts, false, 10*time.Second, time.Hour, 120, now)
	require.Equal(t, 30, step)
	require.Len(t, out, 120)
	require.True(t, out[0].Gap, "a bucket with a gap point")
	require.False(t, out[1].Gap)
	require.Equal(t, uint64(30), out[1].BytesIn, "bytes are summed")
	require.Equal(t, uint64(60), out[1].BytesOut)
	require.NotNil(t, out[1].Conns)
	require.True(t, out[len(out)-1].At.Before(now), "the open bucket is left out")
	require.Equal(t, now.Add(-30*time.Second), out[len(out)-1].At)

	// Missing points are gaps, not zeros.
	step, out = downsample(nil, false, time.Minute, 24*time.Hour, 1440, now)
	require.Equal(t, 60, step)
	require.Len(t, out, 1440)
	require.True(t, out[0].Gap && out[1439].Gap)
	require.Nil(t, out[0].Conns)

	// Hosts take the highest CPU and RAM.
	host := []livePoint{
		{start: now.Add(-50 * time.Second), cpu: 120, ram: 5},
		{start: now.Add(-40 * time.Second), cpu: 300, ram: 3},
	}
	_, out = downsample(host, true, 10*time.Second, time.Hour, 60, now)
	last := out[len(out)-1]
	require.InDelta(t, 30.0, last.CPUPercent, 0.001)
	require.Equal(t, uint64(5), last.RAMBytes)
}

// trafficHub is a hub without listeners around a test sampler, enough for
// the Traffic method.
func trafficHub(r *trafficRig) *local {
	o := Options{Location: tehran, TrafficInterval: testInterval, Now: r.clk.now}.withDefaults()
	h := &Hub{o: o, cfg: r.config(), traffic: r.s}
	return &local{h: h}
}

func TestTrafficQuery(t *testing.T) {
	r := newRig(t, trafficConfig(nil))
	r.start()
	for i := range 20 {
		r.s.noteNode("de-1", float64(i), 1000)
		r.s.noteConns("main", i, true)
		r.nft.add("main", 1000, 2000)
		r.tick()
	}
	r.s.flush()
	l := trafficHub(r)
	ctx := context.Background()

	_, err := l.Traffic(ctx, api.TrafficQuery{Period: "2h"})
	require.Equal(t, deyerr.C027, codeOf(err))
	_, err = l.Traffic(ctx, api.TrafficQuery{Targets: []string{"nope"}})
	require.Equal(t, deyerr.C027, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), "main, games, node:de-1, hub")
	_, err = l.Traffic(ctx, api.TrafficQuery{Targets: []string{"node:zz-9"}})
	require.Equal(t, deyerr.C027, codeOf(err))

	_, reads := r.store.counts()
	rep, err := l.Traffic(ctx, api.TrafficQuery{})
	require.NoError(t, err)
	_, reads2 := r.store.counts()
	require.Equal(t, reads, reads2, "the 1 h period never reads state.db")
	require.True(t, rep.Available)
	require.Equal(t, "IRST", rep.Timezone)
	require.Equal(t, api.TrafficPeriod1h, rep.Period)
	require.Len(t, rep.Series, 2, "every tunnel by default")
	main := rep.Series[0]
	require.Equal(t, "main", main.ID)
	require.Equal(t, "Main", main.Name)
	require.Equal(t, api.TrafficKindTunnel, main.Kind)
	require.Equal(t, 30, main.StepS)
	require.Len(t, main.Points, 120)
	var sum uint64
	maxConns := 0
	for _, p := range main.Points {
		sum += p.BytesIn
		if p.Conns != nil {
			maxConns = max(maxConns, *p.Conns)
		}
	}
	require.Greater(t, sum, uint64(15000))
	require.LessOrEqual(t, sum, uint64(20000))
	require.Equal(t, 17, maxConns, "the open bucket (samples 18 and 19) is left out")
	require.NotNil(t, main.Totals)
	require.Equal(t, uint64(20000), main.Totals.TodayIn)
	require.Equal(t, uint64(40000), main.Totals.Days30Out)
	require.Equal(t, time.Date(2026, 3, 9, 20, 30, 0, 0, time.UTC), main.Totals.TodayStart, "00:00 in Tehran")
	_, err = json.Marshal(rep)
	require.NoError(t, err)

	rep, err = l.Traffic(ctx, api.TrafficQuery{Targets: []string{"node:de-1", "hub"}, Period: "1h", MaxPoints: 360})
	require.NoError(t, err)
	require.Equal(t, []string{api.TrafficKindNode, api.TrafficKindHub}, []string{rep.Series[0].Kind, rep.Series[1].Kind})
	require.Equal(t, "Frankfurt", rep.Series[0].Name)
	require.Equal(t, 10, rep.Series[0].StepS)
	require.Nil(t, rep.Series[0].Totals)
	var cpu float64
	for _, p := range rep.Series[0].Points {
		cpu = max(cpu, p.CPUPercent)
	}
	require.InDelta(t, 19.0, cpu, 0.001, "10 s buckets: the newest sample's bucket is closed")
	var hubCPU float64
	for _, p := range rep.Series[1].Points {
		hubCPU = max(hubCPU, p.CPUPercent)
	}
	require.InDelta(t, 25.0, hubCPU, 0.001)

	for _, c := range []struct {
		period string
		max    int
		step   int
	}{
		{api.TrafficPeriod24h, 0, 720},
		{api.TrafficPeriod24h, 1440, 60},
		{api.TrafficPeriod7d, 0, 5400},
		{api.TrafficPeriod30d, 0, 21600},
	} {
		rep, err := l.Traffic(ctx, api.TrafficQuery{Targets: []string{"main"}, Period: c.period, MaxPoints: c.max})
		require.NoError(t, err, c.period)
		require.Equal(t, c.step, rep.Series[0].StepS, c.period)
	}
	_, reads3 := r.store.counts()
	require.Greater(t, reads3, reads2, "24 h, 7 d and 30 d read state.db")
	rep, err = l.Traffic(ctx, api.TrafficQuery{Targets: []string{"tunnel:main"}, Period: "24h", MaxPoints: 1440})
	require.NoError(t, err)
	var stored uint64
	for _, p := range rep.Series[0].Points {
		stored += p.BytesIn
	}
	require.Positive(t, stored)

	// While the size guard is on, 24 h uses the half-hour points.
	r.s.mu.Lock()
	r.s.guardOver = true
	r.s.mu.Unlock()
	rep, err = l.Traffic(ctx, api.TrafficQuery{Targets: []string{"main"}, Period: "24h"})
	require.NoError(t, err)
	require.Equal(t, 1800, rep.Series[0].StepS)

	// Unavailable accounting: the tunnel series say why, hosts keep working.
	r.s.setUnavailable(deyerr.New(deyerr.X061, deyerr.Params{"reason": "test"}))
	rep, err = l.Traffic(ctx, api.TrafficQuery{Targets: []string{"main", "hub"}})
	require.NoError(t, err)
	require.False(t, rep.Available)
	require.Equal(t, string(deyerr.X061), rep.Reason.Code)
	require.False(t, rep.Series[0].Available)
	require.True(t, rep.Series[1].Available)
}

func TestZoneLabel(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.Equal(t, "IRST", zoneLabel(tehran, now))
	require.Equal(t, "UTC", zoneLabel(time.UTC, now))
	loc := time.FixedZone("", -(4*3600 + 30*60))
	require.Equal(t, "UTC-04:30", zoneLabel(loc, now))
}

// TestStatusTrafficFromMemory: Status carries the traffic of a running
// tunnel and never reads the traffic history of state.db.
func TestStatusTrafficFromMemory(t *testing.T) {
	env, o := prepareEnv(t, func(c *config.Config) {
		c.Nodes = []config.Node{{ID: "de-1", Name: "Frankfurt", PublicIP: "198.51.100.7"}}
		c.Tunnels = []config.Tunnel{config.NewTunnel("main", "Main", []string{"de-1"}, []config.PortMap{{Listen: 443, Proto: config.ProtoTCP}})}
	})
	_ = env
	clk := newFakeClock(trafficT0)
	o.Now = clk.now
	h, err := New(o)
	require.NoError(t, err)
	defer func() { _ = h.Close() }()
	r := rigWith(t, h.Config(), h.st, &fakeNFT{}, clk)
	h.traffic = r.s
	r.start()
	r.tick()
	r.nft.add("main", 1250, 2500)
	r.tick()
	_, reads := r.store.counts()
	st, err := h.Local().Status(context.Background())
	require.NoError(t, err)
	_, after := r.store.counts()
	require.Equal(t, reads, after, "Status reads no traffic history")
	require.Len(t, st.Tunnels, 1)
	tr := st.Tunnels[0].Traffic
	require.NotNil(t, tr)
	require.True(t, tr.Available)
	require.Equal(t, uint64(1000), tr.RateInBitS)
	require.Equal(t, uint64(1250), tr.TodayIn)
	_, err = json.Marshal(st)
	require.NoError(t, err)
}

// TestTrafficHub runs the sampler inside a hub: the table follows the
// tunnels, Status, tunnel show, the metrics pass and Traffic see the
// bytes, the flush stores them, and a deleted tunnel leaves the table.
func TestTrafficHub(t *testing.T) {
	nft := &fakeNFT{}
	env, o := prepareEnv(t, nil, func(o *Options, _ string) {
		o.DisableStats = false
		o.TrafficInterval = 25 * time.Millisecond
		o.TrafficFlush = 50 * time.Millisecond
	})
	te := &tunnelEnv{testEnv: env, o: o}
	te.sd = newFakeSystemd(t, env)
	sd := env.runner.Handler
	env.runner.Handler = func(c exec.Call) (exec.Response, bool) {
		if r, ok := nft.handle(c); ok {
			return r, true
		}
		return sd(c)
	}
	env.startEnv(o)
	te.tunnelNode("de-1")
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	require.Eventually(t, func() bool { return nft.has("main") }, testWait, 10*time.Millisecond, "the table follows the new tunnel")
	require.Empty(t, te.nftScripts(), "the accounting scripts are not firewall scripts")

	require.Eventually(t, func() bool {
		nft.add("main", 1000, 1000)
		st, err := te.client.Status(ctxT(t))
		require.NoError(t, err)
		tr := st.Tunnels[0].Traffic
		return tr != nil && tr.Available && tr.TodayIn >= 2000
	}, testWait, 20*time.Millisecond)

	te.h.collectMetrics(ctxT(t))
	m, ok, err := te.h.st.GetMetrics("main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, state.MetricsSourceNFT, m.Source)
	require.GreaterOrEqual(t, m.BytesIn, uint64(2000))
	d, err := te.client.TunnelShow(ctxT(t), "main")
	require.NoError(t, err)
	require.NotNil(t, d.Metrics)
	require.Equal(t, state.MetricsSourceNFT, d.Metrics.Source)

	rep, err := te.client.Traffic(ctxT(t), api.TrafficQuery{Targets: []string{"main", "hub"}})
	require.NoError(t, err)
	require.True(t, rep.Available)
	require.Len(t, rep.Series, 2)
	require.Eventually(t, func() bool {
		pts, err := te.h.st.TunnelPoints("main", state.TierM1, time.Time{}, time.Time{})
		return err == nil && len(pts) > 0
	}, testWait, 20*time.Millisecond, "flushed to state.db")

	require.NoError(t, te.client.TunnelDelete(ctxT(t), "main", nil))
	require.Eventually(t, func() bool {
		nft.mu.Lock()
		defer nft.mu.Unlock()
		return !nft.table
	}, testWait, 10*time.Millisecond, "no tunnels: the table is removed")
	te.h.traffic.mu.Lock()
	_, kept := te.h.traffic.rings["t:main"]
	te.h.traffic.mu.Unlock()
	require.False(t, kept)
}

// TestTunnelDeleteRemovesAccountingAtOnce: tunnel delete returns with the
// tunnel's counters gone from the accounting table (the sampler's next tick
// is an hour away), and port remove with the port's map elements gone.
func TestTunnelDeleteRemovesAccountingAtOnce(t *testing.T) {
	nft := &fakeNFT{}
	env, o := prepareEnv(t, nil, func(o *Options, _ string) {
		o.DisableStats = false
		o.TrafficInterval = time.Hour
		o.TrafficFlush = time.Hour
	})
	te := &tunnelEnv{testEnv: env, o: o}
	te.sd = newFakeSystemd(t, env)
	sd := env.runner.Handler
	env.runner.Handler = func(c exec.Call) (exec.Response, bool) {
		if r, ok := nft.handle(c); ok {
			return r, true
		}
		return sd(c)
	}
	env.startEnv(o)
	te.tunnelNode("de-1")
	port, port2 := freePort(t), freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: port}, {Listen: port2}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	te.addTunnelUp(api.TunnelAddRequest{ID: "games", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	require.Eventually(t, func() bool { return nft.has("main") && nft.has("games") }, testWait, 10*time.Millisecond)
	require.Contains(t, nft.lastScript(), "tcp . "+strconv.Itoa(port2))

	_, err := te.client.PortRemove(ctxT(t), "main", port2, config.ProtoTCP)
	require.NoError(t, err)
	require.NotContains(t, nft.lastScript(), "tcp . "+strconv.Itoa(port2), "the removed port is no longer counted")

	require.NoError(t, te.client.TunnelDelete(ctxT(t), "games", nil))
	require.False(t, nft.has("games"), "the deleted tunnel's counters are gone when the delete returns")
	require.True(t, nft.has("main"))

	require.NoError(t, te.client.TunnelDelete(ctxT(t), "main", nil))
	nft.mu.Lock()
	table := nft.table
	nft.mu.Unlock()
	require.False(t, table, "no tunnels left: the table is gone when the delete returns")
}

// TestCollectMetricsNATStale: when the node of a kernel-NAT rung does not
// answer, the record says so instead of keeping old numbers silently.
func TestCollectMetricsNATStale(t *testing.T) {
	te := startTunnelHub(t, withIPForward)
	n := te.tunnelNode("de-1")
	n.on(api.CmdMetrics, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return nil, stderrors.New("metrics unavailable")
	})
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "wg", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trNAT, Failover: fastFailover(false)})
	te.h.collectMetrics(ctxT(t))
	m, ok, err := te.h.st.GetMetrics("wg")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, m.Stale)
	require.True(t, m.ConnsUnknown)
	require.Zero(t, m.ActiveConns)
}
