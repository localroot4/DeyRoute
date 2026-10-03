package render

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func TestPlanEndToEnd(t *testing.T) {
	e := newEnv(t)
	in := e.input()
	in.UDPProbe = func(node string) (bool, bool) { return node != "nl-1", true } // blocked to nl-1
	plan, err := Plan(in)
	require.NoError(t, err)

	// udponly/udp is dropped from the ladder of a TCP tunnel (section 8).
	require.Equal(t, []string{"rev/tls", "rev/plain", "fwd/quic", "bad/x", "nat/wg", "boom/y", "native/relay"}, plan.Ladder)
	require.Equal(t, "main", plan.Tunnel)
	require.Equal(t, []string{
		"de-1|rev/tls", "de-1|rev/plain", "de-1|fwd/quic", "de-1|nat/wg", "de-1|native/relay",
		"nl-1|rev/tls", "nl-1|rev/plain", "nl-1|native/relay",
	}, candidateIDs(plan))
	require.Equal(t, map[string]deyerr.Code{
		"de-1|bad/x":    deyerr.B006,
		"de-1|boom/y":   deyerr.B002,
		"nl-1|fwd/quic": deyerr.B007,
		"nl-1|bad/x":    deyerr.B006,
		"nl-1|nat/wg":   deyerr.B007,
		"nl-1|boom/y":   deyerr.B002,
	}, skippedCodes(plan))
	for _, s := range plan.Skipped {
		require.NotEmpty(t, s.Reason)
		require.Error(t, s.Err)
	}
	require.Equal(t, map[string]string{"rev": "v1.2.3", "fwd": "app/v2.0.0", "bad": "v1", "nat": "v0.1", "boom": "v1", "native": "builtin"}, plan.Backends)
	require.NotNil(t, plan.TLS)
	require.Equal(t, "auto", plan.TLS.Mode)
	require.Len(t, plan.TLS.CertSHA256, 64)
	require.Empty(t, plan.Warnings)
	idx, err := e.store.AllocNetIndex("main", 255)
	require.NoError(t, err)
	require.Equal(t, idx, plan.NetIndex)

	// Control ports: stable per (tunnel, node, transport), distinct, in range.
	ports, err := e.store.CtlPorts()
	require.NoError(t, err)
	seen := map[int]bool{}
	for _, c := range plan.Candidates {
		require.Equal(t, ports[state.Key("main", c.Node, c.TransportID)], c.ControlPort)
		require.False(t, seen[c.ControlPort])
		seen[c.ControlPort] = true
		require.GreaterOrEqual(t, c.ControlPort, config.CtlRangeLow)
		require.LessOrEqual(t, c.ControlPort, config.CtlRangeHigh)
	}

	c, ok := plan.Candidate("de-1", "rev/tls")
	require.True(t, ok)
	require.Equal(t, "rev", c.Backend)
	require.Equal(t, "v1.2.3", c.Version)
	require.Equal(t, state.Candidate{Node: "de-1", Transport: "rev/tls"}, c.StateCandidate())
	require.Equal(t, "main.de-1.rev-tls", c.Hub.Instance)
	require.Equal(t, "main.de-1.rev-tls", c.NodeSide.Instance)
	require.Equal(t, "/etc/deyroute/backends/rev/main/de-1/tls", c.Hub.ConfigDir)
	require.Equal(t, c.Hub.ConfigDir, c.NodeSide.ConfigDir)
	// Reverse + TLS: the hub (server) gets cert and key, the node (client)
	// only the CA.
	require.Equal(t, []string{"ca.crt", "config.toml", "tls-cert.pem", "tls-key.pem"}, sortedFileNames(c.Hub.Files))
	require.Equal(t, []string{"ca.crt", "config.toml"}, sortedFileNames(c.NodeSide.Files))
	require.Equal(t, e.ca.CertPEM, c.Hub.Files["ca.crt"])
	require.Contains(t, string(c.Hub.Files["tls-key.pem"]), "PRIVATE KEY")
	require.Contains(t, string(c.Hub.Files["config.toml"]), `cert = "/etc/deyroute/backends/rev/main/de-1/tls/tls-cert.pem"`)
	require.Contains(t, string(c.NodeSide.Files["config.toml"]), `ca = "/etc/deyroute/backends/rev/main/de-1/tls/ca.crt"`)
	require.Contains(t, string(c.NodeSide.Files["config.toml"]), `server_name = "5.6.7.8"`)
	require.Contains(t, string(c.NodeSide.Files["config.toml"]), `pin = "`+plan.TLS.CertSHA256+`"`)
	require.Contains(t, string(c.Hub.Files["config.toml"]), `port = "0.0.0.0:443/tcp -> 127.0.0.1:443"`)
	// Keys from the KeyGenerator, persisted once per (tunnel, backend).
	require.Contains(t, string(c.Hub.Files["config.toml"]), `key.private = "priv-rev-0123456789abcdef"`)
	_, err = os.Stat(e.sec.KeysPath("main", "rev"))
	require.NoError(t, err)
	// Drop-in: ExecStart with the escaped version dir, per-tunnel log.
	drop := string(c.Hub.DropIn)
	require.Contains(t, drop, "/var/lib/deyroute/bin/rev/v1.2.3/rev-bin")
	require.Contains(t, drop, "/var/log/deyroute/tunnels/main.log")
	require.Contains(t, drop, "WorkingDirectory=/etc/deyroute/backends/rev/main/de-1/tls")
	require.Contains(t, string(c.Hub.Files["config.toml"]), "first_run = true")
	require.NotEmpty(t, c.Hub.Binds)

	// RenderInput details.
	ri, ok := e.fakes["rev"].lastInput("de-1", "tls")
	require.True(t, ok)
	require.Equal(t, "/var/lib/deyroute/bin/rev/v1.2.3", ri.Paths.BinDir)
	require.Equal(t, "/var/lib/deyroute/bin/rev/v1.2.3/rev-bin", ri.Paths.Binary)
	require.Equal(t, "/var/log/deyroute/tunnels/main.log", ri.Paths.LogFile)
	require.Equal(t, "/usr/local/bin/deyroute", ri.Paths.SelfBinary)
	require.Equal(t, "0.0.0.0", ri.ListenAddr)
	require.Equal(t, "www.example.org", ri.Decoy)
	require.Equal(t, "Germany 1", ri.Node.Name)
	require.Equal(t, "5.6.7.8", ri.Hub.PublicIP)
	require.NotEmpty(t, ri.Secrets.TLSP12Password)
	require.Equal(t, "/etc/deyroute/backends/rev/main/de-1/tls/tls.p12", ri.Secrets.TLSP12File)
	tok, err := e.sec.Token("main")
	require.NoError(t, err)
	require.Equal(t, tok, ri.Secrets.Token)
	// Backup node: same token.
	ri2, ok := e.fakes["rev"].lastInput("nl-1", "tls")
	require.True(t, ok)
	require.Equal(t, tok, ri2.Secrets.Token)

	// Non-TLS rung: no TLS files, no TLS secrets, no keys missing.
	plain, ok := plan.Candidate("de-1", "rev/plain")
	require.True(t, ok)
	require.Equal(t, []string{"ca.crt", "config.toml"}, sortedFileNames(plain.Hub.Files))
	rp, _ := e.fakes["rev"].lastInput("de-1", "plain")
	require.Empty(t, rp.Secrets.TLSKeyFile)
	require.Empty(t, rp.Secrets.TLSP12Password)

	// Forward + TLS: the node (server) holds cert and key.
	fq, ok := plan.Candidate("de-1", "fwd/quic")
	require.True(t, ok)
	require.Equal(t, []string{"ca.crt", "config.toml", "tls-cert.pem", "tls-key.pem"}, sortedFileNames(fq.NodeSide.Files))
	require.Equal(t, []string{"ca.crt", "config.toml"}, sortedFileNames(fq.Hub.Files))
	require.Contains(t, string(fq.Hub.DropIn), "/var/lib/deyroute/bin/fwd/app%%2Fv2.0.0/fwd-bin", "versions with a slash are path-escaped (and %% systemd-escaped)")

	// NAT-based rung: NAT/masquerade/ip_forward on the hub side only.
	nw, ok := plan.Candidate("de-1", "nat/wg")
	require.True(t, ok)
	require.Len(t, nw.Hub.NAT, 2)
	require.Equal(t, []string{"dey-main"}, nw.Hub.Masquerade)
	require.True(t, nw.Hub.IPForward)
	require.Empty(t, nw.NodeSide.NAT)
	require.Equal(t, "10.77."+strconv.Itoa(plan.NetIndex)+".2", nw.Hub.NAT[0].ToAddr)

	// Builtin: no binary path; the self binary runs.
	nr, ok := plan.Candidate("nl-1", "native/relay")
	require.True(t, ok)
	rn, _ := e.fakes["native"].lastInput("nl-1", "relay")
	require.Empty(t, rn.Paths.Binary)
	require.Empty(t, rn.Paths.BinDir)
	require.Contains(t, string(nr.Hub.DropIn), "/usr/local/bin/deyroute relay --tunnel main")

	// Deterministic: a second plan yields the same ports and bytes.
	plan2, err := Plan(e.input())
	require.NoError(t, err)
	c2, ok := plan2.Candidate("de-1", "rev/tls")
	require.True(t, ok)
	require.Equal(t, c.ControlPort, c2.ControlPort)
	require.Equal(t, c.Hub.Files, c2.Hub.Files)
	require.Equal(t, c.Hub.DropIn, c2.Hub.DropIn)
	// Without UDPProbe the UDP rungs are planned on nl-1 too.
	_, ok = plan2.Candidate("nl-1", "fwd/quic")
	require.True(t, ok)
}

// Section 7.6: rungs that need UDP enter the ladder only after a passed
// UDP probe; a node whose probe failed or never ran gets DEY-B007.
func TestPlanUDPRungsWaitForAPassedProbe(t *testing.T) {
	e := newEnv(t)
	in := e.input()
	in.UDPProbe = func(node string) (bool, bool) {
		if node == "de-1" {
			return false, true // probed: blocked
		}
		return false, false // nl-1: not probed yet
	}
	plan, err := Plan(in)
	require.NoError(t, err)
	reasons := map[string]string{}
	for _, s := range plan.Skipped {
		if s.Code == deyerr.B007 {
			reasons[s.Node+"|"+s.TransportID] = s.Reason
		}
	}
	require.Len(t, reasons, 4, "fwd/quic and nat/wg on both nodes")
	require.Equal(t, "Transport fwd/quic needs UDP and was skipped for tunnel main: the UDP probe between hub and node failed",
		reasons["de-1|fwd/quic"])
	require.Equal(t, "Transport fwd/quic needs UDP and was skipped for tunnel main: "+udpNotTested, reasons["nl-1|fwd/quic"])
	require.Contains(t, reasons["nl-1|nat/wg"], "has not run yet")
	for _, c := range plan.Candidates {
		require.NotContains(t, []string{"fwd/quic", "nat/wg"}, c.TransportID)
	}

	// A passed probe plans them.
	in.UDPProbe = func(string) (bool, bool) { return true, true }
	plan, err = Plan(in)
	require.NoError(t, err)
	for _, id := range []string{"de-1|fwd/quic", "de-1|nat/wg", "nl-1|fwd/quic"} {
		require.Contains(t, candidateIDs(plan), id)
	}
}

func TestPlanP12AndIPv6(t *testing.T) {
	e := newEnv(t)
	e.cfg.Hub.PublicIP6 = "2001:db8::1"
	e.cfg.Hub.Domain = "tun.example.com"
	e.cfg.Tunnels[0].Ladder = config.LadderRef{Inline: []string{"rev/p12"}}
	plan, err := Plan(e.input())
	require.NoError(t, err)
	c, ok := plan.Candidate("de-1", "rev/p12")
	require.True(t, ok)
	require.Equal(t, []string{"ca.crt", "config.toml", "tls-cert.pem", "tls-key.pem", "tls.p12"}, sortedFileNames(c.Hub.Files))
	p12, pass, err := e.sec.PKCS12("main")
	require.NoError(t, err)
	require.Equal(t, p12, c.Hub.Files["tls.p12"])
	require.Contains(t, string(c.Hub.Files["config.toml"]), pass)
	ri, _ := e.fakes["rev"].lastInput("de-1", "p12")
	require.Equal(t, "::", ri.ListenAddr)
	require.Equal(t, "tun.example.com", ri.Secrets.ServerName)
	cert, err := tlsutil.ParseCert(c.Hub.Files["tls-cert.pem"])
	require.NoError(t, err)
	require.NoError(t, cert.VerifyHostname("tun.example.com"))
	require.NoError(t, cert.VerifyHostname("2001:db8::1"))
	require.NoError(t, cert.VerifyHostname("5.6.7.8"))
}

func TestPlanTLSFailuresAndWarnings(t *testing.T) {
	e := newEnv(t)
	e.cfg.Tunnels[0].TLS = config.TLS{Mode: "custom", CertFile: "/nonexistent/c.pem", KeyFile: "/nonexistent/k.pem"}
	plan, err := Plan(e.input())
	require.NoError(t, err)
	codes := skippedCodes(plan)
	require.Equal(t, deyerr.T008, codes["de-1|rev/tls"])
	require.Equal(t, deyerr.T008, codes["de-1|fwd/quic"])
	_, ok := plan.Candidate("de-1", "rev/plain")
	require.True(t, ok, "non-TLS rungs still render")
	require.Nil(t, plan.TLS)

	// ACME without a certificate: auto material and a T003 warning.
	e.cfg.Tunnels[0].TLS = config.TLS{Mode: "acme"}
	e.cfg.Hub.Domain = "tun.example.com"
	plan, err = Plan(e.input())
	require.NoError(t, err)
	require.Equal(t, "auto", plan.TLS.Mode)
	require.Len(t, plan.Warnings, 1)
	require.Equal(t, deyerr.T003, deyerr.As(plan.Warnings[0]).Code)
}

// failingSecrets wraps a SecretsSource and fails selected calls.
type failingSecrets struct {
	SecretsSource
	token, keys, p12 error
}

func (f failingSecrets) Token(tunnel string) (string, error) {
	if f.token != nil {
		return "", f.token
	}
	return f.SecretsSource.Token(tunnel)
}

func (f failingSecrets) BackendKeys(tunnel, b string, gen func() (map[string]string, error)) (map[string]string, error) {
	if f.keys != nil {
		return nil, f.keys
	}
	return f.SecretsSource.BackendKeys(tunnel, b, gen)
}

func (f failingSecrets) PKCS12(tunnel string) ([]byte, string, error) {
	if f.p12 != nil {
		return nil, "", f.p12
	}
	return f.SecretsSource.PKCS12(tunnel)
}

func (f failingSecrets) TunnelTLS(tunnel, mode string, ips []net.IP, domain, c, k string) (secrets.TLSMaterial, error) {
	return f.SecretsSource.TunnelTLS(tunnel, mode, ips, domain, c, k)
}

func TestPlanErrorsAndSkips(t *testing.T) {
	e := newEnv(t)

	// Whole-tunnel errors.
	in := e.input()
	in.Tunnel.Nodes = nil
	_, err := Plan(in)
	require.Equal(t, deyerr.C008, deyerr.As(err).Code)

	in = e.input()
	in.Tunnel.Nodes = []string{"de-1", "xx-9"}
	_, err = Plan(in)
	require.Equal(t, deyerr.C010, deyerr.As(err).Code)

	in = e.input()
	in.Tunnel.Ladder = config.LadderRef{Name: "nope"}
	_, err = Plan(in)
	require.Equal(t, deyerr.C012, deyerr.As(err).Code)

	in = e.input()
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"udponly/udp"}}
	_, err = Plan(in)
	require.Equal(t, deyerr.C009, deyerr.As(err).Code, "the protocol filter leaves nothing")

	in = e.input()
	in.Secrets = failingSecrets{SecretsSource: e.sec, token: deyerr.New(deyerr.S009, deyerr.Params{"path": "x", "reason": "y"})}
	_, err = Plan(in)
	require.Equal(t, deyerr.S009, deyerr.As(err).Code)

	in = e.input()
	in.Secrets = nil
	_, err = Plan(in)
	require.Error(t, err)

	// Per-candidate skips.
	in = e.input()
	in.CtlPort = func(key string) (int, error) {
		if strings.Contains(key, "rev/plain") {
			return 0, deyerr.New(deyerr.P020, nil)
		}
		return e.store.AllocCtlPort(key, config.CtlRangeLow, config.CtlRangeHigh, nil)
	}
	in.NetIndex = func(string) (int, error) { return 0, errors.New("pool exhausted") }
	in.Secrets = failingSecrets{SecretsSource: e.sec, keys: errors.New("rng broken")}
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"rev/tls", "rev/plain", "nat/wg", "plainbad/x", "badunit/z", "native/relay"}}
	plan, err := Plan(in)
	require.NoError(t, err)
	require.Equal(t, -1, plan.NetIndex)
	codes := skippedCodes(plan)
	require.Equal(t, deyerr.B002, codes["de-1|rev/tls"], "key generation failure (plain error) → B002")
	require.Equal(t, deyerr.P020, codes["de-1|rev/plain"])
	require.Equal(t, deyerr.P030, codes["de-1|nat/wg"], "NetIndex failure reaches Validate as -1")
	require.Equal(t, deyerr.B006, codes["de-1|plainbad/x"], "a plain Validate error becomes B006")
	require.Equal(t, deyerr.X034, codes["de-1|badunit/z"], "invalid unit spec")
	require.Equal(t, []string{"de-1|native/relay", "nl-1|native/relay"}, candidateIDs(plan))

	// PKCS12 failure skips the TLS rungs only.
	in = e.input()
	in.Secrets = failingSecrets{SecretsSource: e.sec, p12: deyerr.New(deyerr.S009, deyerr.Params{"path": "p", "reason": "r"})}
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"rev/tls", "rev/plain"}}
	plan, err = Plan(in)
	require.NoError(t, err)
	require.Equal(t, deyerr.S009, skippedCodes(plan)["de-1|rev/tls"])
	_, ok := plan.Candidate("de-1", "rev/plain")
	require.True(t, ok)

	// Unknown transport in the registry (Supports true, Lookup fails).
	in = e.input()
	in.Registry = lyingRegistry{e.reg}
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"ghost/x", "rev/plain"}}
	plan, err = Plan(in)
	require.NoError(t, err)
	require.Equal(t, deyerr.C005, skippedCodes(plan)["de-1|ghost/x"])
	_, ok = plan.Candidate("de-1", "ghost/x")
	require.False(t, ok)
}

type lyingRegistry struct{ testRegistry }

func (lyingRegistry) Supports(string, string) bool { return true }

func TestCanaryPlan(t *testing.T) {
	e := newEnv(t)
	c, err := CanaryPlan(e.input(), 7777)
	require.NoError(t, err)
	require.Equal(t, "de-1", c.Node)
	require.Equal(t, "rev/tls", c.TransportID)
	require.Equal(t, "main.canary", c.Hub.Instance)
	require.Equal(t, "main.canary", c.NodeSide.Instance)
	require.Equal(t, "/etc/deyroute/backends/rev/main/canary", c.Hub.ConfigDir)
	ports, err := e.store.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, ports["main/canary/ctl"], c.ControlPort)
	loop := ports["main/canary/loopback"]
	require.NotZero(t, loop)
	require.NotEqual(t, loop, c.ControlPort)
	ri, ok := e.fakes["rev"].lastInput("de-1", "tls")
	require.True(t, ok)
	require.True(t, ri.Canary)
	require.Equal(t, "127.0.0.1", ri.ListenAddr)
	require.Equal(t, []config.PortMap{{Listen: loop, Proto: "tcp", Target: "127.0.0.1:7777", Probe: "tcp"}}, ri.Tunnel.Ports)
	require.Contains(t, string(c.Hub.Files["config.toml"]), "canary = true")
	require.Contains(t, string(c.Hub.Files["config.toml"]), "127.0.0.1:")
	// The tunnel's real ports are untouched.
	require.Len(t, e.cfg.Tunnels[0].Ports, 2)

	// Stable.
	c2, err := CanaryPlan(e.input(), 7777)
	require.NoError(t, err)
	require.Equal(t, c.ControlPort, c2.ControlPort)

	// UDP-only rung 1: the synthetic port map is UDP.
	e.cfg.Tunnels[0].Ports = []config.PortMap{{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"}}
	e.cfg.Tunnels[0].Ladder = config.LadderRef{Inline: []string{"udponly/udp"}}
	c3, err := CanaryPlan(e.input(), 7777)
	require.NoError(t, err)
	ru, _ := e.fakes["udponly"].lastInput("de-1", "udp")
	require.Equal(t, "udp", ru.Tunnel.Ports[0].Proto)
	require.Equal(t, "/etc/deyroute/backends/udponly/main/canary", c3.Hub.ConfigDir)

	// Errors.
	_, err = CanaryPlan(e.input(), 0)
	require.Equal(t, deyerr.P010, deyerr.As(err).Code)
	in := e.input()
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"bad/x"}}
	in.Tunnel.Ports = []config.PortMap{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"}}
	_, err = CanaryPlan(in, 7777)
	require.Equal(t, deyerr.B006, deyerr.As(err).Code)
	in = e.input()
	in.Nodes = map[string]config.Node{}
	_, err = CanaryPlan(in, 7777)
	require.Equal(t, deyerr.C010, deyerr.As(err).Code)
	in = e.input()
	in.CtlPort = func(string) (int, error) { return 0, deyerr.New(deyerr.P020, nil) }
	_, err = CanaryPlan(in, 7777)
	require.Equal(t, deyerr.P020, deyerr.As(err).Code)
	in = e.input()
	in.Tunnel.Nodes = nil
	_, err = CanaryPlan(in, 7777)
	require.Equal(t, deyerr.C008, deyerr.As(err).Code)
	in = e.input()
	in.Registry = lyingRegistry{e.reg}
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"ghost/x"}}
	_, err = CanaryPlan(in, 7777)
	require.Equal(t, deyerr.C005, deyerr.As(err).Code)
}

func TestDesiredStale(t *testing.T) {
	e := newEnv(t)
	plan, err := Plan(e.input())
	require.NoError(t, err)
	d := Desired([]TunnelPlan{plan})
	require.True(t, d["main.de-1.rev-tls"])
	require.False(t, d["main.de-1.bad-x"])
	require.Len(t, d, len(plan.Candidates))
	existing := []systemd.UnitState{
		{Unit: "deyroute-tun@main.de-1.rev-tls.service", Instance: "main.de-1.rev-tls"},
		{Unit: "deyroute-tun@old.de-1.rev-tls.service", Instance: "old.de-1.rev-tls"},
		{Unit: "deyroute-tun@main.de-1.bad-x.service"},
		{Unit: "deyroute-tun@main.de-1.bad-x.service"},
		{Unit: "deyroute-hub.service"},
	}
	require.Equal(t, []string{"main.de-1.bad-x", "old.de-1.rev-tls"}, Stale(existing, d))
	d["main.canary"] = true
	require.Empty(t, Stale([]systemd.UnitState{{Instance: "main.canary"}}, d))
}

func TestBackendRegistry(t *testing.T) {
	registerGlobalFake()
	var r Registry = BackendRegistry{}
	b, tr, err := r.Lookup("zzrendertest/one")
	require.NoError(t, err)
	require.Equal(t, "zzrendertest", b.Name())
	require.Equal(t, "one", tr.Name)
	require.True(t, r.Supports("zzrendertest/one", "tcp"))
	require.False(t, r.Supports("zzrendertest/one", "udp"))
	require.False(t, r.Supports("zzrendertest/none", "tcp"))

	// A nil Registry in Input uses the global registry.
	e := newEnv(t)
	in := e.input()
	in.Registry = nil
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"zzrendertest/one"}}
	plan, err := Plan(in)
	require.NoError(t, err)
	require.Equal(t, []string{"de-1|zzrendertest/one", "nl-1|zzrendertest/one"}, candidateIDs(plan))
}

func TestPathHelpers(t *testing.T) {
	require.Equal(t, "/etc/deyroute/backends/b/t/n/x", ConfigDir("b", "t", "n", "x"))
	require.Equal(t, "/etc/deyroute/backends/b/t/canary", CanaryConfigDir("b", "t"))
	require.Equal(t, "t/canary/loopback", CanaryLoopbackKey("t"))
	require.Equal(t, "t/canary/ctl", CanaryCtlKey("t"))
	require.Equal(t, "0.0.0.0", listenAddr(config.HubInfo{}))
	require.Equal(t, "d.example", serverName(config.HubInfo{Domain: " d.example ", PublicIP: "1.1.1.1"}))
	require.Len(t, hubIPs(config.HubInfo{PublicIP: "1.1.1.1", PublicIP6: "junk"}), 1)
	var p TunnelPlan
	_, ok := p.Candidate("a", "b")
	require.False(t, ok)
	require.True(t, refers(haystack(backend.Rendered{Unit: backend.UnitSpec{Env: map[string]string{"CERT": "/x/tls-cert.pem"}}}), FileTLSCert))
}

var globalOnce = make(chan struct{}, 1)

// registerGlobalFake registers one uniquely named backend in the global
// registry exactly once per test binary (backend.Register panics on
// duplicates, e.g. with -count=2).
func registerGlobalFake() {
	select {
	case globalOnce <- struct{}{}:
		backend.Register(&fakeBackend{name: "zzrendertest", version: "v9", transports: []backend.Transport{tr("zzrendertest", "one", backend.Reverse, "tcp")}})
	default:
	}
}

// TestPlanBackendTiers: the sticky backend tiers and tuning.wg_mtu reach
// the RenderInput of the hub side and of each node, and a node's tier only
// changes the files of that node's candidates.
func TestPlanBackendTiers(t *testing.T) {
	e := newEnv(t)
	e.cfg.Nodes = append(e.cfg.Nodes, config.Node{ID: "fr-1", Name: "France 1", PublicIP: "1.2.3.5"})
	e.fakes["rev"].render = func(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
		r := defaultRender(in, side)
		extra := "tier = \"" + in.Tier(side) + "\"\nwg_mtu = " + strconv.Itoa(in.WGMTU) + "\n"
		r.Files["config.toml"] = append(r.Files["config.toml"], extra...)
		return r, nil
	}
	plan := func() TunnelPlan {
		in := e.input()
		in.HubTier = e.cfg.BackendTier("")
		in.NodeTier = e.cfg.BackendTier
		in.WGMTU = e.cfg.Tuning.WGMTU
		p, err := Plan(in)
		require.NoError(t, err)
		return p
	}
	files := func(p TunnelPlan, node string) (hub, nodeSide []byte) {
		c, ok := p.Candidate(node, "rev/plain")
		require.True(t, ok)
		return c.Hub.Files["config.toml"], c.NodeSide.Files["config.toml"]
	}

	// No tiers: the backends see "" (their defaults) and wg_mtu 0.
	base := plan()
	ri, ok := e.fakes["rev"].lastInput("de-1", "plain")
	require.True(t, ok)
	require.Empty(t, ri.HubTier)
	require.Empty(t, ri.NodeTier)
	require.Zero(t, ri.WGMTU)

	e.cfg.Tuning.BackendTier = config.BackendTierLarge
	e.cfg.Tuning.WGMTU = 1380
	e.cfg.Nodes[0].BackendTier = config.BackendTierSmall // de-1
	tiered := plan()
	ri, _ = e.fakes["rev"].lastInput("de-1", "plain")
	require.Equal(t, config.BackendTierLarge, ri.HubTier)
	require.Equal(t, config.BackendTierSmall, ri.NodeTier)
	require.Equal(t, 1380, ri.WGMTU)
	ri, _ = e.fakes["rev"].lastInput("nl-1", "plain")
	require.Equal(t, config.BackendTierLarge, ri.HubTier)
	require.Empty(t, ri.NodeTier, "nl-1 has no tier of its own")
	hub, node := files(tiered, "de-1")
	require.Contains(t, string(hub), `tier = "large"`)
	require.Contains(t, string(node), `tier = "small"`)
	_, baseNL := files(base, "nl-1")
	_, nl := files(tiered, "nl-1")
	require.NotEqual(t, baseNL, nl, "wg_mtu changed")

	// Another node's tier (nl-1 in this tunnel, fr-1 in none) leaves the
	// files of de-1's candidates as they were.
	e.cfg.Nodes[1].BackendTier = config.BackendTierLarge
	e.cfg.Nodes[2].BackendTier = config.BackendTierMedium
	again := plan()
	for _, c := range tiered.Candidates {
		if c.Node != "de-1" {
			continue
		}
		c2, ok := again.Candidate(c.Node, c.TransportID)
		require.True(t, ok)
		require.Equal(t, c.Hub.Files, c2.Hub.Files, c.TransportID)
		require.Equal(t, c.NodeSide.Files, c2.NodeSide.Files, c.TransportID)
	}
	_, nl2 := files(again, "nl-1")
	require.Contains(t, string(nl2), `tier = "large"`)
}
