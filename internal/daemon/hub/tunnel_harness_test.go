package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// ---------------------------------------------------------------- backends

// Test transports (registered once for the package):
//
//	tfa/alpha  reverse, tcp+udp
//	tfa/beta   reverse, tcp+udp
//	tfa/gamma  forward, tcp+udp, needs UDP
//	tfa/delta  reverse, tcp; Validate always refuses (DEY-B006)
//	tfa/nat    forward, tcp; the hub side forwards with NAT (WireGuard-like)
//	tfdl/one   reverse, tcp; not built in and without sha256 (install fails)
//	tfdn/one   reverse, tcp; downloaded (raw binary, pinned sha256)
const (
	trAlpha = "tfa/alpha"
	trBeta  = "tfa/beta"
	trGamma = "tfa/gamma"
	trDelta = "tfa/delta"
	trNAT   = "tfa/nat"
	trDL    = "tfdl/one"
	trDN    = "tfdn/one"
)

// tfdnBinary is the "release" of the tfdn test backend.
var tfdnBinary = []byte("#!tfdn test backend binary\n")

func init() {
	backend.Register(&testBackend{name: "tfa", version: "1.0", builtin: true, transports: []backend.Transport{
		{Backend: "tfa", Name: "alpha", Direction: backend.Reverse, Protos: []string{"tcp", "udp"}, Stealth: 4},
		{Backend: "tfa", Name: "beta", Direction: backend.Reverse, Protos: []string{"tcp", "udp"}, Stealth: 2},
		{Backend: "tfa", Name: "gamma", Direction: backend.Forward, Protos: []string{"tcp", "udp"}, NeedsUDP: true, Stealth: 3},
		{Backend: "tfa", Name: "delta", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
		{Backend: "tfa", Name: "nat", Direction: backend.Forward, Protos: []string{"tcp"}, Stealth: 1},
	}})
	backend.Register(&testBackend{name: "tfdl", version: "9.9.9", transports: []backend.Transport{
		{Backend: "tfdl", Name: "one", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
	}})
	sum := sha256.Sum256(tfdnBinary)
	backend.Register(&testBackend{name: "tfdn", version: "1.0", sha: hex.EncodeToString(sum[:]), transports: []backend.Transport{
		{Backend: "tfdn", Name: "one", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
	}})
}

// testBackend renders a small config file per side; the hub side lists the
// tunnel ports (so a port change changes it), the node side the targets.
type testBackend struct {
	name       string
	version    string
	builtin    bool
	sha        string // amd64 sha256 of a raw download ("" = none: the install fails)
	transports []backend.Transport
}

func (b *testBackend) Name() string                    { return b.name }
func (b *testBackend) Transports() []backend.Transport { return b.transports }
func (b *testBackend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

func (b *testBackend) Manifest() backend.ManifestEntry {
	e := backend.ManifestEntry{Name: b.name, Version: b.version, Builtin: b.builtin, TemplateVersion: 1}
	if !b.builtin {
		e.URLs = map[string]string{"amd64": "https://example.invalid/" + b.name + ".tar.gz"}
		e.Archive = "tar.gz"
		e.Binaries = []string{b.name}
		if b.sha != "" {
			e.URLs["amd64"] = "https://example.invalid/" + b.name
			e.Archive = "raw"
			e.SHA256 = map[string]string{"amd64": b.sha}
		}
	}
	return e
}

func (b *testBackend) Validate(in backend.RenderInput) error {
	if in.Transport.Name == "delta" {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": in.Transport.ID(), "reason": "test transport always refused"})
	}
	if in.Secrets.Token == "" || in.ControlPort == 0 {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": in.Transport.ID(), "reason": "token or control port missing"})
	}
	return nil
}

func (b *testBackend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "side=%s\ntransport=%s\ncontrol=%d\ntoken=%s\ncanary=%t\n", side, in.Transport.ID(), in.ControlPort, in.Secrets.Token, in.Canary)
	var binds []backend.PortUse
	for _, p := range in.Tunnel.Ports {
		if side == backend.SideHub {
			fmt.Fprintf(&sb, "listen=%s:%d/%s\n", in.ListenAddrOrDefault(), p.Listen, p.Proto)
			binds = append(binds, backend.PortUse{Port: p.Listen, Proto: p.Proto, Addr: in.ListenAddrOrDefault(), Purpose: "user"})
		} else {
			fmt.Fprintf(&sb, "target=%d/%s->%s\n", p.Listen, p.Proto, p.Target)
		}
	}
	bin := in.Paths.SelfBinary
	args := []string{bin, "relay", "--config", filepath.Join(in.Paths.ConfigDir, "test.conf")}
	if in.Paths.Binary != "" {
		args = []string{in.Paths.Binary, "-c", filepath.Join(in.Paths.ConfigDir, "test.conf")}
	}
	r := backend.Rendered{
		Files: map[string][]byte{"test.conf": []byte(sb.String())},
		Unit:  backend.UnitSpec{ExecStart: args},
		Binds: binds,
	}
	if in.Transport.Name == "nat" && side == backend.SideHub {
		for _, p := range in.Tunnel.Ports {
			r.NAT = append(r.NAT, backend.NATRule{Proto: p.Proto, DportLow: p.Listen, DportHigh: p.Listen, ToAddr: "10.77.1.2", ToPort: p.Listen})
		}
		r.Masquerade = []string{"dey-" + in.Tunnel.ID}
		r.IPForward = true
	}
	return r, nil
}

// ---------------------------------------------------------------- fake systemd

// fakeSystemd answers the systemctl commands of deyroute-tun@ units like a
// tiny systemd: start/restart makes a unit active and — for hub units —
// opens its tunnel's TCP listen ports on 127.0.0.1 (answering like a TLS
// less service) or, for a canary, the loopback port with a TCP echo; stop
// closes them. Transports in broken start but open nothing (their path
// probe fails); transports in crash fail to start (DEY-B003).
type fakeSystemd struct {
	t   *testing.T
	env *testEnv

	mu        sync.Mutex
	units     map[string]string // unit → ActiveState
	listeners map[string][]net.Listener
	broken    map[string]bool
	crash     map[string]bool
	calls     []string // "start <unit>", "stop <unit>", "restart <unit>"
	bindErrs  []string
	wg        sync.WaitGroup
	// brokenIf reports hub units that start but open nothing (set before
	// the units start).
	brokenIf func(inst string) bool
	// canaryTarget, when set, makes a started canary unit proxy its
	// loopback port to the returned address (diag speed) instead of echoing.
	canaryTarget func(tunnel string) string
}

func newFakeSystemd(t *testing.T, env *testEnv) *fakeSystemd {
	fs := &fakeSystemd{t: t, env: env, units: map[string]string{}, listeners: map[string][]net.Listener{},
		broken: map[string]bool{}, crash: map[string]bool{}}
	env.runner.Handler = fs.handle
	t.Cleanup(fs.close)
	return fs
}

// setUnit sets a unit state directly (a unit that runs before the hub).
func (fs *fakeSystemd) setUnit(unit, st string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.units[unit] = st
}

func (fs *fakeSystemd) handle(c exec.Call) (exec.Response, bool) {
	if c.Name != "systemctl" || len(c.Args) == 0 {
		return exec.Response{}, false
	}
	verb := c.Args[0]
	unit := ""
	if len(c.Args) > 1 {
		unit = c.Args[1]
	}
	switch verb {
	case "start", "restart":
		inst, ok := systemd.InstanceOf(unit)
		if !ok {
			return exec.Response{}, false
		}
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.calls = append(fs.calls, verb+" "+unit)
		fs.env.recordOrder("hub " + verb + " " + inst)
		fs.closeLocked(unit)
		if fs.crash[transportOf(inst)] {
			fs.units[unit] = "failed"
			return exec.Response{}, true
		}
		fs.units[unit] = "active"
		fs.openLocked(unit, inst)
		return exec.Response{}, true
	case "stop":
		if _, ok := systemd.InstanceOf(unit); !ok {
			return exec.Response{}, false
		}
		fs.mu.Lock()
		defer fs.mu.Unlock()
		fs.calls = append(fs.calls, "stop "+unit)
		fs.closeLocked(unit)
		if _, known := fs.units[unit]; !known {
			return exec.Fail(5, "Failed to stop "+unit+": Unit "+unit+" not loaded."), true
		}
		fs.units[unit] = "inactive"
		return exec.Response{}, true
	case "show":
		if _, ok := systemd.InstanceOf(unit); !ok {
			return exec.Response{}, false
		}
		fs.mu.Lock()
		st := fs.units[unit]
		fs.mu.Unlock()
		sub := map[string]string{"active": "running", "failed": "failed", "inactive": "dead", "": "dead"}[st]
		if st == "" {
			st = "inactive"
		}
		return exec.OK(fmt.Sprintf("ActiveState=%s\nSubState=%s\nMainPID=0\nNRestarts=0\nResult=success\n", st, sub)), true
	case "list-units":
		fs.mu.Lock()
		defer fs.mu.Unlock()
		var lines []string
		for u, st := range fs.units {
			if st == "" {
				continue
			}
			sub := map[string]string{"active": "running", "failed": "failed", "inactive": "dead"}[st]
			lines = append(lines, u+" loaded "+st+" "+sub+" deyroute tunnel")
		}
		sort.Strings(lines)
		return exec.OK(strings.Join(lines, "\n") + "\n"), true
	}
	return exec.Response{}, false
}

// transportOf returns the transport id of a warm instance ("" for others).
func transportOf(inst string) string {
	in, err := systemd.ParseInstance(inst)
	if err != nil {
		return ""
	}
	return in.Transport
}

// openLocked opens what a started hub unit listens on.
func (fs *fakeSystemd) openLocked(unit, inst string) {
	in, err := systemd.ParseInstance(inst)
	if err != nil {
		return
	}
	h := fs.env.h
	if in.Canary {
		ports, _ := h.st.CtlPorts()
		loop := ports[render.CanaryLoopbackKey(in.Tunnel)]
		if loop == 0 {
			return
		}
		if fs.canaryTarget != nil {
			fs.proxyLocked(unit, loop, fs.canaryTarget(in.Tunnel))
			return
		}
		fs.listenLocked(unit, loop, true)
		return
	}
	if fs.broken[in.Transport] || (fs.brokenIf != nil && fs.brokenIf(inst)) {
		return
	}
	t, ok := h.Config().Tunnel(in.Tunnel)
	if !ok {
		return
	}
	for _, pm := range t.Ports {
		if pm.Proto == config.ProtoTCP {
			fs.listenLocked(unit, pm.Listen, false)
		}
	}
}

func (fs *fakeSystemd) listenLocked(unit string, port int, echo bool) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fs.bindErrs = append(fs.bindErrs, unit+": "+err.Error())
		fs.units[unit] = "failed"
		return
	}
	fs.listeners[unit] = append(fs.listeners[unit], ln)
	fs.wg.Add(1)
	go func() {
		defer fs.wg.Done()
		if echo {
			_ = health.ServeTCPEcho(context.Background(), ln)
			return
		}
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")
			_ = conn.Close()
		}
	}()
}

// proxyLocked listens on the loopback port and forwards every connection
// to target (a tunnel as seen from the hub).
func (fs *fakeSystemd) proxyLocked(unit string, port int, target string) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		fs.bindErrs = append(fs.bindErrs, unit+": "+err.Error())
		fs.units[unit] = "failed"
		return
	}
	fs.listeners[unit] = append(fs.listeners[unit], ln)
	fs.wg.Add(1)
	go func() {
		defer fs.wg.Done()
		var conns sync.WaitGroup
		defer conns.Wait()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			go func() {
				defer conns.Done()
				defer func() { _ = conn.Close() }()
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = up.Close() }()
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(up, conn)
					if tc, ok := up.(*net.TCPConn); ok {
						_ = tc.CloseWrite()
					}
					close(done)
				}()
				_, _ = io.Copy(conn, up)
				if tc, ok := conn.(*net.TCPConn); ok {
					_ = tc.CloseWrite()
				}
				<-done
			}()
		}
	}()
}

func (fs *fakeSystemd) closeLocked(unit string) {
	for _, ln := range fs.listeners[unit] {
		_ = ln.Close()
	}
	delete(fs.listeners, unit)
}

func (fs *fakeSystemd) close() {
	fs.mu.Lock()
	for u := range fs.listeners {
		fs.closeLocked(u)
	}
	fs.mu.Unlock()
	fs.wg.Wait()
}

// state returns the ActiveState of a unit.
func (fs *fakeSystemd) state(unit string) string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.units[unit]
}

// count returns how often "verb unit" was run.
func (fs *fakeSystemd) count(verb, unit string) int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	n := 0
	for _, c := range fs.calls {
		if c == verb+" "+unit {
			n++
		}
	}
	return n
}

// active lists the active units.
func (fs *fakeSystemd) active() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var out []string
	for u, st := range fs.units {
		if st == "active" {
			out = append(out, u)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- tunnel env

// tunnelEnv is a running hub with the fake systemd.
type tunnelEnv struct {
	*testEnv
	o  Options
	sd *fakeSystemd
}

// startTunnelHub runs a hub whose systemctl is the fake systemd.
func startTunnelHub(t *testing.T, opts ...envOption) *tunnelEnv {
	env, o := prepareEnv(t, nil, opts...)
	resetOrder := func() {
		orderMu.Lock()
		delete(orderLog, t.Name())
		orderMu.Unlock()
	}
	resetOrder()
	t.Cleanup(resetOrder)
	te := &tunnelEnv{testEnv: env, o: o}
	te.sd = newFakeSystemd(t, env)
	env.startEnv(o)
	return te
}

// recordOrder appends one line to the start/stop order log.
func (env *testEnv) recordOrder(line string) {
	orderMu.Lock()
	defer orderMu.Unlock()
	orderLog[env.t.Name()] = append(orderLog[env.t.Name()], line)
}

var (
	orderMu  sync.Mutex
	orderLog = map[string][]string{}
)

// order returns the start/stop order log of the test.
func (env *testEnv) order() []string {
	orderMu.Lock()
	defer orderMu.Unlock()
	return append([]string(nil), orderLog[env.t.Name()]...)
}

// freePort returns a TCP port that is free on 127.0.0.1 and 0.0.0.0 and not
// reserved for tunnels.
func freePort(t *testing.T) int {
	t.Helper()
	for range 100 {
		ln, err := net.Listen("tcp", "0.0.0.0:0")
		require.NoError(t, err)
		p := ln.Addr().(*net.TCPAddr).Port
		_ = ln.Close()
		if r, _ := config.ReservedListen(p, config.DefaultControlPort, nil); !r {
			return p
		}
	}
	t.Fatal("no free port")
	return 0
}

// tnode is a fake node that answers every tunnel command.
type tnode struct {
	*fakeNode
	env *tunnelEnv

	tmu        sync.Mutex
	rendered   map[string]api.BackendRenderArgs
	removed    []string
	started    []string
	restarted  []string
	stopped    []string
	udpBlocked bool
	echoes     map[int]net.Listener
	udp        map[int]net.PacketConn
	wg         sync.WaitGroup
	stopEcho   context.CancelFunc
	echoCtx    context.Context
}

// tunnelNode joins node id, installs the tunnel command handlers and
// connects it.
func (te *tunnelEnv) tunnelNode(id string) *tnode {
	fn := te.joinNode(id)
	return te.wireNode(fn)
}

func (te *tunnelEnv) wireNode(fn *fakeNode) *tnode {
	ctx, cancel := context.WithCancel(context.Background())
	n := &tnode{fakeNode: fn, env: te, rendered: map[string]api.BackendRenderArgs{},
		echoes: map[int]net.Listener{}, udp: map[int]net.PacketConn{}, echoCtx: ctx, stopEcho: cancel}
	t := te.t
	t.Cleanup(n.cleanup)
	fn.on(api.CmdBackendInstall, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.BackendInstallResult{}, nil
	})
	fn.on(api.CmdBackendRender, func(_ context.Context, f *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.BackendRenderArgs
		decode(t, cmd, &args)
		n.tmu.Lock()
		n.rendered[args.Instance] = args
		n.tmu.Unlock()
		f.mu.Lock()
		if _, ok := f.units[systemd.UnitName(args.Instance)]; !ok {
			f.units[systemd.UnitName(args.Instance)] = "inactive"
		}
		f.mu.Unlock()
		return nil, nil
	})
	fn.on(api.CmdBackendRemove, func(_ context.Context, f *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.BackendRemoveArgs
		decode(t, cmd, &args)
		n.tmu.Lock()
		delete(n.rendered, args.Instance)
		n.removed = append(n.removed, args.Instance)
		n.tmu.Unlock()
		f.mu.Lock()
		delete(f.units, systemd.UnitName(args.Instance))
		f.mu.Unlock()
		return nil, nil
	})
	unitCmd := func(verb string) cmdFunc {
		return func(_ context.Context, f *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
			var args api.UnitArgs
			decode(t, cmd, &args)
			unit := systemd.UnitName(args.Instance)
			n.tmu.Lock()
			switch verb {
			case "start":
				n.started = append(n.started, args.Instance)
			case "restart":
				n.restarted = append(n.restarted, args.Instance)
			case "stop":
				n.stopped = append(n.stopped, args.Instance)
			}
			n.tmu.Unlock()
			if verb != "status" {
				te.recordOrder("node " + verb + " " + args.Instance)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			switch verb {
			case "start", "restart":
				f.units[unit] = "active"
			case "stop":
				if _, ok := f.units[unit]; ok {
					f.units[unit] = "inactive"
				}
			}
			return api.UnitStatus{Unit: unit, ActiveState: f.units[unit]}, nil
		}
	}
	fn.on(api.CmdUnitStart, unitCmd("start"))
	fn.on(api.CmdUnitRestart, unitCmd("restart"))
	fn.on(api.CmdUnitStop, unitCmd("stop"))
	fn.on(api.CmdUnitStatus, unitCmd("status"))
	fn.on(api.CmdProbeTCP, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.ProbeResultDTO{OK: true, RTTms: 1}, nil
	})
	fn.on(api.CmdProbeTLS, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.ProbeResultDTO{OK: true, RTTms: 1}, nil
	})
	fn.on(api.CmdPortCheckRemote, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.ProbeResultDTO{OK: true, RTTms: 7}, nil
	})
	fn.on(api.CmdSysinfo, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return map[string]string{"os": "Test OS"}, nil
	})
	fn.on(api.CmdProbeUDPListen, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.UDPListenArgs
		decode(t, cmd, &args)
		n.tmu.Lock()
		defer n.tmu.Unlock()
		if n.udpBlocked || n.udp[args.Port] != nil {
			return nil, nil
		}
		conn, err := net.ListenPacket("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(args.Port)))
		if err != nil {
			return nil, deyerr.Wrap(deyerr.P012, err, deyerr.Params{"port": args.Port, "process": "x", "addr": "127.0.0.1"})
		}
		n.udp[args.Port] = conn
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			_ = health.ServeUDPEcho(n.echoCtx, conn)
		}()
		return nil, nil
	})
	fn.on(api.CmdEchoStart, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.EchoArgs
		decode(t, cmd, &args)
		n.tmu.Lock()
		defer n.tmu.Unlock()
		if _, ok := n.echoes[args.Port]; ok && args.Port != 0 {
			return api.EchoResult(args), nil
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(args.Port)))
		if err != nil {
			return nil, deyerr.Wrap(deyerr.P012, err, deyerr.Params{"port": args.Port, "process": "x", "addr": "127.0.0.1"})
		}
		port := ln.Addr().(*net.TCPAddr).Port
		n.echoes[port] = ln
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			_ = health.ServeTCPEcho(n.echoCtx, ln)
		}()
		return api.EchoResult{Port: port}, nil
	})
	fn.on(api.CmdEchoStop, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.EchoArgs
		decode(t, cmd, &args)
		n.tmu.Lock()
		defer n.tmu.Unlock()
		if ln, ok := n.echoes[args.Port]; ok {
			_ = ln.Close()
			delete(n.echoes, args.Port)
		}
		return nil, nil
	})
	fn.start()
	te.waitOnline(fn.id, true)
	return n
}

func (n *tnode) cleanup() {
	n.stop()
	n.stopEcho()
	n.tmu.Lock()
	for _, ln := range n.echoes {
		_ = ln.Close()
	}
	for _, c := range n.udp {
		_ = c.Close()
	}
	n.tmu.Unlock()
	n.wg.Wait()
}

// renderedFor lists the instances rendered on the node for tunnel.
func (n *tnode) renderedFor(tunnel string) []string {
	n.tmu.Lock()
	defer n.tmu.Unlock()
	var out []string
	for inst := range n.rendered {
		if strings.HasPrefix(inst, tunnel+".") {
			out = append(out, inst)
		}
	}
	sort.Strings(out)
	return out
}

// startedCount counts unit.start calls for instance.
func (n *tnode) startedCount(inst string) int {
	n.tmu.Lock()
	defer n.tmu.Unlock()
	c := 0
	for _, s := range n.started {
		if s == inst {
			c++
		}
	}
	return c
}

// restartedList returns the unit.restart calls.
func (n *tnode) restartedList() []string {
	n.tmu.Lock()
	defer n.tmu.Unlock()
	return append([]string(nil), n.restarted...)
}

// removedList returns the backend.remove calls.
func (n *tnode) removedList() []string {
	n.tmu.Lock()
	defer n.tmu.Unlock()
	return append([]string(nil), n.removed...)
}

// fastFailover is the failover block of test tunnels (1 s probes).
func fastFailover(failback bool) *api.FailoverSettings {
	return &api.FailoverSettings{
		ProbeIntervalS: 1, ProbeTimeoutS: 1, FailThreshold: 2, RecoverThreshold: 2,
		Failback: failback, FailbackAfterS: 3600, MaxSwitchesPerHour: 20, QuarantineS: 600,
	}
}

// stepLog collects progress steps.
type stepLog struct {
	mu    sync.Mutex
	steps []api.Step
}

func (s *stepLog) add(st api.Step) {
	s.mu.Lock()
	s.steps = append(s.steps, st)
	s.mu.Unlock()
}

// finished returns the non-running steps as "id:status".
func (s *stepLog) finished() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, st := range s.steps {
		if st.Status != api.StepRunning {
			out = append(out, st.ID+":"+st.Status)
		}
	}
	return out
}

// last returns the last step.
func (s *stepLog) last() api.Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		return api.Step{}
	}
	return s.steps[len(s.steps)-1]
}

// addTunnelUp adds a tunnel over the socket and requires it UP.
func (te *tunnelEnv) addTunnelUp(req api.TunnelAddRequest) (api.TunnelInfo, *stepLog) {
	te.t.Helper()
	var log stepLog
	info, err := te.client.TunnelAdd(ctxT(te.t), req, log.add)
	require.NoError(te.t, err, "steps: %v", log.finished())
	require.Equal(te.t, state.StateUp, info.State)
	return info, &log
}

// waitActive waits until tunnel id is UP on transport.
func (te *tunnelEnv) waitActive(id, node, transport string) state.TunnelState {
	te.t.Helper()
	var ts state.TunnelState
	require.Eventually(te.t, func() bool {
		ts, _ = te.h.tunnelState(id)
		return ts.State == state.StateUp && ts.Active.Node == node && ts.Active.Transport == transport
	}, testWait, 20*time.Millisecond, "tunnel %s not UP on %s/%s", id, node, transport)
	return ts
}

// hubUnit is the unit of a warm instance.
func hubUnit(tunnel, node, transport string) string {
	return systemd.UnitName(systemd.InstanceName(tunnel, node, transport))
}

// dirExists reports whether path exists below root.
func dirExists(root, path string) bool {
	_, err := os.Stat(filepath.Join(root, path))
	return err == nil
}

// rawFetcher serves the tfdn test release (and fails for anything else).
type rawFetcher struct{}

// Fetch implements install.Fetcher.
func (rawFetcher) Fetch(_ context.Context, url string, w io.Writer) error {
	if !strings.HasSuffix(url, "/tfdn") {
		return deyerr.New(deyerr.I004, deyerr.Params{"file": url})
	}
	_, err := w.Write(tfdnBinary)
	return err
}
