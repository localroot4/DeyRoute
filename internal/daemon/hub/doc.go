// Package hub is the hub daemon (deyroute-hub.service, `deyroute daemon hub`;
// spec sections 1, 3, 4, 5, 9, 11, 13 and 14; ARCHITECTURE.md section 7).
//
// One Hub value owns every long-running part of the hub process:
//
//   - config.yaml as the single source of truth (section 4): every change is
//     an atomic write of the applied configuration (mutate; refused with
//     DEY-C026 while config.yaml holds an edit that is not applied)
//     followed by an apply; runtime facts live in state.db (bbolt);
//   - the Control API server (mTLS, HTTP/2) and its handlers: Join (one-time
//     tokens, CSR signing, node registration), Authenticate (certificate
//     fingerprint, node IP change → node_ip_changed), Session (the node
//     registry: hello and version compatibility, heartbeats, control RTT,
//     online/offline detection), Upload (fetch.proxy payloads) and Asset (the
//     hub's own deyroute binary for every architecture);
//   - node commands (Call/Stream with DEY-N003 for offline nodes) and the
//     download chain of section 5 ([via-node, direct] once a node joined);
//   - the event bus (state.db ring, events.log, Telegram, subscribers);
//   - the firewall manager (table inet deyroute: @nodes, join window, listen
//     ports, NAT of the active candidates), debounced;
//   - the tunnel controllers (tunnels.go, tunnel_*.go): one per enabled
//     tunnel, planning, installing and rendering every rung on every node,
//     running the failover engine with the hub's Actions (server side
//     first, the start check with the last 40 log lines, NAT only for the
//     active candidate), the canary, the 30-minute re-check, the 60-second
//     all-ports report and the crash watch (backend_crash);
//   - the operations (ops_*.go, jobs.go): diag speed, doctor, optimize,
//     security (rotate tokens and CA, TLS, firewall, audit), Telegram,
//     updates of deyroute and the backends, and the background jobs (decoy
//     SNI check, TLS renewal, the optional update check, metrics);
//   - the Local API on /run/deyroute/daemon.sock used by every CLI command and
//     the TUI; every method of api.Local is implemented.
//
// # Concurrency
//
// Serve owns every goroutine and waits for all of them. Tunnel operations
// (Local API changes, reconcile, node removal, cleanups) are serialised by
// the tunnel manager's opMu; the failover engines never take it. Inside a
// controller, unitMu serialises the starts, stops and restarts of units and
// canWork the canary slot. The firewall is computed and applied under one
// lock, so a newer table is never replaced by an older one.
//
// # Testability
//
// Every filesystem path is below Options.Root, every program runs through
// Options.Runner, and the clock, listener address, socket path, systemd
// manager, fetcher, ownership lookups and all timings are injectable, so the
// whole daemon runs unprivileged in a temporary directory without systemd,
// nftables or root.
package hub
