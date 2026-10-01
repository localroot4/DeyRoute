package doctor

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// ansiProfile is termenv.ANSI (16 colors): the caller decides whether the
// terminal has color, so Summary does not second-guess it by probing
// stdout; 16 colors work on every color terminal.
const ansiProfile = 2

// colorRenderer renders colored words regardless of where the text goes.
var colorRenderer = func() *lipgloss.Renderer {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(ansiProfile)
	return r
}()

// severity presentation: rank (lower first), word key, symbols, color.
type sevStyle struct {
	rank    int
	word    i18n.Key
	unicode string
	ascii   string
	color   lipgloss.Color
}

var sevStyles = map[string]sevStyle{
	SevError: {0, i18n.DoctorSevError, "✖", "x", lipgloss.Color("1")},
	SevWarn:  {1, i18n.DoctorSevWarn, "▲", "!", lipgloss.Color("3")},
	SevInfo:  {2, i18n.DoctorSevInfo, "●", "i", lipgloss.Color("6")},
	SevOK:    {3, i18n.DoctorSevOK, "✔", "+", lipgloss.Color("2")},
}

func styleOf(sev string) sevStyle {
	if s, ok := sevStyles[sev]; ok {
		return s
	}
	return sevStyles[SevInfo]
}

// label renders "✖ ERROR" (always the word, never color only; section 6).
func label(sev string, unicode, color bool) string {
	s := styleOf(sev)
	sym := s.unicode
	if !unicode {
		sym = s.ascii
	}
	text := fmt.Sprintf("%s %-5s", sym, i18n.T(s.word))
	if !color {
		return text
	}
	return colorRenderer.NewStyle().Foreground(s.color).Bold(true).Render(text)
}

// SortFindings orders findings by severity (error, warn, info, ok), keeping
// rule order within a severity.
func SortFindings(findings []api.DoctorFinding) []api.DoctorFinding {
	out := append([]api.DoctorFinding(nil), findings...)
	sort.SliceStable(out, func(i, j int) bool {
		return styleOf(out[i].Severity).rank < styleOf(out[j].Severity).rank
	})
	return out
}

// Summary renders the terminal summary of at most SummaryMaxLines lines:
// a title, an overview of tunnels and nodes, the result counts and each
// finding (severity word, rule, message, fix), most severe first. When the
// findings do not fit, the last line says how many more are in the bundle.
// With color the severity labels are colored (lipgloss); the words ERROR,
// WARN, INFO and OK are always present. Without unicode only ASCII is used.
func Summary(findings []api.DoctorFinding, st api.Status, unicode, color bool) string {
	sep := " · "
	if !unicode {
		sep = " - "
	}
	var lines []string
	role := cleanText(st.Role)
	if role == "" {
		role = "?"
	}
	ver := cleanText(st.Version)
	if ver == "" {
		ver = "?"
	}
	when := "-"
	if !st.GeneratedAt.IsZero() {
		when = st.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC")
	}
	lines = append(lines, i18n.T(i18n.DoctorTitle, role, ver, when))
	lines = append(lines, overview(st))

	sorted := SortFindings(findings)
	if len(sorted) == 0 {
		lines = append(lines, label(SevOK, unicode, color)+" "+i18n.T(i18n.DoctorHealthy))
		return finish(lines, unicode)
	}
	var nErr, nWarn, nInfo int
	for _, f := range sorted {
		switch f.Severity {
		case SevError:
			nErr++
		case SevWarn:
			nWarn++
		case SevInfo:
			nInfo++
		}
	}
	lines = append(lines, i18n.T(i18n.DoctorCounts, nErr, nWarn, nInfo))

	for i, f := range sorted {
		block := []string{label(f.Severity, unicode, color) + " " + cleanText(f.Rule) + sep + cleanText(f.Message)}
		if fix := cleanText(f.Fix); fix != "" {
			block = append(block, "    "+i18n.T(i18n.DoctorFix, fix))
		}
		rest := len(sorted) - i - 1
		budget := SummaryMaxLines - len(lines)
		if rest > 0 {
			budget-- // keep one line for "… N more"
		}
		if len(block) > budget {
			lines = append(lines, i18n.T(i18n.DoctorMore, len(sorted)-i))
			break
		}
		lines = append(lines, block...)
	}
	return finish(lines, unicode)
}

// overview is the second summary line.
func overview(st api.Status) string {
	if st.Role == "node" && st.NodeSelf != nil {
		conn := i18n.T(i18n.DoctorHubDisconnected)
		if st.NodeSelf.Connected {
			conn = i18n.T(i18n.DoctorHubConnected)
		}
		return i18n.T(i18n.DoctorOverviewNode, cleanText(st.NodeSelf.ID), cleanText(st.NodeSelf.HubAddr), conn)
	}
	up, online := 0, 0
	for _, t := range st.Tunnels {
		if t.State == state.StateUp || t.State == state.StateDegraded {
			up++
		}
	}
	for _, n := range st.Nodes {
		if n.Online {
			online++
		}
	}
	return i18n.T(i18n.DoctorOverviewHub, up, len(st.Tunnels), online, len(st.Nodes))
}

// finish redacts, enforces the line limit and the ASCII mode.
func finish(lines []string, unicode bool) string {
	if len(lines) > SummaryMaxLines {
		lines = lines[:SummaryMaxLines]
	}
	var b strings.Builder
	for _, l := range lines {
		l = dlog.Redact(l)
		if !unicode {
			l = toASCII(l)
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// asciiReplacer maps the typographic characters used in texts to ASCII.
var asciiReplacer = strings.NewReplacer("…", "...", "·", "-", "→", "->", "←", "<-", "—", "-", "–", "-", "✔", "+", "✖", "x", "▲", "!", "●", "*")

// toASCII replaces known symbols and drops any other non-ASCII rune
// (ANSI escape sequences are ASCII and kept).
func toASCII(s string) string {
	s = asciiReplacer.Replace(s)
	return strings.Map(func(r rune) rune {
		if r > 0x7e || (r < 0x20 && r != '\t' && r != 0x1b) {
			return '?'
		}
		return r
	}, s)
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// StripANSI removes terminal escape sequences (the summary file is plain).
func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }
