// Package cli holds the cobra commands of the deyroute binary (spec section
// 14). Commands are thin: daemon-backed commands parse flags, make exactly
// one Local API call (printing its progress) and print human or --json
// output; local operations (setup, join, backup, restore, uninstall, config,
// doctor bundles, completion, version) run in-process through
// internal/daemon/setup, internal/config and internal/doctor.
//
// Exit codes: 0 success, 1 user error (DEY-C/P/T/N/B/F/S/I and usage
// errors), 2 system error (DEY-X), 3 a confirmation is needed but stdin is
// not a terminal and --yes was not given.
package cli

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/version"
)

// NewRoot builds the complete command tree bound to g. Missing fields of g
// get their production defaults first.
func NewRoot(g *Globals) *cobra.Command {
	g.defaults()
	root := &cobra.Command{
		Use:           "deyroute",
		Short:         i18n.T(i18n.CLIShort),
		Long:          i18n.T(i18n.CLILong),
		Example:       i18n.T(i18n.CLIRootExample),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			g.command = cmd.CommandPath()
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return g.runTUI(cmd.Context())
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().BoolVar(&g.Debug, "debug", g.Debug, i18n.T(i18n.CLIFlagDebug))
	root.PersistentFlags().BoolVar(&g.JSON, "json", g.JSON, i18n.T(i18n.CLIFlagJSON))
	root.SetOut(g.Out)
	root.SetErr(g.Err)
	root.SetIn(g.In)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err.Error()) })
	root.AddGroup(
		&cobra.Group{ID: groupStart, Title: i18n.T(i18n.CLIGroupStart)},
		&cobra.Group{ID: groupManage, Title: i18n.T(i18n.CLIGroupManage)},
		&cobra.Group{ID: groupDiag, Title: i18n.T(i18n.CLIGroupDiag)},
		&cobra.Group{ID: groupSystem, Title: i18n.T(i18n.CLIGroupSystem)},
	)
	root.SetHelpCommandGroupID(groupSystem)
	add := func(group string, cmds ...*cobra.Command) {
		for _, c := range cmds {
			c.GroupID = group
			root.AddCommand(c)
		}
	}
	add(groupStart, newSetupCmd(g), newJoinCmd(g), newStatusCmd(g), newVersionCmd(g))
	add(groupManage, newNodeCmd(g), newHubCmd(g), newFrontCmd(g), newTunnelCmd(g), newPortCmd(g), newLadderCmd(g))
	add(groupDiag, newDiagCmd(g), newLogsCmd(g), newEventsCmd(g), newDoctorCmd(g), newStatsCmd(g))
	add(groupSystem, newOptimizeCmd(g), newSecurityCmd(g), newNotifyCmd(g), newBackupCmd(g), newRestoreCmd(g),
		newUpdateCmd(g), newConfigCmd(g), newSettingsCmd(g), newUninstallCmd(g), newCompletionCmd(g))
	root.AddCommand(newMenuCmd(g), newDaemonCmd(g), newRelayCmd(g), newWGCmd(g), newPairCmd(g), newFrontShimCmd(g))
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			c.Short, c.Long = i18n.T(i18n.CLIHelpShort), i18n.T(i18n.CLIHelpLong)
		}
	}
	finishTree(root)
	return root
}

// finishTree applies what every command shares: a plain (non-DEY) error
// returned by a command becomes DEY-X000 (see classify), the -h/--help flag
// is described through i18n, and a group command without examples of its
// own lists the first example of each subcommand (spec section 14: every
// `deyroute <cmd> --help` is complete and has examples).
func finishTree(c *cobra.Command) {
	for _, sub := range c.Commands() {
		finishTree(sub)
	}
	if run := c.RunE; run != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error { return classify(run(cmd, args)) }
	}
	c.InitDefaultHelpFlag()
	if f := c.Flags().Lookup("help"); f != nil {
		f.Usage = i18n.T(i18n.CLIFlagHelp, c.Name())
	}
	if c.Example == "" && c.HasParent() && c.HasAvailableSubCommands() {
		var lines []string
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Example == "" {
				continue
			}
			first, _, _ := strings.Cut(sub.Example, "\n")
			lines = append(lines, first)
		}
		c.Example = strings.Join(lines, "\n")
	}
}

// classify maps what a command returns to what printError expects. DEY
// errors, usage errors and the confirmation sentinels stay as they are; an
// operation interrupted with Ctrl-C (context.Canceled) is errAborted; any
// other plain error is unexpected: DEY-X000 (exit 2) with the redacted
// cause as detail, never a usage error (exit 1). Errors that never reach a
// command (unknown commands, bad arguments and flags) are cobra's usage
// errors.
func classify(err error) error {
	var ue *usageError
	switch {
	case err == nil, isDEY(err), stderrors.As(err, &ue), stderrors.Is(err, errNeedConfirm), stderrors.Is(err, errAborted):
		return err
	case stderrors.Is(err, context.Canceled):
		return errAborted
	}
	return deyerr.Wrap(deyerr.X000, err, nil).WithDetail(clean(dlog.Redact(err.Error())))
}

// Command groups of `deyroute --help`.
const (
	groupStart  = "start"
	groupManage = "manage"
	groupDiag   = "diag"
	groupSystem = "system"
)

// Execute runs the CLI with production settings, stopping on SIGINT or
// SIGTERM, and returns the process exit code.
func Execute(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The first Ctrl-C cancels the running call; once that happened the
	// signals get their default action again, so a second Ctrl-C ends a
	// call that does not stop at once.
	context.AfterFunc(ctx, stop)
	return Run(ctx, &Globals{}, args)
}

// Run executes the command line args with g and returns the exit code
// (section 14: 0, 1, 2 or 3). Panics are recovered: the stack goes to the
// main log file and the owner sees DEY-X000 only (section 13).
func Run(ctx context.Context, g *Globals, args []string) (code int) {
	g.defaults()
	g.command, g.usageCommand, g.ctx = "", "", ctx
	defer g.closeLog()
	defer func() {
		if r := recover(); r != nil {
			logPanic(g.Root, g.component(), r, debug.Stack())
			code = g.printError(deyerr.New(deyerr.X000, nil))
		}
	}()
	root := NewRoot(g)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		if isUsage(err) {
			// The deepest command named on the line, for the --help of
			// DEY-C025 (an unknown flag stops before g.command is set).
			g.usageCommand = root.CommandPath()
			if c, _, ferr := root.Find(args); ferr == nil && c != nil {
				g.usageCommand = c.CommandPath()
			}
		}
		return g.printError(err)
	}
	return deyerr.ExitOK
}

// component is the log component of the running command: hub or node for
// `deyroute daemon hub|node`, cli for everything else.
func (g *Globals) component() string {
	switch g.command {
	case "deyroute daemon " + config.RoleHub:
		return config.RoleHub
	case "deyroute daemon " + config.RoleNode:
		return config.RoleNode
	}
	return logComponent
}

// logPanic appends the panic and its stack to the main log file as one
// record of the deyroute log format (ts in UTC, level, component, code
// DEY-X000, msg, err, stack; section 13), written through the central
// secret filter. Best effort: the file may not exist on a server that is
// not set up.
func logPanic(root, component string, r any, stack []byte) {
	path := rootPath(root, deyerr.DefaultLogPath)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- fixed log path under the configured root
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	slog.New(dlog.NewHandler(f, slog.LevelError, component)).Error("panic",
		dlog.Code(deyerr.X000), slog.String(dlog.KeyErr, fmt.Sprint(r)), slog.String("stack", string(stack)))
}

func newVersionCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   i18n.T(i18n.CLIVersionShort),
		Example: i18n.T(i18n.CLIVersionExample),
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if g.JSON {
				return g.emitJSON(map[string]any{
					"version": version.Version, "commit": version.Commit, "date": version.Date, "go": version.GoVersion(),
				})
			}
			g.say(i18n.CLIVersionLine, version.Display(), version.Commit, version.Date,
				version.GoVersion()+" "+runtime.GOOS+"/"+runtime.GOARCH)
			return nil
		},
	}
}
