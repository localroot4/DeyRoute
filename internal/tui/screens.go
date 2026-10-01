package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// ---- list: sub-menus and pickers

// choice is one numbered entry of a list screen.
type choice struct {
	label string
	value any
}

// listScreen is a numbered list ("number + Enter", 0 = back) with an
// optional header loaded from the Local API. Sub-menus use fixed choices;
// pickers derive their choices from the loaded data.
type listScreen struct {
	screenBase
	intro   string
	load    localOp
	local   func(ctx context.Context) (any, error) // loads without the daemon (instead of load)
	header  func(a *app, v any) string
	derive  func(a *app, v any) []choice
	fixed   []choice
	empty   string
	pick    func(a *app, c choice) tea.Cmd
	data    any
	loaded  bool
	loading bool
	err     error
	ch      chooser
	msg     string
}

func (l *listScreen) start(a *app) tea.Cmd { return l.reload(a) }

func (l *listScreen) resume(a *app) tea.Cmd { return l.reload(a) }

func (l *listScreen) reload(a *app) tea.Cmd {
	switch {
	case l.local != nil:
		l.loading = true
		fn := l.local
		return a.run(l, callTimeout, func(ctx context.Context, _ func(any)) (any, error) { return fn(ctx) })
	case l.load == nil:
		return nil
	}
	l.loading = true
	return a.call(l, callTimeout, l.load)
}

func (l *listScreen) choices(a *app) []choice {
	if l.derive != nil {
		if !l.loaded {
			return nil
		}
		return l.derive(a, l.data)
	}
	return l.fixed
}

func (l *listScreen) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		if d, ok := msg.payload.(donePayload); ok {
			l.loading = false
			l.err = d.err
			if d.err == nil {
				l.data, l.loaded = d.v, true
			}
		}
	case tea.KeyMsg:
		if msg.String() == "r" {
			l.msg = ""
			return l.reload(a)
		}
		if submit, _ := l.ch.key(msg); !submit {
			return nil
		}
		n, raw, ok := l.ch.take()
		l.msg = ""
		if ok && n == 0 {
			return a.pop()
		}
		cs := l.choices(a)
		if !ok || n < 1 || n > len(cs) {
			if raw != "" {
				l.msg = i18n.T(i18n.InvalidChoice, raw)
			}
			return nil
		}
		return l.pick(a, cs[n-1])
	}
	return nil
}

func (l *listScreen) view(a *app) string {
	var b strings.Builder
	if l.intro != "" {
		b.WriteString(indent(l.intro) + "\n\n")
	}
	if l.err != nil {
		b.WriteString(a.errBlock(l.err) + "\n")
	}
	if l.header != nil && l.loaded {
		if h := l.header(a, l.data); h != "" {
			b.WriteString(h + "\n")
		}
	}
	cs := l.choices(a)
	switch {
	case l.loading && !l.loaded:
		b.WriteString(" " + i18n.T(i18n.Loading) + "\n")
	case len(cs) == 0 && l.empty != "" && l.err == nil:
		b.WriteString(indent(l.empty) + "\n")
	}
	for i, c := range cs {
		b.WriteString(a.clip(numLine(i+1, c.label)) + "\n")
	}
	b.WriteString(numLine(0, i18n.T(i18n.TUIBackItem)) + "\n")
	if l.msg != "" {
		b.WriteString("\n" + a.paint(colRed, " "+l.msg) + "\n")
	}
	b.WriteString("\n" + i18n.T(i18n.PromptChoice) + l.ch.input + "\n")
	return b.String()
}

// numLine renders " 1) label" with the number right-aligned in two columns.
func numLine(n int, label string) string { return fmt.Sprintf("%2d) %s", n, label) }

// menu builds a sub-menu. Items marked adv are only listed in Advanced mode
// and always come last so that the other numbers never move. Items with a
// role are only listed on a server with that role (hub or node).
type menuItem struct {
	label i18n.Key
	adv   bool
	role  string
	act   func(a *app) tea.Cmd
}

func newMenu(a *app, title i18n.Key, help i18n.Key, items []menuItem) *listScreen {
	l := &listScreen{screenBase: screenBase{title: itemName(title), help: help}}
	for _, it := range items {
		if (it.adv && !a.advanced) || (it.role != "" && it.role != a.role()) {
			continue
		}
		l.fixed = append(l.fixed, choice{label: i18n.T(it.label), value: it.act})
	}
	l.pick = func(a *app, c choice) tea.Cmd { return c.value.(func(a *app) tea.Cmd)(a) }
	return l
}

// ---- form: sequential text questions

// field is one question of a form. A field with opts is a numbered choice:
// the owner types the number of an option (or its value) and Enter on an
// empty line takes def.
type field struct {
	key      string
	label    string
	hint     string
	def      string
	masked   bool
	optional bool
	opts     []fieldOpt
	check    func(v string, vals map[string]string) error
	skip     func(vals map[string]string) bool
}

// fieldOpt is one numbered answer of a choice field: the value stored and
// the label listed ("transport_only - only transports (current)").
type fieldOpt struct{ value, label string }

// option returns the value of a choice answer: the option number, or an
// option's value typed out.
func (fl field) option(in string) (string, bool) {
	if n, err := strconv.Atoi(in); err == nil {
		if n >= 1 && n <= len(fl.opts) {
			return fl.opts[n-1].value, true
		}
		return "", false
	}
	for _, o := range fl.opts {
		if o.value != "" && strings.EqualFold(o.value, in) {
			return o.value, true
		}
	}
	return "", false
}

// shown is how an answer of fl is listed after it was given: the value,
// or for a choice whose value is empty ("none") its label.
func (fl field) shown(v string) string {
	if fl.masked {
		return strings.Repeat("*", len([]rune(v)))
	}
	if v == "" {
		for _, o := range fl.opts {
			if o.value == "" {
				return o.label
			}
		}
	}
	return v
}

// defNumber is the number of the option def selects (0 = none).
func (fl field) defNumber() int {
	for i, o := range fl.opts {
		if o.value == fl.def {
			return i + 1
		}
	}
	return 0
}

// formScreen asks its fields one after the other. Enter on an empty line
// takes the default; Esc cancels; "?" on an empty line shows the help.
// submit decides where to go next.
type formScreen struct {
	screenBase
	intro  string
	fields []field
	idx    int
	vals   map[string]string
	input  string
	err    error
	submit func(a *app, vals map[string]string) tea.Cmd
}

func newForm(title string, intro string, fields []field, submit func(a *app, vals map[string]string) tea.Cmd) *formScreen {
	return &formScreen{
		screenBase: screenBase{title: title, help: i18n.TUIHelpForm, typing: true},
		intro:      intro, fields: fields, vals: map[string]string{}, submit: submit,
	}
}

func (f *formScreen) start(a *app) tea.Cmd { return f.advance(a) }

func (f *formScreen) inputEmpty() bool { return f.input == "" }

// advance skips fields that do not apply; after the last field it submits.
func (f *formScreen) advance(a *app) tea.Cmd {
	for f.idx < len(f.fields) && f.fields[f.idx].skip != nil && f.fields[f.idx].skip(f.vals) {
		f.idx++
	}
	if f.idx >= len(f.fields) {
		return f.submit(a, f.vals)
	}
	return nil
}

func (f *formScreen) update(a *app, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok || f.idx >= len(f.fields) {
		return nil
	}
	if k.Type != tea.KeyEnter {
		editLine(&f.input, k)
		return nil
	}
	fl := f.fields[f.idx]
	v := strings.TrimSpace(f.input)
	if fl.masked {
		v = f.input
	}
	switch {
	case v == "":
		v = fl.def
	case len(fl.opts) > 0:
		val, ok := fl.option(v)
		if !ok {
			f.err = uiErr(i18n.InvalidChoice, v)
			return nil
		}
		v = val
	}
	if v == "" && !fl.optional {
		f.err = uiErr(i18n.TUIRequired)
		return nil
	}
	if fl.check != nil {
		if err := fl.check(v, f.vals); err != nil {
			f.err = err
			return nil
		}
	}
	f.err = nil
	f.vals[fl.key] = v
	f.input = ""
	f.idx++
	return f.advance(a)
}

func (f *formScreen) view(a *app) string {
	var b strings.Builder
	if f.intro != "" {
		b.WriteString(indent(f.intro) + "\n\n")
	}
	answered := false
	for i := 0; i < f.idx && i < len(f.fields); i++ {
		fl := f.fields[i]
		if v, ok := f.vals[fl.key]; ok {
			b.WriteString(" " + fl.label + ": " + fl.shown(v) + "\n")
			answered = true
		}
	}
	if f.idx < len(f.fields) {
		fl := f.fields[f.idx]
		if answered && (fl.hint != "" || len(fl.opts) > 0) {
			b.WriteString("\n") // the intro already ends with a blank line
		}
		if fl.hint != "" {
			b.WriteString(a.paint(colGray, indent(fl.hint)) + "\n")
		}
		in := f.input
		if fl.masked {
			in = strings.Repeat("*", len([]rune(in)))
		}
		if len(fl.opts) > 0 {
			b.WriteString(" " + fl.label + ":\n")
			for i, o := range fl.opts {
				b.WriteString(a.clip(numLine(i+1, o.label)) + "\n")
			}
			prompt := i18n.T(i18n.PromptChoice)
			if n := fl.defNumber(); n > 0 {
				prompt = i18n.T(i18n.TUIChoiceDefault, n)
			}
			b.WriteString(prompt + in + "_\n")
		} else {
			def := ""
			if fl.def != "" && !fl.masked {
				def = " [" + fl.def + "]"
			}
			b.WriteString(" " + fl.label + def + ": " + in + "_\n")
		}
	}
	if f.err != nil {
		b.WriteString("\n" + a.errBlock(f.err))
	}
	return b.String()
}

// ---- confirm

// confirmScreen states what an action does (and what is lost). Destructive
// actions (typed) need the word yes typed exactly; others take Enter.
type confirmScreen struct {
	screenBase
	text  string
	typed bool
	input string
	yes   func(a *app) tea.Cmd
}

func newConfirm(title, text string, typed bool, yes func(a *app) tea.Cmd) *confirmScreen {
	return &confirmScreen{screenBase: screenBase{title: title, help: i18n.TUIHelpConfirm, typing: typed}, text: text, typed: typed, yes: yes}
}

func (c *confirmScreen) inputEmpty() bool { return c.input == "" }

func (c *confirmScreen) update(a *app, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if !c.typed {
		// 1) Continue, 0) Cancel; Enter alone continues. 0 is Back on
		// every other page, so it must never run the action.
		switch k.Type {
		case tea.KeyEnter:
			in := strings.TrimSpace(c.input)
			c.input = ""
			if in == "" || in == "1" {
				return c.yes(a)
			}
			return a.back(i18n.T(i18n.CLIAborted))
		case tea.KeyRunes:
			if len(c.input) < 3 {
				c.input += string(k.Runes)
			}
		case tea.KeyBackspace:
			editLine(&c.input, k)
		}
		return nil
	}
	if k.Type != tea.KeyEnter {
		editLine(&c.input, k)
		return nil
	}
	if strings.TrimSpace(c.input) == "yes" {
		return c.yes(a)
	}
	return a.back(i18n.T(i18n.CLIAborted))
}

func (c *confirmScreen) view(a *app) string {
	var b strings.Builder
	b.WriteString(a.paint(colYellow, indent(c.text)) + "\n\n")
	if c.typed {
		b.WriteString(" " + i18n.T(i18n.CLITypeYes) + c.input + "_\n")
	} else {
		b.WriteString(i18n.T(i18n.TUIConfirmChoice) + c.input + "_\n")
	}
	return b.String()
}

// ---- task: progress / result

// taskScreen runs one action and shows its progress steps, then its result
// or the DEY error with "1) Retry". Read-only views are refreshable with r.
type taskScreen struct {
	screenBase
	intro       string
	op          localOp
	local       func(ctx context.Context) (any, error) // runs without the daemon
	timeout     time.Duration
	render      func(a *app, v any) string
	onOK        func(a *app, v any)
	after       func(a *app, v any) tea.Cmd // runs on success (e.g. a plain page)
	next        func(a *app, v any) screen  // replaces the task on success (nil = stay)
	refreshable bool
	// option offers one action on a successful result, chosen with 1
	// ("Open it in the firewall"): its label ("" = none) and what it does.
	// An action that changes what the task shows sets stale: the task then
	// runs again when it is the top screen again.
	option func(a *app, v any) (label string, act func(a *app) tea.Cmd)
	stale  bool
	// cancellable read-only tasks may be left while they run (leaving
	// cancels the call). Other running tasks change something on the
	// server: q/Esc does not abandon them half-way.
	cancellable bool
	held        bool // q/Esc was pressed while the change was running
	steps       []api.Step
	running     bool
	done        bool
	val         any
	err         error
	ch          chooser
}

func newTask(title string, timeout time.Duration, op localOp, render func(a *app, v any) string) *taskScreen {
	return &taskScreen{screenBase: screenBase{title: title, help: i18n.TUIHelpTask}, op: op, timeout: timeout, render: render}
}

// newLocalTask runs fn without the daemon (doctor, backup, uninstall).
func newLocalTask(title string, fn func(ctx context.Context) (any, error), render func(a *app, v any) string) *taskScreen {
	return &taskScreen{screenBase: screenBase{title: title, help: i18n.TUIHelpTask}, local: fn, timeout: longTimeout, render: render}
}

// textResult returns a render function that prints a fixed message.
func textResult(msg string) func(*app, any) string {
	return func(*app, any) string { return indent(msg) + "\n" }
}

func (t *taskScreen) start(a *app) tea.Cmd { return t.launch(a) }

// resume runs the task again after an option changed what it shows.
func (t *taskScreen) resume(a *app) tea.Cmd {
	if !t.stale || t.running {
		return nil
	}
	t.stale = false
	return t.launch(a)
}

// optionItem is the result's option (see taskScreen.option), if any.
func (t *taskScreen) optionItem(a *app) (string, func(a *app) tea.Cmd) {
	if t.option == nil || !t.done || t.err != nil {
		return "", nil
	}
	return t.option(a, t.val)
}

// back keeps a running change on screen: leaving would cancel it half-way
// (for example a tunnel created on the hub but not on the node). Read-only
// views stay cancellable; ctrl+c still quits.
func (t *taskScreen) back(*app) bool {
	if t.running && !t.refreshable && !t.cancellable {
		t.held = true
		return true
	}
	return false
}

func (t *taskScreen) launch(a *app) tea.Cmd {
	t.steps, t.running, t.done, t.val, t.err, t.held = nil, true, false, nil, nil, false
	if t.local != nil {
		fn := t.local
		return a.run(t, t.timeout, func(ctx context.Context, _ func(any)) (any, error) { return fn(ctx) })
	}
	return a.call(t, t.timeout, t.op)
}

func (t *taskScreen) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		switch p := msg.payload.(type) {
		case stepPayload:
			t.upsert(api.Step(p))
		case donePayload:
			t.running, t.done, t.val, t.err, t.held = false, true, p.v, p.err, false
			if p.err == nil {
				if t.onOK != nil {
					t.onOK(a, p.v)
				}
				if t.after != nil {
					if c := t.after(a, p.v); c != nil {
						return c
					}
				}
				if t.next != nil {
					if s := t.next(a, p.v); s != nil {
						return a.replace(s)
					}
				}
			}
		}
	case tea.KeyMsg:
		if t.running {
			return nil
		}
		if msg.String() == "r" {
			if t.err != nil || t.refreshable {
				return t.launch(a)
			}
			return nil
		}
		if submit, _ := t.ch.key(msg); !submit {
			return nil
		}
		n, _, ok := t.ch.take()
		switch {
		case !ok || n == 0:
			return a.pop()
		case n == 1 && t.err != nil:
			return t.launch(a)
		case n == 1:
			if label, act := t.optionItem(a); label != "" {
				return act(a)
			}
		}
	}
	return nil
}

func (t *taskScreen) upsert(st api.Step) {
	for i := range t.steps {
		if t.steps[i].ID == st.ID && st.ID != "" {
			t.steps[i] = st
			return
		}
	}
	t.steps = append(t.steps, st)
}

func (t *taskScreen) view(a *app) string {
	var b strings.Builder
	if t.intro != "" {
		b.WriteString(a.paint(colYellow, indent(t.intro)) + "\n\n")
	}
	for _, st := range t.steps {
		b.WriteString(a.stepLine(st) + "\n")
	}
	if len(t.steps) > 0 {
		b.WriteString("\n")
	}
	switch {
	case t.running:
		b.WriteString(" " + i18n.T(i18n.TUIWorking) + "\n")
		if t.held {
			b.WriteString("\n" + a.paint(colYellow, indent(i18n.T(i18n.TUIStillRunning))) + "\n")
		}
	case t.err != nil:
		b.WriteString(a.errBlock(t.err))
		b.WriteString("\n" + i18n.T(i18n.TUIRetryHint) + "\n")
	case t.done:
		if t.render != nil {
			b.WriteString(t.render(a, t.val))
		}
		if label, _ := t.optionItem(a); label != "" {
			b.WriteString("\n" + numLine(1, label) + "\n" + numLine(0, i18n.T(i18n.TUIBackItem)) + "\n")
			b.WriteString("\n" + i18n.T(i18n.PromptChoice) + t.ch.input + "\n")
			break
		}
		b.WriteString("\n " + i18n.T(i18n.TUIPressEnterBack) + "\n")
	}
	return b.String()
}
