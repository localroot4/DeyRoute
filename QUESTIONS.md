# QUESTIONS.md — ambiguities, contradictions and owner decisions

Rule 3 of the specification: when the spec is ambiguous or contradicts itself,
the question is recorded here with the default chosen for implementation.
Every entry stays **open** until the owner answers; answers are written under
the entry and the code follows the answer.

Legend: **[OPEN]** waiting for the owner · **[DEFAULT]** implemented with the
stated default until the owner decides · **[ANSWERED]** closed.

---

## A. Open questions from section 19 (answer before phase 2)

1. **[OPEN]** Do services on the nodes listen on `127.0.0.1` or `0.0.0.0`?
   Default implemented: target `127.0.0.1:<listen>`.
2. **[OPEN]** Do backup nodes always have the same panel/users (Marzban node or
   manual mirror)? Default: node failover is enabled and the UI shows the fixed
   warning "Backup only works if the same service runs on both nodes."
3. **[OPEN]** Is the real client IP needed in the panel? If yes, is PROXY
   protocol enabled on the node service? Default: no PROXY protocol; the UI marks
   each transport `client IP: preserved / masked`.
4. **[OPEN]** Expected number of nodes and tunnels in 12 months? Default sizing:
   probe worker pool 8, bbolt ≤ 50 MB, 64 port maps per tunnel.
5. **[OPEN]** Are all servers amd64, or is arm64 used too? Default: both are built.

## B. Owner-changeable defaults from section 19

| Item | Default in code | Decide by |
| --- | --- | --- |
| Installer URL / mirror (`RELEASE_BASE`) | **[DEFAULT]** GitHub only for now: `https://github.com/localroot4/DeyRoute/releases/latest/download/install.sh`; owner CDN can be set later in `RELEASE_BASE` | phase 1 |
| Domain for ACME | none (`tls.mode: auto`) | phase 3 |
| Decoy SNI list (Reality/Waterwall) | **[OPEN]** placeholder list in `internal/backend/decoy.go`; owner must supply 3 domains reachable from Iran | phase 6 |
| Default ladder | as in section 8 | phase 4 |
| Backend control port range | `30000-31999` | phase 2 |
| Hub `control_port` | `44433` | phase 2 |
| Telegram | off | phase 5 |

## C. Ambiguities found while implementing (defaults chosen)

1. **[DEFAULT] Repository / module name.** Section 15 names the repository
   `deyroute`; the actual repository is `localroot4/DeyRoute`. The Go module is
   `github.com/localroot4/deyroute`; the binary, paths, units and all user-visible
   names stay `deyroute` exactly as specified.
2. **[DEFAULT] Go version.** Spec: Go ≥ 1.22. `go.mod` declares `go 1.25.0`:
   the security fixes for `golang.org/x/net` (GO-2026-5026) and
   `golang.org/x/text` (GO-2026-5970), reachable through the ACME client,
   require Go 1.25, and Go 1.24 no longer receives security releases. CI
   and release builds use the current stable Go (1.26.x), so shipped
   binaries carry a patched standard library. `govulncheck` runs in CI.
3. **[DEFAULT] Star (`*`) items in the main menu.** "Advanced only adds starred
   items" is read as: the 13 top-level items always exist with fixed numbers;
   the starred *sub-items* (`thresholds`, `limits`, `view fingerprints`) appear
   only in Advanced mode.
4. **[DEFAULT] `deyroute-tun@.service` instance name.** Section 2 says the instance
   is `<tunnel_id>`, but sections 3/8 require one *warm* unit per (tunnel, node,
   transport) so that a switch is only stop+start. A single instance per tunnel
   cannot hold several warm transports. Implemented instance:
   `<tunnel>.<node>.<backend>-<transport>` (e.g. `main.de-1.backhaul-wssmux`),
   and `<tunnel>.canary` for the canary unit (section 9). The per-instance
   drop-in sets `StandardOutput=append:/var/log/deyroute/tunnels/<tunnel>.log`
   so logs stay per tunnel as specified.
5. **[DEFAULT] `DEY-Exxx` in section 6.** The UI rule "a red line with code
   `DEY-Exxx`" is read as a placeholder for *any* DEY code; no separate `E`
   category exists (section 13 lists the categories and has no `E`).
6. **[DEFAULT] Extra error codes.** Section 13 lists examples per category.
   Additional codes needed by the implementation were added in the same
   categories with Message/Why/Fix (see `docs/ERRORS.md`), e.g. `DEY-X003`
   (daemon not running, named in section 14), `DEY-C007` invalid id,
   `DEY-P011` reserved port, `DEY-X008` not implemented yet.
7. **[DEFAULT] nftables library.** Section 15 allows `google/nftables` with
   `nft -f -` as fallback. The implementation renders the `inet deyroute` table as
   text and applies it atomically with `nft -f -` (one transaction, golden-tested).
   This avoids a netlink dependency and gives identical behaviour on all
   distributions; `google/nftables` can be added later without changing callers.
8. **[DEFAULT] Hidden helper commands.** Besides section 14, the binary has hidden
   commands used by systemd units and tests: `deyroute daemon hub|node`
   (services), `deyroute relay --tunnel <id>` (the `direct/native` data plane named
   in section 7.8), `deyroute menu --once` (renders the first screen without a TTY
   for smoke tests / S24).
9. **[ANSWERED] Backend binary checksums.** Every sha256 in
   `internal/backend/backends.yaml` is now recorded (computed from the
   upstream files with `scripts/manifest-hashes`, checked against upstream
   checksum files where they exist; see C.29). A backend without a sha256
   would still be refused with `DEY-S006`.
10. **[DEFAULT] WireGuard key configuration.** Section 15 allows `exec` of `ip`
    only (not `wg`). The kernel WireGuard transport therefore configures keys and
    peers through the WireGuard generic-netlink API directly
    (`golang.org/x/sys/unix`), and links/addresses through `ip`.
11. **[DEFAULT] PKCS#12 for rathole/tls.** No PKCS#12 *encoder* is in the allowed
    library list. A minimal encoder (SHA-1/3DES PBE + SHA-1 MAC, the format
    OpenSSL/native-tls reads by default) is implemented in `internal/tlsutil`.
12. **[DEFAULT] Doctor output directory.** `deyroute-hub.service` uses
    `ProtectHome=true`, so `/root/deyroute-doctor-<UTC>.tar.gz` is written by the
    CLI process (which the owner runs as root), not by the daemon; the daemon only
    supplies data over the Local API.
13. **[DEFAULT] Phase tags.** Rule 4 asks for one PR and one tag per phase. In
    this delivery each phase is one Conventional-Commit series on the working
    branch; acceptance evidence that needs real servers (VM installs, 72-hour
    test, videos, real-user test) cannot be produced from the build sandbox and
    is listed per phase in `docs/en/acceptance.md` as **pending**.
14. **[DEFAULT] Config keys beyond the section 4 sample.** Section 4 says the
    sample lists *all* keys, but other sections require settings that have no
    key there: `failover.quarantine_s` (§9 defaults), per-port `probe`
    (auto|tcp|tls|http, §9), tunnel `probe_port` (§14 `--probe-port`),
    `tls.cert_file`/`tls.key_file` (custom mode, §10), hub `decoy_snis` (§7.4),
    `public_ip6` (§10 IPv6), `language` (§14), `mirror`, `update_check` (§5),
    `acme` (email / Cloudflare DNS-01 token file, §10) and per-tunnel
    `advanced:` (`connection_pool`, Hysteria2 bandwidth/port hopping,
    `proxy_protocol`, `backhaul_web_port`). They are all **optional** and
    omitted when empty, so the section 4 sample validates unchanged; any other
    unknown key is still `DEY-C001`.
15. **[DEFAULT] Exec allow-list.** Section 15 allows only `systemctl, nft, ss,
    ip, xray x25519, rathole --genkey`, but section 10 requires detecting and
    (after confirmation) running `ufw` / `firewall-cmd` / `iptables`, and the
    backend user `deyroute` must be created. The allow-list is therefore
    `systemctl, nft, ss, ip, xray, rathole, ufw, firewall-cmd, iptables,
    systemd-sysusers`. Key material for Rathole Noise and Xray Reality is
    generated in pure Go (X25519), so `xray x25519`/`rathole --genkey` are
    allowed but not needed.
16. **[DEFAULT] Secrets readable by backends.** Section 4 requires every file in
    `secrets/` to be 0600 root, while section 7 runs backends as user `deyroute`,
    which then cannot read the tunnel TLS key or token. Rendered backend
    directories (`/etc/deyroute/backends/<backend>/<tunnel>/…`) are `0750`
    and their files `0640`, owner `root:deyroute`, and contain the copies the
    process needs; `secrets/` itself stays 0700/0600 root.
    Paths are `/etc/deyroute/backends/<backend>/<tunnel>/<node>/<transport>/`
    (one directory per warm unit; section 2 shows only the first two levels).
17. **[DEFAULT] Last rung of the default ladder.** The section 4 YAML sample ends
    with `direct/haproxy`, section 8 says the default ladder ends with
    `direct/native` (and HAProxy is optional, phase 7). Section 8 is followed:
    the embedded default ladder ends with `direct/native`.
18. **[DEFAULT] Telegram from Iran.** `api.telegram.org` is usually unreachable
    from Iranian datacenters. The hub first posts directly; on failure it asks
    an online node to perform the same HTTPS POST (`http.post` command) — the
    same pattern as `fetch.proxy` for GitHub. No other data leaves the servers.
19. **[DEFAULT] Manifest file location.** `backends.yaml` is embedded from
    `internal/backend/backends.yaml` (the backend registry owns it); the
    release publishes and signs that same file.
20. **[DEFAULT] AmneziaWG binary.** `amneziawg-go` publishes no release
    binaries. DEYROUTE builds it reproducibly and publishes it on the GitHub
    release `backend-builds` (C.29); the manifest points there. The owner may
    mirror it and override the manifest.
21. **[DEFAULT] HAProxy.** HAProxy has no official static Linux binaries.
    `direct/haproxy` (optional, phase 7) uses the distribution's `haproxy`
    binary when present and is otherwise marked unavailable with DEY-B006.
22. **[DEFAULT] Extra packages.** `internal/version` (build metadata) and
    `internal/backend/all` (blank imports that register every backend) are
    small additions to the section 15 tree; `internal/exec` is named in
    section 15's coding rules. The `direct/native` relay data plane lives in
    `internal/backend/direct`.
23. **[DEFAULT] Control port access for joins and node IP changes.** Spec §3/§11
    open the control port only to joined nodes' IPs (`@nodes`), but a joining
    node's IP is unknown until it connects, and §11/S27 require a node whose IP
    changed to reconnect from the new IP. Implemented: while any unexpired join
    token exists the control port is open to every source (the join window);
    otherwise `@nodes` is accepted without limit and unknown sources get a
    rate-limited accept (`ct state new limit rate 6/minute`) before the drop.
    mTLS client certificates, the single-use 256-bit join tokens and the
    per-IP limit (5 failures/hour, then a 1-hour block) protect the port.
    Owner may choose strict mode (drop unknown sources; a moved node then needs
    `deyroute node set-hub` or a re-join).
24. **[DEFAULT] Reserved node id.** `canary` cannot be a node id (it names the
    canary unit directory and control-port keys of every tunnel; §9).
25. **[OPEN] hello NodeID vs certificate CN.** A node stream whose hello names a
    different id than its client certificate is accepted under the
    certificate CN (the CN is what the hub signed) and a warning is logged.
    Owner: refuse instead?
26. **[DEFAULT] Join limiter granularity.** Failures are counted per exact IP
    as §11 says; IPv6 sources can rotate inside a /64. Harmless in practice
    (256-bit tokens, bounded table); owner may ask for /64 counting.
27. **[DEFAULT] TLS trust for acme/custom tunnel certificates.** `ca.crt` given to
    clients holds the internal CA plus the served chain, so clients verify
    the public chain and host name. Pinning only the leaf would break
    OpenSSL-based clients without PARTIAL_CHAIN.
28. **[DEFAULT] Local toolchain vs lint.** golangci-lint v2.5.0 is built with Go
    1.25 and cannot analyse against the Go 1.26 standard library; CI lints
    with the go.mod version (1.25) and tests/builds with current stable Go.
29. **[DEFAULT] Backend binaries DEYROUTE builds itself.** Two pinned backends
    have no usable upstream binary: amneziawg-go publishes none (C.20), and
    chisel tagged v1.12.1 (the fix restricting reverse-UDP return peers, which
    `chisel/wss` uses for `/udp` maps) without release assets (v1.12.0 lacks
    the fix). `scripts/build-backends.sh` builds both from the tagged Go
    modules with a pinned toolchain exactly like upstream (CGO off,
    `-trimpath`, `-s -w`, chisel's BuildVersion); the output is byte-for-byte
    reproducible, and `.github/workflows/backend-builds.yml` publishes it on the
    prerelease `backend-builds` only when the hashes equal the manifest. Every
    other backend hash in `backends.yaml` was computed from the upstream file
    and, where upstream publishes checksums (frp, Xray, gost, chisel,
    hysteria), matches them (`go run ./scripts/manifest-hashes -check`). Owner
    may mirror these files and override the manifest instead.
30. **[DEFAULT] Uninstall removes the system account.** `deyroute uninstall`
    deletes the `deyroute` user and group the installer created (spec 5: the
    system is restored; scenario S23 compares accounts). `userdel` and
    `groupdel` are therefore on the exec allow-list (`internal/exec`); they are
    run with the fixed name `deyroute` only. Without them the step says so and
    the account stays.
31. **[DEFAULT] Rung 4 of the default ladder is `frp/tcp`.** Spec 8 names
    `frp/wss`, but frps v0.71.0 cannot accept wss without a separate TLS
    terminator (docs/backends/frp.md), so that rung would be skipped on every
    tunnel with a permanent yellow warning. `frp/tcp` keeps the intent of the
    rung (a third program with a different traffic pattern) and is a TLS
    session on the wire. `frp/wss` stays a known id for custom ladders.
32. **[DEFAULT] Join applies the hub's kernel profile.** `deyroute join` without
    a terminal (the one-line join command) applies the balanced profile the
    owner chose when setting up the hub, so hub and nodes match (spec 12);
    `optimize apply --profile off` on the hub reverts it everywhere. On a
    terminal it asks first.
33. **[DEFAULT] Changing the control port.** A changed `hub.control_port`
    (`config apply`) takes effect at the next hub restart: the online nodes
    are told the new address at once (as `hub announce-move`) and switch at
    the restart; offline nodes need `deyroute node set-hub`. Until the restart
    the firewall keeps protecting the port the hub listens on.
34. **[DEFAULT] Removing a tunnel's only node is refused.** `node remove`
    refuses (`DEY-C008`) while a tunnel has no other node; the owner gives it
    a backup node first or deletes it. Texts and docs say so.
35. **[DEFAULT] A port range counts one port map per port.** Spec 10 says more
    than 64 port maps is an error "with the advice to use a range", but every
    backend forwards port by port, so `2000-2100` becomes 101 port maps and a
    range cannot get around the limit. Supporting a range as one map would
    change the config schema and every backend renderer; instead the limit
    counts every port of a range and `DEY-P016`/`DEY-C015` advise splitting
    the ports over several tunnels (up to 64 each).

## D. Known limitations after the v1.0 audit (follow-up work)

The spec audit of 2026-10-01 found 100 gaps; the critical and major ones in
code are fixed (see CHANGELOG). These remain, none of them a security issue:

- **Smaller UX items** found by the audit (raw event ids in LAST EVENTS,
  column alignment on some screens, help on text-input screens, numbered
  answers for fixed options, the update changelog shown as a URL, doctor
  details) are listed in the audit record and do not change behaviour.
- **Lab coverage.** The integration lab cannot run a real Xray client, the
  WireGuard kernel module, or real provider firewalls; those parts of
  phases 4, 6 and 7 are covered by unit tests and need the 72-hour test on
  real servers (docs/en/acceptance.md).
