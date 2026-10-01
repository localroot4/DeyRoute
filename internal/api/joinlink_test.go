package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

const (
	testToken = "q3Vx8o2m7kP0sT9wYbZ1cD4eF6gH8iJ0kL2mN4oP6qR"
	testFP    = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestJoinLinkRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
	}{
		{"dey://" + testToken + "@203.0.113.7:44433#" + testFP, "203.0.113.7", 44433},
		{"  'dey://" + testToken + "@203.0.113.7:44433#" + strings.ToUpper(testFP[7:]) + "'  ", "203.0.113.7", 44433},
		{"DEY://" + testToken + "@[2001:db8::1]:443#SHA256:" + testFP[7:], "2001:db8::1", 443},
		{`"dey://` + testToken + `@hub.example.com:44433#` + testFP + `"`, "hub.example.com", 44433},
	}
	for _, tc := range cases {
		l, err := ParseJoinLink(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, testToken, l.Token)
		require.Equal(t, tc.host, l.Host)
		require.Equal(t, tc.port, l.Port)
		require.Equal(t, testFP, l.Fingerprint)
		again, err := ParseJoinLink(l.String())
		require.NoError(t, err)
		require.Equal(t, l, again)
	}
	l := JoinLink{Token: testToken, Host: "2001:db8::1", Port: 44433, Fingerprint: strings.ToUpper(testFP[7:])}
	require.Equal(t, "dey://"+testToken+"@[2001:db8::1]:44433#"+testFP, FormatJoinLink(l))
	require.Equal(t, "[2001:db8::1]:44433", l.Addr())
}

func TestJoinLinkInvalid(t *testing.T) {
	bad := []string{
		"",
		"https://" + testToken + "@1.2.3.4:1#" + testFP,
		"dey://" + testToken + "@1.2.3.4:44433",
		"dey://" + testToken + "@1.2.3.4:44433#",
		"dey://" + testToken + "@1.2.3.4:44433#sha256:abcd",
		"dey://" + testToken + "@1.2.3.4:44433#md5:" + testFP[7:],
		"dey://1.2.3.4:44433#" + testFP,
		"dey://short@1.2.3.4:44433#" + testFP,
		"dey://" + testToken + "!@1.2.3.4:44433#" + testFP,
		"dey://" + testToken + "@1.2.3.4#" + testFP,
		"dey://" + testToken + "@1.2.3.4:0#" + testFP,
		"dey://" + testToken + "@1.2.3.4:70000#" + testFP,
		"dey://" + testToken + "@1.2.3.4:http#" + testFP,
		"dey://" + testToken + "@2001:db8::1:443#" + testFP,
		"dey://" + testToken + "@bad_host!:443#" + testFP,
		"dey://" + testToken + "@-bad.example:443#" + testFP,
	}
	for _, s := range bad {
		_, err := ParseJoinLink(s)
		e := requireCode(t, err, deyerr.N006)
		require.NotContains(t, e.Error(), testToken, s)
		require.NotContains(t, e.Detail, testToken)
		for _, v := range e.Params {
			require.NotContains(t, v, testToken)
		}
		require.NotEmpty(t, e.Detail, s)
	}
}

func TestRedactJoinLink(t *testing.T) {
	require.Equal(t, "***", redactJoinLink("nothing"))
	require.Equal(t, "dey://***", redactJoinLink("dey://tokenonly"))
	require.Equal(t, "dey://***@1.2.3.4:1#x", redactJoinLink("dey://secret@1.2.3.4:1#x"))
	long := redactJoinLink("dey://" + strings.Repeat("a", 400))
	require.Equal(t, "dey://***", long)
}
