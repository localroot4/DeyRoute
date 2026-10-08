package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
)

func TestUpdateAuto(t *testing.T) {
	e := newEnv(t)
	var gotMode string
	info := api.AutoUpdateInfo{Enabled: true, Hour: 4, MinAgeHours: 24, Current: "1.0.0",
		Next: "1.1.0", NextAt: time.Date(2026, 10, 9, 4, 0, 0, 0, time.Local),
		Last: "updated 0.9.0 → 1.0.0; 1 tunnels and 1 nodes work", LastAt: time.Date(2026, 10, 8, 4, 5, 0, 0, time.Local),
		Skipped: []string{"0.9.5"}}
	e.stub.UpdateAutoFn = func(_ context.Context, mode string) (api.AutoUpdateInfo, error) {
		gotMode = mode
		out := info
		out.Enabled = mode != "off"
		return out, nil
	}
	out := e.ok("update", "auto")
	require.Empty(t, gotMode)
	for _, want := range []string{"AUTOMATIC UPDATE", "Status", "on", "every day at 04:00 server time; only releases out for 24 hours",
		"1.1.0 on 2026-10-09 04:00", "updated 0.9.0", "(2026-10-08 04:05)", "0.9.5",
		"put back by itself", "Turn off: deyroute update auto off"} {
		require.Contains(t, out, want)
	}
	out = e.ok("update", "auto", "off")
	require.Equal(t, "off", gotMode)
	require.Contains(t, out, "Automatic update is off")
	require.Contains(t, out, "Turn on: deyroute update auto on")
	require.Contains(t, e.ok("update", "auto", "ON"), "Automatic update is on.")
	require.Equal(t, "on", gotMode)
	doc := e.json("update", "auto")
	require.Equal(t, true, doc["enabled"])
	require.Equal(t, "1.1.0", doc["next"])
	require.Contains(t, e.fail(1, "update", "auto", "maybe"), "update auto off")
	e.fail(1, "update", "auto", "on", "off")
}
