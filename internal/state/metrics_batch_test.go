package state

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPutMetricsBatch(t *testing.T) {
	s, _ := newStore(t)
	require.NoError(t, s.PutMetricsBatch(nil))
	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	require.NoError(t, s.PutMetricsBatch(map[string]Metrics{
		"main":  {BytesIn: 10, BytesOut: 20, ActiveConns: 3, Source: MetricsSourceNFT, BytesSince: since},
		"games": {Source: MetricsSourceSS, ConnsUnknown: true, Stale: true},
	}))
	m, ok, err := s.GetMetrics("main")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(20), m.BytesOut)
	require.Equal(t, MetricsSourceNFT, m.Source)
	require.True(t, m.BytesSince.Equal(since))
	require.Equal(t, time.UTC, m.At.Location(), "zero At defaults to now, in UTC")
	g, ok, err := s.GetMetrics("games")
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, g.ConnsUnknown)
	require.True(t, g.Stale)
	require.Error(t, s.PutMetricsBatch(map[string]Metrics{"": {}}))
}
