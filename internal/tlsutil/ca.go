package tlsutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Validity periods and renewal windows (sections 3, 10 and 11).
const (
	// CAValidity is the lifetime of the internal CA.
	CAValidity = 20 * 365 * 24 * time.Hour
	// HubCertValidity is the lifetime of the hub control API certificate.
	HubCertValidity = 10 * 365 * 24 * time.Hour
	// NodeCertValidity is the lifetime of a node client certificate (section 3).
	NodeCertValidity = 10 * 365 * 24 * time.Hour
	// TunnelCertValidity is the lifetime of a tunnel TLS certificate in auto
	// mode (section 10).
	TunnelCertValidity = 3 * 365 * 24 * time.Hour
	// RenewBefore is how long before expiry tunnel certificates are renewed.
	RenewBefore = 30 * 24 * time.Hour
	// WarnBefore is how long before expiry the dashboard shows a yellow
	// warning (DEY-T006).
	WarnBefore = 14 * 24 * time.Hour
	// ClockSkew is subtracted from NotBefore of every issued certificate so
	// that servers with slightly different clocks accept it immediately.
	ClockSkew = time.Hour
)

// Naming of issued certificates.
const (
	// CAPrefix starts the common name of every deyroute CA ("DEYROUTE CA ir-1").
	CAPrefix = "DEYROUTE CA"
	// Organization is the subject organization of every issued certificate.
	Organization = "DEYROUTE"
	// TunnelCNPrefix starts the common name of tunnel certificates.
	TunnelCNPrefix = "tunnel-"
	// maxCNLen is the X.520 upper bound (in characters) for a common name.
	maxCNLen = 64
	// maxPEMFileSize bounds certificate and key files read from disk; a
	// chain or a key is a few KiB.
	maxPEMFileSize = 1 << 20
)

// Certificate roles. Every certificate the internal CA issues carries its
// role as the subject organizational unit. The roles keep the identities
// apart even though one CA signs all of them: tunnel certificates are
// ServerAuth too and their keys are copied to backend directories readable by
// the deyroute user (and to nodes for node-side TLS servers), so the control
// channel and the join step accept only a server certificate with HubOU and
// the hub accepts only client certificates with NodeOU.
const (
	// HubOU marks the hub control API certificate (IssueServer).
	HubOU = "hub"
	// NodeOU marks node client certificates (SignCSR).
	NodeOU = "node"
	// TunnelOU marks tunnel TLS certificates (IssueTunnel).
	TunnelOU = "tunnel"
)

// Role names returned by CertRole in addition to the OU roles above.
const (
	// RoleCA is the role of a CA certificate.
	RoleCA = "ca"
)

// PEM block types.
const (
	pemCertificate = "CERTIFICATE"
	pemPrivateKey  = "PRIVATE KEY"
	pemECKey       = "EC PRIVATE KEY"
	pemRSAKey      = "RSA PRIVATE KEY"
	pemEncKey      = "ENCRYPTED PRIVATE KEY"
	pemCSR         = "CERTIFICATE REQUEST"
	pemNewCSR      = "NEW CERTIFICATE REQUEST"
)

// CA is the internal Ed25519 certificate authority of a hub.
type CA struct {
	Cert    *x509.Certificate
	Key     ed25519.PrivateKey
	CertPEM []byte
	// Now returns the current time for certificates issued without an
	// explicit time (IssueServer, SignCSR). nil means time.Now.
	Now func() time.Time
}

// CACommonName returns the CA common name for a hub: "DEYROUTE CA <hub>".
// A name that already starts with "DEYROUTE CA" is kept as is. Non-printable
// characters are dropped and the result is cut to the X.520 limit of 64
// characters (the CN is informational; the fingerprint is the identity).
func CACommonName(hub string) string {
	hub = strings.TrimSpace(hub)
	cn := CAPrefix
	switch {
	case hub == "":
	case hub == CAPrefix || strings.HasPrefix(hub, CAPrefix+" "):
		cn = hub
	default:
		cn = CAPrefix + " " + hub
	}
	return sanitizeCN(cn)
}

// sanitizeCN drops non-printable runes and truncates to maxCNLen runes.
func sanitizeCN(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if !unicode.IsPrint(r) {
			continue
		}
		if len(out) == maxCNLen {
			break
		}
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

// NewCA creates a self-signed Ed25519 CA valid for 20 years. cn is the hub
// name (or a full "DEYROUTE CA <hub>" name, see CACommonName).
func NewCA(cn string, now time.Time) (*CA, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, internalErr("generate CA key", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: CACommonName(cn), Organization: []string{Organization}},
		NotBefore:             now.Add(-ClockSkew).UTC(),
		NotAfter:              now.Add(CAValidity).UTC(),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	if err != nil {
		return nil, internalErr("create CA certificate", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, internalErr("parse CA certificate", err)
	}
	return &CA{Cert: cert, Key: priv, CertPEM: EncodeCertPEM(der)}, nil
}

// LoadCA reads the CA certificate and its PKCS#8 Ed25519 key. A missing file
// is DEY-T007, an unparsable one DEY-T008 and a key that does not belong to
// the certificate DEY-T002.
func LoadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := readPEMFile(certPath, true)
	if err != nil {
		return nil, err
	}
	chain, err := parseCerts(certPEM)
	if err != nil {
		return nil, parseErr(certPath, err)
	}
	cert := chain[0]
	if !cert.IsCA {
		return nil, deyerr.New(deyerr.T008, deyerr.Params{"path": certPath, "reason": "the certificate is not a CA certificate"})
	}
	keyPEM, err := readPEMFile(keyPath, true)
	if err != nil {
		return nil, err
	}
	signer, err := parseKey(keyPEM)
	if err != nil {
		return nil, parseErr(keyPath, err)
	}
	key, ok := signer.(ed25519.PrivateKey)
	if !ok {
		return nil, deyerr.New(deyerr.T008, deyerr.Params{"path": keyPath, "reason": "the CA key is not an Ed25519 key"})
	}
	if !KeyMatchesCert(cert, key) {
		return nil, deyerr.New(deyerr.T002, deyerr.Params{"cert": certPath, "key": keyPath})
	}
	return &CA{Cert: cert, Key: key, CertPEM: EncodeCertPEM(cert.Raw)}, nil
}

// Save writes ca.crt and ca.key with mode 0600 (section 4: every file in
// secrets/ is 0600 root) through WriteSecretPair: both files are staged
// before either is replaced, so a failed write (disk full) never leaves a
// new certificate next to an old key.
func (ca *CA) Save(certPath, keyPath string) error {
	if err := ca.check(); err != nil {
		return err
	}
	keyPEM, err := ca.KeyPEM()
	if err != nil {
		return err
	}
	return WriteSecretPair(certPath, ca.CertPEM, keyPath, keyPEM)
}

// KeyPEM returns the CA private key as a PKCS#8 PEM block.
func (ca *CA) KeyPEM() ([]byte, error) {
	if err := ca.check(); err != nil {
		return nil, err
	}
	return EncodeKeyPEM(ca.Key)
}

// Fingerprint returns "sha256:" followed by the lowercase hex SHA-256 of der.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return FingerprintPrefix + hex.EncodeToString(sum[:])
}

// Fingerprint returns the pinned identity of the CA (the value in join
// links and node configs).
func (ca *CA) Fingerprint() string {
	if ca == nil || ca.Cert == nil {
		return ""
	}
	return Fingerprint(ca.Cert.Raw)
}

// IssueServer issues the hub control API certificate: ECDSA P-256,
// ExtKeyUsage ServerAuth, role HubOU, with the given IP and DNS SANs.
// validity <= 0 means HubCertValidity. Nodes accept only certificates with
// the hub role on the control channel, so never use IssueServer for anything
// but the hub control certificate (tunnels use IssueTunnel).
func (ca *CA) IssueServer(cn string, ips []net.IP, dns []string, validity time.Duration) (certPEM, keyPEM []byte, err error) {
	if err := ca.check(); err != nil {
		return nil, nil, err
	}
	if validity <= 0 {
		validity = HubCertValidity
	}
	return ca.issueECDSA(cn, HubOU, ips, dns, ca.now(), validity)
}

// IssueTunnel issues the tunnel TLS certificate of tunnel (section 10, auto
// mode): ECDSA P-256, CN "tunnel-<id>", role TunnelOU, SAN = hub IPs
// (v4/v6) and the domain when set, valid for 3 years from now. At least one
// SAN is required.
func (ca *CA) IssueTunnel(tunnel string, ips []net.IP, dns []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	sanIPs, sanDNS := splitSANs(ips, dns)
	if len(sanIPs) == 0 && len(sanDNS) == 0 {
		return nil, nil, internalErr("issue tunnel certificate", deyerr.Plain("at least one IP or DNS SAN is required"))
	}
	return ca.issueECDSA(TunnelCNPrefix+tunnel, TunnelOU, ips, dns, now, TunnelCertValidity)
}

// issueECDSA issues a ServerAuth ECDSA P-256 leaf with role ou.
func (ca *CA) issueECDSA(cn, ou string, ips []net.IP, dns []string, now time.Time, validity time.Duration) (certPEM, keyPEM []byte, err error) {
	if err := ca.check(); err != nil {
		return nil, nil, err
	}
	if err := checkCN(cn); err != nil {
		return nil, nil, internalErr("issue certificate", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, internalErr("generate ECDSA key", err)
	}
	tpl, err := ca.leafTemplate(cn, now, validity)
	if err != nil {
		return nil, nil, err
	}
	tpl.Subject.OrganizationalUnit = []string{ou}
	tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tpl.IPAddresses, tpl.DNSNames = splitSANs(ips, dns)
	certPEM, err = ca.sign(tpl, key.Public())
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = EncodeKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// NewKeyAndCSR creates the Ed25519 node key (PKCS#8 PEM) and a CSR for cn.
// The hub ignores everything in the CSR except the public key.
func NewKeyAndCSR(cn string) (csrPEM, keyPEM []byte, err error) {
	if err := checkCN(cn); err != nil {
		return nil, nil, internalErr("create CSR", err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, internalErr("generate node key", err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, priv)
	if err != nil {
		return nil, nil, internalErr("create CSR", err)
	}
	keyPEM, err = EncodeKeyPEM(priv)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: pemCSR, Bytes: der}), keyPEM, nil
}

// SignCSR verifies the CSR signature and issues a client-auth certificate
// for its public key. The subject is always CN=cn (the node id chosen by the
// hub); names and extensions requested in the CSR are never copied.
// validity <= 0 means NodeCertValidity. An invalid CSR is DEY-T009.
func (ca *CA) SignCSR(csrPEM []byte, cn string, validity time.Duration) ([]byte, error) {
	if err := ca.check(); err != nil {
		return nil, err
	}
	csr, err := parseCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	if err := checkCN(cn); err != nil {
		return nil, csrErr(err.Error())
	}
	if validity <= 0 {
		validity = NodeCertValidity
	}
	tpl, err := ca.leafTemplate(cn, ca.now(), validity)
	if err != nil {
		return nil, err
	}
	tpl.Subject.OrganizationalUnit = []string{NodeOU}
	tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	return ca.sign(tpl, csr.PublicKey)
}

func parseCSR(csrPEM []byte) (*x509.CertificateRequest, error) {
	var block *pem.Block
	rest := csrPEM
	for {
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, csrErr("no PEM certificate request found")
		}
		if block.Type == pemCSR || block.Type == pemNewCSR {
			break
		}
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, csrErr("cannot parse the certificate request: " + err.Error())
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, csrErr("the request signature is invalid")
	}
	switch pub := csr.PublicKey.(type) {
	case ed25519.PublicKey:
	case *ecdsa.PublicKey:
		if pub.Curve != elliptic.P256() && pub.Curve != elliptic.P384() {
			return nil, csrErr("unsupported ECDSA curve")
		}
	case *rsa.PublicKey:
		if pub.N.BitLen() < 2048 {
			return nil, csrErr("RSA keys must have at least 2048 bits")
		}
	default:
		return nil, csrErr("unsupported public key type")
	}
	return csr, nil
}

func (ca *CA) leafTemplate(cn string, now time.Time, validity time.Duration) (*x509.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	notAfter := now.Add(validity).UTC()
	if notAfter.After(ca.Cert.NotAfter) {
		// A leaf never outlives its CA; it would stop verifying anyway.
		notAfter = ca.Cert.NotAfter
	}
	return &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{Organization}},
		NotBefore:             now.Add(-ClockSkew).UTC(),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}, nil
}

func (ca *CA) sign(tpl *x509.Certificate, pub crypto.PublicKey) ([]byte, error) {
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, pub, ca.Key)
	if err != nil {
		return nil, internalErr("sign certificate", err)
	}
	return EncodeCertPEM(der), nil
}

func (ca *CA) now() time.Time {
	if ca != nil && ca.Now != nil {
		return ca.Now()
	}
	return time.Now()
}

func (ca *CA) check() error {
	if ca == nil || ca.Cert == nil || len(ca.Key) != ed25519.PrivateKeySize {
		return internalErr("use CA", deyerr.Plain("the CA is not loaded"))
	}
	return nil
}

// randomSerial returns a random positive serial number of up to 128 bits.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	for {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return nil, internalErr("generate serial number", err)
		}
		if n.Sign() > 0 {
			return n, nil
		}
	}
}

// checkCN rejects empty, over-long (more than 64 characters) or
// non-printable common names.
func checkCN(cn string) error {
	if cn == "" {
		return deyerr.Plain("the common name is empty")
	}
	if !utf8.ValidString(cn) {
		return deyerr.Plain("the common name is not valid UTF-8")
	}
	if utf8.RuneCountInString(cn) > maxCNLen {
		return deyerr.Plain("the common name is longer than 64 characters")
	}
	for _, r := range cn {
		if !unicode.IsPrint(r) {
			return deyerr.Plain("the common name contains non-printable characters")
		}
	}
	return nil
}

// splitSANs cleans the SAN lists: DNS entries that are IP literals (e.g. a
// domain setting that holds the hub IP) become IP SANs, because TLS clients
// match IP addresses only against IP SANs.
func splitSANs(ips []net.IP, dns []string) ([]net.IP, []string) {
	allIPs := append([]net.IP(nil), ips...)
	names := make([]string, 0, len(dns))
	for _, n := range dns {
		lit := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(n), "["), "]")
		if ip := net.ParseIP(lit); ip != nil {
			allIPs = append(allIPs, ip)
			continue
		}
		names = append(names, n)
	}
	return cleanIPs(allIPs), cleanDNS(names)
}

// cleanIPs drops nil/invalid IPs and duplicates, keeping order.
func cleanIPs(ips []net.IP) []net.IP {
	out := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if ip == nil || (ip.To4() == nil && len(ip) != net.IPv6len) {
			continue
		}
		if ip4 := ip.To4(); ip4 != nil {
			ip = ip4
		}
		dup := false
		for _, o := range out {
			if o.Equal(ip) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, ip)
		}
	}
	return out
}

// cleanDNS lowercases and trims names, dropping empty ones and duplicates.
func cleanDNS(names []string) []string {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		n = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(n)), ".")
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// readPEMFile reads a certificate or key file. With missingIsT007 a missing
// file is reported as DEY-T007 (internal CA missing), otherwise as DEY-T008.
// Only regular files up to maxPEMFileSize are read: an owner-supplied path
// (tls.mode custom) pointing at a FIFO, a device or a huge file must not
// block or exhaust the daemon.
func readPEMFile(path string, missingIsT007 bool) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if missingIsT007 {
				return nil, deyerr.Wrap(deyerr.T007, err, deyerr.Params{"path": path})
			}
			return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": path, "reason": "the file does not exist"})
		}
		return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": path, "reason": "the file cannot be read"})
	}
	if !st.Mode().IsRegular() {
		return nil, deyerr.New(deyerr.T008, deyerr.Params{"path": path, "reason": "it is not a regular file"})
	}
	f, err := os.Open(path) // #nosec G304 -- path comes from deyroute's own configuration
	if err != nil {
		return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": path, "reason": "the file cannot be read"})
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxPEMFileSize+1))
	if err != nil {
		return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": path, "reason": "the file cannot be read"})
	}
	if len(data) > maxPEMFileSize {
		return nil, deyerr.New(deyerr.T008, deyerr.Params{"path": path, "reason": "the file is larger than 1 MiB, which no certificate or key file is"})
	}
	return data, nil
}

func parseErr(path string, err error) error {
	return deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": path, "reason": err.Error()})
}

func csrErr(reason string) error {
	return deyerr.New(deyerr.T009, deyerr.Params{"reason": reason})
}

func internalErr(op string, err error) error {
	return deyerr.Wrap(deyerr.X000, err, deyerr.Params{"op": op})
}
