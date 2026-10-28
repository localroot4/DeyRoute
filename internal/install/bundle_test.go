package install

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// bundle writes a signed release bundle of ver for arch into a new
// directory: archive holds entries, SHA256SUMS lists it under the name of
// listed (default ver).
func writeBundle(t *testing.T, k testKey, ver, arch, listed string, entries []tarEntry) string {
	t.Helper()
	if listed == "" {
		listed = ver
	}
	dir := t.TempDir()
	archive := makeTarGz(t, entries)
	sums := []byte(sha(archive) + "  " + ArchiveName(listed, arch) + "\n" + sha([]byte("sh")) + "  install.sh\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ArchiveName(ver, arch)), archive, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, SumsFile), sums, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, SumsSigFile), k.signHashed(t, sums), 0o600))
	return dir
}

func TestVerifyBundle(t *testing.T) {
	k := newTestKey(t)
	bin := []tarEntry{{Name: "deyroute_1.2.0_linux_amd64/deyroute", Body: "deyroute 1.2.0"}}
	dir := writeBundle(t, k, "1.2.0", "amd64", "", bin)
	p, err := VerifyBundle(dir, "amd64", "v1.2.0", k.pubString())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, ArchiveName("1.2.0", "amd64")), p)
	require.Equal(t, []string{SumsFile, SumsSigFile, "deyroute_1.2.0_linux_amd64.tar.gz"}, BundleFiles("v1.2.0", "amd64"))

	// Every check names itself in the why.
	cases := map[string]struct {
		dir, arch, ver, key string
		why                 string
	}{
		"other key":     {dir, "amd64", "1.2.0", newTestKey(t).pubString(), "not a valid signature of SHA256SUMS"},
		"release key":   {dir, "amd64", "1.2.0", "", "not with the DEYROUTE release key"},
		"other version": {dir, "amd64", "1.3.0", k.pubString(), "does not list deyroute_1.3.0_linux_amd64.tar.gz: it belongs to another release (deyroute_1.2.0_linux_amd64.tar.gz)"},
		"other arch":    {dir, "arm64", "1.2.0", k.pubString(), "no archive for arm64"},
		"no version":    {dir, "amd64", "", k.pubString(), "named no version"},
	}
	for name, c := range cases {
		_, err := VerifyBundle(c.dir, c.arch, c.ver, c.key)
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), c.why, name)
	}

	// The archive of the right name holds other bytes than signed.
	tampered := writeBundle(t, k, "1.2.0", "amd64", "", bin)
	require.NoError(t, os.WriteFile(filepath.Join(tampered, ArchiveName("1.2.0", "amd64")), []byte("other"), 0o600))
	_, err = VerifyBundle(tampered, "amd64", "1.2.0", k.pubString())
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "does not match the signed SHA256SUMS")
	require.Equal(t, "Signature or checksum invalid: deyroute_1.2.0_linux_amd64.tar.gz", de.Message())

	// Files missing, or not regular.
	for _, f := range []string{ArchiveName("1.2.0", "amd64"), SumsSigFile, SumsFile} {
		d := writeBundle(t, k, "1.2.0", "amd64", "", bin)
		require.NoError(t, os.Remove(filepath.Join(d, f)))
		_, err := VerifyBundle(d, "amd64", "1.2.0", k.pubString())
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), "no readable", f)
	}
	d := writeBundle(t, k, "1.2.0", "amd64", "", bin)
	require.NoError(t, os.Remove(filepath.Join(d, SumsFile)))
	require.NoError(t, os.Symlink(filepath.Join(dir, SumsFile), filepath.Join(d, SumsFile)))
	_, err = VerifyBundle(d, "amd64", "1.2.0", k.pubString())
	de = requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "not a regular file")

	// A validly signed SHA256SUMS that cannot be parsed.
	junk := []byte("junk\n")
	require.NoError(t, os.Remove(filepath.Join(d, SumsFile)))
	require.NoError(t, os.WriteFile(filepath.Join(d, SumsFile), junk, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(d, SumsSigFile), k.signLegacy(junk), 0o600))
	_, err = VerifyBundle(d, "amd64", "1.2.0", k.pubString())
	de = requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "cannot be read")
}

func TestExtractReleaseBinary(t *testing.T) {
	for name, entries := range map[string][]tarEntry{
		"top level":    {{Name: "deyroute", Body: "bin"}, {Name: "README.md", Body: "r"}},
		"in directory": {{Name: "deyroute_1.2.0_linux_amd64/", Type: tar.TypeDir}, {Name: "deyroute_1.2.0_linux_amd64/deyroute", Body: "bin"}},
	} {
		archive := writeTemp(t, "a.tar.gz", makeTarGz(t, entries))
		dst := filepath.Join(t.TempDir(), "bin")
		p, err := ExtractReleaseBinary(archive, dst)
		require.NoError(t, err, name)
		requireBinary(t, p, "bin")
		fi, err := os.Stat(dst)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), fi.Mode().Perm(), "the staging directory is root-only")
	}

	for name, c := range map[string]struct {
		entries []tarEntry
		why     string
	}{
		"traversal":      {[]tarEntry{{Name: "../deyroute", Body: "evil"}}, "escapes the target directory"},
		"absolute":       {[]tarEntry{{Name: "/usr/local/bin/deyroute", Body: "evil"}}, "escapes the target directory"},
		"deep traversal": {[]tarEntry{{Name: "deyroute", Body: "bin"}, {Name: "x/../../etc/cron.d/x", Body: "evil"}}, "escapes the target directory"},
		"symlink":        {[]tarEntry{{Name: "deyroute", Type: tar.TypeSymlink, Linkname: "/bin/sh"}}, "not a regular file"},
		"hard link":      {[]tarEntry{{Name: "deyroute", Type: tar.TypeLink, Linkname: "README.md"}}, "not a regular file"},
		"two":            {[]tarEntry{{Name: "deyroute", Body: "a"}, {Name: "d/deyroute", Body: "b"}}, "twice"},
		"none":           {[]tarEntry{{Name: "README.md", Body: "r"}}, "binary not found"},
	} {
		archive := writeTemp(t, "a.tar.gz", makeTarGz(t, c.entries))
		dst := t.TempDir()
		_, err := ExtractReleaseBinary(archive, dst)
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), c.why, name)
		require.Equal(t, "Signature or checksum invalid: a.tar.gz", de.Message(), name)
		require.NoFileExists(t, filepath.Join(dst, BinaryName), name)
	}

	// The size cap (lowered here; 200 MiB in production).
	require.Equal(t, int64(200<<20), MaxReleaseBinaryBytes)
	old := releaseBinaryLimit
	releaseBinaryLimit = 8
	t.Cleanup(func() { releaseBinaryLimit = old })
	archive := writeTemp(t, "a.tar.gz", makeTarGz(t, []tarEntry{{Name: "deyroute", Body: strings.Repeat("x", 9)}}))
	_, err := ExtractReleaseBinary(archive, t.TempDir())
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "larger than 8 bytes")
	// The generic backend cap is not lowered with it.
	require.Equal(t, MaxExtractBytes, extractLimit)
}

func TestIsReleaseVersion(t *testing.T) {
	for v, want := range map[string]bool{
		"0.3.0": true, "v0.3.0": true, "0.3.0-edge.60": true, "0.9.0-it.1": true,
		"dev": false, "": false, " ": false, "abc": false,
	} {
		require.Equal(t, want, IsReleaseVersion(v), v)
	}
}

// DownloadRelease keeps the verified SHA256SUMS and its signature.
func TestDownloadReleaseKeepsSignedSums(t *testing.T) {
	k := newTestKey(t)
	m := newMapFetcher()
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	rel := newFakeRelease(t, k, "1.4.0", "amd64")
	rel.publish(m, gh, "1.4.0", "1.4.0", "amd64")
	r, err := DownloadRelease(context.Background(), ReleaseOptions{
		Fetcher: m, Sources: []Source{gh}, Arch: "amd64", Version: "1.4.0", PublicKey: k.pubString(),
		WorkDir: t.TempDir(), Retry: RetryOptions{Sleep: (&noSleep{}).sleep},
	})
	require.NoError(t, err)
	require.Equal(t, rel.sums, r.Sums)
	require.Equal(t, rel.sig, r.SumsSig)
	require.NoError(t, VerifySums(r.Sums, r.SumsSig, k.pubString()))
}

func TestFetchSignedManifest(t *testing.T) {
	k := newTestKey(t)
	m := newMapFetcher()
	base := Source{Name: SourceReleaseBase, BaseURL: "https://cdn.example"}
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	ro := RetryOptions{Sleep: (&noSleep{}).sleep}
	// A manifest with a key this build does not know is kept as it is.
	data := append(backend.EmbeddedManifest(), []byte("\nfuture_key: 1\n")...)
	sig := k.signHashed(t, data)
	m.files[base.Manifest("1.4.0")] = data
	m.files[base.ManifestSig("1.4.0")] = newTestKey(t).signLegacy(data)
	m.files[gh.Manifest("1.4.0")] = data
	m.files[gh.ManifestSig("1.4.0")] = sig

	got, gotSig, err := FetchSignedManifest(context.Background(), m, []Source{base, gh}, "1.4.0", k.pubString(), ro)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Equal(t, sig, gotSig)

	_, _, err = FetchSignedManifest(context.Background(), m, []Source{base}, "1.4.0", k.pubString(), ro)
	requireCode(t, err, deyerr.S001)
	_, _, err = FetchSignedManifest(context.Background(), m, []Source{gh}, "1.4.0", "", ro)
	requireCode(t, err, deyerr.S001)
	_, _, err = FetchSignedManifest(context.Background(), newMapFetcher(), []Source{gh}, "1.4.0", k.pubString(), ro)
	requireCode(t, err, deyerr.I004)
	_, _, err = FetchSignedManifest(context.Background(), m, nil, "", "", ro)
	requireCode(t, err, deyerr.I004)
}
