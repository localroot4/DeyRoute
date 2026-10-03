package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
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
	f.BBRAvailable, f.BBRApplied = true, true
	f.SysctlProfile = "aggressive"
	fd := only(t, Run(f), RuleTuning, SevInfo)
	require.Equal(t, "BBR is set by the aggressive profile but the kernel does not use it", fd.Message)
	require.Contains(t, fd.Fix, "deyroute optimize apply --profile aggressive")

	// No tcp_bbr in this kernel, or the profile was applied without BBR
	// (tuning.bbr false): applying it again cannot help, so no finding.
	f.BBRAvailable = false
	require.Empty(t, Run(f))
	f.BBRAvailable, f.BBRApplied = true, false
	require.Empty(t, Run(f))

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
	f.ExpiredJoinTokens = 2
	f.LongJoinTokens, f.LongJoinUntil = 1, testNow.Add(90*time.Minute)
	fs := Run(f)
	require.Equal(t, []string{RuleSecrets, RuleSecrets, RuleSecrets}, rulesOf(fs))
	require.Equal(t, SevError, fs[0].Severity)
	require.Contains(t, fs[0].Message, "ca.key (0644)")
	require.Equal(t, SevWarn, fs[1].Severity)
	require.Contains(t, fs[1].Message, "2 expired join token(s) could not be removed")
	require.NotContains(t, fs[1].Fix, "restart the hub", "a restart does not remove them")
	// A valid long-TTL token is the owner's choice: info with its expiry.
	require.Equal(t, SevInfo, fs[2].Severity)
	require.Equal(t, "1 join token(s) made with a TTL over 15 minutes are valid until 2026-09-30 11:30 UTC: until then the control port accepts every address", fs[2].Message)

	f.SecretPermProblems, f.ExpiredJoinTokens = nil, 0
	f.LongJoinUntil = testNow.Add(-time.Minute) // expired meanwhile
	require.Empty(t, Run(f))
}

func TestRunOrderAndRedaction(t *testing.T) {
	secret := "doctor-rule-secret-value-123"
	dlog.RegisterSecret(secret)
	f := healthyHub()
	f.ExpiredJoinTokens = 1
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

// ---------------------------------------------------------------- tuning and monitoring (R11, R12)

func TestR11TuneOverride(t *testing.T) {
	f := healthyHub()
	f.Tune.Drift = []api.TuneDrift{
		{Key: "net.core.rmem_max", Want: "16777216", Live: "16777216", OverriddenBy: "/etc/sysctl.conf"},
		{Key: "net.core.wmem_max", Want: "16777216", Live: "16777216", OverriddenBy: "/etc/sysctl.conf"},
		{Key: "net.core.somaxconn", Want: "65535", Live: "4096", OverriddenBy: "/etc/sysctl.d/99-zz-local.conf"},
	}
	fs := Run(f)
	require.Equal(t, []string{RuleTuning, RuleTuning}, rulesOf(fs), "one finding per overriding file")
	require.Equal(t, SevWarn, fs[0].Severity)
	require.Contains(t, fs[0].Message, "/etc/sysctl.conf sets net.core.rmem_max, net.core.wmem_max again at boot")
	require.Contains(t, fs[0].Fix, "remove these lines from /etc/sysctl.conf")
	require.Contains(t, fs[0].Fix, "deyroute optimize check")
	require.Contains(t, fs[1].Message, "/etc/sysctl.d/99-zz-local.conf sets net.core.somaxconn")
}

func TestR11TuneDrift(t *testing.T) {
	f := healthyHub()
	f.SysctlProfile = config.SysctlAuto
	f.Tune.Profile = config.SysctlAuto
	f.Tune.Drift = []api.TuneDrift{{Key: "net.ipv4.tcp_fin_timeout", Want: "15", Live: "60"}}
	fd := only(t, Run(f), RuleTuning, SevWarn)
	require.Contains(t, fd.Message, "1 tuned kernel setting(s) changed after deyroute applied them: net.ipv4.tcp_fin_timeout is 60, deyroute set 15")
	require.Equal(t, "apply the tuning again: deyroute optimize auto; then check: deyroute optimize check", fd.Fix)

	// A fixed profile names its own apply command.
	f.Tune.Profile = config.SysctlBalanced
	require.Contains(t, only(t, Run(f), RuleTuning, SevWarn).Fix, "deyroute optimize apply --profile balanced")

	// A limit key lowered by someone else is drift; raised further is not.
	f.Tune.Drift = []api.TuneDrift{{Key: "net.core.somaxconn", Want: "65535", Live: "4096"}}
	require.Contains(t, only(t, Run(f), RuleTuning, SevWarn).Message, "net.core.somaxconn is 4096")
	f.Tune.Drift = []api.TuneDrift{
		{Key: "net.core.somaxconn", Want: "65535", Live: "131072"},
		{Key: "net.ipv4.tcp_rmem", Want: "4096 131072 33554432", Live: "4096 262144 67108864"},
	}
	require.Empty(t, Run(f), "raise-only keys raised further are the owner's choice")
	// tcp_rmem with one field lower is drift.
	f.Tune.Drift = []api.TuneDrift{{Key: "net.ipv4.tcp_rmem", Want: "4096 131072 33554432", Live: "4096 87380 67108864"}}
	only(t, Run(f), RuleTuning, SevWarn)

	// Keys the kernel lacks and BBR (reported by R11 itself) are no drift.
	f.Tune.Drift = []api.TuneDrift{
		{Key: "net.netfilter.nf_conntrack_max", Want: "262144", Live: ""},
		{Key: "net.ipv4.tcp_congestion_control", Want: "bbr", Live: "cubic"},
	}
	require.Empty(t, Run(f))

	// Many keys: three are named, the rest counted.
	f.Tune.Drift = nil
	for _, k := range []string{"a.b", "c.d", "e.f", "g.h", "i.j"} {
		f.Tune.Drift = append(f.Tune.Drift, api.TuneDrift{Key: "net." + k, Want: "1", Live: "0"})
	}
	fd = only(t, Run(f), RuleTuning, SevWarn)
	require.Contains(t, fd.Message, "5 tuned kernel setting(s)")
	require.Contains(t, fd.Message, "net.e.f is 0, deyroute set 1; and 2 more")
	require.NotContains(t, fd.Message, "net.g.h")
}

func TestR11Nofile(t *testing.T) {
	f := healthyHub()
	f.Tune.NROpen = 524288
	f.Tune.Nofile = []NofileLimit{
		{Unit: systemd.TunTemplate, Limit: 1048576},
		{Unit: systemd.HubUnit, Limit: 524288},
		{Unit: systemd.HubUnit, PID: 812, Limit: 1048576},
		{Unit: systemd.TunTemplate, Limit: 1048576},
	}
	fd := only(t, Run(f), RuleTuning, SevWarn)
	require.Contains(t, fd.Message, "above fs.nr_open (524288)")
	require.Contains(t, fd.Message, "deyroute-tun@.service LimitNOFILE=1048576")
	require.Contains(t, fd.Message, "deyroute-hub.service (pid 812) runs with 1048576")
	require.Equal(t, 1, strings.Count(fd.Message, "deyroute-tun@"), "one entry per unit")
	require.Contains(t, fd.Fix, "deyroute optimize auto")

	// Negative: at or below nr_open, or nr_open unknown.
	f.Tune.NROpen = 1048576
	require.Empty(t, Run(f))
	f.Tune.NROpen = 0
	require.Empty(t, Run(f))
}

func TestR11Qdisc(t *testing.T) {
	f := healthyHub()
	f.Tune.QdiscFQ, f.Tune.NIC, f.Tune.Qdisc = true, "eth0", "fq_codel"
	fd := only(t, Run(f), RuleTuning, SevInfo)
	require.Contains(t, fd.Message, "eth0 still uses the fq_codel queue")
	require.Contains(t, fd.Message, "after the next reboot")
	require.Contains(t, fd.Fix, "deyroute optimize check")

	for _, q := range []string{"fq", "mq", "noqueue", ""} {
		f.Tune.Qdisc = q
		require.Empty(t, Run(f), q)
	}
	f.Tune.Qdisc, f.Tune.QdiscFQ = "pfifo_fast", false
	require.Empty(t, Run(f), "fq is not deyroute's default here")
}

func TestR11UDPBuffers(t *testing.T) {
	f := healthyHub()
	f.Tune.UDPRungs, f.Tune.RmemMax = true, 212992
	fd := only(t, Run(f), RuleTuning, SevWarn)
	require.Contains(t, fd.Message, "net.core.rmem_max is only 208 KiB")
	require.Equal(t, "raise the UDP buffers: deyroute optimize auto", fd.Fix)

	f.Tune.RmemMax = 16 << 20
	require.Empty(t, Run(f))
	f.Tune.RmemMax = 7 << 20
	require.Empty(t, Run(f), "7 MiB is enough")
	f.Tune.UDPRungs, f.Tune.RmemMax = false, 212992
	require.Empty(t, Run(f), "no UDP rungs")
	f.Tune.UDPRungs, f.Tune.RmemMax = true, 0
	require.Empty(t, Run(f), "unknown")

	// In a container the provider decides.
	f.Tune.RmemMax, f.Tune.Virt = 212992, "lxc"
	fs := Run(f)
	require.Equal(t, []string{RuleTuning, RuleTuning}, rulesOf(fs))
	require.Contains(t, fs[0].Fix, "lxc container")
	require.Equal(t, SevInfo, fs[1].Severity)
}

func TestR11Container(t *testing.T) {
	f := healthyNode()
	f.Tune.Virt = "openvz"
	fd := only(t, Run(f), RuleTuning, SevInfo)
	require.Contains(t, fd.Message, "openvz container: kernel tuning is skipped")
	require.Contains(t, fd.Fix, "deyroute optimize auto")
}

func TestR12Conntrack(t *testing.T) {
	f := healthyHub()
	f.Tune.ConntrackMax = 1000
	for _, tc := range []struct {
		count int
		sev   string
	}{{800, ""}, {801, SevWarn}, {950, SevWarn}, {951, SevError}, {1000, SevError}} {
		f.Tune.ConntrackCount = tc.count
		if tc.sev == "" {
			require.Empty(t, Run(f), tc.count)
			continue
		}
		fd := only(t, Run(f), RuleResources, tc.sev)
		require.Contains(t, fd.Message, "of 1000 entries")
		require.Contains(t, fd.Fix, "deyroute optimize auto")
	}
	require.Contains(t, only(t, Run(f), RuleResources, SevError).Message, "100% full (1000 of 1000 entries)")
	f.Tune.ConntrackMax = 0
	require.Empty(t, Run(f), "not loaded")
}

func TestR12FileHandles(t *testing.T) {
	f := healthyHub()
	f.Tune.FilesUsed, f.Tune.FilesMax = 900, 1000
	fd := only(t, Run(f), RuleResources, SevWarn)
	require.Contains(t, fd.Message, "90% of the system's file handles are in use (900 of 1000)")
	require.Contains(t, fd.Fix, "deyroute optimize auto")
	f.Tune.FilesUsed = 500
	require.Empty(t, Run(f))
}

func TestR12StateDB(t *testing.T) {
	f := healthyHub()
	f.Tune.StateDBBytes = 30 << 20 // default budget 50 MiB
	require.Empty(t, Run(f))
	f.Tune.StateDBBytes = 41 << 20
	fd := only(t, Run(f), RuleResources, SevWarn)
	require.Contains(t, fd.Message, "state.db uses 41 MiB of its 50 MiB budget (82%)")
	require.Contains(t, fd.Fix, "deyroute tunnel delete")
	f.Tune.StateDBBytes = 51 << 20
	only(t, Run(f), RuleResources, SevError)
	f.Tune.StateDBBudget = 100 << 20
	require.Empty(t, Run(f), "an explicit budget")
}

func TestR12StatsTable(t *testing.T) {
	f := healthyHub()
	f.Tune.Monitoring, f.Tune.MonitoredTunnels, f.Tune.Stats = true, 1, StatsMissing
	fd := only(t, Run(f), RuleResources, SevWarn)
	require.Contains(t, fd.Message, "inet deyroute_stats is missing")
	require.Contains(t, fd.Fix, "deyroute logs hub")

	f.Tune.Stats, f.Tune.StatsReason = StatsUnreadable, "unexpected nft output on line 3"
	require.Contains(t, only(t, Run(f), RuleResources, SevWarn).Message, "unexpected nft output on line 3")
	f.Tune.Stats, f.Tune.StatsReason = StatsUnavailable, "nft is not installed"
	fd = only(t, Run(f), RuleResources, SevInfo)
	require.Contains(t, fd.Message, "not available on this server (nft is not installed)")
	require.Contains(t, fd.Fix, "monitoring.enabled: false")

	// Negatives: table present, no enabled tunnel (no table is built),
	// monitoring off, a node.
	f.Tune.Stats = StatsPresent
	require.Empty(t, Run(f))
	f.Tune.Stats, f.Tune.MonitoredTunnels = StatsMissing, 0
	require.Empty(t, Run(f))
	f.Tune.MonitoredTunnels, f.Tune.Monitoring = 1, false
	require.Empty(t, Run(f))
	n := healthyNode()
	n.Tune = TuneFacts{Monitoring: true, MonitoredTunnels: 1, Stats: StatsMissing}
	require.Empty(t, Run(n))
}

func TestSizeIEC(t *testing.T) {
	for in, want := range map[uint64]string{0: "0 B", 512: "512 B", 2048: "2 KiB", 212992: "208 KiB", 7 << 20: "7 MiB",
		47<<20 + 1<<19: "47.5 MiB", 3 << 30: "3 GiB"} {
		require.Equal(t, want, sizeIEC(in))
	}
}
