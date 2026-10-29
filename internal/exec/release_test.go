package exec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// releaseScript writes a stand-in deyroute binary (a shell script) into a
// directory of its own and returns its path.
func releaseScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ReleaseBinaryName)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755))
	return p
}

// ReleaseVersion runs a binary named deyroute with `--json version` only,
// in its own directory, without the caller's environment, and gives up on
// a binary that fails, hangs or prints too much.
func TestReleaseVersion(t *testing.T) {
	t.Setenv("DEYROUTE_AGENT_SECRET", "s3cret")
	ctx := context.Background()

	bin := releaseScript(t, `printf '{"args":"%s","pwd":"%s","secret":"%s","lc":"%s"}' "$*" "$(pwd)" "$DEYROUTE_AGENT_SECRET" "$LC_ALL"`)
	out, err := ReleaseVersion(ctx, bin)
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, map[string]string{"args": "--json version", "pwd": filepath.Dir(bin), "secret": "", "lc": "C"}, got)

	_, err = ReleaseVersion(ctx, releaseScript(t, "echo '{}'; echo broken >&2; exit 3\n"))
	de := requireCode(t, err, deyerr.X007)
	require.Contains(t, err.Error(), "exit status 3")
	require.Equal(t, "broken", de.Detail)

	_, err = ReleaseVersion(ctx, releaseScript(t, "head -c 70000 /dev/zero | tr '\\0' x\n"))
	requireCode(t, err, deyerr.X007)
	require.ErrorContains(t, err, "more than 65536 bytes of output")

	old := releaseVersionTimeout
	releaseVersionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { releaseVersionTimeout = old })
	start := time.Now()
	_, err = ReleaseVersion(ctx, releaseScript(t, "exec sleep 30\n"))
	requireCode(t, err, deyerr.X031)
	require.Less(t, time.Since(start), 10*time.Second)

	_, err = ReleaseVersion(ctx, filepath.Join(t.TempDir(), ReleaseBinaryName))
	requireCode(t, err, deyerr.X030)
}

// Nothing but an absolute, clean path to a file named deyroute runs.
func TestReleaseVersionRunsOnlyDeyroute(t *testing.T) {
	sh := releaseScript(t, "echo '{}'\n")
	other := filepath.Join(filepath.Dir(sh), "sh")
	require.NoError(t, os.Link(sh, other))
	for _, p := range []string{other, "deyroute", "./deyroute", filepath.Dir(sh) + "/../" + filepath.Base(filepath.Dir(sh)) + "/deyroute", ""} {
		_, err := ReleaseVersion(context.Background(), p)
		requireCode(t, err, deyerr.X004)
	}
}

func requireCode(t *testing.T, err error, code deyerr.Code) *deyerr.Error {
	t.Helper()
	require.Error(t, err)
	require.True(t, deyerr.HasCode(err, code), "want %s, got %v", code, err)
	return deyerr.As(err)
}
