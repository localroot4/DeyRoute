package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

func TestAutoUpdateCandidate(t *testing.T) {
	now := time.Date(2026, 10, 8, 4, 10, 0, 0, time.UTC)
	var r autoUpdateRecord
	ready, waiting, _ := r.candidate("1.0.0", 24*time.Hour, now)
	require.Empty(t, ready)
	require.Empty(t, waiting)

	r.saw("1.1.0", now.Add(-30*time.Hour))
	r.saw("1.2.0", now.Add(-25*time.Hour))
	r.saw("1.3.0", now.Add(-time.Hour))
	r.saw("1.2.0", now) // the first sighting stays
	ready, waiting, until := r.candidate("1.0.0", 24*time.Hour, now)
	require.Equal(t, "1.2.0", ready, "the newest release out for a day")
	require.Equal(t, "1.3.0", waiting)
	require.Equal(t, now.Add(23*time.Hour), until)

	r.skip("1.2.0")
	r.skip("1.2.0")
	require.Equal(t, []string{"1.2.0"}, r.Skipped)
	ready, _, _ = r.candidate("1.0.0", 24*time.Hour, now)
	require.Equal(t, "1.1.0", ready, "a rolled back release is never installed")
	ready, waiting, _ = r.candidate("1.3.0", 24*time.Hour, now)
	require.Empty(t, ready, "nothing newer than the running version")
	require.Empty(t, waiting)

	for i := range 15 {
		r.saw("2.0."+string(rune('a'+i)), now)
	}
	require.Len(t, r.Seen, autoKeepSeen)
}

func TestAutoUpdateStartGuard(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, AutoUpdateStartGuard(root), "nothing pending")
	bin := filepath.Join(root, config.BinaryPath)
	prev := filepath.Join(root, config.PrevBinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(prev), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!new"), 0o755))  // #nosec G306 -- test binary
	require.NoError(t, os.WriteFile(prev, []byte("#!old"), 0o755)) // #nosec G306 -- test binary
	path := filepath.Join(root, AutoPendingPath)

	// Pending for another version: not counted.
	require.NoError(t, writeAutoPending(path, autoPending{From: version.Version, To: "9.9.9"}))
	require.NoError(t, AutoUpdateStartGuard(root))
	p, ok, err := readAutoPending(path)
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, p.Starts)

	require.NoError(t, writeAutoPending(path, autoPending{From: "0.9.0", To: version.Version}))
	for i := 1; i <= autoMaxStarts; i++ {
		require.NoError(t, AutoUpdateStartGuard(root))
		p, _, _ = readAutoPending(path)
		require.Equal(t, i, p.Starts)
	}
	err = AutoUpdateStartGuard(root)
	require.Equal(t, deyerr.S003, codeOf(err))
	data, _ := os.ReadFile(bin)
	require.Equal(t, "#!old", string(data), "the previous binary is back")
	p, _, _ = readAutoPending(path)
	require.Contains(t, p.Failed, "did not start")
	require.NoError(t, AutoUpdateStartGuard(root), "a failed record is left to the hub")

	// A damaged record is dropped.
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	require.NoError(t, AutoUpdateStartGuard(root))
	require.NoFileExists(t, path)
}

// autoClock makes the local hour of the hub 04:30 now.
func autoClock(o *Options, _ string) {
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	offset := int((4*time.Hour + 30*time.Minute - now.Sub(day)) / time.Second)
	o.Location = time.FixedZone("test", offset)
	o.AutoUpdateHour = 4
}

func TestAutoUpdateInstalls(t *testing.T) {
	rs := newReleaseServer(t)
	rs.release(t, "1.2.3", []byte("#!deyroute 1.2.3"), false)
	env, o := prepareEnv(t, nil, releaseOpts(rs), autoClock)
	bin := filepath.Join(env.root, config.BinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!deyroute old"), 0o755)) // #nosec G306 -- test binary
	env.startEnv(o)
	ctx := ctxT(t)

	// Seen just now: it waits a day.
	env.h.autoUpdateTick(ctx)
	rec := env.h.autoRecord()
	require.Contains(t, rec.Seen, "1.2.3")
	require.Empty(t, rec.LastDay)
	info, err := env.client.UpdateAuto(ctx, "")
	require.NoError(t, err)
	require.True(t, info.Enabled, "on by default")
	require.Equal(t, 4, info.Hour)
	require.Equal(t, 24, info.MinAgeHours)
	require.Equal(t, "1.2.3", info.Next)
	require.True(t, info.NextAt.After(time.Now().Add(23*time.Hour)))
	st, err := env.client.Status(ctx)
	require.NoError(t, err)
	for _, w := range st.Warnings {
		require.NotContains(t, w.Message, "is available", "the automatic update installs it")
	}

	// Off: nothing is installed and the dashboard names the release.
	info, err = env.client.UpdateAuto(ctx, "off")
	require.NoError(t, err)
	require.False(t, info.Enabled)
	env.h.editAutoRecord(func(r *autoUpdateRecord) { r.Seen["1.2.3"] = time.Now().Add(-25 * time.Hour) })
	env.h.autoUpdateTick(ctx)
	data, _ := os.ReadFile(bin)
	require.Equal(t, "#!deyroute old", string(data))
	st, err = env.client.Status(ctx)
	require.NoError(t, err)
	var announced bool
	for _, w := range st.Warnings {
		announced = announced || strings.Contains(w.Message, "deyroute 1.2.3 is available")
	}
	require.True(t, announced)
	_, err = env.client.UpdateAuto(ctx, "maybe")
	require.Equal(t, deyerr.C013, codeOf(err))

	// On, a day old, in the update hour: installed once that day.
	_, err = env.client.UpdateAuto(ctx, "on")
	require.NoError(t, err)
	env.h.autoUpdateTick(ctx)
	data, _ = os.ReadFile(bin)
	require.Equal(t, "#!deyroute 1.2.3", string(data))
	p, ok, err := readAutoPending(env.h.path(AutoPendingPath))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, version.Version, p.From)
	require.Equal(t, "1.2.3", p.To)
	ev := env.waitEvent(state.EvUpdateApplied, "")
	require.Contains(t, ev.Message, "updated automatically")
	require.Eventually(t, func() bool {
		return env.runner.Called("systemctl restart --no-block deyroute-hub.service")
	}, testWait, 10*time.Millisecond)
	info, err = env.client.UpdateAuto(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "1.2.3", info.Pending)

	// The owner's own update ends the pending one.
	rs.release(t, "1.2.4", []byte("#!deyroute 1.2.4"), false)
	_, err = env.client.UpdateApply(ctx, "1.2.4", nil)
	require.NoError(t, err)
	require.NoFileExists(t, env.h.path(AutoPendingPath))
}

// pendingEnv starts a hub that finds p in AutoPendingPath.
func pendingEnv(t *testing.T, p autoPending) *testEnv {
	t.Helper()
	env, o := prepareEnv(t, nil, func(o *Options, _ string) {
		o.AutoUpdateSettle = 10 * time.Millisecond
		o.AutoUpdateVerify = 200 * time.Millisecond
	})
	require.NoError(t, writeAutoPending(filepath.Join(env.root, AutoPendingPath), p))
	bin := filepath.Join(env.root, config.BinaryPath)
	prev := filepath.Join(env.root, config.PrevBinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(prev), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!new"), 0o755))  // #nosec G306 -- test binary
	require.NoError(t, os.WriteFile(prev, []byte("#!old"), 0o755)) // #nosec G306 -- test binary
	return env.startEnv(o)
}

func TestAutoUpdateVerified(t *testing.T) {
	env := pendingEnv(t, autoPending{From: "0.9.0", To: version.Version})
	ev := env.waitEvent(state.EvUpdateApplied, "")
	require.Contains(t, ev.Message, "verified")
	require.Eventually(t, func() bool {
		_, err := os.Stat(env.h.path(AutoPendingPath))
		return os.IsNotExist(err)
	}, testWait, 10*time.Millisecond)
	require.Contains(t, env.h.autoRecord().Last, "updated 0.9.0 → "+version.Version)
}

func TestAutoUpdateRollsBack(t *testing.T) {
	env := pendingEnv(t, autoPending{From: "0.9.0", To: version.Version, Tunnels: []string{"ghost"}})
	require.Eventually(t, func() bool {
		return env.runner.Called("systemctl restart --no-block deyroute-hub.service")
	}, testWait, 10*time.Millisecond)
	data, _ := os.ReadFile(filepath.Join(env.root, config.BinaryPath))
	require.Equal(t, "#!old", string(data))
	p, ok, err := readAutoPending(env.h.path(AutoPendingPath))
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, p.Failed, "tunnel ghost")
}

func TestAutoUpdateRecordsRollback(t *testing.T) {
	env := pendingEnv(t, autoPending{From: version.Version, To: "9.9.9", Failed: "deyroute 9.9.9 did not start (2 attempts)"})
	ev := env.waitEvent(state.EvUpdateRolledBack, "")
	require.Equal(t, string(deyerr.S003), ev.Code)
	require.Contains(t, ev.Message, "9.9.9 is not installed automatically")
	require.NoFileExists(t, env.h.path(AutoPendingPath))
	rec := env.h.autoRecord()
	require.Equal(t, []string{"9.9.9"}, rec.Skipped)
	st, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	var warned bool
	for _, w := range st.Warnings {
		warned = warned || strings.Contains(w.Message, "rolled back")
	}
	require.True(t, warned)
}
