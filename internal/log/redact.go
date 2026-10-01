package log

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Mask replaces every redacted value (sections 4 and 11).
const Mask = "***"

// MinSecretLen is the shortest value RegisterSecret accepts; shorter values
// would mask ordinary words and numbers everywhere.
const MinSecretLen = 6

// secretSet is the process-wide registry of exact secret values.
var secretSet struct {
	mu     sync.Mutex
	values map[string]struct{}
	repl   atomic.Pointer[strings.Replacer]
}

// RegisterSecret adds an exact value (token, key, password) that must never
// appear in any output: Redact, every logger built by New and every
// RedactingWriter replace it with "***". Values shorter than MinSecretLen
// are ignored; surrounding whitespace (a token file's trailing newline) is
// trimmed and both forms are registered, as is the JSON-escaped form of a
// value containing quotes, backslashes or control characters (so it is
// also found inside an already encoded log line). Safe for concurrent use.
func RegisterSecret(v string) {
	var candidates []string
	for _, c := range []string{v, strings.TrimSpace(v)} {
		candidates = append(candidates, c, jsonEscaped(c))
	}
	secretSet.mu.Lock()
	defer secretSet.mu.Unlock()
	changed := false
	for _, c := range candidates {
		if len(c) < MinSecretLen {
			continue
		}
		if secretSet.values == nil {
			secretSet.values = make(map[string]struct{})
		}
		if _, ok := secretSet.values[c]; ok {
			continue
		}
		secretSet.values[c] = struct{}{}
		changed = true
	}
	if !changed {
		return
	}
	vals := make([]string, 0, len(secretSet.values))
	for s := range secretSet.values {
		vals = append(vals, s)
	}
	// Longest first so a secret that contains another one is masked whole
	// (strings.Replacer tries the pairs in argument order).
	sort.Slice(vals, func(i, j int) bool {
		if len(vals[i]) != len(vals[j]) {
			return len(vals[i]) > len(vals[j])
		}
		return vals[i] < vals[j]
	})
	pairs := make([]string, 0, 2*len(vals))
	for _, s := range vals {
		pairs = append(pairs, s, Mask)
	}
	secretSet.repl.Store(strings.NewReplacer(pairs...))
}

// jsonEscaped returns s as it appears inside a JSON string literal.
func jsonEscaped(s string) string {
	q := appendJSONString(nil, s)
	return string(q[1 : len(q)-1])
}

// resetSecrets clears the registry (tests only).
func resetSecrets() {
	secretSet.mu.Lock()
	defer secretSet.mu.Unlock()
	secretSet.values = nil
	secretSet.repl.Store(nil)
}

// replaceSecrets masks registered values only.
func replaceSecrets(s string) string {
	r := secretSet.repl.Load()
	if r == nil {
		return s
	}
	return r.Replace(s)
}

// Sensitive names: a key ending in one of these words carries a secret
// value (token=, "password": …, private_key = …).
const sensitiveWords = `token|password|passwd|passphrase|secret|key|psk`

// valueAlt matches the value after a sensitive name: a complete
// double-quoted string (with escapes), a complete \"…\" string as found
// inside JSON-encoded text, a complete single-quoted string, or a bare word
// (optionally after an opening quote that is never closed). maskValue keeps
// the quotes and replaces what is between them.
const valueAlt = `"(?:[^"\\\n]|\\.)*"` +
	`|\\"(?:[^"\\\n]|\\[^"\n])*\\"` +
	`|'[^'\n]*'` +
	`|(?:\\?["'])?[^\s"'&,;\\]+`

// Redaction patterns.
var (
	// -----BEGIN … PRIVATE KEY----- blocks (PKCS#1/#8, EC, OpenSSH, PGP
	// "PRIVATE KEY BLOCK"), including a truncated block (no END line) whose
	// body is base64 / PEM headers / escaped newlines.
	pemBlockRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(?:[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|[A-Za-z0-9+/=\s\\:,-]*)`)
	pemBeginRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`)
	pemEndRe   = regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`)
	// age identities (backups are encrypted with age).
	ageKeyRe = regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Z]+`)
	// Telegram bot tokens: <bot id>:<35 char secret>, also inside
	// https://api.telegram.org/bot<token>/… URLs.
	botTokenRe = regexp.MustCompile(`\d{6,}:[A-Za-z0-9_-]{30,}`)
	// dey://TOKEN@HUB_IP:PORT#FINGERPRINT — only the token is masked; a
	// truncated link without "@" still loses its token.
	joinLinkRe = regexp.MustCompile(`(?i)(dey://)[^@\s/"'\\#]+`)
	// scheme://user:PASSWORD@host (mirror URLs, proxy URLs, gost/chisel);
	// the password runs to the last "@" of the authority (it may contain "@").
	userinfoRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@"'\\]*:)[^/\s"'\\]+@`)
	// Authorization headers (HTTP text, JSON, Go maps).
	authHeaderRe = regexp.MustCompile(`(?i)(\bauthorization\\?"?\]?\s*[:=]\s*\[?\\?"?\s*(?:(?:bearer|basic|token)\s+)?)[^\s"'\\,;\]]+`)
	bearerRe     = regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{6,}`)
	// "token":"…" / "password": 1234 in JSON text (the whole value becomes
	// the string "***"; an unterminated string is masked to its end).
	jsonKVRe = regexp.MustCompile(`(?i)("[a-z0-9_.-]*(?:` + sensitiveWords + `)"\s*:\s*)("(?:[^"\\]|\\.)*"?|-?[0-9][0-9.eE+-]*)`)
	// \"token\":\"…\" inside a JSON string that itself holds JSON. In that
	// context \\\\ is a backslash, \\\" a quote and \" ends the value.
	escJSONKVRe = regexp.MustCompile(`(?i)(\\"[a-z0-9_.-]*(?:` + sensitiveWords + `)\\"\s*:\s*\\")((?:\\\\\\\\|\\\\\\"|\\[^"]|[^"\\])*)`)
	// token=…, password = "a b", PrivateKey = … (query strings, TOML, INI, env).
	eqKVRe = regexp.MustCompile(`(?i)(\b[a-z0-9_.-]*(?:` + sensitiveWords + `)\s*=\s*)(` + valueAlt + `)`)
	// YAML "password: …" at the start of a line; a plain scalar runs to the
	// end of the line (it may contain spaces).
	yamlKVRe = regexp.MustCompile(`(?im)^(\s*-?\s*[a-z0-9_.-]*(?:token|password|passwd|passphrase|secret|private_?key|psk)\s*:[ \t]+)("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|["']?[^\s"'#\\][^\r\n]*)`)
	// --token VALUE / --auth user:pass style command-line flags.
	flagRe = regexp.MustCompile(`(?i)(--[a-z0-9-]*(?:token|password|passphrase|secret|psk|auth)\s+)("(?:[^"\\\n]|\\.)*"|'[^'\n]*'|[^\s"'\\-][^\s"'\\]*)`)
	// A 32+ character base64url string right after a token/key word
	// ("token abc…", "key: 'abc…'").
	afterWordRe = regexp.MustCompile(`(?i)(\b[a-z0-9_-]*(?:token|key|secret|password|passphrase)s?[^a-z0-9_\-\n\\]{1,4})([A-Za-z0-9_+/-]{32,}={0,2})`)
)

// Redact masks every registered secret and every known secret pattern in s
// with "***": token=, key=, password=, passphrase=, secret= (plain, quoted,
// JSON, escaped JSON, YAML and flag forms), Authorization/Bearer
// credentials, passwords in URLs, Telegram bot tokens, the token of dey://
// join links (the rest of the link is kept), PEM private-key blocks, age
// identities, and 32+ character base64url strings following a token/key
// word. Redact is idempotent.
func Redact(s string) string {
	if s == "" {
		return s
	}
	// Each pattern needs a literal that is cheap to look for; strings
	// without it (almost every log line) skip the regular expression.
	// Replacements only insert "***", so they never create a new match.
	if strings.Contains(s, "PRIVATE KEY") {
		s = pemBlockRe.ReplaceAllLiteralString(s, Mask)
	}
	if strings.Contains(s, "AGE-SECRET-KEY-1") {
		s = ageKeyRe.ReplaceAllLiteralString(s, Mask)
	}
	s = replaceSecrets(s)
	hasColon := strings.IndexByte(s, ':') >= 0
	if hasColon {
		s = botTokenRe.ReplaceAllLiteralString(s, Mask)
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "dey://") {
		s = joinLinkRe.ReplaceAllString(s, "${1}"+Mask)
	}
	if strings.Contains(s, "://") && strings.IndexByte(s, '@') >= 0 {
		s = userinfoRe.ReplaceAllString(s, "${1}"+Mask+"@")
	}
	if strings.Contains(lower, "authorization") {
		s = authHeaderRe.ReplaceAllString(s, "${1}"+Mask)
	}
	if strings.Contains(lower, "bearer") {
		s = bearerRe.ReplaceAllString(s, "${1}"+Mask)
	}
	if !containsSensitiveWord(lower) {
		return s
	}
	if strings.IndexByte(s, '"') >= 0 {
		s = jsonKVRe.ReplaceAllString(s, `${1}"`+Mask+`"`)
		if strings.Contains(s, `\"`) {
			s = escJSONKVRe.ReplaceAllString(s, "${1}"+Mask)
		}
	}
	if strings.IndexByte(s, '=') >= 0 {
		s = maskValues(eqKVRe, s)
	}
	if hasColon {
		s = maskValues(yamlKVRe, s)
	}
	if strings.Contains(s, "--") {
		s = maskValues(flagRe, s)
	}
	return afterWordRe.ReplaceAllString(s, "${1}"+Mask)
}

// maskValues replaces submatch 2 of every match of re with maskValue.
func maskValues(re *regexp.Regexp, s string) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, m := range matches {
		vs, ve := m[4], m[5]
		if vs < 0 {
			continue
		}
		b.WriteString(s[last:vs])
		b.WriteString(maskValue(s[vs:ve]))
		last = ve
	}
	b.WriteString(s[last:])
	return b.String()
}

// maskValue masks a matched value and keeps its quoting: "a b" → "***",
// \"x\" → \"***\", 'x' → '***', "unterminated → "***, bare → ***.
func maskValue(v string) string {
	for _, q := range []string{`\"`, `\'`, `"`, `'`} {
		if strings.HasPrefix(v, q) {
			if len(v) >= 2*len(q) && strings.HasSuffix(v, q) {
				return q + Mask + q
			}
			return q + Mask
		}
	}
	return Mask
}

// containsSensitiveWord is the prefilter of the name-based patterns
// (sensitiveWords and the flag words, lowercase input).
func containsSensitiveWord(lower string) bool {
	for _, w := range []string{"token", "pass", "secret", "key", "psk", "auth"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// sensitiveKey reports whether a structured-log attribute (or JSON object
// member) name denotes a secret, so its whole value is masked regardless of
// its content.
func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, suffix := range []string{"file", "path", "dir", "id", "count", "len", "fingerprint", "type", "kind", "name", "expiry", "expires", "ttl"} {
		if strings.HasSuffix(k, suffix) {
			return false
		}
	}
	if k == "key" || strings.HasSuffix(k, "_key") || strings.HasSuffix(k, "-key") || strings.HasSuffix(k, "privatekey") ||
		strings.HasSuffix(k, "presharedkey") {
		return true
	}
	for _, w := range []string{"token", "password", "passwd", "passphrase", "secret", "psk", "authorization", "apikey", "api_key", "cookie"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// sensitiveGroup reports whether any enclosing group name is sensitive
// (slog.Group("secrets", …) masks every member).
func sensitiveGroup(groups []string) bool {
	for _, g := range groups {
		if sensitiveKey(g) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- structured values

// redactJSON re-encodes the JSON document data with every string value and
// object key passed through Redact and every value stored under a
// sensitive member name (sensitiveKey) replaced by the string "***"
// (objects and arrays included). Member order is preserved; booleans and
// null cannot carry a secret and are kept.
func redactJSON(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	type frame struct {
		object  bool // {…}; false for […]
		wantKey bool // object: the next token is a member name
		n       int  // elements or members written so far
	}
	var (
		out   = make([]byte, 0, len(data))
		stack []frame
		skip  int  // nesting depth inside a masked container
		mask  bool // the next value is masked
	)
	valueDone := func() {
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].wantKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if stderrors.Is(err, io.EOF) {
			if len(stack) > 0 || skip > 0 || mask || len(out) == 0 {
				return nil, io.ErrUnexpectedEOF // Token reports a truncated document as io.EOF
			}
			break
		}
		if err != nil {
			return nil, err
		}
		d, isDelim := tok.(json.Delim)
		if skip > 0 {
			if isDelim {
				if d == '{' || d == '[' {
					skip++
				} else {
					skip--
				}
			}
			if skip == 0 {
				valueDone()
			}
			continue
		}
		if isDelim && (d == '}' || d == ']') {
			out = append(out, byte(d))
			stack = stack[:len(stack)-1]
			valueDone()
			continue
		}
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.object && top.wantKey {
				key, _ := tok.(string)
				if top.n > 0 {
					out = append(out, ',')
				}
				top.n++
				out = appendJSONString(out, Redact(key))
				out = append(out, ':')
				top.wantKey = false
				mask = sensitiveKey(key)
				continue
			}
			if !top.object {
				if top.n > 0 {
					out = append(out, ',')
				}
				top.n++
			}
		}
		if mask {
			mask = false
			switch tok.(type) {
			case bool, nil:
				// Cannot carry a secret: written unchanged below.
			default:
				out = appendJSONString(out, Mask)
				if isDelim {
					skip = 1
				} else {
					valueDone()
				}
				continue
			}
		}
		switch v := tok.(type) {
		case json.Delim: // '{' or '['
			out = append(out, byte(v))
			stack = append(stack, frame{object: v == '{', wantKey: v == '{'})
		case string:
			out = appendJSONString(out, Redact(v))
			valueDone()
		case json.Number:
			out = append(out, v...)
			valueDone()
		case bool:
			out = strconv.AppendBool(out, v)
			valueDone()
		case nil:
			out = append(out, "null"...)
			valueDone()
		}
	}
	return out, nil
}

// appendJSONString appends s as a JSON string literal (no HTML escaping,
// like slog's JSON handler).
func appendJSONString(dst []byte, s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // encoding a string cannot fail
	return append(dst, bytes.TrimSuffix(b.Bytes(), []byte("\n"))...)
}

// ---------------------------------------------------------------- raw streams

// maxPending bounds the partial line a RedactWriter buffers while waiting
// for a newline.
const maxPending = 1 << 20

// RedactWriter redacts a raw text stream (doctor bundles, streamed logs)
// line by line before passing it on. Complete lines are written as soon as
// their newline arrives; PEM private-key blocks spanning several lines are
// replaced by a single "***". A final line without a newline is held back
// until Flush or Close — or until the source is exhausted when the writer
// is the destination of io.Copy from a source without io.WriterTo (files,
// network streams, decompressors: it implements io.ReaderFrom). In-memory
// sources such as strings.Reader or bytes.Buffer write through Write, so
// always Close the writer at the end of a stream.
type RedactWriter struct {
	mu      sync.Mutex
	w       io.Writer
	pending []byte
	inKey   bool
}

// RedactingWriter wraps w so that everything written through it is
// redacted with Redact (and multi-line private keys are removed). The
// returned writer is safe for concurrent use. Call Close (or Flush) at the
// end of every stream so a final line without a newline is emitted.
func RedactingWriter(w io.Writer) *RedactWriter {
	return &RedactWriter{w: w}
}

// Write buffers p, redacts every complete line and writes it to the
// underlying writer. It reports len(p) on success.
func (r *RedactWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, p...)
	var out bytes.Buffer
	start := 0
	for {
		i := bytes.IndexByte(r.pending[start:], '\n')
		if i < 0 {
			break
		}
		out.WriteString(r.redactLine(string(r.pending[start : start+i+1])))
		start += i + 1
	}
	rest := len(r.pending) - start
	if rest > maxPending {
		out.WriteString(r.redactLine(string(r.pending[start:])))
		start = len(r.pending)
		rest = 0
	}
	copy(r.pending, r.pending[start:])
	r.pending = r.pending[:rest]
	if out.Len() > 0 {
		if _, err := r.w.Write(out.Bytes()); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// ReadFrom implements io.ReaderFrom: it redacts everything read from src
// and flushes the final partial line when src reports io.EOF, so
// io.Copy(RedactingWriter(w), src) needs no explicit Flush.
func (r *RedactWriter) ReadFrom(src io.Reader) (int64, error) {
	buf := make([]byte, 32<<10)
	var n int64
	for {
		m, err := src.Read(buf)
		if m > 0 {
			n += int64(m)
			if _, werr := r.Write(buf[:m]); werr != nil {
				return n, werr
			}
		}
		if stderrors.Is(err, io.EOF) {
			return n, r.Flush()
		}
		if err != nil {
			return n, err
		}
	}
}

// Flush redacts and writes a buffered partial line and ends a private-key
// block left open by a truncated stream.
func (r *RedactWriter) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() { r.inKey = false }()
	if len(r.pending) == 0 {
		return nil
	}
	s := r.redactLine(string(r.pending))
	r.pending = r.pending[:0]
	if s == "" {
		return nil
	}
	_, err := io.WriteString(r.w, s)
	return err
}

// Close flushes the writer. It does not close the underlying writer.
func (r *RedactWriter) Close() error { return r.Flush() }

// redactLine redacts one line (with its newline, if any) and tracks
// whether the stream is inside a multi-line private-key block.
func (r *RedactWriter) redactLine(line string) string {
	var out strings.Builder
	for line != "" {
		if r.inKey {
			if loc := pemEndRe.FindStringIndex(line); loc != nil {
				r.inKey = false
				line = line[loc[1]:]
				continue
			}
			if pemBodyLine(line) {
				return out.String() // key material: dropped
			}
			// Not key material and no END marker: the block was truncated.
			r.inKey = false
			out.WriteByte('\n')
			continue
		}
		loc := pemBeginRe.FindStringIndex(line)
		if loc == nil {
			out.WriteString(Redact(line))
			break
		}
		if end := pemEndRe.FindStringIndex(line[loc[1]:]); end != nil {
			// Whole block on this line (e.g. JSON-escaped newlines):
			// Redact masks it; continue after the END marker.
			cut := loc[1] + end[1]
			out.WriteString(Redact(line[:cut]))
			line = line[cut:]
			continue
		}
		out.WriteString(Redact(line[:loc[0]]))
		out.WriteString(Mask)
		r.inKey = true
		line = ""
	}
	return out.String()
}

var pemBodyRe = regexp.MustCompile(`^\s*(?:[A-Za-z0-9+/=]*|[A-Za-z-]+:.*)\s*$`)

// pemBodyLine reports whether line looks like the inside of a PEM block
// (base64, blank, or an RFC 1421 header such as "Proc-Type: 4,ENCRYPTED").
func pemBodyLine(line string) bool { return pemBodyRe.MatchString(line) }

// secretFilter masks registered secrets in each Write; used as the last
// stage of every logger so values preformatted before RegisterSecret was
// called (logger.With) are still masked. The handler writes one complete
// JSON line per Write.
type secretFilter struct{ w io.Writer }

func (f secretFilter) Write(p []byte) (int, error) {
	r := secretSet.repl.Load()
	if r == nil {
		return f.w.Write(p)
	}
	if _, err := io.WriteString(f.w, r.Replace(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
