package hub

import (
	stderrors "errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Front mode on the hub (hub.front): a second listener that CDN edges
// reach, whose upgraded connections are fed into the same ControlServer as
// the direct control port, so a node that arrives through the CDN and one
// that arrives directly share every session, command and event.
//
// The front is optional and never fatal: when it cannot start (port taken,
// bad certificate, unreadable secret) the hub logs DEY-X053 once and keeps
// serving the direct control port; the next `config apply` (or a restart)
// tries again. There is no retry loop.

// DefaultRouteStable is how long a front node must stay connected directly
// before its route goes back to direct (Options.RouteStable).
const DefaultRouteStable = 30 * time.Second

// FirstFrontVersion is the first release that knows front mode. An older
// binary refuses the front keys of config.yaml, so rollback and downgrades
// below it are refused while the front is in use (DEY-S010).
// It is the first edge build with front mode (see FirstTrafficVersion).
const FirstFrontVersion = "0.3.0-edge.15"

// frontSecretMax bounds the front secret file that is read.
const frontSecretMax = 1024

// frontFeed is the net.Listener that hands the front's connections to the
// FanIn of the control server. It lives as long as the hub, while the
// front.Server behind it is replaced on a front change.
type frontFeed struct {
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newFrontFeed() *frontFeed {
	return &frontFeed{ch: make(chan net.Conn), done: make(chan struct{})}
}

// Accept implements net.Listener.
func (f *frontFeed) Accept() (net.Conn, error) {
	select {
	case c := <-f.ch:
		return c, nil
	case <-f.done:
		return nil, net.ErrClosed
	}
}

// Close implements net.Listener.
func (f *frontFeed) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

// Addr implements net.Listener.
func (f *frontFeed) Addr() net.Addr { return &net.TCPAddr{} }

// directListener is the direct control listener inside the FanIn: the merge
// would carry on with the front alone when the direct port died, so a
// permanent accept failure is reported to the hub, which stops (as it did
// before the front existed). Temporary failures (descriptor exhaustion, an
// aborted connection) are retried by the merge and not reported.
type directListener struct {
	net.Listener
	fail func(error)
}

// Accept implements net.Listener.
func (d directListener) Accept() (net.Conn, error) {
	c, err := d.Listener.Accept()
	if err != nil && !stderrors.Is(err, net.ErrClosed) && !temporaryAccept(err) {
		d.fail(err)
	}
	return c, err
}

// temporaryAccept reports whether err is a failure of Accept worth retrying.
func temporaryAccept(err error) bool {
	if stderrors.Is(err, syscall.EMFILE) || stderrors.Is(err, syscall.ENFILE) || stderrors.Is(err, syscall.ENOBUFS) ||
		stderrors.Is(err, syscall.ENOMEM) || stderrors.Is(err, syscall.ECONNABORTED) {
		return true
	}
	var ne net.Error
	if stderrors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var t interface{ Temporary() bool }
	return stderrors.As(err, &t) && t.Temporary()
}

// frontKey is the part of hub.front that decides how the listener is built.
// A change makes the next reload rebuild it; domain and cf_only do not
// (they only concern the join link and the firewall).
type frontKey struct {
	Port       int
	TLS        string
	CertFile   string
	KeyFile    string
	SecretFile string
	Proxies    string
}

func keyOf(f config.HubFront) frontKey {
	return frontKey{
		Port: f.Port, TLS: f.TLSMode(), CertFile: f.CertFile, KeyFile: f.KeyFile,
		SecretFile: f.SecretFileOrDefault(), Proxies: strings.Join(f.TrustedProxies, ","),
	}
}

// frontRuntime is the live state of the front listener.
type frontRuntime struct {
	feed *frontFeed

	// reloadMu serialises start/stop/reload.
	reloadMu sync.Mutex

	mu      sync.Mutex
	srv     *front.Server
	stop    chan struct{} // closed to end the pump of srv
	pump    chan struct{} // closed when the pump of srv ended
	addr    net.Addr      // bound address; nil when not listening
	key     frontKey      // what srv was built for
	secret  string        // path secret (never logged); kept across restarts of the listener
	failure error         // why the last start failed or the listener died; nil when listening or disabled
}

// FrontAddr returns the address the front listener is bound to, nil when
// the front is disabled or could not start.
func (h *Hub) FrontAddr() net.Addr {
	h.front.mu.Lock()
	defer h.front.mu.Unlock()
	return h.front.addr
}

// frontBoundPort returns the port the front listener is bound to (0 = none).
func (h *Hub) frontBoundPort() int {
	if tcp, ok := h.FrontAddr().(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}

// frontListen returns the front listen address.
func (o Options) frontListen(port int) string {
	if o.FrontListen != "" {
		return o.FrontListen
	}
	return ":" + strconv.Itoa(port)
}

// frontSecretPath is where the front path secret of cfg is stored.
func (h *Hub) frontSecretPath(f config.HubFront) string {
	return filepath.Join(h.path(config.SecretsDir), f.SecretFileOrDefault())
}

// frontSecret returns the path secret of cfg's front, creating it when the
// file does not exist: 43 base64url characters (secrets.NewToken), written
// 0600 root:deyroute. The value is registered with the log redactor before it
// is returned, so nothing that follows can print it.
func (h *Hub) frontSecret(f config.HubFront) (string, error) {
	path := h.frontSecretPath(f)
	data, err := readLimited(path, frontSecretMax)
	switch {
	case err == nil:
		secret := strings.TrimSpace(string(data))
		dlog.RegisterSecret(secret)
		if !front.ValidSecret(secret) {
			return "", deyerr.Wrap(deyerr.S009, stderrors.New("the front secret must be 1 to 128 characters of A-Z a-z 0-9 - _"),
				deyerr.Params{"path": path, "reason": "not a valid front secret"})
		}
		return secret, nil
	case !stderrors.Is(err, os.ErrNotExist):
		return "", deyerr.Wrap(deyerr.S009, err, deyerr.Params{"path": path, "reason": err.Error()})
	}
	secret, err := secrets.NewToken() // registers the value
	if err != nil {
		return "", err
	}
	if err := tlsutil.WriteSecret(path, []byte(secret+"\n")); err != nil {
		return "", err
	}
	h.chownSecret(path)
	h.log.Info("front secret created", slog.String("file", f.SecretFileOrDefault()))
	return secret, nil
}

// chownSecret gives the secret file the group deyroute (best effort: the mode
// stays 0600, so the group gains nothing, and an unprivileged test hub has
// no such group).
func (h *Hub) chownSecret(path string) {
	_, gid, err := h.o.DeyrouteIDs()
	if err != nil {
		return
	}
	if err := h.o.Chown(path, -1, gid); err != nil {
		h.log.Debug("cannot set the group of the front secret file", dlog.Err(err))
	}
}

// readLimited reads at most max bytes of a regular file.
func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- fixed name below the secrets directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > max {
		return nil, stderrors.New("not a regular file of reasonable size")
	}
	return io.ReadAll(io.LimitReader(f, max))
}

// persistFrontSecretName records hub.front.secret_file in config.yaml once
// the secret exists. A refusal (DEY-C026: an edit waits for `config apply`)
// is not fatal: the file has the default name, so the next start finds the
// same secret.
func (h *Hub) persistFrontSecretName() {
	name := config.DefaultFrontSecretFile
	_, err := h.mutate(func(c *config.Config) error {
		if c.Hub.Front.SecretFile != "" {
			return errNoChange
		}
		c.Hub.Front.SecretFile = name
		return nil
	})
	switch {
	case err == nil, stderrors.Is(err, errNoChange):
	default:
		h.log.Warn("hub.front.secret_file could not be written to config.yaml; the front keeps running with its secret",
			slog.String("file", name), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
	}
}

// startFront builds the front listener for cfg (called with reloadMu held,
// the front stopped). Every failure is logged as DEY-X053 and leaves the
// hub running without the front.
func (h *Hub) startFront(cfg *config.Config) {
	f := cfg.Hub.Front
	rt := &h.front
	addr := h.o.frontListen(f.Port)
	fail := func(err error) {
		e := deyerr.Wrap(deyerr.X053, err, deyerr.Params{"addr": addr, "reason": err.Error()})
		h.log.Error("the front listener cannot start; the hub keeps running without it",
			slog.String("addr", addr), dlog.Err(e), dlog.Code(deyerr.X053))
		rt.mu.Lock()
		rt.failure, rt.key = e, keyOf(f)
		rt.mu.Unlock()
	}
	secret, err := h.frontSecret(f)
	if err != nil {
		fail(err)
		return
	}
	// Known from here on, so the join link works even while the port is busy.
	rt.mu.Lock()
	rt.secret = secret
	rt.mu.Unlock()
	var proxies []netip.Prefix
	for _, c := range f.TrustedProxies {
		if p, err := netip.ParsePrefix(c); err == nil {
			proxies = append(proxies, p)
		}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fail(err)
		return
	}
	data := front.NewDataHandler(front.DataHandlerConfig{
		TLS:       h.tlsCfg,
		Authorize: h.frontAuthorize,
		Logger:    h.log,
	})
	srv, err := front.NewServer(ln, front.ServerOptions{
		Data:           data.Serve,
		Secret:         secret,
		TLSMode:        f.TLSMode(),
		CertFile:       h.pathIf(f.CertFile),
		KeyFile:        h.pathIf(f.KeyFile),
		TrustedProxies: proxies,
		Logger:         h.log,
	})
	if err != nil {
		_ = ln.Close()
		fail(err)
		return
	}
	stop, pump := make(chan struct{}), make(chan struct{})
	rt.mu.Lock()
	rt.srv, rt.stop, rt.pump = srv, stop, pump
	rt.addr, rt.key, rt.failure = ln.Addr(), keyOf(f), nil
	rt.mu.Unlock()
	go h.pumpFront(srv, stop, pump, addr)
	h.log.Info("front listener started", slog.String("addr", ln.Addr().String()),
		slog.String("domain", f.Domain), slog.String("tls", f.TLSMode()))
	if f.SecretFile == "" {
		h.persistFrontSecretName()
	}
}

// pathIf returns Root/p, or "" for an empty p.
func (h *Hub) pathIf(p string) string {
	if p == "" {
		return ""
	}
	return h.path(p)
}

// pumpFront moves the connections srv accepted into the feed of the control
// server until srv is closed. A listener that died by itself is reported
// once as DEY-X053.
func (h *Hub) pumpFront(srv *front.Server, stop <-chan struct{}, done chan<- struct{}, addr string) {
	defer close(done)
	rt := &h.front
	for {
		c, err := srv.Accept()
		if err != nil {
			if !stderrors.Is(err, net.ErrClosed) {
				h.frontDied(srv, addr, err)
			}
			return
		}
		select {
		case rt.feed.ch <- c:
		case <-stop:
			_ = c.Close()
			return
		case <-rt.feed.done:
			_ = c.Close()
			return
		}
	}
}

// frontDied records that the listener of srv stopped on its own.
func (h *Hub) frontDied(srv *front.Server, addr string, err error) {
	rt := &h.front
	rt.mu.Lock()
	if rt.srv != srv {
		rt.mu.Unlock()
		return
	}
	e := deyerr.Wrap(deyerr.X053, err, deyerr.Params{"addr": addr, "reason": err.Error()})
	rt.failure, rt.addr = e, nil
	rt.mu.Unlock()
	h.log.Error("the front listener stopped; the hub keeps running without it",
		slog.String("addr", addr), dlog.Err(e), dlog.Code(deyerr.X053))
	h.requestFirewall()
}

// stopFront closes the running front listener and waits for its pump
// (reloadMu held). Upgraded connections that the control server already
// accepted belong to it and stay open until their sessions end.
func (h *Hub) stopFront() {
	rt := &h.front
	rt.mu.Lock()
	srv, stop, pump := rt.srv, rt.stop, rt.pump
	rt.srv, rt.stop, rt.pump, rt.addr = nil, nil, nil, nil
	rt.mu.Unlock()
	if srv == nil {
		return
	}
	close(stop)
	_ = srv.Close()
	<-pump
}

// closeFront stops the front for good (hub shutdown).
func (h *Hub) closeFront() {
	h.front.reloadMu.Lock()
	defer h.front.reloadMu.Unlock()
	h.stopFront()
	_ = h.front.feed.Close()
}

// reloadFront makes the listener match cfg: it starts a front that is
// enabled and not running (also a retry after DEY-X053), rebuilds one whose
// listener settings changed and stops one that was disabled. Any change
// asks for a firewall apply.
func (h *Hub) reloadFront(cfg *config.Config) {
	rt := &h.front
	rt.reloadMu.Lock()
	defer rt.reloadMu.Unlock()
	f := cfg.Hub.Front
	rt.mu.Lock()
	exists, running, key := rt.srv != nil, rt.srv != nil && rt.addr != nil, rt.key
	rt.mu.Unlock()
	switch {
	case !f.Enabled:
		if exists {
			h.stopFront()
			h.log.Info("front listener stopped (hub.front is disabled)")
		}
		rt.mu.Lock()
		rt.failure = nil
		rt.mu.Unlock()
	case running && key == keyOf(f):
		return
	default:
		h.stopFront()
		h.startFront(cfg)
	}
	h.requestFirewall()
}

// frontStatus is the front part of api.HubStatus (never the secret); nil
// when the front was never configured.
func (h *Hub) frontStatus(cfg *config.Config) *api.FrontStatus {
	f := cfg.Hub.Front
	if !f.Enabled && f.Domain == "" && f.Port == 0 {
		return nil
	}
	return &api.FrontStatus{
		Enabled:   f.Enabled,
		Domain:    f.Domain,
		Port:      f.Port,
		Listening: h.FrontAddr() != nil,
		CFOnly:    f.CFOnlyOrDefault(),
		TLS:       f.TLSMode(),
	}
}

// frontHubAddr returns "<front domain>:<port>", the address a front session
// is told to use as the hub address.
func frontHubAddr(f config.HubFront) string {
	return net.JoinHostPort(f.Domain, strconv.Itoa(f.Port))
}

// isFrontPeer reports whether p arrived through the front listener.
func isFrontPeer(p api.Peer) bool { return p.Via == front.ViaFront }

// frontLink returns the front fields of the join link when the front is on
// and has a secret.
func (h *Hub) frontLink(cfg *config.Config) (host string, port int, secret string, ok bool) {
	f := cfg.Hub.Front
	if !f.Enabled {
		return "", 0, "", false
	}
	h.front.mu.Lock()
	secret = h.front.secret
	h.front.mu.Unlock()
	if secret == "" {
		return "", 0, "", false
	}
	return f.Domain, f.Port, secret, true
}

// setRoute records how node id reaches the hub (nodes[].route): route is
// written only when it becomes "front" and removed when the node is direct
// again (the value "direct" is never written, so an existing config.yaml
// does not change). It reports whether config.yaml changed. A refusal
// (DEY-C026: an unapplied edit) is returned for the caller to log; the next
// attach tries again.
func (h *Hub) setRoute(id, route string) (bool, error) {
	_, err := h.mutate(func(c *config.Config) error {
		n, ok := c.NodeByID(id)
		if !ok {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": id})
		}
		if n.Route == route {
			return errNoChange
		}
		n.Route = route
		return nil
	})
	if stderrors.Is(err, errNoChange) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	h.requestFirewall()
	return true, nil
}

// routeFront marks node id as a front node before its tunnels are planned.
func (h *Hub) routeFront(id string) {
	if n, ok := h.Config().NodeByID(id); !ok || n.Route == config.RouteFront {
		return
	}
	changed, err := h.setRoute(id, config.RouteFront)
	if err != nil {
		h.log.Warn("cannot record that the node connects through the front; it is tried again at its next connect",
			dlog.Node(id), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	if changed {
		h.log.Info("node connects through the front", dlog.Node(id))
	}
}

// routeDirect puts a front node back to direct after its direct session
// was stable for RouteStable (decided once per session: a refusal is logged
// and the next direct attach tries again). The tunnels are planned again.
func (h *Hub) routeDirect(id string) {
	n, ok := h.Config().NodeByID(id)
	if !ok || n.Route != config.RouteFront || n.PublicIP == "" {
		return
	}
	changed, err := h.setRoute(id, "")
	if err != nil {
		h.log.Warn("cannot record that the node connects directly again",
			dlog.Node(id), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	if changed {
		h.log.Info("node connects directly again; its route is direct", dlog.Node(id))
		h.nodeAttached(id)
	}
}

// trustedRemoteIP returns the address of a session when it is the node's
// real one (direct TCP, or a front session whose peer vouched for it).
func trustedRemoteIP(s *api.Session) string {
	if s == nil || (s.Via != "" && !s.Trusted) {
		return ""
	}
	return s.RemoteIP
}

// frontNodes returns the ids of the nodes whose route is front.
func frontNodes(cfg *config.Config) []string {
	var out []string
	for _, n := range cfg.Nodes {
		if n.Route == config.RouteFront {
			out = append(out, n.ID)
		}
	}
	slices.Sort(out)
	return out
}

// frontInUse reports whether the front is enabled or any node uses it.
func frontInUse(cfg *config.Config) bool {
	return cfg.Hub != nil && cfg.Hub.Front.Enabled || len(frontNodes(cfg)) > 0
}

// errNoDataPort refuses a data connection to a port that is not the
// control port of a front node's rung.
var errNoDataPort = stderrors.New("not the control port of a rung of this node")

// frontAuthorize implements front.DataAuthorizer: port must be the control
// port of a rung of node ('<tunnel>/<node>/<backend>/<transport>') or the
// canary control port of a tunnel whose primary node is node, and node must
// be a front node. Companion and loopback ports, the hub's own ports and
// anything else are refused. It returns the tunnel token.
func (h *Hub) frontAuthorize(port int, node string) (string, error) {
	ports, err := h.st.CtlPorts()
	if err != nil {
		return "", err
	}
	cfg := h.Config()
	if n, ok := cfg.NodeByID(node); !ok || n.Route != config.RouteFront {
		return "", errNoDataPort
	}
	for key, p := range ports {
		if p != port {
			continue
		}
		tunnel, owner, ok := dataKeyOwner(key)
		if !ok {
			return "", errNoDataPort
		}
		t, ok := cfg.Tunnel(tunnel)
		if !ok || len(t.Nodes) == 0 || !slices.Contains(t.Nodes, node) {
			return "", errNoDataPort
		}
		if (owner == "" && t.Nodes[0] != node) || (owner != "" && owner != node) {
			return "", errNoDataPort
		}
		return h.secretStore().Token(tunnel)
	}
	return "", errNoDataPort
}

// dataKeyOwner returns the tunnel and node of a control-port key a data
// connection may use: a rung's key, or the canary control key (node "",
// meaning the tunnel's primary node). Companion ("/udp") and canary
// loopback keys are not data ports.
func dataKeyOwner(key string) (tunnel, node string, ok bool) {
	if t, found := strings.CutSuffix(key, "/canary/ctl"); found && config.ValidID(t) {
		return t, "", true
	}
	tunnel, node, transport, ok := state.SplitKey(key)
	if !ok || node == "canary" || strings.HasSuffix(transport, "/udp") || strings.Count(transport, "/") != 1 {
		return "", "", false
	}
	return tunnel, node, true
}
