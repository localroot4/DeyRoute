package tui

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// Regression: "?" used to push a help screen, so a background result that
// replaces its screen (thresholds: load, then the form) dropped the help
// page instead and left the finished task on the stack.
func TestHelpOverlayKeepsStack(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	release := make(chan struct{})
	show := stub.TunnelShowFn
	stub.TunnelShowFn = func(ctx context.Context, id string) (api.TunnelDetail, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return api.TunnelDetail{}, ctx.Err()
		}
		return show(ctx, id)
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("5").choose("7").choose("1") // Failover -> Thresholds * -> main
	require.Equal(t, 4, h.depth())
	h.press("?")
	h.must("Help", "Press q or Esc to go back.")
	close(release)
	h.settle()
	h.must("Help") // still on the help page
	require.Equal(t, 4, h.depth())
	_, ok := h.m.(Model).a.top().(*formScreen)
	require.True(t, ok, "the load task must have been replaced by the form")
	h.press("x") // other keys are ignored on the help page
	h.must("Help")
	h.press("esc")
	h.must("Failover thresholds of main", "Probe interval (seconds) [5]")
	require.Equal(t, 4, h.depth())
}

// Regression: q/Esc during a running change cancelled its context, so a
// tunnel could be left half created or half deleted.
func TestRunningChangeIsNotAbandoned(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	release := make(chan struct{})
	var cancelled bool
	var mu sync.Mutex
	stub.TunnelDeleteFn = func(ctx context.Context, _ string, progress func(api.Step)) error {
		progress(api.Step{ID: "stop", Title: "stop units", Status: api.StepRunning})
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			mu.Lock()
			cancelled = true
			mu.Unlock()
			return ctx.Err()
		}
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("6").choose("1").typeLine("yes")
	h.must("stop units …", "Working…")
	depth := h.depth()
	h.press("q")
	h.press("esc")
	require.Equal(t, depth, h.depth())
	h.must("This change is still running and is not abandoned half-way")
	close(release)
	h.settle()
	h.must("Tunnel main deleted.")
	h.mustNot("still running")
	mu.Lock()
	require.False(t, cancelled)
	mu.Unlock()
	h.press("q")
	require.Equal(t, depth-1, h.depth())
}

// Read-only views stay cancellable: leaving cancels the call.
func TestReadOnlyTaskCanBeLeft(t *testing.T) {
	stopped := make(chan struct{})
	stub := &apitest.Stub{
		PortCheckFn: func(ctx context.Context, _ api.PortCheckRequest) (api.PortCheckResult, error) {
			<-ctx.Done()
			close(stopped)
			return api.PortCheckResult{}, ctx.Err()
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4").choose("3").typeLine("443")
	h.must("Working…")
	h.press("q")
	require.Equal(t, 2, h.depth())
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("leaving did not cancel the check")
	}
}

// Regression: an empty Enter on the main menu printed "Invalid choice: ".
func TestRootEmptyEnter(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.press("enter")
	h.mustNot("Invalid choice")
}

// Regression: lines were measured before the ASCII conversion ("→" is one
// column, "->" two), so a line that fit in Unicode overflowed 80 columns.
func TestASCIIClipMeasuresConvertedText(t *testing.T) {
	a := dashApp(Caps{Unicode: false, Width: 80})
	prefix := "  12:41:03  main   switch   "
	msg := "a → b → c → d"
	msg += strings.Repeat("x", 80-width(prefix)-width(msg))
	st := api.Status{Role: "hub", Hub: &api.HubStatus{}, Events: []state.Event{
		{At: time.Date(2026, 9, 30, 12, 41, 3, 0, time.UTC), Type: state.EvSwitchTransport, Tunnel: "main", Message: msg},
	}}
	out := asciiOnly(renderStatus(a, st, testNow))
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
	}
	require.Contains(t, out, "a -> b -> c")
}

// Regression: log lines and daemon texts reached the terminal unfiltered,
// so an escape sequence in a log file could drive the terminal.
func TestDaemonTextIsSanitized(t *testing.T) {
	require.Equal(t, "a?[2Jb    c?", clean("a\x1b[2Jb\tc\x07\r\n"))
	require.Equal(t, "x\ny?", cleanLines("x\ny\x1b\n"))
	stub := &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) { return api.Status{Role: "hub", Hub: &api.HubStatus{}}, nil },
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) {
			return nil, nil
		},
		LogsFn: func(ctx context.Context, _ api.LogQuery, emit func(api.LogLine) error) error {
			if err := emit(api.LogLine{Source: "hub", Line: "evil \x1b]0;pwned\x07 line"}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("6").choose("4").choose("1")
	h.must("evil ?]0;pwned? line")
	require.NotContains(t, h.view(), "\x1b]0;")
	h.press("q")
}

// Regression: a quarantine that had already ended was still shown.
func TestQuarantineInThePastIsNotShown(t *testing.T) {
	a := dashApp(Caps{Unicode: true, Width: 120})
	out := renderRungs(a, []api.RungStatus{
		{Node: "de-1", Transport: "rathole/noise", Warm: true, Quarantine: testNow.Add(-time.Minute)},
		{Node: "de-1", Transport: "frp/wss", Warm: true, Quarantine: testNow.Add(time.Minute)},
	}, nil)
	require.Contains(t, out, "rathole/noise  warm")
	require.Contains(t, out, "frp/wss        quarantined until 12:46:00")
}

// The Advanced wizard only offers TLS modes TunnelAdd can create; custom
// needs certificate paths and is set with Edit tunnel.
func TestWizardTLSModes(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1").choose("1").typeLine("443")
	h.choose("11") // save the default ladder (8 rungs)
	h.must("Advanced options.")
	// name, target, probe kind, backup, policy
	h.typeLine("").typeLine("").typeLine("").typeLine("").typeLine("")
	h.must("TLS mode (auto, acme; custom is set later with Edit tunnel)")
	h.typeLine("custom")
	h.must("Enter one of: auto, acme")
	h.press("ctrl+u").typeLine("acme").typeLine("n")
	h.must("3. Confirm", "TLS mode     acme")
}

// Regression: a range of ports shared one 2-minute timeout, so checking up
// to 64 ports could fail with DEY-X042 although each check was quick.
func TestWizardChecksEachPortWithItsOwnTimeout(t *testing.T) {
	var mu sync.Mutex
	var deadlines []time.Time
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn: func(ctx context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			d, ok := ctx.Deadline()
			require.True(t, ok)
			mu.Lock()
			deadlines = append(deadlines, d)
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("1").typeLine("2000-2002")
	h.must("2002/tcp is free", "3. Confirm")
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, deadlines, 3)
	require.True(t, deadlines[2].After(deadlines[0]), "each port gets a fresh timeout")
}

func TestDetectCapsDumb(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	require.True(t, DetectCaps(env(map[string]string{"TERM": "dumb", "LANG": "en_US.UTF-8"})).Dumb)
	require.True(t, DetectCaps(env(map[string]string{})).Dumb)
	require.False(t, DetectCaps(env(map[string]string{"TERM": "xterm-256color", "LANG": "C"})).Dumb)
}

func TestLineKeys(t *testing.T) {
	a := NewModel(Options{}).a
	keys := func(line string) []string {
		var out []string
		for _, k := range lineKeys(a, line) {
			out = append(out, k.String())
		}
		return out
	}
	require.Equal(t, []string{"enter"}, keys(""))
	require.Equal(t, []string{"esc"}, keys("q"))
	require.Equal(t, []string{"esc"}, keys(" esc "))
	require.Equal(t, []string{"r"}, keys("r"))
	require.Equal(t, []string{"?"}, keys("?"))
	require.Equal(t, []string{"12", "enter"}, keys("12"))
	require.Equal(t, []string{"pgup"}, keys("pgup"))
	// In a text field q is text; esc still goes back.
	a.push(newForm("t", "", []field{{key: "x", label: "x"}}, func(*app, map[string]string) tea.Cmd { return nil }))
	require.Equal(t, []string{"q", "enter"}, keys("q"))
	require.Equal(t, []string{"esc"}, keys("esc"))
	require.Equal(t, []string{"a b", "enter"}, keys("a b"))
}

// syncBuffer is a goroutine-safe output buffer.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// lineSession runs Run in line mode (TERM=dumb) with a pipe as input.
type lineSession struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *syncBuffer
	done chan error
}

func startLines(t *testing.T, o Options) *lineSession {
	t.Helper()
	pr, pw := io.Pipe()
	s := &lineSession{t: t, in: pw, out: &syncBuffer{}, done: make(chan error, 1)}
	o.In, o.Out = pr, s.out
	o.Caps.Dumb = true
	if o.Now == nil {
		o.Now = func() time.Time { return testNow }
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	go func() {
		err := Run(o)
		_ = pr.Close()
		s.done <- err
	}()
	t.Cleanup(func() {
		_ = pw.Close()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			t.Error("line mode did not stop")
		}
	})
	return s
}

func (s *lineSession) send(line string) {
	s.t.Helper()
	_, err := io.WriteString(s.in, line+"\n")
	require.NoError(s.t, err)
}

// wait blocks until the output contains want (counting occurrences).
func (s *lineSession) wait(want string, n int) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(s.out.String(), want) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Fatalf("output never contained %q %d times:\n%s", want, n, s.out.String())
}

// S24: TERM=dumb, 80 columns, no UTF-8: the menu is usable line by line.
func TestLineModeDumbTerminal(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	s := startLines(t, Options{Caps: Caps{Width: 80}, Local: stub})
	s.wait("Choice:", 1)
	s.wait("Line mode: type a number", 1)
	s.send("1")
	s.wait("TUNNELS", 1)
	s.wait("r + Enter refreshes", 1)
	s.send("r")
	s.wait("TUNNELS", 2)
	s.send("q")
	s.send("?")
	s.wait("Help", 1)
	s.send("")
	s.send("0")
	select {
	case err := <-s.done:
		require.NoError(t, err)
		s.done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("0 did not exit")
	}
	out := s.out.String()
	for _, r := range out {
		require.LessOrEqual(t, r, rune(127), "non-ASCII output")
	}
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, len(l), 120, "line too wide: %q", l)
	}
	require.NotContains(t, out, "\x1b[", "no cursor control on a dumb terminal")
	require.Contains(t, out, "de-1 Germany 1")
}

// End of input leaves line mode, and a text answer (masked passphrase)
// works through the same screens.
func TestLineModeFormAndEOF(t *testing.T) {
	var mu sync.Mutex
	var gotPass string
	s := startLines(t, Options{Caps: Caps{Width: 80}, Backup: func(_ context.Context, _, pass string, _ bool) (string, error) {
		mu.Lock()
		gotPass = pass
		mu.Unlock()
		return "/var/backups/deyroute/b.tar.age", nil
	}})
	s.wait("Choice:", 1)
	s.send("10")
	s.send("1")
	s.wait("Output file", 1)
	s.send("")
	s.send("y")
	s.wait("Passphrase:", 1)
	s.send("q secret")
	s.send("q secret")
	s.wait("Backup saved: /var/backups/deyroute/b.tar.age", 1)
	require.NotContains(t, s.out.String(), "q secret")
	mu.Lock()
	require.Equal(t, "q secret", gotPass)
	mu.Unlock()
	require.NoError(t, s.in.Close())
	select {
	case err := <-s.done:
		require.NoError(t, err)
		s.done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("EOF did not end line mode")
	}
}

func TestEchoControlNeedsATerminal(t *testing.T) {
	require.Nil(t, echoControl(strings.NewReader("")))
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()
	defer w.Close()
	require.Nil(t, echoControl(r))
}

// Regression: without the loaded tunnel detail, Add backup node offered
// every node, the primary included.
func TestBackupNodeNeedsTunnelDetail(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	var fail sync.Mutex
	failing := true
	show := stub.TunnelShowFn
	stub.TunnelShowFn = func(ctx context.Context, id string) (api.TunnelDetail, error) {
		fail.Lock()
		defer fail.Unlock()
		if failing {
			return api.TunnelDetail{}, deyerr.New(deyerr.X006, deyerr.Params{"status": "boom"})
		}
		return show(ctx, id)
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("5").choose("3").choose("1")
	h.must("DEY-X006")
	depth := h.depth()
	h.choose("1")
	require.Equal(t, depth, h.depth(), "no node picker without the tunnel's nodes")
	fail.Lock()
	failing = false
	fail.Unlock()
	h.choose("1") // reloads the detail first
	require.Equal(t, depth, h.depth())
	h.mustNot("DEY-X006")
	h.choose("1")
	require.Equal(t, depth+1, h.depth())
	h.must("Choose a node:")
	h.mustNot("de-1  Germany 1") // the primary is not offered
}

// Line mode never prints colors, even when the caps claim them.
func TestLineModeNoColor(t *testing.T) {
	s := startLines(t, Options{Caps: Caps{Width: 80, Color: true}})
	s.wait("Choice:", 1)
	s.send("0")
	select {
	case err := <-s.done:
		require.NoError(t, err)
		s.done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("0 did not exit")
	}
	require.NotContains(t, s.out.String(), "\x1b[")
}

// Piped input (all lines at once, then EOF): every answered page is
// printed before the next answer, the last one before line mode ends.
func TestLineModePipedInput(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	var out bytes.Buffer
	err := runLines(Options{Caps: Caps{Width: 80}, Local: stub, In: strings.NewReader("1\nq\n2\n"), Out: &out,
		Now: func() time.Time { return testNow }, Location: time.UTC})
	require.NoError(t, err)
	got := out.String()
	require.Contains(t, got, "1) Dashboard")
	require.Contains(t, got, "TUNNELS", "the dashboard answered line 1")
	require.Contains(t, got, "de-1 Germany 1")
	require.Contains(t, got, "1) Add tunnel", "the Tunnels menu answered the last line")
}
