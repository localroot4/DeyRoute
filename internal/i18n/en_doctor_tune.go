package i18n

// Doctor checks of the automatic tuning and the traffic monitoring
// (internal/doctor, rules R11 and R12): the message and fix of every
// finding. Every fix names the exact command to run.
const (
	DoctorTuneOverrideMsg     Key = "doctor.tune.override_msg"
	DoctorTuneOverrideFix     Key = "doctor.tune.override_fix"
	DoctorTuneDriftMsg        Key = "doctor.tune.drift_msg"
	DoctorTuneDriftFix        Key = "doctor.tune.drift_fix"
	DoctorTuneDriftItem       Key = "doctor.tune.drift_item"
	DoctorTuneListMore        Key = "doctor.tune.list_more"
	DoctorTuneNofileMsg       Key = "doctor.tune.nofile_msg"
	DoctorTuneNofileFix       Key = "doctor.tune.nofile_fix"
	DoctorTuneNofileItem      Key = "doctor.tune.nofile_item"
	DoctorTuneNofileItemPID   Key = "doctor.tune.nofile_item_pid"
	DoctorTuneQdiscMsg        Key = "doctor.tune.qdisc_msg"
	DoctorTuneQdiscFix        Key = "doctor.tune.qdisc_fix"
	DoctorTuneUDPMsg          Key = "doctor.tune.udp_msg"
	DoctorTuneUDPFix          Key = "doctor.tune.udp_fix"
	DoctorTuneUDPFixContainer Key = "doctor.tune.udp_fix_container"
	DoctorTuneContainerMsg    Key = "doctor.tune.container_msg"
	DoctorTuneContainerFix    Key = "doctor.tune.container_fix"
	DoctorTuneConntrackMsg    Key = "doctor.tune.conntrack_msg"
	DoctorTuneConntrackFix    Key = "doctor.tune.conntrack_fix"
	DoctorTuneFilesMsg        Key = "doctor.tune.files_msg"
	DoctorTuneFilesFix        Key = "doctor.tune.files_fix"
	DoctorTuneStateDBMsg      Key = "doctor.tune.statedb_msg"
	DoctorTuneStateDBFix      Key = "doctor.tune.statedb_fix"
	DoctorTuneStatsMissingMsg Key = "doctor.tune.stats_missing_msg"
	DoctorTuneStatsMissingFix Key = "doctor.tune.stats_missing_fix"
	DoctorTuneStatsUnavailMsg Key = "doctor.tune.stats_unavailable_msg"
	DoctorTuneStatsUnavailFix Key = "doctor.tune.stats_unavailable_fix"
	DoctorTuneStatsUnreadMsg  Key = "doctor.tune.stats_unreadable_msg"
	DoctorTuneStatsUnreadFix  Key = "doctor.tune.stats_unreadable_fix"
	DoctorTuneFixAuto         Key = "doctor.tune.fix_auto"
	DoctorTuneFixProfile      Key = "doctor.tune.fix_profile"
)

var doctorTuneEN = map[Key]string{
	DoctorTuneOverrideMsg:     "%s sets %s again at boot, after deyroute's 99-deyroute.conf: deyroute's tuning is lost at the next reboot",
	DoctorTuneOverrideFix:     "remove these lines from %s (or give them deyroute's values), then check: deyroute optimize check",
	DoctorTuneDriftMsg:        "%d tuned kernel setting(s) changed after deyroute applied them: %s",
	DoctorTuneDriftFix:        "apply the tuning again: %s; then check: deyroute optimize check",
	DoctorTuneDriftItem:       "%s is %s, deyroute set %s",
	DoctorTuneListMore:        "and %d more",
	DoctorTuneNofileMsg:       "Open-files limit above fs.nr_open (%d): %s; systemd cannot set it, so the service does not start again",
	DoctorTuneNofileFix:       "raise fs.nr_open (or lower LimitNOFILE where the kernel cannot): deyroute optimize auto",
	DoctorTuneNofileItem:      "%s LimitNOFILE=%d",
	DoctorTuneNofileItemPID:   "%s (pid %d) runs with %d",
	DoctorTuneQdiscMsg:        "%s still uses the %s queue although deyroute set fq as the default: fq applies after the next reboot",
	DoctorTuneQdiscFix:        "nothing to do now; it changes at the next reboot (check after it: deyroute optimize check)",
	DoctorTuneUDPMsg:          "Hysteria2/AmneziaWG rungs run here but net.core.rmem_max is only %s (QUIC needs at least 7 MiB): UDP throughput is limited",
	DoctorTuneUDPFix:          "raise the UDP buffers: deyroute optimize auto",
	DoctorTuneUDPFixContainer: "the kernel of the machine this %s container runs on decides it: ask the provider to raise net.core.rmem_max to 16777216",
	DoctorTuneContainerMsg:    "This server is a %s container: kernel tuning is skipped (the kernel belongs to the machine it runs on)",
	DoctorTuneContainerFix:    "nothing to do; deyroute optimize auto still tunes the services and the backends",
	DoctorTuneConntrackMsg:    "The connection tracking table is %d%% full (%d of %d entries): new connections are dropped when it is full",
	DoctorTuneConntrackFix:    "size the table by RAM and expire idle entries sooner: deyroute optimize auto (follow it with: deyroute optimize check)",
	DoctorTuneFilesMsg:        "%d%% of the system's file handles are in use (%d of %d)",
	DoctorTuneFilesFix:        "raise fs.file-max: deyroute optimize auto",
	DoctorTuneStateDBMsg:      "state.db uses %s of its %s budget (%d%%): the hub keeps only 6 hours of 1-minute traffic history while it is this large",
	DoctorTuneStateDBFix:      "delete tunnels you no longer use (deyroute tunnel delete <id>); their history goes with them and the size goes down by itself",
	DoctorTuneStatsMissingMsg: "Traffic monitoring is on but the accounting table inet deyroute_stats is missing: traffic is not counted",
	DoctorTuneStatsMissingFix: "the hub rebuilds it within a minute; if it stays missing, look for DEY-X061 in: deyroute logs hub",
	DoctorTuneStatsUnavailMsg: "Traffic accounting is not available on this server (%s): only connection counts are shown",
	DoctorTuneStatsUnavailFix: "install nftables (apt install nftables) and restart the hub: systemctl restart deyroute-hub; or turn it off with monitoring.enabled: false (deyroute config edit)",
	DoctorTuneStatsUnreadMsg:  "The traffic counters could not be read: %s",
	DoctorTuneStatsUnreadFix:  "look for DEY-X062 in: deyroute logs hub",
	DoctorTuneFixAuto:         "deyroute optimize auto",
	DoctorTuneFixProfile:      "deyroute optimize apply --profile %s",
}

// doctor tune — merge the strings above into the English table.
func init() {
	for k, v := range doctorTuneEN {
		en[k] = v
	}
}
