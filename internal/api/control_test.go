package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	require.Truef(t, deyerr.HasCode(err, code), "want %s, got %v", code, err)
	return deyerr.As(err)
}

// testPKI is a hub CA with a hub control certificate.
type testPKI struct {
	ca        *tlsutil.CA
	serverCfg *tls.Config
}

func newPKI(t *testing.T) *testPKI {
	t.Helper()
	ca, err := tlsutil.NewCA("test-hub", time.Now())
	require.NoError(t, err)
	certPEM, keyPEM, err := ca.IssueServer("hub", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ServerTLSConfig(ca.CertPEM, certPEM, keyPEM)
	require.NoError(t, err)
	return &testPKI{ca: ca, serverCfg: cfg}
}

// nodeConfig issues a node certificate for id and returns its client config.
func (p *testPKI) nodeConfig(t *testing.T, id string) (*tls.Config, string) {
	t.Helper()
	csr, key, err := tlsutil.NewKeyAndCSR(id)
	require.NoError(t, err)
	cert, err := p.ca.SignCSR(csr, id, 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ClientTLSConfig(p.ca.CertPEM, cert, key, "")
	require.NoError(t, err)
	fp, err := tlsutil.CertSHA256Hex(cert)
	require.NoError(t, err)
	return cfg, fp
}

// fakeHub implements ControlHandler for tests.
type fakeHub struct {
	pki      *testPKI
	sessions chan *Session

	mu      sync.Mutex
	joinErr error
	authErr error
	joinIP  string
	uploads map[string]string
	upErr   error
	asset   func(arch string) (AssetInfo, error)
}

func newFakeHub(p *testPKI) *fakeHub {
	return &fakeHub{pki: p, sessions: make(chan *Session, 16), uploads: map[string]string{}}
}

func (f *fakeHub) Join(_ context.Context, req JoinRequest, ip string) (JoinResponse, error) {
	f.mu.Lock()
	f.joinIP = ip
	err := f.joinErr
	f.mu.Unlock()
	if err != nil {
		return JoinResponse{}, err
	}
	if req.Token != "good-token-0123456789" {
		return JoinResponse{}, deyerr.New(deyerr.N001, nil)
	}
	cert, err := f.pki.ca.SignCSR([]byte(req.CSRPEM), req.NodeID, 0)
	if err != nil {
		return JoinResponse{}, err
	}
	return JoinResponse{NodeID: req.NodeID, CertPEM: string(cert), CAPEM: string(f.pki.ca.CertPEM), HubName: "ir-1", PublicIP: ip}, nil
}

func (f *fakeHub) Authenticate(nodeID, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.authErr != nil {
		return f.authErr
	}
	if nodeID == "removed" {
		return deyerr.New(deyerr.N008, deyerr.Params{"node": nodeID})
	}
	return nil
}

func (f *fakeHub) Session(s *Session) {
	f.sessions <- s
	<-s.Done()
}

func (f *fakeHub) Upload(_ context.Context, id, nodeID string, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upErr != nil {
		return f.upErr
	}
	f.uploads[id] = nodeID + ":" + string(data)
	return nil
}

func (f *fakeHub) Asset(_ context.Context, arch string) (AssetInfo, error) {
	if f.asset == nil {
		return AssetInfo{}, deyerr.New(deyerr.X008, deyerr.Params{"feature": "assets"})
	}
	return f.asset(arch)
}

// startServer serves h on addr ("" = random port) until the returned stop.
func startServer(t *testing.T, p *testPKI, h ControlHandler, addr string) (string, func()) {
	t.Helper()
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	srv := &ControlServer{TLSConfig: p.serverCfg, Handler: h, HandshakeTimeout: 2 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			require.NoError(t, <-done)
		})
	}
	t.Cleanup(stop)
	return ln.Addr().String(), stop
}

// startClient runs a ControlClient for node id until the test ends.
func startClient(t *testing.T, cfg *tls.Config, addr, id string, h CommandHandler, mod func(*ControlClient)) *ControlClient {
	t.Helper()
	c := &ControlClient{
		HubAddr:           addr,
		TLSConfig:         cfg,
		Hello:             func() Hello { return Hello{NodeID: id, Version: "1.0.0", Arch: "amd64"} },
		Heartbeat:         func() Heartbeat { return Heartbeat{CPUPercent: 12.5, Units: map[string]string{"u": "active"}} },
		Handler:           h,
		BackoffMin:        20 * time.Millisecond,
		BackoffMax:        100 * time.Millisecond,
		HeartbeatInterval: 50 * time.Millisecond,
	}
	if mod != nil {
		mod(c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return c
}

func waitSession(t *testing.T, h *fakeHub) *Session {
	t.Helper()
	select {
	case s := <-h.sessions:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("no session")
		return nil
	}
}

func TestJoin(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	csr, _, err := tlsutil.NewKeyAndCSR("de-1")
	require.NoError(t, err)
	req := JoinRequest{Token: "good-token-0123456789", NodeID: "de-1", CSRPEM: string(csr), Version: "1.0.0"}
	ctx := context.Background()

	resp, err := Join(ctx, addr, p.ca.Fingerprint(), req)
	require.NoError(t, err)
	require.Equal(t, "de-1", resp.NodeID)
	require.Equal(t, "127.0.0.1", resp.PublicIP)
	cert, err := tlsutil.ParseCert([]byte(resp.CertPEM))
	require.NoError(t, err)
	require.Equal(t, "de-1", cert.Subject.CommonName)

	// Wrong token: the hub's code crosses the wire.
	bad := req
	bad.Token = "wrong-token-0123456789"
	_, err = Join(ctx, addr, p.ca.Fingerprint(), bad)
	requireCode(t, err, deyerr.N001)

	hub.mu.Lock()
	hub.joinErr = deyerr.New(deyerr.N007, deyerr.Params{"ip": "127.0.0.1"})
	hub.mu.Unlock()
	_, err = Join(ctx, addr, p.ca.Fingerprint(), req)
	e := requireCode(t, err, deyerr.N007)
	require.Contains(t, e.Message(), "127.0.0.1")
	hub.mu.Lock()
	hub.joinErr = nil
	hub.mu.Unlock()

	// A link pinning another CA fails before anything is sent.
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	_, err = Join(ctx, addr, other.Fingerprint(), req)
	requireCode(t, err, deyerr.N002)

	// Oversized request.
	huge := req
	huge.CSRPEM = strings.Repeat("A", MaxJoinRequest+10)
	_, err = Join(ctx, addr, p.ca.Fingerprint(), huge)
	requireCode(t, err, deyerr.N015)

	// Nobody listening.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	dead := ln.Addr().String()
	require.NoError(t, ln.Close())
	_, err = Join(ctx, dead, p.ca.Fingerprint(), req)
	e = requireCode(t, err, deyerr.N009)
	require.Contains(t, e.Message(), dead)
}

// caSwapHub answers join with a CA other than the pinned one.
type caSwapHub struct {
	*fakeHub
	otherCA []byte
}

func (h caSwapHub) Join(context.Context, JoinRequest, string) (JoinResponse, error) {
	return JoinResponse{NodeID: "de-1", CAPEM: string(h.otherCA)}, nil
}

func TestJoinResponseCAMustMatchPin(t *testing.T) {
	p := newPKI(t)
	other, err := tlsutil.NewCA("other", time.Now())
	require.NoError(t, err)
	addr, _ := startServer(t, p, caSwapHub{newFakeHub(p), other.CertPEM}, "")
	_, err = Join(context.Background(), addr, p.ca.Fingerprint(), JoinRequest{Token: "x"})
	requireCode(t, err, deyerr.N002)
}

// rawClient performs a request with cfg (nil client certificate allowed).
func rawRequest(t *testing.T, cfg *tls.Config, method, url string, body io.Reader) (*http.Response, func()) {
	t.Helper()
	tr := newTransport(cfg, &dialErrBox{})
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	return resp, func() {
		_ = resp.Body.Close()
		tr.CloseIdleConnections()
	}
}

func TestUnauthenticatedRejected(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	noCert, err := tlsutil.ClientTLSConfig(p.ca.CertPEM, nil, nil, "")
	require.NoError(t, err)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, PathStream},
		{http.MethodPost, PathUploadPrefix + "u1"},
		{http.MethodGet, PathAssetsPrefix + "amd64"},
		{http.MethodGet, PathJoin},
		{http.MethodGet, "/nope"},
	} {
		resp, done := rawRequest(t, noCert, tc.method, "https://"+addr+tc.path, strings.NewReader("{}"))
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode, tc.path)
		err := readControlResponse(resp, nil)
		e := requireCode(t, err, deyerr.N013)
		require.Contains(t, e.Why(), tc.path)
		done()
	}

	// The client API reports the same.
	c := &ControlClient{HubAddr: addr, TLSConfig: noCert}
	requireCode(t, c.Upload(context.Background(), "u1", strings.NewReader("x")), deyerr.N013)

	// A certificate the hub no longer accepts (removed node): hub's code, 401.
	removed, _ := p.nodeConfig(t, "removed")
	resp, done := rawRequest(t, removed, http.MethodPost, "https://"+addr+PathStream, strings.NewReader(""))
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	requireCode(t, readControlResponse(resp, nil), deyerr.N008)
	done()

	hub.mu.Lock()
	hub.authErr = deyerr.New(deyerr.X000, nil)
	hub.mu.Unlock()
	cfg, _ := p.nodeConfig(t, "de-1")
	resp, done = rawRequest(t, cfg, http.MethodGet, "https://"+addr+PathAssetsPrefix+"amd64", nil)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	done()

	// The client of another CA cannot even complete the handshake.
	other := newPKI(t)
	otherCfg, _ := other.nodeConfig(t, "de-1")
	c = &ControlClient{HubAddr: addr, TLSConfig: otherCfg}
	requireCode(t, c.Upload(context.Background(), "u1", strings.NewReader("x")), deyerr.N009)
}

func TestProtocolErrorsWithCert(t *testing.T) {
	p := newPKI(t)
	addr, _ := startServer(t, p, newFakeHub(p), "")
	cfg, _ := p.nodeConfig(t, "de-1")

	resp, done := rawRequest(t, cfg, http.MethodGet, "https://"+addr+"/v1/unknown", nil)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	requireCode(t, readControlResponse(resp, nil), deyerr.N015)
	done()

	resp, done = rawRequest(t, cfg, http.MethodGet, "https://"+addr+PathStream, nil)
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	done()

	resp, done = rawRequest(t, cfg, http.MethodPost, "https://"+addr+PathUploadPrefix+"bad%20id", strings.NewReader("x"))
	requireCode(t, readControlResponse(resp, nil), deyerr.N015)
	done()

	resp, done = rawRequest(t, cfg, http.MethodGet, "https://"+addr+PathAssetsPrefix+"../x", nil)
	require.NotEqual(t, http.StatusOK, resp.StatusCode)
	done()

	// A stream that does not start with hello is closed.
	resp, done = rawRequest(t, cfg, http.MethodPost, "https://"+addr+PathStream, strings.NewReader(`{"type":"heartbeat"}`+"\n"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	rest, _ := io.ReadAll(resp.Body)
	require.Empty(t, bytes.TrimSpace(rest))
	done()

	// Not a JSON join request.
	resp, done = rawRequest(t, cfg, http.MethodPost, "https://"+addr+PathJoin, strings.NewReader("{"))
	requireCode(t, readControlResponse(resp, nil), deyerr.N015)
	done()
}

func TestCommandRoundTrip(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	var connected, disconnected atomic.Int32
	startClient(t, cfg, addr, "de-1", func(_ context.Context, cmd Command, _ func([]string)) (any, error) {
		switch cmd.Name {
		case CmdUnitStatus:
			var a UnitArgs
			if err := json.Unmarshal(cmd.Args, &a); err != nil {
				return nil, err
			}
			return UnitStatus{Unit: "deyroute-tun@" + a.Instance + ".service", ActiveState: "active", MainPID: 42}, nil
		case CmdSysinfo:
			return map[string]string{"kernel": "6.1"}, nil
		case CmdUninstall:
			return nil, nil
		case CmdUnitStart:
			return nil, deyerr.New(deyerr.B006, deyerr.Params{"transport": "x"})
		case CmdUnitStop:
			return nil, errors.New("plain")
		case CmdMetrics:
			return func() {}, nil // not marshallable
		case CmdProbeTCP:
			panic("probe exploded")
		}
		return nil, deyerr.New(deyerr.X008, deyerr.Params{"feature": cmd.Name})
	}, func(c *ControlClient) {
		c.OnConnected = func() { connected.Add(1) }
		c.OnDisconnected = func() { disconnected.Add(1) }
	})
	s := waitSession(t, hub)
	require.Equal(t, "de-1", s.NodeID)
	require.Equal(t, "127.0.0.1", s.RemoteIP)
	require.Equal(t, "1.0.0", s.Hello.Version)
	require.NotEmpty(t, s.CertFingerprint)
	cur, ok := (&ControlServer{}).Session("de-1")
	require.False(t, ok)
	require.Nil(t, cur)

	ctx := context.Background()
	var st UnitStatus
	require.NoError(t, s.Call(ctx, CmdUnitStatus, UnitArgs{Instance: "main.de-1.x"}, &st))
	require.Equal(t, "deyroute-tun@main.de-1.x.service", st.Unit)
	require.Equal(t, 42, st.MainPID)

	var info map[string]string
	require.NoError(t, s.Call(ctx, CmdSysinfo, nil, &info))
	require.Equal(t, "6.1", info["kernel"])
	require.NoError(t, s.Call(ctx, CmdUninstall, nil, nil))

	requireCode(t, s.Call(ctx, CmdUnitStart, UnitArgs{}, nil), deyerr.B006)
	e := requireCode(t, s.Call(ctx, CmdUnitStop, UnitArgs{}, nil), deyerr.N011)
	require.Equal(t, "plain", e.Detail)
	require.Contains(t, e.Message(), CmdUnitStop)
	requireCode(t, s.Call(ctx, CmdMetrics, nil, nil), deyerr.X000)
	requireCode(t, s.Call(ctx, CmdProbeTCP, nil, nil), deyerr.X000)
	requireCode(t, s.Call(ctx, CmdSelfUpdate, nil, nil), deyerr.X008)
	// Unmarshallable arguments fail locally.
	requireCode(t, s.Call(ctx, CmdSysinfo, func() {}, nil), deyerr.X000)
	// Wrong result shape.
	var wrong []int
	requireCode(t, s.Call(ctx, CmdSysinfo, nil, &wrong), deyerr.N011)

	rtt, err := s.Ping(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, rtt, time.Duration(0))
	require.Equal(t, int32(1), connected.Load())
	require.Equal(t, int32(0), disconnected.Load())
}

func TestDecodeResultWithoutCode(t *testing.T) {
	s := newSession("de-1", "1.2.3.4", "", Hello{}, nil, nil, 0)
	e := requireCode(t, s.decodeResult("unit.start", &Result{ID: "c1", Error: &ErrorDTO{Message: "exit status 1"}}, nil), deyerr.N011)
	require.Equal(t, "exit status 1", e.Detail)
	require.Contains(t, e.Message(), "de-1")
	requireCode(t, s.decodeResult("unit.start", &Result{ID: "c1"}, nil), deyerr.N011)
	require.NoError(t, s.decodeResult("x", &Result{ID: "c1", OK: true, Data: json.RawMessage("null")}, &e))
}

func TestCallTimeoutAndCancel(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	nodeSaw := make(chan error, 4)
	startClient(t, cfg, addr, "de-1", func(ctx context.Context, cmd Command, _ func([]string)) (any, error) {
		<-ctx.Done()
		nodeSaw <- ctx.Err()
		return nil, ctx.Err()
	}, nil)
	s := waitSession(t, hub)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	e := requireCode(t, s.Call(ctx, CmdProbeTCP, ProbeArgs{Target: "1.2.3.4:443"}, nil), deyerr.N005)
	require.Contains(t, e.Message(), "probe.tcp")
	require.Contains(t, e.Message(), "de-1")
	select {
	case err := <-nodeSaw:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("node command not cancelled after timeout")
	}

	// Default timeout (no deadline) is the session's call timeout.
	s.callTimeout = 100 * time.Millisecond
	requireCode(t, s.Call(context.Background(), CmdSysinfo, nil, nil), deyerr.N005)
	<-nodeSaw
	s.callTimeout = DefaultCommandTimeout

	cctx, ccancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		ccancel()
	}()
	requireCode(t, s.Call(cctx, CmdSysinfo, nil, nil), deyerr.N014)
	select {
	case err := <-nodeSaw:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("node command not cancelled")
	}
}

func TestLogStreaming(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	followStopped := make(chan struct{})
	startClient(t, cfg, addr, "de-1", func(ctx context.Context, cmd Command, stream func([]string)) (any, error) {
		var a LogsArgs
		_ = json.Unmarshal(cmd.Args, &a)
		if !a.Follow {
			stream([]string{"l1", "l2"})
			stream(nil)
			stream([]string{"l3"})
			return nil, nil
		}
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				close(followStopped)
				return nil, ctx.Err()
			case <-t.C:
				stream([]string{"tick"})
			}
		}
	}, nil)
	s := waitSession(t, hub)

	var lines []string
	require.NoError(t, s.Stream(context.Background(), CmdLogsTail, LogsArgs{Target: "main", Lines: 3}, func(l []string) {
		lines = append(lines, l...)
	}))
	require.Equal(t, []string{"l1", "l2", "l3"}, lines)

	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	err := s.Stream(ctx, CmdLogsTail, LogsArgs{Target: "main", Follow: true}, func(l []string) {
		n += len(l)
		if n >= 3 {
			cancel()
		}
	})
	requireCode(t, err, deyerr.N014)
	select {
	case <-followStopped:
	case <-time.After(5 * time.Second):
		t.Fatal("follow not cancelled on the node")
	}
}

func TestPendingCallQueueBound(t *testing.T) {
	pc := &pendingCall{notify: make(chan struct{}, 1)}
	for i := 0; i < maxQueuedChunks+2; i++ {
		pc.enqueue([]string{"x"})
	}
	var got []string
	pc.drain(func(l []string) { got = append(got, l...) })
	require.Len(t, got, maxQueuedChunks+1)
	require.Contains(t, got[0], "2 log lines dropped")
	pc.drain(nil)
}

func TestUploadAndAssets(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	payload := []byte(strings.Repeat("deyroute-binary", 1000))
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])
	hub.asset = func(arch string) (AssetInfo, error) {
		switch arch {
		case "amd64":
			return AssetInfo{Reader: io.NopCloser(bytes.NewReader(payload)), Size: int64(len(payload)), SHA256: strings.ToUpper(good), Version: "1.2.0"}, nil
		case "arm64":
			return AssetInfo{Reader: io.NopCloser(bytes.NewReader(payload)), SHA256: strings.Repeat("0", 64)}, nil
		case "riscv64":
			return AssetInfo{Reader: io.NopCloser(bytes.NewReader(payload))}, nil
		case "short":
			return AssetInfo{Reader: io.NopCloser(bytes.NewReader(payload[:10])), Size: int64(len(payload)), SHA256: good}, nil
		case "nil":
			return AssetInfo{}, nil
		case "boom":
			panic("asset exploded")
		}
		return AssetInfo{}, deyerr.New(deyerr.I002, deyerr.Params{"arch": arch})
	}
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	c := &ControlClient{HubAddr: addr, TLSConfig: cfg, Hello: func() Hello { return Hello{NodeID: "de-1"} }}
	ctx := context.Background()

	require.NoError(t, c.Upload(ctx, "up-1", strings.NewReader("payload")))
	hub.mu.Lock()
	require.Equal(t, "de-1:payload", hub.uploads["up-1"])
	hub.upErr = deyerr.New(deyerr.S001, deyerr.Params{"file": "x"})
	hub.mu.Unlock()
	requireCode(t, c.Upload(ctx, "up-2", strings.NewReader("payload")), deyerr.S001)
	requireCode(t, c.Upload(ctx, "bad/id", strings.NewReader("")), deyerr.N015)

	var buf bytes.Buffer
	meta, err := c.FetchAsset(ctx, "amd64", &buf)
	require.NoError(t, err)
	require.Equal(t, payload, buf.Bytes())
	require.Equal(t, good, meta.SHA256)
	require.Equal(t, "1.2.0", meta.Version)
	require.Equal(t, int64(len(payload)), meta.Size)

	buf.Reset()
	_, err = c.FetchAsset(ctx, "arm64", &buf)
	e := requireCode(t, err, deyerr.S001)
	require.Contains(t, e.Detail, good)

	_, err = c.FetchAsset(ctx, "riscv64", io.Discard)
	requireCode(t, err, deyerr.S001)
	_, err = c.FetchAsset(ctx, "short", io.Discard)
	require.Error(t, err)
	_, err = c.FetchAsset(ctx, "mips", io.Discard)
	requireCode(t, err, deyerr.I002)
	_, err = c.FetchAsset(ctx, "nil", io.Discard)
	requireCode(t, err, deyerr.X000)
	_, err = c.FetchAsset(ctx, "boom", io.Discard)
	requireCode(t, err, deyerr.X000)

	requireCode(t, (&ControlClient{HubAddr: addr}).Upload(ctx, "x", nil), deyerr.X000)
}

func TestReconnectAfterServerRestart(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, stop := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	var connected, disconnected atomic.Int32
	startClient(t, cfg, addr, "de-1", func(context.Context, Command, func([]string)) (any, error) {
		return "pong", nil
	}, func(c *ControlClient) {
		c.OnConnected = func() { connected.Add(1) }
		c.OnDisconnected = func() { disconnected.Add(1) }
	})
	s1 := waitSession(t, hub)
	stop()
	select {
	case <-s1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session not closed on shutdown")
	}
	require.Eventually(t, func() bool { return disconnected.Load() >= 1 }, 5*time.Second, 10*time.Millisecond)

	startServer(t, p, hub, addr)
	s2 := waitSession(t, hub)
	var out string
	require.NoError(t, s2.Call(context.Background(), CmdSysinfo, nil, &out))
	require.Equal(t, "pong", out)
	require.GreaterOrEqual(t, connected.Load(), int32(2))
}

func TestHeartbeatDelivery(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	startClient(t, cfg, addr, "de-1", nil, nil)
	s := waitSession(t, hub)
	for i := 0; i < 3; i++ {
		select {
		case hb := <-s.Heartbeats():
			require.Equal(t, 12.5, hb.CPUPercent)
			require.Equal(t, "active", hb.Units["u"])
			require.False(t, hb.At.IsZero())
		case <-time.After(5 * time.Second):
			t.Fatal("no heartbeat")
		}
	}
	// Latest wins: a burst leaves exactly one heartbeat queued.
	s.handle(NodeMessage{Type: MsgHeartbeat, Heartbeat: &Heartbeat{CPUPercent: 1}})
	s.handle(NodeMessage{Type: MsgHeartbeat, Heartbeat: &Heartbeat{CPUPercent: 2}})
	require.LessOrEqual(t, len(s.Heartbeats()), 1)
	// Nil-client handler answers X008.
	requireCode(t, s.Call(context.Background(), CmdSysinfo, nil, nil), deyerr.X008)
}

func TestDuplicateSessionReplaced(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	srvAddr := ""
	addr, _ := startServer(t, p, hub, srvAddr)
	cfg, _ := p.nodeConfig(t, "de-1")

	// First stream, opened by hand so it does not reconnect.
	tr := newTransport(cfg, &dialErrBox{})
	defer tr.CloseIdleConnections()
	pr, pw := io.Pipe()
	defer pr.Close()
	req, err := http.NewRequest(http.MethodPost, "https://"+addr+PathStream, pr)
	require.NoError(t, err)
	go func() {
		_ = writeJSONLine(pw, NodeMessage{Type: MsgHello, Hello: &Hello{NodeID: "de-1", Version: "0.9"}})
	}()
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	s1 := waitSession(t, hub)

	startClient(t, cfg, addr, "de-1", nil, nil)
	s2 := waitSession(t, hub)
	select {
	case <-s1.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("old session not replaced")
	}
	requireCode(t, s1.Call(context.Background(), CmdSysinfo, nil, nil), deyerr.N003)
	_, err = io.ReadAll(resp.Body)
	_ = err // the old stream ends (EOF or reset)
	_ = pw.Close()
	require.Equal(t, "0.9", s1.Hello.Version)
	require.Equal(t, "1.0.0", s2.Hello.Version)
	select {
	case <-s2.Done():
		t.Fatal("new session must stay open")
	default:
	}
}

func TestIncompatibleHubHello(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	gotHello := make(chan Hello, 1)
	startClient(t, cfg, addr, "de-1", func(context.Context, Command, func([]string)) (any, error) {
		return "ran", nil
	}, func(c *ControlClient) {
		c.OnHubHello = func(h Hello) { gotHello <- h }
	})
	s := waitSession(t, hub)
	require.NoError(t, s.SendHello(Hello{Version: "2.0.0", Compatible: false}))
	h := <-gotHello
	require.Equal(t, "2.0.0", h.Version)
	e := requireCode(t, s.Call(context.Background(), CmdUnitStart, nil, nil), deyerr.N004)
	require.Contains(t, e.Message(), "2.0.0")
	var out string
	require.NoError(t, s.Call(context.Background(), CmdSelfUpdate, SelfUpdateArgs{Version: "2.0.0"}, &out))
	require.Equal(t, "ran", out)
}

func TestSessionClosedDuringCall(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	started := make(chan struct{}, 1)
	startClient(t, cfg, addr, "de-1", func(ctx context.Context, _ Command, _ func([]string)) (any, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}, nil)
	s := waitSession(t, hub)
	go func() {
		<-started
		s.Close()
	}()
	requireCode(t, s.Call(context.Background(), CmdSysinfo, nil, nil), deyerr.N003)
	requireCode(t, s.Stream(context.Background(), CmdLogsTail, nil, nil), deyerr.N003)
	_, err := s.Ping(context.Background())
	requireCode(t, err, deyerr.N003)
	requireCode(t, s.SendHello(Hello{}), deyerr.N003)
	// The client reconnects on its own.
	s2 := waitSession(t, hub)
	require.NotSame(t, s, s2)
}

func TestServeMisconfigured(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	requireCode(t, (&ControlServer{}).Serve(context.Background(), ln), deyerr.X000)
	requireCode(t, (&ControlClient{}).Run(context.Background()), deyerr.X000)
}

func TestServeListenerFailure(t *testing.T) {
	p := newPKI(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, ln.Close())
	err = (&ControlServer{TLSConfig: p.serverCfg, Handler: newFakeHub(p)}).Serve(context.Background(), ln)
	requireCode(t, err, deyerr.X000)
}

func TestHandshakeTimeout(t *testing.T) {
	p := newPKI(t)
	addr, _ := startServer(t, p, newFakeHub(p), "")
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	// Send nothing: the server gives up after HandshakeTimeout (2 s here).
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err)
	var ne net.Error
	if errors.As(err, &ne) {
		require.False(t, ne.Timeout(), "server should close the connection before the client deadline")
	}
}

func TestJitter(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := jitter(10*time.Second, time.Second)
		require.GreaterOrEqual(t, d, 8*time.Second)
		require.LessOrEqual(t, d, 10*time.Second)
	}
	require.Equal(t, time.Second, jitter(time.Second, time.Second))
}

func TestLineReader(t *testing.T) {
	lr := newLineReader(strings.NewReader("\n  \n{\"a\":1}\n{\"b\":2}"), 64)
	l, err := lr.next()
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(l))
	l, err = lr.next()
	require.NoError(t, err)
	require.Equal(t, `{"b":2}`, string(l))
	_, err = lr.next()
	require.ErrorIs(t, err, io.EOF)

	lr = newLineReader(strings.NewReader(strings.Repeat("x", 100)+"\n"), 64)
	_, err = lr.next()
	require.ErrorIs(t, err, errLineTooLong)
}

func TestControlStatus(t *testing.T) {
	require.Equal(t, http.StatusForbidden, controlStatus(deyerr.New(deyerr.N001, nil)))
	require.Equal(t, http.StatusTooManyRequests, controlStatus(deyerr.New(deyerr.N007, nil)))
	require.Equal(t, http.StatusConflict, controlStatus(deyerr.New(deyerr.N010, nil)))
	require.Equal(t, http.StatusInternalServerError, controlStatus(errors.New("x")))
	require.Equal(t, http.StatusBadRequest, controlStatus(deyerr.New(deyerr.C001, nil)))
	require.Equal(t, http.StatusUnauthorized, controlStatus(deyerr.New(deyerr.N013, nil)))
}
