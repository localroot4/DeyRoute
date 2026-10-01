package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Runner runs an allow-listed program; internal/exec's runner satisfies it
// (`ip` is on the allow-list for WireGuard, spec section 15). It is an
// alias of an unnamed interface so that callers can use PostStart/PostStop
// through a structural interface without importing this package.
type Runner = interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

// Timeouts of Up/Down.
const (
	// DefaultTimeout bounds Up and Down when ctx has no deadline.
	DefaultTimeout = 30 * time.Second
	// DefaultSocketWait bounds how long Up waits for the amneziawg-go UAPI
	// socket after the unit started (the section 7 probe budget is 15s).
	DefaultSocketWait = 15 * time.Second
	socketPoll        = 100 * time.Millisecond
	// DefaultPortWait bounds how long Up retries the configuration while
	// the listen port is still in use (EADDRINUSE): right before a rung
	// starts, the node agent's UDP reachability echo (probe.udp_listen,
	// 10s) may hold the rung's control port, and a process that just
	// stopped may not have released it yet.
	DefaultPortWait = 15 * time.Second
	portPoll        = 250 * time.Millisecond
)

// Manager creates and removes tunnel interfaces from a wg.json. The zero
// value needs only Runner; the other fields exist for tests.
type Manager struct {
	// Root prefixes /proc/sys and /var/run paths ("" = "/").
	Root string
	// Runner runs `ip`.
	Runner Runner
	// SocketWait overrides DefaultSocketWait.
	SocketWait time.Duration
	// PortWait overrides DefaultPortWait.
	PortWait time.Duration

	openNetlink func() (nlTransport, error)
	dialUAPI    func(ctx context.Context, path string) (net.Conn, error)
}

// nlTransport is a generic netlink socket (netlink_linux.go).
type nlTransport interface {
	Roundtrip(ctx context.Context, req []byte, seq uint32) ([]nlMessage, error)
	Close() error
}

// Up brings the interface described by the wg.json at cfgPath up; it is
// what "deyroute wg up --config <path>" runs (and PostStart for awg).
//
// Kernel mode: an existing interface of that name is deleted first, then
// `ip link add <iface> type wireguard`, private key, listen port and the
// single peer are set over generic netlink (WG_CMD_SET_DEVICE), followed by
// `ip address replace <addr> dev <iface>` and `ip link set dev <iface> mtu
// 1420 up`. Any failure removes the half-built interface again.
//
// Userspace mode (awg): the amneziawg-go unit already created the
// interface; Up waits for its UAPI socket /var/run/amneziawg/<iface>.sock,
// sends the configuration (keys, peer, Jc/Jmin/Jmax/S1/S2/H1-H4) and then
// sets address, MTU and link state with `ip`.
//
// In both modes a listen port that is still in use (EADDRINUSE) is retried
// for up to DefaultPortWait. On the node, route_localnet is enabled on the
// interface when a target is on 127.0.0.0/8.
func Up(ctx context.Context, cfgPath string, r Runner) error {
	return (&Manager{Runner: r}).Up(ctx, cfgPath)
}

// Down removes the interface of the wg.json at cfgPath ("deyroute wg down
// --config <path>"); a missing interface is not an error.
func Down(ctx context.Context, cfgPath string, r Runner) error {
	return (&Manager{Runner: r}).Down(ctx, cfgPath)
}

// LoadConfig reads and validates a wg.json.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path of the rendered config (root-owned directory)
	if err != nil {
		return nil, deyerr.Wrap(deyerr.B072, err, deyerr.Params{"path": path, "reason": err.Error()})
	}
	return ParseConfig(data, path)
}

// Up implements the package-level Up.
func (m *Manager) Up(ctx context.Context, cfgPath string) error {
	c, err := LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	if m.Runner == nil {
		return deyerr.New(deyerr.B070, deyerr.Params{"iface": c.Interface, "reason": "no command runner"})
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if c.Mode == ModeUserspace {
		return m.upUserspace(ctx, c)
	}
	return m.upKernel(ctx, c)
}

// Prepare readies the host before the unit of the wg.json at cfgPath
// starts: in userspace mode it creates the amneziawg-go socket directory
// (/var/run/amneziawg); kernel mode needs nothing. It is idempotent.
func (m *Manager) Prepare(_ context.Context, cfgPath string) error {
	c, err := LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	if c.Mode != ModeUserspace {
		return nil
	}
	if err := m.socketDir(); err != nil {
		return deyerr.Wrap(deyerr.B071, err, deyerr.Params{"iface": c.Interface, "reason": err.Error()})
	}
	return nil
}

// socketDir creates the amneziawg-go UAPI socket directory.
func (m *Manager) socketDir() error {
	dir := filepath.Join(m.root(), awgSocketDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return nil
}

// Down implements the package-level Down.
func (m *Manager) Down(ctx context.Context, cfgPath string) error {
	c, err := LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	code := deyerr.B070
	if c.Mode == ModeUserspace {
		code = deyerr.B071
	}
	if m.Runner == nil {
		return deyerr.New(code, deyerr.Params{"iface": c.Interface, "reason": "no command runner"})
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := m.deleteLink(ctx, c.Interface); err != nil {
		return deyerr.Wrap(code, err, deyerr.Params{"iface": c.Interface, "reason": err.Error()})
	}
	return nil
}

func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultTimeout)
}

// upKernel creates and configures a kernel WireGuard interface.
func (m *Manager) upKernel(ctx context.Context, c *Config) error {
	fail := func(err error) error {
		return deyerr.Wrap(deyerr.B070, err, deyerr.Params{"iface": c.Interface, "reason": err.Error()})
	}
	if err := m.deleteLink(ctx, c.Interface); err != nil {
		return fail(err)
	}
	if err := m.ip(ctx, "link", "add", "dev", c.Interface, "type", "wireguard"); err != nil {
		return fail(fmt.Errorf("cannot create the interface (is the wireguard kernel module available?): %w", err))
	}
	err := m.configureKernel(ctx, c)
	if err == nil {
		err = m.finish(ctx, c)
	}
	if err != nil {
		// Leave nothing behind: a later start recreates the interface.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = m.deleteLink(cctx, c.Interface)
		return fail(err)
	}
	return nil
}

// configureKernel applies keys and peer with WG_CMD_SET_DEVICE.
func (m *Manager) configureKernel(ctx context.Context, c *Config) error {
	d, err := settingsFrom(c)
	if err != nil {
		return err
	}
	open := m.openNetlink
	if open == nil {
		open = openGenetlink
	}
	t, err := open()
	if err != nil {
		return fmt.Errorf("generic netlink: %w", err)
	}
	defer func() { _ = t.Close() }()
	msgs, err := t.Roundtrip(ctx, encodeGetFamily(1, wgGenlName), 1)
	if err != nil {
		return fmt.Errorf("resolve the wireguard netlink family (is the kernel module loaded?): %w", err)
	}
	fam, err := familyID(msgs, 1)
	if err != nil {
		return err
	}
	err = m.whilePortInUse(ctx, func() error {
		_, err := t.Roundtrip(ctx, encodeSetDevice(fam, 2, d), 2)
		return err
	})
	if err != nil {
		return fmt.Errorf("configure keys and peer (WG_CMD_SET_DEVICE): %w", err)
	}
	return nil
}

// whilePortInUse runs set until it succeeds or fails with anything but
// EADDRINUSE, retrying for at most PortWait (DefaultPortWait). A failed set
// applies nothing (kernel) or is replaced completely by the next one
// (replace_peers in the UAPI request), so retrying is safe.
func (m *Manager) whilePortInUse(ctx context.Context, set func() error) error {
	wait := m.PortWait
	if wait <= 0 {
		wait = DefaultPortWait
	}
	deadline := time.Now().Add(wait)
	for {
		err := set()
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) || time.Now().Add(portPoll).After(deadline) {
			return err
		}
		t := time.NewTimer(portPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
	}
}

// settingsFrom converts the validated config into netlink settings.
func settingsFrom(c *Config) (deviceSettings, error) {
	d := deviceSettings{iface: c.Interface, listenPort: c.ListenPort, keepalive: c.Peer.PersistentKeepalive}
	var err error
	if d.privateKey, err = decodeKey(c.PrivateKey); err != nil {
		return d, err
	}
	if d.peerKey, err = decodeKey(c.Peer.PublicKey); err != nil {
		return d, err
	}
	if c.Peer.Endpoint != "" {
		if d.endpoint, err = netip.ParseAddrPort(c.Peer.Endpoint); err != nil {
			return d, err
		}
	}
	for _, a := range c.Peer.AllowedIPs {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return d, err
		}
		d.allowedIPs = append(d.allowedIPs, p.Masked())
	}
	return d, nil
}

// upUserspace configures the running amneziawg-go device.
func (m *Manager) upUserspace(ctx context.Context, c *Config) error {
	fail := func(err error) error {
		return deyerr.Wrap(deyerr.B071, err, deyerr.Params{"iface": c.Interface, "reason": err.Error()})
	}
	req, err := uapiSetRequest(c)
	if err != nil {
		return fail(err)
	}
	// amneziawg-go creates this directory itself, but under the unit's
	// ProtectSystem=strict /run is read-only unless it already exists
	// (PreStart normally created it; this covers a unit started by systemd
	// alone, which then succeeds on its Restart=always retry).
	if err := m.socketDir(); err != nil {
		return fail(err)
	}
	sock := filepath.Join(m.root(), awgSocketDir, c.Interface+".sock")
	err = m.whilePortInUse(ctx, func() error {
		conn, err := m.waitSocket(ctx, sock)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		return uapiExchange(ctx, conn, req)
	})
	if err != nil {
		return fail(err)
	}
	if err := m.finish(ctx, c); err != nil {
		return fail(err)
	}
	return nil
}

// waitSocket dials the UAPI socket, retrying until it appears.
func (m *Manager) waitSocket(ctx context.Context, path string) (net.Conn, error) {
	wait := m.SocketWait
	if wait <= 0 {
		wait = DefaultSocketWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	dial := m.dialUAPI
	if dial == nil {
		dial = func(ctx context.Context, p string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", p)
		}
	}
	t := time.NewTimer(0)
	defer t.Stop()
	var last error
	for {
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return nil, fmt.Errorf("UAPI socket %s not ready after %s (is the awg unit running?): %w", path, wait, last)
		case <-t.C:
		}
		conn, err := dial(ctx, path)
		if err == nil {
			return conn, nil
		}
		last = err
		t.Reset(socketPoll)
	}
}

// finish sets address, MTU and link state, then route_localnet.
func (m *Manager) finish(ctx context.Context, c *Config) error {
	if err := m.ip(ctx, "address", "replace", c.Address, "dev", c.Interface); err != nil {
		return err
	}
	if err := m.ip(ctx, "link", "set", "dev", c.Interface, "mtu", strconv.Itoa(c.MTU), "up"); err != nil {
		return err
	}
	if c.RouteLocalnet {
		return m.routeLocalnet(c.Interface)
	}
	return nil
}

// routeLocalnet writes net.ipv4.conf.<iface>.route_localnet=1.
func (m *Manager) routeLocalnet(iface string) error {
	p := filepath.Join(m.root(), "proc/sys/net/ipv4/conf", iface, "route_localnet")
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0) // #nosec G304 -- iface is validated by ParseConfig
	if err != nil {
		return fmt.Errorf("enable route_localnet: %w", err)
	}
	_, werr := f.WriteString("1\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return fmt.Errorf("enable route_localnet: %w", err)
	}
	return nil
}

// deleteLink removes iface when it exists.
func (m *Manager) deleteLink(ctx context.Context, iface string) error {
	exists, err := m.linkExists(ctx, iface)
	if err != nil || !exists {
		return err
	}
	_, stderr, err := m.Runner.Run(ctx, "ip", []string{"link", "del", "dev", iface}, nil)
	if err != nil && !missingDevice(stderr) {
		return ipErr([]string{"link", "del", "dev", iface}, stderr, err)
	}
	return nil
}

// linkExists asks `ip link show`; "does not exist" means absent.
func (m *Manager) linkExists(ctx context.Context, iface string) (bool, error) {
	args := []string{"-o", "link", "show", "dev", iface}
	_, stderr, err := m.Runner.Run(ctx, "ip", args, nil)
	if err == nil {
		return true, nil
	}
	if missingDevice(stderr) {
		return false, nil
	}
	return false, ipErr(args, stderr, err)
}

func missingDevice(stderr []byte) bool {
	s := string(stderr)
	return strings.Contains(s, "does not exist") || strings.Contains(s, "Cannot find device")
}

// ip runs `ip args...`.
func (m *Manager) ip(ctx context.Context, args ...string) error {
	_, stderr, err := m.Runner.Run(ctx, "ip", args, nil)
	if err != nil {
		return ipErr(args, stderr, err)
	}
	return nil
}

func ipErr(args []string, stderr []byte, err error) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		return fmt.Errorf("ip %s: %w", strings.Join(args, " "), err)
	}
	return fmt.Errorf("ip %s: %s: %w", strings.Join(args, " "), msg, err)
}

func (m *Manager) root() string {
	if m.Root == "" {
		return "/"
	}
	return m.Root
}
