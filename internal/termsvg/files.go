package termsvg

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StaleHint is what to run when the committed pictures are out of date.
const StaleHint = "stale; run make screens"

// Picture is one generated file: its base name and its content.
type Picture struct {
	Name string
	Data []byte
}

// Pictures draws one screen in every theme as <name>-<theme>.svg.
func Pictures(name, text string, o Options) []Picture {
	var out []Picture
	for _, t := range Themes {
		o.Theme = t
		out = append(out, Picture{Name: name + "-" + t.Name + ".svg", Data: Render(text, o)})
	}
	return out
}

// Sync keeps the pictures whose names start with prefix in dir equal to
// pics. With write it rewrites the files and removes the other files with
// that prefix; without it, it changes nothing and returns one problem per
// picture that differs or is missing and per file no picture produces.
func Sync(dir, prefix string, pics []Picture, write bool) ([]string, error) {
	want := map[string]bool{}
	var problems []string
	for _, p := range pics {
		want[p.Name] = true
		path := filepath.Join(dir, p.Name)
		if write {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, err
			}
			err := os.WriteFile(path, p.Data, 0o644) // #nosec G306 -- a documentation image, readable by everyone
			if err != nil {
				return nil, err
			}
			continue
		}
		got, err := os.ReadFile(path) // #nosec G304 -- a file of the docs directory
		switch {
		case errors.Is(err, fs.ErrNotExist):
			problems = append(problems, p.Name+" is missing: "+StaleHint)
		case err != nil:
			return nil, err
		case !bytes.Equal(got, p.Data):
			problems = append(problems, p.Name+" is "+StaleHint)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".svg") || want[n] {
			continue
		}
		if write {
			if err := os.Remove(filepath.Join(dir, n)); err != nil {
				return nil, err
			}
			continue
		}
		problems = append(problems, n+" belongs to no screen: "+StaleHint)
	}
	sort.Strings(problems)
	return problems, nil
}
