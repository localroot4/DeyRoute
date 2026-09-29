package i18n

// All English UI strings. Keep keys grouped by screen. Values may contain fmt
// verbs; callers pass matching args to T.

// Banner and chrome.
const (
	BannerArt        Key = "banner.art"
	BannerProduct    Key = "banner.product"
	BannerHub        Key = "banner.hub"
	BannerNode       Key = "banner.node"
	BannerMode       Key = "banner.mode"
	BannerNodes      Key = "banner.nodes"
	BannerTunnelsUp  Key = "banner.tunnels_up"
	BannerNotSetUp   Key = "banner.not_set_up"
	ModeSimple       Key = "mode.simple"
	ModeAdvanced     Key = "mode.advanced"
	FooterKeys       Key = "footer.keys"
	FooterKeysASCII  Key = "footer.keys_ascii"
	PromptChoice     Key = "prompt.choice"
	NotImplemented   Key = "common.not_implemented"
	PressBack        Key = "common.press_back"
	InvalidChoice    Key = "common.invalid_choice"
	HelpTitle        Key = "help.title"
	HelpMainMenu     Key = "help.main_menu"
	DaemonNotRunning Key = "common.daemon_not_running"
	Loading          Key = "common.loading"
)

// Main menu (section 6). Numbers are fixed; "*" marks Advanced-only sub-items.
const (
	MenuDashboard        Key = "menu.dashboard"
	MenuTunnels          Key = "menu.tunnels"
	MenuTunnelsDesc      Key = "menu.tunnels.desc"
	MenuNodes            Key = "menu.nodes"
	MenuNodesDesc        Key = "menu.nodes.desc"
	MenuPorts            Key = "menu.ports"
	MenuPortsDesc        Key = "menu.ports.desc"
	MenuFailover         Key = "menu.failover"
	MenuFailoverDesc     Key = "menu.failover.desc"
	MenuFailoverDescAdv  Key = "menu.failover.desc_adv"
	MenuDiagnostics      Key = "menu.diagnostics"
	MenuDiagnosticsDesc  Key = "menu.diagnostics.desc"
	MenuOptimize         Key = "menu.optimize"
	MenuOptimizeDesc     Key = "menu.optimize.desc"
	MenuOptimizeDescAdv  Key = "menu.optimize.desc_adv"
	MenuSecurity         Key = "menu.security"
	MenuSecurityDesc     Key = "menu.security.desc"
	MenuSecurityDescAdv  Key = "menu.security.desc_adv"
	MenuNotifications    Key = "menu.notifications"
	MenuNotificationsDes Key = "menu.notifications.desc"
	MenuBackup           Key = "menu.backup"
	MenuUpdate           Key = "menu.update"
	MenuUpdateDesc       Key = "menu.update.desc"
	MenuSettings         Key = "menu.settings"
	MenuSettingsDesc     Key = "menu.settings.desc"
	MenuExit             Key = "menu.exit"
)

// CLI.
const (
	CLIShort        Key = "cli.short"
	CLILong         Key = "cli.long"
	CLIVersionShort Key = "cli.version.short"
	CLIVersionLine  Key = "cli.version.line"
	CLIFlagDebug    Key = "cli.flag.debug"
	CLIFlagJSON     Key = "cli.flag.json"
	CLIFlagYes      Key = "cli.flag.yes"
	CLINeedConfirm  Key = "cli.need_confirm"
	CLITypeYes      Key = "cli.type_yes"
	CLIAborted      Key = "cli.aborted"
)

var en = map[Key]string{
	// The DEYROUTE banner. The ASCII variant is derived from this (see tui.ASCIIBanner).
	BannerArt: "" +
		" ██████╗ ███████╗██╗   ██╗██████╗  ██████╗ ██╗   ██╗████████╗███████╗\n" +
		" ██╔══██╗██╔════╝╚██╗ ██╔╝██╔══██╗██╔═══██╗██║   ██║╚══██╔══╝██╔════╝\n" +
		" ██║  ██║█████╗   ╚████╔╝ ██████╔╝██║   ██║██║   ██║   ██║   █████╗\n" +
		" ██║  ██║██╔══╝    ╚██╔╝  ██╔══██╗██║   ██║██║   ██║   ██║   ██╔══╝\n" +
		" ██████╔╝███████╗   ██║   ██║  ██║╚██████╔╝╚██████╔╝   ██║   ███████╗\n" +
		" ╚═════╝ ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝   ╚══════╝",
	BannerProduct:   "DEYROUTE Tunnel Manager",
	BannerHub:       "Hub: %s (%s)",
	BannerNode:      "Node: %s -> hub %s",
	BannerMode:      "Mode: %s",
	BannerNodes:     "%d nodes",
	BannerTunnelsUp: "%d tunnel UP",
	BannerNotSetUp:  "not set up (run: deyroute setup)",
	ModeSimple:      "Simple",
	ModeAdvanced:    "Advanced",
	FooterKeys:      "number + Enter select · q/Esc back · r refresh · ? help",
	FooterKeysASCII: "number + Enter select - q/Esc back - r refresh - ? help",
	PromptChoice:    "Choice: ",
	NotImplemented:  "%s: not implemented yet",
	PressBack:       "Press q or Esc to go back.",
	InvalidChoice:   "Invalid choice: %s",
	HelpTitle:       "Help",
	HelpMainMenu: "Type the number of an item and press Enter.\n" +
		"q or Esc goes back, r refreshes, ? shows this help.\n" +
		"Items marked * are visible in Advanced mode only (Settings -> ui mode).",
	DaemonNotRunning: "The deyroute daemon is not running. Start it with: systemctl start %s",
	Loading:          "Loading…",

	MenuDashboard:        "Dashboard (live)",
	MenuTunnels:          "Tunnels",
	MenuTunnelsDesc:      "add / edit / enable-disable / restart / switch transport / delete",
	MenuNodes:            "Nodes",
	MenuNodesDesc:        "show join command / list / rename / remove / test",
	MenuPorts:            "Ports",
	MenuPortsDesc:        "add port to tunnel / remove / check port / firewall status",
	MenuFailover:         "Failover",
	MenuFailoverDesc:     "policy / ladder order / backup nodes",
	MenuFailoverDescAdv:  "policy / ladder order / backup nodes / thresholds *",
	MenuDiagnostics:      "Diagnostics",
	MenuDiagnosticsDesc:  "port check · tunnel test · speed test · logs · doctor",
	MenuOptimize:         "Optimize",
	MenuOptimizeDesc:     "sysctl profile · BBR",
	MenuOptimizeDescAdv:  "sysctl profile · BBR · limits *",
	MenuSecurity:         "Security",
	MenuSecurityDesc:     "rotate tokens · TLS · firewall",
	MenuSecurityDescAdv:  "rotate tokens · TLS · firewall · view fingerprints *",
	MenuNotifications:    "Notifications",
	MenuNotificationsDes: "Telegram bot setup / test message",
	MenuBackup:           "Backup & Restore",
	MenuUpdate:           "Update",
	MenuUpdateDesc:       "deyroute / backends / manifest",
	MenuSettings:         "Settings",
	MenuSettingsDesc:     "ui mode (Simple/Advanced) · language · uninstall",
	MenuExit:             "Exit",

	CLIShort: "DEYROUTE Tunnel Manager",
	CLILong: "DEYROUTE builds censorship-resistant tunnels from an Iran hub to foreign nodes\n" +
		"with automatic transport and node failover.\n\n" +
		"Run without arguments to open the interactive menu.",
	CLIVersionShort: "Print version, commit, build date and Go version",
	CLIVersionLine:  "deyroute %s (commit %s, built %s, %s)",
	CLIFlagDebug:    "enable debug logging (also DEYROUTE_DEBUG=1)",
	CLIFlagJSON:     "print machine-readable JSON",
	CLIFlagYes:      "assume yes for confirmations",
	CLINeedConfirm:  "This action needs confirmation; re-run with --yes",
	CLITypeYes:      "Type yes to continue: ",
	CLIAborted:      "Aborted.",
}
