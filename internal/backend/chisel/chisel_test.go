package chisel

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testKey is a fixed ECDSA P-256 host key for the golden files.
const testKey = `-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIB3OFStAEVgi2qvSA9G3OgXe/GdBr0pBkkwMpjwgltjwoAoGCCqGSM49
AwEHoUQDQgAE5Fih3MsaljqcIU8vu4IGTPH555KjijxRr/9yLMJjKn+c6reGahip
zsgM/+HvIVFMQZZcAadSXnowrPo0b4JiXA==
-----END EC PRIVATE KEY-----
`

// testFingerprint is chisel's fingerprint of testKey.
const testFingerprint = "cGR7ti/WZmyIJEU78bfdpqgYPLRXS928/t/e6gecBrc="

func fixture(t *testing.T) backend.RenderInput {
	t.Helper()
	dir := "/etc/deyroute/backends/chisel/main/de-1/wss"
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
			Keys:        map[string]string{KeyServerKey: testKey, KeyFingerprint: testFingerprint},
		},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/chisel/v1.12.1/chisel",
			BinDir:     "/var/lib/deyroute/bin/chisel/v1.12.1",
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
	fmt.Fprintf(&b, "# chisel/wss %s side\n", side)
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
		{"wss", fixture(t)},
		{"wss_mixed", mixedFixture(t)},
		{"wss_canary", canaryFixture(t)},
		{"wss_ipv6", ipv6},
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
	require.Equal(t, "chisel/wss", tr.ID())
	require.Equal(t, backend.Reverse, tr.Direction)
	require.Equal(t, []string{"tcp", "udp"}, tr.Protos)
	require.False(t, tr.NeedsUDP)
	require.True(t, tr.NeedsTLS)
	require.Equal(t, 3, tr.Stealth)
	require.True(t, tr.Optional)
	require.False(t, tr.ClientIPPreserved)

	b, _, err := backend.Lookup("chisel/wss")
	require.NoError(t, err)
	_, ok := b.(backend.KeyGenerator)
	require.True(t, ok)

	m := New().Manifest()
	require.Equal(t, "v1.12.1", m.Version)
	require.Equal(t, "jpillora/chisel", m.Repo)
	require.Equal(t, "gz", m.Archive)
	require.Equal(t, []string{"chisel"}, m.Binaries)
	require.Contains(t, m.URLs["amd64"], "/v1.12.1/chisel_1.12.1_linux_amd64.gz")
	require.Contains(t, m.URLs["arm64"], "/v1.12.1/chisel_1.12.1_linux_arm64.gz")
	_, err = New().Probe(context.Background(), fixture(t))
	require.ErrorIs(t, err, backend.ErrNoProbe)
}

func TestGenerateKeys(t *testing.T) {
	b := New()
	keys, err := b.GenerateKeys(b.Transports()[0])
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.True(t, strings.HasPrefix(keys[KeyServerKey], "-----BEGIN EC PRIVATE KEY-----\n"))
	require.Len(t, keys[KeyFingerprint], 44)
	require.NoError(t, checkServerKey(keys[KeyServerKey], keys[KeyFingerprint]))
	in := fixture(t)
	in.Secrets.Keys = keys
	require.NoError(t, b.Validate(in))

	other, err := b.GenerateKeys(backend.Transport{})
	require.NoError(t, err)
	require.NotEqual(t, keys[KeyServerKey], other[KeyServerKey])

	require.NoError(t, checkServerKey(testKey, testFingerprint))

	b.rand = io.LimitReader(strings.NewReader("short"), 5)
	_, err = b.GenerateKeys(backend.Transport{})
	require.True(t, deyerr.HasCode(err, deyerr.B009), "%v", err)
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *backend.RenderInput)
		code   deyerr.Code
		reason string
	}{
		{"foreign backend", func(in *backend.RenderInput) { in.Transport.Backend = "gost" }, deyerr.B006, "not a chisel"},
		{"unknown transport", func(in *backend.RenderInput) { in.Transport.Name = "ws" }, deyerr.B006, "not a chisel"},
		{"ctl port", func(in *backend.RenderInput) { in.ControlPort = 0 }, deyerr.B006, "control port"},
		{"token", func(in *backend.RenderInput) { in.Secrets.Token = "" }, deyerr.B006, "token"},
		{"hub ip", func(in *backend.RenderInput) { in.Hub.PublicIP = "" }, deyerr.B006, "hub public IP"},
		{"hub ip url", func(in *backend.RenderInput) { in.Hub.PublicIP = "a/b" }, deyerr.B006, "hub public IP"},
		{"cert", func(in *backend.RenderInput) { in.Secrets.TLSCertFile = "" }, deyerr.B006, "TLS certificate"},
		{"ca", func(in *backend.RenderInput) { in.Secrets.CAFile = "ca.crt" }, deyerr.B006, "CA certificate"},
		{"binary", func(in *backend.RenderInput) { in.Paths.Binary = "chisel" }, deyerr.B006, "not installed"},
		{"config dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "" }, deyerr.B006, "config directory"},
		{"no key", func(in *backend.RenderInput) { in.Secrets.Keys = nil }, deyerr.B006, "server_key"},
		{"bad key", func(in *backend.RenderInput) {
			in.Secrets.Keys[KeyServerKey] = "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n"
		}, deyerr.B006, "ECDSA P-256"},
		{"wrong fingerprint", func(in *backend.RenderInput) { in.Secrets.Keys[KeyFingerprint] = "x" }, deyerr.B006, "does not match"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"bad proto", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "sctp" }, deyerr.B010, ""},
		{"listen", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = -5 }, deyerr.B006, "out of range"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1] = in.Tunnel.Ports[0] }, deyerr.B006, "listed twice"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "127.0.0.1" }, deyerr.B006, "invalid target"},
		{"socks target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "socks:1080" }, deyerr.B006, "invalid target"},
		{"slash target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "a/udp:443" }, deyerr.B006, "invalid target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t)
			in.Secrets.Keys = map[string]string{KeyServerKey: testKey, KeyFingerprint: testFingerprint}
			c.mutate(&in)
			err := New().Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "%v", err)
			if c.reason != "" {
				require.Contains(t, deyerr.As(err).Message(), c.reason)
			}
			_, err = New().Render(in, backend.SideHub)
			require.Error(t, err)
		})
	}
}

// chiselUserAddr mirrors chisel v1.12.1 settings.DecodeRemote + UserAddr
// for the reverse remotes this package renders.
func chiselUserAddr(t *testing.T, r string) (userAddr, target, proto string) {
	t.Helper()
	require.True(t, strings.HasPrefix(r, "R:"), r)
	s := strings.TrimPrefix(r, "R:")
	proto = "tcp"
	if strings.HasSuffix(s, "/udp") {
		proto, s = "udp", strings.TrimSuffix(s, "/udp")
	}
	parts := regexp.MustCompile(`(\[[^\[\]]+\]|[^\[\]:]+):?`).FindAllStringSubmatch(s, -1)
	require.Len(t, parts, 4, r)
	return "R:" + parts[0][1] + ":" + parts[1][1], parts[2][1] + ":" + parts[3][1], proto
}

func TestParsedAndAllowList(t *testing.T) {
	in := mixedFixture(t)
	hub, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	node, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	pw := Password("tok-test")
	require.Regexp(t, `^[0-9a-f]{32}$`, pw)
	require.NotEqual(t, pw, Password("other"))

	var users map[string][]string
	require.NoError(t, json.Unmarshal(hub.Files[UsersFile], &users))
	require.Equal(t, map[string][]string{"dey:" + pw: {`^R:0\.0\.0\.0:443$`, `^R:0\.0\.0\.0:2053$`, `^R:0\.0\.0\.0:27015$`}}, users)
	require.Equal(t, testKey, string(hub.Files[KeyFile]))
	require.NotContains(t, string(hub.Files[UsersFile]), "tok-test", "the token itself never leaves the secrets")

	dir := in.Paths.ConfigDir
	require.Equal(t, []string{
		in.Paths.Binary, "server", "--host", "0.0.0.0", "--port", "30001", "--reverse",
		"--keyfile", dir + "/server.key", "--authfile", dir + "/users.json",
		"--tls-key", in.Secrets.TLSKeyFile, "--tls-cert", in.Secrets.TLSCertFile, "--keepalive", "25s",
	}, hub.Unit.ExecStart)
	require.Empty(t, hub.Unit.Env)
	require.Equal(t, dir, hub.Unit.WorkingDirectory)
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
	}, hub.Binds)

	remotes := []string{
		"R:0.0.0.0:443:127.0.0.1:443", "R:0.0.0.0:2053:127.0.0.1:2053",
		"R:0.0.0.0:443:127.0.0.1:443/udp", "R:0.0.0.0:27015:10.0.0.5:27016/udp",
	}
	want := append([]string{
		in.Paths.Binary, "client", "--fingerprint", testFingerprint, "--tls-ca", in.Secrets.CAFile,
		"--sni", "5.6.7.8", "--keepalive", "25s", "--max-retry-interval", "10s", "https://5.6.7.8:30001",
	}, remotes...)
	require.Equal(t, want, node.Unit.ExecStart)
	require.Equal(t, map[string]string{"AUTH": "dey:" + pw}, node.Unit.Env)
	require.NotContains(t, strings.Join(node.Unit.ExecStart, " "), pw, "credentials are not in argv")
	require.Empty(t, node.Binds)
	require.Empty(t, node.Files)

	// Every remote the node requests is granted by the authfile, and the
	// node only dials configured targets (spec section 11).
	targets := map[string]bool{}
	for _, p := range in.Tunnel.Ports {
		targets[p.Proto+" "+p.Target] = true
	}
	var pats []*regexp.Regexp
	for _, p := range users["dey:"+pw] {
		pats = append(pats, regexp.MustCompile(p))
	}
	for _, r := range remotes {
		ua, target, proto := chiselUserAddr(t, r)
		ok := false
		for _, re := range pats {
			ok = ok || re.MatchString(ua)
		}
		require.True(t, ok, "%s not granted", ua)
		require.True(t, targets[proto+" "+target], "%s dials an unconfigured target", r)
	}
	// Nothing else is granted: other ports, forward remotes, socks.
	for _, other := range []string{"R:0.0.0.0:22", "R:0.0.0.0:4430", "R:127.0.0.1:443", "R:socks", "socks", "127.0.0.1:22", "R:0.0.0.0:4431"} {
		for _, re := range pats {
			require.False(t, re.MatchString(other), "%s must not be granted", other)
		}
	}
}

func TestCanaryAndIPv6(t *testing.T) {
	c := canaryFixture(t)
	c.ListenAddr = "::" // forced to loopback
	hub, err := New().Render(c, backend.SideHub)
	require.NoError(t, err)
	var users map[string][]string
	require.NoError(t, json.Unmarshal(hub.Files[UsersFile], &users))
	require.Equal(t, []string{`^R:127\.0\.0\.1:39001$`}, users["dey:"+Password("tok-test")])
	require.Equal(t, backend.PortUse{Port: 39001, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"}, hub.Binds[1])
	node, err := New().Render(c, backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, "R:127.0.0.1:39001:127.0.0.1:39002", node.Unit.ExecStart[len(node.Unit.ExecStart)-1])

	in := fixture(t)
	in.ListenAddr = "::"
	in.Hub.PublicIP = "2001:db8::1"
	in.Secrets.ServerName = ""
	in.Tunnel.Ports = []config.PortMap{{Listen: 8443, Target: "[::1]:9444"}}
	hub, err = New().Render(in, backend.SideHub)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(hub.Files[UsersFile], &users))
	require.Equal(t, []string{`^R:\[::\]:8443$`}, users["dey:"+Password("tok-test")])
	require.Equal(t, backend.PortUse{Port: 8443, Proto: "tcp", Addr: "::", Purpose: "user"}, hub.Binds[1])
	node, err = New().Render(in, backend.SideNode)
	require.NoError(t, err)
	argv := node.Unit.ExecStart
	require.NotContains(t, argv, "--sni")
	require.Equal(t, "https://[2001:db8::1]:30001", argv[len(argv)-2])
	require.Equal(t, "R:[::]:8443:[::1]:9444", argv[len(argv)-1])
	ua, target, _ := chiselUserAddr(t, argv[len(argv)-1])
	require.Regexp(t, users["dey:"+Password("tok-test")][0], ua)
	require.Equal(t, "[::1]:9444", target)
}
