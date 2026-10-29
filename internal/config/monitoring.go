package config

import "time"

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

// FirewallReassertInterval is how often the daemon compares the live table
// inet deyroute with the one it applied (security.firewall_reassert_s,
// DefaultFirewallReassertS when unset); 0 = off, also whenever
// security.firewall_managed is false.
func (c *Config) FirewallReassertInterval() time.Duration {
	s := DefaultSecurity()
	if c != nil && c.Security != nil {
		s = c.Security
	}
	if !s.FirewallManaged {
		return 0
	}
	n := DefaultFirewallReassertS
	if s.FirewallReassertS != nil {
		n = *s.FirewallReassertS
	}
	return time.Duration(max(n, 0)) * time.Second
}

// UnknownControlRates returns the nft rates at which addresses outside the
// nodes may open new control-port connections: per source address and in
// all (security.unknown_control_rate_per_source and
// security.unknown_control_rate, their defaults when unset).
func (c *Config) UnknownControlRates() (perSource, global string) {
	perSource, global = DefaultUnknownControlRatePerSource, DefaultUnknownControlRate
	if c == nil || c.Security == nil {
		return perSource, global
	}
	if c.Security.UnknownControlRatePerSource != "" {
		perSource = c.Security.UnknownControlRatePerSource
	}
	if c.Security.UnknownControlRate != "" {
		global = c.Security.UnknownControlRate
	}
	return perSource, global
}
