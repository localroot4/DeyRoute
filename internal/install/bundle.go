package install

import (
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Release bundles (DR-03). A hub keeps the signed files of a release for its
// nodes, and a node installs a deyroute binary only after it has checked
// them with the release key built into it: the hub's word is never enough.

// MaxReleaseBinaryBytes caps the deyroute binary self.update extracts from
// a release archive (200 MiB; a release binary is about 20 MiB).
const MaxReleaseBinaryBytes int64 = 200 << 20

// releaseBinaryLimit is the cap in effect (tests lower it).
var releaseBinaryLimit = MaxReleaseBinaryBytes

// maxSumsBytes caps SHA256SUMS and SHA256SUMS.minisig read from a bundle.
const maxSumsBytes = 1 << 20

// BundleFiles are the files of the release ver a node needs to update
// itself on arch: SHA256SUMS, its signature and the archive.
func BundleFiles(ver, arch string) []string {
	return []string{SumsFile, SumsSigFile, ArchiveName(strings.TrimPrefix(ver, "v"), arch)}
}

// IsReleaseVersion reports whether v is a SemVer release version such as
// "0.3.0" or "0.3.0-edge.60", that is not a development build ("dev", "").
// Only release versions have signed release files.
func IsReleaseVersion(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || v == "dev" {
		return false
	}
	_, ok := CompareVersions(v, v)
	return ok
}

// VerifyBundle checks the release files in dir before self.update installs
// anything from them, in this order: SHA256SUMS.minisig is a valid
// signature of SHA256SUMS by pubKey ("" = MinisignPublicKey, see VerifySums);
// the signed SHA256SUMS lists the archive deyroute_<ver>_linux_<arch>.tar.gz,
// so the version in it is ver; and that archive in dir has the listed
// sha256. It returns the archive's path. Every failure is DEY-S001 with a
// why that names the check that failed.
func VerifyBundle(dir, arch, ver, pubKey string) (string, error) {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	if ver == "" {
		return "", bundleErr(SumsFile, "the hub named no version to check the release against")
	}
	want := ArchiveName(ver, arch)
	sums, err := readBundleFile(dir, SumsFile)
	if err != nil {
		return "", bundleErr(SumsFile, "the release has no readable SHA256SUMS ("+err.Error()+")")
	}
	sig, err := readBundleFile(dir, SumsSigFile)
	if err != nil {
		return "", bundleErr(SumsFile, "the release has no readable signature SHA256SUMS.minisig ("+err.Error()+"), so its checksums cannot be trusted")
	}
	if err := VerifySums(sums, sig, pubKey); err != nil {
		why := "SHA256SUMS.minisig is not a valid signature of SHA256SUMS by the DEYROUTE release key built into this binary"
		if w := deyerr.As(err).WhyOverride; w != "" {
			why += ": " + w
		}
		return "", deyerr.Wrap(deyerr.S001, err, deyerr.Params{"file": SumsFile}).WithWhy(why)
	}
	parsed, err := ParseSHA256SUMS(sums)
	if err != nil {
		return "", deyerr.Wrap(deyerr.S001, err, deyerr.Params{"file": SumsFile}).
			WithWhy("the signed SHA256SUMS cannot be read: " + deyerr.As(err).Why())
	}
	_, _, sum, err := ReleaseFromSums(parsed, arch, ver)
	if err != nil {
		listed := "no archive for " + arch
		if names := listedArchives(parsed, arch); len(names) > 0 {
			listed = strings.Join(names, ", ")
		}
		return "", bundleErr(want, "the signed SHA256SUMS does not list "+want+": it belongs to another release ("+listed+")")
	}
	p := filepath.Join(dir, want)
	got, err := SHA256File(p)
	if err != nil {
		return "", bundleErr(want, "the release has no readable "+want+" ("+err.Error()+")")
	}
	if got != sum {
		return "", bundleErr(want, fmt.Sprintf("%s does not match the signed SHA256SUMS (sha256 %s, signed %s)", want, got, sum))
	}
	return p, nil
}

// bundleErr is DEY-S001 for file with why.
func bundleErr(file, why string) error {
	return deyerr.New(deyerr.S001, deyerr.Params{"file": file}).WithWhy(why)
}

// readBundleFile reads dir/name, a regular file of at most maxSumsBytes.
func readBundleFile(dir, name string) ([]byte, error) {
	p := filepath.Join(dir, name)
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if fi.Size() > maxSumsBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, maxSumsBytes)
	}
	f, err := os.Open(p) // #nosec G304 -- a file of the staged release bundle
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxSumsBytes))
}

// listedArchives names the deyroute archives for arch in a parsed SHA256SUMS.
func listedArchives(sums map[string]string, arch string) []string {
	var out []string
	for name := range sums {
		if m := archiveRe.FindStringSubmatch(path.Base(name)); m != nil && m[2] == arch {
			out = append(out, path.Base(name))
		}
	}
	sort.Strings(out)
	return out
}

// ExtractReleaseBinary writes the deyroute binary of a verified release
// archive (tar.gz) to dstDir and returns its path. Only a regular file named
// deyroute is extracted (the archive has it at the top level or in one
// directory), at most MaxReleaseBinaryBytes; an archive with an absolute or
// ".." entry path, a deyroute that is a link or not a regular file, two of
// them or none is refused. A refused archive is DEY-S001 with the reason as
// why.
func ExtractReleaseBinary(archivePath, dstDir string) (string, error) {
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": dstDir})
	}
	out, err := extractTarGz(archivePath, []string{BinaryName}, dstDir, releaseBinaryLimit)
	if err != nil {
		var de *deyerr.Error
		if stderrors.As(err, &de) && de.Code == deyerr.B001 {
			return "", deyerr.Wrap(deyerr.S001, err, deyerr.Params{"file": filepath.Base(archivePath)}).
				WithWhy("the archive holds no single safe deyroute binary: " + de.Detail)
		}
		return "", err
	}
	return out[BinaryName], nil
}
