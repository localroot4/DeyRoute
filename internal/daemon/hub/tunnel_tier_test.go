package hub

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
)

// TestPlanInputBackendTiers: the renderer reads the sticky backend tiers
// and tuning.wg_mtu from the config (never live facts), so the rendered
// files change only when the config does.
func TestPlanInputBackendTiers(t *testing.T) {
	env := startHub(t, func(c *config.Config) {
		c.Tuning.BackendTier = config.BackendTierLarge
		c.Tuning.WGMTU = 1380
		c.Nodes = append(c.Nodes,
			config.Node{ID: "de-1", Name: "de-1", PublicIP: "203.0.113.10", BackendTier: config.BackendTierSmall},
			config.Node{ID: "nl-1", Name: "nl-1", PublicIP: "203.0.113.11"},
		)
	})
	cfg := env.h.Config()
	in := env.h.planInput(cfg, config.Tunnel{ID: "main", Nodes: []string{"de-1", "nl-1"}})
	require.Equal(t, config.BackendTierLarge, in.HubTier)
	require.Equal(t, 1380, in.WGMTU)
	require.NotNil(t, in.NodeTier)
	require.Equal(t, config.BackendTierSmall, in.NodeTier("de-1"))
	require.Empty(t, in.NodeTier("nl-1"))
	require.Empty(t, in.NodeTier("unknown"))

	bare := config.Clone(cfg)
	bare.Tuning = nil
	in = env.h.planInput(bare, config.Tunnel{ID: "main"})
	require.Empty(t, in.HubTier)
	require.Zero(t, in.WGMTU)
}
