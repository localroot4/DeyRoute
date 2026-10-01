package hub

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/systemd"
)

// speedNode makes a tunnel node answer speed.serve with the built-in
// generator on 127.0.0.1 and returns the address it listens on.
func speedNode(t *testing.T, n *tnode) func() string {
	var mu sync.Mutex
	addr := ""
	n.on(api.CmdSpeedServe, func(_ context.Context, _ *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.SpeedServeArgs
		decode(t, cmd, &a)
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(a.Port)))
		if err != nil {
			return nil, deyerr.Wrap(deyerr.P012, err, deyerr.Params{"port": a.Port, "process": "x", "addr": "127.0.0.1"})
		}
		mu.Lock()
		addr = ln.Addr().String()
		mu.Unlock()
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			_ = health.ServeSpeed(n.echoCtx, ln, a.Seconds)
		}()
		return nil, nil
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return addr
	}
}

func TestDiagSpeed(t *testing.T) {
	te := startTunnelHub(t, withIPForward)
	n := te.tunnelNode("de-1")
	target := speedNode(t, n)
	te.sd.mu.Lock()
	te.sd.canaryTarget = func(string) string { return target() }
	te.sd.mu.Unlock()
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})

	var log stepLog
	res, err := te.client.DiagSpeed(ctx, "main", 1, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Equal(t, "main", res.Tunnel)
	require.Equal(t, trAlpha, res.Transport)
	require.InDelta(t, 1, res.Seconds, 0.01)
	require.Positive(t, res.DownloadMbps)
	require.Positive(t, res.UploadMbps)
	require.Equal(t, []string{"speed_server:ok", "diag_render:ok", "diag_start:ok", "measure:ok", "diag_stop:ok"}, log.finished())

	// The temporary copy ran in the canary slot, server side (hub) first,
	// and is gone again on both sides.
	canary := systemd.CanaryInstance("main")
	order := te.order()
	hubAt, nodeAt := -1, -1
	for i, l := range order {
		if l == "hub start "+canary && hubAt < 0 {
			hubAt = i
		}
		if l == "node start "+canary && nodeAt < 0 {
			nodeAt = i
		}
	}
	require.True(t, hubAt >= 0 && nodeAt > hubAt, "%v", order)
	require.Contains(t, n.removedList(), canary)
	_, err = os.Stat(te.h.o.Systemd.DropInPath(canary))
	require.True(t, os.IsNotExist(err))
	require.NotEqual(t, "active", te.sd.state(systemd.UnitName(canary)))
	c := te.h.tun.lookup("main")
	c.mu.Lock()
	require.False(t, c.can.borrowed)
	c.mu.Unlock()
	te.waitActive("main", "de-1", trAlpha)

	// A NAT transport cannot run a second copy.
	te.addTunnelUp(api.TunnelAddRequest{ID: "wg", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trNAT, Failover: fastFailover(false)})
	_, err = te.client.DiagSpeed(ctx, "wg", 1, nil)
	require.Equal(t, deyerr.B006, codeOf(err))

	// A disabled tunnel has nothing to measure.
	require.NoError(t, te.client.TunnelSetEnabled(ctx, "wg", false))
	_, err = te.client.DiagSpeed(ctx, "wg", 1, nil)
	require.Equal(t, deyerr.F007, codeOf(err))

	// The node cannot start its generator.
	n.on(api.CmdSpeedServe, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return nil, deyerr.New(deyerr.X051, deyerr.Params{"service": "speed generator", "addr": "127.0.0.1"})
	})
	_, err = te.client.DiagSpeed(ctx, "main", 1, nil)
	require.Equal(t, deyerr.X051, codeOf(err))
}

func TestDiagSpeedMeasureFails(t *testing.T) {
	te := startTunnelHub(t, func(o *Options, _ string) { o.DiagReadyWait = 500 * time.Millisecond })
	n := te.tunnelNode("de-1")
	speedNode(t, n)
	// The copy starts but forwards nowhere: the ping fails until the wait
	// for the copy ends.
	te.sd.mu.Lock()
	te.sd.canaryTarget = func(string) string { return "127.0.0.1:1" }
	te.sd.mu.Unlock()
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trAlpha, Failover: fastFailover(false)})
	var log stepLog
	_, err := te.client.DiagSpeed(ctxT(t), "main", 1, log.add)
	require.Equal(t, deyerr.X052, codeOf(err))
	require.Contains(t, log.finished(), "measure:failed")
	require.Contains(t, log.finished(), "diag_stop:ok")
}
