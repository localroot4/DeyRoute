// Package i18n holds every user-facing TUI/CLI string. Menu code must never
// hard-code text; it calls T(key). Only English exists in v1; a Persian table
// can be added later next to en.go and selected with `deyroute settings language fa`.
package i18n

import (
	"fmt"
	"sort"
	"sync"
)

// Key identifies one translatable string.
type Key string

var (
	mu        sync.RWMutex
	current   = "en"
	languages = map[string]map[Key]string{"en": en}
)

// T returns the string for k in the current language, falling back to English.
// When args are given the string is used as a fmt format.
func T(k Key, args ...any) string {
	mu.RLock()
	tbl := languages[current]
	mu.RUnlock()
	s, ok := tbl[k]
	if !ok {
		s, ok = en[k]
		if !ok {
			return string(k)
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// SetLanguage switches the active language. Unknown languages return an error.
func SetLanguage(lang string) error {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := languages[lang]; !ok {
		return fmt.Errorf("unknown language %q", lang)
	}
	current = lang
	return nil
}

// Language returns the active language code.
func Language() string {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Languages lists available language codes.
func Languages() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(languages))
	for l := range languages {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
