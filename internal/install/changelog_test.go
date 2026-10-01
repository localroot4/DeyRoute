package install

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

const sampleChangelog = `# Changelog

All notable changes.

## [Unreleased]

### Added

- edge feature

## [1.2.0] - 2026-12-01

### Fixed

- the 1.2.0 fix

## [1.1.0] - 2026-11-01

- the 1.1.0 change

## [1.0.0] - 2026-10-01

- first release

[1.2.0]: https://example.invalid/compare/v1.1.0...v1.2.0
`

func TestChangelogNotes(t *testing.T) {
	md := []byte(sampleChangelog)
	// Every release after the running one up to the target, headings kept.
	notes := ChangelogNotes(md, "1.0.0", "1.2.0")
	require.True(t, len(notes) > 0)
	require.Contains(t, notes, "## [1.2.0] - 2026-12-01")
	require.Contains(t, notes, "- the 1.2.0 fix")
	require.Contains(t, notes, "## [1.1.0] - 2026-11-01")
	require.NotContains(t, notes, "first release")
	require.NotContains(t, notes, "edge feature")

	require.Equal(t, "## [1.1.0] - 2026-11-01\n\n- the 1.1.0 change", ChangelogNotes(md, "1.0.0", "1.1.0"))
	// An unparseable running version (a dev build) gets everything up to to.
	require.Contains(t, ChangelogNotes(md, "dev", "1.1.0"), "first release")
	// An edge build is made from [Unreleased].
	require.Equal(t, "## [Unreleased]\n\n### Added\n\n- edge feature", ChangelogNotes(md, "1.2.0", "1.2.1-edge.7"))
	// Nothing for a stable version the file does not know.
	require.Empty(t, ChangelogNotes(md, "1.2.0", "1.3.0"))
	require.Empty(t, ChangelogNotes([]byte("no headings"), "1.0.0", "1.0.1-edge.1"))

	require.Equal(t, "1.2.0", headingVersion("## [v1.2.0] - 2026-12-01"))
	require.Equal(t, "1.2.0", headingVersion("## 1.2.0 (2026-12-01)"))
	require.Equal(t, "", headingVersion("## "))
}

func TestFetchChangelog(t *testing.T) {
	ctx := context.Background()
	data := []byte(sampleChangelog)
	sums := map[string]string{"deyroute_1.2.0_linux_amd64.tar.gz": sha([]byte("archive")), ChangelogFile: sha(data)}
	srcs := []Source{{Name: SourceMirror, BaseURL: "https://mirror"}, {Name: SourceGitHub, BaseURL: "https://gh"}}
	m := newMapFetcher()
	// The mirror serves a tampered copy: skipped, GitHub's is used.
	m.files["https://mirror/v1.2.0/CHANGELOG.md"] = []byte("tampered")
	m.files["https://gh/download/v1.2.0/CHANGELOG.md"] = data
	got, err := FetchChangelog(ctx, m, srcs, "1.2.0", sums, RetryOptions{Tries: 1})
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Contains(t, m.shas, sha(data), "the expected sum reaches a via-node fetcher")

	// Only tampered copies: DEY-S001.
	m.files["https://gh/download/v1.2.0/CHANGELOG.md"] = []byte("tampered too")
	_, err = FetchChangelog(ctx, m, srcs, "1.2.0", sums, RetryOptions{Tries: 1})
	requireCode(t, err, deyerr.S001)

	// A release whose SHA256SUMS does not list it: DEY-I004, nothing fetched.
	before := m.count("https://gh/download/v1.1.0/CHANGELOG.md")
	_, err = FetchChangelog(ctx, m, srcs, "1.1.0", map[string]string{"x": sha(nil)}, RetryOptions{})
	requireCode(t, err, deyerr.I004)
	require.Equal(t, before, m.count("https://gh/download/v1.1.0/CHANGELOG.md"))

	_, err = FetchBytesVerified(ctx, m, []string{"https://gh/x"}, "not-a-sum", RetryOptions{})
	requireCode(t, err, deyerr.S001)
	_, err = FetchBytesVerified(ctx, nil, []string{"https://gh/x"}, sha(data), RetryOptions{})
	requireCode(t, err, deyerr.I004)
}
