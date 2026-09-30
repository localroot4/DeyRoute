package cli

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/hub"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
)

// Defaults of the diagnostics commands.
const (
	DefaultSpeedSeconds = 10
	MaxSpeedSeconds     = 120
	DefaultEventsSince  = 24 * time.Hour
)

func newDiagCmd(g *Globals) *cobra.Command {
	cmd := newGroup("diag", i18n.CLIDiagShort)
	var seconds int
	speed := &cobra.Command{
		Use:     "speed <tunnel>",
		Short:   i18n.T(i18n.CLIDiagSpeedShort),
		Example: i18n.T(i18n.CLIDiagSpeedExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if seconds < 1 || seconds > MaxSpeedSeconds {
				return usageErr(i18n.T(i18n.CLIWantSeconds, MaxSpeedSeconds))
			}
			p := g.newProgress()
			var r api.SpeedResult
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.DiagSpeed(ctx, args[0], seconds, p.step)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"result": r, "steps": p.steps})
			}
			g.say(i18n.CLISpeedResult, args[0], fmt.Sprintf("%.1f", r.DownloadMbps), fmt.Sprintf("%.1f", r.UploadMbps),
				ms(r.RTTms), orDash(r.Transport), fmt.Sprintf("%.0f", r.Seconds))
			return nil
		},
	}
	speed.Flags().IntVar(&seconds, "seconds", DefaultSpeedSeconds, i18n.T(i18n.CLIFlagSeconds))
	var allPorts bool
	probe := &cobra.Command{
		Use:     "probe <tunnel>",
		Short:   i18n.T(i18n.CLIDiagProbeShort),
		Example: i18n.T(i18n.CLIDiagProbeExample),
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var rs []api.ProbeReport
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				rs, err = l.DiagProbe(ctx, args[0], allPorts)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"tunnel": args[0], "probes": nonNil(rs)})
			}
			s := g.sym()
			rows := make([][]string, 0, len(rs))
			for _, r := range rs {
				res, rtt := s.ok+" "+i18n.T(i18n.CLIResultOK), ms(r.RTTms)
				if !r.OK {
					res, rtt = s.fail+" "+i18n.T(i18n.CLIResultFailed), orDash("")
				}
				rows = append(rows, []string{strconv.Itoa(r.Port) + "/" + r.Proto, orDash(r.Kind), res, rtt, clean(r.Error)})
			}
			g.table([]string{i18n.T(i18n.CLIColPort), i18n.T(i18n.CLIColKind), i18n.T(i18n.CLIColResult),
				i18n.T(i18n.TUIColRTT), i18n.T(i18n.CLIColDetail)}, rows)
			return nil
		},
	}
	probe.Flags().BoolVar(&allPorts, "all-ports", false, i18n.T(i18n.CLIFlagAllPorts))
	cmd.AddCommand(speed, probe)
	return cmd
}

func newLogsCmd(g *Globals) *cobra.Command {
	var follow bool
	var since time.Duration
	cmd := &cobra.Command{
		Use:     "logs [<tunnel>|hub|node]",
		Short:   i18n.T(i18n.CLILogsShort),
		Long:    i18n.T(i18n.CLILogsLong),
		Example: i18n.T(i18n.CLILogsExample),
		Args:    rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := api.LogQuery{Follow: follow}
			switch {
			case len(args) == 1:
				q.Target = args[0]
			case g.role() == config.RoleNode:
				q.Target = hub.LogTargetNode
			default:
				q.Target = hub.LogTargetHub
			}
			if since < 0 {
				return usageErr(i18n.T(i18n.CLIWantPositiveDuration, "--since"))
			}
			if since > 0 {
				q.Since = g.Now().Add(-since).UTC()
			}
			l, err := g.local()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			if !follow {
				ctx, cancel = callCtx(ctx)
				defer cancel()
			}
			err = l.Logs(ctx, q, func(line api.LogLine) error {
				if g.JSON {
					return g.emitJSONLine(line)
				}
				g.println(g.text("[" + line.Source + "] " + clean(line.Line)))
				return nil
			})
			if follow && cmd.Context().Err() != nil {
				return nil // Ctrl-C ends a followed stream
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, i18n.T(i18n.CLIFlagFollow))
	cmd.Flags().DurationVar(&since, "since", 0, i18n.T(i18n.CLIFlagLogsSince))
	return cmd
}

func newEventsCmd(g *Globals) *cobra.Command {
	var tunnel string
	var since time.Duration
	cmd := &cobra.Command{
		Use:     "events",
		Short:   i18n.T(i18n.CLIEventsShort),
		Example: i18n.T(i18n.CLIEventsExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if since < 0 {
				return usageErr(i18n.T(i18n.CLIWantPositiveDuration, "--since"))
			}
			q := api.EventQuery{Tunnel: tunnel}
			if since > 0 {
				q.Since = g.Now().Add(-since).UTC()
			}
			var evs []state.Event
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				evs, err = l.Events(ctx, q)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"events": nonNil(evs)})
			}
			if len(evs) == 0 {
				g.say(i18n.TUIDashNoEvents)
				return nil
			}
			rows := make([][]string, 0, len(evs))
			for _, e := range evs {
				rows = append(rows, []string{localTime(e.At, "2006-01-02 15:04:05"), e.Level, e.Type, eventWho(e), eventMessage(e), orDash(e.Code)})
			}
			g.table([]string{i18n.T(i18n.CLIColTime), i18n.T(i18n.CLIColLevel), i18n.T(i18n.CLIColType),
				i18n.T(i18n.CLIColWho), i18n.T(i18n.CLIColMessage), i18n.T(i18n.CLIColCode)}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&tunnel, "tunnel", "", i18n.T(i18n.CLIFlagEventsTunnel))
	cmd.Flags().DurationVar(&since, "since", DefaultEventsSince, i18n.T(i18n.CLIFlagEventsSince))
	return cmd
}

// errDaemonDown reports whether err means the daemon is not running.
func errDaemonDown(err error) bool { return deyerr.HasCode(err, deyerr.X003) }
