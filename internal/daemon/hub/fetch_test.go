package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/version"
)

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// serveFetch makes the fake node answer fetch.proxy with payload. With
// grow > 0 it misbehaves: it uploads extra bytes without checking the
// expected sha256.
func serveFetch(n *fakeNode, payload []byte, grow int, seen *atomic.Value) {
	n.on(api.CmdFetchProxy, func(ctx context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.FetchArgs
		decode(n.t, cmd, &args)
		if seen != nil {
			seen.Store(args)
		}
		data := payload
		if grow > 0 {
			data = append(append([]byte(nil), payload...), bytes.Repeat([]byte("x"), grow)...)
		}
		if grow == 0 && args.SHA256 != "" && args.SHA256 != sum(data) {
			return nil, deyerr.New(deyerr.S001, deyerr.Params{"file": "payload"})
		}
		if err := n.upload(ctx, args.UploadID, data); err != nil {
			return nil, err
		}
		return api.FetchResult{Bytes: int64(len(data)), SHA256: sum(data)}, nil
	})
}

func TestFetchViaNode(t *testing.T) {
	var direct atomic.Int32
	env := startHub(t, nil, func(o *Options, _ string) {
		o.Fetcher = install.FetcherFunc(func(context.Context, string, io.Writer) error {
			direct.Add(1)
			return install.Permanent(deyerr.Plain("github unreachable"))
		})
	})
	// Before any node joined, only the direct fetcher is used.
	_, isChain := env.h.Fetcher().(install.ChainFetcher)
	require.False(t, isChain)
	// The via-node fetcher alone has no node: N012.
	err := nodeFetcher{env.h}.Fetch(ctxT(t), "https://example.com/f", io.Discard)
	require.Equal(t, deyerr.N012, codeOf(err))

	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	payload := bytes.Repeat([]byte("backend binary "), 5000)
	var seen atomic.Value
	serveFetch(n, payload, 0, &seen)

	dst := filepath.Join(t.TempDir(), "backhaul.tar.gz")
	err = install.FetchVerified(ctxT(t), env.h.Fetcher(), []string{"https://github.com/x/backhaul.tar.gz"}, sum(payload), dst,
		install.RetryOptions{Tries: 1})
	require.NoError(t, err)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	args := seen.Load().(api.FetchArgs)
	require.Equal(t, sum(payload), args.SHA256)
	require.Equal(t, "https://github.com/x/backhaul.tar.gz", args.URL)
	require.EqualValues(t, install.DefaultMaxBytes, args.MaxBytes)
	require.Zero(t, direct.Load(), "the node served it; no direct download")

	// Without an expected sha256 the limit is the unverified one.
	data, _, err := install.FetchBytes(ctxT(t), env.h.Fetcher(), []string{"https://github.com/x/SHA256SUMS"}, install.RetryOptions{Tries: 1})
	require.NoError(t, err)
	require.Equal(t, payload, data)
	args = seen.Load().(api.FetchArgs)
	require.Empty(t, args.SHA256)
	require.EqualValues(t, MaxUnverifiedFetch, args.MaxBytes)

	// No pending upload is left behind.
	env.h.upMu.Lock()
	require.Empty(t, env.h.uploads)
	env.h.upMu.Unlock()
}

func TestFetchViaNodeLimitsAndFallback(t *testing.T) {
	var direct atomic.Int32
	payload := []byte("small file")
	env := startHub(t, nil, func(o *Options, _ string) {
		o.Fetcher = install.FetcherFunc(func(_ context.Context, _ string, w io.Writer) error {
			direct.Add(1)
			_, err := w.Write(payload)
			return err
		})
	})
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)

	// The node serves other bytes than expected: the chain rewinds the
	// file and falls back to the direct download.
	serveFetch(n, payload, 1000, nil)
	dst := filepath.Join(t.TempDir(), "f")
	err := install.FetchVerified(ctxT(t), env.h.Fetcher(), []string{"https://github.com/x/f"}, sum(payload), dst,
		install.RetryOptions{Tries: 1})
	require.NoError(t, err)
	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, payload, data)
	require.EqualValues(t, 1, direct.Load())

	// The node uploads more than MaxBytes (16 MiB without a sha256): refused.
	serveFetch(n, payload, MaxUnverifiedFetch, nil)
	var buf bytes.Buffer
	err = nodeFetcher{env.h}.Fetch(ctxT(t), "https://github.com/x/f", &buf)
	require.Equal(t, deyerr.N015, codeOf(err))
	require.Contains(t, deyerr.As(err).Message()+deyerr.As(err).Why(), "larger")

	// The node refuses: its error is returned.
	n.on(api.CmdFetchProxy, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return nil, deyerr.New(deyerr.N051, deyerr.Params{"node": "de-1", "file": "f"})
	})
	err = nodeFetcher{env.h}.Fetch(ctxT(t), "https://github.com/x/f", &buf)
	require.Equal(t, deyerr.N051, codeOf(err))

	// The node answers without uploading.
	n.on(api.CmdFetchProxy, func(context.Context, *fakeNode, api.Command, func([]string)) (any, error) {
		return api.FetchResult{Bytes: 3}, nil
	})
	env.h.o.UploadGrace = 50 * time.Millisecond
	err = nodeFetcher{env.h}.Fetch(ctxT(t), "https://github.com/x/f", &buf)
	require.Equal(t, deyerr.N015, codeOf(err))

	// The node reports another size or checksum than it uploaded.
	n.on(api.CmdFetchProxy, func(ctx context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.FetchArgs
		decode(n.t, cmd, &args)
		if err := n.upload(ctx, args.UploadID, payload); err != nil {
			return nil, err
		}
		return api.FetchResult{Bytes: 1, SHA256: sum(payload)}, nil
	})
	err = nodeFetcher{env.h}.Fetch(ctxT(t), "https://github.com/x/f", &buf)
	require.Equal(t, deyerr.N015, codeOf(err))
	n.on(api.CmdFetchProxy, func(ctx context.Context, n *fakeNode, cmd api.Command, _ func([]string)) (any, error) {
		var args api.FetchArgs
		decode(n.t, cmd, &args)
		if err := n.upload(ctx, args.UploadID, payload); err != nil {
			return nil, err
		}
		return api.FetchResult{Bytes: int64(len(payload)), SHA256: strings.Repeat("0", 64)}, nil
	})
	fctx := install.WithExpectedSHA256(ctxT(t), sum(payload))
	err = nodeFetcher{env.h}.Fetch(fctx, "https://github.com/x/f", &bytes.Buffer{})
	require.Equal(t, deyerr.S001, codeOf(err))
}

func TestUploadRules(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	m := env.joinNode("nl-1")

	// Unknown upload id.
	err := n.upload(ctxT(t), "0123456789abcdef", []byte("x"))
	require.Equal(t, deyerr.N015, codeOf(err))

	// An upload id belongs to the node the fetch was sent to, and is used once.
	var buf bytes.Buffer
	p := &pendingUpload{node: "de-1", max: 100, w: &buf, done: make(chan struct{})}
	env.h.upMu.Lock()
	env.h.uploads["abc"] = p
	env.h.upMu.Unlock()
	require.Equal(t, deyerr.N015, codeOf(env.h.upload(ctxT(t), "abc", "nl-1", strings.NewReader("x"))))
	require.NoError(t, n.upload(ctxT(t), "abc", []byte("hello")))
	require.Equal(t, "hello", buf.String())
	require.Equal(t, deyerr.N015, codeOf(env.h.upload(ctxT(t), "abc", "de-1", strings.NewReader("x"))))
	// After the fetch gave up, the payload is not written any more.
	p2 := &pendingUpload{node: "de-1", max: 100, w: &buf, done: make(chan struct{})}
	p2.close()
	env.h.upMu.Lock()
	env.h.uploads["def"] = p2
	env.h.upMu.Unlock()
	require.Equal(t, deyerr.N014, codeOf(env.h.upload(ctxT(t), "def", "de-1", strings.NewReader("late"))))
	require.Equal(t, "hello", buf.String())
	_ = m
}

func TestAssets(t *testing.T) {
	env := startHub(t, nil)
	n := env.joinNode("de-1")

	// The hub's own architecture: SelfBinary with sha256 and version.
	var buf bytes.Buffer
	meta, err := n.client().FetchAsset(ctxT(t), "amd64", &buf)
	require.NoError(t, err)
	require.Equal(t, env.self, buf.Bytes())
	require.Equal(t, sum(env.self), meta.SHA256)
	require.Equal(t, version.Version, meta.Version)
	// Served twice from the digest cache.
	buf.Reset()
	_, err = n.client().FetchAsset(ctxT(t), "amd64", &buf)
	require.NoError(t, err)
	require.Equal(t, env.self, buf.Bytes())

	// Another architecture from the cache.
	cached := []byte("arm64 binary")
	p := env.h.assetCachePath("arm64")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, cached, 0o755)) // #nosec G306 -- test binary
	buf.Reset()
	meta, err = n.client().FetchAsset(ctxT(t), "arm64", &buf)
	require.NoError(t, err)
	require.Equal(t, cached, buf.Bytes())
	require.Equal(t, sum(cached), meta.SHA256)

	// Not cached and a development build: nothing to download (I004).
	require.NoError(t, os.Remove(p))
	_, err = n.client().FetchAsset(ctxT(t), "arm64", &buf)
	require.Equal(t, deyerr.I004, codeOf(err))
	// Unknown architecture: I003.
	_, err = n.client().FetchAsset(ctxT(t), "mips", &buf)
	require.Equal(t, deyerr.I003, codeOf(err))
}

func TestAssetDownloadRelease(t *testing.T) {
	old := version.Version
	version.Version = "1.2.3"
	defer func() { version.Version = old }()
	var urls atomic.Value
	env := startHub(t, nil, func(o *Options, _ string) {
		o.Getenv = func(k string) string {
			if k == install.MirrorEnv {
				return "https://mirror.example"
			}
			return ""
		}
		o.Fetcher = install.FetcherFunc(func(_ context.Context, u string, _ io.Writer) error {
			urls.Store(u)
			return install.Permanent(deyerr.Plain("offline"))
		})
	})
	_, err := env.h.asset(ctxT(t), "arm64")
	require.Error(t, err)
	require.Contains(t, urls.Load().(string), "https://")
	require.Equal(t, "https://mirror.example", env.h.sources()[0].BaseURL)

	// The SelfBinary vanished: X000 with a detail.
	env.h.o.SelfBinary = filepath.Join(env.root, "missing")
	_, err = env.h.asset(ctxT(t), "amd64")
	require.Equal(t, deyerr.X000, codeOf(err))

	// copyFileAtomic writes the cache file.
	src := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.WriteFile(src, []byte("bin"), 0o600))
	dst := filepath.Join(t.TempDir(), "sub", "deyroute-1.2.3-arm64")
	require.NoError(t, copyFileAtomic(src, dst, 0o755))
	fi, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
	require.Error(t, copyFileAtomic(filepath.Join(t.TempDir(), "none"), dst, 0o755))
}

func TestNodeFetcherPermanentFailures(t *testing.T) {
	env := startHub(t, nil)
	// No node online: permanent, so the chain goes direct at once.
	err := nodeFetcher{env.h}.Fetch(ctxT(t), "https://example.com/f", io.Discard)
	require.Equal(t, deyerr.N012, codeOf(err))
	require.True(t, install.IsPermanent(err))

	// A node that cannot fetch (DEY-X008): permanent too.
	env.joinNode("de-1").start()
	env.waitOnline("de-1", true)
	err = nodeFetcher{env.h}.Fetch(ctxT(t), "https://example.com/f", io.Discard)
	require.Equal(t, deyerr.X008, codeOf(err))
	require.True(t, install.IsPermanent(err))

	// Any other failure is worth a retry.
	require.False(t, allUnsupported(nil))
	require.False(t, allUnsupported([]error{deyerr.New(deyerr.X008, nil), deyerr.New(deyerr.N003, nil)}))
	require.True(t, allUnsupported([]error{deyerr.New(deyerr.X008, nil)}))
}
