package hub

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	"github.com/localroot4/deyroute/internal/doctor"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// acmeStub is an injectable ObtainACME.
type acmeStub struct {
	mu    sync.Mutex
	ca    *tlsutil.CA
	fail  error
	calls []tlsutil.ACMEOptions
	now   time.Time // issue time of the certificates (zero = now)
}

func newACMEStub(t *testing.T) *acmeStub {
	ca, err := tlsutil.NewCA("acme test", time.Now())
	require.NoError(t, err)
	return &acmeStub{ca: ca}
}

func (a *acmeStub) obtain(_ context.Context, o tlsutil.ACMEOptions) ([]byte, []byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, o)
	if a.fail != nil {
		return nil, nil, a.fail
	}
	now := a.now
	if now.IsZero() {
		now = time.Now()
	}
	return a.ca.IssueTunnel("acme", nil, []string{o.Domain}, now)
}

func (a *acmeStub) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

// certOf finds the certificate of kind (and tunnel) in list.
func certOf(list []api.CertInfo, kind, tunnel string) (api.CertInfo, bool) {
	for _, c := range list {
		if c.Kind == kind && c.Tunnel == tunnel {
			return c, true
		}
	}
	return api.CertInfo{}, false
}

func TestSecurityTLSShowAndRenew(t *testing.T) {
	acme := newACMEStub(t)
	te := startTunnelHub(t, func(o *Options, _ string) { o.ObtainACME = acme.obtain })
	te.tunnelNode("de-1")
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})

	list, err := te.client.SecurityTLSShow(ctx, "")
	require.NoError(t, err)
	ca, ok := certOf(list, "ca", "")
	require.True(t, ok)
	require.Equal(t, te.ca.Fingerprint(), ca.Fingerprint)
	require.Greater(t, ca.DaysLeft, 3650)
	_, ok = certOf(list, "hub", "")
	require.True(t, ok)
	node, ok := certOf(list, "node", "")
	require.True(t, ok)
	require.Equal(t, "de-1", node.Subject)
	require.Greater(t, node.DaysLeft, 3600, "the join recorded the node certificate")
	require.Empty(t, node.Warning)
	_, ok = certOf(list, "tunnel", "main")
	require.False(t, ok, "no TLS rung: no certificate yet")

	list, err = te.client.SecurityTLSRenew(ctx, "main")
	require.NoError(t, err)
	require.Len(t, list, 1)
	first := list[0]
	require.Equal(t, "tunnel", first.Kind)
	require.Equal(t, config.TLSModeAuto, first.Mode)
	require.Equal(t, "tunnel-main", first.Subject)
	require.Contains(t, first.SANs, "127.0.0.1")
	require.InDelta(t, 3*365, first.DaysLeft, 2)
	list, err = te.client.SecurityTLSRenew(ctx, "main")
	require.NoError(t, err)
	require.NotEqual(t, first.Fingerprint, list[0].Fingerprint, "issued again")

	// ACME: the first request fails (acme_failed, internal certificate),
	// the second succeeds.
	_, err = te.h.mutate(func(c *config.Config) error {
		c.Hub.Domain = "vpn.example.com"
		c.Hub.ACME = &config.ACME{Email: "owner@example.com"}
		tp, _ := c.Tunnel("main")
		tp.TLS.Mode = config.TLSModeACME
		return nil
	})
	require.NoError(t, err)
	acme.fail = deyerr.New(deyerr.T003, deyerr.Params{"domain": "vpn.example.com"})
	list, err = te.client.SecurityTLSRenew(ctx, "main")
	require.NoError(t, err)
	require.Equal(t, config.TLSModeAuto, list[0].Mode)
	ev := te.waitEvent(state.EvACMEFailed, "")
	require.Equal(t, "main", ev.Tunnel)
	require.Equal(t, string(deyerr.T003), ev.Code)
	acme.mu.Lock()
	acme.fail = nil
	acme.mu.Unlock()
	list, err = te.client.SecurityTLSRenew(ctx, "main")
	require.NoError(t, err)
	require.Equal(t, config.TLSModeACME, list[0].Mode)
	require.Contains(t, list[0].SANs, "vpn.example.com")
	require.Equal(t, 2, acme.count())
	o := acme.calls[1]
	require.Equal(t, "vpn.example.com", o.Domain)
	require.Equal(t, "owner@example.com", o.Email)
	require.Equal(t, "127.0.0.1", o.ExpectedIP)
	require.Equal(t, filepath.Join(te.root, config.SecretsDir, ACMEAccountDir), o.AccountDir)

	// The daily pass leaves a valid ACME certificate alone...
	te.h.renewDue(ctx)
	require.Equal(t, 2, acme.count())
	// ...and renews one close to its expiry.
	acme.mu.Lock()
	acme.now = time.Now().Add(-tlsutil.TunnelCertValidity + 10*24*time.Hour)
	certPEM, keyPEM, err := acme.ca.IssueTunnel("acme", nil, []string{"vpn.example.com"}, acme.now)
	acme.now = time.Time{}
	acme.mu.Unlock()
	require.NoError(t, err)
	require.NoError(t, te.h.secretStore().StoreACME("main", certPEM, keyPEM))
	te.h.renewDue(ctx)
	require.Equal(t, 3, acme.count())
	list, err = te.client.SecurityTLSShow(ctx, "main")
	require.NoError(t, err)
	require.Greater(t, list[0].DaysLeft, 1000)

	// Back to auto; an internal certificate close to its expiry is renewed.
	_, err = te.h.mutate(func(c *config.Config) error {
		tp, _ := c.Tunnel("main")
		tp.TLS.Mode = config.TLSModeAuto
		return nil
	})
	require.NoError(t, err)
	old, oldKey, err := te.ca.IssueTunnel("main", []net.IP{net.ParseIP("127.0.0.1")}, []string{"vpn.example.com"},
		time.Now().Add(-tlsutil.TunnelCertValidity+5*24*time.Hour))
	require.NoError(t, err)
	dir := te.h.secretStore().TLSPath("main")
	require.NoError(t, tlsutil.WriteSecretPair(filepath.Join(dir, secrets.CertFile), old, filepath.Join(dir, secrets.KeyFile), oldKey))
	list, err = te.client.SecurityTLSShow(ctx, "main")
	require.NoError(t, err)
	require.Contains(t, list[0].Warning, string(deyerr.T006))
	te.h.renewDue(ctx)
	list, err = te.client.SecurityTLSShow(ctx, "main")
	require.NoError(t, err)
	require.Empty(t, list[0].Warning)
	require.Greater(t, list[0].DaysLeft, 1000)

	// Custom files that do not exist cannot be renewed.
	_, err = te.h.mutate(func(c *config.Config) error {
		tp, _ := c.Tunnel("main")
		tp.TLS = config.TLS{Mode: config.TLSModeCustom, CertFile: "/nonexistent/cert.pem", KeyFile: "/nonexistent/key.pem"}
		return nil
	})
	require.NoError(t, err)
	_, err = te.client.SecurityTLSRenew(ctx, "main")
	require.Equal(t, deyerr.T008, codeOf(err))
	_, err = te.client.SecurityTLSRenew(ctx, "nope")
	require.Equal(t, deyerr.C021, codeOf(err))
}

func TestExpiryWarning(t *testing.T) {
	now := time.Now()
	require.Empty(t, expiryWarning("x", now.Add(30*24*time.Hour), now))
	require.Contains(t, expiryWarning("x", now.Add(3*24*time.Hour), now), string(deyerr.T006))
	require.Contains(t, expiryWarning("x", now.Add(-time.Hour), now), string(deyerr.T001))
}

func TestObtainACMEOptions(t *testing.T) {
	acme := newACMEStub(t)
	env := startHub(t, nil, func(o *Options, _ string) { o.ObtainACME = acme.obtain })
	cfg := config.Clone(env.h.Config())
	_, _, err := env.h.obtainACME(ctxT(t), cfg)
	require.Equal(t, deyerr.T003, codeOf(err), "no domain")
	cfg.Hub.Domain = "vpn.example.com"
	cfg.Hub.ACME = &config.ACME{DisableHTTP01: true}
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.Equal(t, deyerr.T003, codeOf(err), "no challenge left")
	cfg.Hub.ACME = &config.ACME{CloudflareTokenFile: "/etc/deyroute/secrets/cf.token", Staging: true}
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.Equal(t, deyerr.S009, codeOf(err), "token file missing")
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(env.root, config.SecretsDir, "cf.token"), []byte("cf-secret-token-1234567890\n")))
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.NoError(t, err)
	require.Equal(t, "cf-secret-token-1234567890", acme.calls[0].CloudflareToken)
	require.True(t, acme.calls[0].Staging)
	require.Equal(t, 30*24*time.Hour, acmeRenewBefore(env.h.Config()))
	cfg.Hub.ACME.RenewBeforeDays = 10
	require.Equal(t, 10*24*time.Hour, acmeRenewBefore(cfg))
}

func TestSecurityRotateTokens(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	te.waitActive("main", "de-1", trAlpha)
	sec := te.h.secretStore()
	before, err := sec.Token("main")
	require.NoError(t, err)

	var log stepLog
	require.NoError(t, te.client.SecurityRotateTokens(ctx, "main", log.add))
	after, err := sec.Token("main")
	require.NoError(t, err)
	require.NotEqual(t, before, after)
	require.Contains(t, log.finished(), "rotate:main:ok")

	// Every rung has the new token on both sides...
	for _, tr := range []string{trAlpha, trBeta} {
		inst := systemd.InstanceName("main", "de-1", tr)
		b, name, _ := strings.Cut(tr, "/")
		data, err := os.ReadFile(filepath.Join(te.root, config.BackendsConfDir, b, "main", "de-1", name, "test.conf"))
		require.NoError(t, err)
		require.Contains(t, string(data), "token="+after)
		n.tmu.Lock()
		require.Contains(t, string(n.rendered[inst].Files["test.conf"]), "token="+after)
		n.tmu.Unlock()
	}
	// ...and only the active transport restarted (hub and node).
	require.Equal(t, 1, te.sd.count("restart", hubUnit("main", "de-1", trAlpha)))
	require.Equal(t, 0, te.sd.count("restart", hubUnit("main", "de-1", trBeta)))
	require.Contains(t, n.restartedList(), systemd.InstanceName("main", "de-1", trAlpha))
	require.NotContains(t, n.restartedList(), systemd.InstanceName("main", "de-1", trBeta))
	ev := te.waitEvent(state.EvConfigApplied, "")
	require.Equal(t, "main", ev.Tunnel)
	te.waitActive("main", "de-1", trAlpha)

	// A disabled tunnel: its warm files change, nothing restarts.
	require.NoError(t, te.client.TunnelSetEnabled(ctx, "main", false))
	restarts := te.sd.count("restart", hubUnit("main", "de-1", trAlpha))
	require.NoError(t, te.client.SecurityRotateTokens(ctx, "", nil))
	third, err := sec.Token("main")
	require.NoError(t, err)
	require.NotEqual(t, after, third)
	data, err := os.ReadFile(filepath.Join(te.root, config.BackendsConfDir, "tfa", "main", "de-1", "alpha", "test.conf"))
	require.NoError(t, err)
	require.Contains(t, string(data), "token="+third)
	require.Equal(t, restarts, te.sd.count("restart", hubUnit("main", "de-1", trAlpha)))

	// With its node offline the rotation warns (the node syncs later).
	require.NoError(t, te.client.TunnelSetEnabled(ctx, "main", true))
	n.stop()
	te.waitOnline("de-1", false)
	log = stepLog{}
	require.NoError(t, te.client.SecurityRotateTokens(ctx, "main", log.add))
	require.Contains(t, log.finished(), "rotate:main:warn")
}

func TestDoctorCollect(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	n.skew.Store(int64(5 * time.Minute))
	require.Eventually(t, func() bool {
		d := te.h.clockSkews()["de-1"]
		return d > 4*time.Minute
	}, testWait, 20*time.Millisecond)
	// A join token created an hour ago is still stored.
	_, err := te.client.NodeJoinCommand(ctx, time.Hour*2)
	require.NoError(t, err)
	p := te.h.joins.Path
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	var f map[string]any
	require.NoError(t, json.Unmarshal(data, &f))
	for _, tok := range f["tokens"].([]any) {
		tok.(map[string]any)["created"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	}
	data, err = json.Marshal(f)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	jt := te.h.joinTokenFacts()
	require.Equal(t, 1, jt.long, "a valid token made with --ttl 2h")
	require.Zero(t, jt.expired)

	d, err := te.client.DoctorCollect(ctx, "")
	require.NoError(t, err)
	require.Equal(t, config.RoleHub, d.Role)
	for _, s := range []string{doctor.SectionStatus, doctor.SectionEvents, doctor.SectionLadder, doctor.SectionPortChecks,
		doctor.SectionOS, doctor.SectionVersions, doctor.SectionUnits, doctor.SectionCerts} {
		require.Contains(t, d.Sections, s)
	}
	require.Contains(t, d.Sections[doctor.SectionLadder], "tunnel main")
	require.Contains(t, d.Sections[doctor.SectionLadder], "ACTIVE")
	require.Contains(t, d.Sections[doctor.SectionLadder], "probe ")
	require.Contains(t, d.Sections[doctor.SectionPortChecks], "1 bind:")
	rules := map[string]bool{}
	for _, fd := range d.Findings {
		rules[fd.Rule] = true
	}
	require.True(t, rules[doctor.RuleClockSkew], "%v", d.Findings)
	require.True(t, rules[doctor.RuleSecrets], "%v", d.Findings)

	// The node's own data.
	n.on(api.CmdDoctorCollect, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.DoctorData{Role: config.RoleNode, Sections: map[string]string{doctor.SectionOS: "Test OS"}}, nil
	})
	nd, err := te.client.DoctorCollect(ctx, "de-1")
	require.NoError(t, err)
	require.Equal(t, config.RoleNode, nd.Role)
	require.Equal(t, "Test OS", nd.Sections[doctor.SectionOS])
	_, err = te.client.DoctorCollect(ctx, "nope")
	require.Equal(t, deyerr.N008, codeOf(err))
	n.stop()
	te.waitOnline("de-1", false)
	_, err = te.client.DoctorCollect(ctx, "de-1")
	require.Equal(t, deyerr.N003, codeOf(err))
}

func TestDoctorWithoutTunnels(t *testing.T) {
	env := startHub(t, nil)
	d, err := env.client.DoctorCollect(ctxT(t), "")
	require.NoError(t, err)
	require.Equal(t, "no tunnels\n", d.Sections[doctor.SectionLadder])
	require.Equal(t, "no tunnel ports\n", d.Sections[doctor.SectionPortChecks])
	require.Equal(t, joinTokenState{}, env.h.joinTokenFacts())
}

// TestDoctorJoinTokens (R15): an expired join token is removed when the
// doctor looks (a hub restart would not), a valid default token is no
// finding, and a valid long-TTL token is reported as info with its expiry.
func TestDoctorJoinTokens(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	_, err := env.client.NodeJoinCommand(ctx, 0)
	require.NoError(t, err)
	p := env.h.joins.Path
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	var f map[string]any
	require.NoError(t, json.Unmarshal(data, &f))
	now := time.Now().UTC()
	f["tokens"] = append(f["tokens"].([]any), map[string]any{"sha256": strings.Repeat("ab", 32),
		"created": now.Add(-time.Hour).Format(time.RFC3339), "expires": now.Add(-45 * time.Minute).Format(time.RFC3339)})
	data, err = json.Marshal(f)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, data, 0o600))

	require.Equal(t, joinTokenState{}, env.h.joinTokenFacts())
	n, err := env.h.joins.Count()
	require.NoError(t, err)
	require.Equal(t, 1, n, "the valid token stays")
	data, err = os.ReadFile(p)
	require.NoError(t, err)
	require.NotContains(t, string(data), strings.Repeat("ab", 32), "the expired token is gone")

	_, err = env.client.NodeJoinCommand(ctx, 2*time.Hour)
	require.NoError(t, err)
	d, err := env.client.DoctorCollect(ctx, "")
	require.NoError(t, err)
	var r15 []api.DoctorFinding
	for _, fd := range d.Findings {
		if fd.Rule == doctor.RuleSecrets {
			r15 = append(r15, fd)
		}
	}
	require.Len(t, r15, 1, "%v", d.Findings)
	require.Equal(t, doctor.SevInfo, r15[0].Severity)
	require.Contains(t, r15[0].Message, "1 join token(s) made with a TTL over 15 minutes are valid until")
}

// The domain, the ACME e-mail and the Cloudflare token are set through the
// Local API (menu 8 Security > TLS certificates, deyroute security tls
// domain|acme): checked, stored as a 0600 secret file (never in
// config.yaml or the log) and used by the next ACME request.
func TestACMESettings(t *testing.T) {
	acme := newACMEStub(t)
	te := startTunnelHub(t, func(o *Options, _ string) {
		o.ObtainACME = acme.obtain
		o.Logger = nil // real hub.log
	})
	te.tunnelNode("de-1")
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	str := func(s string) *string { return &s }
	set := func(r api.SettingsRequest) error { return te.client.SettingsSet(ctx, r) }

	// acme needs the domain first; the Fix names the command that sets it.
	_, err := te.client.TunnelEdit(ctx, "main", api.TunnelEditRequest{TLSMode: str(config.TLSModeACME)}, nil)
	require.Equal(t, deyerr.C013, codeOf(err))
	require.Contains(t, deyerr.As(err).Fix(), "deyroute security tls domain")
	st, err := te.client.Status(ctx)
	require.NoError(t, err)
	require.Empty(t, st.Hub.Domain)
	require.Equal(t, api.ACMEHTTP01, st.Hub.ACMEChallenge)

	for _, bad := range []string{"5.6.7.8", "*.example.com", "localhost", "a b.example.com"} {
		require.Equal(t, deyerr.C013, codeOf(set(api.SettingsRequest{Domain: str(bad)})), bad)
	}
	require.NoError(t, set(api.SettingsRequest{Domain: str(" VPN.Example.com. ")}))
	require.Equal(t, "vpn.example.com", te.h.Config().Hub.Domain)
	_, err = te.client.TunnelEdit(ctx, "main", api.TunnelEditRequest{TLSMode: str(config.TLSModeACME)}, nil)
	require.NoError(t, err)
	// The domain cannot go while a tunnel uses acme.
	err = set(api.SettingsRequest{Domain: str("")})
	require.Equal(t, deyerr.C013, codeOf(err))
	require.Contains(t, deyerr.As(err).Fix(), "deyroute tunnel edit main --tls-mode auto")
	require.Equal(t, "vpn.example.com", te.h.Config().Hub.Domain)

	require.Equal(t, deyerr.C013, codeOf(set(api.SettingsRequest{ACMEEmail: str("not an address")})))
	require.NoError(t, set(api.SettingsRequest{ACMEEmail: str(" owner@example.com ")}))

	// The token: an absolute path, a readable file holding one token.
	const token = "cfTok_0123456789abcdefghijklmnopqrstuvwx"
	src := filepath.Join(te.root, "root", "cloudflare.token")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o700))
	require.Equal(t, deyerr.C013, codeOf(set(api.SettingsRequest{CloudflareTokenFile: str("cloudflare.token")})))
	require.Equal(t, deyerr.S009, codeOf(set(api.SettingsRequest{CloudflareTokenFile: str("/root/cloudflare.token")})))
	require.NoError(t, os.WriteFile(src, []byte("CF_API_TOKEN="+token+"\n"), 0o600))
	err = set(api.SettingsRequest{CloudflareTokenFile: str("/root/cloudflare.token")})
	require.Equal(t, deyerr.S009, codeOf(err))
	require.NotContains(t, deyerr.As(err).Error(), token, "the file content is never quoted")
	require.NoError(t, os.WriteFile(src, []byte(token+"\n"), 0o600))
	require.NoError(t, set(api.SettingsRequest{CloudflareTokenFile: str("/root/cloudflare.token")}))
	require.NoError(t, os.Remove(src), "the owner's file may be deleted afterwards")

	cfg := te.h.Config()
	require.Equal(t, &config.ACME{Email: "owner@example.com", CloudflareTokenFile: config.DefaultCloudflareTokenFile}, cfg.Hub.ACME)
	stored := filepath.Join(te.root, config.DefaultCloudflareTokenFile)
	fi, err := os.Stat(stored)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	data, err := os.ReadFile(stored) // #nosec G304 -- test file
	require.NoError(t, err)
	require.Equal(t, token, strings.TrimSpace(string(data)))
	yaml, err := os.ReadFile(filepath.Join(te.root, config.DefaultPath)) // #nosec G304 -- test file
	require.NoError(t, err)
	require.NotContains(t, string(yaml), token)
	require.Contains(t, string(yaml), "cloudflare_token_file: "+config.DefaultCloudflareTokenFile)
	st, err = te.client.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, api.ACMEDNS01, st.Hub.ACMEChallenge)
	require.Equal(t, "owner@example.com", st.Hub.ACMEEmail)

	// The next request uses DNS-01 with the stored token and the e-mail.
	list, err := te.client.SecurityTLSRenew(ctx, "main")
	require.NoError(t, err)
	require.Equal(t, config.TLSModeACME, list[0].Mode)
	require.Contains(t, list[0].SANs, "vpn.example.com")
	acme.mu.Lock()
	last := acme.calls[len(acme.calls)-1]
	acme.mu.Unlock()
	require.Equal(t, token, last.CloudflareToken)
	require.Equal(t, "owner@example.com", last.Email)

	// A new domain makes the certificate of the old one count as missing:
	// the daily pass requests it again.
	require.NoError(t, set(api.SettingsRequest{Domain: str("new.example.com")}))
	tun, ok := te.h.Config().Tunnel("main")
	require.True(t, ok)
	_, mode := te.h.tunnelCertFile(*tun, "vpn.example.com", time.Now())
	require.Equal(t, config.TLSModeACME, mode)
	_, mode = te.h.tunnelCertFile(*tun, "new.example.com", time.Now())
	require.Equal(t, config.TLSModeAuto, mode)
	before := acme.count()
	te.h.renewDue(ctx)
	require.Equal(t, before+1, acme.count())
	acme.mu.Lock()
	require.Equal(t, "new.example.com", acme.calls[before].Domain)
	acme.mu.Unlock()
	list, err = te.client.SecurityTLSShow(ctx, "main")
	require.NoError(t, err)
	require.Equal(t, config.TLSModeACME, list[0].Mode)
	require.Contains(t, list[0].SANs, "new.example.com")

	// Removing the token deletes the stored copy (HTTP-01 again); removing
	// the e-mail too leaves no hub.acme section.
	require.NoError(t, set(api.SettingsRequest{CloudflareTokenFile: str("")}))
	require.NoFileExists(t, stored)
	require.Equal(t, &config.ACME{Email: "owner@example.com"}, te.h.Config().Hub.ACME)
	require.NoError(t, set(api.SettingsRequest{ACMEEmail: str("")}))
	require.Nil(t, te.h.Config().Hub.ACME)
	st, err = te.client.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, api.ACMEHTTP01, st.Hub.ACMEChallenge)
	require.Empty(t, st.Hub.ACMEEmail)

	logData, err := os.ReadFile(filepath.Join(te.root, LogFile)) // #nosec G304 -- test file
	require.NoError(t, err)
	require.Contains(t, string(logData), "settings changed")
	require.NotContains(t, string(logData), token)
}

// Every ACME Fix line names a command that exists.
func TestACMEFixTexts(t *testing.T) {
	acme := newACMEStub(t)
	env := startHub(t, nil, func(o *Options, _ string) { o.ObtainACME = acme.obtain })
	cfg := config.Clone(env.h.Config())
	_, _, err := env.h.obtainACME(ctxT(t), cfg)
	require.Contains(t, deyerr.As(err).Fix(), "deyroute security tls domain <name>")
	cfg.Hub.Domain = "vpn.example.com"
	cfg.Hub.ACME = &config.ACME{DisableHTTP01: true}
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.Contains(t, deyerr.As(err).Fix(), "deyroute security tls acme --cloudflare-token-file")
	// A missing token file and a failed DNS-01 request point at the token
	// setting, not at port 80.
	cfg.Hub.ACME = &config.ACME{CloudflareTokenFile: config.DefaultCloudflareTokenFile}
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.Equal(t, deyerr.S009, codeOf(err))
	require.Contains(t, deyerr.As(err).Fix(), "deyroute security tls acme --cloudflare-token-file")
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(env.root, config.DefaultCloudflareTokenFile), []byte("cfTok_0123456789abcdefghij\n")))
	acme.fail = deyerr.New(deyerr.T003, deyerr.Params{"domain": "vpn.example.com"})
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	e := deyerr.As(err)
	require.Equal(t, deyerr.T003, e.Code)
	require.Contains(t, e.Fix(), "Zone:DNS:Edit")
	require.NotContains(t, e.Fix(), "port 80")
	// A Fix the ACME client chose itself is kept.
	acme.fail = deyerr.New(deyerr.T003, deyerr.Params{"domain": "vpn.example.com"}).WithFix("own fix")
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.Equal(t, "own fix", deyerr.As(err).Fix())
	// The default T003 Fix names the DNS-01 command too.
	require.Contains(t, deyerr.New(deyerr.T003, deyerr.Params{"domain": "d"}).Fix(), "deyroute security tls acme --cloudflare-token-file")

	require.Equal(t, api.ACMEHTTP01, acmeChallenge(cfg2(nil)))
	require.Equal(t, api.ACMEDNS01, acmeChallenge(cfg2(&config.ACME{CloudflareTokenFile: "/x", DisableHTTP01: true})))
	require.Equal(t, api.ACMENone, acmeChallenge(cfg2(&config.ACME{DisableHTTP01: true})))
	require.Equal(t, api.ACMEHTTP01, acmeChallenge(cfg2(&config.ACME{Email: "a@b.c"})))
}

// cfg2 returns a hub configuration with hub.acme a.
func cfg2(a *config.ACME) *config.Config {
	c := config.NewHub("ir-1", "127.0.0.1", 44433)
	c.Hub.ACME = a
	return c
}
