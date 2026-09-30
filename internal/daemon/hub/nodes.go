package hub

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

// Node operation limits.
const (
	// MinJoinTTL and MaxJoinTTL bound `deyroute node join-command --ttl`.
	MinJoinTTL = time.Minute
	MaxJoinTTL = 24 * time.Hour
	// udpListenSeconds keeps the node's UDP echo open for one test.
	udpListenSeconds = 10
	// udpPortTries is how many free control-range ports are tried when the
	// node cannot bind one.
	udpPortTries = 3
	// nodeCallTimeout bounds one node command of the node operations.
	nodeCallTimeout = 30 * time.Second
)

// NodeJoinCommand implements api.Local (section 3, Join 1): a new one-time
// token (ttl 15 minutes by default, 1 minute to 24 hours) is turned into
// dey://TOKEN@HUB_IP:PORT#sha256:<ca> and the one-line command
// bash <(curl -fsSL <installer>) join '<link>'. The firewall is re-applied
// at once so the join window (control port open to all) is open.
func (l *local) NodeJoinCommand(ctx context.Context, ttl time.Duration) (api.JoinCommand, error) {
	h := l.h
	if ttl <= 0 {
		ttl = api.JoinTokenTTL
	}
	if ttl < MinJoinTTL || ttl > MaxJoinTTL {
		return api.JoinCommand{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "ttl", "value": ttl.String(), "allowed": "1m to 24h (default 15m)",
		}))
	}
	cfg := h.Config()
	token, expires, err := h.joins.Create(ttl)
	if err != nil {
		return api.JoinCommand{}, withLog(err)
	}
	link := api.FormatJoinLink(api.JoinLink{
		Token:       token,
		Host:        cfg.Hub.PublicIP,
		Port:        cfg.Hub.ControlPort,
		Fingerprint: h.currentCA().Fingerprint(),
	})
	mirror := firstNonEmpty(h.o.Getenv(install.MirrorEnv), cfg.Hub.Mirror)
	cmd := setup.JoinCommand(setup.InstallerURL(mirror), link, version.Version)
	if err := h.applyFirewall(ctx); err != nil {
		h.log.Warn("the join window could not be opened in the firewall", dlog.Err(err))
	}
	h.wakeFirewall()
	h.log.Info("join command created", slog.Time("expires", expires))
	return api.JoinCommand{Command: cmd, Link: link, ExpiresAt: expires}, nil
}

// NodeRename implements api.Local: only the display name changes (ids are
// immutable, section 4).
func (l *local) NodeRename(_ context.Context, id, name string) error {
	h := l.h
	clean := cleanName(name)
	if clean == "" {
		return withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "nodes[" + id + "].name", "value": name, "allowed": "1 to 64 printable characters",
		}))
	}
	if _, ok := h.Config().NodeByID(id); !ok {
		return withLog(deyerr.New(deyerr.N008, deyerr.Params{"node": id}))
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	_, err := h.mutate(func(c *config.Config) error {
		n, ok := c.NodeByID(id)
		if !ok {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": id})
		}
		n.Name = clean
		return nil
	})
	if err != nil {
		return withLog(err)
	}
	h.log.Info("node renamed", dlog.Node(id), slog.String("name", clean))
	return nil
}

// NodeRemove implements api.Local (section 14: stops its tunnels, revokes
// its certificate, drops its firewall entry). A tunnel whose only node it
// is refuses the removal (DEY-C008); otherwise the node leaves every
// tunnel's node list, config.yaml (its certificate is no longer accepted)
// and state.db, its stream is closed and @nodes is re-applied.
func (l *local) NodeRemove(ctx context.Context, id string) error {
	h := l.h
	cfg := h.Config()
	if _, ok := cfg.NodeByID(id); !ok {
		return withLog(deyerr.New(deyerr.N008, deyerr.Params{"node": id}))
	}
	if _, _, err := config.Clone(cfg).RemoveNode(id); err != nil {
		return withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return withLog(err)
	}
	if err := h.onNodeRemoved(ctx, id); err != nil {
		h.log.Warn("stopping the node's tunnels failed; removing it anyway", dlog.Node(id), dlog.Err(err))
	}
	var affected []string
	_, err := h.mutate(func(c *config.Config) error {
		a, removed, err := c.RemoveNode(id)
		if err != nil {
			return err
		}
		if !removed {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": id})
		}
		affected = a
		return nil
	})
	if err != nil {
		return withLog(err)
	}
	// Drop the live entry first so a last heartbeat cannot write the
	// state record again.
	h.forgetNode(id)
	if err := h.st.DeleteNode(id); err != nil {
		h.log.Warn("cannot delete the node state", dlog.Node(id), dlog.Err(err))
	}
	h.requestFirewall()
	if len(affected) > 0 {
		if err := h.reconcileAll(ctx); err != nil {
			h.log.Warn("reconcile after node removal failed", dlog.Node(id), dlog.Err(err))
		}
	}
	h.log.Info("node removed", dlog.Node(id), slog.String("tunnels", strings.Join(affected, ",")))
	return nil
}

// NodeTest implements api.Local (`deyroute node test`): control RTT, the
// node's sysinfo and a UDP echo probe from the hub to the node (section 10:
// probe.udp_listen on a free port of the control range, then health.UDPEcho
// to node_ip:port, 3 tries of 2 s). The UDP result is stored in state.db.
func (l *local) NodeTest(ctx context.Context, id string) (api.NodeTestResult, error) {
	h := l.h
	n, ok := h.Config().NodeByID(id)
	if !ok {
		return api.NodeTestResult{}, withLog(deyerr.New(deyerr.N008, deyerr.Params{"node": id}))
	}
	s, err := h.session(id, api.CmdSysinfo)
	if err != nil {
		return api.NodeTestResult{Node: id}, withLog(err)
	}
	res := api.NodeTestResult{Node: id, Online: true, SysInfo: map[string]string{}}
	pctx, cancel := context.WithTimeout(ctx, pingTimeout)
	rtt, err := s.Ping(pctx)
	cancel()
	if err != nil {
		return res, withLog(err)
	}
	res.ControlRTTms = max(int(rtt.Round(time.Millisecond)/time.Millisecond), 0)
	cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
	err = h.Call(cctx, id, api.CmdSysinfo, nil, &res.SysInfo)
	cancel()
	if err != nil {
		return res, withLog(err)
	}
	host := firstNonEmpty(n.PublicIP, s.RemoteIP)
	udpOK, udpRTT, err := h.udpTest(ctx, id, host)
	if err != nil {
		return res, withLog(err)
	}
	res.UDPOK = udpOK
	res.UDPRTTms = int(udpRTT.Round(time.Millisecond) / time.Millisecond)
	now := h.now()
	h.updateNode(id, func(ns *state.NodeState) {
		v := udpOK
		ns.UDPOK = &v
		ns.UDPCheckedAt = now
	})
	return res, nil
}

// udpTest asks node for a temporary UDP echo on a free port of the backend
// control range and probes it from the hub.
func (h *Hub) udpTest(ctx context.Context, node, host string) (bool, time.Duration, error) {
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
		err := h.Call(cctx, node, api.CmdProbeUDPListen, api.UDPListenArgs{Port: port, Seconds: udpListenSeconds}, nil)
		cancel()
		if err != nil {
			lastErr = err
			if deyerr.HasCode(err, deyerr.N003) || deyerr.HasCode(err, deyerr.N004) || ctx.Err() != nil {
				break
			}
			continue
		}
		r := health.UDPEcho(ctx, net.JoinHostPort(host, strconv.Itoa(port)), health.UDPTries, h.o.UDPProbeTimeout)
		return r.OK, r.RTT, nil
	}
	if lastErr == nil {
		lastErr = deyerr.New(deyerr.P020, nil)
	}
	return false, 0, lastErr
}

// freeCtlPort picks a random port of the control range not in used.
func freeCtlPort(used map[int]bool) (int, bool) {
	span := config.CtlRangeHigh - config.CtlRangeLow + 1
	start := rand.IntN(span) // #nosec G404 -- port choice, not security relevant
	for i := range span {
		p := config.CtlRangeLow + (start+i)%span
		if !used[p] {
			return p, true
		}
	}
	return 0, false
}

// HubAnnounceMove implements api.Local (section 5: moving the hub): every
// online node is told the new address (set_hub) and reconnects there;
// offline nodes are listed so the owner runs `deyroute node set-hub` there.
func (l *local) HubAnnounceMove(ctx context.Context, newAddr string) (api.AnnounceResult, error) {
	h := l.h
	addr := strings.TrimSpace(newAddr)
	if !config.ValidHostPort(addr) {
		return api.AnnounceResult{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "address", "value": newAddr, "allowed": "IP:PORT of the new hub, e.g. 5.6.7.8:44433",
		}))
	}
	res := api.AnnounceResult{Accepted: []string{}, Offline: []string{}}
	for _, n := range h.Config().Nodes {
		cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
		err := h.Call(cctx, n.ID, api.CmdSetHub, api.SetHubArgs{Addr: addr}, nil)
		cancel()
		if err != nil {
			h.log.Warn("node did not accept the new hub address", dlog.Node(n.ID), dlog.Err(err))
			res.Offline = append(res.Offline, n.ID)
			continue
		}
		res.Accepted = append(res.Accepted, n.ID)
	}
	h.log.Info("hub move announced", slog.String("addr", addr),
		slog.Int("accepted", len(res.Accepted)), slog.Int("offline", len(res.Offline)))
	return res, nil
}

// UninstallNodes implements api.Local (section 5: "remove the nodes too?"):
// every online node is told to uninstall itself; the ids that accepted are
// returned.
func (l *local) UninstallNodes(ctx context.Context) ([]string, error) {
	h := l.h
	accepted := []string{}
	for _, n := range h.Config().Nodes {
		if !h.Online(n.ID) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
		err := h.Call(cctx, n.ID, api.CmdUninstall, nil, nil)
		cancel()
		if err != nil {
			h.log.Warn("node did not accept uninstall", dlog.Node(n.ID), dlog.Err(err))
			continue
		}
		accepted = append(accepted, n.ID)
	}
	return accepted, nil
}

// StopAll implements api.Local (uninstall helper): the failover engines
// stop, then every running deyroute-tun@ unit on the hub and on every online
// node is stopped. Failures are collected; the first one is returned after
// everything was tried.
func (l *local) StopAll(ctx context.Context) error {
	h := l.h
	var errs []error
	if err := h.stopEngines(ctx); err != nil {
		errs = append(errs, err)
	}
	units, err := h.o.Systemd.ListInstances(ctx)
	if err != nil {
		errs = append(errs, err)
	}
	for _, u := range units {
		if !unitRunning(u.ActiveState) {
			continue
		}
		if err := h.o.Systemd.Stop(ctx, u.Unit); err != nil {
			errs = append(errs, err)
		}
	}
	for _, n := range h.Config().Nodes {
		if !h.Online(n.ID) {
			continue
		}
		ns, _ := h.nodeState(n.ID)
		for _, unit := range sortedKeys(ns.Units) {
			if !unitRunning(ns.Units[unit]) {
				continue
			}
			inst, ok := systemd.InstanceOf(unit)
			if !ok {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
			err := h.Call(cctx, n.ID, api.CmdUnitStop, api.UnitArgs{Instance: inst}, nil)
			cancel()
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		for _, e := range errs[1:] {
			h.log.Warn("stop all: another unit failed to stop", dlog.Err(e))
		}
		return withLog(errs[0])
	}
	h.log.Info("every tunnel unit stopped")
	return nil
}

// unitRunning reports an ActiveState that needs a stop.
func unitRunning(s string) bool {
	switch s {
	case "active", "activating", "reloading", "deactivating":
		return true
	}
	return false
}
