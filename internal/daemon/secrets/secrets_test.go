package secrets

import (
	"encoding/base64"
	"encoding/json"
	stderrors "errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newCA(t *testing.T, now time.Time) *tlsutil.CA {
	t.Helper()
	ca, err := tlsutil.NewCA("DEYROUTE CA test", now)
	require.NoError(t, err)
	ca.Now = func() time.Time { return now }
	return ca
}

func newStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	clk := &clock{t: t0}
	return &Store{Root: t.TempDir(), CA: newCA(t, t0), Now: clk.Now}, clk
}

func code(t *testing.T, err error) deyerr.Code {
	t.Helper()
	require.Error(t, err)
	e := deyerr.As(err)
	require.NotNil(t, e, "not a DEY error: %v", err)
	return e.Code
}

func requireMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, mode, info.Mode().Perm(), path)
}

func TestTokenCreateStableRotate(t *testing.T) {
	s, _ := newStore(t)
	tok, err := s.Token("main")
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	require.NoError(t, err)
	require.Len(t, raw, TokenBytes)
	require.Equal(t, "***", deylog.Redact(tok), "token must be registered as a secret")

	path := filepath.Join(s.Root, "etc/deyroute/secrets/backend-tokens/main.token")
	require.Equal(t, path, s.TokenPath("main"))
	requireMode(t, path, 0o600)
	requireMode(t, filepath.Dir(path), 0o700)

	again, err := s.Token("main")
	require.NoError(t, err)
	require.Equal(t, tok, again)

	other, err := s.Token("games")
	require.NoError(t, err)
	require.NotEqual(t, tok, other)

	rot, err := s.RotateToken("main")
	require.NoError(t, err)
	require.NotEqual(t, tok, rot)
	got, err := s.Token("main")
	require.NoError(t, err)
	require.Equal(t, rot, got)

	list, err := s.Tunnels()
	require.NoError(t, err)
	require.Equal(t, []string{"games", "main"}, list)

	require.Equal(t, deyerr.C007, code(t, func() error { _, err := s.Token("../x"); return err }()))
	require.Equal(t, deyerr.C007, code(t, func() error { _, err := s.RotateToken("A"); return err }()))

	// An empty token file is replaced.
	require.NoError(t, os.WriteFile(path, []byte("\n"), 0o600))
	fresh, err := s.Token("main")
	require.NoError(t, err)
	require.Len(t, fresh, 43)

	// A directory in place of the file is an S009.
	bad := s.TokenPath("broken")
	require.NoError(t, os.MkdirAll(bad, 0o700))
	require.Equal(t, deyerr.S009, code(t, func() error { _, err := s.Token("broken"); return err }()))
}

func TestTunnelsEmpty(t *testing.T) {
	s, _ := newStore(t)
	list, err := s.Tunnels()
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestBackendKeys(t *testing.T) {
	s, _ := newStore(t)
	calls := 0
	gen := func() (map[string]string, error) {
		calls++
		return map[string]string{"private": "priv-value-0123456789", "public": "pub-value-0123456789"}, nil
	}
	k, err := s.BackendKeys("main", "rathole", gen)
	require.NoError(t, err)
	require.Equal(t, "priv-value-0123456789", k["private"])
	require.Equal(t, "***", deylog.Redact("priv-value-0123456789"))
	path := s.KeysPath("main", "rathole")
	requireMode(t, path, 0o600)

	// Stored values win over fresh ones; new keys are merged in.
	gen2 := func() (map[string]string, error) {
		return map[string]string{"private": "other", "public": "other", "short_id": "abcdef0123456789"}, nil
	}
	k2, err := s.BackendKeys("main", "rathole", gen2)
	require.NoError(t, err)
	require.Equal(t, "priv-value-0123456789", k2["private"])
	require.Equal(t, "abcdef0123456789", k2["short_id"])
	var onDisk map[string]string
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &onDisk))
	require.Len(t, onDisk, 3)

	// nil gen works once the file exists.
	k3, err := s.BackendKeys("main", "rathole", nil)
	require.NoError(t, err)
	require.Equal(t, k2, k3)
	require.Equal(t, 1, calls)

	// nil gen without a file.
	require.Equal(t, deyerr.S009, code(t, func() error { _, err := s.BackendKeys("main", "xray", nil); return err }()))
	// Generation failure.
	_, err = s.BackendKeys("main", "xray", func() (map[string]string, error) { return nil, stderrors.New("boom") })
	require.Equal(t, deyerr.B009, code(t, err))
	// Bad ids.
	require.Equal(t, deyerr.C007, code(t, func() error { _, err := s.BackendKeys("main", "../x", gen); return err }()))
	require.Equal(t, deyerr.C007, code(t, func() error { _, err := s.BackendKeys("x/y", "xray", gen); return err }()))
	// Damaged file.
	require.NoError(t, os.WriteFile(path, []byte("{nope"), 0o600))
	require.Equal(t, deyerr.S009, code(t, func() error { _, err := s.BackendKeys("main", "rathole", gen); return err }()))
}

func TestDeleteTunnel(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Token("main")
	require.NoError(t, err)
	_, err = s.BackendKeys("main", "xray", func() (map[string]string, error) { return map[string]string{"a": "b"}, nil })
	require.NoError(t, err)
	_, err = s.TunnelTLS("main", "auto", []net.IP{net.ParseIP("5.6.7.8")}, "", "", "")
	require.NoError(t, err)
	_, keep := s.Token("keep")
	require.NoError(t, keep)

	require.NoError(t, s.DeleteTunnel("main"))
	for _, p := range []string{s.TokenPath("main"), filepath.Join(s.Dir(), KeysDir, "main"), s.TLSPath("main")} {
		_, err := os.Stat(p)
		require.True(t, os.IsNotExist(err), p)
	}
	_, err = os.Stat(s.TokenPath("keep"))
	require.NoError(t, err)
	// Idempotent.
	require.NoError(t, s.DeleteTunnel("main"))
	require.Equal(t, deyerr.C007, code(t, s.DeleteTunnel("..")))
	// PKCS12 no longer has material.
	_, _, err = s.PKCS12("main")
	require.Equal(t, deyerr.S009, code(t, err))
}

func TestTelegramToken(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "telegram.token")
	require.NoError(t, os.WriteFile(p, []byte("  123456:ABCdefGhIJKlmnoPQRstuVWxyz0123456789\n"), 0o600))
	tok, err := TelegramToken(p)
	require.NoError(t, err)
	require.Equal(t, "123456:ABCdefGhIJKlmnoPQRstuVWxyz0123456789", tok)
	require.Equal(t, "***", deylog.Redact(tok))

	require.NoError(t, os.WriteFile(p, []byte(" \n"), 0o600))
	_, err = TelegramToken(p)
	require.Equal(t, deyerr.S009, code(t, err))
	_, err = TelegramToken(filepath.Join(dir, "missing"))
	require.Equal(t, deyerr.S009, code(t, err))
	require.Contains(t, deyerr.As(err).Why(), "does not exist")
	// Symlinks are refused.
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(p, link))
	_, err = TelegramToken(link)
	require.Equal(t, deyerr.S009, code(t, err))
}

func TestCheckPerms(t *testing.T) {
	s, _ := newStore(t)
	require.Empty(t, s.CheckPerms()) // missing dir: nothing to audit
	_, err := s.Token("main")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(s.Dir(), 0o700))
	require.Empty(t, s.CheckPerms())
	require.NoError(t, os.Chmod(s.TokenPath("main"), 0o644))
	errs := s.CheckPerms()
	require.Len(t, errs, 1)
	require.Equal(t, deyerr.S002, code(t, errs[0]))
}

func TestJoinTokensPath(t *testing.T) {
	s, _ := newStore(t)
	require.Equal(t, filepath.Join(s.Root, "etc/deyroute/secrets/join-tokens.json"), s.JoinTokensPath())
	require.True(t, strings.HasSuffix((&Store{}).Dir(), "/etc/deyroute/secrets"))
	require.False(t, (&Store{}).now().IsZero())
}
