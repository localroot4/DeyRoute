package wireguard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	dexec "github.com/localroot4/deyroute/internal/exec"
)

// TestE2EKernel creates, configures and removes a real kernel WireGuard
// interface. It needs root with CAP_NET_ADMIN, the wireguard module and
// DEYROUTE_WG_E2E=1.
func TestE2EKernel(t *testing.T) {
	if os.Getenv("DEYROUTE_WG_E2E") != "1" || os.Geteuid() != 0 {
		t.Skip("set DEYROUTE_WG_E2E=1 and run as root to create a real interface")
	}
	in := fixture(t, false)
	in.Tunnel.ID = "e2etest"
	in.NetIndex = 250
	in.Tunnel.Ports = []config.PortMap{{Listen: 18443, Proto: "tcp", Target: "1.2.3.4:18443"}}
	r, err := New().Render(in, backend.SideHub)
	require.NoError(t, err)
	cfg := filepath.Join(t.TempDir(), ConfigFile)
	require.NoError(t, os.WriteFile(cfg, r.Files[ConfigFile], 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run := dexec.NewRunner()
	require.NoError(t, Up(ctx, cfg, run))
	t.Cleanup(func() { _ = Down(context.Background(), cfg, run) })
	out, _, err := run.Run(ctx, "ip", []string{"-o", "address", "show", "dev", "dey-e2etest"}, nil)
	require.NoError(t, err)
	require.Contains(t, string(out), "10.77.250.1/30")
	// Up again replaces the interface instead of failing.
	require.NoError(t, Up(ctx, cfg, run))
	require.NoError(t, Down(ctx, cfg, run))
	_, _, err = run.Run(ctx, "ip", []string{"-o", "link", "show", "dev", "dey-e2etest"}, nil)
	require.Error(t, err, "interface must be gone after Down")
}
