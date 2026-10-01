package frp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

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
	dir := "/etc/deyroute/backends/frp/main/de-1/" + name
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
			TLSCertFile: dir + "/tls-cert.pem",
			TLSKeyFile:  dir + "/tls-key.pem",
			CAFile:      dir + "/ca.crt",
			ServerName:  "5.6.7.8",
		},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/frp/v0.71.0/frps",
			BinDir:     "/var/lib/deyroute/bin/frp/v0.71.0",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// mixedFixture adds UDP port maps (frp carries UDP proxies on every
// transport).
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

func TestGolden(t *testing.T) {
	b := New()
	ipv6 := fixture(t, TCP)
	ipv6.ListenAddr = "::"
	pool := fixture(t, WebSocket)
	pool.Tunnel.Advanced = &config.Advanced{ConnectionPool: 16}
	acme := fixture(t, TCP)
	acme.Tunnel.TLS.Mode = config.TLSModeACME
	acme.Hub.Domain = "hub.example.com"
	acme.Secrets.ServerName = "hub.example.com"
	cases := []struct {
		golden string
		in     backend.RenderInput
	}{
		{"tcp", fixture(t, TCP)},
		{"websocket", fixture(t, WebSocket)},
		{"quic", fixture(t, QUIC)},
		{"kcp", fixture(t, KCP)},
		{"tcp_mixed", mixedFixture(t, TCP)},
		{"quic_mixed", mixedFixture(t, QUIC)},
		{"tcp_ipv6", ipv6},
		{"websocket_pool", pool},
		{"tcp_canary", canaryFixture(t, TCP)},
		{"tcp_acme", acme},
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
	require.Equal(t, Name, b.Name())
	want := map[string]struct {
		stealth int
		udp     bool
	}{
		WSS:       {4, false},
		WebSocket: {2, false},
		QUIC:      {3, true},
		KCP:       {2, true},
		TCP:       {3, false},
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
		require.Equal(t, w.udp, tr.NeedsUDP, tr.Name)
		require.True(t, tr.NeedsTLS, tr.Name)
		require.False(t, tr.ClientIPPreserved)
		require.False(t, tr.Optional)
		require.False(t, tr.NeverQuarantine)
	}
	require.Equal(t, "frp/wss", trs[0].ID())
}

func TestRegisteredAndManifest(t *testing.T) {
	for _, name := range []string{WSS, WebSocket, QUIC, KCP, TCP} {
		b, tr, err := backend.Lookup("frp/" + name)
		require.NoError(t, err)
		require.Equal(t, Name, b.Name())
		require.Equal(t, name, tr.Name)
	}
	m := New().Manifest()
	require.Equal(t, "v0.71.0", m.Version)
	require.Equal(t, "fatedier/frp", m.Repo)
	require.Equal(t, "tar.gz", m.Archive)
	require.Equal(t, []string{"frps", "frpc"}, m.Binaries)
	require.Contains(t, m.URLs["amd64"], "/v0.71.0/frp_0.71.0_linux_amd64.tar.gz")
	require.Contains(t, m.URLs["arm64"], "/v0.71.0/frp_0.71.0_linux_arm64.tar.gz")
	_, err := New().Probe(context.Background(), fixture(t, TCP))
	require.ErrorIs(t, err, backend.ErrNoProbe)
}

// TestWSSUnavailable documents why frp/wss is refused: frps v0.71.0 cannot
// terminate wss itself.
func TestWSSUnavailable(t *testing.T) {
	b := New()
	in := fixture(t, WSS)
	err := b.Validate(in)
	require.True(t, deyerr.HasCode(err, deyerr.B006), "%v", err)
	require.Contains(t, deyerr.As(err).Message(), "cannot accept wss")
	for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
		_, err := b.Render(in, side)
		require.True(t, deyerr.HasCode(err, deyerr.B006))
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
		{"foreign backend", func(in *backend.RenderInput) { in.Transport.Backend = "rathole" }, deyerr.B006, "not an frp transport"},
		{"unknown transport", func(in *backend.RenderInput) { in.Transport.Name = "xtcp" }, deyerr.B006, "not an frp transport"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"bad proto", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Proto = "sctp" }, deyerr.B010, ""},
		{"ctl port", func(in *backend.RenderInput) { in.ControlPort = -1 }, deyerr.B006, "control port"},
		{"token", func(in *backend.RenderInput) { in.Secrets.Token = "" }, deyerr.B006, "token"},
		{"hub ip", func(in *backend.RenderInput) { in.Hub.PublicIP = "" }, deyerr.B006, "hub public IP"},
		{"cert", func(in *backend.RenderInput) { in.Secrets.TLSCertFile = "" }, deyerr.B006, "TLS certificate or key"},
		{"key", func(in *backend.RenderInput) { in.Secrets.TLSKeyFile = "key.pem" }, deyerr.B006, "TLS certificate or key"},
		{"ca", func(in *backend.RenderInput) { in.Secrets.CAFile = "" }, deyerr.B006, "CA certificate"},
		{"bin dir", func(in *backend.RenderInput) { in.Paths.BinDir = "bin" }, deyerr.B006, "binaries"},
		{"config dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "" }, deyerr.B006, "config directory"},
		{"template token", func(in *backend.RenderInput) { in.Secrets.Token = "a{{ .Envs.HOME }}" }, deyerr.B006, "template"},
		{"template sni", func(in *backend.RenderInput) { in.Secrets.ServerName = "x}}" }, deyerr.B006, "template"},
		{"negative pool", func(in *backend.RenderInput) { in.Tunnel.Advanced = &config.Advanced{ConnectionPool: -1} }, deyerr.B006, "connection_pool"},
		{"listen range", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 65536 }, deyerr.B006, "out of range"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "127.0.0.1" }, deyerr.B006, "invalid target"},
		{"template target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "{{x}}:443" }, deyerr.B006, "invalid target"},
		{"empty host", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "[]:443" }, deyerr.B006, "invalid target"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1] = in.Tunnel.Ports[0] }, deyerr.B006, "listed twice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t, TCP)
			c.mutate(&in)
			err := b.Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "%v", err)
			if c.reason != "" {
				require.Contains(t, deyerr.As(err).Message(), c.reason)
			}
			_, rerr := b.Render(in, backend.SideNode)
			require.Error(t, rerr)
		})
	}
}

// TestParsedConfig parses the rendered TOML back and checks the keys of
// the pinned version, per transport and side.
func TestParsedConfig(t *testing.T) {
	b := New()
	for _, name := range []string{WebSocket, QUIC, KCP, TCP} {
		t.Run(name, func(t *testing.T) {
			in := mixedFixture(t, name)
			hub, err := b.Render(in, backend.SideHub)
			require.NoError(t, err)
			node, err := b.Render(in, backend.SideNode)
			require.NoError(t, err)

			srv := parseTOML(t, hub.Files[ServerFile])
			require.Empty(t, srv.proxies)
			require.Equal(t, "0.0.0.0", srv.str(t, "bindAddr"))
			require.Equal(t, 30001, srv.num(t, "bindPort"))
			require.Equal(t, "0.0.0.0", srv.str(t, "proxyBindAddr"))
			require.Equal(t, "token", srv.str(t, "auth.method"))
			require.Equal(t, "tok-test", srv.str(t, "auth.token"))
			require.Equal(t, "true", srv.top["transport.tls.force"])
			require.Equal(t, in.Secrets.TLSCertFile, srv.str(t, "transport.tls.certFile"))
			require.Equal(t, in.Secrets.TLSKeyFile, srv.str(t, "transport.tls.keyFile"))
			require.Equal(t, 32, srv.num(t, "transport.maxPoolCount"))
			require.Equal(t, "true", srv.top["transport.tcpMux"])
			require.Equal(t, "console", srv.str(t, "log.to"))
			require.Equal(t, "[{ single = 443 }, { single = 2053 }, { single = 27015 }]", srv.top["allowPorts"])
			// No dashboard, no vhost ports, no ssh gateway.
			for _, k := range []string{"webServer.port", "vhostHTTPPort", "vhostHTTPSPort", "sshTunnelGateway.bindPort"} {
				require.NotContains(t, srv.top, k)
			}
			switch name {
			case QUIC:
				require.Equal(t, 30001, srv.num(t, "quicBindPort"))
				require.NotContains(t, srv.top, "kcpBindPort")
			case KCP:
				require.Equal(t, 30001, srv.num(t, "kcpBindPort"))
				require.NotContains(t, srv.top, "quicBindPort")
			default:
				require.NotContains(t, srv.top, "quicBindPort")
				require.NotContains(t, srv.top, "kcpBindPort")
			}

			cli := parseTOML(t, node.Files[ClientFile])
			require.Equal(t, "5.6.7.8", cli.str(t, "serverAddr"))
			require.Equal(t, 30001, cli.num(t, "serverPort"))
			require.Equal(t, "false", cli.top["loginFailExit"])
			require.Equal(t, "token", cli.str(t, "auth.method"))
			require.Equal(t, "tok-test", cli.str(t, "auth.token"))
			require.Equal(t, name, cli.str(t, "transport.protocol"))
			require.Equal(t, 8, cli.num(t, "transport.poolCount"))
			require.Equal(t, "true", cli.top["transport.tcpMux"])
			require.Equal(t, "true", cli.top["transport.tls.enable"])
			require.Equal(t, "true", cli.top["transport.tls.disableCustomTLSFirstByte"])
			require.Equal(t, in.Secrets.CAFile, cli.str(t, "transport.tls.trustedCaFile"))
			require.Equal(t, "5.6.7.8", cli.str(t, "transport.tls.serverName"))
			require.NotContains(t, cli.top, "webServer.port")

			want := []map[string]string{
				{"name": `"tcp-443"`, "type": `"tcp"`, "localIP": `"127.0.0.1"`, "localPort": "443", "remotePort": "443"},
				{"name": `"tcp-2053"`, "type": `"tcp"`, "localIP": `"127.0.0.1"`, "localPort": "2053", "remotePort": "2053"},
				{"name": `"udp-443"`, "type": `"udp"`, "localIP": `"127.0.0.1"`, "localPort": "443", "remotePort": "443"},
				{"name": `"udp-27015"`, "type": `"udp"`, "localIP": `"10.0.0.5"`, "localPort": "27016", "remotePort": "27015"},
			}
			require.Equal(t, want, cli.proxies)

			bin := in.Paths.BinDir
			require.Equal(t, []string{bin + "/frps", "-c", in.Paths.ConfigDir + "/frps.toml"}, hub.Unit.ExecStart)
			require.Equal(t, []string{bin + "/frpc", "-c", in.Paths.ConfigDir + "/frpc.toml"}, node.Unit.ExecStart)
			require.Equal(t, in.Paths.ConfigDir, node.Unit.WorkingDirectory)
			require.Empty(t, hub.Unit.DropHardening)
			require.False(t, hub.Unit.RunAsRoot)

			binds := []backend.PortUse{{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"}}
			if name == QUIC || name == KCP {
				binds = append(binds, backend.PortUse{Port: 30001, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"})
			}
			binds = append(binds,
				backend.PortUse{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
				backend.PortUse{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
				backend.PortUse{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
				backend.PortUse{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
			)
			require.Equal(t, binds, hub.Binds)
			require.Empty(t, node.Binds)
			require.Empty(t, hub.NAT)
		})
	}
}

func TestDefaultsCanaryAndPool(t *testing.T) {
	b := New()

	// Default target, IPv6 target, no server name, pool cap.
	in := fixture(t, TCP)
	in.Tunnel.Ports = []config.PortMap{{Listen: 8443}, {Listen: 9443, Target: "[::1]:9444"}}
	in.Secrets.ServerName = ""
	in.Tunnel.Advanced = &config.Advanced{ConnectionPool: 500}
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	cli := parseTOML(t, node.Files[ClientFile])
	require.Equal(t, `"127.0.0.1"`, cli.proxies[0]["localIP"])
	require.Equal(t, "8443", cli.proxies[0]["localPort"])
	require.Equal(t, `"::1"`, cli.proxies[1]["localIP"])
	require.Equal(t, "9444", cli.proxies[1]["localPort"])
	require.NotContains(t, cli.top, "transport.tls.serverName")
	require.Equal(t, maxPoolCount, cli.num(t, "transport.poolCount"))

	// A canary with a public listen address is forced to loopback and only
	// renders the first port map.
	c := canaryFixture(t, QUIC)
	c.ListenAddr = ""
	c.Tunnel.Ports = append(c.Tunnel.Ports, config.PortMap{Listen: 39003})
	hub, err := b.Render(c, backend.SideHub)
	require.NoError(t, err)
	srv := parseTOML(t, hub.Files[ServerFile])
	require.Equal(t, "127.0.0.1", srv.str(t, "proxyBindAddr"))
	require.Equal(t, "[{ single = 39001 }]", srv.top["allowPorts"])
	require.Equal(t, []backend.PortUse{
		{Port: 30002, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 30002, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 39001, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"},
	}, hub.Binds)
}

// TestNodeDialsOnlyTargets is the S17-style check for this Reverse
// backend: frpc only dials the configured targets and exposes nothing;
// frps only lets the client bind the tunnel's own listen ports.
func TestNodeDialsOnlyTargets(t *testing.T) {
	in := mixedFixture(t, KCP)
	node, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	cli := parseTOML(t, node.Files[ClientFile])
	allowed := map[string]bool{}
	for _, p := range in.Tunnel.Ports {
		allowed[p.Target] = true
	}
	for _, p := range cli.proxies {
		ip, err := strconv.Unquote(p["localIP"])
		require.NoError(t, err)
		require.True(t, allowed[backend.HostPort(ip, atoi(t, p["localPort"]))], "%v", p)
		require.NotContains(t, p, "plugin")
	}
	for k := range cli.top {
		require.False(t, strings.HasPrefix(k, "webServer."), k)
		require.False(t, strings.HasPrefix(k, "visitors"), k)
	}
	require.Empty(t, node.Binds)

	hub, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	srv := parseTOML(t, hub.Files[ServerFile])
	for _, p := range cli.proxies {
		require.Contains(t, srv.top["allowPorts"], "{ single = "+p["remotePort"]+" }")
	}
}

// TestTrustAnchorByTLSMode: with tls.mode acme/custom the hub presents a
// certificate the internal CA did not issue; frpc (which uses only
// trustedCaFile, no system roots) must then trust the served chain instead
// of the internal CA, or every frp rung fails the TLS handshake.
func TestTrustAnchorByTLSMode(t *testing.T) {
	b := New()
	for _, c := range []struct {
		mode, want string
	}{
		{"", "/ca.crt"},
		{config.TLSModeAuto, "/ca.crt"},
		{config.TLSModeACME, "/tls-cert.pem"},
		{config.TLSModeCustom, "/tls-cert.pem"},
	} {
		t.Run("mode "+c.mode, func(t *testing.T) {
			in := fixture(t, QUIC)
			in.Tunnel.TLS.Mode = c.mode
			require.NoError(t, b.Validate(in))
			node, err := b.Render(in, backend.SideNode)
			require.NoError(t, err)
			cli := parseTOML(t, node.Files[ClientFile])
			require.Equal(t, in.Paths.ConfigDir+c.want, cli.str(t, "transport.tls.trustedCaFile"))
			require.NotContains(t, string(node.Files[ClientFile]), "tls-key.pem", "the node never gets the TLS key")

			// The anchor is required; the other file is not.
			in.Secrets.CAFile = ""
			err = b.Validate(in)
			if c.want == "/ca.crt" {
				require.True(t, deyerr.HasCode(err, deyerr.B006), "%v", err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestLeafPinningVerifies proves the assumption behind trustAnchor for
// acme/custom: a Go TLS client (frpc) whose root pool holds only the served
// certificate chain accepts that certificate even though its issuer is not
// in the pool, and still checks the host name.
func TestLeafPinningVerifies(t *testing.T) {
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "public CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	ca, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "hub.example.com"},
		DNSNames:  []string{"hub.example.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(leafDER)
	require.NoError(t, err)

	// frp's newCertPool: one PEM file appended to an empty pool.
	certFile := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certFile))

	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "hub.example.com", CurrentTime: now})
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "evil.example.com", CurrentTime: now})
	require.Error(t, err, "host name is still verified")

	// Another certificate from the same CA is not accepted.
	otherDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(3), DNSNames: []string{"hub.example.com"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	other, err := x509.ParseCertificate(otherDER)
	require.NoError(t, err)
	_, err = other.Verify(x509.VerifyOptions{Roots: pool, DNSName: "hub.example.com", CurrentTime: now})
	require.Error(t, err)
}

// TestHostAndCommentHygiene: bracketed/padded hub addresses are normalized
// and ids cannot break out of the header comment or form a template action
// (frp executes the whole file as a Go template).
func TestHostAndCommentHygiene(t *testing.T) {
	b := New()
	in := fixture(t, TCP)
	in.Hub.PublicIP = " [2001:db8::1] "
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	cli := parseTOML(t, node.Files[ClientFile])
	require.Equal(t, "2001:db8::1", cli.str(t, "serverAddr"))

	in.Hub.PublicIP = "[]"
	require.True(t, deyerr.HasCode(b.Validate(in), deyerr.B006))

	in = fixture(t, TCP)
	in.Tunnel.ID = "main\nauth.token = \"x\"{{ .Envs.HOME }}"
	in.Node.ID = "de-1\r"
	for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
		r, err := b.Render(in, side)
		require.NoError(t, err)
		for _, data := range r.Files {
			require.NotContains(t, string(data), "{{")
			doc := parseTOML(t, data) // fails on an injected line
			require.NotEqual(t, `"x"`, doc.top["auth.token"])
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	require.NoError(t, err)
	return n
}

// frpDoc is a structural parse of frp's TOML: top-level dotted keys and the
// [[proxies]] array of tables (raw values).
type frpDoc struct {
	top     map[string]string
	proxies []map[string]string
}

func (d frpDoc) str(t *testing.T, key string) string {
	t.Helper()
	raw, ok := d.top[key]
	require.True(t, ok, "missing %s", key)
	s, err := strconv.Unquote(raw)
	require.NoError(t, err, "%s is not a string: %s", key, raw)
	return s
}

func (d frpDoc) num(t *testing.T, key string) int {
	t.Helper()
	raw, ok := d.top[key]
	require.True(t, ok, "missing %s", key)
	return atoi(t, raw)
}

var (
	keyRe    = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_.]*) = (.+)$`)
	scalarRe = regexp.MustCompile(`^("(?:[^"\\]|\\.)*"|-?[0-9]+|true|false)$`)
	inlineRe = regexp.MustCompile(`^\{ single = [0-9]+ \},$`)
)

// parseTOML checks the subset of TOML the renderer emits (comments,
// key = scalar, a multi-line array of { single = N } inline tables, and
// [[proxies]] tables) and rejects duplicate keys or keys after a table
// that belong to the top level.
func parseTOML(t *testing.T, data []byte) frpDoc {
	t.Helper()
	doc := frpDoc{top: map[string]string{}}
	cur := doc.top
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case line == "[[proxies]]":
			doc.proxies = append(doc.proxies, map[string]string{})
			cur = doc.proxies[len(doc.proxies)-1]
		case keyRe.MatchString(line):
			m := keyRe.FindStringSubmatch(line)
			key, val := m[1], m[2]
			if val == "[" {
				var items []string
				for i++; i < len(lines) && lines[i] != "]"; i++ {
					item := strings.TrimSpace(lines[i])
					require.Regexp(t, inlineRe, item, "line %d", i+1)
					items = append(items, strings.TrimSuffix(item, ","))
				}
				require.Less(t, i, len(lines), "unterminated array %s", key)
				val = "[" + strings.Join(items, ", ") + "]"
			} else {
				require.Regexp(t, scalarRe, val, "line %d: bad value", i+1)
			}
			_, dup := cur[key]
			require.False(t, dup, "line %d: duplicate key %s", i+1, key)
			cur[key] = val
		default:
			t.Fatalf("line %d: not TOML: %q", i+1, line)
		}
	}
	return doc
}
