package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	"github.com/localroot4/deyroute/internal/i18n"
)

func newUninstallCmd(g *Globals) *cobra.Command {
	var keep, nodes, yes bool
	cmd := &cobra.Command{
		Use:     "uninstall",
		Short:   i18n.T(i18n.CLIUninstallShort),
		Long:    i18n.T(i18n.CLIUninstallLong),
		Example: i18n.T(i18n.CLIUninstallExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), LocalOpTimeout)
			defer cancel()
			fl := cmd.Flags()
			isHub := g.role() == config.RoleHub
			if g.IsTTY && !yes {
				var err error
				if !fl.Changed("keep-backups") {
					if keep, err = g.askYesNo(i18n.T(i18n.CLIAskKeepBackups), true); err != nil {
						return err
					}
				}
				if isHub && !fl.Changed("nodes") {
					if nodes, err = g.askYesNo(i18n.T(i18n.CLIAskUninstallNodes), false); err != nil {
						return err
					}
				}
			}
			if err := g.confirm(uninstallLost(keep, nodes && isHub), yes); err != nil {
				return err
			}
			p := g.newProgress()
			removed, err := g.uninstallRun(ctx, keep, nodes, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.ok(map[string]any{"kept_backups": keep, "nodes": nonNil(removed)}, p.steps)
			}
			if len(removed) > 0 {
				g.say(i18n.CLINodesUninstalled, strings.Join(removed, ", "))
			}
			g.println()
			g.say(i18n.CLIUninstalled)
			if keep {
				g.say(i18n.CLIBackupsKept, config.BackupDir)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&keep, "keep-backups", false, i18n.T(i18n.CLIFlagKeepBackups))
	f.BoolVar(&nodes, "nodes", false, i18n.T(i18n.CLIFlagUninstallNodes))
	f.BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

// uninstallLost lists exactly what uninstall removes (spec section 5).
func uninstallLost(keepBackups, nodes bool) string {
	backups := i18n.T(i18n.CLIUninstallBackupsGone)
	if keepBackups {
		backups = i18n.T(i18n.CLIUninstallBackupsKept, config.BackupDir)
	}
	text := i18n.T(i18n.CLIUninstallLost, backups)
	if nodes {
		text += "\n" + i18n.T(i18n.CLIUninstallNodesToo)
	}
	return text
}

// uninstallRun removes deyroute from this server (shared by the CLI and the
// TUI). With nodes on a hub the online nodes are uninstalled first through
// the daemon (it is gone afterwards); it returns their ids.
func (g *Globals) uninstallRun(ctx context.Context, keepBackups, nodes bool, progress func(api.Step)) ([]string, error) {
	var removed []string
	if nodes && g.role() == config.RoleHub {
		l, err := g.local()
		if err != nil {
			return nil, err
		}
		cctx, cancel := longCtx(ctx)
		removed, err = l.UninstallNodes(cctx)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	err := g.Ops.Uninstall(ctx, setup.UninstallOptions{
		Root: g.Root, Runner: g.Runner, KeepBackups: keepBackups, Progress: progress, Logger: g.logger(),
	})
	return removed, err
}
