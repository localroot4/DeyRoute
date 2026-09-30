package health

import (
	"sort"
	"sync"
	"time"
)

// Sample is one successful probe RTT.
type Sample struct {
	At  time.Time
	RTT time.Duration
}

// History keeps recent RTT samples of one tunnel path for the DEGRADED rule
// ("RTT above 3x the median of the last 10 minutes for 60 seconds"). Samples
// older than Keep (DegradedWindow when zero) relative to the newest sample
// are dropped, and at most Max (HistoryMax when zero) are kept. The zero
// value is ready to use and safe for concurrent use.
type History struct {
	// Keep is the retention window.
	Keep time.Duration
	// Max caps the number of samples.
	Max int

	mu      sync.Mutex
	samples []Sample // in insertion order (normally chronological)
	newest  time.Time
}

// NewHistory returns a History with the default retention (10 minutes,
// 1000 samples).
func NewHistory() *History { return &History{} }

// Add records a sample taken at t.
func (h *History) Add(t time.Time, rtt time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, Sample{At: t, RTT: rtt})
	if t.After(h.newest) {
		h.newest = t
	}
	h.trim()
}

// trim drops samples outside the retention window and above the cap.
func (h *History) trim() {
	keep := h.Keep
	if keep <= 0 {
		keep = DegradedWindow
	}
	limit := h.Max
	if limit <= 0 {
		limit = HistoryMax
	}
	cutoff := h.newest.Add(-keep)
	kept := h.samples[:0]
	for _, s := range h.samples {
		if !s.At.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	if extra := len(kept) - limit; extra > 0 {
		kept = kept[extra:]
	}
	// Copy when the live part has drifted far into the backing array so it
	// does not grow without bound.
	if cap(kept) > 2*limit {
		kept = append(make([]Sample, 0, len(kept)+1), kept...)
	}
	h.samples = kept
}

// Len returns the number of samples kept.
func (h *History) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.samples)
}

// Samples returns a copy of the samples kept.
func (h *History) Samples() []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Sample(nil), h.samples...)
}

// Reset drops every sample (after a switch the new path starts a fresh
// baseline).
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = nil
	h.newest = time.Time{}
}

// Median returns the median RTT of the samples taken at or after since, or
// 0 when there is none.
func (h *History) Median(since time.Time) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	rtts := make([]time.Duration, 0, len(h.samples))
	for _, s := range h.samples {
		if !s.At.Before(since) {
			rtts = append(rtts, s.RTT)
		}
	}
	return median(rtts)
}

// HighFor reports whether the RTT has been high for dur: there are at least
// 3 samples in (now-dur, now] and every one of them exceeds factor times the
// median of the baseline, the samples in [now-window, now-dur]. The
// baseline needs at least 3 samples. Zero or negative arguments take the
// section 9 defaults (factor 3, window 10 minutes, dur 60 seconds).
func (h *History) HighFor(now time.Time, factor float64, window, dur time.Duration) bool {
	if factor <= 0 {
		factor = DegradedFactor
	}
	if window <= 0 {
		window = DegradedWindow
	}
	if dur <= 0 {
		dur = DegradedFor
	}
	recentFrom := now.Add(-dur)
	baseFrom := now.Add(-window)
	h.mu.Lock()
	var base, recent []time.Duration
	for _, s := range h.samples {
		switch {
		case s.At.After(recentFrom):
			recent = append(recent, s.RTT)
		case !s.At.Before(baseFrom):
			base = append(base, s.RTT)
		}
	}
	h.mu.Unlock()
	const minSamples = 3
	if len(recent) < minSamples || len(base) < minSamples {
		return false
	}
	m := median(base)
	if m <= 0 {
		return false
	}
	threshold := float64(m) * factor
	for _, r := range recent {
		if float64(r) <= threshold {
			return false
		}
	}
	return true
}

// median sorts rtts in place and returns the median (the mean of the two
// middle values for an even count), 0 when empty.
func median(rtts []time.Duration) time.Duration {
	n := len(rtts)
	if n == 0 {
		return 0
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	if n%2 == 1 {
		return rtts[n/2]
	}
	return (rtts[n/2-1] + rtts[n/2]) / 2
}
