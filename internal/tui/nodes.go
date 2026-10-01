package tui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
)

// joinTTL is the lifetime of a join token (sections 3 and 11).
const joinTTL = 15 * time.Minute

// ---- 3 Nodes

func nodesMenu(a *app) screen {
	title := i18n.T(i18n.MenuNodes)
	m := newMenu(a, i18n.MenuNodes, i18n.TUIHelpNodes, []menuItem{
		{label: i18n.TUINdJoin, act: func(a *app) tea.Cmd { return a.push(joinCommand()) }},
		{label: i18n.TUINdList, act: func(a *app) tea.Cmd { return a.push(nodeList()) }},
		{label: i18n.TUINdRename, act: func(a *app) tea.Cmd {
			return a.push(pickNode(title+" - "+i18n.T(i18n.TUINdRename), "", nil, renameNode))
		}},
		{label: i18n.TUINdRemove, act: func(a *app) tea.Cmd {
			return a.push(pickNode(title+" - "+i18n.T(i18n.TUINdRemove), "", nil, removeNode))
		}},
		{label: i18n.TUINdTest, act: func(a *app) tea.Cmd {
			return a.push(pickNode(title+" - "+i18n.T(i18n.TUINdTest), "", nil, func(a *app, n api.NodeInfo) tea.Cmd {
				return a.push(testNode(n.ID))
			}))
		}},
	})
	m.load = loadNodes
	m.header = func(a *app, v any) string {
		ns, _ := v.([]api.NodeInfo)
		if len(ns) == 0 {
			return indent(i18n.T(i18n.TUIDashNoNodes)) + "\n"
		}
		return renderNodeTable(a, ns)
	}
	return m
}

// joinCommand shows the one-line join command and its expiry; r creates a
// new one. The full-screen renderer cuts or wraps a line longer than the
// window, and the command (about 230 characters) must be copied whole, so
// it is first shown on a plain screen outside the menu (plainPage), where
// the terminal wraps it softly.
func joinCommand() *taskScreen {
	t := newTask(i18n.T(i18n.TUINdJoin), callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.NodeJoinCommand(ctx, joinTTL)
	}, func(a *app, v any) string {
		j, _ := v.(api.JoinCommand)
		out := joinText(a, j)
		if !a.lineMode {
			out += "\n" + a.paint(colGray, " "+i18n.T(i18n.TUINdJoinCopy)) + "\n"
		}
		return out
	})
	t.after = func(a *app, v any) tea.Cmd {
		if a.lineMode {
			return nil // printed as plain lines already
		}
		j, _ := v.(api.JoinCommand)
		return tea.Exec(&plainPage{text: ansi.Strip(joinText(a, j)) + "\n " + i18n.T(i18n.TUINdJoinPlainEnd) + "\n"}, nil)
	}
	t.refreshable = true
	return t
}

// joinText is the join command with its intro and expiry.
func joinText(a *app, j api.JoinCommand) string {
	left := j.ExpiresAt.Sub(a.opts.Now()).Round(time.Second)
	if left < 0 {
		left = 0
	}
	return " " + i18n.T(i18n.TUINdJoinIntro) + "\n\n" + j.Command + "\n\n" +
		a.paint(colGray, " "+i18n.T(i18n.TUINdJoinExpires, j.ExpiresAt.In(a.opts.Location).Format("15:04:05"), left.String())) + "\n"
}

// plainPage prints text on the normal terminal while the menu is suspended
// (tea.Exec) and waits for Enter.
type plainPage struct {
	text string
	in   io.Reader
	out  io.Writer
}

func (p *plainPage) SetStdin(r io.Reader)  { p.in = r }
func (p *plainPage) SetStdout(w io.Writer) { p.out = w }
func (p *plainPage) SetStderr(io.Writer)   {}

// Run implements tea.ExecCommand.
func (p *plainPage) Run() error {
	if _, err := io.WriteString(p.out, "\n"+p.text); err != nil {
		return err
	}
	_, err := bufio.NewReader(p.in).ReadString('\n')
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return err
}

// nodeList is the detailed node list.
func nodeList() *taskScreen {
	t := newTask(i18n.T(i18n.TUINdList), callTimeout, loadNodes, func(a *app, v any) string {
		ns, _ := v.([]api.NodeInfo)
		if len(ns) == 0 {
			return indent(i18n.T(i18n.TUINoNodes)) + "\n"
		}
		var b strings.Builder
		for i, n := range ns {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(" " + a.bold(a.nodeLabel(n)) + "\n")
			b.WriteString(renderNodeInfo(a, n))
		}
		return b.String()
	})
	t.refreshable = true
	return t
}

func renderNodeInfo(a *app, n api.NodeInfo) string {
	var b strings.Builder
	b.WriteString(kv(i18n.T(i18n.TUINdIP), n.PublicIP))
	ctl := i18n.T(i18n.TUIOffline)
	if n.Online {
		ctl = i18n.T(i18n.TUIOnline) + " (" + ms(n.ControlRTTms) + ")"
	}
	b.WriteString(kv(i18n.T(i18n.TUINdControl), ctl))
	if n.UDPOK != nil {
		udp := i18n.T(i18n.TUIYes)
		if !*n.UDPOK {
			udp = a.paint(colYellow, i18n.T(i18n.TUINdUDPBlocked))
		}
		b.WriteString(kv(i18n.T(i18n.TUINdUDP), udp))
	}
	ver := n.Version + "  " + i18n.T(i18n.TUINdCompatible)
	if !n.Compatible {
		ver = a.paint(colYellow, n.Version+"  "+i18n.T(i18n.TUINdIncompatible))
	}
	b.WriteString(kv(i18n.T(i18n.TUINdVersion), ver))
	if !n.LastHeartbeat.IsZero() {
		b.WriteString(kv(i18n.T(i18n.TUINdLastSeen), n.LastHeartbeat.In(a.opts.Location).Format("2006-01-02 15:04:05")))
	}
	if len(n.Tunnels) > 0 {
		b.WriteString(kv(i18n.T(i18n.TUINdTunnels), strings.Join(n.Tunnels, ", ")))
	}
	if a.advanced && n.Fingerprint != "" {
		b.WriteString(kv(i18n.T(i18n.TUINdFingerprint), n.Fingerprint))
	}
	return b.String()
}

func renameNode(a *app, n api.NodeInfo) tea.Cmd {
	id := n.ID
	title := i18n.T(i18n.TUINdRename) + ": " + id
	return a.push(newForm(title, "", []field{{key: "name", label: i18n.T(i18n.TUINdNewName, id), def: n.Name}},
		func(a *app, v map[string]string) tea.Cmd {
			name := v["name"]
			if name == n.Name {
				return a.back(i18n.T(i18n.TUINothingChanged))
			}
			return a.replace(newTask(title, callTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
				return nil, l.NodeRename(ctx, id, name)
			}, textResult(i18n.T(i18n.TUINdRenamed, id, name))))
		}))
}

func removeNode(a *app, n api.NodeInfo) tea.Cmd {
	id := n.ID
	title := i18n.T(i18n.TUINdRemove) + ": " + id
	tunnels := strings.Join(n.Tunnels, ", ")
	if tunnels == "" {
		tunnels = i18n.T(i18n.TUINone)
	}
	name := n.Name
	if name == "" {
		name = id
	}
	return a.push(newConfirm(title, i18n.T(i18n.TUINdRemoveLost, id, name, tunnels), true, func(a *app) tea.Cmd {
		return a.replace(newTask(title, longTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
			return nil, l.NodeRemove(ctx, id)
		}, textResult(i18n.T(i18n.TUINdRemoved, id))))
	}))
}

func testNode(id string) *taskScreen {
	t := newTask(i18n.T(i18n.TUINdTest)+": "+id, checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.NodeTest(ctx, id)
	}, func(a *app, v any) string {
		r, _ := v.(api.NodeTestResult)
		s := a.sym()
		var b strings.Builder
		ctl := a.paint(colRed, s.down+" "+i18n.T(i18n.TUIOffline))
		if r.Online {
			ctl = a.paint(colGreen, s.up+" "+i18n.T(i18n.TUIOnline)) + " (" + ms(r.ControlRTTms) + ")"
		}
		b.WriteString(kv(i18n.T(i18n.TUINdControl), ctl))
		udp := a.paint(colYellow, i18n.T(i18n.TUINdUDPBlocked))
		if r.UDPOK {
			udp = a.paint(colGreen, s.ok) + " " + i18n.T(i18n.TUIYes) + " (" + ms(r.UDPRTTms) + ")"
		}
		b.WriteString(kv(i18n.T(i18n.TUINdUDP), udp))
		keys := make([]string, 0, len(r.SysInfo))
		for k := range r.SysInfo {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(kv(k, r.SysInfo[k]))
		}
		return b.String()
	})
	t.refreshable = true
	return t
}
