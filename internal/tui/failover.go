package tui

import (
	"context"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
)

// rungTestSeconds is how long test-ladder tries each rung (section 9).
const rungTestSeconds = 20

// ---- 5 Failover

func failoverMenu(a *app) screen {
	title := i18n.T(i18n.MenuFailover)
	sub := func(k i18n.Key) string { return title + " - " + i18n.T(k) }
	return newMenu(a, i18n.MenuFailover, i18n.TUIHelpFailover, []menuItem{
		{label: i18n.TUIFoPolicy, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIFoPolicy), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(pickPolicy(t))
			}))
		}},
		{label: i18n.TUIFoLadder, act: func(a *app) tea.Cmd {
			if !a.advanced {
				return a.push(textPage(sub(i18n.TUIFoLadder), indent(i18n.T(i18n.TUILadSimple))+"\n"))
			}
			return a.push(pickTunnel(sub(i18n.TUIFoLadder), editLadder))
		}},
		{label: i18n.TUIFoBackups, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIFoBackups), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(backupNodes(a, t.ID))
			}))
		}},
		{label: i18n.TUIFoPause, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIFoPause), pauseResume))
		}},
		{label: i18n.TUIFoReset, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIFoReset), resetTunnel))
		}},
		{label: i18n.TUIFoTest, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIFoTest), testLadder))
		}},
		{label: i18n.TUIFoThresholds, adv: true, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIFoThresholds), editThresholds))
		}},
	})
}

// textPage is a fixed text page (no daemon call).
func textPage(title, text string) *taskScreen {
	t := newLocalTask(title, func(context.Context) (any, error) { return nil, nil }, func(*app, any) string { return text })
	t.cancellable = true
	return t
}

func pickPolicy(t api.TunnelInfo) *listScreen {
	id := t.ID
	title := i18n.T(i18n.TUIFoPolicy) + ": " + id
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIPolPick, id)}
	for _, p := range []struct {
		id  string
		key i18n.Key
	}{
		{config.PolicyTransportThenNode, i18n.TUIPolTTN},
		{config.PolicyTransportOnly, i18n.TUIPolTO},
		{config.PolicyNodeOnly, i18n.TUIPolNO},
	} {
		label := i18n.T(p.key)
		if p.id == t.Policy {
			label += i18n.T(i18n.TUICurrent)
		}
		l.fixed = append(l.fixed, choice{label: label, value: p.id})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		policy := c.value.(string)
		if policy == t.Policy {
			return a.back(i18n.T(i18n.TUINothingChanged))
		}
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return l.TunnelEdit(ctx, id, api.TunnelEditRequest{Policy: &policy}, progress)
		}, textResult(i18n.T(i18n.TUIPolSet, id, policy))))
	}
	return l
}

// editLadder opens the ladder editor for a tunnel (Advanced only).
func editLadder(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUILadTitle, id)
	return a.push(newLadderEditor(title, t.Ladder, func(a *app, rungs []string) tea.Cmd {
		if slices.Equal(rungs, t.Ladder) {
			return a.back(i18n.T(i18n.TUINothingChanged))
		}
		joined := strings.Join(rungs, " "+a.sym().arrow+" ")
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return l.TunnelEdit(ctx, id, api.TunnelEditRequest{Rungs: rungs}, progress)
		}, textResult(i18n.T(i18n.TUILadSaved, id, joined))))
	}))
}

// backupNodes is the backup-node page of one tunnel.
func backupNodes(a *app, id string) *listScreen {
	title := i18n.T(i18n.TUIFoBackups) + ": " + id
	var l *listScreen
	current := func() api.TunnelDetail {
		d, _ := l.data.(api.TunnelDetail)
		return d
	}
	l = newMenu(a, i18n.TUIFoBackups, i18n.TUIHelpFailover, []menuItem{
		{label: i18n.TUIBkAdd, act: func(a *app) tea.Cmd {
			if !l.loaded {
				// Without the tunnel's nodes the primary could be offered.
				l.msg = i18n.T(i18n.Loading)
				return l.reload(a)
			}
			d := current()
			return a.push(pickNode(title, i18n.T(i18n.TUIBkNoCandidates), func(n api.NodeInfo) bool {
				return !slices.Contains(d.Nodes, n.ID)
			}, func(a *app, n api.NodeInfo) tea.Cmd { return addBackup(a, id, n.ID) }))
		}},
		{label: i18n.TUIBkRemove, act: func(a *app) tea.Cmd {
			d := current()
			return a.push(pickBackup(id, d.Nodes))
		}},
	})
	l.title = title
	l.intro = i18n.T(i18n.TUIBkWarning)
	l.load = func(ctx context.Context, lc api.Local, _ func(api.Step)) (any, error) {
		return lc.TunnelShow(ctx, id)
	}
	l.header = func(a *app, v any) string {
		d, _ := v.(api.TunnelDetail)
		backups := i18n.T(i18n.TUINone)
		if len(d.Nodes) > 1 {
			backups = strings.Join(d.Nodes[1:], ", ")
		}
		return indent(i18n.T(i18n.TUIBkHeader, id, backups)) + "\n"
	}
	return l
}

func addBackup(a *app, id, node string) tea.Cmd {
	title := i18n.T(i18n.TUIBkAdd) + ": " + id
	text := i18n.T(i18n.TUIBkWarning) + "\n" + i18n.T(i18n.TUIBkAddConfirm, node, id)
	return a.replace(newConfirm(title, text, false, func(a *app) tea.Cmd {
		t := newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return nil, l.TunnelBackupAdd(ctx, id, node, progress)
		}, func(a *app, _ any) string {
			return a.paint(colGreen, " "+i18n.T(i18n.TUIBkReady, node)) + "\n"
		})
		t.intro = i18n.T(i18n.TUIBkWarning)
		return a.replace(t)
	}))
}

func pickBackup(id string, nodes []string) *listScreen {
	title := i18n.T(i18n.TUIBkRemove) + ": " + id
	l := &listScreen{screenBase: screenBase{title: title}, intro: i18n.T(i18n.TUIPickNode), empty: i18n.T(i18n.TUIBkNoBackups, id)}
	if len(nodes) > 1 {
		for _, n := range nodes[1:] {
			l.fixed = append(l.fixed, choice{label: n, value: n})
		}
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		node := c.value.(string)
		return a.replace(newConfirm(title, i18n.T(i18n.TUIBkRemoveLost, node, id, id, node), true, func(a *app) tea.Cmd {
			return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return nil, l.TunnelBackupRemove(ctx, id, node)
			}, textResult(i18n.T(i18n.TUIBkRemoved, node, id))))
		}))
	}
	return l
}

func pauseResume(a *app, t api.TunnelInfo) tea.Cmd {
	id, pause := t.ID, !t.Paused
	msg := i18n.T(i18n.TUIFoResumed, id)
	if pause {
		msg = i18n.T(i18n.TUIFoPaused, id)
	}
	return a.push(newTask(i18n.T(i18n.TUIFoPause)+": "+id, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		if pause {
			return nil, l.TunnelPause(ctx, id)
		}
		return nil, l.TunnelResume(ctx, id)
	}, textResult(msg)))
}

func resetTunnel(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUIFoReset) + ": " + id
	return a.push(newConfirm(title, i18n.T(i18n.TUIFoResetConfirm, id), false, func(a *app) tea.Cmd {
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.TunnelReset(ctx, id)
		}, textResult(i18n.T(i18n.TUIFoResetDone, id))))
	}))
}

func testLadder(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUIFoTest) + ": " + id
	rungs := max(1, len(t.Ladder)*max(1, len(t.Nodes)))
	minutes := (rungs*rungTestSeconds + 59) / 60
	about := i18n.T(i18n.TUIMinutes, minutes)
	if minutes == 1 {
		about = i18n.T(i18n.TUIMinute1)
	}
	return a.push(newConfirm(title, i18n.T(i18n.TUIFoTestLost, id, about), true, func(a *app) tea.Cmd {
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return l.TunnelTestLadder(ctx, id, progress)
		}, renderRungResults))
	}))
}

func renderRungResults(a *app, v any) string {
	rs, _ := v.([]api.RungResult)
	if len(rs) == 0 {
		return indent(i18n.T(i18n.TUIFoTestNoResults)) + "\n"
	}
	s := a.sym()
	nw, tw := 0, 0
	for _, r := range rs {
		nw, tw = max(nw, width(r.Node)), max(tw, width(r.Transport))
	}
	var b strings.Builder
	for _, r := range rs {
		var res string
		switch {
		case r.Skipped != "":
			res = a.paint(colGray, s.skip+" "+i18n.T(i18n.TUIFoTestSkipped, r.Skipped))
		case r.OK:
			res = a.paint(colGreen, s.ok) + " " + ms(r.RTTms)
		default:
			res = a.paint(colRed, s.fail)
			if r.Error != nil {
				res += " " + r.Error.Code + " " + r.Error.Message
			}
		}
		b.WriteString("  " + pad(r.Node, nw+2) + pad(r.Transport, tw+2) + res + "\n")
	}
	return b.String()
}

// editThresholds loads the tunnel's failover settings, then asks for new ones.
func editThresholds(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := i18n.T(i18n.TUIFoThresholds) + ": " + id
	load := newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.TunnelShow(ctx, id)
	}, nil)
	load.cancellable = true
	load.next = func(a *app, v any) screen {
		d, _ := v.(api.TunnelDetail)
		cur := d.Failover
		return newForm(title, i18n.T(i18n.TUIThIntro, id), thresholdFields(&cur, nil), func(a *app, vals map[string]string) tea.Cmd {
			f := thresholdsFrom(vals)
			f.Policy = cur.Policy
			if *f == cur {
				return a.back(i18n.T(i18n.TUINothingChanged))
			}
			return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
				return l.TunnelEdit(ctx, id, api.TunnelEditRequest{Failover: f}, progress)
			}, textResult(i18n.T(i18n.TUITunUpdated, id))))
		})
	}
	return a.push(load)
}
