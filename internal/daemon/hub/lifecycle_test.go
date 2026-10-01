package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func TestNewErrors(t *testing.T) {
	// No config: C014.
	_, err := New(Options{Root: t.TempDir(), Logger: dlog.Discard()})
	require.Equal(t, deyerr.C014, codeOf(err))

	// A node config: X009.
	root := t.TempDir()
	fp := tlsutil.Fingerprint([]byte("x"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, config.EtcDir), 0o700))
	require.NoError(t, config.Save(filepath.Join(root, config.DefaultPath), config.NewNode("de-1", "1.2.3.4:44433", fp)))
	_, err = New(Options{Root: root, Logger: dlog.Discard()})
	require.Equal(t, deyerr.X009, codeOf(err))

	// No CA: T007 (and nothing is left open).
	env, o := prepareEnv(t, nil)
	require.NoError(t, os.Remove(filepath.Join(env.root, config.SecretsDir, setup.FileCACert)))
	_, err = New(o)
	require.Equal(t, deyerr.T007, codeOf(err))

	// No hub certificate: T008.
	env, o = prepareEnv(t, nil)
	require.NoError(t, os.Remove(filepath.Join(env.root, config.SecretsDir, setup.FileHubKey)))
	_, err = New(o)
	require.Equal(t, deyerr.T008, codeOf(err))

	// The control port is busy: P012.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	_, o = prepareEnv(t, nil)
	o.ControlListen = ln.Addr().String()
	_, err = New(o)
	require.Equal(t, deyerr.P012, codeOf(err))

	// The state database is locked by another process: X020.
	_, o = prepareEnv(t, nil)
	h, err := New(o)
	require.NoError(t, err)
	o.ControlListen = "127.0.0.1:0"
	_, err = New(o)
	require.Equal(t, deyerr.X020, codeOf(err))
	require.NoError(t, h.Close())
	require.NoError(t, h.Close())
}

func TestServeOnceAndClose(t *testing.T) {
	env := startHub(t, nil)
	require.Equal(t, deyerr.X000, codeOf(env.h.Serve(context.Background())))
	require.NoError(t, env.h.Close())
	require.NotNil(t, env.h.State())
	require.NotNil(t, env.h.Logger())
	env.stop()
	// The socket is gone after the stop.
	_, err := os.Stat(env.sock)
	require.True(t, os.IsNotExist(err))
}

func TestRunStopsWithContext(t *testing.T) {
	_, o := prepareEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	o.OnReady = func(*Hub) { cancel() }
	require.NoError(t, Run(ctx, o))
	require.Error(t, Run(context.Background(), Options{Root: t.TempDir(), Logger: dlog.Discard()}))
}

func TestStartupImportsSnapshotsAndRegisters(t *testing.T) {
	env, o := prepareEnv(t, nil, func(o *Options, _ string) {
		o.Logger = nil
		o.SnapshotInterval = 30 * time.Millisecond
	})
	// Restored events left by `deyroute restore`.
	ev := state.Event{Seq: 7, At: time.Now().UTC().Add(-time.Hour), Level: state.LevelInfo, Type: state.EvTunnelUp, Tunnel: "main", Message: "old"}
	line, err := json.Marshal(ev)
	require.NoError(t, err)
	restore := filepath.Join(env.root, setup.RestoreEventsPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(restore), 0o750))
	require.NoError(t, os.WriteFile(restore, append(line, '\n'), 0o600))
	// A tunnel token on disk is registered with the redactor.
	store := &secrets.Store{Root: env.root}
	tok, err := store.Token("main")
	require.NoError(t, err)
	// A broken manifest override is logged, not fatal.
	require.NoError(t, os.WriteFile(filepath.Join(env.root, config.ManifestPath), []byte("not: [yaml"), 0o600))

	env.startEnv(o)
	require.NoFileExists(t, restore)
	evs, err := env.client.Events(ctxT(t), api.EventQuery{Tunnel: "main"})
	require.NoError(t, err)
	require.Len(t, evs, 1)
	require.Equal(t, "old", evs[0].Message)
	require.Equal(t, "***", dlog.Redact(tok))
	bak := state.BackupPath(filepath.Join(env.root, config.StatePath))
	require.Eventually(t, func() bool { _, err := os.Stat(bak); return err == nil }, testWait, 10*time.Millisecond)
	logData, err := os.ReadFile(filepath.Join(env.root, LogFile))
	require.NoError(t, err)
	require.Contains(t, string(logData), "manifest override")
	require.NotContains(t, string(logData), tok)
}

func TestRestartKeepsNodesOnline(t *testing.T) {
	env, o := prepareEnv(t, nil)
	env.startEnv(o)
	n := env.joinNode("de-1").start()
	m := env.joinNode("nl-1").start()
	env.waitOnline("de-1", true)
	env.waitOnline("nl-1", true)
	m.stop()
	env.stop()
	n.stop()

	// A new hub on the same root: nodes that were online get the offline
	// grace period; de-1 reconnects in time and raises no event, nl-1 does
	// not and goes offline.
	o.OnReady = nil
	env2 := &testEnv{t: t, root: env.root, sock: env.sock, runner: env.runner, ca: env.ca}
	env2.startEnv(o)
	ns, ok := env2.h.nodeState("de-1")
	require.True(t, ok)
	require.True(t, ns.Online)
	onlineBefore := env2.countEvents(state.EvNodeOnline)
	n.addr = env2.h.ControlAddr().String()
	n.start()
	env2.waitOnline("de-1", true)
	env2.waitEvent(state.EvNodeOffline, "nl-1")
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, onlineBefore, env2.countEvents(state.EvNodeOnline))
	require.Zero(t, countNodeEvents(t, env2, state.EvNodeOffline, "de-1"))
}

func countNodeEvents(t *testing.T, env *testEnv, typ, node string) int {
	evs, err := env.h.st.Events(state.EventFilter{Types: []string{typ}, Node: node})
	require.NoError(t, err)
	return len(evs)
}

func TestCertificateWarnings(t *testing.T) {
	env, o := prepareEnv(t, nil)
	// A hub certificate that expires in 5 days, and a tunnel certificate
	// that expires in 3.
	dir := filepath.Join(env.root, config.SecretsDir)
	certPEM, keyPEM, err := env.ca.IssueServer("ir-1", []net.IP{net.ParseIP("127.0.0.1")}, nil, 5*24*time.Hour+2*time.Hour)
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecretPair(filepath.Join(dir, setup.FileHubCert), certPEM, filepath.Join(dir, setup.FileHubKey), keyPEM))
	env.startEnv(o)
	n := env.joinNode("de-1")
	_ = n
	env.addTunnel("main", []string{"de-1"}, 443)
	tcert, _, err := env.ca.IssueServer("tunnel-main", []net.IP{net.ParseIP("127.0.0.1")}, nil, 3*24*time.Hour+2*time.Hour)
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(dir, secrets.TLSDir, "main", secrets.CertFile), tcert))

	st, err := env.client.Status(ctxT(t))
	require.NoError(t, err)
	var hubWarn, tunnelWarn bool
	for _, w := range st.Warnings {
		if w.Code != string(deyerr.T006) {
			continue
		}
		if w.Tunnel == "main" {
			tunnelWarn = true
			require.Contains(t, w.Message, "3 days")
		} else if bytes.Contains([]byte(w.Message), []byte(setup.FileHubCert)) {
			hubWarn = true
		}
	}
	require.True(t, hubWarn, "%v", st.Warnings)
	require.True(t, tunnelWarn, "%v", st.Warnings)
	// Cached: the same answer without re-reading.
	require.Equal(t, env.h.certWarnings(env.h.Config()), env.h.certWarnings(env.h.Config()))
}

func TestServeFailsWhenTheSocketIsTaken(t *testing.T) {
	env := startHub(t, nil)
	// A second hub (other root) on the same socket: the Local API cannot be
	// served (DEY-X040) and Serve returns the error.
	_, o := prepareEnv(t, nil)
	o.SocketPath = env.sock
	h, err := New(o)
	require.NoError(t, err)
	err = h.Serve(context.Background())
	require.Equal(t, deyerr.X040, codeOf(err))
}
