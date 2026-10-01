package firewall

import (
	"context"
	"strings"
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
