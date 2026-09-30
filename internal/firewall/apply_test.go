package firewall

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

func TestApply(t *testing.T) {
	ctx := context.Background()
	spec := hubSpec()
	f := exec.NewFake().On("nft -f -")
	require.NoError(t, Apply(ctx, f, spec))

	calls := f.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "nft", calls[0].Name)
	require.Equal(t, []string{"-f", "-"}, calls[0].Args)
	script := string(calls[0].Stdin)
	require.Equal(t, ApplyScript(spec), script)
	require.True(t, strings.HasPrefix(script, "table inet deyroute {}\ndelete table inet deyroute\n"))
	require.True(t, strings.HasSuffix(script, Render(spec)))
}

func TestApplyErrors(t *testing.T) {
	ctx := context.Background()

	// nft rejects the script: P019 → X005 → X007 with stderr as detail.
	stderr := "/dev/stdin:9:3-40: Error: Could not process rule: No such file or directory"
	f := exec.NewFake().On("nft -f -", exec.Fail(1, stderr))
	err := Apply(ctx, f, hubSpec())
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	require.True(t, deyerr.HasCode(err, deyerr.X005))
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	e := deyerr.As(err)
	require.Equal(t, deyerr.P019, e.Code)
	require.Equal(t, "nftables", e.Params["firewall"])
	require.Equal(t, stderr, e.Detail)
	require.Contains(t, e.Format(false), "Could not apply firewall rules (nftables)")

	// nft missing.
	f = exec.NewFake().On("nft -f -", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	err = Apply(ctx, f, hubSpec())
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	require.True(t, deyerr.HasCode(err, deyerr.X030))
	require.Empty(t, deyerr.As(err).Detail)

	// Invalid spec: nothing runs, and the error says so (not "the firewall
	// tool returned an error").
	f = exec.NewFake()
	err = Apply(ctx, f, Spec{ListenTCP: []int{0}})
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	require.Empty(t, f.Calls())
	require.Contains(t, deyerr.As(err).Why(), "nft was not run")
	require.Equal(t, "listen port 0/tcp out of range", deyerr.As(err).Detail)
}

func TestRemove(t *testing.T) {
	ctx := context.Background()
	f := exec.NewFake().On("nft delete table inet deyroute")
	require.NoError(t, Remove(ctx, f))
	require.Equal(t, []string{"nft delete table inet deyroute"}, f.Lines())

	// Already gone: idempotent.
	f = exec.NewFake().On("nft delete table inet deyroute",
		exec.Fail(1, "Error: Could not process rule: No such file or directory\ndelete table inet deyroute\n^^^^^^^^^^^^^^^^^^^^^^^^^^^"))
	require.NoError(t, Remove(ctx, f))

	// No nft on the system: nothing can exist.
	f = exec.NewFake().On("nft delete table inet deyroute", exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "nft"})})
	require.NoError(t, Remove(ctx, f))

	// Other failures are reported.
	f = exec.NewFake().On("nft delete table inet deyroute", exec.Fail(1, "Error: Operation not permitted"))
	err := Remove(ctx, f)
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	require.True(t, deyerr.HasCode(err, deyerr.X005))
	require.Contains(t, deyerr.As(err).Detail, "Operation not permitted")
}

func TestShow(t *testing.T) {
	ctx := context.Background()
	listed := "table inet deyroute {\n\tset nodes {\n\t\ttype ipv4_addr\n\t}\n}\n"
	f := exec.NewFake().On("nft list table inet deyroute", exec.OK(listed))
	out, err := Show(ctx, f)
	require.NoError(t, err)
	require.Equal(t, listed, out)

	f = exec.NewFake().On("nft list table inet deyroute", exec.Fail(1, "Error: No such file or directory; did you mean table 'filter' in family inet?"))
	out, err = Show(ctx, f)
	require.NoError(t, err)
	require.Empty(t, out)

	f = exec.NewFake().On("nft list table inet deyroute", exec.Fail(1, "Error: Operation not permitted"))
	_, err = Show(ctx, f)
	require.True(t, deyerr.HasCode(err, deyerr.P019))
}
