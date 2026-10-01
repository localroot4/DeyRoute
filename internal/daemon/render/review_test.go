package render

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// TestPlanACMEEffectiveModeAndTrustBundle: backends see the TLS mode that
// is actually served, and ca.crt lets clients verify a certificate that is
// not issued by the internal CA.
func TestPlanACMEEffectiveModeAndTrustBundle(t *testing.T) {
	e := newEnv(t)
	e.cfg.Hub.Domain = "tun.example.com"
	e.cfg.Tunnels[0].TLS = config.TLS{Mode: config.TLSModeACME}
	e.cfg.Tunnels[0].Ladder = config.LadderRef{Inline: []string{"rev/tls", "rev/plain"}}

	// No ACME certificate yet: fallback to the internal certificate. The
	// backend must render auto (pin the internal CA), not acme.
	plan, err := Plan(e.input())
	require.NoError(t, err)
	require.Equal(t, config.TLSModeAuto, plan.TLS.Mode)
	ri, ok := e.fakes["rev"].lastInput("de-1", "tls")
	require.True(t, ok)
	require.Equal(t, config.TLSModeAuto, ri.Tunnel.TLS.Mode, "fallback renders as auto")
	c, _ := plan.Candidate("de-1", "rev/tls")
	require.Equal(t, e.ca.CertPEM, c.NodeSide.Files[FileCA])
	require.Equal(t, config.TLSModeACME, e.cfg.Tunnels[0].TLS.Mode, "the config is not modified")

	// A stored ACME certificate from another CA (standing in for LE).
	le, err := tlsutil.NewCA("Fake LE", t0)
	require.NoError(t, err)
	le.Now = func() time.Time { return t0 }
	certPEM, keyPEM, err := le.IssueServer("tun.example.com", nil, []string{"tun.example.com"}, 90*24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, e.sec.StoreACME("main", append(append([]byte(nil), certPEM...), le.CertPEM...), keyPEM))

	plan, err = Plan(e.input())
	require.NoError(t, err)
	require.Equal(t, config.TLSModeACME, plan.TLS.Mode)
	ri, _ = e.fakes["rev"].lastInput("de-1", "tls")
	require.Equal(t, config.TLSModeACME, ri.Tunnel.TLS.Mode)
	c, _ = plan.Candidate("de-1", "rev/tls")
	bundle := string(c.NodeSide.Files[FileCA])
	require.True(t, strings.HasPrefix(bundle, string(e.ca.CertPEM)), "internal CA first")
	require.Contains(t, bundle, strings.TrimSpace(string(certPEM)), "served leaf is trusted")
	require.Contains(t, bundle, strings.TrimSpace(string(le.CertPEM)), "served chain is trusted")
	require.NotContains(t, bundle, "PRIVATE KEY")
	require.Equal(t, "/etc/deyroute/backends/rev/main/de-1/tls/ca.crt", ri.Secrets.CAFile)

	// The client really verifies the served certificate with ca.crt.
	cert, err := tlsutil.ParseCert(c.Hub.Files[FileTLSCert])
	require.NoError(t, err)
	pool, err := poolOf([]byte(bundle))
	require.NoError(t, err)
	_, err = cert.Verify(verifyOpts(pool, "tun.example.com", t0))
	require.NoError(t, err)

	// A non-TLS rung keeps the plain internal CA.
	plain, _ := plan.Candidate("de-1", "rev/plain")
	require.Equal(t, e.ca.CertPEM, plain.NodeSide.Files[FileCA])
}

// TestPlanCustomTrustBundle: tls.mode custom with a self-signed owner
// certificate is verifiable through ca.crt.
func TestPlanCustomTrustBundle(t *testing.T) {
	e := newEnv(t)
	own, err := tlsutil.NewCA("Owner CA", t0)
	require.NoError(t, err)
	own.Now = func() time.Time { return t0 }
	certPEM, keyPEM, err := own.IssueServer("vpn.example.com", nil, []string{"vpn.example.com"}, 365*24*time.Hour)
	require.NoError(t, err)
	dir := t.TempDir()
	cp, kp := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	require.NoError(t, os.WriteFile(cp, append(append([]byte(nil), certPEM...), own.CertPEM...), 0o600))
	require.NoError(t, os.WriteFile(kp, keyPEM, 0o600))
	e.cfg.Tunnels[0].TLS = config.TLS{Mode: config.TLSModeCustom, CertFile: cp, KeyFile: kp}
	e.cfg.Tunnels[0].Ladder = config.LadderRef{Inline: []string{"rev/tls"}}
	e.sec.Now = func() time.Time { return t0 }

	plan, err := Plan(e.input())
	require.NoError(t, err)
	require.Equal(t, config.TLSModeCustom, plan.TLS.Mode)
	c, ok := plan.Candidate("de-1", "rev/tls")
	require.True(t, ok)
	pool, err := poolOf(c.NodeSide.Files[FileCA])
	require.NoError(t, err)
	cert, err := tlsutil.ParseCert(c.Hub.Files[FileTLSCert])
	require.NoError(t, err)
	_, err = cert.Verify(verifyOpts(pool, "vpn.example.com", t0))
	require.NoError(t, err)
}

func TestPlanRequiresConfigAndDedupes(t *testing.T) {
	e := newEnv(t)
	in := e.input()
	in.Cfg = nil
	_, err := Plan(in)
	require.Error(t, err)
	_, err = CanaryPlan(in, 7777)
	require.Error(t, err)

	in = e.input()
	in.Tunnel.Nodes = []string{"de-1", "de-1", "nl-1"}
	in.Tunnel.Ladder = config.LadderRef{Inline: []string{"rev/plain", "rev/plain", "native/relay"}}
	plan, err := Plan(in)
	require.NoError(t, err)
	require.Equal(t, []string{"rev/plain", "native/relay"}, plan.Ladder)
	require.Equal(t, []string{"de-1|rev/plain", "de-1|native/relay", "nl-1|rev/plain", "nl-1|native/relay"}, candidateIDs(plan))
	require.Len(t, Desired([]TunnelPlan{plan}), 4)
}

func TestCanaryUDPProbe(t *testing.T) {
	e := newEnv(t)
	e.cfg.Tunnels[0].Ports = []config.PortMap{{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"}}
	e.cfg.Tunnels[0].Ladder = config.LadderRef{Inline: []string{"udponly/udp"}}
	_, err := CanaryPlan(e.input(), 7777)
	require.NoError(t, err)
	ri, _ := e.fakes["udponly"].lastInput("de-1", "udp")
	require.Empty(t, ri.Tunnel.Ports[0].Probe, "no TCP probe kind on a UDP port map")
}

// TestHubWriterValidatesBeforeWriting: an invalid instance or an empty
// drop-in is refused before the config directory is created.
func TestHubWriterValidatesBeforeWriting(t *testing.T) {
	w, _, _ := newWriter(t)
	ctx := context.Background()
	const dir = "/etc/deyroute/backends/rev/main/de-1/tls"
	s := side(dir, map[string]string{"config.toml": "a"})
	s.Instance = "bad instance/../x"
	_, err := w.Write(ctx, s)
	require.Equal(t, deyerr.X034, deyerr.As(err).Code)
	_, err = os.Stat(filepath.Join(w.Root, dir))
	require.True(t, os.IsNotExist(err), "nothing written")

	s = side(dir, map[string]string{"config.toml": "a"})
	s.DropIn = nil
	_, err = w.Write(ctx, s)
	require.Equal(t, deyerr.X034, deyerr.As(err).Code)
	_, err = os.Stat(filepath.Join(w.Root, dir))
	require.True(t, os.IsNotExist(err), "nothing written")
}

// TestHubWriterRemoveCanaryKeepsNodeCanary: removing the canary of a
// tunnel must not delete the warm directories of a node named "canary",
// which live below the canary directory.
func TestHubWriterRemoveCanaryKeepsNodeCanary(t *testing.T) {
	w, _, _ := newWriter(t)
	ctx := context.Background()
	node := side("/etc/deyroute/backends/rev/main/canary/tls", map[string]string{"config.toml": "node"})
	node.Instance = "main.canary.rev-tls"
	_, err := w.Write(ctx, node)
	require.NoError(t, err)
	canary := side(CanaryConfigDir("rev", "main"), map[string]string{"config.toml": "canary", "sub/x": "y"})
	canary.Instance = "main.canary"
	_, err = w.Write(ctx, canary)
	require.NoError(t, err)

	require.NoError(t, w.Remove(ctx, "main.canary", CanaryConfigDir("rev", "main")))
	got, err := os.ReadFile(filepath.Join(w.Root, "/etc/deyroute/backends/rev/main/canary/tls/config.toml"))
	require.NoError(t, err, "node canary's warm files survive")
	require.Equal(t, "node", string(got))
	for _, gone := range []string{"config.toml", "sub"} {
		_, err = os.Stat(filepath.Join(w.Root, CanaryConfigDir("rev", "main"), gone))
		require.True(t, os.IsNotExist(err), gone)
	}

	// Removing node canary's instance then removes the now empty parents.
	require.NoError(t, w.Remove(ctx, "main.canary.rev-tls", "/etc/deyroute/backends/rev/main/canary/tls"))
	_, err = os.Stat(filepath.Join(w.Root, "/etc/deyroute/backends/rev/main"))
	require.True(t, os.IsNotExist(err))

	// A canary directory alone is removed completely (and idempotently).
	_, err = w.Write(ctx, canary)
	require.NoError(t, err)
	require.NoError(t, w.Remove(ctx, "main.canary", CanaryConfigDir("rev", "main")))
	_, err = os.Stat(filepath.Join(w.Root, "/etc/deyroute/backends/rev"))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, w.Remove(ctx, "main.canary", CanaryConfigDir("rev", "main")))
}

func TestCanaryParts(t *testing.T) {
	b, tun, ok := canaryParts("/etc/deyroute/backends/rev/main/canary")
	require.True(t, ok)
	require.Equal(t, "rev", b)
	require.Equal(t, "main", tun)
	for _, d := range []string{"/etc/deyroute/backends/rev/main/de-1/tls", "/etc/deyroute/backends/rev/main/canary/tls", "/etc/deyroute/backends/rev/canary"} {
		_, _, ok := canaryParts(d)
		require.False(t, ok, d)
	}
}

func poolOf(pemBytes []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("no certificate in bundle")
	}
	return pool, nil
}

func verifyOpts(pool *x509.CertPool, name string, now time.Time) x509.VerifyOptions {
	return x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
}
