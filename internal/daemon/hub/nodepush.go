package hub

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/version"
)

// The wait before a node that refused or failed self.update is asked again
// (DR-11): it doubles after every failure, from nodePushRetryFirst up to
// nodePushRetryMax. Without it a node that refuses the hub's version (a
// signed-update node and an unsigned build: DEY-S012, a tampered release:
// DEY-S001) would be asked on every reconnect. The record lives for this
// run of the hub only: a restart tries again at once.
const (
	nodePushRetryFirst = 10 * time.Minute
	nodePushRetryMax   = 6 * time.Hour
)

// nodePushTry is the failed self.update of one node.
type nodePushTry struct {
	fails int
	next  time.Time // no self.update before
}

// pushIncompatible reports whether node, which runs nodeVersion and has no
// pending update, gets the hub's version anyway (DR-11): only when its
// major.minor differs (version.Compatible) and this hub runs a release. The
// push is recorded like `deyroute update` (markNodesForUpdate), so every
// node then follows. A node of the same major.minor is left as it is.
func (h *Hub) pushIncompatible(node, nodeVersion string) bool {
	if version.Compatible(version.Version, nodeVersion) {
		return false
	}
	if !isRelease(version.Version) {
		// No signed release to push; a node with signed updates would
		// refuse it (DEY-S012) and an older node would run a test build.
		if h.notePush(node, "dev") {
			h.log.Warn("the hub runs a development build: it does not push its binary to a node of another version; update the node with the installer",
				dlog.Node(node), slog.String("node_version", nodeVersion), slog.String("version", version.Version), dlog.Code(deyerr.N004))
		}
		return false
	}
	h.log.Info("node runs another major.minor than the hub: it is told to install the hub's version",
		dlog.Node(node), slog.String("node_version", nodeVersion), slog.String("version", version.Version), dlog.Code(deyerr.N004))
	h.markNodesForUpdate()
	return true
}

// notePush reports whether the note key about node is new in this run (a
// note is logged once).
func (h *Hub) notePush(node, key string) bool {
	h.ops.pushMu.Lock()
	defer h.ops.pushMu.Unlock()
	k := node + "\x00" + key
	if h.ops.pushNoted[k] {
		return false
	}
	h.ops.pushNoted[k] = true
	return true
}

// pushAllowed reports whether node may be sent self.update now: not
// while it waits after a failure.
func (h *Hub) pushAllowed(node string) bool {
	h.ops.pushMu.Lock()
	defer h.ops.pushMu.Unlock()
	t := h.ops.pushTries[node]
	if t == nil || !h.now().Before(t.next) {
		return true
	}
	h.log.Debug("node update waits after a failure", dlog.Node(node), slog.Time("next", t.next), slog.Int("failures", t.fails))
	return false
}

// pushFailed records a failed self.update of node and when it is tried
// again. err (the node's answer; nil = already logged) is logged at warn
// the first time in this run, later at debug.
func (h *Hub) pushFailed(node string, err error) {
	h.ops.pushMu.Lock()
	t := h.ops.pushTries[node]
	if t == nil {
		t = &nodePushTry{}
		h.ops.pushTries[node] = t
	}
	t.fails++
	wait := nodePushRetryFirst << min(t.fails-1, 16)
	if wait > nodePushRetryMax || wait <= 0 {
		wait = nodePushRetryMax
	}
	t.next = h.now().Add(wait)
	first := t.fails == 1
	h.ops.pushMu.Unlock()
	if err == nil {
		return
	}
	attrs := []any{dlog.Node(node), slog.String("version", version.Version), slog.Duration("retry_in", wait),
		dlog.Err(err), dlog.Code(deyerr.As(err).Code)}
	if first {
		h.log.Warn("node could not install the hub's version; it is asked again when it reconnects after a wait", attrs...)
		return
	}
	h.log.Debug("node could not install the hub's version again", attrs...)
}

// pushSucceeded forgets the failures of node (it runs the hub's version).
func (h *Hub) pushSucceeded(node string) {
	h.ops.pushMu.Lock()
	delete(h.ops.pushTries, node)
	h.ops.pushMu.Unlock()
}

// markStaleNodes is the node part of `deyroute update` when no newer
// release exists (DR-11): when a known node runs another version than the
// hub, the update is recorded (every node installs the hub's version when
// it connects) and the online ones are queued now. It returns the number
// of such nodes; 0 and nothing recorded on a development build, which has
// no signed release to push.
func (h *Hub) markStaleNodes() int {
	var stale, online []string
	vers := map[string]string{}
	for _, n := range h.Config().Nodes {
		ns, _ := h.nodeState(n.ID)
		if ns.AgentVersion == "" || sameVersion(ns.AgentVersion, version.Version) {
			continue
		}
		stale = append(stale, n.ID)
		if ns.Online {
			online = append(online, n.ID)
			vers[n.ID] = ns.AgentVersion
		}
	}
	if len(stale) == 0 {
		return 0
	}
	if !isRelease(version.Version) {
		h.log.Warn("the hub runs a development build: its nodes are not told to install it",
			slog.String("nodes", strings.Join(stale, ",")), slog.String("version", version.Version))
		return 0
	}
	h.markNodesForUpdate()
	h.log.Info("nodes run another version than the hub: they install the hub's version",
		slog.String("nodes", strings.Join(stale, ",")), slog.String("version", version.Version))
	for _, id := range online {
		h.requestNodeUpdate(id, vers[id])
	}
	return len(stale)
}

// staleNodesDetail is the step detail of markStaleNodes.
func staleNodesDetail(n int) string {
	if n == 1 {
		return "1 node runs another version; it installs deyroute " + version.Version + " from the hub"
	}
	return strconv.Itoa(n) + " nodes run another version; they install deyroute " + version.Version + " from the hub"
}

// UpdateNodes implements api.Local (`deyroute update` when the hub runs
// the newest release): it installs nothing and tells every known node of
// another version to install the hub's (markStaleNodes). It returns how
// many nodes run another version (0 on a development build).
func (l *local) UpdateNodes(context.Context) (int, error) {
	h := l.h
	h.ops.updMu.Lock()
	defer h.ops.updMu.Unlock()
	return h.markStaleNodes(), nil
}

// noteRestoredFrom reads the restore marker once at start (DR-11): a hub
// restored from a backup of another version tells every node to install
// its version. A development build marks nothing.
func (h *Hub) noteRestoredFrom() {
	from, err := setup.TakeRestoredFrom(h.o.Root)
	if err != nil {
		h.log.Warn("cannot read the version of the restored backup", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
	if from == "" || sameVersion(from, version.Version) {
		return
	}
	if !isRelease(version.Version) {
		h.log.Warn("restored from a backup of another version; a development build does not push itself to the nodes",
			slog.String("backup_version", from), slog.String("version", version.Version))
		return
	}
	h.log.Info("restored from a backup of another version: every node installs the hub's version when it connects",
		slog.String("backup_version", from), slog.String("version", version.Version))
	h.markNodesForUpdate()
}
