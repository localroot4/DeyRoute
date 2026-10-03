package termsvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// cellsOf returns the text and styles of one parsed row.
func cellsOf(r []cell) (text string, styles []style) {
	var sb strings.Builder
	for _, c := range r {
		sb.WriteString(c.s)
		if c.w > 0 {
			styles = append(styles, c.st)
		}
	}
	return sb.String(), styles
}

func TestParseSGR(t *testing.T) {
	red, green := style{fg: 1}, style{fg: 2}
	cases := []struct {
		name string
		in   string
		want []style // one per visible character
	}{
		{"plain", "ab", []style{plain, plain}},
		{"red then reset", "\x1b[31ma\x1b[0mb", []style{red, plain}},
		{"empty reset", "\x1b[32ma\x1b[mb", []style{green, plain}},
		{"bold and colour in one", "\x1b[1;36ma", []style{{fg: 6, bold: true}}},
		{"bold off keeps the colour", "\x1b[1;31ma\x1b[22mb", []style{{fg: 1, bold: true}, red}},
		{"default colour keeps bold", "\x1b[1;33ma\x1b[39mb", []style{{fg: 3, bold: true}, {fg: -1, bold: true}}},
		{"bright", "\x1b[90ma\x1b[97mb", []style{{fg: 8}, {fg: 15}}},
		{"unknown codes are ignored", "\x1b[4;5;7;31ma", []style{red}},
		{"256 colours are skipped", "\x1b[38;5;196;32ma", []style{green}},
		{"true colour is skipped", "\x1b[38;2;1;2;3;31ma", []style{red}},
		{"background is ignored", "\x1b[41;32ma", []style{green}},
		{"other sequences are ignored", "\x1b[2K\x1b[?25l\x1b]0;title\x07\x1b[31ma", []style{red}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sc := parse(c.in, 0)
			require.Len(t, sc.rows, 1)
			_, got := cellsOf(sc.rows[0])
			require.Equal(t, c.want, got)
		})
	}
}

func TestParseLayout(t *testing.T) {
	sc := parse("a\tb\n\nc\n\n\n", 0)
	require.Len(t, sc.rows, 3, "trailing blank lines are dropped")
	text, _ := cellsOf(sc.rows[0])
	require.Equal(t, "a       b", text, "a tab moves to the next multiple of 8")
	require.Empty(t, sc.rows[1])

	// Longer lines wrap at Cols, as in a terminal.
	sc = parse("abcdefg\nhi", 3)
	var rows []string
	for _, r := range sc.rows {
		s, _ := cellsOf(r)
		rows = append(rows, s)
	}
	require.Equal(t, []string{"abc", "def", "g", "hi"}, rows)
}

// Every glyph takes the columns ansi.StringWidth gives it, so the picture
// lines up with what the program laid out.
func TestWidths(t *testing.T) {
	for _, s := range []string{"●", "○", "◐", "✔", "✖", "▲", "→", "↓", "↑", "…", "·", "█", "▁", "─", "┤", "═", "╔", "⣿", "›", "—", "日本", "é"} {
		sc := parse(s+"x", 0)
		require.Len(t, sc.rows, 1)
		require.Equal(t, ansi.StringWidth(s)+1, len(sc.rows[0]), s)
		require.Equal(t, "x", sc.rows[0][len(sc.rows[0])-1].s, s)
	}
	// A wide character is placed at its column and advances by two.
	svg := string(Render("日本 x", Options{Theme: Light}))
	require.Contains(t, svg, `textLength="336">日本</text>`)
	require.Contains(t, svg, `<text x="`+strconv.Itoa(padX+5*cellW)+`" y=`)
}

func TestEscaping(t *testing.T) {
	svg := Render(`bash <(curl -fsSL https://x/install.sh) join 'dey://T@a&b' "q"`, Options{Title: `a <b> & "c"`, Theme: Dark})
	s := string(svg)
	require.Contains(t, s, "bash")
	require.Contains(t, s, "&lt;(curl")
	require.Contains(t, s, "&#39;dey://T@a&amp;b&#39;")
	require.Contains(t, s, "&quot;q&quot;")
	require.Contains(t, s, "<title id=\"title\">a &lt;b&gt; &amp; &quot;c&quot;</title>")
	require.NotContains(t, s, "<(")
	wellFormed(t, svg)
}

// wellFormed decodes the whole document with encoding/xml.
func wellFormed(t *testing.T, svg []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(svg))
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		require.NoError(t, err, string(svg))
	}
}

// sample is a screen with every kind of cell.
const sample = "\x1b[36m ██████╗ ██╗\x1b[0m\n" +
	"\x1b[36m ╚═════╝ ╚═╝\x1b[0m\n" +
	" \x1b[1mTUNNELS\x1b[0m  \x1b[32m● UP\x1b[0m \x1b[33m◐ DEGR\x1b[0m \x1b[31m○ DOWN\x1b[0m\n" +
	" \x1b[1;32m✔\x1b[0m done \x1b[31m✖\x1b[0m failed \x1b[1;33m▲ WARN \x1b[0m\n" +
	" 10 Mb/s ┤\x1b[36m▁▂▃▄▅▆▇█\x1b[0m ⣿⡀ ░▒▓ ▌▐ ▀\n" +
	"          └──┬──┴──┼──┘ ┌┐ ├┤ ╭╮╰╯ ━┃ ║ ╔╗╚╝\n" +
	" ↓ 12.3 Mb/s  ↑ 1.20 Mb/s · today…  \x1b[90mgray\x1b[0m"

func TestRenderDeterministicAndWellFormed(t *testing.T) {
	for _, th := range Themes {
		o := Options{Cols: 80, Title: "dey — sample", Theme: th}
		a, b := Render(sample, o), Render(sample, o)
		require.Equal(t, a, b)
		wellFormed(t, a)
		s := string(a)
		require.True(t, strings.HasPrefix(s, `<svg xmlns="http://www.w3.org/2000/svg" width="720" height="`), s[:120])
		require.Contains(t, s, `role="img"`)
		require.Contains(t, s, "<title id=\"title\">dey — sample</title>")
		require.Contains(t, s, th.Background)
		require.NotContains(t, s, "<script")
		require.NotContains(t, s, "@import")
		require.NotContains(t, s, "url(")
		require.NotContains(t, s, "href")
		// Glyphs are shapes, not text.
		for _, g := range []string{"█", "╗", "●", "◐", "○", "✔", "✖", "▲", "▁", "⣿", "─", "┤"} {
			require.NotContains(t, s, ">"+g, g)
			require.NotContains(t, s, g+"<", g)
		}
		// The six full blocks of the first row are one rectangle.
		require.Contains(t, s, `width="504" height="176"/>`)
		// Text keeps its colours and weight.
		require.Contains(t, s, `class="b" textLength="588">TUNNELS</text>`)
		require.Contains(t, s, `class="f8" textLength="336">gray</text>`)
		require.Contains(t, s, `class="f3 b" textLength="336">WARN</text>`)
	}
	require.NotEqual(t, Render(sample, Options{Theme: Light}), Render(sample, Options{Theme: Dark}))
}

// Every number in the picture is an integer, except the pixel size (one
// decimal) and the shade opacities: no float formatting that could differ
// between machines.
func TestNumbersAreIntegers(t *testing.T) {
	s := string(Render(sample, Options{Cols: 81, Title: "x", Theme: Light}))
	attr := regexp.MustCompile(` (x|y|width|height|cx|cy|r|rx|textLength|stroke-width)="([^"]*)"`)
	ms := attr.FindAllStringSubmatch(s, -1)
	require.NotEmpty(t, ms)
	integer := regexp.MustCompile(`^-?\d+(\.\d)?$`)
	for _, m := range ms {
		require.Regexp(t, integer, m[2], m[0])
	}
	for _, d := range regexp.MustCompile(` d="([^"]*)"`).FindAllStringSubmatch(s, -1) {
		require.Regexp(t, `^[MLAZ0-9 -]+$`, d[1])
	}
	require.Contains(t, s, `width="728.4"`, "81 columns are 728.4 px wide")
}

// relLuminance is the WCAG relative luminance of #rrggbb.
func relLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	require.Len(t, hex, 7, hex)
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	require.NoError(t, err)
	lin := func(c uint64) float64 {
		s := float64(c) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(v>>16&0xff) + 0.7152*lin(v>>8&0xff) + 0.0722*lin(v&0xff)
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := relLuminance(t, a), relLuminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Every text colour is readable on its background (WCAG AA, 4.5:1).
func TestThemeContrast(t *testing.T) {
	for _, th := range Themes {
		require.GreaterOrEqual(t, contrast(t, th.Foreground, th.Background), 4.5, th.Name)
		require.GreaterOrEqual(t, contrast(t, th.Title, th.Background), 4.5, th.Name)
		for i, c := range th.ANSI {
			require.GreaterOrEqualf(t, contrast(t, c, th.Background), 4.5, "%s colour %d (%s)", th.Name, i, c)
		}
	}
}

func TestSync(t *testing.T) {
	dir := t.TempDir()
	pics := Pictures("tui-x", "hello", Options{Cols: 10, Title: "x"})
	require.Len(t, pics, 2)
	require.Equal(t, []string{"tui-x-light.svg", "tui-x-dark.svg"}, []string{pics[0].Name, pics[1].Name})

	problems, err := Sync(dir, "tui-", pics, false)
	require.NoError(t, err)
	require.Equal(t, []string{"tui-x-dark.svg is missing: " + StaleHint, "tui-x-light.svg is missing: " + StaleHint}, problems)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tui-old-light.svg"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cli-other-light.svg"), []byte("x"), 0o600))
	problems, err = Sync(dir, "tui-", pics, true)
	require.NoError(t, err)
	require.Empty(t, problems)
	require.NoFileExists(t, filepath.Join(dir, "tui-old-light.svg"), "an orphan is removed when writing")
	require.FileExists(t, filepath.Join(dir, "cli-other-light.svg"), "other prefixes are left alone")

	problems, err = Sync(dir, "tui-", pics, false)
	require.NoError(t, err)
	require.Empty(t, problems)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tui-old-dark.svg"), []byte("x"), 0o600))
	changed := Pictures("tui-x", "hello!", Options{Cols: 10, Title: "x"})
	problems, err = Sync(dir, "tui-", changed, false)
	require.NoError(t, err)
	require.Equal(t, []string{
		"tui-old-dark.svg belongs to no screen: " + StaleHint,
		"tui-x-dark.svg is " + StaleHint,
		"tui-x-light.svg is " + StaleHint,
	}, problems)
}
