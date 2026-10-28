package node

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"aead.dev/minisign"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
)

// Backend installs (DR-03): a node installs a backend only when a signed
// DEYROUTE release pins the entry the hub sends, in the node's built-in
// backends.yaml or in a backends.yaml the hub sends along whose signature
// verifies with the release key built into the node.

// backendServer serves one backend archive over https and counts the
// downloads.
type backendServer struct {
	*httptest.Server
	archive []byte
	hits    atomic.Int32
}

func newBackendServer(t *testing.T, body string) *backendServer {
	t.Helper()
	bs := &backendServer{archive: tarGz(t, "backhaul", []byte(body))}
	bs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		bs.hits.Add(1)
		_, _ = w.Write(bs.archive)
	}))
	t.Cleanup(bs.Close)
	return bs
}

// entry is the backhaul entry ver of a manifest that is not built into the
// node: its amd64 archive is served by bs.
func (bs *backendServer) entry(ver string) backend.ManifestEntry {
	return backend.ManifestEntry{Name: "backhaul", Version: ver, Archive: "tar.gz", Binaries: []string{"backhaul"},
		URLs:   map[string]string{"amd64": bs.URL + "/backhaul_linux_amd64.tar.gz"},
		SHA256: map[string]string{"amd64": sum(bs.archive)}, TemplateVersion: 1}
}

// manifestOf is a backends.yaml pinning e (amd64 only), with extra lines
// appended to the entry.
func manifestOf(e backend.ManifestEntry, extra string) []byte {
	return []byte("manifest_version: 1\nbackends:\n  " + e.Name + ":\n" +
		"    version: " + e.Version + "\n    archive: tar.gz\n    binaries: [backhaul]\n" +
		"    urls: { amd64: \"" + e.URLs["amd64"] + "\" }\n" +
		"    sha256: { amd64: \"" + e.SHA256["amd64"] + "\" }\n    template_version: 1\n" + extra)
}

func signManifest(key minisign.PrivateKey, data []byte) []byte {
	return minisign.SignWithComments(key, data, "timestamp:1\tfile:backends.yaml", "test manifest")
}

// requireNoBackend checks that backhaul ver was not installed.
func (e *env) requireNoBackend(t *testing.T, ver, what string) {
	t.Helper()
	_, err := os.Stat(e.path("/var/lib/deyroute/bin/backhaul/" + ver))
	require.True(t, os.IsNotExist(err), "%s: nothing is installed", what)
}

// notFound answers every request with 404 and records the URLs.
type notFound struct {
	mu   sync.Mutex
	urls []string
}

// RoundTrip implements http.RoundTripper.
func (n *notFound) RoundTrip(req *http.Request) (*http.Response, error) {
	n.mu.Lock()
	n.urls = append(n.urls, req.URL.String())
	n.mu.Unlock()
	return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: http.Header{},
		Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

// An entry that the node's built-in backends.yaml does not pin, and no
// signed manifest from the hub, is refused before anything is downloaded.
// The entries of the built-in manifest are installed as before.
func TestBackendInstallRejectsUnknownEntry(t *testing.T) {
	bs := newBackendServer(t, "\x7fELF hub's own backhaul")
	e := newEnv(t)
	e.opts.HTTPClient = bs.Client()
	s := e.start()

	builtin, err := backend.ParseManifest(backend.EmbeddedManifest())
	require.NoError(t, err)
	pinned := builtin.Backends["backhaul"]
	cases := map[string]backend.ManifestEntry{
		"not in the manifest":                  bs.entry("v9.9.9"),
		"pinned version, other URL and sha256": bs.entry(pinned.Version),
		"pinned version and URL, other sha256": func() backend.ManifestEntry {
			x := pinned
			x.SHA256 = map[string]string{"amd64": sum(bs.archive), "arm64": pinned.SHA256["arm64"]}
			return x
		}(),
		"pinned URL and sha256, other version": func() backend.ManifestEntry {
			x := pinned
			x.Version = "v9.9.9"
			return x
		}(),
		"unknown backend": func() backend.ManifestEntry {
			x := bs.entry("v1.0.0")
			x.Name = "evil"
			return x
		}(),
	}
	for name, entry := range cases {
		_, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: entry.Name, Entry: entry})
		de := requireCode(t, err, deyerr.N050)
		require.Contains(t, de.Why(), "is not pinned by this node's built-in backends.yaml, and the hub sent no signed backends.yaml", name)
		require.Equal(t, int32(0), bs.hits.Load(), "%s: nothing is downloaded", name)
		e.requireNoBackend(t, entry.Version, name)
	}

	// The built-in entry itself passes the check: its download starts (and
	// fails here, the test has no internet).
	nf := &notFound{}
	e2 := newEnv(t)
	e2.opts.HTTPClient = &http.Client{Transport: nf}
	s2 := e2.start()
	_, err = call[api.BackendInstallResult](t, s2, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul", Entry: pinned})
	require.Error(t, err)
	require.False(t, deyerr.HasCode(err, deyerr.N050), "the built-in entry is not refused: %v", err)
	nf.mu.Lock()
	require.Equal(t, []string{pinned.URLs["amd64"]}, nf.urls)
	nf.mu.Unlock()
}

// A backends.yaml the hub sends along, signed with the release key built
// into the node (the lab key here), pins the entry: it is installed. A
// manifest of a newer build with keys this node does not know counts too,
// but it must pin exactly the entry the hub sends.
func TestBackendInstallAcceptsSignedOverride(t *testing.T) {
	key := useTestKey(t)
	bs := newBackendServer(t, "\x7fELF backhaul v9.9.9")
	e := newEnv(t)
	e.opts.HTTPClient = bs.Client()
	s := e.start()
	h := sha256.Sum256(backend.EmbeddedManifest())
	require.Equal(t, hex.EncodeToString(h[:]), s.Hello.ManifestSHA256, "the node announces its built-in manifest")

	entry := bs.entry("v9.9.9")
	manifest := manifestOf(entry, "    future_key: 1\n")
	args := api.BackendInstallArgs{Name: "backhaul", Entry: entry, Manifest: manifest, ManifestSig: signManifest(key, manifest)}
	res, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall, args)
	require.NoError(t, err)
	require.Equal(t, "/var/lib/deyroute/bin/backhaul/v9.9.9", res.BinDir)
	data, err := os.ReadFile(e.path("/var/lib/deyroute/bin/backhaul/v9.9.9/backhaul"))
	require.NoError(t, err)
	require.Equal(t, "\x7fELF backhaul v9.9.9", string(data))
	require.Contains(t, e.logs.String(), install.PinnedSigned)

	// The signed manifest pins another sha256 than the hub's entry.
	other := entry
	other.Version = "v9.9.8"
	pinnedOther := other
	pinnedOther.SHA256 = map[string]string{"amd64": sum([]byte("the signed archive"))}
	manifest = manifestOf(pinnedOther, "")
	hits := bs.hits.Load()
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall,
		api.BackendInstallArgs{Name: "backhaul", Entry: other, Manifest: manifest, ManifestSig: signManifest(key, manifest)})
	de := requireCode(t, err, deyerr.N050)
	require.Contains(t, de.Why(), "pinned neither by this node's built-in backends.yaml nor by the signed backends.yaml the hub sent")
	require.Equal(t, hits, bs.hits.Load(), "nothing is downloaded")
	e.requireNoBackend(t, "v9.9.8", "other sha256")
}

// A backends.yaml whose signature does not verify with the release key
// built into the node pins nothing: the entry is refused before anything
// is downloaded.
func TestBackendInstallRejectsBadOverrideSig(t *testing.T) {
	key := useTestKey(t)
	bs := newBackendServer(t, "\x7fELF backhaul v9.9.9")
	e := newEnv(t)
	e.opts.HTTPClient = bs.Client()
	s := e.start()

	entry := bs.entry("v9.9.9")
	manifest := manifestOf(entry, "")
	sig := signManifest(key, manifest)
	_, stranger, err := minisign.GenerateKey(nil)
	require.NoError(t, err)
	for name, c := range map[string]struct {
		manifest, sig []byte
		why           string
	}{
		"signed by another key":      {manifest, signManifest(stranger, manifest), "not with the DEYROUTE release key"},
		"signature of other content": {manifest, signManifest(key, []byte("manifest_version: 1\n")), "the signature does not match the file"},
		"manifest changed after":     {[]byte(strings.Replace(string(manifest), "v9.9.9", "v9.9.9 ", 1)), sig, "the signature does not match the file"},
		"no signature":               {manifest, nil, "the signature file is malformed"},
		"garbage signature":          {manifest, []byte("untrusted comment: x\nnot base64\n"), "the signature file is malformed"},
	} {
		_, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall,
			api.BackendInstallArgs{Name: "backhaul", Entry: entry, Manifest: c.manifest, ManifestSig: c.sig})
		de := requireCode(t, err, deyerr.N050)
		require.Contains(t, de.Why(), "the backends.yaml the hub sent is not signed with the DEYROUTE release key", name)
		require.Contains(t, de.Why(), c.why, name)
		require.Equal(t, int32(0), bs.hits.Load(), "%s: nothing is downloaded", name)
		e.requireNoBackend(t, "v9.9.9", name)
	}
}

// A node built without the lab key trusts only the release key: a manifest
// signed with any other key pins nothing.
func TestBackendInstallTrustsOnlyTheBuiltInKey(t *testing.T) {
	require.False(t, install.HasTestKey())
	_, key, err := minisign.GenerateKey(nil)
	require.NoError(t, err)
	bs := newBackendServer(t, "\x7fELF backhaul v9.9.9")
	e := newEnv(t)
	e.opts.HTTPClient = bs.Client()
	s := e.start()
	entry := bs.entry("v9.9.9")
	manifest := manifestOf(entry, "")
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall,
		api.BackendInstallArgs{Name: "backhaul", Entry: entry, Manifest: manifest, ManifestSig: signManifest(key, manifest)})
	de := requireCode(t, err, deyerr.N050)
	require.Contains(t, de.Why(), "not with the DEYROUTE release key")
	require.Equal(t, int32(0), bs.hits.Load())
	e.requireNoBackend(t, "v9.9.9", "release key only")
}

// A version already installed exactly as the entry pins it needs no signed
// manifest: nothing is downloaded or installed. A hub keeps the version a
// running tunnel uses after an update, and the node's new built-in
// manifest may no longer pin it. Any other sha256 for that version is
// checked as before.
func TestBackendInstallKeepsInstalledVersion(t *testing.T) {
	key := useTestKey(t)
	bs := newBackendServer(t, "\x7fELF backhaul v9.9.9")
	e := newEnv(t)
	e.opts.HTTPClient = bs.Client()
	s := e.start()
	entry := bs.entry("v9.9.9")
	manifest := manifestOf(entry, "")
	_, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall,
		api.BackendInstallArgs{Name: "backhaul", Entry: entry, Manifest: manifest, ManifestSig: signManifest(key, manifest)})
	require.NoError(t, err)
	require.Equal(t, int32(1), bs.hits.Load())

	res, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul", Entry: entry})
	require.NoError(t, err, "the installed version needs no manifest")
	require.Equal(t, "/var/lib/deyroute/bin/backhaul/v9.9.9", res.BinDir)
	require.Equal(t, int32(1), bs.hits.Load(), "nothing is downloaded")

	other := entry
	other.SHA256 = map[string]string{"amd64": sum([]byte("another archive"))}
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul", Entry: other})
	de := requireCode(t, err, deyerr.N050)
	require.Contains(t, de.Why(), "is not pinned by this node's built-in backends.yaml")
	// A changed binary is not the installed version either.
	writeFile(t, e.path("/var/lib/deyroute/bin/backhaul/v9.9.9/backhaul"), "changed", 0o755)
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul", Entry: entry})
	requireCode(t, err, deyerr.N050)
	require.Equal(t, int32(1), bs.hits.Load())
}
