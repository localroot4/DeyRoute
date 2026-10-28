package hub

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/systemd"
)

// An install made before tunnels/ became 01770 left it 0770: the hub adds
// the sticky bit when it starts (deyroute update restarts it), so the
// backends' user can no longer remove or replace root's tunnel logs.
func TestHubStartMakesTunnelLogDirSticky(t *testing.T) {
	logger, logs := newFrontLog()
	env, o := prepareEnv(t, nil, func(o *Options, _ string) { o.Logger = logger })
	dir := filepath.Join(env.root, systemd.TunnelLogDir)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, 0o770))
	env.startEnv(o)
	fi, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.ModeSticky|0o770, fi.Mode()&(os.ModePerm|os.ModeSticky))
	require.Contains(t, logs.String(), "tunnel log directory made sticky")
}
