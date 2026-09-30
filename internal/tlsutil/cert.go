package tlsutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"math"
	"strconv"
	"strings"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// FingerprintPrefix starts every fingerprint string.
const FingerprintPrefix = "sha256:"

// pemDataLabel is the {path} of DEY-T008 when the input is PEM data rather
// than a file.
const pemDataLabel = "(PEM data)"

// dateLayout formats expiry dates in DEY messages.
const dateLayout = "2006-01-02"

// Info summarises one certificate for `deyroute security tls show`, the
// dashboard and `security audit`.
type Info struct {
	Subject     string    // subject common name (or full subject when CN is empty)
	Issuer      string    // issuer common name
	SANs        []string  // DNS names then IP addresses
	NotBefore   time.Time // UTC
	NotAfter    time.Time // UTC
	Fingerprint string    // "sha256:<hex>" of the DER
	IsCA        bool
	// Role is "ca", "hub", "node" or "tunnel" for certificates of the
	// internal CA (see CertRole), empty for foreign ones (ACME, custom).
	Role string
}

// CertRole returns the role of a certificate: RoleCA for a CA certificate,
// HubOU, NodeOU or TunnelOU for leaves issued by the internal CA (their
// subject OU with Organization "DEYROUTE"), "" otherwise.
func CertRole(c *x509.Certificate) string {
	if c == nil {
		return ""
	}
	if c.IsCA {
		return RoleCA
	}
	if !contains(c.Subject.Organization, Organization) {
		return ""
	}
	for _, ou := range c.Subject.OrganizationalUnit {
		switch ou {
		case HubOU, NodeOU, TunnelOU:
			return ou
		}
	}
	return ""
}

// hasRole reports whether c is a leaf of the internal CA with role.
func hasRole(c *x509.Certificate, role string) bool {
	return c != nil && !c.IsCA && CertRole(c) == role
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// DaysLeft returns the whole days until NotAfter (negative once expired).
func (i Info) DaysLeft(now time.Time) int {
	return int(math.Floor(i.NotAfter.Sub(now).Hours() / 24))
}

// Expired reports whether the certificate is expired at now.
func (i Info) Expired(now time.Time) bool { return !now.Before(i.NotAfter) }

// CertInfo describes the first certificate (the leaf) of pemBytes.
func CertInfo(pemBytes []byte) (Info, error) {
	c, err := ParseCert(pemBytes)
	if err != nil {
		return Info{}, err
	}
	return InfoOf(c), nil
}

// InfoOf describes a parsed certificate (the zero Info for nil).
func InfoOf(c *x509.Certificate) Info {
	if c == nil {
		return Info{}
	}
	sans := make([]string, 0, len(c.DNSNames)+len(c.IPAddresses))
	sans = append(sans, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		sans = append(sans, ip.String())
	}
	subject := c.Subject.CommonName
	if subject == "" {
		subject = c.Subject.String()
	}
	return Info{
		Subject:     subject,
		Issuer:      c.Issuer.CommonName,
		SANs:        sans,
		NotBefore:   c.NotBefore.UTC(),
		NotAfter:    c.NotAfter.UTC(),
		Fingerprint: Fingerprint(c.Raw),
		IsCA:        c.IsCA,
		Role:        CertRole(c),
	}
}

// ParseCert returns the first certificate in pemBytes. Garbage is DEY-T008.
func ParseCert(pemBytes []byte) (*x509.Certificate, error) {
	chain, err := ParseCertChain(pemBytes)
	if err != nil {
		return nil, err
	}
	return chain[0], nil
}

// ParseCertChain returns every certificate in pemBytes in file order (leaf
// first). Garbage or no certificate at all is DEY-T008.
func ParseCertChain(pemBytes []byte) ([]*x509.Certificate, error) {
	chain, err := parseCerts(pemBytes)
	if err != nil {
		return nil, parseErr(pemDataLabel, err)
	}
	return chain, nil
}

// parseCerts is ParseCertChain with plain (non-DEY) errors.
func parseCerts(pemBytes []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != pemCertificate {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, deyerr.Plain("no PEM certificate found")
	}
	return out, nil
}

// ParsePrivateKey parses the first private key in pemBytes: PKCS#8
// ("PRIVATE KEY"), SEC1 ("EC PRIVATE KEY") or PKCS#1 ("RSA PRIVATE KEY").
// Encrypted keys are not supported. Errors are DEY-T008.
func ParsePrivateKey(pemBytes []byte) (crypto.Signer, error) {
	k, err := parseKey(pemBytes)
	if err != nil {
		return nil, parseErr(pemDataLabel, err)
	}
	return k, nil
}

// parseKey is ParsePrivateKey with plain (non-DEY) errors.
func parseKey(pemBytes []byte) (crypto.Signer, error) {
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, deyerr.Plain("no PEM private key found")
		}
		var (
			key any
			err error
		)
		switch block.Type {
		case pemPrivateKey:
			key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		case pemECKey:
			key, err = x509.ParseECPrivateKey(block.Bytes)
		case pemRSAKey:
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case pemEncKey:
			return nil, deyerr.Plain("encrypted private keys are not supported; decrypt the key first")
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		switch k := key.(type) {
		case ed25519.PrivateKey:
			return k, nil
		case *ecdsa.PrivateKey:
			return k, nil
		case *rsa.PrivateKey:
			return k, nil
		default:
			return nil, deyerr.Plain("unsupported private key type")
		}
	}
}

// EncodeCertPEM wraps a DER certificate in a CERTIFICATE PEM block.
func EncodeCertPEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Bytes: der})
}

// EncodeKeyPEM encodes a private key as a PKCS#8 "PRIVATE KEY" PEM block,
// the format every backend accepts.
func EncodeKeyPEM(key crypto.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, internalErr("encode private key", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemPrivateKey, Bytes: der}), nil
}

// CertSHA256Hex returns the lowercase hex SHA-256 of the leaf DER without
// the "sha256:" prefix (hysteria2 pinSHA256, backend.Secrets.TLSCertSHA256).
func CertSHA256Hex(pemBytes []byte) (string, error) {
	c, err := ParseCert(pemBytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// NormalizeFingerprint accepts "sha256:<hex>" or bare hex (any case, with
// optional ':' separators between bytes) and returns the canonical
// "sha256:<64 lowercase hex>" form.
func NormalizeFingerprint(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= len(FingerprintPrefix) && strings.EqualFold(s[:len(FingerprintPrefix)], FingerprintPrefix) {
		s = s[len(FingerprintPrefix):]
	}
	s = strings.ToLower(strings.ReplaceAll(s, ":", ""))
	if len(s) != 2*sha256.Size {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return FingerprintPrefix + s, true
}

// ValidFingerprint reports whether s is a well-formed fingerprint.
func ValidFingerprint(s string) bool {
	_, ok := NormalizeFingerprint(s)
	return ok
}

// keyMismatchErr is DEY-T002 for in-memory PEM data, which has no file
// names to put in the catalog's Why line.
func keyMismatchErr() *deyerr.Error {
	return deyerr.New(deyerr.T002, deyerr.Params{"cert": pemDataLabel, "key": pemDataLabel}).
		WithWhy("the private key does not belong to the certificate")
}

// KeyMatchesCert reports whether key is the private key of cert.
func KeyMatchesCert(cert *x509.Certificate, key crypto.PrivateKey) bool {
	if cert == nil || key == nil {
		return false
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return false
	}
	pub, ok := signer.Public().(interface{ Equal(crypto.PublicKey) bool })
	if !ok {
		return false
	}
	return pub.Equal(cert.PublicKey)
}

// NeedsRenewal reports whether the leaf in certPEM expires within before of
// now (or cannot be parsed, which also calls for a new certificate).
func NeedsRenewal(certPEM []byte, now time.Time, before time.Duration) bool {
	c, err := parseCerts(certPEM)
	if err != nil {
		return true
	}
	return !now.Add(before).Before(c[0].NotAfter)
}

// ExpiryWarning returns DEY-T001 when the leaf in certPEM is expired,
// DEY-T006 when it expires within WarnBefore (14 days) and nil otherwise.
// The certificate's common name (or first SAN) is used as {path}.
func ExpiryWarning(certPEM []byte, now time.Time) error {
	c, err := ParseCert(certPEM)
	if err != nil {
		return err
	}
	return CheckExpiry(certLabel(c), c, now)
}

// CheckExpiry is ExpiryWarning for a parsed certificate; label fills the
// {path} parameter (a file path or a name).
func CheckExpiry(label string, c *x509.Certificate, now time.Time) error {
	if c == nil {
		return deyerr.New(deyerr.T008, deyerr.Params{"path": label, "reason": "no certificate"})
	}
	if !now.Before(c.NotAfter) {
		return deyerr.New(deyerr.T001, deyerr.Params{"path": label, "expiry": c.NotAfter.UTC().Format(dateLayout)})
	}
	if left := c.NotAfter.Sub(now); left <= WarnBefore {
		days := InfoOf(c).DaysLeft(now)
		return deyerr.New(deyerr.T006, deyerr.Params{"path": label, "days": strconv.Itoa(days)})
	}
	return nil
}

func certLabel(c *x509.Certificate) string {
	switch {
	case c.Subject.CommonName != "":
		return c.Subject.CommonName
	case len(c.DNSNames) > 0:
		return c.DNSNames[0]
	case len(c.IPAddresses) > 0:
		return c.IPAddresses[0].String()
	default:
		return Fingerprint(c.Raw)
	}
}

// ValidateCustom validates an owner-supplied certificate for tls.mode
// custom (section 10): the cert file holds the chain leaf first; every
// certificate must be signed by the next one (DEY-T005), none may be expired
// (DEY-T001) and the key must belong to the leaf (DEY-T002). Unreadable
// files are DEY-T008.
func ValidateCustom(certPath, keyPath string, now time.Time) error {
	certPEM, err := readPEMFile(certPath, false)
	if err != nil {
		return err
	}
	chain, err := parseCerts(certPEM)
	if err != nil {
		return parseErr(certPath, err)
	}
	for _, c := range chain {
		if !now.Before(c.NotAfter) {
			return deyerr.New(deyerr.T001, deyerr.Params{"path": certPath, "expiry": c.NotAfter.UTC().Format(dateLayout)})
		}
		if now.Before(c.NotBefore) {
			return deyerr.New(deyerr.T005, deyerr.Params{"path": certPath}).
				WithWhy("a certificate in the file is not valid before " + c.NotBefore.UTC().Format(dateLayout))
		}
	}
	for i := 0; i+1 < len(chain); i++ {
		if err := chain[i].CheckSignatureFrom(chain[i+1]); err != nil {
			return deyerr.Wrap(deyerr.T005, err, deyerr.Params{"path": certPath})
		}
	}
	if !serverUsable(chain[0]) {
		return deyerr.New(deyerr.T005, deyerr.Params{"path": certPath}).
			WithWhy("the certificate is not valid for TLS servers (extended key usage)")
	}
	keyPEM, err := readPEMFile(keyPath, false)
	if err != nil {
		return err
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return parseErr(keyPath, err)
	}
	if !KeyMatchesCert(chain[0], key) {
		return deyerr.New(deyerr.T002, deyerr.Params{"cert": certPath, "key": keyPath})
	}
	return nil
}

// serverUsable reports whether c may authenticate a TLS server: no extended
// key usage at all, ServerAuth or Any.
func serverUsable(c *x509.Certificate) bool {
	if len(c.ExtKeyUsage) == 0 && len(c.UnknownExtKeyUsage) == 0 {
		return true
	}
	for _, u := range c.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}
