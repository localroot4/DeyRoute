package notify

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestAliases(t *testing.T) {
	a := Aliases()
	want := map[string][]string{
		"down":         {"tunnel_down"},
		"up":           {"tunnel_up"},
		"degraded":     {"tunnel_degraded"},
		"switch":       {"switch_transport", "switch_node"},
		"failback":     {"failback", "failback_failed"},
		"node_offline": {"node_offline"},
		"node_online":  {"node_online"},
		"flapping":     {"flapping"},
		"service_down": {"service_down"},
		"backend_crash": {
			"backend_crash",
		},
		"probe_error": {"probe_error"},
		"update":      {"update_applied", "update_rolled_back", "backend_update_rolled_back"},
	}
	assert.Equal(t, want, a)

	// The result is a copy.
	a["down"][0] = "changed"
	delete(a, "up")
	assert.Equal(t, want, Aliases())
}

func TestResolve(t *testing.T) {
	set, err := Resolve(nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{
		state.EvTunnelDown: true, state.EvSwitchTransport: true, state.EvSwitchNode: true,
		state.EvFailback: true, state.EvFailbackFailed: true, state.EvNodeOffline: true,
	}, set, "empty = the section 4 default [down, switch, failback, node_offline]")

	set, err = Resolve([]string{" UP ", "switch_node", "acme_failed", "update"})
	require.NoError(t, err)
	var got []string
	for k := range set {
		got = append(got, k)
	}
	sort.Strings(got)
	assert.Equal(t, []string{"acme_failed", "backend_update_rolled_back", "switch_node", "tunnel_up", "update_applied", "update_rolled_back"}, got)

	_, err = Resolve([]string{"down", "bogus", "tunnel_sideways"})
	require.Error(t, err)
	assert.True(t, deyerr.HasCode(err, deyerr.C013))
	assert.Contains(t, err.Error(), "bogus")
	assert.Contains(t, err.Error(), "tunnel_sideways")
	e := deyerr.As(err)
	assert.Contains(t, e.Why(), "node_offline")
	assert.Error(t, Validate([]string{"nope"}))
	assert.NoError(t, Validate([]string{"down", "tunnel_down"}))
}

func TestEventNamesAndValid(t *testing.T) {
	names := EventNames()
	assert.True(t, sort.StringsAreSorted(names))
	for _, n := range []string{state.EvTunnelUp, state.EvUpdateRolledBack, state.EvBackendRolledBack, state.EvConfigApplied} {
		assert.Contains(t, names, n)
	}
	valid := Valid()
	assert.True(t, sort.StringsAreSorted(valid))
	seen := map[string]bool{}
	for _, v := range valid {
		assert.False(t, seen[v], "duplicate %s", v)
		seen[v] = true
	}
	assert.True(t, seen["switch"] && seen["switch_transport"] && seen["update"])
	// Every alias target is a known event name.
	for alias, evs := range Aliases() {
		for _, ev := range evs {
			assert.Contains(t, names, ev, alias)
		}
	}
	assert.Equal(t, "down,switch,failback,node_offline", strings.Join(DefaultEvents, ","))
}
