// Package frp renders the fatedier/frp backend (spec section 7.3): the hub
// runs frps, which binds the tunnel's user ports as frp "remote ports", and
// each node runs frpc, which dials the hub's backend control port over the
// selected transport protocol and forwards every proxy to its port-map
// target.
//
// Configuration keys follow the TOML reference of the version pinned in
// backends.yaml (v0.71.0: conf/frps_full_example.toml,
// conf/frpc_full_example.toml and pkg/config/v1); docs/backends/frp.md lists
// the differences to the spec sample. Rendering is pure and deterministic.
package frp

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name used in transport ids ("frp/wss").
const Name = "frp"

// Rendered file names (relative to Paths.ConfigDir).
const (
	ServerFile = "frps.toml" // hub side
	ClientFile = "frpc.toml" // node side
)

// Binaries of one frp release (both live in Paths.BinDir).
const (
	ServerBinary = "frps"
	ClientBinary = "frpc"
)

// Transport names; each is also the frpc transport.protocol value.
const (
	WSS       = "wss"
	WebSocket = "websocket"
	QUIC      = "quic"
	KCP       = "kcp"
	TCP       = "tcp"
)

// Tuning values (spec section 7.3).
const (
	// maxPoolCount is frps transport.maxPoolCount; frps caps every
	// client's poolCount to it.
	maxPoolCount = 32
	logLevel     = "info"
)

// udpBind names the frps key that opens a UDP listener on the control port.
type udpBind string

const (
	noUDP        udpBind = ""
	quicBindPort udpBind = "quicBindPort"
	kcpBindPort  udpBind = "kcpBindPort"
)

// spec describes one frp transport.
type spec struct {
	name    string
	stealth int
	udp     udpBind // UDP listener on the hub control port (quic/kcp)
	// unavailable is non-empty when the pinned frps cannot serve the
	// transport on its own; Validate reports it as DEY-B006.
	unavailable string
}

// wssUnavailable explains why frp/wss is rejected. frpc v0.71.0 sends
// TLS first and the WebSocket upgrade inside it, but frps only accepts
// plain "GET /~!frp" WebSocket upgrades and serves yamux directly on TLS
// connections; upstream's own e2e test (test/e2e/v1/basic/client_server.go,
// "frps only supports ws, so there should be a proxy to terminate TLS before
// frps") puts a separate TLS terminator in front. A deyroute unit runs exactly
// one process, so there is no such terminator.
const wssUnavailable = "frps v0.71.0 cannot accept wss connections itself (upstream requires a separate TLS-terminating proxy in front of frps); use frp/tcp, which is a TLS session on the wire as well"

// specs lists the transports of section 7.3. Stealth follows the section 7
// table (wss:4, quic:3); for the others it reflects what an observer sees,
// given that deyroute always enables frp TLS: tcp is one standard TLS
// session (no frp 0x17 marker byte) = 3; websocket sends a plaintext HTTP
// upgrade to frp's fixed path "/~!frp" before TLS = 2; kcp is KCP framing
// over UDP with plaintext headers = 2.
var specs = []spec{
	{name: WSS, stealth: 4, unavailable: wssUnavailable},
	{name: WebSocket, stealth: 2},
	{name: QUIC, stealth: 3, udp: quicBindPort},
	{name: KCP, stealth: 2, udp: kcpBindPort},
	{name: TCP, stealth: 3},
}

func lookupSpec(name string) (spec, bool) {
	for _, s := range specs {
		if s.name == name {
			return s, true
		}
	}
	return spec{}, false
}

// Backend implements backend.Backend for frp.
type Backend struct{}

// New returns the frp backend.
func New() *Backend { return &Backend{} }

func init() { backend.Register(New()) }

// Name returns "frp".
func (*Backend) Name() string { return Name }

// Transports returns every frp transport. All are Reverse and carry tcp
// and udp proxies; every one consumes the tunnel TLS certificate (TLS is
// mandatory, spec 7.3); quic and kcp run over UDP between node and hub.
func (*Backend) Transports() []backend.Transport {
	out := make([]backend.Transport, 0, len(specs))
	for _, s := range specs {
		out = append(out, backend.Transport{
			Backend:   Name,
			Name:      s.name,
			Direction: backend.Reverse,
			Protos:    []string{config.ProtoTCP, config.ProtoUDP},
			NeedsUDP:  s.udp != noUDP,
			NeedsTLS:  true,
			Stealth:   s.stealth,
		})
	}
	return out
}

// Manifest returns the pinned frp release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no frp specific check: the dashboard and admin APIs stay
// disabled so that nothing but the control port is exposed.
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, _, err := b.plan(in)
	return err
}

// Render renders the hub (frps) or node (frpc) side. It is pure.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	sp, proxies, err := b.plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderClient(in, sp, proxies), nil
	}
	return renderServer(in, sp, proxies), nil
}

// proxy is one frpc [[proxies]] entry: one port map.
type proxy struct {
	name      string // "tcp-443"
	proto     string // "tcp" | "udp"
	listen    int    // frps remotePort
	localIP   string
	localPort int
}

// plan validates in and computes the proxies to render.
func (b *Backend) plan(in backend.RenderInput) (spec, []proxy, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	sp, ok := lookupSpec(in.Transport.Name)
	if in.Transport.Backend != Name || !ok {
		return spec{}, nil, fail("not an frp transport")
	}
	if sp.unavailable != "" {
		return spec{}, nil, fail(sp.unavailable)
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
	s := in.Secrets
	switch {
	case s.Token == "":
		return spec{}, nil, fail("the tunnel token is missing")
	case hubHost(in) == "":
		return spec{}, nil, fail("the hub public IP is unknown (the node client dials it)")
	case !filepath.IsAbs(s.TLSCertFile) || !filepath.IsAbs(s.TLSKeyFile):
		return spec{}, nil, fail("the tunnel TLS certificate or key is missing (frp TLS is mandatory)")
	case !filepath.IsAbs(trustAnchor(in)):
		return spec{}, nil, fail("the CA certificate the node verifies the hub with is missing")
	case !filepath.IsAbs(in.Paths.BinDir):
		return spec{}, nil, fail("the frp binaries are not installed (no absolute binary directory)")
	case !filepath.IsAbs(in.Paths.ConfigDir):
		return spec{}, nil, fail("the rendered config directory is not an absolute path")
	}
	// frp v0.71.0 renders every config file through Go text/template
	// before parsing it, so "{{" in any value would be executed.
	for _, v := range []string{s.Token, s.TLSCertFile, s.TLSKeyFile, s.CAFile, s.ServerName, in.Hub.PublicIP, in.ListenAddr} {
		if strings.Contains(v, "{{") || strings.Contains(v, "}}") {
			return spec{}, nil, fail("a rendered value contains '{{' or '}}', which frp's config template engine would execute")
		}
	}
	if adv := in.Tunnel.Advanced; adv != nil && adv.ConnectionPool < 0 {
		// v0.71.0 release notes: negative pool counts are rejected.
		return spec{}, nil, fail("advanced.connection_pool must not be negative")
	}
	proxies, err := buildProxies(ports, fail)
	if err != nil {
		return spec{}, nil, err
	}
	return sp, proxies, nil
}

// buildProxies turns port maps into frpc proxies named "<proto>-<listen>"
// (the same names as the other backends), rejecting duplicates and bad
// targets.
func buildProxies(ports []config.PortMap, fail func(string) error) ([]proxy, error) {
	out := make([]proxy, 0, len(ports))
	seen := map[string]bool{}
	for _, p := range ports {
		if p.Listen < 1 || p.Listen > 65535 {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + " is out of range")
		}
		target := targetOf(p)
		host, port, ok := backend.SplitTarget(target)
		if !ok || host == "" || strings.ContainsAny(host, "\"\\ \t\r\n{}") {
			return nil, fail("invalid target '" + target + "'")
		}
		proto := protoOf(p)
		name := backend.ServiceName(proto, p.Listen)
		if seen[name] {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + "/" + proto + " is listed twice")
		}
		seen[name] = true
		out = append(out, proxy{name: name, proto: proto, listen: p.Listen, localIP: host, localPort: port})
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

// listenAddr is the hub address user ports bind to (frps proxyBindAddr).
// The canary unit never binds a public address.
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

// trustAnchor returns the file frpc verifies the hub certificate against
// (transport.tls.trustedCaFile). frpc builds its root pool from this file
// alone, without the system roots (pkg/transport/tls.go).
//
//   - tls.mode auto: the internal CA that issued the tunnel certificate
//     (spec 7.3: frpc is pinned to our CA).
//   - tls.mode acme/custom: the certificate is issued by a public or
//     foreign CA, so the internal CA would reject it and every frp rung
//     would fail. The served chain itself (the tunnel cert file, copied to
//     the node by the planner) is the trust anchor: Go's verifier accepts a
//     leaf that is itself in the root pool, so this pins exactly the
//     certificate the hub presents, still with host name verification. It
//     also keeps working when ACME falls back to an internal certificate.
func trustAnchor(in backend.RenderInput) string {
	switch in.Tunnel.TLS.Mode {
	case config.TLSModeACME, config.TLSModeCustom:
		return in.Secrets.TLSCertFile
	}
	return in.Secrets.CAFile
}

// poolCount is advanced.connection_pool, or without it the default of the
// backend tier (8/16/32 for small/medium/large; 8 without a tier, spec
// 7.3), capped at the server's transport.maxPoolCount. The pooled
// connections are held by frpc and frps alike, so the smaller tier of the
// two sides applies.
func poolCount(in backend.RenderInput) int {
	tier := backend.SmallerTier(in.Tier(backend.SideHub), in.Tier(backend.SideNode))
	n := backend.ByTier(tier, config.DefaultConnectionPool, 8, 16, 32)
	if adv := in.Tunnel.Advanced; adv != nil && adv.ConnectionPool > 0 {
		n = adv.ConnectionPool
	}
	if n > maxPoolCount {
		n = maxPoolCount
	}
	return n
}
