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
	for _, want := range []string{"Kernel profile balanced applied.", "BBR: active", "net.core.default_qdisc = fq", "net.core.somaxconn = 65535", "! w1"} {
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
	require.Contains(t, out, "BBR: not available")
	e.stub.OptimizeRevertFn = func(context.Context) (api.OptimizeStatus, error) {
		return api.OptimizeStatus{Profile: "off", BBRAvailable: true}, nil
	}
	require.Contains(t, e.ok("optimize", "revert"), "BBR: available, not active")
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
	require.Contains(t, out, "table inet deyroute {")
	require.Contains(t, e.ok("security", "firewall", "apply"), "applied")
	require.Equal(t, "apply", fwAction)
	out = e.ok("security", "firewall", "disable")
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

func TestUpdateCommands(t *testing.T) {
	e := newEnv(t)
	info := api.UpdateInfo{Current: "1.0.0", Latest: "1.1.0", Available: true, Changelog: "- faster failover\n"}
	e.stub.UpdateCheckFn = func(context.Context) (api.UpdateInfo, error) { return info, nil }
	out := e.ok("update", "--check")
	require.Contains(t, out, "deyroute 1.0.0 is installed; 1.1.0 is available.")
	require.Contains(t, out, "- faster failover")
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
}
