package hub

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/front"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func TestDataKeyOwner(t *testing.T) {
	for key, want := range map[string][2]string{
		"main/de-1/backhaul/tcpmux": {"main", "de-1"},
		"main/canary/ctl":           {"main", ""},
	} {
		tun, node, ok := dataKeyOwner(key)
		require.True(t, ok, key)
		require.Equal(t, want, [2]string{tun, node}, key)
	}
	for _, key := range []string{
		"main/de-1/backhaul/tcpmux/udp", // a companion
		"main/canary/loopback",          // the canary's user port
		"main/de-1/backhaul",            // not a rung key
		"main/de-1",
		"",
		"bad id/canary/ctl",
	} {
		_, _, ok := dataKeyOwner(key)
		require.False(t, ok, key)
	}
}

// A front node's shim reaches the backend server behind the hub's control
// port of its own rung through the CDN; other ports, other nodes and direct
// nodes are refused.
func TestFrontDataPlaneThroughHub(t *testing.T) {
	env := startFrontHub(t, testClientIP, nil)
	env.joinFront("fr-1")
	_, err := env.h.mutate(func(c *config.Config) error {
		c.Nodes = append(c.Nodes, config.Node{ID: "de-1", Name: "direct", PublicIP: "198.51.100.9"})
		c.Tunnels = append(c.Tunnels, config.NewTunnel("main", "Main", []string{"fr-1"}, []config.PortMap{{Listen: 8443}}))
		return nil
	})
	require.NoError(t, err)
	alloc := func(key string) int {
		p, err := env.h.st.AllocCtlPort(key, config.CtlRangeLow, config.CtlRangeHigh, nil)
		require.NoError(t, err)
		return p
	}
	rung := alloc("main/fr-1/backhaul/tcpmux")
	canary := alloc("main/canary/ctl")
	loopback := alloc("main/canary/loopback")
	companion := alloc("main/fr-1/backhaul/wssmux/udp")
	other := alloc("main/de-1/backhaul/tcpmux")

	token, err := env.h.secretStore().Token("main")
	require.NoError(t, err)
	for _, p := range []int{rung, canary} {
		got, err := env.h.frontAuthorize(p, "fr-1")
		require.NoError(t, err, p)
		require.Equal(t, token, got)
	}
	for _, c := range []struct {
		port int
		node string
	}{{rung, "de-1"}, {other, "de-1"}, {other, "fr-1"}, {loopback, "fr-1"}, {companion, "fr-1"}, {rung, "nope"}, {44433, "fr-1"}} {
		_, err := env.h.frontAuthorize(c.port, c.node)
		require.Error(t, err, "%d %s", c.port, c.node)
	}

	// End to end: a backend server on the rung's control port answers through
	// the shim, the CDN and the hub.
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(rung)))
	if err != nil {
		t.Skipf("control port %d is taken on this machine: %v", rung, err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(c, c)
	}()
	inner, err := tlsutil.ClientTLSConfig(env.h.trustPEM(), nil, nil, "")
	require.NoError(t, err)
	shimLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- front.RunShim(ctx, shimLn, front.ShimConfig{
			Target:   front.Target{Host: testFrontDomain, Port: env.cdn.Port(), TLS: true, EdgeIP: "127.0.0.1", Secret: env.secret()},
			Dialer:   env.dialer,
			Port:     rung,
			Node:     "fr-1",
			Token:    token,
			InnerTLS: inner,
		})
	}()
	defer func() { cancel(); require.NoError(t, <-done) }()
	c, err := net.Dial("tcp", shimLn.Addr().String())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(testWait))
	_, err = c.Write([]byte("ping through the front"))
	require.NoError(t, err)
	buf := make([]byte, len("ping through the front"))
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ping through the front", string(buf))
}
