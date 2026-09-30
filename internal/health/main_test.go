package health

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// testServer accepts TCP connections on 127.0.0.1 and runs handle for each
// one. Everything is closed and joined at test cleanup.
func testServer(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return serveListener(t, ln, handle)
}

func serveListener(t *testing.T, ln net.Listener, handle func(net.Conn)) string {
	t.Helper()
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				handle(c)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return ln.Addr().String()
}

// closedPort returns an address where nothing listens.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// selfSigned returns a throwaway server certificate for 127.0.0.1/localhost.
func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsServer completes TLS handshakes and records the SNI it saw.
func tlsServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	sni := make(chan string, 16)
	cfg := &tls.Config{
		Certificates: []tls.Certificate{selfSigned(t)},
		MinVersion:   tls.VersionTLS12,
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			select {
			case sni <- h.ServerName:
			default:
			}
			return nil, nil
		},
	}
	addr := testServer(t, func(c net.Conn) {
		defer c.Close()
		tc := tls.Server(c, cfg)
		_ = tc.SetDeadline(time.Now().Add(5 * time.Second))
		if tc.Handshake() == nil {
			buf := make([]byte, 1)
			_, _ = tc.Read(buf) // wait for the client to go away
		}
	})
	return addr, sni
}

// readSome reads whatever the client sent first (the ClientHello).
func readSome(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	_, _ = c.Read(buf)
}

// alertServer answers any ClientHello with a fatal handshake_failure alert.
func alertServer(t *testing.T) string {
	return testServer(t, func(c net.Conn) {
		defer c.Close()
		readSome(c)
		_, _ = c.Write([]byte{0x15, 0x03, 0x03, 0x00, 0x02, 0x02, 0x28})
		readSome(c)
	})
}

// bannerServer sends an SSH-like banner at once (a non-TLS service).
func bannerServer(t *testing.T) string {
	return testServer(t, func(c net.Conn) {
		defer c.Close()
		_, _ = c.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
		readSome(c)
	})
}

// cleanCloseServer reads the request and closes cleanly without a byte
// (what a reverse-tunnel backend does when the far side is broken).
func cleanCloseServer(t *testing.T) string {
	return testServer(t, func(c net.Conn) {
		readSome(c)
		_ = c.Close()
	})
}

// blackholeServer accepts and never answers.
func blackholeServer(t *testing.T) string {
	return testServer(t, func(c net.Conn) {
		buf := make([]byte, 4096)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	})
}

// resetServer aborts every connection with a TCP RST.
func resetServer(t *testing.T) string {
	return testServer(t, func(c net.Conn) {
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = c.Close()
	})
}
