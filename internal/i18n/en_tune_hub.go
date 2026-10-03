package i18n

// Automatic tuning on the hub and the nodes (`deyroute optimize auto`,
// check and revert): the progress steps (hub.step.<id>), the reasons of the
// resource drop-in and backend items, and the warnings about nodes.
// Arguments are strings.
const (
	HubStepTunePlan    Key = "hub.step.tune_plan"
	HubStepTuneHub     Key = "hub.step.tune_hub"
	HubStepTuneDropins Key = "hub.step.tune_dropins"
	HubStepTuneNodes   Key = "hub.step.tune_nodes"
	HubStepTuneTunnels Key = "hub.step.tune_tunnels"

	TuneReasonDropinOOM        Key = "tune.reason.dropin_oom"
	TuneReasonDropinSlice      Key = "tune.reason.dropin_slice"
	TuneReasonDropinMemoryHigh Key = "tune.reason.dropin_memory_high"
	TuneReasonDropinGOMEMLIMIT Key = "tune.reason.dropin_gomemlimit"
	TuneReasonDropinNofile     Key = "tune.reason.dropin_nofile"
	TuneReasonDropinRemove     Key = "tune.reason.dropin_remove"
	TuneReasonBackendTier      Key = "tune.reason.backend_tier"
	TuneReasonBackendDefault   Key = "tune.reason.backend_default"
	TuneReasonWGMTU            Key = "tune.reason.wg_mtu"
	TuneReasonWGMTUDefault     Key = "tune.reason.wg_mtu_default"
	TuneNoTunnels              Key = "tune.no_tunnels"

	TuneSkipOldAgent Key = "tune.skip.old_agent"

	TuneWarnNodeOffline  Key = "tune.warn.node_offline"
	TuneWarnNodeOld      Key = "tune.warn.node_old"
	TuneWarnNodeFailed   Key = "tune.warn.node_failed"
	TuneWarnNode         Key = "tune.warn.node"
	TuneWarnTunnelFailed Key = "tune.warn.tunnel_failed"
	TuneWarnDropins      Key = "tune.warn.dropins"
)

var tuneHubEN = map[Key]string{
	HubStepTunePlan:    "check the confirmed tuning plan",
	HubStepTuneHub:     "tune the hub kernel",
	HubStepTuneDropins: "service resource limits",
	HubStepTuneNodes:   "tune the nodes",
	HubStepTuneTunnels: "re-render tunnels",

	TuneReasonDropinOOM:        "a runaway transport process is the first one the kernel stops when memory runs out (score %s); its tunnel restarts and fails over, the hub, the node agent and SSH keep running",
	TuneReasonDropinSlice:      "every transport process runs in %s, which bounds their memory together",
	TuneReasonDropinMemoryHigh: "all transport processes together are slowed down and reclaimed above %s instead of pushing other services out of memory",
	TuneReasonDropinGOMEMLIMIT: "the daemon returns memory sooner (%s) on this amount of RAM",
	TuneReasonDropinNofile:     "the open-file limit of the services is lowered to %s, the most this host allows (fs.nr_open)",
	TuneReasonDropinRemove:     "no longer needed on this host",
	TuneReasonBackendTier:      "%s: %s of RAM and %s CPUs; restarts the active rung of %s (users reconnect)",
	TuneReasonBackendDefault:   "%s: back to the backend defaults; restarts the active rung of %s (users reconnect)",
	TuneReasonWGMTU:            "the smallest interface MTU is %s, so WireGuard packets must stay below it; restarts the active rung of %s (users reconnect)",
	TuneReasonWGMTUDefault:     "every interface carries full-size packets: WireGuard uses its default MTU; restarts the active rung of %s (users reconnect)",
	TuneNoTunnels:              "no tunnel",

	TuneSkipOldAgent: "the deyroute agent on this node (%s) predates automatic tuning; it gets the balanced profile until it is updated (deyroute update)",

	TuneWarnNodeOffline:  "node %s is offline: it applies the automatic tuning when it reconnects",
	TuneWarnNodeOld:      "node %s runs deyroute %s, which predates automatic tuning: it got the balanced profile; update it (deyroute update) to tune it automatically",
	TuneWarnNodeFailed:   "node %s: %s",
	TuneWarnNode:         "node %s: %s",
	TuneWarnTunnelFailed: "tunnel %s: %s",
	TuneWarnDropins:      "service resource limits: %s",
}

// tune hub — merge the strings above into the English table.
func init() {
	for k, v := range tuneHubEN {
		en[k] = v
	}
}
