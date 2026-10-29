package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
)

// Signed manifests for backend.install (DR-03): a node with signed
// updates installs a backend only when a signed manifest pins it, so the
// hub sends its signed manifest override, or the signed backends.yaml of
// its own release when the node's built-in one differs.

// manifestHash is the sha256 a node announces for its built-in manifest.
func manifestHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// installRecorder makes node n record the backend.install arguments it
// gets on the returned channel; it answers with an error so that every
// forced installNode sends the command again.
func installRecorder(t *testing.T, n *fakeNode) chan api.BackendInstallArgs {
	got := make(chan api.BackendInstallArgs, 8)
	n.on(api.CmdBackendInstall, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.BackendInstallArgs
		decode(t, cmd, &a)
		got <- a
		return nil, deyerr.New(deyerr.N050, deyerr.Params{"node": n.id, "command": cmd.Name, "reason": "recorded"})
	})
	return got
}

// sendInstall runs backend.install of e on node and returns what it got.
func sendInstall(t *testing.T, env *testEnv, node string, e backend.ManifestEntry, got chan api.BackendInstallArgs) api.BackendInstallArgs {
	t.Helper()
	err := env.h.installNode(ctxT(t), node, e, true)
	require.Equal(t, deyerr.N050, codeOf(err), "%v", err)
	select {
	case a := <-got:
		require.Equal(t, e, a.Entry)
		return a
	case <-time.After(testWait):
		t.Fatal("no backend.install")
	}
	return api.BackendInstallArgs{}
}

// builtinEntry is the entry of name in the built-in manifest.
func builtinEntry(t *testing.T, name string) backend.ManifestEntry {
	t.Helper()
	m, err := backend.ParseManifest(backend.EmbeddedManifest())
	require.NoError(t, err)
	e, ok := m.Backends[name]
	require.True(t, ok)
	return e
}

// `deyroute update manifest` keeps the signature next to the override
// (/etc/deyroute/backends.yaml.minisig), and the hub sends both with
// backend.install to nodes with signed updates (never to older nodes). An
// override an earlier build saved without its signature is not sent until
// the hub has a signature that verifies the bytes on disk: it fetches one
// when it starts, and logs that `deyroute update manifest` fixes it.
func TestHubStoresManifestSignature(t *testing.T) {
	orig, err := backend.ParseManifest(backend.EmbeddedManifest())
	require.NoError(t, err)
	t.Cleanup(func() { backend.SetManifest(orig) })
	rs := newReleaseServer(t)
	data := bytes.Replace(backend.EmbeddedManifest(), []byte("version: v0.7.2"), []byte("version: v0.7.9"), 1)
	sig := rs.sign(data)
	rs.put("/latest/"+install.ManifestFile, data)
	rs.put("/latest/"+install.ManifestSigFile, sig)
	logger, logs := newFrontLog()
	env, o := prepareEnv(t, nil, releaseOpts(rs), func(o *Options, _ string) { o.Logger = logger })
	env.startEnv(o)
	ctx := ctxT(t)

	_, err = env.client.UpdateManifest(ctx)
	require.NoError(t, err)
	overridePath := filepath.Join(env.root, config.ManifestPath)
	sigPath := filepath.Join(env.root, filepath.Dir(config.ManifestPath), install.ManifestSigFile)
	written, err := os.ReadFile(overridePath)
	require.NoError(t, err)
	require.Equal(t, data, written)
	writtenSig, err := os.ReadFile(sigPath)
	require.NoError(t, err, "the signature is kept next to the override")
	require.Equal(t, sig, writtenSig)

	n := env.joinNode("de-1")
	n.features = []string{api.FeatureSignedUpdate}
	n.manifest = manifestHash(backend.EmbeddedManifest())
	got := installRecorder(t, n)
	old := env.joinNode("nl-1")
	gotOld := installRecorder(t, old)
	n.start()
	old.start()
	env.waitOnline("de-1", true)
	env.waitOnline("nl-1", true)
	entry := backend.ManifestFor("backhaul")
	require.Equal(t, "v0.7.9", entry.Version)
	a := sendInstall(t, env, "de-1", entry, got)
	require.Equal(t, data, a.Manifest)
	require.Equal(t, sig, a.ManifestSig)
	a = sendInstall(t, env, "nl-1", entry, gotOld)
	require.Nil(t, a.Manifest, "an older node gets no manifest")
	require.Nil(t, a.ManifestSig)

	// An earlier build saved the override without its signature, and the
	// newest release has another manifest, so none of the signatures the
	// hub can download verifies it: the override is not sent.
	restart := func() {
		t.Helper()
		n.stop()
		old.stop()
		env = restartEnv(env, o)
		n.addr = env.h.ControlAddr().String()
		n.start()
		env.waitOnline("de-1", true)
	}
	require.NoError(t, os.Remove(sigPath))
	other := []byte("manifest_version: 1\nbackends: {}\n")
	rs.put("/latest/"+install.ManifestFile, other)
	rs.put("/latest/"+install.ManifestSigFile, rs.sign(other))
	restart()
	a = sendInstall(t, env, "de-1", entry, got)
	require.Nil(t, a.Manifest, "an override without its signature is not sent")
	require.Nil(t, a.ManifestSig)
	require.NoFileExists(t, sigPath)
	require.Eventually(t, func() bool {
		return bytes.Contains([]byte(logs.String()), []byte("deyroute update manifest"))
	}, testWait, 10*time.Millisecond, "the warning names the fix")
	require.Equal(t, "v0.7.9", backend.ManifestFor("backhaul").Version, "the hub itself still uses the override")

	// A signature on disk that does not verify the override counts as none.
	require.NoError(t, os.WriteFile(sigPath, rs.sign(other), 0o644)) // #nosec G306 -- test file
	restart()
	a = sendInstall(t, env, "de-1", entry, got)
	require.Nil(t, a.Manifest, "a signature that does not verify the override is not sent")

	// Once a source serves a signature of these bytes, the hub fetches it
	// when it starts, keeps it and sends the override again.
	require.NoError(t, os.Remove(sigPath))
	rs.put("/latest/"+install.ManifestSigFile, sig)
	restart()
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(sigPath)
		return err == nil && bytes.Equal(b, sig)
	}, testWait, 10*time.Millisecond, "the signature is fetched and kept")
	a = sendInstall(t, env, "de-1", entry, got)
	require.Equal(t, data, a.Manifest)
	require.Equal(t, sig, a.ManifestSig)
}

// A node of another build (its built-in backends.yaml differs from the
// hub's) gets the signed backends.yaml of the hub's release with
// backend.install, fetched on demand into the release cache, and that
// manifest pins the hub's entry for it. A node of the same build and an
// older node get no manifest.
func TestHubSendsReleaseManifestForOtherBuild(t *testing.T) {
	withVersion(t, "1.2.3")
	rs := newReleaseServer(t)
	rel := publishSigned(t, rs, "1.2.3", "amd64")
	env := startHub(t, nil, releaseOpts(rs))
	dir := filepath.Join(env.root, AssetCacheDir, "1.2.3")
	require.NoFileExists(t, filepath.Join(dir, install.ManifestFile))

	// The node's build pins another backhaul.
	nodeManifest := bytes.Replace(backend.EmbeddedManifest(), []byte("version: v0.7.2"), []byte("version: v0.7.1"), 1)
	require.NotEqual(t, backend.EmbeddedManifest(), nodeManifest)
	other := env.joinNode("de-1")
	other.features = []string{api.FeatureTuneAuto, api.FeatureSignedUpdate}
	other.manifest = manifestHash(nodeManifest)
	gotOther := installRecorder(t, other)
	same := env.joinNode("nl-1")
	same.features = []string{api.FeatureSignedUpdate}
	same.manifest = manifestHash(backend.EmbeddedManifest())
	gotSame := installRecorder(t, same)
	old := env.joinNode("fr-1")
	gotOld := installRecorder(t, old)
	for _, n := range []*fakeNode{other, same, old} {
		n.start()
		env.waitOnline(n.id, true)
	}

	entry := builtinEntry(t, "backhaul")
	_, err := install.CheckBackendEntry(entry, "amd64", nodeManifest, nil, nil, rs.pub.String())
	require.Error(t, err, "the node's own manifest does not pin the hub's entry")
	a := sendInstall(t, env, "de-1", entry, gotOther)
	require.Equal(t, rel.manifest, a.Manifest)
	require.Equal(t, rel.manifSig, a.ManifestSig)
	src, err := install.CheckBackendEntry(a.Entry, "amd64", nodeManifest, a.Manifest, a.ManifestSig, rs.pub.String())
	require.NoError(t, err, "the node accepts the entry")
	require.Equal(t, install.PinnedSigned, src)
	for name, want := range map[string][]byte{install.ManifestFile: rel.manifest, install.ManifestSigFile: rel.manifSig} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, name)
		require.Equal(t, want, data, name)
	}

	a = sendInstall(t, env, "nl-1", entry, gotSame)
	require.Nil(t, a.Manifest, "the same build pins the same entries")
	a = sendInstall(t, env, "fr-1", entry, gotOld)
	require.Nil(t, a.Manifest, "an older node gets no manifest")
}

// The hub keeps trying, every ManifestRetry, to download a signature for
// an override that has none (no restart needed) and the signed manifest
// of its release after a failed download. A development build has no
// release manifest to send.
func TestHubRetriesManifestDownloads(t *testing.T) {
	orig, err := backend.ParseManifest(backend.EmbeddedManifest())
	require.NoError(t, err)
	t.Cleanup(func() { backend.SetManifest(orig) })
	retry := func(o *Options, _ string) { o.ManifestRetry = 20 * time.Millisecond }

	// An override without its signature; no source signs it yet.
	rs := newReleaseServer(t)
	data := bytes.Replace(backend.EmbeddedManifest(), []byte("version: v0.7.2"), []byte("version: v0.7.9"), 1)
	env, o := prepareEnv(t, nil, releaseOpts(rs), retry)
	overridePath := filepath.Join(env.root, config.ManifestPath)
	sigPath := filepath.Join(env.root, config.ManifestSigPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(overridePath), 0o700))
	require.NoError(t, os.WriteFile(overridePath, data, 0o644)) // #nosec G306 -- test file
	env.startEnv(o)
	n := env.joinNode("de-1")
	n.features = []string{api.FeatureSignedUpdate}
	n.manifest = manifestHash(backend.EmbeddedManifest())
	got := installRecorder(t, n)
	n.start()
	env.waitOnline("de-1", true)
	entry := backend.ManifestFor("backhaul")
	require.Equal(t, "v0.7.9", entry.Version)
	require.Nil(t, sendInstall(t, env, "de-1", entry, got).Manifest)
	sig := rs.sign(data)
	rs.put("/latest/"+install.ManifestSigFile, sig)
	require.Eventually(t, func() bool {
		b, err := os.ReadFile(sigPath)
		return err == nil && bytes.Equal(b, sig)
	}, testWait, 10*time.Millisecond, "the signature is downloaded without a restart")
	a := sendInstall(t, env, "de-1", entry, got)
	require.Equal(t, data, a.Manifest)
	require.Equal(t, sig, a.ManifestSig)
	n.stop()
	env.stop()
	backend.SetManifest(orig)

	// The release manifest of the hub's version is not published yet.
	withVersion(t, "1.2.3")
	rs2 := newReleaseServer(t)
	env2 := startHub(t, nil, releaseOpts(rs2), retry)
	m := env2.joinNode("de-2")
	m.features = []string{api.FeatureSignedUpdate}
	m.manifest = manifestHash([]byte("another build"))
	got2 := installRecorder(t, m)
	m.start()
	env2.waitOnline("de-2", true)
	e := builtinEntry(t, "backhaul")
	require.Nil(t, sendInstall(t, env2, "de-2", e, got2).Manifest)
	rel := publishSigned(t, rs2, "1.2.3", "amd64")
	require.Eventually(t, func() bool {
		data, _ := env2.h.nodeManifest(context.Background(), "de-2")
		return data != nil
	}, testWait, 30*time.Millisecond, "downloaded again after ManifestRetry")
	a = sendInstall(t, env2, "de-2", e, got2)
	require.Equal(t, rel.manifest, a.Manifest)
	require.Equal(t, rel.manifSig, a.ManifestSig)
	m.stop()
	env2.stop()

	// A development build has no release to send.
	withVersion(t, "dev")
	env3 := startHub(t, nil, releaseOpts(rs2), retry)
	d := env3.joinNode("de-3")
	d.features = []string{api.FeatureSignedUpdate}
	d.manifest = manifestHash([]byte("another build"))
	got3 := installRecorder(t, d)
	d.start()
	env3.waitOnline("de-3", true)
	require.Nil(t, sendInstall(t, env3, "de-3", e, got3).Manifest)
	require.NoDirExists(t, filepath.Join(env3.root, AssetCacheDir, "dev"))
}
