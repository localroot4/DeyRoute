package front

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/front/fronttest"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

const (
	dataTestPort  = 30123
	dataTestNode  = "de-1"
	dataTestToken = "tunnel-token-0123456789abcdefghijklmnopqrstuvwx" // #nosec G101 -- test value
)

// dataStack is a hub front with a data handler behind the fake CDN, a
// backend server on the hub and a shim on the "node".
type dataStack struct {
	srv     *Server
	cdn     *fronttest.CDN
	backend net.Listener
	shimLn  net.Listener
	cancel  context.CancelFunc
	done    chan error
}

type dataOpts struct {
	shimToken string                      // "" = dataTestToken
	authorize DataAuthorizer              // nil = port, node and token above
	backend   func(c net.Conn)            // nil = echo with half-close
	dial      func(int) (net.Conn, error) // nil = the backend listener
	cdn       func(o *fronttest.Options)  // tweak the CDN
}

// echoHalfClose copies c back to itself and half-closes after the EOF.
func echoHalfClose(c net.Conn) {
	defer func() { _ = c.Close() }()
	_, _ = io.Copy(c, c)
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

func newDataStack(t *testing.T, o dataOpts) *dataStack {
	t.Helper()
	ca, err := tlsutil.NewCA("deyroute test CA", time.Now())
	require.NoError(t, err)
	certPEM, keyPEM, err := ca.IssueServer("hub", []net.IP{net.ParseIP("192.0.2.1")}, nil, 0)
	require.NoError(t, err)
	srvTLS, err := tlsutil.ServerTLSConfig(ca.CertPEM, certPEM, keyPEM)
	require.NoError(t, err)
	cliTLS, err := tlsutil.ClientTLSConfig(ca.CertPEM, nil, nil, "")
	require.NoError(t, err)

	backend, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serve := o.backend
	if serve == nil {
		serve = echoHalfClose
	}
	var bwg sync.WaitGroup
	bwg.Add(1)
	go func() {
		defer bwg.Done()
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			bwg.Add(1)
			go func() { defer bwg.Done(); serve(c) }()
		}
	}()
	t.Cleanup(func() { _ = backend.Close(); bwg.Wait() })

	auth := o.authorize
	if auth == nil {
		auth = func(port int, node string) (string, error) {
			if port != dataTestPort || node != dataTestNode {
				return "", errors.New("not allowed")
			}
			return dataTestToken, nil
		}
	}
	dial := o.dial
	if dial == nil {
		dial = func(int) (net.Conn, error) { return net.Dial("tcp", backend.Addr().String()) }
	}
	dh := NewDataHandler(DataHandlerConfig{
		TLS:       srvTLS,
		Authorize: auth,
		Dial:      func(_ context.Context, port int) (net.Conn, error) { return dial(port) },
	})
	srv := startServer(t, ServerOptions{Secret: testSecret, Data: dh.Serve})

	co := fronttest.Options{OriginAddr: srv.Addr().String(), ClientIP: "203.0.113.7"}
	if o.cdn != nil {
		o.cdn(&co)
	}
	cdn, err := fronttest.NewCDN(co)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cdn.Close() })

	shimLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	token := o.shimToken
	if token == "" {
		token = dataTestToken
	}
	ctx, cancel := context.WithCancel(context.Background())
	ds := &dataStack{srv: srv, cdn: cdn, backend: backend, shimLn: shimLn, cancel: cancel, done: make(chan error, 1)}
	go func() {
		ds.done <- RunShim(ctx, shimLn, ShimConfig{
			Target:   Target{Host: "front.example.com", Port: cdn.Port(), TLS: true, EdgeIP: "127.0.0.1", Secret: testSecret},
			Dialer:   &Dialer{RootCAs: cdn.CAPool(), UpgradeTimeout: 10 * time.Second},
			Port:     dataTestPort,
			Node:     dataTestNode,
			Token:    token,
			InnerTLS: cliTLS,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ds.done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("RunShim did not return after cancel")
		}
	})
	return ds
}

func (d *dataStack) dialShim(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", d.shimLn.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// A backend client's bytes cross the shim, the CDN and the hub's data
// handler intact in both directions, and its half-close comes back as EOF
// after the echo.
func TestDataPlaneEndToEnd(t *testing.T) {
	d := newDataStack(t, dataOpts{})
	for range 3 { // several connections, one after the other and at once
		var wg sync.WaitGroup
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := d.dialShim(t)
				_ = c.SetDeadline(time.Now().Add(30 * time.Second))
				payload := make([]byte, 2<<20)
				_, _ = rand.Read(payload)
				errc := make(chan error, 1)
				go func() {
					_, err := c.Write(payload)
					if err == nil {
						err = c.(*net.TCPConn).CloseWrite()
					}
					errc <- err
				}()
				got, err := io.ReadAll(c)
				assert.NoError(t, err)
				assert.NoError(t, <-errc)
				assert.True(t, bytes.Equal(payload, got), "echo of %d bytes came back as %d bytes", len(payload), len(got))
			}()
		}
		wg.Wait()
	}
	st := d.cdn.Stats()
	assert.GreaterOrEqual(t, st.Upgrades, int64(9), "every local connection is its own WebSocket")
}

// The hub refuses a preface signed with another token: the local connection
// ends without a byte, the backend is never dialled.
func TestDataPlaneWrongToken(t *testing.T) {
	var dialed sync.Mutex
	n := 0
	d := newDataStack(t, dataOpts{
		shimToken: "another-token-0123456789abcdefghijklmnopqrstu",
		dial: func(int) (net.Conn, error) {
			dialed.Lock()
			n++
			dialed.Unlock()
			return nil, errors.New("must not be called")
		},
	})
	c := d.dialShim(t)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	_, _ = c.Write([]byte("hello"))
	b, err := io.ReadAll(c)
	assert.Empty(t, b)
	if err != nil {
		var ne net.Error
		assert.False(t, errors.As(err, &ne) && ne.Timeout(), "the connection must end, not hang: %v", err)
	}
	dialed.Lock()
	defer dialed.Unlock()
	assert.Zero(t, n)
}

// A node may only reach the ports the authorizer gives it.
func TestDataPlaneUnknownPortOrNode(t *testing.T) {
	d := newDataStack(t, dataOpts{authorize: func(int, string) (string, error) { return "", errors.New("no such port") }})
	c := d.dialShim(t)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	b, _ := io.ReadAll(c)
	assert.Empty(t, b)
}

// A backend server that is down is reported to the shim; the local
// connection ends.
func TestDataPlaneBackendDown(t *testing.T) {
	d := newDataStack(t, dataOpts{dial: func(int) (net.Conn, error) { return nil, errors.New("connection refused") }})
	c := d.dialShim(t)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	b, _ := io.ReadAll(c)
	assert.Empty(t, b)
}

// A cut of the CDN in the middle of a transfer resets the local connection
// instead of ending it like a clean EOF.
func TestDataPlaneCutIsNotACleanEnd(t *testing.T) {
	d := newDataStack(t, dataOpts{backend: func(c net.Conn) {
		defer func() { _ = c.Close() }()
		chunk := make([]byte, 32<<10)
		for {
			if _, err := c.Write(chunk); err != nil {
				return
			}
		}
	}})
	c := d.dialShim(t)
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	buf := make([]byte, 64<<10)
	_, err := io.ReadAtLeast(c, buf, 1)
	require.NoError(t, err)
	d.cdn.Configure(func(o *fronttest.Options) { o.DropAll = true })
	require.NoError(t, d.cdn.Close())
	_, err = io.Copy(io.Discard, c)
	require.Error(t, err, "a cut stream must not look like a complete one")
}

func TestPrefaceRoundTripAndChecks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	pre, err := EncodePreface(dataTestToken, dataTestPort, dataTestNode, now)
	require.NoError(t, err)
	p, err := ReadPreface(bytes.NewReader(pre))
	require.NoError(t, err)
	assert.Equal(t, dataTestNode, p.Node)
	assert.True(t, p.Verify(dataTestToken, dataTestPort, now))
	assert.True(t, p.Verify(dataTestToken, dataTestPort, now.Add(PrefaceWindow)))
	assert.False(t, p.Verify(dataTestToken, dataTestPort, now.Add(PrefaceWindow+time.Second)), "too old")
	assert.False(t, p.Verify(dataTestToken, dataTestPort, now.Add(-PrefaceWindow-time.Second)), "from the future")
	assert.False(t, p.Verify("other", dataTestPort, now), "another token")
	assert.False(t, p.Verify(dataTestToken, dataTestPort+1, now), "another port")

	forged := bytes.Clone(pre)
	forged[2] = 'x' // another node id, same MAC
	q, err := ReadPreface(bytes.NewReader(forged))
	require.NoError(t, err)
	assert.False(t, q.Verify(dataTestToken, dataTestPort, now))

	rc := newReplayCache(4)
	assert.True(t, rc.fresh(p.nonce, now))
	assert.False(t, rc.fresh(p.nonce, now), "the same preface twice is a replay")

	_, err = ReadPreface(bytes.NewReader([]byte{2, 1, 'a'}))
	assert.Error(t, err, "unknown version")
	_, err = ReadPreface(bytes.NewReader([]byte{1, 0}))
	assert.Error(t, err, "empty node id")
	_, err = ReadPreface(bytes.NewReader(pre[:len(pre)-1]))
	assert.Error(t, err, "truncated")
	_, err = EncodePreface(dataTestToken, dataTestPort, "", now)
	assert.Error(t, err)
}

func TestDataPortParsing(t *testing.T) {
	for in, want := range map[string]int{
		"t/30000": 30000, "t/1": 1, "t/65535": 65535,
		"t/0": 0, "t/65536": 0, "t/030000": 0, "t/-1": 0, "t/+1": 0, "t/": 0, "t/3a": 0, "c/30000": 0, "t/30000/x": 0, "t": 0,
	} {
		s := append([]string{"secret"}, strings.Split(in, "/")...)
		assert.Equal(t, want, dataPort(s), in)
	}
}

// Without a data handler a data path gets the decoy like any unknown path.
func TestDataPathIsDecoyWithoutHandler(t *testing.T) {
	srv := startServer(t, ServerOptions{Secret: testSecret, TLSMode: "off"})
	resp := rawDo(t, srv.Addr().String(), upgradeReq("/"+testSecret+"/t/30000"), nil)
	assert.Contains(t, string(resp), "404")
}

// The replay cache refuses instead of forgetting when it is full of fresh
// nonces, and forgets nonces older than twice the window.
func TestReplayCacheBounds(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rc := newReplayCache(2)
	var a, b, c [prefaceNonceLen]byte
	a[0], b[0], c[0] = 1, 2, 3
	assert.True(t, rc.fresh(a, now))
	assert.True(t, rc.fresh(b, now))
	assert.False(t, rc.fresh(c, now), "full of fresh nonces")
	assert.True(t, rc.fresh(c, now.Add(2*PrefaceWindow+time.Second)), "old nonces were swept")
}
