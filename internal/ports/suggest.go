package ports

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	"math/rand/v2"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// SSHPort is never used for a tunnel.
const SSHPort = 22

// Backend control port range (section 10); never offered to the owner.
const (
	CtlRangeLow  = config.CtlRangeLow
	CtlRangeHigh = config.CtlRangeHigh
)

// Random suggestions are drawn from this range (section 10).
const (
	RandomLow  = 1024
	RandomHigh = 65535
)

// CloudflarePorts are the HTTPS ports Cloudflare proxies; they are
// suggested first, in this order (section 10). The list is owned by
// internal/config (config.CloudflareHTTPSPorts).
var CloudflarePorts = config.CloudflareHTTPSPorts()

// Reserved reports whether a tunnel may not listen on port, and why: 22
// (SSH), the hub control port and the backend control range 30000-31999
// (section 10). extra lists more reserved ports, for example the hub front
// port (config.Hub.FrontPort). It is the rule config validation applies
// (DEY-C011), so the owner sees the same reason in DEY-P011 and DEY-C011.
func Reserved(port, controlPort int, extra ...int) (bool, string) {
	return config.ReservedListen(port, controlPort, extra)
}

// CheckReserved returns DEY-P011 for every spec on a reserved port (joined),
// or nil. extra is as for Reserved.
func CheckReserved(specs []Spec, controlPort int, extra ...int) error {
	var errs []error
	for _, s := range specs {
		if r, why := Reserved(s.Listen, controlPort, extra...); r {
			errs = append(errs, deyerr.New(deyerr.P011, deyerr.Params{"port": FormatSpec(s), "reason": why}))
		}
	}
	return deyerr.Join(errs...)
}

// Suggest returns up to n free ports for a new tunnel: first the
// Cloudflare-compatible ports (443, 2053, 2083, 2087, 2096, 8443) that are
// free and not reserved, then pseudo-random ports in 1024-65535, never 22,
// controlPort, 30000-31999 or a port for which busy reports true (checked for
// both tcp and udp, so the port suits any tunnel protocol). busy may be nil.
// extra lists more ports that are never suggested (the hub front port).
// Fewer than n ports are returned only when no more exist; use SuggestFree
// to get DEY-P018 in that case.
func Suggest(n int, busy func(port int, proto string) bool, controlPort int, extra ...int) []int {
	out, _ := SuggestFree(n, "", busy, controlPort, extra...)
	return out
}

// SuggestFree is Suggest for one protocol ("tcp" or "udp"; "" means both
// must be free). It returns exactly n ports, or the ports found plus
// DEY-P018 when fewer than n are available.
func SuggestFree(n int, proto string, busy func(port int, proto string) bool, controlPort int, extra ...int) ([]int, error) {
	return suggest(n, proto, busy, controlPort, newRand(), extra...)
}

func suggest(n int, proto string, busy func(int, string) bool, controlPort int, rng *rand.Rand, extra ...int) ([]int, error) {
	if n <= 0 {
		return nil, nil
	}
	span := RandomHigh - RandomLow + 1
	// No request can be served beyond the candidate space; bounding the work
	// by it keeps an absurd count (it comes from the CLI/Local API) from
	// allocating or looping without limit. P018 still reports the shortfall.
	want := min(n, span+len(CloudflarePorts))
	checked := make(map[int]bool) // port → usable
	usable := func(p int) bool {
		if ok, seen := checked[p]; seen {
			return ok
		}
		ok := isUsable(p, proto, busy, controlPort, extra)
		checked[p] = ok
		return ok
	}
	out := make([]int, 0, min(want, 1024))
	taken := make(map[int]bool, min(want, 1024))
	add := func(p int) {
		if !taken[p] && usable(p) {
			taken[p] = true
			out = append(out, p)
		}
	}
	for _, p := range CloudflarePorts {
		if len(out) == n {
			return out, nil
		}
		add(p)
	}
	for attempts := 0; len(out) < want && attempts < min(16*want+256, 2*span); attempts++ {
		add(RandomLow + rng.IntN(span))
	}
	// Almost everything is busy: scan the whole range once from a random
	// offset so "impossible" is exact rather than unlucky.
	if len(out) < n {
		start := rng.IntN(span)
		for i := 0; i < span && len(out) < n; i++ {
			add(RandomLow + (start+i)%span)
		}
	}
	if len(out) < n {
		return out, deyerr.New(deyerr.P018, nil)
	}
	return out, nil
}

func isUsable(p int, proto string, busy func(int, string) bool, controlPort int, extra []int) bool {
	if !ValidPort(p) {
		return false
	}
	if r, _ := Reserved(p, controlPort, extra...); r {
		return false
	}
	if busy == nil {
		return true
	}
	if proto == "" {
		return !busy(p, ProtoTCP) && !busy(p, ProtoUDP)
	}
	return !busy(p, proto)
}

// newRand returns a ChaCha8 generator seeded from crypto/rand. The ports it
// yields are not secrets; the strong seed only avoids every hub suggesting
// the same sequence.
func newRand() *rand.Rand {
	var seed [32]byte
	if _, err := cryptorand.Read(seed[:]); err != nil {
		binary.LittleEndian.PutUint64(seed[:], uint64(time.Now().UnixNano())) //nolint:gosec // G115: bit pattern only
	}
	return rand.New(rand.NewChaCha8(seed)) //nolint:gosec // G404: ChaCha8 seeded from crypto/rand
}
