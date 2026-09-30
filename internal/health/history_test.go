package health

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

const ms = time.Millisecond

func TestHistoryMedian(t *testing.T) {
	h := NewHistory()
	assert.Zero(t, h.Median(time.Time{}))
	h.Add(t0, 30*ms)
	h.Add(t0.Add(5*time.Second), 10*ms)
	h.Add(t0.Add(10*time.Second), 20*ms)
	assert.Equal(t, 20*ms, h.Median(time.Time{}))
	h.Add(t0.Add(15*time.Second), 40*ms)
	assert.Equal(t, 25*ms, h.Median(time.Time{}), "even count: mean of the middle two")
	assert.Equal(t, 30*ms, h.Median(t0.Add(10*time.Second)))
	assert.Zero(t, h.Median(t0.Add(time.Hour)))
	assert.Equal(t, 4, h.Len())
	s := h.Samples()
	assert.Len(t, s, 4)
	s[0].RTT = 0 // a copy
	assert.Equal(t, 30*ms, h.Samples()[0].RTT)
	h.Reset()
	assert.Zero(t, h.Len())
}

func TestHistoryRetention(t *testing.T) {
	var h History // zero value is usable
	for i := 0; i < 200; i++ {
		h.Add(t0.Add(time.Duration(i)*5*time.Second), ms) // 1000 s of samples
	}
	s := h.Samples()
	newest := s[len(s)-1].At
	assert.False(t, s[0].At.Before(newest.Add(-DegradedWindow)), "only the last 10 minutes are kept")
	assert.Equal(t, 121, len(s))

	capped := History{Max: 50, Keep: time.Hour}
	for i := 0; i < 5000; i++ {
		capped.Add(t0.Add(time.Duration(i)*time.Millisecond), ms)
	}
	assert.Equal(t, 50, capped.Len())
	assert.Equal(t, t0.Add(4999*time.Millisecond), capped.Samples()[49].At)

	var def History
	for i := 0; i < 3*HistoryMax; i++ {
		def.Add(t0.Add(time.Duration(i)*time.Millisecond), ms)
	}
	assert.Equal(t, HistoryMax, def.Len())
}

// fill adds a sample every 5 s in [from, to).
func fill(h *History, from, to time.Time, rtt time.Duration) {
	for at := from; at.Before(to); at = at.Add(5 * time.Second) {
		h.Add(at, rtt)
	}
}

func TestHistoryHighFor(t *testing.T) {
	now := t0.Add(10 * time.Minute)
	base := func() *History {
		h := NewHistory()
		fill(h, t0, now.Add(-DegradedFor), 10*ms)
		return h
	}

	h := base()
	fill(h, now.Add(-DegradedFor).Add(5*time.Second), now.Add(time.Second), 40*ms)
	assert.True(t, h.HighFor(now, 3, 10*time.Minute, time.Minute))
	assert.True(t, h.HighFor(now, 0, 0, 0), "zero arguments take the section 9 defaults")

	h = base()
	fill(h, now.Add(-DegradedFor).Add(5*time.Second), now.Add(time.Second), 30*ms)
	assert.False(t, h.HighFor(now, 3, 10*time.Minute, time.Minute), "exactly 3x is not above 3x")

	h = base()
	fill(h, now.Add(-DegradedFor).Add(5*time.Second), now.Add(time.Second), 40*ms)
	h.Add(now.Add(-10*time.Second), 12*ms)
	assert.False(t, h.HighFor(now, 3, 10*time.Minute, time.Minute), "one normal sample breaks the streak")

	h = base()
	h.Add(now.Add(-2*time.Second), 90*ms)
	h.Add(now.Add(-time.Second), 90*ms)
	assert.False(t, h.HighFor(now, 3, 10*time.Minute, time.Minute), "fewer than 3 recent samples")

	h = NewHistory()
	fill(h, now.Add(-DegradedFor).Add(5*time.Second), now.Add(time.Second), 40*ms)
	assert.False(t, h.HighFor(now, 3, 10*time.Minute, time.Minute), "no baseline")

	h = NewHistory()
	fill(h, t0, now.Add(-DegradedFor), 0)
	fill(h, now.Add(-DegradedFor).Add(5*time.Second), now.Add(time.Second), 40*ms)
	assert.False(t, h.HighFor(now, 3, 10*time.Minute, time.Minute), "zero baseline")
}

func TestHistoryConcurrent(t *testing.T) {
	h := NewHistory()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				at := t0.Add(time.Duration(i*8+g) * time.Second)
				h.Add(at, time.Duration(i)*ms)
				_ = h.Median(at.Add(-time.Minute))
				_ = h.HighFor(at, 3, 10*time.Minute, time.Minute)
			}
		}(g)
	}
	wg.Wait()
	assert.LessOrEqual(t, h.Len(), HistoryMax)
}
