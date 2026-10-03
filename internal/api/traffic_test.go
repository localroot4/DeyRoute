package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestParseTrafficTarget(t *testing.T) {
	for in, want := range map[string][2]string{
		"hub":         {TrafficKindHub, "hub"},
		" main ":      {TrafficKindTunnel, "main"},
		"tunnel:main": {TrafficKindTunnel, "main"},
		"tunnel:hub":  {TrafficKindTunnel, "hub"},
		"node:de-1":   {TrafficKindNode, "de-1"},
	} {
		kind, id, ok := ParseTrafficTarget(in)
		require.True(t, ok, in)
		require.Equal(t, want, [2]string{kind, id}, in)
	}
	for _, in := range []string{"", "node:", "tunnel:", "Main", "node:a", "x/y", "node:" + string(make([]byte, 40))} {
		_, _, ok := ParseTrafficTarget(in)
		require.False(t, ok, in)
	}
	require.Equal(t, "hub", TrafficTarget(TrafficKindHub, "hub"))
	require.Equal(t, "node:de-1", TrafficTarget(TrafficKindNode, "de-1"))
	require.Equal(t, "tunnel:hub", TrafficTarget(TrafficKindTunnel, "hub"))
}

func TestTrafficQueryNormalize(t *testing.T) {
	q, err := TrafficQuery{}.Normalize()
	require.NoError(t, err)
	require.Equal(t, TrafficQuery{Period: TrafficPeriod1h, MaxPoints: DefaultTrafficPoints}, q)

	q, err = TrafficQuery{Targets: []string{"main", "tunnel:main", "hub", "node:de-1"}, Period: "30d", MaxPoints: 99999}.Normalize()
	require.NoError(t, err)
	require.Equal(t, []string{"tunnel:main", "hub", "node:de-1"}, q.Targets)
	require.Equal(t, MaxTrafficPoints, q.MaxPoints)

	_, err = TrafficQuery{Period: "2h"}.Normalize()
	e := requireCode(t, err, deyerr.C027)
	require.Contains(t, e.Why(), "1h, 24h, 7d, 30d")
	require.Equal(t, deyerr.ExitUser, e.ExitCode())
	_, err = TrafficQuery{Targets: []string{"node:"}}.Normalize()
	requireCode(t, err, deyerr.C027)

	for _, p := range TrafficPeriods {
		d, ok := TrafficPeriodDuration(p)
		require.True(t, ok)
		require.Positive(t, d)
	}
	_, ok := TrafficPeriodDuration("1y")
	require.False(t, ok)
}

// TestTrafficPointJSON pins the wire names and the meaning of an absent
// conns (unknown) versus 0.
func TestTrafficPointJSON(t *testing.T) {
	zero := 0
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	b, err := json.Marshal([]TrafficPoint{{At: at, BytesIn: 1, BytesOut: 2, Conns: &zero}, {At: at, Gap: true}})
	require.NoError(t, err)
	require.JSONEq(t, `[{"at":"2026-10-01T12:00:00Z","bytes_in":1,"bytes_out":2,"conns":0},{"at":"2026-10-01T12:00:00Z","gap":true}]`, string(b))
	b, err = json.Marshal(TrafficNow{At: at, Available: true, RateInBitS: 8, RateOutBitS: 16})
	require.NoError(t, err)
	require.JSONEq(t, `{"at":"2026-10-01T12:00:00Z","available":true,"rate_in_bit_s":8,"rate_out_bit_s":16,"today_in":0,"today_out":0}`, string(b))
}

func TestSysctlArgsInputsHash(t *testing.T) {
	yes, no := true, false
	a := SysctlArgs{Profile: "auto", BBR: &yes, Reserved: []string{"44433", "30000-31999"}, PlanVersion: 1}
	b := SysctlArgs{Profile: "auto", BBR: &yes, Reserved: []string{"30000-31999", "44433"}, PlanVersion: 1}
	require.Equal(t, a.InputsHash(), b.InputsHash(), "the order of the reserved ports does not matter")
	require.Len(t, a.InputsHash(), 16)
	for _, c := range []SysctlArgs{
		{Profile: "balanced", BBR: &yes, Reserved: a.Reserved, PlanVersion: 1},
		{Profile: "auto", BBR: &no, Reserved: a.Reserved, PlanVersion: 1},
		{Profile: "auto", Reserved: a.Reserved, PlanVersion: 1},
		{Profile: "auto", BBR: &yes, Reserved: a.Reserved, PlanVersion: 2},
		{Profile: "auto", BBR: &yes, Reserved: a.Reserved, PlanVersion: 1, IPForward: true},
		{Profile: "auto", BBR: &yes, Reserved: []string{"44433"}, PlanVersion: 1},
	} {
		require.NotEqual(t, a.InputsHash(), c.InputsHash(), "%+v", c)
	}
}

func TestHelloHasFeature(t *testing.T) {
	var h *Hello
	require.False(t, h.HasFeature(FeatureTuneAuto))
	h = &Hello{Features: []string{FeatureTuneAuto}}
	require.True(t, h.HasFeature(FeatureTuneAuto))
	require.False(t, (&Hello{}).HasFeature(FeatureTuneAuto))
}
