// Package rathole renders the rapiz1/rathole backend (spec section 7.2):
// the hub runs the rathole [server], which binds the tunnel's user ports,
// and each node runs the [client] that dials the hub's backend control port
// and forwards every service to its port-map target.
//
// Configuration keys follow the README, docs/transport.md and src/config.rs
// of the version pinned in backends.yaml (v0.5.0); docs/backends/rathole.md
// lists the differences to the spec sample. Rendering is pure and
// deterministic.
package rathole

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name used in transport ids ("rathole/noise").
const Name = "rathole"

// Rendered file names (relative to Paths.ConfigDir).
const (
	ServerFile = "server.toml" // hub side
	ClientFile = "client.toml" // node side
)

// Transport names of the pinned version (config type values).
const (
	TCP   = "tcp"
	TLS   = "tls"
	Noise = "noise"
)

// Keys produced by GenerateKeys and persisted per (tunnel, backend) in
// /etc/deyroute/secrets/backend-keys/<tunnel>/rathole.json.
const (
	// KeyNoisePrivate is the hub's X25519 static private key, standard
	// base64 of the raw 32 bytes (the encoding "rathole --genkey" prints and
	// [server.transport.noise] local_private_key expects).
	KeyNoisePrivate = "noise_private"
	// KeyNoisePublic is the matching X25519 public key, standard base64 of
	// the raw 32 bytes ([client.transport.noise] remote_public_key).
	KeyNoisePublic = "noise_public"
)

// NoisePattern is the Noise handshake used by rathole/noise: NK
// authenticates the server (hub) to the client, like TLS with a pinned key.
const NoisePattern = "Noise_NK_25519_ChaChaPoly_BLAKE2s"

// Tuning values rendered into every config.
const (
	// heartbeatInterval is the server's application heartbeat (spec: 20s).
	heartbeatInterval = 20
	// heartbeatTimeout must be greater than heartbeatInterval (v0.5.0
	// README); twice the interval tolerates one lost heartbeat.
	heartbeatTimeout = 40
	// retryInterval is the client reconnect interval in seconds (spec: 3).
	retryInterval = 3
)

// spec describes one rathole transport.
type spec struct {
	name    string
	stealth int
	tls     bool // consumes the tunnel PKCS#12 bundle and CA
	noise   bool // consumes the generated Noise keys
}

// specs lists the transports of section 7.2. Stealth (section 7 table
// gives noise:3): tls is a standard TLS session (OpenSSL via native-tls)
// and hides the rathole protocol as well as Noise does, so it is 3 too;
// tcp carries the rathole handshake and the user bytes unencrypted and is
// trivially fingerprinted, so it is 1 like backhaul/tcp and direct/native.
var specs = []spec{
	{name: Noise, stealth: 3, noise: true},
	{name: TLS, stealth: 3, tls: true},
	{name: TCP, stealth: 1},
}

func lookupSpec(name string) (spec, bool) {
	for _, s := range specs {
		if s.name == name {
			return s, true
		}
	}
	return spec{}, false
}

// Backend implements backend.Backend and backend.KeyGenerator for rathole.
type Backend struct {
	// rand is the entropy source of GenerateKeys (crypto/rand by default;
	// tests inject failures).
	rand io.Reader
}

// New returns the rathole backend.
func New() *Backend { return &Backend{rand: rand.Reader} }

func init() { backend.Register(New()) }

// Name returns "rathole".
func (*Backend) Name() string { return Name }

// Transports returns every rathole transport. All are Reverse (the node
// dials the hub) and carry tcp and udp port maps over one TCP connection,
// so none needs UDP between hub and node.
func (*Backend) Transports() []backend.Transport {
	out := make([]backend.Transport, 0, len(specs))
	for _, s := range specs {
		out = append(out, backend.Transport{
			Backend:   Name,
			Name:      s.name,
			Direction: backend.Reverse,
			Protos:    []string{config.ProtoTCP, config.ProtoUDP},
			NeedsTLS:  s.tls,
			Stealth:   s.stealth,
		})
	}
	return out
}

// Manifest returns the pinned rathole release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no rathole specific check (rathole exposes no status API).
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// GenerateKeys creates the per-tunnel Noise keypair in pure Go. The keys
// are generated for every rathole transport so that switching a tunnel to
// rathole/noise never needs new key material.
func (b *Backend) GenerateKeys(backend.Transport) (map[string]string, error) {
	r := b.rand
	if r == nil {
		r = rand.Reader
	}
	priv, err := ecdh.X25519().GenerateKey(r)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": Name})
	}
	return map[string]string{
		KeyNoisePrivate: base64.StdEncoding.EncodeToString(priv.Bytes()),
		KeyNoisePublic:  base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()),
	}, nil
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, _, err := b.plan(in)
	return err
}

// Render renders the hub ([server]) or node ([client]) side. It is pure.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	sp, svcs, err := b.plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderClient(in, sp, svcs), nil
	}
	return renderServer(in, sp, svcs), nil
}

// service is one rathole service: one port map.
type service struct {
	name   string // "tcp-443"
	proto  string // "tcp" | "udp"
	listen int
	target string
}

// plan validates in and computes the services to render.
func (b *Backend) plan(in backend.RenderInput) (spec, []service, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	sp, ok := lookupSpec(in.Transport.Name)
	if in.Transport.Backend != Name || !ok {
		return spec{}, nil, fail("not a rathole transport")
	}
	ports := portMaps(in)
	if len(ports) == 0 {
		return spec{}, nil, fail("the tunnel has no port maps")
	}
	for _, p := range ports {
		if proto := protoOf(p); proto != config.ProtoTCP && proto != config.ProtoUDP {
			return spec{}, nil, deyerr.New(deyerr.B010, deyerr.Params{"transport": id, "proto": proto})
		}
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return spec{}, nil, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	if in.Secrets.Token == "" {
		return spec{}, nil, fail("the tunnel token is missing")
	}
	if hubHost(in) == "" {
		return spec{}, nil, fail("the hub public IP is unknown (the node client dials it)")
	}
	if !filepath.IsAbs(in.Paths.Binary) {
		return spec{}, nil, fail("the rathole binary is not installed (no absolute binary path)")
	}
	if !filepath.IsAbs(in.Paths.ConfigDir) {
		return spec{}, nil, fail("the rendered config directory is not an absolute path")
	}
	if sp.tls {
		if err := checkTLS(in, fail); err != nil {
			return spec{}, nil, err
		}
	}
	if sp.noise {
		if err := checkNoiseKeys(in.Secrets.Keys, fail); err != nil {
			return spec{}, nil, err
		}
	}
	svcs, err := buildServices(ports, fail)
	if err != nil {
		return spec{}, nil, err
	}
	return sp, svcs, nil
}

// checkTLS verifies the material rathole/tls needs: the PKCS#12 bundle
// and its password on the server, the CA certificate on the client.
func checkTLS(in backend.RenderInput, fail func(string) error) error {
	s := in.Secrets
	switch {
	case !filepath.IsAbs(s.TLSP12File):
		return fail("the tunnel PKCS#12 bundle (tls.p12) is missing")
	case s.TLSP12Password == "":
		// OpenSSL treats an empty and an absent PKCS#12 password
		// differently; deyroute always protects the bundle.
		return fail("the tunnel PKCS#12 password is missing")
	case !filepath.IsAbs(trustedRoot(in)):
		return fail("the CA certificate the node verifies the hub with is missing")
	}
	return nil
}

// trustedRoot returns the [client.transport.tls] trusted_root file.
// rathole v0.5.0 (native-tls on OpenSSL) adds the first certificate of that
// file to the system trust store, it does not replace it:
//
//   - tls.mode auto: the internal CA that issued the tunnel certificate.
//   - tls.mode acme: the internal CA as well; the publicly trusted ACME
//     certificate verifies through the system store, and a fallback to an
//     internal certificate (ACME failure) still verifies through the CA.
//   - tls.mode custom: the owner's certificate itself (first certificate of
//     the tunnel cert file). A self-signed custom certificate then verifies
//     as its own anchor; one from a public CA verifies through the system
//     store. The internal CA never issued a custom certificate.
func trustedRoot(in backend.RenderInput) string {
	if in.Tunnel.TLS.Mode == config.TLSModeCustom {
		return in.Secrets.TLSCertFile
	}
	return in.Secrets.CAFile
}

// checkNoiseKeys verifies that both Noise keys exist, decode to 32 bytes and
// belong together (a mismatch would fail every handshake at runtime).
func checkNoiseKeys(keys map[string]string, fail func(string) error) error {
	privB64, pubB64 := keys[KeyNoisePrivate], keys[KeyNoisePublic]
	if privB64 == "" || pubB64 == "" {
		return fail("the Noise keypair (" + KeyNoisePrivate + ", " + KeyNoisePublic + ") has not been generated")
	}
	priv, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil || len(priv) != 32 {
		return fail("the Noise private key is not 32 bytes of standard base64")
	}
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != 32 {
		return fail("the Noise public key is not 32 bytes of standard base64")
	}
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		return fail("the Noise private key is not a valid X25519 key")
	}
	if string(key.PublicKey().Bytes()) != string(pub) {
		return fail("the Noise public key does not belong to the private key")
	}
	return nil
}

// buildServices turns port maps into rathole services named
// "<proto>-<listen>" (spec 7.2), rejecting duplicates and bad targets.
func buildServices(ports []config.PortMap, fail func(string) error) ([]service, error) {
	out := make([]service, 0, len(ports))
	seen := map[string]bool{}
	for _, p := range ports {
		if p.Listen < 1 || p.Listen > 65535 {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + " is out of range")
		}
		target := targetOf(p)
		if _, _, ok := backend.SplitTarget(target); !ok || strings.ContainsAny(target, "\" \t\r\n") {
			return nil, fail("invalid target '" + target + "'")
		}
		proto := protoOf(p)
		name := backend.ServiceName(proto, p.Listen)
		if seen[name] {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + "/" + proto + " is listed twice")
		}
		seen[name] = true
		out = append(out, service{name: name, proto: proto, listen: p.Listen, target: target})
	}
	return out, nil
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

// listenAddr is the hub address user ports bind to. The canary unit never
// binds a public address.
func listenAddr(in backend.RenderInput) string {
	addr := in.ListenAddrOrDefault()
	if in.Canary && !isLoopback(addr) {
		return "127.0.0.1"
	}
	return addr
}

// hubHost is the hub address the node client dials, trimmed and without
// IPv6 brackets (backend.HostPort adds them back).
func hubHost(in backend.RenderInput) string {
	return strings.Trim(strings.TrimSpace(in.Hub.PublicIP), "[]")
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
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
