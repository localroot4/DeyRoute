package hub

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
)

// local is the hub's Local API (section 14: every CLI command and every
// TUI action is one of its methods).
type local struct {
	h *Hub
}

var _ api.Local = (*local)(nil)

// withLog points an owner-visible error at the hub log (section 13: "Log:
// /var/log/deyroute/hub.log (search DEY-…)").
func withLog(err error) error {
	if err == nil {
		return nil
	}
	e := deyerr.As(err)
	if e.LogPath == "" {
		e.LogPath = LogFile
	}
	return e
}

// Events implements api.Local: events newest first, filtered by tunnel and
// time, at most q.Limit (0 = all).
func (l *local) Events(_ context.Context, q api.EventQuery) ([]state.Event, error) {
	evs, err := l.h.st.Events(state.EventFilter{Tunnel: q.Tunnel, Since: q.Since, Limit: q.Limit})
	if err != nil {
		return nil, withLog(err)
	}
	if evs == nil {
		evs = []state.Event{}
	}
	return evs, nil
}

// NodeSetHub implements api.Local: it is a node command (DEY-X009).
func (l *local) NodeSetHub(context.Context, string) error {
	return withLog(deyerr.New(deyerr.X009, deyerr.Params{"role": config.RoleHub, "need": config.RoleNode}))
}

// Step ids of ConfigApply.
const (
	stepValidate  = "validate"
	stepBackup    = "backup"
	stepApply     = "apply"
	stepReconcile = "reconcile"
)

// stepTitle returns the title of step id: the text of the i18n key
// "hub.step.<id>" (internal/i18n/en.go), or the id itself when there is
// none.
func stepTitle(id string) string {
	k := i18n.Key("hub.step." + id)
	if t := i18n.T(k); t != string(k) {
		return t
	}
	return id
}

// steps reports progress through an optional callback (safe for
// concurrent use).
type steps struct {
	mu       sync.Mutex
	progress func(api.Step)
}

func (s *steps) emit(st api.Step) {
	if s == nil || s.progress == nil {
		return
	}
	st.Title = stepTitle(st.ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress(st)
}

// run reports step id as running, runs fn and reports ok (with fn's
// detail) or failed.
func (s *steps) run(id string, fn func() (detail string, err error)) error {
	s.emit(api.Step{ID: id, Status: api.StepRunning})
	detail, err := fn()
	if err != nil {
		s.emit(api.Step{ID: id, Status: api.StepFailed, Detail: detail, Error: api.ToDTO(err)})
		return err
	}
	s.emit(api.Step{ID: id, Status: api.StepOK, Detail: detail})
	return nil
}

// runWarn is run for steps that can partly fail: fn returns a warning
// (reported as a yellow "warn" step with its DEY code; the operation goes
// on) or an error (a failed step, returned).
func (s *steps) runWarn(id string, fn func() (detail string, warn, err error)) error {
	s.emit(api.Step{ID: id, Status: api.StepRunning})
	detail, warn, err := fn()
	switch {
	case err != nil:
		s.emit(api.Step{ID: id, Status: api.StepFailed, Detail: detail, Error: api.ToDTO(err)})
		return err
	case warn != nil:
		s.emit(api.Step{ID: id, Status: api.StepWarn, Detail: detail, Error: api.ToDTO(warn)})
	default:
		s.emit(api.Step{ID: id, Status: api.StepOK, Detail: detail})
	}
	return nil
}

// emitTitled reports a step whose title is not a fixed step name (the final
// "Tunnel main is UP via backhaul/wssmux (41ms)" line, test-ladder rungs).
func (s *steps) emitTitled(st api.Step) {
	if s == nil || s.progress == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress(st)
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// firstNonEmpty returns the first non-blank value.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
