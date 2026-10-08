# Changelog

All notable changes to DEYROUTE are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added — traffic monitoring, automatic tuning, screenshots

- **Traffic monitoring:** the hub counts the bytes of every tunnel on its
  listen ports in a second, verdict-free nftables table
  (`inet deyroute_stats`: it only counts and never drops; no conntrack except
  for the NAT rungs), keeps one hour in memory and minute, 30-minute and
  monthly history in `state.db` (with a size guard), and the CPU and RAM of
  the hub and every node. Shown in a TRAFFIC block on the dashboard and in
  `deyroute status`, a Traffic and load screen (`6) Diagnostics`, or `t` on
  the dashboard; 1h/24h/7d/30d, block or braille charts) and
  `deyroute stats` (`--period`, `--watch`, `--json`). Download and upload are
  always labelled; a hub that cannot count says why (`DEY-X061`) and never
  shows 0. Optional monthly quota per tunnel with alerts at 80 % and 100 %.
  `monitoring.enabled: false` removes the table.
- **Automatic tuning:** `deyroute optimize auto` (also the setup wizard's last
  question and `7) Optimize`) measures each server (RAM, CPUs, cgroup
  limits, kernel, BBR and fq, conntrack, NIC, containers) and lists every
  kernel and service change with its current value, new value, effect and
  reason before one confirmation (`--dry-run`, `--yes`, `--backends`,
  `--json`). Limit keys are only raised, conntrack is sized to the RAM,
  backend control ports are reserved, backends get `OOMScoreAdjust=300` and
  the hub a memory limit. Nodes follow the hub's plan when they connect.
  `optimize check` reports drift and overrides, `optimize status` shows every
  host, and `optimize revert` restores only the values nobody changed since.
- **Screenshots:** the README files and the guides show the real screens
  (setup wizard, menu, dashboard, adding a tunnel, nodes, traffic,
  `deyroute stats`, `optimize auto --dry-run`, doctor) as light and dark SVG
  pictures, drawn from a made-up demo deployment by `make screens`;
  `make check-docs` and `go test ./...` fail when they are out of date.

### Fixed — MSS clamping

- The MSS clamp on WireGuard and AmneziaWG interfaces now also sets an
  explicit size for packets entering the tunnel interface (its MTU minus 40)
  and clamps on the node's interface too, so TCP connections through a
  smaller tunnel MTU no longer stall on large packets.

### Added — tunnels through Cloudflare (front mode)

- When the direct path between the hub and a node is filtered, the node can
  reach the hub through a Cloudflare record, and now its **tunnels** go that
  way too, not only the control channel. `deyroute front enable --domain
  <record>` on the hub (port 2053 by default) prints the Cloudflare settings
  and the `deyroute node set-hub 'wss://…'` line for the node; `front status`
  and `front disable` complete it. Guide: docs/fa/front.md, docs/en/front.md.
- On such a node every backend client dials a local shim
  (`deyroute front-shim`, run next to it by `deyroute pair`), which opens one
  WebSocket through Cloudflare per connection, with the hub's own TLS inside
  and a signed preface; the hub accepts it only for a control port of that
  node's rungs. Transports that need UDP, a direct connection to the node or
  the node's real address are skipped for it (DEY-B012).
- Lab scenario S34: with the node's direct path to the hub cut, a tunnel
  carries 20 MB intact through a Cloudflare-like edge.

### Changed — clearer status and tuning output

- `deyroute status`, `deyroute optimize status` and the tuning plan
  (`optimize auto`) are laid out in titled sections. The tuning settings are
  grouped (speed and queues, buffers, connections and ports, keepalive,
  connection tracking, services), with short names and readable values
  (`32 MiB` rather than `33554432`), and each reason is shown once under its
  group. The node table has a header row; the top line wraps instead of
  running off the screen; every line fits the terminal, also a phone's
  60 columns. `--json` is unchanged.
- `deyroute status` on the hub starts with what needs attention: tunnels that
  are not UP, nodes offline and the warnings, in red or yellow and in plain
  words; one green line when everything works. Tunnel and node states and
  the event types are coloured (green, yellow, red).
- `deyroute optimize status` shows one line per group with what it means now
  ("BBR · fq", "up to 32 MiB per connection", "dead connections found in
  about 8 min"); `--details` lists every value with a short explanation of
  the group. In the menu, `7) Optimize` → `6) Settings in effect` opens each
  group on its own.

### Fixed — update refused between edge builds

- `deyroute update` refused a newer edge build (DEY-S011, "older than
  0.3.0") on a hub that uses automatic tuning or monitoring: every
  0.3.0-edge.N sorts before 0.3.0. The guards now start at 0.3.0-edge.15,
  the first build with those keys and with front mode. A hub on an older
  edge build still runs the old guard: undo the tuning once
  (`deyroute optimize revert`), update, then `deyroute optimize auto` again.

### Fixed — Reality decoys

- With the first built-in decoy, `www.microsoft.com`, `xray/reality` failed
  the ladder test on every lab run. The built-in decoys are now `dl.google.com`,
  `www.speedtest.net` and `www.samsung.com`, each tested with both Reality
  rungs; a hub that used the old list moves to the new one by itself.
- The hub's decoy check also wants HTTP/2 now (REALITY needs it of its
  target), so a site without it is never chosen.

### Added — the working system (phases 1–8)

- **Install and lifecycle:** one-line installer (`install.sh`: GitHub
  releases, mirror, offline `--local`, minisign + SHA256SUMS), `deyroute setup`
  (hub wizard, at most five questions), `deyroute join` with CA pinning,
  `update` / `--rollback` (signed, tunnels keep running), `uninstall`
  (units, nftables table, kernel settings, files, system account),
  encrypted `backup` / `restore`, hub move (`hub announce-move`,
  `node set-hub`).
- **Hub daemon:** mTLS control channel, single-use join tokens, node
  registry with heartbeats, firewall manager (`table inet deyroute`, `@nodes`,
  join window), events and Telegram, backend downloads through a node when
  GitHub is blocked, assets for node updates.
- **Tunnels:** default ladder of eight rungs (Backhaul wssmux/tcpmux,
  Rathole noise, FRP tcp, Xray-Reality, Hysteria2, Waterwall reverse-reality,
  direct/native), warm rungs, skipped rungs with a yellow warning, backup
  nodes, port maps (up to 64, ranges), 4-stage port check, canary failback,
  `test-ladder`, `diag speed`, `diag probe`.
- **Failover engine:** probes (path, node service, control), the state machine
  of spec 9, quarantine, anti-flapping, failback with backoff,
  `backend_crash` detection on hub and node, reconcile after a restart.
- **Node agent:** every control command (backend install/render, units, NAT,
  probes, fetch, self-update, certificate rotation, logs, doctor); never an
  open proxy.
- **Operations:** doctor (15 rules, bundle without secrets), optimize
  profiles with BBR detection, security audit, token and CA rotation, TLS
  show/renew (auto, ACME, custom), backend updates with automatic rollback,
  signed manifest updates.
- **CLI and menu:** every command of spec 14 with `--json` (schema 1,
  `docs/cli-json.md`) and exit codes 0/1/2/3; the full English menu of spec 6
  with dashboard, wizards, ASCII mode for dumb terminals and a guide screen
  on servers that are not set up yet.

### Changed

- Rung 4 of the default ladder is `frp/tcp` (frps cannot serve `frp/wss`
  without a separate TLS terminator; QUESTIONS.md C.31).
- `uninstall` also removes the `deyroute` user and group (QUESTIONS.md C.30).
- Offline installs (`install.sh --local`) need `SHA256SUMS` and
  `SHA256SUMS.minisig` next to the archive; nothing unverified is installed.
- Running the installer again repairs the server (`deyroute setup --repair`:
  unit files, directories, service restart; config untouched).
- Hysteria2: the node serves its own pinned certificate; the tunnel's TLS
  key (internal CA, ACME or the owner's) never leaves the hub.
- Manual switches are recorded as `switch_transport` / `switch_node`
  (`manual_switch` in Telegram settings is kept as an alias).
- Edge builds stop being "latest" once a stable tag exists; stable releases
  are signed with the same Go signer.

### Fixed (spec audit of 2026-10-01)

- Join command shown on a plain screen so it can be copied whole; long lines
  wrap instead of being cut; pages taller than the window keep the error's
  code, Why and Fix visible; `0` cancels a confirmation instead of running it.
- Retry after a failed Add-tunnel step restarts the saved tunnel; line mode
  works with piped input; the ladder order is editable in Simple mode;
  "DEGR (service down)" is shown; a menu restore reports a moved hub address.
- Canary failback waits for the doubled delay after a failed failback; UDP
  rungs wait for a passed UDP probe; certificate renewals that restart the
  active transport say so.
- Tunnel logs are rotated (20 MB, 5 compressed files).
- `update --rollback` works when the daemon is down and on nodes.
- The automatic backup before `config apply` keeps the running configuration.
- A changed control port is announced to the nodes; the firewall keeps the
  listening port protected until the restart.
- `optimize revert` keeps `ip_forward` while WireGuard/AmneziaWG forwards.
- `direct/haproxy` finds the hub's haproxy; port check stage 3 really tests a
  free port and the wizard shows firewall and node warnings.
- Node removal texts match the refusal for a tunnel's only node.
- Flaky tests made robust (failover pause, hub diag probe).
- ACME from the menu and the CLI: `hub.domain` (Security > TLS certificates,
  `deyroute security tls domain`), the ACME e-mail and the Cloudflare DNS-01
  token file (Advanced, `deyroute security tls acme`; stored 0600 in
  `secrets/cloudflare.token`, never in `config.yaml` or logs),
  `tunnel add|edit --tls-mode`; ACME Fix lines name these commands, and a
  changed domain gets a new certificate at the next renewal.
- A port closed by the host firewall can be opened by deyroute after a typed
  confirmation of the exact command: `deyroute port check <port> --open`
  (`--yes`; exit 3 without a terminal), Ports/Diagnostics > Check port and
  the Add-tunnel wizard (which now stops at ports the firewall or the node
  check reports closed: open, change, skip or keep). The hub
  (`PortOpenFirewall`) checks again and runs only the argv it builds from the
  firewall found and the typed port (`DEY-P032` when that is not the
  confirmed command, `DEY-P033` when it fails); nftables table and chain
  names must be plain identifiers.
- Hub move from the menu: `10) Backup & Restore` → `3) Announce hub move` on
  a hub and `3) Set hub address` on a node (saved directly when the node
  agent is stopped, as `node set-hub` does); a menu restore asks the
  moved-hub question and its typed-yes confirmation states the address
  change and the re-issued certificate.
- The menu on a node server marks the hub-only items `(hub only)` and
  explains them instead of answering `DEY-X009`; Diagnostics and Settings
  list only what works on a node (logs, doctor, uninstall).
- Per-port probe kind (`auto|tcp|tls|http`, Advanced) from the menu and the
  CLI: `port add --probe`, the new `port set <tunnel> <ports> --probe`,
  Ports > `Probe kind *`, and a probe question in Ports > Add port and the
  Advanced Add-tunnel wizard; checked like `ports[].probe` in `config.yaml`
  (`DEY-C013`, UDP maps stay `auto`); a kind change restarts nothing.
- `DEY-P016`/`DEY-C015` no longer advise "use a range" (a range counts one
  port map per port); they advise splitting the ports over several tunnels.
- The busy-port option of the Add-tunnel wizard reads `Skip`, as in the spec.
- LAST EVENTS (menu and `deyroute status`) shows a short word for every event
  type (`node offline`, `rolled back`) and caps the tunnel and type columns,
  so a long id no longer pushes the messages off the screen.
- Nodes > Test and `deyroute node test` label the node's facts with human
  values (`Memory 3.8 GiB`, `Uptime 10d 00:02`); the join command expires
  `in 15 minutes`.
- Aligned screens: label columns per block, pick lists in columns, a padded
  rung status, an indented doctor summary, one numbering and prompt style,
  one blank line before a form hint.
- `?` on an empty answer shows the help of text-input screens, whose footer
  names only the keys that work there (Enter, Esc, `?`).
- Fixed answers are numbered lists with `(current)`: policy, ladder profile
  and TLS mode in Edit tunnel; backup node, policy and TLS mode in the
  Advanced Add-tunnel wizard.
- The wizard's backup node shows the fixed backup warning and the progress
  screen reports `backup <id> ready (warm)`.
- Menu wording: no `*` in page titles, `default ladder`, no nested
  parentheses, `→` in Unicode mode, title separators in i18n, the language
  named in Settings.
- Line mode: the hint says an empty line is Enter; a text field takes each
  line as the whole answer and `?` alone shows the help.
- The Logs screen formats deyroute's JSON lines (`12:41:03 WARN  failover …
  tunnel=main`) and marks a tunnel log's lines `[hub]` / `[node]`.
- Notifications shows whether Telegram is on, its chat and events (Status
  `hub.telegram`) and offers them as defaults; Restore lists this server's
  backups by number.
- The hub's progress-step titles come from `internal/i18n` (`hub.step.*`,
  `hub.title.*`); the English fallback table in the hub is gone.
- The docs show the real menu and CLI output of the (single-use) join
  command and of the Add-tunnel progress screen.
- `deyroute update` and the menu show the changelog text of the release (its
  `CHANGELOG.md`, now published and listed in the signed `SHA256SUMS`,
  fetched through a node or the mirror) instead of only the release page URL.
- A kernel without BBR (or `tuning.bbr: false`) skips only
  `tcp_congestion_control`; `net.core.default_qdisc = fq` is applied.
- `optimize apply` shows what each node skipped (`node <id>: …`) and sends
  the hub's `tuning.bbr` to the nodes instead of their own default.
- Doctor rule R11 no longer reports BBR on a kernel without it or with
  `tuning.bbr: false`, and its Fix names the applied profile.
- `aggressive` is recommended only from 4 GB RAM: the menu names the
  profile for the hub's RAM and warns before `aggressive` on a smaller hub,
  and every smaller server reports a warning.
- The panic record in `deyroute.log` has `ts`, the real component (`hub`,
  `node` or `cli`), valid JSON and the secret filter.
- A wrong command line is `DEY-C025` in the three-line format (exit 1) with
  the `--help` of its command.
- The doctor summary is colored only on a terminal.
- Doctor checks every port map (eight at a time) instead of the first 16.
- Doctor rule R15 reports only expired join tokens the hub could not remove
  and, as info with its expiry, a valid token made with a long `--ttl`; its
  Fix no longer asks for a restart that does not remove them.
- `DEY-P014` is raised when the node cannot connect to the port (port check
  in the CLI and the menu, `node_error` in `--json`, the doctor bundle).
- Hub changes (join, menu, CLI, node IP) build on the applied configuration
  and are refused with the new `DEY-C026` while `config.yaml` holds an edit
  that is not applied, instead of taking it over without config apply's
  checks; a join spends its token only when it succeeds.
- `security.firewall_managed: false` that takes effect at hub start (edit
  and restart, restore) removes a table `inet deyroute` left from before.
- The node agent pings the systemd watchdog only while its monitor makes
  progress (a deadlocked lock or a stuck monitor gets it restarted).
- Heartbeats sent before the node agent listed its units say so; the hub
  keeps the last unit list and runs the per-stream cleanup on the first
  heartbeat with the list.
- The node agent's last error is shown by `deyroute node list` (and
  `last_error` in `--json`, the status) and in the menu's node list.
- The join command keeps ` --version X.Y.Z` after the spec's form, recorded
  as QUESTIONS.md C.36 (the node must install the hub's release); `join` on
  an incompatible node no longer promises an automatic update and names
  the installer's `--version` instead.
- The security docs say that `security audit` warns about every unexpired
  join token and that `doctor` reports the long-lived and expired ones.
- Lab: the client is a real Xray VLESS+ws+tls client with a real Xray server
  behind the tunnel (S04, S08, S19, S32); S04 also checks TLS errors, that
  the node's service never sees the client and that traffic exits at the
  node (QUESTIONS.md C.37 records the in-namespace blocker).
- Lab S17 probes every Forward transport (xray/reality, hysteria2/udp,
  WireGuard/AWG, direct/native) from inside with a client built from the
  hub's credentials, and the node's UDP listeners too.
- Lab S32 (new) measures the phase 4 manual switch between three backends
  with the client every 200 ms and fails above a 3 s outage.
- Lab S33 (new) runs wireguard/kernel (where the kernel has it) and
  awg/userspace with a TCP and a UDP port and diffs `nft list ruleset` and
  the interfaces before the tunnel and after `tunnel delete`.
- `docs/en/benchmarks.md` and `docs/en/acceptance.md` record the lab
  numbers (binary size, join, S04, S07, S08, S09, S12, S19, S25, S32);
  real-server rows stay pending. The lab's test service listens with a
  backlog of 1024 (python's 5 made S25 fail 75 of 500 connections).
- Lab S01 and S03 assert phase 2's join < 30 s (installer or `deyroute join`
  to node online); S08 asserts the client is back <= 3 s after the switch.
- Backhaul on tunnels with TCP and UDP port maps: the TCP-only Backhaul
  rungs (tcpmux, ws, wss, wsmux, wssmux) stay in the ladder and run a
  `backhaul/udp` companion process for the UDP maps in the same unit
  (`deyroute pair`, second control port; QUESTIONS.md C.42); a node accepts a
  pair unit only when both command lines are Backhaul binaries. Lab S31
  (new) checks TCP and UDP through `backhaul/tcpmux` with the real Backhaul
  and the restart of the pair when the companion dies.
- A new tunnel whose rung 1 the plan skipped (a UDP rung before the UDP
  probe passed) starts on the next rung instead of trying the skipped one.
- `tunnel add` and `port add` report a listen port that another firewall on
  the hub blocks (ufw, firewalld, iptables, another nftables table) as a
  yellow step with `DEY-P013` and the command; `port check --open` or the
  menu runs it after confirmation.
- Waterwall: `core.json` gets min(4, CPU) workers of the side that runs it
  (nodes report their CPU count in the hello), the JSON is validated before
  every start (`DEY-B040`/`DEY-B041`) and a start failure is `DEY-B043`
  with the log tail.
- Waterwall and gost run with `MemoryDenyWriteExecute` (checked with the
  pinned binaries on systemd 255); only rathole keeps the exception.
- `advanced.backhaul_web_port` is refused by validation (`DEY-C013`) instead
  of silently skipping every Backhaul rung: the pinned Backhaul cannot keep
  its stats page on 127.0.0.1. A config.yaml that already has it still
  starts the hub: the key is ignored with a warning in hub.log and the next
  change writes the file without it.
- The menu says "client IP: preserved" for direct/haproxy only with the
  tunnel's `advanced.proxy_protocol` (`proxy_protocol` in TunnelInfo).
- `port remove`, `tunnel backup remove` and `security firewall disable` ask
  for a typed `yes` and stop with exit 3 on a non-terminal without `--yes`.
- `tunnel backup add|remove --node` takes one node; a comma list or a
  repeated flag is a usage error instead of being sent as one id or
  dropped.
- `config edit` keeps the edited copy (and names it) when the same invalid
  file is saved again.
- The `--json` error document always has the `log` field.
- Probe-kind questions in the menu are numbered answers.
- Backend docs no longer say that sha256s are missing; QUESTIONS.md records
  the rathole/Waterwall glibc and OpenSSL 3 needs (C.38), frp quic/kcp UDP
  (C.39), the Waterwall graph sources (C.40) and the awg runtime directory
  (C.41).

### Fixed (found on real servers)

- The control channel between a node and a hub in Iran was cut right after
  the TLS handshake (the node reconnected every 45 s and never stayed
  online). The node's ClientHello now looks like ordinary HTTPS: ALPN `h2`
  and a cover server name (`node.control_sni`); the hub accepts `h2` and the
  old `deyroute/1` (QUESTIONS.md C.43).
- The setup and join wizards ask one question per block: the step, the
  question, the numbered answers one per line and a line that says exactly
  what to type and what Enter alone takes; they end with a summary and the
  next steps. Menu choices show the same hint.

### Fixed (found by the integration scenarios)

- `backend_crash` was not emitted when systemd restarted a killed backend.
- A node briefly reported itself incompatible before the hub's hello arrived.
- Kernel-setting warnings were printed twice by setup and join.
- Join-command `--ttl` outside 1m–24h now names the allowed range.
- A node with `net.ipv4.ip_forward` already on (Docker, another VPN) routed
  what the hub sent into a WireGuard/AWG tunnel to other hosts; the tunnel
  interface is now confined in the forward chain of `inet deyroute` (S17).
- The first start of an awg/userspace or wireguard/kernel rung failed with
  `DEY-B071`/`DEY-B070` while the node's UDP reachability echo still held the
  listen port: the hub now closes the echo when its probe is done, and the
  configuration is retried for up to 15 s (S33).

### Added — documentation, integration scenarios, backend hashes

- User guides in Persian and English (`docs/fa/`, `docs/en/`): install, join,
  first tunnel, backup node, troubleshooting with error codes, filtering FAQ,
  hub move, backup/restore, update/uninstall, security; `docs/en/benchmarks.md`
  (method; results not measured yet).
- Integration scenarios S01–S30 of spec section 17 (`test/integration/`):
  hub, nodes and client in systemd containers with real units and nftables;
  `run.sh` builds the release files and reports PASS/FAIL/SKIP per scenario.
  CI runs them for `main` and tags, and on any branch by hand.
- `scripts/manifest-hashes`: fills and re-checks the sha256 of every backend
  in `backends.yaml`. All hashes are now recorded (previously empty, so every
  download-based backend was refused with DEY-S006).
- `scripts/build-backends.sh` and the `backend-builds` workflow: reproducible
  builds of amneziawg-go v1.0.4 and chisel v1.12.1, published on the release
  `backend-builds` (QUESTIONS.md C.29).

### Fixed

- chisel pointed at v1.12.1 release assets that upstream never published
  (HTTP 404); awg/userspace pointed at an unset mirror.

### Added — phase 0 (skeleton)

- Repository layout of section 15, Go module with pinned dependencies.
- `deyroute version` (version, commit, build date, Go version).
- DEYROUTE banner (Unicode and ASCII `#` variants) and the fixed main menu of
  section 6; every item answers "not implemented yet".
- DEY error system with the full code catalog (`internal/errors/codes.go`),
  generated `docs/ERRORS.md`, and a test that rejects codes without Why/Fix.
- All UI strings in `internal/i18n/en.go`.
- Makefile, `.goreleaser.yaml`, `.golangci.yml`, GitHub Actions CI
  (lint → unit → build → smoke on Tier 1 images → integration → release).
- `QUESTIONS.md` with open owner questions and implementation defaults.
