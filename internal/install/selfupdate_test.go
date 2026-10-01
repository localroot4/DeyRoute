package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func readString(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return string(b)
}

func TestSelfUpdateAndRollback(t *testing.T) {
	root := t.TempDir()
	s := SelfUpdater{Root: root}
	require.Equal(t, filepath.Join(root, "usr/local/bin/deyroute"), s.BinaryPath())
	require.Equal(t, filepath.Join(root, "var/lib/deyroute/bin/deyroute.prev"), s.PrevPath())

	// Nothing to roll back yet.
	requireCode(t, s.Rollback(), deyerr.S007)
	require.False(t, s.HasPrevious())

	// First install (fresh server): no .prev is created.
	v1 := writeTemp(t, "deyroute-v1", []byte("binary v1"))
	require.NoError(t, s.Install(v1))
	require.Equal(t, "binary v1", readString(t, s.BinaryPath()))
	require.False(t, s.HasPrevious())
	target, err := os.Readlink(s.LinkPath())
	require.NoError(t, err)
	require.Equal(t, "deyroute", target)
	fi, err := os.Stat(s.BinaryPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), fi.Mode().Perm())

	// Update keeps the old binary as deyroute.prev.
	v2 := writeTemp(t, "deyroute-v2", []byte("binary v2"))
	require.NoError(t, Layout{Root: root}.SelfUpdate(v2))
	require.Equal(t, "binary v2", readString(t, s.BinaryPath()))
	require.Equal(t, "binary v1", readString(t, s.PrevPath()))
	require.True(t, s.HasPrevious())

	// Rollback swaps; a second rollback goes forward again.
	require.NoError(t, Layout{Root: root}.Rollback())
	require.Equal(t, "binary v1", readString(t, s.BinaryPath()))
	require.Equal(t, "binary v2", readString(t, s.PrevPath()))
	require.NoError(t, s.Rollback())
	require.Equal(t, "binary v2", readString(t, s.BinaryPath()))
	require.Equal(t, "binary v1", readString(t, s.PrevPath()))

	// No temp files left behind.
	for _, d := range []string{filepath.Dir(s.BinaryPath()), filepath.Dir(s.PrevPath())} {
		ents, err := os.ReadDir(d)
		require.NoError(t, err)
		for _, e := range ents {
			require.NotContains(t, e.Name(), ".new-", d)
			require.NotContains(t, e.Name(), ".swap", d)
		}
	}
}

func TestSelfUpdateRejectsBadInput(t *testing.T) {
	s := SelfUpdater{Root: t.TempDir()}
	requireCode(t, s.Install(filepath.Join(t.TempDir(), "missing")), deyerr.X032)
	requireCode(t, s.Install(writeTemp(t, "empty", nil)), deyerr.S001)
	requireCode(t, s.Install(t.TempDir()), deyerr.S001)

	// An empty .prev cannot be rolled back to.
	require.NoError(t, os.MkdirAll(filepath.Dir(s.PrevPath()), 0o755))
	require.NoError(t, os.WriteFile(s.PrevPath(), nil, 0o755))
	requireCode(t, s.Rollback(), deyerr.S007)
}

func TestRollbackWithoutCurrentBinary(t *testing.T) {
	s := SelfUpdater{Root: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Dir(s.PrevPath()), 0o755))
	require.NoError(t, os.WriteFile(s.PrevPath(), []byte("old"), 0o755))
	require.NoError(t, s.Rollback())
	require.Equal(t, "old", readString(t, s.BinaryPath()))
	require.False(t, s.HasPrevious())
}

func TestEnsureLinkReplacesWrongTarget(t *testing.T) {
	s := SelfUpdater{Root: t.TempDir()}
	require.NoError(t, os.MkdirAll(filepath.Dir(s.LinkPath()), 0o755))
	require.NoError(t, os.WriteFile(s.LinkPath(), []byte("someone else's dey"), 0o755))
	require.NoError(t, s.EnsureLink())
	target, err := os.Readlink(s.LinkPath())
	require.NoError(t, err)
	require.Equal(t, ShortLinkTarget, target)

	// An absolute link to /usr/local/bin/deyroute is accepted as is.
	require.NoError(t, os.Remove(s.LinkPath()))
	require.NoError(t, os.Symlink("/usr/local/bin/deyroute", s.LinkPath()))
	require.NoError(t, s.EnsureLink())
	target, err = os.Readlink(s.LinkPath())
	require.NoError(t, err)
	require.Equal(t, "/usr/local/bin/deyroute", target)
}
