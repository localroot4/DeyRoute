package front

import (
	"net/netip"
	"sync"
	"time"
)

// Limits of the pre-auth phase. Before a request is parsed the only address
// known is the TCP peer, and behind a CDN that is an edge address shared by
// every customer of the edge, so the peer of a trusted proxy is never
// penalised for what it carries: failures are charged to the real client
// address (CF-Connecting-IP of a trusted peer, the TCP peer of any other),
// and only requests of the scanner class count (a wrong secret, a malformed
// request). A request that carries the right secret is never counted and is
// served even from an address that is currently blocked, so the reconnect that
// would fix a broken node is never what locks it out.
const (
	defaultMaxPreAuth    = 256
	defaultHeaderTimeout = 10 * time.Second
	defaultWSIdle        = 25 * time.Second
	defaultFailLimit     = 30
	defaultFailWindow    = time.Minute
	maxHeaderBlock       = 8 << 10 // request line and headers together
	maxTrackedIPs        = 8192
)

// failLimiter counts scanner-class failures per address in a fixed window.
// It has no goroutine: stale entries are swept when the table fills.
type failLimiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu sync.Mutex
	m  map[netip.Addr]*failEntry
}

type failEntry struct {
	start time.Time
	n     int
}

func newFailLimiter(limit int, window time.Duration) *failLimiter {
	return &failLimiter{limit: limit, window: window, now: time.Now, m: map[netip.Addr]*failEntry{}}
}

// blocked reports whether ip exceeded the limit in the current window.
func (l *failLimiter) blocked(ip netip.Addr) bool {
	if l.limit <= 0 || !ip.IsValid() {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[ip]
	if e == nil {
		return false
	}
	if l.now().Sub(e.start) >= l.window {
		delete(l.m, ip)
		return false
	}
	return e.n >= l.limit
}

// fail records one scanner-class request from ip.
func (l *failLimiter) fail(ip netip.Addr) {
	if l.limit <= 0 || !ip.IsValid() {
		return
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.m[ip]
	if e != nil && now.Sub(e.start) >= l.window {
		e = nil
	}
	if e == nil {
		if len(l.m) >= maxTrackedIPs {
			l.sweep(now)
			if len(l.m) >= maxTrackedIPs {
				return // the table is full of live entries: do not grow it
			}
		}
		e = &failEntry{start: now}
		l.m[ip] = e
	}
	e.n++
}

func (l *failLimiter) sweep(now time.Time) {
	for ip, e := range l.m {
		if now.Sub(e.start) >= l.window {
			delete(l.m, ip)
		}
	}
}
