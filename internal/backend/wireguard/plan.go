package wireguard

import (
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// tunnelIDRe is the tunnel id syntax of the config (section 4).
var tunnelIDRe = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)

// maxShortTunnel is the longest tunnel id that fits "dey-<tunnel>" in
// IFNAMSIZ-1 = 15 characters.
const maxShortTunnel = 15 - len("dey-")

// InterfaceName returns the tunnel interface name: "dey-<tunnel>" as in
// spec section 7.7 when it fits Linux's 15-character limit; for longer ids
// "dey-<prefix>_<NetIndex>" with the longest prefix of the id that fits
// (at least 7 characters; '_' never occurs in tunnel ids and the NetIndex
// is unique per tunnel, so names never collide). The canary unit
// uses "deyc-<NetIndex>" so it can run next to an active WireGuard rung of
// the same tunnel.
func InterfaceName(tunnel string, netIndex int, canary bool) string {
	idx := strconv.Itoa(netIndex)
	if canary {
		return "deyc-" + idx
	}
	if len(tunnel) <= maxShortTunnel {
		return "dey-" + tunnel
	}
	keep := 15 - len("dey-") - 1 - len(idx)
	return "dey-" + tunnel[:keep] + "_" + idx
}

// TunnelAddrs returns the hub and node addresses of the tunnel subnet
// 10.77.<netIndex>.0/30 (hub .1, node .2). The canary unit uses the next
// /30 of the same /24 (hub .5, node .6).
func TunnelAddrs(netIndex int, canary bool) (hub, node netip.Addr) {
	base := byte(0)
	if canary {
		base = 4
	}
	n := byte(netIndex) // #nosec G115 -- plan() checks 0..MaxNetIndex
	return netip.AddrFrom4([4]byte{10, 77, n, base + 1}), netip.AddrFrom4([4]byte{10, 77, n, base + 2})
}

// portMap is one validated port map.
type portMap struct {
	proto  string
	listen int
	host   netip.Addr // target host on the node (IPv4)
	port   int        // target port
}

// plan is the validated, side-independent rendering plan.
type plan struct {
	iface         string
	hubAddr       netip.Addr
	nodeAddr      netip.Addr
	maps          []portMap
	routeLocalnet bool
	endpoint      netip.AddrPort // node public IP : control port
	keys          keySet
}

// keySet are the decoded per-tunnel keys.
type keySet struct {
	hubPriv, hubPub, nodePriv, nodePub string
	awg                                *AWGParams
}

// plan validates in and computes everything Render needs.
func (b *Backend) plan(in backend.RenderInput) (plan, error) {
	id := in.Transport.ID()
	fail := func(reason string) error {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": id, "reason": reason})
	}
	if in.Transport.Backend != b.name || in.Transport.Name != b.transport {
		return plan{}, fail("not a " + b.name + "/" + b.transport + " transport")
	}
	if !tunnelIDRe.MatchString(in.Tunnel.ID) {
		return plan{}, fail("tunnel id '" + in.Tunnel.ID + "' is not valid")
	}
	if in.NetIndex < 0 || in.NetIndex > MaxNetIndex {
		return plan{}, fail("network index " + strconv.Itoa(in.NetIndex) + " is outside 0.." + strconv.Itoa(MaxNetIndex) + " (10.77.<n>.0/30)")
	}
	if in.ControlPort < 1 || in.ControlPort > 65535 {
		return plan{}, fail("backend control port " + strconv.Itoa(in.ControlPort) + " is out of range")
	}
	nodeIP, err := netip.ParseAddr(strings.TrimSpace(in.Node.PublicIP))
	if err != nil || nodeIP.Zone() != "" || nodeIP.IsUnspecified() || nodeIP.IsLoopback() {
		return plan{}, fail("the node public IP '" + in.Node.PublicIP + "' is not usable as the WireGuard endpoint")
	}
	nodeIP = nodeIP.Unmap()
	switch {
	case !filepath.IsAbs(in.Paths.ConfigDir):
		return plan{}, fail("the rendered config directory is not an absolute path")
	case !b.awg && !filepath.IsAbs(in.Paths.SelfBinary):
		return plan{}, fail("the deyroute binary path is not absolute (the unit runs \"deyroute wg up\")")
	case b.awg && !filepath.IsAbs(in.Paths.Binary):
		return plan{}, fail("amneziawg-go is not installed (no absolute binary path)")
	}
	keys, err := b.decodeKeys(in.Secrets.Keys)
	if err != nil {
		return plan{}, fail(err.Error())
	}
	maps, localnet, err := buildMaps(in, nodeIP, fail)
	if err != nil {
		return plan{}, err
	}
	hub, node := TunnelAddrs(in.NetIndex, in.Canary)
	return plan{
		iface:         InterfaceName(in.Tunnel.ID, in.NetIndex, in.Canary),
		hubAddr:       hub,
		nodeAddr:      node,
		maps:          maps,
		routeLocalnet: localnet,
		endpoint:      netip.AddrPortFrom(nodeIP, uint16(in.ControlPort)), // #nosec G115 -- checked 1..65535 above
		keys:          keys,
	}, nil
}

// buildMaps validates the port maps. WireGuard forwards at layer 3, so a
// target must be a service on the node itself: 127.0.0.0/8 ("localhost")
// or the node's public IP. Other hosts would need masquerading on the
// node's uplink, which would turn the node into a router for the hub
// (spec section 11); IPv6 targets cannot be reached through the IPv4
// tunnel subnet.
func buildMaps(in backend.RenderInput, nodeIP netip.Addr, fail func(string) error) ([]portMap, bool, error) {
	ports := in.Tunnel.Ports
	if in.Canary && len(ports) > 1 {
		ports = ports[:1]
	}
	if len(ports) == 0 {
		return nil, false, fail("the tunnel has no port maps")
	}
	out := make([]portMap, 0, len(ports))
	seenListen := map[string]bool{}
	targetOf := map[string]netip.Addr{} // proto/port → host
	localnet := false
	for _, p := range ports {
		proto := p.Proto
		if proto == "" {
			proto = config.ProtoTCP
		}
		if proto != config.ProtoTCP && proto != config.ProtoUDP {
			return nil, false, deyerr.New(deyerr.B010, deyerr.Params{"transport": in.Transport.ID(), "proto": proto})
		}
		if p.Listen < 1 || p.Listen > 65535 {
			return nil, false, fail("listen port " + strconv.Itoa(p.Listen) + " is out of range")
		}
		key := proto + "/" + strconv.Itoa(p.Listen)
		if seenListen[key] {
			return nil, false, fail("listen port " + key + " is listed twice")
		}
		seenListen[key] = true
		target := p.Target
		if target == "" {
			target = backend.HostPort("127.0.0.1", p.Listen)
		}
		hostStr, port, ok := backend.SplitTarget(target)
		if !ok {
			return nil, false, fail("invalid target '" + target + "'")
		}
		host, ok := targetHost(hostStr, nodeIP)
		if !ok {
			return nil, false, fail("target " + target + " is not on the node: WireGuard transports forward only to 127.0.0.1 or the node's public IP " + nodeIP.String())
		}
		tkey := proto + "/" + strconv.Itoa(port)
		if prev, dup := targetOf[tkey]; dup && prev != host {
			return nil, false, fail("targets " + prev.String() + " and " + host.String() + " share port " + tkey + "; WireGuard maps each target port to one address")
		}
		targetOf[tkey] = host
		if host.IsLoopback() {
			localnet = true
		}
		out = append(out, portMap{proto: proto, listen: p.Listen, host: host, port: port})
	}
	return out, localnet, nil
}

// targetHost resolves a target host that WireGuard can DNAT to on the node.
func targetHost(h string, nodeIP netip.Addr) (netip.Addr, bool) {
	if h == "localhost" {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1}), true
	}
	a, err := netip.ParseAddr(h)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	a = a.Unmap()
	if !a.Is4() {
		return netip.Addr{}, false
	}
	if a.IsLoopback() || a == nodeIP {
		return a, true
	}
	return netip.Addr{}, false
}

// decodeKeys checks the generated keys (and AWG parameters) in keys.
func (b *Backend) decodeKeys(keys map[string]string) (keySet, error) {
	ks := keySet{
		hubPriv:  keys[KeyHubPrivate],
		hubPub:   keys[KeyHubPublic],
		nodePriv: keys[KeyNodePrivate],
		nodePub:  keys[KeyNodePublic],
	}
	for _, k := range []struct{ name, val string }{
		{KeyHubPrivate, ks.hubPriv}, {KeyHubPublic, ks.hubPub},
		{KeyNodePrivate, ks.nodePriv}, {KeyNodePublic, ks.nodePub},
	} {
		if _, err := decodeKey(k.val); err != nil {
			return ks, errString("generated key " + k.name + " is missing or invalid (run the key generation again)")
		}
	}
	if !publicMatches(ks.hubPriv, ks.hubPub) || !publicMatches(ks.nodePriv, ks.nodePub) {
		return ks, errString("generated WireGuard public keys do not match their private keys")
	}
	if !b.awg {
		return ks, nil
	}
	a, err := awgFromKeys(keys)
	if err != nil {
		return ks, err
	}
	ks.awg = a
	return ks, nil
}

// awgFromKeys parses and checks the AWG parameters.
func awgFromKeys(keys map[string]string) (*AWGParams, error) {
	num := func(name string) (int64, error) {
		v, err := strconv.ParseInt(keys[name], 10, 64)
		if err != nil {
			return 0, errString("AmneziaWG parameter " + name + " is missing or not a number")
		}
		return v, nil
	}
	var vals [9]int64
	for i, name := range []string{KeyJc, KeyJmin, KeyJmax, KeyS1, KeyS2, KeyH1, KeyH2, KeyH3, KeyH4} {
		v, err := num(name)
		if err != nil {
			return nil, err
		}
		if v < 0 || v > 1<<32-1 {
			return nil, errString("AmneziaWG parameter " + name + " is out of range")
		}
		vals[i] = v
	}
	a := &AWGParams{
		Jc: int(vals[0]), Jmin: int(vals[1]), Jmax: int(vals[2]), S1: int(vals[3]), S2: int(vals[4]),
		H1: uint32(vals[5]), H2: uint32(vals[6]), H3: uint32(vals[7]), H4: uint32(vals[8]), // #nosec G115 -- range checked above
	}
	if err := a.check(); err != nil {
		return nil, errString(err.Error())
	}
	return a, nil
}

type errString string

func (e errString) Error() string { return string(e) }
