package backend

import (
	"testing"

	"github.com/localroot4/deyroute/internal/config"
)

func TestDecoyFor(t *testing.T) {
	in := RenderInput{Hub: config.HubInfo{DecoySNIs: []string{" ", "a.example", "b.example"}}}
	if got := DecoyFor(in); got != "a.example" {
		t.Fatalf("first hub decoy: %q", got)
	}
	in.Decoy = " sel.example "
	if got := DecoyFor(in); got != "sel.example" {
		t.Fatalf("selected decoy wins: %q", got)
	}
	// Nothing configured: the built-in (placeholder) list applies.
	if got := DecoyFor(RenderInput{}); got != DefaultDecoySNIs[0] {
		t.Fatalf("default decoy: %q", got)
	}
	// Only blank hub entries: still the defaults.
	if got := DecoyFor(RenderInput{Hub: config.HubInfo{DecoySNIs: []string{" "}}}); got != DefaultDecoySNIs[0] {
		t.Fatalf("blank hub list: %q", got)
	}
	saved := DefaultDecoySNIs
	DefaultDecoySNIs = nil
	defer func() { DefaultDecoySNIs = saved }()
	if got := DecoyFor(RenderInput{}); got != "" {
		t.Fatalf("no decoy anywhere must be empty: %q", got)
	}
}

func TestDecoyCandidates(t *testing.T) {
	if len(DefaultDecoySNIs) != 3 {
		t.Fatalf("spec: 3 default decoys, got %d", len(DefaultDecoySNIs))
	}
	got := DecoyCandidates(nil)
	if len(got) != 3 || got[0] != DefaultDecoySNIs[0] {
		t.Fatalf("defaults: %v", got)
	}
	got = DecoyCandidates([]string{"x.example", "", "x.example", " y.example"})
	if len(got) != 2 || got[0] != "x.example" || got[1] != "y.example" {
		t.Fatalf("configured: %v", got)
	}
}
