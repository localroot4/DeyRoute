package install

import (
	"context"
	"fmt"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Backend entries a node installs (DR-03). A node installs a backend only
// when a signed DEYROUTE release pins it: the entry the hub sends must be
// the entry of that backend in the backends.yaml built into the node, or in
// a backends.yaml the hub sends along whose signature verifies with the
// release key built into the node. The hub's word alone is never enough.

// Where CheckBackendEntry found the entry.
const (
	PinnedBuiltIn = "the node's built-in backends.yaml"
	PinnedSigned  = "the signed backends.yaml the hub sent"
)

// CheckBackendEntry reports whether e may be installed for arch. It must
// equal, in version, URL of arch and sha256 of arch, the entry of the same
// name in embedded (the node's built-in backends.yaml) or in manifest, when
// sig is a valid signature of manifest by pubKey ("" = MinisignPublicKey,
// which also accepts TestPublicKey, see VerifyMinisignAs). The rest of the
// entry (archive kind, binaries, template version) does not matter: the
// downloaded file must have the pinned sha256, so it is the pinned file.
// It returns where the entry was found (PinnedBuiltIn or PinnedSigned), or
// an error that says why it is not pinned.
func CheckBackendEntry(e backend.ManifestEntry, arch string, embedded, manifest, sig []byte, pubKey string) (string, error) {
	what := fmt.Sprintf("backend %s %s for %s", e.Name, e.Version, arch)
	if m, err := backend.ParseManifest(embedded); err == nil && samePin(m, e, arch) {
		return PinnedBuiltIn, nil
	}
	if len(manifest) == 0 {
		return "", fmt.Errorf("%s is not pinned by this node's built-in backends.yaml, and the hub sent no signed backends.yaml", what)
	}
	if strings.TrimSpace(pubKey) == "" {
		pubKey = MinisignPublicKey
	}
	if err := VerifyMinisignAs(deyerr.S001, ManifestFile, manifest, sig, pubKey); err != nil {
		why := deyerr.As(err).WhyOverride
		if why == "" {
			why = "the signature does not match the file"
		}
		return "", fmt.Errorf("%s is not pinned by this node's built-in backends.yaml, and the backends.yaml the hub sent is not signed with the DEYROUTE release key: %s", what, why)
	}
	m, err := backend.ParseSignedManifest(manifest)
	if err != nil {
		return "", fmt.Errorf("%s is not pinned by this node's built-in backends.yaml, and the signed backends.yaml the hub sent cannot be read: %v", what, err)
	}
	if !samePin(m, e, arch) {
		return "", fmt.Errorf("%s is pinned neither by this node's built-in backends.yaml nor by the signed backends.yaml the hub sent (version, URL and sha256 must match)", what)
	}
	return PinnedSigned, nil
}

// samePin reports whether the block of m that pins e.Name
// (backend.ManifestBlock: awg installs the "wireguard" block) has e's
// version and e's URL and sha256 for arch.
func samePin(m *backend.ManifestFile, e backend.ManifestEntry, arch string) bool {
	p, ok := m.Backends[backend.ManifestBlock(e.Name)]
	if !ok || e.Name == "" || p.Version != e.Version || p.URLs[arch] != e.URLs[arch] {
		return false
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return norm(p.SHA256[arch]) == norm(e.SHA256[arch])
}

// FetchManifestSig downloads backends.yaml.minisig of each version in
// versions ("" = latest) from each source in turn and returns the first
// one that is a valid signature of data by pubKey ("" = MinisignPublicKey).
// A hub uses it for a manifest override saved before it kept the signature
// (DR-03): any release whose backends.yaml has the same bytes signs it.
// No valid signature → DEY-S001; nothing downloaded → DEY-I004.
func FetchManifestSig(ctx context.Context, f Fetcher, sources []Source, versions []string, data []byte, pubKey string, ro RetryOptions) ([]byte, error) {
	if strings.TrimSpace(pubKey) == "" {
		pubKey = MinisignPublicKey
	}
	var lastErr, sigErr error
	for _, v := range versions {
		for _, src := range sources {
			r := ro
			r.File = ManifestSigFile
			sig, _, err := FetchBytes(ctx, f, []string{src.ManifestSig(v)}, r)
			if err != nil {
				lastErr = err
				continue
			}
			if err := VerifyMinisignAs(deyerr.S001, ManifestFile, data, sig, pubKey); err != nil {
				sigErr = err
				continue
			}
			return sig, nil
		}
	}
	return nil, firstErr(sigErr, lastErr,
		deyerr.New(deyerr.I004, deyerr.Params{"file": ManifestSigFile}).WithDetail("no download source configured"))
}
