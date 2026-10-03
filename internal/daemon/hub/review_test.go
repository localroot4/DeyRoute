package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

// Tests of the fixes of the hub daemon review.

// The crash watch reports a backend that systemd restarted (spec section 9
// event backend_crash), on the hub and on the node, once a minute per unit.
func TestCrashWatchEmitsBackendCrash(t *testing.T) {
	te := startTunnelHub(t, func(o *Options, _ string) { o.CrashCheckInterval = 20 * time.Millisecond })
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	unit := hubUnit(id, "de-1", trAlpha)
	inst := systemd.InstanceName(id, "de-1", trAlpha)
	require.Never(t, func() bool { return te.countEvents(state.EvBackendCrash) > 0 }, 150*time.Millisecond, 20*time.Millisecond,
		"no restart, no crash")

	// systemd restarted the hub unit twice.
	te.sd.setRestarts(unit, 2)
	ev := te.waitEvent(state.EvBackendCrash, "de-1")
	require.Equal(t, id, ev.Tunnel)
	require.Equal(t, trAlpha, ev.ToTransport)
	require.Equal(t, string(deyerr.B011), ev.Code)
	require.Equal(t, state.LevelError, ev.Level)
	require.Contains(t, ev.Reason, "the hub")
	require.Contains(t, ev.Reason, "2 time(s)")

	// Another restart within a minute is logged, not a second event.
	te.sd.setRestarts(unit, 3)
	require.Never(t, func() bool { return te.countEvents(state.EvBackendCrash) > 1 }, 150*time.Millisecond, 20*time.Millisecond)

	// The node's unit crashed: its own event.
	n.tmu.Lock()
	n.nrestarts[inst] = 1
	n.tmu.Unlock()
	require.Eventually(t, func() bool { return te.countEvents(state.EvBackendCrash) == 2 }, testWait, 20*time.Millisecond)
	evs, err := te.h.st.Events(state.EventFilter{Types: []string{state.EvBackendCrash}})
	require.NoError(t, err)
	require.Contains(t, evs[0].Reason, "node de-1")
	// The tunnel stays up: the crash watch only reports.
	te.waitActive(id, "de-1", trAlpha)
}

// A node whose public IP changed reconnects: config.yaml follows, and a
// forward transport (the hub dials the node) is rendered with the new
// address and its active hub unit restarts on it.
func TestNodeIPChangeRestartsTheForwardTransport(t *testing.T) {
	te := startTunnelHub(t, withIPForward)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trNAT, Failover: fastFailover(false)})
	id := info.ID
	unit := hubUnit(id, "de-1", trNAT)
	conf := filepath.Join(te.root, config.BackendsConfDir, "tfa", id, "de-1", "nat", "test.conf")
	requireFileContains(t, conf, "node=127.0.0.1")

	// While the node is away the hub renders the address it knew.
	n.stop()
	_, err := te.h.mutate(func(c *config.Config) error {
		nd, _ := c.NodeByID("de-1")
		nd.PublicIP = "127.0.0.2"
		return nil
	})
	require.NoError(t, err)
	c := te.h.tun.lookup(id)
	require.NotNil(t, c)
	te.h.tun.opMu.Lock()
	err = c.update(ctxT(t), updateOpts{})
	te.h.tun.opMu.Unlock()
	require.NoError(t, err)
	requireFileContains(t, conf, "node=127.0.0.2")
	restarts := te.sd.count("restart", unit)

	// It comes back from its real address.
	n.start()
	ev := te.waitEvent(state.EvNodeIPChanged, "de-1")
	require.Contains(t, ev.Message, "127.0.0.2 to 127.0.0.1")
	require.Eventually(t, func() bool { return te.sd.count("restart", unit) > restarts }, testWait, 20*time.Millisecond,
		"the active forward transport restarts on the new address")
	requireFileContains(t, conf, "node=127.0.0.1")
	te.waitActive(id, "de-1", trNAT)
}

// requireFileContains fails unless path contains s.
func requireFileContains(t *testing.T, path, s string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), s)
}

// Units a node still runs although no candidate there is started (it
// missed the stops) are stopped once its first heartbeat lists them.
func TestNodeStraysStopAfterReconnect(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(false)})
	id := info.ID
	alpha := systemd.InstanceName(id, "de-1", trAlpha)
	beta := systemd.InstanceName(id, "de-1", trBeta)
	canary := systemd.CanaryInstance(id)

	n.stop()
	n.setUnit(beta, "active")
	n.setUnit(canary, "active")
	n.start()
	require.Eventually(t, func() bool {
		l := n.stoppedList()
		return slices.Contains(l, beta) && slices.Contains(l, canary)
	}, testWait, 20*time.Millisecond)
	require.NotContains(t, n.stoppedList(), alpha, "the active candidate keeps running")
	te.waitActive(id, "de-1", trAlpha)
}

// Until the startup reconcile adopted the running candidates, the NAT
// rules of the previous hub process stay in the firewall (a hub restart or
// update does not interrupt a NAT transport).
func TestFirewallKeepsNATUntilTheStartupReconcile(t *testing.T) {
	te := startTunnelHub(t, withIPForward)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trNAT, Failover: fastFailover(false)})
	id := info.ID
	require.Eventually(t, func() bool { return len(te.h.Firewall().Spec.NAT) > 0 }, testWait, 20*time.Millisecond)
	n.stop()
	te.stop()

	// The new hub process: its startup reconcile waits for systemctl.
	gate := make(chan struct{})
	te.sd.mu.Lock()
	te.sd.listGate = gate
	te.sd.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			te.sd.mu.Lock()
			te.sd.listGate = nil
			te.sd.mu.Unlock()
			close(gate)
		})
	}
	o := te.o
	o.OnReady = nil
	env2 := &testEnv{t: t, root: te.root, sock: te.sock, runner: te.runner, ca: te.ca}
	te.sd.env = env2
	env2.startEnv(o)
	t.Cleanup(release) // runs before the hub stops
	require.Eventually(t, func() bool {
		fw := env2.h.Firewall()
		return fw.Computed && len(fw.Spec.NAT) > 0
	}, testWait, 20*time.Millisecond, "the NAT of the running candidate stays")
	require.False(t, env2.h.tunnelsReady())

	release()
	require.Eventually(t, func() bool {
		ts, _ := env2.h.tunnelState(id)
		return env2.h.tunnelsReady() && ts.State == state.StateUp && ts.Active.Transport == trNAT
	}, testWait, 20*time.Millisecond)
	require.NotEmpty(t, env2.h.Firewall().Spec.NAT, "the adopted candidate keeps its NAT")
	require.Equal(t, 1, env2.h.tun.lookup(id).startedCount())
}

// startedCount is the number of started candidates (tests).
func (c *tunnelCtl) startedCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.started)
}

// security.firewall_managed switched off by a config apply removes the
// table deyroute applied (a stale @nodes restriction would block new nodes);
// a tuning change is reported, not applied.
func TestConfigApplyUnmanagedFirewallRemovesTheTable(t *testing.T) {
	env, o := prepareEnv(t, nil)
	o.DisableFirewall = false
	env.startEnv(o)
	require.Eventually(t, func() bool { return len(env.nftScripts()) > 0 }, testWait, 10*time.Millisecond)

	path := filepath.Join(env.root, config.DefaultPath)
	c, err := config.LoadWith(path, testValidate)
	require.NoError(t, err)
	c.Security.FirewallManaged = false
	c.Tuning.SysctlProfile = config.SysctlAggressive
	require.NoError(t, config.SaveWith(path, c, testValidate))
	res, err := env.client.ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	require.Contains(t, res.Changed, "security")
	require.Contains(t, res.Changed, "tuning")
	joined := ""
	for _, w := range res.Warnings {
		joined += w + "\n"
	}
	require.Contains(t, joined, "deyroute optimize apply --profile aggressive")
	require.Contains(t, joined, "firewall_managed is false")
	require.Eventually(t, func() bool { return env.runner.Called("nft delete table inet deyroute") }, testWait, 10*time.Millisecond)

	// Not managed any more: nothing is applied or removed again.
	scripts := len(env.nftScripts())
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	require.Len(t, env.nftScripts(), scripts)
	require.Equal(t, 1, env.runner.Count("nft delete table inet deyroute"))
	require.False(t, env.h.Firewall().Managed)
}

// The watchdog pings systemd only while the hub is responsive.
func TestWatchdogPingsWhileTheHubIsResponsive(t *testing.T) {
	var (
		mu    sync.Mutex
		pings int
	)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return pings
	}
	env := startHub(t, nil, func(o *Options, _ string) {
		o.Getenv = func(k string) string {
			if k == "WATCHDOG_USEC" {
				return "100000" // a ping every 50 ms
			}
			return ""
		}
		o.Notify = func(s string) error {
			if s == systemd.StateWatchdog {
				mu.Lock()
				pings++
				mu.Unlock()
			}
			return nil
		}
	})
	require.Eventually(t, func() bool { return count() >= 2 }, testWait, 10*time.Millisecond)

	// The registry hangs: the offline detector cannot run, the pings stop.
	env.h.nodesMu.Lock()
	time.Sleep(250 * time.Millisecond)
	before := count()
	time.Sleep(300 * time.Millisecond)
	stalled := count()
	env.h.nodesMu.Unlock()
	require.Equal(t, before, stalled)
	require.Eventually(t, func() bool { return count() > stalled }, testWait, 10*time.Millisecond)
}

func TestWatchdogInterval(t *testing.T) {
	env := map[string]string{}
	h := &Hub{o: Options{Getenv: func(k string) string { return env[k] }}}
	require.Zero(t, h.watchdogInterval())
	env["WATCHDOG_USEC"] = "30000000"
	require.Equal(t, 15*time.Second, h.watchdogInterval())
	env["WATCHDOG_PID"] = "1"
	require.Zero(t, h.watchdogInterval(), "addressed to another process")
	env["WATCHDOG_PID"] = ""
	env["WATCHDOG_USEC"] = "soon"
	require.Zero(t, h.watchdogInterval())
}

// A removed node leaves no certificate or installation records behind.
func TestNodeRemoveForgetsItsRecords(t *testing.T) {
	env := startHub(t, nil)
	env.joinNode("de-1")
	var rec nodeCertRecord
	ok, err := env.h.st.GetMeta(metaNodeCert+"de-1", &rec)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, env.h.st.PutMeta(metaPendingCert+"de-1", "sha256:"+hex.EncodeToString(make([]byte, 32))))
	key := installKey("de-1", backend.ManifestEntry{Name: "x", Version: "1"})
	env.h.rememberInstall(key, nil)

	require.NoError(t, env.client.NodeRemove(ctxT(t), "de-1"))
	for _, k := range []string{metaNodeCert + "de-1", metaPendingCert + "de-1"} {
		ok, err := env.h.st.GetMeta(k, &rec)
		require.NoError(t, err)
		require.False(t, ok, k)
	}
	done, _ := env.h.cachedInstall(key, false)
	require.False(t, done)
}

// publishRelease publishes deyroute ver for arch on rs (latest and v<ver>).
func publishRelease(t *testing.T, rs *releaseServer, ver, arch string, bin []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "deyroute", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg}))
	_, err := tw.Write(bin)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	archive := install.ArchiveName(ver, arch)
	s := sha256.Sum256(buf.Bytes())
	sums := []byte(hex.EncodeToString(s[:]) + "  " + archive + "\n")
	for _, dir := range []string{"/latest/", "/v" + ver + "/"} {
		rs.put(dir+install.SumsFile, sums)
		rs.put(dir+install.SumsSigFile, rs.sign(sums))
		rs.put(dir+archive, buf.Bytes())
	}
}

// The binary of another architecture comes from the signed release of the
// hub's version (verified with Options.MinisignKey) and is cached.
func TestAssetOtherArchitectureFromTheSignedRelease(t *testing.T) {
	old := version.Version
	version.Version = "1.2.3"
	defer func() { version.Version = old }()
	rs := newReleaseServer(t)
	bin := []byte("#!deyroute 1.2.3 arm64")
	publishRelease(t, rs, "1.2.3", "arm64", bin)
	env := startHub(t, nil, releaseOpts(rs))

	info, err := env.h.asset(ctxT(t), "arm64")
	require.NoError(t, err)
	data, err := io.ReadAll(info.Reader)
	require.NoError(t, info.Reader.Close())
	require.NoError(t, err)
	require.Equal(t, bin, data)
	require.Equal(t, sum(bin), info.SHA256)
	require.Equal(t, "1.2.3", info.Version)
	cached, err := os.ReadFile(env.h.assetCachePath("arm64"))
	require.NoError(t, err)
	require.Equal(t, bin, cached)
}

// A changed decoy list is tested at once; the first reachable decoy is
// used (section 7.4).
func TestDecoyListChangeIsCheckedAtOnce(t *testing.T) {
	checked := make(chan string, 64)
	env := startHub(t, nil, func(o *Options, _ string) {
		o.DecoyCheck = func(_ context.Context, sni string) error {
			select {
			case checked <- sni:
			default:
			}
			if sni == "bad.example" {
				return errors.New("connection refused")
			}
			return nil
		}
	})
	require.NoError(t, env.client.SettingsSet(ctxT(t), api.SettingsRequest{Decoys: []string{"bad.example", "good.example"}}))
	deadline := time.After(testWait)
	for seen := false; !seen; {
		select {
		case sni := <-checked:
			seen = sni == "good.example"
		case <-deadline:
			t.Fatal("the new decoy list was not checked")
		}
	}
	require.Eventually(t, func() bool { return env.h.currentDecoy(env.h.Config()) == "good.example" }, testWait, 10*time.Millisecond)
}

// A decoy list edited in config.yaml (config edit / config apply) is tested
// at once as well, not only at the next periodic check.
func TestConfigApplyChecksAnEditedDecoyList(t *testing.T) {
	checked := make(chan string, 64)
	env := startHub(t, nil, func(o *Options, _ string) {
		o.DecoyCheck = func(_ context.Context, sni string) error {
			select {
			case checked <- sni:
			default:
			}
			if sni == "bad.example" {
				return errors.New("connection refused")
			}
			return nil
		}
	})
	c, err := config.LoadWith(env.h.cfgPath, testValidate)
	require.NoError(t, err)
	c.Hub.DecoySNIs = []string{"bad.example", "good.example"}
	require.NoError(t, config.SaveWith(env.h.cfgPath, c, testValidate))
	_, err = env.client.ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	deadline := time.After(testWait)
	for seen := false; !seen; {
		select {
		case sni := <-checked:
			seen = sni == "good.example"
		case <-deadline:
			t.Fatal("the edited decoy list was not checked")
		}
	}
	require.Eventually(t, func() bool { return env.h.currentDecoy(env.h.Config()) == "good.example" }, testWait, 10*time.Millisecond)
}

// A canary whose node side cannot start (the node is going away) does not
// leave its hub side running; it is built again once the node answers.
func TestCanaryPartialStartIsStopped(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(true)})
	id := info.ID
	canaryInst := systemd.CanaryInstance(id)
	canary := systemd.UnitName(canaryInst)
	n.tmu.Lock()
	n.failStart = func(inst string) error {
		if inst == canaryInst {
			return deyerr.New(deyerr.N003, deyerr.Params{"node": "de-1"})
		}
		return nil
	}
	n.tmu.Unlock()
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trBeta}))
	require.Eventually(t, func() bool {
		return te.sd.count("start", canary) >= 1 && te.sd.count("stop", canary) >= 1
	}, testWait, 20*time.Millisecond, "the hub side of the canary is stopped again")

	n.tmu.Lock()
	n.failStart = nil
	n.tmu.Unlock()
	require.Eventually(t, func() bool { return te.sd.state(canary) == "active" }, testWait, 20*time.Millisecond)
	require.Eventually(t, func() bool { return n.startedCount(canaryInst) >= 1 }, testWait, 20*time.Millisecond)
}

// A controller halted before it was launched (the hub stopping during a
// start) never starts its engine.
func TestLaunchAfterHaltStartsNothing(t *testing.T) {
	te := startTunnelHub(t)
	te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha}, Failover: fastFailover(false)})
	require.NoError(t, te.client.TunnelSetEnabled(ctxT(t), info.ID, false))
	c := newTunnelCtl(te.h, info.ID)
	_, err := c.apply(ctxT(t), applyOpts{})
	require.NoError(t, err)
	c.halt()
	c.launch(ctxT(t), nil)
	require.Nil(t, c.eng())
	c.halt() // idempotent, nothing to wait for
}

// A tunnel deleted while its primary node is offline leaves no canary echo
// behind: the node stops it when it reconnects.
func TestDeleteWhileOfflineStopsTheCanaryEcho(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(true)})
	id := info.ID
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trBeta}))
	canary := systemd.UnitName(systemd.CanaryInstance(id))
	require.Eventually(t, func() bool { return te.sd.state(canary) == "active" }, testWait, 20*time.Millisecond)
	var echo int
	ok, err := te.h.st.GetMeta(metaCanaryEcho+id, &echo)
	require.NoError(t, err)
	require.True(t, ok)
	n.tmu.Lock()
	_, running := n.echoes[echo]
	n.tmu.Unlock()
	require.True(t, running)

	n.stop()
	te.waitOnline("de-1", false)
	require.NoError(t, te.client.TunnelDelete(ctxT(t), id, nil))
	n.start()
	require.Eventually(t, func() bool {
		n.tmu.Lock()
		defer n.tmu.Unlock()
		_, running := n.echoes[echo]
		return !running
	}, testWait, 20*time.Millisecond, "the orphaned echo is stopped")
	require.Eventually(t, func() bool {
		ok, err := te.h.st.GetMeta(metaEchoOrphans+"de-1", &[]int{})
		return err == nil && !ok
	}, testWait, 20*time.Millisecond)
}

// A canary left on a former primary node (it was offline when the node
// order changed) is stopped and removed when that node reconnects.
func TestCanaryOnFormerPrimaryIsRemoved(t *testing.T) {
	te := startTunnelHub(t)
	de := te.tunnelNode("de-1")
	te.tunnelNode("nl-1")
	port := freePort(t)
	info, _ := te.addTunnelUp(api.TunnelAddRequest{Node: "de-1", Backups: []string{"nl-1"}, Ports: []api.PortSpec{{Listen: port}},
		Rungs: []string{trAlpha, trBeta}, Failover: fastFailover(true)})
	id := info.ID
	canaryInst := systemd.CanaryInstance(id)
	require.NoError(t, te.client.TunnelSwitch(ctxT(t), id, api.SwitchRequest{Transport: trBeta}))
	require.Eventually(t, func() bool { return de.startedCount(canaryInst) >= 1 }, testWait, 20*time.Millisecond)

	de.stop()
	_, err := te.client.TunnelEdit(ctxT(t), id, api.TunnelEditRequest{Nodes: []string{"nl-1", "de-1"}}, nil)
	require.NoError(t, err)
	de.start()
	require.Eventually(t, func() bool {
		return slices.Contains(de.stoppedList(), canaryInst) && slices.Contains(de.removedList(), canaryInst)
	}, testWait, 20*time.Millisecond)
}

// A changed hub.control_port takes effect at the next restart: the
// firewall keeps protecting the port the hub listens on, and the online
// nodes are told the new address so they reconnect there after the restart.
func TestConfigApplyControlPortIsAnnounced(t *testing.T) {
	env, o := prepareEnv(t, nil)
	o.DisableFirewall = false
	env.startEnv(o)
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	var setHub api.SetHubArgs
	n.on(api.CmdSetHub, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		decode(t, cmd, &setHub)
		return nil, nil
	})
	require.Eventually(t, func() bool { return len(env.nftScripts()) > 0 }, testWait, 10*time.Millisecond)

	path := filepath.Join(env.root, config.DefaultPath)
	c, err := config.LoadWith(path, testValidate)
	require.NoError(t, err)
	c.Hub.ControlPort = 44434
	require.NoError(t, config.SaveWith(path, c, testValidate))
	res, err := env.client.ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	joined := strings.Join(res.Warnings, "\n")
	require.Contains(t, joined, "hub.control_port changed to 44434")
	require.Contains(t, joined, "until then it keeps listening on 44433")
	require.Contains(t, joined, "nodes that switch to 127.0.0.1:44434 at the restart: de-1")
	require.Equal(t, "127.0.0.1:44434", setHub.Addr)

	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	scripts := env.nftScripts()
	last := scripts[len(scripts)-1]
	require.Contains(t, last, "44433", "the listening port stays protected")
	require.NotContains(t, last, "44434")
}
