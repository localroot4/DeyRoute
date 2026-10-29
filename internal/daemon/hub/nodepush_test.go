package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// quiet is how long a test waits to see that no self.update is sent.
const quiet = 600 * time.Millisecond

// selfUpdates makes n answer self.update with err (nil = accepted) and
// returns the versions it was asked to install.
func selfUpdates(n *fakeNode, err error) chan string {
	got := make(chan string, 16)
	n.on(api.CmdSelfUpdate, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SelfUpdateArgs
		decode(n.t, cmd, &a)
		got <- a.Version
		return nil, err
	})
	return got
}

// nextUpdate waits for one self.update and returns its version.
func nextUpdate(t *testing.T, got chan string) string {
	t.Helper()
	select {
	case v := <-got:
		return v
	case <-time.After(testWait):
		t.Fatal("no self.update")
		return ""
	}
}

// noUpdate requires that no self.update arrives for a while.
func noUpdate(t *testing.T, got chan string) {
	t.Helper()
	select {
	case v := <-got:
		t.Fatalf("unexpected self.update to %s", v)
	case <-time.After(quiet):
	}
}

// hasNodeUpdateRecord reports whether a node update is pending.
func hasNodeUpdateRecord(t *testing.T, env *testEnv) bool {
	t.Helper()
	var rec nodeUpdateRecord
	ok, err := env.h.st.GetMeta(metaNodeUpdate, &rec)
	require.NoError(t, err)
	return ok
}

// A node of another major.minor gets the hub's version without a prior
// `deyroute update` (DR-11), an older one and a newer one (a downgrade,
// logged at warn). A development build pushes nothing.
func TestIncompatibleNodeQueuesSelfUpdateWithoutRecord(t *testing.T) {
	withVersion(t, "0.4.0")
	logger, logs := newFrontLog()
	env := startHub(t, nil, func(o *Options, _ string) { o.Logger = logger })
	require.False(t, hasNodeUpdateRecord(t, env))
	n := env.joinNode("de-1")
	n.version = "0.3.2"
	got := selfUpdates(n, nil)
	n.start()
	require.Equal(t, "0.4.0", nextUpdate(t, got))
	require.True(t, hasNodeUpdateRecord(t, env), "the push is recorded like an update")
	require.Contains(t, logs.String(), "runs another major.minor")

	// A newer node is pushed back to the hub's version, at warn level.
	logger2, logs2 := newFrontLog()
	env2 := startHub(t, nil, func(o *Options, _ string) { o.Logger = logger2 })
	n2 := env2.joinNode("nl-1")
	n2.version = "0.5.1"
	got2 := selfUpdates(n2, nil)
	n2.start()
	require.Equal(t, "0.4.0", nextUpdate(t, got2))
	require.Eventually(t, func() bool {
		return strings.Contains(logs2.String(), "level=WARN msg=\"node runs a newer version than the hub")
	}, testWait, 10*time.Millisecond, logs2.String())
}

// A development build has no signed release: it never pushes its binary
// to a release node on its own, and says so once.
func TestDevHubPushesNothingWithoutRecord(t *testing.T) {
	withVersion(t, "dev")
	logger, logs := newFrontLog()
	env := startHub(t, nil, func(o *Options, _ string) { o.Logger = logger })
	n := env.joinNode("de-1")
	n.version = "0.4.0"
	got := selfUpdates(n, nil)
	n.start()
	env.waitOnline("de-1", true)
	noUpdate(t, got)
	require.False(t, hasNodeUpdateRecord(t, env))
	n.stop()
	env.waitOnline("de-1", false)
	n.start()
	env.waitOnline("de-1", true)
	noUpdate(t, got)
	require.Equal(t, 1, strings.Count(logs.String(), "development build"), logs.String())
}

// Same major.minor and no pending update: nothing is forced (unchanged).
func TestCompatibleOlderNodeNotForcedWithoutRecord(t *testing.T) {
	withVersion(t, "0.4.1")
	env := startHub(t, nil)
	n := env.joinNode("de-1")
	n.version = "0.4.0"
	got := selfUpdates(n, nil)
	n.start()
	env.waitOnline("de-1", true)
	noUpdate(t, got)
	require.False(t, hasNodeUpdateRecord(t, env))
	ns, _ := env.h.nodeState("de-1")
	require.True(t, ns.Compatible)
}

// A node that refuses the hub's version (for example DEY-S012 or DEY-S001
// of a signed-update node) is not asked again on every reconnect: the
// next try waits, longer after each refusal, and the refusal is logged at
// warn once.
func TestRefusedNodeUpdateBacksOff(t *testing.T) {
	withVersion(t, "0.4.0")
	var skew atomic.Int64
	logger, logs := newFrontLog()
	env := startHub(t, nil, func(o *Options, _ string) {
		o.Logger = logger
		o.Now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	})
	n := env.joinNode("de-1")
	n.version = "0.3.0"
	got := selfUpdates(n, deyerr.New(deyerr.S012, deyerr.Params{"version": "0.4.0"}))
	n.start()
	require.Equal(t, "0.4.0", nextUpdate(t, got))
	reconnect := func() {
		n.stop()
		env.waitOnline("de-1", false)
		n.start()
		env.waitOnline("de-1", true)
	}
	reconnect()
	noUpdate(t, got)
	// After the wait it is tried again.
	skew.Store(int64(nodePushRetryFirst + time.Second))
	reconnect()
	require.Equal(t, "0.4.0", nextUpdate(t, got))
	// The second wait is longer than the first.
	skew.Store(int64(2*nodePushRetryFirst + 2*time.Second))
	reconnect()
	noUpdate(t, got)
	require.Equal(t, 1, strings.Count(logs.String(), "level=WARN msg=\"node could not install the hub's version"), logs.String())
	// Once the node runs the hub's version, a later difference starts
	// afresh.
	n.version = "0.4.0"
	reconnect()
	require.Eventually(t, func() bool {
		env.h.ops.pushMu.Lock()
		defer env.h.ops.pushMu.Unlock()
		_, tracked := env.h.ops.pushTries["de-1"]
		return !tracked
	}, testWait, 10*time.Millisecond, "the failures are forgotten once the node runs the hub's version")
}

// `deyroute update` on a hub that runs the newest release installs
// nothing, but every known node of another version is told to follow
// (also a node of the same major.minor), and the step says how many.
func TestUpdateApplyNewestStillMarksStaleNodes(t *testing.T) {
	withVersion(t, "1.2.3")
	rs := newReleaseServer(t)
	rs.release(t, "1.2.3", []byte("#!deyroute 1.2.3"), false)
	env := startHub(t, nil, releaseOpts(rs))
	n := env.joinNode("de-1")
	n.version = "1.2.0"
	got := selfUpdates(n, nil)
	n.start()
	env.waitOnline("de-1", true)
	noUpdate(t, got)

	var log stepLog
	info, err := env.client.UpdateApply(ctxT(t), "", log.add)
	require.NoError(t, err)
	require.Equal(t, "1.2.3", info.Latest)
	require.Equal(t, "1.2.3", info.Current)
	require.Equal(t, []string{"resolve:ok", "download:skipped", "nodes:ok"}, log.finished())
	require.Contains(t, log.last().Detail, "1 node runs another version")
	require.NoFileExists(t, filepath.Join(env.root, config.BinaryPath), "nothing is installed")
	require.True(t, hasNodeUpdateRecord(t, env))
	require.Equal(t, "1.2.3", nextUpdate(t, got))

	// The CLI's path when nothing is newer: UpdateNodes installs nothing
	// and reports the nodes it queued.
	queued, err := env.client.UpdateNodes(ctxT(t))
	require.NoError(t, err)
	require.Equal(t, 1, queued)
	require.Equal(t, "1.2.3", nextUpdate(t, got))
	// Every node on the hub's version: nothing to do, no record.
	n.stop()
	env.waitOnline("de-1", false)
	n.version = "1.2.3"
	n.start()
	env.waitOnline("de-1", true)
	require.Eventually(t, func() bool { return !hasNodeUpdateRecord(t, env) }, testWait, 10*time.Millisecond)
	log = stepLog{}
	_, err = env.client.UpdateApply(ctxT(t), "", log.add)
	require.NoError(t, err)
	require.Equal(t, []string{"resolve:ok", "download:skipped"}, log.finished())
	queued, err = env.client.UpdateNodes(ctxT(t))
	require.NoError(t, err)
	require.Zero(t, queued)
	require.False(t, hasNodeUpdateRecord(t, env))
}

// A restored hub reads restored-from once at start: a backup of another
// version marks every node for the hub's version (also a node of the same
// major.minor); the file is deleted. The same version, a symlink or a
// development build mark nothing.
func TestRestoreMarkerMarksNodes(t *testing.T) {
	withVersion(t, "0.4.0")
	writeMarker := func(root, content string) string {
		p := filepath.Join(root, setup.RestoredFromPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		return p
	}

	var envs []*testEnv
	env, o := prepareEnv(t, nil)
	p := writeMarker(env.root, "0.3.4\n")
	envs = append(envs, env.startEnv(o))
	require.NoFileExists(t, p)
	require.True(t, hasNodeUpdateRecord(t, env))
	n := env.joinNode("de-1")
	n.version = "0.4.0-edge.3"
	got := selfUpdates(n, nil)
	n.start()
	require.Equal(t, "0.4.0", nextUpdate(t, got))
	n.stop()

	// Restored from the same version: nothing to push.
	env, o = prepareEnv(t, nil)
	p = writeMarker(env.root, "0.4.0\n")
	envs = append(envs, env.startEnv(o))
	require.NoFileExists(t, p)
	require.False(t, hasNodeUpdateRecord(t, env))

	// A symlink is not followed, only removed.
	env, o = prepareEnv(t, nil)
	target := filepath.Join(t.TempDir(), "version")
	require.NoError(t, os.WriteFile(target, []byte("0.1.0\n"), 0o600))
	p = filepath.Join(env.root, setup.RestoredFromPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.Symlink(target, p))
	envs = append(envs, env.startEnv(o))
	_, err := os.Lstat(p)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.FileExists(t, target)
	require.False(t, hasNodeUpdateRecord(t, env))

	// A development build pushes nothing (the hubs above stop first: they
	// read the version).
	for _, e := range envs {
		e.stop()
	}
	withVersion(t, "dev")
	env, o = prepareEnv(t, nil)
	p = writeMarker(env.root, "0.3.4\n")
	env.startEnv(o)
	require.NoFileExists(t, p)
	require.False(t, hasNodeUpdateRecord(t, env))
}
