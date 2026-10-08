package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
)

const cliFrontSecret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"

// front enable writes hub.front, applies it and prints the Cloudflare steps
// and the command for the nodes; status shows the same; disable turns it off
// and says how front nodes go back to the direct address.
func TestFrontEnableStatusDisable(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	applied := 0
	e.stub.ConfigApplyFn = func(context.Context, func(api.Step)) (api.ApplyResult, error) {
		applied++
		// The hub creates the path secret when the front starts.
		p := filepath.Join(e.root, config.SecretsDir, config.DefaultFrontSecretFile)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(cliFrontSecret+"\n"), 0o600))
		return api.ApplyResult{}, nil
	}
	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Hub: &api.HubStatus{Front: &api.FrontStatus{Enabled: true, Domain: "t1.example.com", Port: 2053, Listening: true}}}, nil
	}

	out := e.ok("front", "enable", "--domain", "T1.Example.com")
	require.Equal(t, 1, applied)
	cfg, err := config.Load(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.Equal(t, config.HubFront{Enabled: true, Domain: "t1.example.com", Port: 2053}, stripFront(cfg.Hub.Front))
	for _, want := range []string{
		"Cloudflare front: on · t1.example.com:2053 · listening · tls auto",
		"t1.example.com -> 5.6.7.8",
		"mode Full",
		"WebSockets on",
		"deyroute node set-hub 'wss://t1.example.com:2053/" + cliFrontSecret + "'",
	} {
		require.Contains(t, out, want)
	}
	require.Contains(t, e.ok("front", "status"), "set-hub 'wss://t1.example.com:2053/")
	doc := e.json("front", "status")
	require.Equal(t, "wss://t1.example.com:2053/"+cliFrontSecret, doc["node_target"])

	// A plain-HTTP Cloudflare port gets tls off and a ws:// target.
	e.ok("front", "enable", "--domain", "t1.example.com", "--port", "8080")
	cfg, err = config.Load(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.Equal(t, config.FrontTLSOff, cfg.Hub.Front.TLS)
	require.Contains(t, e.ok("front", "status"), "set-hub 'ws://t1.example.com:8080/")
	require.Contains(t, e.ok("front", "status"), "mode Flexible")

	// Not a Cloudflare port: refused before anything is written.
	before, _ := os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	e.fail(1, "front", "enable", "--domain", "t1.example.com", "--port", "9999")
	after, _ := os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.Equal(t, string(before), string(after))
	require.Contains(t, e.fail(1, "front", "enable"), "--domain")

	e.ok("front", "disable")
	cfg, err = config.Load(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.False(t, cfg.Hub.Front.Enabled)
	require.Contains(t, e.ok("front", "status"), "Cloudflare front: off")
}

func stripFront(f config.HubFront) config.HubFront {
	f.SecretFile, f.CFOnly = "", nil
	return f
}
