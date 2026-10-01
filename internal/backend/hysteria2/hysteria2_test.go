package hysteria2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

const pin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func transport(t *testing.T) backend.Transport {
	t.Helper()
	tr := New().Transports()
	require.Len(t, tr, 1)
	return tr[0]
}

// fixture is the fixed RenderInput of the golden tests.
func fixture(t *testing.T) backend.RenderInput {
	t.Helper()
	dir := "/etc/deyroute/backends/hysteria2/main/de-1/udp"
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
		Transport:   transport(t),
		ControlPort: 30001,
		Secrets: backend.Secrets{
			Token:      "tok-test",
			CAFile:     dir + "/ca.crt",
			ServerName: "5.6.7.8",
			Keys: map[string]string{
				KeyObfsPassword:   "b2Jmcy10ZXN0LXBhc3N3b3JkLWZpeGVkLXZhbHVlLTAx",
				KeyNodeCert:       "-----BEGIN CERTIFICATE-----\nTEST\n-----END CERTIFICATE-----\n",
				KeyNodeKey:        "-----BEGIN PRIVATE KEY-----\nTEST\n-----END PRIVATE KEY-----\n",
				KeyNodeCertSHA256: pin,
			},
		},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/hysteria2/app%2Fv2.12.3/hysteria",
			BinDir:     "/var/lib/deyroute/bin/hysteria2/app%2Fv2.12.3",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// udpFixture adds UDP port maps and a domain target.
func udpFixture(t *testing.T) backend.RenderInput {
	in := fixture(t)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"},
		config.PortMap{Listen: 5353, Proto: "udp", Target: "127.0.0.1:53"},
		config.PortMap{Listen: 8080, Proto: "tcp", Target: "panel.internal:80"},
	)
	return in
}

// hopFixture enables port hopping and custom bandwidth.
func hopFixture(t *testing.T) backend.RenderInput {
	in := fixture(t)
	in.Tunnel.Advanced = &config.Advanced{HysteriaPortHopping: true, HysteriaUpMbps: 30, HysteriaDownMbps: 250}
	return in
}

func serialize(t *testing.T, side backend.Side, r backend.Rendered) []byte {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "# hysteria2/udp %s side\n", side)
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
	canary := fixture(t)
	canary.Canary = true
	canary.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:39002"}}
	cases := []struct {
		golden string
		in     backend.RenderInput
	}{
		{"udp", fixture(t)},
		{"udp_mixed", udpFixture(t)},
		{"udp_hopping", hopFixture(t)},
		{"udp_canary", canary},
	}
	for _, c := range cases {
		for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
			t.Run(c.golden+"."+side.String(), func(t *testing.T) {
				require.NoError(t, b.Validate(c.in))
				r, err := b.Render(c.in, side)
				require.NoError(t, err)
				r2, err := b.Render(c.in, side)
				require.NoError(t, err)
				require.Equal(t, r, r2, "render must be deterministic")
				checkGolden(t, c.golden+"."+side.String()+".golden", serialize(t, side, r))
			})
		}
	}
}

// serverYAML mirrors the server config keys of app/cmd/server.go.
type serverYAML struct {
	Listen string `yaml:"listen"`
	TLS    struct {
		Cert     string `yaml:"cert"`
		Key      string `yaml:"key"`
		SNIGuard string `yaml:"sniGuard"`
	} `yaml:"tls"`
	Auth struct {
		Type     string `yaml:"type"`
		Password string `yaml:"password"`
	} `yaml:"auth"`
	Obfs struct {
		Type       string `yaml:"type"`
		Salamander struct {
			Password string `yaml:"password"`
		} `yaml:"salamander"`
	} `yaml:"obfs"`
	DisableUDP bool `yaml:"disableUDP"`
	SpeedTest  bool `yaml:"speedTest"`
	ACL        struct {
		Inline []string `yaml:"inline"`
	} `yaml:"acl"`
	Masquerade struct {
		Type string `yaml:"type"`
	} `yaml:"masquerade"`
}

// clientYAML mirrors the client config keys of app/cmd/client.go.
type clientYAML struct {
	Server string `yaml:"server"`
	Auth   string `yaml:"auth"`
	Obfs   struct {
		Type       string `yaml:"type"`
		Salamander struct {
			Password string `yaml:"password"`
		} `yaml:"salamander"`
	} `yaml:"obfs"`
	TLS struct {
		SNI       string `yaml:"sni"`
		Insecure  bool   `yaml:"insecure"`
		PinSHA256 string `yaml:"pinSHA256"`
	} `yaml:"tls"`
	Bandwidth struct {
		Up   string `yaml:"up"`
		Down string `yaml:"down"`
	} `yaml:"bandwidth"`
	FastOpen      bool `yaml:"fastOpen"`
	TCPForwarding []struct {
		Listen string `yaml:"listen"`
		Remote string `yaml:"remote"`
	} `yaml:"tcpForwarding"`
	UDPForwarding []struct {
		Listen string `yaml:"listen"`
		Remote string `yaml:"remote"`
	} `yaml:"udpForwarding"`
}

func strict(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	require.NoError(t, dec.Decode(v), "rendered YAML must only use known keys:\n%s", data)
}

func TestServerConfig(t *testing.T) {
	in := udpFixture(t)
	r, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	var s serverYAML
	strict(t, r.Files[ServerFile], &s)
	require.Equal(t, ":30001", s.Listen)
	// The node serves its own certificate; the tunnel key is not used.
	require.Equal(t, in.Paths.ConfigDir+"/node-cert.pem", s.TLS.Cert)
	require.Equal(t, in.Paths.ConfigDir+"/node-key.pem", s.TLS.Key)
	require.Equal(t, "disable", s.TLS.SNIGuard)
	require.Equal(t, "password", s.Auth.Type)
	require.Equal(t, "tok-test", s.Auth.Password)
	require.Equal(t, "salamander", s.Obfs.Type)
	require.Equal(t, in.Secrets.Keys[KeyObfsPassword], s.Obfs.Salamander.Password)
	require.False(t, s.DisableUDP, "UDP port maps need UDP relaying")
	require.Equal(t, "404", s.Masquerade.Type)
	require.Equal(t, []backend.PortUse{{Port: 30001, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"}}, r.Binds)
	require.Empty(t, r.NAT)
	require.Equal(t, []string{in.Paths.Binary, "server", "-c", in.Paths.ConfigDir + "/server.yaml", "--disable-update-check"}, r.Unit.ExecStart)
	require.Equal(t, in.Paths.ConfigDir, r.Unit.WorkingDirectory)

	r, err = New().Render(fixture(t), backend.SideNode)
	require.NoError(t, err)
	strict(t, r.Files[ServerFile], &s)
	require.True(t, s.DisableUDP, "TCP-only tunnels disable UDP relaying")
}

func TestClientConfig(t *testing.T) {
	in := udpFixture(t)
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	var c clientYAML
	strict(t, r.Files[ClientFile], &c)
	require.Equal(t, "1.2.3.4:30001", c.Server)
	require.Equal(t, "tok-test", c.Auth)
	require.Equal(t, "salamander", c.Obfs.Type)
	require.Equal(t, in.Secrets.Keys[KeyObfsPassword], c.Obfs.Salamander.Password)
	require.Equal(t, "5.6.7.8", c.TLS.SNI)
	require.True(t, c.TLS.Insecure)
	require.Equal(t, pin, c.TLS.PinSHA256)
	require.Equal(t, "100 mbps", c.Bandwidth.Up)
	require.Equal(t, "100 mbps", c.Bandwidth.Down)
	require.True(t, c.FastOpen)
	require.Len(t, c.TCPForwarding, 3)
	require.Equal(t, "0.0.0.0:443", c.TCPForwarding[0].Listen)
	require.Equal(t, "127.0.0.1:443", c.TCPForwarding[0].Remote)
	require.Equal(t, "0.0.0.0:8080", c.TCPForwarding[2].Listen)
	require.Equal(t, "panel.internal:80", c.TCPForwarding[2].Remote)
	require.Len(t, c.UDPForwarding, 2)
	require.Equal(t, "0.0.0.0:5353", c.UDPForwarding[1].Listen)
	require.Equal(t, "127.0.0.1:53", c.UDPForwarding[1].Remote)
	require.Len(t, r.Binds, 5)
	for i, pm := range in.Tunnel.Ports {
		require.Equal(t, backend.PortUse{Port: pm.Listen, Proto: pm.Proto, Addr: "0.0.0.0", Purpose: "user"}, r.Binds[i])
	}
	require.Equal(t, "client", r.Unit.ExecStart[1])
	require.Equal(t, in.Paths.ConfigDir+"/client.yaml", r.Unit.ExecStart[3])

	// TCP-only: no udpForwarding key at all; no SNI when unknown.
	in = fixture(t)
	in.Secrets.ServerName = ""
	in.ListenAddr = "::"
	r, err = New().Render(in, backend.SideHub)
	require.NoError(t, err)
	c = clientYAML{}
	strict(t, r.Files[ClientFile], &c)
	require.Empty(t, c.UDPForwarding)
	require.Empty(t, c.TLS.SNI)
	require.Equal(t, "[::]:443", c.TCPForwarding[0].Listen)
	require.NotContains(t, string(r.Files[ClientFile]), "udpForwarding")
}

func TestPortHopping(t *testing.T) {
	in := hopFixture(t)
	node, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30001}}, node.NAT)
	hub, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Empty(t, hub.NAT)
	var c clientYAML
	strict(t, hub.Files[ClientFile], &c)
	require.Equal(t, "1.2.3.4:20000-20999", c.Server)
	require.Equal(t, "30 mbps", c.Bandwidth.Up)
	require.Equal(t, "250 mbps", c.Bandwidth.Down)

	in.Node.PublicIP = "2001:db8::7"
	hub, err = New().Render(in, backend.SideHub)
	require.NoError(t, err)
	strict(t, hub.Files[ClientFile], &c)
	require.Equal(t, "[2001:db8::7]:20000-20999", c.Server)
}

// aclLine is the rule syntax of extras/outbounds/acl/parse.go (v2.12.3).
var aclLine = regexp.MustCompile(`^(\w+)\s*\(([^,]+)(?:,([^,]+))?(?:,([^,]+))?\)$`)

// aclDecide evaluates rules the way hysteria's ACL engine does for the
// matchers the renderer uses (all, IP, exact domain; proto/port).
func aclDecide(t *testing.T, rules []string, host string, proto string, port int) string {
	t.Helper()
	ob, _ := aclDecideResolved(t, rules, host, nil, proto, port)
	return ob
}

// aclDecideResolved models extras/outbounds (app/v2.12.3): the system
// resolver in front of the ACL fills the first IPv4 and IPv6 of a host name
// (resolved), IP matchers compare against those (acl/matchers.go ipMatcher),
// domain matchers against the name, and a matching rule's hijack address
// replaces the destination. It returns the outbound and the IPs the direct
// outbound would dial (both families in auto mode = happy eyeballs).
func aclDecideResolved(t *testing.T, rules []string, host string, resolved []string, proto string, port int) (string, []string) {
	t.Helper()
	var v4, v6 net.IP
	if ip := net.ParseIP(host); ip != nil {
		resolved = []string{host}
	}
	for _, s := range resolved {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		if ip.To4() != nil && v4 == nil {
			v4 = ip
		} else if ip.To4() == nil && v6 == nil {
			v6 = ip
		}
	}
	for _, r := range rules {
		m := aclLine.FindStringSubmatch(r)
		require.NotNil(t, m, "rule %q must parse", r)
		addr := strings.ToLower(strings.TrimSpace(m[2]))
		pp := strings.TrimSpace(m[3])
		hijack := strings.TrimSpace(m[4])
		hostOK := addr == "all" || addr == "*"
		if !hostOK {
			if ip := net.ParseIP(addr); ip != nil {
				hostOK = ip.Equal(v4) || ip.Equal(v6)
			} else {
				hostOK = addr == strings.ToLower(host)
			}
		}
		if !hostOK {
			continue
		}
		if pp != "" {
			parts := strings.SplitN(pp, "/", 2)
			require.Len(t, parts, 2)
			if parts[0] != proto {
				continue
			}
			if parts[1] != strconv.Itoa(port) {
				continue
			}
		}
		if hijack != "" {
			hip := net.ParseIP(hijack)
			require.NotNil(t, hip, "hijack address %q must be an IP", hijack)
			return m[1], []string{hip.String()}
		}
		var dial []string
		for _, ip := range []net.IP{v4, v6} {
			if ip != nil {
				dial = append(dial, ip.String())
			}
		}
		return m[1], dial
	}
	return "default", nil
}

// TestACLHostNameCannotEscape: a host name whose A record is an allowed
// target IP but whose AAAA record points elsewhere must only ever be dialled
// at the allowed IP (the hijack address), never at the AAAA address.
func TestACLHostNameCannotEscape(t *testing.T) {
	r, err := New().Render(udpFixture(t), backend.SideNode)
	require.NoError(t, err)
	var s serverYAML
	strict(t, r.Files[ServerFile], &s)
	ob, dial := aclDecideResolved(t, s.ACL.Inline, "rebind.example", []string{"127.0.0.1", "2001:db8::66"}, "tcp", 443)
	require.Equal(t, "direct", ob)
	require.Equal(t, []string{"127.0.0.1"}, dial, "must not dial the AAAA address")

	// IPv6 targets are pinned the same way.
	in := fixture(t)
	in.Tunnel.Ports = []config.PortMap{{Listen: 443, Proto: "tcp", Target: "[::1]:443"}}
	r, err = New().Render(in, backend.SideNode)
	require.NoError(t, err)
	strict(t, r.Files[ServerFile], &s)
	require.Equal(t, []string{"direct(::1, tcp/443, ::1)", "reject(all)"}, s.ACL.Inline)
	ob, dial = aclDecideResolved(t, s.ACL.Inline, "rebind6.example", []string{"203.0.113.9", "::1"}, "tcp", 443)
	require.Equal(t, "direct", ob)
	require.Equal(t, []string{"::1"}, dial)
}

// TestServerNotOpenProxy covers spec section 11 / scenario S17 for
// hysteria2: the ACL passes only the tunnel's targets.
func TestServerNotOpenProxy(t *testing.T) {
	r, err := New().Render(udpFixture(t), backend.SideNode)
	require.NoError(t, err)
	var s serverYAML
	strict(t, r.Files[ServerFile], &s)
	rules := s.ACL.Inline
	require.Equal(t, "reject(all)", rules[len(rules)-1])
	require.Equal(t, []string{
		"direct(127.0.0.1, tcp/2053, 127.0.0.1)",
		"direct(127.0.0.1, tcp/443, 127.0.0.1)",
		"direct(127.0.0.1, udp/27015, 127.0.0.1)",
		"direct(127.0.0.1, udp/53, 127.0.0.1)",
		"direct(panel.internal, tcp/80)",
		"reject(all)",
	}, rules)

	for _, a := range []struct {
		host, proto string
		port        int
	}{
		{"127.0.0.1", "tcp", 443}, {"127.0.0.1", "tcp", 2053}, {"127.0.0.1", "udp", 27015},
		{"127.0.0.1", "udp", 53}, {"panel.internal", "tcp", 80},
	} {
		require.Equal(t, "direct", aclDecide(t, rules, a.host, a.proto, a.port), "%v", a)
	}
	for _, d := range []struct {
		host, proto string
		port        int
	}{
		{"8.8.8.8", "udp", 53}, {"8.8.8.8", "tcp", 53}, {"127.0.0.1", "tcp", 22},
		{"127.0.0.1", "udp", 443}, {"1.1.1.1", "tcp", 443}, {"panel.internal", "tcp", 22}, {"evil.example", "tcp", 80},
	} {
		require.Equal(t, "reject", aclDecide(t, rules, d.host, d.proto, d.port), "%v", d)
	}
}

func TestGenerateKeys(t *testing.T) {
	k, err := New().GenerateKeys(transport(t))
	require.NoError(t, err)
	require.Len(t, k, 4)
	raw, err := base64.RawURLEncoding.DecodeString(k[KeyObfsPassword])
	require.NoError(t, err)
	require.Len(t, raw, 32)
	// The node certificate: self-signed, its key matches, the pin is its hash.
	pair, err := tls.X509KeyPair([]byte(k[KeyNodeCert]), []byte(k[KeyNodeKey]))
	require.NoError(t, err)
	sum := sha256.Sum256(pair.Certificate[0])
	require.Equal(t, hex.EncodeToString(sum[:]), k[KeyNodeCertSHA256])
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)
	require.NoError(t, cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature), "self-signed")
	require.True(t, cert.NotAfter.After(time.Now().AddDate(19, 0, 0)))
	k2, err := New().GenerateKeys(transport(t))
	require.NoError(t, err)
	require.NotEqual(t, k, k2)

	_, err = (&Backend{rand: errReader{}}).GenerateKeys(transport(t))
	require.True(t, deyerr.HasCode(err, deyerr.B009), "%v", err)
	_, err = (&Backend{}).GenerateKeys(transport(t))
	require.NoError(t, err)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(in *backend.RenderInput)
		code   deyerr.Code
		reason string
	}{
		{"wrong transport", func(in *backend.RenderInput) { in.Transport.Name = "quic" }, deyerr.B006, "not a hysteria2"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"bad ctl", func(in *backend.RenderInput) { in.ControlPort = 70000 }, deyerr.B006, "control port"},
		{"relative binary", func(in *backend.RenderInput) { in.Paths.Binary = "hysteria" }, deyerr.B006, "binary"},
		{"relative dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "x" }, deyerr.B006, "config directory"},
		{"no token", func(in *backend.RenderInput) { in.Secrets.Token = "" }, deyerr.B006, "token"},
		{"no cert", func(in *backend.RenderInput) { delete(in.Secrets.Keys, KeyNodeKey) }, deyerr.B006, "certificate or key"},
		{"no pin", func(in *backend.RenderInput) { delete(in.Secrets.Keys, KeyNodeCertSHA256) }, deyerr.B006, "sha256"},
		{"bad pin", func(in *backend.RenderInput) { in.Secrets.Keys[KeyNodeCertSHA256] = strings.Repeat("z", 64) }, deyerr.B006, "sha256"},
		{"no obfs", func(in *backend.RenderInput) { in.Secrets.Keys = nil }, deyerr.B006, "obfs_password"},
		{"no node ip", func(in *backend.RenderInput) { in.Node.PublicIP = "" }, deyerr.B006, "node public IP"},
		{"bandwidth", func(in *backend.RenderInput) { in.Tunnel.Advanced = &config.Advanced{HysteriaUpMbps: -1} }, deyerr.B006, "mbps"},
		{"bandwidth high", func(in *backend.RenderInput) { in.Tunnel.Advanced = &config.Advanced{HysteriaDownMbps: maxMbps + 1} }, deyerr.B006, "mbps"},
		{"hop overlaps ctl", func(in *backend.RenderInput) {
			in.Tunnel.Advanced = &config.Advanced{HysteriaPortHopping: true}
			in.ControlPort = 20500
		}, deyerr.B006, "hopping"},
		{"sctp", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "sctp" }, deyerr.B010, ""},
		{"listen range", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 0 }, deyerr.B006, "out of range"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Listen = 443 }, deyerr.B006, "twice"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "a,b:1" }, deyerr.B006, "invalid target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t)
			c.mutate(&in)
			err := New().Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "want %s, got %v", c.code, err)
			_, rerr := New().Render(in, backend.SideNode)
			require.Error(t, rerr)
			var de *deyerr.Error
			require.True(t, errors.As(err, &de))
			if c.reason != "" {
				require.Contains(t, fmt.Sprint(de.Params["reason"]), c.reason)
			}
		})
	}
	// Defaults: empty proto/target, node given as a domain.
	in := fixture(t)
	in.Tunnel.Ports = []config.PortMap{{Listen: 443}}
	in.Node.PublicIP = "node.example.net"
	require.NoError(t, New().Validate(in))
}

func TestCanary(t *testing.T) {
	in := fixture(t)
	in.Canary = true
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, []backend.PortUse{{Port: 443, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"}}, r.Binds)
	var c clientYAML
	strict(t, r.Files[ClientFile], &c)
	require.Len(t, c.TCPForwarding, 1)
	require.Equal(t, "127.0.0.1:443", c.TCPForwarding[0].Listen)
	require.True(t, isLoopback("localhost"))
	require.False(t, isLoopback("0.0.0.0"))
}

func TestMetadata(t *testing.T) {
	b := New()
	require.Equal(t, "hysteria2", b.Name())
	tr := transport(t)
	require.Equal(t, "hysteria2/udp", tr.ID())
	require.Equal(t, backend.Forward, tr.Direction)
	require.Equal(t, 3, tr.Stealth)
	require.True(t, tr.NeedsUDP)
	require.False(t, tr.NeedsTLS, "the tunnel TLS key never goes to the node")
	require.True(t, tr.Supports("tcp") && tr.Supports("udp"))
	_, err := b.Probe(context.Background(), fixture(t))
	require.ErrorIs(t, err, backend.ErrNoProbe)
	m := b.Manifest()
	require.Equal(t, "app/v2.12.3", m.Version)
	require.Equal(t, "hysteria", m.Binary())
	var _ backend.KeyGenerator = b
	require.False(t, validDomain(".x"))
	require.False(t, validDomain("a..b"))
	require.False(t, validDomain("-a.b"))
	require.True(t, validDomain("a.b."))
}
