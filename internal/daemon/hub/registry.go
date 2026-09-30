package hub

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

// nodeRuntime is the live view of one node (section 3: node registry).
type nodeRuntime struct {
	st   state.NodeState
	sess *api.Session // current control stream; nil when not connected
	// seen is the last sign of life (connect or heartbeat) used by the
	// offline detector; after a hub restart a node that was online gets the
	// start time, so it is not reported offline before it had a chance to
	// reconnect.
	seen      time.Time
	persisted time.Time
	// synced is set by the first heartbeat of the current stream (the
	// node's unit list is known from then on).
	synced bool
	// skew is the node clock minus the hub clock at the last heartbeat
	// (doctor rule R13); skewKnown is false before a heartbeat with a time.
	skew      time.Duration
	skewKnown bool
}

// loadNodes fills the registry from state.db at start.
func (h *Hub) loadNodes() {
	saved, err := h.st.ListNodes()
	if err != nil {
		h.log.Warn("cannot read the node states", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	now := h.now()
	cfg := h.Config()
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	for _, ns := range saved {
		if _, ok := cfg.NodeByID(ns.ID); !ok {
			continue
		}
		nr := &nodeRuntime{st: ns, seen: ns.LastHeartbeat}
		if ns.Online {
			nr.seen = now
		}
		h.nodes[ns.ID] = nr
	}
}

// runtimeLocked returns the registry entry of id, creating it.
func (h *Hub) runtimeLocked(id string) *nodeRuntime {
	nr := h.nodes[id]
	if nr == nil {
		nr = &nodeRuntime{st: state.NodeState{ID: id}}
		h.nodes[id] = nr
	}
	return nr
}

// nodeState returns a copy of the live state of node id.
func (h *Hub) nodeState(id string) (state.NodeState, bool) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	nr := h.nodes[id]
	if nr == nil {
		return state.NodeState{ID: id}, false
	}
	return copyNodeState(nr.st), true
}

func copyNodeState(s state.NodeState) state.NodeState {
	if s.Units != nil {
		u := make(map[string]string, len(s.Units))
		for k, v := range s.Units {
			u[k] = v
		}
		s.Units = u
	}
	if s.UDPOK != nil {
		v := *s.UDPOK
		s.UDPOK = &v
	}
	return s
}

// persistNode writes a node state to state.db.
func (h *Hub) persistNode(ns state.NodeState) {
	if err := h.st.PutNode(ns); err != nil {
		h.log.Warn("cannot save the node state", dlog.Node(ns.ID), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
}

// updateNode applies fn to the live state of id and persists the result.
func (h *Hub) updateNode(id string, fn func(ns *state.NodeState)) {
	h.nodesMu.Lock()
	nr := h.runtimeLocked(id)
	fn(&nr.st)
	nr.persisted = h.now()
	snap := copyNodeState(nr.st)
	h.nodesMu.Unlock()
	h.persistNode(snap)
}

// serveSession is ControlHandler.Session: it registers the node's stream,
// answers the hello with the hub's version and the compatibility verdict
// (section 5), records heartbeats and measures the control RTT until the
// stream ends.
func (h *Hub) serveSession(s *api.Session) {
	if _, ok := h.Config().NodeByID(s.NodeID); !ok {
		// Removed between authentication and the start of the stream.
		s.Close()
		return
	}
	compatible := version.Compatible(version.Version, s.Hello.Version)
	if err := s.SendHello(api.Hello{Version: version.Version, Compatible: compatible}); err != nil {
		h.log.Info("cannot answer the node hello", dlog.Node(s.NodeID), dlog.Err(err))
		return
	}
	if !h.attach(s, compatible) {
		s.Close()
		return
	}
	defer h.detach(s)
	ping := time.NewTicker(h.o.PingInterval)
	defer ping.Stop()
	h.pingNode(s)
	for {
		select {
		case <-s.Done():
			return
		case hb := <-s.Heartbeats():
			h.heartbeat(s, hb)
		case <-ping.C:
			h.pingNode(s)
		}
	}
}

// attach records a new control stream; a node that was offline comes
// online (event node_online). It refuses (false) a node that is no longer
// in config.yaml: checked under the registry lock, so a NodeRemove either
// sees this stream (and closes it) or this stream sees the removal.
func (h *Hub) attach(s *api.Session, compatible bool) bool {
	now := h.now()
	h.nodesMu.Lock()
	if _, ok := h.Config().NodeByID(s.NodeID); !ok {
		h.nodesMu.Unlock()
		return false
	}
	nr := h.runtimeLocked(s.NodeID)
	wasOnline := nr.st.Online
	nr.sess = s
	nr.seen = now
	nr.synced = false
	nr.st.Online = true
	if !wasOnline {
		nr.st.OnlineSince = now
	}
	nr.st.AgentVersion = s.Hello.Version
	nr.st.Compatible = compatible
	nr.st.Arch, nr.st.OS, nr.st.Kernel = s.Hello.Arch, s.Hello.OS, s.Hello.Kernel
	nr.st.RemoteIP = s.RemoteIP
	nr.persisted = now
	snap := copyNodeState(nr.st)
	h.nodesMu.Unlock()
	h.persistNode(snap)

	h.log.Info("node connected", dlog.Node(s.NodeID), slog.String("version", s.Hello.Version),
		slog.String("remote_ip", s.RemoteIP), slog.Bool("compatible", compatible))
	if !compatible {
		e := deyerr.New(deyerr.N004, deyerr.Params{"node": s.NodeID, "node_version": s.Hello.Version, "hub_version": version.Version})
		h.log.Warn("node version is incompatible; it will not run commands until it is updated",
			dlog.Node(s.NodeID), dlog.Code(deyerr.N004), dlog.Err(e))
	}
	if !wasOnline {
		h.Emit(state.Event{
			Type: state.EvNodeOnline, Level: state.LevelInfo, Node: s.NodeID,
			Message: "node " + s.NodeID + " is online (" + s.Hello.Version + ", " + s.RemoteIP + ")",
		})
	}
	// Every new stream (a reconnect too: the node may have rebooted) makes
	// the tunnel controllers install, render and start what the node needs.
	if compatible {
		h.nodeAttached(s.NodeID)
	}
	// After `deyroute update` every node follows the hub's version (ops_update.go).
	h.requestNodeUpdate(s.NodeID, s.Hello.Version)
	return true
}

// detach forgets a finished stream. The node stays online until the
// offline detector sees no heartbeat for OfflineAfter, so a quick
// reconnect (set_hub, cert.install, network blip) raises no event.
func (h *Hub) detach(s *api.Session) {
	h.nodesMu.Lock()
	if nr := h.nodes[s.NodeID]; nr != nil && nr.sess == s {
		nr.sess = nil
	}
	h.nodesMu.Unlock()
	h.log.Info("node stream ended", dlog.Node(s.NodeID))
}

// heartbeat records one heartbeat of the current stream of a node.
func (h *Hub) heartbeat(s *api.Session, hb api.Heartbeat) {
	now := h.now()
	h.nodesMu.Lock()
	nr := h.nodes[s.NodeID]
	if nr == nil || nr.sess != s {
		h.nodesMu.Unlock()
		return
	}
	wasOnline := nr.st.Online
	nr.seen = now
	nr.st.Online = true
	if !wasOnline {
		nr.st.OnlineSince = now
	}
	nr.st.LastHeartbeat = now
	if !hb.At.IsZero() {
		nr.skew, nr.skewKnown = hb.At.Sub(now), true
	}
	nr.st.CPUPercent = hb.CPUPercent
	nr.st.RAMBytes = hb.RAMBytes
	units := make(map[string]string, len(hb.Units))
	for k, v := range hb.Units {
		units[k] = v
	}
	nr.st.Units = units
	nr.st.LastError = dlog.Redact(hb.LastError)
	persist := !wasOnline || now.Sub(nr.persisted) >= h.o.NodePersistEvery
	if persist {
		nr.persisted = now
	}
	firstBeat := !nr.synced && nr.st.Compatible
	nr.synced = true
	snap := copyNodeState(nr.st)
	h.nodesMu.Unlock()
	if persist {
		h.persistNode(snap)
	}
	if firstBeat {
		// Its units are known now: leftovers of deleted tunnels go.
		h.requestNodeCleanup(s.NodeID)
	}
	if !wasOnline {
		h.Emit(state.Event{
			Type: state.EvNodeOnline, Level: state.LevelInfo, Node: s.NodeID,
			Message: "node " + s.NodeID + " is online again",
		})
	}
}

// pingNode measures the control round-trip time of a stream.
func (h *Hub) pingNode(s *api.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	rtt, err := s.Ping(ctx)
	if err != nil {
		h.log.Debug("control ping failed", dlog.Node(s.NodeID), dlog.Err(err))
		return
	}
	ms := int(rtt.Round(time.Millisecond) / time.Millisecond)
	h.nodesMu.Lock()
	if nr := h.nodes[s.NodeID]; nr != nil && nr.sess == s {
		nr.st.ControlRTTms = ms
	}
	h.nodesMu.Unlock()
}

// monitorLoop runs the offline detector every MonitorInterval.
func (h *Hub) monitorLoop(ctx context.Context) {
	t := time.NewTicker(h.o.MonitorInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.checkOffline()
			h.alive.Store(time.Now().UnixNano())
		}
	}
}

// watchdogInterval is how often WATCHDOG=1 is due: half of $WATCHDOG_USEC,
// 0 when systemd runs no watchdog for this process (section 3: both
// services run with WatchdogSec).
func (h *Hub) watchdogInterval() time.Duration {
	usec := strings.TrimSpace(h.o.Getenv("WATCHDOG_USEC"))
	if usec == "" {
		return 0
	}
	if pid := strings.TrimSpace(h.o.Getenv("WATCHDOG_PID")); pid != "" && pid != strconv.Itoa(os.Getpid()) {
		return 0
	}
	n, err := strconv.ParseInt(usec, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Microsecond / 2
}

// watchdogLoop sends WATCHDOG=1 every watchdog interval while the hub is
// responsive: the offline detector (registry lock, state.db writes) must
// have completed a pass recently. A hub that hangs stops pinging and
// systemd restarts it; the tunnel units keep running meanwhile.
func (h *Hub) watchdogLoop(ctx context.Context) {
	iv := h.watchdogInterval()
	if iv <= 0 {
		return
	}
	stale := max(iv, 3*h.o.MonitorInterval)
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if since := time.Since(time.Unix(0, h.alive.Load())); since > stale {
			h.log.Error("hub is not responsive; the watchdog ping is withheld so systemd restarts it",
				slog.Duration("stalled", since.Round(time.Second)), dlog.Code(deyerr.X000))
			continue
		}
		if err := h.o.Notify(systemd.StateWatchdog); err != nil {
			h.log.Warn("watchdog notification failed", dlog.Err(err))
		}
	}
}

// checkOffline marks nodes without a sign of life for OfflineAfter as
// offline and emits node_offline (section 3: offline after 15 s without a
// heartbeat).
func (h *Hub) checkOffline() {
	now := h.now()
	var gone []state.NodeState
	h.nodesMu.Lock()
	for _, nr := range h.nodes {
		if nr.st.Online && now.Sub(nr.seen) > h.o.OfflineAfter {
			nr.st.Online = false
			nr.st.ControlRTTms = 0
			nr.persisted = now
			gone = append(gone, copyNodeState(nr.st))
		}
	}
	h.nodesMu.Unlock()
	sort.Slice(gone, func(i, j int) bool { return gone[i].ID < gone[j].ID })
	for _, ns := range gone {
		h.persistNode(ns)
		h.log.Warn("node offline", dlog.Node(ns.ID), dlog.Code(deyerr.N003))
		h.Emit(state.Event{
			Type: state.EvNodeOffline, Level: state.LevelWarn, Node: ns.ID, Code: string(deyerr.N003),
			Message: "node " + ns.ID + " is offline (no heartbeat for " + h.o.OfflineAfter.String() + ")",
		})
	}
}

// ControlOnline reports whether node has a live control connection and,
// when it has none, for how long it has been without one (failover
// Actions.ControlOnline, section 9).
func (h *Hub) ControlOnline(node string) (online bool, offlineFor time.Duration) {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	nr := h.nodes[node]
	if nr == nil {
		return false, time.Duration(1<<63 - 1)
	}
	if nr.st.Online && nr.sess != nil {
		return true, 0
	}
	if nr.seen.IsZero() {
		return false, time.Duration(1<<63 - 1)
	}
	return false, max(h.now().Sub(nr.seen), 0)
}

// session returns the usable control stream of node: DEY-N008 for a node
// that is not in config.yaml, DEY-N003 when it is offline and DEY-N004 when
// its version is incompatible (only self.update may run then).
func (h *Hub) session(node, command string) (*api.Session, error) {
	if _, ok := h.Config().NodeByID(node); !ok {
		return nil, deyerr.New(deyerr.N008, deyerr.Params{"node": node})
	}
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	nr := h.nodes[node]
	if nr == nil || nr.sess == nil || !nr.st.Online {
		return nil, deyerr.New(deyerr.N003, deyerr.Params{"node": node})
	}
	if !nr.st.Compatible && command != api.CmdSelfUpdate {
		return nil, deyerr.New(deyerr.N004, deyerr.Params{"node": node, "node_version": nr.st.AgentVersion, "hub_version": version.Version})
	}
	return nr.sess, nil
}

// Call runs command name on node and decodes its result into out (nil =
// ignore): DEY-N003 when the node is offline, DEY-N004 when its version is
// incompatible, otherwise the Session.Call errors (N005 timeout, N014
// cancelled, the node's own DEY code, N011).
func (h *Hub) Call(ctx context.Context, node, name string, args, out any) error {
	s, err := h.session(node, name)
	if err != nil {
		return err
	}
	return s.Call(ctx, name, args, out)
}

// Stream runs a streaming command (logs.tail with Follow) on node; see
// Session.Stream.
func (h *Hub) Stream(ctx context.Context, node, name string, args any, onLines func([]string)) error {
	s, err := h.session(node, name)
	if err != nil {
		return err
	}
	return s.Stream(ctx, name, args, onLines)
}

// Online reports whether node is connected and online.
func (h *Hub) Online(node string) bool {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	nr := h.nodes[node]
	return nr != nil && nr.sess != nil && nr.st.Online
}

// onlineNodes returns the connected, online and compatible nodes, fastest
// control link first (fetch.proxy and http.post pick from it).
func (h *Hub) onlineNodes() []string {
	type cand struct {
		id  string
		rtt int
	}
	var list []cand
	h.nodesMu.Lock()
	for id, nr := range h.nodes {
		if nr.sess != nil && nr.st.Online && nr.st.Compatible {
			list = append(list, cand{id, nr.st.ControlRTTms})
		}
	}
	h.nodesMu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].rtt != list[j].rtt {
			return list[i].rtt < list[j].rtt
		}
		return list[i].id < list[j].id
	})
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.id
	}
	return out
}

// forgetNode closes the stream of a removed node and drops it from the
// registry.
func (h *Hub) forgetNode(id string) {
	h.nodesMu.Lock()
	nr := h.nodes[id]
	delete(h.nodes, id)
	h.nodesMu.Unlock()
	if nr != nil && nr.sess != nil {
		nr.sess.Close()
	}
}

// clockSkews returns node clock minus hub clock for every node that sent a
// heartbeat with its time (doctor rule R13).
func (h *Hub) clockSkews() map[string]time.Duration {
	h.nodesMu.Lock()
	defer h.nodesMu.Unlock()
	out := map[string]time.Duration{}
	for id, nr := range h.nodes {
		if nr.skewKnown {
			out[id] = nr.skew
		}
	}
	return out
}

// setNodeIP records the node's current public IP in the registry.
func (h *Hub) setNodeIP(id, ip string) {
	h.nodesMu.Lock()
	h.runtimeLocked(id).st.RemoteIP = ip
	h.nodesMu.Unlock()
}
