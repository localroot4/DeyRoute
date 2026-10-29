package i18n

// The address of a restored hub (`deyroute restore --public-ip
// --public-ip6 --keep-address`, cli/restoreaddr.go; menu Backup & Restore),
// and the nodes a restored or updated hub brings to its version
// (`deyroute update`, DR-11).
const (
	CLIFlagRestorePublicIP   Key = "cli.flag_restore_public_ip"
	CLIFlagRestorePublicIP6  Key = "cli.flag_restore_public_ip6"
	CLIFlagKeepAddress       Key = "cli.flag_keep_address"
	CLIRestoreWantIPv4       Key = "cli.restore.want_ipv4"
	CLIRestoreWantIPv6       Key = "cli.restore.want_ipv6"
	CLIRestoreWantWithIP     Key = "cli.restore.want_with_ip"
	CLIRestoreWantNoKeep     Key = "cli.restore.want_no_keep"
	CLIRestoreWantHubBackup  Key = "cli.restore.want_hub_backup"
	CLIRestoreWhyPrivate     Key = "cli.restore.why_private"
	CLIRestoreWhyFailed      Key = "cli.restore.why_failed"
	CLIRestoreWhyNone        Key = "cli.restore.why_none"
	CLIAskRestoreIP          Key = "cli.restore.ask_ip"
	CLIRestoreIPHelp         Key = "cli.restore.ip_help"
	CLIRestoreWarnPrivate    Key = "cli.restore.warn_private"
	CLIRestoreWarnIP6Cleared Key = "cli.restore.warn_ip6_cleared"
	TUIBuAskIPIntro          Key = "tui.bu.ask_ip_intro"
	TUIBuIPField             Key = "tui.bu.ip_field"
	TUIWantIP                Key = "tui.want_ip"
	CLIUpdateNodesQueued     Key = "cli.update_nodes_queued"
	CLIUpdateNodeQueued      Key = "cli.update_node_queued"
	CLIUpdateNodesFailed     Key = "cli.update_nodes_failed"
)

var restoreEN = map[Key]string{
	CLIFlagRestorePublicIP:   "public IPv4 address of this server for a restored hub (the nodes dial it)",
	CLIFlagRestorePublicIP6:  "public IPv6 address of this server for a restored hub (only with --public-ip)",
	CLIFlagKeepAddress:       "keep the backup's hub addresses (same server or floating IP)",
	CLIRestoreWantIPv4:       "an IPv4 address, e.g. 203.0.113.7",
	CLIRestoreWantIPv6:       "an IPv6 address, e.g. 2001:db8::7",
	CLIRestoreWantWithIP:     "an IPv6 address together with --public-ip",
	CLIRestoreWantNoKeep:     "either --keep-address or --public-ip/--public-ip6, not both",
	CLIRestoreWantHubBackup:  "nothing: this is a node backup, which has no public address setting",
	CLIRestoreWhyPrivate:     "this server's detected address %s is not a public IP address (private, CGNAT or loopback)",
	CLIRestoreWhyFailed:      "the public IP of this server could not be detected (%s)",
	CLIRestoreWhyNone:        "the public IP of this server could not be detected",
	CLIAskRestoreIP:          "Public IP of this server for the restored hub",
	CLIRestoreIPHelp:         "Type the address the nodes dial. Enter alone keeps the backup's address %s (right only if this server owns it: same server or floating IP).",
	CLIRestoreWarnPrivate:    "%s is not a public IP address (private, CGNAT or loopback): nodes abroad reach the restored hub only if the provider forwards it to this server.",
	CLIRestoreWarnIP6Cleared: "The backup's hub.public_ip6 %s is cleared: pass --public-ip6 to give the restored hub an IPv6 address.",
	TUIBuAskIPIntro:          "%s.\nType this server's public IP; Enter alone keeps the backup's address %s (right only if this server owns it: same server or floating IP). A new address re-issues the hub certificate.",
	TUIBuIPField:             "Public IP of this server",
	TUIWantIP:                "Enter an IPv4 or IPv6 address, for example 203.0.113.7.",
	CLIUpdateNodesQueued:     "%d nodes run another version; they install deyroute %s from the hub when they connect.",
	CLIUpdateNodeQueued:      "1 node runs another version; it installs deyroute %s from the hub when it connects.",
	CLIUpdateNodesFailed:     "! The hub could not tell its nodes to follow its version (%s %s); nodes of another major.minor still get it when they connect.",
}

// restore address — merge the strings above into the English table.
func init() {
	for k, v := range restoreEN {
		en[k] = v
	}
}
