package xray

import (
	"net"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// The structs below mirror the JSON shape Xray-core v26.3.27 reads
// (infra/conf). Field order is fixed so the output is deterministic.

type xrayConfig struct {
	Comment   string     `json:"_comment,omitempty"`
	Log       logConfig  `json:"log"`
	Inbounds  []inbound  `json:"inbounds"`
	Outbounds []outbound `json:"outbounds"`
	Routing   *routing   `json:"routing,omitempty"`
}

type logConfig struct {
	LogLevel string `json:"loglevel"`
	Access   string `json:"access"`
}

type inbound struct {
	Tag            string  `json:"tag"`
	Listen         string  `json:"listen"`
	Port           int     `json:"port"`
	Protocol       string  `json:"protocol"`
	Settings       any     `json:"settings"`
	StreamSettings *stream `json:"streamSettings,omitempty"`
}

type outbound struct {
	Tag            string  `json:"tag"`
	Protocol       string  `json:"protocol"`
	Settings       any     `json:"settings,omitempty"`
	StreamSettings *stream `json:"streamSettings,omitempty"`
	Mux            *mux    `json:"mux,omitempty"`
}

// dokodemoSettings is the dokodemo-door ("tunnel") inbound: every
// connection is sent to the fixed target.
type dokodemoSettings struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	Network string `json:"network"`
}

type vlessOutSettings struct {
	Vnext []vnext `json:"vnext"`
}

type vnext struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []vlessUser `json:"users"`
}

type vlessUser struct {
	ID         string `json:"id"`
	Encryption string `json:"encryption"`
	Flow       string `json:"flow"`
}

type vlessInSettings struct {
	Clients    []vlessClient `json:"clients"`
	Decryption string        `json:"decryption"`
}

// vlessClient must not carry "encryption" (Xray rejects it on inbounds).
type vlessClient struct {
	ID   string `json:"id"`
	Flow string `json:"flow"`
}

type stream struct {
	Network         string          `json:"network"`
	Security        string          `json:"security"`
	RealitySettings realitySettings `json:"realitySettings"`
}

// realitySettings holds both roles; unused fields are omitted. v26.3.27
// names the server field "target" (alias "dest") and the client public key
// "password" (alias "publicKey").
type realitySettings struct {
	Show        *bool    `json:"show,omitempty"`
	Target      string   `json:"target,omitempty"`
	Xver        *int     `json:"xver,omitempty"`
	ServerNames []string `json:"serverNames,omitempty"`
	PrivateKey  string   `json:"privateKey,omitempty"`
	ShortIDs    []string `json:"shortIds,omitempty"`

	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Password    string `json:"password,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

// freedomSettings pins a freedom outbound to one destination ("host:port");
// freedom then overrides the address of every TCP dial and UDP packet.
type freedomSettings struct {
	Redirect string `json:"redirect"`
}

type mux struct {
	Enabled bool `json:"enabled"`
}

type routing struct {
	DomainStrategy string `json:"domainStrategy"`
	Rules          []rule `json:"rules"`
}

type rule struct {
	RuleTag     string   `json:"ruleTag,omitempty"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	Port        string   `json:"port,omitempty"`
	Network     string   `json:"network"`
	OutboundTag string   `json:"outboundTag"`
}

func comment(in backend.RenderInput, side backend.Side) string {
	return "Rendered by deyroute for tunnel " + in.Tunnel.ID + ", node " + in.Node.ID +
		", transport " + in.Transport.ID() + " (" + side.String() + " side). Regenerated on every apply; do not edit."
}

// inboundTagFor is "in-<proto>-<listen>" (spec: one inbound per port map).
func inboundTagFor(m mapping) string {
	return "in-" + backend.ServiceName(m.proto, m.listen)
}

// hubFlow is the VLESS flow of the hub client. Vision rejects UDP to port
// 443 (QUIC) unless the "-udp443" variant is used; the node side keeps plain
// xtls-rprx-vision, which accepts both.
func hubFlow(p planned) string {
	for _, m := range p.maps {
		if m.proto == config.ProtoUDP && m.port == 443 {
			return flowVisionUDP443
		}
	}
	return flowVision
}

// renderHub renders one dokodemo-door inbound per port map and the VLESS +
// REALITY outbound to the node.
func renderHub(in backend.RenderInput, p planned) (backend.Rendered, error) {
	cfg := xrayConfig{
		Comment: comment(in, backend.SideHub),
		Log:     logConfig{LogLevel: logLevel, Access: "none"},
	}
	binds := make([]backend.PortUse, 0, len(p.maps))
	for _, m := range p.maps {
		cfg.Inbounds = append(cfg.Inbounds, inbound{
			Tag:      inboundTagFor(m),
			Listen:   p.listenAddr,
			Port:     m.listen,
			Protocol: "dokodemo-door",
			Settings: dokodemoSettings{Address: m.host, Port: m.port, Network: m.proto},
		})
		binds = append(binds, backend.PortUse{Port: m.listen, Proto: m.proto, Addr: p.listenAddr, Purpose: "user"})
	}
	cfg.Outbounds = []outbound{{
		Tag:      outboundNode,
		Protocol: "vless",
		Settings: vlessOutSettings{Vnext: []vnext{{
			Address: in.Node.PublicIP,
			Port:    in.ControlPort,
			Users:   []vlessUser{{ID: p.keys.uuid, Encryption: "none", Flow: hubFlow(p)}},
		}}},
		StreamSettings: &stream{
			Network:  "raw",
			Security: "reality",
			RealitySettings: realitySettings{
				ServerName:  p.decoyHost,
				Fingerprint: fingerprint,
				Password:    p.keys.public,
				ShortID:     p.keys.shortID,
				SpiderX:     "/",
			},
		},
		// Mux is incompatible with Vision (spec 7.5). UDP port maps still
		// use XUDP, which Vision enables on its own.
		Mux: &mux{Enabled: false},
	}}
	return finish(in, cfg, binds)
}

// renderNode renders the VLESS + REALITY inbound, freedom/blackhole
// outbounds and the routing allow-list.
func renderNode(in backend.RenderInput, p planned) (backend.Rendered, error) {
	show := false
	xver := 0
	cfg := xrayConfig{
		Comment: comment(in, backend.SideNode),
		Log:     logConfig{LogLevel: logLevel, Access: "none"},
		Inbounds: []inbound{{
			Tag:      inboundTag,
			Listen:   "0.0.0.0",
			Port:     in.ControlPort,
			Protocol: "vless",
			Settings: vlessInSettings{
				Clients:    []vlessClient{{ID: p.keys.uuid, Flow: flowVision}},
				Decryption: "none",
			},
			StreamSettings: &stream{
				Network:  "raw",
				Security: "reality",
				RealitySettings: realitySettings{
					Show:        &show,
					Target:      p.decoyAddr,
					Xver:        &xver,
					ServerNames: []string{p.decoyHost},
					PrivateKey:  p.keys.private,
					ShortIDs:    []string{p.keys.shortID},
				},
			},
		}},
	}
	rules, direct := allowRules(p.maps)
	// The first outbound is Xray's default; block comes first so that
	// anything the rules do not match is dropped even without the catch-all
	// rule. Every allowed target has its own pinned freedom outbound.
	cfg.Outbounds = append([]outbound{{Tag: outboundBlock, Protocol: "blackhole"}}, direct...)
	cfg.Routing = &routing{DomainStrategy: "AsIs", Rules: rules}
	binds := []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: "0.0.0.0", Purpose: "control"}}
	return finish(in, cfg, binds)
}

// allowRules builds the node routing allow-list and its outbounds: one rule
// per distinct (host, port, network) target of the tunnel, each routed to
// its own freedom outbound whose "redirect" pins the destination to exactly
// that target, then a catch-all to "block". Only the exact target pairs are
// reachable, which is stricter than the 127.0.0.0/8 bound of spec section 11
// (see docs/backends/xray.md).
//
// The per-target redirect matters for UDP: Vision carries UDP as XUDP, and
// XUDP "Keep" frames may name a new destination for every packet
// (common/mux/frame.go, full-cone). Routing only sees the first destination
// of a session and freedom would send later packets wherever the frame says;
// with "redirect" freedom overrides every packet's address and port
// (proxy/freedom: UDPOverride), so an allowed session cannot reach
// 8.8.8.8:53 or any other host.
func allowRules(maps []mapping) ([]rule, []outbound) {
	type key struct {
		host  string
		port  int
		proto string
	}
	seen := map[key]bool{}
	var keysList []key
	for _, m := range maps {
		k := key{m.host, m.port, m.proto}
		if !seen[k] {
			seen[k] = true
			keysList = append(keysList, k)
		}
	}
	sort.SliceStable(keysList, func(i, j int) bool {
		a, b := keysList[i], keysList[j]
		if a.host != b.host {
			return a.host < b.host
		}
		if a.port != b.port {
			return a.port < b.port
		}
		return a.proto < b.proto
	})
	rules := make([]rule, 0, len(keysList)+1)
	outs := make([]outbound, 0, len(keysList))
	for _, k := range keysList {
		target := backend.HostPort(k.host, k.port)
		tag := outboundDirect + "-" + k.proto + "-" + target
		r := rule{
			RuleTag:     "allow-" + k.proto + "-" + target,
			InboundTag:  []string{inboundTag},
			Port:        strconv.Itoa(k.port),
			Network:     k.proto,
			OutboundTag: tag,
		}
		if net.ParseIP(k.host) != nil {
			r.IP = []string{k.host}
		} else {
			r.Domain = []string{"full:" + k.host}
		}
		rules = append(rules, r)
		outs = append(outs, outbound{Tag: tag, Protocol: "freedom", Settings: freedomSettings{Redirect: target}})
	}
	rules = append(rules, rule{RuleTag: "deny-all", Network: "tcp,udp", OutboundTag: outboundBlock})
	return rules, outs
}

func finish(in backend.RenderInput, cfg xrayConfig, binds []backend.PortUse) (backend.Rendered, error) {
	data, err := backend.JSONIndent(cfg)
	if err != nil {
		return backend.Rendered{}, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": Reality})
	}
	return backend.Rendered{
		Files: map[string][]byte{ConfigFile: data},
		Unit: backend.UnitSpec{
			// "xray run -c <file>" (main/run.go of the pinned version).
			ExecStart:        []string{in.Paths.Binary, "run", "-c", filepath.Join(in.Paths.ConfigDir, ConfigFile)},
			WorkingDirectory: in.Paths.ConfigDir,
		},
		Binds: binds,
	}, nil
}
