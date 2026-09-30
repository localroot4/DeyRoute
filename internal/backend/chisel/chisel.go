// Package chisel renders the optional jpillora/chisel backend (spec section
// 7.9): transport chisel/wss. The hub runs "chisel server --reverse" with
// TLS on the backend control port; the node runs "chisel client" dialing
// https://<hub>:<ctl> with one reverse remote per port map
// ("R:<ListenAddr>:<listen>:<target host>:<target port>[/udp]"), so the hub
// server binds the user ports and forwards their connections through the
// SSH-over-WebSocket session to the node, which dials the target.
//
// Flags and file formats follow the README and sources of the pinned
// release (v1.12.1); docs/backends/chisel.md lists the differences to the
// spec sample. Rendering is pure and deterministic.
package chisel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name ("chisel/wss").
const Name = "chisel"

// WSS is the only transport: SSH over WebSocket over TLS.
const WSS = "wss"

// Rendered file names on the hub (relative to ConfigDir).
const (
	UsersFile = "users.json" // --authfile
	KeyFile   = "server.key" // --keyfile (SSH host key)
)

// User is the chisel user name of every tunnel.
const User = "dey"

// Keys produced by GenerateKeys and persisted per (tunnel, backend) in
// /etc/deyroute/secrets/backend-keys/<tunnel>/chisel.json.
const (
	// KeyServerKey is the hub's SSH host key: an ECDSA P-256 private key,
	// PEM "EC PRIVATE KEY" (the format `chisel server --keygen` writes and
	// --keyfile reads). It is written to server.key on the hub only.
	KeyServerKey = "server_key"
	// KeyFingerprint is the host key fingerprint the node pins with
	// --fingerprint: standard base64 of SHA-256 over the SSH wire-format
	// public key (chisel share/ccrypto.FingerprintKey), 44 characters.
	KeyFingerprint = "fingerprint"
)

// Tuning values.
const (
	// keepalive is the SSH keepalive of both sides (chisel's default).
	keepalive = "25s"
	// maxRetryInterval caps the client's reconnect backoff (chisel's
	// default of 5 minutes would keep a restarted hub unreachable for
	// longer than a failover cycle).
	maxRetryInterval = "10s"
	// authLabel derives the chisel password from the tunnel token.
	authLabel = "deyroute/chisel/auth"
)

// Backend implements backend.Backend and backend.KeyGenerator for chisel.
type Backend struct {
	// rand is the entropy source of GenerateKeys (crypto/rand by default).
	rand io.Reader
}

// New returns the chisel backend.
func New() *Backend { return &Backend{rand: rand.Reader} }

func init() { backend.Register(New()) }

// Name returns "chisel".
func (*Backend) Name() string { return Name }

// Transports returns chisel/wss: Reverse, tcp+udp over one TLS WebSocket
// connection (no UDP between the servers), tunnel TLS, stealth 3 (section 7
// table), optional (never in the default ladder, section 7.9).
func (*Backend) Transports() []backend.Transport {
	return []backend.Transport{{
		Backend:   Name,
		Name:      WSS,
		Direction: backend.Reverse,
		Protos:    []string{config.ProtoTCP, config.ProtoUDP},
		NeedsTLS:  true,
		Stealth:   3,
		Optional:  true,
	}}
}

// Manifest returns the pinned chisel release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no chisel-specific check.
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// GenerateKeys creates the hub's SSH host key and its fingerprint (pure Go,
// crypto/rand), so the node can pin the server instead of trusting the
// first key it sees.
func (b *Backend) GenerateKeys(backend.Transport) (map[string]string, error) {
	r := b.rand
	if r == nil {
		r = rand.Reader
	}
	fail := func(err error) error { return deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": Name}) }
	k, err := ecdsa.GenerateKey(elliptic.P256(), r)
	if err != nil {
		return nil, fail(err)
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, fail(err)
	}
	fp, err := fingerprint(&k.PublicKey)
	if err != nil {
		return nil, fail(err)
	}
	return map[string]string{
		KeyServerKey:   string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})),
		KeyFingerprint: fp,
	}, nil
}

// fingerprint is chisel's host key fingerprint of pub.
func fingerprint(pub *ecdsa.PublicKey) (string, error) {
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(sp.Marshal())
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

// checkServerKey verifies that keyPEM is an ECDSA P-256 key whose
// fingerprint is fp.
func checkServerKey(keyPEM, fp string) error {
	blk, _ := pem.Decode([]byte(keyPEM))
	if blk == nil || blk.Type != "EC PRIVATE KEY" {
		return errors.New("generated key " + KeyServerKey + " is missing or not a PEM EC private key (run the key generation again)")
	}
	k, err := x509.ParseECPrivateKey(blk.Bytes)
	if err != nil || k.Curve != elliptic.P256() {
		return errors.New("generated key " + KeyServerKey + " is not an ECDSA P-256 key")
	}
	got, err := fingerprint(&k.PublicKey)
	if err != nil || got != fp {
		return errors.New("generated key " + KeyFingerprint + " does not match " + KeyServerKey)
	}
	return nil
}

// Password returns the chisel password of a tunnel: hex(HMAC-SHA256(token,
// "deyroute/chisel/auth"))[:32]. The node passes it in the AUTH environment
// variable (chisel reads credentials only from --auth or AUTH), which local
// users can see with `systemctl show`; deriving it keeps the tunnel token
// itself, shared with the other backends of the tunnel, out of the unit.
func Password(token string) string {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(authLabel))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := plan(in)
	return err
}

// Render renders the hub (chisel server) or node (chisel client) side.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	maps, err := plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderNode(in, maps), nil
	}
	return renderHub(in, maps)
}

// portMap is one validated port map.
type portMap struct {
	proto      string
	listen     int
	targetHost string // bracketed for IPv6
	targetPort int
}

// plan validates in and returns the port maps to render.
func plan(in backend.RenderInput) ([]portMap, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != Name || in.Transport.Name != WSS {
		return nil, fail("not a chisel transport")
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return nil, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	s := in.Secrets
	switch {
	case s.Token == "":
		return nil, fail("the tunnel token is missing")
	case strings.TrimSpace(in.Hub.PublicIP) == "" || strings.ContainsAny(in.Hub.PublicIP, "/?#@ "):
		return nil, fail("the hub public IP is unknown or invalid (the node dials it)")
	case !filepath.IsAbs(s.TLSCertFile) || !filepath.IsAbs(s.TLSKeyFile):
		return nil, fail("the tunnel TLS certificate or key is missing (wss needs TLS)")
	case !filepath.IsAbs(s.CAFile):
		return nil, fail("the CA certificate the node verifies the hub with is missing")
	case !filepath.IsAbs(in.Paths.Binary):
		return nil, fail("chisel is not installed (no absolute binary path)")
	case !filepath.IsAbs(in.Paths.ConfigDir):
		return nil, fail("the rendered config directory is not an absolute path")
	}
	if err := checkServerKey(s.Keys[KeyServerKey], s.Keys[KeyFingerprint]); err != nil {
		return nil, fail(err.Error())
	}
	ports := in.Tunnel.Ports
	if in.Canary && len(ports) > 1 {
		ports = ports[:1]
	}
	if len(ports) == 0 {
		return nil, fail("the tunnel has no port maps")
	}
	out := make([]portMap, 0, len(ports))
	seen := map[string]bool{}
	for _, p := range ports {
		proto := p.Proto
		if proto == "" {
			proto = config.ProtoTCP
		}
		if proto != config.ProtoTCP && proto != config.ProtoUDP {
			return nil, deyerr.New(deyerr.B010, deyerr.Params{"transport": id, "proto": proto})
		}
		if p.Listen < 1 || p.Listen > 65535 {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + " is out of range")
		}
		key := proto + "/" + strconv.Itoa(p.Listen)
		if seen[key] {
			return nil, fail("listen port " + key + " is listed twice")
		}
		seen[key] = true
		target := p.Target
		if target == "" {
			target = backend.HostPort("127.0.0.1", p.Listen)
		}
		host, port, ok := backend.SplitTarget(target)
		// chisel splits remotes at ':' outside brackets and '/' starts the
		// protocol suffix, so hosts must not contain either.
		if !ok || host == "" || strings.ContainsAny(host, "/[] \t\r\n") || strings.EqualFold(host, "socks") || strings.EqualFold(host, "stdio") {
			return nil, fail("invalid target '" + target + "'")
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		out = append(out, portMap{proto: proto, listen: p.Listen, targetHost: host, targetPort: port})
	}
	return out, nil
}
