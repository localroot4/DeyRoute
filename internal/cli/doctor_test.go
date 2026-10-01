package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/doctor"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/tui"
)

// bundleFiles lists the member names and contents of a doctor bundle.
func bundleFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		parts := strings.SplitN(h.Name, "/", 2)
		if len(parts) == 2 {
			out[parts[1]] = string(data)
		}
	}
	return out
}

func TestDoctorWithDaemon(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(hubConfig)
	// A tunnel token on disk is registered so the final scan catches it.
	secret := "Zm9vYmFyYmF6cXV1eHNlY3JldHRva2VudmFsdWUxMjM"
	secDir := filepath.Join(e.root, config.SecretsDir, "backend-tokens")
	require.NoError(t, os.MkdirAll(secDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(secDir, "main.token"), []byte(secret+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(e.root, config.SecretsDir, "ca.crt"), []byte("public cert"), 0o600))
	keysDir := filepath.Join(e.root, config.SecretsDir, "backend-keys", "main")
	require.NoError(t, os.MkdirAll(keysDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(keysDir, "rathole.json"),
		[]byte(`{"private_key":"cHJpdmF0ZWtleXZhbHVlZm9ydGVzdGluZ29ubHkxMjM0","created":"2026-09-30T12:00:00Z"}`), 0o600))

	var nodes []string
	e.stub.DoctorCollectFn = func(_ context.Context, node string) (api.DoctorData, error) {
		nodes = append(nodes, node)
		if node != "" {
			return api.DoctorData{Role: "node", Sections: map[string]string{"os": "Debian 12"},
				Findings: []api.DoctorFinding{{Rule: "R11", Severity: "info", Message: "BBR not active"}}}, nil
		}
		return api.DoctorData{Role: "hub", Sections: map[string]string{"os": "Ubuntu 24.04", "status": "ok", "logs/hub.log": "line token " + secret},
			Findings: []api.DoctorFinding{{Rule: "R01", Severity: "warn", Message: "Node de-1 is offline but tunnel main works", Fix: "check the control port"}}}, nil
	}
	e.stub.StatusFn = func(context.Context) (api.Status, error) { return sampleStatus(), nil }
	out := e.ok("doctor", "--node", "de-1")
	require.Equal(t, []string{"", "de-1"}, nodes)
	require.Contains(t, out, "Node de-1 is offline but tunnel main works")
	require.Contains(t, out, "de-1: BBR not active")
	require.Contains(t, out, "Support file: "+filepath.Join(e.root, "root", "deyroute-doctor-"))
	matches, err := filepath.Glob(filepath.Join(e.root, "root", "deyroute-doctor-*.tar.gz"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	files := bundleFiles(t, matches[0])
	require.Contains(t, files, "os.txt")
	require.Contains(t, files, "node-de-1/os.txt")
	require.Contains(t, files, "logs/hub.log")
	require.NotContains(t, files["logs/hub.log"], secret)
	require.Contains(t, files["logs/hub.log"], dlog.Mask)
	require.Equal(t, "***", dlog.Redact("cHJpdmF0ZWtleXZhbHVlZm9ydGVzdGluZ29ubHkxMjM0"))
	require.Equal(t, "2026-09-30T12:00:00Z", dlog.Redact("2026-09-30T12:00:00Z"))

	outFile := filepath.Join(e.root, "out", "d.tar.gz")
	require.NoError(t, os.MkdirAll(filepath.Dir(outFile), 0o700))
	doc := e.json("doctor", "--out", outFile)
	require.Equal(t, outFile, doc["path"])
	require.Equal(t, true, doc["daemon_running"])
	require.FileExists(t, outFile)

	// --node with an offline node is its error.
	e.stub.DoctorCollectFn = func(_ context.Context, node string) (api.DoctorData, error) {
		if node != "" {
			return api.DoctorData{}, deyerr.New(deyerr.N003, deyerr.Params{"node": node})
		}
		return api.DoctorData{Role: "hub"}, nil
	}
	require.Contains(t, e.fail(1, "doctor", "--node", "nl-1"), "DEY-N003")
	e.stub.DoctorCollectFn = func(context.Context, string) (api.DoctorData, error) {
		return api.DoctorData{}, deyerr.New(deyerr.X006, nil)
	}
	require.Contains(t, e.fail(2, "doctor"), "DEY-X006")
}

func TestDoctorWithoutDaemon(t *testing.T) {
	e := newEnv(t)
	e.writeConfig(strings.Replace(hubConfig, "mode: auto", "mode: custom\n      cert_file: /etc/ssl/main.pem\n      key_file: /etc/ssl/main.key", 1))
	e.down()
	e.g.Caps = func() tui.Caps { return tui.Caps{Width: 80} }
	out := e.ok("doctor")
	require.Contains(t, out, "The daemon is not running")
	require.Contains(t, out, "Support file:")
	for _, r := range out {
		require.Less(t, r, rune(128))
	}
	doc := e.json("doctor")
	require.Equal(t, false, doc["daemon_running"])
	require.Equal(t, "hub", doc["role"])
	files := bundleFiles(t, doc["path"].(string))
	require.Contains(t, files, doctor.SummaryFile)
	// --node needs the daemon.
	require.Contains(t, e.fail(2, "doctor", "--node", "de-1"), "DEY-X003")

	// A daemon without doctor support (DEY-X008): local collection.
	e2 := newEnv(t)
	e2.writeConfig(nodeConfig)
	doc = e2.json("doctor")
	require.Equal(t, true, doc["daemon_running"])
	require.Equal(t, "node", doc["role"])
}

func TestRegisterSecretsSkipsPublicFiles(t *testing.T) {
	require.True(t, publicSecretFile("ca.crt"))
	require.True(t, publicSecretFile("tls.p12"))
	require.True(t, publicSecretFile("tls.p12.cert-sha256"))
	require.True(t, publicSecretFile("join-tokens.json"))
	require.False(t, publicSecretFile("main.token"))
	require.False(t, publicSecretFile("key.pem"))
	root := t.TempDir()
	registerSecrets(root, nil) // no secrets directory: nothing happens
	tok := filepath.Join(root, "tg.token")
	require.NoError(t, os.WriteFile(tok, []byte("1234567:telegram-token-value-for-test\n"), 0o600))
	registerSecrets(root, &config.Config{Hub: &config.Hub{Notify: config.Notify{Telegram: config.Telegram{BotTokenFile: "/tg.token"}}}})
	require.Equal(t, "***", dlog.Redact("1234567:telegram-token-value-for-test"))
}
