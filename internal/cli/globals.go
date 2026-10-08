package cli

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/term"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/hub"
	"github.com/localroot4/deyroute/internal/daemon/node"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/sysinfo"
	"github.com/localroot4/deyroute/internal/tui"
)

// Environment variables read by the CLI.
const (
	// EnvBackupPassphrase supplies the backup passphrase when stdin is not
	// a terminal (`deyroute backup` / `deyroute restore` in scripts).
	EnvBackupPassphrase = "DEYROUTE_BACKUP_PASSPHRASE" // #nosec G101 -- name of an environment variable, not a credential
	// EnvMirror is the owner's release mirror (spec section 5).
	EnvMirror = "DEYROUTE_MIRROR"
)

// Timeouts of Local API calls. Every call has one; only followed log
// streams and `status --watch` run until Ctrl-C.
const (
	// CallTimeout bounds a simple daemon call.
	CallTimeout = 2 * time.Minute
	// LongCallTimeout bounds calls that report progress (tunnel add,
	// update, test-ladder, rotate-ca …).
	LongCallTimeout = time.Hour
	// DefaultWatchInterval is the `status --watch` refresh (section 6).
	DefaultWatchInterval = 2 * time.Second
)

// Globals is the environment of one CLI run. The zero value is completed
// with production defaults by NewRoot; tests and the end-to-end harness
// replace what they need (streams, root directory, daemon dialer, runner,
// clock, prompts, local operations), so every command runs unprivileged in
// a temporary directory without systemd, nftables or root.
type Globals struct {
	// Debug and JSON are the persistent --debug and --json flags.
	Debug bool
	JSON  bool

	// Out, Err and In are the terminal streams (os.Stdout, os.Stderr,
	// os.Stdin).
	Out io.Writer
	Err io.Writer
	In  io.Reader
	// IsTTY reports whether stdin is interactive: confirmations and the
	// setup wizard ask questions only then (section 14: exit 3 otherwise).
	// The default is term.IsTerminal(stdin) when In is os.Stdin.
	IsTTY bool
	// OutTTY reports whether stdout is a terminal: `status --watch` clears
	// the screen only then (term.IsTerminal(stdout) when Out is os.Stdout).
	OutTTY bool

	// Root is the filesystem root of every local path ("/" when empty).
	Root string
	// Socket is the Local API socket (Root/run/deyroute/daemon.sock).
	Socket string
	// Dial connects to the local daemon; the default is api.Dial(Socket)
	// naming deyroute-hub or deyroute-node (from the local config role) in
	// DEY-X003.
	Dial func() (api.Local, error)
	// Runner runs allow-listed programs for local operations
	// (exec.NewRunner()).
	Runner exec.Runner
	// Now is the clock (time.Now).
	Now func() time.Time
	// Getenv reads the environment (os.Getenv).
	Getenv func(string) string
	// Caps describes the terminal (tui.DetectCaps plus the window width).
	Caps func() tui.Caps
	// Hostname returns the host name (os.Hostname), the default server name.
	Hostname func() (string, error)
	// ReadPassword reads a secret without echo (golang.org/x/term).
	ReadPassword func(prompt string) (string, error)
	// Editor opens path in the owner's $EDITOR (exec.RunEditor).
	Editor func(ctx context.Context, path string) error
	// DetectIP detects this server's public IPv4 address
	// (setup.DetectPublicIP).
	DetectIP func(ctx context.Context) (ip string, private bool, err error)
	// PortBusy reports whether a TCP port is taken (setup.PortBusy).
	PortBusy func(port int) bool
	// HostFacts measures this server for the setup wizard's automatic
	// tuning preview (sysinfo.Collect of Root); tests pass fixed facts.
	HostFacts func() sysinfo.Facts
	// NoService keeps local operations from installing and starting the
	// systemd services (tests and the end-to-end harness).
	NoService bool
	// LookupUser, LookupGroup and Chown are handed to the local operations
	// (setup, join, restore) for the backend user deyroute and the ownership
	// of the files they write; nil keeps their defaults (os/user lookups
	// and os.Lchown), so the end-to-end harness can run them unprivileged.
	LookupUser  func(name string) (uid int, err error)
	LookupGroup func(name string) (gid int, err error)
	Chown       func(path string, uid, gid int) error
	// WatchInterval is the `status --watch` refresh (2 s).
	WatchInterval time.Duration

	// Ops are the local operations of internal/daemon/setup.
	Ops Ops
	// RunTUI opens the interactive menu (tui.Run).
	RunTUI func(o tui.Options) error
	// RunHub and RunNode run the daemons of `deyroute daemon hub|node`
	// (hub.Run, node.Run).
	RunHub  func(ctx context.Context, o hub.Options) error
	RunNode func(ctx context.Context, o node.Options) error

	reader *bufio.Reader
	// details is --details of optimize status (every value, not the group summaries).
	details bool
	ready   bool
	// ctx is the context of the current run: Ctrl-C ends it, which also
	// leaves a question that waits for an answer (readLine).
	ctx context.Context

	// log is the CLI log of the current run (see logger), closed by Run.
	log       *slog.Logger
	logCloser io.Closer
	// command is the path of the command being run ("deyroute tunnel add"),
	// recorded with every logged error (never its arguments: a join link
	// carries a token).
	command string
	// usageCommand is the command a wrong command line was meant for, whose
	// --help DEY-C025 names (set by Run).
	usageCommand string
}

// logComponent is the "component" field of the CLI log.
const logComponent = "cli"

// logger is the CLI's own log, Root/var/log/deyroute/deyroute.log: the "Log:"
// line of the errors the CLI prints points there (section 13), and the
// local operations (setup, join, restore, uninstall) log their steps into
// it. The file is used only when its directory exists (the CLI never
// creates /var/log/deyroute); --debug or DEYROUTE_DEBUG=1 lowers the level to
// debug and mirrors the log to stderr. Without either, or when the file
// cannot be opened (e.g. run by a user who is not root), it is discarded.
func (g *Globals) logger() *slog.Logger {
	if g.log != nil {
		return g.log
	}
	g.log = dlog.Discard()
	debug := g.Debug || dlog.DebugFromEnv()
	file := g.path(deyerr.DefaultLogPath)
	if fi, err := os.Stat(filepath.Dir(file)); err != nil || !fi.IsDir() {
		file = ""
	}
	if file == "" && !debug {
		return g.log
	}
	l, c, err := dlog.New(dlog.Options{Component: logComponent, File: file, Debug: debug, StderrWriter: g.Err})
	if err != nil && file != "" && debug {
		l, c, err = dlog.New(dlog.Options{Component: logComponent, Debug: true, StderrWriter: g.Err})
	}
	if err == nil {
		g.log, g.logCloser = l, c
	}
	return g.log
}

// closeLog closes the CLI log of this run (Run calls it last).
func (g *Globals) closeLog() {
	if g.logCloser != nil {
		_ = g.logCloser.Close()
	}
	g.log, g.logCloser = nil, nil
}

// logError records one error the CLI printed, with its code and cause. A
// wrong command line (DEY-C025) may quote an argument, and a join link
// carries a token: only its code and the command are recorded.
func (g *Globals) logError(e *deyerr.Error) {
	if e.Code == deyerr.C025 {
		g.logger().Error("invalid command line", dlog.Code(e.Code), slog.String("command", g.usageCommand))
		return
	}
	attrs := []any{dlog.Code(e.Code), dlog.Err(e)}
	if g.command != "" {
		attrs = append(attrs, slog.String("command", g.command))
	}
	g.logger().Error(e.Message(), attrs...)
}

// Ops are the local operations run in-process (ARCHITECTURE.md §7.4).
type Ops struct {
	SetupHub   func(ctx context.Context, o setup.HubOptions) (*setup.HubResult, error)
	Join       func(ctx context.Context, o setup.JoinOptions) (*setup.JoinResult, error)
	Uninstall  func(ctx context.Context, o setup.UninstallOptions) error
	Backup     func(ctx context.Context, o setup.BackupOptions) (string, error)
	Restore    func(ctx context.Context, o setup.RestoreOptions) (*setup.RestoreResult, error)
	SetHubAddr func(root, addr string) error
	Repair     func(ctx context.Context, o setup.RepairOptions) (string, error)
	// SelfRollback swaps the binary with deyroute.prev (update --rollback
	// without the daemon).
	SelfRollback func(root string) error
}

// defaults fills every unset field with its production value. It is
// idempotent.
func (g *Globals) defaults() {
	if g.ready {
		return
	}
	g.ready = true
	if g.Out == nil {
		g.Out = os.Stdout
		g.OutTTY = term.IsTerminal(int(os.Stdout.Fd()))
	}
	if g.Err == nil {
		g.Err = os.Stderr
	}
	if g.In == nil {
		g.In = os.Stdin
		g.IsTTY = term.IsTerminal(int(os.Stdin.Fd()))
	}
	if g.Root == "" {
		g.Root = "/"
	}
	if g.Getenv == nil {
		g.Getenv = os.Getenv
	}
	if g.Socket == "" {
		g.Socket = rootPath(g.Root, config.SocketPath)
	}
	if g.Runner == nil {
		g.Runner = exec.NewRunner()
	}
	if g.Now == nil {
		g.Now = time.Now
	}
	if g.Caps == nil {
		g.Caps = func() tui.Caps { return detectCaps(g.Getenv) }
	}
	if g.Hostname == nil {
		g.Hostname = os.Hostname
	}
	if g.ReadPassword == nil {
		g.ReadPassword = g.termPassword
	}
	if g.Editor == nil {
		g.Editor = func(ctx context.Context, path string) error {
			return exec.RunEditor(ctx, g.Getenv(exec.EditorEnv), path, os.Stdin, os.Stdout, os.Stderr)
		}
	}
	if g.DetectIP == nil {
		g.DetectIP = func(ctx context.Context) (string, bool, error) { return setup.DetectPublicIP(ctx, g.Runner) }
	}
	if g.PortBusy == nil {
		g.PortBusy = func(port int) bool {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return setup.PortBusy(ctx, g.Runner, g.Root)(port)
		}
	}
	if g.HostFacts == nil {
		g.HostFacts = func() sysinfo.Facts { return sysinfo.Collect(g.Root) }
	}
	if g.WatchInterval <= 0 {
		g.WatchInterval = DefaultWatchInterval
	}
	if g.Dial == nil {
		g.Dial = func() (api.Local, error) {
			l, err := api.Dial(g.Socket, api.DialOptions{Service: g.service()})
			if err != nil && deyerr.HasCode(err, deyerr.X003) && !g.configured() {
				return nil, deyerr.As(err).WithFix(i18n.T(i18n.CLINotSetUpFix))
			}
			return l, err
		}
	}
	if g.RunTUI == nil {
		g.RunTUI = tui.Run
	}
	if g.RunHub == nil {
		g.RunHub = hub.Run
	}
	if g.RunNode == nil {
		g.RunNode = node.Run
	}
	g.Ops.defaults()
}

func (o *Ops) defaults() {
	if o.SetupHub == nil {
		o.SetupHub = setup.SetupHub
	}
	if o.Join == nil {
		o.Join = setup.Join
	}
	if o.Uninstall == nil {
		o.Uninstall = setup.Uninstall
	}
	if o.Backup == nil {
		o.Backup = setup.Backup
	}
	if o.Restore == nil {
		o.Restore = setup.Restore
	}
	if o.SetHubAddr == nil {
		o.SetHubAddr = setup.SetHubAddr
	}
	if o.Repair == nil {
		o.Repair = setup.Repair
	}
	if o.SelfRollback == nil {
		o.SelfRollback = func(root string) error { return install.SelfUpdater{Root: root}.Rollback() }
	}
}

// rootPath joins an absolute system path below root.
func rootPath(root, p string) string {
	if root == "" || root == "/" {
		return p
	}
	return filepath.Join(root, p)
}

// path returns the system path p below g.Root.
func (g *Globals) path(p string) string { return rootPath(g.Root, p) }

// configPath is Root/etc/deyroute/config.yaml.
func (g *Globals) configPath() string { return g.path(config.DefaultPath) }

// configured reports whether this server has a config.yaml.
func (g *Globals) configured() bool {
	_, err := os.Stat(g.configPath())
	return err == nil
}

// localConfig decodes config.yaml as written (no defaults, no validation);
// nil when the server is not set up or the file is unreadable.
func (g *Globals) localConfig() *config.Config {
	data, err := os.ReadFile(g.configPath())
	if err != nil {
		return nil
	}
	c, err := config.Decode(data)
	if err != nil {
		return nil
	}
	return c
}

// role is the local config role ("hub", "node" or "" when not set up).
func (g *Globals) role() string {
	if c := g.localConfig(); c != nil {
		return c.Role
	}
	return ""
}

// service is the systemd service of the local role, named in DEY-X003.
func (g *Globals) service() string {
	if g.role() == config.RoleNode {
		return node.ServiceName
	}
	return hub.ServiceName
}

// caps returns the terminal capabilities.
func (g *Globals) caps() tui.Caps { return g.Caps() }

// outCaps is caps for colored text printed on stdout: no color when stdout
// is not a terminal, so a summary piped into a file or a message is plain
// text (section 13).
func (g *Globals) outCaps() tui.Caps {
	c := g.caps()
	if !g.OutTTY {
		c.Color = false
	}
	return c
}

// unicode reports whether UTF-8 symbols may be printed.
func (g *Globals) unicode() bool { return g.caps().Unicode }

// detectCaps inspects the environment and the terminal width.
func detectCaps(getenv func(string) string) tui.Caps {
	caps := tui.DetectCaps(getenv)
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		caps.Width = w
	} else if cols, err := strconv.Atoi(getenv("COLUMNS")); err == nil {
		caps.Width = cols
	}
	return caps
}

// local dials the daemon.
func (g *Globals) local() (api.Local, error) { return g.Dial() }

// callCtx derives the context of one simple daemon call.
func callCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, CallTimeout)
}

// longCtx derives the context of a daemon call that reports progress.
func longCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, LongCallTimeout)
}
