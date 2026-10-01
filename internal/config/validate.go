package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ValidateOptions carries the knowledge config itself does not have (the
// backend registry imports config, so config cannot import it).
type ValidateOptions struct {
	// KnownTransport reports whether a transport id is registered
	// (backend.KnownTransport). nil skips the check (only the id shape
	// "backend/name" is verified).
	KnownTransport func(id string) bool
	// ValidTransports lists the registered ids for the DEY-C005 hint
	// (backend.ValidIDs). nil falls back to the builtin ladder rungs.
	ValidTransports func() []string
	// ReservedPorts are extra ports no tunnel may listen on. 22, the hub
	// control_port and 30000-31999 are always reserved.
	ReservedPorts []int
}

// Enumerations accepted by Validate (shared with the UI for pickers). Do not
// modify them.
var (
	Roles          = []string{RoleHub, RoleNode}
	UIModes        = []string{UIModeSimple, UIModeAdvanced}
	Policies       = []string{PolicyTransportOnly, PolicyTransportThenNode, PolicyNodeOnly}
	TLSModes       = []string{TLSModeAuto, TLSModeACME, TLSModeCustom}
	Protos         = []string{ProtoTCP, ProtoUDP}
	ProbeKinds     = []string{ProbeAuto, ProbeTCP, ProbeTLS, ProbeHTTP}
	SysctlProfiles = []string{SysctlOff, SysctlBalanced, SysctlAggressive}
	// TelegramEventAliases are the short names allowed in
	// hub.notify.telegram.events; internal/notify maps each alias to the
	// event names of section 9 (e.g. switch → switch_transport + switch_node).
	TelegramEventAliases = []string{
		"down", "up", "degraded", "switch", "failback", "node_offline", "node_online",
		"flapping", "service_down", "backend_crash", "probe_error", "update", "manual_switch",
	}
	// TelegramEventNames are the full event names (section 9, plus
	// acme_failed of section 10 and the other internal/state event types)
	// that hub.notify.telegram.events also accepts, so events without an
	// alias (acme_failed, node_ip_changed, …) can be selected. The union of
	// both lists equals notify.Valid() (enforced by a test).
	TelegramEventNames = []string{
		"tunnel_up", "tunnel_degraded", "tunnel_down", "switch_transport", "switch_node",
		"failback", "failback_failed", "flapping", "node_online", "node_offline",
		"service_down", "backend_crash", "probe_error", "update_applied", "update_rolled_back",
		"backend_update_rolled_back", "node_ip_changed", "acme_failed", "rung_skipped",
		"rung_restored", "config_applied",
	}
)

// ValidTelegramEvent reports whether e may appear in
// hub.notify.telegram.events (an alias or a full event name).
func ValidTelegramEvent(e string) bool {
	return contains(TelegramEventAliases, e) || contains(TelegramEventNames, e)
}

// SpecPlaceholderFingerprint is the literal cert_fingerprint value of the
// section 4 sample. It is accepted in hub nodes: entries so the sample
// validates unchanged; the node's pinned hub_ca_fingerprint never accepts it.
const SpecPlaceholderFingerprint = "sha256:..."

// hiddenValue replaces a rejected value that may be a secret (for example a
// bot token pasted into bot_token_file) in DEY-C013, so it never reaches the
// screen or a log (section 4: secrets are never printed).
const hiddenValue = "***"

// Limits used by Validate.
const (
	SSHPort            = 22
	MaxNameLen         = 64
	MaxProbeIntervalS  = 3600
	MaxProbeTimeoutS   = 60
	MaxThreshold       = 100
	MaxFailbackAfterS  = 86400 // section 9: doubled failback_after_s is capped at 24h
	MaxSwitchesPerHour = 60
	MaxQuarantineS     = 3600 // section 9: quarantine doubles up to 1 hour
	MaxConnectionPool  = 1024
	MaxHysteriaMbps    = 100000
	MaxRenewBeforeDays = 89
)

var (
	transportIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)
	fingerprintRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	languageRe    = regexp.MustCompile(`^[a-z]{2}$`)
	chatIDRe      = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z0-9_]{5,64})$`)
	hostLabelRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	allDigitsRe   = regexp.MustCompile(`^[0-9]+$`)
)

// ValidFingerprint reports whether s is "sha256:" followed by 64 lowercase
// hex characters (tlsutil.Fingerprint format).
func ValidFingerprint(s string) bool { return fingerprintRe.MatchString(s) }

// ValidHostPort reports whether s is host:port with a port in 1-65535 written
// in canonical decimal (no sign, no leading zero, since the value is copied
// verbatim into backend configs) and a host that is an IP address or a DNS
// name.
func ValidHostPort(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" {
		return false
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != port {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	return validHostname(host)
}

// ValidDomain reports whether s is a DNS name with at least two labels that
// is not an IP address (hub.domain, decoy SNIs).
func ValidDomain(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return false
	}
	return strings.Contains(strings.TrimSuffix(s, "."), ".") && validHostname(s)
}

func validHostname(h string) bool {
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if !hostLabelRe.MatchString(l) {
			return false
		}
	}
	// A numeric last label is a mistyped IP address, not a name.
	return !allDigitsRe.MatchString(labels[len(labels)-1])
}

// validPublicIP reports whether s can be the address of a server: a literal
// IPv4 or IPv6 address without zone that is not unspecified (0.0.0.0, ::),
// multicast or IPv4-mapped IPv6 (it would land in the wrong nftables set).
func validPublicIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Zone() == "" && !a.IsUnspecified() && !a.IsMulticast() && !a.Is4In6()
}

// hasControl reports whether s contains a control character (newline, tab,
// escape…), which would break rendered unit files and backend configs.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ReservedListen reports whether a tunnel may not listen on port and why
// (section 10: never 22, the control port or 30000-31999; plus extra).
func ReservedListen(port, controlPort int, extra []int) (bool, string) {
	switch {
	case port == SSHPort:
		return true, "port 22 is reserved for SSH"
	case controlPort > 0 && port == controlPort:
		return true, fmt.Sprintf("port %d is the hub control port (hub.control_port)", controlPort)
	case port >= CtlRangeLow && port <= CtlRangeHigh:
		return true, fmt.Sprintf("ports %d-%d are reserved for backend control ports", CtlRangeLow, CtlRangeHigh)
	}
	for _, p := range extra {
		if p == port {
			return true, fmt.Sprintf("port %d is reserved on this server", port)
		}
	}
	return false, ""
}

// Validate checks every DEY-C rule of section 4 and returns all problems at
// once (errors.Join of *errors.Error values in a stable order: schema, role,
// hub, nodes, ladders, tunnels, tuning, node), or nil. It does not apply
// defaults; Parse/Load call ApplyDefaults first. UDP-requiring rungs are not
// a config error (runtime skip, section 4). A nil config is DEY-C016 (it has
// neither a role nor sections).
func (c *Config) Validate(opt ValidateOptions) error {
	if c == nil {
		return deyerr.New(deyerr.C016, deyerr.Params{"role": ""})
	}
	v := &validator{c: c, opt: opt, listen: map[ListenKey]string{}, webPorts: map[int]string{}}
	if c.SchemaVersion != currentSchema {
		v.add(deyerr.New(deyerr.C019, deyerr.Params{"version": c.SchemaVersion}))
	}
	v.role()
	if c.Hub != nil {
		v.hub(c.Hub)
	}
	v.nodes()
	v.ladders()
	for i := range c.Tunnels {
		v.tunnel(i, &c.Tunnels[i])
	}
	if c.Tuning != nil && !contains(SysctlProfiles, c.Tuning.SysctlProfile) {
		v.bad("tuning.sysctl_profile", c.Tuning.SysctlProfile, strings.Join(SysctlProfiles, ", "))
	}
	if c.Node != nil {
		v.nodeSelf(c.Node)
	}
	return errors.Join(v.errs...)
}

type validator struct {
	c        *Config
	opt      ValidateOptions
	errs     []error
	listen   map[ListenKey]string // listen/proto → first tunnel id
	webPorts map[int]string       // advanced.backhaul_web_port → first tunnel id
}

// add records a DEY error with every string parameter made printable.
func (v *validator) add(e *deyerr.Error) { v.errs = append(v.errs, printableParams(e)) }

// printableParams escapes control characters (newline, ESC…) and invalid
// UTF-8 in the string parameters of e. Values come from config.yaml; printed
// raw they would break the three-line error block or inject terminal escape
// sequences.
func printableParams(e *deyerr.Error) *deyerr.Error {
	for k, val := range e.Params {
		if s, ok := val.(string); ok && (hasControl(s) || !utf8.ValidString(s)) {
			q := strconv.Quote(s)
			e.Params[k] = q[1 : len(q)-1]
		}
	}
	return e
}

// bad records DEY-C013 for field.
func (v *validator) bad(field string, value any, allowed string) {
	v.add(deyerr.New(deyerr.C013, deyerr.Params{"field": field, "value": value, "allowed": allowed}))
}

func (v *validator) controlPort() int {
	if v.c.Hub == nil {
		return 0
	}
	return v.c.Hub.ControlPort
}

func (v *validator) role() {
	c := v.c
	switch c.Role {
	case RoleHub:
		if c.Hub == nil || c.Node != nil {
			v.add(deyerr.New(deyerr.C016, deyerr.Params{"role": c.Role}))
		}
	case RoleNode:
		if c.Node == nil || c.Hub != nil || len(c.Nodes) > 0 || len(c.Tunnels) > 0 || len(c.Ladders) > 0 {
			v.add(deyerr.New(deyerr.C016, deyerr.Params{"role": c.Role}))
		}
	default:
		v.bad("role", c.Role, strings.Join(Roles, ", "))
	}
}

// name checks an optional display name (no control characters, ≤ 64 runes).
func (v *validator) name(field, s string, required bool) {
	if s == "" {
		if required {
			v.bad(field, s, "a non-empty name")
		}
		return
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxNameLen || hasControl(s) {
		v.bad(field, s, fmt.Sprintf("up to %d printable characters", MaxNameLen))
	}
}

// absPath checks a file path: absolute and free of control characters.
func (v *validator) absPath(field, p string, required bool) {
	if p == "" && !required {
		return
	}
	if !validAbsPath(p) {
		v.bad(field, p, "an absolute file path")
	}
}

// secretPath is absPath for a file that holds a secret (bot token, private
// key, API token). A rejected value is shown as "***": a common mistake is to
// paste the secret itself instead of its file name, and the error line must
// not print it.
func (v *validator) secretPath(field, p string, required bool) {
	if p == "" && !required {
		return
	}
	if !validAbsPath(p) {
		shown := p
		if p != "" {
			shown = hiddenValue
		}
		v.bad(field, shown, "an absolute path of the file that holds the secret, e.g. "+filepath.Join(SecretsDir, "<name>"))
	}
}

func validAbsPath(p string) bool {
	return p != "" && filepath.IsAbs(p) && !hasControl(p)
}

func (v *validator) intRange(field string, n, lo, hi int) {
	if n < lo || n > hi {
		v.bad(field, n, fmt.Sprintf("%d-%d", lo, hi))
	}
}

func (v *validator) hub(h *Hub) {
	v.name("hub.name", h.Name, true)
	switch {
	case h.ControlPort < 1 || h.ControlPort > 65535:
		v.bad("hub.control_port", h.ControlPort, "1-65535")
	case h.ControlPort == SSHPort || (h.ControlPort >= CtlRangeLow && h.ControlPort <= CtlRangeHigh):
		v.bad("hub.control_port", h.ControlPort, fmt.Sprintf("1-65535 except 22 and %d-%d", CtlRangeLow, CtlRangeHigh))
	}
	if !validPublicIP(h.PublicIP) {
		v.bad("hub.public_ip", h.PublicIP, "the public IPv4 or IPv6 address of this server")
	}
	if h.PublicIP6 != "" {
		if a, err := netip.ParseAddr(h.PublicIP6); err != nil || !a.Is6() || !validPublicIP(h.PublicIP6) {
			v.bad("hub.public_ip6", h.PublicIP6, "the public IPv6 address of this server")
		}
	}
	if h.Domain != "" && !ValidDomain(h.Domain) {
		v.bad("hub.domain", h.Domain, "a DNS name such as tunnel.example.com, or empty")
	}
	if !contains(UIModes, h.UIMode) {
		v.bad("hub.ui_mode", h.UIMode, strings.Join(UIModes, ", "))
	}
	if h.Language != "" && !languageRe.MatchString(h.Language) {
		v.bad("hub.language", h.Language, "a two-letter language code such as en")
	}
	for i, s := range h.DecoySNIs {
		if !ValidDomain(s) {
			v.bad(fmt.Sprintf("hub.decoy_snis[%d]", i), s, "a DNS name such as www.example.com")
		}
	}
	if h.Mirror != "" {
		u, err := url.Parse(h.Mirror)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			v.bad("hub.mirror", h.Mirror, "an http(s) URL such as https://mirror.example.com/deyroute")
		}
	}
	tg := h.Notify.Telegram
	v.secretPath("hub.notify.telegram.bot_token_file", tg.BotTokenFile, true)
	if tg.ChatID != "" && !chatIDRe.MatchString(tg.ChatID) {
		v.bad("hub.notify.telegram.chat_id", tg.ChatID, "a numeric chat id (e.g. -1001234567890) or @channel")
	}
	if tg.Enabled && tg.ChatID == "" {
		v.bad("hub.notify.telegram.chat_id", tg.ChatID, "a chat id (required when telegram is enabled)")
	}
	for i, e := range tg.Events {
		if !ValidTelegramEvent(e) {
			v.bad(fmt.Sprintf("hub.notify.telegram.events[%d]", i), e,
				strings.Join(TelegramEventAliases, ", ")+" (or a full event name such as acme_failed)")
		}
	}
	if a := h.ACME; a != nil {
		if a.Email != "" {
			if addr, err := mail.ParseAddress(a.Email); err != nil || addr.Address != a.Email {
				v.bad("hub.acme.email", a.Email, "an e-mail address such as owner@example.com")
			}
		}
		v.secretPath("hub.acme.cloudflare_token_file", a.CloudflareTokenFile, false)
		v.intRange("hub.acme.renew_before_days", a.RenewBeforeDays, 0, MaxRenewBeforeDays)
	}
}

func (v *validator) nodes() {
	seen := map[string]bool{}
	for i, n := range v.c.Nodes {
		p := itemPath("nodes", n.ID, i)
		switch {
		case !ValidNodeID(n.ID):
			v.add(deyerr.New(deyerr.C007, deyerr.Params{"kind": "node", "id": n.ID}))
		case seen[n.ID]:
			v.add(deyerr.New(deyerr.C002, deyerr.Params{"kind": "node", "id": n.ID}))
		}
		seen[n.ID] = true
		v.name(p+".name", n.Name, false)
		if !validPublicIP(n.PublicIP) {
			v.bad(p+".public_ip", n.PublicIP, "the public IPv4 or IPv6 address of the node")
		}
		// Recorded by the hub at join (tlsutil.Fingerprint). Empty (not
		// joined yet) and the literal placeholder of the section 4 sample are
		// accepted so that sample validates unchanged; anything else must be
		// a real fingerprint (the node's pinned hub_ca_fingerprint never
		// accepts the placeholder).
		if fp := n.CertFingerprint; fp != "" && fp != SpecPlaceholderFingerprint && !ValidFingerprint(fp) {
			v.bad(p+".cert_fingerprint", fp, "sha256: followed by 64 lowercase hex characters")
		}
		for j, tag := range n.Tags {
			if strings.TrimSpace(tag) == "" || hasControl(tag) {
				v.bad(fmt.Sprintf("%s.tags[%d]", p, j), tag, "a non-empty tag")
			}
		}
	}
}

func (v *validator) ladders() {
	names := make([]string, 0, len(v.c.Ladders))
	for n := range v.c.Ladders {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !ValidID(n) {
			v.add(deyerr.New(deyerr.C007, deyerr.Params{"kind": "ladder", "id": n}))
		}
		rungs := v.c.Ladders[n]
		if len(rungs) == 0 {
			v.bad("ladders."+n, "", "at least one transport")
			continue
		}
		v.rungs("ladders."+n, rungs)
	}
}

// rungs checks transport ids (shape, registry) and duplicates.
func (v *validator) rungs(field string, rungs []string) {
	seen := map[string]bool{}
	for _, r := range rungs {
		known := transportIDRe.MatchString(r)
		if known && v.opt.KnownTransport != nil {
			known = v.opt.KnownTransport(r)
		}
		if !known {
			v.add(deyerr.New(deyerr.C005, deyerr.Params{"transport": r, "valid": v.validTransports()}))
			continue
		}
		if seen[r] {
			v.bad(field, r, "each transport at most once")
		}
		seen[r] = true
	}
}

func (v *validator) validTransports() string {
	var ids []string
	if v.opt.ValidTransports != nil {
		ids = v.opt.ValidTransports()
	}
	if len(ids) == 0 {
		set := map[string]bool{}
		for _, r := range append(cloneStrings(DefaultLadder), DefaultUDPLadder...) {
			set[r] = true
		}
		for r := range set {
			ids = append(ids, r)
		}
		sort.Strings(ids)
	}
	return strings.Join(ids, ", ")
}

func itemPath(list, id string, i int) string {
	if ValidID(id) {
		return fmt.Sprintf("%s[%s]", list, id)
	}
	return fmt.Sprintf("%s[%d]", list, i)
}

func (v *validator) tunnel(i int, t *Tunnel) {
	p := itemPath("tunnels", t.ID, i)
	if !ValidID(t.ID) {
		v.add(deyerr.New(deyerr.C007, deyerr.Params{"kind": "tunnel", "id": t.ID}))
	} else {
		for j := 0; j < i; j++ {
			if v.c.Tunnels[j].ID == t.ID {
				v.add(deyerr.New(deyerr.C002, deyerr.Params{"kind": "tunnel", "id": t.ID}))
				break
			}
		}
	}
	v.name(p+".name", t.Name, false)
	v.tunnelNodes(t)
	v.ports(p, t)
	v.ladderRef(p, t)
	v.failover(p+".failover", t.Failover)
	v.tls(p+".tls", t.TLS)
	if t.ProbePort != 0 {
		if pm, ok := t.ProbeTarget(); !ok || pm.Listen != t.ProbePort {
			v.bad(p+".probe_port", t.ProbePort, "a TCP listen port of this tunnel")
		}
	}
	if a := t.Advanced; a != nil {
		v.advanced(p+".advanced", a)
	}
}

func (v *validator) tunnelNodes(t *Tunnel) {
	if len(t.Nodes) == 0 {
		v.add(deyerr.New(deyerr.C008, deyerr.Params{"tunnel": t.ID}))
		return
	}
	seen := map[string]bool{}
	for _, n := range t.Nodes {
		if seen[n] {
			v.add(deyerr.New(deyerr.C002, deyerr.Params{"kind": "tunnel " + t.ID + " node", "id": n}).
				WithFix(fmt.Sprintf("list node %s only once in the nodes of tunnel %s (first = primary, rest = backups)", n, t.ID)))
			continue
		}
		seen[n] = true
		if _, ok := v.c.NodeByID(n); !ok {
			v.add(deyerr.New(deyerr.C010, deyerr.Params{"node": n, "tunnel": t.ID}))
		}
	}
}

func (v *validator) ports(p string, t *Tunnel) {
	switch n := len(t.Ports); {
	case n == 0:
		v.bad(p+".ports", "", "at least one port map")
	case n > MaxPortMaps:
		v.add(deyerr.New(deyerr.C015, deyerr.Params{"tunnel": t.ID, "count": n}))
	}
	for j, pm := range t.Ports {
		pp := fmt.Sprintf("%s.ports[%d]", p, j)
		protoOK := contains(Protos, pm.Proto)
		if !protoOK {
			v.bad(pp+".proto", pm.Proto, strings.Join(Protos, ", "))
		}
		if pm.Listen < 1 || pm.Listen > 65535 {
			v.bad(pp+".listen", pm.Listen, "1-65535")
		} else {
			key := ListenKey{Port: pm.Listen, Proto: pm.Proto}
			if reserved, why := ReservedListen(pm.Listen, v.controlPort(), v.opt.ReservedPorts); reserved {
				v.add(deyerr.New(deyerr.C011, deyerr.Params{"port": key.String(), "reason": why}))
			} else if protoOK {
				if other, dup := v.listen[key]; dup {
					e := deyerr.New(deyerr.C003, deyerr.Params{"port": key.String(), "tunnel": other, "other": t.ID})
					if other == t.ID {
						e = e.WithWhy(fmt.Sprintf("tunnel %s lists %s twice; only one process can bind a port", t.ID, key))
					}
					v.add(e)
				} else {
					v.listen[key] = t.ID
				}
			}
		}
		if !ValidHostPort(pm.Target) {
			v.add(deyerr.New(deyerr.C004, deyerr.Params{"target": pm.Target, "tunnel": t.ID}))
		}
		if pm.Probe != "" {
			if err := CheckProbe(pp+".probe", pm.Proto, pm.Probe); err != nil {
				v.add(deyerr.As(err))
			}
		}
	}
}

// CheckProbe applies the rule of config.yaml ports[].probe (section 9) to
// the probe kind of a port map of proto: auto, tcp, tls or http, and auto
// only for UDP. The UI and the Local API use it so that a kind set there
// is accepted or refused exactly as in config.yaml; field names the value
// in the DEY-C013 error.
func CheckProbe(field, proto, probe string) error {
	switch {
	case proto == ProtoUDP && probe != ProbeAuto:
		return deyerr.New(deyerr.C013, deyerr.Params{"field": field, "value": probe, "allowed": "auto (UDP port maps are not probed by type)"})
	case !contains(ProbeKinds, probe):
		return deyerr.New(deyerr.C013, deyerr.Params{"field": field, "value": probe, "allowed": strings.Join(ProbeKinds, ", ")})
	}
	return nil
}

func (v *validator) ladderRef(p string, t *Tunnel) {
	if t.Ladder.Inline != nil {
		if len(t.Ladder.Inline) == 0 {
			// "ladder: []": at least one transport is required (section 4).
			v.add(deyerr.New(deyerr.C009, deyerr.Params{"tunnel": t.ID}))
			return
		}
		v.rungs(p+".ladder", t.Ladder.Inline)
		return
	}
	if t.Ladder.Name == "" {
		v.add(deyerr.New(deyerr.C009, deyerr.Params{"tunnel": t.ID}))
		return
	}
	rungs, ok := v.c.LadderProfile(effectiveProfile(t))
	if !ok {
		v.add(deyerr.New(deyerr.C012, deyerr.Params{"ladder": t.Ladder.Name, "tunnel": t.ID}))
		return
	}
	if len(rungs) == 0 {
		v.add(deyerr.New(deyerr.C009, deyerr.Params{"tunnel": t.ID}))
	}
}

func (v *validator) failover(p string, f Failover) {
	if !contains(Policies, f.Policy) {
		v.bad(p+".policy", f.Policy, strings.Join(Policies, ", "))
	}
	v.intRange(p+".probe_interval_s", f.ProbeIntervalS, 1, MaxProbeIntervalS)
	switch {
	case f.ProbeTimeoutS < 1 || f.ProbeTimeoutS > MaxProbeTimeoutS:
		v.intRange(p+".probe_timeout_s", f.ProbeTimeoutS, 1, MaxProbeTimeoutS)
	case f.ProbeIntervalS >= 1 && f.ProbeTimeoutS > f.ProbeIntervalS:
		v.bad(p+".probe_timeout_s", f.ProbeTimeoutS, fmt.Sprintf("1-%d (not longer than probe_interval_s)", f.ProbeIntervalS))
	}
	v.intRange(p+".fail_threshold", f.FailThreshold, 1, MaxThreshold)
	v.intRange(p+".recover_threshold", f.RecoverThreshold, 1, MaxThreshold)
	v.intRange(p+".failback_after_s", f.FailbackAfterS, 1, MaxFailbackAfterS)
	v.intRange(p+".max_switches_per_hour", f.MaxSwitchesPerHour, 1, MaxSwitchesPerHour)
	v.intRange(p+".quarantine_s", f.QuarantineS, 1, MaxQuarantineS)
}

func (v *validator) tls(p string, t TLS) {
	switch t.Mode {
	case TLSModeAuto:
	case TLSModeACME:
		if v.c.Hub == nil || v.c.Hub.Domain == "" {
			v.bad(p+".mode", t.Mode, "auto or custom (acme needs hub.domain)")
		}
	case TLSModeCustom:
		v.absPath(p+".cert_file", t.CertFile, true)
		v.secretPath(p+".key_file", t.KeyFile, true)
	default:
		v.bad(p+".mode", t.Mode, strings.Join(TLSModes, ", "))
	}
}

func (v *validator) advanced(p string, a *Advanced) {
	v.intRange(p+".connection_pool", a.ConnectionPool, 0, MaxConnectionPool)
	v.intRange(p+".hysteria_up_mbps", a.HysteriaUpMbps, 0, MaxHysteriaMbps)
	v.intRange(p+".hysteria_down_mbps", a.HysteriaDownMbps, 0, MaxHysteriaMbps)
	if w := a.BackhaulWebPort; w != 0 {
		allowed := "1-65535, not 22, the control port, 30000-31999, a TCP listen port or another tunnel's backhaul_web_port"
		_, usedTCP := v.c.UsedListenPorts()[ListenKey{Port: w, Proto: ProtoTCP}]
		_, usedWeb := v.webPorts[w]
		if reserved, _ := ReservedListen(w, v.controlPort(), nil); w < 1 || w > 65535 || reserved || usedTCP || usedWeb {
			v.bad(p+".backhaul_web_port", w, allowed)
		}
		if !usedWeb {
			v.webPorts[w] = p
		}
	}
}

func (v *validator) nodeSelf(n *NodeSelf) {
	if !ValidNodeID(n.ID) {
		v.add(deyerr.New(deyerr.C007, deyerr.Params{"kind": "node", "id": n.ID}))
	}
	if !ValidHostPort(n.HubAddr) {
		v.bad("node.hub_addr", n.HubAddr, "host:port of the hub, e.g. 5.6.7.8:44433")
	}
	if !ValidFingerprint(n.HubCAFingerprint) {
		v.bad("node.hub_ca_fingerprint", n.HubCAFingerprint, "sha256: followed by 64 lowercase hex characters")
	}
	v.absPath("node.cert_file", n.CertFile, true)
	v.secretPath("node.key_file", n.KeyFile, true)
}
