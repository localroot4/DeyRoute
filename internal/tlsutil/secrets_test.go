package tlsutil

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestWriteSecret(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "secrets", "tls", "main", "key.pem")
	require.NoError(t, WriteSecret(p, []byte("one")))
	got, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "one", string(got))
	st, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	for _, d := range []string{"secrets", "secrets/tls", "secrets/tls/main"} {
		st, err := os.Stat(filepath.Join(root, d))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), st.Mode().Perm(), d)
	}

	// Overwrite replaces the content and tightens a loose mode.
	require.NoError(t, os.Chmod(p, 0o644))
	require.NoError(t, WriteSecret(p, []byte("two")))
	got, err = os.ReadFile(p)
	require.NoError(t, err)
	require.Equal(t, "two", string(got))
	st, err = os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	// No temporary files are left behind.
	entries, err := os.ReadDir(filepath.Dir(p))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Parent is a regular file: X032.
	blocker := writeFile(t, root, "blocker", []byte("x"))
	e := requireCode(t, WriteSecret(filepath.Join(blocker, "x"), []byte("y")), deyerr.X032)
	require.Contains(t, e.Message(), "blocker")

	// Target is a directory: rename fails, temp file removed.
	dirTarget := filepath.Join(root, "dirtarget")
	require.NoError(t, os.MkdirAll(filepath.Join(dirTarget, "child"), 0o700))
	requireCode(t, WriteSecret(dirTarget, []byte("y")), deyerr.X032)
	entries, err = os.ReadDir(root)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.Contains(e.Name(), ".tmp-"), e.Name())
	}
}

func TestCheckSecretPermsClean(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	require.NoError(t, WriteSecret(filepath.Join(root, "ca.key"), []byte("k")))
	require.NoError(t, WriteSecret(filepath.Join(root, "tls", "main", "cert.pem"), []byte("c")))
	require.NoError(t, os.Chmod(root, 0o700))
	require.Empty(t, CheckSecretPerms(root))
	require.Nil(t, CheckSecretPerms(filepath.Join(root, "missing")))
}

func TestCheckSecretPermsProblems(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	require.NoError(t, WriteSecret(filepath.Join(root, "ca.key"), []byte("k")))
	require.NoError(t, os.Chmod(root, 0o700))
	loose := filepath.Join(root, "hub.key")
	require.NoError(t, os.WriteFile(loose, []byte("k"), 0o600))
	require.NoError(t, os.Chmod(loose, 0o644))
	sub := filepath.Join(root, "backend-tokens")
	require.NoError(t, os.Mkdir(sub, 0o700))
	require.NoError(t, os.Chmod(sub, 0o755))
	link := filepath.Join(root, "node.key")
	require.NoError(t, os.Symlink("/etc/passwd", link))
	fifo := filepath.Join(root, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	errs := CheckSecretPerms(root)
	require.Len(t, errs, 4, "%v", errs)
	byPath := map[string]*deyerr.Error{}
	for _, err := range errs {
		e := requireCode(t, err, deyerr.S002)
		byPath[e.Params["path"].(string)] = e
	}
	require.Equal(t, "Secret file has unsafe permissions: "+loose+" (0644)", byPath[loose].Message())
	require.Equal(t, "chmod 600 "+loose+" && chown root:root "+loose, byPath[loose].Fix())
	require.Equal(t, "0755", byPath[sub].Params["mode"])
	require.Contains(t, byPath[sub].Fix(), "chmod 700 "+sub)
	require.Equal(t, "symlink", byPath[link].Params["mode"])
	require.Contains(t, byPath[fifo].Fix(), "remove")
}

func TestCheckSecretPermsOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("chown needs root")
	}
	root := filepath.Join(t.TempDir(), "secrets")
	f := filepath.Join(root, "ca.key")
	require.NoError(t, WriteSecret(f, []byte("k")))
	require.NoError(t, os.Chmod(root, 0o700))
	require.NoError(t, os.Chown(f, 1000, 1000))
	d := filepath.Join(root, "tls")
	require.NoError(t, os.Mkdir(d, 0o700))
	require.NoError(t, os.Chmod(d, 0o700))
	require.NoError(t, os.Chown(d, 1000, 1000))

	errs := CheckSecretPerms(root)
	require.Len(t, errs, 2, "%v", errs)
	for _, err := range errs {
		e := requireCode(t, err, deyerr.S002)
		require.Contains(t, e.Why(), "uid 1000")
	}

	// Not running as root: ownership is not judged.
	old := geteuid
	geteuid = func() int { return 1000 }
	t.Cleanup(func() { geteuid = old })
	require.Empty(t, CheckSecretPerms(root))
}

func TestCheckSecretPermsUnreadable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	sub := filepath.Join(root, "locked")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.Chmod(root, 0o700))
	require.NoError(t, os.Chmod(sub, 0o000))
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	errs := CheckSecretPerms(root)
	require.NotEmpty(t, errs)
	for _, err := range errs {
		requireCode(t, err, deyerr.S002)
	}
}
