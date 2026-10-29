package firewall

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

const liveTable = `table inet deyroute {
	set nodes {
		type ipv4_addr
		elements = { 1.2.3.4, 5.6.7.8 }
	}

	set scan4 {
		type ipv4_addr
		size 65535
		flags dynamic,timeout
		timeout 10m
	}

	chain input {
		type filter hook input priority filter - 10; policy accept;
		tcp dport 44433 ip saddr @nodes accept
		tcp dport 44433 drop
	}
}
`

// TestFingerprintIgnoresKernelChanges: the fingerprint of `nft list table
// inet deyroute` does not change with the order or wrapping of set
// elements, nor with the elements the kernel adds to a dynamic set; any
// other change does. A missing table is "" and nft's other errors are
// returned.
func TestFingerprintIgnoresKernelChanges(t *testing.T) {
	base := fingerprint(liveTable)
	require.NotEmpty(t, base)
	require.Equal(t, base, fingerprint(strings.Replace(liveTable, "elements = { 1.2.3.4, 5.6.7.8 }",
		"elements = { 5.6.7.8,\n\t\t\t     1.2.3.4 }", 1)))
	require.Equal(t, base, fingerprint(strings.Replace(liveTable, "\t\ttimeout 10m\n",
		"\t\ttimeout 10m\n\t\telements = { 192.0.2.7 limit rate 3/minute burst 3 packets expires 9m58s,\n"+
			"\t\t\t     198.51.100.1 limit rate 3/minute burst 3 packets expires 2m1s }\n", 1)))
	for _, changed := range []string{
		strings.Replace(liveTable, "5.6.7.8", "5.6.7.9", 1),
		strings.Replace(liveTable, "\t\ttcp dport 44433 drop\n", "", 1),
		strings.Replace(liveTable, "policy accept", "policy drop", 1),
		strings.Replace(liveTable, "timeout 10m", "timeout 1m", 1),
	} {
		require.NotEqual(t, base, fingerprint(changed))
	}

	r := exec.NewFake()
	r.On("nft list table inet deyroute",
		exec.OK(liveTable),
		exec.Fail(1, "Error: No such file or directory; did you mean table 'filter' in family inet?"),
		exec.Fail(1, "Error: Could not process rule: Operation not permitted"))
	ctx := context.Background()
	fp, err := Fingerprint(ctx, r)
	require.NoError(t, err)
	require.Equal(t, base, fp)
	fp, err = Fingerprint(ctx, r)
	require.NoError(t, err)
	require.Empty(t, fp, "no table")
	_, err = Fingerprint(ctx, r)
	require.Equal(t, deyerr.P019, deyerr.As(err).Code)
}
