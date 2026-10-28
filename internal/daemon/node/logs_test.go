package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The tunnel logs live in a directory where the backends' user (deyroute)
// can add names. The node reads them as root for deyroute logs (logs.tail,
// with and without follow): it never reads through a link planted at a
// log's name, never blocks on a FIFO and reads only a regular file.
func TestNodeReadTailRefusesLink(t *testing.T) {
	dir := t.TempDir()
	secret := "root:secret-hash:19000:0:99999:7:::"
	victim := filepath.Join(dir, "shadow")
	writeFile(t, victim, secret+"\n", 0o600)

	fifo := filepath.Join(dir, "fifo.log")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	done := make(chan error, 1)
	go func() {
		_, _, err := readTail(fifo, 10, time.Time{})
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err, "a FIFO is not a log")
	case <-time.After(5 * time.Second):
		// Release the blocked open: a writer appears and goes away.
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("readTail blocked on a FIFO")
	}

	link := filepath.Join(dir, "t.log")
	require.NoError(t, os.Symlink(victim, link))
	lines, _, err := readTail(link, 10, time.Time{})
	require.Error(t, err, "a link is not followed")
	require.Empty(t, lines)

	// Follow never streams what the link points to.
	a := &agent{o: Options{FollowPoll: 5 * time.Millisecond}}
	var mu sync.Mutex
	var got []string
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	a.follow(ctx, link, 0, func(ls []string) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ls...)
		return nil
	})
	mu.Lock()
	defer mu.Unlock()
	require.NotContains(t, strings.Join(got, "\n"), "secret-hash")
	require.Empty(t, got)
}
