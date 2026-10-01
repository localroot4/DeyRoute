package gost

import (
	"bytes"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// The structs below mirror the subset of github.com/go-gost/x v0.8.1
// config.Config (config/config.go) that deyroute renders. gost reads the file
// through viper, which matches keys case-insensitively; the spelling used
// here is the one of the upstream documentation and `gost -O yaml`.
type gostConfig struct {
	Services []service `yaml:"services"`
	Chains   []chain   `yaml:"chains,omitempty"`
	Bypasses []bypass  `yaml:"bypasses,omitempty"`
	Log      logConfig `yaml:"log"`
}

type service struct {
	Name      string     `yaml:"name"`
	Addr      string     `yaml:"addr"`
	Bypass    string     `yaml:"bypass,omitempty"`
	Handler   handler    `yaml:"handler"`
	Listener  listener   `yaml:"listener"`
	Forwarder *forwarder `yaml:"forwarder,omitempty"`
}

type handler struct {
	Type     string         `yaml:"type"`
	Auth     *auth          `yaml:"auth,omitempty"`
	Metadata map[string]any `yaml:"metadata,omitempty"`
}

type listener struct {
	Type     string         `yaml:"type"`
	Chain    string         `yaml:"chain,omitempty"`
	TLS      *tlsConfig     `yaml:"tls,omitempty"`
	Metadata map[string]any `yaml:"metadata,omitempty"`
}

type forwarder struct {
	Nodes []forwardNode `yaml:"nodes"`
}

type forwardNode struct {
	Name string `yaml:"name"`
	Addr string `yaml:"addr"`
}

type chain struct {
	Name string `yaml:"name"`
	Hops []hop  `yaml:"hops"`
}

type hop struct {
	Name  string `yaml:"name"`
	Nodes []node `yaml:"nodes"`
}

type node struct {
	Name      string    `yaml:"name"`
	Addr      string    `yaml:"addr"`
	Connector connector `yaml:"connector"`
	Dialer    dialer    `yaml:"dialer"`
}

type connector struct {
	Type string `yaml:"type"`
	Auth *auth  `yaml:"auth,omitempty"`
}

type dialer struct {
	Type     string         `yaml:"type"`
	TLS      *tlsConfig     `yaml:"tls,omitempty"`
	Metadata map[string]any `yaml:"metadata,omitempty"`
}

type tlsConfig struct {
	CertFile   string      `yaml:"certFile,omitempty"`
	KeyFile    string      `yaml:"keyFile,omitempty"`
	CAFile     string      `yaml:"caFile,omitempty"`
	Secure     bool        `yaml:"secure,omitempty"`
	ServerName string      `yaml:"serverName,omitempty"`
	Options    *tlsOptions `yaml:"options,omitempty"`
}

type tlsOptions struct {
	MinVersion string `yaml:"minVersion"`
}

type auth struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type bypass struct {
	Name      string   `yaml:"name"`
	Whitelist bool     `yaml:"whitelist"`
	Matchers  []string `yaml:"matchers,omitempty"`
}

type logConfig struct {
	Output string `yaml:"output"`
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// tls13 is the TLS policy of spec section 10 (TLS 1.3 only).
var tls13 = &tlsOptions{MinVersion: "VersionTLS13"}

// logSettings sends gost's log to stderr, which the unit appends to the
// tunnel log.
var logSettings = logConfig{Output: "stderr", Level: "info", Format: "text"}

// hubBypass returns the bypass of the hub relay service: for TCP-only
// tunnels a whitelist without entries, which refuses every CONNECT; for
// tunnels with UDP port maps a blacklist of unspecified and link-local
// addresses, because gost also filters UDP BIND datagrams by the client
// address with the same bypass (see denyLocal).
func hubBypass(maps []portMap) bypass {
	for _, m := range maps {
		if m.proto != config.ProtoUDP {
			continue
		}
		return bypass{Name: denyLocal, Matchers: append([]string(nil), localMatchers...)}
	}
	// A whitelist bypass with no matchers bypasses every address.
	return bypass{Name: denyConnect, Whitelist: true}
}

// renderHub renders the hub: one relay handler behind a wss listener on the
// control port. BIND is enabled (the nodes' rtcp/rudp listeners bind the
// user ports through it) and CONNECT is restricted by hubBypass, so the
// relay cannot be used as a proxy into the hub.
func renderHub(in backend.RenderInput, maps []portMap) (backend.Rendered, error) {
	s := in.Secrets
	bp := hubBypass(maps)
	cfg := gostConfig{
		Services: []service{{
			Name:   hubService,
			Addr:   backend.HostPort("0.0.0.0", in.ControlPort),
			Bypass: bp.Name,
			Handler: handler{
				Type:     "relay",
				Auth:     &auth{Username: User, Password: s.Token},
				Metadata: map[string]any{"bind": true},
			},
			Listener: listener{
				Type:     "wss",
				TLS:      &tlsConfig{CertFile: s.TLSCertFile, KeyFile: s.TLSKeyFile, Options: tls13},
				Metadata: map[string]any{"path": wsPath(s.Token)},
			},
		}},
		Bypasses: []bypass{bp},
		Log:      logSettings,
	}
	data, err := marshal(in, cfg)
	if err != nil {
		return backend.Rendered{}, err
	}
	addr := listenAddr(in)
	binds := []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: "0.0.0.0", Purpose: "control"}}
	for _, m := range maps {
		binds = append(binds, backend.PortUse{Port: m.listen, Proto: m.proto, Addr: addr, Purpose: "user"})
	}
	return backend.Rendered{
		Files: map[string][]byte{ConfigFile: data},
		Unit:  unit(in),
		Binds: binds,
	}, nil
}

// renderNode renders the node: per port map a remote forward service whose
// listener binds <ListenAddr>:<listen> on the hub through the relay+wss
// chain, and whose forwarder dials exactly the port map's target. The node
// binds nothing and dials nothing else (spec section 11).
func renderNode(in backend.RenderInput, maps []portMap) (backend.Rendered, error) {
	s := in.Secrets
	addr := listenAddr(in)
	cfg := gostConfig{Log: logSettings}
	for _, m := range maps {
		typ := "rtcp"
		var md map[string]any
		if m.proto == config.ProtoUDP {
			typ = "rudp"
			md = map[string]any{"ttl": udpTTL}
		}
		cfg.Services = append(cfg.Services, service{
			Name:      m.name,
			Addr:      backend.HostPort(addr, m.listen),
			Handler:   handler{Type: typ},
			Listener:  listener{Type: typ, Chain: hubChain, Metadata: md},
			Forwarder: &forwarder{Nodes: []forwardNode{{Name: "target", Addr: m.target}}},
		})
	}
	dt := &tlsConfig{CAFile: s.CAFile, Options: tls13}
	if s.ServerName != "" {
		// Full verification (chain + name) against the tunnel CA.
		dt.Secure, dt.ServerName = true, s.ServerName
	}
	// Without a server name gost still verifies the chain against caFile
	// (x/internal/util/tls.LoadClientConfig: VerifyConnection with Roots).
	cfg.Chains = []chain{{
		Name: hubChain,
		Hops: []hop{{
			Name: "hop-0",
			Nodes: []node{{
				Name:      "hub",
				Addr:      backend.HostPort(in.Hub.PublicIP, in.ControlPort),
				Connector: connector{Type: "relay", Auth: &auth{Username: User, Password: s.Token}},
				Dialer:    dialer{Type: "wss", TLS: dt, Metadata: map[string]any{"path": wsPath(s.Token)}},
			}},
		}},
	}}
	data, err := marshal(in, cfg)
	if err != nil {
		return backend.Rendered{}, err
	}
	return backend.Rendered{
		Files: map[string][]byte{ConfigFile: data},
		Unit:  unit(in),
	}, nil
}

// marshal renders the YAML with a provenance header.
func marshal(in backend.RenderInput, cfg gostConfig) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("# Rendered by deyroute for tunnel " + in.Tunnel.ID + ", node " + in.Node.ID +
		", transport " + in.Transport.ID() + ".\n")
	b.WriteString("# Regenerated from /etc/deyroute/config.yaml on every apply; do not edit.\n")
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": RelayWSS})
	}
	if err := enc.Close(); err != nil {
		return nil, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": RelayWSS})
	}
	return b.Bytes(), nil
}

// unit runs "gost -C <ConfigDir>/gost.yaml". The upstream linux release
// binaries are UPX-compressed (.goreleaser.yaml "upx" for linux
// amd64/arm64/arm), and the UPX loader makes its unpacked code executable
// at start, which MemoryDenyWriteExecute forbids.
func unit(in backend.RenderInput) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{in.Paths.Binary, "-C", filepath.Join(in.Paths.ConfigDir, ConfigFile)},
		WorkingDirectory: in.Paths.ConfigDir,
		DropHardening: map[string]string{
			"MemoryDenyWriteExecute": "gost release binaries are UPX-packed; the UPX loader maps the unpacked code executable at start",
		},
	}
}
