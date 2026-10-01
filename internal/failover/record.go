package failover

import (
	"fmt"
	"reflect"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// setState changes the state and its cause; leaving UP ends the stable
// period used by failback.
func (e *Engine) setState(s, cause string) {
	if s != state.StateUp {
		e.st.StableSince = time.Time{}
	}
	e.st.State = s
	e.st.TransitionCause = cause
}

// commit publishes the state to State() and persists it when forced, when
// something significant changed, when the previous Persist failed, or at
// least every PersistEvery.
func (e *Engine) commit(force bool) {
	now := e.clk.Now()
	e.st.ID = e.id
	e.st.UpdatedAt = now.UTC()
	snap := cloneState(e.st)
	e.mu.Lock()
	e.snap = snap
	e.mu.Unlock()
	if !force && !e.dirty && !significantChange(e.lastSaved, snap) && now.Sub(e.lastSaveAt) < PersistEvery {
		return
	}
	e.lastSaved = cloneState(snap)
	e.lastSaveAt = now
	// The Actions implementation logs persistence failures; the engine keeps
	// going and retries on the next commit.
	e.dirty = e.a.Persist(snap) != nil
}

// significantChange ignores the fields that change on every probe.
func significantChange(a, b state.TunnelState) bool {
	strip := func(s state.TunnelState) state.TunnelState {
		s.LastRTTms, s.LastProbeErr, s.RecoverCount, s.CanaryPasses = 0, "", 0, 0
		s.UpdatedAt = time.Time{}
		if len(s.Quarantine) == 0 {
			s.Quarantine = nil
		}
		if len(s.Skipped) == 0 {
			s.Skipped = nil
		}
		return s
	}
	return !reflect.DeepEqual(strip(a), strip(b))
}

// cloneState deep-copies a tunnel state.
func cloneState(s state.TunnelState) state.TunnelState {
	c := s
	if s.SwitchTimes != nil {
		c.SwitchTimes = append([]time.Time(nil), s.SwitchTimes...)
	}
	if s.Tried != nil {
		c.Tried = append([]string(nil), s.Tried...)
	}
	if s.Quarantine != nil {
		c.Quarantine = make(map[string]state.Quarantine, len(s.Quarantine))
		for k, v := range s.Quarantine {
			c.Quarantine[k] = v
		}
	}
	if s.Skipped != nil {
		c.Skipped = make(map[string]state.Skip, len(s.Skipped))
		for k, v := range s.Skipped {
			c.Skipped[k] = v
		}
	}
	return c
}

// evt describes one event to emit.
type evt struct {
	typ    string
	level  string
	from   state.Candidate
	to     state.Candidate
	reason string
	err    *deyerr.Error // code and message, when the event has a DEY code
}

// emit publishes an event with the fixed section 9 fields.
func (e *Engine) emit(v evt) {
	node := v.to.Node
	if node == "" {
		node = v.from.Node
	}
	ev := state.Event{
		At:            e.clk.Now().UTC(),
		Level:         v.level,
		Type:          v.typ,
		Tunnel:        e.id,
		Node:          node,
		FromTransport: v.from.Transport,
		ToTransport:   v.to.Transport,
		FromNode:      v.from.Node,
		ToNode:        v.to.Node,
		Reason:        v.reason,
	}
	if v.err != nil {
		// Copy: the error may belong to the Actions implementation.
		ce := *v.err
		ce.Params = deyerr.Params{"tunnel": e.id}
		for k, val := range v.err.Params {
			ce.Params[k] = val
		}
		ev.Code = string(ce.Code)
		ev.Message = ce.Message()
	} else {
		ev.Message = eventMessage(e.id, v)
	}
	e.a.Emit(ev)
}

// eventMessage is the one-line summary of an event without a DEY code.
func eventMessage(tunnel string, v evt) string {
	switch v.typ {
	case state.EvTunnelUp:
		return fmt.Sprintf("Tunnel %s is up on %s", tunnel, v.to.Key())
	case state.EvTunnelDegraded:
		return fmt.Sprintf("Tunnel %s is degraded on %s", tunnel, v.to.Key())
	case state.EvSwitchTransport:
		return fmt.Sprintf("Tunnel %s switched transport %s -> %s on node %s", tunnel, v.from.Transport, v.to.Transport, v.to.Node)
	case state.EvSwitchNode:
		return fmt.Sprintf("Tunnel %s switched node %s -> %s (%s)", tunnel, v.from.Node, v.to.Node, v.to.Transport)
	case state.EvFailback:
		return fmt.Sprintf("Tunnel %s failed back to %s", tunnel, v.to.Key())
	}
	return fmt.Sprintf("Tunnel %s: %s", tunnel, v.typ)
}
