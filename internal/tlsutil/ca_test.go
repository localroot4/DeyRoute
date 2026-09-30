package tlsutil

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestCACommonName(t *testing.T) {
	require.Equal(t, "DEYROUTE CA ir-1", CACommonName("ir-1"))
	require.Equal(t, "DEYROUTE CA ir-1", CACommonName("  ir-1 "))
	require.Equal(t, "DEYROUTE CA ir-1", CACommonName("DEYROUTE CA ir-1"))
	require.Equal(t, "DEYROUTE CA", CACommonName(""))
	require.Equal(t, "DEYROUTE CA", CACommonName("DEYROUTE CA"))
	// Only a real "DEYROUTE CA " prefix counts.
	require.Equal(t, "DEYROUTE CA DEYROUTE CAx", CACommonName("DEYROUTE CAx"))
	// Control characters are dropped, long names cut to 64 characters.
	require.Equal(t, "DEYROUTE CA ir-1", CACommonName("ir-\n1\x00"))
	long := CACommonName(strings.Repeat("ایران", 20))
	require.Equal(t, 64, utf8.RuneCountInString(long))
	require.True(t, strings.HasPrefix(long, "DEYROUTE CA ایران"))
}

func TestNewCAUnusualNames(t *testing.T) {
	for _, name := range []string{strings.Repeat("x", 200), "hub\twith\ttabs", "تهران-۱"} {
		ca, err := NewCA(name, t0)
		require.NoError(t, err, name)
		cn := ca.Cert.Subject.CommonName
		require.NoError(t, checkCN(cn), cn)
		require.True(t, strings.HasPrefix(cn, CAPrefix+" "), cn)
	}
}

func TestCheckCNCountsCharacters(t *testing.T) {
	require.NoError(t, checkCN(strings.Repeat("ش", 64)), "64 characters (128 bytes) is within X.520")
	require.Error(t, checkCN(strings.Repeat("ش", 65)))
	require.Error(t, checkCN("bad\xffutf8"))
	ca := newTestCA(t)
	_, _, err := ca.IssueServer(strings.Repeat("ش", 40), nil, []string{"hub"}, time.Hour)
	require.NoError(t, err)
}

func TestIssuedRoles(t *testing.T) {
	ca := newTestCA(t)
	hubPEM, _, err := ca.IssueServer("deyroute-hub ir-1", []net.IP{net.ParseIP("192.0.2.1")}, nil, 0)
	require.NoError(t, err)
	tunPEM, _, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("192.0.2.1")}, nil, time.Now())
	require.NoError(t, err)
	csr, _, err := NewKeyAndCSR("de-1")
	require.NoError(t, err)
	nodePEM, err := ca.SignCSR(csr, "de-1", 0)
	require.NoError(t, err)

	for want, p := range map[string][]byte{HubOU: hubPEM, TunnelOU: tunPEM, NodeOU: nodePEM, RoleCA: ca.CertPEM} {
		c, err := ParseCert(p)
		require.NoError(t, err)
		require.Equal(t, want, CertRole(c))
		info, err := CertInfo(p)
		require.NoError(t, err)
		require.Equal(t, want, info.Role)
		if want != RoleCA {
			require.Equal(t, []string{want}, c.Subject.OrganizationalUnit)
			require.True(t, hasRole(c, want))
		}
	}

	// Foreign certificates have no role, even with a look-alike OU.
	foreign := leafTpl("x", t0, t0.Add(time.Hour))
	foreign.Subject.OrganizationalUnit = []string{HubOU}
	require.Equal(t, "", CertRole(makeCert(t, foreign, nil).cert))
	require.Equal(t, "", CertRole(nil))
	require.False(t, hasRole(nil, HubOU))
}

func TestIssueSANsIPLiteralsInDNS(t *testing.T) {
	ca := newTestCA(t)
	certPEM, _, err := ca.IssueTunnel("main", nil, []string{"5.6.7.8", "[2001:db8::9]", " tun.example.com "}, time.Now())
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, []string{"tun.example.com"}, leaf.DNSNames)
	require.Len(t, leaf.IPAddresses, 2)
	require.True(t, leaf.IPAddresses[0].Equal(net.ParseIP("5.6.7.8")))
	require.True(t, leaf.IPAddresses[1].Equal(net.ParseIP("2001:db8::9")))
	// Clients connecting by IP accept it.
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "5.6.7.8"})
	require.NoError(t, err)

	// Only an IP literal given as "domain" still satisfies the SAN rule.
	_, _, err = ca.IssueTunnel("main", nil, []string{"5.6.7.8"}, time.Now())
	require.NoError(t, err)
}

func TestCASaveKeepsOldPairOnFailure(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	old := newTestCA(t)
	require.NoError(t, old.Save(certPath, keyPath))

	// The key cannot be written: the certificate must not be replaced
	// either, or the hub would be left with a new CA cert and the old key.
	blocker := writeFile(t, dir, "blocker", []byte("x"))
	next := newTestCA(t)
	requireCode(t, next.Save(certPath, filepath.Join(blocker, "ca.key")), deyerr.X032)
	loaded, err := LoadCA(certPath, keyPath)
	require.NoError(t, err)
	require.Equal(t, old.Fingerprint(), loaded.Fingerprint())

	// Same when the certificate side fails after the key was staged.
	requireCode(t, next.Save(filepath.Join(blocker, "ca.crt"), keyPath), deyerr.X032)
	loaded, err = LoadCA(certPath, keyPath)
	require.NoError(t, err)
	require.Equal(t, old.Fingerprint(), loaded.Fingerprint())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".tmp-", "staged files are removed")
	}
}

func TestReadPEMFileRefusesSpecialAndHugeFiles(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	keyPEM, err := ca.KeyPEM()
	require.NoError(t, err)
	keyPath := writeFile(t, dir, "ca.key", keyPEM)

	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	done := make(chan error, 1)
	go func() { done <- ValidateCustom(fifo, keyPath, time.Now()) }()
	select {
	case err := <-done:
		e := requireCode(t, err, deyerr.T008)
		require.Contains(t, e.Why(), "not a regular file")
	case <-time.After(5 * time.Second):
		// Unblock the reader so the goroutine ends, then fail.
		f, _ := os.OpenFile(fifo, os.O_WRONLY, 0)
		if f != nil {
			_ = f.Close()
		}
		<-done
		t.Fatal("reading a FIFO blocked")
	}

	huge := filepath.Join(dir, "huge.pem")
	require.NoError(t, os.WriteFile(huge, append(ca.CertPEM, make([]byte, maxPEMFileSize)...), 0o600))
	_, err = LoadCA(huge, keyPath)
	e := requireCode(t, err, deyerr.T008)
	require.Contains(t, e.Why(), "larger than 1 MiB")
}

func TestNewCA(t *testing.T) {
	ca, err := NewCA("ir-1", t0)
	require.NoError(t, err)
	c := ca.Cert
	require.Equal(t, "DEYROUTE CA ir-1", c.Subject.CommonName)
	require.Equal(t, []string{Organization}, c.Subject.Organization)
	require.True(t, c.IsCA)
	require.True(t, c.BasicConstraintsValid)
	require.True(t, c.MaxPathLenZero)
	require.Equal(t, 0, c.MaxPathLen)
	require.Equal(t, x509.KeyUsageCertSign|x509.KeyUsageCRLSign, c.KeyUsage)
	require.Equal(t, x509.Ed25519, c.PublicKeyAlgorithm)
	require.Equal(t, x509.PureEd25519, c.SignatureAlgorithm)
	require.Equal(t, t0.Add(-ClockSkew), c.NotBefore)
	require.Equal(t, t0.Add(CAValidity), c.NotAfter)
	require.Equal(t, 1, c.SerialNumber.Sign())
	require.LessOrEqual(t, c.SerialNumber.BitLen(), 128)
	require.NoError(t, c.CheckSignatureFrom(c), "self-signed")
	require.NotEmpty(t, c.SubjectKeyId)

	block, _ := pem.Decode(ca.CertPEM)
	require.NotNil(t, block)
	require.Equal(t, "CERTIFICATE", block.Type)
	require.Equal(t, c.Raw, block.Bytes)

	fp := ca.Fingerprint()
	require.True(t, strings.HasPrefix(fp, "sha256:"))
	require.Len(t, fp, len("sha256:")+64)
	require.Equal(t, strings.ToLower(fp), fp)
	require.Equal(t, Fingerprint(c.Raw), fp)

	other, err := NewCA("ir-1", t0)
	require.NoError(t, err)
	require.NotEqual(t, fp, other.Fingerprint(), "fresh key and serial")
	require.NotEqual(t, c.SerialNumber, other.Cert.SerialNumber)
}

func TestCAFingerprintNil(t *testing.T) {
	var ca *CA
	require.Equal(t, "", ca.Fingerprint())
	require.Equal(t, "", (&CA{}).Fingerprint())
}

func TestCASaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	ca := newTestCA(t)
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	require.NoError(t, ca.Save(certPath, keyPath))

	for _, p := range []string{certPath, keyPath} {
		st, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), st.Mode().Perm(), p)
	}
	st, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), st.Mode().Perm())

	keyPEM, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	require.Contains(t, string(keyPEM), "BEGIN PRIVATE KEY")

	loaded, err := LoadCA(certPath, keyPath)
	require.NoError(t, err)
	require.Equal(t, ca.Fingerprint(), loaded.Fingerprint())
	require.True(t, ca.Key.Equal(loaded.Key))
	require.Equal(t, ca.CertPEM, loaded.CertPEM)

	// The loaded CA issues certificates that verify against the original.
	certPEM, _, err := loaded.IssueServer("hub", []net.IP{net.ParseIP("192.0.2.10")}, nil, 0)
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.NoError(t, leaf.CheckSignatureFrom(ca.Cert))
}

func TestLoadCAErrors(t *testing.T) {
	dir := t.TempDir()
	ca := newTestCA(t)
	certPath := writeFile(t, dir, "ca.crt", ca.CertPEM)
	keyPEM, err := ca.KeyPEM()
	require.NoError(t, err)
	keyPath := writeFile(t, dir, "ca.key", keyPEM)

	_, err = LoadCA(filepath.Join(dir, "missing.crt"), keyPath)
	e := requireCode(t, err, deyerr.T007)
	require.Contains(t, e.Message(), "missing.crt")

	_, err = LoadCA(certPath, filepath.Join(dir, "missing.key"))
	requireCode(t, err, deyerr.T007)

	garbage := writeFile(t, dir, "garbage", []byte("not pem"))
	_, err = LoadCA(garbage, keyPath)
	requireCode(t, err, deyerr.T008)
	_, err = LoadCA(certPath, garbage)
	requireCode(t, err, deyerr.T008)

	// A leaf is not a CA.
	leafPEM, leafKey, err := ca.IssueServer("hub", []net.IP{net.ParseIP("192.0.2.1")}, nil, time.Hour)
	require.NoError(t, err)
	leafPath := writeFile(t, dir, "leaf.crt", leafPEM)
	_, err = LoadCA(leafPath, writeFile(t, dir, "leaf.key", leafKey))
	e = requireCode(t, err, deyerr.T008)
	require.Contains(t, e.Why(), "not a CA")

	// A key of another CA does not match.
	other := newTestCA(t)
	otherKey, err := other.KeyPEM()
	require.NoError(t, err)
	_, err = LoadCA(certPath, writeFile(t, dir, "other.key", otherKey))
	requireCode(t, err, deyerr.T002)

	// A non-Ed25519 key is rejected.
	_, err = LoadCA(certPath, writeFile(t, dir, "ec.key", leafKey))
	e = requireCode(t, err, deyerr.T008)
	require.Contains(t, e.Why(), "Ed25519")

	// An unreadable path (a directory) is T008, not T007.
	_, err = LoadCA(dir, keyPath)
	requireCode(t, err, deyerr.T008)
}

func TestCASaveErrors(t *testing.T) {
	var nilCA *CA
	requireCode(t, nilCA.Save("a", "b"), deyerr.X000)
	_, err := (&CA{}).KeyPEM()
	requireCode(t, err, deyerr.X000)

	ca := newTestCA(t)
	dir := t.TempDir()
	blocker := writeFile(t, dir, "file", []byte("x"))
	requireCode(t, ca.Save(filepath.Join(blocker, "ca.crt"), filepath.Join(dir, "ca.key")), deyerr.X032)
	requireCode(t, ca.Save(filepath.Join(dir, "ca.crt"), filepath.Join(blocker, "ca.key")), deyerr.X032)
}

func TestIssueServer(t *testing.T) {
	ca, err := NewCA("ir-1", t0)
	require.NoError(t, err)
	ca.Now = func() time.Time { return t0 }
	ips := []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::1"), net.ParseIP("192.0.2.10"), nil}
	certPEM, keyPEM, err := ca.IssueServer("deyroute-hub ir-1", ips, []string{"Hub.Example.COM.", "", "hub.example.com"}, HubCertValidity)
	require.NoError(t, err)

	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, "deyroute-hub ir-1", leaf.Subject.CommonName)
	require.Equal(t, "DEYROUTE CA ir-1", leaf.Issuer.CommonName)
	require.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, leaf.ExtKeyUsage)
	require.Equal(t, x509.KeyUsageDigitalSignature, leaf.KeyUsage)
	require.False(t, leaf.IsCA)
	require.Equal(t, x509.ECDSA, leaf.PublicKeyAlgorithm)
	require.Equal(t, elliptic.P256(), leaf.PublicKey.(*ecdsa.PublicKey).Curve)
	require.Equal(t, []string{"hub.example.com"}, leaf.DNSNames)
	require.Len(t, leaf.IPAddresses, 2)
	require.Equal(t, t0.Add(HubCertValidity), leaf.NotAfter)
	require.Equal(t, t0.Add(-ClockSkew), leaf.NotBefore)

	key, err := ParsePrivateKey(keyPEM)
	require.NoError(t, err)
	require.True(t, KeyMatchesCert(leaf, key))
	require.Contains(t, string(keyPEM), "BEGIN PRIVATE KEY")

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "hub.example.com", CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "2001:db8::1", CurrentTime: t0})
	require.NoError(t, err)

	// Default validity.
	certPEM, _, err = ca.IssueServer("hub", nil, []string{"hub"}, 0)
	require.NoError(t, err)
	leaf, err = ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, t0.Add(HubCertValidity), leaf.NotAfter)

	// Bad common names.
	_, _, err = ca.IssueServer("", nil, nil, time.Hour)
	requireCode(t, err, deyerr.X000)
	_, _, err = ca.IssueServer(strings.Repeat("a", 65), nil, nil, time.Hour)
	requireCode(t, err, deyerr.X000)
	_, _, err = ca.IssueServer("bad\x00name", nil, nil, time.Hour)
	requireCode(t, err, deyerr.X000)

	var nilCA *CA
	_, _, err = nilCA.IssueServer("hub", nil, nil, time.Hour)
	requireCode(t, err, deyerr.X000)
}

func TestLeafCappedAtCAExpiry(t *testing.T) {
	ca, err := NewCA("ir-1", t0)
	require.NoError(t, err)
	late := t0.Add(CAValidity - 24*time.Hour)
	ca.Now = func() time.Time { return late }
	certPEM, _, err := ca.IssueServer("hub", nil, []string{"hub"}, 10*365*24*time.Hour)
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, ca.Cert.NotAfter, leaf.NotAfter)
}

// An expired CA must not issue: capping NotAfter at the CA expiry would give
// certificates that end before they start.
func TestExpiredCARefusesToIssue(t *testing.T) {
	ca, err := NewCA("ir-1", t0)
	require.NoError(t, err)
	after := ca.Cert.NotAfter.Add(time.Second)
	ca.Now = func() time.Time { return after }

	_, _, err = ca.IssueServer("hub", nil, []string{"hub"}, 0)
	e := requireCode(t, err, deyerr.T001)
	require.Equal(t, "Certificate expired: DEYROUTE CA ir-1", e.Message())
	require.Contains(t, e.Fix(), "deyroute security rotate-ca")

	_, _, err = ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, after)
	requireCode(t, err, deyerr.T001)

	csr, _, err := NewKeyAndCSR("de-1")
	require.NoError(t, err)
	_, err = ca.SignCSR(csr, "de-1", 0)
	requireCode(t, err, deyerr.T001)

	// The last valid second still issues, capped at the CA expiry.
	last := ca.Cert.NotAfter
	certPEM, _, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, last)
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, ca.Cert.NotAfter, leaf.NotAfter)
	require.True(t, leaf.NotBefore.Before(leaf.NotAfter))
}

// A zero time (an unset field in the caller) means "now", never year 1.
func TestZeroTimeMeansNow(t *testing.T) {
	ca, err := NewCA("ir-1", time.Time{})
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(-ClockSkew), ca.Cert.NotBefore, time.Minute)

	fixed := time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Second)
	ca.Now = func() time.Time { return fixed }
	certPEM, _, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, time.Time{})
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, fixed.Add(TunnelCertValidity), leaf.NotAfter, "the CA clock is used")
}

func TestIssueTunnel(t *testing.T) {
	ca := newTestCA(t)
	now := time.Now().UTC().Truncate(time.Second)
	certPEM, keyPEM, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8"), net.ParseIP("2001:db8::8")}, []string{"tun.example.com"}, now)
	require.NoError(t, err)
	info, err := CertInfo(certPEM)
	require.NoError(t, err)
	require.Equal(t, "tunnel-main", info.Subject)
	require.Equal(t, []string{"tun.example.com", "5.6.7.8", "2001:db8::8"}, info.SANs)
	require.Equal(t, now.Add(TunnelCertValidity), info.NotAfter)
	require.False(t, info.IsCA)

	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, x509.ECDSA, leaf.PublicKeyAlgorithm)
	require.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, leaf.ExtKeyUsage)
	key, err := ParsePrivateKey(keyPEM)
	require.NoError(t, err)
	require.True(t, KeyMatchesCert(leaf, key))
	require.NoError(t, leaf.CheckSignatureFrom(ca.Cert))

	// Fresh certificate: no renewal, no warning.
	require.False(t, NeedsRenewal(certPEM, now, RenewBefore))
	require.NoError(t, ExpiryWarning(certPEM, now))

	_, _, err = ca.IssueTunnel("main", nil, []string{" "}, now)
	requireCode(t, err, deyerr.X000)
}

func TestNewKeyAndCSRSignCSR(t *testing.T) {
	ca, err := NewCA("ir-1", t0)
	require.NoError(t, err)
	ca.Now = func() time.Time { return t0 }
	csrPEM, keyPEM, err := NewKeyAndCSR("requested-name")
	require.NoError(t, err)
	require.Contains(t, string(csrPEM), "BEGIN CERTIFICATE REQUEST")

	key, err := ParsePrivateKey(keyPEM)
	require.NoError(t, err)
	_, isEd := key.(ed25519.PrivateKey)
	require.True(t, isEd)

	certPEM, err := ca.SignCSR(csrPEM, "de-1", NodeCertValidity)
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, "de-1", leaf.Subject.CommonName, "the hub decides the CN")
	require.Equal(t, []string{NodeOU}, leaf.Subject.OrganizationalUnit)
	require.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, leaf.ExtKeyUsage)
	require.Empty(t, leaf.DNSNames)
	require.Empty(t, leaf.IPAddresses)
	require.Equal(t, t0.Add(NodeCertValidity), leaf.NotAfter)
	require.True(t, KeyMatchesCert(leaf, key))

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: t0, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	require.Error(t, err, "a node certificate must not authenticate a server")

	// Default validity.
	certPEM, err = ca.SignCSR(csrPEM, "de-1", 0)
	require.NoError(t, err)
	leaf, err = ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, t0.Add(NodeCertValidity), leaf.NotAfter)

	_, _, err = NewKeyAndCSR("")
	requireCode(t, err, deyerr.X000)
}

func TestSignCSRIgnoresRequestedNames(t *testing.T) {
	ca := newTestCA(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: "hub", Organization: []string{"evil"}},
		DNSNames:    []string{"hub.example.com"},
		IPAddresses: []net.IP{net.ParseIP("5.6.7.8")},
	}, priv)
	require.NoError(t, err)
	certPEM, err := ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "NEW CERTIFICATE REQUEST", Bytes: der}), "nl-1", time.Hour)
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, "nl-1", leaf.Subject.CommonName)
	require.Equal(t, []string{Organization}, leaf.Subject.Organization)
	require.Empty(t, leaf.DNSNames)
	require.Empty(t, leaf.IPAddresses)
}

func TestSignCSRRejects(t *testing.T) {
	ca := newTestCA(t)
	csrPEM, _, err := NewKeyAndCSR("de-1")
	require.NoError(t, err)

	// Tampered signature.
	block, _ := pem.Decode(csrPEM)
	bad := append([]byte(nil), block.Bytes...)
	bad[len(bad)-1] ^= 0xff
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: bad}), "de-1", time.Hour)
	e := requireCode(t, err, deyerr.T009)
	require.Contains(t, e.Why(), "signature")

	// Tampered subject (signature no longer covers the content).
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	require.NoError(t, err)
	tampered := append([]byte(nil), block.Bytes...)
	idx := strings.Index(string(tampered), "de-1")
	require.Positive(t, idx)
	copy(tampered[idx:], "xx-1")
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: tampered}), "de-1", time.Hour)
	requireCode(t, err, deyerr.T009)
	require.Equal(t, "de-1", csr.Subject.CommonName)

	// Not PEM, wrong PEM type, garbage DER.
	_, err = ca.SignCSR([]byte("hello"), "de-1", time.Hour)
	requireCode(t, err, deyerr.T009)
	_, err = ca.SignCSR(ca.CertPEM, "de-1", time.Hour)
	requireCode(t, err, deyerr.T009)
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{1, 2, 3}}), "de-1", time.Hour)
	requireCode(t, err, deyerr.T009)

	// Bad CN chosen by the caller.
	_, err = ca.SignCSR(csrPEM, "", time.Hour)
	requireCode(t, err, deyerr.T009)

	// Weak RSA key and unsupported curve.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024) // #nosec G403 -- test of the rejection
	require.NoError(t, err)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, rsaKey)
	require.NoError(t, err)
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "de-1", time.Hour)
	e = requireCode(t, err, deyerr.T009)
	require.Contains(t, e.Why(), "2048")

	p521, err := ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	require.NoError(t, err)
	der, err = x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, p521)
	require.NoError(t, err)
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "de-1", time.Hour)
	requireCode(t, err, deyerr.T009)

	// Accepted key types: ECDSA P-256 and RSA 2048.
	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err = x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, p256)
	require.NoError(t, err)
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "de-1", time.Hour)
	require.NoError(t, err)

	var nilCA *CA
	_, err = nilCA.SignCSR(csrPEM, "de-1", time.Hour)
	requireCode(t, err, deyerr.X000)
}

func TestSignCSRRSA2048(t *testing.T) {
	ca := newTestCA(t)
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "x"}}, k)
	require.NoError(t, err)
	_, err = ca.SignCSR(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), "de-1", time.Hour)
	require.NoError(t, err)
}
