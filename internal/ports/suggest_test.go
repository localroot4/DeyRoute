package ports

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestReserved(t *testing.T) {
	tests := []struct {
		port, ctl int
		reserved  bool
		reason    string
	}{
		{22, 44433, true, "port 22 is reserved for SSH"},
		{44433, 44433, true, "port 44433 is the hub control port (hub.control_port)"},
		{30000, 44433, true, "ports 30000-31999 are reserved for backend control ports"},
		{31999, 44433, true, "ports 30000-31999 are reserved for backend control ports"},
		{29999, 44433, false, ""},
		{32000, 44433, false, ""},
		{443, 44433, false, ""},
		{443, 0, false, ""},
		{0, 0, false, ""},
	}
	for _, tt := range tests {
		r, why := Reserved(tt.port, tt.ctl)
		require.Equal(t, tt.reserved, r, tt.port)
		require.Equal(t, tt.reason, why, tt.port)
	}
	// DEY-P011 and DEY-C011 must never disagree.
	for _, ctl := range []int{0, 443, 44433} {
		for p := 0; p <= 65536; p++ {
			r1, w1 := Reserved(p, ctl)
			r2, w2 := config.ReservedListen(p, ctl, nil)
			if r1 != r2 || w1 != w2 {
				t.Fatalf("port %d ctl %d: ports says (%v, %q), config says (%v, %q)", p, ctl, r1, w1, r2, w2)
			}
		}
	}
}

// TestSuggestHugeCount: the count comes from the CLI/Local API; an absurd
// value must neither allocate it nor loop proportionally to it.
func TestSuggestHugeCount(t *testing.T) {
	start := time.Now()
	got, err := SuggestFree(1<<40, ProtoTCP, nil, 44433)
	require.True(t, deyerr.HasCode(err, deyerr.P018))
	// Every candidate is found: 443 plus 1024-65535 without 30000-31999 and
	// the control port.
	require.Len(t, got, 1+(RandomHigh-RandomLow+1)-(CtlRangeHigh-CtlRangeLow+1)-1)
	checkSuggestion(t, got, 44433, nil)
	require.Less(t, time.Since(start), 10*time.Second)
}

func TestCheckReserved(t *testing.T) {
	require.NoError(t, CheckReserved([]Spec{tcp(443), udp(2053)}, 44433))
	err := CheckReserved([]Spec{tcp(443), tcp(22), udp(30001)}, 44433)
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, deyerr.P011))
	require.Contains(t, err.Error(), "22/tcp")
	require.Contains(t, err.Error(), "30001/udp")
}

func checkSuggestion(t *testing.T, got []int, ctl int, busy func(int, string) bool) {
	t.Helper()
	seen := map[int]bool{}
	for _, p := range got {
		require.False(t, seen[p], "duplicate %d", p)
		seen[p] = true
		require.True(t, ValidPort(p))
		r, _ := Reserved(p, ctl)
		require.False(t, r, "reserved %d", p)
		if busy != nil {
			require.False(t, busy(p, ProtoTCP), "busy %d", p)
		}
		isCF := false
		for _, c := range CloudflarePorts {
			isCF = isCF || c == p
		}
		if !isCF {
			require.GreaterOrEqual(t, p, RandomLow)
		}
	}
}

func TestSuggestCloudflareFirst(t *testing.T) {
	got := Suggest(3, nil, 44433)
	require.Equal(t, []int{443, 2053, 2083}, got)

	got = Suggest(6, nil, 44433)
	require.Equal(t, CloudflarePorts, got)

	busy := func(p int, proto string) bool { return p == 443 || (p == 2083 && proto == ProtoUDP) }
	got = Suggest(3, busy, 44433)
	require.Equal(t, []int{2053, 2087, 2096}, got)

	// A single protocol only needs that protocol to be free.
	got, err := SuggestFree(2, ProtoTCP, busy, 44433)
	require.NoError(t, err)
	require.Equal(t, []int{2053, 2083}, got)

	// The control port is skipped even if it is a Cloudflare port.
	got = Suggest(2, nil, 2053)
	require.Equal(t, []int{443, 2083}, got)
}

func TestSuggestRandom(t *testing.T) {
	busy := func(p int, _ string) bool { return p%2 == 0 }
	got := Suggest(20, busy, 44433)
	require.Len(t, got, 20)
	checkSuggestion(t, got, 44433, busy)
	// The odd Cloudflare ports come first (2096 is busy), then random ones.
	require.Equal(t, []int{443, 2053, 2083, 2087, 8443}, got[:5])
	for _, p := range got {
		require.Equal(t, 1, p%2)
	}

	got = Suggest(10, nil, 44433)
	require.Len(t, got, 10)
	require.Equal(t, CloudflarePorts, got[:6])
	checkSuggestion(t, got, 44433, nil)

	require.Nil(t, Suggest(0, nil, 44433))
	require.Nil(t, Suggest(-1, nil, 44433))
}

func TestSuggestDeterministicWithSeed(t *testing.T) {
	seed := [32]byte{1, 2, 3}
	a, err := suggest(12, "", nil, 44433, rand.New(rand.NewChaCha8(seed)))
	require.NoError(t, err)
	b, err := suggest(12, "", nil, 44433, rand.New(rand.NewChaCha8(seed)))
	require.NoError(t, err)
	require.Equal(t, a, b)
	checkSuggestion(t, a, 44433, nil)
}

func TestSuggestNearlyExhausted(t *testing.T) {
	// Only three ports in the whole space are free: the random phase will
	// almost surely miss them; the exhaustive scan must find them.
	free := map[int]bool{1500: true, 40000: true, 65535: true}
	calls := map[int]int{}
	busy := func(p int, _ string) bool { calls[p]++; return !free[p] }
	got, err := SuggestFree(3, ProtoTCP, busy, 44433)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{1500, 40000, 65535}, got)
	for p, n := range calls {
		require.Equal(t, 1, n, "busy called twice for %d", p)
	}

	got, err = SuggestFree(4, ProtoTCP, busy, 44433)
	require.Len(t, got, 3)
	require.True(t, deyerr.HasCode(err, deyerr.P018))
	require.Len(t, Suggest(4, busy, 44433), 3)
}

func TestSuggestNothingFree(t *testing.T) {
	got, err := SuggestFree(1, "", func(int, string) bool { return true }, 44433)
	require.Empty(t, got)
	require.True(t, deyerr.HasCode(err, deyerr.P018))
}
