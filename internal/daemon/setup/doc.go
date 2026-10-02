// Package setup holds the local operations the CLI (and the TUI) run
// without a running daemon (ARCHITECTURE.md §7.4, spec sections 1, 2, 3, 5,
// 11 and 12):
//
//   - SetupHub: the non-interactive part of the hub wizard. The CLI asks the
//     questions (role, name, public IP, control port, sysctl) and passes the
//     answers; nothing here prompts.
//   - Join: joins this server to a hub as a node (dey:// link, CSR, pinned
//     CA, secrets, config, service).
//   - Uninstall, Backup, Restore, SetHubAddr and the helpers
//     DetectPublicIP, SuggestControlPort, JoinCommand and InstallerURL.
//
// Every operation takes an injectable filesystem Root ("/" when empty), an
// exec.Runner (only allow-listed programs run: ip, nft, systemctl), a clock
// and an optional Progress callback that receives one api.Step per state
// change (running, then ok/warn/skipped/failed). Group lookups, chown and
// the local API socket path are injectable too, so tests run unprivileged in
// a temporary directory without systemd or nftables.
//
// # Step order and re-runs
//
// SetupHub runs detect_ip, ca, hub_cert, firewall, sysctl, config, service;
// Join runs parse_link, keys, join, secrets, sysctl, config, service.
// config.yaml is written after every system change it describes and before
// the service starts, so a failure before the config step leaves no config:
// the next run starts over (it is not refused with DEY-I013) and reuses the
// CA and hub certificate already in secrets/ instead of generating new ones.
// A failing SetupHub step returns DEY-I014 {step} wrapping the cause (the
// cause's three lines are in the error's Detail). Join returns the hub's
// own codes (DEY-N001, N002, N006, N007, N009, …) unchanged for the network
// steps and DEY-I014 for local steps. Configuration validation problems are
// reported under the config step. Join prepares everything local (options,
// directories, the backend user) before the join step, because the hub
// spends the single-use token when it answers.
//
// # Backend user and directories
//
// The ca step (hub), the keys step (node) and Restore create the section 2
// directories with the modes of ARCHITECTURE.md §7.5 and make sure the
// system user and group deyroute exist (systemd-sysusers, as install.sh does,
// when it is missing). Without that group the new directories are
// root-only and the step reports a DEY-X032 warning whose Fix is to run the
// installer again.
//
// # Hub move
//
// A hub restored onto a new server keeps the old hub.public_ip unless
// RestoreOptions.PublicIP gives the new one; the CLI compares it with
// DetectPublicIP and asks. The hub certificate is then re-issued by the
// restored CA, so nodes keep trusting the hub; they learn the new address
// from `deyroute hub announce-move` or `deyroute node set-hub` (SetHubAddr).
// set-hub also takes a front target, wss://DOMAIN:PORT/SECRET, which
// switches the node into front mode (ParseHubTarget); a plain host:port
// clears front mode.
//
// # Front join
//
// A join link with a /SECRET path (dey://TOKEN@DOMAIN:PORT/SECRET#sha256:FP)
// joins through the CDN front: Join writes secrets/front.secret (0600) and
// registers the secret with the log redactor before the one-shot POST,
// sends the POST through front.DialControl (DialFront maps failures to
// DEY-N016/N017 and keeps the retry hints), and records node.front next to
// node.hub_addr = DOMAIN:PORT in config.yaml. A front failure never spends
// the token and removes the secret file again.
//
// # Step titles
//
// api.Step.Title is looked up in internal/i18n under the key
// "setup.step.<id>"; until such a key exists the English default in this
// package is used, so the i18n table can take over without code changes.
//
// # Restored events
//
// Restore cannot import events into state.db (the daemon owns it and may
// hold its lock). It writes the backup's events.ndjson to
// RestoreEventsPath (/var/lib/deyroute/restore-events.ndjson, mode 0600).
// The hub imports that file exactly once at start with
// ImportRestoredEvents(root, store.ImportEvents), which deletes the file
// after a successful import.
package setup
