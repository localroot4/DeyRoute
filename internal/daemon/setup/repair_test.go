package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

func TestRepairRestartsTheRoleServiceAndKeepsTheConfig(t *testing.T) {
	root := t.TempDir()
	_, err := SetupHub(ctxT(t), hubOpts(root, hubFake(), nil, nil))
	require.NoError(t, err)
	p := filepath.Join(root, config.DefaultPath)
	before, err := os.ReadFile(p)
	require.NoError(t, err)
	// A lost unit file and a lost directory are both repaired.
	require.NoError(t, os.RemoveAll(filepath.Join(root, config.TunnelLogDir)))

	sock := shortSocket(t)
	serveSocket(t, sock)
	f := exec.NewFake()
	f.OnPrefix("systemctl ", exec.OK(""))
	unit, err := Repair(ctxT(t), RepairOptions{Root: root, Runner: f, SocketPath: sock,
		LookupGroup: withGroup, LookupUser: withUser, Chown: (&chownLog{}).chown})
	require.NoError(t, err)
	require.Equal(t, "deyroute-hub.service", unit)
	require.True(t, f.Called("systemctl enable deyroute-hub.service"))
	require.True(t, f.Called("systemctl restart deyroute-hub.service"))
	require.FileExists(t, filepath.Join(root, "etc/systemd/system/deyroute-hub.service"))
	require.DirExists(t, filepath.Join(root, config.TunnelLogDir))
	after, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the config is untouched")
}

func TestRepairErrors(t *testing.T) {
	_, err := Repair(ctxT(t), RepairOptions{Root: t.TempDir(), Runner: exec.NewFake()})
	requireTop(t, err, deyerr.I023)

	root := t.TempDir()
	p := filepath.Join(root, config.DefaultPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
	require.NoError(t, os.WriteFile(p, []byte("role: relay\n"), 0o600))
	_, err = Repair(ctxT(t), RepairOptions{Root: root, Runner: exec.NewFake()})
	requireTop(t, err, deyerr.C013)
}
