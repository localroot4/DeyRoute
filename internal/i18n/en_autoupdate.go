package i18n

// The automatic update (`deyroute update auto`, cli/autoupdate.go).
const (
	CLIUpdateAutoShort    Key = "cli.update_auto_short"
	CLIUpdateAutoLong     Key = "cli.update_auto_long"
	CLIUpdateAutoExample  Key = "cli.update_auto_example"
	CLIAutoTitle          Key = "cli.auto.title"
	CLIAutoStatus         Key = "cli.auto.status"
	CLIAutoOn             Key = "cli.auto.on"
	CLIAutoOff            Key = "cli.auto.off"
	CLIAutoWhen           Key = "cli.auto.when"
	CLIAutoWhenValue      Key = "cli.auto.when_value"
	CLIAutoRunning        Key = "cli.auto.running"
	CLIAutoNext           Key = "cli.auto.next"
	CLIAutoNextValue      Key = "cli.auto.next_value"
	CLIAutoNextNone       Key = "cli.auto.next_none"
	CLIAutoVerifying      Key = "cli.auto.verifying"
	CLIAutoVerifyingValue Key = "cli.auto.verifying_value"
	CLIAutoLast           Key = "cli.auto.last"
	CLIAutoSkipped        Key = "cli.auto.skipped"
	CLIAutoSafety         Key = "cli.auto.safety"
	CLIAutoHintOff        Key = "cli.auto.hint_off"
	CLIAutoHintOn         Key = "cli.auto.hint_on"
	CLIAutoTurnedOn       Key = "cli.auto.turned_on"
	CLIAutoTurnedOff      Key = "cli.auto.turned_off"
	CLIFlagAutoUpdate     Key = "cli.flag_auto_update"
)

var autoUpdateEN = map[Key]string{
	CLIUpdateAutoShort: "Show the automatic update, or turn it on or off",
	CLIUpdateAutoLong: "Off by default. When on, the hub updates itself: once a day at 04:00 server time it\n" +
		"installs the newest release that has been out for at least 24 hours, and the nodes follow.\n" +
		"After the restart it checks that every tunnel that was UP and every node that was online\n" +
		"works again; if not, or if the new version does not start, the previous version is put\n" +
		"back and that release is never installed automatically. on and off set hub.auto_update in\n" +
		"config.yaml (after an automatic backup), so backups keep the choice. Without an argument\n" +
		"it shows the state.",
	CLIUpdateAutoExample:  "  deyroute update auto\n  deyroute update auto off\n  deyroute update auto on",
	CLIAutoTitle:          "AUTOMATIC UPDATE",
	CLIAutoStatus:         "Status",
	CLIAutoOn:             "on",
	CLIAutoOff:            "off",
	CLIAutoWhen:           "When",
	CLIAutoWhenValue:      "every day at %02d:00 server time; only releases out for %d hours",
	CLIAutoRunning:        "Running",
	CLIAutoNext:           "Next",
	CLIAutoNextValue:      "%s on %s",
	CLIAutoNextNone:       "none: this is the newest release",
	CLIAutoVerifying:      "Checking",
	CLIAutoVerifyingValue: "%s was just installed; the tunnels and nodes are being checked",
	CLIAutoLast:           "Last",
	CLIAutoSkipped:        "Skipped",
	CLIAutoSafety:         "If the tunnels or nodes do not come back after an update, the previous version is put back by itself.",
	CLIAutoHintOff:        "Turn off: deyroute update auto off",
	CLIAutoHintOn:         "Turn on: deyroute update auto on",
	CLIAutoTurnedOn:       "Automatic update is on.",
	CLIAutoTurnedOff:      "Automatic update is off; install releases with deyroute update.",
	CLIFlagAutoUpdate:     "automatic update of the hub: on or off (default off; stored as hub.auto_update)",
}

// automatic update — merge the strings above into the English table.
func init() {
	for k, v := range autoUpdateEN {
		en[k] = v
	}
}

// The head of `deyroute status` (cli/status_head.go).
const (
	CLIHeadTagline Key = "cli.head.tagline"
	CLIHeadHub     Key = "cli.head.hub"
	CLIHeadNode    Key = "cli.head.node"
	CLIHeadHubOf   Key = "cli.head.hub_of"
	CLIHeadMode    Key = "cli.head.mode"
	CLIHeadNodes   Key = "cli.head.nodes"
	CLIHeadTunnels Key = "cli.head.tunnels"
	CLIHeadOnline  Key = "cli.head.online"
	CLIHeadUp      Key = "cli.head.up"
)

var statusHeadEN = map[Key]string{
	CLIHeadTagline: "Tunnel Manager · %s",
	CLIHeadHub:     "Hub",
	CLIHeadNode:    "Node",
	CLIHeadHubOf:   "Hub",
	CLIHeadMode:    "Mode",
	CLIHeadNodes:   "Nodes",
	CLIHeadTunnels: "Tunnels",
	CLIHeadOnline:  "%d/%d online",
	CLIHeadUp:      "%d/%d UP",
}

// status head — merge the strings above into the English table.
func init() {
	for k, v := range statusHeadEN {
		en[k] = v
	}
}
