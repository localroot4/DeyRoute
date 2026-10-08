package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// TestRollbackRefusedWithNewerKeys: an older binary refuses the monitoring
// and tuning keys of config.yaml, so rollback and a downgrade below
// FirstTrafficVersion are refused while one is set (DEY-S011), and allowed
// again without them.
func TestRollbackRefusedWithNewerKeys(t *testing.T) {
	env := startHub(t, func(c *config.Config) {
		c.Monitoring = &config.Monitoring{QuotaResetDay: 5}
	})
	_, err := env.h.Local().UpdateRollback(ctxT(t))
	require.Equal(t, deyerr.S011, codeOf(err))
	require.Contains(t, deyerr.As(err).Message(), "monitoring")
	_, err = env.h.Local().UpdateApply(ctxT(t), "0.2.0", nil)
	require.Equal(t, deyerr.S011, codeOf(err))
	_, err = env.h.Local().UpdateApply(ctxT(t), "0.2.0-edge.1", nil)
	require.Equal(t, deyerr.S011, codeOf(err))
	_, err = env.h.Local().UpdateApply(ctxT(t), "0.3.0-edge.14", nil)
	require.Equal(t, deyerr.S011, codeOf(err), "an edge build before the keys existed")
	// A newer edge build of 0.3.0 knows the keys: never refused for them.
	_, err = env.h.Local().UpdateApply(ctxT(t), "0.3.0-edge.34", nil)
	require.NotEqual(t, deyerr.S011, codeOf(err))

	_, err = env.h.mutate(func(c *config.Config) error {
		c.Monitoring = nil
		return nil
	})
	require.NoError(t, err)
	_, err = env.h.Local().UpdateRollback(ctxT(t))
	require.Equal(t, deyerr.S007, codeOf(err), "no newer keys: the guard stays out of the way")
}

// TestMonitoringAndTuningStubs: Traffic checks its query (DEY-C027) and,
// without byte accounting, says why (DEY-X061); the automatic tuning answers
// (a confirmation with another plan's hash is DEY-X065); the background
// loops are wired and end at once.
func TestMonitoringAndTuningStubs(t *testing.T) {
	env := startHub(t, nil)
	l := env.h.Local()
	_, err := l.Traffic(ctxT(t), api.TrafficQuery{Period: "2h"})
	require.Equal(t, deyerr.C027, codeOf(err))
	_, err = l.Traffic(ctxT(t), api.TrafficQuery{Targets: []string{"node:"}})
	require.Equal(t, deyerr.C027, codeOf(err))
	// The test hub counts no bytes (DisableStats): the report says why.
	rep, err := l.Traffic(ctxT(t), api.TrafficQuery{})
	require.NoError(t, err)
	require.False(t, rep.Available)
	require.Equal(t, string(deyerr.X061), rep.Reason.Code)
	_, err = l.OptimizeAutoPlan(ctxT(t), api.AutoOptions{})
	require.NoError(t, err)
	_, err = l.OptimizeAutoApply(ctxT(t), api.AutoApply{Hash: "x"}, nil)
	require.Equal(t, deyerr.X065, codeOf(err))
	_, err = l.OptimizeCheck(ctxT(t))
	require.NoError(t, err)

	o := Options{}.withDefaults()
	require.Equal(t, DefaultTrafficInterval, o.TrafficInterval)
	require.Equal(t, DefaultTrafficFlush, o.TrafficFlush)
	require.Equal(t, DefaultTuneCheckInterval, o.TuneCheckInterval)
	require.NotNil(t, o.Location)
}
