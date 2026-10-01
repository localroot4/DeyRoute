package tui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sort"
	"strconv"
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
	sub := func(k i18n.Key) string { return subTitle(i18n.MenuNodes, k) }
	m := newMenu(a, i18n.MenuNodes, i18n.TUIHelpNodes, []menuItem{
		{label: i18n.TUINdJoin, act: func(a *app) tea.Cmd { return a.push(joinCommand()) }},
		{label: i18n.TUINdList, act: func(a *app) tea.Cmd { return a.push(nodeList()) }},
		{label: i18n.TUINdRename, act: func(a *app) tea.Cmd {
			return a.push(pickNode(sub(i18n.TUINdRename), "", nil, renameNode))
		}},
		{label: i18n.TUINdRemove, act: func(a *app) tea.Cmd {
			return a.push(pickNode(sub(i18n.TUINdRemove), "", nil, removeNode))
		}},
		{label: i18n.TUINdTest, act: func(a *app) tea.Cmd {
			return a.push(pickNode(sub(i18n.TUINdTest), "", nil, func(a *app, n api.NodeInfo) tea.Cmd {
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

// joinText is the join command with its intro and expiry: "Single use;
// expires at 13:00:00 (in 15 minutes)".
func joinText(a *app, j api.JoinCommand) string {
	at := j.ExpiresAt.In(a.opts.Location).Format("15:04:05")
	expiry := i18n.T(i18n.TUINdJoinExpired, at)
	if left := j.ExpiresAt.Sub(a.opts.Now()); left > 0 {
		expiry = i18n.T(i18n.TUINdJoinExpires, at, minutesText(left))
	}
	return " " + i18n.T(i18n.TUINdJoinIntro) + "\n\n" + j.Command + "\n\n" + a.paint(colGray, " "+expiry) + "\n"
}

// minutesText is a duration in whole minutes, rounded up: "15 minutes",
// "1 minute".
func minutesText(d time.Duration) string {
	n := int((d + time.Minute - 1) / time.Minute)
	if n <= 1 {
		return i18n.T(i18n.TUIMinute1)
	}
	return i18n.T(i18n.TUIMinutes, n)
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
	var t kvTable
	t.add(i18n.T(i18n.TUINdIP), n.PublicIP)
	ctl := i18n.T(i18n.TUIOffline)
	if n.Online {
		ctl = i18n.T(i18n.TUIOnline) + " (" + ms(n.ControlRTTms) + ")"
	}
	t.add(i18n.T(i18n.TUINdControl), ctl)
	if n.UDPOK != nil {
		udp := i18n.T(i18n.TUIYes)
		if !*n.UDPOK {
			udp = a.paint(colYellow, i18n.T(i18n.TUINdUDPBlocked))
		}
		t.add(i18n.T(i18n.TUINdUDP), udp)
	}
	ver := n.Version + "  " + i18n.T(i18n.TUINdCompatible)
	if !n.Compatible {
		ver = a.paint(colYellow, n.Version+"  "+i18n.T(i18n.TUINdIncompatible))
	}
	t.add(i18n.T(i18n.TUINdVersion), ver)
	if !n.LastHeartbeat.IsZero() {
		t.add(i18n.T(i18n.TUINdLastSeen), n.LastHeartbeat.In(a.opts.Location).Format("2006-01-02 15:04:05"))
	}
	if n.LastError != "" {
		t.add(i18n.T(i18n.TUINdLastError), a.paint(colYellow, clean(n.LastError)))
	}
	if len(n.Tunnels) > 0 {
		t.add(i18n.T(i18n.TUINdTunnels), strings.Join(n.Tunnels, ", "))
	}
	if a.advanced && n.Fingerprint != "" {
		t.add(i18n.T(i18n.TUINdFingerprint), n.Fingerprint)
	}
	return t.String()
}

func renameNode(a *app, n api.NodeInfo) tea.Cmd {
	id := n.ID
	title := titleOf(i18n.TUINdRename, id)
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
	title := titleOf(i18n.TUINdRemove, id)
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
	t := newTask(titleOf(i18n.TUINdTest, id), checkTimeout, func(ctx context.Context, l api.Local, _ func(api.Step)) (any, error) {
		return l.NodeTest(ctx, id)
	}, func(a *app, v any) string {
		r, _ := v.(api.NodeTestResult)
		s := a.sym()
		var t kvTable
		ctl := a.paint(colRed, s.down+" "+i18n.T(i18n.TUIOffline))
		if r.Online {
			ctl = a.paint(colGreen, s.up+" "+i18n.T(i18n.TUIOnline)) + " (" + ms(r.ControlRTTms) + ")"
		}
		t.add(i18n.T(i18n.TUINdControl), ctl)
		udp := a.paint(colYellow, i18n.T(i18n.TUINdUDPBlocked))
		if r.UDPOK {
			udp = a.paint(colGreen, s.ok) + " " + i18n.T(i18n.TUIYes) + " (" + ms(r.UDPRTTms) + ")"
		}
		t.add(i18n.T(i18n.TUINdUDP), udp)
		for _, f := range SysFacts(r.SysInfo, id) {
			t.add(f[0], f[1])
		}
		return t.String()
	})
	t.refreshable = true
	return t
}

// sysInfoKeys are the facts a node reports (the sysinfo command), in the
// order Nodes > Test shows them, with their labels.
var sysInfoKeys = []struct {
	key   string
	label i18n.Key
}{
	{"hostname", i18n.TUISysHostname},
	{"os", i18n.TUISysOS},
	{"kernel", i18n.TUISysKernel},
	{"arch", i18n.TUISysArch},
	{"cpus", i18n.TUISysCPUs},
	{"mem_total", i18n.TUISysMemory},
	{"uptime", i18n.TUISysUptime},
	{"version", i18n.TUISysVersion},
	{"go", i18n.TUISysGo},
	{"node_id", i18n.TUISysNodeID},
}

// SysFacts turns the sysinfo of node id into labelled, human values
// (Nodes > Test and `deyroute node test`): memory in GiB, the uptime as
// "10d 00:02". The node id is left out when it is id (the title shows
// it); keys a newer node adds follow as they are.
func SysFacts(info map[string]string, id string) [][2]string {
	var out [][2]string
	known := map[string]bool{}
	for _, k := range sysInfoKeys {
		known[k.key] = true
		v := strings.TrimSpace(info[k.key])
		if v == "" || (k.key == "node_id" && v == id) {
			continue
		}
		switch k.key {
		case "mem_total":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				v = sizeText(n)
			}
		case "uptime":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				v = upTime(time.Duration(n) * time.Second)
			}
		case "version":
			v = strings.TrimPrefix(v, "v")
		}
		out = append(out, [2]string{i18n.T(k.label), clean(v)})
	}
	var rest []string
	for k := range info {
		if !known[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, [2]string{clean(k), clean(info[k])})
	}
	return out
}
