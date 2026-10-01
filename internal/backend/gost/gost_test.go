package gost

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixture is the fixed RenderInput of the golden tests.
func fixture(t *testing.T) backend.RenderInput {
	t.Helper()
	dir := "/etc/deyroute/backends/gost/main/de-1/relay-wss"
	return backend.RenderInput{
		Tunnel: config.Tunnel{
			ID: "main", Name: "main", Enabled: true, Nodes: []string{"de-1"},
			Ports: []config.PortMap{
				{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"},
				{Listen: 2053, Proto: "tcp", Target: "127.0.0.1:2053"},
			},
		},
		Node:        config.Node{ID: "de-1", Name: "de-1", PublicIP: "1.2.3.4"},
		Hub:         config.HubInfo{Name: "ir-1", PublicIP: "5.6.7.8", ControlPort: 44433},
		Transport:   New().Transports()[0],
		ControlPort: 30001,
		Secrets: backend.Secrets{
			Token:       "tok-test",
			TLSCertFile: dir + "/tls-cert.pem",
			TLSKeyFile:  dir + "/tls-key.pem",
			CAFile:      dir + "/ca.crt",
			ServerName:  "5.6.7.8",
		},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/gost/v3.2.6/gost",
			BinDir:     "/var/lib/deyroute/bin/gost/v3.2.6",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

func mixedFixture(t *testing.T) backend.RenderInput {
	in := fixture(t)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp", Target: "10.0.0.5:27016"},
	)
	return in
}

func canaryFixture(t *testing.T) backend.RenderInput {
	in := fixture(t)
	in.Canary = true
	in.ListenAddr = "127.0.0.1"
	in.ControlPort = 30002
	in.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:39002"}, {Listen: 39003}}
	return in
}

func serialize(t *testing.T, side backend.Side, r backend.Rendered) []byte {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "# gost/relay-wss %s side\n", side)
	for _, name := range backend.SortedKeys(r.Files) {
		fmt.Fprintf(&b, "=== file %s\n%s", name, r.Files[name])
	}
	unit, err := backend.JSONIndent(r.Unit)
	require.NoError(t, err)
	fmt.Fprintf(&b, "=== unit\n%s", unit)
	b.WriteString("=== binds\n")
	for _, p := range r.Binds {
		fmt.Fprintf(&b, "%s %s %s\n", p.Proto, backend.HostPort(p.Addr, p.Port), p.Purpose)
	}
	fmt.Fprintf(&b, "=== nat %v\n=== masquerade %v\n=== ip_forward %v\n", r.NAT, r.Masquerade, r.IPForward)
	return b.Bytes()
}

func checkGolden(t *testing.T, file string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", file)
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, got, 0o600))
		return
	}
	want, err := os.ReadFile(path) // #nosec G304 -- test fixture path
	require.NoError(t, err, "missing golden file; run go test -update")
	require.Equal(t, string(want), string(got), "golden mismatch for %s; run go test -update", file)
}

func TestGolden(t *testing.T) {
	ipv6 := mixedFixture(t)
	ipv6.ListenAddr = "::"
	ipv6.Secrets.ServerName = ""
	ipv6.Tunnel.Ports = append(ipv6.Tunnel.Ports, config.PortMap{Listen: 9443, Proto: "tcp", Target: "[::1]:9444"})
	cases := []struct {
		golden string
		in     backend.RenderInput
	}{
		{"relay-wss", fixture(t)},
		{"relay-wss_mixed", mixedFixture(t)},
		{"relay-wss_canary", canaryFixture(t)},
		{"relay-wss_ipv6", ipv6},
	}
	b := New()
	for _, c := range cases {
		for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
			t.Run(c.golden+"."+side.String(), func(t *testing.T) {
				require.NoError(t, b.Validate(c.in))
				r, err := b.Render(c.in, side)
				require.NoError(t, err)
				again, err := b.Render(c.in, side)
				require.NoError(t, err)
				require.Equal(t, r, again, "render must be deterministic")
				checkGolden(t, c.golden+"."+side.String()+".golden", serialize(t, side, r))
			})
		}
	}
}

func TestTransportsAndManifest(t *testing.T) {
	trs := New().Transports()
	require.Len(t, trs, 1)
	tr := trs[0]
	require.Equal(t, "gost/relay-wss", tr.ID())
	require.Equal(t, backend.Reverse, tr.Direction)
	require.Equal(t, []string{"tcp", "udp"}, tr.Protos)
	require.False(t, tr.NeedsUDP)
	require.True(t, tr.NeedsTLS)
	require.Equal(t, 3, tr.Stealth)
	require.True(t, tr.Optional)
	require.False(t, tr.ClientIPPreserved)
	require.False(t, tr.NeverQuarantine)

	b, got, err := backend.Lookup("gost/relay-wss")
	require.NoError(t, err)
	require.Equal(t, Name, b.Name())
	require.Equal(t, tr, got)

	m := New().Manifest()
	require.Equal(t, "v3.2.6", m.Version)
	require.Equal(t, "go-gost/gost", m.Repo)
	require.Equal(t, "tar.gz", m.Archive)
	require.Equal(t, []string{"gost"}, m.Binaries)
	require.Contains(t, m.URLs["amd64"], "/v3.2.6/gost_3.2.6_linux_amd64.tar.gz")
	require.Contains(t, m.URLs["arm64"], "/v3.2.6/gost_3.2.6_linux_arm64.tar.gz")
	_, err = New().Probe(context.Background(), fixture(t))
	require.ErrorIs(t, err, backend.ErrNoProbe)
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *backend.RenderInput)
		code   deyerr.Code
		reason string
	}{
		{"foreign backend", func(in *backend.RenderInput) { in.Transport.Backend = "chisel" }, deyerr.B006, "not a gost"},
		{"unknown transport", func(in *backend.RenderInput) { in.Transport.Name = "wss" }, deyerr.B006, "not a gost"},
		{"ctl port", func(in *backend.RenderInput) { in.ControlPort = 70000 }, deyerr.B006, "control port"},
		{"token", func(in *backend.RenderInput) { in.Secrets.Token = "" }, deyerr.B006, "token"},
		{"long token", func(in *backend.RenderInput) { in.Secrets.Token = strings.Repeat("x", 256) }, deyerr.B006, "255"},
		{"hub ip", func(in *backend.RenderInput) { in.Hub.PublicIP = " " }, deyerr.B006, "hub public IP"},
		{"cert", func(in *backend.RenderInput) { in.Secrets.TLSCertFile = "" }, deyerr.B006, "TLS certificate"},
		{"key", func(in *backend.RenderInput) { in.Secrets.TLSKeyFile = "k.pem" }, deyerr.B006, "TLS certificate"},
		{"ca", func(in *backend.RenderInput) { in.Secrets.CAFile = "" }, deyerr.B006, "CA certificate"},
		{"binary", func(in *backend.RenderInput) { in.Paths.Binary = "" }, deyerr.B006, "not installed"},
		{"config dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "x" }, deyerr.B006, "config directory"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"bad proto", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "icmp" }, deyerr.B010, ""},
		{"listen", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 0 }, deyerr.B006, "out of range"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1] = in.Tunnel.Ports[0] }, deyerr.B006, "listed twice"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "x" }, deyerr.B006, "invalid target"},
		{"url target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "a/b:443" }, deyerr.B006, "invalid target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t)
			c.mutate(&in)
			err := New().Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "%v", err)
			if c.reason != "" {
				require.Contains(t, deyerr.As(err).Message(), c.reason)
			}
			_, err = New().Render(in, backend.SideNode)
			require.Error(t, err)
		})
	}
}

// parse decodes a rendered gost.yaml strictly into the renderer's schema.
func parse(t *testing.T, data []byte) gostConfig {
	t.Helper()
	var c gostConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	require.NoError(t, dec.Decode(&c))
	return c
}

func TestParsedConfig(t *testing.T) {
	in := mixedFixture(t)
	hub, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	node, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	path := wsPath("tok-test")
	require.Regexp(t, `^/[0-9a-f]{16}$`, path)
	require.NotEqual(t, path, wsPath("other"))

	h := parse(t, hub.Files[ConfigFile])
	require.Len(t, h.Services, 1)
	s := h.Services[0]
	require.Equal(t, "0.0.0.0:30001", s.Addr)
	require.Equal(t, "relay", s.Handler.Type)
	require.Equal(t, &auth{Username: "dey", Password: "tok-test"}, s.Handler.Auth)
	require.Equal(t, map[string]any{"bind": true}, s.Handler.Metadata)
	require.Equal(t, "wss", s.Listener.Type)
	require.Equal(t, &tlsConfig{CertFile: in.Secrets.TLSCertFile, KeyFile: in.Secrets.TLSKeyFile, Options: &tlsOptions{MinVersion: "VersionTLS13"}}, s.Listener.TLS)
	require.Equal(t, map[string]any{"path": path}, s.Listener.Metadata)
	// The tunnel has UDP maps: a deny-all bypass would drop every UDP BIND
	// datagram, so only unspecified/link-local CONNECTs are refused.
	require.Equal(t, denyLocal, s.Bypass)
	require.Equal(t, []bypass{{Name: denyLocal, Matchers: localMatchers}}, h.Bypasses)
	require.Nil(t, s.Forwarder)
	require.Empty(t, h.Chains)
	require.Equal(t, logConfig{Output: "stderr", Level: "info", Format: "text"}, h.Log)

	n := parse(t, node.Files[ConfigFile])
	require.Empty(t, n.Bypasses)
	want := []struct{ name, typ, addr, target string }{
		{"tcp-443", "rtcp", "0.0.0.0:443", "127.0.0.1:443"},
		{"tcp-2053", "rtcp", "0.0.0.0:2053", "127.0.0.1:2053"},
		{"udp-443", "rudp", "0.0.0.0:443", "127.0.0.1:443"},
		{"udp-27015", "rudp", "0.0.0.0:27015", "10.0.0.5:27016"},
	}
	require.Len(t, n.Services, len(want))
	for i, w := range want {
		s := n.Services[i]
		require.Equal(t, w.name, s.Name)
		require.Equal(t, w.addr, s.Addr)
		require.Equal(t, w.typ, s.Handler.Type)
		require.Nil(t, s.Handler.Auth)
		require.Equal(t, w.typ, s.Listener.Type)
		require.Equal(t, hubChain, s.Listener.Chain)
		require.Nil(t, s.Listener.TLS)
		if w.typ == "rudp" {
			require.Equal(t, map[string]any{"ttl": "60s"}, s.Listener.Metadata)
		} else {
			require.Empty(t, s.Listener.Metadata)
		}
		require.Equal(t, &forwarder{Nodes: []forwardNode{{Name: "target", Addr: w.target}}}, s.Forwarder)
	}
	require.Len(t, n.Chains, 1)
	require.Equal(t, hubChain, n.Chains[0].Name)
	hn := n.Chains[0].Hops[0].Nodes[0]
	require.Equal(t, "5.6.7.8:30001", hn.Addr)
	require.Equal(t, connector{Type: "relay", Auth: &auth{Username: "dey", Password: "tok-test"}}, hn.Connector)
	require.Equal(t, "wss", hn.Dialer.Type)
	require.Equal(t, &tlsConfig{CAFile: in.Secrets.CAFile, Secure: true, ServerName: "5.6.7.8", Options: &tlsOptions{MinVersion: "VersionTLS13"}}, hn.Dialer.TLS)
	require.Equal(t, map[string]any{"path": path}, hn.Dialer.Metadata)

	// Units: config file, not URLs, so the token is not in argv.
	for _, r := range []backend.Rendered{hub, node} {
		require.Equal(t, []string{in.Paths.Binary, "-C", in.Paths.ConfigDir + "/gost.yaml"}, r.Unit.ExecStart)
		require.Equal(t, in.Paths.ConfigDir, r.Unit.WorkingDirectory)
		require.NotContains(t, strings.Join(r.Unit.ExecStart, " "), "tok-test")
		require.Contains(t, r.Unit.DropHardening, "MemoryDenyWriteExecute")
		require.False(t, r.Unit.RunAsRoot)
		require.Empty(t, r.Unit.Env)
	}
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, hub.Binds)
	require.Empty(t, node.Binds)
	require.Empty(t, hub.NAT)

	// Without a server name the chain is verified against the CA only.
	in.Secrets.ServerName = ""
	node, err = New().Render(in, backend.SideNode)
	require.NoError(t, err)
	n = parse(t, node.Files[ConfigFile])
	require.Equal(t, &tlsConfig{CAFile: in.Secrets.CAFile, Options: &tlsOptions{MinVersion: "VersionTLS13"}}, n.Chains[0].Hops[0].Nodes[0].Dialer.TLS)
}

func TestCanaryAndDefaults(t *testing.T) {
	c := canaryFixture(t)
	c.ListenAddr = "" // forced to loopback anyway
	hub, err := New().Render(c, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, []backend.PortUse{
		{Port: 30002, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 39001, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"},
	}, hub.Binds)
	node, err := New().Render(c, backend.SideNode)
	require.NoError(t, err)
	n := parse(t, node.Files[ConfigFile])
	require.Len(t, n.Services, 1)
	require.Equal(t, "127.0.0.1:39001", n.Services[0].Addr)
	require.Equal(t, "127.0.0.1:39002", n.Services[0].Forwarder.Nodes[0].Addr)

	in := fixture(t)
	in.Tunnel.Ports = []config.PortMap{{Listen: 8443}}
	in.ListenAddr = "::"
	node, err = New().Render(in, backend.SideNode)
	require.NoError(t, err)
	n = parse(t, node.Files[ConfigFile])
	require.Equal(t, "[::]:8443", n.Services[0].Addr)
	require.Equal(t, "rtcp", n.Services[0].Handler.Type)
	require.Equal(t, "127.0.0.1:8443", n.Services[0].Forwarder.Nodes[0].Addr)
}

// TestNodeDialsOnlyTargets: the node forwards every service to a fixed,
// configured target (remote forwarding handlers, no proxy handlers) and
// the hub refuses relay CONNECT (spec section 11).
func TestNodeDialsOnlyTargets(t *testing.T) {
	in := mixedFixture(t)
	node, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	allowed := map[string]bool{}
	for _, p := range in.Tunnel.Ports {
		allowed[p.Target] = true
	}
	n := parse(t, node.Files[ConfigFile])
	for _, s := range n.Services {
		require.Contains(t, []string{"rtcp", "rudp"}, s.Handler.Type, "only remote forwarders on the node")
		require.NotNil(t, s.Forwarder)
		for _, f := range s.Forwarder.Nodes {
			require.True(t, allowed[f.Addr], "%s is not a configured target", f.Addr)
		}
	}
	// TCP-only tunnel: every CONNECT is refused.
	hub, err := New().Render(fixture(t), backend.SideHub)
	require.NoError(t, err)
	h := parse(t, hub.Files[ConfigFile])
	require.Equal(t, denyConnect, h.Services[0].Bypass)
	require.True(t, h.Bypasses[0].Whitelist)
	require.NotContains(t, string(hub.Files[ConfigFile]), "matchers", "an empty whitelist denies every CONNECT")
	for _, addr := range []string{"127.0.0.1:22", "10.0.0.1:80", "8.8.8.8:53", "localhost:22", "[::1]:22"} {
		require.True(t, bypassed(h, addr), "CONNECT %s must be refused", addr)
	}
}

// bypassed evaluates the hub service's bypass the way gost v3 (x v0.8.1
// bypass.localBypass.Contains) does: an address is bypassed (refused) when
// it matches a blacklist, or does not match a whitelist.
func bypassed(c gostConfig, addr string) bool {
	name := c.Services[0].Bypass
	for _, b := range c.Bypasses {
		if b.Name != name {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			host = addr
		}
		matched := false
		for _, m := range b.Matchers {
			if _, n, err := net.ParseCIDR(m); err == nil {
				if ip := net.ParseIP(host); ip != nil && n.Contains(ip) {
					matched = true
				}
			} else if m == host {
				matched = true
			}
		}
		return matched != b.Whitelist
	}
	return false
}

// TestHubUDPNotBypassed: gost applies the service bypass to every datagram
// of a UDP BIND (matched against the user client's address), so a tunnel
// with UDP maps must let ordinary client addresses (and the loopback path
// probe) through while still refusing CONNECT to link-local addresses.
func TestHubUDPNotBypassed(t *testing.T) {
	for _, in := range []backend.RenderInput{mixedFixture(t), canaryFixture(t)} {
		if in.Canary {
			in.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "udp", Target: "127.0.0.1:39002"}}
		}
		hub, err := New().Render(in, backend.SideHub)
		require.NoError(t, err)
		h := parse(t, hub.Files[ConfigFile])
		require.Equal(t, denyLocal, h.Services[0].Bypass)
		require.False(t, h.Bypasses[0].Whitelist, "a whitelist would drop UDP datagrams of unlisted clients")
		for _, client := range []string{"1.2.3.4:50000", "[2001:db8::1]:50000", "127.0.0.1:40000", "10.1.2.3:5000"} {
			require.False(t, bypassed(h, client), "UDP client %s must pass", client)
		}
		for _, addr := range []string{"169.254.169.254:80", "0.0.0.0:22", "[fe80::1]:22", "[::]:22"} {
			require.True(t, bypassed(h, addr), "CONNECT %s must be refused", addr)
		}
	}
}
