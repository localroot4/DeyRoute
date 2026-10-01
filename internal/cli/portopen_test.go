package cli

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// closedByUFW is a port check whose firewall stage is closed by ufw.
func closedByUFW(r api.PortCheckRequest) api.PortCheckResult {
	return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true,
		FirewallName: "ufw", FirewallCommand: "ufw allow 443/tcp", SuggestedPorts: []int{2053}}
}

// `port check --open` names the exact command, asks (typed yes), and sends
// the confirmed command to the daemon; --yes asks nothing; without a
// terminal and --yes it stops with exit 3 and runs nothing.
func TestPortCheckOpenFirewall(t *testing.T) {
	e := newEnv(t)
	e.stub.PortCheckFn = func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		return closedByUFW(r), nil
	}
	var reqs []api.PortOpenRequest
	e.stub.PortOpenFirewallFn = func(_ context.Context, req api.PortOpenRequest) (api.PortOpenResult, error) {
		reqs = append(reqs, req)
		return api.PortOpenResult{Port: req.Port, Proto: req.Proto, Ran: req.Command, By: "ufw", FirewallOpen: true, FirewallName: "nftables, ufw"}, nil
	}

	// Without --open: the command and how to let deyroute run it.
	out := e.ok("port", "check", "443")
	require.Contains(t, out, "closed (ufw); open it with: ufw allow 443/tcp")
	require.Contains(t, out, "Let deyroute run it after a confirmation: deyroute port check 443/tcp --open")
	require.Empty(t, reqs)

	// No terminal, no --yes: exit 3, the command is named, nothing runs.
	errOut := e.fail(3, "port", "check", "443", "--open")
	require.Contains(t, errOut, "To open 443/tcp, deyroute runs this command on this server (ufw):\n  ufw allow 443/tcp")
	require.Empty(t, reqs)

	// A terminal: anything but "yes" aborts.
	e.tty("y")
	e.fail(1, "port", "check", "443", "--open")
	require.Empty(t, reqs)
	e.tty("yes")
	out = e.ok("port", "check", "443", "--open")
	require.Contains(t, out, "1. local bind")
	require.NotContains(t, out, "Let deyroute run it")
	require.Contains(t, out, "Type yes to continue: ")
	require.Contains(t, out, "Ran: ufw allow 443/tcp")
	require.Contains(t, out, "Port 443/tcp is open in the firewall now (nftables, ufw).")
	require.Equal(t, []api.PortOpenRequest{{Port: 443, Proto: "tcp", Command: "ufw allow 443/tcp"}}, reqs)

	// --yes asks nothing; --json carries the check and what ran.
	e.g.IsTTY = false
	doc := e.json("port", "check", "443", "--open", "--yes")
	require.Equal(t, false, doc["firewall_open"])
	opened, ok := doc["opened"].(map[string]any)
	require.True(t, ok, doc)
	require.Equal(t, "ufw allow 443/tcp", opened["ran"])
	require.Equal(t, true, opened["firewall_open"])
	require.Len(t, reqs, 2)

	// Nothing blocks: nothing to open, no daemon call.
	e.stub.PortCheckFn = func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, FirewallOpen: true, FirewallName: "nftables"}, nil
	}
	require.Contains(t, e.ok("port", "check", "8443", "--open"), "No external firewall blocks 8443/tcp; nothing to open.")
	doc = e.json("port", "check", "8443", "--open")
	_, has := doc["opened"]
	require.False(t, has)
	require.Len(t, reqs, 2)
}

// A second firewall that still blocks is confirmed separately; a refusal
// of the daemon (the firewall changed) is the command's error.
func TestPortCheckOpenFirewallTwoFirewalls(t *testing.T) {
	e := newEnv(t)
	e.stub.PortCheckFn = func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
		return closedByUFW(r), nil
	}
	const nftCmd = "nft insert rule inet filter input tcp dport 443 accept"
	var cmds []string
	e.stub.PortOpenFirewallFn = func(_ context.Context, req api.PortOpenRequest) (api.PortOpenResult, error) {
		cmds = append(cmds, req.Command)
		if req.Command == nftCmd {
			return api.PortOpenResult{Port: 443, Proto: "tcp", Ran: nftCmd, By: "nftables", FirewallOpen: true, FirewallName: "nftables, ufw"}, nil
		}
		return api.PortOpenResult{Port: 443, Proto: "tcp", Ran: req.Command, By: "ufw", FirewallName: "nftables", FirewallCommand: nftCmd}, nil
	}
	e.tty("yes", "yes")
	out := e.ok("port", "check", "443", "--open")
	require.Contains(t, out, "Port 443/tcp is still closed by nftables.")
	require.Contains(t, out, "deyroute runs this command on this server (nftables):\n  "+nftCmd)
	require.Contains(t, out, "Port 443/tcp is open in the firewall now (nftables, ufw).")
	require.Equal(t, []string{"ufw allow 443/tcp", nftCmd}, cmds)

	// The owner declines the second command: the first one stays done.
	cmds = nil
	e.tty("yes", "no")
	e.fail(1, "port", "check", "443", "--open")
	require.Equal(t, []string{"ufw allow 443/tcp"}, cmds)

	// Still closed after every round: DEY-P013 with the last command.
	e.stub.PortOpenFirewallFn = func(_ context.Context, req api.PortOpenRequest) (api.PortOpenResult, error) {
		return api.PortOpenResult{Port: 443, Proto: "tcp", Ran: req.Command, By: "ufw", FirewallName: "ufw", FirewallCommand: "ufw allow 443/tcp"}, nil
	}
	require.Contains(t, e.fail(1, "port", "check", "443", "--open", "--yes"), "DEY-P013")

	e.stub.PortOpenFirewallFn = func(context.Context, api.PortOpenRequest) (api.PortOpenResult, error) {
		return api.PortOpenResult{}, deyerr.New(deyerr.P032, deyerr.Params{"port": "443/tcp", "firewall": "firewalld", "command": "firewall-cmd --permanent --add-port=443/tcp && firewall-cmd --reload"})
	}
	errOut := e.fail(1, "port", "check", "443", "--open", "--yes")
	require.Contains(t, errOut, "DEY-P032")
	require.Contains(t, errOut, "deyroute port check 443/tcp --open")
}
