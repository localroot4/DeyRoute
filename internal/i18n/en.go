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
	TUIFoTestLost:      "Testing the ladder of %s tries every transport for 20 seconds, one after the other.\nThe tunnel is interrupted during the test (about %d minute(s)); users lose their connections.",
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
	TUIStUninstallLost:  "Uninstall removes from this server:\n  - every deyroute tunnel unit and the deyroute service (stopped and disabled)\n  - the nftables table inet deyroute\n  - the kernel settings of deyroute (restored from sysctl-before-deyroute.conf)\n  - /etc/deyroute: configuration, secrets and certificates\n  - /var/lib/deyroute: state and backend binaries%s\n  - the deyroute binary\n%sEvery tunnel stops.",
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
