package install

import (
	"strings"
)

// Download locations (spec section 5). RELEASE_BASE in installer/install.sh
// must equal ReleaseBase and GITHUB_BASE must equal GitHubReleases (a test
// enforces both).
const (
	// ReleaseBase is the build-time default download base (the owner's CDN).
	// Placeholder until the owner provides a real one (QUESTIONS.md B).
	ReleaseBase = "https://get.deyroute.example"
	// GitHubReleases is the GitHub releases root of the project.
	GitHubReleases = "https://github.com/localroot4/deyroute/releases"
	// MirrorEnv is the environment variable naming the owner's mirror.
	MirrorEnv = "DEYROUTE_MIRROR"
)

// Source names, in the order they are tried.
const (
	SourceMirror      = "mirror"
	SourceReleaseBase = "release"
	SourceGitHub      = "github"
)

// Release file names (goreleaser: archives, checksum, signs, extra_files).
const (
	SumsFile        = "SHA256SUMS"
	SumsSigFile     = "SHA256SUMS.minisig"
	ManifestFile    = "backends.yaml"
	ManifestSigFile = "backends.yaml.minisig"
	InstallerFile   = "install.sh"
	BinaryName      = "deyroute"
)

// Source is one download location. BaseURL is the root of the release tree:
//
//   - mirror / release base: <BaseURL>/v<version>/<file>, and
//     <BaseURL>/latest/<file> for the newest release;
//   - GitHub: <BaseURL>/download/v<version>/<file>, and
//     <BaseURL>/latest/download/<file> (GitHub's redirect to the newest release).
type Source struct {
	Name    string
	BaseURL string
}

// Sources returns the download sources in the mandatory order: the owner
// mirror (the --mirror flag wins over DEYROUTE_MIRROR), the release base
// (releaseBase, or ReleaseBase when empty) and GitHub releases. Duplicate base
// URLs are dropped so a mirror equal to the release base is tried only once.
func Sources(flagMirror, envMirror, releaseBase string) []Source {
	var out []Source
	add := func(name, base string) {
		base = strings.TrimRight(strings.TrimSpace(base), "/")
		if base == "" {
			return
		}
		for _, s := range out {
			if s.BaseURL == base {
				return
			}
		}
		out = append(out, Source{Name: name, BaseURL: base})
	}
	mirror := flagMirror
	if strings.TrimSpace(mirror) == "" {
		mirror = envMirror
	}
	add(SourceMirror, mirror)
	if strings.TrimSpace(releaseBase) == "" {
		releaseBase = ReleaseBase
	}
	add(SourceReleaseBase, releaseBase)
	add(SourceGitHub, GitHubReleases)
	return out
}

// Tag returns the release tag of version: "v1.2.3" for "1.2.3" or "v1.2.3",
// and "" for the newest release ("" or "latest").
func Tag(version string) string {
	v := strings.TrimSpace(version)
	if v == "" || v == "latest" {
		return ""
	}
	return "v" + strings.TrimPrefix(v, "v")
}

// URL returns the download URL of file for version ("" = latest).
func (s Source) URL(version, file string) string {
	base := strings.TrimRight(s.BaseURL, "/")
	tag := Tag(version)
	if s.Name == SourceGitHub {
		if tag == "" {
			return base + "/latest/download/" + file
		}
		return base + "/download/" + tag + "/" + file
	}
	if tag == "" {
		tag = "latest"
	}
	return base + "/" + tag + "/" + file
}

// Archive returns the URL of the release archive for version and arch.
// version must be concrete; resolve "latest" first with ReleaseFromSums.
func (s Source) Archive(version, arch string) string {
	return s.URL(version, ArchiveName(version, arch))
}

// Sums returns the URL of SHA256SUMS for version ("" = latest).
func (s Source) Sums(version string) string { return s.URL(version, SumsFile) }

// SumsSig returns the URL of SHA256SUMS.minisig for version ("" = latest).
func (s Source) SumsSig(version string) string { return s.URL(version, SumsSigFile) }

// Manifest returns the URL of backends.yaml for version ("" = latest).
func (s Source) Manifest(version string) string { return s.URL(version, ManifestFile) }

// ManifestSig returns the URL of backends.yaml.minisig for version ("" = latest).
func (s Source) ManifestSig(version string) string { return s.URL(version, ManifestSigFile) }

// ArchiveName is the goreleaser archive name: deyroute_<ver>_linux_<arch>.tar.gz
// (the version without a leading "v").
func ArchiveName(version, arch string) string {
	return "deyroute_" + strings.TrimPrefix(strings.TrimSpace(version), "v") + "_linux_" + arch + ".tar.gz"
}

// URLs returns the URL of file for version from every source, in order.
func URLs(sources []Source, version, file string) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.URL(version, file))
	}
	return out
}
