package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

const hubTargetSecret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"

// nodeRoot is a joined node: config.yaml and an empty secrets directory.
func nodeRoot(t *testing.T) (root, cfgPath string) {
	t.Helper()
	root = t.TempDir()
	cfgPath = filepath.Join(root, "etc/deyroute/config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(cfgPath), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, config.SecretsDir), 0o700))
	require.NoError(t, config.SaveWith(cfgPath, config.NewNode("de-1", "5.6.7.8:44433", "sha256:"+stringOf('c', 64)), validateOptions()))
	return root, cfgPath
}

func TestSetHubAddrFrontTargetSwitchesToFrontMode(t *testing.T) {
	root, p := nodeRoot(t)
	secretPath := filepath.Join(root, config.SecretsDir, config.DefaultFrontSecretFile)

	// 2053 is a Cloudflare HTTPS port: the scheme is derived and not stored.
	require.NoError(t, SetHubAddr(root, " wss://front.example.com:2053/"+hubTargetSecret+" "))
	cfg, err := config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Equal(t, "front.example.com:2053", cfg.Node.HubAddr)
	require.Equal(t, config.NodeFront{SecretFile: config.DefaultFrontSecretFile}, cfg.Node.Front)
	require.Equal(t, "sha256:"+stringOf('c', 64), cfg.Node.HubCAFingerprint, "the CA pin is kept")
	require.Equal(t, os.FileMode(0o600), mode(t, secretPath))
	b, err := os.ReadFile(secretPath)
	require.NoError(t, err)
	require.Equal(t, hubTargetSecret, strings.TrimSpace(string(b)))
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	require.NotContains(t, string(raw), hubTargetSecret)

	// The edge address the owner configured survives a new front target; a
	// non-default scheme is stored.
	cfg.Node.Front.EdgeIP = "104.16.0.1"
	require.NoError(t, config.SaveWith(p, cfg, validateOptions()))
	require.NoError(t, SetHubAddr(root, "ws://front2.example.com:443/"+hubTargetSecret+"B"))
	cfg, err = config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Equal(t, "front2.example.com:443", cfg.Node.HubAddr)
	require.Equal(t, config.NodeFront{SecretFile: config.DefaultFrontSecretFile, Scheme: config.FrontSchemeWS, EdgeIP: "104.16.0.1"}, cfg.Node.Front)
	b, err = os.ReadFile(secretPath)
	require.NoError(t, err)
	require.Equal(t, hubTargetSecret+"B", strings.TrimSpace(string(b)), "the new secret replaces the old one")

	// A plain-HTTP Cloudflare port with ws:// needs no stored scheme.
	require.NoError(t, SetHubAddr(root, "ws://front2.example.com:2052/"+hubTargetSecret))
	cfg, err = config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Empty(t, cfg.Node.Front.Scheme)
}

func TestSetHubAddrPlainClearsFrontMode(t *testing.T) {
	root, p := nodeRoot(t)
	secretPath := filepath.Join(root, config.SecretsDir, config.DefaultFrontSecretFile)
	require.NoError(t, SetHubAddr(root, "wss://front.example.com:2053/"+hubTargetSecret))
	require.FileExists(t, secretPath)

	require.NoError(t, SetHubAddr(root, "9.9.9.9:44500"))
	cfg, err := config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9:44500", cfg.Node.HubAddr)
	require.Equal(t, config.NodeFront{}, cfg.Node.Front, "front mode is cleared explicitly")
	require.False(t, cfg.Node.FrontMode())
	require.NoFileExists(t, secretPath, "the unused secret is removed")

	// A custom secret file name of an older config is removed, too.
	cfg.Node.Front = config.NodeFront{SecretFile: "old.secret"}
	cfg.Node.HubAddr = "front.example.com:2053"
	require.NoError(t, config.SaveWith(p, cfg, validateOptions()))
	old := filepath.Join(root, config.SecretsDir, "old.secret")
	require.NoError(t, os.WriteFile(old, []byte(hubTargetSecret), 0o600))
	require.NoError(t, SetHubAddr(root, "wss://front3.example.com:8443/"+hubTargetSecret))
	require.NoFileExists(t, old)
	cfg, err = config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.DefaultFrontSecretFile, cfg.Node.Front.SecretFile)
}

func TestSetHubAddrBadFrontTargetChangesNothing(t *testing.T) {
	root, p := nodeRoot(t)
	before, err := os.ReadFile(p)
	require.NoError(t, err)
	for _, bad := range []string{
		"wss://front.example.com/" + hubTargetSecret,                // no port
		"wss://front.example.com:2053",                              // no path
		"wss://front.example.com:2053/",                             // empty secret
		"wss://front.example.com:2053/short",                        // too short
		"wss://front.example.com:2053/" + hubTargetSecret + "/x",    // second segment
		"wss://front.example.com:2053/" + hubTargetSecret + "?x=1",  // query
		"wss://front.example.com:2053/" + hubTargetSecret + "#frag", // fragment
		"wss://front.example.com:99999/" + hubTargetSecret,          // port out of range
		"wss://:2053/" + hubTargetSecret,                            // no host
		"wss://front.example.com:2053/bad secret bad secret bad",    // not the secret alphabet
		"https://front.example.com:2053/" + hubTargetSecret,
		"front.example.com:2053/" + hubTargetSecret,
	} {
		err := SetHubAddr(root, bad)
		e := requireTop(t, err, deyerr.C013)
		require.NotContains(t, err.Error()+e.Message()+e.Detail, hubTargetSecret, bad)
	}
	after, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	require.NoFileExists(t, filepath.Join(root, config.SecretsDir, config.DefaultFrontSecretFile))
}

func TestSetHubAddrFrontOnAHubWritesNoSecret(t *testing.T) {
	hubRoot := t.TempDir()
	_, err := SetupHub(ctxT(t), hubOpts(hubRoot, hubFake(), nil, nil))
	require.NoError(t, err)
	requireTop(t, SetHubAddr(hubRoot, "wss://front.example.com:2053/"+hubTargetSecret), deyerr.X009)
	require.NoFileExists(t, filepath.Join(hubRoot, config.SecretsDir, config.DefaultFrontSecretFile))
}

func TestParseHubTarget(t *testing.T) {
	for _, tc := range []struct {
		in     string
		want   HubTarget
		stored string
	}{
		{in: "5.6.7.8:44433", want: HubTarget{Addr: "5.6.7.8:44433"}},
		{in: "[2a01:4f8::1]:44433", want: HubTarget{Addr: "[2a01:4f8::1]:44433"}},
		{in: "hub.example.com:44433", want: HubTarget{Addr: "hub.example.com:44433"}},
		{in: "wss://front.example.com:2053/" + hubTargetSecret,
			want: HubTarget{Addr: "front.example.com:2053", Front: true, Secret: hubTargetSecret, Scheme: "wss"}},
		{in: "WSS://Front.Example.com:8443/" + hubTargetSecret,
			want: HubTarget{Addr: "Front.Example.com:8443", Front: true, Secret: hubTargetSecret, Scheme: "wss"}},
		{in: "ws://front.example.com:2052/" + hubTargetSecret,
			want: HubTarget{Addr: "front.example.com:2052", Front: true, Secret: hubTargetSecret, Scheme: "ws"}},
		{in: "ws://front.example.com:443/" + hubTargetSecret, stored: "ws",
			want: HubTarget{Addr: "front.example.com:443", Front: true, Secret: hubTargetSecret, Scheme: "ws"}},
		{in: "wss://front.example.com:9999/" + hubTargetSecret, stored: "wss",
			want: HubTarget{Addr: "front.example.com:9999", Front: true, Secret: hubTargetSecret, Scheme: "wss"}},
		{in: "wss://1.2.3.4:443/" + hubTargetSecret,
			want: HubTarget{Addr: "1.2.3.4:443", Front: true, Secret: hubTargetSecret, Scheme: "wss"}},
	} {
		got, err := ParseHubTarget(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
		require.Equal(t, tc.stored, got.StoredScheme(), tc.in)
	}
	for _, bad := range []string{"", "9.9.9.9", "wss://", "ws://x", "wss://front.example.com:2053/a"} {
		_, err := ParseHubTarget(bad)
		requireTop(t, err, deyerr.C013)
	}
}

func TestRedactHubTarget(t *testing.T) {
	require.Equal(t, "5.6.7.8:44433", RedactHubTarget("5.6.7.8:44433"))
	require.Equal(t, "wss://front.example.com:2053/***", RedactHubTarget("wss://front.example.com:2053/"+hubTargetSecret))
	require.Equal(t, "ws://front.example.com:80/***", RedactHubTarget("ws://front.example.com:80/"+hubTargetSecret+"?x"))
	require.Equal(t, "wss://front.example.com/***", RedactHubTarget("wss://front.example.com/"+hubTargetSecret+"/more"))
	require.Equal(t, "wss://nopath:1/***", RedactHubTarget("wss://nopath:1"))
}
