package node

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func dial(t *testing.T, e *env) api.Local {
	t.Helper()
	l, err := api.Dial(e.socket, api.DialOptions{Service: ServiceName, Timeout: 20 * time.Second})
	require.NoError(t, err)
	return l
}

func TestLocalAPI(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	l := dial(t, e)
	ctx := context.Background()

	eventually(t, func() bool {
		st, err := l.Status(ctx)
		return err == nil && st.NodeSelf != nil && st.NodeSelf.Connected
	}, "status shows the node connected")
	st, err := l.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, "node", st.Role)
	require.Equal(t, api.JSONSchemaVersion, st.Schema)
	require.Equal(t, testNode, st.NodeSelf.ID)
	require.Equal(t, e.hubAddr, st.NodeSelf.HubAddr)
	require.True(t, st.NodeSelf.Compatible)
	require.False(t, st.NodeSelf.LastContact.IsZero())
	require.NotNil(t, st.Tunnels)
	require.Empty(t, st.Warnings)

	// Methods of the hub role answer X009.
	_, err = l.TunnelList(ctx)
	e9 := requireCode(t, err, deyerr.X009)
	require.Contains(t, e9.Message(), "needs a hub, but this server is a node")
	_, err = l.DoctorCollect(ctx, "nl-1")
	requireCode(t, err, deyerr.X009)
	dd, err := l.DoctorCollect(ctx, "")
	require.NoError(t, err)
	require.Equal(t, "node", dd.Role)

	// Logs of a tunnel and of the node; "hub" needs the hub.
	writeFile(t, e.path(systemd.TunnelLogFile("main")), "a\nb\n", 0o600)
	var got []api.LogLine
	require.NoError(t, l.Logs(ctx, api.LogQuery{Target: "main"}, func(ll api.LogLine) error {
		got = append(got, ll)
		return nil
	}))
	require.Equal(t, []api.LogLine{{Source: "node", Line: "a"}, {Source: "node", Line: "b"}}, got)
	err = l.Logs(ctx, api.LogQuery{Target: "hub"}, func(api.LogLine) error { return nil })
	requireCode(t, err, deyerr.X009)
	err = l.Logs(ctx, api.LogQuery{Target: "../x"}, func(api.LogLine) error { return nil })
	requireCode(t, err, deyerr.N050)

	// Follow until the client stops.
	fctx, cancel := context.WithCancel(ctx)
	lines := make(chan string, 10)
	done := make(chan error, 1)
	go func() {
		done <- l.Logs(fctx, api.LogQuery{Target: "main", Follow: true, Lines: 1}, func(ll api.LogLine) error {
			lines <- ll.Line
			return nil
		})
	}()
	require.Equal(t, "b", <-lines)
	f, err := os.OpenFile(e.path(systemd.TunnelLogFile("main")), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("c\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Equal(t, "c", <-lines)
	cancel()
	<-done

	// StopAll stops every running tunnel unit.
	args := renderArgs("main", "backhaul", "wssmux")
	_, err = call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	e.sys.setState("deyroute-tun@other.de-1.rathole-noise.service", "active")
	require.NoError(t, l.StopAll(ctx))
	require.Equal(t, "inactive", e.sys.state(unitOf(args)))
	require.Equal(t, "inactive", e.sys.state("deyroute-tun@other.de-1.rathole-noise.service"))

	// NodeSetHub validates, stores and reconnects.
	err = l.NodeSetHub(ctx, "not-an-address")
	requireCode(t, err, deyerr.C013)
	hub2 := newFakeHub()
	addr2 := startHub(t, e.ca, e.caPEM, hub2)
	require.NoError(t, l.NodeSetHub(ctx, addr2))
	require.Equal(t, addr2, e.config().Node.HubAddr)
	s2 := waitSession(t, hub2)
	require.Equal(t, testNode, s2.NodeID)
	st, err = l.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, addr2, st.NodeSelf.HubAddr)
}

func TestSetHubCommandMovesTheStream(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	hub2 := newFakeHub()
	addr2 := startHub(t, e.ca, e.caPEM, hub2)
	_, err := call[json.RawMessage](t, s, api.CmdSetHub, api.SetHubArgs{Addr: addr2})
	require.NoError(t, err)
	s2 := waitSession(t, hub2)
	require.Equal(t, testNode, s2.NodeID)
	require.Equal(t, addr2, e.config().Node.HubAddr)
	_, err = call[map[string]string](t, s2, api.CmdSysinfo, nil)
	require.NoError(t, err)
	_, err = call[json.RawMessage](t, s2, api.CmdSetHub, api.SetHubArgs{Addr: "bad"})
	requireCode(t, err, deyerr.C013)
}

func TestSetHubHook(t *testing.T) {
	e := newEnv(t)
	var gotAddr string
	e.opts.SetHubAddr = func(_ context.Context, addr string) error {
		gotAddr = addr
		return deyerr.New(deyerr.C017, deyerr.Params{"path": "x"})
	}
	s := e.start()
	_, err := call[json.RawMessage](t, s, api.CmdSetHub, api.SetHubArgs{Addr: "5.6.7.8:44433"})
	requireCode(t, err, deyerr.C017)
	require.Equal(t, "5.6.7.8:44433", gotAddr)
}

func TestCertRotation(t *testing.T) {
	e := newEnv(t)
	ca2 := newTestCA(t, "test-hub-2")
	// During rotate-ca the hub trusts both CAs.
	bundle := append(append([]byte(nil), e.caPEM...), ca2.CertPEM...)
	hub := newFakeHub()
	addr := startHub(t, e.ca, bundle, hub)
	e.writeConfig(addr)
	e.hub = hub
	s := e.start()

	_, err := call[json.RawMessage](t, s, api.CmdCertInstall, api.CertInstallArgs{CertPEM: "x", CAPEM: "y"})
	requireCode(t, err, deyerr.N050) // no pending key yet

	res, err := call[api.CertRenewResult](t, s, api.CmdCertRenew, api.CertRenewArgs{NewCAPEM: string(ca2.CertPEM)})
	require.NoError(t, err)
	require.Contains(t, res.CSRPEM, "CERTIFICATE REQUEST")
	fi, err := os.Stat(e.path(PendingKeyFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	// A certificate for another key or another node is refused.
	otherCSR, _, err := tlsutil.NewKeyAndCSR(testNode)
	require.NoError(t, err)
	wrongKey, err := ca2.SignCSR(otherCSR, testNode, 0)
	require.NoError(t, err)
	_, err = call[json.RawMessage](t, s, api.CmdCertInstall, api.CertInstallArgs{CertPEM: string(wrongKey), CAPEM: string(bundle)})
	requireCode(t, err, deyerr.T002)
	wrongNode, err := ca2.SignCSR([]byte(res.CSRPEM), "nl-1", 0)
	require.NoError(t, err)
	_, err = call[json.RawMessage](t, s, api.CmdCertInstall, api.CertInstallArgs{CertPEM: string(wrongNode), CAPEM: string(bundle)})
	requireCode(t, err, deyerr.N050)
	signed, err := ca2.SignCSR([]byte(res.CSRPEM), testNode, 0)
	require.NoError(t, err)
	_, err = call[json.RawMessage](t, s, api.CmdCertInstall, api.CertInstallArgs{CertPEM: string(signed), CAPEM: string(e.caPEM)})
	requireCode(t, err, deyerr.T005) // not signed by the given CA

	_, err = call[json.RawMessage](t, s, api.CmdCertInstall, api.CertInstallArgs{CertPEM: string(signed), CAPEM: string(bundle)})
	require.NoError(t, err)
	s2 := waitSession(t, hub)
	fp, err := tlsutil.CertSHA256Hex(signed)
	require.NoError(t, err)
	require.Contains(t, s2.CertFingerprint, fp)
	certOnDisk, err := os.ReadFile(e.path(config.DefaultNodeCertFile))
	require.NoError(t, err)
	require.Equal(t, signed, certOnDisk)
	caOnDisk, err := os.ReadFile(e.path(CAFile))
	require.NoError(t, err)
	require.Equal(t, bundle, caOnDisk)
	require.Equal(t, ca2.Fingerprint(), e.config().Node.HubCAFingerprint)
	_, err = os.Stat(e.path(PendingKeyFile))
	require.True(t, os.IsNotExist(err))
	_, err = call[map[string]string](t, s2, api.CmdSysinfo, nil)
	require.NoError(t, err)
}

func TestMissingCertificateKeepsLocalAPI(t *testing.T) {
	e := newEnv(t)
	require.NoError(t, os.Remove(e.path(config.DefaultNodeKeyFile)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, e.opts) }()
	var l api.Local
	eventually(t, func() bool {
		var err error
		l, err = api.Dial(e.socket, api.DialOptions{Service: ServiceName})
		return err == nil
	}, "local API is served even without credentials")
	eventually(t, func() bool {
		st, err := l.Status(ctx)
		return err == nil && !st.NodeSelf.Connected && len(st.Warnings) > 0 && st.Warnings[0].Code == string(deyerr.N009)
	}, "status reports the hub unreachable")
	require.Eventually(t, func() bool { return strings.Contains(e.logs.String(), "DEY-T008") }, 10*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}
