package failover

import (
	"sort"
	"sync"
	"time"
)

// Clock is the engine's only source of time. Production code uses
// RealClock; tests use FakeClock so every timing rule of section 9 can be
// exercised deterministically.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// NewTimer returns a timer that fires once after d (immediately when d
	// is zero or negative).
	NewTimer(d time.Duration) Timer
}

// Timer is a one-shot timer created by a Clock.
type Timer interface {
	// C returns the channel the fire time is delivered on (buffered, one value).
	C() <-chan time.Time
	// Stop prevents the timer from firing; it reports whether the timer was
	// still pending.
	Stop() bool
}

// RealClock is the wall clock.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// NewTimer wraps time.NewTimer.
func (RealClock) NewTimer(d time.Duration) Timer { return realTimer{t: time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// FakeClock is a manually driven Clock for tests. Time only moves when
// Advance is called; due timers fire in deadline order (ties in creation
// order). It is safe for concurrent use.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	seq     uint64
	timers  []*fakeTimer
	changed chan struct{} // closed and replaced whenever the timer set changes
}

// NewFakeClock returns a FakeClock that starts at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, changed: make(chan struct{})}
}

type fakeTimer struct {
	clk  *FakeClock
	when time.Time
	seq  uint64
	ch   chan time.Time
}

// Now returns the fake current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer registers a timer that fires when the fake time reaches now+d.
func (f *FakeClock) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d < 0 {
		d = 0
	}
	f.seq++
	t := &fakeTimer{clk: f, when: f.now.Add(d), seq: f.seq, ch: make(chan time.Time, 1)}
	f.timers = append(f.timers, t)
	f.notifyLocked()
	return t
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	f := t.clk
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.timers {
		if x == t {
			f.timers = append(f.timers[:i], f.timers[i+1:]...)
			f.notifyLocked()
			return true
		}
	}
	return false
}

func (f *FakeClock) notifyLocked() {
	close(f.changed)
	f.changed = make(chan struct{})
}

// Advance moves the fake time forward by d and fires every timer whose
// deadline is reached, earliest first. Timers created by the receivers while
// Advance runs are not fired by this call; see Pending and BlockUntil to
// step an actor timer by timer.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := f.now.Add(d)
	due := make([]*fakeTimer, 0, len(f.timers))
	rest := f.timers[:0]
	for _, t := range f.timers {
		if !t.when.After(target) {
			due = append(due, t)
		} else {
			rest = append(rest, t)
		}
	}
	f.timers = rest
	sort.Slice(due, func(i, j int) bool {
		if due[i].when.Equal(due[j].when) {
			return due[i].seq < due[j].seq
		}
		return due[i].when.Before(due[j].when)
	})
	for _, t := range due {
		if t.when.After(f.now) {
			f.now = t.when
		}
		t.ch <- f.now // buffered, never blocks: each timer fires once
	}
	f.now = target
	if len(due) > 0 {
		f.notifyLocked()
	}
}

// Pending returns the number of timers that have neither fired nor been
// stopped.
func (f *FakeClock) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// NextDeadline returns the earliest pending timer deadline.
func (f *FakeClock) NextDeadline() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.timers) == 0 {
		return time.Time{}, false
	}
	next := f.timers[0].when
	for _, t := range f.timers[1:] {
		if t.when.Before(next) {
			next = t.when
		}
	}
	return next, true
}

// BlockUntil waits (in real time, at most timeout) until at least n timers
// are pending, which is how a test knows an actor went back to sleep. It
// reports whether the condition was met.
func (f *FakeClock) BlockUntil(n int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		ok := len(f.timers) >= n
		ch := f.changed
		f.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-ch:
		case <-deadline.C:
			return false
		}
	}
}
