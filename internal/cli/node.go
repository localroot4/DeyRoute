package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
)

// DefaultJoinTTL is the validity of a join command (spec section 3).
const DefaultJoinTTL = 15 * time.Minute

func newNodeCmd(g *Globals) *cobra.Command {
	cmd := newGroup("node", i18n.CLINodeShort)
	cmd.AddCommand(newNodeJoinCommandCmd(g), newNodeListCmd(g), newNodeRenameCmd(g), newNodeRemoveCmd(g),
		newNodeTestCmd(g), newNodeSetHubCmd(g))
	return cmd
}

func newNodeJoinCommandCmd(g *Globals) *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:     "join-command",
		Short:   i18n.T(i18n.CLINodeJoinCmdShort),
		Example: i18n.T(i18n.CLINodeJoinCmdExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if ttl < api.MinJoinTTL || ttl > api.MaxJoinTTL {
				return usageErr(i18n.T(i18n.CLIJoinTTLRange, ttl))
			}
			jc, err := g.joinCommand(cmd.Context(), ttl)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(jc)
			}
			g.printJoinCommand(jc)
			return nil
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", DefaultJoinTTL, i18n.T(i18n.CLIFlagTTL))
	return cmd
}

func newNodeListCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   i18n.T(i18n.CLINodeListShort),
		Example: i18n.T(i18n.CLINodeListExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var ns []api.NodeInfo
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				ns, err = l.NodeList(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"nodes": nonNil(ns)})
			}
			if len(ns) == 0 {
				g.say(i18n.CLINodeListEmpty)
				return nil
			}
			rows := make([][]string, 0, len(ns))
			s := g.sym()
			for _, n := range ns {
				st := s.up + " " + i18n.T(i18n.TUIOnline)
				if !n.Online {
					st = s.down + " " + i18n.T(i18n.TUIOffline)
				}
				ver := orDash(n.Version)
				if n.Version != "" && !n.Compatible {
					ver += " " + i18n.T(i18n.CLIIncompatible)
				}
				rtt := orDash("")
				if n.Online {
					rtt = ms(n.ControlRTTms)
				}
				rows = append(rows, []string{n.ID, orDash(n.Name), orDash(n.PublicIP), st, rtt, ver,
					fmt.Sprintf("%.0f%%", n.CPUPercent), fmt.Sprintf("%dMB", n.RAMBytes/(1<<20)), orDash(strings.Join(n.Tunnels, ","))})
			}
			g.table([]string{i18n.T(i18n.CLIColID), i18n.T(i18n.TUIColName), i18n.T(i18n.CLIColPublicIP), i18n.T(i18n.TUIColState),
				i18n.T(i18n.CLIColCtl), i18n.T(i18n.CLIColVersion), i18n.T(i18n.CLIColCPU), i18n.T(i18n.CLIColRAM), i18n.T(i18n.CLIColTunnels)}, rows)
			return nil
		},
	}
}

func newNodeRenameCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "rename <id> <name>",
		Short:   i18n.T(i18n.CLINodeRenameShort),
		Example: i18n.T(i18n.CLINodeRenameExample),
		Args:    exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkName(args[1]); err != nil {
				return err
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.NodeRename(ctx, args[0], args[1])
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"node": args[0], "name": args[1]}, i18n.CLINodeRenamed, args[0], args[1])
		},
	}
}

func newNodeRemoveCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "remove <id>",
		Short:   i18n.T(i18n.CLINodeRemoveShort),
		Example: i18n.T(i18n.CLINodeRemoveExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			l, err := g.local()
			if err != nil {
				return err
			}
			if err := g.confirm(i18n.T(i18n.CLINodeRemoveLost, id), yes); err != nil {
				return err
			}
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			if err := l.NodeRemove(ctx, id); err != nil {
				return err
			}
			return g.done(map[string]any{"node": id}, i18n.CLINodeRemoved, id)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newNodeTestCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "test <id>",
		Short:   i18n.T(i18n.CLINodeTestShort),
		Example: i18n.T(i18n.CLINodeTestExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var r api.NodeTestResult
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.NodeTest(ctx, args[0])
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(r)
			}
			s := g.sym()
			online := s.up + " " + i18n.T(i18n.TUIOnline)
			if !r.Online {
				online = s.down + " " + i18n.T(i18n.TUIOffline)
			}
			g.say(i18n.CLINodeTestTitle, r.Node, online)
			g.say(i18n.CLINodeTestCtl, ms(r.ControlRTTms))
			if r.UDPOK {
				g.say(i18n.CLINodeTestUDPOK, ms(r.UDPRTTms))
			} else {
				g.say(i18n.CLINodeTestUDPBlocked)
			}
			if len(r.SysInfo) > 0 {
				g.say(i18n.CLINodeTestSysinfo)
				keys := make([]string, 0, len(r.SysInfo))
				kw := 0
				for k := range r.SysInfo {
					keys = append(keys, k)
					kw = max(kw, width(k))
				}
				sort.Strings(keys)
				for _, k := range keys {
					g.println(g.text("    " + pad(k+":", kw+2) + clean(r.SysInfo[k])))
				}
			}
			return nil
		},
	}
}

func newNodeSetHubCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "set-hub <ip:port>",
		Short:   i18n.T(i18n.CLINodeSetHubShort),
		Example: i18n.T(i18n.CLINodeSetHubExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := strings.TrimSpace(args[0])
			if !config.ValidHostPort(addr) {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "node.hub_addr", "value": addr, "allowed": "ip:port"})
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.NodeSetHub(ctx, addr)
			})
			if deyerr.HasCode(err, deyerr.X003) && g.role() == config.RoleNode {
				// The daemon is down: write node.hub_addr directly; the
				// agent reads it when it starts.
				if err := g.Ops.SetHubAddr(g.Root, addr); err != nil {
					return err
				}
				return g.done(map[string]any{"hub_addr": addr, "daemon_running": false}, i18n.CLINodeSetHubOffline, addr, g.service())
			}
			if err != nil {
				return err
			}
			return g.done(map[string]any{"hub_addr": addr, "daemon_running": true}, i18n.CLINodeSetHubDone, addr)
		},
	}
}

func newHubCmd(g *Globals) *cobra.Command {
	cmd := newGroup("hub", i18n.CLIHubShort)
	cmd.AddCommand(&cobra.Command{
		Use:     "announce-move <ip:port>",
		Short:   i18n.T(i18n.CLIHubAnnounceShort),
		Long:    i18n.T(i18n.CLIHubAnnounceLong),
		Example: i18n.T(i18n.CLIHubAnnounceExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := strings.TrimSpace(args[0])
			if !config.ValidHostPort(addr) {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "hub address", "value": addr, "allowed": "ip:port"})
			}
			var r api.AnnounceResult
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.HubAnnounceMove(ctx, addr)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"addr": addr, "accepted": nonNil(r.Accepted), "offline": nonNil(r.Offline)})
			}
			g.say(i18n.CLIHubAnnounced, addr, orDash(strings.Join(r.Accepted, ", ")))
			if len(r.Offline) > 0 {
				g.say(i18n.CLIHubAnnounceOffline, strings.Join(r.Offline, ", "), addr)
			}
			return nil
		},
	})
	return cmd
}

// call dials the daemon and runs one simple Local API call with the
// default timeout.
func (g *Globals) call(ctx context.Context, fn func(ctx context.Context, l api.Local) error) error {
	l, err := g.local()
	if err != nil {
		return err
	}
	cctx, cancel := callCtx(ctx)
	defer cancel()
	return fn(cctx, l)
}

// callLong is call for operations that report progress (1 hour limit).
func (g *Globals) callLong(ctx context.Context, fn func(ctx context.Context, l api.Local) error) error {
	l, err := g.local()
	if err != nil {
		return err
	}
	cctx, cancel := longCtx(ctx)
	defer cancel()
	return fn(cctx, l)
}

// nonNil turns a nil slice into an empty one so --json prints [] not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// newGroup returns a command that only holds subcommands: without one it
// prints its help, an unknown one is a usage error.
func newGroup(use string, short i18n.Key) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: i18n.T(short),
		Args:  noArgs(),
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
}

// noArgs rejects positional arguments.
func noArgs() cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usageErr(i18n.T(i18n.CLIUnknownSub, args[0], cmd.CommandPath()))
		}
		return nil
	}
}
