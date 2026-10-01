package tui

import (
	"context"
	"testing"

	"github.com/localroot4/deyroute/internal/api"
)

// Nodes > List shows the last error a node agent reported in its
// heartbeat (control characters made harmless); a node without one shows
// no such line.
func TestNodeListShowsTheLastError(t *testing.T) {
	stub := fullStub(&callLog{}, "simple")
	stub.NodeListFn = func(context.Context) ([]api.NodeInfo, error) {
		ns := sampleNodes()
		ns[1].LastError = "DEY-B003 Backend failed to start\x1b[2J"
		return ns, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 200}, Local: stub})
	h.choose("3").choose("2")
	h.must("\n  Control channel  online (39ms)\n", "\n  Last error       DEY-B003 Backend failed to start?[2J\n")
	h.mustNot("\x1b[2J")
}
