package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// TestNodeJoinRevokeClosesWindow: `deyroute node join-revoke` deletes every
// live join token, applies the firewall before it returns (the control port
// is restricted to @nodes again: RestrictControl), records the event
// join_tokens_revoked, and a join with a revoked token fails with DEY-N001
// (DR-16).
func TestNodeJoinRevokeClosesWindow(t *testing.T) {
	env, o := prepareEnv(t, nil, func(o *Options, _ string) { o.DisableFirewall = false })
	env.startEnv(o)
	ctx := ctxT(t)
	require.Eventually(t, func() bool { return env.h.Firewall().Computed }, testWait, 10*time.Millisecond)

	// Nothing to revoke: 0, the window stays closed.
	n, err := env.client.NodeJoinRevoke(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	require.True(t, env.h.Firewall().Spec.RestrictControl)

	jc, err := env.client.NodeJoinCommand(ctx, 0)
	require.NoError(t, err)
	_, err = env.client.NodeJoinCommand(ctx, 2*time.Hour)
	require.NoError(t, err)
	require.False(t, env.h.Firewall().Spec.RestrictControl, "the join window is open")
	applies := len(env.nftScripts())

	n, err = env.client.NodeJoinRevoke(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	// Applied synchronously: no wait for the debounce or the token expiry.
	require.True(t, env.h.Firewall().Spec.RestrictControl)
	scripts := env.nftScripts()
	require.Greater(t, len(scripts), applies)
	last := scripts[len(scripts)-1]
	require.Contains(t, last, "ip saddr @nodes accept")
	require.NotContains(t, last, "tcp dport 44433 accept")
	require.False(t, env.h.joins.Active())

	ev := env.waitEvent(state.EvJoinTokensRevoked, "")
	require.Equal(t, state.LevelInfo, ev.Level)
	require.Contains(t, ev.Message, "2")

	// A node that still has the revoked link cannot join.
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	csr, _, err := tlsutil.NewKeyAndCSR("de-1")
	require.NoError(t, err)
	_, err = api.Join(ctx, env.h.ControlAddr().String(), link.Fingerprint, api.JoinRequest{
		NodeID: "de-1", Hostname: "de-1", Token: link.Token, CSRPEM: string(csr),
		Version: version.Version, Arch: "amd64", OS: "linux",
	})
	require.Error(t, err)
	require.Equal(t, deyerr.N001, deyerr.As(err).Code, err)

	// The firewall loop does not reopen the window later.
	env.h.wakeFirewall()
	env.h.requestFirewall()
	require.Never(t, func() bool { return !env.h.Firewall().Spec.RestrictControl }, 300*time.Millisecond, 20*time.Millisecond)
	for _, s := range env.nftScripts()[len(scripts):] {
		require.False(t, strings.Contains(s, "tcp dport 44433 accept"), "window reopened")
	}
}
