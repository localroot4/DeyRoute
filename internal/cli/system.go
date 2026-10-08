package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/tui"
	"github.com/localroot4/deyroute/internal/version"
)

// --------------------------------------------------------------- optimize

func newOptimizeCmd(g *Globals) *cobra.Command {
	cmd := newGroup("optimize", i18n.CLIOptimizeShort)
	var profile string
	apply := &cobra.Command{
		Use:     "apply --profile auto|balanced|aggressive|off",
		Short:   i18n.T(i18n.CLIOptimizeApplyShort),
		Long:    i18n.T(i18n.CLIOptimizeApplyLong) + "\n" + i18n.T(i18n.CLIOptimizeApplyAutoAlias),
		Example: i18n.T(i18n.CLIOptimizeApplyExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			switch profile {
			case config.SysctlBalanced, config.SysctlAggressive, config.SysctlOff:
			case config.SysctlAuto:
				// The confirmation is the command itself (section 12:
				// "apply --profile" never asks).
				return g.optimizeAuto(cmd.Context(), autoFlags{yes: true})
			case "":
				return usageErr(i18n.T(i18n.CLIWantFlag, "--profile"))
			default:
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "--profile", "value": profile, "allowed": "auto, balanced, aggressive, off"})
			}
			var st api.OptimizeStatus
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				st, err = l.OptimizeApply(ctx, profile)
				return err
			})
			if err != nil {
				return err
			}
			return g.printOptimize(st, i18n.CLIOptimizeApplied)
		},
	}
	apply.Flags().StringVar(&profile, "profile", "", i18n.T(i18n.CLIFlagProfile))
	revert := &cobra.Command{
		Use:     "revert",
		Short:   i18n.T(i18n.CLIOptimizeRevertShort),
		Example: i18n.T(i18n.CLIOptimizeRevertExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st api.OptimizeStatus
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				st, err = l.OptimizeRevert(ctx)
				return err
			})
			if err != nil {
				return err
			}
			return g.printOptimize(st, i18n.CLIOptimizeReverted)
		},
	}
	status := &cobra.Command{
		Use:     "status",
		Short:   i18n.T(i18n.CLIOptimizeStatusShort),
		Example: i18n.T(i18n.CLIOptimizeStatusExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var st api.OptimizeStatus
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				st, err = l.OptimizeStatus(ctx)
				return err
			})
			if err != nil {
				return err
			}
			return g.printOptimize(st, i18n.CLIOptimizeStatusLine)
		},
	}
	cmd.AddCommand(newOptimizeAutoCmd(g), newOptimizeCheckCmd(g), status, apply, revert)
	return cmd
}

// autoFlags are the flags of `deyroute optimize auto`.
type autoFlags struct{ dryRun, backends, yes bool }

func newOptimizeAutoCmd(g *Globals) *cobra.Command {
	var f autoFlags
	cmd := &cobra.Command{
		Use:     "auto [--dry-run] [--backends] [--yes]",
		Short:   i18n.T(i18n.CLIOptimizeAutoShort),
		Long:    i18n.T(i18n.CLIOptimizeAutoLong),
		Example: i18n.T(i18n.CLIOptimizeAutoExample),
		Args:    noArgs(),
		RunE:    func(cmd *cobra.Command, _ []string) error { return g.optimizeAuto(cmd.Context(), f) },
	}
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, i18n.T(i18n.CLIFlagDryRun))
	cmd.Flags().BoolVar(&f.backends, "backends", false, i18n.T(i18n.CLIFlagBackends))
	cmd.Flags().BoolVar(&f.yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

// optimizeAuto is `deyroute optimize auto`: the plan of every server is
// listed, confirmed once (or --yes) and applied with the hash that was
// shown, so a plan that changed in between is refused (DEY-X065) instead of
// applied unseen. --dry-run stops after the list. A plan without a change
// and without an offline node applies nothing.
func (g *Globals) optimizeAuto(ctx context.Context, f autoFlags) error {
	l, err := g.local()
	if err != nil {
		return err
	}
	cctx, cancel := longCtx(ctx)
	defer cancel()
	plan, err := l.OptimizeAutoPlan(cctx, api.AutoOptions{Backends: f.backends})
	if err != nil {
		return err
	}
	changes, pending := tunePlanCounts(plan)
	if g.JSON && (f.dryRun || changes == 0 && len(pending) == 0) {
		return g.emitJSON(plan)
	}
	// The plan is what the owner confirms: on stdout, or with --json on
	// stderr next to the confirmation (stdout then holds only the result).
	w := g.Out
	if g.JSON {
		w = g.Err
	}
	if !g.JSON || !f.yes {
		g.printTunePlan(w, plan)
	}
	switch {
	case f.dryRun:
		g.sayWrap(i18n.CLITuneDryRun)
		return nil
	case changes == 0 && len(pending) == 0:
		g.sayWrap(i18n.CLITuneNothing)
		return nil
	}
	lost := i18n.T(i18n.CLITuneLost, changes)
	if len(pending) > 0 {
		lost += " " + i18n.T(i18n.CLITuneLostPending, strings.Join(pending, ", "))
	}
	if tunePlanRestarts(plan) {
		lost += " " + i18n.T(i18n.CLITuneRestarts)
	}
	if err := g.confirm(lost, f.yes); err != nil {
		return err
	}
	p := g.newProgress()
	res, err := l.OptimizeAutoApply(cctx, api.AutoApply{Hash: plan.Hash, Backends: f.backends}, p.step)
	if err != nil {
		return err
	}
	if g.JSON {
		doc, err := jsonDoc(res)
		if err != nil {
			return err
		}
		doc["steps"] = p.steps
		return writeJSON(g.Out, doc, true)
	}
	s := g.sym()
	for _, h := range res.Hosts {
		if h.Error != nil {
			g.printWarn(g.Out, tuneHostName(h.Host)+": "+h.Error.Code+" "+clean(h.Error.Message))
		}
	}
	for _, w := range res.Warnings {
		g.printWarn(g.Out, clean(w))
	}
	g.println(g.styleOut(styleGreen, g.text(s.ok+" "+i18n.T(i18n.CLITuneApplied))))
	g.sayWrap(i18n.CLITuneUndo)
	return nil
}

// tunePlanCounts returns the number of changes of a plan and the offline
// nodes that apply it when they reconnect.
func tunePlanCounts(r api.TunePlanReport) (changes int, pending []string) {
	for _, h := range r.Hosts {
		changes += len(h.Changes)
		if h.Pending {
			pending = append(pending, h.Host)
		}
	}
	return changes, pending
}

// tunePlanRestarts reports whether a change restarts the active transport
// of tunnels (backend items, only with --backends).
func tunePlanRestarts(r api.TunePlanReport) bool {
	for _, h := range r.Hosts {
		for _, c := range h.Changes {
			if c.Effect == api.TuneEffectRestartsTunnels {
				return true
			}
		}
	}
	return false
}

// tuneHostName names a host of a plan or a check: "hub", "node de-1".
// sayWrap is say wrapped to the line width.
func (g *Globals) sayWrap(k i18n.Key, a ...any) {
	for _, l := range wrapText(i18n.T(k, a...), g.lineWidth()) {
		g.println(g.text(l))
	}
}

// printWarn prints "  ! text", wrapped to the line width.
func (g *Globals) printWarn(w io.Writer, text string) {
	for i, l := range wrapText(text, g.lineWidth()-4) {
		lead := "  " + g.sym().warn + " "
		if i > 0 {
			lead = "    "
		}
		fmt.Fprintln(w, g.text(lead+l))
	}
}

// tuneHostTitle is the section title of a host: "HUB", "NODE de-1".
func tuneHostTitle(host string) string {
	if host == "" || host == config.RoleHub {
		return i18n.T(i18n.CLITuneHostHubTitle)
	}
	return i18n.T(i18n.CLITuneHostNodeTitle, host)
}

func tuneHostName(host string) string {
	if host == "" || host == config.RoleHub {
		return i18n.T(i18n.CLITuneHostHub)
	}
	return i18n.T(i18n.CLITuneHostNode, host)
}

// printTunePlan prints a plan by host: a section per host with what was
// measured, then its changes in groups (tuneGroupOrder): "key  now → new"
// with the effect when it is not immediate, and under each group the
// reasons, each once. Then the skipped items (one line per reason), the
// totals and how to undo it. Nothing is wider than the terminal.
func (g *Globals) printTunePlan(w io.Writer, r api.TunePlanReport) {
	out := func(s string) { fmt.Fprintln(w, g.text(s)) }
	ell := g.sym().ell
	lw := g.lineWidth()
	out(g.styleOut(styleBold, i18n.T(i18n.CLITunePlanTitle)))
	changes, _ := tunePlanCounts(r)
	for _, h := range r.Hosts {
		fmt.Fprintln(w)
		out(g.sectionHead(tuneHostTitle(h.Host), ""))
		if facts := tui.TuneFactsText(h.Facts); facts != "" {
			for _, l := range wrapText(facts, lw-4) {
				out("  " + g.styleOut(styleGray, l))
			}
		}
		switch {
		case h.Error != nil:
			out(g.fit(i18n.T(i18n.CLITuneHostError, h.Error.Code+" "+clean(h.Error.Message))))
		case h.Pending:
			out(i18n.T(i18n.CLITuneHostPending))
		case len(h.Changes) == 0:
			out(i18n.T(i18n.CLITuneHostNothing))
		default:
			byKey := map[string]api.TuneChange{}
			keys := make([]string, 0, len(h.Changes))
			for _, c := range h.Changes {
				byKey[c.Key] = c
				keys = append(keys, c.Key)
			}
			for _, grp := range groupKeys(keys) {
				fmt.Fprintln(w)
				out("  " + g.styleOut(styleBold, grp.title))
				keyW := 0
				for _, k := range grp.keys {
					keyW = max(keyW, width(clean(shortKey(k))))
				}
				keyW = min(keyW, lw/2)
				var reasons []string
				seen := map[string]bool{}
				for _, k := range grp.keys {
					c := byKey[k]
					change := orDash(tuneValue(c.Key, clean(c.From))) + " " + g.sym().arrow + " " + orDash(tuneValue(c.Key, clean(c.To)))
					if c.Effect != "" && c.Effect != api.TuneEffectNow {
						change += "  (" + tui.TuneEffectText(c.Effect) + ")"
					}
					line := "    " + pad(trunc(clean(shortKey(c.Key)), keyW, ell), keyW+3) + change
					out(trunc(line, lw, ell))
					if why := clean(c.Reason); why != "" && !seen[why] {
						seen[why] = true
						reasons = append(reasons, why)
					}
				}
				for _, why := range reasons {
					for i, l := range wrapText(why, lw-8) {
						lead := "      " + g.sym().sep + " "
						if i > 0 {
							lead = "        "
						}
						out(lead + g.styleOut(styleGray, l))
					}
				}
			}
		}
		if len(h.Skips) > 0 {
			fmt.Fprintln(w)
		}
		for _, sk := range groupSkips(h.Skips) {
			short := make([]string, len(sk.keys))
			for i, k := range sk.keys {
				short[i] = shortKey(k)
			}
			for i, l := range wrapText(strings.TrimSpace(i18n.T(i18n.CLITuneHostSkipped, strings.Join(short, ", "), sk.reason)), lw-6) {
				lead := "  " + g.sym().skip + " "
				if i > 0 {
					lead = "    "
				}
				out(lead + g.styleOut(styleGray, l))
			}
		}
	}
	fmt.Fprintln(w)
	out(g.sectionHead(i18n.T(i18n.CLITuneTotalSection), ""))
	out("  " + i18n.T(i18n.CLITuneSummary, changes, len(r.Hosts)))
	for _, warn := range r.Warnings {
		g.printWarn(w, clean(warn))
	}
	out("  " + i18n.T(i18n.CLITuneUndo))
}

// skipGroup is the skipped items of one host that share a reason.
type skipGroup struct {
	reason string
	keys   []string
}

// groupSkips merges skipped items by reason, in the order they first
// appear (in a container every kernel item has the same one).
func groupSkips(skips []api.TuneSkip) []skipGroup {
	var out []skipGroup
	idx := map[string]int{}
	for _, s := range skips {
		reason := clean(s.Reason)
		if s.Code != "" && !strings.Contains(reason, s.Code) {
			reason = s.Code + " " + reason
		}
		i, ok := idx[reason]
		if !ok {
			i = len(out)
			idx[reason] = i
			out = append(out, skipGroup{reason: reason})
		}
		out[i].keys = append(out[i].keys, clean(s.Key))
	}
	return out
}

func newOptimizeCheckCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "check",
		Short:   i18n.T(i18n.CLIOptimizeCheckShort),
		Long:    i18n.T(i18n.CLIOptimizeCheckLong),
		Example: i18n.T(i18n.CLIOptimizeCheckExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r api.TuneCheck
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.OptimizeCheck(ctx)
				return err
			})
			if err != nil {
				return err
			}
			return g.printTuneCheck(r)
		},
	}
}

// tuneDriftError is DEY-X067 for the first drifted key of a check (nil
// without drift); the number of drifted keys is its detail.
func tuneDriftError(r api.TuneCheck) error {
	n := 0
	var first *api.TuneDrift
	for i := range r.Hosts {
		for j := range r.Hosts[i].Drift {
			if first == nil {
				first = &r.Hosts[i].Drift[j]
			}
			n++
		}
	}
	if first == nil {
		return nil
	}
	where := first.OverriddenBy
	if where == "" {
		where = i18n.T(i18n.CLITuneRuntime)
	}
	e := deyerr.New(deyerr.X067, deyerr.Params{"key": first.Key, "where": where})
	if n > 1 {
		e = e.WithDetail(i18n.T(i18n.CLITuneCheckFound, n))
	}
	return e
}

// printTuneCheck prints `optimize check`: per host the drifted keys
// (KEY, WANT, LIVE, CHANGED BY) and the findings. Drift is DEY-X067 (exit
// 2); with --json the error is part of the one document.
func (g *Globals) printTuneCheck(r api.TuneCheck) error {
	derr := tuneDriftError(r)
	if g.JSON {
		doc, err := jsonDoc(r)
		if err != nil {
			return err
		}
		if derr != nil {
			e := deyerr.As(derr)
			d := api.ToDTO(e)
			d.Log = e.Log()
			doc["error"], doc["exit_code"] = d, e.ExitCode()
		}
		if err := writeJSON(g.Out, doc, true); err != nil {
			return err
		}
		if derr != nil {
			return jsonShownError{derr}
		}
		return nil
	}
	s := g.sym()
	for i, h := range r.Hosts {
		if i > 0 {
			g.println()
		}
		g.println(g.styleOut(styleBold, g.text(i18n.T(i18n.CLITuneCheckHost, tuneHostName(h.Host), orDash(h.Profile)))))
		if h.Error != nil {
			g.say(i18n.CLITuneCheckError, h.Error.Code+" "+clean(h.Error.Message))
			continue
		}
		if len(h.Drift) > 0 {
			rows := make([][]string, 0, len(h.Drift))
			for _, d := range h.Drift {
				by := d.OverriddenBy
				if by == "" {
					by = i18n.T(i18n.CLITuneRuntime)
				}
				rows = append(rows, []string{clean(d.Key), orDash(clean(d.Want)), orDash(clean(d.Live)), clean(by)})
			}
			g.table([]string{i18n.T(i18n.CLIColKey), i18n.T(i18n.CLIColWant), i18n.T(i18n.CLIColLive), i18n.T(i18n.CLIColChangedBy)}, rows)
		}
		for _, f := range h.Findings {
			mark := s.ok
			switch f.Severity {
			case "warn":
				mark = s.warn
			case "error":
				mark = s.fail
			}
			msg := clean(f.Message)
			if f.Code != "" {
				msg = f.Code + " " + msg
			}
			g.println(g.text("  " + mark + " " + pad(clean(f.Check), 16) + msg))
		}
		if len(h.Drift) == 0 && len(h.Findings) == 0 {
			g.say(i18n.CLITuneCheckOK)
		}
	}
	if r.Clean {
		g.println()
		g.say(i18n.CLITuneCheckClean)
	}
	return derr
}

func (g *Globals) printOptimize(st api.OptimizeStatus, k i18n.Key) error {
	if g.JSON {
		return g.emitJSON(st)
	}
	if k != i18n.CLIOptimizeStatusLine { // status has the profile in its first section
		g.say(k, orDash(st.Profile))
		g.println()
	}
	bbr := i18n.T(i18n.CLIBBRValInactive)
	switch {
	case st.BBRActive:
		bbr = i18n.T(i18n.CLIBBRValActive)
	case !st.BBRAvailable:
		bbr = i18n.T(i18n.CLIBBRValMissing)
	}
	g.println(g.text(g.sectionHead(i18n.T(i18n.CLITuneStatusSection), "")))
	for _, l := range kvWrapLines("  ", [][2]string{
		{i18n.T(i18n.CLITuneLabelProfile), orDash(st.Profile)},
		{i18n.T(i18n.CLITuneLabelBBR), bbr},
		{i18n.T(i18n.CLITuneLabelServer), tui.TuneFactsText(st.Facts)},
	}, g.lineWidth()) {
		g.println(g.text(l))
	}
	for _, w := range st.Warnings {
		g.printWarn(g.Out, clean(w))
	}
	if len(st.Applied) > 0 {
		keys := make([]string, 0, len(st.Applied))
		for key := range st.Applied {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		g.println()
		g.println(g.text(g.sectionHead(i18n.T(i18n.CLITuneSettingsSection, tuneHostTitle(config.RoleHub)), "")))
		for i, grp := range groupKeys(keys) {
			if i > 0 {
				g.println()
			}
			g.println(g.text("  " + g.styleOut(styleBold, grp.title)))
			rows := make([][2]string, 0, len(grp.keys))
			for _, key := range grp.keys {
				rows = append(rows, [2]string{shortKey(key), orDash(tuneValue(key, st.Applied[key]))})
			}
			for _, l := range kvLines("    ", rows) {
				g.println(g.text(g.fit(l)))
			}
		}
	}
	if len(st.Nodes) == 0 {
		return nil
	}
	g.println()
	g.println(g.text(g.sectionHead(i18n.T(i18n.TUIDashNodes), "")))
	rows := make([][]string, 0, len(st.Nodes))
	for _, n := range st.Nodes {
		rows = append(rows, []string{n.Node, yesNo(n.Online), orDash(n.Profile), yesNo(n.Pending)})
	}
	g.table([]string{i18n.T(i18n.CLIColNode), i18n.T(i18n.CLIColOnline), i18n.T(i18n.CLIColProfile), i18n.T(i18n.CLIColPending)}, rows)
	for _, n := range st.Nodes {
		if n.Online && !n.AutoCapable {
			g.println(g.text("  " + g.sym().warn + " " + tuneHostName(n.Node) + ": " + i18n.T(i18n.CLITuneOldAgent)))
		}
	}
	return nil
}

// --------------------------------------------------------------- security

func newSecurityCmd(g *Globals) *cobra.Command {
	cmd := newGroup("security", i18n.CLISecurityShort)
	tls := newGroup("tls", i18n.CLISecurityTLSShort)
	tls.Example = i18n.T(i18n.CLISecurityTLSExample)
	tls.AddCommand(newTLSCmd(g, "show"), newTLSCmd(g, "renew"), newTLSDomainCmd(g), newTLSACMECmd(g))
	fw := newGroup("firewall", i18n.CLISecurityFirewallShort)
	fw.AddCommand(newFirewallCmd(g, "show"), newFirewallCmd(g, "apply"), newFirewallCmd(g, "disable"))
	cmd.AddCommand(newRotateTokensCmd(g), newRotateCACmd(g), tls, fw, newAuditCmd(g))
	return cmd
}

func newRotateTokensCmd(g *Globals) *cobra.Command {
	var tunnel string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rotate-tokens",
		Short:   i18n.T(i18n.CLIRotateTokensShort),
		Example: i18n.T(i18n.CLIRotateTokensExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := g.local()
			if err != nil {
				return err
			}
			what := i18n.T(i18n.CLIAllTunnels)
			if tunnel != "" {
				what = i18n.T(i18n.CLIOneTunnel, tunnel)
			}
			if err := g.confirm(i18n.T(i18n.CLIRotateTokensLost, what), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			if err := l.SecurityRotateTokens(ctx, tunnel, p.step); err != nil {
				return err
			}
			if g.JSON {
				return g.ok(map[string]any{"tunnel": tunnel}, p.steps)
			}
			g.say(i18n.CLITokensRotated, what)
			return nil
		},
	}
	cmd.Flags().StringVar(&tunnel, "tunnel", "", i18n.T(i18n.CLIFlagRotateTunnel))
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newRotateCACmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rotate-ca",
		Short:   i18n.T(i18n.CLIRotateCAShort),
		Example: i18n.T(i18n.CLIRotateCAExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := g.local()
			if err != nil {
				return err
			}
			if err := g.confirm(i18n.T(i18n.CLIRotateCALost), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			r, err := l.SecurityRotateCA(ctx, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"reissued": nonNil(r.Reissued), "offline": nonNil(r.Offline), "steps": p.steps})
			}
			g.say(i18n.CLICARotated, orDash(strings.Join(r.Reissued, ", ")))
			if len(r.Offline) > 0 {
				g.say(i18n.CLICAOffline, strings.Join(r.Offline, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newTLSCmd(g *Globals, verb string) *cobra.Command {
	var tunnel string
	short, example := i18n.CLITLSShowShort, i18n.CLITLSShowExample
	if verb == "renew" {
		short, example = i18n.CLITLSRenewShort, i18n.CLITLSRenewExample
	}
	cmd := &cobra.Command{
		Use:     verb,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var cs []api.CertInfo
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				if verb == "renew" {
					cs, err = l.SecurityTLSRenew(ctx, tunnel)
				} else {
					cs, err = l.SecurityTLSShow(ctx, tunnel)
				}
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"certificates": nonNil(cs)})
			}
			if verb == "renew" {
				g.say(i18n.CLITLSRenewed)
			}
			rows := make([][]string, 0, len(cs))
			for _, c := range cs {
				days := strconv.Itoa(c.DaysLeft)
				if c.Warning != "" {
					days += " " + g.sym().warn
				}
				rows = append(rows, []string{c.Kind, orDash(c.Tunnel), orDash(c.Mode), clean(c.Subject),
					localTime(c.NotAfter, "2006-01-02"), days, c.Fingerprint})
			}
			g.table([]string{i18n.T(i18n.CLIColKind), i18n.T(i18n.CLIColTunnel), i18n.T(i18n.CLIColMode), i18n.T(i18n.CLIColSubject),
				i18n.T(i18n.CLIColExpires), i18n.T(i18n.CLIColDays), i18n.T(i18n.CLIColFingerprint)}, rows)
			for _, c := range cs {
				if c.Warning != "" {
					g.println(g.text("  " + g.sym().warn + " " + c.Kind + " " + orDash(c.Tunnel) + ": " + clean(c.Warning)))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tunnel, "tunnel", "", i18n.T(i18n.CLIFlagTLSTunnel))
	return cmd
}

// newTLSDomainCmd is `deyroute security tls domain <name> | --clear`:
// hub.domain, the name tls.mode acme requests a Let's Encrypt certificate
// for (section 10). The tunnels are rendered again (TLS SANs).
func newTLSDomainCmd(g *Globals) *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:     "domain <name> | --clear",
		Short:   i18n.T(i18n.CLITLSDomainShort),
		Long:    i18n.T(i18n.CLITLSDomainLong),
		Example: i18n.T(i18n.CLITLSDomainExample),
		Args:    rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := ""
			if len(args) == 1 {
				domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(args[0]), "."))
			}
			if remove == (domain != "") || len(args) == 1 && domain == "" {
				return usageErr(i18n.T(i18n.CLIWantDomainOrClear))
			}
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.SettingsSet(ctx, api.SettingsRequest{Domain: &domain})
			})
			if err != nil {
				return err
			}
			if remove {
				return g.done(map[string]any{"domain": ""}, i18n.CLITLSDomainCleared)
			}
			return g.done(map[string]any{"domain": domain}, i18n.CLITLSDomainSet, domain)
		},
	}
	cmd.Flags().BoolVar(&remove, "clear", false, i18n.T(i18n.CLIFlagClearDomain))
	return cmd
}

// newTLSACMECmd is `deyroute security tls acme [--email E]
// [--cloudflare-token-file F]`: the ACME account e-mail and the Cloudflare
// API token for DNS-01 (section 10, Advanced). The daemon copies the token
// to /etc/deyroute/secrets/cloudflare.token (0600); an empty value removes
// the setting.
func newTLSACMECmd(g *Globals) *cobra.Command {
	var email, tokenFile string
	cmd := &cobra.Command{
		Use:     "acme [--email E] [--cloudflare-token-file F]",
		Short:   i18n.T(i18n.CLITLSACMEShort),
		Long:    i18n.T(i18n.CLITLSACMELong),
		Example: i18n.T(i18n.CLITLSACMEExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fl := cmd.Flags()
			var req api.SettingsRequest
			out := map[string]any{}
			if fl.Changed("email") {
				e := strings.TrimSpace(email)
				req.ACMEEmail = &e
				out["email"] = e
			}
			if fl.Changed("cloudflare-token-file") {
				f := strings.TrimSpace(tokenFile)
				if f != "" {
					// The daemon reads the file with its own working
					// directory (/): a relative path is resolved here.
					abs, err := filepath.Abs(f)
					if err != nil {
						return deyerr.New(deyerr.C013, deyerr.Params{"field": "--cloudflare-token-file", "value": f, "allowed": i18n.T(i18n.CLIWantAbsPath)})
					}
					f = abs
				}
				req.CloudflareTokenFile = &f
				out["cloudflare_token_file"] = ""
				if f != "" {
					out["cloudflare_token_file"] = config.DefaultCloudflareTokenFile
				}
			}
			if req.ACMEEmail == nil && req.CloudflareTokenFile == nil {
				return usageErr(i18n.T(i18n.CLIACMENothing))
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.SettingsSet(ctx, req) })
			if err != nil {
				return err
			}
			if g.JSON {
				return g.ok(out, nil)
			}
			switch {
			case req.ACMEEmail == nil:
			case *req.ACMEEmail == "":
				g.say(i18n.CLIACMEEmailRemoved)
			default:
				g.say(i18n.CLIACMEEmailSet, *req.ACMEEmail)
			}
			switch {
			case req.CloudflareTokenFile == nil:
			case *req.CloudflareTokenFile == "":
				g.say(i18n.CLIACMETokenRemoved)
			default:
				g.say(i18n.CLIACMETokenSet, config.DefaultCloudflareTokenFile)
			}
			g.say(i18n.CLIACMERenewHint)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", i18n.T(i18n.CLIFlagACMEEmail))
	cmd.Flags().StringVar(&tokenFile, "cloudflare-token-file", "", i18n.T(i18n.CLIFlagCloudflareToken))
	return cmd
}

func newFirewallCmd(g *Globals, action string) *cobra.Command {
	var yes bool
	short, example := i18n.CLIFirewallShowShort, i18n.CLIFirewallShowExample
	switch action {
	case "apply":
		short, example = i18n.CLIFirewallApplyShort, i18n.CLIFirewallApplyExample
	case "disable":
		short, example = i18n.CLIFirewallDisableShort, i18n.CLIFirewallDisableExample
	}
	cmd := &cobra.Command{
		Use:     action,
		Short:   i18n.T(short),
		Example: i18n.T(example),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if action == "disable" {
				if err := g.confirm(i18n.T(i18n.TUISeFwDisableLost), yes); err != nil {
					return err
				}
			}
			var fi api.FirewallInfo
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				fi, err = l.SecurityFirewall(ctx, action)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(fi)
			}
			switch action {
			case "apply":
				g.say(i18n.CLIFirewallApplied)
			case "disable":
				g.say(i18n.CLIFirewallDisabled)
			}
			mode := i18n.T(i18n.CLIFirewallManaged)
			if !fi.Managed {
				mode = i18n.T(i18n.CLIFirewallSuggestOnly)
			}
			g.say(i18n.CLIFirewallMode, mode, orDash(strings.Join(fi.Detected, ", ")))
			if strings.TrimSpace(fi.Ruleset) != "" {
				g.println(g.text(cleanLines(fi.Ruleset)))
			}
			if len(fi.Suggested) > 0 {
				g.say(i18n.CLIFirewallSuggested)
				for _, s := range fi.Suggested {
					g.println(g.text("  " + clean(s)))
				}
			}
			return nil
		},
	}
	if action == "disable" {
		cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	}
	return cmd
}

func newAuditCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "audit",
		Short:   i18n.T(i18n.CLIAuditShort),
		Example: i18n.T(i18n.CLIAuditExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r api.AuditReport
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				r, err = l.SecurityAudit(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"clean": r.Clean, "items": nonNil(r.Items)})
			}
			s := g.sym()
			for _, it := range r.Items {
				mark := s.ok
				switch it.Severity {
				case "warn":
					mark = s.warn
				case "error":
					mark = s.fail
				}
				g.println(g.text("  " + mark + " " + pad(it.Check, 18) + clean(it.Message)))
			}
			if r.Clean {
				g.say(i18n.CLIAuditClean)
			} else {
				g.say(i18n.CLIAuditProblems)
			}
			return nil
		},
	}
}

// ----------------------------------------------------------------- notify

func newNotifyCmd(g *Globals) *cobra.Command {
	cmd := newGroup("notify", i18n.CLINotifyShort)
	tg := newGroup("telegram", i18n.CLITelegramShort)
	var tokenFile, chatID string
	set := &cobra.Command{
		Use:     "set --token-file F --chat-id C",
		Short:   i18n.T(i18n.CLITelegramSetShort),
		Long:    i18n.T(i18n.CLITelegramSetLong),
		Example: i18n.T(i18n.CLITelegramSetExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			tokenFile, chatID = strings.TrimSpace(tokenFile), strings.TrimSpace(chatID)
			if tokenFile == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--token-file"))
			}
			if chatID == "" {
				return usageErr(i18n.T(i18n.CLIWantFlag, "--chat-id"))
			}
			// The daemon reads the file with its own working directory
			// (/): a relative path is resolved here, where it was typed.
			abs, err := filepath.Abs(tokenFile)
			if err != nil {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "--token-file", "value": tokenFile, "allowed": i18n.T(i18n.CLIWantAbsPath)})
			}
			tokenFile = abs
			err = g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.NotifyTelegramSet(ctx, tokenFile, chatID, nil)
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"token_file": tokenFile, "chat_id": chatID}, i18n.CLITelegramSet, chatID)
		},
	}
	set.Flags().StringVar(&tokenFile, "token-file", "", i18n.T(i18n.CLIFlagTokenFile))
	set.Flags().StringVar(&chatID, "chat-id", "", i18n.T(i18n.CLIFlagChatID))
	test := &cobra.Command{
		Use:     "test",
		Short:   i18n.T(i18n.CLITelegramTestShort),
		Example: i18n.T(i18n.CLITelegramTestExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.NotifyTelegramTest(ctx) })
			if err != nil {
				return err
			}
			return g.done(nil, i18n.CLITelegramTested)
		},
	}
	off := &cobra.Command{
		Use:     "off",
		Short:   i18n.T(i18n.CLITelegramOffShort),
		Example: i18n.T(i18n.CLITelegramOffExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error { return l.NotifyTelegramOff(ctx) })
			if err != nil {
				return err
			}
			return g.done(nil, i18n.CLITelegramOff)
		},
	}
	tg.AddCommand(set, test, off)
	cmd.AddCommand(tg)
	return cmd
}

// --------------------------------------------------------------- settings

func newSettingsCmd(g *Globals) *cobra.Command {
	cmd := newGroup("settings", i18n.CLISettingsShort)
	cmd.AddCommand(&cobra.Command{
		Use:       "ui-mode simple|advanced",
		Short:     i18n.T(i18n.CLIUIModeShort),
		Example:   i18n.T(i18n.CLIUIModeExample),
		Args:      exactArgs(1),
		ValidArgs: []string{config.UIModeSimple, config.UIModeAdvanced},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := strings.ToLower(strings.TrimSpace(args[0]))
			if mode != config.UIModeSimple && mode != config.UIModeAdvanced {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "ui_mode", "value": args[0], "allowed": "simple, advanced"})
			}
			err := g.call(cmd.Context(), func(ctx context.Context, l api.Local) error {
				return l.SettingsSet(ctx, api.SettingsRequest{UIMode: mode})
			})
			if err != nil {
				return err
			}
			return g.done(map[string]any{"ui_mode": mode}, i18n.CLIUIModeSet, mode)
		},
	})
	return cmd
}

// ----------------------------------------------------------------- update

// rollbackLocal is `deyroute update --rollback` without the daemon (it is
// down, or this is a node): the binary and deyroute.prev are swapped here
// and the role's service is restarted.
func (g *Globals) rollbackLocal(ctx context.Context, yes bool) error {
	if err := g.confirm(i18n.T(i18n.CLIRollbackLost), yes); err != nil {
		return err
	}
	if err := g.Ops.SelfRollback(g.Root); err != nil {
		return err
	}
	unit := g.service() + ".service"
	rctx, cancel := longCtx(ctx)
	defer cancel()
	if _, _, err := g.Runner.Run(rctx, "systemctl", []string{"restart", unit}, nil); err != nil {
		g.note(i18n.CLIRollbackRestartFailed, unit)
		return err
	}
	return g.done(map[string]any{"rolled_back": true, "daemon_running": false, "service": unit}, i18n.CLIRolledBackLocal, unit)
}

func newUpdateCmd(g *Globals) *cobra.Command {
	var check, rollback, yes bool
	var ver string
	cmd := &cobra.Command{
		Use:     "update",
		Short:   i18n.T(i18n.CLIUpdateShort),
		Long:    i18n.T(i18n.CLIUpdateLong),
		Example: i18n.T(i18n.CLIUpdateExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if check && (rollback || ver != "") || rollback && ver != "" {
				return usageErr(i18n.T(i18n.CLIUpdateFlagsConflict))
			}
			l, err := g.local()
			if rollback && (errDaemonDown(err) || err == nil && g.role() == config.RoleNode) {
				// A crash-looping daemon after a bad update must still be
				// rolled back, and a node has no update API of its own.
				return g.rollbackLocal(cmd.Context(), yes)
			}
			if err != nil {
				return err
			}
			switch {
			case check:
				ctx, cancel := callCtx(cmd.Context())
				defer cancel()
				info, err := l.UpdateCheck(ctx)
				if err != nil {
					return err
				}
				return g.printUpdateInfo(info)
			case rollback:
				if err := g.confirm(i18n.T(i18n.CLIRollbackLost), yes); err != nil {
					return err
				}
				ctx, cancel := longCtx(cmd.Context())
				defer cancel()
				info, err := l.UpdateRollback(ctx)
				if err != nil {
					return err
				}
				if g.JSON {
					return g.emitJSON(info)
				}
				g.say(i18n.CLIRolledBack, orDash(info.Current))
				return nil
			}
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			target := strings.TrimPrefix(strings.TrimSpace(ver), "v")
			info, err := l.UpdateCheck(ctx)
			switch {
			case err != nil && (target == "" || errDaemonDown(err)):
				return err
			case err != nil:
				// An explicit --version does not depend on the release
				// check: the hub may not reach the release API while the
				// mirror or a node can still fetch that version.
				g.note(i18n.CLIUpdateCheckSkipped, deyerr.As(err).Code, target)
				info = api.UpdateInfo{Current: version.Version}
			}
			if target == "" {
				if !info.Available {
					if g.JSON {
						return g.emitJSON(info)
					}
					g.say(i18n.CLIUpToDate, orDash(info.Current))
					return nil
				}
				target = info.Latest
			}
			if !g.JSON && info.Changelog != "" && target == info.Latest {
				g.say(i18n.CLIChangelog, info.Latest)
				g.println(g.text(cleanLines(info.Changelog)))
			}
			if err := g.confirm(i18n.T(i18n.CLIUpdateLost, orDash(info.Current), target), yes); err != nil {
				return err
			}
			p := g.newProgress()
			res, err := l.UpdateApply(ctx, target, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"update": res, "steps": p.steps})
			}
			g.say(i18n.CLIUpdated, orDash(res.Previous), orDash(res.Current))
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, i18n.T(i18n.CLIFlagCheck))
	f.StringVar(&ver, "version", "", i18n.T(i18n.CLIFlagVersion))
	f.BoolVar(&rollback, "rollback", false, i18n.T(i18n.CLIFlagRollback))
	f.BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	cmd.AddCommand(newUpdateBackendsCmd(g), newUpdateManifestCmd(g))
	return cmd
}

func (g *Globals) printUpdateInfo(info api.UpdateInfo) error {
	if g.JSON {
		return g.emitJSON(info)
	}
	if !info.Available {
		g.say(i18n.CLIUpToDate, orDash(info.Current))
		return nil
	}
	g.say(i18n.CLIUpdateAvailable, orDash(info.Current), info.Latest)
	if info.Changelog != "" {
		g.println(g.text(cleanLines(info.Changelog)))
	}
	return nil
}

func newUpdateBackendsCmd(g *Globals) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "backends [name]",
		Short:   i18n.T(i18n.CLIUpdateBackendsShort),
		Example: i18n.T(i18n.CLIUpdateBackendsExample),
		Args:    rangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			l, err := g.local()
			if err != nil {
				return err
			}
			what := i18n.T(i18n.CLIAllBackends)
			if name != "" {
				what = name
			}
			if err := g.confirm(i18n.T(i18n.CLIUpdateBackendsLost, what), yes); err != nil {
				return err
			}
			p := g.newProgress()
			ctx, cancel := longCtx(cmd.Context())
			defer cancel()
			res, err := l.UpdateBackends(ctx, name, p.step)
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(map[string]any{"backends": nonNil(res), "steps": p.steps})
			}
			rows := make([][]string, 0, len(res))
			for _, b := range res {
				detail := ""
				if b.Error != nil {
					detail = b.Error.Code + " " + clean(b.Error.Message)
				}
				rows = append(rows, []string{b.Backend, orDash(b.From), orDash(b.To), b.Status, detail})
			}
			g.table([]string{i18n.T(i18n.CLIColBackend), i18n.T(i18n.CLIColFrom), i18n.T(i18n.CLIColTo),
				i18n.T(i18n.CLIColResult), i18n.T(i18n.CLIColDetail)}, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, i18n.T(i18n.CLIFlagYes))
	return cmd
}

func newUpdateManifestCmd(g *Globals) *cobra.Command {
	return &cobra.Command{
		Use:     "manifest",
		Short:   i18n.T(i18n.CLIUpdateManifestShort),
		Example: i18n.T(i18n.CLIUpdateManifestExample),
		Args:    noArgs(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var m api.ManifestInfo
			err := g.callLong(cmd.Context(), func(ctx context.Context, l api.Local) (err error) {
				m, err = l.UpdateManifest(ctx)
				return err
			})
			if err != nil {
				return err
			}
			if g.JSON {
				return g.emitJSON(m)
			}
			g.say(i18n.CLIManifestUpdated, orDash(m.Source))
			names := make([]string, 0, len(m.Versions))
			for n := range m.Versions {
				names = append(names, n)
			}
			sort.Strings(names)
			rows := make([][]string, 0, len(names))
			for _, n := range names {
				rows = append(rows, []string{n, m.Versions[n]})
			}
			g.table([]string{i18n.T(i18n.CLIColBackend), i18n.T(i18n.CLIColVersion)}, rows)
			return nil
		},
	}
}
