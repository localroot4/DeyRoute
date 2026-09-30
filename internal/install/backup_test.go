package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func init() {
	// Keep age's scrypt fast in tests (production uses age's default).
	scryptWorkFactor = 10
}

const hubConfig = "schema_version: 1\nrole: hub\nhub:\n  name: ir-1\n  control_port: 44433\n"

// seedEtc writes a small /etc/deyroute tree under root.
func seedEtc(t *testing.T, root string) {
	t.Helper()
	etc := filepath.Join(root, "etc/deyroute")
	require.NoError(t, os.MkdirAll(filepath.Join(etc, "secrets/backend-tokens"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(etc, "backends/xray/main"), 0o750))
	require.NoError(t, os.Chmod(etc, 0o710))
	require.NoError(t, os.WriteFile(filepath.Join(etc, "config.yaml"), []byte(hubConfig), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(etc, "secrets/ca.key"), []byte("CA KEY"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(etc, "secrets/backend-tokens/main.token"), []byte("tok"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(etc, "backends/xray/main/config.json"), []byte("{}"), 0o640))
	require.NoError(t, os.Symlink("config.yaml", filepath.Join(etc, "link")))
}

func modeOf(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	require.NoError(t, err)
	return fi.Mode().Perm()
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	src := t.TempDir()
	seedEtc(t, src)
	now := time.Date(2026, 9, 29, 10, 11, 12, 0, time.UTC)
	events := "{\"seq\":1,\"type\":\"tunnel_up\"}\n{\"seq\":2,\"type\":\"switch_transport\"}\n"
	path, err := Backup(BackupOptions{Root: src, Passphrase: "correct horse", Events: strings.NewReader(events), Now: now, Version: "1.2.3"})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(src, "var/lib/deyroute/backups/deyroute-backup-20260929T101112Z.tar.gz.age"), path)
	require.Equal(t, os.FileMode(0o600), modeOf(t, path))
	require.Equal(t, os.FileMode(0o700), modeOf(t, filepath.Dir(path)))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(raw, []byte("age-encryption.org/v1")))
	require.NotContains(t, string(raw), "CA KEY")

	// Restore onto another server that already has a (different) config.
	dst := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dst, "etc/deyroute"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dst, "etc/deyroute/config.yaml"), []byte("old"), 0o600))

	// Wrong passphrase → S004, nothing changed.
	_, err = Restore(RestoreOptions{Root: dst, Path: path, Passphrase: "wrong"})
	requireCode(t, err, deyerr.S004)
	_, err = Restore(RestoreOptions{Root: dst, Path: path})
	de := requireCode(t, err, deyerr.S004)
	require.Contains(t, de.Why(), "passphrase")
	require.Equal(t, "old", readString(t, filepath.Join(dst, "etc/deyroute/config.yaml")))

	var validated []byte
	res, err := Restore(RestoreOptions{Root: dst, Path: path, Passphrase: "correct horse", Now: now,
		Validate: func(c []byte) error { validated = c; return nil }})
	require.NoError(t, err)
	require.Equal(t, hubConfig, string(validated))
	require.Equal(t, hubConfig, string(res.Config))
	require.Equal(t, events, string(res.Events))
	require.Equal(t, BackupFormat, res.Manifest.Format)
	require.Equal(t, "1.2.3", res.Manifest.Version)
	require.Equal(t, "hub", res.Manifest.Role)
	require.Equal(t, "ir-1", res.Manifest.HubName)
	require.Equal(t, 4, res.Manifest.Files)
	require.True(t, res.Manifest.Events)
	require.Equal(t, []string{"etc/deyroute/link"}, res.Manifest.Skipped)
	require.Equal(t, now, res.Manifest.Created)

	etc := filepath.Join(dst, "etc/deyroute")
	require.Equal(t, "CA KEY", readString(t, filepath.Join(etc, "secrets/ca.key")))
	require.Equal(t, "tok", readString(t, filepath.Join(etc, "secrets/backend-tokens/main.token")))
	require.Equal(t, os.FileMode(0o710), modeOf(t, etc))
	require.Equal(t, os.FileMode(0o700), modeOf(t, filepath.Join(etc, "secrets")))
	require.Equal(t, os.FileMode(0o600), modeOf(t, filepath.Join(etc, "secrets/ca.key")))
	require.Equal(t, os.FileMode(0o750), modeOf(t, filepath.Join(etc, "backends/xray/main")))
	require.Equal(t, os.FileMode(0o640), modeOf(t, filepath.Join(etc, "backends/xray/main/config.json")))
	_, err = os.Lstat(filepath.Join(etc, "link"))
	require.True(t, os.IsNotExist(err))

	// The replaced directory is kept; the temp directory is gone.
	require.Equal(t, filepath.Join(dst, "etc/deyroute.pre-restore-20260929T101112Z"), res.PreviousDir)
	require.Equal(t, "old", readString(t, filepath.Join(res.PreviousDir, "config.yaml")))
	_, err = os.Stat(filepath.Join(dst, "etc/deyroute.restore-tmp"))
	require.True(t, os.IsNotExist(err))

	// A second restore in the same second gets a distinct pre-restore name.
	res2, err := Restore(RestoreOptions{Root: dst, Path: path, Passphrase: "correct horse", Now: now})
	require.NoError(t, err)
	require.Equal(t, res.PreviousDir+"-1", res2.PreviousDir)
}

func TestBackupPlainAndValidation(t *testing.T) {
	src := t.TempDir()
	seedEtc(t, src)
	out := filepath.Join(t.TempDir(), "sub", "b.tar.gz")
	path, err := Backup(BackupOptions{Root: src, NoEncrypt: true, OutPath: out, HubName: "override"})
	require.NoError(t, err)
	require.Equal(t, out, path)

	// Restore onto an empty root (install.sh --no-setup, then restore).
	dst := t.TempDir()
	res, err := Restore(RestoreOptions{Root: dst, Path: path, Passphrase: "ignored"})
	require.NoError(t, err)
	require.Empty(t, res.PreviousDir)
	require.Nil(t, res.Events)
	require.Equal(t, "override", res.Manifest.HubName)
	require.Equal(t, hubConfig, readString(t, filepath.Join(dst, "etc/deyroute/config.yaml")))

	// Validation errors: DEY errors pass through, others become S005.
	cfgErr := deyerr.New(deyerr.C001, deyerr.Params{"key": "bogus", "line": 3})
	_, err = Restore(RestoreOptions{Root: dst, Path: path, Validate: func([]byte) error { return cfgErr }})
	requireCode(t, err, deyerr.C001)
	_, err = Restore(RestoreOptions{Root: dst, Path: path, Validate: func([]byte) error { return os.ErrInvalid }})
	requireCode(t, err, deyerr.S005)
	_, err = os.Stat(filepath.Join(dst, "etc/deyroute.restore-tmp"))
	require.True(t, os.IsNotExist(err), "failed restores clean up")
}

func TestBackupErrors(t *testing.T) {
	root := t.TempDir()
	_, err := Backup(BackupOptions{Root: root, Passphrase: "x"})
	requireCode(t, err, deyerr.C014)
	seedEtc(t, root)
	_, err = Backup(BackupOptions{Root: root})
	requireCode(t, err, deyerr.S008)
	p, err := Backup(BackupOptions{Root: root, NoEncrypt: true, Now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	require.NoError(t, err)
	require.Equal(t, "deyroute-backup-20260102T030405Z.tar.gz", filepath.Base(p))
	_, err = Backup(BackupOptions{Root: root, NoEncrypt: true, Events: strings.NewReader(strings.Repeat("x", 10)),
		OutPath: filepath.Join(root, "etc/deyroute/config.yaml", "impossible")})
	requireCode(t, err, deyerr.X032)
}

// tarGz builds a raw backup-like archive for negative restore tests.
func tarGz(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Type
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.Name, Typeflag: typ, Mode: 0o600, Linkname: e.Linkname}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.Body))
		}
		require.NoError(t, tw.WriteHeader(h))
		_, err := tw.Write([]byte(e.Body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return writeTemp(t, "b.tar.gz", buf.Bytes())
}

func TestRestoreRejectsInvalidBackups(t *testing.T) {
	manifest := `{"format":"deyroute-backup","format_version":1}`
	cases := map[string]struct {
		path string
		code deyerr.Code
	}{
		"not a backup file":  {writeTemp(t, "x", []byte("hello world, this is text")), deyerr.S005},
		"missing file":       {filepath.Join(t.TempDir(), "nope"), deyerr.S005},
		"garbage gzip":       {writeTemp(t, "x", []byte{0x1f, 0x8b, 0, 1, 2}), deyerr.S005},
		"no manifest":        {tarGz(t, []tarEntry{{Name: "etc/deyroute/config.yaml", Body: "x"}}), deyerr.S005},
		"foreign manifest":   {tarGz(t, []tarEntry{{Name: "manifest.json", Body: `{"format":"other"}`}, {Name: "etc/deyroute/config.yaml", Body: "x"}}), deyerr.S005},
		"bad manifest json":  {tarGz(t, []tarEntry{{Name: "manifest.json", Body: `{`}}), deyerr.S005},
		"future format":      {tarGz(t, []tarEntry{{Name: "manifest.json", Body: `{"format":"deyroute-backup","format_version":99}`}, {Name: "etc/deyroute/config.yaml", Body: "x"}}), deyerr.S005},
		"no config":          {tarGz(t, []tarEntry{{Name: "manifest.json", Body: manifest}, {Name: "etc/deyroute/secrets/ca.key", Body: "k"}}), deyerr.S005},
		"traversal":          {tarGz(t, []tarEntry{{Name: "manifest.json", Body: manifest}, {Name: "etc/deyroute/../../evil", Body: "x"}}), deyerr.S005},
		"symlink":            {tarGz(t, []tarEntry{{Name: "manifest.json", Body: manifest}, {Name: "etc/deyroute/l", Type: tar.TypeSymlink, Linkname: "/etc/shadow"}}), deyerr.S005},
		"duplicate":          {tarGz(t, []tarEntry{{Name: "manifest.json", Body: manifest}, {Name: "etc/deyroute/config.yaml", Body: "a"}, {Name: "etc/deyroute/config.yaml", Body: "b"}}), deyerr.S005},
		"truncated age file": {writeTemp(t, "x.age", []byte("age-encryption.org/v1\n-> scrypt")), deyerr.S005},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			_, err := Restore(RestoreOptions{Root: root, Path: c.path, Passphrase: "p"})
			requireCode(t, err, c.code)
			_, statErr := os.Stat(filepath.Join(root, "etc/deyroute"))
			require.True(t, os.IsNotExist(statErr))
			_, statErr = os.Stat(filepath.Join(root, "evil"))
			require.True(t, os.IsNotExist(statErr))
		})
	}
	// Unknown entries are ignored (forward compatibility).
	root := t.TempDir()
	p := tarGz(t, []tarEntry{{Name: "manifest.json", Body: manifest}, {Name: "etc/deyroute/config.yaml", Body: "c"}, {Name: "future.bin", Body: "?"}})
	_, err := Restore(RestoreOptions{Root: root, Path: p})
	require.NoError(t, err)
}

func TestRestoreDamagedEncryptedBackup(t *testing.T) {
	src := t.TempDir()
	seedEtc(t, src)
	path, err := Backup(BackupOptions{Root: src, Passphrase: "pw"})
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	raw[len(raw)-5] ^= 0xff
	damaged := writeTemp(t, "damaged.tar.gz.age", raw)
	_, err = Restore(RestoreOptions{Root: t.TempDir(), Path: damaged, Passphrase: "pw"})
	requireCode(t, err, deyerr.S004)
}

func TestAutoBackupPrunes(t *testing.T) {
	root := t.TempDir()
	seedEtc(t, root)
	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	var paths []string
	for i := 0; i < 5; i++ {
		p, err := autoBackup(root, 3, base.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), modeOf(t, p))
		paths = append(paths, p)
	}
	got, err := AutoBackups(root)
	require.NoError(t, err)
	require.Equal(t, paths[2:], got, "the 3 newest are kept")
	require.Equal(t, filepath.Join(root, "var/lib/deyroute/backups/auto"), filepath.Dir(got[0]))

	// The public wrapper uses the default keep (20) and the real clock.
	p, err := AutoBackup(root, 0)
	require.NoError(t, err)
	got, err = AutoBackups(root)
	require.NoError(t, err)
	require.Len(t, got, 4)
	require.Equal(t, p, got[3])

	// Restoring an auto backup works (unencrypted).
	_, err = Restore(RestoreOptions{Root: t.TempDir(), Path: p})
	require.NoError(t, err)

	none, err := AutoBackups(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, none)
	_, err = AutoBackup(t.TempDir(), 1)
	requireCode(t, err, deyerr.C014)
}

func TestConfigIdentity(t *testing.T) {
	role, hub, node := configIdentity([]byte("role: node\nnode:\n  id: de-1\n"))
	require.Equal(t, []string{"node", "", "de-1"}, []string{role, hub, node})
	role, hub, node = configIdentity([]byte("{{{"))
	require.Equal(t, []string{"", "", ""}, []string{role, hub, node})
}

func TestOwnersPreferNames(t *testing.T) {
	o := newOwners()
	uid, gid := o.ids(&tar.Header{Uname: "root", Gname: "root", Uid: 4242, Gid: 4343})
	require.Equal(t, 0, uid)
	require.Equal(t, 0, gid)
	uid, gid = o.ids(&tar.Header{Uname: "root", Gname: "root", Uid: 1, Gid: 1})
	require.Equal(t, []int{0, 0}, []int{uid, gid}, "cached")
	uid, gid = o.ids(&tar.Header{Uname: "no-such-user-dey", Gname: "no-such-group-dey", Uid: 4242, Gid: 4343})
	require.Equal(t, []int{4242, 4343}, []int{uid, gid})
	uid, gid = o.ids(&tar.Header{Uid: 7, Gid: 8})
	require.Equal(t, []int{7, 8}, []int{uid, gid})
}
