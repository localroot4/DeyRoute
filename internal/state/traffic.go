package state

import (
	"encoding/binary"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Traffic time series live in nested buckets traffic/<series>/<tier>:
//
//   - series "t:<tunnel>" holds TunnelPoint records (bytes in/out, the
//     highest connection count), "n:<node>" and "hub" hold HostPoint records
//     (CPU, RAM);
//   - tier "m1" holds 1-minute points, "m30" 30-minute points and, for
//     tunnel series only, "period" one point per quota period
//     (monitoring.quota_reset_day, hub-local);
//   - keys are the big-endian uint32 unix seconds (UTC) of the point's start:
//     an epoch multiple of 60 s for m1 and of 1800 s for m30 (so the 30-minute
//     buckets stay aligned for zones such as +03:30), and the instant the
//     quota period starts for period;
//   - values are fixed-size big-endian records (TunnelRecordLen,
//     HostRecordLen); a longer value written by a newer release still decodes.
//
// The key "checkpoint" of the traffic bucket (a plain value, never a series)
// holds the flush cursor and the raw counter baseline as JSON. AppendTraffic
// writes the points, the baseline and the cursor in one transaction, so a
// crash leaves either the whole flush or none of it, and a flush whose cursor
// is not newer than the stored one is refused as a whole: folding the same
// samples twice can never count them twice.
//
// Retention is by count, relative to the newest stored key: each tier keeps
// its newest N keys, never "everything newer than now - X". A wall clock that
// jumps forward therefore cannot wipe the history, and keys stamped more than
// FutureSlack after the flush's Now (left behind by a clock that was ahead
// and was corrected) are deleted when they are seen.

// Traffic tiers.
const (
	TierM1     = "m1"     // 1-minute points
	TierM30    = "m30"    // 30-minute points
	TierPeriod = "period" // one point per quota period
)

// Steps of the fixed-size tiers.
const (
	StepM1  = time.Minute
	StepM30 = 30 * time.Minute
)

// Traffic series names.
const (
	// SeriesHub is the hub's own CPU/RAM series.
	SeriesHub = "hub"
	// SeriesTunnelPrefix starts the series of a tunnel ("t:<tunnel>").
	SeriesTunnelPrefix = "t:"
	// SeriesNodePrefix starts the series of a node ("n:<node>").
	SeriesNodePrefix = "n:"
)

// trafficCheckpointKey is the plain key of the traffic bucket that holds the
// TrafficCheckpoint. Series names always contain ':' or are "hub".
const trafficCheckpointKey = "checkpoint"

// Limits of the traffic store (design A2, section 12 budget).
const (
	// MaxTrafficTunnels and MaxTrafficNodes cap the number of series of each
	// kind; a series beyond the cap is not recorded (TrafficResult.Refused)
	// and the series already stored keep working.
	MaxTrafficTunnels = 64
	MaxTrafficNodes   = 64
	// DefaultM1Keep is 24 h of 1-minute points; ReducedM1Keep (6 h) is used
	// while the size guard is on.
	DefaultM1Keep = 1440
	ReducedM1Keep = 360
	// DefaultM30Keep is 32 days of 30-minute points, so a 31-day quota
	// period can still be summed at its end.
	DefaultM30Keep = 1536
	// DefaultPeriodKeep is how many quota periods are kept (13 months).
	DefaultPeriodKeep = 13
	// FutureSlack: stored keys newer than the flush's Now plus this are
	// mis-stamped (the clock was ahead and has been corrected) and deleted;
	// batch points that far ahead are skipped.
	FutureSlack = time.Hour
)

// Flags of TunnelPoint and HostPoint.
const (
	// FlagGap marks a point whose interval contains missing samples (hub
	// down, a sample far from the interval, a wall-clock jump, the tunnel
	// not UP). Its bytes still count toward the totals.
	FlagGap uint8 = 1 << iota
	// FlagReset marks a point that contains a counter reset (the counter
	// went down: a table rebuild or a reboot).
	FlagReset
	// FlagPartial marks a point that covers less than its step (Secs).
	FlagPartial
	// FlagConnsUnknown marks a point whose connection count was not
	// measured (UDP has no ESTABLISHED state): Conns is not 0 but unknown.
	FlagConnsUnknown
)

// Record sizes of the stored values.
const (
	// TunnelRecordLen: in u64, out u64, conns u32, secs u16, flags u8.
	TunnelRecordLen = 23
	// HostRecordLen: cpu u16, cpu max u16, ram u64, secs u16, flags u8.
	HostRecordLen = 15
)

// TunnelPoint is one point of a tunnel series.
type TunnelPoint struct {
	// Start is the start of the point's interval (UTC on reads). On writes
	// it is truncated to the tier's step.
	Start time.Time
	// In and Out are the bytes counted in the interval: In = upload from
	// users, Out = download to users.
	In, Out uint64
	// Conns is the highest connection count seen in the interval.
	Conns uint32
	// Secs is how many seconds of the interval have samples; 0 on a write
	// means the whole minute. It is at most the step (the period tier
	// saturates at 65535).
	Secs uint16
	// Flags are FlagGap, FlagReset, FlagPartial and FlagConnsUnknown.
	Flags uint8
}

// HostPoint is one point of a host series (hub or node).
type HostPoint struct {
	Start time.Time
	// CPUPermille is the average CPU use in permille (1000 = all CPUs),
	// weighted by the seconds covered; CPUMaxPermille the highest sample.
	CPUPermille    uint16
	CPUMaxPermille uint16
	// RAM is the average memory use in bytes, weighted like CPUPermille.
	RAM   uint64
	Secs  uint16
	Flags uint8
}

// TrafficCounter is one raw counter reading of a tunnel (the nft counter
// pair). Since is when the counters started (the last reset), zero when not
// known.
type TrafficCounter struct {
	In    uint64    `json:"in"`
	Out   uint64    `json:"out"`
	Since time.Time `json:"since,omitzero"`
}

// TrafficCursor identifies the last sample folded into storage. Seq is the
// sampler's sample sequence number (it only grows, also across restarts and
// wall-clock jumps); At is that sample's wall time (informational).
type TrafficCursor struct {
	Seq uint64    `json:"seq"`
	At  time.Time `json:"at"`
}

// TrafficCheckpoint is what the hub continues from after a restart: the
// cursor of the last flush and the raw counter reading of every tunnel at
// that sample (not the latest reading: samples after the cursor were not
// stored yet).
type TrafficCheckpoint struct {
	Cursor   TrafficCursor             `json:"cursor"`
	Baseline map[string]TrafficCounter `json:"baseline,omitempty"`
}

// TrafficBatch is one flush.
type TrafficBatch struct {
	// Cursor must be newer (a greater Seq) than the stored one; otherwise
	// the batch was already folded and nothing is written.
	Cursor TrafficCursor
	// Baseline replaces the stored counter baseline when it is not nil
	// (entries of tunnels without a record are dropped).
	Baseline map[string]TrafficCounter
	// Tunnels holds the 1-minute points per tunnel id, Nodes per node id,
	// Hub the hub's own points. Points of the same minute are merged: bytes
	// and seconds add up, connections and CPU peaks take the maximum,
	// flags are OR-ed.
	Tunnels map[string][]TunnelPoint
	Nodes   map[string][]HostPoint
	Hub     []HostPoint
	// PeriodStart returns the start of the quota period that contains t
	// (hub-local; see PeriodStart). nil = the calendar month in UTC.
	PeriodStart func(t time.Time) time.Time
	// Now is the wall clock of the flush (FutureSlack); zero = the store's
	// clock.
	Now time.Time
}

// Retention is how many keys each tier keeps (zero = the default).
type Retention struct {
	M1      int // DefaultM1Keep, ReducedM1Keep while the size guard is on
	M30     int // DefaultM30Keep
	Periods int // DefaultPeriodKeep
}

func (r Retention) withDefaults() Retention {
	if r.M1 <= 0 {
		r.M1 = DefaultM1Keep
	}
	if r.M30 <= 0 {
		r.M30 = DefaultM30Keep
	}
	if r.Periods <= 0 {
		r.Periods = DefaultPeriodKeep
	}
	return r
}

// TrafficResult reports what AppendTraffic did.
type TrafficResult struct {
	// Applied is false when the cursor was not newer than the stored one:
	// the batch was folded before and nothing was written.
	Applied bool
	// Refused lists the series not recorded because their kind is at its
	// cap (MaxTrafficTunnels, MaxTrafficNodes); doctor reports them.
	Refused []string
	// Unknown lists the series not recorded because their tunnel or node
	// record no longer exists (deleted while the flush was prepared).
	Unknown []string
	// Skipped counts batch points with a zero, pre-1970 or future start.
	Skipped int
	// FutureDeleted counts stored keys deleted for being newer than
	// Now + FutureSlack.
	FutureDeleted int
}

// TunnelSeriesName returns the series name of a tunnel ("t:<id>").
func TunnelSeriesName(tunnel string) string { return SeriesTunnelPrefix + tunnel }

// NodeSeriesName returns the series name of a node ("n:<id>").
func NodeSeriesName(node string) string { return SeriesNodePrefix + node }

// PeriodStart returns the start of the quota period that contains t: the
// latest midnight in loc of day resetDay (clamped to 1..28) not after t.
// resetDay 1 is the calendar month.
func PeriodStart(t time.Time, resetDay int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	resetDay = min(max(resetDay, 1), 28)
	lt := t.In(loc)
	start := time.Date(lt.Year(), lt.Month(), resetDay, 0, 0, 0, 0, loc)
	if start.After(lt) {
		start = time.Date(lt.Year(), lt.Month()-1, resetDay, 0, 0, 0, 0, loc)
	}
	return start
}

// trafficFailpoint lets tests abort a flush between its steps (a crash
// inside the transaction); nil in production.
var trafficFailpoint func(stage string) error

var (
	errStaleFlush  = stderrors.New("traffic flush already folded")
	errUnknownTier = stderrors.New("unknown traffic tier")
	errBadRecord   = stderrors.New("traffic record too short")
)

// AppendTraffic folds one flush into the traffic bucket in ONE transaction:
// every point goes into m1 (merged with an existing key of the same
// minute), into the m30 bucket and into the quota period that contain it;
// each touched tier is pruned to its newest Retention keys; keys newer than
// Now + FutureSlack are deleted from every series; the cursor and the
// baseline are stored. A series is recorded only while its tunnel
// (tunnels/<id>) or node (nodes/<id>) record exists, so a flush that races
// with DeleteTunnel or DeleteNode cannot bring the series back.
func (s *Store) AppendTraffic(b TrafficBatch, r Retention) (TrafficResult, error) {
	r = r.withDefaults()
	now := b.Now
	if now.IsZero() {
		now = s.now()
	}
	limit := now.Add(FutureSlack).Unix()
	periodOf := b.PeriodStart
	if periodOf == nil {
		periodOf = func(t time.Time) time.Time { return PeriodStart(t, 1, time.UTC) }
	}
	var res TrafficResult
	err := s.db.Update(func(tx *bolt.Tx) error {
		res = TrafficResult{}
		root, err := bucket(tx, BucketTraffic)
		if err != nil {
			return err
		}
		cp, found, err := readCheckpoint(root)
		if err != nil {
			return err
		}
		if found && b.Cursor.Seq <= cp.Cursor.Seq {
			return errStaleFlush
		}
		tunnels, err := bucket(tx, BucketTunnels)
		if err != nil {
			return err
		}
		nodes, err := bucket(tx, BucketNodes)
		if err != nil {
			return err
		}
		nTun, nNode := countSeries(root)
		w := trafficWriter{root: root, ret: r, now: now, limit: limit, periodOf: periodOf, res: &res}

		for _, id := range sortedKeys(b.Tunnels) {
			name := TunnelSeriesName(id)
			switch {
			case len(b.Tunnels[id]) == 0:
				continue
			case tunnels.Get([]byte(id)) == nil:
				res.Unknown = append(res.Unknown, name)
			case root.Bucket([]byte(name)) == nil && nTun >= MaxTrafficTunnels:
				res.Refused = append(res.Refused, name)
			default:
				if root.Bucket([]byte(name)) == nil {
					nTun++
				}
				if err := w.tunnel(name, b.Tunnels[id]); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
		}
		for _, id := range sortedKeys(b.Nodes) {
			name := NodeSeriesName(id)
			switch {
			case len(b.Nodes[id]) == 0:
				continue
			case nodes.Get([]byte(id)) == nil:
				res.Unknown = append(res.Unknown, name)
			case root.Bucket([]byte(name)) == nil && nNode >= MaxTrafficNodes:
				res.Refused = append(res.Refused, name)
			default:
				if root.Bucket([]byte(name)) == nil {
					nNode++
				}
				if err := w.host(name, b.Nodes[id]); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
		}
		if len(b.Hub) > 0 {
			if err := w.host(SeriesHub, b.Hub); err != nil {
				return fmt.Errorf("%s: %w", SeriesHub, err)
			}
		}
		if err := w.deleteFuture(); err != nil {
			return err
		}

		cp.Cursor = TrafficCursor{Seq: b.Cursor.Seq, At: utc(b.Cursor.At)}
		if b.Baseline != nil {
			cp.Baseline = make(map[string]TrafficCounter, len(b.Baseline))
			for id, c := range b.Baseline {
				if tunnels.Get([]byte(id)) == nil {
					continue
				}
				c.Since = utc(c.Since)
				cp.Baseline[id] = c
			}
		}
		if trafficFailpoint != nil {
			if err := trafficFailpoint("before-checkpoint"); err != nil {
				return err
			}
		}
		res.Applied = true
		return writeCheckpoint(root, cp)
	})
	if stderrors.Is(err, errStaleFlush) {
		return TrafficResult{}, nil
	}
	if err != nil {
		return TrafficResult{}, s.fail("append traffic", err)
	}
	return res, nil
}

// TrafficCheckpoint returns the stored flush cursor and counter baseline;
// ok is false before the first flush.
func (s *Store) TrafficCheckpoint() (TrafficCheckpoint, bool, error) {
	var (
		cp TrafficCheckpoint
		ok bool
	)
	err := s.db.View(func(tx *bolt.Tx) error {
		root, err := bucket(tx, BucketTraffic)
		if err != nil {
			return err
		}
		cp, ok, err = readCheckpoint(root)
		return err
	})
	if err != nil {
		return TrafficCheckpoint{}, false, s.fail("read traffic checkpoint", err)
	}
	return cp, ok, nil
}

// TunnelPoints returns the points of tunnel's series in tier whose start is
// in [from, to), oldest first; a zero to means no upper bound. An unknown
// series yields an empty slice.
func (s *Store) TunnelPoints(tunnel, tier string, from, to time.Time) ([]TunnelPoint, error) {
	out := []TunnelPoint{}
	err := s.readTier(TunnelSeriesName(tunnel), tier, from, to, func(start time.Time, v []byte) error {
		p, err := decodeTunnel(v)
		if err != nil {
			return err
		}
		p.Start = start
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, s.fail("read traffic", err)
	}
	return out, nil
}

// HostPoints returns the points of a host series in tier whose start is in
// [from, to), oldest first: node "" is the hub, otherwise the node's series.
func (s *Store) HostPoints(node, tier string, from, to time.Time) ([]HostPoint, error) {
	name := SeriesHub
	if node != "" {
		name = NodeSeriesName(node)
	}
	out := []HostPoint{}
	err := s.readTier(name, tier, from, to, func(start time.Time, v []byte) error {
		p, err := decodeHost(v)
		if err != nil {
			return err
		}
		p.Start = start
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, s.fail("read traffic", err)
	}
	return out, nil
}

// PeriodTotals returns the bytes of tunnel in the quota period that starts
// at periodStart (zero when nothing is stored).
func (s *Store) PeriodTotals(tunnel string, periodStart time.Time) (in, out uint64, err error) {
	pts, err := s.TunnelPoints(tunnel, TierPeriod, periodStart, periodStart.Add(time.Second))
	if err != nil {
		return 0, 0, err
	}
	for _, p := range pts {
		in += p.In
		out += p.Out
	}
	return in, out, nil
}

// TrafficSeries lists the stored series names, sorted.
func (s *Store) TrafficSeries() ([]string, error) {
	out := []string{}
	err := s.db.View(func(tx *bolt.Tx) error {
		root, err := bucket(tx, BucketTraffic)
		if err != nil {
			return err
		}
		return root.ForEach(func(k, v []byte) error {
			if v == nil {
				out = append(out, string(k))
			}
			return nil
		})
	})
	if err != nil {
		return nil, s.fail("list traffic", err)
	}
	return out, nil
}

// DeleteTrafficSeries removes one series (no error when absent) and, for a
// tunnel series, its baseline entry, in one transaction.
func (s *Store) DeleteTrafficSeries(series string) error {
	if series == "" {
		return s.fail("delete traffic", errEmptyKey)
	}
	err := s.db.Update(func(tx *bolt.Tx) error { return deleteTraffic(tx, series) })
	if err != nil {
		return s.fail("delete traffic", err)
	}
	return nil
}

// RetainTrafficSeries deletes, in one transaction, every tunnel and node
// series (and baseline entry) whose id is not listed: the hub calls it at
// start with the tunnels and nodes of config.yaml, so ids removed while the
// hub was down do not keep their history or count toward the caps. The hub
// series is kept. It returns the deleted series names.
func (s *Store) RetainTrafficSeries(tunnels, nodes []string) ([]string, error) {
	keep := map[string]bool{SeriesHub: true}
	for _, id := range tunnels {
		keep[TunnelSeriesName(id)] = true
	}
	for _, id := range nodes {
		keep[NodeSeriesName(id)] = true
	}
	var removed []string
	err := s.db.Update(func(tx *bolt.Tx) error {
		removed = nil
		root, err := bucket(tx, BucketTraffic)
		if err != nil {
			return err
		}
		var victims []string
		if err := root.ForEach(func(k, v []byte) error {
			if v == nil && !keep[string(k)] {
				victims = append(victims, string(k))
			}
			return nil
		}); err != nil {
			return err
		}
		cp, found, err := readCheckpoint(root)
		if err != nil {
			return err
		}
		for id := range cp.Baseline {
			if !keep[TunnelSeriesName(id)] {
				delete(cp.Baseline, id)
			}
		}
		for _, name := range victims {
			if err := root.DeleteBucket([]byte(name)); err != nil {
				return err
			}
		}
		removed = victims
		if found {
			return writeCheckpoint(root, cp)
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("retain traffic", err)
	}
	return removed, nil
}

// LiveSize returns the bytes of state.db in use by data: the high-water
// mark of the file minus its free and pending pages. Unlike Size it goes
// down again when history is pruned or deleted (bbolt never shrinks the
// file itself).
func (s *Store) LiveSize() (int64, error) {
	var n int64
	err := s.db.View(func(tx *bolt.Tx) error {
		n = tx.Size()
		return nil
	})
	if err != nil {
		return 0, s.fail("size", err)
	}
	n -= int64(s.db.Stats().FreeAlloc)
	return max(n, 0), nil
}

// SizeGuard reports whether the traffic history must shrink: it turns on
// when the live size (LiveSize) is above 80 % of budget and, once on
// (wasOver), stays on until the live size is below 60 %, so it cannot flap
// at every flush. While it is on, callers keep ReducedM1Keep 1-minute points
// (RetentionFor) and emit DEY-X063 once.
func (s *Store) SizeGuard(budget int64, wasOver bool) (over bool, live int64, err error) {
	live, err = s.LiveSize()
	if err != nil {
		return wasOver, 0, err
	}
	if wasOver {
		return live >= budget/5*3, live, nil
	}
	return live > budget/5*4, live, nil
}

// RetentionFor returns the retention to use while the size guard is on or
// off.
func RetentionFor(over bool) Retention {
	r := Retention{}.withDefaults()
	if over {
		r.M1 = ReducedM1Keep
	}
	return r
}

// ---------------------------------------------------------------- writer

// trafficWriter folds points into series buckets inside one transaction.
type trafficWriter struct {
	root     *bolt.Bucket
	ret      Retention
	now      time.Time
	limit    int64 // unix seconds; keys and points after it are future
	periodOf func(time.Time) time.Time
	res      *TrafficResult
}

// fillPercent: keys are mostly appended in order, so pages are filled
// further than bbolt's default before a split.
const fillPercent = 0.9

// tiers returns the tier buckets of series name, creating them: m1 and m30,
// plus period when withPeriod (tunnel series; host series have no quota).
func (w trafficWriter) tiers(name string, withPeriod bool) (m1, m30, period *bolt.Bucket, err error) {
	sb, err := w.root.CreateBucketIfNotExists([]byte(name))
	if err != nil {
		return nil, nil, nil, err
	}
	sb.FillPercent = fillPercent
	names := []string{TierM1, TierM30}
	if withPeriod {
		names = append(names, TierPeriod)
	}
	out := make([]*bolt.Bucket, 3)
	for i, t := range names {
		b, err := sb.CreateBucketIfNotExists([]byte(t))
		if err != nil {
			return nil, nil, nil, err
		}
		b.FillPercent = fillPercent
		out[i] = b
	}
	return out[0], out[1], out[2], nil
}

// keys returns the m1, m30 and period keys of a point start, or ok=false
// for a start that cannot be stored (zero, before 1970, after 2106, or
// future).
func (w trafficWriter) keys(start time.Time) (k1, k30, kp uint32, ok bool) {
	if start.IsZero() {
		return 0, 0, 0, false
	}
	u := start.Unix()
	if u < 0 || u > math.MaxUint32 || u > w.limit {
		return 0, 0, 0, false
	}
	p := w.periodOf(start).Unix()
	if p < 0 || p > u {
		p = u
	}
	return uint32(u - u%60), uint32(u - u%1800), uint32(p), true // #nosec G115 -- range checked above
}

func (w trafficWriter) tunnel(name string, pts []TunnelPoint) error {
	m1, m30, per, err := w.tiers(name, true)
	if err != nil {
		return err
	}
	for _, p := range pts {
		k1, k30, kp, ok := w.keys(p.Start)
		if !ok {
			w.res.Skipped++
			continue
		}
		p.Secs = clampSecs(p.Secs, StepM1)
		for _, t := range []struct {
			b    *bolt.Bucket
			k    uint32
			step time.Duration
		}{{m1, k1, StepM1}, {m30, k30, StepM30}, {per, kp, 0}} {
			if err := mergeTunnel(t.b, t.k, p, t.step); err != nil {
				return err
			}
		}
	}
	return w.prune(m1, m30, per)
}

func (w trafficWriter) host(name string, pts []HostPoint) error {
	m1, m30, _, err := w.tiers(name, false)
	if err != nil {
		return err
	}
	for _, p := range pts {
		k1, k30, _, ok := w.keys(p.Start)
		if !ok {
			w.res.Skipped++
			continue
		}
		p.Secs = clampSecs(p.Secs, StepM1)
		p.CPUMaxPermille = max(p.CPUMaxPermille, p.CPUPermille)
		if err := mergeHost(m1, k1, p, StepM1); err != nil {
			return err
		}
		if err := mergeHost(m30, k30, p, StepM30); err != nil {
			return err
		}
	}
	return w.prune(m1, m30, nil)
}

func (w trafficWriter) prune(m1, m30, per *bolt.Bucket) error {
	for _, t := range []struct {
		b    *bolt.Bucket
		keep int
	}{{m1, w.ret.M1}, {m30, w.ret.M30}, {per, w.ret.Periods}} {
		if t.b == nil {
			continue
		}
		if err := keepNewest(t.b, t.keep); err != nil {
			return err
		}
	}
	return nil
}

// deleteFuture deletes, in every series and tier, the keys after limit:
// they were stamped by a clock that was ahead and has been corrected. The
// bytes of a tunnel's deleted keys are not lost: each tier folds them into
// its point that contains now, flagged FlagGap and without covered seconds
// (they count toward the totals, never toward a rate).
func (w trafficWriter) deleteFuture() error {
	var series [][]byte
	if err := w.root.ForEach(func(k, v []byte) error {
		if v == nil {
			series = append(series, append([]byte(nil), k...))
		}
		return nil
	}); err != nil {
		return err
	}
	n := w.now.Unix()
	now := map[string]uint32{
		TierM1:     uint32(max(n-n%60, 0)),                   // #nosec G115 -- now is not after 2106
		TierM30:    uint32(max(n-n%1800, 0)),                 // #nosec G115 -- as above
		TierPeriod: uint32(max(w.periodOf(w.now).Unix(), 0)), // #nosec G115 -- as above
	}
	for _, name := range series {
		sb := w.root.Bucket(name)
		tunnel := strings.HasPrefix(string(name), SeriesTunnelPrefix)
		for _, tier := range []string{TierM1, TierM30, TierPeriod} {
			b := sb.Bucket([]byte(tier))
			if b == nil {
				continue
			}
			var (
				victims [][]byte
				moved   = TunnelPoint{Flags: FlagGap}
			)
			c := b.Cursor()
			for k, v := c.Last(); k != nil; k, v = c.Prev() {
				if v == nil || len(k) != 4 || int64(binary.BigEndian.Uint32(k)) <= w.limit {
					break
				}
				victims = append(victims, append([]byte(nil), k...))
				if tunnel {
					p, err := decodeTunnel(v)
					if err != nil {
						return err
					}
					moved.In = satAdd(moved.In, p.In)
					moved.Out = satAdd(moved.Out, p.Out)
					moved.Conns = max(moved.Conns, p.Conns)
					moved.Flags |= p.Flags
				}
			}
			if len(victims) == 0 {
				continue
			}
			if err := deletePoints(b, victims); err != nil {
				return err
			}
			w.res.FutureDeleted += len(victims)
			if tunnel && int64(now[tier]) <= w.limit {
				if err := mergeTunnel(b, now[tier], moved, 0); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// A tier bucket's sequence is its number of keys: putPoint and deletePoints
// keep it, so pruning needs no walk over the whole tier (Bucket.Stats does
// not see the keys added in the running transaction).

// putPoint stores v under k and counts a new key.
func putPoint(b *bolt.Bucket, k, v []byte) error {
	if b.Get(k) == nil {
		if err := b.SetSequence(b.Sequence() + 1); err != nil {
			return err
		}
	}
	return b.Put(k, v)
}

// deletePoints deletes keys of b (all present) and uncounts them.
func deletePoints(b *bolt.Bucket, keys [][]byte) error {
	for _, k := range keys {
		if err := b.Delete(k); err != nil {
			return err
		}
	}
	return b.SetSequence(b.Sequence() - min(b.Sequence(), uint64(len(keys))))
}

// keepNewest deletes the oldest keys of b beyond the newest keep.
func keepNewest(b *bolt.Bucket, keep int) error {
	n := b.Sequence()
	if keep < 1 || n <= uint64(keep) {
		return nil
	}
	drop := n - uint64(keep)
	victims := make([][]byte, 0, min(drop, 4096))
	c := b.Cursor()
	for k, _ := c.First(); k != nil && uint64(len(victims)) < drop; k, _ = c.Next() {
		victims = append(victims, append([]byte(nil), k...))
	}
	return deletePoints(b, victims)
}

// clampSecs returns secs, or the whole step for 0 and anything longer.
func clampSecs(secs uint16, step time.Duration) uint16 {
	full := uint16(step / time.Second) // #nosec G115 -- steps are at most 30 min
	if secs == 0 || secs > full {
		return full
	}
	return secs
}

// addSecs adds covered seconds, capped at step (0 = no cap: the period
// tier, capped only by the type).
func addSecs(a, b uint16, step time.Duration) uint16 {
	sum := uint32(a) + uint32(b)
	limit := uint32(math.MaxUint16)
	if step > 0 {
		limit = uint32(step / time.Second) // #nosec G115 -- steps are at most 30 min
	}
	return uint16(min(sum, limit)) // #nosec G115 -- capped above
}

func mergeTunnel(b *bolt.Bucket, key uint32, p TunnelPoint, step time.Duration) error {
	k := u32Key(key)
	if old := b.Get(k); old != nil {
		o, err := decodeTunnel(old)
		if err != nil {
			return err
		}
		p.In = satAdd(o.In, p.In)
		p.Out = satAdd(o.Out, p.Out)
		p.Conns = max(o.Conns, p.Conns)
		p.Secs = addSecs(o.Secs, p.Secs, step)
		p.Flags |= o.Flags
	}
	return putPoint(b, k, encodeTunnel(p))
}

func mergeHost(b *bolt.Bucket, key uint32, p HostPoint, step time.Duration) error {
	k := u32Key(key)
	if old := b.Get(k); old != nil {
		o, err := decodeHost(old)
		if err != nil {
			return err
		}
		wo, wp := uint64(max(o.Secs, 1)), uint64(max(p.Secs, 1))
		p.CPUPermille = uint16((uint64(o.CPUPermille)*wo + uint64(p.CPUPermille)*wp) / (wo + wp)) // #nosec G115 -- a weighted mean of two uint16
		p.RAM = weightedMean(o.RAM, wo, p.RAM, wp)
		p.CPUMaxPermille = max(o.CPUMaxPermille, p.CPUMaxPermille)
		p.Secs = addSecs(o.Secs, p.Secs, step)
		p.Flags |= o.Flags
	}
	return putPoint(b, k, encodeHost(p))
}

// weightedMean returns (a*wa + b*wb) / (wa+wb) without overflowing for
// byte counts of any realistic size.
func weightedMean(a, wa, b, wb uint64) uint64 {
	fa, fb := float64(wa), float64(wb)
	m := (float64(a)*fa + float64(b)*fb) / (fa + fb)
	if m >= math.MaxUint64 || math.IsNaN(m) {
		return max(a, b)
	}
	return uint64(m)
}

func satAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

// ---------------------------------------------------------------- encoding

func u32Key(v uint32) []byte {
	var k [4]byte
	binary.BigEndian.PutUint32(k[:], v)
	return k[:]
}

func encodeTunnel(p TunnelPoint) []byte {
	v := make([]byte, TunnelRecordLen)
	binary.BigEndian.PutUint64(v[0:], p.In)
	binary.BigEndian.PutUint64(v[8:], p.Out)
	binary.BigEndian.PutUint32(v[16:], p.Conns)
	binary.BigEndian.PutUint16(v[20:], p.Secs)
	v[22] = p.Flags
	return v
}

func decodeTunnel(v []byte) (TunnelPoint, error) {
	if len(v) < TunnelRecordLen {
		return TunnelPoint{}, fmt.Errorf("%w: %d bytes", errBadRecord, len(v))
	}
	return TunnelPoint{
		In:    binary.BigEndian.Uint64(v[0:]),
		Out:   binary.BigEndian.Uint64(v[8:]),
		Conns: binary.BigEndian.Uint32(v[16:]),
		Secs:  binary.BigEndian.Uint16(v[20:]),
		Flags: v[22],
	}, nil
}

func encodeHost(p HostPoint) []byte {
	v := make([]byte, HostRecordLen)
	binary.BigEndian.PutUint16(v[0:], p.CPUPermille)
	binary.BigEndian.PutUint16(v[2:], p.CPUMaxPermille)
	binary.BigEndian.PutUint64(v[4:], p.RAM)
	binary.BigEndian.PutUint16(v[12:], p.Secs)
	v[14] = p.Flags
	return v
}

func decodeHost(v []byte) (HostPoint, error) {
	if len(v) < HostRecordLen {
		return HostPoint{}, fmt.Errorf("%w: %d bytes", errBadRecord, len(v))
	}
	return HostPoint{
		CPUPermille:    binary.BigEndian.Uint16(v[0:]),
		CPUMaxPermille: binary.BigEndian.Uint16(v[2:]),
		RAM:            binary.BigEndian.Uint64(v[4:]),
		Secs:           binary.BigEndian.Uint16(v[12:]),
		Flags:          v[14],
	}, nil
}

// ---------------------------------------------------------------- helpers

// readTier calls fn for every key of traffic/<series>/<tier> in [from, to).
func (s *Store) readTier(series, tier string, from, to time.Time, fn func(time.Time, []byte) error) error {
	if tier != TierM1 && tier != TierM30 && tier != TierPeriod {
		return fmt.Errorf("%w: %q", errUnknownTier, tier)
	}
	lo := clampUnix(from)
	hi := int64(math.MaxUint32) + 1
	if !to.IsZero() {
		hi = min(max(to.Unix(), 0), hi)
	}
	return s.db.View(func(tx *bolt.Tx) error {
		root, err := bucket(tx, BucketTraffic)
		if err != nil {
			return err
		}
		sb := root.Bucket([]byte(series))
		if sb == nil {
			return nil
		}
		b := sb.Bucket([]byte(tier))
		if b == nil {
			return nil
		}
		c := b.Cursor()
		for k, v := c.Seek(u32Key(lo)); k != nil; k, v = c.Next() {
			if v == nil || len(k) != 4 {
				continue
			}
			at := int64(binary.BigEndian.Uint32(k))
			if at >= hi {
				break
			}
			if err := fn(time.Unix(at, 0).UTC(), v); err != nil {
				return fmt.Errorf("traffic/%s/%s: %w", series, tier, err)
			}
		}
		return nil
	})
}

// clampUnix returns t's unix seconds within [0, MaxUint32] (zero time = 0).
func clampUnix(t time.Time) uint32 {
	if t.IsZero() {
		return 0
	}
	return uint32(min(max(t.Unix(), 0), math.MaxUint32)) // #nosec G115 -- clamped
}

func readCheckpoint(root *bolt.Bucket) (TrafficCheckpoint, bool, error) {
	var cp TrafficCheckpoint
	data := root.Get([]byte(trafficCheckpointKey))
	if data == nil {
		return cp, false, nil
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return TrafficCheckpoint{}, false, fmt.Errorf("traffic/%s: %w", trafficCheckpointKey, err)
	}
	return cp, true, nil
}

func writeCheckpoint(root *bolt.Bucket, cp TrafficCheckpoint) error {
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	return root.Put([]byte(trafficCheckpointKey), data)
}

// countSeries counts the stored tunnel and node series.
func countSeries(root *bolt.Bucket) (tunnels, nodes int) {
	_ = root.ForEach(func(k, v []byte) error {
		if v != nil {
			return nil
		}
		switch {
		case strings.HasPrefix(string(k), SeriesTunnelPrefix):
			tunnels++
		case strings.HasPrefix(string(k), SeriesNodePrefix):
			nodes++
		}
		return nil
	})
	return tunnels, nodes
}

// deleteTraffic removes series (and its baseline entry for a tunnel)
// inside tx.
func deleteTraffic(tx *bolt.Tx, series string) error {
	root, err := bucket(tx, BucketTraffic)
	if err != nil {
		return err
	}
	if root.Bucket([]byte(series)) != nil {
		if err := root.DeleteBucket([]byte(series)); err != nil {
			return err
		}
	}
	id, isTunnel := strings.CutPrefix(series, SeriesTunnelPrefix)
	if !isTunnel {
		return nil
	}
	cp, found, err := readCheckpoint(root)
	if err != nil || !found {
		return err
	}
	if _, ok := cp.Baseline[id]; !ok {
		return nil
	}
	delete(cp.Baseline, id)
	return writeCheckpoint(root, cp)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
