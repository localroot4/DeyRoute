package i18n

// Hub: the hint a node_ip_changed event carries when the node's control
// connection moved to the other IP family (hub/control.go). The set-hub
// command comes first: a table cell or a notification may cut the rest.
const (
	// HubNodeIPToIPv6: the node moved to IPv6 while hub.public_ip is IPv4.
	// Arguments: the hub control address for set-hub (host:port),
	// hub.public_ip.
	HubNodeIPToIPv6 Key = "hub.node_ip.to_ipv6"
	// HubNodeIPFamilyOther: the node left the family of hub.public_ip in
	// another way (an IPv6-only hub). Arguments: the hub control address
	// for set-hub, the new family (IPv4 or IPv6), hub.public_ip.
	HubNodeIPFamilyOther Key = "hub.node_ip.family_other"
	// HubNodeIPFamilyHub: the node is back on the family of hub.public_ip.
	// Argument: the new family (IPv4 or IPv6).
	HubNodeIPFamilyHub Key = "hub.node_ip.family_hub"
)

var nodeIPHubEN = map[Key]string{
	HubNodeIPToIPv6:      "IPv6 between hub and node is not supported yet; to go back, run on the node: deyroute node set-hub %s (the node now reaches the hub over IPv6, but the transports where the node dials the hub still use hub.public_ip %s and the hub firewall blocks them)",
	HubNodeIPFamilyOther: "to go back, run on the node: deyroute node set-hub %s (the node now reaches the hub over %s, but the transports where the node dials the hub still use hub.public_ip %s and the hub firewall blocks them)",
	HubNodeIPFamilyHub:   "the node now reaches the hub over %s, like hub.public_ip, so the hub firewall accepts the transports where the node dials the hub",
}

// node IP hub — merge the strings above into the English table.
func init() {
	for k, v := range nodeIPHubEN {
		en[k] = v
	}
}
