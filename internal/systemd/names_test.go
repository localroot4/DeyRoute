package systemd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestConstantsMatchConfig(t *testing.T) {
	require.Equal(t, config.TunnelLogDir, TunnelLogDir)
}

func TestNames(t *testing.T) {
	inst := InstanceName("main", "de-1", "backhaul/wssmux")
	require.Equal(t, "main.de-1.backhaul-wssmux", inst)
	require.Equal(t, "deyroute-tun@main.de-1.backhaul-wssmux.service", UnitName(inst))
	require.Equal(t, "main.canary", CanaryInstance("main"))
	require.Equal(t, "deyroute-tun@main.canary.service", UnitName(CanaryInstance("main")))
	require.Equal(t, "/var/log/deyroute/tunnels/main.log", TunnelLogFile("main"))

	got, ok := InstanceOf("deyroute-tun@main.de-1.backhaul-wssmux.service")
	require.True(t, ok)
	require.Equal(t, inst, got)
	for _, u := range []string{HubUnit, "deyroute-tun@.service", "deyroute-tun@x.socket", "nginx.service"} {
		_, ok = InstanceOf(u)
		require.False(t, ok, u)
	}
}

func TestParseInstance(t *testing.T) {
	cases := map[string]Instance{
		"main.de-1.backhaul-wssmux":           {Tunnel: "main", Node: "de-1", Transport: "backhaul/wssmux"},
		"game.nl-2.waterwall-reverse-reality": {Tunnel: "game", Node: "nl-2", Transport: "waterwall/reverse-reality"},
		"t1.n1.hysteria2-udp":                 {Tunnel: "t1", Node: "n1", Transport: "hysteria2/udp"},
		"main.canary":                         {Tunnel: "main", Canary: true},
	}
	for s, want := range cases {
		got, err := ParseInstance(s)
		require.NoError(t, err, s)
		require.Equal(t, want, got, s)
		require.Equal(t, s, got.String())
		require.True(t, ValidInstance(s), s)
	}

	for _, s := range []string{
		"", "main", "main.de-1", "main.de-1.backhaul", "main.de-1.-wssmux", "main.de-1.backhaul-",
		"a.de-1.backhaul-wssmux", "main.de_1.backhaul-wssmux", "Main.de-1.backhaul-wssmux",
		"main.de-1.back.haul-x", "x.canary", "main.other", "main.de-1.back-haul-x/y",
		strings.Repeat("a", 33) + ".de-1.backhaul-wssmux",
	} {
		_, err := ParseInstance(s)
		require.Error(t, err, s)
		require.True(t, deyerr.HasCode(err, deyerr.X034), s)
	}
}

func TestValidInstanceAndUnit(t *testing.T) {
	for _, s := range []string{"main.de-1.backhaul-wssmux", "x", "a.b"} {
		require.True(t, ValidInstance(s), s)
	}
	for _, s := range []string{"", "../etc", "a..b", "-a", ".a", "a/b", "A", "a b", "a\nb", strings.Repeat("a", 201)} {
		require.False(t, ValidInstance(s), s)
	}
	for _, u := range []string{HubUnit, NodeUnit, "deyroute-tun@main.de-1.backhaul-wssmux.service", "nftables.service", "multi-user.target", `dev-x\x2dy.mount`} {
		require.True(t, ValidUnit(u), u)
	}
	for _, u := range []string{"", "--all", "-x.service", "nginx", "a b.service", "a;b.service", "x.service\n", strings.Repeat("a", 260) + ".service"} {
		require.False(t, ValidUnit(u), u)
	}
	require.True(t, ValidUnit(strings.Repeat("a", 255-len(".service"))+".service"))
	require.False(t, ValidUnit(strings.Repeat("a", 256-len(".service"))+".service"))
	// The longest valid instance still yields a valid unit name.
	require.True(t, ValidUnit(UnitName(strings.Repeat("a", maxInstance))))
}
