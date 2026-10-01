package errors

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A range expands into one port map per port (internal/ports.ParseInput),
// so the advice for more than 64 maps must not be "use a range": it splits
// the ports over several tunnels (spec audit gap 63, QUESTIONS.md C).
func TestPortMapLimitAdvice(t *testing.T) {
	for _, e := range []*Error{
		New(P016, Params{"count": 101}),
		New(C015, Params{"tunnel": "main", "count": 70}),
	} {
		require.Contains(t, e.Why(), "counts one map per port", e.Code)
		require.NotContains(t, e.Fix(), "use a range", e.Code)
		require.NotContains(t, e.Fix(), "port range", e.Code)
		require.Contains(t, e.Fix(), "tunnel", e.Code)
	}
}
