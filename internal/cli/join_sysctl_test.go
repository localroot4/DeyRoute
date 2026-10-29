package cli

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
)

// fakeJoin records the join options; the result reports the profile and
// the reserved ports the options ask for.
func (e *env) fakeJoin(got *setup.JoinOptions, calls *int) {
	e.g.Ops.Join = func(_ context.Context, o setup.JoinOptions) (*setup.JoinResult, error) {
		*got = o
		*calls++
		steps(o.Progress, api.Step{Title: setup.StepTitle(setup.StepJoin), Status: api.StepOK})
		res := &setup.JoinResult{NodeID: "de-1", HubName: "ir-1", HubAddr: "5.6.7.8:44433", Compatible: true, SysctlProfile: config.SysctlOff}
		if o.ApplySysctl {
			res.SysctlProfile = o.SysctlProfile
			res.ReservedPorts = append(setup.NodeReserved(), o.ReservePorts...)
		}
		return res, nil
	}
}

const testLink = "dey://TOKENTOKENTOKENTOKEN@5.6.7.8:44433#sha256:abababababababababababababababababababababababababababababababab"

// TestJoinSysctlFlagSkipsQuestion: join --sysctl off|balanced|auto applies
// that profile without the kernel question, also on a terminal; off (and a
// "no" to the question) is an explicit opt-out the node remembers. Without
// the flag a script still gets balanced and a terminal is asked.
func TestJoinSysctlFlagSkipsQuestion(t *testing.T) {
	e := newEnv(t)
	var jo setup.JoinOptions
	calls := 0
	e.fakeJoin(&jo, &calls)
	for _, c := range []struct {
		value, profile string
		apply, optOut  bool
	}{
		{"balanced", config.SysctlBalanced, true, false},
		{"auto", config.SysctlAuto, true, false},
		{"off", config.SysctlBalanced, false, true},
		{"BALANCED", config.SysctlBalanced, true, false},
	} {
		e.tty() // no answer: a question would abort
		out := e.ok("join", testLink, "--sysctl", c.value)
		require.NotContains(t, out, "Kernel network profile", c.value)
		require.Equal(t, c.apply, jo.ApplySysctl, c.value)
		require.Equal(t, c.optOut, jo.OptOut, c.value)
		if c.apply {
			require.Equal(t, c.profile, jo.SysctlProfile, c.value)
		}
	}
	doc := e.json("join", testLink, "--sysctl", "off")
	require.Equal(t, "off", doc["sysctl_profile"])
	require.Equal(t, []any{}, doc["reserved_ports"], "never null")

	// Without the flag: a script gets balanced, a terminal is asked.
	e.g.IsTTY = false
	e.ok("join", testLink)
	require.True(t, jo.ApplySysctl)
	require.False(t, jo.OptOut)
	require.Equal(t, config.SysctlBalanced, jo.SysctlProfile)
	e.tty("n")
	out := e.ok("join", testLink)
	require.Contains(t, out, "Kernel network profile")
	require.False(t, jo.ApplySysctl)
	require.True(t, jo.OptOut, "no to the question opts out")
	e.tty("")
	e.ok("join", testLink)
	require.True(t, jo.ApplySysctl)
	require.False(t, jo.OptOut)
	// The node wizard's "no" opts out the same way.
	e.tty("2", testLink, "", "n")
	e.ok("setup")
	require.False(t, jo.ApplySysctl)
	require.True(t, jo.OptOut)

	e.g.IsTTY = false
	n := calls
	errOut := e.fail(1, "join", testLink, "--sysctl", "aggressive")
	require.Contains(t, errOut, "DEY-C013")
	require.Contains(t, errOut, "Invalid value for --sysctl: 'aggressive'")
	require.Contains(t, errOut, "--sysctl off")
	require.Equal(t, n, calls, "nothing joined")
	require.Contains(t, e.ok("join", "--help"), "--sysctl")
	require.Contains(t, e.ok("join", "--help"), "--reserve-ports")
}

// TestJoinReservePortsRejectsBadRange: --reserve-ports takes ports 1-65535
// and ranges a-b, at most 64 entries, and must leave at least 1024 ports of
// the profile's ephemeral range free; anything else is DEY-C013 (field
// --reserve-ports) before the join starts.
func TestJoinReservePortsRejectsBadRange(t *testing.T) {
	e := newEnv(t)
	var jo setup.JoinOptions
	calls := 0
	e.fakeJoin(&jo, &calls)
	many := make([]string, 65)
	for i := range many {
		many[i] = strconv.Itoa(1000 + i)
	}
	for _, v := range []string{"0", "70000", "9000-8000", "abc", "1-2-3", "443,", " ", "-5", "8443-", strings.Join(many, ",")} {
		errOut := e.fail(1, "join", testLink, "--reserve-ports", v)
		require.Contains(t, errOut, "DEY-C013", v)
		require.Contains(t, errOut, "Invalid value for --reserve-ports", v)
		require.Contains(t, errOut, "--reserve-ports 443,8443,20000-20999", v)
	}
	// 64 entries are fine; so is the whole list split over repeated flags.
	e.ok("join", testLink, "--reserve-ports", strings.Join(many[:40], ","), "--reserve-ports", strings.Join(many[40:64], ","))
	require.Len(t, jo.ReservePorts, 64)
	errOut := e.fail(1, "join", testLink, "--reserve-ports", strings.Join(many[:40], ","), "--reserve-ports", strings.Join(many[40:], ","))
	require.Contains(t, errOut, "at most 64")
	// Too few ephemeral ports left (10240-65535 minus the control range).
	errOut = e.fail(1, "join", testLink, "--reserve-ports", "10240-29999,32000-64600")
	require.Contains(t, errOut, "DEY-C013")
	require.Contains(t, errOut, "1024")
	require.Contains(t, errOut, "10240-65535")
	e.ok("join", testLink, "--reserve-ports", "10240-29999,32000-64000")
	require.Equal(t, 2, calls, "only the two valid lists joined")
}

// TestJoinReservePortsWithOffRejected: --reserve-ports with --sysctl off,
// or with "no" to the kernel question, is DEY-C013: off writes nothing.
func TestJoinReservePortsWithOffRejected(t *testing.T) {
	e := newEnv(t)
	var jo setup.JoinOptions
	calls := 0
	e.fakeJoin(&jo, &calls)
	errOut := e.fail(1, "join", testLink, "--sysctl", "off", "--reserve-ports", "8443")
	require.Contains(t, errOut, "DEY-C013")
	require.Contains(t, errOut, "Invalid value for --reserve-ports: '8443'")
	require.Contains(t, errOut, "--sysctl balanced")
	e.tty("n")
	errOut = e.fail(1, "join", testLink, "--reserve-ports", "8443")
	require.Contains(t, errOut, "--reserve-ports")
	require.Zero(t, calls)
}

// TestJoinReservePortsFlagMerges: repeated --reserve-ports flags merge;
// duplicates and entries inside the control range drop out; the JSON
// document lists every reserved entry.
func TestJoinReservePortsFlagMerges(t *testing.T) {
	e := newEnv(t)
	var jo setup.JoinOptions
	calls := 0
	e.fakeJoin(&jo, &calls)
	doc := e.json("join", testLink, "--reserve-ports", "443, 8443", "--reserve-ports", "20000-20999,8443,30500,30000-31999,0443")
	require.Equal(t, []string{"443", "8443", "20000-20999"}, jo.ReservePorts)
	require.True(t, jo.ApplySysctl)
	require.Equal(t, []any{"30000-31999", "443", "8443", "20000-20999"}, doc["reserved_ports"])
	e.json("join", testLink, "--sysctl", "auto", "--reserve-ports", "8443")
	require.Equal(t, config.SysctlAuto, jo.SysctlProfile)
	require.Equal(t, []string{"8443"}, jo.ReservePorts)
}
