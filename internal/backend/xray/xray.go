// Package xray renders the XTLS/Xray-core "Reality relay" backend (spec
// section 7.5): a Forward transport where the node runs a VLESS + REALITY
// inbound on its backend control port and the hub runs one dokodemo-door
// inbound per port map that forwards through a VLESS + REALITY outbound to
// the node. To an observer the hub-to-node connection is a TLS 1.3
// handshake with a real decoy site.
//
// The node side only lets the tunnel's own targets through (routing
// allow-list, everything else goes to a blackhole outbound), so the node never
// becomes an open proxy (spec section 11, scenario S17).
//
// Keys follow infra/conf of the version pinned in backends.yaml (v26.3.27);
// docs/backends/xray.md lists the differences to the spec sample. Rendering is
// pure and deterministic.
package xray

import (
	"context"
	"crypto/ecdh"
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

// Name is the backend name used in transport ids ("xray/reality").
const Name = "xray"

// Reality is the only transport of this backend.
const Reality = "reality"

// ConfigFile is the rendered Xray configuration on both sides.
const ConfigFile = "config.json"

// Key names produced by GenerateKeys and stored in
// /etc/deyroute/secrets/backend-keys/<tunnel>/xray.json.
const (
	// KeyPrivate is the node's REALITY X25519 private key
	// (base64.RawURLEncoding, like "xray x25519").
	KeyPrivate = "reality_private"
	// KeyPublic is the matching public key the hub client uses
	// (realitySettings.password, formerly publicKey).
	KeyPublic = "reality_public"
	// KeyShortID is the REALITY shortId: 8 lowercase hex characters.
	KeyShortID = "reality_short_id"
	// KeyUUID is the VLESS user id (random UUID v4) shared by both sides.
	KeyUUID = "uuid"
)

// Rendering constants (spec section 7.5).
const (
	flowVision       = "xtls-rprx-vision"
	flowVisionUDP443 = "xtls-rprx-vision-udp443"
	fingerprint      = "chrome"
	logLevel         = "warning"
	decoyPort        = 443
	inboundTag       = "vless-in"
	outboundNode     = "to-node"
	outboundDirect   = "direct"
	outboundBlock    = "block"
)

// Backend implements backend.Backend and backend.KeyGenerator for Xray.
type Backend struct {
	// rand is the entropy source of GenerateKeys (crypto/rand in production).
	rand io.Reader
}

// New returns the Xray backend.
func New() *Backend { return &Backend{rand: rand.Reader} }

func init() { backend.Register(New()) }

// Name returns "xray".
func (*Backend) Name() string { return Name }

// Transports returns xray/reality (spec section 7 comparison table: Forward,
// tcp+udp, stealth 5). UDP port maps travel as XUDP inside the TCP REALITY
// connection, so the rung does not need UDP between hub and node. REALITY
// uses its own X25519 keys, not the tunnel certificate.
func (*Backend) Transports() []backend.Transport {
	return []backend.Transport{{
		Backend:   Name,
		Name:      Reality,
		Direction: backend.Forward,
		Protos:    []string{config.ProtoTCP, config.ProtoUDP},
		Stealth:   5,
	}}
}

// Manifest returns the pinned Xray-core release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no backend specific check (the Xray API is not enabled).
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// GenerateKeys creates the per-tunnel REALITY key pair, shortId and VLESS
// UUID in pure Go (crypto/ecdh X25519, crypto/rand), in the same encodings
// "xray x25519" and "xray uuid" print.
func (b *Backend) GenerateKeys(backend.Transport) (map[string]string, error) {
	r := b.rand
	if r == nil {
		r = rand.Reader
	}
	fail := func(err error) error { return deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": Name}) }

	seed := make([]byte, 32)
	if _, err := io.ReadFull(r, seed); err != nil {
		return nil, fail(err)
	}
	// Clamp like Xray's genCurve25519 so the printed private key is the
	// scalar actually used.
	seed[0] &= 248
	seed[31] &= 127
	seed[31] |= 64
	priv, err := ecdh.X25519().NewPrivateKey(seed)
	if err != nil {
		return nil, fail(err)
	}
	sid := make([]byte, 4)
	if _, err := io.ReadFull(r, sid); err != nil {
		return nil, fail(err)
	}
	u := make([]byte, 16)
	if _, err := io.ReadFull(r, u); err != nil {
		return nil, fail(err)
	}
	u[6] = (u[6] & 0x0f) | 0x40 // version 4
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return map[string]string{
		KeyPrivate: base64.RawURLEncoding.EncodeToString(priv.Bytes()),
		KeyPublic:  base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		KeyShortID: hex.EncodeToString(sid),
		KeyUUID:    formatUUID(u),
	}, nil
}

func formatUUID(u []byte) string {
	h := hex.EncodeToString(u)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := plan(in)
	return err
}

// Render renders the hub (dokodemo-door + VLESS client) or node (VLESS
// REALITY server with routing allow-list) side. It is pure.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	p, err := plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderNode(in, p)
	}
	return renderHub(in, p)
}

// mapping is one port map ready for rendering.
type mapping struct {
	listen int
	proto  string
	host   string // target host (IP literal or domain, without brackets)
	port   int    // target port
}

// keys are the validated key strings.
type keys struct {
	private, public, shortID, uuid string
}

// planned is everything Render needs after validation.
type planned struct {
	maps       []mapping
	keys       keys
	decoyHost  string // serverName / serverNames[0]
	decoyAddr  string // realitySettings.target ("host:443")
	listenAddr string // hub bind address of user ports
}

// plan validates in and prepares the render data.
func plan(in backend.RenderInput) (planned, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != Name || in.Transport.Name != Reality {
		return planned{}, fail("not an xray transport")
	}
	ports := portMaps(in)
	if len(ports) == 0 {
		return planned{}, fail("the tunnel has no port maps")
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return planned{}, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	if !filepath.IsAbs(in.Paths.Binary) {
		return planned{}, fail("the xray binary is not installed (no absolute binary path)")
	}
	if !filepath.IsAbs(in.Paths.ConfigDir) {
		return planned{}, fail("the rendered config directory is not an absolute path")
	}
	if !validHost(in.Node.PublicIP) {
		return planned{}, fail("the node public IP is unknown (the hub dials it)")
	}
	k, err := checkKeys(in.Secrets.Keys, fail)
	if err != nil {
		return planned{}, err
	}
	decoyHost, decoyAddr, err := decoy(in, fail)
	if err != nil {
		return planned{}, err
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
		if !ok || !validHost(host) {
			return planned{}, fail("invalid target '" + target + "'")
		}
		maps = append(maps, mapping{listen: pm.Listen, proto: proto, host: host, port: port})
	}
	return planned{maps: maps, keys: k, decoyHost: decoyHost, decoyAddr: decoyAddr, listenAddr: listenAddr(in)}, nil
}

// checkKeys verifies the generated key material (DEY-B006 when missing or
// malformed) including that the public key belongs to the private key.
func checkKeys(m map[string]string, fail func(string) error) (keys, error) {
	k := keys{private: m[KeyPrivate], public: m[KeyPublic], shortID: m[KeyShortID], uuid: m[KeyUUID]}
	if k.private == "" || k.public == "" || k.shortID == "" || k.uuid == "" {
		return keys{}, fail("the REALITY keys are missing (reality_private, reality_public, reality_short_id, uuid); regenerate the tunnel keys")
	}
	priv, err := base64.RawURLEncoding.DecodeString(k.private)
	if err != nil || len(priv) != 32 {
		return keys{}, fail("reality_private is not a base64url X25519 key")
	}
	pub, err := base64.RawURLEncoding.DecodeString(k.public)
	if err != nil || len(pub) != 32 {
		return keys{}, fail("reality_public is not a base64url X25519 key")
	}
	sk, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil || string(sk.PublicKey().Bytes()) != string(pub) {
		return keys{}, fail("reality_public does not belong to reality_private")
	}
	if len(k.shortID) > 16 || len(k.shortID)%2 != 0 {
		return keys{}, fail("reality_short_id must be an even number of hex characters (at most 16)")
	}
	if _, err := hex.DecodeString(k.shortID); err != nil {
		return keys{}, fail("reality_short_id is not hex")
	}
	if !validUUID(k.uuid) {
		return keys{}, fail("uuid is not a UUID")
	}
	return k, nil
}

// decoy returns the SNI and the REALITY target of the selected decoy. A
// decoy may carry an explicit port ("site.example:8443"); the default is 443.
func decoy(in backend.RenderInput, fail func(string) error) (host, addr string, err error) {
	d := backend.DecoyFor(in)
	if d == "" {
		return "", "", fail("no decoy SNI is selected (set hub decoy_snis; the hub picks the first reachable one)")
	}
	host, port := d, decoyPort
	if h, p, ok := backend.SplitTarget(d); ok && !strings.Contains(h, ":") {
		host, port = h, p
	}
	if net.ParseIP(host) != nil || !validDomain(host) {
		return "", "", fail("decoy '" + d + "' must be a domain name (it is sent as the TLS SNI)")
	}
	return host, backend.HostPort(host, port), nil
}

func validUUID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	return err == nil
}

// validHost accepts an IP literal or a DNS name.
func validHost(h string) bool {
	h = strings.TrimSpace(h)
	if h == "" {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	return validDomain(h)
}

// validDomain is a conservative host name check (letters, digits, '-', '.').
func validDomain(h string) bool {
	if h == "" || len(h) > 253 || strings.HasPrefix(h, ".") || strings.HasSuffix(h, "..") {
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

// portMaps returns the port maps rendered for in: every map, or only the
// first one for the canary unit.
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
