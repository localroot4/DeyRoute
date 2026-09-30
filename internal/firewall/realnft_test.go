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
// it again (twice, for idempotence). It then checks ports against a foreign
// nftables table and raw iptables rules as the real tools list them, and
// opens a blocked port with the suggested command (nft and iptables must be
// installed).
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

	// A foreign policy-drop table as nft lists it (comments after verdicts,
	// a named verdict map): commented accepts are open, the map's drop
	// blocks, and the confirmed suggestion really opens the port.
	fw, err := os.ReadFile("testdata/nft_comments.txt")
	require.NoError(t, err)
	_, _, err = r.Run(ctx, "nft", []string{"-f", "-"}, fw)
	require.NoError(t, err)
	v, err = Check(ctx, r, 443, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	v, err = Check(ctx, r, 8081, ProtoTCP)
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, "inet fw", v.Table)
	require.Equal(t, []string{"nft insert rule inet fw input tcp dport 8081 accept"}, v.Commands)
	require.NoError(t, v.Open(ctx, r))
	v, err = Check(ctx, r, 8081, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	_, _, err = r.Run(ctx, "nft", []string{"delete", "table", "inet", "fw"}, nil)
	require.NoError(t, err)

	// Raw iptables (iptables-nft or legacy), as the real tool prints it.
	for _, args := range [][]string{
		{"-P", "INPUT", "DROP"},
		{"-A", "INPUT", "-i", "lo", "-j", "ACCEPT"},
		{"-A", "INPUT", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		{"-A", "INPUT", "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-m", "comment", "--comment", "web traffic", "-j", "ACCEPT"},
		{"-A", "INPUT", "-p", "udp", "--dport", "1000:2000", "-j", "ACCEPT"},
		{"-A", "INPUT", "-p", "tcp", "--dport", "7000", "-j", "NFQUEUE", "--queue-num", "0"},
	} {
		_, _, err = r.Run(ctx, "iptables", args, nil)
		require.NoError(t, err, "iptables %v", args)
	}
	require.Contains(t, Detect(ctx, r), IPTables)
	for _, c := range []portCase{
		{443, "tcp", false, false}, {1500, "udp", false, false}, {7000, "tcp", false, true}, {8443, "tcp", true, false},
	} {
		v, err = Check(ctx, r, c.port, c.proto)
		require.NoError(t, err)
		require.Equal(t, c.blocked, v.Blocked, "%d/%s %s", c.port, c.proto, v.Detail)
		require.Equal(t, c.uncertain, v.Uncertain, "%d/%s %s", c.port, c.proto, v.Detail)
	}
	v, err = Check(ctx, r, 8443, ProtoTCP)
	require.NoError(t, err)
	require.Equal(t, IPTables, v.By)
	require.Equal(t, []string{"iptables -I INPUT -p tcp --dport 8443 -j ACCEPT"}, v.Commands)
	require.NoError(t, v.Open(ctx, r))
	v, err = Check(ctx, r, 8443, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	for _, args := range [][]string{{"-F", "INPUT"}, {"-P", "INPUT", "ACCEPT"}} {
		_, _, err = r.Run(ctx, "iptables", args, nil)
		require.NoError(t, err)
	}

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
