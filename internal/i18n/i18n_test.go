package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// englishSources returns the files that declare Key constants: en.go and
// every en_<feature>.go beside it (tests excluded), so each feature keeps
// its strings in a file of its own.
func englishSources(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("en*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		t.Fatal("no en*.go file found")
	}
	return out
}

// Every Key constant declared in en.go or an en_<feature>.go file must
// have an English string, and every English string a Key constant.
func TestEveryKeyHasEnglish(t *testing.T) {
	re := regexp.MustCompile(`Key = "([^"]+)"`)
	seen := map[string]string{} // key value → file
	for _, file := range englishSources(t) {
		src, err := os.ReadFile(file) // #nosec G304 -- files of this package
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			k := m[1]
			if prev, dup := seen[k]; dup {
				t.Errorf("duplicate key value %q (%s and %s)", k, prev, file)
			}
			seen[k] = file
			if v, ok := en[Key(k)]; !ok || v == "" {
				t.Errorf("key %q (%s) has no English text", k, file)
			}
		}
	}
	for k := range en {
		if _, ok := seen[string(k)]; !ok {
			t.Errorf("en has entry %q without a Key constant", k)
		}
	}
}

// TestEnglishSourcesIncludeFeatureFiles checks that the glob above picks
// up en_<feature>.go files and never a test file.
func TestEnglishSourcesIncludeFeatureFiles(t *testing.T) {
	for _, f := range englishSources(t) {
		if f != "en.go" && !strings.HasPrefix(f, "en_") {
			t.Errorf("unexpected English source %s (use en.go or en_<feature>.go)", f)
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
