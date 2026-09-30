package hub

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Files and names of the hub daemon (absolute system paths; the hub
// prefixes them with Options.Root).
const (
	// ServiceName is the hub's systemd service (without ".service").
	ServiceName = "deyroute-hub"
	// LogFile is the hub's own log (section 2).
	LogFile = config.LogDir + "/hub.log"
	// EventsLogFile receives one JSON line per event (sections 2, 3, 13).
	EventsLogFile = config.LogDir + "/events.log"
	// AssetCacheDir caches deyroute binaries of other architectures:
	// deyroute-<version>-<arch> (section 5, GET /v1/assets/deyroute/<arch>).
	AssetCacheDir = config.BinDir
	// DownloadDir stages downloads (release archives for other
	// architectures).
	DownloadDir = config.LibDir + "/tmp"
	// AutoBackupKeep is how many automatic backups are kept (section 5).
	AutoBackupKeep = 20
)

// Defaults of the Options timings.
const (
	// DefaultPingInterval is how often the hub measures the control RTT of
	// every connected node.
	DefaultPingInterval = 5 * time.Second
	// DefaultMonitorInterval is how often the offline detector runs.
	DefaultMonitorInterval = time.Second
	// DefaultFirewallDebounce collects firewall changes before one apply.
	DefaultFirewallDebounce = 500 * time.Millisecond
	// DefaultSnapshotInterval is how often state.db is copied to state.db.bak.
	DefaultSnapshotInterval = time.Hour
	// DefaultNodePersistEvery bounds how often heartbeat-only changes of a
	// node are written to state.db (transitions are written at once).
	DefaultNodePersistEvery = 30 * time.Second
	// DefaultFetchTimeout bounds one fetch.proxy when the caller's context
	// has no deadline.
	DefaultFetchTimeout = 10 * time.Minute
	// DefaultUploadGrace is how long a finished fetch.proxy waits for its
	// upload to complete.
	DefaultUploadGrace = 10 * time.Second
	// pingTimeout bounds one ping.
	pingTimeout = 5 * time.Second
)

// Options configure New and Run. Only Root is commonly set; every other
// field has a production default and exists so tests (and the end-to-end
// harness) run the hub unprivileged in a temporary directory.
type Options struct {
	// Root prefixes every filesystem path ("/" when empty).
	Root string
	// Runner runs allow-listed programs (systemctl, nft, ss, ip); nil =
	// exec.NewRunner().
	Runner exec.Runner
	// Logger receives the hub log; nil = internal/log writing
	// Root/var/log/deyroute/hub.log (rotated, redacted).
	Logger *slog.Logger
	// Now is the clock; nil = time.Now.
	Now func() time.Time
	// ControlListen is the Control API listen address; "" =
	// ":<hub.control_port>". Tests use "127.0.0.1:0".
	ControlListen string
	// SocketPath is the Local API socket; "" = Root/run/deyroute/daemon.sock.
	SocketPath string
	// SelfBinary is the deyroute binary served to nodes of the hub's own
	// architecture; "" = os.Executable().
	SelfBinary string
	// Arch is the architecture of SelfBinary; "" = runtime.GOARCH.
	Arch string
	// Fetcher downloads directly (no node); nil = install.HTTPFetcher{}.
	Fetcher install.Fetcher
	// Systemd manages units; nil = &systemd.Manager{Runner, Root}.
	Systemd *systemd.Manager
	// DeyrouteIDs returns the uid/gid of the system user deyroute (rendered
	// files are root:deyroute); nil = render.LookupDeyrouteIDs.
	DeyrouteIDs func() (uid, gid int, err error)
	// Chown changes file ownership; nil = os.Lchown.
	Chown func(path string, uid, gid int) error
	// FailoverClock drives the failover engines; nil = failover.RealClock{}.
	FailoverClock failover.Clock
	// ProbeHost is the address path probes dial on the hub; "" = 127.0.0.1.
	ProbeHost string
	// DisableFirewall computes the firewall specification but never runs
	// nft (tests).
	DisableFirewall bool
	// OnReady is called once the Control API and the Local API accept
	// connections (tests).
	OnReady func(h *Hub)
	// Notify sends sd_notify states; nil = systemd.Notify.
	Notify func(state string) error
	// Getenv reads the environment (DEYROUTE_MIRROR); nil = os.Getenv.
	Getenv func(key string) string
	// TelegramAPIBase overrides the Telegram Bot API base URL (tests).
	TelegramAPIBase string
	// ProcRoot is the root of the /proc tree the port checker reads to name
	// the process holding a port ("/" when empty).
	ProcRoot string
	// BindCheck is stage 1 of the port check (section 10: can the port be
	// bound locally, and who holds it); nil = ports.Checker{Runner,
	// ProcFS{ProcRoot}}.CheckBind.
	BindCheck func(ctx context.Context, port int, proto string) ports.BindResult

	// Timings; zero values take the defaults above and the api package's
	// (offline after 15 s without a heartbeat).
	OfflineAfter     time.Duration
	PingInterval     time.Duration
	MonitorInterval  time.Duration
	FirewallDebounce time.Duration
	SnapshotInterval time.Duration
	NodePersistEvery time.Duration
	UploadGrace      time.Duration
	FollowPoll       time.Duration

	// Tunnel controller timings (tunnel_ctl.go): TunnelUpWait (60 s) is how
	// long TunnelAdd waits for the new tunnel to come up, RecheckInterval
	// (30 min) re-tests skipped rungs and UDP reachability, ReportInterval
	// (60 s) probes every port map for reporting, UnitStartCheck (300 ms) is
	// how long a started hub unit runs before its state is checked and
	// UDPProbeTimeout (2 s) is the wait for each of the 3 UDP echo tries.
	TunnelUpWait    time.Duration
	RecheckInterval time.Duration
	ReportInterval  time.Duration
	UnitStartCheck  time.Duration
	UDPProbeTimeout time.Duration

	// Operations (ops_*.go, jobs.go).

	// MinisignKey verifies the signatures of release SHA256SUMS and of
	// backends.yaml (update, update manifest); "" = install.MinisignPublicKey.
	MinisignKey string
	// ObtainACME obtains a certificate for tls.mode acme; nil =
	// tlsutil.ObtainACME (Let's Encrypt).
	ObtainACME func(ctx context.Context, o tlsutil.ACMEOptions) (certPEM, keyPEM []byte, err error)
	// DecoyCheck tests one decoy SNI of the Reality transports (section
	// 7.4: a TLS 1.3 handshake from the hub to sni:443); nil = the real
	// handshake verified against the system roots.
	DecoyCheck func(ctx context.Context, sni string) error
	// Job timings; zero values take the defaults of jobs.go: the decoy
	// check every 6 h (DecoyInterval), the TLS renewal pass and the optional
	// update check once a day (TLSRenewInterval, UpdateCheckInterval), the
	// connection metrics every 30 s (MetricsInterval); the first TLS and
	// update pass runs JobStartDelay (10 min) after the start.
	DecoyInterval       time.Duration
	TLSRenewInterval    time.Duration
	UpdateCheckInterval time.Duration
	MetricsInterval     time.Duration
	JobStartDelay       time.Duration
	// BackendProbeWait is how long `update backends` waits for the probe
	// of a restarted active transport before it rolls back (section 5: 60 s).
	BackendProbeWait time.Duration
	// RestartDelay separates the answer of `update` / `update --rollback`
	// from the restart of deyroute-hub (1 s).
	RestartDelay time.Duration
	// NodeReconnectWait bounds how long rotate-ca waits for a node to come
	// back with its new certificate (30 s).
	NodeReconnectWait time.Duration
	// DiagReadyWait bounds how long diag speed waits until its temporary
	// copy of the transport forwards (15 s, like a started rung).
	DiagReadyWait time.Duration
}

func (o Options) withDefaults() Options {
	if o.Root == "" {
		o.Root = "/"
	}
	if o.Runner == nil {
		o.Runner = exec.NewRunner()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SocketPath == "" {
		o.SocketPath = filepath.Join(o.Root, config.SocketPath)
	}
	if o.SelfBinary == "" {
		if p, err := os.Executable(); err == nil {
			o.SelfBinary = p
		} else {
			o.SelfBinary = config.BinaryPath
		}
	}
	if o.Arch == "" {
		o.Arch = runtime.GOARCH
	}
	if o.Fetcher == nil {
		o.Fetcher = install.HTTPFetcher{}
	}
	if o.Systemd == nil {
		o.Systemd = &systemd.Manager{Runner: o.Runner, Root: o.Root}
	}
	if o.DeyrouteIDs == nil {
		o.DeyrouteIDs = render.LookupDeyrouteIDs
	}
	if o.Chown == nil {
		o.Chown = os.Lchown
	}
	if o.FailoverClock == nil {
		o.FailoverClock = failover.RealClock{}
	}
	if o.ProbeHost == "" {
		o.ProbeHost = "127.0.0.1"
	}
	if o.Notify == nil {
		o.Notify = systemd.Notify
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.ProcRoot == "" {
		o.ProcRoot = "/"
	}
	if o.BindCheck == nil {
		checker := ports.Checker{Runner: o.Runner, ProcFS: ports.ProcFS{Root: o.ProcRoot}}
		o.BindCheck = checker.CheckBind
	}
	if o.MinisignKey == "" {
		o.MinisignKey = install.MinisignPublicKey
	}
	if o.ObtainACME == nil {
		o.ObtainACME = tlsutil.ObtainACME
	}
	if o.DecoyCheck == nil {
		o.DecoyCheck = checkDecoyTLS
	}
	setDur(&o.DecoyInterval, DefaultDecoyInterval)
	setDur(&o.TLSRenewInterval, DefaultTLSRenewInterval)
	setDur(&o.UpdateCheckInterval, DefaultUpdateCheckInterval)
	setDur(&o.MetricsInterval, DefaultMetricsInterval)
	setDur(&o.JobStartDelay, DefaultJobStartDelay)
	setDur(&o.BackendProbeWait, DefaultBackendProbeWait)
	setDur(&o.RestartDelay, DefaultRestartDelay)
	setDur(&o.NodeReconnectWait, DefaultNodeReconnectWait)
	setDur(&o.DiagReadyWait, DefaultDiagReadyWait)
	setDur(&o.TunnelUpWait, DefaultTunnelUpWait)
	setDur(&o.RecheckInterval, DefaultRecheckInterval)
	setDur(&o.ReportInterval, DefaultReportInterval)
	setDur(&o.UnitStartCheck, DefaultUnitStartCheck)
	setDur(&o.UDPProbeTimeout, health.UDPTimeout)
	setDur(&o.OfflineAfter, api.OfflineAfter)
	setDur(&o.PingInterval, DefaultPingInterval)
	setDur(&o.MonitorInterval, DefaultMonitorInterval)
	setDur(&o.FirewallDebounce, DefaultFirewallDebounce)
	setDur(&o.SnapshotInterval, DefaultSnapshotInterval)
	setDur(&o.NodePersistEvery, DefaultNodePersistEvery)
	setDur(&o.UploadGrace, DefaultUploadGrace)
	setDur(&o.FollowPoll, DefaultFollowPoll)
	return o
}

func setDur(d *time.Duration, def time.Duration) {
	if *d <= 0 {
		*d = def
	}
}

// controlListen returns the Control API listen address for cfg.
func (o Options) controlListen(controlPort int) string {
	if o.ControlListen != "" {
		return o.ControlListen
	}
	return ":" + strconv.Itoa(controlPort)
}
