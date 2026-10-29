package firewall

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

func perSourceSpec() Spec {
	s := hubSpec()
	s.UnknownControlRate = "30/minute"
	s.UnknownControlRatePerSource = "3/minute"
	return s
}

// scripts returns the stdin of every `nft -f -`.
func scripts(r *exec.Fake) []string {
	var out []string
	for _, c := range r.Calls() {
		if c.Line() == "nft -f -" {
			out = append(out, string(c.Stdin))
		}
	}
	return out
}

// TestRenderFallbackGlobalLimit: an nft that cannot parse (or a kernel that
// cannot run) the per-source admission of DR-15 gets the old single global
// rule at the global rate instead. The first apply fails with the syntax
// error and the second carries the global rule; the Applier remembers this
// for its lifetime (one daemon run) and says so once. Any other failure is
// returned as it is and changes nothing.
func TestRenderFallbackGlobalLimit(t *testing.T) {
	ctx := context.Background()
	r := exec.NewFake()
	r.On("nft -f -",
		exec.Fail(1, "/dev/stdin:21:52-54: Error: syntax error, unexpected add\n\t\ttcp dport 44433 ct state new add @ctl_unknown4 { ip saddr limit rate 3/minute burst 3 packets } limit rate 30/minute accept"),
		exec.OK(""))
	var told []error
	a := &Applier{Runner: r, OnFallback: func(err error) { told = append(told, err) }}
	spec := perSourceSpec()
	applied, err := a.Apply(ctx, spec)
	require.NoError(t, err)
	got := scripts(r)
	require.Len(t, got, 2)
	require.Contains(t, got[0], "add @ctl_unknown4 { ip saddr limit rate 3/minute burst 3 packets } limit rate 30/minute accept")
	require.NotContains(t, got[1], "ctl_unknown")
	require.Contains(t, got[1], "tcp dport 44433 ct state new limit rate 30/minute accept")
	require.Empty(t, applied.UnknownControlRatePerSource)
	require.Equal(t, "30/minute", applied.UnknownControlRate)
	require.Len(t, told, 1)
	require.Contains(t, told[0].Error(), "syntax error")
	require.True(t, a.GlobalOnly())

	// Cached for the run: straight to the global rule, told once.
	applied, err = a.Apply(ctx, spec)
	require.NoError(t, err)
	require.Len(t, scripts(r), 3)
	require.NotContains(t, scripts(r)[2], "ctl_unknown")
	require.Empty(t, applied.UnknownControlRatePerSource)
	require.Len(t, told, 1)

	// A kernel without the needed support says "not supported": the same.
	k := exec.NewFake()
	k.On("nft -f -", exec.Fail(1, "Error: Could not process rule: Operation not supported"), exec.OK(""))
	_, err = (&Applier{Runner: k}).Apply(ctx, spec)
	require.NoError(t, err)
	require.Len(t, scripts(k), 2)

	// Another failure is returned and not cached.
	o := exec.NewFake()
	o.On("nft -f -", exec.Fail(1, "Error: Could not process rule: No buffer space available"), exec.OK(""))
	b := &Applier{Runner: o}
	_, err = b.Apply(ctx, spec)
	require.Equal(t, deyerr.P019, deyerr.As(err).Code)
	require.Len(t, scripts(o), 1)
	require.False(t, b.GlobalOnly())
	applied, err = b.Apply(ctx, spec)
	require.NoError(t, err)
	require.Equal(t, "3/minute", applied.UnknownControlRatePerSource)
	require.Contains(t, scripts(o)[1], "add @ctl_unknown4")

	// The global rule fails as well: the first error is returned with the
	// spec as asked, and nothing is cached.
	f := exec.NewFake()
	f.On("nft -f -", exec.Fail(1, "Error: syntax error, unexpected add"), exec.Fail(1, "Error: Could not process rule: No buffer space available"))
	c := &Applier{Runner: f}
	applied, err = c.Apply(ctx, spec)
	require.ErrorContains(t, err, "syntax error")
	require.Equal(t, "3/minute", applied.UnknownControlRatePerSource)
	require.False(t, c.GlobalOnly())

	// A spec without a per-source rate is applied as it is, also when it
	// fails with a syntax error.
	n := exec.NewFake()
	n.On("nft -f -", exec.Fail(1, "Error: syntax error, unexpected junk"))
	_, err = (&Applier{Runner: n}).Apply(ctx, hubSpec())
	require.Error(t, err)
	require.Len(t, scripts(n), 1)
	require.False(t, strings.Contains(scripts(n)[0], "ctl_unknown"))
}
