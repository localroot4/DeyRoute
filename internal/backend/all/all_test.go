package all

import (
	"testing"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
)

// Every rung of the built-in ladders must resolve to a registered transport,
// and every registered backend must have a manifest entry.
func TestBuiltinLaddersResolve(t *testing.T) {
	for name, rungs := range config.BuiltinLadders() {
		for _, id := range rungs {
			if !backend.KnownTransport(id) {
				t.Errorf("ladder %s: unknown transport %s", name, id)
			}
		}
	}
	for _, b := range backend.All() {
		if b.Manifest().Version == "" {
			t.Errorf("backend %s has no manifest entry", b.Name())
		}
	}
	if len(backend.AllTransports()) < 20 {
		t.Errorf("only %d transports registered", len(backend.AllTransports()))
	}
}
