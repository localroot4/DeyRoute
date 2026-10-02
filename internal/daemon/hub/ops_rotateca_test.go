package hub

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// certRotation makes a fake node answer cert.renew / cert.install like the
// node agent: a new key per renew, and after an install the node
// reconnects with the new certificate and CA bundle.
type certRotation struct {
	n        *fakeNode
	mu       sync.Mutex
	key      []byte
	installs []api.CertInstallArgs
	wg       sync.WaitGroup
}

func rotateOn(t *testing.T, n *fakeNode) *certRotation {
	r := &certRotation{n: n}
	t.Cleanup(r.wg.Wait)
	n.on(api.CmdCertRenew, func(_ context.Context, n *fakeNode, _ api.Command, _ func([]string)) (any, error) {
		csr, key, err := tlsutil.NewKeyAndCSR(n.id)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.key = key
		r.mu.Unlock()
		return api.CertRenewResult{CSRPEM: string(csr)}, nil
	})
	n.on(api.CmdCertInstall, func(_ context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var a api.CertInstallArgs
		decode(t, cmd, &a)
		r.mu.Lock()
		key := r.key
		r.installs = append(r.installs, a)
		r.mu.Unlock()
		cfg, err := tlsutil.ClientTLSConfig([]byte(a.CAPEM), []byte(a.CertPEM), key, "")
		if err != nil {
			return nil, err
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			time.Sleep(50 * time.Millisecond) // the answer goes out first
			n.stop()
			n.mu.Lock()
			n.tls, n.certPEM, n.keyPEM = cfg, []byte(a.CertPEM), key
			n.mu.Unlock()
			n.start()
		}()
		return nil, nil
	})
	return r
}

func (r *certRotation) installed() []api.CertInstallArgs {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]api.CertInstallArgs(nil), r.installs...)
}

func TestSecurityRotateCA(t *testing.T) {
	env := startHub(t, nil)
	ctx := ctxT(t)
	n := env.joinNode("de-1")
	rot := rotateOn(t, n)
	n.start()
	env.waitOnline("de-1", true)
	off := env.joinNode("nl-1") // joined, offline during the rotation
	oldCA := env.h.currentCA()

	var log stepLog
	res, err := env.client.SecurityRotateCA(ctx, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Equal(t, []string{"de-1"}, res.Reissued)
	require.Equal(t, []string{"nl-1"}, res.Offline)
	require.Equal(t, []string{"new_ca:ok", "node:de-1:ok", "node:nl-1:skipped", "hub_cert:ok", "trust_new_ca:ok", "tunnels:ok"}, log.finished())

	newCA := env.h.currentCA()
	require.NotEqual(t, oldCA.Fingerprint(), newCA.Fingerprint())
	dir := filepath.Join(env.root, config.SecretsDir)
	onDisk, err := tlsutil.LoadCA(filepath.Join(dir, setup.FileCACert), filepath.Join(dir, setup.FileCAKey))
	require.NoError(t, err)
	require.Equal(t, newCA.Fingerprint(), onDisk.Fingerprint())
	require.NoFileExists(t, filepath.Join(dir, FilePrevCACert))
	hubPEM, err := os.ReadFile(filepath.Join(dir, setup.FileHubCert))
	require.NoError(t, err)
	hubCert, err := tlsutil.ParseCert(hubPEM)
	require.NoError(t, err)
	require.NoError(t, hubCert.CheckSignatureFrom(newCA.Cert))
	require.Equal(t, newCA.CertPEM, env.h.trustPEM())
	require.Equal(t, newCA.CertPEM, env.h.tunnelCAPEM())

	// Two installs: both CAs trusted, then only the new one.
	inst := rot.installed()
	require.Len(t, inst, 2)
	cas, err := tlsutil.ParseCertChain([]byte(inst[0].CAPEM))
	require.NoError(t, err)
	require.Len(t, cas, 2)
	cas, err = tlsutil.ParseCertChain([]byte(inst[1].CAPEM))
	require.NoError(t, err)
	require.Len(t, cas, 1)
	require.Equal(t, newCA.Fingerprint(), tlsutil.Fingerprint(cas[0].Raw))
	final, err := tlsutil.ParseCert([]byte(inst[1].CertPEM))
	require.NoError(t, err)
	require.NoError(t, final.CheckSignatureFrom(newCA.Cert))
	fp := tlsutil.Fingerprint(final.Raw)
	node, ok := env.h.Config().NodeByID("de-1")
	require.True(t, ok)
	require.Equal(t, fp, node.CertFingerprint)
	require.Eventually(t, func() bool {
		s, err := env.h.session("de-1", api.CmdSysinfo)
		return err == nil && s.CertFingerprint == fp
	}, testWait, 20*time.Millisecond)
	var pending string
	ok, err = env.h.st.GetMeta(metaPendingCert+"de-1", &pending)
	require.NoError(t, err)
	require.False(t, ok)

	// The node that was offline cannot come back with the old CA.
	off.start()
	require.Never(t, func() bool { return env.h.Online("nl-1") }, 500*time.Millisecond, 50*time.Millisecond)
	off.stop()
	// A new join uses the new CA.
	fresh := env.joinNode("fr-1").start()
	env.waitOnline("fr-1", true)
	fresh.stop()

	// An interrupted rotation (the previous CA still on disk) is finished
	// with the CA it created.
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(dir, FilePrevCACert), oldCA.CertPEM))
	log = stepLog{}
	res, err = env.client.SecurityRotateCA(ctx, log.add)
	require.NoError(t, err, "%v", log.finished())
	require.Equal(t, newCA.Fingerprint(), env.h.currentCA().Fingerprint())
	require.Equal(t, []string{"de-1"}, res.Reissued)
	require.NoFileExists(t, filepath.Join(dir, FilePrevCACert))
	env.waitOnline("de-1", true)
}

// An edit of config.yaml that is not applied stops the rotation before a
// CA is created (DEY-C026): the new node certificates could not be saved
// and the nodes would be locked out.
func TestRotateCAWaitsForUnappliedEdit(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1")
	rot := rotateOn(t, n)
	n.start()
	env.waitOnline("de-1", true)
	oldCA := env.h.currentCA()
	path := filepath.Join(env.root, config.DefaultPath)
	c, err := config.LoadWith(path, testValidate)
	require.NoError(t, err)
	c.Hub.Name = "edited"
	require.NoError(t, config.SaveWith(path, c, testValidate))

	var log stepLog
	_, err = env.client.SecurityRotateCA(ctxT(t), log.add)
	require.Equal(t, deyerr.C026, codeOf(err))
	require.Empty(t, log.finished())
	require.Empty(t, rot.installed())
	require.Equal(t, oldCA.Fingerprint(), env.h.currentCA().Fingerprint())
	require.NoFileExists(t, filepath.Join(env.root, config.SecretsDir, FilePrevCACert))
	require.True(t, env.h.Online("de-1"))
}

func TestRotateCALoadsPreviousCA(t *testing.T) {
	env, o := prepareEnv(t, nil)
	prev, err := tlsutil.NewCA("old", time.Now())
	require.NoError(t, err)
	require.NoError(t, tlsutil.WriteSecret(filepath.Join(env.root, config.SecretsDir, FilePrevCACert), prev.CertPEM))
	env.startEnv(o)
	cas, err := tlsutil.ParseCertChain(env.h.trustPEM())
	require.NoError(t, err)
	require.Len(t, cas, 2, "the previous CA stays trusted until the rotation is finished")
	list, err := env.client.SecurityTLSShow(ctxT(t), "")
	require.NoError(t, err)
	var prevShown bool
	for _, c := range list {
		prevShown = prevShown || (c.Kind == "ca" && c.Fingerprint == prev.Fingerprint() && c.Warning != "")
	}
	require.True(t, prevShown)
}
