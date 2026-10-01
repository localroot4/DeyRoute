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
	BannerNode:      "Node: %s -> hub %s",
	BannerMode:      "Mode: %s",
	BannerNodes:     "%d nodes",
	BannerNode1:     "1 node",
	BannerTunnelsUp: "%d tunnels UP",
	BannerTunnel1Up: "1 tunnel UP",
	BannerShort:     "DEYROUTE",
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
	DoctorR11MsgBBR:      "BBR congestion control is not active",
	DoctorR11MsgOff:      "Kernel tuning is off (sysctl profile off)",
	DoctorR11Fix:         "apply the recommended profile: deyroute optimize apply --profile balanced",
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
	DoctorR15MsgJoin:     "%d join token(s) older than 15 minutes are still stored",
	DoctorR15FixJoin:     "restart the hub to purge them (systemctl restart deyroute-hub), then: deyroute security audit",
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

	// tunnel and node states (always shown as words, never color only)
	TUIStateUp        Key = "tui.state.up"
	TUIStateDegraded  Key = "tui.state.degraded"
	TUIStateSwitching Key = "tui.state.switching"
	TUIStateStarting  Key = "tui.state.starting"
	TUIStateInit      Key = "tui.state.init"
	TUIStateDown      Key = "tui.state.down"
	TUIStateDisabled  Key = "tui.state.disabled"
	TUIStatePaused    Key = "tui.state.paused"
	TUIOnline         Key = "tui.state.online"
	TUIOffline        Key = "tui.state.offline"

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
	TUIWizQNode        Key = "tui.wiz.q_node"
	TUIWizNoNode       Key = "tui.wiz.no_node"
	TUIWizAutoNode     Key = "tui.wiz.auto_node"
	TUIWizQPorts       Key = "tui.wiz.q_ports"
	TUIWizPortsHint    Key = "tui.wiz.ports_hint"
	TUIWizPortsPrompt  Key = "tui.wiz.ports_prompt"
	TUIWizChecking     Key = "tui.wiz.checking"
	TUIWizPortFree     Key = "tui.wiz.port_free"
	TUIWizPortBusy     Key = "tui.wiz.port_busy"
	TUIWizPortBusyAny  Key = "tui.wiz.port_busy_any"
	TUIWizPortError    Key = "tui.wiz.port_error"
	TUIWizChange       Key = "tui.wiz.change"
	TUIWizSkip         Key = "tui.wiz.skip"
	TUIWizStop         Key = "tui.wiz.stop"
	TUIWizNewPort      Key = "tui.wiz.new_port"
	TUIWizSuggest      Key = "tui.wiz.suggest"
	TUIWizNoPortsLeft  Key = "tui.wiz.no_ports_left"
	TUIWizDupPort      Key = "tui.wiz.dup_port"
	TUIWizStopConfirm  Key = "tui.wiz.stop_confirm"
	TUIWizQConfirm     Key = "tui.wiz.q_confirm"
	TUIWizSumNode      Key = "tui.wiz.sum_node"
	TUIWizSumPorts     Key = "tui.wiz.sum_ports"
	TUIWizSumLadder    Key = "tui.wiz.sum_ladder"
	TUIWizSumBackup    Key = "tui.wiz.sum_backup"
	TUIWizSumName      Key = "tui.wiz.sum_name"
	TUIWizSumPolicy    Key = "tui.wiz.sum_policy"
	TUIWizSumTLS       Key = "tui.wiz.sum_tls"
	TUIWizSumThresh    Key = "tui.wiz.sum_thresholds"
	TUIWizAutomatic    Key = "tui.wiz.automatic"
	TUIWizDefault      Key = "tui.wiz.default"
	TUIWizDefaultWord  Key = "tui.wiz.default_word"
	TUIWizCustom       Key = "tui.wiz.custom"
	TUIWizCreate       Key = "tui.wiz.create"
	TUIWizAdvOptions   Key = "tui.wiz.adv_options"
	TUIWizCreateItem   Key = "tui.wiz.create_item"
	TUIWizAdvIntro     Key = "tui.wiz.adv_intro"
	TUIWizName         Key = "tui.wiz.name"
	TUIWizTarget       Key = "tui.wiz.target"
	TUIWizBackupQ      Key = "tui.wiz.backup_q"
	TUIWizUnknownNode  Key = "tui.wiz.unknown_node"
	TUIWizThreshQ      Key = "tui.wiz.thresh_q"
	TUITunnelUp        Key = "tui.wiz.tunnel_up"
	TUITunnelCreated   Key = "tui.wiz.tunnel_created"
	TUIThProbeInterval Key = "tui.th.probe_interval"
	TUIThProbeTimeout  Key = "tui.th.probe_timeout"
	TUIThFail          Key = "tui.th.fail"
	TUIThRecover       Key = "tui.th.recover"
	TUIThFailback      Key = "tui.th.failback"
	TUIThFailbackAfter Key = "tui.th.failback_after"
	TUIThMaxSwitches   Key = "tui.th.max_switches"
	TUIThQuarantine    Key = "tui.th.quarantine"
	TUIThIntro         Key = "tui.th.intro"

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
	TUILadSimple   Key = "tui.lad.simple"
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
	TUIBuCreate      Key = "tui.bu.create"
	TUIBuRestore     Key = "tui.bu.restore"
	TUIBuOut         Key = "tui.bu.out"
	TUIBuEncrypt     Key = "tui.bu.encrypt"
	TUIBuPass        Key = "tui.bu.pass"  // #nosec G101 -- i18n key, not a credential
	TUIBuPass2       Key = "tui.bu.pass2" // #nosec G101 -- i18n key, not a credential
	TUIBuMismatch    Key = "tui.bu.mismatch"
	TUIBuSaved       Key = "tui.bu.saved"
	TUIBuPlainWarn   Key = "tui.bu.plain_warn"
	TUIBuPath        Key = "tui.bu.path"
	TUIBuRestPass    Key = "tui.bu.rest_pass" // #nosec G101 -- i18n key, not a credential
	TUIBuRestoreLost Key = "tui.bu.restore_lost"
	TUIBuRestored    Key = "tui.bu.restored"

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
	TUIWantOneOf:      "Enter one of: %s",
	TUIDone:           "Done.",
	TUIKeepHint:       "Press Enter to keep the value in brackets; Esc cancels.",
	TUIPickTunnel:     "Choose a tunnel:",
	TUIPickNode:       "Choose a node:",
	TUINoTunnels:      "No tunnels yet. Add one with 2) Tunnels -> 1) Add tunnel.",
	TUINotSetUpTitle:  "This server is not set up yet.",
	TUINotSetUpHub:    "Iran server (hub):      deyroute setup",
	TUINotSetUpNode:   "Foreign server (node):  paste the join command from the hub (hub menu: 3) Nodes -> 1) Show join command)",
	TUINoNodes:        "No nodes yet. Show the join command with 3) Nodes -> 1) Show join command.",
	TUIClientIP:       "client IP: %s",
	TUIClientKept:     "preserved",
	TUIClientMasked:   "masked",

	TUIStateUp:        "UP",
	TUIStateDegraded:  "DEGR",
	TUIStateSwitching: "SWITCHING",
	TUIStateStarting:  "STARTING",
	TUIStateInit:      "INIT",
	TUIStateDown:      "DOWN",
	TUIStateDisabled:  "DISABLED",
	TUIStatePaused:    "PAUSED",
	TUIOnline:         "online",
	TUIOffline:        "offline",

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
	TUIDashNoTunnels:    "No tunnels yet. Add one: 2) Tunnels -> 1) Add tunnel",
	TUIDashNoNodes:      "No nodes yet. Show the join command: 3) Nodes -> 1) Show join command",
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
	TUIEditPolicy:    "Failover policy (transport_then_node, transport_only, node_only)",
	TUIEditLadder:    "Ladder profile",
	TUIEditTLS:       "TLS mode (auto, acme, custom)",
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

	TUIWizQNode:        "1. Which node?",
	TUIWizNoNode:       "No node is online. Join a node first: 3) Nodes -> 1) Show join command.",
	TUIWizAutoNode:     "Only one node is online: %s. It is used for this tunnel.",
	TUIWizQPorts:       "2. Ports?",
	TUIWizPortsHint:    "Examples: 443,2053,8443   443/tcp,27015/udp   2000-2010   443:8443",
	TUIWizPortsPrompt:  "Ports",
	TUIWizChecking:     "Checking ports…",
	TUIWizPortFree:     "%s is free",
	TUIWizPortBusy:     "%s is used by %s",
	TUIWizPortBusyAny:  "%s is used by another program",
	TUIWizPortError:    "%s cannot be used:",
	TUIWizChange:       "Change port",
	TUIWizSkip:         "Skip this port",
	TUIWizStop:         "Stop that service (deyroute tunnel %s)",
	TUIWizNewPort:      "New port for %s",
	TUIWizSuggest:      "Free suggestions: %s",
	TUIWizNoPortsLeft:  "No ports left. Enter the ports again.",
	TUIWizDupPort:      "%s is already in the list.",
	TUIWizStopConfirm:  "Disabling tunnel %s stops forwarding all of its ports so that %s becomes free. Enable it again later with 2) Tunnels -> 3) Enable / disable.",
	TUIWizQConfirm:     "3. Confirm",
	TUIWizSumNode:      "Node",
	TUIWizSumPorts:     "Ports",
	TUIWizSumLadder:    "Ladder",
	TUIWizSumBackup:    "Backup",
	TUIWizSumName:      "Name",
	TUIWizSumPolicy:    "Policy",
	TUIWizSumTLS:       "TLS mode",
	TUIWizSumThresh:    "Thresholds",
	TUIWizAutomatic:    "automatic",
	TUIWizDefault:      "%s (default)",
	TUIWizDefaultWord:  "default",
	TUIWizCustom:       "custom",
	TUIWizCreate:       "Press Enter to create the tunnel.",
	TUIWizAdvOptions:   "Change advanced options",
	TUIWizCreateItem:   "Create",
	TUIWizAdvIntro:     "Advanced options. Press Enter to keep the value in brackets.",
	TUIWizName:         "Tunnel name (empty = automatic)",
	TUIWizTarget:       "Target for %s (host:port)",
	TUIWizBackupQ:      "Backup node id (empty = none; available: %s)",
	TUIWizUnknownNode:  "%s is not an available node.",
	TUIWizThreshQ:      "Customize failover thresholds? (y/n)",
	TUITunnelUp:        "Tunnel %s is UP via %s (%dms)",
	TUITunnelCreated:   "Tunnel %s was created; it is %s now. Watch it on 1) Dashboard.",
	TUIThProbeInterval: "Probe interval (seconds)",
	TUIThProbeTimeout:  "Probe timeout (seconds)",
	TUIThFail:          "Failed probes before a switch",
	TUIThRecover:       "Good probes to recover",
	TUIThFailback:      "Fail back to rung 1 (y/n)",
	TUIThFailbackAfter: "Fail back after (seconds)",
	TUIThMaxSwitches:   "Max automatic switches per hour",
	TUIThQuarantine:    "Quarantine of a failed transport (seconds)",
	TUIThIntro:         "Failover thresholds of %s. Press Enter to keep the value in brackets.",

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
	TUILadSimple:   "In Simple mode the ladder is chosen automatically and not shown.\nThe dashboard shows which transport each tunnel uses right now.\nSwitch to Advanced (12) Settings -> 1) UI mode) to see or change the order.",
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
	TUINdRemoveLost:   "Removing node %s (%s):\n  - stops every tunnel transport on it (tunnels: %s)\n  - revokes its certificate, so it cannot connect again without a new join\n  - removes it from the firewall allow list\nTunnels whose only node it is go DOWN.",
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
	TUIBkNoCandidates:  "No other node is available. Join another node first: 3) Nodes -> 1) Show join command.",
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
	TUIOpBalanced:      "balanced - recommended: larger buffers, BBR, fq",
	TUIOpAggressive:    "aggressive - 64MB buffers for fast links, uses more memory",
	TUIOpOff:           "off - do not change kernel settings",
	TUIOpApplyConfirm:  "The %s sysctl profile is written to /etc/sysctl.d/99-deyroute.conf and applied now. The kernel values from before deyroute are kept and can be restored with Revert.",
	TUIOpRevertConfirm: "The kernel settings saved before deyroute changed them are restored and 99-deyroute.conf is removed.",
	TUIOpApplied:       "Profile %s applied.",
	TUIOpReverted:      "Kernel settings restored (profile %s).",
	TUIOpBBRHint:       "BBR is switched on by the balanced and aggressive profiles (1) Apply profile).",
	TUIOpNoValues:      "No kernel values are applied by deyroute.",

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

	TUIBuCreate:      "Create backup",
	TUIBuRestore:     "Restore from a backup",
	TUIBuOut:         "Output file (empty = default location)",
	TUIBuEncrypt:     "Encrypt with a passphrase? (y/n)",
	TUIBuPass:        "Passphrase",
	TUIBuPass2:       "Repeat the passphrase",
	TUIBuMismatch:    "The passphrases do not match.",
	TUIBuSaved:       "Backup saved: %s",
	TUIBuPlainWarn:   "This backup is not encrypted: it contains every secret of this server. Keep it safe.",
	TUIBuPath:        "Backup file",
	TUIBuRestPass:    "Passphrase (empty if the backup is not encrypted)",
	TUIBuRestoreLost: "Restoring %s replaces this server's configuration, secrets, certificates and state with the backup contents. Every change made after that backup was taken is lost; tunnels are re-rendered and restarted.",
	TUIBuRestored:    "Restore complete. Tunnels were re-rendered from the backup.",

	TUIUpCheck:           "Check for updates",
	TUIUpApply:           "Apply update",
	TUIUpBackends:        "Update backends",
	TUIUpManifest:        "Backend manifest",
	TUIUpRollback:        "Roll back",
	TUIUpCurrent:         "Installed: %s",
	TUIUpLatest:          "Latest:    %s",
	TUIUpAvailable:       "An update is available: 11) Update -> 2) Apply update.",
	TUIUpNone:            "deyroute is up to date.",
	TUIUpChangelog:       "Changelog:",
	TUIUpApplyConfirm:    "Update deyroute %s -> %s. The deyroute service restarts; tunnels keep running. The current binary is kept for rollback.",
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
	TUIStAdvDesc:        "Advanced - adds ladder order, thresholds, limits and fingerprints (items marked *)",
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

	TUIHelpDashboard: "Live view of tunnels, nodes and the last events; it refreshes every 2 seconds.\nState words: UP green, DEGR yellow, SWITCHING blue, DOWN red, DISABLED/PAUSED gray.\nNarrow terminals (< 100 columns) hide the RTT and UP-TIME columns.\nr refreshes now; q, Esc or Enter goes back.",
	TUIHelpList:      "Type the number of an item and press Enter; 0 goes back.\nr reloads the list; q or Esc goes back.",
	TUIHelpForm:      "Type the answer and press Enter. Enter on an empty line keeps the value in brackets.\nEsc cancels without changes.",
	TUIHelpConfirm:   "Destructive actions need the word yes typed exactly; anything else cancels.\nOther confirmations: Enter continues, q or Esc cancels.",
	TUIHelpTask:      "Shows the steps of the running action and its result.\nOn an error: 1 + Enter or r retries; 0 + Enter or q goes back.",
	TUIHelpWizard:    "Add tunnel asks at most three questions: node, ports, confirm.\nEach port is checked at once; a busy port can be changed, skipped or (for deyroute tunnels) stopped.\nEsc goes back one question; on the first question it leaves the wizard without creating anything.",
	TUIHelpLadder:    "The ladder is tried from the top. Type the number of a rung + Enter to move it up,\ndown or remove it; the numbers after the rungs add a transport, load a profile or save.\nShortcuts on the selected rung: u up, d down, x remove; a add, p profile, s save.\nq or Esc leaves without saving.",
	TUIHelpLogs:      "Log lines arrive live. Up/Down and PgUp/PgDn scroll, End returns to the live view.\nq stops the stream and goes back; r restarts it.",
	TUIHelpTunnels:   "Tunnels forward ports from the hub to a node.\n1 adds a tunnel (node, ports, confirm); the other items act on one tunnel you pick.\nDelete asks you to type yes.",
	TUIHelpNodes:     "Nodes are the foreign servers. 1 shows the one-line join command for a new node\n(single use, 15 minutes). Remove asks you to type yes.",
	TUIHelpPorts:     "Check port runs the four checks: local bind, firewall, reachable from a node,\nreachable through the tunnel. Filtering inside Iran is not measured.",
	TUIHelpFailover:  "Failover moves a tunnel to the next transport or node when probes fail.\nBackup nodes need the same service as the primary node. Items marked * need Advanced mode.",
	TUIHelpDiag:      "Port check, tunnel probes, a speed test through the tunnel, live logs and the doctor bundle.",
	TUIHelpOptimize:  "Kernel tuning profiles (sysctl) and BBR. Revert restores the values from before deyroute.",
	TUIHelpSecurity:  "Rotate tokens replaces tunnel secrets (type yes). TLS shows and renews certificates.\nFirewall shows or applies the table inet deyroute.",
	TUIHelpNotify:    "Telegram sends one message per event (at most one per minute per tunnel and type).",
	TUIHelpBackup:    "Backups hold /etc/deyroute and the event history, encrypted with a passphrase by default.\nRestore replaces the current configuration (type yes).",
	TUIHelpUpdate:    "Updates never run without a question; tunnels keep running while deyroute restarts.",
	TUIHelpSettings:  "Simple mode shows the essentials; Advanced adds the items marked *.\nUninstall removes deyroute from this server (type yes).",

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
	TUIWizTLS:            "TLS mode (auto, acme; custom is set later with Edit tunnel)",
	TUILineModeHint:      "Line mode: type a number and press Enter · q goes back (esc in a text field) · an empty line shows the page again",
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
	CLIUsageHint                 Key = "cli.usage_hint"
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
	CLIWantIP                    Key = "cli.want_ip"
	CLIWantName                  Key = "cli.want_name"
	CLIWantRole                  Key = "cli.want_role"
	CLINotSetUpFix               Key = "cli.not_set_up_fix"
	CLIAsk                       Key = "cli.ask"
	CLIAskDefault                Key = "cli.ask_default"
	CLIInvalidAnswer             Key = "cli.invalid_answer"
	CLIYesNoDefYes               Key = "cli.yes_no_def_yes"
	CLIYesNoDefNo                Key = "cli.yes_no_def_no"
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
	CLIFlagYesAdd                Key = "cli.flag_yes_add"
	CLIFlagPolicy                Key = "cli.flag_policy"
	CLIFlagProbePort             Key = "cli.flag_probe_port"
	CLIFlagSwitchTransport       Key = "cli.flag_switch_transport"
	CLIFlagSwitchNode            Key = "cli.flag_switch_node"
	CLIFlagTarget                Key = "cli.flag_target"
	CLIFlagCheckNode             Key = "cli.flag_check_node"
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
	CLIWatchFooter               Key = "cli.watch_footer"
	CLISetupLong                 Key = "cli.setup_long"
	CLISetupExample              Key = "cli.setup_example"
	CLISetupWelcome              Key = "cli.setup_welcome" // #nosec G101 -- i18n key, not a credential
	CLIAskRole                   Key = "cli.ask_role"
	CLIAskHubName                Key = "cli.ask_hub_name"
	CLIAskPublicIP               Key = "cli.ask_public_ip"
	CLIAskControlPort            Key = "cli.ask_control_port"
	CLIAskSysctl                 Key = "cli.ask_sysctl"
	CLIAskJoinLink               Key = "cli.ask_join_link"
	CLIAskNodeName               Key = "cli.ask_node_name"
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
	CLIHubShort                  Key = "cli.hub_short"
	CLIHubAnnounceShort          Key = "cli.hub_announce_short"
	CLIHubAnnounceLong           Key = "cli.hub_announce_long"
	CLIHubAnnounceExample        Key = "cli.hub_announce_example"
	CLIHubAnnounced              Key = "cli.hub_announced"
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
	CLITLSShowShort              Key = "cli.tls_show_short"
	CLITLSShowExample            Key = "cli.tls_show_example"
	CLITLSRenewShort             Key = "cli.tls_renew_short"
	CLITLSRenewExample           Key = "cli.tls_renew_example"
	CLITLSRenewed                Key = "cli.tls_renewed"
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
	CLIWGShort                   Key = "cli.wg_short"
)

// cli — English texts (merged into en by init).
var cliEN = map[Key]string{
	CLIRootExample:               "  deyroute                         # open the menu (same as: dey)\n  deyroute setup                   # first-time wizard\n  deyroute status --watch          # live dashboard\n  deyroute tunnel add --node de-1 --ports 443,2053\n  deyroute doctor                  # support file for troubleshooting",
	CLIVersionExample:            "  deyroute version\n  deyroute version --json",
	CLIGroupStart:                "Getting started:",
	CLIGroupManage:               "Nodes, tunnels and ports:",
	CLIGroupDiag:                 "Diagnostics:",
	CLIGroupSystem:               "System:",
	CLIUsageHint:                 "Run 'deyroute --help' or 'deyroute <command> --help' for usage and examples.",
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
	CLIWantIP:                    "an IPv4 or IPv6 address, e.g. 5.6.7.8",
	CLIWantName:                  "a name of 1 to %d printable characters",
	CLIWantRole:                  "answer hub (1) or node (2)",
	CLINotSetUpFix:               "this server is not set up yet: run deyroute setup (or join a hub: deyroute join 'dey://...')",
	CLIAsk:                       "%s: ",
	CLIAskDefault:                "%s [%s]: ",
	CLIInvalidAnswer:             "  Not accepted: %s",
	CLIYesNoDefYes:               "[Y/n]",
	CLIYesNoDefNo:                "[y/N]",
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
	CLIFlagYesAdd:                "create at once, without showing the summary first",
	CLIFlagPolicy:                "failover policy: transport_then_node, transport_only or node_only",
	CLIFlagProbePort:             "listen port used by the health probe (0 = first TCP port)",
	CLIFlagSwitchTransport:       "transport to switch to, e.g. backhaul/tcpmux",
	CLIFlagSwitchNode:            "node to switch to, e.g. nl-1",
	CLIFlagTarget:                "target on the node (default 127.0.0.1:<port>)",
	CLIFlagCheckNode:             "node that tests reachability from outside (default: the first online node)",
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
	CLIStatusLong:                "Show the dashboard of the menu as text: every tunnel with its active node, transport,\nstate, RTT, up-time and ports; every node with its state, control RTT and version;\nthe last events and warnings. --watch refreshes it every 2 seconds.",
	CLIStatusExample:             "  deyroute status\n  deyroute status --watch\n  deyroute status --json | jq '.tunnels[] | {id, state}'",
	CLIStatusNoTunnels:           "No tunnels yet. Add one: deyroute tunnel add --node <id> --ports 443",
	CLIStatusNoNodes:             "No nodes yet. Show the join command: deyroute node join-command",
	CLIWatchFooter:               "Updated %s · refreshes every %ds · Ctrl-C quits",
	CLISetupLong:                 "Set this server up. A hub (the Iran server users connect to) takes at most five\nquestions: role, name, public IP, control port and the kernel profile; keys, the\nfirewall table and the service are automatic. It ends with the join command for\nyour nodes. A node asks for the join link printed by the hub.\n\nWithout a terminal pass the answers as flags: --role hub --name ir-1 --yes.",
	CLISetupExample:              "  deyroute setup\n  deyroute setup --role hub --name ir-1 --yes\n  deyroute setup --role hub --name ir-1 --control-port 44500",
	CLISetupWelcome:              "DEYROUTE setup: a few questions; Enter accepts the value in brackets.",
	CLIAskRole:                   "Role of this server: 1) hub (Iran, users connect here)  2) node (abroad, runs your VPN service)",
	CLIAskHubName:                "Name of this hub",
	CLIAskPublicIP:               "Public IP of this server",
	CLIAskControlPort:            "Control port for the nodes",
	CLIAskSysctl:                 "Apply the balanced kernel profile (BBR, larger buffers; undo with: deyroute optimize revert)?",
	CLIAskJoinLink:               "Join link from the hub (on the hub: deyroute node join-command)",
	CLIAskNodeName:               "Name of this node",
	CLIDetectedPrivate:           "! %s is not a public IP address (private, CGNAT or loopback): type the address users connect to.",
	CLISetupHubStart:             "Setting up hub %s ...",
	CLISetupHubDone:              "Hub %s is ready: %s, control port %d.",
	CLISetupPrivateIP:            "! %s is not a public IP address: if users cannot reach it, set hub.public_ip with: deyroute config edit",
	CLISysctlSkipped:             "Kernel profile not applied (no --yes); apply it later with: deyroute optimize apply --profile balanced",
	CLIJoinCmdLater:              "Show the join command later with: deyroute node join-command",
	CLIJoinCmdIntro:              "Run this command on the new node (one node per command, valid until %s, %s):",
	CLISetupNoTTYWhy:             "no terminal is available for the interactive setup",
	CLISetupNoTTYFix:             "run: deyroute setup --role hub --name NAME --yes   (on a node: deyroute join 'dey://...')",
	CLISetupNodeNeedsLink:        "a node joins with the link from the hub: deyroute join 'dey://TOKEN@HUB_IP:PORT#FP' [--name N]",
	CLIJoinLong:                  "Join this server to a hub as a node. The link comes from the hub (menu: Nodes ->\nShow join command, or deyroute node join-command); it is valid once, for 15 minutes,\nand pins the hub's CA fingerprint. The node creates its key, gets its certificate,\nwrites its configuration and starts deyroute-node.",
	CLIJoinExample:               "  deyroute join 'dey://TOKEN@5.6.7.8:44433#sha256:...'\n  deyroute join 'dey://TOKEN@5.6.7.8:44433#sha256:...' --name de-1",
	CLIJoinDone:                  "Node %s joined hub %s; it appears on the hub dashboard shortly",
	CLIJoinIncompatible:          "! The hub runs deyroute %s and this node %s: the hub updates this node automatically.",
	CLINodeShort:                 "Manage nodes: join command, list, rename, remove, test, set-hub",
	CLINodeJoinCmdShort:          "Print the one-line join command for a new node",
	CLINodeJoinCmdExample:        "  deyroute node join-command\n  deyroute node join-command --ttl 1h",
	CLINodeListShort:             "List nodes with state, control RTT, version and load",
	CLINodeListExample:           "  deyroute node list\n  deyroute node list --json",
	CLINodeListEmpty:             "No nodes yet. Show the join command: deyroute node join-command",
	CLIIncompatible:              "(incompatible)",
	CLINodeRenameShort:           "Rename a node (its id never changes)",
	CLINodeRenameExample:         "  deyroute node rename de-1 \"Germany 1\"",
	CLINodeRenamed:               "Node %s is now called %q.",
	CLINodeRemoveShort:           "Remove a node: stops its tunnels, revokes its certificate, drops its firewall entry",
	CLINodeRemoveExample:         "  deyroute node remove nl-1\n  deyroute node remove nl-1 --yes",
	CLINodeRemoveLost:            "Removing node %s:\n  - stops every tunnel transport on it; tunnels whose only node it is go DOWN\n  - revokes its certificate, so it cannot connect again without a new join\n  - removes its IP address from the firewall allow list",
	CLINodeRemoved:               "Node %s removed.",
	CLINodeTestShort:             "Test a node: control RTT, UDP probe and system information",
	CLINodeTestExample:           "  deyroute node test de-1",
	CLINodeTestTitle:             "Node %s: %s",
	CLINodeTestCtl:               "  control RTT  %s",
	CLINodeTestUDPOK:             "  UDP          ok (%s)",
	CLINodeTestUDPBlocked:        "  UDP          blocked (transports that need UDP are skipped for this node)",
	CLINodeTestSysinfo:           "  system:",
	CLINodeSetHubShort:           "On a node: point it to a hub that moved to a new address",
	CLINodeSetHubExample:         "  deyroute node set-hub 5.6.7.9:44433",
	CLINodeSetHubDone:            "This node now connects to hub %s.",
	CLINodeSetHubOffline:         "Hub address %s saved; the node agent is not running: systemctl start %s",
	CLIHubShort:                  "Hub operations",
	CLIHubAnnounceShort:          "Tell every node the hub's new address (after moving the hub)",
	CLIHubAnnounceLong:           "Run on the old hub after restoring its backup on a new server: every online node\nreceives the new address and reconnects there. Offline nodes need\ndeyroute node set-hub <ip:port> on the node itself.",
	CLIHubAnnounceExample:        "  deyroute hub announce-move 5.6.7.9:44433",
	CLIHubAnnounced:              "New hub address %s sent to: %s",
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
	CLITunnelEditExample:         "  deyroute tunnel edit main --name \"Main 443\"\n  deyroute tunnel edit main --ladder stealth --policy transport_only\n  deyroute tunnel edit main --probe-port 2053",
	CLIEditNothing:               "nothing to change: give --name, --ladder, --policy or --probe-port",
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
	CLIPortShort:                 "Tunnel ports: add, remove, check, suggest",
	CLIPortAddShort:              "Add ports to a tunnel",
	CLIPortAddLong:               "Add one or more ports to a tunnel. The port is checked first; the active transport\nrestarts to apply the change (interruption up to 3 seconds).",
	CLIPortAddExample:            "  deyroute port add main 8443\n  deyroute port add main 8443/tcp --target 127.0.0.1:9443\n  deyroute port add main 27015/udp",
	CLITargetOnePort:             "--target needs exactly one port",
	CLIPortRestartNote:           "The active transport of %s restarts to apply the change (interruption up to 3 seconds).",
	CLIPortAdded:                 "Port %s added to tunnel %s (target %s).",
	CLIPortRemoveShort:           "Remove a port from a tunnel",
	CLIPortRemoveExample:         "  deyroute port remove main 8443\n  deyroute port remove main 27015/udp",
	CLIPortRemoved:               "Port %s removed from tunnel %s.",
	CLIPortCheckShort:            "Check a port in four stages: local bind, firewall, from a node, via the tunnel",
	CLIPortCheckLong:             "The most important troubleshooting tool: is the port free on this server, does a\nfirewall block it, can a node reach it from the internet, and does traffic pass\nthrough the tunnel. Filtering inside Iran is not measured.",
	CLIPortCheckExample:          "  deyroute port check 443\n  deyroute port check 27015/udp --node nl-1",
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
	CLISecurityTLSShort:          "Tunnel TLS certificates: show, renew",
	CLITLSShowShort:              "Show certificates with expiry and fingerprint",
	CLITLSShowExample:            "  deyroute security tls show\n  deyroute security tls show --tunnel main",
	CLITLSRenewShort:             "Renew tunnel certificates now",
	CLITLSRenewExample:           "  deyroute security tls renew --tunnel main",
	CLITLSRenewed:                "Certificates renewed:",
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
	CLIConfigEditLong:            "Open a copy of config.yaml in $EDITOR (vi when unset). After saving, the file is\nvalidated; when it has problems the editor opens again with the DEY errors as\ncomments at the top, until the file is valid or you empty it to abort. A valid file\natomically replaces config.yaml and the daemon applies it (an automatic backup is\ntaken first).",
	CLIConfigEditExample:         "  deyroute config edit\n  EDITOR=nano deyroute config edit",
	CLIConfigEditAborted:         "The file was emptied: config.yaml is unchanged.",
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
	CLIWGShort:                   "Configure a WireGuard interface (deyroute-tun@ unit)",
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
