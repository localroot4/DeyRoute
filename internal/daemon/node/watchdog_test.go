package node

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/systemd"
)

// The watchdog pings systemd only while the monitor makes progress: a
// deadlocked instance lock withholds the ping (systemd restarts the agent),
// and the pings resume once the monitor runs again.
func TestWatchdogPingsWhileTheAgentIsResponsive(t *testing.T) {
	a := newTestAgent(t, newFakeSys(), &recHooks{})
	var pings atomic.Int32
	a.o.Notify = func(st string) error {
		if st == systemd.StateWatchdog {
			pings.Add(1)
		}
		return nil
	}
	a.o.Getenv = func(k string) string {
		if k == "WATCHDOG_USEC" {
			return "20000" // a ping every 10 ms
		}
		return ""
	}
	a.o.HeartbeatInterval = 10 * time.Millisecond
	a.o.StallLimit = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	locked := false
	defer func() {
		cancel()
		if locked {
			a.instMu.Unlock()
		}
		wg.Wait()
	}()
	a.markAlive()
	wg.Add(2)
	go func() {
		defer wg.Done()
		a.monitorLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		a.watchdogLoop(ctx)
	}()
	eventually(t, func() bool { return pings.Load() >= 3 }, "pings while the monitor runs")

	a.instMu.Lock()
	locked = true
	time.Sleep(3 * a.o.StallLimit)
	stalled := pings.Load()
	time.Sleep(3 * a.o.StallLimit)
	require.Equal(t, stalled, pings.Load(), "no ping while the monitor is stuck")

	a.instMu.Unlock()
	locked = false
	eventually(t, func() bool { return pings.Load() > stalled }, "pings again once the monitor runs")
}

// No watchdog for this process: no interval, and the loop returns at once.
func TestWatchdogInterval(t *testing.T) {
	a := newTestAgent(t, newFakeSys(), &recHooks{})
	env := map[string]string{}
	a.o.Getenv = func(k string) string { return env[k] }
	require.Zero(t, a.watchdogInterval())
	env["WATCHDOG_USEC"] = "30000000"
	require.Equal(t, 15*time.Second, a.watchdogInterval())
	env["WATCHDOG_PID"] = strconv.Itoa(os.Getpid())
	require.Equal(t, 15*time.Second, a.watchdogInterval())
	env["WATCHDOG_PID"] = strconv.Itoa(os.Getpid() + 1)
	require.Zero(t, a.watchdogInterval())
	env["WATCHDOG_PID"], env["WATCHDOG_USEC"] = "", "bogus"
	require.Zero(t, a.watchdogInterval())
	a.watchdogLoop(context.Background()) // returns: nothing to ping
}

// The first heartbeat goes out as soon as the stream opens; while the
// monitor has not listed the units yet it says so, so the hub's per-stream
// cleanup waits for a beat with the list.
func TestFirstHeartbeatMarksUnitsUnknown(t *testing.T) {
	e := newEnv(t)
	unit := "deyroute-tun@main.de-1.backhaul-wssmux.service"
	e.sys.setState(unit, "active")
	var lists atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	e.runner.Handler = func(c exec.Call) (exec.Response, bool) {
		// The startup reconcile lists the units first; the monitor's first
		// listing (the second) is slow, as systemctl after a boot.
		if c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "list-units" && lists.Add(1) == 2 {
			<-release
		}
		return e.sys.handle(c)
	}
	s := e.start()
	t.Cleanup(open) // runs before the agent is stopped

	var first api.Heartbeat
	select {
	case first = <-s.Heartbeats():
	case <-time.After(10 * time.Second):
		t.Fatal("no heartbeat")
	}
	require.True(t, first.UnitsUnknown)
	require.Empty(t, first.Units)

	open()
	eventually(t, func() bool {
		select {
		case hb := <-s.Heartbeats():
			return !hb.UnitsUnknown && hb.Units[unit] == "active"
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, "a later heartbeat carries the unit list")
}

// A failed listing keeps the last list, marked as not current.
func TestHeartbeatKeepsTheLastUnitsWhenListingFails(t *testing.T) {
	sys := newFakeSys()
	unit := "deyroute-tun@main.de-1.backhaul-wssmux.service"
	sys.setState(unit, "active")
	a := newTestAgent(t, sys, &recHooks{})
	require.True(t, a.heartbeat().UnitsUnknown, "nothing listed yet")
	a.refresh(context.Background())
	hb := a.heartbeat()
	require.False(t, hb.UnitsUnknown)
	require.Equal(t, "active", hb.Units[unit])

	var fail atomic.Bool
	fail.Store(true)
	f := a.o.Runner.(*exec.Fake)
	inner := f.Handler
	f.Handler = func(c exec.Call) (exec.Response, bool) {
		if fail.Load() && c.Name == "systemctl" && len(c.Args) > 0 && c.Args[0] == "list-units" {
			return exec.Fail(1, "Failed to connect to bus: Connection refused"), true
		}
		return inner(c)
	}
	a.refresh(context.Background())
	hb = a.heartbeat()
	require.True(t, hb.UnitsUnknown)
	require.Equal(t, "active", hb.Units[unit], "the last list is kept")

	fail.Store(false)
	a.refresh(context.Background())
	require.False(t, a.heartbeat().UnitsUnknown)
}
