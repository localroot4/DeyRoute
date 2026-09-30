package setup

import (
	"regexp"
	"strings"

	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/version"
)

// releaseVersionRe accepts the characters of a SemVer release, so the
// version can be appended to a shell command unquoted.
var releaseVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?([-+][0-9A-Za-z.+-]+)?$`)

// shellSafeRe matches words bash reads literally when unquoted (no
// metacharacters, globs, quotes, expansions or whitespace).
var shellSafeRe = regexp.MustCompile(`^[A-Za-z0-9._~:/%+,@=-]+$`)

// JoinCommand returns the one-line join command of spec section 3:
//
//	bash <(curl -fsSL <installerURL>) join '<link>'
//
// When ver is a real release (not "dev" or empty) " --version <ver>" is
// appended, so the node installs the hub's release (the hub still pushes
// updates later). The link is single-quoted for the shell. The installer
// URL is written as is when it only contains characters the shell takes
// literally (every normal https URL); otherwise (a mirror with "&", "?",
// "$", spaces, quotes …) it is single-quoted too, so the owner pastes one
// command that neither breaks nor runs anything else as root.
func JoinCommand(installerURL, link, ver string) string {
	u := installerURL
	if !shellSafeRe.MatchString(u) {
		u = shellQuote(u)
	}
	cmd := "bash <(curl -fsSL " + u + ") join " + shellQuote(link)
	if v, ok := releaseVersion(ver); ok {
		cmd += " --version " + v
	}
	return cmd
}

// releaseVersion returns ver without a leading "v" when it names a
// release.
func releaseVersion(ver string) (string, bool) {
	v := strings.TrimPrefix(strings.TrimSpace(ver), "v")
	if v == "" || v == "dev" || !releaseVersionRe.MatchString(v) {
		return "", false
	}
	if _, _, ok := version.MajorMinor(v); !ok {
		return "", false
	}
	return v, true
}

// shellQuote wraps s in single quotes; a single quote inside s is closed,
// escaped with a backslash and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// InstallerURL returns the URL of install.sh: <mirror>/latest/install.sh
// when mirror (or the build-time install.ReleaseBase) is set, otherwise
// https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh
// (QUESTIONS.md B).
func InstallerURL(mirror string) string {
	base := strings.TrimSpace(mirror)
	if base == "" {
		base = strings.TrimSpace(install.ReleaseBase)
	}
	if base != "" {
		return install.Source{Name: install.SourceMirror, BaseURL: base}.URL("", install.InstallerFile)
	}
	return install.Source{Name: install.SourceGitHub, BaseURL: install.GitHubReleases}.URL("", install.InstallerFile)
}
