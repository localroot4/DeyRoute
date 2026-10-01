package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// A plain error returned by a command is unexpected: DEY-X000 (exit 2)
// with its redacted cause, never a usage error (exit 1). An interrupted
// call is "Aborted." (exit 1).
func TestClassifyErrors(t *testing.T) {
	e := newEnv(t)
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { return nil, errors.New("disk on fire") }
	errOut := e.fail(2, "tunnel", "list")
	require.Contains(t, errOut, "✖ DEY-X000")
	require.Contains(t, errOut, "  | disk on fire")
	require.NotContains(t, errOut, "DEY-C025")
	e.fail(2, "tunnel", "list", "--json")
	require.Contains(t, e.out.String(), `"code": "DEY-X000"`)
	require.Contains(t, e.out.String(), `"exit_code": 2`)

	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) {
		return nil, fmt.Errorf("list: %w", context.Canceled)
	}
	require.Contains(t, e.fail(1, "tunnel", "list"), "Aborted.")

	require.NoError(t, classify(nil))
	require.Equal(t, errNeedConfirm, classify(errNeedConfirm))
	require.Equal(t, errAborted, classify(errAborted))
	u := usageErr("bad")
	require.Equal(t, u, classify(u))
	var d error = deyerr.New(deyerr.C021, deyerr.Params{"tunnel": "x"})
	require.Equal(t, d, classify(d))
	dlog.RegisterSecret("classify-secret-value-123")
	x := deyerr.As(classify(errors.New("token classify-secret-value-123\x1b[31m")))
	require.Equal(t, deyerr.X000, x.Code)
	require.NotContains(t, x.Detail, "classify-secret-value-123")
	require.NotContains(t, x.Detail, "\x1b")
}

// Every DEY error the CLI prints is written to the deyroute.log its Log line
// names, with the code and the command path but never the arguments; the
// CLI never creates /var/log/deyroute itself. --debug mirrors the log to
// stderr.
func TestCLILog(t *testing.T) {
	e := newEnv(t)
	e.down()
	logDir := filepath.Join(e.root, "var/log/deyroute")
	logFile := filepath.Join(logDir, "deyroute.log")
	e.fail(2, "status")
	require.NoDirExists(t, logDir)

	require.NoError(t, os.MkdirAll(logDir, 0o750))
	e.fail(2, "tunnel", "delete", "arg-not-logged", "--yes")
	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	for _, want := range []string{`"code":"DEY-X003"`, `"command":"deyroute tunnel delete"`, `"component":"cli"`, "Daemon not running"} {
		require.Contains(t, string(data), want)
	}
	require.NotContains(t, string(data), "arg-not-logged")
	fi, err := os.Stat(logFile)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	// A wrong command line is DEY-C025: logged with the command, never the
	// line itself (it may hold a join link). A declined confirmation is
	// not logged.
	e.fail(1, "tunnel", "frobnicate", "dey://not-logged")
	e.g.Dial = func() (api.Local, error) { return e.stub, nil }
	e.fail(3, "tunnel", "delete", "main")
	after, err := os.ReadFile(logFile)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(after), string(data)))
	added := strings.TrimPrefix(string(after), string(data))
	require.Equal(t, 1, strings.Count(added, "\n"), added)
	require.Contains(t, added, `"msg":"invalid command line","component":"cli","code":"DEY-C025","command":"deyroute tunnel"`)
	require.NotContains(t, added, "frobnicate")
	require.NotContains(t, added, "not-logged")

	// --debug: the same lines also go to stderr.
	e.down()
	errOut := e.fail(2, "status", "--debug")
	require.Contains(t, errOut, `"code":"DEY-X003"`)
	require.Contains(t, errOut, `"command":"deyroute status"`)
	e.g.closeLog()
}

// The local operations get the CLI log, the injected user/group lookups,
// chown and host name (the end-to-end harness runs them unprivileged).
func TestLocalOpsInjection(t *testing.T) {
	e := newEnv(t)
	logDir := filepath.Join(e.root, "var/log/deyroute")
	require.NoError(t, os.MkdirAll(logDir, 0o750))
	e.g.LookupUser = func(string) (int, error) { return 7, nil }
	e.g.LookupGroup = func(string) (int, error) { return 8, nil }
	e.g.Chown = func(string, int, int) error { return errors.New("chown called") }
	check := func(lu, lg func(string) (int, error), ch func(string, int, int) error) {
		t.Helper()
		uid, _ := lu("deyroute")
		gid, _ := lg("deyroute")
		require.Equal(t, 7, uid)
		require.Equal(t, 8, gid)
		require.EqualError(t, ch("/x", 0, 0), "chown called")
	}

	var ho setup.HubOptions
	e.g.Ops.SetupHub = func(_ context.Context, o setup.HubOptions) (*setup.HubResult, error) {
		ho = o
		o.Logger.Info("hub setup step logged")
		return &setup.HubResult{PublicIP: "5.6.7.8", ControlPort: 44433}, nil
	}
	e.ok("setup", "--role", "hub", "--name", "ir-1", "--yes")
	check(ho.LookupUser, ho.LookupGroup, ho.Chown)

	var jo setup.JoinOptions
	e.g.Ops.Join = func(_ context.Context, o setup.JoinOptions) (*setup.JoinResult, error) {
		jo = o
		o.Logger.Info("join step logged")
		return &setup.JoinResult{NodeID: "de-1", HubName: "ir-1", Compatible: true}, nil
	}
	e.ok("join", "dey://T@5.6.7.8:44433#sha256:"+strings.Repeat("ab", 32))
	check(jo.LookupUser, jo.LookupGroup, jo.Chown)
	host, err := jo.Hostname()
	require.NoError(t, err)
	require.Equal(t, "IR-Server.example.com", host)

	var ro setup.RestoreOptions
	e.g.Ops.Restore = func(_ context.Context, o setup.RestoreOptions) (*setup.RestoreResult, error) {
		ro = o
		o.Logger.Info("restore step logged")
		return &setup.RestoreResult{Role: "hub", HubName: "ir-1", PublicIP: "5.6.7.8"}, nil
	}
	e.ok("restore", writeFixtureBackup(t, e.root, hubConfig, ""), "--yes")
	check(ro.LookupUser, ro.LookupGroup, ro.Chown)

	var uo setup.UninstallOptions
	e.g.Ops.Uninstall = func(_ context.Context, o setup.UninstallOptions) error {
		uo = o
		o.Logger.Info("uninstall step logged")
		return nil
	}
	e.ok("uninstall", "--yes")
	require.NotNil(t, uo.Logger)

	data, err := os.ReadFile(filepath.Join(logDir, "deyroute.log"))
	require.NoError(t, err)
	for _, want := range []string{"hub setup step logged", "join step logged", "restore step logged", "uninstall step logged"} {
		require.Contains(t, string(data), want)
	}
}

// Group commands have examples in their --help (spec section 14), the help
// texts of cobra's own help command and flag come from i18n.
func TestHelpForGroups(t *testing.T) {
	e := newEnv(t)
	out := e.ok("node", "--help")
	for _, want := range []string{"Examples:", "deyroute node join-command", "deyroute node remove nl-1", "help for node"} {
		require.Contains(t, out, want)
	}
	out = e.ok("security", "--help")
	// Nested groups contribute their own first example.
	require.Contains(t, out, "deyroute security tls renew --tunnel main")
	require.Contains(t, out, "deyroute security firewall apply")
	require.Contains(t, e.ok("help", "tunnel"), "deyroute tunnel add --node de-1 --ports 443,2053")
	require.Contains(t, e.ok("--help"), i18n.T(i18n.CLIHelpShort))
	require.Contains(t, e.ok("help", "--help"), "deyroute help <command>")

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "help" {
				continue
			}
			require.NotEmpty(t, sub.Example, sub.CommandPath())
			walk(sub)
		}
	}
	walk(NewRoot(e.g))
}

// `deyroute version` prints version, commit, build date and Go version from
// the i18n line.
func TestVersionLine(t *testing.T) {
	e := newEnv(t)
	out := e.ok("version")
	require.Regexp(t, `^deyroute \S+ \(commit \S+, built \S+, go\S+ \S+/\S+\)\n$`, out)
}

// config edit never overwrites a config.yaml that changed while the editor
// was open (another command saved it): DEY-C024, the edited copy is kept.
func TestConfigEditConflict(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	applied := false
	e.stub.ConfigApplyFn = func(context.Context, func(api.Step)) (api.ApplyResult, error) {
		applied = true
		return api.ApplyResult{}, nil
	}
	other := strings.Replace(hubConfig, "ui_mode: simple", "ui_mode: advanced", 1)
	e.g.Editor = func(_ context.Context, path string) error {
		e.writeConfig(other) // e.g. `deyroute settings ui-mode advanced` meanwhile
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(strings.Replace(string(data), `name: "Main"`, `name: "Main 443"`, 1)), 0o600)
	}
	errOut := e.fail(1, "config", "edit")
	require.Contains(t, errOut, "DEY-C024")
	m := regexp.MustCompile(`kept in (\S+):`).FindStringSubmatch(errOut)
	require.Len(t, m, 2, errOut)
	t.Cleanup(func() { _ = os.Remove(m[1]) })
	kept, err := os.ReadFile(m[1])
	require.NoError(t, err)
	require.Contains(t, string(kept), `name: "Main 443"`)
	require.NotContains(t, string(kept), editHeaderPrefix)
	cur, err := os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.Equal(t, other, string(cur))
	require.False(t, applied)
}

// On a node config edit saves the file and says how the agent picks it up:
// the node has no ConfigApply (DEY-X009), so it is not called.
func TestConfigEditOnNode(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(nodeConfig)
	e.stub.ConfigApplyFn = func(context.Context, func(api.Step)) (api.ApplyResult, error) {
		t.Fatal("ConfigApply called on a node")
		return api.ApplyResult{}, nil
	}
	e.g.Editor = (&editorScript{t: t, edits: []func(string) string{
		func(c string) string { return strings.Replace(c, "5.6.7.8:44433", "5.6.7.9:44433", 1) },
	}}).edit
	out := e.ok("config", "edit")
	require.Contains(t, out, "/etc/deyroute/config.yaml saved.")
	require.Contains(t, out, "systemctl restart deyroute-node")
	e.g.Editor = (&editorScript{t: t, edits: []func(string) string{
		func(c string) string { return strings.Replace(c, "5.6.7.9:44433", "5.6.7.10:44433", 1) },
	}}).edit
	doc := e.json("config", "edit")
	require.Equal(t, true, doc["saved"])
	require.Equal(t, "deyroute-node", doc["restart"])
	data, err := os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.Contains(t, string(data), "hub_addr: 5.6.7.10:44433")
}

// The central secret filter applies to config show --json as well.
func TestConfigShowRedacts(t *testing.T) {
	e := newEnv(t)
	secret := "chat-secret-value-9876"
	dlog.RegisterSecret(secret)
	e.writeConfig(strings.Replace(hubConfig, `chat_id: ""`, `chat_id: "`+secret+`"`, 1))
	doc := e.json("config", "show")
	tg := doc["config"].(map[string]any)["hub"].(map[string]any)["notify"].(map[string]any)["telegram"].(map[string]any)
	require.Equal(t, dlog.Mask, tg["chat_id"])
	require.NotContains(t, e.ok("config", "show"), secret)
	require.Equal(t, []any{dlog.Mask, "x"}, redactValues([]any{secret, "x"}))
}

// An unknown ladder profile is named without a bogus tunnel.
func TestLadderShowUnknown(t *testing.T) {
	e := newEnv(t)
	e.stub.LadderListFn = func(context.Context) ([]api.Ladder, error) {
		return []api.Ladder{{Name: "default", Builtin: true, Rungs: []string{"backhaul/wssmux"}}}, nil
	}
	errOut := e.fail(1, "ladder", "show", "nope")
	require.Contains(t, errOut, "DEY-C012  Unknown ladder 'nope'\n")
}

// update --version does not depend on the release check (the hub may not
// reach the release API); a plain update does, and a stopped daemon stays
// fatal.
func TestUpdateVersionWithoutCheck(t *testing.T) {
	e := newEnv(t)
	e.stub.UpdateCheckFn = func(context.Context) (api.UpdateInfo, error) {
		return api.UpdateInfo{}, deyerr.New(deyerr.X006, deyerr.Params{"status": "release API unreachable"})
	}
	var applied string
	e.stub.UpdateApplyFn = func(_ context.Context, v string, _ func(api.Step)) (api.UpdateInfo, error) {
		applied = v
		return api.UpdateInfo{Current: v, Previous: "1.0.0"}, nil
	}
	out := e.ok("update", "--version", "1.2.0", "--yes")
	require.Equal(t, "1.2.0", applied)
	require.Contains(t, out, "The release check failed (DEY-X006); installing 1.2.0 as requested.")
	require.Contains(t, out, "Updated deyroute 1.0.0 -> 1.2.0.")
	// Non-TTY without --yes: the confirmation names the current version.
	require.Contains(t, e.fail(3, "update", "--version", "1.2.0"), "-> 1.2.0")

	applied = ""
	require.Contains(t, e.fail(2, "update", "--yes"), "DEY-X006")
	e.stub.UpdateCheckFn = func(context.Context) (api.UpdateInfo, error) {
		return api.UpdateInfo{}, deyerr.New(deyerr.X003, deyerr.Params{"service": "deyroute-hub"})
	}
	require.Contains(t, e.fail(2, "update", "--version", "1.2.0", "--yes"), "DEY-X003")
	require.Empty(t, applied)
}

// The backup's event export: a node has none (nothing said), a failed
// export is named, a stopped daemon is explained.
func TestBackupEvents(t *testing.T) {
	e := newEnv(t)
	out := filepath.Join(e.root, "b.tar.gz")
	e.writeConfig(nodeConfig)
	e.stub.EventsFn = func(context.Context, api.EventQuery) ([]state.Event, error) {
		t.Fatal("Events called on a node")
		return nil, nil
	}
	text := e.ok("backup", "--no-encrypt", "--out", out)
	require.NotContains(t, text, "event history")
	doc := e.json("backup", "--no-encrypt", "--out", out)
	require.EqualValues(t, 0, doc["events"])
	require.NotContains(t, doc, "events_error")

	e.writeConfig(hubConfig)
	e.stub.EventsFn = func(context.Context, api.EventQuery) ([]state.Event, error) {
		return nil, deyerr.New(deyerr.X001, deyerr.Params{"path": "state.db"})
	}
	require.Contains(t, e.ok("backup", "--no-encrypt", "--out", out), "The event history could not be exported (DEY-X001)")
	doc = e.json("backup", "--no-encrypt", "--out", out)
	require.EqualValues(t, -1, doc["events"])
	require.Equal(t, "DEY-X001", doc["events_error"].(map[string]any)["code"])

	// A daemon that keeps no events (DEY-X009) is like a node.
	e.stub.EventsFn = func(context.Context, api.EventQuery) ([]state.Event, error) {
		return nil, deyerr.New(deyerr.X009, deyerr.Params{"role": "node", "need": "hub"})
	}
	require.EqualValues(t, 0, e.json("backup", "--no-encrypt", "--out", out)["events"])

	e.down()
	require.Contains(t, e.ok("backup", "--no-encrypt", "--out", out), "The daemon is not running")
	doc = e.json("backup", "--no-encrypt", "--out", out)
	require.EqualValues(t, -1, doc["events"])
	require.Equal(t, "DEY-X003", doc["events_error"].(map[string]any)["code"])
}

// A daemon without doctor support still gives the summary its status.
func TestDoctorLocalWithDaemonStatus(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return sampleStatus(), nil }
	out := e.ok("doctor")
	require.Contains(t, out, "1.0.0") // the daemon's version in the summary title
	require.NotContains(t, out, "The daemon is not running")
}

// Execute is the production entry: real streams, signal context.
func TestExecute(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	code := Execute([]string{"version", "--json"})
	os.Stdout = old
	require.NoError(t, w.Close())
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, 0, code)
	require.Contains(t, string(data), `"schema": 1`)
}

// signalReader reports each Read on called (the prompt is waiting).
type signalReader struct {
	called chan struct{}
	r      io.Reader
}

func (s *signalReader) Read(p []byte) (int, error) {
	select {
	case s.called <- struct{}{}:
	default:
	}
	return s.r.Read(p)
}

// Ctrl-C while a question waits for its answer aborts at once (exit 1)
// instead of continuing with the next answer; nothing is deleted.
func TestPromptCtrlC(t *testing.T) {
	e := newEnv(t)
	deleted := false
	e.stub.TunnelDeleteFn = func(context.Context, string, func(api.Step)) error { deleted = true; return nil }
	pr, pw := io.Pipe()
	sr := &signalReader{called: make(chan struct{}, 1), r: pr}
	e.g.IsTTY, e.g.In, e.g.reader = true, sr, nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Run(ctx, e.g, []string{"tunnel", "delete", "main"}) }()
	<-sr.called
	cancel()
	code := <-done
	require.NoError(t, pw.Close()) // releases the reading goroutine
	require.Equal(t, 1, code)
	require.Contains(t, e.errOut.String(), "Aborted.")
	require.False(t, deleted)

	// A run whose context already ended asks nothing.
	e.tty("yes")
	e.out.Reset()
	e.errOut.Reset()
	require.Equal(t, 1, Run(ctx, e.g, []string{"tunnel", "delete", "main"}))
	require.False(t, deleted)
}
