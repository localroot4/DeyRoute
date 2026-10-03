// Package front implements both ends of front mode: a hub that is reachable
// only through a CDN (Cloudflare) because the direct path from the node is
// cut.
//
// Node side (target.go, dial.go, outertls.go): DialControl opens a TCP
// connection to the CDN edge, runs the OUTER TLS handshake (ServerName = the
// front domain, system roots, http/1.1 only), performs a browser-like
// WebSocket upgrade of GET /<secret>/c and returns the connection as a
// net.Conn whose bytes are the payload of binary WebSocket frames
// (internal/wsconn). The control protocol (mTLS plus HTTP/2) runs inside that
// byte stream, so the CDN only ever sees ciphertext. Every failure is a
// *DialError that knows its class, an HTTP status, a retry hint and whether
// retrying is pointless, and converts to the DEY-N016 / DEY-N017 catalog
// errors.
//
// Hub side (server.go, decoy.go, trusted.go, cert.go, limits.go): NewServer
// is an accept loop over a listener the caller bound. It terminates the outer
// TLS itself (an independent self-signed ECDSA certificate, an operator
// certificate, or plain HTTP, and in mode auto it sniffs the first byte so
// one port serves both Cloudflare's "Full" and "Flexible" modes), parses one
// capped HTTP request per connection and upgrades exactly GET /<secret>/c.
// Everything else gets the decoy: the same few bytes for a wrong path, a
// wrong secret, a missing upgrade or a bad method, so the port looks like an
// ordinary web server to a scanner. The path secret is a gate against
// scanners, not authentication; the inner TLS is. The real client address is
// taken from CF-Connecting-IP only when the TCP peer is loopback, a
// Cloudflare range (internal/cfnets) or a configured trusted proxy.
// FanIn merges the front's accepted connections with other listeners (the
// direct control port) into one net.Listener.
//
// The package must not import internal/api or any daemon package. The secret
// never appears in an error string or a log line.
package front
