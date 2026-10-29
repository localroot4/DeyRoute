package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tui"
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
	// A private detection on a new server is DEY-I025 without a terminal
	// (DR-08; TestRestoreNonTTYPrivateFails has the details).
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	ro = setup.RestoreOptions{}
	require.Contains(t, e.fail(1, "restore", path, "--yes"), "DEY-I025")
	require.Empty(t, ro.Path)
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "5.6.7.8", false, nil }

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

// hubConfig6 is hubConfig with an IPv6 hub address.
var hubConfig6 = strings.Replace(hubConfig, "  public_ip: 5.6.7.8\n", "  public_ip: 5.6.7.8\n  public_ip6: 2001:db8::8\n", 1)

// restoreFixture is a new server (no config.yaml) with a hub backup; the
// stub restore records its options and reports the address it would set.
func restoreFixture(t *testing.T, cfg string) (*env, string, *setup.RestoreOptions, *int) {
	t.Helper()
	e := newEnv(t)
	path := writeFixtureBackup(t, t.TempDir(), cfg, "")
	ro := &setup.RestoreOptions{}
	calls := new(int)
	e.g.Ops.Restore = func(_ context.Context, o setup.RestoreOptions) (*setup.RestoreResult, error) {
		*ro = o
		*calls++
		res := &setup.RestoreResult{Role: "hub", HubName: "ir-1", PublicIP: "5.6.7.8"}
		if strings.Contains(cfg, "public_ip6") {
			res.PublicIP6 = "2001:db8::8"
		}
		if o.PublicIP != "" {
			res.PublicIP, res.PublicIP6, res.AddressChanged = o.PublicIP, o.PublicIP6, true
		}
		return res, nil
	}
	return e, path, ro, calls
}

func TestRestorePublicIPFlag(t *testing.T) {
	e, path, ro, calls := restoreFixture(t, hubConfig6)
	// Detection is not asked: the flag decides, even when it is private.
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	doc := e.json("restore", path, "--yes", "--public-ip", "203.0.113.7")
	require.Equal(t, "203.0.113.7", ro.PublicIP)
	require.Empty(t, ro.PublicIP6)
	require.Equal(t, "203.0.113.7", doc["public_ip"])
	require.Equal(t, "", doc["public_ip6"])
	require.Equal(t, "flag", doc["address_source"])
	require.Equal(t, true, doc["address_changed"])
	// The backup's IPv6 address is cleared, and the warnings say so.
	require.Equal(t, []any{"The backup's hub.public_ip6 2001:db8::8 is cleared: pass --public-ip6 to give the restored hub an IPv6 address."}, doc["warnings"])

	// Human output: the next step names the address in effect.
	out := e.ok("restore", path, "--yes", "--public-ip", "203.0.113.7")
	require.Contains(t, out, "deyroute hub announce-move 203.0.113.7:44433")
	require.Contains(t, out, "hub.public_ip6 2001:db8::8 is cleared")
	require.NotContains(t, out, "5.6.7.8:44433")

	// A private address is taken as given, with a warning.
	doc = e.json("restore", path, "--yes", "--public-ip", "10.1.2.3", "--public-ip6", "2001:db8::9")
	require.Equal(t, "10.1.2.3", ro.PublicIP)
	require.Equal(t, []any{"10.1.2.3 is not a public IP address (private, CGNAT or loopback): nodes abroad reach the restored hub only if the provider forwards it to this server."}, doc["warnings"])

	// Invalid values are DEY-C013 before anything is restored.
	n := *calls
	for _, args := range [][]string{
		{"--public-ip", "5.6.7"},
		{"--public-ip", "2001:db8::1"},
		{"--public-ip", "::ffff:1.2.3.4"},
		{"--public-ip6", "2001:db8::1"},
		{"--public-ip", "203.0.113.7", "--public-ip6", "1.2.3.4"},
	} {
		errOut := e.fail(1, append([]string{"restore", path, "--yes"}, args...)...)
		require.Contains(t, errOut, "DEY-C013", "%v", args)
	}
	require.Equal(t, n, *calls)

	// A node backup has no address setting.
	e2 := newEnv(t)
	nodePath := writeFixtureBackup(t, t.TempDir(), nodeConfig, "")
	e2.g.Ops.Restore = func(context.Context, setup.RestoreOptions) (*setup.RestoreResult, error) {
		t.Fatal("a node backup with --public-ip must not be restored")
		return nil, nil
	}
	errOut := e2.fail(1, "restore", nodePath, "--yes", "--public-ip", "203.0.113.7")
	require.Contains(t, errOut, "DEY-C013")
	require.Contains(t, errOut, "--public-ip")
}

func TestRestorePublicIP6Flag(t *testing.T) {
	e, path, ro, _ := restoreFixture(t, hubConfig)
	doc := e.json("restore", path, "--yes", "--public-ip", "203.0.113.7", "--public-ip6", "2001:db8::7")
	require.Equal(t, "203.0.113.7", ro.PublicIP)
	require.Equal(t, "2001:db8::7", ro.PublicIP6)
	require.Equal(t, "2001:db8::7", doc["public_ip6"])
	require.Equal(t, "flag", doc["address_source"])
	require.Equal(t, []any{}, doc["warnings"])
	// --public-ip6 alone is refused: the IPv4 address is required with it.
	errOut := e.fail(1, "restore", path, "--yes", "--public-ip6", "2001:db8::7")
	require.Contains(t, errOut, "DEY-C013")
	require.Contains(t, errOut, "--public-ip6")
}

func TestRestoreNonTTYPrivateFails(t *testing.T) {
	e, path, _, calls := restoreFixture(t, hubConfig)
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	for _, args := range [][]string{
		{"restore", path, "--yes"},
		{"restore", path}, // no terminal: no question, no guess
		{"restore", path, "--yes", "--json"},
	} {
		errOut := e.fail(1, args...)
		require.Contains(t, errOut, "DEY-I025", "%v", args)
		require.Contains(t, errOut, "10.0.0.1")
		require.Contains(t, errOut, "--public-ip")
		require.Contains(t, errOut, "--keep-address if this server really owns 5.6.7.8")
	}
	// --yes on a terminal does not ask either.
	e.tty()
	require.Contains(t, e.fail(1, "restore", path, "--yes"), "DEY-I025")
	require.Zero(t, *calls)
	_, err := os.Stat(filepath.Join(e.root, "etc/deyroute"))
	require.ErrorIs(t, err, os.ErrNotExist, "nothing is written before DEY-I025")
}

func TestRestoreDetectFailFails(t *testing.T) {
	e, path, _, calls := restoreFixture(t, hubConfig)
	e.g.DetectIP = func(context.Context) (string, bool, error) {
		return "", false, deyerr.New(deyerr.I020, deyerr.Params{"reason": "no default route"})
	}
	errOut := e.fail(1, "restore", path, "--yes")
	require.Contains(t, errOut, "DEY-I025")
	require.Contains(t, errOut, "no default route")
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "", false, nil }
	require.Contains(t, e.fail(1, "restore", path, "--yes"), "DEY-I025")
	require.Zero(t, *calls)
}

func TestRestoreKeepAddress(t *testing.T) {
	e, path, ro, calls := restoreFixture(t, hubConfig6)
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	doc := e.json("restore", path, "--yes", "--keep-address")
	require.Equal(t, 1, *calls)
	require.Empty(t, ro.PublicIP)
	require.Empty(t, ro.PublicIP6)
	require.Equal(t, "kept", doc["address_source"])
	require.Equal(t, "5.6.7.8", doc["public_ip"])
	require.Equal(t, "2001:db8::8", doc["public_ip6"])
	require.Equal(t, false, doc["address_changed"])
	require.Equal(t, []any{}, doc["warnings"])
	// A public detection elsewhere does not move it either.
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "9.9.9.9", false, nil }
	e.ok("restore", path, "--yes", "--keep-address")
	require.Empty(t, ro.PublicIP)
}

func TestRestoreKeepAddressWithPublicIPRejected(t *testing.T) {
	e, path, _, calls := restoreFixture(t, hubConfig)
	for _, args := range [][]string{
		{"--keep-address", "--public-ip", "203.0.113.7"},
		{"--keep-address", "--public-ip6", "2001:db8::7"},
		{"--keep-address", "--public-ip", "203.0.113.7", "--public-ip6", "2001:db8::7"},
	} {
		errOut := e.fail(1, append([]string{"restore", path, "--yes"}, args...)...)
		require.Contains(t, errOut, "DEY-C013", "%v", args)
		require.Contains(t, errOut, "--keep-address")
	}
	require.Zero(t, *calls)
}

func TestRestoreSameServerUnchanged(t *testing.T) {
	e, path, ro, _ := restoreFixture(t, hubConfig)
	e.writeConfig(hubConfig)
	// This server is that hub: neither a private nor a failed detection
	// stops the restore, and the address stays.
	for _, det := range []func(context.Context) (string, bool, error){
		func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil },
		func(context.Context) (string, bool, error) { return "", false, deyerr.New(deyerr.I020, nil) },
		func(context.Context) (string, bool, error) { return "9.9.9.9", false, nil },
	} {
		e.g.DetectIP = det
		doc := e.json("restore", path, "--yes")
		require.Empty(t, ro.PublicIP)
		require.Equal(t, "same_server", doc["address_source"])
		require.Equal(t, []any{}, doc["warnings"])
	}
	// A public detection on a new server is still taken as before.
	require.NoError(t, os.Remove(filepath.Join(e.root, "etc/deyroute/config.yaml")))
	doc := e.json("restore", path, "--yes")
	require.Equal(t, "9.9.9.9", ro.PublicIP)
	require.Equal(t, "detected", doc["address_source"])
	// Detection equal to the backup's address: nothing changes.
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "5.6.7.8", false, nil }
	doc = e.json("restore", path, "--yes")
	require.Empty(t, ro.PublicIP)
	require.Equal(t, "detected", doc["address_source"])
}

func TestRestoreTTYAsksOnPrivate(t *testing.T) {
	e, path, ro, _ := restoreFixture(t, hubConfig)
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	// A typed address moves the hub ("asked"); an invalid answer is
	// asked again.
	e.tty("5.6.7", "203.0.113.9", "yes")
	out := e.ok("restore", path)
	require.Contains(t, out, "Public IP of this server for the restored hub")
	require.Contains(t, out, "10.0.0.1 is not a public IP address")
	require.Contains(t, out, "Enter alone keeps the backup's address 5.6.7.8")
	require.Contains(t, out, "DEY-C013")
	require.Contains(t, out, "changes from 5.6.7.8 to 203.0.113.9")
	require.Contains(t, out, "deyroute hub announce-move 203.0.113.9:44433")
	require.Equal(t, "203.0.113.9", ro.PublicIP)
	// Enter alone keeps the backup's address.
	e.tty("", "yes")
	out = e.ok("restore", path)
	require.Empty(t, ro.PublicIP)
	require.NotContains(t, out, "changes from")
	// --json: the question goes to stderr, the source is in the document.
	e.tty("203.0.113.9", "yes")
	require.Equal(t, 0, e.run("restore", path, "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &doc))
	require.Equal(t, "asked", doc["address_source"])
	require.Contains(t, e.errOut.String(), "Public IP of this server")
	// A failed detection asks too.
	e.g.DetectIP = func(context.Context) (string, bool, error) {
		return "", false, deyerr.New(deyerr.I020, deyerr.Params{"reason": "no default route"})
	}
	e.tty("", "yes")
	out = e.ok("restore", path)
	require.Contains(t, out, "DEY-I020")
	require.Empty(t, ro.PublicIP)
}

// The menu's restore check never applies the CLI policy: a private or
// failed detection asks for the address, it never fails with DEY-I025.
func TestRestoreCheckPrivateForTUI(t *testing.T) {
	e, path, _, _ := restoreFixture(t, hubConfig)
	var o tui.Options
	e.g.RunTUI = func(opts tui.Options) error { o = opts; return nil }
	e.tty()
	require.NoError(t, e.g.runTUI(context.Background()))
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	p, err := o.RestoreCheck(context.Background(), path, "")
	require.NoError(t, err)
	require.True(t, p.AskIP)
	require.Empty(t, p.MovedIP)
	require.Equal(t, "5.6.7.8", p.OldIP)
	require.Contains(t, p.AskWhy, "10.0.0.1")
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "", false, deyerr.New(deyerr.I020, nil) }
	p, err = o.RestoreCheck(context.Background(), path, "")
	require.NoError(t, err)
	require.True(t, p.AskIP)
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "9.9.9.9", false, nil }
	p, err = o.RestoreCheck(context.Background(), path, "")
	require.NoError(t, err)
	require.False(t, p.AskIP)
	require.Equal(t, "9.9.9.9", p.MovedIP)
	// Same server: no question at all.
	e.writeConfig(hubConfig)
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.1", true, nil }
	p, err = o.RestoreCheck(context.Background(), path, "")
	require.NoError(t, err)
	require.False(t, p.AskIP)
	require.Empty(t, p.MovedIP)
}
