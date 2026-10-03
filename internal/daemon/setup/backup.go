package setup

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// RestoreEventsPath is where Restore leaves the backup's events
// (events.ndjson) for the hub, which imports them once at start with
// ImportRestoredEvents and then deletes the file.
const RestoreEventsPath = config.LibDir + "/restore-events.ndjson"

// BackupOptions configure Backup (`deyroute backup [--out FILE]
// [--no-encrypt]`).
type BackupOptions struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Out is the backup file; empty = Root/var/lib/deyroute/backups/
	// deyroute-backup-<UTC>.tar.gz(.age).
	Out string
	// Passphrase encrypts the backup with age (required unless NoEncrypt:
	// DEY-S008).
	Passphrase string
	NoEncrypt  bool
	// Events is the NDJSON event export stored in the backup (see
	// EventsReader); nil = none.
	Events io.Reader
	// Version defaults to the running version; HubName to hub.name.
	Version string
	HubName string
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// Backup archives /etc/deyroute plus the events into one file (0600) and
// returns its path: install.Backup (DEY-C014 when nothing is set up,
// DEY-S008 without passphrase, DEY-X032 on write errors).
func Backup(ctx context.Context, o BackupOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", deyerr.Wrap(deyerr.X031, err, deyerr.Params{"command": "backup"})
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	ver := o.Version
	if ver == "" {
		ver = version.Version
	}
	return install.Backup(install.BackupOptions{
		Root: o.Root, OutPath: o.Out, Passphrase: o.Passphrase, NoEncrypt: o.NoEncrypt,
		Events: o.Events, Now: now(), Version: ver, HubName: o.HubName,
	})
}

// EventsReader encodes events (as returned newest first by the Local API)
// as the NDJSON export of state.ExportEvents: one event per line, oldest
// first.
func EventsReader(events []state.Event) (io.Reader, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := len(events) - 1; i >= 0; i-- {
		if err := enc.Encode(events[i]); err != nil {
			return nil, deyerr.Wrap(deyerr.X000, err, nil)
		}
	}
	return &buf, nil
}

// RestoreOptions configure Restore (`deyroute restore FILE [--yes]`; the
// CLI confirms before calling it).
type RestoreOptions struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Path is the backup file; Passphrase decrypts .age backups.
	Path       string
	Passphrase string
	// Runner runs systemctl (exec.NewRunner() when nil).
	Runner exec.Runner
	// PublicIP, when set, replaces hub.public_ip of a restored hub: moving
	// the hub to a new server (spec section 5) changes its address, and
	// the nodes' backend clients dial hub.public_ip. PublicIP6 then
	// replaces hub.public_ip6 ("" = none). Both are validated before
	// anything is replaced (DEY-C013, also for a node backup), and the hub
	// certificate is re-issued for them with the restored CA. The CLI
	// compares the backup's hub.public_ip with DetectPublicIP and asks the
	// owner (a same-server restore keeps the address).
	PublicIP  string
	PublicIP6 string
	// ApplySysctl applies tuning.sysctl_profile of the restored config
	// (the owner confirmed it on the original server).
	ApplySysctl bool
	// StartService stops the other role's service, installs the unit
	// templates, enables and restarts the restored role's service and
	// waits for its local API socket; the daemon re-renders and applies
	// everything at start.
	StartService bool

	// Now is the clock (names the pre-restore copy).
	Now func() time.Time
	// Progress receives every step update (may be nil).
	Progress func(api.Step)
	// Logger receives one line per step (discarded when nil).
	Logger *slog.Logger
	// LookupGroup returns the gid of a group (LookupGroupID when nil).
	LookupGroup func(name string) (gid int, err error)
	// LookupUser returns the uid of a user (LookupUserID when nil); with
	// LookupGroup it tells whether the backend user deyroute exists.
	LookupUser func(name string) (uid int, err error)
	// Chown changes ownership (os.Lchown when nil).
	Chown func(path string, uid, gid int) error
	// SocketPath is the local API socket (Root/run/deyroute/daemon.sock).
	SocketPath string
	// SocketTimeout bounds the wait for the socket (20 s when <= 0).
	SocketTimeout time.Duration
}

// RestoreResult reports a finished restore.
type RestoreResult struct {
	Role    string
	HubName string
	NodeID  string
	// PublicIP and PublicIP6 are hub.public_ip and hub.public_ip6 in effect
	// after the restore (hub only); AddressChanged is true when
	// RestoreOptions.PublicIP replaced them.
	PublicIP       string
	PublicIP6      string
	AddressChanged bool
	// BackupVersion is the deyroute version that wrote the backup; Created
	// its creation time.
	BackupVersion string
	Created       time.Time
	// PreviousDir is where the replaced /etc/deyroute was moved ("" when
	// there was none).
	PreviousDir string
	// Migrated is true when config.yaml was converted to the current
	// schema and saved.
	Migrated bool
	// Events is the backup's events.ndjson (nil when absent); EventsPath
	// is the file the hub imports at start ("" when there are no events).
	Events     []byte
	EventsPath string
	// SysctlProfile is the applied profile ("off" when none).
	SysctlProfile  string
	SysctlWarnings []string
	ServiceStarted bool
}

// Restore replaces /etc/deyroute with a backup (spec section 5): the file is
// decrypted (DEY-S004), checked (DEY-S005) and its config validated with
// every registered transport (DEY-C0xx) before anything is replaced; a
// config of an older schema is migrated and saved. A hub moved to a new
// server gets its new address from PublicIP/PublicIP6 (saved, hub
// certificate re-issued with the restored CA). Steps: restore, events (the
// events are written to RestoreEventsPath for the hub), sysctl and
// service. Backup/validation codes are returned unchanged; later steps
// fail with DEY-I014 {step}. The CA is part of the backup, so nodes trust
// the restored hub.
func Restore(ctx context.Context, o RestoreOptions) (*RestoreResult, error) {
	e := newEnv(envOptions{
		Root: o.Root, Runner: o.Runner, Now: o.Now, LookupGroup: o.LookupGroup, LookupUser: o.LookupUser, Chown: o.Chown,
		SocketPath: o.SocketPath, SocketTimeout: o.SocketTimeout, Progress: o.Progress, Logger: o.Logger,
	})
	fail := func(step string, err error) error {
		e.rep.fail(step, err)
		return stepError(deyerr.I014, step, err)
	}
	refuse := func(err error) (*RestoreResult, error) {
		e.rep.fail(StepRestore, err)
		return nil, err
	}

	// restore
	e.rep.start(StepRestore)
	if err := ctx.Err(); err != nil {
		return refuse(deyerr.Wrap(deyerr.X031, err, deyerr.Params{"command": "restore"}))
	}
	ip4, ip6 := strings.TrimSpace(o.PublicIP), strings.TrimSpace(o.PublicIP6)
	if ip4 == "" && ip6 != "" {
		return refuse(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "hub.public_ip", "value": "", "allowed": "the public IP of this server (required together with the IPv6 address)",
		}))
	}
	// The backend user must exist before the archive's owners are mapped
	// by name: a missing name falls back to the numeric id of the old
	// server, which may belong to another group here.
	userWarn := e.ensureSystemUser(ctx)
	opts := validateOptions()
	var cfg *config.Config
	rr, err := install.Restore(install.RestoreOptions{
		Root: e.root, Path: o.Path, Passphrase: o.Passphrase, Now: e.now(),
		Validate: func(data []byte) error {
			c, err := config.ParseWith(data, opts)
			if err != nil {
				return err
			}
			if ip4 != "" {
				if err := moveHub(c, ip4, ip6, opts); err != nil {
					return err
				}
			}
			cfg = c
			return nil
		},
	})
	if err != nil {
		return refuse(err)
	}
	res := &RestoreResult{
		Role:           cfg.Role,
		BackupVersion:  rr.Manifest.Version,
		Created:        rr.Manifest.Created,
		PreviousDir:    rr.PreviousDir,
		Events:         rr.Events,
		SysctlProfile:  config.SysctlOff,
		AddressChanged: ip4 != "",
	}
	if cfg.Hub != nil {
		res.HubName, res.PublicIP, res.PublicIP6 = cfg.Hub.Name, cfg.Hub.PublicIP, cfg.Hub.PublicIP6
	}
	if cfg.Node != nil {
		res.NodeID = cfg.Node.ID
	}
	if err := e.ensureLayout(); err != nil {
		return nil, fail(StepRestore, err)
	}
	v, verr := config.SchemaVersionOf(rr.Config)
	migrated := verr == nil && v < config.SchemaVersion
	if migrated || res.AddressChanged {
		if err := config.SaveWith(e.configPath(), cfg, opts); err != nil {
			return nil, fail(StepRestore, err)
		}
		res.Migrated = migrated
	}
	if res.AddressChanged {
		if err := e.reissueHubCert(cfg); err != nil {
			return nil, fail(StepRestore, err)
		}
	}
	e.rep.okOrWarn(StepRestore, cfg.Role, userWarn)

	// events
	e.rep.start(StepEvents)
	if len(bytes.TrimSpace(rr.Events)) == 0 {
		e.rep.skip(StepEvents, "")
	} else {
		p := e.path(RestoreEventsPath)
		if err := config.WriteFileAtomic(p, rr.Events, 0o600); err != nil {
			return nil, fail(StepEvents, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}))
		}
		res.EventsPath = p
		e.rep.ok(StepEvents, RestoreEventsPath)
	}

	// sysctl
	e.rep.start(StepSysctl)
	profile := config.SysctlOff
	if cfg.Tuning != nil {
		profile = cfg.Tuning.SysctlProfile
	}
	if !o.ApplySysctl || profile == "" || profile == config.SysctlOff {
		e.rep.skip(StepSysctl, profile)
	} else {
		warnings, err := applySysctl(e.root, profile, cfg.Tuning.BBR, keepIPForward(e.root))
		res.SysctlWarnings = warnings
		switch {
		case err != nil:
			e.rep.warn(StepSysctl, profile, err)
		case len(warnings) > 0:
			res.SysctlProfile = profile
			e.rep.warn(StepSysctl, sysctlDetail(profile, warnings), nil)
		default:
			res.SysctlProfile = profile
			e.rep.ok(StepSysctl, profile)
		}
	}

	// service
	unit, other := systemd.HubUnit, systemd.NodeUnit
	if cfg.Role == config.RoleNode {
		unit, other = systemd.NodeUnit, systemd.HubUnit
	}
	if o.StartService {
		// A server restored into the other role must not keep running
		// the old daemon.
		for _, verb := range []string{"stop", "disable"} {
			if verb == "disable" {
				if err := systemd.RemoveWantsLinks(e.root, other); err != nil {
					e.log.Warn("could not unlink "+other, slog.String("err", err.Error()))
				}
			}
			if _, _, err := e.runner.Run(ctx, "systemctl", []string{verb, other}, nil); err != nil && !notLoaded(err) {
				e.log.Warn("could not "+verb+" "+other, slog.String("err", err.Error()))
			}
		}
	}
	if err := e.serviceStep(ctx, o.StartService, unit, true); err != nil {
		return nil, serviceFailed(fail(StepService, err), unit)
	}
	res.ServiceStarted = o.StartService
	return res, nil
}

// moveHub sets the new public addresses of a hub moved to this server and
// validates the result (DEY-C013). A node backup has no such setting.
func moveHub(c *config.Config, ip4, ip6 string, opts config.ValidateOptions) error {
	if c.Role != config.RoleHub || c.Hub == nil {
		return deyerr.New(deyerr.C013, deyerr.Params{
			"field": "hub.public_ip", "value": ip4, "allowed": "nothing: this backup is of a " + c.Role + ", which has no public address setting",
		})
	}
	c.Hub.PublicIP, c.Hub.PublicIP6 = ip4, ip6
	if sameIP(ip4, ip6) {
		c.Hub.PublicIP6 = ""
	}
	return c.Validate(opts)
}

// reissueHubCert gives the hub control certificate the addresses of cfg,
// signed by the restored CA (nodes verify the chain, not the address, so
// this keeps the certificate truthful rather than making nodes connect).
func (e *env) reissueHubCert(cfg *config.Config) error {
	ca, err := tlsutil.LoadCA(e.secret(FileCACert), e.secret(FileCAKey))
	if err != nil {
		return err
	}
	ca.Now = e.now
	ips := []net.IP{net.ParseIP(cfg.Hub.PublicIP)}
	if cfg.Hub.PublicIP6 != "" {
		ips = append(ips, net.ParseIP(cfg.Hub.PublicIP6))
	}
	return e.ensureHubCert(ca, cfg.Hub.Name, ips)
}

// keepIPForward reports whether deyroute's current /etc/sysctl.d/99-deyroute.conf
// on this server sets net.ipv4.ip_forward = 1; re-applying the restored
// profile keeps that decision. Whether a restored ladder needs forwarding
// is not guessed here from backend names (only the backends know, through
// Rendered.IPForward): the hub and node daemons enable it at run time for
// the candidates that need it and persist it with the next optimize apply.
func keepIPForward(root string) bool {
	applied, err := sysctl.Manager{Root: root}.Applied()
	if err != nil {
		return false
	}
	for _, kv := range applied {
		if kv.Key == sysctl.KeyIPForward {
			return strings.TrimSpace(kv.Value) == "1"
		}
	}
	return false
}

// ImportRestoredEvents imports the events Restore left in
// Root/var/lib/deyroute/restore-events.ndjson with importFn (the hub passes
// (*state.Store).ImportEvents) and deletes the file after a successful
// import. No file is not an error (0, nil); a failed import keeps the file
// for the next start. Anything but a regular file (a symlink included) is
// refused with DEY-X032 and kept.
func ImportRestoredEvents(root string, importFn func(r io.Reader) (int, error)) (int, error) {
	if root == "" {
		root = "/"
	}
	p := (&env{root: root}).path(RestoreEventsPath)
	notRegular := func() error {
		return deyerr.New(deyerr.X032, deyerr.Params{"path": p}).WithDetail(p + " is not a regular file")
	}
	// O_NOFOLLOW: a symlink planted in place of the file is not followed;
	// O_NONBLOCK: a FIFO cannot block the daemon start.
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 -- fixed deyroute path under Root
	switch {
	case stderrors.Is(err, fs.ErrNotExist):
		return 0, nil
	case stderrors.Is(err, syscall.ELOOP):
		return 0, notRegular()
	case err != nil:
		return 0, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return 0, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
		return 0, notRegular()
	}
	n, ierr := importFn(f)
	_ = f.Close()
	if ierr != nil {
		return n, ierr
	}
	if err := os.Remove(p); err != nil && !stderrors.Is(err, fs.ErrNotExist) {
		return n, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	return n, nil
}
