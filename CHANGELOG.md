# Changelog

All notable changes to DEYROUTE are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

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

### Fixed (found by the integration scenarios)

- `backend_crash` was not emitted when systemd restarted a killed backend.
- A node briefly reported itself incompatible before the hub's hello arrived.
- Kernel-setting warnings were printed twice by setup and join.
- Join-command `--ttl` outside 1m–24h now names the allowed range.

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
