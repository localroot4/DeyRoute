package state

import (
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestAllocCtlPortStableAndExhausted(t *testing.T) {
	s, _ := newStore(t)
	const lo, hi = 30000, 30002
	a := Key("main", "de-1", "backhaul/wssmux")
	b := Key("main", "de-1", "rathole/noise")
	c := Key("main", "nl-1", "frp/wss")
	d := Key("alt", "de-1", "frp/wss")

	pa, err := s.AllocCtlPort(a, lo, hi, nil)
	require.NoError(t, err)
	require.Equal(t, 30000, pa)
	again, err := s.AllocCtlPort(a, lo, hi, func(int) bool { return true })
	require.NoError(t, err)
	require.Equal(t, pa, again, "stable per key even when the port is busy (held by its own backend)")

	pb, err := s.AllocCtlPort(b, lo, hi, func(p int) bool { return p == 30001 })
	require.NoError(t, err)
	require.Equal(t, 30002, pb, "busy ports are skipped")
	pc, err := s.AllocCtlPort(c, lo, hi, nil)
	require.NoError(t, err)
	require.Equal(t, 30001, pc)

	_, err = s.AllocCtlPort(d, lo, hi, nil)
	require.True(t, deyerr.HasCode(err, deyerr.P020), "got %v", err)

	ports, err := s.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, map[string]int{a: 30000, b: 30002, c: 30001}, ports)

	// Releasing the node prefix frees its ports; the lowest is reused.
	require.NoError(t, s.ReleaseCtlPorts("main/de-1/"))
	pd, err := s.AllocCtlPort(d, lo, hi, nil)
	require.NoError(t, err)
	require.Equal(t, 30000, pd)
	ports, err = s.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, map[string]int{c: 30001, d: 30000}, ports)

	require.NoError(t, s.ReleaseCtlPort(c))
	require.NoError(t, s.ReleaseCtlPort(c))
	ports, err = s.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, map[string]int{d: 30000}, ports)

	require.Error(t, s.ReleaseCtlPorts(""))
	require.Error(t, s.ReleaseCtlPort(""))
}

func TestAllocCtlPortPersistsAcrossReopen(t *testing.T) {
	s, path := newStore(t)
	k := Key("main", "de-1", "xray/reality")
	p, err := s.AllocCtlPort(k, CtlPortLow, CtlPortHigh, func(p int) bool { return p < 30005 })
	require.NoError(t, err)
	require.Equal(t, 30005, p)
	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s2.Close()) }()
	p2, err := s2.AllocCtlPort(k, CtlPortLow, CtlPortHigh, nil)
	require.NoError(t, err)
	require.Equal(t, p, p2)
}

func TestAllocCtlPortRangeChangeAndValidation(t *testing.T) {
	s, _ := newStore(t)
	k := Key("main", "de-1", "frp/wss")
	p, err := s.AllocCtlPort(k, 30000, 30010, nil)
	require.NoError(t, err)
	require.Equal(t, 30000, p)
	// Outside the new range → reallocated inside it.
	p, err = s.AllocCtlPort(k, 31000, 31010, nil)
	require.NoError(t, err)
	require.Equal(t, 31000, p)

	for _, r := range [][2]int{{0, 10}, {10, 70000}, {20, 10}} {
		_, err := s.AllocCtlPort(k, r[0], r[1], nil)
		require.True(t, deyerr.HasCode(err, deyerr.P017), "range %v: %v", r, err)
	}
	_, err = s.AllocCtlPort("", 30000, 30010, nil)
	require.True(t, deyerr.HasCode(err, deyerr.X021))
}

func TestNetIndex(t *testing.T) {
	s, _ := newStore(t)
	a, err := s.AllocNetIndex("main", 3)
	require.NoError(t, err)
	require.Equal(t, 1, a)
	b, err := s.AllocNetIndex("alt", 3)
	require.NoError(t, err)
	require.Equal(t, 2, b)
	a2, err := s.AllocNetIndex("main", 3)
	require.NoError(t, err)
	require.Equal(t, a, a2, "stable")
	c, err := s.AllocNetIndex("third", 3)
	require.NoError(t, err)
	require.Equal(t, 3, c)
	_, err = s.AllocNetIndex("fourth", 3)
	require.True(t, deyerr.HasCode(err, deyerr.P030), "got %v", err)

	require.NoError(t, s.ReleaseNetIndex("alt"))
	d, err := s.AllocNetIndex("fourth", 3)
	require.NoError(t, err)
	require.Equal(t, 2, d, "lowest free index reused")

	idx, err := s.NetIndexes()
	require.NoError(t, err)
	require.Equal(t, map[string]int{"main": 1, "third": 3, "fourth": 2}, idx)

	// An index beyond a shrunken max must be reassigned; 1 and 2 are taken.
	e, err := s.AllocNetIndex("third", 2)
	require.True(t, deyerr.HasCode(err, deyerr.P030), "got %v", err)
	require.Zero(t, e)

	_, err = s.AllocNetIndex("", 3)
	require.Error(t, err)
	_, err = s.AllocNetIndex("x", 0)
	require.Error(t, err)
}

func TestDeleteTunnelRemovesEverything(t *testing.T) {
	s, _ := newStore(t)
	for _, tun := range []string{"main", "mainx"} {
		require.NoError(t, s.PutTunnel(TunnelState{ID: tun, State: StateUp}))
		require.NoError(t, s.AppendProbe(tun, "de-1", "backhaul/wssmux", ProbeSample{OK: true}))
		require.NoError(t, s.AppendProbe(tun, "nl-1", "rathole/noise", ProbeSample{OK: true}))
		require.NoError(t, s.PutMetrics(tun, Metrics{BytesIn: 1}))
		_, err := s.AllocCtlPort(Key(tun, "de-1", "backhaul/wssmux"), CtlPortLow, CtlPortHigh, nil)
		require.NoError(t, err)
		_, err = s.AllocNetIndex(tun, 10)
		require.NoError(t, err)
		_, err = s.AppendEvent(Event{Type: EvTunnelUp, Tunnel: tun})
		require.NoError(t, err)
	}

	require.NoError(t, s.DeleteTunnel("main"))

	_, ok, err := s.GetTunnel("main")
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = s.GetMetrics("main")
	require.NoError(t, err)
	require.False(t, ok)
	keys, err := s.ProbeKeys()
	require.NoError(t, err)
	require.Equal(t, []string{"mainx/de-1/backhaul/wssmux", "mainx/nl-1/rathole/noise"}, keys, "prefix match is exact on the id")
	ports, err := s.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, map[string]int{"mainx/de-1/backhaul/wssmux": 30001}, ports)
	idx, err := s.NetIndexes()
	require.NoError(t, err)
	require.Equal(t, map[string]int{"mainx": 2}, idx)
	evs, err := s.Events(EventFilter{Tunnel: "main"})
	require.NoError(t, err)
	require.Len(t, evs, 1, "history is kept")

	// Deleting an unknown tunnel is not an error.
	require.NoError(t, s.DeleteTunnel("ghost"))
}

func TestDeleteNodeFreesItsProbesAndPorts(t *testing.T) {
	s, _ := newStore(t)
	require.NoError(t, s.PutNode(NodeState{ID: "de-1"}))
	require.NoError(t, s.PutNode(NodeState{ID: "nl-1"}))
	for _, n := range []string{"de-1", "nl-1"} {
		require.NoError(t, s.AppendProbe("main", n, "frp/wss", ProbeSample{OK: true}))
		_, err := s.AllocCtlPort(Key("main", n, "frp/wss"), CtlPortLow, CtlPortHigh, nil)
		require.NoError(t, err)
	}
	require.NoError(t, s.DeleteNode("de-1"))
	nodes, err := s.ListNodes()
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	keys, err := s.ProbeKeys()
	require.NoError(t, err)
	require.Equal(t, []string{"main/nl-1/frp/wss"}, keys)
	ports, err := s.CtlPorts()
	require.NoError(t, err)
	require.Equal(t, map[string]int{"main/nl-1/frp/wss": 30001}, ports)
}
