package tlsutil

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" // #nosec G505 -- checking the PKCS#12 localKeyId
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/pkcs12"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestPKCS12KDFVectors(t *testing.T) {
	// Vectors from golang.org/x/crypto/pkcs12 (pbkdf_test.go).
	pw, err := bmpPassword("sesame")
	require.NoError(t, err)
	key := pkcs12KDF([]byte("\xff\xff\xff\xff\xff\xff\xff\xff"), pw, 2048, kdfKeyID, 24)
	require.Equal(t, "7cd9fd3e2b3be7691a44e3bef0f9ea0fb9b897d4e325d9d1", hex.EncodeToString(key))

	key = pkcs12KDF([]byte("\xf3\x7e\x05\xb5\x18\x32\x4b\x4b"), []byte("\x00\x00"), 2048, kdfKeyID, 24)
	require.Equal(t, "00f759ff47d14dd03665d5943cb3c4a39a2555c02aed66e1", hex.EncodeToString(key))

	// Empty inputs are allowed by RFC 7292.
	require.Len(t, pkcs12KDF(nil, nil, 1, kdfIVID, 8), 8)
	require.Len(t, pkcs12KDF([]byte{1}, []byte{2}, 1, kdfMACID, 20), 20)
}

func TestAddBlockCarry(t *testing.T) {
	blk := []byte{0x00, 0xff, 0xff}
	addBlock(blk, []byte{0x00, 0x00, 0x00})
	require.Equal(t, []byte{0x01, 0x00, 0x00}, blk)
	blk = []byte{0xff, 0xff}
	addBlock(blk, []byte{0x00, 0x00})
	require.Equal(t, []byte{0x00, 0x00}, blk, "wraps mod 2^(8v)")
}

func TestBMPPassword(t *testing.T) {
	b, err := bmpPassword("abé")
	require.NoError(t, err)
	require.Equal(t, []byte{0, 'a', 0, 'b', 0, 0xe9, 0, 0}, b)
	b, err = bmpPassword("")
	require.NoError(t, err)
	require.Equal(t, []byte{0, 0}, b)
	_, err = bmpPassword("pw\U0001F600")
	require.Error(t, err)
}

func TestEncodePKCS12RoundTrip(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, []string{"tun.example.com"}, time.Now())
	require.NoError(t, err)
	pw, err := NewPKCS12Password()
	require.NoError(t, err)
	require.Len(t, pw, 32)

	p12, err := EncodePKCS12(certPEM, keyPEM, pw)
	require.NoError(t, err)

	key, cert, err := pkcs12.Decode(p12, pw)
	require.NoError(t, err)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.Equal(t, leaf.Raw, cert.Raw)
	origKey, err := ParsePrivateKey(keyPEM)
	require.NoError(t, err)
	require.True(t, origKey.(*ecdsa.PrivateKey).Equal(key))

	_, _, err = pkcs12.Decode(p12, "wrong")
	require.ErrorIs(t, err, pkcs12.ErrIncorrectPassword)

	// Fresh salts: two encodings differ.
	p12b, err := EncodePKCS12(certPEM, keyPEM, pw)
	require.NoError(t, err)
	require.NotEqual(t, p12, p12b)
}

func TestEncodePKCS12Structure(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, time.Now())
	require.NoError(t, err)
	p12, err := EncodePKCS12(certPEM, keyPEM, "secret")
	require.NoError(t, err)

	var pfx p12PFX
	rest, err := asn1.Unmarshal(p12, &pfx)
	require.NoError(t, err)
	require.Empty(t, rest)
	require.Equal(t, 3, pfx.Version)
	require.True(t, pfx.AuthSafe.ContentType.Equal(oidData))
	require.True(t, pfx.MacData.Mac.Algorithm.Algorithm.Equal(oidSHA1))
	require.Equal(t, p12Iterations, pfx.MacData.Iterations)
	require.Len(t, pfx.MacData.MacSalt, p12SaltLen)

	var octets []byte
	_, err = asn1.Unmarshal(pfx.AuthSafe.Content.Bytes, &octets)
	require.NoError(t, err)
	var safe []p12ContentInfo
	_, err = asn1.Unmarshal(octets, &safe)
	require.NoError(t, err)
	require.Len(t, safe, 2)
	require.True(t, safe[0].ContentType.Equal(oidEncryptedData), "certificates are encrypted")
	require.True(t, safe[1].ContentType.Equal(oidData), "key bag is a data content")

	var enc p12EncryptedData
	_, err = asn1.Unmarshal(safe[0].Content.Bytes, &enc)
	require.NoError(t, err)
	require.True(t, enc.EncryptedContentInfo.ContentEncryptionAlgorithm.Algorithm.Equal(oidPBEWithSHA3KeyTDESCBC))
	var params p12PBEParams
	_, err = asn1.Unmarshal(enc.EncryptedContentInfo.ContentEncryptionAlgorithm.Parameters.FullBytes, &params)
	require.NoError(t, err)
	require.Equal(t, p12Iterations, params.Iterations)

	// ToPEM exposes the bag attributes: both bags share the localKeyId.
	blocks, err := pkcs12.ToPEM(p12, "secret")
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	sum := sha1.Sum(leaf.Raw) // #nosec G401 -- identifier check
	for _, b := range blocks {
		require.Equal(t, hex.EncodeToString(sum[:]), b.Headers["localKeyId"], b.Type)
		require.Equal(t, "tunnel-main", b.Headers["friendlyName"], b.Type)
	}
}

func TestEncodePKCS12Chain(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, time.Now())
	require.NoError(t, err)
	chain := append(append([]byte{}, certPEM...), ca.CertPEM...)
	p12, err := EncodePKCS12(chain, keyPEM, "pw")
	require.NoError(t, err)
	blocks, err := pkcs12.ToPEM(p12, "pw")
	require.NoError(t, err)
	var certs [][]byte
	for _, b := range blocks {
		if b.Type == "CERTIFICATE" {
			certs = append(certs, b.Bytes)
		}
	}
	require.Len(t, certs, 2)
	leaf, err := ParseCert(certPEM)
	require.NoError(t, err)
	require.True(t, bytes.Equal(leaf.Raw, certs[0]))
	require.True(t, bytes.Equal(ca.Cert.Raw, certs[1]))
}

func TestEncodePKCS12EmptyPasswordAndRSA(t *testing.T) {
	root := makeCert(t, rootTpl("root", t0, t0.Add(time.Hour)), nil)
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tpl := leafTpl("rsa", t0, t0.Add(time.Hour))
	tpl.SerialNumber = nil
	der, err := x509.CreateCertificate(rand.Reader, withSerial(tpl), root.cert, rk.Public(), root.key)
	require.NoError(t, err)
	certPEM := EncodeCertPEM(der)
	p12, err := EncodePKCS12(certPEM, keyPEMOf(t, rk), "")
	require.NoError(t, err)
	key, cert, err := pkcs12.Decode(p12, "")
	require.NoError(t, err)
	require.Equal(t, der, cert.Raw)
	require.True(t, rk.Equal(key))
}

func withSerial(tpl *x509.Certificate) *x509.Certificate {
	s, err := randomSerial()
	if err == nil {
		tpl.SerialNumber = s
	}
	return tpl
}

func TestEncodePKCS12Errors(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := ca.IssueTunnel("main", []net.IP{net.ParseIP("5.6.7.8")}, nil, time.Now())
	require.NoError(t, err)
	_, otherKey, err := ca.IssueTunnel("other", []net.IP{net.ParseIP("5.6.7.8")}, nil, time.Now())
	require.NoError(t, err)

	_, err = EncodePKCS12([]byte("junk"), keyPEM, "pw")
	requireCode(t, err, deyerr.T008)
	_, err = EncodePKCS12(certPEM, []byte("junk"), "pw")
	requireCode(t, err, deyerr.T008)
	_, err = EncodePKCS12(certPEM, otherKey, "pw")
	requireCode(t, err, deyerr.T002)
	_, err = EncodePKCS12(certPEM, keyPEM, "\U0001F600")
	requireCode(t, err, deyerr.X000)
}
