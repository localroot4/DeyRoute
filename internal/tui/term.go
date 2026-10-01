package tui

import (
	"os"
	"strings"
)

// Caps describes what the terminal can render.
type Caps struct {
	Unicode bool // UTF-8 box drawing and symbols
	Color   bool // ANSI colors
	Width   int  // columns; 0 when unknown
	// Dumb terminals (TERM=dumb or unset) cannot move the cursor: Run then
	// prints each page as plain lines and reads one answer per line.
	Dumb bool
}

// Narrow reports whether tables must drop low-priority columns (< 100 cols).
func (c Caps) Narrow() bool { return c.Width > 0 && c.Width < 100 }

// DetectCaps inspects the environment. TERM=dumb, NO_COLOR and non-UTF-8
// locales degrade to plain ASCII without color (scenario S24).
func DetectCaps(getenv func(string) string) Caps {
	if getenv == nil {
		getenv = os.Getenv
	}
	term := getenv("TERM")
	c := Caps{Unicode: localeIsUTF8(getenv), Color: true}
	if term == "" || term == "dumb" {
		c.Color = false
		c.Unicode = false
		c.Dumb = true
	}
	if getenv("NO_COLOR") != "" {
		c.Color = false
	}
	if getenv("DEYROUTE_ASCII") == "1" {
		c.Unicode = false
	}
	return c
}

func localeIsUTF8(getenv func(string) string) bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return false
}
