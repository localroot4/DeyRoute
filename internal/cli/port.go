package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/ports"
)

// DefaultSuggestCount is `deyroute port suggest` without --count.
const DefaultSuggestCount = 3

func newPortCmd(g *Globals) *cobra.Command {
	cmd := newGroup("port", i18n.CLIPortShort)
	cmd.AddCommand(newPortAddCmd(g), newPortRemoveCmd(g), newPortCheckCmd(g), newPortSuggestCmd(g))
	return cmd
}

// onePort parses "8443" or "8443/udp" (exactly one port, no target).
func onePort(s string) (ports.Spec, error) {
	specs, err := ports.ParseInput(s)
	if err != nil {
		return ports.Spec{}, err
	}
	if len(specs) != 1 || strings.Contains(s, ":") {
		return ports.Spec{}, deyerr.New(deyerr.C020, deyerr.Params{"input": s})
	}
	return specs[0], nil
}

func newPortAddCmd(g *Globals) *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:     "add <tunnel> 8443[/tcp]",
		Short:   i18n.T(i18n.CLIPortAddShort),
		Long:    i18n.T(i18n.CLIPortAddLong),
		Example: i18n.T(i18n.CLIPortAddExample),
		Args:    exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			specs, err := ports.ParseInput(args[1])
			if err != nil {
				return err
			}
			if t := strings.TrimSpace(target); t != "" {
				if len(specs) != 1 {
					return usageErr(i18n.T(i18n.CLITargetOnePort))
				}
				if !config.ValidHostPort(t) {
					return deyerr.New(deyerr.C004, deyerr.Params{"target": t, "tunnel": args[0]})
				}
				specs[0].Target = t
			}
			if !g.JSON {
				g.say(i18n.CLIPortRestartNote, args[0])
			}
			p := g.newProgress()
			var t api.TunnelInfo
			err = g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				t, err = l.PortAdd(ctx, args[0], portSpecs(specs), p.step)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnel": t, "steps": p.steps})
			}
			for _, s := range specs {
				tg := s.Target
				if tg == "" {
					tg = ports.DefaultTarget(s.Listen)
				}
				g.say(i18n.CLIPortAdded, ports.FormatSpec(ports.Spec{Listen: s.Listen, Proto: s.Proto}), args[0], tg)
			}
			g.printTunnelResult(t)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", i18n.T(i18n.CLIFlagTarget))
	return cmd
}

func newPortRemoveCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "remove <tunnel> 8443[/tcp]",
		Short:   i18n.T(i18n.CLIPortRemoveShort),
		Example: i18n.T(i18n.CLIPortRemoveExample),
		Args:    exactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := onePort(args[1])
			if err != nil {
				return err
			}
			if !g.JSON {
				g.say(i18n.CLIPortRestartNote, args[0])
			}
			var t api.TunnelInfo
			err = g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				t, err = l.PortRemove(ctx, args[0], s.Listen, s.Proto)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnel": t})
			}
			g.say(i18n.CLIPortRemoved, ports.FormatSpec(ports.Spec{Listen: s.Listen, Proto: s.Proto}), args[0])
			return nil
		},
	}
}

// maxOpenRounds bounds `port check --open`: each round opens the port in
// one more external firewall that blocks it (ufw, firewalld, iptables and
// other nftables tables).
const maxOpenRounds = 4

// portCheckOpened is the --json document of `port check --open`: the port
// check and what PortOpenFirewall ran last (absent when nothing blocked).
type portCheckOpened struct {
	api.PortCheckResult
	Opened *api.PortOpenResult `json:"opened,omitempty"`
}

func newPortCheckCmd(g *Globals) *cobra.Command {
	var node string
	var open, yes bool
	cmd := &cobra.Command{
		Use:     "check 443[/tcp]",
		Short:   i18n.T(i18n.CLIPortCheckShort),
		Long:    i18n.T(i18n.CLIPortCheckLong),
		Example: i18n.T(i18n.CLIPortCheckExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := onePort(args[0])
			if err != nil {
				return err
			}
			req := api.PortCheckRequest{Port: s.Listen, Proto: s.Proto, Node: strings.TrimSpace(node)}
			var r api.PortCheckResult
			err = g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.PortCheck(ctx, req)
				return err
			})
			if err != nil {
				return err
			}
			if !g.JSON {
				g.printPortCheck(r, !open)
			}
			if !open {
				if g.JSON {
					return g.emitJSON(r)
				}
				return nil
			}
			opened, err := g.openFirewall(cmd.Context(), r, yes)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(portCheckOpened{PortCheckResult: r, Opened: opened})
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&node, "node", "", i18n.T(i18n.CLIFlagCheckNode))
	cmd.Flags().BoolVar(&open, "open", false, i18n.T(i18n.CLIFlagOpenFirewall))
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

// openFirewall is `port check --open` (section 10): for each external
// firewall that blocks the port, the exact command is shown and runs only
// after a confirmation (none with yes; exit 3 without a terminal). The
// daemon runs it only when a fresh check builds the same command
// (DEY-P032). It returns the last PortOpenFirewall result (nil when nothing
// blocked the port); a port still closed after maxOpenRounds is DEY-P013.
func (g *Globals) openFirewall(ctx context.Context, r api.PortCheckResult, yes bool) (*api.PortOpenResult, error) {
	spec := fmt.Sprintf("%d/%s", r.Port, r.Proto)
	if r.FirewallOpen {
		if !g.JSON {
			g.say(i18n.CLIPortOpenNothing, spec)
		}
		return nil, nil
	}
	var last *api.PortOpenResult
	name, command := r.FirewallName, r.FirewallCommand
	for range maxOpenRounds {
		// Without a command there is nothing to confirm: the daemon then
		// explains why it cannot open the port (DEY-P033).
		if command != "" {
			if err := g.confirm(i18n.T(i18n.CLIPortOpenLost, spec, name, command), yes); err != nil {
				return last, err
			}
		}
		var res api.PortOpenResult
		err := g.callLong(ctx, func(ctx context.Context, l api.Local) (err error) {
			res, err = l.PortOpenFirewall(ctx, api.PortOpenRequest{Port: r.Port, Proto: r.Proto, Command: command})
			return err
		})
		if err != nil {
			return last, err
		}
		last = &res
		if !g.JSON {
			if res.Ran != "" {
				g.say(i18n.CLIPortOpenRan, res.Ran)
			}
			if res.FirewallOpen {
				g.say(i18n.CLIPortOpenNow, spec, orDash(res.FirewallName))
			} else {
				g.say(i18n.CLIPortOpenStill, spec, orDash(res.FirewallName))
			}
		}
		if res.FirewallOpen {
			return last, nil
		}
		name, command = res.FirewallName, res.FirewallCommand
	}
	return last, deyerr.New(deyerr.P013, deyerr.Params{"port": spec, "firewall": name, "command": command})
}

// printPortCheck prints the four-stage check of sections 6 and 10; hint
// adds how to let deyroute open a port an external firewall closes.
func (g *Globals) printPortCheck(r api.PortCheckResult, hint bool) {
	s := g.sym()
	g.say(i18n.CLIPortCheckTitle, fmt.Sprintf("%d/%s", r.Port, r.Proto))
	line := func(n int, k i18n.Key, mark, text string) {
		g.println(g.text(fmt.Sprintf("  %d. %s %s %s", n, pad(i18n.T(k), 21), mark, text)))
	}
	// 1. local bind
	if r.BindFree {
		line(1, i18n.CLICheckBind, s.ok, i18n.T(i18n.CLICheckFree))
	} else {
		who := orDash(clean(r.BindProcess))
		txt := i18n.T(i18n.CLICheckUsedBy, who)
		if r.BindAddr != "" {
			txt += " " + i18n.T(i18n.CLICheckOnAddr, r.BindAddr)
		}
		if r.BindByDey {
			txt += " (" + i18n.T(i18n.CLICheckDeyroute) + ")"
		}
		line(1, i18n.CLICheckBind, s.fail, txt)
	}
	// 2. firewall
	fw := orDash(r.FirewallName)
	if r.FirewallOpen {
		line(2, i18n.CLICheckFirewall, s.ok, i18n.T(i18n.CLICheckOpen, fw))
	} else {
		txt := i18n.T(i18n.CLICheckClosed, fw)
		if r.FirewallCommand != "" {
			txt += "; " + i18n.T(i18n.CLICheckOpenWith, r.FirewallCommand)
		}
		line(2, i18n.CLICheckFirewall, s.fail, txt)
	}
	// 3. reachable from node
	switch {
	case r.NodeReachable == nil:
		line(3, i18n.CLICheckFromNode, s.skip, i18n.T(i18n.CLICheckNotTested))
	case *r.NodeReachable:
		line(3, i18n.CLICheckFromNode, s.ok, i18n.T(i18n.CLICheckYes, orDash(r.Node), ms(r.NodeRTTms)))
	default:
		line(3, i18n.CLICheckFromNode, s.fail, i18n.T(i18n.CLICheckNo, orDash(r.Node)))
	}
	// 4. reachable via tunnel
	switch {
	case r.TunnelOK == nil:
		line(4, i18n.CLICheckViaTunnel, s.skip, i18n.T(i18n.CLICheckNoTunnel))
	case *r.TunnelOK:
		line(4, i18n.CLICheckViaTunnel, s.ok, i18n.T(i18n.CLICheckYes, orDash(r.Tunnel), ms(r.TunnelRTTms)))
	default:
		line(4, i18n.CLICheckViaTunnel, s.fail, i18n.T(i18n.CLICheckNo, orDash(r.Tunnel)))
	}
	note := r.Note
	if note == "" {
		note = i18n.T(i18n.CLICheckNote)
	}
	g.println(g.text("  " + clean(note)))
	if len(r.SuggestedPorts) > 0 {
		g.say(i18n.CLISuggestedPorts, joinInts(r.SuggestedPorts))
	}
	if hint && !r.FirewallOpen && r.FirewallCommand != "" {
		g.say(i18n.CLICheckOpenHint, fmt.Sprintf("%d/%s", r.Port, r.Proto))
	}
}

// joinInts renders "443, 2053, 2083".
func joinInts(ns []int) string {
	s := make([]string, 0, len(ns))
	for _, n := range ns {
		s = append(s, strconv.Itoa(n))
	}
	return strings.Join(s, ", ")
}

func newPortSuggestCmd(g *Globals) *cobra.Command {
	var count int
	cmd := &cobra.Command{
		Use:     "suggest",
		Short:   i18n.T(i18n.CLIPortSuggestShort),
		Example: i18n.T(i18n.CLIPortSuggestExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if count < 1 || count > 64 {
				return usageErr(i18n.T(i18n.CLIWantCount, 64))
			}
			var ps []int
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				ps, err = l.PortSuggest(ctx, count)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"ports": nonNil(ps)})
			}
			g.say(i18n.CLISuggestedPorts, joinInts(ps))
			return nil
		},
	}
	cmd.Flags().IntVar(&count, "count", DefaultSuggestCount, i18n.T(i18n.CLIFlagCount))
	return cmd
}

func newLadderCmd(g *Globals) *cobra.Command {
	cmd := newGroup("ladder", i18n.CLILadderShort)
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Short:   i18n.T(i18n.CLILadderListShort),
			Example: i18n.T(i18n.CLILadderListExample),
			Args:    noArgs(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				ls, err := g.ladders(cmd.Context())
				if err != nil {
					return err
				}
				if g.JSON {
					return g.emitJSON(map[string]any{"ladders": nonNil(ls)})
				}
				rows := make([][]string, 0, len(ls))
				for _, l := range ls {
					kind := i18n.T(i18n.CLILadderCustom)
					if l.Builtin {
						kind = i18n.T(i18n.CLILadderBuiltin)
					}
					rows = append(rows, []string{l.Name, kind, orDash(strings.Join(l.UsedBy, ",")), strings.Join(l.Rungs, ", ")})
				}
				g.table([]string{i18n.T(i18n.TUIColName), i18n.T(i18n.CLIColKind), i18n.T(i18n.CLIColUsedBy), i18n.T(i18n.CLIColRungs)}, rows)
				return nil
			},
		},
		&cobra.Command{
			Use:     "show <name>",
			Short:   i18n.T(i18n.CLILadderShowShort),
			Example: i18n.T(i18n.CLILadderShowExample),
			Args:    exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				ls, err := g.ladders(cmd.Context())
				if err != nil {
					return err
				}
				for _, l := range ls {
					if l.Name != args[0] {
						continue
					}
					if g.JSON {
						return g.emitJSON(l)
					}
					kind := i18n.T(i18n.CLILadderCustom)
					if l.Builtin {
						kind = i18n.T(i18n.CLILadderBuiltin)
					}
					g.say(i18n.CLILadderTitle, l.Name, kind, orDash(strings.Join(l.UsedBy, ", ")))
					for i, r := range l.Rungs {
						g.printf("  %2d) %s\n", i+1, r)
					}
					return nil
				}
				e := deyerr.New(deyerr.C012, deyerr.Params{"ladder": args[0], "tunnel": "-"})
				e.MsgOverride = i18n.T(i18n.CLIUnknownLadder, args[0])
				return e
			},
		},
		newLadderSaveCmd(g, true), newLadderSaveCmd(g, false),
		&cobra.Command{
			Use:     "delete <name>",
			Short:   i18n.T(i18n.CLILadderDeleteShort),
			Example: i18n.T(i18n.CLILadderDeleteExample),
			Args:    exactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.LadderDelete(ctx, args[0]) })
				if err != nil {
					return err
				}
				return g.done(map[string]any{"ladder": args[0]}, i18n.CLILadderDeleted, args[0])
			},
		},
	)
	return cmd
}

// ladders is one LadderList call.
func (g *Globals) ladders(ctx context.Context) ([]api.Ladder, error) {
	var ls []api.Ladder
	err := g.call(ctx, func(ctx context.Context, l api.Local) (err error) {
		ls, err = l.LadderList(ctx)
		return err
	})
	return ls, err
}

// newLadderSaveCmd builds `ladder create` (create) and `ladder set`.
func newLadderSaveCmd(g *Globals, create bool) *cobra.Command {
	var rungs []string
	use, short, example, doneKey := "create <name> --rungs a,b,c", i18n.CLILadderCreateShort, i18n.CLILadderCreateExample, i18n.CLILadderCreated
	if !create {
		use, short, example, doneKey = "set <name> --rungs a,b,c", i18n.CLILadderSetShort, i18n.CLILadderSetExample, i18n.CLILadderSaved
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var list []string
			for _, r := range rungs {
				list = append(list, splitList(r)...)
			}
			if len(list) == 0 {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--rungs"))
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.LadderSave(ctx, args[0], list, create)
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"ladder": args[0], "rungs": list}, doneKey, args[0], strings.Join(list, ", "))
		},
	}
	cmd.Flags().StringArrayVar(&rungs, "rungs", nil, i18n.T(i18n.CLIFlagRungs))
	return cmd
}
