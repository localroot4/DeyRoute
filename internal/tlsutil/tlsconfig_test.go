package tlsutil

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

type hsResult struct {
	state tls.ConnectionState
	err   error
}

// handshake runs one TLS handshake over a loopback TCP connection and
// returns the server-side state and both sides' errors. After a successful
// handshake the client writes one byte so that the server also finishes
// verifying the client certificate (TLS 1.3 sends it after the server's
// Finished).
func handshake(t *testing.T, srvCfg, cliCfg *tls.Config) (tls.ConnectionState, error, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	ch := make(chan hsResult, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			ch <- hsResult{err: err}
			return
		}
		defer func() { _ = c.Close() }()
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		tc := tls.Server(c, srvCfg)
		err = tc.Handshake()
		if err == nil {
			buf := make([]byte, 1)
			_, err = io.ReadFull(tc, buf)
		}
		ch <- hsResult{state: tc.ConnectionState(), err: err}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d := tls.Dialer{Config: cliCfg}
	conn, cliErr := d.DialContext(ctx, "tcp", ln.Addr().String())
	if cliErr == nil {
		_, cliErr = conn.Write([]byte{1})
		_ = conn.Close()
	}
	r := <-ch
	return r.state, r.err, cliErr
}

type pki struct {
	ca                  *CA
	hubCert, hubKey     []byte
	nodeCert, nodeKey   []byte
	hubIP               net.IP
	caFingerprint       string
	otherCA             *CA
	rogueCert, rogueKey []byte // node cert from another CA
}

func newPKI(t *testing.T) pki {
	t.Helper()
	var p pki
	p.ca = newTestCA(t)
	p.hubIP = net.ParseIP("127.0.0.1")
	var err error
	p.hubCert, p.hubKey, err = p.ca.IssueServer("deyroute-hub ir-1", []net.IP{p.hubIP}, nil, HubCertValidity)
	require.NoError(t, err)
	csr, key, err := NewKeyAndCSR("de-1")
	require.NoError(t, err)
	p.nodeKey = key
	p.nodeCert, err = p.ca.SignCSR(csr, "de-1", NodeCertValidity)
	require.NoError(t, err)
	p.caFingerprint = p.ca.Fingerprint()

	p.otherCA = newTestCA(t)
	csr, key, err = NewKeyAndCSR("de-1")
	require.NoError(t, err)
	p.rogueKey = key
	p.rogueCert, err = p.otherCA.SignCSR(csr, "de-1", NodeCertValidity)
	require.NoError(t, err)
	return p
}

func TestMTLSHandshake(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	require.Equal(t, uint16(tls.VersionTLS13), srv.MinVersion)
	require.Equal(t, []string{"h2", "deyroute/1"}, srv.NextProtos, "h2 for current nodes, deyroute/1 for older ones")
	require.Equal(t, tls.VerifyClientCertIfGiven, srv.ClientAuth)
	require.True(t, srv.SessionTicketsDisabled, "every connection re-verifies the node certificate")
	require.Len(t, srv.Certificates[0].Certificate, 2, "leaf + CA for pinning")

	for _, name := range []string{"127.0.0.1", ""} {
		t.Run("serverName="+name, func(t *testing.T) {
			cli, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, name)
			require.NoError(t, err)
			require.Equal(t, uint16(tls.VersionTLS13), cli.MinVersion)
			require.NotNil(t, cli.RootCAs)
			state, srvErr, cliErr := handshake(t, srv, cli)
			require.NoError(t, cliErr)
			require.NoError(t, srvErr)
			require.Equal(t, uint16(tls.VersionTLS13), state.Version)
			require.Equal(t, ALPNH2, state.NegotiatedProtocol)
			cn, fp, ok := PeerIdentity(state)
			require.True(t, ok)
			require.Equal(t, "de-1", cn)
			leaf, err := ParseCert(p.nodeCert)
			require.NoError(t, err)
			require.Equal(t, Fingerprint(leaf.Raw), fp)
		})
	}
}

func TestMTLSWrongServerName(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	cli, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, "10.9.9.9")
	require.NoError(t, err)
	_, _, cliErr := handshake(t, srv, cli)
	require.Error(t, cliErr)
}

func TestMTLSNoClientCert(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	cli, err := ClientTLSConfig(p.ca.CertPEM, nil, nil, "")
	require.NoError(t, err)
	require.Empty(t, cli.Certificates)
	state, srvErr, cliErr := handshake(t, srv, cli)
	require.NoError(t, cliErr)
	require.NoError(t, srvErr)
	_, _, ok := PeerIdentity(state)
	require.False(t, ok, "join path: no client certificate")
}

func TestMTLSRejectsForeignClientCert(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	cli, err := ClientTLSConfig(p.ca.CertPEM, p.rogueCert, p.rogueKey, "")
	require.NoError(t, err)
	_, srvErr, _ := handshake(t, srv, cli)
	require.Error(t, srvErr)
}

func TestMTLSRejectsForeignServer(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	// The node trusts only another CA: no fallback to anything else.
	cli, err := ClientTLSConfig(p.otherCA.CertPEM, p.rogueCert, p.rogueKey, "")
	require.NoError(t, err)
	_, _, cliErr := handshake(t, srv, cli)
	require.Error(t, cliErr)

	cli, err = ClientTLSConfig(p.otherCA.CertPEM, nil, nil, "127.0.0.1")
	require.NoError(t, err)
	_, _, cliErr = handshake(t, srv, cli)
	require.Error(t, cliErr)
}

func TestNodeCertCannotImpersonateHub(t *testing.T) {
	p := newPKI(t)
	// A node certificate is valid for the CA but has only ClientAuth.
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey)
	require.NoError(t, err)
	cli, err := ClientTLSConfig(p.ca.CertPEM, nil, nil, "")
	require.NoError(t, err)
	_, _, cliErr := handshake(t, srv, cli)
	require.Error(t, cliErr)

	// The pinned join config refuses it too.
	_, _, cliErr = handshake(t, srv, JoinClientTLSConfig(p.caFingerprint))
	requireCode(t, cliErr, deyerr.N002)
}

// A tunnel certificate is signed by the same CA, is ServerAuth and carries
// the hub IP as SAN, and its key is readable by backend processes (and sent
// to nodes for node-side TLS servers). It must never pass as the hub.
func TestTunnelCertCannotImpersonateHub(t *testing.T) {
	p := newPKI(t)
	tunCert, tunKey, err := p.ca.IssueTunnel("main", []net.IP{p.hubIP}, nil, time.Now())
	require.NoError(t, err)
	srv, err := ServerTLSConfig(p.ca.CertPEM, tunCert, tunKey)
	require.NoError(t, err)
	require.Len(t, srv.Certificates[0].Certificate, 2, "the CA is appended as for the real hub")

	for _, name := range []string{"", "127.0.0.1"} {
		cli, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, name)
		require.NoError(t, err)
		_, _, cliErr := handshake(t, srv, cli)
		require.ErrorContains(t, cliErr, "not the deyroute hub control certificate", "serverName=%q", name)
	}

	_, _, cliErr := handshake(t, srv, JoinClientTLSConfig(p.caFingerprint))
	e := requireCode(t, cliErr, deyerr.N002)
	require.Contains(t, e.Why(), "not the hub control certificate")

	tun, err := ParseCert(tunCert)
	require.NoError(t, err)
	e = requireCode(t, PinnedCAVerifier(p.caFingerprint)([][]byte{tun.Raw, p.ca.Cert.Raw}, nil), deyerr.N002)
	require.Equal(t, p.caFingerprint, e.Params["expected"])
}

func TestPeerIdentityRequiresNodeRole(t *testing.T) {
	p := newPKI(t)
	// A ClientAuth certificate of our CA without the node role (not
	// something SignCSR produces, but the hub must not take it as a node).
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := randomSerial()
	require.NoError(t, err)
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "de-1", Organization: []string{Organization}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, p.ca.Cert, key.Public(), p.ca.Key)
	require.NoError(t, err)

	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	cli, err := ClientTLSConfig(p.ca.CertPEM, EncodeCertPEM(der), keyPEMOf(t, key), "")
	require.NoError(t, err)
	state, srvErr, cliErr := handshake(t, srv, cli)
	require.NoError(t, cliErr)
	require.NoError(t, srvErr)
	require.NotEmpty(t, state.VerifiedChains, "TLS accepted the certificate")
	_, _, ok := PeerIdentity(state)
	require.False(t, ok, "but it is not a node identity")

	// A verified hub certificate in the chain is no node either.
	hub, err := ParseCert(p.hubCert)
	require.NoError(t, err)
	_, _, ok = PeerIdentity(tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{hub, p.ca.Cert}}})
	require.False(t, ok)
	_, _, ok = PeerIdentity(tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{}}})
	require.False(t, ok)
}

func TestALPNAndVersionEnforced(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)

	noALPN := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true} // #nosec G402 -- test client
	_, srvErr, _ := handshake(t, srv, noALPN)
	require.ErrorContains(t, srvErr, "ALPN")

	otherALPN := &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, NextProtos: []string{"spdy/3"}} // #nosec G402 -- test client
	_, srvErr, cliErr := handshake(t, srv, otherALPN)
	require.Error(t, srvErr)
	require.Error(t, cliErr)

	tls12 := &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, InsecureSkipVerify: true, NextProtos: []string{ALPN}} // #nosec G402 -- test client
	_, srvErr, cliErr = handshake(t, srv, tls12)
	require.Error(t, srvErr)
	require.Error(t, cliErr)

	// A server that does not speak deyroute/1 is refused by the client.
	plain, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	plain.NextProtos = nil
	plain.VerifyConnection = nil
	cli, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, "127.0.0.1")
	require.NoError(t, err)
	_, _, cliErr = handshake(t, plain, cli)
	require.ErrorContains(t, cliErr, "ALPN")
}

func TestRotateCATrustsBoth(t *testing.T) {
	p := newPKI(t)
	newCA := newTestCA(t)
	hubCert, hubKey, err := newCA.IssueServer("hub", []net.IP{p.hubIP}, nil, HubCertValidity)
	require.NoError(t, err)
	both := append(append([]byte{}, p.ca.CertPEM...), newCA.CertPEM...)
	// Hub already on the new CA, node still has an old-CA certificate.
	srv, err := ServerTLSConfig(both, hubCert, hubKey)
	require.NoError(t, err)
	require.Len(t, srv.Certificates[0].Certificate, 2)
	cli, err := ClientTLSConfig(both, p.nodeCert, p.nodeKey, "")
	require.NoError(t, err)
	state, srvErr, cliErr := handshake(t, srv, cli)
	require.NoError(t, cliErr)
	require.NoError(t, srvErr)
	cn, _, ok := PeerIdentity(state)
	require.True(t, ok)
	require.Equal(t, "de-1", cn)
}

// crypto/x509 accepts a peer certificate that is itself in the root pool, so
// a leaf that ends up in caPEM (a foreign node certificate appended by
// mistake, hub.crt next to ca.crt) must not become a trust anchor.
func TestCAPoolIgnoresLeaves(t *testing.T) {
	p := newPKI(t)
	mixed := append(append([]byte{}, p.ca.CertPEM...), p.rogueCert...)

	// Why it matters: with the leaf in ClientCAs, stdlib TLS accepts the
	// foreign node certificate as is and it carries the node role.
	naive, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	naive.ClientCAs, err = CertPool(mixed)
	require.NoError(t, err)
	cli, err := ClientTLSConfig(p.ca.CertPEM, p.rogueCert, p.rogueKey, "")
	require.NoError(t, err)
	state, srvErr, cliErr := handshake(t, naive, cli)
	require.NoError(t, srvErr)
	require.NoError(t, cliErr)
	_, _, ok := PeerIdentity(state)
	require.True(t, ok, "a plain pool trusts the leaf")

	// ServerTLSConfig keeps only the CA.
	srv, err := ServerTLSConfig(mixed, p.hubCert, p.hubKey)
	require.NoError(t, err)
	_, srvErr, _ = handshake(t, srv, cli)
	require.Error(t, srvErr, "the foreign node certificate is refused")

	// Same on the node side: a hub-role leaf of another CA pasted into the
	// node's CA file is not trusted.
	otherHub, otherKey, err := p.otherCA.IssueServer("hub", []net.IP{p.hubIP}, nil, 0)
	require.NoError(t, err)
	evil, err := ServerTLSConfig(p.otherCA.CertPEM, otherHub, otherKey)
	require.NoError(t, err)
	for _, name := range []string{"", "127.0.0.1"} {
		cli, err := ClientTLSConfig(append(append([]byte{}, p.ca.CertPEM...), otherHub...), p.nodeCert, p.nodeKey, name)
		require.NoError(t, err)
		_, _, cliErr := handshake(t, evil, cli)
		require.Error(t, cliErr, "serverName=%q", name)
	}

	// No CA at all: T008 instead of an empty trust store.
	_, err = ServerTLSConfig(p.hubCert, p.hubCert, p.hubKey)
	e := requireCode(t, err, deyerr.T008)
	require.Contains(t, e.Why(), "no CA certificate")
	_, err = ClientTLSConfig(p.nodeCert, p.nodeCert, p.nodeKey, "")
	requireCode(t, err, deyerr.T008)
}

// The node verifies validity at tls.Config.Time, like crypto/tls itself, in
// every mode (with and without server name, and when joining).
func TestClientConfigsUseConfigTime(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	future := func() time.Time { return time.Now().Add(HubCertValidity + 24*time.Hour) }

	for _, name := range []string{"", "127.0.0.1"} {
		cli, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, name)
		require.NoError(t, err)
		cli.Time = future
		_, _, cliErr := handshake(t, srv, cli)
		require.Error(t, cliErr, "expired hub certificate, serverName=%q", name)
	}

	join := JoinClientTLSConfig(p.caFingerprint)
	join.Time = future
	_, _, cliErr := handshake(t, srv, join)
	requireCode(t, cliErr, deyerr.N002)
}

func TestJoinPinning(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)

	t.Run("match", func(t *testing.T) {
		cli := JoinClientTLSConfig(p.caFingerprint)
		require.Equal(t, uint16(tls.VersionTLS13), cli.MinVersion)
		require.Nil(t, cli.RootCAs)
		state, srvErr, cliErr := handshake(t, srv, cli)
		require.NoError(t, cliErr)
		require.NoError(t, srvErr)
		_, _, ok := PeerIdentity(state)
		require.False(t, ok)
	})
	t.Run("match bare hex upper case", func(t *testing.T) {
		h, err := CertSHA256Hex(p.ca.CertPEM)
		require.NoError(t, err)
		_, _, cliErr := handshake(t, srv, JoinClientTLSConfig(" SHA256:"+toUpper(h)))
		require.NoError(t, cliErr)
	})
	t.Run("mismatch", func(t *testing.T) {
		_, _, cliErr := handshake(t, srv, JoinClientTLSConfig(p.otherCA.Fingerprint()))
		e := requireCode(t, cliErr, deyerr.N002)
		require.Equal(t, p.otherCA.Fingerprint(), e.Params["expected"])
		require.Equal(t, p.caFingerprint, e.Params["got"])
		require.Contains(t, e.Why(), p.caFingerprint)
	})
	t.Run("server without CA in chain", func(t *testing.T) {
		leafOnly := srv.Clone()
		c := leafOnly.Certificates[0]
		c.Certificate = c.Certificate[:1]
		leafOnly.Certificates = []tls.Certificate{c}
		_, _, cliErr := handshake(t, leafOnly, JoinClientTLSConfig(p.caFingerprint))
		requireCode(t, cliErr, deyerr.N002)
	})
}

func toUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

func TestPinnedCAVerifierDirect(t *testing.T) {
	p := newPKI(t)
	hub, err := ParseCert(p.hubCert)
	require.NoError(t, err)
	v := PinnedCAVerifier(p.caFingerprint)

	require.NoError(t, v([][]byte{hub.Raw, p.ca.Cert.Raw}, nil))

	requireCode(t, v(nil, nil), deyerr.N002)
	requireCode(t, v([][]byte{{1, 2, 3}}, nil), deyerr.N002)

	// The CA presented alone (as leaf) is refused.
	e := requireCode(t, v([][]byte{p.ca.Cert.Raw}, nil), deyerr.N002)
	require.Contains(t, e.Why(), "not a CA that signed")

	// A leaf of another CA next to the pinned CA: signature does not verify.
	otherLeaf, _, err := p.otherCA.IssueServer("evil", []net.IP{p.hubIP}, nil, time.Hour)
	require.NoError(t, err)
	ol, err := ParseCert(otherLeaf)
	require.NoError(t, err)
	e = requireCode(t, v([][]byte{ol.Raw, p.ca.Cert.Raw}, nil), deyerr.N002)
	require.Contains(t, e.Why(), "not signed by the pinned CA")

	// Pinning a non-CA certificate (the hub leaf itself) is refused.
	vLeaf := PinnedCAVerifier(Fingerprint(hub.Raw))
	requireCode(t, vLeaf([][]byte{hub.Raw, p.ca.Cert.Raw}, nil), deyerr.N002)

	// Mismatch without any CA in the chain reports the last certificate.
	vOther := PinnedCAVerifier(p.otherCA.Fingerprint())
	e = requireCode(t, vOther([][]byte{hub.Raw}, nil), deyerr.N002)
	require.Equal(t, Fingerprint(hub.Raw), e.Params["got"])

	// Malformed pin never matches.
	requireCode(t, PinnedCAVerifier("sha256:xyz")([][]byte{hub.Raw, p.ca.Cert.Raw}, nil), deyerr.N002)

	// Expired hub certificate.
	requireCode(t, verifyPinned([][]byte{hub.Raw, p.ca.Cert.Raw}, p.caFingerprint, time.Now().Add(HubCertValidity+time.Hour)), deyerr.N002)
}

func TestVerifyCAPEM(t *testing.T) {
	p := newPKI(t)
	c, err := VerifyCAPEM(p.ca.CertPEM, p.caFingerprint)
	require.NoError(t, err)
	require.Equal(t, p.ca.Cert.Raw, c.Raw)

	_, err = VerifyCAPEM(p.otherCA.CertPEM, p.caFingerprint)
	e := requireCode(t, err, deyerr.N002)
	require.Equal(t, p.otherCA.Fingerprint(), e.Params["got"])

	_, err = VerifyCAPEM([]byte("junk"), p.caFingerprint)
	requireCode(t, err, deyerr.N002)
	_, err = VerifyCAPEM(p.ca.CertPEM, "bogus")
	requireCode(t, err, deyerr.N002)
	// A non-CA certificate with the pinned fingerprint is refused.
	_, err = VerifyCAPEM(p.hubCert, mustFP(t, p.hubCert))
	requireCode(t, err, deyerr.N002)
}

func mustFP(t *testing.T, certPEM []byte) string {
	t.Helper()
	c, err := ParseCert(certPEM)
	require.NoError(t, err)
	return Fingerprint(c.Raw)
}

func TestTLSConfigErrors(t *testing.T) {
	p := newPKI(t)
	_, err := ServerTLSConfig([]byte("junk"), p.hubCert, p.hubKey)
	requireCode(t, err, deyerr.T008)
	_, err = ServerTLSConfig(p.ca.CertPEM, []byte("junk"), p.hubKey)
	requireCode(t, err, deyerr.T008)
	_, err = ServerTLSConfig(p.ca.CertPEM, p.hubCert, []byte("junk"))
	requireCode(t, err, deyerr.T008)
	_, err = ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.nodeKey)
	e := requireCode(t, err, deyerr.T002)
	require.Equal(t, "the private key does not belong to the certificate", e.Why())

	_, err = ClientTLSConfig(nil, p.nodeCert, p.nodeKey, "")
	requireCode(t, err, deyerr.T008)
	_, err = ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.hubKey, "")
	requireCode(t, err, deyerr.T002)
	_, err = ClientTLSConfig(p.ca.CertPEM, nil, p.nodeKey, "")
	requireCode(t, err, deyerr.T008)

	pool, err := CertPool(append(append([]byte{}, p.ca.CertPEM...), p.otherCA.CertPEM...))
	require.NoError(t, err)
	require.NotNil(t, pool)
	_, err = CertPool(nil)
	requireCode(t, err, deyerr.T008)

	_, err = verifyServerChain(nil, x509.NewCertPool(), "", time.Now())
	require.Error(t, err)

	// A server certificate issued by a CA not in caPEM is served as is.
	srv, err := ServerTLSConfig(p.otherCA.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	require.Len(t, srv.Certificates[0].Certificate, 1)
}

// The node's ClientHello looks like ordinary HTTPS: ALPN h2 and a cover
// server name, never "deyroute/1" and never an empty name (QUESTIONS.md
// C.43); the hub is still verified only against the pinned CA. An older
// node offering deyroute/1 is still accepted.
func TestClientHelloLooksLikeHTTPS(t *testing.T) {
	p := newPKI(t)
	srv, err := ServerTLSConfig(p.ca.CertPEM, p.hubCert, p.hubKey)
	require.NoError(t, err)
	var hello *tls.ClientHelloInfo
	srv.GetConfigForClient = func(chi *tls.ClientHelloInfo) (*tls.Config, error) {
		hello = chi
		return nil, nil
	}
	cli, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, "")
	require.NoError(t, err)
	cs, srvErr, cliErr := handshake(t, srv, cli)
	require.NoError(t, srvErr)
	require.NoError(t, cliErr)
	require.Equal(t, ALPNH2, cs.NegotiatedProtocol)
	require.NotNil(t, hello)
	require.Equal(t, DefaultCoverSNI, hello.ServerName)
	require.Equal(t, []string{"h2"}, hello.SupportedProtos)

	SetCoverSNI(cli, "www.example.org")
	_, srvErr, cliErr = handshake(t, srv, cli)
	require.NoError(t, srvErr)
	require.NoError(t, cliErr)
	require.Equal(t, "www.example.org", hello.ServerName)

	join := JoinClientTLSConfig(p.ca.Fingerprint())
	require.Equal(t, DefaultCoverSNI, join.ServerName)
	require.Equal(t, []string{"h2"}, join.NextProtos)

	old, err := ClientTLSConfig(p.ca.CertPEM, p.nodeCert, p.nodeKey, "")
	require.NoError(t, err)
	old.NextProtos = []string{ALPN}
	cs, srvErr, cliErr = handshake(t, srv, old)
	require.NoError(t, srvErr)
	require.NoError(t, cliErr)
	require.Equal(t, ALPN, cs.NegotiatedProtocol)
}
