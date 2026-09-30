package install

// MinisignPublicKey is the DEYROUTE release signing key (minisign format, the
// base64 line of minisign.pub). SHA256SUMS and backends.yaml of every release
// are signed with the matching secret key; self-update, `deyroute update
// manifest` and installer/install.sh (variable MINISIGN_PUBKEY, which must hold
// the same value — a test enforces it) refuse files whose signature does not
// verify against it (spec section 11).
//
// The matching secret key lives only in the repository secret
// MINISIGN_SECRET_KEY, used by .github/workflows/release-edge.yml and ci.yml.
// To rotate: go run ./scripts/minisign keygen, update this constant and
// MINISIGN_PUBKEY in installer/install.sh, and replace the secret.
const MinisignPublicKey = "RWQtE2Hu1KwstgupEuPAWy+J2tiIgFVTIcReWI+Qh+yGMY3s2xWpyMuO"
