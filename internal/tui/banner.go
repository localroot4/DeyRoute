package tui

import (
	"fmt"
	"strings"

	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/version"
)

// BannerStatus is the information shown under the DEYROUTE art.
type BannerStatus struct {
	Role      string // "hub", "node" or "" when not set up
	Name      string
	PublicIP  string
	HubAddr   string // node only
	Advanced  bool
	Nodes     int
	TunnelsUp int
	// Setup marks the banner of the setup wizard ("first-time setup"
	// instead of "not set up").
	Setup bool
}

// ASCIIBanner converts the Unicode art to plain '#' characters for terminals
// without UTF-8: full blocks become '#', box-drawing strokes become spaces.
func ASCIIBanner() string {
	art := strings.Map(func(r rune) rune {
		switch {
		case r == '█':
			return '#'
		case r == '\n':
			return r
		case r > 127:
			return ' '
		}
		return r
	}, i18n.T(i18n.BannerArt))
	lines := strings.Split(art, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimRight(l, " "); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// Banner renders the art plus the status line, e.g.
// " DEYROUTE Tunnel Manager  v1.0.0  ·  Hub: ir-1 (5.6.7.8)  ·  Mode: Simple  ·  2 nodes  ·  1 tunnel UP".
func Banner(c Caps, st BannerStatus) string {
	art := i18n.T(i18n.BannerArt)
	sep := "  ·  "
	if !c.Unicode {
		art = ASCIIBanner()
		sep = "  -  "
	}
	parts := []string{i18n.T(i18n.BannerProduct) + "  " + version.Display()}
	switch st.Role {
	case "hub":
		parts = append(parts, i18n.T(i18n.BannerHub, st.Name, st.PublicIP))
	case "node":
		parts = append(parts, i18n.T(i18n.BannerNode, st.Name, st.HubAddr))
	default:
		if st.Setup {
			parts = append(parts, i18n.T(i18n.BannerSetup))
		} else {
			parts = append(parts, i18n.T(i18n.BannerNotSetUp))
		}
	}
	mode := i18n.T(i18n.ModeSimple)
	if st.Advanced {
		mode = i18n.T(i18n.ModeAdvanced)
	}
	parts = append(parts, i18n.T(i18n.BannerMode, mode))
	if st.Role == "hub" {
		nodes, up := i18n.T(i18n.BannerNodes, st.Nodes), i18n.T(i18n.BannerTunnelsUp, st.TunnelsUp)
		if st.Nodes == 1 {
			nodes = i18n.T(i18n.BannerNode1)
		}
		if st.TunnelsUp == 1 {
			up = i18n.T(i18n.BannerTunnel1Up)
		}
		parts = append(parts, nodes, up)
	}
	line := " " + strings.Join(parts, sep)
	if c.Width > 0 && displayWidth(line) > c.Width {
		// Shorten rather than wrap: first the product name, then the
		// separators, then the mode; the counts stay as long as possible.
		parts[0] = i18n.T(i18n.BannerShort) + " " + version.Display()
		steps := []func(){
			func() {},
			func() { sep = " · " },
			func() {
				if len(parts) > 2 {
					parts = append(parts[:2:2], parts[3:]...)
				}
			},
		}
		for _, step := range steps {
			step()
			if !c.Unicode && sep == " · " {
				sep = " - "
			}
			if line = " " + strings.Join(parts, sep); displayWidth(line) <= c.Width {
				break
			}
		}
		for len(parts) > 1 && displayWidth(line) > c.Width {
			parts = parts[:len(parts)-1]
			line = " " + strings.Join(parts, sep)
		}
	}
	return fmt.Sprintf("%s\n%s", art, line)
}

func displayWidth(s string) int { return len([]rune(s)) }
