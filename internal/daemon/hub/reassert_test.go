package hub

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

// fakeFWTable simulates table inet deyroute for the fake runner: `nft -f -`
// installs the applied table, `nft list table inet deyroute` prints it (or
// fails like nft when it is absent), and `nft delete table` or flush
// removes it.
type fakeFWTable struct {
	mu      sync.Mutex
	table   string
	applies int
	lists   int
}

func newFakeFWTable(env *testEnv) *fakeFWTable {
	f := &fakeFWTable{}
	env.runner.Handler = func(c exec.Call) (exec.Response, bool) {
		if c.Name != "nft" {
			return exec.Response{}, false
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch line := exec.CommandLine(c.Name, c.Args); {
		case len(c.Args) == 2 && c.Args[0] == "-f" && !strings.Contains(string(c.Stdin), firewall.StatsTable):
			s := string(c.Stdin)
			f.table = s[strings.Index(s, "# Managed"):]
			f.applies++
			return exec.OK(""), true
		case line == "nft list table inet deyroute":
			f.lists++
			if f.table == "" {
				return exec.Fail(1, "Error: No such file or directory; did you mean table 'filter' in family inet?"), true
			}
			return exec.OK(f.table), true
		case line == "nft delete table inet deyroute":
			f.table = ""
			return exec.OK(""), true
		}
		return exec.Response{}, false
	}
	return f
}

// flush is `nft flush ruleset` by another tool.
func (f *fakeFWTable) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.table = ""
}

// edit changes the live table behind deyroute's back.
func (f *fakeFWTable) edit(fn func(string) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.table = fn(f.table)
}

func (f *fakeFWTable) counts() (applies, lists int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applies, f.lists
}

func (f *fakeFWTable) live() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.table
}

// startFirewallHub runs a hub that manages table inet deyroute through a
// fakeFWTable and waits for its first apply.
func startFirewallHub(t *testing.T, edit func(c *config.Config), opts ...envOption) (*testEnv, *fakeFWTable) {
	t.Helper()
	env, o := prepareEnv(t, edit, opts...)
	o.DisableFirewall = false
	nft := newFakeFWTable(env)
	env.startEnv(o)
	require.Eventually(t, func() bool {
		fw := env.h.Firewall()
		return fw.Computed && (!fw.Managed || !fw.Applied.IsZero())
	}, testWait, 10*time.Millisecond)
	return env, nft
}

// TestFirewallReassertRestoresMissingTable: a table flushed by another
// tool is applied again by the next check, with the same content, and a
// changed table is put back too (security.firewall_reassert_s, DR-14).
func TestFirewallReassertRestoresMissingTable(t *testing.T) {
	env, nft := startFirewallHub(t, nil)
	want := nft.live()
	require.Contains(t, want, "table inet deyroute {")
	applies, _ := nft.counts()

	nft.flush()
	require.Equal(t, reassertMissing, env.h.reassertFirewall(ctxT(t)))
	got, _ := nft.counts()
	require.Equal(t, applies+1, got)
	require.Equal(t, want, nft.live())
	ev := env.waitEvent(state.EvFirewallReapplied, "")
	require.Equal(t, state.LevelWarn, ev.Level)
	require.Equal(t, reassertMissing, ev.Reason)
	require.Contains(t, ev.Message, "table inet deyroute")
	require.True(t, env.h.Firewall().Applied.After(time.Time{}))
	require.NoError(t, env.h.Firewall().Err)

	nft.edit(func(s string) string { return strings.Replace(s, "policy accept;", "policy drop;", 1) })
	require.Equal(t, reassertDrift, env.h.reassertFirewall(ctxT(t)))
	require.Equal(t, want, nft.live())
}

// TestFirewallReassertNoopWhenEqual: while the live table is the one the
// hub applied, a check only lists it: nothing is applied, no event. The
// order of set elements is not drift.
func TestFirewallReassertNoopWhenEqual(t *testing.T) {
	env, nft := startFirewallHub(t, func(c *config.Config) {
		c.Nodes = append(c.Nodes,
			config.Node{ID: "de-1", Name: "de-1", PublicIP: "1.2.3.4", CertFingerprint: config.SpecPlaceholderFingerprint},
			config.Node{ID: "nl-1", Name: "nl-1", PublicIP: "5.6.7.8", CertFingerprint: config.SpecPlaceholderFingerprint})
	})
	require.Contains(t, nft.live(), "elements = { 1.2.3.4, 5.6.7.8 }")
	applies, lists := nft.counts()
	for range 3 {
		require.Empty(t, env.h.reassertFirewall(ctxT(t)))
	}
	// nft may list set elements in another order or wrapped.
	nft.edit(func(s string) string {
		return strings.Replace(s, "elements = { 1.2.3.4, 5.6.7.8 }", "elements = { 5.6.7.8,\n\t\t\t     1.2.3.4 }", 1)
	})
	require.Empty(t, env.h.reassertFirewall(ctxT(t)))
	got, gotLists := nft.counts()
	require.Equal(t, applies, got)
	require.Equal(t, lists+4, gotLists)
	require.Zero(t, env.countEvents(state.EvFirewallReapplied))
}

// TestFirewallReassertDisabledWhenUnmanaged: with
// security.firewall_managed false nothing is listed or applied, whatever
// firewall_reassert_s says; firewall_reassert_s 0 turns the check off.
func TestFirewallReassertDisabledWhenUnmanaged(t *testing.T) {
	env, nft := startFirewallHub(t, func(c *config.Config) { c.Security.FirewallManaged = false })
	applies, lists := nft.counts()
	require.Empty(t, env.h.reassertFirewall(ctxT(t)))
	gotA, gotL := nft.counts()
	require.Equal(t, applies, gotA)
	require.Equal(t, lists, gotL)

	off := 0
	env, nft = startFirewallHub(t, func(c *config.Config) { c.Security.FirewallReassertS = &off })
	applies, lists = nft.counts()
	require.Positive(t, applies)
	nft.flush()
	require.Empty(t, env.h.reassertFirewall(ctxT(t)))
	gotA, gotL = nft.counts()
	require.Equal(t, applies, gotA)
	require.Equal(t, lists, gotL, "off: not even listed, also not after an apply")
	require.Zero(t, env.countEvents(state.EvFirewallReapplied))
}

// TestFirewallReassertEventDeduped: every check re-applies a missing or
// changed table, but firewall_reapplied is emitted at most once per 10
// minutes per reason.
func TestFirewallReassertEventDeduped(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC).UnixNano())
	env, nft := startFirewallHub(t, nil, func(o *Options, _ string) {
		o.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	})
	applies, _ := nft.counts()
	for i := range 3 {
		nft.flush()
		require.Equal(t, reassertMissing, env.h.reassertFirewall(ctxT(t)))
		got, _ := nft.counts()
		require.Equal(t, applies+i+1, got, "re-applied every time")
	}
	require.Equal(t, 1, env.countEvents(state.EvFirewallReapplied))

	nft.edit(func(s string) string { return s + "\n" })
	nft.edit(func(s string) string { return strings.Replace(s, "accept", "drop", 1) })
	require.Equal(t, reassertDrift, env.h.reassertFirewall(ctxT(t)))
	require.Equal(t, 2, env.countEvents(state.EvFirewallReapplied), "another reason")

	clock.Add(int64(9 * time.Minute))
	nft.flush()
	require.Equal(t, reassertMissing, env.h.reassertFirewall(ctxT(t)))
	require.Equal(t, 2, env.countEvents(state.EvFirewallReapplied))
	clock.Add(int64(time.Minute + time.Second))
	nft.flush()
	require.Equal(t, reassertMissing, env.h.reassertFirewall(ctxT(t)))
	require.Equal(t, 3, env.countEvents(state.EvFirewallReapplied))
}

// TestFirewallReassertLoop: the hub checks on its own every
// security.firewall_reassert_s seconds (the test interval is shortened).
func TestFirewallReassertLoop(t *testing.T) {
	env, nft := startFirewallHub(t, nil, func(o *Options, _ string) { o.FirewallReassertTick = 20 * time.Millisecond })
	nft.flush()
	require.Eventually(t, func() bool { return nft.live() != "" }, testWait, 10*time.Millisecond)
	env.waitEvent(state.EvFirewallReapplied, "")
}

// TestRollbackRefusedWithFirewallReassertKey: security.firewall_reassert_s
// (written only when set) and a Telegram event list naming
// firewall_reapplied are unknown to releases before 0.3.1. Every
// 0.3.1-edge.N sorts before 0.3.1, so while either is set a rollback or a
// downgrade to such a build is refused (DEY-S011); updates to newer builds
// are not. Without them nothing is refused.
func TestRollbackRefusedWithFirewallReassertKey(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old }) // after the hub stopped
	version.Version = "0.3.1-edge.5"
	every := 30
	env := startHub(t, func(c *config.Config) { c.Security.FirewallReassertS = &every })
	l := env.h.Local()
	ctx := ctxT(t)
	bin := filepath.Join(env.root, config.BinaryPath)
	prev := filepath.Join(env.root, config.PrevBinaryPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(prev), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!new"), 0o755))  // #nosec G306 -- test binary
	require.NoError(t, os.WriteFile(prev, []byte("#!old"), 0o755)) // #nosec G306 -- test binary
	env.h.recordPrevious("0.3.1-edge.4")

	_, err := l.UpdateRollback(ctx)
	require.Equal(t, deyerr.S011, codeOf(err))
	require.Contains(t, deyerr.As(err).Message(), "security.firewall_reassert_s (since 0.3.1)")
	for _, v := range []string{"0.3.1-edge.4", "0.3.0"} {
		_, err = l.UpdateApply(ctx, v, nil)
		require.Equal(t, deyerr.S011, codeOf(err), v)
	}
	for _, v := range []string{"0.3.1-edge.6", "0.3.1"} {
		_, err = l.UpdateApply(ctx, v, nil)
		require.NotEqual(t, deyerr.S011, codeOf(err), v)
	}

	// The key goes; a Telegram event list naming the new event blocks the
	// same way.
	_, err = env.h.mutate(func(c *config.Config) error {
		c.Security.FirewallReassertS = nil
		c.Hub.Notify.Telegram.Events = []string{"down", state.EvFirewallReapplied}
		return nil
	})
	require.NoError(t, err)
	_, err = l.UpdateRollback(ctx)
	require.Equal(t, deyerr.S011, codeOf(err))
	require.Contains(t, deyerr.As(err).Message(), "hub.notify.telegram.events: firewall_reapplied (since 0.3.1)")

	_, err = env.h.mutate(func(c *config.Config) error {
		c.Hub.Notify.Telegram.Events = []string{"down"}
		return nil
	})
	require.NoError(t, err)
	_, err = l.UpdateRollback(ctx)
	require.NoError(t, err)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	require.Equal(t, "#!old", string(data))
}
