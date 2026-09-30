package install

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func requireBinary(t *testing.T, p, body string) {
	t.Helper()
	got, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, body, string(got))
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
}

func TestExtractTarGz(t *testing.T) {
	arc := writeTemp(t, "frp.tar.gz", makeTarGz(t, []tarEntry{
		{Name: "frp_0.71.0_linux_amd64/", Type: tar.TypeDir},
		{Name: "frp_0.71.0_linux_amd64/frps", Body: "server", Mode: 0o644},
		{Name: "frp_0.71.0_linux_amd64/frpc", Body: "client"},
		{Name: "frp_0.71.0_linux_amd64/LICENSE", Body: "mit"},
		{Name: "frp_0.71.0_linux_amd64/link", Type: tar.TypeSymlink, Linkname: "/etc/passwd"},
	}))
	dst := filepath.Join(t.TempDir(), "out")
	got, err := Extract(arc, KindTarGz, []string{"frps", "frpc"}, dst)
	require.NoError(t, err)
	require.Len(t, got, 2)
	requireBinary(t, got["frps"], "server")
	requireBinary(t, got["frpc"], "client")
	_, err = os.Stat(filepath.Join(dst, "LICENSE"))
	require.True(t, os.IsNotExist(err), "unwanted entries are not extracted")
	_, err = os.Lstat(filepath.Join(dst, "link"))
	require.True(t, os.IsNotExist(err))
}

func TestExtractTarGzRefusals(t *testing.T) {
	cases := map[string][]tarEntry{
		"traversal":      {{Name: "../../evil", Body: "x"}, {Name: "xray", Body: "ok"}},
		"absolute":       {{Name: "/usr/bin/xray", Body: "x"}},
		"symlink":        {{Name: "dir/xray", Type: tar.TypeSymlink, Linkname: "/bin/sh"}},
		"hardlink":       {{Name: "xray", Type: tar.TypeLink, Linkname: "../../etc/shadow"}},
		"duplicate":      {{Name: "a/xray", Body: "1"}, {Name: "b/xray", Body: "2"}},
		"missing binary": {{Name: "README", Body: "x"}},
		"empty binary":   {{Name: "xray", Body: ""}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			arc := writeTemp(t, "a.tar.gz", makeTarGz(t, entries))
			dst := t.TempDir()
			_, err := Extract(arc, KindTarGz, []string{"xray"}, dst)
			de := requireCode(t, err, deyerr.B001)
			require.NotEmpty(t, de.Detail)
			_, statErr := os.Stat(filepath.Join(dst, "xray"))
			require.True(t, os.IsNotExist(statErr))
			// Nothing escaped the target directory.
			_, statErr = os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dst)), "evil"))
			require.True(t, os.IsNotExist(statErr))
		})
	}
	_, err := Extract(writeTemp(t, "bad.tar.gz", []byte("not gzip")), KindTarGz, []string{"x"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	_, err = Extract(writeTemp(t, "bad.tar.gz", makeGz(t, "not a tar archive at all, just text")), KindTarGz, []string{"x"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
}

func TestExtractSizeCap(t *testing.T) {
	old := extractLimit
	extractLimit = 16
	t.Cleanup(func() { extractLimit = old })
	big := "0123456789abcdefXYZ"
	_, err := Extract(writeTemp(t, "a.tar.gz", makeTarGz(t, []tarEntry{{Name: "xray", Body: big}})), KindTarGz, []string{"xray"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	_, err = Extract(writeTemp(t, "a.zip", makeZip(t, []zipEntry{{Name: "xray", Body: big}})), KindZip, []string{"xray"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	de := requireCode(t, func() error {
		_, err := Extract(writeTemp(t, "a.gz", makeGz(t, big)), KindGz, []string{"chisel"}, t.TempDir())
		return err
	}(), deyerr.B001)
	require.Contains(t, de.Detail, "larger than")
	_, err = Extract(writeTemp(t, "raw", []byte(big)), KindRaw, []string{"hysteria"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
}

func TestExtractZip(t *testing.T) {
	arc := writeTemp(t, "xray.zip", makeZip(t, []zipEntry{
		{Name: "geoip.dat", Body: "geo"},
		{Name: "xray", Body: "XRAY"},
		{Name: "docs/", Body: ""},
	}))
	dst := t.TempDir()
	got, err := Extract(arc, KindZip, []string{"xray"}, dst)
	require.NoError(t, err)
	requireBinary(t, got["xray"], "XRAY")

	for name, entries := range map[string][]zipEntry{
		"traversal": {{Name: "../xray", Body: "x"}},
		"windows":   {{Name: `..\..\xray`, Body: "x"}},
		"symlink":   {{Name: "xray", Body: "/bin/sh", Symlink: true}},
		"duplicate": {{Name: "a/xray", Body: "1"}, {Name: "b/xray", Body: "2"}},
		"missing":   {{Name: "other", Body: "1"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(writeTemp(t, "a.zip", makeZip(t, entries)), KindZip, []string{"xray"}, t.TempDir())
			requireCode(t, err, deyerr.B001)
		})
	}
	_, err = Extract(writeTemp(t, "a.zip", []byte("PK nope")), KindZip, []string{"xray"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
}

func TestExtractGzAndRaw(t *testing.T) {
	got, err := Extract(writeTemp(t, "chisel.gz", makeGz(t, "CHISEL")), KindGz, []string{"chisel"}, t.TempDir())
	require.NoError(t, err)
	requireBinary(t, got["chisel"], "CHISEL")

	got, err = Extract(writeTemp(t, "hysteria-linux-amd64", []byte("HY2")), KindRaw, []string{"hysteria"}, t.TempDir())
	require.NoError(t, err)
	requireBinary(t, got["hysteria"], "HY2")

	got, err = Extract(writeTemp(t, "x", []byte("DEFAULT")), "", []string{"x"}, t.TempDir())
	require.NoError(t, err)
	requireBinary(t, got["x"], "DEFAULT")

	_, err = Extract(writeTemp(t, "x.gz", []byte("plain")), KindGz, []string{"x"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	_, err = Extract(writeTemp(t, "x", []byte("a")), KindGz, []string{"a", "b"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	_, err = Extract(writeTemp(t, "x", []byte("a")), KindRaw, []string{"a", "b"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	_, err = Extract(t.TempDir(), KindRaw, []string{"a"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
	_, err = Extract(filepath.Join(t.TempDir(), "missing"), KindRaw, []string{"a"}, t.TempDir())
	requireCode(t, err, deyerr.B001)
}

func TestExtractArguments(t *testing.T) {
	arc := writeTemp(t, "x", []byte("a"))
	for _, names := range [][]string{nil, {""}, {"../x"}, {"a/b"}, {".."}, {`a\b`}} {
		_, err := Extract(arc, KindRaw, names, t.TempDir())
		requireCode(t, err, deyerr.B001)
	}
	_, err := Extract(arc, "rar", []string{"x"}, t.TempDir())
	de := requireCode(t, err, deyerr.B001)
	require.Contains(t, de.Detail, "rar")
	require.False(t, safeEntryName("C:/x"))
	require.True(t, safeEntryName("./a/b"))
}
