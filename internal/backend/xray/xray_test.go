package xray

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
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

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixedKeys are deterministic keys generated from a constant entropy stream.
func fixedKeys(t *testing.T) map[string]string {
	t.Helper()
	b := &Backend{rand: bytes.NewReader(bytes.Repeat([]byte{0x42, 0x17, 0x99, 0x03}, 32))}
	k, err := b.GenerateKeys(backend.Transport{})
	require.NoError(t, err)
	return k
}

func transport(t *testing.T) backend.Transport {
	t.Helper()
	tr := New().Transports()
	require.Len(t, tr, 1)
	return tr[0]
}

// fixture is the fixed RenderInput of the golden tests.
func fixture(t *testing.T) backend.RenderInput {
	t.Helper()
	dir := "/etc/deyroute/backends/xray/main/de-1/reality"
	return backend.RenderInput{
		Tunnel: config.Tunnel{
			ID: "main", Name: "main", Enabled: true, Nodes: []string{"de-1"},
			Ports: []config.PortMap{
				{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"},
				{Listen: 2053, Proto: "tcp", Target: "127.0.0.1:2053"},
			},
		},
		Node:        config.Node{ID: "de-1", Name: "de-1", PublicIP: "1.2.3.4"},
		Hub:         config.HubInfo{Name: "ir-1", PublicIP: "5.6.7.8", ControlPort: 44433, DecoySNIs: []string{"www.example.com"}},
		Transport:   transport(t),
		ControlPort: 30001,
		Secrets:     backend.Secrets{Token: "tok-test", Keys: fixedKeys(t)},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/xray/v26.3.27/xray",
			BinDir:     "/var/lib/deyroute/bin/xray/v26.3.27",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// udpFixture adds UDP port maps (one of them to UDP/443 so the hub needs the
// vision-udp443 flow) and a domain target.
func udpFixture(t *testing.T) backend.RenderInput {
	in := fixture(t)
	in.Decoy = "decoy.example.net"
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"},
		config.PortMap{Listen: 8080, Proto: "tcp", Target: "panel.internal:80"},
	)
	return in
}

func serialize(t *testing.T, side backend.Side, r backend.Rendered) []byte {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "# xray/reality %s side\n", side)
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
	canary.ListenAddr = "0.0.0.0" // forced to loopback for canary units
	canary.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:39002"}}
	cases := []struct {
		golden string
		in     backend.RenderInput
	}{
		{"reality", fixture(t)},
		{"reality_udp", udpFixture(t)},
		{"reality_canary", canary},
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

// parsed mirrors the parts of the rendered JSON the tests assert.
type parsed struct {
	Log struct {
		LogLevel string `json:"loglevel"`
	} `json:"log"`
	Inbounds []struct {
		Tag      string          `json:"tag"`
		Listen   string          `json:"listen"`
		Port     int             `json:"port"`
		Protocol string          `json:"protocol"`
		Settings json.RawMessage `json:"settings"`
		Stream   *struct {
			Network  string         `json:"network"`
			Security string         `json:"security"`
			Reality  map[string]any `json:"realitySettings"`
		} `json:"streamSettings"`
	} `json:"inbounds"`
	Outbounds []struct {
		Tag      string          `json:"tag"`
		Protocol string          `json:"protocol"`
		Settings json.RawMessage `json:"settings"`
		Stream   *struct {
			Network  string         `json:"network"`
			Security string         `json:"security"`
			Reality  map[string]any `json:"realitySettings"`
		} `json:"streamSettings"`
		Mux *struct {
			Enabled bool `json:"enabled"`
		} `json:"mux"`
	} `json:"outbounds"`
	Routing *struct {
		DomainStrategy string `json:"domainStrategy"`
		Rules          []struct {
			InboundTag  []string `json:"inboundTag"`
			IP          []string `json:"ip"`
			Domain      []string `json:"domain"`
			Port        string   `json:"port"`
			Network     string   `json:"network"`
			OutboundTag string   `json:"outboundTag"`
		} `json:"rules"`
	} `json:"routing"`
}

func render(t *testing.T, in backend.RenderInput, side backend.Side) (backend.Rendered, parsed) {
	t.Helper()
	r, err := New().Render(in, side)
	require.NoError(t, err)
	var p parsed
	require.NoError(t, json.Unmarshal(r.Files[ConfigFile], &p))
	return r, p
}

func TestHubConfig(t *testing.T) {
	in := udpFixture(t)
	r, p := render(t, in, backend.SideHub)
	require.Equal(t, "warning", p.Log.LogLevel)
	require.Len(t, p.Inbounds, 5)
	type doko struct {
		Address string `json:"address"`
		Port    int    `json:"port"`
		Network string `json:"network"`
	}
	for i, pm := range in.Tunnel.Ports {
		ib := p.Inbounds[i]
		require.Equal(t, "dokodemo-door", ib.Protocol)
		require.Equal(t, "in-"+pm.Proto+"-"+strconv.Itoa(pm.Listen), ib.Tag)
		require.Equal(t, "0.0.0.0", ib.Listen)
		require.Equal(t, pm.Listen, ib.Port)
		var d doko
		require.NoError(t, json.Unmarshal(ib.Settings, &d))
		host, port, ok := backend.SplitTarget(pm.Target)
		require.True(t, ok)
		require.Equal(t, doko{Address: host, Port: port, Network: pm.Proto}, d)
		require.Equal(t, backend.PortUse{Port: pm.Listen, Proto: pm.Proto, Addr: "0.0.0.0", Purpose: "user"}, r.Binds[i])
	}
	require.Len(t, r.Binds, 5, "hub binds only the user ports")

	require.Len(t, p.Outbounds, 1)
	ob := p.Outbounds[0]
	require.Equal(t, "vless", ob.Protocol)
	var vs struct {
		Vnext []struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
			Users   []struct {
				ID, Encryption, Flow string
			} `json:"users"`
		} `json:"vnext"`
	}
	require.NoError(t, json.Unmarshal(ob.Settings, &vs))
	require.Len(t, vs.Vnext, 1)
	require.Equal(t, "1.2.3.4", vs.Vnext[0].Address)
	require.Equal(t, 30001, vs.Vnext[0].Port)
	require.Len(t, vs.Vnext[0].Users, 1)
	u := vs.Vnext[0].Users[0]
	require.Equal(t, in.Secrets.Keys[KeyUUID], u.ID)
	require.Equal(t, "none", u.Encryption)
	require.Equal(t, "xtls-rprx-vision-udp443", u.Flow, "UDP/443 port map needs the udp443 flow")
	require.Equal(t, "raw", ob.Stream.Network)
	require.Equal(t, "reality", ob.Stream.Security)
	require.Equal(t, "decoy.example.net", ob.Stream.Reality["serverName"])
	require.Equal(t, "chrome", ob.Stream.Reality["fingerprint"])
	require.Equal(t, in.Secrets.Keys[KeyPublic], ob.Stream.Reality["password"])
	require.Equal(t, in.Secrets.Keys[KeyShortID], ob.Stream.Reality["shortId"])
	require.NotNil(t, ob.Mux)
	require.False(t, ob.Mux.Enabled)

	require.Equal(t, []string{"/var/lib/deyroute/bin/xray/v26.3.27/xray", "run", "-c",
		"/etc/deyroute/backends/xray/main/de-1/reality/config.json"}, r.Unit.ExecStart)
	require.Equal(t, in.Paths.ConfigDir, r.Unit.WorkingDirectory)

	// TCP-only tunnel: plain vision flow.
	_, p = render(t, fixture(t), backend.SideHub)
	require.NoError(t, json.Unmarshal(p.Outbounds[0].Settings, &vs))
	require.Equal(t, "xtls-rprx-vision", vs.Vnext[0].Users[0].Flow)
}

func TestNodeConfig(t *testing.T) {
	in := fixture(t)
	r, p := render(t, in, backend.SideNode)
	require.Equal(t, []backend.PortUse{{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"}}, r.Binds)
	require.Len(t, p.Inbounds, 1)
	ib := p.Inbounds[0]
	require.Equal(t, "vless", ib.Protocol)
	require.Equal(t, "0.0.0.0", ib.Listen)
	require.Equal(t, 30001, ib.Port)
	var vs struct {
		Clients    []map[string]any `json:"clients"`
		Decryption string           `json:"decryption"`
	}
	require.NoError(t, json.Unmarshal(ib.Settings, &vs))
	require.Equal(t, "none", vs.Decryption)
	require.Equal(t, []map[string]any{{"id": in.Secrets.Keys[KeyUUID], "flow": "xtls-rprx-vision"}}, vs.Clients,
		"inbound clients must not carry 'encryption'")
	re := ib.Stream.Reality
	require.Equal(t, "reality", ib.Stream.Security)
	require.Equal(t, "raw", ib.Stream.Network)
	require.Equal(t, "www.example.com:443", re["target"])
	require.Equal(t, []any{"www.example.com"}, re["serverNames"])
	require.Equal(t, in.Secrets.Keys[KeyPrivate], re["privateKey"])
	require.Equal(t, []any{in.Secrets.Keys[KeyShortID]}, re["shortIds"])
	require.Equal(t, false, re["show"])

	require.Len(t, p.Outbounds, 3)
	require.Equal(t, "blackhole", p.Outbounds[0].Protocol, "default outbound must be the blackhole")
	require.Equal(t, "block", p.Outbounds[0].Tag)
	for i, target := range []string{"127.0.0.1:443", "127.0.0.1:2053"} {
		ob := p.Outbounds[i+1]
		require.Equal(t, "freedom", ob.Protocol)
		require.Equal(t, "direct-tcp-"+target, ob.Tag)
		require.Equal(t, target, redirectOf(t, p, ob.Tag))
	}
}

// redirectOf returns settings.redirect of the freedom outbound tag.
func redirectOf(t *testing.T, p parsed, tag string) string {
	t.Helper()
	for _, ob := range p.Outbounds {
		if ob.Tag != tag {
			continue
		}
		require.Equal(t, "freedom", ob.Protocol, tag)
		var fs struct {
			Redirect string `json:"redirect"`
		}
		require.NoError(t, json.Unmarshal(ob.Settings, &fs))
		return fs.Redirect
	}
	t.Fatalf("outbound %q not found", tag)
	return ""
}

// deliver models where Xray v26.3.27 sends a packet or connection: routing
// picks the outbound from the session's first destination (host, port,
// network); a freedom outbound with "redirect" overrides the destination of
// every TCP dial and every UDP packet (XUDP Keep frames may carry their own
// per-packet address, perPacket). It returns "" when the traffic is dropped.
func deliver(t *testing.T, p parsed, host string, port int, network, perPacket string) string {
	t.Helper()
	tag := route(t, p, host, port, network)
	if tag == "block" {
		return ""
	}
	if r := redirectOf(t, p, tag); r != "" {
		return r
	}
	if perPacket != "" {
		return perPacket
	}
	return backend.HostPort(host, port)
}

// route is a minimal re-implementation of Xray's first-match field-rule
// routing for the fields the renderer uses.
func route(t *testing.T, p parsed, host string, port int, network string) string {
	t.Helper()
	require.NotNil(t, p.Routing)
	for _, r := range p.Routing.Rules {
		if r.Network != "" && !strings.Contains(","+r.Network+",", ","+network+",") {
			continue
		}
		if r.Port != "" && r.Port != strconv.Itoa(port) {
			continue
		}
		if len(r.IP) > 0 {
			ip := net.ParseIP(host)
			match := false
			for _, c := range r.IP {
				if !strings.Contains(c, "/") {
					if ip != nil && ip.Equal(net.ParseIP(c)) {
						match = true
					}
					continue
				}
				_, n, err := net.ParseCIDR(c)
				require.NoError(t, err)
				if ip != nil && n.Contains(ip) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		if len(r.Domain) > 0 {
			match := false
			for _, d := range r.Domain {
				if d == "full:"+host {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		return r.OutboundTag
	}
	return p.Outbounds[0].Tag
}

// TestNodeNotOpenProxy covers spec section 11 / scenario S17: only the
// tunnel's targets reach "direct"; 8.8.8.8:53 and every other destination
// is blocked.
func TestNodeNotOpenProxy(t *testing.T) {
	_, p := render(t, udpFixture(t), backend.SideNode)
	require.Equal(t, "AsIs", p.Routing.DomainStrategy)
	last := p.Routing.Rules[len(p.Routing.Rules)-1]
	require.Equal(t, "block", last.OutboundTag)
	require.Equal(t, "tcp,udp", last.Network)
	require.Empty(t, last.IP)
	require.Empty(t, last.Port)

	allowed := []struct {
		host    string
		port    int
		network string
	}{
		{"127.0.0.1", 443, "tcp"}, {"127.0.0.1", 2053, "tcp"}, {"127.0.0.1", 443, "udp"},
		{"127.0.0.1", 27015, "udp"}, {"panel.internal", 80, "tcp"},
	}
	for _, a := range allowed {
		tag := route(t, p, a.host, a.port, a.network)
		require.Equal(t, "direct-"+a.network+"-"+backend.HostPort(a.host, a.port), tag, "%v must be allowed", a)
		require.Equal(t, backend.HostPort(a.host, a.port), redirectOf(t, p, tag), "%v is pinned", a)
	}
	denied := []struct {
		host    string
		port    int
		network string
	}{
		{"8.8.8.8", 53, "udp"}, {"8.8.8.8", 53, "tcp"}, {"1.1.1.1", 443, "tcp"},
		{"127.0.0.1", 22, "tcp"}, {"127.0.0.1", 2053, "udp"}, {"127.0.0.1", 27015, "tcp"},
		{"panel.internal", 22, "tcp"}, {"evil.example", 443, "tcp"}, {"10.0.0.1", 443, "tcp"},
	}
	for _, d := range denied {
		require.Equal(t, "block", route(t, p, d.host, d.port, d.network), "%v must be blocked", d)
	}
	for _, r := range p.Routing.Rules[:len(p.Routing.Rules)-1] {
		require.True(t, strings.HasPrefix(r.OutboundTag, "direct-"), r.OutboundTag)
		require.Equal(t, []string{"vless-in"}, r.InboundTag)
		require.NotEmpty(t, r.Port, "allow rules are always port specific")
		require.True(t, len(r.IP)+len(r.Domain) == 1, "allow rules name exactly one host")
	}
}

// TestNodeXUDPCannotEscape: once a UDP session to an allowed target exists,
// XUDP frames that name another destination per packet (e.g. 8.8.8.8:53)
// must still be delivered only to the allowed target (freedom redirect).
func TestNodeXUDPCannotEscape(t *testing.T) {
	_, p := render(t, udpFixture(t), backend.SideNode)
	require.Equal(t, "127.0.0.1:27015", deliver(t, p, "127.0.0.1", 27015, "udp", "8.8.8.8:53"))
	require.Equal(t, "127.0.0.1:443", deliver(t, p, "127.0.0.1", 443, "udp", "1.1.1.1:443"))
	require.Equal(t, "", deliver(t, p, "8.8.8.8", 53, "udp", ""))
	// Every non-block outbound is a pinned freedom.
	for _, ob := range p.Outbounds[1:] {
		require.NotEmpty(t, redirectOf(t, p, ob.Tag), ob.Tag)
	}
}

func TestCanaryBindsLoopback(t *testing.T) {
	in := fixture(t)
	in.Canary = true
	r, p := render(t, in, backend.SideHub)
	require.Len(t, p.Inbounds, 1)
	require.Equal(t, "127.0.0.1", p.Inbounds[0].Listen)
	require.Equal(t, []backend.PortUse{{Port: 443, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"}}, r.Binds)

	in.Canary = false
	in.ListenAddr = "::"
	r, _ = render(t, in, backend.SideHub)
	require.Equal(t, "::", r.Binds[0].Addr)
}

func TestGenerateKeys(t *testing.T) {
	k, err := New().GenerateKeys(transport(t))
	require.NoError(t, err)
	require.Len(t, k, 4)
	priv, err := base64.RawURLEncoding.DecodeString(k[KeyPrivate])
	require.NoError(t, err)
	pub, err := base64.RawURLEncoding.DecodeString(k[KeyPublic])
	require.NoError(t, err)
	sk, err := ecdh.X25519().NewPrivateKey(priv)
	require.NoError(t, err)
	require.Equal(t, pub, sk.PublicKey().Bytes())
	require.Regexp(t, `^[0-9a-f]{8}$`, k[KeyShortID])
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`), k[KeyUUID])
	k2, err := New().GenerateKeys(transport(t))
	require.NoError(t, err)
	require.NotEqual(t, k[KeyPrivate], k2[KeyPrivate])

	// Entropy failures surface as DEY-B009 at every read.
	for _, n := range []int{0, 32, 36} {
		b := &Backend{rand: &shortReader{n: n}}
		_, err := b.GenerateKeys(transport(t))
		require.True(t, deyerr.HasCode(err, deyerr.B009), "n=%d: %v", n, err)
	}
	// A zero-value Backend falls back to crypto/rand.
	_, err = (&Backend{}).GenerateKeys(transport(t))
	require.NoError(t, err)
}

type shortReader struct{ n int }

func (r *shortReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, errors.New("no entropy")
	}
	if len(p) > r.n {
		p = p[:r.n]
	}
	for i := range p {
		p[i] = 7
	}
	r.n -= len(p)
	return len(p), nil
}

func TestValidate(t *testing.T) {
	other := fixedKeys(t)
	otherPub := ""
	{
		b := &Backend{rand: bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 40))}
		k, err := b.GenerateKeys(backend.Transport{})
		require.NoError(t, err)
		otherPub = k[KeyPublic]
	}
	cases := []struct {
		name   string
		mutate func(in *backend.RenderInput)
		code   deyerr.Code
		reason string
	}{
		{"bad hub decoy", func(in *backend.RenderInput) { in.Hub.DecoySNIs = []string{"bad_decoy!"} }, deyerr.B006, "domain name"},
		{"decoy is ip", func(in *backend.RenderInput) { in.Decoy = "9.9.9.9" }, deyerr.B006, "domain name"},
		{"missing keys", func(in *backend.RenderInput) { in.Secrets.Keys = nil }, deyerr.B006, "keys are missing"},
		{"bad private", func(in *backend.RenderInput) { in.Secrets.Keys = with(other, KeyPrivate, "!!") }, deyerr.B006, "reality_private"},
		{"bad public", func(in *backend.RenderInput) { in.Secrets.Keys = with(other, KeyPublic, "abc") }, deyerr.B006, "reality_public is not"},
		{"key mismatch", func(in *backend.RenderInput) { in.Secrets.Keys = with(other, KeyPublic, otherPub) }, deyerr.B006, "does not belong"},
		{"odd short id", func(in *backend.RenderInput) { in.Secrets.Keys = with(other, KeyShortID, "abc") }, deyerr.B006, "even number"},
		{"non-hex short id", func(in *backend.RenderInput) { in.Secrets.Keys = with(other, KeyShortID, "zz") }, deyerr.B006, "not hex"},
		{"bad uuid", func(in *backend.RenderInput) { in.Secrets.Keys = with(other, KeyUUID, "nope") }, deyerr.B006, "uuid"},
		{"bad uuid hex", func(in *backend.RenderInput) {
			in.Secrets.Keys = with(other, KeyUUID, "zzzzzzzz-zzzz-4zzz-8zzz-zzzzzzzzzzzz")
		}, deyerr.B006, "uuid"},
		{"no node ip", func(in *backend.RenderInput) { in.Node.PublicIP = "" }, deyerr.B006, "node public IP"},
		{"bad ctl port", func(in *backend.RenderInput) { in.ControlPort = 0 }, deyerr.B006, "control port"},
		{"wrong transport", func(in *backend.RenderInput) { in.Transport.Name = "tcp" }, deyerr.B006, "not an xray"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"sctp", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "sctp" }, deyerr.B010, ""},
		{"listen range", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 70000 }, deyerr.B006, "out of range"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Listen = 443 }, deyerr.B006, "twice"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "x y:1" }, deyerr.B006, "invalid target"},
		{"relative binary", func(in *backend.RenderInput) { in.Paths.Binary = "xray" }, deyerr.B006, "binary"},
		{"relative dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "cfg" }, deyerr.B006, "config directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture(t)
			c.mutate(&in)
			err := New().Validate(in)
			require.True(t, deyerr.HasCode(err, c.code), "want %s, got %v", c.code, err)
			_, rerr := New().Render(in, backend.SideHub)
			require.Error(t, rerr)
			var de *deyerr.Error
			require.True(t, errors.As(err, &de))
			if c.reason != "" {
				require.Contains(t, fmt.Sprint(de.Params["reason"]), c.reason)
			}
		})
	}
}

func TestValidateAccepts(t *testing.T) {
	in := fixture(t)
	in.Tunnel.Ports = []config.PortMap{{Listen: 443}} // default proto and target
	in.Decoy = "cdn.example.org:8443"
	require.NoError(t, New().Validate(in))
	_, p := render(t, in, backend.SideNode)
	require.Equal(t, "cdn.example.org:8443", p.Inbounds[0].Stream.Reality["target"])
	require.Equal(t, "127.0.0.1:443", deliver(t, p, "127.0.0.1", 443, "tcp", ""))

	in.Tunnel.Ports = []config.PortMap{{Listen: 5000, Proto: "tcp", Target: "[::1]:5000"}}
	_, p = render(t, in, backend.SideNode)
	require.Equal(t, "[::1]:5000", deliver(t, p, "::1", 5000, "tcp", ""))
}

func with(m map[string]string, k, v string) map[string]string {
	out := map[string]string{}
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func TestMetadata(t *testing.T) {
	b := New()
	require.Equal(t, "xray", b.Name())
	tr := transport(t)
	require.Equal(t, "xray/reality", tr.ID())
	require.Equal(t, backend.Forward, tr.Direction)
	require.Equal(t, 5, tr.Stealth)
	require.True(t, tr.Supports("tcp") && tr.Supports("udp"))
	require.False(t, tr.NeedsUDP)
	require.False(t, tr.NeedsTLS)
	require.False(t, tr.Optional)
	_, err := b.Probe(context.Background(), fixture(t))
	require.ErrorIs(t, err, backend.ErrNoProbe)
	m := b.Manifest()
	require.Equal(t, "v26.3.27", m.Version)
	require.Equal(t, "xray", m.Binary())
	_, tr2, err := backend.Lookup("xray/reality")
	require.NoError(t, err)
	require.Equal(t, tr, tr2)
	var _ backend.KeyGenerator = b
}

func TestDomainValidation(t *testing.T) {
	for _, ok := range []string{"a.example", "x-y.example.com", "localhost", "a.b."} {
		require.True(t, validDomain(ok), ok)
	}
	for _, bad := range []string{"", ".a", "-a.example", "a-.example", "a..b", "a b", "ä.example", strings.Repeat("a", 64) + ".x"} {
		require.False(t, validDomain(bad), bad)
	}
	require.True(t, isLoopback("localhost"))
	require.True(t, isLoopback("::1"))
	require.False(t, isLoopback("0.0.0.0"))
}

// TestDecoyFallback: without a selected or configured decoy the built-in
// list applies (backend.DecoyFor); only when that is empty too does Validate
// fail with DEY-B006.
func TestDecoyFallback(t *testing.T) {
	in := fixture(t)
	in.Decoy = ""
	in.Hub.DecoySNIs = nil
	require.NoError(t, New().Validate(in))
	r, err := New().Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Contains(t, string(r.Files[ConfigFile]), backend.DefaultDecoySNIs[0])

	saved := backend.DefaultDecoySNIs
	backend.DefaultDecoySNIs = nil
	t.Cleanup(func() { backend.DefaultDecoySNIs = saved })
	err = New().Validate(in)
	require.True(t, deyerr.HasCode(err, deyerr.B006), "%v", err)
	var de *deyerr.Error
	require.True(t, errors.As(err, &de))
	require.Contains(t, fmt.Sprint(de.Params["reason"]), "no decoy")
}
