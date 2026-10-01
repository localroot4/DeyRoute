package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ALPN is the application protocol of the control channel (section 11).
// It equals api.ALPN; tlsutil cannot import api (api imports tlsutil).
const ALPN = "deyroute/1"

// ServerTLSConfig builds the hub Control API server config: TLS 1.3 only,
// ALPN "deyroute/1" required, client certificates verified against caPEM when
// presented (VerifyClientCertIfGiven: /v1/join has no client certificate,
// every other path must check PeerIdentity).
//
// caPEM may hold several CA certificates (old + new during rotate-ca); only
// CA certificates in it are trusted (see caPool), none at all is DEY-T008.
// When certPEM holds only the leaf, the CA in caPEM that signed it is
// appended to the presented chain so that joining nodes can pin it.
// Session tickets are disabled: every connection performs a full handshake,
// so a node certificate is always verified against the current caPEM (a
// resumed session would carry a certificate from before a rotate-ca).
func ServerTLSConfig(caPEM, certPEM, keyPEM []byte) (*tls.Config, error) {
	pool, cas, err := caPool(caPEM)
	if err != nil {
		return nil, err
	}
	cert, err := keyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	if len(cert.Certificate) == 1 {
		for _, ca := range cas {
			if cert.Leaf.CheckSignatureFrom(ca) == nil {
				cert.Certificate = append(cert.Certificate, ca.Raw)
				break
			}
		}
	}
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		Certificates:           []tls.Certificate{cert},
		ClientAuth:             tls.VerifyClientCertIfGiven,
		ClientCAs:              pool,
		NextProtos:             []string{ALPN},
		SessionTicketsDisabled: true,
		VerifyConnection:       requireALPN,
	}, nil
}

// ClientTLSConfig builds the node side of the control channel: TLS 1.3 only,
// ALPN "deyroute/1", the node certificate as client certificate (optional:
// empty certPEM sends none) and trust in the CA certificates of caPEM only —
// the system roots are never used (section 11). The server certificate must
// be the hub control certificate (role HubOU, see IssueServer): a tunnel
// certificate of the same CA is refused even though it is a valid
// ServerAuth certificate. Validity is checked at the returned config's Time
// (time.Now when nil), as crypto/tls does.
//
// With a serverName the hub certificate must also carry that name (IP or
// DNS SAN). With an empty serverName only the chain to the pinned CA, the
// ServerAuth usage and the hub role are verified, so a hub that moved to a
// new IP keeps working (hub announce-move / node set-hub).
func ClientTLSConfig(caPEM, certPEM, keyPEM []byte, serverName string) (*tls.Config, error) {
	pool, _, err := caPool(caPEM)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
		NextProtos: []string{ALPN},
		ServerName: serverName,
	}
	if len(certPEM) > 0 || len(keyPEM) > 0 {
		cert, err := keyPair(certPEM, keyPEM)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if serverName == "" {
		// Hostname-free verification: the chain is verified against the
		// private pool below, so skipping crypto/tls' own check (which needs
		// a name) does not weaken anything.
		cfg.InsecureSkipVerify = true // #nosec G402 -- chain verified in VerifyConnection against the pinned CA pool
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if _, err := verifyServerChain(cs.PeerCertificates, pool, "", configNow(cfg)); err != nil {
				return err
			}
			return verifyHubConnection(cs)
		}
	} else {
		// crypto/tls has verified chain, name and ServerAuth already.
		cfg.VerifyConnection = verifyHubConnection
	}
	return cfg, nil
}

// verifyHubConnection checks the hub role of the server leaf and the ALPN.
func verifyHubConnection(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 || !hasRole(cs.PeerCertificates[0], HubOU) {
		return errNotHubCert
	}
	return requireALPN(cs)
}

// errNotHubCert aborts a control-channel handshake whose server certificate
// is signed by our CA but is not the hub control certificate.
var errNotHubCert = deyerr.Plain("tlsutil: the server certificate is not the deyroute hub control certificate")

// JoinClientTLSConfig is the config a node uses for POST /v1/join before it
// has the CA: no client certificate, TLS 1.3, ALPN "deyroute/1", and trust
// only in a chain that contains the CA whose fingerprint is in the join link
// (PinnedCAVerifier). A mismatch aborts the handshake with DEY-N002.
func JoinClientTLSConfig(fingerprint string) *tls.Config {
	want := normalizePin(fingerprint)
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{ALPN},
		// The system roots must not be trusted; the pinned CA check in
		// VerifyConnection replaces crypto/tls' verification. VerifyConnection
		// also runs on resumed sessions, unlike VerifyPeerCertificate.
		InsecureSkipVerify: true, // #nosec G402 -- verified by the pinned CA fingerprint in VerifyConnection
	}
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		raw := make([][]byte, 0, len(cs.PeerCertificates))
		for _, c := range cs.PeerCertificates {
			raw = append(raw, c.Raw)
		}
		if err := verifyPinned(raw, want, configNow(cfg)); err != nil {
			return err
		}
		return requireALPN(cs)
	}
	return cfg
}

// PinnedCAVerifier returns a tls.Config.VerifyPeerCertificate callback for
// the join step: the presented chain must contain a CA certificate whose
// Fingerprint equals fingerprint, and the leaf must verify against that CA
// for server authentication and be the hub control certificate (role
// HubOU). Otherwise it returns DEY-N002 with {expected} and {got}. Use it
// with InsecureSkipVerify (see JoinClientTLSConfig).
func PinnedCAVerifier(fingerprint string) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	want := normalizePin(fingerprint)
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		return verifyPinned(rawCerts, want, time.Now())
	}
}

// normalizePin returns the canonical form of a pinned fingerprint. A
// malformed pin is kept verbatim: it matches no certificate (fail closed)
// and is shown as {expected} in DEY-N002.
func normalizePin(fingerprint string) string {
	if want, ok := NormalizeFingerprint(fingerprint); ok {
		return want
	}
	return fingerprint
}

func verifyPinned(rawCerts [][]byte, want string, now time.Time) error {
	if len(rawCerts) == 0 {
		return pinErr(want, "none")
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return deyerr.Wrap(deyerr.N002, err, deyerr.Params{"expected": want, "got": "an unparsable certificate"})
		}
		certs = append(certs, c)
	}
	var pinned *x509.Certificate
	got := ""
	for _, c := range certs {
		fp := Fingerprint(c.Raw)
		if fp == want {
			pinned = c
			break
		}
		if got == "" && c.IsCA {
			got = fp
		}
	}
	if pinned == nil {
		if got == "" {
			got = Fingerprint(certs[len(certs)-1].Raw)
		}
		return pinErr(want, got)
	}
	if !pinned.IsCA || pinned == certs[0] {
		return pinErr(want, Fingerprint(pinned.Raw)).
			WithWhy("the pinned certificate is not a CA that signed the hub certificate; this may be a wrong address or interception")
	}
	roots := x509.NewCertPool()
	roots.AddCert(pinned)
	if _, err := verifyServerChain(certs, roots, "", now); err != nil {
		return deyerr.Wrap(deyerr.N002, err, deyerr.Params{"expected": want, "got": want}).
			WithWhy("the hub certificate is not signed by the pinned CA; this may be a wrong address or interception")
	}
	if !hasRole(certs[0], HubOU) {
		return pinErr(want, want).
			WithWhy("the server presented a certificate of the pinned CA that is not the hub control certificate (e.g. a tunnel certificate); this may be interception")
	}
	return nil
}

func pinErr(want, got string) *deyerr.Error {
	return deyerr.New(deyerr.N002, deyerr.Params{"expected": want, "got": got})
}

// VerifyCAPEM checks that caPEM (e.g. JoinResponse.CAPEM) contains the CA
// with the pinned fingerprint and returns it; otherwise DEY-N002.
func VerifyCAPEM(caPEM []byte, fingerprint string) (*x509.Certificate, error) {
	want := normalizePin(fingerprint)
	certs, err := parseCerts(caPEM)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.N002, err, deyerr.Params{"expected": want, "got": "no certificate"})
	}
	for _, c := range certs {
		if Fingerprint(c.Raw) == want && c.IsCA {
			return c, nil
		}
	}
	return nil, pinErr(want, Fingerprint(certs[0].Raw))
}

// PeerIdentity returns the common name and fingerprint of the verified
// client certificate of a server-side connection (the node id is the CN of
// its certificate). ok is false when the client sent no certificate or when
// the certificate is not a node certificate (role NodeOU, see SignCSR). The
// caller must still compare the fingerprint with the node's
// cert_fingerprint in config.yaml, so a removed node is refused.
func PeerIdentity(cs tls.ConnectionState) (cn, fingerprint string, ok bool) {
	if len(cs.VerifiedChains) == 0 || len(cs.VerifiedChains[0]) == 0 {
		return "", "", false
	}
	leaf := cs.VerifiedChains[0][0]
	if !hasRole(leaf, NodeOU) || leaf.Subject.CommonName == "" {
		return "", "", false
	}
	return leaf.Subject.CommonName, Fingerprint(leaf.Raw), true
}

// CertPool builds a pool from every certificate in pemBytes (DEY-T008 when
// none is found). Unlike the control-channel configs it keeps non-CA
// certificates, which crypto/x509 then trusts as themselves.
func CertPool(pemBytes []byte) (*x509.CertPool, error) {
	certs, err := parseCerts(pemBytes)
	if err != nil {
		return nil, parseErr("CA certificate", err)
	}
	pool := x509.NewCertPool()
	for _, c := range certs {
		pool.AddCert(c)
	}
	return pool, nil
}

// caPool builds the trust anchors of the control channel from the CA
// certificates in caPEM. Other certificates are left out: crypto/x509
// accepts a peer certificate that is itself in the root pool without any
// chain, so a leaf pasted into caPEM (hub.crt next to ca.crt, a node
// certificate of another hub) would be trusted as is. No CA at all is
// DEY-T008.
func caPool(caPEM []byte) (*x509.CertPool, []*x509.Certificate, error) {
	certs, err := parseCerts(caPEM)
	if err != nil {
		return nil, nil, parseErr("CA certificate", err)
	}
	pool := x509.NewCertPool()
	var cas []*x509.Certificate
	for _, c := range certs {
		if !c.IsCA {
			continue
		}
		pool.AddCert(c)
		cas = append(cas, c)
	}
	if len(cas) == 0 {
		return nil, nil, deyerr.New(deyerr.T008, deyerr.Params{"path": "CA certificate", "reason": "no CA certificate found (only server or client certificates)"})
	}
	return pool, cas, nil
}

// configNow is the verification time of cfg: cfg.Time when set (as
// crypto/tls uses it), otherwise the current time.
func configNow(cfg *tls.Config) time.Time {
	if cfg.Time != nil {
		return cfg.Time()
	}
	return time.Now()
}

// keyPair builds a tls.Certificate from a PEM chain and key, reporting a
// mismatch as DEY-T002.
func keyPair(certPEM, keyPEM []byte) (tls.Certificate, error) {
	chain, err := parseCerts(certPEM)
	if err != nil {
		return tls.Certificate{}, parseErr("certificate", err)
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return tls.Certificate{}, parseErr("private key", err)
	}
	if !KeyMatchesCert(chain[0], key) {
		return tls.Certificate{}, keyMismatchErr()
	}
	out := tls.Certificate{PrivateKey: key, Leaf: chain[0]}
	for _, c := range chain {
		out.Certificate = append(out.Certificate, c.Raw)
	}
	return out, nil
}

// verifyServerChain verifies certs[0] for server authentication against
// roots, using the rest of certs as intermediates.
func verifyServerChain(certs []*x509.Certificate, roots *x509.CertPool, dnsName string, now time.Time) ([][]*x509.Certificate, error) {
	if len(certs) == 0 {
		return nil, deyerr.Plain("tlsutil: the peer presented no certificate")
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	return certs[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inter,
		DNSName:       dnsName,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
}

// requireALPN aborts a handshake that did not negotiate "deyroute/1" (a peer
// that does not speak the control protocol, or a future incompatible one).
func requireALPN(cs tls.ConnectionState) error {
	if cs.NegotiatedProtocol != ALPN {
		return fmt.Errorf("tlsutil: peer did not negotiate ALPN %q (got %q)", ALPN, cs.NegotiatedProtocol)
	}
	return nil
}
