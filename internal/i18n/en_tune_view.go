package i18n

// The layout of `deyroute optimize status` (cli/optimize_view.go).
const (
	CLITuneHeadProfile  Key = "cli.tune.head.profile"
	CLITuneHeadBBR      Key = "cli.tune.head.bbr"
	CLITuneHeadNodes    Key = "cli.tune.head.nodes"
	CLITuneHeadInSync   Key = "cli.tune.head.in_sync"
	CLITuneServerTitle  Key = "cli.tune.server_title"
	CLITuneSrvMemory    Key = "cli.tune.srv.memory"
	CLITuneSrvCPU       Key = "cli.tune.srv.cpu"
	CLITuneSrvCores     Key = "cli.tune.srv.cores"
	CLITuneSrvCore      Key = "cli.tune.srv.core"
	CLITuneSrvKernel    Key = "cli.tune.srv.kernel"
	CLITuneSrvNetwork   Key = "cli.tune.srv.network"
	CLITuneSrvNetVal    Key = "cli.tune.srv.net_val"
	CLITuneSrvSpeed     Key = "cli.tune.srv.speed"
	CLITuneSrvQdisc     Key = "cli.tune.srv.qdisc"
	CLITuneSrvConntrack Key = "cli.tune.srv.conntrack"
	CLITuneSrvCTOff     Key = "cli.tune.srv.ct_off"
	CLITuneSrvCTUsed    Key = "cli.tune.srv.ct_used"
	CLITuneSrvUsed      Key = "cli.tune.srv.used"
	CLITuneSrvFree      Key = "cli.tune.srv.free"
	CLITuneSrvTotal     Key = "cli.tune.srv.total"
	CLITuneSrvVirt      Key = "cli.tune.srv.virt"
	CLITuneColGroup     Key = "cli.tune.col.group"
	CLITuneColNow       Key = "cli.tune.col.now"
	CLITuneColSetting   Key = "cli.tune.col.setting"
	CLITuneColValue     Key = "cli.tune.col.value"
	CLITuneColState     Key = "cli.tune.col.state"
	CLITuneColTuning    Key = "cli.tune.col.tuning"
	CLITuneNodeSynced   Key = "cli.tune.node.synced"
	CLITuneNodeWaiting  Key = "cli.tune.node.waiting"
	CLITuneNodeBalanced Key = "cli.tune.node.balanced"
)

var tuneViewEN = map[Key]string{
	CLITuneHeadProfile:  "Profile",
	CLITuneHeadBBR:      "BBR",
	CLITuneHeadNodes:    "Nodes",
	CLITuneHeadInSync:   "%d/%d in sync",
	CLITuneServerTitle:  "THIS SERVER",
	CLITuneSrvMemory:    "Memory",
	CLITuneSrvCPU:       "CPU",
	CLITuneSrvCores:     "%d cores",
	CLITuneSrvCore:      "1 core",
	CLITuneSrvKernel:    "Kernel",
	CLITuneSrvNetwork:   "Network",
	CLITuneSrvNetVal:    "%s · MTU %d",
	CLITuneSrvSpeed:     "%s · %d Mb/s",
	CLITuneSrvQdisc:     "Queue",
	CLITuneSrvConntrack: "Connections",
	CLITuneSrvCTOff:     "not tracked (conntrack not loaded)",
	CLITuneSrvCTUsed:    "%s tracked",
	CLITuneSrvUsed:      "%s used",
	CLITuneSrvFree:      "%s free",
	CLITuneSrvTotal:     "%s total",
	CLITuneSrvVirt:      "Container",
	CLITuneColGroup:     "SETTING",
	CLITuneColNow:       "NOW",
	CLITuneColSetting:   "KEY",
	CLITuneColValue:     "VALUE",
	CLITuneColState:     "STATE",
	CLITuneColTuning:    "TUNING",
	CLITuneNodeSynced:   "applied",
	CLITuneNodeWaiting:  "waiting",
	CLITuneNodeBalanced: "balanced (old agent)",
}

// tuning view — merge the strings above into the English table.
func init() {
	for k, v := range tuneViewEN {
		en[k] = v
	}
}
