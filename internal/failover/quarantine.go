package failover

import (
	"time"

	"github.com/localroot4/deyroute/internal/state"
)

// Quarantine rules (section 9): a failed candidate is not tried again for
// base (quarantine_s, 10 minutes by default); each repeated failure doubles
// the duration up to QuarantineMax (1 hour). An entry stops excluding its
// candidate when Until passes, and is forgotten (the next failure starts at
// base again) once it has been expired for QuarantineForget, or as soon as
// the candidate passes a probe.

// Quarantined reports whether c is excluded by a quarantine entry at now.
func Quarantined(q map[string]state.Quarantine, c state.Candidate, now time.Time) bool {
	e, ok := q[c.Key()]
	return ok && now.Before(e.Until)
}

// ApplyQuarantine records a failure of c at now and returns the duration
// applied: base for a first failure, twice the previous duration for a
// repeat, never more than max(QuarantineMax, base). q must be non-nil.
func ApplyQuarantine(q map[string]state.Quarantine, c state.Candidate, now time.Time, base time.Duration) time.Duration {
	if base <= 0 {
		base = DefaultQuarantine
	}
	ceiling := QuarantineMax
	if base > ceiling {
		ceiling = base
	}
	d := base
	if prev, ok := q[c.Key()]; ok && prev.Duration > 0 {
		d = prev.Duration * 2
	}
	if d > ceiling {
		d = ceiling
	}
	q[c.Key()] = state.Quarantine{Until: now.Add(d), Duration: d}
	return d
}

// PruneQuarantine forgets entries that expired more than QuarantineForget
// ago.
func PruneQuarantine(q map[string]state.Quarantine, now time.Time) {
	for k, e := range q {
		if now.Sub(e.Until) >= QuarantineForget {
			delete(q, k)
		}
	}
}
