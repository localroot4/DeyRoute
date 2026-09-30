// Package notify delivers events to the owner's Telegram chat (spec
// sections 4 and 9): one short plain-text message per event, only for the
// event types selected in hub.notify.telegram.events (aliases such as
// "down" or "switch", or full event names), at most one message per 60
// seconds for each (tunnel, event type).
//
// Telegram is the only outbound message DEYROUTE sends and only when the owner
// enabled it; nothing else ever leaves the servers (section 0, rule 7: no
// telemetry). Because api.telegram.org is often unreachable from Iranian
// datacenters, delivery goes through a chain of Senders: direct HTTPS first,
// then an online node performing the same POST (QUESTIONS.md C.18).
package notify

import (
	"sort"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// DefaultEvents is the alias list used when none is configured (it mirrors
// config.DefaultTelegramEvents).
var DefaultEvents = []string{"down", "switch", "failback", "node_offline"}

// aliases maps each alias of hub.notify.telegram.events to event types.
var aliases = map[string][]string{
	"down":          {state.EvTunnelDown},
	"up":            {state.EvTunnelUp},
	"degraded":      {state.EvTunnelDegraded},
	"switch":        {state.EvSwitchTransport, state.EvSwitchNode},
	"failback":      {state.EvFailback, state.EvFailbackFailed},
	"node_offline":  {state.EvNodeOffline},
	"node_online":   {state.EvNodeOnline},
	"flapping":      {state.EvFlapping},
	"service_down":  {state.EvServiceDown},
	"backend_crash": {state.EvBackendCrash},
	"probe_error":   {state.EvProbeError},
	"update":        {state.EvUpdateApplied, state.EvUpdateRolledBack, state.EvBackendRolledBack},
}

// eventNames are the event types that may also be selected by full name.
var eventNames = []string{
	state.EvTunnelUp, state.EvTunnelDegraded, state.EvTunnelDown,
	state.EvSwitchTransport, state.EvSwitchNode, state.EvFailback,
	state.EvFailbackFailed, state.EvFlapping, state.EvNodeOnline,
	state.EvNodeOffline, state.EvServiceDown, state.EvBackendCrash,
	state.EvProbeError, state.EvUpdateApplied, state.EvUpdateRolledBack,
	state.EvBackendRolledBack, state.EvNodeIPChanged, state.EvACMEFailed,
	state.EvRungSkipped, state.EvRungRestored, state.EvManualSwitch,
	state.EvConfigApplied,
}

// Aliases returns a copy of the alias → event types mapping:
// down → tunnel_down, up → tunnel_up, degraded → tunnel_degraded,
// switch → switch_transport + switch_node, failback → failback +
// failback_failed, node_offline, node_online, flapping, service_down,
// backend_crash, probe_error, update → update_applied + update_rolled_back +
// backend_update_rolled_back.
func Aliases() map[string][]string {
	out := make(map[string][]string, len(aliases))
	for k, v := range aliases {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// EventNames returns every event type that may be selected by its full name,
// sorted.
func EventNames() []string {
	out := append([]string(nil), eventNames...)
	sort.Strings(out)
	return out
}

// Valid returns every accepted entry of events: (aliases and event names),
// sorted and without duplicates.
func Valid() []string {
	seen := make(map[string]bool, len(aliases)+len(eventNames))
	for k := range aliases {
		seen[k] = true
	}
	for _, n := range eventNames {
		seen[n] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Resolve expands aliases and event names (case-insensitive) into the set
// of selected event types. An empty list selects DefaultEvents. Unknown
// entries are reported together as DEY-C013.
func Resolve(names []string) (map[string]bool, error) {
	if len(names) == 0 {
		names = DefaultEvents
	}
	set := make(map[string]bool)
	var errs []error
	for _, raw := range names {
		n := strings.ToLower(strings.TrimSpace(raw))
		if evs, ok := aliases[n]; ok {
			for _, ev := range evs {
				set[ev] = true
			}
			continue
		}
		if isEventName(n) {
			set[n] = true
			continue
		}
		errs = append(errs, deyerr.New(deyerr.C013, deyerr.Params{
			"field":   "hub.notify.telegram.events",
			"value":   raw,
			"allowed": strings.Join(Valid(), ", "),
		}))
	}
	if len(errs) > 0 {
		return nil, deyerr.Join(errs...)
	}
	return set, nil
}

// Validate reports unknown entries of an events list (DEY-C013).
func Validate(names []string) error {
	_, err := Resolve(names)
	return err
}

func isEventName(n string) bool {
	for _, e := range eventNames {
		if e == n {
			return true
		}
	}
	return false
}
