package node

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front"
	"github.com/localroot4/deyroute/internal/front/fronttest"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// frontTestSecret has the shape of a secrets.NewToken value. It must never
// show up in a log line, an error, the status or the doctor output.
const frontTestSecret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"

const frontDomain = "front.example.com"

// frontEnv is a node whose hub sits behind a fake CDN: the control server
// reads from a real front.Server, the fake CDN terminates TLS in front of it,
// and the node dials through front.Dialer with the CDN's root pool.
type frontEnv struct {
	*env
	srv *front.Server
	cdn *fronttest.CDN

	mu     sync.Mutex
	dialed []string    // TCP addresses the dialer was asked for
	times  []time.Time // when
}

// startHubOn serves h with a hub certificate of ca on ln.
func startHubOn(t *testing.T, ca *tlsutil.CA, caPEM []byte, h api.ControlHandler, ln net.Listener) {
	t.Helper()
	certPEM, keyPEM, err := ca.IssueServer("hub", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ServerTLSConfig(caPEM, certPEM, keyPEM)
	require.NoError(t, err)
	srv := &api.ControlServer{TLSConfig: cfg, Handler: h, HandshakeTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
}

func newFrontEnv(t *testing.T, cdn fronttest.Options) *frontEnv {
	t.Helper()
	e := newEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv, err := front.NewServer(ln, front.ServerOptions{Secret: frontTestSecret})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	startHubOn(t, e.ca, e.caPEM, e.hub, srv)

	cdn.OriginAddr = srv.Addr().String()
	cdn.OriginTLS = true
	cdn.ClientIP = "203.0.113.7"
	c, err := fronttest.NewCDN(cdn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	fe := &frontEnv{env: e, srv: srv, cdn: c}
	d := &front.Dialer{
		RootCAs: c.CAPool(), UpgradeTimeout: 3 * time.Second,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			fe.mu.Lock()
			fe.dialed = append(fe.dialed, addr)
			fe.times = append(fe.times, time.Now())
			fe.mu.Unlock()
			var nd net.Dialer
			return nd.DialContext(ctx, "tcp", c.Addr())
		},
	}
	e.opts.FrontDial = d.DialControl
	require.NoError(t, tlsutil.WriteSecret(e.path(config.SecretsDir+"/"+config.DefaultFrontSecretFile), []byte(frontTestSecret+"\n")))
	fe.setFrontConfig(func(n *config.NodeSelf) {})
	return fe
}

// frontAddr is the domain:port the node is configured with.
func (fe *frontEnv) frontAddr() string {
	return net.JoinHostPort(frontDomain, strconv.Itoa(fe.cdn.Port()))
}

// setFrontConfig writes the node config in front mode (the CDN port is no
// Cloudflare port, so the scheme is explicit) and lets mod adjust it.
func (fe *frontEnv) setFrontConfig(mod func(*config.NodeSelf)) {
	fe.t.Helper()
	cfg := config.NewNode(testNode, fe.frontAddr(), fe.ca.Fingerprint())
	cfg.Node.Front = config.NodeFront{SecretFile: config.DefaultFrontSecretFile, Scheme: config.FrontSchemeWSS, EdgeIP: "127.0.0.1"}
	mod(cfg.Node)
	require.NoError(fe.t, config.Save(fe.path(config.DefaultPath), cfg))
}

func (fe *frontEnv) dials() ([]string, []time.Time) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	return append([]string(nil), fe.dialed...), append([]time.Time(nil), fe.times...)
}

// run starts the agent and returns without waiting for a session.
func (fe *frontEnv) run() func() {
	fe.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, fe.opts) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(fe.t, err)
			case <-time.After(20 * time.Second):
				fe.t.Fatal("agent did not stop")
			}
		})
	}
	fe.t.Cleanup(stop)
	eventually(fe.t, func() bool {
		l, err := api.Dial(fe.socket, api.DialOptions{Service: ServiceName, Timeout: 5 * time.Second})
		if err != nil {
			return false
		}
		_, err = l.Status(context.Background())
		return err == nil
	}, "the local API is up")
	return stop
}

func (fe *frontEnv) status() api.Status {
	fe.t.Helper()
	st, err := dial(fe.t, fe.env).Status(context.Background())
	require.NoError(fe.t, err)
	return st
}

// requireNoSecret fails if the secret is anywhere in the agent log, the
// status or the doctor data.
func (fe *frontEnv) requireNoSecret() {
	fe.t.Helper()
	require.NotContains(fe.t, fe.logs.String(), frontTestSecret, "agent log")
	l := dial(fe.t, fe.env)
	st, err := l.Status(context.Background())
	require.NoError(fe.t, err)
	b, err := json.Marshal(st)
	require.NoError(fe.t, err)
	require.NotContains(fe.t, string(b), frontTestSecret, "status")
	dd, err := l.DoctorCollect(context.Background(), "")
	require.NoError(fe.t, err)
	b, err = json.Marshal(dd)
	require.NoError(fe.t, err)
	require.NotContains(fe.t, string(b), frontTestSecret, "doctor data")
}

func TestFrontNodeConnectsAndHeartbeats(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	unit := "deyroute-tun@main.de-1.backhaul-wssmux.service"
	fe.sys.setState(unit, "active")
	s := fe.start()

	require.Equal(t, "front", s.Via)
	require.Equal(t, "203.0.113.7", s.RemoteIP, "the hub sees the real client address")
	eventually(t, func() bool {
		select {
		case hb := <-s.Heartbeats():
			return hb.Units[unit] == "active"
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, "heartbeats arrive through the front")
	out, err := call[map[string]string](t, s, api.CmdSysinfo, nil)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// The dial went to the configured edge address, with the domain as host.
	addrs, _ := fe.dials()
	require.Equal(t, "127.0.0.1:"+strconv.Itoa(fe.cdn.Port()), addrs[0])
	reqs := fe.cdn.Requests()
	require.NotEmpty(t, reqs)
	require.Equal(t, frontDomain+":"+strconv.Itoa(fe.cdn.Port()), reqs[0].Host)
	require.True(t, reqs[0].Upgrade)

	// Status: the front domain and port plus the marker, no warning.
	eventually(t, func() bool {
		st := fe.status()
		return st.NodeSelf != nil && st.NodeSelf.Connected
	}, "status shows the node connected")
	st := fe.status()
	require.True(t, st.NodeSelf.Front)
	require.Equal(t, fe.frontAddr(), st.NodeSelf.HubAddr)
	require.Empty(t, st.Warnings)
	require.Contains(t, fe.logs.String(), `"route":"front"`)

	// Doctor: the connectivity section names the route and no dial error.
	dd, err := dial(t, fe.env).DoctorCollect(context.Background(), "")
	require.NoError(t, err)
	conn := dd.Sections[ConnectivitySection]
	require.Contains(t, conn, "route: via front")
	require.Contains(t, conn, "hub: "+fe.frontAddr())
	require.Contains(t, conn, "last dial error: none")
	fe.requireNoSecret()
}

func TestFrontReconnectAfterCDNDrop(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	s := fe.start()
	before := len(fe.cdn.Requests())
	warning := func(code deyerr.Code) func() bool {
		return func() bool {
			st := fe.status()
			return len(st.Warnings) == 1 && st.Warnings[0].Code == string(code)
		}
	}

	// The origin is down behind the CDN (Cloudflare 521) and the stream ends:
	// the node says DEY-N017 and keeps trying.
	fe.cdn.Configure(func(o *fronttest.Options) { o.Canned = fronttest.OriginError(521) })
	s.Close()
	eventually(t, warning(deyerr.N017), "status reports DEY-N017 for a 521")
	require.Contains(t, fe.status().Warnings[0].Message, "521")

	// Then the CDN swallows everything: new connections are never answered,
	// the dial times out and the status says DEY-N016.
	fe.cdn.Configure(func(o *fronttest.Options) { o.Canned = nil; o.DropAll = true })
	eventually(t, warning(deyerr.N016), "status reports DEY-N016 while the CDN drops everything")
	dd, err := dial(t, fe.env).DoctorCollect(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, dd.Sections[ConnectivitySection], "last dial error: DEY-N016")
	require.Empty(t, fe.hub.sessions, "no session while the CDN drops")

	fe.cdn.Configure(func(o *fronttest.Options) { o.DropAll = false })
	s2 := waitSession(t, fe.hub)
	require.NotSame(t, s, s2)
	require.Equal(t, "front", s2.Via)
	require.Greater(t, len(fe.cdn.Requests()), before)
	_, err = call[map[string]string](t, s2, api.CmdSysinfo, nil)
	require.NoError(t, err)
	// The failure is gone from the status once the node is back.
	eventually(t, func() bool {
		st := fe.status()
		return st.NodeSelf.Connected && len(st.Warnings) == 0
	}, "the dial failure is cleared after the reconnect")
	fe.requireNoSecret()
}

func TestFrontRetryAfterIsHonoured(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	s := fe.start()

	// A rate limit with Retry-After: the next attempts are at least that far
	// apart although the backoff ceiling of the test is 200 ms.
	const wait = 2
	fe.cdn.Configure(func(o *fronttest.Options) { o.Canned = fronttest.RateLimited(strconv.Itoa(wait)) })
	_, base := fe.dials()
	s.Close()
	eventually(t, func() bool { _, ts := fe.dials(); return len(ts) >= len(base)+2 }, "two attempts under the rate limit")
	_, ts := fe.dials()
	first, second := ts[len(base)], ts[len(base)+1]
	require.GreaterOrEqual(t, second.Sub(first), wait*time.Second-100*time.Millisecond, "Retry-After floors the reconnect delay")
	st := fe.status()
	require.Len(t, st.Warnings, 1)
	require.Equal(t, string(deyerr.N017), st.Warnings[0].Code)
	require.Contains(t, st.Warnings[0].Message, "rate limited")

	fe.cdn.Configure(func(o *fronttest.Options) { o.Canned = nil })
	s2 := waitSession(t, fe.hub)
	require.Equal(t, "front", s2.Via)
}

func TestFrontPermanentErrorBacksOffAndLogsOnce(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	// A wrong secret path is answered with the decoy 404: N017, permanent.
	require.NoError(t, tlsutil.WriteSecret(fe.path(config.SecretsDir+"/"+config.DefaultFrontSecretFile), []byte("a-completely-wrong-secret-0123456789\n")))
	fe.run()

	eventually(t, func() bool {
		st := fe.status()
		return len(st.Warnings) == 1 && st.Warnings[0].Code == string(deyerr.N017)
	}, "status reports DEY-N017")
	st := fe.status()
	require.Contains(t, st.Warnings[0].Message, "not found")
	dd, err := dial(t, fe.env).DoctorCollect(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, dd.Sections[ConnectivitySection], "last dial error: DEY-N017 HTTP 404")

	// The backoff of a permanent error grows past the 200 ms ceiling of an
	// ordinary one: in 3 s a plain loop would try about 15 times.
	time.Sleep(3 * time.Second)
	addrs, _ := fe.dials()
	require.LessOrEqual(t, len(addrs), 8, "permanent errors back off long")
	require.GreaterOrEqual(t, len(addrs), 2)
	logs := fe.logs.String()
	require.Equal(t, 1, strings.Count(logs, "will keep failing until the configuration changes"), "logged once as a warning")
	require.NotContains(t, logs, "a-completely-wrong-secret")
	require.NotContains(t, logs, frontTestSecret)
}

func TestFrontConfigChangeAppliesOnNextReconnect(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	s := fe.start()

	// New edge address and a new front host name, edited by hand: no restart.
	port := strconv.Itoa(fe.cdn.Port())
	fe.setFrontConfig(func(n *config.NodeSelf) {
		n.HubAddr = "localhost:" + port
		n.Front.EdgeIP = "127.0.0.2"
	})
	s.Close()
	s2 := waitSession(t, fe.hub)
	require.Equal(t, "front", s2.Via)

	addrs, _ := fe.dials()
	require.Equal(t, "127.0.0.2:"+port, addrs[len(addrs)-1], "the new edge_ip is used")
	reqs := fe.cdn.Requests()
	last := reqs[len(reqs)-1]
	require.Equal(t, "localhost:"+port, last.Host, "the new hub_addr is used")
	require.Equal(t, "localhost", last.SNI)
	eventually(t, func() bool { return fe.status().NodeSelf.HubAddr == "localhost:"+port }, "status follows the config")
}

func TestFrontMissingSecretFileIsReportedAndRecovers(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	secretPath := fe.path(config.SecretsDir + "/" + config.DefaultFrontSecretFile)
	require.NoError(t, os.Remove(secretPath))
	fe.run()

	eventually(t, func() bool {
		st := fe.status()
		return len(st.Warnings) == 1 && st.Warnings[0].Code == string(deyerr.T008)
	}, "a missing secret file is reported with its code")
	addrs, _ := fe.dials()
	require.Empty(t, addrs, "nothing is dialed without a secret")

	require.NoError(t, tlsutil.WriteSecret(secretPath, []byte(frontTestSecret+"\n")))
	s := waitSession(t, fe.hub)
	require.Equal(t, "front", s.Via)
	eventually(t, func() bool {
		select {
		case hb := <-s.Heartbeats():
			return hb.LastError == ""
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, "the heartbeat no longer carries the T008 error")
	fe.requireNoSecret()
}

func TestFrontUploadAndSelfUpdateUseTheFront(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	newBin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	fe.hub.asset, fe.hub.assetV = newBin, "9.9.9"
	writeFile(t, fe.path(config.BinaryPath), "old binary", 0o755)
	s := fe.start()
	before := fe.cdn.Stats().Upgrades

	sum := sha256.Sum256(newBin)
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: hex.EncodeToString(sum[:])})
	require.NoError(t, err)
	data, err := os.ReadFile(fe.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, newBin, data)
	require.Greater(t, fe.cdn.Stats().Upgrades, before, "the asset download opened its own connection through the CDN")
	fe.requireNoSecret()
}

func TestSetHubFromHubNeverLeavesTheFront(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	s := fe.start()

	// announce-move / control port change: a plain host:port from the hub.
	_, err := call[json.RawMessage](t, s, api.CmdSetHub, api.SetHubArgs{Addr: "5.6.7.8:44433"})
	require.NoError(t, err, "answered with success: a retry would not help")
	cfg := fe.config()
	require.Equal(t, config.DefaultFrontSecretFile, cfg.Node.Front.SecretFile, "node.front is kept")
	require.Equal(t, fe.frontAddr(), cfg.Node.HubAddr, "node.hub_addr is kept")
	require.Contains(t, fe.logs.String(), "stays on the front")
	select {
	case <-s.Done():
		t.Fatal("the node must stay connected through the front")
	case <-time.After(300 * time.Millisecond):
	}
	_, err = call[map[string]string](t, s, api.CmdSysinfo, nil)
	require.NoError(t, err)

	// A malformed target is still refused, without echoing a secret path.
	_, err = call[json.RawMessage](t, s, api.CmdSetHub, api.SetHubArgs{Addr: "wss://front.example.com:443/short"})
	e := requireCode(t, err, deyerr.C013)
	require.NotContains(t, e.Error()+e.Detail, "short")
}

func TestSetHubOwnerSwitchesBetweenDirectAndFront(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	s := fe.start()
	l := dial(t, fe.env)
	ctx := context.Background()
	secretPath := fe.path(config.SecretsDir + "/" + config.DefaultFrontSecretFile)

	// A plain host:port from the owner clears front mode explicitly.
	require.NoError(t, l.NodeSetHub(ctx, fe.hubAddr))
	s2 := waitSession(t, fe.hub)
	require.NotSame(t, s, s2)
	require.Equal(t, "", s2.Via, "direct again")
	cfg := fe.config()
	require.Empty(t, cfg.Node.Front.SecretFile)
	require.Equal(t, fe.hubAddr, cfg.Node.HubAddr)
	require.NoFileExists(t, secretPath, "the unused secret is removed")
	eventually(t, func() bool {
		st := fe.status()
		return !st.NodeSelf.Front && st.NodeSelf.Connected
	}, "status drops the front marker")

	// The front form switches back: secret file 0600, node.front, hub_addr.
	target := "wss://" + fe.frontAddr() + "/" + frontTestSecret
	require.NoError(t, l.NodeSetHub(ctx, target))
	s3 := waitSession(t, fe.hub)
	require.Equal(t, "front", s3.Via)
	cfg = fe.config()
	require.Equal(t, config.DefaultFrontSecretFile, cfg.Node.Front.SecretFile)
	require.Equal(t, config.FrontSchemeWSS, cfg.Node.Front.Scheme, "the port is no Cloudflare port: the scheme is stored")
	require.Equal(t, fe.frontAddr(), cfg.Node.HubAddr)
	fi, err := os.Stat(secretPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	eventually(t, func() bool { return fe.status().NodeSelf.Front }, "status shows the front")
	fe.requireNoSecret()
}

func TestSetHubFrontTargetErrorsHideTheSecret(t *testing.T) {
	e := newEnv(t)
	e.start()
	l := dial(t, e)
	secret := "SecretSecretSecret1234"
	for _, bad := range []string{
		"wss://front.example.com/" + secret,            // no port
		"wss://front.example.com:0/" + secret,          // bad port
		"ws://front.example.com:2052",                  // no secret
		"wss://front.example.com:443/" + secret + "/x", // two segments
		"wss://front.example.com:443/" + secret + "?x", // query
	} {
		err := l.NodeSetHub(context.Background(), bad)
		de := requireCode(t, err, deyerr.C013)
		require.NotContains(t, de.Error(), secret, bad)
		require.NotContains(t, de.Message(), secret, bad)
	}
	require.NotContains(t, e.logs.String(), secret)
}

// Direct mode is exactly what it was: no dial hook, no front marker, the
// default open timeout.
func TestDirectModeHasNoFrontDial(t *testing.T) {
	e := newEnv(t)
	cfg, err := config.Load(e.path(config.DefaultPath))
	require.NoError(t, err)
	o := e.opts.withDefaults()
	a := newAgent(o, cfg, e.path(config.DefaultPath), o.Logger)
	tc, err := a.loadTLS()
	require.NoError(t, err)

	c := a.controlClient(tc)
	require.Nil(t, c.DialFunc)
	require.Nil(t, c.Dial)
	r, err := a.requestClient()
	require.NoError(t, err)
	require.Nil(t, r.DialFunc)
	require.False(t, a.status().NodeSelf.Front)
	require.False(t, a.frontMode())

	// A node in front mode gets the hook on both clients.
	fe := newFrontEnv(t, fronttest.Options{})
	fcfg, err := config.Load(fe.path(config.DefaultPath))
	require.NoError(t, err)
	fo := fe.opts.withDefaults()
	fa := newAgent(fo, fcfg, fe.path(config.DefaultPath), fo.Logger)
	ftc, err := fa.loadTLS()
	require.NoError(t, err)
	require.NotNil(t, fa.controlClient(ftc).DialFunc)
	fr, err := fa.requestClient()
	require.NoError(t, err)
	require.NotNil(t, fr.DialFunc)
	require.True(t, fa.status().NodeSelf.Front)
}

// The secret is registered with the log redactor when the agent starts, so
// even a line that carries it by accident is masked.
func TestFrontSecretIsRegisteredWithTheRedactor(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	other := "Registered-0123456789abcdefXYZ"
	require.NoError(t, tlsutil.WriteSecret(fe.path(config.SecretsDir+"/"+config.DefaultFrontSecretFile), []byte(other+"\n")))
	fe.run()
	eventually(t, func() bool { return !strings.Contains(dlog.Redact("x "+other), other) }, "the secret is masked by the redactor")
}

// shimArgs is a front node's backend.render payload as the hub's planner
// produces it: the client under the shim, the hub part of the shim file.
func shimArgs(self string) api.BackendRenderArgs {
	args := renderArgs("main", "backhaul", "tcpmux")
	f := front.ShimFile{Port: 30001, Tunnel: "main", Node: testNode, Token: "tunnel-token", CA: "CA PEM"}
	data, _ := f.Marshal()
	args.Files[front.ShimFileName] = data
	args.Unit.ExecStart = front.ShimArgv(self, args.ConfigDir, args.Unit.ExecStart)
	return args
}

// A front node accepts the shim wrapper of its own config directory only and
// fills in the shim file from its own config: the front address and the path
// secret never come from the hub.
func TestFrontShimRenderIsCompletedByTheNode(t *testing.T) {
	fe := newFrontEnv(t, fronttest.Options{})
	cfg, err := config.Load(fe.path(config.DefaultPath))
	require.NoError(t, err)
	o := fe.opts.withDefaults()
	a := newAgent(o, cfg, fe.path(config.DefaultPath), o.Logger)
	a.frontSettings()

	args := shimArgs(o.SelfBinary)
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "backhaul", args.ConfigDir, args.Unit))
	require.NoError(t, a.completeShimFile(api.CmdBackendRender, &args))
	f, err := front.ParseShimFile(args.Files[front.ShimFileName])
	require.NoError(t, err)
	require.True(t, f.Complete())
	require.Equal(t, fe.frontAddr(), f.Hub)
	require.Equal(t, frontTestSecret, f.Secret)
	require.Equal(t, "tunnel-token", f.Token, "the hub part is kept")
	_, err = f.Target()
	require.NoError(t, err)

	// Another config directory, another subcommand or the shim alone are not
	// allowed in a unit.
	other := shimArgs(o.SelfBinary)
	other.Unit.ExecStart = front.ShimArgv(o.SelfBinary, "/etc/deyroute/backends/backhaul/x/de-1/tcpmux", []string{backhaulBin, "-c", "x"})
	require.Error(t, a.checkCommands(api.CmdBackendRender, "backhaul", other.ConfigDir, other.Unit))
	relay := shimArgs(o.SelfBinary)
	relay.Unit.ExecStart = []string{o.SelfBinary, "pair", o.SelfBinary, "relay", "--config", "x", "--", backhaulBin, "-c", "x"}
	require.Error(t, a.checkCommands(api.CmdBackendRender, "backhaul", relay.ConfigDir, relay.Unit))
	alone := shimArgs(o.SelfBinary)
	alone.Unit.ExecStart = []string{o.SelfBinary, front.ShimCommand, "--config", alone.ConfigDir + "/" + front.ShimFileName}
	require.Error(t, a.checkCommands(api.CmdBackendRender, "backhaul", alone.ConfigDir, alone.Unit))

	// A shim file for another node or tunnel is refused.
	wrong := shimArgs(o.SelfBinary)
	wf := front.ShimFile{Port: 30001, Tunnel: "main", Node: "other", Token: "t", CA: "c"}
	wrong.Files[front.ShimFileName], _ = wf.Marshal()
	require.Error(t, a.completeShimFile(api.CmdBackendRender, &wrong))
}

// A direct node refuses a tunnel the hub wants to run through the front.
func TestDirectNodeRefusesTheShim(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	args := shimArgs(e.opts.withDefaults().SelfBinary)
	_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.Error(t, err)
	require.Contains(t, deyerr.As(err).Why(), "not in front mode")
	_, err = os.Stat(e.path(args.ConfigDir))
	require.True(t, os.IsNotExist(err), "nothing is written")
}
