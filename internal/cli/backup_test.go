package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// writeFixtureBackup writes a backup in the install format (tar.gz, age
// with a low scrypt work factor when pass is set) holding manifest.json and
// etc/deyroute/config.yaml, so tests decrypt it in milliseconds.
func writeFixtureBackup(t *testing.T, dir, cfg, pass string) string {
	t.Helper()
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: testNow}))
		_, err := tw.Write(data)
		require.NoError(t, err)
	}
	add("manifest.json", []byte(`{"format":"deyroute-backup","format_version":1,"version":"1.0.0","created":"2026-09-30T11:00:00Z"}`))
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "etc/deyroute/", Typeflag: tar.TypeDir, Mode: 0o710, ModTime: testNow}))
	add("etc/deyroute/config.yaml", []byte(cfg))
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	data := tb.Bytes()
	name := "fixture.tar.gz"
	if pass != "" {
		r, err := age.NewScryptRecipient(pass)
		require.NoError(t, err)
		r.SetWorkFactor(10)
		var eb bytes.Buffer
		w, err := age.Encrypt(&eb, r)
		require.NoError(t, err)
		_, err = io.Copy(w, bytes.NewReader(data))
		require.NoError(t, err)
		require.NoError(t, w.Close())
		data, name = eb.Bytes(), "fixture.tar.gz.age"
	}
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func TestBackup(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	e.stub.EventsFn = func(context.Context, api.EventQuery) ([]state.Event, error) {
		return []state.Event{{Seq: 2, At: testNow, Type: state.EvTunnelUp, Tunnel: "main"}, {Seq: 1, At: testNow, Type: state.EvTunnelDown, Tunnel: "main"}}, nil
	}
	// Non-TTY without a passphrase: S008.
	require.Contains(t, e.fail(1, "backup"), "DEY-S008")
	// Plain backup with events from the daemon.
	out := e.ok("backup", "--no-encrypt", "--out", filepath.Join(e.root, "b.tar.gz"))
	require.Contains(t, out, "Backup written: "+filepath.Join(e.root, "b.tar.gz"))
	require.Contains(t, out, "not encrypted")
	info, err := inspectBackup(filepath.Join(e.root, "b.tar.gz"), "")
	require.NoError(t, err)
	require.Equal(t, "hub", info.Role)
	require.Equal(t, "ir-1", info.HubName)
	require.Equal(t, "5.6.7.8", info.PublicIP)
	require.Equal(t, 44433, info.ControlPort)
	require.False(t, info.Encrypted)

	// Encrypted with the environment passphrase, default location (one
	// real age encryption and decryption: slow on purpose).
	e.vars[EnvBackupPassphrase] = "correct horse battery"
	doc := e.json("backup")
	require.Equal(t, true, doc["encrypted"])
	require.EqualValues(t, 2, doc["events"])
	path := doc["path"].(string)
	require.FileExists(t, path)
	require.Contains(t, path, filepath.Join(e.root, "var/lib/deyroute/backups"))
	info, err = inspectBackup(path, "correct horse battery")
	require.NoError(t, err)
	require.True(t, info.Encrypted)
	require.Equal(t, "hub", info.Role)
	_, err = inspectBackup(path, "")
	require.True(t, deyerr.HasCode(err, deyerr.S004), "%v", err)

	// A TTY whose passphrase is empty is S008; the daemon being down only
	// drops the events.
	delete(e.vars, EnvBackupPassphrase)
	e.down()
	e.tty()
	e.g.ReadPassword = func(string) (string, error) { return "", nil }
	require.Contains(t, e.fail(1, "backup"), "DEY-S008")
	out = e.ok("backup", "--no-encrypt", "--out", filepath.Join(e.root, "c.tar.gz"))
	require.Contains(t, out, "no event history")

	// Nothing set up: C014 from the backup itself.
	e2 := newEnv(t)
	require.Contains(t, e2.fail(1, "backup", "--no-encrypt"), "DEY-C014")
}

func TestPassphrasePrompts(t *testing.T) {
	e := newEnv(t)
	e.tty()
	answers := []string{"one-pass", "other", "two-pass", "two-pass"}
	e.g.ReadPassword = func(string) (string, error) {
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	p, err := e.g.newPassphrase()
	require.NoError(t, err)
	require.Equal(t, "two-pass", p)
	require.Contains(t, e.out.String(), "passphrases differ")
	n := 0
	e.g.ReadPassword = func(string) (string, error) {
		n++
		if n%2 == 0 {
			return "b", nil
		}
		return "a", nil
	}
	_, err = e.g.newPassphrase()
	require.ErrorIs(t, err, errAborted)
	e.g.ReadPassword = func(string) (string, error) { return "", errAborted }
	_, err = e.g.newPassphrase()
	require.ErrorIs(t, err, errAborted)
	_, err = e.g.passphrase()
	require.ErrorIs(t, err, errAborted)
	e.vars[EnvBackupPassphrase] = "from-env-pass"
	p, err = e.g.passphrase()
	require.NoError(t, err)
	require.Equal(t, "from-env-pass", p)
	p, err = e.g.newPassphrase()
	require.NoError(t, err)
	require.Equal(t, "from-env-pass", p)
}

func TestInspectBackupErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := inspectBackup(filepath.Join(dir, "missing"), "")
	require.True(t, deyerr.HasCode(err, deyerr.S005))
	p := filepath.Join(dir, "junk")
	require.NoError(t, os.WriteFile(p, []byte("not a backup"), 0o600))
	_, err = inspectBackup(p, "")
	require.True(t, deyerr.HasCode(err, deyerr.S005))
	enc, err := isEncrypted(p)
	require.NoError(t, err)
	require.False(t, enc)
	_, err = isEncrypted(filepath.Join(dir, "missing"))
	require.True(t, deyerr.HasCode(err, deyerr.S005))

	f := writeFixtureBackup(t, dir, hubConfig, "fixture-pass")
	enc, err = isEncrypted(f)
	require.NoError(t, err)
	require.True(t, enc)
	info, err := inspectBackup(f, "fixture-pass")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", info.Version)
	require.Equal(t, "ir-1", info.HubName)
	_, err = inspectBackup(f, "wrong-pass")
	require.True(t, deyerr.HasCode(err, deyerr.S004), "%v", err)
	// A config that does not decode is its own error.
	bad := writeFixtureBackup(t, t.TempDir(), "role: hub\nbogus: 1\nschema_version: 1\n", "")
	_, err = inspectBackup(bad, "")
	require.Error(t, err)
	// A tar without config.yaml is not a backup.
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	empty := filepath.Join(dir, "empty.tar.gz")
	require.NoError(t, os.WriteFile(empty, tb.Bytes(), 0o600))
	_, err = inspectBackup(empty, "")
	require.True(t, deyerr.HasCode(err, deyerr.S005))
}

func TestRestore(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	path := writeFixtureBackup(t, t.TempDir(), hubConfig, "")
	var ro setup.RestoreOptions
	e.g.Ops.Restore = func(_ context.Context, o setup.RestoreOptions) (*setup.RestoreResult, error) {
		ro = o
		steps(o.Progress, api.Step{Title: setup.StepTitle(setup.StepRestore), Status: api.StepOK})
		ip := "5.6.7.8"
		if o.PublicIP != "" {
			ip = o.PublicIP
		}
		return &setup.RestoreResult{Role: "hub", HubName: "ir-1", PublicIP: ip, AddressChanged: o.PublicIP != "", PreviousDir: "/etc/deyroute.pre-restore-x"}, nil
	}
	// Same address: no move, confirmation needed.
	errOut := e.fail(3, "restore", path)
	require.Contains(t, errOut, "replaces this server's /etc/deyroute")
	require.Contains(t, errOut, "hub ir-1")
	require.Contains(t, errOut, "deyroute 1.0.0")
	require.Empty(t, ro.Path)
	out := e.ok("restore", path, "--yes")
	require.Equal(t, path, ro.Path)
	require.Empty(t, ro.Passphrase)
	require.Empty(t, ro.PublicIP)
	require.True(t, ro.ApplySysctl)
	require.False(t, ro.StartService)
	require.Contains(t, out, "Hub ir-1 restored.")
	require.Contains(t, out, "deyroute hub announce-move 5.6.7.8:44433")
	require.Contains(t, out, "/etc/deyroute.pre-restore-x")

	// This server already is that hub: a different detected address is
	// not a move.
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "9.9.9.9", false, nil }
	e.ok("restore", path, "--yes")
	require.Empty(t, ro.PublicIP)

	// A new server: the hub moves to the detected public address; on a
	// TTY the owner is asked first.
	require.NoError(t, os.Remove(filepath.Join(e.root, "etc/deyroute/config.yaml")))
	e.tty("", "yes")
	out = e.ok("restore", path)
	require.Contains(t, out, "This server's public IP is 9.9.9.9, but the backup's hub address is 5.6.7.8")
	require.Contains(t, out, "changes from 5.6.7.8 to 9.9.9.9")
	require.Equal(t, "9.9.9.9", ro.PublicIP)
	e.tty("n", "yes")
	e.ok("restore", path)
	require.Empty(t, ro.PublicIP)
	e.tty("n", "no")
	require.Contains(t, e.fail(1, "restore", path), "Aborted.")
	e.g.IsTTY = false
	doc := e.json("restore", path, "--yes")
	require.Equal(t, "9.9.9.9", ro.PublicIP)
	require.Equal(t, true, doc["address_changed"])
	// A private detection never moves the hub.
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	e.ok("restore", path, "--yes")
	require.Empty(t, ro.PublicIP)

	// Encrypted backups: passphrase from the environment or the terminal.
	enc := writeFixtureBackup(t, t.TempDir(), hubConfig, "fixture-pass")
	e.vars[EnvBackupPassphrase] = "wrong-pass"
	require.Contains(t, e.fail(1, "restore", enc, "--yes"), "DEY-S004")
	e.vars[EnvBackupPassphrase] = "fixture-pass"
	e.ok("restore", enc, "--yes")
	require.Equal(t, "fixture-pass", ro.Passphrase)
	delete(e.vars, EnvBackupPassphrase)
	require.Contains(t, e.fail(1, "restore", enc, "--yes"), "DEY-S008")
	e.g.ReadPassword = func(string) (string, error) { return "fixture-pass", nil }
	e.tty("yes")
	e.ok("restore", enc)
	e.g.IsTTY = false
	errOut = e.fail(1, "restore", filepath.Join(e.root, "missing.age"), "--yes")
	require.Contains(t, errOut, "DEY-S005")
	require.Contains(t, errOut, "no such file or directory")

	// A node backup.
	e2 := newEnv(t)
	nodePath := writeFixtureBackup(t, t.TempDir(), nodeConfig, "")
	e2.g.Ops.Restore = func(_ context.Context, o setup.RestoreOptions) (*setup.RestoreResult, error) {
		require.Empty(t, o.Passphrase)
		require.Empty(t, o.PublicIP)
		return &setup.RestoreResult{Role: "node", NodeID: "de-1"}, nil
	}
	e2.g.DetectIP = func(context.Context) (string, bool, error) { return "9.9.9.9", false, nil }
	out = e2.ok("restore", nodePath, "--yes")
	require.Contains(t, out, "Node de-1 restored.")
	require.Contains(t, out, "reconnects to its hub")
	require.Contains(t, e2.fail(3, "restore", nodePath), "node de-1")
	e2.g.Ops.Restore = func(context.Context, setup.RestoreOptions) (*setup.RestoreResult, error) {
		return nil, deyerr.New(deyerr.C001, deyerr.Params{"key": "x", "line": 3})
	}
	require.Contains(t, e2.fail(1, "restore", nodePath, "--yes"), "DEY-C001")
}
