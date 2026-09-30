package node

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// Probe helper limits.
const (
	// MaxProbeTimeout caps the timeout a probe command may ask for.
	MaxProbeTimeout = 60 * time.Second
	// DefaultUDPListenSeconds is the UDP echo lifetime when the hub gives none.
	DefaultUDPListenSeconds = 30
	// MaxUDPListenSeconds caps the UDP echo lifetime.
	MaxUDPListenSeconds = 600
	// speedGrace is added to 2 × seconds (download + upload) for the
	// generator's lifetime (connects, pings).
	speedGrace = 30 * time.Second
	// loopbackHost is where the canary echo and the speed generator listen:
	// only a tunnel (arriving at 127.0.0.1) can reach them.
	loopbackHost = "127.0.0.1"
)

// registry owns the long-running probe helpers (UDP echo, canary TCP echo,
// speed generator): each runs until it is stopped, its lifetime ends or the
// agent stops.
type registry struct {
	mu      sync.Mutex
	entries map[string]*helper
	closed  bool
	wg      sync.WaitGroup
}

type helper struct {
	ctx      context.Context
	cancel   context.CancelFunc
	timer    *time.Timer // nil: runs until stopped
	deadline time.Time   // guarded by registry.mu
	done     chan struct{}
}

func newRegistry() *registry { return &registry{entries: map[string]*helper{}} }

// lookup returns the live helper of key. A helper that is shutting down is
// waited for (its socket is closed afterwards) and reported as absent.
func (r *registry) lookup(key string, life time.Duration) bool {
	r.mu.Lock()
	h, ok := r.entries[key]
	if ok && h.ctx.Err() == nil {
		if h.timer != nil && life > 0 {
			if d := time.Now().Add(life); d.After(h.deadline) {
				h.deadline = d
			}
		}
		r.mu.Unlock()
		return true
	}
	r.mu.Unlock()
	if ok {
		<-h.done
	}
	return false
}

// extend pushes the end of a running helper to at least now+life; false
// when key is not running.
func (r *registry) extend(key string, life time.Duration) bool { return r.lookup(key, life) }

// running reports whether key is running.
func (r *registry) running(key string) bool { return r.lookup(key, 0) }

// expire is the lifetime timer of h: it cancels h once its (possibly
// extended) deadline has passed, else re-arms itself.
func (r *registry) expire(h *helper) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h.ctx.Err() != nil {
		return
	}
	if left := time.Until(h.deadline); left > 0 {
		h.timer.Reset(left)
		return
	}
	h.cancel()
}

// start runs serve in the background under key; life > 0 stops it after
// that long. serve must return when its ctx ends. onExit receives serve's
// error. false when the registry is closed.
func (r *registry) start(parent context.Context, key string, life time.Duration, serve func(ctx context.Context) error, onExit func(error)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	ctx, cancel := context.WithCancel(parent)
	h := &helper{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if life > 0 {
		h.deadline = time.Now().Add(life)
		h.timer = time.AfterFunc(life, func() { r.expire(h) })
	}
	r.entries[key] = h
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(h.done)
		err := serve(ctx)
		cancel()
		if h.timer != nil {
			h.timer.Stop()
		}
		r.mu.Lock()
		if r.entries[key] == h {
			delete(r.entries, key)
		}
		r.mu.Unlock()
		if onExit != nil {
			onExit(err)
		}
	}()
	return true
}

// stop ends key and waits for it; false when it was not running.
func (r *registry) stop(key string) bool {
	r.mu.Lock()
	h, ok := r.entries[key]
	r.mu.Unlock()
	if !ok {
		return false
	}
	h.cancel()
	<-h.done
	return true
}

// stopAll ends every helper, refuses new ones and waits.
func (r *registry) stopAll() {
	r.mu.Lock()
	r.closed = true
	for _, h := range r.entries {
		h.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// probeTimeout converts a command timeout (ms) to a duration: default
// health.DefaultTimeout, at most MaxProbeTimeout.
func probeTimeout(ms int) time.Duration {
	if ms <= 0 {
		return health.DefaultTimeout
	}
	return min(time.Duration(ms)*time.Millisecond, MaxProbeTimeout)
}

func dto(r health.Result) api.ProbeResultDTO {
	return api.ProbeResultDTO{OK: r.OK, RTTms: int(r.RTT.Milliseconds()), Error: r.Err}
}

// validTarget reports whether s is host:port with a numeric port 1-65535.
func validTarget(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 1 && p <= 65535
}

// probe runs probe.tcp, probe.tls or probe.http against args.Target
// (host:port; probe.http also accepts an http:// or https:// URL). It only
// connects: nothing is relayed.
func (a *agent) probe(ctx context.Context, command string, args api.ProbeArgs) (api.ProbeResultDTO, error) {
	timeout := probeTimeout(args.TimeoutMs)
	if command == api.CmdProbeHTTP && (strings.HasPrefix(args.Target, "http://") || strings.HasPrefix(args.Target, "https://")) {
		return dto(health.HTTP(ctx, args.Target, timeout)), nil
	}
	if !validTarget(args.Target) {
		return api.ProbeResultDTO{}, a.refuse(command, fmt.Sprintf("target %q is not host:port", args.Target))
	}
	switch command {
	case api.CmdProbeTCP:
		return dto(health.TCP(ctx, args.Target, timeout)), nil
	case api.CmdProbeTLS:
		return dto(health.TLS(ctx, args.Target, args.SNI, timeout)), nil
	default:
		return dto(health.Path(ctx, args.Target, health.KindHTTP, timeout, health.PathOptions{SNI: args.SNI, HTTPPath: args.Path})), nil
	}
}

// portCheck answers port.check_from_outside: a TCP connect from this node to
// the hub's public ip:port (section 10 step 3). UDP cannot be checked this
// way (there is no generic UDP answer); the result says so.
func (a *agent) portCheck(ctx context.Context, args api.PortCheckArgs) (api.ProbeResultDTO, error) {
	const cmd = api.CmdPortCheckRemote
	ip, err := netip.ParseAddr(args.IP)
	if err != nil || ip.Zone() != "" {
		return api.ProbeResultDTO{}, a.refuse(cmd, fmt.Sprintf("%q is not an IP address", args.IP))
	}
	if args.Port < 1 || args.Port > 65535 {
		return api.ProbeResultDTO{}, a.refuse(cmd, fmt.Sprintf("port %d is out of range", args.Port))
	}
	switch strings.ToLower(args.Proto) {
	case "", "tcp":
	case "udp":
		return api.ProbeResultDTO{OK: false, Error: "not supported for udp: a UDP port gives no generic answer; UDP reachability is tested with the UDP echo probe"}, nil
	default:
		return api.ProbeResultDTO{}, a.refuse(cmd, fmt.Sprintf("unknown protocol %q", args.Proto))
	}
	addr := netip.AddrPortFrom(ip.Unmap(), uint16(args.Port)).String() // #nosec G115 -- port checked above
	return dto(health.TCP(ctx, addr, probeTimeout(args.TimeoutMs))), nil
}

// udpListen answers probe.udp_listen: a UDP echo on 0.0.0.0:<port> for
// Seconds (section 10 UDP probe). The command returns once the socket is
// bound; asking again while it runs extends its lifetime.
func (a *agent) udpListen(ctx context.Context, args api.UDPListenArgs) error {
	const cmd = api.CmdProbeUDPListen
	if args.Port < 1 || args.Port > 65535 {
		return a.refuse(cmd, fmt.Sprintf("port %d is out of range", args.Port))
	}
	secs := args.Seconds
	if secs <= 0 {
		secs = DefaultUDPListenSeconds
	}
	secs = min(secs, MaxUDPListenSeconds)
	life := time.Duration(secs) * time.Second
	key := "udp-echo/" + strconv.Itoa(args.Port)
	if a.listeners.extend(key, life) {
		return nil
	}
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(args.Port))
	conn, err := a.o.ListenPacket(ctx, "udp", addr)
	if err != nil {
		return listenErr(args.Port, "udp", addr, err)
	}
	if !a.listeners.start(a.ctx, key, life, func(ctx context.Context) error { return health.ServeUDPEcho(ctx, conn) }, a.helperExit("udp echo", addr)) {
		_ = conn.Close()
		return deyerr.New(deyerr.X051, deyerr.Params{"service": "udp echo", "addr": addr})
	}
	a.log.Info("UDP echo started", slog.String("addr", addr), slog.Int("seconds", secs))
	return nil
}

// echoStart answers echo.start: the canary's loopback TCP echo on
// 127.0.0.1:<port> (0 = any free port) until echo.stop (section 9). The
// port is remembered in the state file, so the echo comes back when the
// agent restarts while the canary unit keeps running.
func (a *agent) echoStart(ctx context.Context, args api.EchoArgs) (api.EchoResult, error) {
	if args.Port < 0 || args.Port > 65535 {
		return api.EchoResult{}, a.refuse(api.CmdEchoStart, fmt.Sprintf("port %d is out of range", args.Port))
	}
	port, err := a.openEcho(ctx, args.Port)
	if err != nil {
		return api.EchoResult{}, err
	}
	a.rememberEcho(port, true)
	return api.EchoResult{Port: port}, nil
}

// openEcho starts the loopback echo on port (0 = any free port) unless it
// already runs, and returns its port.
func (a *agent) openEcho(ctx context.Context, port int) (int, error) {
	if port > 0 && a.listeners.running(echoKey(port)) {
		return port, nil
	}
	addr := net.JoinHostPort(loopbackHost, strconv.Itoa(port))
	ln, err := a.o.Listen(ctx, "tcp", addr)
	if err != nil {
		return 0, listenErr(port, "tcp", addr, err)
	}
	if ta, ok := ln.Addr().(*net.TCPAddr); ok {
		port = ta.Port
	}
	bound := ln.Addr().String()
	if !a.listeners.start(a.ctx, echoKey(port), 0, func(ctx context.Context) error { return health.ServeTCPEcho(ctx, ln) }, a.helperExit("tcp echo", bound)) {
		_ = ln.Close()
		return 0, deyerr.New(deyerr.X051, deyerr.Params{"service": "tcp echo", "addr": bound})
	}
	a.log.Info("canary echo started", slog.String("addr", bound))
	return port, nil
}

// echoStop answers echo.stop; stopping an echo that does not run is fine.
func (a *agent) echoStop(args api.EchoArgs) error {
	if args.Port < 1 || args.Port > 65535 {
		return a.refuse(api.CmdEchoStop, fmt.Sprintf("port %d is out of range", args.Port))
	}
	if a.listeners.stop(echoKey(args.Port)) {
		a.log.Info("canary echo stopped", slog.Int("port", args.Port))
	}
	a.rememberEcho(args.Port, false)
	return nil
}

// rememberEcho adds (on) or removes port in the persisted echo list. The
// echo itself already runs or stopped, so a failed save is only logged.
func (a *agent) rememberEcho(port int, on bool) {
	a.instMu.Lock()
	defer a.instMu.Unlock()
	i := slices.Index(a.st.Echo, port)
	switch {
	case on && i < 0:
		a.st.Echo = append(a.st.Echo, port)
		slices.Sort(a.st.Echo)
	case !on && i >= 0:
		a.st.Echo = slices.Delete(a.st.Echo, i, i+1)
	default:
		return
	}
	if err := a.saveState(); err != nil {
		a.setLastError(err)
		a.log.Warn("cannot record the canary echo in the state file", slog.Int("port", port), dlog.Err(err))
	}
}

// restoreEchoes reopens the canary echoes of the state file at start.
func (a *agent) restoreEchoes(ctx context.Context) {
	a.instMu.Lock()
	ports := slices.Clone(a.st.Echo)
	a.instMu.Unlock()
	for _, p := range ports {
		if p < 1 || p > 65535 {
			continue
		}
		if _, err := a.openEcho(ctx, p); err != nil {
			a.setLastError(err)
			a.log.Warn("cannot reopen the canary echo", slog.Int("port", p), dlog.Err(err))
		}
	}
}

func echoKey(port int) string { return "tcp-echo/" + strconv.Itoa(port) }

// speedServe answers speed.serve: the built-in traffic generator on
// 127.0.0.1:<port> for tests of up to Seconds per direction; it stops after
// 2 × Seconds plus a grace period (or on the next agent stop).
func (a *agent) speedServe(ctx context.Context, args api.SpeedServeArgs) error {
	if args.Port < 1 || args.Port > 65535 {
		return a.refuse(api.CmdSpeedServe, fmt.Sprintf("port %d is out of range", args.Port))
	}
	secs := args.Seconds
	if secs <= 0 {
		secs = health.DefaultSpeedSeconds
	}
	secs = min(secs, health.MaxSpeedSeconds)
	life := 2*time.Duration(secs)*time.Second + speedGrace
	key := "speed/" + strconv.Itoa(args.Port)
	if a.listeners.extend(key, life) {
		return nil
	}
	addr := net.JoinHostPort(loopbackHost, strconv.Itoa(args.Port))
	ln, err := a.o.Listen(ctx, "tcp", addr)
	if err != nil {
		return listenErr(args.Port, "tcp", addr, err)
	}
	if !a.listeners.start(a.ctx, key, life, func(ctx context.Context) error { return health.ServeSpeed(ctx, ln, secs) }, a.helperExit("speed generator", addr)) {
		_ = ln.Close()
		return deyerr.New(deyerr.X051, deyerr.Params{"service": "speed generator", "addr": addr})
	}
	a.log.Info("speed generator started", slog.String("addr", addr), slog.Int("seconds", secs))
	return nil
}

// helperExit logs a helper that stopped with an error.
func (a *agent) helperExit(service, addr string) func(error) {
	return func(err error) {
		if err != nil {
			a.setLastError(err)
			a.log.Warn("probe helper stopped", slog.String("service", service), slog.String("addr", addr), dlog.Err(err))
		}
	}
}

// listenErr reports a probe helper port that cannot be bound (DEY-P012).
func listenErr(port int, proto, addr string, err error) error {
	return deyerr.Wrap(deyerr.P012, err, deyerr.Params{
		"port": strconv.Itoa(port) + "/" + proto, "process": "another process", "addr": addr,
	}).WithWhy(fmt.Sprintf("the node could not open %s/%s: %v", addr, proto, err))
}
