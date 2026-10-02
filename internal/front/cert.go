package front

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sync"
	"time"
)

// The outer certificate is not part of the trust model: the CDN does not
// verify it in Cloudflare's "Full" mode, and the inner TLS (pinned CA) is what
// authenticates the hub. It only has to look like what a small web server
// has: an ECDSA P-256 self-signed certificate with an unremarkable random
// subject and an ordinary validity. It is NOT issued by the deyroute CA, so
// nothing links it to the hub's identity.
const (
	selfSignedValidity = 365 * 24 * time.Hour
	selfSignedRenew    = 30 * 24 * time.Hour // a new one is made when less than this is left
)

// SelfSignedTLSConfig returns a server TLS configuration (TLS 1.2 or later,
// http/1.1 only) that serves a self-signed ECDSA P-256 certificate with a
// generic random subject and a one year validity. The certificate is made
// once and renewed in place before it expires, so a long-running hub never
// serves an expired one.
func SelfSignedTLSConfig() (*tls.Config, error) {
	s := &selfSigned{}
	if _, err := s.get(nil); err != nil { // fail early, not at the first handshake
		return nil, err
	}
	return &tls.Config{
		GetCertificate: s.get,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1"},
	}, nil
}

type selfSigned struct {
	mu      sync.Mutex
	cert    *tls.Certificate
	expires time.Time
}

func (s *selfSigned) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cert == nil || time.Until(s.expires) < selfSignedRenew {
		c, exp, err := newSelfSigned(time.Now())
		if err != nil {
			if s.cert != nil && time.Now().Before(s.expires) {
				return s.cert, nil // keep the old one rather than fail the handshake
			}
			return nil, err
		}
		s.cert, s.expires = c, exp
	}
	return s.cert, nil
}

func newSelfSigned(now time.Time) (*tls.Certificate, time.Time, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, time.Time{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, time.Time{}, err
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, time.Time{}, err
	}
	// A generic host-like name: nothing about the product or the owner.
	cn := "srv-" + hex.EncodeToString(rnd[:])
	notBefore := now.Add(-time.Hour).Truncate(time.Hour)
	notAfter := notBefore.Add(selfSignedValidity)
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		DNSNames:              []string{cn},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, time.Time{}, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, notAfter, nil
}

// customTLSConfig serves the operator's certificate (for example a Cloudflare
// Origin CA certificate). The files' modification times are checked at most
// every 30 s and the pair is read again when one changed, so a renewal needs
// no restart; a pair that fails to load is ignored and the old one keeps
// serving.
func customTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile, every: 30 * time.Second}
	if err := r.load(); err != nil {
		return nil, err
	}
	return &tls.Config{
		GetCertificate: r.get,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1"},
	}, nil
}

type certReloader struct {
	certFile, keyFile string
	every             time.Duration

	mu       sync.Mutex
	cert     *tls.Certificate
	certMod  time.Time
	keyMod   time.Time
	lastStat time.Time
}

func (r *certReloader) load() error {
	if r.certFile == "" || r.keyFile == "" {
		return errors.New("front: tls custom needs cert_file and key_file")
	}
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("front: load the custom certificate: %w", err)
	}
	r.cert = &cert
	r.certMod, r.keyMod = modTime(r.certFile), modTime(r.keyFile)
	r.lastStat = time.Now()
	return nil
}

func modTime(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func (r *certReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.lastStat) >= r.every {
		r.lastStat = time.Now()
		if cm, km := modTime(r.certFile), modTime(r.keyFile); !cm.Equal(r.certMod) || !km.Equal(r.keyMod) {
			if cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile); err == nil {
				r.cert, r.certMod, r.keyMod = &cert, cm, km
			} // else keep serving the certificate that works
		}
	}
	return r.cert, nil
}
