// Package tui is the Bubble Tea front-end. It holds no management logic:
// every action is one Local API call, the same one the CLI makes (spec
// section 6). Screens form a stack on top of the fixed main menu; network
// calls run as tea.Cmds and report back through routed messages.
package tui

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
)

// Timeouts of Local API calls started by the TUI.
const (
	callTimeout    = 30 * time.Second
	checkTimeout   = 2 * time.Minute
	longTimeout    = 30 * time.Minute
	defaultRefresh = 2 * time.Second
	defaultHeight  = 24
)

// Options configures the TUI. Only Caps is required; without Local every
// daemon action shows DEY-X003 (or LocalErr) while the menu stays usable.
type Options struct {
	Caps     Caps
	Status   BannerStatus // initial banner data (refreshed from Status())
	Advanced bool         // initial UI mode (refreshed from Status())

	// Local is the connected Local API; nil when the daemon is not running.
	Local api.Local
	// LocalErr is the error of api.Dial, shown instead of the generic DEY-X003.
	LocalErr error
	// Service is named in the DEY-X003 fix line (default "deyroute-hub").
	Service string
	// NotSetUp is set when the server has no /etc/deyroute/config.yaml: the
	// main screen explains how to set it up instead of a daemon error.
	NotSetUp bool

	// Local operations run by the cli package without the daemon. A nil
	// function makes the item answer "not available here".
	Doctor func(ctx context.Context) (summary, path string, err error)
	Backup func(ctx context.Context, out, passphrase string, noEncrypt bool) (string, error)
	// Restore returns what the owner must know afterwards (a changed hub
	// address, the next steps); "" = the generic "Restore complete".
	Restore   func(ctx context.Context, path, passphrase string) (string, error)
	Uninstall func(ctx context.Context, keepBackups, nodes bool) error

	// Now and Location format dashboard times (default time.Now, time.Local).
	Now      func() time.Time
	Location *time.Location
	// Refresh is the dashboard refresh interval (default 2s).
	Refresh time.Duration
	// Height is the initial terminal height (default 24; updated on resize).
	Height int
	// In and Out are the terminal (default os.Stdin and os.Stdout).
	In  io.Reader
	Out io.Writer

	// tick schedules a delayed message (tests replace it).
	tick func(d time.Duration, msg tea.Msg) tea.Cmd
}

// Model is the root Bubble Tea model. It is a thin handle on the shared
// application state so that value copies made by Bubble Tea stay coherent.
type Model struct{ a *app }

// app is the mutable state of the TUI. It is only touched from Update/View
// (the Bubble Tea goroutine); background work communicates through messages.
type app struct {
	opts   Options
	caps   Caps
	height int
	// sized is set once the terminal reported its size (fit budgets the
	// page height only then).
	sized    bool
	status   BannerStatus
	advanced bool
	stack    []screen
	nextID   int
	flash    string
	quit     bool
	// helpKey is the help text shown over the top screen after "?"; the
	// stack is untouched, so background results keep reaching their screens.
	helpKey i18n.Key
	// lineMode is set on terminals that cannot position the cursor
	// (TERM=dumb): the view is printed after each answer and the dashboard
	// refreshes on r instead of every 2 seconds.
	lineMode bool
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewModel builds the root model showing the main menu.
func NewModel(o Options) Model {
	if o.Service == "" {
		o.Service = "deyroute-hub"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.Refresh <= 0 {
		o.Refresh = defaultRefresh
	}
	if o.tick == nil {
		o.tick = func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		}
	}
	h := o.Height
	if h <= 0 {
		h = defaultHeight
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &app{opts: o, caps: o.Caps, height: h, status: o.Status, advanced: o.Advanced, ctx: ctx, cancel: cancel}
	a.push(newRoot())
	return Model{a: a}
}

// Init implements tea.Model: it loads the banner data from Status().
func (m Model) Init() tea.Cmd {
	if m.a.opts.Local == nil {
		return nil
	}
	return m.a.stack[0].(*rootScreen).load(m.a)
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	a := m.a
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.caps.Width = msg.Width
		a.height, a.sized = msg.Height, true
		return m, nil
	case tea.KeyMsg:
		return m, a.key(msg)
	case asyncMsg:
		s := a.find(msg.sid)
		if s == nil || s.base().gen != msg.gen {
			return m, nil
		}
		return m, tea.Batch(s.update(a, msg), msg.next)
	case tickMsg:
		if s := a.find(msg.sid); s != nil {
			return m, s.update(a, msg)
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() string {
	a := m.a
	if a.quit {
		return ""
	}
	var b strings.Builder
	st := a.status
	st.Advanced = a.advanced
	banner := strings.Split(Banner(a.caps, st), "\n")
	for _, l := range banner {
		b.WriteString(a.paint(colCyan, l) + "\n")
	}
	b.WriteString("\n")
	top := a.top()
	switch {
	case a.helpKey != "":
		b.WriteString(" " + a.bold(i18n.T(i18n.HelpTitle)) + "\n\n")
		b.WriteString(indent(i18n.T(a.helpKey)) + "\n\n " + i18n.T(i18n.PressBack) + "\n")
	case len(a.stack) > 1:
		b.WriteString(" " + a.bold(top.base().title) + "\n\n")
		b.WriteString(top.view(a))
	default:
		b.WriteString(top.view(a))
	}
	if a.flash != "" {
		b.WriteString("\n" + a.paint(colYellow, " "+a.flash) + "\n")
	}
	footer := i18n.T(i18n.FooterKeys)
	if !a.caps.Unicode {
		footer = i18n.T(i18n.FooterKeysASCII)
	}
	b.WriteString("\n" + a.paint(colGray, footer) + "\n")
	out := b.String()
	if !a.caps.Unicode {
		out = asciiOnly(out)
	}
	if a.lineMode {
		return out // a dumb terminal wraps and scrolls by itself
	}
	return a.fit(out, len(banner)-1)
}

// Run starts the full-screen menu and blocks until the operator leaves it.
// On a dumb terminal (Caps.Dumb) it runs the same screens in line mode.
func Run(o Options) error {
	if o.Caps.Dumb {
		if o.In == nil {
			o.In = os.Stdin
		}
		if o.Out == nil {
			o.Out = os.Stdout
		}
		return runLines(o)
	}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if o.In != nil { // default: stdin, or the controlling TTY when stdin is redirected
		opts = append(opts, tea.WithInput(o.In))
	}
	if o.Out != nil {
		opts = append(opts, tea.WithOutput(o.Out))
	}
	m := NewModel(o)
	defer m.a.cancel()
	_, err := tea.NewProgram(m, opts...).Run()
	return err
}

// ---- navigation

// screenBase is embedded by every screen.
type screenBase struct {
	id       int
	title    string
	help     i18n.Key
	typing   bool // free text input: q, r and ? are ordinary characters
	gen      int  // generation of the current background operation
	ctx      context.Context
	cancel   context.CancelFunc
	opCancel context.CancelFunc
}

func (b *screenBase) base() *screenBase  { return b }
func (b *screenBase) start(*app) tea.Cmd { return nil }
func (b *screenBase) ctxOrBackground() context.Context {
	if b.ctx == nil {
		return context.Background()
	}
	return b.ctx
}

// screen is one page of the TUI.
type screen interface {
	base() *screenBase
	// start runs when the screen is pushed.
	start(a *app) tea.Cmd
	// update handles keys (only while on top) and messages addressed to it.
	update(a *app, msg tea.Msg) tea.Cmd
	// view renders the page body (the banner and footer are added around it).
	view(a *app) string
}

// resumer screens reload when they become the top screen again.
type resumer interface{ resume(a *app) tea.Cmd }

// backer screens may consume q/Esc themselves (e.g. to leave a sub-step).
type backer interface{ back(a *app) bool }

func (a *app) top() screen { return a.stack[len(a.stack)-1] }

func (a *app) find(id int) screen {
	for _, s := range a.stack {
		if s.base().id == id {
			return s
		}
	}
	return nil
}

// push shows s on top of the stack.
func (a *app) push(s screen) tea.Cmd {
	b := s.base()
	a.nextID++
	b.id = a.nextID
	b.ctx, b.cancel = context.WithCancel(a.ctx)
	if b.help == "" {
		b.help = i18n.TUIHelpList
	}
	a.stack = append(a.stack, s)
	return s.start(a)
}

// pop closes the top screen (cancelling its work); on the main menu it quits.
func (a *app) pop() tea.Cmd {
	if len(a.stack) <= 1 {
		return a.exit()
	}
	a.drop()
	if r, ok := a.top().(resumer); ok {
		return r.resume(a)
	}
	return nil
}

// replace swaps the top screen for s (used by confirm/form/task chains).
func (a *app) replace(s screen) tea.Cmd {
	if len(a.stack) > 1 {
		a.drop()
	}
	return a.push(s)
}

func (a *app) drop() {
	b := a.top().base()
	if b.cancel != nil {
		b.cancel()
	}
	a.stack = a.stack[:len(a.stack)-1]
}

// back pops the top screen and shows msg as a one-line note.
func (a *app) back(msg string) tea.Cmd {
	cmd := a.pop()
	a.flash = msg
	return cmd
}

func (a *app) exit() tea.Cmd {
	a.quit = true
	a.cancel()
	return tea.Quit
}

func (a *app) key(k tea.KeyMsg) tea.Cmd {
	if k.Type == tea.KeyCtrlC {
		return a.exit()
	}
	s := k.String()
	if a.helpKey != "" {
		// The help page closes with Enter, q, Esc or ? and ignores other keys.
		if k.Type == tea.KeyEnter || k.Type == tea.KeyEsc || s == "q" || s == "?" {
			a.helpKey = ""
		}
		return nil
	}
	a.flash = ""
	top := a.top()
	b := top.base()
	if k.Type == tea.KeyEsc || (!b.typing && s == "q") {
		if bk, ok := top.(backer); ok && bk.back(a) {
			return nil
		}
		return a.pop()
	}
	if !b.typing && s == "?" {
		a.helpKey = b.help
		return nil
	}
	return top.update(a, k)
}

// ---- background work

// asyncMsg carries one result or progress item of a background operation to
// the screen that started it. next keeps reading the operation's stream.
type asyncMsg struct {
	sid, gen int
	payload  any
	next     tea.Cmd
}

// tickMsg is a delayed refresh for screen sid.
type tickMsg struct{ sid, seq int }

// donePayload ends an operation.
type donePayload struct {
	v   any
	err error
}

// stepPayload is one progress step.
type stepPayload api.Step

// logPayload is one streamed log line.
type logPayload api.LogLine

type opFunc func(ctx context.Context, send func(any)) (any, error)

// run starts fn in the background for screen s, cancelling the previous
// operation of s. Intermediate payloads passed to send and the final
// donePayload arrive as asyncMsgs. timeout 0 means no timeout (streams).
func (a *app) run(s screen, timeout time.Duration, fn opFunc) tea.Cmd {
	b := s.base()
	if b.opCancel != nil {
		b.opCancel()
	}
	b.gen++
	sid, gen := b.id, b.gen
	alive, stop := context.WithCancel(b.ctxOrBackground())
	b.opCancel = stop
	return func() tea.Msg {
		ch := make(chan asyncMsg, 32)
		go func() {
			defer close(ch)
			ctx, cancel := alive, context.CancelFunc(func() {})
			if timeout > 0 {
				ctx, cancel = context.WithTimeout(alive, timeout)
			}
			defer cancel()
			send := func(p any) {
				select {
				case ch <- asyncMsg{sid: sid, gen: gen, payload: p}:
				case <-alive.Done():
				}
			}
			v, err := fn(ctx, send)
			send(donePayload{v: v, err: err})
		}()
		return recv(ch)()
	}
}

func recv(ch <-chan asyncMsg) tea.Cmd {
	return func() tea.Msg {
		m, ok := <-ch
		if !ok {
			return nil
		}
		m.next = recv(ch)
		return m
	}
}

// localOp is an operation on the Local API.
type localOp func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error)

// call runs op against the Local API; without a daemon it fails with the
// daemon error at once.
func (a *app) call(s screen, timeout time.Duration, op localOp) tea.Cmd {
	l := a.opts.Local
	derr := a.daemonErr()
	return a.run(s, timeout, func(ctx context.Context, send func(any)) (any, error) {
		if l == nil {
			return nil, derr
		}
		return op(ctx, l, func(st api.Step) { send(stepPayload(st)) })
	})
}

// daemonErr is the error shown when the daemon is unreachable.
func (a *app) daemonErr() error {
	if a.opts.NotSetUp {
		return deyerr.New(deyerr.I023, nil)
	}
	if a.opts.LocalErr != nil {
		return a.opts.LocalErr
	}
	return deyerr.New(deyerr.X003, deyerr.Params{"service": a.opts.Service})
}

// applyStatus refreshes the banner and UI mode from a Status answer.
func (a *app) applyStatus(st api.Status) {
	a.status.Role = st.Role
	if st.Hub != nil {
		a.status.Name = st.Hub.Name
		a.status.PublicIP = st.Hub.PublicIP
		a.advanced = st.Hub.UIMode == uiAdvanced
	}
	if st.NodeSelf != nil {
		a.status.Name = st.NodeSelf.ID
		a.status.HubAddr = st.NodeSelf.HubAddr
	}
	a.status.Nodes = len(st.Nodes)
	up := 0
	for _, t := range st.Tunnels {
		if t.Enabled && t.State == stUp {
			up++
		}
	}
	a.status.TunnelsUp = up
}

// ---- main menu

// rootScreen is the fixed main menu of section 6.
type rootScreen struct {
	screenBase
	ch  chooser
	msg string
	err error
}

func newRoot() *rootScreen {
	return &rootScreen{screenBase: screenBase{help: i18n.HelpMainMenu}}
}

func (r *rootScreen) load(a *app) tea.Cmd {
	return a.call(r, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.Status(ctx)
	})
}

func (r *rootScreen) resume(a *app) tea.Cmd {
	if a.opts.Local == nil {
		return nil
	}
	return r.load(a)
}

func (r *rootScreen) back(*app) bool {
	if r.ch.input != "" {
		r.ch.input = ""
		return true
	}
	return false
}

func (r *rootScreen) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		if d, ok := msg.payload.(donePayload); ok {
			r.err = d.err
			if st, ok := d.v.(api.Status); ok && d.err == nil {
				a.applyStatus(st)
			}
		}
	case tea.KeyMsg:
		if msg.String() == "r" {
			r.msg = ""
			return r.resume(a)
		}
		submit, _ := r.ch.key(msg)
		if !submit {
			return nil
		}
		n, raw, ok := r.ch.take()
		r.msg = ""
		if !ok {
			if raw != "" {
				r.msg = i18n.T(i18n.InvalidChoice, raw)
			}
			return nil
		}
		if n == 0 {
			return a.exit()
		}
		open := mainScreens(n)
		if open == nil {
			r.msg = i18n.T(i18n.InvalidChoice, raw)
			return nil
		}
		return a.push(open(a))
	}
	return nil
}

func (r *rootScreen) view(a *app) string {
	var b strings.Builder
	switch {
	case a.opts.Local == nil && a.opts.NotSetUp:
		b.WriteString(a.notSetUpPanel() + "\n\n")
	case a.opts.Local == nil:
		b.WriteString(a.errBlock(a.daemonErr()) + "\n")
	case r.err != nil:
		b.WriteString(a.errBlock(r.err) + "\n")
	}
	b.WriteString(RenderMenu(a.caps, MainMenu(), a.advanced))
	if r.msg != "" {
		b.WriteString("\n" + a.paint(colRed, r.msg) + "\n")
	}
	b.WriteString("\n" + i18n.T(i18n.PromptChoice) + r.ch.input + "\n")
	return b.String()
}

// mainScreens maps a main-menu number to its screen constructor.
func mainScreens(n int) func(a *app) screen {
	switch n {
	case 1:
		return newDashboard
	case 2:
		return tunnelsMenu
	case 3:
		return nodesMenu
	case 4:
		return portsMenu
	case 5:
		return failoverMenu
	case 6:
		return diagMenu
	case 7:
		return optimizeMenu
	case 8:
		return securityMenu
	case 9:
		return notifyMenu
	case 10:
		return backupMenu
	case 11:
		return updateMenu
	case 12:
		return settingsMenu
	}
	return nil
}

// notSetUpPanel is the first screen of a server that is not set up yet:
// what to run on the Iran server and on a foreign server.
func (a *app) notSetUpPanel() string {
	lines := []string{
		" " + a.bold(i18n.T(i18n.TUINotSetUpTitle)),
		"   " + i18n.T(i18n.TUINotSetUpHub),
		"   " + i18n.T(i18n.TUINotSetUpNode),
	}
	return a.clip(strings.Join(lines, "\n"))
}
