package state

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return t0 } }

func newStore(t *testing.T, opts ...option) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lib", "state.db")
	s, err := openWith(path, append([]option{withClock(fixedClock())}, opts...)...)
	require.NoError(t, err)
	require.Nil(t, s.Recovered())
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func garbage(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func TestOpenCreatesFileWithBuckets(t *testing.T) {
	s, path := newStore(t)
	require.Equal(t, path, s.Path())
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())
	require.Equal(t, MaxEvents, s.maxEvents)
	require.Equal(t, MaxProbeSamples, s.maxProbes)
	n, err := s.Size()
	require.NoError(t, err)
	require.Positive(t, n)
	require.Less(t, n, SizeBudget)
	require.Equal(t, path+".bak", BackupPath(path))

	// Reopening an existing database works and tightens its mode.
	require.NoError(t, s.Close())
	require.NoError(t, os.Chmod(path, 0o644))
	s2, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s2.Close()) }()
	st, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())
}

func TestOpenEmptyPath(t *testing.T) {
	_, err := Open("")
	require.True(t, deyerr.HasCode(err, deyerr.X021))
}

func TestOpenDirectoryIsEnvironmentError(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(dir)
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, deyerr.X021), "got %v", err)
	// Nothing was moved aside.
	entries, err := os.ReadDir(filepath.Dir(dir))
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), CorruptInfix)
	}
}

func TestOpenLockedReturnsX020(t *testing.T) {
	s, path := newStore(t)
	_ = s
	_, err := openWith(path, withTimeout(100*time.Millisecond))
	require.True(t, deyerr.HasCode(err, deyerr.X020), "got %v", err)
	// The live file must not have been touched.
	_, err = os.Stat(path)
	require.NoError(t, err)
	matches, _ := filepath.Glob(path + CorruptInfix + "*")
	require.Empty(t, matches)
}

func TestClosedStoreReturnsX021(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	_, _, err = s.GetNode("de-1")
	require.True(t, deyerr.HasCode(err, deyerr.X021), "got %v", err)
	require.NoError(t, s.Close(), "Close is idempotent")
	_, err = s.Size()
	require.Error(t, err)
}

func TestNodes(t *testing.T) {
	s, _ := newStore(t)
	_, ok, err := s.GetNode("de-1")
	require.NoError(t, err)
	require.False(t, ok)

	udp := true
	tehran := time.FixedZone("IRST", 3*3600+1800)
	n := NodeState{ID: "de-1", Online: true, LastHeartbeat: t0.In(tehran), AgentVersion: "1.0.0",
		Compatible: true, ControlRTTms: 42, Units: map[string]string{"u": "active"}, UDPOK: &udp}
	require.NoError(t, s.PutNode(n))
	require.NoError(t, s.PutNode(NodeState{ID: "at-1"}))

	got, ok, err := s.GetNode("de-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, time.UTC, got.LastHeartbeat.Location())
	require.True(t, got.LastHeartbeat.Equal(t0))
	require.Equal(t, 42, got.ControlRTTms)
	require.Equal(t, "active", got.Units["u"])
	require.NotNil(t, got.UDPOK)
	require.True(t, *got.UDPOK)

	list, err := s.ListNodes()
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "at-1", list[0].ID)
	require.Equal(t, "de-1", list[1].ID)

	require.Error(t, s.PutNode(NodeState{}))
	require.NoError(t, s.DeleteNode("de-1"))
	_, ok, err = s.GetNode("de-1")
	require.NoError(t, err)
	require.False(t, ok)
	require.Error(t, s.DeleteNode(""))
}

func TestTunnels(t *testing.T) {
	s, _ := newStore(t)
	list, err := s.ListTunnels()
	require.NoError(t, err)
	require.NotNil(t, list)
	require.Empty(t, list)

	loc := time.FixedZone("X", 7200)
	ts := TunnelState{
		ID: "main", State: StateUp,
		Active:      Candidate{Node: "de-1", Transport: "backhaul/wssmux"},
		SwitchTimes: []time.Time{t0.In(loc), t0.Add(time.Minute).In(loc)},
		Quarantine:  map[string]Quarantine{"de-1/rathole/noise": {Until: t0.In(loc), Duration: 10 * time.Minute}},
		Skipped:     map[string]Skip{"de-1/hysteria2/udp": {Reason: "udp closed", Code: "DEY-B007", RecheckAt: t0.In(loc)}},
		UpSince:     t0.In(loc),
		UpdatedAt:   t0,
	}
	require.NoError(t, s.PutTunnel(ts))
	require.NoError(t, s.PutTunnel(TunnelState{ID: "backup", State: StateDown}))
	got, ok, err := s.GetTunnel("main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StateUp, got.State)
	require.Equal(t, "de-1/backhaul/wssmux", got.Active.Key())
	require.Len(t, got.SwitchTimes, 2)
	require.Equal(t, time.UTC, got.SwitchTimes[1].Location())
	require.Equal(t, time.UTC, got.UpSince.Location())
	require.Equal(t, 10*time.Minute, got.Quarantine["de-1/rathole/noise"].Duration)
	require.Equal(t, time.UTC, got.Quarantine["de-1/rathole/noise"].Until.Location())
	require.Equal(t, time.UTC, got.Skipped["de-1/hysteria2/udp"].RecheckAt.Location())
	require.Equal(t, loc, ts.Quarantine["de-1/rathole/noise"].Until.Location(), "caller's map untouched")

	list, err = s.ListTunnels()
	require.NoError(t, err)
	require.Equal(t, []string{"backup", "main"}, []string{list[0].ID, list[1].ID})
	require.Error(t, s.PutTunnel(TunnelState{}))
	require.Error(t, s.DeleteTunnel(""))
}

func TestMetrics(t *testing.T) {
	s, _ := newStore(t)
	_, ok, err := s.GetMetrics("main")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.PutMetrics("main", Metrics{BytesIn: 10, BytesOut: 20, ActiveConns: 3, Source: "ss"}))
	m, ok, err := s.GetMetrics("main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(10), m.BytesIn)
	require.True(t, m.At.Equal(t0), "zero At defaults to now")
}

func TestMeta(t *testing.T) {
	s, _ := newStore(t)
	var v struct{ Decoy string }
	ok, err := s.GetMeta("decoy", &v)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.PutMeta("decoy", map[string]string{"Decoy": "www.example.com"}))
	ok, err = s.GetMeta("decoy", &v)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "www.example.com", v.Decoy)

	var wrong int
	_, err = s.GetMeta("decoy", &wrong)
	require.True(t, deyerr.HasCode(err, deyerr.X021))

	require.NoError(t, s.DeleteMeta("decoy"))
	require.NoError(t, s.DeleteMeta("decoy"))
	ok, err = s.GetMeta("decoy", &v)
	require.NoError(t, err)
	require.False(t, ok)

	require.Error(t, s.PutMeta("", 1))
	require.Error(t, s.PutMeta("bad", func() {}))
	_, err = s.GetMeta("", &v)
	require.Error(t, err)
	require.Error(t, s.DeleteMeta(""))
}

func TestKeys(t *testing.T) {
	k := Key("main", "de-1", "backhaul/wssmux")
	require.Equal(t, "main/de-1/backhaul/wssmux", k)
	tu, n, tr, ok := SplitKey(k)
	require.True(t, ok)
	require.Equal(t, []string{"main", "de-1", "backhaul/wssmux"}, []string{tu, n, tr})
	for _, bad := range []string{"", "main", "main/de-1", "/de-1/x", "main//x", "main/de-1/"} {
		_, _, _, ok := SplitKey(bad)
		require.False(t, ok, bad)
	}
}

func TestProbesKeepLast120(t *testing.T) {
	s, _ := newStore(t)
	for i := 0; i < MaxProbeSamples+7; i++ {
		require.NoError(t, s.AppendProbe("main", "de-1", "backhaul/wssmux", ProbeSample{
			At: t0.Add(time.Duration(i) * time.Second), OK: i%2 == 0, RTT: time.Duration(i) * time.Millisecond, Kind: "path",
		}))
	}
	got, err := s.Probes("main", "de-1", "backhaul/wssmux")
	require.NoError(t, err)
	require.Len(t, got, MaxProbeSamples)
	require.Equal(t, 7*time.Millisecond, got[0].RTT, "oldest kept sample")
	require.Equal(t, time.Duration(MaxProbeSamples+6)*time.Millisecond, got[len(got)-1].RTT)
	for i := 1; i < len(got); i++ {
		require.True(t, got[i].At.After(got[i-1].At), "chronological order")
	}

	// Zero time defaults to now; other keys are independent.
	require.NoError(t, s.AppendProbe("main", "nl-1", "rathole/noise", ProbeSample{OK: true}))
	other, err := s.Probes("main", "nl-1", "rathole/noise")
	require.NoError(t, err)
	require.Len(t, other, 1)
	require.True(t, other[0].At.Equal(t0))

	none, err := s.Probes("x", "y", "z/w")
	require.NoError(t, err)
	require.NotNil(t, none)
	require.Empty(t, none)

	keys, err := s.ProbeKeys()
	require.NoError(t, err)
	require.Equal(t, []string{"main/de-1/backhaul/wssmux", "main/nl-1/rathole/noise"}, keys)

	require.Error(t, s.AppendProbe("", "de-1", "x/y", ProbeSample{}))
}

func TestProbesSmallCap(t *testing.T) {
	s, _ := newStore(t, withMaxProbes(3))
	for i := 1; i <= 10; i++ {
		require.NoError(t, s.AppendProbe("t", "n", "a/b", ProbeSample{At: t0, RTT: time.Duration(i)}))
	}
	got, err := s.Probes("t", "n", "a/b")
	require.NoError(t, err)
	require.Equal(t, []time.Duration{8, 9, 10}, []time.Duration{got[0].RTT, got[1].RTT, got[2].RTT})
}

func TestSnapshot(t *testing.T) {
	s, path := newStore(t)
	require.NoError(t, s.PutNode(NodeState{ID: "de-1", Online: true}))
	bak := BackupPath(path)
	require.NoError(t, s.Snapshot(bak))
	st, err := os.Stat(bak)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())
	// Overwrite an existing snapshot atomically.
	require.NoError(t, s.PutNode(NodeState{ID: "nl-1"}))
	require.NoError(t, s.Snapshot(bak))
	left, _ := filepath.Glob(bak + ".tmp-*")
	require.Empty(t, left, "temp file removed")

	snap, err := Open(bak)
	require.NoError(t, err)
	nodes, err := snap.ListNodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	require.NoError(t, snap.Close())

	require.Error(t, s.Snapshot(path), "refuses to overwrite the live file")
	// Nested destination directory is created.
	require.NoError(t, s.Snapshot(filepath.Join(t.TempDir(), "a", "b", "state.db")))
}

// Regression: a relative or non-canonical spelling of the live path used
// to pass the guard; the rename then detached the open database from its
// path and every later write was lost on the next Open.
func TestSnapshotRefusesLiveFileUnderAnySpelling(t *testing.T) {
	s, path := newStore(t)
	require.NoError(t, s.PutNode(NodeState{ID: "a"}))
	wd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(wd, path)
	require.NoError(t, err)
	hard := filepath.Join(filepath.Dir(path), "hard.db")
	require.NoError(t, os.Link(path, hard))
	for _, dst := range []string{
		path,
		rel,
		filepath.Join(filepath.Dir(path), "x", "..", filepath.Base(path)),
		filepath.Dir(path) + "//" + filepath.Base(path),
		hard,
	} {
		err := s.Snapshot(dst)
		require.True(t, deyerr.HasCode(err, deyerr.X021), "%s: %v", dst, err)
		require.Contains(t, err.Error(), "live database")
	}
	require.NoError(t, s.PutNode(NodeState{ID: "b"}))
	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s2.Close()) }()
	nodes, err := s2.ListNodes()
	require.NoError(t, err)
	require.Len(t, nodes, 2, "writes after the refused snapshots are kept")

	// A relative path to Open is made absolute.
	s3, err := Open(rel + ".other")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(s3.Path()))
	require.NoError(t, s3.Close())
}

// Section 11 / S26: free text stored in the database (shown in the TUI,
// exported, copied into doctor bundles) never keeps a secret.
func TestFreeTextIsRedacted(t *testing.T) {
	s, _ := newStore(t)
	const tok = "q1W2e3R4t5Y6u7I8o9P0a_S-d1F2g3H4j5K6l7Z8x9C"
	leak := "dial failed token=" + tok + " via dey://" + tok + "@1.2.3.4:44433"
	e, err := s.AppendEvent(Event{Type: EvProbeError, Message: leak, Reason: "password=\"a b\""})
	require.NoError(t, err)
	require.NotContains(t, e.Message, tok)
	require.Equal(t, `password="***"`, e.Reason)
	require.NoError(t, s.AppendProbe("main", "de-1", "a/b", ProbeSample{Error: leak}))
	require.NoError(t, s.PutNode(NodeState{ID: "de-1", LastError: leak}))
	require.NoError(t, s.PutTunnel(TunnelState{ID: "main", LastProbeErr: leak, TransitionCause: leak,
		Skipped: map[string]Skip{"de-1/a/b": {Reason: leak}}}))
	n, err := s.ImportEvents(strings.NewReader(`{"seq":99,"type":"probe_error","message":"` + "token=" + tok + `"}` + "\n"))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var dump bytes.Buffer
	require.NoError(t, s.ExportEvents(&dump))
	probes, err := s.Probes("main", "de-1", "a/b")
	require.NoError(t, err)
	node, _, err := s.GetNode("de-1")
	require.NoError(t, err)
	tun, _, err := s.GetTunnel("main")
	require.NoError(t, err)
	all := dump.String() + probes[0].Error + node.LastError + tun.LastProbeErr + tun.TransitionCause + tun.Skipped["de-1/a/b"].Reason
	require.NotContains(t, all, tok)
	require.Contains(t, all, "dey://***@1.2.3.4:44433")
}

func TestSizeCountsFileOnDisk(t *testing.T) {
	s, path := newStore(t)
	for i := 0; i < 50; i++ {
		require.NoError(t, s.PutMeta(fmt.Sprintf("k%02d", i), strings.Repeat("x", 4000)))
	}
	n, err := s.Size()
	require.NoError(t, err)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, st.Size())
}

// Corruption recoveries keep only the newest damaged copies.
func TestCorruptCopiesArePruned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	older := []string{
		path + CorruptInfix + "1000000000",
		path + CorruptInfix + "1000000001",
		path + CorruptInfix + "1000000002",
		path + CorruptInfix + "1000000002000000001", // nanosecond name (collision)
		path + CorruptInfix + "notanumber",          // not ours: kept
	}
	for _, f := range older {
		require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	}
	require.NoError(t, os.WriteFile(path, garbage(t, 8192), 0o600))
	s, err := openWith(path, withClock(fixedClock()))
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	require.NotNil(t, s.Recovered())
	require.Contains(t, s.Recovered().Detail, "removed older damaged copies")
	left, err := filepath.Glob(path + CorruptInfix + "*")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		fmt.Sprintf("%s%s%d", path, CorruptInfix, t0.Unix()),
		path + CorruptInfix + "1000000002000000001",
		path + CorruptInfix + "1000000002",
		path + CorruptInfix + "notanumber",
	}, left)
	require.Equal(t, `a\*b\?c\[d\\e`, globEscape(`a*b?c[d\e`))
}

// Deeper bucket nesting (a newer layout read after a rollback) is not
// corruption; only runaway nesting is.
func TestDeeperNestingIsNotCorruption(t *testing.T) {
	s, path := newStore(t)
	require.NoError(t, s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(BucketMeta))
		for i := 0; i < 6; i++ {
			var err error
			if b, err = b.CreateBucketIfNotExists([]byte(fmt.Sprintf("l%d", i))); err != nil {
				return err
			}
		}
		return b.Put([]byte("k"), []byte("1"))
	}))
	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s2.Close()) }()
	require.Nil(t, s2.Recovered())
}

func TestConcurrentUse(t *testing.T) {
	s, _ := newStore(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				_, err := s.AppendEvent(Event{Type: EvTunnelUp, Tunnel: fmt.Sprintf("t%d", g), Message: "up"})
				assert.NoError(t, err)
				assert.NoError(t, s.AppendProbe(fmt.Sprintf("t%d", g), "n", "a/b", ProbeSample{OK: true}))
				_, err = s.AllocCtlPort(Key(fmt.Sprintf("t%d", g), "n", fmt.Sprintf("a/b%d", i)), CtlPortLow, CtlPortHigh, nil)
				assert.NoError(t, err)
				_, err = s.Events(EventFilter{Limit: 5})
				assert.NoError(t, err)
			}
		}(g)
	}
	wg.Wait()
	evs, err := s.Events(EventFilter{})
	require.NoError(t, err)
	require.Len(t, evs, 160)
	ports, err := s.CtlPorts()
	require.NoError(t, err)
	require.Len(t, ports, 160)
	seen := map[int]bool{}
	for _, p := range ports {
		require.False(t, seen[p], "port %d allocated twice", p)
		seen[p] = true
	}
}

// ---------------------------------------------------------------- corruption

func TestCorruptGarbageStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	junk := garbage(t, 64*1024)
	require.NoError(t, os.WriteFile(path, junk, 0o600))

	s, err := openWith(path, withClock(fixedClock()))
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	w := s.Recovered()
	require.NotNil(t, w)
	require.Equal(t, deyerr.X001, w.Code)
	require.Contains(t, w.Message(), path)
	aside := fmt.Sprintf("%s.corrupt-%d", path, t0.Unix())
	require.Contains(t, w.Detail, aside)
	require.Contains(t, w.Detail, "empty")

	moved, err := os.ReadFile(aside)
	require.NoError(t, err)
	require.True(t, bytes.Equal(junk, moved), "corrupt file kept for inspection")

	// The new database is fully usable.
	require.NoError(t, s.PutNode(NodeState{ID: "de-1"}))
	nodes, err := s.ListNodes()
	require.NoError(t, err)
	require.Len(t, nodes, 1)
}

func TestCorruptSmallGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	require.NoError(t, os.WriteFile(path, []byte("definitely not a bbolt file"), 0o600))
	s, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	require.NotNil(t, s.Recovered())
	require.Equal(t, deyerr.X001, s.Recovered().Code)
}

func TestCorruptRestoresBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, s.PutTunnel(TunnelState{ID: "main", State: StateUp}))
	_, err = s.AppendEvent(Event{Type: EvTunnelUp, Tunnel: "main", Message: "up"})
	require.NoError(t, err)
	require.NoError(t, s.Snapshot(BackupPath(path)))
	require.NoError(t, s.Close())
	require.NoError(t, os.WriteFile(path, garbage(t, 32*1024), 0o600))

	s, err = openWith(path, withClock(fixedClock()))
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	w := s.Recovered()
	require.NotNil(t, w)
	require.Equal(t, deyerr.X001, w.Code)
	require.Contains(t, w.Detail, "restored from "+BackupPath(path))
	ts, ok, err := s.GetTunnel("main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StateUp, ts.State)
	evs, err := s.Events(EventFilter{})
	require.NoError(t, err)
	require.Len(t, evs, 1)
	// The backup itself is untouched and the restore temp file is gone.
	_, err = os.Stat(BackupPath(path))
	require.NoError(t, err)
	left, _ := filepath.Glob(filepath.Join(dir, "state.db.restore-*"))
	require.Empty(t, left)
	// The sequence continues after the restored events.
	e, err := s.AppendEvent(Event{Type: EvTunnelDown})
	require.NoError(t, err)
	require.Equal(t, uint64(2), e.Seq)
}

func TestCorruptBackupAlsoCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	require.NoError(t, os.WriteFile(path, garbage(t, 16*1024), 0o600))
	require.NoError(t, os.WriteFile(BackupPath(path), garbage(t, 16*1024), 0o600))
	s, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	w := s.Recovered()
	require.NotNil(t, w)
	require.Contains(t, w.Detail, "unusable")
	require.Contains(t, w.Detail, "empty")
	nodes, err := s.ListNodes()
	require.NoError(t, err)
	require.Empty(t, nodes)
}

// Valid meta pages but damaged data pages: bbolt opens the file, and either
// fails or panics while reading pages; both must end in recovery.
func TestCorruptDataPagesRecovered(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fill  func(n int) []byte
		start int // byte offset of the damage (after the two meta pages)
	}{
		{"random", func(n int) []byte { return garbage(t, n) }, 2 * 4096},
		{"ones", func(n int) []byte { return bytes.Repeat([]byte{0xff}, n) }, 2 * 4096},
		{"zeros", func(n int) []byte { return make([]byte, n) }, 2 * 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			s, err := Open(path)
			require.NoError(t, err)
			for i := 0; i < 300; i++ {
				require.NoError(t, s.PutNode(NodeState{ID: fmt.Sprintf("node-%03d", i), AgentVersion: strings.Repeat("v", 50)}))
			}
			require.NoError(t, s.Close())

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Greater(t, len(data), tc.start)
			copy(data[tc.start:], tc.fill(len(data)-tc.start))
			require.NoError(t, os.WriteFile(path, data, 0o600))

			s, err = Open(path)
			require.NoError(t, err)
			defer func() { require.NoError(t, s.Close()) }()
			require.NotNil(t, s.Recovered())
			require.Equal(t, deyerr.X001, s.Recovered().Code)
			require.NoError(t, s.PutNode(NodeState{ID: "de-1"}))
		})
	}
}

// Damage only B+tree pages and keep the meta and freelist pages intact:
// bolt.Open succeeds and the verification walk must catch the damage.
func TestCorruptTreePagesCaughtByVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	for i := 0; i < 200; i++ {
		require.NoError(t, s.PutNode(NodeState{ID: fmt.Sprintf("node-%03d", i), AgentVersion: strings.Repeat("v", 40)}))
		require.NoError(t, s.AppendProbe("main", fmt.Sprintf("n%d", i%3), "a/b", ProbeSample{OK: true}))
	}
	require.NoError(t, s.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	const pageSize = 4096
	le := binary.LittleEndian
	// Pick the meta page with the highest txid (header 16 bytes, then
	// magic, version, pageSize, flags, root{pgid, seq}, freelist, pgid, txid).
	meta := func(n int) (freelist, txid uint64) {
		off := n*pageSize + 16
		return le.Uint64(data[off+32:]), le.Uint64(data[off+48:])
	}
	fl0, tx0 := meta(0)
	fl1, tx1 := meta(1)
	freelist := fl0
	if tx1 > tx0 {
		freelist = fl1
	}
	flOverflow := uint64(le.Uint32(data[int(freelist)*pageSize+12:])) // #nosec G115 -- test data
	for pg := uint64(2); pg*pageSize < uint64(len(data)); pg++ {
		if pg >= freelist && pg <= freelist+flOverflow {
			continue
		}
		off := int(pg) * pageSize // #nosec G115 -- test data
		for i := off; i < off+pageSize; i++ {
			data[i] = 0xee
		}
	}
	require.NoError(t, os.WriteFile(path, data, 0o600))

	s, err = Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	w := s.Recovered()
	require.NotNil(t, w)
	require.Equal(t, deyerr.X001, w.Code)
	require.NoError(t, s.PutNode(NodeState{ID: "de-1"}))
}

func TestVerifyWalksNestedBuckets(t *testing.T) {
	s, _ := newStore(t)
	require.NoError(t, s.AppendProbe("main", "de-1", "a/b", ProbeSample{OK: true}))
	require.NoError(t, verify(s.db))
}

func TestIsCorruptClassification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	require.False(t, isCorrupt(path, os.ErrPermission))
	require.True(t, isCorrupt(path, &corruptError{cause: fmt.Errorf("x")}))
	require.False(t, isCorrupt(path, fmt.Errorf("unknown on missing file")))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	require.True(t, isCorrupt(path, fmt.Errorf("unknown on existing file")))
	require.Contains(t, (&corruptError{cause: fmt.Errorf("boom")}).Error(), "boom")
	// Resource and filesystem failures on an existing file (mmap ENOMEM,
	// quota, busy device) must never move the database aside.
	for _, e := range []error{syscall.ENOMEM, syscall.EAGAIN, syscall.EINTR, syscall.EDQUOT, syscall.ENODEV, syscall.EBUSY} {
		require.False(t, isCorrupt(path, fmt.Errorf("mmap: %w", e)), "%v", e)
		require.True(t, deyerr.HasCode(openError(path, e), deyerr.X021))
	}
}
