package render

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/firewall"
)

func TestStatsSpec(t *testing.T) {
	cfg := fwConfig() // main (443/tcp, 2053/tcp, 443/udp), games (27015/udp, 2053/tcp), off (disabled)
	s := StatsSpec(cfg, func(id string) bool { return id == "games" })
	require.Equal(t, firewall.StatsSpec{Tunnels: []firewall.StatsTunnel{
		{ID: "games", TCP: []int{2053}, UDP: []int{27015}, NAT: true},
		{ID: "main", TCP: []int{443, 2053}, UDP: []int{443}},
	}}, s, "enabled tunnels only, sorted, from the configured listen ports")

	require.Empty(t, StatsSpec(nil, nil).Tunnels)
	require.Empty(t, StatsSpec(config.NewHub("ir-1", "1.2.3.4", 44433), nil).Tunnels)
}

func TestStatsHash(t *testing.T) {
	cfg := fwConfig()
	a := StatsSpec(cfg, nil)
	h := StatsHash(a)
	require.Len(t, h, 16)

	// The seed and the order of the tunnels and ports do not matter.
	b := a
	b.Seed = map[string]firewall.Counter{"tun_main_in": {Bytes: 5}}
	b.Tunnels = []firewall.StatsTunnel{a.Tunnels[1], a.Tunnels[0]}
	b.Tunnels[0].TCP = []int{2053, 443}
	require.Equal(t, h, StatsHash(b))

	// A port, a tunnel or the NAT flag does.
	c := StatsSpec(cfg, func(string) bool { return true })
	require.NotEqual(t, h, StatsHash(c))
	cfg.Tunnels[2].Enabled = true
	require.NotEqual(t, h, StatsHash(StatsSpec(cfg, nil)))
	cfg.Tunnels[2].Enabled = false
	cfg.Tunnels[0].Ports = append(cfg.Tunnels[0].Ports, config.PortMap{Listen: 8443, Proto: config.ProtoTCP})
	require.NotEqual(t, h, StatsHash(StatsSpec(cfg, nil)))
}
