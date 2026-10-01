package hub

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// BackendUpdate statuses (api.BackendUpdate.Status).
const (
	BackendUpdated    = "updated"
	BackendRolledBack = "rolled_back"
	BackendUnchanged  = "unchanged"
	BackendFailed     = "failed"
)

// backendProbePoll is how often update backends looks at the probes.
const backendProbePoll = 100 * time.Millisecond

// UpdateBackends implements api.Local (`deyroute update backends [name]`,
// section 5; the CLI asks for confirmation first). For every backend (or
// the named one) whose manifest version differs from the version in use:
// the new version is installed next to the old one on the hub and on the
// nodes of its tunnels, every rung is rendered again with it (and
// validated), the active transport of a tunnel restarts when it uses that
// backend, and its probe must turn green within 60 seconds. Otherwise the
// previous version is rendered and restarted again, backend_update_rolled_back
// is emitted and the result carries DEY-S003.
func (l *local) UpdateBackends(ctx context.Context, name string, progress func(api.Step)) ([]api.BackendUpdate, error) {
	h := l.h
	h.ops.updMu.Lock()
	defer h.ops.updMu.Unlock()
	name = strings.TrimSpace(name)
	var names []string
	if name != "" {
		if _, ok := backendByName(name); !ok {
			return nil, withLog(deyerr.New(deyerr.B008, deyerr.Params{"backend": name}))
		}
		names = []string{name}
	} else {
		for _, b := range backend.All() {
			names = append(names, b.Name())
		}
	}
	unlock, err := h.lockOps(ctx)
	if err != nil {
		return nil, withLog(err)
	}
	defer unlock()
	rep := &steps{progress: progress}
	out := make([]api.BackendUpdate, 0, len(names))
	for _, n := range names {
		cur, ok := manifestOf(n)
		if !ok {
			continue
		}
		pin, pinned := h.pinned(n)
		if cur.Builtin || cur.System || cur.Version == "" || !pinned || pin.Version == cur.Version {
			v := cur.Version
			if pinned {
				v = pin.Version
			}
			out = append(out, api.BackendUpdate{Backend: n, From: v, To: v, Status: BackendUnchanged})
			continue
		}
		out = append(out, h.updateBackend(ctx, rep, pin, cur))
	}
	return out, nil
}

// affectedTunnel is a running tunnel whose plan uses the updated backend.
type affectedTunnel struct {
	c      *tunnelCtl
	before state.TunnelState // engine state before the update
	probe  bool              // its active transport uses the backend: probed
}

// updateBackend moves one backend from old to cur (see UpdateBackends).
func (h *Hub) updateBackend(ctx context.Context, rep *steps, old, cur backend.ManifestEntry) api.BackendUpdate {
	name := cur.Name
	res := api.BackendUpdate{Backend: name, From: old.Version, To: cur.Version}
	id := "backend:" + name
	title := i18n.T(i18n.HubTitleBackend, name, old.Version, cur.Version)
	rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepRunning})
	fail := func(status string, err error) api.BackendUpdate {
		res.Status, res.Error = status, api.ToDTO(err)
		st := api.StepFailed
		if status == BackendRolledBack {
			st = api.StepWarn
		}
		rep.emitTitled(api.Step{ID: id, Title: title, Status: st, Error: res.Error})
		return res
	}
	var affected []affectedTunnel
	nodes := map[string]bool{}
	for _, c := range h.tun.all() {
		t, plan := c.snapshot()
		if _, uses := plan.Backends[name]; !uses {
			continue
		}
		for _, n := range t.Nodes {
			nodes[n] = true
		}
		st := c.engineState()
		probe := false
		if runningState(st.State) && st.State != state.StatePaused && c.isStarted(st.Active) {
			if pc, ok := plan.Candidate(st.Active.Node, st.Active.Transport); ok && pc.Backend == name {
				probe = true
			}
		}
		affected = append(affected, affectedTunnel{c: c, before: st, probe: probe})
	}
	// 1. The new version next to the old one, on the hub and the nodes.
	if err := h.installHub(ctx, cur, true); err != nil {
		return fail(BackendFailed, err)
	}
	for _, n := range sortedKeys(nodes) {
		if !h.Online(n) {
			continue // installed when it reconnects
		}
		if err := h.installNode(ctx, n, cur, true); err != nil && !deyerr.HasCode(err, deyerr.N003) {
			return fail(BackendFailed, err)
		}
	}
	// 2. Render with the new version; the active transport restarts.
	h.setTrial(name, &cur)
	for _, a := range affected {
		if err := a.c.update(ctx, updateOpts{restartActive: true, quietInstall: true}); err != nil {
			h.log.Warn("tunnel update with the new backend version finished with errors", dlog.Tunnel(a.c.id), dlog.Err(err))
		}
	}
	since := h.now()
	// 3. The probe of every restarted transport must turn green.
	var failed []string
	for _, a := range affected {
		if a.probe && !h.waitGreen(ctx, a, since) {
			failed = append(failed, a.c.id)
		}
	}
	if len(failed) == 0 {
		h.setPin(name, cur)
		h.setTrial(name, nil)
		if err := (install.Layout{Root: h.o.Root}).RemoveBackendVersions(name, []string{cur.Version, old.Version}); err != nil {
			h.log.Info("older backend versions could not be removed", slog.String("backend", name), dlog.Err(err))
		}
		res.Status = BackendUpdated
		rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepOK})
		h.log.Info("backend updated", slog.String("backend", name), slog.String("from", old.Version), slog.String("to", cur.Version))
		return res
	}
	// 4. Roll back: the previous version is rendered and restarted again.
	h.setTrial(name, nil)
	for _, a := range affected {
		if err := a.c.update(ctx, updateOpts{restartActive: true, quietInstall: true}); err != nil {
			h.log.Warn("tunnel update with the previous backend version finished with errors", dlog.Tunnel(a.c.id), dlog.Err(err))
		}
	}
	reason := "the probe of tunnel " + strings.Join(failed, ", ") + " was not green within " +
		h.o.BackendProbeWait.String() + " after the restart with " + name + " " + cur.Version
	e := deyerr.New(deyerr.S003, deyerr.Params{"component": name + " " + cur.Version, "reason": reason})
	h.log.Warn("backend update rolled back", slog.String("backend", name), slog.String("version", cur.Version),
		dlog.Code(e.Code), slog.String("reason", reason))
	for _, t := range failed {
		h.Emit(state.Event{Type: state.EvBackendRolledBack, Level: state.LevelWarn, Tunnel: t, Code: string(e.Code),
			Reason: reason, Message: "Tunnel " + t + ": " + name + " " + cur.Version + " rolled back to " + old.Version})
	}
	return fail(BackendRolledBack, e)
}

// waitGreen waits until the active candidate of a restarted tunnel passes
// a probe taken after since, while it stays active. It fails when the
// engine moved away from it or BackendProbeWait passed.
func (h *Hub) waitGreen(ctx context.Context, a affectedTunnel, since time.Time) bool {
	deadline := time.NewTimer(h.o.BackendProbeWait)
	defer deadline.Stop()
	tick := time.NewTicker(backendProbePoll)
	defer tick.Stop()
	active := a.before.Active
	for {
		st := a.c.engineState()
		if st.Active != active {
			return false
		}
		if samples, err := h.st.Probes(a.c.id, active.Node, active.Transport); err == nil {
			for i := len(samples) - 1; i >= 0; i-- {
				s := samples[i]
				if s.At.Before(since) {
					break
				}
				if s.OK && s.Kind == ProbeKindPath {
					return true
				}
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}
