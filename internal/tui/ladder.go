package tui

import (
	"context"
	"slices"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// ladderEditor orders the rungs of a ladder (Failover -> Ladder order and
// the Advanced Add tunnel wizard). It shows the section 8 guidance table and
// the client-IP property of every transport (section 10); proxy is the
// tunnel's advanced.proxy_protocol.
type ladderEditor struct {
	screenBase
	rungs  []string
	sel    int
	proxy  bool
	tr     map[string]api.TransportInfo
	err    error
	ch     chooser
	msg    string
	onSave func(a *app, rungs []string) tea.Cmd
}

func newLadderEditor(title string, rungs []string, proxy bool, onSave func(a *app, rungs []string) tea.Cmd) *ladderEditor {
	return &ladderEditor{
		screenBase: screenBase{title: title, help: i18n.TUIHelpLadder},
		rungs:      append([]string(nil), rungs...),
		sel:        -1,
		proxy:      proxy,
		onSave:     onSave,
	}
}

func (le *ladderEditor) start(a *app) tea.Cmd {
	return a.call(le, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return transportMap(ctx, l)
	})
}

func (le *ladderEditor) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		if d, ok := msg.payload.(donePayload); ok {
			le.err = d.err
			if m, ok := d.v.(map[string]api.TransportInfo); ok && d.err == nil {
				le.tr = m
			}
		}
		return nil
	case tea.KeyMsg:
		le.msg = ""
		switch msg.String() {
		// Letter shortcuts act on the selected rung; everything is also
		// reachable with numbers + Enter.
		case "u":
			le.move(le.sel, -1)
			return nil
		case "d":
			le.move(le.sel, 1)
			return nil
		case "x":
			le.remove(le.sel)
			return nil
		case "a":
			return le.addPicker(a)
		case "p":
			return a.push(le.profilePicker())
		case "s":
			return le.save(a)
		case "r":
			return le.start(a)
		}
		if submit, _ := le.ch.key(msg); !submit {
			return nil
		}
		n, raw, ok := le.ch.take()
		rungs := len(le.rungs)
		switch {
		case ok && n == 0:
			return a.pop()
		case ok && n >= 1 && n <= rungs:
			le.sel = n - 1
			return a.push(le.rungMenu(n - 1))
		case ok && n == rungs+1:
			return le.addPicker(a)
		case ok && n == rungs+2:
			return a.push(le.profilePicker())
		case ok && n == rungs+3:
			return le.save(a)
		case raw != "":
			le.msg = i18n.T(i18n.InvalidChoice, raw)
		}
	}
	return nil
}

// save hands the edited ladder to onSave (an empty ladder is refused).
func (le *ladderEditor) save(a *app) tea.Cmd {
	if len(le.rungs) == 0 {
		le.msg = i18n.T(i18n.TUILadEmpty)
		return nil
	}
	return le.onSave(a, append([]string(nil), le.rungs...))
}

// move swaps the rung at i with its neighbour at i+delta.
func (le *ladderEditor) move(i, delta int) {
	j := i + delta
	if i < 0 || j < 0 || i >= len(le.rungs) || j >= len(le.rungs) {
		return
	}
	le.rungs[i], le.rungs[j] = le.rungs[j], le.rungs[i]
	le.sel = j
}

// remove deletes the rung at i.
func (le *ladderEditor) remove(i int) {
	if i < 0 || i >= len(le.rungs) {
		return
	}
	le.rungs = slices.Delete(le.rungs, i, i+1)
	le.sel = min(i, len(le.rungs)-1)
}

// rungMenu is the numbered action list of one rung (numbers + Enter only,
// section 6): move up, move down, remove.
func (le *ladderEditor) rungMenu(i int) *listScreen {
	l := &listScreen{
		screenBase: screenBase{title: le.title, help: i18n.TUIHelpLadder},
		intro:      i18n.T(i18n.TUILadRungTitle, i+1, le.rungs[i]),
	}
	act := func(f func()) func(a *app) tea.Cmd {
		return func(a *app) tea.Cmd {
			f()
			return a.pop()
		}
	}
	l.fixed = []choice{
		{label: i18n.T(i18n.TUILadUp), value: act(func() { le.move(i, -1) })},
		{label: i18n.T(i18n.TUILadDown), value: act(func() { le.move(i, 1) })},
		{label: i18n.T(i18n.TUILadRemove), value: act(func() { le.remove(i) })},
	}
	l.pick = func(a *app, c choice) tea.Cmd { return c.value.(func(a *app) tea.Cmd)(a) }
	return l
}

// addPicker lists the known transports that are not in the ladder yet.
func (le *ladderEditor) addPicker(a *app) tea.Cmd {
	ids := make([]string, 0, len(le.tr))
	for id := range le.tr {
		if !slices.Contains(le.rungs, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		le.msg = i18n.T(i18n.TUILadNoneLeft)
		return nil
	}
	sort.Strings(ids)
	w := 0
	for _, id := range ids {
		w = max(w, width(id))
	}
	l := &listScreen{screenBase: screenBase{title: le.title}, intro: i18n.T(i18n.TUILadAdd)}
	for _, id := range ids {
		l.fixed = append(l.fixed, choice{label: strings.TrimRight(pad(id, w+2)+clientIPNote(le.tr, id, le.proxy), " "), value: id})
	}
	l.pick = func(a *app, c choice) tea.Cmd {
		le.rungs = append(le.rungs, c.value.(string))
		le.sel = len(le.rungs) - 1
		return a.pop()
	}
	return a.push(l)
}

// profilePicker loads the ladder profiles; choosing one replaces the rungs.
func (le *ladderEditor) profilePicker() *listScreen {
	return &listScreen{
		screenBase: screenBase{title: le.title},
		intro:      i18n.T(i18n.TUILadProfile),
		load: func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return l.LadderList(ctx)
		},
		derive: func(a *app, v any) []choice {
			ls, _ := v.([]api.Ladder)
			out := make([]choice, 0, len(ls))
			for _, x := range ls {
				label := x.Name + ": " + strings.Join(x.Rungs, " "+a.sym().arrow+" ")
				if x.Builtin {
					label += i18n.T(i18n.TUILadBuiltin)
				}
				out = append(out, choice{label: label, value: x.Rungs})
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd {
			le.rungs = append([]string(nil), c.value.([]string)...)
			le.sel = -1
			return a.pop()
		},
	}
}

// ladderGuide renders the section 8 guidance table.
func ladderGuide(a *app) string {
	rows := [][2]i18n.Key{
		{i18n.TUILadGuideIf, i18n.TUILadGuideDo},
		{i18n.TUILadG1If, i18n.TUILadG1Do},
		{i18n.TUILadG2If, i18n.TUILadG2Do},
		{i18n.TUILadG3If, i18n.TUILadG3Do},
		{i18n.TUILadG4If, i18n.TUILadG4Do},
	}
	w := 0
	for _, r := range rows {
		w = max(w, width(i18n.T(r[0])))
	}
	var b strings.Builder
	for i, r := range rows {
		line := "  " + pad(i18n.T(r[0]), w+3) + i18n.T(r[1])
		if i == 0 {
			line = a.bold(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (le *ladderEditor) view(a *app) string {
	var b strings.Builder
	b.WriteString(ladderGuide(a) + "\n")
	w := 0
	for _, r := range le.rungs {
		w = max(w, width(r))
	}
	for i, r := range le.rungs {
		mark := "  "
		if i == le.sel {
			mark = a.sym().sel + " "
		}
		line := mark + numLine(i+1, strings.TrimRight(pad(r, w+2)+clientIPNote(le.tr, r, le.proxy), " "))
		if i == le.sel {
			line = a.bold(line)
		}
		b.WriteString(line + "\n")
	}
	if len(le.rungs) == 0 {
		b.WriteString(a.paint(colYellow, "  "+i18n.T(i18n.TUILadEmpty)) + "\n")
	}
	n := len(le.rungs)
	b.WriteString("\n")
	b.WriteString("  " + numLine(n+1, i18n.T(i18n.TUILadAddItem)) + "\n")
	b.WriteString("  " + numLine(n+2, i18n.T(i18n.TUILadProfileItem)) + "\n")
	b.WriteString("  " + numLine(n+3, i18n.T(i18n.TUILadSaveItem)) + "\n")
	b.WriteString("  " + numLine(0, i18n.T(i18n.TUIBackItem)) + "\n")
	b.WriteString("\n" + a.paint(colGray, a.clip(" "+i18n.T(i18n.TUILadKeys))) + "\n")
	if le.err != nil {
		b.WriteString("\n" + a.errBlock(le.err))
	}
	if le.msg != "" {
		b.WriteString("\n" + a.paint(colYellow, " "+le.msg) + "\n")
	}
	b.WriteString("\n" + i18n.T(i18n.PromptChoice) + le.ch.input + "\n")
	return b.String()
}
