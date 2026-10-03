package state

import (
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// irst is the hub-local zone of the tests (+03:30, no DST): a fixed zone,
// so the tests do not depend on the tzdata of the machine.
var irst = time.FixedZone("IRST", 3*3600+1800)

// trafficStore opens a store without fsync (thousands of flushes) and
// creates the records of the given tunnels and nodes.
func trafficStore(t *testing.T, tunnels, nodes []string) (*Store, string) {
	t.Helper()
	s, path := newStore(t)
	s.db.NoSync = true
	for _, id := range tunnels {
		require.NoError(t, s.PutTunnel(TunnelState{ID: id, State: StateUp}))
	}
	for _, id := range nodes {
		require.NoError(t, s.PutNode(NodeState{ID: id}))
	}
	return s, path
}

// flusher numbers the batches like the hub's sampler does.
type flusher struct {
	t   *testing.T
	s   *Store
	seq uint64
	ret Retention
	// period is the hub-local quota period (reset day 15 in irst).
	period func(time.Time) time.Time
}

func newFlusher(t *testing.T, s *Store) *flusher {
	return &flusher{t: t, s: s, period: func(at time.Time) time.Time { return PeriodStart(at, 15, irst) }}
}

func (f *flusher) flush(now time.Time, b TrafficBatch) TrafficResult {
	f.t.Helper()
	f.seq++
	b.Cursor = TrafficCursor{Seq: f.seq, At: now}
	b.Now = now
	b.PeriodStart = f.period
	res, err := f.s.AppendTraffic(b, f.ret)
	require.NoError(f.t, err)
	require.True(f.t, res.Applied)
	return res
}

func tp(at time.Time, in, out uint64, conns uint32) TunnelPoint {
	return TunnelPoint{Start: at, In: in, Out: out, Conns: conns}
}

func sumPoints(pts []TunnelPoint) (in, out uint64) {
	for _, p := range pts {
		in += p.In
		out += p.Out
	}
	return in, out
}

func txID(t *testing.T, s *Store) int {
	t.Helper()
	var id int
	require.NoError(t, s.db.View(func(tx *bolt.Tx) error { id = tx.ID(); return nil }))
	return id
}

func TestTrafficRecordLayout(t *testing.T) {
	p := TunnelPoint{In: 1<<40 + 5, Out: 7, Conns: 300, Secs: 42, Flags: FlagGap | FlagConnsUnknown}
	v := encodeTunnel(p)
	require.Len(t, v, TunnelRecordLen)
	require.Equal(t, 23, TunnelRecordLen)
	got, err := decodeTunnel(v)
	require.NoError(t, err)
	require.Equal(t, p, got)
	// A longer record from a newer release still decodes.
	got, err = decodeTunnel(append(v, 9, 9))
	require.NoError(t, err)
	require.Equal(t, p, got)
	_, err = decodeTunnel(v[:20])
	require.ErrorIs(t, err, errBadRecord)

	h := HostPoint{CPUPermille: 250, CPUMaxPermille: 900, RAM: 3 << 30, Secs: 60, Flags: FlagPartial}
	hv := encodeHost(h)
	require.Len(t, hv, HostRecordLen)
	hgot, err := decodeHost(hv)
	require.NoError(t, err)
	require.Equal(t, h, hgot)
	_, err = decodeHost(hv[:3])
	require.ErrorIs(t, err, errBadRecord)

	require.Equal(t, "t:main", TunnelSeriesName("main"))
	require.Equal(t, "n:de-1", NodeSeriesName("de-1"))
}

func TestTrafficValuesAreExactSize(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, []string{"de-1"})
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	f.flush(at, TrafficBatch{
		Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 2, 3)}},
		Nodes:   map[string][]HostPoint{"de-1": {{Start: at, CPUPermille: 10, RAM: 5}}},
		Hub:     []HostPoint{{Start: at, CPUPermille: 20, RAM: 6}},
	})
	require.NoError(t, s.db.View(func(tx *bolt.Tx) error {
		root := tx.Bucket([]byte(BucketTraffic))
		for series, size := range map[string]int{"t:main": TunnelRecordLen, "n:de-1": HostRecordLen, "hub": HostRecordLen} {
			sb := root.Bucket([]byte(series))
			require.NotNil(t, sb, series)
			require.NoError(t, sb.ForEach(func(tier, v []byte) error {
				require.Nil(t, v, "only tier buckets below a series")
				return sb.Bucket(tier).ForEach(func(k, v []byte) error {
					require.Len(t, k, 4)
					require.Len(t, v, size, "%s/%s", series, tier)
					return nil
				})
			}))
		}
		// Host series have no quota period tier.
		require.Nil(t, root.Bucket([]byte("hub")).Bucket([]byte(TierPeriod)))
		return nil
	}))
}

// m1 merges points of the same minute, m30 sums the minutes of its 30
// minutes with epoch-aligned boundaries (:29 and :59 belong to the bucket
// before :30 and :00), and the period tier uses the hub-local quota period.
func TestTrafficTiersAndBoundaries(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, nil)
	f := newFlusher(t, s)
	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	at := func(m, sec int) time.Time {
		return base.Add(time.Duration(m)*time.Minute + time.Duration(sec)*time.Second)
	}

	f.flush(at(31, 0), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {
		tp(at(29, 0), 1, 10, 2),
		tp(at(29, 59), 2, 20, 5), // same minute: merged
		tp(at(30, 0), 4, 40, 1),
		tp(at(59, 30), 8, 80, 1), // flushed early: still the 10:30 bucket
	}}})

	m1, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, 3)
	require.True(t, m1[0].Start.Equal(at(29, 0)))
	require.Equal(t, uint64(3), m1[0].In)
	require.Equal(t, uint64(30), m1[0].Out)
	require.Equal(t, uint32(5), m1[0].Conns, "the highest connection count")
	require.Equal(t, uint16(60), m1[0].Secs, "covered seconds are capped at the step")
	require.True(t, m1[2].Start.Equal(at(59, 0)), "start truncated to the minute")
	require.Equal(t, time.UTC, m1[0].Start.Location())

	m30, err := s.TunnelPoints("main", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m30, 2)
	require.True(t, m30[0].Start.Equal(base))
	require.Equal(t, uint64(3), m30[0].In, ":29 belongs to 10:00")
	require.True(t, m30[1].Start.Equal(at(30, 0)))
	require.Equal(t, uint64(12), m30[1].In, ":30 and :59 belong to 10:30")
	require.Equal(t, uint16(120), m30[1].Secs)

	// 30-minute buckets stay aligned for +03:30: 10:30 UTC is 14:00 IRST.
	require.Equal(t, 0, m30[1].Start.In(irst).Minute())

	// The period starts at 00:00 IRST on the 15th: 2026-09-14 20:30 UTC.
	ps := PeriodStart(base, 15, irst)
	require.True(t, ps.Equal(time.Date(2026, 9, 14, 20, 30, 0, 0, time.UTC)), ps.String())
	in, out, err := s.PeriodTotals("main", ps)
	require.NoError(t, err)
	require.Equal(t, uint64(15), in)
	require.Equal(t, uint64(150), out)
	in, _, err = s.PeriodTotals("main", ps.AddDate(0, -1, 0))
	require.NoError(t, err)
	require.Zero(t, in)

	// Ranges are [from, to).
	part, err := s.TunnelPoints("main", TierM1, at(29, 0), at(59, 0))
	require.NoError(t, err)
	require.Len(t, part, 2)
	part, err = s.TunnelPoints("main", TierM1, at(30, 0), time.Time{})
	require.NoError(t, err)
	require.Len(t, part, 2)

	_, err = s.TunnelPoints("main", "m5", time.Time{}, time.Time{})
	require.True(t, deyerr.HasCode(err, deyerr.X021))
	none, err := s.TunnelPoints("other", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestPeriodStart(t *testing.T) {
	for _, tc := range []struct {
		at   time.Time
		day  int
		want time.Time
	}{
		{time.Date(2026, 3, 20, 12, 0, 0, 0, irst), 15, time.Date(2026, 3, 15, 0, 0, 0, 0, irst)},
		{time.Date(2026, 3, 10, 12, 0, 0, 0, irst), 15, time.Date(2026, 2, 15, 0, 0, 0, 0, irst)},
		{time.Date(2026, 1, 3, 0, 0, 0, 0, irst), 15, time.Date(2025, 12, 15, 0, 0, 0, 0, irst)},
		{time.Date(2026, 3, 15, 0, 0, 0, 0, irst), 15, time.Date(2026, 3, 15, 0, 0, 0, 0, irst)},
		{time.Date(2026, 3, 31, 23, 0, 0, 0, irst), 1, time.Date(2026, 3, 1, 0, 0, 0, 0, irst)},
		{time.Date(2026, 3, 31, 23, 0, 0, 0, irst), 0, time.Date(2026, 3, 1, 0, 0, 0, 0, irst)},   // clamped to 1
		{time.Date(2026, 3, 31, 23, 0, 0, 0, irst), 31, time.Date(2026, 3, 28, 0, 0, 0, 0, irst)}, // clamped to 28
		// 2026-03-31 22:00 UTC is already April 1st in IRST.
		{time.Date(2026, 3, 31, 22, 0, 0, 0, time.UTC), 1, time.Date(2026, 4, 1, 0, 0, 0, 0, irst)},
	} {
		got := PeriodStart(tc.at, tc.day, irst)
		require.True(t, got.Equal(tc.want), "%s day %d: %s", tc.at, tc.day, got)
	}
	require.True(t, PeriodStart(time.Date(2026, 5, 9, 1, 0, 0, 0, time.UTC), 1, nil).
		Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))
}

// 26 hours of one flush per minute: m1 keeps the newest 1440 minutes, m30
// keeps everything, and the totals of m30 equal what was written.
func TestTrafficPruneM1Over26Hours(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, nil)
	f := newFlusher(t, s)
	start := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	const minutes = 26 * 60
	for i := 0; i < minutes; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		f.flush(at.Add(StepM1), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 100, 1000, 1)}}})
	}
	m1, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, DefaultM1Keep)
	require.True(t, m1[0].Start.Equal(start.Add(2*time.Hour)), "the oldest 2 h are pruned")
	require.True(t, m1[len(m1)-1].Start.Equal(start.Add((minutes-1)*time.Minute)))

	m30, err := s.TunnelPoints("main", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m30, minutes/30)
	in, out := sumPoints(m30)
	require.Equal(t, uint64(minutes*100), in)
	require.Equal(t, uint64(minutes*1000), out)
}

// 40 days of half-hour batches: m30 keeps its newest 1536 points (32 days);
// a reduced retention (the size guard) prunes m1 to 6 h.
func TestTrafficPruneM30AndPeriods(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, nil)
	f := newFlusher(t, s)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const halfHours = 40 * 48
	for i := 0; i < halfHours; i++ {
		at := start.Add(time.Duration(i) * StepM30)
		f.flush(at.Add(StepM30), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 2, 0)}}})
	}
	m30, err := s.TunnelPoints("main", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m30, DefaultM30Keep)
	require.True(t, m30[0].Start.Equal(start.Add((halfHours-DefaultM30Keep)*StepM30)))

	// 15 monthly periods: only the newest 13 are kept.
	f.ret = Retention{M1: ReducedM1Keep}
	for m := 0; m < 15; m++ {
		at := time.Date(2026, 3+time.Month(m), 20, 0, 0, 0, 0, time.UTC)
		f.flush(at, TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 1, 0)}}})
	}
	per, err := s.TunnelPoints("main", TierPeriod, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, per, DefaultPeriodKeep)
	require.True(t, per[len(per)-1].Start.Equal(PeriodStart(time.Date(2027, 5, 20, 0, 0, 0, 0, time.UTC), 15, irst)))

	// The reduced m1 retention prunes on the next flush of the series.
	for i := 0; i < ReducedM1Keep+10; i++ {
		at := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
		f.flush(at.Add(StepM1), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 1, 0)}}})
	}
	m1, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, ReducedM1Keep)
}

func TestTrafficOneTransactionPerFlush(t *testing.T) {
	s, _ := trafficStore(t, []string{"a1", "b2"}, []string{"de-1"})
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	before := txID(t, s)
	f.flush(at, TrafficBatch{
		Tunnels:  map[string][]TunnelPoint{"a1": {tp(at, 1, 1, 1)}, "b2": {tp(at, 2, 2, 2), tp(at.Add(time.Minute), 3, 3, 3)}},
		Nodes:    map[string][]HostPoint{"de-1": {{Start: at, CPUPermille: 1}}},
		Hub:      []HostPoint{{Start: at, CPUPermille: 2}},
		Baseline: map[string]TrafficCounter{"a1": {In: 10, Out: 20}, "b2": {In: 30, Out: 40}},
	})
	require.Equal(t, before+1, txID(t, s), "points, baseline and cursor in one write transaction")

	// A stale flush does not even commit an empty transaction.
	res, err := s.AppendTraffic(TrafficBatch{Cursor: TrafficCursor{Seq: 1}}, Retention{})
	require.NoError(t, err)
	require.False(t, res.Applied)
	require.Equal(t, before+1, txID(t, s))
}

// The cursor and the baseline are stored with the points; a batch that was
// already folded (same or older Seq) changes nothing.
func TestTrafficFlushIsIdempotent(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, nil)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	_, ok, err := s.TrafficCheckpoint()
	require.NoError(t, err)
	require.False(t, ok)

	b := TrafficBatch{
		Cursor:   TrafficCursor{Seq: 7, At: at.In(irst)},
		Now:      at,
		Tunnels:  map[string][]TunnelPoint{"main": {tp(at, 100, 200, 1)}},
		Baseline: map[string]TrafficCounter{"main": {In: 1000, Out: 2000, Since: at.Add(-time.Hour)}, "gone": {In: 1}},
	}
	res, err := s.AppendTraffic(b, Retention{})
	require.NoError(t, err)
	require.True(t, res.Applied)
	for _, seq := range []uint64{7, 3} {
		b.Cursor.Seq = seq
		res, err = s.AppendTraffic(b, Retention{})
		require.NoError(t, err)
		require.False(t, res.Applied, "seq %d", seq)
	}
	m1, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, 1)
	require.Equal(t, uint64(100), m1[0].In, "folded once")

	cp, ok, err := s.TrafficCheckpoint()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(7), cp.Cursor.Seq)
	require.Equal(t, time.UTC, cp.Cursor.At.Location())
	require.Equal(t, map[string]TrafficCounter{"main": {In: 1000, Out: 2000, Since: at.Add(-time.Hour)}}, cp.Baseline,
		"baseline entries of unknown tunnels are dropped")

	// A nil baseline keeps the stored one; the next Seq applies.
	res, err = s.AppendTraffic(TrafficBatch{Cursor: TrafficCursor{Seq: 8}, Now: at}, Retention{})
	require.NoError(t, err)
	require.True(t, res.Applied)
	cp, _, err = s.TrafficCheckpoint()
	require.NoError(t, err)
	require.Equal(t, uint64(8), cp.Cursor.Seq)
	require.Equal(t, uint64(1000), cp.Baseline["main"].In)
}

// A crash between the steps of a flush: the transaction is aborted after
// the points were written but before the cursor and the baseline. Nothing
// of it is visible after a reopen, so the restarted sampler takes its delta
// against the old baseline and folds the same bytes exactly once.
func TestTrafficCrashBetweenStepsLosesNothingAndCountsOnce(t *testing.T) {
	s, path := trafficStore(t, []string{"main"}, nil)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	first := TrafficBatch{
		Cursor: TrafficCursor{Seq: 1}, Now: at,
		Tunnels:  map[string][]TunnelPoint{"main": {tp(at, 100, 100, 1)}},
		Baseline: map[string]TrafficCounter{"main": {In: 100, Out: 100}},
	}
	_, err := s.AppendTraffic(first, Retention{})
	require.NoError(t, err)

	second := TrafficBatch{
		Cursor: TrafficCursor{Seq: 2}, Now: at.Add(time.Minute),
		Tunnels:  map[string][]TunnelPoint{"main": {tp(at.Add(time.Minute), 50, 70, 1)}},
		Baseline: map[string]TrafficCounter{"main": {In: 150, Out: 170}},
	}
	crash := stderrors.New("power cut")
	trafficFailpoint = func(stage string) error {
		if stage == "before-checkpoint" {
			return crash
		}
		return nil
	}
	_, err = s.AppendTraffic(second, Retention{})
	trafficFailpoint = nil
	require.ErrorIs(t, err, crash)
	require.True(t, deyerr.HasCode(err, deyerr.X021))

	// Restart.
	require.NoError(t, s.Close())
	s, err = openWith(path, withClock(fixedClock()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	cp, ok, err := s.TrafficCheckpoint()
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(1), cp.Cursor.Seq, "the aborted flush left no cursor")
	require.Equal(t, TrafficCounter{In: 100, Out: 100}, cp.Baseline["main"])
	m1, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, 1, "and no points")

	// The sampler re-reads the counters (150/170) against the stored
	// baseline and folds the minute again: once.
	for range 2 {
		_, err = s.AppendTraffic(second, Retention{})
		require.NoError(t, err)
	}
	m1, err = s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	in, out := sumPoints(m1)
	require.Equal(t, uint64(150), in)
	require.Equal(t, uint64(170), out)
	per, err := s.TunnelPoints("main", TierPeriod, time.Time{}, time.Time{})
	require.NoError(t, err)
	in, out = sumPoints(per)
	require.Equal(t, [2]uint64{150, 170}, [2]uint64{in, out}, "the period total equals the counters")

	// A crash right after the commit: the reopened store has the cursor,
	// so a replay is refused.
	require.NoError(t, s.Close())
	s2, err := openWith(path, withClock(fixedClock()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s2.Close() })
	res, err := s2.AppendTraffic(second, Retention{})
	require.NoError(t, err)
	require.False(t, res.Applied)
}

// The wall clock jumps forward one year and comes back: the history is not
// wiped (pruning is by count), and the mis-stamped future keys are deleted
// when the clock is right again, their bytes moved to now. A clock stepped
// back 2 h merges into the minutes it already has and moves the hour that
// now lies ahead; no byte is lost.
func TestTrafficClockJumps(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, nil)
	f := newFlusher(t, s)
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 120; i++ {
		at := start.Add(time.Duration(i) * time.Minute)
		f.flush(at.Add(time.Minute), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 10, 10, 1)}}})
	}

	// +1 year for 3 minutes.
	ahead := start.Add(120*time.Minute).AddDate(1, 0, 0)
	for i := 0; i < 3; i++ {
		at := ahead.Add(time.Duration(i) * time.Minute)
		f.flush(at.Add(time.Minute), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 1, 1)}}})
	}
	m1, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, 123, "nothing pruned by the jump")

	// Back to the right time: the future keys of every tier go.
	now := start.Add(124 * time.Minute)
	res := f.flush(now, TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(now.Add(-time.Minute), 10, 10, 1)}}})
	require.Equal(t, 3+1+1, res.FutureDeleted, "3 minutes, 1 half hour, 1 period")
	for _, tier := range []string{TierM1, TierM30, TierPeriod} {
		pts, err := s.TunnelPoints("main", tier, now.Add(FutureSlack), time.Time{})
		require.NoError(t, err)
		require.Empty(t, pts, tier)
	}
	m1, err = s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, m1, 122, "120 + the minute flushed now + the minute the 3 future bytes moved to")
	require.True(t, m1[121].Start.Equal(now))
	require.Equal(t, uint64(3), m1[121].In)
	m30, err := s.TunnelPoints("main", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	in, _ := sumPoints(m30)
	require.Equal(t, uint64(121*10+3), in, "the mis-stamped bytes moved to now")

	// A point stamped far ahead of the flush's own clock is skipped.
	res = f.flush(now, TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(now.Add(2*time.Hour), 5, 5, 1), {}}}})
	require.Equal(t, 2, res.Skipped)

	// −2 h: the minutes already stored are merged, not replaced.
	back := start.Add(time.Minute)
	f.flush(back.Add(time.Minute), TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(back, 7, 7, 9)}}})
	m1, err = s.TunnelPoints("main", TierM1, back, back.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, m1, 1)
	require.Equal(t, uint64(17), m1[0].In)
	require.Equal(t, uint32(9), m1[0].Conns)
	require.Equal(t, uint16(60), m1[0].Secs)
	m30, err = s.TunnelPoints("main", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	in, _ = sumPoints(m30)
	require.Equal(t, uint64(121*10+3+7), in, "no byte is lost")
	// The bytes of the minutes that now lie more than an hour ahead
	// (01:03-01:59, 02:03 and 02:04) moved into the current minute 00:02,
	// flagged as a gap; its own seconds are not extended.
	cur, err := s.TunnelPoints("main", TierM1, back.Add(time.Minute), back.Add(2*time.Minute))
	require.NoError(t, err)
	require.Len(t, cur, 1)
	require.Equal(t, uint64(10+57*10+10+3), cur[0].In)
	require.Equal(t, FlagGap, cur[0].Flags&FlagGap)
	require.Equal(t, uint16(60), cur[0].Secs)
	per, err := s.TunnelPoints("main", TierPeriod, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, per, 1)
	in, _ = sumPoints(per)
	require.Equal(t, uint64(121*10+3+7), in, "the quota total keeps every byte")
}

func TestTrafficHostSeries(t *testing.T) {
	s, _ := trafficStore(t, nil, []string{"de-1"})
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	f.flush(at.Add(time.Minute), TrafficBatch{
		Nodes: map[string][]HostPoint{"de-1": {
			{Start: at, CPUPermille: 100, RAM: 1000, Secs: 30},
			{Start: at.Add(30 * time.Second), CPUPermille: 400, CPUMaxPermille: 700, RAM: 4000, Secs: 10},
		}},
		Hub: []HostPoint{{Start: at, CPUPermille: 50, RAM: 1 << 30}},
	})
	pts, err := s.HostPoints("de-1", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, pts, 1)
	require.Equal(t, uint16(175), pts[0].CPUPermille, "weighted by the covered seconds")
	require.Equal(t, uint16(700), pts[0].CPUMaxPermille)
	require.Equal(t, uint64(1750), pts[0].RAM)
	require.Equal(t, uint16(40), pts[0].Secs)

	hub, err := s.HostPoints("", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, hub, 1)
	require.Equal(t, uint16(50), hub[0].CPUMaxPermille, "the peak is at least the mean")
	require.Equal(t, uint64(1<<30), hub[0].RAM)
	_, err = s.HostPoints("", TierPeriod, time.Time{}, time.Time{})
	require.NoError(t, err)
}

func TestDeleteTunnelAndNodeRemoveTheirSeries(t *testing.T) {
	s, _ := trafficStore(t, []string{"main", "other"}, []string{"de-1", "de-2"})
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.AppendProbe("main", "de-1", "a/b", ProbeSample{OK: true}))
	f.flush(at, TrafficBatch{
		Tunnels:  map[string][]TunnelPoint{"main": {tp(at, 1, 1, 1)}, "other": {tp(at, 2, 2, 2)}},
		Nodes:    map[string][]HostPoint{"de-1": {{Start: at}}, "de-2": {{Start: at}}},
		Hub:      []HostPoint{{Start: at}},
		Baseline: map[string]TrafficCounter{"main": {In: 1}, "other": {In: 2}},
	})

	before := txID(t, s)
	require.NoError(t, s.DeleteTunnel("main"))
	require.Equal(t, before+1, txID(t, s), "one transaction")
	require.NoError(t, s.DeleteNode("de-1"))
	series, err := s.TrafficSeries()
	require.NoError(t, err)
	require.Equal(t, []string{"hub", "n:de-2", "t:other"}, series)
	keys, err := s.ProbeKeys()
	require.NoError(t, err)
	require.Empty(t, keys)
	cp, _, err := s.TrafficCheckpoint()
	require.NoError(t, err)
	require.Equal(t, map[string]TrafficCounter{"other": {In: 2}}, cp.Baseline)
	other, err := s.TunnelPoints("other", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, other, 1)

	// A flush prepared before the delete does not bring the series back.
	res := f.flush(at.Add(time.Minute), TrafficBatch{
		Tunnels:  map[string][]TunnelPoint{"main": {tp(at, 5, 5, 5)}, "other": {tp(at, 1, 1, 1)}},
		Nodes:    map[string][]HostPoint{"de-1": {{Start: at}}},
		Baseline: map[string]TrafficCounter{"main": {In: 9}, "other": {In: 3}},
	})
	require.Equal(t, []string{"t:main", "n:de-1"}, res.Unknown)
	series, err = s.TrafficSeries()
	require.NoError(t, err)
	require.Equal(t, []string{"hub", "n:de-2", "t:other"}, series)
	cp, _, err = s.TrafficCheckpoint()
	require.NoError(t, err)
	require.Equal(t, map[string]TrafficCounter{"other": {In: 3}}, cp.Baseline)

	require.NoError(t, s.DeleteTrafficSeries("t:other"))
	require.NoError(t, s.DeleteTrafficSeries("t:other"))
	require.Error(t, s.DeleteTrafficSeries(""))
	cp, _, err = s.TrafficCheckpoint()
	require.NoError(t, err)
	require.Empty(t, cp.Baseline)
}

func TestRetainTrafficSeries(t *testing.T) {
	s, _ := trafficStore(t, []string{"keep", "drop"}, []string{"de-1", "de-2"})
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	f.flush(at, TrafficBatch{
		Tunnels:  map[string][]TunnelPoint{"keep": {tp(at, 1, 1, 1)}, "drop": {tp(at, 1, 1, 1)}},
		Nodes:    map[string][]HostPoint{"de-1": {{Start: at}}, "de-2": {{Start: at}}},
		Hub:      []HostPoint{{Start: at}},
		Baseline: map[string]TrafficCounter{"keep": {In: 1}, "drop": {In: 1}},
	})
	removed, err := s.RetainTrafficSeries([]string{"keep"}, []string{"de-2"})
	require.NoError(t, err)
	require.Equal(t, []string{"n:de-1", "t:drop"}, removed)
	series, err := s.TrafficSeries()
	require.NoError(t, err)
	require.Equal(t, []string{"hub", "n:de-2", "t:keep"}, series)
	cp, _, err := s.TrafficCheckpoint()
	require.NoError(t, err)
	require.Equal(t, map[string]TrafficCounter{"keep": {In: 1}}, cp.Baseline)
}

func TestTrafficSeriesCap(t *testing.T) {
	var tunnels, nodes []string
	for i := 0; i <= MaxTrafficTunnels; i++ {
		tunnels = append(tunnels, fmt.Sprintf("t%02d", i))
	}
	for i := 0; i <= MaxTrafficNodes; i++ {
		nodes = append(nodes, fmt.Sprintf("n%02d", i))
	}
	s, _ := trafficStore(t, tunnels, nodes)
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	b := TrafficBatch{Tunnels: map[string][]TunnelPoint{}, Nodes: map[string][]HostPoint{}}
	for _, id := range tunnels[:MaxTrafficTunnels] {
		b.Tunnels[id] = []TunnelPoint{tp(at, 1, 1, 1)}
	}
	for _, id := range nodes[:MaxTrafficNodes] {
		b.Nodes[id] = []HostPoint{{Start: at}}
	}
	res := f.flush(at, b)
	require.Empty(t, res.Refused)

	// The 65th of each kind is refused; the existing series still append.
	last := at.Add(time.Minute)
	res = f.flush(last, TrafficBatch{
		Tunnels: map[string][]TunnelPoint{tunnels[MaxTrafficTunnels]: {tp(last, 1, 1, 1)}, "t00": {tp(last, 3, 3, 3)}},
		Nodes:   map[string][]HostPoint{nodes[MaxTrafficNodes]: {{Start: last}}},
		Hub:     []HostPoint{{Start: last}},
	})
	require.Equal(t, []string{"t:t64", "n:n64"}, res.Refused)
	pts, err := s.TunnelPoints("t00", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, pts, 2)
	series, err := s.TrafficSeries()
	require.NoError(t, err)
	require.Len(t, series, MaxTrafficTunnels+MaxTrafficNodes+1, "the hub series is not capped")

	// Deleting one frees a slot.
	require.NoError(t, s.DeleteTunnel("t01"))
	res = f.flush(last.Add(time.Minute), TrafficBatch{Tunnels: map[string][]TunnelPoint{tunnels[MaxTrafficTunnels]: {tp(last, 1, 1, 1)}}})
	require.Empty(t, res.Refused)
}

// 20 tunnels with full retention in every tier stay far below the budget;
// the file stays under 8 MiB.
func TestTrafficSizeAtFullRetention(t *testing.T) {
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, fmt.Sprintf("tun-%02d", i))
	}
	s, path := trafficStore(t, ids, nil)
	f := newFlusher(t, s)
	start := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	// 13 periods, one point each.
	for m := 0; m < DefaultPeriodKeep; m++ {
		at := start.AddDate(0, m, 0)
		b := TrafficBatch{Tunnels: map[string][]TunnelPoint{}}
		for _, id := range ids {
			b.Tunnels[id] = []TunnelPoint{tp(at, 1<<30, 1<<34, 10)}
		}
		f.flush(at.Add(time.Minute), b)
	}
	// 32 days of m30 (one batch per 6 hours).
	m30Start := start.AddDate(0, DefaultPeriodKeep, 0)
	for i := 0; i < DefaultM30Keep; i += 12 {
		at := m30Start.Add(time.Duration(i) * StepM30)
		b := TrafficBatch{Tunnels: map[string][]TunnelPoint{}}
		for _, id := range ids {
			for j := 0; j < 12; j++ {
				b.Tunnels[id] = append(b.Tunnels[id], tp(at.Add(time.Duration(j)*StepM30), 1<<20, 1<<24, 10))
			}
		}
		f.flush(at.Add(6*time.Hour), b)
	}
	// A full day of m1 (one flush per 10 minutes).
	day := m30Start.Add(DefaultM30Keep * StepM30)
	for i := 0; i < DefaultM1Keep; i += 10 {
		at := day.Add(time.Duration(i) * time.Minute)
		b := TrafficBatch{Tunnels: map[string][]TunnelPoint{}}
		for _, id := range ids {
			for j := 0; j < 10; j++ {
				b.Tunnels[id] = append(b.Tunnels[id], tp(at.Add(time.Duration(j)*time.Minute), 1<<20, 1<<24, 10))
			}
		}
		f.flush(at.Add(10*time.Minute), b)
	}
	for _, id := range ids[:1] {
		for tier, n := range map[string]int{TierM1: DefaultM1Keep, TierM30: DefaultM30Keep, TierPeriod: DefaultPeriodKeep} {
			pts, err := s.TunnelPoints(id, tier, time.Time{}, time.Time{})
			require.NoError(t, err)
			require.Len(t, pts, n, tier)
		}
	}
	// Write budget of one steady-state flush of 20 tunnel series: a few
	// pages per series (the last leaf and the root of each tier, plus the
	// pruned first leaf), not a rewrite of the history.
	next := day.Add(DefaultM1Keep * time.Minute)
	b := TrafficBatch{Tunnels: map[string][]TunnelPoint{}}
	for _, id := range ids {
		b.Tunnels[id] = []TunnelPoint{tp(next, 1, 1, 1)}
	}
	writes := func() int64 { st := s.db.Stats(); return st.TxStats.GetWrite() }
	w0 := writes()
	f.flush(next.Add(time.Minute), b)
	written := writes() - w0
	require.LessOrEqual(t, written, int64(len(ids)*12+16), "pages written by one flush")
	t.Logf("pages written by one flush of %d series: %d", len(ids), written)

	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Less(t, st.Size(), int64(8<<20), "file size")
	live, err := s.LiveSize()
	require.NoError(t, err)
	require.Less(t, live, int64(8<<20))
	over, _, err := s.SizeGuard(SizeBudget, false)
	require.NoError(t, err)
	require.False(t, over)
}

// The size guard follows the live data, so it switches off again after
// history is deleted, with a hysteresis band between 60 % and 80 %.
func TestTrafficSizeGuard(t *testing.T) {
	s, _ := trafficStore(t, []string{"big"}, nil)
	live0, err := s.LiveSize()
	require.NoError(t, err)
	require.Positive(t, live0)

	f := newFlusher(t, s)
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	b := TrafficBatch{Tunnels: map[string][]TunnelPoint{}}
	for i := 0; i < DefaultM1Keep; i++ {
		b.Tunnels["big"] = append(b.Tunnels["big"], tp(start.Add(time.Duration(i)*time.Minute), 1, 1, 1))
	}
	f.flush(start.Add(25*time.Hour), b)
	live1, err := s.LiveSize()
	require.NoError(t, err)
	require.Greater(t, live1, live0)

	budget := live1 * 5 / 4 * 99 / 100 // live1 is just above 80 %
	over, live, err := s.SizeGuard(budget, false)
	require.NoError(t, err)
	require.True(t, over)
	require.Equal(t, live1, live)
	// Once on, it stays on above 60 %.
	over, _, err = s.SizeGuard(live1*3/2, true)
	require.NoError(t, err)
	require.True(t, over)
	over, _, err = s.SizeGuard(live1*3/2, false)
	require.NoError(t, err)
	require.False(t, over)

	// Deleting the series frees pages: the live size goes down (the file
	// does not shrink) and the guard switches off.
	require.NoError(t, s.DeleteTunnel("big"))
	for i := 0; i < 3; i++ { // pending pages are freed by later transactions
		require.NoError(t, s.PutMeta("tick", i))
	}
	live2, err := s.LiveSize()
	require.NoError(t, err)
	require.Less(t, live2, live1)
	over, _, err = s.SizeGuard(budget, true)
	require.NoError(t, err)
	require.Equal(t, live2 >= budget/5*3, over)

	require.Equal(t, ReducedM1Keep, RetentionFor(true).M1)
	require.Equal(t, Retention{M1: DefaultM1Keep, M30: DefaultM30Keep, Periods: DefaultPeriodKeep}, RetentionFor(false))
}

func TestTrafficReopenVerifiesNestedBuckets(t *testing.T) {
	s, path := trafficStore(t, []string{"main"}, []string{"de-1"})
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	f.flush(at, TrafficBatch{
		Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 1, 1)}},
		Nodes:   map[string][]HostPoint{"de-1": {{Start: at}}},
		Hub:     []HostPoint{{Start: at}},
	})
	require.NoError(t, verify(s.db))
	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s2.Close()) }()
	require.Nil(t, s2.Recovered())
	pts, err := s2.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Len(t, pts, 1)
}

// A damaged value surfaces as DEY-X021 on read instead of a panic.
func TestTrafficShortRecordIsAnError(t *testing.T) {
	s, _ := trafficStore(t, []string{"main"}, nil)
	f := newFlusher(t, s)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	f.flush(at, TrafficBatch{Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 1, 1)}}})
	require.NoError(t, s.db.Update(func(tx *bolt.Tx) error {
		var k [4]byte
		binary.BigEndian.PutUint32(k[:], uint32(at.Unix())) // #nosec G115 -- test time
		return tx.Bucket([]byte(BucketTraffic)).Bucket([]byte("t:main")).Bucket([]byte(TierM1)).Put(k[:], []byte{1, 2})
	}))
	_, err := s.TunnelPoints("main", TierM1, time.Time{}, time.Time{})
	require.True(t, deyerr.HasCode(err, deyerr.X021))
	// Merging into it fails the whole flush, which is rolled back.
	_, err = s.AppendTraffic(TrafficBatch{Cursor: TrafficCursor{Seq: 99}, Now: at,
		Tunnels: map[string][]TunnelPoint{"main": {tp(at, 1, 1, 1)}}}, Retention{})
	require.True(t, deyerr.HasCode(err, deyerr.X021))
	cp, _, err := s.TrafficCheckpoint()
	require.NoError(t, err)
	require.Equal(t, uint64(1), cp.Cursor.Seq)
}

func TestTrafficConcurrentAppendAndRead(t *testing.T) {
	s, _ := trafficStore(t, []string{"a1", "b2"}, nil)
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		seq uint64
	)
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				mu.Lock()
				seq++
				cur := seq
				mu.Unlock()
				_, err := s.AppendTraffic(TrafficBatch{
					Cursor:  TrafficCursor{Seq: cur},
					Now:     at,
					Tunnels: map[string][]TunnelPoint{"a1": {tp(at.Add(-time.Duration(i)*time.Minute), 1, 1, 1)}, "b2": {tp(at, 1, 1, 1)}},
				}, Retention{})
				assert.NoError(t, err)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, err := s.TunnelPoints("a1", TierM1, time.Time{}, time.Time{})
				assert.NoError(t, err)
				_, _, err = s.TrafficCheckpoint()
				assert.NoError(t, err)
				_, _, err = s.SizeGuard(SizeBudget, false)
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
	// Batches may commit out of Seq order; every applied one is counted
	// exactly once, so the m30 total equals the m1 total.
	m1, err := s.TunnelPoints("b2", TierM1, time.Time{}, time.Time{})
	require.NoError(t, err)
	m30, err := s.TunnelPoints("b2", TierM30, time.Time{}, time.Time{})
	require.NoError(t, err)
	in1, _ := sumPoints(m1)
	in30, _ := sumPoints(m30)
	require.Equal(t, in1, in30)
	require.Positive(t, in1)
}
