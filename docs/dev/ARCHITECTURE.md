# DEYROUTE — developer architecture guide

This is the implementation contract for contributors (human or agent). The
product specification is `docs/spec/DEYROUTE-spec-v1.0.fa.md` (Persian; section
numbers below refer to it). Where this guide and the spec disagree, the spec
wins and the conflict goes to `QUESTIONS.md`.

## 1. Ground rules (apply to every package)

- Go module `github.com/localroot4/deyroute`, Go 1.24, `CGO_ENABLED=0`.
  Build/test with `GOTOOLCHAIN=local`. **Never edit `go.mod`/`go.sum`** — every
  allowed dependency is already pinned (spec §15 list). Do not add modules.
- Errors: return `*errors.Error` (`internal/errors`) with a DEY code for anything
  an owner can see: `deyerr.New(deyerr.P012, deyerr.Params{"port": "443/tcp", ...})`
  or `deyerr.Wrap(code, cause, params)`. Import it as
  `deyerr "github.com/localroot4/deyroute/internal/errors"`. New codes are added
  to `internal/errors/codes.go` **with Message, Why and Fix**, then
  `make docs` regenerates `docs/ERRORS.md` (the test fails otherwise).
- No `panic` outside `init`. No goroutine without an owner and a stop path
  (context). Every network/exec call has a context with timeout.
- **Exec allow-list**: only `internal/exec` may import `os/exec`. Allowed
  programs: `systemctl`, `nft`, `ss`, `ip`, `xray`, `rathole`, `ufw`,
  `firewall-cmd`, `iptables`, `systemd-sysusers` (QUESTIONS.md C.15),
  `userdel`/`groupdel` for uninstall (C.30) and `haproxy` (looked up for
  direct/haproxy; only `haproxy -v` may run).
  A test in `internal/exec` scans the tree and fails on any other `os/exec` use.
- **Testability**: every package that touches the filesystem takes a root
  directory (`Root string`, default `/`) or explicit paths so tests run in
  `t.TempDir()`; every package that runs programs takes an `exec.Runner`
  interface so tests use a fake. Time-dependent logic takes a `Clock`.
- UI strings live only in `internal/i18n/en.go` (TUI and CLI human output).
  Library packages return data and DEY errors, not prose for the UI.
- Logging: `log/slog` via `internal/log`; fields `ts, level, component,
  tunnel, node, transport, code, msg, err`. Never log secrets; call
  `log.RegisterSecret(value)` for every token/key/password you load.
- Tests: `testing` + `testify/require`; `goleak.VerifyTestMain` in packages
  that start goroutines; golden files under `testdata/` with an `-update`
  flag. `go test -race` must pass. Coverage ≥ 70 % for `config`, `failover`,
  `ports`, `errors`, `backend/*`.
- Lint: `golangci-lint run ./...` (errcheck, govet, staticcheck, gosec,
  revive) clean; `gofmt`. Exported identifiers have doc comments.

## 2. Package map and responsibilities

| Package | Owns | Key consumers |
| --- | --- | --- |
| `cmd/deyroute` | entrypoint, ldflags | — |
| `internal/errors` | DEY codes, formatting, exit codes | all |
| `internal/i18n` | every UI string | tui, cli |
| `internal/version` | build info, compatibility (major.minor) | all |
| `internal/config` | schema types, strict load, validation, migrations, atomic save, ladder resolution | daemon, cli, backends |
| `internal/state` | bbolt store: nodes, tunnels, probes, events ring, metrics, ctl-port allocations | daemon, failover |
| `internal/log` | slog JSON setup, rotation 20MB×5 gzip, redaction | all daemons |
| `internal/exec` | allow-listed command runner | systemd, firewall, ports, backends keygen |
| `internal/systemd` | unit templates, drop-in rendering, systemctl wrapper, sd_notify | daemon |
| `internal/firewall` | detection (nft/ufw/firewalld/iptables), `inet deyroute` rendering/apply/remove, suggestions | daemon, cli |
| `internal/ports` | port-input parser, bind check, owning process lookup, free-port suggestions | daemon, cli, tui |
| `internal/sysctl` | profiles, apply with backup, revert, BBR detection | daemon, setup |
| `internal/tlsutil` | internal CA (Ed25519), hub/node/tunnel certs, CSR, fingerprints, PKCS#12 encoder, custom-cert validation, ACME (lego) | api, daemon |
| `internal/health` | probes (path auto/tcp/tls/http), UDP echo client+server, TCP echo, worker pool (8), RTT history/median | daemon, failover |
| `internal/notify` | Telegram notifier with per-(tunnel,type) rate limit, alias → event mapping | daemon |
| `internal/install` | download chain (mirror → release base → GitHub → via node), sha256 + minisign verification, archive extraction, backend install layout, self-update/rollback, backup/restore (age), uninstall steps | daemon, cli |
| `internal/backend` | interface, registry, manifest (`backends.yaml`), shared render helpers | all backends, daemon |
| `internal/backend/<name>` | one backend each; pure `Render`, golden tests | registry |
| `internal/backend/all` | blank-imports every backend | daemon, cli |
| `internal/failover` | per-tunnel state machine (actor), candidate selection, quarantine, anti-flap, failback, canary | daemon |
| `internal/api` | Local API contract + unix-socket transport; Control API (join, stream, upload, assets) server/client | daemon, cli, tui |
| `internal/daemon` | hub & node services, setup wizard logic, join, renderer/unit manager, reconcile loop, Local API implementation | cli |
| `internal/doctor` | collection helpers, the 15 rules, redacted tar.gz | cli, daemon |
| `internal/cli` | cobra commands (thin) | main |
| `internal/tui` | Bubble Tea models/views (no logic) | cli |

## 3. Contracts already in the tree (do not change signatures without the owner)

- `internal/config/types.go` — schema structs, defaults, enums, paths.
- `internal/backend/backend.go` — `Backend`, `Transport`, `RenderInput`,
  `Rendered`, `UnitSpec`, `PortUse`, `NATRule`, `Secrets`, `Paths`,
  `ManifestEntry`, `KeyGenerator`, registry (`Register`, `Lookup`, `All`,
  `AllTransports`, `KnownTransport`, `ValidIDs`); `manifest.go` +
  `backends.yaml`.
- `internal/state/types.go` — record types, buckets, event names, states.
- `internal/api/local.go` — the `Local` interface and every DTO.
- `internal/api/control.go` — Control API paths, messages, command names and
  argument/result types.

## 4. Package APIs to implement (exported surface)

Signatures are the minimum; add helpers as needed. `ctx` = `context.Context`.

### internal/config
```go
func Load(path string) (*Config, error)            // strict (KnownFields) → C001 with line; C014; migrates; applies defaults; validates
func Parse(data []byte) (*Config, error)            // same, from bytes
func Save(path string, c *Config) error             // validate, marshal, temp file 0600 + fsync + rename (+ dir fsync) → C017
func (c *Config) Validate(opt ValidateOptions) error // all DEY-C rules of §4 (errors.Join of every problem)
type ValidateOptions struct {
    KnownTransport func(id string) bool             // backend.KnownTransport
    ValidTransports func() []string                 // for the C005 message
    ReservedPorts   []int                           // extra reserved (e.g. 22 is always reserved)
}
func (c *Config) ApplyDefaults()                     // fills zero values (failover, tls, ui_mode, tuning, security, ladders.default…)
func NewHub(name, publicIP string, controlPort int) *Config
func NewNode(id, hubAddr, caFingerprint string) *Config
func (c *Config) ResolveLadder(t *Tunnel) ([]string, error) // profile or inline; filters rungs by tunnel protocol (§8: UDP-only default ladder)
func (c *Config) Tunnel(id string) (*Tunnel, bool)
func (c *Config) NodeByID(id string) (*Node, bool)
func (t *Tunnel) HasProto(p string) bool; func (t *Tunnel) ProbeTarget() (PortMap, bool) // first TCP port or probe_port
func CheckProbe(field, proto, probe string) error     // ports[].probe rule (auto|tcp|tls|http, auto for UDP; C013), shared with the UI and the Local API
func ValidID(s string) bool                          // [a-z0-9-]{2,32}
func Slugify(name string) string                     // derive an id from a name
func Migrate(raw map[string]any, from int) (map[string]any, error) // framework; v1 is current (C006/C019)
func Clone(c *Config) *Config                        // deep copy
```
Validation rules (§4): ids `[a-z0-9-]{2,32}` unique (C007/C002); `listen` 1–65535
(P010→C013), no two tunnels share `listen/proto` (C003), `listen` ≠
`control_port`, not in `30000-31999`, not 22 (C011); `target` valid host:port
(C004), default `127.0.0.1:<listen>`; ≥1 node (C008) that exists (C010);
non-empty ladder (C009) with known transports (C005) and known profile
(C012); ≤ 64 port maps, a range counting one map per port (C015); enums
(C013); role/sections (C016).
UDP-requiring rungs are *not* a config error (runtime skip).

### internal/state
```go
func Open(path string) (*Store, error)               // bbolt, 0600, timeout 1s; corrupt → X001 after trying <path>.bak
func (s *Store) Close() error
func (s *Store) Snapshot(path string) error          // consistent copy (used for <path>.bak hourly and backups)
func (s *Store) GetNode(id string) (NodeState, bool, error); PutNode(NodeState) error; ListNodes() ([]NodeState, error); DeleteNode(id string) error
func (s *Store) GetTunnel(id string) (TunnelState, bool, error); PutTunnel(TunnelState) error; ListTunnels() ([]TunnelState, error); DeleteTunnel(id string) error // also its probes/metrics/ctlports
func (s *Store) AppendProbe(tunnel, node, transport string, p ProbeSample) error // keeps last 120
func (s *Store) Probes(tunnel, node, transport string) ([]ProbeSample, error)
func (s *Store) AppendEvent(e Event) (Event, error)  // assigns Seq + At(UTC if zero); ring of 5000
func (s *Store) Events(f EventFilter) ([]Event, error) // newest first
func (s *Store) PutMetrics(tunnel string, m Metrics) error; GetMetrics(tunnel string) (Metrics, bool, error)
func (s *Store) AllocCtlPort(key string, lo, hi int, busy func(port int) bool) (int, error) // stable per key; P020 when exhausted
func (s *Store) CtlPorts() (map[string]int, error); ReleaseCtlPorts(prefix string) error
func (s *Store) AllocNetIndex(tunnel string, max int) (int, error); ReleaseNetIndex(tunnel string) error
func (s *Store) GetMeta(key string, v any) (bool, error); PutMeta(key string, v any) error; DeleteMeta(key string) error
func (s *Store) ExportEvents(w io.Writer) error      // NDJSON (backup)
```

### internal/log
```go
type Options struct { Component string; File string; Debug bool; Stderr bool; MaxBytes int64; Keep int }
func New(opt Options) (*slog.Logger, io.Closer, error) // JSON handler, UTC RFC3339 "ts", rotation, redaction
func NewRotatingWriter(path string, maxBytes int64, keep int) (*RotatingWriter, error) // 20MB, 5 files, gzip
func RegisterSecret(v string)                          // exact values → "***" everywhere
func Redact(s string) string                           // registered secrets + patterns (token=, key=, password=, dey:// tokens, PEM private keys, bot tokens)
func RedactingWriter(w io.Writer) io.Writer            // for doctor bundles / streamed logs
// attr helpers: Tunnel(id), Node(id), Transport(id), Code(c), Err(err)
```

### internal/exec
```go
var Allowed = []string{"systemctl","nft","ss","ip","xray","rathole","ufw","firewall-cmd","iptables","systemd-sysusers","userdel","groupdel","haproxy"} // allowlist.go
type Runner interface { Run(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error) }
func NewRunner() Runner          // rejects names whose base is not allowed (X004); non-zero exit → X007 wrapping stderr
type Fake struct{...}            // records calls, scripted responses (for tests of other packages)
func LookPath(name string) (string, error)
```

### internal/systemd
```go
const HubUnit = "deyroute-hub.service"; NodeUnit = "deyroute-node.service"; TunTemplate = "deyroute-tun@.service"
func InstanceName(tunnel, node, transportID string) string // "main.de-1.backhaul-wssmux"
func CanaryInstance(tunnel string) string                  // "main.canary"
func UnitName(instance string) string                      // "deyroute-tun@main.de-1.backhaul-wssmux.service"
func Templates() map[string][]byte                         // embedded unit files (identical to deploy/systemd/*, test enforces)
func RenderDropIn(spec backend.UnitSpec, workDir, logFile string) []byte // golden-tested
type Manager struct { Runner exec.Runner; Root string /* "/" */ }
func (m *Manager) InstallTemplates(ctx) error; WriteDropIn(instance string, content []byte) error; RemoveInstance(ctx, instance) error
func (m *Manager) DaemonReload(ctx) error; Start/Stop/Restart/Enable/Disable(ctx, unit string) error; ResetFailed(ctx, unit) error
func (m *Manager) Show(ctx, unit string) (UnitState, error)  // ActiveState, SubState, MainPID, NRestarts, ActiveEnterTimestamp
func (m *Manager) ListInstances(ctx) ([]UnitState, error)    // deyroute-tun@*
func Notify(state string) error                               // sd_notify over $NOTIFY_SOCKET (READY=1, WATCHDOG=1, STOPPING=1)
func WatchdogInterval() time.Duration                         // from WATCHDOG_USEC / 2
func Available() bool; Version(ctx) (int, error)              // systemd ≥ 245
```

### internal/firewall
```go
type Kind string // "nftables","ufw","firewalld","iptables"
type Spec struct { ControlPort int; RestrictControl bool; NodeIPs4, NodeIPs6 []string; CtlLow, CtlHigh int;
                   ListenTCP, ListenUDP []int; NAT []backend.NATRule; Masquerade []string; IPv6 bool }
func Render(s Spec) string                        // full `table inet deyroute { … }` (golden); input chain priority -10 as §11
func Apply(ctx, r exec.Runner, s Spec) error       // nft -f - atomically: add table; delete table; table inet deyroute {…} → P019/X005
func Remove(ctx, r exec.Runner) error              // delete only table inet deyroute (idempotent)
func Show(ctx, r exec.Runner) (string, error)
func Detect(ctx, r exec.Runner) []Kind
func Blocks(ctx, r exec.Runner, port int, proto string) (blocked bool, by Kind, err error) // ufw/firewalld/iptables heuristics
func OpenCommand(k Kind, port int, proto string) []string // "ufw allow 443/tcp"; "firewall-cmd --permanent --add-port=443/tcp && firewall-cmd --reload"
func Check(ctx, r exec.Runner, port int, proto string) (Verdict, error) // first external firewall that blocks; Verdict.Command() is the line the owner confirms
func (v Verdict) Open(ctx, r exec.Runner) error    // runs the argv Check built (kind, nft table/chain, typed port/proto), never Commands
```
`api.Local.PortOpenFirewall` (hub) re-runs `Check`, refuses with `DEY-P032`
unless `Verdict.Command()` equals the command the owner confirmed, runs
`Verdict.Open` and checks again; the CLI (`port check --open`), Ports →
Check port and the Add tunnel wizard call it after a typed confirmation.

### internal/ports
```go
type Spec struct { Listen int; Proto string; Target string } // same fields as api.PortSpec
func ParseInput(s string) ([]Spec, error)      // "443", "443/udp", "443,2053", "2000-2010", "443:8443", "443/tcp,27015/udp", "443:8443/udp"; ≤64 → P016; P010/P017/C020
func Reserved(port, controlPort int) (bool, string) // 22, control port, 30000-31999
type BindResult struct { Free bool; Proto string; Addr string; PID int; Process string /* "nginx (pid 1234)" */ }
func CheckBind(port int, proto string) BindResult        // net.Listen on 0.0.0.0 and [::]; then /proc lookup
type ProcFS struct{ Root string }                        // /proc lookup via /proc/net/{tcp,tcp6,udp,udp6} + /proc/<pid>/fd, testable
func (p ProcFS) Owner(port int, proto string) (pid int, name string, addr string, err error)
func Suggest(n int, busy func(port int, proto string) bool, controlPort int) []int // CF list first (443,2053,2083,2087,2096,8443), then random 1024–65535
```

### internal/sysctl
```go
type KV struct{ Key, Value string }
func Profile(name string, ipForward bool) ([]KV, error) // off|balanced|aggressive (§12 exact values; aggressive: 64MB buffers + tcp_notsent_lowat)
type Manager struct { Root string /* "/" */ }
func (m Manager) Apply(profile string, ipForward bool) (applied []KV, warnings []string, err error) // backup once, write 99-deyroute.conf, write /proc/sys; BBR missing → warning + skip of tcp_congestion_control only (fq stays); aggressive below 4 GB RAM → warning
func (m Manager) Revert() error; Current() (string, error); BBRAvailable() bool
```

### internal/tlsutil
```go
type CA struct { Cert *x509.Certificate; Key ed25519.PrivateKey; CertPEM []byte }
func NewCA(cn string, now time.Time) (*CA, error); LoadCA(certPath, keyPath string) (*CA, error); (ca *CA) Save(certPath, keyPath string) error
func Fingerprint(der []byte) string             // "sha256:<64 hex>"
func (ca *CA) Fingerprint() string
func (ca *CA) IssueServer(cn string, ips []net.IP, dns []string, validity time.Duration) (certPEM, keyPEM []byte, err error) // hub control cert
func NewKeyAndCSR(cn string) (csrPEM, keyPEM []byte, err error)                             // Ed25519 node key
func (ca *CA) SignCSR(csrPEM []byte, cn string, validity time.Duration) ([]byte, error)      // client-auth node cert
func (ca *CA) IssueTunnel(tunnel string, ips []net.IP, dns []string, now time.Time) (certPEM, keyPEM []byte, err error) // ECDSA P-256, 3 years
func ParseCert(pemBytes []byte) (*x509.Certificate, error); CertSHA256Hex(pemBytes []byte) (string, error)
func NeedsRenewal(certPEM []byte, now time.Time, before time.Duration) bool
func ValidateCustom(certPath, keyPath string, now time.Time) error // T001/T002/T005
func EncodePKCS12(certPEM, keyPEM []byte, password string) ([]byte, error) // legacy SHA1/3DES PBE + SHA1 MAC
func WriteSecret(path string, data []byte) error                            // 0600 root, atomic
func CheckSecretPerms(dir string) []error                                   // S002
type ACMEOptions struct { Domain, Email, CacheDir string; Staging bool; HTTPPort int; CloudflareToken string }
func ObtainACME(ctx, o ACMEOptions) (certPEM, keyPEM []byte, err error)     // lego; T003/T004
```

### internal/health
```go
type Result struct { OK bool; RTT time.Duration; Err string }
func Path(ctx, addr string, kind string, timeout time.Duration) Result // kind auto|tcp|tls|http; auto = send TLS ClientHello, any reply/alert = OK, clean close / any byte = OK for non-TLS
func TCP(ctx, addr string, timeout time.Duration) Result
func TLS(ctx, addr, sni string, timeout time.Duration) Result
func HTTP(ctx, url string, timeout time.Duration) Result
func UDPEcho(ctx, addr string, tries int, timeout time.Duration) Result // nonce echo, 3 tries × 2s
func ServeUDPEcho(ctx, conn net.PacketConn) error
func ServeTCPEcho(ctx, ln net.Listener) error                          // canary / diag
type Pool struct{...}; func NewPool(size int) *Pool; (p *Pool) Go(ctx, func()) ; (p *Pool) Close()
type History struct{...}            // RTT samples with timestamps
func (h *History) Add(t time.Time, rtt time.Duration); Median(since time.Time) time.Duration
func (h *History) HighFor(now time.Time, factor float64, window, dur time.Duration) bool // RTT > 3×median(10m) for 60s
```

### internal/notify
```go
type Sender interface { Post(ctx, url, contentType string, body []byte) (status int, err error) } // direct HTTP or via node (http.post)
type Telegram struct{...}
func NewTelegram(token, chatID string, events []string, s Sender, clock func() time.Time) *Telegram
func (t *Telegram) Notify(ctx, e state.Event) error   // filters by alias set; 1 msg / 60s per (tunnel,type)
func (t *Telegram) Test(ctx) error
func Aliases() map[string][]string                    // down→tunnel_down, switch→switch_transport+switch_node, failback→failback+failback_failed, node_offline, …
func Format(e state.Event) string                     // one message text (no secrets)
```

### internal/install
```go
const MinisignPublicKey = "…"                     // release key (placeholder until owner provides; QUESTIONS.md)
type Source struct { Name, BaseURL string }        // mirror, release base, github
func Sources(flagMirror, envMirror, releaseBase string) []Source
type Fetcher interface { Fetch(ctx, url string, w io.Writer) error }
type HTTPFetcher struct{ Client *http.Client }     // honours https_proxy
func FetchVerified(ctx, f Fetcher, urls []string, sha256hex string, dst string) error // 3 tries/source with backoff; I004/I005/S001
func VerifyMinisign(msg, sig []byte, pubKey string) error                            // S001/I006
func ParseSHA256SUMS(data []byte) (map[string]string, error)
func Extract(archivePath, kind string, names []string, dstDir string) error          // tar.gz, zip, gz, raw
type Layout struct{ Root string }                  // /var/lib/deyroute/bin/<b>/<ver>/<bin> + .sha256
func (l Layout) InstallBackend(ctx, e backend.ManifestEntry, arch string, f Fetcher, mirror string) (binDir string, err error) // never overwrites; S006 when sha256 missing
func (l Layout) SelfUpdate(newBinary string) error / Rollback() error                // keeps deyroute.prev
func Backup(opts BackupOptions) (path string, err error); Restore(opts RestoreOptions) error // tar.gz + age passphrase
func AutoBackup(root string, keep int) (string, error)                               // backups/auto/, keep 20
```

### internal/backend/<name> (each)
- `init() { backend.Register(New()) }`; `Name()`, `Transports()` per §7 table
  (directions, protos, stealth, NeedsUDP/TLS), `Manifest()` =
  `backend.ManifestFor(name)`.
- `Render(in, side)` is **pure** (no I/O, deterministic ordering) and returns
  Files, UnitSpec (ExecStart absolute paths from `in.Paths`), Binds, NAT.
- `Validate(in)` returns DEY-B006/B010 for impossible combinations.
- `GenerateKeys` (pure Go: `crypto/ecdh` X25519, `crypto/rand`) when needed.
- Golden tests in `testdata/<transport>.<side>.golden` for every transport and
  both sides, with `-update`. Coverage ≥ 70 %.
- `docs/backends/<name>.md`: pinned version, README link, key differences
  between the spec sample and the pinned version's real keys.

### internal/failover
Pure, deterministic, clock-driven. See §9 of the spec. Public surface:
```go
type Clock interface { Now() time.Time; NewTimer(d time.Duration) Timer }
type Actions interface {
    Start(ctx, c state.Candidate) error          // server side first (Direction.ServerSide), then client side
    Stop(ctx, c state.Candidate) error
    ProbePath(ctx, c state.Candidate) health.Result       // hub → 127.0.0.1:<probe port> through the tunnel
    NodeService(ctx, node string) (up bool, known bool)    // node_service probe (probe.tcp target on node)
    ControlOnline(node string) (online bool, offlineFor time.Duration)
    Emit(e state.Event)
    Persist(ts state.TunnelState) error
}
type Tunnel struct { ID string; Nodes []string; Ladder []string; Policy string; Settings config.Failover; ... }
func NewEngine(t Tunnel, a Actions, clk Clock, st state.TunnelState) *Engine
func (e *Engine) Run(ctx) error                    // actor loop
func (e *Engine) Pause()/Resume()/Reset()/SwitchTransport(id string) error/SwitchNode(id string) error
func (e *Engine) TestLadder(ctx, onRung func(api.RungResult)) error
func (e *Engine) State() state.TunnelState
func NextCandidate(policy string, nodes, ladder []string, cur state.Candidate, tried map[string]bool, excluded func(state.Candidate) bool) (state.Candidate, bool)
```

## 5. Runtime layout

- Hub process (`deyroute daemon hub`, root): config + state, Control API server,
  node registry, renderer/unit manager, health+failover engines (one actor per
  tunnel), event bus + notifier, Local API on `/run/deyroute/daemon.sock` (0600).
- Node process (`deyroute daemon node`, root): one outbound HTTP/2 stream to the
  hub with 1→30 s exponential reconnect; executes commands; heartbeat every 5 s;
  Local API socket for `status`, `logs`, `node set-hub`.
- Units: `deyroute-tun@<tunnel>.<node>.<backend>-<transport>.service` on both
  sides; drop-in `/etc/systemd/system/deyroute-tun@<inst>.service.d/10-deyroute.conf`
  holds ExecStart/WorkingDirectory/log path; rendered files under
  `/etc/deyroute/backends/<backend>/<tunnel>/<node>/<transport>/` (0750 dir,
  0640 files, `root:deyroute`) — they contain the tunnel token and the copies of
  TLS material the backend process must read (QUESTIONS.md C.16).
- Secrets: `/etc/deyroute/secrets/` (0700): `ca.key ca.crt hub.key hub.crt
  node.key node.crt join-tokens.json backend-tokens/<tunnel>.token
  backend-keys/<tunnel>/<backend>.json tls/<tunnel>/{cert,key}.pem`.
- Control ports: per (tunnel, node, transport) from `30000-31999`, stable,
  stored in state (`AllocCtlPort("<tunnel>/<node>/<transport>")`).

## 6. Local API transport

HTTP/1.1 over the unix socket. `POST /v1/rpc/<Method>` with a JSON array of
arguments (excluding ctx and callbacks); response `{"result": …}` or
`{"error": ErrorDTO}`. Methods with a `progress`/`emit` callback respond with
`Content-Type: application/x-ndjson`: zero or more `{"step": Step}` /
`{"log": LogLine}` lines then one `{"result": …}` or `{"error": …}` line.
Client: `api.Dial(socketPath) (Local, error)`; if the socket is missing it
returns `DEY-X003` (Fix: `systemctl start deyroute-hub`).

## 7. Daemon design (wave 2)

The daemon tree is split into sub-packages so that several people can work in
parallel without breaking each other's builds:

| Package | Owns |
| --- | --- |
| `internal/api` (transport files) | `rpc_gen.go` (generated Local client+server), `localserver.go`, `localclient.go` (`Dial`), `controlserver.go`, `controlclient.go`, `session.go` |
| `internal/daemon/secrets` | per-tunnel tokens, backend keys (KeyGenerator), tunnel TLS (auto/acme/custom) incl. copies + PKCS#12, join tokens (single use, TTL, per-IP limit), telegram and Cloudflare (ACME DNS-01) token files |
| `internal/daemon/render` | the **planner**: desired warm set per tunnel (every rung × every node × side), RenderInput construction (paths, ctl ports, secrets, decoy, net index), hub-side file/drop-in writer, node-side `backend.render` payloads, NAT/firewall spec assembly, canary inputs |
| `internal/daemon/hub` | hub service: control API handlers (join, sessions, uploads, assets), node registry + heartbeats, event bus (state + events.log + notifier), tunnel controller (install → render → warm units → firewall → failover engine with real Actions), reconcile loop, UDP/skip re-checks, TLS renewal, metrics, updates, Local API implementation |
| `internal/daemon/node` | node agent: connect/reconnect, hello/heartbeat, command handlers, local API (status/logs/set-hub) |
| `internal/daemon/setup` | local operations run by the CLI (and TUI) *without* the daemon: setup wizard steps (hub), join (node), uninstall, backup/restore wrappers, public-IP detection, service install |
| `internal/doctor` | collection helpers, 15 rules, redacted bundle writer |

### 7.1 Paths used by the planner
- Hub side config dir: `/etc/deyroute/backends/<backend>/<tunnel>/<node>/<transportName>/`;
  node side the same path on the node. Canary: `/etc/deyroute/backends/<backend>/<tunnel>/canary/`.
- Binaries: `/var/lib/deyroute/bin/<backend>/<version>/<binary>` (`install.Layout`).
- Log file: `/var/log/deyroute/tunnels/<tunnel>.log` (both sides).
- Files the backend reads are written 0640 `root:deyroute` (dirs 0750). TLS copies
  in the config dir: `tls-cert.pem`, `tls-key.pem`, `ca.crt`, `tls.p12`.
- Instance: `systemd.InstanceName(tunnel, node, transportID)`; canary
  `systemd.CanaryInstance(tunnel)`.

### 7.2 Control channel
- TLS listener with `tlsutil.ServerTLSConfig` (ALPN `deyroute/1`), each accepted
  conn served by `http2.Server.ServeConn`. Clients use `http2.Transport` with a
  custom `DialTLSContext` (ALPN `deyroute/1`). `/v1/join` is the only path that
  accepts a connection without a client certificate; every other path requires
  a verified client cert whose CN is a known node id.
- Session: `POST /v1/stream` full duplex NDJSON. Hub side `Session.Call(ctx,
  name, args, result) error` with per-command ids and timeouts (`DEY-N005`),
  `Cancel`, `Ping` (RTT), streamed `LogChunk`s. Offline after 15 s without a
  heartbeat.
- Join firewall window: while at least one unexpired join token exists the
  hub renders the firewall with `RestrictControl=false` (control port open to
  all, protected by the per-IP 5-failures/hour limit); afterwards back to
  `@nodes` only (QUESTIONS.md C.23).
- `fetch.proxy`: hub registers an upload id, sends the command; the node
  downloads, verifies sha256 and POSTs `/v1/upload/<id>`; the hub streams it
  into the waiting writer. The hub's `install.Fetcher` chain is
  `[via-node, direct]` once a node has joined (spec §5).
- Telegram: `notify.ChainSender{HTTPSender, via-node http.post}`.

### 7.3 Tunnel lifecycle on the hub
1. `TunnelAdd`: validate request → check every listen port (`ports.Checker`,
   reserved, conflicts with other tunnels) → write config (auto backup first)
   → steps: install backend on hub (all rungs' backends), install on node(s),
   render (all rungs, all nodes, both sides), firewall, start (failover engine
   INIT→STARTING), probe → result "Tunnel main is UP via backhaul/wssmux (41ms)".
   Each step reports `api.Step` progress; a failing step returns its DEY error.
2. Rungs whose `Validate` fails or whose UDP probe has not passed (failed,
   or not run yet: section 7.6) are recorded in `TunnelState.Skipped` with
   `RecheckAt = now+30m` and event `rung_skipped` (yellow); a periodic job
   re-tests and clears them (`rung_restored`).
3. Failover `Actions` (hub implementation): `Start` starts the server side
   unit first (`Direction.ServerSide()`), then the other side; applies the
   candidate's NAT (hub firewall + node `firewall.apply`); for `awg` runs the
   documented post-start configuration. `Stop` stops client side then server
   side and removes the candidate NAT. `ProbePath` = `health.Path` to
   `127.0.0.1:<probe port>` with the port's probe kind; `AcceptCleanClose` only
   when the node reported the target as non-TLS. `NodeService` = cached
   `probe.tcp` of the probe target on the node (refreshed every probe interval).
4. Only the active candidate's units run; every other rung is warm (files +
   drop-in present, unit stopped). NAT rules exist only for the active
   candidate.
5. Hub restart: `failover.Reconcile` with the units actually active on the hub
   (`systemd.Manager.ListInstances`) and on nodes (heartbeat `Units`).
6. `TunnelDelete`: stop engine, stop and remove every instance on hub and
   nodes, remove config dirs, NAT/firewall entries, secrets of the tunnel,
   state (`DeleteTunnel`), config entry.

### 7.4 Local operations (`internal/daemon/setup`)
```go
type HubOptions struct { Name string; ControlPort int; PublicIP string; SysctlProfile string; ApplySysctl bool; Yes bool; Root string; Runner exec.Runner; Progress func(api.Step) }
func SetupHub(ctx, o HubOptions) (*HubResult, error)   // I013 when configured; steps: detect IP, CA + hub cert, control port, config.yaml, firewall, sysctl, units, enable+start deyroute-hub
type JoinOptions struct { Link string; Name string; Root string; Runner exec.Runner; HTTPClient *http.Client; Progress func(api.Step) }
func ParseJoinLink(link string) (JoinLink, error)       // dey://TOKEN@HUB_IP:PORT#sha256:… → N006
func Join(ctx, o JoinOptions) (*JoinResult, error)       // CSR, POST /v1/join with pinned CA, write secrets/config, sysctl, units, start deyroute-node
func DetectPublicIP(ctx, r exec.Runner) (string, error)  // ip route get 1.1.1.1 → src
func Uninstall(ctx, o UninstallOptions) error            // stop/disable units, nft table, sysctl revert, paths (keep backups?), binary
func JoinCommand(installerURL, link string) string       // bash <(curl -fsSL <installer>) join '<link>'
```

### 7.5 Integration notes from wave 1 (binding for wave 2)
- `/etc/deyroute` is `0710 root:deyroute` and `/var/lib/deyroute` `0750 root:deyroute`
  (installer); never chmod them back to 0700 — backends running as `deyroute`
  must traverse them. `secrets/` stays `0700 root`, `config.yaml` `0600`.
- Backend version directories are path-escaped (`app/v2.12.3` →
  `app%2Fv2.12.3`): always build paths with `install.Layout.BinDir/BinaryPath`.
- NAT-based transports (a candidate whose hub-side `Rendered.NAT` is not
  empty, i.e. WireGuard/AWG): output-chain DNAT deliberately excludes
  loopback, so the path probe must dial the hub's public IP (or the tunnel
  address) instead of `127.0.0.1`.
- Control port access (spec §3/§11 vs S27 node IP change and join): the hub
  renders `RestrictControl=true` plus a rate-limited accept for unknown
  sources (`ct state new limit rate 6/minute`) on the control port only; mTLS
  and the join limiter protect it. While a join token is valid the port is
  open without limit. Extend `firewall.Spec` with `UnknownControlRate string`
  ("" = drop) — QUESTIONS.md C.23.
- `log.RedactingWriter` returns `*RedactWriter`; call `Close`/`Flush` at the
  end of in-memory streams. `state.Open` reports recovered corruption via
  `Store.Recovered()`; log it (X001) and continue.
- Do not log non-secret values under attribute names `key`/`*_key`/`token`
  (they are masked); use e.g. `ctl_key`.
- `health.Path` auto mode: `ClosedNoData` is a failure unless
  `PathOptions.AcceptCleanClose` (only when the node-side probe of the
  target showed a non-TLS service that closes immediately).
- `tlsutil.ServerTLSConfig`/`ClientTLSConfig` enforce ALPN `deyroute/1` via
  `VerifyConnection`; serve HTTP/2 with `http2.Server.ServeConn` on the
  accepted `*tls.Conn` (do not rely on "h2"). Nodes use
  `ClientTLSConfig(ca, cert, key, "")` (chain + ServerAuth, no hostname) so a
  hub move keeps working.
- `config.ResolveLadder(t, supports)` takes a protocol-support callback:
  pass `func(id, proto) bool { _, tr, err := backend.Lookup(id); return err == nil && tr.Supports(proto) }`.
- Structs built in code must use `config.NewTunnel`/`DefaultFailover` to get
  boolean defaults (`enabled`, `failback`).
