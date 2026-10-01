package install

import (
	"bytes"
	"strings"
	"testing"

	"aead.dev/minisign"
	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMinisignPublicKeyParses(t *testing.T) {
	var pk minisign.PublicKey
	require.NoError(t, pk.UnmarshalText([]byte(MinisignPublicKey)))
	require.Equal(t, MinisignPublicKey, pk.String())
}

func TestVerifyMinisign(t *testing.T) {
	k := newTestKey(t)
	msg := []byte("abc  deyroute_1.0.0_linux_amd64.tar.gz\n")

	require.NoError(t, VerifyMinisign(msg, k.signLegacy(msg), k.pubString()))
	require.NoError(t, VerifyMinisign(msg, k.signHashed(t, msg), k.pubString()))
	// The two-line minisign.pub form is accepted too.
	full, err := k.pub.MarshalText()
	require.NoError(t, err)
	require.NoError(t, VerifyMinisign(msg, k.signLegacy(msg), string(full)))

	tampered := append([]byte{}, msg...)
	tampered[0] = 'x'
	requireCode(t, VerifyMinisign(tampered, k.signLegacy(msg), k.pubString()), deyerr.S001)
	requireCode(t, VerifyMinisign(tampered, k.signHashed(t, msg), k.pubString()), deyerr.S001)

	// A modified trusted comment breaks the global signature.
	sig := k.signLegacy(msg)
	badComment := bytes.Replace(sig, []byte("timestamp:1"), []byte("timestamp:9"), 1)
	requireCode(t, VerifyMinisign(msg, badComment, k.pubString()), deyerr.S001)

	// Another key (e.g. the placeholder release key) → key id mismatch.
	de := requireCode(t, VerifyMinisign(msg, sig, MinisignPublicKey), deyerr.S001)
	require.Contains(t, de.Why(), "not with the DEYROUTE release key")

	// A signature by another key is rejected.
	other := newTestKey(t)
	forged := minisign.SignWithComments(other.priv, msg, "timestamp:1", "x")
	requireCode(t, VerifyMinisign(msg, forged, k.pubString()), deyerr.S001)
	// Even when the forger copies the release key id into the signature.
	var s minisign.Signature
	require.NoError(t, s.UnmarshalText(forged))
	s.KeyID = k.pub.ID()
	forged, err = s.MarshalText()
	require.NoError(t, err)
	de = requireCode(t, VerifyMinisign(msg, forged, k.pubString()), deyerr.S001)
	require.Equal(t, "the file does not match the signed manifest; it was rejected and nothing changed", de.Why())

	requireCode(t, VerifyMinisign(msg, []byte("not a signature"), k.pubString()), deyerr.S001)
	de = requireCode(t, VerifyMinisign(msg, sig, "RWQnotakey"), deyerr.S001)
	require.Contains(t, de.Why(), "malformed")

	de = requireCode(t, VerifyMinisignAs(deyerr.I006, SumsFile, tampered, sig, k.pubString()), deyerr.I006)
	require.Equal(t, "Signature invalid for SHA256SUMS", de.Message())
}

func TestParseSHA256SUMS(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("B", 64)
	sums, err := ParseSHA256SUMS([]byte("# comment\n" + a + "  deyroute_1.0.0_linux_amd64.tar.gz\r\n\n" + b + " *./install.sh\n"))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"deyroute_1.0.0_linux_amd64.tar.gz": a,
		"install.sh":                        strings.ToLower(b),
	}, sums)

	_, err = ParseSHA256SUMS([]byte("xyz file\n"))
	requireCode(t, err, deyerr.S001)
	_, err = ParseSHA256SUMS([]byte(a + "  f\n" + b + "  f\n"))
	requireCode(t, err, deyerr.S001)
	_, err = ParseSHA256SUMS(nil)
	requireCode(t, err, deyerr.S001)
	// The same line twice is harmless.
	_, err = ParseSHA256SUMS([]byte(a + "  f\n" + a + "  f\n"))
	require.NoError(t, err)
}

func TestReleaseFromSums(t *testing.T) {
	sums := map[string]string{
		"deyroute_1.0.0_linux_amd64.tar.gz":       "s1",
		"deyroute_1.2.0_linux_amd64.tar.gz":       "s2",
		"deyroute_1.10.0-rc.1_linux_amd64.tar.gz": "s3",
		"deyroute_1.2.0_linux_arm64.tar.gz":       "s4",
		"install.sh":                              "s5",
		"backends.yaml":                           "s6",
	}
	ver, file, sum, err := ReleaseFromSums(sums, "amd64", "")
	require.NoError(t, err)
	require.Equal(t, "1.10.0-rc.1", ver)
	require.Equal(t, "deyroute_1.10.0-rc.1_linux_amd64.tar.gz", file)
	require.Equal(t, "s3", sum)

	ver, file, sum, err = ReleaseFromSums(sums, "arm64", "v1.2.0")
	require.NoError(t, err)
	require.Equal(t, "1.2.0", ver)
	require.Equal(t, "deyroute_1.2.0_linux_arm64.tar.gz", file)
	require.Equal(t, "s4", sum)

	ver, _, _, err = ReleaseFromSums(sums, "amd64", "latest")
	require.NoError(t, err)
	require.Equal(t, "1.10.0-rc.1", ver)

	_, _, _, err = ReleaseFromSums(sums, "arm64", "1.0.0")
	de := requireCode(t, err, deyerr.I004)
	require.Contains(t, de.Message(), "deyroute_1.0.0_linux_arm64.tar.gz")
	_, _, _, err = ReleaseFromSums(sums, "riscv64", "")
	requireCode(t, err, deyerr.I004)
}
