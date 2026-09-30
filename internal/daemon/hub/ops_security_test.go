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
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(env.root, config.SecretsDir, "cf.token"), []byte("cf-secret-token-123\n")))
	_, _, err = env.h.obtainACME(ctxT(t), cfg)
	require.NoError(t, err)
	require.Equal(t, "cf-secret-token-123", acme.calls[0].CloudflareToken)
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
	require.Equal(t, 1, te.h.oldJoinTokens())

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
	require.Zero(t, env.h.oldJoinTokens())
}
