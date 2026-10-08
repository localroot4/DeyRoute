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
)

var autoUpdateEN = map[Key]string{
	CLIUpdateAutoShort: "Show the automatic update, or turn it on or off",
	CLIUpdateAutoLong: "The hub updates itself (on by default): once a day at 04:00 server time it installs the\n" +
		"newest release that has been out for at least 24 hours, and the nodes follow. After the\n" +
		"restart it checks that every tunnel that was UP and every node that was online works\n" +
		"again; if not, or if the new version does not start, the previous version is put back\n" +
		"and that release is never installed automatically. Without an argument it shows the state.",
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
}

// automatic update — merge the strings above into the English table.
func init() {
	for k, v := range autoUpdateEN {
		en[k] = v
	}
}
