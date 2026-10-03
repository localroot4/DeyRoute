package hub

import (
	"context"
	stderrors "errors"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// The traffic sampler (ARCHITECTURE.md §8.1, docs/en/security.md).
//
// Every TrafficInterval the hub reads the byte counters of `table inet
// deyroute_stats` with ONE `nft list counters` for every tunnel and turns
// them into deltas: a counter that went down (a reboot, `nft reset`) is a
// reset, its delta is the new value and the point is flagged. Elapsed time
// and rates come from the monotonic clock; a sample whose elapsed time is
// not positive or more than twice the interval, or whose wall clock moved
// more than trafficJumpSlack away from the monotonic one (an NTP step), is a
// gap: its bytes still count toward the totals but it has no rate and is
// drawn as missing. After a wall-clock jump the points in memory are moved
// onto the new clock.
//
// The points of the last hour stay in a ring in memory (Status, the 1 h
// period and the dashboard read only that), together with half-hour byte
// sums of the last 32 days per tunnel for "today", the rolling 30 days and
// the quota period. Every TrafficFlush the samples since the last flush
// are folded into state.db in ONE transaction (state.AppendTraffic) that
// also stores the raw counter reading of the last folded sample and its
// sequence number, so a restart continues from exactly what was stored and
// a repeated flush can never count anything twice.

// Defaults of the traffic sampler (Options.TrafficInterval, TrafficFlush).
const (
	// DefaultTrafficInterval is how often the byte counters of inet
	// deyroute_stats are read (one nft call for every tunnel).
	DefaultTrafficInterval = 10 * time.Second
	// DefaultTrafficFlush is how often the samples in memory are folded
	// into the traffic bucket of state.db (one transaction).
	DefaultTrafficFlush = time.Minute
)

// Limits of the traffic sampler.
const (
	// trafficRingSpan is how much history the live ring keeps per series.
	trafficRingSpan = time.Hour
	// trafficJumpSlack is the largest difference between the wall-clock
	// and the monotonic time between two samples that is not a clock jump.
	trafficJumpSlack = 2 * time.Second
	// trafficMaxPending bounds the samples per series kept in memory while
	// flushes fail (24 h at the default interval); older ones are dropped.
	trafficMaxPending = 8640
	// trafficRetry is how often an unavailable accounting table is tried
	// again (nft missing, nf_tables not usable).
	trafficRetry = 5 * time.Minute
	// trafficRebuildGap is the shortest time between two rebuilds forced by
	// a table that disappeared (flush ruleset) or lost a counter.
	trafficRebuildGap = 30 * time.Second
	// trafficHalfHour is the width of the in-memory byte sums and
	// trafficHalfHours how many are kept (32 days: a 31-day quota period
	// still fits).
	trafficHalfHour  = 30 * time.Minute
	trafficHalfHours = 32 * 48
	// trafficQuotaWarnPct and trafficQuotaFullPct are the quota thresholds.
	trafficQuotaWarnPct = 80
	trafficQuotaFullPct = 100

	// metaTrafficQuota + <tunnel> remembers the quota events sent in the
	// current period; metaTrafficGuard whether the size guard is on.
	metaTrafficQuota = "traffic/quota/"
	metaTrafficGuard = "traffic/guard"
)

// trafficStore is the part of state.Store the sampler uses. The hub passes
// its store; tests wrap it to count transactions and reads.
type trafficStore interface {
	AppendTraffic(b state.TrafficBatch, r state.Retention) (state.TrafficResult, error)
	TrafficCheckpoint() (state.TrafficCheckpoint, bool, error)
	TunnelPoints(tunnel, tier string, from, to time.Time) ([]state.TunnelPoint, error)
	HostPoints(node, tier string, from, to time.Time) ([]state.HostPoint, error)
	RetainTrafficSeries(tunnels, nodes []string) ([]string, error)
	SizeGuard(budget int64, wasOver bool) (over bool, live int64, err error)
	GetMeta(key string, v any) (bool, error)
	PutMeta(key string, v any) error
	DeleteMeta(key string) error
}

// trafficDeps is everything the sampler needs from the hub.
type trafficDeps struct {
	runner exec.Runner
	store  trafficStore
	log    *slog.Logger
	// now is the wall clock; when it carries a monotonic reading (time.Now)
	// elapsed times use it. mono, when set, replaces that reading (tests).
	now  func() time.Time
	mono func() time.Duration
	loc  *time.Location
	// interval is TrafficInterval; connsMaxAge how old a connection count
	// may be before it is unknown (two metrics passes).
	interval    time.Duration
	connsMaxAge time.Duration
	budget      int64
	config      func() *config.Config
	// running reports whether a tunnel's engine runs a candidate (UP,
	// DEGRADED, PAUSED); points of other tunnels are gaps.
	running func(tunnel string) bool
	// nat reports whether a tunnel has a kernel-NAT rung on the hub.
	nat func(tunnel string) bool
	// host samples the hub's own CPU (permille) and RAM in use.
	host func() (cpuPermille uint16, ram uint64)
	emit func(state.Event)
}

// trafficReading is the raw counter pair of one tunnel.
type trafficReading struct {
	In, Out       uint64
	PktIn, PktOut uint64
	Since         time.Time
}

// livePoint is one point of the live ring (a sample, or a 1-minute point
// loaded from state.db at start).
type livePoint struct {
	start   time.Time // interval start, UTC
	secs    uint16    // seconds covered
	flags   uint8     // state.Flag*
	in, out uint64
	conns   uint32
	cpu     uint16 // permille (host series)
	cpuMax  uint16
	ram     uint64
}

// liveRate is the latest sample of a tunnel (TrafficNow).
type liveRate struct {
	at       time.Time
	in, out  float64 // bit/s
	measured bool    // false for a gap or a reset: no rate
}

// connsNote is the connection count the metrics pass measured last.
type connsNote struct {
	n     uint32
	known bool
	at    time.Duration // monotonic
}

// nodeAcc collects the heartbeats of a node between two samples.
type nodeAcc struct {
	sum    float64
	n      int
	max    float64
	ramMax uint64
}

// byteSum is the bytes of one half-hour.
type byteSum struct{ in, out uint64 }

// quotaRecord is the metaTrafficQuota record of a tunnel.
type quotaRecord struct {
	Period time.Time `json:"period"`
	Warned bool      `json:"warned,omitempty"`
	Full   bool      `json:"full,omitempty"`
}

// guardRecord is the metaTrafficGuard record.
type guardRecord struct {
	Over bool `json:"over"`
}

// trafficSampler is the hub's traffic sampler. sample, flush and the stats
// table run on the sampler goroutine; Status, Traffic, heartbeats and the
// metrics pass read and note under mu.
type trafficSampler struct {
	d    trafficDeps
	base time.Time // first clock reading: monotonic origin
	kick chan struct{}

	// statsMu serialises the work on the accounting table.
	statsMu     sync.Mutex
	specHash    string
	tableUp     bool // the table exists as last built (with tunnels)
	tableIDs    map[string]bool
	nextTry     time.Duration // monotonic; while unavailable
	lastRebuild time.Duration
	rebuilt     bool
	forceNext   bool
	removed     bool // RemoveStats ran since monitoring was switched off

	mu         sync.Mutex
	started    bool
	enabled    bool
	avail      bool
	availErr   error
	warned     bool
	readErr    bool // a read error was logged (until the next good read)
	seq        uint64
	havePrev   bool
	prevWall   time.Time
	prevMono   time.Duration
	last       map[string]trafficReading
	rings      map[string][]livePoint
	ringCap    int
	pendTun    map[string][]state.TunnelPoint
	pendNode   map[string][]state.HostPoint
	pendHub    []state.HostPoint
	pendSeq    uint64 // seq of the newest sample
	flushedSeq uint64 // seq of the newest stored sample
	pendAt     time.Time
	halves     map[string]map[int64]byteSum
	live       map[string]liveRate
	conns      map[string]connsNote
	nodes      map[string]*nodeAcc
	quota      map[string]quotaRecord
	guardOver  bool
	flushErr   bool
}

func newTrafficSampler(d trafficDeps) *trafficSampler {
	if d.interval <= 0 {
		d.interval = DefaultTrafficInterval
	}
	if d.loc == nil {
		d.loc = time.Local
	}
	if d.budget <= 0 {
		d.budget = state.SizeBudget
	}
	ringCap := int(trafficRingSpan/d.interval) + 2
	s := &trafficSampler{
		d:        d,
		base:     d.now(),
		kick:     make(chan struct{}, 1),
		tableIDs: map[string]bool{},
		last:     map[string]trafficReading{},
		rings:    map[string][]livePoint{},
		ringCap:  max(ringCap, 64),
		pendTun:  map[string][]state.TunnelPoint{},
		pendNode: map[string][]state.HostPoint{},
		halves:   map[string]map[int64]byteSum{},
		live:     map[string]liveRate{},
		conns:    map[string]connsNote{},
		nodes:    map[string]*nodeAcc{},
		quota:    map[string]quotaRecord{},
	}
	return s
}

// newHubTraffic wires the sampler to the hub.
func newHubTraffic(h *Hub) *trafficSampler {
	cpu := sysinfo.NewCPUSampler(h.o.ProcRoot)
	cpu.Sample() // the first sample only sets the baseline
	return newTrafficSampler(trafficDeps{
		runner:      h.o.Runner,
		store:       h.st,
		log:         h.log,
		now:         h.o.Now,
		loc:         h.o.Location,
		interval:    h.o.TrafficInterval,
		connsMaxAge: 2*h.o.MetricsInterval + h.o.TrafficInterval,
		config:      h.Config,
		running: func(id string) bool {
			c := h.tun.lookup(id)
			if c == nil {
				return false
			}
			st, ok := c.liveState()
			return ok && !st.Active.IsZero() && runningState(st.State)
		},
		nat: func(id string) bool {
			c := h.tun.lookup(id)
			if c == nil {
				return false
			}
			_, plan := c.snapshot()
			for _, pc := range plan.Candidates {
				if len(pc.Hub.NAT) > 0 {
					return true
				}
			}
			return false
		},
		host: func() (uint16, uint64) {
			return permille(cpu.Sample()), sysinfo.Mem(h.o.ProcRoot).Used()
		},
		emit: h.Emit,
	})
}

// trafficLoop samples the traffic counters every TrafficInterval and
// flushes them every TrafficFlush until ctx ends (and once more then). It
// never runs with Options.DisableStats.
func (h *Hub) trafficLoop(ctx context.Context) {
	if h.o.DisableStats || ctx.Err() != nil {
		return
	}
	s := h.traffic
	s.start(ctx)
	tick := time.NewTicker(h.o.TrafficInterval)
	defer tick.Stop()
	flush := time.NewTicker(h.o.TrafficFlush)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			// The store is still open: what was sampled is kept.
			s.flush()
			return
		case <-tick.C:
			s.sample(ctx)
		case <-flush.C:
			s.flush()
		case <-s.kick:
			cfg := s.d.config()
			if cfg.MonitoringEnabled() {
				s.ensureStats(ctx, cfg, s.monoNow(s.d.now()), false)
			}
		}
	}
}

// requestCheck makes the sampler compare the accounting table with the
// configuration now (the firewall loop calls it after its debounced
// apply: tunnels, ports or rungs changed). It never blocks.
func (s *trafficSampler) requestCheck() {
	if s == nil {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// monoNow returns the monotonic time of the clock reading raw.
func (s *trafficSampler) monoNow(raw time.Time) time.Duration {
	if s.d.mono != nil {
		return s.d.mono()
	}
	return raw.Sub(s.base)
}

// ---------------------------------------------------------------- start

// start loads what the previous hub process stored (the checkpoint: cursor
// and counter baseline; the last hour of 1-minute points for the ring; 32
// days of half-hour sums; the quota and size-guard records), forgets the
// series of tunnels and nodes no longer in config.yaml and builds the
// accounting table, seeded with the live counters or the baseline.
func (s *trafficSampler) start(ctx context.Context) {
	cfg := s.d.config()
	raw := s.d.now()
	now := raw.UTC()
	cp, found, err := s.d.store.TrafficCheckpoint()
	if err != nil {
		s.d.log.Warn("cannot read the traffic checkpoint; counting starts afresh", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	tunnels, nodes := configIDs(cfg)
	if removed, err := s.d.store.RetainTrafficSeries(tunnels, nodes); err != nil {
		s.d.log.Warn("cannot remove the traffic history of deleted tunnels and nodes", dlog.Err(err))
	} else if len(removed) > 0 {
		s.d.log.Info("traffic history of deleted tunnels and nodes removed", slog.Any("series", removed))
	}
	var guard guardRecord
	if _, err := s.d.store.GetMeta(metaTrafficGuard, &guard); err != nil {
		s.d.log.Debug("cannot read the size guard record", dlog.Err(err))
	}

	halves := map[string]map[int64]byteSum{}
	rings := map[string][]livePoint{}
	quota := map[string]quotaRecord{}
	ringFrom := now.Add(-trafficRingSpan)
	for _, id := range tunnels {
		if pts, err := s.d.store.TunnelPoints(id, state.TierM30, now.Add(-trafficHalfHours*trafficHalfHour), time.Time{}); err == nil {
			m := map[int64]byteSum{}
			for _, p := range pts {
				m[halfIndex(p.Start)] = byteSum{in: p.In, out: p.Out}
			}
			halves[id] = m
		}
		if pts, err := s.d.store.TunnelPoints(id, state.TierM1, ringFrom, time.Time{}); err == nil {
			r := make([]livePoint, 0, len(pts))
			for _, p := range pts {
				r = append(r, livePoint{start: p.Start, secs: p.Secs, flags: p.Flags, in: p.In, out: p.Out, conns: p.Conns})
			}
			rings[state.TunnelSeriesName(id)] = r
		}
		var q quotaRecord
		if ok, err := s.d.store.GetMeta(metaTrafficQuota+id, &q); err == nil && ok {
			quota[id] = q
		}
	}
	for _, name := range append([]string{""}, nodes...) {
		pts, err := s.d.store.HostPoints(name, state.TierM1, ringFrom, time.Time{})
		if err != nil {
			continue
		}
		r := make([]livePoint, 0, len(pts))
		for _, p := range pts {
			r = append(r, livePoint{start: p.Start, secs: p.Secs, flags: p.Flags, cpu: p.CPUPermille, cpuMax: p.CPUMaxPermille, ram: p.RAM})
		}
		key := state.SeriesHub
		if name != "" {
			key = state.NodeSeriesName(name)
		}
		rings[key] = r
	}

	s.mu.Lock()
	s.started = true
	s.enabled = cfg.MonitoringEnabled()
	if found {
		s.seq, s.flushedSeq = cp.Cursor.Seq, cp.Cursor.Seq
		for id, c := range cp.Baseline {
			s.last[id] = trafficReading{In: c.In, Out: c.Out, Since: c.Since}
		}
	}
	s.halves, s.rings, s.quota, s.guardOver = halves, rings, quota, guard.Over
	if !s.enabled {
		s.avail, s.availErr = false, monitoringOffErr()
	}
	s.mu.Unlock()

	if cfg.MonitoringEnabled() {
		s.ensureStats(ctx, cfg, s.monoNow(raw), true)
	} else {
		s.removeStats(ctx)
	}
}

// configIDs returns the tunnel and node ids of cfg.
func configIDs(cfg *config.Config) (tunnels, nodes []string) {
	if cfg == nil {
		return nil, nil
	}
	for i := range cfg.Tunnels {
		tunnels = append(tunnels, cfg.Tunnels[i].ID)
	}
	for _, n := range cfg.Nodes {
		nodes = append(nodes, n.ID)
	}
	return tunnels, nodes
}

// monitoringOffErr is the reason of a hub with monitoring.enabled false.
func monitoringOffErr() error {
	return deyerr.New(deyerr.X061, deyerr.Params{"reason": "monitoring.enabled is false in config.yaml"})
}

// ---------------------------------------------------------------- table

// ensureStats makes `table inet deyroute_stats` match cfg: it rebuilds the
// table only when the set of tunnels, ports or NAT flags changed, when
// forced (start, a table that disappeared or lost a counter; at most once
// per trafficRebuildGap) or, while the table is missing or accounting is
// unavailable, when the retry time has come. A rebuild is seeded with the
// live counters (or, without a table, the last reading), so the totals
// continue. A spec without tunnels removes the table.
func (s *trafficSampler) ensureStats(ctx context.Context, cfg *config.Config, mono time.Duration, force bool) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.removed = false
	spec := render.StatsSpec(cfg, s.d.nat)
	hash := render.StatsHash(spec)
	if s.forceNext {
		force, s.forceNext = true, false
	}
	changed := hash != s.specHash
	retry := !s.tableUp && len(spec.Tunnels) > 0 && mono >= s.nextTry
	if !force && !changed && !retry {
		return
	}
	if force && !changed && s.rebuilt && mono-s.lastRebuild < trafficRebuildGap {
		// A table that keeps disappearing is rebuilt at most this often.
		s.tableUp, s.nextTry = false, s.lastRebuild+trafficRebuildGap
		return
	}
	s.lastRebuild, s.rebuilt = mono, true
	if len(spec.Tunnels) == 0 {
		if err := firewall.RemoveStats(ctx, s.d.runner); err != nil {
			s.d.log.Warn("cannot remove the traffic accounting table", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		}
		s.specHash, s.tableUp, s.tableIDs = hash, false, map[string]bool{}
		s.setAvailable()
		return
	}
	spec.Seed = s.seed(ctx, spec)
	err := firewall.ApplyStats(ctx, s.d.runner, spec)
	s.specHash = hash
	if err != nil {
		s.tableUp, s.nextTry = false, mono+trafficRetry
		s.setUnavailable(err)
		return
	}
	s.tableUp = true
	s.tableIDs = map[string]bool{}
	for _, t := range spec.Tunnels {
		s.tableIDs[t.ID] = true
	}
	s.d.log.Debug("traffic accounting table built", slog.Int("tunnels", len(spec.Tunnels)))
	s.setAvailable()
}

// seed returns the start values of the rebuilt counters: the live reading
// of the table when it exists (a table the previous hub process left, or
// the one being replaced), otherwise the last reading the sampler knows
// (after a reboot or a flush ruleset: the stored baseline), so a rebuild
// never resets a total (statsMu held).
func (s *trafficSampler) seed(ctx context.Context, spec firewall.StatsSpec) map[string]firewall.Counter {
	seed := map[string]firewall.Counter{}
	s.mu.Lock()
	for _, t := range spec.Tunnels {
		if r, ok := s.last[t.ID]; ok {
			seed[firewall.CounterIn(t.ID)] = firewall.Counter{Packets: r.PktIn, Bytes: r.In}
			seed[firewall.CounterOut(t.ID)] = firewall.Counter{Packets: r.PktOut, Bytes: r.Out}
		}
	}
	s.mu.Unlock()
	if live, err := firewall.ReadCounters(ctx, s.d.runner); err == nil {
		for name, c := range live {
			seed[name] = c
		}
	}
	return seed
}

// removeStats deletes the accounting table once monitoring is switched
// off (idempotent; one nft call per switch).
func (s *trafficSampler) removeStats(ctx context.Context) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if s.removed {
		return
	}
	s.removed = true
	if err := firewall.RemoveStats(ctx, s.d.runner); err != nil {
		s.d.log.Warn("cannot remove the traffic accounting table", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	s.specHash, s.tableUp, s.tableIDs = "", false, map[string]bool{}
}

// setAvailable marks the accounting as working.
func (s *trafficSampler) setAvailable() {
	s.mu.Lock()
	was := s.avail
	s.avail, s.availErr, s.warned = true, nil, false
	s.mu.Unlock()
	if !was {
		s.d.log.Info("traffic accounting is active")
	}
}

// setUnavailable marks the accounting as not working (DEY-X061 with the
// reason): the warning is logged and emitted once until it works again.
func (s *trafficSampler) setUnavailable(err error) {
	if !deyerr.HasCode(err, deyerr.X061) {
		err = deyerr.Wrap(deyerr.X061, err, deyerr.Params{"reason": deyerr.As(err).Message()})
	}
	s.mu.Lock()
	s.avail, s.availErr = false, err
	warn := !s.warned
	s.warned = true
	s.mu.Unlock()
	if !warn {
		return
	}
	e := deyerr.As(err)
	reason, _ := e.Params["reason"].(string)
	if reason == "" {
		reason = e.Why()
	}
	s.d.log.Warn("traffic accounting is not available; connection counts only", dlog.Err(err), dlog.Code(e.Code))
	s.d.emit(state.Event{Type: state.EvProbeError, Level: state.LevelWarn, Code: string(e.Code),
		Reason: reason, Message: i18n.T(i18n.HubTrafficUnavailable, reason)})
}

// readCounters reads the counters (one nft call). A missing table, or a
// tunnel of the table without its counters, rebuilds the table at once
// (rate limited); the samples of this interval are then gaps.
func (s *trafficSampler) readCounters(ctx context.Context, cfg *config.Config, mono time.Duration) (map[string]firewall.Counter, bool) {
	s.statsMu.Lock()
	up := s.tableUp
	ids := s.tableIDs
	s.statsMu.Unlock()
	if !up {
		return nil, false
	}
	m, err := firewall.ReadCounters(ctx, s.d.runner)
	if err != nil {
		if stderrors.Is(err, firewall.ErrNoStatsTable) {
			s.d.log.Warn("the traffic accounting table disappeared; rebuilding it", dlog.Code(deyerr.X062))
			s.statsMu.Lock()
			s.tableUp = false
			s.statsMu.Unlock()
			s.ensureStats(ctx, cfg, mono, true)
			return nil, false
		}
		s.mu.Lock()
		logIt := !s.readErr
		s.readErr = true
		s.mu.Unlock()
		if logIt {
			s.d.log.Warn("cannot read the traffic counters; this interval is a gap", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		}
		return nil, false
	}
	s.mu.Lock()
	s.readErr = false
	s.mu.Unlock()
	for id := range ids {
		if _, _, ok := firewall.TunnelCounters(m, id); !ok {
			s.statsMu.Lock()
			s.forceNext = true
			s.statsMu.Unlock()
			break
		}
	}
	return m, true
}

// ---------------------------------------------------------------- sample

// sample takes one sample: counters, connections and host load.
func (s *trafficSampler) sample(ctx context.Context) {
	raw := s.d.now()
	now := raw.UTC()
	mono := s.monoNow(raw)
	cfg := s.d.config()
	if !cfg.MonitoringEnabled() {
		s.mu.Lock()
		s.enabled, s.avail, s.availErr = false, false, monitoringOffErr()
		s.havePrev = false
		s.mu.Unlock()
		s.removeStats(ctx)
		return
	}
	s.mu.Lock()
	wasEnabled := s.enabled
	s.enabled = true
	s.mu.Unlock()
	s.ensureStats(ctx, cfg, mono, !wasEnabled)
	counters, haveCounters := s.readCounters(ctx, cfg, mono)
	cpu, ram := s.d.host()
	running := map[string]bool{}
	for i := range cfg.Tunnels {
		running[cfg.Tunnels[i].ID] = s.d.running(cfg.Tunnels[i].ID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Elapsed time and gaps from the monotonic clock.
	interval := s.d.interval
	elapsed, gap, jump := interval, true, time.Duration(0)
	if s.havePrev {
		elapsed = mono - s.prevMono
		if d := now.Sub(s.prevWall) - elapsed; d > trafficJumpSlack || d < -trafficJumpSlack {
			jump = d
		}
		gap = elapsed <= 0 || elapsed > 2*interval || jump != 0
	}
	if jump != 0 {
		s.rebaseLocked(jump, now)
	}
	secs := interval
	if !gap {
		secs = elapsed
	}
	start := now.Add(-secs)
	cover := uint16(min(max(math.Round(secs.Seconds()), 1), math.MaxUint16))
	s.seq++
	s.havePrev, s.prevWall, s.prevMono = true, now, mono
	s.pendSeq, s.pendAt = s.seq, now

	known := map[string]bool{state.SeriesHub: true}
	for i := range cfg.Tunnels {
		t := &cfg.Tunnels[i]
		known[state.TunnelSeriesName(t.ID)] = true
		if !t.Enabled {
			continue
		}
		p := livePoint{start: start, secs: cover}
		if gap {
			p.flags |= state.FlagGap
		}
		if !running[t.ID] {
			p.flags |= state.FlagGap
		}
		cn, ok := s.conns[t.ID]
		if ok && cn.known && mono-cn.at <= s.d.connsMaxAge {
			p.conns = cn.n
		} else {
			p.flags |= state.FlagConnsUnknown
		}
		rate := liveRate{at: now}
		if haveCounters {
			in, out, okc := firewall.TunnelCounters(counters, t.ID)
			if okc {
				cur := trafficReading{In: in.Bytes, Out: out.Bytes, PktIn: in.Packets, PktOut: out.Packets}
				prev, had := s.last[t.ID]
				cur.Since = prev.Since
				if !had || cur.Since.IsZero() {
					cur.Since = now
				}
				dIn, rIn := counterDelta(prev.In, cur.In)
				dOut, rOut := counterDelta(prev.Out, cur.Out)
				if rIn || rOut {
					p.flags |= state.FlagReset
					cur.Since = now
				}
				p.in, p.out = dIn, dOut
				s.last[t.ID] = cur
				if p.flags&(state.FlagGap|state.FlagReset) == 0 && !gap && secs > 0 {
					rate.in = sanitize(float64(dIn) * 8 / secs.Seconds())
					rate.out = sanitize(float64(dOut) * 8 / secs.Seconds())
					rate.measured = true
				}
				s.addHalfLocked(t.ID, start, dIn, dOut)
			} else {
				p.flags |= state.FlagGap
			}
		} else {
			p.flags |= state.FlagGap
		}
		s.live[t.ID] = rate
		name := state.TunnelSeriesName(t.ID)
		s.appendRingLocked(name, p)
		s.pendTun[t.ID] = capPending(append(s.pendTun[t.ID], state.TunnelPoint{
			Start: p.start, In: p.in, Out: p.out, Conns: p.conns, Secs: p.secs, Flags: p.flags,
		}))
	}

	hub := livePoint{start: start, secs: cover, cpu: cpu, cpuMax: cpu, ram: ram}
	if gap {
		hub.flags |= state.FlagGap
	}
	s.appendRingLocked(state.SeriesHub, hub)
	s.pendHub = capPending(append(s.pendHub, state.HostPoint{
		Start: hub.start, CPUPermille: hub.cpu, CPUMaxPermille: hub.cpuMax, RAM: hub.ram, Secs: hub.secs, Flags: hub.flags,
	}))
	for _, n := range cfg.Nodes {
		name := state.NodeSeriesName(n.ID)
		known[name] = true
		acc := s.nodes[n.ID]
		if acc == nil || acc.n == 0 {
			continue // no heartbeat in this interval (offline): a gap
		}
		p := livePoint{start: start, secs: cover, cpu: permille(acc.sum / float64(acc.n)), cpuMax: permille(acc.max), ram: acc.ramMax}
		if gap {
			p.flags |= state.FlagGap
		}
		delete(s.nodes, n.ID)
		s.appendRingLocked(name, p)
		s.pendNode[n.ID] = capPending(append(s.pendNode[n.ID], state.HostPoint{
			Start: p.start, CPUPermille: p.cpu, CPUMaxPermille: p.cpuMax, RAM: p.ram, Secs: p.secs, Flags: p.flags,
		}))
	}
	// Series of tunnels and nodes deleted meanwhile go.
	for name := range s.rings {
		if !known[name] {
			delete(s.rings, name)
		}
	}
}

// counterDelta returns cur-prev, or cur when the counter went down (a
// reset: a reboot, `nft reset counters`, a table someone recreated).
func counterDelta(prev, cur uint64) (delta uint64, reset bool) {
	if cur < prev {
		return cur, true
	}
	return cur - prev, false
}

// rebaseLocked moves the points in memory onto the wall clock after a jump
// of d (the new wall clock minus the old one, beyond the monotonic time):
// the ring, the pending samples and the half-hour sums. Sums stamped in
// the future of the new clock are dropped (mu held).
func (s *trafficSampler) rebaseLocked(d time.Duration, now time.Time) {
	s.d.log.Warn("the wall clock jumped; traffic samples in memory moved onto the new time",
		slog.Duration("jump", d))
	for name, r := range s.rings {
		for i := range r {
			r[i].start = r[i].start.Add(d)
		}
		s.rings[name] = r
	}
	for id, pts := range s.pendTun {
		for i := range pts {
			pts[i].Start = pts[i].Start.Add(d)
		}
		s.pendTun[id] = pts
	}
	for id, pts := range s.pendNode {
		for i := range pts {
			pts[i].Start = pts[i].Start.Add(d)
		}
		s.pendNode[id] = pts
	}
	for i := range s.pendHub {
		s.pendHub[i].Start = s.pendHub[i].Start.Add(d)
	}
	shift := int64(d / trafficHalfHour)
	limit := halfIndex(now.Add(state.FutureSlack))
	for id, m := range s.halves {
		moved := make(map[int64]byteSum, len(m))
		for k, v := range m {
			if k+shift > limit {
				continue
			}
			moved[k+shift] = v
		}
		s.halves[id] = moved
	}
}

// appendRingLocked adds p to the ring of series name, keeping one hour
// before its newest point and at most ringCap points (mu held).
func (s *trafficSampler) appendRingLocked(name string, p livePoint) {
	r := append(s.rings[name], p)
	cut := 0
	from := p.start.Add(-trafficRingSpan)
	for cut < len(r) && (r[cut].start.Before(from) || len(r)-cut > s.ringCap) {
		cut++
	}
	s.rings[name] = r[cut:]
}

// addHalfLocked adds bytes to the half-hour sums of a tunnel and drops the
// sums older than trafficHalfHours before the newest (mu held).
func (s *trafficSampler) addHalfLocked(id string, at time.Time, in, out uint64) {
	m := s.halves[id]
	if m == nil {
		m = map[int64]byteSum{}
		s.halves[id] = m
	}
	k := halfIndex(at)
	v := m[k]
	v.in, v.out = satAdd(v.in, in), satAdd(v.out, out)
	m[k] = v
	for old := range m {
		if old <= k-trafficHalfHours {
			delete(m, old)
		}
	}
}

// capPending keeps the newest trafficMaxPending points.
func capPending[T any](p []T) []T {
	if len(p) > trafficMaxPending {
		return p[len(p)-trafficMaxPending:]
	}
	return p
}

// halfIndex is the number of the half-hour that contains t (unix time).
func halfIndex(t time.Time) int64 {
	return floorDiv(t.Unix(), int64(trafficHalfHour/time.Second))
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func satAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

// sanitize turns NaN and ±Inf into 0 (a DTO must always marshal).
func sanitize(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0
	}
	return f
}

// permille converts a CPU percentage into permille (0-1000).
func permille(pct float64) uint16 {
	pct = sanitize(pct)
	return uint16(min(math.Round(pct*10), 1000)) // #nosec G115 -- clamped to 0-1000
}

// ---------------------------------------------------------------- notes

// noteNode records one heartbeat of a node (CPU %, RAM bytes); the next
// sample turns the heartbeats since the last one into a point (average CPU,
// highest CPU and RAM).
func (s *trafficSampler) noteNode(id string, cpuPct float64, ram uint64) {
	if s == nil {
		return
	}
	cpuPct = sanitize(cpuPct)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || !s.enabled {
		return
	}
	acc := s.nodes[id]
	if acc == nil {
		acc = &nodeAcc{}
		s.nodes[id] = acc
	}
	acc.sum += cpuPct
	acc.n++
	acc.max = max(acc.max, cpuPct)
	acc.ramMax = max(acc.ramMax, ram)
}

// noteConns records the connection count the metrics pass measured for a
// tunnel (known false: not measured, e.g. UDP only or the node did not
// answer); the samples copy it until it is older than connsMaxAge.
func (s *trafficSampler) noteConns(id string, n int, known bool) {
	if s == nil {
		return
	}
	mono := s.monoNow(s.d.now())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conns[id] = connsNote{n: uint32(max(n, 0)), known: known, at: mono} // #nosec G115 -- non-negative
}

// counter returns the last raw counter reading of a tunnel while the
// accounting works (metrics and `tunnel show`).
func (s *trafficSampler) counter(id string) (state.TrafficCounter, bool) {
	if s == nil {
		return state.TrafficCounter{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.last[id]
	if !ok || !s.avail || !s.enabled || !s.started {
		return state.TrafficCounter{}, false
	}
	return state.TrafficCounter{In: r.In, Out: r.Out, Since: r.Since}, true
}

// forget drops everything the sampler holds about a deleted tunnel; its
// stored series is deleted with the tunnel (state.DeleteTunnel).
func (s *trafficSampler) forget(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rings, state.TunnelSeriesName(id))
	delete(s.pendTun, id)
	delete(s.halves, id)
	delete(s.last, id)
	delete(s.live, id)
	delete(s.conns, id)
	delete(s.quota, id)
}

// forgetNode drops everything the sampler holds about a removed node.
func (s *trafficSampler) forgetNode(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rings, state.NodeSeriesName(id))
	delete(s.pendNode, id)
	delete(s.nodes, id)
}

// forgetTraffic forgets a deleted tunnel in the sampler and its quota
// record.
func (h *Hub) forgetTraffic(id string) {
	h.traffic.forget(id)
	if err := h.st.DeleteMeta(metaTrafficQuota + id); err != nil {
		h.log.Warn("cannot delete the quota record", dlog.Tunnel(id), dlog.Err(err))
	}
}

// ---------------------------------------------------------------- flush

// flush folds the samples since the last flush into state.db in ONE
// transaction (points of every series, the counter baseline of the last
// sample and its sequence number), then runs the size guard and the quota
// check. On a failure the samples stay in memory for the next flush.
func (s *trafficSampler) flush() {
	cfg := s.d.config()
	now := s.d.now().UTC()
	s.mu.Lock()
	if !s.started || s.pendSeq <= s.flushedSeq {
		s.mu.Unlock()
		s.checkQuota(cfg, now)
		return
	}
	resetDay, loc := cfg.QuotaResetDay(), s.d.loc
	b := state.TrafficBatch{
		Cursor:      state.TrafficCursor{Seq: s.pendSeq, At: s.pendAt},
		Baseline:    make(map[string]state.TrafficCounter, len(s.last)),
		Tunnels:     make(map[string][]state.TunnelPoint, len(s.pendTun)),
		Nodes:       make(map[string][]state.HostPoint, len(s.pendNode)),
		Hub:         s.pendHub,
		PeriodStart: func(t time.Time) time.Time { return state.PeriodStart(t, resetDay, loc) },
		Now:         now,
	}
	for id, r := range s.last {
		b.Baseline[id] = state.TrafficCounter{In: r.In, Out: r.Out, Since: r.Since}
	}
	for id, p := range s.pendTun {
		b.Tunnels[id] = p
	}
	for id, p := range s.pendNode {
		b.Nodes[id] = p
	}
	over := s.guardOver
	s.mu.Unlock()

	res, err := s.d.store.AppendTraffic(b, state.RetentionFor(over))
	s.mu.Lock()
	if err != nil {
		logIt := !s.flushErr
		s.flushErr = true
		s.mu.Unlock()
		if logIt {
			s.d.log.Warn("cannot store the traffic samples; they are kept in memory and stored at the next flush",
				dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		}
		return
	}
	s.flushErr = false
	s.flushedSeq = b.Cursor.Seq
	// Everything up to the cursor is stored (or was stored before, when
	// res.Applied is false): only samples taken meanwhile stay pending.
	s.trimPendingLocked(b)
	s.mu.Unlock()
	if len(res.Refused) > 0 {
		s.d.log.Warn("traffic series not recorded: too many tunnels or nodes", slog.Any("series", res.Refused))
	}
	if res.FutureDeleted > 0 {
		s.d.log.Info("traffic points stamped in the future removed (the clock was ahead)", slog.Int("points", res.FutureDeleted))
	}
	s.sizeGuard()
	s.checkQuota(cfg, now)
}

// trimPendingLocked removes the points of batch b from the pending samples
// (mu held). Samples are only appended, so b's points are a prefix.
func (s *trafficSampler) trimPendingLocked(b state.TrafficBatch) {
	for id, p := range b.Tunnels {
		s.pendTun[id] = dropPrefix(s.pendTun[id], len(p))
		if len(s.pendTun[id]) == 0 {
			delete(s.pendTun, id)
		}
	}
	for id, p := range b.Nodes {
		s.pendNode[id] = dropPrefix(s.pendNode[id], len(p))
		if len(s.pendNode[id]) == 0 {
			delete(s.pendNode, id)
		}
	}
	s.pendHub = dropPrefix(s.pendHub, len(b.Hub))
}

func dropPrefix[T any](p []T, n int) []T {
	if n >= len(p) {
		return nil
	}
	return append([]T(nil), p[n:]...)
}

// sizeGuard turns the reduced 1-minute retention on when the live data of
// state.db passes 80 % of its budget (off again below 60 %), and emits
// DEY-X063 once when it turns on.
func (s *trafficSampler) sizeGuard() {
	s.mu.Lock()
	was := s.guardOver
	s.mu.Unlock()
	over, live, err := s.d.store.SizeGuard(s.d.budget, was)
	if err != nil {
		s.d.log.Debug("size guard failed", dlog.Err(err))
		return
	}
	if over == was {
		return
	}
	s.mu.Lock()
	s.guardOver = over
	s.mu.Unlock()
	if err := s.d.store.PutMeta(metaTrafficGuard, guardRecord{Over: over}); err != nil {
		s.d.log.Debug("cannot store the size guard record", dlog.Err(err))
	}
	if !over {
		s.d.log.Info("state.db is back within its budget; 1-minute traffic points are kept for 24 hours again",
			slog.Int64("bytes", live))
		return
	}
	e := deyerr.New(deyerr.X063, deyerr.Params{"size": sizeText(live), "budget": sizeText(s.d.budget)})
	s.d.log.Warn("state.db is above its size budget; 1-minute traffic points are kept for 6 hours", dlog.Code(e.Code),
		slog.Int64("bytes", live))
	s.d.emit(state.Event{Type: state.EvProbeError, Level: state.LevelWarn, Code: string(e.Code), Message: e.Message() + "; " + e.Why()})
}

// sizeText formats a byte count in MiB.
func sizeText(n int64) string {
	return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MiB"
}

// checkQuota emits traffic_quota once per period and threshold for every
// tunnel with advanced.monthly_quota_gib: a warning at 80 % and an error at
// 100 % of in+out since the start of the quota period (monitoring.
// quota_reset_day, hub-local). Sent events are remembered in state.db.
func (s *trafficSampler) checkQuota(cfg *config.Config, now time.Time) {
	if cfg == nil || !cfg.MonitoringEnabled() {
		return
	}
	periodStart := state.PeriodStart(now, cfg.QuotaResetDay(), s.d.loc)
	for i := range cfg.Tunnels {
		t := &cfg.Tunnels[i]
		quota := t.QuotaBytes()
		if quota == 0 {
			continue
		}
		s.mu.Lock()
		in, out := s.sumSinceLocked(t.ID, periodStart, now)
		rec := s.quota[t.ID]
		s.mu.Unlock()
		if !rec.Period.Equal(periodStart) {
			rec = quotaRecord{Period: periodStart}
		}
		used := satAdd(in, out)
		pct := float64(used) * 100 / float64(quota)
		changed := false
		switch {
		case pct >= trafficQuotaFullPct && !rec.Full:
			rec.Full, rec.Warned, changed = true, true, true
			s.d.emit(state.Event{Type: state.EvTrafficQuota, Level: state.LevelError, Tunnel: t.ID,
				Message: i18n.T(i18n.HubTrafficQuotaFull, t.ID, sizeGiB(used), t.Advanced.MonthlyQuotaGiB, periodStart.In(s.d.loc).Format(time.DateOnly))})
		case pct >= trafficQuotaWarnPct && !rec.Warned && !rec.Full:
			rec.Warned, changed = true, true
			s.d.emit(state.Event{Type: state.EvTrafficQuota, Level: state.LevelWarn, Tunnel: t.ID,
				Message: i18n.T(i18n.HubTrafficQuotaWarn, t.ID, int(pct), sizeGiB(used), t.Advanced.MonthlyQuotaGiB)})
		}
		s.mu.Lock()
		s.quota[t.ID] = rec
		s.mu.Unlock()
		if changed {
			if err := s.d.store.PutMeta(metaTrafficQuota+t.ID, rec); err != nil {
				s.d.log.Warn("cannot store the quota record", dlog.Tunnel(t.ID), dlog.Err(err))
			}
		}
	}
}

// sizeGiB formats bytes in GiB with one decimal.
func sizeGiB(n uint64) string {
	return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64)
}

// sumSinceLocked returns the bytes of a tunnel from the half-hour that
// contains from up to now (inclusive; never sums stamped in the future of
// now by more than FutureSlack) (mu held).
func (s *trafficSampler) sumSinceLocked(id string, from, now time.Time) (in, out uint64) {
	lo, hi := halfIndex(from), halfIndex(now.Add(state.FutureSlack))
	for k, v := range s.halves[id] {
		if k >= lo && k <= hi {
			in, out = satAdd(in, v.in), satAdd(out, v.out)
		}
	}
	return in, out
}
