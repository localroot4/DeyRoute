package i18n

// Join-token revoke and setup without a join window (DR-16): `deyroute node
// join-revoke`, `deyroute setup --no-join` and the event
// join_tokens_revoked.
const (
	CLINodeJoinRevokeShort   Key = "cli.node_join_revoke_short"
	CLINodeJoinRevokeExample Key = "cli.node_join_revoke_example"
	// CLINodeJoinRevoked: tokens were revoked. Argument: how many.
	CLINodeJoinRevoked Key = "cli.node_join_revoked"
	// CLINodeJoinRevokedNone: there was no live token.
	CLINodeJoinRevokedNone Key = "cli.node_join_revoked_none"

	CLIFlagNoJoin Key = "cli.flag_no_join"
	// CLINoJoinHubOnly and CLINoJoinHubOnlyFix: --no-join with --role node.
	CLINoJoinHubOnly    Key = "cli.no_join_hub_only"
	CLINoJoinHubOnlyFix Key = "cli.no_join_hub_only_fix"
	// CLISetupNoJoinNext: the hub setup ends without a join command.
	CLISetupNoJoinNext Key = "cli.setup_no_join_next"

	// TUIEvJoinTokensRevoked is the word of join_tokens_revoked.
	TUIEvJoinTokensRevoked Key = "tui.dash.ev_join_tokens_revoked"
	// HubJoinTokensRevoked is the message of the event. Argument: how many.
	HubJoinTokensRevoked Key = "hub.join_tokens_revoked"
)

var joinRevokeEN = map[Key]string{
	CLINodeJoinRevokeShort:   "Revoke every join command not used yet and close the join window at once",
	CLINodeJoinRevokeExample: "  deyroute node join-revoke\n  deyroute node join-revoke --json",
	CLINodeJoinRevoked:       "Join tokens revoked: %d. The join window is closed: the control port is restricted to the nodes (@nodes) again.",
	CLINodeJoinRevokedNone:   "No join token was live. The join window is closed: the control port is restricted to the nodes (@nodes).",
	CLIFlagNoJoin: "make no join command and open no join window: the control port stays restricted to the nodes " +
		"(create one later with: deyroute node join-command)",
	CLINoJoinHubOnly:       "only for deyroute setup --role hub",
	CLINoJoinHubOnlyFix:    "drop --no-join: a node makes no join commands",
	CLISetupNoJoinNext:     "1. No join command was made (--no-join). When the node is ready, create one with: deyroute node join-command",
	TUIEvJoinTokensRevoked: "join",
	HubJoinTokensRevoked:   "join tokens revoked: %d; the control port is restricted to the nodes again",
}

// join revoke — merge the strings above into the English table.
func init() {
	for k, v := range joinRevokeEN {
		en[k] = v
	}
}
