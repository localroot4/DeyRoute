# Changelog

All notable changes to DEYROUTE are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added — Telegram in the security audit

- `deyroute security audit` has a new check `telegram`: a warning while
  Telegram notifications are on, because every message hands the bot token
  to an online node; `ok` when they are off. With Telegram on, the audit is
  no longer clean (DR-27).

### Changed — a neutral name for the internal CA

- A new internal CA (at `setup` or `security rotate-ca`) is named
  `<hub name> CA` and has no organization; before, it was
  `DEYROUTE CA <hub name>` with organization `DEYROUTE`. The hub sends the
  CA with its certificate to every client of the control port. Existing
  CAs keep their name until `rotate-ca`; nothing breaks either way.
  Hub and node certificates keep the organization `DEYROUTE` for 0.3.x
  peers; this release already accepts them without it, so a later minor
  release can drop it (DR-26).

### Added — lab scenario for a node replacement

- Lab S39: the operator replaces a tunnel's primary node (join the new
  node, `tunnel edit --node NEW --yes`, `node remove OLD --yes`). The
  tunnel carries traffic through the new node 7.2 s after the edit, the old
  node runs no tunnel unit and leaves `@nodes` (DR-23).

## [0.3.1] - 2026-10-29

### Added

- `deyroute setup --public-ip A.B.C.D` and `--public-ip6 X` store exactly
  that hub address (canonical form) and skip the wizard's IP question;
  `--allow-private-ip` accepts a private, CGNAT or loopback address for test
  labs (never a link-local one). A refused address is `DEY-C013` before any
  setup step; the three flags are refused with `--role node` and on
  `deyroute join`. `setup --json` adds `public_ip_source` (`flag`,
  `detected` or `wizard`). Without the flag nothing changes: a private
  detection still only warns (`DEY-I021`), so automation on a NAT or
  floating-IP server must pass `--public-ip` (docs/en/install.md, lab S01,
  DR-07).
- `deyroute join --sysctl off|balanced|auto` (default `balanced`; when
  given, the kernel question is not asked; `off` writes nothing under
  `/etc/sysctl.d`) and `--reserve-ports LIST` (ports and ranges, the flag
  repeats, at most 64 entries, at least 1024 ephemeral ports left free;
  with `off` it is `DEY-C013`). `join --json` adds `reserved_ports` (never
  null). The entries are kept in `/var/lib/deyroute/reserved-ports` and
  reserved again by every profile the node applies except `off`
  (docs/en/join.md, docs/en/tuning.md, lab S23, DR-09).
- A node joined with `--sysctl off`, or whose owner answered no to the
  kernel question, refuses the hub's kernel tuning (`optimize apply` and
  `optimize auto`) with the new `DEY-N052`; a revert to `off` still runs.
  The switch is the root-only marker `/var/lib/deyroute/hub-tuning-off`: to
  lift it, remove it on the node and re-apply the hub's profile as
  `deyroute optimize status` shows it. Nodes joined with an earlier version
  keep following the hub (QUESTIONS.md C.65, DR-09).

### Changed

- The `balanced` and `aggressive` profiles a node applies, at join or
  pushed by the hub, now also reserve the backend control range
  `30000-31999` in `net.ipv4.ip_local_reserved_ports`, as `auto` already
  did (a set union: existing reservations stay). Migration: on the hub,
  re-apply the profile `deyroute optimize status` shows (`deyroute optimize
  auto` on an auto hub, otherwise `deyroute optimize apply --profile <that
  profile>`) so the online nodes reserve the range now (QUESTIONS.md C.64,
  DR-09).

### Fixed

- `deyroute setup --help` said `--yes` applies the balanced kernel profile;
  it applies the automatic profile, and the help now says so (`deyroute
  optimize status` shows it). The hidden `join --yes` keeps its own text: a
  join applies balanced (docs/en/install.md, DR-19).
- The sysctl backup recorded the live union of
  `net.ipv4.ip_local_reserved_ports` as deyroute's value, so a later apply
  followed by a revert or uninstall also removed a port another program
  had reserved in between. It now records only the entries deyroute
  requested; the kernel write is still the union (DR-09).
- `deyroute optimize check` no longer reports the reserved ports as drift
  when another program adds or removes its own reservation; only a port
  deyroute wants that is missing counts (DR-09).

### Added — the firewall table comes back by itself

- The hub and every node now check their table `inet deyroute` every 60
  seconds and apply it again when it is missing or changed (`nft flush
  ruleset` by an admin or a provider script, `ufw reload` on some systems,
  another tool). The new key `security.firewall_reassert_s` sets the
  interval (10-3600 seconds, 0 = off; absent = 60). A node checks only
  while it has NAT rules (WireGuard/AmneziaWG rungs, Hysteria2 port
  hopping) and no longer keeps a flushed table gone until its rules
  change. Nothing is checked with `security.firewall_managed: false`. The
  hub records the warning event `firewall_reapplied` (reason `missing` or
  `drift`, at most once per 10 minutes per reason; Telegram sends it when
  `hub.notify.telegram.events` names it); a node writes the same warning
  to `node.log` (docs/en/security.md, docs/en/monitoring.md, lab S37).
- Releases before 0.3.1 do not know `security.firewall_reassert_s` or
  `firewall_reapplied` in `hub.notify.telegram.events`: while either is
  set, `deyroute update --rollback` and `update --version` to such a
  release are refused (`DEY-S011`), and a node refuses a self.update to
  such a release while its own `config.yaml` sets the key.

### Changed — unknown control-port sources are limited per address

- A node whose public IP changed reconnects through the admission for
  unknown sources. That admission was one global bucket of 6 new
  connections per minute, which a single scanner could use up. Each
  source address now has its own budget of 3 per minute (burst 3; the
  dynamic sets `@ctl_unknown4`/`@ctl_unknown6`, 10-minute buckets) and all
  unknown sources together at most 30 per minute. New keys
  `security.unknown_control_rate_per_source` (default `3/minute`) and
  `security.unknown_control_rate` (default `30/minute`) take nft rates.
  When nft or the kernel refuses the per-source form, the hub applies the
  earlier single rule at `unknown_control_rate`, keeps it until it
  restarts and logs `nft refused the per-source limit of the control
  port` once (docs/en/security.md, lab S27).
- **Migration:** nothing to do; the hub applies the new rules at its next
  start. While either new key is set, a rollback below 0.3.1 is refused
  (`DEY-S011`).

### Fixed — port check behind a per-source limit

- `deyroute port check` read an nftables rule of another firewall that
  adds the source to a set with a per-element limit (`add @set { ip saddr
  limit rate … }`, `meter`) as never matching, so a port behind it was
  reported blocked; it now counts as a rate-limited accept, like
  `limit rate … accept`.

### Added — restore address flags, nodes follow an up-to-date hub

- `deyroute restore` takes `--public-ip IPv4`, `--public-ip6 IPv6` (only
  with `--public-ip`) and `--keep-address` for a restored hub. `--json`
  adds `address_source` (`flag`, `detected`, `kept`, `same_server`,
  `asked`), `public_ip6` and `warnings`. An address that is not public is
  used as given, with a warning; an IPv4-only move clears the backup's
  `hub.public_ip6` and says so (DR-08, docs/en/backup.md).
- `deyroute update` with no newer release installs nothing but tells the
  hub's nodes of another version to install the hub's version; `--json`
  adds `nodes_queued` (DR-11).

### Changed — restore without an address, nodes of another version

- `deyroute restore` of a hub backup on another server whose detected
  address is private or missing now stops with the new `DEY-I025` when it
  runs with `--yes` or without a terminal, before anything is restored;
  on a terminal it asks for the address (the menu too). Migration: scripts
  that restore a hub pass `--public-ip` (or `--keep-address` when the
  server owns the old address) (DR-08).
- A hub sends its version to a node of another major.minor as soon as the
  node connects, without a prior `deyroute update`, and after a restore
  from a backup of another version every node follows. A node newer than
  the hub is moved back to the hub's version (logged as a warning). A
  node that refuses the update is asked again only after a wait (10
  minutes, doubling up to 6 hours). A hub on a development build pushes
  nothing by itself (DR-11, docs/en/update-uninstall.md).

### Fixed — restored hubs

- A restored hub no longer keeps advertising the old address silently
  when this server's detected address is private, and the next step
  printed after a restore names the address in effect (DR-08).
- A restored hub on a newer release no longer leaves every node
  incompatible (`DEY-N004`) until an update that `deyroute update` could
  not start; the `DEY-N004` fix text says what happens now (DR-11).

### Added — echo probe

- Port probe kind `echo` (DR-17): a random nonce goes through one hidden
  port map of the active rung to a probe echo on the node and must come
  back, and the node must reach the service with a TCP connect.
  `deyroute port set <tunnel> <port> --probe echo`; `tunnel show --json`
  gives `probe_effective` and `probe_reason` on the probe port. Nodes
  announce `echo-probe`; a node without it keeps the auto probe and the
  dashboard warns. While a port sets `probe: echo`, a rollback below 0.3.1
  is refused (`DEY-S011`).

### Fixed — silent raw-TCP services

- A tunnel to a raw-TCP service that stays silent after a TLS hello
  (VMess over TCP without TLS, Shadowsocks-2022) failed the default auto
  probe on every rung and went DOWN although it worked (DR-17). The hub
  now detects such a target on the node (the TLS hello times out or is
  reset while a TCP connect succeeds) and uses the echo probe for it; a
  blocked rung is still detected and switched away from
  (docs/en/first-tunnel.md, docs/en/troubleshooting.md, lab S38).

### Fixed — port changes on NAT rungs

- `port add` and `port remove` on an active `wireguard/*` or `awg/*` rung
  now update the hub DNAT before the command returns: the new port carries
  traffic at once and a removed port loses its NAT rule. Before, the NAT
  rules followed only after a failover, `tunnel restart` or a hub restart
  (DR-10).
- The same change restarts both sides of the rung, and the hub side runs
  its start steps again (keys, address, link up). Before, only the hub
  unit restarted, its interface stayed down without an address, and the
  tunnel lost traffic.

### Added — join-token revoke, setup without a join window

- `deyroute node join-revoke` deletes every unused join token and applies
  the firewall at once, so the control port is restricted to the nodes
  again without waiting for the tokens to expire. JSON:
  `{"schema": 1, "ok": true, "revoked": N}`; event `join_tokens_revoked`
  (DR-16). Releases before 0.3.1 refuse that event name in
  `hub.notify.telegram.events`, so a rollback to them is refused
  (`DEY-S011`) while the list names it.
- `deyroute setup --no-join` sets a hub up without a join command and opens
  no join window at setup: the setup firewall restricts the control port to
  the nodes from the start. The JSON document then has no `join` field and
  `"join_window": false`; every hub setup document now carries
  `join_window` (DR-16, docs/en/join.md, docs/en/security.md, lab S03).

### Added — tunnel add --id, tunnel edit --node

- `deyroute tunnel add --id <id>` creates the tunnel with exactly that id;
  a taken id is `DEY-C002` and nothing is created, so a retried call
  never makes a second tunnel (invalid id: `DEY-C007`). Scripts must pass
  `--id`. `--allow-rename` keeps the numbered id (`t5-2`) for a taken
  `--name` without a terminal. The Local API request gains the additive
  field `no_rename` (docs/en/first-tunnel.md, docs/cli-json.md, DR-12).
- `deyroute tunnel edit <id> --node A --node B` sets the exact ordered node
  list, primary first. A new order moves no traffic: the active node stays
  active and the canary fails back to the new primary on its normal
  schedule. A list without the active node needs a typed `yes`, or `--yes`
  (new for `tunnel edit`); without a terminal it exits 3 and changes
  nothing (docs/en/backup-node.md, docs/cli-json.md, DR-13).

### Changed — a taken tunnel name fails for scripts

- `deyroute tunnel add --name N` without a terminal or with `--yes` now
  fails with `DEY-C002` (exit 1, checked before the ports, nothing
  created) when the id derived from the name is taken, instead of
  creating `N-2`. Migration: scripts pass `--id <id>`, or add
  `--allow-rename` to keep the numbered id; on a terminal without `--yes`
  nothing changes (QUESTIONS.md C.68, DR-12).

### Fixed — Hysteria2 port hopping scoped to the hub

- Hysteria2 port hopping no longer redirects every UDP datagram to ports
  20000–20999 of the node: the DNAT matches only the hub's public
  addresses (`hub.public_ip`, `hub.public_ip6`) as source and the node's
  public address as destination, so other UDP services of the node in
  that range keep their traffic. A node behind 1:1 NAT (its public IP is
  not one of its own addresses) keeps the source scope only and logs that
  once. A node gets the scoped rule once it runs this version (it
  announces `nat-scope`); older nodes keep the old rule (DR-18;
  docs/en/faq-filtering.md; lab S42).

### Security — sandboxed hub and node services

- The `deyroute-hub` and `deyroute-node` units run sandboxed (DR-20):
  `ProtectSystem=strict` with an explicit write list, a capability bounding
  set (`CAP_SYS_ADMIN`, `CAP_SYS_MODULE`, `CAP_SETUID`, `CAP_SETGID` and
  `CAP_KILL` are gone), `ProtectControlGroups`, `RestrictSUIDSGID`,
  `LockPersonality` and `SystemCallArchitectures=native`. The hub writes
  only its own paths; the node keeps `/etc`, `/var/lib` and `/var/log`
  writable because the uninstall the hub sends runs inside it.
  `systemd-analyze security` exposure: 8.9 to 6.5 (hub), 8.9 to 6.6
  (node). Existing servers get the new units when the installer runs again
  (or on join); `deyroute update` alone keeps the units in place
  (docs/en/security.md, QUESTIONS.md C.70).

## [0.3.0] - 2026-10-29

### Security — tunnel logs

- The hub and the node rotate the tunnel logs in `/var/log/deyroute/tunnels`
  as root. The rotation no longer follows symbolic links and never writes
  through a file name that the backends' user `deyroute` can create first:
  a `*.log` that is a link, not a regular file or has a second hard link is
  skipped, the copy is staged in the root-only `/var/lib/deyroute/tmp`
  (0700, created when needed), and anything else found at a name the
  rotation writes is removed. Each case is logged once per file and reason
  as the warning `tunnel log rotation refused` in `hub.log` or `node.log`
  (docs/en/security.md, lab S36).
- `/var/log/deyroute/tunnels` is created as `root:deyroute` with mode `1770`
  (sticky) by the installer, `deyroute setup` and the services, which
  created a missing one as `root:root 0750` before. The backends' user can
  still add files there but can no longer remove or rename root's live and
  rotated logs (QUESTIONS.md C.48).
- Migration: on a server installed before, `deyroute update` is enough.
  The hub and node services add the sticky bit to an existing
  `/var/log/deyroute/tunnels` that belongs to root when they start (they
  never remove a bit, and log `tunnel log directory made sticky`). Running
  the installer again (it repairs and keeps the configuration) or
  `chown root:deyroute /var/log/deyroute/tunnels && chmod 1770 /var/log/deyroute/tunnels`
  also works. Check it with `stat -c '%a %U:%G' /var/log/deyroute/tunnels`
  (`1770 root:deyroute`).
- Before the hub or a node starts a transport, and for every tunnel when
  the service starts, `<tunnel>.log` is made root's regular file with a
  single link: systemd opens it as root for the transport's output.
  Anything else found at that name is removed, an empty log (`0644`) is
  created, and the warning `tunnel log replaced` is logged once per file
  and reason (`symlink`, `not_regular`, `hardlink` or `owner`) in
  `hub.log` or `node.log`. To free space, empty a live tunnel log
  (`: > <tunnel>.log`); do not delete it (docs/en/security.md, lab S36,
  QUESTIONS.md C.56 and C.57).
- `deyroute logs` and the log lines shown with a failed start
  (`DEY-B003`) read a tunnel log only when it is a regular file. They no
  longer follow a link or wait on a FIFO at a log name: such a name shows
  no lines on the hub, and a node answers `DEY-X022`.
- A directory found at a name the rotation or the preparation of a tunnel
  log writes is renamed `<name>.removed-<16 hex digits>` at once and
  deleted in the background, at most 10000 entries per directory and
  minute, so neither the start of a transport nor the start of a service
  waits for it.
- The hub and the node prepare a tunnel log only in a safe
  `/var/log/deyroute/tunnels`: root's own directory with the sticky bit,
  or closed to its group and others. At a link, in a directory of another
  user or in a group-writable one without the sticky bit, the transport is
  not started and the hub or node answers `DEY-X022`, whose why names the
  reason; the services log `cannot make the tunnel log directory sticky`
  for such a directory. Fix:
  `chown root:deyroute /var/log/deyroute/tunnels && chmod 1770 /var/log/deyroute/tunnels`
  (the services make a root-owned directory sticky themselves when they
  start).
- `deyroute logs` and the log lines shown with a failed start (`DEY-B003`)
  no longer read a file with a second hard link at a log name
  (docs/en/security.md, lab S36, QUESTIONS.md C.56).

### Security — per-backend privilege allowlist

- A node now checks every backend unit the hub sends against a list of
  privileges built into its binary. Only `wireguard` and `awg` run as root,
  with `CAP_NET_ADMIN` and `AF_NETLINK`. `wireguard` may relax
  `ProtectKernelTunables`, and `awg` may also write to `/run/amneziawg`.
  `rathole` may relax `MemoryDenyWriteExecute`. The node side of
  `direct/haproxy` may open `AF_NETLINK`. Every other backend runs as user
  `deyroute` with `CAP_NET_BIND_SERVICE` only. No backend may relax
  `NoNewPrivileges` or the capability bounding set, or get a writable path
  its entry does not list.
- A unit that asks for more is refused: the node answers `DEY-N050` and names
  the field. The hub checks its own units against the same list before it
  writes them (`DEY-X034`). In both cases only that rung is skipped, with the
  code; the other rungs keep working. Every unit DEYROUTE's own backends
  render passes the check, so a working install does not change. The list
  is in `docs/en/security.md` (Tunnel processes).

### Security — nodes install only what a signed release proves

- A node no longer installs whatever its hub serves. When it follows the
  hub's version, the hub passes on the signed files of its release
  (`SHA256SUMS`, `SHA256SUMS.minisig`, the archive) and the node checks
  them with the release key built into its own binary: the signature, that
  the release is exactly the announced version, the checksum of the
  archive, that the archive holds a single regular file `deyroute` (at most
  200 MB, nothing outside its directory), and that this binary checks
  signed updates itself (`deyroute --json version` says
  `"signed_update": true`). Anything else is refused with `DEY-S001`;
  nothing is installed and the node keeps its version
  (docs/en/update-uninstall.md, "How the nodes follow"; lab S19, S41).
- A node installs a backend only in a version that a signed manifest pins:
  the version, download address and sha256 for its architecture must be
  those of the manifest built into it, or of a manifest the hub sends with
  a signature the node checks itself (the hub's override, or the manifest
  of the hub's own release when the node runs another build). Anything else
  is refused with `DEY-N050` before a download. A version already installed
  exactly as pinned is kept without a manifest (QUESTIONS.md C.62;
  "Backends on the nodes"; lab S20, S21).
- `deyroute update manifest` now keeps the signature as
  `/etc/deyroute/backends.yaml.minisig` next to the override; both are in
  backups. An override saved by an earlier build has no signature: the hub
  fetches a matching one at start and every 10 minutes, and until then its
  nodes install only the versions built into them (warning "the backend
  manifest override has no valid signature …"; fix:
  `deyroute update manifest`).
- The hub keeps the signed release of its version for its nodes in
  `/var/lib/deyroute/bin/<version>/`: `deyroute update` and the installer
  (also with `--local`) leave it there, and a hub without it downloads it
  the first time a node needs it (through a node first). The installer
  keeps it on every server it installs, nodes included (QUESTIONS.md
  C.60). `deyroute doctor` lists it as "signed release kept for the
  nodes".
- **Migration: update the hub first.** A node of this version refuses the
  update of a hub of an earlier build (`DEY-S001`, "update the hub first")
  and keeps running; nodes of earlier builds update from a new hub as
  before. After `deyroute update --version` or `--rollback` on the hub to
  an earlier build, the nodes keep their version. A hub on a development
  build has no signed release: its nodes refuse with `DEY-S012`.

### Added — signed updates

- `DEY-S012` "Hub runs an unsigned development build".
- `node.allow_unsigned_updates` in a node's `config.yaml` (default false,
  written only when true; for test labs only): that node takes the hub's
  binary without a signature, from a development build or from an earlier
  hub. It does not apply to backends. While it is set, the node refuses to
  install a release older than both itself and 0.3.0 (`DEY-S011`, every
  older `0.3.0-edge.N` included) (QUESTIONS.md C.58).
- `deyroute --json version` gains `"test_key"` (true only for a lab build
  that trusts a second, throwaway signing key; both release paths refuse to
  publish such a binary) and `"signed_update": true` (docs/cli-json.md).

### Changed — the automatic update is off by default

- **The automatic update is now off by default** (spec section 5: an update
  never runs without asking). Its switch moved from the state database,
  which no backup contains, to `config.yaml` as `hub.auto_update: true`
  (written only when on), so a backup keeps it and a restored hub has it
  again. `deyroute update auto on|off` sets it after an automatic backup,
  `deyroute --json update auto` adds `"source": "config"`, and
  `deyroute setup --auto-update on|off` (default `off`) chooses it at
  setup. A hub that ran an earlier build records the event
  `auto_update_default_off` once and shows "automatic update is now off by
  default; turn it on with: deyroute update auto on" on the dashboard for 7
  days while it is off. The switch is also still written where earlier
  builds read it, so a rollback to one of them keeps it as it was.
- **Migration:** a hub that relied on the old default no longer installs
  releases by itself; run `deyroute update auto on` once to keep that.
- While `hub.auto_update` is set, `deyroute update --rollback` and
  `deyroute update --version` to a release before 0.3.0 (every
  `0.3.0-edge.N` included) are refused with `DEY-S011`; run
  `deyroute update auto off` first. The rollback guard now compares each
  newer `config.yaml` key with the first release that knows it (`DEY-S011`
  names both), never refuses `--version` to a release that is not older
  than the running one, and trusts the recorded version of
  `deyroute.prev` only while that file is unchanged (after the installer
  or a rollback without the service replaced it, every newer key counts).
- A failed automatic update still goes back to the version it came from.
  Only when a setting added after the update is unknown to that version
  is the new version kept; the event `update_rolled_back` with the code
  `DEY-S011` and a dashboard line for 7 days say so.

### Fixed — the join command installs the hub's own release

- A hub on a release now prints a join command with the installer of that
  release:
  `bash <(curl -fsSL https://github.com/localroot4/DeyRoute/releases/download/v<ver>/install.sh) join '…' --version <ver>`
  (with `DEYROUTE_MIRROR`: `<mirror>/v<ver>/install.sh`). It used the newest
  installer (`…/releases/latest/download/install.sh`) with an older
  `--version`. A development build pins no version and keeps the `latest`
  installer. The JSON field `command` of `node join-command` keeps its name;
  only its text changes. Hubs on older builds print the old form until they
  are updated.
- Migration for mirror owners: keep the `v<ver>/` directory, with its
  `install.sh`, of every release your hubs run. A mirror that keeps only
  `latest/` answers the new join command with 404.

### Added — DEY-I024

- `install.sh --version <ver>` of a release that no download source has
  stops with `DEY-I024` "Release v{version} not found" (it was removed or
  never existed; install a version that exists, or use `--local` with a copy
  you saved) instead of `DEY-I004`, whose hint pointed to `--mirror` and
  `--local`. An HTTP 404 ends the tries of that source at once; any other
  failure keeps the three tries per source and still ends in `DEY-I004`.

### Changed — edge releases: kept longer, published after the whole ci run

- Retention keeps the 10 newest edge builds of each branch (`main` and `dev`
  counted apart), the release marked latest and every release whose tag has
  no `-edge.` (stable releases). It kept the 5 newest edge builds of both
  branches together, so a few pushes to `dev` removed the `main` build that
  hubs pin. A pinned edge build now stays installable until 10 newer builds
  of its branch exist; pin a stable release for longer.
- An edge release is published only after the whole ci run of a push to
  `main` or `dev` passed (lint, race tests, coverage, govulncheck, build,
  smoke and the integration lab, which now runs for `dev` too), from the
  commit that run tested. It appears 20 to 40 minutes after the push;
  before, it followed `go test` alone. Only the tip of the branch is
  published, never a commit that is already in the branch's newest edge
  release, and a manual run of release-edge publishes nothing. Edge version
  numbers have gaps.

### Security — HTTP/2 fixes

- `golang.org/x/net` is updated to v0.61.0 for the HTTP/2 advisories
  GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612 and
  GO-2026-6617 (flow control, SETTINGS frames, HPACK, trailers and framing
  headers), which reach the hub–node control channel. Release binaries are
  built with a current Go toolchain, which carries the same fixes for
  `net/http`. The module now needs Go 1.26 to build.

### Changed — the UDP-only ladder has no WireGuard rung

- The built-in `udp-default` ladder (tunnels whose ports are all UDP) is now
  `backhaul/udp` → `hysteria2/udp` → `direct/native`; `wireguard/kernel` left
  it. A filtering report from Iran of 2026-10-17 measured plain WireGuard
  blocked over IPv4 and IPv6 in the direction this rung uses (the hub dials
  the node). DEYROUTE's UDP test still passes on such a network, and a
  UDP-only tunnel is checked only by the state of its units, so the rung
  would be chosen and then cut (QUESTIONS.md C.45).
- `udp-default` is never written to `config.yaml`, so every UDP-only tunnel
  on it gets the new order with the update. A tunnel that runs on
  `wireguard/kernel` moves to the first usable rung of the new ladder after
  the update (its users reconnect once), and its WireGuard units are
  stopped and removed. Tunnels with a ladder of their own or an inline list
  keep `wireguard/kernel` as written.
- To keep WireGuard on a tunnel on purpose, give it an inline ladder:
  `deyroute tunnel edit <id> --ladder backhaul/udp,hysteria2/udp,wireguard/kernel,direct/native`
  (or create a ladder with `deyroute ladder create` for several tunnels).
  `wireguard/kernel` is now an optional transport, like `awg/userspace`: it
  is in no built-in ladder.

### Changed — failover against "passes, then cut"

- A quarantined rung that passes a probe keeps its quarantine duration for
  an hour instead of forgetting it. When it is cut again within that hour,
  its quarantine doubles (10, 20, 40, then 60 minutes with the default
  `quarantine_s` of 600) instead of starting at 10 minutes again. This is
  for filtering that lets a rung come up and then cuts it. `tunnel reset`
  and `tunnel switch` still clear the history of the rung they move to.
- Failback never goes to a rung 1 that is still quarantined (blind failback
  and canary). A rung 1 that fails its probes is quarantined when the tunnel
  leaves it, so failback now also waits for that quarantine: with the
  defaults the first failback after a cut comes at the earliest about
  10 minutes after the switch, even when the canary passes or
  `failback_after_s` (300 s) is over. `quarantine_s` sets this wait
  (QUESTIONS.md C.46). The canary keeps counting meanwhile, and the
  failback follows on the first probe after the quarantine ends.
- Lab S09, S10 and S13 set `quarantine_s` so that their failback timings
  still hold.

### Changed — test-ladder holds each rung

- `deyroute tunnel test-ladder` keeps every rung that passes its first probe
  running for the whole 20 seconds and probes it every `probe_interval_s`
  (5 s by default). A rung that passes and then fails is reported as failed
  with the new `DEY-B073` ("passed the probe, then failed after N s"). The
  RTT is the median of the passing probes. The test therefore takes about
  20 s per rung and node; the confirmation shows the total
  ("about 6 x 20 s").
- For a tunnel with only UDP ports the result ends with a note: the test
  checked only that the units of each rung run on the hub and the node, not
  that traffic passes.

### Changed — Telegram alerts go through a node first

- The hub now sends Telegram messages through an online node first (fastest
  control link first) and directly to api.telegram.org second; before, it
  tried directly first. api.telegram.org is usually filtered from Iranian
  datacenters, and the direct try could hold each alert for up to 20 s.
  With no node online (before the first join) the hub posts directly at
  once. The nodes get at most 40 s of the one-minute budget, each node in
  turn a share of it, so a node that hangs does not stop the next one or the
  direct post. `deyroute notify telegram test` uses the same order.
- The bot token is part of the Telegram API address, so it now goes to a
  node with every alert, not only when the direct post failed. It travels
  inside the node's control connection (mutual TLS), and the node keeps it
  out of its logs.

### Changed — decoy check

- The decoy check connects over the address family of `hub.public_ip`: over
  IPv4 when it is IPv4, so a dual-stack hub no longer tests a decoy over
  IPv6 and picks one that fails over IPv4. A hub whose `hub.public_ip` is
  IPv6 is unchanged.
- The check looks the name up first, as the hub's resolver answers it, and
  connects only to the public addresses it gets. When every answer is a
  private or reserved address (DNS tampering), the decoy fails without a
  connection, and the `DEY-B042` reason says, for example,
  `www.example.com: resolves to a private address from the hub (10.0.0.1; DNS tampering?)`.
  The next decoy in the list is still tried.

### Added — hint when a node switches IP family

- When a node's control connection moves to the other IP family (for example
  after `deyroute node set-hub` with the hub's IPv6 address on a hub whose
  `hub.public_ip` is IPv4), the `node_ip_changed` event and `hub.log` now say
  what it means: the transports in which the node dials the hub still go to
  `hub.public_ip`, and the hub firewall drops them, because it now allows
  only the node's new address; IPv6 between hub and node is not supported
  yet. The command that goes back comes early in the hint
  (`deyroute node set-hub <hub.public_ip>:<control port>`, on the node). When
  the node comes back to the family of `hub.public_ip`, the event says the
  firewall accepts those transports again. There is no hint when deyroute
  does not manage the firewall (`security.firewall_managed: false`) or when
  the node connects through the Cloudflare front.

### Fixed — rungs that left the ladder

- A skipped rung that is no longer planned (it left the ladder, or its node
  left the tunnel) no longer sends `rung_restored` ("back in the ladder")
  when the hub starts or applies a change; it is only logged.
  `rung_restored` stays for rungs that are still planned.
- After a hub restart (as in an update), a tunnel that ran on a rung that is
  no longer planned waits for its node to reconnect, at most 30 s, before it
  starts on the new ladder. Before, every candidate on that node failed with
  `DEY-N003` and the tunnel went DOWN (`tunnel_down`) until the retry 30 s
  later.

### Added — lab scenario S35

- Lab S35 measures failover against a filter that lets a flow pass and then
  cuts it (`lib.sh` `block --cut-after N`): time to the switch, states,
  switches, the client's outage, `test-ladder` with `DEY-B073`, and a
  UDP-only tunnel whose traffic is cut. It records numbers and asserts no
  reachability.

### Changed — documentation after the Iran filtering report

- The filtering FAQ has a section on the report of 2026-10-17 (what it
  measured and what it did not), describes `hysteria2/udp` correctly (QUIC
  with Salamander obfuscation on a backend control port, never 443), says
  what the UDP test proves and where the 35-second goal holds, and adds
  rows to the ladder guide. Join, hub move and front warn not to point a
  node at the hub's IPv6 address.

### Fixed — downloads that hang

- A download that receives no data for 2 minutes (a stalled connection,
  common towards GitHub from Iran) is given up and the next path or source
  is tried at once, instead of waiting up to 10 minutes per attempt
  (`deyroute update`, backend installs, the release check).

### Fixed — stale "node is offline" line

- A node that was busy during a tunnel sync (for example while installing an
  update) stayed marked as offline on the dashboard until it reconnected,
  although it was online. The hub now syncs such a node again within a
  minute, and the line goes away.

### Added — automatic update

- **Automatic update** (off by default, see "Changed — the automatic
  update is off by default"): once a day at 04:00 server time the hub
  installs the newest release that has been out for at least 24 hours,
  and the nodes follow. After the restart it checks that every tunnel that
  was UP and every node that was online works again within 10 minutes; if
  not, or if the new version does not start (counted before anything else
  loads), the previous binary is put back by itself (`DEY-S003` event and a
  dashboard line) and that release is never installed automatically again.
  `deyroute update auto` shows the state, the next release and when it is
  due, and the last result; `deyroute update auto off|on` turns it off and
  on. A manual `update --rollback` also skips the release you left.

### Changed — clearer status and tuning output

- `deyroute optimize status` is laid out like `status`: a line of labelled
  facts (profile, BBR, nodes in sync) with coloured marks, the server in a
  frame (cores, kernel, network, queue, and fill bars for memory and the
  connection table with the percentage in the bar's colour and used, free
  and total after it), the settings in effect as a table, every
  key and value in its own table per group with `--details`, and the nodes
  with their state and whether the tuning is applied or waiting.
- `deyroute status` opens with the name in large block letters and the
  version small under it, then one line of labelled facts (hub and address,
  mode, nodes online, tunnels UP): labels in gray, values in bold, the
  counts green when everything is up, yellow when part of it is, red when
  nothing is.
- Tables are drawn in boxes: every cell sits in a frame and the column
  headers are set apart in color (`deyroute status`, `stats`, `node list`,
  `tunnel list`, `optimize status`, …). They still fit the terminal, down
  to a phone; without UTF-8 the frame is drawn with `+ - |`.

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
