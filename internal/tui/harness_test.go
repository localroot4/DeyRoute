package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/state"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// testNow is the fixed clock of every test.
var testNow = time.Date(2026, 9, 30, 12, 45, 0, 0, time.UTC)

// harness drives the model like Bubble Tea does: every command runs in its
// own goroutine and its message is fed back into Update.
type harness struct {
	t       *testing.T
	m       tea.Model
	msgs    chan tea.Msg
	pending atomic.Int32
	wg      sync.WaitGroup
	quit    bool
	ticks   []tea.Msg
	mu      sync.Mutex
}

func newHarness(t *testing.T, o Options) *harness {
	t.Helper()
	h := &harness{t: t, msgs: make(chan tea.Msg, 4096)}
	if o.tick == nil {
		o.tick = func(_ time.Duration, msg tea.Msg) tea.Cmd {
			h.mu.Lock()
			h.ticks = append(h.ticks, msg)
			h.mu.Unlock()
			return nil
		}
	}
	if o.Now == nil {
		o.Now = func() time.Time { return testNow }
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.Caps.Width == 0 {
		o.Caps.Width = 120
	}
	h.m = NewModel(o)
	h.exec(h.m.Init())
	h.settle()
	t.Cleanup(h.close)
	return h
}

func (h *harness) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	h.pending.Add(1)
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		msg := cmd()
		h.msgs <- msg
		h.pending.Add(-1)
	}()
}

func (h *harness) handle(msg tea.Msg) {
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.exec(c)
		}
	case tea.QuitMsg:
		h.quit = true
	default:
		nm, cmd := h.m.Update(msg)
		h.m = nm
		h.exec(cmd)
	}
}

// settle processes messages until no command is pending (or the remaining
// ones are blocked, e.g. a followed log stream).
func (h *harness) settle() {
	for {
		select {
		case msg := <-h.msgs:
			h.handle(msg)
			continue
		default:
		}
		if h.pending.Load() == 0 && len(h.msgs) == 0 {
			return
		}
		select {
		case msg := <-h.msgs:
			h.handle(msg)
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

func (h *harness) close() {
	nm, _ := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	h.m = nm
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	for {
		select {
		case <-h.msgs:
		case <-done:
			return
		case <-time.After(10 * time.Second):
			h.t.Error("background commands did not stop")
			return
		}
	}
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// press sends keys one after the other and settles after each.
func (h *harness) press(keys ...string) *harness {
	for _, k := range keys {
		nm, cmd := h.m.Update(keyMsg(k))
		h.m = nm
		h.exec(cmd)
		h.settle()
	}
	return h
}

// choose types a number and Enter.
func (h *harness) choose(n string) *harness { return h.press(n, "enter") }

// typeLine types text (as one paste) and Enter.
func (h *harness) typeLine(s string) *harness {
	if s != "" {
		h.press(s)
	}
	return h.press("enter")
}

func (h *harness) view() string { return h.m.View() }

func (h *harness) must(parts ...string) {
	h.t.Helper()
	v := h.view()
	for _, p := range parts {
		if !strings.Contains(v, p) {
			h.t.Fatalf("view is missing %q:\n%s", p, v)
		}
	}
}

func (h *harness) mustNot(parts ...string) {
	h.t.Helper()
	v := h.view()
	for _, p := range parts {
		if strings.Contains(v, p) {
			h.t.Fatalf("view unexpectedly contains %q:\n%s", p, v)
		}
	}
}

func (h *harness) depth() int { return len(h.m.(Model).a.stack) }

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) // #nosec G304 -- test fixture
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Fatalf("%s differs from golden:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// ---- fixtures (the section 6 sample)

func sampleTunnels() []api.TunnelInfo {
	return []api.TunnelInfo{
		{
			ID: "main", Name: "Main 443/2053", Enabled: true, State: state.StateUp,
			ActiveNode: "de-1", ActiveNodeName: "Germany 1", ActiveTransport: "backhaul/wssmux",
			RTTms: 41, UpSince: testNow.Add(-(3*24*time.Hour + 4*time.Hour + 12*time.Minute + 30*time.Second)),
			Ports: []api.PortMapDTO{{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443"}, {Listen: 2053, Proto: "tcp", Target: "127.0.0.1:2053"}},
			Nodes: []string{"de-1", "nl-1"}, LadderName: "default",
			Ladder: []string{"backhaul/wssmux", "backhaul/tcpmux", "direct/native"}, Policy: "transport_then_node", ClientIP: "masked",
		},
		{
			ID: "games", Name: "Games UDP", Enabled: true, State: state.StateDegraded,
			ActiveNode: "nl-1", ActiveNodeName: "Netherlands 1", ActiveTransport: "hysteria2/udp",
			RTTms: 188, UpSince: testNow.Add(-(3*time.Minute + 10*time.Second)),
			Ports: []api.PortMapDTO{{Listen: 27015, Proto: "udp"}},
			Nodes: []string{"nl-1"}, Ladder: []string{"hysteria2/udp", "direct/native"}, Policy: "transport_then_node",
			Warnings: []string{"rung wireguard/kernel skipped: UDP blocked"},
		},
	}
}

func sampleNodes() []api.NodeInfo {
	return []api.NodeInfo{
		{ID: "de-1", Name: "Germany 1", PublicIP: "1.2.3.4", Online: true, ControlRTTms: 39, Version: "1.0.0", Compatible: true, CPUPercent: 3, RAMBytes: 121 << 20, Tunnels: []string{"main"}, Fingerprint: "sha256:aa"},
		{ID: "nl-1", Name: "Netherlands 1", PublicIP: "9.8.7.6", Online: true, ControlRTTms: 44, Version: "1.0.0", Compatible: true, CPUPercent: 1, RAMBytes: 98 << 20, Tunnels: []string{"games"}, Fingerprint: "sha256:bb"},
	}
}

func sampleStatus() api.Status {
	return api.Status{
		Schema: 1, Role: "hub", Version: "1.0.0", GeneratedAt: testNow,
		Hub:     &api.HubStatus{Name: "ir-1", PublicIP: "5.6.7.8", ControlPort: 44433, UIMode: "simple", Language: "en"},
		Tunnels: sampleTunnels(),
		Nodes:   sampleNodes(),
		Events: []state.Event{
			{At: time.Date(2026, 9, 30, 12, 41, 3, 0, time.UTC), Level: "info", Type: state.EvSwitchTransport, Tunnel: "main",
				Message: "backhaul/tcpmux → backhaul/wssmux (failback, primary healthy 5m)"},
			{At: time.Date(2026, 9, 30, 12, 35, 58, 0, time.UTC), Level: "error", Type: state.EvTunnelDown, Tunnel: "main",
				Message: "backhaul/tcpmux probe failed 3x (timeout)"},
		},
		Warnings: []api.Warning{{Message: "TLS certificate of main expires in 12 days", Tunnel: "main"}},
	}
}
