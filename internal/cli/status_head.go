package cli

import (
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/version"
)

// The head of `deyroute status`: the product name in large letters with
// the version small under it, then one line of labelled facts — label in
// gray, value in bold, the counts green when everything is up, yellow when
// part of it is and red when nothing is.

// logo is DEYROUTE in two rows of block letters (UTF-8 terminals only).
var logo = [2]string{
	"█▀▄ █▀▀ █▄█ █▀█ █▀█ █ █ ▀█▀ █▀▀",
	"█▄▀ ██▄  █  █▀▄ █▄█ █▄█  █  ██▄",
}

// headLines are the lines on top of the dashboard.
func (g *Globals) headLines(st api.Status) []string {
	tag := i18n.T(i18n.CLIHeadTagline, version.Display())
	var lines []string
	if g.unicode() && g.lineWidth() >= width(logo[0])+2 {
		for _, l := range logo {
			lines = append(lines, " "+g.styleOut(styleCyan, l))
		}
		lines = append(lines, " "+g.styleOut(styleGray, tag))
	} else {
		lines = append(lines, " "+g.styleOut(styleCyan, "DEYROUTE")+"  "+g.styleOut(styleGray, g.text(tag)))
	}
	lines = append(lines, "")
	sep := "   " + g.styleOut(styleGray, "│") + "   "
	if !g.unicode() {
		sep = "   " + g.styleOut(styleGray, "|") + "   "
	}
	for _, l := range joinWrap(g.headFacts(st), sep, g.lineWidth()-1) {
		lines = append(lines, " "+l)
	}
	return lines
}

// headFacts are the labelled facts of the head line.
func (g *Globals) headFacts(st api.Status) []string {
	fact := func(label, value string) string {
		return g.styleOut(styleGray, label) + " " + g.styleOut(styleBold, value)
	}
	count := func(label string, format i18n.Key, n, of int) string {
		style := styleGreen
		switch {
		case of == 0:
			style = styleGray
		case n == 0:
			style = styleRed
		case n < of:
			style = styleYellow
		}
		mark := g.sym().up
		if n < of || of == 0 {
			mark = g.sym().half
		}
		if n == 0 && of > 0 {
			mark = g.sym().down
		}
		return g.styleOut(styleGray, label) + " " + g.styleOut(style, mark+" "+i18n.T(format, n, of))
	}
	var out []string
	switch {
	case st.Hub != nil:
		where := st.Hub.Name
		if st.Hub.PublicIP != "" {
			where += " " + g.styleOut(styleGray, g.sym().sep) + " " + st.Hub.PublicIP
		}
		out = append(out, fact(i18n.T(i18n.CLIHeadHub), where))
		mode := i18n.T(i18n.ModeSimple)
		if st.Hub.UIMode == "advanced" {
			mode = i18n.T(i18n.ModeAdvanced)
		}
		out = append(out, fact(i18n.T(i18n.CLIHeadMode), mode))
		online := 0
		for _, n := range st.Nodes {
			if n.Online {
				online++
			}
		}
		out = append(out, count(i18n.T(i18n.CLIHeadNodes), i18n.CLIHeadOnline, online, len(st.Nodes)))
	case st.NodeSelf != nil:
		out = append(out, fact(i18n.T(i18n.CLIHeadNode), st.NodeSelf.ID),
			fact(i18n.T(i18n.CLIHeadHubOf), hubLabel(*st.NodeSelf)))
	}
	if st.NodeSelf == nil {
		up, enabled := 0, 0
		for _, t := range st.Tunnels {
			if t.Enabled {
				enabled++
				if t.State == state.StateUp {
					up++
				}
			}
		}
		out = append(out, count(i18n.T(i18n.CLIHeadTunnels), i18n.CLIHeadUp, up, enabled))
	}
	for i := range out {
		out[i] = strings.TrimSpace(g.text(out[i]))
	}
	return out
}
