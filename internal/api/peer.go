package api

import (
	"context"
	"crypto/tls"
	"net"
)

// Peer describes where one control connection came from. ControlServer
// builds it per accepted connection and puts it into the context of every
// request served on it (PeerFrom), so handlers see the same facts whatever
// carried the bytes.
type Peer struct {
	// IP is the client address as the hub should treat it: the host part of
	// the connection's RemoteAddr. A transport that carries the real client
	// address (a CDN front) reports it through RemoteAddr.
	IP string
	// Via names the transport the connection arrived through ("" = direct
	// TCP, for example "front").
	Via string
	// Trusted reports whether IP is the client's real address. It is true for
	// direct TCP connections; a transport that cannot vouch for the address
	// (an edge address instead of the client's) reports false, and the hub
	// must then not store IP as the node's public address.
	Trusted bool
}

// peerConn is implemented by connections that arrive through a transport
// other than plain TCP.
type peerConn interface {
	// Via names the transport ("" when direct).
	Via() string
	// TrustedClientIP reports whether RemoteAddr is the client's real address.
	TrustedClientIP() bool
}

type peerKey struct{}

// ContextWithPeer returns ctx carrying p.
func ContextWithPeer(ctx context.Context, p Peer) context.Context {
	return context.WithValue(ctx, peerKey{}, p)
}

// PeerFrom returns the Peer stored by ContextWithPeer. Without one (a handler
// called outside ControlServer) it returns the zero Peer: no address, not
// trusted.
func PeerFrom(ctx context.Context) Peer {
	p, _ := ctx.Value(peerKey{}).(Peer)
	return p
}

// peerOf builds the Peer of an accepted connection. The optional peerConn
// interface is looked for on conn and, when a TLS listener wrapped it, on the
// connection below the TLS layer.
func peerOf(conn net.Conn) Peer {
	p := Peer{Trusted: true}
	if a := conn.RemoteAddr(); a != nil {
		p.IP = hostOf(a.String())
	}
	var pc peerConn
	for c := conn; c != nil; {
		if v, ok := c.(peerConn); ok {
			pc = v
			break
		}
		t, ok := c.(*tls.Conn)
		if !ok {
			break
		}
		c = t.NetConn()
	}
	if pc != nil {
		p.Via = pc.Via()
		p.Trusted = pc.TrustedClientIP()
	}
	return p
}

// hostOf returns the host part of "host:port"; anything else unchanged.
func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
