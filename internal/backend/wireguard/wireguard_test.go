package wireguard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files")

// seqReader yields 0x01, 0x02, … (deterministic "entropy" for fixtures).
type seqReader struct{ n byte }

func (r *seqReader) Read(p []byte) (int, error) {
	for i := range p {
		r.n++
		p[i] = r.n
	}
	return len(p), nil
}

// fixedKeys returns the fixture keys: deterministic key pairs and, for
// awg, fixed obfuscation parameters.
func fixedKeys(t *testing.T, awg bool) map[string]string {
	t.Helper()
	r := &seqReader{}
	keys := map[string]string{}
	for _, pair := range [][2]string{{KeyHubPrivate, KeyHubPublic}, {KeyNodePrivate, KeyNodePublic}} {
		priv, pub, err := newKeyPair(r)
		require.NoError(t, err)
		keys[pair[0]], keys[pair[1]] = priv, pub
	}
	if awg {
		for k, v := range map[string]string{
			KeyJc: "5", KeyJmin: "50", KeyJmax: "1000", KeyS1: "40", KeyS2: "77",
			KeyH1: "1234567", KeyH2: "2345678", KeyH3: "3456789", KeyH4: "4567890",
		} {
			keys[k] = v
		}
	}
	return keys
}

func backendFor(awg bool) *Backend {
	if awg {
		return NewAWG()
	}
	return New()
}

// fixture is the fixed RenderInput of the golden tests.
func fixture(t *testing.T, awg bool) backend.RenderInput {
	t.Helper()
	b := backendFor(awg)
	tr := b.Transports()[0]
	dir := "/etc/deyroute/backends/" + b.Name() + "/main/de-1/" + tr.Name
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
		Transport:   tr,
		ControlPort: 30001,
		NetIndex:    7,
		Secrets:     backend.Secrets{Token: "tok-test", Keys: fixedKeys(t, awg)},
		Paths: backend.Paths{
			Binary:     "/var/lib/deyroute/bin/awg/v1.0.4/amneziawg-go",
			BinDir:     "/var/lib/deyroute/bin/awg/v1.0.4",
			ConfigDir:  dir,
			LogFile:    "/var/log/deyroute/tunnels/main.log",
			SelfBinary: "/usr/local/bin/deyroute",
		},
	}
}

// mixedFixture adds UDP maps, a default target and a node-IP target.
func mixedFixture(t *testing.T, awg bool) backend.RenderInput {
	in := fixture(t, awg)
	in.Tunnel.Ports = append(in.Tunnel.Ports,
		config.PortMap{Listen: 443, Proto: "udp", Target: "127.0.0.1:443"},
		config.PortMap{Listen: 27015, Proto: "udp"},
		config.PortMap{Listen: 8443, Proto: "tcp", Target: "1.2.3.4:8443"},
		config.PortMap{Listen: 8444, Proto: "tcp", Target: "localhost:443"},
	)
	return in
}

func canaryFixture(t *testing.T, awg bool) backend.RenderInput {
	in := fixture(t, awg)
	in.Canary = true
	in.ListenAddr = "127.0.0.1"
	in.ControlPort = 30002
	in.Tunnel.Ports = []config.PortMap{{Listen: 39001, Proto: "tcp", Target: "127.0.0.1:443"}, {Listen: 39002}}
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
	long := func(awg bool) backend.RenderInput {
		in := fixture(t, awg)
		in.Tunnel.ID = "production-web"
		in.NetIndex = 123
		in.ListenAddr = "::"
		return in
	}
	// tuning.wg_mtu (optimize auto on a NIC with an MTU below 1500).
	mtu := func(awg bool) backend.RenderInput {
		in := fixture(t, awg)
		in.WGMTU = 1380
		return in
	}
	for _, awg := range []bool{false, true} {
		name := Kernel
		if awg {
			name = Userspace
		}
		cases := []struct {
			golden string
			in     backend.RenderInput
		}{
			{name, fixture(t, awg)},
			{name + "_mixed", mixedFixture(t, awg)},
			{name + "_canary", canaryFixture(t, awg)},
			{name + "_long", long(awg)},
			{name + "_mtu1380", mtu(awg)},
		}
		b := backendFor(awg)
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
}

func TestTransportsAndManifest(t *testing.T) {
	k := New().Transports()
	require.Len(t, k, 1)
	require.Equal(t, "wireguard/kernel", k[0].ID())
	require.Equal(t, backend.Forward, k[0].Direction)
	require.Equal(t, []string{"tcp", "udp"}, k[0].Protos)
	require.True(t, k[0].NeedsUDP)
	require.False(t, k[0].NeedsTLS)
	require.Equal(t, 1, k[0].Stealth)
	require.False(t, k[0].Optional)
	require.False(t, k[0].ClientIPPreserved)

	a := NewAWG().Transports()
	require.Len(t, a, 1)
	require.Equal(t, "awg/userspace", a[0].ID())
	require.Equal(t, backend.Forward, a[0].Direction)
	require.True(t, a[0].NeedsUDP)
	require.Equal(t, 3, a[0].Stealth)
	require.True(t, a[0].Optional)

	for _, id := range []string{"wireguard/kernel", "awg/userspace"} {
		b, tr, err := backend.Lookup(id)
		require.NoError(t, err, id)
		require.Equal(t, id, tr.ID())
		_, ok := b.(backend.KeyGenerator)
		require.True(t, ok, "%s must generate keys", id)
		_, err = b.Probe(context.Background(), backend.RenderInput{})
		require.ErrorIs(t, err, backend.ErrNoProbe)
	}

	km := New().Manifest()
	require.Equal(t, Name, km.Name)
	require.True(t, km.System, "the kernel module needs no download")
	require.Empty(t, km.URLs)
	require.Equal(t, "kernel", km.Version)

	am := NewAWG().Manifest()
	require.Equal(t, AWGName, am.Name)
	require.Equal(t, "v1.0.4", am.Version)
	require.Equal(t, []string{"amneziawg-go"}, am.Binaries)
	require.Equal(t, "raw", am.Archive)
	require.Contains(t, am.URLs["amd64"], "/backend-builds/amneziawg-go-v1.0.4-linux-amd64")
	require.Contains(t, am.URLs["arm64"], "/backend-builds/amneziawg-go-v1.0.4-linux-arm64")
	require.False(t, am.System)
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		awg    bool
		mutate func(in *backend.RenderInput)
		code   deyerr.Code
		reason string
	}{
		{"foreign backend", false, func(in *backend.RenderInput) { in.Transport.Backend = "awg" }, deyerr.B006, "not a wireguard/kernel"},
		{"wrong transport", true, func(in *backend.RenderInput) { in.Transport.Name = Kernel }, deyerr.B006, "not a awg/userspace"},
		{"tunnel id", false, func(in *backend.RenderInput) { in.Tunnel.ID = "Main!" }, deyerr.B006, "tunnel id"},
		{"net index high", false, func(in *backend.RenderInput) { in.NetIndex = 256 }, deyerr.B006, "network index"},
		{"net index negative", false, func(in *backend.RenderInput) { in.NetIndex = -1 }, deyerr.B006, "network index"},
		{"ctl port", false, func(in *backend.RenderInput) { in.ControlPort = 0 }, deyerr.B006, "control port"},
		{"node ip empty", false, func(in *backend.RenderInput) { in.Node.PublicIP = "" }, deyerr.B006, "node public IP"},
		{"node ip loopback", false, func(in *backend.RenderInput) { in.Node.PublicIP = "127.0.0.1" }, deyerr.B006, "node public IP"},
		{"config dir", false, func(in *backend.RenderInput) { in.Paths.ConfigDir = "rel" }, deyerr.B006, "config directory"},
		{"self binary", false, func(in *backend.RenderInput) { in.Paths.SelfBinary = "" }, deyerr.B006, "deyroute binary"},
		{"awg binary", true, func(in *backend.RenderInput) { in.Paths.Binary = "amneziawg-go" }, deyerr.B006, "amneziawg-go is not installed"},
		{"missing key", false, func(in *backend.RenderInput) { delete(in.Secrets.Keys, KeyHubPrivate) }, deyerr.B006, "hub_private"},
		{"bad key", false, func(in *backend.RenderInput) { in.Secrets.Keys[KeyNodePublic] = "AAAA" }, deyerr.B006, "node_public"},
		{"mismatched key", false, func(in *backend.RenderInput) {
			in.Secrets.Keys[KeyHubPublic] = in.Secrets.Keys[KeyNodePublic]
		}, deyerr.B006, "do not match"},
		{"awg param missing", true, func(in *backend.RenderInput) { delete(in.Secrets.Keys, KeyH3) }, deyerr.B006, "h3"},
		{"awg param range", true, func(in *backend.RenderInput) { in.Secrets.Keys[KeyJc] = "99" }, deyerr.B006, "jc"},
		{"awg param negative", true, func(in *backend.RenderInput) { in.Secrets.Keys[KeyJc] = "-1" }, deyerr.B006, "out of range"},
		{"awg headers equal", true, func(in *backend.RenderInput) { in.Secrets.Keys[KeyH2] = in.Secrets.Keys[KeyH1] }, deyerr.B006, "distinct"},
		{"awg size clash", true, func(in *backend.RenderInput) { in.Secrets.Keys[KeyS2] = "96" }, deyerr.B006, "s1+56"},
		{"no ports", false, func(in *backend.RenderInput) { in.Tunnel.Ports = nil }, deyerr.B006, "no port maps"},
		{"bad proto", false, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Proto = "sctp" }, deyerr.B010, ""},
		{"listen range", false, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Listen = 70000 }, deyerr.B006, "out of range"},
		{"duplicate listen", false, func(in *backend.RenderInput) { in.Tunnel.Ports[1] = in.Tunnel.Ports[0] }, deyerr.B006, "listed twice"},
		{"bad target", false, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "127.0.0.1" }, deyerr.B006, "invalid target"},
		{"remote target", false, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "10.0.0.5:443" }, deyerr.B006, "not on the node"},
		{"hostname target", false, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "db.local:443" }, deyerr.B006, "not on the node"},
		{"ipv6 target", false, func(in *backend.RenderInput) { in.Tunnel.Ports[0].Target = "[::1]:443" }, deyerr.B006, "not on the node"},
		{"target port clash", false, func(in *backend.RenderInput) {
			in.Tunnel.Ports[1].Target = "1.2.3.4:443"
		}, deyerr.B006, "share port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := backendFor(c.awg)
			in := fixture(t, c.awg)
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
}

// TestParsedConfig parses wg.json back and checks every value per side.
func TestParsedConfig(t *testing.T) {
	for _, awg := range []bool{false, true} {
		b := backendFor(awg)
		in := mixedFixture(t, awg)
		hub, err := b.Render(in, backend.SideHub)
		require.NoError(t, err)
		node, err := b.Render(in, backend.SideNode)
		require.NoError(t, err)

		hc, err := ParseConfig(hub.Files[ConfigFile], "hub")
		require.NoError(t, err)
		nc, err := ParseConfig(node.Files[ConfigFile], "node")
		require.NoError(t, err)
		keys := in.Secrets.Keys

		mode := ModeKernel
		if awg {
			mode = ModeUserspace
		}
		for _, c := range []*Config{hc, nc} {
			require.Equal(t, mode, c.Mode)
			require.Equal(t, "dey-main", c.Interface)
			require.Equal(t, 1420, c.MTU)
			require.Equal(t, "main", c.Tunnel)
			if awg {
				require.Equal(t, &AWGParams{Jc: 5, Jmin: 50, Jmax: 1000, S1: 40, S2: 77, H1: 1234567, H2: 2345678, H3: 3456789, H4: 4567890}, c.AWG)
			} else {
				require.Nil(t, c.AWG)
			}
		}
		require.Equal(t, "hub", hc.Side)
		require.Equal(t, "10.77.7.1/30", hc.Address)
		require.Equal(t, keys[KeyHubPrivate], hc.PrivateKey)
		require.Zero(t, hc.ListenPort)
		require.False(t, hc.RouteLocalnet)
		require.Equal(t, Peer{PublicKey: keys[KeyNodePublic], Endpoint: "1.2.3.4:30001", AllowedIPs: []string{"10.77.7.2/32"}, PersistentKeepalive: 25}, hc.Peer)

		require.Equal(t, "node", nc.Side)
		require.Equal(t, "10.77.7.2/30", nc.Address)
		require.Equal(t, keys[KeyNodePrivate], nc.PrivateKey)
		require.Equal(t, 30001, nc.ListenPort)
		require.True(t, nc.RouteLocalnet)
		require.Equal(t, Peer{PublicKey: keys[KeyHubPublic], AllowedIPs: []string{"10.77.7.1/32"}}, nc.Peer)

		// Units.
		cfg := in.Paths.ConfigDir + "/wg.json"
		for _, r := range []backend.Rendered{hub, node} {
			u := r.Unit
			require.True(t, u.RunAsRoot)
			require.Equal(t, []string{"CAP_NET_ADMIN"}, u.ExtraCaps)
			require.Equal(t, []string{"AF_NETLINK"}, u.AddressFamilies)
			require.Equal(t, in.Paths.ConfigDir, u.WorkingDirectory)
			if awg {
				require.Equal(t, []string{in.Paths.Binary, "-f", "dey-main"}, u.ExecStart)
				require.Empty(t, u.Type)
				require.False(t, u.RemainAfterExit)
				require.Equal(t, []string{"-/run/amneziawg"}, u.ReadWritePaths)
				require.Empty(t, u.ExecStop)
			} else {
				require.Equal(t, "oneshot", u.Type)
				require.True(t, u.RemainAfterExit)
				require.Equal(t, []string{"/usr/local/bin/deyroute", "wg", "up", "--config", cfg}, u.ExecStart)
				require.Equal(t, [][]string{{"/usr/local/bin/deyroute", "wg", "down", "--config", cfg}}, u.ExecStop)
			}
		}
		if !awg {
			require.Empty(t, hub.Unit.DropHardening)
			require.Contains(t, node.Unit.DropHardening, "ProtectKernelTunables")
		}

		// Hub: user ports, DNAT to the node tunnel address, masquerade.
		require.Equal(t, []backend.PortUse{
			{Port: 443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 2053, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 443, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 27015, Proto: "udp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 8443, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
			{Port: 8444, Proto: "tcp", Addr: "0.0.0.0", Purpose: "user"},
		}, hub.Binds)
		require.Equal(t, []backend.NATRule{
			{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "10.77.7.2", ToPort: 443},
			{Proto: "tcp", DportLow: 2053, DportHigh: 2053, ToAddr: "10.77.7.2", ToPort: 2053},
			{Proto: "udp", DportLow: 443, DportHigh: 443, ToAddr: "10.77.7.2", ToPort: 443},
			{Proto: "udp", DportLow: 27015, DportHigh: 27015, ToAddr: "10.77.7.2", ToPort: 27015},
			{Proto: "tcp", DportLow: 8443, DportHigh: 8443, ToAddr: "10.77.7.2", ToPort: 8443},
			{Proto: "tcp", DportLow: 8444, DportHigh: 8444, ToAddr: "10.77.7.2", ToPort: 443},
		}, hub.NAT)
		require.Equal(t, []string{"dey-main"}, hub.Masquerade)
		require.True(t, hub.IPForward)

		// Node: listens on UDP <ctl>.
		require.Equal(t, []backend.PortUse{{Port: 30001, Proto: "udp", Addr: "0.0.0.0", Purpose: "control"}}, node.Binds)
	}
}

// TestNodeNotOpenProxy is the S17 check (spec section 11): the node only
// DNATs tunnel traffic to the configured targets, never masquerades or
// forwards, and accepts only the hub's tunnel address from the peer.
func TestNodeNotOpenProxy(t *testing.T) {
	for _, awg := range []bool{false, true} {
		in := mixedFixture(t, awg)
		node, err := backendFor(awg).Render(in, backend.SideNode)
		require.NoError(t, err)
		require.Empty(t, node.Masquerade)
		require.False(t, node.IPForward)

		allowed := map[string]bool{}
		for _, p := range in.Tunnel.Ports {
			target := p.Target
			if target == "" {
				target = "127.0.0.1:" + strconv.Itoa(p.Listen)
			}
			if target == "localhost:443" {
				target = "127.0.0.1:443"
			}
			allowed[p.Proto+" "+target] = true
		}
		require.NotEmpty(t, node.NAT)
		for _, n := range node.NAT {
			require.Equal(t, "dey-main", n.Iface, "node DNAT must only match tunnel traffic")
			require.Equal(t, n.DportLow, n.DportHigh, "no port ranges")
			require.True(t, allowed[n.Proto+" "+backend.HostPort(n.ToAddr, n.ToPort)], "%+v is not a configured target", n)
		}
		require.Equal(t, []backend.NATRule{
			{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "127.0.0.1", ToPort: 443, Iface: "dey-main"},
			{Proto: "tcp", DportLow: 2053, DportHigh: 2053, ToAddr: "127.0.0.1", ToPort: 2053, Iface: "dey-main"},
			{Proto: "udp", DportLow: 443, DportHigh: 443, ToAddr: "127.0.0.1", ToPort: 443, Iface: "dey-main"},
			{Proto: "udp", DportLow: 27015, DportHigh: 27015, ToAddr: "127.0.0.1", ToPort: 27015, Iface: "dey-main"},
			{Proto: "tcp", DportLow: 8443, DportHigh: 8443, ToAddr: "1.2.3.4", ToPort: 8443, Iface: "dey-main"},
		}, node.NAT)
		c, err := ParseConfig(node.Files[ConfigFile], "node")
		require.NoError(t, err)
		require.Equal(t, []string{"10.77.7.1/32"}, c.Peer.AllowedIPs, "only the hub tunnel address may send through the tunnel")
		require.Empty(t, c.Peer.Endpoint)
	}
}

func TestCanaryAndNaming(t *testing.T) {
	in := canaryFixture(t, false)
	hub, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	c, err := ParseConfig(hub.Files[ConfigFile], "hub")
	require.NoError(t, err)
	require.Equal(t, "deyc-7", c.Interface)
	require.Equal(t, "10.77.7.5/30", c.Address)
	require.Equal(t, "1.2.3.4:30002", c.Peer.Endpoint)
	require.Equal(t, []backend.PortUse{{Port: 39001, Proto: "tcp", Addr: "127.0.0.1", Purpose: "user"}}, hub.Binds)
	require.Equal(t, []backend.NATRule{{Proto: "tcp", DportLow: 39001, DportHigh: 39001, ToAddr: "10.77.7.6", ToPort: 443}}, hub.NAT)

	require.Equal(t, "dey-main", InterfaceName("main", 3, false))
	require.Equal(t, "dey-abcdefghijk", InterfaceName("abcdefghijk", 3, false))
	require.Equal(t, "dey-abcdefghi_3", InterfaceName("abcdefghijkl", 3, false))
	require.Equal(t, "dey-abcdefg_255", InterfaceName("abcdefghijkl", 255, false))
	for _, n := range []int{0, 9, 99, 255} {
		for _, id := range []string{"ab", "abcdefghijk", "abcdefghijklmnopqrstuvwxyz-01234", "production-api"} {
			name := InterfaceName(id, n, false)
			require.LessOrEqual(t, len(name), 15, name)
			require.Regexp(t, ifaceRe, name)
		}
		require.LessOrEqual(t, len(InterfaceName("x", n, true)), 15)
	}
	h, nd := TunnelAddrs(255, false)
	require.Equal(t, "10.77.255.1", h.String())
	require.Equal(t, "10.77.255.2", nd.String())
}

func TestGenerateKeys(t *testing.T) {
	for _, awg := range []bool{false, true} {
		b := backendFor(awg)
		keys, err := b.GenerateKeys(b.Transports()[0])
		require.NoError(t, err)
		for _, pair := range [][2]string{{KeyHubPrivate, KeyHubPublic}, {KeyNodePrivate, KeyNodePublic}} {
			raw, err := base64.StdEncoding.DecodeString(keys[pair[0]])
			require.NoError(t, err)
			require.Len(t, raw, 32)
			require.Equal(t, byte(0), raw[0]&7, "clamped")
			require.Equal(t, byte(64), raw[31]&192, "clamped")
			require.True(t, publicMatches(keys[pair[0]], keys[pair[1]]))
		}
		require.NotEqual(t, keys[KeyHubPrivate], keys[KeyNodePrivate])
		in := fixture(t, awg)
		in.Secrets.Keys = keys
		require.NoError(t, b.Validate(in), "generated keys must validate")
		if !awg {
			require.Len(t, keys, 4)
			continue
		}
		require.Len(t, keys, 13)
		a, err := awgFromKeys(keys)
		require.NoError(t, err)
		require.NoError(t, a.check())
	}

	// Many draws stay within the limits.
	for i := 0; i < 200; i++ {
		a, err := newAWGParams(randReader())
		require.NoError(t, err)
		require.NoError(t, a.check())
	}

	// Entropy failures surface as DEY-B009.
	b := NewAWG()
	b.rand = io.LimitReader(&seqReader{}, 70)
	_, err := b.GenerateKeys(b.Transports()[0])
	require.True(t, deyerr.HasCode(err, deyerr.B009), "%v", err)
	b.rand = io.LimitReader(&seqReader{}, 10)
	_, err = b.GenerateKeys(b.Transports()[0])
	require.True(t, deyerr.HasCode(err, deyerr.B009), "%v", err)

	// A source that only yields unusable values is rejected, not looped on.
	_, err = randRange(zeroFF{}, 0, 2)
	require.Error(t, err)
	_, err = newAWGParams(zeroFF{})
	require.Error(t, err)

	// Deterministic with a fixed source.
	k1, err := (&Backend{name: AWGName, transport: Userspace, awg: true, rand: &seqReader{}}).GenerateKeys(backend.Transport{})
	require.NoError(t, err)
	k2, err := (&Backend{name: AWGName, transport: Userspace, awg: true, rand: &seqReader{}}).GenerateKeys(backend.Transport{})
	require.NoError(t, err)
	require.Equal(t, k1, k2)
}

// zeroFF always returns 0xff bytes: randRange rejects them for span 3.
type zeroFF struct{}

func (zeroFF) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0xff
	}
	return len(p), nil
}

func randReader() io.Reader { return New().rand }

func TestParseConfigErrors(t *testing.T) {
	in := mixedFixture(t, true)
	r, err := NewAWG().Render(in, backend.SideHub)
	require.NoError(t, err)
	good := string(r.Files[ConfigFile])
	_, err = ParseConfig([]byte(good), "wg.json")
	require.NoError(t, err)

	mutate := func(f func(c *Config)) []byte {
		c, err := ParseConfig([]byte(good), "x")
		require.NoError(t, err)
		f(c)
		out, err := backend.JSONIndent(c)
		require.NoError(t, err)
		return out
	}
	bad := map[string][]byte{
		"json":        []byte("{"),
		"unknown key": []byte(`{"mode":"kernel","extra":1}`),
		"mode":        mutate(func(c *Config) { c.Mode = "tap" }),
		"kernel awg":  mutate(func(c *Config) { c.Mode = ModeKernel }),
		"no awg":      mutate(func(c *Config) { c.AWG = nil }),
		"awg range":   mutate(func(c *Config) { c.AWG.Jmax = 5000 }),
		"awg s":       mutate(func(c *Config) { c.AWG.S1 = 1 }),
		"awg h small": mutate(func(c *Config) { c.AWG.H4 = 1 }),
		"iface":       mutate(func(c *Config) { c.Interface = "this-name-is-too-long" }),
		"address":     mutate(func(c *Config) { c.Address = "10.77.7.1" }),
		"mtu":         mutate(func(c *Config) { c.MTU = 100 }),
		"private":     mutate(func(c *Config) { c.PrivateKey = "x" }),
		"listen":      mutate(func(c *Config) { c.ListenPort = 70000 }),
		"peer key":    mutate(func(c *Config) { c.Peer.PublicKey = "" }),
		"endpoint":    mutate(func(c *Config) { c.Peer.Endpoint = "1.2.3.4" }),
		"no allowed":  mutate(func(c *Config) { c.Peer.AllowedIPs = nil }),
		"allowed":     mutate(func(c *Config) { c.Peer.AllowedIPs = []string{"x"} }),
		"keepalive":   mutate(func(c *Config) { c.Peer.PersistentKeepalive = -1 }),
	}
	for name, data := range bad {
		_, err := ParseConfig(data, "/etc/x/wg.json")
		require.True(t, deyerr.HasCode(err, deyerr.B072), "%s: %v", name, err)
	}
	_, err = LoadConfig(filepath.Join(t.TempDir(), "missing.json"))
	require.True(t, deyerr.HasCode(err, deyerr.B072))
}

func TestPostStartStop(t *testing.T) {
	dir := t.TempDir()
	in := fixture(t, false)
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ConfigFile), r.Files[ConfigFile], 0o600))
	f := &fakeRunner{}
	// Kernel: the unit configures itself; PostStart does nothing.
	require.NoError(t, New().PostStart(context.Background(), dir, f))
	require.Empty(t, f.calls)
	// PostStop removes a leftover interface.
	f.exists = map[string]bool{"dey-main": true}
	require.NoError(t, New().PostStop(context.Background(), dir, f))
	require.Equal(t, []string{"ip -o link show dev dey-main", "ip link del dev dey-main"}, f.calls)
	// awg PostStart runs Up (fails here: config is kernel mode, no socket).
	err = NewAWG().PostStart(context.Background(), filepath.Join(dir, "none"), f)
	require.True(t, deyerr.HasCode(err, deyerr.B072), "%v", err)
}

var errExit = errors.New("exit status 1")

// TestTunnelMTU: RenderInput.WGMTU sets the interface MTU when it is within
// [MinMTU, MTU]; 0 and out-of-range values keep the default.
func TestTunnelMTU(t *testing.T) {
	in := fixture(t, false)
	for _, c := range []struct{ set, want int }{
		{0, MTU}, {1380, 1380}, {MinMTU, MinMTU}, {MTU, MTU}, {1279, MTU}, {1500, MTU}, {-1, MTU},
	} {
		in.WGMTU = c.set
		require.Equal(t, c.want, TunnelMTU(in), "wg_mtu %d", c.set)
	}
	in.WGMTU = 1380
	for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
		r, err := New().Render(in, side)
		require.NoError(t, err)
		c, err := ParseConfig(r.Files[ConfigFile], ConfigFile)
		require.NoError(t, err)
		require.Equal(t, 1380, c.MTU)
	}
}
