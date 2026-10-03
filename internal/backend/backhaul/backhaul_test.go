package backhaul

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

// transport returns the registered transport name.
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
	dir := "/etc/deyroute/backends/backhaul/main/de-1/" + name
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
		Secrets: backend.Secrets{
			Token:       "tok-test",
			TLSCertFile: dir + "/cert.pem",
			TLSKeyFile:  dir + "/key.pem",
			CAFile:      dir + "/ca.pem",
			ServerName:  "5.6.7.8",
		},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/backhaul/v0.7.2/backhaul",
			BinDir:     "/var/lib/deyroute/bin/backhaul/v0.7.2",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// udpFixture replaces the ports with UDP-only ports (backhaul/udp).
func udpFixture(t *testing.T, name string) backend.RenderInput {
	in := fixture(t, name)
	in.Tunnel.Ports = []config.PortMap{
		{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"},
		{Listen: 5353, Proto: "udp", Target: "127.0.0.1:53"},
	}
	return in
}

// mixedFixture maps both protocols of every port (backhaul/tcp with
// accept_udp opens tcp and udp listeners for every entry, so a mixed tunnel
// must map both).
func mixedFixture(t *testing.T, name string) backend.RenderInput {
	in := fixture(t, name)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"},
		config.PortMap{Listen: 27015, Proto: "tcp", Target: "127.0.0.1:27015"},
		config.PortMap{Listen: 2053, Proto: "udp", Target: "127.0.0.1:2053"},
	)
	return in
}

// serialize writes every part of r deterministically for golden files.
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
	cases := []struct {
		golden string
		in     backend.RenderInput
	}{
		{"tcp", fixture(t, TCP)},
		{"tcp_mixed", mixedFixture(t, TCP)},
		{"tcpmux", fixture(t, TCPMux)},
		{"ws", fixture(t, WS)},
		{"wss", fixture(t, WSS)},
		{"wsmux", fixture(t, WSMux)},
		{"wssmux", fixture(t, WSSMux)},
		{"udp", udpFixture(t, UDP)},
	}
	canary := fixture(t, WSSMux)
	canary.Canary = true
	canary.ListenAddr = "127.0.0.1"
	canary.ControlPort = 30002
	canary.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:39002"}}
	cases = append(cases, struct {
		golden string
		in     backend.RenderInput
	}{"wssmux_canary", canary})

	// Backend tiers (optimize auto --backends): each side renders its own.
	for _, tier := range []string{config.BackendTierSmall, config.BackendTierLarge} {
		for _, name := range []string{TCP, WSSMux} {
			in := fixture(t, name)
			in.HubTier, in.NodeTier = tier, tier
			cases = append(cases, struct {
				golden string
				in     backend.RenderInput
			}{name + "_" + tier, in})
		}
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
	require.Equal(t, "backhaul", b.Name())
	want := map[string]struct {
		stealth  int
		protos   []string
		tls, udp bool
	}{
		TCP:    {1, []string{"tcp", "udp"}, false, false},
		TCPMux: {2, []string{"tcp"}, false, false},
		WS:     {2, []string{"tcp"}, false, false},
		WSS:    {4, []string{"tcp"}, true, false},
		WSMux:  {3, []string{"tcp"}, false, false},
		WSSMux: {4, []string{"tcp"}, true, false},
		UDP:    {1, []string{"udp"}, false, true},
	}
	trs := b.Transports()
	require.Len(t, trs, len(want))
	for _, tr := range trs {
		w, ok := want[tr.Name]
		require.True(t, ok, tr.Name)
		require.Equal(t, "backhaul", tr.Backend)
		require.Equal(t, backend.Reverse, tr.Direction, tr.Name)
		require.Equal(t, w.stealth, tr.Stealth, tr.Name)
		require.Equal(t, w.protos, tr.Protos, tr.Name)
		require.Equal(t, w.tls, tr.NeedsTLS, tr.Name)
		require.Equal(t, w.udp, tr.NeedsUDP, tr.Name)
		require.False(t, tr.ClientIPPreserved)
		require.False(t, tr.Optional)
		require.False(t, tr.NeverQuarantine)
	}
	// Transports returns copies: mutating them must not leak into specs.
	trs[0].Protos[0] = "x"
	require.Equal(t, "tcp", b.Transports()[0].Protos[0])
}

func TestRegisteredAndManifest(t *testing.T) {
	bk, tr, err := backend.Lookup("backhaul/wssmux")
	require.NoError(t, err)
	require.Equal(t, "backhaul", bk.Name())
	require.True(t, tr.NeedsTLS)
	m := bk.Manifest()
	require.Equal(t, "v0.7.2", m.Version)
	require.Equal(t, "backhaul", m.Binary())
	require.Equal(t, "tar.gz", m.Archive)
	require.Contains(t, m.URLs["amd64"], "backhaul_linux_amd64.tar.gz")
	require.Contains(t, m.URLs["arm64"], "backhaul_linux_arm64.tar.gz")
	_, err = bk.Probe(context.Background(), fixture(t, WSSMux))
	require.ErrorIs(t, err, backend.ErrNoProbe)
}

// parseTOML is a structural parser for the flat TOML subset the renderer
// emits: one table, scalar keys and string arrays.
func parseTOML(t *testing.T, data []byte) (string, map[string]any) {
	t.Helper()
	var table string
	out := map[string]any{}
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			require.Empty(t, table, "only one table expected")
			table = strings.Trim(line, "[]")
			continue
		}
		k, v, ok := strings.Cut(line, " = ")
		require.True(t, ok, "bad line %q", line)
		_, dup := out[k]
		require.False(t, dup, "duplicate key %s", k)
		switch {
		case v == "[]":
			out[k] = []string{}
		case v == "[":
			var items []string
			for i++; strings.TrimSpace(lines[i]) != "]"; i++ {
				s, err := strconv.Unquote(strings.TrimSuffix(strings.TrimSpace(lines[i]), ","))
				require.NoError(t, err)
				items = append(items, s)
			}
			out[k] = items
		case strings.HasPrefix(v, `"`):
			s, err := strconv.Unquote(v)
			require.NoError(t, err)
			out[k] = s
		case v == "true" || v == "false":
			out[k] = v == "true"
		default:
			n, err := strconv.Atoi(v)
			require.NoError(t, err, "value of %s", k)
			out[k] = n
		}
	}
	return table, out
}

func TestServerConfigParsesBack(t *testing.T) {
	b := New()
	in := fixture(t, WSSMux)
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	table, kv := parseTOML(t, r.Files[ServerFile])
	require.Equal(t, "server", table)
	require.Equal(t, "0.0.0.0:30001", kv["bind_addr"])
	require.Equal(t, "wssmux", kv["transport"])
	require.Equal(t, "tok-test", kv["token"])
	require.Equal(t, 75, kv["keepalive_period"])
	require.Equal(t, true, kv["nodelay"])
	require.Equal(t, 20, kv["heartbeat"])
	require.Equal(t, 2048, kv["channel_size"])
	require.Equal(t, 8, kv["mux_con"])
	require.Equal(t, 1, kv["mux_version"])
	require.Equal(t, 32768, kv["mux_framesize"])
	require.Equal(t, 4194304, kv["mux_recievebuffer"])
	require.Equal(t, 65536, kv["mux_streambuffer"])
	require.Equal(t, false, kv["sniffer"])
	require.Equal(t, 0, kv["web_port"])
	require.Equal(t, in.Secrets.TLSCertFile, kv["tls_cert"])
	require.Equal(t, in.Secrets.TLSKeyFile, kv["tls_key"])
	require.Equal(t, "info", kv["log_level"])
	require.Equal(t, true, kv["skip_optz"])
	require.Equal(t, []string{"443=127.0.0.1:443", "2053=127.0.0.1:2053"}, kv["ports"])
	_, hasAccept := kv["accept_udp"]
	require.False(t, hasAccept, "accept_udp only exists for the tcp transport")

	require.Equal(t, []string{in.Paths.Binary, "-c", in.Paths.ConfigDir + "/server.toml"}, r.Unit.ExecStart)
	require.Equal(t, in.Paths.ConfigDir, r.Unit.WorkingDirectory)
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)
	require.Empty(t, r.NAT)
	require.False(t, r.IPForward)
}

func TestClientConfigParsesBack(t *testing.T) {
	b := New()
	in := fixture(t, WSSMux)
	r, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	table, kv := parseTOML(t, r.Files[ClientFile])
	require.Equal(t, "client", table)
	require.Equal(t, "5.6.7.8:30001", kv["remote_addr"])
	require.Equal(t, "wssmux", kv["transport"])
	require.Equal(t, "tok-test", kv["token"])
	require.Equal(t, 8, kv["connection_pool"])
	require.Equal(t, false, kv["aggressive_pool"])
	require.Equal(t, 75, kv["keepalive_period"])
	require.Equal(t, 10, kv["dial_timeout"])
	require.Equal(t, true, kv["nodelay"])
	require.Equal(t, 3, kv["retry_interval"])
	require.Equal(t, 1, kv["mux_version"])
	require.Equal(t, 0, kv["web_port"])
	require.Equal(t, true, kv["skip_optz"])
	for _, k := range []string{"mux_con", "ports", "tls_cert", "bind_addr", "heartbeat"} {
		_, ok := kv[k]
		require.False(t, ok, "client must not carry %s", k)
	}
	require.Equal(t, []string{in.Paths.Binary, "-c", in.Paths.ConfigDir + "/client.toml"}, r.Unit.ExecStart)
	require.Empty(t, r.Binds, "the client binds nothing")
}

func TestAcceptUDPAndBinds(t *testing.T) {
	b := New()
	in := mixedFixture(t, TCP)
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	_, kv := parseTOML(t, r.Files[ServerFile])
	require.Equal(t, true, kv["accept_udp"])
	// The tcp and udp maps of one port share one entry.
	require.Equal(t, []string{"443=127.0.0.1:443", "2053=127.0.0.1:2053", "27015=127.0.0.1:27015"}, kv["ports"])
	// accept_udp starts TCP and UDP listeners for every entry.
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 27015, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)

	// Every listener accept_udp opens is a configured port map: nothing
	// else of the node (e.g. a loopback-only service on the other
	// protocol) is exposed on the hub.
	configured := map[string]bool{}
	for _, p := range in.Tunnel.Ports {
		configured[p.Proto+"/"+strconv.Itoa(p.Listen)] = true
	}
	for _, bd := range r.Binds {
		if bd.Purpose == "user" {
			require.True(t, configured[bd.Proto+"/"+strconv.Itoa(bd.Port)], "unconfigured listener %s/%d", bd.Proto, bd.Port)
		}
	}

	// A TCP-only tunnel keeps accept_udp off.
	r, err = b.Render(fixture(t, TCP), backend.SideHub)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ServerFile])
	require.Equal(t, false, kv["accept_udp"])

	// A port mapped for one protocol only would get a listener for the
	// other one as well: refused.
	for _, extra := range []config.PortMap{
		{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"}, // udp only; 443/2053 tcp only
		{Listen: 53, Proto: "udp", Target: "127.0.0.1:53"},
	} {
		asym := fixture(t, TCP)
		asym.Tunnel.Ports = append(asym.Tunnel.Ports, extra)
		err = b.Validate(asym)
		require.True(t, deyerr.HasCode(err, deyerr.B006), "%v", err)
		require.Contains(t, deyerr.As(err).Message(), "would also expose")
	}
	// A UDP-only tunnel on backhaul/tcp would open TCP listeners too.
	err = b.Validate(udpFixture(t, TCP))
	require.True(t, deyerr.HasCode(err, deyerr.B006), "%v", err)
}

func TestUDPTransport(t *testing.T) {
	b := New()
	in := udpFixture(t, UDP)
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	_, kv := parseTOML(t, r.Files[ServerFile])
	require.Equal(t, "udp", kv["transport"])
	require.Equal(t, []string{"27015=127.0.0.1:27015", "5353=127.0.0.1:53"}, kv["ports"])
	for _, k := range []string{"keepalive_period", "nodelay", "mux_con", "accept_udp", "tls_cert"} {
		_, ok := kv[k]
		require.False(t, ok, "udp server must not carry %s", k)
	}
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 30001, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 5353, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)
	r, err = b.Render(in, backend.SideNode)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ClientFile])
	require.Equal(t, "udp", kv["transport"])
	_, ok := kv["nodelay"]
	require.False(t, ok)
}

func TestListenAddrAndEdgePorts(t *testing.T) {
	b := New()
	in := fixture(t, TCPMux)
	in.Tunnel.Ports = []config.PortMap{
		{Listen: 65535, Proto: "tcp", Target: "127.0.0.1:8443"},
		{Listen: 1, Proto: "tcp", Target: "[::1]:80"},
		{Listen: 8080, Proto: "", Target: ""},
	}
	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	_, kv := parseTOML(t, r.Files[ServerFile])
	require.Equal(t, []string{":65535=127.0.0.1:8443", ":1=[::1]:80", "8080=127.0.0.1:8080"}, kv["ports"])

	in.ListenAddr = "::"
	r, err = b.Render(in, backend.SideHub)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ServerFile])
	require.Equal(t, []string{"[::]:65535=127.0.0.1:8443", "[::]:1=[::1]:80", "[::]:8080=127.0.0.1:8080"}, kv["ports"])
	require.Equal(t, "::", r.Binds[1].Addr)

	// The canary never binds a public address, even if asked to.
	in.Canary = true
	in.ListenAddr = "0.0.0.0"
	r, err = b.Render(in, backend.SideHub)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ServerFile])
	require.Equal(t, []string{"127.0.0.1:65535=127.0.0.1:8443"}, kv["ports"])
	require.Len(t, r.Binds, 2)
	require.Equal(t, "127.0.0.1", r.Binds[1].Addr)

	// An IPv6 hub address is bracketed in remote_addr, and the server's
	// control port must then accept IPv6 (a 0.0.0.0 socket would not).
	in = fixture(t, UDP)
	in.Tunnel.Ports = []config.PortMap{{Listen: 53, Proto: "udp", Target: "127.0.0.1:53"}}
	in.Hub.PublicIP = "2001:db8::1"
	r, err = b.Render(in, backend.SideNode)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ClientFile])
	require.Equal(t, "[2001:db8::1]:30001", kv["remote_addr"])
	r, err = b.Render(in, backend.SideHub)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ServerFile])
	require.Equal(t, "[::]:30001", kv["bind_addr"])
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "::", Purpose: "control"},
		{Port: 30001, Proto: "udp", Addr: "::", Purpose: "control"},
		{Port: 53, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)
}

func TestConnectionPool(t *testing.T) {
	b := New()
	in := fixture(t, TCP)
	in.Tunnel.Ports = nil
	for p := 1000; p < 1012; p++ {
		in.Tunnel.Ports = append(in.Tunnel.Ports, config.PortMap{Listen: p, Proto: "tcp", Target: "127.0.0.1:" + strconv.Itoa(p)})
	}
	r, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	_, kv := parseTOML(t, r.Files[ClientFile])
	require.Equal(t, 12, kv["connection_pool"], "pool scales with the number of ports")

	in.Tunnel.Advanced = &config.Advanced{ConnectionPool: 32}
	r, err = b.Render(in, backend.SideNode)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ClientFile])
	require.Equal(t, 32, kv["connection_pool"], "advanced.connection_pool wins")
}

func TestTokenEscaping(t *testing.T) {
	b := New()
	in := fixture(t, TCP)
	in.Secrets.Token = `a"b\c`
	r, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	_, kv := parseTOML(t, r.Files[ClientFile])
	require.Equal(t, `a"b\c`, kv["token"])
}

func TestValidate(t *testing.T) {
	b := New()
	cases := []struct {
		name   string
		code   deyerr.Code
		mutate func(in *backend.RenderInput)
	}{
		{"udp port on tcp-only transport", deyerr.B010, func(in *backend.RenderInput) {
			in.Transport = transport(t, WSSMux)
			in.Tunnel.Ports = append(in.Tunnel.Ports, config.PortMap{Listen: 53, Proto: "udp", Target: "127.0.0.1:53"})
		}},
		{"tcp port on udp transport", deyerr.B010, func(in *backend.RenderInput) { in.Transport = transport(t, UDP) }},
		{"unknown transport", deyerr.B006, func(in *backend.RenderInput) {
			in.Transport = backend.Transport{Backend: "backhaul", Name: "quic"}
		}},
		{"foreign backend", deyerr.B006, func(in *backend.RenderInput) {
			in.Transport = backend.Transport{Backend: "rathole", Name: "tcp"}
		}},
		{"no ports", deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports = nil }},
		{"no token", deyerr.B006, func(in *backend.RenderInput) { in.Secrets.Token = "" }},
		{"no hub ip", deyerr.B006, func(in *backend.RenderInput) { in.Hub.PublicIP = " " }},
		{"control port", deyerr.B006, func(in *backend.RenderInput) { in.ControlPort = 0 }},
		{"control port high", deyerr.B006, func(in *backend.RenderInput) { in.ControlPort = 70000 }},
		{"tls missing", deyerr.B006, func(in *backend.RenderInput) { in.Secrets.TLSKeyFile = "" }},
		{"binary missing", deyerr.B006, func(in *backend.RenderInput) { in.Paths.Binary = "" }},
		{"config dir relative", deyerr.B006, func(in *backend.RenderInput) { in.Paths.ConfigDir = "rel" }},
		{"web port", deyerr.B006, func(in *backend.RenderInput) {
			in.Tunnel.Advanced = &config.Advanced{BackhaulWebPort: 2060}
		}},
		{"negative pool", deyerr.B006, func(in *backend.RenderInput) {
			in.Tunnel.Advanced = &config.Advanced{ConnectionPool: -1}
		}},
		{"bad target", deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "127.0.0.1" }},
		{"target with equals", deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "a=b:443" }},
		{"listen out of range", deyerr.B006, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 0 }},
		{"duplicate tcp", deyerr.B006, func(in *backend.RenderInput) {
			in.Tunnel.Ports[1].Listen = 443
			in.Tunnel.Ports[1].Target = "127.0.0.1:443"
		}},
		{"duplicate udp", deyerr.B006, func(in *backend.RenderInput) {
			in.Transport = transport(t, UDP)
			in.Tunnel.Ports = []config.PortMap{{Listen: 53, Proto: "udp", Target: "127.0.0.1:53"}, {Listen: 53, Proto: "udp", Target: "127.0.0.1:53"}}
		}},
		{"tcp and udp with different targets", deyerr.B006, func(in *backend.RenderInput) {
			in.Transport = transport(t, TCP)
			in.Tunnel.Ports = append(in.Tunnel.Ports, config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:8443"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t, WSSMux)
			c.mutate(&in)
			err := b.Validate(in)
			require.Error(t, err)
			require.True(t, deyerr.HasCode(err, c.code), "want %s, got %v", c.code, err)
			_, rerr := b.Render(in, backend.SideHub)
			require.True(t, deyerr.HasCode(rerr, c.code), "render must fail the same way: %v", rerr)
		})
	}

	// Canary only renders (and checks) the first port map.
	in := fixture(t, WSSMux)
	in.Canary = true
	in.Tunnel.Ports = append(in.Tunnel.Ports, config.PortMap{Listen: 53, Proto: "udp", Target: "127.0.0.1:53"})
	require.NoError(t, b.Validate(in))
}

func TestUDPCompanion(t *testing.T) {
	b := New()
	require.Equal(t, UDP, transport(t, WSSMux).UDPCompanion)
	require.Empty(t, transport(t, TCP).UDPCompanion, "backhaul/tcp carries UDP itself (accept_udp)")
	require.Empty(t, transport(t, UDP).UDPCompanion)
	mixed := []string{config.ProtoTCP, config.ProtoUDP}
	require.True(t, backend.NeedsUDPFor(transport(t, WSSMux), mixed), "the companion carries UDP datagrams")
	require.False(t, backend.NeedsUDPFor(transport(t, WSSMux), []string{config.ProtoTCP}))
	require.False(t, backend.NeedsUDPFor(transport(t, TCP), mixed))
	require.True(t, backend.NeedsUDPFor(transport(t, UDP), []string{config.ProtoUDP}))

	in := fixture(t, WSSMux)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"},
	)
	// Without a companion control port the UDP maps cannot be carried.
	requireCode(t, b.Validate(in), deyerr.B010)
	in.CompanionControlPort = 30002
	require.True(t, in.UsesCompanion())

	r, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Len(t, r.Files, 2)
	_, kv := parseTOML(t, r.Files[ServerFile])
	require.Equal(t, "wssmux", kv["transport"])
	require.Equal(t, "0.0.0.0:30001", kv["bind_addr"])
	require.Equal(t, []string{"443=127.0.0.1:443", "2053=127.0.0.1:2053"}, kv["ports"])
	_, kv = parseTOML(t, r.Files[ServerUDPFile])
	require.Equal(t, "udp", kv["transport"])
	require.Equal(t, "0.0.0.0:30002", kv["bind_addr"])
	require.Equal(t, []string{"443=127.0.0.1:443", "27015=127.0.0.1:27015"}, kv["ports"])
	_, ok := kv["tls_cert"]
	require.False(t, ok)
	dir := in.Paths.ConfigDir
	require.Equal(t, []string{"/usr/local/bin/deyroute", "pair",
		in.Paths.Binary, "-c", dir + "/server.toml", "--",
		in.Paths.Binary, "-c", dir + "/server-udp.toml"}, r.Unit.ExecStart)
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 30002, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 30002, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)

	r, err = b.Render(in, backend.SideNode)
	require.NoError(t, err)
	_, kv = parseTOML(t, r.Files[ClientFile])
	require.Equal(t, "5.6.7.8:30001", kv["remote_addr"])
	_, kv = parseTOML(t, r.Files[ClientUDPFile])
	require.Equal(t, "5.6.7.8:30002", kv["remote_addr"])
	require.Equal(t, "udp", kv["transport"])
	require.Equal(t, "pair", r.Unit.ExecStart[1])
	require.Equal(t, dir+"/client-udp.toml", r.Unit.ExecStart[len(r.Unit.ExecStart)-1])

	// A TCP-only tunnel never starts the companion, even with a port.
	tcpOnly := fixture(t, WSSMux)
	tcpOnly.CompanionControlPort = 30002
	r, err = b.Render(tcpOnly, backend.SideHub)
	require.NoError(t, err)
	require.Len(t, r.Files, 1)
	require.Equal(t, in.Paths.Binary, r.Unit.ExecStart[0])

	bad := in
	bad.CompanionControlPort = in.ControlPort
	requireCode(t, b.Validate(bad), deyerr.B006)
	bad = in
	bad.Paths.SelfBinary = "deyroute"
	requireCode(t, b.Validate(bad), deyerr.B006)
	bad = in
	bad.Tunnel.Ports = []config.PortMap{{Listen: 53, Proto: "udp"}, {Listen: 53, Proto: "udp"}}
	bad.Tunnel.Ports = append(bad.Tunnel.Ports, config.PortMap{Listen: 80, Proto: "tcp"})
	requireCode(t, b.Validate(bad), deyerr.B006)
}

func requireCode(t *testing.T, err error, code deyerr.Code) {
	t.Helper()
	require.True(t, deyerr.HasCode(err, code), "want %s, got %v", code, err)
}

// TestBackendTier: no tier and the medium tier render the defaults byte for
// byte; each side scales with its own tier.
func TestBackendTier(t *testing.T) {
	b := New()
	for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
		def, err := b.Render(fixture(t, WSSMux), side)
		require.NoError(t, err)
		in := fixture(t, WSSMux)
		in.HubTier, in.NodeTier = config.BackendTierMedium, config.BackendTierMedium
		med, err := b.Render(in, side)
		require.NoError(t, err)
		require.Equal(t, def, med)
	}

	in := fixture(t, WSSMux)
	in.HubTier, in.NodeTier = config.BackendTierLarge, config.BackendTierSmall
	hub, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Contains(t, string(hub.Files[ServerFile]), "channel_size = 4096\n")
	require.Contains(t, string(hub.Files[ServerFile]), "mux_recievebuffer = 8388608\n")
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Contains(t, string(node.Files[ClientFile]), "mux_recievebuffer = 2097152\n")

	require.Equal(t, 1024, tierChannelSize(config.BackendTierSmall))
	require.Equal(t, channelSize, tierChannelSize("bogus"))
	require.Equal(t, muxReceiveBuffer, tierReceiveBuffer(""))
}
