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

	// ---- optimize
	OptimizeStatus(ctx context.Context) (OptimizeStatus, error)
	OptimizeApply(ctx context.Context, profile string) (OptimizeStatus, error)
	OptimizeRevert(ctx context.Context) (OptimizeStatus, error)

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
}

// NodeSelf is the node part of Status (on a node).
type NodeSelf struct {
	ID          string    `json:"id"`
	HubAddr     string    `json:"hub_addr"`
	Connected   bool      `json:"connected"`
	LastContact time.Time `json:"last_contact"`
	HubVersion  string    `json:"hub_version,omitempty"`
	Compatible  bool      `json:"compatible"`
	Units       []string  `json:"units,omitempty"`
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
	Warnings        []string     `json:"warnings,omitempty"`
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
}

// PortSpec is one parsed port entry (see ports.ParseInput).
type PortSpec struct {
	Listen int    `json:"listen"`
	Proto  string `json:"proto"`
	Target string `json:"target,omitempty"` // default 127.0.0.1:<listen>
	Probe  string `json:"probe,omitempty"`
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
	// 3. reachable from node
	Node          string `json:"node,omitempty"`
	NodeReachable *bool  `json:"node_reachable,omitempty"`
	NodeRTTms     int    `json:"node_rtt_ms,omitempty"`
	// 4. reachable via tunnel
	Tunnel         string `json:"tunnel,omitempty"`
	TunnelOK       *bool  `json:"tunnel_ok,omitempty"`
	TunnelRTTms    int    `json:"tunnel_rtt_ms,omitempty"`
	Note           string `json:"note,omitempty"` // "does not measure filtering inside Iran"
	SuggestedPorts []int  `json:"suggested_ports,omitempty"`
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

// OptimizeStatus describes sysctl/BBR state.
type OptimizeStatus struct {
	Profile      string            `json:"profile"`
	BBRAvailable bool              `json:"bbr_available"`
	BBRActive    bool              `json:"bbr_active"`
	Applied      map[string]string `json:"applied,omitempty"`
	Warnings     []string          `json:"warnings,omitempty"`
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
	Domain   *string  `json:"domain,omitempty"`
}
