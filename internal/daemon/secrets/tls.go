package secrets

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	stderrors "errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Files inside tls/<tunnel>/.
const (
	CertFile    = "cert.pem"
	KeyFile     = "key.pem"
	ACMEDir     = "acme"
	P12File     = "tls.p12"
	P12PassFile = "p12.pass"
	// P12CertFile records the sha256 of the certificate tls.p12 was built
	// from, so the bundle is rebuilt only when the certificate changes.
	P12CertFile = "tls.p12.cert-sha256"
)

// TLSMaterial is the tunnel TLS certificate the backends use (section 10).
type TLSMaterial struct {
	CertPEM []byte // leaf first, then any chain
	KeyPEM  []byte
	// CertSHA256 is the lowercase hex sha256 of the leaf DER (hysteria2
	// pinSHA256).
	CertSHA256 string
	// Mode is the mode actually in use: auto, acme or custom. It is auto
	// when acme was requested but no valid ACME certificate exists.
	Mode     string
	NotAfter time.Time
	// Warning is a yellow, non-fatal problem: DEY-T003 when tls.mode acme
	// fell back to auto (the hub emits acme_failed), DEY-T006 when the
	// certificate expires within 14 days.
	Warning error
}

// TunnelTLS returns the TLS material of tunnel for mode (section 10).
//
//   - auto: tls/<tunnel>/{cert.pem,key.pem} issued by the internal CA with
//     SAN = ips + domain; re-issued when it expires within 30 days, when the
//     SANs changed, when it was not issued by the current CA or when the
//     files are missing or damaged.
//   - acme: tls/<tunnel>/acme/{cert.pem,key.pem} when present, unexpired,
//     matching and valid for domain; otherwise the auto material with
//     Warning DEY-T003 (the hub obtains ACME certificates elsewhere and
//     stores them with StoreACME).
//   - custom: tlsutil.ValidateCustom(customCert, customKey) (DEY-T001/T002/
//     T005/T008), then the files are read.
//
// The result is remembered for PKCS12.
func (s *Store) TunnelTLS(tunnel string, mode string, ips []net.IP, domain, customCert, customKey string) (TLSMaterial, error) {
	if err := checkID("tunnel", tunnel); err != nil {
		return TLSMaterial{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		m   TLSMaterial
		err error
	)
	switch mode {
	case "", config.TLSModeAuto:
		m, err = s.autoTLS(tunnel, ips, domain)
	case config.TLSModeACME:
		m, err = s.acmeTLS(tunnel, ips, domain)
	case config.TLSModeCustom:
		m, err = s.customTLS(customCert, customKey)
	default:
		return TLSMaterial{}, deyerr.New(deyerr.C013, deyerr.Params{"field": "tls.mode", "value": mode, "allowed": "auto, acme, custom"})
	}
	if err != nil {
		return TLSMaterial{}, err
	}
	if m.Warning == nil {
		if w := tlsutil.ExpiryWarning(m.CertPEM, s.now()); w != nil {
			m.Warning = w
		}
	}
	if s.tls == nil {
		s.tls = map[string]TLSMaterial{}
	}
	s.tls[tunnel] = m
	return m, nil
}

// autoTLS returns (issuing when needed) the internal-CA certificate.
func (s *Store) autoTLS(tunnel string, ips []net.IP, domain string) (TLSMaterial, error) {
	dir := s.TLSPath(tunnel)
	certPath, keyPath := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	now := s.now()
	wantIPs, wantDNS := normalizeSANs(ips, domain)
	certPEM, cerr := readSecret(certPath)
	keyPEM, kerr := readSecret(keyPath)
	if cerr == nil && kerr == nil {
		if cert, ok := s.reusableAuto(certPEM, keyPEM, wantIPs, wantDNS, now); ok {
			return material(certPEM, keyPEM, cert, config.TLSModeAuto)
		}
	}
	if s.CA == nil || s.CA.Cert == nil {
		return TLSMaterial{}, deyerr.New(deyerr.T007, deyerr.Params{"path": filepath.Join(s.Dir(), "ca.crt")})
	}
	var sanIPs []net.IP
	for _, ip := range wantIPs {
		sanIPs = append(sanIPs, net.ParseIP(ip))
	}
	certPEM, keyPEM, err := s.CA.IssueTunnel(tunnel, sanIPs, wantDNS, now)
	if err != nil {
		return TLSMaterial{}, err
	}
	if err := tlsutil.WriteSecretPair(certPath, certPEM, keyPath, keyPEM); err != nil {
		return TLSMaterial{}, err
	}
	cert, err := tlsutil.ParseCert(certPEM)
	if err != nil {
		return TLSMaterial{}, err
	}
	return material(certPEM, keyPEM, cert, config.TLSModeAuto)
}

// reusableAuto reports whether the stored auto certificate can stay: it
// parses, its key matches, it was signed by the current CA, it does not
// need renewal (30 days) and its SANs are exactly the wanted ones.
func (s *Store) reusableAuto(certPEM, keyPEM []byte, wantIPs, wantDNS []string, now time.Time) (*x509.Certificate, bool) {
	cert, err := tlsutil.ParseCert(certPEM)
	if err != nil {
		return nil, false
	}
	key, err := tlsutil.ParsePrivateKey(keyPEM)
	if err != nil || !tlsutil.KeyMatchesCert(cert, key) {
		return nil, false
	}
	if s.CA != nil && s.CA.Cert != nil && cert.CheckSignatureFrom(s.CA.Cert) != nil {
		return nil, false
	}
	if tlsutil.NeedsRenewal(certPEM, now, tlsutil.RenewBefore) {
		return nil, false
	}
	gotIPs := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		gotIPs = append(gotIPs, normIP(ip))
	}
	gotDNS := make([]string, 0, len(cert.DNSNames))
	for _, n := range cert.DNSNames {
		gotDNS = append(gotDNS, normDNS(n))
	}
	return cert, sameSet(gotIPs, wantIPs) && sameSet(gotDNS, wantDNS)
}

// acmeTLS returns the stored ACME certificate or falls back to auto.
func (s *Store) acmeTLS(tunnel string, ips []net.IP, domain string) (TLSMaterial, error) {
	dir := filepath.Join(s.TLSPath(tunnel), ACMEDir)
	certPEM, cerr := readSecret(filepath.Join(dir, CertFile))
	keyPEM, kerr := readSecret(filepath.Join(dir, KeyFile))
	if cerr == nil && kerr == nil && domain != "" {
		if cert, err := checkPair(certPEM, keyPEM); err == nil && s.now().Before(cert.NotAfter) && cert.VerifyHostname(domain) == nil {
			return material(certPEM, keyPEM, cert, config.TLSModeACME)
		}
	}
	m, err := s.autoTLS(tunnel, ips, domain)
	if err != nil {
		return TLSMaterial{}, err
	}
	m.Warning = deyerr.New(deyerr.T003, deyerr.Params{"domain": domain}).
		WithWhy("no valid ACME certificate is available yet; the tunnel uses the internal certificate (tls.mode auto) until one is obtained")
	return m, nil
}

// customTLS validates and reads an owner-supplied certificate.
func (s *Store) customTLS(certPath, keyPath string) (TLSMaterial, error) {
	if certPath == "" || keyPath == "" {
		return TLSMaterial{}, deyerr.New(deyerr.T008, deyerr.Params{"path": certPath + " " + keyPath, "reason": "tls.cert_file and tls.key_file are required for tls.mode custom"})
	}
	if err := tlsutil.ValidateCustom(certPath, keyPath, s.now()); err != nil {
		return TLSMaterial{}, err
	}
	certPEM, err := os.ReadFile(certPath) // #nosec G304 -- owner-configured certificate path, validated above
	if err != nil {
		return TLSMaterial{}, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": certPath, "reason": err.Error()})
	}
	keyPEM, err := os.ReadFile(keyPath) // #nosec G304 -- owner-configured key path, validated above
	if err != nil {
		return TLSMaterial{}, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": keyPath, "reason": err.Error()})
	}
	cert, err := checkPair(certPEM, keyPEM)
	if err != nil {
		return TLSMaterial{}, err
	}
	return material(certPEM, keyPEM, cert, config.TLSModeCustom)
}

// StoreACME saves an ACME certificate for tunnel (tls/<tunnel>/acme/,
// 0600). The certificate must parse, be unexpired and match the key.
func (s *Store) StoreACME(tunnel string, certPEM, keyPEM []byte) error {
	if err := checkID("tunnel", tunnel); err != nil {
		return err
	}
	cert, err := checkPair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	if now := s.now(); now.After(cert.NotAfter) {
		return deyerr.New(deyerr.T001, deyerr.Params{"path": "acme certificate of tunnel " + tunnel, "expiry": cert.NotAfter.UTC().Format("2006-01-02")})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.TLSPath(tunnel), ACMEDir)
	return tlsutil.WriteSecretPair(filepath.Join(dir, CertFile), certPEM, filepath.Join(dir, KeyFile), keyPEM)
}

// PKCS12 returns the PKCS#12 bundle (rathole/tls, section 7.2) of the
// material the last TunnelTLS call returned for tunnel (the stored auto
// certificate when TunnelTLS was not called yet in this process) and its
// password. The password is created once and kept in tls/<tunnel>/p12.pass;
// the bundle is kept in tls/<tunnel>/tls.p12 and rebuilt only when the
// certificate changes, so re-rendering produces identical files.
func (s *Store) PKCS12(tunnel string) (p12 []byte, password string, err error) {
	if err := checkID("tunnel", tunnel); err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.tls[tunnel]
	if !ok {
		dir := s.TLSPath(tunnel)
		certPEM, err := readSecret(filepath.Join(dir, CertFile))
		if err != nil {
			return nil, "", unreadable(filepath.Join(dir, CertFile), err)
		}
		keyPEM, err := readSecret(filepath.Join(dir, KeyFile))
		if err != nil {
			return nil, "", unreadable(filepath.Join(dir, KeyFile), err)
		}
		cert, err := checkPair(certPEM, keyPEM)
		if err != nil {
			return nil, "", err
		}
		if m, err = material(certPEM, keyPEM, cert, config.TLSModeAuto); err != nil {
			return nil, "", err
		}
	}
	dir := s.TLSPath(tunnel)
	passPath := filepath.Join(dir, P12PassFile)
	pass, err := readSecret(passPath)
	switch {
	case err == nil && len(bytes.TrimSpace(pass)) > 0:
		password = string(bytes.TrimSpace(pass))
		deylog.RegisterSecret(password)
	case err == nil || stderrors.Is(err, fs.ErrNotExist):
		if password, err = tlsutil.NewPKCS12Password(); err != nil {
			return nil, "", err
		}
		if err := tlsutil.WriteSecret(passPath, []byte(password+"\n")); err != nil {
			return nil, "", err
		}
		// A new password invalidates any stored bundle.
		_ = os.Remove(filepath.Join(dir, P12CertFile))
	default:
		return nil, "", unreadable(passPath, err)
	}
	p12Path, markPath := filepath.Join(dir, P12File), filepath.Join(dir, P12CertFile)
	if mark, err := readSecret(markPath); err == nil && strings.TrimSpace(string(mark)) == m.CertSHA256 {
		if data, err := readSecret(p12Path); err == nil && len(data) > 0 {
			return data, password, nil
		}
	}
	p12, err = tlsutil.EncodePKCS12(m.CertPEM, m.KeyPEM, password)
	if err != nil {
		return nil, "", err
	}
	if err := tlsutil.WriteSecret(p12Path, p12); err != nil {
		return nil, "", err
	}
	if err := tlsutil.WriteSecret(markPath, []byte(m.CertSHA256+"\n")); err != nil {
		return nil, "", err
	}
	return p12, password, nil
}

// checkPair parses the leaf and the key and verifies they belong together.
func checkPair(certPEM, keyPEM []byte) (*x509.Certificate, error) {
	cert, err := tlsutil.ParseCert(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := tlsutil.ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	if !tlsutil.KeyMatchesCert(cert, key) {
		return nil, deyerr.New(deyerr.T002, deyerr.Params{"cert": "certificate", "key": "key"})
	}
	return cert, nil
}

// material builds a TLSMaterial for a parsed leaf. CertPEM is reduced to
// its CERTIFICATE blocks and KeyPEM to its first private key block: an
// owner-supplied "combined" PEM (chain and key in one file, as HAProxy
// uses) must never put the private key into the certificate copy, which
// the planner also hands to TLS clients (trust anchors) that must not get
// the key.
func material(certPEM, keyPEM []byte, cert *x509.Certificate, mode string) (TLSMaterial, error) {
	certPEM = pemBlocks(certPEM, func(t string) bool { return t == "CERTIFICATE" }, 0)
	keyPEM = pemBlocks(keyPEM, func(t string) bool { return strings.HasSuffix(t, "PRIVATE KEY") }, 1)
	sum, err := tlsutil.CertSHA256Hex(certPEM)
	if err != nil {
		return TLSMaterial{}, err
	}
	return TLSMaterial{CertPEM: certPEM, KeyPEM: keyPEM, CertSHA256: sum, Mode: mode, NotAfter: cert.NotAfter.UTC()}, nil
}

// pemBlocks re-encodes the PEM blocks of data whose type keep accepts, in
// file order (at most limit blocks when limit > 0). Headers are dropped
// (encrypted keys are refused earlier).
func pemBlocks(data []byte, keep func(blockType string) bool, limit int) []byte {
	var out bytes.Buffer
	n := 0
	for rest := data; ; {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if !keep(b.Type) {
			continue
		}
		_ = pem.Encode(&out, &pem.Block{Type: b.Type, Bytes: b.Bytes}) // writes to a bytes.Buffer cannot fail
		if n++; limit > 0 && n >= limit {
			break
		}
	}
	return out.Bytes()
}

// normalizeSANs returns the wanted IP SANs (canonical strings, IP literals
// in domain included) and DNS SANs, each sorted and unique.
func normalizeSANs(ips []net.IP, domain string) (ipOut, dnsOut []string) {
	for _, ip := range ips {
		if ip == nil || (ip.To4() == nil && len(ip) != net.IPv6len) {
			continue
		}
		ipOut = appendUnique(ipOut, normIP(ip))
	}
	if d := strings.TrimSpace(domain); d != "" {
		lit := strings.TrimSuffix(strings.TrimPrefix(d, "["), "]")
		if ip := net.ParseIP(lit); ip != nil {
			ipOut = appendUnique(ipOut, normIP(ip))
		} else if n := normDNS(d); n != "" {
			dnsOut = appendUnique(dnsOut, n)
		}
	}
	sort.Strings(ipOut)
	sort.Strings(dnsOut)
	return ipOut, dnsOut
}

func normIP(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.String()
}

func normDNS(n string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".") }

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

// sameSet compares two string lists as sets.
func sameSet(a, b []string) bool {
	seen := map[string]bool{}
	for _, x := range a {
		seen[x] = true
	}
	if len(seen) != len(uniq(b)) {
		return false
	}
	for _, x := range b {
		if !seen[x] {
			return false
		}
	}
	return true
}

func uniq(list []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range list {
		m[x] = true
	}
	return m
}
