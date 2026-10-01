package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewHubDefaults(t *testing.T) {
	c := NewHub("ir-1", "5.6.7.8", 0)
	require.Equal(t, &Config{
		SchemaVersion: SchemaVersion,
		Role:          RoleHub,
		Hub: &Hub{
			Name: "ir-1", ControlPort: DefaultControlPort, PublicIP: "5.6.7.8", UIMode: UIModeSimple,
			Notify: Notify{Telegram: Telegram{
				BotTokenFile: "/etc/deyroute/secrets/telegram.token",
				Events:       []string{"down", "switch", "failback", "node_offline"},
			}},
		},
		Ladders:  map[string][]string{"default": DefaultLadder},
		Tuning:   &Tuning{SysctlProfile: SysctlBalanced, BBR: true},
		Security: &Security{FirewallManaged: true, RestrictControlToNodes: true},
	}, c)
	require.Equal(t, 50000, NewHub("x", "1.1.1.1", 50000).Hub.ControlPort)

	// The events and ladder are copies of the package defaults.
	c.Hub.Notify.Telegram.Events[0] = "x"
	c.Ladders["default"][0] = "x/y"
	require.Equal(t, "down", DefaultTelegramEvents[0])
	require.Equal(t, "backhaul/wssmux", DefaultLadder[0])
}

func TestNewNodeDefaults(t *testing.T) {
	c := NewNode("de-1", "5.6.7.8:44433", testFP)
	require.Equal(t, RoleNode, c.Role)
	require.Equal(t, DefaultNodeCertFile, c.Node.CertFile)
	require.Equal(t, DefaultNodeKeyFile, c.Node.KeyFile)
	require.Nil(t, c.Ladders)
	require.NoError(t, c.Validate(ValidateOptions{}))
}

func TestNewTunnelDefaults(t *testing.T) {
	nodes := []string{"de-1"}
	ports := []PortMap{{Listen: 443}, {Listen: 27015, Proto: ProtoUDP, Target: "10.0.0.1:1"}}
	tun := NewTunnel("main", "", nodes, ports)
	require.Equal(t, Tunnel{
		ID: "main", Name: "main", Enabled: true, Nodes: []string{"de-1"},
		Ports: []PortMap{
			{Listen: 443, Proto: ProtoTCP, Target: "127.0.0.1:443", Probe: ProbeAuto},
			{Listen: 27015, Proto: ProtoUDP, Target: "10.0.0.1:1", Probe: ProbeAuto},
		},
		Ladder: LadderRef{Name: "default"},
		Failover: Failover{
			Policy: PolicyTransportThenNode, ProbeIntervalS: 5, ProbeTimeoutS: 3, FailThreshold: 3,
			RecoverThreshold: 6, Failback: true, FailbackAfterS: 300, MaxSwitchesPerHour: 6, QuarantineS: 600,
		},
		TLS: TLS{Mode: TLSModeAuto},
	}, tun)
	nodes[0] = "changed"
	require.Equal(t, "", ports[0].Target, "inputs are copied, not modified")
	require.Equal(t, "de-1", tun.Nodes[0])
}

func TestApplyDefaults(t *testing.T) {
	c := &Config{
		Role: RoleHub,
		Hub:  &Hub{Name: "ir-1", PublicIP: "5.6.7.8", Notify: Notify{Telegram: Telegram{Events: []string{}}}},
		Tunnels: []Tunnel{{
			ID: "main", Nodes: []string{"de-1"},
			Ports:    []PortMap{{Listen: 443}, {Listen: 0}},
			Failover: Failover{Policy: PolicyNodeOnly, FailThreshold: 9},
			TLS:      TLS{Mode: TLSModeCustom},
		}},
		Tuning: &Tuning{},
		Node:   &NodeSelf{ID: "x"},
	}
	c.ApplyDefaults()
	require.Equal(t, SchemaVersion, c.SchemaVersion)
	require.Equal(t, DefaultControlPort, c.Hub.ControlPort)
	require.Equal(t, UIModeSimple, c.Hub.UIMode)
	require.Equal(t, []string{}, c.Hub.Notify.Telegram.Events, "an explicit empty event list is kept")
	require.Equal(t, DefaultLadder, c.Ladders["default"])

	tun := c.Tunnels[0]
	require.Equal(t, "main", tun.Name)
	require.Equal(t, PolicyNodeOnly, tun.Failover.Policy, "explicit values are kept")
	require.Equal(t, 9, tun.Failover.FailThreshold)
	require.Equal(t, DefaultQuarantineS, tun.Failover.QuarantineS)
	require.False(t, tun.Failover.Failback, "booleans are never defaulted by ApplyDefaults")
	require.False(t, tun.Enabled)
	require.Equal(t, TLSModeCustom, tun.TLS.Mode)
	require.Equal(t, "127.0.0.1:443", tun.Ports[0].Target)
	require.Equal(t, "", tun.Ports[1].Target, "no target is invented for an invalid listen")
	require.Equal(t, LadderRef{Name: "default"}, tun.Ladder)

	require.Equal(t, &Tuning{SysctlProfile: SysctlBalanced}, c.Tuning, "bbr is not forced on an existing section")
	require.Equal(t, DefaultSecurity(), c.Security)
	require.Equal(t, DefaultNodeCertFile, c.Node.CertFile)
	require.Equal(t, DefaultNodeKeyFile, c.Node.KeyFile)

	// Idempotent.
	cp := Clone(c)
	c.ApplyDefaults()
	require.Equal(t, cp, c)

	// Existing ladders are not extended with the builtin default.
	c2 := &Config{Role: RoleHub, Ladders: map[string][]string{"fast": {"direct/native"}}}
	c2.ApplyDefaults()
	require.Equal(t, map[string][]string{"fast": {"direct/native"}}, c2.Ladders)
}

func TestDefaultTarget(t *testing.T) {
	require.Equal(t, "127.0.0.1:2053", DefaultTarget(2053))
}
