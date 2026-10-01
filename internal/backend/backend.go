// Package backend defines the plugin interface every tunnel backend
// implements (section 7) and the registry the failover engine, UI and CLI use.
// Nothing outside a backend's own package may branch on a backend name.
package backend

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Side selects which half of a transport is rendered.
type Side int

// Sides.
const (
	SideHub Side = iota
	SideNode
)

func (s Side) String() string {
	if s == SideNode {
		return "node"
	}
	return "hub"
}

// Direction tells who dials whom.
type Direction int

// Directions.
const (
	// Reverse: the backend server runs on the hub and binds the user ports;
	// the node-side client dials the hub control port.
	Reverse Direction = iota
	// Forward: the hub dials the node; the node runs the backend server/inbound.
	Forward
)

func (d Direction) String() string {
	if d == Forward {
		return "forward"
	}
	return "reverse"
}

// ServerSide returns the side that must start first (section 9: server side
// of the backend first, client side second).
func (d Direction) ServerSide() Side {
	if d == Forward {
		return SideNode
	}
	return SideHub
}

// Transport is one concrete method of a backend, e.g. backhaul/wssmux.
type Transport struct {
	Backend   string // "backhaul"
	Name      string // "wssmux" → full id "backhaul/wssmux"
	Direction Direction
	Protos    []string // "tcp", "udp"
	NeedsUDP  bool     // requires UDP reachability between hub and node
	NeedsTLS  bool     // consumes tunnel TLS cert/key from internal CA or ACME
	Stealth   int      // 1..5, used for default ladder ordering and UI hints

	// ClientIPPreserved is shown in the UI as "client IP: preserved/masked"
	// (section 10). True only for PROXY-protocol capable transports.
	ClientIPPreserved bool
	// Optional transports are never part of the default ladder (section 7.9).
	Optional bool
	// NeverQuarantine is set for direct/native (section 9).
	NeverQuarantine bool
}

// ID returns "backend/name".
func (t Transport) ID() string { return t.Backend + "/" + t.Name }

// Supports reports whether the transport can forward proto ("tcp"/"udp").
func (t Transport) Supports(proto string) bool {
	for _, p := range t.Protos {
		if p == proto {
			return true
		}
	}
	return false
}

// Secrets is the key material a renderer may reference. File paths point to
// copies readable by the backend process (rendered config dir, 0640
// root:deyroute); nothing here is ever logged.
type Secrets struct {
	Token          string // shared per-tunnel token (32 random bytes, base64url)
	TLSCertFile    string // PEM chain for tunnel TLS (auto/acme/custom)
	TLSKeyFile     string // PEM private key
	TLSP12File     string // PKCS#12 bundle (rathole/tls)
	TLSP12Password string
	CAFile         string // internal CA certificate (clients pin to it)
	TLSCertSHA256  string // lowercase hex sha256 of the tunnel leaf DER (hysteria2 pinSHA256)
	ServerName     string // SNI/verify name clients use for tunnel TLS (domain or hub IP)
	// Keys holds backend-specific generated material from KeyGenerator,
	// persisted in /etc/deyroute/secrets/backend-keys/<tunnel>/<backend>.json.
	Keys map[string]string
}

// Paths are absolute locations on the rendered side.
type Paths struct {
	Binary     string // primary backend binary (/var/lib/deyroute/bin/<b>/<ver>/<b>)
	BinDir     string // directory holding every binary of that backend version (frps + frpc)
	ConfigDir  string // where Rendered.Files are written; also WorkingDirectory
	LogFile    string // /var/log/deyroute/tunnels/<tunnel>.log
	SelfBinary string // /usr/local/bin/deyroute (for built-in transports)
}

// RenderInput is everything a backend needs to render one side of one
// (tunnel, node, transport).
type RenderInput struct {
	Tunnel      config.Tunnel
	Node        config.Node
	Hub         config.HubInfo
	Transport   Transport
	ControlPort int // allocated by the hub per (tunnel, node, transport) from 30000-31999
	Secrets     Secrets
	Paths       Paths

	// ListenAddr is the address user ports bind to on the hub ("0.0.0.0"
	// default, "::" for dual-stack, "127.0.0.1" for canary units).
	ListenAddr string
	// Decoy is the selected reachable decoy SNI (Reality/Waterwall).
	Decoy string
	// NetIndex is a small per-tunnel integer allocated by the hub
	// (WireGuard 10.77.<n>.0/30, interface names).
	NetIndex int
	// FirstRun enables verbose backend logging for the first start (Waterwall).
	FirstRun bool
	// Canary marks the canary unit (section 9, phase 8): only the control port
	// and one loopback port are rendered.
	Canary bool
}

// ListenAddrOrDefault returns ListenAddr or "0.0.0.0".
func (in RenderInput) ListenAddrOrDefault() string {
	if in.ListenAddr == "" {
		return "0.0.0.0"
	}
	return in.ListenAddr
}

// PortsFor returns the tunnel port maps whose proto the transport supports.
func (in RenderInput) PortsFor(proto string) []config.PortMap {
	var out []config.PortMap
	for _, p := range in.Tunnel.Ports {
		if p.Proto == proto {
			out = append(out, p)
		}
	}
	return out
}

// UnitSpec is the per-instance part of deyroute-tun@.service, written as a
// drop-in by internal/systemd. Hardening lives in the shared template; a
// backend that cannot run under an option lists it in DropHardening with a
// documented reason (section 11).
type UnitSpec struct {
	ExecStart        []string          // argv; first element absolute path
	ExecStartPre     [][]string        // optional pre-start commands
	ExecStop         [][]string        // optional stop commands (oneshot units)
	WorkingDirectory string            // defaults to Paths.ConfigDir
	Env              map[string]string // extra environment
	Type             string            // "simple" (default) or "oneshot"
	RemainAfterExit  bool              // oneshot units that configure kernel state
	RunAsRoot        bool              // WireGuard/AmneziaWG only
	ExtraCaps        []string          // e.g. CAP_NET_ADMIN (added to ambient + bounding)
	DropHardening    map[string]string // option name → reason, e.g. "MemoryDenyWriteExecute": "JIT"
	AddressFamilies  []string          // extra RestrictAddressFamilies, e.g. AF_NETLINK
	ReadWritePaths   []string          // extra writable paths
}

// PortUse is a port this side will bind.
type PortUse struct {
	Port    int
	Proto   string // "tcp" | "udp"
	Addr    string // bind address
	Purpose string // "user" (tunnel listen port) or "control" (backend control port)
}

// NATRule is a DNAT the renderer needs in table inet deyroute (WireGuard
// port forwarding on the hub, Hysteria2 port hopping on the node).
type NATRule struct {
	Proto     string // "tcp" | "udp"
	DportLow  int
	DportHigh int    // == DportLow for a single port
	ToAddr    string // e.g. 10.77.3.2 (empty = local redirect)
	ToPort    int
	Iface     string // optional input interface
}

// Rendered is one side's output.
type Rendered struct {
	Files      map[string][]byte // relative to Paths.ConfigDir
	Unit       UnitSpec
	Binds      []PortUse
	NAT        []NATRule
	Masquerade []string // interfaces to masquerade on (postrouting)
	IPForward  bool     // needs net.ipv4.ip_forward=1
}

// ManifestEntry pins one backend release (section 5). It is loaded from
// backends.yaml (embedded, overridable in /etc/deyroute/backends.yaml).
type ManifestEntry struct {
	Name            string            `yaml:"-"`
	Version         string            `yaml:"version"`
	Repo            string            `yaml:"repo,omitempty"`     // e.g. Musixal/Backhaul
	Readme          string            `yaml:"readme,omitempty"`   // README of the pinned version
	URLs            map[string]string `yaml:"urls,omitempty"`     // arch (amd64|arm64) → download URL
	SHA256          map[string]string `yaml:"sha256,omitempty"`   // arch → hex sha256 of the downloaded file
	Archive         string            `yaml:"archive,omitempty"`  // tar.gz | zip | gz | raw
	Binaries        []string          `yaml:"binaries,omitempty"` // executables to extract, matched by base name
	TemplateVersion int               `yaml:"template_version"`   // renderer template version
	Builtin         bool              `yaml:"builtin,omitempty"`  // part of deyroute itself (direct/native)
	System          bool              `yaml:"system,omitempty"`   // provided by the OS package (haproxy)
}

// Binary returns the primary executable name (first of Binaries, or Name).
func (m ManifestEntry) Binary() string {
	if len(m.Binaries) > 0 {
		return m.Binaries[0]
	}
	return m.Name
}

// ProbeResult is an optional backend-specific health check result.
type ProbeResult struct {
	OK     bool
	RTT    time.Duration
	Detail string
}

// ErrNoProbe is returned by Probe when a backend has no extra check.
var ErrNoProbe = deyerr.Plain("backend has no extra probe")

// Backend is implemented by every backend package and registered in init().
type Backend interface {
	Name() string
	Transports() []Transport
	Manifest() ManifestEntry                                        // version, urls, sha256 per arch
	Validate(in RenderInput) error                                  // static checks, DEY-Bxxx errors
	Render(in RenderInput, side Side) (Rendered, error)             // pure: no I/O
	Probe(ctx context.Context, in RenderInput) (ProbeResult, error) // optional extra check
}

// SystemBinary is implemented by backends whose transport runs a program of
// the distribution instead of a downloaded one (direct/haproxy): it returns
// the program name for t ("" = none). The planner sets Paths.Binary to its
// absolute path when it is installed, so Validate can tell a missing
// program (DEY-B006) from an installed one.
type SystemBinary interface {
	SystemBinary(t Transport) string
}

// KeyGenerator is implemented by backends that need per-tunnel key material
// (Noise keys, Reality x25519 + shortId, WireGuard keys, obfs passwords).
// Keys are generated once per (tunnel, backend) in pure Go and persisted.
type KeyGenerator interface {
	GenerateKeys(t Transport) (map[string]string, error)
}

var (
	regMu    sync.RWMutex
	registry = map[string]Backend{}
)

// Register adds b; called from each backend package's init().
func Register(b Backend) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[b.Name()]; dup {
		panic("backend registered twice: " + b.Name())
	}
	registry[b.Name()] = b
}

// Lookup resolves "backend/transport". Unknown ids return DEY-C005 listing
// the valid transports.
func Lookup(id string) (Backend, Transport, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	name, tr, ok := strings.Cut(id, "/")
	if ok {
		if b, found := registry[name]; found {
			for _, t := range b.Transports() {
				if t.Name == tr {
					return b, t, nil
				}
			}
		}
	}
	return nil, Transport{}, deyerr.New(deyerr.C005, deyerr.Params{"transport": id, "valid": strings.Join(validIDsLocked(), ", ")})
}

// All returns registered backends sorted by name.
func All() []Backend {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Backend, 0, len(registry))
	for _, b := range registry {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// AllTransports returns every registered transport sorted by id.
func AllTransports() []Transport {
	var out []Transport
	for _, b := range All() {
		out = append(out, b.Transports()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// KnownTransport reports whether id is registered.
func KnownTransport(id string) bool {
	_, _, err := Lookup(id)
	return err == nil
}

// ValidIDs returns all transport ids sorted.
func ValidIDs() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	return validIDsLocked()
}

func validIDsLocked() []string {
	var ids []string
	for _, b := range registry {
		for _, t := range b.Transports() {
			ids = append(ids, t.ID())
		}
	}
	sort.Strings(ids)
	return ids
}
