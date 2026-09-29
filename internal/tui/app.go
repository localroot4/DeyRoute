// Package tui is the Bubble Tea front-end. It holds no management logic:
// every action is one Local API call, the same one the CLI makes.
package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/localroot4/deyroute/internal/i18n"
)

// Options configures Run.
type Options struct {
	Caps     Caps
	Status   BannerStatus
	Advanced bool
}

type screen int

const (
	screenMenu screen = iota
	screenStub
	screenHelp
)

// Model is the root Bubble Tea model.
type Model struct {
	opts   Options
	items  []MenuItem
	screen screen
	input  string
	msg    string // feedback line (invalid choice, stub text)
	quit   bool
}

// NewModel builds the root model.
func NewModel(o Options) Model {
	return Model{opts: o, items: MainMenu()}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.opts.Caps.Width = msg.Width
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key == "ctrl+c" {
		m.quit = true
		return m, tea.Quit
	}
	if m.screen != screenMenu {
		switch key {
		case "q", "esc", "enter":
			m.screen, m.msg = screenMenu, ""
		}
		return m, nil
	}
	switch key {
	case "q", "esc":
		if m.input != "" {
			m.input = ""
			return m, nil
		}
		m.quit = true
		return m, tea.Quit
	case "?":
		m.screen = screenHelp
		return m, nil
	case "r":
		m.msg = ""
		return m, nil
	case "backspace":
		if m.input != "" {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "enter":
		return m.choose()
	}
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && len(m.input) < 2 {
		m.input += key
	}
	return m, nil
}

func (m Model) choose() (tea.Model, tea.Cmd) {
	in := strings.TrimSpace(m.input)
	m.input = ""
	n, err := strconv.Atoi(in)
	if err != nil {
		m.msg = i18n.T(i18n.InvalidChoice, in)
		return m, nil
	}
	for _, it := range m.items {
		if it.Num != n {
			continue
		}
		if n == 0 {
			m.quit = true
			return m, tea.Quit
		}
		m.screen = screenStub
		m.msg = i18n.T(i18n.NotImplemented, i18n.T(it.Title))
		return m, nil
	}
	m.msg = i18n.T(i18n.InvalidChoice, in)
	return m, nil
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quit {
		return ""
	}
	c := m.opts.Caps
	st := m.opts.Status
	st.Advanced = m.opts.Advanced
	var b strings.Builder
	b.WriteString(m.style(lipgloss.Color("6")).Render(Banner(c, st)))
	b.WriteString("\n\n")
	switch m.screen {
	case screenHelp:
		b.WriteString(i18n.T(i18n.HelpTitle) + "\n\n" + i18n.T(i18n.HelpMainMenu) + "\n\n" + i18n.T(i18n.PressBack) + "\n")
	case screenStub:
		b.WriteString(m.msg + "\n\n" + i18n.T(i18n.PressBack) + "\n")
	default:
		b.WriteString(RenderMenu(c, m.items, m.opts.Advanced))
		if m.msg != "" {
			b.WriteString("\n" + m.style(lipgloss.Color("1")).Render(m.msg) + "\n")
		}
		b.WriteString("\n" + i18n.T(i18n.PromptChoice) + m.input + "\n")
	}
	footer := i18n.T(i18n.FooterKeys)
	if !c.Unicode {
		footer = i18n.T(i18n.FooterKeysASCII)
	}
	b.WriteString("\n" + m.style(lipgloss.Color("8")).Render(footer) + "\n")
	return b.String()
}

func (m Model) style(col lipgloss.Color) lipgloss.Style {
	if !m.opts.Caps.Color {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(col)
}

// Run starts the full-screen menu.
func Run(o Options) error {
	p := tea.NewProgram(NewModel(o), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
