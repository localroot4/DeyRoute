package termsvg

import "strconv"

// Theme is the palette of one picture. ANSI holds the 16 terminal colours
// (0-7 normal, 8-15 bright); each one, and Foreground, keeps a contrast of
// at least 4.5:1 against Background (WCAG AA, checked by the tests).
type Theme struct {
	Name       string
	Background string
	Foreground string
	Border     string
	Title      string
	Dots       [3]string
	ANSI       [16]string
}

// color returns the colour of a class suffix: "d" is the foreground,
// "0".."15" an ANSI colour.
func (t Theme) color(k string) string {
	if k == "d" {
		return t.Foreground
	}
	n, err := strconv.Atoi(k)
	if err != nil || n < 0 || n > 15 {
		return t.Foreground
	}
	return t.ANSI[n]
}

// windowDots are the three title-bar buttons.
var windowDots = [3]string{"#ff5f57", "#febc2e", "#28c840"}

// Light matches a light page (the README on a light theme). The muted and
// yellow tones are darker than a terminal's usual ones so that they stay
// readable on the light background.
var Light = Theme{
	Name:       "light",
	Background: "#f6f8fa",
	Foreground: "#1f2328",
	Border:     "#d1d9e0",
	Title:      "#59636e",
	Dots:       windowDots,
	ANSI: [16]string{
		"#1f2328", // black
		"#b42318", // red
		"#116329", // green
		"#7d4e00", // yellow
		"#0550ae", // blue
		"#7a3fc4", // magenta
		"#0e7490", // cyan
		"#59636e", // white: muted on a light page
		"#59636e", // bright black (gray)
		"#a40e26", // bright red
		"#116329", // bright green
		"#7d4e00", // bright yellow
		"#0550ae", // bright blue
		"#6639ba", // bright magenta
		"#0e7490", // bright cyan
		"#1f2328", // bright white: the foreground on a light page
	},
}

// Dark matches a dark page (the README on a dark theme).
var Dark = Theme{
	Name:       "dark",
	Background: "#161b22",
	Foreground: "#e6edf3",
	Border:     "#30363d",
	Title:      "#9da7b3",
	Dots:       windowDots,
	ANSI: [16]string{
		"#9da7b3", // black: muted on a dark page
		"#ff7b72", // red
		"#3fb950", // green
		"#d29922", // yellow
		"#58a6ff", // blue
		"#bc8cff", // magenta
		"#39c5cf", // cyan
		"#c9d1d9", // white
		"#9da7b3", // bright black (gray)
		"#ffa198", // bright red
		"#56d364", // bright green
		"#e3b341", // bright yellow
		"#79c0ff", // bright blue
		"#d2a8ff", // bright magenta
		"#56d4dd", // bright cyan
		"#f0f6fc", // bright white
	},
}

// Themes are the themes every screen is drawn in.
var Themes = []Theme{Light, Dark}
