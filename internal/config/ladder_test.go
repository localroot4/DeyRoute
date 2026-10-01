package config

import (
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

func TestResolveLadder(t *testing.T) {
	custom := []string{"backhaul/tcpmux", "hysteria2/udp", "direct/native"}
	tcp := []PortMap{{Listen: 443, Proto: ProtoTCP}}
	udp := []PortMap{{Listen: 27015, Proto: ProtoUDP}}
	mixed := []PortMap{{Listen: 443, Proto: ProtoTCP}, {Listen: 27015, Proto: ProtoUDP}}

	cases := []struct {
		name     string
		ladders  map[string][]string
		ref      LadderRef
		ports    []PortMap
		supports func(string, string) bool
		want     []string
		code     deyerr.Code
	}{
		{name: "builtin default", ref: LadderRef{Name: "default"}, ports: tcp, want: DefaultLadder},
		{name: "empty ref is default", ports: tcp, want: DefaultLadder},
		{name: "configured default wins", ladders: map[string][]string{"default": custom}, ref: LadderRef{Name: "default"}, ports: tcp, want: custom},
		{name: "named profile", ladders: map[string][]string{"fast": custom}, ref: LadderRef{Name: "fast"}, ports: tcp, want: custom},
		{name: "inline", ref: LadderRef{Inline: []string{"rathole/noise", "direct/native"}}, ports: tcp, want: []string{"rathole/noise", "direct/native"}},
		{name: "udp-only uses udp-default", ref: LadderRef{Name: "default"}, ports: udp, want: DefaultUDPLadder},
		{name: "udp-only ignores customised default", ladders: map[string][]string{"default": custom}, ref: LadderRef{Name: "default"}, ports: udp, want: DefaultUDPLadder},
		{name: "udp-only uses configured udp-default", ladders: map[string][]string{"udp-default": {"direct/native"}}, ref: LadderRef{Name: "default"}, ports: udp, want: []string{"direct/native"}},
		{name: "udp-only named profile is kept", ladders: map[string][]string{"fast": custom}, ref: LadderRef{Name: "fast"}, ports: udp, want: custom},
		{name: "mixed tunnel uses default", ref: LadderRef{Name: "default"}, ports: mixed, want: DefaultLadder},
		{name: "unknown profile", ref: LadderRef{Name: "turbo"}, ports: tcp, code: deyerr.C012},
		{name: "empty configured profile", ladders: map[string][]string{"fast": {}}, ref: LadderRef{Name: "fast"}, ports: tcp, code: deyerr.C009},
		{
			name: "tcp filter drops wireguard", ref: LadderRef{Inline: []string{"wireguard/kernel", "backhaul/tcpmux"}},
			ports: tcp, supports: fakeSupports, want: []string{"backhaul/tcpmux"},
		},
		{
			name: "udp filter on udp-default", ref: LadderRef{Name: "default"}, ports: udp, supports: fakeSupports,
			want: []string{"backhaul/udp", "hysteria2/udp", "wireguard/kernel", "direct/native"},
		},
		{
			name: "mixed filter keeps rungs carrying both", ref: LadderRef{Name: "default"}, ports: mixed, supports: fakeSupports,
			want: []string{"backhaul/wssmux", "backhaul/tcpmux", "hysteria2/udp", "direct/native"},
		},
		{
			name: "filter removes everything", ref: LadderRef{Inline: []string{"wireguard/kernel"}}, ports: tcp,
			supports: fakeSupports, code: deyerr.C009,
		},
		{name: "no ports no filter", ref: LadderRef{Name: "default"}, supports: fakeSupports, want: DefaultLadder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Ladders: tc.ladders}
			tun := &Tunnel{ID: "main", Ladder: tc.ref, Ports: tc.ports}
			got, err := c.ResolveLadder(tun, tc.supports)
			if tc.code != "" {
				requireCodes(t, err, tc.code)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveLadderReturnsCopy(t *testing.T) {
	c := &Config{Ladders: map[string][]string{"default": {"backhaul/tcpmux", "direct/native"}}}
	tun := &Tunnel{ID: "main", Ladder: LadderRef{Name: "default"}, Ports: []PortMap{{Listen: 443, Proto: ProtoTCP}}}
	got, err := c.ResolveLadder(tun, func(id, _ string) bool { return id == "direct/native" })
	require.NoError(t, err)
	require.Equal(t, []string{"direct/native"}, got)
	require.Equal(t, []string{"backhaul/tcpmux", "direct/native"}, c.Ladders["default"], "filtering must not touch the profile")

	tun.Ladder = LadderRef{Inline: []string{"backhaul/tcpmux", "direct/native"}}
	got, err = c.ResolveLadder(tun, func(id, _ string) bool { return id == "direct/native" })
	require.NoError(t, err)
	got[0] = "x/y"
	require.Equal(t, []string{"backhaul/tcpmux", "direct/native"}, tun.Ladder.Inline)

	got, err = (&Config{}).ResolveLadder(&Tunnel{Ladder: LadderRef{Name: "default"}}, nil)
	require.NoError(t, err)
	got[0] = "x/y"
	require.Equal(t, "backhaul/wssmux", DefaultLadder[0], "builtin must not be aliased")
}

func TestLadderProfileHelpers(t *testing.T) {
	c := validHub()
	c.Ladders["fast"] = []string{"backhaul/tcpmux"}
	c.Tunnels = append(c.Tunnels,
		NewTunnel("game", "", []string{"de-1"}, []PortMap{{Listen: 27015, Proto: ProtoUDP}}),
		NewTunnel("quick", "", []string{"de-1"}, []PortMap{{Listen: 8443}}),
		NewTunnel("inline", "", []string{"de-1"}, []PortMap{{Listen: 8444}}),
	)
	c.Tunnels[2].Ladder = LadderRef{Name: "fast"}
	c.Tunnels[3].Ladder = LadderRef{Inline: []string{"direct/native"}}

	require.Equal(t, []string{"default", "fast", "udp-default"}, c.LadderNames())
	require.Equal(t, []string{"default", "udp-default"}, (*Config)(nil).LadderNames())
	require.Equal(t, []string{"main"}, c.LadderUsers("default"))
	require.Equal(t, []string{"game"}, c.LadderUsers("udp-default"))
	require.Equal(t, []string{"quick"}, c.LadderUsers("fast"))
	require.Nil(t, c.LadderUsers("none"))

	r, ok := c.LadderProfile("fast")
	require.True(t, ok)
	r[0] = "changed/x"
	require.Equal(t, "backhaul/tcpmux", c.Ladders["fast"][0])
	r, ok = (*Config)(nil).LadderProfile("udp-default")
	require.True(t, ok)
	require.Equal(t, DefaultUDPLadder, r)
	_, ok = c.LadderProfile("none")
	require.False(t, ok)

	b := BuiltinLadders()
	require.Equal(t, DefaultLadder, b["default"])
	require.Equal(t, DefaultUDPLadder, b["udp-default"])
	b["default"][0] = "x/y"
	require.Equal(t, "backhaul/wssmux", DefaultLadder[0])
	require.True(t, IsBuiltinLadder("default"))
	require.True(t, IsBuiltinLadder("udp-default"))
	require.False(t, IsBuiltinLadder("fast"))
}

func TestResolveLadderEmptyInlineAndNil(t *testing.T) {
	c := validHub()
	tun := &c.Tunnels[0]
	tun.Ladder = LadderRef{Inline: []string{}}
	_, err := c.ResolveLadder(tun, nil)
	requireCodes(t, err, deyerr.C009) // an explicit empty inline list is not the default ladder
	require.Nil(t, c.LadderUsers("default"))

	_, err = c.ResolveLadder(nil, nil)
	requireCodes(t, err, deyerr.C009)
	require.Nil(t, (*Config)(nil).LadderUsers("default"))

	got, err := (*Config)(nil).ResolveLadder(&Tunnel{Ports: []PortMap{{Listen: 1, Proto: ProtoUDP}}}, nil)
	require.NoError(t, err)
	require.Equal(t, DefaultUDPLadder, got)
}
