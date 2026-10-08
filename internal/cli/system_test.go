package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/state"
)

func TestDiagCommands(t *testing.T) {
	e := newEnv(t)
	var secs int
	e.stub.DiagSpeedFn = func(_ context.Context, tunnel string, seconds int, progress func(api.Step)) (api.SpeedResult, error) {
		secs = seconds
		steps(progress, api.Step{Title: "download", Status: api.StepOK})
		return api.SpeedResult{Tunnel: tunnel, Transport: "backhaul/wssmux", Seconds: float64(seconds), DownloadMbps: 94.12, UploadMbps: 41.3, RTTms: 41}, nil
	}
	out := e.ok("diag", "speed", "main")
	require.Equal(t, DefaultSpeedSeconds, secs)
	require.Contains(t, out, "Tunnel main: download 94.1 Mbit/s · upload 41.3 Mbit/s · RTT 41ms (backhaul/wssmux, 10s)")
	doc := e.json("diag", "speed", "main", "--seconds", "5")
	require.Equal(t, 5, secs)
	require.EqualValues(t, 94.12, doc["result"].(map[string]any)["download_mbps"])
	require.Contains(t, e.fail(1, "diag", "speed", "main", "--seconds", "0"), "--seconds")

	var all bool
	e.stub.DiagProbeFn = func(_ context.Context, tunnel string, allPorts bool) ([]api.ProbeReport, error) {
		all = allPorts
		return []api.ProbeReport{{Tunnel: tunnel, Port: 443, Proto: "tcp", Kind: "tls", OK: true, RTTms: 41}, {Tunnel: tunnel, Port: 2053, Proto: "tcp", Kind: "tcp", Error: "timeout"}}, nil
	}
	out = e.ok("diag", "probe", "main", "--all-ports")
	require.True(t, all)
	for _, want := range []string{"PORT", "443/tcp", "tls", "✔ ok", "41ms", "2053/tcp", "✖ failed", "timeout"} {
		require.Contains(t, out, want)
	}
	doc = e.json("diag", "probe", "main")
	require.False(t, all)
	require.Len(t, doc["probes"], 2)
}

func TestLogs(t *testing.T) {
	e := newEnv(t)
	var q api.LogQuery
	e.stub.LogsFn = func(_ context.Context, lq api.LogQuery, emit func(api.LogLine) error) error {
		q = lq
		if err := emit(api.LogLine{Source: "hub", Line: "started \x1b[31mred"}); err != nil {
			return err
		}
		return emit(api.LogLine{Source: "node", Line: "connected\n"})
	}
	out := e.ok("logs")
	require.Equal(t, api.LogQuery{Target: "hub"}, q)
	require.Contains(t, out, "[hub] started ?[31mred\n")
	require.Contains(t, out, "[node] connected\n")
	e.ok("logs", "main", "--since", "1h")
	require.Equal(t, "main", q.Target)
	require.Equal(t, testNow.Add(-time.Hour), q.Since)
	e.writeConfig(nodeConfig)
	e.ok("logs")
	require.Equal(t, "node", q.Target)
	e.ok("logs", "hub", "--json")
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	require.Len(t, lines, 2)
	var d map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &d))
	require.Equal(t, "node", d["source"])
	require.EqualValues(t, 1, d["schema"])
	require.Contains(t, e.fail(1, "logs", "a", "b"), "0 to 1")
	require.Contains(t, e.fail(1, "logs", "--since", "-1h"), "--since")

	// -f streams until Ctrl-C and then exits 0.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.stub.LogsFn = func(c context.Context, lq api.LogQuery, emit func(api.LogLine) error) error {
		q = lq
		_ = emit(api.LogLine{Source: "hub", Line: "one"})
		cancel()
		<-c.Done()
		return deyerr.Wrap(deyerr.X042, c.Err(), nil)
	}
	e.out.Reset()
	require.Equal(t, 0, Run(ctx, e.g, []string{"logs", "main", "-f"}))
	require.True(t, q.Follow)
	require.Contains(t, e.out.String(), "[hub] one")

	e.stub.LogsFn = func(context.Context, api.LogQuery, func(api.LogLine) error) error {
		return deyerr.New(deyerr.C021, deyerr.Params{"tunnel": "x"})
	}
	require.Contains(t, e.fail(1, "logs", "x"), "DEY-C021")
}

func TestEvents(t *testing.T) {
	e := newEnv(t)
	var q api.EventQuery
	e.stub.EventsFn = func(_ context.Context, eq api.EventQuery) ([]state.Event, error) {
		q = eq
		return nil, nil
	}
	require.Contains(t, e.ok("events"), "No events yet.")
	require.Equal(t, api.EventQuery{Since: testNow.Add(-DefaultEventsSince)}, q)
	e.stub.EventsFn = func(_ context.Context, eq api.EventQuery) ([]state.Event, error) {
		q = eq
		return sampleStatus().Events, nil
	}
	out := e.ok("events", "--tunnel", "main", "--since", "1h")
	require.Equal(t, api.EventQuery{Tunnel: "main", Since: testNow.Add(-time.Hour)}, q)
	for _, want := range []string{"TIME", "LEVEL", "TYPE", "TUNNEL/NODE", "MESSAGE", "CODE", "switch_transport", "tunnel_down", "probe failed 3x", "nl-1"} {
		require.Contains(t, out, want)
	}
	doc := e.json("events", "--since", "0s")
	require.True(t, q.Since.IsZero())
	require.Len(t, doc["events"], 5)
	require.Contains(t, e.fail(1, "events", "--since", "-5m"), "--since")
}

func TestOptimizeCommands(t *testing.T) {
	e := newEnv(t)
	var profile string
	e.stub.OptimizeApplyFn = func(_ context.Context, p string) (api.OptimizeStatus, error) {
		profile = p
		return api.OptimizeStatus{Profile: p, BBRAvailable: true, BBRActive: true, Applied: map[string]string{"net.core.somaxconn": "65535", "net.core.default_qdisc": "fq"}, Warnings: []string{"w1"}}, nil
	}
	out := e.ok("optimize", "apply", "--profile", "balanced")
	require.Equal(t, "balanced", profile)
	for _, want := range []string{"Kernel profile balanced applied.", "BBR ● active", "│ ✔ Speed and queues      │ fq          │\n",
		"│ ✔ Connections and ports │ queue 65535 │\n", "! w1", "deyroute optimize status --details"} {
		require.Contains(t, out, want)
	}
	doc := e.json("optimize", "apply", "--profile", "aggressive")
	require.Equal(t, "aggressive", doc["profile"])
	require.Contains(t, e.fail(1, "optimize", "apply"), "--profile")
	require.Contains(t, e.fail(1, "optimize", "apply", "--profile", "turbo"), "DEY-C013")
	e.stub.OptimizeRevertFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "off", BBRAvailable: false}, nil
	}
	out = e.ok("optimize", "revert")
	require.Contains(t, out, "Kernel settings restored (profile now: off).")
	require.Contains(t, out, "BBR ○ not available in this kernel")
	e.stub.OptimizeRevertFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "off", BBRAvailable: true}, nil
	}
	require.Contains(t, e.ok("optimize", "revert"), "BBR ◐ available, not active\n")
}

// samplePlan is an automatic tuning plan of the hub, an online node with a
// skipped item and an offline node.
func samplePlan() api.TunePlanReport {
	return api.TunePlanReport{
		Hash: "h1",
		Hosts: []api.TuneHost{
			{Host: "hub", Role: "hub", Hash: "a",
				Facts: &api.TuneFacts{MemBytes: 2 << 30, CPUs: 2, Kernel: "6.1.0-21-amd64", NIC: "eth0", NICMTU: 1500, Qdisc: "fq_codel"},
				Changes: []api.TuneChange{
					{Kind: "sysctl", Key: "net.core.rmem_max", From: "212992", To: "33554432", Reason: "larger buffers for 2 GiB RAM", Effect: "now", RaiseOnly: true},
					{Kind: "sysctl", Key: "net.core.default_qdisc", From: "fq_codel", To: "fq", Reason: "fq paces every flow", Effect: "reboot"},
					{Kind: "dropin", Key: "deyroute-hub.service", From: "", To: "GOMEMLIMIT=256MiB", Reason: "keeps the hub's memory bounded", Effect: "next-start"},
				}},
			{Host: "de-1", Role: "node", Hash: "b",
				Facts:   &api.TuneFacts{MemBytes: 1 << 30, CPUs: 1, Kernel: "5.15.0", Virt: "lxc"},
				Changes: []api.TuneChange{},
				Skips: []api.TuneSkip{
					{Key: "net.core.rmem_max", Reason: "kernel tuning is not possible in a container (lxc)", Code: "DEY-X064"},
					{Key: "net.core.wmem_max", Reason: "kernel tuning is not possible in a container (lxc)", Code: "DEY-X064"},
				}},
			{Host: "nl-1", Role: "node", Pending: true},
		},
	}
}

// sampleCheck is a tuning check with drift on the hub and a finding on a
// node.
func sampleCheck() api.TuneCheck {
	return api.TuneCheck{Hosts: []api.TuneHostCheck{
		{Host: "hub", Role: "hub", Profile: "auto", Drift: []api.TuneDrift{
			{Key: "net.core.rmem_max", Want: "33554432", Live: "212992", OverriddenBy: "/etc/sysctl.d/99-zz-local.conf"},
			{Key: "net.core.somaxconn", Want: "65535", Live: "4096"},
		}},
		{Host: "de-1", Role: "node", Profile: "auto", Findings: []api.TuneFinding{
			{Check: "conntrack_fill", Severity: "warn", Message: "the conntrack table is 85% full"},
		}},
		{Host: "nl-1", Role: "node", Error: &api.ErrorDTO{Code: "DEY-N004", Message: "node nl-1 is offline"}},
	}}
}

// The plan is listed by host with KEY, NOW, NEW, EFFECT and WHY, the skips
// and the offline node, then applied after one typed confirmation with the
// hash that was shown.
func TestOptimizeAuto(t *testing.T) {
	e := newEnv(t)
	var opts []api.AutoOptions
	var applied []api.AutoApply
	e.stub.OptimizeAutoPlanFn = func(_ context.Context, o api.AutoOptions) (api.TunePlanReport, error) {
		opts = append(opts, o)
		return samplePlan(), nil
	}
	e.stub.OptimizeAutoApplyFn = func(_ context.Context, r api.AutoApply, progress func(api.Step)) (api.TunePlanReport, error) {
		applied = append(applied, r)
		steps(progress, api.Step{ID: "tune_hub", Title: "Tune the hub", Status: api.StepOK})
		res := samplePlan()
		res.Applied = true
		res.Hosts[1].Error = &api.ErrorDTO{Code: "DEY-X064", Message: "kernel tuning is not possible in a container (lxc)"}
		res.Warnings = []string{"node nl-1: offline; it applies the plan when it reconnects"}
		return res, nil
	}

	// --dry-run lists the plan and never applies.
	out := e.ok("optimize", "auto", "--dry-run")
	require.Empty(t, applied)
	for _, want := range []string{
		"Automatic tuning plan\n",
		"\n── HUB ──", "\n  2.0 GiB RAM · 2 CPU · kernel 6.1.0-21-amd64 · eth0 MTU 1500 · qdisc fq_codel\n",
		"\n  Speed and queues\n    default_qdisc   fq_codel → fq  (after reboot)\n      · fq paces every flow\n",
		"\n  Buffers (memory per connection)\n    rmem_max   208 KiB → 32 MiB\n      · larger buffers for 2 GiB RAM\n",
		"\n  Services and memory limits\n    deyroute-hub.service   - → GOMEMLIMIT=256MiB  (next start)\n      · keeps the hub's memory bounded\n",
		"\n── NODE de-1 ──", "\n  1.0 GiB RAM · 1 CPU · kernel 5.15.0 · container: lxc\n  nothing to change\n",
		"  – skipped rmem_max, wmem_max: DEY-X064 kernel tuning is not possible in a container (lxc)\n",
		"\n── NODE nl-1 ──", "  offline: it applies the plan when it reconnects\n",
		"\n── TOTAL ──", "\n  3 changes on 3 servers.\n  Undo any time with: deyroute optimize revert\n",
		"Dry run: nothing was changed. Apply it with: deyroute optimize auto\n",
	} {
		require.Contains(t, out, want)
	}
	require.Equal(t, []api.AutoOptions{{}}, opts)

	// Without a terminal and without --yes: the plan and what happens are
	// printed, nothing is applied, exit 3.
	errOut := e.fail(deyerr.ExitNeedConfirm, "optimize", "auto")
	require.Contains(t, errOut, "The 3 changes listed above are applied now. Offline nodes (nl-1) apply their plan when they reconnect.")
	require.Contains(t, e.out.String(), "rmem_max   208 KiB → 32 MiB")
	require.Empty(t, applied)

	// A declined confirmation applies nothing.
	e.tty("no")
	require.Contains(t, e.fail(1, "optimize", "auto"), "Aborted.")
	require.Empty(t, applied)

	// "yes" applies the plan that was shown.
	e.tty("yes")
	out = e.ok("optimize", "auto", "--backends")
	require.Equal(t, []api.AutoApply{{Hash: "h1", Backends: true}}, applied)
	require.Equal(t, api.AutoOptions{Backends: true}, opts[len(opts)-1])
	for _, want := range []string{
		"Type yes to continue",
		"  ✔ Tune the hub\n",
		"  ! node de-1: DEY-X064 kernel tuning is not possible in a container (lxc)\n",
		"  ! node nl-1: offline; it applies the plan when it reconnects\n",
		"✔ Automatic tuning applied (profile auto).\nUndo any time with: deyroute optimize revert\n",
	} {
		require.Contains(t, out, want)
	}

	// --yes skips the question; apply --profile auto is the same.
	e.g.IsTTY = false
	applied = nil
	e.ok("optimize", "auto", "--yes")
	e.ok("optimize", "apply", "--profile", "auto")
	require.Equal(t, []api.AutoApply{{Hash: "h1"}, {Hash: "h1"}}, applied)

	// --json: the plan with --dry-run, the result and the steps otherwise;
	// stdout holds one document.
	doc := e.json("optimize", "auto", "--dry-run")
	require.Equal(t, "h1", doc["hash"])
	require.Len(t, doc["hosts"], 3)
	require.Nil(t, doc["applied"])
	doc = e.json("optimize", "auto", "--yes")
	require.Equal(t, true, doc["applied"])
	require.Len(t, doc["steps"], 1)
	host := doc["hosts"].([]any)[0].(map[string]any)
	change := host["changes"].([]any)[0].(map[string]any)
	require.Equal(t, "net.core.rmem_max", change["key"])
	require.Equal(t, "now", change["effect"])
	require.Equal(t, true, change["raise_only"])
	// --json without --yes and without a terminal: exit 3 with the plan on
	// stderr and the error document on stdout.
	require.Equal(t, deyerr.ExitNeedConfirm, e.run("optimize", "auto", "--json"))
	require.Contains(t, e.errOut.String(), "Automatic tuning plan")
	var errDoc map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &errDoc), e.out.String())
	require.EqualValues(t, deyerr.ExitNeedConfirm, errDoc["exit_code"])

	// A plan that changed between the list and the confirmation is refused.
	e.stub.OptimizeAutoApplyFn = func(context.Context, api.AutoApply, func(api.Step)) (api.TunePlanReport, error) {
		return api.TunePlanReport{}, deyerr.New(deyerr.X065, nil)
	}
	require.Contains(t, e.fail(deyerr.ExitSystem, "optimize", "auto", "--yes"), "DEY-X065")

	// A second run lists nothing to change and applies nothing.
	e.stub.OptimizeAutoApplyFn = func(context.Context, api.AutoApply, func(api.Step)) (api.TunePlanReport, error) {
		t.Fatal("nothing to apply")
		return api.TunePlanReport{}, nil
	}
	e.stub.OptimizeAutoPlanFn = func(context.Context, api.AutoOptions) (api.TunePlanReport, error) {
		return api.TunePlanReport{Hash: "h2", Hosts: []api.TuneHost{{Host: "hub", Role: "hub", Changes: []api.TuneChange{}}}}, nil
	}
	out = e.ok("optimize", "auto")
	require.Contains(t, out, "── HUB ──")
	require.Contains(t, out, "Nothing to change: automatic tuning is already in effect.")
	doc = e.json("optimize", "auto")
	require.Equal(t, "h2", doc["hash"])

	// Restarting items are named in the confirmation.
	e.stub.OptimizeAutoPlanFn = func(context.Context, api.AutoOptions) (api.TunePlanReport, error) {
		return api.TunePlanReport{Hash: "h3", Hosts: []api.TuneHost{{Host: "hub", Role: "hub", Changes: []api.TuneChange{
			{Kind: "backend", Key: "tuning.backend_tier", To: "medium", Reason: "restarts main", Effect: "restarts-tunnels"}}}}}, nil
	}
	require.Contains(t, e.fail(deyerr.ExitNeedConfirm, "optimize", "auto", "--backends"),
		`Items marked "restarts tunnels" restart the active transport of the tunnels they name (users reconnect).`)
	require.Contains(t, e.out.String(), "  Services and memory limits\n    tuning.backend_tier   - → medium  (restarts tunnels)\n      · restarts main\n")

	// Errors of the plan are shown as they are.
	e.stub.OptimizeAutoPlanFn = nil
	require.Contains(t, e.fail(deyerr.ExitSystem, "optimize", "auto", "--dry-run"), "DEY-X008")
	require.Contains(t, e.fail(1, "optimize", "auto", "extra"), "DEY-C025")
}

// optimize check lists drift and findings per host; drift exits 2 with
// DEY-X067, and --json keeps one document with the error inside it.
func TestOptimizeCheck(t *testing.T) {
	e := newEnv(t)
	e.stub.OptimizeCheckFn = func(context.Context) (api.TuneCheck, error) { return sampleCheck(), nil }
	errOut := e.fail(deyerr.ExitSystem, "optimize", "check")
	out := e.out.String()
	for _, want := range []string{
		"hub · profile auto\n",
		"  │ KEY                │ WANT     │ LIVE   │ CHANGED BY                     │\n",
		"  │ net.core.rmem_max  │ 33554432 │ 212992 │ /etc/sysctl.d/99-zz-local.conf │\n",
		"  │ net.core.somaxconn │ 65535    │ 4096   │ a runtime write                │\n",
		"\nnode de-1 · profile auto\n  ! conntrack_fill  the conntrack table is 85% full\n",
		"\nnode nl-1 · profile -\n  not checked: DEY-N004 node nl-1 is offline\n",
	} {
		require.Contains(t, out, want)
	}
	require.Contains(t, errOut, "DEY-X067  Tuned value net.core.rmem_max is not what deyroute set")
	require.Contains(t, errOut, "/etc/sysctl.d/99-zz-local.conf changed it")
	require.Contains(t, errOut, "2 tuned values differ from what deyroute set.")

	code := e.run("optimize", "check", "--json")
	require.Equal(t, deyerr.ExitSystem, code)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &doc), "one JSON document: %s", e.out.String())
	require.Equal(t, false, doc["clean"])
	require.Len(t, doc["hosts"], 3)
	require.EqualValues(t, deyerr.ExitSystem, doc["exit_code"])
	require.Equal(t, "DEY-X067", doc["error"].(map[string]any)["code"])
	require.Contains(t, e.errOut.String(), "DEY-X067")

	// Findings alone are reported but are no drift: exit 0.
	e.stub.OptimizeCheckFn = func(context.Context) (api.TuneCheck, error) {
		c := sampleCheck()
		c.Hosts[0].Drift = nil
		return c, nil
	}
	out = e.ok("optimize", "check")
	require.Contains(t, out, "hub · profile auto\n  every tuned value is in effect\n")
	e.stub.OptimizeCheckFn = func(context.Context) (api.TuneCheck, error) {
		return api.TuneCheck{Clean: true, Hosts: []api.TuneHostCheck{{Host: "hub", Role: "hub", Profile: "auto"}}}, nil
	}
	require.Contains(t, e.ok("optimize", "check"), "\nEvery tuned value is in effect.\n")
	doc = e.json("optimize", "check")
	require.Equal(t, true, doc["clean"])
	require.NotContains(t, doc, "error")
}

// optimize status shows the hub's facts and one row per node.
func TestOptimizeStatus(t *testing.T) {
	e := newEnv(t)
	e.stub.OptimizeStatusFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "auto", BBRAvailable: true, BBRActive: true,
			Facts: &api.TuneFacts{MemBytes: 4 << 30, CPUs: 4, Kernel: "6.8.0"},
			Nodes: []api.NodeTuneStatus{
				{Node: "de-1", Online: true, Profile: "auto", Hash: "x", AutoCapable: true},
				{Node: "nl-1", Online: false, Pending: true, AutoCapable: true},
				{Node: "fr-1", Online: true, Profile: "balanced"},
			}}, nil
	}
	out := e.ok("optimize", "status")
	for _, want := range []string{
		"── AUTOMATIC TUNING ──", " Profile ● auto   │   BBR ● active   │   Nodes ◐ 2/3 in sync\n",
		"── THIS SERVER ──", "│ Memory      │ 4.0 GiB", "│ CPU         │ 4 cores", "│ Kernel      │ 6.8.0",
		"│ Connections │ not tracked (conntrack not loaded) │",
		"── NODES ──", "  │ NODE │ STATE     │ PROFILE  │ TUNING                 │\n",
		"  │ de-1 │ ● online  │ auto     │ ✔ applied              │\n",
		"  │ nl-1 │ ○ offline │ -        │ ◐ waiting              │\n",
		"  │ fr-1 │ ● online  │ balanced │ ! balanced (old agent) │\n",
	} {
		require.Contains(t, out, want)
	}
	doc := e.json("optimize", "status")
	require.Equal(t, "auto", doc["profile"])
	require.Len(t, doc["nodes"], 3)
}

func TestSecurityCommands(t *testing.T) {
	e := newEnv(t)
	rotated := "-"
	e.stub.SecurityRotateTokensFn = func(_ context.Context, tunnel string, progress func(api.Step)) error {
		rotated = tunnel
		steps(progress, api.Step{Title: "restart main", Status: api.StepOK})
		return nil
	}
	require.Contains(t, e.fail(3, "security", "rotate-tokens"), "every tunnel")
	require.Equal(t, "-", rotated)
	out := e.ok("security", "rotate-tokens", "--yes")
	require.Equal(t, "", rotated)
	require.Contains(t, out, "Tokens rotated for every tunnel.")
	e.tty("yes")
	require.Contains(t, e.ok("security", "rotate-tokens", "--tunnel", "main"), "Tokens rotated for tunnel main.")
	require.Equal(t, "main", rotated)
	e.g.IsTTY = false
	doc := e.json("security", "rotate-tokens", "--tunnel", "main", "--yes")
	require.Len(t, doc["steps"], 1)

	e.stub.SecurityRotateCAFn = func(_ context.Context, progress func(api.Step)) (api.RotateCAResult, error) {
		return api.RotateCAResult{Reissued: []string{"de-1"}, Offline: []string{"nl-1"}}, nil
	}
	require.Contains(t, e.fail(3, "security", "rotate-ca"), "must join again")
	out = e.ok("security", "rotate-ca", "--yes")
	require.Contains(t, out, "CA rotated. Re-issued: de-1.")
	require.Contains(t, out, "Offline, they must join again: nl-1")
	doc = e.json("security", "rotate-ca", "--yes")
	require.Equal(t, []any{"de-1"}, doc["reissued"])

	var tlsTunnel string
	certs := []api.CertInfo{
		{Kind: "ca", Subject: "deyroute CA", NotAfter: testNow.Add(3650 * 24 * time.Hour), DaysLeft: 3650, Fingerprint: "sha256:aa"},
		{Kind: "tunnel", Tunnel: "main", Mode: "auto", Subject: "main", NotAfter: testNow.Add(10 * 24 * time.Hour), DaysLeft: 10, Fingerprint: "sha256:bb", Warning: "expires in 10 days"},
	}
	e.stub.SecurityTLSShowFn = func(_ context.Context, tunnel string) ([]api.CertInfo, error) { tlsTunnel = tunnel; return certs, nil }
	e.stub.SecurityTLSRenewFn = func(_ context.Context, tunnel string) ([]api.CertInfo, error) {
		tlsTunnel = "renew:" + tunnel
		return certs, nil
	}
	out = e.ok("security", "tls", "show")
	for _, want := range []string{"KIND", "FINGERPRINT", "deyroute CA", "3650", "sha256:bb", "10 !", "! tunnel main: expires in 10 days"} {
		require.Contains(t, out, want)
	}
	require.Contains(t, e.ok("security", "tls", "renew", "--tunnel", "main"), "Certificates renewed:")
	require.Equal(t, "renew:main", tlsTunnel)
	doc = e.json("security", "tls", "show", "--tunnel", "main")
	require.Equal(t, "main", tlsTunnel)
	require.Len(t, doc["certificates"], 2)

	var fwAction string
	e.stub.SecurityFirewallFn = func(_ context.Context, action string) (api.FirewallInfo, error) {
		fwAction = action
		if action == "disable" {
			return api.FirewallInfo{Managed: false, Detected: []string{"nftables"}, Suggested: []string{"nft add rule ..."}}, nil
		}
		return api.FirewallInfo{Managed: true, Detected: []string{"nftables", "ufw"}, Ruleset: "table inet deyroute {\n}\n"}, nil
	}
	out = e.ok("security", "firewall", "show")
	require.Equal(t, "show", fwAction)
	require.Contains(t, out, "Firewall: managed by deyroute · detected: nftables, ufw")
	require.Contains(t, out, "table inet deyroute {\n}\n", "the ruleset keeps its lines")
	require.Contains(t, e.ok("security", "firewall", "apply"), "applied")
	require.Equal(t, "apply", fwAction)
	fwAction = ""
	require.Contains(t, e.fail(3, "security", "firewall", "disable"), "control port is no longer limited")
	require.Empty(t, fwAction, "no confirmation, nothing disabled")
	out = e.ok("security", "firewall", "disable", "--yes")
	require.Contains(t, out, "removed")
	require.Contains(t, out, "suggestions only")
	require.Contains(t, out, "nft add rule ...")
	doc = e.json("security", "firewall", "show")
	require.Equal(t, true, doc["managed"])

	e.stub.SecurityAuditFn = func(context.Context) (api.AuditReport, error) {
		return api.AuditReport{Items: []api.AuditItem{{Check: "ports", Severity: "ok", Message: "only tunnel ports"}, {Check: "perms", Severity: "error", Message: "bad"}, {Check: "certs", Severity: "warn", Message: "soon"}}}, nil
	}
	out = e.ok("security", "audit")
	for _, want := range []string{"✔ ports", "✖ perms", "! certs", "The audit found problems"} {
		require.Contains(t, out, want)
	}
	e.stub.SecurityAuditFn = func(context.Context) (api.AuditReport, error) { return api.AuditReport{Clean: true}, nil }
	require.Contains(t, e.ok("security", "audit"), "Audit clean.")
	doc = e.json("security", "audit")
	require.Equal(t, true, doc["clean"])
	require.Equal(t, []any{}, doc["items"])
}

func TestNotifyAndSettings(t *testing.T) {
	e := newEnv(t)
	var tf, chat string
	e.stub.NotifyTelegramSetFn = func(_ context.Context, tokenFile, chatID string, events []string) error {
		tf, chat = tokenFile, chatID
		require.Nil(t, events)
		return nil
	}
	require.Contains(t, e.ok("notify", "telegram", "set", "--token-file", "/etc/deyroute/secrets/telegram.token", "--chat-id", "123"), "chat 123")
	require.Equal(t, "/etc/deyroute/secrets/telegram.token", tf)
	require.Equal(t, "123", chat)
	require.Contains(t, e.fail(1, "notify", "telegram", "set", "--chat-id", "1"), "--token-file")
	require.Contains(t, e.fail(1, "notify", "telegram", "set", "--token-file", "/x"), "--chat-id")
	// A relative token file is resolved where it was typed: the daemon
	// reads it with its own working directory.
	e.ok("notify", "telegram", "set", "--token-file", "rel.token", "--chat-id", "1")
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(wd, "rel.token"), tf)
	e.stub.NotifyTelegramTestFn = func(context.Context) error { return nil }
	require.Contains(t, e.ok("notify", "telegram", "test"), "Test message sent.")
	e.stub.NotifyTelegramOffFn = func(context.Context) error { return nil }
	require.Contains(t, e.ok("notify", "telegram", "off"), "disabled")
	doc := e.json("notify", "telegram", "off")
	require.Equal(t, true, doc["ok"])
	e.stub.NotifyTelegramTestFn = func(context.Context) error { return deyerr.New(deyerr.X050, deyerr.Params{"reason": "blocked"}) }
	require.Contains(t, e.fail(2, "notify", "telegram", "test"), "DEY-X050")
	require.Contains(t, e.ok("notify"), "telegram")

	var req api.SettingsRequest
	e.stub.SettingsSetFn = func(_ context.Context, r api.SettingsRequest) error { req = r; return nil }
	require.Contains(t, e.ok("settings", "ui-mode", "Advanced"), "UI mode set to advanced.")
	require.Equal(t, api.SettingsRequest{UIMode: "advanced"}, req)
	doc = e.json("settings", "ui-mode", "simple")
	require.Equal(t, "simple", doc["ui_mode"])
	require.Contains(t, e.fail(1, "settings", "ui-mode", "expert"), "DEY-C013")
}

// The domain and the ACME options (section 10) are set from the CLI with
// SettingsSet; the Cloudflare token goes as a file path, never as a value.
func TestTLSDomainAndACME(t *testing.T) {
	e := newEnv(t)
	var req api.SettingsRequest
	calls := 0
	e.stub.SettingsSetFn = func(_ context.Context, r api.SettingsRequest) error { req, calls = r, calls+1; return nil }
	out := e.ok("security", "tls", "domain", "VPN.Example.com.")
	require.Equal(t, "vpn.example.com", *req.Domain)
	require.Nil(t, req.ACMEEmail)
	require.Contains(t, out, "Domain set to vpn.example.com.")
	require.Contains(t, out, "deyroute security tls renew")
	require.Contains(t, e.ok("security", "tls", "domain", "--clear"), "Domain removed.")
	require.Equal(t, "", *req.Domain)
	doc := e.json("security", "tls", "domain", "vpn.example.com")
	require.Equal(t, "vpn.example.com", doc["domain"])
	require.Equal(t, true, doc["ok"])
	n := calls
	require.Contains(t, e.fail(1, "security", "tls", "domain"), "--clear")
	require.Contains(t, e.fail(1, "security", "tls", "domain", "x.example.com", "--clear"), "--clear")
	require.Contains(t, e.fail(1, "security", "tls", "domain", " "), "--clear")
	require.Equal(t, n, calls, "usage errors call nothing")

	out = e.ok("security", "tls", "acme", "--email", "owner@example.com", "--cloudflare-token-file", "cf.token")
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, "owner@example.com", *req.ACMEEmail)
	require.Equal(t, filepath.Join(wd, "cf.token"), *req.CloudflareTokenFile)
	require.Nil(t, req.Domain)
	for _, want := range []string{"ACME e-mail set to owner@example.com.", "DNS-01 through Cloudflare is on", "/etc/deyroute/secrets/cloudflare.token", "deyroute security tls renew"} {
		require.Contains(t, out, want)
	}
	out = e.ok("security", "tls", "acme", "--cloudflare-token-file", "")
	require.Equal(t, "", *req.CloudflareTokenFile)
	require.Nil(t, req.ACMEEmail)
	require.Contains(t, out, "HTTP-01 on port 80")
	doc = e.json("security", "tls", "acme", "--email", "", "--cloudflare-token-file", "/root/cf.token")
	require.Equal(t, "", doc["email"])
	require.Equal(t, "/etc/deyroute/secrets/cloudflare.token", doc["cloudflare_token_file"])
	require.Equal(t, "/root/cf.token", *req.CloudflareTokenFile)
	n = calls
	require.Contains(t, e.fail(1, "security", "tls", "acme"), "nothing to change")
	require.Equal(t, n, calls)

	e.stub.SettingsSetFn = func(context.Context, api.SettingsRequest) error {
		return deyerr.New(deyerr.C013, deyerr.Params{"field": "hub.domain", "value": "", "allowed": "a domain"}).WithFix("deyroute tunnel edit main --tls-mode auto")
	}
	out = e.fail(1, "security", "tls", "domain", "--clear")
	require.Contains(t, out, "DEY-C013")
	require.Contains(t, out, "deyroute tunnel edit main --tls-mode auto")
}

func TestUpdateCommands(t *testing.T) {
	e := newEnv(t)
	info := api.UpdateInfo{Current: "1.0.0", Latest: "1.1.0", Available: true, Changelog: "## [1.1.0] - 2026-10-01\r\n\n### Fixed\n\n- faster failover\n"}
	e.stub.UpdateCheckFn = func(context.Context) (api.UpdateInfo, error) { return info, nil }
	out := e.ok("update", "--check")
	require.Contains(t, out, "deyroute 1.0.0 is installed; 1.1.0 is available.")
	// The CHANGELOG section keeps its line breaks.
	require.Contains(t, out, "## [1.1.0] - 2026-10-01\n\n### Fixed\n\n- faster failover\n")
	require.NotContains(t, out, "?")
	doc := e.json("update", "--check")
	require.Equal(t, true, doc["available"])

	var applied string
	e.stub.UpdateApplyFn = func(_ context.Context, v string, progress func(api.Step)) (api.UpdateInfo, error) {
		applied = v
		steps(progress, api.Step{Title: "download", Status: api.StepOK}, api.Step{Title: "restart", Status: api.StepOK})
		return api.UpdateInfo{Current: v, Previous: "1.0.0"}, nil
	}
	require.Contains(t, e.fail(3, "update"), "Update deyroute 1.0.0 -> 1.1.0")
	require.Empty(t, applied)
	e.tty("yes")
	out = e.ok("update")
	require.Equal(t, "1.1.0", applied)
	require.Contains(t, out, "Changes in 1.1.0:")
	require.Contains(t, out, "### Fixed\n\n- faster failover\n")
	require.Contains(t, out, "✔ restart")
	require.Contains(t, out, "Updated deyroute 1.0.0 -> 1.1.0.")
	e.g.IsTTY = false
	doc = e.json("update", "--version", "v1.2.0", "--yes")
	require.Equal(t, "1.2.0", applied)
	require.Len(t, doc["steps"], 2)

	info = api.UpdateInfo{Current: "1.1.0"}
	require.Contains(t, e.ok("update"), "deyroute 1.1.0 is up to date.")
	require.Contains(t, e.ok("update", "--check"), "up to date")
	doc = e.json("update")
	require.Equal(t, false, doc["available"])

	e.stub.UpdateRollbackFn = func(context.Context) (api.UpdateInfo, error) { return api.UpdateInfo{Current: "1.0.0"}, nil }
	require.Contains(t, e.fail(3, "update", "--rollback"), "previous deyroute binary")
	require.Contains(t, e.ok("update", "--rollback", "--yes"), "Rolled back to deyroute 1.0.0.")
	doc = e.json("update", "--rollback", "--yes")
	require.Equal(t, "1.0.0", doc["current"])
	require.Contains(t, e.fail(1, "update", "--check", "--rollback"), "only one")
	require.Contains(t, e.fail(1, "update", "--rollback", "--version", "1"), "only one")

	var bname string
	e.stub.UpdateBackendsFn = func(_ context.Context, name string, progress func(api.Step)) ([]api.BackendUpdate, error) {
		bname = name
		return []api.BackendUpdate{{Backend: "backhaul", From: "0.6.5", To: "0.6.6", Status: "updated"},
			{Backend: "rathole", From: "0.5.0", To: "0.5.1", Status: "rolled_back", Error: &api.ErrorDTO{Code: "DEY-S003", Message: "rolled back"}}}, nil
	}
	require.Contains(t, e.fail(3, "update", "backends"), "every backend")
	out = e.ok("update", "backends", "backhaul", "--yes")
	require.Equal(t, "backhaul", bname)
	for _, want := range []string{"BACKEND", "backhaul", "0.6.5", "0.6.6", "updated", "rolled_back", "DEY-S003 rolled back"} {
		require.Contains(t, out, want)
	}
	doc = e.json("update", "backends", "--yes")
	require.Equal(t, "", bname)
	require.Len(t, doc["backends"], 2)

	e.stub.UpdateManifestFn = func(context.Context) (api.ManifestInfo, error) {
		return api.ManifestInfo{Source: "/etc/deyroute/backends.yaml", Versions: map[string]string{"xray": "v26.3.27", "backhaul": "v0.6.6"}}, nil
	}
	out = e.ok("update", "manifest")
	require.Contains(t, out, "Backend manifest updated (/etc/deyroute/backends.yaml):")
	require.Less(t, strings.Index(out, "backhaul"), strings.Index(out, "xray"))
	doc = e.json("update", "manifest")
	require.Equal(t, "/etc/deyroute/backends.yaml", doc["source"])

	e.down()
	require.Contains(t, e.fail(2, "update", "--check"), "DEY-X003")
	// With the daemon down (a bad update that crash-loops) the rollback
	// runs here: swap the binaries, restart the service.
	swapped := 0
	e.g.Ops.SelfRollback = func(root string) error {
		require.Equal(t, e.root, root)
		swapped++
		return nil
	}
	e.down()
	e.fake.OnPrefix("systemctl restart ", exec.OK(""))
	rb := e.ok("update", "--rollback", "--yes")
	require.Equal(t, 1, swapped)
	require.True(t, e.fake.Called("systemctl restart deyroute-hub.service"))
	require.Contains(t, rb, "Rolled back to the previous deyroute binary and restarted deyroute-hub.service.")
	doc = e.json("update", "--rollback", "--yes")
	require.Equal(t, false, doc["daemon_running"])
	e.g.Ops.SelfRollback = func(string) error {
		return deyerr.New(deyerr.S003, deyerr.Params{"component": "deyroute", "reason": "no deyroute.prev"})
	}
	require.Contains(t, e.fail(1, "update", "--rollback", "--yes"), "DEY-S003")
}

// optimize status --details lists every key of each group in a table under
// the group's summary and explanation.
func TestOptimizeStatusDetails(t *testing.T) {
	e := newEnv(t)
	e.stub.OptimizeStatusFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "auto", BBRAvailable: true, BBRActive: true,
			Applied: map[string]string{"net.core.somaxconn": "65535", "net.core.rmem_max": "33554432"},
			Facts:   &api.TuneFacts{MemBytes: 1 << 30, CPUs: 1, ConntrackLoaded: true, ConntrackMax: 65536, ConntrackCount: 60000}}, nil
	}
	out := e.ok("optimize", "status", "--details")
	for _, want := range []string{"│ KEY", "│ VALUE", "│ rmem_max", "32 MiB", "│ somaxconn", "65535", "│ CPU         │ 1 core",
		"60,000 of 65,536 tracked (92%)", "█████████░"} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "--details\n", "no hint when the details are shown")
}
