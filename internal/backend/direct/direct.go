// Package direct implements the "direct" backend (spec section 7.8): the
// unobfuscated last rung of every ladder.
//
//   - direct/native is a TCP/UDP relay built into deyroute itself ("deyroute
//     relay --tunnel <id>" in its own unit on each side). The hub half binds
//     the tunnel's user ports and forwards every connection/datagram to the
//     node half on <node_ip>:<ctl>, authenticated with the tunnel token; the
//     node half forwards only to the tunnel's configured targets (spec
//     section 11: the node must not become an open proxy). Its data plane is
//     RunRelay in this package.
//   - direct/haproxy (optional, phase 7) uses the distribution's HAProxy on
//     the hub in "mode tcp" and connects straight to <node_ip>:<target port>,
//     optionally with the PROXY protocol so the node service sees the real
//     client IP (spec section 10).
//
// Rendering is pure and deterministic.
package direct

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name ("direct/native", "direct/haproxy").
const Name = "direct"

// Transport names.
const (
	Native  = "native"
	HAProxy = "haproxy"
)

// HAProxyFile is the HAProxy configuration rendered on the hub.
const HAProxyFile = "haproxy.cfg"

// Backend implements backend.Backend for the direct transports.
type Backend struct{}

// New returns the direct backend.
func New() *Backend { return &Backend{} }

func init() { backend.Register(New()) }

// Name returns "direct".
func (*Backend) Name() string { return Name }

// Transports returns direct/native and direct/haproxy (spec section 7
// comparison table; section 9: direct/native is never quarantined).
func (*Backend) Transports() []backend.Transport {
	return []backend.Transport{
		{
			Backend: Name, Name: Native, Direction: backend.Forward,
			Protos: []string{config.ProtoTCP, config.ProtoUDP}, Stealth: 1,
			NeverQuarantine: true,
		},
		{
			Backend: Name, Name: HAProxy, Direction: backend.Forward,
			Protos: []string{config.ProtoTCP}, Stealth: 1,
			// PROXY protocol capable (send-proxy with advanced.proxy_protocol).
			ClientIPPreserved: true,
			Optional:          true,
		},
	}
}

// Manifest returns the manifest entry of the backend (built in; HAProxy is
// the distribution package, see QUESTIONS.md C.21).
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no backend specific check.
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// Validate reports impossible combinations (DEY-B006, DEY-B010). It checks
// the hub-side requirements (for direct/haproxy: the hub's HAProxy binary
// in Paths.Binary) plus everything both sides share.
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := plan(in, backend.SideHub)
	return err
}

// Render renders one side of direct/native or direct/haproxy.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	ports, err := plan(in, side)
	if err != nil {
		return backend.Rendered{}, err
	}
	switch in.Transport.Name {
	case HAProxy:
		if side == backend.SideNode {
			return renderCheck(in, ports)
		}
		return renderHAProxy(in, ports), nil
	default:
		return renderNative(in, ports, side)
	}
}

// portMap is one validated port map with its tunnel index.
type portMap struct {
	index  int
	listen int
	proto  string
	target string
	host   string // target host
	port   int    // target port
}

// plan validates in for side and returns the port maps to render.
func plan(in backend.RenderInput, side backend.Side) ([]portMap, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != Name || (in.Transport.Name != Native && in.Transport.Name != HAProxy) {
		return nil, fail("not a direct transport")
	}
	haproxy := in.Transport.Name == HAProxy
	protos := []string{config.ProtoTCP, config.ProtoUDP}
	if haproxy {
		protos = []string{config.ProtoTCP}
	}

	src := in.Tunnel.Ports
	if in.Canary && len(src) > 1 {
		src = src[:1]
	}
	if len(src) == 0 {
		return nil, fail("the tunnel has no port maps")
	}
	ports := make([]portMap, 0, len(src))
	seen := map[string]bool{}
	for i, p := range src {
		proto := p.Proto
		if proto == "" {
			proto = config.ProtoTCP
		}
		if proto != protos[0] && (len(protos) < 2 || proto != protos[1]) {
			return nil, deyerr.New(deyerr.B010, deyerr.Params{"transport": id, "proto": proto})
		}
		if p.Listen < 1 || p.Listen > 65535 {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + " is out of range")
		}
		key := strconv.Itoa(p.Listen) + "/" + proto
		if seen[key] {
			return nil, fail("listen port " + key + " is listed twice")
		}
		seen[key] = true
		target := p.Target
		if target == "" {
			target = backend.HostPort("127.0.0.1", p.Listen)
		}
		host, port, ok := backend.SplitTarget(target)
		if !ok || host == "" || strings.ContainsAny(host, " \t\r\n\"") {
			return nil, fail("invalid target '" + target + "'")
		}
		ports = append(ports, portMap{index: i, listen: p.Listen, proto: proto, target: target, host: host, port: port})
	}

	nodeIP := strings.TrimSpace(in.Node.PublicIP)
	if net.ParseIP(nodeIP) == nil {
		return nil, fail("the node public IP '" + in.Node.PublicIP + "' is not an IP address (the hub dials it)")
	}
	if !filepath.IsAbs(in.Paths.ConfigDir) {
		return nil, fail("the rendered config directory is not an absolute path")
	}

	if haproxy {
		if side == backend.SideHub && !filepath.IsAbs(in.Paths.Binary) {
			return nil, fail("haproxy is not installed on the hub (install the distribution package, e.g. apt install haproxy)")
		}
		if side == backend.SideNode && !filepath.IsAbs(in.Paths.SelfBinary) {
			return nil, fail("the deyroute binary path is missing")
		}
		for _, p := range ports {
			if !reachableFromHub(p.host, nodeIP) {
				return nil, fail("target " + p.target + " is not reachable from the hub: direct/haproxy connects straight to " +
					haproxyAddr(nodeIP, p.port) + ", so the node service must listen on 0.0.0.0 and the target must be written as 0.0.0.0:" +
					strconv.Itoa(p.port) + " or " + backend.HostPort(nodeIP, p.port))
			}
		}
		return ports, nil
	}

	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return nil, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	if in.Secrets.Token == "" {
		return nil, fail("the tunnel token is missing")
	}
	if !filepath.IsAbs(in.Paths.SelfBinary) {
		return nil, fail("the deyroute binary path is missing")
	}
	return ports, nil
}

// reachableFromHub reports whether a target host names the node service on
// the node's public address: the node IP itself or the unspecified address
// ("listens on every interface").
func reachableFromHub(host, nodeIP string) bool {
	if isUnspecified(host) {
		return true
	}
	h, n := net.ParseIP(host), net.ParseIP(nodeIP)
	return h != nil && n != nil && h.Equal(n)
}

func isUnspecified(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// listenAddr is the hub address user ports bind to; the canary unit never
// binds a public address.
func listenAddr(in backend.RenderInput) string {
	addr := in.ListenAddrOrDefault()
	if in.Canary {
		if ip := net.ParseIP(addr); ip == nil || !ip.IsLoopback() {
			return "127.0.0.1"
		}
	}
	return addr
}

// header is the provenance comment of rendered text files.
func header(in backend.RenderInput, side backend.Side) string {
	return "# Rendered by deyroute for tunnel " + in.Tunnel.ID + ", node " + in.Node.ID +
		", transport " + in.Transport.ID() + " (" + side.String() + " side).\n" +
		"# Regenerated from /etc/deyroute/config.yaml on every apply; do not edit.\n"
}

// relayUnit is the unit of both relay halves and of the haproxy check.
func relayUnit(in backend.RenderInput) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart: []string{in.Paths.SelfBinary, "relay", "--tunnel", in.Tunnel.ID,
			"--config", filepath.Join(in.Paths.ConfigDir, RelayConfigFile)},
		WorkingDirectory: in.Paths.ConfigDir,
	}
}
