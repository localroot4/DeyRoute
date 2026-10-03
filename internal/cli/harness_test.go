package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	// Every transport is registered, as in cmd/deyroute (config validation).
	_ "github.com/localroot4/deyroute/internal/backend/all"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/tui"
)

// TestMain pins the local zone to UTC before any test runs, so output that
// prints local times (stats ticks, event times) does not depend on the
// machine running the tests.
func TestMain(m *testing.M) {
	time.Local = time.UTC
	goleak.VerifyTestMain(m)
}

// testNow is the fixed clock of every test.
var testNow = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// env is one CLI test environment: a temporary root, captured streams, a
// stub daemon and scripted prompts.
type env struct {
	t      *testing.T
	g      *Globals
	stub   *apitest.Stub
	out    *bytes.Buffer
	errOut *bytes.Buffer
	root   string
	vars   map[string]string
	fake   *exec.Fake
	dialed int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, stub: &apitest.Stub{}, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, root: t.TempDir(),
		vars: map[string]string{}, fake: exec.NewFake()}
	e.g = &Globals{
		Out: e.out, Err: e.errOut, In: strings.NewReader(""), Root: e.root, Runner: e.fake,
		Now:      func() time.Time { return testNow },
		Getenv:   func(k string) string { return e.vars[k] },
		Caps:     func() tui.Caps { return tui.Caps{Unicode: true, Width: 120} },
		Hostname: func() (string, error) { return "IR-Server.example.com", nil },
		ReadPassword: func(string) (string, error) {
			return "", errAborted
		},
		Editor:    func(context.Context, string) error { return nil },
		DetectIP:  func(context.Context) (string, bool, error) { return "5.6.7.8", false, nil },
		PortBusy:  func(int) bool { return false },
		NoService: true,
		Dial: func() (api.Local, error) {
			e.dialed++
			return e.stub, nil
		},
		RunTUI: func(tui.Options) error { return nil },
	}
	return e
}

// tty makes stdin an interactive terminal answering lines.
func (e *env) tty(lines ...string) {
	e.g.IsTTY = true
	e.g.In = strings.NewReader(strings.Join(lines, "\n") + "\n")
	e.g.reader = nil
}

// down makes the daemon unreachable (DEY-X003).
func (e *env) down() {
	e.g.Dial = func() (api.Local, error) {
		return nil, deyerr.New(deyerr.X003, deyerr.Params{"service": "deyroute-hub"})
	}
}

// run executes the command line and returns the exit code.
func (e *env) run(args ...string) int {
	e.t.Helper()
	e.out.Reset()
	e.errOut.Reset()
	e.g.JSON, e.g.Debug = false, false
	return Run(context.Background(), e.g, args)
}

// ok runs the command line and requires exit 0.
func (e *env) ok(args ...string) string {
	e.t.Helper()
	code := e.run(args...)
	require.Equalf(e.t, 0, code, "args %v\nstdout: %s\nstderr: %s", args, e.out.String(), e.errOut.String())
	return e.out.String()
}

// json runs the command line with --json, requires exit 0 and decodes the
// document.
func (e *env) json(args ...string) map[string]any {
	e.t.Helper()
	out := e.ok(append(args, "--json")...)
	var doc map[string]any
	require.NoErrorf(e.t, json.Unmarshal([]byte(out), &doc), "not JSON: %s", out)
	require.EqualValues(e.t, 1, doc["schema"], out)
	return doc
}

// fail runs the command line and requires the exit code.
func (e *env) fail(code int, args ...string) string {
	e.t.Helper()
	got := e.run(args...)
	require.Equalf(e.t, code, got, "args %v\nstdout: %s\nstderr: %s", args, e.out.String(), e.errOut.String())
	return e.errOut.String()
}

// writeConfig writes Root/etc/deyroute/config.yaml.
func (e *env) writeConfig(yaml string) {
	e.t.Helper()
	p := filepath.Join(e.root, config.DefaultPath)
	require.NoError(e.t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(e.t, os.WriteFile(p, []byte(yaml), 0o600))
}

// hubConfig is a valid hub config.yaml.
const hubConfig = `schema_version: 1
role: hub
hub:
  name: ir-1
  control_port: 44433
  public_ip: 5.6.7.8
  domain: ""
  ui_mode: simple
  notify:
    telegram:
      enabled: false
      bot_token_file: /etc/deyroute/secrets/telegram.token
      chat_id: ""
      events: [down, switch, failback, node_offline]
nodes:
  - id: de-1
    name: "Germany 1"
    public_ip: 1.2.3.4
    cert_fingerprint: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
tunnels:
  - id: main
    name: "Main"
    enabled: true
    nodes: [de-1]
    ports:
      - listen: 443
        proto: tcp
        target: 127.0.0.1:443
    ladder: default
    failover:
      policy: transport_then_node
      probe_interval_s: 5
      probe_timeout_s: 3
      fail_threshold: 3
      recover_threshold: 6
      failback: true
      failback_after_s: 300
      max_switches_per_hour: 6
    tls:
      mode: auto
tuning:
  sysctl_profile: balanced
  bbr: true
security:
  firewall_managed: true
  restrict_control_to_nodes: true
`

// nodeConfig is a valid node config.yaml.
const nodeConfig = `schema_version: 1
role: node
node:
  id: de-1
  hub_addr: 5.6.7.8:44433
  hub_ca_fingerprint: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
  cert_file: /etc/deyroute/secrets/node.crt
  key_file: /etc/deyroute/secrets/node.key
`

// steps reports the given steps through progress.
func steps(progress func(api.Step), ss ...api.Step) {
	for _, s := range ss {
		if progress != nil {
			progress(api.Step{ID: s.ID, Title: s.Title, Status: api.StepRunning})
			progress(s)
		}
	}
}

func TestHarnessSanity(t *testing.T) {
	e := newEnv(t)
	require.Contains(t, e.ok("version"), "deyroute")
	doc := e.json("version")
	require.Contains(t, doc, "go")
	require.Contains(t, doc, "commit")
}
