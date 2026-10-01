package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/install"
)

// withChangelog publishes CHANGELOG.md next to release ver of rs and lists
// it in the signed SHA256SUMS, as the release workflows do.
func (rs *releaseServer) withChangelog(t *testing.T, ver string, md []byte) {
	t.Helper()
	sum := sha256.Sum256(md)
	for _, dir := range []string{"/latest/", "/v" + ver + "/"} {
		rs.mu.Lock()
		sums := append([]byte(nil), rs.files[dir+install.SumsFile]...)
		rs.mu.Unlock()
		sums = append(sums, []byte(hex.EncodeToString(sum[:])+"  "+install.ChangelogFile+"\n")...)
		rs.put(dir+install.SumsFile, sums)
		rs.put(dir+install.SumsSigFile, rs.sign(sums))
		rs.put(dir+install.ChangelogFile, md)
	}
}

// `deyroute update --check` shows the changelog text of the release (section
// 5), not only a URL that may not open from Iran; a tampered copy is never
// shown, and long notes end with the release page.
func TestUpdateCheckShowsChangelogText(t *testing.T) {
	rs := newReleaseServer(t)
	rs.release(t, "9.1.0", []byte("#!deyroute 9.1.0"), false)
	md := "# Changelog\n\n## [Unreleased]\n\n- not yet\n\n## [9.1.0] - 2026-12-01\n\n### Fixed\n\n- the node list scrolls\n\n## [0.0.1] - 2020-01-01\n\n- ancient\n"
	rs.withChangelog(t, "9.1.0", []byte(md))
	env, o := prepareEnv(t, nil, releaseOpts(rs))
	bin := filepath.Join(env.root, config.BinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!deyroute old"), 0o755)) // #nosec G306 -- test binary
	env.startEnv(o)
	ctx := ctxT(t)

	info, err := env.client.UpdateCheck(ctx)
	require.NoError(t, err)
	require.True(t, info.Available)
	// The test binary runs version "dev": every release up to 9.1.0, never
	// [Unreleased].
	require.Equal(t, "## [9.1.0] - 2026-12-01\n\n### Fixed\n\n- the node list scrolls\n\n## [0.0.1] - 2020-01-01\n\n- ancient", info.Changelog)

	// A copy that does not match the signed SHA256SUMS: the release page.
	rs.put("/latest/"+install.ChangelogFile, []byte(md+"- injected\n"))
	rs.put("/v9.1.0/"+install.ChangelogFile, []byte(md+"- injected\n"))
	info, err = env.client.UpdateCheck(ctx)
	require.NoError(t, err)
	require.Equal(t, install.GitHubReleases+"/tag/v9.1.0", info.Changelog)

	// Long notes are cut, the release page names the rest.
	var b strings.Builder
	b.WriteString("## [9.1.0]\n")
	for i := range 60 {
		fmt.Fprintf(&b, "- change %d\n", i)
	}
	rs.release(t, "9.1.0", []byte("#!deyroute 9.1.0"), false)
	rs.withChangelog(t, "9.1.0", []byte(b.String()))
	info, err = env.client.UpdateCheck(ctx)
	require.NoError(t, err)
	lines := strings.Split(info.Changelog, "\n")
	require.Len(t, lines, changelogMaxLines+1)
	require.Equal(t, "- change 38", lines[changelogMaxLines-1])
	require.Equal(t, "… "+install.GitHubReleases+"/tag/v9.1.0", lines[changelogMaxLines])
}
