package firewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/localroot4/deyroute/internal/cfnets"
	"github.com/localroot4/deyroute/internal/exec"
)

// realNFTEnv enables TestRealNFT. It changes the firewall of the network
// namespace it runs in, so only set it inside a throwaway namespace:
//
//	go test -c -o fw.test ./internal/firewall
//	sudo unshare -n env DEYROUTE_TEST_NFT_NETNS=1 ./fw.test -test.run TestRealNFT -test.v
const realNFTEnv = "DEYROUTE_TEST_NFT_NETNS"

// TestRealNFT applies every golden Spec with the real runner and nft,
// replaces it, checks that nothing else in the ruleset changed, and removes
// it again (twice, for idempotence). It then checks ports against a foreign
// nftables table and raw iptables rules as the real tools list them, and
// opens a blocked port with the suggested command (nft and iptables must be
// installed).
func TestRealNFT(t *testing.T) {
	if os.Getenv(realNFTEnv) != "1" {
		t.Skip("set " + realNFTEnv + "=1 inside `unshare -n` to run against the real nft")
	}
	ctx := context.Background()
	r := exec.NewRunner()

	// A foreign table that must survive untouched.
	_, _, err := r.Run(ctx, "nft", []string{"-f", "-"}, []byte("table ip other {\n\tchain input {\n\t\ttype filter hook input priority 0; policy accept;\n\t\ttcp dport 22 accept\n\t}\n}\n"))
	require.NoError(t, err)
	before, _, err := r.Run(ctx, "nft", []string{"list", "ruleset"}, nil)
	require.NoError(t, err)

	require.Contains(t, Detect(ctx, r), NFTables)

	names := make([]string, 0, len(goldenSpecs))
	for n := range goldenSpecs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := goldenSpecs[name]
		t.Run(name, func(t *testing.T) {
			require.NoError(t, Apply(ctx, r, spec))
			require.NoError(t, Apply(ctx, r, spec), "replacing an existing table")
			out, err := Show(ctx, r)
			require.NoError(t, err)
			require.Contains(t, out, "table inet deyroute {")
			require.Contains(t, out, "set nodes {")
			require.NoError(t, Remove(ctx, r))
			require.NoError(t, Remove(ctx, r), "idempotent")
			out, err = Show(ctx, r)
			require.NoError(t, err)
			require.Empty(t, out)
		})
	}

	after, _, err := r.Run(ctx, "nft", []string{"list", "ruleset"}, nil)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "only table inet deyroute may change")

	// The check sees the foreign table and our own table is ignored.
	require.NoError(t, Apply(ctx, r, hubSpec()))
	v, err := Check(ctx, r, 30500, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	require.NoError(t, Remove(ctx, r))

	// The front port's interval sets are accepted by the real nft and listed
	// back in a form the parser reads: every Cloudflare range, both families.
	for _, name := range []string{"hub_front_cf", "hub_front_cf_ipv6"} {
		spec := goldenSpecs[name]
		require.NoError(t, Apply(ctx, r, spec), name)
		out, err := Show(ctx, r)
		require.NoError(t, err)
		require.Contains(t, out, "flags interval", name)
		var dey *nftTable
		for _, tb := range parseNFTRuleset(out) {
			if tb.ref() == TableRef {
				dey = tb
			}
		}
		require.NotNil(t, dey, name)
		require.Len(t, dey.sets[SetCF4], len(cfnets.V4()), name)
		require.Equal(t, spec.IPv6, len(dey.sets[SetCF6]) == len(cfnets.V6()), name)
		b, u, detail := dey.ruleset(spec.FrontPort, ProtoTCP).blocks("input")
		require.True(t, b && !u, "%s %s", name, detail)
		v, err := Check(ctx, r, spec.FrontPort, ProtoTCP)
		require.NoError(t, err)
		require.False(t, v.Blocked, v.Detail)
		require.NoError(t, Remove(ctx, r))
	}

	// A foreign policy-drop table as nft lists it (comments after verdicts,
	// a named verdict map): commented accepts are open, the map's drop
	// blocks, and the confirmed suggestion really opens the port.
	fw, err := os.ReadFile("testdata/nft_comments.txt")
	require.NoError(t, err)
	_, _, err = r.Run(ctx, "nft", []string{"-f", "-"}, fw)
	require.NoError(t, err)
	v, err = Check(ctx, r, 443, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	v, err = Check(ctx, r, 8081, ProtoTCP)
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, "inet fw", v.Table)
	require.Equal(t, []string{"nft insert rule inet fw input tcp dport 8081 accept"}, v.Commands)
	require.NoError(t, v.Open(ctx, r))
	v, err = Check(ctx, r, 8081, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	_, _, err = r.Run(ctx, "nft", []string{"delete", "table", "inet", "fw"}, nil)
	require.NoError(t, err)

	// Raw iptables (iptables-nft or legacy), as the real tool prints it.
	for _, args := range [][]string{
		{"-P", "INPUT", "DROP"},
		{"-A", "INPUT", "-i", "lo", "-j", "ACCEPT"},
		{"-A", "INPUT", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		{"-A", "INPUT", "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-m", "comment", "--comment", "web traffic", "-j", "ACCEPT"},
		{"-A", "INPUT", "-p", "udp", "--dport", "1000:2000", "-j", "ACCEPT"},
		{"-A", "INPUT", "-p", "tcp", "--dport", "7000", "-j", "NFQUEUE", "--queue-num", "0"},
	} {
		_, _, err = r.Run(ctx, "iptables", args, nil)
		require.NoError(t, err, "iptables %v", args)
	}
	require.Contains(t, Detect(ctx, r), IPTables)
	for _, c := range []portCase{
		{443, "tcp", false, false}, {1500, "udp", false, false}, {7000, "tcp", false, true}, {8443, "tcp", true, false},
	} {
		v, err = Check(ctx, r, c.port, c.proto)
		require.NoError(t, err)
		require.Equal(t, c.blocked, v.Blocked, "%d/%s %s", c.port, c.proto, v.Detail)
		require.Equal(t, c.uncertain, v.Uncertain, "%d/%s %s", c.port, c.proto, v.Detail)
	}
	v, err = Check(ctx, r, 8443, ProtoTCP)
	require.NoError(t, err)
	require.Equal(t, IPTables, v.By)
	require.Equal(t, []string{"iptables -I INPUT -p tcp --dport 8443 -j ACCEPT"}, v.Commands)
	require.NoError(t, v.Open(ctx, r))
	v, err = Check(ctx, r, 8443, ProtoTCP)
	require.NoError(t, err)
	require.False(t, v.Blocked, v.Detail)
	for _, args := range [][]string{{"-F", "INPUT"}, {"-P", "INPUT", "ACCEPT"}} {
		_, _, err = r.Run(ctx, "iptables", args, nil)
		require.NoError(t, err)
	}

	// A pre-existing inet deyroute table with other content is replaced.
	_, _, err = r.Run(ctx, "nft", []string{"-f", "-"}, []byte("table inet deyroute {\n\tchain stale {\n\t}\n}\n"))
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, r, Spec{ControlPort: 44433, RestrictControl: true}))
	out, err := Show(ctx, r)
	require.NoError(t, err)
	require.Contains(t, out, "tcp dport 44433 drop")
	require.NotContains(t, out, "stale")
	require.NoError(t, Remove(ctx, r))
}

// TestRealNFTStats loads every accounting golden into the real nft, reads
// its counters back (seeds included), and removes it; then it sends real
// traffic over veth pairs from other network namespaces and checks the
// counted bytes: TCP and UDP to a listen port, nothing over loopback,
// nothing to other ports, nothing that another table drops, a forwarded
// (DNATed) flow through the forward-hook chain, and a rebuild seeded with
// the last reading keeps the totals.
func TestRealNFTStats(t *testing.T) {
	if os.Getenv(realNFTEnv) != "1" {
		t.Skip("set " + realNFTEnv + "=1 inside `unshare -n` to run against the real nft")
	}
	ctx := context.Background()
	r := exec.NewRunner()
	require.NoError(t, StatsSupport(ctx, r))

	before, _, err := r.Run(ctx, "nft", []string{"list", "ruleset"}, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(statsGoldenSpecs))
	for n := range statsGoldenSpecs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := statsGoldenSpecs[name]
		t.Run(name, func(t *testing.T) {
			require.NoError(t, ApplyStats(ctx, r, spec))
			require.NoError(t, ApplyStats(ctx, r, spec), "replacing an existing table")
			m, err := ReadCounters(ctx, r)
			if len(spec.Tunnels) == 0 {
				require.ErrorIs(t, err, ErrNoStatsTable, "an empty spec removes the table")
				return
			}
			require.NoError(t, err)
			require.Len(t, m, 2*len(spec.Tunnels))
			for _, tu := range spec.Tunnels {
				in, out, ok := TunnelCounters(m, tu.ID)
				require.True(t, ok, tu.ID)
				require.Equal(t, spec.Seed[CounterIn(tu.ID)], in, tu.ID)
				require.Equal(t, spec.Seed[CounterOut(tu.ID)], out, tu.ID)
			}
			require.NoError(t, RemoveStats(ctx, r))
			require.NoError(t, RemoveStats(ctx, r), "idempotent")
			_, err = ReadCounters(ctx, r)
			require.ErrorIs(t, err, ErrNoStatsTable)
		})
	}
	after, _, err := r.Run(ctx, "nft", []string{"list", "ruleset"}, nil)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "only table inet deyroute_stats may change")

	// Both deyroute tables live side by side; removing one keeps the other.
	require.NoError(t, Apply(ctx, r, hubSpec()))
	require.NoError(t, ApplyStats(ctx, r, statsGoldenSpecs["several"]))
	require.NoError(t, Remove(ctx, r))
	_, err = ReadCounters(ctx, r)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, r, hubSpec()))
	require.NoError(t, RemoveStats(ctx, r))
	out, err := Show(ctx, r)
	require.NoError(t, err)
	require.Contains(t, out, "table inet deyroute {")
	require.NoError(t, Remove(ctx, r))

	statsTraffic(ctx, t, r)
}

// Addresses of the traffic test: the "hub" (this namespace) reaches the
// client namespace over 10.199.0.0/24 and a server namespace (the target of
// a DNATed, forwarded flow) over 10.198.0.0/24.
const (
	statsHub      = "10.199.0.1"
	statsClient   = "10.199.0.2"
	statsHubSrv   = "10.198.0.1"
	statsServer   = "10.198.0.2"
	statsPort     = 18443 // tunnel "main", tcp and udp
	statsNATPort  = 18445 // tunnel "wg", DNATed to statsServer:statsSrvPort
	statsSrvPort  = 19445
	statsFreePort = 18999 // in no tunnel
	statsUp       = 100 << 10
	statsDown     = 1 << 20
)

func statsTraffic(ctx context.Context, t *testing.T, r exec.Runner) {
	ip := func(r exec.Runner, args ...string) error {
		if _, stderr, err := r.Run(ctx, "ip", args, nil); err != nil {
			return fmt.Errorf("ip %v: %w: %s", args, err, stderr)
		}
		return nil
	}
	require.NoError(t, ip(r, "link", "set", "lo", "up"))
	client, server := newNetnsThread(t), newNetnsThread(t)
	for _, l := range []struct {
		dev, peer, addr, peerAddr string
		ns                        *netnsThread
	}{{"dsta0", "dsta1", statsHub, statsClient, client}, {"dstb0", "dstb1", statsHubSrv, statsServer, server}} {
		for _, args := range [][]string{
			{"link", "add", l.dev, "type", "veth", "peer", "name", l.peer},
			{"addr", "add", l.addr + "/24", "dev", l.dev},
			{"link", "set", l.dev, "up"},
			{"link", "set", l.peer, "netns", strconv.Itoa(l.ns.tid)},
		} {
			require.NoError(t, ip(r, args...))
		}
		require.NoError(t, l.ns.do(func() error {
			nr := exec.NewRunner()
			for _, args := range [][]string{
				{"link", "set", "lo", "up"},
				{"addr", "add", l.peerAddr + "/24", "dev", l.peer},
				{"link", "set", l.peer, "up"},
				{"route", "add", "default", "via", l.addr},
			} {
				if err := ip(nr, args...); err != nil {
					return err
				}
			}
			return nil
		}))
	}
	require.NoError(t, os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o600))
	_, _, err := r.Run(ctx, "nft", []string{"-f", "-"}, []byte(fmt.Sprintf(
		"table ip stats_test_nat {\n\tchain pre {\n\t\ttype nat hook prerouting priority dstnat; policy accept;\n\t\ttcp dport %d dnat to %s:%d\n\t}\n}\n",
		statsNATPort, statsServer, statsSrvPort)))
	require.NoError(t, err)
	defer func() { _, _, _ = r.Run(ctx, "nft", []string{"delete", "table", "ip", "stats_test_nat"}, nil) }()

	spec := StatsSpec{Tunnels: []StatsTunnel{
		{ID: "main", TCP: []int{statsPort}, UDP: []int{statsPort}},
		{ID: "wg", TCP: []int{statsNATPort}, NAT: true},
	}}
	require.NoError(t, ApplyStats(ctx, r, spec))
	defer func() { require.NoError(t, RemoveStats(ctx, r)) }()
	read := func() map[string]Counter {
		t.Helper()
		m, err := ReadCounters(ctx, r)
		require.NoError(t, err)
		return m
	}
	delta := func(a, b map[string]Counter, name string) Counter {
		return Counter{Packets: b[name].Packets - a[name].Packets, Bytes: b[name].Bytes - a[name].Bytes}
	}

	// TCP: 100 KiB up, 1 MiB down from the client namespace.
	tcpSrv, err := serveTCP("0.0.0.0:" + strconv.Itoa(statsPort))
	require.NoError(t, err)
	start := read()
	require.NoError(t, client.do(func() error { return tcpExchange(net.JoinHostPort(statsHub, strconv.Itoa(statsPort))) }))
	require.NoError(t, tcpSrv.wait())
	got := read()
	in, out := delta(start, got, CounterIn("main")), delta(start, got, CounterOut("main"))
	t.Logf("tcp: in %d B / %d packets, out %d B / %d packets", in.Bytes, in.Packets, out.Bytes, out.Packets)
	require.GreaterOrEqual(t, in.Bytes, uint64(statsUp))
	require.LessOrEqual(t, in.Bytes, uint64(statsUp)+64<<10, "headers and ACKs only")
	require.GreaterOrEqual(t, out.Bytes, uint64(statsDown))
	require.LessOrEqual(t, out.Bytes, uint64(statsDown)*11/10)
	require.Equal(t, Counter{}, delta(start, got, CounterIn("wg")))
	require.Equal(t, Counter{}, delta(start, got, CounterOut("wg")))

	// Loopback is not counted (probes, the canary).
	tcpSrv, err = serveTCP("0.0.0.0:" + strconv.Itoa(statsPort))
	require.NoError(t, err)
	start = read()
	require.NoError(t, tcpExchange(net.JoinHostPort("127.0.0.1", strconv.Itoa(statsPort))))
	require.NoError(t, tcpSrv.wait())
	require.Equal(t, start, read(), "loopback")

	// UDP, exactly: 10 datagrams of 1000 bytes up (IPv4 + UDP = 28 bytes
	// of headers each) and 10 of 500 back.
	udp, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(statsPort))
	require.NoError(t, err)
	defer udp.Close()
	start = read()
	require.NoError(t, client.do(func() error { return udpExchange(udp, net.JoinHostPort(statsHub, strconv.Itoa(statsPort)), 10) }))
	got = read()
	require.Equal(t, Counter{Packets: 10, Bytes: 10 * 1028}, delta(start, got, CounterIn("main")))
	require.Equal(t, Counter{Packets: 10, Bytes: 10 * 528}, delta(start, got, CounterOut("main")))

	// Other ports are not counted, and neither is what another table drops.
	start = read()
	require.NoError(t, client.do(func() error { return udpExchange(nil, net.JoinHostPort(statsHub, strconv.Itoa(statsFreePort)), 10) }))
	_, _, err = r.Run(ctx, "nft", []string{"-f", "-"}, []byte(fmt.Sprintf(
		"table inet stats_test_drop {\n\tchain input {\n\t\ttype filter hook input priority 0; policy accept;\n\t\tudp dport %d drop\n\t}\n}\n", statsPort)))
	require.NoError(t, err)
	require.NoError(t, client.do(func() error { return udpExchange(nil, net.JoinHostPort(statsHub, strconv.Itoa(statsPort)), 10) }))
	_, _, err = r.Run(ctx, "nft", []string{"delete", "table", "inet", "stats_test_drop"}, nil)
	require.NoError(t, err)
	got = read()
	require.Equal(t, start[CounterIn("main")], got[CounterIn("main")], "unknown port and dropped packets")
	// The kernel answers the unknown port with ICMP, which is no tunnel's.
	require.Equal(t, start[CounterOut("main")], got[CounterOut("main")])

	// A DNATed flow is forwarded, not delivered locally: counted by the
	// forward-hook chain under its original destination port.
	var natSrv *tcpServer
	require.NoError(t, server.do(func() (err error) {
		natSrv, err = serveTCP("0.0.0.0:" + strconv.Itoa(statsSrvPort))
		return err
	}))
	start = read()
	require.NoError(t, client.do(func() error { return tcpExchange(net.JoinHostPort(statsHub, strconv.Itoa(statsNATPort))) }))
	require.NoError(t, natSrv.wait())
	got = read()
	in, out = delta(start, got, CounterIn("wg")), delta(start, got, CounterOut("wg"))
	t.Logf("nat: in %d B / %d packets, out %d B / %d packets", in.Bytes, in.Packets, out.Bytes, out.Packets)
	require.GreaterOrEqual(t, in.Bytes, uint64(statsUp))
	require.LessOrEqual(t, in.Bytes, uint64(statsUp)+64<<10)
	require.GreaterOrEqual(t, out.Bytes, uint64(statsDown))
	require.LessOrEqual(t, out.Bytes, uint64(statsDown)*6/5, "forwarded segments are not merged as much")
	require.Equal(t, start[CounterIn("main")], got[CounterIn("main")], "not counted twice")
	require.Equal(t, start[CounterOut("main")], got[CounterOut("main")])

	// A rebuild (another port set) seeded with the last reading keeps the
	// totals.
	last := read()
	spec.Seed = last
	spec.Tunnels = append(spec.Tunnels, StatsTunnel{ID: "new", UDP: []int{statsFreePort}})
	require.NoError(t, ApplyStats(ctx, r, spec))
	now := read()
	for name, c := range last {
		require.Equal(t, c, now[name], name)
	}
	require.Equal(t, Counter{}, now[CounterIn("new")])
}

// netnsThread is a locked OS thread in its own network namespace: sockets
// opened and programs started from do run there.
type netnsThread struct {
	tid  int
	work chan func()
}

func newNetnsThread(t *testing.T) *netnsThread {
	t.Helper()
	n := &netnsThread{work: make(chan func())}
	ready := make(chan error)
	go func() {
		// Never unlocked: the thread ends with the goroutine, and with it
		// the namespace.
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			ready <- err
			return
		}
		n.tid = unix.Gettid()
		ready <- nil
		for f := range n.work {
			f()
		}
	}()
	require.NoError(t, <-ready)
	t.Cleanup(func() { close(n.work) })
	return n
}

// do runs f on the namespace's thread and returns its error. f must not
// stop the goroutine (no t.FailNow).
func (n *netnsThread) do(f func() error) error {
	done := make(chan error, 1)
	n.work <- func() { done <- f() }
	return <-done
}

// tcpServer accepts one connection, reads statsUp bytes and answers with
// statsDown bytes.
type tcpServer struct {
	ln   net.Listener
	done chan error
}

func serveTCP(addr string) (*tcpServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &tcpServer{ln: ln, done: make(chan error, 1)}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			s.done <- err
			return
		}
		defer c.Close()
		if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			s.done <- err
			return
		}
		if _, err := io.ReadFull(c, make([]byte, statsUp)); err != nil {
			s.done <- err
			return
		}
		_, err = c.Write(make([]byte, statsDown))
		s.done <- err
	}()
	return s, nil
}

// wait waits for the exchange (closing the listener when it does not come)
// and then for the last FIN and ACK.
func (s *tcpServer) wait() error {
	var err error
	select {
	case err = <-s.done:
	case <-time.After(15 * time.Second):
		_ = s.ln.Close()
		err = <-s.done
	}
	_ = s.ln.Close()
	time.Sleep(100 * time.Millisecond)
	return err
}

// tcpExchange sends statsUp bytes to addr and reads statsDown back.
func tcpExchange(addr string) error {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if _, err := c.Write(make([]byte, statsUp)); err != nil {
		return err
	}
	n, err := io.Copy(io.Discard, c)
	if err == nil && n != statsDown {
		err = fmt.Errorf("read %d bytes, want %d", n, statsDown)
	}
	return err
}

// udpExchange sends n datagrams of 1000 bytes to addr; with a srv, srv
// answers each with 500 bytes and the client waits for every answer.
func udpExchange(srv net.PacketConn, addr string, n int) error {
	c, err := net.Dial("udp4", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	buf := make([]byte, 2048)
	for i := 0; i < n; i++ {
		_, err := c.Write(make([]byte, 1000))
		if srv == nil && errors.Is(err, syscall.ECONNREFUSED) {
			continue // the ICMP answer of an earlier datagram to a closed port
		}
		if err != nil {
			return err
		}
		if srv == nil {
			continue
		}
		if err := srv.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		got, from, err := srv.ReadFrom(buf)
		if err != nil {
			return err
		}
		if got != 1000 {
			return fmt.Errorf("server read %d bytes", got)
		}
		if _, err := srv.WriteTo(make([]byte, 500), from); err != nil {
			return err
		}
		if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if got, err = c.Read(buf); err != nil {
			return err
		}
		if got != 500 {
			return fmt.Errorf("client read %d bytes", got)
		}
	}
	time.Sleep(50 * time.Millisecond)
	return nil
}
