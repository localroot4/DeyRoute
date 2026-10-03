package i18n

// Automatic tuning plan (internal/sysctl): the reason of every planned
// change, the reason of every skipped item, the warnings of an apply or a
// revert and the findings of the tuning check. Arguments are strings.
const (
	TuneReasonFQ               Key = "tune.reason.fq"
	TuneReasonBBR              Key = "tune.reason.bbr"
	TuneReasonBacklog          Key = "tune.reason.backlog"
	TuneReasonPortRange        Key = "tune.reason.port_range"
	TuneReasonTimeWait         Key = "tune.reason.time_wait"
	TuneReasonKeepalive        Key = "tune.reason.keepalive"
	TuneReasonFastOpen         Key = "tune.reason.fast_open"
	TuneReasonMTUProbing       Key = "tune.reason.mtu_probing"
	TuneReasonBuffersRAM       Key = "tune.reason.buffers_ram"
	TuneReasonBuffersBDP       Key = "tune.reason.buffers_bdp"
	TuneReasonUDPMin           Key = "tune.reason.udp_min"
	TuneReasonFileMax          Key = "tune.reason.file_max"
	TuneReasonNROpen           Key = "tune.reason.nr_open"
	TuneReasonNotsentLowat     Key = "tune.reason.notsent_lowat"
	TuneReasonSlowStart        Key = "tune.reason.slow_start"
	TuneReasonUDPDefault       Key = "tune.reason.udp_default"
	TuneReasonReserved         Key = "tune.reason.reserved_ports"
	TuneReasonConntrackMax     Key = "tune.reason.conntrack_max"
	TuneReasonConntrackBuckets Key = "tune.reason.conntrack_buckets"
	TuneReasonConntrackPersist Key = "tune.reason.conntrack_persist"
	TuneReasonConntrackTimeout Key = "tune.reason.conntrack_timeout"
	TuneReasonConntrackModule  Key = "tune.reason.conntrack_module"
	TuneReasonIPForward        Key = "tune.reason.ip_forward"
	TuneReasonNotOwned         Key = "tune.reason.not_owned"

	TuneSkipContainer       Key = "tune.skip.container"
	TuneSkipMissing         Key = "tune.skip.missing"
	TuneSkipNoBBR           Key = "tune.skip.no_bbr"
	TuneSkipBBROff          Key = "tune.skip.bbr_off"
	TuneSkipNoFQ            Key = "tune.skip.no_fq"
	TuneSkipNoConntrack     Key = "tune.skip.no_conntrack"
	TuneSkipTimeoutAdmin    Key = "tune.skip.timeout_admin"
	TuneSkipReservedInvalid Key = "tune.skip.reserved_invalid"

	TuneWarnChanged      Key = "tune.warn.changed"
	TuneWarnLimitKept    Key = "tune.warn.limit_kept"
	TuneWarnForwardInUse Key = "tune.warn.forward_in_use"
	TuneWarnRestore      Key = "tune.warn.restore"

	TuneFindConntrackFill Key = "tune.find.conntrack_fill"
	TuneFindNofileFill    Key = "tune.find.nofile_fill"
	TuneFindBBRInactive   Key = "tune.find.bbr_inactive"
	TuneFindQdisc         Key = "tune.find.qdisc"
)

var tunePlanEN = map[Key]string{
	TuneReasonFQ:               "fq paces every flow; it applies to network interfaces created from now on (the running interface keeps its queue until the next boot)",
	TuneReasonBBR:              "BBR keeps throughput high on long and lossy paths (new connections)",
	TuneReasonBacklog:          "longer accept and packet queues absorb bursts of new connections",
	TuneReasonPortRange:        "more local ports for outgoing connections",
	TuneReasonTimeWait:         "closed connections free their ports sooner",
	TuneReasonKeepalive:        "dead peers are noticed within minutes instead of hours",
	TuneReasonFastOpen:         "TCP Fast Open saves a round trip on repeated connections",
	TuneReasonMTUProbing:       "recovers from paths that drop large packets",
	TuneReasonBuffersRAM:       "socket buffers up to %s, sized for %s of RAM",
	TuneReasonBuffersBDP:       "socket buffers up to %s: twice the measured bandwidth-delay product (%s), at most what %s of RAM allows",
	TuneReasonUDPMin:           "a minimum UDP buffer for QUIC and WireGuard traffic",
	TuneReasonFileMax:          "enough file handles for many connections at once",
	TuneReasonNROpen:           "lets services use LimitNOFILE=1048576",
	TuneReasonNotsentLowat:     "with BBR, a small queue of unsent data lowers the delay under load",
	TuneReasonSlowStart:        "long-lived tunnel connections keep their speed after an idle period",
	TuneReasonUDPDefault:       "UDP tunnels (Hysteria2, AmneziaWG) get 1 MiB buffers by default",
	TuneReasonReserved:         "keeps %s free for deyroute's listeners and backend control ports (added to the ports this host already reserves)",
	TuneReasonConntrackMax:     "room for %s tracked connections on %s of RAM",
	TuneReasonConntrackBuckets: "a connection hash table a quarter of the table size, so lookups stay fast",
	TuneReasonConntrackPersist: "the connection hash table keeps its size after a reboot",
	TuneReasonConntrackTimeout: "established entries of mobile clients that vanished stay 5 days by default and fill the table; this also applies to other NAT on this host (Docker, other routers)",
	TuneReasonConntrackModule:  "loads nf_conntrack at boot so its settings apply",
	TuneReasonIPForward:        "WireGuard and AmneziaWG transports forward packets",
	TuneReasonNotOwned:         "already %s on this host, more than deyroute's %s: left as it is",

	TuneSkipContainer:       "the kernel belongs to the machine this %s container runs on",
	TuneSkipMissing:         "this kernel has no such setting",
	TuneSkipNoBBR:           "tcp_bbr is not available on this kernel",
	TuneSkipBBROff:          "tuning.bbr is off",
	TuneSkipNoFQ:            "sch_fq is not available on this kernel",
	TuneSkipNoConntrack:     "nf_conntrack is not loaded and no tunnel here needs it",
	TuneSkipTimeoutAdmin:    "set to %s on this host (the kernel default is 432000): left as it is",
	TuneSkipReservedInvalid: "invalid port entry %q",

	TuneWarnChanged:      "%s changed by another program after deyroute; left at %s",
	TuneWarnLimitKept:    "%s kept at %s because %s are in use; its earlier value %s returns at the next boot",
	TuneWarnForwardInUse: "%s kept at 1: %s also forwards packets",
	TuneWarnRestore:      "restore %s: %s",

	TuneFindConntrackFill: "the connection tracking table is %s%% full (%s of %s entries)",
	TuneFindNofileFill:    "%s%% of the system's file handles are in use (%s of %s)",
	TuneFindBBRInactive:   "BBR was applied but the congestion control is %s",
	TuneFindQdisc:         "%s still uses the %s queue; fq applies after the next reboot",
}

// tune plan — merge the strings above into the English table.
func init() {
	for k, v := range tunePlanEN {
		en[k] = v
	}
}
