package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/systemd"
)

// trUpd is the transport of the updatable test backend.
const trUpd = "tfu/one"

// tfu is a downloaded test backend whose manifest version can change (a
// new manifest).
var tfu = &updBackend{testBackend: testBackend{name: "tfu", transports: []backend.Transport{
	{Backend: "tfu", Name: "one", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
}}, ver: "1.0"}

func init() { backend.Register(tfu) }

// updBackend is a testBackend with a mutable manifest version.
type updBackend struct {
	testBackend
	mu  sync.Mutex
	ver string
}

// tfuBinary is the release of version v.
func tfuBinary(v string) []byte { return []byte("#!tfu test backend " + v + "\n") }

func (b *updBackend) setVersion(v string) {
	b.mu.Lock()
	b.ver = v
	b.mu.Unlock()
}

// Manifest implements backend.Backend.
func (b *updBackend) Manifest() backend.ManifestEntry {
	b.mu.Lock()
	v := b.ver
	b.mu.Unlock()
	sum := sha256.Sum256(tfuBinary(v))
	return backend.ManifestEntry{
		Name: "tfu", Version: v, TemplateVersion: 1, Archive: "raw", Binaries: []string{"tfu"},
		URLs:   map[string]string{"amd64": "https://example.invalid/tfu-" + v},
		SHA256: map[string]string{"amd64": hex.EncodeToString(sum[:])},
	}
}

// tfuFetcher serves every tfu release but 4.0.
type tfuFetcher struct{}

// Fetch implements install.Fetcher.
func (tfuFetcher) Fetch(_ context.Context, url string, w io.Writer) error {
	v, ok := strings.CutPrefix(url, "https://example.invalid/tfu-")
	if !ok || v == "4.0" {
		return install.Permanent(deyerr.New(deyerr.I004, deyerr.Params{"file": url}))
	}
	_, err := w.Write(tfuBinary(v))
	return err
}

// dropIn reads the drop-in of a hub instance.
func (te *tunnelEnv) dropIn(inst string) string {
	data, err := os.ReadFile(te.h.o.Systemd.DropInPath(inst))
	if err != nil {
		return ""
	}
	return string(data)
}

func TestUpdateBackends(t *testing.T) {
	tfu.setVersion("1.0")
	t.Cleanup(func() { tfu.setVersion("1.0") })
	te := startTunnelHub(t, func(o *Options, _ string) {
		o.Fetcher = tfuFetcher{}
		o.BackendProbeWait = 3 * time.Second
	})
	n := te.tunnelNode("de-1")
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trUpd, Failover: &api.FailoverSettings{ProbeIntervalS: 1, ProbeTimeoutS: 1, FailThreshold: 5,
			RecoverThreshold: 2, FailbackAfterS: 3600, MaxSwitchesPerHour: 20, QuarantineS: 600}})
	inst := systemd.InstanceName("main", "de-1", trUpd)
	unit := systemd.UnitName(inst)
	pin, ok := te.h.pinned("tfu")
	require.True(t, ok)
	require.Equal(t, "1.0", pin.Version)
	require.Contains(t, te.dropIn(inst), "/tfu/1.0/tfu")

	// Nothing to do while the manifest has the version in use.
	res, err := te.client.UpdateBackends(ctx, "", nil)
	require.NoError(t, err)
	for _, r := range res {
		require.Equal(t, BackendUnchanged, r.Status, r.Backend)
	}
	_, err = te.client.UpdateBackends(ctx, "nope", nil)
	require.Equal(t, deyerr.B008, codeOf(err))

	// A new manifest does not move the running tunnel...
	tfu.setVersion("2.0")
	require.NoError(t, te.client.TunnelRestart(ctx, "main"))
	te.waitActive("main", "de-1", trUpd)
	require.Contains(t, te.dropIn(inst), "/tfu/1.0/tfu")
	tl, err := te.client.TransportList(ctx)
	require.NoError(t, err)
	for _, ti := range tl {
		if ti.ID == trUpd {
			require.Equal(t, "1.0", ti.Version)
		}
	}
	// ...update backends does: installed next to the old one, rendered,
	// the active transport restarted and probed.
	restarts := te.sd.count("restart", unit)
	var log stepLog
	res, err = te.client.UpdateBackends(ctx, "tfu", log.add)
	require.NoError(t, err)
	require.Equal(t, []api.BackendUpdate{{Backend: "tfu", From: "1.0", To: "2.0", Status: BackendUpdated}}, res)
	require.Contains(t, log.finished(), "backend:tfu:ok")
	require.FileExists(t, filepath.Join(te.root, config.BinDir, "tfu", "2.0", "tfu"))
	require.FileExists(t, filepath.Join(te.root, config.BinDir, "tfu", "1.0", "tfu"))
	require.Contains(t, te.dropIn(inst), "/tfu/2.0/tfu")
	require.Equal(t, restarts+1, te.sd.count("restart", unit))
	require.Contains(t, n.restartedList(), inst)
	pin, _ = te.h.pinned("tfu")
	require.Equal(t, "2.0", pin.Version)

	// 3.0 does not pass the probe: rolled back to 2.0 within the window.
	te.sd.mu.Lock()
	te.sd.brokenIf = func(i string) bool { return strings.Contains(te.dropIn(i), "/tfu/3.0/") }
	te.sd.mu.Unlock()
	tfu.setVersion("3.0")
	res, err = te.client.UpdateBackends(ctx, "tfu", nil)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, BackendRolledBack, res[0].Status)
	require.Equal(t, string(deyerr.S003), res[0].Error.Code)
	require.Contains(t, te.dropIn(inst), "/tfu/2.0/tfu")
	pin, _ = te.h.pinned("tfu")
	require.Equal(t, "2.0", pin.Version)
	ev := te.waitEvent(state.EvBackendRolledBack, "")
	require.Equal(t, "main", ev.Tunnel)
	te.waitActive("main", "de-1", trUpd)

	// 4.0 cannot be downloaded: nothing changes.
	tfu.setVersion("4.0")
	res, err = te.client.UpdateBackends(ctx, "tfu", nil)
	require.NoError(t, err)
	require.Equal(t, BackendFailed, res[0].Status)
	require.Contains(t, te.dropIn(inst), "/tfu/2.0/tfu")
	pin, _ = te.h.pinned("tfu")
	require.Equal(t, "2.0", pin.Version)
}

// The failover engine leaves a new version that does not pass its probe
// before the update window ends (fail_threshold probes < 60 s): the update
// is rolled back and the tunnel returns to the rung it had before the
// update, running the previous version again (S21).
func TestUpdateBackendsRollbackReturnsTunnel(t *testing.T) {
	tfu.setVersion("1.0")
	t.Cleanup(func() { tfu.setVersion("1.0") })
	te := startTunnelHub(t, func(o *Options, _ string) {
		o.Fetcher = tfuFetcher{}
		o.BackendProbeWait = 30 * time.Second
	})
	te.tunnelNode("de-1")
	ctx := ctxT(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "main", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		Rungs: []string{trUpd, "direct/native"}, Failover: &api.FailoverSettings{ProbeIntervalS: 1, ProbeTimeoutS: 1,
			FailThreshold: 2, RecoverThreshold: 2, FailbackAfterS: 3600, MaxSwitchesPerHour: 20, QuarantineS: 600}})
	te.waitActive("main", "de-1", trUpd)
	inst := systemd.InstanceName("main", "de-1", trUpd)

	te.sd.mu.Lock()
	te.sd.brokenIf = func(i string) bool { return strings.Contains(te.dropIn(i), "/tfu/2.0/") }
	te.sd.mu.Unlock()
	tfu.setVersion("2.0")
	start := time.Now()
	res, err := te.client.UpdateBackends(ctx, "tfu", nil)
	require.NoError(t, err)
	require.Len(t, res, 1)
	require.Equal(t, BackendRolledBack, res[0].Status)
	require.Less(t, time.Since(start), 30*time.Second, "the engine moved away before the window ended")
	require.Contains(t, te.dropIn(inst), "/tfu/1.0/tfu")
	st := te.waitActive("main", "de-1", trUpd)
	require.Equal(t, state.StateUp, st.State)
	evs, err := te.h.st.Events(state.EventFilter{Types: []string{state.EvSwitchTransport}, Tunnel: "main"})
	require.NoError(t, err)
	var back bool
	for _, ev := range evs {
		if ev.ToTransport == trUpd && strings.Contains(ev.Reason, "tfu 2.0 rolled back") {
			back = true
		}
	}
	require.True(t, back, "a switch_transport event back to %s names the rollback", trUpd)
	pin, _ := te.h.pinned("tfu")
	require.Equal(t, "1.0", pin.Version)
}

func TestPinnedBackendKeepsKeyGenerator(t *testing.T) {
	kb := &keyBackend{testBackend: testBackend{name: "x"}}
	pb := pinBackend(kb, backend.ManifestEntry{Name: "x", Version: "0.1"})
	require.Equal(t, "0.1", pb.Manifest().Version)
	kg, ok := pb.(backend.KeyGenerator)
	require.True(t, ok)
	keys, err := kg.GenerateKeys(backend.Transport{})
	require.NoError(t, err)
	require.Equal(t, "v", keys["k"])
	_, ok = pinBackend(&tfu.testBackend, backend.ManifestEntry{}).(backend.KeyGenerator)
	require.False(t, ok)
}

// keyBackend is a testBackend that generates keys.
type keyBackend struct{ testBackend }

// GenerateKeys implements backend.KeyGenerator.
func (keyBackend) GenerateKeys(backend.Transport) (map[string]string, error) {
	return map[string]string{"k": "v"}, nil
}
