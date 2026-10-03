package tui

import (
	"context"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	"github.com/localroot4/deyroute/internal/termsvg"
	"github.com/localroot4/deyroute/internal/termsvg/demo"
	"github.com/localroot4/deyroute/internal/version"
)

// writeScreens is set by make screens: the pictures are rewritten instead
// of compared.
var writeScreens = flag.Bool("screens", false, "rewrite the screenshots in docs/assets/screens (make screens)")

const (
	// screensDir holds the screenshots of the README and the guides.
	screensDir = "../../docs/assets/screens"
	// screenCols is the terminal width of the TUI screenshots: 80 columns
	// stay readable on a phone.
	screenCols = 80
)

// screenEnv pins everything a screenshot depends on (the colour profile,
// the version) and restores it when the test ends. It is registered
// before any harness, so it runs after every harness has closed.
func screenEnv(t *testing.T) {
	t.Helper()
	profile := lipgloss.ColorProfile()
	v, c, d := version.Version, version.Commit, version.Date
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		version.Version, version.Commit, version.Date = v, c, d
	})
	lipgloss.SetColorProfile(2) // termenv.ANSI: the 16 colours of every terminal
	version.Set(demo.Version, "", "")
}

// demoStub is the Local API of the demo hub.
func demoStub() *apitest.Stub {
	return &apitest.Stub{
		StatusFn:     func(context.Context) (api.Status, error) { return demo.Status(testNow), nil },
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) { return demo.Tunnels(testNow), nil },
		NodeListFn:   func(context.Context) ([]api.NodeInfo, error) { return demo.Nodes(testNow), nil },
		TrafficFn: func(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
			return demo.Traffic(testNow, q), nil
		},
		PortCheckFn: func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true}, nil
		},
	}
}

// demoHarness opens the TUI on the demo hub.
func demoHarness(t *testing.T, stub *apitest.Stub) *harness {
	return newHarness(t, Options{Caps: Caps{Unicode: true, Color: true, Width: screenCols}, Local: stub})
}

// tuiScreen is one screenshot: shoot returns the screen as the terminal
// shows it (text with colour codes).
type tuiScreen struct {
	name, title string
	shoot       func(t *testing.T) string
}

func tuiScreens() []tuiScreen {
	return []tuiScreen{
		{"main-menu", "deyroute", func(t *testing.T) string {
			return demoHarness(t, demoStub()).view()
		}},
		{"dashboard", "deyroute — 1) Dashboard", func(t *testing.T) string {
			h := demoHarness(t, demoStub())
			h.choose("1")
			waitFor(t, h, "TRAFFIC")
			return h.view()
		}},
		{"add-tunnel", "deyroute — 2) Tunnels → 1) Add tunnel", func(t *testing.T) string {
			v, _ := addTunnelScreens(t)
			return v
		}},
		{"add-tunnel-done", "deyroute — 2) Tunnels → 1) Add tunnel", func(t *testing.T) string {
			_, v := addTunnelScreens(t)
			return v
		}},
		{"nodes", "deyroute — 3) Nodes", func(t *testing.T) string {
			h := demoHarness(t, demoStub())
			h.choose("3")
			waitFor(t, h, "198.51.100.21")
			return h.view()
		}},
		{"traffic", "deyroute — 6) Diagnostics → Traffic and load", func(t *testing.T) string {
			h := demoHarness(t, demoStub())
			h.choose("6").choose("6").choose("1")
			h.press("2")
			waitFor(t, h, "Traffic and load: main")
			waitFor(t, h, "[24h]")
			return h.view()
		}},
	}
}

// addTunnelScreens adds a tunnel in the wizard and returns the progress
// screen while the hub is starting the tunnel (the add call waits until
// the screen is taken) and the screen after it finished.
func addTunnelScreens(t *testing.T) (running, done string) {
	reached, release := make(chan struct{}), make(chan struct{})
	stub := demoStub()
	stub.TunnelAddFn = func(ctx context.Context, _ api.TunnelAddRequest, progress func(api.Step)) (api.TunnelInfo, error) {
		steps := demo.AddSteps()
		for _, s := range steps[:4] {
			progress(api.Step{ID: s.ID, Title: s.Title, Status: api.StepRunning})
			progress(s)
		}
		start := steps[4]
		progress(api.Step{ID: start.ID, Title: start.Title, Status: api.StepRunning})
		close(reached)
		select {
		case <-release:
		case <-ctx.Done():
			return api.TunnelInfo{}, ctx.Err()
		}
		progress(start)
		progress(steps[5])
		return demo.AddedTunnel(testNow), nil
	}
	h := demoHarness(t, stub)
	h.choose("2").choose("1")
	h.choose("1")
	h.typeLine("443,2053")
	waitFor(t, h, "3. Confirm")
	h.press("enter")
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the tunnel was not added")
	}
	waitFor(t, h, "firewall ✔  443/tcp, 2053/tcp")
	running = h.view()
	close(release)
	waitFor(t, h, "Tunnel main is UP via backhaul/wssmux (41ms)")
	return running, h.view()
}

// waitFor settles h until its screen, without colours, shows s.
func waitFor(t *testing.T, h *harness, s string) {
	t.Helper()
	for i := 0; i < 50 && !strings.Contains(ansi.Strip(h.view()), s); i++ {
		h.settle()
	}
	require.Contains(t, ansi.Strip(h.view()), s)
}

// TestScreens draws the TUI screenshots of the README and the guides and
// checks that docs/assets/screens holds exactly them; make screens
// rewrites them (go test -run TestScreens -screens). Any change to a
// screen or to an English text it shows needs make screens.
func TestScreens(t *testing.T) {
	screenEnv(t)
	var pics []termsvg.Picture
	for _, s := range tuiScreens() {
		a, b := s.shoot(t), s.shoot(t)
		require.Equal(t, a, b, "screen %s differs between two runs", s.name)
		require.NotContains(t, a, os.TempDir(), "screen %s shows a temporary path", s.name)
		pics = append(pics, termsvg.Pictures("tui-"+s.name, a, termsvg.Options{Cols: screenCols, Title: s.title})...)
	}
	problems, err := termsvg.Sync(screensDir, "tui-", pics, *writeScreens)
	require.NoError(t, err)
	if len(problems) > 0 {
		t.Fatalf("docs/assets/screens:\n%s", strings.Join(problems, "\n"))
	}
}

// screenRef finds the screenshot references of a Markdown file: src and
// srcset attributes and Markdown image targets.
var screenRef = regexp.MustCompile(`(?:src|srcset)="([^"]+)"|!\[[^\]]*\]\(([^)\s]+)`)

// screenRefs returns the screenshot files a Markdown file references, in
// order, as paths relative to the repository root.
func screenRefs(t *testing.T, root, file string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, file)) // #nosec G304 -- a file of the repository
	require.NoError(t, err)
	var out []string
	for _, m := range screenRef.FindAllStringSubmatch(string(data), -1) {
		ref := m[1] + m[2]
		if !strings.Contains(ref, "assets/screens/") {
			continue
		}
		p := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(file), ref)))
		out = append(out, p)
	}
	return out
}

// Every screenshot the README files and the guides reference exists, every
// screenshot is referenced, and both README files show the same pictures
// in the same order.
func TestScreensReferenced(t *testing.T) {
	root := filepath.Join("..", "..")
	files := []string{"README.md", "README.en.md"}
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, err := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return err
	}))
	used := map[string]bool{}
	for _, f := range files {
		for _, ref := range screenRefs(t, root, f) {
			_, err := os.Stat(filepath.Join(root, ref))
			require.NoError(t, err, "%s references a missing screenshot", f)
			used[ref] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "docs", "assets", "screens"))
	require.NoError(t, err)
	var unused []string
	for _, e := range entries {
		ref := "docs/assets/screens/" + e.Name()
		if strings.HasSuffix(e.Name(), ".svg") && !used[ref] {
			unused = append(unused, ref)
		}
	}
	sort.Strings(unused)
	require.Empty(t, unused, "screenshots no document shows")

	fa, en := screenRefs(t, root, "README.md"), screenRefs(t, root, "README.en.md")
	require.NotEmpty(t, en)
	require.Equal(t, en, fa, "README.md and README.en.md must show the same screenshots in the same order")
	// Each picture comes in both themes, dark first (<source>) then light
	// (<img>).
	for i := 0; i+1 < len(en); i += 2 {
		require.True(t, strings.HasSuffix(en[i], "-dark.svg"), en[i])
		require.Equal(t, strings.TrimSuffix(en[i], "-dark.svg")+"-light.svg", en[i+1])
	}
	require.True(t, slices.ContainsFunc(en, func(s string) bool { return strings.Contains(s, "/tui-dashboard-") }))
}
