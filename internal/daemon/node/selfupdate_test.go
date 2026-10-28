package node

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aead.dev/minisign"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/install"
	"github.com/localroot4/deyroute/internal/version"
)

// Self-update from a signed release (DR-03): the node installs a deyroute
// binary only when the signature of SHA256SUMS verifies with the key built
// into it (here the lab key, install.TestPublicKey) and the archive matches.

// useTestKey generates a throwaway signing key, builds it into the node as
// install.TestPublicKey (as -ldflags -X does in the lab) for the test and
// returns the secret key.
func useTestKey(t *testing.T) minisign.PrivateKey {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(nil)
	require.NoError(t, err)
	old := install.TestPublicKey
	install.TestPublicKey = pub.String()
	t.Cleanup(func() { install.TestPublicKey = old })
	return priv
}

// otherKey is a key the node does not trust.
func otherKey(t *testing.T) minisign.PrivateKey {
	t.Helper()
	_, priv, err := minisign.GenerateKey(nil)
	require.NoError(t, err)
	return priv
}

// withVersion runs the test as a node on version v.
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// tarFile is one entry of a test release archive.
type tarFile struct {
	name string
	body []byte
	typ  byte // default tar.TypeReg
	link string
}

func makeArchive(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		typ := f.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: f.name, Typeflag: typ, Mode: 0o755, Linkname: f.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(f.body))
		}
		require.NoError(t, tw.WriteHeader(h))
		if typ == tar.TypeReg {
			_, err := tw.Write(f.body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// signedRelease is the release a hub serves: SHA256SUMS lists archive as
// deyroute_<listed>_linux_<arch>.tar.gz and is signed with key; the archive
// is served as deyroute_<ver>_linux_<arch>.tar.gz.
func signedRelease(t *testing.T, key minisign.PrivateKey, ver, listed, arch string, archive []byte) []bundleFile {
	t.Helper()
	sums := []byte(sum(archive) + "  " + install.ArchiveName(listed, arch) + "\n" +
		sum([]byte("installer")) + "  install.sh\n")
	sig := minisign.SignWithComments(key, sums, "timestamp:1\tfile:SHA256SUMS", "test release")
	return []bundleFile{
		{install.SumsFile, sums},
		{install.SumsSigFile, sig},
		{install.ArchiveName(ver, arch), archive},
	}
}

// signedHub makes the fake hub announce signed updates and serve files as
// its release of ver; its old endpoint serves the binary unsigned (which
// a node with signed updates must never install).
func (e *env) signedHub(ver string, files []bundleFile, unsigned []byte) {
	e.hub.mu.Lock()
	e.hub.hello = api.Hello{Version: ver, Compatible: true, Features: []string{api.FeatureSignedUpdate}}
	e.hub.release, e.hub.releaseV = files, ver
	e.hub.asset, e.hub.assetV = unsigned, ver
	e.hub.mu.Unlock()
}

// requireNothingInstalled checks that the running binary is still old, no
// restart was asked for and no download is left behind.
func (e *env) requireNothingInstalled(t *testing.T, old, what string) {
	t.Helper()
	data, err := os.ReadFile(e.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, old, string(data), "%s: the installed binary is unchanged", what)
	select {
	case <-e.restarts:
		t.Fatalf("%s: a refused update must not restart the service", what)
	default:
	}
	entries, err := os.ReadDir(e.path(DownloadDir))
	if err == nil {
		require.Empty(t, entries, "%s: downloads are cleaned up", what)
	}
}

// requireRestart waits for the service restart after an installed update.
func (e *env) requireRestart(t *testing.T) {
	t.Helper()
	select {
	case <-e.restarts:
	case <-time.After(10 * time.Second):
		t.Fatal("the service was not restarted after self.update")
	}
}

func TestSelfUpdateAcceptsSignedBundle(t *testing.T) {
	key := useTestKey(t)
	e := newEnv(t)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	// As edge builds pack it: the binary in a directory.
	archive := makeArchive(t, []tarFile{{name: "deyroute_9.9.9_linux_amd64/deyroute", body: bin},
		{name: "deyroute_9.9.9_linux_amd64/README.md", body: []byte("readme")}})
	e.signedHub("9.9.9", signedRelease(t, key, "9.9.9", "9.9.9", "amd64", archive), []byte("unsigned binary"))
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	s := e.start()
	require.True(t, s.Hello.HasFeature(api.FeatureSignedUpdate), "the node announces signed updates")

	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: sum(archive)})
	require.NoError(t, err)
	data, err := os.ReadFile(e.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, bin, data)
	prev, err := os.ReadFile(e.path(config.PrevBinaryPath))
	require.NoError(t, err)
	require.Equal(t, "old binary", string(prev))
	e.requireRestart(t)
	entries, err := os.ReadDir(e.path(DownloadDir))
	require.NoError(t, err)
	require.Empty(t, entries, "downloads are cleaned up")

	// As goreleaser packs it: the binary at the top level; no sha256
	// announced (the signed SHA256SUMS is what counts).
	bin2 := append(fakeELF(elf.EM_X86_64, elf.ET_DYN), []byte(" 9.9.10")...)
	archive2 := makeArchive(t, []tarFile{{name: "deyroute", body: bin2}})
	e.signedHub("9.9.10", signedRelease(t, key, "9.9.10", "9.9.10", "amd64", archive2), nil)
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "v9.9.10"})
	require.NoError(t, err)
	data, err = os.ReadFile(e.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, bin2, data)
	e.requireRestart(t)
}

// A hub that serves a valid ELF with a matching announced sha256 but no
// valid signature gets DEY-S001, and nothing is installed.
func TestSelfUpdateRejectsUnsignedBinary(t *testing.T) {
	key := useTestKey(t)
	e := newEnv(t)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	archive := makeArchive(t, []tarFile{{name: "deyroute", body: bin}})
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	good := signedRelease(t, key, "9.9.9", "9.9.9", "amd64", archive)
	e.signedHub("9.9.9", good, bin)
	s := e.start()

	cases := []struct {
		name  string
		files []bundleFile
		args  api.SelfUpdateArgs
		why   string
	}{
		{"signed by another key", signedRelease(t, otherKey(t), "9.9.9", "9.9.9", "amd64", archive),
			api.SelfUpdateArgs{Version: "9.9.9"}, "not a valid signature of SHA256SUMS"},
		{"signed by another key, archive sha256 announced", signedRelease(t, otherKey(t), "9.9.9", "9.9.9", "amd64", archive),
			api.SelfUpdateArgs{Version: "9.9.9", SHA256: sum(archive)}, "not a valid signature of SHA256SUMS"},
		{"no signature", []bundleFile{good[0], good[2]}, api.SelfUpdateArgs{Version: "9.9.9"}, "SHA256SUMS.minisig"},
		{"garbage signature", []bundleFile{good[0], {install.SumsSigFile, []byte("untrusted comment: x\nnot base64\n")}, good[2]},
			api.SelfUpdateArgs{Version: "9.9.9"}, "not a valid signature of SHA256SUMS"},
		{"tampered archive", []bundleFile{good[0], good[1], {good[2].name, makeArchive(t, []tarFile{{name: "deyroute", body: append(bin, 'x')}})}},
			api.SelfUpdateArgs{Version: "9.9.9"}, "does not match the signed SHA256SUMS"},
		{"no archive", good[:2], api.SelfUpdateArgs{Version: "9.9.9"}, "no readable deyroute_9.9.9_linux_amd64.tar.gz"},
	}
	for _, c := range cases {
		e.signedHub("9.9.9", c.files, bin)
		_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, c.args)
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), c.why, c.name)
		e.requireNothingInstalled(t, "old binary", c.name)
	}

	// The archive's sha256 announced by the hub must match what it serves.
	e.signedHub("9.9.9", good, bin)
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: sum(bin)})
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "announced")
	e.requireNothingInstalled(t, "old binary", "announced sha256")

	// Only the release key (and here the lab key) counts: without the lab
	// key built in, the lab release is refused too.
	install.TestPublicKey = ""
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9"})
	de = requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "not with the DEYROUTE release key")
	e.requireNothingInstalled(t, "old binary", "release key only")
}

// A hub without signed updates (an older build) serves only its binary: a
// valid ELF whose sha256 it announces correctly. The node does not take
// it.
func TestSelfUpdateRefusesOlderHub(t *testing.T) {
	useTestKey(t)
	e := newEnv(t)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	e.hub.asset, e.hub.assetV = bin, "9.9.9"
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	s := e.start()
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: sum(bin)})
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "update the hub first")
	e.requireNothingInstalled(t, "old binary", "older hub")
}

// The version in the signed SHA256SUMS (the archive's name) must be the
// version the hub announced: a validly signed release of another version
// is refused.
func TestSelfUpdateRejectsWrongVersionInSums(t *testing.T) {
	key := useTestKey(t)
	e := newEnv(t)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	archive := makeArchive(t, []tarFile{{name: "deyroute", body: bin}})
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	e.signedHub("9.9.9", signedRelease(t, key, "9.9.9", "9.9.9", "amd64", archive), bin)
	s := e.start()

	for name, files := range map[string][]bundleFile{
		// The archive named for 9.9.9, its sums signed for 9.9.8.
		"sums of another version": signedRelease(t, key, "9.9.9", "9.9.8", "amd64", archive),
		// The complete signed release of 9.9.8.
		"another release": signedRelease(t, key, "9.9.8", "9.9.8", "amd64", archive),
		// The release of another architecture.
		"another architecture": signedRelease(t, key, "9.9.9", "9.9.9", "arm64", archive),
	} {
		e.signedHub("9.9.9", files, bin)
		_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9"})
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), "does not list deyroute_9.9.9_linux_amd64.tar.gz", name)
		e.requireNothingInstalled(t, "old binary", name)
	}

	// The hub must name the version: without one nothing can be checked.
	e.signedHub("9.9.9", signedRelease(t, key, "9.9.9", "9.9.9", "amd64", archive), bin)
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{})
	requireCode(t, err, deyerr.S001)
	e.requireNothingInstalled(t, "old binary", "no version")
	// The version the hub serves must be the one it announced.
	e.hub.mu.Lock()
	e.hub.releaseV = "9.9.8"
	e.hub.mu.Unlock()
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9"})
	de := requireCode(t, err, deyerr.S001)
	require.Contains(t, de.Why(), "9.9.8")
	e.requireNothingInstalled(t, "old binary", "served version")
}

// A signed archive whose entries leave the extraction directory, or whose
// deyroute is a link, is refused before anything is written.
func TestSelfUpdateRejectsTarTraversal(t *testing.T) {
	key := useTestKey(t)
	e := newEnv(t)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	e.signedHub("9.9.9", nil, bin)
	s := e.start()

	for name, files := range map[string][]tarFile{
		"parent":          {{name: "../deyroute", body: bin}},
		"deep parent":     {{name: "deyroute_9.9.9_linux_amd64/../../deyroute", body: bin}},
		"absolute":        {{name: "/usr/local/bin/deyroute", body: bin}},
		"other traversal": {{name: "deyroute", body: bin}, {name: "../../etc/cron.d/x", body: []byte("* * * * * root sh")}},
		"symlink":         {{name: "deyroute", typ: tar.TypeSymlink, link: "/bin/sh"}},
		"hard link":       {{name: "README.md", body: bin}, {name: "deyroute", typ: tar.TypeLink, link: "README.md"}},
		"two binaries":    {{name: "deyroute", body: bin}, {name: "x/deyroute", body: bin}},
	} {
		archive := makeArchive(t, files)
		e.signedHub("9.9.9", signedRelease(t, key, "9.9.9", "9.9.9", "amd64", archive), bin)
		_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: sum(archive)})
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), "no single safe deyroute binary", name)
		e.requireNothingInstalled(t, "old binary", name)
		require.NoFileExists(t, filepath.Join(e.root, "..", "deyroute"), name)
		require.NoFileExists(t, e.path("etc/cron.d/x"), name)
	}

	// A signed binary this node cannot run is not installed either.
	for name, b := range map[string][]byte{
		"wrong arch": fakeELF(elf.EM_AARCH64, elf.ET_EXEC),
		"not ELF":    []byte("#!/bin/sh\necho hi\n"),
		"object":     fakeELF(elf.EM_X86_64, elf.ET_REL),
	} {
		archive := makeArchive(t, []tarFile{{name: "deyroute", body: b}})
		e.signedHub("9.9.9", signedRelease(t, key, "9.9.9", "9.9.9", "amd64", archive), bin)
		_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9"})
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), "cannot run", name)
		e.requireNothingInstalled(t, "old binary", name)
	}
}

// A hub on a development build has no signed release: DEY-S012, unless the
// node's config sets node.allow_unsigned_updates (test labs), which takes
// the hub's binary the old way.
func TestSelfUpdateFromDevelopmentBuild(t *testing.T) {
	useTestKey(t)
	e := newEnv(t)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	e.signedHub("dev", nil, bin)
	s := e.start()
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "dev", SHA256: sum(bin)})
	de := requireCode(t, err, deyerr.S012)
	require.Equal(t, "Hub runs an unsigned development build", de.Message())
	require.Contains(t, de.Why(), "deyroute dev")
	e.requireNothingInstalled(t, "old binary", "development build")

	cfg := e.config()
	cfg.Node.AllowUnsignedUpdates = true
	require.NoError(t, config.Save(e.path(config.DefaultPath), cfg))
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "dev", SHA256: sum(bin)})
	require.NoError(t, err)
	data, err := os.ReadFile(e.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, bin, data)
	e.requireRestart(t)
	require.Contains(t, e.logs.String(), "node.allow_unsigned_updates")

	// The old checks still apply on that path.
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "dev", SHA256: sum([]byte("other"))})
	requireCode(t, err, deyerr.S001)

	// The same key lets an older hub (no signed updates) update the node.
	bin2 := append(fakeELF(elf.EM_X86_64, elf.ET_EXEC), '2')
	e.hub.mu.Lock()
	e.hub.hello = api.Hello{Version: "9.9.9", Compatible: true}
	e.hub.asset, e.hub.assetV = bin2, "9.9.9"
	e.hub.mu.Unlock()
	s.Close()
	s = waitSession(t, e.hub)
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: sum(bin2)})
	require.NoError(t, err)
	data, err = os.ReadFile(e.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, bin2, data)
}

// While node.allow_unsigned_updates is set, the node refuses a self.update
// to a version older than the first release that knows the key
// (DEY-S011): that binary would refuse config.yaml and the node could not
// start again. A version that is not older than the node's own is never
// refused, and without the key nothing is refused.
func TestRollbackRefusedWithAllowUnsignedKey(t *testing.T) {
	key := useTestKey(t)
	withVersion(t, "0.3.1")
	e := newEnv(t)
	cfg := e.config()
	cfg.Node.AllowUnsignedUpdates = true
	require.NoError(t, config.Save(e.path(config.DefaultPath), cfg))
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	archive := makeArchive(t, []tarFile{{name: "deyroute", body: bin}})
	e.signedHub("0.2.9", signedRelease(t, key, "0.2.9", "0.2.9", "amd64", archive), bin)
	s := e.start()

	// 0.3.0-edge.60 sorts before 0.3.0, the key's first release.
	for _, v := range []string{"0.2.9", "0.3.0-edge.60"} {
		e.signedHub(v, signedRelease(t, key, v, v, "amd64", archive), bin)
		_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: v, SHA256: sum(archive)})
		de := requireCode(t, err, deyerr.S011)
		require.Contains(t, de.Message(), "node.allow_unsigned_updates (since 0.3.0)", v)
		require.Contains(t, de.Why(), "this node", v)
		require.Contains(t, de.Fix(), "node.allow_unsigned_updates", v)
		e.requireNothingInstalled(t, "old binary", v)
	}

	// Not older than this node: allowed (it knows the key).
	e.signedHub("0.3.2", signedRelease(t, key, "0.3.2", "0.3.2", "amd64", archive), bin)
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "0.3.2"})
	require.NoError(t, err)
	e.requireRestart(t)

	// Without the key the same downgrade is not refused for it.
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	cfg.Node.AllowUnsignedUpdates = false
	require.NoError(t, config.Save(e.path(config.DefaultPath), cfg))
	e.signedHub("0.2.9", signedRelease(t, key, "0.2.9", "0.2.9", "amd64", archive), bin)
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "0.2.9"})
	require.NoError(t, err)
	e.requireRestart(t)
}

// An edge build of the release that brought the key is older than its
// first version (0.3.0) but a self.update to a newer edge build than the
// node's own is not refused.
func TestAllowUnsignedKeyNewerEdgeBuild(t *testing.T) {
	key := useTestKey(t)
	withVersion(t, "0.3.0-edge.60")
	e := newEnv(t)
	cfg := e.config()
	cfg.Node.AllowUnsignedUpdates = true
	require.NoError(t, config.Save(e.path(config.DefaultPath), cfg))
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	bin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	archive := makeArchive(t, []tarFile{{name: "deyroute", body: bin}})
	e.signedHub("0.3.0-edge.61", signedRelease(t, key, "0.3.0-edge.61", "0.3.0-edge.61", "amd64", archive), bin)
	s := e.start()
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "0.3.0-edge.61"})
	require.NoError(t, err)
	e.requireRestart(t)
	e.signedHub("0.3.0-edge.59", signedRelease(t, key, "0.3.0-edge.59", "0.3.0-edge.59", "amd64", archive), bin)
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "0.3.0-edge.59"})
	requireCode(t, err, deyerr.S011)
}
