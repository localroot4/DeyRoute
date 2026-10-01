package direct

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sync"
	"time"
)

// Wire format of the direct/native relay between the hub half and the node
// half (both run "deyroute relay"). The node half only ever connects to the
// targets listed in its own relay.json; the hub names a target by its index
// in that list, authenticated with the shared tunnel token.
//
// TCP: the hub opens one TCP connection to <node_ip>:<ctl> per user
// connection and sends a 32-byte preamble, then raw bytes in both
// directions:
//
//	magic "DEYR" | version 1 | flags 0 | index uint16 BE | nonce [8] | mac [16]
//
// mac = first 16 bytes of HMAC-SHA256(token, magic|version|flags|index|nonce).
// The node rejects bad MACs (constant-time compare), unknown versions,
// non-zero flags, indexes that are not a TCP target, and nonces it has
// already seen within the replay window.
//
// UDP: one datagram per user datagram, both directions, to/from
// <node_ip>:<ctl>/udp:
//
//	magic "DEYU" | index uint16 BE | session uint64 BE | payload | mac [16]
//
// mac = first 16 bytes of HMAC-SHA256(token, dir|magic|index|session|payload)
// where dir is one byte that is not sent: 1 for hub→node, 2 for node→hub.
// The direction byte keeps a datagram authenticated for one direction from
// being reflected back as valid in the other (an on-path attacker could
// otherwise bounce node replies into the node as requests, or hub requests
// into the hub as replies). The session id is chosen at random by the hub
// for every user client address; the node keeps one socket to the target
// per session.
const (
	tcpMagic        = "DEYR"
	udpMagic        = "DEYU"
	protocolVersion = 1

	macLen      = 16
	nonceLen    = 8
	preambleLen = 4 + 1 + 1 + 2 + nonceLen + macLen // 32

	udpHeaderLen = 4 + 2 + 8 // 14
	udpOverhead  = udpHeaderLen + macLen

	// UDP frame directions (bound into the MAC, never sent).
	dirToNode byte = 1
	dirToHub  byte = 2

	// maxUDPPayload is the largest user datagram relayed. Larger datagrams
	// are dropped (per-session buffers stay small: 4096 sessions × 16 KiB).
	maxUDPPayload = 16 * 1024
)

// macer computes truncated HMAC-SHA256 tags. It is not safe for concurrent
// use; every goroutine that authenticates frames owns one.
type macer struct {
	h   hash.Hash
	sum [sha256.Size]byte
}

func newMacer(key []byte) *macer { return &macer{h: hmac.New(sha256.New, key)} }

// tag returns the 16-byte tag of msg (valid until the next call).
func (m *macer) tag(msg []byte) []byte {
	m.h.Reset()
	m.h.Write(msg) // hash.Hash.Write never returns an error
	return m.h.Sum(m.sum[:0])[:macLen]
}

// verify compares the tag of msg with mac in constant time.
func (m *macer) verify(msg, mac []byte) bool {
	return hmac.Equal(m.tag(msg), mac)
}

// tagDir returns the 16-byte tag of dir|msg (valid until the next call).
func (m *macer) tagDir(dir byte, msg []byte) []byte {
	m.h.Reset()
	m.h.Write([]byte{dir}) // hash.Hash.Write never returns an error
	m.h.Write(msg)
	return m.h.Sum(m.sum[:0])[:macLen]
}

// newNonce returns 8 random bytes.
func newNonce() ([nonceLen]byte, error) {
	var n [nonceLen]byte
	_, err := rand.Read(n[:])
	return n, err
}

// buildPreamble returns the 32-byte TCP preamble for target index.
func buildPreamble(m *macer, index int, nonce [nonceLen]byte) [preambleLen]byte {
	var p [preambleLen]byte
	copy(p[0:4], tcpMagic)
	p[4] = protocolVersion
	p[5] = 0                                          // flags, reserved
	binary.BigEndian.PutUint16(p[6:8], uint16(index)) // #nosec G115 -- index < 64 (validated config)
	copy(p[8:16], nonce[:])
	copy(p[16:32], m.tag(p[:16]))
	return p
}

// parsePreamble authenticates p and returns the target index and nonce.
func parsePreamble(m *macer, p []byte) (index int, nonce [nonceLen]byte, ok bool) {
	if len(p) != preambleLen || string(p[0:4]) != tcpMagic {
		return 0, nonce, false
	}
	// The MAC is checked before any other field so an unauthenticated
	// peer learns nothing about versions or indexes.
	if !m.verify(p[:16], p[16:32]) {
		return 0, nonce, false
	}
	if p[4] != protocolVersion || p[5] != 0 {
		return 0, nonce, false
	}
	copy(nonce[:], p[8:16])
	return int(binary.BigEndian.Uint16(p[6:8])), nonce, true
}

// sealUDP frames the payload already stored at buf[udpHeaderLen:udpHeaderLen+n]
// in place for direction dir and returns the datagram to send. buf must have
// room for udpOverhead+n bytes.
func sealUDP(m *macer, dir byte, buf []byte, index int, session uint64, n int) []byte {
	copy(buf[0:4], udpMagic)
	binary.BigEndian.PutUint16(buf[4:6], uint16(index)) // #nosec G115 -- index < 64 (validated config)
	binary.BigEndian.PutUint64(buf[6:14], session)
	end := udpHeaderLen + n
	tag := m.tagDir(dir, buf[:end])
	copy(buf[end:end+macLen], tag)
	return buf[:end+macLen]
}

// openUDP authenticates datagram d received in direction dir and returns
// its fields. payload aliases d.
func openUDP(m *macer, dir byte, d []byte) (index int, session uint64, payload []byte, ok bool) {
	if len(d) < udpOverhead || string(d[0:4]) != udpMagic {
		return 0, 0, nil, false
	}
	end := len(d) - macLen
	if !hmac.Equal(m.tagDir(dir, d[:end]), d[end:]) {
		return 0, 0, nil, false
	}
	return int(binary.BigEndian.Uint16(d[4:6])), binary.BigEndian.Uint64(d[6:14]), d[udpHeaderLen:end], true
}

// replayCache remembers authenticated TCP nonces for ttl so a captured
// preamble cannot be replayed within that window. It is bounded: when it
// holds max entries the oldest is forgotten first (FIFO, which equals LRU
// for insert-only keys).
type replayCache struct {
	mu    sync.Mutex
	ttl   time.Duration
	seen  map[[nonceLen]byte]time.Time
	ring  []replayEntry
	head  int // index of the oldest entry
	count int
}

type replayEntry struct {
	nonce [nonceLen]byte
	at    time.Time
}

func newReplayCache(ttl time.Duration, max int) *replayCache {
	return &replayCache{ttl: ttl, seen: make(map[[nonceLen]byte]time.Time), ring: make([]replayEntry, max)}
}

// add records nonce at now and reports whether it was fresh.
func (c *replayCache) add(nonce [nonceLen]byte, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Expire old entries and make room.
	for c.count > 0 {
		old := c.ring[c.head]
		if c.count < len(c.ring) && now.Sub(old.at) < c.ttl {
			break
		}
		if at, ok := c.seen[old.nonce]; ok && at.Equal(old.at) {
			delete(c.seen, old.nonce)
		}
		c.head = (c.head + 1) % len(c.ring)
		c.count--
	}
	if at, dup := c.seen[nonce]; dup && now.Sub(at) < c.ttl {
		return false
	}
	c.seen[nonce] = now
	c.ring[(c.head+c.count)%len(c.ring)] = replayEntry{nonce: nonce, at: now}
	c.count++
	return true
}

// size returns the number of remembered nonces (tests).
func (c *replayCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}
