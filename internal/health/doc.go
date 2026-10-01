// Package health implements the probes of the health engine (spec sections
// 9, 10 and 12): the path probe (auto|tcp|tls|http) the hub runs through a
// tunnel, the TCP/TLS/HTTP probes a node runs for node_service, the UDP
// nonce echo (client and server), the canary loopback TCP echo, the built-in
// traffic generator of `deyroute diag speed`, the bounded worker pool every
// probe runs in (size 8) and the RTT history behind the DEGRADED rule
// ("RTT above 3x the median of the last 10 minutes for 60 seconds").
//
// The package only talks to addresses its caller names; it never contacts
// any other server (spec section 0, rule 7: no telemetry).
package health

import "time"

// Probe kinds of a port map (section 9, config key ports[].probe).
const (
	KindAuto = "auto"
	KindTCP  = "tcp"
	KindTLS  = "tls"
	KindHTTP = "http"
)

// Defaults of section 9 (probe cadence), section 10 (UDP probe) and
// section 12 (worker pool).
const (
	// DefaultTimeout is probe_timeout_s.
	DefaultTimeout = 3 * time.Second
	// DefaultInterval is probe_interval_s.
	DefaultInterval = 5 * time.Second
	// AllPortsInterval is how often every port map (not only the probe
	// port) is probed for reporting.
	AllPortsInterval = 60 * time.Second
	// PoolSize is the number of probes that run concurrently.
	PoolSize = 8
	// UDPTries and UDPTimeout are the UDP echo attempts and the wait for
	// each one.
	UDPTries   = 3
	UDPTimeout = 2 * time.Second
	// UDPRecheckInterval is how often the UDP probe is repeated.
	UDPRecheckInterval = 30 * time.Minute
	// DegradedFactor, DegradedWindow and DegradedFor are the RTT part of
	// the UP -> DEGRADED rule.
	DegradedFactor = 3.0
	DegradedWindow = 10 * time.Minute
	DegradedFor    = 60 * time.Second
	// HistoryMax caps the samples kept by a History.
	HistoryMax = 1000
)

// Short failure reasons put in Result.Err. They end up in events and in
// Telegram messages ("reason: probe failed 3x (timeout)"), so they are
// deliberately terse and stable.
const (
	ReasonTimeout      = "timeout"
	ReasonRefused      = "connection refused"
	ReasonReset        = "connection reset"
	ReasonUnreachable  = "unreachable"
	ReasonCanceled     = "canceled"
	ReasonClosedNoData = "closed without data"
	ReasonNotTLS       = "not a TLS service"
	ReasonNotHTTP      = "not an HTTP service"
	ReasonNoEcho       = "no echo"
	ReasonEchoMismatch = "echo mismatch"
	ReasonBadAddress   = "invalid address"
	ReasonBadKind      = "unknown probe kind"
)
