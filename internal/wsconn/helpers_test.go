package wsconn

import (
	"encoding/binary"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rawFrame builds the bytes of one frame as a (possibly misbehaving) peer
// would send it.
type rawFrame struct {
	fin     bool
	op      byte
	rsv     byte // RSV bits, already shifted (0x40 = RSV1)
	payload []byte
	mask    bool
	key     [4]byte
	lenForm int // 0 = minimal, 16 or 64 force that extended form
	length  int // when > 0 the length field says this, whatever the payload is
}

func (f rawFrame) bytes() []byte {
	n := len(f.payload)
	if f.length > 0 {
		n = f.length
	}
	var b []byte
	b0 := f.op | f.rsv
	if f.fin {
		b0 |= 0x80
	}
	b = append(b, b0)
	var m byte
	if f.mask {
		m = 0x80
	}
	form := f.lenForm
	if form == 0 {
		switch {
		case n < 126:
			form = 7
		case n <= 0xffff:
			form = 16
		default:
			form = 64
		}
	}
	switch form {
	case 7:
		b = append(b, m|byte(n))
	case 16:
		b = append(b, m|126, byte(n>>8), byte(n))
	default:
		b = append(b, m|127)
		b = binary.BigEndian.AppendUint64(b, uint64(n))
	}
	pl := f.payload
	if f.mask {
		b = append(b, f.key[:]...)
		pl = append([]byte(nil), f.payload...)
		for i := range pl {
			pl[i] ^= f.key[i&3]
		}
	}
	return append(b, pl...)
}

func bin(payload string) rawFrame { return rawFrame{fin: true, op: opBinary, payload: []byte(payload)} }

// readRawFrame reads one frame from a raw peer connection.
func readRawFrame(t testing.TB, c net.Conn) (op byte, fin bool, masked bool, payload []byte) {
	t.Helper()
	var h [2]byte
	_, err := io.ReadFull(c, h[:])
	require.NoError(t, err)
	fin = h[0]&0x80 != 0
	op = h[0] & 0x0f
	masked = h[1]&0x80 != 0
	n := int(h[1] & 0x7f)
	switch n {
	case 126:
		var e [2]byte
		_, err = io.ReadFull(c, e[:])
		require.NoError(t, err)
		n = int(binary.BigEndian.Uint16(e[:]))
	case 127:
		var e [8]byte
		_, err = io.ReadFull(c, e[:])
		require.NoError(t, err)
		n = int(binary.BigEndian.Uint64(e[:]))
	}
	var key [4]byte
	if masked {
		_, err = io.ReadFull(c, key[:])
		require.NoError(t, err)
	}
	payload = make([]byte, n)
	_, err = io.ReadFull(c, payload)
	require.NoError(t, err)
	if masked {
		for i := range payload {
			payload[i] ^= key[i&3]
		}
	}
	return op, fin, masked, payload
}

// rawPair returns a Conn over one end of a net.Pipe and the other end as a raw
// peer. Both are closed at the end of the test.
func rawPair(t testing.TB, cfg Config) (*Conn, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := New(a, nil, cfg)
	t.Cleanup(func() {
		_ = b.Close() // first: Close then has no pipe to wait for
		_ = c.Close()
	})
	return c, b
}

// connPair returns two Conns over a net.Pipe (client masks, server does not).
func connPair(t testing.TB, ccfg, scfg Config) (cli, srv *Conn) {
	t.Helper()
	a, b := net.Pipe()
	ccfg.Client = true
	scfg.Client = false
	cli, srv = New(a, nil, ccfg), New(b, nil, scfg)
	t.Cleanup(func() {
		_ = cli.Close()
		_ = srv.Close()
	})
	return cli, srv
}

// countConn counts the Write calls made on the wrapped connection.
type countConn struct {
	net.Conn
	writes atomic.Int64
	sizes  chan int
}

func (c *countConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	if c.sizes != nil {
		c.sizes <- len(p)
	}
	return c.Conn.Write(p)
}

// fakeConn is a net.Conn that reads from a fixed byte string and discards what
// is written (used by the fuzz target).
type fakeConn struct {
	mu     sync.Mutex
	r      io.Reader
	closed bool
}

func (f *fakeConn) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, net.ErrClosed
	}
	return f.r.Read(p)
}

func (f *fakeConn) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, net.ErrClosed
	}
	return len(p), nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1} }
func (f *fakeConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2} }
func (f *fakeConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error { return nil }

// pattern fills b with a deterministic byte sequence starting at offset off.
func pattern(b []byte, off int) {
	for i := range b {
		b[i] = byte((off + i) * 131 >> 3)
	}
}
