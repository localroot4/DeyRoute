package direct

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/localroot4/deyroute/internal/config"
)

// udpBufLen fits one framed datagram plus one byte to detect oversized
// payloads.
const udpBufLen = udpOverhead + maxUDPPayload + 1

// janitorInterval is how often idle UDP sessions are swept.
func janitorInterval(idle time.Duration) time.Duration {
	return min(max(idle/4, 10*time.Millisecond), 15*time.Second)
}

// ---------------------------------------------------------------- hub

// hubUDP relays datagrams from user clients on the hub's UDP listen ports
// to the node relay over one connected UDP socket.
type hubUDP struct {
	r        *relay
	up       *net.UDPConn
	idle     time.Duration
	limit    int
	mu       sync.Mutex
	byClient map[clientKey]*hubSession
	byID     map[uint64]*hubSession
}

type clientKey struct {
	index int
	addr  netip.AddrPort
}

type hubSession struct {
	id   uint64
	key  clientKey
	ln   *net.UDPConn
	last atomic.Int64 // relay.mono() of the last datagram
}

func newHubUDP(r *relay, up *net.UDPConn) *hubUDP {
	return &hubUDP{
		r: r, up: up,
		idle:     r.cfg.UDPIdleTimeout(),
		limit:    r.cfg.UDPSessionLimit(),
		byClient: map[clientKey]*hubSession{},
		byID:     map[uint64]*hubSession{},
	}
}

// serveListener reads user datagrams of port map index and forwards them.
func (h *hubUDP) serveListener(ln *net.UDPConn, index int) {
	m := newMacer(h.r.key)
	buf := make([]byte, udpBufLen)
	for {
		n, addr, err := ln.ReadFromUDPAddrPort(buf[udpHeaderLen : udpHeaderLen+maxUDPPayload+1])
		if err != nil {
			if h.r.isStopping() || errors.Is(err, net.ErrClosed) {
				return
			}
			if !h.r.sleep(10 * time.Millisecond) {
				return
			}
			continue
		}
		if n > maxUDPPayload {
			h.r.warn.log(h.r.log, "udp-size", "relay: dropped an oversized UDP datagram", "max", maxUDPPayload)
			continue
		}
		s := h.session(index, addr, ln)
		if s == nil {
			continue
		}
		if _, err := h.up.Write(sealUDP(m, dirToNode, buf, index, s.id, n)); err != nil && !isRefused(err) {
			h.r.warn.log(h.r.log, "udp-up", "relay: cannot send to the node relay", "addr", h.r.cfg.Node, "err", err.Error())
		}
	}
}

// session returns (creating when needed) the session of a user client.
func (h *hubUDP) session(index int, addr netip.AddrPort, ln *net.UDPConn) *hubSession {
	now := h.r.mono()
	k := clientKey{index: index, addr: addr}
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.byClient[k]; ok {
		s.last.Store(now)
		return s
	}
	if len(h.byID) >= h.limit {
		h.r.warn.log(h.r.log, "udp-limit", "relay: UDP session limit reached; new clients are dropped until sessions expire", "limit", h.limit)
		return nil
	}
	var id uint64
	for id == 0 || h.byID[id] != nil {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil
		}
		id = binary.BigEndian.Uint64(b[:])
	}
	s := &hubSession{id: id, key: k, ln: ln}
	s.last.Store(now)
	h.byClient[k] = s
	h.byID[id] = s
	return s
}

// readUpstream delivers the node's replies to the user clients.
func (h *hubUDP) readUpstream() {
	m := newMacer(h.r.key)
	buf := make([]byte, udpBufLen)
	for {
		n, err := h.up.Read(buf)
		if err != nil {
			if h.r.isStopping() || errors.Is(err, net.ErrClosed) {
				return
			}
			// ECONNREFUSED: the node relay is not (yet) listening.
			if !h.r.sleep(10 * time.Millisecond) {
				return
			}
			continue
		}
		index, id, payload, ok := openUDP(m, dirToHub, buf[:n])
		if !ok || len(payload) > maxUDPPayload {
			h.r.warn.log(h.r.log, "udp-auth", "relay: dropped an unauthenticated UDP reply")
			continue
		}
		h.mu.Lock()
		s := h.byID[id]
		h.mu.Unlock()
		if s == nil || s.key.index != index {
			continue
		}
		s.last.Store(h.r.mono())
		_, _ = s.ln.WriteToUDPAddrPort(payload, s.key.addr)
	}
}

// janitor expires idle sessions.
func (h *hubUDP) janitor() {
	t := time.NewTicker(janitorInterval(h.idle))
	defer t.Stop()
	for {
		select {
		case <-h.r.ctx.Done():
			return
		case <-t.C:
			cutoff := h.r.mono() - int64(h.idle)
			h.mu.Lock()
			for id, s := range h.byID {
				if s.last.Load() < cutoff {
					delete(h.byID, id)
					delete(h.byClient, s.key)
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *hubUDP) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.byClient = map[clientKey]*hubSession{}
	h.byID = map[uint64]*hubSession{}
}

func (h *hubUDP) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byID)
}

// ---------------------------------------------------------------- node

// nodeUDP authenticates datagrams from the hub relay and forwards them to
// the configured UDP targets, one connected socket per session.
type nodeUDP struct {
	r        *relay
	ln       *net.UDPConn
	idle     time.Duration
	limit    int
	mu       sync.Mutex
	closed   bool
	sessions map[nodeKey]*nodeSession
	// owners binds every session id to the hub address that used it, for
	// ReplayWindow: a captured datagram replayed from another (spoofed)
	// address must not open a session whose replies go to that address.
	owners map[uint64]sidOwner
}

// maxSIDOwners bounds the owners map (authenticated traffic only).
const maxSIDOwners = 1 << 16

type sidOwner struct {
	peer netip.AddrPort
	at   int64 // relay.mono() of the last datagram
}

type nodeKey struct {
	peer netip.AddrPort
	id   uint64
}

type nodeSession struct {
	key   nodeKey
	index int
	conn  *net.UDPConn
	last  atomic.Int64
}

func newNodeUDP(r *relay, ln *net.UDPConn) *nodeUDP {
	return &nodeUDP{
		r: r, ln: ln,
		idle:     r.cfg.UDPIdleTimeout(),
		limit:    r.cfg.UDPSessionLimit(),
		sessions: map[nodeKey]*nodeSession{},
		owners:   map[uint64]sidOwner{},
	}
}

// serve reads framed datagrams from the hub relay.
func (n *nodeUDP) serve() {
	m := newMacer(n.r.key)
	buf := make([]byte, udpBufLen)
	for {
		size, peer, err := n.ln.ReadFromUDPAddrPort(buf)
		if err != nil {
			if n.r.isStopping() || errors.Is(err, net.ErrClosed) {
				return
			}
			if !n.r.sleep(10 * time.Millisecond) {
				return
			}
			continue
		}
		index, id, payload, ok := openUDP(m, dirToNode, buf[:size])
		if !ok {
			n.r.warn.log(n.r.log, "udp-auth", "relay: dropped a UDP datagram that failed authentication (wrong token or not a deyroute hub)", "remote", peer.String())
			continue
		}
		if len(payload) > maxUDPPayload {
			continue
		}
		s := n.session(peer, id, index)
		if s == nil {
			continue
		}
		s.last.Store(n.r.mono())
		if _, err := s.conn.Write(payload); err != nil && !isRefused(err) {
			n.r.warn.log(n.r.log, "udp-target", "relay: cannot send to the UDP target", "err", err.Error())
		}
	}
}

// session returns the session of (peer, id), creating it for a configured
// UDP target. It never dials anything but the configured targets.
func (n *nodeUDP) session(peer netip.AddrPort, id uint64, index int) *nodeSession {
	k := nodeKey{peer: peer, id: id}
	now := n.r.mono()
	n.mu.Lock()
	if o, ok := n.owners[id]; ok && o.peer != peer && now-o.at < int64(ReplayWindow) {
		n.mu.Unlock()
		n.r.warn.log(n.r.log, "udp-replay", "relay: dropped a UDP datagram replayed from another address", "remote", peer.String())
		return nil
	}
	if _, ok := n.owners[id]; ok || len(n.owners) < maxSIDOwners {
		n.owners[id] = sidOwner{peer: peer, at: now}
	}
	if s, ok := n.sessions[k]; ok {
		n.mu.Unlock()
		if s.index != index {
			return nil
		}
		return s
	}
	full := len(n.sessions) >= n.limit
	n.mu.Unlock()
	if full {
		n.r.warn.log(n.r.log, "udp-limit", "relay: UDP session limit reached; new sessions are dropped until others expire", "limit", n.limit)
		return nil
	}
	target, ok := n.r.target(index, config.ProtoUDP)
	if !ok {
		n.r.warn.log(n.r.log, "index", "relay: hub asked for an unknown udp target; hub and node configs differ (re-render the tunnel)", "index", index)
		return nil
	}
	c, err := n.r.dialer.DialContext(n.r.ctx, "udp", target)
	if err != nil {
		n.r.warn.log(n.r.log, "dial-target:"+target, "relay cannot open a socket to the UDP target", "target", target, "err", err.Error())
		return nil
	}
	s := &nodeSession{key: k, index: index, conn: c.(*net.UDPConn)}
	s.last.Store(n.r.mono())
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		_ = c.Close()
		return nil
	}
	n.sessions[k] = s
	n.r.wg.Add(1)
	n.mu.Unlock()
	go func() {
		defer n.r.wg.Done()
		n.readTarget(s)
	}()
	return s
}

// readTarget relays the target's replies of one session back to the hub.
func (n *nodeUDP) readTarget(s *nodeSession) {
	m := newMacer(n.r.key)
	buf := make([]byte, udpBufLen)
	for {
		size, err := s.conn.Read(buf[udpHeaderLen : udpHeaderLen+maxUDPPayload+1])
		if err != nil {
			if isRefused(err) {
				continue // ICMP port unreachable from the target; keep the session
			}
			n.remove(s)
			return
		}
		if size > maxUDPPayload {
			continue
		}
		s.last.Store(n.r.mono())
		_, _ = n.ln.WriteToUDPAddrPort(sealUDP(m, dirToHub, buf, s.index, s.key.id, size), s.key.peer)
	}
}

// remove forgets s and closes its socket.
func (n *nodeUDP) remove(s *nodeSession) {
	n.mu.Lock()
	if cur, ok := n.sessions[s.key]; ok && cur == s {
		delete(n.sessions, s.key)
	}
	n.mu.Unlock()
	_ = s.conn.Close()
}

// janitor expires idle sessions (closing the socket ends its reader).
func (n *nodeUDP) janitor() {
	t := time.NewTicker(janitorInterval(n.idle))
	defer t.Stop()
	for {
		select {
		case <-n.r.ctx.Done():
			return
		case <-t.C:
			now := n.r.mono()
			cutoff := now - int64(n.idle)
			ownerCutoff := now - int64(ReplayWindow)
			var expired []*nodeSession
			n.mu.Lock()
			for k, s := range n.sessions {
				if s.last.Load() < cutoff {
					delete(n.sessions, k)
					expired = append(expired, s)
				}
			}
			for id, o := range n.owners {
				if o.at < ownerCutoff {
					delete(n.owners, id)
				}
			}
			n.mu.Unlock()
			for _, s := range expired {
				_ = s.conn.Close()
			}
		}
	}
}

func (n *nodeUDP) closeAll() {
	n.mu.Lock()
	n.closed = true
	sessions := n.sessions
	n.sessions = map[nodeKey]*nodeSession{}
	n.mu.Unlock()
	for _, s := range sessions {
		_ = s.conn.Close()
	}
}

func (n *nodeUDP) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sessions)
}
