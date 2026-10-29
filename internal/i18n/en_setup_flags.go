package i18n

// Flags of `deyroute setup` and `deyroute join` for automation
// (cli/setup.go): the hub's public addresses and the node's kernel profile.
const (
	CLIFlagPublicIP       Key = "cli.flag_public_ip"
	CLIFlagPublicIP6      Key = "cli.flag_public_ip6"
	CLIFlagAllowPrivateIP Key = "cli.flag_allow_private_ip"
	// CLIWantIPv4 and CLIWantIPv6 are the allowed values of --public-ip
	// and --public-ip6 (DEY-C013).
	CLIWantIPv4 Key = "cli.want_ipv4"
	CLIWantIPv6 Key = "cli.want_ipv6"
	// CLIWantPublicIP: a private, CGNAT, loopback or link-local address
	// without --allow-private-ip (or a link-local one with it).
	CLIWantPublicIP Key = "cli.want_public_ip"
	// CLIPublicIPFix is the fix of every refused --public-ip/--public-ip6.
	CLIPublicIPFix Key = "cli.public_ip_fix"
	// CLIPublicIPHubOnly: an address flag with --role node or on join.
	CLIPublicIPHubOnly    Key = "cli.public_ip_hub_only"
	CLIPublicIPHubOnlyFix Key = "cli.public_ip_hub_only_fix"
	// CLIAllowPrivateNeedsIP: --allow-private-ip without an address flag.
	CLIAllowPrivateNeedsIP    Key = "cli.allow_private_needs_ip"
	CLIAllowPrivateNeedsIPFix Key = "cli.allow_private_needs_ip_fix"
)

var setupFlagsEN = map[Key]string{
	CLIFlagPublicIP:       "public IPv4 address of this hub, stored as hub.public_ip (default: detected, or asked in the wizard)",
	CLIFlagPublicIP6:      "public IPv6 address of this hub, stored as hub.public_ip6 (default: detected)",
	CLIFlagAllowPrivateIP: "accept a private, CGNAT or loopback address for --public-ip/--public-ip6 (test labs only)",
	CLIWantIPv4:           "an IPv4 address, e.g. 203.0.113.7",
	CLIWantIPv6:           "an IPv6 address, e.g. 2001:db8::7",
	CLIWantPublicIP:       "a public address (not private, CGNAT, loopback, link-local or multicast)",
	CLIPublicIPFix: "pass the address your users reach this hub on, e.g. --public-ip 203.0.113.7 (and --public-ip6 2001:db8::7 for IPv6); " +
		"only in a test lab, add --allow-private-ip to accept a private, CGNAT or loopback address",
	CLIPublicIPHubOnly:        "only for deyroute setup --role hub",
	CLIPublicIPHubOnlyFix:     "drop the flag: a node's address is what the hub sees when it joins; the hub's address is set with deyroute setup --role hub --public-ip ADDRESS",
	CLIAllowPrivateNeedsIP:    "only together with --public-ip or --public-ip6",
	CLIAllowPrivateNeedsIPFix: "add --public-ip ADDRESS (or --public-ip6 ADDRESS), or drop --allow-private-ip",
}

// setup flags — merge the strings above into the English table.
func init() {
	for k, v := range setupFlagsEN {
		en[k] = v
	}
}
