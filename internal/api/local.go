// Package api holds both wire protocols of DEYROUTE:
//
//   - the Local API: JSON over HTTP on the unix socket /run/deyroute/daemon.sock,
//     used by the CLI and the TUI (this file defines its contract, Local);
//   - the Control API: mTLS HTTP/2 between hub and nodes (control.go).
//
// The TUI and CLI never contain management logic: every action is exactly one
// Local method, implemented by internal/daemon.
package api

import (
	"context"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// JSONSchemaVersion is emitted as "schema" in every --json document
// (docs/cli-json.md).
const JSONSchemaVersion = 1

// Local is the Local API. Methods that are meaningless for the daemon's role
// return DEY-X009. Long operations report progress through the optional
// progress callback (may be nil); the HTTP transport streams it as NDJSON.
type Local interface {
	// ---- general
	Status(ctx context.Context) (Status, error)
	Events(ctx context.Context, q EventQuery) ([]state.Event, error)
	// Logs streams log lines until ctx is cancelled (follow) or the backlog ends.
	Logs(ctx context.Context, q LogQuery, emit func(LogLine) error) error
	// ConfigApply re-reads config.yaml, validates it, takes an automatic
	// backup, re-renders and reconciles units/firewall (section 4).
	ConfigApply(ctx context.Context, progress func(Step)) (ApplyResult, error)
	SettingsSet(ctx context.Context, req SettingsRequest) error

	// ---- nodes (hub)
	NodeJoinCommand(ctx context.Context, ttl time.Duration) (JoinCommand, error)
	NodeList(ctx context.Context) ([]NodeInfo, error)
	NodeRename(ctx context.Context, id, name string) error
	NodeRemove(ctx context.Context, id string) error
	NodeTest(ctx context.Context, id string) (NodeTestResult, error)
	HubAnnounceMove(ctx context.Context, newAddr string) (AnnounceResult, error)
	// ---- node role
	NodeSetHub(ctx context.Context, addr string) error

	// ---- tunnels (hub)
	TunnelAdd(ctx context.Context, req TunnelAddRequest, progress func(Step)) (TunnelInfo, error)
	TunnelList(ctx context.Context) ([]TunnelInfo, error)
	TunnelShow(ctx context.Context, id string) (TunnelDetail, error)
	TunnelEdit(ctx context.Context, id string, req TunnelEditRequest, progress func(Step)) (TunnelInfo, error)
	TunnelSetEnabled(ctx context.Context, id string, enabled bool) error
	TunnelRestart(ctx context.Context, id string) error
	TunnelDelete(ctx context.Context, id string, progress func(Step)) error
	TunnelSwitch(ctx context.Context, id string, req SwitchRequest) error
	TunnelReset(ctx context.Context, id string) error
	TunnelPause(ctx context.Context, id string) error
	TunnelResume(ctx context.Context, id string) error
	TunnelTestLadder(ctx context.Context, id string, progress func(Step)) ([]RungResult, error)
	TunnelBackupAdd(ctx context.Context, id, node string, progress func(Step)) error
	TunnelBackupRemove(ctx context.Context, id, node string) error

	// ---- ports (hub)
	PortAdd(ctx context.Context, tunnel string, specs []PortSpec, progress func(Step)) (TunnelInfo, error)
	PortRemove(ctx context.Context, tunnel string, listen int, proto string) (TunnelInfo, error)
	PortCheck(ctx context.Context, req PortCheckRequest) (PortCheckResult, error)
	// PortOpenFirewall opens a port in the external firewall that blocks it
	// (ufw, firewalld, iptables or another nftables table), only with the
	// exact command the owner confirmed (section 10).
	PortOpenFirewall(ctx context.Context, req PortOpenRequest) (PortOpenResult, error)
	PortSuggest(ctx context.Context, count int) ([]int, error)

	// ---- ladders and transports
	LadderList(ctx context.Context) ([]Ladder, error)
	LadderSave(ctx context.Context, name string, rungs []string, create bool) error
	LadderDelete(ctx context.Context, name string) error
	TransportList(ctx context.Context) ([]TransportInfo, error)

	// ---- diagnostics
	DiagSpeed(ctx context.Context, tunnel string, seconds int, progress func(Step)) (SpeedResult, error)
	DiagProbe(ctx context.Context, tunnel string, allPorts bool) ([]ProbeReport, error)
	DoctorCollect(ctx context.Context, node string) (DoctorData, error)

	// Traffic returns the traffic and load series of tunnels, nodes and the
	// hub (`deyroute stats`, the Traffic screen; docs/cli-json.md).
	Traffic(ctx context.Context, q TrafficQuery) (TrafficReport, error)

	// ---- optimize
	OptimizeStatus(ctx context.Context) (OptimizeStatus, error)
	OptimizeApply(ctx context.Context, profile string) (OptimizeStatus, error)
	OptimizeRevert(ctx context.Context) (OptimizeStatus, error)
	// OptimizeAutoPlan computes the automatic tuning plan of the hub and of
	// every online node without changing anything (`optimize auto
	// --dry-run`, and the list shown before the confirmation).
	OptimizeAutoPlan(ctx context.Context, opts AutoOptions) (TunePlanReport, error)
	// OptimizeAutoApply applies the plan the owner confirmed: it refuses
	// with DEY-X065 when the plan's hash is no longer req.Hash.
	OptimizeAutoApply(ctx context.Context, req AutoApply, progress func(Step)) (TunePlanReport, error)
	// OptimizeCheck compares the tuned values with the live kernel on the
	// hub and every online node (`optimize check`).
	OptimizeCheck(ctx context.Context) (TuneCheck, error)

	// ---- security
	SecurityRotateTokens(ctx context.Context, tunnel string, progress func(Step)) error
	SecurityRotateCA(ctx context.Context, progress func(Step)) (RotateCAResult, error)
	SecurityTLSShow(ctx context.Context, tunnel string) ([]CertInfo, error)
	SecurityTLSRenew(ctx context.Context, tunnel string) ([]CertInfo, error)
	SecurityFirewall(ctx context.Context, action string) (FirewallInfo, error) // show|apply|disable
	SecurityAudit(ctx context.Context) (AuditReport, error)

	// ---- notifications
	NotifyTelegramSet(ctx context.Context, tokenFile, chatID string, events []string) error
	NotifyTelegramTest(ctx context.Context) error
	NotifyTelegramOff(ctx context.Context) error

	// ---- update
	UpdateCheck(ctx context.Context) (UpdateInfo, error)
	UpdateApply(ctx context.Context, version string, progress func(Step)) (UpdateInfo, error)
	UpdateRollback(ctx context.Context) (UpdateInfo, error)
	// UpdateAuto shows the automatic update (mode "") or turns it on|off.
	UpdateAuto(ctx context.Context, mode string) (AutoUpdateInfo, error)
	UpdateBackends(ctx context.Context, name string, progress func(Step)) ([]BackendUpdate, error)
	UpdateManifest(ctx context.Context) (ManifestInfo, error)

	// ---- uninstall helpers (the CLI runs the local part itself)
	UninstallNodes(ctx context.Context) ([]string, error)
	StopAll(ctx context.Context) error
}

// ErrorDTO is the wire form of *errors.Error.
type ErrorDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Why     string `json:"why"`
	Fix     string `json:"fix"`
	Log     string `json:"log,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// ToDTO converts any error to its wire form (plain errors become DEY-X000).
func ToDTO(err error) *ErrorDTO {
	if err == nil {
		return nil
	}
	e := deyerr.As(err)
	return &ErrorDTO{Code: string(e.Code), Message: e.Message(), Why: e.Why(), Fix: e.Fix(), Log: e.LogPath, Detail: e.Detail}
}

// Err rebuilds a *errors.Error from the wire form.
func (d *ErrorDTO) Err() *deyerr.Error {
	if d == nil {
		return nil
	}
	return &deyerr.Error{Code: deyerr.Code(d.Code), MsgOverride: d.Message, WhyOverride: d.Why, FixOverride: d.Fix, LogPath: d.Log, Detail: d.Detail}
}

// Step is one line of a progress screen, e.g. "install backend on hub ✔".
type Step struct {
	ID     string    `json:"id"`     // stable id, e.g. "install_hub"
	Title  string    `json:"title"`  // human text
	Status string    `json:"status"` // running|ok|failed|skipped|warn
	Detail string    `json:"detail,omitempty"`
	Error  *ErrorDTO `json:"error,omitempty"`
}

// Step statuses.
const (
	StepRunning = "running"
	StepOK      = "ok"
	StepFailed  = "failed"
	StepSkipped = "skipped"
	StepWarn    = "warn"
)

// Status is the dashboard (section 6) and `deyroute status --json`.
type Status struct {
	Schema      int           `json:"schema"`
	Role        string        `json:"role"`
	Version     string        `json:"version"`
	GeneratedAt time.Time     `json:"generated_at"`
	Hub         *HubStatus    `json:"hub,omitempty"`
	NodeSelf    *NodeSelf     `json:"node,omitempty"`
	Tunnels     []TunnelInfo  `json:"tunnels"`
	Nodes       []NodeInfo    `json:"nodes"`
	Events      []state.Event `json:"events"` // newest first, at most 10
	Warnings    []Warning     `json:"warnings,omitempty"`
}

// HubStatus is the hub part of Status.
type HubStatus struct {
	Name        string `json:"name"`
	PublicIP    string `json:"public_ip"`
	ControlPort int    `json:"control_port"`
	Domain      string `json:"domain,omitempty"`
	UIMode      string `json:"ui_mode"`
	Language    string `json:"language"`
	Firewall    string `json:"firewall"` // managed|suggest-only
	// ACMEChallenge is how tls.mode acme proves hub.domain: http-01 (port
	// 80), dns-01 (Cloudflare token file set) or none (HTTP-01 disabled and
	// no token).
	ACMEChallenge string `json:"acme_challenge"`
	ACMEEmail     string `json:"acme_email,omitempty"`
	// Telegram is hub.notify.telegram (Notifications menu); the bot token
	// itself is never sent, only the file it is read from.
	Telegram TelegramStatus `json:"telegram"`
	// Front is the CDN front listener (hub.front); absent when front mode
	// was never configured.
	Front *FrontStatus `json:"front,omitempty"`
}

// FrontStatus is the front part of HubStatus. The path secret is never
// part of it.
type FrontStatus struct {
	Enabled   bool   `json:"enabled"`
	Domain    string `json:"domain,omitempty"`
	Port      int    `json:"port,omitempty"`
	Listening bool   `json:"listening"` // the listener is bound (false while disabled or after DEY-X053)
	CFOnly    bool   `json:"cf_only"`   // the firewall opens the port to Cloudflare ranges only
	TLS       string `json:"tls"`       // auto|custom|off
}

// TelegramStatus is the Telegram part of HubStatus.
type TelegramStatus struct {
	Enabled   bool     `json:"enabled"`
	ChatID    string   `json:"chat_id,omitempty"`
	TokenFile string   `json:"token_file,omitempty"`
	Events    []string `json:"events,omitempty"`
}

// HubStatus.ACMEChallenge values.
const (
	ACMEHTTP01 = "http-01"
	ACMEDNS01  = "dns-01"
	ACMENone   = "none"
)

// NodeSelf is the node part of Status (on a node).
type NodeSelf struct {
	ID          string    `json:"id"`
	HubAddr     string    `json:"hub_addr"`
	Connected   bool      `json:"connected"`
	LastContact time.Time `json:"last_contact"`
	HubVersion  string    `json:"hub_version,omitempty"`
	Compatible  bool      `json:"compatible"`
	Units       []string  `json:"units,omitempty"`
	// Front is true when the node reaches the hub through the CDN front
	// (HubAddr is then the front domain and port).
	Front bool `json:"front,omitempty"`
}

// Warning is a yellow dashboard line.
type Warning struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Tunnel  string `json:"tunnel,omitempty"`
	Node    string `json:"node,omitempty"`
}

// PortMapDTO is one port map.
type PortMapDTO struct {
	Listen int    `json:"listen"`
	Proto  string `json:"proto"`
	Target string `json:"target"`
	Probe  string `json:"probe,omitempty"`
}

// TunnelInfo is one dashboard row.
type TunnelInfo struct {
	ID              string       `json:"id"`
	Name            string       `json:"name"`
	Enabled         bool         `json:"enabled"`
	State           string       `json:"state"` // state.State*
	ActiveNode      string       `json:"active_node"`
	ActiveNodeName  string       `json:"active_node_name"`
	ActiveTransport string       `json:"active_transport"`
	RTTms           int          `json:"rtt_ms"`
	UpSince         time.Time    `json:"up_since"`
	Ports           []PortMapDTO `json:"ports"`
	Nodes           []string     `json:"nodes"` // ordered: primary first
	LadderName      string       `json:"ladder_name,omitempty"`
	Ladder          []string     `json:"ladder"` // resolved rungs
	Policy          string       `json:"policy"`
	Paused          bool         `json:"paused"`
	ServiceDown     bool         `json:"service_down"`
	ClientIP        string       `json:"client_ip"` // preserved|masked for the active transport
	// ProxyProtocol is advanced.proxy_protocol: transports that can keep the
	// client IP (direct/haproxy) keep it only with it.
	ProxyProtocol bool     `json:"proxy_protocol"`
	Warnings      []string `json:"warnings,omitempty"`
	// Traffic is the tunnel's current rate and today's volume, from the
	// hub's memory (never state.db); absent while monitoring is off or the
	// hub has no sample yet.
	Traffic *TrafficNow `json:"traffic,omitempty"`
}

// RungStatus is one ladder rung on one node in TunnelDetail.
type RungStatus struct {
	Node        string    `json:"node"`
	Transport   string    `json:"transport"`
	Warm        bool      `json:"warm"`
	Active      bool      `json:"active"`
	ControlPort int       `json:"control_port"`
	Skipped     string    `json:"skipped,omitempty"` // reason when skipped (yellow)
	Quarantine  time.Time `json:"quarantine_until,omitempty"`
	Unit        string    `json:"unit"`
	UnitState   string    `json:"unit_state,omitempty"`
}

// TunnelDetail is `deyroute tunnel show`.
type TunnelDetail struct {
	TunnelInfo
	Failover      FailoverSettings    `json:"failover"`
	TLSMode       string              `json:"tls_mode"`
	ProbePort     int                 `json:"probe_port"`
	Rungs         []RungStatus        `json:"rungs"`
	Probes        []state.ProbeSample `json:"probes"` // last 120 of the active candidate
	Metrics       *state.Metrics      `json:"metrics,omitempty"`
	FailbackDelay time.Duration       `json:"failback_delay"`
	Events        []state.Event       `json:"events"`
}

// FailoverSettings mirrors config.Failover for the UI.
type FailoverSettings struct {
	Policy             string `json:"policy"`
	ProbeIntervalS     int    `json:"probe_interval_s"`
	ProbeTimeoutS      int    `json:"probe_timeout_s"`
	FailThreshold      int    `json:"fail_threshold"`
	RecoverThreshold   int    `json:"recover_threshold"`
	Failback           bool   `json:"failback"`
	FailbackAfterS     int    `json:"failback_after_s"`
	MaxSwitchesPerHour int    `json:"max_switches_per_hour"`
	QuarantineS        int    `json:"quarantine_s"`
}

// NodeInfo is one node row.
type NodeInfo struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	PublicIP      string    `json:"public_ip"`
	Online        bool      `json:"online"`
	ControlRTTms  int       `json:"control_rtt_ms"`
	Version       string    `json:"version"`
	Compatible    bool      `json:"compatible"`
	CPUPercent    float64   `json:"cpu_percent"`
	RAMBytes      uint64    `json:"ram_bytes"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	Country       string    `json:"country,omitempty"`
	UDPOK         *bool     `json:"udp_ok,omitempty"`
	Tags          []string  `json:"tags,omitempty"`
	Fingerprint   string    `json:"cert_fingerprint"`
	Tunnels       []string  `json:"tunnels,omitempty"`
	// Route is how the node reaches the hub: "front" through the CDN front,
	// absent for a direct node. Via is the transport of its current control
	// stream ("front", or absent for direct TCP or when it is offline).
	Route string `json:"route,omitempty"`
	Via   string `json:"via,omitempty"`
	// LastError is the last error the node agent reported in its
	// heartbeat (section 3: "DEY-B003 …", redacted); "" when none.
	LastError string `json:"last_error,omitempty"`
}

// JoinCommand is the one-line join command (section 3).
type JoinCommand struct {
	Command   string    `json:"command"` // bash <(curl -fsSL <installer>) join 'dey://…'
	Link      string    `json:"link"`    // dey://TOKEN@HUB_IP:PORT#sha256:…
	ExpiresAt time.Time `json:"expires_at"`
}

// NodeTestResult is `deyroute node test`.
type NodeTestResult struct {
	Node         string            `json:"node"`
	Online       bool              `json:"online"`
	ControlRTTms int               `json:"control_rtt_ms"`
	UDPOK        bool              `json:"udp_ok"`
	UDPRTTms     int               `json:"udp_rtt_ms"`
	SysInfo      map[string]string `json:"sysinfo"`
}

// AnnounceResult reports which nodes accepted a hub move.
type AnnounceResult struct {
	Accepted []string `json:"accepted"`
	Offline  []string `json:"offline"`
	// Front lists the nodes that connect through the CDN front: they are not
	// told the new address ("front: unchanged"), because a direct address
	// would cut them off. Repoint the front domain if the hub moved.
	Front []string `json:"front,omitempty"`
}

// PortSpec is one parsed port entry (see ports.ParseInput).
type PortSpec struct {
	Listen int    `json:"listen"`
	Proto  string `json:"proto"`
	Target string `json:"target,omitempty"` // default 127.0.0.1:<listen>
	Probe  string `json:"probe,omitempty"`  // auto|tcp|tls|http; default auto
}

// TunnelAddRequest is `deyroute tunnel add` / the Add tunnel wizard.
type TunnelAddRequest struct {
	ID      string     `json:"id,omitempty"` // derived from name when empty
	Name    string     `json:"name,omitempty"`
	Node    string     `json:"node"`
	Backups []string   `json:"backups,omitempty"`
	Ports   []PortSpec `json:"ports"`
	Ladder  string     `json:"ladder,omitempty"`   // profile name; default by protocol
	Rungs   []string   `json:"rungs,omitempty"`    // inline ladder (Advanced)
	Policy  string     `json:"policy,omitempty"`   // default transport_then_node
	TLSMode string     `json:"tls_mode,omitempty"` // default auto
	// Failover thresholds (Advanced); zero = default.
	Failover *FailoverSettings `json:"failover,omitempty"`
	// FixedTransport limits the tunnel to one transport (phase 2 CLI flag).
	FixedTransport string `json:"fixed_transport,omitempty"`
}

// TunnelEditRequest changes mutable fields; nil/empty = unchanged.
type TunnelEditRequest struct {
	Name      *string           `json:"name,omitempty"`
	Ladder    *string           `json:"ladder,omitempty"`
	Rungs     []string          `json:"rungs,omitempty"`
	Nodes     []string          `json:"nodes,omitempty"` // full ordered list
	Policy    *string           `json:"policy,omitempty"`
	ProbePort *int              `json:"probe_port,omitempty"`
	TLSMode   *string           `json:"tls_mode,omitempty"`
	TLSCert   *string           `json:"tls_cert,omitempty"`
	TLSKey    *string           `json:"tls_key,omitempty"`
	Failover  *FailoverSettings `json:"failover,omitempty"`
	// PortProbes sets the probe kind of existing port maps (Advanced,
	// section 9): Listen and Proto name the map, Probe is the new kind
	// (auto|tcp|tls|http; UDP maps stay auto). Target is ignored.
	PortProbes []PortSpec `json:"port_probes,omitempty"`
}

// SwitchRequest is `deyroute tunnel switch` (exactly one of the fields).
type SwitchRequest struct {
	Transport string `json:"transport,omitempty"`
	Node      string `json:"node,omitempty"`
}

// RungResult is one row of test-ladder.
type RungResult struct {
	Node      string    `json:"node"`
	Transport string    `json:"transport"`
	OK        bool      `json:"ok"`
	RTTms     int       `json:"rtt_ms"`
	Error     *ErrorDTO `json:"error,omitempty"`
	Skipped   string    `json:"skipped,omitempty"`
}

// PortCheckRequest is `deyroute port check 443[/tcp] [--node id]`.
type PortCheckRequest struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	Node  string `json:"node,omitempty"` // default: first online node
}

// PortCheckResult is the 4-line port check (sections 6 and 10).
type PortCheckResult struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	// 1. local bind
	BindFree    bool   `json:"bind_free"`
	BindProcess string `json:"bind_process,omitempty"` // "nginx (pid 1234)"
	BindAddr    string `json:"bind_addr,omitempty"`
	BindByDey   bool   `json:"bind_by_deyroute"`
	// 2. firewall
	FirewallOpen    bool   `json:"firewall_open"`
	FirewallName    string `json:"firewall_name"`
	FirewallCommand string `json:"firewall_command,omitempty"` // suggestion when closed
	// 3. reachable from node; NodeError is DEY-P014 when the node could not
	// connect (shown in the three-line format)
	Node          string    `json:"node,omitempty"`
	NodeReachable *bool     `json:"node_reachable,omitempty"`
	NodeRTTms     int       `json:"node_rtt_ms,omitempty"`
	NodeError     *ErrorDTO `json:"node_error,omitempty"`
	// 4. reachable via tunnel
	Tunnel         string `json:"tunnel,omitempty"`
	TunnelOK       *bool  `json:"tunnel_ok,omitempty"`
	TunnelRTTms    int    `json:"tunnel_rtt_ms,omitempty"`
	Note           string `json:"note,omitempty"` // "does not measure filtering inside Iran"
	SuggestedPorts []int  `json:"suggested_ports,omitempty"`
}

// PortOpenRequest is `deyroute port check 443[/tcp] --open` and "Open it in
// the firewall" in the menu. Command is the command the owner confirmed
// (PortCheckResult.FirewallCommand). The hub never runs it as text: it
// checks the firewall again, builds the command from the firewall it finds
// and the typed Port and Proto, and refuses with DEY-P032 when that is not
// Command.
type PortOpenRequest struct {
	Port    int    `json:"port"`
	Proto   string `json:"proto"`
	Command string `json:"command"`
}

// PortOpenResult is what PortOpenFirewall ran and the firewall check that
// followed (stage 2 of the port check).
type PortOpenResult struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	// Ran is the command that ran and By the firewall it changed; both are
	// empty when no external firewall blocked the port any more.
	Ran string `json:"ran,omitempty"`
	By  string `json:"by,omitempty"`
	// The firewall afterwards, as in PortCheckResult: another firewall that
	// still blocks the port comes with its own command (a new confirmation).
	FirewallOpen    bool   `json:"firewall_open"`
	FirewallName    string `json:"firewall_name"`
	FirewallCommand string `json:"firewall_command,omitempty"`
	Note            string `json:"note,omitempty"`
}

// Ladder is a named ladder profile.
type Ladder struct {
	Name    string   `json:"name"`
	Rungs   []string `json:"rungs"`
	Builtin bool     `json:"builtin"`
	UsedBy  []string `json:"used_by,omitempty"`
}

// TransportInfo describes one transport for UI hints.
type TransportInfo struct {
	ID                string   `json:"id"`
	Backend           string   `json:"backend"`
	Direction         string   `json:"direction"`
	Protos            []string `json:"protos"`
	NeedsUDP          bool     `json:"needs_udp"`
	NeedsTLS          bool     `json:"needs_tls"`
	Stealth           int      `json:"stealth"`
	ClientIPPreserved bool     `json:"client_ip_preserved"`
	Optional          bool     `json:"optional"`
	Version           string   `json:"version"`
}

// SpeedResult is `deyroute diag speed`.
type SpeedResult struct {
	Tunnel       string  `json:"tunnel"`
	Transport    string  `json:"transport"`
	Seconds      float64 `json:"seconds"`
	DownloadMbps float64 `json:"download_mbps"`
	UploadMbps   float64 `json:"upload_mbps"`
	RTTms        int     `json:"rtt_ms"`
}

// ProbeReport is one probe line of `deyroute diag probe`.
type ProbeReport struct {
	Tunnel string `json:"tunnel"`
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
	Kind   string `json:"kind"`
	OK     bool   `json:"ok"`
	RTTms  int    `json:"rtt_ms"`
	Error  string `json:"error,omitempty"`
}

// DoctorData is everything the daemon contributes to a doctor bundle; the
// CLI adds local files, runs the rules and writes the tar.gz (redacted).
type DoctorData struct {
	Role     string            `json:"role"`
	Sections map[string]string `json:"sections"` // name → text (already redacted)
	Findings []DoctorFinding   `json:"findings"`
}

// DoctorFinding is the result of one doctor rule.
type DoctorFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // ok|info|warn|error
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

// OptimizeStatus describes sysctl/BBR state. MemBytes is the hub's RAM
// (MemTotal, 0 = unknown) and Recommended the profile for it: aggressive
// from 4 GB, balanced below (section 12).
type OptimizeStatus struct {
	Profile      string            `json:"profile"`
	BBRAvailable bool              `json:"bbr_available"`
	BBRActive    bool              `json:"bbr_active"`
	Applied      map[string]string `json:"applied,omitempty"`
	Warnings     []string          `json:"warnings,omitempty"`
	MemBytes     uint64            `json:"mem_bytes,omitempty"`
	Recommended  string            `json:"recommended,omitempty"`
	// Facts are the hub's measured host facts (automatic tuning); absent
	// when they could not be read.
	Facts *TuneFacts `json:"facts,omitempty"`
	// Nodes is the tuning state of every node, as the hub last saw it.
	Nodes []NodeTuneStatus `json:"nodes,omitempty"`
}

// NodeTuneStatus is one node row of OptimizeStatus.
type NodeTuneStatus struct {
	Node   string `json:"node"`
	Online bool   `json:"online"`
	// Profile and Hash are what the node reported in its last Hello
	// (Hello.TuneProfile, Hello.TuneHash); "" when it never reported one.
	Profile string `json:"profile,omitempty"`
	Hash    string `json:"hash,omitempty"`
	// Pending is true while the node still has to apply the hub's tuning
	// (it was offline or runs other inputs than the hub's).
	Pending bool `json:"pending,omitempty"`
	// AutoCapable is false for an agent too old for the auto profile (it
	// gets balanced, with a warning).
	AutoCapable bool `json:"auto_capable"`
}

// ---------------------------------------------------------------- traffic

// Traffic targets, kinds, periods and limits (TrafficQuery, TrafficSeries).
// A target is "tunnel:<id>", "node:<id>" or "hub"; a bare id names a tunnel
// (so a tunnel whose id is "hub" is "tunnel:hub").
const (
	TrafficTargetHub    = "hub"
	TrafficTunnelPrefix = "tunnel:"
	TrafficNodePrefix   = "node:"

	TrafficKindTunnel = "tunnel"
	TrafficKindNode   = "node"
	TrafficKindHub    = "hub"

	TrafficPeriod1h  = "1h"
	TrafficPeriod24h = "24h"
	TrafficPeriod7d  = "7d"
	TrafficPeriod30d = "30d"

	// DefaultTrafficPoints is TrafficQuery.MaxPoints when it is 0 or
	// negative; MaxTrafficPoints caps it.
	DefaultTrafficPoints = 120
	MaxTrafficPoints     = 1440
)

// TrafficPeriods lists the periods TrafficQuery accepts, shortest first.
var TrafficPeriods = []string{TrafficPeriod1h, TrafficPeriod24h, TrafficPeriod7d, TrafficPeriod30d}

// TrafficQuery selects the series of Traffic.
type TrafficQuery struct {
	// Targets are "tunnel:<id>" (or a bare tunnel id), "node:<id>" and
	// "hub"; empty = every tunnel. An unknown target or period is DEY-C027.
	Targets []string `json:"targets,omitempty"`
	// Period is 1h, 24h, 7d or 30d; "" = 1h.
	Period string `json:"period,omitempty"`
	// MaxPoints bounds the points of each series; 0 or less =
	// DefaultTrafficPoints, more than MaxTrafficPoints = MaxTrafficPoints.
	// The hub downsamples by summing bytes and taking the maximum of conns,
	// CPU and RAM; a bucket with any gap is a gap.
	MaxPoints int `json:"max_points,omitempty"`
}

// TrafficReport is the answer of Traffic (`deyroute stats --json`).
type TrafficReport struct {
	GeneratedAt time.Time `json:"generated_at"`
	Period      string    `json:"period"`
	// Available is false when the hub cannot count bytes (no nft, no
	// nf_tables, or monitoring.enabled is false); Reason then says why
	// (DEY-X061). Host series and connection counts may still be present.
	Available bool      `json:"available"`
	Reason    *ErrorDTO `json:"reason,omitempty"`
	// Timezone is the hub-local zone that "today", the quota period and the
	// month use (an IANA name, or "UTC+03:30" when the hub has no name for
	// it); clients label ticks in their own zone.
	Timezone string          `json:"timezone"`
	Series   []TrafficSeries `json:"series"`
}

// TrafficSeries is one tunnel, node or the hub.
type TrafficSeries struct {
	// ID is the tunnel or node id, or "hub"; Kind is tunnel|node|hub.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	// Available and Reason are per series: a tunnel series without byte
	// counts says why here, while the hub and node series keep working.
	Available bool      `json:"available"`
	Reason    *ErrorDTO `json:"reason,omitempty"`
	// StepS is the width of one point in seconds (after downsampling).
	StepS  int            `json:"step_s"`
	Points []TrafficPoint `json:"points"`
	// Totals are the byte totals of a tunnel series (absent for hosts).
	Totals *TrafficTotals `json:"totals,omitempty"`
}

// TrafficPoint is one bucket of a series. A tunnel point carries bytes and
// connections, a node or hub point CPU and RAM; a missing number is 0.
// Direction is seen from the users: in = upload from users to the hub,
// out = download from the hub to users. Bytes are L3 bytes on the hub's
// user-facing listen ports.
type TrafficPoint struct {
	At       time.Time `json:"at"` // bucket start, UTC
	BytesIn  uint64    `json:"bytes_in,omitempty"`
	BytesOut uint64    `json:"bytes_out,omitempty"`
	// Conns is the maximum of established TCP connections in the bucket;
	// absent when unknown (UDP has no connection state, or a gap).
	Conns      *int    `json:"conns,omitempty"`
	CPUPercent float64 `json:"cpu_percent,omitempty"`
	RAMBytes   uint64  `json:"ram_bytes,omitempty"`
	// Gap marks a bucket without a valid sample (hub stopped, tunnel not
	// up, counter reset, clock jump): draw it as missing, not as zero.
	Gap bool `json:"gap,omitempty"`
}

// TrafficTotals are the byte totals of one tunnel.
type TrafficTotals struct {
	// TodayStart is 00:00 hub-local of today (in UTC).
	TodayStart time.Time `json:"today_start"`
	TodayIn    uint64    `json:"today_in"`
	TodayOut   uint64    `json:"today_out"`
	// Days30* cover the last 30 days, rolling.
	Days30In  uint64 `json:"days30_in"`
	Days30Out uint64 `json:"days30_out"`
	// PeriodStart is the start of the current quota period
	// (monitoring.quota_reset_day, 00:00 hub-local, in UTC); Period* are
	// the bytes since then.
	PeriodStart time.Time `json:"period_start"`
	PeriodIn    uint64    `json:"period_in"`
	PeriodOut   uint64    `json:"period_out"`
	// QuotaBytes is advanced.monthly_quota_gib in bytes; 0 = no quota.
	// The quota counts in+out user-side bytes; the provider usually bills
	// the hub's NIC, which also carries the tunnel leg (about twice as much).
	QuotaBytes uint64 `json:"quota_bytes,omitempty"`
}

// TrafficNow is TunnelInfo.Traffic: the current rate and today's volume.
type TrafficNow struct {
	At        time.Time `json:"at"`
	Available bool      `json:"available"`
	// Rates are bits per second over the last sample interval.
	RateInBitS  uint64 `json:"rate_in_bit_s"`
	RateOutBitS uint64 `json:"rate_out_bit_s"`
	TodayIn     uint64 `json:"today_in"`
	TodayOut    uint64 `json:"today_out"`
}

// ---------------------------------------------------------------- tuning

// Change kinds and effects of the automatic tuning plan (TuneChange).
const (
	TuneKindSysctl   = "sysctl"
	TuneKindSysfs    = "sysfs"
	TuneKindModules  = "modules"
	TuneKindDropin   = "dropin"
	TuneKindBackend  = "backend"
	TuneKindFirewall = "firewall"

	TuneEffectNow             = "now"
	TuneEffectNextStart       = "next-start"
	TuneEffectReboot          = "reboot"
	TuneEffectRestartsTunnels = "restarts-tunnels"
)

// AutoOptions are the options of OptimizeAutoPlan. Backends includes the
// backend tier items, which restart the active rung of the tunnels they
// touch (users reconnect).
type AutoOptions struct {
	Backends bool `json:"backends,omitempty"`
}

// AutoApply is the confirmed plan: Hash is TunePlanReport.Hash as shown.
type AutoApply struct {
	Hash     string `json:"hash"`
	Backends bool   `json:"backends,omitempty"`
}

// TunePlanReport is the automatic tuning plan of every host (`optimize
// auto --json`). Hash covers every host's changes; OptimizeAutoApply
// refuses another one (DEY-X065).
type TunePlanReport struct {
	Hash  string     `json:"hash"`
	Hosts []TuneHost `json:"hosts"`
	// Applied is true in the answer of OptimizeAutoApply.
	Applied  bool     `json:"applied,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// TuneHost is the plan of one host.
type TuneHost struct {
	// Host is "hub" or the node id; Role is hub|node.
	Host    string       `json:"host"`
	Role    string       `json:"role"`
	Facts   *TuneFacts   `json:"facts,omitempty"`
	Changes []TuneChange `json:"changes"`
	Skips   []TuneSkip   `json:"skips,omitempty"`
	// Hash is this host's part of the plan.
	Hash string `json:"hash,omitempty"`
	// Pending is true for an offline node: it applies when it reconnects
	// (its plan is reported then, as an event).
	Pending bool      `json:"pending,omitempty"`
	Error   *ErrorDTO `json:"error,omitempty"`
}

// TuneFacts are the measured facts a plan is computed from (internal/sysinfo).
type TuneFacts struct {
	MemBytes          uint64 `json:"mem_bytes"`
	MemAvailableBytes uint64 `json:"mem_available_bytes,omitempty"`
	CPUs              int    `json:"cpus"`
	Kernel            string `json:"kernel,omitempty"`
	// Virt is the container type ("openvz", "lxc", "docker", "container")
	// or "" on a VM or bare metal; kernel items are skipped in a container.
	Virt            string `json:"virt,omitempty"`
	BBRAvailable    bool   `json:"bbr_available"`
	FQAvailable     bool   `json:"fq_available"`
	Qdisc           string `json:"qdisc,omitempty"` // root qdisc of the default-route interface
	ConntrackLoaded bool   `json:"conntrack_loaded"`
	ConntrackMax    int    `json:"conntrack_max,omitempty"`
	ConntrackCount  int    `json:"conntrack_count,omitempty"`
	NIC             string `json:"nic,omitempty"` // default-route interface
	NICMTU          int    `json:"nic_mtu,omitempty"`
	NICSpeedMbps    int    `json:"nic_speed_mbps,omitempty"` // 0 = unknown
}

// TuneChange is one item of a plan. From is the live value ("" = absent),
// To the planned one; Reason and Effect are shown to the owner.
type TuneChange struct {
	Kind      string `json:"kind"` // sysctl|sysfs|modules|dropin|backend|firewall
	Key       string `json:"key"`
	From      string `json:"from"`
	To        string `json:"to"`
	Reason    string `json:"reason"`
	Effect    string `json:"effect"` // now|next-start|reboot|restarts-tunnels
	RaiseOnly bool   `json:"raise_only,omitempty"`
}

// TuneSkip is an item the plan leaves out and why (Code is a DEY code when
// one applies, e.g. DEY-X064 in a container).
type TuneSkip struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
	Code   string `json:"code,omitempty"`
}

// TuneCheck is `optimize check`: drift and findings per host. Clean is true
// when no host has drift or a finding.
type TuneCheck struct {
	Clean bool            `json:"clean"`
	Hosts []TuneHostCheck `json:"hosts"`
}

// TuneHostCheck is the check of one host.
type TuneHostCheck struct {
	Host     string        `json:"host"`
	Role     string        `json:"role"`
	Profile  string        `json:"profile"`
	Drift    []TuneDrift   `json:"drift,omitempty"`
	Findings []TuneFinding `json:"findings,omitempty"`
	// Error is set when the host could not be checked (offline node).
	Error *ErrorDTO `json:"error,omitempty"`
}

// TuneDrift is a tuned key whose live value is not what deyroute wrote.
// OverriddenBy names the file that sets it later (sysctl.d order), "" when
// the value was changed at runtime.
type TuneDrift struct {
	Key          string `json:"key"`
	Want         string `json:"want"`
	Live         string `json:"live"`
	OverriddenBy string `json:"overridden_by,omitempty"`
}

// TuneFinding is one other result of the check (conntrack fill, nofile
// margin, BBR or qdisc not active, a file changed after the apply).
type TuneFinding struct {
	Check    string `json:"check"`
	Severity string `json:"severity"` // info|warn|error
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

// RotateCAResult lists nodes re-issued and nodes that must re-join.
type RotateCAResult struct {
	Reissued []string `json:"reissued"`
	Offline  []string `json:"offline"`
}

// CertInfo describes one certificate.
type CertInfo struct {
	Tunnel      string    `json:"tunnel,omitempty"`
	Kind        string    `json:"kind"` // ca|hub|node|tunnel
	Mode        string    `json:"mode,omitempty"`
	Subject     string    `json:"subject"`
	SANs        []string  `json:"sans,omitempty"`
	NotAfter    time.Time `json:"not_after"`
	DaysLeft    int       `json:"days_left"`
	Fingerprint string    `json:"fingerprint"`
	Warning     string    `json:"warning,omitempty"`
}

// FirewallInfo is `deyroute security firewall show`.
type FirewallInfo struct {
	Managed   bool     `json:"managed"`
	Detected  []string `json:"detected"` // nftables|ufw|firewalld|iptables
	Ruleset   string   `json:"ruleset"`  // nft list table inet deyroute
	Suggested []string `json:"suggested,omitempty"`
}

// AuditReport is `deyroute security audit`.
type AuditReport struct {
	Items []AuditItem `json:"items"`
	Clean bool        `json:"clean"`
}

// AuditItem is one audit line.
type AuditItem struct {
	Check    string `json:"check"`
	Severity string `json:"severity"` // ok|warn|error
	Message  string `json:"message"`
}

// UpdateInfo describes deyroute release state.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Changelog string `json:"changelog,omitempty"`
	Previous  string `json:"previous,omitempty"`
}

// AutoUpdateInfo is the automatic update of the hub (`update auto`): once a
// day at Hour (server time) the newest release that has been out for at
// least MinAgeHours is installed; when the tunnels do not come back it is
// rolled back and skipped.
type AutoUpdateInfo struct {
	Enabled     bool      `json:"enabled"`
	Hour        int       `json:"hour"`
	MinAgeHours int       `json:"min_age_hours"`
	Current     string    `json:"current"`
	Next        string    `json:"next,omitempty"`    // release waiting for its turn
	NextAt      time.Time `json:"next_at,omitzero"`  // earliest automatic install of Next
	Pending     string    `json:"pending,omitempty"` // installed, being verified
	Last        string    `json:"last,omitempty"`    // outcome of the last automatic update
	LastAt      time.Time `json:"last_at,omitzero"`
	Skipped     []string  `json:"skipped,omitempty"` // rolled back; never installed automatically
}

// BackendUpdate is one backend's update outcome.
type BackendUpdate struct {
	Backend string    `json:"backend"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Status  string    `json:"status"` // updated|rolled_back|unchanged|failed
	Error   *ErrorDTO `json:"error,omitempty"`
}

// ManifestInfo describes the active backend manifest.
type ManifestInfo struct {
	Source   string            `json:"source"` // embedded|/etc/deyroute/backends.yaml
	Versions map[string]string `json:"versions"`
}

// EventQuery filters Events.
type EventQuery struct {
	Tunnel string    `json:"tunnel,omitempty"`
	Since  time.Time `json:"since,omitempty"`
	Limit  int       `json:"limit,omitempty"`
}

// LogQuery selects a log stream: target is a tunnel id, "hub" or "node".
type LogQuery struct {
	Target string    `json:"target"`
	Follow bool      `json:"follow"`
	Since  time.Time `json:"since,omitempty"`
	Lines  int       `json:"lines,omitempty"`
}

// LogLine is one streamed log line; Source is "hub" or "node".
type LogLine struct {
	Source string `json:"source"`
	Line   string `json:"line"`
}

// ApplyResult summarizes a config apply.
type ApplyResult struct {
	Backup   string   `json:"backup"`
	Changed  []string `json:"changed"`
	Warnings []string `json:"warnings,omitempty"`
}

// SettingsRequest changes UI settings; empty fields are unchanged.
type SettingsRequest struct {
	UIMode   string   `json:"ui_mode,omitempty"`
	Language string   `json:"language,omitempty"`
	Decoys   []string `json:"decoys,omitempty"`
	// Domain is hub.domain (TLS SANs, tls.mode acme); "" removes it.
	Domain *string `json:"domain,omitempty"`
	// ACMEEmail is hub.acme.email (the ACME account contact); "" removes it.
	ACMEEmail *string `json:"acme_email,omitempty"`
	// CloudflareTokenFile is the absolute path of a file holding a
	// Cloudflare API token (Zone:DNS:Edit) for DNS-01. The hub copies the
	// token to /etc/deyroute/secrets/cloudflare.token (0600) and sets
	// hub.acme.cloudflare_token_file; the token itself never travels or
	// enters config.yaml. "" removes it (HTTP-01 on port 80 again).
	CloudflareTokenFile *string `json:"cloudflare_token_file,omitempty"`
}
