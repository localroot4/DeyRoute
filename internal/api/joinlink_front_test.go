package api

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

const testSecret = "Zx9Yw8Vu7Ts6Rq5Po4Nm3Lk2Ji1Hg0Fe_dCbA-98765"

func boolp(b bool) *bool { return &b }

// errText is every string an N006 error can show.
func errText(e *deyerr.Error) string {
	text := e.Error() + "\n" + e.Detail
	for _, v := range e.Params {
		text += "\n" + fmt.Sprint(v)
	}
	return text
}

// requireNoLeak fails when the error text, detail or params contain the
// token or the front secret.
func requireNoLeak(t *testing.T, err error, secrets ...string) {
	t.Helper()
	e := requireCode(t, err, deyerr.N006)
	text := errText(e)
	for _, sec := range secrets {
		if sec != "" {
			require.NotContains(t, text, sec)
		}
	}
	require.NotEmpty(t, e.Detail)
}

func TestJoinLinkFront(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want JoinLink
		out  string // canonical form
	}{
		{"hostname", "dey://" + testToken + "@cdn.example.com:443/" + testSecret + "#" + testFP,
			JoinLink{Token: testToken, Host: "cdn.example.com", Port: 443, Fingerprint: testFP, Secret: testSecret},
			"dey://" + testToken + "@cdn.example.com:443/" + testSecret + "#" + testFP},
		{"ipv4", "dey://" + testToken + "@203.0.113.7:2053/" + testSecret + "#" + testFP,
			JoinLink{Token: testToken, Host: "203.0.113.7", Port: 2053, Fingerprint: testFP, Secret: testSecret},
			"dey://" + testToken + "@203.0.113.7:2053/" + testSecret + "#" + testFP},
		{"ipv6", "dey://" + testToken + "@[2001:DB8::1]:8443/" + testSecret + "#" + testFP,
			JoinLink{Token: testToken, Host: "2001:db8::1", Port: 8443, Fingerprint: testFP, Secret: testSecret},
			"dey://" + testToken + "@[2001:db8::1]:8443/" + testSecret + "#" + testFP},
		{"tls off", "dey://" + testToken + "@cdn.example.com:8080/" + testSecret + "?tls=0#" + testFP,
			JoinLink{Token: testToken, Host: "cdn.example.com", Port: 8080, Fingerprint: testFP, Secret: testSecret, TLS: boolp(false)},
			"dey://" + testToken + "@cdn.example.com:8080/" + testSecret + "?tls=0#" + testFP},
		{"tls on, bare hex, quoted", "  'dey://" + testToken + "@cdn.example.com:8080/" + testSecret + "?tls=1#" + testFP[7:] + "'\n",
			JoinLink{Token: testToken, Host: "cdn.example.com", Port: 8080, Fingerprint: testFP, Secret: testSecret, TLS: boolp(true)},
			"dey://" + testToken + "@cdn.example.com:8080/" + testSecret + "?tls=1#" + testFP},
		{"ipv6 with tls", "dey://" + testToken + "@[::1]:443/" + testSecret + "?tls=1#" + testFP,
			JoinLink{Token: testToken, Host: "::1", Port: 443, Fingerprint: testFP, Secret: testSecret, TLS: boolp(true)},
			"dey://" + testToken + "@[::1]:443/" + testSecret + "?tls=1#" + testFP},
		{"direct with query", "dey://" + testToken + "@1.2.3.4:44433?tls=1#" + testFP,
			JoinLink{Token: testToken, Host: "1.2.3.4", Port: 44433, Fingerprint: testFP, TLS: boolp(true)},
			"dey://" + testToken + "@1.2.3.4:44433?tls=1#" + testFP},
		{"empty path is direct", "dey://" + testToken + "@1.2.3.4:44433/#" + testFP,
			JoinLink{Token: testToken, Host: "1.2.3.4", Port: 44433, Fingerprint: testFP},
			"dey://" + testToken + "@1.2.3.4:44433#" + testFP},
		{"min secret", "dey://" + testToken + "@h.example:443/" + strings.Repeat("a", 16) + "#" + testFP,
			JoinLink{Token: testToken, Host: "h.example", Port: 443, Fingerprint: testFP, Secret: strings.Repeat("a", 16)},
			"dey://" + testToken + "@h.example:443/" + strings.Repeat("a", 16) + "#" + testFP},
		{"max secret", "dey://" + testToken + "@h.example:443/" + strings.Repeat("-", 128) + "#" + testFP,
			JoinLink{Token: testToken, Host: "h.example", Port: 443, Fingerprint: testFP, Secret: strings.Repeat("-", 128)},
			"dey://" + testToken + "@h.example:443/" + strings.Repeat("-", 128) + "#" + testFP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := ParseJoinLink(tc.in)
			require.NoError(t, err)
			require.Equal(t, tc.want, l)
			require.Equal(t, tc.out, l.String())
			require.Equal(t, tc.want.Secret != "", l.Front())
			require.Equal(t, net.JoinHostPort(tc.want.Host, strconv.Itoa(tc.want.Port)), l.Addr())
			again, err := ParseJoinLink(l.String())
			require.NoError(t, err)
			require.Equal(t, l, again)
			require.Equal(t, tc.out, again.String())
		})
	}
}

func TestJoinLinkFrontInvalid(t *testing.T) {
	base := "dey://" + testToken + "@cdn.example.com:443"
	long := strings.Repeat("a", 129)
	cases := []struct {
		name string
		in   string
		leak string // extra text that must not appear in the error
	}{
		{"short secret", base + "/shortsecret#" + testFP, "shortsecret"},
		{"long secret", base + "/" + long + "#" + testFP, long},
		{"bad charset", base + "/" + testSecret[:20] + "!" + testSecret[21:] + "#" + testFP, testSecret[:20]},
		{"padding", base + "/" + testSecret + "=#" + testFP, testSecret},
		{"space in secret", base + "/" + testSecret[:20] + " " + testSecret[21:] + "#" + testFP, testSecret[:20]},
		{"unicode", base + "/" + testSecret[:16] + "é" + testSecret[18:] + "#" + testFP, testSecret[:16]},
		{"two segments", base + "/" + testSecret + "/c#" + testFP, testSecret},
		{"trailing slash", base + "/" + testSecret + "/#" + testFP, testSecret},
		{"empty segment", base + "//" + testSecret + "#" + testFP, testSecret},
		{"double slash empty", base + "//#" + testFP, ""},
		{"percent", base + "/" + strings.Repeat("%41", 16) + "#" + testFP, ""},
		{"dot dot", base + "/../" + testSecret + "#" + testFP, testSecret},
		{"at in secret", base + "/" + testSecret[:20] + "@" + testSecret[21:] + "#" + testFP, testSecret[:20]},
		{"unknown query", base + "/" + testSecret + "?x=1#" + testFP, testSecret},
		{"bad tls value", base + "/" + testSecret + "?tls=2#" + testFP, testSecret},
		{"tls upper", base + "/" + testSecret + "?TLS=1#" + testFP, testSecret},
		{"empty query", base + "/" + testSecret + "?#" + testFP, testSecret},
		{"two params", base + "/" + testSecret + "?tls=1&tls=1#" + testFP, testSecret},
		{"two question marks", base + "/" + testSecret + "?tls=1?tls=1#" + testFP, testSecret},
		{"query before path", base + "?tls=1/" + testSecret + "#" + testFP, testSecret},
		{"path without port", "dey://" + testToken + "@cdn.example.com/" + testSecret + "#" + testFP, testSecret},
		{"secret instead of token", "dey://cdn.example.com:443/" + testSecret + "#" + testFP, testSecret},
		{"token and secret swapped with bad host", "dey://" + testSecret + "@cdn_example.com:443/" + testToken + "#" + testFP, testToken},
		{"query after fingerprint", base + "#" + testFP + "?x=" + testSecret, testSecret},
		{"no fingerprint", base + "/" + testSecret, testSecret},
		{"path after fingerprint", base + "#" + testFP + "/" + testSecret, testSecret},
		{"bad host with path", "dey://" + testToken + "@bad_host!:443/" + testSecret + "#" + testFP, testSecret},
		{"bad port with path", "dey://" + testToken + "@cdn.example.com:0/" + testSecret + "#" + testFP, testSecret},
		{"bad token with path", "dey://short@cdn.example.com:443/" + testSecret + "#" + testFP, testSecret},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseJoinLink(tc.in)
			requireNoLeak(t, err, testToken, testSecret, tc.leak)
		})
	}
}

// legacyParse is the parser as it was before front links existed. Direct
// links must keep producing exactly what it produced.
func legacyParse(link string) (JoinLink, string) {
	s := strings.TrimSpace(link)
	s = strings.Trim(s, `'"`)
	s = strings.TrimSpace(s)
	if len(s) < len(JoinLinkScheme) || !strings.EqualFold(s[:len(JoinLinkScheme)], JoinLinkScheme) {
		return JoinLink{}, "the link must start with dey://"
	}
	rest := s[len(JoinLinkScheme):]
	rest, frag, ok := strings.Cut(rest, "#")
	if !ok || frag == "" {
		return JoinLink{}, "the CA fingerprint (#sha256:...) is missing"
	}
	fp, ok := tlsutil.NormalizeFingerprint(frag)
	if !ok || strings.Contains(frag, ":") && !strings.HasPrefix(strings.ToLower(frag), tlsutil.FingerprintPrefix) {
		return JoinLink{}, "the CA fingerprint must be sha256:<64 hex digits>"
	}
	at := strings.LastIndexByte(rest, '@')
	if at < 0 {
		return JoinLink{}, "the token (TOKEN@) is missing"
	}
	token, hostport := rest[:at], rest[at+1:]
	if !tokenRe.MatchString(token) {
		return JoinLink{}, "the token is not valid base64url"
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return JoinLink{}, "the hub address must be HOST:PORT (IPv6 as [addr]:port)"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return JoinLink{}, "the hub port must be 1-65535"
	}
	if ip := net.ParseIP(host); ip != nil {
		if strings.Contains(host, ":") != strings.HasPrefix(hostport, "[") {
			return JoinLink{}, "an IPv6 hub address must be written as [addr]:port"
		}
		host = ip.String()
	} else if strings.Contains(host, "%") || !hostnameRe.MatchString(host) || len(host) > 253 {
		return JoinLink{}, "the hub host is not a valid IP address or name"
	}
	return JoinLink{Token: token, Host: host, Port: port, Fingerprint: fp}, ""
}

// legacyFormat is FormatJoinLink as it was before front links existed.
func legacyFormat(l JoinLink) string {
	fp := l.Fingerprint
	if n, ok := tlsutil.NormalizeFingerprint(fp); ok {
		fp = n
	}
	return JoinLinkScheme + l.Token + "@" + l.Addr() + "#" + fp
}

// directCorpus is a set of direct-link inputs, valid and not.
func directCorpus() []string {
	var out []string
	hosts := []string{"203.0.113.7", "[2001:db8::1]", "[::1]", "hub.example.com", "a", "[1.2.3.4]", "bad_host!", "-x.example", "2001:db8::1", "h%eth0"}
	ports := []string{"44433", "443", "1", "65535", "0", "70000", "http", ""}
	frags := []string{testFP, testFP[7:], strings.ToUpper(testFP), "sha256:abcd", "md5:" + testFP[7:], "", "x"}
	tokens := []string{testToken, testToken + "=", "short", "bad!tok" + testToken, ""}
	for _, h := range hosts {
		for _, p := range ports {
			for _, f := range frags {
				for _, tk := range tokens {
					addr := h
					if p != "" {
						addr += ":" + p
					}
					out = append(out, "dey://"+tk+"@"+addr+"#"+f)
				}
			}
		}
	}
	out = append(out, "", "dey://", "DEY://"+testToken, "https://x", "dey://"+testToken+"@1.2.3.4:1",
		"  \"dey://"+testToken+"@1.2.3.4:44433#"+testFP+"\" ")
	return out
}

func TestJoinLinkDirectUnchanged(t *testing.T) {
	valid := 0
	for _, in := range directCorpus() {
		old, oldReason := legacyParse(in)
		got, err := ParseJoinLink(in)
		if oldReason == "" {
			valid++
			require.NoError(t, err, in)
			require.Equal(t, old, got, in)
			require.Equal(t, legacyFormat(old), FormatJoinLink(got), in)
			require.Equal(t, legacyFormat(old), got.String(), in)
			require.False(t, got.Front())
			require.Nil(t, got.TLS)
			continue
		}
		// The corpus has no "/" or "?", so a rejected link is rejected for
		// the very same reason as before.
		e := requireCode(t, err, deyerr.N006)
		require.Equal(t, oldReason, e.Detail, in)
	}
	require.Greater(t, valid, 100)
}

func TestJoinLinkDirectGolden(t *testing.T) {
	l := JoinLink{Token: testToken, Host: "203.0.113.7", Port: 44433, Fingerprint: testFP}
	require.Equal(t, "dey://q3Vx8o2m7kP0sT9wYbZ1cD4eF6gH8iJ0kL2mN4oP6qR@203.0.113.7:44433#"+testFP, FormatJoinLink(l))
	l.Host = "2001:db8::1"
	require.Equal(t, "dey://q3Vx8o2m7kP0sT9wYbZ1cD4eF6gH8iJ0kL2mN4oP6qR@[2001:db8::1]:44433#"+testFP, l.String())
}

// leaks reports whether text shows sub, a part of the input in. A part that
// also occurs elsewhere in the input (a host or fingerprint of the same
// digits) may legitimately be shown, so only a unique one counts.
func leaks(in, text, sub string) bool {
	return strings.Count(in, sub) == 1 && strings.Contains(text, sub)
}

func FuzzParseJoinLink(f *testing.F) {
	for _, s := range []string{
		"dey://" + testToken + "@203.0.113.7:44433#" + testFP,
		"dey://" + testToken + "@[2001:db8::1]:443/" + testSecret + "#" + testFP,
		"dey://" + testToken + "@cdn.example.com:8080/" + testSecret + "?tls=0#" + testFP,
		"dey://" + testToken + "@cdn.example.com:8080?tls=1#" + testFP[7:],
		"dey://" + testToken + "@h:1//#" + testFP,
		"dey://" + testToken + "@h:1/a/b?x#" + testFP,
		"dey://@@@:/?#",
		"dey://" + testSecret + "/" + testToken + "@h:1#" + testFP,
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		l, err := ParseJoinLink(in)
		if err != nil {
			e := deyerr.As(err)
			if e == nil || e.Code != deyerr.N006 {
				t.Fatalf("error is not N006: %v", err)
			}
			// The error never echoes a token or a front secret the input
			// carried (before the last "@" / after the first "/").
			text := errText(e)
			s := strings.TrimSpace(strings.Trim(strings.TrimSpace(in), `'"`))
			if len(s) > len(JoinLinkScheme) && strings.EqualFold(s[:len(JoinLinkScheme)], JoinLinkScheme) {
				rest, _, _ := strings.Cut(s[len(JoinLinkScheme):], "#")
				rest, _, _ = strings.Cut(rest, "?")
				if slash := strings.IndexByte(rest, '/'); slash >= 0 {
					if sec := rest[slash+1:]; secretRe.MatchString(sec) && leaks(s, text, sec) {
						t.Fatalf("error leaks the secret: %q", text)
					}
					rest = rest[:slash]
				}
				if at := strings.LastIndexByte(rest, '@'); at >= 0 {
					if tok := rest[:at]; tokenRe.MatchString(tok) && leaks(s, text, tok) {
						t.Fatalf("error leaks the token: %q", text)
					}
				}
			}
			return
		}
		if l.Secret != "" && !secretRe.MatchString(l.Secret) {
			t.Fatalf("bad secret accepted: %q", l.Secret)
		}
		if l.Port < 1 || l.Port > 65535 || !tokenRe.MatchString(l.Token) {
			t.Fatalf("bad link accepted: %+v", l)
		}
		out := FormatJoinLink(l)
		again, err := ParseJoinLink(out)
		if err != nil {
			t.Fatalf("round trip of %+v failed: %v", l, err)
		}
		if again.Token != l.Token || again.Host != l.Host || again.Port != l.Port ||
			again.Fingerprint != l.Fingerprint || again.Secret != l.Secret ||
			(again.TLS == nil) != (l.TLS == nil) || (l.TLS != nil && *again.TLS != *l.TLS) {
			t.Fatalf("round trip changed the link: %+v -> %+v", l, again)
		}
		if FormatJoinLink(again) != out {
			t.Fatalf("format is not stable: %q", out)
		}
		// A link with no path or query is a direct link and must read the
		// same under the pre-front grammar.
		if l.Secret == "" && l.TLS == nil {
			if old, reason := legacyParse(out); reason != "" || old != again {
				t.Fatalf("direct link differs from the legacy parse: %q %+v", reason, old)
			}
		}
	})
}
