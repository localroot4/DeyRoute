// Package wsconn is a small, stdlib-only RFC 6455 adapter that turns an
// upgraded WebSocket connection into a plain net.Conn: the bytes written to
// it travel as binary frames and the payload of the binary frames it receives
// comes out of Read. Front mode (a CDN-fronted hub) runs the mTLS + HTTP/2
// control protocol inside that byte stream, so the adapter has to behave like a
// TCP connection for crypto/tls and the HTTP/2 stack:
//
//   - Write splits the data into frames of at most MaxFrame bytes (32 KiB by
//     default); every frame is one write syscall (header and payload share one
//     pooled buffer), FIN is always set, there are no RSV bits and no
//     extensions. Clients mask every frame with a fresh crypto/rand key.
//   - Read is lenient: masked or unmasked frames, fragmented messages,
//     zero-length frames, 7/16/64-bit lengths and control frames interleaved at
//     any point are accepted. RSV bits, unknown opcodes, oversize frames and
//     malformed control frames are protocol errors (close 1002/1009), text
//     frames are refused with 1003. The payload is streamed, a whole message is
//     never buffered, and the read state survives a Read deadline timeout.
//   - A ping is answered without ever blocking Read: the pong is handed to the
//     connection's own goroutine, which writes it between two data frames.
//   - Pings are sent every PingInterval (30 s by default). IdleTimeout, when
//     set, kills a connection only when one of OUR pings was really written and
//     no byte of any frame arrived since, and it never counts time while a
//     write is blocked or while nobody is reading (a slow consumer cannot
//     answer pings it never reads, so its own slowness must not kill it).
//   - Close is idempotent and never blocks: the close frame is best effort
//     (only when the write side is free) and bounded to a fraction of a second.
//   - A received close frame is echoed and Read then returns an error that
//     wraps ErrClosed, ALWAYS (never io.EOF, not even for code 1000): crypto/tls
//     turns an EOF at a record boundary into io.EOF, which would look like a
//     clean close_notify and hide a truncation. The same goes for a TCP
//     connection that ends without a close frame.
//
// SetDeadline, SetReadDeadline and SetWriteDeadline pass straight through to
// the raw connection. *Conn is a pointer type, so it is hashable and can be a
// map key.
//
// The package knows nothing about HTTP: the caller performs the upgrade
// (NewKey, AcceptKey) and hands the connection, plus the bufio.Reader that
// parsed the HTTP message, to New. golang.org/x/net/websocket is used only by
// the tests, as an interoperability peer.
package wsconn
