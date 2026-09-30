package systemd

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func listen(t *testing.T, addr string) *net.UnixConn {
	t.Helper()
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: addr, Net: "unixgram"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func recv(t *testing.T, conn *net.UnixConn) string {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	buf := make([]byte, 4096)
	n, _, err := conn.ReadFromUnix(buf)
	require.NoError(t, err)
	return string(buf[:n])
}

func TestNotifyUnset(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	require.NoError(t, Notify(StateReady))
}

func TestNotifyPathSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "dn") // short path: sun_path is 108 bytes
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	addr := filepath.Join(dir, "notify.sock")
	conn := listen(t, addr)
	t.Setenv("NOTIFY_SOCKET", addr)

	require.NoError(t, Notify(StateReady))
	require.Equal(t, "READY=1", recv(t, conn))
	require.NoError(t, NotifyStatus("3 tunnels up"))
	require.Equal(t, "STATUS=3 tunnels up", recv(t, conn))
	require.NoError(t, Notify(StateStopping+"\nSTATUS=bye"))
	require.Equal(t, "STOPPING=1\nSTATUS=bye", recv(t, conn))
}

func TestNotifyAbstractSocket(t *testing.T) {
	addr := fmt.Sprintf("@deyroute-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	conn := listen(t, addr)
	t.Setenv("NOTIFY_SOCKET", addr)
	require.NoError(t, Notify(StateWatchdog))
	require.Equal(t, "WATCHDOG=1", recv(t, conn))
}

func TestNotifyErrors(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "vsock:2:1234")
	require.Error(t, Notify(StateReady))
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	require.Error(t, Notify(StateReady))
}

func TestWatchdogInterval(t *testing.T) {
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "")
	require.Zero(t, WatchdogInterval())
	t.Setenv("WATCHDOG_USEC", "30000000")
	require.Equal(t, 15*time.Second, WatchdogInterval())
	t.Setenv("WATCHDOG_PID", strconv.Itoa(os.Getpid()))
	require.Equal(t, 15*time.Second, WatchdogInterval())
	t.Setenv("WATCHDOG_PID", "1")
	require.Zero(t, WatchdogInterval())
	t.Setenv("WATCHDOG_PID", "")
	for _, bad := range []string{"abc", "0", "-5"} {
		t.Setenv("WATCHDOG_USEC", bad)
		require.Zero(t, WatchdogInterval(), bad)
	}
}

func TestRunWatchdogDisabled(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	require.NoError(t, RunWatchdog(context.Background(), nil))
}

func TestRunWatchdogPings(t *testing.T) {
	addr := fmt.Sprintf("@deyroute-wd-%d-%d", os.Getpid(), time.Now().UnixNano())
	conn := listen(t, addr)
	t.Setenv("NOTIFY_SOCKET", addr)
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "20000") // ping every 10ms

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunWatchdog(ctx, nil) }()
	require.Equal(t, "WATCHDOG=1", recv(t, conn))
	require.Equal(t, "WATCHDOG=1", recv(t, conn))
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
}

func TestRunWatchdogReportsErrors(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", filepath.Join(t.TempDir(), "gone.sock"))
	t.Setenv("WATCHDOG_PID", "")
	t.Setenv("WATCHDOG_USEC", "20000")
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var errs []error
	got := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		done <- RunWatchdog(ctx, func(err error) {
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
			once.Do(func() { close(got) })
		})
	}()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no watchdog error reported")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, errs)
}
