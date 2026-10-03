package setup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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

// frontJoinSecret is a path secret of the shape secrets.NewToken produces.
const frontJoinSecret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"

const frontJoinDomain = "front.example.com"

// frontHub is a join hub behind a fake CDN: testHub signs the CSR, a real
// front.Server accepts the WebSocket and the CDN terminates the outer TLS.
type frontHub struct {
	*testHub
	srv *front.Server
	cdn *fronttest.CDN
}

func startFrontHub(t *testing.T, cdn fronttest.Options) *frontHub {
	t.Helper()
	ca, err := tlsutil.NewCA("ir-1", time.Now())
	require.NoError(t, err)
	certPEM, keyPEM, err := ca.IssueServer("ir-1", []net.IP{net.ParseIP("127.0.0.1")}, nil, 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ServerTLSConfig(ca.CertPEM, certPEM, keyPEM)
	require.NoError(t, err)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv, err := front.NewServer(ln, front.ServerOptions{Secret: frontJoinSecret})
	require.NoError(t, err)
	h := &testHub{ca: ca, addr: ln.Addr().String()}
	cs := &api.ControlServer{TLSConfig: cfg, Handler: h, HandshakeTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cs.Serve(ctx, srv) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		require.NoError(t, <-done)
	})

	cdn.OriginAddr = srv.Addr().String()
	cdn.OriginTLS = true
	cdn.ClientIP = "203.0.113.7"
	c, err := fronttest.NewCDN(cdn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return &frontHub{testHub: h, srv: srv, cdn: c}
}

// dialer reaches the CDN whatever address the target names.
func (h *frontHub) dialer(upgrade time.Duration) setupFrontDial {
	d := &front.Dialer{
		RootCAs: h.cdn.CAPool(), UpgradeTimeout: upgrade,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, "tcp", h.cdn.Addr())
		},
	}
	return d.DialControl
}

type setupFrontDial = FrontDialFunc

// link is a front join link; the CDN port is no Cloudflare port, so the
// scheme is forced with ?tls=1.
func (h *frontHub) link(token, secret string) string {
	tls := true
	return api.FormatJoinLink(api.JoinLink{Token: token, Host: frontJoinDomain, Port: h.cdn.Port(), Fingerprint: h.ca.Fingerprint(), Secret: secret, TLS: &tls})
}

func (h *frontHub) hubAddr() string {
	return net.JoinHostPort(frontJoinDomain, fmt.Sprint(h.cdn.Port()))
}

func frontJoinOpts(root string, h *frontHub, link string, steps *stepLog) JoinOptions {
	o := joinOpts(root, link, steps)
	o.FrontDial = h.dialer(5 * time.Second)
	return o
}

func TestJoinOverTheFront(t *testing.T) {
	hub := startFrontHub(t, fronttest.Options{})
	root := t.TempDir()
	steps := &stepLog{}
	link := hub.link(goodToken, frontJoinSecret)
	res, err := Join(ctxT(t), frontJoinOpts(root, hub, link, steps))
	require.NoError(t, err)

	require.Equal(t, []string{
		"parse_link=ok", "keys=ok", "join=ok", "secrets=ok", "sysctl=skipped", "config=ok", "service=skipped",
	}, steps.final())
	require.True(t, res.Front)
	require.Equal(t, hub.hubAddr(), res.HubAddr, "the hub is the front domain and port")
	require.Equal(t, "203.0.113.7", res.PublicIP, "the hub sees the real client address")
	require.Equal(t, "ir-1", res.HubName)

	// The one-shot POST reached the hub through the CDN, once.
	require.Len(t, hub.requests(), 1)
	require.Equal(t, goodToken, hub.requests()[0].Token)
	st := hub.cdn.Stats()
	require.EqualValues(t, 1, st.Upgrades)

	// Secret file 0600, config with node.front and hub_addr = domain:port.
	secretPath := filepath.Join(root, "etc/deyroute/secrets", config.DefaultFrontSecretFile)
	require.Equal(t, os.FileMode(0o600), mode(t, secretPath))
	b, err := os.ReadFile(secretPath)
	require.NoError(t, err)
	require.Equal(t, frontJoinSecret, strings.TrimSpace(string(b)))
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, hub.hubAddr(), cfg.Node.HubAddr)
	require.Equal(t, config.NodeFront{SecretFile: config.DefaultFrontSecretFile, Scheme: config.FrontSchemeWSS}, cfg.Node.Front)
	require.True(t, cfg.Node.FrontMode())
	raw, err := os.ReadFile(res.ConfigPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), frontJoinSecret, "config.yaml names the file, never the secret")
	require.NotContains(t, string(raw), goodToken)

	// The secret and the token are in no step detail and are masked by the
	// log redactor from the start of the join.
	for _, s := range steps.steps {
		require.NotContains(t, s.Detail+s.Title, frontJoinSecret)
		require.NotContains(t, s.Detail+s.Title, goodToken)
	}
	require.NotContains(t, dlog.Redact("x "+frontJoinSecret), frontJoinSecret)

	// A second join on the same server is refused as before.
	_, err = Join(ctxT(t), frontJoinOpts(root, hub, link, nil))
	requireTop(t, err, deyerr.I013)
}

func TestDirectJoinHasNoFrontBlock(t *testing.T) {
	hub := startTestHub(t)
	root := t.TempDir()
	res, err := Join(ctxT(t), joinOpts(root, hub.link(goodToken, hub.ca.Fingerprint()), nil))
	require.NoError(t, err)
	require.False(t, res.Front)
	cfg, err := config.LoadWith(res.ConfigPath, validateOptions())
	require.NoError(t, err)
	require.Equal(t, config.NodeFront{}, cfg.Node.Front)
	require.False(t, exists(filepath.Join(root, "etc/deyroute/secrets", config.DefaultFrontSecretFile)))
}

func TestJoinFrontFailureKeepsTheTokenUsable(t *testing.T) {
	hub := startFrontHub(t, fronttest.Options{})
	root := t.TempDir()
	secretPath := filepath.Join(root, "etc/deyroute/secrets", config.DefaultFrontSecretFile)
	wrong := "a-completely-wrong-secret-0123456789"

	run := func(link string, dial FrontDialFunc) error {
		o := frontJoinOpts(root, hub, link, nil)
		if dial != nil {
			o.FrontDial = dial
		}
		_, err := Join(ctxT(t), o)
		return err
	}
	requireClean := func(err error, code deyerr.Code) {
		t.Helper()
		e := requireTop(t, err, code)
		for _, s := range []string{frontJoinSecret, wrong, goodToken} {
			require.NotContains(t, err.Error(), s)
			require.NotContains(t, e.Detail, s)
			require.NotContains(t, e.Message(), s)
		}
		require.NoFileExists(t, secretPath, "the secret file is removed again")
		require.NoFileExists(t, filepath.Join(root, config.DefaultPath), "no config after a failed join")
		require.Empty(t, hub.requests(), "the hub never saw the request: the token is not spent")
	}

	// A wrong path: the decoy answers 404 (DEY-N017).
	requireClean(run(hub.link(goodToken, wrong), nil), deyerr.N017)

	// The CDN reports the origin down (521): DEY-N017, the same.
	hub.cdn.Configure(func(o *fronttest.Options) { o.Canned = fronttest.OriginError(521) })
	requireClean(run(hub.link(goodToken, frontJoinSecret), nil), deyerr.N017)

	// The CDN swallows the connection: DEY-N016 after the dial budget.
	hub.cdn.Configure(func(o *fronttest.Options) { o.Canned = nil; o.DropAll = true })
	requireClean(run(hub.link(goodToken, frontJoinSecret), hub.dialer(400*time.Millisecond)), deyerr.N016)

	// The same command works once the front does.
	hub.cdn.Configure(func(o *fronttest.Options) { o.DropAll = false })
	res, err := Join(ctxT(t), frontJoinOpts(root, hub, hub.link(goodToken, frontJoinSecret), nil))
	require.NoError(t, err)
	require.True(t, res.Front)
	require.Len(t, hub.requests(), 1)
	require.FileExists(t, secretPath)
}

func TestJoinFrontLinkWithoutSchemeIsRefusedBeforeAnything(t *testing.T) {
	hub := startFrontHub(t, fronttest.Options{})
	root := t.TempDir()
	// The CDN port is no Cloudflare port and the link has no ?tls=: the
	// scheme cannot be derived.
	link := api.FormatJoinLink(api.JoinLink{Token: goodToken, Host: frontJoinDomain, Port: hub.cdn.Port(), Fingerprint: hub.ca.Fingerprint(), Secret: frontJoinSecret})
	_, err := Join(ctxT(t), frontJoinOpts(root, hub, link, nil))
	e := requireTop(t, err, deyerr.N006)
	require.NotContains(t, err.Error()+e.Detail, frontJoinSecret)
	require.False(t, exists(filepath.Join(root, "etc/deyroute/secrets", config.DefaultFrontSecretFile)))
	require.Empty(t, hub.requests())
}

func TestJoinFrontSecretFileFailureStopsBeforeThePost(t *testing.T) {
	hub := startFrontHub(t, fronttest.Options{})
	root := t.TempDir()
	// A directory where the secret file must go: the write fails, and the
	// token is not sent.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc/deyroute/secrets", config.DefaultFrontSecretFile, "x"), 0o700))
	_, err := Join(ctxT(t), frontJoinOpts(root, hub, hub.link(goodToken, frontJoinSecret), nil))
	require.Error(t, err)
	require.NotContains(t, err.Error(), frontJoinSecret)
	require.Empty(t, hub.requests())
}

// A front dial error keeps its Retry-After and Permanent hints reachable for
// the control client, whatever wraps it.
func TestDialFrontKeepsTheRetryHints(t *testing.T) {
	hub := startFrontHub(t, fronttest.Options{})
	target, err := front.NewTarget(hub.hubAddr(), "wss", "", frontJoinSecret)
	require.NoError(t, err)
	dial := DialFront(target, hub.dialer(3*time.Second))

	hub.cdn.Configure(func(o *fronttest.Options) { o.Canned = fronttest.RateLimited("7") })
	_, err = dial(context.Background())
	requireTop(t, err, deyerr.N017)
	var h interface {
		RetryAfter() time.Duration
		Permanent() bool
	}
	require.True(t, errors.As(fmt.Errorf("wrapped: %w", err), &h))
	require.Equal(t, 7*time.Second, h.RetryAfter())
	require.False(t, h.Permanent())

	hub.cdn.Configure(func(o *fronttest.Options) { o.Canned = fronttest.NotFound() })
	_, err = dial(context.Background())
	require.True(t, errors.As(err, &h))
	require.True(t, h.Permanent(), "a 404 is permanent")

	// A dial that is not a front.DialError is DEY-N016; a catalog error and a
	// cancelled context pass through.
	boom := DialFront(target, func(context.Context, front.Target) (net.Conn, error) { return nil, errors.New("boom") })
	_, err = boom(context.Background())
	requireTop(t, err, deyerr.N016)
	cat := deyerr.New(deyerr.T008, deyerr.Params{"path": "x", "reason": "y"})
	_, err = DialFront(target, func(context.Context, front.Target) (net.Conn, error) { return nil, cat })(context.Background())
	require.Same(t, cat, err)
	_, err = DialFront(target, func(context.Context, front.Target) (net.Conn, error) { return nil, context.Canceled })(context.Background())
	require.ErrorIs(t, err, context.Canceled)
}

// Joins are run one at a time per server: two front joins into two roots
// work side by side through the same CDN (each opens its own WebSocket).
func TestJoinFrontTwoServers(t *testing.T) {
	hub := startFrontHub(t, fronttest.Options{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o := frontJoinOpts(t.TempDir(), hub, hub.link(goodToken, frontJoinSecret), nil)
			o.Name = fmt.Sprintf("de-%d", i+1)
			_, errs[i] = Join(context.Background(), o)
		}()
	}
	wg.Wait()
	require.NoError(t, errors.Join(errs...))
	require.Len(t, hub.requests(), 2)
}
