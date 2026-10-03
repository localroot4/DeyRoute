package systemd

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	deylog "github.com/localroot4/deyroute/internal/log"
)

// DefaultTimeout bounds one systemctl call when the caller's context has no
// deadline (stopping a unit may take up to systemd's 90 s stop timeout).
const DefaultTimeout = 100 * time.Second

// ShowProperties is the --property list Manager.Show asks for.
const ShowProperties = "ActiveState,SubState,MainPID,NRestarts,ActiveEnterTimestamp,Result"

// UnitState is the relevant part of `systemctl show` / `list-units`.
type UnitState struct {
	Unit     string
	Instance string // deyroute-tun@ instance, empty for other units
	// LoadState is only filled by ListInstances ("loaded", "not-found", …).
	LoadState   string
	ActiveState string // active, inactive, failed, activating, deactivating, reloading
	SubState    string // running, dead, exited, failed, auto-restart, …
	MainPID     int
	NRestarts   int
	// ActiveEnterTimestamp is when the unit last became active (UTC); zero
	// when it never was.
	ActiveEnterTimestamp time.Time
	Result               string // success, exit-code, signal, timeout, …
}

// Active reports ActiveState == "active".
func (s UnitState) Active() bool { return s.ActiveState == "active" }

// Failed reports ActiveState == "failed".
func (s UnitState) Failed() bool { return s.ActiveState == "failed" }

// Manager installs units and drives systemctl. Every command goes through
// Runner (exec.NewRunner() when nil); files live under Root ("/" when empty).
//
// Commands run (exact argv, useful for exec.Fake scripts):
//
//	systemctl daemon-reload
//	systemctl start|stop|restart|enable|disable|reset-failed <unit>
//	systemctl show <unit> --property=ActiveState,SubState,MainPID,NRestarts,ActiveEnterTimestamp,Result
//	systemctl list-units deyroute-tun@* --all --full --no-legend --plain
//	systemctl --version
type Manager struct {
	Runner exec.Runner
	Root   string
	// Timeout per systemctl call when ctx has no deadline; 0 = DefaultTimeout.
	Timeout time.Duration
}

func (m *Manager) root() string {
	if m.Root == "" {
		return "/"
	}
	return m.Root
}

func (m *Manager) runner() exec.Runner {
	if m.Runner == nil {
		return exec.NewRunner()
	}
	return m.Runner
}

// UnitPath returns the path of a unit file under Root.
func (m *Manager) UnitPath(name string) string {
	return filepath.Join(m.root(), UnitDir, name)
}

// DropInDir returns /etc/systemd/system/deyroute-tun@<instance>.service.d
// under Root.
func (m *Manager) DropInDir(instance string) string {
	return m.UnitPath(UnitName(instance) + ".d")
}

// DropInPath returns the drop-in file of an instance under Root.
func (m *Manager) DropInPath(instance string) string {
	return filepath.Join(m.DropInDir(instance), DropInName)
}

func (m *Manager) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		t := m.Timeout
		if t <= 0 {
			t = DefaultTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t)
		defer cancel()
	}
	out, _, err := m.runner().Run(ctx, "systemctl", args, nil)
	return out, err
}

// InstallTemplates writes the embedded unit files into /etc/systemd/system
// (0644) and runs daemon-reload only when a file changed. It also makes sure
// /var/log/deyroute and /var/log/deyroute/tunnels exist: every tunnel unit has
// ReadWritePaths=/var/log/deyroute and StandardOutput=append: into tunnels/,
// and systemd refuses to start it (226/NAMESPACE, 209/STDOUT) when they are
// missing.
func (m *Manager) InstallTemplates(ctx context.Context) error {
	for _, d := range []string{config.LogDir, TunnelLogDir} {
		p := filepath.Join(m.root(), d)
		if err := os.MkdirAll(p, deylog.DirMode); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
	}
	changed := false
	for _, name := range UnitNames {
		data, _ := Template(name)
		c, err := writeIfChanged(m.UnitPath(name), data, 0o644)
		if err != nil {
			return err
		}
		changed = changed || c
	}
	if changed {
		return m.DaemonReload(ctx)
	}
	return nil
}

// WriteDropIn writes the drop-in of instance (0644, directory 0755) when its
// content differs and reports whether it changed. The caller runs
// DaemonReload once after writing all drop-ins. Invalid instance names
// return DEY-X034; write failures DEY-X032.
func (m *Manager) WriteDropIn(instance string, content []byte) (changed bool, err error) {
	if err := checkInstance(instance); err != nil {
		return false, err
	}
	return writeIfChanged(m.DropInPath(instance), content, 0o644)
}

// RemoveInstance stops and disables an instance, deletes its drop-in
// directory, reloads systemd and clears a failed state. Missing units are
// not an error, so the call is idempotent.
func (m *Manager) RemoveInstance(ctx context.Context, instance string) error {
	if err := checkInstance(instance); err != nil {
		return err
	}
	unit := UnitName(instance)
	for _, verb := range []string{"stop", "disable"} {
		if _, err := m.systemctl(ctx, verb, unit); err != nil && !notLoaded(err) {
			return err
		}
	}
	dir := m.DropInDir(instance)
	if err := os.RemoveAll(dir); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	if err := m.DaemonReload(ctx); err != nil {
		return err
	}
	if _, err := m.systemctl(ctx, "reset-failed", unit); err != nil && !notLoaded(err) {
		return err
	}
	return nil
}

// notLoaded reports systemctl failures meaning "no such unit": exit status 5,
// or the corresponding stderr messages of other verbs and older versions.
// Only a systemctl that ran and exited qualifies; a missing systemctl
// (DEY-X002 "systemctl not found"), a timeout or a refused command is a real
// error, never "already gone".
func notLoaded(err error) bool {
	var xe *exec.ExitError
	if !stderrors.As(err, &xe) {
		return false
	}
	if xe.Code == 5 {
		return true
	}
	s := xe.Stderr
	return strings.Contains(s, "not loaded") || strings.Contains(s, "not found") ||
		strings.Contains(s, "does not exist") || strings.Contains(s, "No such file")
}

// DaemonReload runs systemctl daemon-reload.
func (m *Manager) DaemonReload(ctx context.Context) error {
	_, err := m.systemctl(ctx, "daemon-reload")
	return err
}

func (m *Manager) verb(ctx context.Context, verb, unit string) error {
	if err := checkUnit(unit); err != nil {
		return err
	}
	_, err := m.systemctl(ctx, verb, unit)
	return err
}

// Start runs systemctl start <unit>.
func (m *Manager) Start(ctx context.Context, unit string) error { return m.verb(ctx, "start", unit) }

// Stop runs systemctl stop <unit>.
func (m *Manager) Stop(ctx context.Context, unit string) error { return m.verb(ctx, "stop", unit) }

// Restart runs systemctl restart <unit>.
func (m *Manager) Restart(ctx context.Context, unit string) error {
	return m.verb(ctx, "restart", unit)
}

// Enable runs systemctl enable <unit>.
func (m *Manager) Enable(ctx context.Context, unit string) error { return m.verb(ctx, "enable", unit) }

// Disable removes the links of unit (RemoveWantsLinks), then runs
// systemctl disable <unit>.
func (m *Manager) Disable(ctx context.Context, unit string) error {
	if err := checkUnit(unit); err != nil {
		return err
	}
	if err := RemoveWantsLinks(m.root(), unit); err != nil {
		return err
	}
	return m.verb(ctx, "disable", unit)
}

// wantsDirSuffixes are the dependency directories `systemctl enable` links
// a unit into ([Install] WantedBy=, RequiredBy=, UpheldBy=).
var wantsDirSuffixes = []string{".wants", ".requires", ".upholds"}

// RemoveWantsLinks deletes the symlinks named unit in the .wants, .requires
// and .upholds directories of root/etc/systemd/system — what `systemctl
// enable` created for the unit — and keeps the directories themselves.
// `systemctl disable` removes those links too, but it also deletes a
// directory its last link leaves empty, and that directory belongs to the
// system, not to deyroute (a fresh multi-user.target.wants can be empty
// before deyroute is installed). Removing the links first leaves systemctl
// nothing to delete, so callers run this before `systemctl disable` and the
// system keeps the directories it had before the install. Missing links
// and directories are not an error.
func RemoveWantsLinks(root, unit string) error {
	if err := checkUnit(unit); err != nil {
		return err
	}
	if root == "" {
		root = "/"
	}
	dir := filepath.Join(root, UnitDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		if stderrors.Is(err, os.ErrNotExist) {
			return nil
		}
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	var errs []error
	for _, ent := range ents {
		if !ent.IsDir() || !hasWantsSuffix(ent.Name()) {
			continue
		}
		link := filepath.Join(dir, ent.Name(), unit)
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err := os.Remove(link); err != nil && !stderrors.Is(err, os.ErrNotExist) {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": link}))
		}
	}
	return stderrors.Join(errs...)
}

func hasWantsSuffix(name string) bool {
	for _, s := range wantsDirSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// ResetFailed runs systemctl reset-failed <unit>.
func (m *Manager) ResetFailed(ctx context.Context, unit string) error {
	return m.verb(ctx, "reset-failed", unit)
}

// Show reads the state of one unit with systemctl show.
func (m *Manager) Show(ctx context.Context, unit string) (UnitState, error) {
	if err := checkUnit(unit); err != nil {
		return UnitState{}, err
	}
	out, err := m.systemctl(ctx, "show", unit, "--property="+ShowProperties)
	if err != nil {
		return UnitState{}, err
	}
	st := ParseShow(out)
	st.Unit = unit
	st.Instance, _ = InstanceOf(unit)
	return st, nil
}

// ParseShow parses `systemctl show` key=value output. Unknown keys are
// ignored; unparsable numbers and timestamps become zero values.
func ParseShow(out []byte) UnitState {
	var st UnitState
	for _, l := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimRight(l, "\r"), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "ActiveState":
			st.ActiveState = v
		case "SubState":
			st.SubState = v
		case "MainPID":
			st.MainPID, _ = strconv.Atoi(v)
		case "NRestarts":
			st.NRestarts, _ = strconv.Atoi(v)
		case "ActiveEnterTimestamp":
			st.ActiveEnterTimestamp = ParseTimestamp(v)
		case "Result":
			st.Result = v
		case "LoadState":
			st.LoadState = v
		}
	}
	return st
}

var timestampLayouts = []string{
	"Mon 2006-01-02 15:04:05 MST",
	"Mon 2006-01-02 15:04:05.000000 MST",
	"Mon 2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05 MST",
}

// ParseTimestamp parses a systemd timestamp such as
// "Tue 2026-09-29 12:00:00 UTC" and returns it in UTC. Empty, "n/a" and
// unparsable values return the zero time. deyroute runs systemctl with TZ=UTC
// (see exec.ChildEnv) so the zone is normally UTC.
func ParseTimestamp(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" || v == "n/a" || v == "0" {
		return time.Time{}
	}
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ListInstances returns every deyroute-tun@ instance known to systemd plus
// every instance that has a drop-in on disk (stopped warm units are often not
// loaded, so systemd alone does not list them), sorted by unit name.
func (m *Manager) ListInstances(ctx context.Context) ([]UnitState, error) {
	out, err := m.systemctl(ctx, "list-units", tunPrefix+"*", "--all", "--full", "--no-legend", "--plain")
	if err != nil {
		return nil, err
	}
	byUnit := map[string]UnitState{}
	for _, st := range ParseListUnits(out) {
		byUnit[st.Unit] = st
	}
	// Drop-in directories on disk (ReadDir, not Glob: Root may contain glob
	// metacharacters). A missing or unreadable directory only means there is
	// nothing to add to systemd's list.
	entries, _ := os.ReadDir(filepath.Join(m.root(), UnitDir))
	for _, e := range entries {
		unit, isDropIn := strings.CutSuffix(e.Name(), ".d")
		if !isDropIn || !e.IsDir() {
			continue
		}
		inst, ok := InstanceOf(unit)
		if !ok || !ValidInstance(inst) {
			continue
		}
		if _, listed := byUnit[unit]; !listed {
			byUnit[unit] = UnitState{Unit: unit, Instance: inst, ActiveState: "inactive", SubState: "dead"}
		}
	}
	res := make([]UnitState, 0, len(byUnit))
	for _, st := range byUnit {
		res = append(res, st)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Unit < res[j].Unit })
	return res, nil
}

// ParseListUnits parses `systemctl list-units --no-legend --plain` output
// (UNIT LOAD ACTIVE SUB DESCRIPTION) and keeps deyroute-tun@ services.
func ParseListUnits(out []byte) []UnitState {
	var res []UnitState
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && (f[0] == "●" || f[0] == "*" || f[0] == "×") {
			f = f[1:]
		}
		if len(f) < 4 {
			continue
		}
		inst, ok := InstanceOf(f[0])
		if !ok {
			continue
		}
		res = append(res, UnitState{Unit: f[0], Instance: inst, LoadState: f[1], ActiveState: f[2], SubState: f[3]})
	}
	return res
}

// Available reports whether systemd is the running init system
// (/run/systemd/system exists under Root).
func (m *Manager) Available() bool {
	fi, err := os.Stat(filepath.Join(m.root(), "run", "systemd", "system"))
	return err == nil && fi.IsDir()
}

// Available reports whether the host runs systemd.
func Available() bool { return (&Manager{}).Available() }

// Version returns the systemd version from `systemctl --version`.
func (m *Manager) Version(ctx context.Context) (int, error) {
	out, err := m.systemctl(ctx, "--version")
	if err != nil {
		return 0, err
	}
	return ParseVersion(out)
}

// Version returns the host's systemd version.
func Version(ctx context.Context) (int, error) { return (&Manager{}).Version(ctx) }

// ParseVersion reads the number from the first line of
// `systemctl --version` ("systemd 255 (255.4-1ubuntu8)"). Unrecognised
// output returns DEY-I002.
func ParseVersion(out []byte) (int, error) {
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	f := strings.Fields(first)
	if len(f) >= 2 && f[0] == "systemd" {
		num := f[1]
		if i := strings.IndexFunc(num, func(r rune) bool { return r < '0' || r > '9' }); i > 0 {
			num = num[:i]
		}
		if v, err := strconv.Atoi(num); err == nil {
			return v, nil
		}
	}
	return 0, deyerr.New(deyerr.I002, nil).WithDetail(first)
}

// CheckSupported verifies the installer requirement "systemd 245+"
// (section 2): DEY-I002 when systemd is not running or systemctl is
// missing, DEY-I008 when it is too old.
func (m *Manager) CheckSupported(ctx context.Context) (int, error) {
	if !m.Available() {
		return 0, deyerr.New(deyerr.I002, nil)
	}
	v, err := m.Version(ctx)
	if err != nil {
		if deyerr.HasCode(err, deyerr.X002) {
			return 0, deyerr.Wrap(deyerr.I002, err, nil)
		}
		return 0, err
	}
	if v < MinVersion {
		return v, deyerr.New(deyerr.I008, deyerr.Params{"version": v})
	}
	return v, nil
}

// writeIfChanged atomically replaces path with data when the content
// differs (temp file + fsync + rename + directory fsync) and reports whether
// it wrote. When the content is already right but the file mode is not
// (edited by hand, older release), only the mode is fixed; that needs no
// daemon-reload, so it is not reported as a change.
func writeIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	old, err := os.ReadFile(path) //nolint:gosec // G304: path is built by Manager from Root and a validated name
	if err == nil && bytes.Equal(old, data) {
		if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode().IsRegular() && fi.Mode().Perm() != perm {
			if cerr := os.Chmod(path, perm); cerr != nil {
				return false, deyerr.Wrap(deyerr.X032, cerr, deyerr.Params{"path": path})
			}
		}
		return false, nil
	}
	dir := filepath.Dir(path)
	err = os.MkdirAll(dir, 0o755) //nolint:gosec // G301: systemd unit directories are world-readable by design
	if err != nil {
		return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := writeAtomic(path, data, perm); err != nil {
		return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	return true, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir makes a completed rename durable. It is best effort: some
// filesystems do not support fsync on directories, and the file itself is
// already in place.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // G304: directory of a file Manager just wrote
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
