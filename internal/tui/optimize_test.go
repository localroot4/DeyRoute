package tui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
)

// TestPickProfileFollowsHubRAM: the picker names the profile recommended
// for the hub's RAM, and aggressive on a hub below 4 GB is confirmed with a
// warning (section 12).
func TestPickProfileFollowsHubRAM(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	mem := uint64(1000000 * 1024)
	stub.OptimizeStatusFn = func(context.Context) (api.OptimizeStatus, error) {
		rec := "balanced"
		if mem >= 4_000_000_000 {
			rec = "aggressive"
		}
		return api.OptimizeStatus{Profile: "off", MemBytes: mem, Recommended: rec}, nil
	}
	stub.OptimizeApplyFn = func(_ context.Context, p string) (api.OptimizeStatus, error) {
		log.add("apply " + p)
		return api.OptimizeStatus{Profile: p}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})

	h.choose("7").choose("2")
	h.must("This hub has 976 MB RAM: balanced is recommended", "aggressive - 64MB buffers for fast links, for servers with 4 GB RAM or more")
	h.choose("2")
	h.must("This hub has only 976 MB RAM. aggressive is meant for servers with 4 GB or more", "The aggressive sysctl profile is written")
	h.press("enter")
	require.True(t, log.has("apply aggressive"))
	h.press("esc")

	// balanced needs no warning.
	h.choose("2").choose("1")
	h.mustNot("This hub has only")
	h.must("The balanced sysctl profile is written")
	h.press("esc")

	// An 8 GB hub: aggressive is recommended and confirmed without a warning.
	mem = 8 << 30
	h.choose("2")
	h.must("This hub has 8192 MB RAM: aggressive is recommended")
	h.choose("2")
	h.mustNot("This hub has only")
	h.must("The aggressive sysctl profile is written")
}

// tunePlan is an automatic tuning plan of the hub, a node in a container
// and an offline node.
func tunePlan(hash string) api.TunePlanReport {
	return api.TunePlanReport{Hash: hash, Hosts: []api.TuneHost{
		{Host: "hub", Role: "hub", Facts: &api.TuneFacts{MemBytes: 2 << 30, CPUs: 2, Kernel: "6.1.0", NIC: "eth0", NICMTU: 1500},
			Changes: []api.TuneChange{
				{Kind: "sysctl", Key: "net.core.rmem_max", From: "212992", To: "33554432", Reason: "larger buffers for 2 GiB RAM", Effect: "now"},
				{Kind: "sysctl", Key: "net.core.default_qdisc", From: "fq_codel", To: "fq", Reason: "fq paces every flow", Effect: "reboot"},
			}},
		{Host: "de-1", Role: "node", Facts: &api.TuneFacts{MemBytes: 1 << 30, CPUs: 1, Virt: "lxc"}, Changes: []api.TuneChange{},
			Skips: []api.TuneSkip{{Key: "net.core.rmem_max", Reason: "not in a container", Code: "DEY-X064"}, {Key: "net.core.wmem_max", Reason: "not in a container", Code: "DEY-X064"}}},
		{Host: "nl-1", Role: "node", Pending: true},
	}}
}

// Automatic tuning (7 → 1) lists the plan with reasons and effects, asks
// once and applies it with the hash that was shown, with its progress;
// cancelling applies nothing.
func TestAutoTuning(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	plans := 0
	stub.OptimizeAutoPlanFn = func(_ context.Context, o api.AutoOptions) (api.TunePlanReport, error) {
		plans++
		require.False(t, o.Backends, "the menu never restarts tunnels")
		if plans > 2 {
			return api.TunePlanReport{Hash: "h-after", Hosts: []api.TuneHost{{Host: "hub", Role: "hub", Changes: []api.TuneChange{}}}}, nil
		}
		return tunePlan("h1"), nil
	}
	stub.OptimizeAutoApplyFn = func(_ context.Context, r api.AutoApply, progress func(api.Step)) (api.TunePlanReport, error) {
		log.add("auto " + r.Hash)
		progress(api.Step{ID: "tune_hub", Title: "Tune the hub", Status: api.StepOK})
		res := tunePlan(r.Hash)
		res.Applied = true
		res.Warnings = []string{"node nl-1: offline"}
		return res, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})

	h.choose("7")
	h.must(" 1) Automatic tuning (recommended)\n", " 5) Check tuning\n")
	h.mustNot("Limits *")
	h.choose("1")
	h.must(" Automatic tuning (recommended)\n",
		"2 changes on 3 servers. 1) applies them after one confirmation; Revert undoes them any time.",
		" hub · 2.0 GiB RAM · 2 CPU · kernel 6.1.0 · eth0 MTU 1500\n",
		"  KEY                     NOW       NEW       EFFECT        WHY\n",
		"  net.core.rmem_max       212992    33554432  now           larger buffers for 2 GiB RAM\n",
		"  net.core.default_qdisc  fq_codel  fq        after reboot  fq paces every flow\n",
		" node de-1 · 1.0 GiB RAM · 1 CPU · container: lxc\n  nothing to change\n",
		"  skipped net.core.rmem_max, net.core.wmem_max: DEY-X064 not in a container\n",
		" node nl-1\n  offline: it applies the plan when it reconnects\n",
		" 1) Apply this plan\n")

	// Cancel at the confirmation: nothing is applied.
	h.choose("1")
	h.must("The 2 changes listed are applied now. The values from before deyroute are kept; Revert restores them.",
		"Offline nodes (nl-1) apply their plan when they reconnect.")
	h.choose("0")
	require.False(t, log.has("auto h1"))

	// Back on the plan (computed again), confirm: the shown hash is applied.
	h.must("1) Apply this plan")
	h.choose("1")
	h.press("enter")
	require.True(t, log.has("auto h1"))
	h.must("Tune the hub", "! node nl-1: offline", "✔ Automatic tuning applied (profile auto).", "Undo any time with: deyroute optimize revert")

	// Back again: the plan is computed once more and has nothing left.
	h.press("esc")
	h.must("Nothing to change: automatic tuning is already in effect on every online server.")
	h.mustNot("1) Apply this plan")
}

// Check tuning (7 → 5) shows drift with who changed it, the findings and
// hosts that could not be checked.
func TestCheckTuning(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	clean := false
	stub.OptimizeCheckFn = func(context.Context) (api.TuneCheck, error) {
		if clean {
			return api.TuneCheck{Clean: true, Hosts: []api.TuneHostCheck{{Host: "hub", Role: "hub", Profile: "auto"}}}, nil
		}
		return api.TuneCheck{Hosts: []api.TuneHostCheck{
			{Host: "hub", Role: "hub", Profile: "auto", Drift: []api.TuneDrift{
				{Key: "net.core.rmem_max", Want: "33554432", Live: "212992", OverriddenBy: "/etc/sysctl.d/99-zz.conf"},
				{Key: "net.core.somaxconn", Want: "65535", Live: "4096"},
			}},
			{Host: "de-1", Role: "node", Profile: "auto", Findings: []api.TuneFinding{{Check: "conntrack_fill", Severity: "warn", Message: "85% full"}}},
			{Host: "nl-1", Role: "node", Error: &api.ErrorDTO{Code: "DEY-N004", Message: "offline"}},
		}}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("7").choose("5")
	h.must(" Check tuning\n", " hub · profile auto\n",
		"  ! net.core.rmem_max: deyroute set 33554432, the kernel has 212992 (/etc/sysctl.d/99-zz.conf)\n",
		"  ! net.core.somaxconn: deyroute set 65535, the kernel has 4096 (a runtime write)\n",
		" node de-1 · profile auto\n  ! conntrack_fill: 85% full\n",
		" node nl-1 · profile -\n  ✖ DEY-N004 offline\n")
	h.mustNot("Every tuned value is in effect.")
	clean = true
	h.press("r")
	h.must(" hub · profile auto\n  every tuned value is in effect\n", "Every tuned value is in effect.")
}

// The Optimize header lists every node's tuning state; old agents and
// pending nodes are marked.
func TestOptimizeNodeRows(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	stub.OptimizeStatusFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "auto", BBRAvailable: true, BBRActive: true, Nodes: []api.NodeTuneStatus{
			{Node: "de-1", Online: true, Profile: "auto", AutoCapable: true},
			{Node: "nl-10", Online: false, Pending: true, AutoCapable: true},
			{Node: "fr-1", Online: true, Profile: "balanced"},
		}}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("7")
	h.must(" Profile: auto   BBR: active\n Nodes:\n",
		"  de-1   online   auto\n",
		"  nl-10  offline  never tuned  pending\n",
		"  fr-1   online   balanced     ! agent too old for auto\n")
}

// Settings in effect lists the groups with what each means now; a group
// opens with what it is for and every value in it.
func TestOptimizeSettingsGroups(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	stub.OptimizeStatusFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "auto", BBRAvailable: true, BBRActive: true, Applied: map[string]string{
			"net.ipv4.tcp_congestion_control": "bbr", "net.core.default_qdisc": "fq",
			"net.core.rmem_max": "16777216", "net.ipv4.tcp_rmem": "4096 131072 16777216",
			"net.ipv4.tcp_keepalive_time": "300", "net.ipv4.tcp_keepalive_intvl": "30", "net.ipv4.tcp_keepalive_probes": "5",
		}}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 100}, Local: stub})
	h.choose("7").choose("6")
	h.must("1) Speed and queues", "BBR · fq",
		"2) Buffers (memory per connection)", "up to 16 MiB per connection",
		"3) Dead connections (keepalive)", "dead connections found in about 8 min")
	h.choose("2")
	h.must("How much memory one connection may use", "rmem_max   16 MiB", "tcp_rmem   4 KiB · 128 KiB · 16 MiB")
}
