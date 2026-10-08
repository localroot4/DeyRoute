package cli

import (
	"strings"
)

// Layout of the longer human outputs (status, optimize status, the tuning
// plan): every part starts with a section head that is a full-width rule
// with the title in it, rows are aligned in columns, and no line is longer
// than the terminal, so the output reads the same on a laptop and on a
// phone's SSH client.

// Line widths of human output: unknown terminals get defaultLineWidth, and
// very wide ones are capped so the rules do not run across a whole screen.
const (
	defaultLineWidth = 100
	minLineWidth     = 40
	maxLineWidth     = 120
)

// lineWidth is the width human output is laid out for.
func (g *Globals) lineWidth() int {
	w := g.caps().Width
	if w <= 0 {
		w = defaultLineWidth
	}
	return min(max(w, minLineWidth), maxLineWidth)
}

// sectionHead renders "── TITLE ─────── hint" across the line width ("--"
// without UTF-8); the title is bold on a color terminal and hint, when not
// empty, closes the rule in plain text.
func (g *Globals) sectionHead(title, hint string) string {
	rule := "─"
	if !g.unicode() {
		rule = "-"
	}
	used := 3 + width(title) + 1
	tail := ""
	if hint != "" {
		tail = " " + hint
		used += width(tail) + 2
	}
	n := max(3, g.lineWidth()-used)
	if hint != "" && used+3 > g.lineWidth() {
		tail, n = "", max(3, g.lineWidth()-3-width(title)-1)
	}
	head := rule + rule + " " + g.styleOut(styleCyan, title) + " " + strings.Repeat(rule, n)
	if tail != "" {
		head += tail
	}
	return head
}

// fit cuts a line of plain text to the line width with an ellipsis.
func (g *Globals) fit(s string) string {
	return trunc(s, g.lineWidth(), g.sym().ell)
}

// joinWrap joins parts with sep into as few lines of at most w columns as
// possible, breaking only between parts.
func joinWrap(parts []string, sep string, w int) []string {
	var out []string
	line := ""
	for _, p := range parts {
		switch {
		case line == "":
			line = p
		case width(line)+width(sep)+width(p) <= w:
			line += sep + p
		default:
			out = append(out, line)
			line = p
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// kvWrapLines aligns label/value rows ("  label    value", empty rows
// skipped); values longer than the line width lw wrap under themselves.
func kvWrapLines(indent string, rows [][2]string, lw int) []string {
	w := 0
	for _, r := range rows {
		if r[1] != "" {
			w = max(w, width(r[0]))
		}
	}
	var out []string
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		lead := indent + pad(r[0], w+3)
		for i, l := range wrapText(r[1], max(10, lw-width(lead))) {
			if i == 0 {
				out = append(out, lead+l)
			} else {
				out = append(out, strings.Repeat(" ", width(lead))+l)
			}
		}
	}
	return out
}

// wrapText breaks s into lines of at most w columns at spaces (a longer
// word stays whole).
func wrapText(s string, w int) []string {
	words := strings.Fields(s)
	var out []string
	line := ""
	for _, word := range words {
		switch {
		case line == "":
			line = word
		case width(line)+1+width(word) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
