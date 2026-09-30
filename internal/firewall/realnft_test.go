package firewall

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/exec"
)

// realNFTEnv enables TestRealNFT. It changes the firewall of the network
// namespace it runs in, so only set it inside a throwaway namespace:
//
//	go test -c -o fw.test ./internal/firewall
//	sudo unshare -n env DEYROUTE_TEST_NFT_NETNS=1 ./fw.test -test.run TestRealNFT -test.v
const realNFTEnv = "DEYROUTE_TEST_NFT_NETNS"

// TestRealNFT applies every golden Spec with the real runner and nft,
// replaces it, checks that nothing else in the ruleset changed, and removes
// it again (twice, for idempotence).
func TestRealNFT(t *testing.T) {
	if os.Getenv(realNFTEnv) != "1" {
		t.Skip("set " + realNFTEnv + "=1 inside `unshare -n` to run against the real nft")
	}
	ctx := context.Background()
	r := exec.NewRunner()

	// A foreign table that must survive untouched.
	_, _, err := r.Run(ctx, "nft", []string{"-f", "-"}, []byte("table ip other {\n\tchain input {\n\t\ttype filter hook input priority 0; policy accept;\n\t\ttcp dport 22 accept\n\t}\n}\n"))
	require.NoError(t, err)
	before, _, err := r.Run(ctx, "nft", []string{"list", "ruleset"}, nil)
	require.NoError(t, err)

	require.Contains(t, Detect(ctx, r), NFTables)

	names := make([]string, 0, len(goldenSpecs))
	for n := range goldenSpecs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := goldenSpecs[name]
		t.Run(name, func(t *testing.T) {
			require.NoError(t, Apply(ctx, r, spec))
			require.NoError(t, Apply(ctx, r, spec), "replacing an existing table")
			out, err := Show(ctx, r)
			require.NoError(t, err)
			require.Contains(t, out, "table inet deyroute {")
			require.Contains(t, out, "set nodes {")
			require.NoError(t, Remove(ctx, r))
			require.NoError(t, Remove(ctx, r), "idempotent")
			out, err = Show(ctx, r)
			require.NoError(t, err)
			require.Empty(t, out)
		})
	}

	after, _, err := r.Run(ctx, "nft", []string{"list", "ruleset"}, nil)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "only table inet deyroute may change")

	// The check sees the foreign table and our own table is ignored.
	require.NoError(t, Apply(ctx, r, hubSpec()))
	v, err := Check(ctx, r, 30500, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	require.NoError(t, Remove(ctx, r))

	// A pre-existing inet deyroute table with other content is replaced.
	_, _, err = r.Run(ctx, "nft", []string{"-f", "-"}, []byte("table inet deyroute {\n\tchain stale {\n\t}\n}\n"))
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, r, Spec{ControlPort: 44433, RestrictControl: true}))
	out, err := Show(ctx, r)
	require.NoError(t, err)
	require.Contains(t, out, "tcp dport 44433 drop")
	require.NotContains(t, out, "stale")
	require.NoError(t, Remove(ctx, r))
}
