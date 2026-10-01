package failover

import (
	"sort"
	"time"
)

// rttHistory keeps the successful probe RTTs of the active candidate for the
// DEGRADED rule of section 9: "RTT above 3x the median of the last 10
// minutes for 60 seconds". It is owned by the engine goroutine.
type rttHistory struct {
	samples   []rttSample
	highSince time.Time // first sample of the current run of high RTTs
}

type rttSample struct {
	at  time.Time
	rtt time.Duration
}

// maxRTTSamples bounds memory when probes are very frequent.
const maxRTTSamples = 2000

// add records a successful probe and reports whether the RTT has been above
// RTTHighFactor times the baseline median for at least RTTHighFor, plus the
// baseline median used. The baseline is every sample of the last
// RTTBaselineWindow before this one and needs RTTMinBaseline samples.
func (h *rttHistory) add(now time.Time, rtt time.Duration) (high bool, median time.Duration) {
	cutoff := now.Add(-RTTBaselineWindow)
	kept := h.samples[:0]
	for _, s := range h.samples {
		if !s.at.Before(cutoff) {
			kept = append(kept, s)
		}
	}
	h.samples = kept
	if len(h.samples) >= RTTMinBaseline {
		median = medianOf(h.samples)
	}
	if median > 0 && rtt > RTTHighFactor*median {
		if h.highSince.IsZero() {
			h.highSince = now
		}
	} else {
		h.highSince = time.Time{}
	}
	h.samples = append(h.samples, rttSample{at: now, rtt: rtt})
	if extra := len(h.samples) - maxRTTSamples; extra > 0 {
		h.samples = append([]rttSample(nil), h.samples[extra:]...)
	}
	high = !h.highSince.IsZero() && now.Sub(h.highSince) >= RTTHighFor
	return high, median
}

// high reports the current RTT verdict without adding a sample.
func (h *rttHistory) high(now time.Time) bool {
	return !h.highSince.IsZero() && now.Sub(h.highSince) >= RTTHighFor
}

// reset drops the history (a new candidate starts a fresh baseline).
func (h *rttHistory) reset() {
	h.samples = nil
	h.highSince = time.Time{}
}

func medianOf(s []rttSample) time.Duration {
	v := make([]time.Duration, len(s))
	for i, x := range s {
		v[i] = x.rtt
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}
