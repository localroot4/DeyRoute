package ports

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func tcp(p int) Spec { return Spec{Listen: p, Proto: ProtoTCP, Target: DefaultTarget(p)} }
func udp(p int) Spec { return Spec{Listen: p, Proto: ProtoUDP, Target: DefaultTarget(p)} }

func TestParseInputValid(t *testing.T) {
	rng := func(lo, hi int, mk func(int) Spec) []Spec {
		var out []Spec
		for p := lo; p <= hi; p++ {
			out = append(out, mk(p))
		}
		return out
	}
	tests := []struct {
		in   string
		want []Spec
	}{
		// Every example of spec sections 6 and 10.
		{"443", []Spec{tcp(443)}},
		{"443/udp", []Spec{udp(443)}},
		{"443,2053", []Spec{tcp(443), tcp(2053)}},
		{"2000-2010", rng(2000, 2010, tcp)},
		{"443:8443", []Spec{{Listen: 443, Proto: ProtoTCP, Target: "127.0.0.1:8443"}}},
		{"443/tcp,27015/udp", []Spec{tcp(443), udp(27015)}},
		{"443,2053,8443", []Spec{tcp(443), tcp(2053), tcp(8443)}},
		{"443:8443/udp", []Spec{{Listen: 443, Proto: ProtoUDP, Target: "127.0.0.1:8443"}}},
		// Whitespace tolerance and case.
		{"  443 , 2053  ", []Spec{tcp(443), tcp(2053)}},
		{"443 / UDP", []Spec{udp(443)}},
		{"443 : 8443", []Spec{{Listen: 443, Proto: ProtoTCP, Target: "127.0.0.1:8443"}}},
		{"2000 - 2002/udp", rng(2000, 2002, udp)},
		{"443 2053\t8443", []Spec{tcp(443), tcp(2053), tcp(8443)}},
		{"443,,2053,", []Spec{tcp(443), tcp(2053)}},
		{"443/Tcp", []Spec{tcp(443)}},
		// Duplicates removed, first occurrence and order kept.
		{"443,443/tcp,2053,443", []Spec{tcp(443), tcp(2053)}},
		{"443,443/udp", []Spec{tcp(443), udp(443)}},
		{"2000-2002,2001", rng(2000, 2002, tcp)},
		{"443:443,443", []Spec{tcp(443)}},
		// Single-port range, leading zeros, bounds.
		{"5000-5000", []Spec{tcp(5000)}},
		{"0443", []Spec{tcp(443)}},
		{"1,65535/udp", []Spec{tcp(1), udp(65535)}},
		// Targets on other hosts.
		{"443:10.0.0.5:8443", []Spec{{Listen: 443, Proto: ProtoTCP, Target: "10.0.0.5:8443"}}},
		{"443:[::1]:8443/udp", []Spec{{Listen: 443, Proto: ProtoUDP, Target: "[::1]:8443"}}},
		{"80:Backend.Local:8080", []Spec{{Listen: 80, Proto: ProtoTCP, Target: "backend.local:8080"}}},
		{"443:[::ffff:10.0.0.1]:443", []Spec{{Listen: 443, Proto: ProtoTCP, Target: "10.0.0.1:443"}}},
		// Exactly 64 is allowed.
		{"1000-1063", rng(1000, 1063, tcp)},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseInput(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseInputErrors(t *testing.T) {
	tests := []struct {
		in   string
		code deyerr.Code
		// param is the expected "input" (or "count") parameter.
		param any
	}{
		{"", deyerr.C020, ""},
		{"   ", deyerr.C020, "   "},
		{",", deyerr.C020, ","},
		{"abc", deyerr.C020, "abc"},
		{"443/sctp", deyerr.C020, "443/sctp"},
		{"443/", deyerr.C020, "443/"},
		{"/udp", deyerr.C020, "/udp"},
		{"443,https", deyerr.C020, "https"},
		{"443 udp", deyerr.C020, "udp"},
		{"-443", deyerr.C020, "-443"},
		{"443-", deyerr.C020, "443-"},
		{"+443", deyerr.C020, "+443"},
		{"44.3", deyerr.C020, "44.3"},
		{"443:", deyerr.C020, "443:"},
		{":443", deyerr.C020, ":443"},
		{"443:::1:80", deyerr.C020, "443:::1:80"},
		{"443:bad_host:80", deyerr.C020, "443:bad_host:80"},
		{"443:[fe80::1%eth0]:80", deyerr.C020, "443:[fe80::1%eth0]:80"},
		{"443:[host]:80", deyerr.C020, "443:[host]:80"},
		{"2000-2010:3000", deyerr.C020, "2000-2010:3000"},
		{"0", deyerr.P010, "0"},
		{"65536", deyerr.P010, "65536"},
		{"99999999999999999999", deyerr.P010, "99999999999999999999"},
		{"443:0", deyerr.P010, "443:0"},
		{"443:70000/udp", deyerr.P010, "443:70000/udp"},
		{"443:10.0.0.1:0", deyerr.P010, "443:10.0.0.1:0"},
		{"0-10", deyerr.P010, "0-10"},
		{"65000-65536", deyerr.P010, "65000-65536"},
		{"2010-2000", deyerr.P017, "2010-2000"},
		{"1000-1064", deyerr.P016, 65},
		{"1-65535", deyerr.P016, 65535},
		{"1000-1060,2000-2010,1000-1005", deyerr.P016, 72},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseInput(tt.in)
			require.Nil(t, got)
			require.Error(t, err)
			e := deyerr.As(err)
			require.Equal(t, tt.code, e.Code, err.Error())
			key := "input"
			if tt.code == deyerr.P016 {
				key = "count"
			}
			require.Equal(t, tt.param, e.Params[key])
		})
	}
}

func TestParseInputConflictingTargets(t *testing.T) {
	_, err := ParseInput("443:8443,443")
	e := deyerr.As(err)
	require.Equal(t, deyerr.P021, e.Code)
	require.Equal(t, "443/tcp", e.Params["port"])
	require.Equal(t, "127.0.0.1:8443", e.Params["target"])
	require.Equal(t, "127.0.0.1:443", e.Params["other"])
	require.Contains(t, e.Message(), "443/tcp")
}

func TestFormat(t *testing.T) {
	require.Equal(t, "443/tcp", FormatSpec(tcp(443)))
	require.Equal(t, "27015/udp", udp(27015).String())

	tests := []struct {
		in, want string
	}{
		{"443,2053", "443,2053"},
		{"443/tcp,27015/udp", "443,27015/udp"},
		{"2000-2010", "2000-2010"},
		{"2000-2001", "2000,2001"},
		{"2000-2010/udp,443", "2000-2010/udp,443"},
		{"443:8443,443:8443/udp", "443:8443,443:8443/udp"},
		{"443:10.0.0.5:8443", "443:10.0.0.5:8443"},
		{"443:[::1]:8443/udp", "443:[::1]:8443/udp"},
		{"1000,1001,1002,1003/udp,1004", "1000-1002,1003/udp,1004"},
		{"5,4,3,2,1", "5,4,3,2,1"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			specs, err := ParseInput(tt.in)
			require.NoError(t, err)
			got := FormatList(specs)
			require.Equal(t, tt.want, got)
			back, err := ParseInput(got)
			require.NoError(t, err)
			require.Equal(t, specs, back, "round trip")
		})
	}
	require.Equal(t, "", FormatList(nil))
	// A spec without a target formats like the default.
	require.Equal(t, "443", FormatList([]Spec{{Listen: 443, Proto: ProtoTCP}}))
}

func TestProtoHelpers(t *testing.T) {
	p, ok := NormalizeProto(" UDP ")
	require.True(t, ok)
	require.Equal(t, ProtoUDP, p)
	_, ok = NormalizeProto("icmp")
	require.False(t, ok)
	require.True(t, ValidProto("tcp"))
	require.False(t, ValidProto("TCP"))
	require.True(t, ValidPort(1))
	require.True(t, ValidPort(65535))
	require.False(t, ValidPort(0))
	require.False(t, ValidPort(65536))
	require.Equal(t, "127.0.0.1:8443", DefaultTarget(8443))
}

func TestParseInputLargeCount(t *testing.T) {
	var items []string
	for p := 1000; p < 1100; p++ {
		items = append(items, fmt.Sprint(p))
	}
	_, err := ParseInput(strings.Join(items, ","))
	e := deyerr.As(err)
	require.Equal(t, deyerr.P016, e.Code)
	require.Equal(t, 100, e.Params["count"])

	// Distinct (port, proto) pairs are counted across overlapping ranges
	// and protocols; duplicates beyond the first 64 do not inflate it.
	_, err = ParseInput("1-100,50-150,1-10/udp,1-100")
	require.Equal(t, 160, deyerr.As(err).Params["count"])
}

// TestParseInputHugePaste guards against expanding ranges per item: a long
// paste of full-range items used to take seconds per kilobyte.
func TestParseInputHugePaste(t *testing.T) {
	in := strings.Repeat("1-65535,1-65535/udp,", 2000) // ~40 KB
	start := time.Now()
	_, err := ParseInput(in)
	elapsed := time.Since(start)
	e := deyerr.As(err)
	require.Equal(t, deyerr.P016, e.Code)
	require.Equal(t, 2*65535, e.Params["count"])
	require.Less(t, elapsed, 5*time.Second, "parsing must be linear in the input")
}

func TestParseInputConflictAfterRange(t *testing.T) {
	// A range keeps default targets; a later listen:target for one of its
	// ports is a conflict, an identical default entry is not.
	_, err := ParseInput("2000-2002,2001:9000")
	e := deyerr.As(err)
	require.Equal(t, deyerr.P021, e.Code)
	require.Equal(t, "2001/tcp", e.Params["port"])
	require.Equal(t, "127.0.0.1:2001", e.Params["target"])
	require.Equal(t, "127.0.0.1:9000", e.Params["other"])

	got, err := ParseInput("2001:2001,2000-2002")
	require.NoError(t, err)
	require.Equal(t, []Spec{tcp(2001), tcp(2000), tcp(2002)}, got)

	// The same port on the other protocol is independent.
	got, err = ParseInput("443:8443,443/udp")
	require.NoError(t, err)
	require.Equal(t, []Spec{{Listen: 443, Proto: ProtoTCP, Target: "127.0.0.1:8443"}, udp(443)}, got)
}
