package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

// Automatic update (`deyroute update auto`, on by default): once a day in
// a quiet hour the hub installs the newest release that has been out for a
// day, the nodes follow as after `deyroute update`, and after the restart
// the hub checks that the tunnels and nodes that worked before work again.
// When they do not, or the new binary does not even start, the previous
// binary is put back and that release is never installed automatically.

// Defaults of the automatic update.
const (
	// DefaultAutoUpdateInterval is how often the job looks at the clock.
	DefaultAutoUpdateInterval = 15 * time.Minute
	// DefaultAutoUpdateHour is the local hour of the automatic update.
	DefaultAutoUpdateHour = 4
	// DefaultAutoUpdateMinAge is how long a release must have been out
	// (seen by this hub) before it is installed automatically.
	DefaultAutoUpdateMinAge = 24 * time.Hour
	// DefaultAutoUpdateSettle is the time the tunnels get after the
	// restart before they are judged.
	DefaultAutoUpdateSettle = 3 * time.Minute
	// DefaultAutoUpdateVerify bounds the wait for the tunnels and nodes
	// after an automatic update; then it is rolled back.
	DefaultAutoUpdateVerify = 10 * time.Minute

	// AutoPendingPath records an automatic update until it is verified. It
	// is a plain file, read before anything else at start, so a binary that
	// cannot start is still rolled back.
	AutoPendingPath = "/var/lib/deyroute/update-pending.json"
	// autoMaxStarts is the number of starts the new binary gets before it
	// is rolled back without being verified (systemd restarts a crashing
	// hub every 2 s and gives up after 5 starts in 10 s).
	autoMaxStarts = 2
	// autoCheckEvery is how often the job looks for a new release.
	autoCheckEvery = 6 * time.Hour
	// autoPollEvery is the pace of the verification after the restart.
	autoPollEvery = 15 * time.Second
	// autoKeepSeen bounds the releases remembered by their first sighting.
	autoKeepSeen = 10
	// autoKeepSkipped bounds the rolled back releases remembered.
	autoKeepSkipped = 20
)

// autoUpdateRecord is metaAutoUpdate.
type autoUpdateRecord struct {
	// Disabled turns the automatic update off; the zero value is on.
	Disabled  bool                 `json:"disabled,omitempty"`
	CheckedAt time.Time            `json:"checked_at,omitzero"`
	Seen      map[string]time.Time `json:"seen,omitempty"` // first sighting of each release
	Skipped   []string             `json:"skipped,omitempty"`
	LastDay   string               `json:"last_day,omitempty"` // local day of the last attempt
	Last      string               `json:"last,omitempty"`
	LastAt    time.Time            `json:"last_at,omitzero"`
}

// autoPending is the file AutoPendingPath.
type autoPending struct {
	From    string    `json:"from"`
	To      string    `json:"to"`
	At      time.Time `json:"at"`
	Tunnels []string  `json:"tunnels,omitempty"` // UP before the update
	Nodes   []string  `json:"nodes,omitempty"`   // online before the update
	Starts  int       `json:"starts,omitempty"`  // starts of To so far
	Failed  string    `json:"failed,omitempty"`  // why To was rolled back
}

func readAutoPending(path string) (autoPending, bool, error) {
	var p autoPending
	data, err := os.ReadFile(path) // #nosec G304 -- fixed path below Root
	if errors.Is(err, fs.ErrNotExist) {
		return p, false, nil
	}
	if err != nil {
		return p, false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := json.Unmarshal(data, &p); err != nil || p.To == "" {
		// A damaged record cannot be acted on; it is dropped.
		_ = os.Remove(path)
		return p, false, nil
	}
	return p, true, nil
}

func writeAutoPending(path string, p autoPending) error {
	data, err := json.Marshal(p)
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	if err := os.Rename(tmp, path); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path})
	}
	return nil
}

func sameVersion(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// AutoUpdateStartGuard runs before the hub loads anything: while an
// automatic update to the running version is not verified it counts the
// starts, and once the new binary has started autoMaxStarts times without
// being verified it puts the previous binary back and returns DEY-S003, so
// the service exits and systemd starts the previous version.
func AutoUpdateStartGuard(root string) error {
	path := filepath.Join(root, AutoPendingPath)
	p, ok, err := readAutoPending(path)
	if err != nil || !ok || p.Failed != "" || !sameVersion(p.To, version.Version) {
		return nil
	}
	p.Starts++
	if p.Starts <= autoMaxStarts {
		return writeAutoPending(path, p)
	}
	if err := (install.SelfUpdater{Root: root}).Rollback(); err != nil {
		// Nothing to go back to: keep running this version.
		_ = os.Remove(path)
		return nil
	}
	p.Failed = fmt.Sprintf("deyroute %s did not start (%d attempts)", p.To, p.Starts-1)
	if err := writeAutoPending(path, p); err != nil {
		return err
	}
	return deyerr.New(deyerr.S003, deyerr.Params{"component": "deyroute",
		"reason": p.Failed + "; the previous binary " + p.From + " was put back and starts now"})
}

// autoRecord reads metaAutoUpdate (the zero record when absent).
func (h *Hub) autoRecord() autoUpdateRecord {
	var r autoUpdateRecord
	if _, err := h.st.GetMeta(metaAutoUpdate, &r); err != nil {
		h.log.Warn("cannot read the automatic update record", dlog.Err(err))
	}
	return r
}

// editAutoRecord changes metaAutoUpdate under autoMu.
func (h *Hub) editAutoRecord(edit func(*autoUpdateRecord)) autoUpdateRecord {
	h.ops.autoMu.Lock()
	defer h.ops.autoMu.Unlock()
	r := h.autoRecord()
	edit(&r)
	if err := h.st.PutMeta(metaAutoUpdate, r); err != nil {
		h.log.Warn("cannot record the automatic update", dlog.Err(err))
	}
	return r
}

// skip adds v to the releases never installed automatically.
func (r *autoUpdateRecord) skip(v string) {
	if v == "" || slices.Contains(r.Skipped, v) {
		return
	}
	r.Skipped = append(r.Skipped, v)
	if n := len(r.Skipped); n > autoKeepSkipped {
		r.Skipped = r.Skipped[n-autoKeepSkipped:]
	}
}

// saw records the first sighting of release v.
func (r *autoUpdateRecord) saw(v string, at time.Time) {
	if r.Seen == nil {
		r.Seen = map[string]time.Time{}
	}
	if _, ok := r.Seen[v]; ok {
		return
	}
	r.Seen[v] = at
	for len(r.Seen) > autoKeepSeen {
		oldest := ""
		for k := range r.Seen {
			if oldest == "" || install.NewerThan(oldest, k) {
				oldest = k
			}
		}
		delete(r.Seen, oldest)
	}
}

// candidate is the newest release newer than current that is not skipped,
// with the time it may be installed (first sighting + minAge); the newest
// one already old enough wins over a newer one still waiting.
func (r autoUpdateRecord) candidate(current string, minAge time.Duration, now time.Time) (ready, waiting string, waitUntil time.Time) {
	for v, at := range r.Seen {
		if !install.NewerThan(v, current) || slices.Contains(r.Skipped, v) {
			continue
		}
		if due := at.Add(minAge); !now.Before(due) {
			if ready == "" || install.NewerThan(v, ready) {
				ready = v
			}
		} else if waiting == "" || install.NewerThan(v, waiting) {
			waiting, waitUntil = v, due
		}
	}
	return ready, waiting, waitUntil
}

// autoHour is the local hour of the automatic update (0-23).
func (h *Hub) autoHour() int { return h.o.AutoUpdateHour % 24 }

// nextWindow is the start of the first update hour at or after t.
func (h *Hub) nextWindow(t time.Time) time.Time {
	lt := t.In(h.o.Location)
	w := time.Date(lt.Year(), lt.Month(), lt.Day(), h.autoHour(), 0, 0, 0, h.o.Location)
	if lt.Hour() == h.autoHour() {
		return t
	}
	if !w.After(lt) {
		w = w.AddDate(0, 0, 1)
	}
	return w
}

// autoUpdateLoop finishes an automatic update left by the previous start
// and then installs new releases in the update hour.
func (h *Hub) autoUpdateLoop(ctx context.Context) {
	h.finishAutoUpdate(ctx)
	every(ctx, h.o.JobStartDelay, h.o.AutoUpdateInterval, func() { h.autoUpdateTick(ctx) })
}

// autoUpdateTick checks for a release every autoCheckEvery and installs the
// candidate once a day in the update hour.
func (h *Hub) autoUpdateTick(ctx context.Context) {
	rec := h.autoRecord()
	if rec.Disabled {
		return
	}
	now := h.now()
	if now.Sub(rec.CheckedAt) >= autoCheckEvery {
		info, err := h.checkRelease(ctx)
		if err != nil {
			h.log.Info("release check of the automatic update failed", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		} else {
			rec = h.editAutoRecord(func(r *autoUpdateRecord) {
				r.CheckedAt = now
				if info.Available {
					r.saw(info.Latest, now)
				}
			})
		}
	}
	local := now.In(h.o.Location)
	day := local.Format(time.DateOnly)
	if local.Hour() != h.autoHour() || rec.LastDay == day {
		return
	}
	target, _, _ := rec.candidate(version.Version, h.o.AutoUpdateMinAge, now)
	if target == "" {
		return
	}
	if _, pending, _ := readAutoPending(h.path(AutoPendingPath)); pending {
		return
	}
	h.editAutoRecord(func(r *autoUpdateRecord) { r.LastDay = day })
	h.autoApply(ctx, target)
}

// autoHealthy lists the tunnels UP and the nodes online now.
func (h *Hub) autoHealthy() (tunnels, nodes []string) {
	for _, c := range h.tun.all() {
		if ts, ok := c.liveState(); ok && ts.State == state.StateUp {
			tunnels = append(tunnels, c.id)
		}
	}
	for _, n := range h.Config().Nodes {
		if h.Online(n.ID) {
			nodes = append(nodes, n.ID)
		}
	}
	return tunnels, nodes
}

// autoApply installs target as `deyroute update --version target` does,
// after recording what works now.
func (h *Hub) autoApply(ctx context.Context, target string) {
	path := h.path(AutoPendingPath)
	tunnels, nodes := h.autoHealthy()
	p := autoPending{From: version.Version, To: target, At: h.now(), Tunnels: tunnels, Nodes: nodes}
	if err := writeAutoPending(path, p); err != nil {
		h.log.Warn("automatic update skipped: cannot record it", dlog.Err(err))
		return
	}
	h.log.Info("automatic update starts", slog.String("from", version.Version), slog.String("to", target),
		slog.Int("tunnels_up", len(tunnels)), slog.Int("nodes_online", len(nodes)))
	if _, err := h.updateApply(ctx, target, nil, true); err != nil {
		_ = os.Remove(path)
		e := deyerr.As(err)
		h.editAutoRecord(func(r *autoUpdateRecord) {
			r.Last, r.LastAt = "update to "+target+" failed: "+string(e.Code)+" "+e.Message(), h.now()
		})
		h.log.Warn("automatic update failed; it is tried again tomorrow", slog.String("to", target),
			dlog.Err(err), dlog.Code(e.Code))
		h.Emit(state.Event{Type: state.EvUpdateRolledBack, Level: state.LevelWarn, Code: string(e.Code),
			Message: "automatic update to " + target + " failed (" + e.Message() + "); deyroute " + version.Version + " keeps running"})
	}
}

// finishAutoUpdate settles an automatic update recorded in AutoPendingPath:
// on the new version the tunnels and nodes that worked before must work
// again within AutoUpdateVerify, else the previous binary is put back; on
// any other version (rolled back by the start guard, by this check or by
// the owner) the release is skipped from now on.
func (h *Hub) finishAutoUpdate(ctx context.Context) {
	path := h.path(AutoPendingPath)
	p, ok, err := readAutoPending(path)
	if err != nil {
		h.log.Warn("cannot read the pending automatic update", dlog.Err(err))
		return
	}
	if !ok {
		return
	}
	if !sameVersion(p.To, version.Version) || p.Failed != "" {
		_ = os.Remove(path)
		if !sameVersion(p.From, version.Version) {
			return // another version was installed by hand meanwhile
		}
		reason := firstNonEmpty(p.Failed, "deyroute "+p.To+" was rolled back")
		h.editAutoRecord(func(r *autoUpdateRecord) {
			r.skip(p.To)
			r.Last, r.LastAt = "update to "+p.To+" rolled back: "+reason, h.now()
		})
		e := deyerr.New(deyerr.S003, deyerr.Params{"component": "deyroute", "reason": reason})
		h.log.Warn("automatic update was rolled back; this release is not installed automatically again",
			slog.String("to", p.To), slog.String("reason", reason), dlog.Code(e.Code))
		h.Emit(state.Event{Type: state.EvUpdateRolledBack, Level: state.LevelWarn, Code: string(e.Code),
			Message: "automatic update to " + p.To + " was rolled back (" + reason + "); deyroute " + version.Version +
				" runs again and " + p.To + " is not installed automatically"})
		return
	}
	missing, err := h.autoVerify(ctx, p)
	if err != nil {
		return // the hub stops; the next start verifies again
	}
	if len(missing) == 0 {
		_ = os.Remove(path)
		h.editAutoRecord(func(r *autoUpdateRecord) {
			r.Last, r.LastAt = fmt.Sprintf("updated %s → %s; %d tunnels and %d nodes work", p.From, p.To, len(p.Tunnels), len(p.Nodes)), h.now()
		})
		h.log.Info("automatic update verified", slog.String("from", p.From), slog.String("to", p.To))
		h.Emit(state.Event{Type: state.EvUpdateApplied, Level: state.LevelInfo,
			Message: "automatic update " + p.From + " → " + p.To + " verified: the tunnels and nodes work"})
		return
	}
	h.autoRollback(path, p, "after the update these did not come back: "+strings.Join(missing, ", "))
}

// autoVerify waits until every tunnel and node of p works again, at least
// AutoUpdateSettle and at most AutoUpdateVerify after the start; it returns
// what still does not work.
func (h *Hub) autoVerify(ctx context.Context, p autoPending) ([]string, error) {
	if err := h.waitTunnelsReady(ctx); err != nil {
		return nil, err
	}
	start := h.o.Now()
	if err := sleepCtx(ctx, h.o.AutoUpdateSettle); err != nil {
		return nil, err
	}
	for {
		missing := autoMissing(p, h.autoHealthy)
		if len(missing) == 0 || h.o.Now().Sub(start) >= h.o.AutoUpdateVerify {
			return missing, nil
		}
		if err := sleepCtx(ctx, min(autoPollEvery, h.o.AutoUpdateVerify)); err != nil {
			return nil, err
		}
	}
}

// autoMissing names the tunnels and nodes of p that do not work now.
func autoMissing(p autoPending, healthy func() (tunnels, nodes []string)) []string {
	tunnels, nodes := healthy()
	var out []string
	for _, t := range p.Tunnels {
		if !slices.Contains(tunnels, t) {
			out = append(out, "tunnel "+t)
		}
	}
	for _, n := range p.Nodes {
		if !slices.Contains(nodes, n) {
			out = append(out, "node "+n)
		}
	}
	return out
}

// autoRollback puts the previous binary back after a failed verification
// and restarts the hub; the next start records the outcome.
func (h *Hub) autoRollback(path string, p autoPending, reason string) {
	h.ops.updMu.Lock()
	defer h.ops.updMu.Unlock()
	if err := (install.SelfUpdater{Root: h.o.Root}).Rollback(); err != nil {
		_ = os.Remove(path)
		h.editAutoRecord(func(r *autoUpdateRecord) {
			r.skip(p.To)
			r.Last, r.LastAt = "update to "+p.To+" did not verify ("+reason+") and cannot be rolled back", h.now()
		})
		h.log.Error("automatic update did not verify and cannot be rolled back", slog.String("reason", reason),
			dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	p.Failed = reason
	if err := writeAutoPending(path, p); err != nil {
		h.log.Warn("cannot record the automatic rollback", dlog.Err(err))
	}
	if err := h.st.PutMeta(metaUpdatePrevious, version.Version); err != nil {
		h.log.Warn("cannot record the replaced version", dlog.Err(err))
	}
	h.markNodesForUpdate()
	h.log.Warn("automatic update did not verify; rolling back and restarting the hub",
		slog.String("from", p.To), slog.String("to", p.From), slog.String("reason", reason))
	h.scheduleRestart()
}

// UpdateAuto implements api.Local (`deyroute update auto [on|off]`).
func (l *local) UpdateAuto(_ context.Context, mode string) (api.AutoUpdateInfo, error) {
	h := l.h
	var rec autoUpdateRecord
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		rec = h.autoRecord()
	case "on":
		rec = h.editAutoRecord(func(r *autoUpdateRecord) { r.Disabled = false })
		h.log.Info("automatic update turned on")
	case "off":
		rec = h.editAutoRecord(func(r *autoUpdateRecord) { r.Disabled = true })
		h.log.Info("automatic update turned off")
	default:
		return api.AutoUpdateInfo{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "update auto", "value": mode, "allowed": "on, off",
		}))
	}
	return h.autoInfo(rec), nil
}

// autoInfo is the answer of UpdateAuto.
func (h *Hub) autoInfo(rec autoUpdateRecord) api.AutoUpdateInfo {
	now := h.now()
	info := api.AutoUpdateInfo{
		Enabled: !rec.Disabled, Hour: h.autoHour(), MinAgeHours: int(h.o.AutoUpdateMinAge / time.Hour),
		Current: version.Version, Last: rec.Last, LastAt: rec.LastAt, Skipped: rec.Skipped,
	}
	if p, ok, _ := readAutoPending(h.path(AutoPendingPath)); ok && p.Failed == "" {
		info.Pending = p.To
	}
	ready, waiting, until := rec.candidate(version.Version, h.o.AutoUpdateMinAge, now)
	switch {
	case ready != "":
		info.Next, info.NextAt = ready, h.nextWindow(now)
		if day := now.In(h.o.Location).Format(time.DateOnly); rec.LastDay == day && info.NextAt.Equal(now) {
			info.NextAt = h.nextWindow(now.Add(time.Hour))
		}
	case waiting != "":
		info.Next, info.NextAt = waiting, h.nextWindow(until)
	}
	return info
}

// updateWarnings are the dashboard lines of updates: a release that is
// newer than this hub when the automatic update will not install it (off,
// or the release was rolled back), and the last automatic rollback.
func (h *Hub) updateWarnings() []api.Warning {
	var out []api.Warning
	rec := h.autoRecord()
	var upd api.UpdateInfo
	if ok, err := h.st.GetMeta(metaUpdateCheck, &upd); err == nil && ok && install.NewerThan(upd.Latest, version.Version) &&
		(rec.Disabled || slices.Contains(rec.Skipped, upd.Latest)) {
		out = append(out, api.Warning{Message: "deyroute " + upd.Latest + " is available (running " + version.Version + "): run deyroute update"})
	}
	if strings.Contains(rec.Last, "rolled back") && h.now().Sub(rec.LastAt) < 7*24*time.Hour {
		out = append(out, api.Warning{Code: string(deyerr.S003),
			Message: "automatic " + rec.Last + "; see deyroute update auto"})
	}
	return out
}
