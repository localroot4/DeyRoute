package cli

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/state"
)

// e2ePort returns a TCP port that is free on this machine now.
func e2ePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// e2eLadder is built in: a tunnel add installs no backend binary.
const e2eLadder = "direct/native"

// startHubWithT5 runs the real hub with node de-1 (never connected) and,
// when port is not 0, tunnel t5 on that port; more edit the config after.
func startHubWithT5(t *testing.T, e *env, port int, more ...func(*config.Config)) {
	t.Helper()
	startRealHub(t, e, append([]func(*config.Config){func(c *config.Config) {
		c.Nodes = append(c.Nodes, config.Node{ID: "de-1", Name: "de-1", PublicIP: "203.0.113.10", CertFingerprint: config.SpecPlaceholderFingerprint})
		if port != 0 {
			tn := config.NewTunnel("t5", "t5", []string{"de-1"},
				[]config.PortMap{{Listen: port, Proto: config.ProtoTCP, Target: "127.0.0.1:" + strconv.Itoa(port)}})
			tn.Ladder = config.LadderRef{Inline: []string{e2eLadder}}
			c.Tunnels = append(c.Tunnels, tn)
		}
	}}, more...)...)
}

// hubConfig loads the hub's config.yaml.
func (e *env) hubConfig() *config.Config {
	e.t.Helper()
	c, err := config.LoadWith(filepath.Join(e.root, config.DefaultPath), validateOptions())
	require.NoError(e.t, err)
	return c
}

// tunnelIDs reads the tunnel ids of the hub's config.yaml.
func (e *env) tunnelIDs() []string {
	e.t.Helper()
	ids := []string{}
	for _, tn := range e.hubConfig().Tunnels {
		ids = append(ids, tn.ID)
	}
	return ids
}

func TestTunnelAddIDFlag(t *testing.T) {
	e := newEnv(t)
	var req api.TunnelAddRequest
	calls := 0
	e.stub.TunnelAddFn = func(_ context.Context, r api.TunnelAddRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		req, calls = r, calls+1
		return api.TunnelInfo{ID: r.ID, State: state.StateUp, ActiveTransport: "backhaul/wssmux"}, nil
	}
	doc := e.json("tunnel", "add", "--id", " t5 ", "--name", "Panel 5", "--node", "de-1", "--ports", "443", "--yes")
	require.Equal(t, "t5", req.ID)
	require.Equal(t, "Panel 5", req.Name)
	require.Equal(t, "t5", doc["tunnel"].(map[string]any)["id"])

	// An invalid id is refused before the daemon is asked (DEY-C007).
	for _, bad := range []string{"T5", "t 5", "t", ""} {
		out := e.fail(1, "tunnel", "add", "--id", bad, "--node", "de-1", "--ports", "443", "--yes")
		require.Contains(t, out, "DEY-C007", bad)
	}
	require.Equal(t, 1, calls)
}

func TestTunnelAddIDTaken(t *testing.T) {
	e := newEnv(t)
	startHubWithT5(t, e, 0)
	p1, p2 := e2ePort(t), e2ePort(t)
	// Node de-1 is offline: the first add stops at "install on node"
	// (DEY-N003), but tunnel t5 is configured.
	args := []string{"tunnel", "add", "--id", "t5", "--node", "de-1", "--ports", strconv.Itoa(p1), "--ladder", e2eLadder, "--yes"}
	require.Contains(t, e.fail(1, args...), "DEY-N003")
	require.Equal(t, []string{"t5"}, e.tunnelIDs())
	// The same call again, and with other ports: DEY-C002, nothing new.
	out := e.fail(1, args...)
	require.Contains(t, out, "DEY-C002")
	require.NotContains(t, out, "DEY-C003")
	require.Contains(t, e.fail(1, "tunnel", "add", "--id", "t5", "--node", "de-1", "--ports", strconv.Itoa(p2), "--ladder", e2eLadder, "--yes"), "DEY-C002")
	require.Equal(t, []string{"t5"}, e.tunnelIDs())
}

func TestTunnelAddNameTakenTTYSuffixes(t *testing.T) {
	e := newEnv(t)
	startHubWithT5(t, e, e2ePort(t))
	// A person at a terminal confirms the summary: the daemon picks t5-2
	// as before (the add then stops at the offline node, DEY-N003).
	e.tty("")
	out := e.fail(1, "tunnel", "add", "--name", "t5", "--node", "de-1", "--ports", strconv.Itoa(e2ePort(t)), "--ladder", e2eLadder)
	require.NotContains(t, out, "DEY-C002")
	require.Equal(t, []string{"t5", "t5-2"}, e.tunnelIDs())
}

func TestTunnelAddNameTakenNonTTYFails(t *testing.T) {
	e := newEnv(t)
	startHubWithT5(t, e, e2ePort(t))
	// No terminal, other ports: DEY-C002 and no t5-2.
	out := e.fail(1, "tunnel", "add", "--name", "t5", "--node", "de-1", "--ports", strconv.Itoa(e2ePort(t)), "--ladder", e2eLadder)
	require.Contains(t, out, "DEY-C002")
	require.Contains(t, out, "--id")
	require.Equal(t, []string{"t5"}, e.tunnelIDs())
	// --yes on a terminal is non-interactive too.
	e.tty()
	require.Contains(t, e.fail(1, "tunnel", "add", "--name", "T5", "--node", "de-1", "--ports", strconv.Itoa(e2ePort(t)), "--ladder", e2eLadder, "--yes"), "DEY-C002")
	require.Equal(t, []string{"t5"}, e.tunnelIDs())
}

func TestTunnelAddRetrySamePortsGivesC002(t *testing.T) {
	e := newEnv(t)
	port := e2ePort(t)
	startHubWithT5(t, e, port)
	for _, args := range [][]string{
		{"tunnel", "add", "--name", "t5", "--node", "de-1", "--ports", strconv.Itoa(port), "--ladder", e2eLadder},
		{"tunnel", "add", "--id", "t5", "--node", "de-1", "--ports", strconv.Itoa(port), "--ladder", e2eLadder},
	} {
		out := e.fail(1, args...)
		require.Contains(t, out, "DEY-C002", args)
		require.NotContains(t, out, "DEY-C003", args)
	}
	require.Equal(t, []string{"t5"}, e.tunnelIDs())
}

func TestTunnelAddAllowRename(t *testing.T) {
	e := newEnv(t)
	startHubWithT5(t, e, e2ePort(t))
	// A script that wants the old behaviour asks for it: t5-2 (the add
	// then stops at the offline node, DEY-N003).
	out := e.fail(1, "tunnel", "add", "--name", "t5", "--allow-rename", "--node", "de-1", "--ports", strconv.Itoa(e2ePort(t)), "--ladder", e2eLadder, "--yes")
	require.NotContains(t, out, "DEY-C002")
	require.Equal(t, []string{"t5", "t5-2"}, e.tunnelIDs())

	// The request: no_rename only for a taken --name without a terminal
	// or with --yes, never with --allow-rename; without --name the daemon
	// numbers the default id as before.
	var req api.TunnelAddRequest
	e.g.Dial = func() (api.Local, error) { return e.stub, nil }
	e.stub.TunnelAddFn = func(_ context.Context, r api.TunnelAddRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		req = r
		return api.TunnelInfo{ID: "t5", State: state.StateUp, ActiveTransport: "backhaul/wssmux"}, nil
	}
	for _, c := range []struct {
		tty  bool
		args []string
		want bool
	}{
		{false, []string{"--name", "t5"}, true},
		{false, []string{"--name", "t5", "--allow-rename"}, false},
		{true, []string{"--name", "t5", "--yes"}, true},
		{true, []string{"--name", "t5", "--yes", "--allow-rename"}, false},
		{true, []string{"--name", "t5"}, false},
		{false, nil, false},
	} {
		e.g.IsTTY = false
		if c.tty {
			e.tty("")
		}
		e.ok(append([]string{"tunnel", "add", "--node", "de-1", "--ports", "443"}, c.args...)...)
		require.Equal(t, c.want, req.NoRename, c.args)
	}
}
