package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// newUpdateAutoCmd is `deyroute update auto [on|off]`.
func newUpdateAutoCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:       "auto [on|off]",
		Short:     i18n.T(i18n.CLIUpdateAutoShort),
		Long:      i18n.T(i18n.CLIUpdateAutoLong),
		Example:   i18n.T(i18n.CLIUpdateAutoExample),
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := ""
			if len(args) == 1 {
				mode = strings.ToLower(strings.TrimSpace(args[0]))
				if mode != "on" && mode != "off" {
					return usageErr(i18n.T(i18n.CLIUpdateAutoExample))
				}
			}
			var info api.AutoUpdateInfo
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				info, err = l.UpdateAuto(ctx, mode)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(info)
			}
			switch mode {
			case "on":
				g.println(g.styleOut(styleGreen, g.text(i18n.T(i18n.CLIAutoTurnedOn))))
			case "off":
				g.println(g.styleOut(styleYellow, g.text(i18n.T(i18n.CLIAutoTurnedOff))))
			}
			g.printAutoUpdate(info)
			return nil
		},
	}
}

// printAutoUpdate shows the automatic update as a titled section.
func (g *Globals) printAutoUpdate(info api.AutoUpdateInfo) {
	status := g.styleOut(styleGreen, i18n.T(i18n.CLIAutoOn))
	if !info.Enabled {
		status = g.styleOut(styleYellow, i18n.T(i18n.CLIAutoOff))
	}
	next := i18n.T(i18n.CLIAutoNextNone)
	if info.Next != "" {
		next = i18n.T(i18n.CLIAutoNextValue, info.Next, info.NextAt.Local().Format("2006-01-02 15:04"))
	}
	last := info.Last
	if last != "" && !info.LastAt.IsZero() {
		last += " (" + info.LastAt.Local().Format("2006-01-02 15:04") + ")"
	}
	verifying := ""
	if info.Pending != "" {
		verifying = i18n.T(i18n.CLIAutoVerifyingValue, info.Pending)
	}
	rows := [][2]string{
		{i18n.T(i18n.CLIAutoStatus), status},
		{i18n.T(i18n.CLIAutoWhen), i18n.T(i18n.CLIAutoWhenValue, info.Hour, info.MinAgeHours)},
		{i18n.T(i18n.CLIAutoRunning), info.Current},
		{i18n.T(i18n.CLIAutoNext), next},
		{i18n.T(i18n.CLIAutoVerifying), verifying},
		{i18n.T(i18n.CLIAutoLast), last},
		{i18n.T(i18n.CLIAutoSkipped), strings.Join(info.Skipped, ", ")},
	}
	g.println(g.sectionHead(i18n.T(i18n.CLIAutoTitle), ""))
	for _, l := range kvWrapLines("  ", rows, g.lineWidth()) {
		g.println(g.text(l))
	}
	g.println()
	for _, l := range wrapText(i18n.T(i18n.CLIAutoSafety), g.lineWidth()-2) {
		g.println("  " + g.styleOut(styleGray, g.text(l)))
	}
	hint := i18n.T(i18n.CLIAutoHintOff)
	if !info.Enabled {
		hint = i18n.T(i18n.CLIAutoHintOn)
	}
	g.println("  " + g.styleOut(styleGray, g.text(hint)))
}
