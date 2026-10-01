package node

import (
	"context"
	"path/filepath"

	"github.com/localroot4/deyroute/internal/backend"
	// BackendHooks look backends up in the registry: register all of them.
	_ "github.com/localroot4/deyroute/internal/backend/all"
	"github.com/localroot4/deyroute/internal/exec"
)

// Hooks run the backend-specific steps around starting and stopping a
// unit (spec section 7; ARCHITECTURE.md section 7.3): for awg/userspace the
// UAPI socket directory before start, the device configuration after start
// and the interface cleanup after stop. backendName is the first element of
// the instance's config directory, configDir its absolute system path.
type Hooks interface {
	PreStart(ctx context.Context, backendName, configDir string) error
	PostStart(ctx context.Context, backendName, configDir string) error
	PostStop(ctx context.Context, backendName, configDir string) error
	// HasPostStart reports whether the backend configures its unit after
	// start; the agent then re-runs PostStart when systemd restarted the
	// unit's process (a new MainPID).
	HasPostStart(backendName string) bool
}

// hookRunner is the unnamed runner interface the backends' optional hooks
// take (wireguard.Runner is an alias of the same type), so they are found
// by a structural type assertion without importing any backend package.
type hookRunner = interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

type preStarter interface {
	PreStart(ctx context.Context, configDir string, r hookRunner) error
}

type postStarter interface {
	PostStart(ctx context.Context, configDir string, r hookRunner) error
}

type postStopper interface {
	PostStop(ctx context.Context, configDir string, r hookRunner) error
}

// BackendHooks implements Hooks with the optional PreStart, PostStart and
// PostStop methods of the registered backend of that name (the wireguard
// package's awg backend implements all three). Backends without them need
// nothing.
type BackendHooks struct {
	// Runner runs `ip` for the hooks.
	Runner exec.Runner
	// Root prefixes configDir ("/" or "" on a real system).
	Root string
	// Lookup finds a backend by name; nil = the global registry.
	Lookup func(name string) (backend.Backend, bool)
}

func (h BackendHooks) lookup(name string) (backend.Backend, bool) {
	if h.Lookup != nil {
		return h.Lookup(name)
	}
	for _, b := range backend.All() {
		if b.Name() == name {
			return b, true
		}
	}
	return nil, false
}

func (h BackendHooks) dir(configDir string) string {
	if h.Root == "" {
		return configDir
	}
	return filepath.Join(h.Root, configDir)
}

// PreStart implements Hooks.
func (h BackendHooks) PreStart(ctx context.Context, backendName, configDir string) error {
	if b, ok := h.lookup(backendName); ok {
		if p, ok := b.(preStarter); ok {
			return p.PreStart(ctx, h.dir(configDir), h.Runner)
		}
	}
	return nil
}

// PostStart implements Hooks.
func (h BackendHooks) PostStart(ctx context.Context, backendName, configDir string) error {
	if b, ok := h.lookup(backendName); ok {
		if p, ok := b.(postStarter); ok {
			return p.PostStart(ctx, h.dir(configDir), h.Runner)
		}
	}
	return nil
}

// PostStop implements Hooks.
func (h BackendHooks) PostStop(ctx context.Context, backendName, configDir string) error {
	if b, ok := h.lookup(backendName); ok {
		if p, ok := b.(postStopper); ok {
			return p.PostStop(ctx, h.dir(configDir), h.Runner)
		}
	}
	return nil
}

// HasPostStart implements Hooks.
func (h BackendHooks) HasPostStart(backendName string) bool {
	b, ok := h.lookup(backendName)
	if !ok {
		return false
	}
	_, ok = b.(postStarter)
	return ok
}
