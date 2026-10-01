// Package wireguard renders the two layer-3 transports of spec section 7.7:
// wireguard/kernel (the in-kernel WireGuard module) and awg/userspace
// (amneziawg-go with randomized Jc/Jmin/Jmax/S1/S2/H1-H4 obfuscation).
//
// Both are Forward transports: the node listens for WireGuard on UDP
// <control port>, the hub dials it with PersistentKeepalive 25, and each
// tunnel gets its own /30 (10.77.<NetIndex>.0/30, hub .1, node .2). User
// ports are forwarded with nftables in table inet deyroute: the hub DNATs every
// listen port to 10.77.n.2:<target port> and masquerades on the tunnel
// interface; the node DNATs traffic arriving on the tunnel interface to the
// configured targets only (spec section 11: the node is never an open
// proxy). The service on the node therefore sees the hub tunnel address,
// never the client IP.
//
// Render is pure: it writes wg.json (every parameter of one side) and a
// unit spec. The device itself is created by Up (the "deyroute wg up"
// command): through `ip` and the WireGuard generic-netlink API for the
// kernel transport (QUESTIONS.md C.10: no `wg` binary), through the
// amneziawg-go UAPI socket for awg. docs/backends/wireguard.md documents the
// lifecycle the daemon follows.
package wireguard

import (
	"context"
	"crypto/rand"
	"io"
	"path/filepath"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
)

// Backend names and transport names (transport ids "wireguard/kernel" and
// "awg/userspace", spec section 7 table).
const (
	Name      = "wireguard"
	AWGName   = "awg"
	Kernel    = "kernel"
	Userspace = "userspace"
)

// ConfigFile is the only rendered file (relative to Paths.ConfigDir); it
// holds every parameter "deyroute wg up/down" needs for one side.
const ConfigFile = "wg.json"

// Tunnel network parameters (spec section 7.7).
const (
	// MTU of the tunnel interface: 1500 minus the 80 bytes of the
	// WireGuard/UDP/IPv6 worst-case overhead (the wg-quick default).
	MTU = 1420
	// Keepalive is the PersistentKeepalive the hub sets on its node peer.
	Keepalive = 25
	// prefixLen is the tunnel subnet size: 10.77.<n>.0/30.
	prefixLen = 30
	// MaxNetIndex is the largest NetIndex that fits the third octet.
	MaxNetIndex = 255
)

// Keys produced by GenerateKeys and persisted per (tunnel, backend) in
// /etc/deyroute/secrets/backend-keys/<tunnel>/{wireguard,awg}.json. Keys are
// Curve25519 (X25519) keys, standard base64 of the raw 32 bytes (the
// encoding of `wg genkey` / `wg pubkey`); private keys are clamped like
// `wg genkey` does.
const (
	KeyHubPrivate  = "hub_private"  // hub interface private key
	KeyHubPublic   = "hub_public"   // hub public key (the node's peer)
	KeyNodePrivate = "node_private" // node interface private key
	KeyNodePublic  = "node_public"  // node public key (the hub's peer)
)

// AmneziaWG obfuscation parameters produced by GenerateKeys for awg only,
// decimal strings; both sides must use identical values.
const (
	KeyJc   = "jc"   // junk packets sent before every handshake (3..10)
	KeyJmin = "jmin" // minimum junk packet size in bytes (40..80)
	KeyJmax = "jmax" // maximum junk packet size in bytes (jmin+1..1280)
	KeyS1   = "s1"   // junk bytes prepended to handshake initiations (15..150)
	KeyS2   = "s2"   // junk bytes prepended to handshake responses (15..150, s1+56 != s2)
	KeyH1   = "h1"   // magic header of initiation messages (uint32 >= 5)
	KeyH2   = "h2"   // magic header of response messages (uint32 >= 5)
	KeyH3   = "h3"   // magic header of cookie replies (uint32 >= 5)
	KeyH4   = "h4"   // magic header of transport messages (uint32 >= 5)
)

// Backend implements backend.Backend and backend.KeyGenerator for one of
// the two WireGuard flavours.
type Backend struct {
	name      string
	transport string
	awg       bool
	// rand is the entropy source of GenerateKeys (crypto/rand by default;
	// tests inject fixed or failing readers).
	rand io.Reader
}

// New returns the wireguard backend (transport wireguard/kernel).
func New() *Backend {
	return &Backend{name: Name, transport: Kernel, rand: rand.Reader}
}

// NewAWG returns the awg backend (transport awg/userspace).
func NewAWG() *Backend {
	return &Backend{name: AWGName, transport: Userspace, awg: true, rand: rand.Reader}
}

func init() {
	backend.Register(New())
	backend.Register(NewAWG())
}

// Name returns "wireguard" or "awg".
func (b *Backend) Name() string { return b.name }

// Transports returns the single transport of the backend. Both are
// Forward, carry tcp and udp, need UDP between hub and node and use their
// own keys instead of the tunnel TLS certificate. Stealth follows the
// section 7 table: kernel 1, awg 3. wireguard/kernel is a rung of the
// UDP-only default ladder (section 8), so it is not Optional; awg/userspace
// is in no default ladder and only used when the owner adds it.
func (b *Backend) Transports() []backend.Transport {
	stealth := 1
	if b.awg {
		stealth = 3
	}
	return []backend.Transport{{
		Backend:   b.name,
		Name:      b.transport,
		Direction: backend.Forward,
		Protos:    []string{config.ProtoTCP, config.ProtoUDP},
		NeedsUDP:  true,
		NeedsTLS:  false,
		Stealth:   stealth,
		Optional:  b.awg,
	}}
}

// Manifest returns the pinned release. backends.yaml has one "wireguard"
// block describing amneziawg-go: the awg backend installs exactly that
// (under its own name, /var/lib/deyroute/bin/awg/<ver>/amneziawg-go), while
// wireguard/kernel needs no download at all (the kernel module ships with
// every Tier 1 distribution), so its entry is marked System.
func (b *Backend) Manifest() backend.ManifestEntry {
	e := backend.ManifestFor(Name)
	if b.awg {
		e.Name = AWGName
		return e
	}
	return backend.ManifestEntry{
		Name:            Name,
		Version:         "kernel",
		Repo:            "WireGuard/wireguard-linux",
		Readme:          "https://www.wireguard.com/xplatform/",
		TemplateVersion: e.TemplateVersion,
		System:          true,
	}
}

// Probe has no backend-specific check; the path probe covers the tunnel.
func (*Backend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

// Validate reports impossible combinations for in (DEY-B006, DEY-B010).
func (b *Backend) Validate(in backend.RenderInput) error {
	_, err := b.plan(in)
	return err
}

// Render renders one side: wg.json, the unit spec, binds and NAT. It is
// pure and deterministic.
func (b *Backend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	p, err := b.plan(in)
	if err != nil {
		return backend.Rendered{}, err
	}
	if side == backend.SideNode {
		return b.renderNode(in, p)
	}
	return b.renderHub(in, p)
}

// PreStart is the step the daemon runs before it starts this side's unit
// (docs/backends/wireguard.md). For awg it creates the amneziawg-go UAPI
// socket directory /run/amneziawg: the unit runs under ProtectSystem=strict,
// where /run is read-only and the directory is only writable (optional
// ReadWritePaths) when it already exists. The kernel unit needs nothing.
func (b *Backend) PreStart(ctx context.Context, configDir string, r Runner) error {
	if !b.awg {
		return nil
	}
	return (&Manager{Runner: r}).Prepare(ctx, filepath.Join(configDir, ConfigFile))
}

// PostStart is the step the daemon runs after it started this side's unit
// (docs/backends/wireguard.md). The kernel unit configures the device
// itself (a oneshot "deyroute wg up"), so PostStart does nothing for it; the
// awg unit only runs amneziawg-go, whose device PostStart then configures
// through the UAPI socket (Up). configDir is Paths.ConfigDir of that side.
func (b *Backend) PostStart(ctx context.Context, configDir string, r Runner) error {
	if !b.awg {
		return nil
	}
	return Up(ctx, filepath.Join(configDir, ConfigFile), r)
}

// PostStop is the cleanup the daemon runs after it stopped this side's
// unit (switch away, tunnel delete): it removes the interface if anything
// is left of it. It is idempotent.
func (b *Backend) PostStop(ctx context.Context, configDir string, r Runner) error {
	return Down(ctx, filepath.Join(configDir, ConfigFile), r)
}
