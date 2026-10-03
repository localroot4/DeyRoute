package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Front mode: the nodes reach the hub through a CDN (Cloudflare) because the
// direct path is cut. These are the config types and helpers of that mode;
// the listener, the join link and the dial live in other packages.

// RouteFront is the value of nodes[].route for a node that joined through
// the hub's front listener ("" = direct).
const RouteFront = "front"

// Front TLS modes (hub.front.tls). Auto and custom reuse TLSModeAuto and
// TLSModeCustom; off serves plain HTTP (Cloudflare "Flexible").
const (
	FrontTLSAuto   = TLSModeAuto
	FrontTLSCustom = TLSModeCustom
	FrontTLSOff    = "off"
)

// Front schemes (node.front.scheme).
const (
	FrontSchemeWS  = "ws"
	FrontSchemeWSS = "wss"
)

// DefaultFrontSecretFile is the name, under SecretsDir, of the file that
// holds the front path secret when hub.front.secret_file is empty.
const DefaultFrontSecretFile = "front.secret" // #nosec G101 -- file name, not a credential

// MaxFrontSecretFileLen bounds a secret file name.
const MaxFrontSecretFileLen = 255

// Enumerations accepted for the front keys (shared with the CLI for hints).
var (
	FrontTLSModes = []string{FrontTLSAuto, FrontTLSCustom, FrontTLSOff}
	FrontSchemes  = []string{FrontSchemeWS, FrontSchemeWSS}
)

// cloudflareHTTPSPorts and cloudflareHTTPPorts are the ports Cloudflare
// proxies, in the order they are suggested. The scheme follows the set.
var (
	cloudflareHTTPSPorts = [...]int{443, 2053, 2083, 2087, 2096, 8443}
	cloudflareHTTPPorts  = [...]int{80, 8080, 8880, 2052, 2082, 2086, 2095}
)

// CloudflareHTTPSPorts returns the HTTPS ports Cloudflare proxies (wss), in
// suggestion order. The slice is a copy.
func CloudflareHTTPSPorts() []int { return append([]int(nil), cloudflareHTTPSPorts[:]...) }

// CloudflareHTTPPorts returns the HTTP ports Cloudflare proxies (ws). The
// slice is a copy.
func CloudflareHTTPPorts() []int { return append([]int(nil), cloudflareHTTPPorts[:]...) }

// IsCloudflareHTTPSPort reports whether port is a Cloudflare HTTPS port.
func IsCloudflareHTTPSPort(port int) bool { return inPorts(cloudflareHTTPSPorts[:], port) }

// IsCloudflarePort reports whether Cloudflare proxies port (HTTPS or HTTP set).
func IsCloudflarePort(port int) bool {
	return IsCloudflareHTTPSPort(port) || inPorts(cloudflareHTTPPorts[:], port)
}

// FrontSchemeForPort returns the WebSocket scheme a front on port uses:
// "wss" for the HTTPS set, "ws" for the HTTP set and "" for any other port.
func FrontSchemeForPort(port int) string {
	switch {
	case IsCloudflareHTTPSPort(port):
		return FrontSchemeWSS
	case inPorts(cloudflareHTTPPorts[:], port):
		return FrontSchemeWS
	}
	return ""
}

func inPorts(list []int, port int) bool {
	for _, p := range list {
		if p == port {
			return true
		}
	}
	return false
}

// HubFront is the hub's CDN front listener (hub.front).
type HubFront struct {
	Enabled        bool     `yaml:"enabled,omitempty"`
	Domain         string   `yaml:"domain,omitempty"`          // proxied record the nodes use
	Port           int      `yaml:"port,omitempty"`            // a Cloudflare proxied port, never the control port, never a tunnel port (C011)
	TLS            string   `yaml:"tls,omitempty"`             // "auto" (default when empty), "custom", "off"
	CertFile       string   `yaml:"cert_file,omitempty"`       // tls: custom
	KeyFile        string   `yaml:"key_file,omitempty"`        // tls: custom
	SecretFile     string   `yaml:"secret_file,omitempty"`     // file NAME under SecretsDir (the key stays *_file for the log redactor)
	CFOnly         *bool    `yaml:"cf_only,omitempty"`         // nil = true: the firewall opens the front port to Cloudflare ranges only
	TrustedProxies []string `yaml:"trusted_proxies,omitempty"` // extra CIDRs whose CF-Connecting-IP header is trusted
}

// CFOnlyOrDefault returns cf_only, true when it is not set.
func (f HubFront) CFOnlyOrDefault() bool { return f.CFOnly == nil || *f.CFOnly }

// TLSMode returns the front TLS mode, "auto" when it is not set.
func (f HubFront) TLSMode() string {
	if f.TLS == "" {
		return FrontTLSAuto
	}
	return f.TLS
}

// SecretFileOrDefault returns the secret file name, DefaultFrontSecretFile
// when it is not set.
func (f HubFront) SecretFileOrDefault() string {
	if f.SecretFile == "" {
		return DefaultFrontSecretFile
	}
	return f.SecretFile
}

// FrontPort returns the front listen port when front mode is enabled, else 0.
// It is the value callers pass as the extra reserved port to internal/ports.
// Safe on a nil Hub.
func (h *Hub) FrontPort() int {
	if h == nil || !h.Front.Enabled {
		return 0
	}
	return h.Front.Port
}

// ReservedPorts returns the ports, besides 22, the control port and the
// backend control range, that no tunnel may listen on: the front port when
// front mode is enabled. Safe on a nil Hub.
func (h *Hub) ReservedPorts() []int {
	if p := h.FrontPort(); p > 0 {
		return []int{p}
	}
	return nil
}

// NodeFront is node.front: the node's front dial settings. The node is in
// front mode iff SecretFile is set.
type NodeFront struct {
	SecretFile string `yaml:"secret_file,omitempty"`
	Scheme     string `yaml:"scheme,omitempty"`  // "" = derive from the port; "ws" | "wss"
	EdgeIP     string `yaml:"edge_ip,omitempty"` // dial this IP instead of resolving the domain
}

// FrontMode reports whether the node reaches the hub through the front.
func (n NodeSelf) FrontMode() bool { return n.Front.SecretFile != "" }

// validFrontSecretName reports whether s is a plain file name (it is joined
// to SecretsDir): no separator, not "." or "..", no control character.
func validFrontSecretName(s string) bool {
	return s != "" && len(s) <= MaxFrontSecretFileLen && s != "." && s != ".." &&
		!strings.ContainsAny(s, `/\`) && !hasControl(s)
}

// secretName records DEY-C013 for a secret file name. The rejected value is
// hidden: a common mistake is to paste the secret itself.
func (v *validator) secretName(field, s string) {
	if s == "" || validFrontSecretName(s) {
		return
	}
	v.bad(field, hiddenValue, "the NAME of a file under "+SecretsDir+", without a directory, e.g. "+DefaultFrontSecretFile)
}

// front validates hub.front. Shape rules apply whenever a key is set, the
// required keys and the cross-checks only while the front is enabled, so a
// disabled section can keep its settings for the next enable.
func (v *validator) front(h *Hub) {
	f := h.Front
	const domainHint = "a DNS name such as front.example.com (the proxied record the nodes dial)"
	if f.Enabled && f.Domain == "" {
		v.bad("hub.front.domain", f.Domain, domainHint)
	}
	if f.Domain != "" && !ValidDomain(f.Domain) {
		v.bad("hub.front.domain", f.Domain, domainHint)
	}
	switch {
	case f.Port == 0 && f.Enabled, f.Port != 0 && !IsCloudflarePort(f.Port):
		v.bad("hub.front.port", f.Port, frontPortAllowed())
	case f.Enabled && f.Port == h.ControlPort:
		v.bad("hub.front.port", f.Port, "a Cloudflare port other than hub.control_port")
	}
	if f.TLS != "" && !contains(FrontTLSModes, f.TLS) {
		v.bad("hub.front.tls", f.TLS, strings.Join(FrontTLSModes, ", "))
	}
	// C013: plain HTTP is the only thing Cloudflare speaks to an origin on
	// an HTTP-set port.
	if f.Enabled && f.TLS != FrontTLSOff && FrontSchemeForPort(f.Port) == FrontSchemeWS {
		v.bad("hub.front.tls", f.TLSMode(), fmt.Sprintf("off (port %d is a Cloudflare HTTP port: Cloudflare does not use TLS there)", f.Port))
	}
	if f.TLS == FrontTLSCustom {
		v.absPath("hub.front.cert_file", f.CertFile, true)
		v.secretPath("hub.front.key_file", f.KeyFile, true)
	} else {
		v.absPath("hub.front.cert_file", f.CertFile, false)
		v.secretPath("hub.front.key_file", f.KeyFile, false)
	}
	v.secretName("hub.front.secret_file", f.SecretFile)
	for i, c := range f.TrustedProxies {
		if p, err := netip.ParsePrefix(c); err != nil || p.Addr().Zone() != "" {
			v.bad(fmt.Sprintf("hub.front.trusted_proxies[%d]", i), c, "a CIDR such as 203.0.113.0/24")
		}
	}
}

func frontPortAllowed() string {
	return fmt.Sprintf("a Cloudflare proxied port: %s (https) or %s (http)", joinInts(cloudflareHTTPSPorts[:]), joinInts(cloudflareHTTPPorts[:]))
}

func joinInts(l []int) string {
	s := make([]string, len(l))
	for i, n := range l {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// nodeFront validates node.front of a node config.
func (v *validator) nodeFront(n *NodeSelf) {
	f := n.Front
	v.secretName("node.front.secret_file", f.SecretFile)
	if f.Scheme != "" && !contains(FrontSchemes, f.Scheme) {
		v.bad("node.front.scheme", f.Scheme, strings.Join(FrontSchemes, ", "))
	}
	if f.EdgeIP != "" && !validPublicIP(f.EdgeIP) {
		v.bad("node.front.edge_ip", f.EdgeIP, "an IPv4 or IPv6 address of a Cloudflare edge, or empty")
	}
	if !n.FrontMode() {
		const off = "empty (the node is not in front mode: node.front.secret_file is not set)"
		if f.Scheme != "" {
			v.bad("node.front.scheme", f.Scheme, off)
		}
		if f.EdgeIP != "" {
			v.bad("node.front.edge_ip", f.EdgeIP, off)
		}
		return
	}
	// In front mode hub_addr is "<front domain>:<port>"; the scheme comes
	// from the port unless node.front.scheme says otherwise.
	if _, port, err := net.SplitHostPort(n.HubAddr); err == nil {
		if p, aerr := strconv.Atoi(port); aerr == nil && f.Scheme == "" && FrontSchemeForPort(p) == "" {
			v.bad("node.front.scheme", f.Scheme, fmt.Sprintf("ws or wss (port %d is not a Cloudflare port, so the scheme cannot be derived)", p))
		}
	}
}
