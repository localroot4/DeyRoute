package hub

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/failover"
	"github.com/localroot4/deyroute/internal/health"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// Step ids of diag speed.
const (
	stepSpeedServer = "speed_server"
	stepDiagRender  = "diag_render"
	stepDiagStart   = "diag_start"
	stepMeasure     = "measure"
	stepDiagStop    = "diag_stop"
)

// DefaultDiagReadyWait bounds the wait until the temporary candidate
// forwards (the same 15 s a started rung has to pass its probe, section 7).
const DefaultDiagReadyWait = failover.StartWait

// DiagSpeed implements api.Local (`deyroute diag speed <tunnel> [--seconds]`,
// section 14): an iperf3-like test through the tunnel with the built-in
// generator. The node starts its traffic generator on a free 127.0.0.1
// port (speed.serve); a temporary copy of the ACTIVE transport is rendered
// in the tunnel's canary slot (no user port: a hub loopback port forwards to
// the generator), started server side first, measured (ping, download,
// upload for seconds each) and removed again. The canary stays down during
// the test. A transport that forwards with NAT (WireGuard/AmneziaWG) cannot
// run a second copy (DEY-B006); a UDP-only one cannot carry the TCP test
// (DEY-B010).
func (l *local) DiagSpeed(ctx context.Context, tunnel string, seconds int, progress func(api.Step)) (api.SpeedResult, error) {
	h := l.h
	if seconds <= 0 {
		seconds = health.DefaultSpeedSeconds
	}
	seconds = min(seconds, health.MaxSpeedSeconds)
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return api.SpeedResult{}, withLog(err)
	}
	defer unlock()
	c, eng, err := h.runningCtl(tunnel)
	if err != nil {
		return api.SpeedResult{}, withLog(err)
	}
	st := eng.State()
	if st.Active.IsZero() || !runningState(st.State) || !c.isStarted(st.Active) {
		return api.SpeedResult{}, withLog(deyerr.New(deyerr.F007, deyerr.Params{"tunnel": c.id}).
			WithWhy("the tunnel has no running transport (state " + st.State + ")").
			WithFix("wait until the tunnel is UP (deyroute status), then run the speed test again"))
	}
	t, plan := c.snapshot()
	pc, ok := plan.Candidate(st.Active.Node, st.Active.Transport)
	if !ok {
		b, tr, _ := strings.Cut(st.Active.Transport, "/")
		return api.SpeedResult{}, withLog(deyerr.New(deyerr.B002, deyerr.Params{"backend": b, "transport": tr}))
	}
	switch {
	case len(pc.Hub.NAT) > 0 || len(pc.Hub.Masquerade) > 0:
		return api.SpeedResult{}, withLog(deyerr.New(deyerr.B006, deyerr.Params{
			"transport": pc.TransportID, "reason": "diag speed cannot run a second copy of a transport that forwards with NAT",
		}).WithWhy("the speed test runs a temporary copy of the active transport next to it; a NAT-based transport owns the tunnel ports in the kernel").
			WithFix("switch the tunnel to another transport for the test (deyroute tunnel switch " + c.id + " --transport <id>), or measure with a real client"))
	case !pc.Transport.Supports(config.ProtoTCP):
		return api.SpeedResult{}, withLog(deyerr.New(deyerr.B010, deyerr.Params{"transport": pc.TransportID, "proto": config.ProtoTCP}).
			WithWhy("the speed test is TCP and the active transport forwards UDP only"))
	}
	node := st.Active.Node
	rep := &steps{progress: progress}
	var speedPort int
	if err := rep.run(stepSpeedServer, func() (string, error) {
		p, err := h.startSpeedServer(ctx, node, seconds)
		speedPort = p
		return "127.0.0.1:" + strconv.Itoa(p) + " on " + node, err
	}); err != nil {
		return api.SpeedResult{}, withLog(err)
	}
	c.borrowCanary(ctx)
	defer c.releaseCanary()
	var (
		cand *render.Candidate
		loop int
	)
	if err := rep.run(stepDiagRender, func() (string, error) {
		var err error
		cand, loop, err = render.DiagPlan(h.planInput(h.Config(), t), node, pc.TransportID, speedPort)
		if err != nil {
			return "", err
		}
		return pc.TransportID + " on " + node, h.writeDiag(ctx, c.id, cand)
	}); err != nil {
		if cand != nil {
			// Best effort: the step error is what the owner needs.
			_ = h.removeDiag(context.WithoutCancel(ctx), c.id, node, cand)
		}
		return api.SpeedResult{}, withLog(err)
	}
	defer func() {
		_ = rep.run(stepDiagStop, func() (string, error) {
			return "", h.removeDiag(context.WithoutCancel(ctx), c.id, node, cand)
		})
	}()
	if err := rep.run(stepDiagStart, func() (string, error) {
		return pc.TransportID, h.startDiag(ctx, c.id, node, cand)
	}); err != nil {
		return api.SpeedResult{}, withLog(err)
	}
	var res health.SpeedResult
	if err := rep.run(stepMeasure, func() (string, error) {
		r, err := h.measureSpeed(ctx, net.JoinHostPort(h.o.ProbeHost, strconv.Itoa(loop)), seconds)
		res = r
		if err != nil {
			return "", err
		}
		return strconv.FormatFloat(r.DownloadMbps, 'f', 1, 64) + " / " + strconv.FormatFloat(r.UploadMbps, 'f', 1, 64) + " Mbit/s", nil
	}); err != nil {
		return api.SpeedResult{}, withLog(err)
	}
	h.log.Info("speed test finished", dlog.Tunnel(c.id), dlog.Transport(pc.TransportID),
		slog.Float64("download_mbps", res.DownloadMbps), slog.Float64("upload_mbps", res.UploadMbps))
	return api.SpeedResult{
		Tunnel: c.id, Transport: pc.TransportID, Seconds: res.Seconds,
		DownloadMbps: res.DownloadMbps, UploadMbps: res.UploadMbps,
		RTTms: int(res.RTT.Round(time.Millisecond) / time.Millisecond),
	}, nil
}

// startSpeedServer asks node for its traffic generator on a free port of
// the control range (another port when that one is busy on the node).
func (h *Hub) startSpeedServer(ctx context.Context, node string, seconds int) (int, error) {
	used := map[int]bool{}
	if ports, err := h.st.CtlPorts(); err == nil {
		for _, p := range ports {
			used[p] = true
		}
	}
	var lastErr error
	for range udpPortTries {
		port, ok := freeCtlPort(used)
		if !ok {
			break
		}
		used[port] = true
		cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
		err := h.Call(cctx, node, api.CmdSpeedServe, api.SpeedServeArgs{Port: port, Seconds: seconds}, nil)
		cancel()
		if err == nil {
			return port, nil
		}
		lastErr = err
		if !deyerr.HasCode(err, deyerr.P012) {
			break
		}
	}
	if lastErr == nil {
		lastErr = deyerr.New(deyerr.P020, nil)
	}
	return 0, lastErr
}

// writeDiag writes the hub side of the temporary candidate (one
// daemon-reload) and sends its node side.
func (h *Hub) writeDiag(ctx context.Context, tunnel string, cand *render.Candidate) error {
	changed, err := h.hubWriter().Write(ctx, cand.Hub)
	if err != nil {
		return err
	}
	if changed {
		if err := h.o.Systemd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
	defer cancel()
	return h.Call(cctx, cand.Node, api.CmdBackendRender, render.NodePayload(tunnel, cand.NodeSide), nil)
}

// startDiag starts the temporary candidate, server side first.
func (h *Hub) startDiag(ctx context.Context, tunnel, node string, cand *render.Candidate) error {
	hubSide := func() error { return h.startHubSide(ctx, tunnel, cand.Backend, cand.Hub) }
	nodeSide := func() error {
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		return h.Call(cctx, node, api.CmdUnitStart, api.UnitArgs{Instance: cand.NodeSide.Instance}, nil)
	}
	first, second := hubSide, nodeSide
	if cand.Transport.Direction.ServerSide() == backend.SideNode {
		first, second = nodeSide, hubSide
	}
	if err := first(); err != nil {
		return err
	}
	return second()
}

// removeDiag stops the temporary candidate (client side first) and removes
// its files and drop-ins on the hub and on node.
func (h *Hub) removeDiag(ctx context.Context, tunnel, node string, cand *render.Candidate) error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	hubSide := func() {
		keep(h.stopHubSide(ctx, cand.Hub.Instance, cand.Backend, cand.Hub.ConfigDir))
	}
	nodeSide := func() {
		if !h.Online(node) {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, nodeCmdTimeout)
		defer cancel()
		keep(h.Call(cctx, node, api.CmdBackendRemove, api.BackendRemoveArgs{
			Instance: cand.NodeSide.Instance, ConfigDir: cand.NodeSide.ConfigDir,
		}, nil))
	}
	if cand.Transport.Direction.ServerSide() == backend.SideNode {
		hubSide()
		nodeSide()
	} else {
		nodeSide()
		hubSide()
	}
	keep(h.hubWriter().Remove(ctx, cand.Hub.Instance, cand.Hub.ConfigDir))
	if err := h.o.Systemd.DaemonReload(ctx); err != nil {
		keep(err)
	}
	if first != nil {
		h.log.Warn("the speed test copy was not removed cleanly", dlog.Tunnel(tunnel), dlog.Err(first))
	}
	return first
}

// measureSpeed runs health.MeasureSpeed, retrying while the temporary
// candidate does not forward yet (its ping fails) for up to DiagReadyWait.
func (h *Hub) measureSpeed(ctx context.Context, addr string, seconds int) (health.SpeedResult, error) {
	deadline := time.Now().Add(h.o.DiagReadyWait)
	for {
		mctx, cancel := context.WithTimeout(ctx, 2*time.Duration(seconds)*time.Second+30*time.Second)
		res, err := health.MeasureSpeed(mctx, addr, seconds)
		cancel()
		if err == nil {
			return res, nil
		}
		e := deyerr.As(err)
		if e.Params["phase"] != "ping" || time.Now().After(deadline) || ctx.Err() != nil {
			return res, err
		}
		if err := sleepCtx(ctx, 250*time.Millisecond); err != nil {
			return res, err
		}
	}
}

// borrowCanary stops the canary (when it runs) and keeps it down until
// releaseCanary: diag speed renders its temporary candidate in the canary
// slot, because nodes accept only warm and canary instance names.
func (c *tunnelCtl) borrowCanary(ctx context.Context) {
	c.mu.Lock()
	c.can.borrowed = true
	c.mu.Unlock()
	c.canWork.Lock()
	defer c.canWork.Unlock()
	c.mu.Lock()
	ready := c.can.ready
	c.mu.Unlock()
	if ready {
		c.canaryDown(ctx)
	}
}

// releaseCanary gives the canary slot back; the canary is set up again
// when the engine still wants it.
func (c *tunnelCtl) releaseCanary() {
	c.mu.Lock()
	c.can.borrowed = false
	want := c.can.want
	c.mu.Unlock()
	if want {
		c.requestCanary(true)
	}
}
