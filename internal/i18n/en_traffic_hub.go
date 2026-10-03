package i18n

// Hub traffic monitoring: event texts of the sampler (hub/traffic.go).
const (
	// HubTrafficUnavailable: the accounting table cannot be built; %s is
	// the reason.
	HubTrafficUnavailable Key = "hub.traffic.unavailable"
	// HubTrafficQuotaWarn: tunnel, percent used, GiB used, quota GiB.
	HubTrafficQuotaWarn Key = "hub.traffic.quota_warn"
	// HubTrafficQuotaFull: tunnel, GiB used, quota GiB, period start date.
	HubTrafficQuotaFull Key = "hub.traffic.quota_full"
)

var trafficHubEN = map[Key]string{
	HubTrafficUnavailable: "Traffic accounting is not available: %s; only connection counts are shown",
	HubTrafficQuotaWarn:   "Tunnel %s has used %d%% of its traffic quota (%s of %d GiB in+out this period)",
	HubTrafficQuotaFull:   "Tunnel %s has used its whole traffic quota: %s of %d GiB in+out since %s",
}

// traffic hub — merge the strings above into the English table.
func init() {
	for k, v := range trafficHubEN {
		en[k] = v
	}
}
