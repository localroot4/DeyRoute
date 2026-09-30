package setup

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	// Registers every tunnel backend, so config validation (the default
	// ladder, restored configs) knows all transports.
	_ "github.com/localroot4/deyroute/internal/backend/all"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	deylog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
)

// DefaultSocketTimeout is how long setup waits for the daemon's local API
// socket after starting the service.
const DefaultSocketTimeout = 20 * time.Second

// socketPollInterval is the pause between two socket dial attempts.
const socketPollInterval = 100 * time.Millisecond

// env is the normalized environment shared by every operation.
type env struct {
	root          string
	runner        exec.Runner
	now           func() time.Time
	lookupGroup   func(name string) (int, error)
	lookupUser    func(name string) (int, error)
	chown         func(path string, uid, gid int) error
	socketPath    string
	socketTimeout time.Duration
	rep           *reporter
	log           *slog.Logger
}

// envOptions are the injectable fields common to the option structs.
type envOptions struct {
	Root          string
	Runner        exec.Runner
	Now           func() time.Time
	LookupGroup   func(name string) (gid int, err error)
	LookupUser    func(name string) (uid int, err error)
	Chown         func(path string, uid, gid int) error
	SocketPath    string
	SocketTimeout time.Duration
	Progress      func(api.Step)
	Logger        *slog.Logger
}

func newEnv(o envOptions) *env {
	e := &env{
		root:          o.Root,
		runner:        o.Runner,
		now:           o.Now,
		lookupGroup:   o.LookupGroup,
		lookupUser:    o.LookupUser,
		chown:         o.Chown,
		socketPath:    o.SocketPath,
		socketTimeout: o.SocketTimeout,
		log:           o.Logger,
	}
	if e.root == "" {
		e.root = "/"
	}
	if e.runner == nil {
		e.runner = exec.NewRunner()
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.lookupGroup == nil {
		e.lookupGroup = LookupGroupID
	}
	if e.lookupUser == nil {
		e.lookupUser = LookupUserID
	}
	if e.chown == nil {
		e.chown = os.Lchown
	}
	if e.socketPath == "" {
		e.socketPath = e.path(config.SocketPath)
	}
	if e.socketTimeout <= 0 {
		e.socketTimeout = DefaultSocketTimeout
	}
	if e.log == nil {
		e.log = deylog.Discard()
	}
	e.rep = newReporter(o.Progress, e.log)
	return e
}

// path joins a system path with the root.
func (e *env) path(p string) string { return filepath.Join(e.root, p) }

// configPath is Root/etc/deyroute/config.yaml.
func (e *env) configPath() string { return e.path(config.DefaultPath) }

// secret is Root/etc/deyroute/secrets/<name>.
func (e *env) secret(name string) string { return filepath.Join(e.path(config.SecretsDir), name) }

// systemd returns a unit manager on the environment.
func (e *env) systemd() *systemd.Manager { return &systemd.Manager{Runner: e.runner, Root: e.root} }

// validateOptions are the config checks setup uses: every transport of the
// registry is known (internal/backend/all is imported by this package).
func validateOptions() config.ValidateOptions {
	return config.ValidateOptions{KnownTransport: backend.KnownTransport, ValidTransports: backend.ValidIDs}
}

// LookupGroupID returns the numeric id of a system group (the default
// group lookup of every option struct).
func LookupGroupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(g.Gid)
}

// LookupUserID returns the numeric id of a system user (the default user
// lookup of every option struct).
func LookupUserID(name string) (int, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

// refuseConfigured returns DEY-I013 when Root/etc/deyroute/config.yaml
// exists (setup never overwrites a configuration).
func (e *env) refuseConfigured() error {
	p := e.configPath()
	data, err := os.ReadFile(p) // #nosec G304 -- fixed deyroute path under Root
	switch {
	case err == nil:
	case stderrors.Is(err, fs.ErrNotExist):
		return nil
	default:
		if _, serr := os.Lstat(p); serr != nil && stderrors.Is(serr, fs.ErrNotExist) {
			return nil
		}
		return deyerr.New(deyerr.I013, deyerr.Params{"role": "a deyroute server"}).WithDetail(p)
	}
	var probe struct {
		Role string `yaml:"role"`
	}
	_ = yaml.Unmarshal(data, &probe)
	role := strings.TrimSpace(probe.Role)
	if role != config.RoleHub && role != config.RoleNode {
		role = "a deyroute server"
	}
	return deyerr.New(deyerr.I013, deyerr.Params{"role": role}).WithDetail(p)
}

// sysusersLine is the systemd-sysusers entry of the backend user, the same
// one installer/install.sh uses.
const sysusersLine = `u ` + config.SystemUser + ` - "DEYROUTE tunnel backends" /nonexistent /usr/sbin/nologin`

// ensureSystemUser makes sure the system user and group deyroute exist: the
// tunnel backends run as that user and reach /etc/deyroute/backends and
// /var/lib/deyroute/bin through the group (spec section 11, ARCHITECTURE.md
// §7.5). install.sh normally creates it; when the binary was installed
// another way it is created here with systemd-sysusers (allow-listed,
// QUESTIONS.md C.15; with --root when Root is not "/"). A failure is
// returned as a warning (DEY-X032): the caller continues and the new
// directories are root-only, which the Fix line explains how to repair.
func (e *env) ensureSystemUser(ctx context.Context) error {
	exists := func() error {
		if _, err := e.lookupUser(config.SystemUser); err != nil {
			return err
		}
		_, err := e.lookupGroup(config.SystemUser)
		return err
	}
	if exists() == nil {
		return nil
	}
	// Idempotent: an existing user or group of that name is kept.
	args := []string{"--inline", sysusersLine}
	if filepath.Clean(e.root) != "/" {
		args = append([]string{"--root=" + e.root}, args...)
	}
	_, _, err := e.runner.Run(ctx, "systemd-sysusers", args, nil)
	if err == nil {
		err = exists()
	}
	if err == nil {
		return nil
	}
	return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": e.path(config.EtcDir)}).
		WithWhy("the system user " + config.SystemUser + " does not exist and systemd-sysusers could not create it; " +
			"tunnel backends run as this user and cannot read their files without it").
		WithFix("run the installer again (it creates the " + config.SystemUser + " user and fixes the directory " +
			"permissions without touching the configuration), or: systemd-sysusers --inline '" + sysusersLine + "'")
}

// layoutDir is one directory of the section 2 layout.
type layoutDir struct {
	path string
	// mode is used when the deyroute group exists (owner root, group
	// deyroute when group is set); without that group the group bits are
	// cleared.
	mode  fs.FileMode
	group bool
	// enforce re-applies mode to an existing directory (secrets only: it
	// may never be wider than 0700).
	enforce bool
}

// layout mirrors installer/install.sh and ARCHITECTURE.md §7.5.
var layout = []layoutDir{
	{path: config.EtcDir, mode: 0o710, group: true},
	{path: config.SecretsDir, mode: 0o700, enforce: true},
	{path: config.BackendsConfDir, mode: 0o750, group: true},
	{path: config.LibDir, mode: 0o750, group: true},
	{path: config.BinDir, mode: 0o755},
	{path: config.BackupDir, mode: 0o700},
	{path: config.LogDir, mode: 0o750, group: true},
	{path: config.TunnelLogDir, mode: 0o770, group: true},
}

// ensureLayout creates the deyroute directories that do not exist yet with
// their modes (root:deyroute when the deyroute group exists). Existing
// directories keep their mode and owner (the installer set them; /etc/deyroute
// and /var/lib/deyroute must stay traversable for the deyroute user), except
// secrets/, which is always forced to 0700. Failures are DEY-X032.
func (e *env) ensureLayout() error {
	gid, gerr := e.lookupGroup(config.SystemUser)
	haveGroup := gerr == nil && gid >= 0
	for _, d := range layout {
		p := e.path(d.path)
		mode := d.mode
		if !haveGroup {
			mode &^= 0o070
		}
		info, err := os.Lstat(p)
		switch {
		case err == nil && !info.IsDir():
			return deyerr.New(deyerr.X032, deyerr.Params{"path": p}).WithWhy(p + " exists and is not a directory")
		case err == nil:
			if d.enforce && info.Mode().Perm() != mode {
				if err := os.Chmod(p, mode); err != nil {
					return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
				}
			}
			continue
		case !stderrors.Is(err, fs.ErrNotExist):
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
		err = os.MkdirAll(filepath.Dir(p), 0o755) // #nosec G301 -- system parents such as /etc and /var/lib
		if err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Dir(p)})
		}
		if err := os.Mkdir(p, mode); err != nil && !stderrors.Is(err, fs.ErrExist) {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
		// Mkdir applies the umask; set the exact mode.
		if err := os.Chmod(p, mode); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
		if d.group && haveGroup {
			if err := e.chown(p, -1, gid); err != nil {
				return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
			}
		}
	}
	return nil
}

// waitSocket waits until the daemon accepts connections on its local API
// socket (at most socketTimeout). A timeout is DEY-X003 for service; a
// cancelled caller context is DEY-X031.
func (e *env) waitSocket(parent context.Context, service string) error {
	ctx, cancel := context.WithTimeout(parent, e.socketTimeout)
	defer cancel()
	var d net.Dialer
	t := time.NewTicker(socketPollInterval)
	defer t.Stop()
	for {
		c, err := d.DialContext(ctx, "unix", e.socketPath)
		if err == nil {
			_ = c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			if perr := parent.Err(); perr != nil {
				return deyerr.Wrap(deyerr.X031, perr, deyerr.Params{"command": "wait for " + e.socketPath})
			}
			return deyerr.Wrap(deyerr.X003, err, deyerr.Params{"service": service}).
				WithDetail("the local API socket " + e.socketPath + " did not answer within " +
					e.socketTimeout.String() + "; see: journalctl -u " + service)
		case <-t.C:
		}
	}
}

// startService installs the unit templates, enables and (re)starts unit
// and waits for the local API socket. restart uses `systemctl restart`
// after `enable` (restore: the service may already run with the old
// configuration); otherwise `systemctl enable --now` is used.
func (e *env) startService(ctx context.Context, unit string, restart bool) error {
	m := e.systemd()
	if err := m.InstallTemplates(ctx); err != nil {
		return err
	}
	if restart {
		if err := m.Enable(ctx, unit); err != nil {
			return err
		}
		if err := m.Restart(ctx, unit); err != nil {
			return err
		}
	} else if _, _, err := e.runner.Run(ctx, "systemctl", []string{"enable", "--now", unit}, nil); err != nil {
		return err
	}
	return e.waitSocket(ctx, strings.TrimSuffix(unit, ".service"))
}

// osPrettyName returns PRETTY_NAME from Root/etc/os-release (or
// Root/usr/lib/os-release), "Linux" when neither exists.
func (e *env) osPrettyName() string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		data, err := os.ReadFile(e.path(p)) // #nosec G304 -- fixed system file under Root
		if err != nil {
			continue
		}
		if v := osReleaseValue(data, "PRETTY_NAME"); v != "" {
			return v
		}
	}
	return "Linux"
}

// osReleaseValue returns the unquoted value of key in an os-release file.
func osReleaseValue(data []byte, key string) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `"'`)
		}
		return strings.TrimSpace(v)
	}
	return ""
}

// notLoaded reports systemctl failures meaning "no such unit" (exit status
// 5 or the corresponding messages), which uninstall and restore ignore.
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
