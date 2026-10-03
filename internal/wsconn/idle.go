package wsconn

import (
	"sync"
	"time"
)

// idleTracker implements Config.IdleTimeout. The clock runs only while all of
// these hold:
//
//   - one of our pings was completely written and no byte of any frame has
//     arrived since (pingSent);
//   - a Read call is active (readActive): the peer's frames, the pong among
//     them, sit in the kernel buffer while nobody reads, and a slow consumer
//     must not be punished for not reading;
//   - no write is in progress (writes == 0): a blocked write holds the ping
//     back and means the peer (or the path) is merely slow, not silent.
//
// The accumulated running time (acc) is compared with the timeout. All
// methods are no-ops on a nil tracker (IdleTimeout == 0).
type idleTracker struct {
	timeout time.Duration

	mu         sync.Mutex
	pingSent   bool
	readActive bool
	writes     int
	acc        time.Duration
	since      time.Time // start of the running interval; zero while paused
}

func newIdleTracker(timeout time.Duration) *idleTracker {
	if timeout <= 0 {
		return nil
	}
	return &idleTracker{timeout: timeout}
}

// fold closes the running interval into acc.
func (t *idleTracker) fold(now time.Time) {
	if !t.since.IsZero() {
		t.acc += now.Sub(t.since)
		t.since = time.Time{}
	}
}

// resume restarts the running interval if every condition holds.
func (t *idleTracker) resume(now time.Time) {
	if t.pingSent && t.readActive && t.writes == 0 {
		t.since = now
	}
}

// update applies a state change, keeping acc exact.
func (t *idleTracker) update(f func()) {
	now := time.Now()
	t.mu.Lock()
	t.fold(now)
	f()
	t.resume(now)
	t.mu.Unlock()
}

func (t *idleTracker) readEnter() {
	if t != nil {
		t.update(func() { t.readActive = true })
	}
}

func (t *idleTracker) readExit() {
	if t != nil {
		t.update(func() { t.readActive = false })
	}
}

func (t *idleTracker) writeStart() {
	if t != nil {
		t.update(func() { t.writes++ })
	}
}

func (t *idleTracker) writeEnd() {
	if t != nil {
		t.update(func() { t.writes-- })
	}
}

// pingWritten records that one of our pings went out completely. The clock
// starts with the first unanswered ping, a later one does not restart it.
func (t *idleTracker) pingWritten() {
	if t != nil {
		t.update(func() {
			if !t.pingSent {
				t.pingSent = true
				t.acc = 0
			}
		})
	}
}

// arrived records that bytes of a frame arrived: the peer is alive.
func (t *idleTracker) arrived() {
	if t != nil {
		t.update(func() {
			t.pingSent = false
			t.acc = 0
		})
	}
}

// exceeded reports whether the connection has been silent for the timeout.
func (t *idleTracker) exceeded() bool {
	if t == nil {
		return false
	}
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fold(now)
	t.resume(now)
	return t.pingSent && t.acc >= t.timeout
}
