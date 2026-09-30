// Package backhaul renders the Musixal/Backhaul backend (spec section 7.1):
// the hub runs the Backhaul [server] (it binds the tunnel's user ports) and
// each node runs the [client] that dials the hub's backend control port and
// forwards every stream to the target the server names.
//
// Configuration keys follow the README and the source code of the version
// pinned in backends.yaml (v0.7.2); docs/backends/backhaul.md lists the
// differences to the spec sample. Rendering is pure and deterministic.
package backhaul

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name used in transport ids ("backhaul/wssmux").
const Name = "backhaul"

// Rendered file names (relative to Paths.ConfigDir).
const (
	ServerFile = "server.toml" // hub side
	ClientFile = "client.toml" // node side
)

// Tuning values rendered into every config (spec section 7.1).
const (
	keepalivePeriod  = 75      // seconds, TCP keep-alive
	heartbeat        = 20      // seconds; short for fast failure detection
	channelSize      = 2048    // server queue size
	dialTimeout      = 10      // seconds, client dial timeout
	retryInterval    = 3       // seconds, client reconnect interval
	muxCon           = 8       // server: streams multiplexed per connection
	muxVersion       = 1       // SMUX protocol version
	muxFrameSize     = 32768   // 32 KB
	muxReceiveBuffer = 4194304 // 4 MB
	muxStreamBuffer  = 65536   // 64 KB
	logLevel         = "info"

	// minPool is the default client connection_pool (spec: default 8).
	minPool = 8
	// maxPool caps an owner supplied advanced.connection_pool.
	maxPool = 1024
)

// Transport names of the pinned version.
const (
	TCP    = "tcp"
	TCPMux = "tcpmux"
	WS     = "ws"
	WSS    = "wss"
	WSMux  = "wsmux"
	WSSMux = "wssmux"
	UDP    = "udp"
)

// spec describes one Backhaul transport.
type spec struct {
	name    string
	stealth int
	protos  []string
	mux     bool // renders mux_* keys
	tls     bool // wss/wssmux: tls_cert/tls_key on the server
	udpData bool // "udp" transport: data plane over UDP (control over TCP)
	// acceptUDP: the transport can carry UDP port maps with accept_udp
	// (UDP over the TCP tunnel). In v0.7.2 only the plain tcp server
	// implements it.
	acceptUDP bool
}

var specs = []spec{
	{name: TCP, stealth: 1, protos: []string{config.ProtoTCP, config.ProtoUDP}, acceptUDP: true},
	{name: TCPMux, stealth: 2, protos: []string{config.ProtoTCP}, mux: true},
	{name: WS, stealth: 2, protos: []string{config.ProtoTCP}},
	{name: WSS, stealth: 4, protos: []string{config.ProtoTCP}, tls: true},
	{name: WSMux, stealth: 3, protos: []string{config.ProtoTCP}, mux: true},
	{name: WSSMux, stealth: 4, protos: []string{config.ProtoTCP}, mux: true, tls: true},
	{name: UDP, stealth: 1, protos: []string{config.ProtoUDP}, udpData: true},
}

func lookupSpec(name string) (spec, bool) {
	for _, s := range specs {
		if s.name == name {
			return s, true
		}
	}
	return spec{}, false
}

// Backend implements backend.Backend for Backhaul.
type Backend struct{}

// New returns the Backhaul backend.
func New() *Backend { return &Backend{} }

func init() { backend.Register(New()) }

// Name returns "backhaul".
func (*Backend) Name() string { return Name }

// Transports returns every Backhaul transport (all Reverse, spec section 7
// comparison table).
func (*Backend) Transports() []backend.Transport {
	out := make([]backend.Transport, 0, len(specs))
	for _, s := range specs {
		out = append(out, backend.Transport{
			Backend:   Name,
			Name:      s.name,
			Direction: backend.Reverse,
			Protos:    append([]string(nil), s.protos...),
			NeedsUDP:  s.udpData,
			NeedsTLS:  s.tls,
			Stealth:   s.stealth,
		})
	}
	return out
}

// Manifest returns the pinned Backhaul release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no backend specific check: the web stats port stays disabled
// (see Validate), so there is nothing extra to ask the process.
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, _, err := b.plan(in)
	return err
}

// Render renders the hub ([server]) or node ([client]) side. It is pure.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	sp, maps, err := b.plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return renderClient(in, sp, maps), nil
	}
	return renderServer(in, sp, maps), nil
}

// mapping is one Backhaul "ports" entry: one listen port on the hub
// forwarded to target on the node, carrying tcp and/or udp.
type mapping struct {
	listen int
	target string
	tcp    bool
	udp    bool
}

// plan validates in and computes the port mappings to render.
func (b *Backend) plan(in backend.RenderInput) (spec, []mapping, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	sp, ok := lookupSpec(in.Transport.Name)
	if in.Transport.Backend != Name || !ok {
		return spec{}, nil, fail("not a backhaul transport")
	}
	ports := portMaps(in)
	if len(ports) == 0 {
		return spec{}, nil, fail("the tunnel has no port maps")
	}
	for _, p := range ports {
		proto := protoOf(p)
		if !contains(sp.protos, proto) {
			return spec{}, nil, deyerr.New(deyerr.B010, deyerr.Params{"transport": id, "proto": proto})
		}
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return spec{}, nil, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	if in.Secrets.Token == "" {
		return spec{}, nil, fail("the tunnel token is missing")
	}
	if strings.TrimSpace(in.Hub.PublicIP) == "" {
		return spec{}, nil, fail("the hub public IP is unknown (the node client dials it)")
	}
	if sp.tls && (in.Secrets.TLSCertFile == "" || in.Secrets.TLSKeyFile == "") {
		return spec{}, nil, fail("the tunnel TLS certificate or key is missing")
	}
	if !filepath.IsAbs(in.Paths.Binary) {
		return spec{}, nil, fail("the backhaul binary is not installed (no absolute binary path)")
	}
	if !filepath.IsAbs(in.Paths.ConfigDir) {
		return spec{}, nil, fail("the rendered config directory is not an absolute path")
	}
	if adv := in.Tunnel.Advanced; adv != nil {
		if adv.BackhaulWebPort != 0 {
			// Spec 7.1: web_port only on 127.0.0.1. Backhaul v0.7.2 always
			// serves it on every interface (":<port>"), which would expose
			// a Backhaul dashboard on the hub's public IP.
			return spec{}, nil, fail("advanced.backhaul_web_port cannot be limited to 127.0.0.1 by this Backhaul version (it listens on every interface); remove it")
		}
		if adv.ConnectionPool < 0 || adv.ConnectionPool > maxPool {
			return spec{}, nil, fail("advanced.connection_pool must be between 1 and " + strconv.Itoa(maxPool))
		}
	}
	maps, err := buildMappings(ports, fail)
	if err != nil {
		return spec{}, nil, err
	}
	if acceptsUDP(sp, maps) {
		// accept_udp is global in v0.7.2: every "ports" entry then starts a
		// TCP and a UDP listener. A listen port the tunnel maps for only one
		// protocol would expose the other protocol of the node target (for
		// example a loopback-only DNS or admin service) on the hub's public
		// address (spec section 11), so both protocols must be mapped.
		for _, m := range maps {
			if m.tcp != m.udp {
				have, missing := config.ProtoTCP, config.ProtoUDP
				if m.udp {
					have, missing = config.ProtoUDP, config.ProtoTCP
				}
				return spec{}, nil, fail("with UDP port maps backhaul/tcp opens tcp and udp on every listen port; port " +
					strconv.Itoa(m.listen) + " is mapped for " + have + " only and would also expose " + missing +
					" (map both protocols of every port, or use backhaul/udp or direct/native)")
			}
		}
	}
	return sp, maps, nil
}

// buildMappings groups port maps by listen port. A tcp and a udp port map
// on the same listen port share one Backhaul entry (accept_udp starts both
// listeners from one entry), so their targets must be equal.
func buildMappings(ports []config.PortMap, fail func(string) error) ([]mapping, error) {
	var out []mapping
	index := map[int]int{}
	for _, p := range ports {
		if p.Listen < 1 || p.Listen > 65535 {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + " is out of range")
		}
		target := targetOf(p)
		if _, _, ok := backend.SplitTarget(target); !ok || strings.ContainsAny(target, "=\" \t") {
			return nil, fail("invalid target '" + target + "'")
		}
		proto := protoOf(p)
		i, seen := index[p.Listen]
		if !seen {
			index[p.Listen] = len(out)
			out = append(out, mapping{listen: p.Listen, target: target})
			i = len(out) - 1
		} else if out[i].target != target {
			return nil, fail("listen port " + strconv.Itoa(p.Listen) + " has different tcp and udp targets; backhaul forwards both protocols of one port to one target")
		}
		switch proto {
		case config.ProtoUDP:
			if out[i].udp {
				return nil, fail("listen port " + strconv.Itoa(p.Listen) + "/udp is listed twice")
			}
			out[i].udp = true
		default:
			if out[i].tcp {
				return nil, fail("listen port " + strconv.Itoa(p.Listen) + "/tcp is listed twice")
			}
			out[i].tcp = true
		}
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

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
