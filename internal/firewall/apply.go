package firewall

import (
	"context"
	"strings"
	"sync"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// Timeouts of nft invocations.
const (
	applyTimeout = 30 * time.Second
	queryTimeout = 10 * time.Second
)

// replacePrefix makes the script replace the table in one transaction:
// creating the (possibly existing) empty table first means the delete can
// never fail with "No such file or directory".
const replacePrefix = "table " + TableRef + " {}\ndelete table " + TableRef + "\n"

// ApplyScript returns the exact script Apply pipes to `nft -f -`.
func ApplyScript(s Spec) string { return replacePrefix + Render(s) }

// Apply replaces `table inet deyroute` with Render(s) atomically through
// `nft -f -`. Errors are DEY-P019 wrapping DEY-X005 (which wraps the runner
// error), with nft's stderr as detail; an invalid Spec is DEY-P019 and
// nothing is run.
func Apply(ctx context.Context, r exec.Runner, s Spec) error {
	if err := s.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()
	_, stderr, err := r.Run(ctx, "nft", []string{"-f", "-"}, []byte(ApplyScript(s)))
	if err != nil {
		return nftError(err, stderr)
	}
	return nil
}

// Applier is Apply with the fallback of the per-source control-port limit
// (DR-15): when nft refuses a table with Spec.UnknownControlRatePerSource
// because it cannot parse it, or the kernel cannot run it ("syntax error",
// "not supported"), the same Spec without the per-source rate (the single
// global rule at UnknownControlRate) is applied instead, and every later
// apply of this Applier goes straight to that: one Applier lives as long
// as the daemon run, so the first refusal is cached and reported once
// (OnFallback). No version is parsed. Any other failure is returned as it
// is. Safe for concurrent use.
type Applier struct {
	Runner exec.Runner
	// OnFallback, when set, is called once, with the refusal, when the
	// fallback is taken.
	OnFallback func(err error)

	mu     sync.Mutex
	global bool
}

// Apply applies s (see Applier) and returns the Spec it applied: s, or s
// without UnknownControlRatePerSource after the fallback (s when nothing
// could be applied).
func (a *Applier) Apply(ctx context.Context, s Spec) (Spec, error) {
	a.mu.Lock()
	global := a.global
	a.mu.Unlock()
	if global || s.UnknownControlRatePerSource == "" {
		s.UnknownControlRatePerSource = ""
		return s, Apply(ctx, a.Runner, s)
	}
	err := Apply(ctx, a.Runner, s)
	if err == nil || !unsupportedRule(err) || !strings.Contains(Render(s), "@"+SetCtlUnknown4) {
		return s, err
	}
	g := s
	g.UnknownControlRatePerSource = ""
	if gerr := Apply(ctx, a.Runner, g); gerr != nil {
		return s, err // the global rule fails as well: the per-source rate was not the cause
	}
	a.mu.Lock()
	first := !a.global
	a.global = true
	a.mu.Unlock()
	if first && a.OnFallback != nil {
		a.OnFallback(err)
	}
	return g, nil
}

// GlobalOnly reports whether the fallback was taken.
func (a *Applier) GlobalOnly() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.global
}

// unsupportedRule reports whether an nft failure means that nft cannot
// parse the script or the kernel cannot run a rule of it.
func unsupportedRule(err error) bool {
	msg := err.Error()
	if e := deyerr.As(err); e != nil {
		msg += " " + e.Detail
	}
	return strings.Contains(msg, "syntax error") || strings.Contains(msg, "not supported")
}

// Remove deletes `table inet deyroute` and nothing else. It is idempotent: a
// missing table (or a system without nft, where deyroute cannot have created
// one) is success.
func Remove(ctx context.Context, r exec.Runner) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	_, stderr, err := r.Run(ctx, "nft", []string{"delete", "table", TableFamily, TableName}, nil)
	if err == nil || noSuchTable(err, stderr) || deyerr.HasCode(err, deyerr.X030) {
		return nil
	}
	return nftError(err, stderr)
}

// Show returns `nft list table inet deyroute`; "" when the table does not
// exist.
func Show(ctx context.Context, r exec.Runner) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	out, stderr, err := r.Run(ctx, "nft", []string{"list", "table", TableFamily, TableName}, nil)
	if err != nil {
		if noSuchTable(err, stderr) {
			return "", nil
		}
		return "", nftError(err, stderr)
	}
	return string(out), nil
}

// noSuchTable reports whether an nft failure means the table is absent.
func noSuchTable(err error, stderr []byte) bool {
	const msg = "No such file or directory"
	return strings.Contains(string(stderr), msg) || strings.Contains(err.Error(), msg)
}

// nftError wraps a failed nft run as DEY-P019 → DEY-X005 → cause.
func nftError(err error, stderr []byte) error {
	detail := strings.TrimSpace(string(stderr))
	x := deyerr.Wrap(deyerr.X005, err, nil)
	if detail != "" {
		x = x.WithDetail(detail)
	}
	e := deyerr.Wrap(deyerr.P019, x, deyerr.Params{"firewall": string(NFTables)})
	if detail != "" {
		e = e.WithDetail(detail)
	}
	return e
}
