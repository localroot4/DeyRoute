package systemd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

func newManager(t *testing.T) (*Manager, *exec.Fake) {
	t.Helper()
	f := exec.NewFake()
	return &Manager{Runner: f, Root: t.TempDir()}, f
}

func TestInstallTemplates(t *testing.T) {
	m, f := newManager(t)
	f.On("systemctl daemon-reload")
	ctx := context.Background()

	require.NoError(t, m.InstallTemplates(ctx))
	require.Equal(t, 1, f.Count("systemctl daemon-reload"))
	for name, data := range Templates() {
		p := filepath.Join(m.Root, "etc/systemd/system", name)
		got, err := os.ReadFile(p)
		require.NoError(t, err)
		require.Equal(t, data, got)
		fi, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	}

	// Unchanged: no reload.
	require.NoError(t, m.InstallTemplates(ctx))
	require.Equal(t, 1, f.Count("systemctl daemon-reload"))

	// One file edited by hand: rewritten and reloaded.
	require.NoError(t, os.WriteFile(m.UnitPath(HubUnit), []byte("edited"), 0o600))
	require.NoError(t, m.InstallTemplates(ctx))
	require.Equal(t, 2, f.Count("systemctl daemon-reload"))
	fi, err := os.Stat(m.UnitPath(HubUnit))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())

	// No temp files left behind.
	entries, err := os.ReadDir(filepath.Join(m.Root, "etc/systemd/system"))
	require.NoError(t, err)
	require.Len(t, entries, 3)

	// Same content but wrong mode: the mode is fixed without a reload.
	require.NoError(t, os.Chmod(m.UnitPath(NodeUnit), 0o600))
	require.NoError(t, m.InstallTemplates(ctx))
	require.Equal(t, 2, f.Count("systemctl daemon-reload"))
	fi, err = os.Stat(m.UnitPath(NodeUnit))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())

	// The log directories every tunnel unit needs exist.
	for _, d := range []string{"var/log/deyroute", "var/log/deyroute/tunnels"} {
		fi, err := os.Stat(filepath.Join(m.Root, d))
		require.NoError(t, err, d)
		require.True(t, fi.IsDir(), d)
	}
}

func TestInstallTemplatesLogDirError(t *testing.T) {
	m, f := newManager(t)
	require.NoError(t, os.MkdirAll(filepath.Join(m.Root, "var/log"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(m.Root, "var/log/deyroute"), nil, 0o600))
	err := m.InstallTemplates(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.X032), "%v", err)
	require.Empty(t, f.Calls())
}

func TestInstallTemplatesErrors(t *testing.T) {
	m, f := newManager(t)
	f.On("systemctl daemon-reload", exec.Fail(1, "Failed to reload daemon: Access denied"))
	err := m.InstallTemplates(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.X007), "%v", err)

	// Unit directory is a file → X032.
	m2, _ := newManager(t)
	require.NoError(t, os.MkdirAll(filepath.Join(m2.Root, "etc/systemd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(m2.Root, "etc/systemd/system"), nil, 0o600))
	err = m2.InstallTemplates(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.X032), "%v", err)
}

func TestWriteDropIn(t *testing.T) {
	m, f := newManager(t)
	inst := InstanceName("main", "de-1", "backhaul/wssmux")
	content := []byte("[Service]\nExecStart=/bin/true\n")

	changed, err := m.WriteDropIn(inst, content)
	require.NoError(t, err)
	require.True(t, changed)
	p := filepath.Join(m.Root, "etc/systemd/system/deyroute-tun@main.de-1.backhaul-wssmux.service.d/10-deyroute.conf")
	require.Equal(t, p, m.DropInPath(inst))
	got, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, content, got)
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())

	changed, err = m.WriteDropIn(inst, content)
	require.NoError(t, err)
	require.False(t, changed)

	changed, err = m.WriteDropIn(inst, append(content, '\n'))
	require.NoError(t, err)
	require.True(t, changed)

	for _, bad := range []string{"../../etc/passwd", "", "a/b", "-x"} {
		_, err = m.WriteDropIn(bad, content)
		require.True(t, deyerr.HasCode(err, deyerr.X034), bad)
	}
	require.Empty(t, f.Calls())
}

func TestRemoveInstance(t *testing.T) {
	m, f := newManager(t)
	inst := "main.de-1.backhaul-wssmux"
	unit := UnitName(inst)
	_, err := m.WriteDropIn(inst, []byte("x"))
	require.NoError(t, err)

	f.On("systemctl stop "+unit, exec.Fail(5, "Failed to stop "+unit+": Unit "+unit+" not loaded."))
	f.On("systemctl disable " + unit)
	f.On("systemctl daemon-reload")
	f.On("systemctl reset-failed "+unit, exec.Fail(1, "Failed to reset failed state of unit "+unit+": Unit "+unit+" not loaded."))
	require.NoError(t, m.RemoveInstance(context.Background(), inst))
	require.Equal(t, []string{
		"systemctl stop " + unit,
		"systemctl disable " + unit,
		"systemctl daemon-reload",
		"systemctl reset-failed " + unit,
	}, f.Lines())
	_, err = os.Stat(m.DropInDir(inst))
	require.True(t, os.IsNotExist(err))

	// Idempotent.
	require.NoError(t, m.RemoveInstance(context.Background(), inst))

	// A real stop failure is reported and nothing is removed.
	_, err = m.WriteDropIn(inst, []byte("x"))
	require.NoError(t, err)
	f.On("systemctl stop "+unit, exec.Fail(1, "Job for "+unit+" canceled."))
	err = m.RemoveInstance(context.Background(), inst)
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	_, err = os.Stat(m.DropInPath(inst))
	require.NoError(t, err)

	// daemon-reload and reset-failed errors are reported.
	f.On("systemctl stop " + unit)
	f.On("systemctl daemon-reload", exec.Fail(1, "boom"))
	require.Error(t, m.RemoveInstance(context.Background(), inst))
	f.On("systemctl daemon-reload")
	f.On("systemctl reset-failed "+unit, exec.Fail(1, "Access denied"))
	require.Error(t, m.RemoveInstance(context.Background(), inst))

	require.True(t, deyerr.HasCode(m.RemoveInstance(context.Background(), "../x"), deyerr.X034))
}

// A missing systemctl ("DEY-X002 systemctl not found") or a timeout is not
// "unit not loaded": RemoveInstance must fail before deleting the drop-in of
// a unit it could not stop.
func TestRemoveInstanceSystemctlMissing(t *testing.T) {
	inst := "main.de-1.backhaul-wssmux"
	unit := UnitName(inst)
	for _, r := range []exec.Response{
		{Err: deyerr.Wrap(deyerr.X002, exec.ErrNotFound, nil)},
		{Err: deyerr.Wrap(deyerr.X031, context.DeadlineExceeded, deyerr.Params{"command": "systemctl stop " + unit})},
		{Err: deyerr.New(deyerr.X007, deyerr.Params{"command": "Unit not found"})},
	} {
		m, f := newManager(t)
		_, err := m.WriteDropIn(inst, []byte("x"))
		require.NoError(t, err)
		f.On("systemctl stop "+unit, r)
		// Everything after stop would succeed, so only the stop error can
		// prevent the removal.
		f.On("systemctl disable " + unit).On("systemctl daemon-reload").On("systemctl reset-failed " + unit)
		err = m.RemoveInstance(context.Background(), inst)
		require.Error(t, err)
		_, statErr := os.Stat(m.DropInPath(inst))
		require.NoError(t, statErr, "drop-in removed although the unit was not stopped: %v", err)
		require.Equal(t, []string{"systemctl stop " + unit}, f.Lines())
	}
}

func TestNotLoaded(t *testing.T) {
	exit := func(code int, stderr string) error {
		return deyerr.Wrap(deyerr.X007, &exec.ExitError{Code: code, Stderr: stderr}, nil)
	}
	require.True(t, notLoaded(exit(5, "")))
	require.True(t, notLoaded(exit(1, "Failed to disable unit: Unit file x.service does not exist.")))
	require.True(t, notLoaded(exit(1, "Unit x.service not loaded.")))
	require.False(t, notLoaded(exit(1, "Access denied")))
	require.False(t, notLoaded(deyerr.Wrap(deyerr.X002, exec.ErrNotFound, nil)))
	require.False(t, notLoaded(deyerr.New(deyerr.X030, deyerr.Params{"command": "not found"})))
}

func TestVerbs(t *testing.T) {
	m, f := newManager(t)
	f.OnPrefix("systemctl ", exec.OK(""))
	ctx := context.Background()
	u := UnitName("main.canary")
	require.NoError(t, m.Start(ctx, u))
	require.NoError(t, m.Stop(ctx, u))
	require.NoError(t, m.Restart(ctx, u))
	require.NoError(t, m.Enable(ctx, u))
	require.NoError(t, m.Disable(ctx, u))
	require.NoError(t, m.ResetFailed(ctx, u))
	require.NoError(t, m.DaemonReload(ctx))
	require.Equal(t, []string{
		"systemctl start " + u, "systemctl stop " + u, "systemctl restart " + u,
		"systemctl enable " + u, "systemctl disable " + u, "systemctl reset-failed " + u,
		"systemctl daemon-reload",
	}, f.Lines())

	f.Reset()
	for _, bad := range []string{"--all", "", "x y.service"} {
		require.True(t, deyerr.HasCode(m.Start(ctx, bad), deyerr.X034))
		_, err := m.Show(ctx, bad)
		require.True(t, deyerr.HasCode(err, deyerr.X034))
	}
	require.Empty(t, f.Calls())
}

// deadlineRunner records whether calls carried a deadline.
type deadlineRunner struct{ deadlines []time.Duration }

func (d *deadlineRunner) Run(ctx context.Context, _ string, _ []string, _ []byte) ([]byte, []byte, error) {
	dl, ok := ctx.Deadline()
	if !ok {
		d.deadlines = append(d.deadlines, -1)
	} else {
		d.deadlines = append(d.deadlines, time.Until(dl))
	}
	return nil, nil, nil
}

func TestSystemctlTimeout(t *testing.T) {
	r := &deadlineRunner{}
	m := &Manager{Runner: r, Root: t.TempDir(), Timeout: 5 * time.Second}
	require.NoError(t, m.DaemonReload(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	require.NoError(t, m.DaemonReload(ctx))
	m.Timeout = 0
	require.NoError(t, m.DaemonReload(context.Background()))
	require.Len(t, r.deadlines, 3)
	require.InDelta(t, 5*time.Second, r.deadlines[0], float64(time.Second))
	require.Greater(t, r.deadlines[1], 50*time.Minute)
	require.InDelta(t, DefaultTimeout, r.deadlines[2], float64(time.Second))

	require.NotNil(t, (&Manager{}).runner())
}

func TestShow(t *testing.T) {
	m, f := newManager(t)
	u := UnitName("main.de-1.backhaul-wssmux")
	f.On("systemctl show "+u+" --property="+ShowProperties, exec.OK(
		"ActiveState=active\nSubState=running\nMainPID=4321\nNRestarts=2\n"+
			"ActiveEnterTimestamp=Tue 2026-09-29 12:00:00 UTC\nResult=success\nIgnored=x\nnoequals\n"))
	st, err := m.Show(context.Background(), u)
	require.NoError(t, err)
	require.Equal(t, UnitState{
		Unit: u, Instance: "main.de-1.backhaul-wssmux", ActiveState: "active", SubState: "running",
		MainPID: 4321, NRestarts: 2, ActiveEnterTimestamp: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), Result: "success",
	}, st)
	require.True(t, st.Active())
	require.False(t, st.Failed())

	f.On("systemctl show "+HubUnit+" --property="+ShowProperties, exec.OK(
		"ActiveState=failed\r\nSubState=failed\nMainPID=0\nNRestarts=\nActiveEnterTimestamp=n/a\nResult=exit-code\nLoadState=loaded\n"))
	st, err = m.Show(context.Background(), HubUnit)
	require.NoError(t, err)
	require.True(t, st.Failed())
	require.Equal(t, "", st.Instance)
	require.Equal(t, "loaded", st.LoadState)
	require.True(t, st.ActiveEnterTimestamp.IsZero())
	require.Equal(t, 0, st.MainPID)
	require.Equal(t, "exit-code", st.Result)

	f.On("systemctl show "+NodeUnit+" --property="+ShowProperties, exec.Fail(1, "Access denied"))
	_, err = m.Show(context.Background(), NodeUnit)
	require.True(t, deyerr.HasCode(err, deyerr.X007))
}

func TestParseTimestamp(t *testing.T) {
	want := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	require.Equal(t, want, ParseTimestamp("Tue 2026-09-29 12:00:00 UTC"))
	require.Equal(t, want, ParseTimestamp(" Tue 2026-09-29 12:00:00 UTC "))
	require.Equal(t, want.Add(123456*time.Microsecond), ParseTimestamp("Tue 2026-09-29 12:00:00.123456 UTC"))
	require.Equal(t, want, ParseTimestamp("Tue 2026-09-29 15:30:00 +0330"))
	require.Equal(t, want, ParseTimestamp("2026-09-29 12:00:00 UTC"))
	for _, v := range []string{"", "n/a", "0", "garbage"} {
		require.True(t, ParseTimestamp(v).IsZero(), v)
	}
}

func TestListInstances(t *testing.T) {
	m, f := newManager(t)
	f.On("systemctl list-units deyroute-tun@* --all --full --no-legend --plain", exec.OK(
		"deyroute-tun@main.de-1.backhaul-wssmux.service loaded active running DEYROUTE tunnel transport main.de-1.backhaul-wssmux\n"+
			"● deyroute-tun@main.de-1.rathole-noise.service loaded failed failed DEYROUTE tunnel transport main.de-1.rathole-noise\n"+
			"deyroute-hub.service loaded active running DEYROUTE hub\n"+
			"short line\n\n"))
	// A warm (stopped, unloaded) instance only known from its drop-in.
	_, err := m.WriteDropIn("main.de-1.frp-wss", []byte("x"))
	require.NoError(t, err)
	_, err = m.WriteDropIn("main.de-1.backhaul-wssmux", []byte("x"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(m.Root, UnitDir, "deyroute-tun@.service.d"), 0o755))

	got, err := m.ListInstances(context.Background())
	require.NoError(t, err)
	require.Equal(t, []UnitState{
		{Unit: "deyroute-tun@main.de-1.backhaul-wssmux.service", Instance: "main.de-1.backhaul-wssmux", LoadState: "loaded", ActiveState: "active", SubState: "running"},
		{Unit: "deyroute-tun@main.de-1.frp-wss.service", Instance: "main.de-1.frp-wss", ActiveState: "inactive", SubState: "dead"},
		{Unit: "deyroute-tun@main.de-1.rathole-noise.service", Instance: "main.de-1.rathole-noise", LoadState: "loaded", ActiveState: "failed", SubState: "failed"},
	}, got)

	f.On("systemctl list-units deyroute-tun@* --all --full --no-legend --plain", exec.Fail(1, "x"))
	_, err = m.ListInstances(context.Background())
	require.Error(t, err)
}

// Root may contain glob metacharacters; drop-ins are still found. Files and
// unsafe names that look like drop-in directories are ignored.
func TestListInstancesRootWithGlobChars(t *testing.T) {
	f := exec.NewFake().On("systemctl list-units deyroute-tun@* --all --full --no-legend --plain", exec.OK(""))
	m := &Manager{Runner: f, Root: filepath.Join(t.TempDir(), "r[1]*?")}
	_, err := m.WriteDropIn("main.de-1.frp-wss", []byte("x"))
	require.NoError(t, err)
	unitDir := filepath.Join(m.Root, UnitDir)
	require.NoError(t, os.WriteFile(filepath.Join(unitDir, "deyroute-tun@file.canary.service.d"), nil, 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(unitDir, "deyroute-tun@Bad Name.service.d"), 0o755))
	got, err := m.ListInstances(context.Background())
	require.NoError(t, err)
	require.Equal(t, []UnitState{{Unit: "deyroute-tun@main.de-1.frp-wss.service", Instance: "main.de-1.frp-wss", ActiveState: "inactive", SubState: "dead"}}, got)

	// No unit directory at all: only systemd's list.
	m2 := &Manager{Runner: f, Root: t.TempDir()}
	got, err = m2.ListInstances(context.Background())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestAvailableAndVersion(t *testing.T) {
	m, f := newManager(t)
	require.False(t, m.Available())
	_, err := m.CheckSupported(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.I002))

	require.NoError(t, os.MkdirAll(filepath.Join(m.Root, "run/systemd/system"), 0o755))
	require.True(t, m.Available())

	f.On("systemctl --version", exec.OK("systemd 255 (255.4-1ubuntu8.4)\n+PAM +AUDIT -SELINUX\n"))
	v, err := m.Version(context.Background())
	require.NoError(t, err)
	require.Equal(t, 255, v)
	v, err = m.CheckSupported(context.Background())
	require.NoError(t, err)
	require.Equal(t, 255, v)

	f.On("systemctl --version", exec.OK("systemd 244 (244.1)\n"))
	v, err = m.CheckSupported(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.I008))
	require.Equal(t, 244, v)
	require.Contains(t, deyerr.As(err).Message(), "244")

	f.On("systemctl --version", exec.Response{Err: deyerr.New(deyerr.X002, nil)})
	_, err = m.CheckSupported(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.I002))

	f.On("systemctl --version", exec.Fail(1, "x"))
	_, err = m.CheckSupported(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.X007))

	f.On("systemctl --version", exec.OK("something else\n"))
	_, err = m.Version(context.Background())
	require.True(t, deyerr.HasCode(err, deyerr.I002))

	// Package-level helpers must not panic on any host.
	_ = Available()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = Version(ctx)
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]int{
		"systemd 245 (245.4-4ubuntu3.22)":  245,
		"systemd 252 (252.22-1~deb12u1)\n": 252,
		"systemd 256.5\n":                  256,
		"  systemd 239 (239-78.el8)":       239,
	} {
		v, err := ParseVersion([]byte(in))
		require.NoError(t, err, in)
		require.Equal(t, want, v, in)
	}
	for _, in := range []string{"", "systemd", "systemd abc", "busybox 1.36"} {
		_, err := ParseVersion([]byte(in))
		require.Error(t, err, in)
	}
}

func TestParseListUnitsMarkers(t *testing.T) {
	got := ParseListUnits([]byte("× deyroute-tun@a.canary.service not-found inactive dead deyroute-tun@a.canary.service\n* deyroute-tun@b.canary.service loaded active running x\n"))
	require.Len(t, got, 2)
	require.Equal(t, "not-found", got[0].LoadState)
	require.Equal(t, "b.canary", got[1].Instance)
}
