package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetectCaps(t *testing.T) {
	c := DetectCaps(env(map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}))
	if !c.Unicode || !c.Color {
		t.Fatalf("utf8 xterm: %+v", c)
	}
	c = DetectCaps(env(map[string]string{"TERM": "dumb", "LANG": "en_US.UTF-8"}))
	if c.Unicode || c.Color {
		t.Fatalf("dumb: %+v", c)
	}
	c = DetectCaps(env(map[string]string{"TERM": "xterm", "LANG": "C"}))
	if c.Unicode {
		t.Fatalf("C locale must be ASCII: %+v", c)
	}
}

func TestASCIIBannerIsASCII(t *testing.T) {
	b := Banner(Caps{Unicode: false, Width: 80}, BannerStatus{Role: "hub", Name: "ir-1", PublicIP: "5.6.7.8", Nodes: 2, TunnelsUp: 1})
	for _, r := range b {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in ASCII banner:\n%s", r, b)
		}
	}
	if !strings.Contains(b, "DEYROUTE Tunnel Manager") || !strings.Contains(b, "#") {
		t.Fatalf("banner missing product text or art:\n%s", b)
	}
	for _, l := range strings.Split(b, "\n") {
		if len(l) > 80 {
			t.Fatalf("line wider than 80 cols: %q", l)
		}
	}
}

func TestUnicodeBannerStatusLine(t *testing.T) {
	b := Banner(Caps{Unicode: true}, BannerStatus{Role: "hub", Name: "ir-1", PublicIP: "5.6.7.8", Nodes: 2, TunnelsUp: 1})
	want := "Hub: ir-1 (5.6.7.8)  ·  Mode: Simple  ·  2 nodes  ·  1 tunnel UP"
	if !strings.Contains(b, want) {
		t.Fatalf("status line missing %q:\n%s", want, b)
	}
	if !strings.Contains(b, "██████╗ ███████╗") {
		t.Fatal("unicode art missing")
	}
}

func TestMenuFixedNumbers(t *testing.T) {
	out := RenderMenu(Caps{Unicode: true}, MainMenu(), false)
	for _, want := range []string{" 1) Dashboard (live)", " 2) Tunnels", "10) Backup & Restore", "12) Settings", " 0) Exit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("menu missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "thresholds *") {
		t.Fatal("advanced-only item shown in simple mode")
	}
	adv := RenderMenu(Caps{Unicode: true}, MainMenu(), true)
	if !strings.Contains(adv, "thresholds *") || !strings.Contains(adv, "view fingerprints *") {
		t.Fatalf("advanced items missing:\n%s", adv)
	}
}

func TestMenuASCII80(t *testing.T) {
	out := RenderMenu(Caps{Unicode: false, Width: 80}, MainMenu(), true)
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(l) > 80 {
			t.Fatalf("too wide: %q", l)
		}
		for _, r := range l {
			if r > 127 {
				t.Fatalf("non-ASCII in %q", l)
			}
		}
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestModelNavigation(t *testing.T) {
	var m tea.Model = NewModel(Options{Caps: Caps{Unicode: true}})
	m, _ = m.Update(key("2"))
	m, _ = m.Update(key("enter"))
	if !strings.Contains(m.View(), "Tunnels: not implemented yet") {
		t.Fatalf("stub screen expected:\n%s", m.View())
	}
	m, _ = m.Update(key("esc"))
	if !strings.Contains(m.View(), "Choice:") {
		t.Fatal("expected menu after esc")
	}
	m, _ = m.Update(key("9"))
	m, _ = m.Update(key("9"))
	m, _ = m.Update(key("enter"))
	if !strings.Contains(m.View(), "Invalid choice: 99") {
		t.Fatalf("invalid choice expected:\n%s", m.View())
	}
	m, cmd := m.Update(key("0"))
	_ = cmd
	_, cmd = m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("0 + Enter must quit")
	}
}
