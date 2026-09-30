package setup

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

const goodToken = "Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZg"

// testHub is an in-process Control API server whose Join signs CSRs with a
// test CA.
type testHub struct {
	ca   *tlsutil.CA
	addr string

	mu   sync.Mutex
	reqs []api.JoinRequest
	ip   string
	// answer, when set, replaces the normal answer.
	answer func(req api.JoinRequest) (api.JoinResponse, error)
}

func startTestHub(t *testing.T) *testHub {
	t.Helper()
	ca, err := tlsutil.NewCA("ir-1", time.Now())
	require.NoError(t, err)
	certPEM, keyPEM, err := ca.IssueServer("ir-1", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ServerTLSConfig(ca.CertPEM, certPEM, keyPEM)
	require.NoError(t, err)
	h := &testHub{ca: ca}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	h.addr = ln.Addr().String()
	srv := &api.ControlServer{TLSConfig: cfg, Handler: h, HandshakeTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return h
}

func (h *testHub) link(token, fingerprint string) string {
	host, port, _ := net.SplitHostPort(h.addr)
	p, _ := strconv.Atoi(port)
	return api.FormatJoinLink(api.JoinLink{Token: token, Host: host, Port: p, Fingerprint: fingerprint})
}

func (h *testHub) requests() []api.JoinRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]api.JoinRequest(nil), h.reqs...)
}

// Join implements api.ControlHandler.
func (h *testHub) Join(_ context.Context, req api.JoinRequest, ip string) (api.JoinResponse, error) {
	h.mu.Lock()
	h.reqs = append(h.reqs, req)
	h.ip = ip
	answer := h.answer
	h.mu.Unlock()
	if answer != nil {
		return answer(req)
	}
	if req.Token != goodToken {
		return api.JoinResponse{}, deyerr.New(deyerr.N001, nil)
	}
	id := req.NodeID
	if id == "" {
		id = config.Slugify(req.Hostname)
	}
	cert, err := h.ca.SignCSR([]byte(req.CSRPEM), id, 0)
	if err != nil {
		return api.JoinResponse{}, err
	}
	return api.JoinResponse{
		NodeID: id, CertPEM: string(cert), CAPEM: string(h.ca.CertPEM),
		HubName: "ir-1", HubVersion: version.Version, HubAddr: h.addr, PublicIP: ip,
	}, nil
}

func (h *testHub) Authenticate(string, string, string) error {
	return deyerr.New(deyerr.N013, deyerr.Params{"path": "/"})
}
func (h *testHub) Session(s *api.Session) { <-s.Done() }
func (h *testHub) Upload(context.Context, string, string, io.Reader) error {
	return deyerr.New(deyerr.X008, deyerr.Params{"feature": "upload"})
}
func (h *testHub) Asset(context.Context, string) (api.AssetInfo, error) {
	return api.AssetInfo{}, deyerr.New(deyerr.X008, deyerr.Params{"feature": "assets"})
}

func joinOpts(root, link string, steps *stepLog) JoinOptions {
	o := JoinOptions{
		Root:        root,
		Runner:      exec.NewFake(),
		Link:        link,
		Name:        "Germany 1",
		Hostname:    func() (string, error) { return "vps-4711.example.net", nil },
		LookupGroup: withGroup,
		LookupUser:  withUser,
		Chown:       (&chownLog{}).chown,
	}
	if steps != nil {
		o.Progress = steps.add
	}
	return o
}

func TestJoinSuccess(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/os-release"),
		[]byte("NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\n"), 0o644))
	fakeProc(t, root)
	steps := &stepLog{}
	o := joinOpts(root, "  '"+hub.link(goodToken, hub.ca.Fingerprint())+"'\n", steps)
	o.ApplySysctl = true
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)

	require.Equal(t, []string{
		"parse_link=ok", "keys=ok", "join=ok", "secrets=ok", "sysctl=ok", "config=ok", "service=skipped",
	}, steps.final())
	st, _ := steps.get(StepParseLink)
	require.Equal(t, hub.addr, st.Detail, "the step detail never contains the token")

	require.Equal(t, "germany-1", res.NodeID)
	require.Equal(t, hub.addr, res.HubAddr)
	require.Equal(t, "ir-1", res.HubName)
	require.Equal(t, "127.0.0.1", res.PublicIP)
	require.Equal(t, hub.ca.Fingerprint(), res.CAFingerprint)
	require.True(t, res.Compatible)
	require.Equal(t, config.SysctlBalanced, res.SysctlProfile)

	reqs := hub.requests()
	require.Len(t, reqs, 1)
	req := reqs[0]
	require.Equal(t, goodToken, req.Token)
	require.Equal(t, "germany-1", req.NodeID)
	require.Equal(t, "Germany 1", req.Name)
	require.Equal(t, version.Version, req.Version)
	require.Equal(t, runtime.GOARCH, req.Arch)
	require.Equal(t, "Ubuntu 24.04.1 LTS", req.OS)
	require.Equal(t, "vps-4711.example.net", req.Hostname)
	require.Contains(t, req.CSRPEM, "CERTIFICATE REQUEST")

	sec := filepath.Join(root, "etc/deyroute/secrets")
	for _, n := range []string{"node.key", "node.crt", "ca.crt"} {
		require.Equalf(t, os.FileMode(0o600), mode(t, filepath.Join(sec, n)), "mode of %s", n)
	}
	require.Equal(t, os.FileMode(0o700), mode(t, sec))
	caPEM, err := os.ReadFile(filepath.Join(sec, "ca.crt"))
	require.NoError(t, err)
	require.Equal(t, hub.ca.CertPEM, caPEM)
	certPEM, err := os.ReadFile(filepath.Join(sec, "node.crt"))
	require.NoError(t, err)
	keyPEM, err := os.ReadFile(filepath.Join(sec, "node.key"))
	require.NoError(t, err)
	// The saved material is a working mTLS client configuration.
	_, err = tlsutil.ClientTLSConfig(caPEM, certPEM, keyPEM, "")
	require.NoError(t, err)

	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.RoleNode, cfg.Role)
	require.Equal(t, "germany-1", cfg.Node.ID)
	require.Equal(t, hub.addr, cfg.Node.HubAddr)
	require.Equal(t, hub.ca.Fingerprint(), cfg.Node.HubCAFingerprint)
	require.Equal(t, config.DefaultNodeCertFile, cfg.Node.CertFile)
	require.Equal(t, config.DefaultNodeKeyFile, cfg.Node.KeyFile)
	require.Equal(t, config.SysctlBalanced, cfg.Tuning.SysctlProfile)
	require.Equal(t, os.FileMode(0o600), mode(t, res.ConfigPath))
	require.Equal(t, "65535", procValue(t, root, "net.core.somaxconn"))

	// A second join on the same server is refused.
	_, err = Join(ctxT(t), joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil))
	requireTop(t, err, deyerr.I013)
	require.Len(t, hub.requests(), 1)
}

func TestJoinWithoutNameUsesHubDerivedID(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o.Name = "آلمان"
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)
	req := hub.requests()[0]
	require.Empty(t, req.NodeID, "a name without a usable slug lets the hub pick the id")
	require.Equal(t, "آلمان", req.Name)
	require.Equal(t, "Linux", req.OS)
	require.Equal(t, "vps-4711-example-net", res.NodeID)
	require.Equal(t, config.SysctlOff, res.SysctlProfile)
}

func TestJoinWrongFingerprint(t *testing.T) {
	hub := startTestHub(t)
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	root := t.TempDir()
	steps := &stepLog{}
	_, err = Join(ctxT(t), joinOpts(root, hub.link(goodToken, other.Fingerprint()), steps))
	e := requireTop(t, err, deyerr.N002)
	require.Contains(t, e.Why(), other.Fingerprint())
	st, _ := steps.get(StepJoin)
	require.Equal(t, api.StepFailed, st.Status)
	require.Equal(t, "DEY-N002", st.Error.Code)
	require.Empty(t, hub.requests(), "nothing is sent to a hub that fails the pin")
	requireNothingJoined(t, root)
}

// requireNothingJoined asserts that no secret and no config was written
// (the directory layout itself may exist: it is prepared before the join).
func requireNothingJoined(t *testing.T, root string) {
	t.Helper()
	for _, p := range []string{"etc/deyroute/config.yaml", "etc/deyroute/secrets/node.key", "etc/deyroute/secrets/node.crt", "etc/deyroute/secrets/ca.crt"} {
		require.NoFileExistsf(t, filepath.Join(root, p), "%s written", p)
	}
}

func TestJoinHubErrorPassthrough(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	steps := &stepLog{}
	_, err := Join(ctxT(t), joinOpts(root, hub.link("wrong-token-0123456789", hub.ca.Fingerprint()), steps))
	e := requireTop(t, err, deyerr.N001)
	require.Contains(t, e.Fix(), "deyroute node join-command")
	require.Equal(t, []string{"parse_link=ok", "keys=ok", "join=failed"}, steps.final())
	require.NoFileExists(t, filepath.Join(root, "etc/deyroute/config.yaml"))

	// Other hub codes pass through as well.
	hub.mu.Lock()
	hub.answer = func(api.JoinRequest) (api.JoinResponse, error) {
		return api.JoinResponse{}, deyerr.New(deyerr.N010, deyerr.Params{"node": "germany-1"})
	}
	hub.mu.Unlock()
	_, err = Join(ctxT(t), joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil))
	requireTop(t, err, deyerr.N010)
}

func TestJoinUnusableAnswers(t *testing.T) {
	hub := startTestHub(t)
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	sign := func(ca *tlsutil.CA, csr, id string) string {
		c, err := ca.SignCSR([]byte(csr), id, 0)
		require.NoError(t, err)
		return string(c)
	}
	cases := map[string]struct {
		answer func(req api.JoinRequest) api.JoinResponse
		code   deyerr.Code
	}{
		"invalid node id": {func(req api.JoinRequest) api.JoinResponse {
			return api.JoinResponse{NodeID: "canary", CertPEM: sign(hub.ca, req.CSRPEM, "canary"), CAPEM: string(hub.ca.CertPEM)}
		}, deyerr.N020},
		"cert of another CA": {func(req api.JoinRequest) api.JoinResponse {
			return api.JoinResponse{NodeID: "de-1", CertPEM: sign(other, req.CSRPEM, "de-1"), CAPEM: string(hub.ca.CertPEM)}
		}, deyerr.N002},
		"cert for another key": {func(api.JoinRequest) api.JoinResponse {
			csr, _, err := tlsutil.NewKeyAndCSR("x")
			require.NoError(t, err)
			return api.JoinResponse{NodeID: "de-1", CertPEM: sign(hub.ca, string(csr), "de-1"), CAPEM: string(hub.ca.CertPEM)}
		}, deyerr.N020},
		"cert names another node": {func(req api.JoinRequest) api.JoinResponse {
			return api.JoinResponse{NodeID: "de-1", CertPEM: sign(hub.ca, req.CSRPEM, "de-2"), CAPEM: string(hub.ca.CertPEM)}
		}, deyerr.N020},
		"no cert": {func(api.JoinRequest) api.JoinResponse {
			return api.JoinResponse{NodeID: "de-1", CertPEM: "junk", CAPEM: string(hub.ca.CertPEM)}
		}, deyerr.N020},
		"server cert instead of client cert": {func(api.JoinRequest) api.JoinResponse {
			c, _, err := hub.ca.IssueServer("de-1", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
			require.NoError(t, err)
			return api.JoinResponse{NodeID: "de-1", CertPEM: string(c), CAPEM: string(hub.ca.CertPEM)}
		}, deyerr.N002},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hub.mu.Lock()
			hub.answer = func(req api.JoinRequest) (api.JoinResponse, error) { return tc.answer(req), nil }
			hub.mu.Unlock()
			root := t.TempDir()
			_, err := Join(ctxT(t), joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil))
			requireTop(t, err, tc.code)
			requireNothingJoined(t, root)
		})
	}
}

func TestVerifyJoinResponseCAPin(t *testing.T) {
	ca, err := tlsutil.NewCA("ir-1", time.Now())
	require.NoError(t, err)
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	_, _, _, err = verifyJoinResponse(api.JoinResponse{NodeID: "de-1", CAPEM: string(other.CertPEM)}, ca.Fingerprint(), nil, time.Now())
	requireTop(t, err, deyerr.N002)

	// A CA bundle (e.g. during rotate-ca) is stored as clean PEM.
	csr, key, err := tlsutil.NewKeyAndCSR("de-1")
	require.NoError(t, err)
	cert, err := ca.SignCSR(csr, "de-1", 0)
	require.NoError(t, err)
	bundle := "junk before\n" + string(ca.CertPEM) + string(other.CertPEM)
	caPEM, certPEM, skewed, err := verifyJoinResponse(api.JoinResponse{NodeID: "de-1", CertPEM: string(cert), CAPEM: bundle}, ca.Fingerprint(), key, time.Now())
	require.NoError(t, err)
	require.False(t, skewed)
	require.Equal(t, string(ca.CertPEM)+string(other.CertPEM), string(caPEM))
	require.Equal(t, cert, certPEM)

	// A node clock outside the new certificate's validity is reported, not
	// mistaken for a certificate of another CA.
	resp := api.JoinResponse{NodeID: "de-1", CertPEM: string(cert), CAPEM: string(ca.CertPEM)}
	for _, at := range []time.Time{time.Now().Add(-3 * time.Hour), time.Now().Add(11 * 365 * 24 * time.Hour)} {
		_, _, skewed, err = verifyJoinResponse(resp, ca.Fingerprint(), key, at)
		require.NoError(t, err)
		require.True(t, skewed)
	}
	// A certificate of another CA is still refused at any time.
	foreign, err := other.SignCSR(csr, "de-1", 0)
	require.NoError(t, err)
	_, _, _, err = verifyJoinResponse(api.JoinResponse{NodeID: "de-1", CertPEM: string(foreign), CAPEM: string(ca.CertPEM)},
		ca.Fingerprint(), key, time.Now().Add(-3*time.Hour))
	requireTop(t, err, deyerr.N002)
}

func TestJoinWithSkewedClockWarns(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	steps := &stepLog{}
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), steps)
	// Two hours behind the hub. On a real node the handshake accepts a hub
	// certificate issued long before (here it runs on the real clock), but
	// the new node certificate (backdated by one hour only) is "not yet
	// valid" by this clock; that used to fail as DEY-N002 after the token
	// was spent.
	o.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.FileExists(t, res.ConfigPath)
	st, _ := steps.get(StepJoin)
	require.Equal(t, api.StepWarn, st.Status)
	require.Contains(t, st.Detail, "timedatectl set-ntp true")
}

func TestJoinBadLinkAndJoinFunc(t *testing.T) {
	root := t.TempDir()
	steps := &stepLog{}
	_, err := Join(ctxT(t), joinOpts(root, "https://example.com", steps))
	requireTop(t, err, deyerr.N006)
	require.Equal(t, []string{"parse_link=failed"}, steps.final())

	// A plain error from a custom JoinFunc becomes DEY-I014 {join}.
	link := api.FormatJoinLink(api.JoinLink{Token: goodToken, Host: "5.6.7.8", Port: 44433, Fingerprint: "sha256:" + stringOf('a', 64)})
	o := joinOpts(root, link, nil)
	o.JoinFunc = func(context.Context, string, string, api.JoinRequest) (api.JoinResponse, error) {
		return api.JoinResponse{}, errors.New("proxy refused")
	}
	_, err = Join(ctxT(t), o)
	e := requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "join")
	require.Contains(t, e.Detail, "proxy refused")

	// An unreachable hub is DEY-N009 from api.Join.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	dead := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	link = api.FormatJoinLink(api.JoinLink{Token: goodToken, Host: "127.0.0.1", Port: dead, Fingerprint: "sha256:" + stringOf('b', 64)})
	_, err = Join(ctxT(t), joinOpts(root, link, nil))
	requireTop(t, err, deyerr.N009)
}

func stringOf(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func TestJoinStartsNodeService(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	sock := shortSocket(t)
	serveSocket(t, sock)
	f := exec.NewFake()
	f.OnPrefix("systemctl ", exec.OK(""))
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o.Runner = f
	o.StartService = true
	o.SocketPath = sock
	res, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.True(t, res.ServiceStarted)
	require.True(t, f.Called("systemctl enable --now deyroute-node.service"))
	require.FileExists(t, filepath.Join(root, "etc/systemd/system/deyroute-node.service"))

	// A failing service start after the join is DEY-I014 {service}; the
	// config is already written (the token is spent).
	root2 := t.TempDir()
	f2 := exec.NewFake()
	f2.OnPrefix("systemctl ", exec.Fail(1, "boom"))
	o2 := joinOpts(root2, hub.link(goodToken, hub.ca.Fingerprint()), nil)
	o2.Runner = f2
	o2.StartService = true
	_, err = Join(ctxT(t), o2)
	e := requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "service")
	require.FileExists(t, filepath.Join(root2, "etc/deyroute/config.yaml"))
}

func TestJoinLocalFailuresAreI014(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	// /etc/deyroute/secrets is a file: the directory layout cannot be
	// prepared. This is found before the single-use token is sent.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc/deyroute"), 0o710))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/deyroute/secrets"), nil, 0o600))
	steps := &stepLog{}
	_, err := Join(ctxT(t), joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), steps))
	e := requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "keys")
	requireCode(t, err, deyerr.X032)
	require.Equal(t, []string{"parse_link=ok", "keys=failed"}, steps.final())
	require.Empty(t, hub.requests(), "the token is not spent on a local problem")

	// An invalid sysctl profile is refused before anything happens.
	root2 := t.TempDir()
	steps2 := &stepLog{}
	o := joinOpts(root2, hub.link(goodToken, hub.ca.Fingerprint()), steps2)
	o.ApplySysctl = true
	o.SysctlProfile = "turbo"
	_, err = Join(ctxT(t), o)
	e = requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "config")
	requireCode(t, err, deyerr.C013)
	require.Equal(t, []string{"parse_link=ok", "config=failed"}, steps2.final())
	require.Empty(t, hub.requests())
	require.NoDirExists(t, filepath.Join(root2, "etc/deyroute"))

	// A write failure after the join (node.crt is a directory) is
	// DEY-I014 {secrets}; no config is written.
	root3 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root3, "etc/deyroute/secrets/node.crt/x"), 0o700))
	steps3 := &stepLog{}
	_, err = Join(ctxT(t), joinOpts(root3, hub.link(goodToken, hub.ca.Fingerprint()), steps3))
	e = requireTop(t, err, deyerr.I014)
	require.Contains(t, e.Message(), "secrets")
	require.Equal(t, []string{"parse_link=ok", "keys=ok", "join=ok", "secrets=failed"}, steps3.final())
	require.NoFileExists(t, filepath.Join(root3, "etc/deyroute/config.yaml"))
}

func TestJoinCreatesBackendUserBeforeJoining(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	su := &sysUsers{}
	f := exec.NewFake()
	f.Handler = su.handle
	steps := &stepLog{}
	o := joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), steps)
	o.Runner = f
	o.LookupGroup, o.LookupUser = su.lookup, su.lookupUser
	_, err := Join(ctxT(t), o)
	require.NoError(t, err)
	require.Len(t, su.ran(), 1)
	st, _ := steps.get(StepKeys)
	require.Equal(t, api.StepOK, st.Status)

	// A user that cannot be created is a warning on the keys step.
	root2 := t.TempDir()
	su2 := &sysUsers{fail: true}
	f2 := exec.NewFake()
	f2.Handler = su2.handle
	steps2 := &stepLog{}
	o2 := joinOpts(root2, hub.link(goodToken, hub.ca.Fingerprint()), steps2)
	o2.Runner = f2
	o2.LookupGroup, o2.LookupUser = su2.lookup, su2.lookupUser
	_, err = Join(ctxT(t), o2)
	require.NoError(t, err)
	st, _ = steps2.get(StepKeys)
	require.Equal(t, api.StepWarn, st.Status)
	require.Equal(t, "DEY-X032", st.Error.Code)
}

func TestRequestedNodeIDAndHostname(t *testing.T) {
	for name, want := range map[string]string{
		"":          "",
		"Germany 1": "germany-1",
		"de-1":      "de-1",
		"canary":    "",
		"x":         "",
		"tunnel":    "tunnel",
		"!!":        "",
	} {
		require.Equalf(t, want, requestedNodeID(name), "name %q", name)
	}
	require.Equal(t, "node", hostname(func() (string, error) { return "", errors.New("x") }))
	require.Equal(t, "h", hostname(func() (string, error) { return " h ", nil }))
	require.NotEmpty(t, hostname(nil))
}

func TestOSReleaseValue(t *testing.T) {
	data := []byte("# comment\nNAME=Debian\nPRETTY_NAME='Debian GNU/Linux 12 (bookworm)'\n")
	require.Equal(t, "Debian GNU/Linux 12 (bookworm)", osReleaseValue(data, "PRETTY_NAME"))
	require.Equal(t, "Debian", osReleaseValue(data, "NAME"))
	require.Empty(t, osReleaseValue(data, "ID"))

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "usr/lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "usr/lib/os-release"), []byte(`PRETTY_NAME="Rocky Linux 9.4"`), 0o644))
	e := newEnv(envOptions{Root: root, Runner: exec.NewFake()})
	require.Equal(t, "Rocky Linux 9.4", e.osPrettyName())
}
