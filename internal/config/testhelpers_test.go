package config

import (
	stderrors "errors"
	"sort"
	"strings"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

// testFP is a syntactically valid fingerprint.
var testFP = "sha256:" + strings.Repeat("ab", 32)

// knownRungs is what the fake registry accepts: every builtin rung plus the
// direct/haproxy rung of the section 4 sample.
var knownRungs = func() map[string]bool {
	m := map[string]bool{"direct/haproxy": true}
	for _, r := range append(append([]string{}, DefaultLadder...), DefaultUDPLadder...) {
		m[r] = true
	}
	return m
}()

// fakeOpts validates transport ids against knownRungs.
func fakeOpts() ValidateOptions {
	return ValidateOptions{
		KnownTransport: func(id string) bool { return knownRungs[id] },
		ValidTransports: func() []string {
			out := make([]string, 0, len(knownRungs))
			for r := range knownRungs {
				out = append(out, r)
			}
			sort.Strings(out)
			return out
		},
	}
}

// fakeSupports: "*/udp", wireguard and direct/native carry UDP; everything
// except wireguard carries TCP.
func fakeSupports(id, proto string) bool {
	switch proto {
	case ProtoUDP:
		return strings.HasSuffix(id, "/udp") || strings.HasPrefix(id, "wireguard/") || id == "direct/native"
	case ProtoTCP:
		return !strings.HasPrefix(id, "wireguard/")
	}
	return false
}

// validHub returns a small, valid hub config with two nodes and one tunnel.
func validHub() *Config {
	c := NewHub("ir-1", "5.6.7.8", 0)
	c.Nodes = []Node{
		{ID: "de-1", Name: "Germany 1", PublicIP: "1.2.3.4", CertFingerprint: testFP, Tags: []string{"primary"}},
		{ID: "nl-1", Name: "Netherlands 1", PublicIP: "9.8.7.6"},
	}
	c.Tunnels = []Tunnel{NewTunnel("main", "Main", []string{"de-1", "nl-1"}, []PortMap{{Listen: 443}, {Listen: 2053}})}
	return c
}

// validNode returns a valid node config.
func validNode() *Config { return NewNode("de-1", "5.6.7.8:44433", testFP) }

// codesOf flattens errors.Join trees into the DEY codes they carry, in order.
func codesOf(err error) []deyerr.Code {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []deyerr.Code
		for _, e := range j.Unwrap() {
			out = append(out, codesOf(e)...)
		}
		return out
	}
	var de *deyerr.Error
	if stderrors.As(err, &de) {
		return []deyerr.Code{de.Code}
	}
	return []deyerr.Code{"(plain)"}
}

// deyErrors flattens errors.Join trees into *deyerr.Error values.
func deyErrors(err error) []*deyerr.Error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []*deyerr.Error
		for _, e := range j.Unwrap() {
			out = append(out, deyErrors(e)...)
		}
		return out
	}
	var de *deyerr.Error
	if stderrors.As(err, &de) {
		return []*deyerr.Error{de}
	}
	return nil
}

// requireCodes asserts err carries exactly want (order-insensitive).
func requireCodes(t *testing.T, err error, want ...deyerr.Code) {
	t.Helper()
	got := codesOf(err)
	sortCodes := func(c []deyerr.Code) []deyerr.Code {
		out := append([]deyerr.Code{}, c...)
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	if len(want) == 0 {
		require.NoError(t, err)
		return
	}
	require.Equal(t, sortCodes(want), sortCodes(got), "error: %v", err)
}

// firstErr returns the first DEY error of err.
func firstErr(t *testing.T, err error) *deyerr.Error {
	t.Helper()
	all := deyErrors(err)
	require.NotEmpty(t, all, "no DEY error in %v", err)
	return all[0]
}
