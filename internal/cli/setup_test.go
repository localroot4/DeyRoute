package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// fakeSetup records the hub options and reports the setup steps.
func (e *env) fakeSetup(got *setup.HubOptions) {
	e.g.Ops.SetupHub = func(_ context.Context, o setup.HubOptions) (*setup.HubResult, error) {
		*got = o
		steps(o.Progress,
			api.Step{ID: setup.StepDetectIP, Title: setup.StepTitle(setup.StepDetectIP), Status: api.StepOK, Detail: "5.6.7.8"},
			api.Step{ID: setup.StepCA, Title: setup.StepTitle(setup.StepCA), Status: api.StepOK},
			api.Step{ID: setup.StepService, Title: setup.StepTitle(setup.StepService), Status: api.StepOK},
		)
		ip := o.PublicIP
		if ip == "" {
			ip = "5.6.7.8"
		}
		port := o.ControlPort
		if port == 0 {
			port = 44433
		}
		profile := "off"
		if o.ApplySysctl {
			profile = o.SysctlProfile
		}
		return &setup.HubResult{PublicIP: ip, ControlPort: port, SysctlProfile: profile, SysctlWarnings: []string{"BBR missing"}, CAFingerprint: "sha256:ab"}, nil
	}
	e.stub.NodeJoinCommandFn = func(_ context.Context, ttl time.Duration) (api.JoinCommand, error) {
		require.Equal(e.t, time.Duration(0), ttl)
		return api.JoinCommand{Command: "bash <(curl -fsSL https://x/install.sh) join 'dey://T@5.6.7.8:44433#sha256:ab'", ExpiresAt: testNow.Add(15 * time.Minute)}, nil
	}
}

func TestSetupWizardHub(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.fakeSetup(&o)
	e.vars[EnvMirror] = "https://mirror.example"
	e.g.PortBusy = func(p int) bool { return p == 44433 || p == 5000 }
	// role (default hub), name (default), IP (edited after a bad answer),
	// port (busy answer, then default), sysctl (default yes).
	e.tty("", "", "not-an-ip", "5.6.7.9", "5000", "", "")
	out := e.ok("setup")
	require.Equal(t, "ir-server", o.Name)
	require.Equal(t, "5.6.7.9", o.PublicIP)
	require.Equal(t, 44434, o.ControlPort)
	require.True(t, o.ApplySysctl)
	require.Equal(t, "balanced", o.SysctlProfile)
	require.Equal(t, "https://mirror.example", o.Mirror)
	require.False(t, o.StartService)
	require.Equal(t, e.root, o.Root)
	for _, want := range []string{
		"Role of this server", "[hub]", "Name of this hub [ir-server]", "Public IP of this server [5.6.7.8]",
		"Not accepted: DEY-C013", "Control port for the nodes [44434]", "Not accepted: DEY-P012 Port 5000/tcp is already in use", "[Y/n]",
		"Setting up hub ir-server", "✔ " + setup.StepTitle(setup.StepCA), "Hub ir-server is ready: 5.6.7.9, control port 44434.",
		"! BBR missing", "Run this command on the new node (one node per command, valid until", "\nbash <(curl -fsSL https://x/install.sh) join 'dey://T@5.6.7.8:44433#sha256:ab'\n",
	} {
		require.Contains(t, out, want)
	}

	// Private detection: warned, no default; answers "2"/"node" mean node.
	e2 := newEnv(t)
	e2.fakeSetup(&o)
	e2.g.DetectIP = func(context.Context) (string, bool, error) { return "10.0.0.5", true, nil }
	e2.tty("hub", "My Hub", "10.0.0.5", "", "n")
	out = e2.ok("setup")
	require.Contains(t, out, "10.0.0.5 is not a public IP address")
	require.Equal(t, "My Hub", o.Name)
	require.Equal(t, "10.0.0.5", o.PublicIP)
	require.False(t, o.ApplySysctl)

	// Detection failure: the error is shown and the IP must be typed.
	e3 := newEnv(t)
	e3.fakeSetup(&o)
	e3.g.DetectIP = func(context.Context) (string, bool, error) { return "", false, deyerr.New(deyerr.I020, nil) }
	e3.tty("1", "ir-2", "", "5.6.7.10", "", "y")
	out = e3.ok("setup")
	require.Contains(t, out, "DEY-I020")
	require.Equal(t, "5.6.7.10", o.PublicIP)

	// Running out of input aborts.
	e4 := newEnv(t)
	e4.fakeSetup(&o)
	e4.tty("")
	require.Contains(t, e4.fail(1, "setup"), "Aborted.")
}

func TestSetupNonInteractive(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.fakeSetup(&o)
	out := e.ok("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Equal(t, "ir-1", o.Name)
	require.Empty(t, o.PublicIP)
	require.Zero(t, o.ControlPort)
	require.True(t, o.ApplySysctl)
	require.Contains(t, out, "Hub ir-1 is ready")
	require.NotContains(t, out, "Role of this server")

	// --yes alone: hub with the host name.
	e.ok("setup", "--yes")
	require.Equal(t, "ir-server", o.Name)
	// Flags without --yes on a non-TTY: no sysctl, and it says so.
	out = e.ok("setup", "--role", "hub", "--name", "ir-1", "--control-port", "44500")
	require.False(t, o.ApplySysctl)
	require.Equal(t, 44500, o.ControlPort)
	require.Contains(t, out, "Kernel profile not applied")
	// A TTY with flags asks only the rest.
	e.tty("5.6.7.8", "")
	e.ok("setup", "--role", "hub", "--name", "ir-1", "--control-port", "44500")
	require.True(t, o.ApplySysctl)
	e.g.IsTTY = false

	doc := e.json("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Equal(t, "hub", doc["role"])
	require.EqualValues(t, 44433, doc["control_port"])
	require.Len(t, doc["steps"], 3)
	require.Contains(t, doc, "join")

	// No daemon afterwards: setup still succeeds and says how to get the command.
	e.down()
	out = e.ok("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Contains(t, out, "DEY-X003")
	require.Contains(t, out, "deyroute node join-command")
	doc = e.json("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Contains(t, doc, "join_error")

	// Errors.
	require.Contains(t, e.fail(1, "setup", "--role", "moon"), "DEY-C013")
	require.Contains(t, e.fail(1, "setup"), "DEY-I014")
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--control-port", "22", "--yes"), "DEY-P011")
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--control-port", "70000", "--yes"), "DEY-P010")
	e.g.PortBusy = func(int) bool { return true }
	errOut := e.fail(1, "setup", "--role", "hub", "--control-port", "44433", "--yes")
	require.Contains(t, errOut, "DEY-P012  Port 44433/tcp is already in use")
	require.Contains(t, errOut, "another program is listening on TCP port 44433")
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--name", "\x01bad", "--yes"), "printable")
	require.Contains(t, e.fail(1, "setup", "--role", "node", "--yes"), "deyroute join")
	e.g.Ops.SetupHub = func(context.Context, setup.HubOptions) (*setup.HubResult, error) {
		return nil, deyerr.New(deyerr.I014, deyerr.Params{"step": "firewall"})
	}
	e.g.PortBusy = func(int) bool { return false }
	require.Contains(t, e.fail(1, "setup", "--yes"), "DEY-I014")

	// Already set up: I013 before any question.
	e.writeConfig(hubConfig)
	e.tty("hub")
	require.Contains(t, e.fail(1, "setup"), "DEY-I013")
	require.Contains(t, e.fail(1, "join", "dey://T@5.6.7.8:44433#sha256:ab"), "DEY-I013")
}

func TestSetupNodeAndJoin(t *testing.T) {
	e := newEnv(t)
	var jo setup.JoinOptions
	e.g.Ops.Join = func(_ context.Context, o setup.JoinOptions) (*setup.JoinResult, error) {
		jo = o
		steps(o.Progress, api.Step{Title: setup.StepTitle(setup.StepJoin), Status: api.StepOK})
		return &setup.JoinResult{NodeID: "de-1", HubName: "ir-1", HubAddr: "5.6.7.8:44433", HubVersion: "1.1.0", Compatible: false, SysctlWarnings: []string{"w"}}, nil
	}
	link := "dey://TOKENTOKENTOKENTOKEN@5.6.7.8:44433#sha256:" + strings.Repeat("ab", 32)
	// Wizard, node role: link (a bad one first), name, sysctl.
	e.tty("2", "dey://bad", link, "", "n")
	out := e.ok("setup")
	require.Equal(t, link, jo.Link)
	require.Equal(t, "ir-server", jo.Name)
	require.False(t, jo.ApplySysctl)
	require.Equal(t, "balanced", jo.SysctlProfile)
	require.Contains(t, out, "Not accepted: DEY-N006")
	require.Contains(t, out, "Node de-1 joined hub ir-1; it appears on the hub dashboard shortly")
	require.Contains(t, out, "The hub runs deyroute 1.1.0")
	require.Contains(t, out, "! w")

	// setup --role node --name on a TTY asks the link and the sysctl.
	e.tty(link, "")
	e.ok("setup", "--role", "node", "--name", "de-2")
	require.Equal(t, "de-2", jo.Name)
	require.True(t, jo.ApplySysctl)

	// join: non-TTY applies sysctl; a TTY asks.
	e.g.IsTTY = false
	e.ok("join", link, "--name", "de-3")
	require.Equal(t, "de-3", jo.Name)
	require.True(t, jo.ApplySysctl)
	require.False(t, jo.StartService)
	e.tty("n")
	e.ok("join", link)
	require.False(t, jo.ApplySysctl)
	require.Empty(t, jo.Name)
	e.tty()
	e.ok("join", link, "--yes")
	require.True(t, jo.ApplySysctl)
	e.g.IsTTY = false
	doc := e.json("join", link)
	require.Equal(t, "de-1", doc["node"])
	require.Equal(t, false, doc["compatible"])
	require.Contains(t, e.fail(1, "join"), "needs 1 argument")
	require.Contains(t, e.fail(1, "join", link, "--name", "\x7f"), "printable")

	e.g.Ops.Join = func(context.Context, setup.JoinOptions) (*setup.JoinResult, error) {
		return nil, deyerr.New(deyerr.N001, nil)
	}
	require.Contains(t, e.fail(1, "join", link), "DEY-N001")
	e.g.Ops.Join = func(context.Context, setup.JoinOptions) (*setup.JoinResult, error) {
		return &setup.JoinResult{NodeID: "de-1", HubAddr: "5.6.7.8:44433", Compatible: true}, nil
	}
	require.Contains(t, e.ok("join", link), "joined hub 5.6.7.8:44433")
}

// The default detector runs `ip -4 route get 1.1.1.1` through the runner.
func TestSetupDefaultDetector(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.fakeSetup(&o)
	e.g.DetectIP = nil
	e.g.PortBusy = nil
	e.fake.On("ip -4 route get 1.1.1.1", exec.OK("1.1.1.1 via 5.6.7.1 dev eth0 src 5.6.7.8 uid 0\n    cache\n"))
	e.tty("", "", "", "", "")
	out := e.ok("setup")
	require.Contains(t, out, "Public IP of this server [5.6.7.8]")
	require.Equal(t, "5.6.7.8", o.PublicIP)
	require.NotZero(t, o.ControlPort)
}
