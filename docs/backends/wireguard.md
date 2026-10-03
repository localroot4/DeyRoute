# WireGuard / AmneziaWG backends (`wireguard/kernel`, `awg/userspace`)

| | |
| --- | --- |
| Spec | section 7.7 (plus section 7 common rules, 11, 15, 16 phase 7; QUESTIONS.md C.10, C.20) |
| Package | `internal/backend/wireguard` — registers **two** backends: `wireguard` (transport `kernel`) and `awg` (transport `userspace`) |
| `wireguard/kernel` | the in-kernel WireGuard module (every Tier 1 distribution); nothing is downloaded — `Manifest()` reports a `System` entry with version `kernel` |
| `awg/userspace` | [amnezia-vpn/amneziawg-go](https://github.com/amnezia-vpn/amneziawg-go) **v1.0.4** (Go module version on proxy.golang.org = commit `69ca16c4fbfa`, 2025-07-04; the `v1.0.4` tag is no longer served by raw.githubusercontent.com, so the manifest links the [README of that commit](https://github.com/amnezia-vpn/amneziawg-go/blob/69ca16c4fbfa868834fd733f4cb38513d460aad9/README.md)) |
| Release assets | upstream publishes **no binaries**: `scripts/build-backends.sh` builds `amneziawg-go` statically and reproducibly from the pinned module version, and `.github/workflows/backend-builds.yml` publishes `amneziawg-go-v1.0.4-linux-{amd64,arm64}` (raw files) on the DEYROUTE release `backend-builds` after checking their sha256 against the manifest (QUESTIONS.md C.20, C.29). The `wireguard` block of `backends.yaml` describes this build; the `awg` backend installs it under its own name (`/var/lib/deyroute/bin/awg/v1.0.4/amneziawg-go`). |
| Reference (awg) | v1.0.4 sources: `main.go` (`-f/--foreground`, `WG_PROCESS_FOREGROUND`, `LOG_LEVEL`), `ipc/uapi_unix.go` (socket `/var/run/amneziawg/<iface>.sock`), `device/uapi.go` (UAPI keys `jc jmin jmax s1 s2 h1–h4`, also `i1–i5 j1–j3 itime` which are not used), `device/device.go` `handlePostConfig` (limits) |
| Reference (kernel) | `include/uapi/linux/wireguard.h` (generic netlink family `wireguard`, `WG_CMD_SET_DEVICE`); constants cross-checked with `golang.org/x/sys/unix` v0.41.0 |

## Transports

| Transport id | Direction | Carries | Needs UDP between hub and node | Stealth | Optional | Client IP |
| --- | --- | --- | --- | --- | --- | --- |
| `wireguard/kernel` | Forward | tcp, udp | **yes** (node listens on `<ctl>/udp`) | 1 | no (rung 3 of the UDP-only default ladder, section 8) | masked |
| `awg/userspace` | Forward | tcp, udp | **yes** | 3 | yes (never in a default ladder) | masked |

Both run as **root with `CAP_NET_ADMIN`** and `AF_NETLINK` (section 7 common
rules; the only backends that do). Neither consumes the tunnel TLS
certificate: each tunnel gets its own Curve25519 key pairs.

**The service on the node sees the hub's tunnel address (`10.77.<n>.1`), never
the client IP** (spec 7.7: must be announced in the UI; `ClientIPPreserved` is
false).

## Network layout

* Per tunnel `10.77.<NetIndex>.0/30`: hub `.1`, node `.2` (`NetIndex` 0–255,
  allocated by the hub). The canary unit uses the next `/30` of the same `/24`
  (hub `.5`, node `.6`) so it can run next to an active WireGuard rung.
* Interface name: `dey-<tunnel>` exactly as in the spec when it fits Linux's
  15-character limit (tunnel ids up to 11 characters). Longer ids become
  `dey-<prefix>_<NetIndex>` (the longest prefix that fits, at least 7
  characters; `_` never occurs in tunnel ids, so names cannot collide). The
  canary interface is `deyc-<NetIndex>`. The same name is used on both sides.
* MTU 1420, or `tuning.wg_mtu` (1280-1420) when set (`deyroute optimize auto`
  sets it to `min(1420, NIC MTU - 80)` when the NIC MTU is below 1500);
  `PersistentKeepalive = 25` on the hub's peer (the hub dials the
  node's public IP on `<ctl>/udp`; the node learns the hub's endpoint from the
  handshake). Allowed IPs are the peer's `/32` only.

## How each side is rendered

`Render` writes one file, `wg.json`, into the config directory
(`/etc/deyroute/backends/{wireguard,awg}/<tunnel>/<node>/<transport>/`, `0640
root:deyroute` — it holds the private key). Sample (hub, `awg/userspace`,
shortened):

```json
{
  "mode": "userspace",
  "side": "hub",
  "tunnel": "main",
  "interface": "dey-main",
  "address": "10.77.7.1/30",
  "mtu": 1420,
  "private_key": "AAIDBAUG…H2A=",
  "peer": {
    "public_key": "WGmv9FBU…pns=",
    "endpoint": "1.2.3.4:30001",
    "allowed_ips": ["10.77.7.2/32"],
    "persistent_keepalive": 25
  },
  "awg": {"jc": 5, "jmin": 50, "jmax": 1000, "s1": 40, "s2": 77,
          "h1": 1234567, "h2": 2345678, "h3": 3456789, "h4": 4567890}
}
```

The node side has its own address (`.2/30`), `listen_port` = the control
port, no endpoint/keepalive, the hub's public key, allowed IP `10.77.n.1/32`,
and `route_localnet: true` when a target is on `127.0.0.0/8`.

### Units and lifecycle (what the daemon must do)

| | `wireguard/kernel` | `awg/userspace` |
| --- | --- | --- |
| Unit type | `oneshot`, `RemainAfterExit=yes` | `simple` |
| ExecStart | `/usr/local/bin/deyroute wg up --config <dir>/wg.json` | `<bin>/amneziawg-go -f <iface>` (foreground, `LOG_LEVEL=error`) |
| ExecStop | `/usr/local/bin/deyroute wg down --config <dir>/wg.json` | — (the process exits on SIGTERM and removes its TUN interface) |
| Extra | node side with 127.0.0.1 targets drops `ProtectKernelTunables` (writes `net.ipv4.conf.<iface>.route_localnet`) | `ReadWritePaths=-/run/amneziawg` (UAPI socket directory under `ProtectSystem=strict`) |
| Before start | nothing | **`PreStart`** → creates `/run/amneziawg` (read-only under `ProtectSystem=strict` unless it exists) |
| After start | nothing | **`PostStart`** → `wireguard.Up` configures the device over the UAPI socket; run it **again whenever systemd restarted the unit** (`NRestarts`/`MainPID` changed): amneziawg-go keeps no configuration, a restarted process has an unconfigured device |
| After stop | `PostStop` → `wireguard.Down` (idempotent cleanup) | `PostStop` → `wireguard.Down` |

`UnitSpec` has no `ExecStartPost`, and `amneziawg-go` may only configure its
device after it created it, so the awg configuration is a post-start step run
by the daemon (both sides: the hub daemon for the hub unit, the node agent for
the node unit). The backend exposes it as methods, so the daemon can call it
without branching on the backend name:

```go
// both *wireguard.Backend values implement
PreStart(ctx context.Context, configDir string, r wireguard.Runner) error  // awg: create /run/amneziawg; kernel: no-op
PostStart(ctx context.Context, configDir string, r wireguard.Runner) error // awg: Up(<configDir>/wg.json); kernel: no-op
PostStop(ctx context.Context, configDir string, r wireguard.Runner) error  // Down(<configDir>/wg.json), idempotent
// wireguard.Runner is an alias of the unnamed interface
// interface{ Run(ctx, name string, args []string, stdin []byte) (stdout, stderr []byte, err error) }
// so internal/exec's Runner satisfies it and a structural type assertion works.
```

The CLI implements the hidden commands used by the kernel unit:
`deyroute wg up --config <path>` → `wireguard.Up(ctx, path, exec.NewRunner())`
and `deyroute wg down --config <path>` → `wireguard.Down(…)`.

What `Up` does:

* **kernel**: delete a stale interface of the same name, `ip link add dev
  <iface> type wireguard`, resolve the generic netlink family `wireguard`
  (`CTRL_CMD_GETFAMILY`) and send one `WG_CMD_SET_DEVICE` (`WGDEVICE_A_IFNAME`,
  `PRIVATE_KEY`, `LISTEN_PORT` on the node, `FLAGS=REPLACE_PEERS`, one nested
  peer with `PUBLIC_KEY`, `FLAGS=REPLACE_ALLOWEDIPS`, `ENDPOINT`
  (`sockaddr_in`/`sockaddr_in6`), `PERSISTENT_KEEPALIVE_INTERVAL`, nested
  `ALLOWEDIPS` with `FAMILY`/`IPADDR`/`CIDR_MASK`) — no `wg` binary
  (QUESTIONS.md C.10); then `ip address replace <addr> dev <iface>` and `ip
  link set dev <iface> mtu <mtu> up`. Any failure deletes the half-built
  interface (`DEY-B070`).
* **awg**: create `/var/run/amneziawg` if missing, wait up to 15 s for
  `/var/run/amneziawg/<iface>.sock`, send the UAPI `set=1` request
  (`private_key` hex, `listen_port`, `jc jmin jmax s1 s2 h1 h2 h3 h4`,
  `replace_peers=true`, `public_key`, `endpoint`,
  `persistent_keepalive_interval`, `replace_allowed_ips=true`, `allowed_ip`),
  require `errno=0`, then the same `ip address`/`ip link` calls (`DEY-B071`).
* both: on the node, write `1` to
  `/proc/sys/net/ipv4/conf/<iface>/route_localnet` when `route_localnet` is set.
* both: while the listen port is still in use, the step that binds it is
  repeated every 250 ms for up to 15 s: in kernel mode `ip link set ... up`
  (`RTNETLINK answers: Address already in use`; `WG_CMD_SET_DEVICE` on the
  down interface only stores the port), in awg mode the whole UAPI request
  (`errno=-98` from amneziawg-go). Right before a rung starts, the
  node agent's UDP reachability echo (`probe.udp_listen`) held the rung's
  control port for 10 s, and the first start of a WireGuard rung failed with
  `DEY-B070`/`DEY-B071` (found by lab scenario S33). The hub now closes the
  echo as soon as its probe is done (`probe.udp_listen` with `stop`); the
  retry stays for any other short-lived holder of the port.

`Down` runs `ip link del dev <iface>` when the interface exists (a missing
interface is not an error). Together with the NAT rules living only in
`table inet deyroute` (removed by the firewall package), a tunnel delete leaves
no interface and no rule behind (phase 7 acceptance).

`wg.json` problems are `DEY-B072`.

### Port forwarding (NAT) — `Rendered.NAT`, `Masquerade`, `IPForward`

Hub (spec 7.7): one DNAT per port map, `listen → 10.77.n.2:<target port>`
(tcp and udp), `Masquerade = [<iface>]`, `IPForward = true`. The user ports
are also reported as `Binds` (purpose `user`) for the conflict check even
though no socket is opened. The firewall package renders the DNAT in
`prerouting` (+ `output` for locally generated traffic, loopback excluded —
so the path probe must dial the hub's public IP, ARCHITECTURE 7.5) and the
masquerade/MSS clamp on the interface.

Node: one DNAT per distinct target, matched **only on the tunnel interface**:
`iifname "dey-main" … <proto> dport <target port> dnat to <target host>:<target
port>`. Nothing else: no masquerade, no `ip_forward`, and the hub peer may
only send from `10.77.n.1/32`. Because the NAT rules match on the tunnel
interface, the firewall package also confines it: its `forward` chain drops
whatever the node would route onward from `dey-main` (DNATed and established
flows pass), so a node whose `net.ipv4.ip_forward` is already on (a Docker
host, another VPN) does not route for the hub either. The node therefore
reaches exactly the configured targets and never routes for the hub (spec 11,
scenario S17, which routes 8.8.8.8 into the tunnel from a hub peer that
allows it; `TestNodeNotOpenProxy`). The node binds `<ctl>/udp` (purpose
`control`).

## Keys (`GenerateKeys`, pure Go)

| Key | Meaning |
| --- | --- |
| `hub_private`, `hub_public` | hub X25519 key pair (standard base64, private key clamped like `wg genkey`) |
| `node_private`, `node_public` | node X25519 key pair |
| `jc` (awg) | junk packets before each handshake, 3–10 |
| `jmin`, `jmax` (awg) | junk packet size range, `jmin` 40–80, `jmax` `jmin+1`–1280 |
| `s1`, `s2` (awg) | junk bytes before handshake initiation/response, 15–150, `s1+56 ≠ s2` |
| `h1`–`h4` (awg) | message type headers, distinct `uint32 ≥ 5` |

The limits are stricter than what amneziawg-go accepts (`jmax < 65535`,
`148+s1` and `92+s2` below 65535, `148+s1 ≠ 92+s2`, distinct headers `> 4`)
so every junk packet fits a 1280-byte path. `Validate` checks every key
(public matches private, parameter ranges).

## Differences between the spec text and the implementation

| Spec (7.7) | Implementation | Why |
| --- | --- | --- |
| masquerade on `dey-<tunnel>` | `dey-<tunnel>` when ≤ 15 characters, else `dey-<prefix>_<n>`; canary `deyc-<n>` | Linux interface names are limited to 15 characters; tunnel ids may have 32 |
| `wg`-style configuration | generic netlink (kernel) / UAPI socket (awg), driven by `deyroute wg up` | only `ip` may be executed (section 15, QUESTIONS.md C.10); no `wg`/`awg` tools |
| AWG parameters "Jc/Jmin/Jmax/S1/S2/H1–H4" | UAPI keys `jc jmin jmax s1 s2 h1 h2 h3 h4` | amneziawg-go v1.0.4 `device/uapi.go`; the AWG 1.5/2.0 keys `i1–i5`, `j1–j3`, `itime` are left unset |
| "`amneziawg-go` binary" | built reproducibly by `scripts/build-backends.sh`, published on the release `backend-builds` | upstream has no release binaries (QUESTIONS.md C.20) |
| forward to any `target` | targets must be `127.0.0.0/8`, `localhost` or the node's public IP (IPv4) | layer-3 forwarding to another host would need masquerading on the node's uplink (making the node a router for the hub); `DEY-B006` explains it |

## Known limitations

* Targets on other hosts, IPv6 targets and two port maps whose targets share a
  port number but differ in host are refused (`DEY-B006`).
* IPv6 clients of the hub are not forwarded (the DNAT and the tunnel subnet
  are IPv4); `listen` addresses on `::` still reserve the port.
* The node service sees `10.77.n.1`, never the client IP; PROXY protocol is
  not available on these transports.
* `awg/userspace`: if systemd starts the unit on its own (not through the
  daemon's `PreStart`) before `/run/amneziawg` exists, amneziawg-go exits
  once; `Up` creates the directory and systemd's `Restart=always` brings it
  back within 2 s (Up waits up to 15 s for the socket). A `RuntimeDirectory=`
  in `UnitSpec` would remove this (QUESTIONS.md C.41).
* `awg/userspace`: the device configuration lives only in the running
  amneziawg-go process; after a crash/restart of the unit the tunnel stays
  down until the daemon runs `PostStart` again (or the failover engine
  switches away and back).
* The canary's hub-side DNAT for its loopback port matches every local
  non-loopback address while the canary runs (the firewall's DNAT has no
  destination filter); the canary target is the same service as the tunnel's.
