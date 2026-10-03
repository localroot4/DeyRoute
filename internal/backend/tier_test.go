package backend

import (
	"testing"

	"github.com/localroot4/deyroute/internal/config"
)

func TestTier(t *testing.T) {
	in := RenderInput{HubTier: config.BackendTierLarge, NodeTier: config.BackendTierSmall}
	if got := in.Tier(SideHub); got != config.BackendTierLarge {
		t.Fatalf("hub tier %q", got)
	}
	if got := in.Tier(SideNode); got != config.BackendTierSmall {
		t.Fatalf("node tier %q", got)
	}
	in.NodeTier = "huge"
	if got := in.Tier(SideNode); got != "" {
		t.Fatalf("unknown tier must read as the defaults, got %q", got)
	}

	for tier, want := range map[string]int{
		"": 0, config.BackendTierSmall: 1, config.BackendTierMedium: 2, config.BackendTierLarge: 3, "huge": 0,
	} {
		if got := ByTier(tier, 0, 1, 2, 3); got != want {
			t.Fatalf("ByTier(%q) = %d, want %d", tier, got, want)
		}
	}

	s, m, l := config.BackendTierSmall, config.BackendTierMedium, config.BackendTierLarge
	for _, c := range []struct{ a, b, want string }{
		{"", "", ""},
		{"", l, l},
		{m, "", m},
		{s, l, s},
		{l, m, m},
		{l, l, l},
		{"huge", s, s},
		{s, "huge", s},
		{"huge", "", ""},
	} {
		if got := SmallerTier(c.a, c.b); got != c.want {
			t.Fatalf("SmallerTier(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}
