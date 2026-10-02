package cli

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/tui"
)

// println writes one line of human output.
func (g *Globals) println(a ...any) { fmt.Fprintln(g.Out, a...) }

// printf writes formatted human output.
func (g *Globals) printf(format string, a ...any) { fmt.Fprintf(g.Out, format, a...) }

// say prints an i18n line (with fmt arguments) followed by a newline.
func (g *Globals) say(k i18n.Key, a ...any) { g.println(g.text(i18n.T(k, a...))) }

// text adapts UI text to the terminal: known symbols become ASCII without
// UTF-8 (scenario S24).
func (g *Globals) text(s string) string {
	if g.unicode() {
		return s
	}
	return asciiOnly(s)
}

// asciiOnly replaces the UI symbols with ASCII and any other non-ASCII
// character with '?'.
func asciiOnly(s string) string {
	s = tui.ToASCII(s)
	return strings.Map(func(r rune) rune {
		if r > 127 {
			return '?'
		}
		return r
	}, s)
}

// clean makes daemon and log text safe for a terminal: control characters
// (ESC included) become '?', tabs four spaces, trailing newlines go.
func clean(s string) string {
	s = strings.ReplaceAll(strings.TrimRight(s, "\r\n"), "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}

// cleanLines applies clean to every line of a multi-line text (CRLF too),
// so its line breaks survive.
func cleanLines(s string) string {
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
	for i, l := range lines {
		lines[i] = clean(l)
	}
	return strings.Join(lines, "\n")
}

// jsonDoc returns the fields of v (a struct or map) with "schema": 1 added,
// the stable envelope of every --json document (docs/cli-json.md).
func jsonDoc(v any) (map[string]any, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X000, err, nil)
	}
	doc := map[string]any{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keep 64-bit counters exact
	if err := dec.Decode(&doc); err != nil {
		return nil, deyerr.Wrap(deyerr.X000, err, nil)
	}
	doc["schema"] = api.JSONSchemaVersion
	return doc, nil
}

// emitJSON prints v as one indented JSON document with "schema": 1.
func (g *Globals) emitJSON(v any) error {
	doc, err := jsonDoc(v)
	if err != nil {
		return err
	}
	return writeJSON(g.Out, doc, true)
}

// emitJSONLine prints v as one compact JSON line (streams: logs -f,
// status --watch).
func (g *Globals) emitJSONLine(v any) error {
	doc, err := jsonDoc(v)
	if err != nil {
		return err
	}
	return writeJSON(g.Out, doc, false)
}

func writeJSON(w io.Writer, v any, indent bool) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	return nil
}

// ok is the --json document of commands without a result.
func (g *Globals) ok(fields map[string]any, steps []api.Step) error {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["ok"] = true
	if steps != nil {
		fields["steps"] = steps
	}
	return g.emitJSON(fields)
}

// done prints the human confirmation line of a finished action, or the
// --json {"ok": true} document.
func (g *Globals) done(fields map[string]any, k i18n.Key, a ...any) error {
	if g.JSON {
		return g.ok(fields, nil)
	}
	g.say(k, a...)
	return nil
}

// progress returns the Step callback of a long operation and the steps
// seen so far. In human mode every finished step is printed at once as
// "  ✔ title  detail" ("OK"/"x" without UTF-8); running steps are not
// printed. In --json mode nothing is printed and the steps go into the
// final document.
type progress struct {
	g     *Globals
	steps []api.Step
}

func (g *Globals) newProgress() *progress { return &progress{g: g, steps: []api.Step{}} }

// step is the callback passed to the Local API or to a setup operation.
func (p *progress) step(s api.Step) {
	if s.Status == api.StepRunning {
		return
	}
	p.steps = append(p.steps, s)
	if p.g.JSON {
		return
	}
	p.g.println(p.g.stepLine(s))
}

// stepLine renders one finished step.
func (g *Globals) stepLine(s api.Step) string {
	sym := g.sym()
	mark := sym.run
	switch s.Status {
	case api.StepOK:
		mark = sym.ok
	case api.StepFailed:
		mark = sym.fail
	case api.StepSkipped:
		mark = sym.skip
	case api.StepWarn:
		mark = sym.warn
	}
	line := "  " + mark + " " + clean(s.Title)
	if s.Detail != "" {
		line += "  " + clean(s.Detail)
	}
	if s.Status == api.StepWarn && s.Error != nil {
		line += "  (" + s.Error.Code + " " + clean(s.Error.Message) + ")"
	}
	return g.text(line)
}

// symbols are the status marks of human output.
type symbols struct {
	ok, fail, skip, warn, run, up, half, down, arrow, sep, ell string
}

func (g *Globals) sym() symbols {
	if g.unicode() {
		return symbols{"✔", "✖", "–", "!", "…", "●", "◐", "○", "→", "·", "…"}
	}
	return symbols{"OK", "x", "-", "!", "...", "*", "~", "o", "->", "-", "~"}
}

// width is the display width of s (wide runes count twice).
func width(s string) int { return lipgloss.Width(s) }

// pad left-aligns s in w columns (never cuts).
func pad(s string, w int) string {
	if n := width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// padLeft right-aligns s in w columns.
func padLeft(s string, w int) string {
	if n := width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// trunc cuts s to w columns, ending with ell when cut.
func trunc(s string, w int, ell string) string {
	if width(s) <= w {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if width(b.String()+string(r))+width(ell) > w {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + ell
}

// table prints rows under a header, columns separated by two spaces and
// indented by two, the last column unpadded.
func (g *Globals) table(header []string, rows [][]string) {
	w := make([]int, len(header))
	for i, h := range header {
		w[i] = width(h)
	}
	for _, r := range rows {
		for i := range header {
			if i < len(r) {
				w[i] = max(w[i], width(r[i]))
			}
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		b.WriteString("  ")
		for i := range header {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			if i == len(header)-1 {
				b.WriteString(c)
			} else {
				b.WriteString(pad(c, w[i]+2))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	g.println(g.text(line(header)))
	for _, r := range rows {
		g.println(g.text(line(r)))
	}
}

// ms formats milliseconds ("41ms"); "-" for zero.
func ms(n int) string {
	if n <= 0 {
		return i18n.T(i18n.TUIDash)
	}
	return i18n.T(i18n.TUIms, n)
}

// upTime formats a duration as "3d 04:12" or "00:03:10" (dashboard).
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

// yesNo renders a boolean as yes/no.
func yesNo(v bool) string {
	if v {
		return i18n.T(i18n.TUIYes)
	}
	return i18n.T(i18n.TUINo)
}

// localTime formats t in the server's local time (section 2: UI times are
// local).
func localTime(t time.Time, layout string) string {
	if t.IsZero() {
		return i18n.T(i18n.TUIDash)
	}
	return t.Local().Format(layout)
}

// orDash returns s, or "-" when it is empty.
func orDash(s string) string {
	if s == "" {
		return i18n.T(i18n.TUIDash)
	}
	return s
}

// errors ------------------------------------------------------------------

// errNeedConfirm is returned when a destructive command needs a typed "yes"
// but stdin is not a terminal and --yes was not given (exit 3).
var errNeedConfirm = deyerr.Plain("confirmation required")

// errAborted is returned when the owner declines a confirmation (exit 1).
var errAborted = deyerr.Plain("aborted")

// usageError is a wrong command line: DEY-C025 when printed (exit 1).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErr(msg string) error { return &usageError{msg: msg} }

// usageDEY is a wrong command line (a usageError, or cobra's own error
// for an unknown command or flag) as DEY-C025, shown in the three-line
// format like every other error (section 13). command is the command whose
// --help the Fix names; cobra's suggestions ("Did you mean this?") become
// the detail.
func usageDEY(err error, command string) *deyerr.Error {
	if command == "" {
		command = "deyroute"
	}
	first, rest, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	e := deyerr.New(deyerr.C025, deyerr.Params{"reason": clean(first), "command": command})
	var detail []string
	for _, l := range strings.Split(rest, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			detail = append(detail, clean(l))
		}
	}
	if len(detail) > 0 {
		e = e.WithDetail(strings.Join(detail, "\n"))
	}
	return e
}

// isUsage reports whether err is a wrong command line: a usageError, or a
// plain error that never reached a command (cobra's own, see classify).
func isUsage(err error) bool {
	var ue *usageError
	return !stderrors.Is(err, errNeedConfirm) && !stderrors.Is(err, errAborted) &&
		(stderrors.As(err, &ue) || !isDEY(err))
}

// printError prints err on stderr (the DEY three-line block, several for a
// joined validation error; each is also written to the CLI log its Log line
// names) and, with --json, an error document on stdout. A wrong command
// line is DEY-C025 (see usageDEY). It returns the exit code.
func (g *Globals) printError(err error) int {
	switch {
	case stderrors.Is(err, errNeedConfirm):
		msg := i18n.T(i18n.CLINeedConfirm)
		fmt.Fprintln(g.Err, msg)
		g.jsonError(&api.ErrorDTO{Message: msg}, deyerr.ExitNeedConfirm)
		return deyerr.ExitNeedConfirm
	case stderrors.Is(err, errAborted):
		msg := i18n.T(i18n.CLIAborted)
		fmt.Fprintln(g.Err, msg)
		g.jsonError(&api.ErrorDTO{Message: msg}, deyerr.ExitUser)
		return deyerr.ExitUser
	case isUsage(err):
		err = usageDEY(err, g.usageCommand)
	}
	list := deyErrors(err)
	code := deyerr.ExitUser
	uni := g.unicode()
	for _, e := range list {
		fmt.Fprint(g.Err, g.text(e.Format(uni)))
		g.logError(e)
		code = max(code, e.ExitCode())
	}
	if g.JSON {
		dtos := make([]*api.ErrorDTO, 0, len(list))
		for _, e := range list {
			d := api.ToDTO(e)
			// The log the human block names (docs/cli-json.md: "log").
			d.Log = e.Log()
			dtos = append(dtos, d)
		}
		doc := map[string]any{"schema": api.JSONSchemaVersion, "error": dtos[0], "exit_code": code}
		if len(dtos) > 1 {
			doc["errors"] = dtos
		}
		_ = writeJSON(g.Out, doc, true)
	}
	return code
}

func (g *Globals) jsonError(e *api.ErrorDTO, code int) {
	if g.JSON {
		_ = writeJSON(g.Out, map[string]any{"schema": api.JSONSchemaVersion, "error": e, "exit_code": code}, true)
	}
}

// isDEY reports whether err carries a DEY code anywhere (joined errors
// included).
func isDEY(err error) bool {
	var e *deyerr.Error
	if stderrors.As(err, &e) {
		return true
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, x := range j.Unwrap() {
			if isDEY(x) {
				return true
			}
		}
	}
	return false
}

// deyErrors flattens err (a DEY error or an errors.Join of several, as
// config validation returns) into its DEY errors, in order.
func deyErrors(err error) []*deyerr.Error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []*deyerr.Error
		for _, x := range j.Unwrap() {
			out = append(out, deyErrors(x)...)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []*deyerr.Error{deyerr.As(err)}
}
