package setup

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Secret file names inside /etc/deyroute/secrets (section 4).
const (
	FileCACert   = "ca.crt"
	FileCAKey    = "ca.key"
	FileHubCert  = "hub.crt"
	FileHubKey   = "hub.key"
	FileNodeCert = "node.crt"
	FileNodeKey  = "node.key"
)

// HubOptions are the answers of the hub wizard (spec section 5: at most 5
// questions; everything else is automatic) plus the injectable environment.
type HubOptions struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Runner runs ip, nft and systemctl (exec.NewRunner() when nil).
	Runner exec.Runner

	// Name is hub.name (e.g. "ir-1"); required.
	Name string
	// PublicIP is hub.public_ip; empty = detected with DetectPublicIP.
	PublicIP string
	// PublicIP6 is hub.public_ip6; empty = detected with DetectPublicIP6
	// unless NoIPv6 (only a global address is used).
	PublicIP6 string
	NoIPv6    bool
	// ControlPort is hub.control_port; 0 = SuggestControlPort(44433). An
	// explicit port must be free (DEY-P012 otherwise).
	ControlPort int
	// Mirror is stored as hub.mirror (optional release base override).
	Mirror string
	// ApplySysctl applies SysctlProfile (the owner confirmed; spec section
	// 12). When false nothing is changed and tuning.sysctl_profile is off.
	ApplySysctl bool
	// SysctlProfile is balanced (default when empty), aggressive or auto
	// (the automatic profile: the kernel plan computed from this host's
	// facts plus the resource drop-ins).
	SysctlProfile string
	// TunePlan is the automatic plan the wizard showed (PlanHostTune with
	// HubReserved and tuning.bbr) and the owner confirmed; it is applied
	// as shown. nil computes it at the sysctl step. Only for auto.
	TunePlan *HostTune
	// NoFirewall writes security.firewall_managed: false and skips the
	// firewall step (suggestions only, spec section 11).
	NoFirewall bool
	// StartService installs the unit templates, runs `systemctl enable
	// --now deyroute-hub.service` and waits for the local API socket. Tests
	// leave it false.
	StartService bool

	// Now is the clock (time.Now when nil).
	Now func() time.Time
	// Progress receives every step update (may be nil).
	Progress func(api.Step)
	// Logger receives one line per step (discarded when nil).
	Logger *slog.Logger

	// CheckPort returns nil when the TCP port is free, else DEY-P012; nil
	// uses CheckControlPort (bind test + owner lookup under Root/proc).
	CheckPort func(ctx context.Context, port int) error
	// LookupGroup returns the gid of a group (LookupGroupID when nil).
	LookupGroup func(name string) (gid int, err error)
	// LookupUser returns the uid of a user (LookupUserID when nil); with
	// LookupGroup it tells whether the backend user deyroute exists.
	LookupUser func(name string) (uid int, err error)
	// Chown changes ownership (os.Lchown when nil).
	Chown func(path string, uid, gid int) error
	// SocketPath is the local API socket (Root/run/deyroute/daemon.sock).
	SocketPath string
	// SocketTimeout bounds the wait for the socket (20 s when <= 0).
	SocketTimeout time.Duration
}

// HubResult reports a finished hub setup. The join command is produced by
// the daemon (Local API NodeJoinCommand); the CLI calls it afterwards.
type HubResult struct {
	ConfigPath    string
	CAFingerprint string
	CAReused      bool
	ControlPort   int
	PublicIP      string
	PublicIP6     string
	// PrivateIP is true when PublicIP was detected and is not a public
	// address (the detect_ip step carried DEY-I021 as a warning).
	PrivateIP bool
	// SysctlProfile is the applied profile, "off" when none was applied.
	SysctlProfile  string
	SysctlWarnings []string
	// FirewallManaged is security.firewall_managed as written.
	FirewallManaged bool
	ServiceStarted  bool
}

// SetupHub sets this server up as a hub (spec sections 1, 3, 5, 11, 12).
// It refuses with DEY-I013 when Root/etc/deyroute/config.yaml exists. Steps:
// detect_ip, ca, hub_cert, firewall, sysctl, config, service (see the
// package documentation for the order). A failing step returns DEY-I014
// {step} wrapping the cause; secrets already present are reused by the next
// run.
func SetupHub(ctx context.Context, o HubOptions) (*HubResult, error) {
	e := newEnv(envOptions{
		Root: o.Root, Runner: o.Runner, Now: o.Now, LookupGroup: o.LookupGroup, LookupUser: o.LookupUser, Chown: o.Chown,
		SocketPath: o.SocketPath, SocketTimeout: o.SocketTimeout, Progress: o.Progress, Logger: o.Logger,
	})
	if err := e.refuseConfigured(); err != nil {
		return nil, err
	}
	fail := func(step string, err error) error {
		e.rep.fail(step, err)
		return stepError(deyerr.I014, step, err)
	}
	checkPort := o.CheckPort
	if checkPort == nil {
		checkPort = func(ctx context.Context, port int) error {
			return CheckControlPort(ctx, e.runner, e.root, port)
		}
	}
	res := &HubResult{ConfigPath: e.configPath()}

	// detect_ip
	e.rep.start(StepDetectIP)
	ip4 := strings.TrimSpace(o.PublicIP)
	if ip4 == "" {
		ip, private, err := DetectPublicIP(ctx, e.runner)
		if err != nil {
			return nil, fail(StepDetectIP, err)
		}
		ip4, res.PrivateIP = ip, private
	}
	ip6 := strings.TrimSpace(o.PublicIP6)
	if ip6 == "" && !o.NoIPv6 {
		ip, private, err := DetectPublicIP6(ctx, e.runner)
		switch {
		case err != nil:
			e.log.Warn("IPv6 detection failed; continuing without IPv6", slog.String("err", err.Error()))
		case ip != "" && !private:
			ip6 = ip
		}
	}
	if sameIP(ip4, ip6) {
		// hub.public_ip is already this IPv6 address (an IPv6-only hub).
		ip6 = ""
	}
	detail := ip4
	if ip6 != "" {
		detail += ", " + ip6
	}
	if res.PrivateIP {
		e.rep.warn(StepDetectIP, detail, deyerr.New(deyerr.I021, deyerr.Params{"ip": ip4}))
	} else {
		e.rep.ok(StepDetectIP, detail)
	}
	res.PublicIP, res.PublicIP6 = ip4, ip6

	// The configuration is built and validated before anything is written;
	// problems are reported under the config step.
	name := strings.TrimSpace(o.Name)
	port := o.ControlPort
	if port == 0 {
		port = SuggestControlPort(config.DefaultControlPort, func(p int) bool { return checkPort(ctx, p) != nil })
	}
	cfg := config.NewHub(name, ip4, port)
	cfg.Hub.PublicIP6 = ip6
	cfg.Hub.Mirror = strings.TrimSpace(o.Mirror)
	profile := strings.TrimSpace(o.SysctlProfile)
	if profile == "" {
		profile = config.SysctlBalanced
	}
	if !o.ApplySysctl {
		profile = config.SysctlOff
	}
	cfg.Tuning.SysctlProfile = profile
	cfg.Security.FirewallManaged = !o.NoFirewall
	if err := cfg.Validate(validateOptions()); err != nil {
		return nil, fail(StepConfig, err)
	}
	if err := checkPort(ctx, port); err != nil {
		return nil, fail(StepConfig, err)
	}
	res.ControlPort = port

	// ca (with the directory layout and the backend user it needs)
	e.rep.start(StepCA)
	userWarn := e.ensureSystemUser(ctx)
	if err := e.ensureLayout(); err != nil {
		return nil, fail(StepCA, err)
	}
	ca, reused, err := e.loadOrCreateCA(name)
	if err != nil {
		return nil, fail(StepCA, err)
	}
	res.CAFingerprint, res.CAReused = ca.Fingerprint(), reused
	e.rep.okOrWarn(StepCA, res.CAFingerprint, userWarn)

	// hub_cert
	e.rep.start(StepHubCert)
	ips := []net.IP{net.ParseIP(ip4)}
	if ip6 != "" {
		ips = append(ips, net.ParseIP(ip6))
	}
	if err := e.ensureHubCert(ca, name, ips); err != nil {
		return nil, fail(StepHubCert, err)
	}
	e.rep.ok(StepHubCert, detail)

	// firewall: the join window is open (the first join follows setup).
	e.rep.start(StepFirewall)
	if !cfg.Security.FirewallManaged {
		e.rep.skip(StepFirewall, "security.firewall_managed: false")
	} else {
		spec := render.FirewallSpec(cfg, nil, true, render.DefaultUnknownControlRate)
		err := firewall.Apply(ctx, e.runner, spec)
		switch {
		case err == nil:
			e.rep.ok(StepFirewall, "table inet deyroute")
		case deyerr.HasCode(err, deyerr.X030):
			// No nft on this system: deyroute cannot manage its table, so it
			// only suggests commands (security.firewall_managed: false).
			cfg.Security.FirewallManaged = false
			e.rep.warn(StepFirewall, "nft is not installed; firewall_managed set to false", err)
		default:
			return nil, fail(StepFirewall, err)
		}
	}
	res.FirewallManaged = cfg.Security.FirewallManaged

	// sysctl (only with the owner's confirmation, spec section 12)
	e.rep.start(StepSysctl)
	res.SysctlProfile = config.SysctlOff
	if !o.ApplySysctl {
		e.rep.skip(StepSysctl, config.SysctlOff)
	} else {
		var warnings []string
		var err error
		if profile == config.SysctlAuto {
			warnings, err = e.applyAutoProfile(ctx, config.RoleHub, o.TunePlan, cfg.Tuning.BBR, HubReserved(cfg))
		} else {
			warnings, err = applySysctl(e.root, profile, cfg.Tuning.BBR, false)
		}
		res.SysctlWarnings = warnings
		switch {
		case err != nil:
			// Tuning never blocks setup: nothing is recorded as applied.
			cfg.Tuning.SysctlProfile = config.SysctlOff
			e.rep.warn(StepSysctl, profile, err)
		case len(warnings) > 0:
			res.SysctlProfile = profile
			e.rep.warn(StepSysctl, sysctlDetail(profile, warnings), nil)
		default:
			res.SysctlProfile = profile
			e.rep.ok(StepSysctl, profile)
		}
	}

	// config
	e.rep.start(StepConfig)
	if err := config.SaveWith(res.ConfigPath, cfg, validateOptions()); err != nil {
		return nil, fail(StepConfig, err)
	}
	e.rep.ok(StepConfig, res.ConfigPath)

	// service
	if err := e.serviceStep(ctx, o.StartService, systemd.HubUnit, false); err != nil {
		return nil, serviceFailed(fail(StepService, err), systemd.HubUnit)
	}
	res.ServiceStarted = o.StartService
	return res, nil
}

// serviceStep runs the service step for unit (skipped when !start).
func (e *env) serviceStep(ctx context.Context, start bool, unit string, restart bool) error {
	e.rep.start(StepService)
	if !start {
		e.rep.skip(StepService, unit)
		return nil
	}
	if err := e.startService(ctx, unit, restart); err != nil {
		return err
	}
	e.rep.ok(StepService, unit)
	return nil
}

// sameIP reports whether a and b are the same valid address.
func sameIP(a, b string) bool {
	x, err1 := netip.ParseAddr(a)
	y, err2 := netip.ParseAddr(b)
	return err1 == nil && err2 == nil && x.Unmap() == y.Unmap()
}

// applySysctl applies profile with BBR as configured; ipForward adds
// net.ipv4.ip_forward = 1 (only when a WireGuard transport exists, which is
// never the case during setup or join).
func applySysctl(root, profile string, bbr, ipForward bool) ([]string, error) {
	_, warnings, err := sysctl.Manager{Root: root}.ApplyWith(sysctl.ApplyOptions{Profile: profile, BBR: bbr, IPForward: ipForward})
	return warnings, err
}

// loadOrCreateCA reuses a valid CA in secrets/ (a previous setup run that
// failed before writing config.yaml) or creates and saves a new one.
func (e *env) loadOrCreateCA(name string) (*tlsutil.CA, bool, error) {
	certPath, keyPath := e.secret(FileCACert), e.secret(FileCAKey)
	ca, err := tlsutil.LoadCA(certPath, keyPath)
	if err == nil {
		ca.Now = e.now
		return ca, true, nil
	}
	if !deyerr.HasCode(err, deyerr.T007) {
		// Without config.yaml no node can have joined with it.
		e.log.Warn("the existing CA cannot be used; creating a new one", slog.String("err", err.Error()))
	}
	ca, err = tlsutil.NewCA(name, e.now())
	if err != nil {
		return nil, false, err
	}
	ca.Now = e.now
	if err := ca.Save(certPath, keyPath); err != nil {
		return nil, false, err
	}
	return ca, false, nil
}

// ensureHubCert keeps a usable hub control certificate or issues a new one
// (CN = hub name, SANs = the public IPs, 10 years).
func (e *env) ensureHubCert(ca *tlsutil.CA, name string, ips []net.IP) error {
	certPath, keyPath := e.secret(FileHubCert), e.secret(FileHubKey)
	if hubCertUsable(ca, certPath, keyPath, name, ips, e.now()) {
		return nil
	}
	certPEM, keyPEM, err := ca.IssueServer(name, ips, nil, tlsutil.HubCertValidity)
	if err != nil {
		return err
	}
	return tlsutil.WriteSecretPair(certPath, certPEM, keyPath, keyPEM)
}

// hubCertUsable reports whether the stored hub certificate belongs to ca,
// matches its key, carries the hub role, name and every IP, and is not due
// for renewal.
func hubCertUsable(ca *tlsutil.CA, certPath, keyPath, name string, ips []net.IP, now time.Time) bool {
	certPEM, err := os.ReadFile(certPath) // #nosec G304 -- fixed secret path under Root
	if err != nil {
		return false
	}
	keyPEM, err := os.ReadFile(keyPath) // #nosec G304 -- fixed secret path under Root
	if err != nil {
		return false
	}
	cert, err := tlsutil.ParseCert(certPEM)
	if err != nil || tlsutil.CertRole(cert) != tlsutil.HubOU || cert.Subject.CommonName != name {
		return false
	}
	key, err := tlsutil.ParsePrivateKey(keyPEM)
	if err != nil || !tlsutil.KeyMatchesCert(cert, key) {
		return false
	}
	if tlsutil.NeedsRenewal(certPEM, now, tlsutil.RenewBefore) {
		return false
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return false
	}
	for _, want := range ips {
		found := false
		for _, have := range cert.IPAddresses {
			if have.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
