package ports

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestReservedExtra(t *testing.T) {
	// The extra ports are reserved on top of the built-in rules.
	r, why := Reserved(2053, 44433, 2053)
	require.True(t, r)
	require.Equal(t, "port 2053 is reserved on this server", why)
	r, _ = Reserved(443, 44433, 2053)
	require.False(t, r)
	r, why = Reserved(22, 44433, 22)
	require.True(t, r)
	require.Equal(t, "port 22 is reserved for SSH", why, "the built-in rule wins")

	// Same answer as config validation for every port.
	for p := 0; p <= 65536; p++ {
		r1, w1 := Reserved(p, 44433, 2053, 8443)
		r2, w2 := config.ReservedListen(p, 44433, []int{2053, 8443})
		require.Equal(t, r2, r1, p)
		require.Equal(t, w2, w1, p)
	}
}

func TestCheckReservedExtra(t *testing.T) {
	require.NoError(t, CheckReserved([]Spec{tcp(443)}, 44433, 2053))
	require.NoError(t, CheckReserved([]Spec{tcp(2053)}, 44433), "not reserved without the extra port")

	err := CheckReserved([]Spec{tcp(443), udp(2053), tcp(8443)}, 44433, 2053, 8443)
	require.Error(t, err)
	var codes []deyerr.Code
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		codes = append(codes, deyerr.As(e).Code)
	}
	require.Equal(t, []deyerr.Code{deyerr.P011, deyerr.P011}, codes)
}

func TestSuggestNeverExtraPort(t *testing.T) {
	// The front port is skipped like the control port, in the Cloudflare
	// list and in the random draw.
	require.Equal(t, []int{443, 2083, 2087}, Suggest(3, nil, 44433, 2053))

	got := Suggest(6, nil, 44433, 443, 8443)
	require.Equal(t, []int{2053, 2083, 2087, 2096}, got[:4])
	require.NotContains(t, got, 443)
	require.NotContains(t, got, 8443)

	got, err := SuggestFree(40, ProtoTCP, nil, 44433, 2053, 5000)
	require.NoError(t, err)
	require.Len(t, got, 40)
	require.NotContains(t, got, 2053)
	require.NotContains(t, got, 5000)

	// Even when almost everything is busy the exhaustive scan never offers it.
	front := 12345
	busy := func(p int, _ string) bool { return p != front && p != 4000 }
	got, err = SuggestFree(2, ProtoTCP, busy, 44433, front)
	require.Equal(t, []int{4000}, got)
	require.Error(t, err)
	require.Equal(t, deyerr.P018, deyerr.As(err).Code)

	// Without extra ports the signature behaves as before.
	require.Equal(t, []int{443, 2053, 2083}, Suggest(3, nil, 44433))
}

func TestCloudflarePortsFromConfig(t *testing.T) {
	require.Equal(t, config.CloudflareHTTPSPorts(), CloudflarePorts)
}
