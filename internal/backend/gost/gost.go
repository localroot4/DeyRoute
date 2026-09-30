// Package gost renders the optional go-gost/gost v3 backend (spec section
// 7.9): transport gost/relay-wss. The hub runs a relay service over
// WebSocket-in-TLS on the backend control port with BIND enabled; the node
// runs one remote port forwarding service per port map (rtcp:// for tcp,
// rudp:// for udp) whose listener asks the hub, through a relay+wss chain,
// to bind the user port on the hub and forwards every accepted connection
// to the port map's target.
//
// Both sides use a gost.yaml config file (-C) instead of -L/-F URLs, so the
// tunnel token never appears in argv. Keys follow the config schema of the
// pinned release (gost v3.2.6 → github.com/go-gost/x v0.8.1 config
// package); docs/backends/gost.md lists the differences to the spec sample.
// Rendering is pure and deterministic.
package gost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name ("gost/relay-wss").
const Name = "gost"

// RelayWSS is the only transport.
const RelayWSS = "relay-wss"

// ConfigFile is the rendered file on both sides (relative to ConfigDir).
const ConfigFile = "gost.yaml"

// User is the relay user name; the password is the tunnel token.
const User = "dey"

// Tuning values.
const (
	// udpTTL is the rudp idle timeout: gost v3's default of 5s drops UDP
	// sessions that pause briefly (games, QUIC), so it is raised.
	udpTTL = "60s"
	// maxAuthLen is the longest user/password the relay protocol carries
	// (one length byte, github.com/go-gost/relay).
	maxAuthLen = 255
	// denyConnect is the bypass of TCP-only tunnels: it refuses every relay
	// CONNECT on the hub (nodes only need BIND), so a token holder cannot
	// use the hub as a proxy.
	denyConnect = "deny-connect"
	// denyLocal is the bypass of tunnels with UDP port maps. gost v3.2.6
	// applies the service bypass not only to CONNECT but also to every
	// datagram of a UDP BIND (x v0.8.1 handler/relay/bind.go →
	// internal/net/udp.Relay, matched against the user client's address),
	// so the deny-all whitelist would drop all UDP traffic. It refuses
	// CONNECT to unspecified and link-local addresses (e.g. the cloud
	// metadata service 169.254.169.254), which are never UDP clients.
	// Loopback stays allowed: the hub's own path probe and the canary are
	// loopback UDP clients.
	denyLocal  = "deny-local"
	hubService = "relay-wss"
	hubChain   = "hub"
)

// localMatchers are the addresses denyLocal refuses.
var localMatchers = []string{"0.0.0.0/8", "169.254.0.0/16", "::/128", "fe80::/10"}

// Backend implements backend.Backend for gost.
type Backend struct{}

// New returns the gost backend.
func New() *Backend { return &Backend{} }

func init() { backend.Register(New()) }

// Name returns "gost".
func (*Backend) Name() string { return Name }

// Transports returns gost/relay-wss: Reverse, tcp+udp over one TLS
// WebSocket connection (no UDP between the servers), tunnel TLS, stealth 3
// (section 7 table), optional (never in the default ladder, section 7.9).
func (*Backend) Transports() []backend.Transport {
	return []backend.Transport{{
		Backend:   Name,
		Name:      RelayWSS,
		Direction: backend.Reverse,
		Protos:    []string{config.ProtoTCP, config.ProtoUDP},
		NeedsTLS:  true,
		Stealth:   3,
		Optional:  true,
	}}
}

// Manifest returns the pinned gost release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no gost-specific check (the API and metrics services stay off).
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := plan(in)
	return err
}

// Render renders the hub (relay server) or node (remote forwards) side.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	maps, err := plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderNode(in, maps)
	}
	return renderHub(in, maps)
}

// portMap is one validated port map.
type portMap struct {
	name   string // "tcp-443"
	proto  string
	listen int
	target string // host:port
}

// plan validates in and returns the port maps to render.
func plan(in backend.RenderInput) ([]portMap, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != Name || in.Transport.Name != RelayWSS {
		return nil, fail("not a gost transport")
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return nil, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	s := in.Secrets
	switch {
	case s.Token == "":
		return nil, fail("the tunnel token is missing")
	case len(s.Token) > maxAuthLen:
		return nil, fail("the tunnel token is longer than the relay protocol allows (255 bytes)")
	case strings.TrimSpace(in.Hub.PublicIP) == "":
		return nil, fail("the hub public IP is unknown (the node dials it)")
	case !filepath.IsAbs(s.TLSCertFile) || !filepath.IsAbs(s.TLSKeyFile):
		return nil, fail("the tunnel TLS certificate or key is missing (wss needs TLS)")
	case !filepath.IsAbs(s.CAFile):
		return nil, fail("the CA certificate the node verifies the hub with is missing")
	case !filepath.IsAbs(in.Paths.Binary):
		return nil, fail("gost is not installed (no absolute binary path)")
	case !filepath.IsAbs(in.Paths.ConfigDir):
		return nil, fail("the rendered config directory is not an absolute path")
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
		name := backend.ServiceName(proto, p.Listen)
		if seen[name] {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + "/" + proto + " is listed twice")
		}
		seen[name] = true
		target := p.Target
		if target == "" {
			target = backend.HostPort("127.0.0.1", p.Listen)
		}
		host, port, ok := backend.SplitTarget(target)
		if !ok || host == "" || strings.ContainsAny(host, " \t\r\n/?#@,") {
			return nil, fail("invalid target '" + target + "'")
		}
		out = append(out, portMap{name: name, proto: proto, listen: p.Listen, target: backend.HostPort(host, port)})
	}
	return out, nil
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
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

// wsPath is the WebSocket path both sides use: derived from the token so
// it is stable, differs per tunnel and is not gost's well-known "/ws" (an
// active prober without the token cannot find the endpoint).
func wsPath(token string) string {
	sum := sha256.Sum256([]byte("deyroute/gost/ws-path\x00" + token))
	return "/" + hex.EncodeToString(sum[:8])
}
