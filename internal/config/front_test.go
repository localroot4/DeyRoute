package config

import (
	"os"
	"path/filepath"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

// frontHub returns a valid hub with the front enabled on 8443 (443 and 2053
// are used by the tunnel of validHub).
func frontHub() *Config {
	c := validHub()
	c.Hub.Front = HubFront{Enabled: true, Domain: "front.example.com", Port: 8443}
	return c
}

// fpm returns a complete port map.
func fpm(listen int, proto string) PortMap {
	return PortMap{Listen: listen, Proto: proto, Target: DefaultTarget(listen), Probe: ProbeAuto}
}

// frontNode returns a valid node in front mode.
func frontNode() *Config {
	c := NewNode("de-1", "front.example.com:8443", testFP)
	c.Node.Front = NodeFront{SecretFile: "front.secret"}
	return c
}

func TestCloudflarePorts(t *testing.T) {
	require.Equal(t, []int{443, 2053, 2083, 2087, 2096, 8443}, CloudflareHTTPSPorts())
	require.Equal(t, []int{80, 8080, 8880, 2052, 2082, 2086, 2095}, CloudflareHTTPPorts())

	for _, p := range CloudflareHTTPSPorts() {
		require.True(t, IsCloudflareHTTPSPort(p), p)
		require.True(t, IsCloudflarePort(p), p)
		require.Equal(t, "wss", FrontSchemeForPort(p), p)
	}
	for _, p := range CloudflareHTTPPorts() {
		require.False(t, IsCloudflareHTTPSPort(p), p)
		require.True(t, IsCloudflarePort(p), p)
		require.Equal(t, "ws", FrontSchemeForPort(p), p)
	}
	for _, p := range []int{0, 22, 1, 444, 8444, 44433, 30000, 65535, -443} {
		require.False(t, IsCloudflareHTTPSPort(p), p)
		require.False(t, IsCloudflarePort(p), p)
		require.Equal(t, "", FrontSchemeForPort(p), p)
	}

	// The returned slices are copies.
	s := CloudflareHTTPSPorts()
	s[0] = 1
	require.Equal(t, 443, CloudflareHTTPSPorts()[0])
	h := CloudflareHTTPPorts()
	h[0] = 1
	require.Equal(t, 80, CloudflareHTTPPorts()[0])
}

func TestFrontHelpers(t *testing.T) {
	require.Equal(t, "front", RouteFront)

	var f HubFront
	require.True(t, f.CFOnlyOrDefault(), "nil = true")
	no, yes := false, true
	f.CFOnly = &no
	require.False(t, f.CFOnlyOrDefault())
	f.CFOnly = &yes
	require.True(t, f.CFOnlyOrDefault())

	require.Equal(t, "auto", HubFront{}.TLSMode())
	for _, m := range []string{"auto", "custom", "off"} {
		require.Equal(t, m, HubFront{TLS: m}.TLSMode())
	}
	require.Equal(t, DefaultFrontSecretFile, HubFront{}.SecretFileOrDefault())
	require.Equal(t, "x.secret", HubFront{SecretFile: "x.secret"}.SecretFileOrDefault())

	require.False(t, NodeSelf{}.FrontMode())
	require.False(t, NodeSelf{Front: NodeFront{Scheme: "wss"}}.FrontMode())
	require.True(t, NodeSelf{Front: NodeFront{SecretFile: "front.secret"}}.FrontMode())
}

func TestHubFrontPort(t *testing.T) {
	var nilHub *Hub
	require.Equal(t, 0, nilHub.FrontPort())
	require.Nil(t, nilHub.ReservedPorts())

	h := &Hub{Front: HubFront{Port: 2083}}
	require.Equal(t, 0, h.FrontPort(), "disabled front reserves nothing")
	require.Nil(t, h.ReservedPorts())
	h.Front.Enabled = true
	require.Equal(t, 2083, h.FrontPort())
	require.Equal(t, []int{2083}, h.ReservedPorts())
}

func TestValidateFrontValid(t *testing.T) {
	no := false
	cases := map[string]func(c *Config){
		"minimal":  func(c *Config) {},
		"tls auto": func(c *Config) { c.Hub.Front.TLS = "auto" },
		"tls off":  func(c *Config) { c.Hub.Front.TLS = "off" },
		"tls custom": func(c *Config) {
			c.Hub.Front.TLS, c.Hub.Front.CertFile, c.Hub.Front.KeyFile = "custom", "/etc/deyroute/secrets/f.crt", "/etc/deyroute/secrets/f.key"
		},
		"http port off": func(c *Config) { c.Hub.Front.Port, c.Hub.Front.TLS = 8080, "off" },
		"secret name":   func(c *Config) { c.Hub.Front.SecretFile = "front.secret" },
		"cf_only false": func(c *Config) { c.Hub.Front.CFOnly = &no },
		"trusted v4 v6": func(c *Config) {
			c.Hub.Front.TrustedProxies = []string{"203.0.113.0/24", "2001:db8::/32", "10.0.0.1/32"}
		},
		"another https port": func(c *Config) { c.Tunnels[0].Ports = []PortMap{fpm(1000, ProtoTCP)}; c.Hub.Front.Port = 2087 },
		"disabled, empty":    func(c *Config) { c.Hub.Front = HubFront{} },
		"disabled keeps settings": func(c *Config) {
			c.Hub.Front.Enabled = false
			c.Hub.Front.Port = 2053 // a tunnel may use it while the front is off
		},
		"disabled, tls on http port": func(c *Config) { c.Hub.Front = HubFront{Domain: "front.example.com", Port: 80} },
		"front domain equals hub.domain": func(c *Config) {
			c.Hub.Domain = "front.example.com"
		},
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := frontHub()
			mut(c)
			require.NoError(t, c.Validate(fakeOpts()))
		})
	}
}

func TestValidateFrontInvalid(t *testing.T) {
	cases := []vcase{
		{name: "domain required", base: frontHub, mut: func(c *Config) { c.Hub.Front.Domain = "" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.domain"},
		{name: "domain ip", base: frontHub, mut: func(c *Config) { c.Hub.Front.Domain = "1.2.3.4" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.domain"},
		{name: "domain single label", base: frontHub, mut: func(c *Config) { c.Hub.Front.Domain = "localhost" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.domain"},
		{name: "domain bad chars", base: frontHub, mut: func(c *Config) { c.Hub.Front.Domain = "bad_name.example.com" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.domain"},
		{name: "domain with scheme", base: frontHub, mut: func(c *Config) { c.Hub.Front.Domain = "https://front.example.com" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.domain"},
		{name: "domain shape checked while disabled", base: frontHub, mut: func(c *Config) { c.Hub.Front.Enabled, c.Hub.Front.Domain = false, "1.2.3.4" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.domain"},
		{name: "port required", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = 0 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "port not cloudflare", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = 8444 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "port 22", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = 22 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "port control", base: frontHub, mut: func(c *Config) { c.Hub.ControlPort = 8443 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "port in ctl range", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = 30500 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "port negative", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = -443 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "port shape checked while disabled", base: frontHub, mut: func(c *Config) { c.Hub.Front.Enabled, c.Hub.Front.Port = false, 9999 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.port"},
		{name: "tls bogus", base: frontHub, mut: func(c *Config) { c.Hub.Front.TLS = "acme" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.tls"},
		{name: "tls on http port (empty = auto)", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = 8080 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.tls"},
		{name: "tls auto on http port", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port, c.Hub.Front.TLS = 80, "auto" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.tls"},
		{name: "tls custom on http port", base: frontHub, mut: func(c *Config) {
			c.Hub.Front.Port, c.Hub.Front.TLS = 2095, "custom"
			c.Hub.Front.CertFile, c.Hub.Front.KeyFile = "/c.crt", "/c.key"
		}, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.tls"},
		{name: "custom needs both files", base: frontHub, mut: func(c *Config) { c.Hub.Front.TLS = "custom" }, codes: []deyerr.Code{deyerr.C013, deyerr.C013}},
		{name: "custom needs key", base: frontHub, mut: func(c *Config) {
			c.Hub.Front.TLS, c.Hub.Front.CertFile = "custom", "/etc/deyroute/secrets/f.crt"
		}, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.key_file"},
		{name: "custom needs cert", base: frontHub, mut: func(c *Config) {
			c.Hub.Front.TLS, c.Hub.Front.KeyFile = "custom", "/etc/deyroute/secrets/f.key"
		}, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.cert_file"},
		{name: "cert relative", base: frontHub, mut: func(c *Config) { c.Hub.Front.CertFile = "f.crt" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.cert_file"},
		{name: "key relative", base: frontHub, mut: func(c *Config) { c.Hub.Front.KeyFile = "f.key" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.key_file"},
		{name: "secret with dir", base: frontHub, mut: func(c *Config) { c.Hub.Front.SecretFile = "/etc/deyroute/secrets/front.secret" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.secret_file"},
		{name: "secret dotdot", base: frontHub, mut: func(c *Config) { c.Hub.Front.SecretFile = ".." }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.secret_file"},
		{name: "secret relative path", base: frontHub, mut: func(c *Config) { c.Hub.Front.SecretFile = "a/b" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.secret_file"},
		{name: "secret control char", base: frontHub, mut: func(c *Config) { c.Hub.Front.SecretFile = "a\nb" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.secret_file"},
		{name: "trusted not cidr", base: frontHub, mut: func(c *Config) { c.Hub.Front.TrustedProxies = []string{"203.0.113.0/24", "nope"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.trusted_proxies[1]"},
		{name: "trusted bare ip", base: frontHub, mut: func(c *Config) { c.Hub.Front.TrustedProxies = []string{"203.0.113.7"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.trusted_proxies[0]"},
		{name: "trusted zone", base: frontHub, mut: func(c *Config) { c.Hub.Front.TrustedProxies = []string{"fe80::1%eth0/64"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.front.trusted_proxies[0]"},
		// C011: the front port is reserved against tunnel listens.
		{name: "tunnel on front port", base: frontHub, mut: func(c *Config) { c.Tunnels[0].Ports = []PortMap{fpm(8443, ProtoTCP)} }, codes: []deyerr.Code{deyerr.C011}},
		{name: "tunnel udp on front port", base: frontHub, mut: func(c *Config) { c.Tunnels[0].Ports = []PortMap{fpm(8443, ProtoUDP)} }, codes: []deyerr.Code{deyerr.C011}},
		{name: "tunnel on front port 443", base: frontHub, mut: func(c *Config) { c.Hub.Front.Port = 443 }, codes: []deyerr.Code{deyerr.C011}},
	}
	runValidateCases(t, cases)
}

func TestValidateFrontPortReservedReason(t *testing.T) {
	c := frontHub()
	c.Tunnels[0].Ports = []PortMap{fpm(8443, ProtoTCP)}
	e := firstErr(t, c.Validate(fakeOpts()))
	require.Equal(t, deyerr.C011, e.Code)
	require.Contains(t, e.Params["reason"], "hub front port")

	// Disabled front: the port is free for tunnels.
	c.Hub.Front.Enabled = false
	require.NoError(t, c.Validate(fakeOpts()))

	// ReservedPorts from the options still combine with the front port.
	c = frontHub()
	c.Tunnels[0].Ports = []PortMap{fpm(2083, ProtoTCP)}
	opts := fakeOpts()
	opts.ReservedPorts = []int{2083}
	requireCodes(t, c.Validate(opts), deyerr.C011)
	c.Tunnels[0].Ports = []PortMap{fpm(8443, ProtoTCP)}
	requireCodes(t, c.Validate(opts), deyerr.C011)
}

func TestValidateFrontSecretNotPrinted(t *testing.T) {
	c := frontHub()
	c.Hub.Front.SecretFile = "/etc/x/AbCdEfGhIjKlMnOpQrStUv"
	e := firstErr(t, c.Validate(fakeOpts()))
	require.Equal(t, deyerr.C013, e.Code)
	require.NotContains(t, e.Format(false), "AbCdEfGhIjKlMnOpQrStUv")
}

func TestValidateNodeRoute(t *testing.T) {
	runValidateCases(t, []vcase{
		{name: "route empty", mut: func(c *Config) { c.Nodes[0].Route = "" }},
		{name: "route front", mut: func(c *Config) { c.Nodes[0].Route = RouteFront }},
		{name: "route front keeps a valid ip", mut: func(c *Config) { c.Nodes[0].Route = RouteFront; c.Nodes[0].PublicIP = "9.9.9.9" }},
		{name: "route front empty ip", mut: func(c *Config) { c.Nodes[0].Route, c.Nodes[0].PublicIP = RouteFront, "" }},
		{name: "route front bad ip", mut: func(c *Config) { c.Nodes[0].Route, c.Nodes[0].PublicIP = RouteFront, "not-an-ip" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].public_ip"},
		{name: "direct empty ip", mut: func(c *Config) { c.Nodes[0].PublicIP = "" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].public_ip"},
		{name: "route bogus", mut: func(c *Config) { c.Nodes[0].Route = "cdn" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].route"},
		{name: "route bogus and empty ip", mut: func(c *Config) { c.Nodes[0].Route, c.Nodes[0].PublicIP = "direct", "" }, codes: []deyerr.Code{deyerr.C013, deyerr.C013}},
		{name: "route case sensitive", mut: func(c *Config) { c.Nodes[0].Route = "Front" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].route"},
	})
}

func TestValidateNodeFront(t *testing.T) {
	ok := []func(c *Config){
		func(c *Config) {},
		func(c *Config) { c.Node.Front.Scheme = "wss" },
		func(c *Config) { c.Node.Front.Scheme = "ws"; c.Node.HubAddr = "front.example.com:80" },
		func(c *Config) { c.Node.Front.EdgeIP = "104.16.0.1" },
		func(c *Config) { c.Node.Front.EdgeIP = "2606:4700::1" },
		func(c *Config) { c.Node.HubAddr = "front.example.com:2053" },
		func(c *Config) { c.Node.HubAddr, c.Node.Front.Scheme = "front.example.com:9443", "wss" }, // lab port with an explicit scheme
		func(c *Config) { c.Node.Front.SecretFile = "other.secret" },
	}
	for i, mut := range ok {
		c := frontNode()
		mut(c)
		require.NoError(t, c.Validate(fakeOpts()), "case %d", i)
	}

	runValidateCases(t, []vcase{
		{name: "scheme bogus", base: frontNode, mut: func(c *Config) { c.Node.Front.Scheme = "https" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.scheme"},
		{name: "scheme not derivable", base: frontNode, mut: func(c *Config) { c.Node.HubAddr = "front.example.com:9443" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.scheme"},
		{name: "edge_ip bad", base: frontNode, mut: func(c *Config) { c.Node.Front.EdgeIP = "cf.example.com" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.edge_ip"},
		{name: "edge_ip unspecified", base: frontNode, mut: func(c *Config) { c.Node.Front.EdgeIP = "0.0.0.0" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.edge_ip"},
		{name: "secret with slash", base: frontNode, mut: func(c *Config) { c.Node.Front.SecretFile = "/etc/deyroute/secrets/front.secret" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.secret_file"},
		{name: "secret dot", base: frontNode, mut: func(c *Config) { c.Node.Front.SecretFile = "." }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.secret_file"},
		{name: "scheme without secret", base: validNode, mut: func(c *Config) { c.Node.Front.Scheme = "wss" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.scheme"},
		{name: "edge_ip without secret", base: validNode, mut: func(c *Config) { c.Node.Front.EdgeIP = "104.16.0.1" }, codes: []deyerr.Code{deyerr.C013}, field: "node.front.edge_ip"},
		{name: "hub_addr still required", base: frontNode, mut: func(c *Config) { c.Node.HubAddr = "front.example.com" }, codes: []deyerr.Code{deyerr.C013}, field: "node.hub_addr"},
	})
}

func TestFrontGoldenHub(t *testing.T) {
	opts := fakeOpts()
	c := validHub()
	c.Hub.Domain = "t.example.com"
	no := false
	c.Hub.Front = HubFront{
		Enabled:        true,
		Domain:         "front.example.com",
		Port:           8443,
		TLS:            "auto",
		SecretFile:     "front.secret",
		CFOnly:         &no,
		TrustedProxies: []string{"203.0.113.0/24"},
	}
	c.Nodes[0].Route = RouteFront
	c.Nodes[0].PublicIP = ""

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveWith(path, c, opts))
	back, err := LoadWith(path, opts)
	require.NoError(t, err)
	require.Equal(t, c, back)

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	golden(t, "hub_front.saved.golden", saved)

	// Saving the reloaded config is byte-identical.
	path2 := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveWith(path2, back, opts))
	saved2, err := os.ReadFile(path2)
	require.NoError(t, err)
	require.Equal(t, string(saved), string(saved2))
}

func TestFrontGoldenNode(t *testing.T) {
	c := frontNode()
	c.Node.Front.Scheme = "wss"
	c.Node.Front.EdgeIP = "104.16.0.1"

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, Save(path, c))
	back, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, c, back)
	require.True(t, back.Node.FrontMode())

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	golden(t, "node_front.saved.golden", saved)
}

func TestFrontMinimalHubKeys(t *testing.T) {
	// Only the keys that are set are written (omitempty), and the TLS
	// default is not materialized.
	c := frontHub()
	b, err := Marshal(c)
	require.NoError(t, err)
	require.Contains(t, string(b), "  front:\n    enabled: true\n    domain: front.example.com\n    port: 8443\n")
	for _, k := range []string{"tls:\n    ", "cf_only", "trusted_proxies", "secret_file", "route"} {
		require.NotContains(t, string(b), "    "+k+":", k)
	}
}

// TestOldConfigsUnchanged proves a config without front keys neither gains
// keys on save nor changes on a second save.
func TestOldConfigsUnchanged(t *testing.T) {
	for _, tc := range []struct{ in, golden string }{
		{"hub_sample.yaml", "hub_sample.saved.golden"},
		{"node.saved.golden", "node.saved.golden"}, // the sample's placeholder pin is not loadable
	} {
		raw := readTestdata(t, tc.in)
		c, err := ParseWith(raw, fakeOpts())
		require.NoError(t, err, tc.in)
		if c.Hub != nil {
			require.Zero(t, c.Hub.Front.Enabled)
			require.Nil(t, c.Hub.Front.CFOnly)
			require.Nil(t, c.Hub.Front.TrustedProxies)
		}
		if c.Node != nil {
			require.False(t, c.Node.FrontMode())
			require.Equal(t, NodeFront{}, c.Node.Front)
		}
		out, err := Marshal(c)
		require.NoError(t, err)
		want, err := os.ReadFile(filepath.Join("testdata", tc.golden))
		require.NoError(t, err)
		require.Equal(t, string(want), string(out), tc.in)
		for _, k := range []string{"front", "route", "edge_ip", "cf_only"} {
			require.NotContains(t, string(out), k+":", tc.in)
		}
		// And a Clone keeps the zero value.
		require.Equal(t, c, Clone(c))
	}
}

func TestFrontUnknownKeyStillStrict(t *testing.T) {
	_, err := Parse([]byte(`schema_version: 1
role: node
node:
  id: de-1
  hub_addr: front.example.com:443
  hub_ca_fingerprint: ` + testFP + `
  cert_file: /etc/deyroute/secrets/node.crt
  key_file: /etc/deyroute/secrets/node.key
  front:
    secret_file: front.secret
    bogus: 1
`))
	requireCodes(t, err, deyerr.C001)
}
