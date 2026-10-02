package front

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func leafOf(t *testing.T, cfg *tls.Config) (*x509.Certificate, *tls.Certificate) {
	t.Helper()
	require.NotNil(t, cfg.GetCertificate)
	c, err := cfg.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	require.NoError(t, err)
	return leaf, c
}

func TestSelfSignedCertProperties(t *testing.T) {
	cfg, err := SelfSignedTLSConfig()
	require.NoError(t, err)
	leaf, c := leafOf(t, cfg)

	// ECDSA P-256, as a small web server would have.
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	require.True(t, ok, "ECDSA, not Ed25519 or RSA")
	assert.Equal(t, elliptic.P256(), pub.Curve)
	assert.Equal(t, x509.ECDSAWithSHA256, leaf.SignatureAlgorithm)
	_, ok = c.PrivateKey.(*ecdsa.PrivateKey)
	assert.True(t, ok)

	// Self-signed leaf, not a CA, and nothing that ties it to the product's CA.
	assert.False(t, leaf.IsCA)
	assert.Equal(t, leaf.Issuer.String(), leaf.Subject.String())
	assert.NoError(t, leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature), "signed by its own key")
	assert.Len(t, c.Certificate, 1, "no chain")
	assert.Empty(t, leaf.Subject.Organization)
	assert.Empty(t, leaf.Subject.OrganizationalUnit)
	assert.Empty(t, leaf.AuthorityKeyId)
	assert.Empty(t, leaf.CRLDistributionPoints)
	assert.Empty(t, leaf.OCSPServer)
	assert.Empty(t, leaf.IssuingCertificateURL)
	assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, leaf.ExtKeyUsage)
	assert.Equal(t, x509.KeyUsageDigitalSignature, leaf.KeyUsage)

	// A generic random subject.
	cn := leaf.Subject.CommonName
	assert.True(t, strings.HasPrefix(cn, "srv-"))
	assert.Len(t, cn, len("srv-")+12)
	for _, bad := range []string{"dey", "front", "route", "vpn", "tunnel"} {
		assert.NotContains(t, strings.ToLower(leaf.Subject.String()), bad)
	}

	// Ordinary validity: one year, starting about now.
	assert.WithinDuration(t, time.Now(), leaf.NotBefore, 2*time.Hour)
	assert.False(t, leaf.NotBefore.After(time.Now()), "valid right away")
	assert.InDelta(t, 365*24, leaf.NotAfter.Sub(leaf.NotBefore).Hours(), 1)

	// A random serial of real size.
	assert.Positive(t, leaf.SerialNumber.Sign())
	assert.Greater(t, leaf.SerialNumber.BitLen(), 64)

	// The server configuration.
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
	assert.Equal(t, []string{"http/1.1"}, cfg.NextProtos)
}

func TestSelfSignedCertsDiffer(t *testing.T) {
	seen := map[string]bool{}
	serials := map[string]bool{}
	for i := 0; i < 5; i++ {
		cfg, err := SelfSignedTLSConfig()
		require.NoError(t, err)
		leaf, _ := leafOf(t, cfg)
		assert.False(t, seen[leaf.Subject.CommonName], "the subject is random")
		seen[leaf.Subject.CommonName] = true
		serials[leaf.SerialNumber.String()] = true
	}
	assert.Len(t, serials, 5, "random serials")
}

func TestSelfSignedIsServedAndRenewed(t *testing.T) {
	s := &selfSigned{}
	c1, err := s.get(nil)
	require.NoError(t, err)
	c1b, err := s.get(nil)
	require.NoError(t, err)
	assert.Same(t, c1, c1b, "the same certificate is served until renewal")

	// Close to expiry a new one is made.
	s.mu.Lock()
	s.expires = time.Now().Add(selfSignedRenew / 2)
	s.mu.Unlock()
	c2, err := s.get(nil)
	require.NoError(t, err)
	assert.NotSame(t, c1, c2)
	l2, err := x509.ParseCertificate(c2.Certificate[0])
	require.NoError(t, err)
	assert.Greater(t, time.Until(l2.NotAfter), selfSignedRenew)
}

func TestSelfSignedHandshake(t *testing.T) {
	// The certificate really works for a TLS client that trusts it.
	cfg, err := SelfSignedTLSConfig()
	require.NoError(t, err)
	leaf, _ := leafOf(t, cfg)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	ln := tcpListener(t)
	errc := make(chan error, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := ln.Accept()
			if err != nil {
				errc <- err
				return
			}
			go func() { errc <- tls.Server(c, cfg).Handshake(); _ = c.Close() }()
		}
	}()
	cp, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer cp.Close()
	cli := tls.Client(cp, &tls.Config{RootCAs: pool, ServerName: leaf.Subject.CommonName, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	require.NoError(t, cli.Handshake())
	assert.Equal(t, "http/1.1", cli.ConnectionState().NegotiatedProtocol)

	// And a client that does not trust it refuses it.
	cp2, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	defer cp2.Close()
	assert.Error(t, tls.Client(cp2, &tls.Config{ServerName: leaf.Subject.CommonName}).Handshake())
	for i := 0; i < 2; i++ {
		select {
		case <-errc:
		case <-time.After(5 * time.Second):
			t.Fatal("server handshake did not end")
		}
	}
}
