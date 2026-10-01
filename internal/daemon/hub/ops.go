package hub

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Keys of the operations' records in state.db (meta bucket).
const (
	// metaBackendPins holds the backend versions in use (name → manifest
	// entry): `update manifest` changes the manifest, `update backends`
	// moves a backend to the manifest version (section 5).
	metaBackendPins = "backend-pins"
	// metaDecoy is the decoy SNI chosen by the last reachability check.
	metaDecoy = "decoy"
	// metaPendingCert + node is the fingerprint of a certificate the hub
	// signed for the node during rotate-ca and not yet in config.yaml.
	metaPendingCert = "cert-pending/"
	// metaNodeCert + node records the node certificate the hub signed
	// (fingerprint and expiry; security tls show).
	metaNodeCert = "node-cert/"
	// metaNodeUpdate is set by `deyroute update`: every node is told to
	// install the hub's binary until all run the hub's version.
	metaNodeUpdate = "update/nodes"
	// metaUpdatePrevious is the version an update replaced.
	metaUpdatePrevious = "update/previous"
	// metaUpdateCheck is the result of the last release check.
	metaUpdateCheck = "update/check"
)

// Queue sizes of the operation jobs.
const (
	nodeUpdateQueue = 64
)

// opsState is the state of the operations (ops_*.go, jobs.go).
type opsState struct {
	pinMu    sync.Mutex
	pins     map[string]backend.ManifestEntry // persisted in metaBackendPins
	pinTrial map[string]backend.ManifestEntry // update backends: version under test

	decoyMu   sync.Mutex
	decoy     string        // chosen decoy SNI ("" = not checked yet)
	decoyKick chan struct{} // a decoy check is wanted now (settings changed)

	updMu  sync.Mutex // serialises update, rollback, update backends/manifest
	certMu sync.Mutex // serialises TLS renewals

	restart chan struct{} // scheduled restart of deyroute-hub (update)
	nodeUpd chan string   // nodes that must install the hub's binary
}

// init prepares the zero state (New).
func (o *opsState) init() {
	o.pins = map[string]backend.ManifestEntry{}
	o.pinTrial = map[string]backend.ManifestEntry{}
	o.restart = make(chan struct{}, 1)
	o.nodeUpd = make(chan string, nodeUpdateQueue)
	o.decoyKick = make(chan struct{}, 1)
}

// loadOps reads the persisted operation records at start.
func (h *Hub) loadOps() {
	var pins map[string]backend.ManifestEntry
	if ok, err := h.st.GetMeta(metaBackendPins, &pins); err != nil {
		h.log.Warn("cannot read the backend versions in use", dlog.Err(err))
	} else if ok {
		for name, e := range pins {
			if e.Version != "" {
				h.ops.pins[name] = e
			}
		}
	}
	var decoy string
	if ok, err := h.st.GetMeta(metaDecoy, &decoy); err == nil && ok {
		h.ops.decoy = decoy
	}
}

// ---------------------------------------------------------------- backend versions

// backendEntry returns the manifest entry of backend name in use on this
// hub: the pinned version (section 5: a new manifest never changes a
// running tunnel; `update backends` moves it), the version under test
// during `update backends`, or the manifest entry for a backend that was
// never used (it is pinned from then on). Built-in and system backends are
// never pinned.
func (h *Hub) backendEntry(name string) (backend.ManifestEntry, bool) {
	e, ok := manifestOf(name)
	if !ok {
		return e, false
	}
	if e.Builtin || e.System || e.Version == "" {
		return e, true
	}
	return h.pinnedEntry(name, e), true
}

// pinnedEntry returns the entry of name in use; cur is the manifest entry.
func (h *Hub) pinnedEntry(name string, cur backend.ManifestEntry) backend.ManifestEntry {
	h.ops.pinMu.Lock()
	defer h.ops.pinMu.Unlock()
	if t, ok := h.ops.pinTrial[name]; ok {
		return t
	}
	p, ok := h.ops.pins[name]
	switch {
	case ok && p.Version != cur.Version:
		return p
	case ok && reflect.DeepEqual(p, cur):
		return p
	}
	// First use, or the same version with updated details (a signed
	// manifest that adds checksums): the manifest entry is the one in use.
	h.ops.pins[name] = cur
	h.savePinsLocked()
	return cur
}

// setPin records e as the version of name in use (update backends).
func (h *Hub) setPin(name string, e backend.ManifestEntry) {
	h.ops.pinMu.Lock()
	defer h.ops.pinMu.Unlock()
	h.ops.pins[name] = e
	h.savePinsLocked()
}

// setTrial makes e the version of name while it is tested (nil clears it).
func (h *Hub) setTrial(name string, e *backend.ManifestEntry) {
	h.ops.pinMu.Lock()
	defer h.ops.pinMu.Unlock()
	if e == nil {
		delete(h.ops.pinTrial, name)
		return
	}
	h.ops.pinTrial[name] = *e
}

// pinned returns the persisted pin of name.
func (h *Hub) pinned(name string) (backend.ManifestEntry, bool) {
	h.ops.pinMu.Lock()
	defer h.ops.pinMu.Unlock()
	e, ok := h.ops.pins[name]
	return e, ok
}

func (h *Hub) savePinsLocked() {
	if err := h.st.PutMeta(metaBackendPins, h.ops.pins); err != nil {
		h.log.Warn("cannot save the backend versions in use", dlog.Err(err))
	}
}

// hubRegistry is the planner's backend registry: the global registry with
// every backend at the version in use on this hub (backendEntry), so paths
// and installations follow the pins.
type hubRegistry struct{ h *Hub }

var _ render.Registry = hubRegistry{}

// Lookup implements render.Registry.
func (r hubRegistry) Lookup(id string) (backend.Backend, backend.Transport, error) {
	b, tr, err := backend.Lookup(id)
	if err != nil {
		return nil, tr, err
	}
	cur := b.Manifest()
	if cur.Name == "" {
		cur.Name = b.Name()
	}
	if cur.Builtin || cur.System || cur.Version == "" {
		return b, tr, nil
	}
	e := r.h.pinnedEntry(b.Name(), cur)
	if reflect.DeepEqual(e, cur) {
		return b, tr, nil
	}
	return pinBackend(b, e), tr, nil
}

// Supports implements render.Registry.
func (hubRegistry) Supports(id, proto string) bool { return supports(id, proto) }

// pinnedBackend is a backend rendered at another manifest version.
type pinnedBackend struct {
	backend.Backend
	entry backend.ManifestEntry
}

// Manifest implements backend.Backend with the pinned entry.
func (p pinnedBackend) Manifest() backend.ManifestEntry { return p.entry }

// pinnedKeyGen is a pinnedBackend of a backend that generates keys.
type pinnedKeyGen struct {
	pinnedBackend
	kg backend.KeyGenerator
}

// GenerateKeys implements backend.KeyGenerator.
func (p pinnedKeyGen) GenerateKeys(t backend.Transport) (map[string]string, error) {
	return p.kg.GenerateKeys(t)
}

// pinBackend wraps b so that it reports entry as its manifest.
func pinBackend(b backend.Backend, entry backend.ManifestEntry) backend.Backend {
	pb := pinnedBackend{Backend: b, entry: entry}
	if kg, ok := b.(backend.KeyGenerator); ok {
		return pinnedKeyGen{pinnedBackend: pb, kg: kg}
	}
	return pb
}

// ---------------------------------------------------------------- decoy

// currentDecoy is the decoy SNI the planner uses (section 7.4): the one the
// last reachability check chose, or the first candidate before a check.
func (h *Hub) currentDecoy(cfg *config.Config) string {
	var cands []string
	if cfg != nil && cfg.Hub != nil {
		cands = backend.DecoyCandidates(cfg.Hub.DecoySNIs)
	} else {
		cands = backend.DecoyCandidates(nil)
	}
	h.ops.decoyMu.Lock()
	d := h.ops.decoy
	h.ops.decoyMu.Unlock()
	if d != "" && slices.Contains(cands, d) {
		return d
	}
	if len(cands) > 0 {
		return cands[0]
	}
	return ""
}

// ---------------------------------------------------------------- node certificates

// nodeCertRecord is what the hub remembers about a node certificate it
// signed (security tls show).
type nodeCertRecord struct {
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"not_after"`
}

// recordNodeCert remembers the fingerprint and expiry of a certificate the
// hub signed for node.
func (h *Hub) recordNodeCert(node string, certPEM []byte) {
	cert, err := tlsutil.ParseCert(certPEM)
	if err != nil {
		return
	}
	rec := nodeCertRecord{Fingerprint: tlsutil.Fingerprint(cert.Raw), NotAfter: cert.NotAfter.UTC()}
	if err := h.st.PutMeta(metaNodeCert+node, rec); err != nil {
		h.log.Warn("cannot record the node certificate", dlog.Node(node), dlog.Err(err))
	}
}

// acceptPendingCert accepts the certificate the hub signed for node during
// rotate-ca before config.yaml recorded it: config.yaml gets its
// fingerprint now. It reports whether fingerprint is that certificate.
func (h *Hub) acceptPendingCert(node, fingerprint string) bool {
	var want string
	ok, err := h.st.GetMeta(metaPendingCert+node, &want)
	if err != nil || !ok || !sameFingerprint(want, fingerprint) {
		return false
	}
	if err := h.promoteNodeCert(node, want); err != nil {
		h.log.Error("cannot record the new certificate of a node", dlog.Node(node), dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return false
	}
	return true
}

// promoteNodeCert writes fingerprint as the node's cert_fingerprint and
// forgets the pending record.
func (h *Hub) promoteNodeCert(node, fingerprint string) error {
	_, err := h.mutate(func(c *config.Config) error {
		n, ok := c.NodeByID(node)
		if !ok {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": node})
		}
		if sameFingerprint(n.CertFingerprint, fingerprint) {
			return errNoChange
		}
		n.CertFingerprint = fingerprint
		return nil
	})
	if err != nil && !deyerr.Is(err, errNoChange) {
		return err
	}
	if err := h.st.DeleteMeta(metaPendingCert + node); err != nil {
		h.log.Warn("cannot forget the pending node certificate", dlog.Node(node), dlog.Err(err))
	}
	h.log.Info("node certificate replaced", dlog.Node(node), slog.String("fingerprint", fingerprint))
	return nil
}

// ---------------------------------------------------------------- helpers

// opsEvent emits an event of an owner operation (rotate-tokens, rotate-ca,
// optimize...): config_applied with the operation in the message.
func (h *Hub) opsEvent(tunnel, msg string) {
	h.Emit(state.Event{Type: state.EvConfigApplied, Level: state.LevelInfo, Tunnel: tunnel, Message: msg})
}

// tunnelsOf returns the tunnels of cfg named by id ("" = every tunnel):
// DEY-C021 for an unknown id.
func tunnelsOf(cfg *config.Config, id string) ([]config.Tunnel, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		out := make([]config.Tunnel, 0, len(cfg.Tunnels))
		for _, t := range cfg.Tunnels {
			out = append(out, t.Clone())
		}
		return out, nil
	}
	t, ok := cfg.Tunnel(id)
	if !ok {
		return nil, deyerr.New(deyerr.C021, deyerr.Params{"tunnel": id})
	}
	return []config.Tunnel{t.Clone()}, nil
}

// rerender re-plans tunnel t and makes the hub and its nodes match (after a
// secret changed): an enabled tunnel re-renders through its controller and
// restarts the active transport when its files changed (server side
// first; restarted is then "<transport> on <node>"); a disabled one
// re-renders its warm rungs only. opMu is held.
func (h *Hub) rerender(ctx context.Context, t config.Tunnel, rep *steps) (restarted string, err error) {
	if c := h.tun.lookup(t.ID); c != nil {
		err := c.update(ctx, updateOpts{rep: rep, restartActive: true, quietInstall: true,
			onRestart: func(detail string) { restarted = detail }})
		return restarted, err
	}
	tmp := newTunnelCtl(h, t.ID)
	res, err := tmp.apply(ctx, applyOpts{rep: rep, quietInstall: true})
	if err != nil {
		return "", err
	}
	return "", res.fatal
}
