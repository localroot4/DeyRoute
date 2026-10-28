package node

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/systemd"
)

// An install made before tunnels/ became 01770 left it 0770: the agent adds
// the sticky bit when it starts (deyroute update restarts it), so the
// backends' user can no longer remove or replace root's tunnel logs.
func TestNodeStartMakesTunnelLogDirSticky(t *testing.T) {
	e := newEnv(t)
	dir := e.path(systemd.TunnelLogDir)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, 0o770))
	e.start()
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.ModeSticky|0o770, fi.Mode()&(os.ModePerm|os.ModeSticky))
	require.Contains(t, e.logs.String(), "tunnel log directory made sticky")
}
