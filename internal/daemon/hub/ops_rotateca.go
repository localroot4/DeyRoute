package hub

import (
	"context"
	stderrors "errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Step ids of rotate-ca.
const (
	stepNewCA     = "new_ca"
	stepHubCert   = "hub_cert"
	stepTrustOnly = "trust_new_ca"
	stepTunnelsCA = "tunnels"
	stepNodePfx   = "node:"
)

// SecurityRotateCA implements api.Local (`deyroute security rotate-ca`,
// section 11: every node certificate is issued again while the nodes are
// online; an offline node joins again later). The rotation never locks an
// online node out:
//
//  1. A new CA is created and saved (the old CA certificate stays in
//     secrets/ca.prev.crt and is still trusted); new joins are signed by it.
//  2. Every online node gets a new key and certificate signed by the new CA
//     (cert.renew, cert.install) and trusts both CAs; it reconnects with the
//     new certificate while the hub still presents its old certificate.
//  3. The hub control certificate is issued again by the new CA and the old
//     CA is dropped: nodes that were offline must join again.
//  4. Every migrated node gets its certificate once more with the new CA
//     as its only trust anchor.
//  5. The tunnels are rendered with the new CA (internal tunnel
//     certificates are issued again) and their active transports restart.
//
// Running it again after an interruption finishes the rotation that is in
// progress (secrets/ca.prev.crt exists) instead of starting another one.
func (l *local) SecurityRotateCA(ctx context.Context, progress func(api.Step)) (api.RotateCAResult, error) {
	h := l.h
	h.rotateMu.Lock()
	defer h.rotateMu.Unlock()
	rep := &steps{progress: progress}
	res := api.RotateCAResult{Reissued: []string{}, Offline: []string{}}
	// Every new node certificate is saved to config.yaml: an edit that is
	// not applied must stop the rotation before a CA is created.
	if err := h.checkApplied(h.Config()); err != nil {
		return res, withLog(err)
	}
	if _, err := h.autoBackup(); err != nil {
		return res, withLog(err)
	}
	var (
		oldCert []byte
		newCA   *tlsutil.CA
	)
	if err := rep.run(stepNewCA, func() (string, error) {
		var err error
		oldCert, newCA, err = h.beginRotation()
		if err != nil {
			return "", err
		}
		return newCA.Fingerprint(), nil
	}); err != nil {
		return res, withLog(err)
	}
	bundle := joinPEM(newCA.CertPEM, oldCert)
	migrated := h.reissueNodes(ctx, rep, newCA, bundle, &res)
	if err := rep.run(stepHubCert, func() (string, error) { return "", h.finishRotation(newCA) }); err != nil {
		return res, withLog(err)
	}
	_ = rep.runWarn(stepTrustOnly, func() (string, error, error) {
		var warn error
		for _, node := range migrated {
			if err := h.reissueNode(ctx, node, newCA, newCA.CertPEM); err != nil {
				h.log.Warn("node keeps trusting the old CA as well; run deyroute security rotate-ca again later",
					dlog.Node(node), dlog.Err(err))
				warn = err
			}
		}
		return strings.Join(migrated, ", "), warn, nil
	})
	if err := rep.run(stepTunnelsCA, func() (string, error) {
		h.caMu.Lock()
		h.caPEM = newCA.CertPEM
		h.secrets = &secrets.Store{Root: h.o.Root, CA: newCA, Now: h.o.Now}
		h.caMu.Unlock()
		return "", h.reconcileAll(ctx)
	}); err != nil {
		h.log.Warn("tunnels were not all rendered with the new CA", dlog.Err(err))
	}
	h.log.Info("internal CA rotated", slog.String("fingerprint", newCA.Fingerprint()),
		slog.String("reissued", strings.Join(res.Reissued, ",")), slog.String("offline", strings.Join(res.Offline, ",")))
	h.opsEvent("", "internal CA rotated: "+newCA.Fingerprint()+"; nodes that were offline must join again: "+strings.Join(res.Offline, ", "))
	return res, nil
}

// beginRotation creates and saves the new CA (the old certificate goes to
// ca.prev.crt) and trusts both on the control channel; an unfinished
// rotation is resumed with the CA it created.
func (h *Hub) beginRotation() (oldCert []byte, newCA *tlsutil.CA, err error) {
	dir := h.path(config.SecretsDir)
	prevPath := filepath.Join(dir, FilePrevCACert)
	cur := h.currentCA()
	prev, perr := os.ReadFile(prevPath) // #nosec G304 -- fixed secrets path below Root
	if perr == nil {
		if _, err := tlsutil.ParseCertChain(prev); err == nil {
			h.log.Info("resuming an unfinished CA rotation")
			return prev, cur, nil
		}
	}
	name := h.Config().Hub.Name
	newCA, err = tlsutil.NewCA(name, h.now())
	if err != nil {
		return nil, nil, err
	}
	newCA.Now = h.o.Now
	oldCert = cur.CertPEM
	if err := tlsutil.WriteSecret(prevPath, oldCert); err != nil {
		return nil, nil, err
	}
	if err := newCA.Save(filepath.Join(dir, setup.FileCACert), filepath.Join(dir, setup.FileCAKey)); err != nil {
		return nil, nil, err
	}
	trust := joinPEM(newCA.CertPEM, oldCert)
	if err := h.serveTLS(trust, nil, nil); err != nil {
		return nil, nil, err
	}
	h.caMu.Lock()
	h.ca, h.trust = newCA, trust
	h.caMu.Unlock()
	h.log.Info("new internal CA created; the old one stays trusted during the rotation", slog.String("fingerprint", newCA.Fingerprint()))
	return oldCert, newCA, nil
}

// serveTLS replaces the live control-channel TLS configuration: trust is
// the accepted CA bundle; certPEM/keyPEM the hub certificate (nil = the
// one on disk).
func (h *Hub) serveTLS(trust, certPEM, keyPEM []byte) error {
	dir := h.path(config.SecretsDir)
	if certPEM == nil {
		var err error
		if certPEM, err = readFile(filepath.Join(dir, setup.FileHubCert)); err != nil {
			return err
		}
		if keyPEM, err = readFile(filepath.Join(dir, setup.FileHubKey)); err != nil {
			return err
		}
	}
	cfg, err := tlsutil.ServerTLSConfig(trust, certPEM, keyPEM)
	if err != nil {
		return err
	}
	h.tlsLive.Store(cfg)
	return nil
}

// reissueNodes runs step 2 for every node: online nodes get a certificate
// of the new CA (trusting bundle); offline nodes and failures are listed in
// res.Offline. It returns the nodes that came back with their new
// certificate.
func (h *Hub) reissueNodes(ctx context.Context, rep *steps, newCA *tlsutil.CA, bundle []byte, res *api.RotateCAResult) []string {
	var migrated []string
	for _, n := range h.Config().Nodes {
		id := stepNodePfx + n.ID
		title := i18n.T(i18n.HubTitleNodeCert, n.ID)
		if !h.Online(n.ID) {
			res.Offline = append(res.Offline, n.ID)
			w := deyerr.New(deyerr.N003, deyerr.Params{"node": n.ID})
			rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepSkipped, Detail: "offline: join it again later", Error: api.ToDTO(w)})
			continue
		}
		rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepRunning})
		if err := h.reissueNode(ctx, n.ID, newCA, bundle); err != nil {
			res.Offline = append(res.Offline, n.ID)
			rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepFailed, Error: api.ToDTO(err),
				Detail: "join it again later"})
			h.log.Warn("node certificate could not be replaced; the node must join again", dlog.Node(n.ID), dlog.Err(err))
			continue
		}
		res.Reissued = append(res.Reissued, n.ID)
		migrated = append(migrated, n.ID)
		rep.emitTitled(api.Step{ID: id, Title: title, Status: api.StepOK})
	}
	return migrated
}

// reissueNode gives node a new key and a certificate signed by ca
// (cert.renew, sign, cert.install with trust as its CA bundle), records the
// certificate and waits until the node reconnected with it.
func (h *Hub) reissueNode(ctx context.Context, node string, ca *tlsutil.CA, trust []byte) error {
	var rr api.CertRenewResult
	cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
	err := h.Call(cctx, node, api.CmdCertRenew, api.CertRenewArgs{NewCAPEM: string(ca.CertPEM)}, &rr)
	cancel()
	if err != nil {
		return err
	}
	certPEM, err := ca.SignCSR([]byte(rr.CSRPEM), node, api.NodeCertValidity)
	if err != nil {
		return err
	}
	cert, err := tlsutil.ParseCert(certPEM)
	if err != nil {
		return err
	}
	fp := tlsutil.Fingerprint(cert.Raw)
	// The node may reconnect with the new certificate before the answer
	// arrives: authenticate accepts it from now on.
	if err := h.st.PutMeta(metaPendingCert+node, fp); err != nil {
		return err
	}
	cctx, cancel = context.WithTimeout(ctx, nodeCallTimeout)
	err = h.Call(cctx, node, api.CmdCertInstall, api.CertInstallArgs{CertPEM: string(certPEM), CAPEM: string(trust)}, nil)
	cancel()
	if err != nil && !deyerr.HasCode(err, deyerr.N003) && !deyerr.HasCode(err, deyerr.N014) {
		// The node refused: it keeps its certificate.
		_ = h.st.DeleteMeta(metaPendingCert + node)
		return err
	}
	if err == nil {
		if perr := h.promoteNodeCert(node, fp); perr != nil {
			return perr
		}
	}
	h.recordNodeCert(node, certPEM)
	return h.waitNodeCert(ctx, node, fp)
}

// waitNodeCert waits until node is online with the certificate fp (its
// stream reconnected after cert.install).
func (h *Hub) waitNodeCert(ctx context.Context, node, fp string) error {
	deadline := time.NewTimer(h.o.NodeReconnectWait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if s, err := h.session(node, api.CmdSysinfo); err == nil && sameFingerprint(s.CertFingerprint, fp) {
			return nil
		}
		select {
		case <-ctx.Done():
			return deyerr.Wrap(deyerr.X031, ctx.Err(), deyerr.Params{"command": "rotate-ca"})
		case <-deadline.C:
			return deyerr.New(deyerr.N003, deyerr.Params{"node": node}).
				WithWhy("the node did not reconnect with its new certificate within " + h.o.NodeReconnectWait.String())
		case <-tick.C:
		}
	}
}

// finishRotation issues the hub control certificate with the new CA (same
// name and addresses as before), saves it, stops trusting the old CA and
// deletes ca.prev.crt.
func (h *Hub) finishRotation(newCA *tlsutil.CA) error {
	dir := h.path(config.SecretsDir)
	oldPEM, err := readFile(filepath.Join(dir, setup.FileHubCert))
	if err != nil {
		return err
	}
	old, err := tlsutil.ParseCert(oldPEM)
	if err != nil {
		return err
	}
	cn := old.Subject.CommonName
	if cn == "" {
		cn = h.Config().Hub.Name
	}
	certPEM, keyPEM, err := newCA.IssueServer(cn, old.IPAddresses, old.DNSNames, tlsutil.HubCertValidity)
	if err != nil {
		return err
	}
	if err := tlsutil.WriteSecretPair(filepath.Join(dir, setup.FileHubCert), certPEM, filepath.Join(dir, setup.FileHubKey), keyPEM); err != nil {
		return err
	}
	if err := h.serveTLS(newCA.CertPEM, certPEM, keyPEM); err != nil {
		return err
	}
	h.caMu.Lock()
	h.trust = newCA.CertPEM
	h.caMu.Unlock()
	if err := os.Remove(filepath.Join(dir, FilePrevCACert)); err != nil && !stderrors.Is(err, os.ErrNotExist) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": filepath.Join(dir, FilePrevCACert)})
	}
	h.log.Info("hub control certificate issued by the new CA; the old CA is no longer trusted")
	return nil
}
