package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"aead.dev/minisign"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// tarEntry describes one tar member for makeTarGz.
type tarEntry struct {
	Name     string
	Body     string
	Type     byte // default tar.TypeReg
	Linkname string
	Mode     int64
}

func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Type
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.Mode
		if mode == 0 {
			mode = 0o755
		}
		h := &tar.Header{Name: e.Name, Typeflag: typ, Mode: mode, Linkname: e.Linkname, ModTime: time.Unix(1700000000, 0)}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.Body))
		}
		require.NoError(t, tw.WriteHeader(h))
		if typ == tar.TypeReg {
			_, err := tw.Write([]byte(e.Body))
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

type zipEntry struct {
	Name    string
	Body    string
	Symlink bool
}

func makeZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.Name, Method: zip.Deflate}
		if e.Symlink {
			h.SetMode(os.ModeSymlink | 0o777)
		} else {
			h.SetMode(0o755)
		}
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, err = w.Write([]byte(e.Body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func makeGz(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, data, 0o600))
	return p
}

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// testKey is a throwaway minisign key pair generated per test binary.
type testKey struct {
	pub  minisign.PublicKey
	priv minisign.PrivateKey
}

func newTestKey(t *testing.T) testKey {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(nil)
	require.NoError(t, err)
	return testKey{pub: pub, priv: priv}
}

func (k testKey) pubString() string { return k.pub.String() }

// signLegacy produces a non-prehashed ("Ed") signature.
func (k testKey) signLegacy(msg []byte) []byte {
	return minisign.SignWithComments(k.priv, msg, "timestamp:1\tfile:test", "test signature")
}

// signHashed produces a prehashed ("ED", minisign's default) signature.
func (k testKey) signHashed(t *testing.T, msg []byte) []byte {
	t.Helper()
	r := minisign.NewReader(bytes.NewReader(msg))
	_, err := io.Copy(io.Discard, r)
	require.NoError(t, err)
	return r.SignWithComments(k.priv, "timestamp:2\tfile:test\thashed", "test signature")
}

// mapFetcher serves fixed URL → content and counts calls.
type mapFetcher struct {
	mu    sync.Mutex
	files map[string][]byte
	errs  map[string]error
	calls map[string]int
	shas  []string
}

func newMapFetcher() *mapFetcher {
	return &mapFetcher{files: map[string][]byte{}, errs: map[string]error{}, calls: map[string]int{}}
}

func (m *mapFetcher) Fetch(ctx context.Context, url string, w io.Writer) error {
	m.mu.Lock()
	m.calls[url]++
	m.shas = append(m.shas, ExpectedSHA256(ctx))
	data, ok := m.files[url]
	err := m.errs[url]
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return Permanent(deyerr.Plain("404 " + url))
	}
	_, werr := w.Write(data)
	return werr
}

func (m *mapFetcher) count(url string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[url]
}

// noSleep records requested backoff durations without waiting.
type noSleep struct {
	mu     sync.Mutex
	waited []time.Duration
}

func (n *noSleep) sleep(_ context.Context, d time.Duration) error {
	n.mu.Lock()
	n.waited = append(n.waited, d)
	n.mu.Unlock()
	return nil
}

func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	de := deyerr.As(err)
	require.Equal(t, code, de.Code, "error: %v", err)
	return de
}
