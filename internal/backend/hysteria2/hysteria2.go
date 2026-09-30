// Package hysteria2 renders the apernet/hysteria (Hysteria 2) backend (spec
// section 7.6): a Forward, QUIC based transport. The node runs the Hysteria
// server on UDP <ctl> with the tunnel certificate, password authentication
// and Salamander obfuscation; the hub runs the client whose tcpForwarding and
// udpForwarding entries bind the tunnel's user ports.
//
// The server ACL only lets the tunnel's own targets through and rejects
// everything else, so the node never becomes an open proxy (spec section 11,
// scenario S17).
//
// Keys follow app/cmd/server.go and app/cmd/client.go of the version pinned
// in backends.yaml (app/v2.12.3); docs/backends/hysteria2.md lists the
// differences to the spec sample. Rendering is pure and deterministic.
package hysteria2

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name used in transport ids ("hysteria2/udp").
const Name = "hysteria2"

// UDP is the only transport of this backend.
const UDP = "udp"

// Rendered file names (relative to Paths.ConfigDir).
const (
	ServerFile = "server.yaml" // node side
	ClientFile = "client.yaml" // hub side
)

// KeyObfsPassword is the Salamander obfuscation password produced by
// GenerateKeys (32 random bytes, base64url without padding). It is separate
// from the tunnel token used for authentication (spec 7.6).
const KeyObfsPassword = "obfs_password"

// Port hopping range on the node (spec 7.6). Packets to any port of the range
// are redirected to <ctl> by a DNAT rule on the node.
const (
	HopLow  = 20000
	HopHigh = 20999
)

// maxMbps bounds the owner supplied bandwidth values (100 Gbit/s).
const maxMbps = 100000

// Backend implements backend.Backend and backend.KeyGenerator for Hysteria 2.
type Backend struct {
	// rand is the entropy source of GenerateKeys (crypto/rand in production).
	rand io.Reader
}

// New returns the Hysteria 2 backend.
func New() *Backend { return &Backend{rand: rand.Reader} }

func init() { backend.Register(New()) }

// Name returns "hysteria2".
func (*Backend) Name() string { return Name }

// Transports returns hysteria2/udp (spec section 7 comparison table: Forward,
// tcp+udp, needs UDP between hub and node, stealth 3, tunnel TLS).
func (*Backend) Transports() []backend.Transport {
	return []backend.Transport{{
		Backend:   Name,
		Name:      UDP,
		Direction: backend.Forward,
		Protos:    []string{config.ProtoTCP, config.ProtoUDP},
		NeedsUDP:  true,
		NeedsTLS:  true,
		Stealth:   3,
	}}
}

// Manifest returns the pinned Hysteria release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no backend specific check (the traffic stats API stays off).
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// GenerateKeys creates the Salamander obfuscation password in pure Go.
func (b *Backend) GenerateKeys(backend.Transport) (map[string]string, error) {
	r := b.rand
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, 32)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": Name})
	}
	return map[string]string{KeyObfsPassword: base64.RawURLEncoding.EncodeToString(buf)}, nil
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := plan(in)
	return err
}

// Render renders the hub (client) or node (server) side. It is pure.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	p, err := plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderServer(in, p), nil
	}
	return renderClient(in, p), nil
}

// mapping is one port map ready for rendering.
type mapping struct {
	listen int
	proto  string
	host   string // target host without brackets
	port   int
}

func (m mapping) target() string { return backend.HostPort(m.host, m.port) }

// planned is everything Render needs after validation.
type planned struct {
	maps       []mapping
	obfs       string
	upMbps     int
	downMbps   int
	hopping    bool
	listenAddr string
}

// plan validates in and prepares the render data.
func plan(in backend.RenderInput) (planned, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != Name || in.Transport.Name != UDP {
		return planned{}, fail("not a hysteria2 transport")
	}
	ports := portMaps(in)
	if len(ports) == 0 {
		return planned{}, fail("the tunnel has no port maps")
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return planned{}, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	if !filepath.IsAbs(in.Paths.Binary) {
		return planned{}, fail("the hysteria binary is not installed (no absolute binary path)")
	}
	if !filepath.IsAbs(in.Paths.ConfigDir) {
		return planned{}, fail("the rendered config directory is not an absolute path")
	}
	if in.Secrets.Token == "" {
		return planned{}, fail("the tunnel token is missing")
	}
	if in.Secrets.TLSCertFile == "" || in.Secrets.TLSKeyFile == "" {
		return planned{}, fail("the tunnel TLS certificate or key is missing")
	}
	if pin := in.Secrets.TLSCertSHA256; len(pin) != 64 || !isHex(pin) {
		return planned{}, fail("the tunnel certificate sha256 (pinSHA256) is missing or malformed")
	}
	obfs := in.Secrets.Keys[KeyObfsPassword]
	if len(obfs) < 16 {
		return planned{}, fail("the obfuscation password is missing (obfs_password); regenerate the tunnel keys")
	}
	if net.ParseIP(strings.TrimSpace(in.Node.PublicIP)) == nil && !validDomain(in.Node.PublicIP) {
		return planned{}, fail("the node public IP is unknown (the hub dials it)")
	}
	up, down, hop := config.DefaultHysteriaMbps, config.DefaultHysteriaMbps, false
	if adv := in.Tunnel.Advanced; adv != nil {
		if adv.HysteriaUpMbps < 0 || adv.HysteriaUpMbps > maxMbps || adv.HysteriaDownMbps < 0 || adv.HysteriaDownMbps > maxMbps {
			return planned{}, fail("advanced.hysteria_up_mbps/hysteria_down_mbps must be between 1 and " + strconv.Itoa(maxMbps))
		}
		if adv.HysteriaUpMbps > 0 {
			up = adv.HysteriaUpMbps
		}
		if adv.HysteriaDownMbps > 0 {
			down = adv.HysteriaDownMbps
		}
		hop = adv.HysteriaPortHopping
	}
	if hop && in.ControlPort >= HopLow && in.ControlPort <= HopHigh {
		return planned{}, fail("the control port lies inside the port hopping range")
	}

	seen := map[string]bool{}
	maps := make([]mapping, 0, len(ports))
	for _, pm := range ports {
		proto := protoOf(pm)
		if proto != config.ProtoTCP && proto != config.ProtoUDP {
			return planned{}, deyerr.New(deyerr.B010, deyerr.Params{"transport": id, "proto": proto})
		}
		if pm.Listen < 1 || pm.Listen > 65535 {
			return planned{}, fail("listen port " + strconv.Itoa(pm.Listen) + " is out of range")
		}
		key := proto + "/" + strconv.Itoa(pm.Listen)
		if seen[key] {
			return planned{}, fail("listen port " + key + " is listed twice")
		}
		seen[key] = true
		target := targetOf(pm)
		host, port, ok := backend.SplitTarget(target)
		if !ok || (net.ParseIP(host) == nil && !validDomain(host)) {
			return planned{}, fail("invalid target '" + target + "'")
		}
		maps = append(maps, mapping{listen: pm.Listen, proto: proto, host: host, port: port})
	}
	return planned{maps: maps, obfs: obfs, upMbps: up, downMbps: down, hopping: hop, listenAddr: listenAddr(in)}, nil
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

// validDomain is a conservative host name check (letters, digits, '-', '.').
func validDomain(h string) bool {
	if h == "" || len(h) > 253 || strings.HasPrefix(h, ".") {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !isLabelRune(r) {
				return false
			}
		}
	}
	return true
}

func isLabelRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-'
}

// portMaps returns every port map, or only the first one for the canary.
func portMaps(in backend.RenderInput) []config.PortMap {
	ports := in.Tunnel.Ports
	if in.Canary && len(ports) > 1 {
		ports = ports[:1]
	}
	return ports
}

// listenAddr is the hub address user ports bind to; the canary never binds
// a public address.
func listenAddr(in backend.RenderInput) string {
	addr := in.ListenAddrOrDefault()
	if in.Canary && !isLoopback(addr) {
		return "127.0.0.1"
	}
	return addr
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// protoOf treats an empty proto as tcp (the config default).
func protoOf(p config.PortMap) string {
	if p.Proto == "" {
		return config.ProtoTCP
	}
	return p.Proto
}

// targetOf returns the port map target, defaulting to 127.0.0.1:<listen>.
func targetOf(p config.PortMap) string {
	if p.Target == "" {
		return backend.HostPort("127.0.0.1", p.Listen)
	}
	return p.Target
}
