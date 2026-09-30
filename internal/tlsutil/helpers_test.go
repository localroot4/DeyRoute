package tlsutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// t0 is a fixed reference time for deterministic validity checks.
var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newTestCA(t *testing.T) *CA {
	t.Helper()
	ca, err := NewCA("ir-1", time.Now())
	require.NoError(t, err)
	return ca
}

func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	require.Truef(t, deyerr.HasCode(err, code), "want %s, got %v", code, err)
	e := deyerr.As(err)
	require.NotNil(t, e)
	return e
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

// selfSigned creates a certificate template signed by parent (or itself when
// parent is nil) and returns the parsed certificate, its PEM and its key.
type testCert struct {
	cert *x509.Certificate
	pem  []byte
	key  crypto.Signer
}

func makeCert(t *testing.T, tpl *x509.Certificate, parent *testCert) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	if tpl.SerialNumber == nil {
		tpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	}
	parentCert, parentKey := tpl, crypto.Signer(key)
	if parent != nil {
		parentCert, parentKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parentCert, key.Public(), parentKey)
	require.NoError(t, err)
	c, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return testCert{cert: c, pem: EncodeCertPEM(der), key: key}
}

func rootTpl(cn string, notBefore, notAfter time.Time) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
}

func leafTpl(cn string, notBefore, notAfter time.Time, usage ...x509.ExtKeyUsage) *x509.Certificate {
	return &x509.Certificate{
		Subject:     pkix.Name{CommonName: cn},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: usage,
		DNSNames:    []string{"example.com"},
		IPAddresses: []net.IP{net.ParseIP("192.0.2.1")},
	}
}

func keyPEMOf(t *testing.T, k crypto.Signer) []byte {
	t.Helper()
	p, err := EncodeKeyPEM(k)
	require.NoError(t, err)
	return p
}
