package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/state"
)

// startHubWithT5Backup runs the real hub with nodes de-1 and nl-1 (never
// connected) and tunnel t5 on de-1, backup nl-1.
func startHubWithT5Backup(t *testing.T, e *env) {
	t.Helper()
	startHubWithT5(t, e, e2ePort(t), func(c *config.Config) {
		c.Nodes = append(c.Nodes, config.Node{ID: "nl-1", Name: "nl-1", PublicIP: "203.0.113.11", CertFingerprint: config.SpecPlaceholderFingerprint})
		c.Tunnels[0].Nodes = []string{"de-1", "nl-1"}
	})
}

// tunnelNodes reads the node list of tunnel id from the hub's config.yaml.
func (e *env) tunnelNodes(id string) []string {
	e.t.Helper()
	tn, ok := e.hubConfig().Tunnel(id)
	require.True(e.t, ok)
	return tn.Nodes
}

func TestTunnelEditNodesReorders(t *testing.T) {
	e := newEnv(t)
	var req api.TunnelEditRequest
	e.stub.TunnelShowFn = func(context.Context, string) (api.TunnelDetail, error) {
		return api.TunnelDetail{TunnelInfo: api.TunnelInfo{ID: "t5", State: state.StateUp, ActiveNode: "de-1", Nodes: []string{"de-1", "nl-1"}}}, nil
	}
	e.stub.TunnelEditFn = func(_ context.Context, id string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		req = r
		// The hub keeps the active node; the new primary comes first.
		return api.TunnelInfo{ID: id, State: state.StateUp, ActiveNode: "de-1", Nodes: r.Nodes}, nil
	}
	doc := e.json("tunnel", "edit", "t5", "--node", "nl-1", "--node", "de-1")
	require.Equal(t, []string{"nl-1", "de-1"}, req.Nodes)
	require.Equal(t, []any{"nl-1", "de-1"}, doc["tunnel"].(map[string]any)["nodes"])
	require.Equal(t, "de-1", doc["tunnel"].(map[string]any)["active_node"])
	require.Nil(t, req.Name)
	require.Nil(t, req.Policy)
	// Commas work like a repeated flag; spaces are trimmed.
	e.ok("tunnel", "edit", "t5", "--node", " de-1 , nl-1", "--yes")
	require.Equal(t, []string{"de-1", "nl-1"}, req.Nodes)
	require.Contains(t, e.fail(1, "tunnel", "edit", "t5"), "--node")
}

func TestTunnelEditNodesRejectsUnknown(t *testing.T) {
	e := newEnv(t)
	startHubWithT5Backup(t, e)
	for _, c := range []struct {
		code string
		args []string
	}{
		{"DEY-C010", []string{"--node", "nl-1", "--node", "fr-9"}},
		{"DEY-C002", []string{"--node", "nl-1", "--node", "nl-1"}},
		{"DEY-C007", []string{"--node", "NL 1"}},
		{"DEY-C008", []string{"--node", " , "}},
	} {
		out := e.fail(1, append([]string{"tunnel", "edit", "t5", "--yes"}, c.args...)...)
		require.Contains(t, out, c.code, c.args)
	}
	require.Equal(t, []string{"de-1", "nl-1"}, e.tunnelNodes("t5"))
}

func TestTunnelEditNodesRemovingActiveNeedsYes(t *testing.T) {
	e := newEnv(t)
	var req api.TunnelEditRequest
	edits, shows := 0, 0
	e.stub.TunnelShowFn = func(context.Context, string) (api.TunnelDetail, error) {
		shows++
		return api.TunnelDetail{TunnelInfo: api.TunnelInfo{ID: "t5", State: state.StateUp, ActiveNode: "de-1", Nodes: []string{"de-1", "nl-1"}}}, nil
	}
	e.stub.TunnelEditFn = func(_ context.Context, id string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		req, edits = r, edits+1
		return api.TunnelInfo{ID: id, State: state.StateUp, ActiveNode: "nl-1", Nodes: r.Nodes}, nil
	}

	// The active node stays in the list: no question, even without --yes.
	e.ok("tunnel", "edit", "t5", "--node", "nl-1", "--node", "de-1")
	require.Equal(t, []string{"nl-1", "de-1"}, req.Nodes)
	require.Equal(t, 1, edits)

	// It leaves the list: without a terminal and without --yes, exit 3 and
	// no edit (also with --json).
	out := e.fail(3, "tunnel", "edit", "t5", "--node", "nl-1")
	require.Contains(t, out, "Node de-1 carries tunnel t5 now")
	require.Contains(t, out, "moves to nl-1")
	e.fail(3, "--json", "tunnel", "edit", "t5", "--node", "nl-1")
	require.Equal(t, 1, edits)

	// A person types yes, or anything else (aborted, exit 1).
	e.tty("no")
	require.Contains(t, e.fail(1, "tunnel", "edit", "t5", "--node", "nl-1"), "Aborted.")
	require.Equal(t, 1, edits)
	e.tty("yes")
	e.ok("tunnel", "edit", "t5", "--node", "nl-1")
	require.Equal(t, 2, edits)
	e.g.IsTTY = false

	// --yes: no question and no lookup.
	shows = 0
	doc := e.json("tunnel", "edit", "t5", "--node", "nl-1", "--yes")
	require.Equal(t, []any{"nl-1"}, doc["tunnel"].(map[string]any)["nodes"])
	require.Equal(t, []string{"nl-1"}, req.Nodes)
	require.Zero(t, shows)
	require.Equal(t, 3, edits)

	// Without --node nothing is looked up and the list is unchanged.
	e.ok("tunnel", "edit", "t5", "--policy", "node_only")
	require.Nil(t, req.Nodes)
	require.Zero(t, shows)
}
