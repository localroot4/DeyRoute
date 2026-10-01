// Package state wraps the bbolt database /var/lib/deyroute/state.db that holds
// every runtime fact (section 4): node liveness, tunnel state machines, probe
// history, the event ring buffer and metrics. Nothing here is ever written to
// config.yaml. All times are UTC.
package state

import "time"

// Bucket names (section 4).
const (
	BucketNodes    = "nodes"    // nodes/<id>
	BucketTunnels  = "tunnels"  // tunnels/<id>
	BucketProbes   = "probes"   // probes/<tunnel>/<node>/<transport>
	BucketEvents   = "events"   // ring buffer of the last MaxEvents events
	BucketMetrics  = "metrics"  // metrics/<tunnel>
	BucketCtlPorts = "ctlports" // backend control port allocations "<tunnel>/<node>/<transport>" → port
	BucketNetIdx   = "netidx"   // per-tunnel small integers (WireGuard subnets) "<tunnel>" → n
	BucketMeta     = "meta"     // misc key/value (udp probe results, decoy choice, update check…)
)

// Limits (sections 4 and 12).
const (
	MaxEvents       = 5000
	MaxProbeSamples = 120
)

// Tunnel state machine states (section 9).
const (
	StateInit      = "INIT"
	StateStarting  = "STARTING"
	StateUp        = "UP"
	StateDegraded  = "DEGRADED"
	StateSwitching = "SWITCHING"
	StateDown      = "DOWN"
	StatePaused    = "PAUSED"
	StateDisabled  = "DISABLED" // tunnel enabled=false (display only)
)

// Event types (section 9; names are fixed).
const (
	EvTunnelUp          = "tunnel_up"
	EvTunnelDegraded    = "tunnel_degraded"
	EvTunnelDown        = "tunnel_down"
	EvSwitchTransport   = "switch_transport"
	EvSwitchNode        = "switch_node"
	EvFailback          = "failback"
	EvFailbackFailed    = "failback_failed"
	EvFlapping          = "flapping"
	EvNodeOnline        = "node_online"
	EvNodeOffline       = "node_offline"
	EvServiceDown       = "service_down"
	EvBackendCrash      = "backend_crash"
	EvProbeError        = "probe_error"
	EvUpdateApplied     = "update_applied"
	EvUpdateRolledBack  = "update_rolled_back"
	EvBackendRolledBack = "backend_update_rolled_back" // section 5
	EvNodeIPChanged     = "node_ip_changed"            // section 11
	EvACMEFailed        = "acme_failed"                // section 10
	EvRungSkipped       = "rung_skipped"               // yellow warning: validate failed / UDP closed (section 8)
	EvRungRestored      = "rung_restored"
	EvConfigApplied     = "config_applied"
)

// Event levels.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// NodeState is nodes/<id>.
type NodeState struct {
	ID            string            `json:"id"`
	Online        bool              `json:"online"`
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	OnlineSince   time.Time         `json:"online_since"`
	AgentVersion  string            `json:"agent_version"`
	Compatible    bool              `json:"compatible"`
	ControlRTTms  int               `json:"control_rtt_ms"`
	CPUPercent    float64           `json:"cpu_percent"`
	RAMBytes      uint64            `json:"ram_bytes"`
	Units         map[string]string `json:"units,omitempty"` // unit → ActiveState
	LastError     string            `json:"last_error,omitempty"`
	RemoteIP      string            `json:"remote_ip,omitempty"` // IP the control connection came from
	Arch          string            `json:"arch,omitempty"`
	OS            string            `json:"os,omitempty"`
	Kernel        string            `json:"kernel,omitempty"`
	Country       string            `json:"country,omitempty"`
	UDPOK         *bool             `json:"udp_ok,omitempty"` // last UDP echo probe result
	UDPCheckedAt  time.Time         `json:"udp_checked_at"`
}

// Candidate identifies one rung on one node.
type Candidate struct {
	Node      string `json:"node"`
	Transport string `json:"transport"`
}

// Key returns "node/transport".
func (c Candidate) Key() string { return c.Node + "/" + c.Transport }

// IsZero reports an empty candidate.
func (c Candidate) IsZero() bool { return c.Node == "" && c.Transport == "" }

// Quarantine is a failed candidate's cool-down (section 9: 10 min doubling to 1 h).
type Quarantine struct {
	Until    time.Time     `json:"until"`
	Duration time.Duration `json:"duration"` // last applied duration, doubles on repeat
}

// Skip marks a rung removed from one tunnel's ladder with a yellow warning
// (validate failed / UDP closed); re-tested every 30 minutes (section 8).
type Skip struct {
	Reason    string    `json:"reason"`
	Code      string    `json:"code"`
	RecheckAt time.Time `json:"recheck_at"`
}

// TunnelState is tunnels/<id>.
type TunnelState struct {
	ID              string                `json:"id"`
	State           string                `json:"state"`
	Active          Candidate             `json:"active"`
	Previous        Candidate             `json:"previous"` // rung before the last switch (failback revert)
	FailCount       int                   `json:"fail_count"`
	RecoverCount    int                   `json:"recover_count"`
	UpSince         time.Time             `json:"up_since"`
	StableSince     time.Time             `json:"stable_since"` // start of the current uninterrupted UP period
	LastSwitch      time.Time             `json:"last_switch"`
	SwitchTimes     []time.Time           `json:"switch_times,omitempty"` // automatic switches in the last hour
	LastRTTms       int                   `json:"last_rtt_ms"`
	LastProbeErr    string                `json:"last_probe_err,omitempty"`
	Paused          bool                  `json:"paused"`
	ServiceDown     bool                  `json:"service_down"`
	Flapping        bool                  `json:"flapping"`
	FailbackDelay   time.Duration         `json:"failback_delay"` // current failback_after (doubles on failure, cap 24h)
	Quarantine      map[string]Quarantine `json:"quarantine,omitempty"`
	Skipped         map[string]Skip       `json:"skipped,omitempty"` // key node/transport
	Tried           []string              `json:"tried,omitempty"`   // candidates tried in the current switching cycle
	DownRetryAt     time.Time             `json:"down_retry_at"`
	DownBackoff     time.Duration         `json:"down_backoff"`
	CanaryPasses    int                   `json:"canary_passes"`
	TransitionCause string                `json:"transition_cause,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

// ProbeSample is one probe result in probes/<tunnel>/<node>/<transport>.
type ProbeSample struct {
	At    time.Time     `json:"at"`
	OK    bool          `json:"ok"`
	RTT   time.Duration `json:"rtt"`
	Kind  string        `json:"kind,omitempty"` // path|node_service|control|udp|port:<n>
	Error string        `json:"error,omitempty"`
}

// Event is one entry of the events ring buffer.
type Event struct {
	Seq           uint64    `json:"seq"`
	At            time.Time `json:"at"`
	Level         string    `json:"level"`
	Type          string    `json:"type"`
	Tunnel        string    `json:"tunnel,omitempty"`
	Node          string    `json:"node,omitempty"`
	FromTransport string    `json:"from_transport,omitempty"`
	ToTransport   string    `json:"to_transport,omitempty"`
	FromNode      string    `json:"from_node,omitempty"`
	ToNode        string    `json:"to_node,omitempty"`
	Reason        string    `json:"reason,omitempty"` // probe text
	Code          string    `json:"code,omitempty"`   // DEY code
	Message       string    `json:"message"`
}

// EventFilter selects events; zero values mean "any".
type EventFilter struct {
	Tunnel string
	Node   string
	Types  []string
	Since  time.Time
	Limit  int // newest N after filtering; 0 = all
}

// Metrics is metrics/<tunnel>.
type Metrics struct {
	At          time.Time `json:"at"`
	BytesIn     uint64    `json:"bytes_in"`
	BytesOut    uint64    `json:"bytes_out"`
	ActiveConns int       `json:"active_conns"`
	Source      string    `json:"source"` // "backend" | "ss"
}
