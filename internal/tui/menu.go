package tui

import (
	"fmt"
	"strings"

	"github.com/localroot4/deyroute/internal/i18n"
)

// MenuItem is one entry of the fixed main menu (section 6).
type MenuItem struct {
	Num     int
	Title   i18n.Key
	Desc    i18n.Key // Simple-mode description ("" for none)
	DescAdv i18n.Key // Advanced-mode description; falls back to Desc
}

// MainMenu returns the main menu. Numbers are fixed across modes.
func MainMenu() []MenuItem {
	return []MenuItem{
		{1, i18n.MenuDashboard, "", ""},
		{2, i18n.MenuTunnels, i18n.MenuTunnelsDesc, ""},
		{3, i18n.MenuNodes, i18n.MenuNodesDesc, ""},
		{4, i18n.MenuPorts, i18n.MenuPortsDesc, ""},
		{5, i18n.MenuFailover, i18n.MenuFailoverDesc, i18n.MenuFailoverDescAdv},
		{6, i18n.MenuDiagnostics, i18n.MenuDiagnosticsDesc, ""},
		{7, i18n.MenuOptimize, i18n.MenuOptimizeDesc, i18n.MenuOptimizeDescAdv},
		{8, i18n.MenuSecurity, i18n.MenuSecurityDesc, i18n.MenuSecurityDescAdv},
		{9, i18n.MenuNotifications, i18n.MenuNotificationsDes, ""},
		{10, i18n.MenuBackup, "", ""},
		{11, i18n.MenuUpdate, i18n.MenuUpdateDesc, ""},
		{12, i18n.MenuSettings, i18n.MenuSettingsDesc, ""},
		{0, i18n.MenuExit, "", ""},
	}
}

// RenderMenu renders the menu in the layout of section 6: a right-aligned
// two-digit number, ") ", the title padded to 14 columns, then the description.
func RenderMenu(c Caps, items []MenuItem, advanced bool) string {
	var b strings.Builder
	for _, it := range items {
		title := i18n.T(it.Title)
		desc := ""
		switch {
		case advanced && it.DescAdv != "":
			desc = i18n.T(it.DescAdv)
		case it.Desc != "":
			desc = i18n.T(it.Desc)
		}
		if !c.Unicode {
			desc = ToASCII(desc)
		}
		line := fmt.Sprintf("%2d) %-14s %s", it.Num, title, desc)
		if c.Width > 1 && width(line) > c.Width {
			ell := "…"
			if !c.Unicode {
				ell = "..."
			}
			line = trunc(line, c.Width, ell)
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// ToASCII replaces the few non-ASCII symbols used in UI strings.
func ToASCII(s string) string {
	r := strings.NewReplacer("·", "-", "…", "...", "→", "->", "●", "*", "◐", "~", "○", "o", "✔", "OK", "✖", "x", "—", "-")
	return r.Replace(s)
}
