// Package config is the single source of truth: /etc/deyroute/config.yaml.
// It defines the strict schema (unknown key = DEY-C001), validation with DEY-C
// codes, schema migrations and atomic writes. Runtime state never lives here
// (see internal/state).
package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Schema and file locations (section 2).
const (
	SchemaVersion   = 1
	DefaultPath     = "/etc/deyroute/config.yaml"
	EtcDir          = "/etc/deyroute"
	SecretsDir      = "/etc/deyroute/secrets" // #nosec G101 -- directory path, not a credential
	BackendsConfDir = "/etc/deyroute/backends"
	ManifestPath    = "/etc/deyroute/backends.yaml"
	LibDir          = "/var/lib/deyroute"
	BinDir          = "/var/lib/deyroute/bin"
	StatePath       = "/var/lib/deyroute/state.db"
	BackupDir       = "/var/lib/deyroute/backups"
	AutoBackupDir   = "/var/lib/deyroute/backups/auto"
	LogDir          = "/var/log/deyroute"
	TunnelLogDir    = "/var/log/deyroute/tunnels"
	RunDir          = "/run/deyroute"
	SocketPath      = "/run/deyroute/daemon.sock"
	BinaryPath      = "/usr/local/bin/deyroute"
	ShortLinkPath   = "/usr/local/bin/dey"
	PrevBinaryPath  = "/var/lib/deyroute/bin/deyroute.prev"
	SysctlConfPath  = "/etc/sysctl.d/99-deyroute.conf"
	SysctlBackup    = "/var/lib/deyroute/sysctl-before-deyroute.conf"
	SystemUser      = "deyroute"
)

// Defaults (sections 2, 4, 7, 9).
const (
	DefaultControlPort     = 44433
	CtlRangeLow            = 30000
	CtlRangeHigh           = 31999
	MaxPortMaps            = 64
	DefaultLadderName      = "default"
	DefaultUDPLadderName   = "udp-default"
	DefaultProbeIntervalS  = 5
	DefaultProbeTimeoutS   = 3
	DefaultFailThreshold   = 3
	DefaultRecoverThresh   = 6
	DefaultFailbackAfterS  = 300
	DefaultMaxSwitchesHour = 6
	DefaultQuarantineS     = 600
	DefaultConnectionPool  = 8
	DefaultHysteriaMbps    = 100
)

// Enumerations.
const (
	RoleHub  = "hub"
	RoleNode = "node"

	UIModeSimple   = "simple"
	UIModeAdvanced = "advanced"

	PolicyTransportOnly     = "transport_only"
	PolicyTransportThenNode = "transport_then_node"
	PolicyNodeOnly          = "node_only"

	TLSModeAuto   = "auto"
	TLSModeACME   = "acme"
	TLSModeCustom = "custom"

	ProtoTCP = "tcp"
	ProtoUDP = "udp"
	// ProtoMixedUDP stands for the UDP port maps of a tunnel that also has
	// TCP maps when the ladder is resolved: a transport may carry them
	// itself or through a UDP companion process (backhaul/wssmux plus
	// backhaul/udp). It never appears in config.yaml.
	ProtoMixedUDP = "udp-with-tcp"

	ProbeAuto = "auto"
	ProbeTCP  = "tcp"
	ProbeTLS  = "tls"
	ProbeHTTP = "http"

	SysctlOff        = "off"
	SysctlBalanced   = "balanced"
	SysctlAggressive = "aggressive"
	// SysctlAuto is the automatic profile: balanced plus values computed
	// from the measured host facts (`deyroute optimize auto`).
	SysctlAuto = "auto"

	// Backend tiers (tuning.backend_tier, nodes[].backend_tier): sticky
	// per host, set by `optimize auto --backends`, read by the renderer.
	BackendTierSmall  = "small"
	BackendTierMedium = "medium"
	BackendTierLarge  = "large"
)

// Monitoring and tuning limits.
const (
	// DefaultQuotaResetDay is monitoring.quota_reset_day when unset.
	DefaultQuotaResetDay = 1
	// MaxQuotaResetDay keeps the reset day in every month.
	MaxQuotaResetDay = 28
	// MaxMonthlyQuotaGiB bounds advanced.monthly_quota_gib (1 PiB).
	MaxMonthlyQuotaGiB = 1 << 20
	// MinWGMTU and MaxWGMTU bound tuning.wg_mtu (0 = the backend default).
	MinWGMTU = 1280
	MaxWGMTU = 1420
)

// DefaultLadder is the ordered default ladder of section 8. It ends with
// direct/native (section 8 text); see QUESTIONS.md about direct/haproxy in the
// section 4 example. Rung 4 is frp/tcp instead of frp/wss, which the pinned
// frp cannot serve without a separate TLS terminator (QUESTIONS.md C.31).
var DefaultLadder = []string{
	"backhaul/wssmux",
	"backhaul/tcpmux",
	"rathole/noise",
	"frp/tcp",
	"xray/reality",
	"hysteria2/udp",
	"waterwall/reverse-reality",
	"direct/native",
}

// DefaultUDPLadder is used for UDP-only tunnels (section 8).
var DefaultUDPLadder = []string{
	"backhaul/udp",
	"hysteria2/udp",
	"wireguard/kernel",
	"direct/native",
}

// DefaultTelegramEvents are the notification aliases enabled by default.
var DefaultTelegramEvents = []string{"down", "switch", "failback", "node_offline"}

// Config is the whole file. Hub configs use Hub/Nodes/Tunnels/Ladders;
// node configs use Node. Tuning and Security are valid for both roles.
type Config struct {
	SchemaVersion int                 `yaml:"schema_version"`
	Role          string              `yaml:"role"`
	Hub           *Hub                `yaml:"hub,omitempty"`
	Nodes         []Node              `yaml:"nodes,omitempty"`
	Tunnels       []Tunnel            `yaml:"tunnels,omitempty"`
	Ladders       map[string][]string `yaml:"ladders,omitempty"`
	Tuning        *Tuning             `yaml:"tuning,omitempty"`
	Security      *Security           `yaml:"security,omitempty"`
	// Monitoring is the hub's traffic monitoring; absent = the defaults
	// (on, quota period from day 1). Hub only.
	Monitoring *Monitoring `yaml:"monitoring,omitempty"`
	Node       *NodeSelf   `yaml:"node,omitempty"`
}

// Monitoring is the monitoring: section (hub only). It is never added by
// ApplyDefaults, so a config without it saves without it.
type Monitoring struct {
	// Enabled turns the traffic accounting table (inet deyroute_stats) and
	// the sampler on; nil = true.
	Enabled *bool `yaml:"enabled,omitempty"`
	// QuotaResetDay is the hub-local day of the month (1-28) on which the
	// quota period of advanced.monthly_quota_gib starts; 0 = 1.
	QuotaResetDay int `yaml:"quota_reset_day,omitempty"`
}

// Hub is the hub: section.
type Hub struct {
	Name        string `yaml:"name"`
	ControlPort int    `yaml:"control_port"`
	PublicIP    string `yaml:"public_ip"`
	Domain      string `yaml:"domain"`
	UIMode      string `yaml:"ui_mode"`
	Notify      Notify `yaml:"notify"`

	// Optional keys beyond the section 4 sample (see QUESTIONS.md C.14).
	PublicIP6   string   `yaml:"public_ip6,omitempty"`   // enables IPv6 listen (section 10)
	Language    string   `yaml:"language,omitempty"`     // "en" (default)
	DecoySNIs   []string `yaml:"decoy_snis,omitempty"`   // Reality/Waterwall decoys (section 7.4, 19)
	Mirror      string   `yaml:"mirror,omitempty"`       // release base override (DEYROUTE_MIRROR wins)
	UpdateCheck bool     `yaml:"update_check,omitempty"` // daily release check, default off (section 5)
	ACME        *ACME    `yaml:"acme,omitempty"`

	// Front is the CDN front listener (front mode); absent = off.
	Front HubFront `yaml:"front,omitempty"`
}

// ACME holds optional ACME settings (section 10, tls.mode=acme).
type ACME struct {
	Email               string `yaml:"email,omitempty"`
	CloudflareTokenFile string `yaml:"cloudflare_token_file,omitempty"` // enables DNS-01
	Staging             bool   `yaml:"staging,omitempty"`
	DisableHTTP01       bool   `yaml:"disable_http01,omitempty"`
	RenewBeforeDays     int    `yaml:"renew_before_days,omitempty"`
}

// Notify is hub.notify.
type Notify struct {
	Telegram Telegram `yaml:"telegram"`
}

// Telegram is hub.notify.telegram.
type Telegram struct {
	Enabled      bool     `yaml:"enabled"`
	BotTokenFile string   `yaml:"bot_token_file"`
	ChatID       string   `yaml:"chat_id"`
	Events       []string `yaml:"events"`
}

// Node is one entry of nodes: on the hub.
type Node struct {
	ID              string   `yaml:"id"`
	Name            string   `yaml:"name"`
	PublicIP        string   `yaml:"public_ip"`
	CertFingerprint string   `yaml:"cert_fingerprint"`
	Tags            []string `yaml:"tags,omitempty"`
	// Route is how the node reaches the hub: "" = direct, "front" = through
	// the CDN front (public_ip may then be empty, the hub cannot see it).
	Route string `yaml:"route,omitempty"`
	// BackendTier is the node's sticky backend tier (small|medium|large),
	// set by `optimize auto --backends`; "" = the renderer's defaults.
	BackendTier string `yaml:"backend_tier,omitempty"`
}

// PortMap maps listen on the hub to target on the node.
type PortMap struct {
	Listen int    `yaml:"listen"`
	Proto  string `yaml:"proto"`
	Target string `yaml:"target"`
	// Probe type for this port map (Advanced): auto|tcp|tls|http. Empty = auto.
	Probe string `yaml:"probe,omitempty"`
}

// Tunnel is one entry of tunnels:.
type Tunnel struct {
	ID       string    `yaml:"id"`
	Name     string    `yaml:"name"`
	Enabled  bool      `yaml:"enabled"`
	Nodes    []string  `yaml:"nodes"`
	Ports    []PortMap `yaml:"ports"`
	Ladder   LadderRef `yaml:"ladder"`
	Failover Failover  `yaml:"failover"`
	TLS      TLS       `yaml:"tls"`

	// Optional keys beyond the section 4 sample (see QUESTIONS.md C.14).
	ProbePort int       `yaml:"probe_port,omitempty"` // 0 = first TCP port (section 9)
	Advanced  *Advanced `yaml:"advanced,omitempty"`
}

// Advanced holds per-tunnel backend tuning exposed in Advanced mode.
type Advanced struct {
	ConnectionPool      int  `yaml:"connection_pool,omitempty"`       // backhaul/frp pool (default 8)
	HysteriaUpMbps      int  `yaml:"hysteria_up_mbps,omitempty"`      // default 100
	HysteriaDownMbps    int  `yaml:"hysteria_down_mbps,omitempty"`    // default 100
	HysteriaPortHopping bool `yaml:"hysteria_port_hopping,omitempty"` // node_ip:20000-20999
	ProxyProtocol       bool `yaml:"proxy_protocol,omitempty"`        // direct/haproxy send-proxy
	BackhaulWebPort     int  `yaml:"backhaul_web_port,omitempty"`     // refused: the pinned Backhaul cannot serve stats on 127.0.0.1 only
	// MonthlyQuotaGiB is the tunnel's traffic quota per quota period
	// (monitoring.quota_reset_day), in+out user-side bytes; 0 = none. The
	// hub warns at 80 % and 100 % (event traffic_quota).
	MonthlyQuotaGiB int `yaml:"monthly_quota_gib,omitempty"`
}

// Failover is tunnels[].failover.
type Failover struct {
	Policy             string `yaml:"policy"`
	ProbeIntervalS     int    `yaml:"probe_interval_s"`
	ProbeTimeoutS      int    `yaml:"probe_timeout_s"`
	FailThreshold      int    `yaml:"fail_threshold"`
	RecoverThreshold   int    `yaml:"recover_threshold"`
	Failback           bool   `yaml:"failback"`
	FailbackAfterS     int    `yaml:"failback_after_s"`
	MaxSwitchesPerHour int    `yaml:"max_switches_per_hour"`
	QuarantineS        int    `yaml:"quarantine_s,omitempty"`
}

// TLS is tunnels[].tls.
type TLS struct {
	Mode     string `yaml:"mode"`
	CertFile string `yaml:"cert_file,omitempty"` // custom mode
	KeyFile  string `yaml:"key_file,omitempty"`  // custom mode
}

// Tuning is the tuning: section.
type Tuning struct {
	SysctlProfile string `yaml:"sysctl_profile"`
	BBR           bool   `yaml:"bbr"`

	// Keys of the automatic profile (hub only; `optimize auto`).

	// NodesAuto is the owner's consent that nodes follow the hub's
	// automatic tuning, also when they connect or join later. Only used
	// while sysctl_profile is auto.
	NodesAuto bool `yaml:"nodes_auto,omitempty"`
	// BackendTier is the hub's sticky backend tier (small|medium|large);
	// a node's tier is nodes[].backend_tier. "" = the renderer's defaults.
	BackendTier string `yaml:"backend_tier,omitempty"`
	// WGMTU is the MTU of WireGuard/AmneziaWG tunnel interfaces (1280-1420);
	// 0 = the backend default (1420).
	WGMTU int `yaml:"wg_mtu,omitempty"`
}

// Security is the security: section.
type Security struct {
	FirewallManaged        bool `yaml:"firewall_managed"`
	RestrictControlToNodes bool `yaml:"restrict_control_to_nodes"`
}

// NodeSelf is the node: section of a node config.
type NodeSelf struct {
	ID               string `yaml:"id"`
	HubAddr          string `yaml:"hub_addr"`
	HubCAFingerprint string `yaml:"hub_ca_fingerprint"`
	CertFile         string `yaml:"cert_file"`
	KeyFile          string `yaml:"key_file"`
	// ControlSNI is the server name the node sends in the ClientHello of
	// the control channel (a cover name, never used to verify the hub);
	// empty = tlsutil.DefaultCoverSNI (QUESTIONS.md C.43).
	ControlSNI string `yaml:"control_sni,omitempty"`
	// Front holds the front dial settings; the node is in front mode iff
	// front.secret_file is set (hub_addr is then "<front domain>:<port>").
	Front NodeFront `yaml:"front,omitempty"`
}

// HubInfo is the subset of hub data a backend renderer needs.
type HubInfo struct {
	Name        string
	PublicIP    string
	PublicIP6   string
	Domain      string
	ControlPort int
	DecoySNIs   []string
}

// Info returns the renderer view of the hub section.
func (h *Hub) Info() HubInfo {
	if h == nil {
		return HubInfo{}
	}
	return HubInfo{Name: h.Name, PublicIP: h.PublicIP, PublicIP6: h.PublicIP6, Domain: h.Domain, ControlPort: h.ControlPort, DecoySNIs: h.DecoySNIs}
}

// LadderRef is tunnels[].ladder: either the name of a ladder profile or an
// inline list of transport ids.
type LadderRef struct {
	Name   string
	Inline []string
}

// IsZero lets yaml omit empty refs.
func (l LadderRef) IsZero() bool { return l.Name == "" && len(l.Inline) == 0 }

// String returns the name or a comma-joined inline list.
func (l LadderRef) String() string {
	if l.Name != "" {
		return l.Name
	}
	return fmt.Sprint(l.Inline)
}

// UnmarshalYAML accepts a scalar (profile name) or a sequence (inline list).
func (l *LadderRef) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		l.Name, l.Inline = n.Value, nil
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		l.Name, l.Inline = "", list
		return nil
	}
	return fmt.Errorf("line %d: ladder must be a name or a list of transports", n.Line)
}

// MarshalYAML writes the scalar or sequence form.
func (l LadderRef) MarshalYAML() (any, error) {
	if l.Name != "" || len(l.Inline) == 0 {
		return l.Name, nil
	}
	return l.Inline, nil
}
