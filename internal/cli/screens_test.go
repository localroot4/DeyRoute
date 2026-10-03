package cli

import (
	"context"
	"flag"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	"github.com/localroot4/deyroute/internal/termsvg"
	"github.com/localroot4/deyroute/internal/termsvg/demo"
	"github.com/localroot4/deyroute/internal/tui"
	"github.com/localroot4/deyroute/internal/version"
)

// writeScreens is set by make screens: the pictures are rewritten instead
// of compared.
var writeScreens = flag.Bool("screens", false, "rewrite the screenshots in docs/assets/screens (make screens)")

// screensDir holds the screenshots of the README and the guides.
const screensDir = "../../docs/assets/screens"

// echoInput answers questions one line at a time and writes each answer
// to the screen as the terminal echoes what is typed, so the picture shows
// the answers after their prompts.
type echoInput struct {
	mu    sync.Mutex
	lines []string
	echo  io.Writer
}

func (r *echoInput) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	l := r.lines[0] + "\n"
	r.lines = r.lines[1:]
	if _, err := io.WriteString(r.echo, l); err != nil {
		return 0, err
	}
	return copy(p, l), nil
}

// prompt is the shell prompt shown before a command.
func prompt(cmd string) string { return "\x1b[1;32mroot@ir-1\x1b[0m:\x1b[1;34m~\x1b[0m# " + cmd + "\n" }

// screenEnv is a CLI environment on a colour terminal of cols columns
// whose daemon is the demo hub; the colour profile and the version are
// restored when the test ends.
func screenEnv(t *testing.T, cols int) *env {
	t.Helper()
	profile := lipgloss.ColorProfile()
	v, c, d := version.Version, version.Commit, version.Date
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		version.Version, version.Commit, version.Date = v, c, d
	})
	lipgloss.SetColorProfile(2) // termenv.ANSI
	version.Set(demo.Version, "", "")

	e := newEnv(t)
	e.g.Caps = func() tui.Caps { return tui.Caps{Unicode: true, Color: true, Width: cols} }
	e.g.IsTTY, e.g.OutTTY = true, true
	e.g.Hostname = func() (string, error) { return "ir-1.example.net", nil }
	e.g.DetectIP = func(context.Context) (string, bool, error) { return demo.HubIP, false, nil }
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return demo.Status(testNow), nil }
	e.stub.TunnelListFn = func(context.Context) ([]api.TunnelInfo, error) { return demo.Tunnels(testNow), nil }
	e.stub.NodeListFn = func(context.Context) ([]api.NodeInfo, error) { return demo.Nodes(testNow), nil }
	e.stub.TrafficFn = func(_ context.Context, q api.TrafficQuery) (api.TrafficReport, error) {
		return demo.Traffic(testNow, q), nil
	}
	e.stub.OptimizeAutoPlanFn = func(context.Context, api.AutoOptions) (api.TunePlanReport, error) { return demo.TunePlan(), nil }
	e.stub.DoctorCollectFn = func(context.Context, string) (api.DoctorData, error) {
		return api.DoctorData{Role: "hub", Sections: map[string]string{"os": "Ubuntu 24.04"}, Findings: demo.DoctorFindings()}, nil
	}
	e.stub.NodeJoinCommandFn = func(context.Context, time.Duration) (api.JoinCommand, error) {
		return api.JoinCommand{Command: setup.JoinCommand(setup.InstallerURL(""), demo.JoinLink, demo.Version),
			ExpiresAt: testNow.Add(15 * time.Minute)}, nil
	}
	e.g.Ops.SetupHub = func(_ context.Context, o setup.HubOptions) (*setup.HubResult, error) {
		for _, id := range []string{setup.StepDetectIP, setup.StepCA, setup.StepHubCert, setup.StepFirewall, setup.StepSysctl, setup.StepConfig, setup.StepService} {
			s := api.Step{ID: id, Title: setup.StepTitle(id), Status: api.StepOK}
			if id == setup.StepDetectIP {
				s.Detail = demo.HubIP
			}
			steps(o.Progress, s)
		}
		return &setup.HubResult{PublicIP: o.PublicIP, ControlPort: o.ControlPort, SysctlProfile: o.SysctlProfile,
			CAFingerprint: "sha256:3f9a5c", FirewallManaged: true, ServiceStarted: true}, nil
	}
	return e
}

// shoot runs one command line on the demo terminal and returns what the
// terminal shows: the prompt, the command, the answers typed and the
// output. The test root never appears (it is a temporary directory).
func (e *env) shoot(answers []string, args ...string) string {
	e.t.Helper()
	e.out.Reset()
	e.errOut.Reset()
	e.g.reader = nil
	e.g.In = &echoInput{lines: answers, echo: e.out}
	e.g.JSON, e.g.Debug = false, false
	code := Run(context.Background(), e.g, args)
	require.Equalf(e.t, 0, code, "%v\n%s%s", args, e.out.String(), e.errOut.String())
	return prompt("deyroute "+strings.Join(args, " ")) + strings.ReplaceAll(e.out.String()+e.errOut.String(), e.root, "")
}

// cliScreen is one screenshot of a command.
type cliScreen struct {
	name, title string
	cols        int
	shoot       func(t *testing.T) string
}

func cliScreens() []cliScreen {
	return []cliScreen{
		{"setup", "deyroute setup", 110, func(t *testing.T) string {
			e := screenEnv(t, 110)
			e.tuneFixture()
			// Role 1 (hub); Enter takes the suggested name, IP and port; y
			// tunes the server.
			return e.shoot([]string{"1", "", "", "", "y"}, "setup")
		}},
		{"stats", "deyroute stats", 80, func(t *testing.T) string {
			e := screenEnv(t, 80)
			return e.shoot(nil, "stats") + "\n" + e.shoot(nil, "stats", "main", "--period", "24h")
		}},
		{"optimize-auto", "deyroute optimize auto --dry-run", 128, func(t *testing.T) string {
			return screenEnv(t, 128).shoot(nil, "optimize", "auto", "--dry-run")
		}},
		{"doctor", "deyroute doctor", 80, func(t *testing.T) string {
			return screenEnv(t, 80).shoot(nil, "doctor")
		}},
	}
}

// TestScreens draws the CLI screenshots of the README and the guides and
// checks that docs/assets/screens holds exactly them; make screens
// rewrites them (go test -run TestScreens -screens). Any change to these
// commands' output or to an English text they print needs make screens.
func TestScreens(t *testing.T) {
	var pics []termsvg.Picture
	for _, s := range cliScreens() {
		a, b := s.shoot(t), s.shoot(t)
		require.Equal(t, a, b, "screen %s differs between two runs", s.name)
		require.NotContains(t, a, os.TempDir(), "screen %s shows a temporary path", s.name)
		pics = append(pics, termsvg.Pictures("cli-"+s.name, a, termsvg.Options{Cols: s.cols, Title: s.title})...)
	}
	problems, err := termsvg.Sync(screensDir, "cli-", pics, *writeScreens)
	require.NoError(t, err)
	if len(problems) > 0 {
		t.Fatalf("docs/assets/screens:\n%s", strings.Join(problems, "\n"))
	}
}
