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
| Decoy SNI list (Reality/Waterwall) | **[DEFAULT]** `dl.google.com`, `www.speedtest.net`, `www.samsung.com` in `internal/backend/decoy.go` (each carries both Reality rungs, S18); the owner can set others in `hub.decoy_snis` | phase 6 |
| Default ladder | as in section 8, except rung 4 of `default` (C.31) and `udp-default` without `wireguard/kernel` (C.45) | phase 4 |
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
    unknown key is still `DEY-C001`. `backhaul_web_port` is refused by
    validation (`DEY-C013`): the pinned Backhaul v0.7.2 serves its stats page
    on every interface and has no bind-address key, so "stats only on
    127.0.0.1" (§7.1) cannot be honoured; earlier it silently skipped every
    Backhaul rung instead. A config.yaml from such an earlier build still
    starts the hub (the key is ignored with a warning in hub.log and dropped
    at the next change); `config validate`, `edit`, `apply` and `restore`
    refuse it.
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
    (filtered, DNS tampered) from Iranian datacenters. The hub first asks an
    online node to perform the HTTPS POST (`http.post` command, fastest
    control link first, at most 40 s for all nodes together, each node in
    turn a share of it) and posts directly second — the same order as
    `fetch.proxy` for GitHub downloads. With no node online (before the first
    join) it posts directly at once. The bot token is part of the API
    address, so it goes to a node with every message (inside the control
    connection; the node keeps it out of its logs). No other data leaves the
    servers. (Changed after the filtering report of 2026-10-17: the order was
    direct first, through a node second, and a direct post that waited for
    its 20 s timeout delayed every alert.)
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
36. **[DEFAULT] The join command pins the hub's release.** Spec 3 (Join 1)
    shows `bash <(curl -fsSL <installer>) join 'dey://…'`. A release build
    of the hub appends ` --version X.Y.Z` (its own version; a development
    build appends nothing), so the command is the spec's form plus that
    flag. The installer URL is the `latest` one: without the flag a node
    would install the newest release, and a hub on another major.minor
    could not use it (`DEY-N004`, no commands). The node cannot take the
    hub's binary instead, because `/v1/assets` needs the certificate it
    gets only by joining, and the hub sends `self.update` to its nodes only
    to finish its own `deyroute update`. `docs/en/join.md` and
    `docs/fa/join.md` show the flag and say why it is there.
    (Changed by C.52: a release build now uses the installer of the pinned
    release.)
37. **[DEFAULT] The integration lab's blocker and client (spec 17).** The
    spec's `blocker` is a separate container with `NET_ADMIN` on the same
    network; on a Docker bridge such a container is not on the path between
    hub and node and cannot drop their packets. `test/integration/lib.sh`
    `block`/`unblock` run the same nftables DROP rules in the hub's network
    namespace instead (input and output, established flows cut too). The
    `client` is a real Xray VLESS+ws+tls client (the Xray pinned in
    `backends.yaml`) that dials `hub:<port>`, with a real Xray server behind
    the tunnel on the node (`vpn_up`); curl uses it with `--proxy` and
    `--proxytunnel`, because Xray's plain-HTTP proxying fails short responses
    that the server closes at once. Offline (`DEY_OFFLINE=1`) Xray cannot be
    downloaded and the client requests the hub's port directly. S04's "user
    IP = hub IP" is checked as: the client talks only to the hub, the VPN
    service on the node never sees the client's address (it sees the tunnel:
    127.0.0.1 for the reverse transports, 10.77.n.1 for WireGuard) and the
    user's traffic leaves at the node.
38. **[DEFAULT] rathole and Waterwall on Tier 2 and minimal images.** The
    pinned rathole v0.5.0 amd64 asset needs glibc 2.34 and `libssl.so.3`; the
    Waterwall v1.46.94 assets need glibc 2.34. On Ubuntu 20.04, Debian 11 and
    Rocky/Alma 8 (Tier 2), or an image without OpenSSL 3, those units fail at
    start (`DEY-B003`, Waterwall `DEY-B043`, the loader's message in the
    detail), the rungs are quarantined and failover uses the other rungs:
    the tunnel works, rungs 3 and 7 of the default ladder do not. The arm64
    rathole asset has no TLS feature, so `rathole/tls` cannot start on arm64.
    A static (musl, rustls) rathole published on `backend-builds` would remove
    the first limit; it is not built yet.
39. **[DEFAULT] `frp/quic` and `frp/kcp` need UDP.** They carry the tunnel
    over UDP between node and hub, so they set `NeedsUDP` like Hysteria2 and
    WireGuard (spec 7.6 names only those): the UDP probe and DEY-B007 skip
    apply to them too.
40. **[DEFAULT] Waterwall reverse-reality graph.** The pinned Waterwall
    release has no reverse-reality example file in its repository
    (`tests/examples`, `tests/cases` at v1.46.94); the graph is assembled from
    the node documentation of that tag (docs/backends/waterwall.md lists the
    sources). Owner check: compare a rendered `config.json` with upstream's
    documentation page and report a difference.
41. **[DEFAULT] `awg/userspace` runtime directory.** The unit does not use
    systemd's `RuntimeDirectory=`; the backend's `PreStart` hook creates
    `/run/amneziawg`. When systemd restarts the unit on its own before that,
    amneziawg-go exits once and `Restart=always` brings it back within 2 s.
42. **[DEFAULT] Backhaul with TCP and UDP port maps.** The TCP-family Backhaul
    transports carry TCP only. In a tunnel with both, the rung runs two
    Backhaul processes in one unit through `deyroute pair`: the main process
    with the TCP maps and a `backhaul/udp` process with the UDP maps on a
    second control port (docs/backends/backhaul.md). Such a rung needs the
    UDP probe to pass, like the other UDP rungs.

43. **[DEFAULT] The control channel looks like ordinary HTTPS.** Section 11
    names ALPN `deyroute/1` for the control channel. On a real hub in Iran the
    node's stream to the hub was cut right after the TLS handshake (no data
    in either direction, the node reconnecting every 45 s), while ordinary
    HTTPS passed: a ClientHello with no server name and an unknown protocol
    in the clear is what the filter singles out. Nodes therefore offer ALPN
    `h2` and send a cover server name (`node.control_sni`, default
    `www.digikala.com`); TLS 1.3 encrypts the certificates, so nothing else
    of the handshake is visible. The hub accepts `h2` and `deyroute/1` (older
    nodes), and still verifies every node by its client certificate; nodes
    still verify the hub only against the pinned CA, never by that name.

44. **[DEFERRED] Measuring the rungs and preferring the fastest one.** The
    monitoring and tuning work planned a per-tunnel benchmark of the rungs
    (in the canary slot, at quiet hours, within a daily byte budget) and an
    opt-in `failover.prefer: fastest` that moves the fastest rung to the
    front of the ladder. It is not built. Adopting a faster rung goes through
    failback, which stops the active rung and makes every user reconnect,
    for a gain that is often small; and a preferred rung conflicts with the
    canary (which tests rung 1 of the primary node) and with the primary
    node itself, in ways that need a decision first. Until then the ladder
    order is the owner's: `deyroute tunnel test-ladder` compares the rungs
    (it interrupts the tunnel) and `deyroute ladder create` sets the order.

45. **[DEFAULT] `udp-default` has no `wireguard/kernel` rung.** Section 8
    lists `wireguard/kernel` as rung 3 of the UDP-only default ladder. A
    filtering report of 2026-10-17 (one datacenter in Iran to Hetzner in
    Germany, connections started from Iran) measured plain WireGuard
    blocked over IPv4 (cut after about 2 s) and over IPv6 (no answer), in
    the direction this rung uses (the hub dials the node). DEYROUTE cannot
    see this before it uses the rung: the UDP test (up to three small plain
    datagrams echoed by the node) passes on such a network, and a UDP-only
    tunnel has no TCP port to probe, so its rungs are checked only by the
    state of their units (hub and node unit active). So DEYROUTE would
    choose the rung, and the filter would then cut it. `udp-default` is
    therefore `backhaul/udp` → `hysteria2/udp` → `direct/native`, and
    `wireguard/kernel` is optional like `awg/userspace`; owners add it to a
    ladder of their own. This is the same kind of deviation as C.31. Open
    for later: a WireGuard-specific reachability check before the rung is
    added back.
46. **[DEFAULT] Failback waits for rung 1's quarantine.** Against filtering
    that lets a rung come up and then cuts it, a rung that passes a probe
    keeps its quarantine duration for an hour (a new cut doubles it: 10, 20,
    40, 60 minutes by default), and failback never goes to a rung 1 that is
    still quarantined (blind failback and canary). A rung 1 that fails its
    probes is quarantined when the tunnel leaves it, so failback comes at
    the earliest when that quarantine ends: at the later of
    `failback_after_s` (or the canary's `recover_threshold` passes) and the
    end of the quarantine, plus up to one probe interval. Spec section 16
    (phase 5 acceptance) and section 17 (S09) ask for failback ≤
    `failback_after_s` + 20 s after the block is lifted; that bound now
    holds only when `quarantine_s` ≤ `failback_after_s`. Under the defaults
    (300 s and 600 s) it does not: the first failback comes about 10
    minutes after the switch. This also departs from section 9 phase 5,
    which fails back once the tunnel has been stable UP for
    `failback_after_s`: failback now also waits for the quarantine. Lab
    S09 and S10 set `quarantine_s` to `failback_after_s`, S13 sets it to
    5 s.
    `tunnel reset` and `tunnel switch` still clear the history of the rung
    they move to. Owner decision: keep this, or reword the criterion.
47. **[DEFAULT] The automatic update is off by default; its switch is
    `hub.auto_update` in `config.yaml` (DR-05).** Spec section 5 says an
    update never runs without asking, but the automatic update shipped on
    by default with its switch in `state.db`, which no backup contains (a
    restored hub was silently on again). Now `hub.auto_update` (bool,
    default false, written only when true) decides: `update auto on|off`
    changes it through the normal config path with an automatic backup, and
    `setup --auto-update on|off` (default off) sets it at setup. The key is
    registered in the rollback guard (`FirstAutoUpdateVersion`, see C.49). A
    hub with a record of an earlier build is told once (event
    `auto_update_default_off`, a dashboard line for 7 days while off); a
    fresh install is not. Deviation from the brief ("ignore the old
    `disabled` field when reading; drop it on the next write"): the field is
    kept, written as `!hub.auto_update` on every write of the record and by
    `update auto on|off`, and never acted on. Earlier builds read it as the
    switch (on when absent), and `update --version` and `update --rollback`
    write the record right before restarting into one of them; dropping it
    would turn the automatic update back on there (section 2.2: new versions
    must not break a rollback).
48. **[DEFAULT] Tunnel log directory and rotation staging.**
    `/var/log/deyroute/tunnels` is now `root:deyroute` with mode `01770`
    (sticky) wherever it is created: the installer, `setup`,
    `InstallTemplates` and the hub and node `ensureTunnelLogDir`
    (`systemd.TunnelLogDirMode`; without the deyroute group the group bits
    are cleared, `01700`). Before, the installer and setup made it `0770`
    and the other three `root:root 0750`. The brief allowed `0770` or
    `01770`; the sticky bit keeps the backends' user from removing or
    renaming root's live and rotated logs, and group write stays. An
    existing directory keeps its mode and owner, as setup's other
    directories do: re-running the installer applies `1770` (`install -d`),
    `deyroute update` alone does not. Of the brief's two staging designs
    the rotation uses the root-only one: the copy is staged as
    `/var/lib/deyroute/tmp/tunnel-log-<log>.rotating` (`0700`, created on
    demand and refused unless private to root), so a leftover there is the
    rotator's own and is finished first. The brief's rule for a leftover
    `<log>.rotating` (finish root's regular file with one link, remove
    anything else) applies to the one an older version left in the log
    directory. Anything else at a name the rotation writes in the log
    directory is removed and reported; a directory is moved aside and
    deleted in the background (C.56). The
    "tunnel log rotation refused" warnings are remembered per file and
    reason up to 1024 pairs per daemon run; beyond that they are not
    logged, so planted names cannot grow the daemon's memory. The node
    agent now looks up the deyroute ids and changes ownership like the hub
    (`Options.DeyrouteIDs`, `Options.Chown`). DR-01.
    (Changed by C.56: the hub and node services add the sticky bit to an
    existing directory of root when they start, so `deyroute update` is
    enough.)
49. **[OPEN] `hub.auto_update` counts from 0.3.0, so edge builds are
    treated as not knowing it (DR-05).** The edge builds that carry the key
    cannot be told apart by version from those before it (every
    `0.3.0-edge.N` sorts before `0.3.0`), so `FirstAutoUpdateVersion` is the
    stable 0.3.0. While the key is set, `update --rollback` and
    `update --version` to any `0.3.0-edge.N` are refused with `DEY-S011`
    (fix: `update auto off` first). `update --version V` is never refused
    for keys when V is not older than the running build, which knows every
    key the running build writes. A failed automatic update still rolls back
    to the build it came from: `update-pending.json` records the keys in use
    when the update started (that build had loaded them), and only keys set
    after that are compared; a record without them (written by an earlier
    build) compares every key and keeps the new binary when one is unknown
    (event `update_rolled_back` with `DEY-S011`, a dashboard line for 7
    days). Owner decision: keep 0.3.0, or set the constant to the first
    edge build that carries the key once its number is known.
50. **[OPEN] Who sees the "now off by default" notice (DR-05).** The notice
    (one `auto_update_default_off` event, a dashboard line for 7 days) goes
    to every hub that has a state record of an earlier build, also one that
    had already turned the automatic update off there (`"disabled": true`):
    the brief keys the notice on the record alone, so the field is not used
    to tell the two apart. The dashboard line is hidden while
    `hub.auto_update` is on. Owner decision: skip the notice on hubs that
    had it off already.
51. **[OPEN] Rollback paths that do not check config keys (DR-05, section
    2.2).** `update --rollback` without the daemon (`rollbackLocal` in
    `internal/cli/system.go`, used when the service is down and on nodes)
    swaps the binaries without checking `config.yaml` keys, and so does the
    installer when it replaces the binary; the start guard of the automatic
    update (`AutoUpdateStartGuard`) rolls back before the config is loaded.
    The start guard goes back to the build the update came from, which had
    loaded the config, so only keys set by hand in between can break it;
    `rollbackLocal` and the installer can start a binary that refuses
    `config.yaml` (`DEY-C001`). The hub's own guard now trusts the recorded
    version of `deyroute.prev` only with a matching sha256
    (`update/previous-binary` in state.db) and otherwise refuses for every
    newer key. Not changed: `update --check` (`previous`) and the answer and
    event of `update --rollback` still name the plain recorded version
    (`update/previous`), which can be stale after the installer replaced
    `deyroute.prev`. Owner decision: add the key check to `rollbackLocal`
    (it can read `config.yaml` without the daemon) and show the checked
    version.
52. **[DEFAULT] The join command uses the installer of the pinned release.**
    C.36 chose the `latest` installer next to ` --version X.Y.Z`. That pairs
    the newest installer with an older release, and once a pinned edge build
    was pruned the line failed with `DEY-I004`, whose hint points to
    `--mirror` and `--local`. A release build of the hub now prints
    `bash <(curl -fsSL <github>/releases/download/vX.Y.Z/install.sh) join 'dey://…' --version X.Y.Z`,
    or `<mirror>/vX.Y.Z/install.sh` when `hub.mirror` is set; a development
    build pins nothing and keeps the `latest` installer (DR-02). This
    replaces the sentence of C.36 that names the `latest` installer; the
    rest of C.36 stands. A mirror must keep the `vX.Y.Z/` directory of every
    release its hubs run. The JSON field `command` keeps its name; the panel
    uses only `.link`.
53. **[OPEN] The join line exits 0 when the pinned release is gone.** With
    the versioned installer (C.52), a hub whose release is no longer
    published prints a line whose `curl -fsSL` fails (`curl: (22) The
    requested URL returned error: 404`); `bash <(…)` then runs an empty
    script and the line exits 0 without the installer's three-line error.
    DR-02 asks that the installer say so when the pinned version is missing;
    on this path the installer itself is the missing file, so it cannot.
    `install.sh --version X.Y.Z` run from any other installer does stop with
    `DEY-I024`. Today the behaviour is documented (`docs/en/join.md`,
    `docs/fa/join.md` and "Which versions stay available" in
    `update-uninstall.md`: update the hub and make a new join command), and
    the retention keeps a pinned edge build until 10 newer builds of its
    branch exist; stable releases are never removed. Options for the owner:
    (a) keep the join format of spec 3 (Join 1) and the documented
    behaviour; (b) change the format so the line fails loudly, for example
    `bash <(curl -fsSL URL || echo 'echo "✖ DEY-I024  Release vX.Y.Z not found" >&2; exit 1') join …`,
    which changes the spec's join format and the text of
    `api.JoinCommand.Command`. The panel is not affected: it never runs
    `.command`.
54. **[DEFAULT] Which green ci run publishes an edge build.** Under
    `workflow_run` (DR-06) the ci runs of a branch end in any order (they
    run in parallel, and an old run can be re-run), and an edge version is
    numbered by release-edge's own run, not by its commit. A green ci run
    therefore publishes its commit only while that commit is the tip of its
    branch and is not in the newest edge release of that branch
    (`scripts/edge-gate.sh`, checked when the ci run ends and again when the
    publishing job starts). The publishes of a branch run one at a time and
    never cancel each other, `main` and `dev` independently, and the
    retention runs one at a time. Consequences, by design: edge numbers
    have gaps (every run of release-edge counts, also one that publishes
    nothing); a re-run of a published commit publishes nothing; when the ci
    run of the tip fails, nothing new is published until a later push
    passes, even if an earlier commit was green; a push that starts no ci
    run (`[skip ci]`) holds back the publish of the green commit before it.
    Documented in `docs/dev/RELEASING.md`.
55. **[OPEN] Move the signing key into GitHub environments.** GitHub runs a
    manual run with the workflow file of the chosen ref. The tag
    `backend-builds` (fe816ca) and the tags `v0.3.0-edge.56` to `60` carry a
    `release-edge.yml` that, on `gh workflow run release-edge.yml --ref
    <that ref>`, builds, signs and publishes an edge release without the ci
    gate and then deletes every edge release but the 5 newest of both
    branches, pinned `main` builds included. The workflows now read the key
    in the environments `edge-release` (edge job of release-edge.yml) and
    `release` (release job of ci.yml), but the bypass closes only when the
    owner, in the repository settings, creates `edge-release` limited to the
    branch `main` (a workflow_run run has the ref of `main`, also for `dev`
    builds) and `release` limited to the tags `v*`, adds
    `MINISIGN_SECRET_KEY` and `MINISIGN_PASSWORD` to both, deletes the two
    repository secrets, and protects the `v*` tags with a ruleset (steps in
    `docs/dev/RELEASING.md`, "Signing key"). Until then anyone with write
    access can use this path. Owner action (DR-06, DR-02).
56. **[DEFAULT] The services prepare each tunnel log before systemd opens
    it (DR-01).** systemd opens a tunnel unit's `StandardOutput=append:`
    file as root before it drops privileges. It creates a missing file and
    follows a link, and `fs.protected_symlinks` does not cover the `01770`
    directory, which is not world-writable. So right before the hub starts
    a hub unit (`startHubSide`, and the restart of a changed hub unit) and
    before the node's `unit.start` and `unit.restart`, only their own
    regular file with one link may stand at `<tunnel>.log`. Both services
    also check this when they start: the hub for every tunnel in its
    configuration, the node for every tunnel it has instances of. This is
    needed because systemd's own restarts (`Restart=always`) never go
    through them, and a restarted hub adopts running units without
    starting them. Anything else at the name is removed, the same way the
    rotation removes what stands at its names: a link or a file is
    unlinked, never followed, and a directory is renamed
    `<name>.removed-<16 hex digits>` next to it, which frees the name at
    once whatever the directory holds. The rotation loop deletes what was
    renamed so in the tunnel log and the staging directory, at most 10000
    removals per directory and minute (about a quarter of a second of
    system calls). It empties a directory one level at a time and moves
    its subdirectories up, so any depth costs three descriptors and no
    path is built. A missing log is created with
    `O_CREAT|O_EXCL|O_NOFOLLOW` and mode `0644`. That is the mode systemd
    gives a file it creates (`0666` under `UMask=0022`), not the `0600` of
    rotated files, so a log looks the same whoever created it; the
    directory keeps other users out. Calls for the same path run one at a
    time within a process. Each removal is logged as the warning
    `tunnel log replaced` with `file` and `reason`. The reasons are
    `symlink`, `not_regular` and `hardlink`, as for the rotation, plus the
    new `owner`: a regular file with one link that belongs to another
    user, which that user could still remove in the sticky directory. The
    warning is logged once per file and reason per daemon run, with the
    same cap of 1024 pairs as C.48.
    Root's file stays root's only while no other user can remove or rename
    it before systemd opens the name. So the preparation first opens
    `tunnels/` with `O_DIRECTORY|O_NOFOLLOW` and requires a directory of
    root that has the sticky bit or no write bit for its group and others;
    otherwise nothing at the name is touched. A link at `tunnels/` is
    refused even when it leads to a good directory. If the check fails
    right before a unit starts, the unit is not started and the error is
    the existing `DEY-X022` ("Cannot write log file {path}"); no new code.
    When the directory is refused, the why names the reason (a link,
    another owner, the mode) and the fix is
    `chown root:deyroute <dir> && chmod 1770 <dir>`. If the check fails
    when a service starts, it is only logged
    (`cannot prepare a tunnel log`).
    When the services start, they also add the sticky bit, and only that
    bit, to an existing `/var/log/deyroute/tunnels` owned by root. They do
    this through a descriptor opened with `O_DIRECTORY|O_NOFOLLOW` and log
    `tunnel log directory made sticky` with `from` and `to`. They leave a
    link, a non-directory and a directory of another user alone, and log
    the warning `cannot make the tunnel log directory sticky` for it. This
    replaces C.48's "an existing directory keeps its mode":
    `deyroute update` is now enough on an older install.
    The readers are `deyroute logs` on the hub and the node (follow
    included) and the log lines of a failed start (`DEY-B003`). They open a
    log with `O_NOFOLLOW|O_NONBLOCK` and accept only a regular file with a
    single link, as the rotation, the preparation and `doctor collect` do.
    A link, a FIFO or a file with a second hard link at the name shows no
    lines on the hub and is `DEY-X022` for the node's `logs.tail`. That
    command takes any valid tunnel id, also one the node never starts and
    so never prepares; with `fs.protected_hardlinks=0` the `deyroute` user
    could otherwise hard-link a root-only file there for a hub to read.
57. **[OPEN] A tunnel log name can be free between starts (DR-01).** The
    preparation of C.56 runs only when the hub or the node starts a unit
    and when the services start. systemd also opens the log as root on its
    own restarts of a unit (`Restart=always`, `RestartSec=2`).
    a) While a live log name is free, the `deyroute` user can put a link
    there and end its own process, and systemd follows the link as root
    2 s later. The name is free when an admin deleted the log, or for the
    moment between the removal of a planted entry and the creation of
    root's file. The docs say to empty a live log (`: > file`) and never
    delete it, and that a service restart creates a deleted log again.
    There are two complete fixes. Each changes every creator of the
    directory (installer, setup, `InstallTemplates`, both
    `ensureTunnelLogDir`, `TightenTunnelLogDir`) and S36:
    (1) `tunnels/` without group write (`01750` or `0750 root:deyroute`).
    No backend writes there itself: every unit's output reaches its log
    through the descriptor systemd opens as root. The `deyroute` user then
    could add no names at all. The brief asks to verify
    `StandardOutput=append:` in the lab before group write is removed.
    (2) `01773` or `01777`. A sticky, world-writable directory makes
    `fs.protected_symlinks=1` (the default) refuse root's open of a link
    owned by `deyroute` with `EACCES`, so the unit fails with 209/STDOUT
    instead of writing through the link. `/var/log/deyroute`
    (`0750 root:deyroute`) still keeps other users out.
    b) A unit whose log was replaced when the services started keeps the
    descriptor it had until it restarts (an unlinked file, or the file a
    link pointed to if systemd had already followed it before the update).
    The services do not restart such units.
    c) (Resolved, see C.56.) A planted directory is no longer deleted
    where it is found. The preparation and the rotation rename it aside at
    once and the rotation loop deletes it in bounded steps, so neither a
    unit start nor a service start waits for it. Before, the preparation
    deleted it inline with everything in it: the services did this before
    they were ready, the node held its instance lock meanwhile, and a deep
    enough chain failed with `EMFILE` at `LimitNOFILE`. A backend that
    keeps adding entries now only keeps its own files on disk longer.
    d) (Resolved, see C.56.) The readers refuse `nlink > 1`, like the
    rotation.
    Owner decision: (1), (2), or keep the documented rule and restart
    affected units by hand.
58. **[DEFAULT] `node.allow_unsigned_updates`, a lab-only switch (DR-03).**
    New node key, bool, default false, written only when true, so a default
    install never blocks a rollback. With it, self.update takes the hub's
    binary without a signature as before DR-03: from a hub on a development
    build (otherwise `DEY-S012`) and, beyond the brief, from a hub of an
    earlier build that serves no signed release (otherwise `DEY-S001`), so a
    lab can mix builds. It does not apply to `backend.install`. The key is
    registered in the per-key rollback guard with
    `FirstAllowUnsignedUpdatesVersion` = 0.3.0, for the reason of C.49 (edge
    builds cannot be told apart by version): while it is set, the node
    refuses a self.update to a release older than both itself and 0.3.0
    (`DEY-S011`, every older `0.3.0-edge.N` included). Documented as
    lab-only in `update-uninstall.md` and `security.md`.
59. **[DEFAULT] A node installs only a release that checks signed updates
    itself (DR-03).** The brief's checks (signature, version in
    `SHA256SUMS`, archive sha256, safe extraction) accept any signed
    release, also one built before signed updates, whose node then takes
    any binary the hub serves; signatures do not expire. Before it installs,
    the node now runs the extracted binary's `deyroute --json version` and
    requires the new additive field `"signed_update": true`; otherwise
    `DEY-S001` and nothing is installed. This asks the build what it can do
    (brief section 2.2) instead of using a version floor, which would have
    to be 0.3.0 (edge builds sort before it) and would stop every node
    update between edge builds until then. Only `internal/exec` may start
    programs (spec section 15), so the run is `exec.ReleaseVersion`, a
    narrow path beside the config editor: exactly `<path> --json version`
    for an absolute path to a file named `deyroute`, in its own directory,
    with only `PATH` and `LC_ALL=C`, at most 15 s and 64 KiB of output; the
    runner's allow-list is unchanged (`docs/dev/ARCHITECTURE.md`). It runs
    as root, as the installer test-runs the binary it installs: the code is
    the release's own, verified against the release key, and as the
    backends' user `deyroute` a compromised backend could trace it and
    answer for it. An honest hub never meets the refusal: it names only its
    own version, and a hub rolled back to an earlier build announces no
    `signed-update` and is refused as an earlier hub.
60. **[DEFAULT] Where the signed release is kept (DR-03).** In
    `/var/lib/deyroute/bin/<version>/`, the hub's `AssetCacheDir`, beside
    the backend directories (backend names never contain a dot). The
    installer keeps the verified `SHA256SUMS`, `SHA256SUMS.minisig` and
    archive (and `backends.yaml` with its signature when they lie next to a
    `--local` archive and verify) on every server it installs, nodes
    included: it runs before setup or join decides the role (`--no-setup`
    is the panel's path), and `install.sh` has 249 of its 250 lines. On a
    node the copy (one archive) is never served; the next installer run
    replaces it. The installer removes the release directories of other
    versions; the hub at start removes those of versions it neither runs
    nor rolls back to (`deyroute.prev`). `deyroute doctor` lists them apart
    from the backends. Owner decision: keep, or drop the copy on nodes (for
    example in `join`).
61. **[DEFAULT] S19 updates through `deyroute update` and self.update; the
    installer phase is a same-version repair (DR-03).** The brief asks to
    add a hub-driven self.update step to S19. S19 upgrades from
    `DEY_OLD_VERSION` to `DEY_VERSION`, and a second upgrade would need a
    third lab build, so the installer-driven upgrade of both servers became
    `deyroute update --version` from a lab HTTPS mirror on the hub and
    self.update alone on the node. Re-running the installer under traffic
    stays as a second phase: with the prober still running,
    `install.sh --local` of the same release on the hub, then on the node
    (`setup --repair` restarts each service); no request may fail and the
    backend units keep their PIDs. Every lab install now checks the lab
    signature (`--skip-signature` is gone from `lib.sh` and the scenarios).
62. **[DEFAULT] A backend version already installed needs no manifest
    (DR-03).** The brief accepts a `backend.install` entry only when the
    node's built-in manifest or a signed one from the hub pins it. After
    `deyroute update` the hub keeps the backend versions running tunnels
    use until `deyroute update backends`, and the node's new built-in
    manifest may no longer pin them, so every node would refuse them after
    each hub restart. A node now accepts, without a manifest, an entry that
    is installed exactly as pinned (the directory's archive marker is the
    entry's sha256 and every binary matches its `.sha256` file,
    `Layout.HasBackend`); nothing is downloaded then. A new node cannot
    install such a version: the docs say to run `deyroute update backends`
    after an update that brings new backend versions.
63. **[OPEN] Older signed backend manifests (DR-03).** A node checks that a
    manifest is signed with the release key, not how old its release is,
    and signatures do not expire. A hub can therefore send the signed
    `backends.yaml` of any older release and make its nodes install an
    older backend version that release pinned, possibly one with a known
    flaw. The binaries are still upstream releases that DEYROUTE once
    pinned; the same holds for self.update to an older release that checks
    signed updates (C.59). Options: (a) keep this; (b) refuse a manifest
    that pins an older version than the node's built-in one (breaks a node
    on a newer build than its hub, which brief section 2.2 requires to
    work); (c) add a release sequence to the signed manifest and refuse
    manifests older than the node's build (only for releases after the
    change). Owner decision.
64. **[DEFAULT] A node's balanced and aggressive profiles reserve the
    control range (DR-09).** Until now only the profile `auto` added the
    backend control range `30000-31999` to
    `net.ipv4.ip_local_reserved_ports`; a scripted join applied `balanced`,
    which widens the ephemeral range to 10240-65535 and reserved nothing.
    Every profile a node applies except `off`, at join or pushed by the
    hub, now reserves the control range plus the entries of `join
    --reserve-ports`, as a set union with the live value. `join` keeps
    `balanced` as its default (`--sysctl` chooses), so a script gets
    today's profile plus a safe superset. The sysctl ledger records the
    entries deyroute requested, not the live union, so revert and uninstall
    remove only deyroute's ports. The hub's own `optimize apply --profile
    balanced` is unchanged (out of scope). Existing nodes reserve the range
    at the next profile the hub pushes; CHANGELOG names the re-apply.
65. **[OPEN] How a node opts out of the hub's tuning (DR-09, DR-19,
    DR-07).** The brief says a node joined with `off` skips hub-pushed
    `sysctl.apply` "until the operator runs `deyroute tune`", but no
    `deyroute tune` command exists. The switch is a root-only marker,
    `/var/lib/deyroute/hub-tuning-off` (0600, created with
    O_EXCL|O_NOFOLLOW, read without following links), written only when
    this version's join opts out explicitly (`--sysctl off`, or "no" to the
    kernel question). It is not keyed on `tuning.sysctl_profile: off`, so
    existing nodes and joins whose sysctl step failed keep following the
    hub. While it exists the node refuses `sysctl.apply` (balanced,
    aggressive, auto) and `tune.plan` with `DEY-N052` and a warning; `off`
    still runs. Lifting: `rm /var/lib/deyroute/hub-tuning-off`, then
    re-apply the hub's profile as `deyroute optimize status` shows it. The
    help of `setup --yes` therefore names `deyroute optimize status`
    instead of `deyroute tune`. Also: `setup --json` reports a third
    `public_ip_source` value, `wizard` (typed in the wizard), besides the
    brief's `flag` and `detected`; and an IPv6-only hub still cannot be set
    up non-interactively: `--public-ip` takes only IPv4, and with only
    `--public-ip6` the IPv4 address is still detected. Owner decision: a
    command or flag that lifts the marker, and whether `--public-ip` should
    accept IPv6 for an IPv6-only hub.
71. **[DEFAULT] Periodic re-assert of table inet deyroute (DR-14).** The
    hub and the nodes compare a fingerprint of `nft list table inet
    deyroute`, taken right after each apply, with the live table every
    `security.firewall_reassert_s` seconds (new key; absent = 60, 0 = off,
    10-3600, off with `security.firewall_managed: false`) and apply the
    table again when it is missing or changed. The fingerprint ignores the
    order of set elements and the elements of dynamic sets, which the
    kernel fills. The check holds the apply lock and acts only while the
    last apply succeeded; a failed apply stays reported as `DEY-P019` and
    is retried by the next change, not every minute. The hub emits
    `firewall_reapplied` (reason `missing|drift`) at most once per 10
    minutes per reason. A node only logs it: it has no event store, and
    sending it to the hub would need a protocol feature. A node reads the
    key at start (a change needs `systemctl restart deyroute-node`); the
    hub follows `config apply`. The rollback guard counts the key whenever
    it is present (an explicit 60 too: an older release refuses it all
    the same), and `firewall_reapplied` in `hub.notify.telegram.events`
    counts the same way; both have first version 0.3.1. The node's
    self.update guard now counts `security.*` keys as well as `node.*`.
72. **[DEFAULT] Per-source admission of unknown control-port sources
    (DR-15).** The single global `limit rate 6/minute` becomes a limit per
    source address (`security.unknown_control_rate_per_source`, default
    `3/minute`) through the dynamic sets `@ctl_unknown4/6` (size 65535,
    10-minute timeout), under a global cap
    (`security.unknown_control_rate`, default `30/minute`). This changes a
    live default, as brief section 2.2 lists. A per-source rate without a
    burst gets a burst of its own count (`3/minute burst 3 packets`, as in
    the brief's design), because nft's default burst of 5 would exceed the
    per-minute budget. The IPv6 rule is rendered even on a hub without
    IPv6, as the old global rule covered IPv6 too. Both keys are absent by
    default and each has its own first version 0.3.1 in the rollback
    guard.
73. **[DEFAULT] Fallback when nft refuses the per-source form (DR-15).**
    No nftables version is parsed. When applying the table fails and nft's
    message contains "syntax error" or "not supported" (an old kernel), the
    hub applies the earlier single rule `ct state new limit rate R accept`
    with R = `security.unknown_control_rate` (default 30/minute; the old
    6/minute default is not kept). It keeps that until the hub restarts and
    logs one warning. Any other failure, or a fallback that fails too, is
    reported as before and not cached. The per-source form works with nft
    1.0.9 on Linux 6.18 (checked); the other lab distributions ship
    nftables 1.0.2 or newer and are expected to work, which the lab matrix
    confirms.
66. **[OPEN] `restore --public-ip` accepts a private address (DR-08).**
    `deyroute restore --public-ip` uses a private, CGNAT or loopback
    address as given and reports it in `warnings` (JSON) and as a `!` line;
    an address typed at the terminal question is recorded as
    `address_source: "asked"` and treated the same way. DR-07
    (`setup --public-ip`) refuses such an address unless
    `--allow-private-ip` is also given. The restore keeps the explicit
    operator choice because a hub moved behind a provider NAT or floating
    IP may really be reached that way, and a refusal would leave no flag to
    override it. Options: (a) keep this; (b) align with DR-07: refuse with
    `DEY-C013` unless `--allow-private-ip` (a new restore flag). Owner
    decision.
67. **[DEFAULT] How a hub brings its nodes to its version (DR-11).** A
    hub on a release sends `self.update` to a node of another major.minor
    when it connects, without an update record, and then records the push
    like `deyroute update`, so every node follows; a node of the same
    major.minor without a record is left alone. A node newer than the hub
    is pushed back to the hub's version (warn log): the hub's version wins.
    A development build (`dev`) pushes nothing by itself (no signed
    release; signed-update nodes would refuse with `DEY-S012`). A refused
    or failed `self.update` (for example `DEY-S001`, `DEY-S012`, no signed
    release) is retried only after a wait of 10 minutes, doubling up to 6
    hours, kept in memory per hub run (a restart retries at once); the
    first failure is a warning, later ones debug. `deyroute update` with
    nothing newer calls the new local API `UpdateNodes`, which installs
    nothing and marks the nodes of another version: the CLI never calls
    `UpdateApply` there, so no release the owner did not see and confirm is
    installed. `setup.Restore` writes `/var/lib/deyroute/restored-from`
    (0600, hub backups only, the backup's version); the hub reads it once
    at start without following links, deletes it, and marks the nodes
    when the version differs from its own.
75. **[DEFAULT] Echo probe for silent raw-TCP targets (DR-17).** The
    spike against the pinned Xray v26.3.27 confirmed false probe failures:
    VMess tcp without TLS stays silent after the probe's TLS hello for
    about half of the UUIDs (Xray's drain size is seeded by its users), and
    Shadowsocks-2022 resets the connection. Default chosen as recommended
    by the brief: a new port probe kind `echo` (transport: a nonce through
    one hidden port map of the active rung, a hub port in the backend
    control range 30000-31999 forwarded to a nonce-only probe echo on the
    node's 127.0.0.1; service: the node's TCP connect to the target; both
    must pass), and `auto` switches to it for a node whose own TLS probe of
    the target times out or is reset while a TCP connect succeeds. The
    verdict changes only on a conclusive TLS result, is kept in state.db
    with whether the echo ran, and is asked again at most every 5 minutes
    while the tunnel fails. Only nodes announcing `api.FeatureEchoProbe`
    ("echo-probe") get it; older nodes keep the auto probe and a warning. A
    rung that cannot carry the map (direct/haproxy, a 65th map on
    direct/*, which checks config.MaxPortMaps) keeps the auto probe.
    Rejected: treating silence as success (a black-holed rung is silent
    too). `probe: echo` is registered in the rollback guard with
    FirstEchoProbeVersion 0.3.1.
76. **[OPEN] Exposure of the hidden echo port on NAT rungs (DR-17).** On
    a rung whose hub side forwards with DNAT (WireGuard/AmneziaWG), the
    hidden map is DNATed like a user port, so it is reachable from every
    source, as the canary's loopback port already is; the node's probe
    echo answers only a DEYE nonce with the same nonce. On WireGuard rungs
    its 127.0.0.1 target also turns on route_localnet for the tunnel
    interface on the node when no user target needed it. Default kept: no
    extra restriction. Owner decision: limit the hub's DNAT of the hidden
    port to local sources (a firewall change), or keep the echo probe off
    NAT rungs.

## D. Known limitations after the v1.0 audit (follow-up work)

The spec audit of 2026-10-01 found 100 gaps; the critical and major ones in
code are fixed (see CHANGELOG). These remain, none of them a security issue:

- **Lab coverage.** The lab now runs a real Xray VLESS+ws+tls client (S04,
  S08, S19, S32), the phase 4 manual switch (S32), awg/userspace (S33) and
  every Forward transport from inside (S17). `wireguard/kernel` did not run
  in the recorded lab: the kernel of the build host has no WireGuard module
  (`CONFIG_WIREGUARD` is not set; `ip link add type wireguard` fails even in
  the privileged containers, which share that kernel). S33 and S17 add it
  by themselves on a host whose kernel has it (the CI runners), so its first
  real run is the next CI `integration` run. Real provider firewalls, the
  resource budget on 1 vCPU / 1 GB servers and the 72-hour test need real
  servers (docs/en/acceptance.md, docs/en/benchmarks.md).
