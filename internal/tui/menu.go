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
	// NodeDesc describes the item on a node server ("" = Desc). HubOnly
	// items have nothing to do on a node: NodeMenu marks them.
	NodeDesc i18n.Key
	HubOnly  bool
}

// MainMenu returns the main menu. Numbers are fixed across modes.
func MainMenu() []MenuItem {
	return []MenuItem{
		{1, i18n.MenuDashboard, "", "", "", false},
		{2, i18n.MenuTunnels, i18n.MenuTunnelsDesc, "", "", true},
		{3, i18n.MenuNodes, i18n.MenuNodesDesc, "", "", true},
		{4, i18n.MenuPorts, i18n.MenuPortsDesc, "", "", true},
		{5, i18n.MenuFailover, i18n.MenuFailoverDesc, i18n.MenuFailoverDescAdv, "", true},
		{6, i18n.MenuDiagnostics, i18n.MenuDiagnosticsDesc, "", i18n.MenuDiagnosticsNode, false},
		{7, i18n.MenuOptimize, i18n.MenuOptimizeDesc, i18n.MenuOptimizeDescAdv, "", true},
		{8, i18n.MenuSecurity, i18n.MenuSecurityDesc, i18n.MenuSecurityDescAdv, "", true},
		{9, i18n.MenuNotifications, i18n.MenuNotificationsDes, "", "", true},
		{10, i18n.MenuBackup, "", "", i18n.MenuBackupNode, false},
		{11, i18n.MenuUpdate, i18n.MenuUpdateDesc, "", "", true},
		{12, i18n.MenuSettings, i18n.MenuSettingsDesc, "", i18n.MenuSettingsNode, false},
		{0, i18n.MenuExit, "", "", "", false},
	}
}

// NodeMenu is the main menu of a node server: the same numbers, the items
// only the hub can do marked "(hub only)" (picking one explains where it
// is done instead of answering DEY-X009) and the others described by what
// they do on a node.
func NodeMenu() []MenuItem {
	items := MainMenu()
	for i := range items {
		it := &items[i]
		switch {
		case it.HubOnly:
			it.Desc, it.DescAdv = i18n.MenuHubOnly, ""
		case it.NodeDesc != "":
			it.Desc, it.DescAdv = it.NodeDesc, ""
		}
	}
	return items
}

// mainItem returns the main-menu item with number n.
func mainItem(items []MenuItem, n int) (MenuItem, bool) {
	for _, it := range items {
		if it.Num == n {
			return it, true
		}
	}
	return MenuItem{}, false
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

// ToASCII replaces the few non-ASCII symbols used in UI strings (the
// traffic arrows "↓" download and "↑" upload become "v" and "^").
func ToASCII(s string) string {
	r := strings.NewReplacer("·", "-", "…", "...", "→", "->", "●", "*", "◐", "~", "○", "o", "✔", "OK", "✖", "x", "—", "-",
		"↓", "v", "↑", "^")
	return r.Replace(s)
}
