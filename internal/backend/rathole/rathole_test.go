package rathole

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixedKeys is a deterministic, valid X25519 keypair for golden files.
func fixedKeys(t *testing.T) map[string]string {
	t.Helper()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	priv, err := ecdh.X25519().NewPrivateKey(seed)
	require.NoError(t, err)
	return map[string]string{
		KeyNoisePrivate: base64.StdEncoding.EncodeToString(priv.Bytes()),
		KeyNoisePublic:  base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()),
	}
}

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
	dir := "/etc/deyroute/backends/rathole/main/de-1/" + name
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
			Token:          "tok-test",
			TLSCertFile:    dir + "/tls-cert.pem",
			TLSKeyFile:     dir + "/tls-key.pem",
			TLSP12File:     dir + "/tls.p12",
			TLSP12Password: "p12-test",
			CAFile:         dir + "/ca.crt",
			ServerName:     "5.6.7.8",
			Keys:           fixedKeys(t),
		},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/rathole/v0.5.0/rathole",
			BinDir:     "/var/lib/deyroute/bin/rathole/v0.5.0",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// mixedFixture adds UDP port maps (rathole carries UDP over its TCP link).
func mixedFixture(t *testing.T, name string) backend.RenderInput {
	in := fixture(t, name)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp", Target: "10.0.0.5:27016"},
	)
	return in
}

func canaryFixture(t *testing.T, name string) backend.RenderInput {
	in := fixture(t, name)
	in.Canary = true
	in.ListenAddr = "127.0.0.1"
	in.ControlPort = 30002
	in.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:39002"}}
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

type goldenCase struct {
	golden string
	in     backend.RenderInput
}

func goldenCases(t *testing.T) []goldenCase {
	ipv6 := fixture(t, TCP)
	ipv6.ListenAddr = "::"
	return []goldenCase{
		{"noise", fixture(t, Noise)},
		{"tls", fixture(t, TLS)},
		{"tcp", fixture(t, TCP)},
		{"noise_mixed", mixedFixture(t, Noise)},
		{"tls_mixed", mixedFixture(t, TLS)},
		{"tcp_ipv6", ipv6},
		{"noise_canary", canaryFixture(t, Noise)},
	}
}

func TestGolden(t *testing.T) {
	b := New()
	for _, c := range goldenCases(t) {
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
	require.Equal(t, Name, b.Name())
	want := map[string]struct {
		stealth int
		tls     bool
	}{
		Noise: {3, false},
		TLS:   {3, true},
		TCP:   {1, false},
	}
	trs := b.Transports()
	require.Len(t, trs, len(want))
	for _, tr := range trs {
		w, ok := want[tr.Name]
		require.True(t, ok, tr.Name)
		require.Equal(t, Name, tr.Backend)
		require.Equal(t, backend.Reverse, tr.Direction, tr.Name)
		require.Equal(t, []string{"tcp", "udp"}, tr.Protos, tr.Name)
		require.Equal(t, w.stealth, tr.Stealth, tr.Name)
		require.Equal(t, w.tls, tr.NeedsTLS, tr.Name)
		require.False(t, tr.NeedsUDP, tr.Name)
		require.True(t, tr.Supports("udp"))
		require.False(t, tr.ClientIPPreserved)
		require.False(t, tr.Optional)
		require.False(t, tr.NeverQuarantine)
	}
	// The default ladder rung exists.
	require.Equal(t, "rathole/noise", trs[0].ID())
}

func TestRegisteredAndManifest(t *testing.T) {
	for _, id := range []string{"rathole/noise", "rathole/tls", "rathole/tcp"} {
		b, tr, err := backend.Lookup(id)
		require.NoError(t, err, id)
		require.Equal(t, Name, b.Name())
		require.Equal(t, id, tr.ID())
	}
	m := New().Manifest()
	require.Equal(t, Name, m.Name)
	require.Equal(t, "v0.5.0", m.Version)
	require.Equal(t, "rapiz1/rathole", m.Repo)
	require.Equal(t, "zip", m.Archive)
	require.Equal(t, "rathole", m.Binary())
	require.Contains(t, m.URLs["amd64"], "/v0.5.0/rathole-x86_64-unknown-linux-gnu.zip")
	require.Contains(t, m.URLs["arm64"], "/v0.5.0/rathole-aarch64-unknown-linux-musl.zip")
	_, err := New().Probe(context.Background(), fixture(t, Noise))
	require.ErrorIs(t, err, backend.ErrNoProbe)
	var _ backend.KeyGenerator = New()
}

func TestGenerateKeys(t *testing.T) {
	b := New()
	keys, err := b.GenerateKeys(transport(t, Noise))
	require.NoError(t, err)
	require.Len(t, keys, 2)
	fail := func(reason string) error { return errors.New(reason) }
	require.NoError(t, checkNoiseKeys(keys, fail))
	priv, err := base64.StdEncoding.DecodeString(keys[KeyNoisePrivate])
	require.NoError(t, err)
	require.Len(t, priv, 32)

	// Keys are fresh every call.
	other, err := b.GenerateKeys(transport(t, TCP))
	require.NoError(t, err)
	require.NotEqual(t, keys[KeyNoisePrivate], other[KeyNoisePrivate])

	// A fixture with generated keys renders them on the right side only.
	in := fixture(t, Noise)
	in.Secrets.Keys = keys
	hub, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Contains(t, string(hub.Files[ServerFile]), keys[KeyNoisePrivate])
	require.NotContains(t, string(hub.Files[ServerFile]), keys[KeyNoisePublic])
	require.Contains(t, string(node.Files[ClientFile]), keys[KeyNoisePublic])
	require.NotContains(t, string(node.Files[ClientFile]), keys[KeyNoisePrivate])
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestGenerateKeysFailure(t *testing.T) {
	b := &Backend{rand: failingReader{}}
	_, err := b.GenerateKeys(transport(t, Noise))
	require.True(t, deyerr.HasCode(err, deyerr.B009), "%v", err)

	// A zero Backend falls back to crypto/rand.
	keys, err := (&Backend{}).GenerateKeys(transport(t, Noise))
	require.NoError(t, err)
	require.Len(t, keys, 2)
}

// TestUpstreamKeyEncoding checks the key encoding against the keypairs
// published in rathole v0.5.0 (docs/transport.md and examples/noise_nk):
// the public key derived by crypto/ecdh equals the one rathole printed.
func TestUpstreamKeyEncoding(t *testing.T) {
	fail := func(reason string) error { return errors.New(reason) }
	pairs := []map[string]string{
		{KeyNoisePrivate: "cQ/vwIqNPJZmuM/OikglzBo/+jlYGrOt9i0k5h5vn1Q=", KeyNoisePublic: "GQYTKSbWLBUSZiGfdWPSgek9yoOuaiwGD/GIX8Z1kkE="},
		{KeyNoisePrivate: "QLYMByBnjgM254zT6YKaBVvuAA61swyZfFxoA/SKZHM=", KeyNoisePublic: "xrpknQcAagcd/b9foMwxSCD+EindWxq450NEONk8XQo="},
	}
	for _, p := range pairs {
		require.NoError(t, checkNoiseKeys(p, fail))
	}
}

func TestValidateErrors(t *testing.T) {
	b := New()
	cases := []struct {
		name   string
		mutate func(in *backend.RenderInput)
		code   deyerr.Code
		reason string
	}{
		{"foreign backend", func(in *backend.RenderInput) { in.Transport.Backend = "frp" }, deyerr.B006, "not a rathole transport"},
		{"unknown transport", func(in *backend.RenderInput) { in.Transport.Name = "websocket" }, deyerr.B006, "not a rathole transport"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"bad proto", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "sctp" }, deyerr.B010, ""},
		{"ctl port", func(in *backend.RenderInput) { in.ControlPort = 0 }, deyerr.B006, "control port"},
		{"ctl port high", func(in *backend.RenderInput) { in.ControlPort = 70000 }, deyerr.B006, "control port"},
		{"token", func(in *backend.RenderInput) { in.Secrets.Token = "" }, deyerr.B006, "token"},
		{"hub ip", func(in *backend.RenderInput) { in.Hub.PublicIP = " " }, deyerr.B006, "hub public IP"},
		{"binary", func(in *backend.RenderInput) { in.Paths.Binary = "rathole" }, deyerr.B006, "binary"},
		{"config dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "" }, deyerr.B006, "config directory"},
		{"noise keys missing", func(in *backend.RenderInput) { in.Secrets.Keys = nil }, deyerr.B006, "has not been generated"},
		{"noise private bad", func(in *backend.RenderInput) { in.Secrets.Keys[KeyNoisePrivate] = "!!" }, deyerr.B006, "private key is not 32 bytes"},
		{"noise private short", func(in *backend.RenderInput) { in.Secrets.Keys[KeyNoisePrivate] = "AAAA" }, deyerr.B006, "private key is not 32 bytes"},
		{"noise public bad", func(in *backend.RenderInput) { in.Secrets.Keys[KeyNoisePublic] = "AAAA" }, deyerr.B006, "public key is not 32 bytes"},
		{"noise mismatch", func(in *backend.RenderInput) {
			in.Secrets.Keys[KeyNoisePublic] = "GQYTKSbWLBUSZiGfdWPSgek9yoOuaiwGD/GIX8Z1kkE="
		}, deyerr.B006, "does not belong"},
		{"listen range", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 0 }, deyerr.B006, "out of range"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "nohost" }, deyerr.B006, "invalid target"},
		{"target quote", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "a\"b:443" }, deyerr.B006, "invalid target"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1] = in.Tunnel.Ports[0] }, deyerr.B006, "listed twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t, Noise)
			c.mutate(&in)
			err := b.Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "%v", err)
			if c.reason != "" {
				require.Contains(t, deyerr.As(err).Message(), c.reason)
			}
			_, rerr := b.Render(in, backend.SideHub)
			require.Error(t, rerr)
		})
	}

	tlsCases := []struct {
		name   string
		mutate func(s *backend.Secrets)
		reason string
	}{
		{"p12", func(s *backend.Secrets) { s.TLSP12File = "" }, "PKCS#12 bundle"},
		{"p12 password", func(s *backend.Secrets) { s.TLSP12Password = "" }, "PKCS#12 password"},
		{"ca", func(s *backend.Secrets) { s.CAFile = "ca.crt" }, "CA certificate"},
	}
	for _, c := range tlsCases {
		t.Run("tls "+c.name, func(t *testing.T) {
			in := fixture(t, TLS)
			c.mutate(&in.Secrets)
			err := b.Validate(in)
			require.True(t, deyerr.HasCode(err, deyerr.B006), "%v", err)
			require.Contains(t, deyerr.As(err).Message(), c.reason)
		})
	}

	// Noise keys are not needed by tcp/tls; TLS files are not needed by
	// noise/tcp.
	in := fixture(t, TCP)
	in.Secrets = backend.Secrets{Token: "tok-test"}
	require.NoError(t, b.Validate(in))
}

func TestDefaultsAndCanary(t *testing.T) {
	b := New()
	in := fixture(t, TCP)
	in.Tunnel.Ports = []config.PortMap{{Listen: 8443}}
	hub, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	srv := parseTOML(t, hub.Files[ServerFile])
	cli := parseTOML(t, node.Files[ClientFile])
	require.Equal(t, "tcp", srv.str(t, "server.services.tcp-8443", "type"))
	require.Equal(t, "0.0.0.0:8443", srv.str(t, "server.services.tcp-8443", "bind_addr"))
	require.Equal(t, "127.0.0.1:8443", cli.str(t, "client.services.tcp-8443", "local_addr"))

	// A canary with a public listen address is forced to loopback and only
	// renders the first port map.
	c := canaryFixture(t, Noise)
	c.ListenAddr = "0.0.0.0"
	c.Tunnel.Ports = append(c.Tunnel.Ports, config.PortMap{Listen: 39003, Proto: "tcp"})
	hub, err = b.Render(c, backend.SideHub)
	require.NoError(t, err)
	srv = parseTOML(t, hub.Files[ServerFile])
	require.Equal(t, "127.0.0.1:39001", srv.str(t, "server.services.tcp-39001", "bind_addr"))
	require.NotContains(t, srv.tables, "server.services.tcp-39003")
	require.Equal(t, []backend.PortUse{
		{Port: 30002, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 39001, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"},
	}, hub.Binds)

	// The tls client omits hostname when no server name is known.
	tin := fixture(t, TLS)
	tin.Secrets.ServerName = ""
	node, err = b.Render(tin, backend.SideNode)
	require.NoError(t, err)
	cli = parseTOML(t, node.Files[ClientFile])
	_, ok := cli.tables["client.transport.tls"]["hostname"]
	require.False(t, ok)
}

// TestParsedConfig parses the rendered TOML back and checks every key the
// pinned version needs, per transport and side.
func TestParsedConfig(t *testing.T) {
	b := New()
	keys := fixedKeys(t)
	for _, name := range []string{Noise, TLS, TCP} {
		in := mixedFixture(t, name)
		hub, err := b.Render(in, backend.SideHub)
		require.NoError(t, err)
		node, err := b.Render(in, backend.SideNode)
		require.NoError(t, err)
		require.Len(t, hub.Files, 1)
		require.Len(t, node.Files, 1)

		srv := parseTOML(t, hub.Files[ServerFile])
		require.Equal(t, "0.0.0.0:30001", srv.str(t, "server", "bind_addr"))
		require.Equal(t, "tok-test", srv.str(t, "server", "default_token"))
		require.Equal(t, "20", srv.tables["server"]["heartbeat_interval"])
		require.Equal(t, name, srv.str(t, "server.transport", "type"))

		cli := parseTOML(t, node.Files[ClientFile])
		require.Equal(t, "5.6.7.8:30001", cli.str(t, "client", "remote_addr"))
		require.Equal(t, "tok-test", cli.str(t, "client", "default_token"))
		require.Equal(t, "3", cli.tables["client"]["retry_interval"])
		hbTimeout, err := strconv.Atoi(cli.tables["client"]["heartbeat_timeout"])
		require.NoError(t, err)
		require.Greater(t, hbTimeout, heartbeatInterval, "v0.5.0: heartbeat_timeout must exceed heartbeat_interval")
		require.Equal(t, name, cli.str(t, "client.transport", "type"))

		switch name {
		case Noise:
			require.Equal(t, NoisePattern, srv.str(t, "server.transport.noise", "pattern"))
			require.Equal(t, keys[KeyNoisePrivate], srv.str(t, "server.transport.noise", "local_private_key"))
			require.Equal(t, NoisePattern, cli.str(t, "client.transport.noise", "pattern"))
			require.Equal(t, keys[KeyNoisePublic], cli.str(t, "client.transport.noise", "remote_public_key"))
		case TLS:
			require.Equal(t, in.Secrets.TLSP12File, srv.str(t, "server.transport.tls", "pkcs12"))
			require.Equal(t, "p12-test", srv.str(t, "server.transport.tls", "pkcs12_password"))
			require.Equal(t, in.Secrets.CAFile, cli.str(t, "client.transport.tls", "trusted_root"))
			require.Equal(t, "5.6.7.8", cli.str(t, "client.transport.tls", "hostname"))
		default:
			require.NotContains(t, srv.tables, "server.transport.noise")
			require.NotContains(t, srv.tables, "server.transport.tls")
		}

		// One service per port map on both sides, same names.
		wantSvc := map[string][2]string{
			"tcp-443":   {"0.0.0.0:443", "127.0.0.1:443"},
			"tcp-2053":  {"0.0.0.0:2053", "127.0.0.1:2053"},
			"udp-443":   {"0.0.0.0:443", "127.0.0.1:443"},
			"udp-27015": {"0.0.0.0:27015", "10.0.0.5:27016"},
		}
		for svc, addrs := range wantSvc {
			proto := svc[:3]
			require.Equal(t, proto, srv.str(t, "server.services."+svc, "type"))
			require.Equal(t, addrs[0], srv.str(t, "server.services."+svc, "bind_addr"))
			require.Equal(t, proto, cli.str(t, "client.services."+svc, "type"))
			require.Equal(t, addrs[1], cli.str(t, "client.services."+svc, "local_addr"))
		}
		require.Equal(t, len(wantSvc), countPrefix(srv.tables, "server.services."))
		require.Equal(t, len(wantSvc), countPrefix(cli.tables, "client.services."))

		// Units: explicit mode flag and absolute config path.
		require.Equal(t, []string{in.Paths.Binary, "--server", in.Paths.ConfigDir + "/server.toml"}, hub.Unit.ExecStart)
		require.Equal(t, []string{in.Paths.Binary, "--client", in.Paths.ConfigDir + "/client.toml"}, node.Unit.ExecStart)
		require.Equal(t, in.Paths.ConfigDir, hub.Unit.WorkingDirectory)
		require.Contains(t, hub.Unit.DropHardening, "MemoryDenyWriteExecute")
		require.False(t, hub.Unit.RunAsRoot)
		require.Empty(t, hub.Unit.ExtraCaps)

		// Hub binds the control port and every user port; node binds none.
		require.Equal(t, []backend.PortUse{
			{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
			{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		}, hub.Binds)
		require.Empty(t, node.Binds)
		require.Empty(t, hub.NAT)
		require.False(t, hub.IPForward)
	}
}

// TestNodeDialsOnlyTargets is the S17-style check for this Reverse
// backend: the node client only ever connects to the configured targets
// (fixed local_addr per service) and binds no listener.
func TestNodeDialsOnlyTargets(t *testing.T) {
	in := mixedFixture(t, Noise)
	node, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	cli := parseTOML(t, node.Files[ClientFile])
	allowed := map[string]bool{}
	for _, p := range in.Tunnel.Ports {
		allowed[p.Target] = true
	}
	for name, kv := range cli.tables {
		if strings.HasPrefix(name, "client.services.") {
			require.True(t, allowed[cli.str(t, name, "local_addr")], name)
			require.NotContains(t, kv, "bind_addr")
		}
	}
	require.Empty(t, node.Binds)
}

// TestTrustedRootByTLSMode: the internal CA never issued a custom
// certificate, so with tls.mode custom the node trusts the served
// certificate itself (native-tls keeps the system store for public CAs).
func TestTrustedRootByTLSMode(t *testing.T) {
	b := New()
	for mode, want := range map[string]string{
		"":                   "/ca.crt",
		config.TLSModeAuto:   "/ca.crt",
		config.TLSModeACME:   "/ca.crt",
		config.TLSModeCustom: "/tls-cert.pem",
	} {
		in := fixture(t, TLS)
		in.Tunnel.TLS.Mode = mode
		require.NoError(t, b.Validate(in), mode)
		node, err := b.Render(in, backend.SideNode)
		require.NoError(t, err)
		cli := parseTOML(t, node.Files[ClientFile])
		require.Equal(t, in.Paths.ConfigDir+want, cli.str(t, "client.transport.tls", "trusted_root"), mode)
		require.NotContains(t, string(node.Files[ClientFile]), "tls.p12", "the node never gets the PKCS#12 bundle")
		require.NotContains(t, string(node.Files[ClientFile]), in.Secrets.TLSP12Password)

		in.Secrets.CAFile = ""
		err = b.Validate(in)
		if want == "/ca.crt" {
			require.True(t, deyerr.HasCode(err, deyerr.B006), "%s: %v", mode, err)
		} else {
			require.NoError(t, err, mode)
		}
	}
}

// TestHostAndCommentHygiene: a padded or bracketed hub address still yields
// a valid remote_addr, and ids cannot inject lines through the header.
func TestHostAndCommentHygiene(t *testing.T) {
	b := New()
	for ip, want := range map[string]string{
		" 5.6.7.8 ":       "5.6.7.8:30001",
		"[2001:db8::1]":   "[2001:db8::1]:30001",
		" 2001:db8::1\t":  "[2001:db8::1]:30001",
		"hub.example.com": "hub.example.com:30001",
	} {
		in := fixture(t, Noise)
		in.Hub.PublicIP = ip
		node, err := b.Render(in, backend.SideNode)
		require.NoError(t, err, ip)
		cli := parseTOML(t, node.Files[ClientFile])
		require.Equal(t, want, cli.str(t, "client", "remote_addr"), ip)
	}
	in := fixture(t, Noise)
	in.Hub.PublicIP = "[]"
	require.True(t, deyerr.HasCode(b.Validate(in), deyerr.B006))

	in = fixture(t, Noise)
	in.Tunnel.ID = "main\n[server.services.evil]\nbind_addr = \"0.0.0.0:22\""
	in.Node.ID = "de-1\r\n"
	for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
		r, err := b.Render(in, side)
		require.NoError(t, err)
		for _, data := range r.Files {
			doc := parseTOML(t, data) // fails on an injected line
			require.NotContains(t, doc.tables, "server.services.evil")
		}
	}
}

func countPrefix(m map[string]map[string]string, prefix string) int {
	n := 0
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			n++
		}
	}
	return n
}

// tomlDoc is a structural TOML parse: table name → key → raw value.
type tomlDoc struct {
	tables map[string]map[string]string
}

func (d tomlDoc) str(t *testing.T, table, key string) string {
	t.Helper()
	raw, ok := d.tables[table][key]
	require.True(t, ok, "missing %s.%s", table, key)
	s, err := strconv.Unquote(raw)
	require.NoError(t, err, "%s.%s is not a string: %s", table, key, raw)
	return s
}

var (
	tableRe = regexp.MustCompile(`^\[([A-Za-z0-9_.-]+)\]$`)
	keyRe   = regexp.MustCompile(`^([A-Za-z0-9_-]+) = (.+)$`)
	valueRe = regexp.MustCompile(`^("(?:[^"\\]|\\.)*"|-?[0-9]+|true|false)$`)
)

// parseTOML checks the subset of TOML the renderer emits: comments, table
// headers, and key = string|integer|bool lines; no duplicate tables or
// keys; no key before the first table.
func parseTOML(t *testing.T, data []byte) tomlDoc {
	t.Helper()
	doc := tomlDoc{tables: map[string]map[string]string{}}
	cur := ""
	for i, line := range strings.Split(string(data), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case tableRe.MatchString(line):
			cur = tableRe.FindStringSubmatch(line)[1]
			_, dup := doc.tables[cur]
			require.False(t, dup, "line %d: duplicate table %s", i+1, cur)
			doc.tables[cur] = map[string]string{}
		case keyRe.MatchString(line):
			require.NotEmpty(t, cur, "line %d: key outside a table", i+1)
			m := keyRe.FindStringSubmatch(line)
			require.Regexp(t, valueRe, m[2], "line %d: bad value", i+1)
			_, dup := doc.tables[cur][m[1]]
			require.False(t, dup, "line %d: duplicate key %s", i+1, m[1])
			doc.tables[cur][m[1]] = m[2]
		default:
			t.Fatalf("line %d: not TOML: %q", i+1, line)
		}
	}
	return doc
}
