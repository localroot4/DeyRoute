package log

import (
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// OpenLogFile opens only a regular file: a link (to a file or a directory),
// a FIFO, a socket and a directory at the name are ErrNotRegular, at once.
func TestOpenLogFileAcceptsOnlyRegular(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(victim, victimData, 0o600))

	p := filepath.Join(dir, "t.log")
	require.NoError(t, os.WriteFile(p, []byte("a\nb\n"), 0o600))
	f, fi, err := OpenLogFile(p)
	require.NoError(t, err)
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.Equal(t, "a\nb\n", string(data))
	require.Equal(t, int64(4), fi.Size())

	_, _, err = OpenLogFile(filepath.Join(dir, "missing.log"))
	require.ErrorIs(t, err, fs.ErrNotExist)

	link := filepath.Join(dir, "link.log")
	require.NoError(t, os.Symlink(victim, link))
	dirLink := filepath.Join(dir, "dirlink.log")
	require.NoError(t, os.Symlink(dir, dirLink))
	fifo := filepath.Join(dir, "fifo.log")
	require.NoError(t, unix.Mkfifo(fifo, 0o600))
	sub := filepath.Join(dir, "dir.log")
	require.NoError(t, os.Mkdir(sub, 0o700))
	sock := filepath.Join(dir, "sock.log")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	for _, name := range []string{link, dirLink, fifo, sub, sock} {
		f, fi, err := OpenLogFile(name)
		require.ErrorIs(t, err, ErrNotRegular, name)
		require.Nil(t, f)
		require.Nil(t, fi)
	}
	requireContent(t, victim, victimData)
}
