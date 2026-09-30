// Package failover is the per-tunnel health and failover state machine of
// section 9: one actor goroutine per tunnel (Engine.Run) with the seven
// states INIT, STARTING, UP, DEGRADED, SWITCHING, DOWN and PAUSED,
// candidate selection per policy (NextCandidate), quarantine with doubling,
// anti-flapping, failback (blind in phase 5, canary in phase 8), the DOWN
// retry backoff, reconciliation after a hub restart (Reconcile) and the
// owner's manual commands (pause, resume, switch, reset, test ladder).
//
// The package is pure and deterministic: every side effect (starting and
// stopping units, probes, node state, events, persistence) goes through the
// Actions interface implemented by the daemon, and all timing comes from a
// Clock (FakeClock in tests). It imports no I/O package.
//
// Timeline with the defaults (probe_interval_s 5, fail_threshold 3): a
// blocked active rung fails three probes (≤ 15 s + probe timeouts), the next
// candidate is started and polled every second for up to 15 s, which keeps
// the switch within the 35 s p95 goal of section 9.
package failover
