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
	BannerNode1      Key = "banner.node1"
	BannerTunnelsUp  Key = "banner.tunnels_up"
	BannerTunnel1Up  Key = "banner.tunnel1_up"
	BannerShort      Key = "banner.short"
	BannerNotSetUp   Key = "banner.not_set_up"
	BannerSetup      Key = "banner.setup"
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
	// On a node server: the same numbers; hub-only items are marked.
	MenuHubOnly         Key = "menu.hub_only"
	MenuDiagnosticsNode Key = "menu.diagnostics.node"
	MenuBackupNode      Key = "menu.backup.node"
	MenuSettingsNode    Key = "menu.settings.node"
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
	CLISetupShort   Key = "cli.setup.short"
	CLIJoinShort    Key = "cli.join.short"
	CLINotYetFix    Key = "cli.not_yet_fix"
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
	BannerNode:      "Node: %s → hub %s",
	BannerMode:      "Mode: %s",
	BannerNodes:     "%d nodes",
	BannerNode1:     "1 node",
	BannerTunnelsUp: "%d tunnels UP",
	BannerTunnel1Up: "1 tunnel UP",
	BannerShort:     "DEYROUTE",
	BannerNotSetUp:  "not set up (run: deyroute setup)",
	BannerSetup:     "first-time setup",
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
		"Items marked * are visible in Advanced mode only: 12) Settings → 1) UI mode.\n" +
		"On a node, items marked (hub only) are managed in the hub's menu.",
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
	MenuHubOnly:          "(hub only)",
	MenuDiagnosticsNode:  "logs · doctor",
	MenuBackupNode:       "backup · restore · set hub address",
	MenuSettingsNode:     "uninstall",

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
	CLISetupShort:   "Set up this server as a hub or node (wizard)",
	CLIJoinShort:    "Join this server to a hub as a node",
	CLINotYetFix:    "this development build installs the binary only; re-run the install command after the next build to upgrade (config is never touched)",
}

// doctor — strings of `deyroute doctor` (internal/doctor): the 20-line
// summary, the findings file and the message/fix pair of each of the 15
// rules (section 13). Kept in one block so it merges cleanly.
const (
	DoctorSevError        Key = "doctor.sev.error"
	DoctorSevWarn         Key = "doctor.sev.warn"
	DoctorSevInfo         Key = "doctor.sev.info"
	DoctorSevOK           Key = "doctor.sev.ok"
	DoctorTitle           Key = "doctor.title"
	DoctorOverviewHub     Key = "doctor.overview.hub"
	DoctorOverviewNode    Key = "doctor.overview.node"
	DoctorHubConnected    Key = "doctor.hub_connected"
	DoctorHubDisconnected Key = "doctor.hub_disconnected"
	DoctorCounts          Key = "doctor.counts"
	DoctorHealthy         Key = "doctor.healthy"
	DoctorFix             Key = "doctor.fix"
	DoctorMore            Key = "doctor.more"
	DoctorFindingsHeader  Key = "doctor.findings_header"
	DoctorR01Msg          Key = "doctor.r01.msg"
	DoctorR01Fix          Key = "doctor.r01.fix"
	DoctorR02Msg          Key = "doctor.r02.msg"
	DoctorR02Fix          Key = "doctor.r02.fix"
	DoctorNever           Key = "doctor.never"
	DoctorR01MsgIdle      Key = "doctor.r01.msg_idle"
	DoctorR01FixIdle      Key = "doctor.r01.fix_idle"
	DoctorR02MsgOffline   Key = "doctor.r02.msg_offline"
	DoctorR02FixOffline   Key = "doctor.r02.fix_offline"
	DoctorR03Msg          Key = "doctor.r03.msg"
	DoctorR03Fix          Key = "doctor.r03.fix"
	DoctorR04Msg          Key = "doctor.r04.msg"
	DoctorR04MsgRaw       Key = "doctor.r04.msg_raw"
	DoctorR04Fix          Key = "doctor.r04.fix"
	DoctorR04FixGeneric   Key = "doctor.r04.fix_generic"
	DoctorR05Msg          Key = "doctor.r05.msg"
	DoctorR05Fix          Key = "doctor.r05.fix"
	DoctorR06Msg          Key = "doctor.r06.msg"
	DoctorR06Fix          Key = "doctor.r06.fix"
	DoctorR07Msg          Key = "doctor.r07.msg"
	DoctorR07Fix          Key = "doctor.r07.fix"
	DoctorR08MsgExpired   Key = "doctor.r08.msg_expired"
	DoctorR08MsgSoon      Key = "doctor.r08.msg_soon"
	DoctorR08FixTunnel    Key = "doctor.r08.fix_tunnel"
	DoctorR08FixInternal  Key = "doctor.r08.fix_internal"
	DoctorR09Msg          Key = "doctor.r09.msg"
	DoctorR09Fix          Key = "doctor.r09.fix"
	DoctorR10Msg          Key = "doctor.r10.msg"
	DoctorR10Fix          Key = "doctor.r10.fix"
	DoctorR11MsgBBR       Key = "doctor.r11.msg_bbr"
	DoctorR11MsgOff       Key = "doctor.r11.msg_off"
	DoctorR11Fix          Key = "doctor.r11.fix"
	DoctorR11FixBBR       Key = "doctor.r11.fix_bbr"
	DoctorR12MsgDisk      Key = "doctor.r12.msg_disk"
	DoctorR12FixDisk      Key = "doctor.r12.fix_disk"
	DoctorR12MsgMem       Key = "doctor.r12.msg_mem"
	DoctorR12FixMem       Key = "doctor.r12.fix_mem"
	DoctorR13Msg          Key = "doctor.r13.msg"
	DoctorR13Fix          Key = "doctor.r13.fix"
	DoctorR14Msg          Key = "doctor.r14.msg"
	DoctorR14Fix          Key = "doctor.r14.fix"
	DoctorR15MsgPerm      Key = "doctor.r15.msg_perm"
	DoctorR15FixPerm      Key = "doctor.r15.fix_perm"
	DoctorR15MsgJoin      Key = "doctor.r15.msg_join"
	DoctorR15FixJoin      Key = "doctor.r15.fix_join"
	DoctorR15MsgJoinLong  Key = "doctor.r15.msg_join_long"
	DoctorR15FixJoinLong  Key = "doctor.r15.fix_join_long"
)

// doctor — English texts (merged into en by init).
var doctorEN = map[Key]string{
	DoctorSevError:        "ERROR",
	DoctorSevWarn:         "WARN",
	DoctorSevInfo:         "INFO",
	DoctorSevOK:           "OK",
	DoctorTitle:           "DEYROUTE doctor: %s %s, %s",
	DoctorOverviewHub:     "Tunnels: %d/%d UP   Nodes: %d/%d online",
	DoctorOverviewNode:    "Node %s   Hub %s: %s",
	DoctorHubConnected:    "connected",
	DoctorHubDisconnected: "not connected",
	DoctorCounts:          "Result: %d ERROR, %d WARN, %d INFO",
	DoctorHealthy:         "No problems found",
	DoctorFix:             "Fix: %s",
	DoctorMore:            "... %d more finding(s): see findings.txt in the doctor file",
	DoctorFindingsHeader:  "deyroute doctor findings (%d)",

	DoctorR01Msg:         "Node %s is offline on the control channel, but tunnel %s still passes traffic: the control network has a problem, the tunnel is healthy",
	DoctorR01Fix:         "check that node %s can reach the hub control port (on the node: deyroute logs node); the tunnel needs nothing",
	DoctorR02Msg:         "Tunnel %s is DOWN and every transport failed or is quarantined: the IP of node %s is probably blocked",
	DoctorR02Fix:         "test the node from the hub (deyroute node test %s); if it is blocked give the node a new IP, or add a backup node: deyroute tunnel backup add %s --node <id>",
	DoctorNever:          "never",
	DoctorR01MsgIdle:     "Node %s is offline on the control channel (last heartbeat: %s)",
	DoctorR01FixIdle:     "on node %s check the service and its log (systemctl status deyroute-node; deyroute logs node) and that it can reach the hub control port",
	DoctorR02MsgOffline:  "Tunnel %s is DOWN and node %s is offline on the control channel too: the node server is down or its IP is blocked",
	DoctorR02FixOffline:  "check that the node server is running (on it: systemctl status deyroute-node); if it runs, its IP is probably blocked: give it a new IP, or add a backup node: deyroute tunnel backup add %s --node <id>",
	DoctorR03Msg:         "Tunnel %s is connected, but the service behind it on node %s does not answer on %s",
	DoctorR03Fix:         "start the service on node %s and make sure it listens on %s",
	DoctorR04Msg:         "Port %d/%s is blocked by an external firewall (%s)",
	DoctorR04MsgRaw:      "An external firewall blocks deyroute: %s",
	DoctorR04Fix:         "open it: %s",
	DoctorR04FixGeneric:  "allow the port in that firewall; see deyroute security firewall show",
	DoctorR05Msg:         "Port conflict: %s",
	DoctorR05Fix:         "stop the other program or use another port (deyroute port check <port>, deyroute port suggest)",
	DoctorR06Msg:         "Service %s is %s",
	DoctorR06Fix:         "start it and read its log: systemctl restart %s; journalctl -u %s -n 50",
	DoctorR07Msg:         "Tunnel unit %s keeps crashing (%d restarts)",
	DoctorR07Fix:         "read the backend log (deyroute logs %s), then re-render the tunnel: deyroute tunnel restart %s",
	DoctorR08MsgExpired:  "Certificate %s expired on %s",
	DoctorR08MsgSoon:     "Certificate %s expires in %d days (%s)",
	DoctorR08FixTunnel:   "renew it: deyroute security tls renew --tunnel %s",
	DoctorR08FixInternal: "re-issue the internal certificates: deyroute security rotate-ca",
	DoctorR09Msg:         "Version mismatch: hub %s, node %s runs %s (major.minor must be equal)",
	DoctorR09Fix:         "update both servers to the same release: deyroute update",
	DoctorR10Msg:         "UDP is blocked between the hub and node %s: UDP transports are skipped",
	DoctorR10Fix:         "nothing to do while TCP transports work; skipped rungs are re-tested every 30 minutes",
	DoctorR11MsgBBR:      "BBR is set by the %s profile but the kernel does not use it",
	DoctorR11MsgOff:      "Kernel tuning is off (sysctl profile off)",
	DoctorR11Fix:         "apply the recommended profile: deyroute optimize apply --profile balanced",
	DoctorR11FixBBR:      "apply it again on the hub (it loads tcp_bbr; the nodes get it too): deyroute optimize apply --profile %s",
	DoctorR12MsgDisk:     "Low disk space on %s: %.1f%% free",
	DoctorR12FixDisk:     "free space (df -h %s): e.g. journalctl --vacuum-size=100M, old backups in /var/lib/deyroute/backups",
	DoctorR12MsgMem:      "Low memory: %.1f%% available",
	DoctorR12FixMem:      "stop unneeded services or add RAM; check with: free -m",
	DoctorR13Msg:         "Clock of %s differs from the hub by %s: TLS connections fail with a large difference",
	DoctorR13Fix:         "enable time sync on both servers: timedatectl set-ntp true",
	DoctorR14Msg:         "Tunnel %s is flapping (%d flapping events in the last hour)",
	DoctorR14Fix:         "the network is unstable; pin one transport (deyroute tunnel switch %s --transport <id>) or pause failover: deyroute tunnel pause %s",
	DoctorR15MsgPerm:     "Secret file permissions are wrong: %s",
	DoctorR15FixPerm:     "restrict them: chown -R root:root /etc/deyroute/secrets; chmod -R go-rwx /etc/deyroute/secrets; then: deyroute security audit",
	DoctorR15MsgJoin:     "%d expired join token(s) could not be removed from /etc/deyroute/secrets/join-tokens.json",
	DoctorR15FixJoin:     "they are never accepted; deyroute removes them by itself but could not write the file: free disk space, check the permissions (deyroute security audit) and run deyroute doctor again",
	DoctorR15MsgJoinLong: "%d join token(s) made with a TTL over 15 minutes are valid until %s: until then the control port accepts every address",
	DoctorR15FixJoinLong: "nothing to do while a node still has to join with it; it expires and is removed by itself. Keep the default 15-minute TTL next time: deyroute node join-command",
}

// doctor — merge the block above into the English table.
func init() {
	for k, v := range doctorEN {
		en[k] = v
	}
}

// tui — strings of the interactive menu (internal/tui), grouped per screen.
// Kept in one block (merged into en by init) so it merges cleanly.
const (
	// common
	TUIBackItem       Key = "tui.common.back_item"
	TUIWorking        Key = "tui.common.working"
	TUIPressEnterBack Key = "tui.common.press_enter_back"
	TUIRetryHint      Key = "tui.common.retry_hint"
	TUIEnterContinue  Key = "tui.common.enter_continue"
	TUIConfirmChoice  Key = "tui.common.confirm_choice"
	TUIMinute1        Key = "tui.common.minute1"
	TUIMinutes        Key = "tui.common.minutes"
	TUIRequired       Key = "tui.common.required"
	TUINone           Key = "tui.common.none"
	TUIYes            Key = "tui.common.yes"
	TUINo             Key = "tui.common.no"
	TUIAnswerYN       Key = "tui.common.answer_yn"
	TUINotAvailable   Key = "tui.common.not_available"
	TUICurrent        Key = "tui.common.current"
	TUINothingChanged Key = "tui.common.nothing_changed"
	TUIms             Key = "tui.common.ms"
	TUIUptimeDays     Key = "tui.common.uptime_days"
	TUIUptimeClock    Key = "tui.common.uptime_clock"
	TUIDash           Key = "tui.common.dash"
	TUIWantNumber     Key = "tui.common.want_number"
	TUIWantOneOf      Key = "tui.common.want_one_of"
	TUIWantDomain     Key = "tui.common.want_domain"
	TUIWantAbsPath    Key = "tui.common.want_abs_path"
	TUIDone           Key = "tui.common.done"
	TUIKeepHint       Key = "tui.common.keep_hint"
	TUIPickTunnel     Key = "tui.common.pick_tunnel"
	TUIPickNode       Key = "tui.common.pick_node"
	TUINoTunnels      Key = "tui.common.no_tunnels"
	TUINotSetUpTitle  Key = "tui.not_set_up.title"
	TUINotSetUpHub    Key = "tui.not_set_up.hub"
	TUINotSetUpNode   Key = "tui.not_set_up.node"
	TUINoNodes        Key = "tui.common.no_nodes"
	TUIClientIP       Key = "tui.common.client_ip"
	TUIClientKept     Key = "tui.common.client_preserved"
	TUIClientMasked   Key = "tui.common.client_masked"
	TUIClientMaskedPP Key = "tui.client_masked_pp"

	// tunnel and node states (always shown as words, never color only)
	TUIStateUp          Key = "tui.state.up"
	TUIStateDegraded    Key = "tui.state.degraded"
	TUIStateDegradedSvc Key = "tui.state.degraded_svc"
	TUIStateSwitching   Key = "tui.state.switching"
	TUIStateStarting    Key = "tui.state.starting"
	TUIStateInit        Key = "tui.state.init"
	TUIStateDown        Key = "tui.state.down"
	TUIStateDisabled    Key = "tui.state.disabled"
	TUIStatePaused      Key = "tui.state.paused"
	TUIOnline           Key = "tui.state.online"
	TUIOffline          Key = "tui.state.offline"

	// 1 dashboard
	TUIDashTunnels      Key = "tui.dash.tunnels"
	TUIDashNodes        Key = "tui.dash.nodes"
	TUIDashEvents       Key = "tui.dash.events"
	TUIDashNode         Key = "tui.dash.node"
	TUIColNum           Key = "tui.dash.col_num"
	TUIColName          Key = "tui.dash.col_name"
	TUIColNode          Key = "tui.dash.col_node"
	TUIColTransport     Key = "tui.dash.col_transport"
	TUIColState         Key = "tui.dash.col_state"
	TUIColRTT           Key = "tui.dash.col_rtt"
	TUIColUptime        Key = "tui.dash.col_uptime"
	TUIColPorts         Key = "tui.dash.col_ports"
	TUINodeCtl          Key = "tui.dash.node_ctl"
	TUINodeVersion      Key = "tui.dash.node_version"
	TUINodeCPU          Key = "tui.dash.node_cpu"
	TUINodeRAM          Key = "tui.dash.node_ram"
	TUIDashNoTunnels    Key = "tui.dash.no_tunnels"
	TUIDashNoNodes      Key = "tui.dash.no_nodes"
	TUIDashNoEvents     Key = "tui.dash.no_events"
	TUIDashHub          Key = "tui.dash.hub"
	TUIDashConnected    Key = "tui.dash.connected"
	TUIDashDisconnected Key = "tui.dash.disconnected"
	TUIDashLastContact  Key = "tui.dash.last_contact"
	TUIDashUnits        Key = "tui.dash.units"
	TUIDashUpdated      Key = "tui.dash.updated"
	TUIEvUp             Key = "tui.dash.ev_up"
	TUIEvDegraded       Key = "tui.dash.ev_degraded"
	TUIEvDown           Key = "tui.dash.ev_down"
	TUIEvSwitch         Key = "tui.dash.ev_switch"

	// 2 tunnels
	TUITunAdd            Key = "tui.tun.add"
	TUITunEdit           Key = "tui.tun.edit"
	TUITunToggle         Key = "tui.tun.toggle"
	TUITunRestart        Key = "tui.tun.restart"
	TUITunSwitch         Key = "tui.tun.switch"
	TUITunDelete         Key = "tui.tun.delete"
	TUITunShow           Key = "tui.tun.show"
	TUITunEnabled        Key = "tui.tun.enabled"
	TUITunDisabled       Key = "tui.tun.disabled"
	TUITunDisableConfirm Key = "tui.tun.disable_confirm"
	TUITunRestartConfirm Key = "tui.tun.restart_confirm"
	TUITunRestarted      Key = "tui.tun.restarted"
	TUITunDeleteLost     Key = "tui.tun.delete_lost"
	TUITunDeleted        Key = "tui.tun.deleted"
	TUITunSwitchPick     Key = "tui.tun.switch_pick"
	TUITunSwitchNode     Key = "tui.tun.switch_node"
	TUITunSwitched       Key = "tui.tun.switched"
	TUITunUpdated        Key = "tui.tun.updated"
	TUIEditName          Key = "tui.tun.edit_name"
	TUIEditPolicy        Key = "tui.tun.edit_policy"
	TUIEditLadder        Key = "tui.tun.edit_ladder"
	TUIEditTLS           Key = "tui.tun.edit_tls"
	TUIEditTLSCert       Key = "tui.tun.edit_tls_cert"
	TUIEditTLSKey        Key = "tui.tun.edit_tls_key"
	TUIEditProbe         Key = "tui.tun.edit_probe"
	TUIDetID             Key = "tui.tun.det_id"
	TUIDetName           Key = "tui.tun.det_name"
	TUIDetState          Key = "tui.tun.det_state"
	TUIDetVia            Key = "tui.tun.det_via"
	TUIDetPorts          Key = "tui.tun.det_ports"
	TUIDetNodes          Key = "tui.tun.det_nodes"
	TUIDetPrimary        Key = "tui.tun.det_primary"
	TUIDetBackup         Key = "tui.tun.det_backup"
	TUIDetClientIP       Key = "tui.tun.det_client_ip"
	TUIDetPolicy         Key = "tui.tun.det_policy"
	TUIDetLadder         Key = "tui.tun.det_ladder"
	TUIDetTLS            Key = "tui.tun.det_tls"
	TUIDetRungs          Key = "tui.tun.det_rungs"
	TUIDetEvents         Key = "tui.tun.det_events"
	TUIDetWarm           Key = "tui.tun.det_warm"
	TUIDetActive         Key = "tui.tun.det_active"
	TUIDetSkipped        Key = "tui.tun.det_skipped"
	TUIDetQuarantine     Key = "tui.tun.det_quarantine"

	// 2 tunnels -> add tunnel wizard
	TUIWizQNode           Key = "tui.wiz.q_node"
	TUIWizNoNode          Key = "tui.wiz.no_node"
	TUIWizAutoNode        Key = "tui.wiz.auto_node"
	TUIWizQPorts          Key = "tui.wiz.q_ports"
	TUIWizPortsHint       Key = "tui.wiz.ports_hint"
	TUIWizPortsPrompt     Key = "tui.wiz.ports_prompt"
	TUIWizChecking        Key = "tui.wiz.checking"
	TUIWizPortFree        Key = "tui.wiz.port_free"
	TUIWizPortFirewall    Key = "tui.wiz.port_firewall"
	TUIWizPortRun         Key = "tui.wiz.port_run"
	TUIWizPortUnreachable Key = "tui.wiz.port_unreachable"
	TUIWizPortBusy        Key = "tui.wiz.port_busy"
	TUIWizPortBusyAny     Key = "tui.wiz.port_busy_any"
	TUIWizPortError       Key = "tui.wiz.port_error"
	TUIWizChange          Key = "tui.wiz.change"
	TUIWizSkip            Key = "tui.wiz.skip"
	TUIWizStop            Key = "tui.wiz.stop"
	TUIWizNewPort         Key = "tui.wiz.new_port"
	TUIWizSuggest         Key = "tui.wiz.suggest"
	TUIWizNoPortsLeft     Key = "tui.wiz.no_ports_left"
	TUIWizDupPort         Key = "tui.wiz.dup_port"
	TUIWizStopConfirm     Key = "tui.wiz.stop_confirm"
	TUIWizOpenFw          Key = "tui.wiz.open_fw"
	TUIWizOpenFwAll       Key = "tui.wiz.open_fw_all"
	TUIWizOpenConfirmAll  Key = "tui.wiz.open_confirm_all"
	TUIWizKeep            Key = "tui.wiz.keep"
	TUIWizKeepAll         Key = "tui.wiz.keep_all"
	TUIWizOpened          Key = "tui.wiz.opened"
	TUIWizQConfirm        Key = "tui.wiz.q_confirm"
	TUIWizSumNode         Key = "tui.wiz.sum_node"
	TUIWizSumPorts        Key = "tui.wiz.sum_ports"
	TUIWizSumLadder       Key = "tui.wiz.sum_ladder"
	TUIWizSumBackup       Key = "tui.wiz.sum_backup"
	TUIWizSumName         Key = "tui.wiz.sum_name"
	TUIWizSumPolicy       Key = "tui.wiz.sum_policy"
	TUIWizSumTLS          Key = "tui.wiz.sum_tls"
	TUIWizSumThresh       Key = "tui.wiz.sum_thresholds"
	TUIWizAutomatic       Key = "tui.wiz.automatic"
	TUIWizDefault         Key = "tui.wiz.default"
	TUIWizDefaultWord     Key = "tui.wiz.default_word"
	TUIWizCustom          Key = "tui.wiz.custom"
	TUIWizCreate          Key = "tui.wiz.create"
	TUIWizAdvOptions      Key = "tui.wiz.adv_options"
	TUIWizCreateItem      Key = "tui.wiz.create_item"
	TUIWizAdvIntro        Key = "tui.wiz.adv_intro"
	TUIWizName            Key = "tui.wiz.name"
	TUIWizTarget          Key = "tui.wiz.target"
	TUIWizProbe           Key = "tui.wiz.probe"
	TUIWizSumProbe        Key = "tui.wiz.sum_probe"
	TUIWizBackupQ         Key = "tui.wiz.backup_q"
	TUIWizThreshQ         Key = "tui.wiz.thresh_q"
	TUITunnelUp           Key = "tui.wiz.tunnel_up"
	TUITunnelCreated      Key = "tui.wiz.tunnel_created"
	TUIThProbeInterval    Key = "tui.th.probe_interval"
	TUIThProbeTimeout     Key = "tui.th.probe_timeout"
	TUIThFail             Key = "tui.th.fail"
	TUIThRecover          Key = "tui.th.recover"
	TUIThFailback         Key = "tui.th.failback"
	TUIThFailbackAfter    Key = "tui.th.failback_after"
	TUIThMaxSwitches      Key = "tui.th.max_switches"
	TUIThQuarantine       Key = "tui.th.quarantine"
	TUIThIntro            Key = "tui.th.intro"

	// ladder editor (Failover -> Ladder order, Advanced add tunnel)
	TUILadTitle    Key = "tui.lad.title"
	TUILadGuideIf  Key = "tui.lad.guide_if"
	TUILadGuideDo  Key = "tui.lad.guide_do"
	TUILadG1If     Key = "tui.lad.g1_if"
	TUILadG1Do     Key = "tui.lad.g1_do"
	TUILadG2If     Key = "tui.lad.g2_if"
	TUILadG2Do     Key = "tui.lad.g2_do"
	TUILadG3If     Key = "tui.lad.g3_if"
	TUILadG3Do     Key = "tui.lad.g3_do"
	TUILadG4If     Key = "tui.lad.g4_if"
	TUILadG4Do     Key = "tui.lad.g4_do"
	TUILadKeys     Key = "tui.lad.keys"
	TUILadEmpty    Key = "tui.lad.empty"
	TUILadAdd      Key = "tui.lad.add"
	TUILadProfile  Key = "tui.lad.profile"
	TUILadSaved    Key = "tui.lad.saved"
	TUILadNoneLeft Key = "tui.lad.none_left"
	TUILadBuiltin  Key = "tui.lad.builtin"

	// 3 nodes
	TUINdJoin         Key = "tui.nd.join"
	TUINdList         Key = "tui.nd.list"
	TUINdRename       Key = "tui.nd.rename"
	TUINdRemove       Key = "tui.nd.remove"
	TUINdTest         Key = "tui.nd.test"
	TUINdJoinIntro    Key = "tui.nd.join_intro"
	TUINdJoinExpires  Key = "tui.nd.join_expires"
	TUINdJoinCopy     Key = "tui.nd.join_copy"
	TUINdJoinPlainEnd Key = "tui.nd.join_plain_end"
	TUIMoreLines      Key = "tui.more_lines"
	TUINdNewName      Key = "tui.nd.new_name"
	TUINdRenamed      Key = "tui.nd.renamed"
	TUINdRemoveLost   Key = "tui.nd.remove_lost"
	TUINdRemoved      Key = "tui.nd.removed"
	TUINdControl      Key = "tui.nd.control"
	TUINdUDP          Key = "tui.nd.udp"
	TUINdUDPBlocked   Key = "tui.nd.udp_blocked"
	TUINdVersion      Key = "tui.nd.version"
	TUINdCompatible   Key = "tui.nd.compatible"
	TUINdIncompatible Key = "tui.nd.incompatible"
	TUINdLastSeen     Key = "tui.nd.last_seen"
	TUINdFingerprint  Key = "tui.nd.fingerprint"
	TUINdTunnels      Key = "tui.nd.tunnels"
	TUINdIP           Key = "tui.nd.ip"
	TUINdLastError    Key = "tui.nd.last_error"

	// 4 ports
	TUIPtAdd        Key = "tui.pt.add"
	TUIPtRemove     Key = "tui.pt.remove"
	TUIPtCheck      Key = "tui.pt.check"
	TUIPtFirewall   Key = "tui.pt.firewall"
	TUIPtInput      Key = "tui.pt.input"
	TUIPtAddConfirm Key = "tui.pt.add_confirm"
	TUIPtAdded      Key = "tui.pt.added"
	TUIPtPick       Key = "tui.pt.pick"
	TUIPtRemoveLost Key = "tui.pt.remove_lost"
	TUIPtNoPorts    Key = "tui.pt.no_ports"
	TUIPtProbe      Key = "tui.pt.probe"
	TUIPtProbeTitle Key = "tui.pt.probe_title"
	TUIPtProbeField Key = "tui.pt.probe_field"
	TUIPtProbeHint  Key = "tui.pt.probe_hint"
	TUIPtProbePick  Key = "tui.pt.probe_pick"
	TUIPtProbeNoTCP Key = "tui.pt.probe_no_tcp"
	TUIPtProbePort  Key = "tui.pt.probe_port"
	TUIPtProbeKind  Key = "tui.pt.probe_kind"
	TUIPtProbeSet   Key = "tui.pt.probe_set"
	TUIProbeAuto    Key = "tui.probe.auto"
	TUIProbeTCP     Key = "tui.probe.tcp"
	TUIProbeTLS     Key = "tui.probe.tls"
	TUIProbeHTTP    Key = "tui.probe.http"

	// port check (Ports -> Check port, Diagnostics -> Port check)
	TUIPCPort          Key = "tui.pc.port"
	TUIPCNode          Key = "tui.pc.node"
	TUIPCOnePort       Key = "tui.pc.one_port"
	TUIPCBind          Key = "tui.pc.bind"
	TUIPCFirewall      Key = "tui.pc.firewall"
	TUIPCFromNode      Key = "tui.pc.from_node"
	TUIPCFromNodeNone  Key = "tui.pc.from_node_none"
	TUIPCViaTunnel     Key = "tui.pc.via_tunnel"
	TUIPCViaTunnelNone Key = "tui.pc.via_tunnel_none"
	TUIPCFree          Key = "tui.pc.free"
	TUIPCUsedBy        Key = "tui.pc.used_by"
	TUIPCUsedByDey     Key = "tui.pc.used_by_dey"
	TUIPCUsedUnknown   Key = "tui.pc.used_unknown"
	TUIPCSuggest       Key = "tui.pc.suggest"
	TUIPCOpen          Key = "tui.pc.open"
	TUIPCClosed        Key = "tui.pc.closed"
	TUIPCOpenCmd       Key = "tui.pc.open_cmd"
	TUIPCYesRTT        Key = "tui.pc.yes_rtt"
	TUIPCNotTested     Key = "tui.pc.not_tested"
	TUIPCNoTunnel      Key = "tui.pc.no_tunnel"
	TUIPCNoteIran      Key = "tui.pc.note_iran"
	TUIPCOpenItem      Key = "tui.pc.open_item"
	TUIPCOpenTitle     Key = "tui.pc.open_title"
	TUIPCOpenConfirm   Key = "tui.pc.open_confirm"
	TUIPCOpenRan       Key = "tui.pc.open_ran"
	TUIPCOpenNow       Key = "tui.pc.open_now"
	TUIPCOpenStill     Key = "tui.pc.open_still"
	TUIPCOpenNothing   Key = "tui.pc.open_nothing"
	TUIPCOpenBack      Key = "tui.pc.open_back"

	// 5 failover
	TUIFoPolicy        Key = "tui.fo.policy"
	TUIFoLadder        Key = "tui.fo.ladder"
	TUIFoBackups       Key = "tui.fo.backups"
	TUIFoPause         Key = "tui.fo.pause"
	TUIFoReset         Key = "tui.fo.reset"
	TUIFoTest          Key = "tui.fo.test"
	TUIFoThresholds    Key = "tui.fo.thresholds"
	TUIPolTTN          Key = "tui.fo.pol_ttn"
	TUIPolTO           Key = "tui.fo.pol_to"
	TUIPolNO           Key = "tui.fo.pol_no"
	TUIPolPick         Key = "tui.fo.pol_pick"
	TUIPolSet          Key = "tui.fo.pol_set"
	TUIBkHeader        Key = "tui.fo.bk_header"
	TUIBkAdd           Key = "tui.fo.bk_add"
	TUIBkRemove        Key = "tui.fo.bk_remove"
	TUIBkWarning       Key = "tui.fo.bk_warning"
	TUIBkAddConfirm    Key = "tui.fo.bk_add_confirm"
	TUIBkReady         Key = "tui.fo.bk_ready"
	TUIBkRemoveLost    Key = "tui.fo.bk_remove_lost"
	TUIBkRemoved       Key = "tui.fo.bk_removed"
	TUIBkNoCandidates  Key = "tui.fo.bk_no_candidates"
	TUIBkNoBackups     Key = "tui.fo.bk_no_backups"
	TUIFoPaused        Key = "tui.fo.paused"
	TUIFoResumed       Key = "tui.fo.resumed"
	TUIFoResetConfirm  Key = "tui.fo.reset_confirm"
	TUIFoResetDone     Key = "tui.fo.reset_done"
	TUIFoTestLost      Key = "tui.fo.test_lost"
	TUIFoTestSkipped   Key = "tui.fo.test_skipped"
	TUIFoTestNoResults Key = "tui.fo.test_no_results"

	// 6 diagnostics
	TUIDgPortCheck  Key = "tui.dg.port_check"
	TUIDgTunnelTest Key = "tui.dg.tunnel_test"
	TUIDgSpeed      Key = "tui.dg.speed"
	TUIDgLogs       Key = "tui.dg.logs"
	TUIDgDoctor     Key = "tui.dg.doctor"
	TUIProbeNone    Key = "tui.dg.probe_none"
	TUISpSeconds    Key = "tui.dg.sp_seconds"
	TUISpResult     Key = "tui.dg.sp_result"
	TUISpVia        Key = "tui.dg.sp_via"
	TUILogTarget    Key = "tui.dg.log_target"
	TUILogHub       Key = "tui.dg.log_hub"
	TUILogNode      Key = "tui.dg.log_node"
	TUILogTunnel    Key = "tui.dg.log_tunnel"
	TUILogFollow    Key = "tui.dg.log_follow"
	TUILogScrolled  Key = "tui.dg.log_scrolled"
	TUILogEmpty     Key = "tui.dg.log_empty"
	TUILogEnded     Key = "tui.dg.log_ended"
	TUIDocSaved     Key = "tui.dg.doc_saved"

	// 7 optimize
	TUIOpApply         Key = "tui.op.apply"
	TUIOpRevert        Key = "tui.op.revert"
	TUIOpBBR           Key = "tui.op.bbr"
	TUIOpLimits        Key = "tui.op.limits"
	TUIOpHeader        Key = "tui.op.header"
	TUIOpBBRActive     Key = "tui.op.bbr_active"
	TUIOpBBRAvail      Key = "tui.op.bbr_avail"
	TUIOpBBRNone       Key = "tui.op.bbr_none"
	TUIOpPick          Key = "tui.op.pick"
	TUIOpBalanced      Key = "tui.op.balanced"
	TUIOpAggressive    Key = "tui.op.aggressive"
	TUIOpOff           Key = "tui.op.off"
	TUIOpApplyConfirm  Key = "tui.op.apply_confirm"
	TUIOpRevertConfirm Key = "tui.op.revert_confirm"
	TUIOpApplied       Key = "tui.op.applied"
	TUIOpReverted      Key = "tui.op.reverted"
	TUIOpBBRHint       Key = "tui.op.bbr_hint"
	TUIOpNoValues      Key = "tui.op.no_values"
	TUIOpRecommend     Key = "tui.op.recommend"
	TUIOpSmallRAM      Key = "tui.op.small_ram"

	// 8 security
	TUISeRotate         Key = "tui.se.rotate"
	TUISeTLS            Key = "tui.se.tls"
	TUISeRenew          Key = "tui.se.renew"
	TUISeFirewall       Key = "tui.se.firewall"
	TUISeAudit          Key = "tui.se.audit"
	TUISeFingerprints   Key = "tui.se.fingerprints"
	TUISeAllTunnels     Key = "tui.se.all_tunnels"
	TUISeRotateLost     Key = "tui.se.rotate_lost"
	TUISeRotated        Key = "tui.se.rotated"
	TUISeExpires        Key = "tui.se.expires"
	TUISeNoCerts        Key = "tui.se.no_certs"
	TUISeFwShow         Key = "tui.se.fw_show"
	TUISeFwApply        Key = "tui.se.fw_apply"
	TUISeFwDisable      Key = "tui.se.fw_disable"
	TUISeFwManaged      Key = "tui.se.fw_managed"
	TUISeFwDetected     Key = "tui.se.fw_detected"
	TUISeFwSuggested    Key = "tui.se.fw_suggested"
	TUISeFwRules        Key = "tui.se.fw_rules"
	TUISeFwDisableLost  Key = "tui.se.fw_disable_lost"
	TUISeFwApplyConfirm Key = "tui.se.fw_apply_confirm"
	TUISeAuditClean     Key = "tui.se.audit_clean"
	TUISeFpNode         Key = "tui.se.fp_node"
	TUISeTLSShow        Key = "tui.se.tls_show"
	TUISeDomain         Key = "tui.se.domain"
	TUISeACMEEmail      Key = "tui.se.acme_email"
	TUISeCFToken        Key = "tui.se.cf_token" // #nosec G101 -- i18n key, not a credential
	TUISeDomainField    Key = "tui.se.domain_field"
	TUISeDomainHint     Key = "tui.se.domain_hint"
	TUISeDomainSet      Key = "tui.se.domain_set"
	TUISeDomainCleared  Key = "tui.se.domain_cleared"
	TUISeEmailField     Key = "tui.se.email_field"
	TUISeEmailHint      Key = "tui.se.email_hint"
	TUISeEmailSet       Key = "tui.se.email_set"
	TUISeEmailCleared   Key = "tui.se.email_cleared"
	TUISeTokenField     Key = "tui.se.token_field"   // #nosec G101 -- i18n key, not a credential
	TUISeTokenHint      Key = "tui.se.token_hint"    // #nosec G101 -- i18n key, not a credential
	TUISeTokenSet       Key = "tui.se.token_set"     // #nosec G101 -- i18n key, not a credential
	TUISeTokenCleared   Key = "tui.se.token_cleared" // #nosec G101 -- i18n key, not a credential
	TUISeDomainLabel    Key = "tui.se.tls_domain_label"
	TUISeChLabel        Key = "tui.se.tls_challenge_label"
	TUISeEmailLabel     Key = "tui.se.tls_email_label"
	TUISeNoDomain       Key = "tui.se.no_domain"
	TUISeChHTTP         Key = "tui.se.ch_http"
	TUISeChDNS          Key = "tui.se.ch_dns"
	TUISeChNone         Key = "tui.se.ch_none"

	// 9 notifications
	TUINtSet        Key = "tui.nt.set"
	TUINtTest       Key = "tui.nt.test"
	TUINtOff        Key = "tui.nt.off"
	TUINtTokenFile  Key = "tui.nt.token_file" // #nosec G101 -- i18n key, not a credential
	TUINtTokenHint  Key = "tui.nt.token_hint" // #nosec G101 -- i18n key, not a credential
	TUINtChat       Key = "tui.nt.chat"
	TUINtEvents     Key = "tui.nt.events"
	TUINtOn         Key = "tui.nt.on"
	TUINtSent       Key = "tui.nt.sent"
	TUINtOffDone    Key = "tui.nt.off_done"
	TUINtOffConfirm Key = "tui.nt.off_confirm"

	// 10 backup & restore
	TUIBuCreate    Key = "tui.bu.create"
	TUIBuRestore   Key = "tui.bu.restore"
	TUIBuOut       Key = "tui.bu.out"
	TUIBuEncrypt   Key = "tui.bu.encrypt"
	TUIBuPass      Key = "tui.bu.pass"  // #nosec G101 -- i18n key, not a credential
	TUIBuPass2     Key = "tui.bu.pass2" // #nosec G101 -- i18n key, not a credential
	TUIBuMismatch  Key = "tui.bu.mismatch"
	TUIBuSaved     Key = "tui.bu.saved"
	TUIBuPlainWarn Key = "tui.bu.plain_warn"
	TUIBuPath      Key = "tui.bu.path"
	TUIBuRestPass  Key = "tui.bu.rest_pass" // #nosec G101 -- i18n key, not a credential
	TUIBuRestored  Key = "tui.bu.restored"

	// 10 backup & restore: hub move (section 5) and the node menu
	TUIBuMovedIntro      Key = "tui.bu.moved_intro"
	TUIBuMoveField       Key = "tui.bu.move_field"
	TUIBuAnnounce        Key = "tui.bu.announce"
	TUIBuAnnounceIntro   Key = "tui.bu.announce_intro"
	TUIBuNewAddr         Key = "tui.bu.new_addr"
	TUIBuNewAddrHint     Key = "tui.bu.new_addr_hint"
	TUIBuAnnounceConfirm Key = "tui.bu.announce_confirm"
	TUIBuAnnounceOffline Key = "tui.bu.announce_offline"
	TUIBuSetHub          Key = "tui.bu.set_hub"
	TUIBuSetHubIntro     Key = "tui.bu.set_hub_intro"
	TUIBuHubAddr         Key = "tui.bu.hub_addr"
	TUIBuHubAddrHint     Key = "tui.bu.hub_addr_hint"
	TUIWantHostPort      Key = "tui.want_host_port"
	TUIHubOnlyNote       Key = "tui.hub_only_note"

	// 11 update
	TUIUpCheck           Key = "tui.up.check"
	TUIUpApply           Key = "tui.up.apply"
	TUIUpBackends        Key = "tui.up.backends"
	TUIUpManifest        Key = "tui.up.manifest"
	TUIUpRollback        Key = "tui.up.rollback"
	TUIUpCurrent         Key = "tui.up.current"
	TUIUpLatest          Key = "tui.up.latest"
	TUIUpAvailable       Key = "tui.up.available"
	TUIUpNone            Key = "tui.up.none"
	TUIUpChangelog       Key = "tui.up.changelog"
	TUIUpApplyConfirm    Key = "tui.up.apply_confirm"
	TUIUpApplied         Key = "tui.up.applied"
	TUIUpBackendsConfirm Key = "tui.up.backends_confirm"
	TUIUpNoBackends      Key = "tui.up.no_backends"
	TUIUpManifestSource  Key = "tui.up.manifest_source"
	TUIUpRollbackConfirm Key = "tui.up.rollback_confirm"
	TUIUpRolledBack      Key = "tui.up.rolled_back"

	// 12 settings
	TUIStMode           Key = "tui.st.mode"
	TUIStLang           Key = "tui.st.lang"
	TUIStUninstall      Key = "tui.st.uninstall"
	TUIStModePick       Key = "tui.st.mode_pick"
	TUIStSimpleDesc     Key = "tui.st.simple_desc"
	TUIStAdvDesc        Key = "tui.st.adv_desc"
	TUIStModeSet        Key = "tui.st.mode_set"
	TUIStLangPick       Key = "tui.st.lang_pick"
	TUIStLangEn         Key = "tui.st.lang_en"
	TUIStLangSet        Key = "tui.st.lang_set"
	TUIStKeepBackups    Key = "tui.st.keep_backups"
	TUIStNodes          Key = "tui.st.nodes"
	TUIStUninstallLost  Key = "tui.st.uninstall_lost"
	TUIStUninstallKeep  Key = "tui.st.uninstall_keep"
	TUIStUninstallAll   Key = "tui.st.uninstall_all"
	TUIStUninstallNodes Key = "tui.st.uninstall_nodes"
	TUIStUninstalled    Key = "tui.st.uninstalled"

	// "?" help of each screen
	TUIHelpDashboard Key = "tui.help.dashboard"
	TUIHelpList      Key = "tui.help.list"
	TUIHelpForm      Key = "tui.help.form"
	TUIHelpConfirm   Key = "tui.help.confirm"
	TUIHelpTask      Key = "tui.help.task"
	TUIHelpWizard    Key = "tui.help.wizard"
	TUIHelpLadder    Key = "tui.help.ladder"
	TUIHelpLogs      Key = "tui.help.logs"
	TUIHelpTunnels   Key = "tui.help.tunnels"
	TUIHelpNodes     Key = "tui.help.nodes"
	TUIHelpPorts     Key = "tui.help.ports"
	TUIHelpFailover  Key = "tui.help.failover"
	TUIHelpDiag      Key = "tui.help.diag"
	TUIHelpOptimize  Key = "tui.help.optimize"
	TUIHelpSecurity  Key = "tui.help.security"
	TUIHelpTLS       Key = "tui.help.tls"
	TUIHelpNotify    Key = "tui.help.notify"
	TUIHelpBackup    Key = "tui.help.backup"
	TUIHelpUpdate    Key = "tui.help.update"
	TUIHelpSettings  Key = "tui.help.settings"

	// review additions: numeric ladder editor, running changes, line mode
	TUIStillRunning      Key = "tui.still_running"
	TUILadAddItem        Key = "tui.lad.add_item"
	TUILadProfileItem    Key = "tui.lad.profile_item"
	TUILadSaveItem       Key = "tui.lad.save_item"
	TUILadRungTitle      Key = "tui.lad.rung_title"
	TUILadUp             Key = "tui.lad.up"
	TUILadDown           Key = "tui.lad.down"
	TUILadRemove         Key = "tui.lad.remove"
	TUIDashUpdatedManual Key = "tui.dash.updated_manual"
	TUIWizNewTunnel      Key = "tui.wiz.new_tunnel"
	TUIWizTLS            Key = "tui.wiz.tls"
	TUILineModeHint      Key = "tui.line_mode_hint"
)

// tui — English texts (merged into en by init).
var tuiEN = map[Key]string{
	TUIBackItem:       "Back",
	TUIWorking:        "Working…",
	TUIPressEnterBack: "Press Enter or q to go back.",
	TUIRetryHint:      " 1) Retry   0) Back   (r retries)",
	TUIEnterContinue:  "Press Enter to continue, or q / Esc to cancel.",
	TUIConfirmChoice:  " 1) Continue\n 0) Cancel\n\nChoice [1]: ",
	TUIMinute1:        "1 minute",
	TUIMinutes:        "%d minutes",
	TUIRequired:       "A value is required.",
	TUINone:           "none",
	TUIYes:            "yes",
	TUINo:             "no",
	TUIAnswerYN:       "Answer y or n.",
	TUINotAvailable:   "Not available here: run this action with the deyroute command on the server.",
	TUICurrent:        " (current)",
	TUINothingChanged: "Nothing changed.",
	TUIms:             "%dms",
	TUIUptimeDays:     "%dd %02d:%02d",
	TUIUptimeClock:    "%02d:%02d:%02d",
	TUIDash:           "-",
	TUIWantNumber:     "Enter a whole number.",
	TUIWantDomain:     "Enter a domain name such as vpn.example.com (no IP address), or - to remove it.",
	TUIWantAbsPath:    "Enter an absolute path such as /root/cloudflare.token, or - to remove it.",
	TUIWantOneOf:      "Enter one of: %s",
	TUIDone:           "Done.",
	TUIKeepHint:       "Press Enter to keep the value in brackets; Esc cancels.",
	TUIPickTunnel:     "Choose a tunnel:",
	TUIPickNode:       "Choose a node:",
	TUINoTunnels:      "No tunnels yet. Add one with 2) Tunnels → 1) Add tunnel.",
	TUINotSetUpTitle:  "This server is not set up yet.",
	TUINotSetUpHub:    "Iran server (hub):      deyroute setup",
	TUINotSetUpNode:   "Foreign server (node):  paste the join command of the hub menu, 3) Nodes → 1) Show join command",
	TUINoNodes:        "No nodes yet. Show the join command with 3) Nodes → 1) Show join command.",
	TUIClientIP:       "client IP: %s",
	TUIClientKept:     "preserved",
	TUIClientMasked:   "masked",
	TUIClientMaskedPP: "masked (preserved with advanced.proxy_protocol)",

	TUIStateUp:          "UP",
	TUIStateDegraded:    "DEGR",
	TUIStateDegradedSvc: "DEGR (service down)",
	TUIStateSwitching:   "SWITCHING",
	TUIStateStarting:    "STARTING",
	TUIStateInit:        "INIT",
	TUIStateDown:        "DOWN",
	TUIStateDisabled:    "DISABLED",
	TUIStatePaused:      "PAUSED",
	TUIOnline:           "online",
	TUIOffline:          "offline",

	TUIDashTunnels:      "TUNNELS",
	TUIDashNodes:        "NODES",
	TUIDashEvents:       "LAST EVENTS",
	TUIDashNode:         "NODE",
	TUIColNum:           "#",
	TUIColName:          "NAME",
	TUIColNode:          "NODE (active)",
	TUIColTransport:     "TRANSPORT",
	TUIColState:         "STATE",
	TUIColRTT:           "RTT",
	TUIColUptime:        "UP-TIME",
	TUIColPorts:         "PORTS",
	TUINodeCtl:          "ctl %s",
	TUINodeVersion:      "v%s",
	TUINodeCPU:          "cpu %.0f%%",
	TUINodeRAM:          "ram %3dMB",
	TUIDashNoTunnels:    "No tunnels yet. Add one: 2) Tunnels → 1) Add tunnel",
	TUIDashNoNodes:      "No nodes yet. Show the join command: 3) Nodes → 1) Show join command",
	TUIDashNoEvents:     "No events yet.",
	TUIDashHub:          "hub %s",
	TUIDashConnected:    "connected",
	TUIDashDisconnected: "not connected",
	TUIDashLastContact:  "last contact %s",
	TUIDashUnits:        "units: %s",
	TUIDashUpdated:      "Updated %s · refreshes every %ds",
	TUIEvUp:             "up",
	TUIEvDegraded:       "degraded",
	TUIEvDown:           "down",
	TUIEvSwitch:         "switch",

	TUITunAdd:            "Add tunnel",
	TUITunEdit:           "Edit tunnel",
	TUITunToggle:         "Enable / disable",
	TUITunRestart:        "Restart",
	TUITunSwitch:         "Switch transport",
	TUITunDelete:         "Delete",
	TUITunShow:           "Show details",
	TUITunEnabled:        "Tunnel %s is enabled.",
	TUITunDisabled:       "Tunnel %s is disabled.",
	TUITunDisableConfirm: "Disabling tunnel %s stops forwarding its ports (%s) until you enable it again.",
	TUITunRestartConfirm: "Restarting tunnel %s interrupts its connections for a few seconds.",
	TUITunRestarted:      "Tunnel %s restarted.",
	TUITunDeleteLost: "Deleting tunnel %s permanently removes:\n" +
		"  - its entry in config.yaml\n" +
		"  - the warm units of every transport on the hub and on node(s) %s\n" +
		"  - its firewall rules and NAT entries\n" +
		"  - its tokens, keys and TLS certificates\n" +
		"  - its state, probe history and metrics\n" +
		"Ports %s stop forwarding immediately. This cannot be undone.",
	TUITunDeleted:    "Tunnel %s deleted.",
	TUITunSwitchPick: "Switch tunnel %s to:",
	TUITunSwitchNode: "node %s",
	TUITunSwitched:   "Tunnel %s switched to %s.",
	TUITunUpdated:    "Tunnel %s updated.",
	TUIEditName:      "Name",
	TUIEditPolicy:    "Failover policy",
	TUIEditLadder:    "Ladder profile",
	TUIEditTLS:       "TLS mode",
	TUIEditTLSCert:   "TLS certificate file",
	TUIEditTLSKey:    "TLS key file",
	TUIEditProbe:     "Probe port (empty = first TCP port)",
	TUIDetID:         "ID",
	TUIDetName:       "Name",
	TUIDetState:      "State",
	TUIDetVia:        "via %s on %s",
	TUIDetPorts:      "Ports",
	TUIDetNodes:      "Nodes",
	TUIDetPrimary:    "primary",
	TUIDetBackup:     "backup",
	TUIDetClientIP:   "Client IP",
	TUIDetPolicy:     "Policy",
	TUIDetLadder:     "Ladder",
	TUIDetTLS:        "TLS",
	TUIDetRungs:      "Rungs",
	TUIDetEvents:     "Recent events",
	TUIDetWarm:       "warm",
	TUIDetActive:     "active",
	TUIDetSkipped:    "skipped: %s",
	TUIDetQuarantine: "quarantined until %s",

	TUIWizQNode:           "1. Which node?",
	TUIWizNoNode:          "No node is online. Join a node first: 3) Nodes → 1) Show join command.",
	TUIWizAutoNode:        "Only one node is online: %s. It is used for this tunnel.",
	TUIWizQPorts:          "2. Ports?",
	TUIWizPortsHint:       "Examples: 443,2053,8443   443/tcp,27015/udp   2000-2010   443:8443",
	TUIWizPortsPrompt:     "Ports",
	TUIWizChecking:        "Checking ports…",
	TUIWizPortFree:        "%s is free",
	TUIWizPortFirewall:    "the firewall (%s) blocks it: users cannot reach it until it is opened.",
	TUIWizPortRun:         "Open it with: %s",
	TUIWizPortUnreachable: "node %s cannot reach it: open it in the provider's firewall panel (DEY-P014).",
	TUIWizPortBusy:        "%s is used by %s",
	TUIWizPortBusyAny:     "%s is used by another program",
	TUIWizPortError:       "%s cannot be used:",
	TUIWizChange:          "Change port",
	TUIWizSkip:            "Skip",
	TUIWizStop:            "Stop that service (deyroute tunnel %s)",
	TUIWizNewPort:         "New port for %s",
	TUIWizSuggest:         "Free suggestions: %s",
	TUIWizNoPortsLeft:     "No ports left. Enter the ports again.",
	TUIWizDupPort:         "%s is already in the list.",
	TUIWizStopConfirm:     "Disabling tunnel %s stops forwarding all of its ports so that %s becomes free. Enable it again later with 2) Tunnels → 3) Enable / disable.",
	TUIWizOpenFw:          "Open it in the firewall: %s",
	TUIWizOpenFwAll:       "Open the %d blocked ports in the firewall",
	TUIWizOpenConfirmAll:  "To open %s, deyroute runs these commands on the hub:\n\n%s\n\nAfterwards anyone on the internet can connect to these ports.",
	TUIWizKeep:            "Keep it and continue (users may not reach it)",
	TUIWizKeepAll:         "Keep these %d ports and continue (users may not reach them)",
	TUIWizOpened:          "Ran %d firewall command(s).",
	TUIWizQConfirm:        "3. Confirm",
	TUIWizSumNode:         "Node",
	TUIWizSumPorts:        "Ports",
	TUIWizSumLadder:       "Ladder",
	TUIWizSumBackup:       "Backup",
	TUIWizSumName:         "Name",
	TUIWizSumPolicy:       "Policy",
	TUIWizSumTLS:          "TLS mode",
	TUIWizSumThresh:       "Thresholds",
	TUIWizAutomatic:       "automatic",
	TUIWizDefault:         "%s ladder",
	TUIWizDefaultWord:     "default",
	TUIWizCustom:          "custom",
	TUIWizCreate:          "Press Enter to create the tunnel.",
	TUIWizAdvOptions:      "Change advanced options",
	TUIWizCreateItem:      "Create",
	TUIWizAdvIntro:        "Advanced options. Press Enter to keep the value in brackets.",
	TUIWizName:            "Tunnel name (empty = automatic)",
	TUIWizTarget:          "Target for %s (host:port)",
	TUIWizProbe:           "Probe kind of %s",
	TUIWizSumProbe:        "probe %s",
	TUIWizBackupQ:         "Backup node",
	TUIWizThreshQ:         "Customize failover thresholds? (y/n)",
	TUITunnelUp:           "Tunnel %s is UP via %s (%dms)",
	TUITunnelCreated:      "Tunnel %s was created; it is %s now. Watch it on 1) Dashboard.",
	TUIThProbeInterval:    "Probe interval (seconds)",
	TUIThProbeTimeout:     "Probe timeout (seconds)",
	TUIThFail:             "Failed probes before a switch",
	TUIThRecover:          "Good probes to recover",
	TUIThFailback:         "Fail back to rung 1 (y/n)",
	TUIThFailbackAfter:    "Fail back after (seconds)",
	TUIThMaxSwitches:      "Max automatic switches per hour",
	TUIThQuarantine:       "Quarantine of a failed transport (seconds)",
	TUIThIntro:            "Failover thresholds of %s. Press Enter to keep the value in brackets.",

	TUILadTitle:    "Ladder of %s",
	TUILadGuideIf:  "If ...",
	TUILadGuideDo:  "Suggestion",
	TUILadG1If:     "speed matters more than hiding",
	TUILadG1Do:     "put tcpmux first",
	TUILadG2If:     "filtering recognizes the tunnel TLS",
	TUILadG2Do:     "move xray/reality up",
	TUILadG3If:     "UDP is open in the Iran datacenter",
	TUILadG3Do:     "put hysteria2/udp second",
	TUILadG4If:     "the node only has TCP services",
	TUILadG4Do:     "remove the udp rungs",
	TUILadKeys:     "number + Enter: a rung to move or remove it, or an action · shortcuts: u up · d down · x remove · a add · p profile · s save",
	TUILadEmpty:    "The ladder is empty; add at least one transport.",
	TUILadAdd:      "Add a transport:",
	TUILadProfile:  "Use a ladder profile:",
	TUILadSaved:    "Ladder of %s saved: %s",
	TUILadNoneLeft: "Every known transport is already in the ladder.",
	TUILadBuiltin:  " (built-in)",

	TUINdJoin:         "Show join command",
	TUINdList:         "List",
	TUINdRename:       "Rename",
	TUINdRemove:       "Remove",
	TUINdTest:         "Test",
	TUINdJoinIntro:    "Run this one line on the new node (as root):",
	TUINdJoinExpires:  "Single use; expires at %s (in %s). Press r for a new command.",
	TUINdJoinCopy:     "The command was shown on a plain screen so it can be copied whole; deyroute node join-command prints a new one too.",
	TUINdJoinPlainEnd: "Copy the whole line above, then press Enter to return to the menu.",
	TUIMoreLines:      "(%d more lines: make the window taller to see them)",
	TUINdNewName:      "New name for %s",
	TUINdRenamed:      "Node %s renamed to %s.",
	TUINdRemoveLost:   "Removing node %s (%s):\n  - stops every tunnel transport on it (tunnels: %s)\n  - revokes its certificate, so it cannot connect again without a new join\n  - removes it from the firewall allow list\nA tunnel whose only node it is must get another node first (Failover → Backup nodes) or be deleted.",
	TUINdRemoved:      "Node %s removed.",
	TUINdControl:      "Control channel",
	TUINdUDP:          "UDP echo",
	TUINdUDPBlocked:   "blocked (UDP transports are skipped)",
	TUINdVersion:      "Version",
	TUINdCompatible:   "compatible",
	TUINdIncompatible: "incompatible: update both servers to the same release",
	TUINdLastSeen:     "Last heartbeat",
	TUINdFingerprint:  "Certificate",
	TUINdTunnels:      "Tunnels",
	TUINdIP:           "Public IP",
	TUINdLastError:    "Last error",

	TUIPtAdd:        "Add port to tunnel",
	TUIPtRemove:     "Remove port",
	TUIPtCheck:      "Check port",
	TUIPtFirewall:   "Firewall status",
	TUIPtInput:      "Ports to add",
	TUIPtAddConfirm: "Adding %s to tunnel %s re-renders it and restarts its active transport (interruption up to 3 seconds).",
	TUIPtAdded:      "Ports of %s: %s",
	TUIPtPick:       "Choose the port to remove:",
	TUIPtRemoveLost: "Removing %s from tunnel %s stops forwarding that port for good; the active transport restarts (interruption up to 3 seconds).",
	TUIPtNoPorts:    "Tunnel %s has no ports.",
	TUIPtProbe:      "Probe kind *",
	TUIPtProbeTitle: "Probe kind: %s",
	TUIPtProbeField: "Probe kind of the TCP ports",
	TUIPtProbeHint:  "auto: a TLS hello, any answer counts · tcp: the connection opens\ntls: a TLS handshake or alert · http: an HTTP status line",
	TUIPtProbePick:  "Choose the port whose probe kind to change:",
	TUIPtProbeNoTCP: "Tunnel %s has no TCP ports; UDP port maps are not probed by type.",
	TUIPtProbePort:  "%s  probe: %s",
	TUIPtProbeKind:  "How should the health probe test %s of tunnel %s (now %s)?",
	TUIPtProbeSet:   "Probe kind of %s in tunnel %s: %s.",
	TUIProbeAuto:    "auto  a TLS hello; any answer counts (default)",
	TUIProbeTCP:     "tcp   the TCP connection opens",
	TUIProbeTLS:     "tls   a TLS handshake or TLS alert comes back",
	TUIProbeHTTP:    "http  an HTTP status line comes back (HEAD /)",

	TUIPCPort:          "Port (e.g. 443 or 27015/udp)",
	TUIPCNode:          "Node id (empty = first online node)",
	TUIPCOnePort:       "Enter exactly one port.",
	TUIPCBind:          "local bind",
	TUIPCFirewall:      "firewall",
	TUIPCFromNode:      "reachable from node %s",
	TUIPCFromNodeNone:  "reachable from a node",
	TUIPCViaTunnel:     "reachable via tunnel %s",
	TUIPCViaTunnelNone: "reachable via tunnel",
	TUIPCFree:          "free",
	TUIPCUsedBy:        "used by %s",
	TUIPCUsedByDey:     "used by %s (deyroute)",
	TUIPCUsedUnknown:   "used by another program",
	TUIPCSuggest:       "free ports: %s",
	TUIPCOpen:          "open (%s)",
	TUIPCClosed:        "closed (%s)",
	TUIPCOpenCmd:       "open it: %s",
	TUIPCYesRTT:        "yes (%s)",
	TUIPCNotTested:     "not tested (no online node)",
	TUIPCNoTunnel:      "not part of a tunnel",
	TUIPCNoteIran:      "filtering inside Iran is not measured",
	TUIPCOpenItem:      "Open it in the firewall: %s",
	TUIPCOpenTitle:     "Open %s in the firewall",
	TUIPCOpenConfirm:   "To open %s, deyroute runs this command on the hub (%s):\n\n  %s\n\nAfterwards anyone on the internet can connect to this port.",
	TUIPCOpenRan:       "Ran: %s",
	TUIPCOpenNow:       "%s is open in the firewall now (%s).",
	TUIPCOpenStill:     "%s is still closed by %s.",
	TUIPCOpenNothing:   "No external firewall blocks %s any more; nothing was run.",
	TUIPCOpenBack:      "Go back to check the port again.",

	TUIFoPolicy:        "Policy",
	TUIFoLadder:        "Ladder order",
	TUIFoBackups:       "Backup nodes",
	TUIFoPause:         "Pause / resume failover",
	TUIFoReset:         "Reset to rung 1",
	TUIFoTest:          "Test ladder",
	TUIFoThresholds:    "Thresholds *",
	TUIPolTTN:          "transport_then_node - next transport on the same node, then the next node",
	TUIPolTO:           "transport_only - only transports; the node stays fixed",
	TUIPolNO:           "node_only - the same transport on the next node",
	TUIPolPick:         "Failover policy of %s:",
	TUIPolSet:          "Policy of %s is now %s.",
	TUIBkHeader:        "Backup nodes of %s: %s",
	TUIBkAdd:           "Add backup node",
	TUIBkRemove:        "Remove backup node",
	TUIBkWarning:       "Backup only works if the same service runs on both nodes.",
	TUIBkAddConfirm:    "Node %s becomes a backup of tunnel %s; every transport of its ladder is warmed up on it.",
	TUIBkReady:         "backup %s ready (warm)",
	TUIBkRemoveLost:    "Removing backup node %s from tunnel %s deletes the warm units of this tunnel on it; %s can no longer fail over to %s.",
	TUIBkRemoved:       "Backup node %s removed from %s.",
	TUIBkNoCandidates:  "No other node is available. Join another node first: 3) Nodes → 1) Show join command.",
	TUIBkNoBackups:     "Tunnel %s has no backup node.",
	TUIFoPaused:        "Failover of %s is paused: probes continue, automatic switches stop.",
	TUIFoResumed:       "Failover of %s is running again.",
	TUIFoResetConfirm:  "Tunnel %s switches to rung 1 on its primary node now (a short interruption).",
	TUIFoResetDone:     "Tunnel %s is back on rung 1.",
	TUIFoTestLost:      "Testing the ladder of %s tries every transport for 20 seconds, one after the other.\nThe tunnel is interrupted during the test (about %s); users lose their connections.",
	TUIFoTestSkipped:   "skipped: %s",
	TUIFoTestNoResults: "No rung was tested.",

	TUIDgPortCheck:  "Port check",
	TUIDgTunnelTest: "Tunnel test",
	TUIDgSpeed:      "Speed test",
	TUIDgLogs:       "Logs",
	TUIDgDoctor:     "Doctor",
	TUIProbeNone:    "No ports to probe.",
	TUISpSeconds:    "Duration in seconds",
	TUISpResult:     "download %.1f Mbps · upload %.1f Mbps · RTT %s",
	TUISpVia:        "via %s, %.0f seconds",
	TUILogTarget:    "Show the log of:",
	TUILogHub:       "hub (deyroute-hub service)",
	TUILogNode:      "node (deyroute-node service)",
	TUILogTunnel:    "tunnel %s",
	TUILogFollow:    "live · Up/Down PgUp/PgDn scroll · q stops",
	TUILogScrolled:  "lines %d-%d of %d · End returns to live · q stops",
	TUILogEmpty:     "Waiting for log lines…",
	TUILogEnded:     "The log stream ended. Press r to restart it.",
	TUIDocSaved:     "Doctor file: %s",

	TUIOpApply:         "Apply profile",
	TUIOpRevert:        "Revert",
	TUIOpBBR:           "BBR",
	TUIOpLimits:        "Limits *",
	TUIOpHeader:        "Profile: %s   BBR: %s",
	TUIOpBBRActive:     "active",
	TUIOpBBRAvail:      "available, not active",
	TUIOpBBRNone:       "not available in this kernel",
	TUIOpPick:          "Kernel tuning profile:",
	TUIOpBalanced:      "balanced - larger buffers, BBR, fq (the default)",
	TUIOpAggressive:    "aggressive - 64MB buffers for fast links, for servers with 4 GB RAM or more",
	TUIOpOff:           "off - do not change kernel settings",
	TUIOpApplyConfirm:  "The %s sysctl profile is written to /etc/sysctl.d/99-deyroute.conf and applied now. The kernel values from before deyroute are kept and can be restored with Revert.",
	TUIOpRevertConfirm: "The kernel settings saved before deyroute changed them are restored and 99-deyroute.conf is removed.",
	TUIOpApplied:       "Profile %s applied.",
	TUIOpReverted:      "Kernel settings restored (profile %s).",
	TUIOpBBRHint:       "BBR is switched on by automatic tuning and by the balanced and aggressive profiles: 1) Automatic tuning or 2) Apply profile.",
	TUIOpNoValues:      "No kernel values are applied by deyroute.",
	TUIOpRecommend:     "This hub has %d MB RAM: %s is recommended (aggressive is for 4 GB or more).",
	TUIOpSmallRAM:      "This hub has only %d MB RAM. aggressive is meant for servers with 4 GB or more; balanced suits this one better. Nodes with less than 4 GB report the same warning.",

	TUISeRotate:         "Rotate tokens",
	TUISeTLS:            "TLS certificates",
	TUISeRenew:          "Renew TLS certificate",
	TUISeFirewall:       "Firewall",
	TUISeAudit:          "Audit",
	TUISeFingerprints:   "View fingerprints *",
	TUISeAllTunnels:     "all tunnels",
	TUISeRotateLost:     "Rotating tokens replaces the secret token of %s. The old token stops working at once; every transport of the affected tunnel(s) restarts with the new token on the hub and on the nodes (a short interruption).",
	TUISeRotated:        "Tokens of %s rotated.",
	TUISeExpires:        "expires %s (%d days left)",
	TUISeNoCerts:        "No certificates.",
	TUISeFwShow:         "Show",
	TUISeFwApply:        "Apply rules",
	TUISeFwDisable:      "Disable",
	TUISeFwManaged:      "Managed by deyroute: %s",
	TUISeFwDetected:     "Detected firewalls: %s",
	TUISeFwSuggested:    "Suggested commands (run them yourself):",
	TUISeFwRules:        "Rules (table inet deyroute):",
	TUISeFwDisableLost:  "Disabling the deyroute firewall deletes the nftables table inet deyroute: the listen ports are no longer opened by deyroute, the control port is no longer limited to your nodes, and the NAT rules of active WireGuard transports stop working.",
	TUISeFwApplyConfirm: "The nftables table inet deyroute is rendered from the configuration and applied atomically.",
	TUISeAuditClean:     "No problems found.",
	TUISeFpNode:         "node %s",
	TUISeTLSShow:        "Show certificates",
	TUISeDomain:         "Domain (for ACME)",
	TUISeACMEEmail:      "ACME e-mail",
	TUISeCFToken:        "Cloudflare token (DNS-01)",
	TUISeDomainField:    "Domain",
	TUISeDomainHint:     "A name such as vpn.example.com with a DNS-only A record pointing at the hub (no Cloudflare proxy). Type - to remove it.",
	TUISeDomainSet:      "Domain set to %s. Tunnels in tls mode acme get their Let's Encrypt certificate at the daily renewal, or now with 8) Security → 3) Renew TLS certificate.",
	TUISeDomainCleared:  "Domain removed.",
	TUISeEmailField:     "ACME e-mail",
	TUISeEmailHint:      "Let's Encrypt sends expiry notices to this address. Type - to remove it.",
	TUISeEmailSet:       "ACME e-mail set to %s.",
	TUISeEmailCleared:   "ACME e-mail removed.",
	TUISeTokenField:     "Cloudflare token file",
	TUISeTokenHint:      "Put a Cloudflare API token with Zone:DNS:Edit in a file first (e.g. /root/cloudflare.token); the token itself is never typed here. It is copied to /etc/deyroute/secrets/cloudflare.token and the file you give may be deleted afterwards. Type - to remove the token (HTTP-01 on port 80 again).",
	TUISeTokenSet:       "DNS-01 through Cloudflare is on; the token is stored in %s. Request the certificates with 8) Security → 3) Renew TLS certificate.",
	TUISeTokenCleared:   "Cloudflare token removed; ACME uses HTTP-01 on port 80 again.",
	TUISeDomainLabel:    "Domain",
	TUISeChLabel:        "ACME check",
	TUISeEmailLabel:     "ACME e-mail",
	TUISeNoDomain:       "none (tls mode acme needs one)",
	TUISeChHTTP:         "HTTP-01 on port 80",
	TUISeChDNS:          "DNS-01 through Cloudflare",
	TUISeChNone:         "none: HTTP-01 is disabled and no Cloudflare token is set",

	TUINtSet:        "Set up Telegram",
	TUINtTest:       "Send test message",
	TUINtOff:        "Turn off",
	TUINtTokenFile:  "Bot token file",
	TUINtTokenHint:  "Put the bot token in a file first (e.g. /root/telegram.token, mode 0600); the token itself is never typed here.",
	TUINtChat:       "Chat id",
	TUINtEvents:     "Events (comma separated)",
	TUINtOn:         "Telegram notifications are on.",
	TUINtSent:       "Test message sent.",
	TUINtOffDone:    "Telegram notifications are off.",
	TUINtOffConfirm: "Telegram notifications stop; the bot token file is not deleted.",

	TUIBuCreate:    "Create backup",
	TUIBuRestore:   "Restore from a backup",
	TUIBuOut:       "Output file (empty = default location)",
	TUIBuEncrypt:   "Encrypt with a passphrase? (y/n)",
	TUIBuPass:      "Passphrase",
	TUIBuPass2:     "Repeat the passphrase",
	TUIBuMismatch:  "The passphrases do not match.",
	TUIBuSaved:     "Backup saved: %s",
	TUIBuPlainWarn: "This backup is not encrypted: it contains every secret of this server. Keep it safe.",
	TUIBuPath:      "Backup file",
	TUIBuRestPass:  "Passphrase (empty if the backup is not encrypted)",
	TUIBuRestored:  "Restore complete. Tunnels were re-rendered from the backup.",

	TUIBuMovedIntro:      "This server's public IP is %s, but the backup's hub address is %s.\nIf the hub moved to this server, the hub takes this server's address and its certificate is re-issued.",
	TUIBuMoveField:       "Use this server's address (the hub moved here)? (y/n)",
	TUIBuAnnounce:        "Announce hub move",
	TUIBuAnnounceIntro:   "Run this on the old hub after its backup was restored on the new server:\nevery online node saves the new address and reconnects there.",
	TUIBuNewAddr:         "New hub address (IP:port)",
	TUIBuNewAddrHint:     "The new server's public IP and the control port, e.g. 5.6.7.9:%d (a restore keeps the port).",
	TUIBuAnnounceConfirm: "Every online node saves %s as its hub address and reconnects there at once.\nThe restored hub must already run at that address: a node told a wrong address\nloses its hub until Set hub address is used on that node.",
	TUIBuAnnounceOffline: "Offline, not told: %s.\nOn each of them use 10) Backup & Restore → 3) Set hub address, or run: deyroute node set-hub %s",
	TUIBuSetHub:          "Set hub address",
	TUIBuSetHubIntro:     "This node connects to hub %s.\nEnter the hub's new address after the hub moved to another server or IP.\nThe address is saved even when the node agent is stopped.",
	TUIBuHubAddr:         "Hub address (IP:port)",
	TUIBuHubAddrHint:     "The hub's public IP and its control port, e.g. 5.6.7.9:44433.",
	TUIWantHostPort:      "Enter the address as IP:port, for example 5.6.7.9:44433.",
	TUIHubOnlyNote:       "%s is managed on the hub (%s): open the menu there.\nThe hub applies every change to this node.",

	TUIUpCheck:           "Check for updates",
	TUIUpApply:           "Apply update",
	TUIUpBackends:        "Update backends",
	TUIUpManifest:        "Backend manifest",
	TUIUpRollback:        "Roll back",
	TUIUpCurrent:         "Installed: %s",
	TUIUpLatest:          "Latest:    %s",
	TUIUpAvailable:       "An update is available: 11) Update → 2) Apply update.",
	TUIUpNone:            "deyroute is up to date.",
	TUIUpChangelog:       "Changelog:",
	TUIUpApplyConfirm:    "Update deyroute %s → %s. The deyroute service restarts; tunnels keep running. The current binary is kept for rollback.",
	TUIUpApplied:         "deyroute updated to %s.",
	TUIUpBackendsConfirm: "New backend versions are installed next to the current ones on the hub and on all nodes. An active transport whose backend changes restarts, and it rolls back automatically if its probe is not green within 60 seconds.",
	TUIUpNoBackends:      "No backend needed an update.",
	TUIUpManifestSource:  "Source: %s",
	TUIUpRollbackConfirm: "The previous deyroute binary is restored and the service restarts; tunnels keep running.",
	TUIUpRolledBack:      "Rolled back to %s.",

	TUIStMode:           "UI mode (Simple / Advanced)",
	TUIStLang:           "Language",
	TUIStUninstall:      "Uninstall",
	TUIStModePick:       "UI mode:",
	TUIStSimpleDesc:     "Simple - the essentials only; ladders are automatic",
	TUIStAdvDesc:        "Advanced - adds failover thresholds, resource limits and certificate fingerprints (items marked *)",
	TUIStModeSet:        "UI mode: %s",
	TUIStLangPick:       "Language:",
	TUIStLangEn:         "English",
	TUIStLangSet:        "Language: %s",
	TUIStKeepBackups:    "Keep backups? (y/n)",
	TUIStNodes:          "Also uninstall deyroute on every online node? (y/n)",
	TUIStUninstallLost:  "Uninstall removes from this server:\n  - every deyroute tunnel unit and the deyroute service (stopped and disabled)\n  - the nftables table inet deyroute\n  - the kernel settings of deyroute (restored from sysctl-before-deyroute.conf)\n  - /etc/deyroute: configuration, secrets and certificates\n  - /var/lib/deyroute: state and backend binaries%s\n  - the deyroute system user and group\n  - the deyroute binary\n%sEvery tunnel stops.",
	TUIStUninstallKeep:  " (backups are kept)",
	TUIStUninstallAll:   ", and every backup",
	TUIStUninstallNodes: "  - deyroute on every online node as well\n",
	TUIStUninstalled:    "deyroute was removed from this server. Press ctrl+c to leave.",

	TUIHelpDashboard: "Live view of tunnels, nodes and the last events; it refreshes every 2 seconds.\nState words: UP green, DEGR yellow, SWITCHING blue, DOWN red, DISABLED/PAUSED gray.\nNarrow terminals (< 100 columns) hide the RTT and UP-TIME columns.\nTRAFFIC (when the hub counts bytes): the last hour, the rates now (↓ download to users,\n↑ upload from users) and today's volume; t opens the charts of a tunnel, a node or the hub.\nr refreshes now; q, Esc or Enter goes back.",
	TUIHelpList:      "Type the number of an item and press Enter; 0 goes back.\nr reloads the list; q or Esc goes back.",
	TUIHelpForm:      "Type the answer and press Enter. Enter on an empty line keeps the value in brackets.\nA numbered question takes the number of the answer. Esc cancels without changes;\n? on an empty line shows this help.",
	TUIHelpConfirm:   "Destructive actions need the word yes typed exactly; anything else cancels.\nOther confirmations: Enter continues, q or Esc cancels.",
	TUIHelpTask:      "Shows the steps of the running action and its result.\nOn an error: 1 + Enter or r retries; 0 + Enter or q goes back.",
	TUIHelpWizard:    "Add tunnel asks at most three questions: node, ports, confirm.\nEach port is checked at once; a busy port can be changed, skipped or (for deyroute tunnels) stopped.\nA port the firewall closes can be opened (after you confirm the exact command), changed, skipped or kept.\nEsc goes back one question; on the first question it leaves the wizard without creating anything.",
	TUIHelpLadder:    "The ladder is tried from the top. Type the number of a rung + Enter to move it up,\ndown or remove it; the numbers after the rungs add a transport, load a profile or save.\nShortcuts on the selected rung: u up, d down, x remove; a add, p profile, s save.\nq or Esc leaves without saving.",
	TUIHelpLogs:      "Log lines arrive live. Up/Down and PgUp/PgDn scroll, End returns to the live view.\nq stops the stream and goes back; r restarts it.",
	TUIHelpTunnels:   "Tunnels forward ports from the hub to a node.\n1 adds a tunnel (node, ports, confirm); the other items act on one tunnel you pick.\nDelete asks you to type yes.",
	TUIHelpNodes:     "Nodes are the foreign servers. 1 shows the one-line join command for a new node\n(single use, 15 minutes). Remove asks you to type yes.",
	TUIHelpPorts:     "Check port runs the four checks: local bind, firewall, reachable from a node,\nreachable through the tunnel. Filtering inside Iran is not measured.\nWhen an external firewall blocks the port, 1 + Enter opens it after you confirm the exact command.\nProbe kind * (Advanced) sets how the health probe tests a TCP port: auto, tcp, tls or http.",
	TUIHelpFailover:  "Failover moves a tunnel to the next transport or node when probes fail.\nBackup nodes need the same service as the primary node. Items marked * need Advanced mode.",
	TUIHelpDiag:      "Port check, tunnel probes, a speed test through the tunnel (on the hub), live logs, the doctor bundle\nand the traffic and load charts of the tunnels, the nodes and the hub (on the hub).",
	TUIHelpOptimize:  "Kernel tuning. Automatic tuning measures every server and lists each change with its reason before it applies it; the fixed profiles (balanced, aggressive) stay available. Check tuning reports values changed by someone else. Revert restores the values from before deyroute.",
	TUIHelpSecurity:  "Rotate tokens replaces tunnel secrets (type yes). TLS certificates shows them and sets the domain for ACME.\nFirewall shows or applies the table inet deyroute.",
	TUIHelpTLS:       "Domain is the name tls mode acme gets a Let's Encrypt certificate for (DNS-only record, no Cloudflare proxy).\nAdvanced: the ACME e-mail and a Cloudflare token for DNS-01 when port 80 is not free. Switch a tunnel to acme with 2) Tunnels → 2) Edit tunnel.",
	TUIHelpNotify:    "Telegram sends one message per event (at most one per minute per tunnel and type).",
	TUIHelpBackup:    "Backups hold /etc/deyroute and the event history, encrypted with a passphrase by default.\nRestore replaces the current configuration (type yes).\nAfter the hub moved: Announce hub move on the old hub tells the online nodes its new address;\nSet hub address on a node sets it there (also with the node agent stopped).",
	TUIHelpUpdate:    "Updates never run without a question; tunnels keep running while deyroute restarts.",
	TUIHelpSettings:  "Simple mode shows the essentials; Advanced adds the items marked * (UI mode and language are set on the hub).\nUninstall removes deyroute from this server (type yes).",

	TUIStillRunning:      "This change is still running and is not abandoned half-way: wait for its result.\nctrl+c quits the menu and interrupts it.",
	TUILadAddItem:        "Add a transport",
	TUILadProfileItem:    "Load a ladder profile",
	TUILadSaveItem:       "Save this ladder",
	TUILadRungTitle:      "Rung %d: %s",
	TUILadUp:             "Move up",
	TUILadDown:           "Move down",
	TUILadRemove:         "Remove from the ladder",
	TUIDashUpdatedManual: "Updated %s · r + Enter refreshes",
	TUIWizNewTunnel:      "(new tunnel)",
	TUIWizTLS:            "TLS mode",
	TUILineModeHint:      "Line mode: type the answer and press Enter · an empty line is Enter · q goes back (esc in a text field) · ? help",
}

// tui — merge the block above into the English table.
func init() {
	for k, v := range tuiEN {
		en[k] = v
	}
}

// cli — strings of the command line (internal/cli): flags, help texts with
// examples, prompts, confirmations and human output of every command of
// section 14. Kept in one block (merged into en by init) so it merges cleanly.
const (
	CLIRootExample               Key = "cli.root_example"
	CLIVersionExample            Key = "cli.version_example"
	CLIGroupStart                Key = "cli.group_start"
	CLIGroupManage               Key = "cli.group_manage"
	CLIGroupDiag                 Key = "cli.group_diag"
	CLIGroupSystem               Key = "cli.group_system"
	CLIUnknownSub                Key = "cli.unknown_sub"
	CLIWantArgs                  Key = "cli.want_args"
	CLIWantArgsRange             Key = "cli.want_args_range"
	CLIWantFlag                  Key = "cli.want_flag"
	CLIWantPositiveDuration      Key = "cli.want_positive_duration"
	CLIJoinTTLRange              Key = "cli.join_ttl_range"
	CLIWantCount                 Key = "cli.want_count"
	CLIWantSeconds               Key = "cli.want_seconds"
	CLIWantShell                 Key = "cli.want_shell"
	CLIWantAbsPath               Key = "cli.want_abs_path"
	CLIWantAbsPathPEM            Key = "cli.want_abs_path_pem"
	CLIWantDomainOrClear         Key = "cli.want_domain_or_clear"
	CLIWantIP                    Key = "cli.want_ip"
	CLIWantName                  Key = "cli.want_name"
	CLINotSetUpFix               Key = "cli.not_set_up_fix"
	CLIInvalidAnswer             Key = "cli.invalid_answer"
	CLIColBackend                Key = "cli.col_backend"
	CLIColCPU                    Key = "cli.col_cpu"
	CLIColCode                   Key = "cli.col_code"
	CLIColCtl                    Key = "cli.col_ctl"
	CLIColCtlPort                Key = "cli.col_ctl_port"
	CLIColDays                   Key = "cli.col_days"
	CLIColDetail                 Key = "cli.col_detail"
	CLIColExpires                Key = "cli.col_expires"
	CLIColFingerprint            Key = "cli.col_fingerprint"
	CLIColFrom                   Key = "cli.col_from"
	CLIColID                     Key = "cli.col_id"
	CLIColKind                   Key = "cli.col_kind"
	CLIColLevel                  Key = "cli.col_level"
	CLIColMessage                Key = "cli.col_message"
	CLIColMode                   Key = "cli.col_mode"
	CLIColNode                   Key = "cli.col_node"
	CLIColNodes                  Key = "cli.col_nodes"
	CLIColPort                   Key = "cli.col_port"
	CLIColPublicIP               Key = "cli.col_public_ip"
	CLIColRAM                    Key = "cli.col_ram"
	CLIColResult                 Key = "cli.col_result"
	CLIColRungs                  Key = "cli.col_rungs"
	CLIColSubject                Key = "cli.col_subject"
	CLIColTime                   Key = "cli.col_time"
	CLIColTo                     Key = "cli.col_to"
	CLIColTunnel                 Key = "cli.col_tunnel"
	CLIColTunnels                Key = "cli.col_tunnels"
	CLIColType                   Key = "cli.col_type"
	CLIColUnit                   Key = "cli.col_unit"
	CLIColUsedBy                 Key = "cli.col_used_by"
	CLIColVersion                Key = "cli.col_version"
	CLIColWho                    Key = "cli.col_who"
	CLIFlagWatch                 Key = "cli.flag_watch"
	CLIFlagRole                  Key = "cli.flag_role"
	CLIFlagSetupName             Key = "cli.flag_setup_name"
	CLIFlagControlPort           Key = "cli.flag_control_port"
	CLIFlagYesSetup              Key = "cli.flag_yes_setup"
	CLIFlagRepair                Key = "cli.flag_repair"
	CLISetupRepaired             Key = "cli.setup_repaired"
	CLIFlagJoinName              Key = "cli.flag_join_name"
	CLIFlagTTL                   Key = "cli.flag_ttl"
	CLIFlagTunnelNode            Key = "cli.flag_tunnel_node"
	CLIFlagPorts                 Key = "cli.flag_ports"
	CLIFlagTunnelName            Key = "cli.flag_tunnel_name"
	CLIFlagLadder                Key = "cli.flag_ladder"
	CLIFlagBackupNode            Key = "cli.flag_backup_node"
	CLIFlagBackupNodeOne         Key = "cli.flag_backup_node_one"
	CLIOneBackupNode             Key = "cli.one_backup_node"
	CLIFlagYesAdd                Key = "cli.flag_yes_add"
	CLIFlagPolicy                Key = "cli.flag_policy"
	CLIFlagProbePort             Key = "cli.flag_probe_port"
	CLIFlagSwitchTransport       Key = "cli.flag_switch_transport"
	CLIFlagSwitchNode            Key = "cli.flag_switch_node"
	CLIFlagTarget                Key = "cli.flag_target"
	CLIFlagProbe                 Key = "cli.flag_probe"
	CLIProbeFix                  Key = "cli.probe_fix"
	CLIFlagCheckNode             Key = "cli.flag_check_node"
	CLIFlagOpenFirewall          Key = "cli.flag_open_firewall"
	CLIFlagCount                 Key = "cli.flag_count"
	CLIFlagRungs                 Key = "cli.flag_rungs"
	CLIFlagSeconds               Key = "cli.flag_seconds"
	CLIFlagAllPorts              Key = "cli.flag_all_ports"
	CLIFlagFollow                Key = "cli.flag_follow"
	CLIFlagLogsSince             Key = "cli.flag_logs_since"
	CLIFlagEventsTunnel          Key = "cli.flag_events_tunnel"
	CLIFlagEventsSince           Key = "cli.flag_events_since"
	CLIFlagDoctorNode            Key = "cli.flag_doctor_node"
	CLIFlagDoctorOut             Key = "cli.flag_doctor_out"
	CLIFlagProfile               Key = "cli.flag_profile"
	CLIFlagRotateTunnel          Key = "cli.flag_rotate_tunnel"
	CLIFlagTLSTunnel             Key = "cli.flag_tls_tunnel"
	CLIFlagCloudflareToken       Key = "cli.flag_cloudflare_token" // #nosec G101 -- i18n key, not a credential
	CLIFlagTLSMode               Key = "cli.flag_tls_mode"
	CLIFlagTLSModeAdd            Key = "cli.flag_tls_mode_add"
	CLIFlagTLSCert               Key = "cli.flag_tls_cert"
	CLIFlagTLSKey                Key = "cli.flag_tls_key"
	CLIFlagClearDomain           Key = "cli.flag_clear_domain"
	CLIFlagACMEEmail             Key = "cli.flag_acme_email"
	CLIFlagTokenFile             Key = "cli.flag_token_file" // #nosec G101 -- i18n key, not a credential
	CLIFlagChatID                Key = "cli.flag_chat_id"
	CLIFlagBackupOut             Key = "cli.flag_backup_out"
	CLIFlagNoEncrypt             Key = "cli.flag_no_encrypt"
	CLIFlagCheck                 Key = "cli.flag_check"
	CLIFlagVersion               Key = "cli.flag_version"
	CLIFlagRollback              Key = "cli.flag_rollback"
	CLIFlagKeepBackups           Key = "cli.flag_keep_backups"
	CLIFlagUninstallNodes        Key = "cli.flag_uninstall_nodes"
	CLIFlagOnce                  Key = "cli.flag_once"
	CLIFlagRelayTunnel           Key = "cli.flag_relay_tunnel"
	CLIFlagRelayConfig           Key = "cli.flag_relay_config"
	CLIFlagWGConfig              Key = "cli.flag_wg_config"
	CLIStatusShort               Key = "cli.status_short"
	CLIStatusLong                Key = "cli.status_long"
	CLIStatusExample             Key = "cli.status_example"
	CLIStatusNoTunnels           Key = "cli.status_no_tunnels"
	CLIStatusNoNodes             Key = "cli.status_no_nodes"
	CLIStatusFront               Key = "cli.status_front"
	CLIStatusFrontUp             Key = "cli.status_front_up"
	CLIStatusFrontDown           Key = "cli.status_front_down"
	CLIStatusFrontCF             Key = "cli.status_front_cf"
	CLIStatusFrontAll            Key = "cli.status_front_all"
	CLIStatusViaFront            Key = "cli.status_via_front"
	CLIWatchFooter               Key = "cli.watch_footer"
	CLISetupLong                 Key = "cli.setup_long"
	CLISetupExample              Key = "cli.setup_example"
	CLISetupWelcome              Key = "cli.setup_welcome" // #nosec G101 -- i18n key, not a credential
	CLIAskRole                   Key = "cli.ask_role"
	CLIAskHubName                Key = "cli.ask_hub_name"
	CLIAskPublicIP               Key = "cli.ask_public_ip"
	CLIAskControlPort            Key = "cli.ask_control_port"
	CLIAskJoinLink               Key = "cli.ask_join_link"
	CLIAskNodeName               Key = "cli.ask_node_name"
	CLIStepOf                    Key = "cli.step_of"
	CLIAnswerRequired            Key = "cli.answer_required"
	CLIAnswerYesNo               Key = "cli.answer_yes_no"
	CLIAnswerNumber              Key = "cli.answer_number"
	CLIDefYes                    Key = "cli.def_yes"
	CLIDefNo                     Key = "cli.def_no"
	CLIHintYesNo                 Key = "cli.hint_yes_no"
	CLIHintYesNoRequired         Key = "cli.hint_yes_no_required"
	CLIHintChoose                Key = "cli.hint_choose"
	CLIHintChooseRequired        Key = "cli.hint_choose_required"
	CLIHintRequired              Key = "cli.hint_required"
	CLIHintText                  Key = "cli.hint_text"
	CLIOr                        Key = "cli.or"
	CLINumberRange               Key = "cli.number_range"
	CLIRoleHubLabel              Key = "cli.role_hub_label"
	CLIRoleNodeLabel             Key = "cli.role_node_label"
	CLIRoleHelp                  Key = "cli.role_help"
	CLIHubNameHelp               Key = "cli.hub_name_help"
	CLIPublicIPHelp              Key = "cli.public_ip_help"
	CLIPublicIPDetected          Key = "cli.public_ip_detected"
	CLIControlPortHelp           Key = "cli.control_port_help"
	CLIAskKernel                 Key = "cli.ask_kernel"
	CLIKernelHelp                Key = "cli.kernel_help"
	CLIKernelUndo                Key = "cli.kernel_undo"
	CLIJoinLinkHelp              Key = "cli.join_link_help"
	CLINodeNameHelp              Key = "cli.node_name_help"
	CLISummaryTitle              Key = "cli.summary_title"
	CLISummaryRole               Key = "cli.summary_role"
	CLISummaryName               Key = "cli.summary_name"
	CLISummaryPublicIP           Key = "cli.summary_public_ip"
	CLISummaryControlPort        Key = "cli.summary_control_port"
	CLISummaryKernel             Key = "cli.summary_kernel"
	CLISummaryHub                Key = "cli.summary_hub"
	CLIKernelBalanced            Key = "cli.kernel_balanced"
	CLIKernelUnchanged           Key = "cli.kernel_unchanged"
	CLINextTitle                 Key = "cli.next_title"
	CLINextJoin                  Key = "cli.next_join"
	CLINextTunnel                Key = "cli.next_tunnel"
	CLINextTunnelMenu            Key = "cli.next_tunnel_menu"
	CLINextTunnelCLI             Key = "cli.next_tunnel_cli"
	CLINodeNextTitle             Key = "cli.node_next_title"
	CLINodeNextHub               Key = "cli.node_next_hub"
	CLIDetectedPrivate           Key = "cli.detected_private"
	CLISetupHubStart             Key = "cli.setup_hub_start"
	CLISetupHubDone              Key = "cli.setup_hub_done"
	CLISetupPrivateIP            Key = "cli.setup_private_ip"
	CLISysctlSkipped             Key = "cli.sysctl_skipped"
	CLIJoinCmdLater              Key = "cli.join_cmd_later"
	CLIJoinCmdIntro              Key = "cli.join_cmd_intro"
	CLISetupNoTTYWhy             Key = "cli.setup_no_tty_why"
	CLISetupNoTTYFix             Key = "cli.setup_no_tty_fix"
	CLISetupNodeNeedsLink        Key = "cli.setup_node_needs_link"
	CLIJoinLong                  Key = "cli.join_long"
	CLIJoinExample               Key = "cli.join_example"
	CLIJoinDone                  Key = "cli.join_done"
	CLIJoinIncompatible          Key = "cli.join_incompatible"
	CLINodeShort                 Key = "cli.node_short"
	CLINodeJoinCmdShort          Key = "cli.node_join_cmd_short"
	CLINodeJoinCmdExample        Key = "cli.node_join_cmd_example"
	CLINodeListShort             Key = "cli.node_list_short"
	CLINodeListExample           Key = "cli.node_list_example"
	CLINodeListEmpty             Key = "cli.node_list_empty"
	CLINodeLastError             Key = "cli.node_last_error"
	CLIIncompatible              Key = "cli.incompatible"
	CLINodeRenameShort           Key = "cli.node_rename_short"
	CLINodeRenameExample         Key = "cli.node_rename_example"
	CLINodeRenamed               Key = "cli.node_renamed"
	CLINodeRemoveShort           Key = "cli.node_remove_short"
	CLINodeRemoveExample         Key = "cli.node_remove_example"
	CLINodeRemoveLost            Key = "cli.node_remove_lost"
	CLINodeRemoved               Key = "cli.node_removed"
	CLINodeTestShort             Key = "cli.node_test_short"
	CLINodeTestExample           Key = "cli.node_test_example"
	CLINodeTestTitle             Key = "cli.node_test_title"
	CLINodeTestCtl               Key = "cli.node_test_ctl"
	CLINodeTestUDPOK             Key = "cli.node_test_udpok"
	CLINodeTestUDPBlocked        Key = "cli.node_test_udp_blocked"
	CLINodeTestSysinfo           Key = "cli.node_test_sysinfo"
	CLINodeSetHubShort           Key = "cli.node_set_hub_short"
	CLINodeSetHubExample         Key = "cli.node_set_hub_example"
	CLINodeSetHubDone            Key = "cli.node_set_hub_done"
	CLINodeSetHubOffline         Key = "cli.node_set_hub_offline"
	CLINodeSetHubLong            Key = "cli.node_set_hub_long"
	CLIViaFront                  Key = "cli.via_front"
	CLIHubShort                  Key = "cli.hub_short"
	CLIHubAnnounceShort          Key = "cli.hub_announce_short"
	CLIHubAnnounceLong           Key = "cli.hub_announce_long"
	CLIHubAnnounceExample        Key = "cli.hub_announce_example"
	CLIHubAnnounced              Key = "cli.hub_announced"
	CLIHubAnnounceFront          Key = "cli.hub_announce_front"
	CLIHubAnnounceOffline        Key = "cli.hub_announce_offline"
	CLITunnelShort               Key = "cli.tunnel_short"
	CLITunnelAddShort            Key = "cli.tunnel_add_short"
	CLITunnelAddLong             Key = "cli.tunnel_add_long"
	CLITunnelAddExample          Key = "cli.tunnel_add_example"
	CLITunnelAddSummary          Key = "cli.tunnel_add_summary"
	CLILadderDefault             Key = "cli.ladder_default"
	CLIAskCreateTunnel           Key = "cli.ask_create_tunnel"
	CLITunnelState               Key = "cli.tunnel_state"
	CLITunnelListShort           Key = "cli.tunnel_list_short"
	CLITunnelListExample         Key = "cli.tunnel_list_example"
	CLITunnelShowShort           Key = "cli.tunnel_show_short"
	CLITunnelShowExample         Key = "cli.tunnel_show_example"
	CLIBackupRole                Key = "cli.backup_role"
	CLIPrimaryRole               Key = "cli.primary_role"
	CLIKeyNodes                  Key = "cli.key_nodes"
	CLIKeyActive                 Key = "cli.key_active"
	CLIKeyPorts                  Key = "cli.key_ports"
	CLIKeyLadder                 Key = "cli.key_ladder"
	CLIKeyPolicy                 Key = "cli.key_policy"
	CLIKeyProbe                  Key = "cli.key_probe"
	CLIKeyFailover               Key = "cli.key_failover"
	CLIKeyTraffic                Key = "cli.key_traffic"
	CLIActiveVia                 Key = "cli.active_via"
	CLIUpFor                     Key = "cli.up_for"
	CLIPolicyLine                Key = "cli.policy_line"
	CLIProbeLine                 Key = "cli.probe_line"
	CLIFailbackAfter             Key = "cli.failback_after"
	CLIFailoverLine              Key = "cli.failover_line"
	CLITrafficLine               Key = "cli.traffic_line"
	CLIRungsTitle                Key = "cli.rungs_title"
	CLIRungWarm                  Key = "cli.rung_warm"
	CLIRungActive                Key = "cli.rung_active"
	CLIRungSkipped               Key = "cli.rung_skipped"
	CLIRungQuarantine            Key = "cli.rung_quarantine"
	CLIRungCold                  Key = "cli.rung_cold"
	CLIProbesTitle               Key = "cli.probes_title"
	CLIProbesNone                Key = "cli.probes_none"
	CLIProbesLine                Key = "cli.probes_line"
	CLIProbesLastFail            Key = "cli.probes_last_fail"
	CLITunnelEditShort           Key = "cli.tunnel_edit_short"
	CLITunnelEditExample         Key = "cli.tunnel_edit_example"
	CLIEditNothing               Key = "cli.edit_nothing"
	CLITunnelUpdated             Key = "cli.tunnel_updated"
	CLITunnelEnableShort         Key = "cli.tunnel_enable_short"
	CLITunnelEnableExample       Key = "cli.tunnel_enable_example"
	CLITunnelDisableShort        Key = "cli.tunnel_disable_short"
	CLITunnelDisableExample      Key = "cli.tunnel_disable_example"
	CLITunnelDisableLost         Key = "cli.tunnel_disable_lost"
	CLITunnelEnabled             Key = "cli.tunnel_enabled"
	CLITunnelDisabled            Key = "cli.tunnel_disabled"
	CLITunnelRestartShort        Key = "cli.tunnel_restart_short"
	CLITunnelRestartExample      Key = "cli.tunnel_restart_example"
	CLITunnelRestartNote         Key = "cli.tunnel_restart_note"
	CLITunnelRestarted           Key = "cli.tunnel_restarted"
	CLITunnelDeleteShort         Key = "cli.tunnel_delete_short"
	CLITunnelDeleteExample       Key = "cli.tunnel_delete_example"
	CLITunnelDeleteLost          Key = "cli.tunnel_delete_lost"
	CLITunnelDeleted             Key = "cli.tunnel_deleted"
	CLITunnelSwitchShort         Key = "cli.tunnel_switch_short"
	CLITunnelSwitchExample       Key = "cli.tunnel_switch_example"
	CLISwitchOneOf               Key = "cli.switch_one_of"
	CLITunnelSwitched            Key = "cli.tunnel_switched"
	CLITunnelResetShort          Key = "cli.tunnel_reset_short"
	CLITunnelResetExample        Key = "cli.tunnel_reset_example"
	CLITunnelResetDone           Key = "cli.tunnel_reset_done"
	CLITunnelPauseShort          Key = "cli.tunnel_pause_short"
	CLITunnelPauseExample        Key = "cli.tunnel_pause_example"
	CLITunnelPaused              Key = "cli.tunnel_paused"
	CLITunnelResumeShort         Key = "cli.tunnel_resume_short"
	CLITunnelResumeExample       Key = "cli.tunnel_resume_example"
	CLITunnelResumed             Key = "cli.tunnel_resumed"
	CLITunnelTestLadderShort     Key = "cli.tunnel_test_ladder_short"
	CLITunnelTestLadderExample   Key = "cli.tunnel_test_ladder_example"
	CLITestLadderLost            Key = "cli.test_ladder_lost"
	CLIResultOK                  Key = "cli.result_ok"
	CLIResultFailed              Key = "cli.result_failed"
	CLIResultSkipped             Key = "cli.result_skipped"
	CLITunnelBackupShort         Key = "cli.tunnel_backup_short"
	CLITunnelBackupAddShort      Key = "cli.tunnel_backup_add_short"
	CLITunnelBackupAddExample    Key = "cli.tunnel_backup_add_example"
	CLITunnelBackupRemoveShort   Key = "cli.tunnel_backup_remove_short"
	CLITunnelBackupRemoveExample Key = "cli.tunnel_backup_remove_example"
	CLIBackupRemoved             Key = "cli.backup_removed"
	CLIPortShort                 Key = "cli.port_short"
	CLIPortAddShort              Key = "cli.port_add_short"
	CLIPortAddLong               Key = "cli.port_add_long"
	CLIPortAddExample            Key = "cli.port_add_example"
	CLITargetOnePort             Key = "cli.target_one_port"
	CLIPortRestartNote           Key = "cli.port_restart_note"
	CLIPortAdded                 Key = "cli.port_added"
	CLIPortRemoveShort           Key = "cli.port_remove_short"
	CLIPortRemoveExample         Key = "cli.port_remove_example"
	CLIPortRemoved               Key = "cli.port_removed"
	CLIPortSetShort              Key = "cli.port_set_short"
	CLIPortSetLong               Key = "cli.port_set_long"
	CLIPortSetExample            Key = "cli.port_set_example"
	CLIPortCheckShort            Key = "cli.port_check_short"
	CLIPortCheckLong             Key = "cli.port_check_long"
	CLIPortCheckExample          Key = "cli.port_check_example"
	CLIPortCheckTitle            Key = "cli.port_check_title"
	CLICheckBind                 Key = "cli.check_bind"
	CLICheckFirewall             Key = "cli.check_firewall"
	CLICheckFromNode             Key = "cli.check_from_node"
	CLICheckViaTunnel            Key = "cli.check_via_tunnel"
	CLICheckFree                 Key = "cli.check_free"
	CLICheckUsedBy               Key = "cli.check_used_by"
	CLICheckOnAddr               Key = "cli.check_on_addr"
	CLICheckDeyroute             Key = "cli.check_deyroute"
	CLICheckOpen                 Key = "cli.check_open"
	CLICheckClosed               Key = "cli.check_closed"
	CLICheckOpenWith             Key = "cli.check_open_with"
	CLICheckOpenHint             Key = "cli.check_open_hint"
	CLIPortOpenLost              Key = "cli.port_open_lost"
	CLIPortOpenRan               Key = "cli.port_open_ran"
	CLIPortOpenNow               Key = "cli.port_open_now"
	CLIPortOpenStill             Key = "cli.port_open_still"
	CLIPortOpenNothing           Key = "cli.port_open_nothing"
	CLICheckNotTested            Key = "cli.check_not_tested"
	CLICheckYes                  Key = "cli.check_yes"
	CLICheckNo                   Key = "cli.check_no"
	CLICheckNoTunnel             Key = "cli.check_no_tunnel"
	CLICheckNote                 Key = "cli.check_note"
	CLISuggestedPorts            Key = "cli.suggested_ports"
	CLIPortSuggestShort          Key = "cli.port_suggest_short"
	CLIPortSuggestExample        Key = "cli.port_suggest_example"
	CLILadderShort               Key = "cli.ladder_short"
	CLILadderListShort           Key = "cli.ladder_list_short"
	CLILadderListExample         Key = "cli.ladder_list_example"
	CLILadderBuiltin             Key = "cli.ladder_builtin"
	CLILadderCustom              Key = "cli.ladder_custom"
	CLILadderShowShort           Key = "cli.ladder_show_short"
	CLILadderShowExample         Key = "cli.ladder_show_example"
	CLILadderTitle               Key = "cli.ladder_title"
	CLILadderCreateShort         Key = "cli.ladder_create_short"
	CLILadderCreateExample       Key = "cli.ladder_create_example"
	CLILadderSetShort            Key = "cli.ladder_set_short"
	CLILadderSetExample          Key = "cli.ladder_set_example"
	CLILadderCreated             Key = "cli.ladder_created"
	CLILadderSaved               Key = "cli.ladder_saved"
	CLILadderDeleteShort         Key = "cli.ladder_delete_short"
	CLILadderDeleteExample       Key = "cli.ladder_delete_example"
	CLILadderDeleted             Key = "cli.ladder_deleted"
	CLIDiagShort                 Key = "cli.diag_short"
	CLIDiagSpeedShort            Key = "cli.diag_speed_short"
	CLIDiagSpeedExample          Key = "cli.diag_speed_example"
	CLISpeedResult               Key = "cli.speed_result"
	CLIDiagProbeShort            Key = "cli.diag_probe_short"
	CLIDiagProbeExample          Key = "cli.diag_probe_example"
	CLILogsShort                 Key = "cli.logs_short"
	CLILogsLong                  Key = "cli.logs_long"
	CLILogsExample               Key = "cli.logs_example"
	CLIEventsShort               Key = "cli.events_short"
	CLIEventsExample             Key = "cli.events_example"
	CLIDoctorShort               Key = "cli.doctor_short"
	CLIDoctorLong                Key = "cli.doctor_long"
	CLIDoctorExample             Key = "cli.doctor_example"
	CLIDoctorLocalOnly           Key = "cli.doctor_local_only"
	CLIDoctorWritten             Key = "cli.doctor_written"
	CLIOptimizeShort             Key = "cli.optimize_short"
	CLIOptimizeApplyShort        Key = "cli.optimize_apply_short"
	CLIOptimizeApplyLong         Key = "cli.optimize_apply_long"
	CLIOptimizeApplyExample      Key = "cli.optimize_apply_example"
	CLIOptimizeApplied           Key = "cli.optimize_applied"
	CLIOptimizeRevertShort       Key = "cli.optimize_revert_short"
	CLIOptimizeRevertExample     Key = "cli.optimize_revert_example"
	CLIOptimizeReverted          Key = "cli.optimize_reverted"
	CLIBBRActive                 Key = "cli.bbr_active"
	CLIBBRInactive               Key = "cli.bbr_inactive"
	CLIBBRMissing                Key = "cli.bbr_missing"
	CLISecurityShort             Key = "cli.security_short"
	CLIRotateTokensShort         Key = "cli.rotate_tokens_short"   // #nosec G101 -- i18n key, not a credential
	CLIRotateTokensExample       Key = "cli.rotate_tokens_example" // #nosec G101 -- i18n key, not a credential
	CLIAllTunnels                Key = "cli.all_tunnels"
	CLIOneTunnel                 Key = "cli.one_tunnel"
	CLIRotateTokensLost          Key = "cli.rotate_tokens_lost" // #nosec G101 -- i18n key, not a credential
	CLITokensRotated             Key = "cli.tokens_rotated"     // #nosec G101 -- i18n key, not a credential
	CLIRotateCAShort             Key = "cli.rotate_ca_short"
	CLIRotateCAExample           Key = "cli.rotate_ca_example"
	CLIRotateCALost              Key = "cli.rotate_ca_lost"
	CLICARotated                 Key = "cli.ca_rotated"
	CLICAOffline                 Key = "cli.ca_offline"
	CLISecurityTLSShort          Key = "cli.security_tls_short"
	CLISecurityTLSExample        Key = "cli.security_tls_example"
	CLITLSShowShort              Key = "cli.tls_show_short"
	CLITLSShowExample            Key = "cli.tls_show_example"
	CLITLSRenewShort             Key = "cli.tls_renew_short"
	CLITLSRenewExample           Key = "cli.tls_renew_example"
	CLITLSRenewed                Key = "cli.tls_renewed"
	CLITLSDomainShort            Key = "cli.tls_domain_short"
	CLITLSDomainLong             Key = "cli.tls_domain_long"
	CLITLSDomainExample          Key = "cli.tls_domain_example"
	CLITLSDomainSet              Key = "cli.tls_domain_set"
	CLITLSDomainCleared          Key = "cli.tls_domain_cleared"
	CLITLSACMEShort              Key = "cli.tls_acme_short"
	CLITLSACMELong               Key = "cli.tls_acme_long"
	CLITLSACMEExample            Key = "cli.tls_acme_example"
	CLIACMENothing               Key = "cli.acme_nothing"
	CLIACMEEmailSet              Key = "cli.acme_email_set"
	CLIACMEEmailRemoved          Key = "cli.acme_email_removed"
	CLIACMETokenSet              Key = "cli.acme_token_set"     // #nosec G101 -- i18n key, not a credential
	CLIACMETokenRemoved          Key = "cli.acme_token_removed" // #nosec G101 -- i18n key, not a credential
	CLIACMERenewHint             Key = "cli.acme_renew_hint"
	CLISecurityFirewallShort     Key = "cli.security_firewall_short"
	CLIFirewallShowShort         Key = "cli.firewall_show_short"
	CLIFirewallShowExample       Key = "cli.firewall_show_example"
	CLIFirewallApplyShort        Key = "cli.firewall_apply_short"
	CLIFirewallApplyExample      Key = "cli.firewall_apply_example"
	CLIFirewallDisableShort      Key = "cli.firewall_disable_short"
	CLIFirewallDisableExample    Key = "cli.firewall_disable_example"
	CLIFirewallApplied           Key = "cli.firewall_applied"
	CLIFirewallDisabled          Key = "cli.firewall_disabled"
	CLIFirewallManaged           Key = "cli.firewall_managed"
	CLIFirewallSuggestOnly       Key = "cli.firewall_suggest_only"
	CLIFirewallMode              Key = "cli.firewall_mode"
	CLIFirewallSuggested         Key = "cli.firewall_suggested"
	CLIAuditShort                Key = "cli.audit_short"
	CLIAuditExample              Key = "cli.audit_example"
	CLIAuditClean                Key = "cli.audit_clean"
	CLIAuditProblems             Key = "cli.audit_problems"
	CLINotifyShort               Key = "cli.notify_short"
	CLITelegramShort             Key = "cli.telegram_short"
	CLITelegramSetShort          Key = "cli.telegram_set_short"
	CLITelegramSetLong           Key = "cli.telegram_set_long"
	CLITelegramSetExample        Key = "cli.telegram_set_example"
	CLITelegramSet               Key = "cli.telegram_set"
	CLITelegramTestShort         Key = "cli.telegram_test_short"
	CLITelegramTestExample       Key = "cli.telegram_test_example"
	CLITelegramTested            Key = "cli.telegram_tested"
	CLITelegramOffShort          Key = "cli.telegram_off_short"
	CLITelegramOffExample        Key = "cli.telegram_off_example"
	CLITelegramOff               Key = "cli.telegram_off"
	CLIBackupShort               Key = "cli.backup_short"
	CLIBackupLong                Key = "cli.backup_long"
	CLIBackupExample             Key = "cli.backup_example"
	CLIAskPassphrase             Key = "cli.ask_passphrase"       // #nosec G101 -- i18n key, not a credential
	CLIAskPassphraseAgain        Key = "cli.ask_passphrase_again" // #nosec G101 -- i18n key, not a credential
	CLIPassphraseMismatch        Key = "cli.passphrase_mismatch"  // #nosec G101 -- i18n key, not a credential
	CLIPassphraseFix             Key = "cli.passphrase_fix"       // #nosec G101 -- i18n key, not a credential
	CLIBackupWritten             Key = "cli.backup_written"       // #nosec G101 -- i18n key, not a credential
	CLIBackupNoEvents            Key = "cli.backup_no_events"
	CLIBackupKeepPass            Key = "cli.backup_keep_pass" // #nosec G101 -- i18n key, not a credential
	CLIBackupPlainWarn           Key = "cli.backup_plain_warn"
	CLIRestoreShort              Key = "cli.restore_short"
	CLIRestoreLong               Key = "cli.restore_long"
	CLIRestoreExample            Key = "cli.restore_example"
	CLIAskMovedHub               Key = "cli.ask_moved_hub"
	CLIRestoreLost               Key = "cli.restore_lost"
	CLIRestoreMove               Key = "cli.restore_move"
	CLIRestoredHub               Key = "cli.restored_hub"
	CLIRestoreNextHub            Key = "cli.restore_next_hub"
	CLIRestoredNode              Key = "cli.restored_node"
	CLIRestoreNextNode           Key = "cli.restore_next_node"
	CLIRestoreNextMenu           Key = "cli.restore_next_menu"
	CLIRestorePrevious           Key = "cli.restore_previous"
	CLIUpdateShort               Key = "cli.update_short"
	CLIUpdateLong                Key = "cli.update_long"
	CLIUpdateExample             Key = "cli.update_example"
	CLIUpdateFlagsConflict       Key = "cli.update_flags_conflict"
	CLIRollbackLost              Key = "cli.rollback_lost"
	CLIRolledBack                Key = "cli.rolled_back"
	CLIRolledBackLocal           Key = "cli.rolled_back_local"
	CLIRollbackRestartFailed     Key = "cli.rollback_restart_failed"
	CLIUpToDate                  Key = "cli.up_to_date"
	CLIChangelog                 Key = "cli.changelog"
	CLIUpdateLost                Key = "cli.update_lost"
	CLIUpdated                   Key = "cli.updated"
	CLIUpdateAvailable           Key = "cli.update_available"
	CLIUpdateBackendsShort       Key = "cli.update_backends_short"
	CLIUpdateBackendsExample     Key = "cli.update_backends_example"
	CLIAllBackends               Key = "cli.all_backends"
	CLIUpdateBackendsLost        Key = "cli.update_backends_lost"
	CLIUpdateManifestShort       Key = "cli.update_manifest_short"
	CLIUpdateManifestExample     Key = "cli.update_manifest_example"
	CLIManifestUpdated           Key = "cli.manifest_updated"
	CLIConfigShort               Key = "cli.config_short"
	CLIConfigShowShort           Key = "cli.config_show_short"
	CLIConfigShowExample         Key = "cli.config_show_example"
	CLIConfigValidateShort       Key = "cli.config_validate_short"
	CLIConfigValidateExample     Key = "cli.config_validate_example"
	CLIConfigValidHub            Key = "cli.config_valid_hub"
	CLIConfigValid               Key = "cli.config_valid"
	CLIConfigEditShort           Key = "cli.config_edit_short"
	CLIConfigEditLong            Key = "cli.config_edit_long"
	CLIConfigEditExample         Key = "cli.config_edit_example"
	CLIConfigEditAborted         Key = "cli.config_edit_aborted"
	CLIConfigEditKept            Key = "cli.config_edit_kept"
	CLIConfigUnchanged           Key = "cli.config_unchanged"
	CLIConfigEditInvalid         Key = "cli.config_edit_invalid"
	CLIConfigEditHeader          Key = "cli.config_edit_header"
	CLIConfigEditHeader2         Key = "cli.config_edit_header2"
	CLIConfigSaved               Key = "cli.config_saved"
	CLIConfigNotApplied          Key = "cli.config_not_applied"
	CLIConfigApplyShort          Key = "cli.config_apply_short"
	CLIConfigApplyExample        Key = "cli.config_apply_example"
	CLIConfigApplied             Key = "cli.config_applied"
	CLIConfigBackup              Key = "cli.config_backup"
	CLISettingsShort             Key = "cli.settings_short"
	CLIUIModeShort               Key = "cli.ui_mode_short"
	CLIUIModeExample             Key = "cli.ui_mode_example"
	CLIUIModeSet                 Key = "cli.ui_mode_set"
	CLIUninstallShort            Key = "cli.uninstall_short"
	CLIUninstallLong             Key = "cli.uninstall_long"
	CLIUninstallExample          Key = "cli.uninstall_example"
	CLIAskKeepBackups            Key = "cli.ask_keep_backups"
	CLIAskUninstallNodes         Key = "cli.ask_uninstall_nodes"
	CLIUninstallBackupsGone      Key = "cli.uninstall_backups_gone"
	CLIUninstallBackupsKept      Key = "cli.uninstall_backups_kept"
	CLIUninstallLost             Key = "cli.uninstall_lost"
	CLIUninstallNodesToo         Key = "cli.uninstall_nodes_too"
	CLINodesUninstalled          Key = "cli.nodes_uninstalled"
	CLIUninstalled               Key = "cli.uninstalled"
	CLIBackupsKept               Key = "cli.backups_kept"
	CLICompletionShort           Key = "cli.completion_short"
	CLICompletionLong            Key = "cli.completion_long"
	CLICompletionExample         Key = "cli.completion_example"
	CLIMenuShort                 Key = "cli.menu_short"
	CLIDaemonShort               Key = "cli.daemon_short"
	CLIDaemonHubShort            Key = "cli.daemon_hub_short"
	CLIDaemonNodeShort           Key = "cli.daemon_node_short"
	CLIRelayShort                Key = "cli.relay_short"
	CLIFrontShimShort            Key = "cli.front_shim_short"
	CLIStatusNodesOnline         Key = "cli.status.nodes_online"
	CLIStatusTunnelsUp           Key = "cli.status.tunnels_up"
	CLIStatusWarnings            Key = "cli.status.warnings"
	CLIAttnTitle                 Key = "cli.attn.title"
	CLIAttnTunnel                Key = "cli.attn.tunnel"
	CLIAttnNodeOffline           Key = "cli.attn.node_offline"
	CLIAttnAllGood               Key = "cli.attn.all_good"
	CLIAttnDegraded              Key = "cli.attn.degraded"
	CLIAttnServiceDown           Key = "cli.attn.service_down"
	CLIAttnSwitching             Key = "cli.attn.switching"
	CLIAttnStarting              Key = "cli.attn.starting"
	CLIAttnDown                  Key = "cli.attn.down"
	CLIAttnInit                  Key = "cli.attn.init"
	CLIColAddress                Key = "cli.col.address"
	CLIColControl                Key = "cli.col.control"
	CLINodeCPUValue              Key = "cli.node.cpu_value"
	CLINodeRAMValue              Key = "cli.node.ram_value"
	CLIFlagFrontShimConfig       Key = "cli.flag_front_shim_config"
	CLIFrontShort                Key = "cli.front_short"
	CLIFrontLong                 Key = "cli.front_long"
	CLIFrontEnableShort          Key = "cli.front_enable_short"
	CLIFrontEnableExample        Key = "cli.front_enable_example"
	CLIFlagFrontDomain           Key = "cli.flag_front_domain"
	CLIFlagFrontPort             Key = "cli.flag_front_port"
	CLIFlagFrontTLS              Key = "cli.flag_front_tls"
	CLIFrontStatusShort          Key = "cli.front_status_short"
	CLIFrontStatusExample        Key = "cli.front_status_example"
	CLIFrontDisableShort         Key = "cli.front_disable_short"
	CLIFrontDisableExample       Key = "cli.front_disable_example"
	CLIFrontDisabledNode         Key = "cli.front_disabled_node"
	CLIFrontHubOnly              Key = "cli.front_hub_only"
	CLIFrontListening            Key = "cli.front_listening"
	CLIFrontNotListening         Key = "cli.front_not_listening"
	CLIFrontOff                  Key = "cli.front_off"
	CLIFrontOn                   Key = "cli.front_on"
	CLIFrontNodes                Key = "cli.front_nodes"
	CLIFrontCloudflare           Key = "cli.front_cloudflare"
	CLIFrontNodeCommand          Key = "cli.front_node_command"
	CLIWGShort                   Key = "cli.wg_short"
	CLIPairShort                 Key = "cli.pair_short"
)

// cli — English texts (merged into en by init).
var cliEN = map[Key]string{
	CLIRootExample:               "  deyroute                         # open the menu (same as: dey)\n  deyroute setup                   # first-time wizard\n  deyroute status --watch          # live dashboard\n  deyroute tunnel add --node de-1 --ports 443,2053\n  deyroute doctor                  # support file for troubleshooting",
	CLIVersionExample:            "  deyroute version\n  deyroute version --json",
	CLIGroupStart:                "Getting started:",
	CLIGroupManage:               "Nodes, tunnels and ports:",
	CLIGroupDiag:                 "Diagnostics:",
	CLIGroupSystem:               "System:",
	CLIUnknownSub:                "unknown command %q for %q",
	CLIWantArgs:                  "%s needs %d argument(s), got %d",
	CLIWantArgsRange:             "%s takes %d to %d argument(s), got %d",
	CLIWantFlag:                  "missing required flag %s",
	CLIWantPositiveDuration:      "%s must be a positive duration, e.g. 15m or 1h",
	CLIJoinTTLRange:              "--ttl must be between 1m and 24h (got %s); the default is 15m",
	CLIWantCount:                 "--count must be between 1 and %d",
	CLIWantSeconds:               "--seconds must be between 1 and %d",
	CLIWantShell:                 "unknown shell %q: use bash, zsh or fish",
	CLIWantAbsPath:               "an absolute path, e.g. /etc/deyroute/secrets/telegram.token",
	CLIWantAbsPathPEM:            "the path of a PEM file, e.g. /etc/ssl/vpn.example.com/fullchain.pem",
	CLIWantDomainOrClear:         "give a domain (e.g. vpn.example.com) or --clear",
	CLIWantIP:                    "an IPv4 or IPv6 address, e.g. 5.6.7.8",
	CLIWantName:                  "a name of 1 to %d printable characters",
	CLINotSetUpFix:               "this server is not set up yet: run deyroute setup (or join a hub: deyroute join 'dey://...')",
	CLIInvalidAnswer:             "Not accepted: %s",
	CLIColBackend:                "BACKEND",
	CLIColCPU:                    "CPU",
	CLIColCode:                   "CODE",
	CLIColCtl:                    "CTL RTT",
	CLIColCtlPort:                "CTL PORT",
	CLIColDays:                   "DAYS",
	CLIColDetail:                 "DETAIL",
	CLIColExpires:                "EXPIRES",
	CLIColFingerprint:            "FINGERPRINT",
	CLIColFrom:                   "FROM",
	CLIColID:                     "ID",
	CLIColKind:                   "KIND",
	CLIColLevel:                  "LEVEL",
	CLIColMessage:                "MESSAGE",
	CLIColMode:                   "MODE",
	CLIColNode:                   "NODE",
	CLIColNodes:                  "NODES",
	CLIColPort:                   "PORT",
	CLIColPublicIP:               "PUBLIC IP",
	CLIColRAM:                    "RAM",
	CLIColResult:                 "RESULT",
	CLIColRungs:                  "RUNGS",
	CLIColSubject:                "SUBJECT",
	CLIColTime:                   "TIME",
	CLIColTo:                     "TO",
	CLIColTunnel:                 "TUNNEL",
	CLIColTunnels:                "TUNNELS",
	CLIColType:                   "TYPE",
	CLIColUnit:                   "UNIT",
	CLIColUsedBy:                 "USED BY",
	CLIColVersion:                "VERSION",
	CLIColWho:                    "TUNNEL/NODE",
	CLIFlagWatch:                 "refresh the dashboard every 2 seconds until Ctrl-C",
	CLIFlagRole:                  "role of this server: hub (Iran, users connect here) or node (abroad)",
	CLIFlagSetupName:             "server name, e.g. ir-1 (default: the host name)",
	CLIFlagControlPort:           "hub control port (default: 44433, or the next free port)",
	CLIFlagYesSetup:              "ask nothing: use the defaults and apply the balanced kernel profile",
	CLIFlagRepair:                "repair a set-up server (unit files, directories, service restart); the config is untouched",
	CLISetupRepaired:             "Repaired: unit files are current and %s was restarted; the configuration is unchanged.",
	CLIFlagJoinName:              "node name, e.g. de-1 (default: the host name)",
	CLIFlagTTL:                   "how long the join command stays valid",
	CLIFlagTunnelNode:            "primary node of the tunnel",
	CLIFlagPorts:                 "listen ports: 443,2053 | 27015/udp | 2000-2010 | 443:8443 (listen:target port)",
	CLIFlagTunnelName:            "display name of the tunnel (the id is derived from it)",
	CLIFlagLadder:                "ladder profile (default) or an inline list such as backhaul/wssmux,rathole/noise",
	CLIFlagBackupNode:            "backup node id (repeat the flag or separate ids with commas)",
	CLIFlagBackupNodeOne:         "backup node id, e.g. nl-1 (one node per command)",
	CLIOneBackupNode:             "give one backup node per command (--node nl-1); run the command again for the next node",
	CLIFlagYesAdd:                "create at once, without showing the summary first",
	CLIFlagPolicy:                "failover policy: transport_then_node, transport_only or node_only",
	CLIFlagProbePort:             "listen port used by the health probe (0 = first TCP port)",
	CLIFlagSwitchTransport:       "transport to switch to, e.g. backhaul/tcpmux",
	CLIFlagSwitchNode:            "node to switch to, e.g. nl-1",
	CLIFlagTarget:                "target on the node (default 127.0.0.1:<port>)",
	CLIFlagProbe:                 "probe kind of the port maps: auto (default), tcp, tls or http; UDP maps are always auto",
	CLIProbeFix:                  "run it again with --probe auto, tcp, tls or http (UDP ports: auto only)",
	CLIFlagCheckNode:             "node that tests reachability from outside (default: the first online node)",
	CLIFlagOpenFirewall:          "open the port in the external firewall that blocks it (ufw, firewalld, iptables, nftables); asks first unless --yes",
	CLIFlagCount:                 "how many free ports to suggest",
	CLIFlagRungs:                 "transports in order, comma-separated",
	CLIFlagSeconds:               "test duration in seconds",
	CLIFlagAllPorts:              "probe every port of the tunnel, not only the probe port",
	CLIFlagFollow:                "keep printing new lines until Ctrl-C",
	CLIFlagLogsSince:             "only lines newer than this, e.g. 1h (default: the last 200 lines)",
	CLIFlagEventsTunnel:          "only events of this tunnel",
	CLIFlagEventsSince:           "only events newer than this",
	CLIFlagDoctorNode:            "also collect the data of this node",
	CLIFlagDoctorOut:             "write the support file to FILE (default /root/deyroute-doctor-<UTC>.tar.gz)",
	CLIFlagProfile:               "sysctl profile: balanced, aggressive or off",
	CLIFlagRotateTunnel:          "rotate only this tunnel's token (default: every tunnel)",
	CLIFlagTLSTunnel:             "only the certificate of this tunnel",
	CLIFlagTLSMode:               "tunnel TLS: auto (internal CA), acme (Let's Encrypt for the hub domain) or custom (with --tls-cert and --tls-key)",
	CLIFlagTLSModeAdd:            "tunnel TLS: auto (internal CA, default) or acme (Let's Encrypt for the hub domain); custom is set afterwards with tunnel edit",
	CLIFlagTLSCert:               "certificate chain file for --tls-mode custom (PEM, leaf first)",
	CLIFlagTLSKey:                "private key file for --tls-mode custom (PEM, unencrypted)",
	CLIFlagClearDomain:           "remove the domain (refused while a tunnel uses tls mode acme)",
	CLIFlagACMEEmail:             "ACME account e-mail for expiry notices from Let's Encrypt ('' removes it)",
	CLIFlagCloudflareToken:       "file holding a Cloudflare API token with Zone:DNS:Edit, for DNS-01 instead of HTTP-01 on port 80; copied to /etc/deyroute/secrets/cloudflare.token ('' removes it)",
	CLIFlagTokenFile:             "file holding the Telegram bot token (mode 0600, e.g. /etc/deyroute/secrets/telegram.token)",
	CLIFlagChatID:                "Telegram chat id that receives the messages",
	CLIFlagBackupOut:             "backup file (default /var/lib/deyroute/backups/deyroute-backup-<UTC>.tar.gz.age)",
	CLIFlagNoEncrypt:             "write a plain tar.gz (it contains the CA key and every secret)",
	CLIFlagCheck:                 "only check for a new release and show its changelog",
	CLIFlagVersion:               "install this release instead of the latest, e.g. 1.2.0",
	CLIFlagRollback:              "go back to the previous deyroute binary",
	CLIFlagKeepBackups:           "keep /var/lib/deyroute/backups",
	CLIFlagUninstallNodes:        "on a hub: uninstall deyroute from every online node first",
	CLIFlagOnce:                  "render the first screen once and exit",
	CLIFlagRelayTunnel:           "tunnel id (must match the configuration file)",
	CLIFlagRelayConfig:           "relay.json of this relay half",
	CLIFlagWGConfig:              "wg.json of this interface",
	CLIStatusShort:               "Show the dashboard: tunnels, nodes and the last events",
	CLIStatusLong:                "Show the dashboard of the menu as text: every tunnel with its active node, transport,\nstate, RTT, up-time and ports; every node with its state, control RTT and version;\nthe traffic of each tunnel when the hub counts it (deyroute stats has the charts);\nthe last events and warnings. --watch refreshes it every 2 seconds.",
	CLIStatusExample:             "  deyroute status\n  deyroute status --watch\n  deyroute status --json | jq '.tunnels[] | {id, state}'",
	CLIStatusNoTunnels:           "No tunnels yet. Add one: deyroute tunnel add --node <id> --ports 443",
	CLIStatusNoNodes:             "No nodes yet. Show the join command: deyroute node join-command",
	CLIStatusFront:               "Front: %s:%d (%s, %s, tls %s)",
	CLIStatusFrontUp:             "listening",
	CLIStatusFrontDown:           "NOT listening, DEY-X053: see deyroute logs hub",
	CLIStatusFrontCF:             "Cloudflare only",
	CLIStatusFrontAll:            "open to all",
	CLIStatusViaFront:            "via front",
	CLIWatchFooter:               "Updated %s · refreshes every %ds · Ctrl-C quits",
	CLISetupLong:                 "Set this server up. A hub (the Iran server users connect to) takes at most five\nquestions: role, name, public IP, control port and the kernel profile; keys, the\nfirewall table and the service are automatic. It ends with the join command for\nyour nodes. A node asks for the join link printed by the hub.\n\nWithout a terminal pass the answers as flags: --role hub --name ir-1 --yes.",
	CLISetupExample:              "  deyroute setup\n  deyroute setup --role hub --name ir-1 --yes\n  deyroute setup --role hub --name ir-1 --control-port 44500",
	CLISetupWelcome:              "First-time setup: a few questions. Each one says what to type; Enter alone takes the suggested answer.",
	CLIAskRole:                   "Role of this server",
	CLIAskHubName:                "Name of this hub",
	CLIAskPublicIP:               "Public IP of this server",
	CLIAskControlPort:            "Control port for the nodes",
	CLIAskJoinLink:               "Join link from the hub (on the hub: deyroute node join-command)",
	CLIAskNodeName:               "Name of this node",
	CLIStepOf:                    "Step %d of %d",
	CLIAnswerRequired:            "an answer is required (there is no default)",
	CLIAnswerYesNo:               "type y (yes) or n (no)",
	CLIAnswerNumber:              "type a number from 1 to %d",
	CLIDefYes:                    "y (yes)",
	CLIDefNo:                     "n (no)",
	CLIHintYesNo:                 "Type y (yes) or n (no) and press Enter. Enter alone = %s.",
	CLIHintYesNoRequired:         "Type y (yes) or n (no) and press Enter.",
	CLIHintChoose:                "Type %s and press Enter. Enter alone = %s.",
	CLIHintChooseRequired:        "Type %s and press Enter.",
	CLIHintRequired:              "Type the answer and press Enter (there is no default).",
	CLIHintText:                  "Press Enter to use %s, or type another value and press Enter.",
	CLIOr:                        "or",
	CLINumberRange:               "a number from 1 to %d",
	CLIRoleHubLabel:              "the server in Iran: your users connect to it",
	CLIRoleNodeLabel:             "the server abroad: runs your VPN service (Xray, Marzban node ...)",
	CLIRoleHelp:                  "Set up the hub first; each node then joins it with the command the hub prints.",
	CLIHubNameHelp:               "Shown in the menu, the logs and the messages. Letters, digits and -, e.g. ir-1.",
	CLIPublicIPHelp:              "The address your users and your nodes connect to.",
	CLIPublicIPDetected:          "Detected automatically: %s",
	CLIControlPortHelp:           "Your nodes connect to the hub on this TCP port (mutual TLS). Allow it in your provider's firewall too.",
	CLIAskKernel:                 "Kernel network profile",
	CLIKernelHelp:                "Apply the balanced profile: BBR congestion control and larger network buffers (recommended).",
	CLIKernelUndo:                "You can undo it any time with: deyroute optimize revert",
	CLIJoinLinkHelp:              "On the hub run: deyroute node join-command. Copy the link that starts with dey:// (valid for 15 minutes) and paste it here.",
	CLINodeNameHelp:              "How the hub shows this node, e.g. de-1. Letters, digits and -.",
	CLISummaryTitle:              "Summary",
	CLISummaryRole:               "Role",
	CLISummaryName:               "Name",
	CLISummaryPublicIP:           "Public IP",
	CLISummaryControlPort:        "Control port",
	CLISummaryKernel:             "Kernel profile",
	CLISummaryHub:                "Hub",
	CLIKernelBalanced:            "balanced (BBR, larger buffers)",
	CLIKernelUnchanged:           "unchanged",
	CLINextTitle:                 "Next: add a node (the server abroad that runs your VPN service)",
	CLINextJoin:                  "1. Run this command on the node (one node per command, valid until %s, %s):",
	CLINextTunnel:                "2. When the node is online (deyroute node list), create a tunnel on this hub:",
	CLINextTunnelMenu:            "in the menu: run deyroute, then 2) Tunnels → 1) Add tunnel",
	CLINextTunnelCLI:             "or directly: deyroute tunnel add --node <node id> --ports 443",
	CLINodeNextTitle:             "Next, on the hub (%s):",
	CLINodeNextHub:               "deyroute tunnel add --node %s --ports <port of your VPN service>",
	CLIDetectedPrivate:           "! %s is not a public IP address (private, CGNAT or loopback): type the address users connect to.",
	CLISetupHubStart:             "Setting up hub %s ...",
	CLISetupHubDone:              "Hub %s is ready: %s, control port %d.",
	CLISetupPrivateIP:            "! %s is not a public IP address: if users cannot reach it, set hub.public_ip with: deyroute config edit",
	CLISysctlSkipped:             "Kernel tuning not applied (no --yes); apply it later with: deyroute optimize auto",
	CLIJoinCmdLater:              "Show the join command later with: deyroute node join-command",
	CLIJoinCmdIntro:              "Run this command on the new node (one node per command, valid until %s, %s):",
	CLISetupNoTTYWhy:             "no terminal is available for the interactive setup",
	CLISetupNoTTYFix:             "run: deyroute setup --role hub --name NAME --yes   (on a node: deyroute join 'dey://...')",
	CLISetupNodeNeedsLink:        "a node joins with the link from the hub: deyroute join 'dey://TOKEN@HUB_IP:PORT#FP' [--name N]",
	CLIJoinLong:                  "Join this server to a hub as a node. The link comes from the hub (menu: Nodes ->\nShow join command, or deyroute node join-command); it is valid once, for 15 minutes,\nand pins the hub's CA fingerprint. The node creates its key, gets its certificate,\nwrites its configuration and starts deyroute-node.\nA hub behind the CDN front gives a link with a /SECRET path (dey://TOKEN@DOMAIN:PORT/SECRET#sha256:...):\nthe node then joins and stays connected through the front. Keep the link private.",
	CLIJoinExample:               "  deyroute join 'dey://TOKEN@5.6.7.8:44433#sha256:...'\n  deyroute join 'dey://TOKEN@5.6.7.8:44433#sha256:...' --name de-1\n  deyroute join 'dey://TOKEN@front.example.com:2053/SECRET#sha256:...'",
	CLIJoinDone:                  "Node %s joined hub %s; it appears on the hub dashboard shortly",
	CLIJoinIncompatible:          "! The hub runs deyroute %[1]s and this node %[2]s: this node runs no commands until both run the same release. Run the installer here again with --version %[1]s",
	CLINodeShort:                 "Manage nodes: join command, list, rename, remove, test, set-hub",
	CLINodeJoinCmdShort:          "Print the one-line join command for a new node",
	CLINodeJoinCmdExample:        "  deyroute node join-command\n  deyroute node join-command --ttl 1h",
	CLINodeListShort:             "List nodes with state, control RTT, version and load",
	CLINodeListExample:           "  deyroute node list\n  deyroute node list --json",
	CLINodeListEmpty:             "No nodes yet. Show the join command: deyroute node join-command",
	CLINodeLastError:             "Last error on %s: %s",
	CLIIncompatible:              "(incompatible)",
	CLINodeRenameShort:           "Rename a node (its id never changes)",
	CLINodeRenameExample:         "  deyroute node rename de-1 \"Germany 1\"",
	CLINodeRenamed:               "Node %s is now called %q.",
	CLINodeRemoveShort:           "Remove a node: stops its tunnels, revokes its certificate, drops its firewall entry",
	CLINodeRemoveExample:         "  deyroute node remove nl-1\n  deyroute node remove nl-1 --yes",
	CLINodeRemoveLost:            "Removing node %s:\n  - stops every tunnel transport on it (its tunnels continue on their other nodes)\n  - revokes its certificate, so it cannot connect again without a new join\n  - removes its IP address from the firewall allow list\nA tunnel whose only node it is must get another node first (deyroute tunnel backup add) or be deleted.",
	CLINodeRemoved:               "Node %s removed.",
	CLINodeTestShort:             "Test a node: control RTT, UDP probe and system information",
	CLINodeTestExample:           "  deyroute node test de-1",
	CLINodeTestTitle:             "Node %s: %s",
	CLINodeTestCtl:               "  control RTT  %s",
	CLINodeTestUDPOK:             "  UDP          ok (%s)",
	CLINodeTestUDPBlocked:        "  UDP          blocked (transports that need UDP are skipped for this node)",
	CLINodeTestSysinfo:           "  system:",
	CLINodeSetHubShort:           "On a node: point it to a hub that moved, or switch between a direct and a front connection",
	CLINodeSetHubLong:            "Point this node to its hub. Run it on the node itself.\n\n  ip:port                  connect to the hub directly. This CLEARS front mode: the node leaves the CDN front.\n  wss://DOMAIN:PORT/SECRET  connect through the hub's CDN front (ws:// for a plain-HTTP port).\n                           DOMAIN:PORT/SECRET is the host and path of the front join link.\n\nA hub that moves never changes a node that is in front mode; only this command does.",
	CLINodeSetHubExample:         "  deyroute node set-hub 5.6.7.9:44433\n  deyroute node set-hub 'wss://front.example.com:2053/SECRET'",
	CLIViaFront:                  "via front",
	CLINodeSetHubDone:            "This node now connects to hub %s.",
	CLINodeSetHubOffline:         "Hub address %s saved; the node agent is not running: systemctl start %s",
	CLIHubShort:                  "Hub operations",
	CLIHubAnnounceShort:          "Tell every node the hub's new address (after moving the hub)",
	CLIHubAnnounceLong:           "Run on the old hub after restoring its backup on a new server: every online node\nreceives the new address and reconnects there. Offline nodes need\ndeyroute node set-hub <ip:port> on the node itself.",
	CLIHubAnnounceExample:        "  deyroute hub announce-move 5.6.7.9:44433",
	CLIHubAnnounced:              "New hub address %s sent to: %s",
	CLIHubAnnounceFront:          "front: unchanged (%s): these nodes reach the hub through the CDN front, not this address",
	CLIHubAnnounceOffline:        "! Offline, not told: %s. Run on each of them: deyroute node set-hub %s",
	CLITunnelShort:               "Manage tunnels: add, list, show, edit, enable/disable, switch, failover",
	CLITunnelAddShort:            "Create a tunnel to a node (default ladder, ports checked and opened)",
	CLITunnelAddLong:             "Create a tunnel from this hub to a node. Every port is checked first; the tunnel\nstarts on the first rung of its ladder, all other rungs are warmed, the firewall\nis opened and the tunnel TLS certificate is created. Backup nodes take over when\nthe primary node fails (the same service must run on both).",
	CLITunnelAddExample:          "  deyroute tunnel add --node de-1 --ports 443,2053\n  deyroute tunnel add --node de-1 --ports 443,27015/udp --name \"Main\" --backup nl-1 --yes\n  deyroute tunnel add --node de-1 --ports 443 --ladder backhaul/wssmux,rathole/noise",
	CLITunnelAddSummary:          "New tunnel\n  node:    %s\n  ports:   %s\n  ladder:  %s\n  backup:  %s",
	CLILadderDefault:             "default (by protocol)",
	CLIAskCreateTunnel:           "Create the tunnel?",
	CLITunnelState:               "Tunnel %s: %s",
	CLITunnelListShort:           "List tunnels",
	CLITunnelListExample:         "  deyroute tunnel list\n  deyroute tunnel list --json",
	CLITunnelShowShort:           "Show a tunnel: ports, ladder, active transport, rungs and probe history",
	CLITunnelShowExample:         "  deyroute tunnel show main",
	CLIBackupRole:                "backup",
	CLIPrimaryRole:               "primary",
	CLIKeyNodes:                  "Nodes:",
	CLIKeyActive:                 "Active:",
	CLIKeyPorts:                  "Ports:",
	CLIKeyLadder:                 "Ladder:",
	CLIKeyPolicy:                 "Policy:",
	CLIKeyProbe:                  "Probe:",
	CLIKeyFailover:               "Failover:",
	CLIKeyTraffic:                "Traffic:",
	CLIActiveVia:                 "%s via %s",
	CLIUpFor:                     "up %s",
	CLIPolicyLine:                "%s  (failover paused: %s)",
	CLIProbeLine:                 "port %s · tunnel TLS %s · client IP %s",
	CLIFailbackAfter:             "after %ds",
	CLIFailoverLine:              "probe every %ds (timeout %ds) · switch after %d failures · healthy after %d · failback %s · at most %d switches/h · quarantine %ds",
	CLITrafficLine:               "in %s · out %s · %d connections",
	CLIRungsTitle:                "RUNGS",
	CLIRungWarm:                  "warm",
	CLIRungActive:                "active",
	CLIRungSkipped:               "skipped: %s",
	CLIRungQuarantine:            "quarantined until %s",
	CLIRungCold:                  "not rendered",
	CLIProbesTitle:               "PROBES (active transport)",
	CLIProbesNone:                "No probe results yet.",
	CLIProbesLine:                "%d probes: %d ok, %d failed, median RTT %s",
	CLIProbesLastFail:            "last failure %s (%s)",
	CLITunnelEditShort:           "Change a tunnel's name, ladder, failover policy or probe port",
	CLITunnelEditExample:         "  deyroute tunnel edit main --name \"Main 443\"\n  deyroute tunnel edit main --ladder stealth --policy transport_only\n  deyroute tunnel edit main --probe-port 2053\n  deyroute tunnel edit main --tls-mode acme",
	CLIEditNothing:               "nothing to change: give --name, --ladder, --policy, --probe-port or --tls-mode",
	CLITunnelUpdated:             "Tunnel %s updated.",
	CLITunnelEnableShort:         "Enable a tunnel",
	CLITunnelEnableExample:       "  deyroute tunnel enable main",
	CLITunnelDisableShort:        "Disable a tunnel (stops forwarding its ports)",
	CLITunnelDisableExample:      "  deyroute tunnel disable main\n  deyroute tunnel disable main --yes",
	CLITunnelDisableLost:         "Disabling tunnel %s stops forwarding its ports until you enable it again; users lose their connections.",
	CLITunnelEnabled:             "Tunnel %s enabled.",
	CLITunnelDisabled:            "Tunnel %s disabled.",
	CLITunnelRestartShort:        "Restart a tunnel's active transport",
	CLITunnelRestartExample:      "  deyroute tunnel restart main",
	CLITunnelRestartNote:         "Restarting tunnel %s interrupts its connections for a few seconds.",
	CLITunnelRestarted:           "Tunnel %s restarted.",
	CLITunnelDeleteShort:         "Delete a tunnel",
	CLITunnelDeleteExample:       "  deyroute tunnel delete main\n  deyroute tunnel delete main --yes",
	CLITunnelDeleteLost:          "Deleting tunnel %s permanently removes:\n  - its entry in config.yaml and every port it forwards\n  - its transport units and rendered configurations on the hub and its nodes\n  - its firewall and NAT rules\n  - its token, keys and TLS certificate\n  - its state and probe history (events are kept)",
	CLITunnelDeleted:             "Tunnel %s deleted.",
	CLITunnelSwitchShort:         "Switch a tunnel to another transport or node now",
	CLITunnelSwitchExample:       "  deyroute tunnel switch main --transport backhaul/tcpmux\n  deyroute tunnel switch main --node nl-1",
	CLISwitchOneOf:               "give exactly one of --transport or --node",
	CLITunnelSwitched:            "Tunnel %s is switching to %s.",
	CLITunnelResetShort:          "Go back to rung 1 on the primary node now",
	CLITunnelResetExample:        "  deyroute tunnel reset main",
	CLITunnelResetDone:           "Tunnel %s is going back to rung 1.",
	CLITunnelPauseShort:          "Pause automatic failover (probes continue)",
	CLITunnelPauseExample:        "  deyroute tunnel pause main",
	CLITunnelPaused:              "Failover of %s paused: probes continue, nothing switches automatically.",
	CLITunnelResumeShort:         "Resume automatic failover",
	CLITunnelResumeExample:       "  deyroute tunnel resume main",
	CLITunnelResumed:             "Failover of %s resumed.",
	CLITunnelTestLadderShort:     "Try every rung for 20 seconds and report RTT and success (interrupts the tunnel)",
	CLITunnelTestLadderExample:   "  deyroute tunnel test-ladder main\n  deyroute tunnel test-ladder main --yes --json",
	CLITestLadderLost:            "Testing the ladder of %s tries every transport for %d seconds, one after the other.\nThe tunnel is interrupted during the test; users lose their connections.",
	CLIResultOK:                  "ok",
	CLIResultFailed:              "failed",
	CLIResultSkipped:             "skipped",
	CLITunnelBackupShort:         "Add or remove a backup node of a tunnel",
	CLITunnelBackupAddShort:      "Add a backup node: the ladder is warmed on it",
	CLITunnelBackupAddExample:    "  deyroute tunnel backup add main --node nl-1",
	CLITunnelBackupRemoveShort:   "Remove a backup node from a tunnel",
	CLITunnelBackupRemoveExample: "  deyroute tunnel backup remove main --node nl-1",
	CLIBackupRemoved:             "Backup node %s removed from tunnel %s.",
	CLIPortShort:                 "Tunnel ports: add, remove, set the probe kind, check, suggest",
	CLIPortAddShort:              "Add ports to a tunnel",
	CLIPortAddLong:               "Add one or more ports to a tunnel. The port is checked first; the active transport\nrestarts to apply the change (interruption up to 3 seconds).\n\n--probe sets how the health probe tests the new TCP ports (Advanced): auto sends a TLS\nhello and accepts any answer, tcp only connects, tls needs a TLS handshake or alert,\nhttp needs an HTTP status line. deyroute port set changes it later.",
	CLIPortAddExample:            "  deyroute port add main 8443\n  deyroute port add main 8443/tcp --target 127.0.0.1:9443\n  deyroute port add main 8080 --probe http\n  deyroute port add main 27015/udp",
	CLITargetOnePort:             "--target needs exactly one port",
	CLIPortRestartNote:           "The active transport of %s restarts to apply the change (interruption up to 3 seconds).",
	CLIPortAdded:                 "Port %s added to tunnel %s (target %s).",
	CLIPortRemoveShort:           "Remove a port from a tunnel",
	CLIPortRemoveExample:         "  deyroute port remove main 8443\n  deyroute port remove main 27015/udp",
	CLIPortRemoved:               "Port %s removed from tunnel %s.",
	CLIPortSetShort:              "Change the probe kind of ports of a tunnel",
	CLIPortSetLong:               "Change how the health probe tests ports a tunnel already has (Advanced; config.yaml\nports[].probe): auto sends a TLS hello and accepts any answer, tcp only connects, tls\nneeds a TLS handshake or alert, http needs an HTTP status line. UDP maps are always\nauto. Nothing restarts.",
	CLIPortSetExample:            "  deyroute port set main 443 --probe tls\n  deyroute port set main 8080,8081 --probe http\n  deyroute port set main 443 --probe auto",
	CLIPortCheckShort:            "Check a port in four stages: local bind, firewall, from a node, via the tunnel",
	CLIPortCheckLong:             "The most important troubleshooting tool: is the port free on this server, does a\nfirewall block it, can a node reach it from the internet, and does traffic pass\nthrough the tunnel. Filtering inside Iran is not measured.\n\nWith --open, deyroute opens the port in the external firewall that blocks it: it\nshows the exact command (ufw allow 443/tcp, firewall-cmd ..., iptables ...,\nnft ...) and runs it only after you type yes (or with --yes).",
	CLIPortCheckExample:          "  deyroute port check 443\n  deyroute port check 27015/udp --node nl-1\n  deyroute port check 443 --open",
	CLIPortCheckTitle:            "Port %s",
	CLICheckBind:                 "local bind",
	CLICheckFirewall:             "firewall",
	CLICheckFromNode:             "reachable from node",
	CLICheckViaTunnel:            "reachable via tunnel",
	CLICheckFree:                 "free",
	CLICheckUsedBy:               "used by %s",
	CLICheckOnAddr:               "on %s",
	CLICheckDeyroute:             "a deyroute unit",
	CLICheckOpen:                 "open (%s)",
	CLICheckClosed:               "closed (%s)",
	CLICheckOpenWith:             "open it with: %s",
	CLICheckOpenHint:             "Let deyroute run it after a confirmation: deyroute port check %s --open",
	CLIPortOpenLost:              "To open %s, deyroute runs this command on this server (%s):\n  %s\nAfterwards anyone on the internet can connect to this port.",
	CLIPortOpenRan:               "Ran: %s",
	CLIPortOpenNow:               "Port %s is open in the firewall now (%s).",
	CLIPortOpenStill:             "Port %s is still closed by %s.",
	CLIPortOpenNothing:           "No external firewall blocks %s; nothing to open.",
	CLICheckNotTested:            "not tested (no online node)",
	CLICheckYes:                  "%s: yes (%s)",
	CLICheckNo:                   "%s: no",
	CLICheckNoTunnel:             "not tested (the port is in no tunnel)",
	CLICheckNote:                 "Note: this shows the port is open from the internet; it does not measure filtering inside Iran.",
	CLISuggestedPorts:            "Free ports: %s",
	CLIPortSuggestShort:          "Suggest free ports (Cloudflare-compatible ports first)",
	CLIPortSuggestExample:        "  deyroute port suggest\n  deyroute port suggest --count 5",
	CLILadderShort:               "Ladder profiles: list, show, create, set, delete",
	CLILadderListShort:           "List ladder profiles",
	CLILadderListExample:         "  deyroute ladder list",
	CLILadderBuiltin:             "built-in",
	CLILadderCustom:              "custom",
	CLILadderShowShort:           "Show the rungs of a ladder profile",
	CLILadderShowExample:         "  deyroute ladder show default",
	CLILadderTitle:               "Ladder %s (%s), used by: %s",
	CLILadderCreateShort:         "Create a ladder profile",
	CLILadderCreateExample:       "  deyroute ladder create stealth --rungs xray/reality,waterwall/reverse-reality,backhaul/wssmux",
	CLILadderSetShort:            "Replace the rungs of a ladder profile",
	CLILadderSetExample:          "  deyroute ladder set stealth --rungs xray/reality,backhaul/wssmux",
	CLILadderCreated:             "Ladder %s created: %s",
	CLILadderSaved:               "Ladder %s saved: %s",
	CLILadderDeleteShort:         "Delete a ladder profile (not while a tunnel uses it)",
	CLILadderDeleteExample:       "  deyroute ladder delete stealth",
	CLILadderDeleted:             "Ladder %s deleted.",
	CLIDiagShort:                 "Diagnostics: speed test and probes through a tunnel",
	CLIDiagSpeedShort:            "Measure throughput through a tunnel with the built-in generator",
	CLIDiagSpeedExample:          "  deyroute diag speed main\n  deyroute diag speed main --seconds 5",
	CLISpeedResult:               "Tunnel %s: download %s Mbit/s · upload %s Mbit/s · RTT %s (%s, %ss)",
	CLIDiagProbeShort:            "Probe a tunnel's ports now",
	CLIDiagProbeExample:          "  deyroute diag probe main\n  deyroute diag probe main --all-ports",
	CLILogsShort:                 "Show logs: a tunnel (hub and node side), the hub or the node",
	CLILogsLong:                  "Show the log of a tunnel (the hub and node sides, prefixed [hub] and [node]), of the\nhub daemon or of the node agent. Without an argument: this server's daemon.\n-f keeps streaming until Ctrl-C. Secrets are always masked.",
	CLILogsExample:               "  deyroute logs main -f\n  deyroute logs hub --since 1h\n  deyroute logs node",
	CLIEventsShort:               "List events: switches, failures, failbacks, nodes online/offline",
	CLIEventsExample:             "  deyroute events\n  deyroute events --tunnel main --since 1h\n  deyroute events --json",
	CLIDoctorShort:               "Collect diagnostics, explain problems and write a redacted support file",
	CLIDoctorLong:                "Collects the system, versions, units, logs, ports, firewall, kernel settings, nodes,\nladder probes, events and certificates, runs 15 checks and prints a short summary\nin plain language. The support file /root/deyroute-doctor-<UTC>.tar.gz has every\nsecret removed: send it together with the summary.",
	CLIDoctorExample:             "  deyroute doctor\n  deyroute doctor --node de-1\n  deyroute doctor --out /tmp/doctor.tar.gz",
	CLIDoctorLocalOnly:           "! The daemon is not running (systemctl start %s): only this server's local data was collected.",
	CLIDoctorWritten:             "Support file: %s (secrets removed; send it with the summary above)",
	CLIOptimizeShort:             "Kernel tuning: sysctl profile and BBR",
	CLIOptimizeApplyShort:        "Apply a sysctl profile (the previous values are backed up)",
	CLIOptimizeApplyLong:         "Write /etc/sysctl.d/99-deyroute.conf with the chosen profile and apply it. The values\nfrom before deyroute are saved once and come back with: deyroute optimize revert.\naggressive uses 64 MB buffers and suits servers with 4 GB RAM or more.",
	CLIOptimizeApplyExample:      "  deyroute optimize apply --profile balanced\n  deyroute optimize apply --profile off",
	CLIOptimizeApplied:           "Kernel profile %s applied.",
	CLIOptimizeRevertShort:       "Restore the kernel settings from before deyroute",
	CLIOptimizeRevertExample:     "  deyroute optimize revert",
	CLIOptimizeReverted:          "Kernel settings restored (profile now: %s).",
	CLIBBRActive:                 "BBR: active",
	CLIBBRInactive:               "BBR: available, not active",
	CLIBBRMissing:                "BBR: not available in this kernel (skipped)",
	CLISecurityShort:             "Security: tokens, CA, TLS certificates, firewall, audit",
	CLIRotateTokensShort:         "Replace the secret token of one tunnel or of every tunnel",
	CLIRotateTokensExample:       "  deyroute security rotate-tokens\n  deyroute security rotate-tokens --tunnel main --yes",
	CLIAllTunnels:                "every tunnel",
	CLIOneTunnel:                 "tunnel %s",
	CLIRotateTokensLost:          "Rotating tokens replaces the secret token of %s. The old token stops working at once; every transport of the affected tunnels restarts with the new token on the hub and on the nodes (a short interruption).",
	CLITokensRotated:             "Tokens rotated for %s.",
	CLIRotateCAShort:             "Re-issue the internal CA and the certificates of every online node",
	CLIRotateCAExample:           "  deyroute security rotate-ca",
	CLIRotateCALost:              "Rotating the CA replaces the internal certificate authority and the hub certificate and re-issues the certificate of every online node. Offline nodes can no longer connect and must join again; join links created before stop working.",
	CLICARotated:                 "CA rotated. Re-issued: %s.",
	CLICAOffline:                 "! Offline, they must join again: %s",
	CLISecurityTLSShort:          "Tunnel TLS certificates: show, renew, domain, ACME options",
	CLISecurityTLSExample:        "  deyroute security tls renew --tunnel main\n  deyroute security tls show\n  deyroute security tls domain vpn.example.com\n  deyroute security tls acme --cloudflare-token-file /root/cloudflare.token",
	CLITLSShowShort:              "Show certificates with expiry and fingerprint",
	CLITLSShowExample:            "  deyroute security tls show\n  deyroute security tls show --tunnel main",
	CLITLSRenewShort:             "Renew tunnel certificates now",
	CLITLSRenewExample:           "  deyroute security tls renew --tunnel main",
	CLITLSRenewed:                "Certificates renewed:",
	CLITLSDomainShort:            "Set the hub domain that tls mode acme gets certificates for",
	CLITLSDomainLong:             "Sets hub.domain. Tunnels in tls mode acme get a Let's Encrypt certificate for it; every tunnel certificate also names it.\nThe domain needs a DNS-only A record pointing at the hub (no Cloudflare proxy). The tunnels are rendered again.\nSwitch a tunnel to acme with: deyroute tunnel edit <id> --tls-mode acme",
	CLITLSDomainExample:          "  deyroute security tls domain vpn.example.com\n  deyroute security tls domain --clear",
	CLITLSDomainSet:              "Domain set to %s. Tunnels in tls mode acme get their certificate at the daily renewal, or now with: deyroute security tls renew",
	CLITLSDomainCleared:          "Domain removed.",
	CLITLSACMEShort:              "Set the ACME e-mail and the Cloudflare token for DNS-01 (Advanced)",
	CLITLSACMELong:               "Let's Encrypt checks the domain with HTTP-01 on port 80. When port 80 is taken, give a Cloudflare API token\n(Zone:DNS:Edit for the domain's zone) for DNS-01: put it in a file first; deyroute copies it to\n/etc/deyroute/secrets/cloudflare.token (0600) and never writes it to config.yaml or the logs. An empty value removes a setting.",
	CLITLSACMEExample:            "  deyroute security tls acme --email owner@example.com\n  deyroute security tls acme --cloudflare-token-file /root/cloudflare.token\n  deyroute security tls acme --cloudflare-token-file ''   # back to HTTP-01 on port 80",
	CLIACMENothing:               "nothing to change: give --email and/or --cloudflare-token-file",
	CLIACMEEmailSet:              "ACME e-mail set to %s.",
	CLIACMEEmailRemoved:          "ACME e-mail removed.",
	CLIACMETokenSet:              "DNS-01 through Cloudflare is on; the token is stored in %s (the file you gave may be deleted).",
	CLIACMETokenRemoved:          "Cloudflare token removed; ACME uses HTTP-01 on port 80 again.",
	CLIACMERenewHint:             "Request the certificates of tunnels in tls mode acme now with: deyroute security tls renew",
	CLISecurityFirewallShort:     "The deyroute firewall table (inet deyroute): show, apply, disable",
	CLIFirewallShowShort:         "Show the table inet deyroute and the detected firewalls",
	CLIFirewallShowExample:       "  deyroute security firewall show",
	CLIFirewallApplyShort:        "Render and apply the table inet deyroute now",
	CLIFirewallApplyExample:      "  deyroute security firewall apply",
	CLIFirewallDisableShort:      "Delete the table inet deyroute; deyroute then only suggests commands",
	CLIFirewallDisableExample:    "  deyroute security firewall disable",
	CLIFirewallApplied:           "Firewall table inet deyroute applied.",
	CLIFirewallDisabled:          "Firewall table inet deyroute removed; deyroute only suggests commands now.",
	CLIFirewallManaged:           "managed by deyroute",
	CLIFirewallSuggestOnly:       "suggestions only",
	CLIFirewallMode:              "Firewall: %s · detected: %s",
	CLIFirewallSuggested:         "Suggested commands:",
	CLIAuditShort:                "Audit: public ports, file permissions, certificates, node versions, join tokens",
	CLIAuditExample:              "  deyroute security audit",
	CLIAuditClean:                "Audit clean.",
	CLIAuditProblems:             "The audit found problems (see above).",
	CLINotifyShort:               "Notifications (Telegram)",
	CLITelegramShort:             "Telegram bot notifications: set, test, off",
	CLITelegramSetShort:          "Enable Telegram notifications",
	CLITelegramSetLong:           "Send one Telegram message per event (down, switch, failback, node offline), at most\none per minute per tunnel and type. Put the bot token in a file readable by root only\n(chmod 600); the hub posts through a node when api.telegram.org is blocked.",
	CLITelegramSetExample:        "  deyroute notify telegram set --token-file /etc/deyroute/secrets/telegram.token --chat-id 123456789",
	CLITelegramSet:               "Telegram notifications enabled (chat %s). Send a test: deyroute notify telegram test",
	CLITelegramTestShort:         "Send a test message",
	CLITelegramTestExample:       "  deyroute notify telegram test",
	CLITelegramTested:            "Test message sent.",
	CLITelegramOffShort:          "Disable Telegram notifications",
	CLITelegramOffExample:        "  deyroute notify telegram off",
	CLITelegramOff:               "Telegram notifications disabled.",
	CLIBackupShort:               "Back up /etc/deyroute and the events (encrypted with a passphrase)",
	CLIBackupLong:                "Archive /etc/deyroute (configuration, secrets, CA) and the event history into one file,\nencrypted with age and a passphrase. Without a terminal the passphrase comes from\nDEYROUTE_BACKUP_PASSPHRASE. Restoring it on a new server moves the hub: nodes keep\ntrusting it because the CA is in the backup.",
	CLIBackupExample:             "  deyroute backup\n  deyroute backup --out /root/hub.tar.gz.age\n  DEYROUTE_BACKUP_PASSPHRASE=... deyroute backup --json",
	CLIAskPassphrase:             "Backup passphrase: ",
	CLIAskPassphraseAgain:        "Repeat the passphrase: ",
	CLIPassphraseMismatch:        "The passphrases differ; try again.",
	CLIPassphraseFix:             "type it on a terminal, set %s, or use --no-encrypt and keep the file private",
	CLIBackupWritten:             "Backup written: %s",
	CLIBackupNoEvents:            "! The daemon is not running: the backup has no event history.",
	CLIBackupKeepPass:            "Keep the passphrase safe: the backup cannot be restored without it.",
	CLIBackupPlainWarn:           "! This file is not encrypted and holds the CA key and every secret: keep it private.",
	CLIRestoreShort:              "Restore a backup (replaces this server's configuration)",
	CLIRestoreLong:               "Replace /etc/deyroute with a backup: the file is decrypted and validated first, an\nolder schema is migrated, then the service restarts and re-renders every tunnel.\nThe current directory is kept as /etc/deyroute.pre-restore-<time>. A hub restored on\na new server takes the new public IP; tell the nodes with deyroute hub announce-move\non the old hub or deyroute node set-hub on each node.",
	CLIRestoreExample:            "  deyroute restore /var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age\n  DEYROUTE_BACKUP_PASSPHRASE=... deyroute restore backup.tar.gz.age --yes",
	CLIAskMovedHub:               "This server's public IP is %s, but the backup's hub address is %s. Use this server's address (the hub moved here)?",
	CLIRestoreLost:               "Restoring %s (%s, created %s with deyroute %s) replaces this server's /etc/deyroute: configuration, secrets and certificates. The current directory is kept as /etc/deyroute.pre-restore-<time>; every change made after that backup is lost. Tunnels are re-rendered and restarted.",
	CLIRestoreMove:               "The hub address changes from %s to %s; the hub certificate is re-issued.",
	CLIRestoredHub:               "Hub %s restored.",
	CLIRestoreNextHub:            "If the hub moved to this server, tell the nodes the new address:\n  on the old hub:   deyroute hub announce-move %s\n  or on each node:  deyroute node set-hub %s",
	CLIRestoredNode:              "Node %s restored.",
	CLIRestoreNextNode:           "It reconnects to its hub by itself; check with: deyroute status",
	CLIRestoreNextMenu:           "If the hub moved to this server, tell the nodes its address %s:\n  on the old hub:   10) Backup & Restore -> 3) Announce hub move\n  or on each node:  10) Backup & Restore -> 3) Set hub address\n  (commands: deyroute hub announce-move %s, deyroute node set-hub %s)",
	CLIRestorePrevious:           "The previous configuration is in %s.",
	CLIUpdateShort:               "Update deyroute (tunnels keep running)",
	CLIUpdateLong:                "Check for a new release, show its changelog, download and verify it (checksum and\nsignature), replace the binary and restart the deyroute service. Tunnel units are\nnot touched, so traffic keeps flowing. The previous binary is kept for --rollback.",
	CLIUpdateExample:             "  deyroute update --check\n  deyroute update\n  deyroute update --version 1.2.0 --yes\n  deyroute update --rollback",
	CLIUpdateFlagsConflict:       "use only one of --check, --rollback and --version",
	CLIRollbackLost:              "The previous deyroute binary is restored and the deyroute service restarts; tunnels keep running.",
	CLIRolledBack:                "Rolled back to deyroute %s.",
	CLIRolledBackLocal:           "Rolled back to the previous deyroute binary and restarted %s. Check it with: deyroute version",
	CLIRollbackRestartFailed:     "The binary was rolled back, but %s did not restart:",
	CLIUpToDate:                  "deyroute %s is up to date.",
	CLIChangelog:                 "Changes in %s:",
	CLIUpdateLost:                "Update deyroute %s -> %s: the binary is replaced and the deyroute service restarts (the nodes follow); tunnels keep running. Undo with: deyroute update --rollback",
	CLIUpdated:                   "Updated deyroute %s -> %s.",
	CLIUpdateAvailable:           "deyroute %s is installed; %s is available. Install it with: deyroute update",
	CLIUpdateBackendsShort:       "Update tunnel backends on the hub and every node",
	CLIUpdateBackendsExample:     "  deyroute update backends\n  deyroute update backends backhaul --yes",
	CLIAllBackends:               "every backend",
	CLIUpdateBackendsLost:        "Updating %s installs the new versions next to the current ones on the hub and on all nodes. An active transport whose backend changes restarts, and rolls back automatically if its probe is not green within 60 seconds.",
	CLIUpdateManifestShort:       "Fetch the latest signed backend manifest",
	CLIUpdateManifestExample:     "  deyroute update manifest",
	CLIManifestUpdated:           "Backend manifest updated (%s):",
	CLIConfigShort:               "config.yaml: show, validate, edit, apply",
	CLIConfigShowShort:           "Print config.yaml",
	CLIConfigShowExample:         "  deyroute config show\n  deyroute config show --json",
	CLIConfigValidateShort:       "Validate config.yaml (every problem with its DEY code)",
	CLIConfigValidateExample:     "  deyroute config validate",
	CLIConfigValidHub:            "%s is valid: %d tunnel(s), %d node(s).",
	CLIConfigValid:               "%s is valid (%s).",
	CLIConfigEditShort:           "Edit config.yaml in $EDITOR; validated, saved atomically and applied",
	CLIConfigEditLong:            "Open a copy of config.yaml in $EDITOR (vi when unset). After saving, the file is\nvalidated; when it has problems the editor opens again with the DEY errors as\ncomments at the top, until the file is valid or you empty it to abort. Quitting\nwithout changing an invalid file stops and keeps your edited copy. A valid file\natomically replaces config.yaml and the daemon applies it (an automatic backup is\ntaken first).",
	CLIConfigEditExample:         "  deyroute config edit\n  EDITOR=nano deyroute config edit",
	CLIConfigEditAborted:         "The file was emptied: config.yaml is unchanged.",
	CLIConfigEditKept:            "! config.yaml is unchanged; your edited copy is kept in %s (fix it there and copy it over /etc/deyroute/config.yaml, then run: deyroute config apply).",
	CLIConfigUnchanged:           "No changes.",
	CLIConfigEditInvalid:         "! %d problem(s) found; the editor opens again with them at the top (empty the file to abort).",
	CLIConfigEditHeader:          "config.yaml was NOT saved: fix the problems below and save again.",
	CLIConfigEditHeader2:         "Delete everything to abort. These comment lines are removed automatically.",
	CLIConfigSaved:               "%s saved.",
	CLIConfigNotApplied:          "! Not applied: the daemon is not running; it applies the file when it starts (systemctl start %s).",
	CLIConfigApplyShort:          "Apply config.yaml now (validate, back up, re-render, reconcile)",
	CLIConfigApplyExample:        "  deyroute config apply",
	CLIConfigApplied:             "Configuration applied; changed: %s",
	CLIConfigBackup:              "Automatic backup: %s",
	CLISettingsShort:             "Settings",
	CLIUIModeShort:               "Switch the menu between Simple and Advanced mode",
	CLIUIModeExample:             "  deyroute settings ui-mode advanced",
	CLIUIModeSet:                 "UI mode set to %s.",
	CLIUninstallShort:            "Remove deyroute from this server",
	CLIUninstallLong:             "Stop and disable every deyroute unit, delete the table inet deyroute, restore the kernel\nsettings, remove /etc/deyroute, /var/lib/deyroute (backups only when asked) and the\nbinary. On a hub --nodes first uninstalls every online node.",
	CLIUninstallExample:          "  deyroute uninstall\n  deyroute uninstall --keep-backups --yes\n  deyroute uninstall --nodes --yes",
	CLIAskKeepBackups:            "Keep the backups in /var/lib/deyroute/backups?",
	CLIAskUninstallNodes:         "Also uninstall deyroute from every online node?",
	CLIUninstallBackupsGone:      "backups included",
	CLIUninstallBackupsKept:      "except the backups in %s",
	CLIUninstallLost:             "Uninstall removes from this server:\n  - every deyroute tunnel unit and the deyroute service (stopped and disabled)\n  - the nftables table inet deyroute\n  - the kernel settings of deyroute (restored from sysctl-before-deyroute.conf)\n  - /etc/deyroute: configuration, secrets and certificates\n  - /var/lib/deyroute: state and backend binaries, %s\n  - /var/log/deyroute and the deyroute binary\nEvery tunnel stops.",
	CLIUninstallNodesToo:         "Every online node is uninstalled first, the same way.",
	CLINodesUninstalled:          "Uninstalled on the nodes: %s",
	CLIUninstalled:               "deyroute was removed from this server.",
	CLIBackupsKept:               "Backups kept in %s.",
	CLICompletionShort:           "Print a shell completion script (bash, zsh, fish)",
	CLICompletionLong:            "Print the completion script for your shell; it also completes the short name dey.",
	CLICompletionExample:         "  deyroute completion bash > /etc/bash_completion.d/deyroute\n  deyroute completion zsh > \"${fpath[1]}/_deyroute\"\n  deyroute completion fish > ~/.config/fish/completions/deyroute.fish",
	CLIMenuShort:                 "Open the menu (--once prints the first screen and exits)",
	CLIDaemonShort:               "Run a deyroute service (used by systemd)",
	CLIDaemonHubShort:            "Run the hub daemon (deyroute-hub.service)",
	CLIDaemonNodeShort:           "Run the node agent (deyroute-node.service)",
	CLIRelayShort:                "Run one half of a direct/native relay (deyroute-tun@ unit)",
	CLIFrontShimShort:            "Carry a node's tunnel connections through the hub's front (deyroute-tun@ unit)",
	CLIStatusNodesOnline:         "nodes %d/%d online",
	CLIStatusTunnelsUp:           "tunnels %d/%d UP",
	CLIStatusWarnings:            "WARNINGS",
	CLIAttnTitle:                 "NEEDS ATTENTION (%d)",
	CLIAttnTunnel:                "tunnel %s is %s",
	CLIAttnNodeOffline:           "node %s is offline",
	CLIAttnAllGood:               "Everything works: %d tunnels UP, %d nodes online",
	CLIAttnDegraded:              "DEGRADED: it works, but the health probes fail now and then",
	CLIAttnServiceDown:           "DEGRADED: the VPN service on the node does not answer",
	CLIAttnSwitching:             "switching to another rung",
	CLIAttnStarting:              "starting",
	CLIAttnDown:                  "DOWN: no rung works (see deyroute logs tunnel)",
	CLIAttnInit:                  "not started yet",
	CLIColAddress:                "ADDRESS",
	CLIColControl:                "CONTROL",
	CLINodeCPUValue:              "%.0f%%",
	CLINodeRAMValue:              "%d MB",
	CLIFlagFrontShimConfig:       "front-shim.json of this tunnel instance",
	CLIFrontShort:                "Reach the hub through Cloudflare when the direct path is cut",
	CLIFrontLong:                 "Front mode puts the hub behind a Cloudflare record: nodes whose direct path to the hub is filtered connect, and carry their tunnels, through Cloudflare instead. Turn it on here, then point each such node at it with the command this prints.",
	CLIFrontEnableShort:          "Turn the Cloudflare front on (hub)",
	CLIFrontEnableExample:        "  deyroute front enable --domain t1.example.com\n  deyroute front enable --domain t1.example.com --port 8443",
	CLIFlagFrontDomain:           "the Cloudflare record (orange cloud) that points to this hub",
	CLIFlagFrontPort:             "a port Cloudflare proxies (default 2053; 2053 2083 2087 2096 8443 are HTTPS, 8080 8880 2052 2082 2086 2095 plain HTTP)",
	CLIFlagFrontTLS:              "origin TLS: auto (Cloudflare SSL Full or Flexible), off (Flexible only), custom",
	CLIFrontStatusShort:          "Show the Cloudflare front and the command for the nodes (hub)",
	CLIFrontStatusExample:        "  deyroute front status",
	CLIFrontDisableShort:         "Turn the Cloudflare front off (hub)",
	CLIFrontDisableExample:       "  deyroute front disable",
	CLIFrontDisabledNode:         "! node %s used the front: on it run  deyroute node set-hub %s",
	CLIFrontHubOnly:              "deyroute front runs on the hub; on a node use: deyroute node set-hub '<target the hub printed>'",
	CLIFrontListening:            "listening",
	CLIFrontNotListening:         "NOT listening (see: deyroute logs hub)",
	CLIFrontOff:                  "Cloudflare front: off. Turn it on with: deyroute front enable --domain <your Cloudflare record>",
	CLIFrontOn:                   "Cloudflare front: on · %s:%d · %s · tls %s",
	CLIFrontNodes:                "Nodes through the front: %s",
	CLIFrontCloudflare:           "In Cloudflare (once):\n   1. DNS: an A record %s -> %s with the orange cloud (Proxied) on\n   2. SSL/TLS -> Overview: mode %s\n   3. Network: WebSockets on\nIn the firewall panel of this server's provider: allow TCP port %d (deyroute opens it to Cloudflare's addresses only).",
	CLIFrontNodeCommand:          "On each node that should go through Cloudflare, run (keep it private, it holds the front's secret path):",
	CLIWGShort:                   "Configure a WireGuard interface (deyroute-tun@ unit)",
	CLIPairShort:                 "Run a backend and its UDP companion process together (deyroute-tun@ unit)",
}

// cli — review additions: help texts of cobra's own help command and flag,
// and messages added by the CLI review.
const (
	CLIHelpShort          Key = "cli.help_short"
	CLIHelpLong           Key = "cli.help_long"
	CLIFlagHelp           Key = "cli.flag_help"
	CLIUnknownLadder      Key = "cli.unknown_ladder"
	CLIPortBusyWhy        Key = "cli.port_busy_why"
	CLIUpdateCheckSkipped Key = "cli.update_check_skipped"
	CLIConfigSavedNode    Key = "cli.config_saved_node"
	CLIBackupEventsFailed Key = "cli.backup_events_failed"
)

// cli — English texts of the review additions.
var cliReviewEN = map[Key]string{
	CLIHelpShort:          "Help about any command",
	CLIHelpLong:           "Show the help of a command with its flags and examples:\n  deyroute help <command>   (the same as: deyroute <command> --help)",
	CLIFlagHelp:           "help for %s",
	CLIUnknownLadder:      "Unknown ladder '%s'",
	CLIPortBusyWhy:        "another program is listening on TCP port %d",
	CLIUpdateCheckSkipped: "! The release check failed (%s); installing %s as requested.",
	CLIConfigSavedNode:    "The node agent uses node.hub_addr from its next reconnect; restart it to apply everything else: systemctl restart %s",
	CLIBackupEventsFailed: "! The event history could not be exported (%s): the backup has none.",
}

// cli — merge the blocks above into the English table.
func init() {
	for k, v := range cliEN {
		en[k] = v
	}
	for k, v := range cliReviewEN {
		en[k] = v
	}
}

// tui polish (spec audit of 2026-10-01): page titles, the footer of text
// fields, one word per event type, the node facts of Nodes > Test, numbered
// choices, Telegram and backup context, log lines and the hub's progress
// steps (hub.step.<step id>; hub.title.* take arguments).
const (
	// page titles ("Tunnels - Edit tunnel", "Edit tunnel: main") and the
	// footer of a text field
	TUITitleSub          Key = "tui.title.sub"
	TUITitleOf           Key = "tui.title.of"
	FooterTyping         Key = "footer.typing"
	TUIChoiceDefault     Key = "tui.common.choice_default"
	TUIHintChoose        Key = "tui.common.hint_choose"
	TUIHintChooseDefault Key = "tui.common.hint_choose_default"
	TUIHintKeep          Key = "tui.common.hint_keep"
	TUIOr                Key = "tui.common.or"
	TUINumberRange       Key = "tui.common.number_range"

	// LAST EVENTS: the short word of every event type
	TUIEvFailback        Key = "tui.dash.ev_failback"
	TUIEvFailbackFailed  Key = "tui.dash.ev_failback_failed"
	TUIEvFlapping        Key = "tui.dash.ev_flapping"
	TUIEvNodeOnline      Key = "tui.dash.ev_node_online"
	TUIEvNodeOffline     Key = "tui.dash.ev_node_offline"
	TUIEvServiceDown     Key = "tui.dash.ev_service_down"
	TUIEvBackendCrash    Key = "tui.dash.ev_backend_crash"
	TUIEvProbeError      Key = "tui.dash.ev_probe_error"
	TUIEvUpdated         Key = "tui.dash.ev_updated"
	TUIEvRolledBack      Key = "tui.dash.ev_rolled_back"
	TUIEvBackendRollback Key = "tui.dash.ev_backend_rollback"
	TUIEvNodeIP          Key = "tui.dash.ev_node_ip"
	TUIEvACMEFailed      Key = "tui.dash.ev_acme_failed"
	TUIEvRungSkipped     Key = "tui.dash.ev_rung_skipped"
	TUIEvRungRestored    Key = "tui.dash.ev_rung_restored"
	TUIEvConfigApplied   Key = "tui.dash.ev_config_applied"
	TUIEvUpdateAvailable Key = "tui.dash.ev_update_available"

	// Nodes > Test: the facts the node reports; sizes; join expiry
	TUISysNodeID     Key = "tui.nd.sys_node_id"
	TUISysHostname   Key = "tui.nd.sys_hostname"
	TUISysOS         Key = "tui.nd.sys_os"
	TUISysKernel     Key = "tui.nd.sys_kernel"
	TUISysArch       Key = "tui.nd.sys_arch"
	TUISysCPUs       Key = "tui.nd.sys_cpus"
	TUISysMemory     Key = "tui.nd.sys_memory"
	TUISysUptime     Key = "tui.nd.sys_uptime"
	TUISysVersion    Key = "tui.nd.sys_version"
	TUISysGo         Key = "tui.nd.sys_go"
	TUIGiB           Key = "tui.common.gib"
	TUIMiB           Key = "tui.common.mib"
	TUIKiB           Key = "tui.common.kib"
	TUINdJoinExpired Key = "tui.nd.join_expired"

	// numbered choices: Edit tunnel and the Add tunnel wizard
	TUIEditLadderCustom Key = "tui.edit.ladder_custom"
	TUITLSAuto          Key = "tui.edit.tls_auto"
	TUITLSACME          Key = "tui.edit.tls_acme"
	TUITLSCustom        Key = "tui.edit.tls_custom"
	TUIWizTLSHint       Key = "tui.wiz.tls_hint"
	TUIWizNoBackup      Key = "tui.wiz.no_backup"
	TUIBkNotWarm        Key = "tui.fo.bk_not_warm"
	TUIBkWarmDetail     Key = "tui.fo.bk_warm_detail"

	// Notifications: the current settings
	TUINtLabel       Key = "tui.nt.label"
	TUINtStateOn     Key = "tui.nt.state_on"
	TUINtStateOff    Key = "tui.nt.state_off"
	TUINtChatLabel   Key = "tui.nt.chat_label"
	TUINtEventsLabel Key = "tui.nt.events_label"
	TUINtEventsHint  Key = "tui.nt.events_hint"

	// Restore: the backups on this server
	TUIBuPick    Key = "tui.bu.pick"
	TUIBuOther   Key = "tui.bu.other"
	TUIBuNoFiles Key = "tui.bu.no_files"
	TUIBuAutoTag Key = "tui.bu.auto_tag"

	// Logs: the side a tunnel log line comes from
	TUILogSource Key = "tui.dg.log_source"

	// hub progress steps, by step id
	HubStepValidate    Key = "hub.step.validate"
	HubStepBackup      Key = "hub.step.backup"
	HubStepApply       Key = "hub.step.apply"
	HubStepReconcile   Key = "hub.step.reconcile"
	HubStepInstallHub  Key = "hub.step.install_hub"
	HubStepInstallNode Key = "hub.step.install_node"
	HubStepRender      Key = "hub.step.render"
	HubStepFirewall    Key = "hub.step.firewall"
	HubStepExtFirewall Key = "hub.step.external_firewall"
	HubStepStart       Key = "hub.step.start"
	HubStepProbe       Key = "hub.step.probe"
	HubStepCheckPorts  Key = "hub.step.check_ports"
	HubStepEngine      Key = "hub.step.engine"
	HubStepRestart     Key = "hub.step.restart"
	HubStepStop        Key = "hub.step.stop"
	HubStepRemove      Key = "hub.step.remove"
	HubStepConfig      Key = "hub.step.config"
	HubStepCleanup     Key = "hub.step.cleanup"
	HubStepNewCA       Key = "hub.step.new_ca"
	HubStepHubCert     Key = "hub.step.hub_cert"
	HubStepTrustNewCA  Key = "hub.step.trust_new_ca"
	HubStepTunnelsCA   Key = "hub.step.tunnels"
	HubStepResolve     Key = "hub.step.resolve"
	HubStepDownload    Key = "hub.step.download"
	HubStepInstall     Key = "hub.step.install"
	HubStepNodes       Key = "hub.step.nodes"
	HubStepRestartHub  Key = "hub.step.restart_hub"
	HubStepSpeedServer Key = "hub.step.speed_server"
	HubStepDiagRender  Key = "hub.step.diag_render"
	HubStepDiagStart   Key = "hub.step.diag_start"
	HubStepMeasure     Key = "hub.step.measure"
	HubStepDiagStop    Key = "hub.step.diag_stop"
	// hub progress steps with arguments
	HubTitleTunnelUp    Key = "hub.title.tunnel_up"
	HubTitleBackupReady Key = "hub.title.backup_ready"
	HubTitleRung        Key = "hub.title.rung"
	HubTitleBackend     Key = "hub.title.backend"
	HubTitleRotate      Key = "hub.title.rotate"
	HubTitleNodeCert    Key = "hub.title.node_cert"
)

// tui polish — English texts (merged into en by init).
var tuiPolishEN = map[Key]string{
	TUITitleSub:          "%s - %s",
	TUITitleOf:           "%s: %s",
	FooterTyping:         "type the answer + Enter · Esc cancel · ? on an empty line: help",
	TUIChoiceDefault:     "Choice [%d]: ",
	TUIHintChoose:        "Type %s and press Enter.",
	TUIHintChooseDefault: "Type %s and press Enter. Enter alone = %d.",
	TUIHintKeep:          "Enter alone keeps %s; or type a new value.",
	TUIOr:                "or",
	TUINumberRange:       "a number from 1 to %d",

	TUIEvFailback:        "failback",
	TUIEvFailbackFailed:  "no failback",
	TUIEvFlapping:        "flapping",
	TUIEvNodeOnline:      "node online",
	TUIEvNodeOffline:     "node offline",
	TUIEvServiceDown:     "service down",
	TUIEvBackendCrash:    "crash",
	TUIEvProbeError:      "probe error",
	TUIEvUpdated:         "updated",
	TUIEvRolledBack:      "rolled back",
	TUIEvBackendRollback: "rolled back",
	TUIEvNodeIP:          "IP changed",
	TUIEvACMEFailed:      "ACME failed",
	TUIEvRungSkipped:     "rung skipped",
	TUIEvRungRestored:    "rung back",
	TUIEvConfigApplied:   "config apply",
	TUIEvUpdateAvailable: "new release",

	TUISysNodeID:     "Node id",
	TUISysHostname:   "Hostname",
	TUISysOS:         "OS",
	TUISysKernel:     "Kernel",
	TUISysArch:       "Architecture",
	TUISysCPUs:       "CPUs",
	TUISysMemory:     "Memory",
	TUISysUptime:     "Uptime",
	TUISysVersion:    "deyroute",
	TUISysGo:         "Go runtime",
	TUIGiB:           "%.1f GiB",
	TUIMiB:           "%.1f MiB",
	TUIKiB:           "%d KiB",
	TUINdJoinExpired: "Expired at %s. Press r for a new command.",

	TUIEditLadderCustom: "keep the custom order of this tunnel",
	TUITLSAuto:          "auto - a certificate of the internal CA",
	TUITLSACME:          "acme - a Let's Encrypt certificate for the hub's domain",
	TUITLSCustom:        "custom - your own certificate and key files",
	TUIWizTLSHint:       "custom (your own certificate files) is set after the tunnel exists: 2) Tunnels → 2) Edit tunnel.",
	TUIWizNoBackup:      "none",
	TUIBkNotWarm:        "backup %s: no rung is warm yet; 2) Tunnels → 7) Show details lists its rungs.",
	TUIBkWarmDetail:     "%d of %d rungs warm",

	TUINtLabel:       "Telegram",
	TUINtStateOn:     "on",
	TUINtStateOff:    "off; 1) Set up Telegram turns it on",
	TUINtChatLabel:   "Chat id",
	TUINtEventsLabel: "Events",
	TUINtEventsHint:  "Comma separated. Names: down, up, degraded, switch, failback, node_offline, node_online,\nflapping, service_down, backend_crash, probe_error, update (or a full event name).",

	TUIBuPick:    "Choose the backup to restore (newest first):",
	TUIBuOther:   "Another file (type its path)",
	TUIBuNoFiles: "No backups in %s yet.",
	TUIBuAutoTag: "automatic",

	TUILogSource: "[%s]",

	HubStepValidate:    "validate config.yaml",
	HubStepBackup:      "automatic backup",
	HubStepApply:       "apply configuration",
	HubStepReconcile:   "reconcile tunnels",
	HubStepInstallHub:  "install backend on hub",
	HubStepInstallNode: "install on node",
	HubStepRender:      "render",
	HubStepFirewall:    "firewall",
	HubStepExtFirewall: "other firewalls on the hub",
	HubStepStart:       "start",
	HubStepProbe:       "probe",
	HubStepCheckPorts:  "check ports",
	HubStepEngine:      "update failover engine",
	HubStepRestart:     "restart active transport",
	HubStepStop:        "stop units",
	HubStepRemove:      "remove units and files",
	HubStepConfig:      "update config.yaml",
	HubStepCleanup:     "remove secrets and state",
	HubStepNewCA:       "create a new internal CA",
	HubStepHubCert:     "issue the hub certificate with the new CA",
	HubStepTrustNewCA:  "nodes trust only the new CA",
	HubStepTunnelsCA:   "render the tunnels with the new CA",
	HubStepResolve:     "check the release",
	HubStepDownload:    "download and verify",
	HubStepInstall:     "install the new binary",
	HubStepNodes:       "nodes follow the hub",
	HubStepRestartHub:  "restart deyroute-hub",
	HubStepSpeedServer: "start the traffic generator on the node",
	HubStepDiagRender:  "render a temporary copy of the active transport",
	HubStepDiagStart:   "start the temporary copy",
	HubStepMeasure:     "measure ping, download and upload",
	HubStepDiagStop:    "remove the temporary copy",

	HubTitleTunnelUp:    "Tunnel %s is UP via %s (%dms)",
	HubTitleBackupReady: "backup %s ready (warm)",
	HubTitleRung:        "%s on %s",
	HubTitleBackend:     "update %s %s → %s",
	HubTitleRotate:      "rotate the backend token of tunnel %s",
	HubTitleNodeCert:    "issue a new certificate for node %s",
}

// tui polish — merge the block above into the English table.
func init() {
	for k, v := range tuiPolishEN {
		en[k] = v
	}
}
