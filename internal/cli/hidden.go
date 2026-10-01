package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/backend/direct"
	"github.com/localroot4/deyroute/internal/backend/wireguard"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/hub"
	"github.com/localroot4/deyroute/internal/daemon/node"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tui"
)

// WGTimeout bounds `deyroute wg up|down` (interface setup through ip and
// netlink, or the amneziawg-go UAPI socket).
const WGTimeout = 2 * time.Minute

// --------------------------------------------------------------- TUI

// runTUI opens the interactive menu (spec section 6). The TUI holds no
// logic: it gets the daemon connection (or the dial error, shown as
// DEY-X003 above the menu) and the local operations of this package.
func (g *Globals) runTUI(context.Context) error {
	// The local operations below run in the TUI's goroutines: open the CLI
	// log now so they only read it.
	g.logger()
	l, err := g.local()
	o := g.tuiOptions()
	if err != nil {
		o.LocalErr = err
	} else {
		o.Local = l
	}
	o.Doctor = func(ctx context.Context) (string, string, error) {
		r, err := g.doctorRun(ctx, "", "", false)
		return r.Summary, r.Path, err
	}
	o.Backup = func(ctx context.Context, out, pass string, noEncrypt bool) (string, error) {
		path, _, _, err := g.backupRun(ctx, out, pass, noEncrypt)
		return path, err
	}
	o.Restore = func(ctx context.Context, path, pass string) error {
		info, err := inspectBackup(path, pass)
		if err != nil {
			return err
		}
		ip, err := g.restoreAddress(ctx, info, true)
		if err != nil {
			return err
		}
		_, err = g.restoreRun(ctx, path, pass, ip, nil)
		return err
	}
	o.Uninstall = func(ctx context.Context, keepBackups, nodes bool) error {
		_, err := g.uninstallRun(ctx, keepBackups, nodes, nil)
		return err
	}
	return g.RunTUI(o)
}

// tuiOptions are the TUI options without daemon and local operations: the
// banner data comes from the local config until Status() answers.
func (g *Globals) tuiOptions() tui.Options {
	o := tui.Options{Caps: g.caps(), Service: g.service(), Now: g.Now}
	if _, err := os.Stat(g.configPath()); errors.Is(err, fs.ErrNotExist) {
		o.NotSetUp = true
	}
	if c := g.localConfig(); c != nil {
		o.Status.Role = c.Role
		if c.Hub != nil {
			o.Status.Name, o.Status.PublicIP = c.Hub.Name, c.Hub.PublicIP
			o.Advanced = c.Hub.UIMode == config.UIModeAdvanced
			o.Status.Advanced = o.Advanced
			o.Status.Nodes = len(c.Nodes)
		}
		if c.Node != nil {
			o.Status.Name, o.Status.HubAddr = c.Node.ID, c.Node.HubAddr
		}
	}
	return o
}

// newMenuCmd is a hidden helper: `deyroute menu --once` prints the first
// screen without a TTY (smoke tests, scenario S24); `deyroute menu` = TUI.
func newMenuCmd(g *Globals) *cobra.Command {
	var once bool
	cmd := &cobra.Command{
		Use:    "menu",
		Short:  i18n.T(i18n.CLIMenuShort),
		Hidden: true,
		Args:   noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !once {
				return g.runTUI(cmd.Context())
			}
			g.printf("%s", tui.NewModel(g.tuiOptions()).View())
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, i18n.T(i18n.CLIFlagOnce))
	return cmd
}

// --------------------------------------------------------------- daemons

// newDaemonCmd holds `deyroute daemon hub|node`, the services' ExecStart
// (deploy/systemd). They run until SIGTERM/SIGINT.
func newDaemonCmd(g *Globals) *cobra.Command {
	cmd := newGroup("daemon", i18n.CLIDaemonShort)
	cmd.Hidden = true
	cmd.AddCommand(
		&cobra.Command{
			Use:   "hub",
			Short: i18n.T(i18n.CLIDaemonHubShort),
			Args:  noArgs(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				o := hub.Options{Root: g.Root, SocketPath: g.Socket}
				logger, closer, err := g.daemonLogger(config.RoleHub, hub.LogFile)
				if err != nil {
					return err
				}
				defer closeQuietly(closer)
				o.Logger = logger
				return g.RunHub(cmd.Context(), o)
			},
		},
		&cobra.Command{
			Use:   "node",
			Short: i18n.T(i18n.CLIDaemonNodeShort),
			Args:  noArgs(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				o := node.Options{Root: g.Root, SocketPath: g.Socket}
				logger, closer, err := g.daemonLogger(config.RoleNode, node.LogFile)
				if err != nil {
					return err
				}
				defer closeQuietly(closer)
				o.Logger = logger
				return g.RunNode(cmd.Context(), o)
			},
		},
	)
	return cmd
}

// daemonLogger builds the daemon logger with debug level for --debug; nil
// (the daemon's own default, which honours DEYROUTE_DEBUG) otherwise.
func (g *Globals) daemonLogger(component, file string) (*slog.Logger, io.Closer, error) {
	if !g.Debug {
		return nil, nil, nil
	}
	return dlog.New(dlog.Options{Component: component, File: g.path(file), Debug: true})
}

func closeQuietly(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}

// newRelayCmd is the direct/native data plane (spec section 7.8): the
// ExecStart of its deyroute-tun@ units, one relay half per side.
func newRelayCmd(g *Globals) *cobra.Command {
	var tunnel, cfgPath string
	cmd := &cobra.Command{
		Use:    "relay --tunnel <id> --config <file>",
		Short:  i18n.T(i18n.CLIRelayShort),
		Hidden: true,
		Args:   noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cfgPath == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--config"))
			}
			cfg, err := direct.LoadRelayConfig(cfgPath)
			if err != nil {
				return err
			}
			dlog.RegisterSecret(cfg.Token)
			if tunnel != "" && cfg.Tunnel != tunnel {
				return deyerr.New(deyerr.B060, deyerr.Params{"path": cfgPath, "reason": "the file belongs to tunnel " + cfg.Tunnel + ", not " + tunnel})
			}
			level := slog.LevelInfo
			if g.Debug || dlog.DebugFromEnv() {
				level = slog.LevelDebug
			}
			logger := slog.New(dlog.NewHandler(g.Err, level, "relay"))
			return direct.Run(cmd.Context(), cfg, logger)
		},
	}
	cmd.Flags().StringVar(&tunnel, "tunnel", "", i18n.T(i18n.CLIFlagRelayTunnel))
	cmd.Flags().StringVar(&cfgPath, "config", "", i18n.T(i18n.CLIFlagRelayConfig))
	return cmd
}

// newWGCmd is `deyroute wg up|down --config <wg.json>`: the oneshot units
// of the WireGuard/AmneziaWG transports.
func newWGCmd(g *Globals) *cobra.Command {
	cmd := newGroup("wg", i18n.CLIWGShort)
	cmd.Hidden = true
	for _, verb := range []string{"up", "down"} {
		var cfgPath string
		c := &cobra.Command{
			Use:   verb + " --config <file>",
			Short: i18n.T(i18n.CLIWGShort) + ": " + verb,
			Args:  noArgs(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				if cfgPath == "" {
					return usageErr(i18n.T(i18n.CLIWantFlag, "--config"))
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), WGTimeout)
				defer cancel()
				if verb == "up" {
					return wireguard.Up(ctx, cfgPath, g.Runner)
				}
				return wireguard.Down(ctx, cfgPath, g.Runner)
			},
		}
		c.Flags().StringVar(&cfgPath, "config", "", i18n.T(i18n.CLIFlagWGConfig))
		cmd.AddCommand(c)
	}
	return cmd
}

// ------------------------------------------------------------ completion

func newCompletionCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:       "completion bash|zsh|fish",
		Short:     i18n.T(i18n.CLICompletionShort),
		Long:      i18n.T(i18n.CLICompletionLong),
		Example:   i18n.T(i18n.CLICompletionExample),
		Args:      exactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			root := cmd.Root()
			var err error
			switch args[0] {
			case "bash":
				if err = root.GenBashCompletionV2(g.Out, true); err == nil {
					// The short name `dey` completes the same way.
					g.println("complete -o default -F __start_deyroute dey")
				}
			case "zsh":
				if err = root.GenZshCompletion(g.Out); err == nil {
					g.println("compdef _deyroute dey")
				}
			case "fish":
				if err = root.GenFishCompletion(g.Out, true); err == nil {
					g.println("complete -c dey -w deyroute")
				}
			default:
				return usageErr(i18n.T(i18n.CLIWantShell, args[0]))
			}
			if err != nil {
				return deyerr.Wrap(deyerr.X000, err, nil)
			}
			return nil
		},
	}
}
