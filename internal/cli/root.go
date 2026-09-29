// Package cli holds the cobra commands. Commands are thin: they parse flags,
// call the daemon through the Local API and print human or --json output.
package cli

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/tui"
	"github.com/localroot4/deyroute/internal/version"
)

// Globals shared by all commands.
type Globals struct {
	Debug bool
	JSON  bool
	Yes   bool
	Out   io.Writer
	Err   io.Writer
	In    io.Reader
	// IsTTY reports whether stdin is interactive (confirmation prompts).
	IsTTY bool
}

// NewRoot builds the command tree.
func NewRoot(g *Globals) *cobra.Command {
	root := &cobra.Command{
		Use:           "deyroute",
		Short:         i18n.T(i18n.CLIShort),
		Long:          i18n.T(i18n.CLILong),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI()
		},
	}
	root.PersistentFlags().BoolVar(&g.Debug, "debug", os.Getenv("DEYROUTE_DEBUG") == "1", i18n.T(i18n.CLIFlagDebug))
	root.SetOut(g.Out)
	root.SetErr(g.Err)
	root.SetIn(g.In)
	root.AddCommand(newVersionCmd(g), newMenuCmd(g))
	return root
}

func detectCaps() tui.Caps {
	caps := tui.DetectCaps(os.Getenv)
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		caps.Width = w
	} else if cols, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil {
		caps.Width = cols
	}
	return caps
}

func runTUI() error {
	return tui.Run(tui.Options{Caps: detectCaps()})
}

// newMenuCmd is a hidden helper: `deyroute menu --once` prints the first
// screen without a TTY (smoke tests, scenario S24); `deyroute menu` = TUI.
func newMenuCmd(g *Globals) *cobra.Command {
	var once bool
	cmd := &cobra.Command{
		Use:    "menu",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !once {
				return runTUI()
			}
			_, err := fmt.Fprint(g.Out, tui.NewModel(tui.Options{Caps: detectCaps()}).View())
			return err
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "render the first screen once and exit")
	return cmd
}

func newVersionCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: i18n.T(i18n.CLIVersionShort),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(g.Out, version.String())
			return err
		},
	}
}

// Execute runs the CLI and returns the process exit code. Panics are
// recovered here: the stack goes to the log, the UI shows DEY-X000 only.
func Execute(args []string) (code int) {
	g := &Globals{Out: os.Stdout, Err: os.Stderr, In: os.Stdin, IsTTY: term.IsTerminal(int(os.Stdin.Fd()))}
	defer func() {
		if r := recover(); r != nil {
			logPanic(r, debug.Stack())
			e := deyerr.New(deyerr.X000, nil)
			fmt.Fprint(g.Err, e.Format(tui.DetectCaps(os.Getenv).Unicode))
			code = deyerr.ExitSystem
		}
	}()
	root := NewRoot(g)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		return printError(g, err)
	}
	return deyerr.ExitOK
}

func printError(g *Globals, err error) int {
	e := deyerr.As(err)
	if e.Code == deyerr.X000 && e.Cause != nil && isUsageError(e.Cause) {
		fmt.Fprintln(g.Err, e.Cause.Error())
		return deyerr.ExitUser
	}
	fmt.Fprint(g.Err, e.Format(tui.DetectCaps(os.Getenv).Unicode))
	return e.ExitCode()
}

// cobra reports flag/arg problems as plain errors; they are user errors.
func isUsageError(err error) bool {
	_, isDey := err.(*deyerr.Error)
	return !isDey
}

// logPanic appends the panic and stack to the main log file. Best effort.
func logPanic(r any, stack []byte) {
	f, err := os.OpenFile(deyerr.DefaultLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	fmt.Fprintf(f, "{\"level\":\"ERROR\",\"code\":\"DEY-X000\",\"msg\":\"panic\",\"err\":%q,\"stack\":%q}\n", fmt.Sprint(r), string(stack))
}
