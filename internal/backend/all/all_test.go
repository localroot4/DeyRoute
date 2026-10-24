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

// Optional transports (backend.Transport.Optional) are in no built-in
// ladder, and wireguard/kernel is one of them since it left udp-default
// (field report of 2026-10-17); a ladder of the owner's may still use it.
func TestBuiltinLaddersHaveNoOptionalRung(t *testing.T) {
	for name, rungs := range config.BuiltinLadders() {
		for _, id := range rungs {
			if _, tr, err := backend.Lookup(id); err == nil && tr.Optional {
				t.Errorf("ladder %s: optional transport %s", name, id)
			}
		}
	}
	if _, tr, err := backend.Lookup("wireguard/kernel"); err != nil || !tr.Optional {
		t.Errorf("wireguard/kernel must be registered and optional: %+v %v", tr, err)
	}
	c := &config.Config{Ladders: map[string][]string{
		"udp-wg": {"backhaul/udp", "hysteria2/udp", "wireguard/kernel", "direct/native"},
	}}
	tun := &config.Tunnel{ID: "games", Ladder: config.LadderRef{Name: "udp-wg"},
		Ports: []config.PortMap{{Listen: 27015, Proto: config.ProtoUDP}}}
	got, err := c.ResolveLadder(tun, func(id, proto string) bool {
		_, tr, err := backend.Lookup(id)
		return err == nil && tr.Supports(proto)
	})
	if err != nil || len(got) != 4 || got[2] != "wireguard/kernel" {
		t.Errorf("custom UDP ladder with wireguard/kernel: %v %v", got, err)
	}
}
