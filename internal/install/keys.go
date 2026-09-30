package install

// MinisignPublicKey is the DEYROUTE release signing key (minisign format, the
// base64 line of minisign.pub). SHA256SUMS and backends.yaml of every release
// are signed with the matching secret key; self-update, `deyroute update
// manifest` and installer/install.sh (variable MINISIGN_PUBKEY, which must hold
// the same value — a test enforces it) refuse files whose signature does not
// verify against it (spec section 11).
//
// PLACEHOLDER: this key was generated for development and its secret key was
// discarded, so nothing can be signed with it. The owner MUST replace it (here
// and in installer/install.sh) with the public key of the real release key
// before the first release; see QUESTIONS.md.
const MinisignPublicKey = "RWTv3LWjY2Y1kBNezZhjxAw4SzGU5K8tkH8D3Rq76L2iMddh+P0cOvJS"
