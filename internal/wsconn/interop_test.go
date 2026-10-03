package wsconn

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"
)

// dialUpgrade performs the client side of the HTTP upgrade by hand and returns
// the Conn (client role) plus the status line check.
func dialUpgrade(t *testing.T, addr string, cfg Config) *Conn {
	t.Helper()
	raw, err := net.DialTimeout("tcp", addr, 5*time.Second)
	require.NoError(t, err)
	key := NewKey()
	req := "GET /ws HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nOrigin: http://example.test\r\n\r\n"
	_, err = raw.Write([]byte(req))
	require.NoError(t, err)
	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Equal(t, AcceptKey(key), resp.Header.Get("Sec-WebSocket-Accept"))
	cfg.Client = true
	c := New(raw, br, cfg)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestInteropOurClientAgainstLibraryServer(t *testing.T) {
	// The library echoes every message back as a binary message.
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		for {
			var msg []byte
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			if err := websocket.Message.Send(ws, msg); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c := dialUpgrade(t, strings.TrimPrefix(srv.URL, "http://"), Config{PingInterval: 50 * time.Millisecond})
	for _, size := range []int{1, 125, 126, 5000, 32 << 10, 100000, 600000} {
		msg := make([]byte, size)
		pattern(msg, size)
		werr := make(chan error, 1)
		go func() {
			_, err := c.Write(msg)
			werr <- err
		}()
		got := make([]byte, size)
		_, err := io.ReadFull(c, got)
		require.NoError(t, err, "size %d", size)
		require.True(t, bytes.Equal(msg, got), "size %d", size)
		require.NoError(t, <-werr)
	}

	// The library closes: we see a close frame (ErrClosed), never io.EOF.
	require.NoError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	go func() { _, _ = c.Write([]byte("x")) }()
	// Closing from our side is answered by the library with its own close.
	require.NoError(t, c.Close())
}

func TestInteropLibraryClientAgainstOurServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	served := make(chan error, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			served <- err
			return
		}
		br := bufio.NewReader(raw)
		req, err := http.ReadRequest(br)
		if err != nil {
			served <- err
			return
		}
		resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + AcceptKey(req.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n"
		if _, err := raw.Write([]byte(resp)); err != nil {
			served <- err
			return
		}
		c := New(raw, br, Config{PingInterval: 50 * time.Millisecond, RemoteAddr: raw.RemoteAddr(), Via: "interop"})
		defer func() { _ = c.Close() }()
		_, err = io.Copy(c, c) // echo until the peer closes
		served <- err
	}()

	ws, err := websocket.Dial("ws://"+ln.Addr().String()+"/", "", "http://example.test")
	require.NoError(t, err)
	for _, size := range []int{1, 125, 126, 5000, 65535, 65536, 300000} {
		msg := make([]byte, size)
		pattern(msg, size)
		require.NoError(t, websocket.Message.Send(ws, msg), "size %d", size)
		got := make([]byte, size)
		_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
		_, err := io.ReadFull(ws, got)
		require.NoError(t, err, "size %d", size)
		require.True(t, bytes.Equal(msg, got), "size %d", size)
	}

	// A text message from the library is a protocol violation for us (1003).
	require.NoError(t, websocket.Message.Send(ws, "text message"))
	select {
	case err := <-served:
		require.ErrorIs(t, err, ErrProtocol)
		var pe *ProtocolError
		require.ErrorAs(t, err, &pe)
		require.Equal(t, CloseUnsupported, pe.Code)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not end")
	}
	_ = ws.Close()
}

func TestInteropLibraryClientCloses(t *testing.T) {
	// The library's close handshake: Read on our side returns ErrClosed (code
	// 1000), the echo reaches the library, and nothing is io.EOF.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	res := make(chan error, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			res <- err
			return
		}
		br := bufio.NewReader(raw)
		req, err := http.ReadRequest(br)
		if err != nil {
			res <- err
			return
		}
		_, _ = fmt.Fprintf(raw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
			AcceptKey(req.Header.Get("Sec-WebSocket-Key")))
		c := New(raw, br, Config{})
		defer func() { _ = c.Close() }()
		_, err = c.Read(make([]byte, 16))
		res <- err
	}()
	ws, err := websocket.Dial("ws://"+ln.Addr().String()+"/", "", "http://example.test")
	require.NoError(t, err)
	require.NoError(t, ws.Close())
	select {
	case err := <-res:
		require.ErrorIs(t, err, ErrClosed)
		require.NotErrorIs(t, err, io.EOF)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not see the close")
	}
}
