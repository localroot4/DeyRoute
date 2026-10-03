package termsvg

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

// Line weights of the box-drawing characters.
const (
	none   = 0
	light  = 1
	heavy  = 2
	double = 3
)

// Thicknesses and offsets of box-drawing lines, in tenths of a pixel.
const (
	lightW  = 12
	heavyW  = 24
	doubleW = 10
	doubleD = 22 // distance of each double line from the cell's centre line
)

// arms are the four arms of a box-drawing character: up, right, down, left.
type arms [4]uint8

// boxes are the box-drawing characters drawn as shapes.
var boxes = map[rune]arms{
	'─': {0, light, 0, light}, '━': {0, heavy, 0, heavy},
	'│': {light, 0, light, 0}, '┃': {heavy, 0, heavy, 0},
	'┌': {0, light, light, 0}, '┐': {0, 0, light, light},
	'└': {light, light, 0, 0}, '┘': {light, 0, 0, light},
	'╭': {0, light, light, 0}, '╮': {0, 0, light, light},
	'╰': {light, light, 0, 0}, '╯': {light, 0, 0, light},
	'├': {light, light, light, 0}, '┤': {light, 0, light, light},
	'┬': {0, light, light, light}, '┴': {light, light, 0, light},
	'┼': {light, light, light, light},
	'═': {0, double, 0, double}, '║': {double, 0, double, 0},
	'╔': {0, double, double, 0}, '╗': {0, 0, double, double},
	'╚': {double, double, 0, 0}, '╝': {double, 0, 0, double},
}

// isGlyph reports whether s is drawn as a shape rather than as text.
func isGlyph(s string) bool {
	r, n := utf8.DecodeRuneInString(s)
	if n != len(s) {
		return false
	}
	if _, ok := boxes[r]; ok {
		return true
	}
	switch {
	case r >= 0x2580 && r <= 0x2593: // block elements
		return true
	case r >= 0x2801 && r <= 0x28ff: // braille patterns (U+2800 is blank)
		return true
	}
	switch r {
	case '●', '○', '◐', '✔', '✓', '✖', '✗', '▲':
		return true
	}
	return false
}

// drawGlyph writes the shapes of glyph s in colour fg into the cell whose
// top-left corner is (x, y).
func drawGlyph(b *bytes.Buffer, used map[string]bool, s string, fg, x, y int) {
	r, _ := utf8.DecodeRuneInString(s)
	f := fillClass(fg)
	rect := func(rx, ry, w, h int) {
		used[f] = true
		fmt.Fprintf(b, `<rect class="%s" x="%d" y="%d" width="%d" height="%d"/>`+"\n", f, rx, ry, w, h)
	}
	if a, ok := boxes[r]; ok {
		drawBox(rect, a, x, y)
		return
	}
	cx, cy := x+cellW/2, y+cellH/2
	switch {
	case r >= 0x2581 && r <= 0x2588: // ▁▂▃▄▅▆▇█: eighths from the bottom
		h := int(r-0x2580) * cellH / 8
		rect(x, y+cellH-h, cellW, h)
		return
	case r == 0x2580: // ▀
		rect(x, y, cellW, cellH/2)
		return
	case r >= 0x2589 && r <= 0x258f: // ▉▊▋▌▍▎▏: eighths from the left
		w := int(0x2590-r) * cellW / 8
		rect(x, y, w, cellH)
		return
	case r == 0x2590: // ▐
		rect(x+cellW/2, y, cellW/2, cellH)
		return
	case r >= 0x2591 && r <= 0x2593: // ░▒▓
		used[f] = true
		fmt.Fprintf(b, `<rect class="%s" x="%d" y="%d" width="%d" height="%d" fill-opacity="0.%d"/>`+"\n",
			f, x, y, cellW, cellH, 25*int(r-0x2590))
		return
	case r >= 0x2801 && r <= 0x28ff:
		drawBraille(b, used, f, int(r-0x2800), x, y)
		return
	}
	k := strokeClass(fg)
	switch r {
	case '●':
		used[f] = true
		fmt.Fprintf(b, `<circle class="%s" cx="%d" cy="%d" r="30"/>`+"\n", f, cx, cy)
	case '○':
		used[k] = true
		fmt.Fprintf(b, `<circle class="k %s" cx="%d" cy="%d" r="26" stroke-width="10"/>`+"\n", k, cx, cy)
	case '◐':
		used[k], used[f] = true, true
		fmt.Fprintf(b, `<circle class="k %s" cx="%d" cy="%d" r="26" stroke-width="10"/>`+"\n", k, cx, cy)
		fmt.Fprintf(b, `<path class="%s" d="M%d %dA30 30 0 0 0 %d %dZ"/>`+"\n", f, cx, cy-30, cx, cy+30)
	case '✔', '✓':
		used[k] = true
		fmt.Fprintf(b, `<path class="k %s" d="M%d %dL%d %dL%d %d" stroke-width="16"/>`+"\n",
			k, x+14, cy+2, x+34, cy+26, x+72, cy-30)
	case '✖', '✗':
		used[k] = true
		fmt.Fprintf(b, `<path class="k %s" d="M%d %dL%d %dM%d %dL%d %d" stroke-width="16"/>`+"\n",
			k, x+18, cy-24, x+66, cy+24, x+66, cy-24, x+18, cy+24)
	case '▲':
		used[f] = true
		fmt.Fprintf(b, `<path class="%s" d="M%d %dL%d %dL%d %dZ"/>`+"\n", f, cx, cy-34, x+78, cy+30, x+6, cy+30)
	}
}

// drawBraille draws the raised dots of a braille pattern (bits as in
// Unicode: dots 1-3 and 7 down the left column, 4-6 and 8 down the right).
func drawBraille(b *bytes.Buffer, used map[string]bool, f string, bits, x, y int) {
	dots := [8][2]int{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {0, 3}, {1, 3}}
	used[f] = true
	for i, d := range dots {
		if bits&(1<<i) == 0 {
			continue
		}
		fmt.Fprintf(b, `<circle class="%s" cx="%d" cy="%d" r="11"/>`+"\n", f, x+cellW/4+d[0]*cellW/2, y+cellH/8+d[1]*cellH/4)
	}
}

// drawBox draws a box-drawing character from its arms.
func drawBox(rect func(x, y, w, h int), a arms, x, y int) {
	cx, cy := x+cellW/2, y+cellH/2
	right, bottom := x+cellW, y+cellH
	// hline and vline draw a segment of thickness t between two points.
	hline := func(x1, x2, yc, t int) { rect(min(x1, x2), yc-t/2, abs(x2-x1), t) }
	vline := func(y1, y2, xc, t int) { rect(xc-t/2, min(y1, y2), t, abs(y2-y1)) }
	isDouble := a[0] == double || a[1] == double || a[2] == double || a[3] == double
	if !isDouble {
		for i, w := range a {
			if w == none {
				continue
			}
			t := lightW
			if w == heavy {
				t = heavyW
			}
			switch i {
			case 0:
				vline(y, cy+t/2, cx, t)
			case 1:
				hline(cx-t/2, right, cy, t)
			case 2:
				vline(cy-t/2, bottom, cx, t)
			case 3:
				hline(x, cx+t/2, cy, t)
			}
		}
		return
	}
	d, t := doubleD, doubleW
	up, rt, dn, lt := a[0] != none, a[1] != none, a[2] != none, a[3] != none
	switch {
	case lt && rt && !up && !dn: // ═
		hline(x, right, cy-d, t)
		hline(x, right, cy+d, t)
	case up && dn && !lt && !rt: // ║
		vline(y, bottom, cx-d, t)
		vline(y, bottom, cx+d, t)
	default: // a corner: one horizontal and one vertical arm
		h := 1 // +1 = the horizontal arm goes right, -1 = left
		if lt {
			h = -1
		}
		v := 1 // +1 = the vertical arm goes down, -1 = up
		if up {
			v = -1
		}
		hEdge, vEdge := right, bottom
		if h < 0 {
			hEdge = x
		}
		if v < 0 {
			vEdge = y
		}
		// The outer line turns on the far side of the corner, the inner
		// line on the near side.
		for _, s := range []int{-1, 1} {
			ox, oy := cx+s*h*d, cy+s*v*d
			hline(ox-h*t/2, hEdge, oy, t)
			vline(oy-v*t/2, vEdge, ox, t)
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
