package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// Automatic tuning in the Optimize menu (7): the plan of every server with
// the reason and effect of each change, applied after one confirmation with
// the hash that was shown (a plan that changed is refused, DEY-X065), and
// the tuning check. The CLI shares the fact and effect texts.

// TuneEffectText is the word for a change's effect (api.TuneEffect*):
// "now", "next start", "after reboot", "restarts tunnels".
func TuneEffectText(effect string) string {
	switch effect {
	case api.TuneEffectNow:
		return i18n.T(i18n.TuneEffectNow)
	case api.TuneEffectNextStart:
		return i18n.T(i18n.TuneEffectNextStart)
	case api.TuneEffectReboot:
		return i18n.T(i18n.TuneEffectReboot)
	case api.TuneEffectRestartsTunnels:
		return i18n.T(i18n.TuneEffectRestarts)
	}
	return clean(effect)
}

// TuneFactsText summarizes measured facts on one line: "2.0 GiB RAM · 2
// CPUs · kernel 6.1.0 · eth0 MTU 1500 · qdisc fq"; "" without facts.
// Unknown values are left out.
func TuneFactsText(f *api.TuneFacts) string {
	if f == nil {
		return ""
	}
	var parts []string
	if f.MemBytes > 0 {
		parts = append(parts, i18n.T(i18n.TuneFactRAM, FormatBytes(f.MemBytes)))
	}
	if f.CPUs > 0 {
		parts = append(parts, i18n.T(i18n.TuneFactCPUs, f.CPUs))
	}
	if f.Kernel != "" {
		parts = append(parts, i18n.T(i18n.TuneFactKernel, clean(f.Kernel)))
	}
	if f.NIC != "" && f.NICMTU > 0 {
		parts = append(parts, i18n.T(i18n.TuneFactNIC, clean(f.NIC), f.NICMTU))
	}
	if f.Qdisc != "" {
		parts = append(parts, i18n.T(i18n.TuneFactQdisc, clean(f.Qdisc)))
	}
	if f.ConntrackLoaded && f.ConntrackMax > 0 {
		parts = append(parts, i18n.T(i18n.TuneFactConntrack, f.ConntrackCount, f.ConntrackMax))
	}
	if f.Virt != "" {
		parts = append(parts, i18n.T(i18n.TuneFactVirt, clean(f.Virt)))
	}
	return strings.Join(parts, " · ")
}

// tuneHost names a host of a plan or a check: "hub" or "node de-1".
func tuneHost(host string) string {
	if host == "" || host == roleHub {
		return i18n.T(i18n.CLITuneHostHub)
	}
	return i18n.T(i18n.CLITuneHostNode, clean(host))
}

// tuneCounts returns the number of changes of a plan and its offline nodes.
func tuneCounts(r api.TunePlanReport) (changes int, pending []string, restarts bool) {
	for _, h := range r.Hosts {
		changes += len(h.Changes)
		if h.Pending {
			pending = append(pending, clean(h.Host))
		}
		for _, c := range h.Changes {
			restarts = restarts || c.Effect == api.TuneEffectRestartsTunnels
		}
	}
	return changes, pending, restarts
}

// autoTuning is "Automatic tuning (recommended)": the plan, then "1) Apply
// this plan" when it changes something.
func autoTuning() *taskScreen {
	title := itemName(i18n.TUIOpAuto)
	t := newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.OptimizeAutoPlan(ctx, api.AutoOptions{})
	}, renderTunePlan)
	t.refreshable = true
	t.option = func(_ *app, v any) (string, func(a *app) tea.Cmd) {
		r, _ := v.(api.TunePlanReport)
		changes, pending, restarts := tuneCounts(r)
		if changes == 0 && len(pending) == 0 {
			return "", nil
		}
		return i18n.T(i18n.TUIOpAutoApplyItem), func(a *app) tea.Cmd {
			text := i18n.T(i18n.TUIOpAutoConfirm, changes)
			if len(pending) > 0 {
				text += "\n" + i18n.T(i18n.TUIOpAutoPending, strings.Join(pending, ", "))
			}
			if restarts {
				text += "\n" + i18n.T(i18n.TUIOpAutoRestarts)
			}
			hash := r.Hash
			// Back on the plan after the apply, it is computed again.
			t.stale = true
			return a.push(newConfirm(title, text, false, func(a *app) tea.Cmd {
				return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
					return l.OptimizeAutoApply(ctx, api.AutoApply{Hash: hash}, progress)
				}, renderTuneApplied))
			}))
		}
	}
	return t
}

// renderTunePlan shows the plan grouped by host: the facts, one line per
// change (KEY, NOW, NEW, EFFECT, WHY, cut to the width), the skipped items
// by reason and the offline nodes. The totals come first so a tall plan
// never hides them.
func renderTunePlan(a *app, v any) string {
	r, _ := v.(api.TunePlanReport)
	changes, pending, _ := tuneCounts(r)
	var b strings.Builder
	if changes == 0 && len(pending) == 0 {
		b.WriteString(a.paint(colGreen, indent(i18n.T(i18n.TUIOpAutoNothing))) + "\n")
	} else {
		b.WriteString(a.paint(colYellow, indent(i18n.T(i18n.TUIOpAutoIntro, changes, len(r.Hosts)))) + "\n")
	}
	for _, w := range r.Warnings {
		b.WriteString(a.paint(colYellow, "  "+a.sym().warn+" "+clean(w)) + "\n")
	}
	for _, h := range r.Hosts {
		head := tuneHost(h.Host)
		if f := TuneFactsText(h.Facts); f != "" {
			head += " " + a.sym().sep + " " + f
		}
		b.WriteString("\n" + a.clip(" "+a.bold(head)) + "\n")
		switch {
		case h.Error != nil:
			b.WriteString(a.paint(colRed, "  "+a.sym().fail+" "+h.Error.Code+" "+clean(h.Error.Message)) + "\n")
		case h.Pending:
			b.WriteString(a.paint(colYellow, i18n.T(i18n.CLITuneHostPending)) + "\n")
		case len(h.Changes) == 0:
			b.WriteString(i18n.T(i18n.CLITuneHostNothing) + "\n")
		default:
			b.WriteString(a.tuneTable(h.Changes))
		}
		for _, line := range skipLines(h.Skips) {
			b.WriteString(a.paint(colGray, a.clip(line)) + "\n")
		}
	}
	return b.String()
}

// tuneTable lays out the changes of one host, one line each.
func (a *app) tuneTable(cs []api.TuneChange) string {
	header := []string{i18n.T(i18n.CLIColKey), i18n.T(i18n.CLIColNow), i18n.T(i18n.CLIColNew), i18n.T(i18n.CLIColEffect), i18n.T(i18n.CLIColWhy)}
	rows := make([][]string, 0, len(cs))
	for _, c := range cs {
		from, to := clean(c.From), clean(c.To)
		if from == "" {
			from = i18n.T(i18n.TUIDash)
		}
		rows = append(rows, []string{clean(c.Key), from, to, TuneEffectText(c.Effect), clean(c.Reason)})
	}
	w := make([]int, len(header))
	for i, h := range header {
		w[i] = width(h)
	}
	for _, r := range rows {
		for i, c := range r {
			w[i] = max(w[i], width(c))
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		b.WriteString("  ")
		for i, c := range cells {
			if i == len(cells)-1 {
				b.WriteString(c)
			} else {
				b.WriteString(pad(c, w[i]+2))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	var b strings.Builder
	b.WriteString(a.paint(colGray, a.clip(line(header))) + "\n")
	for _, r := range rows {
		b.WriteString(a.clip(line(r)) + "\n")
	}
	return b.String()
}

// skipLines merges the skipped items of a host by reason: "  skipped a,
// b: reason".
func skipLines(skips []api.TuneSkip) []string {
	var reasons []string
	keys := map[string][]string{}
	for _, s := range skips {
		reason := clean(s.Reason)
		if s.Code != "" && !strings.Contains(reason, s.Code) {
			reason = s.Code + " " + reason
		}
		if _, ok := keys[reason]; !ok {
			reasons = append(reasons, reason)
		}
		keys[reason] = append(keys[reason], clean(s.Key))
	}
	out := make([]string, 0, len(reasons))
	for _, r := range reasons {
		out = append(out, i18n.T(i18n.CLITuneHostSkipped, strings.Join(keys[r], ", "), r))
	}
	return out
}

// renderTuneApplied is the result of the apply: the hosts that failed,
// the warnings and how to undo it.
func renderTuneApplied(a *app, v any) string {
	r, _ := v.(api.TunePlanReport)
	var b strings.Builder
	for _, h := range r.Hosts {
		if h.Error != nil {
			b.WriteString(a.paint(colYellow, "  "+a.sym().warn+" "+tuneHost(h.Host)+": "+h.Error.Code+" "+clean(h.Error.Message)) + "\n")
		}
	}
	for _, w := range r.Warnings {
		b.WriteString(a.paint(colYellow, "  "+a.sym().warn+" "+clean(w)) + "\n")
	}
	b.WriteString(a.paint(colGreen, " "+a.sym().ok+" "+i18n.T(i18n.TUIOpAutoApplied)) + "\n")
	b.WriteString(indent(i18n.T(i18n.CLITuneUndo)) + "\n")
	return b.String()
}

// checkTuning is "Check tuning": drift and findings of every server.
func checkTuning() *taskScreen {
	t := newTask(itemName(i18n.TUIOpCheck), longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.OptimizeCheck(ctx)
	}, renderTuneCheck)
	t.refreshable = true
	return t
}

// renderTuneCheck lists per host the keys whose live value is not what
// deyroute set (and who changed it), then the findings.
func renderTuneCheck(a *app, v any) string {
	r, _ := v.(api.TuneCheck)
	s := a.sym()
	var b strings.Builder
	for i, h := range r.Hosts {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(" " + a.bold(i18n.T(i18n.CLITuneCheckHost, tuneHost(h.Host), orDashTUI(clean(h.Profile)))) + "\n")
		if h.Error != nil {
			b.WriteString(a.paint(colRed, "  "+s.fail+" "+h.Error.Code+" "+clean(h.Error.Message)) + "\n")
			continue
		}
		for _, d := range h.Drift {
			by := clean(d.OverriddenBy)
			if by == "" {
				by = i18n.T(i18n.CLITuneRuntime)
			}
			b.WriteString(a.paint(colYellow, a.clip("  "+s.warn+" "+i18n.T(i18n.TUIOpCheckDrift, clean(d.Key), clean(d.Want), clean(d.Live), by))) + "\n")
		}
		for _, f := range h.Findings {
			mark := a.paint(colGreen, s.ok)
			switch f.Severity {
			case "warn":
				mark = a.paint(colYellow, s.warn)
			case "error":
				mark = a.paint(colRed, s.fail)
			}
			msg := clean(f.Message)
			if f.Code != "" {
				msg = f.Code + " " + msg
			}
			b.WriteString("  " + mark + " " + clean(f.Check) + ": " + msg + "\n")
		}
		if len(h.Drift) == 0 && len(h.Findings) == 0 {
			b.WriteString(i18n.T(i18n.CLITuneCheckOK) + "\n")
		}
	}
	if r.Clean {
		b.WriteString("\n" + a.paint(colGreen, " "+i18n.T(i18n.TUIOpCheckClean)) + "\n")
	}
	return b.String()
}

// renderTuneNodes lists the tuning state of every node under the Optimize
// header, in aligned columns: "de-1   online   auto  pending".
func renderTuneNodes(a *app, nodes []api.NodeTuneStatus) string {
	if len(nodes) == 0 {
		return ""
	}
	type row struct {
		cells []string // plain text
		cols  []string // colour of each cell ("" = none)
	}
	rows := make([]row, 0, len(nodes))
	w := make([]int, 4)
	for _, n := range nodes {
		r := row{cells: []string{clean(n.Node), i18n.T(i18n.TUIOffline), clean(n.Profile)}, cols: []string{"", colGray, ""}}
		if n.Online {
			r.cells[1], r.cols[1] = i18n.T(i18n.TUIOnline), colGreen
		}
		if r.cells[2] == "" {
			r.cells[2] = i18n.T(i18n.TUIOpNever)
		}
		switch {
		case n.Pending:
			r.cells, r.cols = append(r.cells, i18n.T(i18n.TUIOpNodePending)), append(r.cols, colYellow)
		case n.Online && !n.AutoCapable:
			r.cells, r.cols = append(r.cells, a.sym().warn+" "+i18n.T(i18n.TUIOpNodeOldAgent)), append(r.cols, colYellow)
		}
		for i, c := range r.cells {
			w[i] = max(w[i], width(c))
		}
		rows = append(rows, r)
	}
	var b strings.Builder
	b.WriteString(" " + i18n.T(i18n.TUIOpNodes) + "\n")
	for _, r := range rows {
		b.WriteString(" ")
		for i, c := range r.cells {
			b.WriteString(" ")
			if r.cols[i] != "" {
				b.WriteString(a.paint(r.cols[i], c))
			} else {
				b.WriteString(c)
			}
			if i < len(r.cells)-1 {
				b.WriteString(strings.Repeat(" ", w[i]-width(c)+1))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// orDashTUI returns v, or "-" when it is empty.
func orDashTUI(v string) string {
	if v == "" {
		return i18n.T(i18n.TUIDash)
	}
	return v
}
