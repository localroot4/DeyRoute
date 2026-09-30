package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestNodeJoinCommand(t *testing.T) {
	e := newEnv(t)
	var gotTTL time.Duration
	e.stub.NodeJoinCommandFn = func(_ context.Context, ttl time.Duration) (api.JoinCommand, error) {
		gotTTL = ttl
		return api.JoinCommand{Command: "bash <(curl -fsSL https://x/install.sh) join 'dey://T@5.6.7.8:44433#sha256:ab'",
			Link: "dey://T@5.6.7.8:44433#sha256:ab", ExpiresAt: testNow.Add(ttl)}, nil
	}
	out := e.ok("node", "join-command")
	require.Equal(t, DefaultJoinTTL, gotTTL)
	require.Contains(t, out, "Run this command on each node")
	require.Contains(t, out, "\nbash <(curl -fsSL https://x/install.sh) join 'dey://T@5.6.7.8:44433#sha256:ab'\n")
	require.Contains(t, out, ", 15m):")
	require.Equal(t, "1h", shortDuration(time.Hour))
	require.Equal(t, "1h30m", shortDuration(90*time.Minute))
	require.Equal(t, "0m", shortDuration(-time.Minute))

	doc := e.json("node", "join-command", "--ttl", "1h")
	require.Equal(t, time.Hour, gotTTL)
	require.Equal(t, "dey://T@5.6.7.8:44433#sha256:ab", doc["link"])

	require.Contains(t, e.fail(1, "node", "join-command", "--ttl", "0s"), "--ttl must be a positive duration")
	require.Contains(t, e.fail(1, "node", "join-command", "--ttl", "soon"), "invalid argument")
}

func TestNodeList(t *testing.T) {
	e := newEnv(t)
	e.stub.NodeListFn = func(context.Context) ([]api.NodeInfo, error) { return nil, nil }
	require.Contains(t, e.ok("node", "list"), "No nodes yet")
	doc := e.json("node", "list")
	require.Equal(t, []any{}, doc["nodes"])

	e.stub.NodeListFn = func(context.Context) ([]api.NodeInfo, error) {
		return []api.NodeInfo{
			{ID: "de-1", Name: "Germany 1", PublicIP: "1.2.3.4", Online: true, ControlRTTms: 39, Version: "1.0.0", Compatible: true, CPUPercent: 3.2, RAMBytes: 121 << 20, Tunnels: []string{"main"}},
			{ID: "nl-1", Online: false, Version: "0.9.0"},
		}, nil
	}
	out := e.ok("node", "list")
	for _, want := range []string{"ID", "PUBLIC IP", "CTL RTT", "de-1", "Germany 1", "● online", "39ms", "3%", "121MB", "main", "○ offline", "0.9.0 (incompatible)"} {
		require.Contains(t, out, want)
	}
	doc = e.json("node", "list")
	require.Len(t, doc["nodes"], 2)
}

func TestNodeRenameRemoveTest(t *testing.T) {
	e := newEnv(t)
	var renamed [2]string
	e.stub.NodeRenameFn = func(_ context.Context, id, name string) error { renamed = [2]string{id, name}; return nil }
	require.Contains(t, e.ok("node", "rename", "de-1", "Germany 1"), `Node de-1 is now called "Germany 1".`)
	require.Equal(t, [2]string{"de-1", "Germany 1"}, renamed)
	doc := e.json("node", "rename", "de-1", "X")
	require.Equal(t, true, doc["ok"])
	require.Contains(t, e.fail(1, "node", "rename", "de-1"), "needs 2 argument(s), got 1")
	require.Contains(t, e.fail(1, "node", "rename", "de-1", " "), "printable")

	removed := ""
	e.stub.NodeRemoveFn = func(_ context.Context, id string) error { removed = id; return nil }
	// Non-TTY without --yes: exit 3, nothing removed, the loss explained.
	errOut := e.fail(3, "node", "remove", "nl-1")
	require.Contains(t, errOut, "revokes its certificate")
	require.Contains(t, errOut, "needs confirmation")
	require.Empty(t, removed)
	// TTY: anything but "yes" aborts (exit 1).
	e.tty("y")
	require.Contains(t, e.fail(1, "node", "remove", "nl-1"), "Aborted.")
	require.Empty(t, removed)
	e.tty("yes")
	out := e.ok("node", "remove", "nl-1")
	require.Contains(t, out, "Type yes to continue")
	require.Contains(t, out, "Node nl-1 removed.")
	require.Equal(t, "nl-1", removed)
	e.g.IsTTY = false
	removed = ""
	doc = e.json("node", "remove", "nl-2", "--yes")
	require.Equal(t, "nl-2", doc["node"])
	require.Equal(t, "nl-2", removed)
	// JSON confirmation error document.
	e.fail(3, "node", "remove", "nl-3", "--json")
	require.Contains(t, e.out.String(), `"exit_code": 3`)

	e.stub.NodeTestFn = func(_ context.Context, id string) (api.NodeTestResult, error) {
		return api.NodeTestResult{Node: id, Online: true, ControlRTTms: 39, UDPOK: true, UDPRTTms: 41, SysInfo: map[string]string{"os": "Debian 12", "kernel": "6.1"}}, nil
	}
	out = e.ok("node", "test", "de-1")
	for _, want := range []string{"Node de-1: ● online", "control RTT  39ms", "UDP          ok (41ms)", "kernel:", "Debian 12"} {
		require.Contains(t, out, want)
	}
	e.stub.NodeTestFn = func(_ context.Context, id string) (api.NodeTestResult, error) {
		return api.NodeTestResult{Node: id}, nil
	}
	out = e.ok("node", "test", "de-1")
	require.Contains(t, out, "offline")
	require.Contains(t, out, "blocked")
	doc = e.json("node", "test", "de-1")
	require.Equal(t, "de-1", doc["node"])
	e.stub.NodeTestFn = func(context.Context, string) (api.NodeTestResult, error) {
		return api.NodeTestResult{}, deyerr.New(deyerr.N003, deyerr.Params{"node": "de-1"})
	}
	require.Contains(t, e.fail(1, "node", "test", "de-1"), "DEY-N003")
}

func TestNodeSetHubAndAnnounce(t *testing.T) {
	e := newEnv(t)
	got := ""
	e.stub.NodeSetHubFn = func(_ context.Context, addr string) error { got = addr; return nil }
	require.Contains(t, e.ok("node", "set-hub", "5.6.7.9:44433"), "now connects to hub 5.6.7.9:44433")
	require.Equal(t, "5.6.7.9:44433", got)
	require.Contains(t, e.fail(1, "node", "set-hub", "nope"), "DEY-C013")

	// Daemon down on a node: the address is written directly.
	e.writeConfig(nodeConfig)
	e.down()
	saved := ""
	e.g.Ops.SetHubAddr = func(root, addr string) error {
		require.Equal(t, e.root, root)
		saved = addr
		return nil
	}
	out := e.ok("node", "set-hub", "5.6.7.10:44433")
	require.Equal(t, "5.6.7.10:44433", saved)
	require.Contains(t, out, "systemctl start deyroute-node")
	doc := e.json("node", "set-hub", "5.6.7.10:44433")
	require.Equal(t, false, doc["daemon_running"])
	e.g.Ops.SetHubAddr = func(string, string) error { return deyerr.New(deyerr.C017, nil) }
	require.Contains(t, e.fail(1, "node", "set-hub", "5.6.7.10:44433"), "DEY-C017")

	e2 := newEnv(t)
	var newAddr string
	e2.stub.HubAnnounceMoveFn = func(_ context.Context, addr string) (api.AnnounceResult, error) {
		newAddr = addr
		return api.AnnounceResult{Accepted: []string{"de-1"}, Offline: []string{"nl-1"}}, nil
	}
	out = e2.ok("hub", "announce-move", "5.6.7.9:44433")
	require.Equal(t, "5.6.7.9:44433", newAddr)
	require.Contains(t, out, "sent to: de-1")
	require.Contains(t, out, "deyroute node set-hub 5.6.7.9:44433")
	doc = e2.json("hub", "announce-move", "5.6.7.9:44433")
	require.Equal(t, []any{"nl-1"}, doc["offline"])
	require.Contains(t, e2.fail(1, "hub", "announce-move", "x"), "DEY-C013")
	// A group command without a subcommand prints its help.
	require.Contains(t, e2.ok("hub"), "announce-move")
}
