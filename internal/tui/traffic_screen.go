package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
)

// trafficTarget is one entry of the Traffic picker: a tunnel, a node or
// the hub, with its canonical Traffic target ("tunnel:main", "node:de-1",
// "hub").
type trafficTarget struct {
	target, kind, id, label string
}

// trafficTargets is what the picker loads: the tunnels and the nodes.
type trafficTargets struct {
	tunnels []api.TunnelInfo
	nodes   []api.NodeInfo
}

// trafficPicker lists the tunnels, the nodes and the hub (Diagnostics →
// Traffic and load, and the dashboard key t). A node list that fails only
// leaves the nodes out.
func trafficPicker() *listScreen {
	return &listScreen{
		screenBase: screenBase{title: itemName(i18n.TUIDgTraffic), help: i18n.TUIHelpTraffic},
		intro:      i18n.T(i18n.TUITrafficPick),
		load: func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			ts, err := l.TunnelList(ctx)
			if err != nil {
				return nil, err
			}
			ns, err := l.NodeList(ctx)
			if err != nil {
				ns = nil
			}
			return trafficTargets{tunnels: ts, nodes: ns}, nil
		},
		derive: func(a *app, v any) []choice {
			d, _ := v.(trafficTargets)
			var rows [][]string
			var targets []trafficTarget
			for _, t := range d.tunnels {
				name := t.Name
				if name == t.ID {
					name = ""
				}
				rows = append(rows, []string{i18n.T(i18n.TUITrafficKindTunnel), t.ID, clean(name), a.stateText(t)})
				targets = append(targets, trafficTarget{target: api.TrafficTarget(api.TrafficKindTunnel, t.ID),
					kind: api.TrafficKindTunnel, id: t.ID, label: t.ID})
			}
			for _, n := range d.nodes {
				name := n.Name
				if name == n.ID {
					name = ""
				}
				rows = append(rows, []string{i18n.T(i18n.TUITrafficKindNode), n.ID, clean(name), a.nodeState(n)})
				targets = append(targets, trafficTarget{target: api.TrafficTarget(api.TrafficKindNode, n.ID),
					kind: api.TrafficKindNode, id: n.ID, label: n.ID})
			}
			rows = append(rows, []string{i18n.T(i18n.TUITrafficKindHub), clean(a.status.Name), i18n.T(i18n.TUITrafficThisServer), ""})
			targets = append(targets, trafficTarget{target: api.TrafficTargetHub, kind: api.TrafficKindHub, id: api.TrafficTargetHub,
				label: i18n.T(i18n.TUITrafficKindHub)})
			out := make([]choice, 0, len(rows))
			for i, label := range columns(rows) {
				out = append(out, choice{label: label, value: targets[i]})
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd { return a.push(newTrafficScreen(c.value.(trafficTarget))) },
	}
}

// trafficScreen shows the charts of one tunnel, node or the hub. Keys 1-4
// pick the period (1h, 24h, 7d, 30d), b switches blocks and braille, r
// refreshes; the 1h view refreshes by itself every 2 seconds (not in line
// mode, where r does). Enter does nothing here: in line mode a typed
// period arrives as the digit and Enter.
type trafficScreen struct {
	screenBase
	t       trafficTarget
	period  string
	style   ChartStyle
	rep     *api.TrafficReport
	at      time.Time
	err     error
	loading bool
	seq     int
	msg     string
}

func newTrafficScreen(t trafficTarget) *trafficScreen {
	return &trafficScreen{
		screenBase: screenBase{title: titleOf(i18n.TUIDgTraffic, t.label), help: i18n.TUIHelpTraffic},
		t:          t,
		period:     api.TrafficPeriod1h,
	}
}

func (s *trafficScreen) start(a *app) tea.Cmd { return s.load(a) }

// points is MaxPoints of the query: about one point per chart column (two
// with braille), so the hub's downsampling decides the resolution.
func (s *trafficScreen) points(a *app) int {
	w := a.caps.Width
	if w <= 0 {
		w = chartDefaultWidth
	}
	n := w - chartLabelWidth - 3
	if s.style == ChartBraille && a.caps.Unicode {
		n *= 2
	}
	return min(max(n, TrafficSparkPoints), api.MaxTrafficPoints)
}

// rows is the height of each byte chart: smaller on a short terminal so
// both panels, the connections and the totals fit.
func (s *trafficScreen) rows(a *app) int {
	if !a.sized || a.lineMode {
		return chartDefaultRows
	}
	return min(max((a.height-24)/2, 3), 10)
}

func (s *trafficScreen) load(a *app) tea.Cmd {
	s.loading = true
	s.seq++ // a pending refresh of the old period is dropped
	q := api.TrafficQuery{Targets: []string{s.t.target}, Period: s.period, MaxPoints: s.points(a)}
	return a.call(s, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.Traffic(ctx, q)
	})
}

func (s *trafficScreen) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		p, ok := msg.payload.(donePayload)
		if !ok {
			return nil
		}
		s.loading = false
		s.err = p.err
		if rep, ok := p.v.(api.TrafficReport); ok && p.err == nil {
			s.rep, s.at = &rep, a.opts.Now()
		}
		if s.period != api.TrafficPeriod1h || !retryable(p.err) {
			return nil
		}
		return a.opts.tick(a.opts.Refresh, tickMsg{sid: s.id, seq: s.seq})
	case tickMsg:
		if msg.seq == s.seq {
			return s.load(a)
		}
	case tea.KeyMsg:
		if msg.Type != tea.KeyRunes {
			return nil
		}
		s.msg = ""
		switch k := msg.String(); k {
		case "1", "2", "3", "4":
			s.period = api.TrafficPeriods[int(k[0]-'1')]
			return s.load(a)
		case "b":
			if !a.caps.Unicode {
				s.msg = i18n.T(i18n.TUITrafficNoBraille)
				return nil
			}
			if s.style == ChartBraille {
				s.style = ChartBlocks
			} else {
				s.style = ChartBraille
			}
			return s.load(a)
		case "r":
			return s.load(a)
		case "0":
			return a.pop()
		}
	}
	return nil
}

func (s *trafficScreen) view(a *app) string {
	var b strings.Builder
	b.WriteString(" " + s.header(a) + "\n\n")
	if s.err != nil {
		b.WriteString(a.errBlock(s.err) + "\n")
	}
	switch {
	case s.rep == nil && s.loading:
		b.WriteString(" " + i18n.T(i18n.Loading) + "\n")
	case s.rep != nil:
		ser, ok := seriesOf(s.rep, s.t.kind, s.t.id)
		if !ok && len(s.rep.Series) == 1 {
			ser, ok = s.rep.Series[0], true
		}
		if !ok {
			b.WriteString(indent(i18n.T(i18n.TUITrafficNoData)) + "\n")
			break
		}
		lines := TrafficPanels(a.caps, *s.rep, ser, TrafficView{Loc: a.opts.Location, Style: s.style, Rows: s.rows(a)})
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}
	if s.msg != "" {
		b.WriteString("\n" + a.paint(colYellow, " "+s.msg) + "\n")
	}
	b.WriteString("\n" + a.paint(colGray, " "+trafficText(a.caps, i18n.T(i18n.TUITrafficKeys))) + "\n")
	if s.rep != nil {
		at := s.at.In(a.opts.Location).Format("15:04:05")
		updated := i18n.T(i18n.TUITrafficUpdated, at)
		if s.period == api.TrafficPeriod1h && !a.lineMode {
			updated = i18n.T(i18n.TUIDashUpdated, at, int(a.opts.Refresh/time.Second))
		}
		b.WriteString(a.paint(colGray, " "+updated) + "\n")
	}
	return b.String()
}

// retryable reports whether a refresh may help after err: not after a user
// error (an unknown tunnel) or a daemon without traffic statistics
// (DEY-X008, DEY-X009).
func retryable(err error) bool {
	if err == nil {
		return true
	}
	if deyerr.HasCode(err, deyerr.X008) || deyerr.HasCode(err, deyerr.X009) {
		return false
	}
	return deyerr.As(err).ExitCode() != deyerr.ExitUser
}

// header is "Period: [1h] 24h 7d 30d · Chart: blocks": the current period
// in brackets, so it reads without colour.
func (s *trafficScreen) header(a *app) string {
	ps := make([]string, len(api.TrafficPeriods))
	for i, p := range api.TrafficPeriods {
		ps[i] = p
		if p == s.period {
			ps[i] = "[" + p + "]"
		}
	}
	style := i18n.T(i18n.TUITrafficBlocks)
	if s.style == ChartBraille {
		style = i18n.T(i18n.TUITrafficBraille)
	}
	return trafficText(a.caps, i18n.T(i18n.TUITrafficHeader, strings.Join(ps, " "), style))
}
