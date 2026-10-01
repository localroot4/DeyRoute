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

// JoinLink is a parsed join link dey://TOKEN@HUB_IP:PORT#sha256:<hex>.
type JoinLink struct {
	Token       string // one-time token (base64url, secret)
	Host        string // hub IP (or DNS name); IPv6 without brackets
	Port        int    // hub control_port
	Fingerprint string // canonical "sha256:<64 hex>" of the hub CA
}

// Addr is the hub control address "host:port" (IPv6 bracketed).
func (l JoinLink) Addr() string { return net.JoinHostPort(l.Host, strconv.Itoa(l.Port)) }

// String formats the link (it contains the secret token).
func (l JoinLink) String() string { return FormatJoinLink(l) }

var (
	tokenRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}={0,2}$`)
	hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
)

// FormatJoinLink renders dey://TOKEN@HOST:PORT#sha256:<hex>; an IPv6 host
// is bracketed.
func FormatJoinLink(l JoinLink) string {
	fp := l.Fingerprint
	if n, ok := tlsutil.NormalizeFingerprint(fp); ok {
		fp = n
	}
	return JoinLinkScheme + l.Token + "@" + l.Addr() + "#" + fp
}

// ParseJoinLink parses a join link. The fingerprint may be written as
// sha256:<hex> or as bare hex. Surrounding whitespace and quotes (as
// pasted from the join command) are ignored. Any problem is DEY-N006; the
// error never contains the token.
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
	return JoinLink{Token: token, Host: host, Port: port, Fingerprint: fp}, nil
}

// redactJoinLink hides the token of a (possibly malformed) link.
func redactJoinLink(s string) string {
	if len(s) > 256 {
		s = s[:256] + "..."
	}
	if len(s) < len(JoinLinkScheme) || !strings.EqualFold(s[:len(JoinLinkScheme)], JoinLinkScheme) {
		return "***"
	}
	rest := s[len(JoinLinkScheme):]
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		return JoinLinkScheme + "***" + rest[at:]
	}
	return JoinLinkScheme + "***"
}
