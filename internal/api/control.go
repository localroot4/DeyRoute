package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
)

// Control API (section 3). HTTPS with mTLS on hub.control_port, TLS 1.3,
// ALPN "deyroute/1", HTTP/2. Only the hub listens; nodes always dial out.
//
//	POST /v1/join                   no client cert; JoinRequest → JoinResponse
//	POST /v1/stream                 client cert; full-duplex NDJSON:
//	                                request body = NodeMessage lines,
//	                                response body = HubMessage lines
//	POST /v1/upload/{id}            client cert; raw body for a pending
//	                                command (fetch.proxy payload, doctor bundle)
//	GET  /v1/assets/deyroute/{arch}   client cert; the hub's own deyroute binary
//	                                for arch (nodes always update from the hub)
const (
	ALPN              = "deyroute/1"
	PathJoin          = "/v1/join"
	PathStream        = "/v1/stream"
	PathUploadPrefix  = "/v1/upload/"
	PathAssetsPrefix  = "/v1/assets/deyroute/"
	HeartbeatInterval = 5 * time.Second
	// A node is offline after this long without a heartbeat.
	OfflineAfter = 15 * time.Second
	// JoinTokenTTL is the default join token lifetime (section 11).
	JoinTokenTTL = 15 * time.Minute
	// MinJoinTTL and MaxJoinTTL bound the lifetime an owner may ask for
	// (`deyroute node join-command --ttl`).
	MinJoinTTL = time.Minute
	MaxJoinTTL = 24 * time.Hour
	// NodeCertValidity is 10 years (section 3).
	NodeCertValidity = 10 * 365 * 24 * time.Hour
	// Reconnect backoff bounds (section 3).
	ReconnectMin = 1 * time.Second
	ReconnectMax = 30 * time.Second
)

// JoinRequest is sent by a node with the one-time token.
type JoinRequest struct {
	Token    string `json:"token"`
	NodeID   string `json:"node_id,omitempty"` // requested id (from --name); hub derives one when empty
	Name     string `json:"name,omitempty"`
	CSRPEM   string `json:"csr_pem"`
	Version  string `json:"version"`
	Arch     string `json:"arch"`
	OS       string `json:"os"`
	Hostname string `json:"hostname"`
}

// JoinResponse returns the signed node certificate and the hub CA.
type JoinResponse struct {
	NodeID     string `json:"node_id"`
	CertPEM    string `json:"cert_pem"`
	CAPEM      string `json:"ca_pem"`
	HubName    string `json:"hub_name"`
	HubVersion string `json:"hub_version"`
	HubAddr    string `json:"hub_addr"`
	PublicIP   string `json:"public_ip"` // node public IP as seen by the hub
}

// Message types on the stream.
const (
	MsgHello     = "hello"
	MsgHeartbeat = "heartbeat"
	MsgCommand   = "command"
	MsgResult    = "result"
	MsgLog       = "log"    // streamed log line for logs.follow
	MsgPing      = "ping"   // hub → node keepalive / RTT measurement
	MsgPong      = "pong"   // node → hub
	MsgCancel    = "cancel" // hub → node: cancel a running command
)

// NodeMessage is one NDJSON line from node to hub.
type NodeMessage struct {
	Type      string     `json:"type"`
	Hello     *Hello     `json:"hello,omitempty"`
	Heartbeat *Heartbeat `json:"heartbeat,omitempty"`
	Result    *Result    `json:"result,omitempty"`
	Log       *LogChunk  `json:"log,omitempty"`
	PingID    string     `json:"ping_id,omitempty"`
}

// HubMessage is one NDJSON line from hub to node.
type HubMessage struct {
	Type    string   `json:"type"`
	Hello   *Hello   `json:"hello,omitempty"`
	Command *Command `json:"command,omitempty"`
	PingID  string   `json:"ping_id,omitempty"`
	Cancel  string   `json:"cancel,omitempty"` // command id
}

// Hello is exchanged first in both directions.
type Hello struct {
	NodeID     string `json:"node_id,omitempty"`
	Version    string `json:"version"`
	Arch       string `json:"arch,omitempty"`
	OS         string `json:"os,omitempty"`
	Kernel     string `json:"kernel,omitempty"`
	CPUs       int    `json:"cpus,omitempty"` // runtime.NumCPU of the node (Waterwall workers)
	Compatible bool   `json:"compatible"`     // hub → node: false = node must not run commands except self.update
	// MemTotal is the node's RAM in bytes and Virt its container type
	// ("" on a VM or bare metal), for the hub's plans and status.
	MemTotal uint64 `json:"mem_total,omitempty"`
	Virt     string `json:"virt,omitempty"`
	// TuneProfile is the sysctl profile the node last applied and TuneHash
	// the SysctlArgs.InputsHash of that apply ("" = none): the hub re-sends
	// its tuning only when its own inputs hash differs.
	TuneProfile string `json:"tune_profile,omitempty"`
	TuneHash    string `json:"tune_hash,omitempty"`
	// Features lists the optional commands this agent understands
	// (Feature*). The hub gates them on this list, not on the version: an
	// agent of the same major.minor may predate them.
	Features []string `json:"features,omitempty"`
}

// Optional agent features announced in Hello.Features.
const (
	// FeatureTuneAuto: the agent knows the sysctl profile "auto",
	// CmdTunePlan, CmdTuneCheck and the tuning fields of SysctlArgs and
	// SysctlResult.
	FeatureTuneAuto = "tune-auto"
)

// HasFeature reports whether the Hello announces feature f.
func (h *Hello) HasFeature(f string) bool {
	return h != nil && slices.Contains(h.Features, f)
}

// Heartbeat is sent every 5 seconds (section 3). UnitsUnknown is set while
// the node has no current unit list (before its first listing after a
// start, or when listing failed): Units is then not authoritative and the
// hub keeps the last list it received.
type Heartbeat struct {
	At           time.Time         `json:"at"`
	CPUPercent   float64           `json:"cpu_percent"`
	RAMBytes     uint64            `json:"ram_bytes"`
	Units        map[string]string `json:"units"` // deyroute-tun@… → ActiveState
	UnitsUnknown bool              `json:"units_unknown,omitempty"`
	LastError    string            `json:"last_error,omitempty"`
}

// Command is hub → node.
type Command struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Args      json.RawMessage `json:"args,omitempty"`
	TimeoutMs int             `json:"timeout_ms"`
}

// Result is node → hub.
type Result struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Error *ErrorDTO       `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// LogChunk carries streamed lines for a logs.follow command.
type LogChunk struct {
	CommandID string   `json:"command_id"`
	Lines     []string `json:"lines"`
	EOF       bool     `json:"eof,omitempty"`
}

// Command names (section 3 plus lifecycle helpers).
const (
	CmdBackendInstall  = "backend.install"         // BackendInstallArgs → BackendInstallResult
	CmdBackendRender   = "backend.render"          // BackendRenderArgs → nil
	CmdBackendRemove   = "backend.remove"          // BackendRemoveArgs → nil
	CmdUnitStart       = "unit.start"              // UnitArgs → UnitStatus
	CmdUnitStop        = "unit.stop"               // UnitArgs → UnitStatus
	CmdUnitRestart     = "unit.restart"            // UnitArgs → UnitStatus
	CmdUnitStatus      = "unit.status"             // UnitArgs → UnitStatus
	CmdProbeTCP        = "probe.tcp"               // ProbeArgs → ProbeResultDTO
	CmdProbeTLS        = "probe.tls"               // ProbeArgs → ProbeResultDTO
	CmdProbeHTTP       = "probe.http"              // ProbeArgs → ProbeResultDTO
	CmdProbeUDPListen  = "probe.udp_listen"        // UDPListenArgs → nil (echo server for Seconds)
	CmdPortCheckRemote = "port.check_from_outside" // PortCheckArgs → ProbeResultDTO
	CmdFetchProxy      = "fetch.proxy"             // FetchArgs → FetchResult (payload via /v1/upload/{UploadID})
	CmdSysinfo         = "sysinfo"                 // nil → map[string]string
	CmdMetrics         = "metrics"                 // MetricsArgs → MetricsResult
	CmdLogsTail        = "logs.tail"               // LogsArgs → []string (or streamed with Follow)
	CmdDoctorCollect   = "doctor.collect"          // nil → DoctorData (sections)
	CmdSelfUpdate      = "self.update"             // SelfUpdateArgs → nil (downloads /v1/assets)
	CmdSetHub          = "set_hub"                 // SetHubArgs → nil
	CmdUninstall       = "uninstall"               // nil → nil
	CmdCertRenew       = "cert.renew"              // CertRenewArgs → CertRenewResult (rotate-ca)
	CmdCertInstall     = "cert.install"            // CertInstallArgs → nil (rotate-ca: signed cert + CA, then reconnect)
	CmdHTTPPost        = "http.post"               // HTTPPostArgs → HTTPPostResult (telegram via node)
	CmdEchoStart       = "echo.start"              // EchoArgs → EchoResult (canary loopback echo)
	CmdEchoStop        = "echo.stop"               // EchoArgs → nil (stops the canary loopback echo on Port)
	CmdNodeFirewall    = "firewall.apply"          // NodeFirewallArgs → nil (hysteria2 port hopping DNAT)
	CmdSysctlApply     = "sysctl.apply"            // SysctlArgs → SysctlResult (always a real apply)
	CmdTunePlan        = "tune.plan"               // SysctlArgs → SysctlResult (plan only, changes nothing; FeatureTuneAuto)
	CmdTuneCheck       = "tune.check"              // nil → TuneHostCheck (drift and findings; FeatureTuneAuto)
	CmdSpeedServe      = "speed.serve"             // SpeedServeArgs → nil (built-in generator for diag speed)
)

// BackendInstallArgs installs a pinned backend on the node.
type BackendInstallArgs struct {
	Entry backend.ManifestEntry `json:"entry"`
	Name  string                `json:"name"`
}

// BackendInstallResult reports installed paths.
type BackendInstallResult struct {
	BinDir string `json:"bin_dir"`
	Binary string `json:"binary"`
}

// BackendRenderArgs writes rendered files and the unit drop-in on the node.
type BackendRenderArgs struct {
	Instance  string            `json:"instance"` // <tunnel>.<node>.<backend>-<transport>
	Tunnel    string            `json:"tunnel"`
	ConfigDir string            `json:"config_dir"` // absolute
	Files     map[string][]byte `json:"files"`
	Unit      backend.UnitSpec  `json:"unit"`
	NAT       []backend.NATRule `json:"nat,omitempty"`
	IPForward bool              `json:"ip_forward,omitempty"`
}

// BackendRemoveArgs deletes a rendered instance (unit stopped and removed).
type BackendRemoveArgs struct {
	Instance  string `json:"instance"`
	ConfigDir string `json:"config_dir"`
}

// UnitArgs names one unit instance.
type UnitArgs struct {
	Instance string `json:"instance"`
}

// UnitStatus is the relevant part of `systemctl show`.
type UnitStatus struct {
	Unit        string    `json:"unit"`
	ActiveState string    `json:"active_state"`
	SubState    string    `json:"sub_state"`
	MainPID     int       `json:"main_pid"`
	NRestarts   int       `json:"n_restarts"`
	Since       time.Time `json:"since"`
	LogTail     []string  `json:"log_tail,omitempty"` // last 40 lines on failure
}

// ProbeArgs targets host:port.
type ProbeArgs struct {
	Target    string `json:"target"`
	TimeoutMs int    `json:"timeout_ms"`
	SNI       string `json:"sni,omitempty"`
	Path      string `json:"path,omitempty"`
}

// ProbeResultDTO is a probe outcome.
type ProbeResultDTO struct {
	OK    bool   `json:"ok"`
	RTTms int    `json:"rtt_ms"`
	Error string `json:"error,omitempty"`
}

// UDPListenArgs opens a temporary UDP echo on the node.
type UDPListenArgs struct {
	Port    int `json:"port"`
	Seconds int `json:"seconds"`
	// Stop closes the echo on Port at once (the hub's probe is done, so the
	// rung that uses the port can start).
	Stop bool `json:"stop,omitempty"`
}

// PortCheckArgs asks the node to connect to the hub's public ip:port.
type PortCheckArgs struct {
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	Proto     string `json:"proto"`
	TimeoutMs int    `json:"timeout_ms"`
}

// FetchArgs asks the node to download URL, verify sha256 and upload it.
type FetchArgs struct {
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	UploadID string `json:"upload_id"`
	MaxBytes int64  `json:"max_bytes"`
}

// FetchResult reports what was uploaded.
type FetchResult struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// MetricsArgs lists tunnel ports to count connections on.
type MetricsArgs struct {
	Ports []int `json:"ports"`
}

// MetricsResult is node-side traffic counters.
type MetricsResult struct {
	ActiveConns int    `json:"active_conns"`
	BytesIn     uint64 `json:"bytes_in"`
	BytesOut    uint64 `json:"bytes_out"`
}

// LogsArgs selects node logs.
type LogsArgs struct {
	Target string    `json:"target"` // tunnel id or "node"
	Lines  int       `json:"lines"`
	Since  time.Time `json:"since,omitempty"`
	Follow bool      `json:"follow"`
}

// SelfUpdateArgs tells the node to fetch the hub's binary.
type SelfUpdateArgs struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// SetHubArgs points a node to a moved hub.
type SetHubArgs struct {
	Addr string `json:"addr"`
}

// CertRenewArgs carries a new CSR request during rotate-ca.
type CertRenewArgs struct {
	NewCAPEM string `json:"new_ca_pem"`
}

// CertRenewResult returns the node's new CSR to be signed, or the installed cert.
type CertRenewResult struct {
	CSRPEM string `json:"csr_pem,omitempty"`
}

// CertInstallArgs completes cert.renew: CertPEM is the certificate the hub
// signed for the CSR of the last cert.renew, CAPEM the CA certificate(s) the
// node trusts from now on. The node installs both with the pending key and
// reconnects with the new credentials.
type CertInstallArgs struct {
	CertPEM string `json:"cert_pem"`
	CAPEM   string `json:"ca_pem"`
}

// HTTPPostArgs asks a node to POST on the hub's behalf (Telegram API from Iran).
type HTTPPostArgs struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Body        []byte `json:"body"`
}

// HTTPPostResult is the response status.
type HTTPPostResult struct {
	Status int    `json:"status"`
	Body   string `json:"body,omitempty"`
}

// EchoArgs starts the canary loopback echo on the node.
type EchoArgs struct {
	Port int `json:"port"` // 127.0.0.1:<port>
}

// EchoResult reports the bound port.
type EchoResult struct {
	Port int `json:"port"`
}

// NodeFirewallArgs is the node-side NAT for Hysteria2 port hopping.
type NodeFirewallArgs struct {
	NAT []backend.NATRule `json:"nat"`
}

// SysctlArgs applies a sysctl profile on the node. BBR is the hub's
// tuning.bbr (the hub config is the only source of truth, section 4); nil
// (a hub before it was sent) makes the node read its own config.
//
// For the profile "auto" (sent only to agents with FeatureTuneAuto) the node
// computes its own plan from its measured facts and these inputs; the same
// arguments with CmdTunePlan only compute it. There is deliberately no "dry
// run" field: an older agent would ignore it and apply.
type SysctlArgs struct {
	Profile   string `json:"profile"`
	IPForward bool   `json:"ip_forward"`
	BBR       *bool  `json:"bbr,omitempty"`
	// Reserved are the port entries ("30000-31999", "44433") the node adds
	// to net.ipv4.ip_local_reserved_ports (a set union with the live
	// value; a revert removes only these).
	Reserved []string `json:"reserved,omitempty"`
	// PlanVersion is the version of the plan algorithm the hub expects
	// (internal/sysctl); part of InputsHash.
	PlanVersion int `json:"plan_version,omitempty"`
}

// InputsHash is the hash of the tuning inputs the hub sends (profile, plan
// version, BBR, IP forwarding, reserved ports). The node stores it after an
// apply and reports it in Hello.TuneHash; the hub, which can compute it
// without the node's facts, re-sends its tuning only when it differs.
func (a SysctlArgs) InputsHash() string {
	bbr := "default"
	if a.BBR != nil {
		bbr = strconv.FormatBool(*a.BBR)
	}
	res := slices.Clone(a.Reserved)
	slices.Sort(res)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"v1", a.Profile, strconv.Itoa(a.PlanVersion), bbr, strconv.FormatBool(a.IPForward), strings.Join(res, ","),
	}, "\n")))
	return hex.EncodeToString(sum[:8])
}

// SysctlResult reports what the node skipped (no tcp_bbr, keys its kernel
// lacks, aggressive on less than 4 GB RAM) so the hub shows it to the owner.
// For the profile "auto" it also carries the node's facts and plan: the
// changes made (CmdSysctlApply) or planned (CmdTunePlan), the skipped items
// and the plan hash.
type SysctlResult struct {
	Warnings []string     `json:"warnings,omitempty"`
	Facts    *TuneFacts   `json:"facts,omitempty"`
	Changes  []TuneChange `json:"changes,omitempty"`
	Skips    []TuneSkip   `json:"skips,omitempty"`
	Hash     string       `json:"hash,omitempty"`
}

// SpeedServeArgs starts the built-in traffic generator behind a tunnel port.
type SpeedServeArgs struct {
	Port    int `json:"port"`
	Seconds int `json:"seconds"`
}
