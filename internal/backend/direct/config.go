package direct

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// RelayConfigFile is the file name of the relay configuration inside the
// rendered config directory.
const RelayConfigFile = "relay.json"

// Relay roles (RelayConfig.Role).
const (
	// RoleHub listens on the user ports and forwards to the node relay.
	RoleHub = "hub"
	// RoleNode listens on the backend control port and forwards to the
	// configured targets only.
	RoleNode = "node"
	// RoleCheck is the node half of direct/haproxy: nothing is relayed; the
	// process verifies once that every target service is reachable on a
	// non-loopback address (HAProxy on the hub connects to it directly)
	// and exits.
	RoleCheck = "check"
)

// RelayConfigVersion is the relay.json format version.
const RelayConfigVersion = 1

// Defaults (spec section 12 and 7.8).
const (
	DefaultIdleTimeout    = 5 * time.Minute  // TCP connection idle timeout
	DefaultDialTimeout    = 10 * time.Second // TCP dial timeout (hub→node, node→target)
	DefaultUDPIdleTimeout = 60 * time.Second // UDP session expiry
	DefaultMaxUDPSessions = 4096             // per relay process
	// ReplayWindow is how long authenticated TCP nonces are remembered.
	ReplayWindow = 5 * time.Minute
)

// RelayConfig is relay.json, rendered per side by the direct backend and
// read by "deyroute relay --tunnel <id> --config <file>".
type RelayConfig struct {
	Version int    `json:"version"`
	Tunnel  string `json:"tunnel"`
	Role    string `json:"role"`
	// Token is the shared tunnel token (HMAC key). Never logged.
	Token string `json:"token,omitempty"`
	// Node is the node relay address the hub dials (hub role).
	Node string `json:"node,omitempty"`
	// Bind is the address the node relay listens on (node role).
	Bind string `json:"bind,omitempty"`
	// NodeIP is the node's public IP (check role).
	NodeIP string      `json:"node_ip,omitempty"`
	Ports  []RelayPort `json:"ports"`

	IdleTimeoutS    int `json:"idle_timeout_s,omitempty"`
	DialTimeoutS    int `json:"dial_timeout_s,omitempty"`
	UDPIdleTimeoutS int `json:"udp_idle_timeout_s,omitempty"`
	MaxUDPSessions  int `json:"max_udp_sessions,omitempty"`
}

// RelayPort is one port map. Index is the position of the port map in the
// tunnel, identical on both sides; the hub knows only Listen, the node only
// Target (its allow-list).
type RelayPort struct {
	Index  int    `json:"index"`
	Proto  string `json:"proto"`
	Listen string `json:"listen,omitempty"`
	Target string `json:"target,omitempty"`
}

// IdleTimeout returns the TCP idle timeout.
func (c *RelayConfig) IdleTimeout() time.Duration {
	return seconds(c.IdleTimeoutS, DefaultIdleTimeout)
}

// DialTimeout returns the dial timeout.
func (c *RelayConfig) DialTimeout() time.Duration {
	return seconds(c.DialTimeoutS, DefaultDialTimeout)
}

// UDPIdleTimeout returns the UDP session expiry.
func (c *RelayConfig) UDPIdleTimeout() time.Duration {
	return seconds(c.UDPIdleTimeoutS, DefaultUDPIdleTimeout)
}

// UDPSessionLimit returns the maximum number of UDP sessions.
func (c *RelayConfig) UDPSessionLimit() int {
	if c.MaxUDPSessions > 0 {
		return c.MaxUDPSessions
	}
	return DefaultMaxUDPSessions
}

func seconds(v int, def time.Duration) time.Duration {
	if v > 0 {
		return time.Duration(v) * time.Second
	}
	return def
}

// LoadRelayConfig reads and validates a relay.json. Errors are DEY-B060.
func LoadRelayConfig(path string) (*RelayConfig, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path of the rendered config (unit ExecStart)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.B060, err, deyerr.Params{"path": path, "reason": "cannot read the file"})
	}
	return ParseRelayConfig(path, data)
}

// ParseRelayConfig decodes relay.json strictly (unknown keys are errors)
// and validates it; path is only used in error messages.
func ParseRelayConfig(path string, data []byte) (*RelayConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c RelayConfig
	if err := dec.Decode(&c); err != nil {
		return nil, deyerr.Wrap(deyerr.B060, err, deyerr.Params{"path": path, "reason": "not valid relay JSON"})
	}
	if err := c.Validate(); err != nil {
		return nil, deyerr.Wrap(deyerr.B060, err, deyerr.Params{"path": path, "reason": err.Error()})
	}
	return &c, nil
}

// Validate checks c for its role.
func (c *RelayConfig) Validate() error {
	if c.Version != RelayConfigVersion {
		return deyerr.Plain("unsupported version " + strconv.Itoa(c.Version))
	}
	if strings.TrimSpace(c.Tunnel) == "" {
		return deyerr.Plain("tunnel is empty")
	}
	if c.IdleTimeoutS < 0 || c.DialTimeoutS < 0 || c.UDPIdleTimeoutS < 0 || c.MaxUDPSessions < 0 {
		return deyerr.Plain("timeouts and limits must not be negative")
	}
	switch c.Role {
	case RoleHub:
		if c.Token == "" {
			return deyerr.Plain("token is empty")
		}
		if !validAddr(c.Node) {
			return deyerr.Plain("node address '" + c.Node + "' is not host:port")
		}
	case RoleNode:
		if c.Token == "" {
			return deyerr.Plain("token is empty")
		}
		if !validAddr(c.Bind) {
			return deyerr.Plain("bind address '" + c.Bind + "' is not host:port")
		}
	case RoleCheck:
		if net.ParseIP(c.NodeIP) == nil {
			return deyerr.Plain("node_ip '" + c.NodeIP + "' is not an IP address")
		}
	default:
		return deyerr.Plain("unknown role '" + c.Role + "'")
	}
	if len(c.Ports) == 0 {
		return deyerr.Plain("no ports")
	}
	seen := map[int]bool{}
	for _, p := range c.Ports {
		if p.Index < 0 || p.Index >= config.MaxPortMaps || seen[p.Index] {
			return deyerr.Plain("port index " + strconv.Itoa(p.Index) + " is invalid or duplicated")
		}
		seen[p.Index] = true
		if p.Proto != config.ProtoTCP && p.Proto != config.ProtoUDP {
			return deyerr.Plain("port " + strconv.Itoa(p.Index) + ": proto must be tcp or udp")
		}
		if c.Role == RoleCheck && p.Proto != config.ProtoTCP {
			return deyerr.Plain("port " + strconv.Itoa(p.Index) + ": check supports tcp only")
		}
		switch c.Role {
		case RoleHub:
			if !validAddr(p.Listen) {
				return deyerr.Plain("port " + strconv.Itoa(p.Index) + ": listen '" + p.Listen + "' is not host:port")
			}
		default:
			if !validAddr(p.Target) {
				return deyerr.Plain("port " + strconv.Itoa(p.Index) + ": target '" + p.Target + "' is not host:port")
			}
		}
	}
	return nil
}

// validAddr reports whether s is host:port with a port in 1..65535.
func validAddr(s string) bool {
	host, port, ok := backend.SplitTarget(s)
	return ok && host != "" && port > 0 && !strings.ContainsAny(host, " \t\r\n")
}
