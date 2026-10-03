package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/setup"
)

const (
	frontCLISecret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"
	frontCLIToken  = "TOKENTOKENTOKENTOKEN"
)

func frontCLILink() string {
	return "dey://" + frontCLIToken + "@front.example.com:2053/" + frontCLISecret + "#sha256:" + strings.Repeat("ab", 32)
}

// The join CLI accepts a front link and never prints the secret or the
// token; the hub is shown as domain:port with a "via front" marker.
func TestJoinFrontLink(t *testing.T) {
	e := newEnv(t)
	var jo setup.JoinOptions
	e.g.Ops.Join = func(_ context.Context, o setup.JoinOptions) (*setup.JoinResult, error) {
		jo = o
		return &setup.JoinResult{NodeID: "de-1", HubName: "ir-1", HubAddr: "front.example.com:2053", Front: true, Compatible: true}, nil
	}
	e.g.IsTTY = false
	out := e.ok("join", frontCLILink(), "--name", "de-1")
	require.Equal(t, frontCLILink(), jo.Link)
	require.Contains(t, out, "Node de-1 joined hub ir-1 (via front)")
	require.Contains(t, out, "Next, on the hub (ir-1 (via front))")
	require.NotContains(t, out, frontCLISecret)
	require.NotContains(t, out, frontCLIToken)

	doc := e.json("join", frontCLILink())
	require.Equal(t, true, doc["front"])
	require.Equal(t, "front.example.com:2053", doc["hub_addr"])
	require.NotContains(t, e.out.String(), frontCLISecret)

	// Without a hub name the address is shown, still with the marker.
	e.g.Ops.Join = func(context.Context, setup.JoinOptions) (*setup.JoinResult, error) {
		return &setup.JoinResult{NodeID: "de-1", HubAddr: "front.example.com:2053", Front: true, Compatible: true}, nil
	}
	require.Contains(t, e.ok("join", frontCLILink()), "joined hub front.example.com:2053 (via front)")

	// A direct join is as before: no marker, "front": false.
	e.g.Ops.Join = func(context.Context, setup.JoinOptions) (*setup.JoinResult, error) {
		return &setup.JoinResult{NodeID: "de-1", HubAddr: "5.6.7.8:44433", Compatible: true}, nil
	}
	out = e.ok("join", "dey://"+frontCLIToken+"@5.6.7.8:44433#sha256:"+strings.Repeat("ab", 32))
	require.Contains(t, out, "joined hub 5.6.7.8:44433")
	require.NotContains(t, out, "via front")
	require.Equal(t, false, e.json("join", "dey://"+frontCLIToken+"@5.6.7.8:44433#sha256:"+strings.Repeat("ab", 32))["front"])
}

// The wizard shows only domain:port in its summary.
func TestSetupNodeWithFrontLinkPrintsNoSecret(t *testing.T) {
	e := newEnv(t)
	e.g.Ops.Join = func(context.Context, setup.JoinOptions) (*setup.JoinResult, error) {
		return &setup.JoinResult{NodeID: "de-1", HubName: "ir-1", HubAddr: "front.example.com:2053", Front: true, Compatible: true}, nil
	}
	e.tty("2", frontCLILink(), "", "n")
	out := e.ok("setup")
	require.Contains(t, out, "front.example.com:2053")
	require.NotContains(t, out, frontCLISecret)
	require.NotContains(t, out, frontCLIToken)
}

func TestNodeSetHubFrontTarget(t *testing.T) {
	e := newEnv(t)
	target := "wss://front.example.com:2053/" + frontCLISecret
	got := ""
	e.stub.NodeSetHubFn = func(_ context.Context, addr string) error { got = addr; return nil }
	out := e.ok("node", "set-hub", target)
	require.Equal(t, target, got, "the daemon gets the full target through its socket")
	require.Contains(t, out, "now connects to hub front.example.com:2053 (via front)")
	require.NotContains(t, out, frontCLISecret)
	doc := e.json("node", "set-hub", target)
	require.Equal(t, true, doc["front"])
	require.Equal(t, "front.example.com:2053", doc["hub_addr"])
	require.NotContains(t, e.out.String(), frontCLISecret)

	// A plain host:port is the explicit way back to a direct connection.
	out = e.ok("node", "set-hub", "5.6.7.9:44433")
	require.Equal(t, "5.6.7.9:44433", got)
	require.NotContains(t, out, "via front")
	require.Equal(t, false, e.json("node", "set-hub", "5.6.7.9:44433")["front"])

	// Bad targets are refused before the daemon is asked, without the secret.
	got = ""
	for _, bad := range []string{
		"wss://front.example.com/" + frontCLISecret,
		"wss://front.example.com:2053/" + frontCLISecret + "/x",
		"https://front.example.com:2053/" + frontCLISecret,
		"wss://front.example.com:2053/tooshort",
	} {
		msg := e.fail(1, "node", "set-hub", bad)
		require.Contains(t, msg, "DEY-C013", bad)
		require.NotContains(t, msg, frontCLISecret, bad)
	}
	require.Empty(t, got)

	// Daemon down on a node: the setup helper gets the target.
	e.writeConfig(nodeConfig)
	e.down()
	saved := ""
	e.g.Ops.SetHubAddr = func(_, addr string) error { saved = addr; return nil }
	out = e.ok("node", "set-hub", target)
	require.Equal(t, target, saved)
	require.Contains(t, out, "front.example.com:2053 (via front)")
	require.Contains(t, out, "systemctl start deyroute-node")
	require.NotContains(t, out, frontCLISecret)
}

func TestSetHubHelpDocumentsTheFrontForms(t *testing.T) {
	e := newEnv(t)
	out := e.ok("node", "set-hub", "--help")
	require.Contains(t, out, "wss://DOMAIN:PORT/SECRET")
	require.Contains(t, out, "CLEARS front mode")
	require.Contains(t, out, "deyroute node set-hub 5.6.7.9:44433")
	require.Contains(t, out, "deyroute node set-hub 'wss://front.example.com:2053/SECRET'")
}

func TestStatusShowsTheFrontMarker(t *testing.T) {
	e := newEnv(t)
	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Schema: 1, Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "front.example.com:2053", Front: true,
			Connected: true, LastContact: testNow}}, nil
	}
	out := e.ok("status")
	require.Contains(t, out, "Node: de-1 → hub front.example.com:2053 (via front)")
	require.Contains(t, out, "hub front.example.com:2053 (via front)")
	doc := e.json("status")
	self := doc["node"].(map[string]any)
	require.Equal(t, true, self["front"])
	require.Equal(t, "front.example.com:2053", self["hub_addr"])

	e.stub.StatusFn = func(context.Context) (api.Status, error) {
		return api.Status{Schema: 1, Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8:44433", Connected: true}}, nil
	}
	require.NotContains(t, e.ok("status"), "via front")
}
