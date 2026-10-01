package hub

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const testWait = 10 * time.Second

var testValidate = config.ValidateOptions{KnownTransport: backend.KnownTransport, ValidTransports: backend.ValidIDs}

// writeHubFiles writes the secrets and config.yaml a hub setup leaves.
func writeHubFiles(t *testing.T, root string, certValidity time.Duration, edit func(c *config.Config)) *tlsutil.CA {
	t.Helper()
	dir := filepath.Join(root, config.SecretsDir)
	ca, err := tlsutil.NewCA("ir-1", time.Now())
	require.NoError(t, err)
	require.NoError(t, ca.Save(filepath.Join(dir, setup.FileCACert), filepath.Join(dir, setup.FileCAKey)))
	certPEM, keyPEM, err := ca.IssueServer("ir-1", []net.IP{net.ParseIP("127.0.0.1")}, nil, certValidity)
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecretPair(filepath.Join(dir, setup.FileHubCert), certPEM, filepath.Join(dir, setup.FileHubKey), keyPEM))
	cfg := config.NewHub("ir-1", "127.0.0.1", 44433)
	if edit != nil {
		edit(cfg)
	}
	require.NoError(t, config.SaveWith(filepath.Join(root, config.DefaultPath), cfg, testValidate))
	return ca
}

// testEnv is a running hub in a temporary root.
type testEnv struct {
	t      *testing.T
	root   string
	sock   string
	runner *exec.Fake
	ca     *tlsutil.CA
	h      *Hub
	client api.Local // over the unix socket
	cancel context.CancelFunc
	done   chan error
	self   []byte // content of SelfBinary
}

type envOption func(o *Options, root string)

// prepareEnv writes the hub files and returns the options of a test hub.
func prepareEnv(t *testing.T, edit func(c *config.Config), opts ...envOption) (*testEnv, Options) {
	t.Helper()
	root := t.TempDir()
	env := &testEnv{t: t, root: root, runner: exec.NewFake()}
	env.runner.Default = &exec.Response{}
	env.ca = writeHubFiles(t, root, 0, edit)
	sockDir, err := os.MkdirTemp("", "dh")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	env.sock = filepath.Join(sockDir, "d.sock")
	env.self = []byte("#!deyroute test binary " + t.Name())
	self := filepath.Join(root, "deyroute-self")
	require.NoError(t, os.WriteFile(self, env.self, 0o755)) // #nosec G306 -- test binary
	o := Options{
		Root:            root,
		Runner:          env.runner,
		Logger:          dlog.Discard(),
		ControlListen:   "127.0.0.1:0",
		SocketPath:      env.sock,
		SelfBinary:      self,
		Arch:            "amd64",
		DisableFirewall: true,
		Notify:          func(string) error { return nil },
		Getenv:          func(string) string { return "" },
		// Long enough that a loaded test machine (-race, other packages'
		// tests) does not miss heartbeats every 50 ms for that long.
		OfflineAfter:     2 * time.Second,
		PingInterval:     100 * time.Millisecond,
		MonitorInterval:  25 * time.Millisecond,
		FirewallDebounce: 20 * time.Millisecond,
		FollowPoll:       20 * time.Millisecond,
		UploadGrace:      2 * time.Second,
		// Rendered files are root:deyroute in production; tests keep the
		// current owner.
		Chown:           func(string, int, int) error { return nil },
		DeyrouteIDs:     func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		UnitStartCheck:  time.Millisecond,
		TunnelUpWait:    testWait,
		UDPProbeTimeout: 150 * time.Millisecond,
		// No test reaches the internet: decoys always answer and downloads
		// fail at once (a test that downloads sets its own Fetcher).
		DecoyCheck:   func(context.Context, string) error { return nil },
		Fetcher:      offlineFetcher{},
		RestartDelay: time.Millisecond,
	}
	for _, f := range opts {
		f(&o, root)
	}
	return env, o
}

// offlineFetcher is the default test download path: every URL fails
// permanently, as without internet access.
type offlineFetcher struct{}

// Fetch implements install.Fetcher.
func (offlineFetcher) Fetch(_ context.Context, url string, _ io.Writer) error {
	return install.Permanent(deyerr.Plain("no internet access in tests: " + url))
}

// startEnv runs the hub with o until the test ends.
func (env *testEnv) startEnv(o Options) *testEnv {
	t := env.t
	t.Helper()
	ready := make(chan struct{})
	prev := o.OnReady
	o.OnReady = func(h *Hub) {
		if prev != nil {
			prev(h)
		}
		close(ready)
	}
	h, err := New(o)
	require.NoError(t, err)
	env.h = h
	ctx, cancel := context.WithCancel(context.Background())
	env.cancel = cancel
	env.done = make(chan error, 1)
	go func() { env.done <- h.Serve(ctx) }()
	select {
	case <-ready:
	case err := <-env.done:
		t.Fatalf("hub stopped early: %v", err)
	case <-time.After(testWait):
		t.Fatal("hub not ready")
	}
	t.Cleanup(env.stop)
	c, err := api.Dial(env.sock, api.DialOptions{Timeout: testWait})
	require.NoError(t, err)
	env.client = c
	return env
}

// stop ends the hub (idempotent).
func (env *testEnv) stop() {
	if env.cancel == nil {
		return
	}
	env.cancel()
	select {
	case err := <-env.done:
		require.NoError(env.t, err)
	case <-time.After(testWait):
		env.t.Error("hub did not stop")
	}
	env.cancel = nil
}

// startHub prepares and runs a hub.
func startHub(t *testing.T, edit func(c *config.Config), opts ...envOption) *testEnv {
	env, o := prepareEnv(t, edit, opts...)
	return env.startEnv(o)
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	t.Cleanup(cancel)
	return ctx
}

// ---------------------------------------------------------------- fake node

// cmdFunc answers one command of the fake node.
type cmdFunc func(ctx context.Context, n *fakeNode, cmd api.Command, stream func([]string)) (any, error)

// fakeNode is an in-process node client (api.Join + api.ControlClient).
type fakeNode struct {
	t       *testing.T
	id      string
	addr    string
	version string
	tls     *tls.Config
	certPEM []byte
	keyPEM  []byte

	mu       sync.Mutex
	handlers map[string]cmdFunc
	units    map[string]string
	calls    []api.Command

	cancel context.CancelFunc
	done   chan struct{}
	conns  atomic.Int32
	// skew is added to the heartbeat time (node clock minus hub clock).
	skew atomic.Int64
	// unitsUnknown makes the heartbeats say that the unit list is not
	// current (an agent that has not listed its units yet).
	unitsUnknown atomic.Bool
	// lastError is the last error the heartbeats report (string).
	lastError atomic.Value
}

// joinNode joins a node with id through a fresh join command.
func (env *testEnv) joinNode(id string) *fakeNode {
	t := env.t
	t.Helper()
	resp, key := env.join(api.JoinRequest{NodeID: id, Hostname: id})
	return env.nodeFromJoin(resp, key)
}

// join runs the join protocol with req (Token and CSR are filled in).
func (env *testEnv) join(req api.JoinRequest) (api.JoinResponse, []byte) {
	t := env.t
	t.Helper()
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	name := req.NodeID
	if name == "" {
		name = "node"
	}
	csr, key, err := tlsutil.NewKeyAndCSR(name)
	require.NoError(t, err)
	req.Token = link.Token
	req.CSRPEM = string(csr)
	if req.Version == "" {
		req.Version = version.Version
	}
	req.Arch, req.OS = "amd64", "linux"
	resp, err := api.Join(ctxT(t), env.h.ControlAddr().String(), link.Fingerprint, req)
	require.NoError(t, err)
	return resp, key
}

func (env *testEnv) nodeFromJoin(resp api.JoinResponse, key []byte) *fakeNode {
	t := env.t
	cfg, err := tlsutil.ClientTLSConfig([]byte(resp.CAPEM), []byte(resp.CertPEM), key, "")
	require.NoError(t, err)
	return &fakeNode{
		t: t, id: resp.NodeID, addr: env.h.ControlAddr().String(), version: version.Version,
		tls: cfg, certPEM: []byte(resp.CertPEM), keyPEM: key,
		handlers: map[string]cmdFunc{}, units: map[string]string{},
	}
}

// on sets the answer of command name.
func (n *fakeNode) on(name string, f cmdFunc) {
	n.mu.Lock()
	n.handlers[name] = f
	n.mu.Unlock()
}

func (n *fakeNode) handle(ctx context.Context, cmd api.Command, stream func([]string)) (any, error) {
	n.mu.Lock()
	n.calls = append(n.calls, cmd)
	f := n.handlers[cmd.Name]
	n.mu.Unlock()
	if f == nil {
		return nil, deyerr.New(deyerr.X008, deyerr.Params{"feature": cmd.Name})
	}
	return f(ctx, n, cmd, stream)
}

// client returns a control client with the node's credentials.
func (n *fakeNode) client() *api.ControlClient {
	return &api.ControlClient{
		HubAddr:   n.addr,
		TLSConfig: n.tls,
		Hello: func() api.Hello {
			return api.Hello{NodeID: n.id, Version: n.version, Arch: "amd64", OS: "Test OS", Kernel: "6.1"}
		},
		Heartbeat: func() api.Heartbeat {
			n.mu.Lock()
			defer n.mu.Unlock()
			u := map[string]string{}
			for k, v := range n.units {
				u[k] = v
			}
			lastErr, _ := n.lastError.Load().(string)
			return api.Heartbeat{At: time.Now().Add(time.Duration(n.skew.Load())), CPUPercent: 3, RAMBytes: 121 << 20, Units: u,
				UnitsUnknown: n.unitsUnknown.Load(), LastError: lastErr}
		},
		Handler:           n.handle,
		Logger:            dlog.Discard(),
		OnConnected:       func() { n.conns.Add(1) },
		BackoffMin:        20 * time.Millisecond,
		BackoffMax:        100 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
		OpenTimeout:       5 * time.Second,
	}
}

// start connects the node's control stream until stop or the test end.
func (n *fakeNode) start() *fakeNode {
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	n.done = make(chan struct{})
	c := n.client()
	go func() {
		defer close(n.done)
		_ = c.Run(ctx)
	}()
	n.t.Cleanup(n.stop)
	return n
}

// stop disconnects the node (idempotent).
func (n *fakeNode) stop() {
	if n.cancel == nil {
		return
	}
	n.cancel()
	<-n.done
	n.cancel = nil
}

// upload sends a payload for a pending upload id.
func (n *fakeNode) upload(ctx context.Context, id string, data []byte) error {
	return n.client().Upload(ctx, id, bytes.NewReader(data))
}

// decode unmarshals the arguments of cmd.
func decode(t *testing.T, cmd api.Command, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(cmd.Args, v))
}

// waitOnline waits until the hub reports node id online (or offline).
func (env *testEnv) waitOnline(id string, online bool) {
	env.t.Helper()
	require.Eventually(env.t, func() bool {
		ns, _ := env.h.nodeState(id)
		return ns.Online == online && (!online || env.h.Online(id))
	}, testWait, 10*time.Millisecond, "node %s online=%v", id, online)
}

// waitEvent waits for an event of typ (optionally about node).
func (env *testEnv) waitEvent(typ, node string) state.Event {
	env.t.Helper()
	var found state.Event
	require.Eventually(env.t, func() bool {
		evs, err := env.h.st.Events(state.EventFilter{Types: []string{typ}, Node: node})
		if err != nil || len(evs) == 0 {
			return false
		}
		found = evs[0]
		return true
	}, testWait, 10*time.Millisecond, "event %s for %q", typ, node)
	return found
}

// countEvents counts stored events of typ.
func (env *testEnv) countEvents(typ string) int {
	evs, err := env.h.st.Events(state.EventFilter{Types: []string{typ}})
	require.NoError(env.t, err)
	return len(evs)
}

// addTunnel adds a tunnel over nodes to config.yaml.
func (env *testEnv) addTunnel(id string, nodes []string, listen int) {
	env.t.Helper()
	_, err := env.h.mutate(func(c *config.Config) error {
		return c.AddTunnel(config.NewTunnel(id, strings.ToUpper(id[:1])+id[1:], nodes,
			[]config.PortMap{{Listen: listen, Proto: config.ProtoTCP, Target: "127.0.0.1:" + strconv.Itoa(listen)}}))
	})
	require.NoError(env.t, err)
}

// nftScripts returns the stdin of every `nft -f -` run.
func (env *testEnv) nftScripts() []string {
	var out []string
	for _, c := range env.runner.Calls() {
		if c.Name == "nft" && len(c.Args) == 2 && c.Args[0] == "-f" {
			out = append(out, string(c.Stdin))
		}
	}
	return out
}

// codeOf returns the DEY code of err.
func codeOf(err error) deyerr.Code {
	if err == nil {
		return ""
	}
	return deyerr.As(err).Code
}
