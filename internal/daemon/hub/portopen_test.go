package hub

import (
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// ufw status verbose with a default deny and the given rule lines.
func ufwStatus(rules ...string) exec.Response {
	out := "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\nNew profiles: skip\n\n" +
		"To                         Action      From\n--                         ------      ----\n"
	for _, r := range rules {
		out += r + "\n"
	}
	return exec.OK(out)
}

const nftFilterDrop = "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n}\n"

// The confirmed command runs only when it is exactly the one the hub builds
// from a fresh check; the port is checked again afterwards.
func TestPortOpenFirewallUFW(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	env.runner.On("ufw status", exec.OK("Status: active\n"))
	env.runner.On("ufw status verbose", ufwStatus())

	res, err := env.client.PortCheck(ctx, api.PortCheckRequest{Port: 9443})
	require.NoError(t, err)
	require.False(t, res.FirewallOpen)
	require.Equal(t, "ufw", res.FirewallName)
	require.Equal(t, "ufw allow 9443/tcp", res.FirewallCommand)

	// Not the command the owner confirmed (or none): nothing runs.
	for _, cmd := range []string{"", "ufw allow 9444/tcp", "ufw allow 9443/tcp; reboot", "ufw disable"} {
		_, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Command: cmd})
		require.Equal(t, deyerr.P032, codeOf(err), cmd)
		e := deyerr.As(err)
		require.Contains(t, e.Why(), "ufw allow 9443/tcp")
		require.Contains(t, e.Fix(), "deyroute port check 9443/tcp --open")
	}
	require.Zero(t, env.runner.Count("ufw allow 9443/tcp"))
	require.Zero(t, env.runner.Count("ufw disable"))

	// The confirmed command: ufw opens the port, the re-check sees it.
	env.runner.On("ufw status verbose", ufwStatus(), ufwStatus("9443/tcp                   ALLOW IN    Anywhere"))
	env.runner.On("ufw allow 9443/tcp", exec.OK("Rule added\nRule added (v6)\n"))
	got, err := env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Proto: "tcp", Command: " ufw allow 9443/tcp "})
	require.NoError(t, err)
	require.Equal(t, 1, env.runner.Count("ufw allow 9443/tcp"))
	require.Equal(t, "ufw allow 9443/tcp", got.Ran)
	require.Equal(t, "ufw", got.By)
	require.True(t, got.FirewallOpen)
	require.Contains(t, got.FirewallName, "ufw")
	require.Empty(t, got.FirewallCommand)
	require.Equal(t, 9443, got.Port)
	require.Equal(t, "tcp", got.Proto)

	// Already open: nothing runs, the state is reported.
	got, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Command: "ufw allow 9443/tcp"})
	require.NoError(t, err)
	require.Empty(t, got.Ran)
	require.True(t, got.FirewallOpen)
	require.Equal(t, 1, env.runner.Count("ufw allow 9443/tcp"))

	// Invalid input.
	_, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 0, Command: "ufw allow 0/tcp"})
	require.Equal(t, deyerr.P010, codeOf(err))
	_, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Proto: "icmp"})
	require.Equal(t, deyerr.P010, codeOf(err))
}

// A UDP port in firewalld: both commands run in order.
func TestPortOpenFirewallFirewalld(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	env.runner.On("firewall-cmd --state", exec.OK("running\n"))
	env.runner.On("firewall-cmd --list-ports", exec.OK("443/tcp\n"), exec.OK("443/tcp 27015/udp\n"))
	env.runner.On("firewall-cmd --list-services", exec.OK("ssh\n"))
	env.runner.On("firewall-cmd --list-all", exec.OK("public (active)\n  target: default\n  services: ssh\n  ports: 443/tcp\n  protocols:\n  rich rules:\n"))
	const cmd = "firewall-cmd --permanent --add-port=27015/udp && firewall-cmd --reload"
	got, err := env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 27015, Proto: "udp", Command: cmd})
	require.NoError(t, err)
	require.Equal(t, cmd, got.Ran)
	require.Equal(t, "firewalld", got.By)
	require.True(t, got.FirewallOpen)
	lines := env.runner.Lines()
	add := slices.Index(lines, "firewall-cmd --permanent --add-port=27015/udp")
	reload := slices.Index(lines, "firewall-cmd --reload")
	require.GreaterOrEqual(t, add, 0)
	require.Greater(t, reload, add)
}

// A second firewall that still blocks comes back with its own command (a
// new confirmation); the same command still being needed is DEY-P033.
func TestPortOpenFirewallStillBlocked(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	env.runner.On("ufw status verbose", ufwStatus(), ufwStatus("9443/tcp                   ALLOW IN    Anywhere"))
	env.runner.On("nft list ruleset", exec.OK(nftFilterDrop))
	env.runner.On("ufw allow 9443/tcp", exec.OK("Rule added\n"))
	got, err := env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Command: "ufw allow 9443/tcp"})
	require.NoError(t, err)
	require.Equal(t, "ufw allow 9443/tcp", got.Ran)
	require.False(t, got.FirewallOpen)
	require.Equal(t, "nftables", got.FirewallName)
	const nftCmd = "nft insert rule inet filter input tcp dport 9443 accept"
	require.Equal(t, nftCmd, got.FirewallCommand)

	// The owner confirms the nftables command too.
	env.runner.On("nft list ruleset", exec.OK(nftFilterDrop), exec.OK(""))
	got, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Command: nftCmd})
	require.NoError(t, err)
	require.Equal(t, nftCmd, got.Ran)
	require.Equal(t, "nftables", got.By)
	require.True(t, got.FirewallOpen)
	require.True(t, env.runner.Called(nftCmd))

	// An earlier deny rule: ufw allow appends after it and changes nothing.
	deny := "9555/tcp                   DENY IN     Anywhere"
	env.runner.On("ufw status verbose", ufwStatus(deny), ufwStatus(deny, "9555/tcp                   ALLOW IN    Anywhere"))
	env.runner.On("ufw allow 9555/tcp", exec.OK("Rule added\n"))
	_, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9555, Command: "ufw allow 9555/tcp"})
	require.Equal(t, deyerr.P033, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), "still blocks the port")
	require.Equal(t, 1, env.runner.Count("ufw allow 9555/tcp"))
}

// A failing firewall tool is DEY-P033 with its output; a blocking nftables
// rule without a safe command runs nothing.
func TestPortOpenFirewallFailures(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	env.runner.On("ufw status verbose", ufwStatus())
	env.runner.On("ufw allow 9443/tcp", exec.Fail(1, "ERROR: Could not update running firewall"))
	_, err := env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Command: "ufw allow 9443/tcp"})
	require.Equal(t, deyerr.P033, codeOf(err))
	e := deyerr.As(err)
	require.Equal(t, "Could not open port 9443/tcp in ufw", e.Message())
	require.Contains(t, e.Why(), "ufw allow 9443/tcp failed")
	require.Contains(t, e.Detail, "Could not update running firewall")

	env.runner.On("ufw status verbose", exec.OK("Status: inactive\n"))
	env.runner.On("nft list ruleset", exec.OK("table inet 9odd {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n}\n"))
	res, err := env.client.PortCheck(ctx, api.PortCheckRequest{Port: 9443})
	require.NoError(t, err)
	require.False(t, res.FirewallOpen)
	require.Empty(t, res.FirewallCommand)
	_, err = env.client.PortOpenFirewall(ctx, api.PortOpenRequest{Port: 9443, Command: ""})
	require.Equal(t, deyerr.P033, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), "no safe command")
	for _, l := range env.runner.Lines() {
		require.NotContains(t, l, "nft insert")
	}
}

// Section 10 stage 2 on tunnel add / port add: a port another firewall on
// the hub blocks is a yellow step with DEY-P013 and the command; the tunnel
// is added anyway.
func TestTunnelAddReportsExternalFirewallBlocks(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	te.runner.On("ufw status", exec.OK("Status: active\n"))
	te.runner.On("ufw status verbose", ufwStatus())
	port := freePort(t)
	var log stepLog
	info, err := te.client.TunnelAdd(ctxT(t), api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)}, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Contains(t, log.finished(), "external_firewall:warn")
	var warn api.Step
	log.mu.Lock()
	for _, s := range log.steps {
		if s.ID == stepExtFirewall {
			warn = s
		}
	}
	log.mu.Unlock()
	require.NotNil(t, warn.Error)
	require.Equal(t, string(deyerr.P013), warn.Error.Code)
	require.Contains(t, warn.Detail, "ufw allow "+strconv.Itoa(port)+"/tcp")
	require.Zero(t, te.runner.Count("ufw allow "+strconv.Itoa(port)+"/tcp"), "nothing is opened without the owner")

	p2 := freePort(t)
	var log2 stepLog
	_, err = te.client.PortAdd(ctxT(t), info.ID, []api.PortSpec{{Listen: p2}}, log2.add)
	require.NoError(t, err)
	require.Contains(t, log2.finished(), "external_firewall:warn")
}
