package log

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// lines parses every JSON line of the live log file.
func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), "line %q", sc.Text())
		out = append(out, m)
	}
	return out
}

func newFileLogger(t *testing.T, opt Options) (*slog.Logger, string) {
	t.Helper()
	if opt.File == "" {
		opt.File = filepath.Join(t.TempDir(), "log", "hub.log")
	}
	l, c, err := New(opt)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return l, opt.File
}

func TestJSONFieldNames(t *testing.T) {
	t.Setenv(DebugEnv, "")
	l, path := newFileLogger(t, Options{Component: "hub"})
	l.Info("switched", Tunnel("main"), Node("de-1"), Transport("backhaul/wssmux"),
		Code(deyerr.P012), Err(errors.New("boom")), slog.Int("rtt_ms", 42),
		slog.Time("since", time.Date(2026, 9, 29, 15, 30, 0, 0, time.FixedZone("IRST", 12600))))
	l.Debug("hidden at info level")

	lines := readLines(t, path)
	require.Len(t, lines, 1)
	m := lines[0]
	for _, k := range []string{KeyTS, KeyLevel, KeyComponent, KeyTunnel, KeyNode, KeyTransport, KeyCode, KeyMsg, KeyErr} {
		require.Contains(t, m, k)
	}
	require.NotContains(t, m, "time")
	require.Equal(t, "info", m[KeyLevel])
	require.Equal(t, "hub", m[KeyComponent])
	require.Equal(t, "switched", m[KeyMsg])
	require.Equal(t, "DEY-P012", m[KeyCode])
	require.Equal(t, "boom", m[KeyErr])
	require.EqualValues(t, 42, m["rtt_ms"])
	require.Equal(t, "2026-09-29T12:00:00Z", m["since"], "time attributes are UTC")
	ts, ok := m[KeyTS].(string)
	require.True(t, ok)
	require.True(t, strings.HasSuffix(ts, "Z"), "UTC: %s", ts)
	parsed, err := time.Parse(time.RFC3339, ts)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), parsed, time.Minute)

	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())
	dst, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, DirMode, dst.Mode().Perm())
}

func TestLevelNames(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewHandler(&buf, slog.LevelDebug-8, ""))
	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")
	l.Log(t.Context(), slog.LevelError+4, "fatal-ish")
	l.Log(t.Context(), slog.LevelDebug-4, "trace")
	var got []string
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m))
		got = append(got, m[KeyLevel].(string))
		require.Equal(t, DefaultComponent, m[KeyComponent])
	}
	require.Equal(t, []string{"debug", "info", "warn", "error", "error", "debug"}, got)
}

func TestComponentOverrideAndGroups(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(NewHandler(&buf, slog.LevelInfo, "hub"))
	base.With("component", "failover", Tunnel("main")).Info("a")
	base.Info("b", Component("api"))
	base.With(slog.Group("req", slog.String("path", "/v1"))).WithGroup("g").With("component", "inner").Info("c", "x", 1)
	base.With("k1", "v1").With(Component("notify")).Info("d")
	base.With().WithGroup("").Info("e")

	raw := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, raw, 5)
	for _, line := range raw[:2] {
		require.Equal(t, 1, strings.Count(line, `"component"`), line)
	}
	var m []map[string]any
	for _, line := range raw {
		var x map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &x))
		m = append(m, x)
	}
	require.Equal(t, "failover", m[0][KeyComponent])
	require.Equal(t, "main", m[0][KeyTunnel])
	require.Equal(t, "api", m[1][KeyComponent])
	require.Equal(t, "hub", m[2][KeyComponent], "component inside a group is an ordinary field")
	g, ok := m[2]["g"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "inner", g["component"])
	require.EqualValues(t, 1, g["x"])
	require.Equal(t, "/v1", m[2]["req"].(map[string]any)["path"])
	require.Equal(t, "notify", m[3][KeyComponent])
	require.Equal(t, "v1", m[3]["k1"])
	require.Equal(t, "hub", m[4][KeyComponent])
}

func TestDebugViaEnvMirrorsToStderr(t *testing.T) {
	t.Setenv(DebugEnv, "1")
	require.True(t, DebugFromEnv())
	var stderr bytes.Buffer
	l, path := newFileLogger(t, Options{Component: "node", StderrWriter: &stderr})
	l.Debug("probe detail", Tunnel("main"))
	lines := readLines(t, path)
	require.Len(t, lines, 1)
	require.Equal(t, "debug", lines[0][KeyLevel])
	require.Contains(t, stderr.String(), `"msg":"probe detail"`)
}

func TestNoDebugNoMirror(t *testing.T) {
	t.Setenv(DebugEnv, "0")
	require.False(t, DebugFromEnv())
	var stderr bytes.Buffer
	l, path := newFileLogger(t, Options{StderrWriter: &stderr})
	l.Debug("dropped")
	l.Info("kept")
	require.Len(t, readLines(t, path), 1)
	require.Empty(t, stderr.String())

	// Explicit Stderr mirrors without debug; Debug option works without env.
	var se2 bytes.Buffer
	l2, _ := newFileLogger(t, Options{Stderr: true, Debug: true, StderrWriter: &se2})
	l2.Debug("dbg")
	require.Contains(t, se2.String(), `"level":"debug"`)
}

func TestNewWithoutFileWritesStderr(t *testing.T) {
	t.Setenv(DebugEnv, "")
	var se bytes.Buffer
	l, c, err := New(Options{Component: "cli", StderrWriter: &se})
	require.NoError(t, err)
	l.Warn("careful")
	require.NoError(t, c.Close())
	require.Contains(t, se.String(), `"level":"warn"`)
	require.Contains(t, se.String(), `"component":"cli"`)
}

func TestNewBadPath(t *testing.T) {
	f := filepath.Join(t.TempDir(), "plainfile")
	require.NoError(t, os.WriteFile(f, nil, 0o600))
	_, _, err := New(Options{File: filepath.Join(f, "sub", "x.log")})
	require.True(t, deyerr.HasCode(err, deyerr.X022), "got %v", err)
}

func TestFanoutKeepsWritingAfterSinkError(t *testing.T) {
	var ok bytes.Buffer
	f := fanout{failWriter{}, &ok}
	_, err := f.Write([]byte("x\n"))
	require.Error(t, err)
	require.Equal(t, "x\n", ok.String())
	n, err := fanout{&ok}.Write([]byte("y"))
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestHelpers(t *testing.T) {
	require.Equal(t, slog.Attr{}, Err(nil))
	require.Nil(t, ErrAttrs(nil))
	plain := ErrAttrs(errors.New("x"))
	require.Len(t, plain, 1)
	dey := ErrAttrs(fmt.Errorf("wrapped: %w", deyerr.New(deyerr.X001, deyerr.Params{"path": "/p"})))
	require.Len(t, dey, 2)
	require.Equal(t, "DEY-X001", dey[0].(slog.Attr).Value.String())
	Discard().Error("nothing happens")
	require.False(t, Discard().Enabled(t.Context(), slog.LevelError))
}

type panicky struct{}

func (panicky) String() string { panic("boom") }

type ptrStringer struct{ s string }

func (p *ptrStringer) String() string { return p.s }

// A nil pointer inside a non-nil error or Stringer must not crash the
// process through the logger (slog guards its own values; our ReplaceAttr
// and helpers run before that guard).
func TestTypedNilValuesDoNotPanic(t *testing.T) {
	var de *deyerr.Error
	var err error = de
	var ps *ptrStringer
	var buf bytes.Buffer
	l := slog.New(NewHandler(&buf, slog.LevelInfo, "hub"))
	require.NotPanics(t, func() {
		l.Error("a", Err(err))
		l.Error("b", slog.Any("e", err), slog.Any("s", ps), slog.Any("p", panicky{}))
		l.Error("c", ErrAttrs(err)...)
	})
	out := buf.String()
	require.Equal(t, 3, strings.Count(out, `"err":"<nil>"`)+strings.Count(out, `"e":"<nil>"`), out)
	require.Contains(t, out, `"s":"<nil>"`)
	require.Contains(t, out, `"p":"!PANIC: boom"`)
	require.NotContains(t, out, `"code"`, "a nil DEY error has no code")
}

type stringer struct{ s string }

func (s stringer) String() string { return s.s }

type valuer struct{ s string }

func (v valuer) LogValue() slog.Value { return slog.StringValue(v.s) }

// S26: a registered token must never reach the log file, whatever path it
// takes through the logger — message, attribute, error, nested group,
// preformatted With attribute (registered afterwards), LogValuer, maps,
// byte slices, Stringers — and also not the rotated gzip files.
func TestS26SecretsNeverReachDisk(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	t.Setenv(DebugEnv, "")
	const tok = "Zm9vYmFyYmF6cXV4LXNlY3JldC10b2tlbi0xMjM0NTY" // 43-char base64url
	const (
		other        = "unregistered-but-named-token-value"
		spaced       = "pa55 w0rd tail"
		urlPass      = "mirror-pass-42"
		unregistered = "group-member-secret"
	)
	dir := t.TempDir()
	path := filepath.Join(dir, "deyroute.log")
	l, c, err := New(Options{Component: "hub", File: path, MaxBytes: 2048, Keep: 3})
	require.NoError(t, err)

	early := l.With(slog.String("detail", "prefix "+tok)) // preformatted before registration
	RegisterSecret(tok)

	for i := 0; i < 20; i++ {
		l.Info("join token is "+tok, Tunnel("main"))
		l.Warn("attr", slog.String("value", tok), slog.String("token", other))
		l.Error("failed", Err(fmt.Errorf("handshake with %s failed", tok)), slog.Any("err2", errors.New("x "+tok)))
		l.Info("nested", slog.Group("outer", slog.Group("inner", slog.String("v", "a "+tok+" b"))))
		early.Info("preformatted")
		l.Info("valuer", slog.Any("lv", valuer{tok}))
		l.Info("map", slog.Any("m", map[string]string{"x": tok, "password": "pw-" + tok}))
		l.Info("bytes", slog.Any("b", []byte("raw "+tok)))
		l.Info("stringer", slog.Any("s", stringer{"s " + tok}))
		l.WithGroup("grp").Info("group", slog.String("deep", tok))
		l.Info("patterns", slog.String("url", "https://api.telegram.org/bot"+testBot+"/send"), slog.String("link", "dey://"+b64url43+"@1.2.3.4:44433#sha256:ab"))
		l.Info(`backend said password="`+spaced+`" and dey://`+b64url43, slog.String("mirror", "https://u:"+urlPass+"@m.example/x"))
		l.Info("groups", slog.Group("secrets", slog.String("hub", unregistered)), slog.Any("cfg", map[string]any{"private_key": map[string]string{"k": unregistered}}))
	}
	require.NoError(t, c.Close())

	files, err := filepath.Glob(path + "*")
	require.NoError(t, err)
	require.Contains(t, files, path+".1.gz", "rotation happened")
	var all bytes.Buffer
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		if strings.HasSuffix(f, ".gz") {
			zr, err := gzip.NewReader(bytes.NewReader(data))
			require.NoError(t, err)
			data, err = io.ReadAll(zr)
			require.NoError(t, err)
			require.NoError(t, zr.Close())
		}
		all.Write(data)
	}
	text := all.String()
	require.NotEmpty(t, text)
	for _, secret := range []string{tok, other, testBot, b64url43, "w0rd tail", urlPass, unregistered} {
		require.NotContains(t, text, secret)
	}
	require.Contains(t, text, "***")
	require.Contains(t, text, "dey://***@1.2.3.4:44433")
	// Every line is still valid JSON after redaction.
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		require.True(t, json.Valid(sc.Bytes()), sc.Text())
	}
}

func TestRedactAnyFallbacks(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	RegisterSecret("abcdefgh")
	require.Equal(t, slog.KindAny, redactAny(nil).Kind())
	// Unmarshalable value falls back to %+v.
	v := redactAny(func() {})
	require.Equal(t, slog.KindString, v.Kind())
	// Registered secrets inside struct fields are masked in the JSON form.
	type weird struct{ A string }
	v = redactAny(weird{A: "abcdefgh"})
	require.Equal(t, slog.KindAny, v.Kind())
	require.Equal(t, `{"A":"***"}`, string(v.Any().(json.RawMessage)))
	// Sensitive key with an empty value stays empty; numbers are masked;
	// booleans, durations and times cannot carry a secret and are kept.
	require.Equal(t, "", redactAttr(slog.String("token", ""), false).Value.String())
	require.Equal(t, Mask, redactAttr(slog.Int("password", 1234), false).Value.String())
	require.Equal(t, int64(5), redactAttr(slog.Int("n", 5), false).Value.Int64())
	require.True(t, redactAttr(slog.Bool("has_token", true), false).Value.Bool())
	require.Equal(t, time.Second, redactAttr(slog.Duration("token_age", time.Second), false).Value.Duration())
	require.Equal(t, Mask, redactAttr(slog.String("value", "plain"), true).Value.String(), "inside a secret group")
	g := slog.Group("g", slog.String("token", "x"))
	require.Equal(t, g, redactAttr(g, false))
}

// Structured values are redacted on their raw strings, member order is kept
// and members with secret names are masked whatever their JSON type.
func TestRedactJSONStructured(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	type inner struct {
		Password string `json:"password"`
		PIN      int    `json:"pin_token"`
		Note     string `json:"note"`
	}
	type cfg struct {
		Zeta     string            `json:"zeta"`
		Alpha    []string          `json:"alpha"`
		Secrets  map[string]string `json:"secrets"`
		Inner    inner             `json:"inner"`
		HasToken bool              `json:"has_token"`
		KeyFile  string            `json:"key_file"`
		Nothing  *string           `json:"private_key"`
		Nums     []float64         `json:"nums"`
	}
	v := redactAny(cfg{
		Zeta:     "z",
		Alpha:    []string{"--password", "p w", `password="a b"`, "dey://" + b64url43 + "@1.2.3.4:1#fp"},
		Secrets:  map[string]string{"a": "b"},
		Inner:    inner{Password: `quote"inside`, PIN: 1234, Note: "token=abc123"},
		HasToken: true,
		KeyFile:  "/etc/deyroute/secrets/hub.key",
		Nums:     []float64{1.5, -2},
	})
	got := string(v.Any().(json.RawMessage))
	require.True(t, json.Valid([]byte(got)), got)
	require.Equal(t, `{"zeta":"z","alpha":["--password","p w","password=\"***\"","dey://***@1.2.3.4:1#fp"],`+
		`"secrets":"***","inner":{"password":"***","pin_token":"***","note":"token=***"},"has_token":true,`+
		`"key_file":"/etc/deyroute/secrets/hub.key","private_key":null,"nums":[1.5,-2]}`, got)

	// Map keys are redacted too (a token used as a map key).
	RegisterSecret(b64url43)
	v = redactAny(map[string]int{b64url43: 1})
	require.Equal(t, `{"***":1}`, string(v.Any().(json.RawMessage)))

	for _, bad := range []string{`{"a":`, `{"token":`, `{"token":[1`, ``, `[1,`} {
		_, err := redactJSON([]byte(bad))
		require.Error(t, err, bad)
	}
	out, err := redactJSON([]byte(`[[],{},{"token":{"x":[1,{"y":2}]},"k":"v"}]`))
	require.NoError(t, err)
	require.Equal(t, `[[],{},{"token":"***","k":"v"}]`, string(out))
}

// A secret registered after a With attribute was preformatted is masked
// even when JSON escaping changed its spelling in the encoded line.
func TestLateRegisteredSecretWithJSONEscapes(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	var buf bytes.Buffer
	l := slog.New(NewHandler(&buf, slog.LevelInfo, "hub"))
	const secret = `pa"ss\word-value`
	early := l.With(slog.String("v", "pre "+secret+" x"))
	RegisterSecret(secret)
	early.Info("m")
	l.Info("g", slog.Group("secrets", slog.String("value", "plainvalue")), slog.Int("component", 7))
	out := buf.String()
	require.NotContains(t, out, `ss\\word`)
	require.NotContains(t, out, "plainvalue")
	require.Contains(t, out, `"v":"pre *** x"`)
	require.Contains(t, out, `"secrets":{"value":"***"}`)
	require.Equal(t, 2, strings.Count(out, `"component"`), "non-string component replaces the field")
	require.Contains(t, out, `"component":"7"`)
}
