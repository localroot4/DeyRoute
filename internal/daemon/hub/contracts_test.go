package hub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/version"
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

// TestRollbackComparesPerKey: every key of config.NewerKeysInUse carries
// the first release that knows it, and rollback and downgrades are refused
// only when their target is older than a key in use. The previous binary's
// version is compared when it is recorded (unknown: it may predate every
// key), and a target that is not older than the running build knows every
// key that build writes. The running build here predates the monitoring
// key on purpose: that is the situation of a key whose first version is
// set conservatively (hub.auto_update and the edge builds before 0.3.0).
func TestRollbackComparesPerKey(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old }) // after the hub stopped
	version.Version = "0.3.0-edge.13"
	env := startHub(t, func(c *config.Config) {
		c.Monitoring = &config.Monitoring{QuotaResetDay: 5}
	})
	l := env.h.Local()
	ctx := ctxT(t)

	_, err := l.UpdateApply(ctx, "0.3.0-edge.12", nil)
	require.Equal(t, deyerr.S011, codeOf(err), "older than the running build and than the key")
	require.Contains(t, deyerr.As(err).Message(), "monitoring (since 0.3.0-edge.15)")
	_, err = l.UpdateApply(ctx, "0.3.0-edge.14", nil)
	require.NotEqual(t, deyerr.S011, codeOf(err), "newer than the running build: never refused for the keys")

	bin := filepath.Join(env.root, config.BinaryPath)
	prev := filepath.Join(env.root, config.PrevBinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(prev), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!new"), 0o755))  // #nosec G306 -- test binary
	require.NoError(t, os.WriteFile(prev, []byte("#!old"), 0o755)) // #nosec G306 -- test binary
	_, err = l.UpdateRollback(ctx)
	require.Equal(t, deyerr.S011, codeOf(err), "the previous binary's version is not recorded")
	require.NoError(t, env.h.st.PutMeta(metaUpdatePrevious, "0.3.0-edge.14"))
	_, err = l.UpdateRollback(ctx)
	require.Equal(t, deyerr.S011, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), "0.3.0-edge.14")

	require.NoError(t, env.h.st.PutMeta(metaUpdatePrevious, "0.3.0-edge.15"))
	info, err := l.UpdateRollback(ctx)
	require.NoError(t, err, "the previous binary knows every key in use")
	require.Equal(t, "0.3.0-edge.15", info.Current)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!old", string(data))
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
