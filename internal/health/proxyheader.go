package health

import (
	"bufio"
	"net"
)

// PROXY protocol v1 (haproxy's proxy-protocol.txt): one line
// "PROXY TCP4 <src> <dst> <sport> <dport>\r\n" of at most 107 bytes before
// the data. frp sends one to every local target of a tunnel with
// advanced.proxy_protocol (DR-21), the hidden probe echo map included.
const (
	proxyV1Prefix = "PROXY "
	proxyV1MaxLen = 107
)

// headerConn reads through r, which may hold bytes already taken from Conn.
type headerConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *headerConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// skipProxyHeader returns a conn that reads c after a leading PROXY v1
// header, so the helpers behind a tunnel with advanced.proxy_protocol
// answer as without it. Without a header nothing is skipped; the caller's
// read deadline bounds the wait for the first bytes. A header longer than
// the protocol allows is dropped up to its first 107 bytes; what follows
// then fails the helper's own check.
func skipProxyHeader(c net.Conn) net.Conn {
	br := bufio.NewReaderSize(c, 2*proxyV1MaxLen)
	hc := &headerConn{Conn: c, r: br}
	for n := 1; n <= len(proxyV1Prefix); n++ {
		b, err := br.Peek(n)
		if err != nil || string(b) != proxyV1Prefix[:n] {
			return hc
		}
	}
	for range proxyV1MaxLen {
		ch, err := br.ReadByte()
		if err != nil || ch == '\n' {
			return hc
		}
	}
	return hc
}
