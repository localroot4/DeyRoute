package render

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// fakeBackend is a configurable backend used to exercise the planner end
// to end. Render is pure and deterministic like the real backends.
type fakeBackend struct {
	name       string
	version    string
	builtin    bool
	transports []backend.Transport
	validate   func(in backend.RenderInput) error
	render     func(in backend.RenderInput, side backend.Side) (backend.Rendered, error)

	mu     sync.Mutex
	inputs []backend.RenderInput
}

func (f *fakeBackend) Name() string                    { return f.name }
func (f *fakeBackend) Transports() []backend.Transport { return f.transports }
func (f *fakeBackend) Manifest() backend.ManifestEntry { return f.manifest() }
func (f *fakeBackend) Probe(context.Context, backend.RenderInput) (backend.ProbeResult, error) {
	return backend.ProbeResult{}, backend.ErrNoProbe
}

func (f *fakeBackend) manifest() backend.ManifestEntry {
	return backend.ManifestEntry{Name: f.name, Version: f.version, Builtin: f.builtin, Binaries: []string{f.name + "-bin"}}
}

func (f *fakeBackend) Validate(in backend.RenderInput) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	if f.validate != nil {
		return f.validate(in)
	}
	if in.Secrets.Token == "" {
		return deyerr.New(deyerr.B006, deyerr.Params{"transport": in.Transport.ID(), "reason": "token missing"})
	}
	return nil
}

func (f *fakeBackend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	if f.render != nil {
		return f.render(in, side)
	}
	return defaultRender(in, side), nil
}

// lastInput returns the last RenderInput seen for (node, transport).
func (f *fakeBackend) lastInput(node, tr string) (backend.RenderInput, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.inputs) - 1; i >= 0; i-- {
		in := f.inputs[i]
		if in.Node.ID == node && in.Transport.Name == tr {
			return in, true
		}
	}
	return backend.RenderInput{}, false
}

// keyedBackend adds KeyGenerator.
type keyedBackend struct{ *fakeBackend }

func (k keyedBackend) GenerateKeys(t backend.Transport) (map[string]string, error) {
	return map[string]string{"private": "priv-" + t.Backend + "-0123456789abcdef", "public": "pub-" + t.Backend + "-0123456789abcdef"}, nil
}

// defaultRender models a reverse/forward backend: the server side (hub
// for reverse, node for forward) binds the control port and uses the TLS
// key; the client side pins the CA or the cert hash.
func defaultRender(in backend.RenderInput, side backend.Side) backend.Rendered {
	server := in.Transport.Direction.ServerSide() == side
	var b strings.Builder
	fmt.Fprintf(&b, "side = %q\ntoken = %q\ncontrol = %d\n", side, in.Secrets.Token, in.ControlPort)
	if in.Transport.NeedsTLS {
		if server {
			fmt.Fprintf(&b, "cert = %q\nkey = %q\n", in.Secrets.TLSCertFile, in.Secrets.TLSKeyFile)
			if in.Transport.Name == "p12" {
				fmt.Fprintf(&b, "pkcs12 = %q\npassword = %q\n", in.Secrets.TLSP12File, in.Secrets.TLSP12Password)
			}
		} else {
			fmt.Fprintf(&b, "ca = %q\nserver_name = %q\npin = %q\n", in.Secrets.CAFile, in.Secrets.ServerName, in.Secrets.TLSCertSHA256)
		}
	}
	for _, k := range backend.SortedKeys(in.Secrets.Keys) {
		fmt.Fprintf(&b, "key.%s = %q\n", k, in.Secrets.Keys[k])
	}
	var binds []backend.PortUse
	if side == backend.SideHub {
		for _, p := range in.Tunnel.Ports {
			fmt.Fprintf(&b, "port = \"%s:%d/%s -> %s\"\n", in.ListenAddrOrDefault(), p.Listen, p.Proto, p.Target)
			binds = append(binds, backend.PortUse{Port: p.Listen, Proto: p.Proto, Addr: in.ListenAddrOrDefault(), Purpose: "user"})
		}
	}
	if server {
		binds = append(binds, backend.PortUse{Port: in.ControlPort, Proto: "tcp", Addr: "0.0.0.0", Purpose: "control"})
	}
	fmt.Fprintf(&b, "decoy = %q\nnet = %d\nfirst_run = %t\ncanary = %t\n", in.Decoy, in.NetIndex, in.FirstRun, in.Canary)
	cfg := filepath.Join(in.Paths.ConfigDir, "config.toml")
	return backend.Rendered{
		Files: map[string][]byte{"config.toml": []byte(b.String())},
		Unit: backend.UnitSpec{
			ExecStart: []string{in.Paths.Binary, "-c", cfg},
		},
		Binds: binds,
	}
}

// testRegistry is a local Registry so the test backends never touch the
// global registry.
type testRegistry map[string]backend.Backend

func (r testRegistry) Lookup(id string) (backend.Backend, backend.Transport, error) {
	name, tr, _ := strings.Cut(id, "/")
	if b, ok := r[name]; ok {
		for _, t := range b.Transports() {
			if t.Name == tr {
				return b, t, nil
			}
		}
	}
	return nil, backend.Transport{}, deyerr.New(deyerr.C005, deyerr.Params{"transport": id, "valid": ""})
}

func (r testRegistry) Supports(id, proto string) bool {
	_, t, err := r.Lookup(id)
	return err == nil && t.Supports(proto)
}

func tr(b, name string, dir backend.Direction, protos ...string) backend.Transport {
	return backend.Transport{Backend: b, Name: name, Direction: dir, Protos: protos, Stealth: 3}
}

// fakes builds the test backends: a reverse one (rev: tls, plain, p12 —
// with keys), a forward one (fwd/quic: UDP + TLS), a UDP-only one, one
// whose Validate fails, one NAT-based (nat/wg), one whose node render
// fails, and a builtin one.
func fakes() (testRegistry, map[string]*fakeBackend) {
	rev := &fakeBackend{name: "rev", version: "v1.2.3", transports: []backend.Transport{
		func() backend.Transport {
			t := tr("rev", "tls", backend.Reverse, "tcp", "udp")
			t.NeedsTLS = true
			return t
		}(),
		tr("rev", "plain", backend.Reverse, "tcp", "udp"),
		func() backend.Transport {
			t := tr("rev", "p12", backend.Reverse, "tcp")
			t.NeedsTLS = true
			return t
		}(),
	}}
	fwd := &fakeBackend{name: "fwd", version: "app/v2.0.0", transports: []backend.Transport{func() backend.Transport {
		t := tr("fwd", "quic", backend.Forward, "tcp", "udp")
		t.NeedsUDP, t.NeedsTLS = true, true
		return t
	}()}}
	udp := &fakeBackend{name: "udponly", version: "v1", transports: []backend.Transport{tr("udponly", "udp", backend.Reverse, "udp")}}
	bad := &fakeBackend{name: "bad", version: "v1", transports: []backend.Transport{tr("bad", "x", backend.Reverse, "tcp", "udp")},
		validate: func(in backend.RenderInput) error {
			return deyerr.New(deyerr.B006, deyerr.Params{"transport": in.Transport.ID(), "reason": "impossible combination"})
		}}
	plainBad := &fakeBackend{name: "plainbad", version: "v1", transports: []backend.Transport{tr("plainbad", "x", backend.Reverse, "tcp", "udp")},
		validate: func(backend.RenderInput) error { return fmt.Errorf("plain failure") }}
	nat := &fakeBackend{name: "nat", version: "v0.1", transports: []backend.Transport{func() backend.Transport {
		t := tr("nat", "wg", backend.Forward, "tcp", "udp")
		t.NeedsUDP = true
		return t
	}()},
		validate: func(in backend.RenderInput) error {
			if in.NetIndex < 0 {
				return deyerr.New(deyerr.P030, deyerr.Params{"tunnel": in.Tunnel.ID, "max": 255})
			}
			return nil
		},
		render: func(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
			r := defaultRender(in, side)
			if side == backend.SideHub {
				peer := fmt.Sprintf("10.77.%d.2", in.NetIndex)
				for _, p := range in.Tunnel.Ports {
					r.NAT = append(r.NAT, backend.NATRule{Proto: p.Proto, DportLow: p.Listen, DportHigh: p.Listen, ToAddr: peer, ToPort: p.Listen})
				}
				r.Masquerade = []string{"dey-" + in.Tunnel.ID}
				r.IPForward = true
				r.Unit.RunAsRoot = true
				r.Unit.ExtraCaps = []string{"CAP_NET_ADMIN"}
			}
			return r, nil
		}}
	boom := &fakeBackend{name: "boom", version: "v1", transports: []backend.Transport{tr("boom", "y", backend.Reverse, "tcp", "udp")},
		render: func(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
			if side == backend.SideNode {
				return backend.Rendered{}, fmt.Errorf("template exploded")
			}
			return defaultRender(in, side), nil
		}}
	badUnit := &fakeBackend{name: "badunit", version: "v1", transports: []backend.Transport{tr("badunit", "z", backend.Reverse, "tcp", "udp")},
		render: func(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
			r := defaultRender(in, side)
			r.Unit.ExecStart = []string{"relative-binary"}
			return r, nil
		}}
	builtin := &fakeBackend{name: "native", version: "builtin", builtin: true, transports: []backend.Transport{tr("native", "relay", backend.Forward, "tcp", "udp")},
		render: func(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
			r := defaultRender(in, side)
			r.Unit.ExecStart = []string{in.Paths.SelfBinary, "relay", "--tunnel", in.Tunnel.ID}
			return r, nil
		}}
	all := map[string]*fakeBackend{}
	reg := testRegistry{}
	for _, b := range []*fakeBackend{rev, fwd, udp, bad, plainBad, nat, boom, badUnit, builtin} {
		all[b.name] = b
		reg[b.name] = b
	}
	reg["rev"] = keyedBackend{rev}
	return reg, all
}

// env is a complete planner environment in t.TempDir().
type env struct {
	cfg   *config.Config
	store *state.Store
	sec   *secrets.Store
	ca    *tlsutil.CA
	reg   testRegistry
	fakes map[string]*fakeBackend
	first map[string]bool
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	ca, err := tlsutil.NewCA("DEYROUTE CA test", t0)
	require.NoError(t, err)
	ca.Now = func() time.Time { return t0 }
	reg, all := fakes()

	cfg := config.NewHub("ir-1", "5.6.7.8", 44433)
	cfg.Nodes = []config.Node{
		{ID: "de-1", Name: "Germany 1", PublicIP: "1.2.3.4"},
		{ID: "nl-1", Name: "Netherlands 1", PublicIP: "2001:db8::9"},
	}
	tun := config.NewTunnel("main", "Main", []string{"de-1", "nl-1"}, []config.PortMap{{Listen: 443}, {Listen: 2053}})
	tun.Ladder = config.LadderRef{Inline: []string{"rev/tls", "rev/plain", "fwd/quic", "udponly/udp", "bad/x", "nat/wg", "boom/y", "native/relay"}}
	cfg.Tunnels = []config.Tunnel{tun}
	return &env{
		cfg:   cfg,
		store: st,
		sec:   &secrets.Store{Root: filepath.Join(dir, "root"), CA: ca, Now: func() time.Time { return t0 }},
		ca:    ca,
		reg:   reg,
		fakes: all,
		first: map[string]bool{"main.de-1.rev-tls": true},
	}
}

func (e *env) input() Input {
	nodes := map[string]config.Node{}
	for _, n := range e.cfg.Nodes {
		nodes[n.ID] = n
	}
	return Input{
		Cfg:    e.cfg,
		Tunnel: e.cfg.Tunnels[0],
		Hub:    e.cfg.Hub.Info(),
		Nodes:  nodes,
		CtlPort: func(key string) (int, error) {
			return e.store.AllocCtlPort(key, config.CtlRangeLow, config.CtlRangeHigh, nil)
		},
		NetIndex: func(tunnel string) (int, error) { return e.store.AllocNetIndex(tunnel, 255) },
		Secrets:  e.sec,
		Registry: e.reg,
		Layout:   install.Layout{},
		Decoy:    "www.example.org",
		FirstRun: func(instance string) bool { return e.first[instance] },
		CAPEM:    e.ca.CertPEM,
	}
}

func candidateIDs(p TunnelPlan) []string {
	var out []string
	for _, c := range p.Candidates {
		out = append(out, c.Node+"|"+c.TransportID)
	}
	return out
}

func skippedCodes(p TunnelPlan) map[string]deyerr.Code {
	out := map[string]deyerr.Code{}
	for _, s := range p.Skipped {
		out[s.Node+"|"+s.TransportID] = s.Code
	}
	return out
}

func sortedFileNames(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
