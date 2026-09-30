package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestSideDirectionStrings(t *testing.T) {
	require.Equal(t, "hub", SideHub.String())
	require.Equal(t, "node", SideNode.String())
	require.Equal(t, "reverse", Reverse.String())
	require.Equal(t, "forward", Forward.String())
}

func TestTransportSupports(t *testing.T) {
	tr := Transport{Backend: "x", Name: "y", Protos: []string{"tcp"}}
	require.True(t, tr.Supports("tcp"))
	require.False(t, tr.Supports("udp"))
	require.Equal(t, "x/y", tr.ID())
}

func TestRenderInputHelpers(t *testing.T) {
	in := RenderInput{Tunnel: config.Tunnel{Ports: []config.PortMap{
		{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"},
		{Listen: 27015, Proto: "udp", Target: "127.0.0.1:27015"},
		{Listen: 2053, Proto: "tcp", Target: "127.0.0.1:2053"},
	}}}
	require.Equal(t, "0.0.0.0", in.ListenAddrOrDefault())
	in.ListenAddr = "::"
	require.Equal(t, "::", in.ListenAddrOrDefault())
	tcp := in.PortsFor("tcp")
	require.Len(t, tcp, 2)
	require.Equal(t, 443, tcp[0].Listen)
	require.Equal(t, 2053, tcp[1].Listen)
	require.Len(t, in.PortsFor("udp"), 1)
}

func TestManifestEntryBinary(t *testing.T) {
	require.Equal(t, "frps", ManifestEntry{Name: "frp", Binaries: []string{"frps", "frpc"}}.Binary())
	require.Equal(t, "xray", ManifestEntry{Name: "xray"}.Binary())
}

func TestRegistryListings(t *testing.T) {
	Register(fakeBackend{"zz-list"})
	names := map[string]bool{}
	for _, b := range All() {
		names[b.Name()] = true
	}
	require.True(t, names["zz-list"])
	all := AllTransports()
	for i := 1; i < len(all); i++ {
		require.LessOrEqual(t, all[i-1].ID(), all[i].ID(), "AllTransports must be sorted")
	}
	require.Contains(t, ValidIDs(), "zz-list/tcp")
}

func TestManifestOverride(t *testing.T) {
	orig := active()
	t.Cleanup(func() { SetManifest(orig) })

	dir := t.TempDir()
	missing := filepath.Join(dir, "none.yaml")
	require.NoError(t, LoadManifestOverride(missing), "a missing override is not an error")

	good := filepath.Join(dir, "backends.yaml")
	require.NoError(t, os.WriteFile(good, []byte("manifest_version: 1\nbackends:\n  backhaul:\n    version: v9.9.9\n    template_version: 2\n"), 0o600))
	require.NoError(t, LoadManifestOverride(good))
	require.Equal(t, "v9.9.9", ManifestFor("backhaul").Version)
	require.Equal(t, "backhaul", ManifestFor("backhaul").Name)
	unknown := ManifestFor("nope")
	require.Equal(t, "nope", unknown.Name)
	require.Empty(t, unknown.Version)

	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("manifest_version: 1\nunknown_key: true\n"), 0o600))
	err := LoadManifestOverride(bad)
	require.True(t, deyerr.HasCode(err, deyerr.S005), "strict decoding must reject unknown keys: %v", err)
	require.Equal(t, "v9.9.9", ManifestFor("backhaul").Version, "a bad override must not replace the active manifest")

	require.Error(t, LoadManifestOverride(dir), "a directory cannot be read as a manifest")
}

func TestResolveURL(t *testing.T) {
	require.Equal(t, "https://m.example/backends/x", ResolveURL("{mirror}/backends/x", "https://m.example/"))
	require.Equal(t, "https://a/b", ResolveURL("https://a/b", "https://m"))
}

func TestRenderHelpers(t *testing.T) {
	require.Equal(t, `"a\tb\u0001"`, TOMLString("a\tb\x01"))
	require.Equal(t, `"x\r"`, TOMLString("x\r"))
	require.Equal(t, `["a", "b\"c"]`, TOMLStringArray([]string{"a", `b"c`}))
	require.Equal(t, `[]`, TOMLStringArray(nil))

	out, err := JSONIndent(map[string]any{"b": 1, "a": "<x>"})
	require.NoError(t, err)
	require.Equal(t, "{\n  \"a\": \"<x>\",\n  \"b\": 1\n}\n", string(out), "sorted keys, no HTML escaping")
	_, err = JSONIndent(func() {})
	require.Error(t, err)

	require.Equal(t, []string{"a", "b", "c"}, SortedKeys(map[string]int{"c": 3, "a": 1, "b": 2}))
	require.Equal(t, "1.2.3.4:80", HostPort("1.2.3.4", 80))
	require.Equal(t, "[::1]:80", HostPort("[::1]", 80)[0:0]+HostPort("::1", 80))
	require.Equal(t, "tcp-443", ServiceName("tcp", 443))
	require.Equal(t, "udp-27015", ServiceName("udp", 27015))

	h, p, ok := SplitTarget("[::1]:8443")
	require.True(t, ok)
	require.Equal(t, "::1", h)
	require.Equal(t, 8443, p)
	for _, bad := range []string{"", "nohost", ":80", "h:", "h:x", "h:70000"} {
		_, _, ok := SplitTarget(bad)
		require.False(t, ok, bad)
	}
	require.True(t, strings.HasPrefix(TOMLString("é"), `"é`))
}
