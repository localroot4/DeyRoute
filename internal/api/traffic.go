package api

import (
	"slices"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// trafficPeriodLen maps each TrafficQuery period to its length.
var trafficPeriodLen = map[string]time.Duration{
	TrafficPeriod1h:  time.Hour,
	TrafficPeriod24h: 24 * time.Hour,
	TrafficPeriod7d:  7 * 24 * time.Hour,
	TrafficPeriod30d: 30 * 24 * time.Hour,
}

// TrafficPeriodDuration returns the length of period p (1h, 24h, 7d, 30d).
func TrafficPeriodDuration(p string) (time.Duration, bool) {
	d, ok := trafficPeriodLen[p]
	return d, ok
}

// ParseTrafficTarget splits a Traffic target into its kind and id: "hub" is
// (hub, "hub"), "node:<id>" is (node, id), "tunnel:<id>" and a bare id are
// (tunnel, id). ok is false when the id is not a valid id (section 4:
// [a-z0-9-]{2,32}). Whether the tunnel or node exists is the hub's check.
func ParseTrafficTarget(s string) (kind, id string, ok bool) {
	s = strings.TrimSpace(s)
	switch {
	case s == TrafficTargetHub:
		return TrafficKindHub, TrafficTargetHub, true
	case strings.HasPrefix(s, TrafficNodePrefix):
		kind, id = TrafficKindNode, strings.TrimPrefix(s, TrafficNodePrefix)
	case strings.HasPrefix(s, TrafficTunnelPrefix):
		kind, id = TrafficKindTunnel, strings.TrimPrefix(s, TrafficTunnelPrefix)
	default:
		kind, id = TrafficKindTunnel, s
	}
	if !config.ValidID(id) {
		return "", "", false
	}
	return kind, id, true
}

// TrafficTarget returns the canonical target of a series: "hub",
// "node:<id>" or "tunnel:<id>".
func TrafficTarget(kind, id string) string {
	switch kind {
	case TrafficKindHub:
		return TrafficTargetHub
	case TrafficKindNode:
		return TrafficNodePrefix + id
	}
	return TrafficTunnelPrefix + id
}

// Normalize returns q with its defaults applied: period 1h, MaxPoints
// DefaultTrafficPoints (at most MaxTrafficPoints), targets in canonical form
// ("tunnel:<id>", "node:<id>", "hub") without duplicates. An unknown period
// or a malformed target is DEY-C027 (a user error).
func (q TrafficQuery) Normalize() (TrafficQuery, error) {
	out := TrafficQuery{Period: strings.TrimSpace(q.Period), MaxPoints: q.MaxPoints}
	if out.Period == "" {
		out.Period = TrafficPeriod1h
	}
	if _, ok := trafficPeriodLen[out.Period]; !ok {
		return TrafficQuery{}, deyerr.New(deyerr.C027, deyerr.Params{
			"field": "period", "value": q.Period, "allowed": strings.Join(TrafficPeriods, ", "),
		})
	}
	switch {
	case out.MaxPoints <= 0:
		out.MaxPoints = DefaultTrafficPoints
	case out.MaxPoints > MaxTrafficPoints:
		out.MaxPoints = MaxTrafficPoints
	}
	for _, t := range q.Targets {
		kind, id, ok := ParseTrafficTarget(t)
		if !ok {
			return TrafficQuery{}, deyerr.New(deyerr.C027, deyerr.Params{
				"field": "target", "value": t, "allowed": "a tunnel id, tunnel:<id>, node:<id> or hub",
			})
		}
		if c := TrafficTarget(kind, id); !slices.Contains(out.Targets, c) {
			out.Targets = append(out.Targets, c)
		}
	}
	return out, nil
}
