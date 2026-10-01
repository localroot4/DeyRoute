package setup

import (
	"context"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	deylog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// hubOpts returns options for an unprivileged hub setup in root: the
// deyroute group exists (gid testGID) and chown is recorded in ch (a fresh
// log when nil), never run for real.
func hubOpts(root string, f *exec.Fake, steps *stepLog, ch *chownLog) HubOptions {
	if ch == nil {
		ch = &chownLog{}
	}
	o := HubOptions{
		Root:        root,
		Runner:      f,
		Name:        "ir-1",
		ApplySysctl: true,
		Now:         fixedNow,
		CheckPort:   func(context.Context, int) error { return nil },
		LookupGroup: withGroup,
		LookupUser:  withUser,
		Chown:       ch.chown,
	}
	if steps != nil {
		o.Progress = steps.add
	}
	return o
}

func TestSetupHubEndToEnd(t *testing.T) {
	root := t.TempDir()
	fakeProc(t, root)
	f := hubFake()
	steps := &stepLog{}
	ch := &chownLog{}
	res, err := SetupHub(ctxT(t), hubOpts(root, f, steps, ch))
	require.NoError(t, err)

	require.Equal(t, []string{
		"detect_ip=ok", "ca=ok", "hub_cert=ok", "firewall=ok", "sysctl=ok", "config=ok", "service=skipped",
	}, steps.final())
	st, _ := steps.get(StepDetectIP)
	require.Equal(t, "5.6.7.8", st.Detail)
	require.Equal(t, "Detect public IP", st.Title)

	require.Equal(t, "5.6.7.8", res.PublicIP)
	require.Empty(t, res.PublicIP6)
	require.False(t, res.PrivateIP)
	require.Equal(t, config.DefaultControlPort, res.ControlPort)
	require.True(t, tlsutil.ValidFingerprint(res.CAFingerprint))
	require.False(t, res.CAReused)
	require.Equal(t, config.SysctlBalanced, res.SysctlProfile)
	require.True(t, res.FirewallManaged)
	require.False(t, res.ServiceStarted)
	require.Equal(t, filepath.Join(root, "etc/deyroute/config.yaml"), res.ConfigPath)

	// config.yaml: valid, strict, 0600.
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.RoleHub, cfg.Role)
	require.Equal(t, "ir-1", cfg.Hub.Name)
	require.Equal(t, "5.6.7.8", cfg.Hub.PublicIP)
	require.Equal(t, config.DefaultControlPort, cfg.Hub.ControlPort)
	require.Equal(t, config.SysctlBalanced, cfg.Tuning.SysctlProfile)
	require.True(t, cfg.Security.FirewallManaged)
	require.True(t, cfg.Security.RestrictControlToNodes)
	require.Equal(t, config.DefaultLadder, cfg.Ladders[config.DefaultLadderName])
	require.Equal(t, os.FileMode(0o600), mode(t, res.ConfigPath))

	// Directory layout: modes and group of ARCHITECTURE.md §7.5.
	for dir, want := range map[string]os.FileMode{
		"etc/deyroute":             0o710,
		"etc/deyroute/secrets":     0o700,
		"etc/deyroute/backends":    0o750,
		"var/lib/deyroute":         0o750,
		"var/lib/deyroute/bin":     0o755,
		"var/lib/deyroute/backups": 0o700,
		"var/log/deyroute":         0o750,
		"var/log/deyroute/tunnels": 0o770,
	} {
		require.Equalf(t, want, mode(t, filepath.Join(root, dir)), "mode of %s", dir)
	}
	for _, dir := range []string{"etc/deyroute", "etc/deyroute/backends", "var/lib/deyroute", "var/log/deyroute", "var/log/deyroute/tunnels"} {
		gid, ok := ch.gid(filepath.Join(root, dir))
		require.Truef(t, ok, "%s not chowned", dir)
		require.Equal(t, testGID, gid)
	}
	for _, dir := range []string{"etc/deyroute/secrets", "var/lib/deyroute/bin", "var/lib/deyroute/backups"} {
		_, ok := ch.gid(filepath.Join(root, dir))
		require.Falsef(t, ok, "%s must stay root:root", dir)
	}

	// Secrets: 0600, CA + hub certificate for the public IP.
	sec := filepath.Join(root, "etc/deyroute/secrets")
	for _, n := range []string{"ca.crt", "ca.key", "hub.crt", "hub.key"} {
		require.Equalf(t, os.FileMode(0o600), mode(t, filepath.Join(sec, n)), "mode of %s", n)
	}
	ca, err := tlsutil.LoadCA(filepath.Join(sec, "ca.crt"), filepath.Join(sec, "ca.key"))
	require.NoError(t, err)
	require.Equal(t, res.CAFingerprint, ca.Fingerprint())
	require.Equal(t, "DEYROUTE CA ir-1", ca.Cert.Subject.CommonName)
	hubPEM, err := os.ReadFile(filepath.Join(sec, "hub.crt"))
	require.NoError(t, err)
	hub, err := tlsutil.ParseCert(hubPEM)
	require.NoError(t, err)
	require.Equal(t, "ir-1", hub.Subject.CommonName)
	require.Equal(t, tlsutil.HubOU, tlsutil.CertRole(hub))
	require.Len(t, hub.IPAddresses, 1)
	require.Equal(t, "5.6.7.8", hub.IPAddresses[0].String())
	require.WithinDuration(t, fixedNow().Add(tlsutil.HubCertValidity), hub.NotAfter, 2*time.Hour)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = hub.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: fixedNow(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	require.NoError(t, err)

	// Firewall: one nft transaction, control port open (join window).
	var script string
	for _, c := range f.Calls() {
		if c.Line() == "nft -f -" {
			script = string(c.Stdin)
		}
	}
	require.Contains(t, script, "table inet deyroute")
	require.Contains(t, script, "tcp dport 44433 accept")
	require.Contains(t, script, "tcp dport 30000-31999 drop")
	require.Equal(t, []string{route4Line, route6Line, "nft -f -"}, f.Lines())

	// Sysctl: balanced written and applied, previous values backed up.
	conf, err := os.ReadFile(filepath.Join(root, config.SysctlConfPath))
	require.NoError(t, err)
	require.Contains(t, string(conf), "profile: balanced")
	require.Equal(t, "65535", procValue(t, root, "net.core.somaxconn"))
	require.Equal(t, "bbr", procValue(t, root, "net.ipv4.tcp_congestion_control"))
	backup, err := os.ReadFile(filepath.Join(root, config.SysctlBackup))
	require.NoError(t, err)
	require.Contains(t, string(backup), "net.core.somaxconn = 4096")
}

func TestSetupHubRefusesConfigured(t *testing.T) {
	root := t.TempDir()
	res, err := SetupHub(ctxT(t), hubOpts(root, hubFake(), nil, nil))
	require.NoError(t, err)
	caBefore, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/ca.crt"))
	require.NoError(t, err)

	f := hubFake()
	_, err = SetupHub(ctxT(t), hubOpts(root, f, nil, nil))
	e := requireTop(t, err, deyerr.I013)
	require.Contains(t, e.Message(), "already set up as hub")
	require.Empty(t, f.Calls(), "a refused setup runs nothing")
	caAfter, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/ca.crt"))
	require.NoError(t, err)
	require.Equal(t, caBefore, caAfter)
	require.FileExists(t, res.ConfigPath)

	// Join is refused on a configured server too.
	_, err = Join(ctxT(t), JoinOptions{Root: root, Link: "dey://x@1.2.3.4:1#sha256:00"})
	requireTop(t, err, deyerr.I013)

	// An unreadable role still refuses.
	require.NoError(t, os.WriteFile(res.ConfigPath, []byte(":::"), 0o600))
	_, err = SetupHub(ctxT(t), hubOpts(root, hubFake(), nil, nil))
	e = requireTop(t, err, deyerr.I013)
	require.Contains(t, e.Message(), "a deyroute server")
}

func TestSetupHubRerunAfterFailureReusesSecrets(t *testing.T) {
	root := t.TempDir()
	f := hubFake()
	f.On("nft -f -", exec.Fail(1, "Error: Could not process rule: Operation not supported"))
	steps := &stepLog{}
	_, err := SetupHub(ctxT(t), hubOpts(root, f, steps, nil))
	e := requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "firewall")
	requireCode(t, err, deyerr.P019)
	require.Contains(t, e.Detail, "DEY-P019")
	require.Contains(t, e.Detail, "Operation not supported")
	require.Equal(t, []string{"detect_ip=ok", "ca=ok", "hub_cert=ok", "firewall=failed"}, steps.final())
	st, _ := steps.get(StepFirewall)
	require.Equal(t, "DEY-P019", st.Error.Code)
	require.NoFileExists(t, filepath.Join(root, "etc/deyroute/config.yaml"), "no half-written config")
	hubBefore, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	caBefore, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/ca.crt"))
	require.NoError(t, err)

	// The next run is not refused and keeps the CA and hub certificate.
	res, err := SetupHub(ctxT(t), hubOpts(root, hubFake(), nil, nil))
	require.NoError(t, err)
	require.True(t, res.CAReused)
	caAfter, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/ca.crt"))
	require.NoError(t, err)
	require.Equal(t, caBefore, caAfter)
	hubAfter, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	require.Equal(t, hubBefore, hubAfter)

	// A changed public IP re-issues the hub certificate but keeps the CA.
	require.NoError(t, os.Remove(res.ConfigPath))
	o := hubOpts(root, hubFake(), nil, nil)
	o.PublicIP = "5.6.7.9"
	res2, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, res.CAFingerprint, res2.CAFingerprint)
	hubNew, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	require.NotEqual(t, hubAfter, hubNew)
	c, err := tlsutil.ParseCert(hubNew)
	require.NoError(t, err)
	require.Equal(t, "5.6.7.9", c.IPAddresses[0].String())

	// A damaged CA (no config yet) is replaced.
	require.NoError(t, os.Remove(res2.ConfigPath))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/deyroute/secrets/ca.key"), []byte("junk"), 0o600))
	res3, err := SetupHub(ctxT(t), hubOpts(root, hubFake(), nil, nil))
	require.NoError(t, err)
	require.False(t, res3.CAReused)
	require.NotEqual(t, res.CAFingerprint, res3.CAFingerprint)
}

func TestSetupHubPrivateAddressWarns(t *testing.T) {
	root := t.TempDir()
	f := exec.NewFake()
	f.On(route4Line, exec.OK("1.1.1.1 via 10.0.0.1 dev ens3 src 10.0.0.5 uid 0\n"))
	f.On(route6Line, exec.OK("2606:4700:4700::1111 from :: via fe80::1 dev ens3 src fd00::5 metric 1024 pref medium\n"))
	f.On("nft -f -", exec.OK(""))
	steps := &stepLog{}
	res, err := SetupHub(ctxT(t), hubOpts(root, f, steps, nil))
	require.NoError(t, err)
	require.True(t, res.PrivateIP)
	require.Equal(t, "10.0.0.5", res.PublicIP)
	require.Empty(t, res.PublicIP6, "a ULA address is not used")
	st, ok := steps.get(StepDetectIP)
	require.True(t, ok)
	require.Equal(t, api.StepWarn, st.Status)
	require.Equal(t, "DEY-I021", st.Error.Code)
	require.Contains(t, st.Error.Message, "10.0.0.5")
}

func TestSetupHubExplicitAddressesAndIPv6(t *testing.T) {
	root := t.TempDir()
	f := exec.NewFake()
	f.On(route6Line, exec.OK("2606:4700:4700::1111 from :: via fe80::1 dev eth0 proto ra src 2a01:4f8:1:2::5 metric 1024\n"))
	f.On("nft -f -", exec.OK(""))
	o := hubOpts(root, f, nil, nil)
	o.PublicIP = " 5.6.7.8 "
	o.ControlPort = 44500
	o.Mirror = "https://mirror.example/deyroute"
	res, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, "2a01:4f8:1:2::5", res.PublicIP6)
	require.Equal(t, 44500, res.ControlPort)
	require.False(t, f.Called(route4Line), "a given public IP is not detected")

	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, "2a01:4f8:1:2::5", cfg.Hub.PublicIP6)
	require.Equal(t, "https://mirror.example/deyroute", cfg.Hub.Mirror)
	pemBytes, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	c, err := tlsutil.ParseCert(pemBytes)
	require.NoError(t, err)
	var sans []string
	for _, ip := range c.IPAddresses {
		sans = append(sans, ip.String())
	}
	require.ElementsMatch(t, []string{"5.6.7.8", "2a01:4f8:1:2::5"}, sans)
	script := string(f.Calls()[len(f.Calls())-1].Stdin)
	require.Contains(t, script, "tcp dport 44500 accept")
	require.Contains(t, script, "nodes6")

	// NoIPv6 skips the IPv6 lookup.
	root2 := t.TempDir()
	f2 := hubFake()
	o2 := hubOpts(root2, f2, nil, nil)
	o2.NoIPv6 = true
	_, err = SetupHub(ctxT(t), o2)
	require.NoError(t, err)
	require.False(t, f2.Called(route6Line))
}

func TestSetupHubValidationWritesNothing(t *testing.T) {
	cases := map[string]struct {
		mod  func(*HubOptions)
		code deyerr.Code
	}{
		"empty name":        {func(o *HubOptions) { o.Name = " " }, deyerr.C013},
		"reserved port":     {func(o *HubOptions) { o.ControlPort = 30500 }, deyerr.C013},
		"bad ip":            {func(o *HubOptions) { o.PublicIP = "not-an-ip" }, deyerr.C013},
		"bad sysctl":        {func(o *HubOptions) { o.SysctlProfile = "turbo" }, deyerr.C013},
		"control port busy": {func(o *HubOptions) { o.CheckPort = busyPort }, deyerr.P012},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			steps := &stepLog{}
			o := hubOpts(root, hubFake(), steps, nil)
			tc.mod(&o)
			_, err := SetupHub(ctxT(t), o)
			e := requireTop(t, err, deyerr.I014)
			require.Contains(t, e.Message(), "config")
			requireCode(t, err, tc.code)
			st, ok := steps.get(StepConfig)
			require.True(t, ok)
			require.Equal(t, api.StepFailed, st.Status)
			require.NoDirExists(t, filepath.Join(root, "etc/deyroute"))
		})
	}
}

func busyPort(_ context.Context, port int) error {
	return deyerr.New(deyerr.P012, deyerr.Params{"port": port, "process": "nginx (pid 1)", "addr": "0.0.0.0"})
}

func TestSetupHubSuggestsFreeControlPort(t *testing.T) {
	root := t.TempDir()
	o := hubOpts(root, hubFake(), nil, nil)
	o.CheckPort = func(_ context.Context, port int) error {
		if port == config.DefaultControlPort {
			return busyPort(context.Background(), port)
		}
		return nil
	}
	res, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, config.DefaultControlPort+1, res.ControlPort)
}

func TestSetupHubDefaultPortCheckFindsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	o := hubOpts(t.TempDir(), hubFake(), nil, nil)
	o.CheckPort = nil
	o.ControlPort = port
	o.PublicIP = "5.6.7.8"
	_, err = SetupHub(ctxT(t), o)
	requireCode(t, err, deyerr.P012)
}

func TestSetupHubWithoutNFTOrFirewall(t *testing.T) {
	root := t.TempDir()
	f := hubFake()
	f.On("nft -f -", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	steps := &stepLog{}
	res, err := SetupHub(ctxT(t), hubOpts(root, f, steps, nil))
	require.NoError(t, err)
	require.False(t, res.FirewallManaged)
	st, _ := steps.get(StepFirewall)
	require.Equal(t, api.StepWarn, st.Status)
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.False(t, cfg.Security.FirewallManaged)

	root2 := t.TempDir()
	f2 := hubFake()
	steps2 := &stepLog{}
	o := hubOpts(root2, f2, steps2, nil)
	o.NoFirewall = true
	o.ApplySysctl = false
	res2, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.False(t, f2.Called("nft -f -"))
	require.Equal(t, config.SysctlOff, res2.SysctlProfile)
	st, _ = steps2.get(StepFirewall)
	require.Equal(t, api.StepSkipped, st.Status)
	st, _ = steps2.get(StepSysctl)
	require.Equal(t, api.StepSkipped, st.Status)
	cfg2, err := config.LoadWith(res2.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, cfg2.Tuning.SysctlProfile)
	require.NoFileExists(t, filepath.Join(root2, config.SysctlConfPath))
}

func TestSetupHubCreatesBackendUser(t *testing.T) {
	root := t.TempDir()
	su := &sysUsers{}
	f := hubFake()
	f.Handler = su.handle
	ch := &chownLog{}
	steps := &stepLog{}
	o := hubOpts(root, f, steps, ch)
	o.LookupGroup, o.LookupUser = su.lookup, su.lookupUser
	_, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	calls := su.ran()
	require.Len(t, calls, 1)
	require.Equal(t, []string{"--root=" + root, "--inline", sysusersLine}, calls[0].Args,
		"the same entry as install.sh, inside Root")
	st, _ := steps.get(StepCA)
	require.Equal(t, api.StepOK, st.Status)
	gid, ok := ch.gid(filepath.Join(root, "etc/deyroute"))
	require.True(t, ok)
	require.Equal(t, testGID, gid)
	require.Equal(t, os.FileMode(0o710), mode(t, filepath.Join(root, "etc/deyroute")))

	// When the user cannot be created setup still succeeds: the ca step
	// warns (DEY-X032, Fix: run the installer again) and the directories
	// are root-only.
	root2 := t.TempDir()
	su2 := &sysUsers{fail: true}
	f2 := hubFake()
	f2.Handler = su2.handle
	steps2 := &stepLog{}
	o2 := hubOpts(root2, f2, steps2, nil)
	o2.LookupGroup, o2.LookupUser = su2.lookup, su2.lookupUser
	res2, err := SetupHub(ctxT(t), o2)
	require.NoError(t, err)
	require.FileExists(t, res2.ConfigPath)
	st, _ = steps2.get(StepCA)
	require.Equal(t, api.StepWarn, st.Status)
	require.Equal(t, res2.CAFingerprint, st.Detail)
	require.Equal(t, "DEY-X032", st.Error.Code)
	require.Contains(t, st.Error.Fix, "installer again")
	require.Contains(t, st.Error.Why, "deyroute does not exist")
	require.Equal(t, os.FileMode(0o700), mode(t, filepath.Join(root2, "etc/deyroute")))
	require.Equal(t, os.FileMode(0o700), mode(t, filepath.Join(root2, "var/log/deyroute/tunnels")))

	// On the real root the command is exactly the installer's.
	e := newEnv(envOptions{Runner: f2, LookupGroup: noGroup, LookupUser: withUser})
	require.Error(t, e.ensureSystemUser(ctxT(t)))
	last := f2.Calls()[len(f2.Calls())-1]
	require.Equal(t, []string{"--inline", sysusersLine}, last.Args)

	// A group without the user is not enough: the units run as the user.
	su3 := &sysUsers{}
	f3 := exec.NewFake()
	f3.Handler = su3.handle
	e3 := newEnv(envOptions{Root: t.TempDir(), Runner: f3, LookupGroup: withGroup, LookupUser: su3.lookupUser})
	require.NoError(t, e3.ensureSystemUser(ctxT(t)))
	require.Len(t, su3.ran(), 1)
	// Both present: nothing runs.
	f4 := exec.NewFake()
	e4 := newEnv(envOptions{Root: t.TempDir(), Runner: f4, LookupGroup: withGroup, LookupUser: withUser})
	require.NoError(t, e4.ensureSystemUser(ctxT(t)))
	require.Empty(t, f4.Calls())
}

func TestSetupHubIPv6OnlyAddress(t *testing.T) {
	root := t.TempDir()
	f := exec.NewFake()
	f.On(route6Line, exec.OK("2606:4700:4700::1111 from :: via fe80::1 dev eth0 src 2a01:4f8:1:2::5 metric 1024\n"))
	f.On("nft -f -", exec.OK(""))
	o := hubOpts(root, f, nil, nil)
	o.PublicIP = "2a01:4f8:1:2::5"
	res, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, "2a01:4f8:1:2::5", res.PublicIP)
	require.Empty(t, res.PublicIP6, "the same address is not listed twice")
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Empty(t, cfg.Hub.PublicIP6)
	pemBytes, err := os.ReadFile(filepath.Join(root, "etc/deyroute/secrets/hub.crt"))
	require.NoError(t, err)
	c, err := tlsutil.ParseCert(pemBytes)
	require.NoError(t, err)
	require.Len(t, c.IPAddresses, 1)
}

func TestSetupHubSysctlProblemsDoNotBlock(t *testing.T) {
	// No /proc tree: every key is missing, so apply only warns.
	root := t.TempDir()
	steps := &stepLog{}
	o := hubOpts(root, hubFake(), steps, nil)
	o.SysctlProfile = config.SysctlAggressive
	res, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.Equal(t, config.SysctlAggressive, res.SysctlProfile)
	require.NotEmpty(t, res.SysctlWarnings)
	st, _ := steps.get(StepSysctl)
	require.Equal(t, api.StepWarn, st.Status)

	// A write failure (sysctl.d is a file) is a warning and records "off".
	root2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root2, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root2, "etc/sysctl.d"), nil, 0o644))
	fakeProc(t, root2)
	steps2 := &stepLog{}
	res2, err := SetupHub(ctxT(t), hubOpts(root2, hubFake(), steps2, nil))
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, res2.SysctlProfile)
	st, _ = steps2.get(StepSysctl)
	require.Equal(t, api.StepWarn, st.Status)
	require.NotNil(t, st.Error)
	cfg, err := config.LoadWith(res2.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, cfg.Tuning.SysctlProfile)
}

func TestSetupHubDetectFailures(t *testing.T) {
	f := exec.NewFake()
	f.On(route4Line, exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "ip"})})
	_, err := SetupHub(ctxT(t), hubOpts(t.TempDir(), f, nil, nil))
	requireTop(t, err, deyerr.I014)
	requireCode(t, err, deyerr.I010)

	f2 := exec.NewFake()
	f2.On(route4Line, exec.Fail(2, "RTNETLINK answers: Network is unreachable"))
	steps := &stepLog{}
	_, err = SetupHub(ctxT(t), hubOpts(t.TempDir(), f2, steps, nil))
	requireCode(t, err, deyerr.I020)
	st, _ := steps.get(StepDetectIP)
	require.Equal(t, "DEY-I020", st.Error.Code)
}

func TestSetupHubStartsService(t *testing.T) {
	root := t.TempDir()
	sock := shortSocket(t)
	serveSocket(t, sock)
	f := hubFake()
	o := hubOpts(root, f, nil, nil)
	o.StartService = true
	o.SocketPath = sock
	res, err := SetupHub(ctxT(t), o)
	require.NoError(t, err)
	require.True(t, res.ServiceStarted)
	require.True(t, f.Called("systemctl daemon-reload"))
	require.True(t, f.Called("systemctl enable --now deyroute-hub.service"))
	for _, u := range []string{"deyroute-hub.service", "deyroute-node.service", "deyroute-tun@.service"} {
		require.FileExists(t, filepath.Join(root, "etc/systemd/system", u))
	}
	// The unit is enabled only after config.yaml exists.
	lines := f.Lines()
	require.Equal(t, "systemctl enable --now deyroute-hub.service", lines[len(lines)-1])
}

func TestSetupHubServiceTimeout(t *testing.T) {
	root := t.TempDir()
	o := hubOpts(root, hubFake(), nil, nil)
	o.StartService = true
	o.SocketPath = filepath.Join(shortSocketDir(t), "none.sock")
	o.SocketTimeout = 300 * time.Millisecond
	_, err := SetupHub(ctxT(t), o)
	e := requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "service")
	x := requireCode(t, err, deyerr.X003)
	require.NotNil(t, x)
	require.Contains(t, e.Detail, "journalctl -u deyroute-hub")
	require.Contains(t, e.Fix(), "systemctl enable --now deyroute-hub.service")
	require.FileExists(t, filepath.Join(root, "etc/deyroute/config.yaml"))

	// systemctl failing is reported the same way.
	root2 := t.TempDir()
	f := hubFake()
	f.On("systemctl enable --now deyroute-hub.service", exec.Fail(1, "Job for deyroute-hub.service failed"))
	o2 := hubOpts(root2, f, nil, nil)
	o2.StartService = true
	_, err = SetupHub(ctxT(t), o2)
	requireTop(t, err, deyerr.I014)
	requireCode(t, err, deyerr.X007)
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(shortSocket(t))
}

func TestSetupHubCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SetupHub(ctx, hubOpts(t.TempDir(), hubFake(), nil, nil))
	requireTop(t, err, deyerr.I014)
	requireCode(t, err, deyerr.X031)
}

func TestWaitSocketCancelledIsX031(t *testing.T) {
	e := newEnv(envOptions{Root: t.TempDir(), Runner: exec.NewFake(),
		SocketPath: filepath.Join(shortSocketDir(t), "none.sock"), SocketTimeout: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	err := e.waitSocket(ctx, "deyroute-hub")
	requireTop(t, err, deyerr.X031)
	require.Less(t, time.Since(start), 30*time.Second, "cancellation ends the wait")
}

func TestStepErrorsAreRedacted(t *testing.T) {
	const secret = "tok-ZmFrZXNlY3JldHZhbHVlMTIzNDU2Nzg5MA"
	deylog.RegisterSecret(secret)
	steps := &stepLog{}
	r := newReporter(steps.add, nil)
	r.fail(StepJoin, deyerr.New(deyerr.X000, nil).WithDetail("proxy said: "+secret).WithWhy("why "+secret))
	st, ok := steps.get(StepJoin)
	require.True(t, ok)
	require.NotContains(t, st.Error.Detail, secret)
	require.NotContains(t, st.Error.Why, secret)
	require.Contains(t, st.Error.Detail, "***")
}

func TestHubCertUsable(t *testing.T) {
	dir := t.TempDir()
	ca, err := tlsutil.NewCA("ir-1", fixedNow())
	require.NoError(t, err)
	ca.Now = fixedNow
	ips := []net.IP{net.ParseIP("5.6.7.8")}
	certPath, keyPath := filepath.Join(dir, "hub.crt"), filepath.Join(dir, "hub.key")
	require.False(t, hubCertUsable(ca, certPath, keyPath, "ir-1", ips, fixedNow()), "missing files")

	certPEM, keyPEM, err := ca.IssueServer("ir-1", ips, nil, 0)
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecretPair(certPath, certPEM, keyPath, keyPEM))
	require.True(t, hubCertUsable(ca, certPath, keyPath, "ir-1", ips, fixedNow()))
	require.False(t, hubCertUsable(ca, certPath, keyPath, "ir-2", ips, fixedNow()), "other name")
	require.False(t, hubCertUsable(ca, certPath, keyPath, "ir-1", append(ips, net.ParseIP("2a01::1")), fixedNow()), "missing SAN")
	require.False(t, hubCertUsable(ca, certPath, keyPath, "ir-1", ips, fixedNow().Add(tlsutil.HubCertValidity)), "due for renewal")

	other, err := tlsutil.NewCA("other", fixedNow())
	require.NoError(t, err)
	require.False(t, hubCertUsable(other, certPath, keyPath, "ir-1", ips, fixedNow()), "other CA")

	_, otherKey, err := ca.IssueServer("ir-1", ips, nil, 0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyPath, otherKey, 0o600))
	require.False(t, hubCertUsable(ca, certPath, keyPath, "ir-1", ips, fixedNow()), "key mismatch")

	// A tunnel certificate of the same CA is not a hub certificate.
	tunCert, tunKey, err := ca.IssueTunnel("main", ips, nil, fixedNow())
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecretPair(certPath, tunCert, keyPath, tunKey))
	require.False(t, hubCertUsable(ca, certPath, keyPath, "tunnel-main", ips, fixedNow()))
}

func TestStepTitle(t *testing.T) {
	require.Equal(t, "Issue hub certificate", StepTitle(StepHubCert))
	require.Equal(t, "no-such-step", StepTitle("no-such-step"))
	for id := range defaultTitles {
		require.NotEmpty(t, StepTitle(id))
		require.False(t, strings.HasPrefix(StepTitle(id), StepTitleKeyPrefix))
	}
}

func TestEnsureLayoutKeepsExistingDirs(t *testing.T) {
	root := t.TempDir()
	etc := filepath.Join(root, "etc/deyroute")
	require.NoError(t, os.MkdirAll(filepath.Join(etc, "secrets"), 0o755))
	require.NoError(t, os.Chmod(etc, 0o750))
	require.NoError(t, os.Chmod(filepath.Join(etc, "secrets"), 0o755))
	ch := &chownLog{}
	e := newEnv(envOptions{Root: root, Runner: exec.NewFake(), LookupGroup: withGroup, Chown: ch.chown})
	require.NoError(t, e.ensureLayout())
	require.Equal(t, os.FileMode(0o750), mode(t, etc), "an existing /etc/deyroute keeps its mode")
	require.Equal(t, os.FileMode(0o700), mode(t, filepath.Join(etc, "secrets")), "secrets is always 0700")
	_, chowned := ch.gid(etc)
	require.False(t, chowned)
	gid, ok := ch.gid(filepath.Join(etc, "backends"))
	require.True(t, ok)
	require.Equal(t, testGID, gid)

	// A failing chown is DEY-X032.
	root2 := t.TempDir()
	e2 := newEnv(envOptions{Root: root2, Runner: exec.NewFake(), LookupGroup: withGroup,
		Chown: func(string, int, int) error { return os.ErrPermission }})
	requireTop(t, e2.ensureLayout(), deyerr.X032)

	// A file in place of a directory is DEY-X032.
	root3 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root3, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root3, "etc/deyroute"), nil, 0o600))
	e3 := newEnv(envOptions{Root: root3, Runner: exec.NewFake(), LookupGroup: noGroup})
	requireTop(t, e3.ensureLayout(), deyerr.X032)
}

func TestLookupGroupID(t *testing.T) {
	gid, err := LookupGroupID("root")
	if err == nil {
		require.Equal(t, 0, gid)
	}
	_, err = LookupGroupID("deyroute-no-such-group-xyz")
	require.Error(t, err)
	uid, err := LookupUserID("root")
	if err == nil {
		require.Equal(t, 0, uid)
	}
	_, err = LookupUserID("deyroute-no-such-user-xyz")
	require.Error(t, err)
}
