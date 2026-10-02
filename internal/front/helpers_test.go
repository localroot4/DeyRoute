package front

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/front/fronttest"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// testSecret is a path secret of the shape secrets.NewToken produces.
const testSecret = "Zq3-vK9_mT2xWc7LpR5nBd8HfJ4sGa6Y"

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// debugLogger logs everything, including debug lines, into the returned buffer.
func debugLogger() (*slog.Logger, *syncBuf) {
	buf := &syncBuf{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// startServer binds 127.0.0.1:0 and starts a Server (secret defaults to
// testSecret). Nothing calls Accept: use acceptEcho or call it directly.
func startServer(t *testing.T, o ServerOptions) *Server {
	t.Helper()
	if o.Secret == "" {
		o.Secret = testSecret
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv, err := NewServer(ln, o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// acceptor accepts upgraded connections, echoes everything back on each one
// (a stand-in for the control server) and hands the connections to the test.
type acceptor struct {
	srv   *Server
	ch    chan *Conn
	wg    sync.WaitGroup
	mu    sync.Mutex
	conns []net.Conn
}

func acceptEcho(t *testing.T, srv *Server) *acceptor {
	t.Helper()
	a := &acceptor{srv: srv, ch: make(chan *Conn, 256)}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			c, err := srv.Accept()
			if err != nil {
				return
			}
			fc := c.(*Conn)
			a.mu.Lock()
			a.conns = append(a.conns, fc)
			a.mu.Unlock()
			select {
			case a.ch <- fc:
			default:
			}
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				defer fc.Close()
				_, _ = io.Copy(fc, fc)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		a.mu.Lock()
		for _, c := range a.conns {
			_ = c.Close()
		}
		a.mu.Unlock()
		a.wg.Wait()
	})
	return a
}

// next waits for the next accepted connection.
func (a *acceptor) next(t *testing.T) *Conn {
	t.Helper()
	select {
	case c := <-a.ch:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("no connection was accepted")
		return nil
	}
}

// stackOpts configure newStack.
type stackOpts struct {
	server ServerOptions
	cdn    fronttest.Options
}

// stack is a complete path: node dialer -> fake CDN -> front server.
type stack struct {
	srv    *Server
	acc    *acceptor
	cdn    *fronttest.CDN
	target Target
	dialer *Dialer
}

func newStack(t *testing.T, so stackOpts) *stack {
	t.Helper()
	srv := startServer(t, so.server)
	acc := acceptEcho(t, srv)
	so.cdn.OriginAddr = srv.Addr().String()
	if so.cdn.ClientIP == "" {
		// The test CDN is on loopback, whose address a front never accepts as a
		// client address, so give the "client" a public one.
		so.cdn.ClientIP = "203.0.113.7"
	}
	cdn, err := fronttest.NewCDN(so.cdn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cdn.Close() })
	tgt := Target{Host: "front.example.com", Port: cdn.Port(), TLS: !so.cdn.PlainClients, EdgeIP: "127.0.0.1", Secret: testSecret}
	return &stack{srv: srv, acc: acc, cdn: cdn, target: tgt, dialer: &Dialer{RootCAs: cdn.CAPool(), UpgradeTimeout: 10 * time.Second}}
}

// dial opens a control connection and registers its Close.
func (s *stack) dial(t *testing.T) net.Conn {
	t.Helper()
	c, err := s.dialer.DialControl(context.Background(), s.target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// echoOnce writes msg and reads it back.
func echoOnce(t *testing.T, c net.Conn, msg []byte) {
	t.Helper()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	defer func() { _ = c.SetDeadline(time.Time{}) }()
	_, err := c.Write(msg)
	require.NoError(t, err)
	got := make([]byte, len(msg))
	_, err = io.ReadFull(c, got)
	require.NoError(t, err)
	require.True(t, bytes.Equal(msg, got), "echo mismatch")
}

var dateRe = regexp.MustCompile(`Date: [^\r]*\r\n`)

// stripDate removes the Date header so two answers can be compared.
func stripDate(b []byte) []byte { return dateRe.ReplaceAll(b, nil) }

// rawDo sends req to addr (over TLS when cfg != nil) and returns everything
// the server answers until it closes the connection.
func rawDo(t *testing.T, addr, req string, cfg *tls.Config) []byte {
	t.Helper()
	conn := rawConn(t, addr, cfg)
	defer conn.Close()
	_, err := io.WriteString(conn, req)
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(conn)
	if err != nil {
		// A reset after the answer is fine: what arrived is what counts.
		t.Logf("read ended with %v", err)
	}
	return b
}

func rawConn(t *testing.T, addr string, cfg *tls.Config) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	require.NoError(t, err)
	if cfg == nil {
		return c
	}
	tc := tls.Client(c, cfg)
	require.NoError(t, tc.Handshake())
	return tc
}

// insecureTLS is the client configuration for the self-signed outer
// certificate in tests that are not about verification.
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} // #nosec G402 -- test client for a self-signed throw-away certificate
}

// upgradeReq renders a client upgrade request for path.
func upgradeReq(path string, extra ...string) string {
	r := "GET " + path + " HTTP/1.1\r\nHost: front.example.com\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
	for _, e := range extra {
		r += e + "\r\n"
	}
	return r + "\r\n"
}

func portOf(a net.Addr) int {
	_, p, _ := net.SplitHostPort(a.String())
	n, _ := strconv.Atoi(p)
	return n
}
