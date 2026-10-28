package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/version"
)

// Signed manifests for backend.install (DR-03). A node that announced
// api.FeatureSignedUpdate installs a backend only when a signed manifest
// pins the entry: the backends.yaml built into it, or one the hub sends
// along with its signature (api.BackendInstallArgs.Manifest). The hub
// sends, to such nodes only:
//
//   - its manifest override (/etc/deyroute/backends.yaml, written by
//     `deyroute update manifest`) with its signature
//     (/etc/deyroute/backends.yaml.minisig). An override an earlier build
//     saved without the signature is not sent until the hub has a
//     signature that verifies the bytes on disk: it downloads one when it
//     starts and then every ManifestRetry (manifestSigLoop), and its log
//     says that `deyroute update manifest` fixes it;
//   - without an override, when the node's built-in backends.yaml differs
//     from the hub's (Hello.ManifestSHA256, another build), the signed
//     backends.yaml of the hub's own release from the release cache
//     (AssetCacheDir/<version>/, downloaded when missing).
//
// The hub checks the signatures with its release key too, but only the
// node's check counts.

// DefaultManifestRetry is how long the hub waits before it downloads again
// a signature for a manifest override that has none, or the signed
// backends.yaml of its release after a download failed.
const DefaultManifestRetry = 10 * time.Minute

// manifestState is the manifest override the hub sends to its nodes.
type manifestState struct {
	// mu guards the fields and serialises the writes of the override and
	// its signature.
	mu sync.Mutex
	// override is set while a manifest override is active; data are its
	// bytes and sig its verified signature (nil while it has none: it is
	// not sent then).
	override  bool
	data, sig []byte

	// relMu serialises downloads of the release manifest; relFailed is the
	// time the last one failed (zero = none).
	relMu     sync.Mutex
	relFailed time.Time
}

// builtinManifestSHA256 is the sha256 of the backends.yaml built into
// this binary, compared with the one a node announces.
var builtinManifestSHA256 = sync.OnceValue(func() string {
	h := sha256.Sum256(backend.EmbeddedManifest())
	return hex.EncodeToString(h[:])
})

// loadManifestOverride activates the manifest override when there is one
// and checks its signature (New). A broken override is logged and the
// built-in manifest stays active; an override without a valid signature is
// active on the hub but not sent to the nodes until manifestSigLoop has
// one.
func (h *Hub) loadManifestOverride() {
	p := h.path(config.ManifestPath)
	data, err := os.ReadFile(p) // #nosec G304 -- fixed root-owned path
	if os.IsNotExist(err) {
		return
	}
	var m *backend.ManifestFile
	if err == nil {
		m, err = backend.ParseManifest(data)
	}
	if err != nil {
		h.log.Error("the backend manifest override cannot be used; the built-in manifest stays active",
			slog.String("path", p), dlog.Err(err), dlog.Code(deyerr.S001))
		return
	}
	backend.SetManifest(m)
	sig, err := os.ReadFile(h.path(config.ManifestSigPath)) // #nosec G304 -- fixed root-owned path
	if err == nil {
		err = install.VerifyMinisignAs(deyerr.S001, install.ManifestFile, data, sig, h.o.MinisignKey)
	}
	if err != nil {
		sig = nil
	}
	h.man.mu.Lock()
	h.man.override, h.man.data, h.man.sig = true, data, sig
	h.man.mu.Unlock()
	if sig == nil {
		h.log.Warn("the backend manifest override has no valid signature: nodes with signed updates install none of its "+
			"backend versions until the hub has one (it keeps trying to download it); run deyroute update manifest",
			slog.String("path", p), slog.String("fix", "deyroute update manifest"), dlog.Err(err))
	}
}

// installManifest writes the verified manifest data and its signature sig
// as the override and activates it (`deyroute update manifest`).
func (h *Hub) installManifest(data, sig []byte) error {
	h.man.mu.Lock()
	defer h.man.mu.Unlock()
	if err := install.InstallManifest(h.o.Root, data, sig); err != nil {
		return err
	}
	if err := backend.LoadManifestOverride(h.path(config.ManifestPath)); err != nil {
		return err
	}
	h.man.override, h.man.data, h.man.sig = true, data, sig
	return nil
}

// unsignedOverride returns the active override when it has no signature.
func (h *Hub) unsignedOverride() ([]byte, bool) {
	h.man.mu.Lock()
	defer h.man.mu.Unlock()
	return h.man.data, h.man.override && h.man.sig == nil
}

// manifestSigLoop downloads a signature for a manifest override that has
// none: when the hub starts, then every ManifestRetry until it has one.
func (h *Hub) manifestSigLoop(ctx context.Context) {
	if _, ok := h.unsignedOverride(); !ok {
		return
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if h.fetchManifestSig(ctx) {
			return
		}
		t.Reset(h.o.ManifestRetry)
	}
}

// fetchManifestSig downloads a signature of the override's bytes through
// the fetcher chain (the newest release's, then the one of the hub's own
// release) and keeps it next to the override. It reports whether nothing
// is left to do.
func (h *Hub) fetchManifestSig(ctx context.Context) bool {
	data, ok := h.unsignedOverride()
	if !ok {
		return true
	}
	versions := []string{""}
	if isRelease(version.Version) {
		versions = append(versions, version.Version)
	}
	sig, err := install.FetchManifestSig(ctx, h.Fetcher(), h.sources(), versions, data, h.o.MinisignKey,
		install.RetryOptions{Tries: manifestFetchTries, Backoff: time.Second})
	if err != nil {
		if ctx.Err() == nil {
			h.log.Info("no signature of the backend manifest override yet; trying again later (or run deyroute update manifest)",
				dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		}
		return false
	}
	h.man.mu.Lock()
	defer h.man.mu.Unlock()
	if !h.man.override || h.man.sig != nil || !bytes.Equal(h.man.data, data) {
		return true // `deyroute update manifest` replaced it meanwhile
	}
	p := h.path(config.ManifestSigPath)
	if err := config.WriteFileAtomic(p, sig, 0o644); err != nil {
		h.log.Warn("cannot save the signature of the backend manifest override", slog.String("path", p), dlog.Err(err))
		return false
	}
	h.man.sig = sig
	h.log.Info("signature of the backend manifest override downloaded; nodes with signed updates get the override again",
		slog.String("path", p))
	return true
}

// nodeManifest returns the manifest and signature to send with
// backend.install to node (nil, nil = none; see the top of this file).
func (h *Hub) nodeManifest(ctx context.Context, node string) (data, sig []byte) {
	hello, _ := h.nodeHello(node)
	if !hello.HasFeature(api.FeatureSignedUpdate) {
		return nil, nil
	}
	h.man.mu.Lock()
	override, data, sig := h.man.override, h.man.data, h.man.sig
	h.man.mu.Unlock()
	if override {
		if sig == nil {
			return nil, nil
		}
		return data, sig
	}
	if hello.ManifestSHA256 == builtinManifestSHA256() {
		return nil, nil
	}
	return h.releaseManifest(ctx)
}

// releaseManifest returns backends.yaml and backends.yaml.minisig of this
// hub's release from the release cache, downloading them through the
// fetcher chain when they are missing (after a failure, again only after
// ManifestRetry). A development build has none.
func (h *Hub) releaseManifest(ctx context.Context) (data, sig []byte) {
	ver := version.Version
	if !isRelease(ver) {
		return nil, nil
	}
	dir := h.bundleDir(ver)
	if data, sig, ok := readManifestPair(dir); ok {
		return data, sig
	}
	h.man.relMu.Lock()
	defer h.man.relMu.Unlock()
	if data, sig, ok := readManifestPair(dir); ok {
		return data, sig
	}
	if !h.man.relFailed.IsZero() && h.now().Sub(h.man.relFailed) < h.o.ManifestRetry {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- public release files, like the rest of the bin dir
		h.log.Warn("cannot keep the signed backends.yaml of the release", slog.String("path", dir), dlog.Err(err))
		return nil, nil
	}
	h.storeManifest(ctx, dir, ver)
	data, sig, ok := readManifestPair(dir)
	if !ok {
		h.man.relFailed = h.now()
		return nil, nil
	}
	h.man.relFailed = time.Time{}
	return data, sig
}

// readManifestPair reads backends.yaml and backends.yaml.minisig in dir.
func readManifestPair(dir string) (data, sig []byte, ok bool) {
	mp, sp := filepath.Join(dir, install.ManifestFile), filepath.Join(dir, install.ManifestSigFile)
	if !regularFile(mp) || !regularFile(sp) {
		return nil, nil, false
	}
	data, err := os.ReadFile(mp) // #nosec G304 -- the hub's own release cache
	if err != nil {
		return nil, nil, false
	}
	sig, err = os.ReadFile(sp) // #nosec G304 -- the hub's own release cache
	if err != nil {
		return nil, nil, false
	}
	return data, sig, true
}
