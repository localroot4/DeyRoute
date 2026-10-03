package setup

import (
	"net"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front"
)

// MinFrontSecretLen is the shortest path secret a hub target or join link
// accepts (the join link grammar: 16 to 128 characters).
const MinFrontSecretLen = 16

// HubTarget is where a node is pointed with `deyroute node set-hub`: a direct
// hub address (host:port) or a front target ws[s]://DOMAIN:PORT/SECRET, the
// same grammar as the join link.
type HubTarget struct {
	// Addr is host:port: the hub control address, or the front domain and
	// port (what node.hub_addr holds).
	Addr string
	// Front is true for a ws:// or wss:// target.
	Front bool
	// Secret is the front path secret ("" for a direct target). Never print it.
	Secret string
	// Scheme is the scheme the owner wrote ("ws" or "wss"), "" for a direct
	// target.
	Scheme string
}

// StoredScheme is the value for node.front.scheme: empty when the scheme the
// owner wrote is the one the port implies (the Cloudflare HTTPS ports are
// wss, the HTTP ports ws), else the explicit scheme.
func (t HubTarget) StoredScheme() string { return storedFrontScheme(t.Addr, t.Scheme) }

// storedFrontScheme returns scheme unless it equals the scheme derived from
// the port of addr.
func storedFrontScheme(addr, scheme string) string {
	_, ps, err := net.SplitHostPort(addr)
	if err != nil || scheme == "" {
		return ""
	}
	if port, err := strconv.Atoi(ps); err == nil && config.FrontSchemeForPort(port) == scheme {
		return ""
	}
	return scheme
}

// ParseHubTarget parses a set-hub argument. A plain host:port is a direct
// target (config.ValidHostPort); ws://HOST:PORT/SECRET and
// wss://HOST:PORT/SECRET are front targets whose scheme is the one written
// (a port that is no Cloudflare port needs it explicitly, which the prefix
// is). Errors are DEY-C013 and never contain the secret.
func ParseHubTarget(s string) (HubTarget, error) {
	s = strings.TrimSpace(s)
	bad := func() error {
		return deyerr.New(deyerr.C013, deyerr.Params{
			"field": "node.hub_addr", "value": RedactHubTarget(s),
			"allowed": "host:port of the hub, e.g. 5.6.7.8:44433, or for a hub behind the front wss://DOMAIN:PORT/SECRET (ws:// for a plain-HTTP port)",
		})
	}
	var scheme, rest string
	switch lower := strings.ToLower(s); {
	case strings.HasPrefix(lower, "wss://"):
		scheme, rest = config.FrontSchemeWSS, s[len("wss://"):]
	case strings.HasPrefix(lower, "ws://"):
		scheme, rest = config.FrontSchemeWS, s[len("ws://"):]
	default:
		if !config.ValidHostPort(s) {
			return HubTarget{}, bad()
		}
		return HubTarget{Addr: s}, nil
	}
	hostPort, secret, ok := strings.Cut(rest, "/")
	if !ok || len(secret) < MinFrontSecretLen || strings.ContainsAny(secret, "/?#") {
		return HubTarget{}, bad()
	}
	t, err := front.NewTarget(hostPort, scheme, "", secret)
	if err != nil {
		return HubTarget{}, bad()
	}
	return HubTarget{Addr: t.Addr(), Front: true, Secret: secret, Scheme: scheme}, nil
}

// RedactHubTarget hides the path of anything that looks like a target with
// one (a ws:// or wss:// target, but also a mistyped scheme or a missing
// one): everything from the first "/", "?" or "#" after the host is
// replaced, since that is where the front secret is. A plain host:port is
// returned unchanged.
func RedactHubTarget(s string) string {
	scheme, rest := "", s
	if i := strings.Index(s, "://"); i >= 0 {
		scheme, rest = s[:i+3], s[i+3:]
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		return scheme + rest[:i] + "/***"
	}
	if scheme != "" {
		return scheme + rest + "/***"
	}
	return s
}
