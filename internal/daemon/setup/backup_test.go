package setup

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func testEvents() []state.Event {
	t0 := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	// Newest first, as the Local API returns them.
	return []state.Event{
		{Seq: 2, At: t0.Add(time.Minute), Level: state.LevelInfo, Type: state.EvTunnelUp, Tunnel: "main", Message: "up"},
		{Seq: 1, At: t0, Level: state.LevelInfo, Type: state.EvTunnelDown, Tunnel: "main", Message: "down"},
	}
}

func TestEventsReaderOldestFirst(t *testing.T) {
	r, err := EventsReader(testEvents())
	require.NoError(t, err)
	sc := bufio.NewScanner(r)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], `"seq":1`)
	require.Contains(t, lines[1], `"seq":2`)
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	hub, err := SetupHub(ctxT(t), hubOpts(src, hubFake(), nil, nil))
	require.NoError(t, err)
	events, err := EventsReader(testEvents())
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "hub.tar.gz.age")
	path, err := Backup(ctxT(t), BackupOptions{Root: src, Out: out, Passphrase: "correct horse", Events: events, Now: fixedNow})
	require.NoError(t, err)
	require.Equal(t, out, path)
	require.Equal(t, os.FileMode(0o600), mode(t, path))

	// Restore on a fresh server (install.sh --no-setup) with the service.
	dst := t.TempDir()
	fakeProc(t, dst)
	sock := shortSocket(t)
	serveSocket(t, sock)
	f := exec.NewFake()
	f.OnPrefix("systemctl ", exec.OK(""))
	f.On("systemctl stop deyroute-node.service", exec.Fail(5, "Unit deyroute-node.service not loaded."))
	f.On("systemctl disable deyroute-node.service", exec.Fail(1, "Unit file deyroute-node.service does not exist."))
	// The new server has no deyroute user yet: it is created before the
	// archive's owners are mapped.
	su := &sysUsers{}
	f.Handler = su.handle
	ch := &chownLog{}
	steps := &stepLog{}
	res, err := Restore(ctxT(t), RestoreOptions{
		Root: dst, Path: path, Passphrase: "correct horse", Runner: f, ApplySysctl: true, StartService: true,
		SocketPath: sock, Progress: steps.add, LookupGroup: su.lookup, LookupUser: su.lookupUser, Chown: ch.chown, Now: fixedNow,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"restore=ok", "events=ok", "sysctl=ok", "service=ok"}, steps.final())
	require.Len(t, su.ran(), 1)
	require.Equal(t, "systemd-sysusers", f.Calls()[0].Name, "the user exists before anything is restored")
	require.False(t, res.AddressChanged)
	require.Equal(t, hub.PublicIP, res.PublicIP)
	require.Equal(t, config.RoleHub, res.Role)
	require.Equal(t, "ir-1", res.HubName)
	require.True(t, res.ServiceStarted)
	require.False(t, res.Migrated)
	require.Empty(t, res.PreviousDir)
	require.Equal(t, config.SysctlBalanced, res.SysctlProfile)
	require.WithinDuration(t, fixedNow(), res.Created, time.Second)

	// Config and secrets are identical.
	for _, p := range []string{"etc/deyroute/config.yaml", "etc/deyroute/secrets/ca.key", "etc/deyroute/secrets/ca.crt", "etc/deyroute/secrets/hub.crt"} {
		a, err := os.ReadFile(filepath.Join(src, p))
		require.NoError(t, err)
		b, err := os.ReadFile(filepath.Join(dst, p))
		require.NoError(t, err)
		require.Equalf(t, a, b, "%s differs", p)
	}
	cfg, err := config.LoadWith(filepath.Join(dst, "etc/deyroute/config.yaml"), validateOptions())
	require.NoError(t, err)
	require.Equal(t, hub.PublicIP, cfg.Hub.PublicIP)

	// The service of the other role is stopped; the hub is enabled and
	// restarted (it may already run with the old configuration).
	require.True(t, f.Called("systemctl stop deyroute-node.service"))
	require.True(t, f.Called("systemctl enable deyroute-hub.service"))
	require.True(t, f.Called("systemctl restart deyroute-hub.service"))
	require.FileExists(t, filepath.Join(dst, "etc/systemd/system/deyroute-hub.service"))
	require.DirExists(t, filepath.Join(dst, "var/log/deyroute/tunnels"))

	// Events are left for the hub, which imports them once.
	require.Equal(t, filepath.Join(dst, RestoreEventsPath), res.EventsPath)
	require.Equal(t, os.FileMode(0o600), mode(t, res.EventsPath))
	require.Contains(t, string(res.Events), `"seq":2`)

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, st.Close()) }()
	n, err := ImportRestoredEvents(dst, st.ImportEvents)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	evs, err := st.Events(state.EventFilter{})
	require.NoError(t, err)
	require.Len(t, evs, 2)
	require.Equal(t, "up", evs[0].Message)
	require.NoFileExists(t, res.EventsPath)
	n, err = ImportRestoredEvents(dst, st.ImportEvents)
	require.NoError(t, err)
	require.Zero(t, n, "imported only once")

	// Restoring again over the restored server keeps the replaced copy
	// (an unencrypted backup: age's scrypt is slow under -race).
	plain, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "hub.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)
	res2, err := Restore(ctxT(t), RestoreOptions{Root: dst, Path: plain, Runner: f, LookupGroup: su.lookup, LookupUser: su.lookupUser, Chown: ch.chown, Now: fixedNow})
	require.NoError(t, err)
	require.DirExists(t, res2.PreviousDir)
	require.False(t, res2.ServiceStarted)
}

func TestRestoreNodeBackupWithoutEvents(t *testing.T) {
	src := t.TempDir()
	hub := startTestHub(t)
	_, err := Join(ctxT(t), joinOpts(src, hub.link(goodToken, hub.ca.Fingerprint()), nil))
	require.NoError(t, err)
	path, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "n.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)

	dst := t.TempDir()
	sock := shortSocket(t)
	serveSocket(t, sock)
	f := exec.NewFake()
	f.OnPrefix("systemctl ", exec.OK(""))
	steps := &stepLog{}
	res, err := Restore(ctxT(t), RestoreOptions{Root: dst, Path: path, Runner: f, StartService: true, SocketPath: sock, Progress: steps.add,
		LookupGroup: withGroup, LookupUser: withUser, Chown: (&chownLog{}).chown})
	require.NoError(t, err)
	require.Equal(t, config.RoleNode, res.Role)
	require.Equal(t, "germany-1", res.NodeID)
	require.Empty(t, res.EventsPath)
	require.Equal(t, []string{"restore=ok", "events=skipped", "sysctl=skipped", "service=ok"}, steps.final())
	require.True(t, f.Called("systemctl stop deyroute-hub.service"))
	require.True(t, f.Called("systemctl disable deyroute-hub.service"))
	require.True(t, f.Called("systemctl restart deyroute-node.service"))
}

func TestRestoreErrors(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	_, err := SetupHub(ctxT(t), hubOpts(src, hubFake(), nil, nil))
	require.NoError(t, err)
	path, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "b.tar.gz.age"), Passphrase: "pw"})
	require.NoError(t, err)
	plain, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "b.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)

	dst := t.TempDir()
	steps := &stepLog{}
	_, err = Restore(ctxT(t), RestoreOptions{Root: dst, Path: path, Passphrase: "wrong", Runner: exec.NewFake(), Progress: steps.add})
	requireTop(t, err, deyerr.S004)
	require.Equal(t, []string{"restore=failed"}, steps.final())
	require.NoDirExists(t, filepath.Join(dst, "etc/deyroute"))

	_, err = Restore(ctxT(t), RestoreOptions{Root: dst, Path: filepath.Join(dst, "missing"), Runner: exec.NewFake()})
	requireTop(t, err, deyerr.S005)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Restore(ctx, RestoreOptions{Root: dst, Path: plain, Runner: exec.NewFake()})
	requireTop(t, err, deyerr.X031)

	// A failing service start is DEY-I014 {service}.
	f := exec.NewFake()
	f.OnPrefix("systemctl ", exec.Fail(1, "bus error"))
	f.OnPrefix("systemctl stop ", exec.Fail(5, "not loaded"))
	f.OnPrefix("systemctl disable ", exec.Fail(1, "does not exist"))
	_, err = Restore(ctxT(t), RestoreOptions{Root: dst, Path: plain, Runner: f, StartService: true, LookupGroup: noGroup})
	e := requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "service")
}

func TestRestoreRejectsInvalidConfig(t *testing.T) {
	src := t.TempDir()
	_, err := SetupHub(ctxT(t), hubOpts(src, hubFake(), nil, nil))
	require.NoError(t, err)
	// An unknown transport in the ladder is refused before anything changes.
	p := filepath.Join(src, "etc/deyroute/config.yaml")
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	data = bytes.Replace(data, []byte("- direct/native"), []byte("- nope/nope"), 1)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	path, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "b.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)
	dst := t.TempDir()
	_, err = Restore(ctxT(t), RestoreOptions{Root: dst, Path: path, Runner: exec.NewFake()})
	requireCode(t, err, deyerr.C005)
	require.NoDirExists(t, filepath.Join(dst, "etc/deyroute"))
}

func TestBackupErrors(t *testing.T) {
	root := t.TempDir()
	_, err := Backup(ctxT(t), BackupOptions{Root: root, Passphrase: "x"})
	requireTop(t, err, deyerr.C014)
	_, err = SetupHub(ctxT(t), hubOpts(root, hubFake(), nil, nil))
	require.NoError(t, err)
	_, err = Backup(ctxT(t), BackupOptions{Root: root})
	requireTop(t, err, deyerr.S008)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Backup(ctx, BackupOptions{Root: root, Passphrase: "x"})
	requireTop(t, err, deyerr.X031)
	// The default location is the backups directory.
	path, err := Backup(ctxT(t), BackupOptions{Root: root, NoEncrypt: true})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "var/lib/deyroute/backups"), filepath.Dir(path))
}

func TestImportRestoredEventsKeepsFileOnFailure(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, RestoreEventsPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o600))
	_, err := ImportRestoredEvents(root, func(io.Reader) (int, error) { return 0, errors.New("db locked") })
	require.EqualError(t, err, "db locked")
	require.FileExists(t, p)
	n, err := ImportRestoredEvents(root, func(r io.Reader) (int, error) {
		b, err := io.ReadAll(r)
		return bytes.Count(b, []byte("\n")), err
	})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoFileExists(t, p)

	// Anything but a regular file is refused (DEY-X032) and kept.
	require.NoError(t, os.Mkdir(p, 0o700))
	_, err = ImportRestoredEvents(root, func(io.Reader) (int, error) { return 0, errors.New("never called") })
	requireTop(t, err, deyerr.X032)
	require.DirExists(t, p)
}

func TestKeepIPForward(t *testing.T) {
	root := t.TempDir()
	require.False(t, keepIPForward(root), "no deyroute sysctl file")
	conf := filepath.Join(root, config.SysctlConfPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(conf), 0o755))
	require.NoError(t, os.WriteFile(conf, []byte("# managed by deyroute (profile: balanced)\nnet.core.somaxconn = 65535\n"), 0o644))
	require.False(t, keepIPForward(root))
	require.NoError(t, os.WriteFile(conf, []byte("# managed by deyroute (profile: balanced)\nnet.ipv4.ip_forward = 1\n"), 0o644))
	require.True(t, keepIPForward(root))
	require.NoError(t, os.WriteFile(conf, []byte("net.ipv4.ip_forward = 0\n"), 0o644))
	require.False(t, keepIPForward(root))
}

// A restore over a server whose tuning forwards IPv4 (a WireGuard or
// AmneziaWG transport was used) keeps net.ipv4.ip_forward = 1.
func TestRestoreKeepsIPForward(t *testing.T) {
	src := t.TempDir()
	_, err := SetupHub(ctxT(t), hubOpts(src, hubFake(), nil, nil))
	require.NoError(t, err)
	plain, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "b.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)

	dst := t.TempDir()
	fakeProc(t, dst)
	conf := filepath.Join(dst, config.SysctlConfPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(conf), 0o755))
	require.NoError(t, os.WriteFile(conf, []byte("# managed by deyroute (profile: balanced)\nnet.ipv4.ip_forward = 1\n"), 0o644))
	_, err = Restore(ctxT(t), RestoreOptions{Root: dst, Path: plain, Runner: exec.NewFake(), ApplySysctl: true,
		LookupGroup: withGroup, LookupUser: withUser, Chown: (&chownLog{}).chown})
	require.NoError(t, err)
	require.Equal(t, "1", procValue(t, dst, "net.ipv4.ip_forward"))
	data, err := os.ReadFile(conf)
	require.NoError(t, err)
	require.Contains(t, string(data), "net.ipv4.ip_forward = 1")
}

func TestRestoreMovesHub(t *testing.T) {
	src := t.TempDir()
	hub, err := SetupHub(ctxT(t), hubOpts(src, hubFake(), nil, nil))
	require.NoError(t, err)
	require.Equal(t, "5.6.7.8", hub.PublicIP)
	plain, err := Backup(ctxT(t), BackupOptions{Root: src, Out: filepath.Join(t.TempDir(), "b.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)
	restore := func(root, ip4, ip6 string) (*RestoreResult, error) {
		return Restore(ctxT(t), RestoreOptions{Root: root, Path: plain, Runner: exec.NewFake(), PublicIP: ip4, PublicIP6: ip6,
			LookupGroup: withGroup, LookupUser: withUser, Chown: (&chownLog{}).chown, Now: fixedNow})
	}

	// Invalid addresses are refused before anything is replaced.
	for _, bad := range [][2]string{{"not-an-ip", ""}, {"9.9.9.9", "10.0.0.1"}, {"", "2a01:4f8::9"}, {"0.0.0.0", ""}} {
		dst := t.TempDir()
		_, err := restore(dst, bad[0], bad[1])
		requireCode(t, err, deyerr.C013)
		require.NoDirExists(t, filepath.Join(dst, "etc/deyroute"), "%v", bad)
	}

	dst := t.TempDir()
	res, err := restore(dst, " 9.9.9.9 ", "2a01:4f8::9")
	require.NoError(t, err)
	require.True(t, res.AddressChanged)
	require.Equal(t, "9.9.9.9", res.PublicIP)
	require.Equal(t, "2a01:4f8::9", res.PublicIP6)
	cfg, err := config.LoadWith(filepath.Join(dst, "etc/deyroute/config.yaml"), validateOptions())
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9", cfg.Hub.PublicIP)
	require.Equal(t, "2a01:4f8::9", cfg.Hub.PublicIP6)
	require.Equal(t, os.FileMode(0o600), mode(t, filepath.Join(dst, "etc/deyroute/config.yaml")))

	// The hub certificate names the new addresses and still chains to the
	// restored CA, which is unchanged (nodes keep trusting it).
	sec := filepath.Join(dst, "etc/deyroute/secrets")
	ca, err := tlsutil.LoadCA(filepath.Join(sec, "ca.crt"), filepath.Join(sec, "ca.key"))
	require.NoError(t, err)
	require.Equal(t, hub.CAFingerprint, ca.Fingerprint())
	certPEM, err := os.ReadFile(filepath.Join(sec, "hub.crt"))
	require.NoError(t, err)
	c, err := tlsutil.ParseCert(certPEM)
	require.NoError(t, err)
	var sans []string
	for _, ip := range c.IPAddresses {
		sans = append(sans, ip.String())
	}
	require.ElementsMatch(t, []string{"9.9.9.9", "2a01:4f8::9"}, sans)
	require.True(t, hubCertUsable(ca, filepath.Join(sec, "hub.crt"), filepath.Join(sec, "hub.key"), "ir-1",
		[]net.IP{net.ParseIP("9.9.9.9"), net.ParseIP("2a01:4f8::9")}, fixedNow()))
	require.Equal(t, os.FileMode(0o600), mode(t, filepath.Join(sec, "hub.key")))

	// Without PublicIP the address is kept (same-server restore).
	dst2 := t.TempDir()
	res2, err := restore(dst2, "", "")
	require.NoError(t, err)
	require.False(t, res2.AddressChanged)
	require.Equal(t, "5.6.7.8", res2.PublicIP)
	a, err := os.ReadFile(filepath.Join(src, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(dst2, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	require.Equal(t, a, b)

	// A node backup has no address to change.
	nsrc := t.TempDir()
	th := startTestHub(t)
	_, err = Join(ctxT(t), joinOpts(nsrc, th.link(goodToken, th.ca.Fingerprint()), nil))
	require.NoError(t, err)
	nb, err := Backup(ctxT(t), BackupOptions{Root: nsrc, Out: filepath.Join(t.TempDir(), "n.tar.gz"), NoEncrypt: true})
	require.NoError(t, err)
	ndst := t.TempDir()
	_, err = Restore(ctxT(t), RestoreOptions{Root: ndst, Path: nb, Runner: exec.NewFake(), PublicIP: "9.9.9.9",
		LookupGroup: withGroup, LookupUser: withUser, Chown: (&chownLog{}).chown})
	e := requireTop(t, err, deyerr.C013)
	require.Contains(t, e.Fix()+e.Why()+e.Message(), "node")
	require.NoDirExists(t, filepath.Join(ndst, "etc/deyroute"))
}

func TestImportRestoredEventsRefusesSymlinkAndFIFO(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, RestoreEventsPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	target := filepath.Join(root, "secret.txt")
	require.NoError(t, os.WriteFile(target, []byte("do not read\n"), 0o600))
	require.NoError(t, os.Symlink(target, p))
	called := false
	_, err := ImportRestoredEvents(root, func(io.Reader) (int, error) { called = true; return 0, nil })
	requireTop(t, err, deyerr.X032)
	require.False(t, called, "a symlink is never followed")
	require.FileExists(t, target)

	require.NoError(t, os.Remove(p))
	require.NoError(t, syscall.Mkfifo(p, 0o600))
	done := make(chan error, 1)
	go func() {
		_, err := ImportRestoredEvents(root, func(io.Reader) (int, error) { called = true; return 0, nil })
		done <- err
	}()
	select {
	case err := <-done:
		requireTop(t, err, deyerr.X032)
	case <-time.After(10 * time.Second):
		t.Fatal("a FIFO blocked the import")
	}
	require.False(t, called)
}

var _ = api.StepOK
