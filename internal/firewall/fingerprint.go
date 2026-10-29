package firewall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/exec"
)

// Fingerprint returns a digest of the live `table inet deyroute` as `nft
// list table` prints it, "" when the table does not exist. The daemons
// take it right after an apply and compare it periodically
// (security.firewall_reassert_s, DR-14): another tool or `nft flush
// ruleset` that removed or changed the table changes it. What the kernel
// changes by itself does not count: the elements of a dynamic set (filled
// from the packet path, each with its own expiry) are left out, and the
// elements of every other set are compared as a sorted list, so nft
// listing them in another order or wrapped is not a change. Errors are
// Show's.
func Fingerprint(ctx context.Context, r exec.Runner) (string, error) {
	out, err := Show(ctx, r)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", err
	}
	return fingerprint(out), nil
}

// fingerprint is the digest of a non-empty table listing (Fingerprint).
func fingerprint(listing string) string {
	h := sha256.New()
	for _, line := range normalizeListing(listing) {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// normalizeListing returns the trimmed, non-empty lines of a table listing
// with every "elements = { … }" statement on one line: sorted, or left out
// in a dynamic set ("flags dynamic", which nft lists before the elements).
func normalizeListing(listing string) []string {
	var (
		out     []string
		inSet   bool
		dynamic bool
		elems   []string
		depth   int // brace depth inside an elements statement; 0 = none
	)
	for _, raw := range strings.Split(listing, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if depth > 0 || (inSet && strings.HasPrefix(line, "elements")) {
			body := line
			if depth == 0 {
				_, body, _ = strings.Cut(line, "{")
				depth = 1
			}
			depth += strings.Count(body, "{") - strings.Count(body, "}")
			if depth <= 0 {
				body = body[:strings.LastIndexByte(body, '}')]
			}
			for _, e := range strings.Split(body, ",") {
				if e = strings.Join(strings.Fields(e), " "); e != "" {
					elems = append(elems, e)
				}
			}
			if depth > 0 {
				continue
			}
			depth = 0
			if !dynamic {
				slices.Sort(elems)
				out = append(out, "elements = { "+strings.Join(elems, ", ")+" }")
			}
			elems = nil
			continue
		}
		switch {
		case strings.HasPrefix(line, "set ") || strings.HasPrefix(line, "map "):
			inSet, dynamic = true, false
		case inSet && line == "}":
			inSet = false
		case inSet && strings.HasPrefix(line, "flags "):
			flags := strings.FieldsFunc(strings.TrimPrefix(line, "flags "), func(r rune) bool { return r == ',' || r == ' ' })
			dynamic = slices.Contains(flags, "dynamic")
		}
		out = append(out, line)
	}
	return out
}

// Reasons of a re-assert (event firewall_reapplied, DR-14).
const (
	// ReasonMissing: table inet deyroute was gone (`nft flush ruleset`,
	// another tool deleted it).
	ReasonMissing = "missing"
	// ReasonDrift: the table was there but not the one applied.
	ReasonDrift = "drift"
)

// ReportGap is the shortest time between two firewall_reapplied reports
// of the same reason: a tool that flushes the ruleset every minute gets
// its table back every minute, but one report per 10 minutes.
const ReportGap = 10 * time.Minute

// ReportGate deduplicates firewall_reapplied per reason (ReportGap). The
// zero value is ready; it is not safe for concurrent use.
type ReportGate struct{ last map[string]time.Time }

// Due reports whether a re-assert for reason is reported at now, and
// remembers it when it is.
func (g *ReportGate) Due(reason string, now time.Time) bool {
	if t, ok := g.last[reason]; ok && now.Sub(t) < ReportGap {
		return false
	}
	if g.last == nil {
		g.last = map[string]time.Time{}
	}
	g.last[reason] = now
	return true
}
