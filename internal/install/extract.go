package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Archive kinds of backends.yaml ("archive:").
const (
	KindTarGz = "tar.gz"
	KindZip   = "zip"
	KindGz    = "gz"
	KindRaw   = "raw"
)

// MaxExtractBytes caps every extracted file (512 MiB).
const MaxExtractBytes = DefaultMaxBytes

// extractLimit is the cap in effect (tests lower it).
var extractLimit = MaxExtractBytes

// Extract copies the executables named in names (matched by base name) out of
// the archive at archivePath into dstDir with mode 0755 and returns
// name → absolute path.
//
//   - tar.gz / zip: only regular-file entries whose base name is in names are
//     written. An archive containing an absolute or ".." entry path is refused
//     entirely; a wanted name that is a symlink, hard link or device, or that
//     appears twice, is refused.
//   - gz: a single gzip-compressed binary, written as names[0].
//   - raw: the file itself, copied as names[0].
//
// Every file is capped at MaxExtractBytes. A missing binary or unsafe
// archive is DEY-B001 with the reason in Detail (callers set the
// backend/version params).
func Extract(archivePath, kind string, names []string, dstDir string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, extractErr("no binary names given")
	}
	for _, n := range names {
		if n == "" || n != path.Base(n) || n == "." || n == ".." || strings.ContainsAny(n, `/\`) {
			return nil, extractErr(fmt.Sprintf("invalid binary name %q", n))
		}
	}
	err := os.MkdirAll(dstDir, 0o755) // #nosec G301 -- binary dirs are world-readable by design (section 7)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dstDir})
	}
	switch strings.ToLower(kind) {
	case KindTarGz, "tgz":
		return extractTarGz(archivePath, names, dstDir)
	case KindZip:
		return extractZip(archivePath, names, dstDir)
	case KindGz:
		if len(names) != 1 {
			return nil, extractErr("a gz archive holds exactly one binary")
		}
		return extractSingle(archivePath, true, names[0], dstDir)
	case KindRaw, "":
		if len(names) != 1 {
			return nil, extractErr("a raw download is exactly one binary")
		}
		return extractSingle(archivePath, false, names[0], dstDir)
	default:
		return nil, extractErr(fmt.Sprintf("unknown archive kind %q", kind))
	}
}

func extractErr(detail string) *deyerr.Error {
	return deyerr.New(deyerr.B001, nil).WithDetail(detail)
}

// safeEntryName reports whether an archive entry path stays inside the
// extraction root.
func safeEntryName(name string) bool {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
		return false
	}
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func wantedSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func extractTarGz(archivePath string, names []string, dstDir string) (map[string]string, error) {
	f, err := os.Open(archivePath) // #nosec G304 -- verified download in our staging dir
	if err != nil {
		return nil, extractErr(err.Error())
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, extractErr("not a gzip archive: " + err.Error())
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	want := wantedSet(names)
	out := map[string]string{}
	ok := false
	defer func() {
		if !ok {
			removeAll(out)
		}
	}()
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, extractErr("corrupt tar archive: " + err.Error())
		}
		if !safeEntryName(h.Name) || (h.Linkname != "" && h.Typeflag == tar.TypeLink && !safeEntryName(h.Linkname)) {
			return nil, extractErr(fmt.Sprintf("archive entry %q escapes the target directory", h.Name))
		}
		base := path.Base(strings.TrimRight(h.Name, "/"))
		if !want[base] {
			continue
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		// The tar reader already maps the legacy TypeRegA to TypeReg.
		if h.Typeflag != tar.TypeReg {
			return nil, extractErr(fmt.Sprintf("archive entry %q is not a regular file (type %q)", h.Name, string(h.Typeflag)))
		}
		if _, dup := out[base]; dup {
			return nil, extractErr(fmt.Sprintf("archive contains %q twice", base))
		}
		if h.Size > extractLimit {
			return nil, extractErr(fmt.Sprintf("%s is larger than %d bytes", h.Name, extractLimit))
		}
		p, err := writeBinary(dstDir, base, tr)
		if err != nil {
			return nil, err
		}
		out[base] = p
	}
	if err := missingCheck(out, names); err != nil {
		return nil, err
	}
	ok = true
	return out, nil
}

// removeAll deletes files extracted before a failure.
func removeAll(paths map[string]string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

func extractZip(archivePath string, names []string, dstDir string) (map[string]string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, extractErr("not a zip archive: " + err.Error())
	}
	defer func() { _ = zr.Close() }()
	want := wantedSet(names)
	out := map[string]string{}
	ok := false
	defer func() {
		if !ok {
			removeAll(out)
		}
	}()
	for _, zf := range zr.File {
		if !safeEntryName(zf.Name) {
			return nil, extractErr(fmt.Sprintf("archive entry %q escapes the target directory", zf.Name))
		}
		base := path.Base(strings.TrimRight(strings.ReplaceAll(zf.Name, `\`, "/"), "/"))
		if !want[base] {
			continue
		}
		mode := zf.Mode()
		if mode.IsDir() {
			continue
		}
		if !mode.IsRegular() {
			return nil, extractErr(fmt.Sprintf("archive entry %q is not a regular file (%s)", zf.Name, mode.Type()))
		}
		if _, dup := out[base]; dup {
			return nil, extractErr(fmt.Sprintf("archive contains %q twice", base))
		}
		limit := uint64(extractLimit) // #nosec G115 -- extractLimit is a positive size cap
		if zf.UncompressedSize64 > limit {
			return nil, extractErr(fmt.Sprintf("%s is larger than %d bytes", zf.Name, extractLimit))
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, extractErr("corrupt zip entry " + zf.Name + ": " + err.Error())
		}
		p, err := writeBinary(dstDir, base, rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out[base] = p
	}
	if err := missingCheck(out, names); err != nil {
		return nil, err
	}
	ok = true
	return out, nil
}

func extractSingle(archivePath string, gzipped bool, name, dstDir string) (map[string]string, error) {
	f, err := os.Open(archivePath) // #nosec G304 -- verified download in our staging dir
	if err != nil {
		return nil, extractErr(err.Error())
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, extractErr(err.Error())
	}
	if !fi.Mode().IsRegular() {
		return nil, extractErr(archivePath + " is not a regular file")
	}
	var r io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, extractErr("not a gzip file: " + err.Error())
		}
		defer func() { _ = gz.Close() }()
		gz.Multistream(false)
		r = gz
	}
	p, err := writeBinary(dstDir, name, r)
	if err != nil {
		return nil, err
	}
	return map[string]string{name: p}, nil
}

// writeBinary streams r into dstDir/name (0755) through a temp file, capped
// at extractLimit.
func writeBinary(dstDir, name string, r io.Reader) (string, error) {
	dst := filepath.Join(dstDir, name)
	tmp, err := os.CreateTemp(dstDir, "."+name+".x-*")
	if err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dstDir})
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, io.LimitReader(r, extractLimit+1))
	if err != nil {
		return "", extractErr(fmt.Sprintf("reading %s: %v", name, err))
	}
	if n > extractLimit {
		return "", extractErr(fmt.Sprintf("%s is larger than %d bytes", name, extractLimit))
	}
	if n == 0 {
		return "", extractErr(name + " is empty")
	}
	if err := tmp.Chmod(0o755); err != nil { // #nosec G302 -- executables must be runnable by the deyroute user
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Sync(); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := tmp.Close(); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dst})
	}
	ok = true
	abs, err := filepath.Abs(dst)
	if err != nil {
		return dst, nil
	}
	return abs, nil
}

func missingCheck(out map[string]string, names []string) error {
	var missing []string
	for _, n := range names {
		if _, ok := out[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return extractErr("binary not found in archive: " + strings.Join(missing, ", "))
}
