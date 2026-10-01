package cli

import (
	"bufio"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
)

// maxAsk bounds how often a question is repeated after an invalid answer.
const maxAsk = 5

// promptOut is where questions and confirmation texts go: stdout, or
// stderr in --json mode (stdout then carries only the JSON document).
func (g *Globals) promptOut() io.Writer {
	if g.JSON {
		return g.Err
	}
	return g.Out
}

// note prints an i18n line where questions go (see promptOut): wizard
// hints, summaries and warnings that must not mix with --json output.
func (g *Globals) note(k i18n.Key, a ...any) {
	fmt.Fprintln(g.promptOut(), g.text(i18n.T(k, a...)))
}

// readLine reads one answer from In. End of input is errAborted, and so is
// Ctrl-C while it waits: the run's context ends and the question is left at
// once (a read of the terminal cannot be interrupted, so it runs in its own
// goroutine; after an abort nothing reads In again, and the process exits).
func (g *Globals) readLine() (string, error) {
	ctx := g.context()
	if ctx.Err() != nil {
		return "", errAborted
	}
	if g.reader == nil {
		g.reader = bufio.NewReader(g.In)
	}
	type answer struct {
		s   string
		err error
	}
	ch := make(chan answer, 1)
	r := g.reader
	go func() {
		s, err := r.ReadString('\n')
		ch <- answer{s, err}
	}()
	select {
	case a := <-ch:
		if a.err != nil && (a.err != io.EOF || a.s == "") {
			return "", errAborted
		}
		return strings.TrimSpace(a.s), nil
	case <-ctx.Done():
		fmt.Fprintln(g.promptOut())
		return "", errAborted
	}
}

// context is the context of the current run (Run sets it).
func (g *Globals) context() context.Context {
	if g.ctx == nil {
		return context.Background()
	}
	return g.ctx
}

// ask prints "question [def]: " and returns the answer, def when empty.
// check validates an answer (nil = any); an invalid answer is explained and
// the question repeated.
func (g *Globals) ask(question, def string, check func(string) error) (string, error) {
	w := g.promptOut()
	for i := 0; i < maxAsk; i++ {
		if def != "" {
			fmt.Fprint(w, g.text(i18n.T(i18n.CLIAskDefault, question, def)))
		} else {
			fmt.Fprint(w, g.text(i18n.T(i18n.CLIAsk, question)))
		}
		ans, err := g.readLine()
		if err != nil {
			return "", err
		}
		if ans == "" {
			ans = def
		}
		if check == nil {
			return ans, nil
		}
		cerr := check(ans)
		if cerr == nil {
			return ans, nil
		}
		fmt.Fprintln(w, g.text(i18n.T(i18n.CLIInvalidAnswer, answerProblem(cerr))))
	}
	return "", errAborted
}

// answerProblem is the one-line reason an answer was refused.
func answerProblem(err error) string {
	var e *deyerr.Error
	if stderrors.As(err, &e) {
		return string(e.Code) + " " + e.Message()
	}
	return err.Error()
}

// askYesNo asks a [Y/n] or [y/N] question.
func (g *Globals) askYesNo(question string, def bool) (bool, error) {
	w := g.promptOut()
	hint := i18n.T(i18n.CLIYesNoDefYes)
	if !def {
		hint = i18n.T(i18n.CLIYesNoDefNo)
	}
	for i := 0; i < maxAsk; i++ {
		fmt.Fprint(w, g.text(question+" "+hint+" "))
		ans, err := g.readLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(ans) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(w, g.text(i18n.T(i18n.TUIAnswerYN)))
	}
	return false, errAborted
}

// confirm guards a destructive action (spec sections 6 and 14): lost
// explains exactly what is removed or interrupted. With yes it only
// proceeds. On a terminal the owner must type "yes"; anything else aborts.
// Without a terminal it prints lost and returns errNeedConfirm (exit 3).
func (g *Globals) confirm(lost string, yes bool) error {
	if yes {
		return nil
	}
	if !g.IsTTY {
		fmt.Fprintln(g.Err, g.text(lost))
		return errNeedConfirm
	}
	w := g.promptOut()
	fmt.Fprintln(w, g.text(lost))
	fmt.Fprint(w, g.text(i18n.T(i18n.CLITypeYes)))
	ans, err := g.readLine()
	if err != nil {
		return err
	}
	if ans != "yes" {
		return errAborted
	}
	return nil
}

// termPassword reads a secret from the terminal without echo. Ctrl-C
// aborts at once and turns the echo back on (the read itself cannot be
// interrupted; see readLine).
func (g *Globals) termPassword(prompt string) (string, error) {
	fmt.Fprint(g.Err, prompt)
	defer fmt.Fprintln(g.Err)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errAborted
	}
	state, err := term.GetState(fd)
	if err != nil {
		return "", errAborted
	}
	type answer struct {
		b   []byte
		err error
	}
	ch := make(chan answer, 1)
	go func() {
		b, err := term.ReadPassword(fd)
		ch <- answer{b, err}
	}()
	select {
	case a := <-ch:
		if a.err != nil {
			return "", errAborted
		}
		return string(a.b), nil
	case <-g.context().Done():
		_ = term.Restore(fd, state)
		return "", errAborted
	}
}
