package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Shared helpers for renderers. Every renderer must be deterministic so
// golden files stay stable: iterate maps through SortedKeys.

// TOMLString quotes s as a TOML basic string.
func TOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f || r == utf8.RuneError {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TOMLStringArray renders ["a", "b"].
func TOMLStringArray(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = TOMLString(s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// JSONIndent marshals v with two-space indentation and a trailing newline,
// without HTML escaping (deterministic; struct field order is preserved,
// maps are sorted by encoding/json).
func JSONIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SortedKeys returns the keys of m in ascending order.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// HostPort joins host and port, bracketing IPv6 literals.
func HostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

// SplitTarget splits "host:port" into its parts; ok=false when malformed.
func SplitTarget(target string) (host string, port int, ok bool) {
	i := strings.LastIndex(target, ":")
	if i <= 0 || i == len(target)-1 {
		return "", 0, false
	}
	host = strings.Trim(target[:i], "[]")
	p, err := strconv.Atoi(target[i+1:])
	if err != nil || p < 1 || p > 65535 {
		return "", 0, false
	}
	return host, p, true
}

// ServiceName returns the per-port-map service name used by several
// backends: "tcp-443", "udp-27015".
func ServiceName(proto string, listen int) string {
	return proto + "-" + strconv.Itoa(listen)
}
