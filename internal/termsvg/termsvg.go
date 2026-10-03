// Package termsvg draws captured terminal output (text with ANSI SGR colour
// codes) as an SVG picture of a terminal window. The README and the guides
// show the program's screens with it (make screens).
//
// The output is byte-stable: the same text and options always give the same
// bytes, on every machine and architecture. Every coordinate is an integer
// in tenths of a pixel (the viewBox is ten times the pixel size), so no
// floating-point arithmetic is involved. Text is placed word by word at its
// terminal column with a fixed textLength, so the columns line up in any
// monospace font. Block, box-drawing, chart and status glyphs are drawn as
// shapes, so a missing font glyph never breaks a chart or turns into an
// emoji. The picture loads no fonts, scripts or other files.
package termsvg

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Geometry, in tenths of a pixel.
const (
	cellW    = 84  // one terminal column: 8.4 px, 0.6 em of the 14 px font
	cellH    = 176 // one terminal row: 17.6 px
	fontSize = 140
	baseline = 128 // from the top of a row to the text baseline
	padX     = 240
	barH     = 320 // the title bar
	padTop   = 180
	padBot   = 220
	radius   = 120
	border   = 10
)

// mono is the font stack of the terminal text: system fonts only.
const mono = `ui-monospace,SFMono-Regular,'SF Mono',Menlo,Consolas,'Liberation Mono','DejaVu Sans Mono',monospace`

// Options describe one picture.
type Options struct {
	// Cols is the terminal width in columns. Longer lines wrap as they do
	// in a terminal. 0 = the width of the longest line, nothing wraps.
	Cols int
	// Title is shown in the title bar and is the picture's accessible name.
	Title string
	// Theme gives the colours (Light or Dark).
	Theme Theme
}

// style is the SGR state of one cell: fg is -1 for the default colour or
// an ANSI index 0-15.
type style struct {
	fg   int
	bold bool
}

var plain = style{fg: -1}

// cell is one terminal column. A wide grapheme takes w columns; the
// columns after its first are continuation cells (w == 0, s == "").
type cell struct {
	s  string
	st style
	w  int
}

// screen is the parsed text: rows of cells.
type screen struct {
	rows [][]cell
}

// width is the number of columns of the longest row.
func (sc *screen) width() int {
	w := 0
	for _, r := range sc.rows {
		w = max(w, len(r))
	}
	return w
}

// parse decodes text into cells. SGR 0, 1, 22, 30-37, 39 and 90-97 set the
// style; every other escape sequence and control character is ignored
// (a tab moves to the next multiple of 8). cols > 0 wraps longer lines.
func parse(text string, cols int) *screen {
	sc := &screen{}
	var row []cell
	cur := plain
	p := ansi.NewParser()
	var state byte
	put := func(c cell) {
		if cols > 0 && len(row)+max(c.w, 1) > cols {
			sc.rows = append(sc.rows, row)
			row = nil
		}
		row = append(row, c)
		for i := 1; i < c.w; i++ {
			row = append(row, cell{st: c.st})
		}
	}
	for len(text) > 0 {
		seq, width, n, ns := ansi.DecodeSequence(text, state, p)
		state = ns
		if n <= 0 {
			break
		}
		text = text[n:]
		switch {
		case width > 0:
			put(cell{s: seq, st: cur, w: width})
		case seq == "\n":
			sc.rows = append(sc.rows, row)
			row = nil
		case seq == "\t":
			for {
				put(cell{s: " ", st: cur, w: 1})
				if len(row)%8 == 0 {
					break
				}
			}
		case ansi.HasCsiPrefix(seq) && ansi.Cmd(p.Command()) == 'm':
			cur = sgr(cur, p.Params())
		}
	}
	if len(row) > 0 {
		sc.rows = append(sc.rows, row)
	}
	for len(sc.rows) > 0 && blank(sc.rows[len(sc.rows)-1]) {
		sc.rows = sc.rows[:len(sc.rows)-1]
	}
	return sc
}

// blank reports whether a row shows nothing.
func blank(r []cell) bool {
	for _, c := range r {
		if c.s != "" && c.s != " " {
			return false
		}
	}
	return true
}

// sgr applies one Select Graphic Rendition sequence to st.
func sgr(st style, ps ansi.Params) style {
	if len(ps) == 0 {
		return plain
	}
	for i := 0; i < len(ps); i++ {
		v := ps[i].Param(0)
		switch {
		case v == 0:
			st = plain
		case v == 1:
			st.bold = true
		case v == 22:
			st.bold = false
		case v >= 30 && v <= 37:
			st.fg = v - 30
		case v == 39:
			st.fg = -1
		case v >= 90 && v <= 97:
			st.fg = v - 90 + 8
		case v == 38 || v == 48 || v == 58:
			// Extended colours are not drawn; skip their arguments.
			if i+1 < len(ps) {
				switch ps[i+1].Param(0) {
				case 5:
					i += 2
				case 2:
					i += 4
				}
			}
		}
	}
	return st
}

// Render draws text as an SVG terminal window.
func Render(text string, o Options) []byte {
	sc := parse(text, o.Cols)
	cols := o.Cols
	if cols <= 0 {
		cols = sc.width()
	}
	rows := max(len(sc.rows), 1)
	w := 2*padX + cols*cellW
	h := barH + padTop + rows*cellH + padBot
	t := o.Theme

	var body bytes.Buffer
	used := map[string]bool{}
	for y, r := range sc.rows {
		drawRow(&body, used, r, padX, barH+padTop+y*cellH)
	}

	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %d %d" role="img" aria-labelledby="title">`+"\n",
		px(w), px(h), w, h)
	fmt.Fprintf(&b, "<title id=\"title\">%s</title>\n", escape(o.Title))
	b.WriteString("<style>\n")
	// Blocks and lines are drawn with sharp edges, so neighbouring cells
	// join without a hairline seam; the window keeps its round corners.
	b.WriteString("rect{shape-rendering:crispEdges}\n")
	fmt.Fprintf(&b, ".w{fill:%s;stroke:%s;stroke-width:%d;shape-rendering:auto}\n", t.Background, t.Border, border)
	fmt.Fprintf(&b, ".l{fill:%s}\n", t.Border)
	fmt.Fprintf(&b, ".tt{fill:%s;font-family:%s;font-size:120px}\n", t.Title, mono)
	fmt.Fprintf(&b, "text{fill:%s;font-family:%s;font-size:%dpx}\n", t.Foreground, mono, fontSize)
	b.WriteString(".b{font-weight:700}\n")
	b.WriteString(".k{fill:none;stroke-linecap:round;stroke-linejoin:round}\n")
	keys := make([]string, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := t.color(k[1:])
		if k[0] == 'f' {
			fmt.Fprintf(&b, ".%s{fill:%s}\n", k, c)
		} else {
			fmt.Fprintf(&b, ".%s{stroke:%s}\n", k, c)
		}
	}
	b.WriteString("</style>\n")
	half := border / 2
	fmt.Fprintf(&b, `<rect class="w" x="%d" y="%d" width="%d" height="%d" rx="%d"/>`+"\n", half, half, w-border, h-border, radius)
	for i, c := range t.Dots {
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="60" fill="%s"/>`+"\n", padX+i*200, barH/2, c)
	}
	if o.Title != "" {
		fmt.Fprintf(&b, `<text class="tt" x="%d" y="%d" text-anchor="middle">%s</text>`+"\n", w/2, barH/2+42, escape(o.Title))
	}
	fmt.Fprintf(&b, `<rect class="l" x="%d" y="%d" width="%d" height="%d"/>`+"\n", border, barH, w-2*border, border)
	b.WriteString(`<g xml:space="preserve">` + "\n")
	b.Write(body.Bytes())
	b.WriteString("</g>\n</svg>\n")
	return b.Bytes()
}

// drawRow writes the words and glyph shapes of one row whose top-left
// corner is (x0, y0). used collects the colour classes it needs.
func drawRow(b *bytes.Buffer, used map[string]bool, r []cell, x0, y0 int) {
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c.w == 0 || c.s == " " || c.s == "":
			i++
		case c.s == "█":
			// One rectangle for a run of full blocks of one colour.
			j := i + 1
			for j < len(r) && r[j].s == "█" && r[j].st.fg == c.st.fg {
				j++
			}
			cls := fillClass(c.st.fg)
			used[cls] = true
			fmt.Fprintf(b, `<rect class="%s" x="%d" y="%d" width="%d" height="%d"/>`+"\n", cls, x0+i*cellW, y0, (j-i)*cellW, cellH)
			i = j
		case isGlyph(c.s):
			drawGlyph(b, used, c.s, c.st.fg, x0+i*cellW, y0)
			i += c.w
		default:
			j, n := i, 0
			var sb strings.Builder
			for j < len(r) {
				d := r[j]
				if d.w == 0 {
					j++
					continue
				}
				if d.s == " " || d.st != c.st || isGlyph(d.s) || d.s == "█" {
					break
				}
				sb.WriteString(d.s)
				n += d.w
				j++
			}
			cls := ""
			if c.st.fg >= 0 {
				cls = fillClass(c.st.fg)
				used[cls] = true
			}
			if c.st.bold {
				cls = strings.TrimSpace(cls + " b")
			}
			fmt.Fprintf(b, `<text x="%d" y="%d"`, x0+i*cellW, y0+baseline)
			if cls != "" {
				fmt.Fprintf(b, ` class="%s"`, cls)
			}
			if n > 1 {
				fmt.Fprintf(b, ` textLength="%d"`, n*cellW)
			}
			fmt.Fprintf(b, ">%s</text>\n", escape(sb.String()))
			i = j
		}
	}
}

// fillClass is the fill class of a colour (-1 = the default foreground).
func fillClass(fg int) string {
	if fg < 0 {
		return "fd"
	}
	return fmt.Sprintf("f%d", fg)
}

// strokeClass is the stroke class of a colour.
func strokeClass(fg int) string {
	if fg < 0 {
		return "sd"
	}
	return fmt.Sprintf("s%d", fg)
}

// px formats tenths of a pixel as pixels ("720" or "720.5").
func px(tenths int) string {
	if tenths%10 == 0 {
		return fmt.Sprintf("%d", tenths/10)
	}
	return fmt.Sprintf("%d.%d", tenths/10, tenths%10)
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

// escape makes s safe as XML text or attribute value; control characters
// (not allowed in XML) are dropped.
func escape(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return xmlEscaper.Replace(s)
}
