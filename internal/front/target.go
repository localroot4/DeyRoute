package front

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
)

// MaxSecretLen bounds a path secret.
const MaxSecretLen = 128

// Target is where and how a node reaches the hub's front.
type Target struct {
	Host   string // the front domain (or an IP literal in tests): Host header, TLS server name
	Port   int    // the CDN port
	TLS    bool   // wss (outer TLS) or ws (plain)
	EdgeIP string // dial this address instead of resolving Host ("" = resolve)
	Secret string // the path secret; never printed
}

// NewTarget validates and builds a Target. hostPort is "host:port" (host a
// DNS name or an IP literal, port 1..65535). scheme is "ws", "wss" or ""
// (derive it from the port with config.FrontSchemeForPort: the Cloudflare
// HTTPS ports are wss, the HTTP ports ws, any other port needs an explicit
// scheme). edgeIP, when not empty, is an IP literal the connection goes to
// instead of resolving the host. secret is the path secret.
//
// Errors never contain the secret.
func NewTarget(hostPort, scheme, edgeIP, secret string) (Target, error) {
	host, ps, err := net.SplitHostPort(hostPort)
	if err != nil || host == "" {
		return Target{}, fmt.Errorf("front: %q is not host:port", hostPort)
	}
	port, err := strconv.Atoi(ps)
	if err != nil || port < 1 || port > 65535 {
		return Target{}, fmt.Errorf("front: port %q is not in 1-65535", ps)
	}
	if !validHost(host) {
		return Target{}, fmt.Errorf("front: %q is not a host name or an IP address", host)
	}
	t := Target{Host: host, Port: port, Secret: secret}
	switch scheme {
	case "":
		switch config.FrontSchemeForPort(port) {
		case config.FrontSchemeWSS:
			t.TLS = true
		case config.FrontSchemeWS:
		default:
			return Target{}, fmt.Errorf("front: port %d is not a Cloudflare port, so the scheme cannot be derived: use ws or wss", port)
		}
	case config.FrontSchemeWSS:
		t.TLS = true
	case config.FrontSchemeWS:
	default:
		return Target{}, fmt.Errorf("front: scheme %q is not ws or wss", scheme)
	}
	if edgeIP != "" {
		a, err := netip.ParseAddr(edgeIP)
		if err != nil || a.Zone() != "" || a.IsUnspecified() || a.IsMulticast() {
			return Target{}, fmt.Errorf("front: edge address %q is not an IP address", edgeIP)
		}
		t.EdgeIP = a.Unmap().String()
	}
	if !ValidSecret(secret) {
		return Target{}, errors.New("front: the path secret is empty, too long or has characters outside A-Z a-z 0-9 - _")
	}
	return t, nil
}

// ValidSecret reports whether s can be a path secret: 1 to MaxSecretLen
// characters of [A-Za-z0-9_-] (the alphabet of secrets.NewToken output, safe
// in a URL path without escaping).
func ValidSecret(s string) bool {
	if s == "" || len(s) > MaxSecretLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !isAlnum(c) && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// validHost accepts a DNS name (labels of letters, digits and hyphen, not
// all numeric at the end) or an IP literal without zone.
func validHost(h string) bool {
	if a, err := netip.ParseAddr(h); err == nil {
		return a.Zone() == ""
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" || len(h) > 253 {
		return false
	}
	labels := strings.Split(h, ".")
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for i := 0; i < len(l); i++ {
			if c := l[i]; !isAlnum(c) && c != '-' {
				return false
			}
		}
	}
	last := labels[len(labels)-1]
	allDigits := true
	for i := 0; i < len(last); i++ {
		if last[i] < '0' || last[i] > '9' {
			allDigits = false
		}
	}
	return !allDigits // a numeric last label is a mistyped IP address
}

// Addr returns the front address as host:port, for messages (never the edge
// address or the secret).
func (t Target) Addr() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }

// dialAddr is where the TCP connection goes.
func (t Target) dialAddr() string {
	if t.EdgeIP != "" {
		return net.JoinHostPort(t.EdgeIP, strconv.Itoa(t.Port))
	}
	return t.Addr()
}

// hostHeader is the Host header value: the host, with the port unless it is
// the default of the scheme the browser would use (80 or 443).
func (t Target) hostHeader() string {
	h := t.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]" // IPv6 literal
	}
	if t.Port == 80 || t.Port == 443 {
		return h
	}
	return h + ":" + strconv.Itoa(t.Port)
}

// origin is the Origin header value of a browser page on the front domain.
func (t Target) origin() string {
	h := t.Host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return "https://" + h
}
