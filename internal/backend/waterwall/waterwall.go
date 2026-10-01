// Package waterwall renders the radkesvat/WaterWall backend (spec section
// 7.4): the Reverse "reverse-reality" transport. The hub accepts the node's
// Reality connections on its backend control port (RealityServer, with the
// decoy site as visitor destination) and pairs them in a ReverseServer with
// the user connections accepted on the tunnel's listen ports. The node keeps
// a pool of ReverseClient connections open through RealityClient to the hub
// and connects every paired stream to the target service.
//
// The node graph follows the official reverse / Reality layout of the pinned
// version (v1.46.94: tunnels/*/description.md and the tests/cases reverse and
// Reality configs); only the hub address and port, the user ports, the
// password and the SNI (decoy) are substituted. docs/backends/waterwall.md
// explains the choice of version and the differences to the spec sample.
// Rendering is pure and deterministic.
package waterwall

import (
	"context"
	"crypto/rand"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Name is the backend name used in transport ids.
const Name = "waterwall"

// ReverseReality is the only transport of this backend.
const ReverseReality = "reverse-reality"

// Rendered file names (relative to Paths.ConfigDir). Waterwall reads
// core.json from its working directory; core.json lists config.json.
const (
	CoreFile   = "core.json"
	ConfigFile = "config.json"
)

// KeyPassword is the shared Reality password produced by GenerateKeys
// (24 random alphanumeric characters; Waterwall accepts 1..32 bytes).
const KeyPassword = "password"

// DefaultWorkers is the core.json worker count when the CPU count of the
// side is unknown (RenderInput.HubCPUs/NodeCPUs 0); otherwise the spec's
// min(4, CPU) is rendered (Workers).
const DefaultWorkers = 4

// Rendering constants.
const (
	passwordLen   = 24
	decoyPort     = 443
	minimumUnused = 16
	// logDir is where Waterwall writes its own log files. It lives in the
	// unit's PrivateTmp (the config directory is read-only under
	// ProtectSystem=strict); every logger also writes to the console, which
	// systemd appends to /var/log/deyroute/tunnels/<tunnel>.log.
	logDir = "/tmp/deyroute-waterwall/"
	mtu    = 1500
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Backend implements backend.Backend and backend.KeyGenerator for Waterwall.
type Backend struct {
	// rand is the entropy source of GenerateKeys (crypto/rand in production).
	rand io.Reader
}

// New returns the Waterwall backend.
func New() *Backend { return &Backend{rand: rand.Reader} }

func init() { backend.Register(New()) }

// Name returns "waterwall".
func (*Backend) Name() string { return Name }

// Transports returns waterwall/reverse-reality (spec section 7 comparison
// table: Reverse, tcp, stealth 5). Reality uses its own password, not the
// tunnel certificate.
func (*Backend) Transports() []backend.Transport {
	return []backend.Transport{{
		Backend:   Name,
		Name:      ReverseReality,
		Direction: backend.Reverse,
		Protos:    []string{config.ProtoTCP},
		Stealth:   5,
	}}
}

// Manifest returns the pinned Waterwall release.
func (*Backend) Manifest() backend.ManifestEntry { return backend.ManifestFor(Name) }

// Probe has no backend specific check.
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// GenerateKeys creates the shared Reality password in pure Go.
func (b *Backend) GenerateKeys(backend.Transport) (map[string]string, error) {
	r := b.rand
	if r == nil {
		r = rand.Reader
	}
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, passwordLen)
	for i := range out {
		n, err := rand.Int(r, max)
		if err != nil {
			return nil, deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": Name})
		}
		out[i] = alphabet[n.Int64()]
	}
	return map[string]string{KeyPassword: string(out)}, nil
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := plan(in)
	return err
}

// Render renders the hub (RealityServer + ReverseServer) or node
// (ReverseClient + RealityClient) side. It is pure.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	p, err := plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	var nodes []wwNode
	var binds []backend.PortUse
	if side == backend.SideNode {
		nodes = nodeGraph(in, p)
	} else {
		nodes = hubGraph(in, p)
		binds = append(binds, backend.PortUse{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: "0.0.0.0", Purpose: "control"})
		for _, port := range p.listen {
			binds = append(binds, backend.PortUse{Port: port, Proto: config.ProtoTCP, Addr: p.listenAddr, Purpose: "user"})
		}
	}
	cfg, err := backend.JSONIndent(wwConfig{
		Name:  "deyroute-" + in.Tunnel.ID + "-" + in.Node.ID + "-" + side.String(),
		Nodes: nodes,
	})
	if err != nil {
		return backend.Rendered{}, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": ReverseReality})
	}
	return backend.Rendered{
		Files: map[string][]byte{
			CoreFile:   CoreJSONFor(sideWorkers(in, side), in.FirstRun),
			ConfigFile: cfg,
		},
		Unit: backend.UnitSpec{
			// Waterwall takes no arguments and reads ./core.json, so the
			// working directory must be the directory of core.json.
			ExecStart:        []string{in.Paths.Binary},
			WorkingDirectory: in.Paths.ConfigDir,
		},
		Binds: binds,
	}, nil
}

// planned is everything Render needs after validation.
type planned struct {
	listen     []int  // hub user ports in port-map order
	host       string // target host on the node
	port       int    // target port for a single port map (0 = from header)
	multi      bool   // several ports: HeaderClient/HeaderServer carry the port
	password   string
	decoyHost  string
	decoyPort  int
	listenAddr string
}

// plan validates in and prepares the render data.
func plan(in backend.RenderInput) (planned, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != Name || in.Transport.Name != ReverseReality {
		return planned{}, fail("not a waterwall transport")
	}
	ports := in.Tunnel.Ports
	if in.Canary && len(ports) > 1 {
		ports = ports[:1]
	}
	if len(ports) == 0 {
		return planned{}, fail("the tunnel has no port maps")
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return planned{}, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	if !filepath.IsAbs(in.Paths.Binary) {
		return planned{}, fail("the waterwall binary is not installed (no absolute binary path)")
	}
	if !filepath.IsAbs(in.Paths.ConfigDir) {
		return planned{}, fail("the rendered config directory is not an absolute path")
	}
	if net.ParseIP(strings.TrimSpace(in.Hub.PublicIP)) == nil {
		return planned{}, fail("the hub public IP is unknown (the node dials it)")
	}
	pw := in.Secrets.Keys[KeyPassword]
	if pw == "" || len(pw) > 32 || !utf8.ValidString(pw) || strings.ContainsAny(pw, "\"\\") {
		return planned{}, fail("the Reality password is missing or invalid (password, 1..32 bytes); regenerate the tunnel keys")
	}
	decoy := backend.DecoyFor(in)
	if decoy == "" {
		return planned{}, fail("no decoy SNI is selected (set hub decoy_snis; the hub picks the first reachable one)")
	}
	dHost, dPort := decoy, decoyPort
	if h, p, ok := backend.SplitTarget(decoy); ok && !strings.Contains(h, ":") {
		dHost, dPort = h, p
	}
	if net.ParseIP(dHost) != nil || !validDomain(dHost) {
		return planned{}, fail("decoy '" + decoy + "' must be a domain name (it is sent as the TLS SNI)")
	}

	p := planned{password: pw, decoyHost: dHost, decoyPort: dPort, listenAddr: listenAddr(in), multi: len(ports) > 1}
	seen := map[int]bool{}
	for i, pm := range ports {
		proto := pm.Proto
		if proto == "" {
			proto = config.ProtoTCP
		}
		if proto != config.ProtoTCP {
			return planned{}, deyerr.New(deyerr.B010, deyerr.Params{"transport": id, "proto": proto})
		}
		if pm.Listen < 1 || pm.Listen > 65535 {
			return planned{}, fail("listen port " + strconv.Itoa(pm.Listen) + " is out of range")
		}
		if seen[pm.Listen] {
			return planned{}, fail("listen port " + strconv.Itoa(pm.Listen) + "/tcp is listed twice")
		}
		seen[pm.Listen] = true
		target := pm.Target
		if target == "" {
			target = backend.HostPort("127.0.0.1", pm.Listen)
		}
		host, port, ok := backend.SplitTarget(target)
		if !ok || (net.ParseIP(host) == nil && !validDomain(host)) {
			return planned{}, fail("invalid target '" + target + "'")
		}
		if i == 0 {
			p.host, p.port = host, port
		}
		if p.multi && (host != p.host || port != pm.Listen) {
			// The node learns only the hub listen port (HeaderClient ->
			// HeaderServer) and connects to <host>:<that port>.
			return planned{}, fail("with several ports every target must be <one host>:<same port as listen> " +
				"(target '" + target + "' for listen " + strconv.Itoa(pm.Listen) + "); use one tunnel per differing target")
		}
		p.listen = append(p.listen, pm.Listen)
	}
	if p.multi {
		p.port = 0
	}
	return p, nil
}

// listenAddr is the hub address user ports bind to; the canary never binds
// a public address.
func listenAddr(in backend.RenderInput) string {
	addr := in.ListenAddrOrDefault()
	if in.Canary {
		if ip := net.ParseIP(addr); ip == nil || !ip.IsLoopback() {
			return "127.0.0.1"
		}
	}
	return addr
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

// sideWorkers is the worker count of side: Workers of its CPU count, or
// DefaultWorkers while it is unknown.
func sideWorkers(in backend.RenderInput, side backend.Side) int {
	n := in.HubCPUs
	if side == backend.SideNode {
		n = in.NodeCPUs
	}
	if n < 1 {
		return DefaultWorkers
	}
	return Workers(n)
}

// StartFailureCode makes a Waterwall unit that fails to start DEY-B043
// (spec section 7.4), with the last 40 log lines as its detail.
func (*Backend) StartFailureCode() deyerr.Code { return deyerr.B043 }

// Runner is the runner the daemons pass to backend hooks (unused here).
type Runner = interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

// PreStart validates the rendered JSON files in configDir before the unit
// starts (spec section 7.4: Waterwall explains broken configs badly):
// DEY-B041 without core.json, DEY-B040 for an invalid file.
func (*Backend) PreStart(_ context.Context, configDir string, _ Runner) error {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return deyerr.Wrap(deyerr.B041, err, deyerr.Params{"dir": configDir})
	}
	files := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(configDir, e.Name())) // #nosec G304 -- the unit's own config directory
		if err != nil {
			return deyerr.Wrap(deyerr.B040, err, deyerr.Params{"file": e.Name()})
		}
		files[e.Name()] = data
	}
	if _, ok := files[CoreFile]; !ok {
		return deyerr.New(deyerr.B041, deyerr.Params{"dir": configDir})
	}
	return ValidateJSON(files)
}

// Workers returns the core.json worker count for a machine with ncpu CPUs:
// min(4, ncpu), at least 1 (spec section 7.4).
func Workers(ncpu int) int {
	if ncpu < 1 {
		return 1
	}
	if ncpu > DefaultWorkers {
		return DefaultWorkers
	}
	return ncpu
}
