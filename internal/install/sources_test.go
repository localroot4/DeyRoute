package install

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourcesOrder(t *testing.T) {
	got := Sources("https://flag.example/", "https://env.example", "")
	require.Equal(t, []Source{
		{Name: SourceMirror, BaseURL: "https://flag.example"},
		{Name: SourceReleaseBase, BaseURL: ReleaseBase},
		{Name: SourceGitHub, BaseURL: GitHubReleases},
	}, got)

	got = Sources("", "https://env.example", "https://cdn.example/")
	require.Equal(t, []Source{
		{Name: SourceMirror, BaseURL: "https://env.example"},
		{Name: SourceReleaseBase, BaseURL: "https://cdn.example"},
		{Name: SourceGitHub, BaseURL: GitHubReleases},
	}, got)

	// No mirror; a mirror equal to the release base is tried once.
	require.Len(t, Sources("", "", ""), 2)
	got = Sources(ReleaseBase+"/", "", "")
	require.Len(t, got, 2)
	require.Equal(t, SourceMirror, got[0].Name)
}

func TestSourceURLs(t *testing.T) {
	gh := Source{Name: SourceGitHub, BaseURL: GitHubReleases}
	mir := Source{Name: SourceMirror, BaseURL: "https://m.example/deyroute/"}

	require.Equal(t, GitHubReleases+"/latest/download/SHA256SUMS", gh.Sums(""))
	require.Equal(t, GitHubReleases+"/latest/download/SHA256SUMS.minisig", gh.SumsSig("latest"))
	require.Equal(t, GitHubReleases+"/download/v1.2.3/deyroute_1.2.3_linux_arm64.tar.gz", gh.Archive("v1.2.3", "arm64"))
	require.Equal(t, GitHubReleases+"/download/v1.2.3/backends.yaml", gh.Manifest("1.2.3"))
	require.Equal(t, GitHubReleases+"/download/v1.2.3/backends.yaml.minisig", gh.ManifestSig("1.2.3"))

	require.Equal(t, "https://m.example/deyroute/latest/SHA256SUMS", mir.Sums(""))
	require.Equal(t, "https://m.example/deyroute/v2.0.0/deyroute_2.0.0_linux_amd64.tar.gz", mir.Archive("2.0.0", "amd64"))

	require.Equal(t, "deyroute_1.0.0_linux_amd64.tar.gz", ArchiveName("v1.0.0", "amd64"))
	require.Equal(t, "", Tag("latest"))
	require.Equal(t, "v1.0.0", Tag("1.0.0"))

	urls := URLs(Sources("https://m.example", "", ""), "1.0.0", SumsFile)
	require.Equal(t, []string{
		"https://m.example/v1.0.0/SHA256SUMS",
		ReleaseBase + "/v1.0.0/SHA256SUMS",
		GitHubReleases + "/download/v1.0.0/SHA256SUMS",
	}, urls)
}

func TestSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		ok   bool
	}{
		{"1.0.0", "1.0.0", 0, true},
		{"v1.0.1", "1.0.0", 1, true},
		{"1.2.0", "1.10.0", -1, true},
		{"2.0.0", "1.99.99", 1, true},
		{"1.0.0-rc.1", "1.0.0", -1, true},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1, true},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1, true},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1, true},
		{"1.0.0-rc.1", "1.0.0-beta", 1, true},
		{"1.0.0+build.5", "1.0.0", 0, true},
		{"v1.41", "v1.40", 1, true},
		{"app/v2.12.3", "app/v2.12.10", -1, true},
		{"dev", "1.0.0", 0, false},
		{"1", "1.0.0", 0, false},
		{"1.x.0", "1.0.0", 0, false},
		{"1.0.0-", "1.0.0", 0, false},
		{"1.0.0-a..b", "1.0.0", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		require.Equal(t, c.ok, ok, "%s vs %s", c.a, c.b)
		if ok {
			require.Equal(t, c.want, got, "%s vs %s", c.a, c.b)
			back, _ := CompareVersions(c.b, c.a)
			require.Equal(t, -c.want, back, "%s vs %s (reversed)", c.b, c.a)
		}
	}
	require.True(t, NewerThan("1.0.1", "1.0.0"))
	require.False(t, NewerThan("1.0.0", "1.0.0"))
	require.False(t, NewerThan("1.0.0-rc.1", "1.0.0"))
	require.True(t, NewerThan("1.0.0", "dev"))
	require.False(t, NewerThan("dev", "1.0.0"))
	require.False(t, NewerThan("dev", "dev"))
}
