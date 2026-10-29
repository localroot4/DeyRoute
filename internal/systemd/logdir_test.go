package systemd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// modeBits returns the permission, setuid, setgid and sticky bits of p as
// chmod writes them (0o1770).
func modeBits(t *testing.T, p string) uint32 {
	t.Helper()
	fi, err := os.Lstat(p)
	require.NoError(t, err)
	m := uint32(fi.Mode().Perm())
	if fi.Mode()&os.ModeSticky != 0 {
		m |= 0o1000
	}
	if fi.Mode()&os.ModeSetgid != 0 {
		m |= 0o2000
	}
	if fi.Mode()&os.ModeSetuid != 0 {
		m |= 0o4000
	}
	return m
}

// The services add the sticky bit to an existing tunnels/ of their own user
// (root) when they start: an install made before the directory became
// 01770 has 0770, where the backends' user could remove or replace root's
// logs. Only the sticky bit is added; no other bit changes, and a directory
// of another user, a link or a file at the name is left alone.
func TestTunnelLogDirGetsStickyBit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     os.FileMode // chmod before
		from, to uint32
	}{
		{"older install", 0o770, 0o770, 0o1770},
		{"already sticky", os.ModeSticky | 0o770, 0o1770, 0o1770},
		{"closed to the group", 0o750, 0o750, 0o1750},
		{"setgid kept", os.ModeSetgid | 0o770, 0o2770, 0o3770},
		{"no bit removed", 0o777, 0o777, 0o1777},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tunnels")
			require.NoError(t, os.Mkdir(dir, 0o700))
			require.NoError(t, os.Chmod(dir, tc.mode))
			from, to, err := TightenTunnelLogDir(dir)
			require.NoError(t, err)
			require.Equal(t, tc.from, from)
			require.Equal(t, tc.to, to)
			require.Equal(t, tc.to, modeBits(t, dir))
		})
	}

	t.Run("missing", func(t *testing.T) {
		from, to, err := TightenTunnelLogDir(filepath.Join(t.TempDir(), "tunnels"))
		require.NoError(t, err)
		require.Equal(t, from, to)
	})

	t.Run("a link to a directory", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "elsewhere")
		require.NoError(t, os.Mkdir(target, 0o700))
		require.NoError(t, os.Chmod(target, 0o770))
		dir := filepath.Join(base, "tunnels")
		require.NoError(t, os.Symlink(target, dir))
		from, to, err := TightenTunnelLogDir(dir)
		require.NoError(t, err)
		require.Equal(t, from, to)
		require.Equal(t, uint32(0o770), modeBits(t, target))
	})

	t.Run("a file", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "tunnels")
		require.NoError(t, os.WriteFile(p, nil, 0o600))
		require.NoError(t, os.Chmod(p, 0o660))
		from, to, err := TightenTunnelLogDir(p)
		require.NoError(t, err)
		require.Equal(t, from, to)
		require.Equal(t, uint32(0o660), modeBits(t, p))
	})

	t.Run("another owner", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("chown to another user needs root")
		}
		dir := filepath.Join(t.TempDir(), "tunnels")
		require.NoError(t, os.Mkdir(dir, 0o700))
		require.NoError(t, os.Chmod(dir, 0o770))
		require.NoError(t, os.Lchown(dir, 65534, 65534))
		from, to, err := TightenTunnelLogDir(dir)
		require.NoError(t, err)
		require.Equal(t, uint32(0o770), from)
		require.Equal(t, from, to)
		require.Equal(t, uint32(0o770), modeBits(t, dir))
	})
}
