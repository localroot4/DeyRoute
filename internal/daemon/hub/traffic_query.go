package hub

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// Traffic implements api.Local (`deyroute stats`, the Traffic screen; docs/
// cli-json.md): the series of the requested tunnels, nodes and the hub for
// one period, downsampled to at most MaxPoints points, plus the byte totals
// of every tunnel. The 1 h period comes from memory only (the dashboard
// asks every 2 s); 24 h reads the 1-minute points of state.db (the
// half-hour points while the size guard keeps only 6 hours of them), 7 d
// and 30 d the half-hour points. An unknown period or target is DEY-C027.
func (l *local) Traffic(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
	h := l.h
	q, err := q.Normalize()
	if err != nil {
		return api.TrafficReport{}, err
	}
	cfg := h.Config()
	targets := q.Targets
	if len(targets) == 0 {
		for i := range cfg.Tunnels {
			targets = append(targets, api.TrafficTarget(api.TrafficKindTunnel, cfg.Tunnels[i].ID))
		}
	}
	type target struct{ kind, id, name string }
	resolved := make([]target, 0, len(targets))
	for _, raw := range targets {
		kind, id, _ := api.ParseTrafficTarget(raw)
		t := target{kind: kind, id: id}
		switch kind {
		case api.TrafficKindTunnel:
			tun, ok := cfg.Tunnel(id)
			if !ok {
				return api.TrafficReport{}, unknownTarget(raw, cfg)
			}
			t.name = tun.Name
		case api.TrafficKindNode:
			n, ok := cfg.NodeByID(id)
			if !ok {
				return api.TrafficReport{}, unknownTarget(raw, cfg)
			}
			t.name = n.Name
		default:
			t.name = cfg.Hub.Name
		}
		resolved = append(resolved, t)
	}

	s := h.traffic
	now := h.now()
	avail, reason := s.availability(h.o.DisableStats)
	rep := api.TrafficReport{
		GeneratedAt: now,
		Period:      q.Period,
		Available:   avail,
		Reason:      api.ToDTO(reason),
		Timezone:    zoneLabel(h.o.Location, now),
		Series:      make([]api.TrafficSeries, 0, len(resolved)),
	}
	length, _ := api.TrafficPeriodDuration(q.Period)
	for _, t := range resolved {
		ser := api.TrafficSeries{ID: t.id, Kind: t.kind, Name: t.name, Available: true, Points: []api.TrafficPoint{}}
		if t.kind == api.TrafficKindTunnel {
			ser.Available, ser.Reason = avail, api.ToDTO(reason)
		}
		pts, base, err := s.points(t.kind, t.id, q.Period, length, now)
		if err != nil {
			return api.TrafficReport{}, withLog(err)
		}
		ser.StepS, ser.Points = downsample(pts, t.kind != api.TrafficKindTunnel, base, length, q.MaxPoints, now)
		if t.kind == api.TrafficKindTunnel {
			if tun, ok := cfg.Tunnel(t.id); ok {
				ser.Totals = s.totals(cfg, tun, now)
			}
		}
		rep.Series = append(rep.Series, ser)
	}
	return rep, nil
}

// unknownTarget is DEY-C027 for a target that names no tunnel or node.
func unknownTarget(raw string, cfg *config.Config) error {
	var ids []string
	for i := range cfg.Tunnels {
		ids = append(ids, cfg.Tunnels[i].ID)
	}
	for _, n := range cfg.Nodes {
		ids = append(ids, api.TrafficNodePrefix+n.ID)
	}
	ids = append(ids, api.TrafficTargetHub)
	return deyerr.New(deyerr.C027, deyerr.Params{"field": "target", "value": raw, "allowed": strings.Join(ids, ", ")})
}

// zoneLabel names the hub-local zone: its IANA name, or "UTC+03:30" when
// the process only knows it as Local.
func zoneLabel(loc *time.Location, now time.Time) string {
	if loc == nil {
		loc = time.Local
	}
	if name := loc.String(); name != "" && name != "Local" {
		return name
	}
	_, off := now.In(loc).Zone()
	if off == 0 {
		return "UTC"
	}
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return "UTC" + sign + pad2(off/3600) + ":" + pad2(off%3600/60)
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// availability reports whether bytes are counted, and why not.
func (s *trafficSampler) availability(disabled bool) (bool, error) {
	if disabled {
		return false, deyerr.New(deyerr.X061, deyerr.Params{"reason": "traffic accounting is turned off for this hub process"})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !s.started:
		return false, deyerr.New(deyerr.X061, deyerr.Params{"reason": "the hub has not sampled the counters yet"})
	case !s.enabled:
		return false, monitoringOffErr()
	case !s.avail:
		if s.availErr != nil {
			return false, s.availErr
		}
		return false, deyerr.New(deyerr.X061, deyerr.Params{"reason": "the accounting table is not built yet"})
	}
	return true, nil
}

// points returns the raw points of one series for a period, oldest first,
// and their base step: the live ring for 1 h (never state.db), the
// 1-minute tier for 24 h (the half-hour tier while the size guard keeps
// only 6 hours of 1-minute points) and the half-hour tier for 7 d and 30 d.
func (s *trafficSampler) points(kind, id, period string, length time.Duration, now time.Time) ([]livePoint, time.Duration, error) {
	name := state.SeriesHub
	switch kind {
	case api.TrafficKindTunnel:
		name = state.TunnelSeriesName(id)
	case api.TrafficKindNode:
		name = state.NodeSeriesName(id)
	}
	if period == api.TrafficPeriod1h {
		s.mu.Lock()
		defer s.mu.Unlock()
		return append([]livePoint(nil), s.rings[name]...), s.d.interval, nil
	}
	tier, base := state.TierM30, state.StepM30
	s.mu.Lock()
	over := s.guardOver
	s.mu.Unlock()
	if period == api.TrafficPeriod24h && !over {
		tier, base = state.TierM1, state.StepM1
	}
	from := now.Add(-length - base)
	var out []livePoint
	if kind == api.TrafficKindTunnel {
		pts, err := s.d.store.TunnelPoints(id, tier, from, time.Time{})
		if err != nil {
			return nil, 0, err
		}
		for _, p := range pts {
			out = append(out, livePoint{start: p.Start, secs: p.Secs, flags: p.Flags, in: p.In, out: p.Out, conns: p.Conns})
		}
		return out, base, nil
	}
	node := ""
	if kind == api.TrafficKindNode {
		node = id
	}
	pts, err := s.d.store.HostPoints(node, tier, from, time.Time{})
	if err != nil {
		return nil, 0, err
	}
	for _, p := range pts {
		out = append(out, livePoint{start: p.Start, secs: p.Secs, flags: p.Flags, cpu: p.CPUPermille, cpuMax: p.CPUMaxPermille, ram: p.RAM})
	}
	return out, base, nil
}

// downsample turns raw points into at most maxPoints buckets of the
// period ending at the last closed bucket before now. The step is a whole
// multiple of base, buckets are aligned to multiples of the step since the
// epoch, the open bucket is left out. A bucket sums the bytes, takes the
// highest connection count, CPU and RAM, and is a gap when it has no point
// or any point is a gap or a reset (its bytes stay).
func downsample(pts []livePoint, host bool, base, length time.Duration, maxPoints int, now time.Time) (int, []api.TrafficPoint) {
	baseS := max(int64(base/time.Second), 1)
	n := max(int64(length/time.Second)/baseS, 1)
	maxP := int64(max(maxPoints, 1))
	factor := (n + maxP - 1) / maxP
	step := baseS * factor
	count := (n + factor - 1) / factor
	end := floorDiv(now.Unix(), step) * step
	start := end - count*step
	out := make([]api.TrafficPoint, count)
	seen := make([]bool, count)
	for i := range out {
		out[i] = api.TrafficPoint{At: time.Unix(start+int64(i)*step, 0).UTC(), Gap: true}
	}
	for _, p := range pts {
		at := p.start.Unix()
		if at < start || at >= end {
			continue
		}
		i := (at - start) / step
		o := &out[i]
		if !seen[i] {
			seen[i], o.Gap = true, false
		}
		if p.flags&(state.FlagGap|state.FlagReset) != 0 {
			o.Gap = true
		}
		if host {
			o.CPUPercent = max(o.CPUPercent, sanitize(float64(p.cpu)/10))
			o.RAMBytes = max(o.RAMBytes, p.ram)
			continue
		}
		o.BytesIn, o.BytesOut = satAdd(o.BytesIn, p.in), satAdd(o.BytesOut, p.out)
		if p.flags&state.FlagConnsUnknown == 0 && p.flags&state.FlagGap == 0 {
			c := int(p.conns)
			if o.Conns == nil || *o.Conns < c {
				o.Conns = &c
			}
		}
	}
	return int(step), out
}

// totals are the byte totals of a tunnel from memory: today since 00:00
// hub-local, the last 30 days and the current quota period.
func (s *trafficSampler) totals(cfg *config.Config, t *config.Tunnel, now time.Time) *api.TrafficTotals {
	loc := s.d.loc
	lt := now.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	period := state.PeriodStart(now, cfg.QuotaResetDay(), loc)
	tt := &api.TrafficTotals{TodayStart: today.UTC(), PeriodStart: period.UTC(), QuotaBytes: t.QuotaBytes()}
	s.mu.Lock()
	defer s.mu.Unlock()
	tt.TodayIn, tt.TodayOut = s.sumSinceLocked(t.ID, today, now)
	tt.Days30In, tt.Days30Out = s.sumSinceLocked(t.ID, now.Add(-30*24*time.Hour), now)
	tt.PeriodIn, tt.PeriodOut = s.sumSinceLocked(t.ID, period, now)
	return tt
}

// tunnelNow is TunnelInfo.Traffic: the rate of the latest sample and
// today's bytes, from memory only (Status never reads state.db for it).
// nil while monitoring is off, the sampler did not run or the tunnel has
// no sample yet.
func (s *trafficSampler) tunnelNow(id string, now time.Time) *api.TrafficNow {
	if s == nil {
		return nil
	}
	loc := s.d.loc
	lt := now.In(loc)
	today := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, loc)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || !s.enabled {
		return nil
	}
	r, ok := s.live[id]
	if !ok {
		return nil
	}
	tn := &api.TrafficNow{At: r.at, Available: s.avail}
	if s.avail {
		tn.TodayIn, tn.TodayOut = s.sumSinceLocked(id, today, now)
		if r.measured && now.Sub(r.at) <= 3*s.d.interval {
			tn.RateInBitS, tn.RateOutBitS = uint64(sanitize(r.in)), uint64(sanitize(r.out))
		}
	}
	return tn
}

// overlayMetrics puts the current byte counters of a tunnel into its
// stored metrics record (`tunnel show`: the record is up to 30 s old).
func (s *trafficSampler) overlayMetrics(id string, m *state.Metrics) *state.Metrics {
	c, ok := s.counter(id)
	if !ok {
		return m
	}
	if m == nil {
		m = &state.Metrics{ConnsUnknown: true}
	}
	m.BytesIn, m.BytesOut, m.BytesSince, m.Source = c.In, c.Out, c.Since, state.MetricsSourceNFT
	return m
}
