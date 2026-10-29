package install

import "strings"

// MinisignPublicKey is the DEYROUTE release signing key (minisign format, the
// base64 line of minisign.pub). SHA256SUMS and backends.yaml of every release
// are signed with the matching secret key; self-update, `deyroute update
// manifest` and installer/install.sh (variable MINISIGN_PUBKEY, which must hold
// the same value — a test enforces it) refuse files whose signature does not
// verify against it (spec section 11).
//
// The matching secret key lives only in the secret MINISIGN_SECRET_KEY of the
// GitHub environments edge-release and release, used by
// .github/workflows/release-edge.yml and ci.yml (docs/dev/RELEASING.md).
// release-edge.yml reads this constant with scripts/release-pubkey.sh, which
// takes only the line below: keep it one line of the form
// const MinisignPublicKey = "<key>".
// To rotate: go run ./scripts/minisign keygen, update this constant and
// MINISIGN_PUBKEY in installer/install.sh, and replace the secret.
const MinisignPublicKey = "RWQtE2Hu1KwstgupEuPAWy+J2tiIgFVTIcReWI+Qh+yGMY3s2xWpyMuO"

// TestPublicKey is a second key that signatures checked against
// MinisignPublicKey may come from, for the integration lab only: the lab
// signs its own release with a throwaway key. It is empty in every release
// build and is set only at link time, never in code:
//
//	go build -ldflags "-X github.com/localroot4/deyroute/internal/install.TestPublicKey=<lab public key>" ./cmd/deyroute
//
// `deyroute --json version` reports "test_key": true while it is set (CI
// refuses a release binary that does), and VerifyMinisignAs accepts it only
// where the release key is expected, never in place of another key.
var TestPublicKey string

// HasTestKey reports whether this binary was built with a TestPublicKey.
func HasTestKey() bool { return strings.TrimSpace(TestPublicKey) != "" }
