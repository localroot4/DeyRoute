package wireguard

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strconv"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// GenerateKeys returns the per-tunnel key material (pure Go, crypto/rand):
// two X25519 key pairs, and for awg the randomized obfuscation parameters.
// The key names are documented at KeyHubPrivate and KeyJc.
func (b *Backend) GenerateKeys(backend.Transport) (map[string]string, error) {
	r := b.rand
	if r == nil {
		r = rand.Reader
	}
	fail := func(err error) error {
		return deyerr.Wrap(deyerr.B009, err, deyerr.Params{"backend": b.name})
	}
	out := map[string]string{}
	for _, pair := range [][2]string{{KeyHubPrivate, KeyHubPublic}, {KeyNodePrivate, KeyNodePublic}} {
		priv, pub, err := newKeyPair(r)
		if err != nil {
			return nil, fail(err)
		}
		out[pair[0]], out[pair[1]] = priv, pub
	}
	if !b.awg {
		return out, nil
	}
	a, err := newAWGParams(r)
	if err != nil {
		return nil, fail(err)
	}
	for k, v := range map[string]int64{
		KeyJc: int64(a.Jc), KeyJmin: int64(a.Jmin), KeyJmax: int64(a.Jmax), KeyS1: int64(a.S1), KeyS2: int64(a.S2),
		KeyH1: int64(a.H1), KeyH2: int64(a.H2), KeyH3: int64(a.H3), KeyH4: int64(a.H4),
	} {
		out[k] = strconv.FormatInt(v, 10)
	}
	return out, nil
}

// newKeyPair returns a clamped X25519 private key and its public key, both
// standard base64 (the format of `wg genkey | wg pubkey`).
func newKeyPair(r io.Reader) (priv, pub string, err error) {
	var k [32]byte
	if _, err := io.ReadFull(r, k[:]); err != nil {
		return "", "", err
	}
	// Curve25519 clamping, as `wg genkey` does (RFC 7748 section 5; the
	// public key is the same with or without it).
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	sk, err := ecdh.X25519().NewPrivateKey(k[:])
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k[:]), base64.StdEncoding.EncodeToString(sk.PublicKey().Bytes()), nil
}

// publicMatches reports whether pub is the X25519 public key of priv.
func publicMatches(priv, pub string) bool {
	k, err := decodeKey(priv)
	if err != nil {
		return false
	}
	sk, err := ecdh.X25519().NewPrivateKey(k[:])
	if err != nil {
		return false
	}
	return base64.StdEncoding.EncodeToString(sk.PublicKey().Bytes()) == pub
}

// newAWGParams draws AmneziaWG parameters within the limits of check().
func newAWGParams(r io.Reader) (*AWGParams, error) {
	a := &AWGParams{}
	var err error
	pick := func(lo, hi int) int {
		if err != nil {
			return lo
		}
		var v int
		v, err = randRange(r, lo, hi)
		return v
	}
	a.Jc = pick(awgJcMin, awgJcMax)
	a.Jmin = pick(awgJminLow, awgJminHigh)
	a.Jmax = pick(a.Jmin+1, awgJmaxHigh)
	a.S1 = pick(awgSLow, awgSHigh)
	for i := 0; i < 64 && err == nil; i++ {
		a.S2 = pick(awgSLow, awgSHigh)
		if a.S1+awgSizeDelta != a.S2 {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	var hs [4]uint32
	for i := range hs {
		for try := 0; ; try++ {
			if try == maxDraws {
				return nil, errString("entropy source returned too many unusable values")
			}
			var buf [4]byte
			if _, err := io.ReadFull(r, buf[:]); err != nil {
				return nil, err
			}
			h := binary.BigEndian.Uint32(buf[:])
			if h < awgHMin || contains(hs[:i], h) {
				continue
			}
			hs[i] = h
			break
		}
	}
	a.H1, a.H2, a.H3, a.H4 = hs[0], hs[1], hs[2], hs[3]
	if err := a.check(); err != nil {
		return nil, err
	}
	return a, nil
}

func contains(hs []uint32, h uint32) bool {
	for _, x := range hs {
		if x == h {
			return true
		}
	}
	return false
}

// randRange returns a uniform integer in [lo, hi] (rejection sampling).
func randRange(r io.Reader, lo, hi int) (int, error) {
	span := uint32(hi - lo + 1) // #nosec G115 -- small positive constants
	limit := (1<<32 - 1) - (1<<32-1)%span
	for try := 0; try < maxDraws; try++ {
		var buf [4]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		v := binary.BigEndian.Uint32(buf[:])
		if v < limit {
			return lo + int(v%span), nil
		}
	}
	return 0, errString("entropy source returned too many unusable values")
}

// maxDraws bounds rejection sampling (a working source needs ~1 draw).
const maxDraws = 1000
