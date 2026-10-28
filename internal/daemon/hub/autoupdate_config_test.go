package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

// The automatic update is off by default and its switch is hub.auto_update
// in config.yaml (DR-05).

// autoNotice is the dashboard line and event message of a hub that ran a
// build where the automatic update was on by default.
const autoNotice = "automatic update is now off by default; turn it on with: deyroute update auto on"

// autoEnv prepares a hub at 04:30 local time whose release source serves
// 1.2.3 and whose installed binary is "#!deyroute old"; it returns the path
// of that binary.
func autoEnv(t *testing.T, edit func(c *config.Config)) (*testEnv, Options, string) {
	t.Helper()
	rs := newReleaseServer(t)
	rs.release(t, "1.2.3", []byte("#!deyroute 1.2.3"), false)
	env, o := prepareEnv(t, edit, releaseOpts(rs), autoClock)
	bin := filepath.Join(env.root, config.BinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!deyroute old"), 0o755)) // #nosec G306 -- test binary
	return env, o, bin
}

// seedAutoRecord writes raw as metaAutoUpdate into the state.db of root
// before the hub starts, as an earlier build left it.
func seedAutoRecord(t *testing.T, root, raw string) {
	t.Helper()
	path := filepath.Join(root, config.StatePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	st, err := state.Open(path)
	require.NoError(t, err)
	require.NoError(t, st.PutMeta(metaAutoUpdate, json.RawMessage(raw)))
	require.NoError(t, st.Close())
}

// restartEnv stops env and starts a new hub with o on the same root.
func restartEnv(env *testEnv, o Options) *testEnv {
	env.stop()
	o.OnReady = nil
	next := &testEnv{t: env.t, root: env.root, sock: env.sock, runner: env.runner, ca: env.ca}
	return next.startEnv(o)
}

// hasWarning reports whether the dashboard of env shows a line with text.
func hasWarning(t *testing.T, env *testEnv, text string) bool {
	t.Helper()
	st, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	for _, w := range st.Warnings {
		if strings.Contains(w.Message, text) {
			return true
		}
	}
	return false
}

func readConfigFile(t *testing.T, env *testEnv) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(env.root, config.DefaultPath))
	require.NoError(t, err)
	return string(data)
}

// TestAutoUpdateDefaultOff: a fresh hub installs nothing by itself, even in
// the update hour with a release this hub first saw more than a day ago
// (the minimum age counts from that sighting); config.yaml has no key.
func TestAutoUpdateDefaultOff(t *testing.T) {
	env, o, bin := autoEnv(t, nil)
	env.startEnv(o)
	ctx := ctxT(t)
	env.h.editAutoRecord(func(r *autoUpdateRecord) {
		r.saw("1.2.3", time.Now().Add(-25*time.Hour))
		r.CheckedAt = time.Now()
	})
	env.h.autoUpdateTick(ctx)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!deyroute old", string(data), "nothing is installed")
	require.NoFileExists(t, env.h.path(AutoPendingPath))
	require.Empty(t, env.h.autoRecord().LastDay, "no attempt")

	info, err := env.client.UpdateAuto(ctx, "")
	require.NoError(t, err)
	require.False(t, info.Enabled)
	require.Equal(t, "config", info.Source)
	require.NotContains(t, readConfigFile(t, env), "auto_update", "false is written by omitting the key")
	require.Empty(t, env.h.Config().NewerKeysInUse(), "a default install never blocks a rollback")
}

// TestAutoUpdateOnFromConfig: hub.auto_update: true turns it on; update
// auto on|off edits config.yaml after an automatic backup.
func TestAutoUpdateOnFromConfig(t *testing.T) {
	env, o, bin := autoEnv(t, func(c *config.Config) { c.Hub.AutoUpdate = true })
	env.startEnv(o)
	ctx := ctxT(t)
	info, err := env.client.UpdateAuto(ctx, "")
	require.NoError(t, err)
	require.True(t, info.Enabled)
	require.Equal(t, "config", info.Source)

	env.h.editAutoRecord(func(r *autoUpdateRecord) {
		r.saw("1.2.3", time.Now().Add(-25*time.Hour))
		r.CheckedAt = time.Now()
	})
	env.h.autoUpdateTick(ctx)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!deyroute 1.2.3", string(data))
	require.Eventually(t, func() bool {
		return env.runner.Called("systemctl restart --no-block deyroute-hub.service")
	}, testWait, 10*time.Millisecond)

	before, err := install.AutoBackups(env.root)
	require.NoError(t, err)
	info, err = env.client.UpdateAuto(ctx, "off")
	require.NoError(t, err)
	require.False(t, info.Enabled)
	require.False(t, env.h.Config().Hub.AutoUpdate)
	require.NotContains(t, readConfigFile(t, env), "auto_update")
	after, err := install.AutoBackups(env.root)
	require.NoError(t, err)
	require.Len(t, after, len(before)+1, "an automatic backup before the change")
	info, err = env.client.UpdateAuto(ctx, "on")
	require.NoError(t, err)
	require.True(t, info.Enabled)
	require.Contains(t, readConfigFile(t, env), "auto_update: true")
	again, err := install.AutoBackups(env.root)
	require.NoError(t, err)
	_, err = env.client.UpdateAuto(ctx, "on")
	require.NoError(t, err)
	unchanged, err := install.AutoBackups(env.root)
	require.NoError(t, err)
	require.Len(t, unchanged, len(again), "no change, no backup")
}

// TestAutoUpdateLegacyRecordIgnored: the switch earlier builds kept in
// state.db is ignored: a record that says on ({"disabled": false}) leaves
// it off without the config key, a record that says off does not override
// the key, and the next write of the record drops the field.
func TestAutoUpdateLegacyRecordIgnored(t *testing.T) {
	env, o, bin := autoEnv(t, nil)
	seen := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	seedAutoRecord(t, env.root, `{"disabled":false,"checked_at":"`+now+`","seen":{"1.2.3":"`+seen+`"}}`)
	env.startEnv(o)
	ctx := ctxT(t)
	info, err := env.client.UpdateAuto(ctx, "")
	require.NoError(t, err)
	require.False(t, info.Enabled)
	env.h.autoUpdateTick(ctx)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!deyroute old", string(data))

	env.h.editAutoRecord(func(r *autoUpdateRecord) { r.CheckedAt = time.Now() })
	var raw json.RawMessage
	ok, err := env.h.st.GetMeta(metaAutoUpdate, &raw)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotContains(t, string(raw), "disabled")
	require.Contains(t, string(raw), "1.2.3", "the runtime fields stay")

	on, o2, _ := autoEnv(t, func(c *config.Config) { c.Hub.AutoUpdate = true })
	seedAutoRecord(t, on.root, `{"disabled":true}`)
	on.startEnv(o2)
	info, err = on.client.UpdateAuto(ctx, "")
	require.NoError(t, err)
	require.True(t, info.Enabled, "config.yaml decides")
}

// TestAutoUpdateNoticeOnlyOnUpgrade: a hub that ran a build with the
// automatic update on by default (its record is in state.db) is told once
// that it is off now: one event auto_update_default_off and a dashboard
// line for 7 days (not while it is turned on). A fresh install gets
// neither, also after it wrote a record itself, and later starts add
// nothing.
func TestAutoUpdateNoticeOnlyOnUpgrade(t *testing.T) {
	const event = "auto_update_default_off"
	env, o := prepareEnv(t, nil)
	seedAutoRecord(t, env.root, `{"checked_at":"2026-10-01T00:00:00Z","seen":{"1.0.0":"2026-10-01T00:00:00Z"}}`)
	env.startEnv(o)
	ev := env.waitEvent(event, "")
	require.Equal(t, state.LevelWarn, ev.Level)
	require.Equal(t, autoNotice, ev.Message)
	require.True(t, hasWarning(t, env, autoNotice))
	_, err := env.client.UpdateAuto(ctxT(t), "on")
	require.NoError(t, err)
	require.False(t, hasWarning(t, env, autoNotice), "it is on: nothing to say")
	_, err = env.client.UpdateAuto(ctxT(t), "off")
	require.NoError(t, err)

	env = restartEnv(env, o)
	require.Equal(t, 1, env.countEvents(event), "once")
	require.True(t, hasWarning(t, env, autoNotice), "7 days from the first start")
	o.Now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	env = restartEnv(env, o)
	require.Equal(t, 1, env.countEvents(event))
	require.False(t, hasWarning(t, env, autoNotice), "after 7 days")

	fresh, fo := prepareEnv(t, nil)
	fresh.startEnv(fo)
	require.Zero(t, fresh.countEvents(event))
	require.False(t, hasWarning(t, fresh, autoNotice))
	fresh.h.editAutoRecord(func(r *autoUpdateRecord) { r.CheckedAt = time.Now() })
	fresh = restartEnv(fresh, fo)
	require.Zero(t, fresh.countEvents(event), "a record this build wrote is not a legacy one")
	require.False(t, hasWarning(t, fresh, autoNotice))
}

// TestAutoUpdateSurvivesRestore: the switch is in config.yaml, so a backup
// carries it: restored on a new hub (empty state.db) it is on when it was
// on, and off when the key was absent.
func TestAutoUpdateSurvivesRestore(t *testing.T) {
	for _, on := range []bool{true, false} {
		src := startHub(t, nil)
		_, err := src.client.UpdateAuto(ctxT(t), map[bool]string{true: "on", false: "off"}[on])
		require.NoError(t, err)
		backup, err := install.Backup(install.BackupOptions{Root: src.root, NoEncrypt: true,
			OutPath: filepath.Join(t.TempDir(), "hub.tar.gz")})
		require.NoError(t, err)

		dst, o := prepareEnv(t, nil)
		_, err = install.Restore(install.RestoreOptions{Root: dst.root, Path: backup})
		require.NoError(t, err)
		dst.startEnv(o)
		info, err := dst.client.UpdateAuto(ctxT(t), "")
		require.NoError(t, err)
		require.Equal(t, on, info.Enabled, "restored with the key %v", on)
		require.Equal(t, on, strings.Contains(readConfigFile(t, dst), "auto_update: true"))
	}
}

// TestAutoRollbackKnowsKeysOfPreviousBinary: the previous binary had loaded
// config.yaml with the keys in use when it started the automatic update, so
// it knows them even when its version sorts before their first release
// (hub.auto_update is always set then, and every 0.3.0-edge.N sorts before
// 0.3.0): a failed update is rolled back to it. Only a key set after the
// update, or every key when the record lists none (an earlier build wrote
// it), keeps the new binary.
func TestAutoRollbackKnowsKeysOfPreviousBinary(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old }) // after the hub stopped
	version.Version = "0.3.0-edge.61"
	env, o, bin := autoEnv(t, func(c *config.Config) { c.Hub.AutoUpdate = true })
	// A stale record of other versions: the start drops it, which shows
	// that the start is done with the record before this test writes one.
	path := filepath.Join(env.root, AutoPendingPath)
	require.NoError(t, writeAutoPending(path, autoPending{From: "0.0.1", To: "0.0.2"}))
	env.startEnv(o)
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return os.IsNotExist(err)
	}, testWait, 10*time.Millisecond)
	binary := func() string {
		data, err := os.ReadFile(bin)
		require.NoError(t, err)
		return string(data)
	}

	env.h.autoApply(ctxT(t), "1.2.3")
	require.Equal(t, "#!deyroute 1.2.3", binary())
	p, ok, err := readAutoPending(path)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0.3.0-edge.61", p.From)
	env.h.autoRollback(path, p, "tunnel t1 did not come back")
	require.Equal(t, "#!deyroute old", binary(), "rolled back to the edge build")
	p, ok, err = readAutoPending(path)
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, p.Failed, "tunnel t1")
	require.NotContains(t, env.h.autoRecord().Last, "was kept")

	// A record of an earlier build lists no keys: kept, as before.
	require.NoError(t, os.WriteFile(path, []byte(`{"from":"0.3.0-edge.61","to":"1.2.3"}`), 0o600))
	p, _, err = readAutoPending(path)
	require.NoError(t, err)
	env.h.autoRollback(path, p, "tunnel t1 did not come back")
	require.Equal(t, "#!deyroute old", binary(), "nothing swapped")
	require.Contains(t, env.h.autoRecord().Last, "was kept")
	require.Contains(t, env.h.autoRecord().Last, "hub.auto_update (since 0.3.0)")

	// A key set after the update: only that key keeps the new binary.
	_, err = env.h.mutate(func(c *config.Config) error {
		c.Monitoring = &config.Monitoring{QuotaResetDay: 5}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"from":"0.3.0-edge.14","to":"1.2.3","keys":["hub.auto_update"]}`), 0o600))
	p, _, err = readAutoPending(path)
	require.NoError(t, err)
	env.h.autoRollback(path, p, "tunnel t1 did not come back")
	require.Equal(t, "#!deyroute old", binary(), "nothing swapped")
	last := env.h.autoRecord().Last
	require.Contains(t, last, "was kept")
	require.Contains(t, last, "monitoring (since 0.3.0-edge.15)")
	require.NotContains(t, last, "hub.auto_update", "the previous binary loaded it")
}
