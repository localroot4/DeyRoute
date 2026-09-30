package log

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testPEM = "-----BEGIN PRIVATE KEY-----\n" +
		"MC4CAQAwBQYDK2VwBCIEIFq3m1Nf0bQkqGx1b1QbJ9k0vYp0pY1yq4wP8j2vG7cU\n" +
		"-----END PRIVATE KEY-----"
	testECPEM = "-----BEGIN EC PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\n\nMHcCAQEEIIb0\nAwEHoUQDQgAE\n-----END EC PRIVATE KEY-----"
	testBot   = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw1"
	b64url43  = "q1W2e3R4t5Y6u7I8o9P0a_S-d1F2g3H4j5K6l7Z8x9C"
)

func TestRedactTable(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	cases := []struct {
		name, in, want string
	}{
		{"query token", "GET /x?token=abc123&x=1", "GET /x?token=***&x=1"},
		{"eq token", "token=abc123", "token=***"},
		{"eq spaced quoted", `private_key = "abc123"`, `private_key = "***"`},
		{"wireguard ini", "PrivateKey = aGVsbG8gd29ybGQ=", "PrivateKey = ***"},
		{"eq password", "password=hunter22 user=root", "password=*** user=root"},
		{"eq passphrase", "passphrase=correct-horse", "passphrase=***"},
		{"eq secret", "client_secret=s3cr3t;next", "client_secret=***;next"},
		{"eq key", "key=VALUE123", "key=***"},
		{"json token", `{"token":"abc","id":"main"}`, `{"token":"***","id":"main"}`},
		{"json password with space", `{"password": "p@ss w0rd"}`, `{"password": "***"}`},
		{"json default_token", `{"default_token":"abc"}`, `{"default_token":"***"}`},
		{"json privateKey", `{"privateKey":"abc"}`, `{"privateKey":"***"}`},
		{"escaped json", `{"msg":"{\"secret\":\"abc\"}"}`, `{"msg":"{\"secret\":\"***\"}"}`},
		{"json non secret", `{"tunnel":"main","port":443}`, `{"tunnel":"main","port":443}`},
		{"yaml password", "auth:\n  type: password\n  password: hunter22\n", "auth:\n  type: password\n  password: ***\n"},
		{"yaml token quoted", `bot_token: "abcdef"`, `bot_token: "***"`},
		{"yaml token file kept", "bot_token_file: /etc/deyroute/secrets/telegram.token", "bot_token_file: /etc/deyroute/secrets/telegram.token"},
		{"toml token", `token = "abc123"`, `token = "***"`},
		{"auth header", "Authorization: Bearer abc.def.ghi", "Authorization: Bearer ***"},
		{"auth json", `{"Authorization":"Bearer abc.def"}`, `{"Authorization":"Bearer ***"}`},
		{"auth go map", "map[Authorization:[Bearer xyz123]]", "map[Authorization:[Bearer ***]]"},
		{"auth basic", "authorization: Basic dXNlcjpwYXNz", "authorization: Basic ***"},
		{"bearer alone", "sent bearer abcdef123", "sent bearer ***"},
		{"telegram url", "POST https://api.telegram.org/bot" + testBot + "/sendMessage", "POST https://api.telegram.org/bot***/sendMessage"},
		{"telegram bare", "bot token " + testBot + " loaded", "bot token *** loaded"},
		{"join link", "run: deyroute node join dey://" + b64url43 + "@5.6.7.8:44433#sha256:abcd", "run: deyroute node join dey://***@5.6.7.8:44433#sha256:abcd"},
		{"pem", "key:\n" + testPEM + "\nafter", "key:\n***\nafter"},
		{"pem ec encrypted", testECPEM + " tail", "*** tail"},
		{"pem escaped newlines", `"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"`, `"***"`},
		{"pem truncated", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n", "***"},
		{"age identity", "identity AGE-SECRET-KEY-1QYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQSZQGPQYQS end", "identity *** end"},
		{"after word token", "using token " + b64url43, "using token ***"},
		{"after word key quoted", "public key: '" + b64url43 + "'", "public key: '***'"},
		{"flag", "rathole --token abcdef --port 1", "rathole --token *** --port 1"},
		{"flag next flag kept", "cmd --password --verbose", "cmd --password --verbose"},
		{"plain text kept", "tunnel main up on de-1 via backhaul/wssmux port 443/tcp", "tunnel main up on de-1 via backhaul/wssmux port 443/tcp"},
		{"fingerprint kept", "CA sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "CA sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"timestamp kept", "at 2026-09-29T12:00:00.123Z rtt=42ms", "at 2026-09-29T12:00:00.123Z rtt=42ms"},
		{"short key word kept", "key rotation scheduled for main", "key rotation scheduled for main"},
		{"public public_key json", `{"public_key":"abc"}`, `{"public_key":"***"}`},
		{"empty", "", ""},
		// Regressions: values with spaces or escaped quotes used to leak
		// everything after the first space / quote.
		{"eq double quoted spaces", `password="p w0rd x" next=1`, `password="***" next=1`},
		{"eq single quoted spaces", `secret='a b c'`, `secret='***'`},
		{"eq escaped quotes in json text", `{"msg":"password=\"a b\" end"}`, `{"msg":"password=\"***\" end"}`},
		{"eq unterminated quote", `token="abc123`, `token="***`},
		{"yaml quoted spaces", `password: "p w0rd x"`, `password: "***"`},
		{"yaml plain spaces", "  passphrase: correct horse battery # note\nnext: 1", "  passphrase: ***\nnext: 1"},
		{"json escaped quote", `{"password":"ab\"cdef","x":1}`, `{"password":"***","x":1}`},
		{"json number", `{"password": 1234, "port": 443}`, `{"password": "***", "port": 443}`},
		{"json unterminated", `{"token":"abcdef`, `{"token":"***"`},
		{"escaped json embedded quote", `{"m":"{\"secret\":\"a\\\"b\",\"x\":1}"}`, `{"m":"{\"secret\":\"***\",\"x\":1}"}`},
		{"escaped json backslash end", `{"m":"{\"secret\":\"a\\\\\",\"x\":1}"}`, `{"m":"{\"secret\":\"***\",\"x\":1}"}`},
		{"join link without host", "bad link dey://" + b64url43, "bad link dey://***"},
		{"url userinfo", "fetch https://user:hunter22@mirror.example/x failed", "fetch https://user:***@mirror.example/x failed"},
		{"url without password kept", "https://user@mirror.example/x", "https://user@mirror.example/x"},
		{"url password with at", "socks5://u:p@ss@proxy:1080/x?mail=a@b.c", "socks5://u:***@proxy:1080/x?mail=a@b.c"},
		{"url host port only kept", "https://mirror.example:8443/a@b", "https://mirror.example:8443/a@b"},
		{"flag auth", "chisel client --auth user:pass hub:443", "chisel client --auth *** hub:443"},
		{"flag quoted", `cmd --password "a b" --x`, `cmd --password "***" --x`},
		{"pgp block", "k -----BEGIN PGP PRIVATE KEY BLOCK-----\nabc\n-----END PGP PRIVATE KEY BLOCK----- t", "k *** t"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			require.Equal(t, tc.want, got)
			require.Equal(t, got, Redact(got), "idempotent")
		})
	}
}

func TestRegisterSecret(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	require.Equal(t, "abc and abcdef", Redact("abc and abcdef"))
	RegisterSecret("abc")        // too short: ignored
	RegisterSecret("abcdef\n")   // trimmed variant registered too
	RegisterSecret("abcdefghij") // contains the shorter one: masked whole
	RegisterSecret("abcdefghij") // duplicate: no-op
	RegisterSecret("")
	require.Equal(t, "abc and *** and ***!", Redact("abc and abcdef and abcdefghij!"))
	require.Equal(t, "x***y", Redact("xabcdefghijy"))
}

func TestSensitiveKey(t *testing.T) {
	for k, want := range map[string]bool{
		"token": true, "join_token": true, "Password": true, "passphrase": true, "secret": true,
		"key": true, "private_key": true, "privateKey": true, "psk": true, "Authorization": true, "api_key": true,
		"token_file": false, "bot_token_file": false, "key_path": false, "secret_id": false, "tunnel": false,
		"err": false, "code": false, "keys_count": false, "fingerprint": false, "monkey": false, "token_len": false,
	} {
		require.Equal(t, want, sensitiveKey(k), k)
	}
}

func TestRedactingWriterChunkedSecrets(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	RegisterSecret(b64url43)
	var out bytes.Buffer
	w := RedactingWriter(&out)
	text := "line one " + b64url43 + " end\n" + testPEM + "\nnext\n" + "token=abcdef\nlast without newline " + b64url43
	// Write one byte at a time: secrets straddle every possible boundary.
	for i := 0; i < len(text); i++ {
		n, err := w.Write([]byte{text[i]})
		require.NoError(t, err)
		require.Equal(t, 1, n)
	}
	require.NotContains(t, out.String(), "last without newline", "partial line is buffered")
	require.NoError(t, w.Close())
	got := out.String()
	require.Equal(t, "line one *** end\n***\nnext\ntoken=***\nlast without newline ***", got)
	require.NoError(t, w.Flush(), "flush with nothing pending")
}

func TestRedactingWriterPEMCases(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"prefix and suffix", "k=" + "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY----- tail\nok\n", "k=*** tail\nok\n"},
		{"two blocks one line", `a -----BEGIN PRIVATE KEY-----\nA\n-----END PRIVATE KEY----- b -----BEGIN PRIVATE KEY-----\nB\n-----END PRIVATE KEY----- c` + "\n", "a *** b *** c\n"},
		{"truncated then text", "-----BEGIN PRIVATE KEY-----\nAAAA\nBBBB\nnormal log line here\n", "***\nnormal log line here\n"},
		{"encrypted headers", testECPEM + "\nafter\n", "***\nafter\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			w := RedactingWriter(&out)
			_, err := w.Write([]byte(tc.in))
			require.NoError(t, err)
			require.NoError(t, w.Flush())
			require.Equal(t, tc.want, out.String())
		})
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestRedactingWriterErrorsAndLongLines(t *testing.T) {
	w := RedactingWriter(failWriter{})
	_, err := w.Write([]byte("a line\n"))
	require.Error(t, err)
	_, err = w.Write([]byte("partial"))
	require.NoError(t, err, "nothing written yet")
	require.Error(t, w.Flush())

	var out bytes.Buffer
	w = RedactingWriter(&out)
	long := strings.Repeat("x", maxPending+10)
	_, err = w.Write([]byte(long))
	require.NoError(t, err)
	require.Equal(t, len(long), out.Len(), "overlong partial line is flushed")
}

// io.Copy into a RedactingWriter (the doctor's way of copying a file) must
// emit the final line without an explicit Flush, and a truncated key block
// must not swallow the next stream.
func TestRedactingWriterIOCopyFlushesTail(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	const text = "a token=abc123\nlast line password=xyz"

	// From a file (os.File falls back to io.Copy → ReadFrom for a non-socket destination).
	path := filepath.Join(t.TempDir(), "hub.log")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var out bytes.Buffer
	n, err := io.Copy(RedactingWriter(&out), f)
	require.NoError(t, err)
	require.EqualValues(t, len(text), n)
	require.Equal(t, "a token=***\nlast line password=***", out.String())

	// From a plain reader.
	out.Reset()
	w := RedactingWriter(&out)
	_, err = io.Copy(w, plainReader{strings.NewReader(text)})
	require.NoError(t, err)
	require.Equal(t, "a token=***\nlast line password=***", out.String())

	out.Reset()
	_, err = io.Copy(w, plainReader{strings.NewReader("-----BEGIN PRIVATE KEY-----\nAAAA")})
	require.NoError(t, err)
	_, err = io.Copy(w, plainReader{strings.NewReader("BBBB\n")})
	require.NoError(t, err)
	require.Equal(t, "***BBBB\n", out.String(), "a new stream starts outside the key block")

	_, err = io.Copy(RedactingWriter(failWriter{}), plainReader{strings.NewReader("x\n")})
	require.Error(t, err)
	_, err = RedactingWriter(&out).ReadFrom(errReader{})
	require.Error(t, err)
}

// plainReader hides io.WriterTo so io.Copy uses the destination's ReadFrom.
type plainReader struct{ io.Reader }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestSecretFilter(t *testing.T) {
	resetSecrets()
	t.Cleanup(resetSecrets)
	var out bytes.Buffer
	f := secretFilter{w: &out}
	n, err := f.Write([]byte("plain abcdefgh\n"))
	require.NoError(t, err)
	require.Equal(t, 15, n)
	RegisterSecret("abcdefgh")
	n, err = f.Write([]byte("plain abcdefgh\n"))
	require.NoError(t, err)
	require.Equal(t, 15, n)
	require.Equal(t, "plain abcdefgh\nplain ***\n", out.String())
	_, err = secretFilter{w: failWriter{}}.Write([]byte("abcdefgh"))
	require.Error(t, err)
}
