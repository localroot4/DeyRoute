package secrets

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"io/fs"
	"net"
	"strings"
	"sync"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// Join token defaults (section 11).
const (
	DefaultJoinTTL     = 15 * time.Minute
	DefaultMaxFailures = 5
	DefaultJoinWindow  = time.Hour
	DefaultJoinBlock   = time.Hour
	// maxTrackedIPs bounds the in-memory failure table; the oldest entries
	// are dropped first when an attacker cycles through many addresses.
	maxTrackedIPs = 4096
	// maxBlockedIPs bounds the in-memory block table the same way; the
	// block that ends first is dropped when it is full.
	maxBlockedIPs = 16384
	// sweepInterval is how often Consume scans the counters for stale
	// entries.
	sweepInterval = time.Minute
)

// JoinTokens manages the single-use join tokens of section 11 in
// join-tokens.json (0600). Only the sha256 of each token is written to
// disk; the plain token exists only in the join command shown once to the
// owner.
//
// Rate limiting: an address with MaxFailures failed attempts within Window
// is blocked for Block (DEY-N007). The failure counters and blocks live in
// memory only, so restarting the hub resets them; a restart cannot be
// triggered from the network, and the tokens themselves (32 random bytes,
// 15 minutes) cannot be guessed within any realistic number of attempts.
//
// A JoinTokens is safe for concurrent use. Zero MaxFailures, Window and
// Block mean 5, 1 hour and 1 hour; a nil Now means time.Now.
type JoinTokens struct {
	Path        string
	Now         func() time.Time
	MaxFailures int
	Window      time.Duration
	Block       time.Duration

	mu        sync.Mutex
	failures  map[string][]time.Time
	blocked   map[string]time.Time
	lastSweep time.Time
}

// joinFile is the on-disk format of join-tokens.json.
type joinFile struct {
	Version int         `json:"version"`
	Tokens  []joinEntry `json:"tokens"`
}

type joinEntry struct {
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

func (j *JoinTokens) now() time.Time {
	if j.Now != nil {
		return j.Now().UTC()
	}
	return time.Now().UTC()
}

func (j *JoinTokens) maxFailures() int {
	if j.MaxFailures > 0 {
		return j.MaxFailures
	}
	return DefaultMaxFailures
}

func (j *JoinTokens) window() time.Duration {
	if j.Window > 0 {
		return j.Window
	}
	return DefaultJoinWindow
}

func (j *JoinTokens) block() time.Duration {
	if j.Block > 0 {
		return j.Block
	}
	return DefaultJoinBlock
}

// Create adds a new single-use token valid for ttl (15 minutes when ttl <=
// 0) and returns it with its expiry. Expired tokens are pruned from the
// file at the same time.
func (j *JoinTokens) Create(ttl time.Duration) (token string, expires time.Time, err error) {
	if ttl <= 0 {
		ttl = DefaultJoinTTL
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.load()
	if err != nil {
		return "", time.Time{}, err
	}
	token, err = NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := j.now()
	expires = now.Add(ttl)
	f.Tokens = append(live(f.Tokens, now), joinEntry{SHA256: hashToken(token), Created: now, Expires: expires})
	if err := j.save(f); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// Consume validates and burns token presented from ip (an address, with or
// without a port).
//
//   - ip is blocked → DEY-N007 (the token is not even looked at);
//   - token unknown or expired → a failure is counted for ip → DEY-N001;
//     the MaxFailures-th failure within Window blocks ip for Block;
//   - success → the token is deleted from the file immediately (atomic
//     rewrite) and nil is returned.
func (j *JoinTokens) Consume(token, ip string) error {
	ip = normalizeIP(ip)
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	j.sweep(now, false)
	if until, ok := j.blocked[ip]; ok && now.Before(until) {
		return deyerr.New(deyerr.N007, deyerr.Params{"ip": ip})
	}
	f, err := j.load()
	if err != nil {
		return err
	}
	tokens := live(f.Tokens, now)
	pruned := len(tokens) != len(f.Tokens)
	found := -1
	if token != "" {
		want := []byte(hashToken(token))
		for i, e := range tokens {
			if subtle.ConstantTimeCompare([]byte(e.SHA256), want) == 1 {
				found = i
			}
		}
	}
	if found < 0 {
		if pruned {
			f.Tokens = tokens
			_ = j.save(f) // best effort: expired entries are ignored anyway
		}
		j.fail(ip, now)
		return deyerr.New(deyerr.N001, nil)
	}
	f.Tokens = append(tokens[:found:found], tokens[found+1:]...)
	if err := j.save(f); err != nil {
		return err
	}
	return nil
}

// Active reports whether at least one unexpired token exists: while it
// does, the hub opens the control port to every address (join window,
// QUESTIONS.md C.23).
func (j *JoinTokens) Active() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.load()
	if err != nil {
		return false
	}
	return len(live(f.Tokens, j.now())) > 0
}

// Count returns the number of unexpired tokens (security audit).
func (j *JoinTokens) Count() (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.load()
	if err != nil {
		return 0, err
	}
	return len(live(f.Tokens, j.now())), nil
}

// Expiry returns the latest expiry of the unexpired tokens (zero when there
// is none), so the hub can schedule closing the join window.
func (j *JoinTokens) Expiry() time.Time {
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := j.load()
	if err != nil {
		return time.Time{}
	}
	var last time.Time
	for _, e := range live(f.Tokens, j.now()) {
		if e.Expires.After(last) {
			last = e.Expires
		}
	}
	return last
}

// Prune removes expired tokens from the file and forgets stale failure
// counters and finished blocks.
func (j *JoinTokens) Prune() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := j.now()
	j.sweep(now, true)
	f, err := j.load()
	if err != nil {
		return err
	}
	tokens := live(f.Tokens, now)
	if len(tokens) == len(f.Tokens) {
		return nil
	}
	f.Tokens = tokens
	return j.save(f)
}

// Blocked reports whether ip is currently blocked and until when.
func (j *JoinTokens) Blocked(ip string) (bool, time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	until, ok := j.blocked[normalizeIP(ip)]
	if !ok || !j.now().Before(until) {
		return false, time.Time{}
	}
	return true, until
}

// fail records one failed attempt; the call that reaches MaxFailures
// within Window blocks ip.
func (j *JoinTokens) fail(ip string, now time.Time) {
	if j.failures == nil {
		j.failures = map[string][]time.Time{}
	}
	if j.blocked == nil {
		j.blocked = map[string]time.Time{}
	}
	if _, ok := j.failures[ip]; !ok && len(j.failures) >= maxTrackedIPs {
		j.evictOldest()
	}
	list := append(recent(j.failures[ip], now.Add(-j.window())), now)
	if len(list) >= j.maxFailures() {
		delete(j.failures, ip)
		if _, ok := j.blocked[ip]; !ok && len(j.blocked) >= maxBlockedIPs {
			j.evictBlock()
		}
		j.blocked[ip] = now.Add(j.block())
		return
	}
	j.failures[ip] = list
}

// evictOldest drops the address whose latest failure is the oldest.
func (j *JoinTokens) evictOldest() {
	var (
		oldestIP string
		oldestAt time.Time
	)
	for ip, list := range j.failures {
		last := list[len(list)-1]
		if oldestIP == "" || last.Before(oldestAt) {
			oldestIP, oldestAt = ip, last
		}
	}
	delete(j.failures, oldestIP)
}

// evictBlock drops the block that ends first, so the table stays bounded
// when an attacker spreads its attempts over very many addresses (an IPv6
// prefix). A finished block always ends first, so it goes before any
// active one.
func (j *JoinTokens) evictBlock() {
	var (
		first string
		until time.Time
	)
	for ip, u := range j.blocked {
		if first == "" || u.Before(until) {
			first, until = ip, u
		}
	}
	delete(j.blocked, first)
}

// sweep drops failures older than Window and finished blocks from both
// tables. Unless force is set it runs at most once per sweepInterval, so a
// flood of attempts does not pay a full table scan each; correctness does
// not depend on it (fail trims the address's own list and blocks are
// compared with now).
func (j *JoinTokens) sweep(now time.Time, force bool) {
	if !force && !j.lastSweep.IsZero() && now.Sub(j.lastSweep) < sweepInterval && !now.Before(j.lastSweep) {
		return
	}
	j.lastSweep = now
	cutoff := now.Add(-j.window())
	for ip, list := range j.failures {
		kept := recent(list, cutoff)
		if len(kept) == 0 {
			delete(j.failures, ip)
		} else {
			j.failures[ip] = kept
		}
	}
	for ip, until := range j.blocked {
		if !now.Before(until) {
			delete(j.blocked, ip)
		}
	}
}

// recent returns the failures of list after cutoff (reusing list).
func recent(list []time.Time, cutoff time.Time) []time.Time {
	kept := list[:0]
	for _, t := range list {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// load reads the token file; a missing file is an empty set.
func (j *JoinTokens) load() (joinFile, error) {
	f := joinFile{Version: 1}
	data, err := readSecret(j.Path)
	if stderrors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, unreadable(j.Path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return joinFile{Version: 1}, unreadable(j.Path, err)
	}
	return f, nil
}

// save rewrites the token file atomically (0600).
func (j *JoinTokens) save(f joinFile) error {
	f.Version = 1
	if f.Tokens == nil {
		f.Tokens = []joinEntry{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	return tlsutil.WriteSecret(j.Path, append(data, '\n'))
}

// live returns the entries that expire after now (a new slice).
func live(in []joinEntry, now time.Time) []joinEntry {
	out := make([]joinEntry, 0, len(in))
	for _, e := range in {
		if now.Before(e.Expires) {
			out = append(out, e)
		}
	}
	return out
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// normalizeIP strips a port and brackets and canonicalizes the address so
// "1.2.3.4:5555" and "1.2.3.4" count as the same source.
func normalizeIP(s string) string {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if ip := net.ParseIP(s); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ip.String()
	}
	return s
}
