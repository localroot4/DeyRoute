package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/version"
)

// Architectures release archives exist for (section 2).
var releaseArchs = []string{"amd64", "arm64"}

// fileDigest caches the sha256 of an asset file.
type fileDigest struct {
	size    int64
	modTime time.Time
	sum     string
}

// asset handles GET /v1/assets/deyroute/{arch} (section 5: nodes always get
// deyroute from the hub so every server runs the same version). The hub's
// own architecture is served from SelfBinary; another architecture from
// the cache Root/var/lib/deyroute/bin/deyroute-<version>-<arch>, which is filled
// on first use with the signed release archive of the same version
// downloaded through the fetcher chain (via a node first).
func (h *Hub) asset(ctx context.Context, arch string) (api.AssetInfo, error) {
	info, err := h.assetFor(ctx, arch)
	if err != nil {
		h.logAssetError(arch, err)
	}
	return info, err
}

func (h *Hub) assetFor(ctx context.Context, arch string) (api.AssetInfo, error) {
	if arch == h.o.Arch {
		return h.openAsset(h.o.SelfBinary)
	}
	if !knownArch(arch) {
		return api.AssetInfo{}, deyerr.New(deyerr.I003, deyerr.Params{"arch": arch})
	}
	p := h.assetCachePath(arch)
	if regularFile(p) {
		return h.openAsset(p)
	}
	h.assetMu.Lock()
	defer h.assetMu.Unlock()
	if regularFile(p) {
		return h.openAsset(p)
	}
	if err := h.downloadAsset(ctx, arch, p); err != nil {
		return api.AssetInfo{}, err
	}
	return h.openAsset(p)
}

func knownArch(arch string) bool {
	for _, a := range releaseArchs {
		if a == arch {
			return true
		}
	}
	return false
}

// assetCachePath is the cached binary of arch for this hub's version.
func (h *Hub) assetCachePath(arch string) string {
	return h.path(filepath.Join(AssetCacheDir, install.BinaryName+"-"+version.Version+"-"+arch))
}

// downloadAsset fetches, verifies (SHA256SUMS + minisign) and caches the
// deyroute binary of arch.
func (h *Hub) downloadAsset(ctx context.Context, arch, dst string) error {
	file := install.ArchiveName(version.Version, arch)
	if !isRelease(version.Version) {
		return deyerr.New(deyerr.I004, deyerr.Params{"file": file}).
			WithWhy("this hub runs a development build (" + version.Version + "); only release builds can be downloaded for another architecture").
			WithFix("install a release build on the hub (deyroute update), or install the node with the installer directly")
	}
	work, err := os.MkdirTemp(ensureDir(h.path(DownloadDir)), "asset-"+arch+"-")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": h.path(DownloadDir)})
	}
	defer func() { _ = os.RemoveAll(work) }()
	rel, err := install.DownloadRelease(ctx, install.ReleaseOptions{
		Fetcher:   h.Fetcher(),
		Sources:   h.sources(),
		Version:   version.Version,
		Arch:      arch,
		PublicKey: h.o.MinisignKey,
		WorkDir:   work,
	})
	if err != nil {
		return err
	}
	if err := copyFileAtomic(rel.Binary, dst, 0o755); err != nil {
		return err
	}
	h.log.Info("deyroute binary cached for nodes", slog.String("arch", arch), slog.String("version", rel.Version),
		slog.String("source", rel.Source))
	return nil
}

// sources returns the release download sources: DEYROUTE_MIRROR (wins),
// hub.mirror, the build's release base, GitHub.
func (h *Hub) sources() []install.Source {
	return install.Sources(h.o.Getenv(install.MirrorEnv), h.Config().Hub.Mirror, "")
}

// isRelease reports whether v is a SemVer release (not "dev").
func isRelease(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" {
		return false
	}
	_, ok := install.CompareVersions(v, v)
	return ok
}

// ensureDir creates dir (0700) and returns it.
func ensureDir(dir string) string {
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// regularFile reports whether p is an existing regular file.
func regularFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// openAsset opens p for serving with its size, sha256 and the hub version.
func (h *Hub) openAsset(p string) (api.AssetInfo, error) {
	f, err := os.Open(p) // #nosec G304 -- the hub's own binary or its cache
	if err != nil {
		return api.AssetInfo{}, deyerr.Wrap(deyerr.X000, err, nil).
			WithDetail("the deyroute binary " + p + " cannot be read")
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = stderrors.New("not a regular file")
	}
	var sum string
	if err == nil {
		sum, err = h.digest(p, f, fi)
	}
	if err != nil {
		_ = f.Close()
		return api.AssetInfo{}, deyerr.Wrap(deyerr.X000, err, nil).
			WithDetail("the deyroute binary " + p + " cannot be read")
	}
	return api.AssetInfo{Reader: f, Size: fi.Size(), SHA256: sum, Version: version.Version}, nil
}

// digest returns the sha256 of the open file f (cached by path, size and
// modification time) and rewinds f.
func (h *Hub) digest(p string, f *os.File, fi fs.FileInfo) (string, error) {
	h.digestMu.Lock()
	d, ok := h.digests[p]
	h.digestMu.Unlock()
	if ok && d.size == fi.Size() && d.modTime.Equal(fi.ModTime()) {
		return d.sum, nil
	}
	hs := sha256.New()
	if _, err := io.Copy(hs, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(hs.Sum(nil))
	h.digestMu.Lock()
	h.digests[p] = fileDigest{size: fi.Size(), modTime: fi.ModTime(), sum: sum}
	h.digestMu.Unlock()
	return sum, nil
}

// copyFileAtomic copies src to dst through a temporary file in dst's
// directory (fsync, chmod, rename).
func copyFileAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- extracted release binary in our work dir
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": src})
	}
	defer func() { _ = in.Close() }()
	dir := filepath.Dir(dst)
	err = os.MkdirAll(dir, 0o755) // #nosec G301 -- the bin dir holds public release files
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Chmod(mode); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Sync(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Close(); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := os.Rename(name, dst); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	ok = true
	return nil
}

// logAssetError keeps asset failures in the hub log (the node only sees
// the DEY code).
func (h *Hub) logAssetError(arch string, err error) {
	h.log.Warn("cannot serve the deyroute binary", slog.String("arch", arch), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
}
