package secrets

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

var hubIP = []net.IP{net.ParseIP("5.6.7.8")}

func TestTunnelTLSAutoIssueReuseRenew(t *testing.T) {
	s, clk := newStore(t)
	m, err := s.TunnelTLS("main", "auto", hubIP, "", "", "")
	require.NoError(t, err)
	require.Equal(t, "auto", m.Mode)
	require.NoError(t, m.Warning)
	require.Len(t, m.CertSHA256, 64)
	require.WithinDuration(t, t0.Add(tlsutil.TunnelCertValidity), m.NotAfter, 2*time.Hour)
	dir := s.TLSPath("main")
	requireMode(t, filepath.Join(dir, CertFile), 0o600)
	requireMode(t, filepath.Join(dir, KeyFile), 0o600)
	cert, err := tlsutil.ParseCert(m.CertPEM)
	require.NoError(t, err)
	require.Equal(t, "tunnel-main", cert.Subject.CommonName)
	require.NoError(t, cert.VerifyHostname("5.6.7.8"))

	// Same inputs: the stored certificate is reused.
	m2, err := s.TunnelTLS("main", "", hubIP, "", "", "")
	require.NoError(t, err)
	require.Equal(t, m.CertPEM, m2.CertPEM)

	// SAN change (domain added): re-issued with the domain.
	m3, err := s.TunnelTLS("main", "auto", hubIP, "Tunnel.Example.com.", "", "")
	require.NoError(t, err)
	require.NotEqual(t, m.CertPEM, m3.CertPEM)
	c3, err := tlsutil.ParseCert(m3.CertPEM)
	require.NoError(t, err)
	require.NoError(t, c3.VerifyHostname("tunnel.example.com"))
	m3b, err := s.TunnelTLS("main", "auto", hubIP, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, m3.CertPEM, m3b.CertPEM, "domain normalisation must not force a re-issue")

	// IPv6 added: re-issued.
	m4, err := s.TunnelTLS("main", "auto", []net.IP{net.ParseIP("5.6.7.8"), net.ParseIP("2001:db8::1"), nil}, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.NotEqual(t, m3.CertPEM, m4.CertPEM)

	// 29 days before expiry: renewed (30-day window).
	clk.t = m4.NotAfter.Add(-29 * 24 * time.Hour)
	s.CA.Now = clk.Now
	m5, err := s.TunnelTLS("main", "auto", []net.IP{net.ParseIP("5.6.7.8"), net.ParseIP("2001:db8::1")}, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.NotEqual(t, m4.CertPEM, m5.CertPEM)
	require.True(t, m5.NotAfter.After(m4.NotAfter))

	// A new CA (rotate-ca) re-issues.
	s.CA = newCA(t, clk.Now())
	m6, err := s.TunnelTLS("main", "auto", []net.IP{net.ParseIP("5.6.7.8"), net.ParseIP("2001:db8::1")}, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.NotEqual(t, m5.CertPEM, m6.CertPEM)
	c6, err := tlsutil.ParseCert(m6.CertPEM)
	require.NoError(t, err)
	require.NoError(t, c6.CheckSignatureFrom(s.CA.Cert))

	// Damaged key file: re-issued.
	require.NoError(t, os.WriteFile(filepath.Join(dir, KeyFile), []byte("junk"), 0o600))
	m7, err := s.TunnelTLS("main", "auto", []net.IP{net.ParseIP("5.6.7.8"), net.ParseIP("2001:db8::1")}, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.NotEqual(t, m6.CertPEM, m7.CertPEM)
}

func TestTunnelTLSAutoErrors(t *testing.T) {
	s, _ := newStore(t)
	s.CA = nil
	_, err := s.TunnelTLS("main", "auto", hubIP, "", "", "")
	require.Equal(t, deyerr.T007, code(t, err))

	s, _ = newStore(t)
	_, err = s.TunnelTLS("main", "auto", nil, "", "", "")
	require.Error(t, err, "no SAN at all")

	_, err = s.TunnelTLS("main", "letsencrypt", hubIP, "", "", "")
	require.Equal(t, deyerr.C013, code(t, err))
	_, err = s.TunnelTLS("Main", "auto", hubIP, "", "", "")
	require.Equal(t, deyerr.C007, code(t, err))

	// An IP literal in domain is an IP SAN.
	m, err := s.TunnelTLS("main", "auto", nil, "[2001:db8::5]", "", "")
	require.NoError(t, err)
	c, err := tlsutil.ParseCert(m.CertPEM)
	require.NoError(t, err)
	require.Len(t, c.IPAddresses, 1)
	require.Empty(t, c.DNSNames)
}

func TestTunnelTLSACME(t *testing.T) {
	s, clk := newStore(t)
	// No ACME certificate yet: auto with a T003 warning.
	m, err := s.TunnelTLS("main", "acme", hubIP, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, "auto", m.Mode)
	require.Equal(t, deyerr.T003, code(t, m.Warning))

	// Store an ACME certificate (here signed by another CA standing in for
	// Let's Encrypt).
	le := newCA(t, t0)
	certPEM, keyPEM, err := le.IssueServer("tunnel.example.com", nil, []string{"tunnel.example.com"}, 90*24*time.Hour)
	require.NoError(t, err)
	require.NoError(t, s.StoreACME("main", certPEM, keyPEM))
	requireMode(t, filepath.Join(s.TLSPath("main"), ACMEDir, KeyFile), 0o600)
	m2, err := s.TunnelTLS("main", "acme", hubIP, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, "acme", m2.Mode)
	require.NoError(t, m2.Warning)
	require.Equal(t, certPEM, m2.CertPEM)

	// Wrong domain: fallback.
	m3, err := s.TunnelTLS("main", "acme", hubIP, "other.example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, "auto", m3.Mode)
	require.Equal(t, deyerr.T003, code(t, m3.Warning))
	// No domain at all: fallback.
	m4, err := s.TunnelTLS("main", "acme", hubIP, "", "", "")
	require.NoError(t, err)
	require.Equal(t, "auto", m4.Mode)

	// 10 days left: still used, with a T006 warning.
	clk.t = m2.NotAfter.Add(-10 * 24 * time.Hour)
	m5, err := s.TunnelTLS("main", "acme", hubIP, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, "acme", m5.Mode)
	require.Equal(t, deyerr.T006, code(t, m5.Warning))

	// Expired: fallback to auto.
	clk.t = m2.NotAfter.Add(time.Hour)
	m6, err := s.TunnelTLS("main", "acme", hubIP, "tunnel.example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, "auto", m6.Mode)
	// ...and StoreACME refuses expired certificates.
	require.Equal(t, deyerr.T001, code(t, s.StoreACME("main", certPEM, keyPEM)))

	// StoreACME validation.
	_, otherKey, err := le.IssueServer("x", nil, []string{"x.example.com"}, 0)
	require.NoError(t, err)
	clk.t = t0
	require.Equal(t, deyerr.T002, code(t, s.StoreACME("main", certPEM, otherKey)))
	require.Error(t, s.StoreACME("main", []byte("junk"), keyPEM))
	require.Error(t, s.StoreACME("main", certPEM, []byte("junk")))
	require.Equal(t, deyerr.C007, code(t, s.StoreACME("..", certPEM, keyPEM)))
}

func writeCustom(t *testing.T, dir string, validity time.Duration) (certPath, keyPath string) {
	t.Helper()
	ca := newCA(t, t0)
	certPEM, keyPEM, err := ca.IssueServer("vpn.example.com", nil, []string{"vpn.example.com"}, validity)
	require.NoError(t, err)
	certPath, keyPath = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	require.NoError(t, os.WriteFile(certPath, append(certPEM, ca.CertPEM...), 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))
	return certPath, keyPath
}

func TestTunnelTLSCustom(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	certPath, keyPath := writeCustom(t, dir, 365*24*time.Hour)
	m, err := s.TunnelTLS("main", "custom", hubIP, "", certPath, keyPath)
	require.NoError(t, err)
	require.Equal(t, "custom", m.Mode)
	require.NoError(t, m.Warning)
	sum, err := tlsutil.CertSHA256Hex(m.CertPEM)
	require.NoError(t, err)
	require.Equal(t, sum, m.CertSHA256)

	// Expiring within 14 days: warning.
	c2, k2 := writeCustom(t, t.TempDir(), 10*24*time.Hour)
	m2, err := s.TunnelTLS("main", "custom", hubIP, "", c2, k2)
	require.NoError(t, err)
	require.Equal(t, deyerr.T006, code(t, m2.Warning))

	// Key mismatch.
	_, otherKey := writeCustom(t, t.TempDir(), 365*24*time.Hour)
	_, err = s.TunnelTLS("main", "custom", hubIP, "", certPath, otherKey)
	require.Equal(t, deyerr.T002, code(t, err))
	// Missing file.
	_, err = s.TunnelTLS("main", "custom", hubIP, "", filepath.Join(dir, "missing.pem"), keyPath)
	require.Equal(t, deyerr.T008, code(t, err))
	// Missing settings.
	_, err = s.TunnelTLS("main", "custom", hubIP, "", "", "")
	require.Equal(t, deyerr.T008, code(t, err))
	// Not PEM.
	junk := filepath.Join(dir, "junk.pem")
	require.NoError(t, os.WriteFile(junk, []byte("hello"), 0o600))
	_, err = s.TunnelTLS("main", "custom", hubIP, "", junk, keyPath)
	require.Error(t, err)
}

func TestPKCS12(t *testing.T) {
	s, _ := newStore(t)
	m, err := s.TunnelTLS("main", "auto", hubIP, "", "", "")
	require.NoError(t, err)
	p12, pass, err := s.PKCS12("main")
	require.NoError(t, err)
	require.NotEmpty(t, p12)
	require.Len(t, pass, 32)
	dir := s.TLSPath("main")
	requireMode(t, filepath.Join(dir, P12PassFile), 0o600)
	requireMode(t, filepath.Join(dir, P12File), 0o600)

	// Stable while the certificate does not change.
	p12b, passb, err := s.PKCS12("main")
	require.NoError(t, err)
	require.Equal(t, p12, p12b)
	require.Equal(t, pass, passb)

	// A fresh Store (restart) without TunnelTLS reads the stored auto pair.
	s2 := &Store{Root: s.Root, CA: s.CA, Now: s.Now}
	p12c, passc, err := s2.PKCS12("main")
	require.NoError(t, err)
	require.Equal(t, p12, p12c)
	require.Equal(t, pass, passc)

	// A new certificate rebuilds the bundle with the same password.
	m2, err := s.TunnelTLS("main", "auto", hubIP, "t.example.com", "", "")
	require.NoError(t, err)
	require.NotEqual(t, m.CertSHA256, m2.CertSHA256)
	p12d, passd, err := s.PKCS12("main")
	require.NoError(t, err)
	require.NotEqual(t, p12, p12d)
	require.Equal(t, pass, passd)

	// Losing the password makes a new one and a new bundle.
	require.NoError(t, os.Remove(filepath.Join(dir, P12PassFile)))
	p12e, passe, err := s.PKCS12("main")
	require.NoError(t, err)
	require.NotEqual(t, pass, passe)
	require.NotEqual(t, p12d, p12e)

	// No material at all.
	_, _, err = s.PKCS12("none")
	require.Equal(t, deyerr.S009, code(t, err))
	_, _, err = s.PKCS12("..")
	require.Equal(t, deyerr.C007, code(t, err))

	// Damaged stored key on a fresh store.
	require.NoError(t, os.WriteFile(filepath.Join(dir, KeyFile), []byte("junk"), 0o600))
	_, _, err = (&Store{Root: s.Root}).PKCS12("main")
	require.Error(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, KeyFile)))
	_, _, err = (&Store{Root: s.Root}).PKCS12("main")
	require.Equal(t, deyerr.S009, code(t, err))
	// Unreadable password path.
	require.NoError(t, os.Remove(filepath.Join(dir, P12PassFile)))
	require.NoError(t, os.Mkdir(filepath.Join(dir, P12PassFile), 0o700))
	_, _, err = s.PKCS12("main")
	require.Equal(t, deyerr.S009, code(t, err))
}

func TestSameSet(t *testing.T) {
	require.True(t, sameSet(nil, nil))
	require.True(t, sameSet([]string{"a", "b"}, []string{"b", "a", "a"}))
	require.False(t, sameSet([]string{"a"}, []string{"a", "b"}))
	require.False(t, sameSet([]string{"a", "c"}, []string{"a", "b"}))
}
