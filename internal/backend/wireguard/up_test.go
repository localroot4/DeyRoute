package wireguard

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeRunner records `ip` calls and simulates link existence.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []string
	exists map[string]bool
	failOn string // command prefix that fails
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, _ []byte) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if f.failOn != "" && strings.HasPrefix(cmd, f.failOn) {
		return nil, []byte("RTNETLINK answers: Operation not supported"), errExit
	}
	if f.exists == nil {
		f.exists = map[string]bool{}
	}
	switch {
	case len(args) == 5 && args[1] == "link" && args[2] == "show":
		if !f.exists[args[4]] {
			return nil, []byte(`Device "` + args[4] + `" does not exist.`), errExit
		}
	case len(args) == 6 && args[0] == "link" && args[1] == "add":
		f.exists[args[3]] = true
	case len(args) == 4 && args[0] == "link" && args[1] == "del":
		delete(f.exists, args[3])
	}
	return nil, nil, nil
}

// fakeNetlink answers the family lookup and records SET_DEVICE.
type fakeNetlink struct {
	family  uint16
	reqs    [][]byte
	setErr  syscall.Errno
	noFam   bool
	closed  bool
	failGet error
}

func (f *fakeNetlink) Roundtrip(_ context.Context, req []byte, seq uint32) ([]nlMessage, error) {
	f.reqs = append(f.reqs, req)
	if seq == 1 {
		if f.failGet != nil {
			return nil, f.failGet
		}
		if f.noFam {
			return nil, nil
		}
		payload := append([]byte{1, 2, 0, 0}, nlU16(ctrlAttrID, f.family)...)
		payload = append(payload, nlString(ctrlAttrName, wgGenlName)...)
		return []nlMessage{{typ: genlIDCtrl, seq: 1, data: payload}}, nil
	}
	if f.setErr != 0 {
		return nil, f.setErr
	}
	return nil, nil
}

func (f *fakeNetlink) Close() error { f.closed = true; return nil }

func writeConfig(t *testing.T, awg bool, side backend.Side, mutate func(in *backend.RenderInput)) string {
	t.Helper()
	in := mixedFixture(t, awg)
	if mutate != nil {
		mutate(&in)
	}
	r, err := backendFor(awg).Render(in, side)
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), ConfigFile)
	require.NoError(t, os.WriteFile(p, r.Files[ConfigFile], 0o600))
	return p
}

// procRoot creates <root>/proc/sys/net/ipv4/conf/<iface>/route_localnet.
func procRoot(t *testing.T, iface string) (root, file string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "proc/sys/net/ipv4/conf", iface)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	file = filepath.Join(dir, "route_localnet")
	require.NoError(t, os.WriteFile(file, []byte("0\n"), 0o600))
	return root, file
}

func TestUpKernelNode(t *testing.T) {
	cfg := writeConfig(t, false, backend.SideNode, nil)
	root, rl := procRoot(t, "dey-main")
	f := &fakeRunner{exists: map[string]bool{"dey-main": true}}
	nl := &fakeNetlink{family: 0x1c}
	m := &Manager{Root: root, Runner: f, openNetlink: func() (nlTransport, error) { return nl, nil }}
	require.NoError(t, m.Up(context.Background(), cfg))
	require.Equal(t, []string{
		"ip -o link show dev dey-main", // stale interface from a crash
		"ip link del dev dey-main",
		"ip link add dev dey-main type wireguard",
		"ip address replace 10.77.7.2/30 dev dey-main",
		"ip link set dev dey-main mtu 1420 up",
	}, f.calls)
	require.True(t, nl.closed)
	require.Len(t, nl.reqs, 2)
	require.Equal(t, encodeGetFamily(1, "wireguard"), nl.reqs[0])
	c, err := LoadConfig(cfg)
	require.NoError(t, err)
	d, err := settingsFrom(c)
	require.NoError(t, err)
	require.Equal(t, encodeSetDevice(0x1c, 2, d), nl.reqs[1])
	require.Equal(t, 30001, d.listenPort)
	require.False(t, d.endpoint.IsValid())
	got, err := os.ReadFile(rl) // #nosec G304 -- test path
	require.NoError(t, err)
	require.Equal(t, "1\n", string(got))

	// Down deletes it again and is idempotent.
	f.calls = nil
	require.NoError(t, m.Down(context.Background(), cfg))
	require.NoError(t, m.Down(context.Background(), cfg))
	require.Equal(t, []string{"ip -o link show dev dey-main", "ip link del dev dey-main", "ip -o link show dev dey-main"}, f.calls)
}

func TestUpKernelFailuresCleanUp(t *testing.T) {
	cfg := writeConfig(t, false, backend.SideHub, nil)
	cases := []struct {
		name   string
		runner *fakeRunner
		nl     *fakeNetlink
		open   error
		want   string
		link   bool // interface created (and must be removed)
	}{
		{"link add", &fakeRunner{failOn: "ip link add"}, &fakeNetlink{family: 1}, nil, "kernel module", false},
		{"show fails", &fakeRunner{failOn: "ip -o link show"}, &fakeNetlink{family: 1}, nil, "Operation not supported", false},
		{"socket", &fakeRunner{}, nil, errors.New("EPERM"), "generic netlink", true},
		{"no family", &fakeRunner{}, &fakeNetlink{noFam: true}, nil, "not found", true},
		{"get family", &fakeRunner{}, &fakeNetlink{failGet: syscall.ENOENT}, nil, "kernel module loaded", true},
		{"set device", &fakeRunner{}, &fakeNetlink{family: 1, setErr: syscall.EPERM}, nil, "WG_CMD_SET_DEVICE", true},
		{"address", &fakeRunner{failOn: "ip address"}, &fakeNetlink{family: 1}, nil, "ip address replace", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Manager{Runner: c.runner, openNetlink: func() (nlTransport, error) {
				if c.open != nil {
					return nil, c.open
				}
				return c.nl, nil
			}}
			err := m.Up(context.Background(), cfg)
			require.True(t, deyerr.HasCode(err, deyerr.B070), "%v", err)
			require.Contains(t, deyerr.As(err).Message()+deyerr.As(err).Error(), "dey-main")
			require.Contains(t, err.Error(), c.want)
			if c.link {
				require.Contains(t, c.runner.calls, "ip link del dev dey-main", "half-built interface must be removed")
				require.False(t, c.runner.exists["dey-main"])
			}
		})
	}
	// route_localnet file missing (node side).
	node := writeConfig(t, false, backend.SideNode, nil)
	f := &fakeRunner{}
	m := &Manager{Root: t.TempDir(), Runner: f, openNetlink: func() (nlTransport, error) { return &fakeNetlink{family: 1}, nil }}
	err := m.Up(context.Background(), node)
	require.True(t, deyerr.HasCode(err, deyerr.B070), "%v", err)
	require.Contains(t, err.Error(), "route_localnet")
	require.False(t, f.exists["dey-main"])

	// No runner.
	err = (&Manager{}).Up(context.Background(), cfg)
	require.True(t, deyerr.HasCode(err, deyerr.B070))
	err = (&Manager{}).Down(context.Background(), cfg)
	require.True(t, deyerr.HasCode(err, deyerr.B070))
	// Invalid config.
	err = Up(context.Background(), filepath.Join(t.TempDir(), "nope.json"), f)
	require.True(t, deyerr.HasCode(err, deyerr.B072))
	err = Down(context.Background(), filepath.Join(t.TempDir(), "nope.json"), f)
	require.True(t, deyerr.HasCode(err, deyerr.B072))
	// Down with a failing `ip link del`.
	f = &fakeRunner{exists: map[string]bool{"dey-main": true}, failOn: "ip link del"}
	err = Down(context.Background(), cfg, f)
	require.True(t, deyerr.HasCode(err, deyerr.B070), "%v", err)
}

// uapiServer serves one UAPI exchange over a net.Pipe.
func uapiServer(t *testing.T, reply string) (dial func(context.Context, string) (net.Conn, error), got chan string, wait func()) {
	t.Helper()
	got = make(chan string, 1)
	var wg sync.WaitGroup
	dial = func(context.Context, string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { _ = c2.Close() }()
			r := bufio.NewReader(c2)
			var b strings.Builder
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					got <- b.String()
					return
				}
				b.WriteString(line)
				if line == "\n" {
					break
				}
			}
			got <- b.String()
			_, _ = c2.Write([]byte(reply))
		}()
		return c1, nil
	}
	return dial, got, wg.Wait
}

func TestUpUserspace(t *testing.T) {
	cfg := writeConfig(t, true, backend.SideHub, nil)
	root := t.TempDir()
	dial, got, wait := uapiServer(t, "errno=0\n\n")
	f := &fakeRunner{}
	m := &Manager{Root: root, Runner: f, dialUAPI: dial}
	require.NoError(t, m.Up(context.Background(), cfg))
	wait()
	req := <-got
	c, err := LoadConfig(cfg)
	require.NoError(t, err)
	want, err := uapiSetRequest(c)
	require.NoError(t, err)
	require.Equal(t, want, req)
	require.DirExists(t, filepath.Join(root, "var/run/amneziawg"))
	require.Equal(t, []string{
		"ip address replace 10.77.7.1/30 dev dey-main",
		"ip link set dev dey-main mtu 1420 up",
	}, f.calls)

	// A non-zero errno is reported.
	dial, _, wait = uapiServer(t, "errno=22\n\n")
	m = &Manager{Root: root, Runner: &fakeRunner{}, dialUAPI: dial}
	err = m.Up(context.Background(), cfg)
	wait()
	require.True(t, deyerr.HasCode(err, deyerr.B071), "%v", err)
	require.Contains(t, err.Error(), "errno=22")

	// A connection closed without a reply is reported.
	dial, _, wait = uapiServer(t, "")
	m = &Manager{Root: root, Runner: &fakeRunner{}, dialUAPI: dial}
	err = m.Up(context.Background(), cfg)
	wait()
	require.True(t, deyerr.HasCode(err, deyerr.B071), "%v", err)

	// The socket never appears.
	m = &Manager{Root: root, Runner: &fakeRunner{}, SocketWait: 250 * time.Millisecond}
	err = m.Up(context.Background(), cfg)
	require.True(t, deyerr.HasCode(err, deyerr.B071), "%v", err)
	require.Contains(t, err.Error(), "not ready")

	// ip fails after configuration.
	dial, _, wait = uapiServer(t, "errno=0\n\n")
	m = &Manager{Root: root, Runner: &fakeRunner{failOn: "ip link set"}, dialUAPI: dial}
	err = m.Up(context.Background(), cfg)
	wait()
	require.True(t, deyerr.HasCode(err, deyerr.B071), "%v", err)

	// Down of an awg interface.
	fr := &fakeRunner{}
	require.NoError(t, Down(context.Background(), cfg, fr))
	fr.failOn = "ip -o"
	require.True(t, deyerr.HasCode(Down(context.Background(), cfg, fr), deyerr.B071))
}

// TestUpUserspaceUnixSocket exercises the real dialer with a unix socket.
func TestUpUserspaceUnixSocket(t *testing.T) {
	cfg := writeConfig(t, true, backend.SideNode, nil)
	root, err := os.MkdirTemp("", "wg") // short path: sun_path is 108 bytes
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := filepath.Join(root, "var/run/amneziawg")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	// route_localnet lives under the same root as the socket.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "proc/sys/net/ipv4/conf/dey-main"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/net/ipv4/conf/dey-main/route_localnet"), []byte("0"), 0o600))

	sock := filepath.Join(dir, "dey-main.sock")
	var wg sync.WaitGroup
	wg.Add(1)
	started := make(chan struct{})
	go func() {
		defer wg.Done()
		<-started
		time.Sleep(300 * time.Millisecond) // appears after Up started polling
		ln, err := net.Listen("unix", sock)
		if err != nil {
			return
		}
		defer func() { _ = ln.Close() }()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil || line == "\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("errno=0\n\n"))
	}()
	close(started)
	f := &fakeRunner{}
	err = (&Manager{Root: root, Runner: f}).Up(context.Background(), cfg)
	wg.Wait()
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(root, "proc/sys/net/ipv4/conf/dey-main/route_localnet")) // #nosec G304 -- test path
	require.NoError(t, err)
	require.Equal(t, "1\n", string(b))
}

func TestUAPIRequestGolden(t *testing.T) {
	for _, side := range []backend.Side{backend.SideHub, backend.SideNode} {
		in := fixture(t, true)
		r, err := NewAWG().Render(in, side)
		require.NoError(t, err)
		c, err := ParseConfig(r.Files[ConfigFile], "x")
		require.NoError(t, err)
		req, err := uapiSetRequest(c)
		require.NoError(t, err)
		checkGolden(t, "uapi."+side.String()+".golden", []byte(req))
		require.True(t, strings.HasPrefix(req, "set=1\nprivate_key="))
		require.True(t, strings.HasSuffix(req, "\n\n"))
		priv, _ := decodeKey(c.PrivateKey)
		require.Contains(t, req, "private_key="+hex.EncodeToString(priv[:])+"\n")
		for _, k := range []string{"jc=5\n", "jmin=50\n", "jmax=1000\n", "s1=40\n", "s2=77\n", "h1=1234567\n", "h2=2345678\n", "h3=3456789\n", "h4=4567890\n", "replace_peers=true\n", "replace_allowed_ips=true\n"} {
			require.Contains(t, req, k)
		}
	}
	_, err := uapiSetRequest(&Config{PrivateKey: "x"})
	require.Error(t, err)
	_, err = uapiSetRequest(&Config{PrivateKey: fixedKeys(t, false)[KeyHubPrivate]})
	require.Error(t, err)
}

func littleEndian() bool {
	var b [2]byte
	ne.PutUint16(b[:], 1)
	return b[0] == 1
}

// hexDump renders b as 16-byte hex lines for golden files.
func hexDump(b []byte) []byte {
	var s strings.Builder
	for i := 0; i < len(b); i += 16 {
		s.WriteString(hex.EncodeToString(b[i:min(i+16, len(b))]) + "\n")
	}
	return []byte(s.String())
}

// TestNetlinkEncoding pins the exact bytes of the generic netlink
// requests (little-endian hosts: amd64, arm64).
func TestNetlinkEncoding(t *testing.T) {
	if !littleEndian() {
		t.Skip("golden bytes are little-endian")
	}
	checkGolden(t, "netlink_getfamily.golden", hexDump(encodeGetFamily(1, "wireguard")))

	var priv, peer [32]byte
	for i := range priv {
		priv[i] = byte(i + 1)
		peer[i] = byte(0xa0 + i)
	}
	hub := deviceSettings{
		iface: "dey-main", privateKey: priv, peerKey: peer,
		endpoint:   netip.MustParseAddrPort("1.2.3.4:30001"),
		keepalive:  25,
		allowedIPs: []netip.Prefix{netip.MustParsePrefix("10.77.7.2/32")},
	}
	msg := encodeSetDevice(0x1c, 2, hub)
	checkGolden(t, "netlink_setdevice_hub.golden", hexDump(msg))

	// Structural checks of the same message.
	require.Equal(t, uint32(len(msg)), binary.LittleEndian.Uint32(msg[0:4]))
	require.Equal(t, uint16(0x1c), binary.LittleEndian.Uint16(msg[4:6]))
	require.Equal(t, uint16(nlmFRequest|nlmFAck), binary.LittleEndian.Uint16(msg[6:8]))
	require.Equal(t, uint32(2), binary.LittleEndian.Uint32(msg[8:12]))
	require.Equal(t, []byte{wgCmdSetDevice, wgGenlVersion}, msg[16:18])
	attrs := parseAttrs(msg[20:])
	require.Equal(t, "dey-main\x00", string(attrs[wgDeviceAIfname]))
	require.Equal(t, priv[:], attrs[wgDeviceAPrivateKey])
	require.Equal(t, []byte{1, 0, 0, 0}, attrs[wgDeviceAFlags])
	require.NotContains(t, attrs, uint16(wgDeviceAListenPort))
	peers := parseAttrs(attrs[wgDeviceAPeers])
	p := parseAttrs(peers[0])
	require.Equal(t, peer[:], p[wgPeerAPublicKey])
	require.Equal(t, []byte{2, 0, 0, 0}, p[wgPeerAFlags])
	require.Equal(t, []byte{2, 0, 0x75, 0x31, 1, 2, 3, 4, 0, 0, 0, 0, 0, 0, 0, 0}, p[wgPeerAEndpoint])
	require.Equal(t, []byte{25, 0}, p[wgPeerAKeepalive])
	ip := parseAttrs(parseAttrs(p[wgPeerAAllowedIP])[0])
	require.Equal(t, []byte{2, 0}, ip[wgAllowedIPAFamily])
	require.Equal(t, []byte{10, 77, 7, 2}, ip[wgAllowedIPAAddr])
	require.Equal(t, []byte{32}, ip[wgAllowedIPAMask])
	// Nested attributes carry NLA_F_NESTED.
	require.Equal(t, uint16(wgDeviceAPeers|nlaFNested), findType(msg[20:], wgDeviceAPeers))

	node := deviceSettings{
		iface: "dey-main", privateKey: priv, peerKey: peer, listenPort: 30001,
		allowedIPs: []netip.Prefix{netip.MustParsePrefix("10.77.7.1/32"), netip.MustParsePrefix("fd00::1/128")},
	}
	msg = encodeSetDevice(0x1c, 3, node)
	checkGolden(t, "netlink_setdevice_node.golden", hexDump(msg))
	attrs = parseAttrs(msg[20:])
	require.Equal(t, []byte{0x31, 0x75}, attrs[wgDeviceAListenPort])
	p = parseAttrs(parseAttrs(attrs[wgDeviceAPeers])[0])
	require.NotContains(t, p, uint16(wgPeerAEndpoint))
	require.NotContains(t, p, uint16(wgPeerAKeepalive))

	// IPv6 endpoints use sockaddr_in6.
	sa := sockaddr(netip.MustParseAddrPort("[2001:db8::1]:51820"))
	require.Len(t, sa, 28)
	require.Equal(t, []byte{10, 0, 0xca, 0x6c}, sa[0:4])
	require.Equal(t, netip.MustParseAddr("2001:db8::1").AsSlice(), sa[8:24])
}

// findType returns the raw type field of the first attribute whose masked
// type is typ.
func findType(b []byte, typ uint16) uint16 {
	for len(b) >= nlaHdrLen {
		n := int(ne.Uint16(b[0:2]))
		raw := ne.Uint16(b[2:4])
		if raw&nlaTypeMask == typ {
			return raw
		}
		b = b[align4(n):]
	}
	return 0
}

func TestNetlinkParsing(t *testing.T) {
	// A reply datagram: NEWFAMILY then ACK.
	fam := genlMessage(genlIDCtrl, 1, 1, 2, nlString(ctrlAttrName, "wireguard"), nlU16(ctrlAttrID, 0x21))
	ack := make([]byte, nlmsgHdrLen+4)
	ne.PutUint32(ack[0:4], uint32(len(ack)))
	ne.PutUint16(ack[4:6], nlmsgError)
	ne.PutUint32(ack[8:12], 1)
	msgs, err := parseMessages(append(fam, ack...))
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	id, err := familyID(msgs, 1)
	require.NoError(t, err)
	require.Equal(t, uint16(0x21), id)
	require.NoError(t, ackError(msgs[1]))
	_, err = familyID(msgs, 9)
	require.Error(t, err)

	neg := make([]byte, nlmsgHdrLen+4)
	copy(neg, ack)
	ne.PutUint32(neg[16:20], uint32(0xfffffffe)) // -2 = ENOENT
	msgs, err = parseMessages(neg)
	require.NoError(t, err)
	require.ErrorIs(t, ackError(msgs[0]), syscall.ENOENT)
	require.Error(t, ackError(nlMessage{typ: nlmsgError}))

	_, err = parseMessages([]byte{0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	require.Error(t, err)
	require.Empty(t, parseAttrs([]byte{0xff, 0, 1, 0}))
}

// TestGenetlinkFamilyReal resolves the wireguard family over a real socket
// (read-only, needs no privileges); skipped when the module is absent.
func TestGenetlinkFamilyReal(t *testing.T) {
	tr, err := openGenetlink()
	if err != nil {
		t.Skipf("no generic netlink: %v", err)
	}
	defer func() { _ = tr.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msgs, err := tr.Roundtrip(ctx, encodeGetFamily(1, wgGenlName), 1)
	if errors.Is(err, syscall.ENOENT) {
		t.Skip("wireguard module not loaded")
	}
	require.NoError(t, err)
	id, err := familyID(msgs, 1)
	require.NoError(t, err)
	require.NotZero(t, id)
}

// TestPrepare: PreStart creates the amneziawg-go socket directory before
// the awg unit starts (under ProtectSystem=strict amneziawg-go cannot) and
// is a no-op for the kernel transport.
func TestPrepare(t *testing.T) {
	cfg := writeConfig(t, true, backend.SideHub, nil)
	root := t.TempDir()
	m := &Manager{Root: root, Runner: &fakeRunner{}}
	require.NoError(t, m.Prepare(context.Background(), cfg))
	require.DirExists(t, filepath.Join(root, "var/run/amneziawg"))
	require.NoError(t, m.Prepare(context.Background(), cfg), "idempotent")

	kernel := writeConfig(t, false, backend.SideHub, nil)
	kroot := t.TempDir()
	require.NoError(t, (&Manager{Root: kroot}).Prepare(context.Background(), kernel))
	require.NoDirExists(t, filepath.Join(kroot, "var/run/amneziawg"))

	// A file in the way of the directory is reported as DEY-B071.
	bad := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(bad, "var/run"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(bad, "var/run/amneziawg"), nil, 0o600))
	err := (&Manager{Root: bad}).Prepare(context.Background(), cfg)
	require.True(t, deyerr.HasCode(err, deyerr.B071), "%v", err)
	// ... and so is the same failure inside Up.
	err = (&Manager{Root: bad, Runner: &fakeRunner{}}).Up(context.Background(), cfg)
	require.True(t, deyerr.HasCode(err, deyerr.B071), "%v", err)

	err = (&Manager{Root: root}).Prepare(context.Background(), filepath.Join(root, "missing.json"))
	require.True(t, deyerr.HasCode(err, deyerr.B072), "%v", err)

	// Backend hooks: kernel PreStart is a no-op, awg PreStart prepares.
	dir := filepath.Dir(kernel)
	require.NoError(t, New().PreStart(context.Background(), dir, &fakeRunner{}))
	err = NewAWG().PreStart(context.Background(), t.TempDir(), &fakeRunner{})
	require.True(t, deyerr.HasCode(err, deyerr.B072), "awg PreStart reads wg.json: %v", err)
}
