package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func frpEntry(url string, archive []byte) backend.ManifestEntry {
	return backend.ManifestEntry{
		Name:     "frp",
		Version:  "v0.71.0",
		Archive:  KindTarGz,
		Binaries: []string{"frps", "frpc"},
		URLs:     map[string]string{"amd64": url},
		SHA256:   map[string]string{"amd64": sha(archive)},
	}
}

func TestInstallBackend(t *testing.T) {
	root := t.TempDir()
	l := Layout{Root: root}
	archive := makeTarGz(t, []tarEntry{
		{Name: "frp_0.71.0_linux_amd64/frps", Body: "FRPS"},
		{Name: "frp_0.71.0_linux_amd64/frpc", Body: "FRPC"},
	})
	const url = "{mirror}/backends/frp/frp_0.71.0_linux_amd64.tar.gz"
	resolved := "https://mirror.example/backends/frp/frp_0.71.0_linux_amd64.tar.gz"
	m := newMapFetcher()
	m.files[resolved] = archive
	e := frpEntry(url, archive)

	dir, err := l.InstallBackend(context.Background(), e, "amd64", m, "https://mirror.example/")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "var/lib/deyroute/bin/frp/v0.71.0"), dir)
	require.Equal(t, dir, l.BinDir("frp", "v0.71.0"))
	requireBinary(t, filepath.Join(dir, "frps"), "FRPS")
	requireBinary(t, l.BinaryPath("frp", "v0.71.0", "frpc"), "FRPC")
	sum, err := os.ReadFile(filepath.Join(dir, "frps.sha256"))
	require.NoError(t, err)
	require.Equal(t, sha([]byte("FRPS"))+"  frps\n", string(sum))
	marker, err := os.ReadFile(filepath.Join(dir, ArchiveMarker))
	require.NoError(t, err)
	require.Equal(t, sha(archive)+"\n", string(marker))
	require.Equal(t, []string{sha(archive)}, m.shas, "the manifest sha256 reaches the (via-node) fetcher")
	// No staging leftovers.
	ents, err := os.ReadDir(l.BackendDir("frp"))
	require.NoError(t, err)
	require.Len(t, ents, 1)
	require.NoError(t, l.VerifyBackend(e))

	// Second install: nothing downloaded, nothing overwritten.
	fi1, _ := os.Stat(filepath.Join(dir, "frps"))
	dir2, err := l.InstallBackend(context.Background(), e, "amd64", m, "https://mirror.example")
	require.NoError(t, err)
	require.Equal(t, dir, dir2)
	require.Equal(t, 1, m.count(resolved))
	fi2, _ := os.Stat(filepath.Join(dir, "frps"))
	require.Equal(t, fi1.ModTime(), fi2.ModTime())

	vers, err := l.InstalledVersions("frp")
	require.NoError(t, err)
	require.Equal(t, []string{"v0.71.0"}, vers)
}

func TestInstallBackendNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	l := Layout{Root: root}
	archive := makeTarGz(t, []tarEntry{{Name: "frps", Body: "FRPS"}, {Name: "frpc", Body: "FRPC"}})
	m := newMapFetcher()
	m.files["https://x/frp.tgz"] = archive
	e := frpEntry("https://x/frp.tgz", archive)
	dir, err := l.InstallBackend(context.Background(), e, "amd64", m, "")
	require.NoError(t, err)

	// A modified binary is detected and left alone.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frps"), []byte("EVIL"), 0o755))
	_, err = l.InstallBackend(context.Background(), e, "amd64", m, "")
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Fix(), "deyroute update backends frp")
	got, _ := os.ReadFile(filepath.Join(dir, "frps"))
	require.Equal(t, "EVIL", string(got))
	requireCode(t, l.VerifyBackend(e), deyerr.S001)

	// The manifest pins another archive for the same version.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "frps"), []byte("FRPS"), 0o755))
	e2 := e
	e2.SHA256 = map[string]string{"amd64": strings.Repeat("0", 64)}
	_, err = l.InstallBackend(context.Background(), e2, "amd64", m, "")
	requireCode(t, err, deyerr.S001)
	require.Equal(t, 1, m.count("https://x/frp.tgz"))

	// A missing marker is not trusted either.
	require.NoError(t, os.Remove(filepath.Join(dir, ArchiveMarker)))
	_, err = l.InstallBackend(context.Background(), e, "amd64", m, "")
	requireCode(t, err, deyerr.S001)

	// A file where the version directory should be.
	e3 := e
	e3.Version = "v9"
	require.NoError(t, os.WriteFile(l.BinDir("frp", "v9"), []byte("x"), 0o600))
	_, err = l.InstallBackend(context.Background(), e3, "amd64", m, "")
	requireCode(t, err, deyerr.S001)
}

func TestInstallBackendErrors(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	ctx := context.Background()
	m := newMapFetcher()

	dir, err := l.InstallBackend(ctx, backend.ManifestEntry{Name: "direct", Version: "builtin", Builtin: true}, "amd64", m, "")
	require.NoError(t, err)
	require.Empty(t, dir)
	dir, err = l.InstallBackend(ctx, backend.ManifestEntry{Name: "haproxy", System: true}, "amd64", nil, "")
	require.NoError(t, err)
	require.Empty(t, dir)

	_, err = l.InstallBackend(ctx, backend.ManifestEntry{Name: "nope"}, "amd64", m, "")
	requireCode(t, err, deyerr.B008)
	_, err = l.InstallBackend(ctx, backend.ManifestEntry{Name: "../x", Version: "v1"}, "amd64", m, "")
	requireCode(t, err, deyerr.B008)
	_, err = l.InstallBackend(ctx, backend.ManifestEntry{Name: "x", Version: ".."}, "amd64", m, "")
	requireCode(t, err, deyerr.B008)

	e := backend.ManifestEntry{Name: "xray", Version: "v26.3.27", Archive: KindZip, Binaries: []string{"xray"},
		URLs: map[string]string{"amd64": "https://x/xray.zip"}, SHA256: map[string]string{"amd64": ""}}
	de := requireCode(t, func() error { _, err := l.InstallBackend(ctx, e, "arm64", m, ""); return err }(), deyerr.B005)
	require.Equal(t, "xray has no binary for arm64", de.Message())
	de = requireCode(t, func() error { _, err := l.InstallBackend(ctx, e, "amd64", m, ""); return err }(), deyerr.S006)
	require.Equal(t, "Manifest has no sha256 for xray (amd64)", de.Message())
	e.SHA256["amd64"] = "not-hex"
	requireCode(t, func() error { _, err := l.InstallBackend(ctx, e, "amd64", m, ""); return err }(), deyerr.S006)
	require.Zero(t, m.count("https://x/xray.zip"))

	// Download failure → B001 with backend/version.
	zipData := makeZip(t, []zipEntry{{Name: "xray", Body: "X"}})
	e.SHA256["amd64"] = sha(zipData)
	de = requireCode(t, func() error {
		_, err := l.installBackend(ctx, e, "amd64", m, "", RetryOptions{Sleep: (&noSleep{}).sleep})
		return err
	}(), deyerr.B001)
	require.Equal(t, "Could not download xray v26.3.27", de.Message())
	requireCode(t, func() error { _, err := l.InstallBackend(ctx, e, "amd64", nil, ""); return err }(), deyerr.B001)

	// Checksum mismatch → S001.
	m.files["https://x/xray.zip"] = []byte("tampered")
	requireCode(t, func() error { _, err := l.InstallBackend(ctx, e, "amd64", m, ""); return err }(), deyerr.S001)

	// Archive without the binary → B001 with params.
	noBin := makeZip(t, []zipEntry{{Name: "README", Body: "x"}})
	m.files["https://x/xray.zip"] = noBin
	e.SHA256["amd64"] = sha(noBin)
	de = requireCode(t, func() error { _, err := l.InstallBackend(ctx, e, "amd64", m, ""); return err }(), deyerr.B001)
	require.Equal(t, "Could not download xray v26.3.27", de.Message())
	require.Contains(t, de.Detail, "binary not found")
	_, err = os.Stat(l.BinDir("xray", "v26.3.27"))
	require.True(t, os.IsNotExist(err))

	// Success with a raw binary and default Binaries.
	raw := []byte("HY2")
	m.files["https://x/hy"] = raw
	hy := backend.ManifestEntry{Name: "hysteria2", Version: "app/v2.12.3", Archive: KindRaw,
		URLs: map[string]string{"amd64": "https://x/hy"}, SHA256: map[string]string{"amd64": sha(raw)}}
	dir, err = l.InstallBackend(ctx, hy, "amd64", m, "")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(l.BackendDir("hysteria2"), "app%2Fv2.12.3"), dir)
	requireBinary(t, filepath.Join(dir, "hysteria2"), "HY2")
	vers, err := l.InstalledVersions("hysteria2")
	require.NoError(t, err)
	require.Equal(t, []string{"app/v2.12.3"}, vers)
}

// Manifest binary names are plain file names: nothing outside the version
// directory is ever read or written, not even for the "already installed" check.
func TestInstallBackendRejectsUnsafeBinaryNames(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	m := newMapFetcher()
	for _, bad := range []string{"../../../etc/passwd", "a/b", ".hidden", ".."} {
		e := backend.ManifestEntry{Name: "evil", Version: "v1", Archive: KindRaw, Binaries: []string{bad},
			URLs: map[string]string{"amd64": "https://x/e"}, SHA256: map[string]string{"amd64": sha([]byte("E"))}}
		_, err := l.InstallBackend(context.Background(), e, "amd64", m, "")
		requireCode(t, err, deyerr.B008)
		requireCode(t, l.VerifyBackend(e), deyerr.B008)
	}
	require.Zero(t, m.count("https://x/e"))
	requireCode(t, l.VerifyBackend(backend.ManifestEntry{Name: "../x", Version: "v1"}), deyerr.B008)
}

// A manifest URL on the owner mirror without a configured mirror fails at
// once with B001 (no retries against a relative URL).
func TestInstallBackendNeedsMirror(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	raw := []byte("AWG")
	e := backend.ManifestEntry{Name: "amneziawg", Version: "v0.2.12", Archive: KindRaw, Binaries: []string{"amneziawg-go"},
		URLs:   map[string]string{"amd64": "{mirror}/backends/amneziawg-go/v0.2.12/amneziawg-go-linux-amd64"},
		SHA256: map[string]string{"amd64": sha(raw)}}
	m := newMapFetcher()
	de := requireCode(t, func() error { _, err := l.InstallBackend(context.Background(), e, "amd64", m, " "); return err }(), deyerr.B001)
	require.Contains(t, de.Detail, "no mirror is configured")
	require.Empty(t, m.calls)

	m.files["https://mirror.example/backends/amneziawg-go/v0.2.12/amneziawg-go-linux-amd64"] = raw
	dir, err := l.InstallBackend(context.Background(), e, "amd64", m, "https://mirror.example/")
	require.NoError(t, err)
	requireBinary(t, filepath.Join(dir, "amneziawg-go"), "AWG")
}

func TestInstalledVersionsAndRemove(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	for _, v := range []string{"v1.10.0", "v1.2.0", "v1.9.1", ".stage-123"} {
		require.NoError(t, os.MkdirAll(l.BinDir("gost", v), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(l.BackendDir("gost"), "stray-file"), nil, 0o600))
	vers, err := l.InstalledVersions("gost")
	require.NoError(t, err)
	require.Equal(t, []string{"v1.2.0", "v1.9.1", "v1.10.0"}, vers)

	none, err := l.InstalledVersions("chisel")
	require.NoError(t, err)
	require.Empty(t, none)
	_, err = l.InstalledVersions("../etc")
	requireCode(t, err, deyerr.B008)

	require.NoError(t, l.RemoveBackendVersions("gost", []string{"v1.10.0", "v1.9.1"}))
	vers, err = l.InstalledVersions("gost")
	require.NoError(t, err)
	require.Equal(t, []string{"v1.9.1", "v1.10.0"}, vers)
	_, err = os.Stat(l.BinDir("gost", ".stage-123"))
	require.True(t, os.IsNotExist(err), "stale staging directories are cleaned")

	require.NoError(t, l.RemoveBackendVersions("gost", nil))
	_, err = os.Stat(l.BackendDir("gost"))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, l.RemoveBackendVersions("gost", nil), "idempotent")
	requireCode(t, l.RemoveBackendVersions("", nil), deyerr.B008)

	requireCode(t, l.VerifyBackend(backend.ManifestEntry{Name: "gost", Version: "v3"}), deyerr.B001)
	require.NoError(t, l.VerifyBackend(backend.ManifestEntry{Name: "direct", Builtin: true}))
	require.Equal(t, "/var/lib/deyroute/bin/x/v1", Layout{}.BinDir("x", "v1"))
}
