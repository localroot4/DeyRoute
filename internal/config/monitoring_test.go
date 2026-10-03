package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func boolPtr(b bool) *bool { return &b }

func TestValidateMonitoringAndAutoTuning(t *testing.T) {
	c13 := []deyerr.Code{deyerr.C013}
	runValidateCases(t, []vcase{
		{name: "monitoring defaults", mut: func(c *Config) { c.Monitoring = &Monitoring{} }},
		{name: "monitoring off", mut: func(c *Config) { c.Monitoring = &Monitoring{Enabled: boolPtr(false)} }},
		{name: "reset day 1", mut: func(c *Config) { c.Monitoring = &Monitoring{QuotaResetDay: 1} }},
		{name: "reset day 28", mut: func(c *Config) { c.Monitoring = &Monitoring{QuotaResetDay: 28} }},
		{name: "reset day 29", mut: func(c *Config) { c.Monitoring = &Monitoring{QuotaResetDay: 29} }, codes: c13, field: "monitoring.quota_reset_day"},
		{name: "reset day negative", mut: func(c *Config) { c.Monitoring = &Monitoring{QuotaResetDay: -1} }, codes: c13, field: "monitoring.quota_reset_day"},
		{name: "monitoring on a node", base: validNode, mut: func(c *Config) { c.Monitoring = &Monitoring{} }, codes: []deyerr.Code{deyerr.C016}},
		{name: "quota ok", mut: func(c *Config) { tun0(c).Advanced = &Advanced{MonthlyQuotaGiB: 500} }},
		{name: "quota negative", mut: func(c *Config) { tun0(c).Advanced = &Advanced{MonthlyQuotaGiB: -1} }, codes: c13, field: "tunnels[main].advanced.monthly_quota_gib"},
		{name: "quota huge", mut: func(c *Config) { tun0(c).Advanced = &Advanced{MonthlyQuotaGiB: MaxMonthlyQuotaGiB + 1} }, codes: c13, field: "tunnels[main].advanced.monthly_quota_gib"},
		{name: "profile auto", mut: func(c *Config) { c.Tuning.SysctlProfile = SysctlAuto; c.Tuning.NodesAuto = true }},
		{name: "hub tier large", mut: func(c *Config) { c.Tuning.BackendTier = BackendTierLarge }},
		{name: "hub tier bogus", mut: func(c *Config) { c.Tuning.BackendTier = "huge" }, codes: c13, field: "tuning.backend_tier"},
		{name: "node tier small", mut: func(c *Config) { c.Nodes[0].BackendTier = BackendTierSmall }},
		{name: "node tier bogus", mut: func(c *Config) { c.Nodes[0].BackendTier = "Small" }, codes: c13, field: "nodes[de-1].backend_tier"},
		{name: "wg_mtu 1280", mut: func(c *Config) { c.Tuning.WGMTU = MinWGMTU }},
		{name: "wg_mtu 1420", mut: func(c *Config) { c.Tuning.WGMTU = MaxWGMTU }},
		{name: "wg_mtu 1500", mut: func(c *Config) { c.Tuning.WGMTU = 1500 }, codes: c13, field: "tuning.wg_mtu"},
		{name: "wg_mtu 1279", mut: func(c *Config) { c.Tuning.WGMTU = 1279 }, codes: c13, field: "tuning.wg_mtu"},
	})
}

func TestMonitoringHelpers(t *testing.T) {
	var nilCfg *Config
	require.True(t, nilCfg.MonitoringEnabled())
	require.Equal(t, DefaultQuotaResetDay, nilCfg.QuotaResetDay())
	require.Empty(t, nilCfg.BackendTier(""))

	c := validHub()
	require.True(t, c.MonitoringEnabled(), "on without a monitoring: section")
	c.Monitoring = &Monitoring{}
	require.True(t, c.MonitoringEnabled(), "on without the key")
	require.Equal(t, 1, c.QuotaResetDay())
	c.Monitoring = &Monitoring{Enabled: boolPtr(false), QuotaResetDay: 15}
	require.False(t, c.MonitoringEnabled())
	require.Equal(t, 15, c.QuotaResetDay())

	require.Zero(t, tun0(c).QuotaBytes())
	tun0(c).Advanced = &Advanced{MonthlyQuotaGiB: 2}
	require.Equal(t, uint64(2<<30), tun0(c).QuotaBytes())

	c.Tuning.BackendTier = BackendTierLarge
	c.Nodes[0].BackendTier = BackendTierSmall
	require.Equal(t, BackendTierLarge, c.BackendTier(""))
	require.Equal(t, BackendTierSmall, c.BackendTier("de-1"))
	require.Empty(t, c.BackendTier("nl-1"))
	require.Empty(t, c.BackendTier("missing"))
}

func TestNewerKeysInUse(t *testing.T) {
	c := validHub()
	require.Empty(t, c.NewerKeysInUse(), "a config of an older release")
	c.Tuning.SysctlProfile = SysctlAggressive
	require.Empty(t, c.NewerKeysInUse())

	c.Nodes[0].BackendTier = BackendTierSmall
	c.Nodes[1].BackendTier = BackendTierSmall
	tun0(c).Advanced = &Advanced{MonthlyQuotaGiB: 1}
	c.Tuning = &Tuning{SysctlProfile: SysctlAuto, BBR: true, NodesAuto: true, BackendTier: BackendTierMedium, WGMTU: 1380}
	c.Monitoring = &Monitoring{}
	require.Equal(t, []string{
		"nodes[].backend_tier", "tunnels[].advanced.monthly_quota_gib", "tuning.sysctl_profile: auto",
		"tuning.nodes_auto", "tuning.backend_tier", "tuning.wg_mtu", "monitoring",
	}, c.NewerKeysInUse())
	var nilCfg *Config
	require.Nil(t, nilCfg.NewerKeysInUse())
}

// TestMonitoringRoundTrip: the new keys survive save and load, and a config
// without them saves without them (an older release can still read it).
func TestMonitoringRoundTrip(t *testing.T) {
	dir := t.TempDir()
	plain := validHub()
	path := filepath.Join(dir, "plain.yaml")
	require.NoError(t, SaveWith(path, plain, fakeOpts()))
	raw, err := os.ReadFile(path) // #nosec G304 -- test file
	require.NoError(t, err)
	for _, k := range []string{"monitoring", "nodes_auto", "backend_tier", "wg_mtu", "monthly_quota_gib"} {
		require.NotContains(t, string(raw), k)
	}
	back, err := LoadWith(path, fakeOpts())
	require.NoError(t, err)
	require.Nil(t, back.Monitoring)
	require.Equal(t, plain, back)

	c := validHub()
	c.Monitoring = &Monitoring{Enabled: boolPtr(false), QuotaResetDay: 10}
	c.Tuning = &Tuning{SysctlProfile: SysctlAuto, BBR: true, NodesAuto: true, BackendTier: BackendTierLarge, WGMTU: 1400}
	c.Nodes[1].BackendTier = BackendTierMedium
	tun0(c).Advanced = &Advanced{MonthlyQuotaGiB: 300}
	path = filepath.Join(dir, "full.yaml")
	require.NoError(t, SaveWith(path, c, fakeOpts()))
	back, err = LoadWith(path, fakeOpts())
	require.NoError(t, err)
	require.Equal(t, c, back)

	// Clone copies the monitoring section deeply.
	cl := Clone(c)
	*cl.Monitoring.Enabled = true
	cl.Monitoring.QuotaResetDay = 2
	require.False(t, *c.Monitoring.Enabled)
	require.Equal(t, 10, c.Monitoring.QuotaResetDay)
}
