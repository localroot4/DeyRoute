package setup

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

const testLink = "dey://Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZg@5.6.7.8:44433#sha256:" +
	"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestJoinCommandFormat(t *testing.T) {
	url := "https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh"
	require.Equal(t,
		"bash <(curl -fsSL "+url+") join '"+testLink+"'",
		JoinCommand(url, testLink, "dev"))
	require.Equal(t,
		"bash <(curl -fsSL "+url+") join '"+testLink+"' --version 1.2.3",
		JoinCommand(url, testLink, "1.2.3"))
	require.Equal(t,
		"bash <(curl -fsSL "+url+") join '"+testLink+"' --version 1.0.0-rc.1",
		JoinCommand(url, testLink, "v1.0.0-rc.1"))
	for _, v := range []string{"", "dev", "latest", "1", "1.2.3; rm -rf /", "$(id)", "v"} {
		require.Equalf(t, "bash <(curl -fsSL "+url+") join '"+testLink+"'", JoinCommand(url, testLink, v), "version %q", v)
	}
	require.Equal(t, `bash <(curl -fsSL u) join 'a'\''b'`, JoinCommand("u", "a'b", ""))

	// A normal mirror URL stays unquoted (the exact spec format).
	mirror := InstallerURL("https://cdn.example.com/deyroute")
	require.Equal(t, "bash <(curl -fsSL https://cdn.example.com/deyroute/latest/install.sh) join '"+testLink+"'",
		JoinCommand(mirror, testLink, ""))
	// Anything the shell would interpret is single-quoted, so the pasted
	// line neither breaks ("&" of a signed URL) nor runs extra commands.
	for url, want := range map[string]string{
		"https://m.example/i.sh?a=1&b=2":         `'https://m.example/i.sh?a=1&b=2'`,
		"https://m.example/$(id)/install.sh":     `'https://m.example/$(id)/install.sh'`,
		"https://m.example/`id`;x/install.sh":    "'https://m.example/`id`;x/install.sh'",
		"https://m.example/a b/install.sh":       `'https://m.example/a b/install.sh'`,
		"https://m.example/it's/install.sh":      `'https://m.example/it'\''s/install.sh'`,
		"https://m.example/x)|sh;(/install.sh":   `'https://m.example/x)|sh;(/install.sh'`,
		"https://m.example/~u/%41/+,@=/i.sh":     "https://m.example/~u/%41/+,@=/i.sh",
		"https://m.example:8443/deyroute/i-1.sh": "https://m.example:8443/deyroute/i-1.sh",
		"https://m.example/{a,b}/install.sh":     `'https://m.example/{a,b}/install.sh'`,
		"https://m.example/*/install.sh":         `'https://m.example/*/install.sh'`,
		"https://m.example/x\n/install.sh":       "'https://m.example/x\n/install.sh'",
		"https://m.example/x#frag/install.sh":    `'https://m.example/x#frag/install.sh'`,
		"https://m.example/x!hist/install.sh":    `'https://m.example/x!hist/install.sh'`,
		"https://m.example/x>out/install.sh":     `'https://m.example/x>out/install.sh'`,
		"https://m.example/x\\y/install.sh":      `'https://m.example/x\y/install.sh'`,
		"https://m.example/x\"y\"/install.sh":    `'https://m.example/x"y"/install.sh'`,
		"https://m.example/x[1]/install.sh":      `'https://m.example/x[1]/install.sh'`,
		"https://m.example/x\ty/install.sh":      "'https://m.example/x\ty/install.sh'",
		"https://m.example/x&&reboot/i.sh":       `'https://m.example/x&&reboot/i.sh'`,
		"https://m.example/x|reboot/install.sh":  `'https://m.example/x|reboot/install.sh'`,
	} {
		require.Equalf(t, "bash <(curl -fsSL "+want+") join '"+testLink+"'", JoinCommand(url, testLink, ""), "url %q", url)
	}
}

func TestInstallerURL(t *testing.T) {
	require.Equal(t, "https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh", InstallerURL(""))
	require.Equal(t, "https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh", InstallerURL("  "))
	require.Equal(t, "https://mirror.example/deyroute/latest/install.sh", InstallerURL("https://mirror.example/deyroute/"))
	require.Equal(t, "https://cdn.example/latest/install.sh", InstallerURL(" https://cdn.example "))
}

func TestParseRouteSource(t *testing.T) {
	cases := map[string]string{
		"1.1.1.1 via 5.6.7.1 dev eth0 src 5.6.7.8 uid 0 \n    cache":                                  "5.6.7.8",
		"1.1.1.1 dev ppp0 src 100.72.1.2 uid 0":                                                       "100.72.1.2",
		"2606:4700:4700::1111 from :: via fe80::1 dev eth0 proto ra src 2a01:4f8::5 metric 1024 pref": "2a01:4f8::5",
		"local 1.1.1.1 dev lo table local src 1.1.1.1 uid 0":                                          "1.1.1.1",
	}
	for out, want := range cases {
		a, ok := parseRouteSource(out)
		require.Truef(t, ok, "%q", out)
		require.Equal(t, want, a.String())
	}
	for _, out := range []string{"", "1.1.1.1 via 5.6.7.1 dev eth0", "x src", "x src not-an-ip", "x src fe80::1%eth0"} {
		_, ok := parseRouteSource(out)
		require.Falsef(t, ok, "%q", out)
	}
}

func TestIsPublicIP(t *testing.T) {
	for s, want := range map[string]bool{
		"5.6.7.8":         true,
		"2a01:4f8::5":     true,
		"10.1.2.3":        false,
		"172.16.0.1":      false,
		"172.32.0.1":      true,
		"192.168.1.10":    false,
		"100.64.0.1":      false,
		"100.127.255.254": false,
		"100.128.0.1":     true,
		"127.0.0.1":       false,
		"169.254.1.1":     false,
		"0.0.0.0":         false,
		"224.0.0.1":       false,
		"::1":             false,
		"fe80::1":         false,
		"fd00::1":         false,
		"::ffff:10.0.0.1": false,
		"::ffff:5.6.7.8":  true,
	} {
		require.Equalf(t, want, IsPublicIP(netip.MustParseAddr(s)), "%s", s)
	}
	require.False(t, IsPublicIP(netip.Addr{}))
}

func TestDetectPublicIP(t *testing.T) {
	ctx := context.Background()
	f := exec.NewFake()
	f.On(route4Line, exec.OK(route4Out))
	ip, private, err := DetectPublicIP(ctx, f)
	require.NoError(t, err)
	require.Equal(t, "5.6.7.8", ip)
	require.False(t, private)

	f.On(route4Line, exec.OK("1.1.1.1 via 192.168.1.1 dev wlan0 src 192.168.1.20 uid 0"))
	ip, private, err = DetectPublicIP(ctx, f)
	require.NoError(t, err)
	require.Equal(t, "192.168.1.20", ip)
	require.True(t, private)

	f.On(route4Line, exec.OK("garbage"))
	_, _, err = DetectPublicIP(ctx, f)
	e := requireTop(t, err, deyerr.I020)
	require.Contains(t, e.Why(), "no source address")
	require.Equal(t, "garbage", e.Detail)

	f.On(route4Line, exec.Fail(2, noRoute6))
	_, _, err = DetectPublicIP(ctx, f)
	e = requireTop(t, err, deyerr.I020)
	require.Contains(t, e.Why(), "no route")

	f.On(route4Line, exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "ip"})})
	_, _, err = DetectPublicIP(ctx, f)
	requireTop(t, err, deyerr.I010)

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, _, err = DetectPublicIP(cctx, f)
	requireTop(t, err, deyerr.X031)
}

func TestDetectPublicIP6(t *testing.T) {
	ctx := context.Background()
	f := exec.NewFake()
	f.On(route6Line, exec.Fail(2, noRoute6))
	ip, private, err := DetectPublicIP6(ctx, f)
	require.NoError(t, err, "no IPv6 route is not an error")
	require.Empty(t, ip)
	require.False(t, private)

	f.On(route6Line, exec.OK("2606:4700:4700::1111 from :: via fe80::1 dev eth0 src fd12::7 metric 1024"))
	ip, private, err = DetectPublicIP6(ctx, f)
	require.NoError(t, err)
	require.Equal(t, "fd12::7", ip)
	require.True(t, private)

	f.On(route6Line, exec.Response{Err: deyerr.New(deyerr.X030, deyerr.Params{"command": "ip"})})
	_, _, err = DetectPublicIP6(ctx, f)
	requireTop(t, err, deyerr.I010)
}

func TestSuggestControlPort(t *testing.T) {
	require.Equal(t, 44433, SuggestControlPort(0, nil))
	require.Equal(t, 44433, SuggestControlPort(70000, nil))
	require.Equal(t, 44433, SuggestControlPort(44433, func(int) bool { return false }))
	busy := map[int]bool{44433: true, 44434: true}
	require.Equal(t, 44435, SuggestControlPort(44433, func(p int) bool { return busy[p] }))
	require.Equal(t, 23, SuggestControlPort(22, nil), "22 is never suggested")
	require.Equal(t, 32000, SuggestControlPort(29999, func(p int) bool { return p == 29999 }))
	require.Equal(t, 32000, SuggestControlPort(30500, nil), "the backend control range is skipped")
	require.Equal(t, 1024, SuggestControlPort(65535, func(p int) bool { return p == 65535 }), "wraps around")
	require.Equal(t, 0, SuggestControlPort(44433, func(int) bool { return true }))
}

func TestPortBusyAndCheckControlPort(t *testing.T) {
	ctx := context.Background()
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	f := exec.NewFake()
	require.True(t, PortBusy(ctx, f, t.TempDir())(port))
	requireTop(t, CheckControlPort(ctx, f, "", port), deyerr.P012)
	require.NoError(t, ln.Close())
	require.False(t, PortBusy(ctx, f, t.TempDir())(port))
	require.NoError(t, CheckControlPort(ctx, f, t.TempDir(), port))
}
