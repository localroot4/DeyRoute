package direct

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestPreambleRoundTrip(t *testing.T) {
	m := newMacer([]byte("tok-test"))
	nonce, err := newNonce()
	require.NoError(t, err)
	p := buildPreamble(m, 3, nonce)
	require.Len(t, p, 32)
	require.Equal(t, "DEYR", string(p[:4]))
	require.Equal(t, byte(1), p[4])

	idx, got, ok := parsePreamble(newMacer([]byte("tok-test")), p[:])
	require.True(t, ok)
	require.Equal(t, 3, idx)
	require.Equal(t, nonce, got)

	// Wrong token.
	_, _, ok = parsePreamble(newMacer([]byte("other")), p[:])
	require.False(t, ok)
	// Any flipped bit fails.
	for i := range p {
		q := p
		q[i] ^= 0x01
		_, _, ok := parsePreamble(m, q[:])
		require.False(t, ok, "byte %d", i)
	}
	// Short input.
	_, _, ok = parsePreamble(m, p[:31])
	require.False(t, ok)
}

func TestPreambleRejectsVersionAndFlags(t *testing.T) {
	m := newMacer([]byte("k"))
	var nonce [nonceLen]byte
	p := buildPreamble(m, 0, nonce)
	p[4] = 2 // version 2 with a valid MAC
	copy(p[16:], m.tag(p[:16]))
	_, _, ok := parsePreamble(m, p[:])
	require.False(t, ok)
	p[4], p[5] = 1, 1 // flags set
	copy(p[16:], m.tag(p[:16]))
	_, _, ok = parsePreamble(m, p[:])
	require.False(t, ok)
}

func TestUDPFrames(t *testing.T) {
	m := newMacer([]byte("tok-test"))
	buf := make([]byte, udpBufLen)
	n := copy(buf[udpHeaderLen:], "hello")
	d := sealUDP(m, dirToNode, buf, 2, 0xdeadbeef, n)
	require.Len(t, d, udpOverhead+5)
	idx, id, payload, ok := openUDP(newMacer([]byte("tok-test")), dirToNode, append([]byte(nil), d...))
	require.True(t, ok)
	require.Equal(t, 2, idx)
	require.Equal(t, uint64(0xdeadbeef), id)
	require.Equal(t, "hello", string(payload))

	_, _, _, ok = openUDP(newMacer([]byte("nope")), dirToNode, d)
	require.False(t, ok)
	bad := append([]byte(nil), d...)
	bad[udpHeaderLen] ^= 1
	_, _, _, ok = openUDP(m, dirToNode, bad)
	require.False(t, ok)
	_, _, _, ok = openUDP(m, dirToNode, d[:udpOverhead-1])
	require.False(t, ok)
	wrongMagic := append([]byte(nil), d...)
	copy(wrongMagic, "DEYR")
	_, _, _, ok = openUDP(m, dirToNode, wrongMagic)
	require.False(t, ok)

	// Empty payloads are valid datagrams.
	d = sealUDP(m, dirToNode, buf, 0, 1, 0)
	_, _, payload, ok = openUDP(m, dirToNode, d)
	require.True(t, ok)
	require.Empty(t, payload)

	// A datagram sealed for one direction is not valid in the other one
	// (no reflection of node replies into the node or hub requests into
	// the hub).
	d = sealUDP(m, dirToHub, buf, 1, 9, n)
	_, _, _, ok = openUDP(m, dirToNode, d)
	require.False(t, ok, "a node→hub frame must not authenticate as hub→node")
	_, _, _, ok = openUDP(m, dirToHub, d)
	require.True(t, ok)
	d = sealUDP(m, dirToNode, buf, 1, 9, n)
	_, _, _, ok = openUDP(m, dirToHub, d)
	require.False(t, ok, "a hub→node frame must not authenticate as node→hub")
}

func TestReplayCache(t *testing.T) {
	c := newReplayCache(5*time.Minute, 3)
	t0 := time.Unix(1000, 0)
	n := func(b byte) [nonceLen]byte { return [nonceLen]byte{b} }

	require.True(t, c.add(n(1), t0))
	require.False(t, c.add(n(1), t0.Add(time.Minute)), "replay within the window")
	require.True(t, c.add(n(2), t0.Add(time.Minute)))
	require.True(t, c.add(n(3), t0.Add(2*time.Minute)))
	require.Equal(t, 3, c.size())
	// Capacity: the oldest (1) is forgotten to make room.
	require.True(t, c.add(n(4), t0.Add(3*time.Minute)))
	require.Equal(t, 3, c.size())
	require.True(t, c.add(n(1), t0.Add(3*time.Minute)), "evicted early by capacity")
	// Expiry: after 5 minutes every old nonce is gone.
	later := t0.Add(20 * time.Minute)
	require.True(t, c.add(n(2), later))
	require.Equal(t, 1, c.size())
	require.False(t, c.add(n(2), later.Add(4*time.Minute)))
	require.True(t, c.add(n(2), later.Add(5*time.Minute)))
}

func TestRelayConfigValidate(t *testing.T) {
	good := func() *RelayConfig {
		return &RelayConfig{Version: 1, Tunnel: "main", Role: RoleNode, Token: "t", Bind: "0.0.0.0:30001",
			Ports: []RelayPort{{Index: 0, Proto: "tcp", Target: "127.0.0.1:443"}}}
	}
	require.NoError(t, good().Validate())
	cases := map[string]func(c *RelayConfig){
		"version":      func(c *RelayConfig) { c.Version = 2 },
		"tunnel":       func(c *RelayConfig) { c.Tunnel = "" },
		"negative":     func(c *RelayConfig) { c.IdleTimeoutS = -1 },
		"role":         func(c *RelayConfig) { c.Role = "proxy" },
		"node token":   func(c *RelayConfig) { c.Token = "" },
		"bind":         func(c *RelayConfig) { c.Bind = "0.0.0.0" },
		"no ports":     func(c *RelayConfig) { c.Ports = nil },
		"index":        func(c *RelayConfig) { c.Ports[0].Index = 64 },
		"dup index":    func(c *RelayConfig) { c.Ports = append(c.Ports, c.Ports[0]) },
		"proto":        func(c *RelayConfig) { c.Ports[0].Proto = "icmp" },
		"target":       func(c *RelayConfig) { c.Ports[0].Target = "x" },
		"hub token":    func(c *RelayConfig) { c.Role, c.Token, c.Node = RoleHub, "", "1.2.3.4:1" },
		"hub node":     func(c *RelayConfig) { c.Role, c.Node = RoleHub, "1.2.3.4" },
		"hub listen":   func(c *RelayConfig) { c.Role, c.Node = RoleHub, "1.2.3.4:1" },
		"check ip":     func(c *RelayConfig) { c.Role, c.NodeIP = RoleCheck, "host" },
		"check udp":    func(c *RelayConfig) { c.Role, c.NodeIP, c.Ports[0].Proto = RoleCheck, "1.2.3.4", "udp" },
		"target space": func(c *RelayConfig) { c.Ports[0].Target = "a b:1" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := good()
			mut(c)
			require.Error(t, c.Validate())
		})
	}

	c := good()
	require.Equal(t, DefaultIdleTimeout, c.IdleTimeout())
	require.Equal(t, DefaultDialTimeout, c.DialTimeout())
	require.Equal(t, DefaultUDPIdleTimeout, c.UDPIdleTimeout())
	require.Equal(t, DefaultMaxUDPSessions, c.UDPSessionLimit())
	c.IdleTimeoutS, c.DialTimeoutS, c.UDPIdleTimeoutS, c.MaxUDPSessions = 1, 2, 3, 4
	require.Equal(t, time.Second, c.IdleTimeout())
	require.Equal(t, 2*time.Second, c.DialTimeout())
	require.Equal(t, 3*time.Second, c.UDPIdleTimeout())
	require.Equal(t, 4, c.UDPSessionLimit())
}

func TestLoadRelayConfigErrors(t *testing.T) {
	_, err := LoadRelayConfig(t.TempDir() + "/missing.json")
	require.True(t, deyerr.HasCode(err, deyerr.B060))
	_, err = ParseRelayConfig("x", []byte("{"))
	require.True(t, deyerr.HasCode(err, deyerr.B060))
	_, err = ParseRelayConfig("x", []byte(`{"version":1,"tunnel":"main","role":"hub","ports":[]}`))
	require.True(t, deyerr.HasCode(err, deyerr.B060))
}

func TestLimiter(t *testing.T) {
	l := newLimiter(time.Hour)
	var h countHandler
	logger := newTestLogger(&h)
	l.log(logger, "k", "first")
	l.log(logger, "k", "second")
	l.log(logger, "other", "third")
	require.Equal(t, 2, h.count())
	l.last["k"] = time.Now().Add(-2 * time.Hour)
	l.log(logger, "k", "again")
	require.Equal(t, 3, h.count())
	require.Equal(t, int64(1), h.lastSuppressed())
}
