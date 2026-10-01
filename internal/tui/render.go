package tui

import (
	stderrors "errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
)

// ANSI colors of the state words (section 6).
const (
	colRed    = "1"
	colGreen  = "2"
	colYellow = "3"
	colBlue   = "4"
	colCyan   = "6"
	colGray   = "8"
)

const (
	uiAdvanced = config.UIModeAdvanced
	uiSimple   = config.UIModeSimple
	stUp       = state.StateUp
)

// paint colors s when the terminal supports colors.
func (a *app) paint(col, s string) string {
	if !a.caps.Color || s == "" {
		return s
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Render(s)
}

func (a *app) bold(s string) string {
	if !a.caps.Color || s == "" {
		return s
	}
	return lipgloss.NewStyle().Bold(true).Render(s)
}

// symbols are the few glyphs of the UI, with ASCII fallbacks (S24).
type symbols struct {
	up, half, down, ok, fail, run, skip, warn, arrow, ell, sel, sep string
}

func (a *app) sym() symbols {
	if a.caps.Unicode {
		return symbols{"●", "◐", "○", "✔", "✖", "…", "-", "!", "→", "…", ">", "·"}
	}
	return symbols{"*", "~", "o", "OK", "x", "...", "-", "!", "->", "...", ">", "-"}
}

// asciiOnly makes s printable on a terminal without UTF-8: known symbols
// become ASCII words, anything else non-ASCII becomes '?'.
func asciiOnly(s string) string {
	s = ToASCII(s)
	return strings.Map(func(r rune) rune {
		if r > 127 {
			return '?'
		}
		return r
	}, s)
}

// clean makes text that comes from the daemon or from log files safe to
// print: control characters (including ESC, which could drive the terminal)
// become '?', tabs become four spaces and trailing newlines are dropped.
func clean(s string) string {
	s = strings.ReplaceAll(strings.TrimRight(s, "\r\n"), "\t", "    ")
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return '?'
		}
		return r
	}, s)
}

// cleanLines applies clean to every line of a multi-line text.
func cleanLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = clean(l)
	}
	return strings.Join(lines, "\n")
}

// width is the display width of s (ANSI sequences ignored).
func width(s string) int { return lipgloss.Width(s) }

// pad right-pads plain text s to w columns.
func pad(s string, w int) string {
	if n := width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// trunc cuts plain text s to at most w columns, ending in ell. It prefers a
// word boundary (the cut moves back to the last space when that keeps at
// least half of the text) and drops trailing separators, so a cut line
// reads "... / switch transport…" rather than "... / switch transport / d…".
func trunc(s string, w int, ell string) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	full := []rune(s)
	r := full
	for len(r) > 0 && width(string(r))+width(ell) > w {
		r = r[:len(r)-1]
	}
	if n := len(r); n < len(full) && !unicode.IsSpace(full[n]) {
		if i := lastSpace(r); i >= n/2 {
			r = r[:i]
		}
	}
	return strings.TrimRight(string(r), " /·-,;:>→") + ell
}

// lastSpace is the index of the last whitespace rune in r, or -1.
func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsSpace(r[i]) {
			return i
		}
	}
	return -1
}

// clip cuts a plain line to the terminal width (tables never wrap).
func (a *app) clip(s string) string {
	if !a.caps.Unicode {
		s = asciiOnly(s) // measure what the terminal will really print
	}
	if a.caps.Width <= 1 {
		return s
	}
	return trunc(s, a.caps.Width, a.sym().ell)
}

// indent prefixes every line of s with one space.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = " " + l
		}
	}
	return strings.Join(lines, "\n")
}

// fitTail is the number of last page lines (key hint, blank line, footer)
// that fit always keeps.
const fitTail = 3

// fit makes a full-screen page fit the window, whose renderer would cut
// it otherwise: a line wider than the window is word-wrapped and keeps its
// indentation; a page taller than the window first loses the banner art
// (the art lines are the first art lines; the status line stays), then the
// lines just above the last fitTail ones, replaced by a note. The title and
// the start of an error block (code, Why, Fix) therefore stay visible.
func (a *app) fit(page string, art int) string {
	lines := strings.Split(strings.TrimSuffix(page, "\n"), "\n")
	if w := a.caps.Width; w > 0 {
		out := make([]string, 0, len(lines))
		for i, l := range lines {
			switch {
			case ansi.StringWidth(l) <= w:
				out = append(out, l)
			case i < art:
				out = append(out, ansi.Truncate(l, w, ""))
			default:
				out = append(out, wrapLine(l, w)...)
			}
		}
		lines = out
	}
	h := a.height
	if !a.sized || h <= 0 || len(lines) <= h {
		return strings.Join(lines, "\n") + "\n"
	}
	if art > 0 && art < len(lines) {
		lines = lines[art:]
	}
	if len(lines) > h && h > fitTail+1 {
		keep := h - fitTail - 1
		note := " " + i18n.T(i18n.TUIMoreLines, len(lines)-keep-fitTail)
		if !a.caps.Unicode {
			note = asciiOnly(note)
		}
		tail := lines[len(lines)-fitTail:]
		lines = append(append(lines[:keep:keep], a.paint(colGray, note)), tail...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// wrapLine word-wraps l (which may contain colour codes) to width w; the
// continuation lines get the indentation of the first one.
func wrapLine(l string, w int) []string {
	plain := ansi.Strip(l)
	pad := len(plain) - len(strings.TrimLeft(plain, " "))
	if pad > w/2 {
		pad = 0
	}
	parts := strings.Split(ansi.Wrap(l, w-pad, ""), "\n")
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.Repeat(" ", pad) + strings.TrimLeft(parts[i], " ")
	}
	return parts
}

// uiError is a plain validation message (shown as one red line, no code).
type uiError string

func (e uiError) Error() string { return string(e) }

func uiErr(k i18n.Key, args ...any) error { return uiError(i18n.T(k, args...)) }

// errBlock renders err as the canonical DEY block: a red line with the
// code, then Why, Fix and the log path. Never a stack trace.
func (a *app) errBlock(err error) string {
	if err == nil {
		return ""
	}
	var ue uiError
	if stderrors.As(err, &ue) {
		return a.paint(colRed, " "+ue.Error()) + "\n"
	}
	e := deyerr.As(err)
	if e.Code == deyerr.X000 && e.Detail == "" && e.Cause != nil {
		c := *e
		c.Detail = e.Cause.Error()
		e = &c
	}
	lines := strings.Split(strings.TrimRight(e.Format(a.caps.Unicode), "\n"), "\n")
	for i := range lines {
		lines[i] = clean(lines[i]) // Detail may quote program output
	}
	lines[0] = a.paint(colRed, lines[0])
	return indent(strings.Join(lines, "\n")) + "\n"
}

// stepLine renders one progress step: "install backend on hub ✔".
func (a *app) stepLine(st api.Step) string {
	s := a.sym()
	var mark string
	switch st.Status {
	case api.StepOK:
		mark = a.paint(colGreen, s.ok)
	case api.StepFailed:
		mark = a.paint(colRed, s.fail)
	case api.StepSkipped:
		mark = a.paint(colGray, s.skip)
	case api.StepWarn:
		mark = a.paint(colYellow, s.warn)
	default:
		mark = a.paint(colBlue, s.run)
	}
	line := "  " + st.Title + " " + mark
	if st.Detail != "" {
		line += "  " + st.Detail
	}
	return line
}

// ms formats milliseconds: "41ms".
func ms(n int) string { return i18n.T(i18n.TUIms, n) }

// upTime formats a duration as "3d 04:12" or "00:03:10".
func upTime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	sec := int64(d / time.Second)
	days, h, m, s := sec/86400, (sec/3600)%24, (sec/60)%60, sec%60
	if days > 0 {
		return i18n.T(i18n.TUIUptimeDays, days, h, m)
	}
	return i18n.T(i18n.TUIUptimeClock, h, m, s)
}

// yesNo renders a boolean as the words yes/no.
func yesNo(v bool) string {
	if v {
		return i18n.T(i18n.TUIYes)
	}
	return i18n.T(i18n.TUINo)
}

// parseYes reads a y/n answer.
func parseYes(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	}
	return false, uiErr(i18n.TUIAnswerYN)
}

func checkYes(v string, _ map[string]string) error {
	_, err := parseYes(v)
	return err
}

// checkInt accepts a whole number >= min.
func checkInt(min int) func(string, map[string]string) error {
	return func(v string, _ map[string]string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < min {
			return uiErr(i18n.TUIWantNumber)
		}
		return nil
	}
}

// checkOneOf accepts one of the allowed values (empty allowed when the field
// is optional).
func checkOneOf(allowed ...string) func(string, map[string]string) error {
	return func(v string, _ map[string]string) error {
		if v == "" {
			return nil
		}
		for _, x := range allowed {
			if v == x {
				return nil
			}
		}
		return uiErr(i18n.TUIWantOneOf, strings.Join(allowed, ", "))
	}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// kv renders an aligned "  Label       value" line.
func kv(label, value string) string { return "  " + pad(label, 12) + " " + value + "\n" }

// chooser collects "number + Enter" input.
type chooser struct{ input string }

// key feeds k; it reports whether Enter was pressed and whether k was used.
func (c *chooser) key(k tea.KeyMsg) (submit, handled bool) {
	switch k.Type {
	case tea.KeyEnter:
		return true, true
	case tea.KeyBackspace:
		if c.input != "" {
			c.input = c.input[:len(c.input)-1]
		}
		return false, true
	case tea.KeyRunes:
		for _, r := range k.Runes {
			if r < '0' || r > '9' {
				return false, false
			}
		}
		if len(c.input)+len(k.Runes) <= 3 {
			c.input += string(k.Runes)
		}
		return false, true
	}
	return false, false
}

// take returns and clears the typed number.
func (c *chooser) take() (n int, raw string, ok bool) {
	raw = strings.TrimSpace(c.input)
	c.input = ""
	n, err := strconv.Atoi(raw)
	return n, raw, err == nil
}

// editLine applies a key to a free-text input; it reports whether k was used.
func editLine(s *string, k tea.KeyMsg) bool {
	switch k.Type {
	case tea.KeyBackspace:
		if r := []rune(*s); len(r) > 0 {
			*s = string(r[:len(r)-1])
		}
		return true
	case tea.KeyCtrlU:
		*s = ""
		return true
	case tea.KeySpace:
		*s += " "
		return true
	case tea.KeyRunes:
		*s += string(k.Runes)
		return true
	}
	return false
}
