package node

import (
	"context"
	"crypto/x509"
	stderrors "errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// certRenew answers cert.renew (security rotate-ca, section 11): a new
// Ed25519 key and CSR for this node id. The key stays pending (0600) until
// cert.install brings the certificate the hub signed with the new CA.
func (a *agent) certRenew(_ context.Context, args api.CertRenewArgs) (api.CertRenewResult, error) {
	if args.NewCAPEM != "" {
		if _, err := tlsutil.ParseCertChain([]byte(args.NewCAPEM)); err != nil {
			return api.CertRenewResult{}, err
		}
	}
	csr, key, err := tlsutil.NewKeyAndCSR(a.nodeID)
	if err != nil {
		return api.CertRenewResult{}, err
	}
	a.certMu.Lock()
	defer a.certMu.Unlock()
	if err := tlsutil.WriteSecret(a.path(PendingKeyFile), key); err != nil {
		return api.CertRenewResult{}, err
	}
	a.log.Info("new node key generated for certificate renewal")
	return api.CertRenewResult{CSRPEM: string(csr)}, nil
}

// certInstall answers cert.install: the certificate must match the pending
// key, carry this node id and chain to the given CA for client auth. The
// CA, certificate and key replace the old ones (the old CA is put back if
// the pair cannot be written), config.yaml gets the new CA fingerprint and
// the agent reconnects with the new credentials after answering.
func (a *agent) certInstall(_ context.Context, args api.CertInstallArgs) error {
	const cmd = api.CmdCertInstall
	a.certMu.Lock()
	defer a.certMu.Unlock()
	pendingPath := a.path(PendingKeyFile)
	keyPEM, err := os.ReadFile(pendingPath) // #nosec G304 -- fixed secrets path below Root
	if stderrors.Is(err, fs.ErrNotExist) {
		return a.refuse(cmd, "no pending key: cert.renew must come first")
	}
	if err != nil {
		return deyerr.Wrap(deyerr.S009, err, deyerr.Params{"path": PendingKeyFile, "reason": err.Error()})
	}
	key, err := tlsutil.ParsePrivateKey(keyPEM)
	if err != nil {
		return err
	}
	cert, err := tlsutil.ParseCert([]byte(args.CertPEM))
	if err != nil {
		return err
	}
	if !tlsutil.KeyMatchesCert(cert, key) {
		return deyerr.New(deyerr.T002, deyerr.Params{"cert": "the new node certificate", "key": PendingKeyFile})
	}
	if cert.Subject.CommonName != a.nodeID {
		return a.refuse(cmd, "the certificate names node "+cert.Subject.CommonName+", this is "+a.nodeID)
	}
	cas, err := tlsutil.ParseCertChain([]byte(args.CAPEM))
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	for _, c := range cas {
		pool.AddCert(c)
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots: pool, CurrentTime: a.o.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return deyerr.Wrap(deyerr.T005, err, deyerr.Params{"path": "the new node certificate"}).WithDetail(err.Error())
	}
	var issuer *x509.Certificate
	for _, c := range cas {
		if cert.CheckSignatureFrom(c) == nil {
			issuer = c
			break
		}
	}
	if _, err := tlsutil.ClientTLSConfig([]byte(args.CAPEM), []byte(args.CertPEM), keyPEM, ""); err != nil {
		return err
	}
	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		return err
	}
	if cfg.Node == nil {
		return deyerr.New(deyerr.C016, deyerr.Params{"role": cfg.Role})
	}
	caPath := a.path(CAFile)
	oldCA, oldErr := os.ReadFile(caPath) // #nosec G304 -- fixed secrets path below Root
	if err := tlsutil.WriteSecret(caPath, []byte(args.CAPEM)); err != nil {
		return err
	}
	if err := tlsutil.WriteSecretPair(a.path(cfg.Node.CertFile), []byte(args.CertPEM), a.path(cfg.Node.KeyFile), keyPEM); err != nil {
		if oldErr == nil {
			_ = tlsutil.WriteSecret(caPath, oldCA)
		}
		return err
	}
	_ = os.Remove(pendingPath)
	if issuer != nil {
		fp := tlsutil.Fingerprint(issuer.Raw)
		a.cfgMu.Lock()
		_, err := config.Mutate(a.cfgPath, config.ValidateOptions{}, func(c *config.Config) error {
			if c.Node == nil {
				return deyerr.New(deyerr.C016, deyerr.Params{"role": c.Role})
			}
			c.Node.HubCAFingerprint = fp
			return nil
		})
		a.cfgMu.Unlock()
		if err != nil {
			a.setLastError(err)
			a.log.Error("new certificate installed but config.yaml could not record the CA fingerprint", dlog.Err(err))
		}
	}
	a.log.Info("new node certificate installed; reconnecting", slog.String("ca", filepath.Base(CAFile)))
	a.requestReconnect(a.o.ReconnectDelay)
	return nil
}
