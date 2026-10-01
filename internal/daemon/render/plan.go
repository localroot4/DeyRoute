// Package render is the hub's planner (ARCHITECTURE.md §7, §7.1, §7.3): for
// every tunnel it computes the warm set — every rung of the ladder on every
// node of the tunnel, both sides — builds each backend.RenderInput (paths,
// control ports, secrets, decoy, network index), renders it, and packages
// the result as files + systemd drop-ins for the hub (HubWriter) and
// backend.render payloads for the nodes (NodePayload). It also assembles
// the hub firewall spec (FirewallSpec) and the canary inputs (CanaryPlan).
//
// Plan is deterministic and does no I/O of its own besides the injected
// callbacks (control-port and network-index allocation, secrets); a rung
// that cannot be rendered never fails the plan, it is reported in
// TunnelPlan.Skipped with its reason and DEY code (section 8: yellow
// warning, re-checked every 30 minutes by the hub).
package render

import (
	"bytes"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Names of the TLS copies placed in a rendered config directory (§7.1).
const (
	FileTLSCert = "tls-cert.pem"
	FileTLSKey  = "tls-key.pem"
	FileTLSP12  = "tls.p12"
	FileCA      = "ca.crt"
)

// CanaryDirName is the config directory element of canary units:
// /etc/deyroute/backends/<backend>/<tunnel>/canary/.
const CanaryDirName = "canary"

// SecretsSource is the secret material the planner needs; *secrets.Store
// implements it.
type SecretsSource interface {
	Token(tunnel string) (string, error)
	BackendKeys(tunnel, backendName string, gen func() (map[string]string, error)) (map[string]string, error)
	TunnelTLS(tunnel string, mode string, ips []net.IP, domain, customCert, customKey string) (secrets.TLSMaterial, error)
	PKCS12(tunnel string) (p12 []byte, password string, err error)
}

// Registry resolves transport ids; BackendRegistry uses the global
// backend registry.
type Registry interface {
	Lookup(id string) (backend.Backend, backend.Transport, error)
	Supports(id, proto string) bool
}

// BackendRegistry is the Registry of every backend registered with
// backend.Register (import internal/backend/all to get them all).
type BackendRegistry struct{}

// Lookup implements Registry with backend.Lookup.
func (BackendRegistry) Lookup(id string) (backend.Backend, backend.Transport, error) {
	return backend.Lookup(id)
}

// Supports reports whether transport id is registered and forwards proto.
func (BackendRegistry) Supports(id, proto string) bool {
	_, tr, err := backend.Lookup(id)
	return err == nil && tr.Supports(proto)
}

// Input is everything Plan needs for one tunnel.
type Input struct {
	Cfg    *config.Config
	Tunnel config.Tunnel
	Hub    config.HubInfo
	// Nodes maps node id → node (config nodes:).
	Nodes map[string]config.Node
	// CtlPort allocates the stable control port of a key (normally
	// state.Store.AllocCtlPort in 30000-31999).
	CtlPort func(key string) (int, error)
	// NetIndex allocates the per-tunnel network index (WireGuard
	// 10.77.<n>.0/30); nil = 0. It is called once per tunnel; on failure
	// NetIndex -1 is passed so only the transports that need an index fail
	// their Validate (and are skipped).
	NetIndex func(tunnel string) (int, error)
	Secrets  SecretsSource
	Registry Registry
	// Layout places backend binaries. Its Root must be the target root
	// ("" or "/" in production): the paths are written into unit files on
	// the hub and on the nodes.
	Layout install.Layout
	// Decoy is the selected reachable decoy SNI (Reality/Waterwall).
	Decoy string
	// FirstRun reports whether an instance has never been started (verbose
	// first-run logging, Waterwall); nil = false.
	FirstRun func(instance string) bool
	// CAPEM is the internal CA certificate, copied to every config dir as
	// ca.crt.
	CAPEM []byte
	// LookPath resolves the program of a backend.SystemBinary transport
	// (haproxy) on the hub; nil = never installed.
	LookPath func(name string) (string, error)
	// UDPProbe optionally reports the last UDP echo probe to node (section
	// 10); tested is false while none has completed. Rungs with NeedsUDP
	// are planned only after a passed probe (section 7.6): a failed probe,
	// or none yet, skips them with DEY-B007. nil = UDP is open to every node.
	UDPProbe func(node string) (passed, tested bool)
}

// Side is one rendered half of a candidate, ready to be written.
type Side struct {
	Instance  string // deyroute-tun@ instance
	ConfigDir string // absolute path on that machine
	// Files are relative to ConfigDir: the backend's rendered files plus the
	// TLS copies (ca.crt always; tls-cert.pem, tls-key.pem, tls.p12 when
	// the rendered side references them).
	Files      map[string][]byte
	Unit       backend.UnitSpec
	DropIn     []byte // systemd.RenderDropIn output
	Binds      []backend.PortUse
	NAT        []backend.NATRule
	Masquerade []string
	IPForward  bool
}

// Candidate is one warm (node, transport) of a tunnel with both sides.
type Candidate struct {
	Node        string
	TransportID string
	Transport   backend.Transport
	ControlPort int
	Backend     string
	Version     string // manifest version of the backend
	Hub         Side
	NodeSide    Side
}

// StateCandidate returns the failover identity of c.
func (c Candidate) StateCandidate() state.Candidate {
	return state.Candidate{Node: c.Node, Transport: c.TransportID}
}

// SkippedCandidate is a rung that could not be rendered for one node.
type SkippedCandidate struct {
	Node        string
	TransportID string
	Reason      string
	Code        deyerr.Code
	Err         error
}

// TLSInfo summarizes the tunnel TLS material used by the plan.
type TLSInfo struct {
	Mode       string
	CertSHA256 string
	NotAfter   string // RFC 3339 UTC
}

// TunnelPlan is the desired warm set of one tunnel.
type TunnelPlan struct {
	Tunnel     string
	Ladder     []string
	Candidates []Candidate
	Skipped    []SkippedCandidate
	// Backends maps backend name → manifest version needed on the hub and
	// the nodes (every resolved rung, skipped ones included so a later
	// re-check finds the binary installed).
	Backends map[string]string
	// NetIndex is the network index passed to the renderers.
	NetIndex int
	// TLS is set when a rung needed the tunnel certificate.
	TLS *TLSInfo
	// Warnings are yellow, non-fatal problems (DEY-T003 ACME fallback →
	// event acme_failed, DEY-T006 certificate expiring).
	Warnings []error
}

// Candidate returns the planned candidate for (node, transport).
func (p *TunnelPlan) Candidate(node, transportID string) (*Candidate, bool) {
	for i := range p.Candidates {
		if p.Candidates[i].Node == node && p.Candidates[i].TransportID == transportID {
			return &p.Candidates[i], true
		}
	}
	return nil, false
}

// ConfigDir returns /etc/deyroute/backends/<backend>/<tunnel>/<node>/<transportName>.
func ConfigDir(backendName, tunnel, node, transportName string) string {
	return path.Join(config.BackendsConfDir, backendName, tunnel, node, transportName)
}

// CanaryConfigDir returns /etc/deyroute/backends/<backend>/<tunnel>/canary.
func CanaryConfigDir(backendName, tunnel string) string {
	return path.Join(config.BackendsConfDir, backendName, tunnel, CanaryDirName)
}

// CanaryLoopbackKey and CanaryCtlKey are the control-port allocation keys
// of the canary's loopback listen port and its backend control port. They
// cannot collide with "<tunnel>/<node>/<backend>/<transport>" keys because
// transport ids always contain a slash.
func CanaryLoopbackKey(tunnel string) string { return tunnel + "/" + CanaryDirName + "/loopback" }

// CanaryCtlKey is the control-port key of the canary unit.
func CanaryCtlKey(tunnel string) string { return tunnel + "/" + CanaryDirName + "/ctl" }

// planner holds per-tunnel state shared by every candidate of one Plan.
type planner struct {
	in       Input
	reg      Registry
	token    string
	netIndex int

	keys    map[string]keysResult
	tlsDone bool
	tls     secrets.TLSMaterial
	tlsErr  error
	p12Done bool
	p12     []byte
	p12Pass string
	p12Err  error
}

type keysResult struct {
	keys map[string]string
	err  error
}

// Plan computes the warm set of in.Tunnel: for every node of the tunnel in
// order × every rung of the resolved ladder, both sides rendered. Rungs
// that fail (unknown transport, UDP blocked, allocation, secrets,
// Validate, Render, invalid unit) are listed in Skipped. Errors are only
// returned for problems that affect the whole tunnel: an invalid ladder
// (DEY-C009/C012), no or unknown nodes (DEY-C008/C010) or a token that
// cannot be read or created.
func Plan(in Input) (TunnelPlan, error) {
	p, ladder, err := newPlanner(in)
	if err != nil {
		return TunnelPlan{}, err
	}
	t := in.Tunnel
	plan := TunnelPlan{Tunnel: t.ID, Ladder: ladder, Backends: map[string]string{}, NetIndex: p.netIndex}
	nodes := make([]config.Node, 0, len(t.Nodes))
	for _, id := range unique(t.Nodes) {
		n, ok := in.Nodes[id]
		if !ok {
			return TunnelPlan{}, deyerr.New(deyerr.C010, deyerr.Params{"node": id, "tunnel": t.ID})
		}
		if n.ID == "" {
			n.ID = id
		}
		nodes = append(nodes, n)
	}
	for _, rung := range ladder {
		if b, _, err := p.reg.Lookup(rung); err == nil {
			plan.Backends[b.Name()] = b.Manifest().Version
		}
	}
	for _, node := range nodes {
		for _, rung := range ladder {
			c, skip := p.candidate(node, rung)
			if skip != nil {
				plan.Skipped = append(plan.Skipped, *skip)
				continue
			}
			plan.Candidates = append(plan.Candidates, c)
		}
	}
	if p.tlsDone && p.tlsErr == nil {
		plan.TLS = &TLSInfo{Mode: p.tls.Mode, CertSHA256: p.tls.CertSHA256, NotAfter: p.tls.NotAfter.UTC().Format("2006-01-02T15:04:05Z07:00")}
		if p.tls.Warning != nil {
			plan.Warnings = append(plan.Warnings, p.tls.Warning)
		}
	}
	return plan, nil
}

// newPlanner checks the tunnel-wide inputs and resolves the ladder.
func newPlanner(in Input) (*planner, []string, error) {
	t := in.Tunnel
	if len(t.Nodes) == 0 {
		return nil, nil, deyerr.New(deyerr.C008, deyerr.Params{"tunnel": t.ID})
	}
	if in.Cfg == nil || in.Secrets == nil || in.CtlPort == nil {
		return nil, nil, deyerr.Wrap(deyerr.X000, deyerr.Plain("render: Input.Cfg, Input.Secrets and Input.CtlPort are required"), nil)
	}
	reg := in.Registry
	if reg == nil {
		reg = BackendRegistry{}
	}
	ladder, err := in.Cfg.ResolveLadder(&t, reg.Supports)
	if err != nil {
		return nil, nil, err
	}
	// Config validation rejects duplicate rungs; a duplicate that slips
	// through (a hand-built Input) must not yield two candidates with the
	// same instance.
	ladder = unique(ladder)
	token, err := in.Secrets.Token(t.ID)
	if err != nil {
		return nil, nil, err
	}
	p := &planner{in: in, reg: reg, token: token, keys: map[string]keysResult{}}
	if in.NetIndex != nil {
		n, err := in.NetIndex(t.ID)
		if err != nil {
			n = -1
		}
		p.netIndex = n
	}
	return p, ladder, nil
}

// udpNotTested is the DEY-B007 reason of a UDP rung on a node whose UDP
// probe has not completed yet (offline since it joined, or the probe could
// not be sent); the probe runs when the node connects and on every
// 30-minute re-check.
const udpNotTested = "the UDP probe between hub and node has not run yet; the rung is used once it passes"

// skipped builds a SkippedCandidate from err (its DEY code, or fallback).
func skipped(node, rung string, err error, fallback deyerr.Code) *SkippedCandidate {
	c, reason := fallback, err.Error()
	if de := deyerr.As(err); de.Code != deyerr.X000 {
		c, reason = de.Code, de.Message()
		if why := de.Why(); why != "" {
			reason += ": " + why
		}
	}
	return &SkippedCandidate{Node: node, TransportID: rung, Reason: reason, Code: c, Err: err}
}

// candidate renders one (node, rung).
func (p *planner) candidate(node config.Node, rung string) (Candidate, *SkippedCandidate) {
	t := p.in.Tunnel
	b, tr, err := p.reg.Lookup(rung)
	if err != nil {
		return Candidate{}, skipped(node.ID, rung, err, deyerr.C005)
	}
	if tr.NeedsUDP && p.in.UDPProbe != nil {
		if passed, tested := p.in.UDPProbe(node.ID); !passed {
			e := deyerr.New(deyerr.B007, deyerr.Params{"transport": rung, "tunnel": t.ID})
			if !tested {
				e = e.WithWhy(udpNotTested)
			}
			return Candidate{}, skipped(node.ID, rung, e, deyerr.B007)
		}
	}
	ctl, err := p.in.CtlPort(state.Key(t.ID, node.ID, rung))
	if err != nil {
		return Candidate{}, skipped(node.ID, rung, err, deyerr.P020)
	}
	ri, err := p.renderInput(b, tr, node, ctl, ConfigDir(b.Name(), t.ID, node.ID, tr.Name))
	if err != nil {
		return Candidate{}, skipped(node.ID, rung, err, deyerr.B002)
	}
	instance := systemd.InstanceName(t.ID, node.ID, rung)
	ri.FirstRun = p.in.FirstRun != nil && p.in.FirstRun(instance)
	c, err := p.renderCandidate(b, tr, ri, instance)
	if err != nil {
		return Candidate{}, skipped(node.ID, rung, err, deyerr.B002)
	}
	c.Node = node.ID
	return c, nil
}

// renderCandidate validates and renders both sides of ri.
func (p *planner) renderCandidate(b backend.Backend, tr backend.Transport, ri backend.RenderInput, instance string) (Candidate, error) {
	if err := b.Validate(ri); err != nil {
		if de := deyerr.As(err); de.Code == deyerr.X000 {
			return Candidate{}, deyerr.Wrap(deyerr.B006, err, deyerr.Params{"transport": tr.ID(), "reason": err.Error()})
		}
		return Candidate{}, err
	}
	hub, err := p.side(b, tr, ri, backend.SideHub, instance)
	if err != nil {
		return Candidate{}, err
	}
	nodeSide, err := p.side(b, tr, ri, backend.SideNode, instance)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		Node:        ri.Node.ID,
		TransportID: tr.ID(),
		Transport:   tr,
		ControlPort: ri.ControlPort,
		Backend:     b.Name(),
		Version:     b.Manifest().Version,
		Hub:         hub,
		NodeSide:    nodeSide,
	}, nil
}

// renderInput assembles the side-independent RenderInput.
func (p *planner) renderInput(b backend.Backend, tr backend.Transport, node config.Node, ctl int, configDir string) (backend.RenderInput, error) {
	t := p.in.Tunnel
	m := b.Manifest()
	paths := backend.Paths{
		ConfigDir:  configDir,
		LogFile:    systemd.TunnelLogFile(t.ID),
		SelfBinary: config.BinaryPath,
	}
	if !m.Builtin && !m.System && m.Version != "" {
		paths.BinDir = p.in.Layout.BinDir(b.Name(), m.Version)
		paths.Binary = p.in.Layout.BinaryPath(b.Name(), m.Version, m.Binary())
	}
	if sb, ok := b.(backend.SystemBinary); ok && p.in.LookPath != nil {
		if name := sb.SystemBinary(tr); name != "" {
			if bin, err := p.in.LookPath(name); err == nil {
				paths.Binary = bin
			}
		}
	}
	sec := backend.Secrets{Token: p.token, ServerName: serverName(p.in.Hub)}
	tunnel := t.Clone()
	if kg, ok := b.(backend.KeyGenerator); ok {
		keys, err := p.backendKeys(b.Name(), tr, kg)
		if err != nil {
			return backend.RenderInput{}, err
		}
		sec.Keys = keys
	}
	if tr.NeedsTLS {
		mat, err := p.tunnelTLS()
		if err != nil {
			return backend.RenderInput{}, err
		}
		_, pass, err := p.pkcs12()
		if err != nil {
			return backend.RenderInput{}, err
		}
		sec.TLSCertFile = path.Join(configDir, FileTLSCert)
		sec.TLSKeyFile = path.Join(configDir, FileTLSKey)
		sec.TLSP12File = path.Join(configDir, FileTLSP12)
		sec.TLSP12Password = pass
		sec.TLSCertSHA256 = mat.CertSHA256
		// Backends see the mode actually served: an acme tunnel that fell
		// back to the internal certificate renders exactly like auto.
		tunnel.TLS.Mode = mat.Mode
	}
	if len(p.caBundle(tr)) > 0 {
		sec.CAFile = path.Join(configDir, FileCA)
	}
	return backend.RenderInput{
		Tunnel:      tunnel,
		Node:        node,
		Hub:         p.in.Hub,
		Transport:   tr,
		ControlPort: ctl,
		Secrets:     sec,
		Paths:       paths,
		ListenAddr:  listenAddr(p.in.Hub),
		Decoy:       p.in.Decoy,
		NetIndex:    p.netIndex,
	}, nil
}

func (p *planner) backendKeys(name string, tr backend.Transport, kg backend.KeyGenerator) (map[string]string, error) {
	if r, ok := p.keys[name]; ok {
		return r.keys, r.err
	}
	keys, err := p.in.Secrets.BackendKeys(p.in.Tunnel.ID, name, func() (map[string]string, error) { return kg.GenerateKeys(tr) })
	p.keys[name] = keysResult{keys: keys, err: err}
	return keys, err
}

func (p *planner) tunnelTLS() (secrets.TLSMaterial, error) {
	if !p.tlsDone {
		p.tlsDone = true
		t := p.in.Tunnel
		p.tls, p.tlsErr = p.in.Secrets.TunnelTLS(t.ID, t.TLS.Mode, hubIPs(p.in.Hub), p.in.Hub.Domain, t.TLS.CertFile, t.TLS.KeyFile)
	}
	return p.tls, p.tlsErr
}

func (p *planner) pkcs12() ([]byte, string, error) {
	if !p.p12Done {
		p.p12Done = true
		p.p12, p.p12Pass, p.p12Err = p.in.Secrets.PKCS12(p.in.Tunnel.ID)
	}
	return p.p12, p.p12Pass, p.p12Err
}

// side renders one side and adds the TLS copies it references.
func (p *planner) side(b backend.Backend, tr backend.Transport, ri backend.RenderInput, s backend.Side, instance string) (Side, error) {
	r, err := b.Render(ri, s)
	if err != nil {
		if de := deyerr.As(err); de.Code == deyerr.X000 {
			return Side{}, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": b.Name(), "transport": tr.Name})
		}
		return Side{}, err
	}
	if err := systemd.ValidateUnitSpec(r.Unit); err != nil {
		return Side{}, err
	}
	files := make(map[string][]byte, len(r.Files)+4)
	for name, data := range r.Files {
		files[name] = append([]byte(nil), data...)
	}
	addIfAbsent(files, FileCA, p.caBundle(tr))
	if tr.NeedsTLS {
		hay := haystack(r)
		if refers(hay, FileTLSCert) {
			addIfAbsent(files, FileTLSCert, p.tls.CertPEM)
		}
		if refers(hay, FileTLSKey) {
			addIfAbsent(files, FileTLSKey, p.tls.KeyPEM)
		}
		if refers(hay, FileTLSP12) {
			addIfAbsent(files, FileTLSP12, p.p12)
		}
	}
	return Side{
		Instance:   instance,
		ConfigDir:  ri.Paths.ConfigDir,
		Files:      files,
		Unit:       r.Unit,
		DropIn:     systemd.RenderDropIn(r.Unit, ri.Paths.ConfigDir, ri.Paths.LogFile),
		Binds:      append([]backend.PortUse(nil), r.Binds...),
		NAT:        append([]backend.NATRule(nil), r.NAT...),
		Masquerade: append([]string(nil), r.Masquerade...),
		IPForward:  r.IPForward,
	}, nil
}

// caBundle is the content of ca.crt for transport tr: the internal CA
// (Input.CAPEM), and — for a TLS transport whose tunnel certificate is not
// issued by the internal CA (tls.mode acme or custom actually in use) —
// the served certificate chain too. Clients that take ca.crt as their only
// trust root (chisel --tls-ca, gost caFile, rathole trusted_root) would
// otherwise reject a Let's Encrypt or owner certificate and every such
// rung would fail its probe; with the served chain in the pool the client
// still verifies the host name (ServerName) and accepts only that chain.
func (p *planner) caBundle(tr backend.Transport) []byte {
	out := append([]byte(nil), p.in.CAPEM...)
	if !tr.NeedsTLS || !p.tlsDone || p.tlsErr != nil || p.tls.Mode == "" || p.tls.Mode == config.TLSModeAuto {
		return out
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, p.tls.CertPEM...)
}

func addIfAbsent(files map[string][]byte, name string, data []byte) {
	if _, ok := files[name]; !ok && len(data) > 0 {
		files[name] = append([]byte(nil), data...)
	}
}

// haystack concatenates everything a side could use to reference a file:
// rendered file contents, command lines, environment and paths. Private
// key copies are only placed on a side that references them (a reverse
// transport's node-side client never gets the tunnel key).
func haystack(r backend.Rendered) []byte {
	var b bytes.Buffer
	for _, name := range backend.SortedKeys(r.Files) {
		b.Write(r.Files[name])
		b.WriteByte(0)
	}
	u := r.Unit
	for _, argv := range append(append([][]string{u.ExecStart}, u.ExecStartPre...), u.ExecStop...) {
		b.WriteString(strings.Join(argv, "\x00"))
		b.WriteByte(0)
	}
	for _, k := range backend.SortedKeys(u.Env) {
		b.WriteString(k + "=" + u.Env[k])
		b.WriteByte(0)
	}
	b.WriteString(strings.Join(u.ReadWritePaths, "\x00"))
	return b.Bytes()
}

func refers(hay []byte, name string) bool { return bytes.Contains(hay, []byte(name)) }

// unique returns list without repeated elements, in first-seen order.
func unique(list []string) []string {
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, x := range list {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// serverName is the SNI / verify name of tunnel TLS: the hub domain, else
// its public IP.
func serverName(h config.HubInfo) string {
	if d := strings.TrimSpace(h.Domain); d != "" {
		return d
	}
	return strings.TrimSpace(h.PublicIP)
}

// listenAddr is "::" (dual stack) when the hub has a public IPv6, else
// "0.0.0.0" (section 10).
func listenAddr(h config.HubInfo) string {
	if strings.TrimSpace(h.PublicIP6) != "" {
		return "::"
	}
	return "0.0.0.0"
}

// hubIPs returns the parsed public addresses of the hub (TLS SANs).
func hubIPs(h config.HubInfo) []net.IP {
	var out []net.IP
	for _, s := range []string{h.PublicIP, h.PublicIP6} {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

// CanaryPlan renders the canary unit of section 9 (phase 8): rung 1 of the
// ladder on the primary node with Canary=true, its own control port
// (CanaryCtlKey) and one synthetic port map: a loopback listen port on the
// hub (allocated with CanaryLoopbackKey, bound on 127.0.0.1) forwarded to
// the node's built-in echo server on 127.0.0.1:echoPort. The port map is
// TCP when rung 1 forwards TCP, otherwise UDP. Both sides use instance
// systemd.CanaryInstance(tunnel) and /etc/deyroute/backends/<backend>/<tunnel>/canary.
// Unlike Plan, any failure is returned as an error.
func CanaryPlan(in Input, echoPort int) (*Candidate, error) {
	if echoPort < 1 || echoPort > 65535 {
		return nil, deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(echoPort)})
	}
	p, ladder, err := newPlanner(in)
	if err != nil {
		return nil, err
	}
	t := in.Tunnel
	primary := t.Nodes[0]
	node, ok := in.Nodes[primary]
	if !ok {
		return nil, deyerr.New(deyerr.C010, deyerr.Params{"node": primary, "tunnel": t.ID})
	}
	if node.ID == "" {
		node.ID = primary
	}
	rung := ladder[0]
	b, tr, err := p.reg.Lookup(rung)
	if err != nil {
		return nil, err
	}
	loop, err := in.CtlPort(CanaryLoopbackKey(t.ID))
	if err != nil {
		return nil, err
	}
	ctl, err := in.CtlPort(CanaryCtlKey(t.ID))
	if err != nil {
		return nil, err
	}
	proto := config.ProtoTCP
	if !tr.Supports(config.ProtoTCP) {
		proto = config.ProtoUDP
	}
	pm := config.PortMap{Listen: loop, Proto: proto, Target: backend.HostPort("127.0.0.1", echoPort)}
	if proto == config.ProtoTCP {
		pm.Probe = config.ProbeTCP // the echo server is not TLS
	}
	p.in.Tunnel.Ports = []config.PortMap{pm}
	p.in.Tunnel.ProbePort = 0
	ri, err := p.renderInput(b, tr, node, ctl, CanaryConfigDir(b.Name(), t.ID))
	if err != nil {
		return nil, err
	}
	ri.Canary = true
	ri.ListenAddr = "127.0.0.1"
	instance := systemd.CanaryInstance(t.ID)
	ri.FirstRun = in.FirstRun != nil && in.FirstRun(instance)
	c, err := p.renderCandidate(b, tr, ri, instance)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Desired returns the hub instances of every planned candidate.
func Desired(plans []TunnelPlan) map[string]bool {
	out := map[string]bool{}
	for _, p := range plans {
		for _, c := range p.Candidates {
			out[c.Hub.Instance] = true
		}
	}
	return out
}

// Stale returns the instances in existing that are not desired (sorted,
// unique): warm units of removed tunnels, nodes or rungs. Add canary
// instances to desired when they should stay.
func Stale(existing []systemd.UnitState, desired map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range existing {
		inst := u.Instance
		if inst == "" {
			if i, ok := systemd.InstanceOf(u.Unit); ok {
				inst = i
			}
		}
		if inst == "" || desired[inst] || seen[inst] {
			continue
		}
		seen[inst] = true
		out = append(out, inst)
	}
	sort.Strings(out)
	return out
}
