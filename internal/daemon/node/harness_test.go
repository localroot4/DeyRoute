package node

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	require.Truef(t, deyerr.HasCode(err, code), "want %s, got %v", code, err)
	return deyerr.As(err)
}

// ---------------------------------------------------------------- PKI

func newTestCA(t *testing.T, name string) *tlsutil.CA {
	t.Helper()
	ca, err := tlsutil.NewCA(name, time.Now())
	require.NoError(t, err)
	return ca
}

// ---------------------------------------------------------------- fake hub

// fakeHub implements api.ControlHandler.
type fakeHub struct {
	sessions chan *api.Session
	hello    api.Hello

	mu      sync.Mutex
	uploads map[string][]byte
	asset   []byte
	assetV  string
}

func newFakeHub() *fakeHub {
	return &fakeHub{
		sessions: make(chan *api.Session, 16),
		hello:    api.Hello{Version: version.Version, Compatible: true},
		uploads:  map[string][]byte{},
	}
}

func (h *fakeHub) Join(context.Context, api.JoinRequest, string) (api.JoinResponse, error) {
	return api.JoinResponse{}, deyerr.New(deyerr.N001, nil)
}

func (h *fakeHub) Authenticate(string, string, string) error { return nil }

func (h *fakeHub) Session(s *api.Session) {
	_ = s.SendHello(h.hello)
	h.sessions <- s
	<-s.Done()
}

func (h *fakeHub) Upload(_ context.Context, id, _ string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.uploads[id] = data
	return nil
}

func (h *fakeHub) upload(id string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.uploads[id]
	return d, ok
}

func (h *fakeHub) Asset(context.Context, string) (api.AssetInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.asset == nil {
		return api.AssetInfo{}, deyerr.New(deyerr.X008, deyerr.Params{"feature": "assets"})
	}
	sum := sha256.Sum256(h.asset)
	return api.AssetInfo{Reader: io.NopCloser(bytes.NewReader(h.asset)), Size: int64(len(h.asset)),
		SHA256: hex.EncodeToString(sum[:]), Version: h.assetV}, nil
}

// startHub serves h with a hub certificate of ca (clients of every CA in
// caPEM accepted) and returns its address.
func startHub(t *testing.T, ca *tlsutil.CA, caPEM []byte, h api.ControlHandler) string {
	t.Helper()
	certPEM, keyPEM, err := ca.IssueServer("hub", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ServerTLSConfig(caPEM, certPEM, keyPEM)
	require.NoError(t, err)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &api.ControlServer{TLSConfig: cfg, Handler: h, HandshakeTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return ln.Addr().String()
}

func waitSession(t *testing.T, h *fakeHub) *api.Session {
	t.Helper()
	select {
	case s := <-h.sessions:
		return s
	case <-time.After(15 * time.Second):
		t.Fatal("no session from the node")
		return nil
	}
}

// ---------------------------------------------------------------- fake system

// fakeSys simulates systemctl and nft for the exec.Fake handler.
type fakeSys struct {
	mu        sync.Mutex
	units     map[string]string // unit → ActiveState
	pids      map[string]int
	nextPID   int
	failStart map[string]bool
	crash     map[string]bool // start succeeds, then the unit is failed
	nft       []string        // stdin of every `nft -f -`
	nftDelete int
	svc       []string // systemctl calls on deyroute-node.service
}

func newFakeSys() *fakeSys {
	return &fakeSys{units: map[string]string{}, pids: map[string]int{}, failStart: map[string]bool{}, crash: map[string]bool{}, nextPID: 100}
}

func (s *fakeSys) runner() *exec.Fake {
	f := exec.NewFake()
	f.Handler = s.handle
	return f
}

func (s *fakeSys) state(unit string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.units[unit]
}

func (s *fakeSys) setState(unit, st string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.units[unit] = st
}

func (s *fakeSys) nftScripts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.nft...)
}

func (s *fakeSys) deletes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nftDelete
}

func (s *fakeSys) serviceCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.svc...)
}

func (s *fakeSys) handle(c exec.Call) (exec.Response, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch c.Name {
	case "nft":
		if len(c.Args) == 2 && c.Args[0] == "-f" {
			s.nft = append(s.nft, string(c.Stdin))
			return exec.OK(""), true
		}
		if len(c.Args) >= 1 && c.Args[0] == "delete" {
			s.nftDelete++
			return exec.OK(""), true
		}
		return exec.OK(""), true
	case "systemctl":
	default:
		return exec.Response{}, false
	}
	if len(c.Args) == 0 {
		return exec.Response{}, false
	}
	for _, a := range c.Args {
		if a == "deyroute-node.service" {
			s.svc = append(s.svc, strings.Join(c.Args, " "))
			return exec.OK(""), true
		}
	}
	switch c.Args[0] {
	case "daemon-reload", "enable", "disable", "reset-failed":
		return exec.OK(""), true
	case "start", "restart":
		u := c.Args[1]
		if s.failStart[u] {
			s.units[u] = "failed"
			return exec.Fail(1, "Job for "+u+" failed because the control process exited with error code."), true
		}
		if s.crash[u] {
			s.units[u] = "failed"
			return exec.OK(""), true
		}
		s.units[u] = "active"
		s.nextPID++
		s.pids[u] = s.nextPID
		return exec.OK(""), true
	case "stop":
		u := c.Args[1]
		if _, ok := s.units[u]; !ok {
			return exec.Fail(5, "Failed to stop "+u+": Unit "+u+" not loaded."), true
		}
		s.units[u] = "inactive"
		s.pids[u] = 0
		return exec.OK(""), true
	case "list-units":
		var names []string
		for u := range s.units {
			names = append(names, u)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, u := range names {
			sub := "dead"
			if s.units[u] == "active" {
				sub = "running"
			}
			fmt.Fprintf(&b, "%s loaded %s %s deyroute tunnel\n", u, s.units[u], sub)
		}
		return exec.OK(b.String()), true
	case "show":
		if strings.HasPrefix(c.Args[1], "--property=Id") {
			var b strings.Builder
			for _, u := range c.Args[2:] {
				fmt.Fprintf(&b, "Id=%s\nActiveState=%s\nMainPID=%d\nMemoryCurrent=1000000\n\n", u, s.units[u], s.pids[u])
			}
			return exec.OK(b.String()), true
		}
		u := c.Args[1]
		st, ok := s.units[u]
		if !ok {
			st = "inactive"
		}
		sub := "dead"
		switch st {
		case "active":
			sub = "running"
		case "failed":
			sub = "failed"
		}
		return exec.OK(fmt.Sprintf("ActiveState=%s\nSubState=%s\nMainPID=%d\nNRestarts=0\nActiveEnterTimestamp=Tue 2026-09-29 12:00:00 UTC\nResult=success\n", st, sub, s.pids[u])), true
	}
	return exec.Response{}, false
}

// ---------------------------------------------------------------- hooks

// recHooks records hook calls; backend "awg" has a PostStart. When gate is
// set, PostStart signals entered and waits for gate (set both before use).
type recHooks struct {
	mu      sync.Mutex
	calls   []string
	fail    error
	entered chan struct{}
	gate    chan struct{}
}

func (h *recHooks) add(s string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, s)
	return h.fail
}

func (h *recHooks) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func (h *recHooks) PreStart(_ context.Context, b, dir string) error {
	return h.add("pre " + b + " " + dir)
}

func (h *recHooks) PostStart(_ context.Context, b, dir string) error {
	err := h.add("post " + b + " " + dir)
	if h.gate != nil {
		h.entered <- struct{}{}
		<-h.gate
	}
	return err
}

func (h *recHooks) PostStop(_ context.Context, b, dir string) error {
	return h.add("stop " + b + " " + dir)
}

func (h *recHooks) HasPostStart(b string) bool { return b == "awg" }

// ---------------------------------------------------------------- logs

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

// ---------------------------------------------------------------- environment

// env is one node installation in a temporary root with a hub CA, the
// node's certificate and a node config.yaml.
type env struct {
	t        *testing.T
	root     string
	ca       *tlsutil.CA
	caPEM    []byte
	hub      *fakeHub
	hubAddr  string
	sys      *fakeSys
	runner   *exec.Fake
	hooks    *recHooks
	logs     *syncBuffer
	socket   string
	opts     Options
	restarts chan struct{}
	unins    chan struct{}
	nodeCert []byte
}

const testNode = "de-1"

func writeFile(t *testing.T, p string, data string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(data), mode))
}

// newEnv prepares the root and a hub; mod adjusts the options before start.
func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, root: root, hub: newFakeHub(), sys: newFakeSys(), hooks: &recHooks{}, logs: &syncBuffer{},
		restarts: make(chan struct{}, 4), unins: make(chan struct{}, 4)}
	e.ca = newTestCA(t, "test-hub")
	e.caPEM = e.ca.CertPEM
	e.runner = e.sys.runner()

	// Node certificate and key, as join leaves them.
	csr, key, err := tlsutil.NewKeyAndCSR(testNode)
	require.NoError(t, err)
	cert, err := e.ca.SignCSR(csr, testNode, 0)
	require.NoError(t, err)
	e.nodeCert = cert
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(root, CAFile), e.caPEM))
	require.NoError(t, tlsutil.WriteSecretPair(filepath.Join(root, config.DefaultNodeCertFile), cert,
		filepath.Join(root, config.DefaultNodeKeyFile), key))

	// A fake /proc and os-release.
	writeFile(t, filepath.Join(root, "proc/sys/kernel/osrelease"), "6.1.0-test\n", 0o644)
	writeFile(t, filepath.Join(root, "proc/sys/kernel/hostname"), "node-host\n", 0o644)
	writeFile(t, filepath.Join(root, "proc/stat"), "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 1 1 1\n", 0o644)
	writeFile(t, filepath.Join(root, "proc/self/status"), "Name:\tdeyroute\nVmRSS:\t    2048 kB\n", 0o644)
	writeFile(t, filepath.Join(root, "proc/meminfo"), "MemTotal:        1000 kB\nMemAvailable:     500 kB\n", 0o644)
	writeFile(t, filepath.Join(root, "proc/uptime"), "12345.67 100.00\n", 0o644)
	writeFile(t, filepath.Join(root, "etc/os-release"), "NAME=Debian\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n", 0o644)

	e.hubAddr = startHub(t, e.ca, e.caPEM, e.hub)
	e.writeConfig(e.hubAddr)

	sockDir, err := os.MkdirTemp("", "dn")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e.socket = filepath.Join(sockDir, "d.sock")

	e.opts = Options{
		Root:        root,
		Runner:      e.runner,
		Logger:      slog.New(dlog.NewHandler(e.logs, slog.LevelDebug, "node")),
		SocketPath:  e.socket,
		DeyrouteIDs: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Chown:       func(string, int, int) error { return nil },
		Hooks:       e.hooks,
		Notify:      func(string) error { return nil },
		RestartSelf: func(context.Context) error {
			e.restarts <- struct{}{}
			return nil
		},
		Uninstall: func(context.Context) error {
			e.unins <- struct{}{}
			return nil
		},
		Arch:              "amd64",
		HeartbeatInterval: 50 * time.Millisecond,
		BackoffMin:        20 * time.Millisecond,
		BackoffMax:        200 * time.Millisecond,
		ReconnectDelay:    20 * time.Millisecond,
		RestartDelay:      10 * time.Millisecond,
		UninstallDelay:    10 * time.Millisecond,
		StartCheckDelay:   time.Millisecond,
		FollowPoll:        20 * time.Millisecond,
		DownloadRetry:     install.RetryOptions{Tries: 2, Backoff: time.Millisecond},
	}
	return e
}

func (e *env) writeConfig(hubAddr string) {
	cfg := config.NewNode(testNode, hubAddr, e.ca.Fingerprint())
	require.NoError(e.t, os.MkdirAll(filepath.Join(e.root, config.EtcDir), 0o710))
	require.NoError(e.t, config.Save(filepath.Join(e.root, config.DefaultPath), cfg))
}

func (e *env) config() *config.Config {
	cfg, err := config.Load(filepath.Join(e.root, config.DefaultPath))
	require.NoError(e.t, err)
	return cfg
}

// start runs the agent until the test ends and returns the hub session.
func (e *env) start() *api.Session {
	e.t.Helper()
	s, _ := e.startStoppable()
	return s
}

// startStoppable runs the agent and returns the hub session and a stop
// function (also run when the test ends; calling it twice is fine).
func (e *env) startStoppable() (*api.Session, func()) {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, e.opts) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(e.t, err)
			case <-time.After(20 * time.Second):
				e.t.Fatal("agent did not stop")
			}
		})
	}
	e.t.Cleanup(stop)
	return waitSession(e.t, e.hub), stop
}

// fakeELF returns a minimal ELF executable header for machine followed by
// some bytes: enough for VerifyELF, not runnable.
func fakeELF(machine elf.Machine, typ elf.Type) []byte {
	var buf bytes.Buffer
	h := elf.Header64{
		Ident:   [16]byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)},
		Type:    uint16(typ),
		Machine: uint16(machine),
		Version: uint32(elf.EV_CURRENT),
		Ehsize:  64,
	}
	_ = binary.Write(&buf, binary.LittleEndian, h)
	buf.WriteString("fake deyroute binary")
	return buf.Bytes()
}

func (e *env) path(p string) string { return filepath.Join(e.root, p) }

// call runs a command on the node through the hub session.
func call[T any](t *testing.T, s *api.Session, name string, args any) (T, error) {
	t.Helper()
	var out T
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := s.Call(ctx, name, args, &out)
	return out, err
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 10*time.Second, 10*time.Millisecond, msg)
}
