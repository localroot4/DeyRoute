// Package node is the node agent (deyroute-node.service, spec sections 3, 5,
// 7, 9, 10, 11 and 13; ARCHITECTURE.md section 7).
//
// The agent keeps one outbound HTTP/2 stream to the hub's Control API
// (api.ControlClient: mTLS with the node certificate, reconnect with 1-30 s
// exponential backoff), sends its hello and a heartbeat every 5 seconds
// (CPU, RAM, the state of every deyroute-tun@ unit, the last error) and
// executes the hub's commands. It takes no decisions of its own: it renders
// what the hub sends, starts and stops what the hub asks for and reports.
//
// Commands (api.Cmd*): backend.install/render/remove, unit.start/stop/
// restart/status, probe.tcp/tls/http, probe.udp_listen,
// port.check_from_outside, fetch.proxy, http.post, sysinfo, metrics,
// logs.tail (optionally followed), doctor.collect, self.update, set_hub,
// uninstall, cert.renew/cert.install, echo.start/echo.stop, firewall.apply,
// sysctl.apply and speed.serve. Rendered instances, the hub's NAT rules and
// the canary echo ports survive agent restarts in
// /var/lib/deyroute/node-state.json.
//
// # Front mode
//
// A node whose config has node.front.secret_file reaches the hub through
// the hub's CDN front (front.DialControl: a WebSocket to
// wss://<domain>:<port>/<secret>/c) instead of a direct TCP connection; the
// pinned mTLS control protocol runs unchanged inside it. The dial is built
// from config.yaml on every (re)connect, so a new hub_addr, edge_ip or
// secret applies without a restart; uploads and asset downloads (self.update)
// use the same dial. A failed dial is DEY-N016 (the front is unreachable) or
// DEY-N017 (it did not accept the node) and carries the front's Retry-After
// and Permanent hints, so a 404 or a challenge backs off slowly and is
// logged once. node.hub_addr is the front's domain:port; the status shows it
// with a "via front" marker, and the doctor section "connectivity" reports
// the last dial error code (never the path or the secret). A hub-originated
// set_hub with a plain host:port is ignored while the node is in front mode;
// only the owner's local "deyroute node set-hub" changes the mode.
//
// Security rules enforced here (section 11): backend.render only writes
// below /etc/deyroute/backends/<backend>/<tunnel>/ with relative file names
// and bounded sizes; unit commands may only run the instance backend's
// binaries installed below /var/lib/deyroute/bin/<backend>/<version>/ and
// the deyroute relay / wg subcommands, and may not redirect the dynamic
// loader through their environment; fetch.proxy only downloads https URLs
// (redirects included) and never executes anything; self.update installs
// only an ELF executable for this architecture; http.post only reaches
// https://api.telegram.org; the node firewall table (inet deyroute) holds
// nothing but NAT rules (the hub's and those of the currently started
// instances), never opens or closes other ports and never redirects SSH.
// The secrets of rendered files and unit environments are registered with
// the log redactor; nothing secret is logged.
//
// The Local API on /run/deyroute/daemon.sock serves Status, Logs,
// NodeSetHub, StopAll and DoctorCollect (for this node); every other method
// answers DEY-X009 (it needs the hub).
//
// Every filesystem path is below Options.Root and every external program
// runs through Options.Runner, so the agent runs unprivileged in tests.
package node
