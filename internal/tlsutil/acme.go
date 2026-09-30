package tlsutil

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
)

// ACME defaults (section 10).
const (
	// DefaultACMEHTTPPort is where the HTTP-01 challenge is served.
	DefaultACMEHTTPPort = 80
	// DefaultACMETimeout bounds ObtainACME when ctx has no deadline.
	DefaultACMETimeout = 5 * time.Minute
	// ACMEProduction and ACMEStaging are the Let's Encrypt directories.
	ACMEProduction = lego.LEDirectoryProduction
	ACMEStaging    = lego.LEDirectoryStaging

	acmeKeyFile        = "account.key"
	acmeAccountFile    = "account.json"
	acmeResolveTimeout = 10 * time.Second
	acmeRequestTimeout = 2 * time.Minute
	acmeUserAgent      = "deyroute"
)

// ACMEOptions configures ObtainACME.
type ACMEOptions struct {
	// Domain is hub.domain; it must resolve (DNS only, no CDN proxy) to
	// ExpectedIP.
	Domain string
	// Email is the optional ACME account contact.
	Email string
	// AccountDir holds the ACME account key and registration (files 0600,
	// directory 0700), one sub-directory per ACME directory
	// (production/staging). Required.
	AccountDir string
	// CacheDir is the ARCHITECTURE.md name of AccountDir; it is used when
	// AccountDir is empty.
	CacheDir string
	// Staging uses the Let's Encrypt staging directory.
	Staging bool
	// HTTPPort serves the HTTP-01 challenge (default 80). Ignored with
	// CloudflareToken.
	HTTPPort int
	// CloudflareToken switches to DNS-01 through the Cloudflare API (an API
	// token with Zone:DNS:Edit). Empty = HTTP-01.
	CloudflareToken string
	// ExpectedIP is the hub public IP the domain must resolve to (DEY-T004).
	// Empty skips the DNS pre-flight check.
	ExpectedIP string
	// Resolver looks up host addresses; nil = net.DefaultResolver.LookupHost.
	Resolver func(ctx context.Context, host string) ([]string, error)
	// DirectoryURL overrides the ACME directory (another CA, tests).
	DirectoryURL string
	// HTTPClient is the base client for ACME and Cloudflare API calls; nil
	// = a client that honours https_proxy.
	HTTPClient *http.Client
	// PortFree reports whether HTTPPort can be bound; nil = try to listen.
	PortFree func(port int) bool
}

// acmeSlot serialises ObtainACME within the process.
var acmeSlot = make(chan struct{}, 1)

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidDomain reports whether s is a plain DNS name ACME can issue for
// (no wildcard, no IP, at least two labels, at most 253 bytes).
func ValidDomain(s string) bool {
	return len(s) <= 253 && domainRe.MatchString(s)
}

func (o ACMEOptions) accountDir() string {
	if o.AccountDir != "" {
		return o.AccountDir
	}
	return o.CacheDir
}

func (o ACMEOptions) directory() string {
	switch {
	case o.DirectoryURL != "":
		return o.DirectoryURL
	case o.Staging:
		return ACMEStaging
	default:
		return ACMEProduction
	}
}

// accountSubdir names the per-directory account folder.
func accountSubdir(directory string) string {
	switch directory {
	case ACMEProduction:
		return "production"
	case ACMEStaging:
		return "staging"
	default:
		sum := sha256.Sum256([]byte(directory))
		return "custom-" + hex.EncodeToString(sum[:6])
	}
}

// ObtainACME obtains a certificate for o.Domain from Let's Encrypt (section
// 10, tls.mode acme): it checks that the domain resolves to the hub
// (DEY-T004), that port 80 is free for HTTP-01 (or uses Cloudflare DNS-01
// when a token is given), registers or reuses the account stored in
// AccountDir and requests an ECDSA P-256 certificate. It returns the full
// chain (leaf first) and the PKCS#8 key. Every other failure is DEY-T003;
// the caller then falls back to auto mode and emits acme_failed.
func ObtainACME(ctx context.Context, o ACMEOptions) (certPEM, keyPEM []byte, err error) {
	// The token may surface in lego or Cloudflare error texts that end up in
	// DEY errors and logs (sections 4 and 11).
	deylog.RegisterSecret(o.CloudflareToken)
	o.Domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(o.Domain)), ".")
	if !ValidDomain(o.Domain) {
		return nil, nil, acmeErr(o.Domain, nil).WithWhy("'" + o.Domain + "' is not a valid domain name (no IPs, wildcards or single labels)")
	}
	if o.accountDir() == "" {
		return nil, nil, internalErr("obtain ACME certificate", deyerr.Plain("no ACME account directory given"))
	}
	if o.HTTPPort == 0 {
		o.HTTPPort = DefaultACMEHTTPPort
	}
	if o.CloudflareToken == "" && (o.HTTPPort < 1 || o.HTTPPort > 65535) {
		// HTTPPort is ignored with DNS-01.
		return nil, nil, acmeErr(o.Domain, nil).WithWhy("the HTTP-01 port " + strconv.Itoa(o.HTTPPort) + " is not a valid port")
	}
	// Every HTTP request below is bound to ctx; cancelling it on return
	// also tears down connections whose bodies lego left open.
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); ok {
		ctx, cancel = context.WithCancel(ctx)
	} else {
		ctx, cancel = context.WithTimeout(ctx, DefaultACMETimeout)
	}
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, nil, acmeErr(o.Domain, err)
	}
	// One issuance at a time: the HTTP-01 server needs the port for itself
	// and concurrent runs would race on the account files.
	select {
	case acmeSlot <- struct{}{}:
		defer func() { <-acmeSlot }()
	case <-ctx.Done():
		return nil, nil, acmeErr(o.Domain, ctx.Err())
	}
	if o.ExpectedIP != "" {
		if err := CheckDomainResolves(ctx, o.Domain, o.ExpectedIP, o.Resolver); err != nil {
			return nil, nil, err
		}
	}
	if o.CloudflareToken == "" {
		free := o.PortFree
		if free == nil {
			free = tcpPortFree
		}
		if !free(o.HTTPPort) {
			return nil, nil, acmeErr(o.Domain, nil).
				WithWhy("port " + strconv.Itoa(o.HTTPPort) + "/tcp is in use, so the HTTP-01 challenge cannot be answered; tunnel TLS fell back to auto").
				WithFix("free port " + strconv.Itoa(o.HTTPPort) + " (or set a Cloudflare API token for DNS-01 in Advanced settings), then: deyroute security tls renew")
		}
	}

	dir := o.directory()
	store := accountStore{dir: filepath.Join(o.accountDir(), accountSubdir(dir))}
	user, err := store.load(o.Email)
	if err != nil {
		return nil, nil, err
	}

	client, closeIdle := o.boundClient(ctx)
	defer closeIdle()

	cfg := lego.NewConfig(user)
	cfg.CADirURL = dir
	cfg.HTTPClient = client
	cfg.UserAgent = acmeUserAgent
	cfg.Certificate.KeyType = certcrypto.EC256
	cfg.Certificate.Timeout = time.Minute
	lc, err := lego.NewClient(cfg)
	if err != nil {
		return nil, nil, acmeErr(o.Domain, err)
	}
	if o.CloudflareToken != "" {
		cf := cloudflare.NewDefaultConfig()
		cf.AuthToken = o.CloudflareToken
		cf.HTTPClient = client
		provider, err := cloudflare.NewDNSProviderConfig(cf)
		if err != nil {
			return nil, nil, acmeErr(o.Domain, err)
		}
		if err := lc.Challenge.SetDNS01Provider(provider); err != nil {
			return nil, nil, acmeErr(o.Domain, err)
		}
	} else {
		if err := lc.Challenge.SetHTTP01Provider(http01.NewProviderServer("", strconv.Itoa(o.HTTPPort))); err != nil {
			return nil, nil, acmeErr(o.Domain, err)
		}
	}

	if user.reg == nil {
		reg, err := lc.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, nil, acmeErr(o.Domain, err)
		}
		user.reg = reg
		if err := store.saveRegistration(user, dir); err != nil {
			return nil, nil, err
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, internalErr("generate ECDSA key", err)
	}
	res, err := lc.Certificate.Obtain(certificate.ObtainRequest{
		Domains:    []string{o.Domain},
		PrivateKey: key,
		Bundle:     true,
	})
	if err != nil {
		return nil, nil, acmeErr(o.Domain, err)
	}
	chain, err := parseCerts(res.Certificate)
	if err != nil {
		return nil, nil, acmeErr(o.Domain, err)
	}
	if !KeyMatchesCert(chain[0], key) {
		return nil, nil, acmeErr(o.Domain, deyerr.Plain("the issued certificate does not match the requested key"))
	}
	keyPEM, err = EncodeKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	return res.Certificate, keyPEM, nil
}

// CheckDomainResolves verifies that domain resolves to expectedIP (section
// 10: ACME needs a DNS-only record pointing at the hub). A lookup failure, a
// record pointing elsewhere (e.g. a CDN proxy) or an extra address of the
// same family next to the hub's (Let's Encrypt may validate against any of
// them) is DEY-T004; the addresses found are attached as detail. Addresses
// of the other family are not judged: the hub's other address is unknown
// here.
func CheckDomainResolves(ctx context.Context, domain, expectedIP string, resolve func(ctx context.Context, host string) ([]string, error)) error {
	expectedIP = strings.TrimSpace(expectedIP)
	params := deyerr.Params{"domain": domain, "ip": expectedIP}
	want := net.ParseIP(expectedIP)
	if want == nil {
		return deyerr.New(deyerr.T004, params).WithWhy("the hub public IP '" + expectedIP + "' is not a valid IP address")
	}
	if resolve == nil {
		resolve = net.DefaultResolver.LookupHost
	}
	rctx, cancel := context.WithTimeout(ctx, acmeResolveTimeout)
	defer cancel()
	addrs, err := resolve(rctx, domain)
	if err != nil {
		return deyerr.Wrap(deyerr.T004, err, params).WithWhy("the DNS lookup of " + domain + " failed: " + err.Error())
	}
	wantV4 := want.To4() != nil
	matched := false
	var stray []string
	for _, a := range addrs {
		ip := net.ParseIP(strings.TrimSpace(a))
		switch {
		case ip == nil:
		case ip.Equal(want):
			matched = true
		case (ip.To4() != nil) == wantV4:
			stray = append(stray, a)
		}
	}
	if matched && len(stray) == 0 {
		return nil
	}
	found := "no addresses"
	if len(addrs) > 0 {
		found = strings.Join(addrs, ", ")
	}
	e := deyerr.New(deyerr.T004, params).WithDetail(domain + " resolves to: " + found)
	if matched {
		e = e.WithWhy("besides " + expectedIP + " its DNS records also point to " + strings.Join(stray, ", ") + ", which Let's Encrypt may validate against").
			WithFix("keep only the record for " + expectedIP + " (DNS only) and wait for propagation")
	}
	return e
}

func acmeErr(domain string, err error) *deyerr.Error {
	return deyerr.Wrap(deyerr.T003, err, deyerr.Params{"domain": domain})
}

// tcpPortFree reports whether port can be bound on all interfaces (the
// HTTP-01 server listens there).
func tcpPortFree(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port)) // #nosec G102 -- probing the port the HTTP-01 server must bind on all interfaces
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// boundClient returns an HTTP client whose every request is bound to ctx,
// so cancelling ctx stops lego's (context-free) ACME and Cloudflare calls,
// and a func closing its idle connections.
//
// http.Client.Timeout is deliberately not used: it starts a timer goroutine
// per request that outlives requests whose bodies lego never closes. The
// per-request limit is a child context instead (no goroutine).
func (o ACMEOptions) boundClient(ctx context.Context) (*http.Client, func()) {
	var base http.RoundTripper
	timeout := acmeRequestTimeout
	if o.HTTPClient != nil {
		base = o.HTTPClient.Transport
		if o.HTTPClient.Timeout > 0 {
			timeout = o.HTTPClient.Timeout
		}
	}
	if base == nil {
		base = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		}
	}
	closeIdle := func() {
		if ci, ok := base.(interface{ CloseIdleConnections() }); ok {
			ci.CloseIdleConnections()
		}
	}
	return &http.Client{Transport: &ctxTransport{ctx: ctx, base: base, timeout: timeout}}, closeIdle
}

// ctxTransport binds every request to ctx and limits each one to timeout
// (until its body is closed).
type ctxTransport struct {
	ctx     context.Context
	base    http.RoundTripper
	timeout time.Duration
}

// RoundTrip implements http.RoundTripper. Like every RoundTripper it closes
// the request body, also when it fails before calling the base transport.
func (t *ctxTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	ctx, cancel := context.WithTimeout(t.ctx, t.timeout)
	resp, err := t.base.RoundTrip(r.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose releases the per-request context when the body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close implements io.Closer.
func (b *cancelOnClose) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// acmeUser implements registration.User.
type acmeUser struct {
	email string
	key   crypto.Signer
	reg   *registration.Resource
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// acmeAccount is the JSON stored in account.json.
type acmeAccount struct {
	Email     string `json:"email"`
	Directory string `json:"directory"`
	// KeyFingerprint is the Fingerprint of the account public key (PKIX
	// DER); a registration is reused only with the key it was made for.
	KeyFingerprint string                 `json:"key_fingerprint"`
	Registration   *registration.Resource `json:"registration"`
}

// publicKeyFingerprint returns Fingerprint of the PKIX encoding of the
// public half of key.
func publicKeyFingerprint(key crypto.Signer) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return "", internalErr("encode ACME account key", err)
	}
	return Fingerprint(der), nil
}

// accountStore persists one ACME account (key + registration) in dir.
type accountStore struct{ dir string }

// load returns the stored account, creating and saving a new ECDSA P-256
// account key when none exists. The registration is reused only when it
// was made for the same email and the same key; otherwise the account
// registers again (an ACME server answers a known key with its existing
// account).
func (s accountStore) load(email string) (*acmeUser, error) {
	keyPath := filepath.Join(s.dir, acmeKeyFile)
	u := &acmeUser{email: email}
	keyPEM, err := os.ReadFile(keyPath) // #nosec G304 -- deyroute's own secrets directory
	switch {
	case err == nil:
		k, perr := parseKey(keyPEM)
		if perr != nil {
			return nil, parseErr(keyPath, perr)
		}
		u.key = k
	case os.IsNotExist(err):
		k, gerr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if gerr != nil {
			return nil, internalErr("generate ACME account key", gerr)
		}
		pemBytes, eerr := EncodeKeyPEM(k)
		if eerr != nil {
			return nil, eerr
		}
		if werr := WriteSecret(keyPath, pemBytes); werr != nil {
			return nil, werr
		}
		u.key = k
		return u, nil
	default:
		return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": keyPath, "reason": "the file cannot be read"})
	}
	accPath := filepath.Join(s.dir, acmeAccountFile)
	data, err := os.ReadFile(accPath) // #nosec G304 -- deyroute's own secrets directory
	if err != nil {
		if os.IsNotExist(err) {
			return u, nil
		}
		return nil, deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": accPath, "reason": "the file cannot be read"})
	}
	var acc acmeAccount
	if err := json.Unmarshal(data, &acc); err != nil {
		// A damaged registration is recreated; the key stays the same.
		return u, nil
	}
	fp, err := publicKeyFingerprint(u.key)
	if err != nil {
		return nil, err
	}
	if acc.Registration != nil && acc.Registration.URI != "" && acc.Email == email && acc.KeyFingerprint == fp {
		u.reg = acc.Registration
	}
	return u, nil
}

// saveRegistration writes account.json (0600).
func (s accountStore) saveRegistration(u *acmeUser, directory string) error {
	fp, err := publicKeyFingerprint(u.key)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(acmeAccount{Email: u.email, Directory: directory, KeyFingerprint: fp, Registration: u.reg}, "", "  ")
	if err != nil {
		return internalErr("encode ACME account", err)
	}
	return WriteSecret(filepath.Join(s.dir, acmeAccountFile), append(data, '\n'))
}
