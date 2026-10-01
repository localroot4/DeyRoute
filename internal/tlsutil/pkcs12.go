package tlsutil

import (
	"crypto/cipher"
	"crypto/des" // #nosec G502 -- PKCS#12 3DES PBE is the format rathole/native-tls reads by default (QUESTIONS.md C.11)
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- PKCS#12 KDF/MAC and localKeyId are defined over SHA-1 (RFC 7292)
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"unicode/utf16"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
)

// PKCS#12 parameters (RFC 7292). SHA-1/3DES PBE with a SHA-1 HMAC is what
// OpenSSL (and therefore rathole's native-tls) reads without the legacy
// provider; RC2 is avoided on purpose.
const (
	p12Iterations = 2048
	p12SaltLen    = 8
	sha1Size      = 20 // u in RFC 7292 B.2
	sha1Block     = 64 // v in RFC 7292 B.2
	kdfKeyID      = 1
	kdfIVID       = 2
	kdfMACID      = 3
	desKeyLen     = 24
	desIVLen      = 8
)

var (
	oidData                  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidEncryptedData         = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 6}
	oidPBEWithSHA3KeyTDESCBC = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 1, 3}
	oidShroudedKeyBag        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 2}
	oidCertBag               = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 12, 10, 1, 3}
	oidX509Certificate       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 22, 1}
	oidFriendlyName          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 20}
	oidLocalKeyID            = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 21}
	oidSHA1                  = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
)

// ASN.1 structures of RFC 7292. Explicitly tagged ANY fields are built as
// asn1.RawValue because encoding/asn1 ignores struct tags on RawValue.
type p12PFX struct {
	Version  int
	AuthSafe p12ContentInfo
	MacData  p12MacData
}

type p12ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue // [0] EXPLICIT
}

type p12EncryptedData struct {
	Version              int
	EncryptedContentInfo p12EncryptedContentInfo
}

type p12EncryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedContent           []byte `asn1:"tag:0,optional"` // [0] IMPLICIT OCTET STRING
}

type p12SafeBag struct {
	ID         asn1.ObjectIdentifier
	Value      asn1.RawValue     // [0] EXPLICIT
	Attributes []p12BagAttribute `asn1:"set,optional"`
}

type p12BagAttribute struct {
	ID    asn1.ObjectIdentifier
	Value asn1.RawValue // SET OF
}

type p12CertBag struct {
	ID   asn1.ObjectIdentifier
	Data []byte `asn1:"tag:0,explicit"`
}

type p12EncryptedPrivateKeyInfo struct {
	Algorithm     pkix.AlgorithmIdentifier
	EncryptedData []byte
}

type p12PBEParams struct {
	Salt       []byte
	Iterations int
}

type p12MacData struct {
	Mac        p12DigestInfo
	MacSalt    []byte
	Iterations int
}

type p12DigestInfo struct {
	Algorithm pkix.AlgorithmIdentifier
	Digest    []byte
}

// NewPKCS12Password returns a random password for a .p12 bundle
// (backend.Secrets.TLSP12Password): 24 random bytes, base64url.
// The password is registered with log.RegisterSecret: it is written into
// rendered backend configs (rathole pkcs12_password) and must never appear
// in logs.
func NewPKCS12Password() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", internalErr("generate PKCS#12 password", err)
	}
	pw := base64.RawURLEncoding.EncodeToString(b)
	deylog.RegisterSecret(pw)
	return pw, nil
}

// EncodePKCS12 builds a password-protected PKCS#12 (PFX v3) bundle from a
// PEM certificate chain (leaf first) and its PEM private key, for
// rathole/tls (section 7.2):
//
//   - an encrypted-data ContentInfo holding one CertBag per certificate,
//   - a data ContentInfo holding the pkcs8ShroudedKeyBag,
//
// both encrypted with pbeWithSHAAnd3-KeyTripleDES-CBC (2048 iterations);
// the leaf and key bags share a localKeyId (SHA-1 of the leaf); the whole
// AuthenticatedSafe is protected by an HMAC-SHA1 (PKCS#12 KDF, ID=3, 2048
// iterations). The password must be BMP-encodable (no emoji and the like);
// it is registered with log.RegisterSecret.
func EncodePKCS12(certPEM, keyPEM []byte, password string) ([]byte, error) {
	deylog.RegisterSecret(password)
	chain, err := parseCerts(certPEM)
	if err != nil {
		return nil, parseErr("certificate", err)
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return nil, parseErr("private key", err)
	}
	if !KeyMatchesCert(chain[0], key) {
		return nil, keyMismatchErr()
	}
	pw, err := bmpPassword(password)
	if err != nil {
		return nil, internalErr("encode PKCS#12 password", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, internalErr("encode private key", err)
	}
	localKeyID := sha1.Sum(chain[0].Raw) // #nosec G401 -- identifier only, as OpenSSL does

	// (1) certificates, encrypted.
	certBags := make([]p12SafeBag, 0, len(chain))
	for i, c := range chain {
		bagDER, err := asn1.Marshal(p12CertBag{ID: oidX509Certificate, Data: c.Raw})
		if err != nil {
			return nil, internalErr("encode PKCS#12 cert bag", err)
		}
		bag := p12SafeBag{ID: oidCertBag, Value: explicit0(bagDER)}
		if i == 0 {
			if bag.Attributes, err = bagAttributes(localKeyID[:], chain[0].Subject.CommonName); err != nil {
				return nil, err
			}
		}
		certBags = append(certBags, bag)
	}
	certSafe, err := asn1.Marshal(certBags)
	if err != nil {
		return nil, internalErr("encode PKCS#12 certificates", err)
	}
	certAlg, certEnc, err := pbeEncrypt(certSafe, pw)
	if err != nil {
		return nil, err
	}
	encDataDER, err := asn1.Marshal(p12EncryptedData{
		Version: 0,
		EncryptedContentInfo: p12EncryptedContentInfo{
			ContentType:                oidData,
			ContentEncryptionAlgorithm: certAlg,
			EncryptedContent:           certEnc,
		},
	})
	if err != nil {
		return nil, internalErr("encode PKCS#12 encrypted data", err)
	}

	// (2) the private key, shrouded.
	keyAlg, keyEnc, err := pbeEncrypt(pkcs8, pw)
	if err != nil {
		return nil, err
	}
	epkiDER, err := asn1.Marshal(p12EncryptedPrivateKeyInfo{Algorithm: keyAlg, EncryptedData: keyEnc})
	if err != nil {
		return nil, internalErr("encode PKCS#12 key", err)
	}
	keyBag := p12SafeBag{ID: oidShroudedKeyBag, Value: explicit0(epkiDER)}
	if keyBag.Attributes, err = bagAttributes(localKeyID[:], chain[0].Subject.CommonName); err != nil {
		return nil, err
	}
	keySafe, err := asn1.Marshal([]p12SafeBag{keyBag})
	if err != nil {
		return nil, internalErr("encode PKCS#12 key bag", err)
	}
	keySafeOctets, err := asn1.Marshal(keySafe)
	if err != nil {
		return nil, internalErr("encode PKCS#12 key bag", err)
	}

	authSafe, err := asn1.Marshal([]p12ContentInfo{
		{ContentType: oidEncryptedData, Content: explicit0(encDataDER)},
		{ContentType: oidData, Content: explicit0(keySafeOctets)},
	})
	if err != nil {
		return nil, internalErr("encode PKCS#12 authenticated safe", err)
	}
	authSafeOctets, err := asn1.Marshal(authSafe)
	if err != nil {
		return nil, internalErr("encode PKCS#12 authenticated safe", err)
	}

	macSalt, err := randomBytes(p12SaltLen)
	if err != nil {
		return nil, err
	}
	macKey := pkcs12KDF(macSalt, pw, p12Iterations, kdfMACID, sha1Size)
	mac := hmac.New(sha1.New, macKey)
	_, _ = mac.Write(authSafe)

	out, err := asn1.Marshal(p12PFX{
		Version:  3,
		AuthSafe: p12ContentInfo{ContentType: oidData, Content: explicit0(authSafeOctets)},
		MacData: p12MacData{
			Mac: p12DigestInfo{
				Algorithm: pkix.AlgorithmIdentifier{Algorithm: oidSHA1, Parameters: asn1.NullRawValue},
				Digest:    mac.Sum(nil),
			},
			MacSalt:    macSalt,
			Iterations: p12Iterations,
		},
	})
	if err != nil {
		return nil, internalErr("encode PKCS#12", err)
	}
	return out, nil
}

// explicit0 wraps DER in a context-specific constructed [0] tag.
func explicit0(der []byte) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: der}
}

// bagAttributes returns the localKeyId and (when name is set) friendlyName
// attributes shared by the leaf certificate bag and the key bag.
func bagAttributes(localKeyID []byte, name string) ([]p12BagAttribute, error) {
	idDER, err := asn1.Marshal(localKeyID)
	if err != nil {
		return nil, internalErr("encode PKCS#12 localKeyId", err)
	}
	attrs := []p12BagAttribute{{ID: oidLocalKeyID, Value: setOf(idDER)}}
	if name != "" {
		bmp, err := bmpString(name)
		if err == nil {
			nameDER, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagBMPString, Bytes: bmp})
			if err != nil {
				return nil, internalErr("encode PKCS#12 friendlyName", err)
			}
			attrs = append(attrs, p12BagAttribute{ID: oidFriendlyName, Value: setOf(nameDER)})
		}
	}
	return attrs, nil
}

func setOf(der []byte) asn1.RawValue {
	return asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: der}
}

// pbeEncrypt encrypts data with pbeWithSHAAnd3-KeyTripleDES-CBC and a fresh
// salt, returning the AlgorithmIdentifier (with PBE parameters) and the
// ciphertext (PKCS#7 padded).
func pbeEncrypt(data, password []byte) (pkix.AlgorithmIdentifier, []byte, error) {
	salt, err := randomBytes(p12SaltLen)
	if err != nil {
		return pkix.AlgorithmIdentifier{}, nil, err
	}
	params, err := asn1.Marshal(p12PBEParams{Salt: salt, Iterations: p12Iterations})
	if err != nil {
		return pkix.AlgorithmIdentifier{}, nil, internalErr("encode PBE parameters", err)
	}
	key := pkcs12KDF(salt, password, p12Iterations, kdfKeyID, desKeyLen)
	iv := pkcs12KDF(salt, password, p12Iterations, kdfIVID, desIVLen)
	block, err := des.NewTripleDESCipher(key) // #nosec G401 G405 -- see the package-level PKCS#12 note
	if err != nil {
		return pkix.AlgorithmIdentifier{}, nil, internalErr("create 3DES cipher", err)
	}
	bs := block.BlockSize()
	pad := bs - len(data)%bs
	buf := make([]byte, len(data)+pad)
	copy(buf, data)
	for i := len(data); i < len(buf); i++ {
		buf[i] = byte(pad)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(buf, buf)
	return pkix.AlgorithmIdentifier{Algorithm: oidPBEWithSHA3KeyTDESCBC, Parameters: asn1.RawValue{FullBytes: params}}, buf, nil
}

// pkcs12KDF is the key derivation of RFC 7292 appendix B.2 with SHA-1
// (u = 20, v = 64 bytes). id selects key (1), IV (2) or MAC key (3).
func pkcs12KDF(salt, password []byte, iterations int, id byte, size int) []byte {
	const u, v = sha1Size, sha1Block
	// 1. D = v copies of ID.
	d := make([]byte, v)
	for i := range d {
		d[i] = id
	}
	// 2.-4. I = S || P, each the input repeated to a multiple of v bytes.
	s := repeatToBlocks(salt, v)
	p := repeatToBlocks(password, v)
	i := append(s, p...)
	// 5. c = ceil(n/u).
	c := (size + u - 1) / u
	out := make([]byte, 0, c*u)
	for n := 0; n < c; n++ {
		// 6A. A = H^r(D || I).
		h := sha1.New() // #nosec G401 -- RFC 7292 KDF
		_, _ = h.Write(d)
		_, _ = h.Write(i)
		a := h.Sum(nil)
		for r := 1; r < iterations; r++ {
			sum := sha1.Sum(a) // #nosec G401 -- RFC 7292 KDF
			a = sum[:]
		}
		out = append(out, a...)
		if n == c-1 {
			break
		}
		// 6B. B = A repeated to v bytes.
		b := repeatToBlocks(a, v)[:v]
		// 6C. I_j = (I_j + B + 1) mod 2^(8v) for each v-byte block.
		for j := 0; j < len(i); j += v {
			addBlock(i[j:j+v], b)
		}
	}
	return out[:size]
}

// repeatToBlocks concatenates copies of in to v*ceil(len(in)/v) bytes (the
// last copy truncated). Empty input yields an empty result.
func repeatToBlocks(in []byte, v int) []byte {
	if len(in) == 0 {
		return nil
	}
	n := v * ((len(in) + v - 1) / v)
	out := make([]byte, n)
	for k := 0; k < n; k += len(in) {
		copy(out[k:], in)
	}
	return out
}

// addBlock sets blk = (blk + b + 1) mod 2^(8*len(blk)), big-endian.
func addBlock(blk, b []byte) {
	carry := 1
	for k := len(blk) - 1; k >= 0; k-- {
		sum := int(blk[k]) + int(b[k]) + carry
		blk[k] = byte(sum)
		carry = sum >> 8
	}
}

// bmpPassword encodes a password as a NUL-terminated BMPString (UTF-16BE),
// RFC 7292 appendix B.1.
func bmpPassword(s string) ([]byte, error) {
	b, err := bmpString(s)
	if err != nil {
		return nil, err
	}
	return append(b, 0, 0), nil
}

// bmpString encodes s as UTF-16BE, rejecting characters outside the BMP.
func bmpString(s string) ([]byte, error) {
	out := make([]byte, 0, 2*len(s))
	for _, r := range s {
		if r1, _ := utf16.EncodeRune(r); r1 != 0xfffd || r > 0xffff {
			return nil, deyerr.Plain("the password contains characters outside the Basic Multilingual Plane")
		}
		out = append(out, byte(r>>8), byte(r))
	}
	return out, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, internalErr("read random bytes", err)
	}
	return b, nil
}
