package config

import "slices"

// BackendTiers are the values of tuning.backend_tier and
// nodes[].backend_tier, smallest first.
var BackendTiers = []string{BackendTierSmall, BackendTierMedium, BackendTierLarge}

// MonitoringEnabled reports whether the hub counts traffic
// (monitoring.enabled; true when the section or the key is missing).
func (c *Config) MonitoringEnabled() bool {
	if c == nil || c.Monitoring == nil || c.Monitoring.Enabled == nil {
		return true
	}
	return *c.Monitoring.Enabled
}

// QuotaResetDay returns monitoring.quota_reset_day, or DefaultQuotaResetDay
// (1) when it is not set.
func (c *Config) QuotaResetDay() int {
	if c == nil || c.Monitoring == nil || c.Monitoring.QuotaResetDay <= 0 {
		return DefaultQuotaResetDay
	}
	return c.Monitoring.QuotaResetDay
}

// QuotaBytes returns advanced.monthly_quota_gib in bytes (0 = no quota).
func (t *Tunnel) QuotaBytes() uint64 {
	if t == nil || t.Advanced == nil || t.Advanced.MonthlyQuotaGiB <= 0 {
		return 0
	}
	return uint64(t.Advanced.MonthlyQuotaGiB) << 30 // #nosec G115 -- positive, checked above
}

// BackendTier returns the sticky backend tier of a host: the hub's
// (tuning.backend_tier) for node "", otherwise nodes[].backend_tier of that
// node. "" means the renderer's defaults.
func (c *Config) BackendTier(node string) string {
	if c == nil {
		return ""
	}
	if node == "" {
		if c.Tuning == nil {
			return ""
		}
		return c.Tuning.BackendTier
	}
	if n, ok := c.NodeByID(node); ok {
		return n.BackendTier
	}
	return ""
}

// NewerKeysInUse lists the config.yaml keys that releases before traffic
// monitoring and automatic tuning do not know, in the order of the file.
// Those releases decode config.yaml strictly and refuse it (DEY-C001), so
// a rollback or downgrade below them is refused while any is set (DEY-S011).
func (c *Config) NewerKeysInUse() []string {
	if c == nil {
		return nil
	}
	var keys []string
	add := func(k string) {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, n := range c.Nodes {
		if n.BackendTier != "" {
			add("nodes[].backend_tier")
		}
	}
	for i := range c.Tunnels {
		if a := c.Tunnels[i].Advanced; a != nil && a.MonthlyQuotaGiB != 0 {
			add("tunnels[].advanced.monthly_quota_gib")
		}
	}
	if t := c.Tuning; t != nil {
		if t.SysctlProfile == SysctlAuto {
			add("tuning.sysctl_profile: auto")
		}
		if t.NodesAuto {
			add("tuning.nodes_auto")
		}
		if t.BackendTier != "" {
			add("tuning.backend_tier")
		}
		if t.WGMTU != 0 {
			add("tuning.wg_mtu")
		}
	}
	if c.Monitoring != nil {
		add("monitoring")
	}
	return keys
}
