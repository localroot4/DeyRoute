package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func seedInstall(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{
		"etc/deyroute/secrets", "var/lib/deyroute/bin/xray/v1", "var/lib/deyroute/backups/auto", "var/log/deyroute/tunnels",
		"run/deyroute", "usr/local/bin", "etc/systemd/system/deyroute-tun@main.de-1.xray-reality.service.d",
		"etc/systemd/system/multi-user.target.wants", "etc/sysctl.d",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
	}
	for _, f := range []string{
		"etc/deyroute/config.yaml", "var/lib/deyroute/state.db", "var/lib/deyroute/bin/deyroute.prev",
		"var/lib/deyroute/backups/deyroute-backup-20260101T000000Z.tar.gz.age", "usr/local/bin/deyroute", "usr/local/bin/other-tool",
		"etc/systemd/system/deyroute-hub.service", "etc/systemd/system/deyroute-tun@.service", "etc/systemd/system/nginx.service",
		"etc/sysctl.d/99-deyroute.conf",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, f), []byte("x"), 0o600))
	}
	require.NoError(t, os.Symlink("deyroute", filepath.Join(root, "usr/local/bin/dey")))
	require.NoError(t, os.Symlink("/etc/systemd/system/deyroute-hub.service",
		filepath.Join(root, "etc/systemd/system/multi-user.target.wants/deyroute-hub.service")))
}

func rel(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		r, err := filepath.Rel(root, p)
		require.NoError(t, err)
		out = append(out, r)
	}
	return out
}

func TestUninstallPlanAndRemove(t *testing.T) {
	root := t.TempDir()
	seedInstall(t, root)
	plan := UninstallPlan(root, false)
	require.Equal(t, []string{
		"etc/deyroute",
		"etc/systemd/system/deyroute-hub.service",
		"etc/systemd/system/deyroute-tun@.service",
		"etc/systemd/system/deyroute-tun@main.de-1.xray-reality.service.d",
		"etc/systemd/system/multi-user.target.wants/deyroute-hub.service",
		"run/deyroute",
		"usr/local/bin/dey",
		"usr/local/bin/deyroute",
		"var/lib/deyroute",
		"var/log/deyroute",
	}, rel(t, root, plan))

	keep := UninstallPlan(root, true)
	require.Contains(t, rel(t, root, keep), "var/lib/deyroute/bin")
	require.Contains(t, rel(t, root, keep), "var/lib/deyroute/state.db")
	require.NotContains(t, rel(t, root, keep), "var/lib/deyroute")
	require.NotContains(t, rel(t, root, keep), "var/lib/deyroute/backups")

	require.NoError(t, RemovePaths(root, keep))
	_, err := os.Stat(filepath.Join(root, "var/lib/deyroute/backups/deyroute-backup-20260101T000000Z.tar.gz.age"))
	require.NoError(t, err, "backups are kept")
	for _, p := range []string{"usr/local/bin/other-tool", "etc/systemd/system/nginx.service", "etc/sysctl.d/99-deyroute.conf"} {
		_, err := os.Stat(filepath.Join(root, p))
		require.NoError(t, err, "%s is not deyroute's to delete here", p)
	}
	_, err = os.Lstat(filepath.Join(root, "usr/local/bin/dey"))
	require.True(t, os.IsNotExist(err))

	// Second plan only has what is left; removal is idempotent.
	require.Equal(t, []string{}, append([]string{}, rel(t, root, UninstallPlan(root, true))...))
	require.NoError(t, RemovePaths(root, UninstallPlan(root, false)))
	_, err = os.Stat(filepath.Join(root, "var/lib/deyroute"))
	require.True(t, os.IsNotExist(err))
	require.NoError(t, RemovePaths(root, []string{filepath.Join(root, "var/lib/deyroute")}))
}

func TestRemovePathsRefusesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "keep", "me")
	require.NoError(t, os.MkdirAll(victim, 0o755))
	err := RemovePaths(root, []string{
		root,
		filepath.Join(root, ".."),
		victim,
		filepath.Join(root, "etc"),
		filepath.Join(root, "etc/deyroute/../../../"+filepath.Base(outside)),
	})
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, deyerr.X032))
	_, statErr := os.Stat(victim)
	require.NoError(t, statErr)
	_, statErr = os.Stat(root)
	require.NoError(t, statErr)
}
