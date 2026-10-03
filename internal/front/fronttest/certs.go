package fronttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"
)

// DefaultHosts are the names and addresses the edge certificate covers when
// Options.Hosts is empty.
var DefaultHosts = []string{"front.example.com", "localhost", "127.0.0.1", "::1"}

// CertSet is a throw-away CA and a server certificate issued by it.
type CertSet struct {
	Cert  tls.Certificate // the leaf plus its key
	Pool  *x509.CertPool  // trusts only the CA
	CAPEM []byte
}

// NewCertSet issues an ECDSA P-256 leaf for hosts (DNS names or IP literals)
// from a fresh ECDSA P-256 CA. The validity window is as given, so tests can
// build an expired or not-yet-valid certificate.
func NewCertSet(hosts []string, notBefore, notAfter time.Time) (*CertSet, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "fronttest edge CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "fronttest edge"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			leafTpl.IPAddresses = append(leafTpl.IPAddresses, ip)
		} else {
			leafTpl.DNSNames = append(leafTpl.DNSNames, h)
		}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	return &CertSet{
		Cert:  tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey},
		Pool:  pool,
		CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n.Add(n, big.NewInt(1))
}
