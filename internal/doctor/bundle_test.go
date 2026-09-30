package doctor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

const fakeKey = "-----BEGIN PRIVATE KEY-----\n" +
	"MC4CAQAwBQYDK2VwBCIEIJ+DYvh6SEqVTm50DFtMDoQikTmiCqirVv9mWG9qfSnF\n" +
	"-----END PRIVATE KEY-----\n"

// readBundle returns every member of a bundle.
func readBundle(t *testing.T, p string) map[string]string {
	t.Helper()
	f, err := os.Open(p)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.Equal(t, int64(0o600), hdr.Mode, hdr.Name)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[hdr.Name] = string(data)
	}
	return out
}

func TestWriteBundleRedactsEveryMember(t *testing.T) {
	secret := "tunnel-token-Zq9vX2mK7pL4"
	dlog.RegisterSecret(secret)
	dir := t.TempDir()
	now := time.Date(2026, 9, 30, 10, 11, 12, 0, time.FixedZone("IRST", 3*3600+1800))

	parts := SectionParts(map[string]string{
		"os":                    "os: Ubuntu\n",
		"logs/hub.log":          `{"msg":"joined","token":"` + secret + `"}` + "\nplain " + secret + " line\n" + fakeKey + "after key",
		"logs/tunnels/main.log": "password = hunter2hunter2\nlink dey://" + secret + "@1.2.3.4:44433#sha256:ab\n",
	}, "")
	for k, v := range SectionParts(map[string]string{"os": "node os " + secret + "\n"}, NodePrefix("de-1")) {
		parts[k] = v
	}
	parts["findings.txt"] = []byte("overridden by the generated file")
	findings := []api.DoctorFinding{{Rule: "R05", Severity: SevWarn, Message: "conflict " + secret, Fix: "fix it"}}
	summary := "\x1b[1;31m✖ ERROR\x1b[0m R05 token=" + secret + "\n"

	p, err := WriteBundle(dir, now, parts, findings, summary)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "deyroute-doctor-20260930T064112Z.tar.gz"), p)
	st, err := os.Stat(p)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	members := readBundle(t, p)
	top := "deyroute-doctor-20260930T064112Z/"
	for _, name := range []string{"os.txt", "logs/hub.log", "logs/tunnels/main.log", "node-de-1/os.txt", FindingsFile, SummaryFile} {
		require.Contains(t, members, top+name)
	}
	require.Len(t, members, 6)
	for name, data := range members {
		require.NotContains(t, data, secret, name)
		require.NotContains(t, data, "PRIVATE KEY", name)
		require.NotContains(t, data, "MC4CAQAw", name)
		require.NotContains(t, data, "hunter2", name)
		require.NotContains(t, data, "\x1b[", name)
	}
	require.Contains(t, members[top+"logs/hub.log"], "after key")
	require.Contains(t, members[top+"logs/tunnels/main.log"], "dey://***@1.2.3.4:44433")
	require.Contains(t, members[top+FindingsFile], "WARN R05 conflict ***")
	require.Contains(t, members[top+FindingsFile], "Fix: fix it")
	require.Contains(t, members[top+SummaryFile], "ERROR")

	// No temp files are left behind.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestWriteBundleEmptyFindings(t *testing.T) {
	p, err := WriteBundle(t.TempDir(), testNow, nil, nil, "")
	require.NoError(t, err)
	m := readBundle(t, p)
	require.Contains(t, m["deyroute-doctor-20260930T100000Z/"+FindingsFile], "No problems found")
}

func TestWriteBundleRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../etc/passwd", "a/../../b", "", "/", "x\x00y"} {
		_, err := WriteBundle(dir, testNow, map[string][]byte{name: []byte("x")}, nil, "")
		require.Error(t, err, name)
		require.True(t, deyerr.HasCode(err, deyerr.X000), name)
	}
	clean, ok := cleanPartName(`/abs\win/./file.txt`)
	require.True(t, ok)
	require.Equal(t, "abs/win/file.txt", clean)
}

func TestWriteBundleWriteError(t *testing.T) {
	_, err := WriteBundle(filepath.Join(t.TempDir(), "missing"), testNow, nil, nil, "")
	require.True(t, deyerr.HasCode(err, deyerr.X032))
}

func TestVerifyArchiveRefusesSecrets(t *testing.T) {
	secret := "verify-secret-Hq7Lm2Np"
	dlog.RegisterSecret(secret)
	for name, content := range map[string]string{
		"key.txt":    "x\n" + fakeKey,
		"secret.txt": "value " + secret + "\n",
		"token.txt":  "token=abcdefgh\n",
	} {
		archive, err := buildArchive("top", testNow, map[string][]byte{name: []byte(content)})
		require.NoError(t, err)
		err = verifyArchive(archive)
		require.True(t, deyerr.HasCode(err, deyerr.X060), name)
		require.Contains(t, deyerr.As(err).Message(), name)
	}
	archive, err := buildArchive("top", testNow, map[string][]byte{"token=" + secret: []byte("ok\n")})
	require.NoError(t, err)
	require.True(t, deyerr.HasCode(verifyArchive(archive), deyerr.X060))

	archive, err = buildArchive("top", testNow, map[string][]byte{"ok.txt": []byte("token=***\nall good\n")})
	require.NoError(t, err)
	require.NoError(t, verifyArchive(archive))

	require.Error(t, verifyArchive([]byte("not gzip")))
}

func TestSectionPartsAndNames(t *testing.T) {
	parts := SectionParts(map[string]string{"os": "a", "logs/tunnels/x.log": "b", "status": "c"}, "node-nl-1/")
	require.Equal(t, map[string][]byte{
		"node-nl-1/os.txt": []byte("a"), "node-nl-1/logs/tunnels/x.log": []byte("b"), "node-nl-1/status.txt": []byte("c"),
	}, parts)
	require.Equal(t, "deyroute-doctor-20260930T100000Z.tar.gz", BundleName(testNow))
	require.Equal(t, "node-de-1/", NodePrefix("de-1"))
}

func TestStatusAndEventsSections(t *testing.T) {
	secret := "status-secret-Pp0Qq1Rr2"
	dlog.RegisterSecret(secret)
	s := StatusSection(api.Status{Role: "hub", Version: "1.0.0", Warnings: []api.Warning{{Message: secret}}})
	require.Contains(t, s, `"role": "hub"`)
	require.NotContains(t, s, secret)

	require.Equal(t, "no events\n", EventsSection(nil))
	var evs []state.Event
	for i := 0; i < EventCount+10; i++ {
		evs = append(evs, state.Event{
			At: testNow, Level: state.LevelWarn, Type: state.EvSwitchTransport, Tunnel: "main", Node: "de-1",
			FromTransport: "backhaul/tcpmux", ToTransport: "backhaul/wssmux", FromNode: "de-1", ToNode: "nl-1",
			Code: "DEY-F001", Message: "switched\nnow", Reason: "probe " + secret,
		})
	}
	out := EventsSection(evs)
	ls := lines(out)
	require.Len(t, ls, EventCount)
	require.Contains(t, ls[0], "2026-09-30T10:00:00Z  warn  switch_transport  tunnel=main  node=de-1  transport=backhaul/tcpmux->backhaul/wssmux  switch_node=de-1->nl-1  DEY-F001  switched now (probe ***)")
	require.False(t, bytes.Contains([]byte(out), []byte(secret)))
	require.True(t, strings.HasSuffix(out, "\n"))
}
