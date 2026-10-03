package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/hub"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	"github.com/localroot4/deyroute/internal/exec"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// startRealHub runs the real hub daemon unprivileged in the env's root
// (the files a hub setup leaves, no systemd, no nftables) and points the
// CLI at its socket through the default dialer.
func startRealHub(t *testing.T, e *env) {
	t.Helper()
	dir := filepath.Join(e.root, config.SecretsDir)
	ca, err := tlsutil.NewCA("ir-1", time.Now())
	require.NoError(t, err)
	require.NoError(t, ca.Save(filepath.Join(dir, setup.FileCACert), filepath.Join(dir, setup.FileCAKey)))
	certPEM, keyPEM, err := ca.IssueServer("ir-1", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecretPair(filepath.Join(dir, setup.FileHubCert), certPEM, filepath.Join(dir, setup.FileHubKey), keyPEM))
	cfg := config.NewHub("ir-1", "127.0.0.1", 44433)
	require.NoError(t, config.SaveWith(filepath.Join(e.root, config.DefaultPath), cfg, validateOptions()))

	sockDir, err := os.MkdirTemp("", "dc")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	self := filepath.Join(e.root, "deyroute-self")
	require.NoError(t, os.WriteFile(self, []byte("#!deyroute test"), 0o755)) // #nosec G306 -- test binary
	runner := exec.NewFake()
	runner.Default = &exec.Response{}
	ready := make(chan struct{})
	o := hub.Options{
		Root: e.root, Runner: runner, Logger: dlog.Discard(), ControlListen: "127.0.0.1:0",
		SocketPath: filepath.Join(sockDir, "d.sock"), SelfBinary: self, Arch: "amd64", DisableFirewall: true,
		DisableStats: true, DisableTuning: true,
		Notify: func(string) error { return nil }, Getenv: func(string) string { return "" },
		OnReady: func(*hub.Hub) { close(ready) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- hub.Run(ctx, o) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("hub did not start: %v", err)
	case <-time.After(20 * time.Second):
		cancel()
		t.Fatal("hub did not start in time")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("hub did not stop")
		}
	})
	e.g.Socket = o.SocketPath
	e.g.Dial = nil
	e.g.ready = false
}

// The CLI against the real hub daemon over its unix socket.
func TestAgainstRealHub(t *testing.T) {
	e := newEnv(t)
	startRealHub(t, e)

	out := e.ok("status")
	require.Contains(t, out, "Hub: ir-1 (127.0.0.1)")
	require.Contains(t, out, "No tunnels yet")
	doc := e.json("status")
	require.Equal(t, "hub", doc["role"])

	out = e.ok("node", "join-command")
	require.Contains(t, out, "join 'dey://")
	require.Contains(t, out, "@127.0.0.1:44433#sha256:")
	link := e.json("node", "join-command", "--ttl", "5m")["link"].(string)
	require.True(t, strings.HasPrefix(link, "dey://"))

	require.Contains(t, e.ok("node", "list"), "No nodes yet")
	require.Contains(t, e.ok("tunnel", "list"), "No tunnels yet")
	e.json("events")
	// A node-only command is refused by the hub with DEY-X009.
	require.Contains(t, e.fail(2, "node", "set-hub", "1.2.3.4:44433"), "DEY-X009")
	e.fail(2, "logs", "node")
}
