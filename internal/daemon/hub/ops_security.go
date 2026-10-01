package hub

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// Security operation constants.
const (
	// FilePrevCACert is the previous CA certificate kept in secrets/ while a
	// rotate-ca is in progress (still trusted on the control channel).
	FilePrevCACert = "ca.prev.crt"
	// ACMEAccountDir holds the ACME account (secrets/acme/, section 10).
	ACMEAccountDir = "acme"
	// Firewall actions of SecurityFirewall.
	FirewallShow    = "show"
	FirewallApply   = "apply"
	FirewallDisable = "disable"
	// Audit checks (api.AuditItem.Check).
	AuditPorts      = "public-ports"
	AuditSecrets    = "secret-permissions"
	AuditCerts      = "certificates"
	AuditVersions   = "node-versions"
	AuditJoinTokens = "join-tokens"
	AuditFirewall   = "firewall"
	AuditControl    = "control-port"
	// Audit severities.
	sevOK    = "ok"
	sevWarn  = "warn"
	sevError = "error"
	// auditTimeout bounds the socket listing.
	auditTimeout = 15 * time.Second
	// oldJoinToken is the age after which a stored join token counts as old
	// (doctor rule R15).
	oldJoinToken = 15 * time.Minute
)

// ---------------------------------------------------------------- firewall

// SecurityFirewall implements api.Local (`deyroute security firewall
// show|apply|disable`, section 11): show lists table inet deyroute (or the
// table deyroute would apply when it does not manage the firewall), the
// firewalls found on the hub and the commands that open the deyroute ports an
// external firewall blocks; apply sets security.firewall_managed and applies
// the table now; disable removes table inet deyroute and stops managing it.
func (l *local) SecurityFirewall(ctx context.Context, action string) (api.FirewallInfo, error) {
	h := l.h
	switch strings.TrimSpace(action) {
	case "", FirewallShow:
	case FirewallApply:
		if err := h.setFirewallManaged(true); err != nil {
			return api.FirewallInfo{}, withLog(err)
		}
		if err := h.applyFirewall(ctx); err != nil {
			return api.FirewallInfo{}, withLog(err)
		}
		h.log.Info("firewall applied by the owner")
	case FirewallDisable:
		if err := h.setFirewallManaged(false); err != nil {
			return api.FirewallInfo{}, withLog(err)
		}
		if !h.o.DisableFirewall {
			if err := firewall.Remove(ctx, h.o.Runner); err != nil {
				return api.FirewallInfo{}, withLog(err)
			}
		}
		_ = h.applyFirewall(ctx) // records the unmanaged state (runs no nft)
		h.log.Warn("firewall management disabled by the owner: table inet deyroute removed", dlog.Code(deyerr.P031))
	default:
		return api.FirewallInfo{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "action", "value": action, "allowed": "show, apply, disable",
		}))
	}
	return h.firewallInfo(ctx)
}

// setFirewallManaged saves security.firewall_managed (automatic backup
// first) when it changes.
func (h *Hub) setFirewallManaged(on bool) error {
	if firewallManaged(h.Config()) == on {
		return nil
	}
	if _, err := h.autoBackup(); err != nil {
		return err
	}
	_, err := h.mutate(func(c *config.Config) error {
		if c.Security == nil {
			c.Security = config.DefaultSecurity()
		}
		c.Security.FirewallManaged = on
		return nil
	})
	return err
}

// firewallInfo is SecurityFirewall's show.
func (h *Hub) firewallInfo(ctx context.Context) (api.FirewallInfo, error) {
	cfg := h.Config()
	info := api.FirewallInfo{Managed: firewallManaged(cfg), Detected: []string{}}
	for _, k := range firewall.Detect(ctx, h.o.Runner) {
		info.Detected = append(info.Detected, string(k))
	}
	if info.Managed {
		rs, err := firewall.Show(ctx, h.o.Runner)
		if err != nil {
			return info, withLog(err)
		}
		info.Ruleset = rs
	} else {
		fw := h.Firewall()
		if !fw.Computed {
			_ = h.applyFirewall(ctx)
			fw = h.Firewall()
		}
		info.Ruleset = firewall.Render(fw.Spec)
		info.Suggested = append(info.Suggested, "deyroute security firewall apply")
	}
	for _, pp := range h.deyroutePorts(cfg) {
		v, err := firewall.Check(ctx, h.o.Runner, pp.port, pp.proto)
		if err != nil || !v.Blocked {
			continue
		}
		info.Suggested = append(info.Suggested, v.Commands...)
	}
	return info, nil
}

// portProto is one port the hub must accept.
type portProto struct {
	port  int
	proto string
}

// deyroutePorts returns the control port and every tunnel listen port.
func (h *Hub) deyroutePorts(cfg *config.Config) []portProto {
	out := []portProto{{cfg.Hub.ControlPort, config.ProtoTCP}}
	seen := map[portProto]bool{out[0]: true}
	for _, t := range cfg.Tunnels {
		for _, pm := range t.Ports {
			pp := portProto{pm.Listen, pm.Proto}
			if !seen[pp] {
				seen[pp] = true
				out = append(out, pp)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- audit

// SecurityAudit implements api.Local (`deyroute security audit`, section 11):
// public listening sockets that are neither tunnel ports, backend control
// ports nor the control port; secret file permissions; certificate expiry;
// nodes with an older version; unexpired join tokens; the firewall and the
// control port restriction. Clean is true only when nothing warns.
func (l *local) SecurityAudit(ctx context.Context) (api.AuditReport, error) {
	h := l.h
	cfg := h.Config()
	var items []api.AuditItem
	add := func(check, sev, msg string) {
		items = append(items, api.AuditItem{Check: check, Severity: sev, Message: dlog.Redact(msg)})
	}
	for _, it := range h.auditPorts(ctx, cfg) {
		add(AuditPorts, it.Severity, it.Message)
	}
	if probs := h.secretStore().CheckPerms(); len(probs) > 0 {
		for _, p := range probs {
			e := deyerr.As(p)
			add(AuditSecrets, sevError, string(e.Code)+" "+e.Message())
		}
	} else {
		add(AuditSecrets, sevOK, "every file in "+config.SecretsDir+" is 0600 root and the directory 0700")
	}
	certs, err := h.tlsShow(cfg, "")
	if err != nil {
		add(AuditCerts, sevWarn, "certificates could not be read: "+deyerr.As(err).Message())
	}
	certProblems := 0
	for _, c := range certs {
		if c.Warning == "" {
			continue
		}
		certProblems++
		sev := sevWarn
		if c.DaysLeft < 0 || strings.Contains(c.Warning, string(deyerr.T001)) {
			sev = sevError
		}
		add(AuditCerts, sev, c.Warning)
	}
	if certProblems == 0 && err == nil {
		add(AuditCerts, sevOK, fmt.Sprintf("%d certificates, none expires within 14 days", len(certs)))
	}
	old := 0
	for _, n := range cfg.Nodes {
		ns, _ := h.nodeState(n.ID)
		switch {
		case ns.AgentVersion == "":
		case !ns.Compatible:
			old++
			add(AuditVersions, sevError, "node "+n.ID+" runs "+ns.AgentVersion+", incompatible with the hub "+version.Version+": update it (deyroute update)")
		case ns.AgentVersion != version.Version:
			old++
			add(AuditVersions, sevWarn, "node "+n.ID+" runs "+ns.AgentVersion+", the hub runs "+version.Version+": update it (deyroute update)")
		}
	}
	if old == 0 {
		add(AuditVersions, sevOK, "every node runs the hub's version "+version.Version)
	}
	if n, err := h.joins.Count(); err != nil {
		add(AuditJoinTokens, sevWarn, "join tokens could not be read: "+deyerr.As(err).Message())
	} else if n > 0 {
		add(AuditJoinTokens, sevWarn, fmt.Sprintf("%d unexpired join token(s): the control port is open to every address until they expire", n))
	} else {
		add(AuditJoinTokens, sevOK, "no unexpired join token")
	}
	fw := h.Firewall()
	switch {
	case !firewallManaged(cfg):
		e := deyerr.New(deyerr.P031, nil)
		add(AuditFirewall, sevWarn, string(e.Code)+" "+e.Message())
	case fw.Err != nil:
		e := deyerr.As(fw.Err)
		add(AuditFirewall, sevError, string(e.Code)+" "+e.Message())
	default:
		add(AuditFirewall, sevOK, "table inet deyroute is managed by deyroute")
	}
	if cfg.Security != nil && !cfg.Security.RestrictControlToNodes {
		add(AuditControl, sevWarn, fmt.Sprintf("security.restrict_control_to_nodes is false: control port %d/tcp accepts every address", cfg.Hub.ControlPort))
	} else {
		add(AuditControl, sevOK, fmt.Sprintf("control port %d/tcp accepts only joined nodes (and joins while a token is valid)", cfg.Hub.ControlPort))
	}
	rep := api.AuditReport{Items: items, Clean: true}
	for _, it := range items {
		if it.Severity != sevOK {
			rep.Clean = false
		}
	}
	return rep, nil
}

// listener is one listening socket of the hub.
type listener struct {
	proto   string
	addr    string
	port    int
	process string
}

// auditPorts lists the public listening sockets (not loopback) that are not
// deyroute's (tunnel listen ports, backend control ports, the control port).
// SSH is expected and reported as ok; DHCP clients are ignored.
func (h *Hub) auditPorts(ctx context.Context, cfg *config.Config) []api.AuditItem {
	cctx, cancel := context.WithTimeout(ctx, auditTimeout)
	defer cancel()
	stdout, _, err := h.o.Runner.Run(cctx, "ss", []string{"-Hlntup"}, nil)
	if err != nil {
		e := deyerr.As(err)
		return []api.AuditItem{{Severity: sevWarn, Message: "listening sockets could not be listed: " + string(e.Code) + " " + e.Message()}}
	}
	ours := map[portProto]bool{{cfg.Hub.ControlPort, config.ProtoTCP}: true}
	for _, pp := range h.deyroutePorts(cfg) {
		ours[pp] = true
	}
	var out []api.AuditItem
	seen := map[string]bool{}
	for _, l := range parseListeners(stdout) {
		if ours[portProto{l.port, l.proto}] || (l.port >= config.CtlRangeLow && l.port <= config.CtlRangeHigh) {
			continue
		}
		if l.proto == config.ProtoUDP && (l.port == 68 || l.port == 546) {
			continue // DHCP clients
		}
		key := fmt.Sprintf("%d/%s", l.port, l.proto)
		if seen[key] {
			continue
		}
		seen[key] = true
		desc := key + " on " + l.addr
		if l.process != "" {
			desc += " (" + l.process + ")"
		}
		if l.proto == config.ProtoTCP && l.port == config.SSHPort {
			out = append(out, api.AuditItem{Severity: sevOK, Message: desc + " is SSH"})
			continue
		}
		out = append(out, api.AuditItem{Severity: sevWarn, Message: desc + " is open to the internet and not used by deyroute; close it if it is not needed"})
	}
	if len(out) == 0 {
		out = append(out, api.AuditItem{Severity: sevOK, Message: "only deyroute ports listen on public addresses"})
	}
	return out
}

// parseListeners reads `ss -Hlntup` output and keeps the sockets bound to a
// non-loopback address.
func parseListeners(data []byte) []listener {
	var out []listener
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 {
			continue
		}
		proto := f[0]
		if proto != config.ProtoTCP && proto != config.ProtoUDP {
			continue
		}
		local := f[4]
		i := strings.LastIndexByte(local, ':')
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil {
			continue
		}
		host := strings.Trim(local[:i], "[]")
		if j := strings.IndexByte(host, '%'); j >= 0 {
			host = host[:j]
		}
		if host != "*" {
			if a, err := netip.ParseAddr(host); err == nil && a.IsLoopback() {
				continue
			}
		}
		l := listener{proto: proto, addr: local, port: port}
		if j := strings.Index(sc.Text(), `(("`); j >= 0 {
			rest := sc.Text()[j+3:]
			if k := strings.IndexByte(rest, '"'); k > 0 {
				l.process = rest[:k]
			}
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].port != out[j].port {
			return out[i].port < out[j].port
		}
		return out[i].proto < out[j].proto
	})
	return out
}

// ---------------------------------------------------------------- TLS show / renew

// SecurityTLSShow implements api.Local (`deyroute security tls show
// [--tunnel]`, section 10): the internal CA, the hub control certificate,
// the node certificates and every tunnel certificate with expiry,
// fingerprint and a warning within 14 days of expiry (DEY-T006) or after
// it (DEY-T001). With a tunnel, only that tunnel's certificate.
func (l *local) SecurityTLSShow(_ context.Context, tunnel string) ([]api.CertInfo, error) {
	out, err := l.h.tlsShow(l.h.Config(), tunnel)
	return out, withLog(err)
}

// tlsShow lists the certificates (tunnel "" = all).
func (h *Hub) tlsShow(cfg *config.Config, tunnel string) ([]api.CertInfo, error) {
	tunnels, err := tunnelsOf(cfg, tunnel)
	if err != nil {
		return nil, err
	}
	now := h.now()
	out := []api.CertInfo{}
	if strings.TrimSpace(tunnel) == "" {
		dir := filepath.Join(config.SecretsDir)
		if ci, ok := h.certFile("ca", "", "", filepath.Join(dir, setup.FileCACert), now); ok {
			out = append(out, ci)
		}
		if ci, ok := h.certFile("ca", "", "", filepath.Join(dir, FilePrevCACert), now); ok {
			ci.Subject += " (previous, rotation not finished)"
			ci.Warning = "the previous CA is still trusted: finish the rotation with deyroute security rotate-ca"
			out = append(out, ci)
		}
		if ci, ok := h.certFile("hub", "", "", filepath.Join(dir, setup.FileHubCert), now); ok {
			out = append(out, ci)
		}
		for _, n := range cfg.Nodes {
			ci := api.CertInfo{Kind: "node", Subject: n.ID, Fingerprint: n.CertFingerprint}
			var rec nodeCertRecord
			if ok, err := h.st.GetMeta(metaNodeCert+n.ID, &rec); err == nil && ok && sameFingerprint(rec.Fingerprint, n.CertFingerprint) {
				ci.NotAfter = rec.NotAfter
				ci.DaysLeft = daysLeft(rec.NotAfter, now)
				ci.Warning = expiryWarning("node "+n.ID, rec.NotAfter, now)
			}
			out = append(out, ci)
		}
	}
	for _, t := range tunnels {
		file, mode := h.tunnelCertFile(t, cfg.Hub.Domain, now)
		if ci, ok := h.certFile("tunnel", t.ID, mode, file, now); ok {
			out = append(out, ci)
		}
	}
	return out, nil
}

// certFile describes the certificate at the system path file (below Root
// unless it is a custom certificate outside /etc/deyroute).
func (h *Hub) certFile(kind, tunnel, mode, file string, now time.Time) (api.CertInfo, bool) {
	data, err := os.ReadFile(h.path(file)) // #nosec G304 -- certificate paths below Root
	if err != nil {
		return api.CertInfo{}, false
	}
	info, err := tlsutil.CertInfo(data)
	if err != nil {
		return api.CertInfo{Tunnel: tunnel, Kind: kind, Mode: mode, Subject: file, Warning: deyerr.As(err).Message()}, true
	}
	label := info.Subject
	if tunnel != "" {
		label = "tunnel " + tunnel
	}
	return api.CertInfo{
		Tunnel: tunnel, Kind: kind, Mode: mode, Subject: info.Subject, SANs: info.SANs,
		NotAfter: info.NotAfter, DaysLeft: info.DaysLeft(now), Fingerprint: info.Fingerprint,
		Warning: expiryWarning(label, info.NotAfter, now),
	}, true
}

// daysLeft is the number of whole days until notAfter.
func daysLeft(notAfter, now time.Time) int {
	return int(notAfter.Sub(now) / (24 * time.Hour))
}

// expiryWarning is the DEY-T001 / T006 line of a certificate ("" when it is
// valid for more than 14 days).
func expiryWarning(label string, notAfter, now time.Time) string {
	switch {
	case !now.Before(notAfter):
		e := deyerr.New(deyerr.T001, deyerr.Params{"path": label, "expiry": notAfter.UTC().Format("2006-01-02")})
		return string(e.Code) + " " + e.Message() + ": " + e.Fix()
	case notAfter.Sub(now) <= tlsutil.WarnBefore:
		e := deyerr.New(deyerr.T006, deyerr.Params{"path": label, "days": daysLeft(notAfter, now)})
		return string(e.Code) + " " + e.Message() + ": " + e.Fix()
	}
	return ""
}

// tunnelCertFile returns the certificate a tunnel serves and its mode: the
// owner's file (custom), the ACME certificate while it is valid for domain
// (hub.domain; acme, else the internal one: "auto", as secrets.TunnelTLS
// decides), or the internal one (auto). An ACME certificate of a previous
// domain therefore counts as missing and is requested again.
func (h *Hub) tunnelCertFile(t config.Tunnel, domain string, now time.Time) (file, mode string) {
	auto := filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.CertFile)
	switch t.TLS.Mode {
	case config.TLSModeCustom:
		return t.TLS.CertFile, config.TLSModeCustom
	case config.TLSModeACME:
		acme := filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.ACMEDir, secrets.CertFile)
		if data, err := os.ReadFile(h.path(acme)); err == nil && domain != "" { // #nosec G304 -- fixed secrets path below Root
			if c, err := tlsutil.ParseCert(data); err == nil && now.Before(c.NotAfter) && c.VerifyHostname(domain) == nil {
				return acme, config.TLSModeACME
			}
		}
		return auto, config.TLSModeAuto
	}
	return auto, config.TLSModeAuto
}

// hubIPs returns the parsed public addresses of the hub (tunnel TLS SANs).
func hubIPs(cfg *config.Config) []net.IP {
	var out []net.IP
	for _, s := range []string{cfg.Hub.PublicIP, cfg.Hub.PublicIP6} {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

// SecurityTLSRenew implements api.Local (`deyroute security tls renew
// [--tunnel]`, section 10): internal (auto) certificates are issued again,
// ACME certificates are requested again (a failure keeps the tunnel on the
// internal certificate and emits acme_failed; the tunnel never goes down),
// custom certificates are validated again; every affected tunnel is
// rendered again and its active transport restarts when its files changed.
func (l *local) SecurityTLSRenew(ctx context.Context, tunnel string) ([]api.CertInfo, error) {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return nil, withLog(err)
	}
	defer unlock()
	h.ops.certMu.Lock()
	defer h.ops.certMu.Unlock()
	cfg := h.Config()
	tunnels, err := tunnelsOf(cfg, tunnel)
	if err != nil {
		return nil, withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return nil, withLog(err)
	}
	acme := &acmeRun{}
	var errs []error
	for _, t := range tunnels {
		prev := h.servedCert(t)
		if err := h.renewTunnelCert(ctx, cfg, t, acme, true); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := h.renderRenewed(ctx, t, prev); err != nil {
			errs = append(errs, err)
		}
	}
	out, err := h.tlsShow(h.Config(), tunnel)
	if err != nil {
		return nil, withLog(err)
	}
	if len(errs) > 0 {
		return out, withLog(errs[0])
	}
	return out, nil
}

// acmeRun obtains the ACME certificate of hub.domain once per renewal pass
// (every acme tunnel serves the same domain; Let's Encrypt limits
// duplicate certificates).
type acmeRun struct {
	done      bool
	cert, key []byte
	err       error
}

// obtain returns the certificate of the pass, requesting it the first time.
func (a *acmeRun) obtain(ctx context.Context, h *Hub, cfg *config.Config) ([]byte, []byte, error) {
	if !a.done {
		a.done = true
		a.cert, a.key, a.err = h.obtainACME(ctx, cfg)
	}
	return a.cert, a.key, a.err
}

// Fix texts that name the commands (and menu items) setting the ACME
// options of section 10.
const (
	fixSetDomain     = "set the domain: deyroute security tls domain <name> (menu: 8) Security > TLS certificates > Domain (for ACME))"
	fixSetCloudflare = "deyroute security tls acme --cloudflare-token-file F (menu, Advanced: 8) Security > TLS certificates > Cloudflare token (DNS-01))"
	fixThenRenew     = ", then: deyroute security tls renew"
)

// acmeChallenge is how tls.mode acme proves hub.domain (api.HubStatus):
// DNS-01 when a Cloudflare token file is set, else HTTP-01 on port 80
// unless hub.acme.disable_http01.
func acmeChallenge(cfg *config.Config) string {
	a := cfg.Hub.ACME
	switch {
	case a == nil:
		return api.ACMEHTTP01
	case strings.TrimSpace(a.CloudflareTokenFile) != "":
		return api.ACMEDNS01
	case a.DisableHTTP01:
		return api.ACMENone
	}
	return api.ACMEHTTP01
}

// obtainACME requests a certificate for hub.domain (HTTP-01 on port 80, or
// DNS-01 through Cloudflare with hub.acme.cloudflare_token_file); the
// account lives in secrets/acme/.
func (h *Hub) obtainACME(ctx context.Context, cfg *config.Config) ([]byte, []byte, error) {
	domain := strings.TrimSpace(cfg.Hub.Domain)
	if domain == "" {
		return nil, nil, deyerr.New(deyerr.T003, deyerr.Params{"domain": "(none)"}).
			WithWhy("tls.mode acme needs hub.domain").WithFix(fixSetDomain + fixThenRenew)
	}
	o := tlsutil.ACMEOptions{
		Domain:     domain,
		AccountDir: h.path(filepath.Join(config.SecretsDir, ACMEAccountDir)),
		ExpectedIP: cfg.Hub.PublicIP,
	}
	tokenFile := ""
	if a := cfg.Hub.ACME; a != nil {
		o.Email, o.Staging = a.Email, a.Staging
		if tokenFile = strings.TrimSpace(a.CloudflareTokenFile); tokenFile != "" {
			tok, err := secrets.CloudflareToken(h.path(tokenFile))
			if err != nil {
				return nil, nil, deyerr.As(err).WithFix("set the token file again: " + fixSetCloudflare)
			}
			o.CloudflareToken = tok
		} else if a.DisableHTTP01 {
			return nil, nil, deyerr.New(deyerr.T003, deyerr.Params{"domain": domain}).
				WithWhy("HTTP-01 is disabled (hub.acme.disable_http01) and no Cloudflare token file is set for DNS-01").
				WithFix("set a Cloudflare API token for DNS-01: " + fixSetCloudflare +
					", or allow HTTP-01 again (remove hub.acme.disable_http01 with deyroute config edit)" + fixThenRenew)
		}
	}
	cert, key, err := h.o.ObtainACME(ctx, o)
	if err != nil && tokenFile != "" {
		if e := deyerr.As(err); e.Code == deyerr.T003 && e.FixOverride == "" {
			// DNS-01 does not use port 80: the default Fix would mislead.
			return nil, nil, e.WithFix("check that the Cloudflare API token in " + tokenFile + " may edit the DNS zone of " +
				domain + " (Zone:DNS:Edit); set another one with " + fixSetCloudflare + fixThenRenew)
		}
	}
	return cert, key, err
}

// renewTunnelCert renews the certificate of one tunnel. force issues an
// internal certificate again even when it is not due (tls renew); without
// it only certificates within their renewal window are renewed (daily
// pass). ACME failures emit acme_failed and leave the internal certificate
// in use; they are not returned.
func (h *Hub) renewTunnelCert(ctx context.Context, cfg *config.Config, t config.Tunnel, acme *acmeRun, force bool) error {
	sec := h.secretStore()
	now := h.now()
	switch t.TLS.Mode {
	case config.TLSModeCustom:
		if err := tlsutil.ValidateCustom(t.TLS.CertFile, t.TLS.KeyFile, now); err != nil {
			return err
		}
		return nil
	case config.TLSModeACME:
		file, _ := h.tunnelCertFile(t, cfg.Hub.Domain, now)
		if !force && file != filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.CertFile) && !h.certDue(file, now, acmeRenewBefore(cfg)) {
			return nil
		}
		certPEM, keyPEM, err := acme.obtain(ctx, h, cfg)
		if err == nil {
			err = sec.StoreACME(t.ID, certPEM, keyPEM)
		}
		if err != nil {
			h.acmeFailed(t.ID, err)
			// The internal certificate stays in use (issued when missing).
			_, terr := sec.TunnelTLS(t.ID, config.TLSModeAuto, hubIPs(cfg), cfg.Hub.Domain, "", "")
			return terr
		}
		h.log.Info("ACME certificate renewed", dlog.Tunnel(t.ID), slog.String("domain", cfg.Hub.Domain))
		return nil
	}
	dir := sec.TLSPath(t.ID)
	certPath := filepath.Join(dir, secrets.CertFile)
	if !force && !h.certDue(filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.CertFile), now, tlsutil.RenewBefore) {
		return nil
	}
	if force {
		for _, p := range []string{certPath, filepath.Join(dir, secrets.KeyFile)} {
			if err := os.Remove(p); err != nil && !stderrors.Is(err, os.ErrNotExist) {
				return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
			}
		}
	}
	if _, err := sec.TunnelTLS(t.ID, config.TLSModeAuto, hubIPs(cfg), cfg.Hub.Domain, "", ""); err != nil {
		return err
	}
	h.log.Info("tunnel certificate issued again", dlog.Tunnel(t.ID))
	return nil
}

// acmeRenewBefore is hub.acme.renew_before_days (30 days by default).
func acmeRenewBefore(cfg *config.Config) time.Duration {
	if cfg.Hub.ACME != nil && cfg.Hub.ACME.RenewBeforeDays > 0 {
		return time.Duration(cfg.Hub.ACME.RenewBeforeDays) * 24 * time.Hour
	}
	return tlsutil.RenewBefore
}

// certDue reports whether the certificate at the system path file is
// missing, unreadable or expires within before.
func (h *Hub) certDue(file string, now time.Time, before time.Duration) bool {
	data, err := os.ReadFile(h.path(file)) // #nosec G304 -- certificate paths below Root
	if err != nil {
		return true
	}
	return tlsutil.NeedsRenewal(data, now, before)
}

// acmeFailed reports a failed ACME request (section 10: acme_failed; the
// tunnel keeps working with the internal certificate).
func (h *Hub) acmeFailed(tunnel string, err error) {
	e := deyerr.As(err)
	h.log.Warn("ACME certificate could not be obtained; the tunnel uses the internal certificate",
		dlog.Tunnel(tunnel), dlog.Err(err), dlog.Code(e.Code))
	h.Emit(state.Event{Type: state.EvACMEFailed, Level: state.LevelWarn, Tunnel: tunnel, Code: string(e.Code),
		Reason: dlog.Redact(e.Why()), Message: "Tunnel " + tunnel + ": " + e.Message() + "; using the internal certificate"})
}

// renewDue is the daily renewal pass (section 10): internal certificates
// within 30 days of expiry are issued again, ACME certificates within
// hub.acme.renew_before_days (or missing) are requested again, and the
// tunnels whose certificate changed are rendered again (their active
// transport restarts).
func (h *Hub) renewDue(ctx context.Context) {
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return
	}
	defer unlock()
	h.ops.certMu.Lock()
	defer h.ops.certMu.Unlock()
	cfg := h.Config()
	now := h.now()
	acme := &acmeRun{}
	for _, t := range cfg.Tunnels {
		if ctx.Err() != nil {
			return
		}
		before, _ := h.tunnelCertFile(t, cfg.Hub.Domain, now)
		due := false
		switch t.TLS.Mode {
		case config.TLSModeCustom:
			if h.certDue(t.TLS.CertFile, now, tlsutil.WarnBefore) {
				h.log.Warn("the custom certificate of a tunnel expires soon; replace the files", dlog.Tunnel(t.ID), dlog.Code(deyerr.T006))
			}
			continue
		case config.TLSModeACME:
			due = before == filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.CertFile) || h.certDue(before, now, acmeRenewBefore(cfg))
		default:
			due = regularFile(h.path(before)) && h.certDue(before, now, tlsutil.RenewBefore)
		}
		if !due {
			continue
		}
		prev := h.servedCert(t)
		if err := h.renewTunnelCert(ctx, cfg, t, acme, false); err != nil {
			h.log.Warn("tunnel certificate renewal failed", dlog.Tunnel(t.ID), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
			continue
		}
		_ = h.renderRenewed(ctx, t, prev)
	}
}

// servedCert describes the certificate tunnel t serves now (zero when it
// has none yet).
func (h *Hub) servedCert(t config.Tunnel) api.CertInfo {
	now := h.now()
	file, mode := h.tunnelCertFile(t, h.Config().Hub.Domain, now)
	ci, _ := h.certFile("tunnel", t.ID, mode, file, now)
	return ci
}

// renderRenewed renders tunnel t again after a certificate renewal (its
// active transport restarts when its files changed) and, when the served
// certificate changed, emits config_applied with the mode, the new expiry
// and the restarted transport (section 10: the active transport restarts
// with a notification).
func (h *Hub) renderRenewed(ctx context.Context, t config.Tunnel, prev api.CertInfo) error {
	restarted, err := h.rerender(ctx, t, nil)
	if err != nil {
		h.log.Warn("tunnel not rendered after a certificate renewal", dlog.Tunnel(t.ID), dlog.Err(err))
	}
	cur := h.servedCert(t)
	if cur.Fingerprint == "" || cur.Fingerprint == prev.Fingerprint {
		return err
	}
	msg := fmt.Sprintf("Tunnel %s: certificate renewed (%s, expires %s)", t.ID, cur.Mode, cur.NotAfter.UTC().Format("2006-01-02"))
	if restarted != "" {
		msg += "; active transport " + restarted + " restarted"
	}
	h.opsEvent(t.ID, msg)
	return err
}

// ---------------------------------------------------------------- rotate tokens

// SecurityRotateTokens implements api.Local (`deyroute security
// rotate-tokens [--tunnel]`, section 11): the backend token of the tunnel
// (or of every tunnel) is replaced, every rung is rendered again on the hub
// and the nodes, and the active transport restarts with one coordinated
// restart (server side first). A node that is offline gets the new token
// when it reconnects.
func (l *local) SecurityRotateTokens(ctx context.Context, tunnel string, progress func(api.Step)) error {
	h := l.h
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return withLog(err)
	}
	defer unlock()
	cfg := h.Config()
	tunnels, err := tunnelsOf(cfg, tunnel)
	if err != nil {
		return withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	rep := &steps{progress: progress}
	var first error
	for _, t := range tunnels {
		id := "rotate:" + t.ID
		title := i18n.T(i18n.HubTitleRotate, t.ID)
		rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepRunning})
		tok, err := h.secretStore().RotateToken(t.ID)
		if err == nil {
			dlog.RegisterSecret(tok)
			_, err = h.rerender(ctx, t, rep)
		}
		var offline []string
		for _, n := range t.Nodes {
			if !h.Online(n) {
				offline = append(offline, n)
			}
		}
		switch {
		case err != nil && !deyerr.HasCode(err, deyerr.N003):
			rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepFailed, Error: api.ToDTO(err)})
			h.log.Error("backend token rotation failed", dlog.Tunnel(t.ID), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
			if first == nil {
				first = err
			}
			continue
		case len(offline) > 0:
			w := deyerr.New(deyerr.N003, deyerr.Params{"node": offline[0]})
			rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepWarn,
				Detail: "offline (updated when they reconnect): " + strings.Join(offline, ", "), Error: api.ToDTO(w)})
		default:
			rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepOK})
		}
		h.log.Info("backend token rotated", dlog.Tunnel(t.ID))
		h.opsEvent(t.ID, "Tunnel "+t.ID+": backend token rotated")
	}
	return withLog(first)
}
