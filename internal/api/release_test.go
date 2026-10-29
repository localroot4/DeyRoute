package api

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// releaseFile is a ReleaseFile serving data.
func releaseFile(name string, data []byte) ReleaseFile {
	return ReleaseFile{Name: name, Size: int64(len(data)), Reader: io.NopCloser(bytes.NewReader(data))}
}

// closeRecorder closes closed when the server closes it.
type closeRecorder struct {
	io.Reader
	closed chan struct{}
}

func (c closeRecorder) Close() error { close(c.closed); return nil }

// GET /v1/assets/release/{arch} streams the hub's release files as a tar;
// the client hands each one over by name and refuses anything malformed.
func TestReleaseBundle(t *testing.T) {
	p := newPKI(t)
	hub := newFakeHub(p)
	sums := []byte("abc  deyroute_1.2.0_linux_amd64.tar.gz\n")
	archive := bytes.Repeat([]byte("archive"), 5000)
	closed := make(chan struct{})
	hub.release = func(arch string) (ReleaseBundle, error) {
		switch arch {
		case "amd64":
			return ReleaseBundle{Version: "1.2.0", Files: []ReleaseFile{
				releaseFile("SHA256SUMS", sums),
				releaseFile("SHA256SUMS.minisig", []byte("sig")),
				{Name: "deyroute_1.2.0_linux_amd64.tar.gz", Size: int64(len(archive)),
					Reader: closeRecorder{Reader: bytes.NewReader(archive), closed: closed}},
			}}, nil
		case "short":
			return ReleaseBundle{Files: []ReleaseFile{{Name: "SHA256SUMS", Size: 100, Reader: io.NopCloser(strings.NewReader("short"))}}}, nil
		case "twice":
			return ReleaseBundle{Files: []ReleaseFile{releaseFile("a", []byte("1")), releaseFile("a", []byte("2"))}}, nil
		case "many":
			var b ReleaseBundle
			for i := 0; i < maxReleaseFiles+1; i++ {
				b.Files = append(b.Files, releaseFile("f"+strings.Repeat("x", i), []byte("x")))
			}
			return b, nil
		case "badname":
			return ReleaseBundle{Files: []ReleaseFile{releaseFile("../evil", []byte("x"))}}, nil
		case "noreader":
			return ReleaseBundle{Files: []ReleaseFile{{Name: "SHA256SUMS", Size: 1}}}, nil
		case "boom":
			panic("release exploded")
		}
		return ReleaseBundle{}, deyerr.New(deyerr.I004, deyerr.Params{"file": "SHA256SUMS"})
	}
	addr, _ := startServer(t, p, hub, "")
	cfg, _ := p.nodeConfig(t, "de-1")
	c := &ControlClient{HubAddr: addr, TLSConfig: cfg, Hello: func() Hello { return Hello{NodeID: "de-1"} }}
	ctx := context.Background()

	got := map[string][]byte{}
	var order []string
	collect := func(name string, r io.Reader) error {
		data, err := io.ReadAll(r)
		got[name] = data
		order = append(order, name)
		return err
	}
	ver, err := c.FetchRelease(ctx, "amd64", collect)
	require.NoError(t, err)
	require.Equal(t, "1.2.0", ver)
	require.Equal(t, []string{"SHA256SUMS", "SHA256SUMS.minisig", "deyroute_1.2.0_linux_amd64.tar.gz"}, order)
	require.Equal(t, sums, got["SHA256SUMS"])
	require.Equal(t, archive, got["deyroute_1.2.0_linux_amd64.tar.gz"])
	<-closed // the server closed the file
	hub.mu.Lock()
	require.Equal(t, "de-1", hub.releaseNode, "the handler learns which node asks")
	hub.mu.Unlock()

	// An error of the callback stops the download and is returned as is.
	stop := errors.New("stop")
	_, err = c.FetchRelease(ctx, "amd64", func(string, io.Reader) error { return stop })
	require.ErrorIs(t, err, stop)

	nop := func(string, io.Reader) error { return nil }
	for arch, want := range map[string]struct {
		code deyerr.Code
		why  string
	}{
		"short":    {deyerr.S001, "cut short"},
		"twice":    {deyerr.S001, "listed twice"},
		"many":     {deyerr.S001, "more than 16 files"},
		"badname":  {deyerr.X000, ""},
		"noreader": {deyerr.X000, ""},
		"boom":     {deyerr.X000, ""},
		"mips":     {deyerr.I004, ""},
	} {
		_, err := c.FetchRelease(ctx, arch, nop)
		e := requireCode(t, err, want.code)
		require.Contains(t, e.Why(), want.why, arch)
	}
	_, err = c.FetchRelease(ctx, "a/b", nop)
	requireCode(t, err, deyerr.N015)

	// The old endpoint learns the node too.
	_, err = c.FetchAsset(ctx, "amd64", io.Discard)
	requireCode(t, err, deyerr.X008)
	hub.mu.Lock()
	require.Equal(t, "de-1", hub.assetNode)
	hub.mu.Unlock()
}

// readReleaseTar refuses entries no hub produces: links, paths, and more
// bytes than the limit.
func TestReadReleaseTar(t *testing.T) {
	build := func(hdrs ...*tar.Header) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		for _, h := range hdrs {
			require.NoError(t, tw.WriteHeader(h))
			if h.Typeflag == tar.TypeReg {
				_, err := tw.Write(bytes.Repeat([]byte("x"), int(h.Size)))
				require.NoError(t, err)
			}
		}
		require.NoError(t, tw.Close())
		return buf.Bytes()
	}
	nop := func(string, io.Reader) error { return nil }
	ctx := context.Background()
	for name, c := range map[string]struct {
		tar []byte
		why string
	}{
		"symlink":   {build(&tar.Header{Name: "deyroute", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"}), "not a regular file"},
		"directory": {build(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}), "not a regular file"},
		"path":      {build(&tar.Header{Name: "d/deyroute", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), "not a plain file name"},
		"dotfile":   {build(&tar.Header{Name: ".hidden", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), "not a plain file name"},
		"traversal": {build(&tar.Header{Name: "../SHA256SUMS", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}), "not a plain file name"},
		"not tar":   {[]byte(strings.Repeat("garbage!", 200)), "broken or cut short"},
	} {
		err := readReleaseTar(ctx, bytes.NewReader(c.tar), "amd64", "hub:1", nop)
		e := requireCode(t, err, deyerr.S001)
		require.Contains(t, e.Why(), c.why, name)
		require.Equal(t, "Signature or checksum invalid: deyroute release amd64", e.Message(), name)
	}

	old := releaseBundleLimit
	releaseBundleLimit = 1024
	t.Cleanup(func() { releaseBundleLimit = old })
	big := build(&tar.Header{Name: "SHA256SUMS", Typeflag: tar.TypeReg, Size: 4096, Mode: 0o644})
	err := readReleaseTar(ctx, bytes.NewReader(big), "amd64", "hub:1", func(_ string, r io.Reader) error {
		_, err := io.Copy(io.Discard, r)
		return err
	})
	require.Error(t, err)
	err = readReleaseTar(ctx, bytes.NewReader(big), "amd64", "hub:1", nop)
	e := requireCode(t, err, deyerr.S001)
	require.Contains(t, e.Why(), "larger than 1024 bytes")

	// A transfer that breaks is a network error.
	err = readReleaseTar(ctx, io.MultiReader(bytes.NewReader(big[:512]), errReader{}), "amd64", "hub:1", nop)
	requireCode(t, err, deyerr.N009)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }
