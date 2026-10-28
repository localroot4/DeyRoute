package install

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// pinnedManifest is a backends.yaml with one entry, backhaul v9.9.9, and
// extra appended to it (a key a newer build may write).
func pinnedManifest(extra string) []byte {
	return []byte(`manifest_version: 1
backends:
  backhaul:
    version: v9.9.9
    archive: tar.gz
    binaries: [backhaul]
    urls:
      amd64: https://mirror.example/backhaul_amd64.tar.gz
      arm64: https://mirror.example/backhaul_arm64.tar.gz
    sha256: { amd64: "` + sha([]byte("amd64")) + `", arm64: "` + sha([]byte("arm64")) + `" }
    template_version: 1
` + extra)
}

func pinnedEntry() backend.ManifestEntry {
	return backend.ManifestEntry{Name: "backhaul", Version: "v9.9.9", Archive: "tar.gz", Binaries: []string{"backhaul"},
		URLs:   map[string]string{"amd64": "https://mirror.example/backhaul_amd64.tar.gz", "arm64": "https://mirror.example/backhaul_arm64.tar.gz"},
		SHA256: map[string]string{"amd64": sha([]byte("amd64")), "arm64": sha([]byte("arm64"))}, TemplateVersion: 1}
}

func TestCheckBackendEntry(t *testing.T) {
	k := newTestKey(t)
	embedded := backend.EmbeddedManifest()
	builtin, err := backend.ParseManifest(embedded)
	require.NoError(t, err)

	// Every entry of the built-in manifest is pinned by it, whatever the hub
	// sends along.
	for name, e := range builtin.Backends {
		if e.Builtin || e.System {
			continue
		}
		for _, arch := range []string{"amd64", "arm64"} {
			src, err := CheckBackendEntry(e, arch, embedded, []byte("junk"), []byte("junk"), k.pubString())
			require.NoError(t, err, name)
			require.Equal(t, PinnedBuiltIn, src, name)
		}
	}

	// A signed manifest pins its entries; a key this build does not know is
	// ignored, the rest of the entry (archive, binaries) does not matter.
	manifest := pinnedManifest("    future_key: 1\n")
	sig := k.signHashed(t, manifest)
	e := pinnedEntry()
	e.Binaries, e.Archive, e.Readme = []string{"other"}, "zip", "https://example.com"
	src, err := CheckBackendEntry(e, "amd64", embedded, manifest, sig, k.pubString())
	require.NoError(t, err)
	require.Equal(t, PinnedSigned, src)
	// An upper-case sha256 is the same sha256.
	e.SHA256 = map[string]string{"amd64": string(bytes.ToUpper([]byte(sha([]byte("amd64")))))}
	_, err = CheckBackendEntry(e, "amd64", embedded, manifest, sig, k.pubString())
	require.NoError(t, err)

	// Without a signed manifest only the built-in one counts.
	_, err = CheckBackendEntry(pinnedEntry(), "amd64", embedded, nil, nil, k.pubString())
	require.ErrorContains(t, err, "backend backhaul v9.9.9 for amd64 is not pinned by this node's built-in backends.yaml, and the hub sent no signed backends.yaml")

	// Any difference in name, version, URL or sha256 of the node's arch.
	for name, edit := range map[string]func(e *backend.ManifestEntry){
		"version":   func(e *backend.ManifestEntry) { e.Version = "v9.9.8" },
		"url":       func(e *backend.ManifestEntry) { e.URLs = map[string]string{"amd64": "https://evil.example/b.tar.gz"} },
		"sha256":    func(e *backend.ManifestEntry) { e.SHA256 = map[string]string{"amd64": sha([]byte("evil"))} },
		"no sha256": func(e *backend.ManifestEntry) { e.SHA256 = nil },
		"other arch": func(e *backend.ManifestEntry) {
			e.URLs["amd64"] = e.URLs["arm64"]
			e.SHA256["amd64"] = e.SHA256["arm64"]
		},
		"other name": func(e *backend.ManifestEntry) { e.Name = "frp" },
		"no name":    func(e *backend.ManifestEntry) { e.Name = "" },
	} {
		e := pinnedEntry()
		edit(&e)
		_, err := CheckBackendEntry(e, "amd64", embedded, manifest, sig, k.pubString())
		require.ErrorContains(t, err, "pinned neither by this node's built-in backends.yaml nor by the signed backends.yaml the hub sent", name)
	}
	// The same entry for an arch the signed manifest pins differently.
	_, err = CheckBackendEntry(pinnedEntry(), "riscv64", embedded, manifest, sig, k.pubString())
	require.NoError(t, err, "no URL and no sha256 on both sides: installing it fails later (DEY-B005)")

	// A signature that does not verify.
	for name, c := range map[string]struct {
		manifest, sig []byte
		why           string
	}{
		"another key":    {manifest, newTestKey(t).signLegacy(manifest), "not with the DEYROUTE release key"},
		"other content":  {manifest, k.signLegacy([]byte("other")), "the signature does not match the file"},
		"tampered":       {append(append([]byte{}, manifest...), "#"...), sig, "the signature does not match the file"},
		"no signature":   {manifest, nil, "the signature file is malformed"},
		"garbage":        {manifest, []byte("untrusted comment: x\nnope\n"), "the signature file is malformed"},
		"release key":    {manifest, sig, "not with the DEYROUTE release key"},
		"signed garbage": {[]byte("backends: ["), k.signLegacy([]byte("backends: [")), "cannot be read"},
	} {
		key := k.pubString()
		if name == "release key" {
			key = ""
		}
		_, err := CheckBackendEntry(pinnedEntry(), "amd64", embedded, c.manifest, c.sig, key)
		require.ErrorContains(t, err, c.why, name)
		require.ErrorContains(t, err, "backend backhaul v9.9.9 for amd64 is not pinned by this node's built-in backends.yaml", name)
	}
}

func TestFetchManifestSig(t *testing.T) {
	k := newTestKey(t)
	m := newMapFetcher()
	base := Source{Name: SourceReleaseBase, BaseURL: "https://cdn.example"}
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	ro := RetryOptions{Sleep: (&noSleep{}).sleep}
	data := pinnedManifest("")
	newer := pinnedManifest("    template_version: 2\n")
	// latest has another manifest; the release 1.4.0 has this one, signed
	// on the CDN with another key and on GitHub with the release key.
	m.files[gh.ManifestSig("")] = k.signLegacy(newer)
	m.files[base.ManifestSig("1.4.0")] = newTestKey(t).signLegacy(data)
	sig := k.signHashed(t, data)
	m.files[gh.ManifestSig("1.4.0")] = sig

	got, err := FetchManifestSig(context.Background(), m, []Source{base, gh}, []string{"", "1.4.0"}, data, k.pubString(), ro)
	require.NoError(t, err)
	require.Equal(t, sig, got)

	// No source signs these bytes → S001; nothing downloaded → I004.
	_, err = FetchManifestSig(context.Background(), m, []Source{base, gh}, []string{""}, data, k.pubString(), ro)
	requireCode(t, err, deyerr.S001)
	_, err = FetchManifestSig(context.Background(), newMapFetcher(), []Source{base, gh}, []string{"", "1.4.0"}, data, k.pubString(), ro)
	requireCode(t, err, deyerr.I004)
	_, err = FetchManifestSig(context.Background(), m, nil, []string{""}, data, k.pubString(), ro)
	requireCode(t, err, deyerr.I004)
}
