// Package fronttest is test support for front mode: a fake Cloudflare edge
// (CDN) and a byte-level cut proxy. It is a leaf package (standard library
// and internal/wsconn only; it must not import internal/front or
// internal/api) so every test that needs a CDN can use it.
//
// The constructors take no testing.TB so a stand-alone fake edge binary can
// use them too; every goroutine and connection they start is tracked and
// joined by Close.
//
// The CDN is a TLS-terminating (or plain) HTTP/1.1 reverse proxy specialised
// for WebSocket upgrades. It parses the client's request itself, drops
// Sec-WebSocket-Extensions like the real edge, adds CF-Connecting-IP and the
// X-Forwarded headers, forwards the request to the origin as an HTTP/1.1
// client and, after a 101, pumps bytes both ways. Knobs model the behaviours
// of the real edge that front mode has to survive: re-chunking, latency,
// bandwidth limits, a stalled origin, an idle cut that pings do not reset,
// swallowed pings, a cut of one direction after N bytes, and the canned
// answers a misconfigured zone gives (redirect, challenge, rate limit, 52x,
// 530).
package fronttest
