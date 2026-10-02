package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/i18n"
)

// question is one question of an interactive wizard (setup, join) or of
// any other CLI command, printed as a block the owner reads at a glance
// (spec section 6):
//
//	Step 1 of 5 · Role of this server
//	   1) hub    the server in Iran: users connect to it
//	   2) node   the server abroad: runs your VPN service
//	   Type 1 or 2 and press Enter. Enter alone = 1 (hub).
//	 › _
//
// The title is on its own line, the numbered answers one per line, an
// optional explanation, and a last line that says exactly what to type and
// what Enter alone does.
type question struct {
	// step and total number the wizard's questions ("Step 2 of 5"); 0 =
	// a single question without a step header.
	step, total int
	title       string
	// help are explanation lines printed under the title.
	help []string
	// opts are numbered answers; the owner types the number or the value.
	opts []askOpt
	// def is taken when the owner presses Enter alone; "" = an answer is
	// required.
	def string
	// yesNo makes it a y/n question; the answer is "yes" or "no".
	yesNo bool
	// check validates an answer (after the mapping of opts and yesNo).
	check func(string) error
}

// askOpt is one numbered answer: the value returned and its description.
type askOpt struct{ value, label string }

// Answers of a yes/no question.
const (
	answerYes = "yes"
	answerNo  = "no"
)

// stepper numbers the questions of a wizard.
type stepper struct{ n, total int }

// next returns q with the next step number.
func (s *stepper) next(q question) question {
	s.n++
	q.step, q.total = s.n, s.total
	return q
}

// askQ prints q and reads the answer until it is valid (at most maxAsk
// tries). A refused answer is explained on its own line and only the
// prompt is repeated.
func (g *Globals) askQ(q question) (string, error) {
	w := g.promptOut()
	g.printQuestion(w, q)
	for i := 0; i < maxAsk; i++ {
		fmt.Fprint(w, g.style(styleGreen, g.promptMark())+" ")
		ans, err := g.readLine()
		if err != nil {
			return "", err
		}
		v, problem := g.answer(q, ans)
		if problem == "" {
			return v, nil
		}
		fmt.Fprintln(w, "   "+g.style(styleRed, g.text(g.sym().fail+" "+i18n.T(i18n.CLIInvalidAnswer, problem))))
	}
	return "", errAborted
}

// answer maps the typed text to the value of q, or explains why it is
// refused.
func (g *Globals) answer(q question, ans string) (string, string) {
	if ans == "" {
		if q.def == "" {
			return "", i18n.T(i18n.CLIAnswerRequired)
		}
		ans = q.def
	}
	switch {
	case q.yesNo:
		switch strings.ToLower(ans) {
		case "y", "yes":
			ans = answerYes
		case "n", "no":
			ans = answerNo
		default:
			return "", i18n.T(i18n.CLIAnswerYesNo)
		}
	case len(q.opts) > 0:
		v, ok := optionValue(q.opts, ans)
		if !ok {
			return "", i18n.T(i18n.CLIAnswerNumber, len(q.opts))
		}
		ans = v
	}
	if q.check != nil {
		if err := q.check(ans); err != nil {
			return "", answerProblem(err)
		}
	}
	return ans, ""
}

// optionValue returns the value of an answer: an option number or a value
// typed out (any case).
func optionValue(opts []askOpt, in string) (string, bool) {
	if n, err := strconv.Atoi(in); err == nil {
		if n >= 1 && n <= len(opts) {
			return opts[n-1].value, true
		}
		return "", false
	}
	for _, o := range opts {
		if strings.EqualFold(o.value, in) {
			return o.value, true
		}
	}
	return "", false
}

// printQuestion prints the block of q (everything but the prompt).
func (g *Globals) printQuestion(w io.Writer, q question) {
	fmt.Fprintln(w)
	title := g.text(q.title)
	if q.total > 0 {
		head := g.text(i18n.T(i18n.CLIStepOf, q.step, q.total))
		title = g.style(styleCyan, head) + " " + g.text(g.sym().sep) + " " + g.style(styleBold, title)
	} else {
		title = g.style(styleBold, title)
	}
	fmt.Fprintln(w, title)
	const indent = "   "
	for _, h := range q.help {
		fmt.Fprintln(w, indent+g.text(h))
	}
	if len(q.opts) > 0 {
		vw := 0
		for _, o := range q.opts {
			vw = max(vw, width(o.value))
		}
		for i, o := range q.opts {
			num := g.style(styleGreen, strconv.Itoa(i+1)+")")
			line := num + " " + g.style(styleBold, pad(o.value, vw))
			if o.label != "" {
				line += "   " + g.text(o.label)
			}
			fmt.Fprintln(w, indent+line)
		}
	}
	fmt.Fprintln(w, indent+g.style(styleGray, g.text(g.hint(q))))
}

// hint is the last line of a question: what to type and what Enter alone
// does.
func (g *Globals) hint(q question) string {
	switch {
	case q.yesNo:
		def := i18n.T(i18n.CLIDefYes)
		if q.def == answerNo || q.def == "n" {
			def = i18n.T(i18n.CLIDefNo)
		}
		if q.def == "" {
			return i18n.T(i18n.CLIHintYesNoRequired)
		}
		return i18n.T(i18n.CLIHintYesNo, def)
	case len(q.opts) > 0:
		keys := numberList(len(q.opts))
		if q.def == "" {
			return i18n.T(i18n.CLIHintChooseRequired, keys)
		}
		def := q.def
		for i, o := range q.opts {
			if o.value == q.def {
				def = strconv.Itoa(i+1) + " (" + o.value + ")"
			}
		}
		return i18n.T(i18n.CLIHintChoose, keys, def)
	case q.def == "":
		return i18n.T(i18n.CLIHintRequired)
	}
	return i18n.T(i18n.CLIHintText, q.def)
}

// numberList is "1 or 2", "1, 2 or 3", "a number from 1 to 5".
func numberList(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n <= 3:
		nums := make([]string, n)
		for i := range nums {
			nums[i] = strconv.Itoa(i + 1)
		}
		return strings.Join(nums[:n-1], ", ") + " " + i18n.T(i18n.CLIOr) + " " + nums[n-1]
	}
	return i18n.T(i18n.CLINumberRange, n)
}

// promptMark is the answer prompt: "›" (">" without UTF-8).
func (g *Globals) promptMark() string {
	if g.unicode() {
		return " ›"
	}
	return " >"
}

// ANSI styles of the question blocks.
const (
	styleBold  = "1"
	styleCyan  = "1;36"
	styleGreen = "1;32"
	styleRed   = "31"
	styleGray  = "90"
)

// style colors s with an ANSI SGR code when the terminal the questions go
// to shows colors (NO_COLOR, TERM=dumb and pipes get plain text).
func (g *Globals) style(code, s string) string {
	if s == "" || !g.promptColor() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// styleOut colors s for standard output (only on a color terminal).
func (g *Globals) styleOut(code, s string) string {
	if s == "" || !g.outCaps().Color || !g.OutTTY {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// promptColor reports whether questions may be colored: a terminal on
// both ends and colors allowed.
func (g *Globals) promptColor() bool {
	if !g.IsTTY || !g.caps().Color {
		return false
	}
	return g.JSON || g.OutTTY
}
