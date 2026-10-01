package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

var testNow = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

// healthyHub is a hub with one UP tunnel on an online, compatible node.
func healthyHub() Facts {
	udpOK := true
	return Facts{
		Role: "hub",
		Now:  testNow,
		Status: api.Status{
			Role: "hub", Version: "1.0.0", GeneratedAt: testNow,
			Tunnels: []api.TunnelInfo{{
				ID: "main", Enabled: true, State: state.StateUp, ActiveNode: "de-1", Nodes: []string{"de-1"},
				ActiveTransport: "backhaul/wssmux",
				Ports:           []api.PortMapDTO{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"}},
			}},
			Nodes: []api.NodeInfo{{ID: "de-1", Online: true, Version: "1.0.3", Compatible: true, UDPOK: &udpOK}},
		},
		DiskFreePct:   map[string]float64{"/": 50, "/var": 40},
		MemAvailPct:   60,
		ClockSkew:     map[string]time.Duration{"de-1": 2 * time.Second},
		BBRActive:     true,
		SysctlProfile: "balanced",
		UnitStates:    map[string]string{systemd.HubUnit: "active", "deyroute-tun@main.de-1.backhaul-wssmux.service": "active"},
		UnitRestarts:  map[string]int{systemd.HubUnit: 0, "deyroute-tun@main.de-1.backhaul-wssmux.service": 1},
		Certs:         []CertExpiry{{Name: "hub.crt", Kind: "hub", NotAfter: testNow.AddDate(5, 0, 0)}},
	}
}

func healthyNode() Facts {
	return Facts{
		Role: "node",
		Now:  testNow,
		Status: api.Status{
			Role: "node", Version: "1.0.0",
			NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8:44433", Connected: true, HubVersion: "1.0.1", Compatible: true},
		},
		BBRActive:     true,
		SysctlProfile: "balanced",
		UnitStates:    map[string]string{systemd.NodeUnit: "active", "deyroute-tun@main.de-1.backhaul-wssmux.service": "active"},
	}
}

func rulesOf(fs []api.DoctorFinding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

func only(t *testing.T, fs []api.DoctorFinding, rule, sev string) api.DoctorFinding {
	t.Helper()
	require.Len(t, fs, 1, "findings: %v", fs)
	require.Equal(t, rule, fs[0].Rule)
	require.Equal(t, sev, fs[0].Severity)
	require.NotEmpty(t, fs[0].Message)
	require.NotEmpty(t, fs[0].Fix)
	require.NotContains(t, fs[0].Message, "%!", "format verbs must match")
	require.NotContains(t, fs[0].Fix, "%!", "format verbs must match")
	return fs[0]
}

func TestHealthyHasNoFindings(t *testing.T) {
	require.Empty(t, Run(healthyHub()))
	require.Empty(t, Run(healthyNode()))
	require.Empty(t, Run(Facts{}), "zero facts are 'not measured' and never a problem")
}

func TestRunDefaultsRoleAndNow(t *testing.T) {
	f := healthyHub()
	f.Role = ""
	f.Now = time.Time{}
	f.UnitStates[systemd.HubUnit] = "failed"
	only(t, Run(f), RuleServiceUnit, SevError) // role taken from Status
}

func TestR01ControlOffline(t *testing.T) {
	f := healthyHub()
	f.Status.Nodes[0].Online = false
	fd := only(t, Run(f), RuleControlOffline, SevWarn)
	require.Contains(t, fd.Message, "de-1")
	require.Contains(t, fd.Message, "main")
	require.Contains(t, fd.Message, "tunnel is healthy")

	// Negative: offline node whose tunnel is DOWN is R02, not R01.
	f.Status.Tunnels[0].State = state.StateDown
	require.Equal(t, []string{RuleIPBlocked}, rulesOf(Run(f)))

	// Node side: not connected to the hub but tunnel units still active.
	n := healthyNode()
	n.Status.NodeSelf.Connected = false
	fd = only(t, Run(n), RuleControlOffline, SevWarn)
	require.Contains(t, fd.Message, "main")
	// Negative: no active tunnel unit.
	n.UnitStates = map[string]string{systemd.NodeUnit: "active", "deyroute-tun@main.canary.service": "active"}
	require.Empty(t, Run(n))
}

func TestR02IPBlocked(t *testing.T) {
	f := healthyHub()
	f.Status.Tunnels[0].State = state.StateDown
	fd := only(t, Run(f), RuleIPBlocked, SevError)
	require.Contains(t, fd.Message, "main")
	require.Contains(t, fd.Message, "probably blocked")
	require.Contains(t, fd.Fix, "deyroute node test de-1")

	// Negative: paused or disabled tunnels are not reported.
	f.Status.Tunnels[0].Paused = true
	require.Empty(t, Run(f))
	f.Status.Tunnels[0].Paused, f.Status.Tunnels[0].Enabled = false, false
	require.Empty(t, Run(f))

	// No active node: the tunnel's node list is used.
	f = healthyHub()
	f.Status.Tunnels[0].State = state.StateDown
	f.Status.Tunnels[0].ActiveNode = ""
	f.Status.Tunnels[0].Nodes = []string{"de-1", "nl-1"}
	fd = only(t, Run(f), RuleIPBlocked, SevError)
	require.Contains(t, fd.Message, "de-1, nl-1")
}

func TestR03ServiceDown(t *testing.T) {
	f := healthyHub()
	f.Status.Tunnels[0].ServiceDown = true
	f.Status.Tunnels[0].Ports = []api.PortMapDTO{{Listen: 27015, Proto: "udp"}, {Listen: 2053, Proto: "tcp"}}
	fd := only(t, Run(f), RuleServiceDown, SevError)
	require.Contains(t, fd.Message, "127.0.0.1:2053")
	require.Contains(t, fd.Fix, "de-1")

	f.Status.Tunnels[0].ServiceDown = false
	require.Empty(t, Run(f))

	require.Equal(t, "?", probeTarget(nil))
	require.Equal(t, "10.0.0.1:53", probeTarget([]api.PortMapDTO{{Listen: 53, Proto: "udp", Target: "10.0.0.1:53"}}))
}

func TestR04FirewallBlocks(t *testing.T) {
	f := healthyHub()
	f.ExternalFirewallBlocks = []string{
		FirewallBlock(44433, "tcp", firewall.UFW),
		FirewallBlock(44433, "tcp", firewall.UFW), // duplicate
		"443/tcp nftables",
		"something odd",
	}
	fs := Run(f)
	require.Equal(t, []string{RuleFirewallBlocks, RuleFirewallBlocks, RuleFirewallBlocks}, rulesOf(fs))
	require.Contains(t, fs[0].Message, "44433/tcp")
	require.Contains(t, fs[0].Fix, "ufw allow 44433/tcp")
	require.Contains(t, fs[1].Fix, "deyroute security firewall show", "nftables has no generic command")
	require.Contains(t, fs[2].Message, "something odd")

	f.ExternalFirewallBlocks = nil
	require.Empty(t, Run(f))

	for _, bad := range []string{"", "443 ufw", "0/tcp ufw", "443/sctp ufw", "443/tcp pf", "x/tcp ufw"} {
		_, _, _, ok := parseFirewallBlock(bad)
		require.False(t, ok, bad)
	}
}

func TestR05PortConflict(t *testing.T) {
	f := healthyHub()
	f.PortConflicts = []string{"443/tcp is used by nginx (pid 1234)"}
	f.Status.Warnings = []api.Warning{
		{Code: string(deyerr.P012), Message: "2053/tcp is used by caddy (pid 99)"},
		{Code: "DEY-F002", Message: "unrelated"},
	}
	fs := Run(f)
	require.Equal(t, []string{RulePortConflict, RulePortConflict}, rulesOf(fs))
	require.Equal(t, SevWarn, fs[0].Severity)
	require.Contains(t, fs[0].Message, "nginx")
	require.Contains(t, fs[1].Message, "caddy")

	f.PortConflicts, f.Status.Warnings = nil, nil
	require.Empty(t, Run(f))
}

func TestR06ServiceUnit(t *testing.T) {
	f := healthyHub()
	f.UnitStates[systemd.HubUnit] = "failed"
	fd := only(t, Run(f), RuleServiceUnit, SevError)
	require.Contains(t, fd.Message, "deyroute-hub.service is failed")
	require.Contains(t, fd.Fix, "systemctl restart deyroute-hub.service")

	// The node unit being inactive on a hub is normal.
	f = healthyHub()
	f.UnitStates[systemd.NodeUnit] = "inactive"
	require.Empty(t, Run(f))

	n := healthyNode()
	n.UnitStates[systemd.NodeUnit] = "inactive"
	only(t, Run(n), RuleServiceUnit, SevError)

	// Unknown role or unknown state: nothing.
	require.Empty(t, ruleServiceUnit(Facts{Role: "other", UnitStates: map[string]string{systemd.HubUnit: "failed"}}))
	require.Empty(t, ruleServiceUnit(Facts{Role: "hub"}))
}

func TestR07CrashLoop(t *testing.T) {
	f := healthyHub()
	f.UnitRestarts["deyroute-tun@main.de-1.backhaul-wssmux.service"] = CrashLoopRestarts
	f.UnitRestarts[systemd.HubUnit] = 50 // not a tunnel unit
	fd := only(t, Run(f), RuleCrashLoop, SevWarn)
	require.Contains(t, fd.Message, "5 restarts")
	require.Contains(t, fd.Fix, "deyroute logs main")

	f.UnitRestarts["deyroute-tun@main.de-1.backhaul-wssmux.service"] = CrashLoopRestarts - 1
	require.Empty(t, Run(f))
}

func TestR08CertExpiry(t *testing.T) {
	f := healthyHub()
	f.Certs = []CertExpiry{
		{Name: "tls/main/cert.pem", Kind: "tunnel", Tunnel: "main", NotAfter: testNow.Add(-time.Hour)},
		{Name: "hub.crt", Kind: "hub", NotAfter: testNow.Add(10 * 24 * time.Hour)},
		{Name: "ca.crt", Kind: "ca", NotAfter: testNow.Add(15 * 24 * time.Hour)}, // fine
		{Name: "zero.crt", Kind: "other"},                                        // unknown
	}
	fs := Run(f)
	require.Equal(t, []string{RuleCertExpiry, RuleCertExpiry}, rulesOf(fs))
	require.Equal(t, SevWarn, fs[0].Severity)
	require.Contains(t, fs[0].Message, "hub.crt expires in 10 days")
	require.Contains(t, fs[0].Fix, "rotate-ca")
	require.Equal(t, SevError, fs[1].Severity)
	require.Contains(t, fs[1].Message, "tls/main/cert.pem expired on 2026-09-30")
	require.Contains(t, fs[1].Fix, "deyroute security tls renew --tunnel main")
}

func TestR09Version(t *testing.T) {
	f := healthyHub()
	f.Status.Nodes[0].Version = "1.1.0"
	fd := only(t, Run(f), RuleVersion, SevError)
	require.Contains(t, fd.Message, "hub 1.0.0")
	require.Contains(t, fd.Message, "de-1 runs 1.1.0")

	f.Status.Nodes[0].Version = "" // unknown
	require.Empty(t, Run(f))
	f.Status.Version = ""
	require.Empty(t, ruleVersion(f))

	n := healthyNode()
	n.Status.NodeSelf.HubVersion = "2.0.0"
	fd = only(t, Run(n), RuleVersion, SevError)
	require.Contains(t, fd.Message, "hub 2.0.0")
}

func TestR10UDPBlocked(t *testing.T) {
	f := healthyHub()
	blocked := false
	f.Status.Nodes[0].UDPOK = &blocked
	fd := only(t, Run(f), RuleUDPBlocked, SevInfo)
	require.Contains(t, fd.Message, "UDP transports are skipped")

	f.Status.Nodes[0].UDPOK = nil // not tested yet
	require.Empty(t, Run(f))
}

func TestR11Tuning(t *testing.T) {
	f := healthyHub()
	f.BBRActive = false
	fd := only(t, Run(f), RuleTuning, SevInfo)
	require.Contains(t, fd.Message, "BBR")

	f.SysctlProfile = "off"
	fd = only(t, Run(f), RuleTuning, SevInfo)
	require.Contains(t, fd.Message, "profile off")
	require.Contains(t, fd.Fix, "deyroute optimize apply --profile balanced")

	f.SysctlProfile = "" // unknown
	require.Empty(t, Run(f))
}

func TestR12Resources(t *testing.T) {
	f := healthyHub()
	f.DiskFreePct = map[string]float64{"/": 5.5, "/var": 9.99, "/etc": 10}
	f.MemAvailPct = 4
	fs := Run(f)
	require.Equal(t, []string{RuleResources, RuleResources, RuleResources}, rulesOf(fs))
	require.Contains(t, fs[0].Message, "Low disk space on /: 5.5% free")
	require.Contains(t, fs[1].Message, "/var")
	require.Contains(t, fs[2].Message, "Low memory: 4.0% available")

	f.DiskFreePct = map[string]float64{"/": 10}
	f.MemAvailPct = 10
	require.Empty(t, Run(f))
}

func TestR13ClockSkew(t *testing.T) {
	f := healthyHub()
	f.ClockSkew = map[string]time.Duration{"de-1": -90 * time.Second, "nl-1": 60 * time.Second}
	fd := only(t, Run(f), RuleClockSkew, SevError)
	require.Contains(t, fd.Message, "de-1")
	require.Contains(t, fd.Message, "1m30s")
	require.Contains(t, fd.Fix, "timedatectl set-ntp true")
}

func TestR14Flapping(t *testing.T) {
	f := healthyHub()
	f.Status.Events = []state.Event{
		{Type: state.EvFlapping, Tunnel: "main", At: testNow.Add(-10 * time.Minute)},
		{Type: state.EvFlapping, Tunnel: "main", At: testNow.Add(-50 * time.Minute)},
		{Type: state.EvFlapping, Tunnel: "games", At: testNow.Add(-2 * time.Hour)}, // too old
		{Type: state.EvTunnelDown, Tunnel: "games", At: testNow},
	}
	fd := only(t, Run(f), RuleFlapping, SevWarn)
	require.Contains(t, fd.Message, "Tunnel main is flapping (2 flapping events")
	require.Contains(t, fd.Fix, "deyroute tunnel pause main")

	// Facts.Events wins over Status.Events.
	f.Events = []state.Event{}
	require.Empty(t, Run(f))
}

func TestR15Secrets(t *testing.T) {
	f := healthyHub()
	f.SecretPermProblems = []string{"/etc/deyroute/secrets/ca.key (0644)", ""}
	f.OldJoinTokens = 2
	fs := Run(f)
	require.Equal(t, []string{RuleSecrets, RuleSecrets}, rulesOf(fs))
	require.Equal(t, SevError, fs[0].Severity)
	require.Contains(t, fs[0].Message, "ca.key (0644)")
	require.Equal(t, SevWarn, fs[1].Severity)
	require.Contains(t, fs[1].Message, "2 join token(s)")
}

func TestRunOrderAndRedaction(t *testing.T) {
	secret := "doctor-rule-secret-value-123"
	dlog.RegisterSecret(secret)
	f := healthyHub()
	f.OldJoinTokens = 1
	f.Status.Tunnels[0].State = state.StateDown
	f.PortConflicts = []string{"443/tcp used by " + secret + "\nsecond line"}
	fs := Run(f)
	require.Equal(t, []string{RuleIPBlocked, RulePortConflict, RuleSecrets}, rulesOf(fs))
	for _, fd := range fs {
		require.NotContains(t, fd.Message, secret)
		require.NotContains(t, fd.Message, "\n")
	}
	require.True(t, strings.Contains(fs[1].Message, "***"))
}
