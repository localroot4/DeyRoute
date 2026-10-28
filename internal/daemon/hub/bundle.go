package hub

import (
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/version"
)

// Signed releases for the nodes (DR-03). A node installs a deyroute binary
// only when it can check it against a release signed with the key built
// into it, so the hub keeps the signed files of the release of every
// version it serves under AssetCacheDir/<version>/ (with its own
// architecture's): SHA256SUMS, SHA256SUMS.minisig, the archives
// deyroute_<version>_linux_<arch>.tar.gz it served, and backends.yaml with
// backends.yaml.minisig of the same release. `deyroute update`, the
// download for another architecture and the installer (also with --local)
// leave them there; a hub without them downloads its release through the
// fetcher chain (via a node first) on the first request, as for another
// architecture. The hub checks the signature when it stores the files and
// serves them as they are: the node is the one that verifies.

// hubFeatures are the features the hub announces in the hello it sends to
// every node (api.Hello.Features).
var hubFeatures = []string{api.FeatureSignedUpdate}

// manifestFetchTries bounds the downloads of the signed backends.yaml kept
// with a release (it is not needed for self.update).
const manifestFetchTries = 2

// bundleDir is AssetCacheDir/<ver>/, the signed release files of ver.
func (h *Hub) bundleDir(ver string) string {
	return h.path(filepath.Join(AssetCacheDir, strings.TrimPrefix(ver, "v")))
}

// hasBundle reports whether the files a node of arch needs from release
// ver are cached.
func (h *Hub) hasBundle(ver, arch string) bool {
	for _, f := range install.BundleFiles(ver, arch) {
		if !regularFile(filepath.Join(h.bundleDir(ver), f)) {
			return false
		}
	}
	return true
}

// releaseBundle handles GET /v1/assets/release/{arch}: the signed release
// of this hub's version for arch (api.FeatureSignedUpdate), fetched first
// when it is not cached. A development build has none (DEY-S012).
func (h *Hub) releaseBundle(ctx context.Context, arch string) (api.ReleaseBundle, error) {
	if err := h.ensureBundle(ctx, arch); err != nil {
		return api.ReleaseBundle{}, err
	}
	return h.openBundle(version.Version, arch)
}

// ensureBundle makes sure the signed release of this hub's version for
// arch is cached, downloading and verifying it through the fetcher chain
// when it is not.
func (h *Hub) ensureBundle(ctx context.Context, arch string) error {
	if !isRelease(version.Version) {
		return deyerr.New(deyerr.S012, deyerr.Params{"version": version.Version})
	}
	if !knownArch(arch) {
		return deyerr.New(deyerr.I003, deyerr.Params{"arch": arch})
	}
	if h.hasBundle(version.Version, arch) {
		return nil
	}
	h.assetMu.Lock()
	defer h.assetMu.Unlock()
	if h.hasBundle(version.Version, arch) {
		return nil
	}
	rel, cleanup, err := h.downloadRelease(ctx, arch)
	if err != nil {
		return err
	}
	defer cleanup()
	return h.storeBundle(ctx, rel, arch)
}

// downloadRelease downloads and verifies the release of this hub's version
// for arch into a work directory (SHA256SUMS signature, archive checksum);
// cleanup removes it.
func (h *Hub) downloadRelease(ctx context.Context, arch string) (*install.Release, func(), error) {
	work, err := os.MkdirTemp(ensureDir(h.path(DownloadDir)), "asset-"+arch+"-")
	if err != nil {
		return nil, nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": h.path(DownloadDir)})
	}
	cleanup := func() { _ = os.RemoveAll(work) }
	rel, err := install.DownloadRelease(ctx, install.ReleaseOptions{
		Fetcher:   h.Fetcher(),
		Sources:   h.sources(),
		Version:   version.Version,
		Arch:      arch,
		PublicKey: h.o.MinisignKey,
		WorkDir:   work,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return rel, cleanup, nil
}

// storeBundle keeps the verified release rel for arch under
// bundleDir(rel.Version): the archive, then SHA256SUMS.minisig and
// SHA256SUMS (each replaced atomically), and the signed backends.yaml of
// the same release when it can be downloaded.
func (h *Hub) storeBundle(ctx context.Context, rel *install.Release, arch string) error {
	dir := h.bundleDir(rel.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- public release files, like the rest of the bin dir
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	if err := copyFileAtomic(rel.Archive, filepath.Join(dir, install.ArchiveName(rel.Version, arch)), 0o644); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{install.SumsSigFile, rel.SumsSig}, {install.SumsFile, rel.Sums}} {
		p := filepath.Join(dir, f.name)
		if len(f.data) == 0 {
			return deyerr.New(deyerr.X000, nil).WithDetail("the verified release has no " + f.name)
		}
		if err := config.WriteFileAtomic(p, f.data, 0o644); err != nil {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
		}
	}
	h.storeManifest(ctx, dir, rel.Version)
	h.log.Info("signed release kept for the nodes", slog.String("version", rel.Version), slog.String("arch", arch),
		slog.String("dir", dir))
	return nil
}

// storeManifest keeps backends.yaml and backends.yaml.minisig of release
// ver in dir (signature verified, not parsed). A failure is logged only:
// self.update does not need them.
func (h *Hub) storeManifest(ctx context.Context, dir, ver string) {
	mp, sp := filepath.Join(dir, install.ManifestFile), filepath.Join(dir, install.ManifestSigFile)
	if regularFile(mp) && regularFile(sp) {
		return
	}
	data, sig, err := install.FetchSignedManifest(ctx, h.Fetcher(), h.sources(), ver, h.o.MinisignKey,
		install.RetryOptions{Tries: manifestFetchTries, Backoff: time.Second})
	if err == nil {
		err = config.WriteFileAtomic(sp, sig, 0o644)
	}
	if err == nil {
		err = config.WriteFileAtomic(mp, data, 0o644)
	}
	if err != nil {
		h.log.Warn("the signed backends.yaml of the release is not kept with it", slog.String("version", ver),
			dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
}

// pruneBundles removes, when the hub starts, the cached releases of every
// version but the running one and the version of deyroute.prev (what a
// rollback returns to); every update restarts the hub, so at most two stay
// (plus one stored by an update until its restart). Only directories named
// like a release version are touched: backend binaries live in
// AssetCacheDir/<backend>/.
func (h *Hub) pruneBundles() {
	kept := map[string]bool{}
	for _, v := range []string{version.Version, h.previousVersion()} {
		kept[strings.TrimPrefix(v, "v")] = true
	}
	dir := h.path(AssetCacheDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || !isRelease(n) || kept[n] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
			h.log.Warn("cannot remove an older release kept for the nodes", slog.String("version", n), dlog.Err(err))
			continue
		}
		h.log.Info("older release kept for the nodes removed", slog.String("version", n))
	}
}

// openBundle opens the cached release files of ver for arch.
func (h *Hub) openBundle(ver, arch string) (api.ReleaseBundle, error) {
	b := api.ReleaseBundle{Version: ver}
	for _, name := range install.BundleFiles(ver, arch) {
		p := filepath.Join(h.bundleDir(ver), name)
		f, err := os.Open(p) // #nosec G304 -- the hub's own release cache
		if err != nil {
			b.Close()
			return api.ReleaseBundle{}, deyerr.Wrap(deyerr.X000, err, nil).WithDetail("the release file " + p + " cannot be read")
		}
		fi, err := f.Stat()
		if err == nil && !fi.Mode().IsRegular() {
			err = stderrors.New("not a regular file")
		}
		if err != nil {
			_ = f.Close()
			b.Close()
			return api.ReleaseBundle{}, deyerr.Wrap(deyerr.X000, err, nil).WithDetail("the release file " + p + " cannot be read")
		}
		b.Files = append(b.Files, api.ReleaseFile{Name: name, Size: fi.Size(), Reader: f})
	}
	return b, nil
}

// selfUpdateSum is SelfUpdateArgs.SHA256 for node of arch. A node with
// signed updates gets the sha256 of the release archive of this hub's
// version (the release is fetched now when it is not cached, so the node's
// request is served at once); its error stops the self.update. Any other
// node gets the sha256 of the hub's binary for the hub's own architecture,
// "" for another one, as before.
func (h *Hub) selfUpdateSum(ctx context.Context, node, arch string) (string, error) {
	if hello, _ := h.nodeHello(node); hello.HasFeature(api.FeatureSignedUpdate) && isRelease(version.Version) {
		if err := h.ensureBundle(ctx, arch); err != nil {
			return "", err
		}
		return install.SHA256File(filepath.Join(h.bundleDir(version.Version), install.ArchiveName(version.Version, arch)))
	}
	if arch != h.o.Arch {
		return "", nil
	}
	info, err := h.openAsset(h.o.SelfBinary)
	if err != nil {
		return "", nil
	}
	_ = info.Reader.Close()
	return info.SHA256, nil
}

// refuseOwnBinary keeps the hub's own binary (GET /v1/assets/deyroute/{arch}
// for the hub's architecture) from a node that announced signed updates
// while the hub runs a release: such a node installs only the signed
// release (GET /v1/assets/release/{arch}), and the hub never offers it
// unsigned bytes in its place. A development build has no release and
// still serves its binary; the node then decides (DEY-S012 unless
// node.allow_unsigned_updates).
func (h *Hub) refuseOwnBinary(node, arch string) error {
	if arch != h.o.Arch || !isRelease(version.Version) {
		return nil
	}
	if hello, _ := h.nodeHello(node); !hello.HasFeature(api.FeatureSignedUpdate) {
		return nil
	}
	return deyerr.New(deyerr.N015, deyerr.Params{"node": node,
		"reason": "a node with signed updates gets the signed release (GET " + api.PathReleasePrefix + arch + "), not the hub's own binary"})
}
