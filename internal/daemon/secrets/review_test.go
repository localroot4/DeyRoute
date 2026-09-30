package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// TestReadSecretRefusesLinksAndFIFOs: a planted symlink or FIFO in place of
// a secret is refused (S009) instead of followed or blocking the hub.
func TestReadSecretRefusesLinksAndFIFOs(t *testing.T) {
	s, _ := newStore(t)
	tok, err := s.Token("main")
	require.NoError(t, err)

	// Symlink to a readable file elsewhere.
	outside := filepath.Join(t.TempDir(), "attacker.token")
	require.NoError(t, os.WriteFile(outside, []byte("attacker-controlled-token\n"), 0o600))
	path := s.TokenPath("main")
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(outside, path))
	_, err = s.Token("main")
	require.Equal(t, deyerr.S009, code(t, err))
	require.Contains(t, err.Error(), "not a regular file")

	// FIFO: must not block.
	require.NoError(t, os.Remove(path))
	require.NoError(t, syscall.Mkfifo(path, 0o600))
	done := make(chan error, 1)
	go func() { _, err := s.Token("main"); done <- err }()
	select {
	case err := <-done:
		require.Equal(t, deyerr.S009, code(t, err))
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO secret blocked")
	}

	// Oversized file.
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.WriteFile(path, make([]byte, maxSecretFileSize+1), 0o600))
	_, err = s.Token("main")
	require.Equal(t, deyerr.S009, code(t, err))

	// The original token is not resurrected by accident.
	require.NoError(t, os.Remove(path))
	fresh, err := s.Token("main")
	require.NoError(t, err)
	require.NotEqual(t, tok, fresh)
}

// TestCustomCombinedPEMKeyNotInCert: an owner-supplied combined file
// (certificate chain + private key in one PEM, used as both cert_file and
// key_file) must not put the private key into CertPEM, which the planner
// copies to TLS clients as tls-cert.pem / trust anchor.
func TestCustomCombinedPEMKeyNotInCert(t *testing.T) {
	s, _ := newStore(t)
	ca := newCA(t, t0)
	certPEM, keyPEM, err := ca.IssueServer("vpn.example.com", nil, []string{"vpn.example.com"}, 365*24*time.Hour)
	require.NoError(t, err)
	combined := filepath.Join(t.TempDir(), "combined.pem")
	require.NoError(t, os.WriteFile(combined, append(append(append([]byte(nil), keyPEM...), certPEM...), ca.CertPEM...), 0o600))

	m, err := s.TunnelTLS("main", "custom", hubIP, "", combined, combined)
	require.NoError(t, err)
	require.NotContains(t, string(m.CertPEM), "PRIVATE KEY")
	require.Equal(t, 2, strings.Count(string(m.CertPEM), "BEGIN CERTIFICATE"), "the chain is kept")
	require.NotContains(t, string(m.KeyPEM), "CERTIFICATE")
	require.Equal(t, 1, strings.Count(string(m.KeyPEM), "-----BEGIN "), "one key block")
	_, pass, err := s.PKCS12("main")
	require.NoError(t, err)
	require.NotEmpty(t, pass)
}

// TestJoinBlockedIPsBounded: an attacker spreading failures over very many
// addresses cannot grow the block table without bound.
func TestJoinBlockedIPsBounded(t *testing.T) {
	j, clk := newJoin(t)
	j.MaxFailures = 1 // every failure blocks
	for i := 0; i < maxBlockedIPs+10; i++ {
		clk.Add(time.Millisecond)
		require.Equal(t, deyerr.N001, code(t, j.Consume("x", fmt.Sprintf("2001:db8::%x", i))))
	}
	require.LessOrEqual(t, len(j.blocked), maxBlockedIPs)
	blocked, _ := j.Blocked("2001:db8::0")
	require.False(t, blocked, "the block that ends first is evicted")
	last := fmt.Sprintf("2001:db8::%x", maxBlockedIPs+9)
	blocked, _ = j.Blocked(last)
	require.True(t, blocked, "recent blocks are kept")
	require.Equal(t, deyerr.N007, code(t, j.Consume("x", last)))
}
