package tlsutil

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestCertInfoAndDaysLeft(t *testing.T) {
	ca := newTestCA(t)
	certPEM, _, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, t0)
	require.NoError(t, err)
	info, err := CertInfo(certPEM)
	require.NoError(t, err)
	require.Equal(t, "tunnel-main", info.Subject)
	require.Equal(t, "DEYROUTE CA ir-1", info.Issuer)
	require.Equal(t, []string{"5.6.7.8"}, info.SANs)
	require.Equal(t, time.UTC, info.NotAfter.Location())
	require.True(t, ValidFingerprint(info.Fingerprint))

	require.Equal(t, 3*365, info.DaysLeft(t0))
	require.Equal(t, 0, info.DaysLeft(info.NotAfter.Add(-time.Hour)))
	require.Equal(t, -1, info.DaysLeft(info.NotAfter.Add(time.Hour)))
	require.False(t, info.Expired(t0))
	// RFC 5280: NotAfter itself is still valid, as crypto/x509 agrees.
	require.False(t, info.Expired(info.NotAfter))
	require.True(t, info.Expired(info.NotAfter.Add(time.Second)))

	caInfo, err := CertInfo(ca.CertPEM)
	require.NoError(t, err)
	require.True(t, caInfo.IsCA)
	require.Equal(t, ca.Fingerprint(), caInfo.Fingerprint)

	_, err = CertInfo([]byte("junk"))
	requireCode(t, err, deyerr.T008)

	// A certificate without CN is described by its full subject.
	tpl := leafTpl("", t0, t0.Add(time.Hour))
	tpl.Subject.Organization = []string{"Org"}
	leaf := makeCert(t, tpl, nil)
	require.Equal(t, "O=Org", InfoOf(leaf.cert).Subject)
}

func TestNeedsRenewal(t *testing.T) {
	ca := newTestCA(t)
	certPEM, _, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, t0)
	require.NoError(t, err)
	exp := t0.Add(TunnelCertValidity)
	require.False(t, NeedsRenewal(certPEM, t0, RenewBefore))
	require.False(t, NeedsRenewal(certPEM, exp.Add(-RenewBefore-time.Second), RenewBefore))
	require.True(t, NeedsRenewal(certPEM, exp.Add(-RenewBefore), RenewBefore))
	require.True(t, NeedsRenewal(certPEM, exp.Add(time.Hour), RenewBefore))
	require.True(t, NeedsRenewal([]byte("junk"), t0, RenewBefore))
}

func TestExpiryWarning(t *testing.T) {
	ca := newTestCA(t)
	certPEM, _, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, t0)
	require.NoError(t, err)
	exp := t0.Add(TunnelCertValidity)

	require.NoError(t, ExpiryWarning(certPEM, exp.Add(-WarnBefore-time.Minute)))

	err = ExpiryWarning(certPEM, exp.Add(-10*24*time.Hour-time.Minute))
	e := requireCode(t, err, deyerr.T006)
	require.Equal(t, "Certificate expires in 10 days: tunnel-main", e.Message())

	// The last second of validity warns; one second later it is expired.
	e = requireCode(t, ExpiryWarning(certPEM, exp), deyerr.T006)
	require.Equal(t, "0", e.Params["days"])
	err = ExpiryWarning(certPEM, exp.Add(time.Second))
	e = requireCode(t, err, deyerr.T001)
	require.Equal(t, "Certificate expired: tunnel-main", e.Message())
	require.Contains(t, e.Why(), exp.Format("2006-01-02"))

	// crypto/x509 uses the same boundary.
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: exp})
	require.NoError(t, err)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: exp.Add(time.Second)})
	require.Error(t, err)

	requireCode(t, ExpiryWarning([]byte("junk"), t0), deyerr.T008)

	// Labels fall back to SANs and the fingerprint.
	noCN := makeCert(t, leafTpl("", t0, t0.Add(time.Hour)), nil)
	require.Equal(t, "example.com", certLabel(noCN.cert))
	tpl := leafTpl("", t0, t0.Add(time.Hour))
	tpl.DNSNames = nil
	ipOnly := makeCert(t, tpl, nil)
	require.Equal(t, "192.0.2.1", certLabel(ipOnly.cert))
	tpl = leafTpl("", t0, t0.Add(time.Hour))
	tpl.DNSNames, tpl.IPAddresses = nil, nil
	bare := makeCert(t, tpl, nil)
	require.Equal(t, Fingerprint(bare.cert.Raw), certLabel(bare.cert))
}

func TestCertSHA256Hex(t *testing.T) {
	ca := newTestCA(t)
	h, err := CertSHA256Hex(ca.CertPEM)
	require.NoError(t, err)
	sum := sha256.Sum256(ca.Cert.Raw)
	require.Equal(t, hex.EncodeToString(sum[:]), h)
	require.Equal(t, "sha256:"+h, ca.Fingerprint())
	_, err = CertSHA256Hex(nil)
	requireCode(t, err, deyerr.T008)
}

func TestNormalizeFingerprint(t *testing.T) {
	hexStr := strings.Repeat("ab", 32)
	want := "sha256:" + hexStr
	for _, in := range []string{
		want,
		"SHA256:" + strings.ToUpper(hexStr),
		hexStr,
		"  " + want + "\n",
		strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
	} {
		got, ok := NormalizeFingerprint(in)
		require.True(t, ok, in)
		require.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "sha256:", "sha256:abc", "sha256:" + strings.Repeat("zz", 32), "md5:" + hexStr} {
		_, ok := NormalizeFingerprint(in)
		require.False(t, ok, in)
		require.False(t, ValidFingerprint(in), in)
	}
}

func TestParsePrivateKeyFormats(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	sec1, err := x509.MarshalECPrivateKey(ec)
	require.NoError(t, err)
	k, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}))
	require.NoError(t, err)
	require.True(t, ec.Equal(k))

	// "EC PARAMETERS" before the key (openssl ecparam output) is skipped.
	withParams := append(pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{6, 8, 42, 134, 72, 206, 61, 3, 1, 7}}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})...)
	k, err = ParsePrivateKey(withParams)
	require.NoError(t, err)
	require.True(t, ec.Equal(k))

	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	k, err = ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rk)}))
	require.NoError(t, err)
	require.True(t, rk.Equal(k))

	_, ed, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	k, err = ParsePrivateKey(keyPEMOf(t, ed))
	require.NoError(t, err)
	require.True(t, ed.Equal(k))

	_, err = ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}}))
	e := requireCode(t, err, deyerr.T008)
	require.Contains(t, e.Why(), "encrypted")
	// Legacy OpenSSL encryption (openssl ec/rsa -aes256) keeps the block
	// type; the reason must still say "encrypted", not an ASN.1 error.
	for _, typ := range []string{"EC PRIVATE KEY", "RSA PRIVATE KEY"} {
		legacy := pem.EncodeToMemory(&pem.Block{Type: typ, Headers: map[string]string{
			"Proc-Type": "4,ENCRYPTED",
			"DEK-Info":  "AES-256-CBC,00112233445566778899AABBCCDDEEFF",
		}, Bytes: []byte{0x30, 0x03, 0x02, 0x01, 0x01}})
		_, err = ParsePrivateKey(legacy)
		e = requireCode(t, err, deyerr.T008)
		require.Contains(t, e.Why(), "encrypted", typ)
	}
	_, err = ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2}}))
	requireCode(t, err, deyerr.T008)
	_, err = ParsePrivateKey([]byte("nothing"))
	requireCode(t, err, deyerr.T008)

	require.False(t, KeyMatchesCert(nil, ec))
	ca := newTestCA(t)
	require.False(t, KeyMatchesCert(ca.Cert, nil))
	require.False(t, KeyMatchesCert(ca.Cert, "not a key"))
	require.False(t, KeyMatchesCert(ca.Cert, ec))
	require.True(t, KeyMatchesCert(ca.Cert, ca.Key))
}

func TestParseCertChainSkipsOtherBlocks(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := ca.IssueServer("hub", nil, []string{"hub"}, time.Hour)
	require.NoError(t, err)
	mixed := append(append(append([]byte{}, keyPEM...), certPEM...), ca.CertPEM...)
	chain, err := ParseCertChain(mixed)
	require.NoError(t, err)
	require.Len(t, chain, 2)
	require.Equal(t, "hub", chain[0].Subject.CommonName)
	require.True(t, chain[1].IsCA)

	_, err = ParseCertChain(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2}}))
	requireCode(t, err, deyerr.T008)
}

func TestValidateCustom(t *testing.T) {
	dir := t.TempDir()
	now := t0
	root := makeCert(t, rootTpl("Root", now.Add(-time.Hour), now.Add(10*365*24*time.Hour)), nil)
	inter := makeCert(t, rootTpl("Intermediate", now.Add(-time.Hour), now.Add(5*365*24*time.Hour)), &root)
	leaf := makeCert(t, leafTpl("tun.example.com", now.Add(-time.Hour), now.Add(90*24*time.Hour), x509.ExtKeyUsageServerAuth), &inter)
	keyPath := writeFile(t, dir, "key.pem", keyPEMOf(t, leaf.key))

	chainPEM := append(append([]byte{}, leaf.pem...), inter.pem...)
	chainPath := writeFile(t, dir, "chain.pem", chainPEM)
	require.NoError(t, ValidateCustom(chainPath, keyPath, now))

	fullPath := writeFile(t, dir, "full.pem", append(append([]byte{}, chainPEM...), root.pem...))
	require.NoError(t, ValidateCustom(fullPath, keyPath, now))

	// Leaf alone (issuer not included) is accepted: nothing to link-check.
	leafPath := writeFile(t, dir, "leaf.pem", leaf.pem)
	require.NoError(t, ValidateCustom(leafPath, keyPath, now))

	// Self-signed server certificate.
	self := makeCert(t, leafTpl("self", now.Add(-time.Hour), now.Add(time.Hour*24)), nil)
	require.NoError(t, ValidateCustom(writeFile(t, dir, "self.pem", self.pem), writeFile(t, dir, "self.key", keyPEMOf(t, self.key)), now))

	// Wrong order / foreign intermediate: T005.
	other := makeCert(t, rootTpl("Other", now.Add(-time.Hour), now.Add(24*time.Hour)), nil)
	bad := writeFile(t, dir, "bad.pem", append(append([]byte{}, leaf.pem...), other.pem...))
	requireCode(t, ValidateCustom(bad, keyPath, now), deyerr.T005)

	// Expired: T001 (leaf) and T001 (intermediate).
	e := requireCode(t, ValidateCustom(chainPath, keyPath, now.Add(91*24*time.Hour)), deyerr.T001)
	require.Contains(t, e.Message(), chainPath)
	expInter := makeCert(t, rootTpl("OldInter", now.Add(-48*time.Hour), now.Add(-time.Hour)), &root)
	leaf2 := makeCert(t, leafTpl("x", now.Add(-time.Hour), now.Add(24*time.Hour)), &expInter)
	p := writeFile(t, dir, "exp-inter.pem", append(append([]byte{}, leaf2.pem...), expInter.pem...))
	e = requireCode(t, ValidateCustom(p, writeFile(t, dir, "k2", keyPEMOf(t, leaf2.key)), now), deyerr.T001)
	require.Contains(t, e.Detail, "certificate 2 in the file (OldInter)", "the owner learns which certificate expired")
	require.Empty(t, requireCode(t, ValidateCustom(chainPath, keyPath, now.Add(91*24*time.Hour)), deyerr.T001).Detail)

	// The last second of validity is still valid (RFC 5280).
	require.NoError(t, ValidateCustom(chainPath, keyPath, leaf.cert.NotAfter))

	// Not yet valid: T005.
	e = requireCode(t, ValidateCustom(chainPath, keyPath, now.Add(-2*time.Hour)), deyerr.T005)
	require.Contains(t, e.Why(), "not valid before")

	// Key mismatch: T002.
	e = requireCode(t, ValidateCustom(chainPath, writeFile(t, dir, "other.key", keyPEMOf(t, other.key)), now), deyerr.T002)
	require.Contains(t, e.Why(), "other.key")

	// Client-only certificate: T005.
	client := makeCert(t, leafTpl("client", now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageClientAuth), &inter)
	cp := writeFile(t, dir, "client.pem", append(append([]byte{}, client.pem...), inter.pem...))
	e = requireCode(t, ValidateCustom(cp, writeFile(t, dir, "client.key", keyPEMOf(t, client.key)), now), deyerr.T005)
	require.Contains(t, e.Why(), "extended key usage")
	anyUse := makeCert(t, leafTpl("any", now.Add(-time.Hour), now.Add(24*time.Hour), x509.ExtKeyUsageAny), nil)
	require.NoError(t, ValidateCustom(writeFile(t, dir, "any.pem", anyUse.pem), writeFile(t, dir, "any.key", keyPEMOf(t, anyUse.key)), now))

	// Missing / garbage files: T008.
	e = requireCode(t, ValidateCustom(filepath.Join(dir, "nope.pem"), keyPath, now), deyerr.T008)
	require.Contains(t, e.Why(), "does not exist")
	requireCode(t, ValidateCustom(chainPath, filepath.Join(dir, "nope.key"), now), deyerr.T008)
	junk := writeFile(t, dir, "junk", []byte("junk"))
	requireCode(t, ValidateCustom(junk, keyPath, now), deyerr.T008)
	requireCode(t, ValidateCustom(chainPath, junk, now), deyerr.T008)
}

func TestCheckExpiryDirect(t *testing.T) {
	c := makeCert(t, leafTpl("x", t0.Add(-time.Hour), t0.Add(3*24*time.Hour)), nil)
	e := requireCode(t, CheckExpiry("/etc/deyroute/secrets/tls/main/cert.pem", c.cert, t0), deyerr.T006)
	require.Equal(t, "Certificate expires in 3 days: /etc/deyroute/secrets/tls/main/cert.pem", e.Message())

	// Exactly 14 days left still warns; one second more does not.
	c = makeCert(t, leafTpl("x", t0.Add(-time.Hour), t0.Add(WarnBefore)), nil)
	e = requireCode(t, CheckExpiry("x", c.cert, t0), deyerr.T006)
	require.Equal(t, "14", e.Params["days"])
	require.NoError(t, CheckExpiry("x", c.cert, t0.Add(-time.Second)))

	// nil input is an error, not a panic.
	requireCode(t, CheckExpiry("x", nil, t0), deyerr.T008)
	require.Equal(t, Info{}, InfoOf(nil))
}
