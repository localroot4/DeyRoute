package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestSetHubAddr(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "etc/deyroute/config.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	fp := "sha256:" + stringOf('c', 64)
	require.NoError(t, config.SaveWith(p, config.NewNode("de-1", "5.6.7.8:44433", fp), validateOptions()))

	require.NoError(t, SetHubAddr(root, " 9.9.9.9:44500 "))
	cfg, err := config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9:44500", cfg.Node.HubAddr)
	require.Equal(t, fp, cfg.Node.HubCAFingerprint)
	require.Equal(t, os.FileMode(0o600), mode(t, p))

	require.NoError(t, SetHubAddr(root, "[2a01:4f8::1]:44433"))
	require.NoError(t, SetHubAddr(root, "hub.example.com:44433"))

	for _, bad := range []string{"", "9.9.9.9", "9.9.9.9:0", "9.9.9.9:65536", "2a01::1:44433", "a b:1"} {
		e := requireTop(t, SetHubAddr(root, bad), deyerr.C013)
		require.Contains(t, e.Message(), "node.hub_addr")
	}
	cfg, err = config.LoadWith(p, validateOptions())
	require.NoError(t, err)
	require.Equal(t, "hub.example.com:44433", cfg.Node.HubAddr, "invalid input changes nothing")

	// On a hub it is the wrong role; without config the file is missing.
	hubRoot := t.TempDir()
	_, err = SetupHub(ctxT(t), hubOpts(hubRoot, hubFake(), nil, nil))
	require.NoError(t, err)
	requireTop(t, SetHubAddr(hubRoot, "9.9.9.9:44433"), deyerr.X009)
	requireTop(t, SetHubAddr(t.TempDir(), "9.9.9.9:44433"), deyerr.C014)
}
