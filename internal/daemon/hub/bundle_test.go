package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/version"
)

// signedRelease is a release published on a releaseServer.
type signedRelease struct {
	sums, sig          []byte
	archives           map[string][]byte // arch → archive
	manifest, manifSig []byte
}

// publishSigned publishes deyroute ver for archs on rs (latest and v<ver>)
// as a release does: one signed SHA256SUMS for every archive, and the
// signed backends.yaml. Each archive holds "#!deyroute <ver> <arch>" in a
// directory, as edge builds pack it.
func publishSigned(t *testing.T, rs *releaseServer, ver string, archs ...string) signedRelease {
	t.Helper()
	r := signedRelease{archives: map[string][]byte{}}
	for _, arch := range archs {
		bin := []byte("#!deyroute " + ver + " " + arch)
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		dir := "deyroute_" + ver + "_linux_" + arch + "/"
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}))
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: dir + "deyroute", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(bin)
		require.NoError(t, err)
		require.NoError(t, tw.Close())
		require.NoError(t, gz.Close())
		r.archives[arch] = buf.Bytes()
		s := sha256.Sum256(buf.Bytes())
		r.sums = append(r.sums, []byte(hex.EncodeToString(s[:])+"  "+install.ArchiveName(ver, arch)+"\n")...)
	}
	r.sig = rs.sign(r.sums)
	r.manifest = backend.EmbeddedManifest()
	r.manifSig = rs.sign(r.manifest)
	for _, d := range []string{"/latest/", "/v" + ver + "/"} {
		rs.put(d+install.SumsFile, r.sums)
		rs.put(d+install.SumsSigFile, r.sig)
		rs.put(d+install.ManifestFile, r.manifest)
		rs.put(d+install.ManifestSigFile, r.manifSig)
		for arch, a := range r.archives {
			rs.put(d+install.ArchiveName(ver, arch), a)
		}
	}
	return r
}

// withVersion runs the test as a hub on version v.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// fetchRelease downloads the hub's release for arch as node n.
func fetchRelease(ctx context.Context, n *fakeNode, arch string) (string, map[string][]byte, error) {
	files := map[string][]byte{}
	ver, err := n.client().FetchRelease(ctx, arch, func(name string, r io.Reader) error {
		data, err := io.ReadAll(r)
		files[name] = data
		return err
	})
	return ver, files, err
}

// requireBundle checks the files under AssetCacheDir/<ver>/ for arch.
func requireBundle(t *testing.T, env *testEnv, rel signedRelease, ver, arch string) {
	t.Helper()
	dir := filepath.Join(env.root, AssetCacheDir, ver)
	for name, want := range map[string][]byte{
		install.SumsFile:               rel.sums,
		install.SumsSigFile:            rel.sig,
		install.ArchiveName(ver, arch): rel.archives[arch],
		install.ManifestFile:           rel.manifest,
		install.ManifestSigFile:        rel.manifSig,
	} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, name)
		require.Equal(t, want, got, name)
	}
}

// The hub keeps the signed release of another architecture it downloads for
// a node (SHA256SUMS, its signature, the archive and the signed
// backends.yaml under AssetCacheDir/<version>/), not only the binary, and
// serves later requests from there.
func TestHubCachesSignedBundle(t *testing.T) {
	withVersion(t, "1.2.3")
	rs := newReleaseServer(t)
	rel := publishSigned(t, rs, "1.2.3", "amd64", "arm64")
	env := startHub(t, nil, releaseOpts(rs))

	// An old arm64 node: the binary from the signed release, as before.
	info, err := env.h.asset(ctxT(t), "arm64")
	require.NoError(t, err)
	data, err := io.ReadAll(info.Reader)
	require.NoError(t, info.Reader.Close())
	require.NoError(t, err)
	require.Equal(t, []byte("#!deyroute 1.2.3 arm64"), data)
	requireBundle(t, env, rel, "1.2.3", "arm64")
	dir := filepath.Join(env.root, AssetCacheDir, "1.2.3")
	archive, err := install.VerifyBundle(dir, "arm64", "1.2.3", rs.pub.String())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, install.ArchiveName("1.2.3", "arm64")), archive)

	// A node with signed updates gets that release from the cache: the
	// mirror is gone now.
	rs.mu.Lock()
	rs.files = map[string][]byte{}
	rs.mu.Unlock()
	n := env.joinNode("de-1")
	n.features = []string{api.FeatureSignedUpdate}
	ver, files, err := fetchRelease(ctxT(t), n, "arm64")
	require.NoError(t, err)
	require.Equal(t, "1.2.3", ver)
	require.Equal(t, map[string][]byte{
		install.SumsFile:                      rel.sums,
		install.SumsSigFile:                   rel.sig,
		install.ArchiveName("1.2.3", "arm64"): rel.archives["arm64"],
	}, files)
	// An architecture without releases.
	_, _, err = fetchRelease(ctxT(t), n, "mips")
	require.Equal(t, deyerr.I003, codeOf(err))
}

// A node of the hub's own architecture that announced signed-update gets
// the signed release of the hub's version, downloaded on the first request
// through the fetcher chain, and never the hub's own binary: self.update
// names the archive's sha256, the release endpoint serves the release, and
// the old endpoint refuses that node. A node without the feature still
// gets the hub's binary as before. The hub announces the feature in its
// hello.
func TestHubServesSignedBundleForOwnArch(t *testing.T) {
	withVersion(t, "1.2.3")
	rs := newReleaseServer(t)
	rel := publishSigned(t, rs, "1.2.3", "amd64", "arm64")
	env := startHub(t, nil, releaseOpts(rs))
	require.Equal(t, "amd64", env.h.o.Arch)
	require.NoFileExists(t, filepath.Join(env.root, AssetCacheDir, "1.2.3", install.SumsFile), "no bundle before the first request")

	got := make(chan api.SelfUpdateArgs, 4)
	n := env.joinNode("de-1")
	n.version = "1.0.0"
	n.features = []string{api.FeatureTuneAuto, api.FeatureSignedUpdate}
	n.on(api.CmdSelfUpdate, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SelfUpdateArgs
		decode(t, cmd, &a)
		got <- a
		return nil, nil
	})
	env.h.markNodesForUpdate()
	n.start()
	var args api.SelfUpdateArgs
	select {
	case args = <-got:
	case <-time.After(testWait):
		t.Fatal("no self.update")
	}
	hello, _ := n.hubHello.Load().(api.Hello)
	require.True(t, hello.HasFeature(api.FeatureSignedUpdate), "the hub announces signed updates: %+v", hello)
	require.Equal(t, "1.2.3", args.Version)
	archiveSum := sha256.Sum256(rel.archives["amd64"])
	require.Equal(t, hex.EncodeToString(archiveSum[:]), args.SHA256, "the archive's sha256, not the hub binary's")
	requireBundle(t, env, rel, "1.2.3", "amd64")

	ver, files, err := fetchRelease(ctxT(t), n, "amd64")
	require.NoError(t, err)
	require.Equal(t, "1.2.3", ver)
	require.Equal(t, map[string][]byte{
		install.SumsFile:                      rel.sums,
		install.SumsSigFile:                   rel.sig,
		install.ArchiveName("1.2.3", "amd64"): rel.archives["amd64"],
	}, files)
	for name, data := range files {
		require.NotEqual(t, env.self, data, name)
	}

	// The old endpoint does not hand the hub's binary to this node.
	var buf bytes.Buffer
	_, err = n.client().FetchAsset(ctxT(t), "amd64", &buf)
	require.Equal(t, deyerr.N015, codeOf(err))
	require.NotEqual(t, env.self, buf.Bytes())

	// A node without the feature: the hub's binary, as before.
	old := env.joinNode("nl-1")
	buf.Reset()
	meta, err := old.client().FetchAsset(ctxT(t), "amd64", &buf)
	require.NoError(t, err)
	require.Equal(t, env.self, buf.Bytes())
	require.Equal(t, "1.2.3", meta.Version)
}

// A hub on a development build has no signed release: the release endpoint
// says so (DEY-S012), the old endpoint still serves the hub's binary (a lab
// node with node.allow_unsigned_updates takes it), and self.update names
// that binary's sha256.
func TestDevHubServesNoSignedRelease(t *testing.T) {
	withVersion(t, "dev")
	env := startHub(t, nil)
	n := env.joinNode("de-1")
	n.features = []string{api.FeatureSignedUpdate}
	_, _, err := fetchRelease(ctxT(t), n, "amd64")
	require.Equal(t, deyerr.S012, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), "deyroute dev")
	var buf bytes.Buffer
	_, err = n.client().FetchAsset(ctxT(t), "amd64", &buf)
	require.NoError(t, err)
	require.Equal(t, env.self, buf.Bytes())
	sum, err := env.h.selfUpdateSum(ctxT(t), "de-1", "amd64")
	require.NoError(t, err)
	s := sha256.Sum256(env.self)
	require.Equal(t, hex.EncodeToString(s[:]), sum)
}

// A failed download of the release is reported to the node, and the hub
// sends no self.update it cannot back with a release.
func TestHubReleaseDownloadFails(t *testing.T) {
	withVersion(t, "1.2.3")
	rs := newReleaseServer(t)
	publishSigned(t, rs, "1.2.3", "amd64")
	rs.mu.Lock()
	rs.files["/v1.2.3/"+install.SumsSigFile] = rs.sign([]byte("other"))
	rs.files["/latest/"+install.SumsSigFile] = rs.sign([]byte("other"))
	rs.mu.Unlock()
	env := startHub(t, nil, releaseOpts(rs))
	n := env.joinNode("de-1")
	n.features = []string{api.FeatureSignedUpdate}
	n.start()
	env.waitOnline("de-1", true)
	_, _, err := fetchRelease(ctxT(t), n, "amd64")
	require.Equal(t, deyerr.S001, codeOf(err))
	_, err = env.h.selfUpdateSum(ctxT(t), "de-1", "amd64")
	require.Equal(t, deyerr.S001, codeOf(err))
	require.NoFileExists(t, filepath.Join(env.root, AssetCacheDir, "1.2.3", install.SumsFile))
}

// `deyroute update` keeps the verified release it installed under
// AssetCacheDir/<version>/ for the nodes. When the hub starts, the
// releases of versions it neither runs nor rolls back to go.
func TestUpdateKeepsSignedBundle(t *testing.T) {
	withVersion(t, "1.0.0")
	rs := newReleaseServer(t)
	rel := publishSigned(t, rs, "1.2.3", "amd64", "arm64")
	env, o := prepareEnv(t, nil, releaseOpts(rs))
	cache := filepath.Join(env.root, AssetCacheDir)
	env.startEnv(o)
	for _, d := range []string{"0.0.9", "1.0.0", "backhaul/1.0.0", "0.0.8"} {
		require.NoError(t, os.MkdirAll(filepath.Join(cache, d), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(cache, d, install.SumsFile), []byte("old"), 0o644))
	}
	// deyroute.prev is 0.0.8, as recorded by the update that wrote it.
	prev := install.SelfUpdater{Root: env.root}.PrevPath()
	require.NoError(t, os.WriteFile(prev, []byte("#!deyroute 0.0.8"), 0o755)) // #nosec G306 -- test binary
	env.h.recordPrevious("0.0.8")
	env.h.pruneBundles()
	require.NoDirExists(t, filepath.Join(cache, "0.0.9"), "the release of a version neither running nor kept is removed")
	require.DirExists(t, filepath.Join(cache, "1.0.0"), "the running version's release stays")
	require.DirExists(t, filepath.Join(cache, "0.0.8"), "the release a rollback returns to stays")
	require.DirExists(t, filepath.Join(cache, "backhaul/1.0.0"), "backend binaries are never touched")

	_, err := env.client.UpdateApply(ctxT(t), "1.2.3", nil)
	require.NoError(t, err)
	requireBundle(t, env, rel, "1.2.3", "amd64")
	require.NoFileExists(t, filepath.Join(cache, "1.2.3", install.ArchiveName("1.2.3", "arm64")))
	require.DirExists(t, filepath.Join(cache, "1.0.0"), "kept until the hub runs the new version")
}

// A hub that starts removes the releases of versions it neither runs nor
// rolls back to (an installer upgrade leaves the old one behind).
func TestHubStartPrunesOldBundles(t *testing.T) {
	withVersion(t, "1.2.3")
	env, o := prepareEnv(t, nil)
	cache := filepath.Join(env.root, AssetCacheDir)
	for _, d := range []string{"1.2.2", "1.2.3", "awg/1.0.0"} {
		require.NoError(t, os.MkdirAll(filepath.Join(cache, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(cache, "deyroute-1.2.2-arm64"), []byte("bin"), 0o755)) // #nosec G306 -- test binary
	env.startEnv(o)
	require.NoDirExists(t, filepath.Join(cache, "1.2.2"))
	require.DirExists(t, filepath.Join(cache, "1.2.3"))
	require.DirExists(t, filepath.Join(cache, "awg/1.0.0"))
	require.FileExists(t, filepath.Join(cache, "deyroute-1.2.2-arm64"), "only release directories are pruned")
}
