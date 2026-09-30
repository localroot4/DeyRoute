package install

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

type fakeRelease struct {
	archive []byte
	sums    []byte
	sig     []byte
}

func newFakeRelease(t *testing.T, k testKey, ver, arch string) fakeRelease {
	t.Helper()
	archive := makeTarGz(t, []tarEntry{{Name: "deyroute", Body: "deyroute " + ver}, {Name: "README.md", Body: "r", Mode: 0o644}})
	sums := []byte(sha(archive) + "  " + ArchiveName(ver, arch) + "\n" + sha([]byte("sh")) + "  install.sh\n")
	return fakeRelease{archive: archive, sums: sums, sig: k.signHashed(t, sums)}
}

func (r fakeRelease) publish(m *mapFetcher, s Source, urlVersion, ver, arch string) {
	m.files[s.Sums(urlVersion)] = r.sums
	m.files[s.SumsSig(urlVersion)] = r.sig
	m.files[s.Archive(ver, arch)] = r.archive
}

func TestDownloadRelease(t *testing.T) {
	k := newTestKey(t)
	m := newMapFetcher()
	mirror := Source{Name: SourceMirror, BaseURL: "https://mirror.example"}
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	rel := newFakeRelease(t, k, "1.4.0", "amd64")
	rel.publish(m, gh, "", "1.4.0", "amd64")
	// The mirror serves a SHA256SUMS signed by another key.
	evil := newFakeRelease(t, newTestKey(t), "1.4.0", "amd64")
	evil.publish(m, mirror, "", "1.4.0", "amd64")

	work := t.TempDir()
	r, err := DownloadRelease(context.Background(), ReleaseOptions{
		Fetcher: m, Sources: []Source{mirror, gh}, Arch: "amd64", PublicKey: k.pubString(), WorkDir: work,
		Retry: RetryOptions{Sleep: (&noSleep{}).sleep},
	})
	require.NoError(t, err)
	require.Equal(t, "1.4.0", r.Version)
	require.Equal(t, SourceGitHub, r.Source)
	require.Equal(t, sha(rel.archive), r.SHA256)
	requireBinary(t, r.Binary, "deyroute 1.4.0")
	require.Zero(t, m.count(mirror.Archive("1.4.0", "amd64")), "archives are fetched only after a valid signature")

	// The verified binary can be installed with SelfUpdater.
	root := t.TempDir()
	require.NoError(t, SelfUpdater{Root: root}.Install(r.Binary))
	require.Equal(t, "deyroute 1.4.0", readString(t, SelfUpdater{Root: root}.BinaryPath()))
}

func TestDownloadReleaseErrors(t *testing.T) {
	k := newTestKey(t)
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	opts := func(m *mapFetcher) ReleaseOptions {
		return ReleaseOptions{Fetcher: m, Sources: []Source{gh}, Arch: "amd64", Version: "1.4.0",
			PublicKey: k.pubString(), WorkDir: t.TempDir(), Retry: RetryOptions{Sleep: (&noSleep{}).sleep}}
	}

	// Unreachable → I004.
	_, err := DownloadRelease(context.Background(), opts(newMapFetcher()))
	requireCode(t, err, deyerr.I004)

	// Bad signature everywhere → S001 (not I004).
	m := newMapFetcher()
	newFakeRelease(t, newTestKey(t), "1.4.0", "amd64").publish(m, gh, "1.4.0", "1.4.0", "amd64")
	_, err = DownloadRelease(context.Background(), opts(m))
	requireCode(t, err, deyerr.S001)

	// Signed sums, tampered archive → S001; wrong version → I004.
	m = newMapFetcher()
	rel := newFakeRelease(t, k, "1.4.0", "amd64")
	rel.publish(m, gh, "1.4.0", "1.4.0", "amd64")
	m.files[gh.Archive("1.4.0", "amd64")] = []byte("tampered")
	_, err = DownloadRelease(context.Background(), opts(m))
	requireCode(t, err, deyerr.S001)

	o := opts(m)
	o.Arch = "arm64"
	_, err = DownloadRelease(context.Background(), o)
	requireCode(t, err, deyerr.I004)

	// Archive without a deyroute binary → S001 for that archive.
	m = newMapFetcher()
	bad := makeTarGz(t, []tarEntry{{Name: "README.md", Body: "r"}})
	sums := []byte(sha(bad) + "  " + ArchiveName("1.4.0", "amd64") + "\n")
	m.files[gh.Sums("1.4.0")] = sums
	m.files[gh.SumsSig("1.4.0")] = k.signLegacy(sums)
	m.files[gh.Archive("1.4.0", "amd64")] = bad
	_, err = DownloadRelease(context.Background(), opts(m))
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "binary not found")

	// Unparseable SHA256SUMS (validly signed) → S001.
	m = newMapFetcher()
	junk := []byte("junk\n")
	m.files[gh.Sums("1.4.0")] = junk
	m.files[gh.SumsSig("1.4.0")] = k.signLegacy(junk)
	_, err = DownloadRelease(context.Background(), opts(m))
	requireCode(t, err, deyerr.S001)

	// Missing signature → I004 (download failure).
	m = newMapFetcher()
	m.files[gh.Sums("1.4.0")] = sums
	_, err = DownloadRelease(context.Background(), opts(m))
	requireCode(t, err, deyerr.I004)

	_, err = DownloadRelease(context.Background(), ReleaseOptions{})
	requireCode(t, err, deyerr.I004)
}

func TestFetchManifest(t *testing.T) {
	k := newTestKey(t)
	m := newMapFetcher()
	base := Source{Name: SourceReleaseBase, BaseURL: "https://cdn.example"}
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	data := backend.EmbeddedManifest()
	m.files[base.Manifest("")] = data
	m.files[base.ManifestSig("")] = newTestKey(t).signLegacy(data) // wrong key on the CDN
	m.files[gh.Manifest("")] = data
	m.files[gh.ManifestSig("")] = k.signHashed(t, data)

	mf, raw, err := FetchManifest(context.Background(), m, []Source{base, gh}, "", k.pubString(), RetryOptions{Sleep: (&noSleep{}).sleep})
	require.NoError(t, err)
	require.Equal(t, data, raw)
	require.Contains(t, mf.Backends, "xray")

	root := t.TempDir()
	require.NoError(t, InstallManifest(root, raw))
	got, err := os.ReadFile(filepath.Join(root, "etc/deyroute/backends.yaml"))
	require.NoError(t, err)
	require.Equal(t, data, got)
	requireCode(t, InstallManifest(root, []byte("bogus: [")), deyerr.S005)

	// Only badly signed copies → S001; nothing reachable → I004.
	_, _, err = FetchManifest(context.Background(), m, []Source{base}, "", k.pubString(), RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.S001)
	_, _, err = FetchManifest(context.Background(), newMapFetcher(), []Source{base}, "1.0.0", k.pubString(), RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.I004)
	_, _, err = FetchManifest(context.Background(), m, nil, "", "", RetryOptions{})
	requireCode(t, err, deyerr.I004)

	// A validly signed but invalid manifest → S005 from the strict parser.
	bad := []byte("manifest_version: 1\nunknown_key: 1\n")
	m.files[gh.Manifest("2.0.0")] = bad
	m.files[gh.ManifestSig("2.0.0")] = k.signLegacy(bad)
	_, _, err = FetchManifest(context.Background(), m, []Source{gh}, "2.0.0", k.pubString(), RetryOptions{Sleep: (&noSleep{}).sleep})
	requireCode(t, err, deyerr.S005)
}
