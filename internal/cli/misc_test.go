package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/hub"
	"github.com/localroot4/deyroute/internal/daemon/node"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tui"
)

func TestUninstall(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	var uo *setup.UninstallOptions
	e.g.Ops.Uninstall = func(_ context.Context, o setup.UninstallOptions) error {
		uo = &o
		steps(o.Progress, api.Step{Title: setup.StepTitle(setup.StepStopUnits), Status: api.StepOK})
		return nil
	}
	nodesCalled := false
	e.stub.UninstallNodesFn = func(context.Context) ([]string, error) { nodesCalled = true; return []string{"de-1", "nl-1"}, nil }

	errOut := e.fail(3, "uninstall")
	require.Contains(t, errOut, "Uninstall removes from this server")
	require.Contains(t, errOut, "backups included")
	require.Nil(t, uo)

	out := e.ok("uninstall", "--yes", "--keep-backups")
	require.True(t, uo.KeepBackups)
	require.Equal(t, e.root, uo.Root)
	require.False(t, nodesCalled)
	require.Contains(t, out, "deyroute was removed from this server.")
	require.Contains(t, out, "Backups kept in /var/lib/deyroute/backups.")

	// On a TTY: keep backups? (default yes) → nodes too? → typed yes.
	e.tty("n", "y", "yes")
	out = e.ok("uninstall")
	require.False(t, uo.KeepBackups)
	require.True(t, nodesCalled)
	require.Contains(t, out, "Every online node is uninstalled first")
	require.Contains(t, out, "Uninstalled on the nodes: de-1, nl-1")
	e.tty("", "", "no")
	require.Contains(t, e.fail(1, "uninstall"), "Aborted.")

	e.g.IsTTY = false
	doc := e.json("uninstall", "--nodes", "--yes")
	require.Equal(t, []any{"de-1", "nl-1"}, doc["nodes"])
	require.Len(t, doc["steps"], 1)

	// --nodes needs the daemon; the local uninstall does not.
	e.down()
	require.Contains(t, e.fail(2, "uninstall", "--nodes", "--yes"), "DEY-X003")
	e.ok("uninstall", "--yes")
	e.g.Ops.Uninstall = func(context.Context, setup.UninstallOptions) error {
		return deyerr.Join(deyerr.New(deyerr.I022, deyerr.Params{"step": "files"}), deyerr.New(deyerr.I022, deyerr.Params{"step": "binary"}))
	}
	errOut = e.fail(1, "uninstall", "--yes")
	require.Equal(t, 2, strings.Count(errOut, "✖ DEY-I022"), errOut)
	e.fail(1, "uninstall", "--yes", "--json")
	require.Contains(t, e.out.String(), `"errors"`)
}

func TestSetupRepair(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	var ro *setup.RepairOptions
	e.g.Ops.Repair = func(_ context.Context, o setup.RepairOptions) (string, error) {
		ro = &o
		return "deyroute-hub.service", nil
	}
	// A set-up server refuses the wizard but accepts the repair.
	require.Contains(t, e.fail(1, "setup", "--yes"), "DEY-I013")
	out := e.ok("setup", "--repair")
	require.Equal(t, e.root, ro.Root)
	require.Contains(t, out, "deyroute-hub.service was restarted; the configuration is unchanged")
	doc := e.json("setup", "--repair")
	require.Equal(t, "deyroute-hub.service", doc["service"])
	e.g.Ops.Repair = func(context.Context, setup.RepairOptions) (string, error) {
		return "", deyerr.New(deyerr.I023, nil)
	}
	require.Contains(t, e.fail(1, "setup", "--repair"), "DEY-I023")
}

func TestCompletion(t *testing.T) {
	e := newEnv(t)
	out := e.ok("completion", "bash")
	require.Contains(t, out, "__start_deyroute")
	require.Contains(t, out, "complete -o default -F __start_deyroute dey")
	require.Contains(t, e.ok("completion", "zsh"), "compdef _deyroute dey")
	require.Contains(t, e.ok("completion", "fish"), "complete -c dey -w deyroute")
	require.Contains(t, e.fail(1, "completion", "tcsh"), "unknown shell")
	require.Contains(t, e.fail(1, "completion"), "needs 1 argument")
}

func TestHelpHasExamples(t *testing.T) {
	e := newEnv(t)
	root := NewRoot(e.g)
	var walk func(c interface {
		HasSubCommands() bool
	})
	_ = walk
	for _, c := range root.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		require.NotEmpty(t, c.Short, c.Name())
		for _, sub := range c.Commands() {
			require.NotEmpty(t, sub.Short, sub.CommandPath())
			if !sub.HasSubCommands() {
				require.NotEmpty(t, sub.Example, sub.CommandPath())
			}
		}
		if !c.HasSubCommands() {
			require.NotEmpty(t, c.Example, c.Name())
		}
	}
	out := e.ok("tunnel", "add", "--help")
	require.Contains(t, out, "Examples:")
	require.Contains(t, out, "deyroute tunnel add --node de-1 --ports 443,2053")
	require.Contains(t, e.ok("--help"), "Getting started:")
}

func TestHiddenCommands(t *testing.T) {
	e := newEnv(t)
	var ho hub.Options
	var no node.Options
	e.g.RunHub = func(_ context.Context, o hub.Options) error { ho = o; return nil }
	e.g.RunNode = func(_ context.Context, o node.Options) error { no = o; return errors.New("boom") }
	e.ok("daemon", "hub")
	require.Equal(t, e.root, ho.Root)
	require.Nil(t, ho.Logger)
	e.ok("daemon", "hub", "--debug")
	require.NotNil(t, ho.Logger)
	// A plain error of a command is unexpected: DEY-X000 (exit 2) with the
	// cause as detail, never a usage error.
	errOut := e.fail(2, "daemon", "node")
	require.Contains(t, errOut, "DEY-X000")
	require.Contains(t, errOut, "| boom")
	require.NotContains(t, errOut, "--help")
	require.Equal(t, e.root, no.Root)
	require.Contains(t, e.ok("daemon"), "hub")

	// relay: config errors are DEY-B060.
	require.Contains(t, e.fail(1, "relay", "--tunnel", "main"), "--config")
	require.Contains(t, e.fail(1, "relay", "--tunnel", "main", "--config", filepath.Join(e.root, "none.json")), "DEY-B060")
	cfg := filepath.Join(e.root, "relay.json")
	require.NoError(t, os.WriteFile(cfg, []byte(`{"version":1,"tunnel":"other","role":"hub","token":"tokentokentoken"}`), 0o600))
	errOut = e.fail(1, "relay", "--tunnel", "main", "--config", cfg)
	require.Contains(t, errOut, "DEY-B060")

	// wg up/down: config errors are DEY-B072.
	require.Contains(t, e.fail(1, "wg", "up"), "--config")
	require.Contains(t, e.fail(1, "wg", "up", "--config", filepath.Join(e.root, "wg.json")), "DEY-B072")
	require.Contains(t, e.fail(1, "wg", "down", "--config", filepath.Join(e.root, "wg.json")), "DEY-B072")

	// menu --once renders the first screen without a terminal.
	e.writeConfig(hubConfig)
	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 100} }
	out := e.ok("menu", "--once")
	require.Contains(t, out, "DEYROUTE Tunnel Manager")
	require.Contains(t, out, "1) Dashboard (live)")
	require.Contains(t, out, "Hub: ir-1 (5.6.7.8)")

	// Hidden commands stay out of the help.
	help := e.ok("--help")
	for _, h := range []string{"daemon", "relay", "wg ", "menu"} {
		require.NotContains(t, help, "  "+h)
	}
}

func TestTUIWiring(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(strings.Replace(hubConfig, "ui_mode: simple", "ui_mode: advanced", 1))
	var got tui.Options
	e.g.RunTUI = func(o tui.Options) error { got = o; return nil }
	e.ok()
	require.NotNil(t, got.Local)
	require.Nil(t, got.LocalErr)
	require.Equal(t, "deyroute-hub", got.Service)
	require.Equal(t, "hub", got.Status.Role)
	require.Equal(t, "ir-1", got.Status.Name)
	require.True(t, got.Advanced)
	require.Equal(t, 1, got.Status.Nodes)
	require.NotNil(t, got.Doctor)
	e.ok("menu")
	require.NotNil(t, got.Local)

	// The TUI's local operations use the CLI code paths.
	e.stub.DoctorCollectFn = func(context.Context, string) (api.DoctorData, error) {
		return api.DoctorData{Role: "hub", Sections: map[string]string{"os": "x"}}, nil
	}
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return api.Status{Role: "hub"}, nil }
	summary, path, err := got.Doctor(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, summary)
	require.FileExists(t, path)

	e.stub.EventsFn = func(context.Context, api.EventQuery) ([]state.Event, error) { return nil, nil }
	bpath, err := got.Backup(context.Background(), filepath.Join(e.root, "t.tar.gz"), "", true)
	require.NoError(t, err)
	require.FileExists(t, bpath)

	var ro setup.RestoreOptions
	e.g.Ops.Restore = func(_ context.Context, o setup.RestoreOptions) (*setup.RestoreResult, error) {
		ro = o
		res := &setup.RestoreResult{Role: "hub", HubName: "ir-1", PublicIP: "5.6.7.8"}
		if o.PublicIP != "" {
			res.PublicIP, res.AddressChanged = o.PublicIP, true
		}
		return res, nil
	}
	e.g.DetectIP = func(context.Context) (string, bool, error) { return "9.9.9.9", false, nil }
	// Same server: no moved-hub question, the backup's address stays.
	plan, err := got.RestoreCheck(context.Background(), bpath, "")
	require.NoError(t, err)
	require.Empty(t, plan.MovedIP)
	require.Contains(t, plan.Lost, "Restoring t.tar.gz (hub ir-1, created ")
	require.NotContains(t, plan.Lost, "address changes")
	sum, err := got.Restore(context.Background(), bpath, "", plan.MovedIP)
	require.NoError(t, err)
	require.Empty(t, ro.PublicIP)
	require.NotContains(t, sum, "address changes")
	// Another server: the menu asks with the detected address, then
	// restores with the address the owner chose.
	require.NoError(t, os.Remove(filepath.Join(e.root, "etc/deyroute/config.yaml")))
	plan, err = got.RestoreCheck(context.Background(), bpath, "")
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9", plan.MovedIP)
	require.Equal(t, "5.6.7.8", plan.OldIP)
	sum, err = got.Restore(context.Background(), bpath, "", plan.MovedIP)
	require.NoError(t, err)
	require.Equal(t, "9.9.9.9", ro.PublicIP)
	require.Nil(t, ro.Progress)
	// The menu says that the hub moved and what to do on the nodes, by
	// menu item and by command.
	require.Contains(t, sum, "to 9.9.9.9; the hub certificate is re-issued")
	require.Contains(t, sum, "10) Backup & Restore -> 3) Announce hub move")
	require.Contains(t, sum, "deyroute hub announce-move 9.9.9.9:44433")
	_, err = got.Restore(context.Background(), bpath, "", "")
	require.NoError(t, err)
	require.Empty(t, ro.PublicIP) // the owner answered no
	_, err = got.RestoreCheck(context.Background(), filepath.Join(e.root, "none"), "")
	require.Error(t, err)
	_, err = got.Restore(context.Background(), filepath.Join(e.root, "none"), "", "")
	require.Error(t, err)

	var uo setup.UninstallOptions
	e.g.Ops.Uninstall = func(_ context.Context, o setup.UninstallOptions) error { uo = o; return nil }
	e.stub.UninstallNodesFn = func(context.Context) ([]string, error) { return nil, nil }
	require.NoError(t, got.Uninstall(context.Background(), true, true))
	require.True(t, uo.KeepBackups)

	// Without a daemon the TUI still opens, with the dial error.
	e.down()
	e.writeConfig(nodeConfig)
	e.ok()
	require.Nil(t, got.Local)
	require.True(t, deyerr.HasCode(got.LocalErr, deyerr.X003))
	require.Equal(t, "deyroute-node", got.Service)
	require.Equal(t, "de-1", got.Status.Name)
	require.Equal(t, "5.6.7.8:44433", got.Status.HubAddr)
	// Set hub address with the node agent stopped writes node.hub_addr.
	saved := ""
	e.g.Ops.SetHubAddr = func(_, addr string) error { saved = addr; return nil }
	running, err := got.SetHub(context.Background(), "5.6.7.9:44433")
	require.NoError(t, err)
	require.False(t, running)
	require.Equal(t, "5.6.7.9:44433", saved)
}

func TestErrorPrinting(t *testing.T) {
	e := newEnv(t)
	// Unknown commands and flags are usage errors (exit 1, no DEY code).
	require.Contains(t, e.fail(1, "frobnicate"), `unknown command "frobnicate"`)
	require.Contains(t, e.fail(1, "status", "--frob"), "unknown flag")
	require.Contains(t, e.fail(1, "tunnel", "nope"), "unknown command")
	e.fail(1, "frobnicate", "--json")
	require.Contains(t, e.out.String(), `"exit_code": 1`)

	// ASCII terminals print "x" instead of the cross.
	e.g.Caps = func() tui.Caps { return tui.Caps{} }
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) {
		return nil, deyerr.New(deyerr.C021, deyerr.Params{"tunnel": "nope"})
	}
	errOut := e.fail(1, "tunnel", "list")
	require.True(t, strings.HasPrefix(errOut, "x DEY-C021"), errOut)

	// A panic is DEY-X000 (exit 2), the stack goes to the log.
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { panic("kaboom") }
	logDir := filepath.Join(e.root, "var/log/deyroute")
	require.NoError(t, os.MkdirAll(logDir, 0o700))
	errOut = e.fail(2, "tunnel", "list")
	require.Contains(t, errOut, "DEY-X000")
	require.NotContains(t, errOut, "kaboom")
	data, err := os.ReadFile(filepath.Join(logDir, "deyroute.log"))
	require.NoError(t, err)
	require.Contains(t, string(data), "kaboom")

	// deyErrors / isDEY on joined and plain errors.
	require.False(t, isDEY(errors.New("plain")))
	require.True(t, isDEY(errors.Join(errors.New("a"), deyerr.New(deyerr.C001, nil))))
	require.Len(t, deyErrors(errors.Join(errors.New("a"))), 1)
	require.Nil(t, deyErrors(nil))
	require.Equal(t, "1.5 KB", humanBytes(1536))
	require.Equal(t, "12 B", humanBytes(12))
	require.Equal(t, "ab…", trunc("abcdef", 3, "…"))
	require.Equal(t, "  x", padLeft("x", 3))
	require.Equal(t, "", clean("\n"))
	require.Equal(t, config.RoleHub, e.g.defaultName(config.RoleHub)[:0]+config.RoleHub)
}

func TestDefaultsAndHostname(t *testing.T) {
	g := &Globals{Root: t.TempDir()}
	g.defaults()
	require.NotNil(t, g.Dial)
	require.NotNil(t, g.Runner)
	require.Equal(t, filepath.Join(g.Root, config.SocketPath), g.Socket)
	g.Hostname = func() (string, error) { return "", errors.New("no") }
	require.Equal(t, "hub", g.defaultName("hub"))
	g.Hostname = func() (string, error) { return "---", nil }
	require.Equal(t, "node", g.defaultName("node"))
	g.Hostname = func() (string, error) { return "Tehran-01.dc", nil }
	require.Equal(t, "tehran-01", g.defaultName("node"))
	require.Equal(t, "/etc/x", rootPath("/", "/etc/x"))
	require.Equal(t, "/etc/x", rootPath("", "/etc/x"))
	require.NotEmpty(t, detectCaps(func(string) string { return "" }))
}
