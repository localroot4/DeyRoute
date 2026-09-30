package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func seqs(evs []Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}

func TestAppendEventDefaults(t *testing.T) {
	s, _ := newStore(t)
	e, err := s.AppendEvent(Event{Type: EvTunnelDown, Tunnel: "main", Message: "down"})
	require.NoError(t, err)
	require.Equal(t, uint64(1), e.Seq)
	require.True(t, e.At.Equal(t0))
	require.Equal(t, time.UTC, e.At.Location())
	require.Equal(t, LevelInfo, e.Level)

	loc := time.FixedZone("IRST", 12600)
	e, err = s.AppendEvent(Event{Type: EvSwitchNode, At: t0.In(loc), Level: LevelWarn})
	require.NoError(t, err)
	require.Equal(t, uint64(2), e.Seq)
	require.Equal(t, time.UTC, e.At.Location())
	require.Equal(t, LevelWarn, e.Level)
}

func TestEventsRingPrunesOldest(t *testing.T) {
	const limit = 50
	s, _ := newStore(t, withMaxEvents(limit))
	for i := 1; i <= limit+13; i++ {
		_, err := s.AppendEvent(Event{Type: EvProbeError, Message: "x"})
		require.NoError(t, err)
	}
	evs, err := s.Events(EventFilter{})
	require.NoError(t, err)
	require.Len(t, evs, limit)
	require.Equal(t, uint64(limit+13), evs[0].Seq, "newest first")
	require.Equal(t, uint64(14), evs[len(evs)-1].Seq, "oldest kept")
}

// The production limit is 5000; a full-size ring fill proves the bound
// with the real constant.
func TestEventsRingDefault5000(t *testing.T) {
	if testing.Short() {
		t.Skip("fills 5000+ events")
	}
	s, _ := newStore(t)
	s.db.NoSync = true // speed only; semantics unchanged
	for i := 0; i < MaxEvents+10; i++ {
		_, err := s.AppendEvent(Event{Type: EvTunnelUp})
		require.NoError(t, err)
	}
	evs, err := s.Events(EventFilter{})
	require.NoError(t, err)
	require.Len(t, evs, MaxEvents)
	require.Equal(t, uint64(MaxEvents+10), evs[0].Seq)
	require.Equal(t, uint64(11), evs[len(evs)-1].Seq)
}

func TestEventSequencePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := openWith(path, withMaxEvents(3))
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err := s.AppendEvent(Event{Type: EvTunnelUp})
		require.NoError(t, err)
	}
	require.NoError(t, s.Close())
	s, err = openWith(path, withMaxEvents(3))
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	e, err := s.AppendEvent(Event{Type: EvTunnelUp})
	require.NoError(t, err)
	require.Equal(t, uint64(6), e.Seq)
	evs, err := s.Events(EventFilter{})
	require.NoError(t, err)
	require.Equal(t, []uint64{6, 5, 4}, seqs(evs))
}

func TestEventsFilter(t *testing.T) {
	s, _ := newStore(t)
	add := func(e Event) {
		t.Helper()
		_, err := s.AppendEvent(e)
		require.NoError(t, err)
	}
	add(Event{At: t0, Type: EvTunnelUp, Tunnel: "main", Node: "de-1"})                                        // 1
	add(Event{At: t0.Add(time.Minute), Type: EvSwitchNode, Tunnel: "main", FromNode: "de-1", ToNode: "nl-1"}) // 2
	add(Event{At: t0.Add(2 * time.Minute), Type: EvNodeOffline, Node: "de-1"})                                // 3
	add(Event{At: t0.Add(3 * time.Minute), Type: EvTunnelDown, Tunnel: "backup", Node: "at-1"})               // 4
	add(Event{At: t0.Add(4 * time.Minute), Type: EvTunnelUp, Tunnel: "main", Node: "nl-1"})                   // 5

	cases := []struct {
		name string
		f    EventFilter
		want []uint64
	}{
		{"all", EventFilter{}, []uint64{5, 4, 3, 2, 1}},
		{"tunnel", EventFilter{Tunnel: "main"}, []uint64{5, 2, 1}},
		{"node any role", EventFilter{Node: "nl-1"}, []uint64{5, 2}},
		{"node de-1", EventFilter{Node: "de-1"}, []uint64{3, 2, 1}},
		{"types", EventFilter{Types: []string{EvTunnelUp, EvTunnelDown}}, []uint64{5, 4, 1}},
		{"since inclusive", EventFilter{Since: t0.Add(2 * time.Minute)}, []uint64{5, 4, 3}},
		{"limit after filter", EventFilter{Tunnel: "main", Limit: 2}, []uint64{5, 2}},
		{"combined", EventFilter{Tunnel: "main", Node: "de-1", Types: []string{EvSwitchNode}}, []uint64{2}},
		{"no match", EventFilter{Tunnel: "nope"}, []uint64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Events(tc.f)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, tc.want, seqs(got))
		})
	}
}

func TestExportImportEvents(t *testing.T) {
	src, _ := newStore(t)
	for i := 0; i < 4; i++ {
		_, err := src.AppendEvent(Event{At: t0.Add(time.Duration(i) * time.Second), Type: EvTunnelUp, Tunnel: "main", Message: "m"})
		require.NoError(t, err)
	}
	var buf bytes.Buffer
	require.NoError(t, src.ExportEvents(&buf))
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 4)
	var first Event
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	require.Equal(t, uint64(1), first.Seq, "oldest first")

	dst, _ := newStore(t)
	n, err := dst.ImportEvents(strings.NewReader(buf.String() + "\n\n"))
	require.NoError(t, err)
	require.Equal(t, 4, n)
	evs, err := dst.Events(EventFilter{})
	require.NoError(t, err)
	require.Equal(t, []uint64{4, 3, 2, 1}, seqs(evs))
	e, err := dst.AppendEvent(Event{Type: EvTunnelDown})
	require.NoError(t, err)
	require.Equal(t, uint64(5), e.Seq, "sequence continues after the import")

	// Events without a sequence get new numbers; re-import replaces.
	n, err = dst.ImportEvents(strings.NewReader(`{"type":"flapping","message":"a"}` + "\n" + lines[0] + "\n"))
	require.NoError(t, err)
	require.Equal(t, 2, n)
	evs, err = dst.Events(EventFilter{})
	require.NoError(t, err)
	require.Equal(t, []uint64{6, 5, 4, 3, 2, 1}, seqs(evs))
	require.Equal(t, LevelInfo, evs[0].Level)

	// Empty input is a no-op.
	n, err = dst.ImportEvents(strings.NewReader(""))
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestImportEventsTrimsAndRejectsGarbage(t *testing.T) {
	s, _ := newStore(t, withMaxEvents(3))
	var b strings.Builder
	for _, seq := range []int{10, 20, 30, 40, 50} {
		e := Event{Seq: uint64(seq), Type: EvTunnelUp, At: t0}
		data, err := json.Marshal(e)
		require.NoError(t, err)
		b.Write(data)
		b.WriteByte('\n')
	}
	n, err := s.ImportEvents(strings.NewReader(b.String()))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	evs, err := s.Events(EventFilter{})
	require.NoError(t, err)
	require.Equal(t, []uint64{50, 40, 30}, seqs(evs))

	_, err = s.ImportEvents(strings.NewReader("{\"seq\":1}\nnot json\n"))
	require.True(t, deyerr.HasCode(err, deyerr.X021), "got %v", err)
	evs, err = s.Events(EventFilter{})
	require.NoError(t, err)
	require.Equal(t, []uint64{50, 40, 30}, seqs(evs), "failed import changes nothing")

	_, err = s.ImportEvents(errReader{})
	require.Error(t, err)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestExportEventsWriterError(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.AppendEvent(Event{Type: EvTunnelUp, Message: strings.Repeat("x", 8192)})
	require.NoError(t, err)
	require.True(t, deyerr.HasCode(s.ExportEvents(errWriter{}), deyerr.X021))
}
