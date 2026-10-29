package cli

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// TestNodeJoinRevokeJSON: `deyroute node join-revoke` (no flags) calls
// NodeJoinRevoke once and prints {"schema":1,"ok":true,"revoked":N}; the
// human form says how many were revoked (DR-16).
func TestNodeJoinRevokeJSON(t *testing.T) {
	e := newEnv(t)
	calls := 0
	e.stub.NodeJoinRevokeFn = func(context.Context) (int, error) {
		calls++
		return 2, nil
	}
	statsGolden(t, "node_join_revoke.json.golden", e.ok("node", "join-revoke", "--json"))
	require.Equal(t, 1, calls)

	out := e.ok("node", "join-revoke")
	require.Contains(t, out, "Join tokens revoked: 2.")
	require.Contains(t, out, "@nodes")

	e.stub.NodeJoinRevokeFn = func(context.Context) (int, error) { return 0, nil }
	require.Contains(t, e.ok("node", "join-revoke"), "No join token was live")
	doc := e.json("node", "join-revoke")
	require.InDelta(t, 0, doc["revoked"], 0)

	// No flags, no arguments: revoking all tokens is the only mode.
	require.Contains(t, e.fail(1, "node", "join-revoke", "--all"), "unknown flag")
	e.fail(1, "node", "join-revoke", "tok")

	e.stub.NodeJoinRevokeFn = func(context.Context) (int, error) {
		return 0, deyerr.New(deyerr.X000, nil)
	}
	require.Contains(t, e.fail(2, "node", "join-revoke"), "DEY-X000")
	e.down()
	require.Contains(t, e.fail(2, "node", "join-revoke"), "DEY-X003")
}

// TestSetupNoJoinJSON: setup --no-join passes NoJoinWindow to the setup
// (no join window in the setup-time firewall) and never asks the daemon for
// a join command; the JSON document has no join field and join_window
// false. Without the flag, join_window is true next to the join command
// (DR-16).
func TestSetupNoJoinJSON(t *testing.T) {
	e := newEnv(t)
	var fo setup.HubOptions
	e.fakeSetup(&fo)
	e.stub.NodeJoinCommandFn = func(context.Context, time.Duration) (api.JoinCommand, error) {
		t.Fatal("setup --no-join must not create a join command")
		return api.JoinCommand{}, nil
	}
	statsGolden(t, "setup_hub_no_join.json.golden",
		e.ok("setup", "--role", "hub", "--name", "ir-1", "--public-ip", "203.0.113.7", "--no-join", "--yes", "--json"))
	require.True(t, fo.NoJoinWindow)
	doc := e.json("setup", "--role", "hub", "--name", "ir-1", "--no-join", "--yes")
	require.NotContains(t, doc, "join")
	require.NotContains(t, doc, "join_error")
	require.Equal(t, false, doc["join_window"])

	// Human output: no join command, how to make one later.
	out := e.ok("setup", "--role", "hub", "--name", "ir-1", "--no-join", "--yes")
	require.NotContains(t, out, "bash <(curl")
	require.Contains(t, out, "deyroute node join-command")

	// Only for a hub.
	errOut := e.fail(1, "setup", "--role", "node", "--no-join")
	require.Contains(t, errOut, "DEY-C013")
	require.Contains(t, errOut, "--no-join")

	// Without --no-join: the window is open with the join command.
	e = newEnv(t)
	e.fakeSetup(&fo)
	doc = e.json("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.False(t, fo.NoJoinWindow)
	require.Equal(t, true, doc["join_window"])
	require.Contains(t, doc, "join")
	// A join command that cannot be made leaves no window open.
	e.stub.NodeJoinCommandFn = func(context.Context, time.Duration) (api.JoinCommand, error) {
		return api.JoinCommand{}, deyerr.New(deyerr.X003, deyerr.Params{"service": "deyroute-hub"})
	}
	doc = e.json("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Equal(t, false, doc["join_window"])
	require.Contains(t, doc, "join_error")
}
