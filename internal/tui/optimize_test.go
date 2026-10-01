package tui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
)

// TestPickProfileFollowsHubRAM: the picker names the profile recommended
// for the hub's RAM, and aggressive on a hub below 4 GB is confirmed with a
// warning (section 12).
func TestPickProfileFollowsHubRAM(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	mem := uint64(1000000 * 1024)
	stub.OptimizeStatusFn = func(context.Context) (api.OptimizeStatus, error) {
		rec := "balanced"
		if mem >= 4_000_000_000 {
			rec = "aggressive"
		}
		return api.OptimizeStatus{Profile: "off", MemBytes: mem, Recommended: rec}, nil
	}
	stub.OptimizeApplyFn = func(_ context.Context, p string) (api.OptimizeStatus, error) {
		log.add("apply " + p)
		return api.OptimizeStatus{Profile: p}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})

	h.choose("7").choose("1")
	h.must("This hub has 976 MB RAM: balanced is recommended", "aggressive - 64MB buffers for fast links, for servers with 4 GB RAM or more")
	h.choose("2")
	h.must("This hub has only 976 MB RAM. aggressive is meant for servers with 4 GB or more", "The aggressive sysctl profile is written")
	h.press("enter")
	require.True(t, log.has("apply aggressive"))
	h.press("esc")

	// balanced needs no warning.
	h.choose("1").choose("1")
	h.mustNot("This hub has only")
	h.must("The balanced sysctl profile is written")
	h.press("esc")

	// An 8 GB hub: aggressive is recommended and confirmed without a warning.
	mem = 8 << 30
	h.choose("1")
	h.must("This hub has 8192 MB RAM: aggressive is recommended")
	h.choose("2")
	h.mustNot("This hub has only")
	h.must("The aggressive sysctl profile is written")
}
