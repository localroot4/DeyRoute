package setup

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

const (
	route4Line = "ip -4 route get 1.1.1.1"
	route6Line = "ip -6 route get 2606:4700:4700::1111"
	route4Out  = "1.1.1.1 via 5.6.7.1 dev eth0 src 5.6.7.8 uid 0 \n    cache \n"
	noRoute6   = "RTNETLINK answers: Network is unreachable"
	testGID    = 4242
	testUID    = 4243
)

// requireCode asserts err carries code somewhere in its chain.
func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	require.Truef(t, deyerr.HasCode(err, code), "want %s in %v", code, err)
	return deyerr.As(err)
}

// requireTop asserts the top-level code of err.
func requireTop(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	e := deyerr.As(err)
	require.Equalf(t, code, e.Code, "got %v", err)
	return e
}

// hubFake scripts the commands a hub setup runs: IPv4 route with a public
// source, no IPv6 route, nft and systemctl succeed.
func hubFake() *exec.Fake {
	f := exec.NewFake()
	f.On(route4Line, exec.OK(route4Out))
	f.On(route6Line, exec.Fail(2, noRoute6))
	f.On("nft -f -", exec.OK(""))
	f.OnPrefix("systemctl ", exec.OK(""))
	return f
}

// chownLog records chown calls.
type chownLog struct {
	mu    sync.Mutex
	calls map[string]int
}

func (c *chownLog) chown(path string, uid, gid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if uid != -1 {
		return errors.New("setup must not change the owner")
	}
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[path] = gid
	return nil
}

func (c *chownLog) gid(path string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.calls[path]
	return g, ok
}

func withGroup(string) (int, error) { return testGID, nil }

func withUser(string) (int, error) { return testUID, nil }

func noGroup(name string) (int, error) { return 0, errors.New("unknown group " + name) }

// sysUsers simulates systemd-sysusers: the deyroute group exists once the
// command ran successfully.
type sysUsers struct {
	mu      sync.Mutex
	created bool
	fail    bool
	calls   []exec.Call
}

func (s *sysUsers) lookup(name string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.created {
		return testGID, nil
	}
	return noGroup(name)
}

func (s *sysUsers) lookupUser(name string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.created {
		return testUID, nil
	}
	return 0, errors.New("unknown user " + name)
}

// handle is an exec.Fake Handler for systemd-sysusers.
func (s *sysUsers) handle(c exec.Call) (exec.Response, bool) {
	if c.Name != "systemd-sysusers" {
		return exec.Response{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	if s.fail {
		return exec.Fail(1, "Failed to create user deyroute: Read-only file system"), true
	}
	s.created = true
	return exec.OK(""), true
}

func (s *sysUsers) ran() []exec.Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]exec.Call(nil), s.calls...)
}

// stepLog collects progress updates.
type stepLog struct {
	mu    sync.Mutex
	steps []api.Step
}

func (s *stepLog) add(st api.Step) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, st)
}

// final returns "id=status" for every non-running update, in order.
func (s *stepLog) final() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, st := range s.steps {
		if st.Status != api.StepRunning {
			out = append(out, st.ID+"="+st.Status)
		}
	}
	return out
}

// get returns the last non-running update of id.
func (s *stepLog) get(id string) (api.Step, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.steps) - 1; i >= 0; i-- {
		if s.steps[i].ID == id && s.steps[i].Status != api.StepRunning {
			return s.steps[i], true
		}
	}
	return api.Step{}, false
}

// fakeProc creates the /proc/sys keys of the balanced profile under root.
func fakeProc(t *testing.T, root string) {
	t.Helper()
	keys := map[string]string{
		"net/core/default_qdisc":                    "pfifo_fast",
		"net/ipv4/tcp_congestion_control":           "cubic",
		"net/ipv4/tcp_available_congestion_control": "reno cubic bbr",
		"net/core/somaxconn":                        "4096",
		"net/ipv4/tcp_max_syn_backlog":              "512",
		"net/core/netdev_max_backlog":               "1000",
		"net/ipv4/ip_local_port_range":              "32768\t60999",
		"net/ipv4/tcp_fin_timeout":                  "60",
		"net/ipv4/tcp_tw_reuse":                     "2",
		"net/ipv4/tcp_keepalive_time":               "7200",
		"net/ipv4/tcp_keepalive_intvl":              "75",
		"net/ipv4/tcp_keepalive_probes":             "9",
		"net/ipv4/tcp_fastopen":                     "1",
		"net/ipv4/tcp_mtu_probing":                  "0",
		"net/core/rmem_max":                         "212992",
		"net/core/wmem_max":                         "212992",
		"net/ipv4/tcp_rmem":                         "4096\t131072\t6291456",
		"net/ipv4/tcp_wmem":                         "4096\t16384\t4194304",
		"net/ipv4/udp_rmem_min":                     "4096",
		"net/ipv4/udp_wmem_min":                     "4096",
		"fs/file-max":                               "9223372036854775807",
		"net/ipv4/ip_forward":                       "0",
		"net/ipv4/tcp_notsent_lowat":                "4294967295",
	}
	for k, v := range keys {
		p := filepath.Join(root, "proc/sys", k)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(v+"\n"), 0o644))
	}
}

// procValue reads a fake /proc/sys key.
func procValue(t *testing.T, root, key string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "proc/sys", strings.ReplaceAll(key, ".", "/")))
	require.NoError(t, err)
	return strings.Join(strings.Fields(string(b)), " ")
}

// shortSocket returns a unix socket path short enough for sun_path.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dsk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// serveSocket accepts connections on a unix socket until the test ends.
func serveSocket(t *testing.T, path string) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
}

// mode returns the permission bits of path.
func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Mode().Perm()
}

func fixedNow() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
