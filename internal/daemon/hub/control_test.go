package hub

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

func TestJoinOnlineOffline(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1")

	cfg := env.h.Config()
	node, ok := cfg.NodeByID("de-1")
	require.True(t, ok)
	require.Equal(t, "127.0.0.1", node.PublicIP)
	cert, err := tlsutil.ParseCert(n.certPEM)
	require.NoError(t, err)
	require.Equal(t, tlsutil.Fingerprint(cert.Raw), node.CertFingerprint)
	require.Equal(t, "de-1", cert.Subject.CommonName)
	// The config on disk has the node too (atomic write).
	onDisk, err := config.LoadWith(filepath.Join(env.root, config.DefaultPath), testValidate)
	require.NoError(t, err)
	require.Len(t, onDisk.Nodes, 1)

	n.start()
	env.waitOnline("de-1", true)
	ev := env.waitEvent(state.EvNodeOnline, "de-1")
	require.Equal(t, state.LevelInfo, ev.Level)

	// Heartbeats, RTT and hello facts reach the node list (over the socket).
	require.Eventually(t, func() bool {
		list, err := env.client.NodeList(ctxT(t))
		require.NoError(t, err)
		return len(list) == 1 && list[0].Online && list[0].CPUPercent == 3 && list[0].RAMBytes == 121<<20 &&
			!list[0].LastHeartbeat.IsZero()
	}, testWait, 10*time.Millisecond)
	list, err := env.client.NodeList(ctxT(t))
	require.NoError(t, err)
	require.Equal(t, version.Version, list[0].Version)
	require.True(t, list[0].Compatible)
	require.Equal(t, node.CertFingerprint, list[0].Fingerprint)
	ns, _ := env.h.nodeState("de-1")
	require.Equal(t, "Test OS", ns.OS)
	require.Equal(t, "127.0.0.1", ns.RemoteIP)

	online, off := env.h.ControlOnline("de-1")
	require.True(t, online)
	require.Zero(t, off)

	// A node without heartbeats for OfflineAfter goes offline once.
	n.stop()
	env.waitOnline("de-1", false)
	ev = env.waitEvent(state.EvNodeOffline, "de-1")
	require.Equal(t, string(deyerr.N003), ev.Code)
	online, off = env.h.ControlOnline("de-1")
	require.False(t, online)
	require.Positive(t, off)
	require.Equal(t, deyerr.N003, codeOf(env.h.Call(ctxT(t), "de-1", api.CmdSysinfo, nil, nil)))

	// Reconnecting brings it back with a second node_online.
	n.start()
	env.waitOnline("de-1", true)
	require.Eventually(t, func() bool { return env.countEvents(state.EvNodeOnline) == 2 }, testWait, 10*time.Millisecond)
	require.Equal(t, 1, env.countEvents(state.EvNodeOffline))

	// The events are in events.log as JSON lines.
	data, err := os.ReadFile(filepath.Join(env.root, EventsLogFile))
	require.NoError(t, err)
	require.Contains(t, string(data), `"event":"node_offline"`)
	require.Contains(t, string(data), `"node":"de-1"`)
	require.Contains(t, string(data), `"component":"events"`)
}

func TestJoinRules(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	addr := env.h.ControlAddr().String()
	fp := env.ca.Fingerprint()
	csr, _, err := tlsutil.NewKeyAndCSR("x")
	require.NoError(t, err)

	// Unknown token: N001, nothing registered.
	_, err = api.Join(ctx, addr, fp, api.JoinRequest{Token: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", CSRPEM: string(csr)})
	require.Equal(t, deyerr.N001, codeOf(err))
	require.Empty(t, env.h.Config().Nodes)

	// Derived id from the hostname, name kept.
	resp, _ := env.join(api.JoinRequest{Hostname: "Web-Server.example.com", Name: "Germany 1\x07"})
	require.Equal(t, "web-server", resp.NodeID)
	require.Equal(t, "127.0.0.1", resp.PublicIP)
	require.Equal(t, "ir-1", resp.HubName)
	require.Equal(t, "127.0.0.1:44433", resp.HubAddr)
	n, _ := env.h.Config().NodeByID("web-server")
	require.Equal(t, "Germany 1", n.Name)

	// The same hostname again gets a unique id.
	resp, _ = env.join(api.JoinRequest{Hostname: "web-server"})
	require.Equal(t, "web-server-2", resp.NodeID)
	// An empty hostname falls back to "node".
	resp, _ = env.join(api.JoinRequest{})
	require.Equal(t, "node", resp.NodeID)

	// A requested id that is taken: N010; reserved or invalid: C007.
	for id, want := range map[string]deyerr.Code{"web-server": deyerr.N010, "canary": deyerr.C007, "Bad_ID": deyerr.C007} {
		jc, err := env.h.Local().NodeJoinCommand(ctx, 0)
		require.NoError(t, err)
		link, err := api.ParseJoinLink(jc.Link)
		require.NoError(t, err)
		_, err = api.Join(ctx, addr, fp, api.JoinRequest{Token: link.Token, NodeID: id, CSRPEM: string(csr)})
		require.Equal(t, want, codeOf(err), id)
	}
	// A broken CSR is refused (T009) and registers nothing.
	jc, err := env.h.Local().NodeJoinCommand(ctx, 0)
	require.NoError(t, err)
	link, err := api.ParseJoinLink(jc.Link)
	require.NoError(t, err)
	_, err = api.Join(ctx, addr, fp, api.JoinRequest{Token: link.Token, NodeID: "de-9", CSRPEM: "garbage"})
	require.Equal(t, deyerr.T009, codeOf(err))
	_, ok := env.h.Config().NodeByID("de-9")
	require.False(t, ok)
	require.Len(t, env.h.Config().Nodes, 3)

	// A token works once.
	_, err = api.Join(ctx, addr, fp, api.JoinRequest{Token: link.Token, NodeID: "de-8", CSRPEM: string(csr)})
	require.Equal(t, deyerr.N001, codeOf(err))
}

func TestAuthenticateRefusesUnknownNodes(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1")

	// A certificate of the right CA for a node that never joined: N008.
	csr, key, err := tlsutil.NewKeyAndCSR("ghost")
	require.NoError(t, err)
	certPEM, err := env.ca.SignCSR(csr, "ghost", 0)
	require.NoError(t, err)
	cfg, err := tlsutil.ClientTLSConfig(env.ca.CertPEM, certPEM, key, "")
	require.NoError(t, err)
	ghost := &api.ControlClient{HubAddr: n.addr, TLSConfig: cfg}
	_, err = ghost.FetchAsset(ctxT(t), "amd64", &bytes.Buffer{})
	require.Equal(t, deyerr.N008, codeOf(err))

	// A re-issued certificate for de-1 has another fingerprint: N008.
	certPEM2, err := env.ca.SignCSR(csr, "de-1", 0)
	require.NoError(t, err)
	cfg2, err := tlsutil.ClientTLSConfig(env.ca.CertPEM, certPEM2, key, "")
	require.NoError(t, err)
	_, err = (&api.ControlClient{HubAddr: n.addr, TLSConfig: cfg2}).FetchAsset(ctxT(t), "amd64", &bytes.Buffer{})
	require.Equal(t, deyerr.N008, codeOf(err))

	// The real node is accepted.
	_, err = n.client().FetchAsset(ctxT(t), "amd64", &bytes.Buffer{})
	require.NoError(t, err)
}

func TestNodeIPChange(t *testing.T) {
	env, o := prepareEnv(t, nil, func(o *Options, _ string) { o.DisableFirewall = false })
	env.startEnv(o)
	n := env.joinNode("de-1")
	// The node's recorded IP differs from the address it connects from.
	_, err := env.h.mutate(func(c *config.Config) error {
		nd, _ := c.NodeByID("de-1")
		nd.PublicIP = "10.9.9.9"
		return nil
	})
	require.NoError(t, err)
	n.start()
	ev := env.waitEvent(state.EvNodeIPChanged, "de-1")
	require.Contains(t, ev.Message, "10.9.9.9")
	require.Contains(t, ev.Message, "127.0.0.1")
	nd, _ := env.h.Config().NodeByID("de-1")
	require.Equal(t, "127.0.0.1", nd.PublicIP)
	// @nodes follows.
	require.Eventually(t, func() bool {
		scripts := env.nftScripts()
		if len(scripts) == 0 {
			return false
		}
		last := scripts[len(scripts)-1]
		return bytes.Contains([]byte(last), []byte("127.0.0.1")) && !bytes.Contains([]byte(last), []byte("10.9.9.9"))
	}, testWait, 10*time.Millisecond)
	env.waitOnline("de-1", true)
	// Only one event although several requests came from the new IP.
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 1, env.countEvents(state.EvNodeIPChanged))
}

func TestIncompatibleNode(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1")
	n.version = "0.0.1"
	var hubHello api.Hello
	got := make(chan struct{})
	c := n.client()
	c.OnHubHello = func(h api.Hello) {
		hubHello = h
		close(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-got:
	case <-time.After(testWait):
		t.Fatal("no hub hello")
	}
	require.False(t, hubHello.Compatible)
	require.Equal(t, version.Version, hubHello.Version)
	env.waitOnline("de-1", true)
	err := env.h.Call(ctxT(t), "de-1", api.CmdSysinfo, nil, nil)
	require.Equal(t, deyerr.N004, codeOf(err))
	st, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	require.False(t, st.Nodes[0].Compatible)
	var found bool
	for _, w := range st.Warnings {
		if w.Code == string(deyerr.N004) && w.Node == "de-1" {
			found = true
			require.Contains(t, w.Message, "0.0.1")
			require.Contains(t, w.Message, "Update")
		}
	}
	require.True(t, found, "N004 warning in %v", st.Warnings)
	// No node is offered for downloads.
	require.Empty(t, env.h.onlineNodes())
}

func TestCallUnknownNode(t *testing.T) {
	env := startHub(t, nil)
	require.Equal(t, deyerr.N008, codeOf(env.h.Call(ctxT(t), "nope", api.CmdSysinfo, nil, nil)))
	require.Equal(t, deyerr.N008, codeOf(env.h.Stream(ctxT(t), "nope", api.CmdLogsTail, nil, nil)))
	online, off := env.h.ControlOnline("nope")
	require.False(t, online)
	require.Positive(t, off)
}

func TestHelpers(t *testing.T) {
	require.Equal(t, "1.2.3.4", normalizeIP("::ffff:1.2.3.4"))
	require.Equal(t, "1.2.3.4", normalizeIP("1.2.3.4:5678"))
	require.Equal(t, "", normalizeIP("nope"))
	require.True(t, sameIP("1.2.3.4", "::ffff:1.2.3.4"))
	require.False(t, sameIP("", ""))
	fp := tlsutil.Fingerprint([]byte("x"))
	require.True(t, sameFingerprint(fp, fp[len("sha256:"):]))
	require.False(t, sameFingerprint("sha256:...", fp))
	require.Equal(t, "node", nodeBase("_"))
	require.Equal(t, "tunnel", nodeBase("tunnel"))
	require.Equal(t, "db1", nodeBase("DB1.local"))
	require.Equal(t, "node", nodeBase("canary"))
	long := string(bytes.Repeat([]byte("é"), 80))
	require.Len(t, []rune(cleanName(long)), config.MaxNameLen)
	require.Equal(t, "ab", cleanName(" a\x00b "))
	require.Equal(t, "download", fileOfURL("%zz"))
	require.Equal(t, "example.com", fileOfURL("https://example.com/"))
	require.Equal(t, "f.tar.gz", fileOfURL("https://example.com/x/f.tar.gz"))
	_, err := parseCandidateKey("nokey")
	require.Error(t, err)
	c, err := parseCandidateKey("de-1/backhaul/wssmux")
	require.NoError(t, err)
	require.Equal(t, state.Candidate{Node: "de-1", Transport: "backhaul/wssmux"}, c)
	require.True(t, unitRunning("active"))
	require.False(t, unitRunning("inactive"))
	require.True(t, isRelease("1.2.3"))
	require.False(t, isRelease("dev"))
	require.False(t, isRelease("x.y"))
}
