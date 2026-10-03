package waterwall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

const password = "Pw0rdPw0rdPw0rdPw0rdPw0r"

func transport(t *testing.T) backend.Transport {
	t.Helper()
	tr := New().Transports()
	require.Len(t, tr, 1)
	return tr[0]
}

// fixture is the fixed RenderInput of the golden tests.
func fixture(t *testing.T) backend.RenderInput {
	t.Helper()
	dir := "/etc/deyroute/backends/waterwall/main/de-1/reverse-reality"
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
		Secrets:     backend.Secrets{Token: "tok-test", Keys: map[string]string{KeyPassword: password}},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/waterwall/v1.46.94/Waterwall",
			BinDir:     "/var/lib/deyroute/bin/waterwall/v1.46.94",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// singleFixture has one port map with a different target port.
func singleFixture(t *testing.T) backend.RenderInput {
	in := fixture(t)
	in.Tunnel.Ports = []config.PortMap{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:8443"}}
	in.FirstRun = true
	return in
}

func serialize(t *testing.T, side backend.Side, r backend.Rendered) []byte {
	t.Helper()
	var b bytes.Buffer
	fmt.Fprintf(&b, "# waterwall/reverse-reality %s side\n", side)
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
		{"reverse-reality", fixture(t)},
		{"reverse-reality_single", singleFixture(t)},
		{"reverse-reality_canary", canary},
	}
	// Backend tiers (optimize auto --backends): the ram-profile of each
	// side follows its own tier.
	for _, tier := range []string{config.BackendTierSmall, config.BackendTierLarge} {
		in := fixture(t)
		in.HubTier, in.NodeTier = tier, tier
		cases = append(cases, struct {
			golden string
			in     backend.RenderInput
		}{"reverse-reality_" + tier, in})
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
				require.NoError(t, ValidateJSON(r.Files), "rendered files must pass ValidateJSON")
				checkGolden(t, c.golden+"."+side.String()+".golden", serialize(t, side, r))
			})
		}
	}
}

type graph struct {
	Name  string `json:"name"`
	Nodes []struct {
		Name     string         `json:"name"`
		Type     string         `json:"type"`
		Settings map[string]any `json:"settings"`
		Next     string         `json:"next"`
	} `json:"nodes"`
}

func (g graph) node(t *testing.T, name string) (typ string, settings map[string]any, next string) {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Name == name {
			return n.Type, n.Settings, n.Next
		}
	}
	t.Fatalf("node %s missing", name)
	return "", nil, ""
}

func parse(t *testing.T, r backend.Rendered) graph {
	t.Helper()
	var g graph
	require.NoError(t, json.Unmarshal(r.Files[ConfigFile], &g))
	return g
}

func TestHubGraph(t *testing.T) {
	in := fixture(t)
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	g := parse(t, r)
	require.Equal(t, "deyroute-main-de-1-hub", g.Name)

	typ, s, next := g.node(t, "users-inbound")
	require.Equal(t, "TcpListener", typ)
	require.Equal(t, "0.0.0.0", s["address"])
	require.Equal(t, []any{float64(443), float64(2053)}, s["port"])
	require.Equal(t, "header-client", next)
	typ, s, next = g.node(t, "header-client")
	require.Equal(t, "HeaderClient", typ)
	require.Equal(t, "src_context->port", s["data"])
	require.Equal(t, "bridge-users", next)
	_, s, next = g.node(t, "bridge-users")
	require.Equal(t, "bridge-reverse", s["pair"])
	require.Empty(t, next)
	_, s, _ = g.node(t, "bridge-reverse")
	require.Equal(t, "bridge-users", s["pair"])
	typ, _, next = g.node(t, "reverse-server")
	require.Equal(t, "ReverseServer", typ)
	require.Equal(t, "bridge-reverse", next, "ReverseServer -> Bridge must be adjacent")
	typ, s, next = g.node(t, "reality-server")
	require.Equal(t, "RealityServer", typ)
	require.Equal(t, "reality-decoy", s["destination"])
	require.Equal(t, password, s["password"])
	require.Equal(t, "reverse-server", next)
	typ, s, next = g.node(t, "node-inbound")
	require.Equal(t, "TcpListener", typ)
	require.Equal(t, "0.0.0.0", s["address"])
	require.Equal(t, float64(30001), s["port"])
	require.Equal(t, []any{"1.2.3.4/32"}, s["whitelist"])
	require.Equal(t, "reality-server", next)
	typ, s, _ = g.node(t, "reality-decoy")
	require.Equal(t, "TcpConnector", typ)
	require.Equal(t, "www.example.com", s["address"])
	require.Equal(t, float64(443), s["port"])

	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
	}, r.Binds)
	require.Equal(t, []string{in.Paths.Binary}, r.Unit.ExecStart)
	require.Equal(t, in.Paths.ConfigDir, r.Unit.WorkingDirectory, "WorkingDirectory must hold core.json")
	require.Empty(t, r.Unit.DropHardening, "Waterwall runs with the full hardening, MemoryDenyWriteExecute included")
}

func TestNodeGraph(t *testing.T) {
	r, err := New().Render(fixture(t), backend.SideNode)
	require.NoError(t, err)
	require.Empty(t, r.Binds, "the node side binds nothing")
	g := parse(t, r)
	typ, s, next := g.node(t, "bridge-reverse")
	require.Equal(t, "Bridge", typ)
	require.Equal(t, "bridge-service", s["pair"])
	require.Equal(t, "reverse-client", next, "Bridge -> ReverseClient must be adjacent")
	typ, s, next = g.node(t, "reverse-client")
	require.Equal(t, "ReverseClient", typ)
	require.Equal(t, float64(16), s["minimum-unused"])
	require.Equal(t, "reality-client", next)
	typ, s, next = g.node(t, "reality-client")
	require.Equal(t, "RealityClient", typ)
	require.Equal(t, "www.example.com", s["sni"])
	require.Equal(t, password, s["password"])
	require.Equal(t, "hub-outbound", next)
	_, s, _ = g.node(t, "hub-outbound")
	require.Equal(t, "5.6.7.8", s["address"])
	require.Equal(t, float64(30001), s["port"])
	_, _, next = g.node(t, "bridge-service")
	require.Equal(t, "header-server", next)
	_, s, next = g.node(t, "header-server")
	require.Equal(t, "dest_context->port", s["override"])
	require.Equal(t, "service-outbound", next)
	_, s, _ = g.node(t, "service-outbound")
	require.Equal(t, "127.0.0.1", s["address"])
	require.Equal(t, "dest_context->port", s["port"])

	// A single port map connects straight to its target (no header).
	r, err = New().Render(singleFixture(t), backend.SideNode)
	require.NoError(t, err)
	g = parse(t, r)
	_, _, next = g.node(t, "bridge-service")
	require.Equal(t, "service-outbound", next)
	_, s, _ = g.node(t, "service-outbound")
	require.Equal(t, float64(8443), s["port"])
	for _, n := range g.Nodes {
		require.NotEqual(t, "HeaderServer", n.Type)
	}
}

func TestCoreJSON(t *testing.T) {
	var c struct {
		Log struct {
			Path string `json:"path"`
			Core struct {
				LogLevel string `json:"loglevel"`
				Console  bool   `json:"console"`
			} `json:"core"`
			Network struct {
				LogLevel string `json:"loglevel"`
			} `json:"network"`
		} `json:"log"`
		Misc struct {
			Workers        int    `json:"workers"`
			RAMProfile     string `json:"ram-profile"`
			TCPTune        bool   `json:"tcp-tune"`
			TryEnablingBBR bool   `json:"try-enabling-bbr"`
		} `json:"misc"`
		Configs []string `json:"configs"`
	}
	require.NoError(t, json.Unmarshal(CoreJSON(2), &c))
	require.Equal(t, 2, c.Misc.Workers)
	require.Equal(t, "server", c.Misc.RAMProfile)
	require.False(t, c.Misc.TCPTune)
	require.False(t, c.Misc.TryEnablingBBR)
	require.Equal(t, "INFO", c.Log.Core.LogLevel)
	require.True(t, c.Log.Core.Console)
	require.Equal(t, []string{"config.json"}, c.Configs)
	require.Equal(t, "/tmp/deyroute-waterwall/", c.Log.Path)

	require.NoError(t, json.Unmarshal(CoreJSONFor(0, true), &c))
	require.Equal(t, 1, c.Misc.Workers)
	require.Equal(t, "DEBUG", c.Log.Core.LogLevel)
	require.Equal(t, "DEBUG", c.Log.Network.LogLevel)

	r, err := New().Render(fixture(t), backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, CoreJSONFor(DefaultWorkers, false), r.Files[CoreFile])
	r, err = New().Render(singleFixture(t), backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, CoreJSONFor(DefaultWorkers, true), r.Files[CoreFile], "first run logs at DEBUG")

	require.Equal(t, 1, Workers(0))
	require.Equal(t, 2, Workers(2))
	require.Equal(t, 4, Workers(4))
	require.Equal(t, 4, Workers(64))
}

func TestValidateJSON(t *testing.T) {
	r, err := New().Render(fixture(t), backend.SideHub)
	require.NoError(t, err)
	good := r.Files
	clone := func() map[string][]byte {
		out := map[string][]byte{}
		for k, v := range good {
			out[k] = append([]byte(nil), v...)
		}
		return out
	}
	require.NoError(t, ValidateJSON(good))

	f := clone()
	delete(f, CoreFile)
	require.True(t, deyerr.HasCode(ValidateJSON(f), deyerr.B041))

	cases := []struct {
		name   string
		file   string
		data   string
		detail string
	}{
		{"truncated json", ConfigFile, `{"name": "x", "nodes": [`, "truncated"},
		{"broken json", ConfigFile, `{"name": "x", "nodes": [,]}`, "syntax error"},
		{"trailing", ConfigFile, `{} {}`, "trailing data"},
		{"broken core", CoreFile, `{"configs": }`, "syntax error"},
		{"configs type", CoreFile, `{"configs": "config.json"}`, "configs"},
		{"no configs", CoreFile, `{"configs": []}`, "lists no node config"},
		{"missing config", CoreFile, `{"configs": ["other.json"]}`, "not present"},
		{"nodes wrong type", ConfigFile, `{"name": "x", "nodes": {}}`, "unexpected structure"},
		{"no nodes", ConfigFile, `{"name": "x", "nodes": []}`, "empty"},
		{"unnamed", ConfigFile, `{"nodes": [{"type": "TcpListener"}]}`, "no name or type"},
		{"duplicate", ConfigFile, `{"nodes": [{"name": "a", "type": "Bridge", "settings": {"pair": "a"}}, {"name": "a", "type": "Bridge"}]}`, "used twice"},
		{"bad next", ConfigFile, `{"nodes": [{"name": "a", "type": "TcpListener", "next": "b"}]}`, "does not exist"},
		{"double chain", ConfigFile, `{"nodes": [{"name": "a", "type": "X", "next": "c"}, {"name": "b", "type": "X", "next": "c"}, {"name": "c", "type": "Y"}]}`, "both chain"},
		{"bad settings", ConfigFile, `{"nodes": [{"name": "a", "type": "X", "settings": []}]}`, "must be an object"},
		{"no pair", ConfigFile, `{"nodes": [{"name": "a", "type": "Bridge", "settings": {}}]}`, "settings.pair"},
		{"bad pair", ConfigFile, `{"nodes": [{"name": "a", "type": "Bridge", "settings": {"pair": "z"}}]}`, "does not exist"},
		{"bad destination", ConfigFile, `{"nodes": [{"name": "a", "type": "RealityServer", "settings": {"destination": "z", "password": "p"}}]}`, "does not exist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := clone()
			f[c.file] = []byte(c.data)
			err := ValidateJSON(f)
			require.True(t, deyerr.HasCode(err, deyerr.B040), "%v", err)
			var de *deyerr.Error
			require.True(t, errors.As(err, &de))
			require.Contains(t, de.Detail, c.detail)
		})
	}

	f = clone()
	f["extra.json"] = []byte(`{`)
	err = ValidateJSON(f)
	require.True(t, deyerr.HasCode(err, deyerr.B040))
	var de *deyerr.Error
	require.True(t, errors.As(err, &de))
	require.Equal(t, "extra.json", de.Params["file"])
	f = clone()
	f[ConfigFile] = []byte(`{"nodes": [{"name": "a", "type": "X", "settings": null}]}`)
	require.NoError(t, ValidateJSON(f))
}

func TestGenerateKeys(t *testing.T) {
	k, err := New().GenerateKeys(transport(t))
	require.NoError(t, err)
	require.Len(t, k, 1)
	require.Regexp(t, `^[A-Za-z0-9]{24}$`, k[KeyPassword])
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
		{"wrong transport", func(in *backend.RenderInput) { in.Transport.Name = "reality" }, deyerr.B006, "not a waterwall"},
		{"no ports", func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"udp", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Proto = "udp" }, deyerr.B010, ""},
		{"bad ctl", func(in *backend.RenderInput) { in.ControlPort = -1 }, deyerr.B006, "control port"},
		{"relative binary", func(in *backend.RenderInput) { in.Paths.Binary = "Waterwall" }, deyerr.B006, "binary"},
		{"relative dir", func(in *backend.RenderInput) { in.Paths.ConfigDir = "." }, deyerr.B006, "config directory"},
		{"no hub ip", func(in *backend.RenderInput) { in.Hub.PublicIP = "" }, deyerr.B006, "hub public IP"},
		{"no password", func(in *backend.RenderInput) { in.Secrets.Keys = nil }, deyerr.B006, "password"},
		{"long password", func(in *backend.RenderInput) {
			in.Secrets.Keys = map[string]string{KeyPassword: password + password}
		}, deyerr.B006, "password"},
		{"bad hub decoy", func(in *backend.RenderInput) { in.Hub.DecoySNIs = []string{"bad_decoy!"} }, deyerr.B006, "domain name"},
		{"decoy ip", func(in *backend.RenderInput) { in.Decoy = "8.8.8.8" }, deyerr.B006, "domain name"},
		{"listen range", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 65536 }, deyerr.B006, "out of range"},
		{"duplicate", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Listen = 443 }, deyerr.B006, "twice"},
		{"bad target", func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "nope" }, deyerr.B006, "invalid target"},
		{"multi different port", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Target = "127.0.0.1:8443" }, deyerr.B006, "same port as listen"},
		{"multi different host", func(in *backend.RenderInput) { in.Tunnel.Ports[1].Target = "10.0.0.2:2053" }, deyerr.B006, "one host"},
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

	// Accepted variants: default target, decoy with port, IPv6 node, "::" listen.
	in := fixture(t)
	in.Tunnel.Ports = []config.PortMap{{Listen: 443}, {Listen: 2053}}
	in.Decoy = "cdn.example.org:8443"
	in.Node.PublicIP = "2001:db8::5"
	in.ListenAddr = "::"
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	g := parse(t, r)
	_, s, _ := g.node(t, "reality-decoy")
	require.Equal(t, "cdn.example.org", s["address"])
	require.Equal(t, float64(8443), s["port"])
	_, s, _ = g.node(t, "node-inbound")
	require.Equal(t, []any{"2001:db8::5/128"}, s["whitelist"])
	_, s, _ = g.node(t, "users-inbound")
	require.Equal(t, "::", s["address"])
	in.Node.PublicIP = "node.example.net"
	r, err = New().Render(in, backend.SideHub)
	require.NoError(t, err)
	g = parse(t, r)
	_, s, _ = g.node(t, "node-inbound")
	require.NotContains(t, s, "whitelist")
}

func TestCanary(t *testing.T) {
	in := fixture(t)
	in.Canary = true
	in.ListenAddr = "0.0.0.0"
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, []backend.PortUse{
		{Port: 30001, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"},
		{Port: 443, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"},
	}, r.Binds)
	in.ListenAddr = "127.0.0.2"
	r, err = New().Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.2", r.Binds[1].Addr)
}

func TestMetadata(t *testing.T) {
	b := New()
	require.Equal(t, "waterwall", b.Name())
	tr := transport(t)
	require.Equal(t, "waterwall/reverse-reality", tr.ID())
	require.Equal(t, backend.Reverse, tr.Direction)
	require.Equal(t, []string{"tcp"}, tr.Protos)
	require.Equal(t, 5, tr.Stealth)
	require.False(t, tr.NeedsUDP)
	require.False(t, tr.NeedsTLS)
	_, err := b.Probe(context.Background(), fixture(t))
	require.ErrorIs(t, err, backend.ErrNoProbe)
	m := b.Manifest()
	require.Equal(t, "v1.46.94", m.Version)
	require.Equal(t, "Waterwall", m.Binary())
	var _ backend.KeyGenerator = b
	require.False(t, validDomain(".a"))
	require.False(t, validDomain("a..b"))
	require.False(t, validDomain("a_b.c"))
	require.True(t, validDomain("a-b.c."))
	require.Equal(t, "", whitelist("x"))
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

// Spec 7.4: core.json gets min(4, CPU) workers of the side that runs it.
func TestWorkersFollowTheSideCPUCount(t *testing.T) {
	b := New()
	workers := func(r backend.Rendered) int {
		var c struct {
			Misc struct {
				Workers int `json:"workers"`
			} `json:"misc"`
		}
		require.NoError(t, json.Unmarshal(r.Files[CoreFile], &c))
		return c.Misc.Workers
	}
	in := fixture(t)
	hub, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, DefaultWorkers, workers(hub), "unknown CPU count")
	in.HubCPUs, in.NodeCPUs = 2, 16
	hub, err = b.Render(in, backend.SideHub)
	require.NoError(t, err)
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, 2, workers(hub))
	require.Equal(t, 4, workers(node))
	in.NodeCPUs = 1
	node, err = b.Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, 1, workers(node))
}

// Spec 7.4: the JSON is validated before the unit starts; a start failure
// is DEY-B043.
func TestPreStartValidatesTheJSON(t *testing.T) {
	b := New()
	r, err := b.Render(fixture(t), backend.SideHub)
	require.NoError(t, err)
	dir := t.TempDir()
	for name, data := range r.Files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), data, 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("not json"), 0o600))
	require.NoError(t, b.PreStart(context.Background(), dir, nil))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFile), []byte(`{"name": "x", "nodes": [`), 0o600))
	err = b.PreStart(context.Background(), dir, nil)
	require.True(t, deyerr.HasCode(err, deyerr.B040), "%v", err)

	require.NoError(t, os.Remove(filepath.Join(dir, CoreFile)))
	require.True(t, deyerr.HasCode(b.PreStart(context.Background(), dir, nil), deyerr.B041))
	require.True(t, deyerr.HasCode(b.PreStart(context.Background(), filepath.Join(dir, "missing"), nil), deyerr.B041))

	require.Equal(t, deyerr.B043, b.StartFailureCode())
	require.Equal(t, deyerr.B043, backend.StartFailureCode("waterwall/reverse-reality"))
	require.Equal(t, deyerr.B003, backend.StartFailureCode("nosuch/x"))
}

// The ram-profile follows the backend tier of the side that runs the unit;
// only names the pinned Waterwall accepts are rendered, and no tier (or
// medium) keeps the spec's "server".
func TestRAMProfileFollowsTheSideTier(t *testing.T) {
	b := New()
	profile := func(r backend.Rendered) string {
		var c struct {
			Misc struct {
				RAMProfile string `json:"ram-profile"`
			} `json:"misc"`
		}
		require.NoError(t, json.Unmarshal(r.Files[CoreFile], &c))
		return c.Misc.RAMProfile
	}
	for _, tier := range []string{"", config.BackendTierSmall, config.BackendTierMedium, config.BackendTierLarge, "bogus"} {
		require.Contains(t, []string{RAMProfileServer, RAMProfileClient}, RAMProfile(tier))
	}
	require.Equal(t, RAMProfileServer, RAMProfile(""))
	require.Equal(t, RAMProfileServer, RAMProfile(config.BackendTierMedium))

	in := fixture(t)
	def, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	in.HubTier, in.NodeTier = config.BackendTierMedium, config.BackendTierSmall
	hub, err := b.Render(in, backend.SideHub)
	require.NoError(t, err)
	require.Equal(t, def, hub, "medium renders the defaults")
	node, err := b.Render(in, backend.SideNode)
	require.NoError(t, err)
	require.Equal(t, RAMProfileServer, profile(hub))
	require.Equal(t, RAMProfileClient, profile(node))
}
