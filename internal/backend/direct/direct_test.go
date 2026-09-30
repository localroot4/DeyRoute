package direct

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func transport(t *testing.T, name string) backend.Transport {
	t.Helper()
	for _, tr := range New().Transports() {
		if tr.Name == name {
			return tr
		}
	}
	t.Fatalf("no transport %s", name)
	return backend.Transport{}
}

// fixture is the fixed RenderInput of the golden tests.
func fixture(t *testing.T, name string) backend.RenderInput {
	t.Helper()
	dir := "/etc/deyroute/backends/direct/main/de-1/" + name
	bin := "/usr/local/bin/deyroute"
	if name == HAProxy {
		bin = "/usr/sbin/haproxy"
	}
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
		Transport:   transport(t, name),
		ControlPort: 30001,
		Secrets:     backend.Secrets{Token: "tok-test", ServerName: "5.6.7.8"},
		Paths: backend.Paths{
			Binary:     bin,
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// mixedFixture adds UDP port maps (direct/native carries both).
func mixedFixture(t *testing.T) backend.RenderInput {
	in := fixture(t, Native)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp", Target: "10.0.0.5:27015"},
	)
	return in
}

// haproxyFixture uses targets HAProxy can reach from the hub.
func haproxyFixture(t *testing.T) backend.RenderInput {
	in := fixture(t, HAProxy)
	in.Tunnel.Ports[0].Target = "0.0.0.0:443"
	in.Tunnel.Ports[1].Target = "1.2.3.4:8443"
	return in
}

func serialize(t *testing.T, id string, side backend.Side, r backend.Rendered) []byte {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s %s side\n", id, side)
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
	b.WriteString("=== nat\n")
	for _, n := range r.NAT {
		fmt.Fprintf(&b, "%+v\n", n)
	}
	fmt.Fprintf(&b, "=== masquerade %v\n=== ip_forward %v\n", r.Masquerade, r.IPForward)
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
	b := New()
	proxy := haproxyFixture(t)
	proxy.Tunnel.Advanced = &config.Advanced{ProxyProtocol: true}
	canary := fixture(t, Native)
	canary.Canary = true
	canary.ListenAddr = "127.0.0.1"
	canary.ControlPort = 30002
	canary.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:39002"}}
	dual := fixture(t, Native)
	dual.ListenAddr = "::"
	dual.Node.PublicIP = "2001:db8::2"

	cases := []struct {
		golden string
		in     backend.RenderInput
	}{
		{"native", fixture(t, Native)},
		{"native_mixed", mixedFixture(t)},
		{"native_canary", canary},
		{"native_ipv6", dual},
		{"haproxy", haproxyFixture(t)},
		{"haproxy_proxy", proxy},
	}
	for _, c := range cases {
		for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
			t.Run(c.golden+"."+side.String(), func(t *testing.T) {
				require.NoError(t, b.Validate(c.in))
				r, err := b.Render(c.in, side)
				require.NoError(t, err)
				again, err := b.Render(c.in, side)
				require.NoError(t, err)
				require.Equal(t, r, again, "render must be deterministic")
				checkGolden(t, c.golden+"."+side.String()+".golden", serialize(t, c.in.Transport.ID(), side, r))
			})
		}
	}
}

func TestTransports(t *testing.T) {
	b := New()
	require.Equal(t, "direct", b.Name())
	trs := b.Transports()
	require.Len(t, trs, 2)
	native, haproxy := trs[0], trs[1]
	require.Equal(t, "direct/native", native.ID())
	require.Equal(t, backend.Forward, native.Direction)
	require.Equal(t, []string{"tcp", "udp"}, native.Protos)
	require.Equal(t, 1, native.Stealth)
	require.True(t, native.NeverQuarantine)
	require.False(t, native.Optional)
	require.False(t, native.NeedsUDP)
	require.False(t, native.NeedsTLS)
	require.False(t, native.ClientIPPreserved)

	require.Equal(t, "direct/haproxy", haproxy.ID())
	require.Equal(t, backend.Forward, haproxy.Direction)
	require.Equal(t, []string{"tcp"}, haproxy.Protos)
	require.Equal(t, 1, haproxy.Stealth)
	require.True(t, haproxy.Optional)
	require.True(t, haproxy.ClientIPPreserved)
	require.False(t, haproxy.NeverQuarantine)

	bk, tr, err := backend.Lookup("direct/native")
	require.NoError(t, err)
	require.True(t, tr.NeverQuarantine)
	m := bk.Manifest()
	require.True(t, m.Builtin)
	require.Equal(t, "builtin", m.Version)
	_, err = bk.Probe(context.Background(), fixture(t, Native))
	require.ErrorIs(t, err, backend.ErrNoProbe)
}

// parseRelay parses a rendered relay.json with the loader the relay uses.
func parseRelay(t *testing.T, r backend.Rendered) *RelayConfig {
	t.Helper()
	cfg, err := ParseRelayConfig("relay.json", r.Files[RelayConfigFile])
	require.NoError(t, err)
	return cfg
}

func TestNativeHubConfig(t *testing.T) {
	b := New()
	in := mixedFixture(t)
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	cfg := parseRelay(t, r)
	require.Equal(t, RoleHub, cfg.Role)
	require.Equal(t, "main", cfg.Tunnel)
	require.Equal(t, "tok-test", cfg.Token)
	require.Equal(t, "1.2.3.4:30001", cfg.Node)
	require.Empty(t, cfg.Bind)
	require.Equal(t, []RelayPort{
		{Index: 0, Proto: "tcp", Listen: "0.0.0.0:443"},
		{Index: 1, Proto: "tcp", Listen: "0.0.0.0:2053"},
		{Index: 2, Proto: "udp", Listen: "0.0.0.0:443"},
		{Index: 3, Proto: "udp", Listen: "0.0.0.0:27015"},
	}, cfg.Ports)
	for _, p := range cfg.Ports {
		require.Empty(t, p.Target, "the hub half never learns targets")
	}
	require.Equal(t, 300, cfg.IdleTimeoutS)
	require.Equal(t, 10, cfg.DialTimeoutS)
	require.Equal(t, 60, cfg.UDPIdleTimeoutS)
	require.Equal(t, 4096, cfg.MaxUDPSessions)
	require.Equal(t, []string{"/usr/local/bin/deyroute", "relay", "--tunnel", "main", "--config", in.Paths.ConfigDir + "/relay.json"}, r.Unit.ExecStart)
	require.Equal(t, in.Paths.ConfigDir, r.Unit.WorkingDirectory)
	require.Equal(t, []backend.PortUse{
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)
}

// TestNodeAllowList is the render half of scenario S17: the node relay's
// only possible destinations are exactly the tunnel's targets.
func TestNodeAllowList(t *testing.T) {
	b := New()
	in := mixedFixture(t)
	r, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	cfg := parseRelay(t, r)
	require.Equal(t, RoleNode, cfg.Role)
	require.Equal(t, "0.0.0.0:30001", cfg.Bind)
	require.Empty(t, cfg.Node)
	var targets []string
	for _, p := range cfg.Ports {
		require.Empty(t, p.Listen)
		targets = append(targets, p.Proto+" "+p.Target)
	}
	require.Equal(t, []string{"tcp 127.0.0.1:443", "tcp 127.0.0.1:2053", "udp 127.0.0.1:443", "udp 10.0.0.5:27015"}, targets)
	require.NotContains(t, string(r.Files[RelayConfigFile]), "8.8.8.8")

	// The relay resolves an index only to a configured target of the
	// same protocol.
	rl := newRelay(cfg, nil)
	got, ok := rl.target(0, "tcp")
	require.True(t, ok)
	require.Equal(t, "127.0.0.1:443", got)
	_, ok = rl.target(0, "udp")
	require.False(t, ok, "index 0 is not a udp target")
	_, ok = rl.target(7, "tcp")
	require.False(t, ok)

	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 30001, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"},
	}, r.Binds)

	// A TCP-only tunnel binds only tcp on the control port.
	r, err = b.Render(fixture(t, Native), backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, []backend.PortUse{{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"}}, r.Binds)
}

func TestNativeCanary(t *testing.T) {
	b := New()
	in := fixture(t, Native)
	in.Canary = true
	in.ListenAddr = "" // the canary must not bind publicly even when unset
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	cfg := parseRelay(t, r)
	require.Equal(t, []RelayPort{{Index: 0, Proto: "tcp", Listen: "127.0.0.1:443"}}, cfg.Ports)
	require.Equal(t, []backend.PortUse{{Port: 443, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"}}, r.Binds)
	in.ListenAddr = "::1"
	r, err = b.Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, "::1", r.Binds[0].Addr)
}

func TestHAProxyConfig(t *testing.T) {
	b := New()
	in := haproxyFixture(t)
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	cfg := string(r.Files[HAProxyFile])
	for _, want := range []string{
		"\nglobal\n    maxconn 65536\n",
		"\ndefaults\n    mode tcp\n",
		"    timeout connect 10s\n",
		"    timeout client 5m\n",
		"    timeout server 5m\n",
		"\nfrontend tcp-443\n    bind 0.0.0.0:443\n    default_backend tcp-443\n",
		"\nbackend tcp-443\n    option tcp-check\n    server de-1 1.2.3.4:443 check inter 5s fall 3 rise 2\n",
		"\nbackend tcp-2053\n    option tcp-check\n    server de-1 1.2.3.4:8443 check inter 5s fall 3 rise 2\n",
	} {
		require.Contains(t, cfg, want)
	}
	require.NotContains(t, cfg, "send-proxy")
	path := in.Paths.ConfigDir + "/haproxy.cfg"
	require.Equal(t, []string{"/usr/sbin/haproxy", "-f", path, "-db"}, r.Unit.ExecStart)
	require.Equal(t, [][]string{{"/usr/sbin/haproxy", "-c", "-f", path}}, r.Unit.ExecStartPre)
	require.Equal(t, []backend.PortUse{
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)

	in.Tunnel.Advanced = &config.Advanced{ProxyProtocol: true}
	in.ListenAddr = "::"
	in.Node.PublicIP = "2001:db8::2"
	in.Tunnel.Ports[1].Target = "[2001:db8::2]:8443"
	r, err = b.Render(in, backend.SideHub)
	require.NoError(t, err)
	cfg = string(r.Files[HAProxyFile])
	require.Contains(t, cfg, "    bind :::443 v4v6\n")
	require.Contains(t, cfg, "    server de-1 2001:db8::2:8443 check inter 5s fall 3 rise 2 send-proxy check-send-proxy\n")
}

func TestHAProxyNodeCheck(t *testing.T) {
	b := New()
	in := haproxyFixture(t)
	in.Paths.Binary = "" // no haproxy on the node
	r, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	cfg := parseRelay(t, r)
	require.Equal(t, RoleCheck, cfg.Role)
	require.Equal(t, "1.2.3.4", cfg.NodeIP)
	require.Empty(t, cfg.Token, "the check needs no secret")
	require.Equal(t, []RelayPort{
		{Index: 0, Proto: "tcp", Target: "0.0.0.0:443"},
		{Index: 1, Proto: "tcp", Target: "1.2.3.4:8443"},
	}, cfg.Ports)
	require.Equal(t, "oneshot", r.Unit.Type)
	require.True(t, r.Unit.RemainAfterExit)
	// net.InterfaceAddrs needs a netlink socket, which the shared template's
	// RestrictAddressFamilies would otherwise refuse (the 1:1 NAT fallback
	// of checkCandidates would silently never work).
	require.Equal(t, []string{"AF_NETLINK"}, r.Unit.AddressFamilies)
	require.Equal(t, "/usr/local/bin/deyroute", r.Unit.ExecStart[0])
	require.Empty(t, r.Binds)
}

func TestValidate(t *testing.T) {
	b := New()
	cases := []struct {
		name   string
		tr     string
		code   deyerr.Code
		mutate func(in *backend.RenderInput)
	}{
		{"foreign transport", Native, deyerr.B006, func(in *backend.RenderInput) { in.Transport = backend.Transport{Backend: "direct", Name: "socat"} }},
		{"no ports", Native, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports = nil }},
		{"bad proto", Native, deyerr.B010, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "sctp" }},
		{"listen range", Native, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 70000 }},
		{"duplicate", Native, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[1].Listen = 443 }},
		{"bad target", Native, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "nohost" }},
		{"node ip", Native, deyerr.B006, func(in *backend.RenderInput) { in.Node.PublicIP = "" }},
		{"node hostname", Native, deyerr.B006, func(in *backend.RenderInput) { in.Node.PublicIP = "node.example" }},
		{"config dir", Native, deyerr.B006, func(in *backend.RenderInput) { in.Paths.ConfigDir = "" }},
		{"control port", Native, deyerr.B006, func(in *backend.RenderInput) { in.ControlPort = 0 }},
		{"token", Native, deyerr.B006, func(in *backend.RenderInput) { in.Secrets.Token = "" }},
		{"self binary", Native, deyerr.B006, func(in *backend.RenderInput) { in.Paths.SelfBinary = "deyroute" }},
		{"haproxy udp", HAProxy, deyerr.B010, func(in *backend.RenderInput) {
			in.Tunnel.Ports = append(in.Tunnel.Ports, config.PortMap{Listen: 53, Proto: "udp", Target: "0.0.0.0:53"})
		}},
		{"haproxy not installed", HAProxy, deyerr.B006, func(in *backend.RenderInput) { in.Paths.Binary = "" }},
		{"haproxy loopback target", HAProxy, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "127.0.0.1:443" }},
		{"haproxy default target", HAProxy, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "" }},
		{"haproxy other host", HAProxy, deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "10.0.0.5:443" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t, c.tr)
			if c.tr == HAProxy {
				in = haproxyFixture(t)
			}
			c.mutate(&in)
			err := b.Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "want %s, got %v", c.code, err)
			_, rerr := b.Render(in, backend.SideHub)
			require.True(t, deyerr.HasCode(rerr, c.code), "render: %v", rerr)
		})
	}

	// The loopback reason explains the fix.
	in := haproxyFixture(t)
	in.Tunnel.Ports[0].Target = "127.0.0.1:443"
	err := b.Validate(in)
	require.Contains(t, deyerr.As(err).Message(), "0.0.0.0:443")

	// The node side of haproxy needs deyroute, not haproxy.
	in = haproxyFixture(t)
	in.Paths.SelfBinary = ""
	_, err = b.Render(in, backend.SideNode)
	require.True(t, deyerr.HasCode(err, deyerr.B006))

	// Empty proto defaults to tcp and an empty target to 127.0.0.1:<listen>.
	in = fixture(t, Native)
	in.Tunnel.Ports = []config.PortMap{{Listen: 8080}}
	r, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, []RelayPort{{Index: 0, Proto: "tcp", Target: "127.0.0.1:8080"}}, parseRelay(t, r).Ports)
}

func TestRelayJSONIsStrict(t *testing.T) {
	b := New()
	r, err := b.Render(fixture(t, Native), backend.SideNode)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(r.Files[RelayConfigFile], &raw))
	raw["extra"] = true
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	_, err = ParseRelayConfig("x.json", data)
	require.True(t, deyerr.HasCode(err, deyerr.B060))
	require.True(t, strings.HasSuffix(string(r.Files[RelayConfigFile]), "}\n"))
}
