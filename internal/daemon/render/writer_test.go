package render

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/systemd"
)

type chownRec struct {
	mu    sync.Mutex
	calls map[string][2]int
	fail  error
}

func (c *chownRec) chown(path string, uid, gid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	if c.calls == nil {
		c.calls = map[string][2]int{}
	}
	c.calls[path] = [2]int{uid, gid}
	return nil
}

// ownedBy reports whether path (or the temporary file renamed onto it)
// was chowned to uid:gid.
func (c *chownRec) ownedBy(path string, uid, gid int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.calls[path]; ok {
		return v == [2]int{uid, gid}
	}
	prefix := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	for p, v := range c.calls {
		if strings.HasPrefix(p, prefix) && v == [2]int{uid, gid} {
			return true
		}
	}
	return false
}

func newWriter(t *testing.T) (*HubWriter, *chownRec, *exec.Fake) {
	t.Helper()
	root := t.TempDir()
	fake := exec.NewFake()
	fake.Default = &exec.Response{}
	rec := &chownRec{}
	return &HubWriter{
		Root:        root,
		Systemd:     &systemd.Manager{Root: root, Runner: fake},
		Chown:       rec.chown,
		DeyrouteIDs: func() (int, int, error) { return 997, 996, nil },
	}, rec, fake
}

func side(dir string, files map[string]string) Side {
	f := map[string][]byte{}
	for k, v := range files {
		f[k] = []byte(v)
	}
	return Side{Instance: "main.de-1.rev-tls", ConfigDir: dir, Files: f, DropIn: []byte("[Service]\nExecStart=/bin/true\n")}
}

func TestHubWriterWrite(t *testing.T) {
	w, rec, _ := newWriter(t)
	ctx := context.Background()
	const dir = "/etc/deyroute/backends/rev/main/de-1/tls"
	s := side(dir, map[string]string{"config.toml": "a", "tls-key.pem": "KEY", "sub/extra.json": "{}"})
	changed, err := w.Write(ctx, s)
	require.NoError(t, err)
	require.True(t, changed)

	abs := filepath.Join(w.Root, dir)
	for _, name := range []string{"config.toml", "tls-key.pem", "sub/extra.json"} {
		p := filepath.Join(abs, name)
		info, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, FileMode, info.Mode().Perm(), name)
		require.True(t, rec.ownedBy(p, 0, 996), "owner root:deyroute for %s", name)
	}
	for _, d := range []string{"/etc/deyroute/backends", "/etc/deyroute/backends/rev", "/etc/deyroute/backends/rev/main", "/etc/deyroute/backends/rev/main/de-1", dir, dir + "/sub"} {
		p := filepath.Join(w.Root, d)
		info, err := os.Stat(p)
		require.NoError(t, err)
		require.Equal(t, DirMode, info.Mode().Perm(), d)
		require.Equal(t, [2]int{0, 996}, rec.calls[p], d)
	}
	drop, err := os.ReadFile(w.Systemd.DropInPath("main.de-1.rev-tls"))
	require.NoError(t, err)
	require.Equal(t, s.DropIn, drop)

	// Same content: nothing changes.
	changed, err = w.Write(ctx, s)
	require.NoError(t, err)
	require.False(t, changed)

	// A file whose mode drifted is fixed without counting as a change.
	require.NoError(t, os.Chmod(filepath.Join(abs, "config.toml"), 0o666))
	changed, err = w.Write(ctx, s)
	require.NoError(t, err)
	require.False(t, changed)
	info, err := os.Stat(filepath.Join(abs, "config.toml"))
	require.NoError(t, err)
	require.Equal(t, FileMode, info.Mode().Perm())

	// Content change, removed file, drop-in change.
	s2 := side(dir, map[string]string{"config.toml": "b"})
	changed, err = w.Write(ctx, s2)
	require.NoError(t, err)
	require.True(t, changed)
	got, err := os.ReadFile(filepath.Join(abs, "config.toml"))
	require.NoError(t, err)
	require.Equal(t, "b", string(got))
	_, err = os.Stat(filepath.Join(abs, "tls-key.pem"))
	require.True(t, os.IsNotExist(err), "files no longer rendered are removed")
	_, err = os.Stat(filepath.Join(abs, "sub/extra.json"))
	require.NoError(t, err, "unrelated subdirectories are left alone")
	entries, err := os.ReadDir(abs)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	require.Equal(t, []string{"config.toml", "sub"}, names, "no temporary files left")

	s3 := s2
	s3.DropIn = []byte("[Service]\nExecStart=/bin/false\n")
	changed, err = w.Write(ctx, s3)
	require.NoError(t, err)
	require.True(t, changed, "drop-in change")

	// A stale file inside a rendered subdirectory is removed.
	s4 := side(dir, map[string]string{"config.toml": "b", "sub/a": "1"})
	_, err = w.Write(ctx, s4)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(abs, "sub/extra.json"))
	require.True(t, os.IsNotExist(err))

	// A symlink planted in place of a file is replaced, not followed.
	outside := filepath.Join(t.TempDir(), "victim")
	require.NoError(t, os.WriteFile(outside, []byte("keep"), 0o600))
	require.NoError(t, os.Remove(filepath.Join(abs, "config.toml")))
	require.NoError(t, os.Symlink(outside, filepath.Join(abs, "config.toml")))
	changed, err = w.Write(ctx, s4)
	require.NoError(t, err)
	require.True(t, changed)
	victim, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Equal(t, "keep", string(victim))
	li, err := os.Lstat(filepath.Join(abs, "config.toml"))
	require.NoError(t, err)
	require.True(t, li.Mode().IsRegular())

	// A directory where a file must go is replaced.
	require.NoError(t, os.Remove(filepath.Join(abs, "sub/a")))
	require.NoError(t, os.Mkdir(filepath.Join(abs, "sub/a"), 0o750))
	_, err = w.Write(ctx, s4)
	require.NoError(t, err)
	li, err = os.Lstat(filepath.Join(abs, "sub/a"))
	require.NoError(t, err)
	require.True(t, li.Mode().IsRegular())
}

func TestHubWriterCanaryKeepsNodeCanary(t *testing.T) {
	w, _, _ := newWriter(t)
	ctx := context.Background()
	// A node literally named "canary" has its warm dir below the canary dir.
	node := side("/etc/deyroute/backends/rev/main/canary/tls", map[string]string{"config.toml": "node"})
	node.Instance = "main.canary.rev-tls"
	_, err := w.Write(ctx, node)
	require.NoError(t, err)
	canary := side(CanaryConfigDir("rev", "main"), map[string]string{"config.toml": "canary"})
	canary.Instance = "main.canary"
	_, err = w.Write(ctx, canary)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(w.Root, "/etc/deyroute/backends/rev/main/canary/tls/config.toml"))
	require.NoError(t, err)
	require.Equal(t, "node", string(got))
}

func TestHubWriterErrors(t *testing.T) {
	ctx := context.Background()
	w, rec, _ := newWriter(t)
	for _, dir := range []string{"", "relative/x/y/z", "/etc/deyroute/backends/rev/main", "/etc/passwd/x/y/z", "/etc/deyroute/backends/rev/../../x/y", "/etc/deyroute/backends/rev/main/de-1/"} {
		_, err := w.Write(ctx, side(dir, map[string]string{"a": "b"}))
		require.Equal(t, deyerr.X032, deyerr.As(err).Code, dir)
	}
	for _, name := range []string{"../x", "/abs", "a/../../b", "", "a//b"} {
		_, err := w.Write(ctx, side("/etc/deyroute/backends/rev/main/de-1/tls", map[string]string{name: "b"}))
		require.Equal(t, deyerr.X032, deyerr.As(err).Code, name)
	}

	// deyroute user missing.
	w2, _, _ := newWriter(t)
	w2.DeyrouteIDs = func() (int, int, error) { return 0, 0, errors.New("unknown user deyroute") }
	_, err := w2.Write(ctx, side("/etc/deyroute/backends/rev/main/de-1/tls", map[string]string{"a": "b"}))
	require.Equal(t, deyerr.X032, deyerr.As(err).Code)
	require.Contains(t, deyerr.As(err).Why(), "deyroute")

	// chown failure.
	rec.fail = errors.New("EPERM")
	_, err = w.Write(ctx, side("/etc/deyroute/backends/rev/main/de-1/tls", map[string]string{"a": "b"}))
	require.Equal(t, deyerr.X032, deyerr.As(err).Code)
	rec.fail = nil

	// A file where a directory must go.
	w3, _, _ := newWriter(t)
	p := filepath.Join(w3.Root, "/etc/deyroute/backends/rev")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	_, err = w3.Write(ctx, side("/etc/deyroute/backends/rev/main/de-1/tls", map[string]string{"a": "b"}))
	require.Equal(t, deyerr.X032, deyerr.As(err).Code)

	// Cancelled context.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = w.Write(cctx, side("/etc/deyroute/backends/rev/main/de-1/tls", map[string]string{"a": "b"}))
	require.Equal(t, deyerr.X031, deyerr.As(err).Code)

	// Invalid instance name for the drop-in.
	s := side("/etc/deyroute/backends/rev/main/de-1/tls", map[string]string{"a": "b"})
	s.Instance = "bad instance/../x"
	_, err = w.Write(ctx, s)
	require.Error(t, err)
}

func TestHubWriterDefaults(t *testing.T) {
	w := &HubWriter{}
	require.Equal(t, "/", w.root())
	require.NotNil(t, w.manager())
	// LookupDeyrouteIDs runs against the real user database; the user usually
	// does not exist in a test environment, both outcomes are fine.
	if uid, gid, err := LookupDeyrouteIDs(); err == nil {
		require.GreaterOrEqual(t, uid, 0)
		require.GreaterOrEqual(t, gid, 0)
	}
	// Default chown on a file we own, to our own uid/gid, works unprivileged
	// only when gid matches; just exercise the error path mapping.
	err := (&HubWriter{}).chown(filepath.Join(t.TempDir(), "missing"), 0)
	require.Equal(t, deyerr.X032, deyerr.As(err).Code)
}

func TestHubWriterRemove(t *testing.T) {
	w, _, fake := newWriter(t)
	ctx := context.Background()
	const dir = "/etc/deyroute/backends/rev/main/de-1/tls"
	_, err := w.Write(ctx, side(dir, map[string]string{"a": "b"}))
	require.NoError(t, err)
	other := "/etc/deyroute/backends/rev/other/de-1/tls"
	o := side(other, map[string]string{"a": "b"})
	o.Instance = "other.de-1.rev-tls"
	_, err = w.Write(ctx, o)
	require.NoError(t, err)

	require.NoError(t, w.Remove(ctx, "main.de-1.rev-tls", dir))
	require.True(t, fake.Called("systemctl stop deyroute-tun@main.de-1.rev-tls.service"))
	require.True(t, fake.Called("systemctl daemon-reload"))
	for _, d := range []string{dir, "/etc/deyroute/backends/rev/main"} {
		_, err := os.Stat(filepath.Join(w.Root, d))
		require.True(t, os.IsNotExist(err), d)
	}
	_, err = os.Stat(filepath.Join(w.Root, "/etc/deyroute/backends/rev"))
	require.NoError(t, err, "a parent that still holds another tunnel stays")
	_, err = os.Stat(w.Systemd.DropInDir("main.de-1.rev-tls"))
	require.True(t, os.IsNotExist(err))
	// Idempotent.
	require.NoError(t, w.Remove(ctx, "main.de-1.rev-tls", dir))
	// Validation and systemctl failure.
	require.Equal(t, deyerr.X032, deyerr.As(w.Remove(ctx, "main.de-1.rev-tls", "/tmp")).Code)
	fake.On("systemctl stop deyroute-tun@other.de-1.rev-tls.service", exec.Fail(1, "boom"))
	require.Error(t, w.Remove(ctx, "other.de-1.rev-tls", other))
}
