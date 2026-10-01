package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ChangelogFile is the release's CHANGELOG.md. A release publishes it next
// to SHA256SUMS, which lists its checksum, so the notes shown before an
// update (section 5) are verified like the archive.
const ChangelogFile = "CHANGELOG.md"

// ChangelogMaxBytes caps the CHANGELOG.md download.
const ChangelogMaxBytes int64 = 512 << 10

// FetchChangelog downloads CHANGELOG.md of version from sources, in order
// (the caller puts the source that served SHA256SUMS first), and checks it
// against sums, the parsed SHA256SUMS whose signature the caller verified.
// A release whose SHA256SUMS does not list it (built before it was
// published) is DEY-I004; a source serving other bytes is skipped, and
// DEY-S001 when no source serves the listed ones.
func FetchChangelog(ctx context.Context, f Fetcher, sources []Source, version string, sums map[string]string, ro RetryOptions) ([]byte, error) {
	want := ""
	for name, sum := range sums {
		if path.Base(name) == ChangelogFile {
			want = sum
		}
	}
	if want == "" {
		return nil, deyerr.New(deyerr.I004, deyerr.Params{"file": ChangelogFile}).
			WithWhy("the release's SHA256SUMS does not list " + ChangelogFile)
	}
	ro.File = ChangelogFile
	if ro.MaxBytes <= 0 {
		ro.MaxBytes = ChangelogMaxBytes
	}
	return FetchBytesVerified(ctx, f, URLs(sources, version, ChangelogFile), want, ro)
}

// FetchBytesVerified is FetchBytes for a small file whose sha256 is known
// from a signed SHA256SUMS: a URL serving other bytes is skipped like a
// failed one, and opt.MismatchCode (DEY-S001) is returned when no URL
// serves the expected content. The expected sum is passed on to a via-node
// fetcher (WithExpectedSHA256).
func FetchBytesVerified(ctx context.Context, f Fetcher, urls []string, sha256hex string, opt RetryOptions) ([]byte, error) {
	o := opt.withDefaults()
	file := o.File
	if file == "" && len(urls) > 0 {
		file = baseOfURL(urls[0])
	}
	want := strings.ToLower(strings.TrimSpace(sha256hex))
	if !validSHA256(want) {
		return nil, deyerr.New(o.MismatchCode, o.params(file)).
			WithWhy("no valid expected sha256 was supplied; unverified files are never used")
	}
	if f == nil {
		return nil, deyerr.New(o.FailCode, o.params(file)).WithDetail("no fetcher configured")
	}
	var buf limitedBuffer
	_, err := retryURLs(WithExpectedSHA256(ctx, want), urls, o, file, func(actx context.Context, u string) error {
		buf = limitedBuffer{max: o.MaxBytes}
		if err := f.Fetch(actx, u, &buf); err != nil {
			return err
		}
		sum := sha256.Sum256(buf.b)
		if got := hex.EncodeToString(sum[:]); got != want {
			return &errMismatch{want: want, got: got, url: RedactURL(u)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return buf.b, nil
}

// ChangelogNotes cuts what changed after version from up to version to out
// of a Keep a Changelog file: every "## [x.y.z]" section with
// from < x.y.z <= to, in file order, headings included. An edge build (to
// with a pre-release part such as 1.0.1-edge.7) is made from the
// "## [Unreleased]" section, which is returned when no numbered section
// matches. The result is "" when nothing matches.
func ChangelogNotes(md []byte, from, to string) string {
	type section struct {
		ver   string
		lines []string
	}
	var secs []section
	for _, line := range strings.Split(strings.ReplaceAll(string(md), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "## ") {
			secs = append(secs, section{ver: headingVersion(line)})
		}
		if len(secs) > 0 {
			secs[len(secs)-1].lines = append(secs[len(secs)-1].lines, line)
		}
	}
	var out []string
	var unreleased []string
	for _, s := range secs {
		if strings.EqualFold(s.ver, "unreleased") {
			unreleased = s.lines
			continue
		}
		c, ok := CompareVersions(s.ver, to)
		if !ok || c > 0 || (from != "" && !NewerThan(s.ver, from)) {
			continue
		}
		out = append(out, s.lines...)
	}
	if len(out) == 0 {
		if _, isPre := preRelease(to); !isPre {
			return ""
		}
		out = unreleased
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// headingVersion is the version of a "## [1.2.0] - 2026-10-01" heading
// ("1.2.0"; "Unreleased" for "## [Unreleased]").
func headingVersion(line string) string {
	h := strings.TrimSpace(strings.TrimPrefix(line, "## "))
	if strings.HasPrefix(h, "[") {
		if i := strings.Index(h, "]"); i > 0 {
			return strings.TrimPrefix(strings.TrimSpace(h[1:i]), "v")
		}
	}
	if f := strings.Fields(h); len(f) > 0 {
		return strings.TrimPrefix(f[0], "v")
	}
	return ""
}

// preRelease reports whether v parses as a SemVer pre-release
// ("1.0.1-edge.7"), returning its pre-release part.
func preRelease(v string) (string, bool) {
	s, ok := parseSemver(v)
	if !ok || len(s.pre) == 0 {
		return "", false
	}
	return strings.Join(s.pre, "."), true
}
