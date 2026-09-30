package install

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ArchiveMarker is the file in a backend version directory holding the
// manifest sha256 of the archive the binaries were extracted from.
const ArchiveMarker = ".archive.sha256"

// Layout places files under Root ("/" in production).
type Layout struct {
	Root string
}

func (l Layout) root() string {
	if l.Root == "" {
		return "/"
	}
	return l.Root
}

// Path joins an absolute system path (e.g. config.BinDir) under Root.
func (l Layout) Path(abs string) string { return filepath.Join(l.root(), abs) }

// BackendDir is Root/var/lib/deyroute/bin/<backend>.
func (l Layout) BackendDir(name string) string {
	return filepath.Join(l.Path(config.BinDir), name)
}

// BinDir is Root/var/lib/deyroute/bin/<backend>/<version> (spec sections 2
// and 7). Versions containing "/" (e.g. hysteria's "app/v2.12.3") are
// path-escaped so every version is exactly one directory.
func (l Layout) BinDir(name, version string) string {
	return filepath.Join(l.BackendDir(name), versionDir(version))
}

// BinaryPath is BinDir(name, version)/<bin>.
func (l Layout) BinaryPath(name, version, bin string) string {
	return filepath.Join(l.BinDir(name, version), bin)
}

func versionDir(v string) string { return url.PathEscape(v) }

func validComponent(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && !strings.HasPrefix(s, ".")
}

// InstallBackend makes the binaries of manifest entry e for arch available in
// BinDir(e.Name, e.Version) and returns that directory.
//
//   - Builtin/System entries need nothing: ("", nil).
//   - No URL for arch → DEY-B005; no (valid) sha256 for arch → DEY-S006.
//   - When the directory already holds every binary matching its <bin>.sha256
//     and the archive marker equals the manifest sha256, nothing is
//     downloaded. A directory that exists but does not verify is never
//     overwritten: DEY-S001 with instructions.
//   - Otherwise the archive (URL with "{mirror}" resolved) is fetched through
//     f with FetchVerified (3 tries, sha256 from the manifest; DEY-S001 on
//     mismatch, DEY-B001 when no source works), extracted in a staging
//     directory, a <bin>.sha256 ("<hex>  <bin>") is written next to each
//     binary, and the finished directory is renamed into place atomically.
func (l Layout) InstallBackend(ctx context.Context, e backend.ManifestEntry, arch string, f Fetcher, mirror string) (string, error) {
	return l.installBackend(ctx, e, arch, f, mirror, RetryOptions{})
}

func (l Layout) installBackend(ctx context.Context, e backend.ManifestEntry, arch string, f Fetcher, mirror string, ro RetryOptions) (string, error) {
	if e.Builtin || e.System {
		return "", nil
	}
	params := deyerr.Params{"backend": e.Name, "version": e.Version, "arch": arch}
	if e.Version == "" || !validComponent(e.Name) {
		return "", deyerr.New(deyerr.B008, deyerr.Params{"backend": e.Name})
	}
	if !validComponent(versionDir(e.Version)) {
		return "", deyerr.New(deyerr.B008, deyerr.Params{"backend": e.Name}).
			WithWhy(fmt.Sprintf("the manifest version %q is not usable as a directory name", e.Version))
	}
	rawURL := e.URLs[arch]
	if rawURL == "" {
		return "", deyerr.New(deyerr.B005, params)
	}
	sum := strings.ToLower(strings.TrimSpace(e.SHA256[arch]))
	if !validSHA256(sum) {
		return "", deyerr.New(deyerr.S006, params)
	}
	bins := e.Binaries
	if len(bins) == 0 {
		bins = []string{e.Binary()}
	}
	dir := l.BinDir(e.Name, e.Version)
	switch state, reason := verifyDir(dir, bins, sum); state {
	case dirOK:
		return dir, nil
	case dirBad:
		return "", deyerr.New(deyerr.S001, deyerr.Params{"file": dir}).
			WithWhy(reason + "; installed backend binaries are never overwritten").
			WithFix(fmt.Sprintf("remove %s and run: deyroute update backends %s", dir, e.Name))
	}
	if f == nil {
		return "", deyerr.New(deyerr.B001, params).WithDetail("no fetcher configured")
	}

	parent := l.BackendDir(e.Name)
	if err := mkdirPublic(parent); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": parent})
	}
	stage, err := os.MkdirTemp(parent, ".stage-")
	if err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": parent})
	}
	defer func() { _ = os.RemoveAll(stage) }()

	u := backend.ResolveURL(rawURL, mirror)
	archive := filepath.Join(stage, "download")
	ro.File = baseOfURL(u)
	if ro.FailCode == "" {
		ro.FailCode = deyerr.B001
	}
	if ro.MismatchCode == "" {
		ro.MismatchCode = deyerr.S001
	}
	ro.Params = params
	if err := FetchVerified(ctx, f, []string{u}, sum, archive, ro); err != nil {
		return "", err
	}
	out := filepath.Join(stage, "out")
	paths, err := Extract(archive, e.Archive, bins, out)
	if err != nil {
		var de *deyerr.Error
		if stderrors.As(err, &de) && de.Code == deyerr.B001 && de.Params == nil {
			de.Params = params
		}
		return "", err
	}
	for _, b := range bins {
		h, err := SHA256File(paths[b])
		if err != nil {
			return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": paths[b]})
		}
		if err := writeFileAtomic(filepath.Join(out, b+".sha256"), []byte(h+"  "+b+"\n"), 0o644); err != nil {
			return "", err
		}
	}
	if err := writeFileAtomic(filepath.Join(out, ArchiveMarker), []byte(sum+"\n"), 0o644); err != nil {
		return "", err
	}
	err = os.Chmod(out, 0o755) // #nosec G302 -- directory must be traversable by the deyroute user
	if err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": out})
	}
	syncDir(out)
	if err := os.Rename(out, dir); err != nil {
		// Someone else may have finished the same install concurrently.
		if st, _ := verifyDir(dir, bins, sum); st == dirOK {
			return dir, nil
		}
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dir})
	}
	syncDir(parent)
	return dir, nil
}

type dirState int

const (
	dirMissing dirState = iota
	dirOK
	dirBad
)

// verifyDir checks an installed version directory: the archive marker must
// equal archiveSum (when archiveSum != "") and every binary must match its
// <bin>.sha256.
func verifyDir(dir string, bins []string, archiveSum string) (dirState, string) {
	fi, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return dirMissing, ""
	}
	if err != nil {
		return dirBad, err.Error()
	}
	if !fi.IsDir() {
		return dirBad, dir + " is not a directory"
	}
	if archiveSum != "" {
		m, err := os.ReadFile(filepath.Join(dir, ArchiveMarker)) // #nosec G304 -- our layout
		if err != nil {
			return dirBad, "archive marker missing"
		}
		if strings.TrimSpace(string(m)) != archiveSum {
			return dirBad, "it was installed from a different archive than the manifest pins"
		}
	}
	for _, b := range bins {
		if err := verifyBinary(filepath.Join(dir, b)); err != nil {
			return dirBad, err.Error()
		}
	}
	return dirOK, ""
}

// verifyBinary checks bin against bin.sha256.
func verifyBinary(bin string) error {
	fi, err := os.Lstat(bin)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(bin), err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", filepath.Base(bin))
	}
	want, err := os.ReadFile(bin + ".sha256") // #nosec G304 -- our layout
	if err != nil {
		return fmt.Errorf("%s.sha256: %w", filepath.Base(bin), err)
	}
	fields := strings.Fields(string(want))
	if len(fields) == 0 {
		return fmt.Errorf("%s.sha256 is empty", filepath.Base(bin))
	}
	got, err := SHA256File(bin)
	if err != nil {
		return err
	}
	if strings.ToLower(fields[0]) != got {
		return fmt.Errorf("%s does not match %s.sha256", filepath.Base(bin), filepath.Base(bin))
	}
	return nil
}

// VerifyBackend checks an installed backend version (every binary matches
// its .sha256 file). Missing → DEY-B001, mismatch → DEY-S001. Used by
// doctor and before starting units.
func (l Layout) VerifyBackend(e backend.ManifestEntry) error {
	if e.Builtin || e.System {
		return nil
	}
	bins := e.Binaries
	if len(bins) == 0 {
		bins = []string{e.Binary()}
	}
	dir := l.BinDir(e.Name, e.Version)
	switch st, reason := verifyDir(dir, bins, ""); st {
	case dirMissing:
		return deyerr.New(deyerr.B001, deyerr.Params{"backend": e.Name, "version": e.Version}).
			WithDetail(dir + " does not exist")
	case dirBad:
		return deyerr.New(deyerr.S001, deyerr.Params{"file": dir}).WithWhy(reason)
	}
	return nil
}

// InstalledVersions lists the versions of backend name present on disk,
// oldest first (SemVer order, then lexical). Staging directories are ignored.
func (l Layout) InstalledVersions(name string) ([]string, error) {
	if !validComponent(name) {
		return nil, deyerr.New(deyerr.B008, deyerr.Params{"backend": name})
	}
	ents, err := os.ReadDir(l.BackendDir(name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": l.BackendDir(name)})
	}
	var out []string
	for _, de := range ents {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		v, err := url.PathUnescape(de.Name())
		if err != nil {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if c, ok := CompareVersions(out[i], out[j]); ok && c != 0 {
			return c < 0
		}
		return out[i] < out[j]
	})
	return out, nil
}

// RemoveBackendVersions deletes every installed version of backend name that
// is not in keep (and stale staging directories). keep == nil removes all;
// the backend directory itself is removed when it ends up empty.
func (l Layout) RemoveBackendVersions(name string, keep []string) error {
	if !validComponent(name) {
		return deyerr.New(deyerr.B008, deyerr.Params{"backend": name})
	}
	base := l.BackendDir(name)
	ents, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": base})
	}
	keepDirs := map[string]bool{}
	for _, k := range keep {
		keepDirs[versionDir(k)] = true
	}
	var errs []error
	for _, de := range ents {
		if keepDirs[de.Name()] {
			continue
		}
		p := filepath.Join(base, de.Name())
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p}))
		}
	}
	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	if len(keep) == 0 {
		if err := os.Remove(base); err != nil && !os.IsNotExist(err) {
			return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": base})
		}
	}
	return nil
}
