package config

import (
	"fmt"
	"strings"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

type vcase struct {
	name  string
	base  func() *Config // validHub when nil
	mut   func(c *Config)
	opts  *ValidateOptions
	codes []deyerr.Code
	field string // expected {field} of the first C013, when set
}

func runValidateCases(t *testing.T, cases []vcase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if base == nil {
				base = validHub
			}
			c := base()
			if tc.mut != nil {
				tc.mut(c)
			}
			opts := fakeOpts()
			if tc.opts != nil {
				opts = *tc.opts
			}
			err := c.Validate(opts)
			requireCodes(t, err, tc.codes...)
			if tc.field != "" {
				var found bool
				for _, e := range deyErrors(err) {
					if e.Code == deyerr.C013 && e.Params["field"] == tc.field {
						found = true
					}
				}
				require.True(t, found, "no C013 for field %s in %v", tc.field, err)
			}
			for _, e := range deyErrors(err) {
				// Every rendered line must be complete (no "?" placeholders).
				for _, s := range []string{e.Message(), e.Why(), e.Fix()} {
					require.NotContains(t, s, "?", "unfilled placeholder in %q", s)
				}
			}
		})
	}
}

func tun0(c *Config) *Tunnel { return &c.Tunnels[0] }

func TestValidateBaseConfigs(t *testing.T) {
	require.NoError(t, validHub().Validate(fakeOpts()))
	require.NoError(t, validHub().Validate(ValidateOptions{}))
	require.NoError(t, validNode().Validate(fakeOpts()))
	require.NoError(t, NewHub("ir-1", "5.6.7.8", 0).Validate(fakeOpts()), "a fresh hub without nodes is valid")
}

func TestValidateRoleAndSchema(t *testing.T) {
	runValidateCases(t, []vcase{
		{name: "schema 0", mut: func(c *Config) { c.SchemaVersion = 0 }, codes: []deyerr.Code{deyerr.C019}},
		{name: "schema 2", mut: func(c *Config) { c.SchemaVersion = 2 }, codes: []deyerr.Code{deyerr.C019}},
		{name: "role empty", mut: func(c *Config) { c.Role = "" }, codes: []deyerr.Code{deyerr.C013}, field: "role"},
		{name: "role bogus", mut: func(c *Config) { c.Role = "master" }, codes: []deyerr.Code{deyerr.C013}, field: "role"},
		{name: "hub without hub section", mut: func(c *Config) { c.Hub = nil }, codes: []deyerr.Code{deyerr.C016}},
		{name: "hub with node section", mut: func(c *Config) { c.Node = validNode().Node }, codes: []deyerr.Code{deyerr.C016}},
		{name: "node with hub section", base: validNode, mut: func(c *Config) { c.Hub = validHub().Hub }, codes: []deyerr.Code{deyerr.C016}},
		{name: "node without node section", base: validNode, mut: func(c *Config) { c.Node = nil }, codes: []deyerr.Code{deyerr.C016}},
		{name: "node with nodes", base: validNode, mut: func(c *Config) { c.Nodes = validHub().Nodes }, codes: []deyerr.Code{deyerr.C016}},
		{name: "node with tunnels", base: validNode, mut: func(c *Config) {
			h := validHub()
			c.Nodes, c.Tunnels = h.Nodes, h.Tunnels
		}, codes: []deyerr.Code{deyerr.C016}},
		{name: "node with ladders", base: validNode, mut: func(c *Config) { c.Ladders = BuiltinLadders() }, codes: []deyerr.Code{deyerr.C016}},
		{name: "role hub is fine", mut: func(c *Config) { c.Role = RoleHub }},
	})
}

func TestValidateHub(t *testing.T) {
	cases := []vcase{
		{name: "name empty", mut: func(c *Config) { c.Hub.Name = "" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.name"},
		{name: "name control char", mut: func(c *Config) { c.Hub.Name = "ir\n1" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.name"},
		{name: "name too long", mut: func(c *Config) { c.Hub.Name = fmt.Sprintf("%065d", 1) }, codes: []deyerr.Code{deyerr.C013}, field: "hub.name"},
		{name: "name unicode ok", mut: func(c *Config) { c.Hub.Name = "ایران ۱" }},
		{name: "control_port 0", mut: func(c *Config) { c.Hub.ControlPort = 0 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.control_port"},
		{name: "control_port 65536", mut: func(c *Config) { c.Hub.ControlPort = 65536 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.control_port"},
		{name: "control_port 22", mut: func(c *Config) { c.Hub.ControlPort = 22 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.control_port"},
		{name: "control_port in ctl range", mut: func(c *Config) { c.Hub.ControlPort = 30500 }, codes: []deyerr.Code{deyerr.C013}, field: "hub.control_port"},
		{name: "control_port 1", mut: func(c *Config) { c.Hub.ControlPort = 1 }},
		{name: "control_port 65535", mut: func(c *Config) { c.Hub.ControlPort = 65535 }},
		{name: "public_ip empty", mut: func(c *Config) { c.Hub.PublicIP = "" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip"},
		{name: "public_ip bad", mut: func(c *Config) { c.Hub.PublicIP = "5.6.7" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip"},
		{name: "public_ip zone", mut: func(c *Config) { c.Hub.PublicIP = "fe80::1%eth0" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip"},
		{name: "public_ip v6 ok", mut: func(c *Config) { c.Hub.PublicIP = "2001:db8::1" }},
		{name: "public_ip6 v4", mut: func(c *Config) { c.Hub.PublicIP6 = "1.2.3.4" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip6"},
		{name: "public_ip6 mapped", mut: func(c *Config) { c.Hub.PublicIP6 = "::ffff:1.2.3.4" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip6"},
		{name: "public_ip6 ok", mut: func(c *Config) { c.Hub.PublicIP6 = "2001:db8::2" }},
		{name: "domain bad", mut: func(c *Config) { c.Hub.Domain = "bad_domain" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.domain"},
		{name: "domain ip", mut: func(c *Config) { c.Hub.Domain = "1.2.3.4" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.domain"},
		{name: "domain single label", mut: func(c *Config) { c.Hub.Domain = "localhost" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.domain"},
		{name: "domain numeric tld", mut: func(c *Config) { c.Hub.Domain = "a.123" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.domain"},
		{name: "domain too long", mut: func(c *Config) { c.Hub.Domain = fmt.Sprintf("%0250d.com", 0) }, codes: []deyerr.Code{deyerr.C013}, field: "hub.domain"},
		{name: "domain ok", mut: func(c *Config) { c.Hub.Domain = "tunnel.example.com." }},
		{name: "ui_mode bogus", mut: func(c *Config) { c.Hub.UIMode = "expert" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.ui_mode"},
		{name: "ui_mode empty", mut: func(c *Config) { c.Hub.UIMode = "" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.ui_mode"},
		{name: "language bad", mut: func(c *Config) { c.Hub.Language = "english" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.language"},
		{name: "language ok", mut: func(c *Config) { c.Hub.Language = "en" }},
		{name: "decoy bad", mut: func(c *Config) { c.Hub.DecoySNIs = []string{"www.example.com", "not a host"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.decoy_snis[1]"},
		{name: "mirror ftp", mut: func(c *Config) { c.Hub.Mirror = "ftp://m.example.com" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.mirror"},
		{name: "mirror no host", mut: func(c *Config) { c.Hub.Mirror = "https://" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.mirror"},
		{name: "mirror unparsable", mut: func(c *Config) { c.Hub.Mirror = "https://[::1" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.mirror"},
		{name: "mirror ok", mut: func(c *Config) { c.Hub.Mirror = "https://m.example.com/deyroute" }},
		{name: "update_check ok", mut: func(c *Config) { c.Hub.UpdateCheck = true }},
		{name: "telegram token relative", mut: func(c *Config) { c.Hub.Notify.Telegram.BotTokenFile = "telegram.token" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.bot_token_file"},
		{name: "telegram token empty", mut: func(c *Config) { c.Hub.Notify.Telegram.BotTokenFile = "" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.bot_token_file"},
		{name: "telegram enabled without chat", mut: func(c *Config) { c.Hub.Notify.Telegram.Enabled = true }, codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.chat_id"},
		{name: "telegram chat bad", mut: func(c *Config) { c.Hub.Notify.Telegram.ChatID = "chat me" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.chat_id"},
		{name: "telegram enabled numeric chat", mut: func(c *Config) {
			c.Hub.Notify.Telegram.Enabled = true
			c.Hub.Notify.Telegram.ChatID = "-1001234567890"
		}},
		{name: "telegram channel chat", mut: func(c *Config) { c.Hub.Notify.Telegram.ChatID = "@deyroute_alerts" }},
		{name: "telegram event bogus", mut: func(c *Config) { c.Hub.Notify.Telegram.Events = []string{"down", "explode"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.events[1]"},
		{name: "telegram events empty ok", mut: func(c *Config) { c.Hub.Notify.Telegram.Events = []string{} }},
		{name: "acme email bad", mut: func(c *Config) { c.Hub.ACME = &ACME{Email: "owner"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.acme.email"},
		{name: "acme email with name", mut: func(c *Config) { c.Hub.ACME = &ACME{Email: "Owner <o@example.com>"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.acme.email"},
		{name: "acme renew", mut: func(c *Config) { c.Hub.ACME = &ACME{RenewBeforeDays: 90} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.acme.renew_before_days"},
		{name: "acme renew negative", mut: func(c *Config) { c.Hub.ACME = &ACME{RenewBeforeDays: -1} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.acme.renew_before_days"},
		{name: "acme cf relative", mut: func(c *Config) { c.Hub.ACME = &ACME{CloudflareTokenFile: "cf.token"} }, codes: []deyerr.Code{deyerr.C013}, field: "hub.acme.cloudflare_token_file"},
		{name: "acme ok", mut: func(c *Config) {
			c.Hub.ACME = &ACME{Email: "o@example.com", CloudflareTokenFile: "/etc/deyroute/secrets/cf.token", RenewBeforeDays: 30, Staging: true}
		}},
	}
	for _, m := range UIModes {
		cases = append(cases, vcase{name: "ui_mode " + m, mut: func(c *Config) { c.Hub.UIMode = m }})
	}
	for _, e := range TelegramEventAliases {
		cases = append(cases, vcase{name: "telegram event " + e, mut: func(c *Config) { c.Hub.Notify.Telegram.Events = []string{e} }})
	}
	runValidateCases(t, cases)
}

func TestValidateNodes(t *testing.T) {
	runValidateCases(t, []vcase{
		{name: "id upper", mut: func(c *Config) { c.Nodes[1].ID = "NL-1"; tun0(c).Nodes = []string{"de-1"} }, codes: []deyerr.Code{deyerr.C007}},
		{name: "id too short", mut: func(c *Config) { c.Nodes[1].ID = "n"; tun0(c).Nodes = []string{"de-1"} }, codes: []deyerr.Code{deyerr.C007}},
		{name: "id too long", mut: func(c *Config) { c.Nodes[1].ID = fmt.Sprintf("%033d", 0); tun0(c).Nodes = []string{"de-1"} }, codes: []deyerr.Code{deyerr.C007}},
		{name: "id underscore", mut: func(c *Config) { c.Nodes[1].ID = "nl_1"; tun0(c).Nodes = []string{"de-1"} }, codes: []deyerr.Code{deyerr.C007}},
		{name: "duplicate id", mut: func(c *Config) { c.Nodes[1].ID = "de-1"; tun0(c).Nodes = []string{"de-1"} }, codes: []deyerr.Code{deyerr.C002}},
		{name: "name newline", mut: func(c *Config) { c.Nodes[0].Name = "a\nb" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].name"},
		{name: "public_ip empty", mut: func(c *Config) { c.Nodes[0].PublicIP = "" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].public_ip"},
		{name: "public_ip host", mut: func(c *Config) { c.Nodes[1].PublicIP = "node.example.com" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[nl-1].public_ip"},
		{name: "public_ip path by index", mut: func(c *Config) {
			c.Nodes[1].ID, c.Nodes[1].PublicIP = "NL", "x"
			tun0(c).Nodes = []string{"de-1"}
		}, codes: []deyerr.Code{deyerr.C007, deyerr.C013}, field: "nodes[1].public_ip"},
		{name: "fingerprint md5", mut: func(c *Config) { c.Nodes[0].CertFingerprint = "md5:abc" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].cert_fingerprint"},
		{name: "fingerprint placeholder of the spec sample", mut: func(c *Config) { c.Nodes[0].CertFingerprint = "sha256:..." }},
		{name: "empty tag", mut: func(c *Config) { c.Nodes[0].Tags = []string{"primary", " "} }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].tags[1]"},
	})
}

func TestValidateLadderProfiles(t *testing.T) {
	noRegistry := ValidateOptions{}
	runValidateCases(t, []vcase{
		{name: "profile name invalid", mut: func(c *Config) { c.Ladders["Fast!"] = []string{"backhaul/tcpmux"} }, codes: []deyerr.Code{deyerr.C007}},
		{name: "profile empty", mut: func(c *Config) { c.Ladders["fast"] = nil }, codes: []deyerr.Code{deyerr.C013}, field: "ladders.fast"},
		{name: "profile unknown transport", mut: func(c *Config) { c.Ladders["fast"] = []string{"backhaul/tcpmux", "openvpn/udp"} }, codes: []deyerr.Code{deyerr.C005}},
		{name: "profile bad shape without registry", opts: &noRegistry, mut: func(c *Config) { c.Ladders["fast"] = []string{"Backhaul TCP"} }, codes: []deyerr.Code{deyerr.C005}},
		{name: "profile unknown but no registry", opts: &noRegistry, mut: func(c *Config) { c.Ladders["fast"] = []string{"openvpn/udp"} }},
		{name: "profile duplicate rung", mut: func(c *Config) { c.Ladders["fast"] = []string{"backhaul/tcpmux", "backhaul/tcpmux"} }, codes: []deyerr.Code{deyerr.C013}, field: "ladders.fast"},
		{name: "used empty default profile", mut: func(c *Config) { c.Ladders["default"] = []string{} }, codes: []deyerr.Code{deyerr.C013, deyerr.C009}},
		{name: "no ladders section uses builtin", mut: func(c *Config) { c.Ladders = nil }},
	})
	c := validHub()
	c.Ladders["fast"] = []string{"openvpn/udp"}
	e := firstErr(t, c.Validate(ValidateOptions{KnownTransport: func(string) bool { return false }}))
	require.Contains(t, e.Fix(), "backhaul/udp", "fallback valid list names builtin rungs")
}

func TestValidateTunnelIDsAndNodes(t *testing.T) {
	second := func(c *Config) *Tunnel {
		nt := NewTunnel("game", "Game", []string{"de-1"}, []PortMap{{Listen: 27015, Proto: ProtoUDP}})
		c.Tunnels = append(c.Tunnels, nt)
		return &c.Tunnels[len(c.Tunnels)-1]
	}
	runValidateCases(t, []vcase{
		{name: "second tunnel ok", mut: func(c *Config) { second(c) }},
		{name: "id invalid", mut: func(c *Config) { tun0(c).ID = "Main Tunnel" }, codes: []deyerr.Code{deyerr.C007}},
		{name: "id empty", mut: func(c *Config) { tun0(c).ID = "" }, codes: []deyerr.Code{deyerr.C007}},
		{name: "id duplicate", mut: func(c *Config) { second(c).ID = "main" }, codes: []deyerr.Code{deyerr.C002}},
		{name: "name control", mut: func(c *Config) { tun0(c).Name = "a\tb" }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].name"},
		{name: "no nodes", mut: func(c *Config) { tun0(c).Nodes = nil }, codes: []deyerr.Code{deyerr.C008}},
		{name: "unknown node", mut: func(c *Config) { tun0(c).Nodes = []string{"de-1", "fr-1"} }, codes: []deyerr.Code{deyerr.C010}},
		{name: "duplicate node", mut: func(c *Config) { tun0(c).Nodes = []string{"de-1", "de-1"} }, codes: []deyerr.Code{deyerr.C002}},
	})
}

func TestValidatePorts(t *testing.T) {
	sixtyFive := make([]PortMap, 65)
	for i := range sixtyFive {
		sixtyFive[i] = PortMap{Listen: 2000 + i, Proto: ProtoTCP, Target: DefaultTarget(2000 + i)}
	}
	sixtyFour := sixtyFive[:64]
	extra := fakeOpts()
	extra.ReservedPorts = []int{8080}
	addTunnel := func(c *Config, ports ...PortMap) {
		c.Tunnels = append(c.Tunnels, NewTunnel("second", "", []string{"nl-1"}, ports))
	}
	cases := []vcase{
		{name: "no ports", mut: func(c *Config) { tun0(c).Ports = nil }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports"},
		{name: "64 ports ok", mut: func(c *Config) { tun0(c).Ports = clonePorts(sixtyFour) }},
		{name: "65 ports", mut: func(c *Config) { tun0(c).Ports = clonePorts(sixtyFive) }, codes: []deyerr.Code{deyerr.C015}},
		{name: "listen 0", mut: func(c *Config) { tun0(c).Ports[0].Listen = 0 }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports[0].listen"},
		{name: "listen 65536", mut: func(c *Config) { tun0(c).Ports[0].Listen = 65536 }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports[0].listen"},
		{name: "listen 22", mut: func(c *Config) { tun0(c).Ports[0].Listen = 22 }, codes: []deyerr.Code{deyerr.C011}},
		{name: "listen 22 udp", mut: func(c *Config) { tun0(c).Ports[0] = PortMap{Listen: 22, Proto: ProtoUDP, Target: "127.0.0.1:22"} }, codes: []deyerr.Code{deyerr.C011}},
		{name: "listen control port", mut: func(c *Config) { tun0(c).Ports[0].Listen = DefaultControlPort }, codes: []deyerr.Code{deyerr.C011}},
		{name: "listen ctl low", mut: func(c *Config) { tun0(c).Ports[0].Listen = CtlRangeLow }, codes: []deyerr.Code{deyerr.C011}},
		{name: "listen ctl high", mut: func(c *Config) { tun0(c).Ports[0].Listen = CtlRangeHigh }, codes: []deyerr.Code{deyerr.C011}},
		{name: "listen below ctl", mut: func(c *Config) { tun0(c).Ports[0].Listen = CtlRangeLow - 1 }},
		{name: "listen above ctl", mut: func(c *Config) { tun0(c).Ports[0].Listen = CtlRangeHigh + 1 }},
		{name: "listen extra reserved", opts: &extra, mut: func(c *Config) { tun0(c).Ports[0].Listen = 8080 }, codes: []deyerr.Code{deyerr.C011}},
		{name: "duplicate listen across tunnels", mut: func(c *Config) { addTunnel(c, PortMap{Listen: 443}) }, codes: []deyerr.Code{deyerr.C003}},
		{name: "duplicate listen in tunnel", mut: func(c *Config) { tun0(c).Ports[1].Listen = 443 }, codes: []deyerr.Code{deyerr.C003}},
		{name: "same port other proto", mut: func(c *Config) { addTunnel(c, PortMap{Listen: 443, Proto: ProtoUDP}) }},
		{name: "proto bogus", mut: func(c *Config) { tun0(c).Ports[0].Proto = "sctp" }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports[0].proto"},
		{name: "proto upper", mut: func(c *Config) { tun0(c).Ports[0].Proto = "TCP" }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports[0].proto"},
		{name: "target no port", mut: func(c *Config) { tun0(c).Ports[0].Target = "127.0.0.1" }, codes: []deyerr.Code{deyerr.C004}},
		{name: "target port 0", mut: func(c *Config) { tun0(c).Ports[0].Target = "127.0.0.1:0" }, codes: []deyerr.Code{deyerr.C004}},
		{name: "target port text", mut: func(c *Config) { tun0(c).Ports[0].Target = "127.0.0.1:https" }, codes: []deyerr.Code{deyerr.C004}},
		{name: "target no host", mut: func(c *Config) { tun0(c).Ports[0].Target = ":443" }, codes: []deyerr.Code{deyerr.C004}},
		{name: "target bad host", mut: func(c *Config) { tun0(c).Ports[0].Target = "bad_host:443" }, codes: []deyerr.Code{deyerr.C004}},
		{name: "target empty", mut: func(c *Config) { tun0(c).Ports[0].Target = "" }, codes: []deyerr.Code{deyerr.C004}},
		{name: "target ipv6", mut: func(c *Config) { tun0(c).Ports[0].Target = "[::1]:8443" }},
		{name: "target hostname", mut: func(c *Config) { tun0(c).Ports[0].Target = "panel.local:8443" }},
		{name: "probe bogus", mut: func(c *Config) { tun0(c).Ports[0].Probe = "icmp" }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports[0].probe"},
		{name: "probe empty ok", mut: func(c *Config) { tun0(c).Ports[0].Probe = "" }},
		{name: "udp probe tls", mut: func(c *Config) {
			tun0(c).Ports[0] = PortMap{Listen: 443, Proto: ProtoUDP, Target: "127.0.0.1:443", Probe: ProbeTLS}
		}, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ports[0].probe"},
		{name: "udp probe auto", mut: func(c *Config) {
			tun0(c).Ports[0] = PortMap{Listen: 443, Proto: ProtoUDP, Target: "127.0.0.1:443", Probe: ProbeAuto}
		}},
	}
	for _, p := range ProbeKinds {
		cases = append(cases, vcase{name: "probe " + p, mut: func(c *Config) { tun0(c).Ports[0].Probe = p }})
	}
	for _, p := range Protos {
		cases = append(cases, vcase{name: "proto " + p, mut: func(c *Config) { tun0(c).Ports[0].Proto = p }})
	}
	runValidateCases(t, cases)

	c := validHub()
	tun0(c).Ports[0].Listen = 22
	e := firstErr(t, c.Validate(ValidateOptions{}))
	require.Equal(t, "22/tcp", e.Params["port"])
	require.Contains(t, e.Why(), "SSH")

	c = validHub()
	c.Tunnels = append(c.Tunnels, NewTunnel("second", "", []string{"nl-1"}, []PortMap{{Listen: 2053}}))
	e = firstErr(t, c.Validate(ValidateOptions{}))
	require.Equal(t, deyerr.C003, e.Code)
	require.Equal(t, "Listen port 2053/tcp is used by two tunnels", e.Message())
	require.Contains(t, e.Why(), "main and second")
}

func TestValidateTunnelLadder(t *testing.T) {
	noRegistry := ValidateOptions{}
	udpOnly := func(c *Config) {
		tun0(c).Ports = []PortMap{{Listen: 27015, Proto: ProtoUDP, Target: "127.0.0.1:27015", Probe: ProbeAuto}}
	}
	runValidateCases(t, []vcase{
		{name: "unknown profile", mut: func(c *Config) { tun0(c).Ladder = LadderRef{Name: "turbo"} }, codes: []deyerr.Code{deyerr.C012}},
		{name: "empty ref", mut: func(c *Config) { tun0(c).Ladder = LadderRef{} }, codes: []deyerr.Code{deyerr.C009}},
		{name: "builtin udp-default by name", mut: func(c *Config) { tun0(c).Ladder = LadderRef{Name: DefaultUDPLadderName} }},
		{name: "custom profile", mut: func(c *Config) {
			c.Ladders["fast"] = []string{"backhaul/tcpmux", "direct/native"}
			tun0(c).Ladder = LadderRef{Name: "fast"}
		}},
		{name: "inline ok", mut: func(c *Config) { tun0(c).Ladder = LadderRef{Inline: []string{"rathole/noise", "direct/native"}} }},
		{name: "inline unknown", mut: func(c *Config) { tun0(c).Ladder = LadderRef{Inline: []string{"rathole/noise", "ssh/tunnel"}} }, codes: []deyerr.Code{deyerr.C005}},
		{name: "inline bad shape", opts: &noRegistry, mut: func(c *Config) { tun0(c).Ladder = LadderRef{Inline: []string{"rathole"}} }, codes: []deyerr.Code{deyerr.C005}},
		{name: "inline duplicate", mut: func(c *Config) {
			tun0(c).Ladder = LadderRef{Inline: []string{"direct/native", "direct/native"}}
		}, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].ladder"},
		{name: "udp-only default ladder", mut: udpOnly},
		{name: "udp-only tunnel with tcp rungs is not a config error", mut: func(c *Config) {
			udpOnly(c)
			tun0(c).Ladder = LadderRef{Inline: []string{"backhaul/tcpmux"}}
		}},
	})
}

func TestValidateFailoverAndTLS(t *testing.T) {
	f := func(fn func(f *Failover)) func(c *Config) { return func(c *Config) { fn(&tun0(c).Failover) } }
	cases := []vcase{
		{name: "policy bogus", mut: f(func(f *Failover) { f.Policy = "random" }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.policy"},
		{name: "interval 0", mut: f(func(f *Failover) { f.ProbeIntervalS = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.probe_interval_s"},
		{name: "interval huge", mut: f(func(f *Failover) { f.ProbeIntervalS = 3601 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.probe_interval_s"},
		{name: "timeout 0", mut: f(func(f *Failover) { f.ProbeTimeoutS = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.probe_timeout_s"},
		{name: "timeout > interval", mut: f(func(f *Failover) { f.ProbeTimeoutS = 6 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.probe_timeout_s"},
		{name: "timeout = interval", mut: f(func(f *Failover) { f.ProbeTimeoutS = 5 })},
		{name: "fail 0", mut: f(func(f *Failover) { f.FailThreshold = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.fail_threshold"},
		{name: "recover 0", mut: f(func(f *Failover) { f.RecoverThreshold = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.recover_threshold"},
		{name: "failback after 0", mut: f(func(f *Failover) { f.FailbackAfterS = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.failback_after_s"},
		{name: "failback after > 24h", mut: f(func(f *Failover) { f.FailbackAfterS = 86401 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.failback_after_s"},
		{name: "switches 0", mut: f(func(f *Failover) { f.MaxSwitchesPerHour = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.max_switches_per_hour"},
		{name: "quarantine 0", mut: f(func(f *Failover) { f.QuarantineS = 0 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.quarantine_s"},
		{name: "quarantine > 1h", mut: f(func(f *Failover) { f.QuarantineS = 3601 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].failover.quarantine_s"},
		{name: "failback off ok", mut: f(func(f *Failover) { f.Failback = false })},
		{name: "tls bogus", mut: func(c *Config) { tun0(c).TLS.Mode = "letsencrypt" }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].tls.mode"},
		{name: "tls empty", mut: func(c *Config) { tun0(c).TLS.Mode = "" }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].tls.mode"},
		{name: "tls custom without files", mut: func(c *Config) { tun0(c).TLS.Mode = TLSModeCustom }, codes: []deyerr.Code{deyerr.C013, deyerr.C013}, field: "tunnels[main].tls.cert_file"},
		{name: "tls custom relative key", mut: func(c *Config) {
			tun0(c).TLS = TLS{Mode: TLSModeCustom, CertFile: "/etc/ssl/a.pem", KeyFile: "a.key"}
		}, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].tls.key_file"},
		{name: "tls custom ok", mut: func(c *Config) {
			tun0(c).TLS = TLS{Mode: TLSModeCustom, CertFile: "/etc/ssl/a.pem", KeyFile: "/etc/ssl/a.key"}
		}},
		{name: "tls acme without domain", mut: func(c *Config) { tun0(c).TLS.Mode = TLSModeACME }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].tls.mode"},
		{name: "tls acme with domain", mut: func(c *Config) { tun0(c).TLS.Mode = TLSModeACME; c.Hub.Domain = "t.example.com" }},
	}
	for _, p := range Policies {
		cases = append(cases, vcase{name: "policy " + p, mut: f(func(f *Failover) { f.Policy = p })})
	}
	runValidateCases(t, cases)
}

func TestValidateProbePortAndAdvanced(t *testing.T) {
	a := func(fn func(a *Advanced)) func(c *Config) {
		return func(c *Config) {
			tun0(c).Advanced = &Advanced{}
			fn(tun0(c).Advanced)
		}
	}
	runValidateCases(t, []vcase{
		{name: "probe_port tcp listen", mut: func(c *Config) { tun0(c).ProbePort = 2053 }},
		{name: "probe_port not a listen", mut: func(c *Config) { tun0(c).ProbePort = 8443 }, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].probe_port"},
		{name: "probe_port udp", mut: func(c *Config) {
			tun0(c).Ports = append(tun0(c).Ports, PortMap{Listen: 27015, Proto: ProtoUDP, Target: "127.0.0.1:27015"})
			tun0(c).ProbePort = 27015
		}, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].probe_port"},
		{name: "advanced ok", mut: a(func(a *Advanced) {
			*a = Advanced{ConnectionPool: 16, HysteriaUpMbps: 200, HysteriaDownMbps: 300, HysteriaPortHopping: true, ProxyProtocol: true, BackhaulWebPort: 8081}
		})},
		{name: "pool negative", mut: a(func(a *Advanced) { a.ConnectionPool = -1 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.connection_pool"},
		{name: "pool huge", mut: a(func(a *Advanced) { a.ConnectionPool = 5000 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.connection_pool"},
		{name: "hysteria up", mut: a(func(a *Advanced) { a.HysteriaUpMbps = 200000 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.hysteria_up_mbps"},
		{name: "hysteria down", mut: a(func(a *Advanced) { a.HysteriaDownMbps = -5 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.hysteria_down_mbps"},
		{name: "web port = listen", mut: a(func(a *Advanced) { a.BackhaulWebPort = 443 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.backhaul_web_port"},
		{name: "web port ctl range", mut: a(func(a *Advanced) { a.BackhaulWebPort = 30001 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.backhaul_web_port"},
		{name: "web port control", mut: a(func(a *Advanced) { a.BackhaulWebPort = DefaultControlPort }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.backhaul_web_port"},
		{name: "web port range", mut: a(func(a *Advanced) { a.BackhaulWebPort = 70000 }), codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].advanced.backhaul_web_port"},
	})
}

func TestValidateTuning(t *testing.T) {
	cases := []vcase{
		{name: "profile bogus", mut: func(c *Config) { c.Tuning.SysctlProfile = "max" }, codes: []deyerr.Code{deyerr.C013}, field: "tuning.sysctl_profile"},
		{name: "profile empty", mut: func(c *Config) { c.Tuning.SysctlProfile = "" }, codes: []deyerr.Code{deyerr.C013}, field: "tuning.sysctl_profile"},
		{name: "no tuning section", mut: func(c *Config) { c.Tuning, c.Security = nil, nil }},
	}
	for _, p := range SysctlProfiles {
		cases = append(cases, vcase{name: "profile " + p, mut: func(c *Config) { c.Tuning.SysctlProfile = p }})
		cases = append(cases, vcase{name: "node profile " + p, base: validNode, mut: func(c *Config) { c.Tuning.SysctlProfile = p }})
	}
	runValidateCases(t, cases)
}

func TestValidateNodeConfig(t *testing.T) {
	n := func(fn func(n *NodeSelf)) func(c *Config) { return func(c *Config) { fn(c.Node) } }
	runValidateCases(t, []vcase{
		{name: "id invalid", base: validNode, mut: n(func(n *NodeSelf) { n.ID = "DE 1" }), codes: []deyerr.Code{deyerr.C007}},
		{name: "hub_addr no port", base: validNode, mut: n(func(n *NodeSelf) { n.HubAddr = "5.6.7.8" }), codes: []deyerr.Code{deyerr.C013}, field: "node.hub_addr"},
		{name: "hub_addr empty", base: validNode, mut: n(func(n *NodeSelf) { n.HubAddr = "" }), codes: []deyerr.Code{deyerr.C013}, field: "node.hub_addr"},
		{name: "hub_addr port 0", base: validNode, mut: n(func(n *NodeSelf) { n.HubAddr = "5.6.7.8:0" }), codes: []deyerr.Code{deyerr.C013}, field: "node.hub_addr"},
		{name: "hub_addr host ok", base: validNode, mut: n(func(n *NodeSelf) { n.HubAddr = "hub.example.com:44433" })},
		{name: "hub_addr v6 ok", base: validNode, mut: n(func(n *NodeSelf) { n.HubAddr = "[2001:db8::1]:44433" })},
		{name: "fingerprint short", base: validNode, mut: n(func(n *NodeSelf) { n.HubCAFingerprint = "sha256:abcd" }), codes: []deyerr.Code{deyerr.C013}, field: "node.hub_ca_fingerprint"},
		{name: "fingerprint upper", base: validNode, mut: n(func(n *NodeSelf) { n.HubCAFingerprint = "sha256:" + fmt.Sprintf("%064X", 255) }), codes: []deyerr.Code{deyerr.C013}, field: "node.hub_ca_fingerprint"},
		{name: "fingerprint no prefix", base: validNode, mut: n(func(n *NodeSelf) { n.HubCAFingerprint = testFP[7:] }), codes: []deyerr.Code{deyerr.C013}, field: "node.hub_ca_fingerprint"},
		{name: "cert relative", base: validNode, mut: n(func(n *NodeSelf) { n.CertFile = "node.crt" }), codes: []deyerr.Code{deyerr.C013}, field: "node.cert_file"},
		{name: "key empty", base: validNode, mut: n(func(n *NodeSelf) { n.KeyFile = "" }), codes: []deyerr.Code{deyerr.C013}, field: "node.key_file"},
	})
}

func TestValidateReportsEverything(t *testing.T) {
	c := validHub()
	c.Hub.UIMode = "x"
	c.Nodes[0].ID = "BAD"
	tun0(c).Ports[0].Listen = 22
	tun0(c).Ports[1].Target = "nowhere"
	tun0(c).Ladder = LadderRef{Name: "nope"}
	err := c.Validate(fakeOpts())
	// C010 for de-1: the node id is now invalid, so the reference is unknown.
	requireCodes(t, err, deyerr.C013, deyerr.C007, deyerr.C010, deyerr.C011, deyerr.C004, deyerr.C012)
	for _, e := range deyErrors(err) {
		require.True(t, deyerr.ValidCode(e.Code))
		_, ok := deyerr.Lookup(e.Code)
		require.True(t, ok, "code %s is in the catalog", e.Code)
	}
}

func TestReservedListen(t *testing.T) {
	for _, tc := range []struct {
		port, cp int
		extra    []int
		want     bool
	}{
		{22, 44433, nil, true}, {44433, 44433, nil, true}, {30000, 0, nil, true}, {31999, 0, nil, true},
		{29999, 0, nil, false}, {32000, 0, nil, false}, {443, 0, nil, false}, {8080, 0, []int{8080}, true},
		{44433, 0, nil, false},
	} {
		got, why := ReservedListen(tc.port, tc.cp, tc.extra)
		require.Equal(t, tc.want, got, "port %d", tc.port)
		require.Equal(t, tc.want, why != "")
	}
}

func TestValidHelpers(t *testing.T) {
	require.True(t, ValidFingerprint(testFP))
	require.False(t, ValidFingerprint("sha256:..."))
	require.True(t, ValidHostPort("127.0.0.1:443"))
	require.True(t, ValidHostPort("localhost:80"))
	require.False(t, ValidHostPort("localhost"))
	require.False(t, ValidHostPort("-bad-:80"))
	require.False(t, ValidHostPort("1.2.3.999:80"))
	require.True(t, ValidDomain("example.com"))
	require.False(t, ValidDomain("example"))
	require.False(t, ValidDomain("::1"))
	require.False(t, ValidDomain(""))
}

func TestValidateNil(t *testing.T) {
	requireCodes(t, (*Config)(nil).Validate(ValidateOptions{}), deyerr.C016)
}

func TestValidatePublicIPs(t *testing.T) {
	var cases []vcase
	for _, ip := range []string{"0.0.0.0", "::", "224.0.0.1", "ff02::1", "::ffff:1.2.3.4", "1.2.3.4/32", " 1.2.3.4"} {
		cases = append(cases,
			vcase{name: "hub " + ip, mut: func(c *Config) { c.Hub.PublicIP = ip }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip"},
			vcase{name: "node " + ip, mut: func(c *Config) { c.Nodes[1].PublicIP = ip }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[nl-1].public_ip"},
		)
	}
	for _, ip := range []string{"::", "ff02::1", "fe80::1%eth0"} {
		cases = append(cases, vcase{name: "hub6 " + ip, mut: func(c *Config) { c.Hub.PublicIP6 = ip }, codes: []deyerr.Code{deyerr.C013}, field: "hub.public_ip6"})
	}
	for _, ip := range []string{"10.1.2.3", "192.168.1.1", "127.0.0.1", "2001:db8::5"} {
		cases = append(cases, vcase{name: "ok " + ip, mut: func(c *Config) { c.Nodes[1].PublicIP = ip }})
	}
	runValidateCases(t, cases)
}

// TestValidateHidesSecrets: a secret pasted where its file path belongs must
// not be echoed by DEY-C013 (message, why, fix, format or Error()).
func TestValidateHidesSecrets(t *testing.T) {
	const token = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	const pem = "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIJ\n-----END PRIVATE KEY-----"
	cases := []struct {
		name, field string
		base        func() *Config
		mut         func(c *Config)
	}{
		{"telegram token", "hub.notify.telegram.bot_token_file", validHub, func(c *Config) { c.Hub.Notify.Telegram.BotTokenFile = token }},
		{"cloudflare token", "hub.acme.cloudflare_token_file", validHub, func(c *Config) { c.Hub.ACME = &ACME{CloudflareTokenFile: token} }},
		{"tls key", "tunnels[main].tls.key_file", validHub, func(c *Config) {
			tun0(c).TLS = TLS{Mode: TLSModeCustom, CertFile: "/etc/ssl/a.pem", KeyFile: pem}
		}},
		{"node key", "node.key_file", validNode, func(c *Config) { c.Node.KeyFile = pem }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.base()
			tc.mut(c)
			err := c.Validate(fakeOpts())
			requireCodes(t, err, deyerr.C013)
			e := firstErr(t, err)
			require.Equal(t, tc.field, e.Params["field"])
			require.Equal(t, hiddenValue, e.Params["value"])
			for _, s := range []string{err.Error(), e.Format(true)} {
				require.NotContains(t, s, "AAHdqTcv")
				require.NotContains(t, s, "PRIVATE KEY")
			}
		})
	}
	// An empty required secret path is shown as empty, not hidden.
	c := validHub()
	c.Hub.Notify.Telegram.BotTokenFile = ""
	require.Equal(t, "", firstErr(t, c.Validate(ValidateOptions{})).Params["value"])
}

func TestValidatePathControlChars(t *testing.T) {
	runValidateCases(t, []vcase{
		{name: "cert newline", mut: func(c *Config) {
			tun0(c).TLS = TLS{Mode: TLSModeCustom, CertFile: "/etc/ssl/a.pem\nExecStartPre=/bin/x", KeyFile: "/etc/ssl/a.key"}
		}, codes: []deyerr.Code{deyerr.C013}, field: "tunnels[main].tls.cert_file"},
		{name: "node cert tab", base: validNode, mut: func(c *Config) { c.Node.CertFile = "/etc/deyroute/\tnode.crt" }, codes: []deyerr.Code{deyerr.C013}, field: "node.cert_file"},
		{name: "token file escape", mut: func(c *Config) { c.Hub.Notify.Telegram.BotTokenFile = "/etc/deyroute/\x1b[31m" }, codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.bot_token_file"},
	})
}

func TestValidateTelegramEventNames(t *testing.T) {
	var cases []vcase
	for _, e := range TelegramEventNames {
		cases = append(cases, vcase{name: "event name " + e, mut: func(c *Config) { c.Hub.Notify.Telegram.Events = []string{e} }})
	}
	for _, e := range []string{"Down", " down", "tunnel-down", "acme", ""} {
		cases = append(cases, vcase{name: "bad event " + e, mut: func(c *Config) { c.Hub.Notify.Telegram.Events = []string{e} },
			codes: []deyerr.Code{deyerr.C013}, field: "hub.notify.telegram.events[0]"})
	}
	runValidateCases(t, cases)
	require.True(t, ValidTelegramEvent("acme_failed"))
	require.True(t, ValidTelegramEvent("switch"))
	require.False(t, ValidTelegramEvent("SWITCH"))
}

func TestValidateCertFingerprintForms(t *testing.T) {
	runValidateCases(t, []vcase{
		{name: "empty ok", mut: func(c *Config) { c.Nodes[0].CertFingerprint = "" }},
		{name: "placeholder ok", mut: func(c *Config) { c.Nodes[0].CertFingerprint = SpecPlaceholderFingerprint }},
		{name: "full ok", mut: func(c *Config) { c.Nodes[0].CertFingerprint = testFP }},
		{name: "short", mut: func(c *Config) { c.Nodes[0].CertFingerprint = "sha256:abcd" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].cert_fingerprint"},
		{name: "garbage after prefix", mut: func(c *Config) { c.Nodes[0].CertFingerprint = "sha256:\nx" }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].cert_fingerprint"},
		{name: "bare hex", mut: func(c *Config) { c.Nodes[0].CertFingerprint = testFP[7:] }, codes: []deyerr.Code{deyerr.C013}, field: "nodes[de-1].cert_fingerprint"},
	})
	// The node's own pin never accepts the placeholder.
	c := validNode()
	c.Node.HubCAFingerprint = SpecPlaceholderFingerprint
	requireCodes(t, c.Validate(ValidateOptions{}), deyerr.C013)
}

func TestValidateCanonicalTargetPort(t *testing.T) {
	var cases []vcase
	for _, tg := range []string{"127.0.0.1:+443", "127.0.0.1:0443", "127.0.0.1:443 ", "127.0.0.1: 443", "[::1]:-1"} {
		cases = append(cases, vcase{name: "target " + tg, mut: func(c *Config) { tun0(c).Ports[0].Target = tg }, codes: []deyerr.Code{deyerr.C004}})
	}
	cases = append(cases, vcase{name: "hub_addr +port", base: validNode, mut: func(c *Config) { c.Node.HubAddr = "5.6.7.8:+44433" },
		codes: []deyerr.Code{deyerr.C013}, field: "node.hub_addr"})
	runValidateCases(t, cases)
}

func TestValidateEmptyInlineLadder(t *testing.T) {
	runValidateCases(t, []vcase{
		{name: "explicit empty inline", mut: func(c *Config) { tun0(c).Ladder = LadderRef{Inline: []string{}} }, codes: []deyerr.Code{deyerr.C009}},
	})
	c := validHub()
	tun0(c).Ladder = LadderRef{Inline: []string{}}
	c.ApplyDefaults()
	require.Equal(t, []string{}, tun0(c).Ladder.Inline, "ApplyDefaults keeps an explicit empty list")
}

func TestValidateBackhaulWebPortUnique(t *testing.T) {
	c := validHub()
	c.Tunnels = append(c.Tunnels, NewTunnel("second", "", []string{"nl-1"}, []PortMap{{Listen: 8443}}))
	c.Tunnels[0].Advanced = &Advanced{BackhaulWebPort: 8081}
	c.Tunnels[1].Advanced = &Advanced{BackhaulWebPort: 8081}
	err := c.Validate(fakeOpts())
	requireCodes(t, err, deyerr.C013)
	require.Equal(t, "tunnels[second].advanced.backhaul_web_port", firstErr(t, err).Params["field"])
	c.Tunnels[1].Advanced.BackhaulWebPort = 8082
	require.NoError(t, c.Validate(fakeOpts()))
}

func TestValidateDuplicateListenSameTunnelWording(t *testing.T) {
	c := validHub()
	tun0(c).Ports[1].Listen = 443
	tun0(c).Ports[1].Target = "127.0.0.1:443"
	e := firstErr(t, c.Validate(fakeOpts()))
	require.Equal(t, deyerr.C003, e.Code)
	require.Equal(t, "tunnel main lists 443/tcp twice; only one process can bind a port", e.Why())
}

// TestErrorParamsArePrintable: control characters from config.yaml are
// escaped in error parameters so the three-line block stays intact and no
// terminal escape sequence reaches the screen.
func TestErrorParamsArePrintable(t *testing.T) {
	c := validHub()
	c.Hub.Name = "ir\x1b[2J1"
	tun0(c).Ports[0].Target = "evil\nWhy: fake"
	tun0(c).Ladder = LadderRef{Name: "x\ry"}
	c.Nodes[0].Tags = []string{"a\tb"}
	err := c.Validate(fakeOpts())
	requireCodes(t, err, deyerr.C013, deyerr.C004, deyerr.C012, deyerr.C013)
	for _, e := range deyErrors(err) {
		out := e.Format(true)
		require.Equal(t, 4, strings.Count(out, "\n"), "one line each for message/why/fix/log: %q", out)
		require.NotContains(t, out, "\x1b")
		require.NotContains(t, out, "\r")
	}
	require.Equal(t, `ir\x1b[2J1`, firstErr(t, err).Params["value"])

	_, err = Parse([]byte(miniHub + "    \"bad\\nkey\": 1\n"))
	requireCodes(t, err, deyerr.C001)
	require.Equal(t, `tunnels[0].bad\nkey`, firstErr(t, err).Params["key"])
}

func TestDuplicateTunnelNodeFix(t *testing.T) {
	c := validHub()
	tun0(c).Nodes = []string{"de-1", "de-1"}
	e := firstErr(t, c.Validate(fakeOpts()))
	require.Equal(t, deyerr.C002, e.Code)
	require.Contains(t, e.Fix(), "list node de-1 only once")
}

func TestPrintableParamsInvalidUTF8(t *testing.T) {
	e := printableParams(deyerr.New(deyerr.C013, deyerr.Params{"value": "a\xffb", "n": 3}))
	require.Equal(t, `a\xffb`, e.Params["value"])
	require.Equal(t, 3, e.Params["n"])
}
