package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestConfigShowValidateApply(t *testing.T) {
	e := newEnv(t)
	require.Contains(t, e.fail(1, "config", "show"), "DEY-C014")
	require.Contains(t, e.fail(1, "config", "validate"), "not set up yet")
	e.writeConfig(hubConfig)
	out := e.ok("config", "show")
	require.Equal(t, hubConfig, out)
	doc := e.json("config", "show")
	require.Equal(t, config.DefaultPath, doc["path"])
	require.Equal(t, "hub", doc["config"].(map[string]any)["role"])
	require.Contains(t, e.ok("config", "validate"), "/etc/deyroute/config.yaml is valid: 1 tunnel(s), 1 node(s).")
	doc = e.json("config", "validate")
	require.Equal(t, true, doc["valid"])
	e.writeConfig(nodeConfig)
	require.Contains(t, e.ok("config", "validate"), "is valid (node)")
	require.Contains(t, e.ok("config", "show"), "role: node")

	// Several problems: every DEY error printed, the JSON lists them all.
	bad := strings.Replace(hubConfig, "ladder: default", "ladder: [nope/none]", 1)
	bad = strings.Replace(bad, "target: 127.0.0.1:443", "target: nowhere", 1)
	e.writeConfig(bad)
	errOut := e.fail(1, "config", "validate")
	require.Contains(t, errOut, "DEY-C005")
	require.Contains(t, errOut, "DEY-C004")
	e.fail(1, "config", "validate", "--json")
	require.Contains(t, e.out.String(), `"errors"`)
	e.writeConfig(hubConfig + "bogus: 1\n")
	require.Contains(t, e.fail(1, "config", "validate"), "DEY-C001")
	e.writeConfig("role: [\n")
	e.fail(1, "config", "show", "--json")

	var applied bool
	e.stub.ConfigApplyFn = func(_ context.Context, progress func(api.Step)) (api.ApplyResult, error) {
		applied = true
		steps(progress, api.Step{Title: "render", Status: api.StepOK})
		return api.ApplyResult{Backup: "/var/lib/deyroute/backups/auto/x.tar.gz", Changed: []string{"main"}, Warnings: []string{"w1"}}, nil
	}
	out = e.ok("config", "apply")
	require.True(t, applied)
	for _, want := range []string{"✔ render", "Configuration applied; changed: main", "Automatic backup: /var/lib/deyroute/backups/auto/x.tar.gz", "! w1"} {
		require.Contains(t, out, want)
	}
	doc = e.json("config", "apply")
	require.Equal(t, []any{"main"}, doc["apply"].(map[string]any)["changed"])
	e.down()
	require.Contains(t, e.fail(2, "config", "apply"), "DEY-X003")
}

// editorScript is a fake $EDITOR: each call applies the next edit to the
// file and records what the owner saw.
type editorScript struct {
	t     *testing.T
	edits []func(string) string
	seen  []string
}

func (s *editorScript) edit(_ context.Context, path string) error {
	data, err := os.ReadFile(path)
	require.NoError(s.t, err)
	s.seen = append(s.seen, string(data))
	fi, err := os.Stat(path)
	require.NoError(s.t, err)
	require.Equal(s.t, os.FileMode(0o600), fi.Mode().Perm())
	if len(s.edits) == 0 {
		return nil
	}
	fn := s.edits[0]
	s.edits = s.edits[1:]
	return os.WriteFile(path, []byte(fn(string(data))), 0o600)
}

func TestConfigEditLoop(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	applied := 0
	e.stub.ConfigApplyFn = func(context.Context, func(api.Step)) (api.ApplyResult, error) {
		applied++
		return api.ApplyResult{Changed: []string{"main"}}, nil
	}
	s := &editorScript{t: t, edits: []func(string) string{
		// 1: an unknown transport and a bad target.
		func(c string) string {
			c = strings.Replace(c, "ladder: default", "ladder: [nope/none]", 1)
			return strings.Replace(c, "target: 127.0.0.1:443", "target: nowhere", 1)
		},
		// 2: fix one problem, keep the header lines.
		func(c string) string { return strings.Replace(c, "target: nowhere", "target: 127.0.0.1:443", 1) },
		// 3: fix the other one, and rename the tunnel.
		func(c string) string {
			c = strings.Replace(c, "ladder: [nope/none]", "ladder: default", 1)
			return strings.Replace(c, `name: "Main"`, `name: "Main 443"`, 1)
		},
	}}
	e.g.Editor = s.edit
	out := e.ok("config", "edit")
	require.Len(t, s.seen, 3)
	require.Equal(t, hubConfig, s.seen[0])
	require.True(t, strings.HasPrefix(s.seen[1], editHeaderPrefix+"config.yaml was NOT saved"), s.seen[1])
	require.Contains(t, s.seen[1], "DEY-C005")
	require.Contains(t, s.seen[1], "DEY-C004")
	require.NotContains(t, s.seen[2], "DEY-C004")
	require.Contains(t, s.seen[2], "DEY-C005")
	require.Contains(t, out, "problem(s) found")
	require.Contains(t, out, "/etc/deyroute/config.yaml saved.")
	require.Contains(t, out, "Configuration applied")
	require.Equal(t, 1, applied)
	data, err := os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.Equal(t, strings.Replace(hubConfig, `name: "Main"`, `name: "Main 443"`, 1), string(data))
	require.NotContains(t, string(data), editHeaderPrefix)

	// No change.
	e.g.Editor = (&editorScript{t: t}).edit
	require.Contains(t, e.ok("config", "edit"), "No changes.")
	doc := e.json("config", "edit")
	require.Equal(t, "unchanged", doc["reason"])
	require.Equal(t, 1, applied)

	// Emptying the file aborts, even after an invalid round.
	s = &editorScript{t: t, edits: []func(string) string{
		func(c string) string { return c + "bogus: 1\n" },
		func(string) string { return "# nothing\n\n" },
	}}
	e.g.Editor = s.edit
	require.Contains(t, e.ok("config", "edit"), "The file was emptied")
	require.Contains(t, s.seen[1], "DEY-C001")
	data, err = os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.NotContains(t, string(data), "bogus")

	// The editor saving the same invalid file again ends the loop with the
	// validation error and keeps the edited copy (no edit is lost).
	s = &editorScript{t: t, edits: []func(string) string{
		func(c string) string {
			return strings.Replace(strings.Replace(c, "id: main", "id: Main!", 1), "name: ir-1", "name: ir-1-edited", 1)
		},
		func(c string) string { return c },
	}}
	e.g.Editor = s.edit
	out = e.fail(1, "config", "edit")
	require.Contains(t, out, "DEY-C007")
	m := regexp.MustCompile(`kept in (\S+)`).FindStringSubmatch(out)
	require.Len(t, m, 2, out)
	kept, err := os.ReadFile(m[1])
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(m[1]) })
	require.Contains(t, string(kept), "ir-1-edited")
	require.Contains(t, string(kept), "DEY-C007", "the problems stay on top")

	// Ids are immutable (C018).
	s = &editorScript{t: t, edits: []func(string) string{
		func(c string) string {
			c = strings.Replace(c, "- id: de-1", "- id: de-9", 1)
			return strings.Replace(c, "nodes: [de-1]", "nodes: [de-9]", 1)
		},
		func(string) string { return "" },
	}}
	e.g.Editor = s.edit
	e.ok("config", "edit")
	require.Contains(t, s.seen[1], "DEY-C018")

	// An editor error stops at once; the daemon being down leaves the file
	// saved but reports X003.
	e.g.Editor = func(context.Context, string) error { return deyerr.New(deyerr.X030, deyerr.Params{"command": "vi"}) }
	require.Contains(t, e.fail(2, "config", "edit"), "DEY-X030")
	e.g.Editor = (&editorScript{t: t, edits: []func(string) string{
		func(c string) string { return strings.Replace(c, "ui_mode: simple", "ui_mode: advanced", 1) },
	}}).edit
	e.down()
	errOut := e.fail(2, "config", "edit")
	require.Contains(t, errOut, "DEY-X003")
	require.Contains(t, e.out.String(), "Not applied")
	data, err = os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.NoError(t, err)
	require.Contains(t, string(data), "ui_mode: advanced")

	// A broken config.yaml opens with its problems on top and can be fixed.
	e2 := newEnv(t)
	e2.writeConfig(hubConfig + "bogus: 1\n")
	s = &editorScript{t: t, edits: []func(string) string{
		func(c string) string { return strings.Replace(c, "bogus: 1\n", "", 1) },
	}}
	e2.g.Editor = s.edit
	e2.stub.ConfigApplyFn = func(context.Context, func(api.Step)) (api.ApplyResult, error) { return api.ApplyResult{}, nil }
	doc = e2.json("config", "edit")
	require.Equal(t, true, doc["saved"])
	require.Contains(t, s.seen[0], "DEY-C001")
}

func TestEditHelpers(t *testing.T) {
	require.True(t, emptyYAML([]byte("\n  # a\n#b\n")))
	require.False(t, emptyYAML([]byte("# a\nrole: hub\n")))
	in := []byte(editorHeaderSample() + "role: hub\n" + editHeaderPrefix + "stray\n")
	require.Equal(t, "role: hub\n", string(stripEditHeader(in)))
}

func editorHeaderSample() string {
	return string(editHeader(deyerr.New(deyerr.C001, deyerr.Params{"key": "x", "line": 2})))
}
