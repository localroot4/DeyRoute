package health

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const shortTimeout = 300 * time.Millisecond

func TestPathAutoTLSServer(t *testing.T) {
	addr, _ := tlsServer(t)
	r := Path(context.Background(), addr, KindAuto, time.Second, PathOptions{})
	require.True(t, r.OK, r.Err)
	assert.True(t, r.TLS)
	assert.False(t, r.ClosedNoData)
	assert.Positive(t, r.RTT)
	assert.Empty(t, r.Err)

	// An empty kind means auto.
	r = Path(context.Background(), addr, "", time.Second, PathOptions{})
	assert.True(t, r.OK, r.Err)
	assert.True(t, r.TLS)
}

func TestPathAutoAlertIsSuccess(t *testing.T) {
	addr := alertServer(t)
	r := Path(context.Background(), addr, KindAuto, time.Second, PathOptions{})
	require.True(t, r.OK, r.Err)
	assert.True(t, r.TLS)
	assert.Positive(t, r.RTT)
}

func TestPathAutoNonTLSBytesAreSuccess(t *testing.T) {
	addr := bannerServer(t)
	r := Path(context.Background(), addr, KindAuto, time.Second, PathOptions{})
	require.True(t, r.OK, r.Err)
	assert.False(t, r.TLS)
	assert.Positive(t, r.RTT)
}

func TestPathAutoCleanClose(t *testing.T) {
	addr := cleanCloseServer(t)

	r := Path(context.Background(), addr, KindAuto, time.Second, PathOptions{})
	assert.False(t, r.OK, "a clean close must not count by default (reverse tunnel)")
	assert.True(t, r.ClosedNoData)
	assert.Equal(t, ReasonClosedNoData, r.Err)

	r = Path(context.Background(), addr, KindAuto, time.Second, PathOptions{AcceptCleanClose: true})
	assert.True(t, r.OK, r.Err)
	assert.True(t, r.ClosedNoData)
	assert.Positive(t, r.RTT, "RTT is the connect time")
	assert.Empty(t, r.Err)
}

func TestPathAutoTimeout(t *testing.T) {
	addr := blackholeServer(t)
	start := time.Now()
	r := Path(context.Background(), addr, KindAuto, shortTimeout, PathOptions{AcceptCleanClose: true})
	assert.False(t, r.OK)
	assert.Equal(t, ReasonTimeout, r.Err)
	assert.Less(t, time.Since(start), 3*time.Second)
}

func TestPathRefused(t *testing.T) {
	addr := closedPort(t)
	for _, kind := range []string{KindAuto, KindTCP, KindTLS, KindHTTP} {
		r := Path(context.Background(), addr, kind, time.Second, PathOptions{AcceptCleanClose: true})
		assert.False(t, r.OK, kind)
		assert.Equal(t, ReasonRefused, r.Err, kind)
	}
}

func TestPathReset(t *testing.T) {
	addr := resetServer(t)
	for i := 0; i < 3; i++ {
		r := Path(context.Background(), addr, KindAuto, time.Second, PathOptions{AcceptCleanClose: true})
		assert.False(t, r.OK)
		assert.Contains(t, []string{ReasonReset, ReasonClosedNoData}, r.Err)
	}
}

func TestPathTLSKind(t *testing.T) {
	addr, sni := tlsServer(t)
	r := TLS(context.Background(), addr, "example.test", time.Second)
	require.True(t, r.OK, r.Err)
	assert.True(t, r.TLS)
	select {
	case got := <-sni:
		assert.Equal(t, "example.test", got)
	case <-time.After(2 * time.Second):
		t.Fatal("server saw no ClientHello")
	}

	// Alert = a TLS server answered.
	r = TLS(context.Background(), alertServer(t), "", time.Second)
	assert.True(t, r.OK, r.Err)
	assert.True(t, r.TLS)

	// Non-TLS bytes fail a strict TLS probe.
	r = TLS(context.Background(), bannerServer(t), "", time.Second)
	assert.False(t, r.OK)
	assert.Equal(t, ReasonNotTLS, r.Err)

	// EOF before any data fails, whatever AcceptCleanClose says.
	r = Path(context.Background(), cleanCloseServer(t), KindTLS, time.Second, PathOptions{AcceptCleanClose: true})
	assert.False(t, r.OK)
	assert.True(t, r.ClosedNoData)
	assert.Equal(t, ReasonClosedNoData, r.Err)

	r = TLS(context.Background(), blackholeServer(t), "", shortTimeout)
	assert.False(t, r.OK)
	assert.Equal(t, ReasonTimeout, r.Err)
}

func TestPathAutoSNIFromHostName(t *testing.T) {
	addr, sni := tlsServer(t)
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	r := Path(context.Background(), net.JoinHostPort("localhost", port), KindAuto, time.Second, PathOptions{})
	if !r.OK {
		t.Skipf("localhost does not resolve to 127.0.0.1 here: %s", r.Err)
	}
	assert.Equal(t, "localhost", <-sni)

	// IP addresses send no SNI.
	r = Path(context.Background(), addr, KindAuto, time.Second, PathOptions{})
	require.True(t, r.OK, r.Err)
	assert.Equal(t, "", <-sni)
}

func TestPathTCPKind(t *testing.T) {
	r := TCP(context.Background(), blackholeServer(t), time.Second)
	require.True(t, r.OK, r.Err)
	assert.Positive(t, r.RTT)
	assert.False(t, r.TLS)

	r = TCP(context.Background(), closedPort(t), time.Second)
	assert.False(t, r.OK)
	assert.Equal(t, ReasonRefused, r.Err)
}

func TestPathHTTPKind(t *testing.T) {
	type seen struct{ host, method, path string }
	reqs := make(chan seen, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs <- seen{r.Host, r.Method, r.URL.Path}
		w.WriteHeader(http.StatusNotFound) // any status line is fine
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	r := Path(context.Background(), addr, KindHTTP, time.Second, PathOptions{})
	require.True(t, r.OK, r.Err)
	assert.Positive(t, r.RTT)
	assert.Equal(t, seen{addr, http.MethodHead, "/"}, <-reqs)

	r = Path(context.Background(), addr, KindHTTP, time.Second, PathOptions{SNI: "panel.example", HTTPPath: "/health"})
	require.True(t, r.OK, r.Err)
	assert.Equal(t, seen{"panel.example", http.MethodHead, "/health"}, <-reqs)

	r = Path(context.Background(), bannerServer(t), KindHTTP, time.Second, PathOptions{})
	assert.False(t, r.OK)
	assert.Equal(t, ReasonNotHTTP, r.Err)

	r = Path(context.Background(), alertServer(t), KindHTTP, time.Second, PathOptions{})
	assert.False(t, r.OK)
	assert.True(t, r.TLS)

	r = Path(context.Background(), cleanCloseServer(t), KindHTTP, time.Second, PathOptions{})
	assert.False(t, r.OK)
	assert.True(t, r.ClosedNoData)

	r = Path(context.Background(), blackholeServer(t), KindHTTP, shortTimeout, PathOptions{})
	assert.False(t, r.OK)
	assert.Equal(t, ReasonTimeout, r.Err)

	for _, bad := range []PathOptions{{HTTPPath: "no-slash"}, {HTTPPath: "/a b"}, {SNI: "a\r\nX-Evil: 1"}} {
		r = Path(context.Background(), addr, KindHTTP, time.Second, bad)
		assert.False(t, r.OK)
		assert.Equal(t, "invalid http request", r.Err)
	}
}

func TestHTTPFunc(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	r := HTTP(context.Background(), srv.URL+"/x?y=1", time.Second)
	assert.True(t, r.OK, r.Err)
	assert.False(t, r.TLS)

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer tlsSrv.Close()
	r = HTTP(context.Background(), tlsSrv.URL, time.Second)
	assert.True(t, r.OK, r.Err)
	assert.True(t, r.TLS)

	// https:// against a plain HTTP server: the handshake fails.
	r = HTTP(context.Background(), strings.Replace(srv.URL, "http://", "https://", 1), time.Second)
	assert.False(t, r.OK)
	assert.Equal(t, ReasonNotTLS, r.Err)

	// https:// against a server that closes at once.
	r = HTTP(context.Background(), "https://"+cleanCloseServer(t), time.Second)
	assert.False(t, r.OK)
	assert.True(t, r.ClosedNoData)

	// https:// against a TLS alert.
	r = HTTP(context.Background(), "https://"+alertServer(t), time.Second)
	assert.False(t, r.OK)
	assert.True(t, strings.HasPrefix(r.Err, "tls: "), r.Err)
	assert.True(t, r.TLS)

	for _, bad := range []string{"ftp://x:1", "://", "http://", "no-scheme"} {
		r = HTTP(context.Background(), bad, 0)
		assert.False(t, r.OK, bad)
		assert.Equal(t, ReasonBadAddress, r.Err, bad)
	}
	r = HTTP(context.Background(), "http://"+closedPort(t), time.Second)
	assert.Equal(t, ReasonRefused, r.Err)
}

func TestHTTPFuncDefaultPorts(t *testing.T) {
	// Only checks that default ports are filled in; nothing listens on 1.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, ReasonCanceled, HTTP(ctx, "http://127.0.0.1", time.Second).Err)
	assert.Equal(t, ReasonCanceled, HTTP(ctx, "https://127.0.0.1", time.Second).Err)
}

func TestPathBadInput(t *testing.T) {
	r := Path(context.Background(), "127.0.0.1:1", "icmp", time.Second, PathOptions{})
	assert.Equal(t, ReasonBadKind, r.Err)
	for _, a := range []string{"", "127.0.0.1", "127.0.0.1:", "[::1"} {
		r = Path(context.Background(), a, KindTCP, time.Second, PathOptions{})
		assert.Equal(t, ReasonBadAddress, r.Err, a)
		assert.Equal(t, ReasonBadAddress, TCPEcho(context.Background(), a, time.Second).Err)
		assert.Equal(t, ReasonBadAddress, UDPEcho(context.Background(), a, 1, time.Second).Err)
	}
}

func TestPathCanceled(t *testing.T) {
	addr := blackholeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	r := Path(ctx, addr, KindAuto, 5*time.Second, PathOptions{})
	assert.False(t, r.OK)
	assert.Equal(t, ReasonCanceled, r.Err)
	assert.Less(t, time.Since(start), 2*time.Second)

	ctx2, cancel2 := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel2)
	r = Path(ctx2, addr, KindHTTP, 5*time.Second, PathOptions{})
	assert.Equal(t, ReasonCanceled, r.Err)
}

func TestPathDefaultTimeout(t *testing.T) {
	addr, _ := tlsServer(t)
	r := Path(context.Background(), addr, KindAuto, 0, PathOptions{})
	assert.True(t, r.OK, r.Err)
}

func TestTCPEcho(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeTCPEcho(ctx, ln) }()
	defer func() {
		cancel()
		require.NoError(t, <-done)
	}()

	r := TCPEcho(context.Background(), ln.Addr().String(), time.Second)
	require.True(t, r.OK, r.Err)
	assert.Positive(t, r.RTT)

	// A service that answers something else.
	r = TCPEcho(context.Background(), bannerServer(t), time.Second)
	assert.False(t, r.OK)
	assert.Equal(t, ReasonEchoMismatch, r.Err)

	short := testServer(t, func(c net.Conn) {
		readSome(c)
		_, _ = c.Write([]byte(EchoMagic))
		_ = c.Close()
	})
	r = TCPEcho(context.Background(), short, time.Second)
	assert.Equal(t, ReasonEchoMismatch, r.Err)

	r = TCPEcho(context.Background(), cleanCloseServer(t), time.Second)
	assert.False(t, r.OK)
	assert.True(t, r.ClosedNoData)

	r = TCPEcho(context.Background(), blackholeServer(t), shortTimeout)
	assert.Equal(t, ReasonTimeout, r.Err)

	r = TCPEcho(context.Background(), closedPort(t), 0)
	assert.Equal(t, ReasonRefused, r.Err)
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{context.Canceled, ReasonCanceled},
		{context.DeadlineExceeded, ReasonTimeout},
		{fmt.Errorf("read: %w", os.ErrDeadlineExceeded), ReasonTimeout},
		{&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, ReasonRefused},
		{&net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, ReasonReset},
		{&net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.EPIPE)}, ReasonReset},
		{&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}, ReasonUnreachable},
		{&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}, ReasonUnreachable},
		{io.EOF, "connection closed"},
		{&net.DNSError{Err: "no such host", Name: "x.invalid"}, "dns: no such host"},
		{&net.OpError{Op: "read", Err: timeoutErr{}}, ReasonTimeout},
		{&net.OpError{Op: "remote error", Err: errors.New("tls: handshake failure")}, "tls: handshake failure"},
		{errors.New(strings.Repeat("x", 200)), strings.Repeat("x", 120)},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Classify(c.err), "%v", c.err)
	}
}

func TestHelpers(t *testing.T) {
	assert.True(t, isHTTPStatusLine([]byte("HTTP/1.1 200 OK\r\n")))
	assert.True(t, isHTTPStatusLine([]byte("HTTP/1.0 404\r\n")))
	assert.False(t, isHTTPStatusLine([]byte("HTTP/1.1 2000 OK")))
	assert.False(t, isHTTPStatusLine([]byte("HTTP/1.1 2x0 OK")))
	assert.False(t, isHTTPStatusLine([]byte("SSH-2.0-OpenSSH")))
	assert.False(t, isHTTPStatusLine(nil))
	assert.True(t, isTLSRecordType(0x15))
	assert.True(t, isTLSRecordType(0x16))
	assert.False(t, isTLSRecordType('H'))
	p := newEchoPacket()
	q := newEchoPacket()
	assert.Equal(t, EchoMagic, string(p[:4]))
	assert.NotEqual(t, p, q)
	assert.Equal(t, "", probeTLSConfig("", "1.2.3.4:443").ServerName)
	assert.Equal(t, "a.example", probeTLSConfig("", "a.example:443").ServerName)
	assert.Equal(t, "b.example", probeTLSConfig("b.example", "a.example:443").ServerName)
}
