package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

func TestParseShowBlocks(t *testing.T) {
	out := "Id=a.service\nActiveState=active\nMainPID=12\nMemoryCurrent=2048\n\n" +
		"Id=b.service\nActiveState=inactive\nMainPID=0\nMemoryCurrent=[not set]\n\n" +
		"Id=c.service\nMemoryCurrent=18446744073709551615\nnonsense\n"
	got := parseShowBlocks([]byte(out))
	require.Equal(t, unitProps{ActiveState: "active", MainPID: 12, MemoryCurrent: 2048}, got["a.service"])
	require.Equal(t, unitProps{ActiveState: "inactive"}, got["b.service"])
	require.Equal(t, unitProps{}, got["c.service"])
}

// The CPU sampler, os-release and RSS readers moved to internal/sysinfo
// (TestCPUSampler, TestOSAndSelfRSS there).
func TestSystemInfoHelpers(t *testing.T) {
	require.Equal(t, []string{"a active", "b failed"}, unitList(map[string]string{"b": "failed", "a": "active"}))
	require.True(t, validTarget("127.0.0.1:443"))
	require.False(t, validTarget(":443"))
	require.False(t, validTarget("h:0"))
	require.Equal(t, health.DefaultTimeout, probeTimeout(0))
	require.Equal(t, MaxProbeTimeout, probeTimeout(10*60*1000))
	require.Equal(t, 1500*time.Millisecond, probeTimeout(1500))
	require.True(t, isRunning("activating"))
	require.False(t, isRunning("failed"))
}

func TestCountEstablishedSS(t *testing.T) {
	f := exec.NewFake().On("ss -Htn state established",
		exec.OK("0 0 127.0.0.1:443 127.0.0.1:54321\n0 0 [::1]:443 [::1]:5000\n0 0 127.0.0.1:80 1.2.3.4:5\nbad\n0 0 nocolon x\n"))
	a := &agent{o: Options{Runner: f, Root: t.TempDir()}}
	res := a.metrics(context.Background(), api.MetricsArgs{Ports: []int{443, 70000}})
	require.Equal(t, 2, res.ActiveConns)
}

func TestSplitLinesAndLineTime(t *testing.T) {
	lines, rest := splitLines([]byte("a\r\nb\npart"))
	require.Equal(t, []string{"a", "b"}, lines)
	require.Equal(t, "part", string(rest))
	long := strings.Repeat("x", maxFollowLine+1)
	lines, rest = splitLines([]byte(long))
	require.Len(t, lines, 1)
	require.Empty(t, rest)

	ts, ok := lineTime(`{"ts":"2026-09-30T10:00:00.000Z","msg":"x"}`)
	require.True(t, ok)
	require.Equal(t, 2026, ts.Year())
	_, ok = lineTime("plain text")
	require.False(t, ok)
	_, ok = lineTime(`{"msg":"no ts"}`)
	require.False(t, ok)
}

func TestReadTailSince(t *testing.T) {
	root := t.TempDir()
	p := root + "/node.log"
	writeFile(t, p, `{"ts":"2026-09-30T10:00:00Z","msg":"old"}`+"\n"+`{"ts":"2026-09-30T12:00:00Z","msg":"new"}`+"\nraw line\n", 0o600)
	lines, size, err := readTail(p, 0, time.Date(2026, 9, 30, 11, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "new")
	require.Equal(t, "raw line", lines[1])
	fi, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, fi.Size(), size)
	lines, _, err = readTail(root+"/missing.log", 5, time.Time{})
	require.NoError(t, err)
	require.Empty(t, lines)
	_, _, err = readTail(root+"\x00", 5, time.Time{})
	require.Error(t, err)
}

func TestRegistry(t *testing.T) {
	r := newRegistry()
	ctx := context.Background()
	started := make(chan struct{})
	var exitErr error
	var mu sync.Mutex
	// Generous margins: the extension must land well inside the first
	// lifetime even on a loaded -race run.
	require.True(t, r.start(ctx, "k", 400*time.Millisecond, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return errors.New("stopped")
	}, func(err error) {
		mu.Lock()
		exitErr = err
		mu.Unlock()
	}))
	<-started
	require.True(t, r.running("k"))
	require.True(t, r.extend("k", 1500*time.Millisecond))
	time.Sleep(700 * time.Millisecond)
	require.True(t, r.running("k"), "extended past the first lifetime")
	require.Eventually(t, func() bool { return !r.running("k") }, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	require.EqualError(t, exitErr, "stopped")
	mu.Unlock()
	require.False(t, r.extend("k", time.Second))
	require.False(t, r.stop("k"))

	require.True(t, r.start(ctx, "forever", 0, func(ctx context.Context) error { <-ctx.Done(); return nil }, nil))
	require.True(t, r.stop("forever"))
	require.True(t, r.start(ctx, "last", 0, func(ctx context.Context) error { <-ctx.Done(); return nil }, nil))
	r.stopAll()
	require.False(t, r.running("last"))
	require.False(t, r.start(ctx, "late", 0, func(context.Context) error { return nil }, nil))
}

// hookBackend is a registered test backend with the optional hooks.
type hookBackend struct {
	mu    sync.Mutex
	calls []string
}

func (b *hookBackend) Name() string                    { return "zzhooktest" }
func (b *hookBackend) Transports() []backend.Transport { return nil }
func (b *hookBackend) Manifest() backend.ManifestEntry {
	return backend.ManifestEntry{Name: "zzhooktest"}
}
func (b *hookBackend) Validate(backend.RenderInput) error { return nil }
func (b *hookBackend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}
func (b *hookBackend) Render(backend.RenderInput, backend.Side) (backend.Rendered, error) {
	return backend.Rendered{}, nil
}
func (b *hookBackend) rec(s string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, s)
	return nil
}
func (b *hookBackend) PreStart(_ context.Context, dir string, r hookRunner) error {
	if r == nil {
		return errors.New("no runner")
	}
	return b.rec("pre " + dir)
}
func (b *hookBackend) PostStart(_ context.Context, dir string, _ hookRunner) error {
	return b.rec("post " + dir)
}
func (b *hookBackend) PostStop(_ context.Context, dir string, _ hookRunner) error {
	return b.rec("stop " + dir)
}

var testHookBackend = &hookBackend{}

func init() { backend.Register(testHookBackend) }

func (b *hookBackend) take() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := b.calls
	b.calls = nil
	return c
}

func TestBackendHooks(t *testing.T) {
	ctx := context.Background()
	testHookBackend.take()
	h := BackendHooks{Runner: exec.NewFake(), Root: "/r"}
	require.NoError(t, h.PreStart(ctx, "zzhooktest", "/etc/deyroute/backends/zzhooktest/t/n/x"))
	require.NoError(t, h.PostStart(ctx, "zzhooktest", "/d"))
	require.NoError(t, h.PostStop(ctx, "zzhooktest", "/d"))
	require.True(t, h.HasPostStart("zzhooktest"))
	require.Equal(t, []string{"pre /r/etc/deyroute/backends/zzhooktest/t/n/x", "post /r/d", "stop /r/d"}, testHookBackend.take())

	// Backends without hooks, and unknown backends, need nothing.
	for _, name := range []string{"backhaul", "nosuchbackend"} {
		require.NoError(t, h.PreStart(ctx, name, "/d"))
		require.NoError(t, h.PostStart(ctx, name, "/d"))
		require.NoError(t, h.PostStop(ctx, name, "/d"))
		require.False(t, h.HasPostStart(name))
	}
	// The real awg backend is found through the registry.
	require.True(t, h.HasPostStart("awg"))
	custom := BackendHooks{Lookup: func(string) (backend.Backend, bool) { return testHookBackend, true }}
	require.Equal(t, "/x", custom.dir("/x"))
	require.True(t, custom.HasPostStart("anything"))
}

func TestReconcileRestoresNATAndWatchesPostStart(t *testing.T) {
	e := newEnv(t)
	awg := natArgs("awg", "userspace", "deyc-1", 2053)
	unit := unitOf(awg)
	st := nodeState{Instances: map[string]instance{awg.Instance: {
		Tunnel: "main", ConfigDir: awg.ConfigDir, Backend: "awg", NAT: awg.NAT,
	}}}
	data, err := json.Marshal(st)
	require.NoError(t, err)
	writeFile(t, e.path(StateFile), string(data), 0o600)
	e.sys.mu.Lock()
	e.sys.units[unit] = "active"
	e.sys.pids[unit] = 500
	e.sys.mu.Unlock()
	e.start()

	eventually(t, func() bool {
		sc := e.sys.nftScripts()
		return len(sc) == 1 && strings.Contains(sc[0], "127.0.0.1:2053")
	}, "the NAT of the running instance is applied at start")

	// systemd restarts amneziawg-go: the device must be configured again.
	e.sys.mu.Lock()
	e.sys.pids[unit] = 501
	e.sys.mu.Unlock()
	eventually(t, func() bool {
		for _, c := range e.hooks.list() {
			if c == "post awg "+awg.ConfigDir {
				return true
			}
		}
		return false
	}, "PostStart runs again for the new process")
}

func TestDamagedStateFileStartsEmpty(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.path(StateFile), "{not json", 0o600)
	e.start()
	require.Eventually(t, func() bool { return strings.Contains(e.logs.String(), "state file is damaged") },
		10*time.Second, 10*time.Millisecond)
}

func TestBuiltinUninstall(t *testing.T) {
	root := t.TempDir()
	sys := newFakeSys()
	sys.units["deyroute-tun@main.de-1.backhaul-wssmux.service"] = "active"
	for _, p := range []string{config.DefaultPath, "/var/lib/deyroute/bin/backhaul/v1/backhaul", config.BinaryPath,
		"/etc/systemd/system/deyroute-node.service", "/var/log/deyroute/node.log"} {
		writeFile(t, root+p, "x", 0o600)
	}
	a := newAgent(Options{Root: root, Runner: sys.runner(), Hooks: &recHooks{}}.withDefaults(),
		config.NewNode(testNode, "5.6.7.8:44433", "sha256:"+strings.Repeat("a", 64)), root+config.DefaultPath, dlog.Discard())
	a.o.StartCheckDelay = time.Millisecond
	a.started["main.de-1.backhaul-wssmux"] = true
	a.st.Instances["main.de-1.backhaul-wssmux"] = instance{Tunnel: "main", Backend: "backhaul",
		ConfigDir: "/etc/deyroute/backends/backhaul/main/de-1/wssmux",
		NAT:       []backend.NATRule{{Proto: "tcp", DportLow: 1, DportHigh: 1, ToAddr: "127.0.0.1", ToPort: 1}}}
	require.NoError(t, a.uninstall(context.Background()))
	for _, p := range []string{config.EtcDir, config.LibDir, config.BinaryPath, "/etc/systemd/system/deyroute-node.service", config.LogDir} {
		_, err := os.Stat(root + p)
		require.Truef(t, os.IsNotExist(err), "%s must be removed", p)
	}
	require.Equal(t, "inactive", sys.state("deyroute-tun@main.de-1.backhaul-wssmux.service"))
	require.GreaterOrEqual(t, sys.deletes(), 1)
	svc := sys.serviceCalls()
	require.Equal(t, []string{"disable deyroute-node.service", "stop --no-block deyroute-node.service"}, svc)
}

func TestRestartSelfDefault(t *testing.T) {
	sys := newFakeSys()
	a := &agent{o: Options{Runner: sys.runner()}}
	require.NoError(t, a.restartSelf(context.Background()))
	require.Equal(t, []string{"restart --no-block " + systemd.NodeUnit}, sys.serviceCalls())
}

func TestRegisterRenderedSecrets(t *testing.T) {
	registerRenderedSecrets(map[string][]byte{
		"client.toml": []byte("[client]\ntoken = \"tok-abcdefgh-0001\"\nauth.method = \"token\"\n"),
		"config.json": []byte(`{"privateKey": "priv-abcdefgh-0002", "public_key": "pub-not-secret-0003",` +
			` "clients": [{"id": "5f0c2a31-9b7e-4c1d-8e2f-0a1b2c3d4e5f", "flow": "xtls-rprx-vision"}]}`),
		"hy.yaml":    []byte("obfs:\n  salamander:\n    password: pw-abcdefgh-0004\n"),
		"server.tml": []byte("[server.transport.noise]\nlocal_private_key = \"noise-priv-abcdefgh-0005\"\n"),
	}, map[string]string{
		"AUTH":     "dey:chisel-pw-abcdefgh-0006",
		"RUST_LOG": "information-level",
	})
	for _, v := range []string{"tok-abcdefgh-0001", "priv-abcdefgh-0002", "pw-abcdefgh-0004",
		"5f0c2a31-9b7e-4c1d-8e2f-0a1b2c3d4e5f", "noise-priv-abcdefgh-0005", "chisel-pw-abcdefgh-0006"} {
		require.NotContains(t, dlog.Redact("backend said "+v), v)
	}
	for _, v := range []string{"pub-not-secret-0003", "information-level"} {
		require.Contains(t, dlog.Redact("backend said "+v), v)
	}
}

// newTestAgent builds an agent without running it.
func newTestAgent(t *testing.T, sys *fakeSys, hooks Hooks) *agent {
	t.Helper()
	root := t.TempDir()
	return newAgent(Options{Root: root, Runner: sys.runner(), Hooks: hooks, StartCheckDelay: time.Millisecond}.withDefaults(),
		config.NewNode(testNode, "5.6.7.8:44433", "sha256:"+strings.Repeat("a", 64)), root+config.DefaultPath, dlog.Discard())
}

func TestPostStartRerunIsSerialisedAndRechecked(t *testing.T) {
	sys := newFakeSys()
	hooks := &recHooks{}
	a := newTestAgent(t, sys, hooks)
	ctx := context.Background()
	inst := "main.de-1.awg-userspace"
	unit := systemd.UnitName(inst)
	dir := "/etc/deyroute/backends/awg/main/de-1/userspace"
	a.st.Instances[inst] = instance{Tunnel: "main", Backend: "awg", ConfigDir: dir}
	a.started[inst] = true
	a.pids[inst] = 10
	sys.setState(unit, "active")
	sys.mu.Lock()
	sys.pids[unit] = 11
	sys.mu.Unlock()

	// The monitor snapshot says 11, but a unit.restart already configured
	// that process: nothing to do.
	a.pids[inst] = 11
	a.watchPostStart(ctx, map[string]unitProps{unit: {ActiveState: "active", MainPID: 10}})
	require.Empty(t, hooks.list())

	// A really restarted process is configured once, and a stop waits for
	// the post-start step instead of racing it.
	a.pids[inst] = 10
	hooks.entered, hooks.gate = make(chan struct{}), make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		a.watchPostStart(ctx, map[string]unitProps{unit: {ActiveState: "active", MainPID: 11}})
	}()
	<-hooks.entered
	stopped := make(chan error, 1)
	go func() {
		_, err := a.unitStop(ctx, api.UnitArgs{Instance: inst})
		stopped <- err
	}()
	select {
	case <-stopped:
		t.Fatal("unit.stop ran while the post-start step was configuring the device")
	case <-time.After(50 * time.Millisecond):
	}
	close(hooks.gate)
	<-watched
	require.NoError(t, <-stopped)
	require.Equal(t, []string{"post awg " + dir, "stop awg " + dir}, hooks.list())

	// A stopped instance is never configured again.
	hooks.gate = nil
	a.watchPostStart(ctx, map[string]unitProps{unit: {ActiveState: "active", MainPID: 12}})
	require.Len(t, hooks.list(), 2)
}

func TestUnknownInstanceStillRunsBackendHooks(t *testing.T) {
	// The state file was lost: the awg unit's backend and directory follow
	// from its name, so its device is still set up and cleaned up.
	sys := newFakeSys()
	hooks := &recHooks{}
	a := newTestAgent(t, sys, hooks)
	ctx := context.Background()
	inst := "main.de-1.awg-userspace"
	dir := "/etc/deyroute/backends/awg/main/de-1/userspace"
	st, err := a.unitStart(ctx, api.UnitArgs{Instance: inst}, false)
	require.NoError(t, err)
	require.Equal(t, "active", st.ActiveState)
	require.Equal(t, []string{"pre awg " + dir, "post awg " + dir}, hooks.list())
	_, err = a.unitStop(ctx, api.UnitArgs{Instance: inst})
	require.NoError(t, err)
	require.Equal(t, "stop awg "+dir, hooks.list()[2])

	// The canary's backend is not in its name: no hook without a record.
	_, err = a.unitStart(ctx, api.UnitArgs{Instance: "main.canary"}, false)
	require.NoError(t, err)
	require.Len(t, hooks.list(), 3)

	// After an agent restart, a running unknown instance is watched too.
	b := newTestAgent(t, sys, hooks)
	sys.setState(systemd.UnitName(inst), "active")
	b.reconcile(ctx)
	require.True(t, b.started[inst])
}

func TestCompatibleHelloClearsIncompatibility(t *testing.T) {
	a := newTestAgent(t, newFakeSys(), &recHooks{})
	a.onHubHello(api.Hello{Version: "9.0.0", Compatible: false})
	require.Contains(t, a.heartbeat().LastError, string(deyerr.N004))
	a.onHubHello(api.Hello{Version: version.Version, Compatible: true})
	require.Empty(t, a.heartbeat().LastError)

	// Another error is kept.
	a.setLastError(deyerr.New(deyerr.B003, deyerr.Params{"unit": "u"}))
	a.onHubHello(api.Hello{Version: version.Version, Compatible: true})
	require.Contains(t, a.heartbeat().LastError, string(deyerr.B003))
}

func TestLateJobAfterShutdownIsDropped(t *testing.T) {
	a := newTestAgent(t, newFakeSys(), &recHooks{})
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.jobsMu.Lock()
	a.jobsDone = true
	a.jobsMu.Unlock()
	ran := make(chan struct{}, 1)
	a.later(0, func(context.Context) error {
		ran <- struct{}{}
		return nil
	})
	a.jobs.Wait()
	cancel()
	select {
	case <-ran:
		t.Fatal("a job asked for after the shutdown ran")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestHandleErrorMapping(t *testing.T) {
	root := t.TempDir()
	sys := newFakeSys()
	a := newAgent(Options{Root: root, Runner: sys.runner(), Hooks: &recHooks{}, StartCheckDelay: time.Hour}.withDefaults(),
		config.NewNode(testNode, "5.6.7.8:44433", "sha256:"+strings.Repeat("a", 64)), root+config.DefaultPath, dlog.Discard())
	args, err := json.Marshal(api.UnitArgs{Instance: "main.de-1.backhaul-wssmux"})
	require.NoError(t, err)

	// A command cut short by the hub returns the context error, which the
	// control client reports as DEY-N014/N005.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = a.handle(ctx, api.Command{ID: "1", Name: api.CmdUnitStart, Args: args}, nil)
	require.ErrorIs(t, err, context.Canceled)

	// A DEY error keeps its code; its cause travels as Detail.
	_, err = a.handle(context.Background(), api.Command{ID: "2", Name: api.CmdUnitStatus, Args: []byte(`{"instance":"bad/x"}`)}, nil)
	requireCode(t, err, deyerr.N050)
	_, err = a.handle(context.Background(), api.Command{ID: "3", Name: api.CmdUnitStatus, Args: []byte(`[1]`)}, nil)
	de := requireCode(t, err, deyerr.N015)
	require.NotEmpty(t, de.Detail)
	require.Contains(t, a.heartbeat().LastError, "DEY-N015")
}

func TestIPForwardFollowsStartedInstances(t *testing.T) {
	root := t.TempDir()
	sys := newFakeSys()
	a := newAgent(Options{Root: root, Runner: sys.runner(), Hooks: &recHooks{}}.withDefaults(),
		config.NewNode(testNode, "5.6.7.8:44433", "sha256:"+strings.Repeat("a", 64)), root+config.DefaultPath, dlog.Discard())
	fwd := root + "/proc/sys/net/ipv4/ip_forward"
	writeFile(t, fwd, "0\n", 0o644)
	a.st.Instances["t1.de-1.x-y"] = instance{Tunnel: "t1", Backend: "x", IPForward: true}
	a.started["t1.de-1.x-y"] = true
	require.NoError(t, a.syncFirewall(context.Background(), false))
	require.Equal(t, "1", readTrim(fwd))
	require.Equal(t, 1, sys.deletes(), "no NAT rules: the table is removed")
	require.NoError(t, a.syncFirewall(context.Background(), false))
	require.Equal(t, 1, sys.deletes(), "unchanged rules are not applied again")
	require.NoError(t, os.Remove(fwd))
	require.NoError(t, os.RemoveAll(root+"/proc"))
	writeFile(t, root+"/proc/sys/net/ipv4/ip_forward/x", "", 0o644) // a directory: not writable as a file
	requireCode(t, a.syncFirewall(context.Background(), true), deyerr.X033)
}

func TestCanaryRender(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	dir := "/etc/deyroute/backends/backhaul/main/canary"
	args := api.BackendRenderArgs{Instance: systemd.CanaryInstance("main"), Tunnel: "main", ConfigDir: dir,
		Files: map[string][]byte{"client.toml": []byte("x")},
		Unit:  backend.UnitSpec{ExecStart: []string{backhaulBin, "-c", dir + "/client.toml"}, WorkingDirectory: dir}}
	_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)
	_, err = os.Stat(e.path(dir + "/client.toml"))
	require.NoError(t, err)
	bad := args
	bad.ConfigDir = "/etc/deyroute/backends/backhaul/main/de-1/wssmux"
	_, err = call[json.RawMessage](t, s, api.CmdBackendRender, bad)
	requireCode(t, err, deyerr.N050)
	bad.ConfigDir = "/etc/deyroute/backends/BAD/main/canary"
	_, err = call[json.RawMessage](t, s, api.CmdBackendRender, bad)
	requireCode(t, err, deyerr.N050)
	_, err = call[json.RawMessage](t, s, api.CmdBackendRemove, api.BackendRemoveArgs{Instance: args.Instance, ConfigDir: dir})
	require.NoError(t, err)
	_, err = os.Stat(e.path(dir))
	require.True(t, os.IsNotExist(err))
}

func TestDoctorReportsSecretPermissions(t *testing.T) {
	e := newEnv(t)
	writeFile(t, e.path(config.SecretsDir+"/stray.key"), "x", 0o644)
	s := e.start()
	dd, err := call[api.DoctorData](t, s, api.CmdDoctorCollect, nil)
	require.NoError(t, err)
	found := false
	for _, f := range dd.Findings {
		if strings.Contains(f.Message, "stray.key") {
			found = true
		}
	}
	require.True(t, found, "R15 names the file with wrong permissions: %+v", dd.Findings)
}
