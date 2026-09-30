package doctor

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/ports"
)

// Ports returns the listening sockets: `ss -Hlntup` when it works,
// otherwise the listening entries of /proc/net/{tcp,tcp6,udp,udp6} with
// the owning process (ports.ProcFS).
func (c *Collector) Ports(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	out, _, err := c.runner().Run(ctx, "ss", []string{"-Hlntup"}, nil)
	if err == nil {
		text := string(out)
		if strings.TrimSpace(text) == "" {
			text = "no listening sockets\n"
		}
		return redactText("source: ss -Hlntup\n" + text)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "source: /proc/net (ss failed: %v)\n", err)
	socks := c.procListening()
	if len(socks) == 0 {
		b.WriteString("no listening sockets\n")
	}
	pfs := ports.ProcFS{Root: c.Root}
	for i, s := range socks {
		owner := ""
		if i < maxOwnerLookups {
			if pid, name, _, err := pfs.Owner(int(s.addr.Port()), s.proto); err == nil && pid > 0 {
				owner = fmt.Sprintf("  %s (pid %d)", name, pid)
			}
		}
		fmt.Fprintf(&b, "%s  %s%s\n", s.proto, s.addr, owner)
	}
	return redactText(b.String())
}

// listenSock is one listening socket from /proc/net.
type listenSock struct {
	proto string
	addr  netip.AddrPort
}

// procListening lists TCP sockets in LISTEN state and unconnected UDP
// sockets, sorted by protocol and port, without duplicates.
func (c *Collector) procListening() []listenSock {
	seen := map[listenSock]bool{}
	var out []listenSock
	for _, f := range []struct{ file, proto string }{
		{"tcp", ports.ProtoTCP}, {"tcp6", ports.ProtoTCP}, {"udp", ports.ProtoUDP}, {"udp6", ports.ProtoUDP},
	} {
		for _, s := range readListening(c.path("/proc/net/"+f.file), f.proto) {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].proto != out[j].proto {
			return out[i].proto < out[j].proto
		}
		if out[i].addr.Port() != out[j].addr.Port() {
			return out[i].addr.Port() < out[j].addr.Port()
		}
		return out[i].addr.Addr().Less(out[j].addr.Addr())
	})
	return out
}

// readListening parses one /proc/net table. A missing file yields nothing.
func readListening(p, proto string) []listenSock {
	f, err := os.Open(p) // #nosec G304 -- fixed /proc/net file under Root
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []listenSock
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		fl := strings.Fields(sc.Text())
		if len(fl) < 4 {
			continue
		}
		local, ok := parseHexAddrPort(fl[1])
		if !ok {
			continue
		}
		remote, ok := parseHexAddrPort(fl[2])
		if !ok {
			continue
		}
		switch proto {
		case ports.ProtoTCP:
			if fl[3] != "0A" { // LISTEN
				continue
			}
		default:
			if remote.Port() != 0 { // connected UDP socket
				continue
			}
		}
		out = append(out, listenSock{proto: proto, addr: local})
	}
	return out
}

// parseHexAddrPort parses the kernel's "0100007F:0277" (IPv4) or 32 hex
// digit (IPv6) address, both stored as host-order (little-endian) 32-bit
// words.
func parseHexAddrPort(s string) (netip.AddrPort, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(b[i:], binary.LittleEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), true
}
