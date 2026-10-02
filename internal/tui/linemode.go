package tui

import (
	"bufio"
	"io"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/i18n"
)

// Line mode timings.
const (
	lineSettle   = 150 * time.Millisecond // wait for quick answers before printing
	lineMinPrint = time.Second            // at most one unasked reprint per second
	lineStopWait = 5 * time.Second        // wait for background calls on exit
	lineFlushMax = 3 * time.Second        // longest wait for a pending page before the next answer
)

// runLines runs the TUI on a terminal that cannot position the cursor
// (TERM=dumb, scenario S24): every page is printed as plain lines and each
// input line is one answer ("3" + Enter picks item 3, "q" goes back, an
// empty line is Enter: it takes a default or continues where Enter does,
// "?" shows the help). The screens and the Local API calls are
// exactly those of the full-screen mode; only the dashboard does not refresh
// by itself (r refreshes it) so that the output does not scroll away.
func runLines(o Options) error {
	o.tick = func(time.Duration, tea.Msg) tea.Cmd { return nil }
	o.Caps.Color = false // a dumb terminal shows escape codes literally
	m := NewModel(o)
	m.a.lineMode = true
	r := &lineRunner{m: m, out: o.Out, msgs: make(chan tea.Msg, 64), done: make(chan struct{}), echo: echoControl(o.In)}
	return r.run(o.In)
}

// lineRunner feeds input lines and command results into the model, like the
// Bubble Tea program loop does, and prints the view when it changes.
type lineRunner struct {
	m    Model
	out  io.Writer
	msgs chan tea.Msg
	done chan struct{}
	wg   sync.WaitGroup
	last string
	// echo switches terminal echo (nil when the input is not a terminal).
	echo func(on bool)
}

func (r *lineRunner) run(in io.Reader) error {
	lines := make(chan string)
	go readLines(in, lines, r.done) // may stay blocked in Read until the process exits

	defer r.stop()
	r.exec(r.m.Init())

	var (
		// The first page waits briefly for the banner data (Status).
		settle  = time.After(lineSettle)
		asked   = true    // the pending print answers an input line
		printed time.Time // time of the last print
	)
	for {
		select {
		case line, ok := <-lines:
			if settle != nil && asked {
				// Piped input: the page that answers the previous line is
				// printed before the next one is applied (and at the end).
				if r.flush() {
					return nil
				}
				printed = time.Now()
			}
			if !ok {
				return nil // end of input
			}
			for _, k := range lineKeys(r.m.a, line) {
				if r.handle(k) {
					return nil
				}
			}
			asked = true
			settle = time.After(lineSettle)
		case msg := <-r.msgs:
			if r.handle(msg) {
				return nil
			}
			if settle == nil {
				wait := lineSettle
				if next := time.Until(printed.Add(lineMinPrint)); next > wait {
					wait = next
				}
				settle = time.After(wait)
			}
		case <-settle:
			if r.print(asked) {
				printed = time.Now()
			}
			settle, asked = nil, false
		}
	}
}

// flush applies the messages that arrive until none came for lineSettle
// (at most lineFlushMax), then prints the page; it reports whether the TUI
// quit meanwhile.
func (r *lineRunner) flush() bool {
	deadline := time.After(lineFlushMax)
	for {
		select {
		case msg := <-r.msgs:
			if r.handle(msg) {
				return true
			}
			continue
		case <-time.After(lineSettle):
		case <-deadline:
		}
		r.print(true)
		return false
	}
}

// stop cancels every call of the model and waits (bounded) for the
// commands still running.
func (r *lineRunner) stop() {
	if r.echo != nil {
		r.echo(true)
	}
	r.m.a.cancel()
	close(r.done)
	finished := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(lineStopWait):
	}
}

// exec runs cmd in the background and queues its message.
func (r *lineRunner) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		msg := cmd()
		if msg == nil {
			return
		}
		select {
		case r.msgs <- msg:
		case <-r.done:
		}
	}()
}

// handle applies one message; it reports whether the TUI quit.
func (r *lineRunner) handle(msg tea.Msg) bool {
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			r.exec(c)
		}
	case tea.QuitMsg:
		return true
	default:
		nm, cmd := r.m.Update(msg)
		r.m = nm.(Model)
		r.exec(cmd)
	}
	return r.m.a.quit
}

// print writes the view when it changed (or always when force is set) and
// reports whether it wrote anything.
func (r *lineRunner) print(force bool) bool {
	v := r.m.View()
	if v == "" || (!force && v == r.last) {
		return false
	}
	r.last = v
	if r.echo != nil {
		r.echo(!r.m.a.maskedInput()) // a passphrase is never echoed
	}
	hint := i18n.T(i18n.TUILineModeHint)
	_, err := io.WriteString(r.out, "\n"+v+asciiOnly(hint)+"\n")
	return err == nil
}

// maskedInput reports whether the current question is a masked field.
func (a *app) maskedInput() bool {
	f, ok := a.top().(*formScreen)
	return ok && a.helpKey == "" && f.idx < len(f.fields) && f.fields[f.idx].masked
}

// readLines sends every input line to out until in ends or done closes.
func readLines(in io.Reader, out chan<- string, done <-chan struct{}) {
	defer close(out)
	br := bufio.NewReader(in)
	for {
		s, err := br.ReadString('\n')
		if s != "" || err == nil {
			select {
			case out <- strings.TrimRight(s, "\r\n"):
			case <-done:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// lineKeyNames are the named keys accepted as a whole line on screens
// without a text field (log scrolling).
var lineKeyNames = map[string]tea.KeyType{
	"up": tea.KeyUp, "down": tea.KeyDown, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	"home": tea.KeyHome, "end": tea.KeyEnd,
}

// lineKeys translates one input line into key messages: an empty line is
// Enter, "q" (or "esc") goes back, a single letter such as r or ? is that
// key alone, anything else is typed and followed by Enter. In a text field
// every line is the whole answer: what a rejected answer left in the field
// is cleared first, and "?" alone shows the help (on a masked field it is
// the answer, like any other secret).
func lineKeys(a *app, line string) []tea.KeyMsg {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	typing := a.helpKey == "" && a.top().base().typing
	trimmed := strings.TrimSpace(line)
	clear := tea.KeyMsg{Type: tea.KeyCtrlU}
	switch {
	case typing && trimmed == "?" && !secretField(a.top()):
		return []tea.KeyMsg{clear, {Type: tea.KeyRunes, Runes: []rune("?")}}
	case typing && trimmed == "":
		return []tea.KeyMsg{clear, enter}
	case trimmed == "":
		return []tea.KeyMsg{enter}
	case trimmed == "esc" || (!typing && trimmed == "q"):
		return []tea.KeyMsg{{Type: tea.KeyEsc}}
	case !typing:
		if t, ok := lineKeyNames[trimmed]; ok {
			return []tea.KeyMsg{{Type: t}}
		}
		if r := []rune(trimmed); len(r) == 1 && (r[0] < '0' || r[0] > '9') {
			return []tea.KeyMsg{{Type: tea.KeyRunes, Runes: r}}
		}
		return []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune(trimmed)}, enter}
	}
	return []tea.KeyMsg{clear, {Type: tea.KeyRunes, Runes: []rune(line)}, enter}
}

// secretField reports a masked question, where "?" is part of the answer.
func secretField(s screen) bool {
	f, ok := s.(*formScreen)
	return ok && f.secret()
}
