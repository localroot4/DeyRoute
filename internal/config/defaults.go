package config

import (
	"path/filepath"
	"strconv"
)

// Default secret file locations referenced from the config (section 4).
var (
	// DefaultTelegramTokenFile is hub.notify.telegram.bot_token_file.
	DefaultTelegramTokenFile = filepath.Join(SecretsDir, "telegram.token")
	// DefaultCloudflareTokenFile is hub.acme.cloudflare_token_file when the
	// token is set from the menu or the CLI (DNS-01, section 10).
	DefaultCloudflareTokenFile = filepath.Join(SecretsDir, "cloudflare.token")
	// DefaultNodeCertFile is node.cert_file on a node.
	DefaultNodeCertFile = filepath.Join(SecretsDir, "node.crt")
	// DefaultNodeKeyFile is node.key_file on a node.
	DefaultNodeKeyFile = filepath.Join(SecretsDir, "node.key")
)

// Default enum values (sections 4, 9, 10, 12).
const (
	DefaultUIMode        = UIModeSimple
	DefaultPolicy        = PolicyTransportThenNode
	DefaultTLSMode       = TLSModeAuto
	DefaultProto         = ProtoTCP
	DefaultProbe         = ProbeAuto
	DefaultSysctlProfile = SysctlBalanced
	DefaultTargetHost    = "127.0.0.1"
)

// DefaultFailover returns the failover defaults of section 9, including
// failback: true (see ApplyDefaults for why booleans come from here).
func DefaultFailover() Failover {
	return Failover{
		Policy:             DefaultPolicy,
		ProbeIntervalS:     DefaultProbeIntervalS,
		ProbeTimeoutS:      DefaultProbeTimeoutS,
		FailThreshold:      DefaultFailThreshold,
		RecoverThreshold:   DefaultRecoverThresh,
		Failback:           true,
		FailbackAfterS:     DefaultFailbackAfterS,
		MaxSwitchesPerHour: DefaultMaxSwitchesHour,
		QuarantineS:        DefaultQuarantineS,
	}
}

// DefaultTuning returns tuning: sysctl_profile balanced, bbr true.
func DefaultTuning() *Tuning { return &Tuning{SysctlProfile: DefaultSysctlProfile, BBR: true} }

// DefaultSecurity returns security: both switches on.
func DefaultSecurity() *Security {
	return &Security{FirewallManaged: true, RestrictControlToNodes: true}
}

// DefaultTarget returns the default port-map target "127.0.0.1:<listen>".
func DefaultTarget(listen int) string {
	return DefaultTargetHost + ":" + strconv.Itoa(listen)
}

// NewHub returns a complete hub config with every default of section 4
// (control port 44433 when controlPort is 0, ui_mode simple, telegram off
// with the default events, the default ladder, balanced tuning with BBR and
// the managed firewall).
func NewHub(name, publicIP string, controlPort int) *Config {
	if controlPort == 0 {
		controlPort = DefaultControlPort
	}
	c := &Config{
		SchemaVersion: SchemaVersion,
		Role:          RoleHub,
		Hub: &Hub{
			Name:        name,
			ControlPort: controlPort,
			PublicIP:    publicIP,
			UIMode:      DefaultUIMode,
		},
		Tuning:   DefaultTuning(),
		Security: DefaultSecurity(),
	}
	c.ApplyDefaults()
	return c
}

// NewNode returns a complete node config pointing at hubAddr ("ip:port") and
// pinning the hub CA fingerprint ("sha256:<64 hex>").
func NewNode(id, hubAddr, caFingerprint string) *Config {
	c := &Config{
		SchemaVersion: SchemaVersion,
		Role:          RoleNode,
		Node: &NodeSelf{
			ID:               id,
			HubAddr:          hubAddr,
			HubCAFingerprint: caFingerprint,
			CertFile:         DefaultNodeCertFile,
			KeyFile:          DefaultNodeKeyFile,
		},
		Tuning:   DefaultTuning(),
		Security: DefaultSecurity(),
	}
	c.ApplyDefaults()
	return c
}

// NewTunnel returns an enabled tunnel with every default: the "default"
// ladder, section 9 failover values (failback on), tls auto, and port maps
// completed with proto tcp, target 127.0.0.1:<listen> and probe auto. name
// defaults to id. nodes and ports are copied.
func NewTunnel(id, name string, nodes []string, ports []PortMap) Tunnel {
	if name == "" {
		name = id
	}
	t := Tunnel{
		ID:       id,
		Name:     name,
		Enabled:  true,
		Nodes:    cloneStrings(nodes),
		Ports:    clonePorts(ports),
		Ladder:   LadderRef{Name: DefaultLadderName},
		Failover: DefaultFailover(),
		TLS:      TLS{Mode: DefaultTLSMode},
	}
	t.applyDefaults()
	return t
}

// ApplyDefaults fills zero values with the spec defaults: schema_version,
// hub control_port/ui_mode/telegram (bot_token_file, events), the "default"
// ladder profile when no ladders: section exists (hub only), per tunnel the
// ladder "default", failover numbers and policy, quarantine_s, tls.mode auto,
// port proto tcp / target 127.0.0.1:<listen> / probe auto, a missing tuning
// section (balanced + bbr) or security section (both on), an empty
// tuning.sysctl_profile, and node cert/key paths.
//
// Booleans are deliberately NOT defaulted here: after decoding, a plain bool
// cannot tell an explicit "false" from a missing key, and overwriting an
// owner's "failback: false" would be wrong. Boolean defaults (tunnel enabled,
// failover failback, tuning bbr, security switches) come from the
// constructors (NewHub, NewNode, NewTunnel, DefaultFailover, DefaultTuning,
// DefaultSecurity), from whole missing sections here, and — when a file is
// parsed — from the YAML tree, where a missing key is visible (Parse/Load).
//
// An explicit empty inline ladder ("ladder: []") is kept, so Validate reports
// it as DEY-C009 instead of silently replacing it with the default ladder.
//
// ApplyDefaults is idempotent; a nil config is left alone.
func (c *Config) ApplyDefaults() {
	if c == nil {
		return
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = currentSchema
	}
	if c.Hub != nil {
		c.Hub.applyDefaults()
	}
	if c.Role == RoleHub && c.Ladders == nil {
		c.Ladders = map[string][]string{DefaultLadderName: cloneStrings(DefaultLadder)}
	}
	for i := range c.Tunnels {
		c.Tunnels[i].applyDefaults()
	}
	if c.Tuning == nil {
		c.Tuning = DefaultTuning()
	} else if c.Tuning.SysctlProfile == "" {
		c.Tuning.SysctlProfile = DefaultSysctlProfile
	}
	if c.Security == nil {
		c.Security = DefaultSecurity()
	}
	if c.Node != nil {
		if c.Node.CertFile == "" {
			c.Node.CertFile = DefaultNodeCertFile
		}
		if c.Node.KeyFile == "" {
			c.Node.KeyFile = DefaultNodeKeyFile
		}
	}
}

func (h *Hub) applyDefaults() {
	if h.ControlPort == 0 {
		h.ControlPort = DefaultControlPort
	}
	if h.UIMode == "" {
		h.UIMode = DefaultUIMode
	}
	tg := &h.Notify.Telegram
	if tg.BotTokenFile == "" {
		tg.BotTokenFile = DefaultTelegramTokenFile
	}
	if tg.Events == nil {
		tg.Events = cloneStrings(DefaultTelegramEvents)
	}
}

func (t *Tunnel) applyDefaults() {
	if t.Name == "" {
		t.Name = t.ID
	}
	if t.Ladder.Name == "" && t.Ladder.Inline == nil {
		t.Ladder = LadderRef{Name: DefaultLadderName}
	}
	f := &t.Failover
	d := DefaultFailover()
	if f.Policy == "" {
		f.Policy = d.Policy
	}
	setIfZero(&f.ProbeIntervalS, d.ProbeIntervalS)
	setIfZero(&f.ProbeTimeoutS, d.ProbeTimeoutS)
	setIfZero(&f.FailThreshold, d.FailThreshold)
	setIfZero(&f.RecoverThreshold, d.RecoverThreshold)
	setIfZero(&f.FailbackAfterS, d.FailbackAfterS)
	setIfZero(&f.MaxSwitchesPerHour, d.MaxSwitchesPerHour)
	setIfZero(&f.QuarantineS, d.QuarantineS)
	if t.TLS.Mode == "" {
		t.TLS.Mode = DefaultTLSMode
	}
	for i := range t.Ports {
		t.Ports[i].applyDefaults()
	}
}

func (p *PortMap) applyDefaults() {
	if p.Proto == "" {
		p.Proto = DefaultProto
	}
	if p.Target == "" && p.Listen > 0 {
		p.Target = DefaultTarget(p.Listen)
	}
	if p.Probe == "" {
		p.Probe = DefaultProbe
	}
}

func setIfZero(v *int, d int) {
	if *v == 0 {
		*v = d
	}
}
