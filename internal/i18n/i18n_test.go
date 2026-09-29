package i18n

import (
	"os"
	"regexp"
	"testing"
)

// Every Key constant declared in en.go must have an English string.
func TestEveryKeyHasEnglish(t *testing.T) {
	src, err := os.ReadFile("en.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`Key = "([^"]+)"`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		k := m[1]
		if seen[k] {
			t.Errorf("duplicate key value %q", k)
		}
		seen[k] = true
		if v, ok := en[Key(k)]; !ok || v == "" {
			t.Errorf("key %q has no English text", k)
		}
	}
	for k := range en {
		if !seen[string(k)] {
			t.Errorf("en has entry %q without a Key constant", k)
		}
	}
}

func TestTFallbacks(t *testing.T) {
	if got := T(Key("no.such.key")); got != "no.such.key" {
		t.Fatalf("unknown key should echo, got %q", got)
	}
	if got := T(NotImplemented, "Tunnels"); got != "Tunnels: not implemented yet" {
		t.Fatalf("got %q", got)
	}
	if err := SetLanguage("xx"); err == nil {
		t.Fatal("expected error for unknown language")
	}
	if Language() != "en" {
		t.Fatal("language changed on error")
	}
}
