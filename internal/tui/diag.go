package tui

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
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
		line, col := logText(a, l.Line)
		if l.Source != "" && s.target != "hub" && s.target != "node" {
			// A tunnel log has both sides (section 13: [hub] / [node]).
			line = pad(i18n.T(i18n.TUILogSource, clean(l.Source)), 7) + line
		}
		b.WriteString(a.paint(col, a.clip(" "+line)) + "\n")
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

// logFirstKeys are the fields of a deyroute log line shown first, in this
// order, after the time, level, component and message; the others follow
// sorted and the error comes last.
var logFirstKeys = []string{dlog.KeyTunnel, dlog.KeyNode, dlog.KeyTransport, dlog.KeyCode}

// logText renders one log line for the Logs screen and returns the color
// of its level. A JSON line of the deyroute logs (section 13) becomes
// "12:41:03 WARN  failover  probe failed  tunnel=main code=DEY-F001"; any
// other line (a backend's own output) is shown as it is.
func logText(a *app, line string) (text, col string) {
	var m map[string]any
	if !strings.HasPrefix(strings.TrimSpace(line), "{") || json.Unmarshal([]byte(line), &m) != nil {
		return clean(line), ""
	}
	msg, ok := m[dlog.KeyMsg].(string)
	if !ok {
		return clean(line), ""
	}
	str := func(k string) string {
		v, _ := m[k].(string)
		return v
	}
	var parts []string
	if t, err := time.Parse(time.RFC3339Nano, str(dlog.KeyTS)); err == nil {
		parts = append(parts, t.In(a.opts.Location).Format("15:04:05"))
	}
	level := strings.ToUpper(str(dlog.KeyLevel))
	switch level {
	case "ERROR":
		col = colRed
	case "WARN":
		col = colYellow
	}
	if level != "" {
		parts = append(parts, pad(level, 5))
	}
	if c := str(dlog.KeyComponent); c != "" {
		parts = append(parts, c)
	}
	parts = append(parts, msg)
	shown := map[string]bool{dlog.KeyTS: true, dlog.KeyLevel: true, dlog.KeyComponent: true, dlog.KeyMsg: true, dlog.KeyErr: true}
	keys := append([]string(nil), logFirstKeys...)
	var rest []string
	for k := range m {
		if !shown[k] && !slices.Contains(logFirstKeys, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(append(keys, rest...), dlog.KeyErr)
	for _, k := range keys {
		if v, ok := m[k]; ok {
			parts = append(parts, k+"="+logValue(v))
		}
	}
	return clean(strings.Join(parts, " ")), col
}

// logValue is a field value of a log line: strings as they are (quoted
// when they hold spaces), anything else as compact JSON.
func logValue(v any) string {
	if s, ok := v.(string); ok {
		if strings.ContainsAny(s, " \t") {
			return strconv.Quote(s)
		}
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	return string(b)
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
