package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/front"
	"github.com/localroot4/deyroute/internal/front/fronttest"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/ports"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

const (
	testFrontDomain = "front.example.com"
	testFrontPort   = 2053
	// testClientIP is the real client address the fake CDN reports (a public
	// one: the front never accepts a private or loopback CF-Connecting-IP).
	testClientIP = "203.0.113.7"
)

// frontLogBuf is a goroutine-safe log buffer.
type frontLogBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *frontLogBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *frontLogBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newFrontLog() (*slog.Logger, *frontLogBuf) {
	s := &frontLogBuf{}
	return slog.New(slog.NewTextHandler(s, &slog.HandlerOptions{Level: slog.LevelDebug})), s
}

// frontConfig enables the front in a hub config; edit may change it further.
func frontConfig(edit func(f *config.HubFront)) func(c *config.Config) {
	return func(c *config.Config) {
		c.Hub.Front = config.HubFront{Enabled: true, Domain: testFrontDomain, Port: testFrontPort}
		if edit != nil {
			edit(&c.Hub.Front)
		}
	}
}

// frontOpts bind the front on a free loopback port and shorten the route
// debounce.
func frontOpts(extra ...envOption) []envOption {
	return append([]envOption{func(o *Options, _ string) {
		o.FrontListen = "127.0.0.1:0"
		o.RouteStable = 150 * time.Millisecond
	}}, extra...)
}

// frontEnv is a hub with the front enabled behind a fake CDN.
type frontEnv struct {
	*testEnv
	cdn    *fronttest.CDN
	dialer *front.Dialer
	logs   *frontLogBuf
}

// startFrontHub runs a hub with the front enabled and a fake CDN in front of
// it. clientIP is what the CDN reports as the client address ("" = the
// loopback peer, which the front does not accept: an untrusted address).
func startFrontHub(t *testing.T, clientIP string, edit func(f *config.HubFront), opts ...envOption) *frontEnv {
	t.Helper()
	logger, logs := newFrontLog()
	opts = append(frontOpts(func(o *Options, _ string) { o.Logger = logger }), opts...)
	env := startHub(t, frontConfig(edit), opts...)
	require.NotNil(t, env.h.FrontAddr(), "the front is bound")
	var f config.HubFront
	if edit != nil {
		edit(&f)
	}
	cdn, err := fronttest.NewCDN(fronttest.Options{
		OriginAddr: env.h.FrontAddr().String(),
		OriginTLS:  f.TLS != config.FrontTLSOff,
		ClientIP:   clientIP,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cdn.Close() })
	return &frontEnv{testEnv: env, cdn: cdn, dialer: &front.Dialer{RootCAs: cdn.CAPool(), UpgradeTimeout: testWait}, logs: logs}
}

// secret returns the front path secret from its file.
func (e *frontEnv) secret() string {
	data, err := os.ReadFile(filepath.Join(e.root, config.SecretsDir, config.DefaultFrontSecretFile))
	require.NoError(e.t, err)
	return strings.TrimSpace(string(data))
}

// joinFront joins a node through the fake CDN with the front link of the hub.
func (e *frontEnv) joinFront(id string) *fakeNode {
	t := e.t
	t.Helper()
	jc, err := e.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	require.True(t, link.Front(), "the join link carries the front path")
	dial := e.dialFor(link.Secret)
	csr, key, err := tlsutil.NewKeyAndCSR(id)
	require.NoError(t, err)
	resp, err := api.JoinVia(ctxT(t), link.Addr(), link.Fingerprint, api.JoinRequest{
		Token: link.Token, CSRPEM: string(csr), NodeID: id, Hostname: id, Version: version.Version, Arch: "amd64", OS: "linux",
	}, dial)
	require.NoError(t, err)
	require.Equal(t, testFrontDomain+":"+strconv.Itoa(testFrontPort), resp.HubAddr, "the join answer names the front address")
	n := e.nodeFromJoin(resp, key)
	n.addr, n.dial = link.Addr(), dial
	return n
}

// dialFor returns the dial hook of a node that uses secret.
func (e *frontEnv) dialFor(secret string) func(ctx context.Context) (net.Conn, error) {
	target := front.Target{Host: testFrontDomain, Port: e.cdn.Port(), TLS: true, EdgeIP: "127.0.0.1", Secret: secret}
	return func(ctx context.Context) (net.Conn, error) { return e.dialer.DialControl(ctx, target) }
}

func nodeRoute(t *testing.T, h *Hub, id string) config.Node {
	t.Helper()
	n, ok := h.Config().NodeByID(id)
	require.True(t, ok, id)
	return *n
}

// configBytes returns config.yaml as it is on disk.
func (e *testEnv) configBytes() []byte {
	data, err := os.ReadFile(filepath.Join(e.root, config.DefaultPath))
	require.NoError(e.t, err)
	return data
}

func TestFrontJoinAndSession(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	n := env.joinFront("fr-1")

	// The node joined through the CDN: route front, the real client address
	// recorded (the peer vouched for it), the front address in the answer.
	node := nodeRoute(t, env.h, "fr-1")
	require.Equal(t, config.RouteFront, node.Route)
	require.Equal(t, testClientIP, node.PublicIP)
	require.Contains(t, string(env.configBytes()), "route: front")
	require.EqualValues(t, 1, env.cdn.Stats().Upgrades, "the join used one CDN upgrade")

	// Heartbeat and commands flow through the same path.
	n.on(api.CmdSysinfo, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return map[string]string{"host": "fr-1"}, nil
	})
	n.start()
	env.waitOnline("fr-1", true)
	require.Eventually(t, func() bool {
		ns, _ := env.h.nodeState("fr-1")
		return !ns.LastHeartbeat.IsZero() && ns.CPUPercent == 3
	}, testWait, 10*time.Millisecond)
	var out map[string]string
	require.NoError(t, env.h.Call(ctxT(t), "fr-1", api.CmdSysinfo, nil, &out))
	require.Equal(t, "fr-1", out["host"])
	require.Equal(t, front.ViaFront, env.h.nodeVia("fr-1"))
	require.Equal(t, testClientIP, mustNodeState(env.h, "fr-1").RemoteIP)

	// The dashboard says so, and never prints the secret.
	st, err := env.h.Local().Status(ctxT(t))
	require.NoError(t, err)
	require.Len(t, st.Nodes, 1)
	require.Equal(t, config.RouteFront, st.Nodes[0].Route)
	require.Equal(t, front.ViaFront, st.Nodes[0].Via)
	require.NotNil(t, st.Hub.Front)
	require.Equal(t, api.FrontStatus{Enabled: true, Domain: testFrontDomain, Port: testFrontPort, Listening: true, CFOnly: true, TLS: "auto"}, *st.Hub.Front)
	data, err := json.Marshal(st)
	require.NoError(t, err)
	require.NotContains(t, string(data), env.secret())
	require.NotContains(t, env.logs.String(), env.secret(), "the secret never reaches the log")

	// The route was written once: a reconnect leaves config.yaml untouched.
	before := env.configBytes()
	n.stop()
	n.start()
	env.waitOnline("fr-1", true)
	require.Eventually(t, func() bool { return n.conns.Load() >= 2 }, testWait, 10*time.Millisecond)
	require.Equal(t, string(before), string(env.configBytes()))
	require.Equal(t, 1, strings.Count(string(env.configBytes()), "route:"))
}

func TestFrontUntrustedPeerKeepsPublicIP(t *testing.T) {
	// The CDN reports no usable client address: the hub only knows an edge
	// (here loopback) address, which must never become node.public_ip.
	env := startFrontHub(t, "", nil)
	n := env.joinFront("fr-1")
	node := nodeRoute(t, env.h, "fr-1")
	require.Equal(t, config.RouteFront, node.Route)
	require.Empty(t, node.PublicIP, "an untrusted front join stores no address")

	n.start()
	env.waitOnline("fr-1", true)
	// Many requests later (stream, pings) the address is still empty and no
	// node_ip_changed was emitted.
	time.Sleep(300 * time.Millisecond)
	require.Empty(t, nodeRoute(t, env.h, "fr-1").PublicIP)
	require.Zero(t, env.countEvents(state.EvNodeIPChanged))
	require.Empty(t, mustNodeState(env.h, "fr-1").RemoteIP)

	// The firewall never gets an edge address, and the node list says so.
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	spec := env.h.Firewall().Spec
	require.Empty(t, spec.NodeIPs4)
	require.Empty(t, spec.NodeIPs6)
	list, err := env.h.Local().NodeList(ctxT(t))
	require.NoError(t, err)
	require.Equal(t, "", list[0].PublicIP)
	require.Equal(t, config.RouteFront, list[0].Route)

	// NodeTest and the UDP check cope with the missing address.
	n.on(api.CmdSysinfo, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return map[string]string{}, nil
	})
	res, err := env.h.Local().NodeTest(ctxT(t), "fr-1")
	require.NoError(t, err)
	require.True(t, res.Online)
	require.False(t, res.UDPOK)
	env.h.ensureUDP(ctxT(t), "t", "fr-1", "backhaul/udp", time.Now().Add(time.Minute))
	ns := mustNodeState(env.h, "fr-1")
	require.Nil(t, ns.UDPOK)
}

func TestFrontTrustedPeerRecordsRealIP(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	n := env.joinFront("fr-1").start()
	env.waitOnline("fr-1", true)
	require.Equal(t, testClientIP, nodeRoute(t, env.h, "fr-1").PublicIP)

	// The client moves: the new real address is recorded (informational) but
	// the node stays out of @nodes, and the route stays front.
	env.cdn.Configure(func(o *fronttest.Options) { o.ClientIP = "198.51.100.9" })
	n.stop()
	n.start()
	require.Eventually(t, func() bool { return nodeRoute(t, env.h, "fr-1").PublicIP == "198.51.100.9" }, testWait, 10*time.Millisecond)
	require.Equal(t, config.RouteFront, nodeRoute(t, env.h, "fr-1").Route)

	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	require.Empty(t, env.h.Firewall().Spec.NodeIPs4, "a front node is never in @nodes")
}

func TestFrontDirectNodeUnchanged(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	d := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	node := nodeRoute(t, env.h, "de-1")
	require.Empty(t, node.Route, "a direct node has no route")
	require.Equal(t, "127.0.0.1", node.PublicIP)
	require.NotContains(t, string(env.configBytes()), "route:", "direct is never written")
	require.Empty(t, env.h.nodeVia("de-1"))

	// A direct node and a front node side by side: only the direct one is in @nodes.
	env.joinFront("fr-1").start()
	env.waitOnline("fr-1", true)
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	spec := env.h.Firewall().Spec
	require.Equal(t, []string{"127.0.0.1"}, spec.NodeIPs4)
	require.Equal(t, 1, strings.Count(string(env.configBytes()), "route: front"))
	_ = d
}

func TestFrontNodeFlipsBackToDirect(t *testing.T) {
	env := startFrontHub(t, "", nil)
	fr := env.joinFront("fr-1").start()
	env.waitOnline("fr-1", true)
	require.Equal(t, config.RouteFront, nodeRoute(t, env.h, "fr-1").Route)
	require.Empty(t, nodeRoute(t, env.h, "fr-1").PublicIP)
	fr.stop()

	// The same node (same certificate) now connects directly and stays up: its
	// real address is recorded at once and the route goes after RouteStable.
	direct := &fakeNode{
		t: t, id: fr.id, addr: env.h.ControlAddr().String(), version: fr.version, tls: fr.tls,
		certPEM: fr.certPEM, keyPEM: fr.keyPEM, handlers: map[string]cmdFunc{}, units: map[string]string{},
	}
	direct.start()
	require.Eventually(t, func() bool { return nodeRoute(t, env.h, "fr-1").PublicIP == "127.0.0.1" }, testWait, 10*time.Millisecond)
	require.Eventually(t, func() bool { return nodeRoute(t, env.h, "fr-1").Route == "" }, testWait, 10*time.Millisecond)
	require.NotContains(t, string(env.configBytes()), "route:")
	require.Empty(t, env.h.nodeVia("fr-1"))
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	require.Equal(t, []string{"127.0.0.1"}, env.h.Firewall().Spec.NodeIPs4, "a direct node is in @nodes again")
}

func TestFrontRouteWriteRefusedIsRetried(t *testing.T) {
	// An unapplied edit of config.yaml (DEY-C026) refuses the route write at
	// an attach; that is not fatal and the next attach retries.
	env := startFrontHub(t, testClientIP, nil)
	n := env.joinFront("fr-1")
	changed, err := env.h.setRoute("fr-1", "")
	require.NoError(t, err)
	require.True(t, changed)
	require.Empty(t, nodeRoute(t, env.h, "fr-1").Route)

	cfg := config.Clone(env.h.Config())
	cfg.Hub.Name = "edited"
	require.NoError(t, config.SaveWith(filepath.Join(env.root, config.DefaultPath), cfg, testValidate))
	n.start()
	env.waitOnline("fr-1", true)
	require.Eventually(t, func() bool { return strings.Contains(env.logs.String(), "code=DEY-C026") }, testWait, 10*time.Millisecond)
	require.Empty(t, nodeRoute(t, env.h, "fr-1").Route, "refused, the node stays online")

	_, err = env.h.Local().ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	n.stop()
	n.start()
	require.Eventually(t, func() bool { return nodeRoute(t, env.h, "fr-1").Route == config.RouteFront }, testWait, 10*time.Millisecond)
}

func TestFrontSecretGenerated(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	path := filepath.Join(env.root, config.SecretsDir, config.DefaultFrontSecretFile)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	secret := env.secret()
	require.Len(t, secret, 43)
	require.True(t, front.ValidSecret(secret))
	// Registered with the log redactor.
	require.NotContains(t, dlog.Redact("secret is "+secret), secret)
	// Persisted by name in config.yaml, never by value.
	require.Equal(t, config.DefaultFrontSecretFile, env.h.Config().Hub.Front.SecretFile)
	require.Contains(t, string(env.configBytes()), "secret_file: "+config.DefaultFrontSecretFile)
	require.NotContains(t, string(env.configBytes()), secret)

	// The front accepts it: GET /<secret>/c upgrades, other paths get the decoy.
	n := env.joinFront("fr-1")
	_ = n
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	require.Contains(t, jc.Link, "@"+testFrontDomain+":2053/"+secret+"#sha256:")
	require.Contains(t, jc.Command, "'"+jc.Link+"'")
}

func TestFrontSecretExistingFileAndRestart(t *testing.T) {
	env, o := prepareEnv(t, frontConfig(func(f *config.HubFront) { f.SecretFile = "mine.secret" }), frontOpts()...)
	const secret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"
	require.NoError(t, os.WriteFile(filepath.Join(env.root, config.SecretsDir, "mine.secret"), []byte(secret+"\n"), 0o600))
	env.startEnv(o)
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	require.Contains(t, jc.Link, "/"+secret+"#")
	_, err = os.Stat(filepath.Join(env.root, config.SecretsDir, config.DefaultFrontSecretFile))
	require.ErrorIs(t, err, os.ErrNotExist, "no second secret is created")
}

func TestFrontSecretBadFileDegrades(t *testing.T) {
	logger, logs := newFrontLog()
	env, o := prepareEnv(t, frontConfig(func(f *config.HubFront) { f.SecretFile = "bad.secret" }),
		frontOpts(func(o *Options, _ string) { o.Logger = logger })...)
	require.NoError(t, os.WriteFile(filepath.Join(env.root, config.SecretsDir, "bad.secret"), []byte("not a/valid secret\n"), 0o600))
	env.startEnv(o)
	require.Nil(t, env.h.FrontAddr())
	require.Equal(t, 1, strings.Count(logs.String(), "code=DEY-X053"))
	// The direct path keeps working and the join link stays a direct link.
	env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	require.False(t, link.Front())
}

func TestFrontPersistSecretNameRefusedIsNotFatal(t *testing.T) {
	logger, logs := newFrontLog()
	env := startHub(t, frontConfig(nil), frontOpts(func(o *Options, _ string) { o.Logger = logger })...)
	// Another edit is waiting for `config apply`: the name cannot be written.
	cfg := config.Clone(env.h.Config())
	cfg.Hub.Name = "edited"
	cfg.Hub.Front.SecretFile = ""
	require.NoError(t, config.SaveWith(filepath.Join(env.root, config.DefaultPath), cfg, testValidate))
	env.h.persistFrontSecretName()
	require.Contains(t, logs.String(), "code=DEY-C026")
	require.Contains(t, logs.String(), "level=WARN")
	require.NotNil(t, env.h.FrontAddr(), "the hub keeps running")
	// The hub runs on with the same secret after a restart: the default file is found again.
	_, err := os.Stat(filepath.Join(env.root, config.SecretsDir, config.DefaultFrontSecretFile))
	require.NoError(t, err)
}

func TestFrontBindFailureDegrades(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = busy.Close() }()
	logger, logs := newFrontLog()
	env := startHub(t, frontConfig(nil), frontOpts(func(o *Options, _ string) {
		o.FrontListen = busy.Addr().String()
		o.Logger = logger
	})...)

	// The hub is up, the front is not: DEY-X053 once, no retry loop.
	require.Nil(t, env.h.FrontAddr())
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 1, strings.Count(logs.String(), "code=DEY-X053"))
	st, err := env.h.Local().Status(ctxT(t))
	require.NoError(t, err)
	require.True(t, st.Hub.Front.Enabled)
	require.False(t, st.Hub.Front.Listening)
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	require.Zero(t, env.h.Firewall().Spec.FrontPort, "no rule for a port nobody listens on")

	// The direct control port keeps serving nodes.
	env.joinNode("de-1").start()
	env.waitOnline("de-1", true)

	// The next config apply retries: still busy, one more line; then free.
	_, err = env.h.Local().ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(logs.String(), "code=DEY-X053"))
	require.Nil(t, env.h.FrontAddr())
	require.NoError(t, busy.Close())
	_, err = env.h.Local().ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	require.NotNil(t, env.h.FrontAddr(), "the retry bound the port")
	require.Equal(t, 2, strings.Count(logs.String(), "code=DEY-X053"))
}

func TestFrontReloadEnableDisable(t *testing.T) {
	env := startHub(t, nil, frontOpts()...)
	require.Nil(t, env.h.FrontAddr())
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	link, _ := api.ParseJoinLink(jc.Link)
	require.False(t, link.Front(), "front off: the link is unchanged")

	editFront := func(f func(h *config.HubFront)) {
		cfg := config.Clone(env.h.Config())
		f(&cfg.Hub.Front)
		require.NoError(t, config.SaveWith(filepath.Join(env.root, config.DefaultPath), cfg, testValidate))
		_, err := env.h.Local().ConfigApply(ctxT(t), nil)
		require.NoError(t, err)
	}
	editFront(func(f *config.HubFront) {
		*f = config.HubFront{Enabled: true, Domain: testFrontDomain, Port: testFrontPort}
	})
	require.NotNil(t, env.h.FrontAddr(), "enabled by config apply")
	first := env.h.FrontAddr().String()
	require.Eventually(t, func() bool {
		require.NoError(t, env.h.applyFirewall(ctxT(t)))
		return env.h.Firewall().Spec.FrontPort == env.h.frontBoundPort()
	}, testWait, 10*time.Millisecond)

	// A listener setting change rebinds; a domain change does not.
	editFront(func(f *config.HubFront) { f.Domain = "other.example.com" })
	require.Equal(t, first, env.h.FrontAddr().String())
	editFront(func(f *config.HubFront) { f.TLS = config.FrontTLSOff })
	require.NotNil(t, env.h.FrontAddr())

	editFront(func(f *config.HubFront) { f.Enabled = false })
	require.Nil(t, env.h.FrontAddr(), "disabled by config apply")
	require.NoError(t, env.h.applyFirewall(ctxT(t)))
	require.Zero(t, env.h.Firewall().Spec.FrontPort)
}

func TestFrontFirewallSpec(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfOnly *bool
		open   bool
	}{
		{"cf_only default", nil, false},
		{"cf_only true", boolPtr(true), false},
		{"cf_only false", boolPtr(false), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := startFrontHub(t, testClientIP, func(f *config.HubFront) { f.CFOnly = tc.cfOnly })
			env.joinNode("de-1").start()
			env.joinFront("fr-1").start()
			env.waitOnline("de-1", true)
			env.waitOnline("fr-1", true)
			require.NoError(t, env.h.applyFirewall(ctxT(t)))
			spec := env.h.Firewall().Spec
			require.Equal(t, env.h.FrontAddr().(*net.TCPAddr).Port, spec.FrontPort, "the port the listener is bound to")
			require.Equal(t, tc.open, spec.FrontOpen)
			require.Equal(t, []string{"127.0.0.1"}, spec.NodeIPs4, "the front node is not in @nodes")
			script := firewall.Render(spec)
			if tc.open {
				require.NotContains(t, script, "@cf4")
			} else {
				require.Contains(t, script, "@cf4")
			}
			// The front port is a port deyroute owns: audit and doctor do not flag it.
			var found bool
			for _, pp := range env.h.deyroutePorts(env.h.Config()) {
				found = found || pp.port == spec.FrontPort
			}
			require.True(t, found)
		})
	}
}

func TestFrontAnnounceMoveSkipsFrontNodes(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	d := env.joinNode("de-1")
	d.on(api.CmdSetHub, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) { return nil, nil })
	d.start()
	fr := env.joinFront("fr-1")
	fr.on(api.CmdSetHub, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) { return nil, nil })
	fr.start()
	env.waitOnline("de-1", true)
	env.waitOnline("fr-1", true)

	res, err := env.h.Local().HubAnnounceMove(ctxT(t), "9.9.9.9:44433")
	require.NoError(t, err)
	require.Equal(t, []string{"de-1"}, res.Accepted)
	require.Equal(t, []string{"fr-1"}, res.Front, "front: unchanged")
	require.Empty(t, res.Offline)
	fr.mu.Lock()
	defer fr.mu.Unlock()
	for _, c := range fr.calls {
		require.NotEqual(t, api.CmdSetHub, c.Name, "a front node never gets a direct hub address")
	}

	// The automatic announcement after a control-port change says so too.
	msg := env.h.announcePort(ctxT(t), env.h.Config())
	require.Contains(t, msg, "front: unchanged (fr-1)")
}

func TestFrontUpdateGuards(t *testing.T) {
	// Front enabled: rollback and downgrades below the first front release are refused.
	env := startFrontHub(t, testClientIP, nil)
	_, err := env.h.Local().UpdateRollback(ctxT(t))
	require.Equal(t, deyerr.S010, codeOf(err))
	require.Contains(t, deyerr.As(err).Fix(), "deyroute front disable")
	_, err = env.h.Local().UpdateApply(ctxT(t), "v0.2.0", nil)
	require.Equal(t, deyerr.S010, codeOf(err))
	// A version that knows the front is not refused by the guard (the offline
	// test fetcher fails later, with another code).
	_, err = env.h.Local().UpdateApply(ctxT(t), FirstFrontVersion, nil)
	require.NotEqual(t, deyerr.S010, codeOf(err))
	_, err = env.h.Local().UpdateApply(ctxT(t), "", nil)
	require.NotEqual(t, deyerr.S010, codeOf(err))

	// A front node alone (front disabled again) is enough.
	env.joinFront("fr-1")
	cfg := config.Clone(env.h.Config())
	cfg.Hub.Front.Enabled = false
	require.NoError(t, config.SaveWith(filepath.Join(env.root, config.DefaultPath), cfg, testValidate))
	_, err = env.h.Local().ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	require.Nil(t, env.h.FrontAddr())
	_, err = env.h.Local().UpdateRollback(ctxT(t))
	require.Equal(t, deyerr.S010, codeOf(err))
	require.Contains(t, deyerr.As(err).Why(), "fr-1")
}

func TestUpdateGuardsWithoutFront(t *testing.T) {
	env := startHub(t, nil)
	env.joinNode("de-1")
	_, err := env.h.Local().UpdateRollback(ctxT(t))
	require.Equal(t, deyerr.S007, codeOf(err), "no front: the rollback guard stays out of the way")
	_, err = env.h.Local().UpdateApply(ctxT(t), "0.1.0", nil)
	require.NotEqual(t, deyerr.S010, codeOf(err))
}

func TestOlderThanFront(t *testing.T) {
	for v, want := range map[string]bool{
		"0.1.0": true, "0.2.9": true, "v0.2.9": true, "0.3.0-rc.1": true,
		"0.3.0": false, "0.3.1": false, "1.0.0": false, "dev": false, "": false,
	} {
		require.Equal(t, want, olderThanFront(v), v)
	}
}

func TestFrontNodeNotUpdatedToOlderBuild(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	env.joinNode("de-1")
	env.joinFront("fr-1")
	require.True(t, env.h.pushBlockedForFront("fr-1", "0.2.0"))
	require.False(t, env.h.pushBlockedForFront("de-1", "0.2.0"))
	require.False(t, env.h.pushBlockedForFront("fr-1", "0.3.0"))
	require.False(t, env.h.pushBlockedForFront("fr-1", "dev"))
}

func TestFrontJoinLinkAndStatusWhenDisabled(t *testing.T) {
	env := startHub(t, nil)
	jc, err := env.h.Local().NodeJoinCommand(ctxT(t), 0)
	require.NoError(t, err)
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	require.False(t, link.Front())
	require.Equal(t, "127.0.0.1", link.Host)
	require.Equal(t, 44433, link.Port)
	st, err := env.h.Local().Status(ctxT(t))
	require.NoError(t, err)
	require.Nil(t, st.Hub.Front, "nothing changes with the front disabled")
	data, err := json.Marshal(st)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"front"`)
	require.NotContains(t, string(data), `"route"`)
}

func TestFrontPortReserved(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	// No tunnel may listen on the front port, and it is never suggested.
	cfg := env.h.Config()
	reserved, why := ports.Reserved(testFrontPort, cfg.Hub.ControlPort, cfg.Hub.ReservedPorts()...)
	require.True(t, reserved)
	require.Contains(t, why, "reserved")
	res, err := env.h.Local().PortCheck(ctxT(t), api.PortCheckRequest{Port: testFrontPort, Proto: config.ProtoTCP})
	require.NoError(t, err)
	require.Contains(t, res.Note, "reserved")
	require.NotContains(t, env.h.suggestPorts(ctxT(t), 20), testFrontPort)
}

func boolPtr(b bool) *bool { return &b }

func mustNodeState(h *Hub, id string) state.NodeState {
	ns, _ := h.nodeState(id)
	return ns
}

// errListener returns scripted Accept errors.
type errListener struct {
	net.Listener
	errs []error
}

func (e *errListener) Accept() (net.Conn, error) {
	err := e.errs[0]
	e.errs = e.errs[1:]
	return nil, err
}

func TestDirectListenerReportsPermanentFailuresOnly(t *testing.T) {
	var failed []error
	d := directListener{Listener: &errListener{errs: []error{
		syscall.EMFILE, net.ErrClosed, syscall.ECONNABORTED, os.ErrDeadlineExceeded, errors.New("boom"),
	}}, fail: func(err error) { failed = append(failed, err) }}
	for range 5 {
		_, _ = d.Accept()
	}
	require.Len(t, failed, 1, "temporary errors and a close are not reported")
	require.EqualError(t, failed[0], "boom")
}
