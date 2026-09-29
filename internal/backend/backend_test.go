package backend

import (
	"context"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

type fakeBackend struct{ name string }

func (f fakeBackend) Name() string { return f.name }
func (f fakeBackend) Transports() []Transport {
	return []Transport{{Backend: f.name, Name: "tcp", Protos: []string{"tcp"}}}
}
func (f fakeBackend) Manifest() ManifestEntry       { return ManifestFor(f.name) }
func (f fakeBackend) Validate(in RenderInput) error { return nil }
func (f fakeBackend) Render(in RenderInput, side Side) (Rendered, error) {
	return Rendered{}, nil
}
func (f fakeBackend) Probe(ctx context.Context, in RenderInput) (ProbeResult, error) {
	return ProbeResult{}, ErrNoProbe
}

func TestRegistryLookup(t *testing.T) {
	Register(fakeBackend{"zz-test"})
	b, tr, err := Lookup("zz-test/tcp")
	if err != nil || b.Name() != "zz-test" || tr.ID() != "zz-test/tcp" {
		t.Fatalf("lookup: %v %v %v", b, tr, err)
	}
	_, _, err = Lookup("nope/x")
	if !deyerr.HasCode(err, deyerr.C005) {
		t.Fatalf("want C005, got %v", err)
	}
	if !KnownTransport("zz-test/tcp") || KnownTransport("zz-test/udp") {
		t.Fatal("KnownTransport wrong")
	}
}

func TestEmbeddedManifestParses(t *testing.T) {
	m, err := ParseManifest(EmbeddedManifest())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backhaul", "rathole", "frp", "waterwall", "xray", "hysteria2", "wireguard", "direct", "gost", "chisel"} {
		e, ok := m.Backends[name]
		if !ok || e.Version == "" {
			t.Errorf("manifest missing %s", name)
		}
		if !e.Builtin && (e.URLs["amd64"] == "" || e.URLs["arm64"] == "") {
			t.Errorf("%s: urls for amd64 and arm64 required", name)
		}
	}
	if ManifestFor("backhaul").Version == "" {
		t.Fatal("ManifestFor")
	}
}

func TestHelpers(t *testing.T) {
	if got := TOMLString("a\"b\\c\n"); got != `"a\"b\\c\n"` {
		t.Fatalf("TOMLString: %s", got)
	}
	if got := HostPort("::1", 443); got != "[::1]:443" {
		t.Fatal(got)
	}
	h, p, ok := SplitTarget("127.0.0.1:443")
	if !ok || h != "127.0.0.1" || p != 443 {
		t.Fatal("SplitTarget")
	}
	if _, _, ok := SplitTarget("x:0"); ok {
		t.Fatal("port 0 must be invalid")
	}
	if Forward.ServerSide() != SideNode || Reverse.ServerSide() != SideHub {
		t.Fatal("server side")
	}
}
