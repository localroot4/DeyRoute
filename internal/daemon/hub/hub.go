package hub

import (
	"bytes"
	"context"
	"crypto/tls"
	stderrors "errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	// Every backend registers itself: config validation needs the whole
	// transport registry (KnownTransport).
	_ "github.com/localroot4/deyroute/internal/backend/all"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/notify"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// Hub is one running hub daemon. Create it with New and run it with Serve
// (or use Run for both). All methods are safe for concurrent use.
type Hub struct {
	o       Options
	log     *slog.Logger
	evLog   *slog.Logger
	closers []io.Closer

	cfgPath string
	valOpts config.ValidateOptions
	cfgMu   sync.RWMutex
	cfg     *config.Config // immutable snapshot; replaced on every change
	mutMu   sync.Mutex     // serialises config.yaml mutations

	st        *state.Store
	statePath string

	// caMu guards the CA state, which changes during rotate-ca (use the
	// accessors currentCA, trustPEM, tunnelCAPEM and secretStore).
	caMu     sync.RWMutex
	ca       *tlsutil.CA    // signs node certificates; its fingerprint goes into join links
	caPEM    []byte         // CA copied to the tunnel config dirs (ca.crt)
	trust    []byte         // CA bundle nodes trust and the control channel accepts
	secrets  *secrets.Store // tunnel secrets; its CA issues the tunnel certificates
	tlsCfg   *tls.Config    // handed to the Control API: serves tlsLive
	tlsLive  atomic.Pointer[tls.Config]
	joins    *secrets.JoinTokens
	rotateMu sync.Mutex // serialises rotate-ca

	// ops holds the state of the operations of ops_*.go and jobs.go.
	ops opsState

	ln net.Listener
	// ctlPort is hub.control_port as it was when ln was bound: the firewall
	// keeps protecting it until the hub restarts on a changed port.
	ctlPort int
	ctl     *api.ControlServer
	local   *local

	nodesMu sync.Mutex
	nodes   map[string]*nodeRuntime

	upMu    sync.Mutex
	uploads map[string]*pendingUpload

	subMu   sync.Mutex
	subs    map[int]chan state.Event
	nextSub int

	notifyQ  chan state.Event
	notifier atomic.Pointer[notify.Telegram]

	fwKick    chan struct{}
	fwWake    chan struct{}
	fwApplyMu sync.Mutex // serialises firewall applies
	fwCarry   string     // last saved natRecord (JSON); fwApplyMu
	fwMu      sync.Mutex // guards fw
	fw        firewallStatus

	assetMu  sync.Mutex // serialises downloads of other architectures
	digestMu sync.Mutex
	digests  map[string]fileDigest

	warnMu    sync.Mutex
	certWarns []api.Warning
	certAt    time.Time

	// tun owns the tunnel controllers (tunnels.go).
	tun tunnelManager

	// alive is the wall-clock time (UnixNano) of the offline detector's last
	// pass: the watchdog pings systemd only while it keeps running.
	alive atomic.Int64

	serving   atomic.Bool
	closeOnce sync.Once
}

// Run is `deyroute daemon hub`: New followed by Serve. It returns nil once
// ctx ends and everything the hub started has stopped, or an error when the
// hub cannot start (config, state database, CA, listeners).
func Run(ctx context.Context, o Options) error {
	h, err := New(o)
	if err != nil {
		return err
	}
	return h.Serve(ctx)
}

// New loads everything the hub needs and opens its resources: config.yaml
// (role hub, strict validation against the backend registry), state.db
// (a recovered corruption is logged as DEY-X001), hub.log and events.log,
// the backend manifest override, the CA and the hub control certificate,
// the secret stores, restored events (imported once) and the Control API
// listener. Serve must be called to run the hub; Close releases the
// resources when Serve is never called.
func New(o Options) (_ *Hub, err error) {
	o = o.withDefaults()
	h := &Hub{
		o:       o,
		cfgPath: filepath.Join(o.Root, config.DefaultPath),
		valOpts: config.ValidateOptions{KnownTransport: backend.KnownTransport, ValidTransports: backend.ValidIDs},
		nodes:   map[string]*nodeRuntime{},
		uploads: map[string]*pendingUpload{},
		subs:    map[int]chan state.Event{},
		notifyQ: make(chan state.Event, notifyQueueSize),
		fwKick:  make(chan struct{}, 1),
		fwWake:  make(chan struct{}, 1),
		digests: map[string]fileDigest{},
	}
	h.tun.init()
	h.ops.init()
	defer func() {
		if err != nil {
			h.release()
		}
	}()

	cfg, err := config.LoadWith(h.cfgPath, h.valOpts)
	if err != nil {
		return nil, err
	}
	if cfg.Role != config.RoleHub || cfg.Hub == nil {
		return nil, deyerr.New(deyerr.X009, deyerr.Params{"role": cfg.Role, "need": config.RoleHub})
	}
	h.cfg = cfg

	if err := h.openLogs(); err != nil {
		return nil, err
	}
	if err := h.openState(); err != nil {
		return nil, err
	}
	if err := backend.LoadManifestOverride(h.path(config.ManifestPath)); err != nil {
		h.log.Error("the backend manifest override cannot be used; the built-in manifest stays active",
			slog.String("path", h.path(config.ManifestPath)), dlog.Err(err), dlog.Code(deyerr.S001))
	}
	if err := h.loadTLS(); err != nil {
		return nil, err
	}
	h.secrets = &secrets.Store{Root: o.Root, CA: h.ca, Now: o.Now}
	h.joins = &secrets.JoinTokens{Path: h.secrets.JoinTokensPath(), Now: o.Now}
	h.registerSecrets()
	h.importRestoredEvents()
	h.loadNodes()
	h.loadOps()
	h.reloadNotifier(cfg)

	ln, err := net.Listen("tcp", o.controlListen(cfg.Hub.ControlPort))
	if err != nil {
		return nil, deyerr.Wrap(deyerr.P012, err, deyerr.Params{
			"port": strconv.Itoa(cfg.Hub.ControlPort) + "/tcp", "process": "another process",
			"addr": o.controlListen(cfg.Hub.ControlPort),
		}).WithDetail(err.Error()).WithLog(LogFile)
	}
	h.ln, h.ctlPort = ln, cfg.Hub.ControlPort
	h.ctl = &api.ControlServer{TLSConfig: h.tlsCfg, Handler: controlHandler{h}, Logger: h.log, Clock: o.Now}
	h.local = &local{h: h}
	return h, nil
}

// openLogs sets up hub.log (unless a Logger was given) and events.log.
func (h *Hub) openLogs() error {
	if h.o.Logger != nil {
		h.log = h.o.Logger
	} else {
		l, c, err := dlog.New(dlog.Options{Component: config.RoleHub, File: h.path(LogFile)})
		if err != nil {
			return err
		}
		h.closers = append(h.closers, c)
		h.log = l
	}
	ev, c, err := dlog.New(dlog.Options{Component: "events", File: h.path(EventsLogFile)})
	if err != nil {
		return err
	}
	h.closers = append(h.closers, c)
	h.evLog = ev
	return nil
}

// openState opens state.db; a recovered corruption is logged, not fatal.
func (h *Hub) openState() error {
	h.statePath = h.path(config.StatePath)
	// /var/lib/deyroute normally exists (installer: 0750 root:deyroute). When
	// it does not, it must stay traversable for the deyroute user, which runs
	// the backends from /var/lib/deyroute/bin (ARCHITECTURE.md §7.5); state.db
	// itself is 0600.
	err := os.MkdirAll(filepath.Dir(h.statePath), 0o755) // #nosec G301 -- see above
	if err != nil {
		return deyerr.Wrap(deyerr.X021, err, deyerr.Params{"op": "open", "path": h.statePath})
	}
	st, err := state.Open(h.statePath)
	if err != nil {
		return err
	}
	h.st = st
	if rec := st.Recovered(); rec != nil {
		h.log.Error("state database was corrupt and has been recovered", dlog.Code(deyerr.X001),
			slog.String("path", h.statePath), slog.String("detail", rec.Detail))
	}
	return nil
}

// loadTLS reads the CA and the hub control certificate from secrets/. A
// previous CA left by an unfinished rotate-ca (ca.prev.crt) stays trusted
// until the rotation is completed. The Control API is served from a live
// configuration (tlsLive) that rotate-ca replaces without a restart.
func (h *Hub) loadTLS() error {
	dir := h.path(config.SecretsDir)
	ca, err := tlsutil.LoadCA(filepath.Join(dir, setup.FileCACert), filepath.Join(dir, setup.FileCAKey))
	if err != nil {
		return err
	}
	ca.Now = h.o.Now
	certPEM, err := readFile(filepath.Join(dir, setup.FileHubCert))
	if err != nil {
		return err
	}
	keyPEM, err := readFile(filepath.Join(dir, setup.FileHubKey))
	if err != nil {
		return err
	}
	trust := append([]byte(nil), ca.CertPEM...)
	prev, perr := os.ReadFile(filepath.Join(dir, FilePrevCACert)) // #nosec G304 -- fixed secrets path below Root
	if perr == nil {
		if _, err := tlsutil.ParseCertChain(prev); err == nil {
			trust = joinPEM(trust, prev)
			h.log.Warn("a CA rotation was not finished: the previous CA is still trusted; run deyroute security rotate-ca to finish it")
		}
	}
	cfg, err := tlsutil.ServerTLSConfig(trust, certPEM, keyPEM)
	if err != nil {
		return err
	}
	h.ca, h.caPEM, h.trust = ca, ca.CertPEM, trust
	h.tlsLive.Store(cfg)
	h.tlsCfg = &tls.Config{
		MinVersion:             tls.VersionTLS13,
		NextProtos:             []string{api.ALPN},
		SessionTicketsDisabled: true,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return h.tlsLive.Load(), nil
		},
	}
	return nil
}

// currentCA returns the CA that signs node certificates.
func (h *Hub) currentCA() *tlsutil.CA {
	h.caMu.RLock()
	defer h.caMu.RUnlock()
	return h.ca
}

// trustPEM returns the CA bundle nodes trust (the join answer).
func (h *Hub) trustPEM() []byte {
	h.caMu.RLock()
	defer h.caMu.RUnlock()
	return h.trust
}

// tunnelCAPEM returns the CA copied into the tunnel config directories.
func (h *Hub) tunnelCAPEM() []byte {
	h.caMu.RLock()
	defer h.caMu.RUnlock()
	return h.caPEM
}

// secretStore returns the tunnel secret store (replaced by rotate-ca).
func (h *Hub) secretStore() *secrets.Store {
	h.caMu.RLock()
	defer h.caMu.RUnlock()
	return h.secrets
}

// joinPEM concatenates PEM documents, each ending with a newline.
func joinPEM(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		out = append(out, p...)
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	return out
}

func readFile(p string) ([]byte, error) {
	data, err := os.ReadFile(p) // #nosec G304 -- fixed secret paths below Root
	if err != nil {
		return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": p, "reason": err.Error()})
	}
	return data, nil
}

// registerSecrets registers every tunnel token and the Telegram token with
// the log redactor, so no secret ever reaches a log line (section 11).
func (h *Hub) registerSecrets() {
	tunnels, err := h.secrets.Tunnels()
	if err != nil {
		h.log.Warn("cannot list tunnel tokens", dlog.Err(err))
	}
	for _, t := range tunnels {
		if _, err := h.secrets.Token(t); err != nil {
			h.log.Warn("cannot read a tunnel token", dlog.Tunnel(t), dlog.Err(err))
		}
	}
}

// importRestoredEvents imports the events a restore left behind (once).
func (h *Hub) importRestoredEvents() {
	n, err := setup.ImportRestoredEvents(h.o.Root, h.st.ImportEvents)
	if err != nil {
		h.log.Error("cannot import the restored events", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	if n > 0 {
		h.log.Info("restored events imported", slog.Int("events", n))
	}
}

// Serve runs the hub until ctx ends: the Control API, the Local API (then
// READY=1), the watchdog, the node offline detector, the firewall manager,
// the notifier and the hourly state snapshot. It returns nil after a clean
// shutdown (every goroutine stopped, state.db and the log files closed) and
// an error when the Control API or the Local API cannot be served. Serve may
// be called once.
func (h *Hub) Serve(ctx context.Context) error {
	if !h.serving.CompareAndSwap(false, true) {
		return deyerr.Wrap(deyerr.X000, stderrors.New("hub: Serve called twice"), nil)
	}
	defer h.release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	h.log.Info("hub starting", slog.String("version", version.Version),
		slog.String("control", h.ln.Addr().String()), slog.String("socket", h.o.SocketPath))

	var wg sync.WaitGroup
	fatal := make(chan error, 2)
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	// The tunnel manager is ready before the Local API accepts calls; the
	// startup reconcile (section 9: recovery after a hub restart) runs in
	// its own goroutine.
	h.startTunnels(ctx)
	run(func() { h.runTunnels(ctx) })
	run(func() {
		if err := h.ctl.Serve(ctx, h.ln); err != nil && ctx.Err() == nil {
			fatal <- err
		}
	})
	run(func() {
		handler := api.NewLocalHandler(h.local, h.log)
		err := api.ServeLocalReady(ctx, h.o.SocketPath, handler, h.log, func() {
			if err := h.o.Notify(systemd.StateReady); err != nil {
				h.log.Warn("sd_notify READY failed", dlog.Err(err))
			}
			h.log.Info("hub ready")
			if h.o.OnReady != nil {
				h.o.OnReady(h)
			}
		})
		if err != nil && ctx.Err() == nil {
			fatal <- err
		}
	})
	h.alive.Store(time.Now().UnixNano())
	run(func() { h.watchdogLoop(ctx) })
	run(func() { h.monitorLoop(ctx) })
	run(func() { h.firewallLoop(ctx) })
	run(func() { h.notifyLoop(ctx) })
	run(func() { h.snapshotLoop(ctx) })
	run(func() { dlog.RotateDirLoop(ctx, h.path(systemd.TunnelLogDir), dlog.RotateDirInterval, h.log) })
	h.startJobs(ctx, run)
	h.requestFirewall()

	var result error
	select {
	case <-ctx.Done():
	case err := <-fatal:
		h.log.Error("hub cannot serve; stopping", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		result = err
	}
	_ = h.o.Notify(systemd.StateStopping)
	cancel()
	wg.Wait()
	h.closeSubscribers()
	h.snapshot()
	h.log.Info("hub stopped")
	return result
}

// Close releases the resources New opened when Serve is never called. It is
// a no-op after Serve.
func (h *Hub) Close() error {
	if h.serving.Load() {
		return nil
	}
	h.release()
	return nil
}

// release closes the listener, state.db and the log files (once).
func (h *Hub) release() {
	h.closeOnce.Do(func() {
		if h.ln != nil {
			_ = h.ln.Close()
		}
		if h.st != nil {
			if err := h.st.Close(); err != nil && h.log != nil {
				h.log.Warn("closing the state database failed", dlog.Err(err))
			}
		}
		for i := len(h.closers) - 1; i >= 0; i-- {
			_ = h.closers[i].Close()
		}
	})
}

// Local returns the Local API implementation (the CLI and TUI use it over
// the unix socket; tests may call it directly).
func (h *Hub) Local() api.Local { return h.local }

// ControlAddr returns the address the Control API listens on.
func (h *Hub) ControlAddr() net.Addr { return h.ln.Addr() }

// State returns the state database (for the tunnel controller).
func (h *Hub) State() *state.Store { return h.st }

// Logger returns the hub logger.
func (h *Hub) Logger() *slog.Logger { return h.log }

// path joins an absolute system path with Root.
func (h *Hub) path(p string) string { return filepath.Join(h.o.Root, p) }

// now returns the current time in UTC.
func (h *Hub) now() time.Time { return h.o.Now().UTC() }

// Config returns the current configuration snapshot. It must not be
// modified; changes go through mutate.
func (h *Hub) Config() *config.Config {
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()
	return h.cfg
}

// setConfig installs a new configuration snapshot.
func (h *Hub) setConfig(c *config.Config) {
	h.cfgMu.Lock()
	h.cfg = c
	h.cfgMu.Unlock()
	// Tunnels or certificate paths may have changed.
	h.warnMu.Lock()
	h.certAt = time.Time{}
	h.warnMu.Unlock()
}

// mutate changes the configuration: fn edits a copy of the applied
// configuration (not the file on disk), the result is validated, written
// to config.yaml atomically (temp file + rename) and installed as the
// current configuration. Mutations are serialised. Nothing changes when fn
// or validation fails, or when config.yaml holds an edit that was not
// applied (DEY-C026, see checkApplied).
func (h *Hub) mutate(fn func(c *config.Config) error) (*config.Config, error) {
	return h.mutateCommit(fn, nil)
}

// mutateCommit is mutate with commit (when not nil) run after fn and the
// validation succeeded, right before config.yaml is written: when commit
// fails nothing is written either (join spends its token there).
func (h *Hub) mutateCommit(fn func(c *config.Config) error, commit func() error) (*config.Config, error) {
	h.mutMu.Lock()
	defer h.mutMu.Unlock()
	cur := h.Config()
	if err := h.checkApplied(cur); err != nil {
		return nil, err
	}
	c := config.Clone(cur)
	if err := fn(c); err != nil {
		return nil, err
	}
	c.ApplyDefaults()
	if err := c.Validate(h.valOpts); err != nil {
		return nil, err
	}
	if commit != nil {
		if err := commit(); err != nil {
			return nil, err
		}
	}
	if err := config.SaveWith(h.cfgPath, c, h.valOpts); err != nil {
		return nil, err
	}
	h.setConfig(c)
	return c, nil
}

// checkApplied returns DEY-C026 unless config.yaml still holds the applied
// configuration cur (comments and formatting aside). A manual edit waits
// for `deyroute config apply`: writing cur over it would silently discard
// it, and changing the edited file instead would take it over without the
// checks of config apply (immutable ids, section 4). An edit that does not
// even load is reported in the detail.
func (h *Hub) checkApplied(cur *config.Config) error {
	disk, err := config.LoadWith(h.cfgPath, h.valOpts)
	if err == nil {
		if same, merr := sameConfig(disk, cur); merr == nil && same {
			return nil
		}
		return deyerr.New(deyerr.C026, deyerr.Params{"path": config.DefaultPath})
	}
	return deyerr.Wrap(deyerr.C026, err, deyerr.Params{"path": config.DefaultPath}).WithDetail(deyerr.As(err).Error())
}

// sameConfig reports whether a and b encode to the same config.yaml.
func sameConfig(a, b *config.Config) (bool, error) {
	da, err := config.Marshal(a)
	if err != nil {
		return false, err
	}
	db, err := config.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(da, db), nil
}

// autoBackup takes the automatic backup that precedes every owner-initiated
// apply (section 5: backups/auto/, the last 20 are kept).
func (h *Hub) autoBackup() (string, error) { return h.autoBackupOf(nil) }

// autoBackupOf is autoBackup with cfg (when not nil) stored as config.yaml:
// the configuration that was running before a `config apply`.
func (h *Hub) autoBackupOf(cfg *config.Config) (string, error) {
	var data []byte
	if cfg != nil {
		var err error
		if data, err = config.Marshal(cfg); err != nil {
			return "", err
		}
	}
	p, err := install.AutoBackupConfig(h.o.Root, AutoBackupKeep, data)
	if err != nil {
		h.log.Error("automatic backup failed", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return "", err
	}
	return p, nil
}

// snapshotLoop copies state.db to state.db.bak every SnapshotInterval.
func (h *Hub) snapshotLoop(ctx context.Context) {
	t := time.NewTicker(h.o.SnapshotInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.snapshot()
		}
	}
}

// snapshot writes a consistent copy of state.db to state.db.bak.
func (h *Hub) snapshot() {
	if h.st == nil {
		return
	}
	if err := h.st.Snapshot(state.BackupPath(h.statePath)); err != nil {
		h.log.Warn("state snapshot failed", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
}

// firewallStatus is the last firewall computation.
type firewallStatus struct {
	spec    firewall.Spec
	err     error
	applied time.Time
	managed bool
	done    bool
}
