package cli

import (
	"slices"
	"strings"
)

// Boxed tables of the longer human outputs (status): every cell sits in a
// frame, the header row is set apart by a rule and drawn in color, and
// the table never runs past the line width — the flexible columns give way
// first, their cells cut with an ellipsis.

// boxCol is one column of a boxed table.
type boxCol struct {
	title string
	// min is the narrowest the column may become; 0 = the title's width.
	min int
	// max caps the column (0 = no cap).
	max int
	// flex columns shrink first when the table is wider than the line.
	flex bool
	// right aligns the column to the right (numbers).
	right bool
}

// boxGlyphs are the frame characters (ASCII without UTF-8).
type boxGlyphs struct {
	h, v              string
	tl, tm, tr        string
	ml, mm, mr        string
	bl, bm, br        string
	hh, hml, hmm, hmr string // the rule under the header
}

func (g *Globals) boxGlyphs() boxGlyphs {
	if !g.unicode() {
		return boxGlyphs{h: "-", v: "|", tl: "+", tm: "+", tr: "+", ml: "+", mm: "+", mr: "+",
			bl: "+", bm: "+", br: "+", hh: "-", hml: "+", hmm: "+", hmr: "+"}
	}
	return boxGlyphs{h: "─", v: "│", tl: "┌", tm: "┬", tr: "┐", ml: "├", mm: "┼", mr: "┤",
		bl: "└", bm: "┴", br: "┘", hh: "─", hml: "├", hmm: "┼", hmr: "┤"}
}

// boxWidths lays the columns out: each as wide as its widest cell (within
// min and max), then the flexible columns and after them the others shrink,
// widest first, until the table fits avail columns.
func boxWidths(cols []boxCol, rows [][]string, avail int) []int {
	w := make([]int, len(cols))
	floor := make([]int, len(cols))
	for c, col := range cols {
		w[c] = width(col.title)
		for _, r := range rows {
			if c < len(r) {
				w[c] = max(w[c], width(r[c]))
			}
		}
		if col.max > 0 {
			w[c] = min(w[c], col.max)
		}
		floor[c] = col.min
		if floor[c] <= 0 {
			floor[c] = min(width(col.title), w[c])
		}
		floor[c] = max(1, min(floor[c], w[c]))
	}
	// Frame: "│ " before every cell, " " after it and the closing "│".
	total := func() int {
		n := 1
		for _, x := range w {
			n += x + 3
		}
		return n
	}
	for _, flexFirst := range []bool{true, false} {
		for total() > avail {
			widest := -1
			for c, col := range cols {
				if col.flex != flexFirst || w[c] <= floor[c] {
					continue
				}
				if widest < 0 || w[c] > w[widest] {
					widest = c
				}
			}
			if widest < 0 {
				break
			}
			w[widest]--
		}
	}
	return w
}

// boxTable renders a boxed table indented by two spaces. style, when not
// nil, returns the style code of a body cell ("" = plain).
func (g *Globals) boxTable(cols []boxCol, rows [][]string, style func(row, col int) string) []string {
	const indent = "  "
	b := g.boxGlyphs()
	ell := g.sym().ell
	// Measure what is printed: without UTF-8 "→" becomes "->".
	cols = slices.Clone(cols)
	for c := range cols {
		cols[c].title = g.text(cols[c].title)
	}
	plain := make([][]string, len(rows))
	for i, r := range rows {
		plain[i] = make([]string, len(r))
		for c, x := range r {
			plain[i][c] = g.text(x)
		}
	}
	rows = plain
	w := boxWidths(cols, rows, g.lineWidth()-len(indent))
	rule := func(l, m, r, h string) string {
		var s strings.Builder
		s.WriteString(indent + l)
		for c, x := range w {
			if c > 0 {
				s.WriteString(m)
			}
			s.WriteString(strings.Repeat(h, x+2))
		}
		return s.String() + r
	}
	cell := func(text string, c int) string {
		text = trunc(text, w[c], ell)
		if cols[c].right {
			return padLeft(text, w[c])
		}
		return pad(text, w[c])
	}
	frame := g.styleOut(styleGray, b.v)
	line := func(cells []string) string {
		var s strings.Builder
		s.WriteString(indent + frame)
		for c := range w {
			s.WriteString(" " + cells[c] + " " + frame)
		}
		return s.String()
	}
	out := []string{g.styleOut(styleGray, rule(b.tl, b.tm, b.tr, b.h))}
	headed := false
	for _, col := range cols {
		headed = headed || col.title != ""
	}
	if headed { // a table whose columns have no titles is a plain frame
		head := make([]string, len(w))
		for c := range w {
			head[c] = g.styleOut(styleCyan, cell(cols[c].title, c))
		}
		out = append(out, line(head), g.styleOut(styleGray, rule(b.hml, b.hmm, b.hmr, b.hh)))
	}
	for i, r := range rows {
		cells := make([]string, len(w))
		for c := range w {
			text := ""
			if c < len(r) {
				text = r[c]
			}
			cells[c] = cell(text, c)
			if style != nil {
				if code := style(i, c); code != "" {
					cells[c] = g.styleOut(code, cells[c])
				}
			}
		}
		out = append(out, line(cells))
	}
	return append(out, g.styleOut(styleGray, rule(b.bl, b.bm, b.br, b.h)))
}
