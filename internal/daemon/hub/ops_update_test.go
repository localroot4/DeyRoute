package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aead.dev/minisign"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

// releaseServer is a fake release mirror (<base>/latest/<file> and
// <base>/v<version>/<file>) signed with a throwaway minisign key.
type releaseServer struct {
	*httptest.Server
	pub  minisign.PublicKey
	priv minisign.PrivateKey

	mu    sync.Mutex
	files map[string][]byte // path → content
}

func newReleaseServer(t *testing.T) *releaseServer {
	pub, priv, err := minisign.GenerateKey(nil)
	require.NoError(t, err)
	rs := &releaseServer{pub: pub, priv: priv, files: map[string][]byte{}}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.mu.Lock()
		data, ok := rs.files[r.URL.Path]
		rs.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(rs.Close)
	return rs
}

// sign returns a minisign signature of msg.
func (rs *releaseServer) sign(msg []byte) []byte {
	return minisign.SignWithComments(rs.priv, msg, "timestamp:1\tfile:test", "test signature")
}

// put serves data at path.
func (rs *releaseServer) put(path string, data []byte) {
	rs.mu.Lock()
	rs.files[path] = data
	rs.mu.Unlock()
}

// release publishes deyroute ver for amd64 (latest and v<ver>) with binary
// content bin; badSig breaks the signature.
func (rs *releaseServer) release(t *testing.T, ver string, bin []byte, badSig bool) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "deyroute", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}))
	_, err := tw.Write(bin)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	archive := install.ArchiveName(ver, "amd64")
	sum := sha256.Sum256(buf.Bytes())
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + archive + "\n")
	sig := rs.sign(sums)
	if badSig {
		sig = rs.sign([]byte("something else"))
	}
	for _, dir := range []string{"/latest/", "/v" + ver + "/"} {
		rs.put(dir+install.SumsFile, sums)
		rs.put(dir+install.SumsSigFile, sig)
		rs.put(dir+archive, buf.Bytes())
	}
}

// localOnly fetches from srv only; every other URL fails permanently (no
// test reaches GitHub).
type localOnly struct{ base string }

// Fetch implements install.Fetcher.
func (f localOnly) Fetch(ctx context.Context, url string, w io.Writer) error {
	if !strings.HasPrefix(url, f.base) {
		return install.Permanent(errors.New("not reachable in tests: " + url))
	}
	return install.HTTPFetcher{}.Fetch(ctx, url, w)
}

// releaseOpts points a hub at rs (mirror, key, fetcher).
func releaseOpts(rs *releaseServer) envOption {
	return func(o *Options, _ string) {
		o.MinisignKey = rs.pub.String()
		o.Fetcher = localOnly{base: rs.URL}
		o.Getenv = func(k string) string {
			if k == install.MirrorEnv {
				return rs.URL
			}
			return ""
		}
	}
}

func TestUpdateCheckApplyRollback(t *testing.T) {
	rs := newReleaseServer(t)
	rs.release(t, "1.2.3", []byte("#!deyroute 1.2.3"), false)
	env, o := prepareEnv(t, nil, releaseOpts(rs))
	bin := filepath.Join(env.root, config.BinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!deyroute old"), 0o755)) // #nosec G306 -- test binary
	env.startEnv(o)
	ctx := ctxT(t)

	info, err := env.client.UpdateCheck(ctx)
	require.NoError(t, err)
	require.Equal(t, version.Version, info.Current)
	require.Equal(t, "1.2.3", info.Latest)
	require.True(t, info.Available)
	require.Equal(t, install.GitHubReleases+"/tag/v1.2.3", info.Changelog)
	require.Empty(t, info.Previous)
	_, err = env.client.UpdateAuto(ctx, "off") // else the automatic update installs it
	require.NoError(t, err)
	st, err := env.client.Status(ctx)
	require.NoError(t, err)
	var announced bool
	for _, w := range st.Warnings {
		announced = announced || strings.Contains(w.Message, "deyroute 1.2.3 is available")
	}
	require.True(t, announced)

	// A node of another version connects after the update: it is told to
	// install the hub's binary.
	var selfUpd = make(chan api.SelfUpdateArgs, 4)
	n := env.joinNode("de-1")
	n.version = "0.9.0"
	n.on(api.CmdSelfUpdate, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SelfUpdateArgs
		decode(t, cmd, &a)
		selfUpd <- a
		return nil, nil
	})

	var log stepLog
	info, err = env.client.UpdateApply(ctx, "", log.add)
	require.NoError(t, err, "steps %v", log.finished())
	require.Equal(t, "1.2.3", info.Current)
	require.Equal(t, version.Version, info.Previous)
	require.Equal(t, []string{"resolve:ok", "download:ok", "install:ok", "nodes:ok", "restart_hub:ok"}, log.finished())
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!deyroute 1.2.3", string(data))
	prev, err := os.ReadFile(filepath.Join(env.root, config.PrevBinaryPath))
	require.NoError(t, err)
	require.Equal(t, "#!deyroute old", string(prev))
	env.waitEvent(state.EvUpdateApplied, "")
	require.Eventually(t, func() bool {
		return env.runner.Called("systemctl restart --no-block deyroute-hub.service")
	}, testWait, 10*time.Millisecond)
	var rec nodeUpdateRecord
	ok, err := env.h.st.GetMeta(metaNodeUpdate, &rec)
	require.NoError(t, err)
	require.True(t, ok)

	n.start()
	select {
	case a := <-selfUpd:
		require.Equal(t, version.Version, a.Version)
		sum := sha256.Sum256(env.self)
		require.Equal(t, hex.EncodeToString(sum[:]), a.SHA256)
	case <-time.After(testWait):
		t.Fatal("no self.update")
	}
	// The node comes back with the hub's version: the update is complete.
	n.stop()
	env.waitOnline("de-1", false)
	n.version = version.Version
	n.start()
	require.Eventually(t, func() bool {
		ok, err := env.h.st.GetMeta(metaNodeUpdate, &rec)
		return err == nil && !ok
	}, testWait, 20*time.Millisecond)

	// Roll back: the old binary returns and the hub restarts.
	info, err = env.client.UpdateRollback(ctx)
	require.NoError(t, err)
	require.Equal(t, version.Version, info.Current)
	data, err = os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!deyroute old", string(data))
	ev := env.waitEvent(state.EvUpdateRolledBack, "")
	require.Equal(t, string(deyerr.S003), ev.Code)
	require.Eventually(t, func() bool {
		return env.runner.Count("systemctl restart --no-block deyroute-hub.service") == 2
	}, testWait, 10*time.Millisecond)
	check, err := env.client.UpdateCheck(ctx)
	require.NoError(t, err)
	require.Equal(t, version.Version, check.Previous)
	require.NoError(t, os.Remove(filepath.Join(env.root, config.PrevBinaryPath)))
	_, err = env.client.UpdateRollback(ctx)
	require.Equal(t, deyerr.S007, codeOf(err))

	// A specific version.
	rs.release(t, "1.1.0", []byte("#!deyroute 1.1.0"), false)
	info, err = env.client.UpdateApply(ctx, "v1.1.0", nil)
	require.NoError(t, err)
	require.Equal(t, "1.1.0", info.Current)
	_, err = env.client.UpdateApply(ctx, "7.7.7", nil)
	require.Equal(t, deyerr.I004, codeOf(err))
}

func TestUpdateRefusesBadSignature(t *testing.T) {
	rs := newReleaseServer(t)
	rs.release(t, "1.2.3", []byte("#!deyroute 1.2.3"), true)
	env := startHub(t, nil, releaseOpts(rs))
	ctx := ctxT(t)
	_, err := env.client.UpdateCheck(ctx)
	require.Equal(t, deyerr.S001, codeOf(err))
	_, err = env.client.UpdateApply(ctx, "", nil)
	require.Equal(t, deyerr.S001, codeOf(err))
	require.NoFileExists(t, filepath.Join(env.root, config.BinaryPath))
	require.False(t, env.runner.Called("systemctl restart --no-block deyroute-hub.service"))
	// Nothing published at all.
	rs.mu.Lock()
	rs.files = map[string][]byte{}
	rs.mu.Unlock()
	_, err = env.client.UpdateCheck(ctx)
	require.Equal(t, deyerr.I004, codeOf(err))
}

func TestDailyUpdateCheck(t *testing.T) {
	rs := newReleaseServer(t)
	rs.release(t, "1.2.3", []byte("#!deyroute 1.2.3"), false)
	env := startHub(t, func(c *config.Config) { c.Hub.UpdateCheck = true }, releaseOpts(rs),
		func(o *Options, _ string) { o.JobStartDelay = 10 * time.Millisecond })
	ev := env.waitEvent(UpdateAvailableEvent, "")
	require.Contains(t, ev.Message, "deyroute 1.2.3 is available")
	// Announced once per release.
	env.h.dailyUpdateCheck(ctxT(t))
	require.Equal(t, 1, env.countEvents(UpdateAvailableEvent))
	// Nothing is installed.
	require.NoFileExists(t, filepath.Join(env.root, config.BinaryPath))
}

func TestUpdateManifest(t *testing.T) {
	orig, err := backend.ParseManifest(backend.EmbeddedManifest())
	require.NoError(t, err)
	t.Cleanup(func() { backend.SetManifest(orig) })
	rs := newReleaseServer(t)
	data := bytes.Replace(backend.EmbeddedManifest(), []byte("version: v0.7.2"), []byte("version: v0.7.9"), 1)
	rs.put("/latest/"+install.ManifestFile, data)
	rs.put("/latest/"+install.ManifestSigFile, rs.sign(data))
	env := startHub(t, nil, releaseOpts(rs))
	ctx := ctxT(t)
	info, err := env.client.UpdateManifest(ctx)
	require.NoError(t, err)
	require.Equal(t, config.ManifestPath, info.Source)
	require.Equal(t, "v0.7.9", info.Versions["backhaul"])
	require.Equal(t, "v0.7.9", backend.ManifestFor("backhaul").Version)
	written, err := os.ReadFile(filepath.Join(env.root, config.ManifestPath))
	require.NoError(t, err)
	require.Equal(t, data, written)

	// A bad signature changes nothing.
	rs.put("/latest/"+install.ManifestSigFile, rs.sign([]byte("other")))
	_, err = env.client.UpdateManifest(ctx)
	require.Equal(t, deyerr.S001, codeOf(err))
	require.Equal(t, "v0.7.9", backend.ManifestFor("backhaul").Version)
}
