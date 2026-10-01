package tui

import (
	"context"
	stderrors "errors"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// Log view limits.
const (
	logBacklog  = 200  // lines requested before following
	logKeep     = 2000 // lines kept in memory
	defaultSecs = 10   // speed test duration
)

// ---- 6 Diagnostics

func diagMenu(a *app) screen {
	sub := func(k i18n.Key) string { return subTitle(i18n.MenuDiagnostics, k) }
	// A node has its logs and the doctor; the checks run from the hub.
	return newMenu(a, i18n.MenuDiagnostics, i18n.TUIHelpDiag, []menuItem{
		{label: i18n.TUIDgPortCheck, role: roleHub, act: func(a *app) tea.Cmd { return a.push(portCheckForm(a)) }},
		{label: i18n.TUIDgTunnelTest, role: roleHub, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIDgTunnelTest), func(a *app, t api.TunnelInfo) tea.Cmd {
				return a.push(probeTunnel(t.ID))
			}))
		}},
		{label: i18n.TUIDgSpeed, role: roleHub, act: func(a *app) tea.Cmd {
			return a.push(pickTunnel(sub(i18n.TUIDgSpeed), speedTest))
		}},
		{label: i18n.TUIDgLogs, act: func(a *app) tea.Cmd { return a.push(pickLog(a)) }},
		{label: i18n.TUIDgDoctor, act: func(a *app) tea.Cmd { return a.push(doctorTask(a)) }},
	})
}

// probeTunnel runs DiagProbe on every port of a tunnel (refreshable).
func probeTunnel(id string) *taskScreen {
	t := newTask(titleOf(i18n.TUIDgTunnelTest, id), checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.DiagProbe(ctx, id, true)
	}, func(a *app, v any) string {
		rs, _ := v.([]api.ProbeReport)
		if len(rs) == 0 {
			return indent(i18n.T(i18n.TUIProbeNone)) + "\n"
		}
		s := a.sym()
		var b strings.Builder
		for _, r := range rs {
			spec := strconv.Itoa(r.Port) + "/" + r.Proto
			res := a.paint(colGreen, s.ok) + " " + ms(r.RTTms)
			if !r.OK {
				res = a.paint(colRed, s.fail+" "+r.Error)
			}
			b.WriteString("  " + pad(spec, 12) + pad(r.Kind, 7) + res + "\n")
		}
		return b.String()
	})
	t.refreshable = true
	return t
}

func speedTest(a *app, t api.TunnelInfo) tea.Cmd {
	id := t.ID
	title := titleOf(i18n.TUIDgSpeed, id)
	run := func(secs int) *taskScreen {
		t := newTask(title, longTimeout, func(ctx context.Context, l api.Local, progress func(api.Step)) (any, error) {
			return l.DiagSpeed(ctx, id, secs, progress)
		}, func(a *app, v any) string {
			r, _ := v.(api.SpeedResult)
			return " " + i18n.T(i18n.TUISpResult, r.DownloadMbps, r.UploadMbps, ms(r.RTTms)) + "\n" +
				a.paint(colGray, " "+i18n.T(i18n.TUISpVia, r.Transport, r.Seconds)) + "\n"
		})
		t.cancellable = true // a measurement: stopping it early changes nothing
		return t
	}
	if !a.advanced {
		return a.push(run(defaultSecs))
	}
	return a.push(newForm(title, "", []field{{key: "secs", label: i18n.T(i18n.TUISpSeconds), def: strconv.Itoa(defaultSecs), check: checkInt(1)}},
		func(a *app, v map[string]string) tea.Cmd { return a.replace(run(atoi(v["secs"]))) }))
}

// pickLog chooses the log stream: the service of this role or a tunnel.
func pickLog(a *app) *listScreen {
	role := a.status.Role
	return &listScreen{
		screenBase: screenBase{title: i18n.T(i18n.TUIDgLogs)},
		intro:      i18n.T(i18n.TUILogTarget),
		load: func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			ts, err := l.TunnelList(ctx)
			if err != nil && role != "hub" {
				return []api.TunnelInfo(nil), nil // a node has no tunnel list
			}
			return ts, err
		},
		derive: func(a *app, v any) []choice {
			var out []choice
			if role != "node" {
				out = append(out, choice{label: i18n.T(i18n.TUILogHub), value: "hub"})
			}
			if role != "hub" {
				out = append(out, choice{label: i18n.T(i18n.TUILogNode), value: "node"})
			}
			ts, _ := v.([]api.TunnelInfo)
			for _, t := range ts {
				out = append(out, choice{label: i18n.T(i18n.TUILogTunnel, t.ID), value: t.ID})
			}
			return out
		},
		pick: func(a *app, c choice) tea.Cmd { return a.push(newLogs(c.value.(string))) },
	}
}

// logsScreen streams Logs (follow) into a scrollable view; q stops it.
type logsScreen struct {
	screenBase
	target string
	lines  []api.LogLine
	off    int // lines scrolled up from the live bottom
	err    error
	ended  bool
}

func newLogs(target string) *logsScreen {
	return &logsScreen{screenBase: screenBase{title: titleOf(i18n.TUIDgLogs, target), help: i18n.TUIHelpLogs}, target: target}
}

func (s *logsScreen) start(a *app) tea.Cmd {
	s.lines, s.off, s.err, s.ended = nil, 0, nil, false
	q := api.LogQuery{Target: s.target, Follow: true, Lines: logBacklog}
	l, derr := a.opts.Local, a.daemonErr()
	return a.run(s, 0, func(ctx context.Context, send func(any)) (any, error) {
		if l == nil {
			return nil, derr
		}
		return nil, l.Logs(ctx, q, func(ll api.LogLine) error {
			send(logPayload(ll))
			return ctx.Err()
		})
	})
}

func (s *logsScreen) page(a *app) int { return max(5, a.height-14) }

func (s *logsScreen) update(a *app, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case asyncMsg:
		switch p := msg.payload.(type) {
		case logPayload:
			s.lines = append(s.lines, api.LogLine(p))
			if n := len(s.lines) - logKeep; n > 0 {
				s.lines = s.lines[n:]
			}
			if s.off > 0 {
				s.off++ // keep the scrolled window still
			}
		case donePayload:
			s.ended = true
			if p.err != nil && !stderrors.Is(p.err, context.Canceled) {
				s.err = p.err
			}
		}
	case tea.KeyMsg:
		maxOff := max(0, len(s.lines)-s.page(a))
		switch msg.String() {
		case "up", "k":
			s.off = min(maxOff, s.off+1)
		case "down", "j":
			s.off = max(0, s.off-1)
		case "pgup":
			s.off = min(maxOff, s.off+s.page(a))
		case "pgdown":
			s.off = max(0, s.off-s.page(a))
		case "home", "g":
			s.off = maxOff
		case "end", "G":
			s.off = 0
		case "r":
			return s.start(a)
		case "enter", "0":
			return a.pop()
		}
	}
	return nil
}

func (s *logsScreen) view(a *app) string {
	var b strings.Builder
	page := s.page(a)
	end := max(0, len(s.lines)-s.off)
	begin := max(0, end-page)
	if len(s.lines) == 0 && !s.ended {
		b.WriteString(" " + i18n.T(i18n.TUILogEmpty) + "\n")
	}
	for _, l := range s.lines[begin:end] {
		line := clean(l.Line)
		if l.Source != "" && s.target != "hub" && s.target != "node" {
			line = pad(clean(l.Source), 5) + line
		}
		b.WriteString(a.clip(" "+line) + "\n")
	}
	b.WriteString("\n")
	if s.err != nil {
		b.WriteString(a.errBlock(s.err))
	}
	switch {
	case s.ended:
		b.WriteString(a.paint(colGray, " "+i18n.T(i18n.TUILogEnded)) + "\n")
	case s.off == 0:
		b.WriteString(a.paint(colGray, " "+i18n.T(i18n.TUILogFollow)) + "\n")
	default:
		b.WriteString(a.paint(colGray, " "+i18n.T(i18n.TUILogScrolled, begin+1, end, len(s.lines))) + "\n")
	}
	return b.String()
}

// doctorTask runs the doctor provided by the cli package.
func doctorTask(a *app) *taskScreen {
	title := i18n.T(i18n.TUIDgDoctor)
	fn := a.opts.Doctor
	t := newLocalTask(title, func(ctx context.Context) (any, error) {
		if fn == nil {
			return nil, uiErr(i18n.TUINotAvailable)
		}
		summary, path, err := fn(ctx)
		if err != nil {
			return nil, err
		}
		return [2]string{summary, path}, nil
	}, func(a *app, v any) string {
		r, _ := v.([2]string)
		out := indent(cleanLines(r[0])) + "\n"
		if r[1] != "" {
			out += "\n " + i18n.T(i18n.TUIDocSaved, r[1]) + "\n"
		}
		return out
	})
	t.cancellable = true // collects data only
	return t
}
