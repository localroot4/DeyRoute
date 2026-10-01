package tui

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// A line wider than the window is word-wrapped with its indentation; the
// banner art is cut instead (wrapping would break the drawing).
func TestFitWrapsLongLines(t *testing.T) {
	a := &app{caps: Caps{Unicode: true, Width: 30}}
	page := "ART that is wider than thirty columns\n status\n\n" +
		"   Removing node nl-1 deletes its tunnels and its certificate for good.\nfooter\n"
	out := a.fit(page, 1)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	for _, l := range lines {
		require.LessOrEqual(t, ansi.StringWidth(l), 30, "%q", l)
	}
	require.Equal(t, "ART that is wider than thirty ", lines[0])
	require.Equal(t, []string{
		"   Removing node nl-1",
		"   deletes its tunnels and its",
		"   certificate for good.",
	}, lines[3:6])
	require.Equal(t, "footer", lines[6])
	// Nothing changes when the page fits.
	a.caps.Width = 200
	require.Equal(t, page, a.fit(page, 1))
}

// A page taller than the window loses the art first, then the lines just
// above the footer, with a note; the title and an error's code line stay.
func TestFitBudgetsTheHeight(t *testing.T) {
	var b strings.Builder
	b.WriteString("art1\nart2\nart3\n status\n\n Title\n\n ✖ DEY-B003  failed\n   Why:  x\n   Fix:  y\n")
	for i := 0; i < 20; i++ {
		b.WriteString("   | log line\n")
	}
	b.WriteString(" 1) Retry\n\nfooter\n")
	page := b.String()

	a := &app{caps: Caps{Unicode: true, Width: 80}, height: 12}
	require.Equal(t, page, a.fit(page, 3), "the height is budgeted only once the window reported its size")
	a.sized = true
	lines := strings.Split(strings.TrimSuffix(a.fit(page, 3), "\n"), "\n")
	require.Len(t, lines, 12)
	require.Equal(t, " status", lines[0])
	require.Equal(t, " ✖ DEY-B003  failed", lines[4])
	require.Equal(t, "   Fix:  y", lines[6])
	require.Equal(t, " (19 more lines: make the window taller to see them)", lines[8])
	require.Equal(t, []string{" 1) Retry", "", "footer"}, lines[9:])

	// Only the art goes when that is enough.
	a.height = 30
	lines = strings.Split(strings.TrimSuffix(a.fit(page, 3), "\n"), "\n")
	require.Len(t, lines, 30)
	require.Equal(t, " status", lines[0])
	require.NotContains(t, strings.Join(lines, "\n"), "more lines")
}

// The join command is shown whole on the plain terminal; Enter returns.
func TestPlainPage(t *testing.T) {
	var out bytes.Buffer
	p := &plainPage{text: " Run this:\n\nbash <(curl) join 'dey://T@1.2.3.4:1#fp'\n"}
	p.SetStdin(strings.NewReader("\n"))
	p.SetStdout(&out)
	p.SetStderr(io.Discard)
	require.NoError(t, p.Run())
	require.Equal(t, "\n Run this:\n\nbash <(curl) join 'dey://T@1.2.3.4:1#fp'\n", out.String())

	p.SetStdin(strings.NewReader("")) // end of input is not an error
	require.NoError(t, p.Run())
}
