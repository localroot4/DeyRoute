package wireguard

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Modes of Config.Mode.
const (
	ModeKernel    = "kernel"    // in-kernel WireGuard, configured over generic netlink
	ModeUserspace = "userspace" // amneziawg-go, configured over its UAPI socket
)

// Config is the content of wg.json: everything "deyroute wg up/down" needs for
// one side of one (tunnel, node, transport). It is written 0640 root:deyroute
// in the rendered config directory because it holds the private key.
type Config struct {
	Mode      string `json:"mode"`   // ModeKernel | ModeUserspace
	Side      string `json:"side"`   // "hub" | "node" (informational)
	Tunnel    string `json:"tunnel"` // tunnel id (informational)
	Interface string `json:"interface"`
	// Address is this side's tunnel address with prefix, e.g. 10.77.3.1/30.
	Address    string `json:"address"`
	MTU        int    `json:"mtu"`
	PrivateKey string `json:"private_key"` // standard base64, 32 bytes
	// ListenPort is the UDP port WireGuard listens on (the node's control
	// port); 0 lets the kernel pick one (hub side, it only dials out).
	ListenPort int `json:"listen_port,omitempty"`
	// RouteLocalnet enables net.ipv4.conf.<iface>.route_localnet so that
	// the node can DNAT tunnel traffic to 127.0.0.1 targets.
	RouteLocalnet bool       `json:"route_localnet,omitempty"`
	Peer          Peer       `json:"peer"`
	AWG           *AWGParams `json:"awg,omitempty"` // userspace only
}

// Peer is the single peer of a side (the other side).
type Peer struct {
	PublicKey string `json:"public_key"` // standard base64, 32 bytes
	// Endpoint is "ip:port" of the peer (hub side: node_ip:<ctl>); empty on
	// the node, which learns the hub's address from its handshakes.
	Endpoint   string   `json:"endpoint,omitempty"`
	AllowedIPs []string `json:"allowed_ips"`
	// PersistentKeepalive in seconds (hub side: 25; 0 = off).
	PersistentKeepalive int `json:"persistent_keepalive,omitempty"`
}

// AWGParams are the AmneziaWG obfuscation parameters (UAPI keys jc, jmin,
// jmax, s1, s2, h1-h4 of amneziawg-go).
type AWGParams struct {
	Jc   int    `json:"jc"`
	Jmin int    `json:"jmin"`
	Jmax int    `json:"jmax"`
	S1   int    `json:"s1"`
	S2   int    `json:"s2"`
	H1   uint32 `json:"h1"`
	H2   uint32 `json:"h2"`
	H3   uint32 `json:"h3"`
	H4   uint32 `json:"h4"`
}

// Limits of the AWG parameters. The bounds are stricter than what
// amneziawg-go v1.0.4 accepts (device.handlePostConfig: jmax < 65535,
// 148+s1 and 92+s2 below 65535, 148+s1 != 92+s2, distinct headers > 4) so
// that junk traffic stays small and every packet fits a 1280-byte path MTU.
const (
	awgJcMin    = 3
	awgJcMax    = 10
	awgJminLow  = 40
	awgJminHigh = 80
	awgJmaxHigh = 1280
	awgSLow     = 15
	awgSHigh    = 150
	awgHMin     = 5
	// awgSizeDelta is MessageInitiationSize(148) - MessageResponseSize(92):
	// s1+56 == s2 would make initiation and response the same size.
	awgSizeDelta = 56
)

// ifaceRe matches Linux interface names we create (IFNAMSIZ 16 incl. NUL).
var ifaceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,14}$`)

// ParseConfig decodes and validates wg.json strictly (unknown keys are
// errors). path is only used in error messages.
func ParseConfig(data []byte, path string) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, configErr(path, "not valid JSON: "+err.Error())
	}
	if err := c.check(); err != nil {
		return nil, configErr(path, err.Error())
	}
	return &c, nil
}

func configErr(path, reason string) error {
	return deyerr.New(deyerr.B072, deyerr.Params{"path": path, "reason": reason})
}

// check validates every field.
func (c *Config) check() error {
	switch c.Mode {
	case ModeKernel:
		if c.AWG != nil {
			return fmt.Errorf("awg parameters are only valid in userspace mode")
		}
	case ModeUserspace:
		if c.AWG == nil {
			return fmt.Errorf("userspace mode needs the awg parameters")
		}
		if err := c.AWG.check(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("mode must be %q or %q", ModeKernel, ModeUserspace)
	}
	if !ifaceRe.MatchString(c.Interface) {
		return fmt.Errorf("interface name %q is not valid", c.Interface)
	}
	if _, err := netip.ParsePrefix(c.Address); err != nil {
		return fmt.Errorf("address %q is not a prefix", c.Address)
	}
	if c.MTU < 1280 || c.MTU > 9000 {
		return fmt.Errorf("mtu %d is out of range", c.MTU)
	}
	if _, err := decodeKey(c.PrivateKey); err != nil {
		return fmt.Errorf("private_key: %w", err)
	}
	if c.ListenPort < 0 || c.ListenPort > 65535 {
		return fmt.Errorf("listen_port %d is out of range", c.ListenPort)
	}
	if _, err := decodeKey(c.Peer.PublicKey); err != nil {
		return fmt.Errorf("peer.public_key: %w", err)
	}
	if c.Peer.Endpoint != "" {
		if _, err := netip.ParseAddrPort(c.Peer.Endpoint); err != nil {
			return fmt.Errorf("peer.endpoint %q is not ip:port", c.Peer.Endpoint)
		}
	}
	if len(c.Peer.AllowedIPs) == 0 {
		return fmt.Errorf("peer.allowed_ips is empty")
	}
	for _, a := range c.Peer.AllowedIPs {
		if _, err := netip.ParsePrefix(a); err != nil {
			return fmt.Errorf("peer.allowed_ips entry %q is not a prefix", a)
		}
	}
	if c.Peer.PersistentKeepalive < 0 || c.Peer.PersistentKeepalive > 65535 {
		return fmt.Errorf("peer.persistent_keepalive %d is out of range", c.Peer.PersistentKeepalive)
	}
	return nil
}

// check validates the AWG parameters against the limits above.
func (a *AWGParams) check() error {
	switch {
	case a.Jc < awgJcMin || a.Jc > awgJcMax:
		return fmt.Errorf("awg jc %d is not in %d..%d", a.Jc, awgJcMin, awgJcMax)
	case a.Jmin < awgJminLow || a.Jmin > awgJminHigh:
		return fmt.Errorf("awg jmin %d is not in %d..%d", a.Jmin, awgJminLow, awgJminHigh)
	case a.Jmax <= a.Jmin || a.Jmax > awgJmaxHigh:
		return fmt.Errorf("awg jmax %d is not in jmin+1..%d", a.Jmax, awgJmaxHigh)
	case a.S1 < awgSLow || a.S1 > awgSHigh || a.S2 < awgSLow || a.S2 > awgSHigh:
		return fmt.Errorf("awg s1/s2 must be in %d..%d", awgSLow, awgSHigh)
	case a.S1+awgSizeDelta == a.S2:
		return fmt.Errorf("awg s1+%d must differ from s2", awgSizeDelta)
	}
	hs := []uint32{a.H1, a.H2, a.H3, a.H4}
	for i, h := range hs {
		if h < awgHMin {
			return fmt.Errorf("awg h%d must be at least %d", i+1, awgHMin)
		}
		for _, o := range hs[:i] {
			if o == h {
				return fmt.Errorf("awg h1-h4 must be distinct")
			}
		}
	}
	return nil
}

// decodeKey decodes a standard-base64 32-byte Curve25519 key.
func decodeKey(s string) ([32]byte, error) {
	var k [32]byte
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != len(k) {
		return k, fmt.Errorf("not a base64 32-byte key")
	}
	copy(k[:], raw)
	return k, nil
}
