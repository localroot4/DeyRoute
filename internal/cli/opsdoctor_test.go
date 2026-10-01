package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/hub"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tui"
)

// A wrong command line is DEY-C025 in the three-line format (section 13),
// exit 1, with the --help of the command it was meant for.
func TestUsageErrorsAreC025(t *testing.T) {
	e := newEnv(t)
	errOut := e.fail(1, "optimize", "apply")
	lines := strings.Split(strings.TrimRight(errOut, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 4, errOut)
	require.Equal(t, "✖ DEY-C025  Invalid command line: missing required flag --profile", lines[0])
	require.True(t, strings.HasPrefix(lines[1], "  Why:  deyroute optimize apply does not accept it"), errOut)
	require.Equal(t, "  Fix:  see the usage and the examples: deyroute optimize apply --help", lines[2])
	require.Equal(t, "  Log:  /var/log/deyroute/deyroute.log (search DEY-C025)", lines[3])

	errOut = e.fail(1, "frobnicate")
	require.Contains(t, errOut, `✖ DEY-C025  Invalid command line: unknown command "frobnicate" for "deyroute"`)
	require.Contains(t, errOut, "Fix:  see the usage and the examples: deyroute --help")
	// An unknown flag stops before the command runs: still its own --help.
	errOut = e.fail(1, "status", "--bogus")
	require.Contains(t, errOut, "DEY-C025  Invalid command line: unknown flag: --bogus")
	require.Contains(t, errOut, "deyroute status --help")
	// cobra's suggestions become the detail.
	ue := usageDEY(errors.New("unknown command \"statu\" for \"deyroute\"\n\nDid you mean this?\n\tstatus\n"), "")
	require.Equal(t, `Invalid command line: unknown command "statu" for "deyroute"`, ue.Message())
	require.Equal(t, "Did you mean this?\nstatus", ue.Detail)
	require.Contains(t, e.fail(1, "tunnel", "add", "a", "b", "--node", "de-1"), "deyroute tunnel add --help")

	doc := map[string]any{}
	e.fail(1, "frobnicate", "--json")
	require.NoError(t, json.Unmarshal(e.out.Bytes(), &doc))
	require.Equal(t, float64(1), doc["exit_code"])
	require.Equal(t, "DEY-C025", doc["error"].(map[string]any)["code"])
}

// The doctor summary is colored only on a terminal: piped into a file or
// another program it is plain text (section 13).
func TestDoctorSummaryNoANSIWhenPiped(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	e.g.Caps = func() tui.Caps { return tui.Caps{Unicode: true, Color: true, Width: 120} }
	e.stub.DoctorCollectFn = func(context.Context, string) (api.DoctorData, error) {
		return api.DoctorData{Role: "hub", Sections: map[string]string{"os": "Ubuntu 24.04"},
			Findings: []api.DoctorFinding{{Rule: "R12", Severity: "warn", Message: "Low disk space on /: 5.0% free", Fix: "free space"}}}, nil
	}
	out := e.ok("doctor", "--out", filepath.Join(e.root, "d1.tar.gz"))
	require.Contains(t, out, "R12")
	require.NotContains(t, out, "\x1b[")

	e.g.OutTTY = true
	out = e.ok("doctor", "--out", filepath.Join(e.root, "d2.tar.gz"))
	require.Contains(t, out, "\x1b[")
}

// The panic record is one JSON line of the log format (ts, level,
// component, code, msg, err) with the stack, through the secret filter;
// component names the daemon for `deyroute daemon hub|node`.
func TestPanicRecord(t *testing.T) {
	e := newEnv(t)
	logDir := filepath.Join(e.root, "var/log/deyroute")
	require.NoError(t, os.MkdirAll(logDir, 0o700))
	secret := "panic-secret-value-0123456789"
	dlog.RegisterSecret(secret)
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { panic("kaboom " + secret) }
	e.g.RunHub = func(context.Context, hub.Options) error { panic("hub kaboom") }

	require.NotContains(t, e.fail(2, "tunnel", "list"), "kaboom")
	require.NotContains(t, e.fail(2, "daemon", "hub"), "kaboom")
	data, err := os.ReadFile(filepath.Join(logDir, "deyroute.log"))
	require.NoError(t, err)
	require.NotContains(t, string(data), secret)
	var recs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &r), l)
		if r["msg"] == "panic" {
			recs = append(recs, r)
		}
	}
	require.Len(t, recs, 2)
	for i, want := range []struct{ component, err string }{{"cli", "kaboom ***"}, {"hub", "hub kaboom"}} {
		r := recs[i]
		require.Equal(t, want.component, r["component"])
		require.Equal(t, "error", r["level"])
		require.Equal(t, "DEY-X000", r["code"])
		require.Equal(t, want.err, r["err"])
		require.Contains(t, r["stack"], "runtime/debug.Stack")
		ts, err := time.Parse(time.RFC3339, r["ts"].(string))
		require.NoError(t, err)
		require.Equal(t, time.UTC, ts.Location())
	}
}

// Stage 3 of `port check` failing prints DEY-P014 in the three-line format.
func TestPortCheckPrintsP014(t *testing.T) {
	e := newEnv(t)
	no := false
	e.stub.PortCheckFn = func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, FirewallOpen: true, FirewallName: "nftables",
			Node: "de-1", NodeReachable: &no, NodeError: &api.ErrorDTO{Code: "DEY-P014",
				Message: "Port 2053/tcp is not reachable from node de-1", Why: "the node could not connect",
				Fix: "open the port in your provider's firewall panel", Log: "/var/log/deyroute/hub.log", Detail: "i/o timeout"}}, nil
	}
	out := e.ok("port", "check", "2053")
	require.Contains(t, out, "✖ DEY-P014  Port 2053/tcp is not reachable from node de-1\n"+
		"  Why:  the node could not connect\n"+
		"  Fix:  open the port in your provider's firewall panel\n"+
		"  Log:  /var/log/deyroute/hub.log (search DEY-P014)\n"+
		"  | i/o timeout\n")
	doc := e.json("port", "check", "2053")
	require.Equal(t, "DEY-P014", doc["node_error"].(map[string]any)["code"])
}
