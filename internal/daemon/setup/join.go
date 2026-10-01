package setup

import (
	"context"
	"crypto/x509"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	deylog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// JoinFunc performs POST /v1/join against the hub with the pinned CA
// (api.Join).
type JoinFunc func(ctx context.Context, hubAddr, fingerprint string, req api.JoinRequest) (api.JoinResponse, error)

// JoinOptions configure Join (`deyroute join 'dey://…' [--name N]`).
type JoinOptions struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Runner runs systemctl (exec.NewRunner() when nil).
	Runner exec.Runner

	// Link is the join link dey://TOKEN@HUB_IP:PORT#sha256:… (quotes and
	// whitespace as pasted from the join command are accepted).
	Link string
	// Name is the node's display name; its slug is requested as node id.
	// Empty lets the hub derive the id from the host name.
	Name string
	// ApplySysctl applies SysctlProfile. The join command is
	// non-interactive, so the CLI sets it unless the owner declined (spec
	// section 12 asks for confirmation; `--yes` gives it).
	ApplySysctl bool
	// SysctlProfile is balanced (default when empty) or aggressive.
	SysctlProfile string
	// StartService installs the unit templates, runs `systemctl enable
	// --now deyroute-node.service` and waits for the local API socket.
	StartService bool

	// Now is the clock used to verify the issued certificate.
	Now func() time.Time
	// Progress receives every step update (may be nil).
	Progress func(api.Step)
	// Logger receives one line per step (discarded when nil).
	Logger *slog.Logger

	// JoinFunc replaces api.Join (tests, proxies); nil = api.Join, which
	// dials the hub over its own pinned HTTP/2 transport.
	JoinFunc JoinFunc
	// Hostname returns the host name (os.Hostname when nil).
	Hostname func() (string, error)
	// LookupGroup returns the gid of a group (LookupGroupID when nil).
	LookupGroup func(name string) (gid int, err error)
	// LookupUser returns the uid of a user (LookupUserID when nil); with
	// LookupGroup it tells whether the backend user deyroute exists.
	LookupUser func(name string) (uid int, err error)
	// Chown changes ownership (os.Lchown when nil).
	Chown func(path string, uid, gid int) error
	// SocketPath is the local API socket (Root/run/deyroute/daemon.sock).
	SocketPath string
	// SocketTimeout bounds the wait for the socket (20 s when <= 0).
	SocketTimeout time.Duration
}

// JoinResult reports a finished join.
type JoinResult struct {
	NodeID        string
	HubAddr       string
	HubName       string
	HubVersion    string
	PublicIP      string // this node's public IP as seen by the hub
	CAFingerprint string
	ConfigPath    string
	// Compatible reports whether hub and node share major.minor (spec
	// section 5); the hub updates incompatible nodes.
	Compatible     bool
	SysctlProfile  string
	SysctlWarnings []string
	ServiceStarted bool
}

// Join joins this server to a hub as a node (spec sections 3 and 11):
// parse_link (DEY-N006), keys (Ed25519 key + CSR), join (POST /v1/join with
// the CA pinned from the link; the hub's codes N001/N002/N007/N009/N010 are
// returned unchanged; the answer is verified: CA = pin (N002), certificate
// chains to it and matches our key (N002/N020)), secrets (node.key,
// node.crt, ca.crt, 0600), sysctl, config (config.NewNode) and service.
// DEY-I013 when this server is already set up. The token is single use, so
// every local check (sysctl profile, directories, the backend user) runs
// before it is sent: an invalid SysctlProfile is DEY-I014 {config} (C013)
// and nothing reaches the hub.
func Join(ctx context.Context, o JoinOptions) (*JoinResult, error) {
	e := newEnv(envOptions{
		Root: o.Root, Runner: o.Runner, Now: o.Now, LookupGroup: o.LookupGroup, LookupUser: o.LookupUser, Chown: o.Chown,
		SocketPath: o.SocketPath, SocketTimeout: o.SocketTimeout, Progress: o.Progress, Logger: o.Logger,
	})
	if err := e.refuseConfigured(); err != nil {
		return nil, err
	}
	// Network steps keep the hub's codes (their Fix lines are what the
	// owner needs); local steps are wrapped in DEY-I014.
	passthrough := func(step string, err error) error {
		e.rep.fail(step, err)
		if isDEY(err) {
			return err
		}
		return stepError(deyerr.I014, step, err)
	}
	fail := func(step string, err error) error {
		e.rep.fail(step, err)
		return stepError(deyerr.I014, step, err)
	}
	joinFn := o.JoinFunc
	if joinFn == nil {
		joinFn = api.Join
	}

	// parse_link
	e.rep.start(StepParseLink)
	link, err := api.ParseJoinLink(o.Link)
	if err != nil {
		return nil, passthrough(StepParseLink, err)
	}
	deylog.RegisterSecret(link.Token)
	e.rep.ok(StepParseLink, link.Addr())

	// The join token is single use: everything that can fail locally is
	// checked or prepared before the hub spends it (options, directories,
	// the backend user), so a local problem never forces a new join
	// command and a node removal on the hub.
	profile := strings.TrimSpace(o.SysctlProfile)
	if profile == "" {
		profile = config.SysctlBalanced
	}
	if o.ApplySysctl {
		if _, err := sysctl.Profile(profile, false); err != nil {
			return nil, fail(StepConfig, err)
		}
	}

	// keys (and the directory layout the secrets go to)
	e.rep.start(StepKeys)
	userWarn := e.ensureSystemUser(ctx)
	if err := e.ensureLayout(); err != nil {
		return nil, fail(StepKeys, err)
	}
	name := strings.TrimSpace(o.Name)
	host := hostname(o.Hostname)
	nodeID := requestedNodeID(name)
	cn := nodeID
	if cn == "" {
		cn = config.Slugify(host)
	}
	csrPEM, keyPEM, err := tlsutil.NewKeyAndCSR(cn)
	if err != nil {
		return nil, fail(StepKeys, err)
	}
	e.rep.okOrWarn(StepKeys, "ed25519", userWarn)

	// join
	e.rep.start(StepJoin)
	req := api.JoinRequest{
		Token:    link.Token,
		NodeID:   nodeID,
		Name:     name,
		CSRPEM:   string(csrPEM),
		Version:  version.Version,
		Arch:     runtime.GOARCH,
		OS:       e.osPrettyName(),
		Hostname: host,
	}
	resp, err := joinFn(ctx, link.Addr(), link.Fingerprint, req)
	if err != nil {
		return nil, passthrough(StepJoin, err)
	}
	caPEM, certPEM, skewed, err := verifyJoinResponse(resp, link.Fingerprint, keyPEM, e.now())
	if err != nil {
		return nil, passthrough(StepJoin, err)
	}
	if skewed {
		e.rep.warn(StepJoin, resp.NodeID+": the clock of this server ("+e.now().UTC().Format(time.RFC3339)+
			") differs from the hub's by more than "+tlsutil.ClockSkew.String()+"; turn on time sync: timedatectl set-ntp true", nil)
	} else {
		e.rep.ok(StepJoin, resp.NodeID)
	}

	// secrets
	e.rep.start(StepSecrets)
	if err := tlsutil.WriteSecretPair(e.secret(FileNodeCert), certPEM, e.secret(FileNodeKey), keyPEM); err != nil {
		return nil, fail(StepSecrets, err)
	}
	if err := tlsutil.WriteSecret(e.secret(FileCACert), caPEM); err != nil {
		return nil, fail(StepSecrets, err)
	}
	e.rep.ok(StepSecrets, config.SecretsDir)

	res := &JoinResult{
		NodeID:        resp.NodeID,
		HubAddr:       link.Addr(),
		HubName:       resp.HubName,
		HubVersion:    resp.HubVersion,
		PublicIP:      resp.PublicIP,
		CAFingerprint: link.Fingerprint,
		ConfigPath:    e.configPath(),
		Compatible:    resp.HubVersion != "" && version.Compatible(resp.HubVersion, version.Version),
		SysctlProfile: config.SysctlOff,
	}

	// sysctl
	e.rep.start(StepSysctl)
	cfg := config.NewNode(resp.NodeID, link.Addr(), link.Fingerprint)
	if !o.ApplySysctl {
		e.rep.skip(StepSysctl, config.SysctlOff)
	} else {
		warnings, err := applySysctl(e.root, profile, cfg.Tuning.BBR, false)
		res.SysctlWarnings = warnings
		switch {
		case err != nil:
			e.rep.warn(StepSysctl, profile, err)
		case len(warnings) > 0:
			res.SysctlProfile = profile
			e.rep.warn(StepSysctl, sysctlDetail(profile, warnings), nil)
		default:
			res.SysctlProfile = profile
			e.rep.ok(StepSysctl, profile)
		}
	}
	cfg.Tuning.SysctlProfile = res.SysctlProfile

	// config
	e.rep.start(StepConfig)
	if err := config.SaveWith(res.ConfigPath, cfg, validateOptions()); err != nil {
		return nil, fail(StepConfig, err)
	}
	e.rep.ok(StepConfig, res.ConfigPath)

	// service
	if err := e.serviceStep(ctx, o.StartService, systemd.NodeUnit, false); err != nil {
		return nil, serviceFailed(fail(StepService, err), systemd.NodeUnit)
	}
	res.ServiceStarted = o.StartService
	return res, nil
}

// requestedNodeID is the node id asked for with --name: its slug when that
// is a usable node id. Names without ASCII letters or digits (e.g. Persian)
// and reserved ids yield "" so the hub derives the id.
func requestedNodeID(name string) string {
	if name == "" {
		return ""
	}
	id := config.Slugify(name)
	if !config.ValidNodeID(id) {
		return ""
	}
	if id == config.FallbackID && !strings.EqualFold(strings.TrimSpace(name), config.FallbackID) {
		return ""
	}
	return id
}

// hostname returns the host name, "node" when it cannot be read.
func hostname(fn func() (string, error)) string {
	if fn == nil {
		fn = os.Hostname
	}
	h, err := fn()
	h = strings.TrimSpace(h)
	if err != nil || h == "" {
		return "node"
	}
	return h
}

// verifyJoinResponse checks the hub's answer: the CA is the pinned one
// (DEY-N002), the node id is valid, and the node certificate chains to the
// pinned CA for client authentication, names the node id and belongs to
// keyPEM (DEY-N020). It returns the CA certificates and the node
// certificate as clean PEM.
//
// The chain is checked at a time inside the new certificate's validity:
// the hub just issued it with its own clock, and a node whose clock is off
// by more than tlsutil.ClockSkew must not see a false "not signed by the
// pinned CA" after the hub has spent the token (the hub checks the node
// certificate with the hub's clock later). skewed reports that this
// server's clock is outside the certificate's validity.
func verifyJoinResponse(resp api.JoinResponse, fingerprint string, keyPEM []byte, now time.Time) (caPEM, certPEM []byte, skewed bool, err error) {
	ca, err := tlsutil.VerifyCAPEM([]byte(resp.CAPEM), fingerprint)
	if err != nil {
		return nil, nil, false, err
	}
	unusable := func(reason string) error {
		return deyerr.New(deyerr.N020, deyerr.Params{"reason": reason})
	}
	if !config.ValidNodeID(resp.NodeID) {
		return nil, nil, false, unusable("the hub assigned the invalid node id '" + resp.NodeID + "'")
	}
	cas, err := tlsutil.ParseCertChain([]byte(resp.CAPEM))
	if err != nil {
		return nil, nil, false, unusable("the CA certificate cannot be read")
	}
	for _, c := range cas {
		caPEM = append(caPEM, tlsutil.EncodeCertPEM(c.Raw)...)
	}
	chain, err := tlsutil.ParseCertChain([]byte(resp.CertPEM))
	if err != nil {
		return nil, nil, false, unusable("the node certificate cannot be read")
	}
	leaf := chain[0]
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	at := now
	if at.Before(leaf.NotBefore) {
		at, skewed = leaf.NotBefore, true
	} else if at.After(leaf.NotAfter) {
		at, skewed = leaf.NotAfter, true
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return nil, nil, false, deyerr.Wrap(deyerr.N002, err, deyerr.Params{"expected": fingerprint, "got": fingerprint}).
			WithWhy("the node certificate from the hub is not signed by the pinned CA; this may be a wrong address or interception")
	}
	if leaf.Subject.CommonName != resp.NodeID {
		return nil, nil, false, unusable("the node certificate names '" + leaf.Subject.CommonName + "' instead of node id '" + resp.NodeID + "'")
	}
	key, err := tlsutil.ParsePrivateKey(keyPEM)
	if err != nil || !tlsutil.KeyMatchesCert(leaf, key) {
		return nil, nil, false, unusable("the node certificate was issued for another key")
	}
	for _, c := range chain {
		certPEM = append(certPEM, tlsutil.EncodeCertPEM(c.Raw)...)
	}
	return caPEM, certPEM, skewed, nil
}
