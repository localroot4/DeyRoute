package config

import (
	"reflect"
	"strings"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

func TestValidID(t *testing.T) {
	for s, want := range map[string]bool{
		"de-1": true, "main": true, "ab": true, "a": false, "": false, "UP": false, "a_b": false,
		"a.b": false, "a b": false, strings.Repeat("a", 32): true, strings.Repeat("a", 33): false,
		"--": true, "مثال": false,
	} {
		require.Equal(t, want, ValidID(s), s)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Main 443/2053":                "main-443-2053",
		"  Germany   1  ":              "germany-1",
		"--x--y--":                     "x-y",
		"A":                            FallbackID,
		"":                             FallbackID,
		"تانل اصلی":                    FallbackID,
		"تانل 2 main":                  "2-main",
		"Ünïcode Straße":               "n-code-stra-e",
		strings.Repeat("ab", 20):       strings.Repeat("ab", 16),
		strings.Repeat("a", 31) + " b": strings.Repeat("a", 31),
		"x9":                           "x9",
	} {
		got := Slugify(in)
		require.Equal(t, want, got, "Slugify(%q)", in)
		require.True(t, ValidID(got), got)
	}
}

func TestUniqueID(t *testing.T) {
	taken := map[string]bool{"main": true, "main-2": true}
	isTaken := func(s string) bool { return taken[s] }
	require.Equal(t, "main-3", UniqueID("main", isTaken))
	require.Equal(t, "free", UniqueID("free", isTaken))
	require.Equal(t, "main", UniqueID("main", nil))
	require.Equal(t, "my-tunnel", UniqueID("My Tunnel", isTaken))

	long := strings.Repeat("a", 32)
	taken[long] = true
	got := UniqueID(long, isTaken)
	require.Equal(t, strings.Repeat("a", 30)+"-2", got)
	require.True(t, ValidID(got))

	dashy := strings.Repeat("a", 29) + "-bb"
	taken[dashy] = true
	got = UniqueID(dashy, isTaken)
	require.Equal(t, strings.Repeat("a", 29)+"-2", got, "a trailing '-' of the cut stem is trimmed")
}

func TestTunnelAndNodeLookup(t *testing.T) {
	c := validHub()
	tun, ok := c.Tunnel("main")
	require.True(t, ok)
	tun.Name = "renamed"
	require.Equal(t, "renamed", c.Tunnels[0].Name, "Tunnel aliases the config")
	_, ok = c.Tunnel("none")
	require.False(t, ok)

	n, ok := c.NodeByID("nl-1")
	require.True(t, ok)
	require.Equal(t, "9.8.7.6", n.PublicIP)
	_, ok = c.NodeByID("none")
	require.False(t, ok)

	require.Equal(t, []string{"main"}, c.TunnelsUsingNode("nl-1"))
	require.Nil(t, c.TunnelsUsingNode("xx"))
}

func TestProtosAndProbeTarget(t *testing.T) {
	tcp := PortMap{Listen: 443, Proto: ProtoTCP}
	tcp2 := PortMap{Listen: 2053, Proto: ProtoTCP}
	udp := PortMap{Listen: 27015, Proto: ProtoUDP}
	cases := []struct {
		name      string
		ports     []PortMap
		probePort int
		protos    []string
		udpOnly   bool
		target    PortMap
		ok        bool
	}{
		{"tcp", []PortMap{tcp, tcp2}, 0, []string{"tcp"}, false, tcp, true},
		{"probe port", []PortMap{tcp, tcp2}, 2053, []string{"tcp"}, false, tcp2, true},
		{"probe port missing falls back", []PortMap{tcp, tcp2}, 9999, []string{"tcp"}, false, tcp, true},
		{"udp first then tcp", []PortMap{udp, tcp}, 0, []string{"tcp", "udp"}, false, tcp, true},
		{"probe port on udp ignored", []PortMap{udp, tcp}, 27015, []string{"tcp", "udp"}, false, tcp, true},
		{"udp only", []PortMap{udp}, 0, []string{"udp"}, true, udp, false},
		{"empty proto is tcp", []PortMap{{Listen: 80}}, 0, []string{"tcp"}, false, PortMap{Listen: 80}, true},
		{"none", nil, 0, nil, false, PortMap{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tun := &Tunnel{Ports: tc.ports, ProbePort: tc.probePort}
			require.Equal(t, tc.protos, tun.Protos())
			require.Equal(t, tc.udpOnly, tun.UDPOnly())
			for _, p := range tc.protos {
				require.True(t, tun.HasProto(p))
			}
			got, ok := tun.ProbeTarget()
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.target, got)
		})
	}
}

func TestUsedListenPorts(t *testing.T) {
	c := validHub()
	c.Tunnels = append(c.Tunnels, NewTunnel("game", "", []string{"de-1"}, []PortMap{{Listen: 443, Proto: ProtoUDP}, {Listen: 443}}))
	used := c.UsedListenPorts()
	require.Equal(t, map[ListenKey]string{
		{443, "tcp"}:  "main",
		{2053, "tcp"}: "main",
		{443, "udp"}:  "game",
	}, used)
	require.Equal(t, "443/udp", ListenKey{443, "udp"}.String())
}

func TestAddRemoveTunnel(t *testing.T) {
	c := validHub()
	ports := []PortMap{{Listen: 8443}}
	require.NoError(t, c.AddTunnel(Tunnel{ID: "alt", Nodes: []string{"de-1"}, Ports: ports}))
	alt, ok := c.Tunnel("alt")
	require.True(t, ok)
	require.Equal(t, "127.0.0.1:8443", alt.Ports[0].Target, "defaults applied")
	require.Equal(t, "", ports[0].Target, "caller's slice is not modified")
	require.Equal(t, LadderRef{Name: DefaultLadderName}, alt.Ladder)

	before := Clone(c)
	requireCodes(t, c.AddTunnel(Tunnel{ID: "alt", Nodes: []string{"de-1"}, Ports: []PortMap{{Listen: 9443}}}), deyerr.C002)
	requireCodes(t, c.AddTunnel(Tunnel{ID: "Bad", Nodes: []string{"de-1"}, Ports: []PortMap{{Listen: 9443}}}), deyerr.C007)
	requireCodes(t, c.AddTunnel(Tunnel{ID: "x1", Nodes: []string{"zz-1"}, Ports: []PortMap{{Listen: 9443}}}), deyerr.C010)
	requireCodes(t, c.AddTunnel(Tunnel{ID: "x1", Ports: []PortMap{{Listen: 9443}}}), deyerr.C008)
	requireCodes(t, c.AddTunnel(Tunnel{ID: "x1", Nodes: []string{"de-1"}, Ports: []PortMap{{Listen: 443}}}), deyerr.C003)
	requireCodes(t, c.AddTunnel(Tunnel{ID: "x1", Nodes: []string{"de-1"}, Ports: []PortMap{{Listen: 30001}}}), deyerr.C011)
	require.Equal(t, before, c, "failed adds change nothing")

	require.True(t, c.RemoveTunnel("alt"))
	require.False(t, c.RemoveTunnel("alt"))
	_, ok = c.Tunnel("alt")
	require.False(t, ok)
}

func TestAddRemoveNode(t *testing.T) {
	c := validHub()
	n := Node{ID: "fr-1", Name: "France", PublicIP: "10.1.1.1", Tags: []string{"backup"}}
	require.NoError(t, c.AddNode(n))
	n.Tags[0] = "mutated"
	got, ok := c.NodeByID("fr-1")
	require.True(t, ok)
	require.Equal(t, []string{"backup"}, got.Tags, "tags are copied")

	before := Clone(c)
	requireCodes(t, c.AddNode(Node{ID: "fr-1", PublicIP: "10.1.1.2"}), deyerr.C002)
	requireCodes(t, c.AddNode(Node{ID: "FR", PublicIP: "10.1.1.2"}), deyerr.C007)
	requireCodes(t, c.AddNode(Node{ID: "fr-2", PublicIP: "nope"}), deyerr.C013)
	require.Equal(t, before, c)

	// Removing a backup node takes it out of the tunnel's list.
	affected, removed, err := c.RemoveNode("nl-1")
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, []string{"main"}, affected)
	require.Equal(t, []string{"de-1"}, c.Tunnels[0].Nodes)
	require.NoError(t, c.Validate(ValidateOptions{}))

	// The last node of a tunnel cannot be removed.
	before = Clone(c)
	_, removed, err = c.RemoveNode("de-1")
	requireCodes(t, err, deyerr.C008)
	require.False(t, removed)
	require.Equal(t, before, c)

	affected, removed, err = c.RemoveNode("fr-1")
	require.NoError(t, err)
	require.True(t, removed)
	require.Nil(t, affected)

	affected, removed, err = c.RemoveNode("none")
	require.NoError(t, err)
	require.False(t, removed)
	require.Nil(t, affected)
}

// fullConfig sets every pointer, slice and map so Clone is fully exercised.
func fullConfig() *Config {
	c := validHub()
	c.Hub.DecoySNIs = []string{"www.example.com"}
	c.Hub.ACME = &ACME{Email: "o@example.com"}
	c.Tunnels[0].Ladder = LadderRef{Inline: []string{"direct/native"}}
	c.Tunnels[0].Advanced = &Advanced{ConnectionPool: 4}
	c.Node = validNode().Node
	return c
}

func TestCloneDeep(t *testing.T) {
	require.Nil(t, Clone(nil))
	c := fullConfig()
	cp := Clone(c)
	require.Equal(t, c, cp)
	assertNoAliasing(t, reflect.ValueOf(c), reflect.ValueOf(cp), "config")

	// Nil-ness is preserved (DeepEqual distinguishes nil and empty).
	e := &Config{Nodes: []Node{}, Ladders: map[string][]string{}}
	require.Equal(t, e, Clone(e))
	require.Equal(t, &Config{}, Clone(&Config{}))
}

// assertNoAliasing fails when a and b share any pointer, slice backing array
// or map.
func assertNoAliasing(t *testing.T, a, b reflect.Value, path string) {
	t.Helper()
	switch a.Kind() {
	case reflect.Ptr:
		if a.IsNil() || b.IsNil() {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s is shared", path)
		assertNoAliasing(t, a.Elem(), b.Elem(), path)
	case reflect.Slice:
		if a.Len() > 0 && b.Len() > 0 {
			require.NotEqual(t, a.Pointer(), b.Pointer(), "%s backing array is shared", path)
		}
		for i := 0; i < a.Len() && i < b.Len(); i++ {
			assertNoAliasing(t, a.Index(i), b.Index(i), path+"[]")
		}
	case reflect.Map:
		if a.IsNil() || b.IsNil() {
			return
		}
		require.NotEqual(t, a.Pointer(), b.Pointer(), "%s map is shared", path)
		for _, k := range a.MapKeys() {
			if bv := b.MapIndex(k); bv.IsValid() {
				assertNoAliasing(t, a.MapIndex(k), bv, path+"["+k.String()+"]")
			}
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			assertNoAliasing(t, a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name)
		}
	}
}

func TestTunnelClone(t *testing.T) {
	tun := fullConfig().Tunnels[0]
	cp := tun.Clone()
	require.Equal(t, tun, cp)
	assertNoAliasing(t, reflect.ValueOf(&tun), reflect.ValueOf(&cp), "tunnel")
}

func TestCheckImmutable(t *testing.T) {
	require.NoError(t, CheckImmutable(nil, validHub()))
	require.NoError(t, CheckImmutable(validHub(), validHub()))

	prev := validNode()
	next := validNode()
	next.Node.ID = "de-2"
	requireCodes(t, CheckImmutable(prev, next), deyerr.C018)

	prevHub := validHub()
	nextHub := validHub()
	nextHub.Nodes[0].ID = "de-9" // same fingerprint, new id
	err := CheckImmutable(prevHub, nextHub)
	requireCodes(t, err, deyerr.C018)
	require.Equal(t, "de-1", firstErr(t, err).Params["id"])

	// Renaming the display name is fine; so is a new node with the same
	// fingerprint while the old one still exists (a duplicate, caught elsewhere).
	nextHub = validHub()
	nextHub.Nodes[0].Name = "Frankfurt"
	nextHub.Nodes = append(nextHub.Nodes, Node{ID: "de-2", PublicIP: "1.1.1.1", CertFingerprint: testFP})
	require.NoError(t, CheckImmutable(prevHub, nextHub))
}

func TestUniqueIDGivesUp(t *testing.T) {
	require.Equal(t, "", UniqueID("main", func(string) bool { return true }))
}

func TestTypesHelpers(t *testing.T) {
	require.Equal(t, HubInfo{}, (*Hub)(nil).Info())
	h := &Hub{Name: "ir-1", PublicIP: "5.6.7.8", PublicIP6: "2001:db8::1", Domain: "t.example.com", ControlPort: 44433, DecoySNIs: []string{"a.example.com"}}
	require.Equal(t, HubInfo{Name: "ir-1", PublicIP: "5.6.7.8", PublicIP6: "2001:db8::1", Domain: "t.example.com", ControlPort: 44433, DecoySNIs: []string{"a.example.com"}}, h.Info())
	require.Equal(t, "default", LadderRef{Name: "default"}.String())
	require.Equal(t, "[a/b c/d]", LadderRef{Inline: []string{"a/b", "c/d"}}.String())
	require.True(t, LadderRef{}.IsZero())
	require.Nil(t, clonePorts(nil))
	require.Nil(t, cloneStrings(nil))
}

func TestNilReceivers(t *testing.T) {
	var c *Config
	_, ok := c.Tunnel("main")
	require.False(t, ok)
	_, ok = c.NodeByID("de-1")
	require.False(t, ok)
	require.Nil(t, c.TunnelsUsingNode("de-1"))
	require.Empty(t, c.UsedListenPorts())
	require.False(t, c.RemoveTunnel("main"))
	affected, removed, err := c.RemoveNode("de-1")
	require.NoError(t, err)
	require.False(t, removed)
	require.Nil(t, affected)
	requireCodes(t, c.AddTunnel(NewTunnel("main", "", []string{"de-1"}, []PortMap{{Listen: 443}})), deyerr.C016)
	requireCodes(t, c.AddNode(Node{ID: "de-1", PublicIP: "1.2.3.4"}), deyerr.C016)
	c.ApplyDefaults() // must not panic

	var tun *Tunnel
	require.False(t, tun.HasProto(ProtoTCP))
	require.Nil(t, tun.Protos())
	require.False(t, tun.UDPOnly())
	pm, ok := tun.ProbeTarget()
	require.False(t, ok)
	require.Equal(t, PortMap{}, pm)
	require.Equal(t, Tunnel{}, tun.Clone())
	require.Equal(t, DefaultConnectionPool, tun.ConnectionPool())
	up, down := tun.HysteriaMbps()
	require.Equal(t, []int{DefaultHysteriaMbps, DefaultHysteriaMbps}, []int{up, down})
}

func TestAdvancedEffectiveValues(t *testing.T) {
	tun := NewTunnel("main", "", []string{"de-1"}, []PortMap{{Listen: 443}})
	require.Equal(t, 8, tun.ConnectionPool())
	up, down := tun.HysteriaMbps()
	require.Equal(t, 100, up)
	require.Equal(t, 100, down)

	tun.Advanced = &Advanced{ConnectionPool: 32, HysteriaDownMbps: 500}
	require.Equal(t, 32, tun.ConnectionPool())
	up, down = tun.HysteriaMbps()
	require.Equal(t, 100, up)
	require.Equal(t, 500, down)

	tun.Advanced = &Advanced{HysteriaUpMbps: 50}
	require.Equal(t, 8, tun.ConnectionPool())
	up, down = tun.HysteriaMbps()
	require.Equal(t, 50, up)
	require.Equal(t, 100, down)
}

func TestRemoveClearsTail(t *testing.T) {
	c := validHub()
	c.Tunnels = append(c.Tunnels, NewTunnel("b", "", []string{"de-1"}, []PortMap{{Listen: 8443}}))
	full := c.Tunnels[:2]
	require.True(t, c.RemoveTunnel("main"))
	require.Len(t, c.Tunnels, 1)
	require.Equal(t, "b", c.Tunnels[0].ID)
	require.Equal(t, Tunnel{}, full[1], "the vacated slot is zeroed, not a stale duplicate")
}
