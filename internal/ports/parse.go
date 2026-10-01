// Package ports implements the port rules of spec section 10: the owner's
// port input parser, reserved ports, the local bind check with owning-process
// lookup (/proc first, `ss -Hlntup` as fallback) and free-port suggestions.
//
// The package never runs a shell; the only external program it may use is
// `ss`, through an injectable exec.Runner.
package ports

import (
	"math/bits"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Protocols accepted in port specs.
const (
	ProtoTCP = "tcp"
	ProtoUDP = "udp"
)

// DefaultTargetHost is the node-side host a port map forwards to when the
// input names no target (QUESTIONS.md A.1).
const DefaultTargetHost = "127.0.0.1"

// MaxSpecs is the maximum number of port maps per tunnel (section 10).
const MaxSpecs = config.MaxPortMaps

// Spec is one parsed port map entry. It has the same fields as
// api.PortSpec (without the probe kind).
type Spec struct {
	Listen int    `json:"listen"`
	Proto  string `json:"proto"`
	Target string `json:"target"`
}

// String returns "443/tcp".
func (s Spec) String() string { return FormatSpec(s) }

// DefaultTarget returns the default target of a listen port:
// "127.0.0.1:<listen>".
func DefaultTarget(listen int) string {
	return net.JoinHostPort(DefaultTargetHost, strconv.Itoa(listen))
}

// NormalizeProto lower-cases and trims p and reports whether it is "tcp" or
// "udp".
func NormalizeProto(p string) (string, bool) {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case ProtoTCP, ProtoUDP:
		return p, true
	}
	return p, false
}

// ValidProto reports whether p is exactly "tcp" or "udp".
func ValidProto(p string) bool { return p == ProtoTCP || p == ProtoUDP }

// ValidPort reports whether p is in 1..65535.
func ValidPort(p int) bool { return p >= 1 && p <= 65535 }

// FormatSpec returns "<listen>/<proto>", e.g. "443/tcp" (the form used in
// DEY error parameters).
func FormatSpec(s Spec) string {
	return strconv.Itoa(s.Listen) + "/" + s.Proto
}

// FormatList renders specs in the owner's input syntax, the inverse of
// ParseInput: TCP ports carry no suffix, UDP ports end in "/udp", runs of
// three or more consecutive ports with default targets become "2000-2010",
// and non-default targets are written "443:8443" (or "443:10.0.0.5:8443"
// for another host). ParseInput(FormatList(x)) returns x for any
// duplicate-free x. Example: "443,2053,27015/udp".
func FormatList(specs []Spec) string {
	parts := make([]string, 0, len(specs))
	for i := 0; i < len(specs); {
		s := specs[i]
		j := i
		if hasDefaultTarget(s) {
			for j+1 < len(specs) {
				n := specs[j+1]
				if n.Proto != s.Proto || n.Listen != specs[j].Listen+1 || !hasDefaultTarget(n) {
					break
				}
				j++
			}
		}
		var item string
		if j-i >= 2 {
			item = strconv.Itoa(s.Listen) + "-" + strconv.Itoa(specs[j].Listen)
		} else {
			j = i
			item = formatOne(s)
		}
		if s.Proto != ProtoTCP {
			item += "/" + s.Proto
		}
		parts = append(parts, item)
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// hasDefaultTarget reports whether s forwards to 127.0.0.1:<listen> (an
// empty target means the default).
func hasDefaultTarget(s Spec) bool {
	return s.Target == "" || s.Target == DefaultTarget(s.Listen)
}

// formatOne renders one spec without its protocol suffix.
func formatOne(s Spec) string {
	l := strconv.Itoa(s.Listen)
	if hasDefaultTarget(s) {
		return l
	}
	host, port, err := net.SplitHostPort(s.Target)
	if err == nil && host == DefaultTargetHost {
		return l + ":" + port
	}
	return l + ":" + s.Target
}

// sepSpace matches whitespace around the separators of one item so that
// "443 / udp" and "443 : 8443" are read as "443/udp" and "443:8443".
var sepSpace = regexp.MustCompile(`\s*([/:\-])\s*`)

// ParseInput parses the owner's port input (sections 6 and 10). Items are
// separated by commas (or whitespace); each item is one of
//
//	443            TCP port 443 → 127.0.0.1:443
//	443/udp        UDP port 443
//	2000-2010      a range (optionally /udp), expanded to single ports
//	443:8443       listen 443, target 127.0.0.1:8443 (optionally /udp)
//	443:10.0.0.5:8443   listen 443, target 10.0.0.5:8443 ([v6]:port allowed)
//
// The protocol defaults to tcp, the target to 127.0.0.1:<listen>. Exact
// duplicates are dropped (first occurrence wins, input order is kept).
// Errors: DEY-C020 for input that is not in these forms, DEY-P010 for port
// numbers outside 1-65535, DEY-P017 for a range whose start exceeds its
// end, DEY-P021 when one listen port is given two different targets, and
// DEY-P016 for more than 64 port maps.
func ParseInput(s string) ([]Spec, error) {
	norm := sepSpace.ReplaceAllString(strings.TrimSpace(s), "$1")
	items := strings.FieldsFunc(norm, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	if len(items) == 0 {
		return nil, deyerr.New(deyerr.C020, deyerr.Params{"input": s})
	}
	// Ranges are never materialised beyond MaxSpecs entries: the distinct
	// (port, proto) pairs are counted in bit sets a 64-bit word at a time,
	// so a paste of thousands of "1-65535" items costs at most 1024 word
	// operations per item, and duplicates are checked only against the (at
	// most 64) kept entries.
	var (
		out   []Spec
		seen  [2]portBits // every (port, proto) given, for the count
		count int
	)
	for _, raw := range items {
		it, err := parseItem(raw)
		if err != nil {
			return nil, err
		}
		// Duplicates of kept entries: identical ones are dropped below, a
		// second target for a kept listen port is an error.
		if err := it.conflict(out); err != nil {
			return nil, err
		}
		visit := func(p int) bool {
			out = append(out, Spec{Listen: p, Proto: it.proto, Target: it.targetFor(p)})
			return len(out) < MaxSpecs
		}
		if len(out) >= MaxSpecs {
			visit = nil
		}
		count += seen[protoIndex(it.proto)].addRange(it.lo, it.hi, visit)
	}
	if count > MaxSpecs {
		return nil, deyerr.New(deyerr.P016, deyerr.Params{"count": count})
	}
	return out, nil
}

// portBits is a set of port numbers 0-65535.
type portBits [65536 / 64]uint64

// addRange adds the ports lo..hi (0 <= lo <= hi <= 65535) and returns how
// many of them were not present yet. visit, when not nil, is called with
// each newly added port in ascending order until it returns false.
func (b *portBits) addRange(lo, hi int, visit func(p int) bool) int {
	added := 0
	for w := lo >> 6; w <= hi>>6; w++ {
		mask := ^uint64(0)
		if w == lo>>6 {
			mask &= ^uint64(0) << (lo & 63)
		}
		if w == hi>>6 {
			mask &= ^uint64(0) >> (63 - (hi & 63))
		}
		fresh := mask &^ b[w]
		b[w] |= mask
		added += bits.OnesCount64(fresh)
		for visit != nil && fresh != 0 {
			p := w<<6 + bits.TrailingZeros64(fresh)
			fresh &= fresh - 1
			if !visit(p) {
				visit = nil
			}
		}
	}
	return added
}

func protoIndex(proto string) int {
	if proto == ProtoUDP {
		return 1
	}
	return 0
}

// inputItem is one parsed item: ports lo..hi (lo == hi for a single port)
// with an explicit target only for the listen:target form.
type inputItem struct {
	lo, hi int
	proto  string
	target string // "" = default target of each port
}

func (it inputItem) targetFor(p int) string {
	if it.target != "" {
		return it.target
	}
	return DefaultTarget(p)
}

// conflict returns DEY-P021 when the item gives one of the kept specs (same
// protocol, listen port inside the item) a different target; the lowest
// such port is reported.
func (it inputItem) conflict(kept []Spec) error {
	var prev *Spec
	for i := range kept {
		k := &kept[i]
		if k.Proto != it.proto || k.Listen < it.lo || k.Listen > it.hi || k.Target == it.targetFor(k.Listen) {
			continue
		}
		if prev == nil || k.Listen < prev.Listen {
			prev = k
		}
	}
	if prev == nil {
		return nil
	}
	return deyerr.New(deyerr.P021, deyerr.Params{
		"port": FormatSpec(*prev), "target": prev.Target, "other": it.targetFor(prev.Listen),
	})
}

// parseItem parses one comma-separated item.
func parseItem(item string) (inputItem, error) {
	garbage := func() error { return deyerr.New(deyerr.C020, deyerr.Params{"input": item}) }
	body, proto := item, ProtoTCP
	if i := strings.LastIndexByte(item, '/'); i >= 0 {
		p, ok := NormalizeProto(item[i+1:])
		if !ok {
			return inputItem{}, garbage()
		}
		body, proto = item[:i], p
	}
	if body == "" {
		return inputItem{}, garbage()
	}

	// listen:target
	if listenPart, targetPart, ok := strings.Cut(body, ":"); ok {
		listen, err := parsePort(listenPart, item)
		if err != nil {
			return inputItem{}, err
		}
		target, err := parseTarget(targetPart, item)
		if err != nil {
			return inputItem{}, err
		}
		return inputItem{lo: listen, hi: listen, proto: proto, target: target}, nil
	}

	// range lo-hi
	if loPart, hiPart, ok := strings.Cut(body, "-"); ok {
		lo, err := parsePort(loPart, item)
		if err != nil {
			return inputItem{}, err
		}
		hi, err := parsePort(hiPart, item)
		if err != nil {
			return inputItem{}, err
		}
		if lo > hi {
			return inputItem{}, deyerr.New(deyerr.P017, deyerr.Params{"input": item})
		}
		return inputItem{lo: lo, hi: hi, proto: proto}, nil
	}

	p, err := parsePort(body, item)
	if err != nil {
		return inputItem{}, err
	}
	return inputItem{lo: p, hi: p, proto: proto}, nil
}

// parsePort parses a decimal port; item is the whole item for the error.
func parsePort(s, item string) (int, error) {
	if s == "" {
		return 0, deyerr.New(deyerr.C020, deyerr.Params{"input": item})
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, deyerr.New(deyerr.C020, deyerr.Params{"input": item})
		}
	}
	trimmed := strings.TrimLeft(s, "0")
	if len(trimmed) > 5 {
		return 0, deyerr.New(deyerr.P010, deyerr.Params{"input": item})
	}
	n, err := strconv.Atoi("0" + trimmed)
	if err != nil || !ValidPort(n) {
		return 0, deyerr.New(deyerr.P010, deyerr.Params{"input": item})
	}
	return n, nil
}

// hostRe matches a DNS host name.
var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$`)

// parseTarget parses "8443" (port on 127.0.0.1) or "host:port" /
// "[v6]:port" and returns the canonical "host:port" target.
func parseTarget(t, item string) (string, error) {
	garbage := deyerr.New(deyerr.C020, deyerr.Params{"input": item})
	if t == "" {
		return "", garbage
	}
	if !strings.ContainsAny(t, ":[]") {
		p, err := parsePort(t, item)
		if err != nil {
			return "", err
		}
		return DefaultTarget(p), nil
	}
	host, portStr, err := net.SplitHostPort(t)
	if err != nil || host == "" {
		return "", garbage
	}
	p, err := parsePort(portStr, item)
	if err != nil {
		return "", err
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return "", garbage
		}
		return net.JoinHostPort(addr.Unmap().String(), strconv.Itoa(p)), nil
	}
	if strings.HasPrefix(t, "[") || len(host) > 253 || !hostRe.MatchString(host) {
		return "", garbage
	}
	return net.JoinHostPort(strings.ToLower(host), strconv.Itoa(p)), nil
}
