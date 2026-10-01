package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	deylog "github.com/localroot4/deyroute/internal/log"
)

func newJoin(t *testing.T) (*JoinTokens, *clock) {
	t.Helper()
	clk := &clock{t: t0}
	s := &Store{Root: t.TempDir()}
	return &JoinTokens{Path: s.JoinTokensPath(), Now: clk.Now}, clk
}

func TestJoinSingleUse(t *testing.T) {
	j, _ := newJoin(t)
	require.False(t, j.Active())
	tok, exp, err := j.Create(0)
	require.NoError(t, err)
	require.Equal(t, t0.Add(15*time.Minute), exp)
	require.Len(t, tok, 43)
	require.Equal(t, "***", deylog.Redact(tok))
	requireMode(t, j.Path, 0o600)
	data, err := os.ReadFile(j.Path)
	require.NoError(t, err)
	require.NotContains(t, string(data), tok, "only the hash is stored")
	require.Contains(t, string(data), hashToken(tok))
	require.True(t, j.Active())
	n, err := j.Count()
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, exp, j.Expiry())

	require.NoError(t, j.Consume(tok, "1.2.3.4:5555"))
	// Deleted immediately.
	data, err = os.ReadFile(j.Path)
	require.NoError(t, err)
	require.NotContains(t, string(data), hashToken(tok))
	require.False(t, j.Active())
	require.True(t, j.Expiry().IsZero())
	// Second use fails.
	require.Equal(t, deyerr.N001, code(t, j.Consume(tok, "1.2.3.4")))
}

// Check validates like Consume (failures count, blocks apply) but leaves a
// valid token usable.
func TestJoinCheckDoesNotBurn(t *testing.T) {
	j, clk := newJoin(t)
	tok, _, err := j.Create(0)
	require.NoError(t, err)
	require.NoError(t, j.Check(tok, "1.2.3.4"))
	require.NoError(t, j.Check(tok, "1.2.3.4"))
	require.True(t, j.Active())
	require.NoError(t, j.Consume(tok, "1.2.3.4"))
	require.Equal(t, deyerr.N001, code(t, j.Check(tok, "1.2.3.4")))

	const ip = "203.0.113.9"
	other, _, err := j.Create(0)
	require.NoError(t, err)
	for i := 0; i < DefaultMaxFailures; i++ {
		require.Equal(t, deyerr.N001, code(t, j.Check("wrong-token", ip)), "attempt %d", i+1)
	}
	require.Equal(t, deyerr.N007, code(t, j.Check(other, ip)))
	require.Equal(t, deyerr.N007, code(t, j.Consume(other, ip)))
	// An expired token fails the check too.
	clk.Add(16 * time.Minute)
	require.Equal(t, deyerr.N001, code(t, j.Check(other, "198.51.100.2")))
}

func TestJoinSeveralTokensAndExpiry(t *testing.T) {
	j, clk := newJoin(t)
	a, _, err := j.Create(time.Minute)
	require.NoError(t, err)
	b, expB, err := j.Create(time.Hour)
	require.NoError(t, err)
	require.Equal(t, expB, j.Expiry())
	require.NoError(t, j.Consume(b, "9.9.9.9"))
	require.True(t, j.Active(), "a is still valid")

	clk.Add(2 * time.Minute)
	require.False(t, j.Active())
	require.Equal(t, deyerr.N001, code(t, j.Consume(a, "9.9.9.9")), "expired")
	// Expired entries were pruned from the file.
	data, err := os.ReadFile(j.Path)
	require.NoError(t, err)
	require.NotContains(t, string(data), hashToken(a))

	c, _, err := j.Create(time.Minute)
	require.NoError(t, err)
	clk.Add(2 * time.Minute)
	require.NoError(t, j.Prune())
	data, err = os.ReadFile(j.Path)
	require.NoError(t, err)
	require.NotContains(t, string(data), hashToken(c))
	require.NoError(t, j.Prune()) // nothing to do
	n, err := j.Count()
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestJoinRateLimit(t *testing.T) {
	j, clk := newJoin(t)
	tok, _, err := j.Create(2 * time.Hour)
	require.NoError(t, err)
	const ip = "203.0.113.7"
	for i := 0; i < DefaultMaxFailures; i++ {
		require.Equal(t, deyerr.N001, code(t, j.Consume("wrong-token", ip)), "attempt %d", i+1)
	}
	blocked, until := j.Blocked(ip)
	require.True(t, blocked)
	require.Equal(t, clk.Now().Add(time.Hour), until)
	// Even the right token is refused while blocked (and not burned).
	err = j.Consume(tok, ip+":1234")
	require.Equal(t, deyerr.N007, code(t, err))
	require.Contains(t, deyerr.As(err).Message(), ip)
	require.True(t, j.Active())
	// Another address is not affected.
	require.Equal(t, deyerr.N001, code(t, j.Consume("", "198.51.100.1")))

	// After the block the address may try again.
	clk.Add(time.Hour)
	blocked, _ = j.Blocked(ip)
	require.False(t, blocked)
	require.NoError(t, j.Consume(tok, ip))
}

func TestJoinFailuresOutsideWindow(t *testing.T) {
	j, clk := newJoin(t)
	const ip = "2001:db8::7"
	for i := 0; i < DefaultMaxFailures-1; i++ {
		require.Equal(t, deyerr.N001, code(t, j.Consume("x", ip)))
	}
	clk.Add(61 * time.Minute) // the earlier failures age out
	for i := 0; i < DefaultMaxFailures-1; i++ {
		require.Equal(t, deyerr.N001, code(t, j.Consume("x", "["+ip+"]:443")))
	}
	blocked, _ := j.Blocked(ip)
	require.False(t, blocked)
	require.Equal(t, deyerr.N001, code(t, j.Consume("x", ip)))
	blocked, _ = j.Blocked(ip)
	require.True(t, blocked)
}

func TestJoinCustomLimits(t *testing.T) {
	j, clk := newJoin(t)
	j.MaxFailures, j.Window, j.Block = 2, time.Minute, 10*time.Minute
	require.Equal(t, deyerr.N001, code(t, j.Consume("x", "10.0.0.1")))
	require.Equal(t, deyerr.N001, code(t, j.Consume("x", "10.0.0.1")))
	require.Equal(t, deyerr.N007, code(t, j.Consume("x", "10.0.0.1")))
	clk.Add(10 * time.Minute)
	require.Equal(t, deyerr.N001, code(t, j.Consume("x", "10.0.0.1")))
}

func TestJoinTrackedIPsBounded(t *testing.T) {
	j, clk := newJoin(t)
	for i := 0; i < maxTrackedIPs+10; i++ {
		clk.Add(time.Millisecond)
		_ = j.Consume("x", fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	require.LessOrEqual(t, len(j.failures), maxTrackedIPs)
	_, kept := j.failures["10.0.0.0"]
	require.False(t, kept, "the oldest address is evicted first")
}

func TestJoinDamagedFile(t *testing.T) {
	j, _ := newJoin(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(j.Path), 0o700))
	require.NoError(t, os.WriteFile(j.Path, []byte("{broken"), 0o600))
	_, _, err := j.Create(0)
	require.Equal(t, deyerr.S009, code(t, err))
	require.Equal(t, deyerr.S009, code(t, j.Consume("x", "1.1.1.1")))
	require.False(t, j.Active())
	require.True(t, j.Expiry().IsZero())
	_, err = j.Count()
	require.Equal(t, deyerr.S009, code(t, err))
	require.Equal(t, deyerr.S009, code(t, j.Prune()))

	// An empty file is an empty set.
	require.NoError(t, os.WriteFile(j.Path, nil, 0o600))
	require.False(t, j.Active())
	_, _, err = j.Create(0)
	require.NoError(t, err)
	require.True(t, j.Active())

	// A directory in place of the file.
	j2 := &JoinTokens{Path: t.TempDir()}
	require.Equal(t, deyerr.S009, code(t, j2.Consume("x", "1.1.1.1")))
}

func TestJoinConcurrentConsume(t *testing.T) {
	j, _ := newJoin(t)
	tok, _, err := j.Create(0)
	require.NoError(t, err)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if j.Consume(tok, fmt.Sprintf("10.0.0.%d", i)) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, 1, ok, "a token is single use even under concurrency")
}

func TestNormalizeIP(t *testing.T) {
	require.Equal(t, "1.2.3.4", normalizeIP(" 1.2.3.4:80 "))
	require.Equal(t, "1.2.3.4", normalizeIP("::ffff:1.2.3.4"))
	require.Equal(t, "2001:db8::1", normalizeIP("[2001:DB8::1]:443"))
	require.Equal(t, "2001:db8::1", normalizeIP("[2001:db8::1]"))
	require.Equal(t, "not-an-ip", normalizeIP("not-an-ip"))
	require.True(t, strings.HasPrefix(hashToken("a"), "ca978112"))
	require.False(t, (&JoinTokens{}).now().IsZero())
}
