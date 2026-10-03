package api

import (
	"net"
	"regexp"
	"strconv"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// JoinLinkScheme is the scheme of a join link (section 3).
const JoinLinkScheme = "dey://"

// JoinLink is a parsed join link
// dey://TOKEN@HOST:PORT[/SECRET][?tls=0|1]#sha256:<hex>. A link with a
// path is a front link (the hub is reached through a CDN front, see
// Front); a link without one is a direct link and behaves as it always did.
type JoinLink struct {
	Token       string // one-time token (base64url, secret)
	Host        string // hub IP (or DNS name); IPv6 without brackets
	Port        int    // hub control_port, or the front port in a front link
	Fingerprint string // canonical "sha256:<64 hex>" of the hub CA
	Secret      string // front path secret (no slashes, secret); "" in a direct link
	TLS         *bool  // front scheme override (?tls=0|1); nil = derive from the port
}

// Front reports whether this is a front link (it carries the path secret).
func (l JoinLink) Front() bool { return l.Secret != "" }

// Addr is the hub control address "host:port" (IPv6 bracketed). In a front
// link it is the front domain and port.
func (l JoinLink) Addr() string { return net.JoinHostPort(l.Host, strconv.Itoa(l.Port)) }

// String formats the link (it contains the secret token).
func (l JoinLink) String() string { return FormatJoinLink(l) }

var (
	tokenRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}={0,2}$`)
	secretRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// FormatJoinLink renders dey://TOKEN@HOST:PORT[/SECRET][?tls=0|1]#sha256:<hex>;
// an IPv6 host is bracketed. The path is written when Secret is set, the
// query when TLS is set; a direct link keeps its original form byte for
// byte.
func FormatJoinLink(l JoinLink) string {
	fp := l.Fingerprint
	if n, ok := tlsutil.NormalizeFingerprint(fp); ok {
		fp = n
	}
	out := JoinLinkScheme + l.Token + "@" + l.Addr()
	if l.Secret != "" {
		out += "/" + l.Secret
	}
	if l.TLS != nil {
		if *l.TLS {
			out += "?tls=1"
		} else {
			out += "?tls=0"
		}
	}
	return out + "#" + fp
}

// ParseJoinLink parses a join link. The fingerprint may be written as
// sha256:<hex> or as bare hex. Surrounding whitespace and quotes (as
// pasted from the join command) are ignored. Any problem is DEY-N006; the
// error never contains the token or the front secret.
//
// The parts are taken off in a fixed order: the "#" fragment, the "?tls="
// query, the path (everything after the first "/"), then the token (up to
// the last "@") and finally HOST:PORT.
func ParseJoinLink(link string) (JoinLink, error) {
	s := strings.TrimSpace(link)
	s = strings.Trim(s, `'"`)
	s = strings.TrimSpace(s)
	bad := func(reason string) (JoinLink, error) {
		return JoinLink{}, deyerr.New(deyerr.N006, deyerr.Params{"link": redactJoinLink(s)}).WithDetail(reason)
	}
	if len(s) < len(JoinLinkScheme) || !strings.EqualFold(s[:len(JoinLinkScheme)], JoinLinkScheme) {
		return bad("the link must start with dey://")
	}
	rest := s[len(JoinLinkScheme):]
	rest, frag, ok := strings.Cut(rest, "#")
	if !ok || frag == "" {
		return bad("the CA fingerprint (#sha256:...) is missing")
	}
	// "sha256:<hex>" (canonical) or the bare 64 hex digits (spec section 3).
	fp, ok := tlsutil.NormalizeFingerprint(frag)
	if !ok || strings.Contains(frag, ":") && !strings.HasPrefix(strings.ToLower(frag), tlsutil.FingerprintPrefix) {
		return bad("the CA fingerprint must be sha256:<64 hex digits>")
	}
	var tls *bool
	rest, query, hasQuery := strings.Cut(rest, "?")
	if hasQuery {
		switch query {
		case "tls=1":
			v := true
			tls = &v
		case "tls=0":
			v := false
			tls = &v
		default:
			return bad("the only accepted option is ?tls=0 or ?tls=1")
		}
	}
	secret := ""
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		secret = rest[slash+1:]
		rest = rest[:slash]
		// A lone trailing "/" is an empty path: a direct link.
		if strings.Contains(secret, "/") {
			return bad("the path must be a single segment (/SECRET)")
		}
		if secret != "" && !secretRe.MatchString(secret) {
			return bad("the path secret must be 16-128 characters of A-Z a-z 0-9 _ -")
		}
	}
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return bad("the token (TOKEN@) is missing")
	}
	token, hostport := rest[:at], rest[at+1:]
	if !tokenRe.MatchString(token) {
		return bad("the token is not valid base64url")
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return bad("the hub address must be HOST:PORT (IPv6 as [addr]:port)")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return bad("the hub port must be 1-65535")
	}
	if ip := net.ParseIP(host); ip != nil {
		if strings.Contains(host, ":") != strings.HasPrefix(hostport, "[") {
			return bad("an IPv6 hub address must be written as [addr]:port")
		}
		host = ip.String()
	} else if strings.Contains(host, "%") || !hostnameRe.MatchString(host) || len(host) > 253 {
		return bad("the hub host is not a valid IP address or name")
	}
	return JoinLink{Token: token, Host: host, Port: port, Fingerprint: fp, Secret: secret, TLS: tls}, nil
}

// redactJoinLink hides the token and the front path secret of a (possibly
// malformed) link. Everything from the first "/" or "?" after the scheme
// up to the fragment is replaced, so a secret is never shown even when it
// contains an "@"; the fingerprint (public) is kept.
func redactJoinLink(s string) string {
	if len(s) > 256 {
		s = s[:256] + "..."
	}
	if len(s) < len(JoinLinkScheme) || !strings.EqualFold(s[:len(JoinLinkScheme)], JoinLinkScheme) {
		return "***"
	}
	rest := s[len(JoinLinkScheme):]
	rest, frag, hasFrag := strings.Cut(rest, "#")
	tail := ""
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		tail = rest[i:i+1] + "***"
		rest = rest[:i]
	}
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return JoinLinkScheme + "***"
	}
	out := JoinLinkScheme + "***" + rest[at:] + tail
	if hasFrag {
		// A path written after the fragment is as secret as one before it.
		if i := strings.IndexAny(frag, "/?"); i >= 0 {
			frag = frag[:i+1] + "***"
		}
		out += "#" + frag
	}
	return out
}
