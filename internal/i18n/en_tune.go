package i18n

// Automatic tuning in the CLI (`deyroute optimize auto|check|status`, the
// setup wizard) and the TUI Optimize menu (7).
const (
	CLIOptimizeAutoShort      Key = "cli.optimize_auto_short"
	CLIOptimizeAutoLong       Key = "cli.optimize_auto_long"
	CLIOptimizeAutoExample    Key = "cli.optimize_auto_example"
	CLIOptimizeCheckShort     Key = "cli.optimize_check_short"
	CLIOptimizeCheckLong      Key = "cli.optimize_check_long"
	CLIOptimizeCheckExample   Key = "cli.optimize_check_example"
	CLIOptimizeStatusShort    Key = "cli.optimize_status_short"
	CLIOptimizeStatusExample  Key = "cli.optimize_status_example"
	CLIOptimizeStatusLine     Key = "cli.optimize_status_line"
	CLIOptimizeApplyAutoAlias Key = "cli.optimize_apply_auto_alias"
	CLIFlagDryRun             Key = "cli.flag.dry_run"
	CLIFlagBackends           Key = "cli.flag.backends"

	CLIColKey       Key = "cli.col.key"
	CLIColNow       Key = "cli.col.now"
	CLIColNew       Key = "cli.col.new"
	CLIColEffect    Key = "cli.col.effect"
	CLIColWhy       Key = "cli.col.why"
	CLIColWant      Key = "cli.col.want"
	CLIColLive      Key = "cli.col.live"
	CLIColChangedBy Key = "cli.col.changed_by"
	CLIColOnline    Key = "cli.col.online"
	CLIColProfile   Key = "cli.col.profile"
	CLIColPending   Key = "cli.col.pending"

	CLITunePlanTitle       Key = "cli.tune.plan_title"
	CLITuneHostHub         Key = "cli.tune.host_hub"
	CLITuneHostNode        Key = "cli.tune.host_node"
	CLITuneHostNothing     Key = "cli.tune.host_nothing"
	CLITuneHostSkipped     Key = "cli.tune.host_skipped"
	CLITuneHostPending     Key = "cli.tune.host_pending"
	CLITuneHostError       Key = "cli.tune.host_error"
	CLITuneSummary         Key = "cli.tune.summary"
	CLITuneUndo            Key = "cli.tune.undo"
	CLITuneNothing         Key = "cli.tune.nothing"
	CLITuneLost            Key = "cli.tune.lost"
	CLITuneLostPending     Key = "cli.tune.lost_pending"
	CLITuneRestarts        Key = "cli.tune.restarts"
	CLITuneApplied         Key = "cli.tune.applied"
	CLITuneDryRun          Key = "cli.tune.dry_run"
	CLITuneCheckClean      Key = "cli.tune.check_clean"
	CLITuneCheckHost       Key = "cli.tune.check_host"
	CLITuneCheckError      Key = "cli.tune.check_error"
	CLITuneCheckOK         Key = "cli.tune.check_ok"
	CLITuneRuntime         Key = "cli.tune.runtime"
	CLITuneCheckFound      Key = "cli.tune.check_found"
	CLITuneNodesTitle      Key = "cli.tune.nodes_title"
	CLITuneOldAgent        Key = "cli.tune.old_agent"
	CLITuneGroupSpeed      Key = "cli.tune.group_speed"
	CLITuneGroupBuffers    Key = "cli.tune.group_buffers"
	CLITuneGroupConns      Key = "cli.tune.group_conns"
	CLITuneGroupKeepalive  Key = "cli.tune.group_keepalive"
	CLITuneGroupConntrack  Key = "cli.tune.group_conntrack"
	CLITuneGroupServices   Key = "cli.tune.group_services"
	CLITuneGroupOther      Key = "cli.tune.group_other"
	CLITuneNoLimit         Key = "cli.tune.no_limit"
	CLIBBRValActive        Key = "cli.tune.bbr_active"
	CLIBBRValInactive      Key = "cli.tune.bbr_inactive"
	CLIBBRValMissing       Key = "cli.tune.bbr_missing"
	CLITuneStatusSection   Key = "cli.tune.status_section"
	CLITuneSettingsSection Key = "cli.tune.settings_section"
	CLITuneTotalSection    Key = "cli.tune.total_section"
	CLITuneLabelProfile    Key = "cli.tune.label_profile"
	CLITuneLabelBBR        Key = "cli.tune.label_bbr"
	CLITuneLabelServer     Key = "cli.tune.label_server"
	CLITuneHostHubTitle    Key = "cli.tune.host_hub_title"
	CLITuneHostNodeTitle   Key = "cli.tune.host_node_title"

	// Measured facts ("2.0 GiB RAM · 2 CPUs · kernel 6.1.0 · eth0 MTU 1500").
	TuneFactRAM       Key = "tune.fact.ram"
	TuneFactCPUs      Key = "tune.fact.cpus"
	TuneFactKernel    Key = "tune.fact.kernel"
	TuneFactNIC       Key = "tune.fact.nic"
	TuneFactQdisc     Key = "tune.fact.qdisc"
	TuneFactConntrack Key = "tune.fact.conntrack"
	TuneFactVirt      Key = "tune.fact.virt"

	// Effects of a change (api.TuneEffect*).
	TuneEffectNow       Key = "tune.effect.now"
	TuneEffectNextStart Key = "tune.effect.next_start"
	TuneEffectReboot    Key = "tune.effect.reboot"
	TuneEffectRestarts  Key = "tune.effect.restarts"

	// Setup wizard.
	CLIAskTuneAuto       Key = "cli.ask_tune_auto"
	CLITuneAutoHelp      Key = "cli.tune_auto_help"
	CLITunePreviewFacts  Key = "cli.tune_preview_facts"
	CLITunePreviewCount  Key = "cli.tune_preview_count"
	CLITunePreviewMore   Key = "cli.tune_preview_more"
	CLITunePreviewSkips  Key = "cli.tune_preview_skips"
	CLITunePreviewNone   Key = "cli.tune_preview_none"
	CLITunePreviewInCont Key = "cli.tune_preview_container"
	CLIKernelAuto        Key = "cli.kernel_auto"
	CLIKernelNodeAuto    Key = "cli.kernel_node_auto"

	// TUI, Optimize (7).
	TUIOpAuto          Key = "tui.op.auto"
	TUIOpCheck         Key = "tui.op.check"
	TUIOpAutoIntro     Key = "tui.op.auto_intro"
	TUIOpAutoNothing   Key = "tui.op.auto_nothing"
	TUIOpAutoApplyItem Key = "tui.op.auto_apply_item"
	TUIOpAutoConfirm   Key = "tui.op.auto_confirm"
	TUIOpAutoPending   Key = "tui.op.auto_pending"
	TUIOpAutoRestarts  Key = "tui.op.auto_restarts"
	TUIOpAutoApplied   Key = "tui.op.auto_applied"
	TUIOpCheckClean    Key = "tui.op.check_clean"
	TUIOpCheckDrift    Key = "tui.op.check_drift"
	TUIOpNodes         Key = "tui.op.nodes"
	TUIOpNodePending   Key = "tui.op.node_pending"
	TUIOpNodeOldAgent  Key = "tui.op.node_old_agent"
	TUIOpNever         Key = "tui.op.never"
)

var tuneEN = map[Key]string{
	CLIOptimizeAutoShort: "Tune every server automatically from its measured facts",
	CLIOptimizeAutoLong: "Measure the hub and every online node (RAM, CPUs, kernel, network card, conntrack) and\n" +
		"compute the kernel and service settings that suit each one. Every change is listed with its\n" +
		"current value, the new value, when it takes effect and why, and applied only after one\n" +
		"confirmation (--yes for scripts). Running it again lists nothing when nothing changed.\n" +
		"The values from before deyroute are saved and come back with: deyroute optimize revert.\n" +
		"--backends also sizes the transports to each server; that restarts the active transport of\n" +
		"the tunnels it lists (users reconnect), so it is off unless asked for.",
	CLIOptimizeAutoExample:    "  deyroute optimize auto --dry-run\n  deyroute optimize auto\n  deyroute optimize auto --yes --backends",
	CLIOptimizeCheckShort:     "Compare the tuned values with the live kernel on every server",
	CLIOptimizeCheckLong:      "Report every tuned key whose live value is no longer what deyroute set (a later sysctl.d\nfile, a runtime write or another tool), and findings such as a conntrack table close to full.\nIt changes nothing. Drift exits with code 2 (DEY-X067).",
	CLIOptimizeCheckExample:   "  deyroute optimize check\n  deyroute optimize check --json",
	CLIOptimizeStatusShort:    "Show the kernel profile of the hub and every node",
	CLIOptimizeStatusExample:  "  deyroute optimize status\n  deyroute optimize status --json",
	CLIOptimizeStatusLine:     "Kernel profile: %s",
	CLIOptimizeApplyAutoAlias: "--profile auto is the same as: deyroute optimize auto --yes",
	CLIFlagDryRun:             "show the plan only; change nothing",
	CLIFlagBackends:           "also size the transports to each server (restarts the active transport of the listed tunnels; users reconnect)",

	CLIColKey:       "KEY",
	CLIColNow:       "NOW",
	CLIColNew:       "NEW",
	CLIColEffect:    "EFFECT",
	CLIColWhy:       "WHY",
	CLIColWant:      "WANT",
	CLIColLive:      "LIVE",
	CLIColChangedBy: "CHANGED BY",
	CLIColOnline:    "ONLINE",
	CLIColProfile:   "PROFILE",
	CLIColPending:   "PENDING",

	CLITunePlanTitle:       "Automatic tuning plan",
	CLITuneHostHub:         "hub",
	CLITuneHostNode:        "node %s",
	CLITuneHostNothing:     "  nothing to change",
	CLITuneHostSkipped:     "  skipped %s: %s",
	CLITuneHostPending:     "  offline: it applies the plan when it reconnects",
	CLITuneHostError:       "  could not compute a plan: %s",
	CLITuneSummary:         "%d changes on %d servers.",
	CLITuneUndo:            "Undo any time with: deyroute optimize revert",
	CLITuneNothing:         "Nothing to change: automatic tuning is already in effect.",
	CLITuneLost:            "The %d changes listed above are applied now.",
	CLITuneLostPending:     "Offline nodes (%s) apply their plan when they reconnect.",
	CLITuneRestarts:        "Items marked \"restarts tunnels\" restart the active transport of the tunnels they name (users reconnect).",
	CLITuneApplied:         "Automatic tuning applied (profile auto).",
	CLITuneDryRun:          "Dry run: nothing was changed. Apply it with: deyroute optimize auto",
	CLITuneCheckClean:      "Every tuned value is in effect.",
	CLITuneCheckHost:       "%s · profile %s",
	CLITuneCheckError:      "  not checked: %s",
	CLITuneCheckOK:         "  every tuned value is in effect",
	CLITuneRuntime:         "a runtime write",
	CLITuneCheckFound:      "%d tuned values differ from what deyroute set.",
	CLITuneNodesTitle:      "Nodes:",
	CLITuneOldAgent:        "too old for auto (gets balanced)",
	CLITuneGroupSpeed:      "Speed and queues",
	CLITuneGroupBuffers:    "Buffers (memory per connection)",
	CLITuneGroupConns:      "Connections and ports",
	CLITuneGroupKeepalive:  "Dead connections (keepalive)",
	CLITuneGroupConntrack:  "Connection tracking",
	CLITuneGroupServices:   "Services and memory limits",
	CLITuneGroupOther:      "Other",
	CLITuneNoLimit:         "no limit",
	CLIBBRValActive:        "active",
	CLIBBRValInactive:      "available, not active",
	CLIBBRValMissing:       "not available in this kernel (skipped)",
	CLITuneStatusSection:   "AUTOMATIC TUNING",
	CLITuneSettingsSection: "SETTINGS IN EFFECT · %s",
	CLITuneTotalSection:    "TOTAL",
	CLITuneLabelProfile:    "Profile",
	CLITuneLabelBBR:        "BBR",
	CLITuneLabelServer:     "This server",
	CLITuneHostHubTitle:    "HUB",
	CLITuneHostNodeTitle:   "NODE %s",

	TuneFactRAM:       "%s RAM",
	TuneFactCPUs:      "%d CPU",
	TuneFactKernel:    "kernel %s",
	TuneFactNIC:       "%s MTU %d",
	TuneFactQdisc:     "qdisc %s",
	TuneFactConntrack: "conntrack %d of %d",
	TuneFactVirt:      "container: %s",

	TuneEffectNow:       "now",
	TuneEffectNextStart: "next start",
	TuneEffectReboot:    "after reboot",
	TuneEffectRestarts:  "restarts tunnels",

	CLIAskTuneAuto:       "Tune this server automatically (recommended)",
	CLITuneAutoHelp:      "deyroute measured this server and lists the kernel settings that suit it:",
	CLITunePreviewFacts:  "Measured: %s",
	CLITunePreviewCount:  "%d settings change (%d of them only after a reboot or the next start):",
	CLITunePreviewMore:   "Why each one: deyroute optimize auto --dry-run (after setup). You can undo it any time with: deyroute optimize revert",
	CLITunePreviewSkips:  "%d left out (for example %s: %s).",
	CLITunePreviewNone:   "Nothing to change: the kernel already has these values.",
	CLITunePreviewInCont: "This server is a container (%s): its kernel belongs to the host, so deyroute leaves every kernel setting alone.",
	CLIKernelAuto:        "automatic (%d changes)",
	CLIKernelNodeAuto:    "When the hub uses automatic tuning, it tunes this node the same way once it is online.",

	TUIOpAuto:          "Automatic tuning (recommended)",
	TUIOpCheck:         "Check tuning",
	TUIOpAutoIntro:     "%d changes on %d servers. 1) applies them after one confirmation; Revert undoes them any time.",
	TUIOpAutoNothing:   "Nothing to change: automatic tuning is already in effect on every online server.",
	TUIOpAutoApplyItem: "Apply this plan",
	TUIOpAutoConfirm:   "The %d changes listed are applied now. The values from before deyroute are kept; Revert restores them.",
	TUIOpAutoPending:   "Offline nodes (%s) apply their plan when they reconnect.",
	TUIOpAutoRestarts:  "Items marked \"restarts tunnels\" restart the active transport of the tunnels they name (users reconnect).",
	TUIOpAutoApplied:   "Automatic tuning applied (profile auto).",
	TUIOpCheckClean:    "Every tuned value is in effect.",
	TUIOpCheckDrift:    "%s: deyroute set %s, the kernel has %s (%s)",
	TUIOpNodes:         "Nodes:",
	TUIOpNodePending:   "pending",
	TUIOpNodeOldAgent:  "agent too old for auto",
	TUIOpNever:         "never tuned",
}

// tune — merge the strings above into the English table.
func init() {
	for k, v := range tuneEN {
		en[k] = v
	}
}
