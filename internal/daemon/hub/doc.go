// Package hub is the hub daemon (deyroute-hub.service, `deyroute daemon hub`;
// spec sections 1, 3, 4, 5, 9, 11, 13 and 14; ARCHITECTURE.md section 7).
//
// One Hub value owns every long-running part of the hub process:
//
//   - config.yaml as the single source of truth (section 4): every change is
//     an atomic config.Mutate followed by an apply; runtime facts live in
//     state.db (bbolt);
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
//   - the Local API on /run/deyroute/daemon.sock used by every CLI command and
//     the TUI.
//
// # Extension
//
// The package is built by several engineers in turn. Each concern lives in
// its own file. Hook methods in hooks.go (reconcileAll, onNodeRemoved,
// activeHubSides, stopEngines, tunnelView) are the places where the tunnel
// controller plugs in; the Local API methods that are not implemented yet
// answer DEY-X008 from the embedded pendingLocal (pending.go). A later file
// implements a method simply by defining it on *local, which shadows the
// pending one; the pending method is then deleted.
//
// # Testability
//
// Every filesystem path is below Options.Root, every program runs through
// Options.Runner, and the clock, listener address, socket path, systemd
// manager, fetcher, ownership lookups and all timings are injectable, so the
// whole daemon runs unprivileged in a temporary directory without systemd,
// nftables or root.
package hub
